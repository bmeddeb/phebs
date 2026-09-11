package lifecycle

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

type selectedCleanupTestOwner struct {
	name string
	work func(Limits) OwnerResult
}

func (owner selectedCleanupTestOwner) Name() string { return owner.name }
func (owner selectedCleanupTestOwner) Sweep(_ context.Context, _ time.Time, _ string, limits Limits) OwnerResult {
	return owner.work(limits)
}

func TestSelectedCleanupOwnerLimits(t *testing.T) {
	for _, test := range []struct {
		name, owner string
		selected    bool
		deleted     int
		refuse      bool
	}{
		{"observation-bound", ObservationV2Owner, true, 1024, false},
		{"observation-overshoot", ObservationV2Owner, true, 1025, true},
		{"search-bound", SearchOwner, true, 64, false},
		{"search-overshoot", SearchOwner, true, 65, true},
		{"relationship-bound", RelationshipV3Owner, true, 1024, false},
		{"relationship-overshoot", RelationshipV3Owner, true, 1025, true},
		{"store-bound", GenerationScheduleOwner, true, 16, false},
		{"store-overshoot", GenerationScheduleOwner, true, 17, true},
		{"ordinary-observation", ObservationV2Owner, false, 17, true},
		{"ordinary-search", SearchOwner, false, 17, true},
		{"ordinary-relationship", RelationshipV3Owner, false, 17, true},
		{"legacy-relationship", RelationshipOwner, true, 17, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := selectedCleanupTestOwner{name: test.owner, work: func(limits Limits) OwnerResult {
				want := DefaultLimits()
				if test.selected {
					want.Deletes = SelectedCleanupDeleteLimit(test.owner)
				}
				if limits != want {
					t.Fatalf("effective limits = %+v, want %+v", limits, want)
				}
				return OwnerResult{Scanned: 1, Deleted: test.deleted, Completeness: Exact}
			}}
			controller, err := NewController(newMemoryCursorStore(), owner)
			if err != nil {
				t.Fatal(err)
			}
			result := controller.tick(t.Context(), test.selected)
			if (result.Err != nil) != test.refuse || result.Deleted != test.deleted {
				t.Fatalf("actual owner result = %+v, want refusal %v", result, test.refuse)
			}
			constructor := NewCycleCollector
			if test.selected {
				constructor = NewSelectedCleanupCycleCollector
			}
			collector, err := constructor([]Owner{owner}, 1)
			if err != nil {
				t.Fatal(err)
			}
			done, err := collector.arm(false)
			if err != nil {
				t.Fatal(err)
			}
			result.Err = nil // Independently test collector's own cap boundary.
			result.Completeness = Exact
			result.AttemptedAt = time.Now().UTC()
			collector.ObserveOwner(result)
			collector.ObserveCapacity(Capacity{TotalBytes: 1000, AvailableBytes: 500, UsedBytes: 500,
				ProjectedBytes: 500, UsedPercent: 50, Pressure: PressureNormal}, nil)
			select {
			case value := <-done:
				if (value.err != nil) != test.refuse {
					t.Fatalf("collector = %+v, want refusal %v", value, test.refuse)
				}
			default:
				t.Fatal("collector did not complete or refuse")
			}
		})
	}
}

func TestSelectedCleanupStatusProfile(t *testing.T) {
	owners := []Owner{StaticOwner{OwnerName: ObservationV2Owner}, StaticOwner{OwnerName: SearchOwner}, StaticOwner{OwnerName: RelationshipV3Owner}, StaticOwner{OwnerName: GenerationScheduleOwner}}
	ordinary, err := NewStatusMonitor(true, owners)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := NewSelectedCleanupStatusMonitor(true, owners)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range owners {
		selected.ObserveOwner(OwnerResult{Owner: owner.Name(), Completeness: Exact, Scanned: 1,
			Deleted: SelectedCleanupDeleteLimit(owner.Name()), AttemptedAt: time.Now()})
	}
	status := selected.Snapshot()
	if ValidateSelectedCleanupStatus(status) != nil || ValidateStatus(status) == nil {
		t.Fatal("selected status profile crossed the ordinary boundary")
	}
	for index := range status.Owners {
		status.Owners[index].Deleted++
		if ValidateSelectedCleanupStatus(status) == nil {
			t.Fatalf("owner-specific overshoot accepted for %s", status.Owners[index].Name)
		}
		status.Owners[index].Deleted--
	}
	if ValidateStatus(ordinary.Snapshot()) != nil {
		t.Fatal("ordinary status changed")
	}
}

func TestSelectedCleanupControlledCadence(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				owner := selectedCleanupTestOwner{name: ObservationV2Owner, work: func(limits Limits) OwnerResult {
					calls++
					return OwnerResult{Scanned: 1, Deleted: limits.Deletes, Completeness: Exact, More: calls == 1}
				}}
				controller, err := NewController(newMemoryCursorStore(), owner)
				if err != nil {
					t.Fatal(err)
				}
				constructor := NewCycleCollector
				wantDelay, wantDeleted := DefaultBacklogDelay, 2*MaxDeletesPerTick
				if selected {
					constructor = NewSelectedCleanupCycleCollector
					wantDelay, wantDeleted = SelectedCleanupPendingDelay, 2*SelectedCleanupObservationDeletes
				}
				collector, err := constructor([]Owner{owner}, 2)
				if err != nil {
					t.Fatal(err)
				}
				gate := NewGateWithProbe("/unused", func(context.Context, string) (Capacity, error) {
					return Capacity{TotalBytes: 1000, AvailableBytes: 500, UsedBytes: 500}, nil
				})
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				state := runnerState{idleInterval: time.Hour, backlogDelay: DefaultBacklogDelay}
				start, due := time.Now(), time.Now()
				value, err := state.drive(ctx, &runnerCommand{ctx: ctx, operation: runnerDriveNormal, collector: collector}, &due, controller, gate, nil, nil)
				if err != nil || value.Deleted != uint64(wantDeleted) || time.Since(start) != wantDelay || calls != 2 {
					t.Fatalf("drive = %+v, %v, calls %d, duration %s", value, err, calls, time.Since(start))
				}
				if result := controller.Tick(ctx); result.Deleted != MaxDeletesPerTick {
					t.Fatal("selected profile leaked into ordinary Tick")
				}
			})
		})
	}
}
