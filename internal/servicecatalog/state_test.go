package servicecatalog

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServiceProjectionIsolatesSiblingAndBindsSource(t *testing.T) {
	publication := stateTestPublication(t, "1111111111111111111111111111111111111111")
	first, err := ProjectServices(publication)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Service.Key != "orders" ||
		first[1].Service.Key != "payments" {
		t.Fatalf("first projections = %+v", first)
	}
	for _, projection := range first {
		point, found, pointErr := ProjectService(publication, projection.Service.Key)
		if pointErr != nil {
			t.Fatal(pointErr)
		}
		if !found || !reflect.DeepEqual(point, projection) {
			t.Fatalf("point projection = %+v, want %+v", point, projection)
		}
	}
	if _, found, pointErr := ProjectService(publication, "absent"); pointErr != nil || found {
		t.Fatalf("absent point projection = found %t, err %v", found, pointErr)
	}
	var forged VerifiedPublication
	if _, _, pointErr := forged.ProjectService("orders"); !errors.Is(pointErr, ErrInvalidPublication) {
		t.Fatalf("unverified point projection = %v, want ErrInvalidPublication", pointErr)
	}
	desiredOne, err := ServiceDesiredGeneration(first[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	desiredTwo, err := ServiceDesiredGeneration(first[0], 2)
	if err != nil {
		t.Fatal(err)
	}
	if desiredOne == desiredTwo {
		t.Fatal("removal/re-add incarnation reused the prior desired generation")
	}

	catalog, err := Decode(publication.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	catalog.Services[0].DisplayName = "Orders API"
	changed := finishStateTestPublication(t, publication, catalog)
	second, err := ProjectServices(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].GenerationDigest == second[0].GenerationDigest {
		t.Fatal("edited service retained its desired identity")
	}
	if first[1].GenerationDigest != second[1].GenerationDigest {
		t.Fatal("sibling-only edit relabeled the unchanged service")
	}

	sourceChanged := changed
	sourceChanged.SourceCommit = "2222222222222222222222222222222222222222"
	sourceChanged.SourceCensusDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	sourceChanged.Authority.Version = sourceChanged.SourceCommit
	catalog.Authority.Version = sourceChanged.SourceCommit
	sourceChanged = finishStateTestPublication(t, sourceChanged, catalog)
	third, err := ProjectServices(sourceChanged)
	if err != nil {
		t.Fatal(err)
	}
	for index := range second {
		if second[index].GenerationDigest == third[index].GenerationDigest {
			t.Fatalf("source transition did not relabel service %q", third[index].Service.Key)
		}
	}
}

func TestProjectPlacementsRetainsCanonicalClaims(t *testing.T) {
	publication := stateTestPublication(t, "4444444444444444444444444444444444444444")
	verified, err := VerifyPublication(publication, true)
	if err != nil {
		t.Fatal(err)
	}
	placements, err := verified.ProjectPlacements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 2 || placements[0].Path != "orders" ||
		placements[1].Path != "payments" || placements[0].Unowned ||
		len(placements[0].Claims) != 1 ||
		placements[0].Claims[0].ServiceKey != "orders" ||
		placements[0].Claims[0].Disposition != DispositionAccepted ||
		!reflect.DeepEqual(placements[0].Claims[0].Roles, []PlacementRole{{
			Role: RolePrimary, Origin: OriginBase,
		}}) {
		t.Fatalf("placement projection = %+v", placements)
	}
	placements[0].Claims[0].Roles[0].Role = RoleShared
	again, err := verified.ProjectPlacements()
	if err != nil || again[0].Claims[0].Roles[0].Role != RolePrimary {
		t.Fatalf("placement projection alias = %+v, %v", again, err)
	}
}

func TestServiceStateAndSummaryDigestsRejectMutation(t *testing.T) {
	publication := stateTestPublication(t, "3333333333333333333333333333333333333333")
	projections, err := ProjectServices(publication)
	if err != nil {
		t.Fatal(err)
	}
	desiredGeneration, err := ServiceDesiredGeneration(projections[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	state := ServiceState{
		Schema: ServiceStateSchema, Repository: publication.Repository,
		ServiceKey:  projections[0].Service.Key,
		DisplayName: projections[0].Service.DisplayName,
		Disposition: projections[0].Service.Disposition,
		Origin:      projections[0].Service.Origin, Successors: []string{},
		Incarnation:              1,
		DesiredGeneration:        desiredGeneration,
		DesiredSourceGeneration:  projections[0].SourceGeneration,
		DesiredCatalogGeneration: projections[0].CatalogGeneration,
		Status:                   StatusUnavailable, ControlRevision: 1, ChangedAt: now,
	}
	if err := SetServiceStateDigest(&state); err != nil {
		t.Fatal(err)
	}
	if err := ValidateServiceState(state, true); err != nil {
		t.Fatal(err)
	}
	nilSuccessors := state
	nilSuccessors.Successors = nil
	nilSuccessors.StateDigest = ""
	if err := SetServiceStateDigest(&nilSuccessors); err != nil {
		t.Fatal(err)
	}
	if nilSuccessors.Successors == nil || nilSuccessors.StateDigest != state.StateDigest {
		t.Fatalf(
			"nil successor normalization = successors %#v digest %q, want [] and %q",
			nilSuccessors.Successors, nilSuccessors.StateDigest, state.StateDigest,
		)
	}
	if err := ValidateServiceState(nilSuccessors, true); err != nil {
		t.Fatal(err)
	}
	mutated := state
	mutated.DisplayName = "mutated"
	if err := ValidateServiceState(mutated, true); err == nil {
		t.Fatal("mutated state passed its semantic digest")
	}

	summary := RepositoryState{
		Schema: RepositoryStateSchema, Repository: publication.Repository,
		CatalogGeneration:      publication.GenerationDigest,
		CatalogControlRevision: publication.ControlRevision,
		CatalogServiceCount:    2, LiveServiceCount: 2,
		UnavailableCount: 2, ControlRevision: 1, UpdatedAt: now,
	}
	if err := SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryState(summary, true); err != nil {
		t.Fatal(err)
	}
	summary.UnavailableCount--
	if err := ValidateRepositoryState(summary, true); err == nil {
		t.Fatal("mutated summary passed its semantic digest")
	}
}

func stateTestPublication(t *testing.T, commit string) Publication {
	t.Helper()
	catalog := Catalog{
		Schema:    Schema,
		Authority: Authority{Kind: AuthorityCommitted, ID: "catalog", Version: commit},
		Services: []Service{
			{Key: "orders", DisplayName: "Orders", Disposition: DispositionAccepted, Origin: OriginBase},
			{Key: "payments", DisplayName: "Payments", Disposition: DispositionAccepted, Origin: OriginBase},
		},
		Memberships: []Membership{
			{ServiceKey: "orders", Path: "orders", Role: RolePrimary, Origin: OriginBase},
			{ServiceKey: "payments", Path: "payments", Role: RolePrimary, Origin: OriginBase},
		},
		Unowned: []UnownedPlacement{},
	}
	publication := Publication{
		Schema: PublicationSchema, Repository: "example.com/acme/mono",
		SourceKind: SourceCommitted, SourcePath: "/catalog.json", SourceCommit: commit,
		SourceCensusDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		SourceFileCount:    2, AcceptedFileCount: 2,
		ControlRevision: 1,
		PublishedAt:     time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC),
	}
	return finishStateTestPublication(t, publication, catalog)
}

func finishStateTestPublication(
	t *testing.T,
	publication Publication,
	catalog Catalog,
) Publication {
	t.Helper()
	canonical, err := Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication.Authority = catalog.Authority
	publication.Override = catalog.Override
	publication.CatalogDigest = digest
	publication.Canonical = canonical
	publication.GenerationDigest, err = PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublication(publication, true); err != nil {
		t.Fatal(err)
	}
	return publication
}
