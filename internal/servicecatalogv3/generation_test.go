package servicecatalogv3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestDecodeCatalogExpandedBoundsAndRootRoundTrip(t *testing.T) {
	expanded := acceptedCatalog(servicecatalog.MaxServices+1, false)
	expanded.Unowned = []servicecatalog.UnownedPlacement{}
	raw, err := json.Marshal(expanded)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCatalog(raw)
	if err != nil || len(decoded.Services) != servicecatalog.MaxServices+1 {
		t.Fatalf("decode expanded catalog = %d, %v", len(decoded.Services), err)
	}
	generation, err := Build(testBinding(decoded.Authority), decoded)
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, err := EncodeRoot(generation.Root)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := DecodeRoot(rootRaw)
	if err != nil || !reflect.DeepEqual(opened, generation.Root) {
		t.Fatalf("root round-trip = %+v, %v", opened, err)
	}

	overflow := acceptedCatalog(MaxTotalServices+1, false)
	overflow.Unowned = []servicecatalog.UnownedPlacement{}
	overflowRaw, err := json.Marshal(overflow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCatalog(overflowRaw); !errors.Is(err, ErrLimit) {
		t.Fatalf("service collection one-over = %v", err)
	}
	for name, malformed := range map[string][]byte{
		"unknown field":   []byte(`{"schema":"phebs-service-catalog-v2","authority":{"kind":"committed","id":"catalog","version":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"services":[],"memberships":[],"unowned":[],"extra":true}`),
		"duplicate field": []byte(`{"schema":"phebs-service-catalog-v2","schema":"phebs-service-catalog-v2","authority":{"kind":"committed","id":"catalog","version":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"services":[],"memberships":[],"unowned":[]}`),
		"missing field":   []byte(`{"schema":"phebs-service-catalog-v2","authority":{"kind":"committed","id":"catalog","version":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"services":[],"memberships":[]}`),
		"trailing value":  append(bytes.Clone(raw), []byte(` {}`)...),
		"deep nesting":    []byte(strings.Repeat("[", maxPreflightDepth+1) + "0" + strings.Repeat("]", maxPreflightDepth+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCatalog(malformed); err == nil {
				t.Fatal("malformed catalog was admitted")
			}
		})
	}
}

func TestValidateStateProjectionAllowsUnchangedDesiredCatalogSuccessor(t *testing.T) {
	projection := servicecatalog.ServiceProjection{
		Repository: "example/catalog",
		Service: servicecatalog.Service{
			Key: "orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		},
		SourceGeneration:  "sha256:" + strings.Repeat("1", 64),
		CatalogGeneration: "sha256:" + strings.Repeat("2", 64),
		GenerationDigest:  "sha256:" + strings.Repeat("3", 64),
	}
	desired, err := ServiceDesiredGeneration(projection, 1)
	if err != nil {
		t.Fatal(err)
	}
	state := servicecatalog.ServiceState{
		Schema: ServiceStateSchema, Repository: projection.Repository,
		ServiceKey: projection.Service.Key, DisplayName: projection.Service.DisplayName,
		Disposition: projection.Service.Disposition, Origin: projection.Service.Origin,
		Incarnation: 1, DesiredGeneration: desired,
		DesiredSourceGeneration:  projection.SourceGeneration,
		DesiredCatalogGeneration: projection.CatalogGeneration,
		ActiveDesiredGeneration:  desired,
		ActiveSourceGeneration:   projection.SourceGeneration,
		ActiveCatalogGeneration:  projection.CatalogGeneration,
		ActiveSearchGeneration:   "sha256:" + strings.Repeat("4", 64),
		Status:                   servicecatalog.StatusCurrent,
		ControlRevision:          1,
		ChangedAt:                time.Unix(1, 0).UTC(),
	}
	if err := SetServiceStateDigest(&state); err != nil {
		t.Fatal(err)
	}
	successor := projection
	successor.CatalogGeneration = "sha256:" + strings.Repeat("5", 64)
	if err := ValidateStateProjection(state, successor, false); err != nil {
		t.Fatalf("unchanged desired projection in successor: %v", err)
	}
	if err := ValidateStateProjection(state, successor, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("active projection accepted wrong catalog root: %v", err)
	}
}

func testBinding(authority servicecatalog.Authority) Binding {
	commit := strings.Repeat("a", 40)
	if authority.Kind == servicecatalog.AuthorityCommitted {
		commit = authority.Version
	}
	return Binding{Repository: "example/catalog", Source: Source{
		Kind: servicecatalog.SourceCommitted, Path: "/tmp/catalog.json", Commit: commit,
		CensusDigest: rawDigest([]byte("census")),
	}, Authority: authority}
}

func acceptedCatalog(count int, nested bool) servicecatalog.Catalog {
	authority := servicecatalog.Authority{Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: strings.Repeat("b", 40)}
	catalog := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: authority, Services: make([]servicecatalog.Service, 0, count), Memberships: make([]servicecatalog.Membership, 0, count)}
	for index := range count {
		key := fmt.Sprintf("service-%05d", index)
		path := fmt.Sprintf("path/%05d", index)
		if nested {
			path = fmt.Sprintf("tree/%05d", index)
		}
		catalog.Services = append(catalog.Services, servicecatalog.Service{Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase})
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{ServiceKey: key, Path: path, Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase})
	}
	return catalog
}

