package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestLocalGenerationChunkReaderReportsAuthoritativeLeaseState(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	dataDir := t.TempDir()
	state, err := OpenLocal(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	spec := generationSpec("example.invalid/lease-probe", "sha256:"+strings.Repeat("7", 64))
	spec.TotalItems, spec.ChunkItems = 1, 1
	if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation); err != nil {
		t.Fatal(err)
	}
	chunk, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "probe-worker")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenLocalGenerationChunkReader(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(context.Background()) })
	running, err := reader.GenerationChunkLeaseState(t.Context(), chunk.Identity)
	if err != nil || running.Identity != chunk.Identity || running.Repository != chunk.Repository ||
		running.Stage != chunk.Stage || running.Generation != chunk.Generation ||
		running.Attempt != chunk.Attempt || running.Status != GenerationChunkRunning {
		t.Fatalf("running lease state = %+v, %v", running, err)
	}
	selected, err := reader.CurrentGenerationRunningChunk(
		t.Context(), spec.Repository, spec.Stage,
	)
	if err != nil || selected != running {
		t.Fatalf("selected current running chunk = %+v, %v", selected, err)
	}
	if err := state.CompleteGenerationChunk(t.Context(), *chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.CurrentGenerationRunningChunk(
		t.Context(), spec.Repository, spec.Stage,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("settled schedule retained a running chunk: %v", err)
	}
	done, err := reader.GenerationChunkLeaseState(t.Context(), chunk.Identity)
	if err != nil || done.Status != GenerationChunkDone {
		t.Fatalf("settled lease state = %+v, %v", done, err)
	}
	settledProgress, err := reader.GenerationScheduleProgress(
		t.Context(), spec.Repository, spec.Stage,
	)
	if err != nil || settledProgress.Status != GenerationScheduleSettled ||
		settledProgress.Succeeded != 1 || settledProgress.Failed != 0 ||
		settledProgress.Failure != nil {
		t.Fatalf("settled success progress = %+v, %v", settledProgress, err)
	}

	failureSpec := generationSpec(
		"example.invalid/relationship-progress", "sha256:"+strings.Repeat("8", 64),
	)
	failureSpec.Stage = "service-relationship"
	failureSpec.TotalItems, failureSpec.ChunkItems = 1, 1
	failureSchedule, err := state.EnqueueGenerationSchedule(t.Context(), failureSpec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(
		t.Context(), failureSpec.Repository, failureSpec.Stage, failureSpec.Generation,
	); err != nil {
		t.Fatal(err)
	}
	failureChunk, err := state.ClaimGenerationChunk(
		t.Context(), failureSpec.ResourceClass, "relationship-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	activeProgress, err := reader.GenerationScheduleProgress(
		t.Context(), failureSpec.Repository, failureSpec.Stage,
	)
	if err != nil || activeProgress.Status != GenerationScheduleActive ||
		activeProgress.Running != 1 || activeProgress.Failure != nil {
		t.Fatalf("active relationship progress = %+v, %v", activeProgress, err)
	}
	closed := pipelinerefusal.Limit(
		errors.New("private relationship failure"),
		pipelinerefusal.StageRelationshipKafkaProjection,
		pipelinerefusal.GenerationRelationship,
		pipelinerefusal.DimensionResidentBytes,
		(160<<20)+1, 160<<20,
	)
	if err := state.FailGenerationChunk(
		t.Context(), *failureChunk, DurableErrorText(closed),
	); err != nil {
		t.Fatal(err)
	}
	progress, err := reader.GenerationScheduleProgress(
		t.Context(), failureSpec.Repository, failureSpec.Stage,
	)
	if err != nil || progress.Schema != GenerationScheduleProgressSchema ||
		progress.ScheduleDigest != failureSchedule.Digest ||
		progress.Generation != failureSpec.Generation ||
		progress.Status != GenerationScheduleSettled || progress.Total != 1 ||
		progress.Pending != 0 || progress.Running != 0 || progress.Succeeded != 0 ||
		progress.Failed != 1 || progress.Failure == nil ||
		progress.Failure.ScheduleDigest != failureSchedule.Digest ||
		progress.Failure.Refusal == nil ||
		progress.Failure.Refusal.Stage != pipelinerefusal.StageRelationshipKafkaProjection ||
		progress.Failure.Refusal.Dimension != pipelinerefusal.DimensionResidentBytes {
		t.Fatalf("settled relationship progress = %+v, %v", progress, err)
	}
}

func generationSpec(repository, generation string) GenerationScheduleSpec {
	return GenerationScheduleSpec{
		Repository: repository, Stage: "source-observation", Generation: generation,
		ResourceClass: GenerationResourceCPU, TotalItems: 130,
		ChunkItems: 2, MaxAttempts: 3, RepositoryTokens: 1,
	}
}

func TestGenerationResourceClassMigrationAdmitsExtractionSchedules(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	state, err := OpenLocalMemory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	results, err := surrealdb.Query[any](t.Context(), state.db, `
DELETE $marker;
DEFINE FIELD OVERWRITE resource_class ON generation_schedule TYPE string
	ASSERT $value INSIDE ['cpu', 'io', 'memory'];
DEFINE FIELD OVERWRITE resource_class ON generation_schedule_chunk TYPE string
	ASSERT $value INSIDE ['cpu', 'io', 'memory'];`, map[string]any{
		"marker": generationResourceClassMigrationID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("install legacy resource schema statement %d: %s", index, result.Error.Message)
		}
	}
	if err := state.migrateGenerationResourceClasses(t.Context()); err != nil {
		t.Fatal(err)
	}
	spec := generationSpec(
		"example.invalid/extraction-class", "sha256:"+strings.Repeat("e", 64),
	)
	spec.ResourceClass = GenerationResourceExtraction
	spec.TotalItems, spec.ChunkItems = 1, 1
	if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(
		t.Context(), spec.Repository, spec.Stage, spec.Generation,
	); err != nil {
		t.Fatal(err)
	}
	chunk, err := state.ClaimGenerationChunk(
		t.Context(), GenerationResourceExtraction, "extraction-worker",
	)
	if err != nil || chunk.ResourceClass != GenerationResourceExtraction {
		t.Fatalf("extraction chunk = %+v, %v", chunk, err)
	}
}

func TestGenerationScheduleValidationClosesProgressRows(t *testing.T) {
	spec := generationSpec("github.com/example/progress", "sha256:"+strings.Repeat("a", 64))
	digest, err := GenerationScheduleDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	active := GenerationSchedule{
		Schema: GenerationScheduleSchema, Digest: digest,
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ResourceClass: spec.ResourceClass, TotalItems: spec.TotalItems,
		ChunkItems:       spec.ChunkItems,
		TotalChunks:      int((spec.TotalItems + int64(spec.ChunkItems) - 1) / int64(spec.ChunkItems)),
		MaxAttempts:      spec.MaxAttempts,
		RepositoryTokens: spec.RepositoryTokens, Status: GenerationScheduleActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := ValidateGenerationSchedule(active); err != nil {
		t.Fatal(err)
	}
	settled := active
	settled.Status = GenerationScheduleSettled
	settled.NextOffset = settled.TotalItems
	settled.Materialized = settled.TotalChunks
	settled.Succeeded = settled.TotalChunks
	if err := ValidateGenerationSchedule(settled); err != nil {
		t.Fatal(err)
	}
	invalid := settled
	invalid.Pending = 1
	if err := ValidateGenerationSchedule(invalid); err == nil {
		t.Fatal("settled schedule with pending work validated")
	}
	invalid = active
	invalid.Digest = "sha256:" + strings.Repeat("b", 64)
	if err := ValidateGenerationSchedule(invalid); err == nil {
		t.Fatal("schedule with relabeled digest validated")
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
	if applied, err := store.reconcileGenerationCompletion(t.Context(), *chunk); err != nil || !applied {
		t.Fatalf("committed completion reconciliation = %v, %v", applied, err)
	}
	// A replayed completion reconciles against the durable done row under the
	// same claimant: idempotent success, with no accounting replay.
	if err := store.CompleteGenerationChunk(t.Context(), *chunk); err != nil {
		t.Fatalf("replayed completion = %v", err)
	}
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.CompleteGenerationChunk(canceledCtx, *chunk); err != nil {
		t.Fatalf("canceled replayed completion = %v", err)
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

func TestGenerationRepositoryTokenYieldsAcrossStagesAfterDeferral(t *testing.T) {
	state := newRunnerStore(t)
	repository := "example.invalid/cross-stage-yield"
	relationship := generationSpec(repository, "sha256:"+strings.Repeat("a", 64))
	relationship.Stage = "service-relationship"
	relationship.ResourceClass = GenerationResourceMemory
	relationship.TotalItems, relationship.ChunkItems = 1, 1
	extraction := generationSpec(repository, "sha256:"+strings.Repeat("b", 64))
	extraction.Stage = "extraction-partitions"
	extraction.ResourceClass = GenerationResourceExtraction
	extraction.TotalItems, extraction.ChunkItems = 1, 1
	for _, spec := range []GenerationScheduleSpec{relationship, extraction} {
		if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if _, err := state.ExpandGenerationSchedule(
			t.Context(), spec.Repository, spec.Stage, spec.Generation,
		); err != nil {
			t.Fatal(err)
		}
	}
	blocked, err := state.ClaimGenerationChunk(
		t.Context(), GenerationResourceMemory, "relationship-worker",
	)
	if err != nil || blocked == nil || blocked.Stage != relationship.Stage {
		t.Fatalf("relationship claim = %+v, %v", blocked, err)
	}
	if _, err := state.ClaimGenerationChunk(
		t.Context(), GenerationResourceExtraction, "extraction-worker",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repository token admitted extraction beside relationship = %v", err)
	}
	if err := state.DeferGenerationChunk(
		t.Context(), *blocked, "bounded mutation lock wait", time.Hour,
	); err != nil {
		t.Fatalf("defer relationship chunk: %v", err)
	}
	if err := state.DeferGenerationChunk(
		t.Context(), *blocked, "replayed deferral", time.Hour,
	); !errors.Is(err, ErrGenerationLeaseLost) {
		t.Fatalf("replayed deferral = %v, want lease fence", err)
	}
	deferred, err := state.generationChunkByIdentity(t.Context(), blocked.Identity)
	if err != nil || deferred == nil || deferred.Status != GenerationChunkPending || deferred.Attempt != 0 ||
		deferred.NotBefore == nil || deferred.NotBefore.Before(time.Now().Add(59*time.Minute)) {
		t.Fatalf("deferred relationship chunk = %+v, %v", deferred, err)
	}
	progress, err := state.GetGenerationSchedule(
		t.Context(), repository, relationship.Stage,
	)
	if err != nil || progress == nil || progress.Pending != 1 || progress.Running != 0 ||
		progress.Materialized != 1 || progress.Failed != 0 {
		t.Fatalf("relationship after deferral = %+v, %v", progress, err)
	}
	claimed, err := state.ClaimGenerationChunk(
		t.Context(), GenerationResourceExtraction, "extraction-worker",
	)
	if err != nil || claimed == nil || claimed.Stage != extraction.Stage ||
		claimed.Repository != repository {
		t.Fatalf("cross-stage claim after bounded deferral = %+v, %v", claimed, err)
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

func TestGenerationExhaustionReconciliationFencesReclaimedWorker(t *testing.T) {
	state := newRunnerStore(t)
	spec := generationSpec(
		"example.invalid/exhaustion-reclaim",
		"sha256:"+strings.Repeat("8", 64),
	)
	spec.TotalItems, spec.ChunkItems, spec.MaxAttempts = 1, 1, 1
	if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(
		t.Context(), spec.Repository, spec.Stage, spec.Generation,
	); err != nil {
		t.Fatal(err)
	}
	original, err := state.ClaimGenerationChunk(
		t.Context(), spec.ResourceClass, "worker-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](t.Context(), state.db,
		"UPDATE $chunk SET heartbeat_at = $old RETURN NONE", map[string]any{
			"chunk": generationChunkRecordID(*original),
			"old":   time.Now().UTC().Add(-time.Hour),
		}); err != nil {
		t.Fatal(err)
	}
	if count, err := state.ReapStaleGenerationChunks(
		t.Context(), spec.ResourceClass, time.Minute,
	); err != nil || count != 1 {
		t.Fatalf("reap original worker = %d, %v", count, err)
	}
	reclaimed, err := state.ClaimGenerationChunk(
		t.Context(), spec.ResourceClass, "worker-b",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.RetryGenerationChunk(
		t.Context(), *reclaimed, "same failure", time.Now().UTC(),
	); !errors.Is(err, ErrGenerationExhausted) {
		t.Fatalf("reclaimed exhaustion = %v", err)
	}
	if state.reconcileGenerationExhaustion(
		t.Context(), *original, spec.MaxAttempts, "same failure",
	) {
		t.Fatal("original worker adopted reclaimed worker exhaustion")
	}
	if !state.reconcileGenerationExhaustion(
		t.Context(), *reclaimed, spec.MaxAttempts, "same failure",
	) {
		t.Fatal("reclaimed worker did not reconcile its exhaustion")
	}
}

func TestFailGenerationChunkFencesImmediateTerminalSettlement(t *testing.T) {
	tests := []struct {
		name            string
		mode            string
		wantErr         error
		wantChunkStatus GenerationChunkStatus
		wantStatus      GenerationScheduleStatus
		wantFailed      int
		wantRunning     int
	}{
		{
			name: "current lease settles without retry", mode: "current",
			wantChunkStatus: GenerationChunkFailed,
			wantStatus:      GenerationScheduleSettled, wantFailed: 1,
		},
		{
			name: "superseded generation cancels without contribution", mode: "stale",
			wantErr: ErrGenerationStale, wantChunkStatus: GenerationChunkCanceled,
			wantStatus: GenerationScheduleActive,
		},
		{
			name: "lost lease cannot mutate", mode: "lease-lost",
			wantErr: ErrGenerationLeaseLost, wantChunkStatus: GenerationChunkRunning,
			wantStatus: GenerationScheduleActive, wantRunning: 1,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newRunnerStore(t)
			spec := generationSpec(
				fmt.Sprintf("example.invalid/terminal-%d", index),
				"sha256:"+fmt.Sprintf("%064d", index+100),
			)
			spec.TotalItems = 1
			spec.ChunkItems = 1
			if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
				t.Fatal(err)
			}
			if _, err := state.ExpandGenerationSchedule(
				t.Context(), spec.Repository, spec.Stage, spec.Generation,
			); err != nil {
				t.Fatal(err)
			}
			chunk, err := state.ClaimGenerationChunk(
				t.Context(), spec.ResourceClass, "terminal-worker",
			)
			if err != nil {
				t.Fatal(err)
			}
			originalLease := chunk.LeaseToken
			currentSpec := spec
			switch test.mode {
			case "current":
			case "stale":
				currentSpec.Generation = "sha256:" + fmt.Sprintf("%064d", index+200)
				if _, err := state.EnqueueGenerationSchedule(t.Context(), currentSpec); err != nil {
					t.Fatal(err)
				}
			case "lease-lost":
				chunk.LeaseToken = "wrong-lease"
			default:
				t.Fatalf("unknown test mode %q", test.mode)
			}

			err = state.FailGenerationChunk(t.Context(), *chunk, "terminal refusal")
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("FailGenerationChunk error = %v, want %v", err, test.wantErr)
			}
			stored, err := state.generationChunkByIdentity(t.Context(), chunk.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != test.wantChunkStatus {
				t.Fatalf("chunk status = %q, want %q", stored.Status, test.wantChunkStatus)
			}
			if test.mode == "lease-lost" {
				if stored.Error != "" || stored.FinishedAt != nil || stored.LeaseToken != originalLease {
					t.Fatalf("lost lease mutated chunk: %+v", stored)
				}
			} else if stored.Error != "terminal refusal" || stored.FinishedAt == nil ||
				stored.LeaseToken != "" {
				t.Fatalf("terminal chunk settlement = %+v", stored)
			}
			current, err := state.GetGenerationSchedule(
				t.Context(), currentSpec.Repository, currentSpec.Stage,
			)
			if err != nil {
				t.Fatal(err)
			}
			if current.Generation != currentSpec.Generation || current.Status != test.wantStatus ||
				current.Failed != test.wantFailed || current.Running != test.wantRunning {
				t.Fatalf("current schedule = %+v", current)
			}
		})
	}
}

func TestGenerationScheduleFailureExposesOnlyClosedRefusal(t *testing.T) {
	state := newRunnerStore(t)
	spec := generationSpec(
		"example.invalid/failure-projection",
		"sha256:"+strings.Repeat("a", 64),
	)
	spec.TotalItems, spec.ChunkItems = 1, 1
	schedule, err := state.EnqueueGenerationSchedule(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(
		t.Context(), spec.Repository, spec.Stage, spec.Generation,
	); err != nil {
		t.Fatal(err)
	}
	chunk, err := state.ClaimGenerationChunk(
		t.Context(), spec.ResourceClass, "failure-projection-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	closed := pipelinerefusal.Classified(
		errors.New("private planning detail"),
		pipelinerefusal.StageObservationPublication,
		pipelinerefusal.GenerationObservation,
		pipelinerefusal.ClassificationInvalid,
		pipelinerefusal.DimensionUnknown,
	)
	if err := state.FailGenerationChunk(
		t.Context(), *chunk, DurableErrorText(closed),
	); err != nil {
		t.Fatal(err)
	}
	failure, err := state.GetGenerationScheduleFailure(
		t.Context(), spec.Repository, spec.Stage, schedule.Digest,
	)
	if err != nil || failure.ScheduleDigest != schedule.Digest ||
		failure.Generation != spec.Generation || failure.Attempt != 0 ||
		failure.Refusal == nil ||
		failure.Refusal.Classification != pipelinerefusal.ClassificationInvalid {
		t.Fatalf("failure projection = %+v, %v", failure, err)
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
	selected, err := store.generationChunkByIdentity(t.Context(), second.Identity)
	if err != nil || selected.HeartbeatAt == nil {
		t.Fatalf("selected stale candidate = %+v, %v", selected, err)
	}
	if err := store.HeartbeatGenerationChunk(t.Context(), *second); err != nil {
		t.Fatal(err)
	}
	if err := store.releaseStaleGenerationChunk(
		t.Context(), *selected, time.Now().UTC().Add(-time.Minute),
	); !errors.Is(err, ErrGenerationLeaseLost) {
		t.Fatalf("reaper revoked a lease renewed after selection: %v", err)
	}
	if running, err := store.generationChunkByIdentity(t.Context(), second.Identity); err != nil ||
		running.Status != GenerationChunkRunning || running.LeaseToken != second.LeaseToken {
		t.Fatalf("renewed chunk after stale reaper = %+v, %v", running, err)
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
