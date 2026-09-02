package t4110

import (
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestTargetRelationshipScheduleExpandsBeforeClaim(t *testing.T) {
	state, err := store.OpenLocalMemory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeLiveStore(state); err != nil {
			t.Error(err)
		}
	})

	generation := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	schedule, err := state.EnqueueGenerationSchedule(t.Context(), store.GenerationScheduleSpec{
		Repository: liveRepository, Stage: relationshippublication.ScheduleStageV3,
		Generation: generation, ResourceClass: store.GenerationResourceMemory,
		TotalItems: 1, ChunkItems: 1,
		MaxAttempts:      relationshippublication.ScheduleMaxAttempts,
		RepositoryTokens: relationshippublication.ScheduleRepositoryTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	if schedule.NextOffset != 0 || schedule.Materialized != 0 || schedule.Pending != 0 {
		t.Fatalf("new relationship schedule = %+v", schedule)
	}
	if _, err := state.ClaimGenerationChunk(
		t.Context(), store.GenerationResourceMemory, "pre-expansion",
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pre-expansion claim = %v", err)
	}

	harness := liveHarness{state: state}
	expanded, chunk, err := harness.claimTargetRelationshipV3Chunk(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Digest != schedule.Digest || chunk.ScheduleDigest != schedule.Digest ||
		chunk.Generation != generation || chunk.Offset != 0 || chunk.Length != 1 {
		t.Fatalf("expanded schedule=%+v chunk=%+v", expanded, chunk)
	}
	if err := state.CompleteGenerationChunk(t.Context(), chunk); err != nil {
		t.Fatal(err)
	}
	settled, err := state.GetGenerationSchedule(
		t.Context(), liveRepository, relationshippublication.ScheduleStageV3,
	)
	if err != nil || settled.Status != store.GenerationScheduleSettled ||
		settled.Succeeded != 1 || settled.Failed != 0 {
		t.Fatalf("settled relationship schedule = %+v, %v", settled, err)
	}
}
