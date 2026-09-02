package t4110

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestSelectedServiceComparisonUsesSemanticSlices(t *testing.T) {
	expected := servicecatalog.Service{
		Key: "alpha", DisplayName: "Alpha",
		Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
	}
	membership := servicecatalog.Membership{
		ServiceKey: expected.Key, Path: "alpha/main.go",
		Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
	}
	projectionService := expected
	projectionService.Successors = []string{}
	entry := store.ServiceStateEntry{
		State: servicecatalog.ServiceState{
			ServiceKey: expected.Key, DisplayName: expected.DisplayName,
			Disposition: expected.Disposition, Origin: expected.Origin,
			Status: servicecatalog.StatusCurrent, Successors: []string{},
		},
		Projection: &servicecatalog.ServiceProjection{
			Service: projectionService, Memberships: []servicecatalog.Membership{membership},
		},
	}
	if !selectedServiceValuesMatch(entry, expected, []servicecatalog.Membership{membership}) ||
		!sameCatalogService(expected, projectionService) {
		t.Fatal("nil and empty successor slices were treated as different values")
	}
	changed := projectionService
	changed.Successors = []string{"beta"}
	if sameCatalogService(expected, changed) {
		t.Fatal("a real successor change was ignored")
	}

	left := servicecatalog.Catalog{Services: []servicecatalog.Service{
		expected,
		{Key: "beta", DisplayName: "Beta", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
	}}
	right := cloneCatalog(left)
	right.Services[0].Successors = []string{}
	right.Services[1].DisplayName = "Beta changed"
	key, err := firstCatalogServiceDifference(left, right)
	if err != nil || key != "beta" {
		t.Fatalf("first semantic service difference = %q, %v", key, err)
	}
}

func TestT4110EmptyRelationshipInputsAreValidAndDeterministic(t *testing.T) {
	repository := "neutral.invalid/t4110/relationship"
	commit := strings.Repeat("a", 40)
	first, err := publishEmptyRelationshipComponents(
		t.Context(),
		t.TempDir(),
		repository,
		commit,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := publishEmptyRelationshipComponents(
		t.Context(),
		t.TempDir(),
		repository,
		commit,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.resolver.Root().Digest != second.resolver.Root().Digest ||
		first.rpc.Root().Digest != second.rpc.Root().Digest ||
		first.kafka.Root().Digest != second.kafka.Root().Digest ||
		first.upstream.Digest != second.upstream.Digest {
		t.Fatal("empty relationship inputs are not deterministic")
	}
	if err := first.rpc.WalkPostings(context.Background(), func(_ rpccallerposting.Posting) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
