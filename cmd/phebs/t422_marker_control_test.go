package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/store"
)

// This is only the private two-barrier state. It cannot pass the live bootstrap
// checks or produce a native marker observation, and is not a graph fixture.
func t422MarkerTestBarriers(t *testing.T) (*t422MarkerControl, *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	failures := new(atomic.Int32)
	return &t422MarkerControl{
		ctx: ctx, cancel: cancel, stage: t422MarkerArmed,
		launch: &t422SemanticLaunch{fail: func(error) { failures.Add(1) }},
		hit: t422MarkerBarrier{
			ready: make(chan struct{}), release: make(chan struct{}),
		},
		recovered: t422MarkerBarrier{
			ready: make(chan struct{}), release: make(chan struct{}),
		},
	}, failures
}

func TestT422MarkerReportBarrier(t *testing.T) {
	for _, point := range []relationshippublication.PublicationTransitionPointV3{
		relationshippublication.PublicationTransitionHitV3,
		relationshippublication.PublicationTransitionRecoveredV3,
	} {
		for _, failReport := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", point, failReport), func(t *testing.T) {
				control, failures := t422MarkerTestBarriers(t)
				control.stage = t422MarkerHit
				if point == relationshippublication.PublicationTransitionRecoveredV3 {
					control.hit.reported = true
					control.stage, control.recoveryCommitted = t422MarkerRecovered, true
				}
				barrier, err := control.claimRead(point)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- control.awaitReport(ctx, barrier) }()
				// Claiming/reading the body alone cannot release the worker.
				select {
				case <-barrier.release:
					t.Fatal("worker released before report completion")
				default:
				}
				if failReport {
					_ = control.stop(errors.New("exact report sink refused"))
				} else if err := control.finishReport(barrier); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if (err != nil) != failReport {
						t.Fatal("report result changed", err)
					}
				case <-ctx.Done():
					t.Fatal("owned report waiter did not join")
				}
				if barrier.reported == failReport || failures.Load() != int32(map[bool]int{false: 0, true: 1}[failReport]) {
					t.Fatal("report completion/failure was invented")
				}
				if point == relationshippublication.PublicationTransitionRecoveredV3 && !control.recoveryCommitted {
					t.Fatal("report failure erased durable recovery")
				}
			})
		}
	}
}

func TestT422MarkerReadOrderingAndReplay(t *testing.T) {
	control, failures := t422MarkerTestBarriers(t)
	if _, err := control.claimRead(relationshippublication.PublicationTransitionRecoveredV3); err == nil {
		t.Fatal("recovered read preceded the reported hit")
	}
	if _, err := control.claimRead("arbitrary"); err == nil {
		t.Fatal("unknown native point accepted")
	}
	control.stage = t422MarkerHit
	barrier, err := control.claimRead(relationshippublication.PublicationTransitionHitV3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.claimRead(relationshippublication.PublicationTransitionHitV3); err == nil {
		t.Fatal("second pending reader was retained")
	}
	if err := control.finishReport(barrier); err != nil {
		t.Fatal(err)
	}
	if err := control.finishReport(barrier); err == nil || failures.Load() != 1 || !barrier.reported {
		t.Fatal("duplicate finish did not retain the positive report and fail closed")
	}
	_ = control.stop(store.ErrGenerationLeaseLost)
	if failures.Load() != 1 || !barrier.reported {
		t.Fatal("terminal failure re-reported or erased a positive prefix")
	}
}

func TestT422MarkerCancellationAndFailurePrefix(t *testing.T) {
	for _, failure := range []error{
		context.Canceled, store.ErrGenerationLeaseLost, errors.New("native recovery refused"), errors.New("Advance refused"),
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			control, failures := t422MarkerTestBarriers(t)
			control.hit.reported = true
			control.stage, control.recoveryCommitted = t422MarkerRecovered, true
			barrier, err := control.claimRead(relationshippublication.PublicationTransitionRecoveredV3)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- control.awaitReport(t.Context(), barrier) }()
			if err := control.stop(failure); !errors.Is(err, failure) {
				t.Fatal("native cause lost", err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("failed native continuation succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("terminal cancellation did not join its waiter")
			}
			if !control.hit.reported || !control.recoveryCommitted || control.recovered.reported || failures.Load() != 1 {
				t.Fatal("failure changed its positive prefix")
			}
		})
	}
}

