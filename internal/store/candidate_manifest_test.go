package store_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/store"
)

func candidateDigest(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

func candidateCommit(fill byte) string {
	return strings.Repeat(string(fill), 40)
}

func candidatePublication(repository, commit, unitDigest string) store.CandidateManifestPublication {
	return store.CandidateManifestPublication{
		Repository:       repository,
		HeadCommit:       commit,
		UnitDigest:       unitDigest,
		PolicyDigest:     candidateDigest('a'),
		ManifestDigest:   candidateDigest('b'),
		GenerationDigest: candidateDigest('c'),
		ManifestPath:     candidateid.ManifestName(repository),
	}
}

func setCandidateIndexedState(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository,
	commit string,
	unit *analysisunit.State,
	revisions []store.IndexedRevision,
) {
	t.Helper()
	if revisions == nil {
		revisions = []store.IndexedRevision{{
			Selector: "HEAD",
			Branch:   "HEAD",
			Commit:   commit,
		}}
	}
	if err := s.SetRepoIndexedState(
		ctx, repository, commit, revisions, unit, time.Now().UTC(),
	); err != nil {
		t.Fatalf("SetRepoIndexedState: %v", err)
	}
}

func finishPendingCandidateExtraction(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
) {
	t.Helper()
	job, err := s.ClaimJob(ctx, store.JobExtract, "candidate-publication-test")
	if err != nil {
		t.Fatalf("claim extraction: %v", err)
	}
	if err := s.SetJobStatus(ctx, *job, store.StatusRunning, ""); err != nil {
		t.Fatalf("start extraction: %v", err)
	}
	if err := s.SetJobStatus(ctx, *job, store.StatusDone, ""); err != nil {
		t.Fatalf("finish extraction: %v", err)
	}
}

func TestCandidateManifestGuardedPublicationAndIdempotentFanout(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/candidate"
	commit := candidateCommit('1')
	publication := candidatePublication(repository, commit, "")

	if err := s.PublishCandidateManifest(ctx, publication); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("publish missing repository = %v, want ErrConflict", err)
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get missing publication = %v, want ErrNotFound", err)
	}
	jobs, err := s.ListJobs(ctx, store.JobExtract, store.StatusPending)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("stale publish extraction jobs = %+v, %v; want none", jobs, err)
	}

	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://github.com/acme/candidate.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatalf("publish: %v", err)
	}
	published, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if published.PublishedAt.IsZero() {
		t.Fatal("published pointer has zero timestamp")
	}
	firstPublishedAt := published.PublishedAt
	publication.PublishedAt = time.Unix(1, 0)
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	published, err = s.GetCandidateManifestPublication(ctx, repository)
	if err != nil || !published.PublishedAt.Equal(firstPublishedAt) {
		t.Fatalf(
			"exact retry published_at = %v, %v; want preserved %v",
			published.PublishedAt, err, firstPublishedAt,
		)
	}
	jobs, err = s.ListJobs(ctx, store.JobExtract, store.StatusPending)
	if err != nil || len(jobs) != 1 || jobs[0].Target != repository {
		t.Fatalf("initial fanout = %+v, %v; want one pending extraction", jobs, err)
	}

	finishPendingCandidateExtraction(t, ctx, s)
	const retries = 16
	errs := make(chan error, retries)
	var wg sync.WaitGroup
	for range retries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.PublishCandidateManifest(ctx, publication)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent exact retry: %v", err)
		}
	}
	jobs, err = s.ListJobs(ctx, store.JobExtract, store.StatusPending)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("repaired fanout = %+v, %v; want one pending extraction", jobs, err)
	}

	conflicting := publication
	conflicting.ManifestDigest = candidateDigest('e')
	if err := s.PublishCandidateManifest(ctx, conflicting); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same-scope conflicting manifest = %v, want ErrConflict", err)
	}
	published, err = s.GetCandidateManifestPublication(ctx, repository)
	if err != nil || published.ManifestDigest != publication.ManifestDigest {
		t.Fatalf("pointer after conflict = %+v, %v; want original", published, err)
	}
}