func TestBuildV2ParityDeterminismAndPrefixRoutedLookup(t *testing.T) {
	catalog := acceptedCatalog(MaxPathsPerMember+2, true)
	catalog.Services = append(catalog.Services, servicecatalog.Service{Key: "tree-owner", DisplayName: "tree owner", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase})
	catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{ServiceKey: "tree-owner", Path: "tree", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase})
	binding := testBinding(catalog.Authority)
	first, err := Build(binding, catalog)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(binding, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.Root.PlacementMembers) != 2 || first.Root.PlacementMembers[1].PreludeClaims != 1 {
		t.Fatalf("non-deterministic or unsegmented root: %+v", first.Root)
	}
	v2Digest, err := servicecatalog.Digest(catalog)
	if err != nil || first.Root.MappedV2Digest != v2Digest {
		t.Fatalf("v2 digest = %q, %v", first.Root.MappedV2Digest, err)
	}
	mapped, err := first.ToV2()
	if err != nil {
		t.Fatal(err)
	}
	want, err := servicecatalog.Normalize(catalog)
	if err != nil || !reflect.DeepEqual(mapped, want) {
		t.Fatalf("mapped catalog differs: %v", err)
	}
	service, memberships, err := first.LookupService("tree-owner")
	if err != nil || service.Key != "tree-owner" || len(memberships) != 1 {
		t.Fatalf("service lookup = %+v %+v %v", service, memberships, err)
	}
	placements, err := first.LookupPath("tree/02049/file.go")
	if err != nil || len(placements) != 2 || placements[0].Path != "tree" || placements[1].Path != "tree/02049" {
		t.Fatalf("path lookup = %+v, %v", placements, err)
	}
}

