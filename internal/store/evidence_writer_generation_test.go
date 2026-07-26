package store

import (
	"context"
	"errors"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestEvidenceWriterGenerationFailsClosed(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	const (
		repo   = "github.com/writer/generation"
		commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.com/generation.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginExtractionRun(ctx, repo, commit, "contracts", "current")
	if err != nil {
		t.Fatal(err)
	}
	oldReader, err := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
		`SELECT id FROM $rid WHERE store_schema_version = $old_schema`,
		map[string]any{
			"rid": extractionRunID(run.ID), "old_schema": evidencePreviousStoreSchemaVersion,
		})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *oldReader {
		if len(result.Result) != 0 {
			t.Fatal("previous-generation exact reader exposed a v7 staged run")
		}
	}

	// Model the previous v6 binary reapplying the exact weaker field assertion
	// it knows. The generation-named v7 event is unknown to that binary and
	// remains installed, so the old writer still cannot create a run.
	weakened, err := surrealdb.Query[any](ctx, s.db,
		`DEFINE FIELD OVERWRITE store_schema_version ON extraction_run TYPE string
			ASSERT $value NOT IN
				['t12-store-v1', 't12-store-v2', 't12-store-v3',
				 't12-store-v4', 't12-store-v5'];`,
		nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range *weakened {
		if result.Error != nil {
			t.Fatalf("weaken writer field guard statement %d: %s", i, result.Error.Message)
		}
	}
	results, rawErr := surrealdb.Query[any](ctx, s.db,
		`CREATE $rid SET run_id = $run_id, repo = $repo, commit = $commit,
			domain = 'old-writer', extractor = 'old', status = 'staged',
			started_at = time::now(), store_schema_version = $old_schema,
			evidence_format_version = $format,
			evidence_migration_version = $old_migration,
			retention_quarantined = false, staged_revision = 0, retention_revision = 0`,
		map[string]any{
			"rid": extractionRunID("old-writer-run"), "run_id": "old-writer-run",
			"repo": repo, "commit": commit, "old_schema": evidencePreviousStoreSchemaVersion,
			"format": evidenceFormatVersion, "old_migration": evidencePreviousMigrationVersion,
		})
	if rawErr == nil {
		for _, result := range *results {
			if result.Error != nil {
				rawErr = errors.New(result.Error.Message)
				break
			}
		}
	}
	if rawErr == nil {
		t.Fatal("previous writer generation created a run under the retained v7 event")
	}
	if exists, err := s.extractionRunExists(ctx, "old-writer-run"); err != nil || exists {
		t.Fatalf("old writer row exists = %v, %v", exists, err)
	}

	// A concurrent/rollback opener can overwrite the old completion marker.
	// Every current mutation independently checks it, so neither generation can
	// continue writing until exclusive startup restores the v7 marker.
	markerResults, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE $rid SET version = $version RETURN NONE",
		map[string]any{
			"rid": evidenceMigrationStateID(), "version": evidencePreviousMigrationVersion,
		})
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range *markerResults {
		if result.Error != nil {
			t.Fatalf("replace migration marker statement %d: %s", i, result.Error.Message)
		}
	}
	if _, err := s.BeginExtractionRun(ctx, repo, commit, "blocked", "current"); !errors.Is(err, ErrConflict) {
		t.Fatalf("begin under stale writer marker = %v", err)
	}
	if err := s.AddEvidence(ctx, run.ID, nil, nil, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("stage under stale writer marker = %v", err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, t203Coverage(0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("publish under stale writer marker = %v", err)
	}
	if err := s.AbortExtractionRun(ctx, run.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("abort under stale writer marker = %v", err)
	}
	staged, err := s.getRun(ctx, run.ID)
	if err != nil || staged.Status != "staged" {
		t.Fatalf("guarded run changed = %+v, %v", staged, err)
	}

	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("exclusive current reopen did not restore writer marker: %v", err)
	}
	if err := s.AbortExtractionRun(ctx, run.ID); err != nil {
		t.Fatalf("current writer did not recover after migration: %v", err)
	}
}
