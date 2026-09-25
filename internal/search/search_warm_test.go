package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func warmSelectorStore(fixture serviceSearchFixture) *runtimeSelectorSearchStore {
	return &runtimeSelectorSearchStore{
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
}

// fixedSelectorStore returns one selector for every repository, so the warm's
// own repository and generation checks run instead of the fake's lookup.
type fixedSelectorStore struct {
	*runtimeSelectorSearchStore
	selector store.ServiceRuntimeSelector
}

func (fixed fixedSelectorStore) GetServiceRuntimeSelector(context.Context, string) (store.ServiceRuntimeSelector, error) {
	return fixed.selector, nil
}

func TestWarmWholeGenerationSharedAndSelected(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	repository := fixture.store.repo.Name
	searcher, err := Open(fixture.indexDir, warmSelectorStore(fixture))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(searcher.Close)
	controls, err := focusedindex.ReadSearchGenerationControls(t.Context(), fixture.indexDir, repository, fixture.search.Digest)
	if err != nil {
		t.Fatal(err)
	}

	observation, err := searcher.WarmSelectedWholeRepository(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Repository != repository || observation.SelectedSearchDigest != fixture.search.Digest ||
		observation.SelectedDirectory != controls.Directory || len(observation.SelectedRevisions) == 0 ||
		observation.SharedValidated == (observation.SharedExactDigest != "") {
		t.Fatalf("warm observation = %+v", observation)
	}

	// The selected exact reader is resident, bound to the selected generation.
	selected, err := searcher.whole.selectedRepo(repository)
	if err != nil {
		t.Fatal(err)
	}
	selected.mu.Lock()
	entry := selected.entry
	warm := entry != nil && entry.searcher != nil && !entry.retired &&
		entry.searchDigest == fixture.search.Digest && entry.directory == controls.Directory
	selected.mu.Unlock()
	if !warm {
		t.Fatalf("selected exact reader is not resident after warm: %+v", entry)
	}
	// The all-code path is warm too: a validated shared binding, or its exact reader.
	shared, err := searcher.whole.repo(repository)
	if err != nil {
		t.Fatal(err)
	}
	shared.mu.Lock()
	sharedWarm := observation.SharedValidated && shared.shared != nil && shared.shared.validated ||
		!observation.SharedValidated && shared.entry != nil && shared.entry.searcher != nil
	shared.mu.Unlock()
	if !sharedWarm {
		t.Fatalf("all-code reader is not warm after warm: validated=%v", observation.SharedValidated)
	}

	// A second warm reuses the resident entry instead of filling again.
	again, err := searcher.WarmSelectedWholeRepository(t.Context(), repository)
	if err != nil || again.SelectedSearchDigest != observation.SelectedSearchDigest {
		t.Fatalf("second warm = %+v, %v", again, err)
	}
	selected.mu.Lock()
	reused := selected.entry == entry
	selected.mu.Unlock()
	if !reused {
		t.Fatal("second warm replaced the resident selected reader")
	}

	scoped, err := NewRuntimeScopedSearcher(searcher, &store.ServiceStateV3Reader{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scoped.SearchScoped(t.Context(), ScopeSelector{
		Kind: ScopeService, Repository: repository, ServiceKey: "orders",
	}, "T343_NEEDLE", Options{MaxMatches: 10})
	if err != nil || len(result.Files) != 1 || result.Files[0].Path != "services/orders/main.go" {
		t.Fatalf("service search after warm = %+v, %v", result, err)
	}
}

func TestWarmWholeGenerationRefusals(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	repository := fixture.store.repo.Name
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name   string
		mutate func(*runtimeSelectorSearchStore)
		ctx    context.Context
		repo   string
		want   error
	}{
		{name: "empty repository", repo: "", want: ErrWholeWarmUnavailable},
		{name: "missing selector", repo: repository, mutate: func(fake *runtimeSelectorSearchStore) {
			fake.selectorErr = store.ErrNotFound
		}, want: store.ErrNotFound},
		{name: "unknown selected generation", repo: repository, mutate: func(fake *runtimeSelectorSearchStore) {
			fake.selector.SearchGenerationDigest = "sha256:" + strings.Repeat("c", 64)
		}},
		{name: "focused posture", repo: repository, mutate: func(fake *runtimeSelectorSearchStore) {
			repo := fake.repo
			repo.IndexedAnalysisUnit = &analysisunit.State{SearchIndexPosture: analysisunit.SearchIndexFocused}
			fake.serviceSearchStore = &serviceSearchStore{
				Store: fake.Store, repo: repo, publication: fake.publication,
				summary: fake.summary, state: fake.state,
			}
		}, want: ErrWholeWarmUnavailable},
		{name: "canceled", repo: repository, ctx: canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := warmSelectorStore(fixture)
			if test.mutate != nil {
				test.mutate(fake)
			}
			searcher, err := Open(fixture.indexDir, fake)
			if err != nil {
				t.Fatal(err)
			}
			defer searcher.Close()
			ctx := test.ctx
			if ctx == nil {
				ctx = t.Context()
			}
			observation, err := searcher.WarmSelectedWholeRepository(ctx, test.repo)
			if err == nil || test.want != nil && !errors.Is(err, test.want) || observation.SelectedSearchDigest != "" {
				t.Fatalf("warm = %+v, %v; want %v", observation, err, test.want)
			}
		})
	}

	for name, mutate := range map[string]func(*store.ServiceRuntimeSelector){
		"selector for another repository": func(selector *store.ServiceRuntimeSelector) {
			selector.Repository = "example.com/acme/other"
		},
		"selector without search generation": func(selector *store.ServiceRuntimeSelector) {
			selector.SearchGenerationDigest = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := warmSelectorStore(fixture)
			selector := fake.selector
			mutate(&selector)
			searcher, err := Open(fixture.indexDir, fixedSelectorStore{fake, selector})
			if err != nil {
				t.Fatal(err)
			}
			defer searcher.Close()
			if _, err := searcher.WarmSelectedWholeRepository(t.Context(), repository); !errors.Is(err, ErrWholeWarmUnavailable) {
				t.Fatalf("warm = %v; want %v", err, ErrWholeWarmUnavailable)
			}
		})
	}

	var nilSearcher *Searcher
	if _, err := nilSearcher.WarmSelectedWholeRepository(t.Context(), repository); !errors.Is(err, ErrWholeWarmUnavailable) {
		t.Fatalf("nil searcher warm = %v", err)
	}
	searcher, err := Open(fixture.indexDir, warmSelectorStore(fixture))
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()
	if _, err := searcher.warmWholeGeneration(t.Context(), repository, nil, "", fixture.search.Digest, nil); !errors.Is(err, ErrWholeWarmUnavailable) {
		t.Fatalf("incomplete binding warm = %v", err)
	}
}