func TestCatalogContextAccountsDecodedGenerationRecords(t *testing.T) {
	catalog := acceptedCatalog(MaxPathsPerMember+1, true)
	catalog.Services = append(catalog.Services, servicecatalog.Service{
		Key: "tree-owner", DisplayName: "tree owner",
		Disposition: servicecatalog.DispositionAccepted,
		Origin:      servicecatalog.OriginBase,
	})
	catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
		ServiceKey: "tree-owner", Path: "tree",
		Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
	})
	generation, err := Build(testBinding(catalog.Authority), catalog)
	if err != nil {
		t.Fatal(err)
	}
	var serviceVisits, placementVisits, inheritedVisits uint64
	for _, encoded := range generation.Members {
		switch encoded.Kind {
		case "service":
			var member ServiceMember
			if err := decodeCanonical(encoded.Content, &member); err != nil {
				t.Fatal(err)
			}
			serviceVisits += uint64(len(member.Services) + len(member.Memberships))
		case "placement":
			var member PlacementMember
			if err := decodeCanonical(encoded.Content, &member); err != nil {
				t.Fatal(err)
			}
			placementVisits += uint64(len(member.Inherited) + len(member.Placements))
			inheritedVisits += uint64(len(member.Inherited))
		default:
			t.Fatalf("unexpected member kind %q", encoded.Kind)
		}
	}
	if serviceVisits == 0 || placementVisits == 0 || inheritedVisits == 0 {
		t.Fatalf(
			"incomplete accounting fixture: service=%d placement=%d inherited=%d",
			serviceVisits, placementVisits, inheritedVisits,
		)
	}
	wantVisits := serviceVisits + placementVisits
	scoped, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := generation.CatalogContext(scoped)
	if err != nil || len(opened.Services) != len(catalog.Services) {
		t.Fatalf("catalog context = %d services, %v", len(opened.Services), err)
	}
	counts, err := ledger.Finish()
	if err != nil || counts.MemberVisits != wantVisits {
		t.Fatalf("generation member visits = %+v, %v", counts, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := generation.CatalogContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled catalog context = %v", err)
	}
	closed, closedLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closedLedger.Finish(); err != nil {
		t.Fatal(err)
	}
	if _, err := generation.CatalogContext(closed); !errors.Is(err, readaccounting.ErrClosed) {
		t.Fatalf("closed-scope catalog = %v", err)
	}
}

func TestPrefixPreludeCoversWholeRoutedRange(t *testing.T) {
	catalog := acceptedCatalog(2, false)
	catalog.Memberships[0].Path = "a"
	catalog.Memberships[1].Path = "a/x"
	for index := range MaxPathsPerMember {
		catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
			Path: fmt.Sprintf("a-%04d", index), Origin: servicecatalog.OriginBase,
		})
	}
	generation, err := Build(testBinding(catalog.Authority), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(generation.Root.PlacementMembers) != 2 || generation.Root.PlacementMembers[1].PreludeClaims != 1 {
		t.Fatalf("placement members = %+v", generation.Root.PlacementMembers)
	}
	placements, err := generation.LookupPath("a/x/file.go")
	if err != nil || len(placements) != 2 || placements[0].Path != "a" || placements[1].Path != "a/x" {
		t.Fatalf("path lookup = %+v, %v", placements, err)
	}
	mapped, err := generation.ToV2()
	if err != nil || len(mapped.Unowned) != MaxPathsPerMember {
		t.Fatalf("unowned round-trip = %d, %v", len(mapped.Unowned), err)
	}

	missing := cloneGeneration(generation)
	ordinal := 1
	memberIndex := len(missing.Root.ServiceMembers) + ordinal
	var member PlacementMember
	if err := decodeCanonical(missing.Members[memberIndex].Content, &member); err != nil {
		t.Fatal(err)
	}
	member.Inherited = []Placement{}
	replacePlacementMember(t, &missing, ordinal, member)
	if err := ValidateGeneration(missing); err == nil {
		t.Fatal("missing routed-range ancestor was admitted")
	}
}

