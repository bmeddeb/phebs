package store

import (
	"errors"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// This checks native claim admission, not state projection or lifecycle
// completion. The selected-successor regression covers those actual handlers.
func TestGenerationStateClaimAppliedPrefix(t *testing.T) {
	state := newRunnerStore(t)
	for _, phase := range []string{serviceStateV3Reconcile, serviceStateV3Activate} {
		t.Run(phase, func(t *testing.T) {
			ctx := t.Context()
			plan, _, _ := serviceStateV3HeadroomFixture(t, phase, 0)
			plan.Repository = "example.invalid/serial-" + phase
			plan.BaseChunk, plan.NextChunk, plan.TotalChunks = 7, 7, 9
			plan.ServiceMemberChunks, plan.Repair = 8, 1
			plan.Digest = serviceStateV3PlanDigest(plan.Repository, phase, plan.CatalogRoot,
				plan.CatalogControlRevision, plan.SearchGeneration, plan.Repair)
			spec := generationSpec(plan.Repository, plan.Digest)
			spec.Stage, spec.TotalItems, spec.ChunkItems = serviceStateV3Stage(phase), 2, 1
			schedule, err := state.EnqueueGenerationSchedule(ctx, spec)
			if err != nil {
				t.Fatal(err)
			}
			plan.ScheduleDigest = schedule.Digest
			if _, err := state.ExpandGenerationSchedule(ctx, spec.Repository, spec.Stage, spec.Generation); err != nil {
				t.Fatal(err)
			}
			refuse := func() {
				t.Helper()
				for range 3 {
					if chunk, err := state.ClaimGenerationChunk(ctx, spec.ResourceClass, "serial-worker"); chunk != nil || !errors.Is(err, ErrNotFound) {
						t.Fatalf("unready prefix claimed %+v: %v", chunk, err)
					}
				}
			}
			writePlan := func() {
				t.Helper()
				if err := validateServiceStateV3Plan(plan); err != nil {
					t.Fatal(err)
				}
				if _, err := surrealdb.Query[any](ctx, state.db, "UPSERT $rid CONTENT $content RETURN NONE", map[string]any{
					"rid": serviceStateV3PlanID(plan.Digest), "content": serviceStateV3PlanContent(plan),
				}); err != nil {
					t.Fatal(err)
				}
			}
			refuse() // Enqueue/expand can precede the owning plan creation.
			// Counterfactual/corruption fence, not a real terminal transition:
			// a failed counter must not bypass missing or mismatched identity.
			setFailed := func(failed int) {
				t.Helper()
				if _, err := surrealdb.Query[any](ctx, state.db, "UPDATE $rid SET failed = $failed RETURN NONE", map[string]any{
					"rid": models.NewRecordID("generation_schedule", schedule.Digest[7:]), "failed": failed,
				}); err != nil {
					t.Fatal(err)
				}
			}
			setFailed(1)
			refuse()
			plan.ScheduleDigest = generationSchedulerDigest("test", "wrong schedule")
			writePlan()
			refuse()
			setFailed(0)
			refuse()
			plan.ScheduleDigest = schedule.Digest
			writePlan()
			first, err := state.ClaimGenerationChunk(ctx, spec.ResourceClass, "serial-worker")
			if err != nil || first.Offset != 0 || first.Attempt != 0 {
				t.Fatalf("first repaired-plan offset: %+v, %v", first, err)
			}
			if err := state.DeferGenerationChunk(ctx, *first, "prerequisite unavailable", time.Hour); err != nil {
				t.Fatal(err)
			}
			refuse() // Higher never-run offset must not consume a lease/retry.
			// A real handler may apply then defer its completion continuation.
			// Its older idempotent offset must remain eligible after progress.
			plan.NextChunk++
			writePlan()
			variables := map[string]any{
				"current":          models.NewRecordID("generation_schedule_current", generationCurrentID(spec.Repository, spec.Stage)[7:]),
				"schedule":         models.NewRecordID("generation_schedule", schedule.Digest[7:]),
				"repository_state": models.NewRecordID("generation_schedule_repository", generationRepositoryID(spec.Repository)[7:]),
				"digest":           schedule.Digest, "repository_tokens": spec.RepositoryTokens,
				"worker": "serial-race", "lease": "serial-race-lease",
				"state_phase": phase, "state_plan_rid": serviceStateV3PlanID(plan.Digest),
				"generation": plan.Digest, "repository": plan.Repository,
			}
			selected, err := surrealdb.Query[[]models.RecordID](ctx, state.db,
				claimGenerationChunkSelectionSQL+`RETURN IF $eligible THEN [$candidate] ELSE [] END;`, variables)
			if err != nil {
				t.Fatal(err)
			}
			ids, err := generationMutationIDs(ctx, selected, "generation_schedule_chunk", 1, 6)
			if err != nil || len(ids) != 1 {
				t.Fatal("native selection", ids, err)
			}
			// Counterfactual mutation after census must be rechecked inside
			// the claim transaction, not trusted from the earlier point read.
			plan.NextChunk--
			writePlan()
			variables["chunk"] = ids[0]
			blocked, err := surrealdb.Query[[]generationChunkRec](ctx, state.db, claimGenerationChunkSQL, variables)
			if err != nil || len(generationChunkRows(blocked)) != 0 {
				t.Fatal("native claim ignored changed prerequisite", err)
			}
			plan.NextChunk++
			writePlan()
			second, err := state.ClaimGenerationChunk(ctx, spec.ResourceClass, "serial-worker")
			if err != nil || second.Offset != 1 || second.Attempt != 0 {
				t.Fatalf("applied prefix did not unblock successor: %+v, %v", second, err)
			}
			if err := state.CompleteGenerationChunk(ctx, *second); err != nil {
				t.Fatal(err)
			}
			if _, err := surrealdb.Query[any](ctx, state.db, "UPDATE $rid SET not_before = $past RETURN NONE", map[string]any{
				"rid": generationChunkRecordID(*first), "past": time.Now().Add(-time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
			reclaimed, err := state.ClaimGenerationChunk(ctx, spec.ResourceClass, "serial-worker")
			if err != nil || reclaimed.ID != first.ID || reclaimed.Attempt != 0 || reclaimed.LeaseToken == first.LeaseToken {
				t.Fatalf("already-applied continuation: %+v, %v", reclaimed, err)
			}
			if err := state.CompleteGenerationChunk(ctx, *reclaimed); err != nil {
				t.Fatal(err)
			}
			actual, err := state.GetGenerationSchedule(ctx, spec.Repository, spec.Stage)
			if err != nil || actual.Materialized != 2 || actual.Succeeded != 2 || actual.Failed != 0 {
				t.Fatalf("unexpected retry materialization: %+v, %v", actual, err)
			}
		})
	}
}
