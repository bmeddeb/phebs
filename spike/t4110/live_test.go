package t4110

import (
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/spike/t411"
)

func TestMeasuredUTCDateUsesActualUTCDay(t *testing.T) {
	local := time.Date(
		2026, time.September, 1, 23, 30, 0, 0,
		time.FixedZone("test-offset", -2*60*60),
	)
	if got := measuredUTCDate(local); got != "2026-09-02" {
		t.Fatalf("measured UTC date = %q", got)
	}
}

func TestLiveTargetCatalogUsesExpandedV3Input(t *testing.T) {
	target, err := t411.BuildTargetCorpus()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeLiveV3Catalog(target.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := servicecatalogv3.DecodeCatalog(raw)
	if err != nil || len(decoded.Services) != t411.AcceptedServiceTarget ||
		len(decoded.Memberships) != target.Profile.Memberships {
		t.Fatalf(
			"expanded live catalog services=%d memberships=%d: %v",
			len(decoded.Services), len(decoded.Memberships), err,
		)
	}
}

func TestMappedTransitionRetainsExpandedV3Target(t *testing.T) {
	target, err := t411.BuildTargetCorpus()
	if err != nil {
		t.Fatal(err)
	}
	transition, err := t411.BuildTransitionCorpus()
	if err != nil {
		t.Fatal(err)
	}
	mapping := make(map[string]string, len(transition.Profile.Revisions[0].Catalog.Services))
	for index, service := range transition.Profile.Revisions[0].Catalog.Services {
		mapping[service.Key] = target.Catalog.Services[index].Key
	}
	mapped, err := mappedTransitionCatalog(
		target.Catalog, transition.Profile.Revisions[0], mapping,
	)
	if err != nil || len(mapped.Services) != t411.AcceptedServiceTarget {
		t.Fatalf("mapped expanded transition services=%d: %v", len(mapped.Services), err)
	}
}
