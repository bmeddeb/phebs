package search

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

type serviceV3SearchSource struct {
	*serviceV3SelectorAuthority

	mu sync.Mutex

	pointer store.ServiceCatalogV3Pointer
	roots   map[string]servicecatalogv3.Root
	members map[string][]byte
	summary servicecatalog.RepositoryState
	state   servicecatalog.ServiceState

	calls              []string
	confirmCalls       int
	publishOnConfirmAt int
}

func (source *serviceV3SearchSource) record(call string) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls = append(source.calls, call)
}

func (source *serviceV3SearchSource) GetServiceCatalogV3CandidatePointer(
	_ context.Context,
	repository string,
) (store.ServiceCatalogV3Pointer, error) {
	source.record("catalog-pointer")
	source.mu.Lock()
	defer source.mu.Unlock()
	if repository != source.pointer.Repository {
		return store.ServiceCatalogV3Pointer{}, store.ErrNotFound
	}
	return source.pointer, nil
}

func (source *serviceV3SearchSource) ReadServiceCatalogV3Root(
	_ context.Context,
	repository, digest string,
) (servicecatalogv3.Root, error) {
	source.record("catalog-root")
	source.mu.Lock()
	defer source.mu.Unlock()
	root, ok := source.roots[digest]
	if !ok || root.Binding.Repository != repository {
		return servicecatalogv3.Root{}, store.ErrNotFound
	}
	return root, nil
}

func (source *serviceV3SearchSource) ReadServiceCatalogV3Member(
	_ context.Context,
	descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	source.record("catalog-member")
	source.mu.Lock()
	defer source.mu.Unlock()
	raw := source.members[descriptor.Digest]
	if raw == nil {
		return nil, store.ErrNotFound
	}
	return slices.Clone(raw), nil
}

func (source *serviceV3SearchSource) GetServiceStateV3SummaryPoint(
	_ context.Context,
	repository string,
) (servicecatalog.RepositoryState, error) {
	source.record("state-summary")
	source.mu.Lock()
	defer source.mu.Unlock()
	if repository != source.summary.Repository {
		return servicecatalog.RepositoryState{}, store.ErrNotFound
	}
	return source.summary, nil
}

func (source *serviceV3SearchSource) GetServiceStateV3Point(
	_ context.Context,
	repository, serviceKey string,
) (servicecatalog.ServiceState, error) {
	source.record("service-state")
	source.mu.Lock()
	defer source.mu.Unlock()
	if repository != source.state.Repository || serviceKey != source.state.ServiceKey {
		return servicecatalog.ServiceState{}, store.ErrNotFound
	}
	state := source.state
	state.Successors = slices.Clone(source.state.Successors)
	return state, nil
}

func (source *serviceV3SearchSource) ListServiceStateV3Rows(
	context.Context,
	string,
	string,
	int,
) ([]servicecatalog.ServiceState, error) {
	source.record("list-service-state")
	return nil, nil
}

func (source *serviceV3SearchSource) ListAcceptedServiceStateV3Rows(
	context.Context,
	string,
	int,
) ([]servicecatalog.ServiceState, error) {
	source.record("list-accepted-state")
	return nil, nil
}

func (source *serviceV3SearchSource) ConfirmServiceStateV3Snapshot(
	_ context.Context,
	pointer store.ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) error {
	source.record("confirm")
	source.mu.Lock()
	defer source.mu.Unlock()
	source.confirmCalls++
	if source.publishOnConfirmAt == source.confirmCalls {
		source.pointer.ControlRevision++
		return store.ErrConflict
	}
	if pointer.Repository != source.pointer.Repository ||
		pointer.RootDigest != source.pointer.RootDigest ||
		pointer.ControlRevision != source.pointer.ControlRevision ||
		summary.SummaryDigest != source.summary.SummaryDigest ||
		summary.ControlRevision != source.summary.ControlRevision {
		return store.ErrConflict
	}
	return nil
}

func (source *serviceV3SearchSource) resetCalls() {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls = nil
	source.confirmCalls = 0
}

func (source *serviceV3SearchSource) observedCalls() []string {
	source.mu.Lock()
	defer source.mu.Unlock()
	return slices.Clone(source.calls)
}

func (source *serviceV3SearchSource) confirmations() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.confirmCalls
}

type orderedServiceV3SearchStore struct {
	*serviceSearchStore
	*serviceV3SelectorAuthority
	mu    sync.Mutex
	calls []string
}

