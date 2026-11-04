package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

// recoveredWithDeferredSiblings drives a four-chunk schedule into the V23
// order: the selected chunk 0 is reaped to stale priority, chunks 2 and 3 are
// deferred to the same priority behind a long delay, and chunk 0 completes
// while both deferred siblings are still pending.
func recoveredWithDeferredSiblings(
	t *testing.T,
	state *Surreal,
	spec GenerationScheduleSpec,
) (*GenerationSchedule, GenerationChunk) {
	t.Helper()
	spec.TotalItems, spec.ChunkItems = 4, 1
	schedule, err := state.EnqueueGenerationSchedule(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation); err != nil {
		t.Fatal(err)
	}
	selected, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "selected-before-kill")
	if err != nil || selected == nil || selected.Offset != 0 {
		t.Fatalf("selected claim = %+v, %v", selected, err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	if _, err := surrealdb.Query[any](t.Context(), state.db,
		"UPDATE $chunk SET claimed_at = $claim, heartbeat_at = $old RETURN NONE", map[string]any{
			"chunk": generationChunkRecordID(*selected), "claim": old.Add(-time.Hour), "old": old,
		}); err != nil {
		t.Fatal(err)
	}
	if reaped, err := state.ReapStaleGenerationChunks(t.Context(), spec.ResourceClass, time.Minute); err != nil || reaped != 1 {
		t.Fatalf("reaped = %d, %v", reaped, err)
	}
	for _, step := range []struct {
		offset int64
		defer_ bool
	}{{1, false}, {2, true}, {3, true}} {
		chunk, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "sibling")
		if err != nil || chunk == nil || chunk.Offset != step.offset || chunk.Priority != GenerationPriorityNeverRun {
			t.Fatalf("sibling claim %d = %+v, %v", step.offset, chunk, err)
		}
		if step.defer_ {
			err = state.DeferGenerationChunk(t.Context(), *chunk, "extraction authority fence is busy", time.Hour)
		} else {
			err = state.CompleteGenerationChunk(t.Context(), *chunk)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "selected-after-restart")
	if err != nil || recovered == nil || recovered.Identity != selected.Identity || recovered.Priority != GenerationPriorityStale {
		t.Fatalf("recovered claim = %+v, %v", recovered, err)
	}
	if err := state.CompleteGenerationChunk(t.Context(), *recovered); err != nil {
		t.Fatal(err)
	}
	return schedule, *recovered
}

func releaseDeferredSiblings(t *testing.T, state *Surreal, digest string) {
	t.Helper()
	if _, err := surrealdb.Query[any](t.Context(), state.db,
		"UPDATE generation_schedule_chunk SET not_before = time::now() - 1s WHERE schedule_digest = $digest AND status = 'pending' RETURN NONE",
		map[string]any{"digest": digest}); err != nil {
		t.Fatal(err)
	}
}

func settlementRequest(spec GenerationScheduleSpec, digest string) GenerationScheduleSettlementRequest {
	return GenerationScheduleSettlementRequest{
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ResourceClass: spec.ResourceClass, ScheduleDigest: digest,
	}
}

func readSettlement(t *testing.T, state *Surreal, request GenerationScheduleSettlementRequest) (GenerationScheduleSettlement, error) {
	t.Helper()
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
		StoreReadAttempts: GenerationScheduleSettlementStoreReadAttempts,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, readErr := state.ReadGenerationScheduleSettlement(ctx, request)
	counts, finishErr := ledger.Finish()
	if finishErr != nil || counts != (readaccounting.Counts{StoreReadAttempts: GenerationScheduleSettlementStoreReadAttempts}) {
		t.Fatalf("settlement counts = %+v, %v", counts, finishErr)
	}
	return value, readErr
}

// TestRecoveredPrecedesDeferredSiblingsThenSettles is the V23 epoch-4 order:
// the selected recovery completes before higher-offset deferred chunks of the
// same schedule. Recovered must accept the active schedule; settlement is
// proved only after the siblings finish.
func TestRecoveredPrecedesDeferredSiblingsThenSettles(t *testing.T) {
	state := newRunnerStore(t)
	spec := generationSpec("example.invalid/recovered-before-deferred", "sha256:"+strings.Repeat("a", 64))
	schedule, recovered := recoveredWithDeferredSiblings(t, state, spec)
	spec.TotalItems, spec.ChunkItems = 4, 1
	request := GenerationStaleLeaseTransitionRequest{
		Point: GenerationStaleLeaseTransitionRecovered, Repository: spec.Repository, Stage: spec.Stage,
		Generation: spec.Generation, ResourceClass: spec.ResourceClass, ScheduleDigest: schedule.Digest,
		ChunkIdentity: recovered.Identity, Offset: recovered.Offset, Length: recovered.Length, Attempt: recovered.Attempt,
	}
	before, err := state.ReadGenerationStaleLeaseTransition(t.Context(), request)
	if err != nil || before.ScheduleStatus != GenerationScheduleActive || before.ChunkStatus != GenerationChunkDone ||
		before.Priority != GenerationPriorityStale || before.Leased {
		t.Fatalf("recovered with pending siblings = %+v, %v", before, err)
	}
	pending, err := readSettlement(t, state, settlementRequest(spec, schedule.Digest))
	if err != nil || pending.Complete() || pending.Status != GenerationScheduleActive || pending.Pending != 2 ||
		pending.Running != 0 || pending.Succeeded != 2 || pending.Failed != 0 {
		t.Fatalf("settlement before siblings finish = %+v, %v", pending, err)
	}

	releaseDeferredSiblings(t, state, schedule.Digest)
	for _, offset := range []int64{2, 3} {
		chunk, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "deferred-sibling")
		if err != nil || chunk == nil || chunk.Offset != offset || chunk.Priority != GenerationPriorityStale {
			t.Fatalf("deferred sibling claim %d = %+v, %v", offset, chunk, err)
		}
		if err := state.CompleteGenerationChunk(t.Context(), *chunk); err != nil {
			t.Fatal(err)
		}
	}
	settled, err := readSettlement(t, state, settlementRequest(spec, schedule.Digest))
	if err != nil || !settled.Complete() || settled.Succeeded != 4 || settled.ScheduleDigest != schedule.Digest {
		t.Fatalf("settlement after siblings finish = %+v, %v", settled, err)
	}
	after, err := state.ReadGenerationStaleLeaseTransition(t.Context(), request)
	if err != nil || after.ScheduleStatus != GenerationScheduleSettled || after.ChunkStateDigest != before.ChunkStateDigest {
		t.Fatalf("recovered after settlement = %+v, %v", after, err)
	}
}

