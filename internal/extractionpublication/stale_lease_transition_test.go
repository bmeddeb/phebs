package extractionpublication

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

type staleLeaseTransitionTestStore struct {
	*testScheduleStore
	mu         sync.Mutex
	transition store.GenerationStaleLeaseTransition
	calls      int
	drift      bool
}

func (state *staleLeaseTransitionTestStore) ReadGenerationStaleLeaseTransition(
	ctx context.Context,
	request store.GenerationStaleLeaseTransitionRequest,
) (store.GenerationStaleLeaseTransition, error) {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 2); err != nil {
		return store.GenerationStaleLeaseTransition{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.calls++
	value := state.transition
	want := store.GenerationStaleLeaseTransitionRequest{
		Point: value.Point, Repository: value.Repository, Stage: value.Stage,
		Generation: value.Generation, ResourceClass: value.ResourceClass,
		ScheduleDigest: value.ScheduleDigest, ChunkIdentity: value.ChunkIdentity,
		Offset: value.Offset, Length: value.Length, Attempt: value.Attempt,
		StaleBefore: value.StaleBefore,
	}
	if request != want {
		return store.GenerationStaleLeaseTransition{}, store.ErrGenerationStale
	}
	if state.drift && state.calls == 2 {
		value.ChunkStateDigest = digest("stale-lease-test-drift", nil)
	}
	return value, nil
}

func (state *staleLeaseTransitionTestStore) set(
	value store.GenerationStaleLeaseTransition,
	drift bool,
) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.transition = value
	state.calls = 0
	state.drift = drift
}

func TestReadStaleLeaseTransitionBindsResultAndExactReadCost(t *testing.T) {
	runtime, state, request, hit, recovered := staleLeaseTransitionFixture(t)

	for _, transition := range []store.GenerationStaleLeaseTransition{hit, recovered} {
		t.Run(string(transition.Point), func(t *testing.T) {
			state.set(transition, false)
			request.Transition = transition
			if transition.Point == store.GenerationStaleLeaseTransitionRecovered {
				request.Transition.ScheduleStatus = ""
				request.Transition.ChunkStateDigest = ""
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
				ControlFileReads:  StaleLeaseTransitionControlFileReads,
				StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := runtime.ReadStaleLeaseTransition(ctx, request)
			counts, accountingErr := ledger.Finish()
			if readErr != nil || accountingErr != nil ||
				counts != (readaccounting.Counts{
					ControlFileReads:  StaleLeaseTransitionControlFileReads,
					StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts,
				}) {
				t.Fatalf("read error=%v accounting=%+v/%v", readErr, counts, accountingErr)
			}
			if got.Point != transition.Point || got.TargetGeneration != request.TargetGeneration ||
				got.ScheduleGeneration != transition.Generation ||
				got.PriorScheduleDigest != request.PriorScheduleDigest ||
				got.ScheduleDigest != transition.ScheduleDigest ||
				got.ChunkIdentity != transition.ChunkIdentity || got.Domain != request.Domain ||
				got.Ordinal != request.Ordinal || got.PlanDigest != request.PlanDigest ||
				got.ResultIdentity != request.ResultIdentity {
				t.Fatalf("transition = %+v", got)
			}
		})
	}

	state.set(hit, true)
	request.Transition = hit
	if _, err := runtime.ReadStaleLeaseTransition(t.Context(), request); !errors.Is(err, ErrStale) {
		t.Fatalf("moving store state error = %v", err)
	}
	state.set(hit, false)
	resultIdentity := request.ResultIdentity
	request.ResultIdentity = digest("wrong-result", nil)
	if _, err := runtime.ReadStaleLeaseTransition(t.Context(), request); !errors.Is(err, ErrStale) {
		t.Fatalf("wrong result error = %v", err)
	}
	request.ResultIdentity = resultIdentity
	wrongSchedule := hit
	wrongSchedule.ScheduleDigest = digest("wrong-schedule", nil)
	state.set(wrongSchedule, false)
	request.Transition = wrongSchedule
	if _, err := runtime.ReadStaleLeaseTransition(t.Context(), request); !errors.Is(err, ErrStale) {
		t.Fatalf("wrong prepared schedule error = %v", err)
	}
}

func TestReadStaleLeaseTransitionRefusesReadLimitPlusOne(t *testing.T) {
	runtime, state, request, hit, _ := staleLeaseTransitionFixture(t)
	state.set(hit, false)
	request.Transition = hit
	for name, limits := range map[string]readaccounting.Counts{
		"control": {
			ControlFileReads:  StaleLeaseTransitionControlFileReads - 1,
			StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts,
		},
		"store": {
			ControlFileReads:  StaleLeaseTransitionControlFileReads,
			StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts - 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			state.set(hit, false)
			ctx, ledger, err := readaccounting.Start(t.Context(), limits)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := runtime.ReadStaleLeaseTransition(ctx, request)
			_, accountingErr := ledger.Finish()
			if !readaccounting.IsError(readErr) || !readaccounting.IsError(accountingErr) {
				t.Fatalf("limit refusal = %v/%v", readErr, accountingErr)
			}
		})
	}
}

