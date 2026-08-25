package servicecatalogv3

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

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
