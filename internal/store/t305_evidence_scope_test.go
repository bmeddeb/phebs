package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidateid"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

const t305EmptyDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type t305MigrationState struct {
	UnitDigest   string `json:"unit_digest"`
	StoreSchema  string `json:"store_schema_version"`
	Migration    string `json:"evidence_migration_version"`
	PublishedKey any    `json:"published_key"`
}

type t305RunMutationState struct {
	Status       string `json:"status"`
	StoreSchema  string `json:"store_schema_version"`
	Format       string `json:"evidence_format_version"`
	PublishedKey any    `json:"published_key"`
}

func t305RelaxEvidenceWriterGuards(t *testing.T, s *Surreal) {
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

func t305ClearEvidenceMigrationMarker(t *testing.T, s *Surreal) {
	t.Helper()
	if _, err := surrealdb.Query[any](context.Background(), s.db,
		"DELETE $rid RETURN NONE",
		map[string]any{"rid": evidenceMigrationStateID()},
	); err != nil {
		t.Fatalf("clear evidence migration marker: %v", err)
	}
}

func t305ReadMigrationState(
	t *testing.T, s *Surreal, runID string,
) t305MigrationState {
	t.Helper()
	results, err := surrealdb.Query[[]t305MigrationState](context.Background(), s.db,
		`SELECT unit_digest, store_schema_version, evidence_migration_version,
			published_key FROM $rid`,
		map[string]any{"rid": extractionRunID(runID)},
	)
	if err != nil {
		t.Fatalf("read migration state: %v", err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatalf("read migration state: row not found")
	return t305MigrationState{}
}

func t305ReadRunMutationState(
	t *testing.T, s *Surreal, recordID any,
) t305RunMutationState {
	t.Helper()
	results, err := surrealdb.Query[[]t305RunMutationState](
		context.Background(),
		s.db,
		`SELECT status, store_schema_version, evidence_format_version,
			published_key FROM $rid`,
		map[string]any{"rid": recordID},
	)
	if err != nil {
		t.Fatalf("read run mutation state: %v", err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatalf("read run mutation state: row not found")
	return t305RunMutationState{}
}

func t305OverwriteCoverage(
	t *testing.T,
	s *Surreal,
	runID string,
	coverage CoverageManifest,
) {
	t.Helper()
	results, err := surrealdb.Query[any](
		context.Background(),
		s.db,
		`UPDATE $rid SET coverage = $coverage RETURN NONE`,
		map[string]any{
			"rid":      extractionRunID(runID),
			"coverage": coverage,
		},
	)
	if err != nil {
		t.Fatalf("overwrite focused coverage: %v", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf(
				"overwrite focused coverage statement %d: %s",
				index,
				result.Error.Message,
			)
		}
	}
}

func t305Unit(t *testing.T, repository, name, primary string) *analysisunit.State {
	t.Helper()
	state, err := (analysisunit.Scope{
		Repository: repository,
		Name:       name,
		Primary:    []string{primary},
	}).State()
	if err != nil {
		t.Fatalf("analysis unit %s: %v", name, err)
	}
	return state
}

func t305IndexedState(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	repository, commit string,
	unit *analysisunit.State,
) {
	t.Helper()
	if err := s.SetRepoIndexedState(ctx, repository, commit, []IndexedRevision{{
		Selector: "HEAD",
		Branch:   "HEAD",
		Commit:   commit,
	}}, unit, time.Now().UTC()); err != nil {
		t.Fatalf("set indexed state: %v", err)
	}
}

func t305EvidenceRevision(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	repository string,
) int64 {
	t.Helper()
	repo, err := s.GetRepo(ctx, repository)
	if err != nil {
		t.Fatalf("get repository evidence revision: %v", err)
	}
	return repo.EvidenceRevision
}

func t305PublishAssertion(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	scope ExtractionScope,
	extractor, object string,
) *ExtractionRun {
	t.Helper()
	if err := s.PublishCandidateManifest(
		ctx,
		t305CandidatePublication(
			scope.Repository,
			scope.Commit,
			scope.UnitDigest,
			t305EmptyDigest,
			t305EmptyDigest,
			t305EmptyDigest,
		),
	); err != nil {
		t.Fatalf("publish candidate pointer for %s: %v", object, err)
	}
	return t305PublishAssertionForManifest(
		t, ctx, s, scope, extractor, object, t305EmptyDigest,
	)
}

func t305PublishAssertionForManifest(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	scope ExtractionScope,
	extractor, object, manifestDigest string,
) *ExtractionRun {
	t.Helper()
	run, err := s.BeginExtractionRun(ctx, scope, extractor)
	if err != nil {
		t.Fatalf("begin %s: %v", object, err)
	}
	atom := EvidenceAtom{
		SchemaVersion:       "t30.5-v1",
		BlobDigest:          "blob-" + object,
		StartByte:           0,
		EndByte:             len(object) + 1,
		RuleID:              "scope",
		ExtractorVersion:    extractor,
		AdapterConfigDigest: "none",
		FactFingerprint:     "scope|" + object,
	}
	atom.ID = ComputeAtomID(atom)
	association := SnapshotEvidence{
		AtomID:          atom.ID,
		Repo:            scope.Repository,
		Commit:          scope.Commit,
		Path:            "service/" + object + ".go",
		StartLine:       1,
		EndLine:         1,
		VisibilityScope: "repo:" + scope.Repository,
	}
	assertion := Assertion{
		Predicate:  "OWNS_SCOPE",
		Subject:    association.Path,
		Object:     object,
		Tier:       TierExact,
		Repo:       scope.Repository,
		Supporting: []string{atom.ID},
	}
	if err := s.AddEvidence(
		ctx,
		run.ID,
		[]EvidenceAtom{atom},
		[]SnapshotEvidence{association},
		[]Assertion{assertion},
	); err != nil {
		t.Fatalf("add %s: %v", object, err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, CoverageManifest{
		CorpusFileCount: 1, CandidateFileCount: 1, ReadFileCount: 1,
		ReadBytes: int64(len(object) + 1), SourceScopeDigest: t305EmptyDigest,
		AssertionCount: 1, AtomCount: 1,
		ScopePosture:             "focused-local",
		CandidateManifestDigest:  manifestDigest,
		CandidatePlane:           "local",
		ScopeCorpusFileCount:     1,
		ScopeCorpusDeclaredBytes: int64(len(object) + 1),
		ScopeCorpusDigest:        t305EmptyDigest,
		PlannedFileCount:         1,
		PlannedRequiredFileCount: 1,
		PlannedDeclaredBytes:     int64(len(object) + 1),
		PlannedScopeDigest:       t305EmptyDigest,
	}); err != nil {
		t.Fatalf("publish %s: %v", object, err)
	}
	return run
}

func t305CandidatePublication(
	repository, commit, unitDigest, policyDigest, manifestDigest, generationDigest string,
) CandidateManifestPublication {
	return CandidateManifestPublication{
		Repository: repository, HeadCommit: commit, UnitDigest: unitDigest,
		PolicyDigest: policyDigest, ManifestDigest: manifestDigest,
		GenerationDigest: generationDigest,
		ManifestPath:     candidateid.ManifestName(repository),
	}
}

func TestT305TypedAndCandidateTransitionsInvalidateCurrentEvidence(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	const (
		repository = "github.com/t305/typed-transition"
		commit     = "1111111111111111111111111111111111111111"
		domain     = "scip-proto-field"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/t305-typed.git",
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
	unitA, err := unitScope.State()
	if err != nil {
		t.Fatal(err)
	}
	unitScope.TypedIndex.Path = "service/b.scip"
	unitB, err := unitScope.State()
	if err != nil {
		t.Fatal(err)
	}
	if unitA.Digest != unitB.Digest {
		t.Fatal("typed designation changed semantic unit digest")
	}
	scope := ExtractionScope{
		Repository: repository, Commit: commit,
		UnitDigest: unitA.Digest, Domain: domain,
	}
	digest := func(fill string) string {
		return "sha256:" + strings.Repeat(fill, 64)
	}
	publicationA := t305CandidatePublication(
		repository, commit, unitA.Digest,
		digest("a"), digest("b"), digest("c"),
	)
	publicationB := t305CandidatePublication(
		repository, commit, unitB.Digest,
		digest("a"), digest("d"), digest("e"),
	)
	publicationC := t305CandidatePublication(
		repository, commit, unitB.Digest,
		digest("f"), digest("1"), digest("2"),
	)
	publicationD := t305CandidatePublication(
		repository, commit, unitB.Digest,
		digest("3"), digest("4"), digest("5"),
	)

	t305IndexedState(t, ctx, s, repository, commit, unitA)
	if err := s.PublishCandidateManifest(ctx, publicationA); err != nil {
		t.Fatal(err)
	}
	runA := t305PublishAssertionForManifest(
		t, ctx, s, scope, "extractor-a", "typed-a",
		publicationA.ManifestDigest,
	)
	if attempt, err := s.LatestExtractionAttempt(ctx, scope); err != nil ||
		attempt.RunID != runA.ID || attempt.Status != "published" {
		t.Fatalf("initial attempt = %+v, %v", attempt, err)
	}

	beforeTypedTransition := t305EvidenceRevision(
		t,
		ctx,
		s,
		repository,
	)
	t305IndexedState(t, ctx, s, repository, commit, unitB)
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeTypedTransition+1 {
		t.Fatalf(
			"typed transition evidence revision = %d, want %d",
			got,
			beforeTypedTransition+1,
		)
	}
	if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("typed transition publication = %v, want not found", err)
	}
	if _, err := s.LatestExtractionAttempt(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("typed transition attempt = %v, want not found", err)
	}
	if retained, err := s.getRun(ctx, runA.ID); err != nil ||
		retained.Status != "superseded" {
		t.Fatalf("typed transition retained run = %+v, %v", retained, err)
	}
	if _, err := s.GetCandidateManifestPublication(
		ctx, repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("typed transition candidate pointer = %v, want not found", err)
	}

	if err := s.PublishCandidateManifest(ctx, publicationB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("candidate-only transition publication = %v, want not found", err)
	}
	stagedScope := scope
	stagedScope.Domain = "staged-retry"
	stagedRun, err := s.BeginExtractionRun(ctx, stagedScope, "staged-retry")
	if err != nil {
		t.Fatalf("begin staged exact-retry run: %v", err)
	}
	if err := s.PublishCandidateManifest(ctx, publicationB); err != nil {
		t.Fatalf("retry candidate with staged extraction: %v", err)
	}
	if current, err := s.getRun(ctx, stagedRun.ID); err != nil ||
		current.Status != "staged" {
		t.Fatalf("staged run after exact candidate retry = %+v, %v", current, err)
	}
	if attempt, err := s.LatestExtractionAttempt(ctx, stagedScope); err != nil ||
		attempt.RunID != stagedRun.ID || attempt.Status != "staged" {
		t.Fatalf("staged attempt after exact candidate retry = %+v, %v", attempt, err)
	}
	runB := t305PublishAssertionForManifest(
		t, ctx, s, scope, "extractor-b", "typed-b",
		publicationB.ManifestDigest,
	)
	if latest, err := s.LatestPublishedRun(ctx, scope); err != nil ||
		latest.ID != runB.ID {
		t.Fatalf("typed-b publication = %+v, %v", latest, err)
	}

	beforeCandidateTransition := t305EvidenceRevision(
		t,
		ctx,
		s,
		repository,
	)
	if err := s.PublishCandidateManifest(ctx, publicationC); err != nil {
		t.Fatal(err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeCandidateTransition+1 {
		t.Fatalf(
			"candidate transition evidence revision = %d, want %d",
			got,
			beforeCandidateTransition+1,
		)
	}
	if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("policy transition publication = %v, want not found", err)
	}
	if _, err := s.LatestExtractionAttempt(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("policy transition attempt = %v, want not found", err)
	}
	if current, err := s.getRun(ctx, stagedRun.ID); err != nil ||
		current.Status != "aborted" {
		t.Fatalf("staged run after real candidate transition = %+v, %v", current, err)
	}
	if _, err := s.LatestExtractionAttempt(
		ctx,
		stagedScope,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("staged attempt after real candidate transition = %v, want not found", err)
	}
	if retained, err := s.getRun(ctx, runB.ID); err != nil ||
		retained.Status != "superseded" {
		t.Fatalf("policy transition retained run = %+v, %v", retained, err)
	}

	runC := t305PublishAssertionForManifest(
		t, ctx, s, scope, "extractor-c", "typed-c",
		publicationC.ManifestDigest,
	)
	beforeExactRetry := t305EvidenceRevision(t, ctx, s, repository)
	if err := s.PublishCandidateManifest(ctx, publicationC); err != nil {
		t.Fatalf("exact candidate retry: %v", err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeExactRetry {
		t.Fatalf(
			"exact candidate retry evidence revision = %d, want %d",
			got,
			beforeExactRetry,
		)
	}
	if latest, err := s.LatestPublishedRun(ctx, scope); err != nil ||
		latest.ID != runC.ID {
		t.Fatalf("exact retry publication = %+v, %v", latest, err)
	}
	if attempt, err := s.LatestExtractionAttempt(ctx, scope); err != nil ||
		attempt.RunID != runC.ID || attempt.Status != "published" {
		t.Fatalf("exact retry attempt = %+v, %v", attempt, err)
	}

	// Clearing and then restoring an exact pointer can reactivate a retained
	// matching run without changing the visible tuple. Both pointer edges must
	// advance the monotonic result fence, while an exact retry stays stable.
	beforePointerClear := t305EvidenceRevision(t, ctx, s, repository)
	if err := s.ClearCandidateManifestPublication(ctx, repository); err != nil {
		t.Fatalf("clear exact candidate pointer: %v", err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforePointerClear+1 {
		t.Fatalf(
			"candidate clear evidence revision = %d, want %d",
			got,
			beforePointerClear+1,
		)
	}
	if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publication without candidate pointer = %v, want not found", err)
	}
	beforePointerRestore := t305EvidenceRevision(t, ctx, s, repository)
	if err := s.PublishCandidateManifest(ctx, publicationC); err != nil {
		t.Fatalf("restore exact candidate pointer: %v", err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforePointerRestore+1 {
		t.Fatalf(
			"candidate restore evidence revision = %d, want %d",
			got,
			beforePointerRestore+1,
		)
	}
	if latest, err := s.LatestPublishedRun(ctx, scope); err != nil ||
		latest.ID != runC.ID {
		t.Fatalf("restored exact publication = %+v, %v", latest, err)
	}
	beforeRestoredRetry := t305EvidenceRevision(t, ctx, s, repository)
	if err := s.PublishCandidateManifest(ctx, publicationC); err != nil {
		t.Fatalf("retry restored exact candidate pointer: %v", err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeRestoredRetry {
		t.Fatalf(
			"restored exact retry evidence revision = %d, want %d",
			got,
			beforeRestoredRetry,
		)
	}

	const (
		futureSchema = "t12-store-v999"
		futureKey    = "future-candidate-published-key"
	)
	futureRID := extractionRunID("future-candidate-claimant")
	if _, err := surrealdb.Query[any](
		ctx,
		s.db,
		`CREATE $future_rid CONTENT {
			run_id: $claimed_run_id,
			repo: $repository,
			commit: $commit,
			unit_digest: $unit_digest,
			domain: $domain,
			extractor: 'future-candidate-writer',
			status: 'published',
			started_at: $now,
			published_at: $now,
			store_schema_version: $future_schema,
			evidence_format_version: $evidence_format,
			retention_quarantined: false,
			published_key: $future_key,
			coverage: {
				candidate_manifest_digest: $manifest_digest
			}
		}`,
		map[string]any{
			"future_rid":      futureRID,
			"claimed_run_id":  runC.ID,
			"repository":      repository,
			"commit":          commit,
			"unit_digest":     unitB.Digest,
			"domain":          domain,
			"now":             time.Now().UTC(),
			"future_schema":   futureSchema,
			"evidence_format": evidenceFormatVersion,
			"future_key":      futureKey,
			"manifest_digest": publicationC.ManifestDigest,
		},
	); err != nil {
		t.Fatalf("create future logical-id claimant: %v", err)
	}
	beforeGuardedInvalidation := t305EvidenceRevision(
		t,
		ctx,
		s,
		repository,
	)
	if err := s.PublishCandidateManifest(ctx, publicationD); err != nil {
		t.Fatalf("publish candidate with future claimant: %v", err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeGuardedInvalidation+1 {
		t.Fatalf(
			"guarded invalidation evidence revision = %d, want %d",
			got,
			beforeGuardedInvalidation+1,
		)
	}
	future := t305ReadRunMutationState(t, s, futureRID)
	if future.Status != "published" ||
		future.StoreSchema != futureSchema ||
		future.Format != evidenceFormatVersion ||
		future.PublishedKey != futureKey {
		t.Fatalf("future claimant was mutated: %+v", future)
	}
	owner := t305ReadRunMutationState(t, s, extractionRunID(runC.ID))
	if owner.Status != "published" || owner.PublishedKey == nil {
		t.Fatalf("ambiguous canonical owner was mutated: %+v", owner)
	}
	if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous candidate transition publication = %v, want not found", err)
	}
	if _, err := s.LatestExtractionAttempt(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous candidate transition attempt = %v, want not found", err)
	}
	pointer, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil || pointer.ManifestDigest != publicationD.ManifestDigest {
		t.Fatalf("candidate pointer after guarded invalidation = %+v, %v", pointer, err)
	}

	// The internal fence is monotonic across semantic scope, commit, and clear
	// transitions. In particular A -> B -> A must not become invisible to a
	// result-time consumer merely because the endpoint state equals its start.
	unitScope.Name = "service-next"
	unitC, err := unitScope.State()
	if err != nil {
		t.Fatal(err)
	}
	if unitC.Digest == unitB.Digest {
		t.Fatal("semantic unit transition did not change digest")
	}
	beforeScopeChange := t305EvidenceRevision(t, ctx, s, repository)
	t305IndexedState(t, ctx, s, repository, commit, unitC)
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeScopeChange+1 {
		t.Fatalf("scope transition evidence revision = %d, want %d", got, beforeScopeChange+1)
	}
	const rollbackCommit = "9999999999999999999999999999999999999999"
	beforeCommitChange := t305EvidenceRevision(t, ctx, s, repository)
	t305IndexedState(t, ctx, s, repository, rollbackCommit, unitC)
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeCommitChange+1 {
		t.Fatalf("commit transition evidence revision = %d, want %d", got, beforeCommitChange+1)
	}
	beforeABA := t305EvidenceRevision(t, ctx, s, repository)
	t305IndexedState(t, ctx, s, repository, commit, unitB)
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeABA+1 {
		t.Fatalf("ABA return evidence revision = %d, want %d", got, beforeABA+1)
	}
	beforeClear := t305EvidenceRevision(t, ctx, s, repository)
	if err := s.ClearRepoIndexState(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeClear+1 {
		t.Fatalf("clear evidence revision = %d, want %d", got, beforeClear+1)
	}
	if err := s.ClearRepoIndexState(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeClear+1 {
		t.Fatalf("no-op clear evidence revision = %d, want %d", got, beforeClear+1)
	}
}

func TestT305SearchPostureTransitionsInvalidateSameTupleEvidence(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	const (
		repository = "github.com/t305/search-posture-transition"
		commit     = "dddddddddddddddddddddddddddddddddddddddd"
		domain     = "proto-contract"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/search-posture.git",
	}); err != nil {
		t.Fatal(err)
	}
	focused := t305Unit(t, repository, "service", "service")
	whole := analysisunit.CloneState(focused)
	whole.SearchIndexPosture = analysisunit.SearchIndexWholeRepository
	if err := whole.Validate(repository); err != nil {
		t.Fatalf("validate legacy whole-repository state: %v", err)
	}
	digest := func(fill string) string {
		return "sha256:" + strings.Repeat(fill, 64)
	}
	publication := t305CandidatePublication(
		repository,
		commit,
		focused.Digest,
		digest("a"),
		digest("b"),
		digest("c"),
	)
	scope := ExtractionScope{
		Repository: repository, Commit: commit,
		UnitDigest: focused.Digest, Domain: domain,
	}

	t305IndexedState(t, ctx, s, repository, commit, whole)
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	wholeRun := t305PublishAssertionForManifest(
		t,
		ctx,
		s,
		scope,
		"whole-posture",
		"whole-posture",
		publication.ManifestDigest,
	)
	beforeFocused := t305EvidenceRevision(t, ctx, s, repository)
	t305IndexedState(t, ctx, s, repository, commit, focused)
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeFocused+1 {
		t.Fatalf("whole-to-focused evidence revision = %d, want %d", got, beforeFocused+1)
	}
	if _, err := s.GetCandidateManifestPublication(
		ctx,
		repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("whole-to-focused candidate pointer = %v, want not found", err)
	}
	if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("whole-to-focused publication = %v, want not found", err)
	}
	if _, err := s.LatestExtractionAttempt(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("whole-to-focused attempt = %v, want not found", err)
	}
	if retained, err := s.getRun(ctx, wholeRun.ID); err != nil ||
		retained.Status != "superseded" {
		t.Fatalf("whole-to-focused retained run = %+v, %v", retained, err)
	}

	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	focusedRun := t305PublishAssertionForManifest(
		t,
		ctx,
		s,
		scope,
		"focused-posture",
		"focused-posture",
		publication.ManifestDigest,
	)
	beforeWhole := t305EvidenceRevision(t, ctx, s, repository)
	t305IndexedState(t, ctx, s, repository, commit, whole)
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeWhole+1 {
		t.Fatalf("focused-to-whole evidence revision = %d, want %d", got, beforeWhole+1)
	}
	repo, err := s.GetRepo(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if !analysisunit.EqualState(repo.IndexedAnalysisUnit, whole) {
		t.Fatalf("whole-posture rollback state = %+v, want %+v", repo.IndexedAnalysisUnit, whole)
	}
	if _, err := s.GetCandidateManifestPublication(
		ctx,
		repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focused-to-whole candidate pointer = %v, want not found", err)
	}
	if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focused-to-whole publication = %v, want not found", err)
	}
	if _, err := s.LatestExtractionAttempt(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focused-to-whole attempt = %v, want not found", err)
	}
	if retained, err := s.getRun(ctx, focusedRun.ID); err != nil ||
		retained.Status != "superseded" {
		t.Fatalf("focused-to-whole retained run = %+v, %v", retained, err)
	}
	beforeNoOp := t305EvidenceRevision(t, ctx, s, repository)
	t305IndexedState(t, ctx, s, repository, commit, whole)
	if got := t305EvidenceRevision(t, ctx, s, repository); got != beforeNoOp {
		t.Fatalf("exact posture retry evidence revision = %d, want %d", got, beforeNoOp)
	}
}

func TestT305EvidenceScopesFenceAndRemainIndependent(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	const (
		repository = "github.com/t305/scoped"
		commit     = "2222222222222222222222222222222222222222"
		domain     = "proto-contract"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/t305.git",
	}); err != nil {
		t.Fatal(err)
	}
	unitA := t305Unit(t, repository, "service-a", "service/a")
	unitB := t305Unit(t, repository, "service-b", "service/b")
	scopeA := ExtractionScope{
		Repository: repository, Commit: commit,
		UnitDigest: unitA.Digest, Domain: domain,
	}
	scopeB := ExtractionScope{
		Repository: repository, Commit: commit,
		UnitDigest: unitB.Digest, Domain: domain,
	}
	t305IndexedState(t, ctx, s, repository, commit, unitA)
	runA := t305PublishAssertion(t, ctx, s, scopeA, "extractor-a", "unit-a")

	staleA, err := s.BeginExtractionRun(ctx, scopeA, "stale-a")
	if err != nil {
		t.Fatal(err)
	}
	t305IndexedState(t, ctx, s, repository, commit, unitB)
	if err := s.PublishExtractionRun(ctx, staleA.ID, CoverageManifest{
		SourceScopeDigest:       t305EmptyDigest,
		ScopePosture:            "focused-local",
		CandidateManifestDigest: t305EmptyDigest,
		CandidatePlane:          "local",
		ScopeCorpusDigest:       t305EmptyDigest,
		PlannedScopeDigest:      t305EmptyDigest,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale unit publication error = %v, want conflict", err)
	}

	runB := t305PublishAssertion(t, ctx, s, scopeB, "extractor-b1", "unit-b1")
	replacementB := t305PublishAssertion(t, ctx, s, scopeB, "extractor-b2", "unit-b2")

	latestA, err := s.LatestPublishedRun(ctx, scopeA)
	if err != nil || latestA.ID != runA.ID {
		t.Fatalf("unit A publication = %+v, %v", latestA, err)
	}
	latestB, err := s.LatestPublishedRun(ctx, scopeB)
	if err != nil || latestB.ID != replacementB.ID {
		t.Fatalf("unit B publication = %+v, %v", latestB, err)
	}
	supersededB, err := s.getRun(ctx, runB.ID)
	if err != nil || supersededB.Status != "superseded" {
		t.Fatalf("first unit B run = %+v, %v", supersededB, err)
	}
	for name, scope := range map[string]ExtractionScope{
		"whole repository": {
			Repository: repository, Commit: commit, Domain: domain,
		},
		"other commit": {
			Repository: repository, Commit: "other", UnitDigest: unitB.Digest, Domain: domain,
		},
	} {
		if _, err := s.LatestPublishedRun(ctx, scope); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s lookup = %v, want not found", name, err)
		}
	}

	if _, err := s.ListAssertions(ctx, AssertionQuery{Repo: repository}); err == nil {
		t.Fatal("repository-wide assertion query unexpectedly accepted")
	}
	assertionsA, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repository, Scope: &scopeA,
	})
	if err != nil || len(assertionsA) != 1 || assertionsA[0].Object != "unit-a" {
		t.Fatalf("unit A assertions = %+v, %v", assertionsA, err)
	}
	assertionsB, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repository, Scope: &scopeB,
	})
	if err != nil || len(assertionsB) != 1 || assertionsB[0].Object != "unit-b2" {
		t.Fatalf("unit B assertions = %+v, %v", assertionsB, err)
	}
	mismatched, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repository, RunID: runA.ID, Scope: &scopeB,
	})
	if err != nil || len(mismatched) != 0 {
		t.Fatalf("mismatched run/scope assertions = %+v, %v", mismatched, err)
	}

	attemptA, err := s.LatestExtractionAttempt(ctx, scopeA)
	if err != nil || attemptA.RunID != staleA.ID || attemptA.Status != "staged" {
		t.Fatalf("unit A attempt = %+v, %v", attemptA, err)
	}
	attemptB, err := s.LatestExtractionAttempt(ctx, scopeB)
	if err != nil || attemptB.RunID != replacementB.ID || attemptB.Status != "published" {
		t.Fatalf("unit B attempt = %+v, %v", attemptB, err)
	}
}

func TestT305LatestPublishedRunRejectsTamperedFocusedCoverage(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	const (
		repository = "github.com/t305/tampered-coverage"
		commit     = "3333333333333333333333333333333333333333"
		domain     = "proto-contract"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/tampered.git",
	}); err != nil {
		t.Fatal(err)
	}
	unit := t305Unit(t, repository, "service", "service")
	t305IndexedState(t, ctx, s, repository, commit, unit)
	scope := ExtractionScope{
		Repository: repository,
		Commit:     commit,
		UnitDigest: unit.Digest,
		Domain:     domain,
	}
	run := t305PublishAssertion(
		t, ctx, s, scope, "scope-extractor", "valid",
	)

	// Model an out-of-band/raw-row edit which preserves the identity fields
	// used by no-op checks but drops the receipts that prove focused coverage.
	tampered := CoverageManifest{
		CorpusFileCount:         1,
		CandidateFileCount:      1,
		ReadFileCount:           1,
		ReadBytes:               6,
		SourceScopeDigest:       t305EmptyDigest,
		AssertionCount:          1,
		AtomCount:               1,
		CandidateManifestDigest: t305EmptyDigest,
	}
	t305OverwriteCoverage(t, s, run.ID, tampered)

	if _, err := s.LatestPublishedRun(ctx, scope); err == nil ||
		!strings.Contains(err.Error(), "invalid stored coverage") {
		t.Fatalf("tampered focused publication read = %v", err)
	}
}

func TestT305LatestPublishedRunRejectsMismatchedCandidateReceipt(
	t *testing.T,
) {
	s := newRunnerStore(t)
	ctx := context.Background()
	const (
		repository = "github.com/t305/mismatched-receipt"
		commit     = "4444444444444444444444444444444444444444"
		domain     = "proto-contract"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name:     repository,
		CloneURL: "https://example.com/mismatched.git",
	}); err != nil {
		t.Fatal(err)
	}
	unit := t305Unit(t, repository, "service", "service")
	t305IndexedState(t, ctx, s, repository, commit, unit)
	scope := ExtractionScope{
		Repository: repository,
		Commit:     commit,
		UnitDigest: unit.Digest,
		Domain:     domain,
	}
	run := t305PublishAssertion(
		t,
		ctx,
		s,
		scope,
		"scope-extractor",
		"valid-receipt",
	)
	t305OverwriteCoverage(t, s, run.ID, CoverageManifest{
		CorpusFileCount:          1,
		CandidateFileCount:       1,
		ReadFileCount:            1,
		ReadBytes:                int64(len("valid-receipt") + 1),
		SourceScopeDigest:        t305EmptyDigest,
		AssertionCount:           1,
		AtomCount:                1,
		ScopePosture:             "focused-local",
		CandidateManifestDigest:  "sha256:" + strings.Repeat("f", 64),
		CandidatePlane:           "local",
		ScopeCorpusFileCount:     1,
		ScopeCorpusDeclaredBytes: int64(len("valid-receipt") + 1),
		ScopeCorpusDigest:        t305EmptyDigest,
		PlannedFileCount:         1,
		PlannedRequiredFileCount: 1,
		PlannedDeclaredBytes:     int64(len("valid-receipt") + 1),
		PlannedScopeDigest:       t305EmptyDigest,
	})
	if _, err := s.LatestPublishedRun(ctx, scope); err == nil ||
		!strings.Contains(
			err.Error(),
			"candidate publication receipt disagrees",
		) {
		t.Fatalf("mismatched candidate receipt read = %v", err)
	}
}

// A predecessor that does record unit digests must keep them across the writer
// bump. Reading the unit digest only at the current generation would recompute
// this row's published_key against an empty unit, collapsing a focused
// publication into the whole-repository slot for the same repository, commit,
// and domain — silently, and only for stores that had focused evidence.
func TestT306bWriterBumpPreservesPredecessorUnitScope(t *testing.T) {
	s := newRunnerStore(t)
	t305RelaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	const (
		repository = "github.com/t306b/unit-scope"
		commit     = "focused-head"
		domain     = "scip-field"
		runID      = "predecessor-focused-run"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/t306b.git",
	}); err != nil {
		t.Fatal(err)
	}
	focused := t305Unit(t, repository, "focused-now", "service/current")
	t305IndexedState(t, ctx, s, repository, commit, focused)
	focusedScope := ExtractionScope{
		Repository: repository, Commit: commit,
		UnitDigest: focused.Digest, Domain: domain,
	}
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db, `
		CREATE $run_rid CONTENT {
			run_id: $run_id, repo: $repo, commit: $commit,
			unit_digest: $unit, domain: $domain, extractor: 'v1',
			status: 'published', started_at: $now, published_at: $now,
			store_schema_version: $previous_schema,
			evidence_format_version: $format,
			retention_quarantined: false,
			published_key: $focused_key,
			coverage: {
				corpus_file_count: 0, candidate_file_count: 0,
				read_file_count: 0, read_bytes: 0,
				source_scope_digest: $empty_digest,
				unresolved_count: 0, assertion_count: 0, atom_count: 0
			}
		};
		CREATE $attempt_rid CONTENT {
			run_id: $run_id, repo: $repo, commit: $commit,
			unit_digest: $unit, domain: $domain, extractor: 'v1',
			status: 'published', started_at: $now,
			store_schema_version: $previous_schema,
			evidence_format_version: $format
		};`, map[string]any{
		"run_rid": extractionRunID(runID), "run_id": runID,
		"attempt_rid": extractionAttemptID(focusedScope),
		"repo":        repository, "commit": commit, "domain": domain,
		"unit": focused.Digest, "now": now,
		"previous_schema": evidencePreviousStoreSchemaVersion,
		"format":          evidenceFormatVersion,
		"empty_digest":    t305EmptyDigest,
		"focused_key":     publishedKey(focusedScope),
	}); err != nil {
		t.Fatal(err)
	}
	t305ClearEvidenceMigrationMarker(t, s)
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("apply writer bump migration: %v", err)
	}

	state := t305ReadMigrationState(t, s, runID)
	if state.UnitDigest != focused.Digest ||
		state.PublishedKey != publishedKey(focusedScope) ||
		state.StoreSchema != evidenceStoreSchemaVersion ||
		state.Migration != evidenceMigrationVersion {
		t.Fatalf("migrated focused run = %+v", state)
	}
	// Collapsing into the whole-repository slot is the exact corruption this
	// guards, so assert the recomputed key against it directly. Reading the run
	// back through LatestPublishedRun would pull in the whole T30.5
	// candidate-receipt fence stack, which other tests own; the migration's job
	// is only to land the row in the right slot.
	whole := ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}
	if state.PublishedKey == publishedKey(whole) {
		t.Fatal("focused publication collapsed into the whole-repository slot")
	}
	// The attempt row keeps its own id and unit digest: it is stamped in place,
	// never moved, so there is no destination for it to collide with.
	attempt, err := s.LatestExtractionAttempt(ctx, focusedScope)
	if err != nil || attempt.RunID != runID ||
		attempt.UnitDigest != focused.Digest {
		t.Fatalf("focused attempt = %+v, %v", attempt, err)
	}
}

// The pre-unit writer predates focused scopes entirely, so a unit digest on one
// of its rows is never authentic. This is bound to that generation's own
// literal, not to "the previous generation": once a generation that legitimately
// records unit digests occupies the previous slot, ignoring its digest would
// silently relabel real focused evidence as whole-repository.
func TestT305PreUnitWriterMigratesOnlyToExplicitWholeScope(t *testing.T) {
	s := newRunnerStore(t)
	t305RelaxEvidenceWriterGuards(t, s)
	ctx := context.Background()
	const (
		repository = "github.com/t305/migration"
		commit     = "same-head"
		domain     = "scip-field"
		runID      = "v7-whole-run"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/t305-migration.git",
	}); err != nil {
		t.Fatal(err)
	}
	focused := t305Unit(t, repository, "focused-now", "service/current")
	t305IndexedState(t, ctx, s, repository, commit, focused)
	now := time.Now().UTC()
	if _, err := surrealdb.Query[any](ctx, s.db, `
		CREATE $run_rid CONTENT {
			run_id: $run_id, repo: $repo, commit: $commit,
			unit_digest: $forged_unit, domain: $domain, extractor: 'v7',
			status: 'published', started_at: $now, published_at: $now,
			store_schema_version: $previous_schema,
			evidence_format_version: $format,
			retention_quarantined: false,
			published_key: $legacy_key,
			coverage: {
				corpus_file_count: 0, candidate_file_count: 0,
				read_file_count: 0, read_bytes: 0,
				source_scope_digest: $empty_digest,
				unresolved_count: 0, assertion_count: 0, atom_count: 0
			}
		};
		CREATE $attempt_rid CONTENT {
			run_id: $run_id, repo: $repo, commit: $commit,
			unit_digest: $forged_unit, domain: $domain, extractor: 'v7',
			status: 'aborted', started_at: $now,
			store_schema_version: $previous_schema,
			evidence_format_version: $format
		};`, map[string]any{
		"run_rid": extractionRunID(runID), "run_id": runID,
		"attempt_rid": legacyExtractionAttemptID(repository, domain),
		"repo":        repository, "commit": commit, "domain": domain,
		"forged_unit": focused.Digest, "now": now,
		"previous_schema": evidenceLegacyUpgradableStoreSchemaVersion,
		"format":          evidenceFormatVersion,
		"empty_digest":    t305EmptyDigest,
		"legacy_key":      legacyPublishedKey(repository, domain),
	}); err != nil {
		t.Fatal(err)
	}
	t305ClearEvidenceMigrationMarker(t, s)
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("apply pre-unit writer migration: %v", err)
	}

	whole := ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}
	focusedScope := whole
	focusedScope.UnitDigest = focused.Digest
	state := t305ReadMigrationState(t, s, runID)
	if state.UnitDigest != "" || state.PublishedKey != publishedKey(whole) ||
		state.StoreSchema != evidenceStoreSchemaVersion ||
		state.Migration != evidenceMigrationVersion {
		t.Fatalf("migrated whole run = %+v", state)
	}
	run, err := s.LatestPublishedRun(ctx, whole)
	if err != nil || run.ID != runID || run.UnitDigest != "" {
		t.Fatalf("whole publication = %+v, %v", run, err)
	}
	if _, err := s.LatestPublishedRun(ctx, focusedScope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focused lookup relabeled v7 evidence: %v", err)
	}
	attempt, err := s.LatestExtractionAttempt(ctx, whole)
	if err != nil || attempt.RunID != runID || attempt.UnitDigest != "" ||
		attempt.Status != "aborted" {
		t.Fatalf("whole attempt = %+v, %v", attempt, err)
	}
	if _, err := s.LatestExtractionAttempt(ctx, focusedScope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focused attempt relabeled v7 diagnostics: %v", err)
	}
}
