package servicecatalogv3

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

// DecodeCatalog opens the retained logical record model under v3's expanded
// aggregate bounds. Input need not already be canonically ordered.
func DecodeCatalog(raw []byte) (servicecatalog.Catalog, error) {
	catalog, err := servicecatalog.DecodeWithLimits(raw, servicecatalog.DecodeLimits{
		MaxEncodedBytes: MaxPublicationBytes, MaxServices: MaxTotalServices,
		MaxMemberships: MaxMemberships, MaxDistinctPaths: MaxDistinctPaths,
		MaxSuccessorEdges:    MaxSuccessorEdges,
		MaxServiceSuccessors: MaxServiceSuccessors,
	})
	if err != nil {
		if errors.Is(err, servicecatalog.ErrEncodedLimit) ||
			errors.Is(err, servicecatalog.ErrServiceLimit) ||
			errors.Is(err, servicecatalog.ErrMembershipLimit) ||
			errors.Is(err, servicecatalog.ErrDistinctPathLimit) ||
			errors.Is(err, servicecatalog.ErrSuccessorLimit) {
			return servicecatalog.Catalog{}, fmt.Errorf("%w: %v", ErrLimit, err)
		}
		return servicecatalog.Catalog{}, err
	}
	normalized, _, err := normalize(catalog)
	return normalized, err
}

// ValidateCatalog applies v3's logical contract without building members.
func ValidateCatalog(catalog servicecatalog.Catalog) error {
	_, _, err := normalize(catalog)
	return err
}

// EncodeRoot returns the exact precious root bytes stored by T41.3.
func EncodeRoot(root Root) ([]byte, error) {
	if err := ValidateRoot(root); err != nil {
		return nil, err
	}
	return canonical(root)
}

// DecodeRoot strict-opens exact canonical precious root bytes.
func DecodeRoot(raw []byte) (Root, error) {
	if len(raw) > MaxRootBytes {
		return Root{}, limitf("root bytes")
	}
	var root Root
	if err := decodeCanonical(raw, &root); err != nil {
		return Root{}, err
	}
	if err := ValidateRoot(root); err != nil {
		return Root{}, err
	}
	return root, nil
}

func FromV2(publication servicecatalog.Publication, catalog servicecatalog.Catalog) (Generation, error) {
	persisted := publication.ControlRevision != 0 || !publication.PublishedAt.IsZero()
	if err := servicecatalog.ValidatePublication(publication, persisted); err != nil {
		return Generation{}, err
	}
	digest, err := servicecatalog.Digest(catalog)
	if err != nil || digest != publication.CatalogDigest {
		return Generation{}, invalidf("v2 publication catalog mismatch")
	}
	return Build(Binding{Repository: publication.Repository, Source: Source{
		Kind: publication.SourceKind, Path: publication.SourcePath, Commit: publication.SourceCommit,
		CensusDigest: publication.SourceCensusDigest, LegacyDigest: publication.LegacyAnalysisUnitDigest,
		FileCount: publication.SourceFileCount, AcceptedFileCount: publication.AcceptedFileCount,
		UnownedFileCount: publication.UnownedFileCount,
	}, Authority: publication.Authority, Override: cloneOverride(publication.Override)}, catalog)
}

func ValidateGeneration(generation Generation) error {
	return validateGeneration(generation, nil)
}

