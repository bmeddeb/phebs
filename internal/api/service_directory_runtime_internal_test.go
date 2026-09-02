package api

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

type runtimeSelectorDirectoryStore struct {
	*serviceDirectoryTestStore

	selector        store.ServiceRuntimeSelector
	selectorErr     error
	selectorReads   int
	selectorAtRead  int
	confirmSelector int
	failConfirmAt   int
}

func (fake *runtimeSelectorDirectoryStore) GetServiceRuntimeSelector(
	_ context.Context,
	repository string,
) (store.ServiceRuntimeSelector, error) {
	fake.calls = append(fake.calls, "get-selector")
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

func (fake *runtimeSelectorDirectoryStore) ConfirmServiceRuntimeSelector(
	_ context.Context,
	expected store.ServiceRuntimeSelector,
) error {
	fake.calls = append(fake.calls, "confirm-selector")
	fake.confirmSelector++
	if fake.failConfirmAt == fake.confirmSelector || expected != fake.selector {
		return store.ErrConflict
	}
	return nil
}

func TestRuntimeServiceDirectorySelectsAfterAuthorizationAndFinalFences(t *testing.T) {
	base := newServiceDirectoryTestStore()
	fake := &runtimeSelectorDirectoryStore{
		serviceDirectoryTestStore: base,
		selector: store.ServiceRuntimeSelector{
			Schema:     store.ServiceRuntimeSelectorSchema,
			Repository: base.publication.Repository, Backend: store.ServiceRuntimeV2,
			CatalogGenerationDigest: base.publication.GenerationDigest,
			CatalogControlRevision:  base.publication.ControlRevision,
			StateControlRevision:    base.summary.ControlRevision,
			StateSummaryDigest:      base.summary.SummaryDigest,
			ControlRevision:         7,
		},
	}
	service := NewRuntimeServiceDirectoryService(Options{
		Store: fake,
		Visible: func(context.Context) func(store.Repo) bool {
			fake.calls = append(fake.calls, "resolve-visibility")
			return func(repository store.Repo) bool {
				fake.calls = append(fake.calls, "check-visibility:"+repository.Name)
				return true
			}
		},
	}, &store.ServiceStateV3Reader{})
	if service == nil {
		t.Fatal("runtime service directory constructor returned nil")
	}
	inventory, err := service.List(t.Context(), ServiceInventoryQuery{
		Repository: base.publication.Repository,
	}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Services) != 1 || inventory.Services[0].Key != "orders" ||
		inventory.Pagination.NextCursor == "" {
		t.Fatalf("selected inventory = %+v", inventory)
	}
	selectorIndex := slices.Index(fake.calls, "get-selector")
	visibilityIndex := slices.Index(
		fake.calls, "check-visibility:"+base.publication.Repository,
	)
	if selectorIndex < 0 || visibilityIndex < 0 || selectorIndex <= visibilityIndex ||
		fake.calls[len(fake.calls)-1] != "confirm-selector" {
		t.Fatalf("selected directory call order = %v", fake.calls)
	}

	fake.calls = nil
	fake.confirmSelector = 0
	detail, err := service.Detail(
		t.Context(), base.publication.Repository, "orders",
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Service.Key != "orders" || fake.calls[len(fake.calls)-1] != "confirm-selector" {
		t.Fatalf("selected detail = %+v; calls = %v", detail, fake.calls)
	}

	fake.selector.ControlRevision++
	fake.calls = nil
	if result, err := service.List(t.Context(), ServiceInventoryQuery{
		Repository: base.publication.Repository,
	}, 1, inventory.Pagination.NextCursor); result != nil || humaStatus(err) != 409 {
		t.Fatalf("selector-revision cursor = %+v, %v", result, err)
	}
	if slices.ContainsFunc(fake.calls, func(call string) bool {
		return strings.HasPrefix(call, "list:")
	}) {
		t.Fatalf("stale selector cursor reached state reader: %v", fake.calls)
	}
}

func TestRuntimeServiceDirectoryHandlesHiddenAbsentAndDriftedSelector(t *testing.T) {
	t.Run("hidden", func(t *testing.T) {
		base := newServiceDirectoryTestStore()
		fake := &runtimeSelectorDirectoryStore{serviceDirectoryTestStore: base}
		service := NewRuntimeServiceDirectoryService(Options{
			Store: fake,
			Visible: func(context.Context) func(store.Repo) bool {
				return func(store.Repo) bool { return false }
			},
		}, &store.ServiceStateV3Reader{})
		result, err := service.List(t.Context(), ServiceInventoryQuery{
			Repository: base.publication.Repository,
		}, serviceMaxPageSize+1, "malformed")
		if result != nil || humaStatus(err) != 404 {
			t.Fatalf("hidden runtime directory = %+v, %v", result, err)
		}
		if slices.Contains(fake.calls, "get-selector") {
			t.Fatalf("hidden directory consulted selector: %v", fake.calls)
		}
	})

	t.Run("absent uses implicit v2", func(t *testing.T) {
		base := newServiceDirectoryTestStore()
		fake := &runtimeSelectorDirectoryStore{
			serviceDirectoryTestStore: base,
			selectorErr:               store.ErrNotFound,
		}
		service := NewRuntimeServiceDirectoryService(
			Options{Store: fake}, &store.ServiceStateV3Reader{},
		)
		result, err := service.List(t.Context(), ServiceInventoryQuery{
			Repository: base.publication.Repository,
		}, 1, "")
		if err != nil || result == nil || len(result.Services) != 1 {
			t.Fatalf("implicit v2 runtime = %+v, %v", result, err)
		}
		if !slices.Contains(fake.calls, "get-selector") ||
			!slices.ContainsFunc(fake.calls, func(call string) bool {
				return strings.HasPrefix(call, "list:")
			}) || slices.Contains(fake.calls, "confirm-selector") {
			t.Fatalf("implicit v2 selector calls = %v", fake.calls)
		}
	})

	t.Run("implicit v2 refuses selector activation", func(t *testing.T) {
		base := newServiceDirectoryTestStore()
		fake := &runtimeSelectorDirectoryStore{
			serviceDirectoryTestStore: base,
			selectorErr:               store.ErrNotFound,
			selectorAtRead:            2,
			selector: store.ServiceRuntimeSelector{
				Repository: base.publication.Repository,
				Backend:    store.ServiceRuntimeV3,
			},
		}
		service := NewRuntimeServiceDirectoryService(
			Options{Store: fake}, &store.ServiceStateV3Reader{},
		)
		result, err := service.List(t.Context(), ServiceInventoryQuery{
			Repository: base.publication.Repository,
		}, 1, "")
		if result != nil || humaStatus(err) != 409 {
			t.Fatalf("implicit v2 selector activation = %+v, %v", result, err)
		}
		if fake.calls[len(fake.calls)-1] != "get-selector" {
			t.Fatalf("implicit v2 selector activation calls = %v", fake.calls)
		}
	})

	t.Run("selector conflict fails closed", func(t *testing.T) {
		base := newServiceDirectoryTestStore()
		fake := &runtimeSelectorDirectoryStore{
			serviceDirectoryTestStore: base,
			selectorErr:               store.ErrConflict,
		}
		service := NewRuntimeServiceDirectoryService(
			Options{Store: fake}, &store.ServiceStateV3Reader{},
		)
		result, err := service.List(t.Context(), ServiceInventoryQuery{
			Repository: base.publication.Repository,
		}, 1, "")
		if result != nil || humaStatus(err) != 409 {
			t.Fatalf("conflicted runtime selector = %+v, %v", result, err)
		}
		if !slices.Contains(fake.calls, "get-selector") ||
			slices.ContainsFunc(fake.calls, func(call string) bool {
				return strings.HasPrefix(call, "list:")
			}) {
			t.Fatalf("conflicted selector fallback calls = %v", fake.calls)
		}
	})

	t.Run("final drift", func(t *testing.T) {
		base := newServiceDirectoryTestStore()
		fake := &runtimeSelectorDirectoryStore{
			serviceDirectoryTestStore: base,
			selector: store.ServiceRuntimeSelector{
				Repository: base.publication.Repository, Backend: store.ServiceRuntimeV2,
				CatalogGenerationDigest: base.publication.GenerationDigest,
				CatalogControlRevision:  base.publication.ControlRevision,
				StateControlRevision:    base.summary.ControlRevision,
				StateSummaryDigest:      base.summary.SummaryDigest,
			},
			failConfirmAt: 1,
		}
		service := NewRuntimeServiceDirectoryService(
			Options{Store: fake}, &store.ServiceStateV3Reader{},
		)
		result, err := service.Detail(
			t.Context(), base.publication.Repository, "orders",
		)
		if result != nil || humaStatus(err) != 409 {
			t.Fatalf("drifted runtime selector = %+v, %v", result, err)
		}
		if fake.calls[len(fake.calls)-1] != "confirm-selector" {
			t.Fatalf("drifted selector final calls = %v", fake.calls)
		}
	})
}
