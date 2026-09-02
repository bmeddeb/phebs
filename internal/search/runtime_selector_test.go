package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

type runtimeSelectorSearchStore struct {
	*serviceSearchStore

	mu              sync.Mutex
	selector        store.ServiceRuntimeSelector
	selectorErr     error
	selectorReads   int
	selectorAtRead  int
	confirmSelector int
	failConfirmAt   int
	calls           []string
}

func (fake *runtimeSelectorSearchStore) record(call string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, call)
}

func (fake *runtimeSelectorSearchStore) GetRepo(
	ctx context.Context,
	repository string,
) (*store.Repo, error) {
	fake.record("get-repository")
	return fake.serviceSearchStore.GetRepo(ctx, repository)
}

func (fake *runtimeSelectorSearchStore) GetServiceRuntimeSelector(
	_ context.Context,
	repository string,
) (store.ServiceRuntimeSelector, error) {
	fake.record("get-selector")
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.selectorReads++
	if fake.selectorAtRead > 0 && fake.selectorReads >= fake.selectorAtRead {
		return fake.selector, nil
	}
	if fake.selectorErr != nil {
		return store.ServiceRuntimeSelector{}, fake.selectorErr
	}
	if repository != fake.selector.Repository {
		return store.ServiceRuntimeSelector{}, store.ErrNotFound
	}
	return fake.selector, nil
}

func (fake *runtimeSelectorSearchStore) ConfirmServiceRuntimeSelector(
	_ context.Context,
	expected store.ServiceRuntimeSelector,
) error {
	fake.record("confirm-selector")
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.confirmSelector++
	if fake.failConfirmAt == fake.confirmSelector || expected != fake.selector {
		return store.ErrConflict
	}
	return nil
}

func (fake *runtimeSelectorSearchStore) resetObserved() {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = nil
	fake.confirmSelector = 0
	fake.selectorReads = 0
	fake.selectorAtRead = 0
	fake.selectorErr = nil
	fake.failConfirmAt = 0
}

func (fake *runtimeSelectorSearchStore) observed() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return slices.Clone(fake.calls)
}