func staleLeaseTransitionFixture(
	t *testing.T,
) (*Runtime, *staleLeaseTransitionTestStore, StaleLeaseTransitionRequest,
	store.GenerationStaleLeaseTransition, store.GenerationStaleLeaseTransition) {
	t.Helper()
	plan := buildTestPlan(t, digest("stale-lease-source", nil), true)
	runtime, scheduleState, _, _, _, _, domain := newRuntimeFixture(t, plan)
	target, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	generation, err := runtime.openGeneration(
		runtime.generationDirectory(plan.Repository, target), plan.Repository, target,
	)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := (&testExecutor{}).ExecutePartition(t.Context(), plan, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := candidate.BuildPartitionResult(plan, 0, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := installPartitionResult(
		filepath.Join(runtime.generationDirectory(plan.Repository, target), domainKey(plan.Domain), resultName(0)),
		result,
	); err != nil {
		t.Fatal(err)
	}
	prior, err := scheduleState.GetGenerationSchedule(t.Context(), plan.Repository, ScheduleStage)
	if err != nil {
		t.Fatal(err)
	}
	scheduleState.settle(plan.Repository, 0)
	if err := runtime.enqueue(t.Context(), generation); err != nil {
		t.Fatal(err)
	}
	current, err := scheduleState.GetGenerationSchedule(t.Context(), plan.Repository, ScheduleStage)
	if err != nil {
		t.Fatal(err)
	}
	state := &staleLeaseTransitionTestStore{testScheduleStore: scheduleState}
	runtime.Store = state
	base := store.GenerationStaleLeaseTransition{
		Repository: plan.Repository, Stage: ScheduleStage,
		Generation: current.Generation, ResourceClass: store.GenerationResourceExtraction,
		ScheduleDigest: current.Digest, ChunkIdentity: digest("stale-lease-chunk", nil),
		Offset: 0, Length: 1, Attempt: 0,
	}
	hit := base
	hit.Point = store.GenerationStaleLeaseTransitionHit
	hit.ScheduleStatus = store.GenerationScheduleActive
	hit.Priority = store.GenerationPriorityNeverRun
	hit.ChunkStatus = store.GenerationChunkRunning
	hit.Leased = true
	hit.ChunkStateDigest = digest("stale-lease-hit-state", nil)
	hit.StaleBefore = time.Now().UTC()
	recovered := base
	recovered.Point = store.GenerationStaleLeaseTransitionRecovered
	recovered.ScheduleStatus = store.GenerationScheduleSettled
	recovered.Priority = store.GenerationPriorityStale
	recovered.ChunkStatus = store.GenerationChunkDone
	recovered.ChunkStateDigest = digest("stale-lease-recovered-state", nil)
	request := StaleLeaseTransitionRequest{
		TargetGeneration: target, PriorScheduleDigest: prior.Digest,
		Domain: plan.Domain, Ordinal: 0, PlanDigest: plan.Digest,
		ResultIdentity: result.Identity,
	}
	return runtime, state, request, hit, recovered
}
