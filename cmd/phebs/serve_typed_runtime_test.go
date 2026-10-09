package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedexecutor"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedServeDisabledAndRefusal(t *testing.T) {
	// Missing dependencies would panic if the absent installation did any work.
	d := &serveDeps{}
	if err := prepareServeTypedIndex(d); err != nil || d.typedRuntime != nil {
		t.Fatal("disabled preparation", err)
	}
	if err := startServeTypedIndex(d); err != nil {
		t.Fatal("disabled startup", err)
	}
	for _, mode := range []string{"missing-installation-fields", "exact-reads", "exact-reports", "ceremony", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			d := &serveDeps{ctx: t.Context(), typedInstallation: &typedServeInstallation{
				Bundle: func(context.Context, typedindex.Admission) (string, []byte, error) {
					calls++
					return "private-source", nil, nil
				},
			}, acquireLifecycleMutation: func(context.Context) (func(), error) {
				calls++
				return func() {}, nil
			}}
			switch mode {
			case "exact-reads":
				d.exactReads = true
			case "exact-reports":
				d.exactReports = true
			case "ceremony":
				d.semanticLaunch = &t422SemanticLaunch{}
			case "duplicate":
				d.typedRuntime = &typedServeRuntime{}
			}
			if err := prepareServeTypedIndex(d); !errors.Is(err, typedexecutor.ErrUnavailable) || calls != 0 {
				t.Fatal("refusal performed work", err, calls)
			}
		})
	}
}

func TestTypedServeConfiguredInstallationRefusesCeremonyBeforeReads(t *testing.T) {
	for _, mode := range []string{"exact-reads", "exact-reports", "ceremony"} {
		t.Run(mode, func(t *testing.T) {
			d := &serveDeps{ctx: t.Context(), cfg: &config.Config{ManagedSCIP: &config.ManagedSCIP{Manifest: "absent", SHA256: "invalid"}}}
			switch mode {
			case "exact-reads":
				d.exactReads = true
			case "exact-reports":
				d.exactReports = true
			case "ceremony":
				d.semanticLaunch = &t422SemanticLaunch{}
			}
			if err := prepareServeTypedIndex(d); !errors.Is(err, typedexecutor.ErrUnavailable) || d.typedInstallation != nil || d.typedRuntime != nil {
				t.Fatal("ceremony read installation", err)
			}
		})
	}
}

func TestTypedServeSchedulerSettlement(t *testing.T) {
	r := &typedServeRuntime{}
	chunk := store.GenerationChunk{Identity: "same-exact-lease"}
	budget := generationscheduler.TypedIndexBudget()
	handles, settlements := 0, 0
	private := errors.New("private build source /secret/workspace")
	settleErr := error(nil)
	scheduler := &generationscheduler.Scheduler{
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{
			store.GenerationResourceTypedIndex: {Concurrency: 1, Budget: budget,
				Handle: func(_ context.Context, got store.GenerationChunk, b generationscheduler.Budget) error {
					if got != chunk || b != budget {
						t.Fatal("changed exact lease or budget")
					}
					handles++
					return store.WithTerminal(private)
				},
				AfterSettlement: func(_ context.Context, got store.GenerationChunk) error {
					if got != chunk {
						t.Fatal("changed settlement lease")
					}
					settlements++
					return settleErr
				},
			},
		},
		HeartbeatEvery: 5 * time.Second, StaleAfter: 20 * time.Second,
	}
	r.bindScheduler(scheduler)
	class := scheduler.Classes[store.GenerationResourceTypedIndex]
	if class.Concurrency != 1 || class.Budget != budget || scheduler.HeartbeatEvery != 5*time.Second || scheduler.StaleAfter != 20*time.Second {
		t.Fatal("changed scheduler policy")
	}
	if err := class.Handle(t.Context(), chunk, budget); !store.IsTerminal(err) || !errors.Is(err, private) || strings.Contains(err.Error(), "private") {
		t.Fatal("handler privacy or classification", err)
	}
	if err := class.AfterSettlement(t.Context(), chunk); err != nil || r.pending.Load() {
		t.Fatal("healthy settlement scheduled census", err)
	}
	settleErr = private
	if err := class.AfterSettlement(t.Context(), chunk); !errors.Is(err, private) || !r.pending.Load() || strings.Contains(err.Error(), "private") {
		t.Fatal("ambiguous settlement lost", err)
	}
	if err := class.Handle(t.Context(), chunk, budget); !store.IsDeferral(err) || handles != 1 {
		t.Fatal("new work before reconciliation", err, handles)
	}
	// The repair turn consumes before recovery; a concurrent new failure must
	// survive a successful repair. It never clears the latch after recovery.
	r.recovering.Store(true)
	r.pending.Store(false)
	if err := class.Handle(t.Context(), chunk, budget); !store.IsDeferral(err) || handles != 1 {
		t.Fatal("work entered before repair acquired controller", err, handles)
	}
	if err := class.AfterSettlement(t.Context(), chunk); err == nil || !r.pending.Load() {
		t.Fatal("failure during repair lost", err)
	}
	r.recovering.Store(false)
	if err := class.Handle(t.Context(), chunk, budget); !store.IsDeferral(err) || handles != 1 {
		t.Fatal("repair lost a concurrent settlement failure", err, handles)
	}
	if settlements != 3 {
		t.Fatal("settlement callback replaced", settlements)
	}
}

func TestTypedServeErrorPrivacyAndLifecycle(t *testing.T) {
	private := errors.New("private source /secret/path")
	for _, tc := range []struct {
		err    error
		reason string
	}{
		{nil, "ok"}, {private, "failed"},
		{fmt.Errorf("private: %w", typedindex.Stale), "authority_changed"},
		{typedindex.Refusal("private-source"), "failed"},
		{store.WithDeferral(typedexecutor.ErrHeld), "custody_held"},
		{typedexecutor.ErrUnavailable, "unavailable"},
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "wall_limit"},
		{lifecycle.ErrPressureRefusal, "capacity_refused"},
	} {
		if got := typedServeReason(tc.err); got != tc.reason {
			t.Fatal(got, tc.reason)
		}
		wrapped := typedServeSafeError(tc.err)
		if tc.err == nil {
			if wrapped != nil {
				t.Fatal("nil changed")
			}
			continue
		}
		if !errors.Is(wrapped, tc.err) || strings.Contains(wrapped.Error(), "private") || store.DurableErrorText(wrapped) != wrapped.Error() {
			t.Fatal("unsafe durable error", wrapped)
		}
	}
	classified := store.WithTerminal(store.WithClass(store.ClassOOM, private))
	wrapped := typedServeSafeError(classified)
	if !store.IsTerminal(wrapped) || store.Classify(wrapped) != store.ClassOOM {
		t.Fatal("lost retry policy", wrapped)
	}
	r := &typedServeRuntime{owner: typedexecutor.LifecycleOwner{}}
	result := r.Sweep(t.Context(), time.Now(), "", lifecycle.DefaultLimits())
	if r.Name() != lifecycle.TypedIndexOwner || result.Completeness != lifecycle.Unavailable || result.Err == nil {
		t.Fatal("invalid lifecycle composition", result)
	}
	monitor, err := lifecycle.NewStatusMonitor(true, []lifecycle.Owner{r})
	if err != nil {
		t.Fatal(err)
	}
	monitor.ObserveOwner(result)
	status := monitor.Snapshot()
	if err := lifecycle.ValidateStatus(status); err != nil || len(status.Owners) != 1 || status.Owners[0].Name != lifecycle.TypedIndexOwner || status.Owners[0].State != "error" {
		t.Fatal("typed lifecycle status", status, err)
	}
}
