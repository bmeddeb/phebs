package servicecatalogv3

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

// The reserve covers two maximally JSON-escaped 4-KiB placement range keys
// plus the fixed control fields before an ordinary record is appended.
const memberEnvelopeReserve = 32 << 10

// Build creates canonical root and member bytes without registering or
// publishing them anywhere.
func Build(binding Binding, catalog servicecatalog.Catalog) (Generation, error) {
	normalized, stats, err := normalize(catalog)
	if err != nil {
		return Generation{}, err
	}
	if binding.Authority != normalized.Authority || !equalOverride(binding.Override, normalized.Override) {
		return Generation{}, invalidf("binding authority disagrees with catalog")
	}
	if err := validateBinding(binding); err != nil {
		return Generation{}, err
	}
	logicalBytes, logicalDigest, err := logicalIdentity(normalized)
	if err != nil {
		return Generation{}, err
	}
	if logicalBytes > MaxLogicalBytes {
		return Generation{}, limitf("logical bytes")
	}
	mappedV2 := ""
	if value, digestErr := servicecatalog.Digest(normalized); digestErr == nil {
		mappedV2 = value
	}

	placements, err := makePlacements(normalized)
	if err != nil {
		return Generation{}, err
	}
	serviceGroups, err := packServiceGroups(normalized)
	if err != nil {
		return Generation{}, err
	}
	placementGroups, err := packPlacementGroups(placements, MaxMembers-len(serviceGroups))
	if err != nil {
		return Generation{}, err
	}
	if len(serviceGroups)+len(placementGroups) > MaxMembers {
		return Generation{}, limitf("member count")
	}

	root := Root{
		Schema: RootSchema, Binding: cloneBinding(binding), Policy: FrozenPolicy(), PolicyDigest: PolicyDigest(),
		LogicalDigest: logicalDigest, MappedV2Digest: mappedV2, Services: len(normalized.Services),
		Dispositions: stats.dispositions, Memberships: len(normalized.Memberships), Roles: stats.roles,
		Paths: stats.paths, Unowned: len(normalized.Unowned), Successors: stats.successors, Claims: stats.claims,
		ServiceMembers: []MemberDescriptor{}, PlacementMembers: []MemberDescriptor{},
	}
	generation := Generation{Members: make([]EncodedMember, 0, len(serviceGroups)+len(placementGroups))}
	for ordinal, group := range serviceGroups {
		member := ServiceMember{Schema: ServiceMemberSchema, PolicyDigest: root.PolicyDigest, LogicalDigest: logicalDigest,
			Ordinal: ordinal, Count: len(serviceGroups), FirstKey: group.services[0].Key,
			LastKey: group.services[len(group.services)-1].Key, Services: group.services, Memberships: group.memberships}
		raw, err := canonical(member)
		if err != nil || len(raw) > MaxMemberBytes {
			return Generation{}, limitf("service member bytes")
		}
		descriptor := MemberDescriptor{Kind: "service", Ordinal: ordinal, Count: len(serviceGroups), First: member.FirstKey,
			Last: member.LastKey, Records: len(member.Services), Memberships: len(member.Memberships),
			ContentBytes: len(raw), Digest: rawDigest(raw)}
		root.ServiceMembers = append(root.ServiceMembers, descriptor)
		generation.Members = append(generation.Members, EncodedMember{Kind: "service", Ordinal: ordinal, Content: raw})
		root.EncodedMemberBytes += len(raw)
	}
	for ordinal, group := range placementGroups {
		member := PlacementMember{Schema: PlacementMemberSchema, PolicyDigest: root.PolicyDigest, LogicalDigest: logicalDigest,
			Ordinal: ordinal, Count: len(placementGroups), FirstPath: group.placements[0].Path,
			LastPath: group.placements[len(group.placements)-1].Path, Inherited: group.inherited, Placements: group.placements}
		raw, err := canonical(member)
		if err != nil || len(raw) > MaxMemberBytes {
			return Generation{}, limitf("placement member bytes")
		}
		descriptor := MemberDescriptor{Kind: "placement", Ordinal: ordinal, Count: len(placementGroups), First: member.FirstPath,
			Last: member.LastPath, Records: len(member.Placements), Claims: countClaims(member.Placements),
			PreludeClaims: countClaims(member.Inherited), ContentBytes: len(raw), Digest: rawDigest(raw)}
		root.PlacementMembers = append(root.PlacementMembers, descriptor)
		generation.Members = append(generation.Members, EncodedMember{Kind: "placement", Ordinal: ordinal, Content: raw})
		root.EncodedMemberBytes += len(raw)
	}
	if root.EncodedMemberBytes >= MaxPublicationBytes {
		return Generation{}, limitf("encoded member bytes")
	}
	if err := finalizeRoot(&root); err != nil {
		return Generation{}, err
	}
	generation.Root = root
	if err := ValidateGeneration(generation); err != nil {
		return Generation{}, err
	}
	return generation, nil
}