func validateGeneration(generation Generation, opened *servicecatalog.Catalog) error {
	root := generation.Root
	if err := ValidateRoot(root); err != nil {
		return err
	}
	if len(generation.Members) != len(root.ServiceMembers)+len(root.PlacementMembers) {
		return invalidf("member inventory count")
	}
	serviceView := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: root.Binding.Authority, Override: cloneOverride(root.Binding.Override)}
	placementView := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: root.Binding.Authority, Override: cloneOverride(root.Binding.Override)}
	memberIndex := 0
	for ordinal, descriptor := range root.ServiceMembers {
		encoded := generation.Members[memberIndex]
		memberIndex++
		if encoded.Kind != "service" || encoded.Ordinal != ordinal || len(encoded.Content) != descriptor.ContentBytes || rawDigest(encoded.Content) != descriptor.Digest {
			return invalidf("service member inventory")
		}
		var member ServiceMember
		if decodeCanonical(encoded.Content, &member) != nil || validateServiceMember(root, descriptor, member) != nil {
			return invalidf("service member %d", ordinal)
		}
		serviceView.Services = append(serviceView.Services, member.Services...)
		serviceView.Memberships = append(serviceView.Memberships, member.Memberships...)
	}
	serviceDispositions := make(map[string]string, len(serviceView.Services))
	for _, service := range serviceView.Services {
		serviceDispositions[service.Key] = service.Disposition
	}
	priorByPath := make(map[string]Placement, root.Paths)
	prior := make([]Placement, 0, root.Paths)
	for ordinal, descriptor := range root.PlacementMembers {
		encoded := generation.Members[memberIndex]
		memberIndex++
		if encoded.Kind != "placement" || encoded.Ordinal != ordinal || len(encoded.Content) != descriptor.ContentBytes || rawDigest(encoded.Content) != descriptor.Digest {
			return invalidf("placement member inventory")
		}
		var member PlacementMember
		if decodeCanonical(encoded.Content, &member) != nil || validatePlacementMember(root, descriptor, member) != nil {
			return invalidf("placement member %d", ordinal)
		}
		next := ""
		if ordinal+1 < len(root.PlacementMembers) {
			next = root.PlacementMembers[ordinal+1].First
		}
		if !reflect.DeepEqual(member.Inherited, inheritedForRange(member.FirstPath, next, prior)) {
			return invalidf("placement member %d inherited prelude", ordinal)
		}
		for _, placement := range member.Placements {
			if _, duplicate := priorByPath[placement.Path]; duplicate {
				return invalidf("duplicate placement %q", placement.Path)
			}
			priorByPath[placement.Path] = placement
			prior = append(prior, placement)
			if placement.Unowned != nil {
				placementView.Unowned = append(placementView.Unowned, *placement.Unowned)
			}
			for _, claim := range placement.Claims {
				if serviceDispositions[claim.ServiceKey] != claim.Disposition {
					return invalidf("placement claim disposition")
				}
				for _, role := range claim.Roles {
					placementView.Memberships = append(placementView.Memberships, servicecatalog.Membership{
						ServiceKey: claim.ServiceKey, Path: placement.Path, Role: role.Role, Origin: role.Origin,
					})
				}
			}
		}
	}
	serviceView.Unowned = slices.Clone(placementView.Unowned)
	serviceNormalized, serviceStats, err := normalize(serviceView)
	if err != nil {
		return err
	}
	placementView.Services = slices.Clone(serviceNormalized.Services)
	for index := range placementView.Services {
		placementView.Services[index].Successors = slices.Clone(serviceNormalized.Services[index].Successors)
	}
	placementNormalized, placementStats, err := normalize(placementView)
	if err != nil {
		return err
	}
	if !equalCatalog(serviceNormalized, placementNormalized) {
		return invalidf("service and placement views disagree")
	}
	logicalBytes, logicalDigest, err := logicalIdentity(serviceNormalized)
	if err != nil || logicalBytes > MaxLogicalBytes || logicalDigest != root.LogicalDigest {
		return invalidf("logical catalog digest")
	}
	if root.Services != len(serviceNormalized.Services) || root.Memberships != len(serviceNormalized.Memberships) ||
		root.Unowned != len(serviceNormalized.Unowned) || root.Dispositions != serviceStats.dispositions ||
		root.Roles != serviceStats.roles || root.Paths != serviceStats.paths || root.Successors != serviceStats.successors ||
		root.Claims != serviceStats.claims || serviceStats != placementStats {
		return invalidf("root logical totals")
	}
	v2Digest, v2Err := servicecatalog.Digest(serviceNormalized)
	if v2Err == nil && root.MappedV2Digest != v2Digest || v2Err != nil && root.MappedV2Digest != "" {
		return invalidf("mapped v2 digest")
	}
	if opened != nil {
		*opened = serviceNormalized
	}
	return nil
}

