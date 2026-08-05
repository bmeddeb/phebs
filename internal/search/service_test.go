package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

type serviceSearchStore struct {
	store.Store
	repo        store.Repo
	publication servicecatalog.Publication
	summary     servicecatalog.RepositoryState
	state       servicecatalog.ServiceState
	confirm     int
	failFinal   bool
	repairs     int
}

func (s *serviceSearchStore) GetRepo(_ context.Context, repository string) (*store.Repo, error) {
	if repository != s.repo.Name {
		return nil, store.ErrNotFound
	}
	repo := s.repo
	repo.IndexedRevisions = slices.Clone(s.repo.IndexedRevisions)
	return &repo, nil
}

func (s *serviceSearchStore) ListRepos(context.Context) ([]store.Repo, error) {
	repo, _ := s.GetRepo(context.Background(), s.repo.Name)
	return []store.Repo{*repo}, nil
}

func (s *serviceSearchStore) GetServiceStateRead(
	_ context.Context,
	repository, serviceKey string,
) (*store.ServiceStateRead, error) {
	if repository != s.repo.Name || serviceKey != s.state.ServiceKey {
		return nil, store.ErrNotFound
	}
	publication := s.publication
	publication.Canonical = slices.Clone(publication.Canonical)
	state := s.state
	state.Successors = slices.Clone(state.Successors)
	return &store.ServiceStateRead{
		Publication: publication, Summary: s.summary,
		Entry: store.ServiceStateEntry{State: state},
	}, nil
}

func (s *serviceSearchStore) GetServiceCatalogGeneration(
	_ context.Context,
	repository, generation string,
) (*servicecatalog.Publication, error) {
	if repository != s.repo.Name || generation != s.publication.GenerationDigest {
		return nil, store.ErrNotFound
	}
	publication := s.publication
	publication.ControlRevision = 0
	publication.PublishedAt = time.Time{}
	publication.Canonical = slices.Clone(publication.Canonical)
	return &publication, nil
}

func (s *serviceSearchStore) GetServiceCatalog(
	_ context.Context,
	repository string,
) (*servicecatalog.Publication, error) {
	if repository != s.repo.Name {
		return nil, store.ErrNotFound
	}
	publication := s.publication
	publication.Canonical = slices.Clone(publication.Canonical)
	return &publication, nil
}

func (s *serviceSearchStore) GetServiceStateSummary(
	_ context.Context,
	repository string,
) (*servicecatalog.RepositoryState, error) {
	if repository != s.repo.Name {
		return nil, store.ErrNotFound
	}
	summary := s.summary
	return &summary, nil
}

func (s *serviceSearchStore) ServiceGenerationActivationNeeded(
	_ context.Context,
	repository, catalogGeneration, sourceGeneration, searchGeneration string,
) (bool, error) {
	if repository != s.repo.Name || catalogGeneration != s.publication.GenerationDigest ||
		sourceGeneration != s.state.DesiredSourceGeneration {
		return false, store.ErrConflict
	}
	return s.state.Disposition == servicecatalog.DispositionAccepted &&
		(s.state.Status != servicecatalog.StatusCurrent ||
			s.state.ActiveSearchGeneration != searchGeneration), nil
}

func (s *serviceSearchStore) ActivateServiceGeneration(
	_ context.Context,
	repository, catalogGeneration, sourceGeneration, searchGeneration string,
) (int, error) {
	needed, err := s.ServiceGenerationActivationNeeded(
		context.Background(), repository, catalogGeneration, sourceGeneration,
		searchGeneration,
	)
	if err != nil || !needed {
		return 0, err
	}
	s.state.ActiveDesiredGeneration = s.state.DesiredGeneration
	s.state.ActiveSourceGeneration = s.state.DesiredSourceGeneration
	s.state.ActiveCatalogGeneration = s.state.DesiredCatalogGeneration
	s.state.ActiveSearchGeneration = searchGeneration
	s.state.Status = servicecatalog.StatusCurrent
	s.state.ControlRevision++
	s.state.ChangedAt = time.Now().UTC()
	if err := servicecatalog.SetServiceStateDigest(&s.state); err != nil {
		return 0, err
	}
	s.summary.UnavailableCount--
	s.summary.CurrentCount++
	s.summary.ControlRevision++
	s.summary.UpdatedAt = time.Now().UTC()
	if err := servicecatalog.SetRepositoryStateDigest(&s.summary); err != nil {
		return 0, err
	}
	return 1, nil
}

