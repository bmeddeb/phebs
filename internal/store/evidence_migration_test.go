package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

type evidenceMigrationTestState struct {
	RunID        string `json:"run_id"`
	UnitDigest   string `json:"unit_digest"`
	Status       string `json:"status"`
	StoreSchema  string `json:"store_schema_version"`
	Format       string `json:"evidence_format_version"`
	Migration    string `json:"evidence_migration_version"`
	Ambiguous    string `json:"evidence_migration_ambiguous_run_id"`
	Quarantined  bool   `json:"retention_quarantined"`
	Phase        string `json:"retention_phase"`
	PublishedKey any    `json:"published_key"`
}

type evidenceMigrationTestRunID struct {
	RunID string `json:"run_id"`
}

type evidenceMigrationTestMarker struct {
	Version     string    `json:"version"`
	CompletedAt time.Time `json:"completed_at"`
}

func clearEvidenceMigrationMarker(t *testing.T, s *Surreal) {
	t.Helper()
	if _, err := surrealdb.Query[any](context.Background(), s.db,
		"DELETE $rid RETURN NONE", map[string]any{"rid": evidenceMigrationStateID()}); err != nil {
		t.Fatalf("clear evidence migration marker: %v", err)
	}
}

// Migration fixtures model preceding, future, and malformed writers after the
// current store has already opened once. A real foreign writer would install
// its own generation guard before writing; tests remove the current guard
// explicitly, create the raw rows, then let applySchema restore it.
func relaxEvidenceWriterGuards(t *testing.T, s *Surreal) {
	t.Helper()
	results, err := surrealdb.Query[any](context.Background(), s.db,
		fmt.Sprintf(
			`REMOVE FIELD store_schema_version ON extraction_run;
			REMOVE EVENT IF EXISTS %s ON TABLE extraction_run;`,
			evidenceWriterGuardEvent,
		), nil)
	if err != nil {
		t.Fatalf("relax evidence writer guards: %v", err)
	}
	for i, result := range *results {
		if result.Error != nil {
			t.Fatalf("relax evidence writer guard statement %d: %s", i, result.Error.Message)
		}
	}
}

