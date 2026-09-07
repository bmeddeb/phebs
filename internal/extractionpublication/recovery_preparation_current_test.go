package extractionpublication

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func currentRecoveryRequest(request RecoveryPreparationRequest) CurrentRecoveryPreparationRequest {
	return CurrentRecoveryPreparationRequest{
		Authority: request.Authority, GenerationDigest: request.GenerationDigest,
		Roots: request.Roots, Mode: request.Mode,
		TargetDomain: request.TargetDomain, TargetOrdinal: request.TargetOrdinal,
	}
}

func TestCurrentRecoveryPreparationBindsNativePredecessorWithoutExtraReads(t *testing.T) {
	for _, mode := range []string{RecoveryPreparationScheduleOnly, RecoveryPreparationCheckpoint} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
			// First create and finish a genuine predecessor-derived operational
			// schedule. Its identity is not the immutable target generation.
			prior, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request)
			if err != nil {
				t.Fatal(err)
			}
			for ordinal := range fixture.generation.WorkItems {
				if err := fixture.reconciler.Runtime.Handle(t.Context(), currentChunk(t, fixture.schedules.testScheduleStore, fixture.request.Authority.Repository, ordinal)); err != nil {
					t.Fatal(err)
				}
			}
			fixture.schedules.settle(fixture.request.Authority.Repository, 0)
			before := fixture.snapshot(t)
			if _, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request); !errors.Is(err, ErrStale) || !reflect.DeepEqual(before, fixture.snapshot(t)) {
				t.Fatalf("explicit stale predecessor was accepted: %v", err)
			}
			fixture.schedules.reads.Store(0)
			fixture.evidence.reads.Store(0)
			request := currentRecoveryRequest(fixture.request)
			request.Mode = mode
			reference := fixture.reconciler.CandidateReference
			confirmations := 0
			fixture.reconciler.CandidateReference = func(ctx context.Context, repository string) (candidate.State, error) {
				for _, lock := range []*sync.Mutex{&fixture.reconciler.mu[reconcileShard(repository)], fixture.reconciler.Runtime.assemblyLock(fixture.domain.Plan.Digest)} {
					if lock.TryLock() {
						lock.Unlock()
						t.Fatal("native confirmation escaped the live lock order")
					}
				}
				if !fixture.reconciler.Runtime.Fence.(*testFence).active() {
					t.Fatal("native confirmation escaped the publication fence")
				}
				confirmations++
				return reference(ctx, repository)
			}
			wantFiles := uint64(13)
			if mode == RecoveryPreparationCheckpoint {
				wantFiles++
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: wantFiles})
			if err != nil {
				t.Fatal(err)
			}
			target, prepareErr := fixture.reconciler.PrepareCurrentRecovery(ctx, request)
			counts, accountingErr := ledger.Finish()
			// Real control-file attempts; the scheduler/evidence doubles count
			// method calls, not SDK submissions or ordinary callback reads.
			if prepareErr != nil || accountingErr != nil || counts != (readaccounting.Counts{ControlFileReads: wantFiles}) ||
				fixture.schedules.reads.Load() != 4 || fixture.evidence.reads.Load() != 1 || confirmations != 3 {
				t.Fatalf("prepare=%v reads=%+v/%v schedule=%d evidence=%d", prepareErr, counts, accountingErr, fixture.schedules.reads.Load(), fixture.evidence.reads.Load())
			}
			if target.PriorScheduleDigest != prior.Digest || target.PriorScheduleDigest == fixture.request.PriorScheduleDigest ||
				target.TargetGeneration != fixture.generation.Digest || target.Schedule.Generation != recoveryGeneration(fixture.generation.Digest, prior.Digest) ||
				target.Domain != fixture.domain.Plan.Domain || target.Ordinal != 0 || target.Offset != fixture.generation.Domains[0].StartOrdinal ||
				target.PlanDigest != fixture.domain.Plan.Digest || target.ResultIdentity != fixture.root.Results[0].Identity ||
				store.ValidateGenerationSchedule(target.Schedule) != nil {
				t.Fatalf("native target = %+v", target)
			}
			after := fixture.snapshot(t)
			if after.enqueues != before.enqueues+1 || after.acquired != before.acquired || after.executions != before.executions ||
				!reflect.DeepEqual(after.publications, before.publications) {
				t.Fatal("current preparation repeated source/evidence work or enqueue")
			}
			assertCurrentRecoveryLocksReleased(t, fixture)
		})
	}
}

