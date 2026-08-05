package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func generationSpec(repository, generation string) GenerationScheduleSpec {
	return GenerationScheduleSpec{
		Repository: repository, Stage: "source-observation", Generation: generation,
		ResourceClass: GenerationResourceCPU, TotalItems: 130,
		ChunkItems: 2, MaxAttempts: 3, RepositoryTokens: 1,
	}
}

func TestGenerationSchedulePagedFanoutAndLeaseLifecycle(t *testing.T) {
	store := newRunnerStore(t)
	spec := generationSpec("example.invalid/mono", "sha256:"+fmt.Sprintf("%064d", 1))
	schedule, err := store.EnqueueGenerationSchedule(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.TotalChunks != 65 || schedule.Materialized != 0 || schedule.NextOffset != 0 {
		t.Fatalf("initial schedule = %+v", schedule)
	}
	first, err := store.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if first.Materialized != MaxGenerationFanoutPage || first.Pending != MaxGenerationFanoutPage ||
		first.NextOffset != 128 {
		t.Fatalf("first fanout page = %+v", first)
	}
	second, err := store.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if second.Materialized != 65 || second.Pending != 65 || second.NextOffset != 130 {
		t.Fatalf("second fanout page = %+v", second)
	}

	chunk, err := store.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Offset != 0 || chunk.Length != 2 || chunk.Attempt != 0 ||
		chunk.Priority != GenerationPriorityNeverRun {
		t.Fatalf("first chunk = %+v", chunk)
	}
	if _, err := store.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "worker-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repository token did not fence second claim: %v", err)
	}
	if err := store.HeartbeatGenerationChunk(t.Context(), *chunk); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteGenerationChunk(t.Context(), *chunk); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteGenerationChunk(t.Context(), *chunk); !errors.Is(err, ErrGenerationLeaseLost) {
		t.Fatalf("replayed completion = %v", err)
	}
	for index := 1; index < MaxGenerationActiveStagesPerRepository; index++ {
		additional := spec
		additional.Stage = fmt.Sprintf("stage-%d", index)
		additional.Generation = "sha256:" + fmt.Sprintf("%064d", index+10)
		additional.TotalItems = 1
		if _, err := store.EnqueueGenerationSchedule(t.Context(), additional); err != nil {
			t.Fatal(err)
		}
	}
	overflow := spec
	overflow.Stage = "stage-overflow"
	overflow.Generation = "sha256:" + fmt.Sprintf("%064d", 99)
	overflow.TotalItems = 1
	if _, err := store.EnqueueGenerationSchedule(t.Context(), overflow); err == nil {
		t.Fatal("active-stage limit accepted a ninth repository stage")
	}
}

func TestGenerationScheduleRetryCoalescingAndStaleFence(t *testing.T) {
	store := newRunnerStore(t)
	firstSpec := generationSpec("example.invalid/mono", "sha256:"+fmt.Sprintf("%064d", 2))
	firstSpec.TotalItems = 4
	if _, err := store.EnqueueGenerationSchedule(t.Context(), firstSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExpandGenerationSchedule(t.Context(), firstSpec.Repository, firstSpec.Stage, firstSpec.Generation); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimGenerationChunk(t.Context(), firstSpec.ResourceClass, "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	successor, err := store.RetryGenerationChunk(
		t.Context(), *first, "retryable", time.Now().UTC().Add(-time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if successor.Attempt != 1 || successor.Priority != GenerationPriorityRetry ||
		successor.Identity == first.Identity || successor.Offset != first.Offset {
		t.Fatalf("retry successor = %+v, first %+v", successor, first)
	}

	claimedRetry, err := store.ClaimGenerationChunk(t.Context(), firstSpec.ResourceClass, "worker-b")
	if err != nil {
		t.Fatal(err)
	}
	if claimedRetry.Offset != 2 {
		// Never-run siblings deterministically precede a retry successor.
		t.Fatalf("priority order claimed %+v", claimedRetry)
	}
	secondSpec := firstSpec
	secondSpec.Generation = "sha256:" + fmt.Sprintf("%064d", 3)
	if _, err := store.EnqueueGenerationSchedule(t.Context(), secondSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueGenerationSchedule(t.Context(), firstSpec); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("superseded A-B-A schedule was resurrected: %v", err)
	}
	if err := store.CompleteGenerationChunk(t.Context(), *claimedRetry); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("old worker completion = %v", err)
	}
	current, err := store.GetGenerationSchedule(t.Context(), secondSpec.Repository, secondSpec.Stage)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != secondSpec.Generation || current.Succeeded != 0 {
		t.Fatalf("coalesced current schedule = %+v", current)
	}
}

func TestGenerationScheduleReleaseAndAttemptExhaustionPreserveSiblings(t *testing.T) {
	store := newRunnerStore(t)
	spec := generationSpec("example.invalid/restart", "sha256:"+fmt.Sprintf("%064d", 4))
	spec.TotalItems = 4
	spec.RepositoryTokens = 2
	spec.MaxAttempts = 1
	if _, err := store.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "worker-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteGenerationChunk(t.Context(), *first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryGenerationChunk(
		t.Context(), *second, "terminal attempt", time.Now().UTC(),
	); !errors.Is(err, ErrGenerationExhausted) {
		t.Fatalf("attempt exhaustion = %v", err)
	}
	settled, err := store.GetGenerationSchedule(t.Context(), spec.Repository, spec.Stage)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != GenerationScheduleSettled || settled.Succeeded != 1 || settled.Failed != 1 {
		t.Fatalf("settled siblings = %+v", settled)
	}
}

func TestGenerationScheduleRepositoryFairnessAndStaleLeaseRecovery(t *testing.T) {
	store := newRunnerStore(t)
	for index, repository := range []string{"example.invalid/a", "example.invalid/b"} {
		spec := generationSpec(repository, "sha256:"+fmt.Sprintf("%064d", index+5))
		spec.TotalItems = 2
		if _, err := store.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ClaimGenerationChunk(t.Context(), GenerationResourceCPU, "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimGenerationChunk(t.Context(), GenerationResourceCPU, "worker-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.Repository == second.Repository {
		t.Fatalf("fair claims selected one repository twice: %s", first.Repository)
	}
	if err := store.CompleteGenerationChunk(t.Context(), *first); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatGenerationChunk(t.Context(), *second); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](t.Context(), store.db,
		"UPDATE $chunk SET heartbeat_at = $old RETURN NONE", map[string]any{
			"chunk": generationChunkRecordID(*second), "old": time.Now().UTC().Add(-time.Hour),
		}); err != nil {
		t.Fatal(err)
	}
	reaped, err := store.ReapStaleGenerationChunks(t.Context(), GenerationResourceCPU, time.Minute)
	if err != nil || reaped != 1 {
		t.Fatalf("stale reaping = %d, %v", reaped, err)
	}
	if err := store.CompleteGenerationChunk(t.Context(), *second); !errors.Is(err, ErrGenerationLeaseLost) {
		t.Fatalf("stale worker retained authority: %v", err)
	}
	recovered, err := store.ClaimGenerationChunk(t.Context(), GenerationResourceCPU, "worker-c")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ID != second.ID || recovered.Priority != GenerationPriorityStale ||
		recovered.LeaseToken == second.LeaseToken {
		t.Fatalf("stale recovery claim = %+v, prior %+v", recovered, second)
	}
	if err := store.CompleteGenerationChunk(t.Context(), *recovered); err != nil {
		t.Fatal(err)
	}
}