func TestV2SuccessorBoundaryAndPersistedConversion(t *testing.T) {
	authority := servicecatalog.Authority{Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: strings.Repeat("b", 40)}
	catalog := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: authority}
	owner := servicecatalog.Service{Key: "owner", DisplayName: "owner", Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase, Reason: "renamed"}
	for index := range MaxServiceSuccessors {
		key := fmt.Sprintf("target-%04d", index)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
			ServiceKey: key, Path: fmt.Sprintf("target/%04d", index),
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
		owner.Successors = append(owner.Successors, key)
	}
	catalog.Services = append(catalog.Services, owner)
	wantDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := Build(testBinding(authority), catalog)
	if err != nil || generation.Root.MappedV2Digest != wantDigest {
		t.Fatalf("v2 successor mapping = %q, %v", generation.Root.MappedV2Digest, err)
	}

	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: "example/catalog",
		SourceKind: servicecatalog.SourceCommitted, SourcePath: "/tmp/catalog.json",
		SourceCommit: authority.Version, SourceCensusDigest: rawDigest([]byte("census")),
		Authority: authority, CatalogDigest: wantDigest, Canonical: canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		persisted bool
	}{
		{name: "unpublished"},
		{name: "persisted", persisted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := publication
			if test.persisted {
				candidate.ControlRevision = 7
				candidate.PublishedAt = time.Date(2026, 8, 25, 18, 0, 0, 0, time.UTC)
			}
			if _, err := FromV2(candidate, catalog); err != nil {
				t.Fatal(err)
			}
		})
	}

	overflow := catalog
	overflow.Services = slices.Clone(catalog.Services)
	overflow.Memberships = slices.Clone(catalog.Memberships)
	overflowOwner := overflow.Services[len(overflow.Services)-1]
	overflowOwner.Successors = append(slices.Clone(overflowOwner.Successors), "target-overflow")
	overflow.Services[len(overflow.Services)-1] = overflowOwner
	overflow.Services = append(overflow.Services, servicecatalog.Service{
		Key: "target-overflow", DisplayName: "target overflow", Disposition: servicecatalog.DispositionAccepted,
		Origin: servicecatalog.OriginBase,
	})
	overflow.Memberships = append(overflow.Memberships, servicecatalog.Membership{
		ServiceKey: "target-overflow", Path: "target/overflow", Role: servicecatalog.RolePrimary,
		Origin: servicecatalog.OriginBase,
	})
	overflowDigest, err := servicecatalog.Digest(overflow)
	if err != nil {
		t.Fatalf("v2 successor one-over validity = %v", err)
	}
	overflowCanonical, err := servicecatalog.Canonical(overflow)
	if err != nil {
		t.Fatal(err)
	}
	overflowPublication := publication
	overflowPublication.CatalogDigest = overflowDigest
	overflowPublication.Canonical = overflowCanonical
	overflowPublication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(overflowPublication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromV2(overflowPublication, overflow); !errors.Is(err, ErrLimit) {
		t.Fatalf("valid v2 successor one-over conversion = %v", err)
	}
}

func TestSuccessorAggregateBoundary(t *testing.T) {
	catalog := acceptedCatalog(MaxServiceSuccessors, false)
	targets := make([]string, len(catalog.Services))
	for index, service := range catalog.Services {
		targets[index] = service.Key
	}
	remaining := MaxSuccessorEdges
	for index := 0; remaining > 0; index++ {
		count := min(MaxServiceSuccessors, remaining)
		key := fmt.Sprintf("owner-%02d", index)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionRejected,
			Origin: servicecatalog.OriginBase, Reason: "renamed", Successors: slices.Clone(targets[:count]),
		})
		remaining -= count
	}
	generation, err := Build(testBinding(catalog.Authority), catalog)
	if err != nil || generation.Root.Successors != MaxSuccessorEdges {
		t.Fatalf("aggregate successor boundary = %d, %v", generation.Root.Successors, err)
	}

	overflow := catalog
	overflow.Services = slices.Clone(catalog.Services)
	owner := overflow.Services[len(overflow.Services)-1]
	owner.Successors = append(slices.Clone(owner.Successors), targets[len(owner.Successors)])
	overflow.Services[len(overflow.Services)-1] = owner
	if _, err := Build(testBinding(overflow.Authority), overflow); !errors.Is(err, ErrLimit) {
		t.Fatalf("aggregate successor one-over = %v", err)
	}
}