type serviceV3SelectorAuthority struct {
	mu            sync.Mutex
	selector      store.ServiceRuntimeSelector
	confirmations int
	failConfirmAt int
}

func (authority *serviceV3SelectorAuthority) GetServiceRuntimeSelector(
	_ context.Context,
	repository string,
) (store.ServiceRuntimeSelector, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if repository != authority.selector.Repository {
		return store.ServiceRuntimeSelector{}, store.ErrNotFound
	}
	return authority.selector, nil
}

func (authority *serviceV3SelectorAuthority) ConfirmServiceRuntimeSelector(
	_ context.Context,
	expected store.ServiceRuntimeSelector,
) error {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.confirmations++
	if authority.failConfirmAt == authority.confirmations ||
		expected != authority.selector {
		return store.ErrConflict
	}
	return nil
}

func (authority *serviceV3SelectorAuthority) reset(failConfirmAt int) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.confirmations = 0
	authority.failConfirmAt = failConfirmAt
}

func (authority *serviceV3SelectorAuthority) confirmationCount() int {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.confirmations
}

func (st *orderedServiceV3SearchStore) GetRepo(
	ctx context.Context,
	repository string,
) (*store.Repo, error) {
	st.record("get-repository")
	return st.serviceSearchStore.GetRepo(ctx, repository)
}

func (st *orderedServiceV3SearchStore) record(call string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.calls = append(st.calls, call)
}

func (st *orderedServiceV3SearchStore) resetCalls() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.calls = nil
}

func (st *orderedServiceV3SearchStore) observedCalls() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.Clone(st.calls)
}

type serviceV3SearchFixture struct {
	base       serviceSearchFixture
	searcher   *Searcher
	reader     *store.ServiceStateV3Reader
	source     *serviceV3SearchSource
	store      *orderedServiceV3SearchStore
	binding    servicecatalogv3.Binding
	catalog    servicecatalog.Catalog
	generation servicecatalogv3.Generation
	projection servicecatalog.ServiceProjection
}

func TestServiceSearchV3AuthorizesBeforeAnySegmentedRead(t *testing.T) {
	fixture := buildServiceV3SearchFixture(t)
	fixture.source.resetCalls()
	fixture.store.resetCalls()
	fixture.searcher.Visible = func(context.Context) func(store.Repo) bool {
		fixture.store.record("resolve-visibility")
		return func(store.Repo) bool { return false }
	}

	result, err := fixture.searcher.SearchScopedV3(
		t.Context(), fixture.reader, ScopeSelector{
			Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
		}, "T343_NEEDLE", Options{MaxMatches: 10},
	)
	if result != nil || !errors.Is(err, ErrScopeNotFound) ||
		!errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden v3 service result = %+v, %v", result, err)
	}
	if calls := fixture.source.observedCalls(); len(calls) != 0 {
		t.Fatalf("hidden service performed v3 reads: %v", calls)
	}
	if calls := fixture.store.observedCalls(); !slices.Equal(
		calls, []string{"resolve-visibility", "get-repository"},
	) {
		t.Fatalf("hidden service authorization order = %v", calls)
	}

	fixture.store.resetCalls()
	result, err = fixture.searcher.SearchScopedV3(
		t.Context(), nil, ScopeSelector{
			Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
		}, "T343_NEEDLE", Options{MaxMatches: 10},
	)
	if result != nil || !errors.Is(err, ErrScopeNotFound) ||
		!errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden service with unavailable v3 backend = %+v, %v", result, err)
	}
	if calls := fixture.store.observedCalls(); !slices.Equal(
		calls, []string{"resolve-visibility", "get-repository"},
	) {
		t.Fatalf("unavailable backend authorization order = %v", calls)
	}
	if calls := fixture.source.observedCalls(); len(calls) != 0 {
		t.Fatalf("unavailable hidden service performed v3 reads: %v", calls)
	}
}