func evidenceMigrationState(t *testing.T, s *Surreal, runID string) evidenceMigrationTestState {
	t.Helper()
	results, err := surrealdb.Query[[]evidenceMigrationTestState](context.Background(), s.db,
		`SELECT run_id, unit_digest, status, store_schema_version, evidence_format_version,
			evidence_migration_version, evidence_migration_ambiguous_run_id,
			retention_quarantined, retention_phase, published_key
			FROM $rid`, map[string]any{"rid": extractionRunID(runID)})
	if err != nil {
		t.Fatalf("read migration state %s: %v", runID, err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatalf("read migration state %s: row not found", runID)
	return evidenceMigrationTestState{}
}

func evidenceMigrationMarker(t *testing.T, s *Surreal) evidenceMigrationTestMarker {
	t.Helper()
	results, err := surrealdb.Query[[]evidenceMigrationTestMarker](context.Background(), s.db,
		"SELECT version, completed_at FROM $rid", map[string]any{"rid": evidenceMigrationStateID()})
	if err != nil {
		t.Fatalf("read evidence migration marker: %v", err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatal("evidence migration completion marker not found")
	return evidenceMigrationTestMarker{}
}

func TestRecordExtractionDomainOutcomeRequiresMigrationMarker(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	repository := "github.com/migration/outcome-marker"
	commit := strings.Repeat("7", 40)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/outcome-marker.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scope := ExtractionScope{
		Repository: repository, Commit: commit, Domain: "proto-contract",
	}
	outcome := ExtractionDomainOutcome{
		Scope:       scope,
		Disposition: DomainOutcomeRetryableFailure,
		Generation: ExtractionGenerationIdentity{
			Extractor:        "v1",
			InventoryPolicy:  "gitlink-boundary-v2",
			DependencyDigest: "sha256:" + strings.Repeat("a", 64),
		},
		ReceiptSchema: ExtractionOutcomeReceiptSchema,
		Receipt:       `{"schema":"phebs-extraction-domain-outcome-v1"}`,
	}
	if err := s.RecordExtractionDomainOutcome(ctx, outcome); err != nil {
		t.Fatal(err)
	}
	clearEvidenceMigrationMarker(t, s)
	outcome.Generation.Extractor = "v2"
	if err := s.RecordExtractionDomainOutcome(
		ctx, outcome,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("markerless outcome write = %v, want ErrConflict", err)
	}
	current, err := s.LatestExtractionDomainOutcome(ctx, scope)
	if err != nil || current.Generation.Extractor != "v1" {
		t.Fatalf("markerless write changed outcome = %+v, %v", current, err)
	}
}

// The retracted T12 store could leave rows with an explicit run_id but no
// version marker, deterministic occurrence key, or typed atom link. Reopen
// must retain those rows for audit and explicit administrator cleanup while
// retiring them from every current query path, including a pre-existing pin.
// A current-version publication must then survive another idempotent schema
// application unchanged.
func TestMigrateEvidenceRunsRetiresLegacyAndPreservesCurrent(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	repo := "github.com/migration/repo"
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.com/repo.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, "current", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Simulate an older schemaless database that admitted an unmapped status
	// and fields the current typed run struct cannot decode.
	if _, err := surrealdb.Query[any](ctx, s.db,
		"REMOVE FIELD status ON extraction_run", nil); err != nil {
		t.Fatalf("remove current status field for legacy fixture: %v", err)
	}
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
		};
		CREATE $invalid CONTENT {
			run_id: 'wrong-legacy-id', repo: $repo, commit: 'legacy', domain: 'malformed',
			extractor: 'legacy', status: 'retired-by-old-writer',
			started_at: {not_a_datetime: true}, coverage: 'not-a-manifest'
		};
		CREATE $invalid_assertion CONTENT {
			assertion_id: 'legacy-invalid-assertion', predicate: 'P', subject: 'bad', object: 'legacy',
			tier: 'exact', repo: $repo, run_id: 'wrong-legacy-id', supporting: [], contradicting: []
		};
		CREATE $invalid_occurrence CONTENT {
			occurrence_id: 'legacy-invalid-occurrence', atom_id: 'legacy-atom', atom_record: $atom,
			repo: $repo, commit: 'legacy', path: 'invalid.proto', start_line: 1, end_line: 1,
			visibility_scope: $visibility, run_id: 'wrong-legacy-id', observed_at: $now
		};
		CREATE $invalid_pin CONTENT {
			pin_key: 'legacy-invalid-pin', run_id: 'wrong-legacy-id', kind: 'invalid-bundle', created_at: $now
        };`, map[string]any{
			"published": extractionRunID("legacy-published"),
			"staged":    extractionRunID("legacy-staged"),
			"atom":      evidenceAtomRecordID("legacy-atom"),
			"occurrence": snapshotEvidenceRecordID(
				"legacy-published", "legacy-occurrence",
			),
			"pin":     evidencePinRecordID("legacy-published", "legacy-bundle"),
			"invalid": extractionRunID("legacy-invalid"),
			"invalid_assertion": assertionRecordID(
				"wrong-legacy-id", "legacy-invalid-assertion",
			),
			"invalid_occurrence": snapshotEvidenceRecordID(
				"wrong-legacy-id", "legacy-invalid-occurrence",
			),
			"invalid_pin": evidencePinRecordID("wrong-legacy-id", "invalid-bundle"),
			"repo":        repo,
			"visibility":  "repo:" + repo,
			"now":         now,
		}); err != nil {
		t.Fatal(err)
	}
	clearEvidenceMigrationMarker(t, s)
	before, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: "legacy-published",
	})
	if err != nil || len(before) != 0 {
		t.Fatalf("unversioned legacy assertion was visible before migration: %+v, %v", before, err)
	}

	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("migrate legacy evidence: %v", err)
	}
	published := evidenceMigrationState(t, s, "legacy-published")
	if published.Status != "superseded" || !published.Quarantined ||
		published.StoreSchema != evidenceStoreSchemaVersion || published.Format != evidenceFormatVersion ||
		published.Migration != evidenceMigrationVersion || published.PublishedKey != nil {
		t.Fatalf("legacy published run = %+v", published)
	}
	staged := evidenceMigrationState(t, s, "legacy-staged")
	if staged.Status != "aborted" || !staged.Quarantined {
		t.Fatalf("legacy staged run = %+v", staged)
	}
	invalid := evidenceMigrationState(t, s, "legacy-invalid")
	if invalid.RunID != "legacy-invalid" || invalid.Status != "aborted" || !invalid.Quarantined {
		t.Fatalf("malformed legacy run = %+v", invalid)
	}
	after, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: "legacy-published",
	})
	if err != nil || len(after) != 0 {
		t.Fatalf("legacy assertion remained visible: %+v, %v", after, err)
	}
	if n, err := sweepEvidenceRun(ctx, s, time.Now().UTC().Add(48*time.Hour), time.Hour); err != nil || n != 0 {
		t.Fatalf("unbounded legacy run entered automatic sweep: %d, %v", n, err)
	}
	if _, err := s.ResolveEvidence(ctx, repo, "legacy-published", "legacy-atom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pinned legacy evidence resolved after quarantine: %v", err)
	}
	if err := s.PinRun(ctx, "legacy-published", "new-bundle"); !errors.Is(err, ErrConflict) {
		t.Fatalf("quarantined legacy run accepted a new pin: %v", err)
	}
	for table, record := range map[string]any{
		"assertion": assertionRecordID("wrong-legacy-id", "legacy-invalid-assertion"),
		"occurrence": snapshotEvidenceRecordID(
			"wrong-legacy-id", "legacy-invalid-occurrence",
		),
		"pin": evidencePinRecordID("wrong-legacy-id", "invalid-bundle"),
	} {
		rows, queryErr := surrealdb.Query[[]evidenceMigrationTestRunID](ctx, s.db,
			"SELECT run_id FROM $rid", map[string]any{"rid": record})
		if queryErr != nil {
			t.Fatalf("read rewritten %s: %v", table, queryErr)
		}
		found := false
		for _, result := range *rows {
			found = found || (len(result.Result) == 1 && result.Result[0].RunID == "wrong-legacy-id")
		}
		if !found {
			t.Fatalf("quarantined legacy %s lost its audit run id", table)
		}
	}

	current, err := beginExtractionRun(s, ctx, repo, "current", "proto-contract", "2")
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
	latest, err := latestPublishedRun(s, ctx, repo, "proto-contract")
	if err != nil || latest.ID != current.ID {
		t.Fatalf("current publication changed on reopen: %+v, %v", latest, err)
	}
}

// A deployment can skip writer generations entirely (a v4-era store reopened
// by the v7 binary), leaving published rows whose per-generation upgrade pass
// never ran. Such a row is invisible to the legacy and previous-writer
// branches, yet still holds its unique published_key, so every replacement
// publication for its (repo, domain) slot aborts on the
// extraction_run_published_key index. Reopen must retire it — even on a store
// whose completion marker says an older migration already finished — and a
// replacement publication for the freed slot must then succeed.
func TestMigrateEvidenceRunsRetiresSkippedGenerationPublications(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	repo := "github.com/migration/skipped"
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.com/skipped.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, "current", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $stranded CONTENT {
			run_id: 'v4-published', repo: $repo, commit: 'old', domain: 'proto-contract',
			extractor: 'v4', status: 'published', started_at: $now, published_at: $now,
			store_schema_version: 't12-store-v4', evidence_format_version: $format,
			retention_quarantined: false, published_key: $slot
		};`, map[string]any{
			"stranded": extractionRunID("v4-published"),
			"repo":     repo, "now": now,
			"format": evidenceFormatVersion,
			"slot":   legacyPublishedKey(repo, "proto-contract"),
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		"UPSERT $rid SET version = $version, completed_at = time::now() RETURN NONE",
		map[string]any{
			"rid": evidenceMigrationStateID(), "version": evidencePreviousMigrationVersion,
		}); err != nil {
		t.Fatal(err)
	}

	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("migrate skipped-generation evidence: %v", err)
	}
	stranded := evidenceMigrationState(t, s, "v4-published")
	if stranded.Status != "superseded" || !stranded.Quarantined ||
		stranded.StoreSchema != evidenceStoreSchemaVersion ||
		stranded.Migration != evidenceMigrationVersion || stranded.PublishedKey != nil {
		t.Fatalf("skipped-generation run = %+v", stranded)
	}

	replacement, err := beginExtractionRun(s, ctx, repo, "current", "proto-contract", "2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, replacement.ID, CoverageManifest{
		SourceScopeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}); err != nil {
		t.Fatalf("replacement publication still blocked: %v", err)
	}
	latest, err := latestPublishedRun(s, ctx, repo, "proto-contract")
	if err != nil || latest.ID != replacement.ID {
		t.Fatalf("replacement publication not visible: %+v, %v", latest, err)
	}
}

func TestMigrateEvidenceRunsUpgradesPreviousWriterAndCanonicalizesPins(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	repo := "github.com/migration/v3"
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.com/v3.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, "v3-commit", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	extraPins := make([]map[string]any, 0, evidenceMigrationBatchSize+1)
	for i := 0; i < evidenceMigrationBatchSize+1; i++ {
		kind := fmt.Sprintf("batch-proof-%03d", i)
		extraPins = append(extraPins, map[string]any{
			"rid":     evidencePinRecordID("v3-old-id", kind),
			"pin_key": hashIdentity("pin_", "v3-old-id", kind),
			"kind":    kind,
		})
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $published CONTENT {
			run_id: 'v3-old-id', repo: $repo, commit: 'v3-commit', domain: 'proto-contract',
			extractor: 'v3', status: 'published', started_at: $now, published_at: $now,
			unit_digest: '', store_schema_version: $v3
		};
		CREATE $staged CONTENT {
			run_id: 'v3-staged', repo: $repo, commit: 'v3-commit', domain: 'identity',
			extractor: 'v3', status: 'staged', started_at: $now,
			unit_digest: '', store_schema_version: $v3
		};
		CREATE $hidden_alias CONTENT {
			run_id: 'v3-hidden-logical', repo: $repo, commit: 'future', domain: 'proto-contract',
			extractor: 'future', status: 'published', started_at: $now, published_at: $now,
			store_schema_version: 't12-store-v999', evidence_format_version: $format,
			retention_quarantined: false, published_key: 'bogus-future-slot'
		};
		CREATE $blocked_candidate CONTENT {
			run_id: 'v3-blocked', repo: $repo, commit: 'v3-commit', domain: 'blocked',
			extractor: 'v3', status: 'published', started_at: $now, published_at: $now,
			unit_digest: '', store_schema_version: $v3
		};
		CREATE $canonical_blocker CONTENT {
			run_id: 'not-the-physical-id', repo: $repo, commit: 'future', domain: 'blocked',
			extractor: 'future', status: 'aborted', started_at: $now,
			store_schema_version: 't12-store-v999', evidence_format_version: 't12-evidence-v999',
			retention_quarantined: true, published_key: $blocked_key
		};
		CREATE $atom CONTENT {
			atom_id: 'v3-atom', schema_version: 'v3', blob_digest: 'sha256:v3',
			start_byte: 0, end_byte: 1, rule_id: 'v3', extractor_version: 'v3',
			adapter_config_digest: 'v3', fact_fingerprint: 'v3', first_seen: $now
		};
		CREATE $occurrence CONTENT {
			occurrence_id: 'v3-occurrence', atom_id: 'v3-atom', atom_record: $atom,
			repo: $repo, commit: 'v3-commit', path: 'v3.proto', start_line: 1, end_line: 1,
			visibility_scope: $visibility, run_id: 'v3-old-id', observed_at: $now
		};
		CREATE $pin CONTENT {
			pin_key: 'v3-old-pin', run_id: 'v3-old-id', kind: 'proof-bundle', created_at: $now
		};
		FOR $p IN $extra_pins {
			CREATE $p.rid SET pin_key = $p.pin_key, run_id = 'v3-old-id',
				kind = $p.kind, created_at = $now RETURN NONE;
		};`, map[string]any{
			"published": extractionRunID("v3-published"),
			"staged":    extractionRunID("v3-staged"),
			"hidden_alias": extractionRunID(
				"v3-hidden-alias",
			),
			"blocked_candidate": extractionRunID(
				"v3-blocked",
			),
			"canonical_blocker": extractionRunID(
				"future-canonical-blocker",
			),
			"atom": evidenceAtomRecordID("v3-atom"),
			"occurrence": snapshotEvidenceRecordID(
				"v3-old-id", "v3-occurrence",
			),
			"pin":  evidencePinRecordID("v3-old-id", "proof-bundle"),
			"repo": repo, "visibility": "repo:" + repo, "now": now,
			"v3": evidencePreviousStoreSchemaVersion, "format": evidenceFormatVersion,
			"blocked_key": publishedKey(ExtractionScope{
				Repository: repo,
				Commit:     "v3-commit",
				Domain:     "blocked",
			}),
			"extra_pins": extraPins,
		}); err != nil {
		t.Fatal(err)
	}
	clearEvidenceMigrationMarker(t, s)
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("upgrade previous-writer evidence: %v", err)
	}

	published := evidenceMigrationState(t, s, "v3-published")
	if published.RunID != "v3-published" || published.Status != "published" ||
		published.StoreSchema != evidenceStoreSchemaVersion || published.Format != evidenceFormatVersion ||
		published.Quarantined || published.Phase != "" ||
		published.PublishedKey != publishedKey(ExtractionScope{
			Repository: repo,
			Commit:     "v3-commit",
			Domain:     "proto-contract",
		}) {
		t.Fatalf("upgraded v3 publication = %+v", published)
	}
	blocked := evidenceMigrationState(t, s, "v3-blocked")
	if blocked.Status != "superseded" || blocked.Quarantined || blocked.PublishedKey != nil {
		t.Fatalf("canonical slot blocker did not retire v3 candidate: %+v", blocked)
	}
	resolved, err := s.ResolveEvidence(ctx, repo, "v3-published", "v3-atom")
	if err != nil || len(resolved.Occurrences) != 1 || resolved.Occurrences[0].RunID != "v3-published" {
		t.Fatalf("resolve upgraded v3 proof = %+v, %v", resolved, err)
	}
	if err := s.PinRun(ctx, "v3-published", "proof-bundle"); err != nil {
		t.Fatalf("idempotent canonical re-pin: %v", err)
	}
	pins, err := surrealdb.Query[[]evidencePinRec](ctx, s.db,
		"SELECT run_id, kind FROM evidence_pin WHERE run_id = $run AND kind = $kind",
		map[string]any{"run": "v3-published", "kind": "proof-bundle"})
	if err != nil {
		t.Fatal(err)
	}
	pinCount := 0
	for _, result := range *pins {
		pinCount += len(result.Result)
	}
	if pinCount != 1 {
		t.Fatalf("canonical proof pin count = %d, want 1", pinCount)
	}
	allPins, err := surrealdb.Query[[]evidencePinRec](ctx, s.db,
		"SELECT run_id, kind FROM evidence_pin WHERE run_id = $run",
		map[string]any{"run": "v3-published"})
	if err != nil {
		t.Fatal(err)
	}
	allPinCount := 0
	for _, result := range *allPins {
		allPinCount += len(result.Result)
	}
	if allPinCount != evidenceMigrationBatchSize+2 {
		t.Fatalf("batched canonical pin count = %d, want %d", allPinCount, evidenceMigrationBatchSize+2)
	}
	if err := s.AbortExtractionRun(ctx, "v3-staged"); err != nil {
		t.Fatalf("upgraded compatible staged run was not writable: %v", err)
	}

	before := evidenceMigrationMarker(t, s)
	if before.Version != evidenceMigrationVersion {
		t.Fatalf("completion marker = %+v", before)
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("steady-state apply: %v", err)
	}
	after := evidenceMigrationMarker(t, s)
	if !after.CompletedAt.Equal(before.CompletedAt) {
		t.Fatalf("steady-state Open rewrote completion marker: before=%v after=%v", before, after)
	}
}

func TestMigrateEvidenceRunIDCollisionDoesNotStealProof(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	repo := "github.com/migration/collision"
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $owner CONTENT {
				run_id: 'collision-owner', repo: $repo, commit: 'owner', domain: 'owner',
				extractor: 'v3', status: 'published', started_at: $now, published_at: $now,
				unit_digest: '', store_schema_version: $v3
			};
			CREATE $collider CONTENT {
				run_id: 'collision-owner', repo: $repo, commit: 'owner', domain: 'owner',
				extractor: 'v3', status: 'published', started_at: $now, published_at: $now,
				unit_digest: '', store_schema_version: $v3
		};
		CREATE $orphan_a CONTENT {
			run_id: 'orphan-x', repo: $repo, commit: 'orphan-a', domain: 'orphan',
			extractor: 'v3', status: 'published', started_at: $now, published_at: $now,
			unit_digest: '', store_schema_version: $v3
		};
		CREATE $orphan_b CONTENT {
			run_id: 'orphan-x', repo: $repo, commit: 'orphan-b', domain: 'orphan',
			extractor: 'v3', status: 'superseded', started_at: $now,
			unit_digest: '', store_schema_version: $v3
		};
		CREATE $atom CONTENT {
			atom_id: 'collision-atom', schema_version: 'v3', blob_digest: 'collision',
			start_byte: 0, end_byte: 1, rule_id: 'v3', extractor_version: 'v3',
			adapter_config_digest: 'v3', fact_fingerprint: 'owner', first_seen: $now
		};
		CREATE $occurrence CONTENT {
			occurrence_id: 'collision-occurrence', atom_id: 'collision-atom', atom_record: $atom,
				repo: $repo, commit: 'owner', path: 'owner.proto', start_line: 1, end_line: 1,
				visibility_scope: $visibility, run_id: 'collision-owner', observed_at: $now
			};
			CREATE $collider_occurrence CONTENT {
				occurrence_id: 'collider-occurrence', atom_id: 'collision-atom', atom_record: $atom,
				repo: $repo, commit: 'owner', path: 'collider.proto', start_line: 1, end_line: 1,
				visibility_scope: $visibility, run_id: 'collision-owner', observed_at: $now
			};
			CREATE $collider_assertion CONTENT {
				assertion_id: 'collider-assertion', predicate: 'collider.injected',
				subject: 'collider.proto', object: 'Injected', lineage: 'collision',
				tier: 'exact', code_role: '', repo: $repo,
				supporting: ['collision-atom'], contradicting: [],
				run_id: 'collision-owner', detail: 'ambiguous collider proof'
			};
		CREATE $orphan_occurrence CONTENT {
			occurrence_id: 'orphan-occurrence', atom_id: 'collision-atom', atom_record: $atom,
			repo: $repo, commit: 'orphan', path: 'orphan.proto', start_line: 1, end_line: 1,
			visibility_scope: $visibility, run_id: 'orphan-x', observed_at: $now
		};
		CREATE $orphan_pin CONTENT {
			pin_key: 'orphan-pin', run_id: 'orphan-x', kind: 'orphan-proof', created_at: $now
		};`, map[string]any{
			"owner": extractionRunID("collision-owner"),
			"collider": extractionRunID(
				"collision-collider",
			),
			"orphan_a": extractionRunID("orphan-a"),
			"orphan_b": extractionRunID("orphan-b"),
			"atom":     evidenceAtomRecordID("collision-atom"),
			"occurrence": snapshotEvidenceRecordID(
				"collision-owner", "collision-occurrence",
			),
			"collider_occurrence": snapshotEvidenceRecordID(
				"collision-collider", "collider-occurrence",
			),
			"collider_assertion": assertionRecordID(
				"collision-collider", "collider-assertion",
			),
			"orphan_occurrence": snapshotEvidenceRecordID(
				"orphan-x", "orphan-occurrence",
			),
			"orphan_pin": evidencePinRecordID("orphan-x", "orphan-proof"),
			"repo":       repo, "visibility": "repo:" + repo, "now": now,
			"v3": evidencePreviousStoreSchemaVersion,
		}); err != nil {
		t.Fatal(err)
	}
	clearEvidenceMigrationMarker(t, s)
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("migrate colliding run ids: %v", err)
	}

	collider := evidenceMigrationState(t, s, "collision-collider")
	if collider.RunID != "collision-collider" || collider.Status != "superseded" ||
		!collider.Quarantined || collider.Ambiguous != "collision-owner" {
		t.Fatalf("colliding run was not retired fail-closed: %+v", collider)
	}
	owner := evidenceMigrationState(t, s, "collision-owner")
	if owner.RunID != "collision-owner" || owner.Status != "published" || owner.Quarantined {
		t.Fatalf("legitimate owner changed: %+v", owner)
	}
	if _, err := s.getRun(ctx, "collision-owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous physical owner remained directly visible: %v", err)
	}
	if _, err := latestPublishedRun(s, ctx, repo, "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous physical owner remained latest: %v", err)
	}
	assertions, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: "collision-owner",
	})
	if err != nil || len(assertions) != 0 {
		t.Fatalf("ambiguous collider assertion leaked through owner: %+v, %v", assertions, err)
	}
	if _, err := s.ResolveEvidence(ctx, repo, "collision-owner", "collision-atom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous collider occurrence leaked through owner: %v", err)
	}
	if err := s.PinRun(ctx, "collision-owner", "owner-proof"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous physical owner was pinnable: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE $owner SET status = 'staged', published_key = NONE, started_at = $started_at RETURN NONE",
		map[string]any{
			"owner":      extractionRunID("collision-owner"),
			"started_at": now.Add(-2 * time.Hour),
		}); err != nil {
		t.Fatalf("make ambiguous owner retention-eligible: %v", err)
	}
	if err := s.AbortExtractionRun(ctx, "collision-owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous physical owner remained mutable: %v", err)
	}
	deleted, err := sweepEvidenceRun(ctx, s, now, time.Hour)
	if err != nil || deleted != 0 {
		t.Fatalf("ambiguous physical owner was swept: deleted=%d err=%v", deleted, err)
	}
	for kind, record := range map[string]any{
		"owner run":           extractionRunID("collision-owner"),
		"collider run":        extractionRunID("collision-collider"),
		"atom":                evidenceAtomRecordID("collision-atom"),
		"owner occurrence":    snapshotEvidenceRecordID("collision-owner", "collision-occurrence"),
		"collider occurrence": snapshotEvidenceRecordID("collision-collider", "collider-occurrence"),
		"collider assertion":  assertionRecordID("collision-collider", "collider-assertion"),
	} {
		rows, queryErr := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
			"SELECT id FROM $rid", map[string]any{"rid": record})
		if queryErr != nil {
			t.Fatalf("read preserved %s: %v", kind, queryErr)
		}
		preserved := false
		for _, result := range *rows {
			preserved = preserved || len(result.Result) == 1
		}
		if !preserved {
			t.Fatalf("ambiguous %s was deleted", kind)
		}
	}
	for _, runID := range []string{"orphan-a", "orphan-b"} {
		claimant := evidenceMigrationState(t, s, runID)
		if claimant.RunID != runID || !claimant.Quarantined || claimant.Ambiguous != "orphan-x" {
			t.Fatalf("orphan claimant %s was not retired: %+v", runID, claimant)
		}
	}
	for kind, record := range map[string]any{
		"occurrence": snapshotEvidenceRecordID("orphan-x", "orphan-occurrence"),
		"pin":        evidencePinRecordID("orphan-x", "orphan-proof"),
	} {
		rows, queryErr := surrealdb.Query[[]evidenceMigrationTestRunID](ctx, s.db,
			"SELECT run_id FROM $rid", map[string]any{"rid": record})
		if queryErr != nil {
			t.Fatalf("read orphan %s: %v", kind, queryErr)
		}
		preserved := false
		for _, result := range *rows {
			preserved = preserved || (len(result.Result) == 1 && result.Result[0].RunID == "orphan-x")
		}
		if !preserved {
			t.Fatalf("ambiguous orphan %s was reassigned", kind)
		}
	}
	// Model a retired v5 writer adding a late claimant after the first v6
	// migration pass. The current guard must be removed explicitly for the
	// fixture write, then applySchema must restore it before migrating.
	relaxEvidenceWriterGuards(t, s)
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE $orphan_a SET evidence_migration_version = 't12-evidence-migration-v1';
		UPDATE $orphan_b SET evidence_migration_version = 't12-evidence-migration-v1';
		CREATE $late CONTENT {
			run_id: 'orphan-x', repo: $repo, commit: 'late', domain: 'orphan',
			extractor: 'v3', status: 'published', started_at: $now, published_at: $now,
			unit_digest: '', store_schema_version: $v3
		};
		UPSERT $marker SET version = 't12-evidence-migration-v1', completed_at = $now;`,
		map[string]any{
			"orphan_a": extractionRunID("orphan-a"),
			"orphan_b": extractionRunID("orphan-b"),
			"late":     extractionRunID("orphan-c"),
			"marker":   evidenceMigrationStateID(),
			"repo":     repo, "now": now, "v3": evidencePreviousStoreSchemaVersion,
		}); err != nil {
		t.Fatal(err)
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("upgrade v1 ambiguity markers: %v", err)
	}
	for _, runID := range []string{"orphan-a", "orphan-b", "orphan-c"} {
		claimant := evidenceMigrationState(t, s, runID)
		if !claimant.Quarantined || claimant.Ambiguous != "orphan-x" {
			t.Fatalf("v2 lost ambiguity marker for %s: %+v", runID, claimant)
		}
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("collision migration did not reach steady state: %v", err)
	}
}

func TestMigrateEvidenceUnsafeRetainedClaimsReserveOwner(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	repo := "github.com/migration/retained-claim"
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		"REMOVE FIELD status ON extraction_run", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $owner CONTENT {
			run_id: 'unsafe-owner', repo: $repo, commit: 'owner', domain: 'contracts',
			extractor: 'current', status: 'published', started_at: $now, published_at: $now,
			store_schema_version: $schema, evidence_format_version: $format,
			evidence_migration_version: $migration, retention_quarantined: false,
			published_key: $published_key
		};
		CREATE $legacy CONTENT {
			run_id: 'unsafe-owner', repo: $repo, commit: 'owner', domain: 'legacy',
			extractor: 'legacy', status: 'superseded', started_at: $old
		};
		CREATE $prequarantined CONTENT {
			run_id: 'unsafe-owner', repo: $repo, commit: 'owner', domain: 'prequarantined',
			extractor: 'v3', status: 'superseded', started_at: $old,
			unit_digest: '', store_schema_version: $v3, evidence_format_version: $format,
			retention_quarantined: true
		};
		CREATE $invalid_status CONTENT {
			run_id: 'unsafe-owner', repo: $repo, commit: 'owner', domain: 'invalid',
			extractor: 'v3', status: 'invalid-old-status', started_at: $old,
			unit_digest: '', store_schema_version: $v3, evidence_format_version: $format,
			retention_quarantined: false
		};
		CREATE $staged CONTENT {
			run_id: 'unsafe-owner', repo: $repo, commit: 'owner', domain: 'staged',
			extractor: 'v3', status: 'staged', started_at: $old,
			unit_digest: '', store_schema_version: $v3, evidence_format_version: $format,
			retention_quarantined: false
		};
		CREATE $atom CONTENT {
			atom_id: 'unsafe-atom', schema_version: 'legacy', blob_digest: 'unsafe',
			start_byte: 0, end_byte: 1, rule_id: 'legacy', extractor_version: 'legacy',
			adapter_config_digest: 'legacy', fact_fingerprint: 'unsafe', first_seen: $now
		};
		CREATE $occurrence CONTENT {
			occurrence_id: 'unsafe-occurrence', atom_id: 'unsafe-atom', atom_record: $atom,
			repo: $repo, commit: 'owner', path: 'claimant.proto', start_line: 1, end_line: 1,
			visibility_scope: $visibility, run_id: 'unsafe-owner', observed_at: $now
		};
		CREATE $assertion CONTENT {
			assertion_id: 'unsafe-assertion', predicate: 'Unsafe', subject: 'claimant.proto', object: 'Injected',
			tier: 'exact', repo: $repo, run_id: 'unsafe-owner',
			supporting: ['unsafe-atom'], contradicting: []
		};`, map[string]any{
			"owner":          extractionRunID("unsafe-owner"),
			"legacy":         extractionRunID("unsafe-legacy"),
			"prequarantined": extractionRunID("unsafe-prequarantined"),
			"invalid_status": extractionRunID("unsafe-invalid-status"),
			"staged":         extractionRunID("unsafe-staged"),
			"atom":           evidenceAtomRecordID("unsafe-atom"),
			"occurrence":     snapshotEvidenceRecordID("unsafe-legacy", "unsafe-occurrence"),
			"assertion":      assertionRecordID("unsafe-legacy", "unsafe-assertion"),
			"repo":           repo, "visibility": "repo:" + repo,
			"now": now, "old": now.Add(-72 * time.Hour),
			"schema": evidenceStoreSchemaVersion, "v3": evidencePreviousStoreSchemaVersion,
			"format": evidenceFormatVersion, "migration": evidenceMigrationVersion,
			"published_key": publishedKey(ExtractionScope{
				Repository: repo,
				Commit:     "owner",
				Domain:     "contracts",
			}),
		}); err != nil {
		t.Fatal(err)
	}
	clearEvidenceMigrationMarker(t, s)
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("migrate retained logical-id claimants: %v", err)
	}
	for _, runID := range []string{
		"unsafe-legacy", "unsafe-prequarantined", "unsafe-invalid-status", "unsafe-staged",
	} {
		state := evidenceMigrationState(t, s, runID)
		if state.RunID != runID || !state.Quarantined || state.Ambiguous != "unsafe-owner" {
			t.Fatalf("unsafe retained claimant %s lost its reservation: %+v", runID, state)
		}
	}
	if _, err := latestPublishedRun(s, ctx, repo, "contracts"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner reserved by retained claim remained latest: %v", err)
	}
	if assertions, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: "unsafe-owner",
	}); err != nil || len(assertions) != 0 {
		t.Fatalf("retained claimant assertion leaked through owner: %+v, %v", assertions, err)
	}
	if _, err := s.ResolveEvidence(ctx, repo, "unsafe-owner", "unsafe-atom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retained claimant occurrence leaked through owner: %v", err)
	}
	if err := s.PinRun(ctx, "unsafe-owner", "proof"); !errors.Is(err, ErrConflict) {
		t.Fatalf("owner reserved by retained claim accepted pin: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE $owner SET status = 'staged', published_key = NONE, started_at = $old RETURN NONE",
		map[string]any{
			"owner": extractionRunID("unsafe-owner"), "old": now.Add(-72 * time.Hour),
		}); err != nil {
		t.Fatal(err)
	}
	if err := s.AbortExtractionRun(ctx, "unsafe-owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner reserved by retained claim remained mutable: %v", err)
	}
	if n, err := sweepEvidenceRun(ctx, s, now, time.Hour); err != nil || n != 0 {
		t.Fatalf("owner reserved by retained claim was swept: %d, %v", n, err)
	}
	for kind, record := range map[string]any{
		"owner run":          extractionRunID("unsafe-owner"),
		"legacy claimant":    extractionRunID("unsafe-legacy"),
		"prequarantined run": extractionRunID("unsafe-prequarantined"),
		"invalid-status run": extractionRunID("unsafe-invalid-status"),
		"staged run":         extractionRunID("unsafe-staged"),
		"atom":               evidenceAtomRecordID("unsafe-atom"),
		"occurrence":         snapshotEvidenceRecordID("unsafe-legacy", "unsafe-occurrence"),
		"assertion":          assertionRecordID("unsafe-legacy", "unsafe-assertion"),
	} {
		rows, queryErr := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
			"SELECT id FROM $rid", map[string]any{"rid": record})
		if queryErr != nil {
			t.Fatalf("read preserved %s: %v", kind, queryErr)
		}
		preserved := false
		for _, result := range *rows {
			preserved = preserved || len(result.Result) == 1
		}
		if !preserved {
			t.Fatalf("retained ambiguity %s was deleted", kind)
		}
	}
}

