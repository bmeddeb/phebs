package extractionpublication

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestCheckpointRestartTransitionHookAndExactReader(t *testing.T) {
	runtime, state, request, hit, chunk := checkpointRestartTransitionFixture(t)

	hardDeath := errors.New("checkpoint hard death")
	var hitEvent store.GenerationStaleLeaseTransition
	runtime.OnPartitionCheckpoint = func(
		_ context.Context, transition store.GenerationStaleLeaseTransition,
	) error {
		hitEvent = transition
		return hardDeath
	}
	if err := runtime.Handle(t.Context(), chunk); !errors.Is(err, hardDeath) {
		t.Fatalf("checkpoint hook error = %v", err)
	}
	if hitEvent.Point != store.GenerationStaleLeaseTransitionCheckpointHit ||
		hitEvent.ChunkIdentity != chunk.Identity || hitEvent.Attempt != 0 ||
		hitEvent.Priority != store.GenerationPriorityNeverRun || !hitEvent.Leased ||
		hitEvent.PrivateLeaseTokenDigest != store.GenerationLeaseTokenDigest(chunk.LeaseToken) {
		t.Fatalf("checkpoint hit = %+v", hitEvent)
	}
	rawHit, err := json.Marshal(hitEvent)
	if err != nil || strings.Contains(string(rawHit), hitEvent.PrivateLeaseTokenDigest) {
		t.Fatalf("private lease digest escaped hit JSON: %s, %v", rawHit, err)
	}
	request.Transition = hitEvent
	state.set(hit, false)
	hitResult := readCheckpointRestartTransition(t, runtime, request)
	if !hitResult.CanonicalResultExists || !hitResult.CompletionFileExists ||
		hitResult.CompletionBitSet || hitResult.RootExists || hitResult.Current ||
		hitResult.Attempt != 0 || hitResult.Priority != store.GenerationPriorityNeverRun ||
		hitResult.PrivateLeaseTokenDigest != hitEvent.PrivateLeaseTokenDigest {
		t.Fatalf("prepared checkpoint = %+v", hitResult)
	}

	// The full row digest includes HeartbeatAt. A renewal between the two store
	// snapshots must not invalidate the stable checkpoint lease fingerprint.
	state.set(hit, true)
	heartbeatResult, err := runtime.ReadCheckpointRestartTransition(t.Context(), request)
	if err != nil || heartbeatResult.CheckpointStateDigest != hit.CheckpointStateDigest {
		t.Fatalf("heartbeat-only drift = %v", err)
	}
	state.set(hit, false)
	state.stableDrift = true
	if _, err := runtime.ReadCheckpointRestartTransition(t.Context(), request); !errors.Is(err, ErrStale) {
		t.Fatalf("stable lease/token drift = %v", err)
	}

	for name, limits := range map[string]readaccounting.Counts{
		"control": {
			ControlFileReads:  CheckpointRestartTransitionControlFileReads - 1,
			StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts,
		},
		"store": {
			ControlFileReads:  CheckpointRestartTransitionControlFileReads,
			StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts - 1,
		},
	} {
		t.Run(name+"_limit_plus_one", func(t *testing.T) {
			state.set(hit, false)
			ctx, ledger, err := readaccounting.Start(t.Context(), limits)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := runtime.ReadCheckpointRestartTransition(ctx, request)
			_, accountingErr := ledger.Finish()
			if !readaccounting.IsError(readErr) || !readaccounting.IsError(accountingErr) {
				t.Fatalf("limit refusal = %v/%v", readErr, accountingErr)
			}
		})
	}

	state.set(hit, false)
	lock := runtime.assemblyLock(request.PlanDigest)
	lock.Lock()
	lockCtx, cancelLock := context.WithTimeout(t.Context(), 20*time.Millisecond)
	_, lockErr := runtime.ReadCheckpointRestartTransition(lockCtx, request)
	cancelLock()
	lock.Unlock()
	if !errors.Is(lockErr, context.DeadlineExceeded) {
		t.Fatalf("blocked assembly lock = %v", lockErr)
	}

	hookCalls := 0
	runtime.OnPartitionCheckpoint = func(context.Context, store.GenerationStaleLeaseTransition) error {
		hookCalls++
		return nil
	}
	recoveredChunk := chunk
	recoveredChunk.Priority = store.GenerationPriorityStale
	recoveredChunk.LeaseToken = "recovered-private-lease"
	if err := runtime.Handle(t.Context(), recoveredChunk); err != nil {
		t.Fatal(err)
	}
	if hookCalls != 0 {
		t.Fatalf("recovered reuse retriggered checkpoint hook %d times", hookCalls)
	}
	recoveredStore := hit
	recoveredStore.Point = store.GenerationStaleLeaseTransitionRecovered
	recoveredStore.ScheduleStatus = store.GenerationScheduleSettled
	recoveredStore.Priority = store.GenerationPriorityStale
	recoveredStore.ChunkStatus = store.GenerationChunkDone
	recoveredStore.Leased = false
	recoveredStore.ChunkStateDigest = digest("checkpoint-recovered-row", nil)
	recoveredStore.CheckpointStateDigest = digest("checkpoint-recovered-stable-row", nil)
	recoveredStore.PrivateLeaseTokenDigest = ""
	state.set(recoveredStore, false)
	recoveredEvent := recoveredStore
	recoveredEvent.ScheduleStatus = ""
	recoveredEvent.ChunkStateDigest = ""
	recoveredEvent.CheckpointStateDigest = ""
	recoveredEvent.PrivateLeaseTokenDigest = store.GenerationLeaseTokenDigest(
		recoveredChunk.LeaseToken,
	)
	request.Transition = recoveredEvent
	recoveredResult := readCheckpointRestartTransition(t, runtime, request)
	if !recoveredResult.CompletionBitSet || !recoveredResult.RootExists ||
		!recoveredResult.Current || recoveredResult.RootDigest == "" ||
		recoveredResult.Priority != store.GenerationPriorityStale || recoveredResult.Attempt != 0 ||
		recoveredResult.PrivateLeaseTokenDigest != recoveredEvent.PrivateLeaseTokenDigest ||
		recoveredResult.PrivateLeaseTokenDigest == hitResult.PrivateLeaseTokenDigest ||
		recoveredResult.ResultIdentity != hitResult.ResultIdentity ||
		recoveredResult.ResultDigest != hitResult.ResultDigest {
		t.Fatalf("recovered checkpoint = %+v", recoveredResult)
	}
	rawRecovered, err := json.Marshal(recoveredResult)
	if err != nil || strings.Contains(string(rawRecovered), recoveredResult.PrivateLeaseTokenDigest) {
		t.Fatalf("private lease digest escaped recovery JSON: %s, %v", rawRecovered, err)
	}
}