func TestServiceSearchV3FinalAuthorizationAndPublicationFences(t *testing.T) {
	t.Run("visibility revocation", func(t *testing.T) {
		fixture := buildServiceV3SearchFixture(t)
		resolutions := 0
		fixture.searcher.Visible = func(context.Context) func(store.Repo) bool {
			resolutions++
			allowed := resolutions == 1
			return func(store.Repo) bool { return allowed }
		}
		result, err := fixture.searcher.SearchScopedV3(
			t.Context(), fixture.reader, ScopeSelector{
				Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
			}, "T343_NEEDLE", Options{MaxMatches: 10},
		)
		if result != nil || !errors.Is(err, ErrScopeNotFound) ||
			!errors.Is(err, store.ErrNotFound) || resolutions != 2 {
			t.Fatalf(
				"revoked v3 service result = %+v, %v; visibility resolutions=%d",
				result, err, resolutions,
			)
		}
		if confirmations := fixture.source.confirmations(); confirmations != 2 {
			t.Fatalf("revoked v3 service confirmations = %d, want 2 before final auth", confirmations)
		}
	})

	t.Run("concurrent catalog publication", func(t *testing.T) {
		fixture := buildServiceV3SearchFixture(t)
		fixture.source.mu.Lock()
		fixture.source.publishOnConfirmAt = 3
		fixture.source.mu.Unlock()
		result, err := fixture.searcher.SearchScopedV3(
			t.Context(), fixture.reader, ScopeSelector{
				Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
			}, "T343_NEEDLE", Options{MaxMatches: 10},
		)
		if result != nil || !errors.Is(err, servicequery.ErrUnavailable) ||
			!strings.Contains(err.Error(), "final fence") {
			t.Fatalf("concurrent v3 publication result = %+v, %v", result, err)
		}
		if confirmations := fixture.source.confirmations(); confirmations != 3 {
			t.Fatalf("concurrent publication confirmations = %d, want 3", confirmations)
		}
	})
}

