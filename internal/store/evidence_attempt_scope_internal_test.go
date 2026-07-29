package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

type t305AttemptFenceState struct {
	RunID     string `json:"run_id"`
	Repo      string `json:"repo"`
	Commit    string `json:"commit"`
	Unit      string `json:"unit_digest"`
	Domain    string `json:"domain"`
	Status    string `json:"status"`
	Schema    string `json:"store_schema_version"`
	Format    string `json:"evidence_format_version"`
	Migration string `json:"evidence_migration_version"`
}

func t305AttemptFenceRead(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	scope ExtractionScope,
) t305AttemptFenceState {
	t.Helper()
	results, err := surrealdb.Query[[]t305AttemptFenceState](
		ctx,
		s.db,
		`SELECT run_id, repo, commit, unit_digest, domain, status, store_schema_version,
			evidence_format_version, evidence_migration_version
			FROM $rid`,
		map[string]any{"rid": extractionAttemptID(scope)},
	)
	if err != nil {
		t.Fatalf("read attempt fence: %v", err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatal("read attempt fence: row not found")
	return t305AttemptFenceState{}
}

func t305AttemptFenceMisScope(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	scope ExtractionScope,
) {
	t.Helper()
	if _, err := surrealdb.Query[any](
		ctx,
		s.db,
		`UPDATE $rid SET repo = 'github.com/t305/foreign-attempt-owner'
			RETURN NONE`,
		map[string]any{"rid": extractionAttemptID(scope)},
	); err != nil {
		t.Fatalf("install mis-scoped attempt owner: %v", err)
	}
}

func t305AttemptFenceCorrupt(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	scope ExtractionScope,
) {
	t.Helper()
	if _, err := surrealdb.Query[any](
		ctx,
		s.db,
		`UPDATE $rid SET
			evidence_migration_version = 't12-evidence-migration-v999'
			RETURN NONE`,
		map[string]any{"rid": extractionAttemptID(scope)},
	); err != nil {
		t.Fatalf("install foreign attempt owner: %v", err)
	}
}

func TestT305AttemptRowsRemainWriterGenerationIsolated(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	const (
		repository = "github.com/t305/attempt-fence"
		commit     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/attempt-fence.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	collisionScope := ExtractionScope{
		Repository: repository,
		Commit:     commit,
		Domain:     "collision",
	}
	if _, err := surrealdb.Query[any](
		ctx,
		s.db,
		`CREATE $rid CONTENT {
			run_id: 'future-attempt',
			repo: $repo,
			commit: $commit,
			unit_digest: '',
			domain: $domain,
			extractor: 'future',
			status: 'staged',
			started_at: $now,
			store_schema_version: 't12-store-v999',
			evidence_format_version: $format,
			evidence_migration_version: 't12-evidence-migration-v999'
		}`,
		map[string]any{
			"rid":    extractionAttemptID(collisionScope),
			"repo":   repository,
			"commit": commit,
			"domain": collisionScope.Domain,
			"now":    time.Now().UTC(),
			"format": evidenceFormatVersion,
		},
	); err != nil {
		t.Fatalf("create future attempt: %v", err)
	}
	if _, err := s.BeginExtractionRun(
		ctx, collisionScope, "current",
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("begin over foreign attempt = %v, want conflict", err)
	}
	collision := t305AttemptFenceRead(t, ctx, s, collisionScope)
	if collision.RunID != "future-attempt" ||
		collision.Status != "staged" ||
		collision.Schema != "t12-store-v999" ||
		collision.Format != evidenceFormatVersion ||
		collision.Migration != "t12-evidence-migration-v999" {
		t.Fatalf("foreign attempt was overwritten: %+v", collision)
	}
	runRows, err := surrealdb.Query[[]extractionRunIdentityRec](
		ctx,
		s.db,
		`SELECT id FROM extraction_run
			WHERE repo = $repo AND commit = $commit
				AND unit_digest = '' AND domain = $domain
				AND store_schema_version = $schema`,
		map[string]any{
			"repo": repository, "commit": commit,
			"domain": collisionScope.Domain,
			"schema": evidenceStoreSchemaVersion,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *runRows {
		if len(result.Result) != 0 {
			t.Fatalf("foreign attempt collision left an orphan run: %+v", result.Result)
		}
	}

	mutationScope := ExtractionScope{
		Repository: repository,
		Commit:     commit,
		Domain:     "mutation",
	}
	run, err := s.BeginExtractionRun(ctx, mutationScope, "current")
	if err != nil {
		t.Fatal(err)
	}
	t305AttemptFenceCorrupt(t, ctx, s, mutationScope)
	if err := s.PublishExtractionRun(ctx, run.ID, CoverageManifest{
		SourceScopeDigest: t305EmptyDigest,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("publish with foreign attempt = %v, want conflict", err)
	}
	if err := s.AbortExtractionRun(ctx, run.ID); err != nil {
		t.Fatalf("abort current run with foreign attempt: %v", err)
	}
	mutation := t305AttemptFenceRead(t, ctx, s, mutationScope)
	if mutation.RunID != run.ID ||
		mutation.Status != "staged" ||
		mutation.Schema != evidenceStoreSchemaVersion ||
		mutation.Migration != "t12-evidence-migration-v999" {
		t.Fatalf("publish/abort mutated foreign attempt: %+v", mutation)
	}
	if _, err := surrealdb.Query[any](
		ctx,
		s.db,
		`CREATE $rid CONTENT {
			pin_key: $pin_key, run_id: $run_id, kind: 'test', created_at: $now
		}`,
		map[string]any{
			"rid":     evidencePinRecordID(run.ID, "test"),
			"pin_key": "test-" + run.ID,
			"run_id":  run.ID,
			"now":     time.Now().UTC(),
		},
	); err != nil {
		t.Fatalf("exclude aborted run from retention: %v", err)
	}

	exactScope := ExtractionScope{
		Repository: repository,
		Commit:     commit,
		Domain:     "exact-scope",
	}
	exactRun, err := s.BeginExtractionRun(ctx, exactScope, "current")
	if err != nil {
		t.Fatal(err)
	}
	t305AttemptFenceMisScope(t, ctx, s, exactScope)
	if err := s.PublishExtractionRun(ctx, exactRun.ID, CoverageManifest{
		SourceScopeDigest: t305EmptyDigest,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("publish with mis-scoped attempt = %v, want conflict", err)
	}
	if err := s.AbortExtractionRun(ctx, exactRun.ID); err != nil {
		t.Fatalf("abort current run with mis-scoped attempt: %v", err)
	}
	exact := t305AttemptFenceRead(t, ctx, s, exactScope)
	if exact.RunID != exactRun.ID ||
		exact.Status != "staged" ||
		exact.Repo != "github.com/t305/foreign-attempt-owner" ||
		exact.Schema != evidenceStoreSchemaVersion ||
		exact.Migration != evidenceMigrationVersion {
		t.Fatalf("publish/abort mutated mis-scoped attempt: %+v", exact)
	}
	if _, err := surrealdb.Query[any](
		ctx,
		s.db,
		`CREATE $rid CONTENT {
			pin_key: $pin_key, run_id: $run_id, kind: 'test', created_at: $now
		}`,
		map[string]any{
			"rid":     evidencePinRecordID(exactRun.ID, "test"),
			"pin_key": "test-" + exactRun.ID,
			"run_id":  exactRun.ID,
			"now":     time.Now().UTC(),
		},
	); err != nil {
		t.Fatalf("exclude exact-scope run from retention: %v", err)
	}

	retentionScope := ExtractionScope{
		Repository: repository,
		Commit:     commit,
		Domain:     "retention",
	}
	retained, err := s.BeginExtractionRun(ctx, retentionScope, "current")
	if err != nil {
		t.Fatal(err)
	}
	t305AttemptFenceMisScope(t, ctx, s, retentionScope)
	progress, err := s.SweepEvidence(ctx, time.Now().UTC().Add(time.Hour), 0)
	if err != nil {
		t.Fatalf("sweep staged current run: %v", err)
	}
	if progress.RunsMarkedDeleting != 1 {
		t.Fatalf("sweep progress = %+v", progress)
	}
	retention := t305AttemptFenceRead(t, ctx, s, retentionScope)
	if retention.RunID != retained.ID ||
		retention.Status != "staged" ||
		retention.Repo != "github.com/t305/foreign-attempt-owner" ||
		retention.Schema != evidenceStoreSchemaVersion ||
		retention.Migration != evidenceMigrationVersion {
		t.Fatalf("retention mutated foreign attempt: %+v", retention)
	}
}

func TestT305EvidenceTransitionsLeaveForeignMigrationAttemptsUntouched(
	t *testing.T,
) {
	s := newRunnerStore(t)
	ctx := context.Background()
	digest := func(fill string) string {
		return "sha256:" + strings.Repeat(fill, 64)
	}

	t.Run("candidate publication", func(t *testing.T) {
		const (
			repository = "github.com/t305/candidate-attempt-owner"
			commit     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			domain     = "candidate-attempt"
		)
		if err := s.UpsertRepo(ctx, Repo{
			Name: repository, CloneURL: "https://example.com/candidate-attempt.git",
		}); err != nil {
			t.Fatal(err)
		}
		unit := t305Unit(t, repository, "service", "service/main.go")
		t305IndexedState(t, ctx, s, repository, commit, unit)
		first := t305CandidatePublication(
			repository, commit, unit.Digest,
			digest("1"), digest("2"), digest("3"),
		)
		if err := s.PublishCandidateManifest(ctx, first); err != nil {
			t.Fatal(err)
		}
		scope := ExtractionScope{
			Repository: repository, Commit: commit,
			UnitDigest: unit.Digest, Domain: domain,
		}
		run := t305PublishAssertionForManifest(
			t, ctx, s, scope, "candidate-owner", "candidate-owner",
			first.ManifestDigest,
		)
		t305AttemptFenceCorrupt(t, ctx, s, scope)
		replacement := t305CandidatePublication(
			repository, commit, unit.Digest,
			digest("4"), digest("5"), digest("6"),
		)
		if err := s.PublishCandidateManifest(ctx, replacement); err != nil {
			t.Fatal(err)
		}
		attempt := t305AttemptFenceRead(t, ctx, s, scope)
		if attempt.RunID != run.ID ||
			attempt.Status != "published" ||
			attempt.Schema != evidenceStoreSchemaVersion ||
			attempt.Migration != "t12-evidence-migration-v999" {
			t.Fatalf("candidate transition mutated foreign attempt: %+v", attempt)
		}
	})

	t.Run("typed designation", func(t *testing.T) {
		const (
			repository = "github.com/t305/typed-attempt-owner"
			commit     = "cccccccccccccccccccccccccccccccccccccccc"
			domain     = "typed-attempt"
		)
		if err := s.UpsertRepo(ctx, Repo{
			Name: repository, CloneURL: "https://example.com/typed-attempt.git",
		}); err != nil {
			t.Fatal(err)
		}
		unitScope := analysisunit.Scope{
			Repository: repository,
			Name:       "service",
			Primary:    []string{"service/api.proto"},
			Supporting: []string{"service/a.scip", "service/b.scip"},
			TypedIndex: &analysisunit.TypedIndex{
				Kind: analysisunit.TypedIndexKindSCIP,
				Path: "service/a.scip",
			},
		}
		firstUnit, err := unitScope.State()
		if err != nil {
			t.Fatal(err)
		}
		unitScope.TypedIndex.Path = "service/b.scip"
		secondUnit, err := unitScope.State()
		if err != nil {
			t.Fatal(err)
		}
		if firstUnit.Digest != secondUnit.Digest {
			t.Fatal("typed designation changed semantic unit digest")
		}
		t305IndexedState(t, ctx, s, repository, commit, firstUnit)
		publication := t305CandidatePublication(
			repository, commit, firstUnit.Digest,
			digest("7"), digest("8"), digest("9"),
		)
		if err := s.PublishCandidateManifest(ctx, publication); err != nil {
			t.Fatal(err)
		}
		scope := ExtractionScope{
			Repository: repository, Commit: commit,
			UnitDigest: firstUnit.Digest, Domain: domain,
		}
		run := t305PublishAssertionForManifest(
			t, ctx, s, scope, "typed-owner", "typed-owner",
			publication.ManifestDigest,
		)
		t305AttemptFenceCorrupt(t, ctx, s, scope)
		t305IndexedState(t, ctx, s, repository, commit, secondUnit)
		attempt := t305AttemptFenceRead(t, ctx, s, scope)
		if attempt.RunID != run.ID ||
			attempt.Status != "published" ||
			attempt.Schema != evidenceStoreSchemaVersion ||
			attempt.Migration != "t12-evidence-migration-v999" {
			t.Fatalf("typed transition mutated foreign attempt: %+v", attempt)
		}
	})
}
