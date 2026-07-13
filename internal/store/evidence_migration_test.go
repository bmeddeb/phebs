package store

import (
	"context"
	"errors"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

// The retracted T12 store could leave rows with an explicit run_id but no
// version marker, deterministic occurrence key, or typed atom link. Reopen
// must retain those rows for audit and explicit administrator cleanup while
// retiring them from every current query path, including a pre-existing pin.
// A current-version publication must then survive another idempotent schema
// application unchanged.
func TestMigrateEvidenceRunsRetiresLegacyAndPreservesCurrent(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	repo := "github.com/migration/repo"
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.com/repo.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, "current", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $published CONTENT {
            run_id: 'legacy-published', repo: $repo, commit: 'legacy', domain: 'proto-contract',
            extractor: 'legacy', status: 'published', started_at: $now, published_at: $now
        };
        CREATE $staged CONTENT {
            run_id: 'legacy-staged', repo: $repo, commit: 'legacy', domain: 'identity',
            extractor: 'legacy', status: 'staged', started_at: $now
        };
        CREATE assertion CONTENT {
            assertion_id: 'legacy-assertion', predicate: 'P', subject: 'p', object: 'legacy',
            tier: 'exact', repo: $repo, run_id: 'legacy-published', supporting: [], contradicting: []
        };
        CREATE $atom CONTENT {
            atom_id: 'legacy-atom', schema_version: 'legacy', blob_digest: 'legacy',
            start_byte: 0, end_byte: 1, rule_id: 'legacy', extractor_version: 'legacy',
            adapter_config_digest: 'legacy', fact_fingerprint: 'legacy', first_seen: $now
        };
        CREATE $occurrence CONTENT {
            occurrence_id: 'legacy-occurrence', atom_id: 'legacy-atom', atom_record: $atom,
            repo: $repo, commit: 'legacy', path: 'legacy.proto', start_line: 1, end_line: 1,
            visibility_scope: $visibility, run_id: 'legacy-published', observed_at: $now
        };
        CREATE $pin CONTENT {
            pin_key: 'legacy-pin', run_id: 'legacy-published', kind: 'legacy-bundle', created_at: $now
        };`, map[string]any{
			"published": extractionRunID("legacy-published"),
			"staged":    extractionRunID("legacy-staged"),
			"atom":      evidenceAtomRecordID("legacy-atom"),
			"occurrence": snapshotEvidenceRecordID(
				"legacy-published", "legacy-occurrence",
			),
			"pin":        evidencePinRecordID("legacy-published", "legacy-bundle"),
			"repo":       repo,
			"visibility": "repo:" + repo,
			"now":        now,
		}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ListAssertions(ctx, AssertionQuery{Repo: repo})
	if err != nil || len(before) != 1 {
		t.Fatalf("legacy setup was not query-visible: %+v, %v", before, err)
	}

	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("migrate legacy evidence: %v", err)
	}
	published, err := s.getRun(ctx, "legacy-published")
	if err != nil || published.Status != "superseded" {
		t.Fatalf("legacy published run = %+v, %v", published, err)
	}
	staged, err := s.getRun(ctx, "legacy-staged")
	if err != nil || staged.Status != "aborted" {
		t.Fatalf("legacy staged run = %+v, %v", staged, err)
	}
	after, err := s.ListAssertions(ctx, AssertionQuery{Repo: repo})
	if err != nil || len(after) != 0 {
		t.Fatalf("legacy assertion remained visible: %+v, %v", after, err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC().Add(48*time.Hour), time.Hour); err != nil || n != 0 {
		t.Fatalf("unbounded legacy run entered automatic sweep: %d, %v", n, err)
	}
	if _, err := s.ResolveEvidence(ctx, repo, "legacy-published", "legacy-atom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pinned legacy evidence resolved after quarantine: %v", err)
	}
	if err := s.PinRun(ctx, "legacy-published", "new-bundle"); !errors.Is(err, ErrConflict) {
		t.Fatalf("quarantined legacy run accepted a new pin: %v", err)
	}

	current, err := s.BeginExtractionRun(ctx, repo, "current", "proto-contract", "2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, current.ID, CoverageManifest{
		SourceScopeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("steady-state schema reapply: %v", err)
	}
	latest, err := s.LatestPublishedRun(ctx, repo, "proto-contract")
	if err != nil || latest.ID != current.ID {
		t.Fatalf("current publication changed on reopen: %+v, %v", latest, err)
	}
}