func TestDecodeCanonicalPreflightsCollections(t *testing.T) {
	tests := []struct {
		name  string
		field string
		count int
	}{
		{name: "services", field: "services", count: MaxServicesPerMember + 1},
		{name: "placements", field: "placements", count: MaxPathsPerMember + 1},
		{name: "successors", field: "successors", count: MaxServiceSuccessors + 1},
		{name: "claims", field: "claims", count: MaxClaimsPerPlacement + 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(`{"` + test.field + `":[` + strings.Repeat(`{},`, test.count-1) + `{}` + `]}`)
			var value map[string]any
			if err := decodeCanonical(raw, &value); !errors.Is(err, ErrLimit) {
				t.Fatalf("decode error = %v", err)
			}
		})
	}

	t.Run("nesting depth", func(t *testing.T) {
		raw := []byte(strings.Repeat("[", maxPreflightDepth+1) + "0" + strings.Repeat("]", maxPreflightDepth+1))
		if err := preflightCollections(raw); !errors.Is(err, ErrLimit) {
			t.Fatalf("decode error = %v", err)
		}
	})
}

func TestCompleteValidationRejectsInventoryPreludeAndCrossViewDrift(t *testing.T) {
	catalog := acceptedCatalog(MaxPathsPerMember+2, true)
	catalog.Services = append(catalog.Services, servicecatalog.Service{Key: "tree-owner", DisplayName: "tree owner", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase})
	catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{ServiceKey: "tree-owner", Path: "tree", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase})
	base, err := Build(testBinding(catalog.Authority), catalog)
	if err != nil {
		t.Fatal(err)
	}

	missing := cloneGeneration(base)
	placementIndex := len(missing.Root.ServiceMembers) + 1
	var member PlacementMember
	if err := decodeCanonical(missing.Members[placementIndex].Content, &member); err != nil {
		t.Fatal(err)
	}
	member.Inherited = []Placement{}
	replacePlacementMember(t, &missing, 1, member)
	if err := ValidateGeneration(missing); err == nil {
		t.Fatal("missing inherited claim was admitted")
	}

	drift := cloneGeneration(base)
	if err := decodeCanonical(drift.Members[placementIndex].Content, &member); err != nil {
		t.Fatal(err)
	}
	member.Placements[0].Claims[0].Disposition = servicecatalog.DispositionRejected
	replacePlacementMember(t, &drift, 1, member)
	if err := ValidateGeneration(drift); err == nil {
		t.Fatal("cross-view disposition drift was admitted")
	}

	corrupt := cloneGeneration(base)
	corrupt.Members[0].Content[0] ^= 1
	if err := ValidateGeneration(corrupt); err == nil {
		t.Fatal("corrupt member was admitted")
	}
	incomplete := cloneGeneration(base)
	incomplete.Members = incomplete.Members[:len(incomplete.Members)-1]
	if err := ValidateGeneration(incomplete); err == nil {
		t.Fatal("missing member was admitted")
	}
	wrongLogicalBytes := cloneGeneration(base)
	wrongLogicalBytes.Root.LogicalBytes++
	if err := finalizeRoot(&wrongLogicalBytes.Root); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGeneration(wrongLogicalBytes); err == nil {
		t.Fatal("wrong logical byte count was admitted")
	}
}

