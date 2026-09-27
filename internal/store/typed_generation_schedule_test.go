package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestTypedGenerationClosedSchedule(t *testing.T) {
	spec := generationSpec("example.invalid/typed", "sha256:"+strings.Repeat("a", 64))
	spec.Stage = TypedIndexScheduleStage
	spec.ResourceClass = GenerationResourceTypedIndex
	spec.TotalItems, spec.ChunkItems, spec.RepositoryTokens = 1, 1, 1
	if _, err := GenerationScheduleDigest(spec); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*GenerationScheduleSpec)
	}{
		{"items", func(s *GenerationScheduleSpec) { s.TotalItems = 2 }},
		{"chunk", func(s *GenerationScheduleSpec) { s.ChunkItems = 2 }},
		{"tokens", func(s *GenerationScheduleSpec) { s.RepositoryTokens = 2 }},
		{"stage", func(s *GenerationScheduleSpec) { s.Stage = "observation" }},
		{"class", func(s *GenerationScheduleSpec) { s.ResourceClass = GenerationResourceCPU }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := spec
			tc.change(&bad)
			if _, err := GenerationScheduleDigest(bad); err == nil {
				t.Fatal("invalid typed shape admitted")
			}
		})
	}
}

func TestTypedGenerationMigrationAndIsolation(t *testing.T) {
	state, err := OpenLocalMemory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	query := func(sql string, vars map[string]any) {
		t.Helper()
		results, err := surrealdb.Query[any](t.Context(), state.db, sql, vars)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range *results {
			if r.Error != nil {
				t.Fatal(r.Error.Message)
			}
		}
	}
	query(`UPSERT $marker SET version=$version;
DEFINE FIELD OVERWRITE resource_class ON generation_schedule TYPE string ASSERT $value INSIDE ['cpu','io','memory','extraction'];
DEFINE FIELD OVERWRITE resource_class ON generation_schedule_chunk TYPE string ASSERT $value INSIDE ['cpu','io','memory','extraction'];`, map[string]any{"marker": generationResourceClassMigrationID(), "version": previousGenerationResourceClassMigrationVersion})
	if err := state.migrateGenerationResourceClasses(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ok, err := state.generationResourceClassMigrationComplete(t.Context()); err != nil || !ok {
		t.Fatalf("marker incomplete: %v", err)
	}
	// A second startup must take the completed marker path without schema work.
	if err := state.migrateGenerationResourceClasses(t.Context()); err != nil {
		t.Fatal(err)
	}
	typed := generationSpec("example.invalid/typed", "sha256:"+strings.Repeat("a", 64))
	typed.Stage, typed.ResourceClass = TypedIndexScheduleStage, GenerationResourceTypedIndex
	typed.TotalItems, typed.ChunkItems, typed.RepositoryTokens = 1, 1, 1
	generic := generationSpec("example.invalid/generic", "sha256:"+strings.Repeat("b", 64))
	generic.TotalItems, generic.ChunkItems = 1, 1
	for _, spec := range []GenerationScheduleSpec{typed, generic} {
		if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if _, err := state.ExpandGenerationSchedule(t.Context(), spec.Repository, spec.Stage, spec.Generation); err != nil {
			t.Fatal(err)
		}
	}
	tc, err := state.ClaimGenerationChunk(t.Context(), GenerationResourceTypedIndex, "typed-worker")
	if err != nil || tc.Repository != typed.Repository {
		t.Fatalf("typed claim %+v %v", tc, err)
	}
	if _, err := state.ClaimGenerationChunk(t.Context(), GenerationResourceTypedIndex, "typed-worker-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("typed stole generic work: %v", err)
	}
	gc, err := state.ClaimGenerationChunk(t.Context(), GenerationResourceCPU, "generic-worker")
	if err != nil || gc.Repository != generic.Repository {
		t.Fatalf("generic claim %+v %v", gc, err)
	}
	query(`UPDATE $marker SET version='future-schema';`, map[string]any{"marker": generationResourceClassMigrationID()})
	if err := state.migrateGenerationResourceClasses(t.Context()); err == nil {
		t.Fatal("future marker overwritten")
	}
}