func TestMigrateEvidenceRejectsConflictingAmbiguityMarkers(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $rid CONTENT {
			run_id: 'first-owner', repo: 'repo', commit: 'c', domain: 'd', extractor: 'v3',
			status: 'superseded', started_at: $now, unit_digest: '', store_schema_version: $v3,
			evidence_format_version: $format, evidence_migration_version: 't12-evidence-migration-v1',
			evidence_migration_ambiguous_run_id: 'second-owner', retention_quarantined: true
		};`, map[string]any{
			"rid": extractionRunID("conflicting-claimant"), "now": now,
			"v3": evidencePreviousStoreSchemaVersion, "format": evidenceFormatVersion,
		}); err != nil {
		t.Fatal(err)
	}
	clearEvidenceMigrationMarker(t, s)
	err := s.applySchema(ctx)
	if err == nil || !strings.Contains(err.Error(), "conflicting ambiguity markers") {
		t.Fatalf("conflicting ambiguity markers did not fail explicitly: %v", err)
	}
	state := evidenceMigrationState(t, s, "conflicting-claimant")
	if state.RunID != "first-owner" || state.Ambiguous != "second-owner" || !state.Quarantined {
		t.Fatalf("conflicting ambiguity failure partially normalized row: %+v", state)
	}
}

func TestEvidenceFutureWriterCompatibilityIsForwardSafe(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	repo := "github.com/migration/future"
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.com/future.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, "future", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-72 * time.Hour)
	if _, err := surrealdb.Query[any](ctx, s.db,
		"REMOVE FIELD status ON extraction_run", nil); err != nil {
		t.Fatalf("remove current status field for future fixture: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $compatible CONTENT {
			run_id: 'future-compatible', repo: $repo, commit: 'future', domain: 'compatible',
			unit_digest: '',
			extractor: 'future', status: 'published', started_at: $now, published_at: $now,
			store_schema_version: 't12-store-v999', evidence_format_version: $format,
			retention_quarantined: false, published_key: $compatible_key
		};
		CREATE $incompatible CONTENT {
			run_id: 'future-incompatible', repo: $repo, commit: 'future', domain: 'incompatible',
			extractor: 'future', status: 'future-sealed', started_at: $now,
			store_schema_version: 't12-store-v999', evidence_format_version: 't12-evidence-v999',
			retention_quarantined: false
		};
		CREATE $compatible_aborted CONTENT {
			run_id: 'future-compatible-aborted', repo: $repo, commit: 'future', domain: 'retention',
			extractor: 'future', status: 'aborted', started_at: $old,
			store_schema_version: 't12-store-v999', evidence_format_version: $format,
			retention_quarantined: false
		};
		CREATE $compatible_staged CONTENT {
			run_id: 'future-compatible-staged', repo: $repo, commit: 'future', domain: 'retention',
			extractor: 'future', status: 'staged', started_at: $old,
			store_schema_version: 't12-store-v999', evidence_format_version: $format,
			retention_quarantined: false
		};
		CREATE $incompatible_aborted CONTENT {
			run_id: 'future-incompatible-aborted', repo: $repo, commit: 'future', domain: 'retention',
			extractor: 'future', status: 'aborted', started_at: $old,
			store_schema_version: 't12-store-v999', evidence_format_version: 't12-evidence-v999',
			retention_quarantined: false
		};
		CREATE $compatible_atom CONTENT {
			atom_id: 'future-compatible-atom', schema_version: 'future', blob_digest: 'future',
			start_byte: 0, end_byte: 1, rule_id: 'future', extractor_version: 'future',
			adapter_config_digest: 'future', fact_fingerprint: 'compatible', first_seen: $now
		};
		CREATE $compatible_occurrence CONTENT {
			occurrence_id: 'future-compatible-occurrence', atom_id: 'future-compatible-atom',
			atom_record: $compatible_atom, repo: $repo, commit: 'future', path: 'compatible.proto',
			start_line: 1, end_line: 1, visibility_scope: $visibility,
			run_id: 'future-compatible', observed_at: $now
		};
		CREATE $compatible_assertion CONTENT {
			assertion_id: 'future-compatible-assertion', predicate: 'Future',
			subject: 'compatible.proto', object: 'VisibleByRunOnly',
			tier: 'exact', repo: $repo, run_id: 'future-compatible',
			supporting: ['future-compatible-atom'], contradicting: []
		};
		CREATE $incompatible_atom CONTENT {
			atom_id: 'future-incompatible-atom', schema_version: 'future', blob_digest: 'future',
			start_byte: 0, end_byte: 1, rule_id: 'future', extractor_version: 'future',
			adapter_config_digest: 'future', fact_fingerprint: 'incompatible', first_seen: $now
		};
		CREATE $incompatible_occurrence CONTENT {
			occurrence_id: 'future-incompatible-occurrence', atom_id: 'future-incompatible-atom',
			atom_record: $incompatible_atom, repo: $repo, commit: 'future', path: 'incompatible.proto',
			start_line: 1, end_line: 1, visibility_scope: $visibility,
			run_id: 'future-incompatible', observed_at: $now
		};
		CREATE $incompatible_pin CONTENT {
			pin_key: 'future-incompatible-pin', run_id: 'future-incompatible',
			kind: 'future-proof', created_at: $now
		};`, map[string]any{
			"compatible":   extractionRunID("future-compatible"),
			"incompatible": extractionRunID("future-incompatible"),
			"compatible_aborted": extractionRunID(
				"future-compatible-aborted",
			),
			"compatible_staged": extractionRunID("future-compatible-staged"),
			"incompatible_aborted": extractionRunID(
				"future-incompatible-aborted",
			),
			"compatible_atom": evidenceAtomRecordID("future-compatible-atom"),
			"compatible_occurrence": snapshotEvidenceRecordID(
				"future-compatible", "future-compatible-occurrence",
			),
			"compatible_assertion": assertionRecordID(
				"future-compatible", "future-compatible-assertion",
			),
			"incompatible_atom": evidenceAtomRecordID("future-incompatible-atom"),
			"incompatible_occurrence": snapshotEvidenceRecordID(
				"future-incompatible", "future-incompatible-occurrence",
			),
			"incompatible_pin": evidencePinRecordID("future-incompatible", "future-proof"),
			"repo":             repo, "visibility": "repo:" + repo, "now": now, "old": old,
			"format": evidenceFormatVersion,
			"compatible_key": publishedKey(ExtractionScope{
				Repository: repo,
				Commit:     "future",
				Domain:     "compatible",
			}),
		}); err != nil {
		t.Fatal(err)
	}
	clearEvidenceMigrationMarker(t, s)
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("scan with future writers: %v", err)
	}

	compatible := evidenceMigrationState(t, s, "future-compatible")
	if compatible.StoreSchema != "t12-store-v999" || compatible.Format != evidenceFormatVersion ||
		compatible.Quarantined || compatible.Migration != "" {
		t.Fatalf("compatible future writer was mutated: %+v", compatible)
	}
	if _, err := latestPublishedRun(
		s, ctx, repo, "compatible",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf(
			"future writer publication entered the current read set: %v",
			err,
		)
	}
	runProof, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: "future-compatible",
	})
	if err != nil || len(runProof) != 1 {
		t.Fatalf(
			"compatible future proof by run = %+v, %v",
			runProof,
			err,
		)
	}
	currentScope, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo,
		Scope: &ExtractionScope{
			Repository: repo,
			Commit:     "future",
			Domain:     "compatible",
		},
	})
	if err != nil || len(currentScope) != 0 {
		t.Fatalf(
			"future writer entered v8 scope discovery = %+v, %v",
			currentScope,
			err,
		)
	}
	if err := s.PinRun(ctx, "future-compatible", "current-proof"); err != nil {
		t.Fatalf("compatible future run could not be pinned: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE $rid SET status = 'superseded', published_key = NONE RETURN NONE`,
		map[string]any{"rid": extractionRunID("future-compatible")}); err != nil {
		t.Fatalf("supersede compatible future fixture: %v", err)
	}
	resolved, err := s.ResolveEvidence(ctx, repo, "future-compatible", "future-compatible-atom")
	if err != nil || len(resolved.Occurrences) != 1 {
		t.Fatalf("pinned superseded compatible future proof could not be read: %+v, %v", resolved, err)
	}
	if err := s.AbortExtractionRun(ctx, "future-compatible-staged"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("older writer mutated future staged run: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, "future-compatible-staged", CoverageManifest{
		SourceScopeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("older writer published future staged run: %v", err)
	}

	incompatibleBefore := evidenceMigrationState(t, s, "future-incompatible")
	if _, err := s.ResolveEvidence(ctx, repo, "future-incompatible", "future-incompatible-atom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("incompatible future proof was visible: %v", err)
	}
	if err := s.PinRun(ctx, "future-incompatible", "current-proof"); !errors.Is(err, ErrConflict) {
		t.Fatalf("incompatible future run accepted a current pin: %v", err)
	}

	for range 2 {
		if n, err := sweepEvidenceRun(ctx, s, now, time.Hour); err != nil || n != 1 {
			t.Fatalf("compatible future retention sweep = %d, %v", n, err)
		}
	}
	if n, err := sweepEvidenceRun(ctx, s, now, time.Hour); err != nil || n != 0 {
		t.Fatalf("incompatible future row entered retention sweep: %d, %v", n, err)
	}
	for _, runID := range []string{"future-compatible-aborted", "future-compatible-staged"} {
		if exists, err := s.extractionRunExists(ctx, runID); err != nil || exists {
			t.Fatalf("eligible compatible future run %s remains: %v, %v", runID, exists, err)
		}
	}
	if exists, err := s.extractionRunExists(ctx, "future-incompatible-aborted"); err != nil || !exists {
		t.Fatalf("incompatible future run was deleted: %v, %v", exists, err)
	}
	if exists, err := s.extractionRunExists(ctx, "future-compatible"); err != nil || !exists {
		t.Fatalf("pinned superseded compatible proof was swept: %v, %v", exists, err)
	}
	incompatibleAfter := evidenceMigrationState(t, s, "future-incompatible")
	if incompatibleAfter != incompatibleBefore {
		t.Fatalf("incompatible future proof metadata changed: before=%+v after=%+v", incompatibleBefore, incompatibleAfter)
	}
}

func TestEvidenceRequiresCanonicalPhysicalRunAndPublicationEnvelope(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	repo := "github.com/migration/envelope"
	otherRepo := "github.com/migration/forged"
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $owner CONTENT {
				run_id: 'future-owner', repo: $repo, commit: 'owner', domain: 'contracts',
				extractor: 'future', status: 'published', started_at: $now, published_at: $now,
				store_schema_version: $current_schema, evidence_format_version: $format,
				evidence_migration_version: $migration,
				retention_quarantined: false, published_key: $owner_key
			};
			CREATE $rogue CONTENT {
				run_id: 'future-owner', repo: $repo, commit: 'owner', domain: 'contracts',
				extractor: 'future', status: 'published', started_at: $old, published_at: $future,
				store_schema_version: 't12-store-v999', evidence_format_version: $format,
				retention_quarantined: false, published_key: 'rogue-slot'
			};
			CREATE $malformed_claim CONTENT {
				run_id: {bad: true}, repo: $repo, commit: 'owner', domain: 'contracts',
				extractor: 'future', status: 'aborted', started_at: $old,
				store_schema_version: 't12-store-v999', evidence_format_version: $format,
				retention_quarantined: false
			};
			CREATE $unbounded_claim CONTENT {
				run_id: $unbounded_run_id, repo: $repo, commit: 'owner', domain: 'contracts',
				extractor: 'future', status: 'aborted', started_at: $old,
				store_schema_version: 't12-store-v999', evidence_format_version: $format,
				retention_quarantined: false
			};
		CREATE $missing_key CONTENT {
			run_id: 'future-missing-key', repo: $repo, commit: 'missing', domain: 'missing',
			extractor: 'future', status: 'published', started_at: $now, published_at: $now,
			store_schema_version: 't12-store-v999', evidence_format_version: $format,
			retention_quarantined: false
		};
		CREATE $stale_key CONTENT {
			run_id: 'future-stale-key', repo: $repo, commit: 'stale', domain: 'stale',
			extractor: 'future', status: 'superseded', started_at: $old,
			store_schema_version: 't12-store-v999', evidence_format_version: $format,
			retention_quarantined: false, published_key: 'stale-slot'
		};
			CREATE $current_mismatch CONTENT {
				run_id: 'current-mismatch-owner', repo: $repo, commit: 'current', domain: 'current',
			extractor: 'current', status: 'staged', started_at: $old,
			store_schema_version: $current_schema, evidence_format_version: $format,
			evidence_migration_version: $migration, retention_quarantined: false
		};
		CREATE $atom CONTENT {
			atom_id: 'envelope-atom', schema_version: 'future', blob_digest: 'future',
			start_byte: 0, end_byte: 1, rule_id: 'future', extractor_version: 'future',
			adapter_config_digest: 'future', fact_fingerprint: 'envelope', first_seen: $now
		};
		CREATE $owner_occurrence CONTENT {
			occurrence_id: 'owner-occurrence', atom_id: 'envelope-atom', atom_record: $atom,
			repo: $repo, commit: 'owner', path: 'owner.proto', start_line: 1, end_line: 1,
			visibility_scope: $visibility, run_id: 'future-owner', observed_at: $now
		};
			CREATE $rogue_occurrence CONTENT {
				occurrence_id: 'rogue-occurrence', atom_id: 'envelope-atom', atom_record: $atom,
				repo: $repo, commit: 'owner', path: 'rogue.proto', start_line: 1, end_line: 1,
				visibility_scope: $visibility, run_id: 'future-owner', observed_at: $now
			};
			CREATE $rogue_assertion CONTENT {
				assertion_id: 'rogue-assertion', predicate: 'Rogue', subject: 'rogue.proto', object: 'Injected',
				tier: 'exact', repo: $repo, run_id: 'future-owner',
				supporting: ['envelope-atom'], contradicting: []
			};
		CREATE $forged_assertion CONTENT {
			assertion_id: 'forged-assertion', predicate: 'P', subject: 's', object: 'o',
			tier: 'exact', repo: $other_repo, run_id: 'future-owner', supporting: [], contradicting: []
		};
		CREATE $stale_pin CONTENT {
			pin_key: 'stale-pin', run_id: 'future-stale-key', kind: 'proof', created_at: $now
		};`, map[string]any{
			"owner": extractionRunID("future-owner"),
			"rogue": extractionRunID("future-rogue"),
			"malformed_claim": extractionRunID(
				"future-malformed-claim",
			),
			"unbounded_claim": extractionRunID(
				"future-unbounded-claim",
			),
			"missing_key": extractionRunID("future-missing-key"),
			"stale_key":   extractionRunID("future-stale-key"),
			"current_mismatch": extractionRunID(
				"current-mismatch",
			),
			"atom": evidenceAtomRecordID("envelope-atom"),
			"owner_occurrence": snapshotEvidenceRecordID(
				"future-owner", "owner-occurrence",
			),
			"rogue_occurrence": snapshotEvidenceRecordID(
				"future-rogue", "rogue-occurrence",
			),
			"rogue_assertion": assertionRecordID(
				"future-rogue", "rogue-assertion",
			),
			"forged_assertion": assertionRecordID(
				"future-owner", "forged-assertion",
			),
			"stale_pin": evidencePinRecordID("future-stale-key", "proof"),
			"repo":      repo, "other_repo": otherRepo, "visibility": "repo:" + repo,
			"now": now, "old": now.Add(-72 * time.Hour), "future": now.Add(time.Hour),
			"format": evidenceFormatVersion, "owner_key": publishedKey(ExtractionScope{
				Repository: repo,
				Commit:     "owner",
				Domain:     "contracts",
			}),
			"current_schema": evidenceStoreSchemaVersion, "migration": evidenceMigrationVersion,
			"unbounded_run_id": strings.Repeat("é", maxEvidenceIdentityBytes/2+1),
		}); err != nil {
		t.Fatal(err)
	}

	if _, err := latestPublishedRun(s, ctx, repo, "contracts"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner with a live logical-id claimant remained latest: %v", err)
	}
	if err := s.PinRun(ctx, "future-rogue", "proof"); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched compatible run accepted a pin: %v", err)
	}
	if err := s.PinRun(ctx, "future-owner", "proof"); !errors.Is(err, ErrConflict) {
		t.Fatalf("owner with a live logical-id claimant accepted a pin: %v", err)
	}
	if _, err := s.ResolveEvidence(ctx, repo, "future-owner", "envelope-atom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("same-commit rogue occurrence leaked through physical owner: %v", err)
	}
	assertions, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: "future-owner",
	})
	if err != nil || len(assertions) != 0 {
		t.Fatalf("same-repo rogue assertion leaked through physical owner: %+v, %v", assertions, err)
	}
	forged, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: otherRepo, RunID: "future-owner",
	})
	if err != nil || len(forged) != 0 {
		t.Fatalf("cross-repo assertion borrowed a publication: %+v, %v", forged, err)
	}

	if _, err := latestPublishedRun(s, ctx, repo, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing publication key appeared latest: %v", err)
	}
	if err := s.PinRun(ctx, "future-missing-key", "proof"); !errors.Is(err, ErrConflict) {
		t.Fatalf("published run without slot accepted pin: %v", err)
	}
	if err := s.PinRun(ctx, "future-stale-key", "other-proof"); !errors.Is(err, ErrConflict) {
		t.Fatalf("superseded run with stale slot accepted pin: %v", err)
	}
	if _, err := s.ResolveEvidence(ctx, repo, "future-stale-key", "envelope-atom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseded run with stale slot resolved: %v", err)
	}
	if err := s.AddEvidence(ctx, "current-mismatch", nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched exact-writer run accepted evidence: %v", err)
	}
	if err := s.AbortExtractionRun(ctx, "current-mismatch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched exact-writer run aborted: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, "current-mismatch", CoverageManifest{
		SourceScopeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched exact-writer run published: %v", err)
	}

	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE $owner SET status = 'staged', published_key = NONE, started_at = $old RETURN NONE;
			UPDATE $rogue SET status = 'aborted', published_key = NONE RETURN NONE;`,
		map[string]any{
			"owner": extractionRunID("future-owner"),
			"rogue": extractionRunID("future-rogue"), "old": now.Add(-72 * time.Hour),
		}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEvidence(ctx, "future-owner", nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner with a live logical-id claimant accepted evidence: %v", err)
	}
	if err := s.AbortExtractionRun(ctx, "future-owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner with a live logical-id claimant aborted: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, "future-owner", CoverageManifest{
		SourceScopeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner with a live logical-id claimant published: %v", err)
	}
	if n, err := sweepEvidenceRun(ctx, s, now, time.Hour); err != nil || n != 0 {
		t.Fatalf("mismatched or stale-envelope run entered sweep: %d, %v", n, err)
	}
	for kind, record := range map[string]any{
		"owner run":          extractionRunID("future-owner"),
		"rogue run":          extractionRunID("future-rogue"),
		"malformed claimant": extractionRunID("future-malformed-claim"),
		"unbounded claimant": extractionRunID("future-unbounded-claim"),
		"stale-key run":      extractionRunID("future-stale-key"),
		"atom":               evidenceAtomRecordID("envelope-atom"),
		"owner occurrence":   snapshotEvidenceRecordID("future-owner", "owner-occurrence"),
		"rogue occurrence":   snapshotEvidenceRecordID("future-rogue", "rogue-occurrence"),
		"rogue assertion":    assertionRecordID("future-rogue", "rogue-assertion"),
	} {
		rows, queryErr := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
			"SELECT id FROM $rid", map[string]any{"rid": record})
		if queryErr != nil {
			t.Fatalf("read preserved %s: %v", kind, queryErr)
		}
		preserved := false
		for _, result := range *rows {
			preserved = preserved || len(result.Result) == 1
		}
		if !preserved {
			t.Fatalf("fail-closed %s was deleted", kind)
		}
	}
}