func TestRuntimeScopedSearchSelectsAfterAuthorizationAndFinalFences(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	fake := &runtimeSelectorSearchStore{
		serviceSearchStore: fixture.store,
		selector: runtimeSelectorForSearchTest(store.ServiceRuntimeSelector{
			Schema:     store.ServiceRuntimeSelectorSchema,
			Repository: fixture.store.repo.Name, Backend: store.ServiceRuntimeV2,
			CatalogGenerationDigest:      fixture.store.publication.GenerationDigest,
			CatalogControlRevision:       fixture.store.publication.ControlRevision,
			StateControlRevision:         fixture.store.summary.ControlRevision,
			StateSummaryDigest:           fixture.store.summary.SummaryDigest,
			SearchGenerationDigest:       fixture.search.Digest,
			RelationshipGenerationDigest: "sha256:" + strings.Repeat("a", 64),
			RelationshipRootDigest:       "sha256:" + strings.Repeat("b", 64),
			ControlRevision:              1,
			ChangedAt:                    time.Now().UTC(),
		}),
	}
	searcher, err := Open(fixture.indexDir, fake)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)
	searcher.Visible = func(context.Context) func(store.Repo) bool {
		fake.record("resolve-visibility")
		return func(store.Repo) bool {
			fake.record("check-visibility")
			return true
		}
	}
	scoped, err := NewRuntimeScopedSearcher(searcher, &store.ServiceStateV3Reader{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scoped.SearchScoped(t.Context(), ScopeSelector{
		Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
	}, "T343_NEEDLE", Options{MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "services/orders/main.go" {
		t.Fatalf("selected v2 result = %+v", result)
	}
	calls := fake.observed()
	selectorIndex := slices.Index(calls, "get-selector")
	visibilityIndex := slices.Index(calls, "check-visibility")
	if selectorIndex < 0 || visibilityIndex < 0 || selectorIndex <= visibilityIndex ||
		calls[len(calls)-1] != "confirm-selector" {
		t.Fatalf("selected search call order = %v", calls)
	}

	fake.resetObserved()
	if _, err := scoped.SearchScoped(
		t.Context(), ScopeSelector{Kind: ScopeAllCode}, "T343_NEEDLE",
		Options{MaxMatches: 10},
	); err != nil {
		t.Fatal(err)
	}
	if calls := fake.observed(); slices.Contains(calls, "get-selector") ||
		slices.Contains(calls, "confirm-selector") {
		t.Fatalf("all-code search consulted runtime selector: %v", calls)
	}
}

func TestRuntimeScopedSearchKeepsSelectedV2AfterFlatCurrentAdvances(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	selectedSearch := fixture.search.Digest
	fake := &runtimeSelectorSearchStore{
		serviceSearchStore: fixture.store,
		selector: runtimeSelectorForSearchTest(store.ServiceRuntimeSelector{
			Schema:     store.ServiceRuntimeSelectorSchema,
			Repository: fixture.store.repo.Name, Backend: store.ServiceRuntimeV2,
			CatalogGenerationDigest:      fixture.store.publication.GenerationDigest,
			CatalogControlRevision:       fixture.store.publication.ControlRevision,
			StateControlRevision:         fixture.store.summary.ControlRevision,
			StateSummaryDigest:           fixture.store.summary.SummaryDigest,
			SearchGenerationDigest:       selectedSearch,
			RelationshipGenerationDigest: "sha256:" + strings.Repeat("a", 64),
			RelationshipRootDigest:       "sha256:" + strings.Repeat("b", 64),
			ControlRevision:              1,
			ChangedAt:                    time.Now().UTC(),
		}),
	}
	searcher, err := Open(fixture.indexDir, fake)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)
	scoped, err := NewRuntimeScopedSearcher(searcher, &store.ServiceStateV3Reader{})
	if err != nil {
		t.Fatal(err)
	}
	replacement := advanceServiceSearchGeneration(t, fixture)
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
		result.Scope.Authority.RepositorySearchGeneration != selectedSearch {
		t.Fatalf("selected v2 result after flat advance = %+v", result)
	}
}

func TestRuntimeScopedSearchHandlesHiddenAbsentAndDriftedSelector(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	fake := &runtimeSelectorSearchStore{
		serviceSearchStore: fixture.store,
		selector: runtimeSelectorForSearchTest(store.ServiceRuntimeSelector{
			Schema:     store.ServiceRuntimeSelectorSchema,
			Repository: fixture.store.repo.Name, Backend: store.ServiceRuntimeV2,
			CatalogGenerationDigest:      fixture.store.publication.GenerationDigest,
			CatalogControlRevision:       fixture.store.publication.ControlRevision,
			StateControlRevision:         fixture.store.summary.ControlRevision,
			StateSummaryDigest:           fixture.store.summary.SummaryDigest,
			SearchGenerationDigest:       fixture.search.Digest,
			RelationshipGenerationDigest: "sha256:" + strings.Repeat("a", 64),
			RelationshipRootDigest:       "sha256:" + strings.Repeat("b", 64),
			ControlRevision:              1,
			ChangedAt:                    time.Now().UTC(),
		}),
	}
	searcher, err := Open(fixture.indexDir, fake)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)
	scoped, err := NewRuntimeScopedSearcher(searcher, &store.ServiceStateV3Reader{})
	if err != nil {
		t.Fatal(err)
	}
	selector := ScopeSelector{
		Kind: ScopeService, Repository: fixture.store.repo.Name, ServiceKey: "orders",
	}

	searcher.Visible = func(context.Context) func(store.Repo) bool {
		return func(store.Repo) bool { return false }
	}
	if result, err := scoped.SearchScoped(
		t.Context(), selector, "T343_NEEDLE", Options{MaxMatches: 10},
	); result != nil || !errors.Is(err, ErrScopeNotFound) {
		t.Fatalf("hidden selected search = %+v, %v", result, err)
	}
	if calls := fake.observed(); slices.Contains(calls, "get-selector") {
		t.Fatalf("hidden search consulted runtime selector: %v", calls)
	}

	searcher.Visible = nil
	fake.resetObserved()
	fake.selectorErr = store.ErrNotFound
	if result, err := scoped.SearchScoped(
		t.Context(), selector, "T343_NEEDLE", Options{MaxMatches: 10},
	); err != nil || result == nil || len(result.Files) != 1 {
		t.Fatalf("implicit v2 runtime = %+v, %v", result, err)
	}
	if calls := fake.observed(); !slices.Contains(calls, "get-selector") ||
		slices.Contains(calls, "confirm-selector") {
		t.Fatalf("implicit v2 selector calls = %v", calls)
	}

	fake.resetObserved()
	fake.selectorErr = store.ErrNotFound
	fake.selectorAtRead = 2
	if result, err := scoped.SearchScoped(
		t.Context(), selector, "T343_NEEDLE", Options{MaxMatches: 10},
	); result != nil || !errors.Is(err, servicequery.ErrUnavailable) {
		t.Fatalf("implicit v2 selector activation = %+v, %v", result, err)
	}
	if calls := fake.observed(); len(calls) == 0 || calls[len(calls)-1] != "get-selector" {
		t.Fatalf("implicit v2 selector activation calls = %v", calls)
	}

	fake.resetObserved()
	fake.selectorErr = store.ErrConflict
	if result, err := scoped.SearchScoped(
		t.Context(), selector, "T343_NEEDLE", Options{MaxMatches: 10},
	); result != nil || !errors.Is(err, servicequery.ErrUnavailable) {
		t.Fatalf("conflicted runtime selector = %+v, %v", result, err)
	}
	if calls := fake.observed(); !slices.Contains(calls, "get-selector") ||
		slices.Contains(calls, "confirm-selector") {
		t.Fatalf("conflicted selector fallback calls = %v", calls)
	}

	fake.resetObserved()
	fake.failConfirmAt = 2
	if result, err := scoped.SearchScoped(
		t.Context(), selector, "T343_NEEDLE", Options{MaxMatches: 10},
	); result != nil || !errors.Is(err, servicequery.ErrUnavailable) {
		t.Fatalf("drifted selected runtime = %+v, %v", result, err)
	}
	if calls := fake.observed(); len(calls) == 0 || calls[len(calls)-1] != "confirm-selector" {
		t.Fatalf("drifted selector final calls = %v", calls)
	}
}

func runtimeSelectorForSearchTest(
	selector store.ServiceRuntimeSelector,
) store.ServiceRuntimeSelector {
	payload := struct {
		Schema                       string `json:"schema"`
		Repository                   string `json:"repository"`
		Backend                      string `json:"backend"`
		CatalogGenerationDigest      string `json:"catalog_generation_digest"`
		CatalogRootDigest            string `json:"catalog_root_digest"`
		CatalogControlRevision       uint64 `json:"catalog_control_revision"`
		StateControlRevision         uint64 `json:"state_control_revision"`
		StateSummaryDigest           string `json:"state_summary_digest"`
		SearchGenerationDigest       string `json:"search_generation_digest"`
		RelationshipGenerationDigest string `json:"relationship_generation_digest"`
		RelationshipRootDigest       string `json:"relationship_root_digest"`
		ControlRevision              uint64 `json:"control_revision"`
	}{
		Schema: selector.Schema, Repository: selector.Repository, Backend: selector.Backend,
		CatalogGenerationDigest:      selector.CatalogGenerationDigest,
		CatalogRootDigest:            selector.CatalogRootDigest,
		CatalogControlRevision:       selector.CatalogControlRevision,
		StateControlRevision:         selector.StateControlRevision,
		StateSummaryDigest:           selector.StateSummaryDigest,
		SearchGenerationDigest:       selector.SearchGenerationDigest,
		RelationshipGenerationDigest: selector.RelationshipGenerationDigest,
		RelationshipRootDigest:       selector.RelationshipRootDigest,
		ControlRevision:              selector.ControlRevision,
	}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	selector.Digest = "sha256:" + hex.EncodeToString(digest[:])
	return selector
}