type serviceGroup struct {
	services    []servicecatalog.Service
	memberships []servicecatalog.Membership
}

func packServiceGroups(catalog servicecatalog.Catalog) ([]serviceGroup, error) {
	if len(catalog.Services) == 0 {
		return nil, nil
	}
	byService := make(map[string][]servicecatalog.Membership, len(catalog.Services))
	for _, membership := range catalog.Memberships {
		byService[membership.ServiceKey] = append(byService[membership.ServiceKey], membership)
	}
	groups := []serviceGroup{{}}
	estimated := memberEnvelopeReserve
	for _, service := range catalog.Services {
		serviceBytes, err := json.Marshal(service)
		if err != nil {
			return nil, err
		}
		addition := len(serviceBytes) + 1
		for _, membership := range byService[service.Key] {
			raw, marshalErr := json.Marshal(membership)
			if marshalErr != nil {
				return nil, marshalErr
			}
			addition += len(raw) + 1
		}
		current := &groups[len(groups)-1]
		if len(current.services) > 0 && (len(current.services) >= MaxServicesPerMember || addition > MaxMemberBytes-estimated) {
			if len(groups) >= MaxMembers {
				return nil, limitf("service members")
			}
			groups = append(groups, serviceGroup{})
			current = &groups[len(groups)-1]
			estimated = memberEnvelopeReserve
		}
		if addition > MaxMemberBytes-estimated {
			return nil, limitf("one service member")
		}
		current.services = append(current.services, service)
		current.memberships = append(current.memberships, byService[service.Key]...)
		estimated += addition
	}
	return groups, nil
}

type placementGroup struct{ inherited, placements []Placement }

func packPlacementGroups(placements []Placement, maxGroups int) ([]placementGroup, error) {
	if len(placements) == 0 {
		return nil, nil
	}
	if maxGroups < 1 {
		return nil, limitf("placement members")
	}
	groups := make([]placementGroup, 0, min(maxGroups, (len(placements)+MaxPathsPerMember-1)/MaxPathsPerMember))
	prior := make([]Placement, 0, len(placements))
	for index := 0; index < len(placements); {
		first := placements[index].Path
		candidates, err := inheritedCandidates(first, prior)
		if err != nil {
			return nil, err
		}
		group := placementGroup{}
		ordinaryBytes, inheritedBytes, activated := 0, 0, 0
		acceptedNext := ""
		for index < len(placements) && len(group.placements) < MaxPathsPerMember {
			raw, err := json.Marshal(placements[index])
			if err != nil {
				return nil, err
			}
			next := ""
			if index+1 < len(placements) {
				next = placements[index+1].Path
			}
			for activated < len(candidates) && (next == "" || candidates[activated].first < next) {
				if candidates[activated].bytes > MaxMemberBytes-inheritedBytes {
					return nil, limitf("inherited claim prelude")
				}
				inheritedBytes += candidates[activated].bytes
				activated++
			}
			estimated := memberEnvelopeReserve + ordinaryBytes + len(raw) + 1 + inheritedBytes
			if len(group.placements) > 0 && estimated > MaxMemberBytes {
				break
			}
			if estimated > MaxMemberBytes {
				return nil, limitf("one placement member")
			}
			group.placements = append(group.placements, placements[index])
			ordinaryBytes += len(raw) + 1
			acceptedNext = next
			index++
		}
		group.inherited = inheritedForRange(first, acceptedNext, prior)
		if len(groups) >= maxGroups {
			return nil, limitf("placement members")
		}
		groups = append(groups, group)
		prior = append(prior, group.placements...)
	}
	return groups, nil
}

