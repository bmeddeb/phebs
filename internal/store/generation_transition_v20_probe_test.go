//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Component discriminator for V20's missing record-ID refusal. Every database
// and runtime below is newly test-owned. This neither reopens retained native
// evidence nor establishes a native cause, custody result or acceptance pass.
func TestV20RecoveredReaderPersistentSelectedSDKProbe(t *testing.T) {
	for _, shape := range []struct {
		name            string
		total, selected int64
	}{{"tiny", 3, 2}, {"native_schedule", 56, 29}} {
		for _, restart := range []struct {
			name                  string
			persistent, hardDeath bool
		}{{"memory", false, false}, {"persistent_restart", true, false}, {"hard_death_restart", true, true}} {
			name, persistent := restart.name, restart.persistent
			t.Run(shape.name+"/"+name, func(t *testing.T) {
				ctx := t.Context()
				dir := t.TempDir()
				open := OpenLocalMemory
				if persistent {
					open = OpenLocal
				}
				state, err := open(ctx, dir)
				if err != nil {
					t.Fatal("test-owned engine refused")
				}
				t.Cleanup(func() {
					bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if err := state.Close(bounded); err != nil {
						t.Error("test-owned engine closure refused")
					}
				})
				spec := generationSpec("example.invalid/v20-reader-"+shape.name+"-"+name, "sha256:"+strings.Repeat("7", 64))
				spec.Stage, spec.ResourceClass, spec.TotalItems, spec.ChunkItems, spec.RepositoryTokens = "extraction-partitions", GenerationResourceExtraction, shape.total, 1, 2
				schedule, err := state.EnqueueGenerationSchedule(ctx, spec)
				if err != nil {
					t.Fatal("schedule refused")
				}
				if _, err := state.ExpandGenerationSchedule(ctx, spec.Repository, spec.Stage, spec.Generation); err != nil {
					t.Fatal("expand refused")
				}
				var selected *GenerationChunk
				for offset := int64(0); offset < shape.total; offset++ {
					chunk, err := state.ClaimGenerationChunk(ctx, spec.ResourceClass, "v20-prior")
					if err != nil || chunk == nil || chunk.Offset != offset || chunk.Attempt != 0 {
						t.Fatal("claim refused")
					}
					if offset == shape.selected {
						selected = chunk
						continue
					}
					if err := state.CompleteGenerationChunk(ctx, *chunk); err != nil {
						t.Fatal("predecessor completion refused")
					}
				}
				request := GenerationStaleLeaseTransitionRequest{Point: GenerationStaleLeaseTransitionCheckpointHit,
					Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation, ResourceClass: spec.ResourceClass,
					ScheduleDigest: schedule.Digest, ChunkIdentity: selected.Identity, Offset: selected.Offset, Length: 1, Attempt: 0}
				if _, err := state.ReadGenerationStaleLeaseTransition(ctx, request); err != nil {
					t.Fatal("prepared point refused")
				}
				if persistent {
					if restart.hardDeath {
						if state.engine == nil || state.engine.process == nil {
							t.Fatal("test-owned exact engine handle unavailable")
						}
						if err := state.engine.process.Kill(); err != nil {
							t.Fatal("test-owned engine hard death refused")
						}
					}
					if err := state.Close(ctx); err != nil && !restart.hardDeath {
						t.Fatal("test-owned prior engine closure refused")
					}
					state, err = open(ctx, dir)
					if err != nil {
						t.Fatal("test-owned restart refused")
					}
				}
				// Seed only a test-owned old heartbeat. The actual twenty-second
				// reaper, original attempt identity, re-claim and completion execute.
				past := time.Now().UTC().Add(-time.Hour)
				if _, err := surrealdb.Query[any](ctx, state.db,
					"UPDATE $rid SET claimed_at = $past, heartbeat_at = $past RETURN NONE",
					map[string]any{"rid": generationChunkRecordID(*selected), "past": past}); err != nil {
					t.Fatal("old-heartbeat fixture refused")
				}
				reaped, err := state.ReapStaleGenerationChunksObserved(ctx, spec.ResourceClass, 20*time.Second,
					func(context.Context, GenerationStaleLeaseTransition) error { return nil })
				if err != nil || reaped != 1 {
					t.Fatal("actual reaper refused")
				}
				recovered, err := state.ClaimGenerationChunk(ctx, spec.ResourceClass, "v20-recovered")
				if err != nil || recovered == nil || recovered.Identity != selected.Identity || recovered.ID != selected.ID ||
					recovered.Attempt != 0 || recovered.Priority != GenerationPriorityStale || recovered.LeaseToken == selected.LeaseToken {
					t.Fatal("original attempt reclaim refused")
				}
				if err := state.CompleteGenerationChunk(ctx, *recovered); err != nil {
					t.Fatal("actual recovery completion refused")
				}
				request.Point = GenerationStaleLeaseTransitionRecovered
				local, err := ReadLocalRuntime(dir)
				if err != nil {
					t.Fatal("test-owned connection descriptor refused")
				}
				selectedCtx, owner, _ := storeAccountingFixture(t, 40, 2)
				accountedStore, err := openLocalRootWithOwner(selectedCtx, local, owner)
				if err != nil {
					t.Fatal("selected SDK connection refused")
				}
				defer func() {
					if err := accountedStore.Close(context.Background()); err != nil {
						t.Error("selected SDK closure refused")
					}
				}()
				read := func(state *Surreal, target GenerationStaleLeaseTransitionRequest, wantMissing bool) {
					t.Helper()
					accounted, ledger, err := readaccounting.Start(selectedCtx, readaccounting.Counts{StoreReadAttempts: 4})
					if err != nil {
						t.Fatal("read ledger refused")
					}
					before, first := state.ReadGenerationStaleLeaseTransition(accounted, target)
					after, second := state.ReadGenerationStaleLeaseTransition(accounted, target)
					counts, finish := ledger.Finish()
					if finish != nil || counts != (readaccounting.Counts{StoreReadAttempts: 4}) {
						t.Fatal("point read accounting differs", counts)
					}
					if wantMissing {
						if !errors.Is(first, ErrGenerationLeaseLost) || !errors.Is(second, ErrGenerationLeaseLost) ||
							!strings.Contains(first.Error(), "generation chunk has no record id") || before != (GenerationStaleLeaseTransition{}) || after != before {
							t.Fatal("missing original row gained credit")
						}
					} else if first != nil || second != nil || before != after || before.ChunkIdentity != selected.Identity ||
						before.ChunkStatus != GenerationChunkDone || before.Attempt != 0 || before.Leased {
						t.Fatalf("valid recovered read refused: first_missing_id=%t second_missing_id=%t first_lease=%t second_lease=%t", first != nil && strings.Contains(first.Error(), "no record id"), second != nil && strings.Contains(second.Error(), "no record id"), errors.Is(first, ErrGenerationLeaseLost), errors.Is(second, ErrGenerationLeaseLost))
					}
				}
				read(state, request, false)
				read(accountedStore, request, false)
				// The real selected connection executes overlapping query pairs,
				// within the existing two-transaction concurrency capacity.
				var readers sync.WaitGroup
				for range 2 {
					readers.Add(1)
					go func() {
						defer readers.Done()
						for range 3 {
							read(accountedStore, request, false)
						}
					}()
				}
				readers.Wait()
				// Distinct operands are essential: identical concurrent requests
				// would hide any cross-call variable/result contamination.
				absent := request
				absent.Offset = shape.total + 5
				absent.ChunkIdentity, _ = GenerationChunkIdentity(schedule.Digest, absent.Offset, 0)
				readers.Add(2)
				go func() {
					defer readers.Done()
					for range 3 {
						read(accountedStore, request, false)
					}
				}()
				go func() {
					defer readers.Done()
					for range 3 {
						read(accountedStore, absent, true)
					}
				}()
				readers.Wait()
				// A retry identity never supplies original-attempt recovery credit.
				wrong := request
				wrong.Attempt = 1
				wrong.ChunkIdentity, _ = GenerationChunkIdentity(schedule.Digest, request.Offset, 1)
				if value, err := accountedStore.ReadGenerationStaleLeaseTransition(selectedCtx, wrong); err == nil || value != (GenerationStaleLeaseTransition{}) {
					t.Fatal("retry attempt gained original-attempt credit")
				}
				// Counterfactual signature: retain the current schedule but delete
				// this exact test-owned original row. This is injected corruption,
				// not evidence of what deleted or hid V20's retained native row.
				if _, err := surrealdb.Query[any](ctx, state.db, "DELETE $rid RETURN NONE", map[string]any{"rid": models.NewRecordID("generation_schedule_chunk", selected.ID)}); err != nil {
					t.Fatal("missing-row control refused")
				}
				read(state, request, true)
				read(accountedStore, request, true)
				t.Log("ordinary and selected SDK original-attempt recovered reads passed; missing-row signature and retry refusal preserved")
			})
		}
	}
}