func (s *serviceSearchStore) ConfirmServiceStateSnapshot(
	_ context.Context,
	repository string,
	expected servicecatalog.RepositoryState,
) error {
	s.confirm++
	if s.failFinal && s.confirm >= 3 {
		return store.ErrConflict
	}
	if repository != s.repo.Name || expected.SummaryDigest != s.summary.SummaryDigest ||
		expected.ControlRevision != s.summary.ControlRevision {
		return store.ErrConflict
	}
	return nil
}

func (s *serviceSearchStore) EnqueuePending(
	_ context.Context,
	kind store.JobKind,
	target string,
	force bool,
) (*store.Job, error) {
	if kind != store.JobIndex || target != s.repo.Name || !force {
		return nil, errors.New("unexpected repair request")
	}
	s.repairs++
	return &store.Job{Kind: kind, Target: target, Force: true}, nil
}

func TestServiceSearchUsesExactV2PredicateAndFinalFence(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	searcher, err := Open(fixture.indexDir, fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)

	result, err := searcher.SearchService(t.Context(), ServiceRequest{
		Repository: fixture.store.repo.Name, ServiceKey: "orders",
		Expression: "T343_NEEDLE", RevisionSelector: "HEAD",
	}, Options{MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Result.Files) != 1 ||
		result.Result.Files[0].Path != "services/orders/main.go" ||
		result.Authority.RepositorySearchGeneration != fixture.search.Digest ||
		result.Authority.ServiceKey != "orders" {
		t.Fatalf("service result = %+v", result)
	}

	fixture.store.failFinal = true
	fixture.store.confirm = 0
	if _, err := searcher.SearchService(t.Context(), ServiceRequest{
		Repository: fixture.store.repo.Name, ServiceKey: "orders",
		Expression: "T343_NEEDLE", RevisionSelector: "HEAD",
	}, Options{MaxMatches: 10}); !errors.Is(err, servicequery.ErrUnavailable) {
		t.Fatalf("final state-fence error = %v", err)
	}
}

func TestSharedSearchScopeReceiptDistinguishesAllCodeAndService(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	searcher, err := Open(fixture.indexDir, fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)
	allCode, err := searcher.SearchScoped(
		t.Context(), ScopeSelector{Kind: ScopeAllCode}, "T343_NEEDLE",
		Options{MaxMatches: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(allCode.Files) != 2 || allCode.Scope == nil ||
		allCode.Scope.Kind != ScopeAllCode || len(allCode.Scope.Revisions) != 1 ||
		allCode.Scope.MembershipPolicy != allCodeMembershipPolicy ||
		allCode.Scope.Digest != scopeReceiptDigest(*allCode.Scope) {
		t.Fatalf("All code scoped result = %+v", allCode)
	}
	service, err := searcher.SearchScoped(t.Context(), ScopeSelector{
		Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
	}, "T343_NEEDLE", Options{MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.Files) != 1 || service.Files[0].Path != "services/orders/main.go" ||
		service.Scope == nil || service.Scope.Authority == nil ||
		service.Scope.ServiceStatus != servicecatalog.StatusCurrent ||
		service.Scope.MembershipPolicy != serviceMembershipPolicy ||
		service.Scope.Authority.RepositorySearchGeneration != fixture.search.Digest ||
		service.Scope.ResultSetDigest == allCode.Scope.ResultSetDigest ||
		service.Scope.Digest != scopeReceiptDigest(*service.Scope) {
		t.Fatalf("service scoped result = %+v", service)
	}
	if _, err := searcher.SearchScoped(t.Context(), ScopeSelector{
		Kind: ScopeAllCode, Repository: fixture.store.repo.Name,
	}, "T343_NEEDLE", Options{}); err == nil {
		t.Fatal("All code selector accepted service identity fields")
	}
}

func TestServiceSearchRefusesInvalidV2WithoutLegacyFallback(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	searchPath := filepath.Join(
		fixture.indexDir,
		repositoryindex.SearchManifestName(fixture.store.repo.Name),
	)
	content, err := os.ReadFile(searchPath)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)-2] ^= 1
	if err := os.WriteFile(searchPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	searcher, err := Open(fixture.indexDir, fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)
	if _, err := searcher.SearchService(t.Context(), ServiceRequest{
		Repository: fixture.store.repo.Name, ServiceKey: "orders",
		Expression: "T343_NEEDLE", RevisionSelector: "HEAD",
	}, Options{}); !errors.Is(err, servicequery.ErrUnavailable) {
		t.Fatalf("invalid v2 error = %v", err)
	}
}

func TestServiceSearchReconcileValidatesBeforeActivation(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	makeFixtureUnavailable(t, fixture.store)
	outcome, err := servicequery.ReconcileGeneration(
		t.Context(), fixture.indexDir, fixture.store, fixture.store.repo.Name,
	)
	if err != nil || outcome.Activated != 1 ||
		fixture.store.state.Status != servicecatalog.StatusCurrent {
		t.Fatalf("reconcile outcome = %+v, %v; state=%+v", outcome, err, fixture.store.state)
	}

	broken := buildServiceSearchFixture(t)
	makeFixtureUnavailable(t, broken.store)
	source, err := repositoryindex.ReadSourceManifest(
		broken.indexDir, broken.store.repo.Name,
	)
	if err != nil || len(source.Members) == 0 {
		t.Fatalf("source manifest = %+v, %v", source, err)
	}
	memberPath := filepath.Join(broken.indexDir, source.Members[0].Name)
	member, err := os.ReadFile(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	member[0] ^= 1
	if err := os.WriteFile(memberPath, member, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := servicequery.ReconcileGeneration(
		t.Context(), broken.indexDir, broken.store, broken.store.repo.Name,
	); err == nil {
		t.Fatal("corrupt source generation activated a service")
	}
	if broken.store.state.Status != servicecatalog.StatusUnavailable ||
		broken.store.state.ActiveDesiredGeneration != "" {
		t.Fatalf("failed replacement changed prior state = %+v", broken.store.state)
	}
}

func TestServiceSearchRetiresReaderOnlyAfterActiveLeaseReleases(t *testing.T) {
	backend := &cacheTestStreamer{}
	repo := &wholeRepoCache{}
	entry := &wholeCacheEntry{
		searcher: backend, searchDigest: testServiceDigest("search"), refs: 1,
	}
	repo.entry = entry
	lease := &wholeLease{repo: repo, entry: entry, searcher: backend}
	repo.mu.Lock()
	retireWholeEntry(entry)
	repo.mu.Unlock()
	if !entry.retired || backend.closes.Load() != 0 {
		t.Fatalf("reader retired/closed with active lease = %t/%d", entry.retired, backend.closes.Load())
	}
	lease.release()
	if backend.closes.Load() != 1 {
		t.Fatalf("reader close count after release = %d", backend.closes.Load())
	}
}

func makeFixtureUnavailable(t *testing.T, st *serviceSearchStore) {
	t.Helper()
	st.state.ActiveDesiredGeneration = ""
	st.state.ActiveSourceGeneration = ""
	st.state.ActiveCatalogGeneration = ""
	st.state.ActiveSearchGeneration = ""
	st.state.Status = servicecatalog.StatusUnavailable
	st.state.ControlRevision++
	st.state.ChangedAt = time.Now().UTC()
	if err := servicecatalog.SetServiceStateDigest(&st.state); err != nil {
		t.Fatal(err)
	}
	st.summary.CurrentCount = 0
	st.summary.UnavailableCount = 1
	st.summary.ControlRevision++
	st.summary.UpdatedAt = time.Now().UTC()
	if err := servicecatalog.SetRepositoryStateDigest(&st.summary); err != nil {
		t.Fatal(err)
	}
}

type serviceSearchFixture struct {
	indexDir string
	store    *serviceSearchStore
	search   repositoryindex.SearchManifest
}

func buildServiceSearchFixture(t *testing.T) serviceSearchFixture {
	t.Helper()
	repository := "example.com/acme/t343-search"
	repositoryDir := t.TempDir()
	runServiceGit(t, repositoryDir, "init", "-q", "-b", "main")
	runServiceGit(t, repositoryDir, "config", "user.name", "T343 fixture")
	runServiceGit(t, repositoryDir, "config", "user.email", "fixture@example.invalid")
	for name, content := range map[string]string{
		"services/orders/main.go": "package orders\nconst Needle = \"T343_NEEDLE\"\n",
		"outside/main.go":         "package outside\nconst Needle = \"T343_NEEDLE\"\n",
	} {
		path := filepath.Join(repositoryDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runServiceGit(t, repositoryDir, "add", "--all")
	runServiceGit(t, repositoryDir, "commit", "-q", "-m", "fixture")
	commit := runServiceGit(t, repositoryDir, "rev-parse", "HEAD")
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	indexDir := t.TempDir()
	shardStage := t.TempDir()
	builder, err := index.NewBuilder(index.Options{
		IndexDir: shardStage, ShardPrefixOverride: "t343",
		ShardMax: 100 << 20, Parallelism: 1, DisableCTags: true,
		RepositoryDescription: zoekt.Repository{
			Name:     repository,
			Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: commit}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"outside/main.go", "services/orders/main.go"} {
		content, err := os.ReadFile(filepath.Join(repositoryDir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.Add(index.Document{
			Name: path, Content: content, Branches: []string{"HEAD"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	sourceStage := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDir, sourceStage, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.PublishWholeGeneration(
		t.Context(), indexDir, shardStage, sourceStage,
		repository, revisions, source,
	); err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	search, err := focusedindex.ValidateRepositorySearchGeneration(
		t.Context(), indexDir, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication := serviceSearchPublication(t, repository, commit)
	projection, found, err := publicationProjectService(publication, "orders")
	if err != nil || !found {
		t.Fatalf("project service = %+v, %t, %v", projection, found, err)
	}
	desired, err := servicecatalog.ServiceDesiredGeneration(projection, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := servicecatalog.ServiceState{
		Schema: servicecatalog.ServiceStateSchema, Repository: repository,
		ServiceKey: "orders", DisplayName: "Orders",
		Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		Successors: []string{}, Incarnation: 1,
		DesiredGeneration: desired, DesiredSourceGeneration: projection.SourceGeneration,
		DesiredCatalogGeneration: publication.GenerationDigest,
		ActiveDesiredGeneration:  desired, ActiveSourceGeneration: projection.SourceGeneration,
		ActiveCatalogGeneration: publication.GenerationDigest,
		ActiveSearchGeneration:  search.Digest,
		Status:                  servicecatalog.StatusCurrent, ControlRevision: 1, ChangedAt: now,
	}
	if err := servicecatalog.SetServiceStateDigest(&state); err != nil {
		t.Fatal(err)
	}
	summary := servicecatalog.RepositoryState{
		Schema: servicecatalog.RepositoryStateSchema, Repository: repository,
		CatalogGeneration: publication.GenerationDigest, CatalogControlRevision: 1,
		CatalogServiceCount: 1, LiveServiceCount: 1, CurrentCount: 1,
		ControlRevision: 1, UpdatedAt: now,
	}
	if err := servicecatalog.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	return serviceSearchFixture{
		indexDir: indexDir, search: search,
		store: &serviceSearchStore{
			repo: store.Repo{
				Name: repository, IndexedCommitHash: commit,
				IndexedRevisions: revisions,
			},
			publication: publication, summary: summary, state: state,
		},
	}
}

func serviceSearchPublication(
	t *testing.T,
	repository, commit string,
) servicecatalog.Publication {
	t.Helper()
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "t343", Version: "v1",
		},
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "services/orders",
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		}},
		Unowned: []servicecatalog.UnownedPlacement{{
			Path: "outside", Origin: servicecatalog.OriginBase,
		}},
	}
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind: servicecatalog.SourceOperator, SourcePath: "/t343/catalog.json",
		SourceCommit: commit, SourceCensusDigest: testServiceDigest("census"),
		SourceFileCount: 2, AcceptedFileCount: 1, UnownedFileCount: 1,
		Authority: catalog.Authority, CatalogDigest: catalogDigest, Canonical: canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	publication.ControlRevision = 1
	publication.PublishedAt = time.Now().UTC()
	if err := servicecatalog.ValidatePublication(publication, true); err != nil {
		t.Fatal(err)
	}
	return publication
}

func publicationProjectService(
	publication servicecatalog.Publication,
	serviceKey string,
) (servicecatalog.ServiceProjection, bool, error) {
	verified, err := servicecatalog.VerifyPublication(publication, true)
	if err != nil {
		return servicecatalog.ServiceProjection{}, false, err
	}
	return verified.ProjectService(serviceKey)
}

func runServiceGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func testServiceDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
