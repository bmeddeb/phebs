package t411

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

const (
	logicalCatalogSchema = "t411-production-aligned-catalog-v2-semantics-v1"
	serviceMemberSchema  = "t411-service-range-member-estimate-v1"
	pathMemberSchema     = "t411-placement-range-member-estimate-v1"
	rootSchema           = "t411-catalog-root-estimate-v1"
	relationshipSchema   = "t411-relationship-distribution-v1"
)

type logicalCatalog struct {
	Schema      string                            `json:"schema"`
	Authority   servicecatalog.Authority          `json:"authority"`
	Services    []servicecatalog.Service          `json:"services"`
	Memberships []servicecatalog.Membership       `json:"memberships"`
	Unowned     []servicecatalog.UnownedPlacement `json:"unowned"`
}

type roleClaim struct {
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

type placementClaim struct {
	ServiceKey  string      `json:"service_key"`
	Disposition string      `json:"disposition"`
	Roles       []roleClaim `json:"roles"`
}

type pathRecord struct {
	Path    string           `json:"path"`
	Unowned bool             `json:"unowned"`
	Claims  []placementClaim `json:"claims"`
}

type serviceMember struct {
	Schema      string                      `json:"schema"`
	FirstKey    string                      `json:"first_key"`
	LastKey     string                      `json:"last_key"`
	Services    []servicecatalog.Service    `json:"services"`
	Memberships []servicecatalog.Membership `json:"memberships"`
}

type pathMember struct {
	Schema    string       `json:"schema"`
	FirstPath string       `json:"first_path"`
	LastPath  string       `json:"last_path"`
	Paths     []pathRecord `json:"paths"`
}

type publicationPolicy struct {
	ServicesPerMember   int `json:"services_per_member"`
	PathsPerMember      int `json:"paths_per_member"`
	MaxMembers          int `json:"max_members"`
	MaxRootBytes        int `json:"max_root_bytes"`
	MaxMemberBytes      int `json:"max_member_bytes"`
	MaxLogicalBytes     int `json:"max_logical_bytes"`
	MaxPublicationBytes int `json:"max_publication_bytes"`
}

type memberDescriptor struct {
	Kind    string `json:"kind"`
	First   string `json:"first"`
	Last    string `json:"last"`
	Records int    `json:"records"`
	Bytes   int    `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type publicationRoot struct {
	Schema               string             `json:"schema"`
	LogicalCatalogSHA256 string             `json:"logical_catalog_sha256"`
	PolicyDigest         string             `json:"policy_digest"`
	Services             int                `json:"services"`
	Memberships          int                `json:"memberships"`
	Paths                int                `json:"paths"`
	Members              []memberDescriptor `json:"members"`
}

type pathAccumulator struct {
	unowned bool
	claims  map[string]*placementClaim
}

type relationshipEdge struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
	Provider string `json:"provider"`
	Consumer string `json:"consumer"`
}

type countingHash struct {
	hash  hash.Hash
	bytes int64
}

func (writer *countingHash) Write(content []byte) (int, error) {
	written, err := writer.hash.Write(content)
	writer.bytes += int64(written)
	return written, err
}

func BuildEnvelope() (Envelope, error) {
	envelope, err := buildEnvelope()
	if err != nil {
		return Envelope{}, err
	}
	if err := validateEnvelopeShape(envelope); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func ValidateEnvelope(envelope Envelope) error {
	if err := validateEnvelopeShape(envelope); err != nil {
		return err
	}
	want, err := buildEnvelope()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(envelope, want) {
		return errors.New("T41.1 envelope differs from the frozen generator")
	}
	return nil
}

func ProfileDigest(profile Profile) (string, error) {
	content, err := MarshalCanonical(profile)
	if err != nil {
		return "", err
	}
	return SHA256(content), nil
}

func buildEnvelope() (Envelope, error) {
	profiles := make([]Profile, 0, 3)
	for _, services := range []int{AcceptedServiceFloor, AcceptedServiceTarget, MaxTotalServices} {
		profile, err := buildAcceptedProfile(services)
		if err != nil {
			return Envelope{}, err
		}
		profiles = append(profiles, profile)
	}
	transition, err := buildTransitionProfile()
	if err != nil {
		return Envelope{}, err
	}
	boundary, err := buildBoundaryProfile()
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Schema: EnvelopeSchema, Profiles: profiles, Transition: transition,
		Boundary: boundary, Claims: neutralClaims(),
	}, nil
}

func buildAcceptedProfile(serviceCount int) (Profile, error) {
	catalog, paths, membershipsByService, maxFanout, maxClaims, err := generateCatalog(serviceCount)
	if err != nil {
		return Profile{}, err
	}
	logicalBytes, err := json.Marshal(catalog)
	if err != nil {
		return Profile{}, err
	}
	logicalBytes = append(logicalBytes, '\n')
	fixture, err := fixtureIdentity(paths)
	if err != nil {
		return Profile{}, err
	}
	publication, err := projectPublication(catalog, paths, membershipsByService, logicalBytes)
	if err != nil {
		return Profile{}, err
	}
	relationships, err := relationshipIdentities(serviceCount)
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{
		Schema: ProfileSchema, Name: fmt.Sprintf("accepted-services-%d", serviceCount),
		Seed: "t411-neutral-service-load-v1",
		Authority: AuthorityRule{
			Kind: servicecatalog.AuthorityOperator, ID: "t411-neutral-authority",
			Version: fmt.Sprintf("accepted-%d-v1", serviceCount), Explicit: true,
		},
		AcceptedServices: serviceCount, TotalServiceRecords: serviceCount,
		Memberships: 6 * serviceCount, DistinctPaths: len(paths),
		UnownedPaths: serviceCount / 100,
		RoleMemberships: []Count{
			{Name: servicecatalog.RoleGenerated, Count: 2 * serviceCount},
			{Name: servicecatalog.RolePrimary, Count: serviceCount},
			{Name: servicecatalog.RoleShared, Count: serviceCount},
			{Name: servicecatalog.RoleSupporting, Count: serviceCount},
			{Name: servicecatalog.RoleTyped, Count: serviceCount},
		},
		MaxAcceptedPathFanout: maxFanout, MaxTotalClaimsPerPlacement: maxClaims,
		LogicalCatalog: ArtifactIdentity{Bytes: len(logicalBytes), SHA256: SHA256(logicalBytes)},
		Fixture:        fixture, Publication: publication, Relationships: relationships,
		Claims: neutralClaims(),
	}
	if err := validateProfile(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func generateCatalog(serviceCount int) (
	logicalCatalog,
	[]pathRecord,
	map[string][]servicecatalog.Membership,
	int,
	int,
	error,
) {
	if serviceCount != AcceptedServiceFloor && serviceCount != AcceptedServiceTarget && serviceCount != MaxTotalServices {
		return logicalCatalog{}, nil, nil, 0, 0, fmt.Errorf("service count %d is not a frozen T41.1 profile", serviceCount)
	}
	catalog := logicalCatalog{
		Schema: logicalCatalogSchema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "t411-neutral-authority",
			Version: fmt.Sprintf("accepted-%d-v1", serviceCount),
		},
		Services:    make([]servicecatalog.Service, 0, serviceCount),
		Memberships: make([]servicecatalog.Membership, 0, 6*serviceCount),
		Unowned:     make([]servicecatalog.UnownedPlacement, 0, serviceCount/100),
	}
	byPath := make(map[string]*pathAccumulator, 4*serviceCount)
	byService := make(map[string][]servicecatalog.Membership, serviceCount)
	addMembership := func(serviceKey, path, role string) error {
		if err := repopath.Validate(path); err != nil {
			return fmt.Errorf("profile path %q: %w", path, err)
		}
		membership := servicecatalog.Membership{
			ServiceKey: serviceKey, Path: path, Role: role, Origin: servicecatalog.OriginBase,
		}
		catalog.Memberships = append(catalog.Memberships, membership)
		byService[serviceKey] = append(byService[serviceKey], membership)
		pathValue := byPath[path]
		if pathValue == nil {
			pathValue = &pathAccumulator{claims: make(map[string]*placementClaim)}
			byPath[path] = pathValue
		}
		claim := pathValue.claims[serviceKey]
		if claim == nil {
			claim = &placementClaim{ServiceKey: serviceKey, Disposition: servicecatalog.DispositionAccepted}
			pathValue.claims[serviceKey] = claim
		}
		claim.Roles = append(claim.Roles, roleClaim{Role: role, Origin: servicecatalog.OriginBase})
		return nil
	}
	for index := range serviceCount {
		key := fmt.Sprintf("svc.load-%05d", index)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
		placements := []struct{ path, role string }{
			{fmt.Sprintf("services/service-%05d/main.go", index), servicecatalog.RolePrimary},
			{fmt.Sprintf("contracts/service-%05d/api.proto", index), servicecatalog.RoleSupporting},
			{fmt.Sprintf("contracts/service-%05d/api.proto", index), servicecatalog.RoleTyped},
			{fmt.Sprintf("shared/group-%04d/library.go", index/20), servicecatalog.RoleShared},
			{fmt.Sprintf("generated/service-%05d/client.pb.go", index), servicecatalog.RoleGenerated},
			{fmt.Sprintf("generated/shared/group-%04d/types.pb.go", index/10), servicecatalog.RoleGenerated},
		}
		for _, placement := range placements {
			if err := addMembership(key, placement.path, placement.role); err != nil {
				return logicalCatalog{}, nil, nil, 0, 0, err
			}
		}
	}
	for index := range serviceCount / 100 {
		path := fmt.Sprintf("tools/unowned-%04d.go", index)
		catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
			Path: path, Origin: servicecatalog.OriginBase,
		})
		byPath[path] = &pathAccumulator{unowned: true, claims: make(map[string]*placementClaim)}
	}
	sort.Slice(catalog.Memberships, func(i, j int) bool {
		left, right := catalog.Memberships[i], catalog.Memberships[j]
		if left.ServiceKey != right.ServiceKey {
			return left.ServiceKey < right.ServiceKey
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Role < right.Role
	})
	pathNames := make([]string, 0, len(byPath))
	for path := range byPath {
		pathNames = append(pathNames, path)
	}
	slices.Sort(pathNames)
	paths := make([]pathRecord, 0, len(pathNames))
	maxFanout, maxClaims := 0, 0
	for _, path := range pathNames {
		value := byPath[path]
		keys := make([]string, 0, len(value.claims))
		for key := range value.claims {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		record := pathRecord{Path: path, Unowned: value.unowned, Claims: make([]placementClaim, 0, len(keys))}
		for _, key := range keys {
			claim := *value.claims[key]
			slices.SortFunc(claim.Roles, func(left, right roleClaim) int {
				if left.Role != right.Role {
					return strings.Compare(left.Role, right.Role)
				}
				return strings.Compare(left.Origin, right.Origin)
			})
			record.Claims = append(record.Claims, claim)
		}
		maxFanout = max(maxFanout, len(record.Claims))
		maxClaims = max(maxClaims, len(record.Claims))
		paths = append(paths, record)
	}
	return catalog, paths, byService, maxFanout, maxClaims, nil
}

func fixtureIdentity(paths []pathRecord) (FixtureIdentity, error) {
	digest := sha256.New()
	var contentBytes int64
	var length [8]byte
	for _, record := range paths {
		content := []byte("t411-neutral-fixture-v1\n" + record.Path + "\n")
		binary.BigEndian.PutUint64(length[:], uint64(len(record.Path)))
		if _, err := digest.Write(length[:]); err != nil {
			return FixtureIdentity{}, err
		}
		if _, err := digest.Write([]byte(record.Path)); err != nil {
			return FixtureIdentity{}, err
		}
		binary.BigEndian.PutUint64(length[:], uint64(len(content)))
		if _, err := digest.Write(length[:]); err != nil {
			return FixtureIdentity{}, err
		}
		if _, err := digest.Write(content); err != nil {
			return FixtureIdentity{}, err
		}
		contentBytes += int64(len(content))
	}
	return FixtureIdentity{
		Algorithm:    "sha256-framed-path-and-t411-neutral-fixture-v1-content",
		RegularFiles: len(paths), DistinctContents: len(paths), ContentBytes: contentBytes,
		SHA256: "sha256:" + hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func projectPublication(
	catalog logicalCatalog,
	paths []pathRecord,
	byService map[string][]servicecatalog.Membership,
	logicalBytes []byte,
) (PublicationProjection, error) {
	policy := publicationPolicy{
		ServicesPerMember: MaxServicesPerMember, PathsPerMember: MaxPathsPerMember,
		MaxMembers: MaxCatalogMembers, MaxRootBytes: MaxCatalogRootBytes,
		MaxMemberBytes: MaxCatalogMemberBytes, MaxLogicalBytes: MaxLogicalBytes,
		MaxPublicationBytes: MaxPublicationBytes,
	}
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		return PublicationProjection{}, err
	}
	descriptors := make([]memberDescriptor, 0, MaxCatalogMembers)
	members := make([][]byte, 0, MaxCatalogMembers)
	serviceBytes, maxServiceBytes := 0, 0
	for start := 0; start < len(catalog.Services); start += MaxServicesPerMember {
		end := min(start+MaxServicesPerMember, len(catalog.Services))
		services := catalog.Services[start:end]
		memberships := make([]servicecatalog.Membership, 0, 6*len(services))
		for _, service := range services {
			memberships = append(memberships, byService[service.Key]...)
		}
		member := serviceMember{
			Schema: serviceMemberSchema, FirstKey: services[0].Key,
			LastKey: services[len(services)-1].Key, Services: services, Memberships: memberships,
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return PublicationProjection{}, err
		}
		raw = append(raw, '\n')
		if len(raw) > MaxCatalogMemberBytes {
			return PublicationProjection{}, fmt.Errorf("service member bytes %d exceed %d", len(raw), MaxCatalogMemberBytes)
		}
		members = append(members, raw)
		descriptors = append(descriptors, memberDescriptor{
			Kind: "service", First: member.FirstKey, Last: member.LastKey,
			Records: len(services), Bytes: len(raw), SHA256: SHA256(raw),
		})
		serviceBytes += len(raw)
		maxServiceBytes = max(maxServiceBytes, len(raw))
	}
	serviceMembers := len(descriptors)
	placementBytes, maxPlacementBytes := 0, 0
	for start := 0; start < len(paths); start += MaxPathsPerMember {
		end := min(start+MaxPathsPerMember, len(paths))
		member := pathMember{
			Schema: pathMemberSchema, FirstPath: paths[start].Path,
			LastPath: paths[end-1].Path, Paths: paths[start:end],
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return PublicationProjection{}, err
		}
		raw = append(raw, '\n')
		if len(raw) > MaxCatalogMemberBytes {
			return PublicationProjection{}, fmt.Errorf("placement member bytes %d exceed %d", len(raw), MaxCatalogMemberBytes)
		}
		members = append(members, raw)
		descriptors = append(descriptors, memberDescriptor{
			Kind: "placement", First: member.FirstPath, Last: member.LastPath,
			Records: len(member.Paths), Bytes: len(raw), SHA256: SHA256(raw),
		})
		placementBytes += len(raw)
		maxPlacementBytes = max(maxPlacementBytes, len(raw))
	}
	if len(descriptors) > MaxCatalogMembers {
		return PublicationProjection{}, fmt.Errorf("catalog members %d exceed %d", len(descriptors), MaxCatalogMembers)
	}
	root := publicationRoot{
		Schema: rootSchema, LogicalCatalogSHA256: SHA256(logicalBytes),
		PolicyDigest: SHA256(policyBytes), Services: len(catalog.Services),
		Memberships: len(catalog.Memberships), Paths: len(paths), Members: descriptors,
	}
	rootBytes, err := json.Marshal(root)
	if err != nil {
		return PublicationProjection{}, err
	}
	rootBytes = append(rootBytes, '\n')
	if len(rootBytes) > MaxCatalogRootBytes {
		return PublicationProjection{}, fmt.Errorf("catalog root bytes %d exceed %d", len(rootBytes), MaxCatalogRootBytes)
	}
	memberDigest := sha256.New()
	var length [8]byte
	for _, raw := range members {
		binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
		_, _ = memberDigest.Write(length[:])
		_, _ = memberDigest.Write(raw)
	}
	encodedBytes := len(rootBytes) + serviceBytes + placementBytes
	if encodedBytes > MaxPublicationBytes {
		return PublicationProjection{}, fmt.Errorf("publication bytes %d exceed %d", encodedBytes, MaxPublicationBytes)
	}
	return PublicationProjection{
		PolicyDigest:   root.PolicyDigest,
		Root:           ArtifactIdentity{Bytes: len(rootBytes), SHA256: SHA256(rootBytes)},
		ServiceMembers: serviceMembers, ServiceMemberBytes: serviceBytes,
		MaxServiceMemberBytes: maxServiceBytes,
		PlacementMembers:      len(descriptors) - serviceMembers,
		PlacementMemberBytes:  placementBytes, MaxPlacementMemberBytes: maxPlacementBytes,
		TotalMembers: len(descriptors), EncodedBytes: encodedBytes,
		MemberSetSHA256: "sha256:" + hex.EncodeToString(memberDigest.Sum(nil)),
	}, nil
}

func relationshipIdentities(serviceCount int) ([]RelationshipIdentity, error) {
	result := make([]RelationshipIdentity, 0, 3)
	for _, name := range []string{"empty", "mixed", "dense"} {
		identity, err := relationshipIdentity(name, serviceCount)
		if err != nil {
			return nil, err
		}
		result = append(result, identity)
	}
	return result, nil
}

func relationshipIdentity(name string, serviceCount int) (RelationshipIdentity, error) {
	writer := &countingHash{hash: sha256.New()}
	prefix, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Name   string `json:"name"`
	}{Schema: relationshipSchema, Name: name})
	if err != nil {
		return RelationshipIdentity{}, err
	}
	prefix = prefix[:len(prefix)-1]
	if _, err := writer.Write(append(prefix, []byte(`,"edges":[`)...)); err != nil {
		return RelationshipIdentity{}, err
	}
	edges := 0
	writeEdge := func(edge relationshipEdge) error {
		raw, err := json.Marshal(edge)
		if err != nil {
			return err
		}
		if edges != 0 {
			if _, err := writer.Write([]byte{','}); err != nil {
				return err
			}
		}
		_, err = writer.Write(raw)
		edges++
		return err
	}
	switch name {
	case "empty":
	case "mixed":
		for index := range serviceCount {
			kind := "rpc"
			if index%2 != 0 {
				kind = "kafka"
			}
			if err := writeEdge(relationshipEdge{
				Kind: kind, Identity: fmt.Sprintf("%s-%05d", kind, index),
				Provider: fmt.Sprintf("svc.load-%05d", index),
				Consumer: fmt.Sprintf("svc.load-%05d", (index+1)%serviceCount),
			}); err != nil {
				return RelationshipIdentity{}, err
			}
		}
	case "dense":
		for group := 0; group < serviceCount/20; group++ {
			for provider := 0; provider < 20; provider++ {
				for consumer := 0; consumer < 20; consumer++ {
					if provider == consumer {
						continue
					}
					providerIndex, consumerIndex := group*20+provider, group*20+consumer
					if err := writeEdge(relationshipEdge{
						Kind: "rpc", Identity: fmt.Sprintf("rpc-%05d-%02d", group, consumer),
						Provider: fmt.Sprintf("svc.load-%05d", providerIndex),
						Consumer: fmt.Sprintf("svc.load-%05d", consumerIndex),
					}); err != nil {
						return RelationshipIdentity{}, err
					}
				}
			}
		}
	default:
		return RelationshipIdentity{}, fmt.Errorf("unknown relationship distribution %q", name)
	}
	if _, err := writer.Write([]byte("]}\n")); err != nil {
		return RelationshipIdentity{}, err
	}
	return RelationshipIdentity{
		Name: name, Edges: edges, Bytes: writer.bytes,
		SHA256: "sha256:" + hex.EncodeToString(writer.hash.Sum(nil)),
	}, nil
}

func buildTransitionProfile() (TransitionProfile, error) {
	authority := func(version string) servicecatalog.Authority {
		return servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "t411-transition-authority", Version: version,
		}
	}
	service := func(key, disposition string) servicecatalog.Service {
		value := servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: disposition, Origin: servicecatalog.OriginBase,
		}
		if disposition != servicecatalog.DispositionAccepted {
			value.Reason = "t411-transition-case"
		}
		return value
	}
	alpha := service("svc.alpha", servicecatalog.DispositionAccepted)
	betaProposal := service("svc.beta", servicecatalog.DispositionProposal)
	betaAccepted := service("svc.beta", servicecatalog.DispositionAccepted)
	gamma := service("svc.gamma", servicecatalog.DispositionConflict)
	legacy := service("svc.legacy", servicecatalog.DispositionRejected)
	legacy.Successors = []string{"svc.alpha"}
	readd := service("svc.readd", servicecatalog.DispositionAccepted)
	memberships := func(includeBeta, includeReadd bool) []servicecatalog.Membership {
		keys := []string{"svc.alpha", "svc.gamma"}
		if includeBeta {
			keys = append(keys, "svc.beta")
		}
		if includeReadd {
			keys = append(keys, "svc.readd")
		}
		slices.Sort(keys)
		result := make([]servicecatalog.Membership, 0, len(keys))
		for _, key := range keys {
			result = append(result, servicecatalog.Membership{
				ServiceKey: key, Path: "services/" + strings.TrimPrefix(key, "svc.") + "/main.go",
				Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
			})
		}
		return result
	}
	r0 := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority("r0"),
		Services:    []servicecatalog.Service{alpha, betaProposal, gamma, legacy, readd},
		Memberships: memberships(true, true),
		Unowned:     []servicecatalog.UnownedPlacement{},
	}
	r1 := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority("r1"),
		Services:    []servicecatalog.Service{alpha, betaAccepted, gamma, legacy},
		Memberships: memberships(true, false),
		Unowned:     []servicecatalog.UnownedPlacement{},
	}
	r2 := r0
	r2.Authority = authority("r2-a-return")
	for _, catalog := range []servicecatalog.Catalog{r0, r1, r2} {
		if err := servicecatalog.Validate(catalog); err != nil {
			return TransitionProfile{}, fmt.Errorf("transition catalog: %w", err)
		}
		raw, err := json.Marshal(catalog)
		if err != nil {
			return TransitionProfile{}, fmt.Errorf("marshal transition catalog: %w", err)
		}
		if _, err := servicecatalog.Decode(raw); err != nil {
			return TransitionProfile{}, fmt.Errorf("decode transition catalog: %w", err)
		}
	}
	return TransitionProfile{
		Schema: TransitionSchema, Name: "authority-and-lifecycle-transitions-v1",
		Revisions: []TransitionRevision{
			{Name: "r0-a", Catalog: r0}, {Name: "r1-b", Catalog: r1},
			{Name: "r2-a-return", Catalog: r2},
		},
		Cases: []TransitionCase{
			{Name: "proposal-to-accepted", ServiceKey: "svc.beta", From: "proposal", To: "accepted"},
			{Name: "conflict-preserved", ServiceKey: "svc.gamma", From: "conflict", To: "conflict"},
			{Name: "rejected-successor", ServiceKey: "svc.legacy", From: "rejected", To: "rejected", Successors: []string{"svc.alpha"}},
			{Name: "omission-tombstone", ServiceKey: "svc.readd", From: "accepted", To: "removed", ExpectedIncarnation: 1},
			{Name: "readd-incarnation", ServiceKey: "svc.readd", From: "removed", To: "accepted", ExpectedIncarnation: 2},
			{Name: "a-b-a", ServiceKey: "svc.alpha", From: "accepted", To: "accepted", ExpectedIncarnation: 1},
		},
		Claims: neutralClaims(),
	}, nil
}

func buildBoundaryProfile() (BoundaryProfile, error) {
	maxService, err := maximumServiceBoundary()
	if err != nil {
		return BoundaryProfile{}, err
	}
	maxPlacement, err := maximumPlacementBoundary()
	if err != nil {
		return BoundaryProfile{}, err
	}
	return BoundaryProfile{MaxService: maxService, MaxPlacement: maxPlacement}, nil
}

func maximumServiceBoundary() (MaxServiceBoundary, error) {
	serviceKey := maximumKey("svc.boundary.service.", 0)
	successors := make([]string, 0, MaxServiceSuccessors)
	for index := range MaxServiceSuccessors {
		successors = append(successors, maximumKey("svc.boundary.successor.", index))
	}
	service := servicecatalog.Service{
		Key: serviceKey, DisplayName: strings.Repeat("d", servicecatalog.MaxDisplayNameBytes),
		Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase,
		Reason: strings.Repeat("r", servicecatalog.MaxReasonBytes), Successors: successors,
	}
	memberships := make([]servicecatalog.Membership, 0, servicecatalog.MaxServicePaths)
	pathBytes := 0
	for index := range servicecatalog.MaxServicePaths {
		prefix := fmt.Sprintf("boundary/%03d/", index)
		path := prefix + strings.Repeat("x", 200) + "/" + strings.Repeat("y", 200) + "/" +
			strings.Repeat("z", 512-len(prefix)-402)
		if err := repopath.Validate(path); err != nil {
			return MaxServiceBoundary{}, err
		}
		pathBytes += len(path)
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: serviceKey, Path: path, Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		})
	}
	member := serviceMember{
		Schema: serviceMemberSchema, FirstKey: serviceKey, LastKey: serviceKey,
		Services: []servicecatalog.Service{service}, Memberships: memberships,
	}
	raw, err := json.Marshal(member)
	if err != nil {
		return MaxServiceBoundary{}, err
	}
	raw = append(raw, '\n')
	return MaxServiceBoundary{
		ServiceKeyBytes: len(serviceKey), DisplayNameBytes: len(service.DisplayName),
		ReasonBytes: len(service.Reason), DistinctPaths: len(memberships), PathBytes: pathBytes,
		Successors: len(successors), Member: ArtifactIdentity{Bytes: len(raw), SHA256: SHA256(raw)},
	}, nil
}

func maximumPlacementBoundary() (MaxPlacementBoundary, error) {
	path := strings.Repeat(strings.Repeat("x", 200)+"/", 20) + strings.Repeat("x", 76)
	claims := make([]placementClaim, 0, MaxClaimsPerPlacement)
	relationshipClaims := make([]relationshippublication.ServiceClaim, 0, MaxClaimsPerPlacement)
	roles := []roleClaim{
		{Role: servicecatalog.RoleGenerated, Origin: servicecatalog.OriginOverride},
		{Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginOverride},
		{Role: servicecatalog.RoleShared, Origin: servicecatalog.OriginOverride},
		{Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginOverride},
		{Role: servicecatalog.RoleTyped, Origin: servicecatalog.OriginOverride},
	}
	for index := range MaxClaimsPerPlacement {
		disposition := servicecatalog.DispositionAccepted
		if index >= servicecatalog.MaxAcceptedPathFanout {
			disposition = []string{
				servicecatalog.DispositionProposal, servicecatalog.DispositionConflict,
				servicecatalog.DispositionRejected,
			}[(index-servicecatalog.MaxAcceptedPathFanout)%3]
		}
		claim := placementClaim{
			ServiceKey: maximumKey("svc.boundary.claim.", index), Disposition: disposition,
			Roles: slices.Clone(roles),
		}
		claims = append(claims, claim)
		relationshipRoles := make([]relationshippublication.RoleClaim, len(claim.Roles))
		for roleIndex, role := range claim.Roles {
			relationshipRoles[roleIndex] = relationshippublication.RoleClaim{Role: role.Role, Origin: role.Origin}
		}
		relationshipClaims = append(relationshipClaims, relationshippublication.ServiceClaim{
			ServiceKey: claim.ServiceKey, Disposition: claim.Disposition, Roles: relationshipRoles,
		})
	}
	member := pathMember{
		Schema: pathMemberSchema, FirstPath: path, LastPath: path,
		Paths: []pathRecord{{Path: path, Claims: claims}},
	}
	raw, err := json.Marshal(member)
	if err != nil {
		return MaxPlacementBoundary{}, err
	}
	raw = append(raw, '\n')
	unbucketed, err := marshalRelationshipProjection(path, relationshipClaims)
	if err != nil {
		return MaxPlacementBoundary{}, err
	}
	buckets, maxBucketBytes := 0, 0
	for start := 0; start < len(relationshipClaims); start += MaxClaimsPerBucket {
		end := min(start+MaxClaimsPerBucket, len(relationshipClaims))
		bucket, err := marshalRelationshipProjection(path, relationshipClaims[start:end])
		if err != nil {
			return MaxPlacementBoundary{}, err
		}
		buckets++
		maxBucketBytes = max(maxBucketBytes, len(bucket))
	}
	return MaxPlacementBoundary{
		PathBytes: len(path), Claims: len(claims), RolesPerClaim: len(roles),
		CatalogMember:               ArtifactIdentity{Bytes: len(raw), SHA256: SHA256(raw)},
		UnbucketedRelationshipBytes: len(unbucketed),
		ClaimsPerRelationshipBucket: MaxClaimsPerBucket,
		RelationshipBuckets:         buckets, MaxRelationshipBucketBytes: maxBucketBytes,
	}, nil
}

func marshalRelationshipProjection(
	path string,
	claims []relationshippublication.ServiceClaim,
) ([]byte, error) {
	digest := "sha256:" + strings.Repeat("0", 64)
	projection := relationshippublication.Projection{
		Schema: relationshippublication.ProjectionSchema, Kind: "rpc", PostingDigest: digest,
		Class:     strings.Repeat("c", relationshippublication.MaxTextBytes),
		Plane:     strings.Repeat("p", relationshippublication.MaxTextBytes),
		LookupKey: strings.Repeat("l", relationshippublication.MaxTextBytes),
		Source:    relationshippublication.Placement{Path: path, Claims: claims}, Digest: digest,
	}
	projection.Target = &relationshippublication.Placement{Path: path, Claims: claims}
	return json.Marshal(projection)
}

func maximumKey(prefix string, index int) string {
	suffix := fmt.Sprintf("%05d", index)
	return prefix + strings.Repeat("x", servicecatalog.MaxServiceKeyBytes-len(prefix)-len(suffix)) + suffix
}

func validateEnvelopeShape(envelope Envelope) error {
	if envelope.Schema != EnvelopeSchema || len(envelope.Profiles) != 3 ||
		envelope.Profiles[0].AcceptedServices != AcceptedServiceFloor ||
		envelope.Profiles[1].AcceptedServices != AcceptedServiceTarget ||
		envelope.Profiles[2].AcceptedServices != MaxTotalServices ||
		!validClaims(envelope.Claims) {
		return errors.New("T41.1 envelope is invalid")
	}
	for _, profile := range envelope.Profiles {
		if err := validateProfile(profile); err != nil {
			return err
		}
	}
	if envelope.Transition.Schema != TransitionSchema || len(envelope.Transition.Revisions) != 3 ||
		len(envelope.Transition.Cases) != 6 || !validClaims(envelope.Transition.Claims) {
		return errors.New("T41.1 transition profile is invalid")
	}
	for _, revision := range envelope.Transition.Revisions {
		if revision.Name == "" || servicecatalog.Validate(revision.Catalog) != nil {
			return errors.New("T41.1 transition revision is invalid")
		}
	}
	boundary := envelope.Boundary
	if boundary.MaxService.ServiceKeyBytes != servicecatalog.MaxServiceKeyBytes ||
		boundary.MaxService.DisplayNameBytes != servicecatalog.MaxDisplayNameBytes ||
		boundary.MaxService.ReasonBytes != servicecatalog.MaxReasonBytes ||
		boundary.MaxService.DistinctPaths != servicecatalog.MaxServicePaths ||
		boundary.MaxService.PathBytes != servicecatalog.MaxServicePathBytes ||
		boundary.MaxService.Successors != MaxServiceSuccessors ||
		boundary.MaxService.Member.Bytes > MaxCatalogMemberBytes ||
		boundary.MaxPlacement.PathBytes != repopath.MaxBytes ||
		boundary.MaxPlacement.Claims != MaxClaimsPerPlacement ||
		boundary.MaxPlacement.RolesPerClaim != relationshippublication.MaxRolesPerClaim ||
		boundary.MaxPlacement.CatalogMember.Bytes > MaxCatalogMemberBytes ||
		boundary.MaxPlacement.UnbucketedRelationshipBytes <= relationshippublication.MaxProjectionBytes ||
		boundary.MaxPlacement.ClaimsPerRelationshipBucket != MaxClaimsPerBucket ||
		boundary.MaxPlacement.RelationshipBuckets != 8 ||
		boundary.MaxPlacement.MaxRelationshipBucketBytes > relationshippublication.MaxProjectionBytes {
		return errors.New("T41.1 maximum-shape boundary is invalid")
	}
	return nil
}

func validateProfile(profile Profile) error {
	if profile.Schema != ProfileSchema || profile.Name == "" || profile.Seed != "t411-neutral-service-load-v1" ||
		!profile.Authority.Explicit || profile.Authority.Inferred ||
		profile.Authority.Kind != servicecatalog.AuthorityOperator || profile.Authority.ID == "" ||
		profile.AcceptedServices != profile.TotalServiceRecords ||
		(profile.AcceptedServices != AcceptedServiceFloor && profile.AcceptedServices != AcceptedServiceTarget &&
			profile.AcceptedServices != MaxTotalServices) ||
		profile.Memberships != 6*profile.AcceptedServices ||
		profile.DistinctPaths != 316*profile.AcceptedServices/100 ||
		profile.UnownedPaths != profile.AcceptedServices/100 ||
		profile.MaxAcceptedPathFanout != servicecatalog.MaxAcceptedPathFanout ||
		profile.MaxTotalClaimsPerPlacement != servicecatalog.MaxAcceptedPathFanout ||
		!validClaims(profile.Claims) {
		return fmt.Errorf("T41.1 profile %q envelope is invalid", profile.Name)
	}
	wantRoles := []Count{
		{Name: servicecatalog.RoleGenerated, Count: 2 * profile.AcceptedServices},
		{Name: servicecatalog.RolePrimary, Count: profile.AcceptedServices},
		{Name: servicecatalog.RoleShared, Count: profile.AcceptedServices},
		{Name: servicecatalog.RoleSupporting, Count: profile.AcceptedServices},
		{Name: servicecatalog.RoleTyped, Count: profile.AcceptedServices},
	}
	if !slices.Equal(profile.RoleMemberships, wantRoles) ||
		profile.Fixture.Algorithm != "sha256-framed-path-and-t411-neutral-fixture-v1-content" ||
		profile.Fixture.RegularFiles != profile.DistinctPaths ||
		profile.Fixture.DistinctContents != profile.DistinctPaths || profile.Fixture.ContentBytes < 1 ||
		!validDigest(profile.Fixture.SHA256) || !validDigest(profile.LogicalCatalog.SHA256) ||
		profile.LogicalCatalog.Bytes < 1 {
		return fmt.Errorf("T41.1 profile %q identities are invalid", profile.Name)
	}
	for name, observed := range map[string]int{
		"total_service_records": profile.TotalServiceRecords,
		"memberships":           profile.Memberships,
		"distinct_paths":        profile.DistinctPaths,
		"logical_bytes":         profile.LogicalCatalog.Bytes,
		"publication_bytes":     profile.Publication.EncodedBytes,
	} {
		if err := CheckLimit(name, observed); err != nil {
			return err
		}
	}
	if err := validatePublication(profile.Publication); err != nil {
		return err
	}
	if len(profile.Relationships) != 3 || profile.Relationships[0].Name != "empty" ||
		profile.Relationships[0].Edges != 0 || profile.Relationships[1].Name != "mixed" ||
		profile.Relationships[1].Edges != profile.AcceptedServices ||
		profile.Relationships[2].Name != "dense" ||
		profile.Relationships[2].Edges != 19*profile.AcceptedServices {
		return fmt.Errorf("T41.1 profile %q relationship distributions are invalid", profile.Name)
	}
	for _, relationship := range profile.Relationships {
		if relationship.Bytes < 1 || !validDigest(relationship.SHA256) {
			return fmt.Errorf("T41.1 profile %q relationship identity is invalid", profile.Name)
		}
	}
	if profile.Fixture.RegularFiles > 50_000 {
		return fmt.Errorf("T41.1 profile %q exceeds the small physical corpus", profile.Name)
	}
	return nil
}

func validatePublication(value PublicationProjection) error {
	if !validDigest(value.PolicyDigest) || value.Root.Bytes < 1 || value.Root.Bytes > MaxCatalogRootBytes ||
		!validDigest(value.Root.SHA256) || value.ServiceMembers < 1 || value.PlacementMembers < 1 ||
		value.TotalMembers != value.ServiceMembers+value.PlacementMembers || value.TotalMembers > MaxCatalogMembers ||
		value.ServiceMemberBytes < 1 || value.PlacementMemberBytes < 1 ||
		value.MaxServiceMemberBytes < 1 || value.MaxServiceMemberBytes > MaxCatalogMemberBytes ||
		value.MaxPlacementMemberBytes < 1 || value.MaxPlacementMemberBytes > MaxCatalogMemberBytes ||
		value.EncodedBytes != value.Root.Bytes+value.ServiceMemberBytes+value.PlacementMemberBytes ||
		value.EncodedBytes > MaxPublicationBytes || !validDigest(value.MemberSetSHA256) {
		return errors.New("T41.1 publication projection is invalid")
	}
	return nil
}

func neutralClaims() Claims {
	return Claims{Synthetic: true, SourceFree: true, ExplicitAuthority: true}
}

func validClaims(claims Claims) bool {
	return claims.Synthetic && claims.SourceFree && claims.ExplicitAuthority &&
		!claims.EstablishesTargetSLO && !claims.EstablishesAccuracy &&
		!claims.QualifiesSupportedScale && !claims.ChangesProductionBehavior &&
		!claims.AuthorizesRelease
}
