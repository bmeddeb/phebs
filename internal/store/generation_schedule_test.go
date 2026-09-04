package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/readaccounting"
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
	schedule, err := state.EnqueueGenerationSchedule(t.Context(), spec)
	if err != nil {
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
	if err != nil || running.Identity != chunk.Identity ||
		running.ScheduleDigest != schedule.Digest || running.Repository != chunk.Repository ||
		running.Stage != chunk.Stage || running.Generation != chunk.Generation ||
		running.Attempt != chunk.Attempt || running.Priority != GenerationPriorityNeverRun ||
		running.Status != GenerationChunkRunning || !running.Leased {
		t.Fatalf("running lease state = %+v, %v", running, err)
	}
	selected, err := reader.CurrentGenerationRunningChunk(
		t.Context(), spec.Repository, spec.Stage,
	)
	if err != nil || selected != running {
		t.Fatalf("selected current running chunk = %+v, %v", selected, err)
	}
	if err := state.ReleaseGenerationChunk(t.Context(), *chunk, "restart"); err != nil {
		t.Fatal(err)
	}
	requeued, err := reader.GenerationChunkLeaseState(t.Context(), chunk.Identity)
	if err != nil || requeued.Status != GenerationChunkPending ||
		requeued.Priority != GenerationPriorityStale || requeued.Leased {
		t.Fatalf("requeued lease state = %+v, %v", requeued, err)
	}
	chunk, err = state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "probe-worker")
	if err != nil || chunk.Identity != requeued.Identity || chunk.Attempt != requeued.Attempt {
		t.Fatalf("reclaimed lease = %+v, %v", chunk, err)
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
	if err != nil || done.Status != GenerationChunkDone || done.Leased {
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
	retained, err := reader.GenerationScheduleRetentionState(
		t.Context(), spec.Repository, spec.Stage, schedule.Digest,
	)
	if err != nil || !retained.Present || !retained.CurrentPresent || !retained.Current ||
		retained.Status != GenerationScheduleSettled {
		t.Fatalf("current retained schedule = %+v, %v", retained, err)
	}
	successor := spec
	successor.Generation = "sha256:" + strings.Repeat("6", 64)
	if _, err := state.EnqueueGenerationSchedule(t.Context(), successor); err != nil {
		t.Fatal(err)
	}
	retired, err := reader.GenerationScheduleRetentionState(
		t.Context(), spec.Repository, spec.Stage, schedule.Digest,
	)
	if err != nil || !retired.Present || !retired.CurrentPresent || retired.Current ||
		retired.Status != GenerationScheduleSettled {
		t.Fatalf("superseded retained schedule = %+v, %v", retired, err)
	}
	settle := func(spec GenerationScheduleSpec) {
		t.Helper()
		if _, err := state.ExpandGenerationSchedule(
			t.Context(), spec.Repository, spec.Stage, spec.Generation,
		); err != nil {
			t.Fatal(err)
		}
		chunk, err := state.ClaimGenerationChunk(
			t.Context(), spec.ResourceClass, "retention-worker",
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.CompleteGenerationChunk(t.Context(), *chunk); err != nil {
			t.Fatal(err)
		}
	}
	settle(successor)
	secondSuccessor := successor
	secondSuccessor.Generation = "sha256:" + strings.Repeat("5", 64)
	if _, err := state.EnqueueGenerationSchedule(t.Context(), secondSuccessor); err != nil {
		t.Fatal(err)
	}
	settle(secondSuccessor)
	current := secondSuccessor
	current.Generation = "sha256:" + strings.Repeat("4", 64)
	if _, err := state.EnqueueGenerationSchedule(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	sweep, err := state.SweepGenerationScheduleLifecycle(
		t.Context(), "", 64, 16, 2,
	)
	if err != nil || sweep.Deleted < 2 {
		t.Fatalf("generation lifecycle sweep = %+v, %v", sweep, err)
	}
	collected, err := reader.GenerationScheduleRetentionState(
		t.Context(), spec.Repository, spec.Stage, schedule.Digest,
	)
	if err != nil || collected.Present || !collected.CurrentPresent || collected.Current ||
		collected.ScheduleDigest != schedule.Digest {
		t.Fatalf("collected schedule = %+v, %v", collected, err)
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

func TestRetireCurrentGenerationScheduleFencesExactActiveAndSettledRows(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	dataDir := t.TempDir()
	state, err := OpenLocal(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	reader, err := OpenLocalGenerationChunkReader(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(context.Background()) })

	for index, settled := range []bool{false, true} {
		name := "active"
		if settled {
			name = "settled"
		}
		t.Run(name, func(t *testing.T) {
			spec := generationSpec(
				fmt.Sprintf("example.invalid/retire-%d", index),
				"sha256:"+strings.Repeat(fmt.Sprintf("%x", index+1), 64),
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
				t.Context(), spec.ResourceClass, "retire-worker",
			)
			if err != nil {
				t.Fatal(err)
			}
			if settled {
				if err := state.CompleteGenerationChunk(t.Context(), *chunk); err != nil {
					t.Fatal(err)
				}
			}
			current, err := state.GetGenerationSchedule(t.Context(), spec.Repository, spec.Stage)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.RetireCurrentGenerationSchedule(t.Context(), *current); err != nil {
				t.Fatal(err)
			}
			if _, err := state.GetGenerationSchedule(
				t.Context(), spec.Repository, spec.Stage,
			); !errors.Is(err, ErrNotFound) {
				t.Fatalf("retired current schedule = %v", err)
			}
			if !settled {
				if err := state.CompleteGenerationChunk(t.Context(), *chunk); !errors.Is(err, ErrGenerationStale) {
					t.Fatalf("retired active completion = %v", err)
				}
			}
			retained, err := reader.GenerationScheduleRetentionState(
				t.Context(), spec.Repository, spec.Stage, schedule.Digest,
			)
			wantStatus := GenerationScheduleSuperseded
			if settled {
				wantStatus = GenerationScheduleSettled
			}
			if err != nil || !retained.Present || retained.CurrentPresent || retained.Current ||
				retained.Status != wantStatus {
				t.Fatalf("retired exact schedule = %+v, %v", retained, err)
			}
			if err := state.RetireCurrentGenerationSchedule(
				t.Context(), *current,
			); !errors.Is(err, ErrGenerationStale) {
				t.Fatalf("repeated retirement = %v", err)
			}
			successorSpec := spec
			successorSpec.Generation = "sha256:" + strings.Repeat(fmt.Sprintf("%x", index+3), 64)
			successor, err := state.EnqueueGenerationSchedule(t.Context(), successorSpec)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.RetireCurrentGenerationSchedule(
				t.Context(), *current,
			); !errors.Is(err, ErrGenerationStale) {
				t.Fatalf("stale retirement beside successor = %v", err)
			}
			selected, err := state.GetGenerationSchedule(t.Context(), spec.Repository, spec.Stage)
			if err != nil || selected.Digest != successor.Digest {
				t.Fatalf("successor after stale retirement = %+v, %v", selected, err)
			}
		})
	}
}

func TestClearAllGenerationScheduleStateForRestore(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	state, err := OpenLocal(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	spec := generationSpec("example.invalid/restore", "sha256:"+strings.Repeat("9", 64))
	spec.TotalItems, spec.ChunkItems = 1, 1
	if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "restore-worker"); err != nil {
		t.Fatal(err)
	}
	if err := state.ClearAllGenerationScheduleStateForRestore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.GetGenerationSchedule(t.Context(), spec.Repository, spec.Stage); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restored current schedule survived clear: %v", err)
	}
	if _, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "restore-worker"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restored chunk survived clear: %v", err)
	}
}

func TestLocalGenerationDiagnosticFenceMakesRunningCompletionStale(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	dataDir := t.TempDir()
	state, err := OpenLocal(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	spec := generationSpec("example.invalid/diagnostic-fence", "sha256:"+strings.Repeat("6", 64))
	spec.TotalItems, spec.ChunkItems = 1, 1
	if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation); err != nil {
		t.Fatal(err)
	}
	chunk, err := state.ClaimGenerationChunk(t.Context(), spec.ResourceClass, "diagnostic-worker")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenLocalGenerationChunkReader(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(context.Background()) })
	selected, err := reader.GenerationChunkLeaseState(t.Context(), chunk.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.FenceCurrentGenerationScheduleForDiagnostic(t.Context(), selected); err != nil {
		t.Fatal(err)
	}
	if err := state.CompleteGenerationChunk(t.Context(), *chunk); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("diagnostically superseded completion = %v, want stale", err)
	}
	settled, err := reader.GenerationChunkLeaseState(t.Context(), chunk.Identity)
	if err != nil || settled.Status != GenerationChunkCanceled {
		t.Fatalf("diagnostically superseded lease = %+v, %v", settled, err)
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

func TestGenerationStaleLeaseTransitionObservationAndPointReader(t *testing.T) {
	state := newRunnerStore(t)
	spec := generationSpec(
		"example.invalid/observed-stale-lease",
		"sha256:"+strings.Repeat("8", 64),
	)
	spec.TotalItems, spec.ChunkItems = 2, 1
	schedule, err := state.EnqueueGenerationSchedule(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ExpandGenerationSchedule(
		t.Context(), spec.Repository, spec.Stage, spec.Generation,
	); err != nil {
		t.Fatal(err)
	}
	claimed, err := state.ClaimGenerationChunk(
		t.Context(), spec.ResourceClass, "stale-lease-first",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](t.Context(), state.db,
		"UPDATE $chunk SET claimed_at = $claim, heartbeat_at = $old RETURN NONE", map[string]any{
			"chunk": generationChunkRecordID(*claimed),
			"claim": now.Add(-2 * time.Hour), "old": now.Add(-time.Hour),
		}); err != nil {
		t.Fatal(err)
	}
	stale, err := state.generationChunkByIdentity(t.Context(), claimed.Identity)
	if err != nil {
		t.Fatal(err)
	}

	observerFailure := errors.New("hit reader refused")
	reaped, err := state.ReapStaleGenerationChunksObserved(
		t.Context(), spec.ResourceClass, time.Minute,
		func(_ context.Context, transition GenerationStaleLeaseTransition) error {
			if transition.Point != GenerationStaleLeaseTransitionHit ||
				transition.ChunkIdentity != claimed.Identity || transition.StaleBefore.IsZero() {
				t.Fatalf("hit transition = %+v", transition)
			}
			return observerFailure
		},
	)
	if reaped != 0 || !errors.Is(err, observerFailure) {
		t.Fatalf("refused observation = %d, %v", reaped, err)
	}
	preserved, err := state.generationChunkByIdentity(t.Context(), claimed.Identity)
	if err != nil || generationStaleLeaseChunkStateDigest(*preserved) !=
		generationStaleLeaseChunkStateDigest(*stale) {
		t.Fatalf("refused hit mutated lease = %+v, %v", preserved, err)
	}

	requestFor := func(transition GenerationStaleLeaseTransition) GenerationStaleLeaseTransitionRequest {
		return GenerationStaleLeaseTransitionRequest{
			Point: transition.Point, Repository: transition.Repository,
			Stage: transition.Stage, Generation: transition.Generation,
			ResourceClass: transition.ResourceClass, ScheduleDigest: transition.ScheduleDigest,
			ChunkIdentity: transition.ChunkIdentity, Offset: transition.Offset,
			Length: transition.Length, Attempt: transition.Attempt,
			StaleBefore: transition.StaleBefore,
		}
	}
	active, err := state.GetGenerationSchedule(t.Context(), spec.Repository, spec.Stage)
	if err != nil {
		t.Fatal(err)
	}
	hitRequest := requestFor(generationStaleLeaseTransitionFromChunk(
		GenerationStaleLeaseTransitionHit, *stale, now.Add(-30*time.Minute),
	))
	if !validGenerationStaleLeaseTransitionShape(hitRequest, *active, *stale) {
		t.Fatal("valid stale lease hit was rejected")
	}
	for name, mutate := range map[string]func(*GenerationChunk){
		"untrimmed worker": func(chunk *GenerationChunk) { chunk.ClaimedBy = " invalid" },
		"invalid worker":   func(chunk *GenerationChunk) { chunk.ClaimedBy = string([]byte{0xff}) },
		"oversized worker": func(chunk *GenerationChunk) {
			chunk.ClaimedBy = strings.Repeat("w", MaxGenerationWorkerBytes+1)
		},
		"noncanonical lease": func(chunk *GenerationChunk) { chunk.LeaseToken = "lease" },
		"reversed heartbeat": func(chunk *GenerationChunk) {
			value := chunk.ClaimedAt.Add(-time.Second)
			chunk.HeartbeatAt = &value
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := *stale
			mutate(&changed)
			if validGenerationStaleLeaseTransitionShape(hitRequest, *active, changed) {
				t.Fatal("malformed stale lease hit was accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*GenerationSchedule){
		"double-counted terminal": func(schedule *GenerationSchedule) { schedule.Succeeded++ },
		"running over token limit": func(schedule *GenerationSchedule) {
			schedule.Pending--
			schedule.Running++
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := *active
			mutate(&changed)
			if validGenerationStaleLeaseTransitionShape(hitRequest, changed, *stale) {
				t.Fatal("impossible stale lease schedule counters were accepted")
			}
		})
	}
	var points []GenerationStaleLeaseTransitionPoint
	requeueFailure := errors.New("requeue signal refused")
	reaped, err = state.ReapStaleGenerationChunksObserved(
		t.Context(), spec.ResourceClass, time.Minute,
		func(ctx context.Context, transition GenerationStaleLeaseTransition) error {
			points = append(points, transition.Point)
			switch transition.Point {
			case GenerationStaleLeaseTransitionHit:
				readCtx, ledger, startErr := readaccounting.Start(ctx, readaccounting.Counts{
					StoreReadAttempts: GenerationStaleLeaseTransitionStoreReadAttempts,
				})
				if startErr != nil {
					t.Fatal(startErr)
				}
				request := requestFor(transition)
				before, beforeErr := state.ReadGenerationStaleLeaseTransition(readCtx, request)
				after, afterErr := state.ReadGenerationStaleLeaseTransition(readCtx, request)
				counts, finishErr := ledger.Finish()
				if errors.Join(beforeErr, afterErr, finishErr) != nil ||
					before != transition || before != after ||
					counts != (readaccounting.Counts{
						StoreReadAttempts: GenerationStaleLeaseTransitionStoreReadAttempts,
					}) {
					t.Fatalf(
						"hit snapshots/counts = %+v / %+v / %+v, errors=%v",
						before, after, counts, errors.Join(beforeErr, afterErr, finishErr),
					)
				}
			case GenerationStaleLeaseTransitionRequeued:
				if transition.ChunkStatus != GenerationChunkPending ||
					transition.Priority != GenerationPriorityStale || transition.Leased {
					t.Fatalf("requeued transition = %+v", transition)
				}
				return requeueFailure
			default:
				t.Fatalf("unexpected transition = %+v", transition)
			}
			return nil
		},
	)
	if reaped != 1 || !errors.Is(err, requeueFailure) ||
		!slices.Equal(points, []GenerationStaleLeaseTransitionPoint{
			GenerationStaleLeaseTransitionHit,
			GenerationStaleLeaseTransitionRequeued,
		}) {
		t.Fatalf("observed reaping = %d, %v, points=%v", reaped, err, points)
	}
	requeued, err := state.generationChunkByIdentity(t.Context(), claimed.Identity)
	if err != nil || requeued.Status != GenerationChunkPending ||
		requeued.Priority != GenerationPriorityStale || requeued.LeaseToken != "" {
		t.Fatalf("durable requeue after observer error = %+v, %v", requeued, err)
	}
	if err := state.CompleteGenerationChunk(t.Context(), *claimed); !errors.Is(err, ErrGenerationLeaseLost) {
		t.Fatalf("old lease completed after requeue: %v", err)
	}
	recovered, err := state.ClaimGenerationChunk(
		t.Context(), spec.ResourceClass, "stale-lease-recovered",
	)
	recoveredFirst := err == nil && recovered != nil && recovered.Identity == claimed.Identity
	if err == nil && recovered != nil && !recoveredFirst {
		if err = state.CompleteGenerationChunk(t.Context(), *recovered); err == nil {
			recovered, err = state.ClaimGenerationChunk(
				t.Context(), spec.ResourceClass, "stale-lease-recovered",
			)
		}
	}
	if err != nil || recovered == nil || recovered.Identity != claimed.Identity ||
		recovered.Attempt != 0 || recovered.Priority != GenerationPriorityStale {
		t.Fatalf("recovered claim = %+v, %v", recovered, err)
	}
	if err := state.CompleteGenerationChunk(t.Context(), *recovered); err != nil {
		t.Fatal(err)
	}
	if recoveredFirst {
		remaining, err := state.ClaimGenerationChunk(
			t.Context(), spec.ResourceClass, "stale-lease-final",
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.CompleteGenerationChunk(t.Context(), *remaining); err != nil {
			t.Fatal(err)
		}
	}
	recoveredRequest := GenerationStaleLeaseTransitionRequest{
		Point:      GenerationStaleLeaseTransitionRecovered,
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ResourceClass: spec.ResourceClass, ScheduleDigest: schedule.Digest,
		ChunkIdentity: recovered.Identity, Offset: recovered.Offset,
		Length: recovered.Length, Attempt: recovered.Attempt,
	}
	readCtx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
		StoreReadAttempts: GenerationStaleLeaseTransitionStoreReadAttempts,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, beforeErr := state.ReadGenerationStaleLeaseTransition(readCtx, recoveredRequest)
	after, afterErr := state.ReadGenerationStaleLeaseTransition(readCtx, recoveredRequest)
	counts, finishErr := ledger.Finish()
	if errors.Join(beforeErr, afterErr, finishErr) != nil || before != after ||
		before.Point != GenerationStaleLeaseTransitionRecovered ||
		before.ScheduleStatus != GenerationScheduleSettled ||
		before.ChunkStatus != GenerationChunkDone || before.Leased ||
		counts != (readaccounting.Counts{
			StoreReadAttempts: GenerationStaleLeaseTransitionStoreReadAttempts,
		}) {
		t.Fatalf(
			"recovered snapshots/counts = %+v / %+v / %+v, errors=%v",
			before, after, counts, errors.Join(beforeErr, afterErr, finishErr),
		)
	}
	limitedCtx, limitedLedger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
		StoreReadAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, limitedErr := state.ReadGenerationStaleLeaseTransition(limitedCtx, recoveredRequest)
	limitedCounts, finishErr := limitedLedger.Finish()
	if !errors.Is(errors.Join(limitedErr, finishErr), readaccounting.ErrLimit) ||
		limitedCounts != (readaccounting.Counts{StoreReadAttempts: 2}) {
		t.Fatalf("limited point read = %+v, %v", limitedCounts, limitedErr)
	}
}