func assertCandidateFanoutPreservesFreshResolverWork(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
) {
	t.Helper()
	repository := "github.com/acme/candidate-recovery-race"
	commit := candidateCommit('8')
	publication := candidatePublication(repository, commit, "")
	if err := s.UpsertRepo(ctx, store.Repo{
		Name:     repository,
		CloneURL: "https://github.com/acme/candidate-recovery-race.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	active, err := s.ClaimJob(ctx, store.JobResolverCatalog, "resolver-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *active, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	active.Attempts = 2
	recovery, err := s.EnsureJobSuccessor(ctx, *active, false)
	if err != nil {
		t.Fatal(err)
	}
	// This exact candidate retry is independent freshness work. Its direct SQL
	// fan-out must clear the active lease's recovery ownership even though it
	// merely coalesces into the already-pending row.
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err := s.FailJobWithSuccessor(
		ctx, *active, "final resolver publication failure",
	); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	if err != nil || len(pending) != 1 || pending[0].ID != recovery.ID ||
		pending[0].Attempts != 0 {
		t.Fatalf("fresh resolver successor = %+v, %v; want %s at attempt 0",
			pending, err, recovery.ID)
	}
	failed, err := s.ListJobs(ctx, store.JobResolverCatalog, store.StatusFailed)
	if err != nil || len(failed) != 1 || failed[0].ID != active.ID ||
		failed[0].Attempts != 3 {
		t.Fatalf("exhausted active resolver = %+v, %v; want %s at attempt 3",
			failed, err, active.ID)
	}
}

func TestCandidateManifestInvalidationTracksHeadUnitAndTypedDesignation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/scoped"
	firstCommit := candidateCommit('1')
	secondCommit := candidateCommit('2')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://github.com/acme/scoped.git",
	}); err != nil {
		t.Fatal(err)
	}
	firstUnit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "payments",
		Primary:    []string{"services/payments/main.go"},
		Supporting: []string{
			"services/payments/a.scip",
			"services/payments/b.scip",
		},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: "services/payments/a.scip",
		},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	alternateTypedUnit := analysisunit.CloneState(firstUnit)
	alternateTypedUnit.TypedIndex.Path = "services/payments/b.scip"
	if err := alternateTypedUnit.Validate(repository); err != nil {
		t.Fatal(err)
	}
	if alternateTypedUnit.Digest != firstUnit.Digest {
		t.Fatal("typed-index designation changed the semantic unit digest")
	}
	secondUnit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "ledger",
		Primary:    []string{"services/ledger"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}

	setCandidateIndexedState(t, ctx, s, repository, firstCommit, firstUnit, nil)
	first := candidatePublication(repository, firstCommit, firstUnit.Digest)
	if err := s.PublishCandidateManifest(ctx, first); err != nil {
		t.Fatal(err)
	}
	searchOnly := []store.IndexedRevision{
		{Selector: "HEAD", Branch: "HEAD", Commit: firstCommit},
		{Selector: "release", Branch: "refs/heads/release", Commit: candidateCommit('3')},
	}
	setCandidateIndexedState(
		t, ctx, s, repository, firstCommit, firstUnit, searchOnly,
	)
	if _, err := s.GetCandidateManifestPublication(ctx, repository); err != nil {
		t.Fatalf("search-only revision invalidated pointer: %v", err)
	}

	setCandidateIndexedState(
		t, ctx, s, repository, firstCommit, alternateTypedUnit, searchOnly,
	)
	if _, err := s.GetCandidateManifestPublication(
		ctx, repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("typed-index change pointer = %v, want ErrNotFound", err)
	}
	typedReplacement := first
	typedReplacement.ManifestDigest = candidateDigest('d')
	typedReplacement.GenerationDigest = candidateDigest('e')
	if err := s.PublishCandidateManifest(ctx, typedReplacement); err != nil {
		t.Fatalf("typed-index generation replacement: %v", err)
	}
	setCandidateIndexedState(
		t, ctx, s, repository, firstCommit, alternateTypedUnit, nil,
	)
	if published, err := s.GetCandidateManifestPublication(ctx, repository); err != nil ||
		published.GenerationDigest != typedReplacement.GenerationDigest {
		t.Fatalf(
			"unchanged typed-index pointer = %+v, %v; want generation %q",
			published, err, typedReplacement.GenerationDigest,
		)
	}

	setCandidateIndexedState(t, ctx, s, repository, secondCommit, firstUnit, nil)
	if _, err := s.GetCandidateManifestPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("HEAD change pointer = %v, want ErrNotFound", err)
	}
	if err := s.PublishCandidateManifest(ctx, first); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale HEAD publish = %v, want ErrConflict", err)
	}
	second := candidatePublication(repository, secondCommit, firstUnit.Digest)
	if err := s.PublishCandidateManifest(ctx, second); err != nil {
		t.Fatal(err)
	}

	setCandidateIndexedState(t, ctx, s, repository, secondCommit, secondUnit, nil)
	if _, err := s.GetCandidateManifestPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unit change pointer = %v, want ErrNotFound", err)
	}
	second.UnitDigest = secondUnit.Digest
	second.PolicyDigest = candidateDigest('4')
	second.ManifestDigest = candidateDigest('5')
	second.GenerationDigest = candidateDigest('6')
	if err := s.PublishCandidateManifest(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearRepoIndexState(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("clear index pointer = %v, want ErrNotFound", err)
	}
}

func TestCandidateManifestClearDeleteAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repositories := []string{
		"github.com/acme/zeta",
		"github.com/acme/alpha",
	}
	for index, repository := range repositories {
		commit := candidateCommit(byte('1' + index))
		if err := s.UpsertRepo(ctx, store.Repo{
			Name: repository, CloneURL: "https://" + repository + ".git",
		}); err != nil {
			t.Fatal(err)
		}
		setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
		publication := candidatePublication(repository, commit, "")
		publication.PolicyDigest = candidateDigest(byte('3' + index))
		publication.ManifestDigest = candidateDigest(byte('5' + index))
		publication.GenerationDigest = candidateDigest(byte('7' + index))
		if err := s.PublishCandidateManifest(ctx, publication); err != nil {
			t.Fatal(err)
		}
	}
	publications, err := s.ListCandidateManifestPublications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotRepositories := make([]string, len(publications))
	for index := range publications {
		gotRepositories[index] = publications[index].Repository
	}
	if want := []string{repositories[1], repositories[0]}; !reflect.DeepEqual(gotRepositories, want) {
		t.Fatalf("listed repositories = %v, want %v", gotRepositories, want)
	}

	beforeClear, err := s.GetRepo(ctx, repositories[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClearCandidateManifestPublication(ctx, repositories[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repositories[0]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("explicit clear = %v, want ErrNotFound", err)
	}
	afterClear, err := s.GetRepo(ctx, repositories[0])
	if err != nil {
		t.Fatal(err)
	}
	if afterClear.EvidenceRevision != beforeClear.EvidenceRevision+1 {
		t.Fatalf(
			"clear evidence revision = %d, want %d",
			afterClear.EvidenceRevision,
			beforeClear.EvidenceRevision+1,
		)
	}
	if err := s.ClearCandidateManifestPublication(ctx, repositories[0]); err != nil {
		t.Fatal(err)
	}
	afterNoopClear, err := s.GetRepo(ctx, repositories[0])
	if err != nil {
		t.Fatal(err)
	}
	if afterNoopClear.EvidenceRevision != afterClear.EvidenceRevision {
		t.Fatalf(
			"no-op clear evidence revision = %d, want %d",
			afterNoopClear.EvidenceRevision,
			afterClear.EvidenceRevision,
		)
	}
	if _, err := s.EnqueuePending(ctx, store.JobCandidate, repositories[1], false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepo(ctx, repositories[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repositories[1]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete pointer = %v, want ErrNotFound", err)
	}
	canceled, err := s.ListJobs(ctx, store.JobCandidate, store.StatusCanceled)
	if err != nil || len(canceled) != 1 || canceled[0].Target != repositories[1] {
		t.Fatalf("deleted candidate jobs = %+v, %v; want one canceled", canceled, err)
	}

	testResolverCatalogStoreLifecycle(t, s)
}

func TestCandidateManifestPublicationRejectsInvalidPrimitiveIdentity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/strict"
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://github.com/acme/strict.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	valid := candidatePublication(repository, commit, "")
	tests := []struct {
		name   string
		mutate func(*store.CandidateManifestPublication)
	}{
		{"repository", func(value *store.CandidateManifestPublication) {
			value.Repository = "../strict"
		}},
		{"commit", func(value *store.CandidateManifestPublication) {
			value.HeadCommit = "not-a-commit"
		}},
		{"unit digest", func(value *store.CandidateManifestPublication) {
			value.UnitDigest = "sha256:ABC"
		}},
		{"policy digest", func(value *store.CandidateManifestPublication) {
			value.PolicyDigest = "sha256:short"
		}},
		{"manifest digest", func(value *store.CandidateManifestPublication) {
			value.ManifestDigest = ""
		}},
		{"generation digest", func(value *store.CandidateManifestPublication) {
			value.GenerationDigest = "candidate-manifest-v1"
		}},
		{"absolute manifest path", func(value *store.CandidateManifestPublication) {
			value.ManifestPath = "/var/lib/phebs/candidate.json"
		}},
		{"unclean manifest path", func(value *store.CandidateManifestPublication) {
			value.ManifestPath = "candidate/../manifest.json"
		}},
		{"wrong stable manifest path", func(value *store.CandidateManifestPublication) {
			value.ManifestPath = candidateid.ManifestName("github.com/acme/other")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if err := s.PublishCandidateManifest(ctx, value); err == nil {
				t.Fatal("invalid publication succeeded")
			}
		})
	}
	if _, err := s.GetCandidateManifestPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid inputs created pointer: %v", err)
	}
	jobs, err := s.ListJobs(ctx, store.JobExtract, store.StatusPending)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("invalid inputs created jobs: %+v, %v", jobs, err)
	}
}

// Candidate-manifest publication mints the resolver-catalog successor inside
// its own domain transaction; the resolver job projection must follow that
// fan-out — creation and repair — so caller-generation evidence never
// classifies from a stale resolver row.
func TestCandidateFanoutMaintainsResolverJobProjection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/candidate-resolver-projection"
	commit := candidateCommit('9')
	publication := candidatePublication(repository, commit, "")
	if err := s.UpsertRepo(ctx, store.Repo{
		Name:     repository,
		CloneURL: "https://github.com/acme/candidate-resolver-projection.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	state, projection, err := s.GetResolverJobProjection(ctx, repository)
	if err != nil || state != store.JobProjectionExact || projection == nil ||
		projection.Status != store.StatusPending {
		t.Fatalf("fanout resolver projection = %v %+v %v", state, projection, err)
	}
	active, err := s.ClaimJob(ctx, store.JobResolverCatalog, "resolver-projection-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *active, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *active, store.StatusFailed, "died before minting the caller successor"); err != nil {
		t.Fatal(err)
	}
	state, projection, err = s.GetResolverJobProjection(ctx, repository)
	if err != nil || state != store.JobProjectionExact || projection == nil ||
		projection.Status != store.StatusFailed {
		t.Fatalf("failed resolver projection = %v %+v %v", state, projection, err)
	}
	// An exact candidate retry repairs the dead pipeline with a fresh pending
	// successor through the same domain transaction; the projection must move
	// with it instead of continuing to present the settled failure.
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	state, projection, err = s.GetResolverJobProjection(ctx, repository)
	if err != nil || state != store.JobProjectionExact || projection == nil ||
		projection.Status != store.StatusPending {
		t.Fatalf("repaired resolver projection = %v %+v %v", state, projection, err)
	}
}