func TestSweepEvidenceDecodesOnlyCandidateIdentity(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $rid CONTENT {
			run_id: 'malformed-sweep', repo: 'github.com/migration/sweep', commit: 'future',
			domain: 'future', extractor: 'future', status: 'aborted', started_at: $old,
			coverage: 'not-a-coverage-manifest', published_at: {not_a_datetime: true},
			store_schema_version: 't12-store-v999', evidence_format_version: $format,
			retention_quarantined: false
		};`, map[string]any{
			"rid": extractionRunID("malformed-sweep"), "old": now.Add(-72 * time.Hour),
			"format": evidenceFormatVersion,
		}); err != nil {
		t.Fatal(err)
	}
	if n, err := sweepEvidenceRun(ctx, s, now, time.Hour); err != nil || n != 1 {
		t.Fatalf("sweep malformed irrelevant fields = %d, %v", n, err)
	}
	if exists, err := s.extractionRunExists(ctx, "malformed-sweep"); err != nil || exists {
		t.Fatalf("malformed eligible run remains: %v, %v", exists, err)
	}
}

func TestMigrateEvidenceRunsClassifiesMalformedSchemaTypes(t *testing.T) {
	s := newRunnerStore(t)
	relaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db,
		"REMOVE FIELD status ON extraction_run", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`CREATE $empty_v3 CONTENT {
			run_id: 'format-empty-v3', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
			status: 'published', started_at: $now, published_at: $now,
			unit_digest: '', store_schema_version: $v3, evidence_format_version: '',
			retention_quarantined: false, published_key: 'bad-empty-slot'
		};
		CREATE $object_v4 CONTENT {
			run_id: 'format-object-v4', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
			status: 'staged', started_at: $now, store_schema_version: $v4,
			evidence_format_version: {bad: true}, retention_quarantined: false
		};
		CREATE $object_schema CONTENT {
			run_id: 'schema-object', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
			status: 'published', started_at: $now, published_at: $now,
			store_schema_version: {bad: true}, evidence_format_version: $format,
			retention_quarantined: false, published_key: 'bad-schema-slot'
		};
		CREATE $status_object CONTENT {
			run_id: 'status-object-v4', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
			status: {bad: true}, started_at: $now, store_schema_version: $v4,
			evidence_format_version: $format, evidence_migration_version: 't12-evidence-migration-v1',
			retention_quarantined: false
		};
			CREATE $missing_run_id CONTENT {
				repo: 'repo', commit: 'c', domain: 'd', extractor: 'x', status: 'staged',
				started_at: $now, store_schema_version: $v4, evidence_format_version: $format,
				evidence_migration_version: 't12-evidence-migration-v1', retention_quarantined: false
			};
			CREATE $ambiguity_owner CONTENT {
				run_id: 'ambiguity-owner-v4', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
				status: 'staged', started_at: $now, store_schema_version: $v4,
				evidence_format_version: $format, evidence_migration_version: $migration,
				retention_quarantined: false
			};
			CREATE $ambiguity_object CONTENT {
				run_id: 'ambiguity-object-v4', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
				status: 'staged', started_at: $now, store_schema_version: $v4,
				evidence_format_version: $format, evidence_migration_version: $migration,
				evidence_migration_ambiguous_run_id: {bad: true}, retention_quarantined: false
			};
			CREATE $ambiguity_empty CONTENT {
				run_id: 'ambiguity-empty-v4', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
				status: 'staged', started_at: $now, store_schema_version: $v4,
				evidence_format_version: $format, evidence_migration_version: $migration,
				evidence_migration_ambiguous_run_id: '', retention_quarantined: false
			};
			CREATE $ambiguity_unquarantined CONTENT {
				run_id: 'ambiguity-unquarantined-v4', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
				status: 'staged', started_at: $now, store_schema_version: $v4,
				evidence_format_version: $format, evidence_migration_version: $migration,
				evidence_migration_ambiguous_run_id: 'ambiguity-owner-v4', retention_quarantined: false
			};
			CREATE $unknown_v3 CONTENT {
			run_id: 'format-unknown-v3', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
			status: 'future-state', started_at: $now, unit_digest: '', store_schema_version: $v3,
			evidence_format_version: 't12-evidence-v999', retention_quarantined: false
		};
		CREATE $unknown_v4 CONTENT {
			run_id: 'format-unknown-v4', repo: 'repo', commit: 'c', domain: 'd', extractor: 'x',
			status: 'future-state', started_at: $now, store_schema_version: $v4,
			evidence_format_version: 't12-evidence-v999', retention_quarantined: false
		};
		UPSERT $marker SET version = 't12-evidence-migration-v1', completed_at = $now;`,
		map[string]any{
			"empty_v3":  extractionRunID("format-empty-v3"),
			"object_v4": extractionRunID("format-object-v4"),
			"object_schema": extractionRunID(
				"schema-object",
			),
			"status_object": extractionRunID("status-object-v4"),
			"missing_run_id": extractionRunID(
				"missing-run-id-v4",
			),
			"ambiguity_owner": extractionRunID(
				"ambiguity-owner-v4",
			),
			"ambiguity_object": extractionRunID(
				"ambiguity-object-v4",
			),
			"ambiguity_empty": extractionRunID(
				"ambiguity-empty-v4",
			),
			"ambiguity_unquarantined": extractionRunID(
				"ambiguity-unquarantined-v4",
			),
			"unknown_v3": extractionRunID("format-unknown-v3"),
			"unknown_v4": extractionRunID("format-unknown-v4"),
			"marker":     evidenceMigrationStateID(), "now": now,
			"v3": evidencePreviousStoreSchemaVersion, "v4": evidenceStoreSchemaVersion,
			"format": evidenceFormatVersion, "migration": evidenceMigrationVersion,
		}); err != nil {
		t.Fatal(err)
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("classify malformed schema fields: %v", err)
	}

	for _, runID := range []string{
		"format-empty-v3", "format-object-v4", "schema-object",
		"status-object-v4", "missing-run-id-v4",
		"ambiguity-object-v4", "ambiguity-empty-v4", "ambiguity-unquarantined-v4",
	} {
		state := evidenceMigrationState(t, s, runID)
		if !state.Quarantined || state.StoreSchema != evidenceStoreSchemaVersion ||
			state.Format != evidenceFormatVersion || state.Migration != evidenceMigrationVersion ||
			(state.Status != "superseded" && state.Status != "aborted") {
			t.Fatalf("malformed schema row %s was not retired: %+v", runID, state)
		}
	}
	for _, runID := range []string{"ambiguity-object-v4", "ambiguity-empty-v4"} {
		if state := evidenceMigrationState(t, s, runID); state.Ambiguous != "" {
			t.Fatalf("malformed ambiguity marker %s survived normalization: %+v", runID, state)
		}
	}
	if state := evidenceMigrationState(t, s, "ambiguity-unquarantined-v4"); state.Ambiguous != "ambiguity-owner-v4" {
		t.Fatalf("valid ambiguity marker was not preserved: %+v", state)
	}
	if _, err := s.getRun(ctx, "ambiguity-owner-v4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner named by normalized ambiguity marker remained visible: %v", err)
	}
	for _, runID := range []string{"format-unknown-v3", "format-unknown-v4"} {
		state := evidenceMigrationState(t, s, runID)
		if state.Format != "t12-evidence-v999" || state.Status != "future-state" ||
			state.Quarantined || state.Migration != "" {
			t.Fatalf("unknown future format %s was mutated: %+v", runID, state)
		}
	}
	marker := evidenceMigrationMarker(t, s)
	if marker.Version != evidenceMigrationVersion {
		t.Fatalf("old global marker stranded new scan: %+v", marker)
	}
}