func TestT422MarkerOperationContextPreservesReadOwnership(t *testing.T) {
	control, _ := t422MarkerTestBarriers(t)
	caller, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	reservation := dispatchadmission.ProductionSemanticSnapshot{ProducerID: 9, Phase: 6, RequestSequence: 2}
	caller = context.WithValue(caller, t422SemanticRequestKey{}, reservation)
	caller, ledger, err := readaccounting.Start(caller, readaccounting.Counts{ControlFileReads: 5})
	if err != nil {
		t.Fatal(err)
	}
	operation, finish := control.operationContext(caller)
	if operation.Value(t422SemanticRequestKey{}) != reservation {
		t.Fatal("native read lost its authenticated request reservation")
	}
	wantDeadline, _ := caller.Deadline()
	if deadline, ok := operation.Deadline(); !ok || !deadline.Equal(wantDeadline) {
		t.Fatal("operation extended the caller deadline")
	}
	if err := readaccounting.Charge(operation, readaccounting.ControlFileRead, 1); err != nil {
		t.Fatal(err)
	}
	if counts, err := ledger.Finish(); err != nil || counts.ControlFileReads != 1 {
		t.Fatal("native read escaped its original ledger", counts, err)
	}
	control.cancel()
	select {
	case <-operation.Done():
	case <-time.After(time.Second):
		t.Fatal("shared failure did not cancel native work")
	}
	// Concurrent/duplicate after-report cleanup still joins only the one
	// cancellation callback; a stopped registration cannot become an empty wait.
	var joined sync.WaitGroup
	for range 4 {
		joined.Go(finish)
	}
	joined.Wait()
}

func TestT422MarkerOperationContextBoundsAndPrecancel(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		control, _ := t422MarkerTestBarriers(t)
		caller, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		before := time.Now()
		operation, finish := control.operationContext(caller)
		deadline, ok := operation.Deadline()
		if !ok || deadline.Before(before) || deadline.After(time.Now().Add(t422MarkerMaximum)) ||
			canceled && operation.Err() == nil {
			t.Fatal("unbounded or uncanceled operation")
		}
		cancel()
		finish()
		finish()
	}
}

func TestT422OnlyMarkerContinuation(t *testing.T) {
	continuation := errors.New("private continuation")
	other := errors.New("native cleanup failed")
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"direct", continuation, true},
		{"native-wrap", fmt.Errorf("native publish: %w", continuation), true},
		{"deferred-single-join", errors.Join(fmt.Errorf("native: %w", continuation)), true},
		{"missing-hook", nil, false},
		{"other-instance", errors.New("private continuation"), false},
		{"cleanup-error", errors.Join(continuation, other), false},
		{"nested-cleanup-error", fmt.Errorf("native: %w", errors.Join(continuation, other)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if t422OnlyMarkerContinuation(test.err, continuation) != test.want {
				t.Fatal("continuation swallowed a native failure")
			}
		})
	}
}

func TestT422MarkerUnadmittedMethodsRefuse(t *testing.T) {
	for _, method := range []string{"hit", "recovered", "handle"} {
		t.Run(method, func(t *testing.T) {
			control, failures := t422MarkerTestBarriers(t)
			var err error
			if method == "handle" {
				err = control.HandleV3(t.Context(), store.GenerationChunk{})
			} else {
				var body []byte
				var finish func(error)
				if method == "hit" {
					body, finish, err = control.ReadHit(t.Context())
				} else {
					body, finish, err = control.ReadRecovered(t.Context())
				}
				if body != nil || finish != nil {
					t.Fatal("unadmitted request obtained body or release capability")
				}
			}
			if err == nil || failures.Load() != 1 || control.stage != t422MarkerArmed {
				t.Fatal("unadmitted method reached native work")
			}
		})
	}
}

func TestT422MarkerConstructorRefusesWithoutLiveBinding(t *testing.T) {
	state := &store.Surreal{} // no engine; it must never be queried
	repository := "local/tmp/marker"
	launch := &t422SemanticLaunch{
		request: t422SemanticLaunchRequest{ServerEpoch: 3, Repository: repository},
		initial: dispatchadmission.ProductionSemanticSnapshot{Phase: 6}, fail: func(error) {},
	}
	runtime := &relationshippublication.Runtime{DataDir: t.TempDir(), Store: state,
		Acquire: func(context.Context) (func(), error) { t.Fatal("constructor took a native lock"); return nil, nil }}
	services := &serviceRuntimeController{
		dataDir: runtime.DataDir, store: state, relationship: runtime, acquire: runtime.Acquire,
		v3Catalog:  &servicecatalogingest.V3Reconciler{},
		selections: map[string]config.ServiceCatalog{repository: {Runtime: config.ServiceCatalogRuntimeV3}},
	}
	for _, ctx := range []context.Context{nil, t.Context()} {
		if control, err := newT422MarkerControl(ctx, launch, runtime, services, runtime.Acquire); err == nil || control != nil || runtime.AfterV3MarkerInstall != nil {
			t.Fatal("constructor manufactured bootstrap authority or installed a partial hook")
		}
	}
}

func TestT422MarkerNativeRecoveryRefusalReleasesFence(t *testing.T) {
	control, _ := t422MarkerTestBarriers(t)
	control.runtime = &relationshippublication.Runtime{DataDir: t.TempDir()}
	root := filepath.Join(control.runtime.DataDir, "index")
	control.exclusive = func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireExclusiveMutationLock(ctx, root)
	}
	// Call the real selected-recovery method with no marker admission. It must
	// refuse before writes and still release the actual exclusive lock. This
	// deliberately does not model a successful native publication or Advance.
	if err := control.recoverSelected(t.Context()); err == nil || control.recoveryCommitted {
		t.Fatal("invalid native recovery became durable success")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := focusedindex.AcquireMutationLock(ctx, root)
	if err != nil {
		t.Fatal("exclusive recovery fence remained held", err)
	}
	release()
}
