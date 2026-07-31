package store

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidateid"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

func requireCandidateRawQuery(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	statement string,
	vars map[string]any,
) {
	t.Helper()
	results, err := surrealdb.Query[any](ctx, s.db, statement, vars)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("statement %d: %s", index, result.Error.Message)
		}
	}
}

func TestCandidateManifestLegacyQueueMigration(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	directory := t.TempDir()
	s, err := OpenLocal(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	firstClosed := false
	t.Cleanup(func() {
		if !firstClosed {
			_ = s.Close(context.Background())
		}
	})
	target := "github.com/acme/legacy-candidate"
	created := time.Now().UTC().Add(-time.Minute)
	requireCandidateRawQuery(t, ctx, s, `
CREATE candidate_manifest_job CONTENT {
	target: $target, status: 'pending', attempts: 0, created_at: $first
};
CREATE candidate_manifest_job CONTENT {
	target: $target, status: 'pending', attempts: 0, created_at: $second
};`, map[string]any{
		"target": target,
		"first":  created,
		"second": created.Add(time.Second),
	})
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	firstClosed = true

	reopened, err := OpenLocal(ctx, directory)
	if err != nil {
		t.Fatalf("reopen legacy candidate jobs: %v", err)
	}
	reopenedClosed := false
	t.Cleanup(func() {
		if !reopenedClosed {
			if err := reopened.Close(context.Background()); err != nil {
				t.Errorf("close reopened store: %v", err)
			}
		}
	})
	pending, err := reopened.ListJobs(ctx, JobCandidate, StatusPending)
	if err != nil || len(pending) != 1 || pending[0].Target != target {
		t.Fatalf("migrated pending = %+v, %v; want one", pending, err)
	}
	canceled, err := reopened.ListJobs(ctx, JobCandidate, StatusCanceled)
	if err != nil || len(canceled) != 1 ||
		canceled[0].Error != "migration: duplicate pending job" {
		t.Fatalf("migrated canceled = %+v, %v; want duplicate cancellation", canceled, err)
	}

	testResolverCatalogSchemaRejectsWrongWriter(t, ctx, reopened)
	requireCandidateRawQuery(t, ctx, reopened, `
UPDATE $rid SET version = 'future-resolver-catalog-writer' RETURN NONE;`,
		map[string]any{"rid": resolverCatalogMigrationID()},
	)
	if err := reopened.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopenedClosed = true
	if _, err := OpenLocal(ctx, directory); err == nil ||
		!strings.Contains(err.Error(), "unsupported resolver catalog writer generation") {
		t.Fatalf("reopen with future resolver writer marker = %v", err)
	}
}

func testResolverCatalogSchemaRejectsWrongWriter(
	t *testing.T, ctx context.Context, s *Surreal,
) {
	t.Helper()
	declarationDigest, err := resolverCatalogDeclarationSetDigest(
		[]ResolverCatalogDeclarationPublication{},
	)
	if err != nil {
		t.Fatal(err)
	}
	packDigest, err := resolverCatalogPackSetDigest(
		[]ResolverCatalogPack{},
	)
	if err != nil {
		t.Fatal(err)
	}
	results, err := surrealdb.Query[any](ctx, s.db, `
CREATE resolver_catalog_publication:test CONTENT {
	repository: 'github.com/acme/wrong-writer',
	head_commit: '1111111111111111111111111111111111111111',
	unit_digest: '',
	declarations: [],
	declaration_set_digest: $declaration_digest,
	candidate_manifest_digest: $digest,
	source_lane_policy: 'candidate-source-lane-base-v1',
	resolver_packs: [],
	resolver_pack_set_digest: $pack_digest,
	catalog_policy_digest: $digest,
	generation_digest: $digest,
	manifest_digest: $digest,
	manifest_path: 'wrong',
	control_revision: 1,
	writer_schema: 'future-writer',
	published_at: time::now()
};`, map[string]any{
		"digest":             "sha256:" + strings.Repeat("a", 64),
		"declaration_digest": declarationDigest,
		"pack_digest":        packDigest,
	})
	if err != nil {
		return
	}
	hasError := false
	for _, result := range *results {
		hasError = hasError || result.Error != nil
	}
	if !hasError {
		t.Fatal("wrong resolver catalog writer schema was accepted")
	}
}

func TestInvalidCandidatePointerDoesNotPoisonRepositoryReads(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	repository := "github.com/acme/isolated-validation"
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	requireCandidateRawQuery(t, ctx, s, `
UPSERT $rid SET
	repository = $repository,
	head_commit = $head_commit,
	unit_digest = '',
	policy_digest = $policy_digest,
	manifest_digest = 'tampered',
	generation_digest = $generation_digest,
	manifest_path = $manifest_path,
	published_at = time::now();`, map[string]any{
		"rid":               candidateManifestPublicationID(repository),
		"repository":        repository,
		"head_commit":       strings.Repeat("1", 40),
		"policy_digest":     "sha256:" + strings.Repeat("a", 64),
		"generation_digest": "sha256:" + strings.Repeat("b", 64),
		"manifest_path":     "phebs-candidate-" + strings.Repeat("c", 64) + ".manifest.json",
	})
	if _, err := s.GetCandidateManifestPublication(ctx, repository); err == nil ||
		!errors.Is(err, ErrInvalidCandidateManifestPublication) {
		t.Fatalf(
			"invalid candidate pointer read = %v, want ErrInvalidCandidateManifestPublication",
			err,
		)
	}
	if _, err := s.ListCandidateManifestPublications(ctx); !errors.Is(
		err, ErrInvalidCandidateManifestPublication,
	) {
		t.Fatalf(
			"invalid candidate pointer list = %v, want ErrInvalidCandidateManifestPublication",
			err,
		)
	}
	if got, err := s.GetRepo(ctx, repository); err != nil || got.Name != repository {
		t.Fatalf("GetRepo poisoned by candidate pointer: %+v, %v", got, err)
	}
	if got, err := s.ListRepos(ctx); err != nil || len(got) != 1 ||
		got[0].Name != repository {
		t.Fatalf("ListRepos poisoned by candidate pointer: %+v, %v", got, err)
	}
	if err := s.ClearAllCandidateManifestPublications(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCandidateManifestPublication(
		ctx, repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bulk clear invalid candidate pointer = %v, want ErrNotFound", err)
	}

	commit := strings.Repeat("1", 40)
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	publication := CandidateManifestPublication{
		Repository:       repository,
		HeadCommit:       commit,
		PolicyDigest:     "sha256:" + strings.Repeat("a", 64),
		ManifestDigest:   "sha256:" + strings.Repeat("b", 64),
		GenerationDigest: "sha256:" + strings.Repeat("c", 64),
		ManifestPath:     candidateid.ManifestName(repository),
	}
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatalf("exact publication retry: %v", err)
	}
	second, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil || !second.PublishedAt.Equal(first.PublishedAt) {
		t.Fatalf(
			"exact retry publication = %+v, %v; want timestamp %v",
			second, err, first.PublishedAt,
		)
	}
	pending, err := s.ListJobs(ctx, JobExtract, StatusPending)
	if err != nil || len(pending) != 1 || pending[0].Target != repository {
		t.Fatalf("publication fanout = %+v, %v; want one extraction", pending, err)
	}
	conflicting := publication
	conflicting.ManifestDigest = "sha256:" + strings.Repeat("e", 64)
	if err := s.PublishCandidateManifest(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting same-generation publication = %v, want ErrConflict", err)
	}
	replacement := publication
	replacement.PolicyDigest = "sha256:" + strings.Repeat("4", 64)
	replacement.ManifestDigest = "sha256:" + strings.Repeat("5", 64)
	replacement.GenerationDigest = "sha256:" + strings.Repeat("6", 64)
	if err := s.PublishCandidateManifest(ctx, replacement); err != nil {
		t.Fatalf("new-policy replacement: %v", err)
	}
	replaced, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil ||
		replaced.PolicyDigest != replacement.PolicyDigest ||
		replaced.ManifestDigest != replacement.ManifestDigest {
		t.Fatalf("new-policy pointer = %+v, %v; want replacement", replaced, err)
	}
	revisions := []IndexedRevision{
		{Selector: "HEAD", Branch: "HEAD", Commit: commit},
		{
			Selector: "release",
			Branch:   "refs/heads/release",
			Commit:   strings.Repeat("2", 40),
		},
	}
	if err := s.SetRepoIndexedState(
		ctx, repository, commit, revisions, nil, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repository); err != nil {
		t.Fatalf("search-only revisions invalidated pointer: %v", err)
	}
	if err := s.SetRepoIndexed(
		ctx, repository, strings.Repeat("3", 40), time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repository); !errors.Is(err, ErrNotFound) {
		t.Fatalf("HEAD change pointer = %v, want ErrNotFound", err)
	}

	currentCommit := strings.Repeat("3", 40)
	firstUnit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "first",
		Primary:    []string{"services/first"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	secondUnit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "second",
		Primary:    []string{"services/second"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexedState(
		ctx, repository, currentCommit,
		[]IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: currentCommit}},
		firstUnit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	publication.HeadCommit = currentCommit
	publication.UnitDigest = firstUnit.Digest
	publication.PolicyDigest = "sha256:" + strings.Repeat("4", 64)
	publication.ManifestDigest = "sha256:" + strings.Repeat("5", 64)
	publication.GenerationDigest = "sha256:" + strings.Repeat("6", 64)
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexedState(
		ctx, repository, currentCommit,
		[]IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: currentCommit}},
		secondUnit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repository); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unit change pointer = %v, want ErrNotFound", err)
	}
}
