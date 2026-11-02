package extractionpublication_test

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

// Only new test-owned persistent databases and real production extraction,
// scheduler, reaper and exact runtime reader are used. This is not a diagnosis
// of the retained V20 database or native custody/accounting/RSS acceptance.
type v20PointerChangingStore struct {
	*store.Surreal
	prior   store.GenerationSchedule
	calls   int
	changed bool
}

func (state *v20PointerChangingStore) ReadGenerationStaleLeaseTransition(ctx context.Context, request store.GenerationStaleLeaseTransitionRequest) (store.GenerationStaleLeaseTransition, error) {
	state.calls++
	value, err := state.Surreal.ReadGenerationStaleLeaseTransition(ctx, request)
	if state.calls == 1 && err == nil {
		if err := state.RetireCurrentGenerationSchedule(ctx, state.prior); err != nil {
			return store.GenerationStaleLeaseTransition{}, err
		}
		state.changed = true
	}
	return value, err
}

func TestV20CheckpointPersistentMultidomainReader(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	for _, mode := range []string{"ordinary", "pointer_change"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			fixture := newRecoveryPreparationFixtureDomainOptions(t, ctx, 3, true, 27, 26)
			generation, err := fixture.reconciler.Reconcile(ctx, fixture.repository)
			if err != nil {
				t.Fatal(err)
			}
			initial := fixture.schedule(t, ctx)
			if initial.TotalChunks != 56 || initial.RepositoryTokens != 1 {
				t.Fatal("fixture is not fifty-six genuine multidomain parts with one repository token", initial.TotalChunks)
			}
			if _, err := fixture.state.ExpandGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage, initial.Generation); err != nil {
				t.Fatal(err)
			}
			for ordinal := range 56 {
				chunk, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "checkpoint-cold")
				if err != nil || chunk == nil || chunk.Offset != int64(ordinal) {
					t.Fatal("claim cold part", ordinal, err)
				}
				if err := fixture.runtime.Handle(ctx, *chunk); err != nil {
					t.Fatal("execute real cold part", ordinal, err)
				}
				if err := fixture.state.CompleteGenerationChunk(ctx, *chunk); err != nil {
					t.Fatal(err)
				}
			}
			baseline := fixture.publication(t, ctx)
			originalAcquisitions, originalAppends := fixture.source.acquisitions, fixture.evidence.appends
			if baseline.root.ResultCount != 3 || baseline.root.Totals.Facts == 0 || len(baseline.assertions) == 0 {
				t.Fatal("missing real three-part target evidence")
			}
			fixture.reconciler.RecoveryPreparationEnabled = true
			prior := fixture.schedule(t, ctx)
			prefix, err := fixture.state.GetPartitionedExtractionDomain(ctx, fixture.repository, "grpc-consumer")
			if err != nil || prefix == nil {
				t.Fatal("real preceding domain publication unavailable", err)
			}

			suffix, err := fixture.state.GetPartitionedExtractionDomain(ctx, fixture.repository, "thrift-contract")
			if err != nil || suffix == nil {
				t.Fatal("real trailing domain publication unavailable", err)
			}
			prepared, err := fixture.reconciler.PrepareRecovery(ctx, extractionpublication.RecoveryPreparationRequest{
				Schema: extractionpublication.RecoveryPreparationSchema, Authority: fixture.authority,
				GenerationDigest: generation, PriorScheduleDigest: prior.Digest,
				Roots: []extractionpublication.RecoveryPreparationRoot{{Domain: prefix.Domain, PlanDigest: prefix.PlanDigest, RootDigest: prefix.RootDigest}, {Domain: baseline.root.Domain, PlanDigest: baseline.root.PlanDigest, RootDigest: baseline.root.Digest}, {Domain: suffix.Domain, PlanDigest: suffix.PlanDigest, RootDigest: suffix.RootDigest}},
				Mode:  extractionpublication.RecoveryPreparationCheckpoint, TargetDomain: baseline.root.Domain, TargetOrdinal: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.state.ExpandGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage, prepared.Generation); err != nil {
				t.Fatal(err)
			}
			cut := errors.New("test-owned ordinal-two checkpoint interruption")
			var checkpoint store.GenerationStaleLeaseTransition
			fixture.runtime.OnPartitionCheckpoint = func(_ context.Context, event store.GenerationStaleLeaseTransition) error {
				if event.Offset != 29 {
					return nil
				}
				checkpoint = event
				return cut
			}
			var selected store.GenerationChunk
			for ordinal := range 56 {
				chunk, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "checkpoint-predecessor")
				if err != nil || chunk == nil || chunk.Offset != int64(ordinal) {
					t.Fatal("claim checkpoint part", ordinal, err)
				}
				err = fixture.runtime.Handle(ctx, *chunk)
				if ordinal == 29 {
					if !errors.Is(err, cut) || checkpoint.ChunkIdentity != chunk.Identity {
						t.Fatal("actual reused ordinal two was not interrupted", err)
					}
					selected = *chunk
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := fixture.state.CompleteGenerationChunk(ctx, *chunk); err != nil {
					t.Fatal(err)
				}
			}
			request := extractionpublication.CheckpointRestartTransitionRequest{Transition: checkpoint, TargetGeneration: generation,
				PriorScheduleDigest: prior.Digest, Domain: baseline.root.Domain, Ordinal: 2, PlanDigest: baseline.root.PlanDigest,
				ResultIdentity: baseline.root.Results[2].Identity}
			hit, err := fixture.runtime.ReadCheckpointRestartTransition(ctx, request)
			if err != nil || hit.Current || hit.RootExists || hit.CompletionBitSet || !hit.CanonicalResultExists {
				t.Fatal("actual public checkpoint hit unavailable", err)
			}
			if err := fixture.state.Close(ctx); err != nil {
				t.Fatal(err)
			}
			next, err := store.OpenLocal(ctx, fixture.dataDir)
			if err != nil {
				t.Fatal("new persistent engine lifetime", err)
			}
			fixture.state, fixture.evidence.Surreal = next, next
			fixture.runtime.Store, fixture.reconciler.Evidence = next, next
			fence := fixture.runtime.Fence.(extractionpublication.AuthorityFence)
			fence.Store = next
			fixture.runtime.Fence = fence
			fixture.runtime.Publisher = extractionpublication.StorePublisher{Store: next}
			// Preserve the production twenty-second stale lease, including the
			// SDK's whole-second query cutoff. No native deadline is changed.
			timer := time.NewTimer(21*time.Second + 100*time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			}
			var mu sync.Mutex
			var attempts []store.GenerationChunk
			var transitions []store.GenerationStaleLeaseTransitionPoint
			var readErr error
			var readCounts readaccounting.Counts
			var recovered extractionpublication.CheckpointRestartTransition
			var recoveredEvent store.GenerationStaleLeaseTransition
			settledReady := make(chan struct{})
			// Enroll each complete plan/reap/worker turn so shutdown drains
			// real calls before cancellation, matching the production owner contract.
			owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 4, Requests: 1})
			if err != nil {
				t.Fatal(err)
			}
			scheduler := &generationscheduler.Scheduler{Store: next, Owners: owners, PollEvery: 10 * time.Millisecond, HeartbeatEvery: time.Second,
				StaleAfter: 20 * time.Second, Backoff: func(int) time.Duration { return 20 * time.Millisecond },
				Classes: map[store.GenerationResourceClass]generationscheduler.Class{store.GenerationResourceExtraction: {
					Concurrency: 2, Budget: generationscheduler.Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
					Handle: func(ctx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
						mu.Lock()
						attempts = append(attempts, chunk)
						mu.Unlock()
						return fixture.runtime.Handle(ctx, chunk)
					},
					OnStaleLeaseTransition: func(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
						mu.Lock()
						transitions = append(transitions, event.Point)
						mu.Unlock()
						if event.Point != store.GenerationStaleLeaseTransitionRecovered {
							return nil
						}
						// The genuine recovered callback occurs in the middle of the
						// schedule. Keep the reader's settled-schedule contract strict:
						// a second scheduler worker drains trailing parts, then the test
						// releases this callback within its unchanged store-call deadline.
						select {
						case <-settledReady:
						case <-ctx.Done():
							return ctx.Err()
						}
						accounted, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
						if err != nil {
							return err
						}
						observed, projection := readaccounting.CaptureRecoverySchedule(accounted)
						current := request
						current.Transition = event
						value, err := fixture.runtime.ReadCheckpointRestartTransition(observed, current)
						counts, finishErr := ledger.Finish()
						inventory, available := projection.Observation()
						if !available || inventory.Chunks != 56 || inventory.Successes != 56 {
							err = errors.Join(err, errors.New("actual recovery schedule is not complete"))
						}
						mu.Lock()
						readErr, readCounts, recovered = errors.Join(err, finishErr), counts, value
						recoveredEvent = event
						mu.Unlock()
						return errors.Join(err, finishErr)
					},
				}}}
			runCtx, stop := context.WithCancel(ctx)
			joined := make(chan error, 1)
			go func() { joined <- scheduler.Run(runCtx) }()
			var joinOnce sync.Once
			var schedulerErr error
			join := func() error {
				joinOnce.Do(func() {
					drain, cancelDrain := context.WithTimeout(ctx, 5*time.Second)
					drainErr := owners.Pause(drain)
					cancelDrain()
					stop()
					schedulerErr = errors.Join(drainErr, <-joined)
				})
				return schedulerErr
			}
			defer func() {
				if err := join(); err != nil {
					t.Error("scheduler join", err)
				}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				current := fixture.schedule(t, ctx)
				if current.Status == store.GenerationScheduleSettled {
					if current.Succeeded != 56 || current.Failed != 0 {
						t.Fatal("recovery did not durably complete", current.Succeeded, current.Failed)
					}
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("bounded scheduler recovery", ctx.Err())
				}
			}
			close(settledReady)
			// Join callbacks before inspecting their saved diagnostics.
			if err := join(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(baseline, fixture.publication(t, ctx)) {
				t.Fatal("publication/evidence changed across the real restart")
			}
			if fixture.source.acquisitions != originalAcquisitions || fixture.evidence.appends != originalAppends {
				t.Fatal("completed-result recovery reacquired source or appended evidence")
			}
			seen := make(map[int64]bool, 27)
			for _, attempt := range attempts {
				identity, err := store.GenerationChunkIdentity(selected.ScheduleDigest, attempt.Offset, 0)
				wantPriority := store.GenerationPriorityNeverRun
				if attempt.Offset == 29 {
					wantPriority = store.GenerationPriorityStale
				}
				if err != nil || attempt.Offset < 29 || attempt.Offset >= 56 || seen[attempt.Offset] || attempt.Attempt != 0 ||
					attempt.Identity != identity || attempt.Priority != wantPriority {
					t.Fatal("original middle/trailing attempt identity differs")
				}
				seen[attempt.Offset] = true
			}
			if len(attempts) != 27 || len(seen) != 27 || !seen[29] || readErr != nil || recovered.Point != store.GenerationStaleLeaseTransitionRecovered ||
				!recovered.Current || !recovered.RootExists || !recovered.CompletionBitSet || recovered.PrivateLeaseTokenDigest == hit.PrivateLeaseTokenDigest ||
				readCounts != (readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4}) || recovered.RootDigest != baseline.root.Digest {
				t.Fatal("actual ordinal-two R unavailable", len(attempts), readErr, readCounts)
			}
			if selected.Offset != 29 || request.Ordinal != 2 || selected.Attempt != 0 || recoveredEvent.ChunkIdentity != selected.Identity {
				t.Fatal("multidomain local/global original-attempt binding differs")
			}
			if mode == "pointer_change" {
				// Change only this fresh test-owned current pointer after the genuine
				// before snapshot. The real runtime must refuse its after snapshot.
				changing := &v20PointerChangingStore{Surreal: next, prior: fixture.schedule(t, ctx)}
				fixture.runtime.Store = changing
				request.Transition = recoveredEvent
				accounted, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
				if err != nil {
					t.Fatal(err)
				}
				value, err := fixture.runtime.ReadCheckpointRestartTransition(accounted, request)
				counts, finish := ledger.Finish()
				if !errors.Is(err, store.ErrNotFound) || finish != nil || value != (extractionpublication.CheckpointRestartTransition{}) ||
					counts != (readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 3}) || changing.calls != 2 || !changing.changed {
					t.Fatal("pointer change gained credit or accounting differed", errors.Is(err, store.ErrNotFound), counts, changing.calls, changing.changed)
				}
				t.Log("deterministic between-snapshot pointer removal refused: controls=7 store=3; differs from V20 controls=0 store=2")
			}
			t.Logf("real_persistent_multidomain mode=%s attempts=%d transitions=%v R=%s controls=%d store=%d", mode, len(attempts), transitions, recovered.Point, readCounts.ControlFileReads, readCounts.StoreReadAttempts)
		})
	}
}
