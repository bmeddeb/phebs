//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// All scans and mutations below target a new bounded test-owned database.
// The three reads are discriminators, not additional production reader RPCs.
func TestGenerationRecoveredCompletionReadViewsAndLifecycle(t *testing.T) {
	for _, hardDeath := range []bool{false, true} {
		t.Run(fmt.Sprint("hard_death_", hardDeath), func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			state, err := OpenLocal(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := state.Close(bounded); err != nil {
					t.Error("test-owned closure", err)
				}
			}()
			const repository = "example.invalid/v21-reader-views"
			enqueue := func(version int, total int64) *GenerationSchedule {
				t.Helper()
				spec := generationSpec(repository, "sha256:"+fmt.Sprintf("%064d", version))
				spec.Stage, spec.ResourceClass, spec.TotalItems, spec.ChunkItems, spec.RepositoryTokens = "extraction-partitions", GenerationResourceExtraction, total, 1, 1
				schedule, err := state.EnqueueGenerationSchedule(ctx, spec)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := state.ExpandGenerationSchedule(ctx, repository, spec.Stage, spec.Generation); err != nil {
					t.Fatal(err)
				}
				return schedule
			}
			claim := func() *GenerationChunk {
				t.Helper()
				chunk, err := state.ClaimGenerationChunk(ctx, GenerationResourceExtraction, "v21-real-views")
				if err != nil || chunk == nil {
					t.Fatal("claim refused", err)
				}
				return chunk
			}
			complete := func(chunk GenerationChunk) {
				t.Helper()
				if err := state.CompleteGenerationChunk(ctx, chunk); err != nil {
					t.Fatal(err)
				}
			}
			for _, version := range []int{1, 2} {
				_ = enqueue(version, 1)
				complete(*claim())
			}
			schedule := enqueue(3, 56)
			var selected *GenerationChunk
			for offset := int64(0); offset <= 29; offset++ {
				selected = claim()
				if selected.Offset != offset {
					t.Fatal("cold offset differs")
				}
				if offset != 29 {
					complete(*selected)
				}
			}
			if hardDeath {
				if state.engine == nil || state.engine.process == nil {
					t.Fatal("exact test-owned child unavailable")
				}
				if err := state.engine.process.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			if err := state.Close(ctx); err != nil && !hardDeath {
				t.Fatal(err)
			}
			state, err = OpenLocal(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			// The lease cutoff remains twenty seconds; only this owned fixture's prior
			// heartbeat is old. Production clocks, TTLs and callback budgets are intact.
			if _, err := surrealdb.Query[any](ctx, state.db, "UPDATE $rid SET claimed_at=$past, heartbeat_at=$past RETURN NONE", map[string]any{"rid": generationChunkRecordID(*selected), "past": time.Now().UTC().Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
			n, err := state.ReapStaleGenerationChunksObserved(ctx, GenerationResourceExtraction, 20*time.Second, func(context.Context, GenerationStaleLeaseTransition) error { return nil })
			if err != nil || n != 1 {
				t.Fatal("actual reaper refused", err)
			}
			// Preserve actual queue priority: never-run=0 precedes stale=2.
			// With one repository token every trailing completion commits before
			// the selected original stale chunk can be reclaimed.
			for offset := int64(30); offset < 56; offset++ {
				tail := claim()
				if tail.Offset != offset || tail.Priority != GenerationPriorityNeverRun || tail.Attempt != 0 {
					t.Fatal("actual trailing claim order differs")
				}
				complete(*tail)
			}
			recovered := claim()
			if recovered.Identity != selected.Identity || recovered.ID != selected.ID || recovered.Attempt != 0 || recovered.Priority != GenerationPriorityStale {
				t.Fatal("recovered identity differs")
			}
			complete(*recovered)
			local, err := ReadLocalRuntime(dir)
			if err != nil {
				t.Fatal("owned descriptor refused")
			}
			selectedCtx, owner, _ := storeAccountingFixture(t, 40, 2)
			sdk, err := openLocalRootWithOwner(selectedCtx, local, owner)
			if err != nil {
				t.Fatal("actual selected connection refused", err)
			}
			defer func() {
				if err := sdk.Close(context.Background()); err != nil {
					t.Error("selected connection closure refused")
				}
			}()
			vars := map[string]any{"identity": recovered.Identity, "current": models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(repository, "extraction-partitions"), "sha256:"))}
			compareViews := func() {
				t.Helper()
				nested, err := storeQuery[[]generationStaleLeaseChunkFenceRec](selectedCtx, sdk.accounting, sdk.db, generationTransitionReadSQL, vars, storeWrite(0))
				if err != nil {
					t.Fatal("nested view refused", err)
				}
				var fenced []generationStaleLeaseChunkFenceRec
				for _, result := range *nested {
					if len(result.Result) > 0 {
						fenced = result.Result
						break
					}
				}
				if len(fenced) != 1 || fenced[0].CurrentScheduleDigest != schedule.Digest {
					t.Fatal("nested fence differs")
				}
				expected, err := fenced[0].Chunk.chunk()
				if err != nil || expected.ID != recovered.ID || expected.Identity != recovered.Identity {
					t.Fatal("nested original row unavailable", err)
				}
				for _, query := range []string{"SELECT * FROM $rid LIMIT 1", "SELECT * FROM generation_schedule_chunk WITH NOINDEX WHERE identity=$identity LIMIT 1", "SELECT * FROM generation_schedule_chunk WITH INDEX generation_schedule_chunk_identity WHERE identity=$identity LIMIT 1"} {
					results, err := storeQuery[[]generationChunkRec](selectedCtx, sdk.accounting, sdk.db, query, map[string]any{"rid": generationChunkRecordID(*recovered), "identity": recovered.Identity}, storeRead())
					if err != nil {
						t.Fatal("comparison view refused", err)
					}
					rows := generationChunkRows(results)
					if len(rows) != 1 {
						t.Fatal("comparison view cardinality differs")
					}
					actual, err := rows[0].chunk()
					if err != nil || !reflect.DeepEqual(expected, actual) {
						t.Fatal("nested/direct/scan/index views disagree", err)
					}
				}
			}
			compareViews()
			request := GenerationStaleLeaseTransitionRequest{Point: GenerationStaleLeaseTransitionRecovered, Repository: repository, Stage: "extraction-partitions", Generation: schedule.Generation, ResourceClass: GenerationResourceExtraction, ScheduleDigest: schedule.Digest, ChunkIdentity: recovered.Identity, Offset: 29, Length: 1, Attempt: 0}
			read := func(wantErr error, wantReads uint64) {
				t.Helper()
				accounted, ledger, err := readaccounting.Start(selectedCtx, readaccounting.Counts{StoreReadAttempts: 2})
				if err != nil {
					t.Fatal(err)
				}
				value, failure := sdk.ReadGenerationStaleLeaseTransition(accounted, request)
				counts, finish := ledger.Finish()
				if finish != nil || counts != (readaccounting.Counts{StoreReadAttempts: wantReads}) {
					t.Fatal("reader accounting differs", counts, finish)
				}
				if wantErr != nil {
					if !errors.Is(failure, wantErr) || errors.Is(failure, ErrGenerationLeaseLost) || value != (GenerationStaleLeaseTransition{}) {
						t.Fatal("reader granted timing/retirement credit", failure)
					}
				} else if failure != nil || value.ChunkIdentity != recovered.Identity || value.Attempt != 0 {
					t.Fatal("settled original reader refused", failure)
				}
			}
			// The first read is immediately after genuine selected completion;
			// it receives no artificial settlement wait or row reconstruction.
			read(nil, 2)
			sweep, err := sdk.SweepGenerationScheduleLifecycle(selectedCtx, "", 64, 64, 1)
			if err != nil || sweep.Deleted != 4 {
				t.Fatal("older schedule collection did not execute", sweep, err)
			}
			compareViews()
			read(nil, 2)
			// Once the selected schedule is genuinely superseded and collected, its
			// reader fails at the earlier schedule fence, not nil ID after that fence.
			_ = enqueue(4, 1)
			complete(*claim())
			sweep, err = sdk.SweepGenerationScheduleLifecycle(selectedCtx, "", 64, 64, 1)
			if err != nil || sweep.Deleted != 57 {
				t.Fatal("superseded collection differs", sweep, err)
			}
			read(ErrGenerationStale, 1)
			t.Log("views agree immediately after original recovery and after real current-safe GC; superseded collection refuses at the schedule fence")
		})
	}
}