func validateServiceMember(root Root, descriptor MemberDescriptor, member ServiceMember) error {
	if member.Schema != ServiceMemberSchema || member.PolicyDigest != root.PolicyDigest || member.LogicalDigest != root.LogicalDigest ||
		member.Ordinal != descriptor.Ordinal || member.Count != descriptor.Count || len(member.Services) != descriptor.Records ||
		len(member.Memberships) != descriptor.Memberships || member.Services == nil || member.Memberships == nil ||
		member.Services[0].Key != descriptor.First || member.Services[len(member.Services)-1].Key != descriptor.Last {
		return ErrInvalid
	}
	for index := 1; index < len(member.Services); index++ {
		if member.Services[index].Key <= member.Services[index-1].Key {
			return ErrInvalid
		}
	}
	for _, membership := range member.Memberships {
		index, found := slices.BinarySearchFunc(member.Services, membership.ServiceKey, func(service servicecatalog.Service, target string) int { return strings.Compare(service.Key, target) })
		if !found || index < 0 {
			return ErrInvalid
		}
	}
	if !slices.IsSortedFunc(member.Memberships, compareMembership) {
		return ErrInvalid
	}
	return nil
}

func validatePlacementMember(root Root, descriptor MemberDescriptor, member PlacementMember) error {
	if member.Schema != PlacementMemberSchema || member.PolicyDigest != root.PolicyDigest || member.LogicalDigest != root.LogicalDigest ||
		member.Ordinal != descriptor.Ordinal || member.Count != descriptor.Count || len(member.Placements) != descriptor.Records ||
		member.Placements == nil || member.Inherited == nil || member.Placements[0].Path != descriptor.First ||
		member.Placements[len(member.Placements)-1].Path != descriptor.Last || countClaims(member.Placements) != descriptor.Claims ||
		countClaims(member.Inherited) != descriptor.PreludeClaims {
		return ErrInvalid
	}
	if !slices.IsSortedFunc(member.Placements, comparePlacement) || !slices.IsSortedFunc(member.Inherited, comparePlacement) {
		return ErrInvalid
	}
	for _, inherited := range member.Inherited {
		if inherited.Unowned != nil || len(inherited.Claims) == 0 {
			return ErrInvalid
		}
	}
	for _, placement := range append(slices.Clone(member.Inherited), member.Placements...) {
		if placement.Path == "" || placement.Unowned != nil && placement.Unowned.Path != placement.Path ||
			len(placement.Claims) > MaxClaimsPerPlacement || !slices.IsSortedFunc(placement.Claims, compareClaim) {
			return ErrInvalid
		}
		for _, claim := range placement.Claims {
			if len(claim.Roles) == 0 || !slices.IsSortedFunc(claim.Roles, compareClaimRole) {
				return ErrInvalid
			}
		}
	}
	return nil
}

func (generation Generation) Catalog() (servicecatalog.Catalog, error) {
	var catalog servicecatalog.Catalog
	if err := validateGeneration(generation, &catalog); err != nil {
		return servicecatalog.Catalog{}, err
	}
	return catalog, nil
}

func (generation Generation) ToV2() (servicecatalog.Catalog, error) {
	if generation.Root.MappedV2Digest == "" {
		return servicecatalog.Catalog{}, ErrNotV2Compatible
	}
	catalog, err := generation.Catalog()
	if err != nil {
		return servicecatalog.Catalog{}, err
	}
	digest, err := servicecatalog.Digest(catalog)
	if err != nil || digest != generation.Root.MappedV2Digest {
		return servicecatalog.Catalog{}, ErrNotV2Compatible
	}
	return catalog, nil
}

func (generation Generation) LookupService(key string) (servicecatalog.Service, []servicecatalog.Membership, error) {
	if err := ValidateRoot(generation.Root); err != nil {
		return servicecatalog.Service{}, nil, err
	}
	index := sort.Search(len(generation.Root.ServiceMembers), func(index int) bool { return generation.Root.ServiceMembers[index].Last >= key })
	if index >= len(generation.Root.ServiceMembers) || key < generation.Root.ServiceMembers[index].First {
		return servicecatalog.Service{}, nil, os.ErrNotExist
	}
	encodedIndex := index
	descriptor := generation.Root.ServiceMembers[index]
	if encodedIndex >= len(generation.Members) {
		return servicecatalog.Service{}, nil, ErrInvalid
	}
	encoded := generation.Members[encodedIndex]
	var member ServiceMember
	if encoded.Kind != "service" || rawDigest(encoded.Content) != descriptor.Digest || decodeCanonical(encoded.Content, &member) != nil || validateServiceMember(generation.Root, descriptor, member) != nil {
		return servicecatalog.Service{}, nil, ErrInvalid
	}
	serviceIndex, found := slices.BinarySearchFunc(member.Services, key, func(service servicecatalog.Service, target string) int { return strings.Compare(service.Key, target) })
	if !found {
		return servicecatalog.Service{}, nil, os.ErrNotExist
	}
	memberships := []servicecatalog.Membership{}
	for _, membership := range member.Memberships {
		if membership.ServiceKey == key {
			memberships = append(memberships, membership)
		}
	}
	return member.Services[serviceIndex], memberships, nil
}