type inheritedCandidate struct {
	first string
	bytes int
}

func inheritedCandidates(first string, prior []Placement) ([]inheritedCandidate, error) {
	result := make([]inheritedCandidate, 0)
	for _, placement := range prior {
		if len(placement.Claims) == 0 || placement.Path+"0" <= first {
			continue
		}
		placement.Unowned = nil
		raw, err := json.Marshal(placement)
		if err != nil {
			return nil, err
		}
		result = append(result, inheritedCandidate{first: placement.Path + "/", bytes: len(raw) + 1})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].first < result[j].first })
	return result, nil
}

func makePlacements(catalog servicecatalog.Catalog) ([]Placement, error) {
	type claimKey struct{ path, service string }
	byPath := make(map[string]*Placement, len(catalog.Memberships)+len(catalog.Unowned))
	claims := make(map[claimKey]int, len(catalog.Memberships))
	disposition := make(map[string]string, len(catalog.Services))
	for _, service := range catalog.Services {
		disposition[service.Key] = service.Disposition
	}
	for _, membership := range catalog.Memberships {
		placement := byPath[membership.Path]
		if placement == nil {
			placement = &Placement{Path: membership.Path, Claims: []Claim{}}
			byPath[membership.Path] = placement
		}
		key := claimKey{path: membership.Path, service: membership.ServiceKey}
		claimIndex, exists := claims[key]
		if !exists {
			if len(placement.Claims) >= MaxClaimsPerPlacement {
				return nil, limitf("claims at %q", membership.Path)
			}
			placement.Claims = append(placement.Claims, Claim{ServiceKey: membership.ServiceKey, Disposition: disposition[membership.ServiceKey], Roles: []ClaimRole{}})
			claimIndex = len(placement.Claims) - 1
			claims[key] = claimIndex
		}
		placement.Claims[claimIndex].Roles = append(placement.Claims[claimIndex].Roles, ClaimRole{Role: membership.Role, Origin: membership.Origin})
	}
	for _, unowned := range catalog.Unowned {
		placement := byPath[unowned.Path]
		if placement == nil {
			placement = &Placement{Path: unowned.Path, Claims: []Claim{}}
			byPath[unowned.Path] = placement
		}
		copy := unowned
		placement.Unowned = &copy
	}
	result := make([]Placement, 0, len(byPath))
	for _, placement := range byPath {
		sort.Slice(placement.Claims, func(i, j int) bool { return placement.Claims[i].ServiceKey < placement.Claims[j].ServiceKey })
		for index := range placement.Claims {
			sort.Slice(placement.Claims[index].Roles, func(i, j int) bool {
				left, right := placement.Claims[index].Roles[i], placement.Claims[index].Roles[j]
				return left.Role < right.Role || left.Role == right.Role && left.Origin < right.Origin
			})
		}
		result = append(result, *placement)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func inheritedForRange(first, next string, prior []Placement) []Placement {
	result := make([]Placement, 0)
	for _, placement := range prior {
		if len(placement.Claims) == 0 || placement.Path+"0" <= first || next != "" && placement.Path+"/" >= next {
			continue
		}
		placement.Unowned = nil
		result = append(result, placement)
	}
	return result
}

func finalizeRoot(root *Root) error {
	root.Digest = "sha256:" + strings.Repeat("0", sha256.Size*2)
	for attempts := 0; attempts < 4; attempts++ {
		raw, err := canonical(*root)
		if err != nil {
			return err
		}
		root.RootBytes = len(raw)
		root.EncodedBytes = len(raw) + root.EncodedMemberBytes
	}
	if root.RootBytes > MaxRootBytes || root.EncodedBytes > MaxPublicationBytes {
		return limitf("root or publication bytes")
	}
	digest, err := RootDigest(*root)
	if err != nil {
		return err
	}
	root.Digest = digest
	raw, err := canonical(*root)
	if err != nil || len(raw) != root.RootBytes {
		return invalidf("unstable root byte count")
	}
	return nil
}

func countClaims(placements []Placement) int {
	total := 0
	for _, placement := range placements {
		total += len(placement.Claims)
	}
	return total
}

func cloneBinding(value Binding) Binding {
	value.Override = cloneOverride(value.Override)
	return value
}

func equalOverride(left, right *servicecatalog.OperatorOverride) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
