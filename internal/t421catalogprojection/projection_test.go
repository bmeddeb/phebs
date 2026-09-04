package t421catalogprojection

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestDeriveIsOrderedAndCancellable(t *testing.T) {
	catalog := servicecatalog.Catalog{
		Services: []servicecatalog.Service{
			{Key: "b", Disposition: servicecatalog.DispositionAccepted},
			{Key: "a", Disposition: servicecatalog.DispositionAccepted},
		},
		Memberships: []servicecatalog.Membership{
			{ServiceKey: "b", Path: "z", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
			{ServiceKey: "a", Path: "a", Role: servicecatalog.RoleTyped, Origin: servicecatalog.OriginOverride},
			{ServiceKey: "a", Path: "a", Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase},
		},
		Unowned: []servicecatalog.UnownedPlacement{
			{Path: "u", Origin: servicecatalog.OriginOverride},
		},
	}
	want, err := Derive(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	reordered := catalog
	reordered.Services = []servicecatalog.Service{catalog.Services[1], catalog.Services[0]}
	reordered.Memberships = []servicecatalog.Membership{
		catalog.Memberships[2], catalog.Memberships[1], catalog.Memberships[0],
	}
	got, err := Derive(context.Background(), reordered)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("equivalent catalog ordering changed the five-set projection")
	}
	if got.Catalog.Records != 2 || got.Memberships.Records != 3 || got.Placements.Records != 3 ||
		got.UnownedPrefixes.Records != 1 || got.ServiceQueries.Records != 2 {
		t.Fatalf("projection counts = %+v", got)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Derive(canceled, catalog); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled derivation error = %v", err)
	}
}