func TestCurrentRecoveryPreparationRefusesInvalidAuthorityAndCurrent(t *testing.T) {
	for _, name := range []string{"disabled", "nil_context", "canceled", "authority", "root", "target", "ordinal", "mode", "active", "failed", "changed_current", "late_authority", "canceled_reconfirmation"} {
		t.Run(name, func(t *testing.T) {
			fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
			request := currentRecoveryRequest(fixture.request)
			ctx := t.Context()
			var injected *recoveryPreparationSnapshot
			switch name {
			case "disabled":
				fixture.reconciler.RecoveryPreparationEnabled = false
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "authority":
				request.Authority.CandidateManifestDigest = digest("wrong-authority", nil)
			case "root":
				request.Roots[0].RootDigest = digest("wrong-root", nil)
			case "target":
				request.TargetDomain = "missing"
			case "ordinal":
				request.TargetOrdinal = len(fixture.domain.Plan.Expected)
			case "mode":
				request.Mode = "rewrite"
			case "active":
				key := scheduleKey(request.Authority.Repository, ScheduleStage)
				current := fixture.schedules.current[key]
				current.Status = store.GenerationScheduleActive
				fixture.schedules.current[key] = current
			case "failed":
				fixture.schedules.settle(request.Authority.Repository, 1)
			case "changed_current", "late_authority", "canceled_reconfirmation":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				read := fixture.reconciler.CandidateReference
				calls := 0
				fixture.reconciler.CandidateReference = func(ctx context.Context, repository string) (candidate.State, error) {
					value, err := read(ctx, repository)
					calls++
					if name == "changed_current" && calls == 2 {
						// A coherently bound, settled same-target successor is
						// still not the predecessor captured on confirmation one.
						if err := fixture.reconciler.Runtime.enqueue(ctx, fixture.generation); err != nil {
							return candidate.State{}, err
						}
						fixture.schedules.settle(repository, 0)
						snapshot := fixture.snapshot(t)
						injected = &snapshot
					}
					if name == "late_authority" && calls == 3 {
						value.ManifestDigest = digest("late-authority", nil)
					}
					if name == "canceled_reconfirmation" && calls == 2 {
						cancel()
					}
					return value, err
				}
			}
			before := fixture.snapshot(t)
			target, err := fixture.reconciler.PrepareCurrentRecovery(ctx, request)
			if err == nil || target != (RecoveryPreparationTarget{}) {
				t.Fatalf("invalid preparation returned target=%+v err=%v", target, err)
			}
			// This store double ignores context; it can return the second read
			// after cancellation. The preparation must still refuse mutation.
			if name == "canceled_reconfirmation" && (!errors.Is(err, context.Canceled) || fixture.schedules.reads.Load() != 2) {
				t.Fatalf("canceled reconfirmation = %v reads=%d", err, fixture.schedules.reads.Load())
			}
			after := fixture.snapshot(t)
			if name == "changed_current" {
				if injected == nil || !reflect.DeepEqual(*injected, after) || !errors.Is(err, ErrStale) {
					t.Fatal("preparation reselected a different coherent predecessor")
				}
				before = *injected
			}
			if name == "late_authority" {
				if after.enqueues != before.enqueues+1 || len(after.files) != len(before.files)+1 ||
					!reflect.DeepEqual(after.publications, before.publications) || after.acquired != before.acquired || after.executions != before.executions {
					t.Fatal("late refusal lost committed successor or repeated work")
				}
			} else if !reflect.DeepEqual(before, after) {
				t.Fatal("refusal mutated preparation state")
			}
			assertCurrentRecoveryLocksReleased(t, fixture)
		})
	}
}

func TestCurrentRecoveryPreparationCanceledLiveLockWait(t *testing.T) {
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
	lock := &fixture.reconciler.mu[reconcileShard(fixture.request.Authority.Repository)]
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	before := fixture.snapshot(t)
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := fixture.reconciler.PrepareCurrentRecovery(ctx, currentRecoveryRequest(fixture.request))
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("crossed held live lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled lock wait = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled preparation did not join")
	}
	if fixture.schedules.reads.Load() != 0 || fixture.evidence.reads.Load() != 0 || !reflect.DeepEqual(before, fixture.snapshot(t)) {
		t.Fatal("canceled lock wait performed native I/O or left work")
	}
}

func assertCurrentRecoveryLocksReleased(t *testing.T, fixture recoveryPreparationFixture) {
	t.Helper()
	for _, lock := range []*sync.Mutex{&fixture.reconciler.mu[reconcileShard(fixture.request.Authority.Repository)], fixture.reconciler.Runtime.assemblyLock(fixture.domain.Plan.Digest)} {
		if !lock.TryLock() {
			t.Fatal("preparation retained its live lock")
		}
		lock.Unlock()
	}
	if fixture.reconciler.Runtime.Fence.(*testFence).active() {
		t.Fatal("preparation retained publication fence")
	}
}