func TestExpandedRulesAndDowngradeRefusal(t *testing.T) {
	expanded := servicecatalog.Catalog{Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: strings.Repeat("b", 40)}}
	for index := range servicecatalog.MaxServices + 1 {
		key := fmt.Sprintf("proposal-%05d", index)
		expanded.Services = append(expanded.Services, servicecatalog.Service{Key: key, DisplayName: key, Disposition: servicecatalog.DispositionProposal, Origin: servicecatalog.OriginBase, Reason: "review"})
		expanded.Memberships = append(expanded.Memberships, servicecatalog.Membership{ServiceKey: key, Path: fmt.Sprintf("proposal/%05d", index), Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase})
	}
	generation, err := Build(testBinding(expanded.Authority), expanded)
	if err != nil {
		t.Fatal(err)
	}
	if generation.Root.MappedV2Digest != "" {
		t.Fatal("expanded catalog received a v2 digest")
	}
	if _, err := generation.ToV2(); !errors.Is(err, ErrNotV2Compatible) {
		t.Fatalf("downgrade = %v", err)
	}

	cycle := acceptedCatalog(2, false)
	cycle.Services[0].Disposition, cycle.Services[0].Reason, cycle.Services[0].Successors = servicecatalog.DispositionRejected, "renamed", []string{cycle.Services[1].Key}
	cycle.Services[1].Disposition, cycle.Services[1].Reason, cycle.Services[1].Successors = servicecatalog.DispositionRejected, "renamed", []string{cycle.Services[0].Key}
	if _, err := Build(testBinding(cycle.Authority), cycle); err == nil {
		t.Fatal("successor cycle was admitted")
	}

	fanout := acceptedCatalog(servicecatalog.MaxAcceptedPathFanout+1, false)
	for index := range fanout.Memberships {
		fanout.Memberships[index].Path = "shared"
	}
	if _, err := Build(testBinding(fanout.Authority), fanout); err == nil {
		t.Fatal("accepted fanout overflow was admitted")
	}

	unowned := acceptedCatalog(1, false)
	unowned.Unowned = []servicecatalog.UnownedPlacement{{Path: "path", Origin: servicecatalog.OriginBase}}
	if _, err := Build(testBinding(unowned.Authority), unowned); err == nil {
		t.Fatal("unowned overlap was admitted")
	}

	nonRejected := acceptedCatalog(2, false)
	nonRejected.Services[0].Successors = []string{nonRejected.Services[1].Key}
	if _, err := Build(testBinding(nonRejected.Authority), nonRejected); err == nil {
		t.Fatal("accepted successor was admitted")
	}

	tooMany := acceptedCatalog(MaxTotalServices+1, false)
	if _, err := Build(testBinding(tooMany.Authority), tooMany); !errors.Is(err, ErrLimit) {
		t.Fatalf("service one-over = %v", err)
	}

	successorOverflow := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: cycle.Authority, Services: []servicecatalog.Service{{
		Key: "retired", DisplayName: "retired", Disposition: servicecatalog.DispositionRejected,
		Origin: servicecatalog.OriginBase, Reason: "renamed", Successors: make([]string, MaxServiceSuccessors+1),
	}}}
	if _, err := Build(testBinding(successorOverflow.Authority), successorOverflow); !errors.Is(err, ErrLimit) {
		t.Fatalf("successor one-over = %v", err)
	}
}