func readCheckpointRestartTransition(
	t *testing.T,
	runtime *Runtime,
	request CheckpointRestartTransitionRequest,
) CheckpointRestartTransition {
	t.Helper()
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
		ControlFileReads:  CheckpointRestartTransitionControlFileReads,
		StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := runtime.ReadCheckpointRestartTransition(ctx, request)
	counts, accountingErr := ledger.Finish()
	if readErr != nil || accountingErr != nil || counts != (readaccounting.Counts{
		ControlFileReads:  CheckpointRestartTransitionControlFileReads,
		StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts,
	}) {
		t.Fatalf("transition = %+v, %v counts=%+v/%v", got, readErr, counts, accountingErr)
	}
	return got
}

func checkpointRestartTransitionFixture(
	t *testing.T,
) (*Runtime, *staleLeaseTransitionTestStore, CheckpointRestartTransitionRequest,
	store.GenerationStaleLeaseTransition, store.GenerationChunk) {
	t.Helper()
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationCheckpoint)
	schedule, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	runtime := fixture.reconciler.Runtime
	result, present, err := readPartitionResult(
		filepath.Join(fixture.resultDirectory(), resultName(fixture.request.TargetOrdinal)),
		fixture.domain.Plan, fixture.request.TargetOrdinal,
	)
	if err != nil || !present {
		t.Fatalf("prepared result = %+v, %v", result, err)
	}
	state := &staleLeaseTransitionTestStore{testScheduleStore: fixture.schedules.testScheduleStore}
	runtime.Store = state
	privateLease := "checkpoint-private-lease"
	hit := store.GenerationStaleLeaseTransition{
		Point:      store.GenerationStaleLeaseTransitionCheckpointHit,
		Repository: fixture.request.Authority.Repository, Stage: ScheduleStage,
		Generation: schedule.Generation, ResourceClass: store.GenerationResourceExtraction,
		ScheduleDigest: schedule.Digest, ScheduleStatus: store.GenerationScheduleActive,
		ChunkIdentity: digest("checkpoint-restart-chunk", nil),
		Offset:        int64(fixture.generation.Domains[0].StartOrdinal + fixture.request.TargetOrdinal),
		Length:        1, Attempt: 0, Priority: store.GenerationPriorityNeverRun,
		ChunkStatus: store.GenerationChunkRunning, Leased: true,
		ChunkStateDigest:        digest("checkpoint-running-row", nil),
		CheckpointStateDigest:   digest("checkpoint-stable-running-row", nil),
		PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest(privateLease),
	}
	state.set(hit, false)
	request := CheckpointRestartTransitionRequest{
		TargetGeneration:    fixture.request.GenerationDigest,
		PriorScheduleDigest: fixture.request.PriorScheduleDigest,
		Domain:              fixture.request.TargetDomain, Ordinal: fixture.request.TargetOrdinal,
		PlanDigest: fixture.domain.Plan.Digest, ResultIdentity: result.Identity,
	}
	chunk := store.GenerationChunk{
		ID: "checkpoint-restart-chunk", Identity: hit.ChunkIdentity,
		ScheduleDigest: schedule.Digest, Repository: hit.Repository, Stage: ScheduleStage,
		Generation: schedule.Generation, ResourceClass: store.GenerationResourceExtraction,
		Offset: hit.Offset, Length: 1, Attempt: 0,
		Priority: store.GenerationPriorityNeverRun, Status: store.GenerationChunkRunning,
		ClaimedBy: "checkpoint-worker", LeaseToken: privateLease,
	}
	return runtime, state, request, hit, chunk
}
