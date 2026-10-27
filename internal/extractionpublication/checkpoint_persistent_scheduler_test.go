package extractionpublication_test

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

// This supplies one known count mismatch, not a diagnosis of V19's unknown
// predicate. Both refusal and later publication execute the real store query.
type checkpointOneShotCountStore struct {
	store.PartitionedEvidenceStore
	refuse atomic.Bool
}

func (state *checkpointOneShotCountStore) PublishPartitionedExtractionDomain(ctx context.Context, publication store.PartitionedExtractionDomain) error {
	if state.refuse.Swap(false) {
		publication.Facts++
	}
	return state.PartitionedEvidenceStore.PublishPartitionedExtractionDomain(ctx, publication)
}

// Real three-part evidence and publication, ordinal-two interruption, a new
// persistent engine lifetime, actual stale reaping, scheduler retry/completion
// and the exact Runtime R reader. This is not the authenticated HTTP bridge or
// a native custody/RSS qualification.
func TestCheckpointPersistentThreePartSchedulerPublication(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	for _, mode := range []string{"ordinary", "diagnostic", "one_shot_refusal"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			fixture := newRecoveryPreparationFixtureOptions(t, ctx, 3, true)
			generation, err := fixture.reconciler.Reconcile(ctx, fixture.repository)
			if err != nil {
				t.Fatal(err)
			}
			initial := fixture.schedule(t, ctx)
			if initial.TotalChunks != 3 {
				t.Fatal("fixture is not three genuine parts", initial.TotalChunks)
			}
			if _, err := fixture.state.ExpandGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage, initial.Generation); err != nil {
				t.Fatal(err)
			}
			for ordinal := range 3 {
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
				t.Fatal("missing real three-part evidence")
			}
			fixture.reconciler.RecoveryPreparationEnabled = true
			prior := fixture.schedule(t, ctx)
			prepared, err := fixture.reconciler.PrepareRecovery(ctx, extractionpublication.RecoveryPreparationRequest{
				Schema: extractionpublication.RecoveryPreparationSchema, Authority: fixture.authority,
				GenerationDigest: generation, PriorScheduleDigest: prior.Digest,
				Roots: []extractionpublication.RecoveryPreparationRoot{{Domain: baseline.root.Domain, PlanDigest: baseline.root.PlanDigest, RootDigest: baseline.root.Digest}},
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
				if event.Offset != 2 {
					return nil
				}
				checkpoint = event
				return cut
			}
			var selected store.GenerationChunk
			for ordinal := range 3 {
				chunk, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "checkpoint-predecessor")
				if err != nil || chunk == nil || chunk.Offset != int64(ordinal) {
					t.Fatal("claim checkpoint part", ordinal, err)
				}
				err = fixture.runtime.Handle(ctx, *chunk)
				if ordinal == 2 {
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
			publisher := &checkpointOneShotCountStore{PartitionedEvidenceStore: next}
			publisher.refuse.Store(mode == "one_shot_refusal")
			fixture.runtime.Publisher = extractionpublication.StorePublisher{Store: publisher}
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
			var diagnosticFlags []readaccounting.PublicationFailureFlags
			var readErr error
			var readCounts readaccounting.Counts
			var recovered extractionpublication.CheckpointRestartTransition
			scheduler := &generationscheduler.Scheduler{Store: next, PollEvery: 10 * time.Millisecond, HeartbeatEvery: time.Second,
				StaleAfter: 20 * time.Second, Backoff: func(int) time.Duration { return 20 * time.Millisecond },
				Classes: map[store.GenerationResourceClass]generationscheduler.Class{store.GenerationResourceExtraction: {
					Concurrency: 1, Budget: generationscheduler.Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
					Handle: func(ctx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
						mu.Lock()
						attempts = append(attempts, chunk)
						mu.Unlock()
						if mode != "ordinary" {
							ctx = readaccounting.WithPublicationFailureObserver(ctx, func(_ readaccounting.PublicationFailureStage, _ error, flags readaccounting.PublicationFailureFlags) {
								mu.Lock()
								defer mu.Unlock()
								if flags.StorePredicatesAvailable {
									diagnosticFlags = append(diagnosticFlags, flags)
								}
							})
						}
						return fixture.runtime.Handle(ctx, chunk)
					},
					OnStaleLeaseTransition: func(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
						mu.Lock()
						transitions = append(transitions, event.Point)
						mu.Unlock()
						if event.Point != store.GenerationStaleLeaseTransitionRecovered {
							return nil
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
						if !available || inventory.Chunks != 3 || inventory.Successes != 3 {
							err = errors.Join(err, errors.New("actual recovery schedule is not complete"))
						}
						mu.Lock()
						readErr, readCounts, recovered = errors.Join(err, finishErr), counts, value
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
					stop()
					schedulerErr = <-joined
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
					if current.Succeeded != 3 || current.Failed != 0 {
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
			if mode == "one_shot_refusal" {
				identity, err := store.GenerationChunkIdentity(selected.ScheduleDigest, 2, 1)
				if err != nil || len(attempts) != 2 || attempts[0].Attempt != 0 || attempts[0].Priority != store.GenerationPriorityStale || attempts[0].Identity != selected.Identity ||
					attempts[1].Attempt != 1 || attempts[1].Identity != identity || attempts[1].Priority != store.GenerationPriorityRetry {
					t.Fatal("actual durable retry identity differs", err, len(attempts))
				}
				for _, point := range transitions {
					if point == store.GenerationStaleLeaseTransitionRecovered {
						t.Fatal("retry-priority completion unexpectedly supplied selected Recovered")
					}
				}
				if !reflect.DeepEqual(transitions, []store.GenerationStaleLeaseTransitionPoint{store.GenerationStaleLeaseTransitionHit, store.GenerationStaleLeaseTransitionRequeued}) ||
					recovered.Point != "" || readCounts != (readaccounting.Counts{}) {
					t.Fatal("retry completion gained an R callback or reader credit")
				}
				if len(diagnosticFlags) != 1 || diagnosticFlags[0].CountsExact || diagnosticFlags[0].SingleResult || diagnosticFlags[0].MarkedSingle || !diagnosticFlags[0].RunEligible || !diagnosticFlags[0].CandidateCurrent || !diagnosticFlags[0].RunOwned {
					t.Fatal("real one-shot count refusal not isolated", diagnosticFlags)
				}
				if _, err := next.ReadGenerationStaleLeaseTransition(ctx, store.GenerationStaleLeaseTransitionRequest{
					Point: store.GenerationStaleLeaseTransitionRecovered, Repository: selected.Repository, Stage: selected.Stage,
					Generation: selected.Generation, ResourceClass: selected.ResourceClass, ScheduleDigest: selected.ScheduleDigest,
					ChunkIdentity: selected.Identity, Offset: 2, Length: 1, Attempt: 0}); err == nil {
					t.Fatal("failed original attempt gained a recovered-reader result")
				}
			} else if len(attempts) != 1 || attempts[0].Priority != store.GenerationPriorityStale || readErr != nil || recovered.Point != store.GenerationStaleLeaseTransitionRecovered ||
				!recovered.Current || !recovered.RootExists || !recovered.CompletionBitSet || recovered.PrivateLeaseTokenDigest == hit.PrivateLeaseTokenDigest ||
				readCounts != (readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4}) || recovered.RootDigest != baseline.root.Digest {
				t.Fatal("actual ordinal-two R unavailable", len(attempts), readErr, readCounts)
			}
			t.Logf("real_persistent_three_part mode=%s attempts=%d transitions=%v R=%s controls=%d store=%d", mode, len(attempts), transitions, recovered.Point, readCounts.ControlFileReads, readCounts.StoreReadAttempts)
		})
	}
}