func TestMaximumServiceAndPlacementFitOneMember(t *testing.T) {
	authority := servicecatalog.Authority{Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: strings.Repeat("b", 40)}
	maxService := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: authority}
	for index := range MaxServiceSuccessors {
		key := fmt.Sprintf("target-%03d", index)
		maxService.Services = append(maxService.Services, servicecatalog.Service{Key: key, DisplayName: strings.Repeat("d", servicecatalog.MaxDisplayNameBytes), Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase})
		maxService.Memberships = append(maxService.Memberships, servicecatalog.Membership{ServiceKey: key, Path: fmt.Sprintf("target/%03d", index), Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase})
	}
	owner := servicecatalog.Service{Key: strings.Repeat("s", servicecatalog.MaxServiceKeyBytes), DisplayName: strings.Repeat("d", servicecatalog.MaxDisplayNameBytes), Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase, Reason: strings.Repeat("r", servicecatalog.MaxReasonBytes)}
	for index := range MaxServiceSuccessors {
		owner.Successors = append(owner.Successors, fmt.Sprintf("target-%03d", index))
	}
	maxService.Services = append(maxService.Services, owner)
	remaining := servicecatalog.MaxServicePathBytes
	for index := range servicecatalog.MaxServicePaths {
		length := remaining / (servicecatalog.MaxServicePaths - index)
		prefix := fmt.Sprintf("owned/%03d/", index)
		value := prefix + strings.Repeat("p", length-len(prefix))
		remaining -= len(value)
		maxService.Memberships = append(maxService.Memberships, servicecatalog.Membership{ServiceKey: owner.Key, Path: value, Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase})
	}
	generation, err := Build(testBinding(authority), maxService)
	if err != nil {
		t.Fatal(err)
	}
	ownerMember := slices.IndexFunc(generation.Root.ServiceMembers, func(descriptor MemberDescriptor) bool {
		return owner.Key >= descriptor.First && owner.Key <= descriptor.Last
	})
	if ownerMember < 0 || generation.Root.ServiceMembers[ownerMember].ContentBytes > MaxMemberBytes {
		t.Fatalf("maximum service member = %+v", generation.Root.ServiceMembers)
	}

	maxPlacement := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: authority}
	roles := []string{servicecatalog.RolePrimary, servicecatalog.RoleSupporting, servicecatalog.RoleShared, servicecatalog.RoleGenerated, servicecatalog.RoleTyped}
	for index := range MaxClaimsPerPlacement {
		key := fmt.Sprintf("claim-%04d", index)
		maxPlacement.Services = append(maxPlacement.Services, servicecatalog.Service{Key: key, DisplayName: key, Disposition: servicecatalog.DispositionProposal, Origin: servicecatalog.OriginBase, Reason: "review"})
		for _, role := range roles {
			maxPlacement.Memberships = append(maxPlacement.Memberships, servicecatalog.Membership{ServiceKey: key, Path: "shared", Role: role, Origin: servicecatalog.OriginBase})
		}
	}
	generation, err = Build(testBinding(authority), maxPlacement)
	if err != nil {
		t.Fatal(err)
	}
	if len(generation.Root.PlacementMembers) != 1 || generation.Root.PlacementMembers[0].Claims != MaxClaimsPerPlacement || generation.Root.PlacementMembers[0].ContentBytes > MaxMemberBytes {
		t.Fatalf("maximum placement member = %+v", generation.Root.PlacementMembers)
	}
	var member PlacementMember
	if err := decodeCanonical(generation.Members[len(generation.Root.ServiceMembers)].Content, &member); err != nil {
		t.Fatal(err)
	}
	member.FirstPath, member.LastPath, member.Placements[0].Path = strings.Repeat("p", 4096), strings.Repeat("p", 4096), strings.Repeat("p", 4096)
	raw, err := canonical(member)
	if err != nil || len(raw) > MaxMemberBytes {
		t.Fatalf("maximum-path compact placement bytes = %d, %v", len(raw), err)
	}
}