func TestServiceSearchV3StreamMatchesScopedReceipt(t *testing.T) {
	fixture := buildServiceV3SearchFixture(t)
	scoped, err := NewV3ScopedSearcher(fixture.searcher, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	selector := ScopeSelector{
		Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
	}
	direct, err := scoped.SearchScoped(
		t.Context(), selector, "T343_NEEDLE", Options{MaxMatches: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	var batches []*Result
	stats, receipt, err := scoped.StreamScoped(
		t.Context(), selector, "T343_NEEDLE", Options{MaxMatches: 10},
		func(result *Result) { batches = append(batches, result) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if direct.Scope == nil || receipt == nil || stats == nil ||
		receipt.Digest != direct.Scope.Digest ||
		stats.MatchCount != direct.Stats.MatchCount ||
		stats.FileCount != direct.Stats.FileCount ||
		len(batches) != 1 || len(batches[0].Files) != 1 ||
		batches[0].Files[0].Path != direct.Files[0].Path ||
		batches[0].Scope == nil || batches[0].Scope.Digest != receipt.Digest {
		t.Fatalf(
			"v3 stream parity = direct %+v; stats %+v receipt %+v batches %+v",
			direct, stats, receipt, batches,
		)
	}
}

func TestRuntimeScopedSearchKeepsSelectedV3AcrossDarkCandidateAdvance(t *testing.T) {
	fixture := buildServiceV3SearchFixture(t)
	scoped, err := NewRuntimeScopedSearcher(fixture.searcher, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture.source.mu.Lock()
	fixture.source.pointer.RootDigest = "sha256:" + strings.Repeat("f", 64)
	fixture.source.pointer.ControlRevision++
	fixture.source.mu.Unlock()
	fixture.source.resetCalls()
	fixture.store.reset(0)

	result, err := scoped.SearchScoped(t.Context(), ScopeSelector{
		Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
	}, "T343_NEEDLE", Options{MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Scope == nil || result.Scope.Authority == nil ||
		result.Scope.Authority.CurrentCatalogGeneration != fixture.generation.Root.Digest {
		t.Fatalf("selected v3 result after dark advance = %+v", result)
	}
	if calls := fixture.source.observedCalls(); slices.Contains(calls, "catalog-pointer") {
		t.Fatalf("selected v3 search followed dark candidate pointer: %v", calls)
	}
	if confirmations := fixture.store.confirmationCount(); confirmations != 3 {
		t.Fatalf("selected v3 selector confirmations = %d, want 3", confirmations)
	}
}

func TestRuntimeScopedSearchKeepsSelectedV3AfterFlatCurrentAdvances(t *testing.T) {
	fixture := buildServiceV3SearchFixture(t)
	selectedSearch := fixture.base.search.Digest
	scoped, err := NewRuntimeScopedSearcher(fixture.searcher, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	replacement := advanceServiceSearchGeneration(t, fixture.base)
	if replacement.Digest == selectedSearch {
		t.Fatal("replacement did not advance the flat search generation")
	}

	result, err := scoped.SearchScoped(t.Context(), ScopeSelector{
		Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
	}, "T343_NEEDLE", Options{MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "services/orders/main.go" ||
		result.Scope == nil || result.Scope.Authority == nil ||
		result.Scope.Authority.RepositorySearchGeneration != selectedSearch ||
		result.Scope.Authority.CurrentCatalogGeneration != fixture.generation.Root.Digest {
		t.Fatalf("selected v3 result after flat advance = %+v", result)
	}
}

func TestRuntimeScopedSearchV3RefusesFinalSelectorDrift(t *testing.T) {
	fixture := buildServiceV3SearchFixture(t)
	scoped, err := NewRuntimeScopedSearcher(fixture.searcher, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture.store.reset(3)
	result, err := scoped.SearchScoped(t.Context(), ScopeSelector{
		Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
	}, "T343_NEEDLE", Options{MaxMatches: 10})
	if result != nil || !errors.Is(err, servicequery.ErrUnavailable) ||
		!strings.Contains(err.Error(), "final fence") {
		t.Fatalf("drifted selected v3 result = %+v, %v", result, err)
	}
	if confirmations := fixture.store.confirmationCount(); confirmations != 3 {
		t.Fatalf("drifted selected v3 confirmations = %d, want 3", confirmations)
	}
}

func TestServiceSearchV3MapsLifecyclePosturesWithoutFallback(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		fixture := buildServiceV3SearchFixture(t)
		setServiceV3SearchStale(t, &fixture)
		result, err := fixture.searcher.SearchScopedV3(
			t.Context(), fixture.reader, ScopeSelector{
				Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
			}, "T343_NEEDLE", Options{MaxMatches: 10},
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Scope == nil || result.Scope.Authority == nil ||
			result.Scope.ServiceStatus != servicecatalog.StatusStale ||
			result.Scope.Authority.Status != servicecatalog.StatusStale ||
			result.Scope.Authority.CurrentCatalogGeneration != fixture.source.pointer.RootDigest ||
			result.Scope.Authority.ActiveCatalogGeneration != fixture.generation.Root.Digest ||
			len(result.Files) != 1 || result.Files[0].Path != "services/orders/main.go" {
			t.Fatalf("stale v3 scope result = %+v", result)
		}
	})

	t.Run("partial publication", func(t *testing.T) {
		fixture := buildServiceV3SearchFixture(t)
		fixture.source.mu.Lock()
		fixture.source.summary.CatalogControlRevision++
		fixture.source.summary.ControlRevision++
		if err := servicecatalogv3.SetRepositoryStateDigest(&fixture.source.summary); err != nil {
			fixture.source.mu.Unlock()
			t.Fatal(err)
		}
		fixture.source.mu.Unlock()
		result, err := fixture.searcher.SearchScopedV3(
			t.Context(), fixture.reader, ScopeSelector{
				Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
			}, "T343_NEEDLE", Options{MaxMatches: 10},
		)
		if result != nil || !errors.Is(err, servicequery.ErrUnavailable) ||
			!strings.Contains(err.Error(), "unreconciled summary") {
			t.Fatalf("partial v3 publication result = %+v, %v", result, err)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		fixture := buildServiceV3SearchFixture(t)
		setServiceV3SearchConflict(t, &fixture)
		result, err := fixture.searcher.SearchScopedV3(
			t.Context(), fixture.reader, ScopeSelector{
				Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
			}, "T343_NEEDLE", Options{MaxMatches: 10},
		)
		if result != nil || !errors.Is(err, servicequery.ErrUnavailable) ||
			!strings.Contains(err.Error(), "no accepted desired projection") {
			t.Fatalf("conflict v3 service result = %+v, %v", result, err)
		}
	})
}

func buildServiceV3SearchFixture(t *testing.T) serviceV3SearchFixture {
	t.Helper()
	base := buildServiceSearchFixture(t)
	catalog, err := servicecatalog.Decode(base.store.publication.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	binding := servicecatalogv3.Binding{
		Repository: base.store.repo.Name,
		Source: servicecatalogv3.Source{
			Kind: base.store.publication.SourceKind, Path: base.store.publication.SourcePath,
			Commit:            base.store.publication.SourceCommit,
			CensusDigest:      base.store.publication.SourceCensusDigest,
			LegacyDigest:      base.store.publication.LegacyAnalysisUnitDigest,
			FileCount:         base.store.publication.SourceFileCount,
			AcceptedFileCount: base.store.publication.AcceptedFileCount,
			UnownedFileCount:  base.store.publication.UnownedFileCount,
		},
		Authority: base.store.publication.Authority,
		Override:  base.store.publication.Override,
	}
	generation, members, projection := buildServiceV3Generation(t, binding, catalog)
	state := serviceV3State(
		t, projection, &projection, servicecatalog.StatusCurrent, base.search.Digest,
	)
	summary := serviceV3Summary(
		t, generation.Root, 1, servicecatalog.StatusCurrent,
	)
	pointer := store.ServiceCatalogV3Pointer{
		Repository: base.store.repo.Name, RootDigest: generation.Root.Digest,
		ControlRevision: 1, PublishedAt: time.Now().UTC(),
	}
	selectorAuthority := &serviceV3SelectorAuthority{
		selector: runtimeSelectorForSearchTest(store.ServiceRuntimeSelector{
			Schema:     store.ServiceRuntimeSelectorSchema,
			Repository: base.store.repo.Name, Backend: store.ServiceRuntimeV3,
			CatalogRootDigest:            generation.Root.Digest,
			CatalogControlRevision:       pointer.ControlRevision,
			StateControlRevision:         summary.ControlRevision,
			StateSummaryDigest:           summary.SummaryDigest,
			SearchGenerationDigest:       base.search.Digest,
			RelationshipGenerationDigest: "sha256:" + strings.Repeat("a", 64),
			RelationshipRootDigest:       "sha256:" + strings.Repeat("b", 64),
			ControlRevision:              1,
			ChangedAt:                    pointer.PublishedAt,
		}),
	}
	source := &serviceV3SearchSource{
		serviceV3SelectorAuthority: selectorAuthority,
		pointer:                    pointer,
		roots:                      map[string]servicecatalogv3.Root{generation.Root.Digest: generation.Root},
		members:                    members, summary: summary, state: state,
	}
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := store.NewServiceStateV3Reader(source, cache)
	if err != nil {
		t.Fatal(err)
	}
	orderedStore := &orderedServiceV3SearchStore{
		serviceSearchStore:         base.store,
		serviceV3SelectorAuthority: selectorAuthority,
	}
	searcher, err := Open(base.indexDir, orderedStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)
	orderedStore.resetCalls()
	return serviceV3SearchFixture{
		base: base, searcher: searcher, reader: reader, source: source,
		store: orderedStore, binding: binding, catalog: catalog,
		generation: generation, projection: projection,
	}
}

func buildServiceV3Generation(
	t *testing.T,
	binding servicecatalogv3.Binding,
	catalog servicecatalog.Catalog,
) (
	servicecatalogv3.Generation,
	map[string][]byte,
	servicecatalog.ServiceProjection,
) {
	t.Helper()
	generation, err := servicecatalogv3.Build(binding, catalog)
	if err != nil {
		t.Fatal(err)
	}
	members := make(map[string][]byte, len(generation.Members))
	var projection servicecatalog.ServiceProjection
	for _, encoded := range generation.Members {
		descriptors := generation.Root.ServiceMembers
		if encoded.Kind == "placement" {
			descriptors = generation.Root.PlacementMembers
		}
		for _, descriptor := range descriptors {
			if descriptor.Kind != encoded.Kind || descriptor.Ordinal != encoded.Ordinal {
				continue
			}
			members[descriptor.Digest] = slices.Clone(encoded.Content)
			if encoded.Kind != "service" {
				break
			}
			projections, projectErr := servicecatalogv3.ProjectServiceMember(
				generation.Root, descriptor, encoded.Content,
			)
			if projectErr != nil {
				t.Fatal(projectErr)
			}
			for _, candidate := range projections {
				if candidate.Service.Key == "orders" {
					projection = candidate
				}
			}
			break
		}
	}
	if projection.Service.Key != "orders" {
		t.Fatal("v3 generation omitted orders projection")
	}
	return generation, members, projection
}

func serviceV3State(
	t *testing.T,
	desired servicecatalog.ServiceProjection,
	active *servicecatalog.ServiceProjection,
	status, searchGeneration string,
) servicecatalog.ServiceState {
	t.Helper()
	desiredGeneration, err := servicecatalogv3.ServiceDesiredGeneration(desired, 1)
	if err != nil {
		t.Fatal(err)
	}
	state := servicecatalog.ServiceState{
		Schema: servicecatalogv3.ServiceStateSchema, Repository: desired.Repository,
		ServiceKey: desired.Service.Key, DisplayName: desired.Service.DisplayName,
		Disposition: desired.Service.Disposition, Origin: desired.Service.Origin,
		Reason: desired.Service.Reason, Successors: slices.Clone(desired.Service.Successors),
		Incarnation: 1, DesiredGeneration: desiredGeneration,
		DesiredSourceGeneration:  desired.SourceGeneration,
		DesiredCatalogGeneration: desired.CatalogGeneration,
		Status:                   status, Removed: desired.Removed,
		ControlRevision: 1, ChangedAt: time.Now().UTC(),
	}
	if active != nil {
		activeGeneration, activeErr := servicecatalogv3.ServiceDesiredGeneration(*active, 1)
		if activeErr != nil {
			t.Fatal(activeErr)
		}
		state.ActiveDesiredGeneration = activeGeneration
		state.ActiveSourceGeneration = active.SourceGeneration
		state.ActiveCatalogGeneration = active.CatalogGeneration
		state.ActiveSearchGeneration = searchGeneration
	}
	if err := servicecatalogv3.SetServiceStateDigest(&state); err != nil {
		t.Fatal(err)
	}
	if err := servicecatalogv3.ValidateServiceState(state, true); err != nil {
		t.Fatal(err)
	}
	return state
}

func serviceV3Summary(
	t *testing.T,
	root servicecatalogv3.Root,
	controlRevision uint64,
	status string,
) servicecatalog.RepositoryState {
	t.Helper()
	summary := servicecatalog.RepositoryState{
		Schema:     servicecatalogv3.RepositoryStateSchema,
		Repository: root.Binding.Repository, CatalogGeneration: root.Digest,
		CatalogControlRevision: controlRevision, CatalogServiceCount: root.Services,
		LiveServiceCount: root.Services, ControlRevision: 1, UpdatedAt: time.Now().UTC(),
	}
	switch status {
	case servicecatalog.StatusCurrent:
		summary.CurrentCount = 1
	case servicecatalog.StatusStale:
		summary.StaleCount = 1
	case servicecatalog.StatusConflict:
		summary.ConflictCount = 1
	}
	if err := servicecatalogv3.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	if err := servicecatalogv3.ValidateRepositoryState(summary, true); err != nil {
		t.Fatal(err)
	}
	return summary
}

func setServiceV3SearchStale(t *testing.T, fixture *serviceV3SearchFixture) {
	t.Helper()
	catalog := fixture.catalog
	catalog.Services = slices.Clone(catalog.Services)
	catalog.Services[0].DisplayName = "Orders next"
	current, members, desired := buildServiceV3Generation(t, fixture.binding, catalog)
	fixture.source.mu.Lock()
	fixture.source.roots[current.Root.Digest] = current.Root
	for digest, raw := range members {
		fixture.source.members[digest] = raw
	}
	fixture.source.pointer.RootDigest = current.Root.Digest
	fixture.source.pointer.ControlRevision++
	fixture.source.state = serviceV3State(
		t, desired, &fixture.projection, servicecatalog.StatusStale, fixture.base.search.Digest,
	)
	fixture.source.summary = serviceV3Summary(
		t, current.Root, fixture.source.pointer.ControlRevision, servicecatalog.StatusStale,
	)
	fixture.source.mu.Unlock()
}

func setServiceV3SearchConflict(t *testing.T, fixture *serviceV3SearchFixture) {
	t.Helper()
	catalog := fixture.catalog
	catalog.Services = slices.Clone(catalog.Services)
	catalog.Services[0].Disposition = servicecatalog.DispositionConflict
	catalog.Services[0].Reason = "ambiguous authority"
	current, members, desired := buildServiceV3Generation(t, fixture.binding, catalog)
	fixture.source.mu.Lock()
	fixture.source.roots[current.Root.Digest] = current.Root
	for digest, raw := range members {
		fixture.source.members[digest] = raw
	}
	fixture.source.pointer.RootDigest = current.Root.Digest
	fixture.source.pointer.ControlRevision++
	fixture.source.state = serviceV3State(
		t, desired, &fixture.projection, servicecatalog.StatusConflict, fixture.base.search.Digest,
	)
	fixture.source.summary = serviceV3Summary(
		t, current.Root, fixture.source.pointer.ControlRevision, servicecatalog.StatusConflict,
	)
	fixture.source.mu.Unlock()
}
