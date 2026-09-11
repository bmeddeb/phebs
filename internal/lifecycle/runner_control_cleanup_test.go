package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// Real job/stage owner state machines, durable cursor API, controlled runner
// and actual empty filesystem; the job backend and capacity probe are modeled.
// Fake time removes pacing waits, not owner turns or their capacity callbacks.
func TestRunnerControlSelectedNormalSkewedCensuses(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_strict", true: "selected_truthful_backlog"}[selected], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				acquire := func(context.Context) (func(), error) { return func() {}, nil }
				owners := []Owner{
					JobOwnerImpl{Store: &memoryJobStore{}, Acquire: acquire},
					ExtractionStageOwner{Root: t.TempDir(), Acquire: acquire},
				}
				for index := range 14 {
					owners = append(owners, StaticOwner{OwnerName: fmt.Sprintf("static-%02d", index), Completeness: Exact})
				}
				runner := newControlledTestRunner(t, owners, nil)
				// Jobs finish on visits 1,17,33,...; the regular/sparse census
				// finishes on even visits. They cannot finish in one cycle.
				if err := runner.controller.store.CompareAndSwapLifecycleCursor(runner.ctx,
					"owner:"+JobOwner, 0, `{"kind":7,"phase":"count"}`); err != nil {
					t.Fatal(err)
				}
				if err := runner.owners.Pause(runner.ctx); err != nil {
					t.Fatal(err)
				}
				constructor := NewCycleCollector
				if selected {
					constructor = NewSelectedCleanupCycleCollector
				}
				collector, err := constructor(owners, MaxCycleObservationTurns)
				if err != nil {
					t.Fatal(err)
				}
				value, err := runner.control.DriveNormal(runner.ctx, collector)
				if !selected {
					if err == nil || errors.Is(err, context.DeadlineExceeded) || runner.probes.Load() != int64(MaxCycleObservationTurns) {
						t.Fatalf("ordinary skew did not exhaust the exact turn bound: %+v / %v, probes=%d", value, err, runner.probes.Load())
					}
					return
				}
				if err != nil || value.OwnerTurns != 32 || runner.probes.Load() != 32 || value.Deleted != 0 || len(value.Owners) != 16 || value.Capacity.Pressure != PressureNormal {
					t.Fatalf("selected skewed cycle=%+v / %v, probes=%d", value, err, runner.probes.Load())
				}
				for _, row := range value.Owners {
					if row.State != "ok" || (row.Name == JobOwner && (row.Completeness != LowerBound || !row.Backlog)) ||
						(row.Name != JobOwner && (row.Completeness != Exact || row.Backlog)) {
						t.Fatalf("truthful selected row changed: %+v", row)
					}
				}
			})
		})
	}
}

func TestRunnerControlSelectedNormalEvidenceBoundaries(t *testing.T) {
	for _, test := range []struct {
		name            string
		selected, fresh bool
		job, other      OwnerResult
		accept          bool
	}{
		{"selected_job_backlog", true, false, OwnerResult{Completeness: LowerBound, More: true}, OwnerResult{Completeness: Exact}, true},
		{"ordinary_job_backlog", false, false, OwnerResult{Completeness: LowerBound, More: true}, OwnerResult{Completeness: Exact}, false},
		{"ordinary_fresh_unchanged", false, true, OwnerResult{Completeness: LowerBound, More: true}, OwnerResult{Completeness: Exact}, true},
		{"selected_fresh_unchanged", true, true, OwnerResult{Completeness: LowerBound, More: true}, OwnerResult{Completeness: Exact}, true},
		{"job_error", true, false, OwnerResult{Completeness: LowerBound, More: true, Err: errors.New("job refused")}, OwnerResult{Completeness: Exact}, false},
		{"job_fabricated_exact", true, false, OwnerResult{Completeness: Exact}, OwnerResult{Completeness: Exact}, false},
		{"other_backlog", true, false, OwnerResult{Completeness: LowerBound, More: true}, OwnerResult{Completeness: Exact, More: true}, false},
		{"other_lower_bound", true, false, OwnerResult{Completeness: LowerBound, More: true}, OwnerResult{Completeness: LowerBound}, false},
		{"other_error", true, false, OwnerResult{Completeness: LowerBound, More: true}, OwnerResult{Completeness: Exact, Err: errors.New("other refused")}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				owners := []Owner{
					controlledTestOwner{name: JobOwner, work: func(context.Context) OwnerResult { result := test.job; result.Scanned = 1; return result }},
					controlledTestOwner{name: PartialStageOwner, work: func(context.Context) OwnerResult { result := test.other; result.Scanned = 1; return result }},
				}
				runner := newControlledTestRunner(t, owners, nil)
				if err := runner.owners.Pause(runner.ctx); err != nil {
					t.Fatal(err)
				}
				constructor := NewCycleCollector
				if test.selected {
					constructor = NewSelectedCleanupCycleCollector
				}
				collector, err := constructor(owners, 4)
				if err != nil {
					t.Fatal(err)
				}
				drive := runner.control.DriveNormal
				if test.fresh {
					drive = runner.control.DriveFresh
				}
				value, err := drive(runner.ctx, collector)
				wantTurns := int64(4)
				if test.accept {
					wantTurns = 2
				}
				if (err == nil) != test.accept || runner.probes.Load() != wantTurns {
					t.Fatalf("boundary=%+v / %v, probes=%d want=%d", value, err, runner.probes.Load(), wantTurns)
				}
			})
		})
	}
}

func TestRunnerControlSelectedProfileDoesNotRelaxAwaitNormal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owners := []Owner{StaticOwner{OwnerName: JobOwner}}
		collector, err := NewSelectedCleanupCycleCollector(owners, 1)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := collector.AwaitNormal(ctx); done <- err }()
		synctest.Wait()
		collector.ObserveOwner(OwnerResult{Owner: JobOwner, AttemptedAt: time.Now(), Scanned: 1,
			Completeness: LowerBound, More: true, CycleStart: true, CycleComplete: true})
		collector.ObserveCapacity(Capacity{TotalBytes: 1000, AvailableBytes: 500, UsedBytes: 500,
			ProjectedBytes: 500, UsedPercent: 50, Pressure: PressureNormal}, nil)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("selected AwaitNormal accepted backlog: %v", err)
		}
	})
}