func TestT411ExactAggregateAndByteBoundaries(t *testing.T) {
	catalog := t411MaximumShapeCatalog()
	generation, err := Build(testBinding(catalog.Authority), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if generation.Root.Services != MaxTotalServices ||
		generation.Root.Memberships != MaxMemberships ||
		generation.Root.Paths != MaxDistinctPaths {
		t.Fatalf(
			"maximum aggregate root = services %d memberships %d paths %d",
			generation.Root.Services, generation.Root.Memberships, generation.Root.Paths,
		)
	}

	membershipOverflow := catalog
	existingPath := catalog.Memberships[0].Path
	membershipOverflow.Memberships = append(slices.Clone(catalog.Memberships), servicecatalog.Membership{
		ServiceKey: catalog.Services[0].Key, Path: existingPath,
		Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase,
	})
	if len(membershipOverflow.Memberships) != MaxMemberships+1 ||
		membershipOverflow.Memberships[len(membershipOverflow.Memberships)-1].Path != existingPath {
		t.Fatal("membership one-over fixture did not isolate the membership bound")
	}
	if _, err := Build(testBinding(catalog.Authority), membershipOverflow); !errors.Is(err, ErrLimit) {
		t.Fatalf("membership one-over = %v", err)
	}
	pathOverflow := catalog
	pathOverflow.Unowned = append(slices.Clone(catalog.Unowned), servicecatalog.UnownedPlacement{
		Path: "overflow/path", Origin: servicecatalog.OriginBase,
	})
	if _, err := Build(testBinding(catalog.Authority), pathOverflow); !errors.Is(err, ErrLimit) {
		t.Fatalf("distinct-path one-over = %v", err)
	}

	if err := admitLogicalBytes(MaxLogicalBytes); err != nil {
		t.Fatalf("exact logical-byte admission = %v", err)
	}
	if err := admitLogicalBytes(MaxLogicalBytes + 1); !errors.Is(err, ErrLimit) {
		t.Fatalf("logical-byte one-over = %v", err)
	}
	if err := admitPublicationBytes(
		MaxRootBytes,
		MaxPublicationBytes-MaxRootBytes,
	); err != nil {
		t.Fatalf("exact publication-byte admission = %v", err)
	}
	if err := admitPublicationBytes(
		MaxRootBytes,
		MaxPublicationBytes-MaxRootBytes+1,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("publication-byte one-over = %v", err)
	}
}

func t411MaximumShapeCatalog() servicecatalog.Catalog {
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityCommitted, ID: "catalog",
		Version: strings.Repeat("b", 40),
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services:    make([]servicecatalog.Service, 0, MaxTotalServices),
		Memberships: make([]servicecatalog.Membership, 0, MaxMemberships),
		Unowned:     make([]servicecatalog.UnownedPlacement, 0, MaxDistinctPaths-3*MaxTotalServices),
	}
	roles := []string{
		servicecatalog.RoleSupporting, servicecatalog.RoleShared,
		servicecatalog.RoleGenerated, servicecatalog.RoleTyped,
	}
	for index := range MaxTotalServices {
		key := fmt.Sprintf("service-%05d", index)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
			ServiceKey: key, Path: fmt.Sprintf("primary/%05d", index),
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
		for _, role := range roles {
			catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
				ServiceKey: key, Path: fmt.Sprintf("support/%05d", index),
				Role: role, Origin: servicecatalog.OriginBase,
			})
		}
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
			ServiceKey: key, Path: fmt.Sprintf("shared/%05d", index),
			Role: servicecatalog.RoleShared, Origin: servicecatalog.OriginBase,
		})
	}
	for index := range MaxDistinctPaths - 3*MaxTotalServices {
		catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
			Path: fmt.Sprintf("unowned/%05d", index), Origin: servicecatalog.OriginBase,
		})
	}
	return catalog
}

func replacePlacementMember(t *testing.T, generation *Generation, ordinal int, member PlacementMember) {
	t.Helper()
	index := len(generation.Root.ServiceMembers) + ordinal
	raw, err := canonical(member)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := &generation.Root.PlacementMembers[ordinal]
	generation.Root.EncodedMemberBytes += len(raw) - descriptor.ContentBytes
	descriptor.ContentBytes, descriptor.Digest = len(raw), rawDigest(raw)
	descriptor.PreludeClaims, descriptor.Claims = countClaims(member.Inherited), countClaims(member.Placements)
	generation.Members[index].Content = raw
	if err := finalizeRoot(&generation.Root); err != nil {
		t.Fatal(err)
	}
}

func cloneGeneration(value Generation) Generation {
	result := value
	result.Root.Binding = cloneBinding(value.Root.Binding)
	result.Root.ServiceMembers = slices.Clone(value.Root.ServiceMembers)
	result.Root.PlacementMembers = slices.Clone(value.Root.PlacementMembers)
	result.Members = make([]EncodedMember, len(value.Members))
	for index, member := range value.Members {
		result.Members[index] = member
		result.Members[index].Content = bytes.Clone(member.Content)
	}
	return result
}