func TestScheduleSettlementBarrierRefusesFailureAndReplacement(t *testing.T) {
	state := newRunnerStore(t)
	spec := generationSpec("example.invalid/settlement-failure", "sha256:"+strings.Repeat("b", 64))
	spec.MaxAttempts = 1
	schedule, _ := recoveredWithDeferredSiblings(t, state, spec)
	spec.TotalItems, spec.ChunkItems = 4, 1
	releaseDeferredSiblings(t, state, schedule.Digest)
	first, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "deferred-sibling")
	if err != nil || first == nil {
		t.Fatalf("deferred sibling claim = %+v, %v", first, err)
	}
	if err := state.CompleteGenerationChunk(t.Context(), *first); err != nil {
		t.Fatal(err)
	}
	last, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "deferred-sibling")
	if err != nil || last == nil {
		t.Fatalf("last sibling claim = %+v, %v", last, err)
	}
	if err := state.FailGenerationChunk(t.Context(), *last, "sibling failed"); err != nil && !errors.Is(err, ErrGenerationExhausted) {
		t.Fatal(err)
	}
	failed, err := readSettlement(t, state, settlementRequest(spec, schedule.Digest))
	if !errors.Is(err, ErrGenerationScheduleFailed) || failed.Status != GenerationScheduleSettled || failed.Failed != 1 || failed.Complete() {
		t.Fatalf("settled-with-failure barrier = %+v, %v", failed, err)
	}

	replacementSpec := spec
	replacementSpec.Generation = "sha256:" + strings.Repeat("c", 64)
	replacement, err := state.EnqueueGenerationSchedule(t.Context(), replacementSpec)
	if err != nil || replacement.Digest == schedule.Digest {
		t.Fatalf("replacement = %+v, %v", replacement, err)
	}
	if _, err := readSettlement(t, state, settlementRequest(spec, schedule.Digest)); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("replaced schedule barrier = %v", err)
	}
}

func TestScheduleSettlementObservationShape(t *testing.T) {
	spec := generationSpec("example.invalid/settlement-shape", "sha256:"+strings.Repeat("d", 64))
	spec.TotalItems, spec.ChunkItems = 4, 1
	normalized, err := normalizeGenerationScheduleSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := GenerationSchedule{
		Schema: GenerationScheduleSchema, Digest: generationScheduleDigest(normalized), Repository: spec.Repository,
		Stage: spec.Stage, Generation: spec.Generation, ResourceClass: spec.ResourceClass, TotalItems: 4,
		ChunkItems: 1, TotalChunks: 4, MaxAttempts: spec.MaxAttempts, RepositoryTokens: spec.RepositoryTokens,
		NextOffset: 4, Materialized: 4, Succeeded: 4, Status: GenerationScheduleSettled, CreatedAt: now, UpdatedAt: now,
	}
	request := settlementRequest(spec, base.Digest)
	for name, test := range map[string]struct {
		mutate   func(*GenerationSchedule)
		request  func(*GenerationScheduleSettlementRequest)
		complete bool
		err      error
	}{
		"settled complete":    {complete: true},
		"active pending":      {mutate: func(s *GenerationSchedule) { s.Status, s.Succeeded, s.Pending = GenerationScheduleActive, 1, 3 }},
		"settled with failed": {mutate: func(s *GenerationSchedule) { s.Succeeded, s.Failed = 3, 1 }, err: ErrGenerationScheduleFailed},
		"superseded":          {mutate: func(s *GenerationSchedule) { s.Status = GenerationScheduleSuperseded }, err: ErrGenerationStale},
		"unexpanded":          {mutate: func(s *GenerationSchedule) { s.Status, s.NextOffset, s.Succeeded = GenerationScheduleActive, 2, 0 }, err: ErrGenerationStale},
		"other digest": {request: func(r *GenerationScheduleSettlementRequest) {
			r.ScheduleDigest = "sha256:" + strings.Repeat("e", 64)
		}, err: ErrGenerationStale},
		"other generation": {request: func(r *GenerationScheduleSettlementRequest) {
			r.Generation = "sha256:" + strings.Repeat("f", 64)
		}, err: ErrGenerationStale},
	} {
		t.Run(name, func(t *testing.T) {
			schedule, req := base, request
			if test.mutate != nil {
				test.mutate(&schedule)
			}
			if test.request != nil {
				test.request(&req)
			}
			value, err := generationScheduleSettlement(req, schedule)
			if !errors.Is(err, test.err) || test.err == nil && err != nil || value.Complete() != test.complete {
				t.Fatalf("settlement = %+v, %v; want complete=%t err=%v", value, err, test.complete, test.err)
			}
		})
	}
	if _, err := (&Surreal{}).ReadGenerationScheduleSettlement(context.Background(), GenerationScheduleSettlementRequest{}); err == nil {
		t.Fatal("invalid settlement request was accepted")
	}
}