func (generation Generation) LookupPath(target string) ([]Placement, error) {
	if err := ValidateRoot(generation.Root); err != nil {
		return nil, err
	}
	if len(generation.Root.PlacementMembers) == 0 {
		return nil, os.ErrNotExist
	}
	index := sort.Search(len(generation.Root.PlacementMembers), func(index int) bool { return generation.Root.PlacementMembers[index].First > target }) - 1
	if index < 0 {
		index = 0
	}
	descriptor := generation.Root.PlacementMembers[index]
	encodedIndex := len(generation.Root.ServiceMembers) + index
	if encodedIndex >= len(generation.Members) {
		return nil, ErrInvalid
	}
	encoded := generation.Members[encodedIndex]
	var member PlacementMember
	if encoded.Kind != "placement" || rawDigest(encoded.Content) != descriptor.Digest || decodeCanonical(encoded.Content, &member) != nil || validatePlacementMember(generation.Root, descriptor, member) != nil {
		return nil, ErrInvalid
	}
	result := []Placement{}
	for _, placement := range append(slices.Clone(member.Inherited), member.Placements...) {
		if target == placement.Path || strings.HasPrefix(target, placement.Path+"/") {
			result = append(result, placement)
		}
	}
	if len(result) == 0 {
		return nil, os.ErrNotExist
	}
	return result, nil
}

func validateBinding(binding Binding) error {
	identity := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: binding.Authority, Override: cloneOverride(binding.Override)}
	if reponame.Validate(binding.Repository) != nil || servicecatalog.Validate(identity) != nil ||
		binding.Source.FileCount < 0 || binding.Source.AcceptedFileCount < 0 || binding.Source.UnownedFileCount < 0 ||
		binding.Source.AcceptedFileCount+binding.Source.UnownedFileCount != binding.Source.FileCount || !validDigest(binding.Source.CensusDigest) || !fullGitID(binding.Source.Commit) {
		return invalidf("binding")
	}
	switch binding.Source.Kind {
	case servicecatalog.SourceCommitted, servicecatalog.SourceOperator:
		if !filepath.IsAbs(binding.Source.Path) || filepath.Clean(binding.Source.Path) != binding.Source.Path || binding.Source.LegacyDigest != "" {
			return invalidf("binding source path")
		}
	case servicecatalog.SourceAnalysisUnitV1:
		if binding.Source.Path != "" || !validDigest(binding.Source.LegacyDigest) {
			return invalidf("legacy binding source")
		}
	default:
		return invalidf("binding source kind")
	}
	if binding.Source.Kind == servicecatalog.SourceCommitted &&
		(binding.Authority.Kind != servicecatalog.AuthorityCommitted || binding.Authority.Version != binding.Source.Commit) {
		return invalidf("committed binding authority")
	}
	if binding.Source.Kind == servicecatalog.SourceOperator && binding.Authority.Kind != servicecatalog.AuthorityOperator {
		return invalidf("operator binding authority")
	}
	if binding.Source.Kind == servicecatalog.SourceAnalysisUnitV1 &&
		(binding.Authority.Kind != servicecatalog.AuthorityOperator || binding.Authority.ID != servicecatalog.AnalysisUnitV1AuthorityID || binding.Authority.Version != binding.Source.LegacyDigest) {
		return invalidf("legacy binding authority")
	}
	return nil
}

func fullGitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}

func compareMembership(left, right servicecatalog.Membership) int {
	if value := strings.Compare(left.ServiceKey, right.ServiceKey); value != 0 {
		return value
	}
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	if value := strings.Compare(left.Role, right.Role); value != 0 {
		return value
	}
	return strings.Compare(left.Origin, right.Origin)
}

func comparePlacement(left, right Placement) int { return strings.Compare(left.Path, right.Path) }
func compareClaim(left, right Claim) int         { return strings.Compare(left.ServiceKey, right.ServiceKey) }
func compareClaimRole(left, right ClaimRole) int {
	if value := strings.Compare(left.Role, right.Role); value != 0 {
		return value
	}
	return strings.Compare(left.Origin, right.Origin)
}
