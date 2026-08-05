// Package servicecatalog defines the closed, repository-local service catalog
// contract selected by T33.1. It is deliberately pure: ingestion, repository
// binding, and publication belong to later tickets.
package servicecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	Schema = "phebs-service-catalog-v2"

	MaxEncodedBytes       = 5 << 20
	MaxCanonicalBytes     = 5 << 20
	MaxServices           = 4_000
	MaxMemberships        = 20_000
	MaxDistinctPaths      = 12_000
	MaxAcceptedPathFanout = 20
	MaxSuccessorEdges     = MaxServices

	MaxServicePaths     = analysisunit.MaxSelectedPaths
	MaxServicePathBytes = analysisunit.MaxSelectedPathBytes
	MaxServiceKeyBytes  = analysisunit.MaxUnitNameBytes
	MaxDisplayNameBytes = analysisunit.MaxUnitNameBytes
	MaxAuthorityIDBytes = analysisunit.MaxUnitNameBytes
	MaxVersionBytes     = analysisunit.MaxUnitNameBytes
	MaxReasonBytes      = 512
)

const (
	AuthorityCommitted = "committed"
	AuthorityOperator  = "operator"

	OriginBase     = "base"
	OriginOverride = "override"

	DispositionAccepted = "accepted"
	DispositionProposal = "proposal"
	DispositionConflict = "conflict"
	DispositionRejected = "rejected"

	RolePrimary    = "primary"
	RoleSupporting = "supporting"
	RoleShared     = "shared"
	RoleGenerated  = "generated"
	RoleTyped      = "typed"
)

var (
	ErrInvalid           = errors.New("invalid service catalog")
	ErrEncodedLimit      = errors.New("service catalog encoded-byte limit exceeded")
	ErrCanonicalLimit    = errors.New("service catalog canonical-byte limit exceeded")
	ErrServiceLimit      = errors.New("service catalog service limit exceeded")
	ErrMembershipLimit   = errors.New("service catalog membership limit exceeded")
	ErrDistinctPathLimit = errors.New("service catalog distinct-path limit exceeded")
	ErrFanoutLimit       = errors.New("service catalog accepted path fan-out limit exceeded")
	ErrSuccessorLimit    = errors.New("service catalog successor limit exceeded")

	serviceKeyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	versionRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+/@-]*$`)
)

// Catalog is one complete repository-local service authority projection.
// Membership paths are repository placement identities, not service identity.
type Catalog struct {
	Schema      string             `json:"schema"`
	Authority   Authority          `json:"authority"`
	Override    *OperatorOverride  `json:"override,omitempty"`
	Services    []Service          `json:"services"`
	Memberships []Membership       `json:"memberships"`
	Unowned     []UnownedPlacement `json:"unowned"`
}

// Authority is the one selected base authority. A committed authority version
// is an exact full Git object ID; an operator authority version is an opaque,
// bounded version token interpreted only by the later repository binding.
type Authority struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

// OperatorOverride identifies the optional selected operator override. Records
// derived from it carry origin "override" explicitly.
type OperatorOverride struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Service is one stable logical service key and its authority disposition.
// Successors are explicit key-replacement edges and are legal only on rejected
// records; incarnation and lifecycle state are intentionally deferred to T33.3.
type Service struct {
	Key         string   `json:"key"`
	DisplayName string   `json:"display_name"`
	Disposition string   `json:"disposition"`
	Origin      string   `json:"origin"`
	Reason      string   `json:"reason,omitempty"`
	Successors  []string `json:"successors,omitempty"`
}

// Membership is one normalized (service key, path, role) record. Roles are
// nonexclusive, so exact path reuse across roles and services is intentional.
type Membership struct {
	ServiceKey string `json:"service_key"`
	Path       string `json:"path"`
	Role       string `json:"role"`
	Origin     string `json:"origin"`
}

// UnownedPlacement is one explicit member of the repository complement. T33.2
// binds this projection to a complete source census and proves the complement.
type UnownedPlacement struct {
	Path   string `json:"path"`
	Origin string `json:"origin"`
}

// Validate applies the complete structural and semantic T33.1 contract without
// mutating catalog.
func Validate(catalog Catalog) error {
	_, err := normalize(catalog)
	return err
}

// Normalize validates catalog and returns its deterministic ordering without
// mutating the input slices.
func Normalize(catalog Catalog) (Catalog, error) {
	return normalize(catalog)
}

// Canonical returns the closed deterministic JSON identity for catalog.
func Canonical(catalog Catalog) ([]byte, error) {
	normalized, err := normalize(catalog)
	if err != nil {
		return nil, err
	}
	return appendCanonical(nil, normalized)
}

// Digest returns the domain-separated semantic identity of the canonical
// catalog. Publication revisions and service incarnations remain separate ABA
// fences owned by later tickets.
func Digest(catalog Catalog) (string, error) {
	canonical, err := Canonical(catalog)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-catalog-v2\x00"))
	_, _ = hash.Write(canonical)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func normalize(catalog Catalog) (Catalog, error) {
	if catalog.Schema != Schema {
		return Catalog{}, invalidf("schema must be %q", Schema)
	}
	if err := ValidateAuthority(catalog.Authority); err != nil {
		return Catalog{}, err
	}
	if catalog.Override != nil {
		if err := validateAuthorityToken("override id", catalog.Override.ID, MaxAuthorityIDBytes); err != nil {
			return Catalog{}, err
		}
		if err := validateVersion("override version", catalog.Override.Version); err != nil {
			return Catalog{}, err
		}
	}
	if len(catalog.Services) > MaxServices {
		return Catalog{}, ErrServiceLimit
	}
	if len(catalog.Memberships) > MaxMemberships {
		return Catalog{}, ErrMembershipLimit
	}
	if len(catalog.Unowned) > MaxDistinctPaths {
		return Catalog{}, ErrDistinctPathLimit
	}
	successorCount := 0
	for _, service := range catalog.Services {
		if len(service.Successors) > MaxSuccessorEdges-successorCount {
			return Catalog{}, ErrSuccessorLimit
		}
		successorCount += len(service.Successors)
	}

	normalized := Catalog{
		Schema:      Schema,
		Authority:   catalog.Authority,
		Override:    cloneOverride(catalog.Override),
		Services:    make([]Service, len(catalog.Services)),
		Memberships: slices.Clone(catalog.Memberships),
		Unowned:     slices.Clone(catalog.Unowned),
	}
	services := make(map[string]Service, len(catalog.Services))
	accepted := make(map[string]struct{}, len(catalog.Services))
	for index, service := range catalog.Services {
		service.Successors = slices.Clone(service.Successors)
		normalized.Services[index] = service
		if err := validateService(service, catalog.Override != nil); err != nil {
			return Catalog{}, fmt.Errorf("%w: service %d: %v", ErrInvalid, index, err)
		}
		if _, duplicate := services[service.Key]; duplicate {
			return Catalog{}, invalidf("duplicate service key %q", service.Key)
		}
		services[service.Key] = service
		if service.Disposition == DispositionAccepted {
			accepted[service.Key] = struct{}{}
		}
	}

	servicePaths := make(map[string]map[string]struct{}, len(services))
	servicePathBytes := make(map[string]int, len(services))
	primary := make(map[string]bool, len(services))
	typed := make(map[string]bool)
	supporting := make(map[string]bool)
	membershipTriples := make(map[string]struct{}, len(catalog.Memberships))
	distinctPaths := make(map[string]struct{}, min(MaxDistinctPaths, len(catalog.Memberships)+len(catalog.Unowned)))
	acceptedFanout := make(map[string]map[string]struct{})
	acceptedPaths := make(map[string]struct{})

	for index, membership := range catalog.Memberships {
		service, exists := services[membership.ServiceKey]
		if !exists {
			return Catalog{}, invalidf("membership %d references unknown service %q", index, membership.ServiceKey)
		}
		if err := validateOrigin(membership.Origin, catalog.Override != nil); err != nil {
			return Catalog{}, fmt.Errorf("%w: membership %d: %v", ErrInvalid, index, err)
		}
		if !validRole(membership.Role) {
			return Catalog{}, invalidf("membership %d has unsupported role %q", index, membership.Role)
		}
		if err := repopath.Validate(membership.Path); err != nil {
			return Catalog{}, invalidf("membership %d has unsafe path %q", index, membership.Path)
		}
		triple := membership.ServiceKey + "\x00" + membership.Path + "\x00" + membership.Role
		if _, duplicate := membershipTriples[triple]; duplicate {
			return Catalog{}, invalidf("duplicate membership (%q, %q, %q)", membership.ServiceKey, membership.Path, membership.Role)
		}
		membershipTriples[triple] = struct{}{}
		paths := servicePaths[membership.ServiceKey]
		if paths == nil {
			paths = make(map[string]struct{})
			servicePaths[membership.ServiceKey] = paths
		}
		if _, seen := paths[membership.Path]; !seen {
			if len(paths) >= MaxServicePaths {
				return Catalog{}, invalidf("service %q exceeds %d distinct paths", membership.ServiceKey, MaxServicePaths)
			}
			if servicePathBytes[membership.ServiceKey]+len(membership.Path) > MaxServicePathBytes {
				return Catalog{}, invalidf("service %q exceeds %d path bytes", membership.ServiceKey, MaxServicePathBytes)
			}
			paths[membership.Path] = struct{}{}
			servicePathBytes[membership.ServiceKey] += len(membership.Path)
		}
		if _, seen := distinctPaths[membership.Path]; !seen {
			if len(distinctPaths) >= MaxDistinctPaths {
				return Catalog{}, ErrDistinctPathLimit
			}
			distinctPaths[membership.Path] = struct{}{}
		}
		pair := membership.ServiceKey + "\x00" + membership.Path
		switch membership.Role {
		case RolePrimary:
			primary[membership.ServiceKey] = true
		case RoleTyped:
			typed[pair] = true
		case RoleSupporting:
			supporting[pair] = true
		}
		if service.Disposition == DispositionAccepted {
			acceptedPaths[membership.Path] = struct{}{}
			fanout := acceptedFanout[membership.Path]
			if fanout == nil {
				fanout = make(map[string]struct{})
				acceptedFanout[membership.Path] = fanout
			}
			if _, seen := fanout[membership.ServiceKey]; !seen && len(fanout) >= MaxAcceptedPathFanout {
				return Catalog{}, ErrFanoutLimit
			}
			fanout[membership.ServiceKey] = struct{}{}
		}
	}

	for key := range accepted {
		if !primary[key] {
			return Catalog{}, invalidf("accepted service %q has no primary membership", key)
		}
	}
	for pair := range typed {
		if !supporting[pair] {
			return Catalog{}, invalidf("typed membership %q has no exact supporting membership", pair)
		}
	}
	for serviceKey, paths := range servicePaths {
		if parent, child, overlap := prefixOverlap(paths); overlap {
			return Catalog{}, invalidf("service %q has overlapping paths %q and %q", serviceKey, parent, child)
		}
	}

	unownedPaths := make(map[string]struct{}, len(catalog.Unowned))
	for index, unowned := range catalog.Unowned {
		if err := validateOrigin(unowned.Origin, catalog.Override != nil); err != nil {
			return Catalog{}, fmt.Errorf("%w: unowned path %d: %v", ErrInvalid, index, err)
		}
		if err := repopath.Validate(unowned.Path); err != nil {
			return Catalog{}, invalidf("unowned path %d is unsafe: %q", index, unowned.Path)
		}
		if _, duplicate := unownedPaths[unowned.Path]; duplicate {
			return Catalog{}, invalidf("duplicate unowned path %q", unowned.Path)
		}
		unownedPaths[unowned.Path] = struct{}{}
		if _, seen := distinctPaths[unowned.Path]; !seen {
			if len(distinctPaths) >= MaxDistinctPaths {
				return Catalog{}, ErrDistinctPathLimit
			}
			distinctPaths[unowned.Path] = struct{}{}
		}
	}
	if parent, child, overlap := prefixOverlap(unownedPaths); overlap {
		return Catalog{}, invalidf("unowned paths overlap at %q and %q", parent, child)
	}
	if unownedPath, acceptedPath, overlap := overlapBetween(unownedPaths, acceptedPaths); overlap {
		return Catalog{}, invalidf("unowned path %q overlaps accepted membership %q", unownedPath, acceptedPath)
	}
	if err := validateSuccessors(services); err != nil {
		return Catalog{}, err
	}

	sort.Slice(normalized.Services, func(i, j int) bool {
		return normalized.Services[i].Key < normalized.Services[j].Key
	})
	for index := range normalized.Services {
		if len(normalized.Services[index].Successors) == 0 {
			normalized.Services[index].Successors = nil
		} else {
			slices.Sort(normalized.Services[index].Successors)
		}
	}
	sort.Slice(normalized.Memberships, func(i, j int) bool {
		left, right := normalized.Memberships[i], normalized.Memberships[j]
		return left.ServiceKey < right.ServiceKey || left.ServiceKey == right.ServiceKey &&
			(left.Path < right.Path || left.Path == right.Path &&
				(left.Role < right.Role || left.Role == right.Role && left.Origin < right.Origin))
	})
	sort.Slice(normalized.Unowned, func(i, j int) bool {
		left, right := normalized.Unowned[i], normalized.Unowned[j]
		return left.Path < right.Path || left.Path == right.Path && left.Origin < right.Origin
	})
	return normalized, nil
}

// ValidateAuthority validates the selected base identity without requiring a
// complete catalog. T33.2 uses it to reject invalid explicit configuration
// before reading an operator file or repository blob.
func ValidateAuthority(authority Authority) error {
	if authority.Kind != AuthorityCommitted && authority.Kind != AuthorityOperator {
		return invalidf("authority kind must be %q or %q", AuthorityCommitted, AuthorityOperator)
	}
	if err := validateAuthorityToken("authority id", authority.ID, MaxAuthorityIDBytes); err != nil {
		return err
	}
	if authority.Kind == AuthorityCommitted {
		if !isFullGitObjectID(authority.Version) {
			return invalidf("committed authority version must be a lowercase 40- or 64-digit Git object ID")
		}
		return nil
	}
	return validateVersion("authority version", authority.Version)
}

func validateService(service Service, hasOverride bool) error {
	if !validServiceKey(service.Key) {
		return errors.New("key must be a 128-byte-or-shorter analysis-unit token")
	}
	if !validBoundedText(service.DisplayName, MaxDisplayNameBytes) {
		return fmt.Errorf("display name must be nonempty valid text of at most %d bytes", MaxDisplayNameBytes)
	}
	if err := validateOrigin(service.Origin, hasOverride); err != nil {
		return err
	}
	switch service.Disposition {
	case DispositionAccepted:
		if service.Reason != "" {
			return errors.New("accepted service must omit reason")
		}
		if len(service.Successors) != 0 {
			return errors.New("accepted service must omit successors")
		}
	case DispositionProposal, DispositionConflict:
		if !validBoundedText(service.Reason, MaxReasonBytes) {
			return fmt.Errorf("%s service requires a reason of at most %d bytes", service.Disposition, MaxReasonBytes)
		}
		if len(service.Successors) != 0 {
			return fmt.Errorf("%s service must omit successors", service.Disposition)
		}
	case DispositionRejected:
		if !validBoundedText(service.Reason, MaxReasonBytes) {
			return fmt.Errorf("rejected service requires a reason of at most %d bytes", MaxReasonBytes)
		}
	default:
		return fmt.Errorf("unsupported disposition %q", service.Disposition)
	}
	return nil
}

func validateSuccessors(services map[string]Service) error {
	edgeCount := 0
	indegree := make(map[string]int, len(services))
	adjacency := make(map[string][]string, len(services))
	for key := range services {
		indegree[key] = 0
	}
	for key, service := range services {
		seen := make(map[string]struct{}, len(service.Successors))
		for _, successor := range service.Successors {
			edgeCount++
			if edgeCount > MaxSuccessorEdges {
				return ErrSuccessorLimit
			}
			if !validServiceKey(successor) {
				return invalidf("service %q has invalid successor %q", key, successor)
			}
			if successor == key {
				return invalidf("service %q is its own successor", key)
			}
			if _, duplicate := seen[successor]; duplicate {
				return invalidf("service %q repeats successor %q", key, successor)
			}
			seen[successor] = struct{}{}
			if _, exists := services[successor]; !exists {
				return invalidf("service %q references unknown successor %q", key, successor)
			}
			target := services[successor]
			if target.Disposition != DispositionAccepted && target.Disposition != DispositionRejected {
				return invalidf("service %q successor %q is neither accepted nor rejected", key, successor)
			}
			adjacency[key] = append(adjacency[key], successor)
			indegree[successor]++
		}
	}
	queue := make([]string, 0, len(services))
	for key, degree := range indegree {
		if degree == 0 {
			queue = append(queue, key)
		}
	}
	visited := 0
	for len(queue) > 0 {
		current := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		visited++
		for _, successor := range adjacency[current] {
			indegree[successor]--
			if indegree[successor] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	if visited != len(services) {
		return invalidf("successor graph contains a cycle")
	}
	return nil
}

func validateOrigin(origin string, hasOverride bool) error {
	switch origin {
	case OriginBase:
		return nil
	case OriginOverride:
		if !hasOverride {
			return errors.New("override origin requires selected override authority")
		}
		return nil
	default:
		return fmt.Errorf("unsupported origin %q", origin)
	}
}

func validateAuthorityToken(label, value string, limit int) error {
	if !validBoundedText(value, limit) || !versionRE.MatchString(value) {
		return invalidf("%s must be a nonempty %d-byte-or-shorter version token", label, limit)
	}
	return nil
}

func validateVersion(label, value string) error {
	return validateAuthorityToken(label, value, MaxVersionBytes)
}

func validServiceKey(value string) bool {
	return len(value) <= MaxServiceKeyBytes && utf8.ValidString(value) && serviceKeyRE.MatchString(value)
}

func validBoundedText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if current < 0x20 || current == 0x7f {
			return false
		}
	}
	return true
}

func validRole(role string) bool {
	switch role {
	case RolePrimary, RoleSupporting, RoleShared, RoleGenerated, RoleTyped:
		return true
	default:
		return false
	}
}

func prefixOverlap(paths map[string]struct{}) (string, string, bool) {
	for current := range paths {
		for parent := path.Dir(current); parent != "."; parent = path.Dir(parent) {
			if _, exists := paths[parent]; exists {
				return parent, current, true
			}
		}
	}
	return "", "", false
}

func overlapBetween(left, right map[string]struct{}) (string, string, bool) {
	rightSorted := make([]string, 0, len(right))
	for value := range right {
		rightSorted = append(rightSorted, value)
	}
	slices.Sort(rightSorted)
	for value := range left {
		if _, exact := right[value]; exact {
			return value, value, true
		}
		for parent := path.Dir(value); parent != "."; parent = path.Dir(parent) {
			if _, exists := right[parent]; exists {
				return value, parent, true
			}
		}
		prefix := value + "/"
		index, _ := slices.BinarySearch(rightSorted, prefix)
		if index < len(rightSorted) && strings.HasPrefix(rightSorted[index], prefix) {
			return value, rightSorted[index], true
		}
	}
	return "", "", false
}

func isFullGitObjectID(value string) bool {
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

func cloneOverride(value *OperatorOverride) *OperatorOverride {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func appendCanonical(out []byte, catalog Catalog) ([]byte, error) {
	var err error
	grow := func(length int) bool {
		if err != nil {
			return false
		}
		needed := len(out) + length
		if needed > MaxCanonicalBytes {
			err = ErrCanonicalLimit
			return false
		}
		if cap(out) < needed {
			capacity := max(256, cap(out)*2)
			if capacity < needed {
				capacity = needed
			}
			if capacity > MaxCanonicalBytes {
				capacity = MaxCanonicalBytes
			}
			grown := make([]byte, len(out), capacity)
			copy(grown, out)
			out = grown
		}
		return true
	}
	appendRaw := func(value string) {
		if !grow(len(value)) {
			return
		}
		out = append(out, value...)
	}
	appendString := func(value string) {
		if err != nil {
			return
		}
		encoded, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			err = fmt.Errorf("%w: encode canonical string: %v", ErrInvalid, encodeErr)
			return
		}
		if !grow(len(encoded)) {
			return
		}
		out = append(out, encoded...)
	}

	appendRaw(`{"schema":`)
	appendString(catalog.Schema)
	appendRaw(`,"authority":{"kind":`)
	appendString(catalog.Authority.Kind)
	appendRaw(`,"id":`)
	appendString(catalog.Authority.ID)
	appendRaw(`,"version":`)
	appendString(catalog.Authority.Version)
	appendRaw(`}`)
	if catalog.Override != nil {
		appendRaw(`,"override":{"id":`)
		appendString(catalog.Override.ID)
		appendRaw(`,"version":`)
		appendString(catalog.Override.Version)
		appendRaw(`}`)
	}
	appendRaw(`,"services":[`)
	for index, service := range catalog.Services {
		if index != 0 {
			appendRaw(`,`)
		}
		appendRaw(`{"key":`)
		appendString(service.Key)
		appendRaw(`,"display_name":`)
		appendString(service.DisplayName)
		appendRaw(`,"disposition":`)
		appendString(service.Disposition)
		appendRaw(`,"origin":`)
		appendString(service.Origin)
		if service.Reason != "" {
			appendRaw(`,"reason":`)
			appendString(service.Reason)
		}
		if len(service.Successors) != 0 {
			appendRaw(`,"successors":[`)
			for successorIndex, successor := range service.Successors {
				if successorIndex != 0 {
					appendRaw(`,`)
				}
				appendString(successor)
			}
			appendRaw(`]`)
		}
		appendRaw(`}`)
	}
	appendRaw(`],"memberships":[`)
	for index, membership := range catalog.Memberships {
		if index != 0 {
			appendRaw(`,`)
		}
		appendRaw(`{"service_key":`)
		appendString(membership.ServiceKey)
		appendRaw(`,"path":`)
		appendString(membership.Path)
		appendRaw(`,"role":`)
		appendString(membership.Role)
		appendRaw(`,"origin":`)
		appendString(membership.Origin)
		appendRaw(`}`)
	}
	appendRaw(`],"unowned":[`)
	for index, unowned := range catalog.Unowned {
		if index != 0 {
			appendRaw(`,`)
		}
		appendRaw(`{"path":`)
		appendString(unowned.Path)
		appendRaw(`,"origin":`)
		appendString(unowned.Origin)
		appendRaw(`}`)
	}
	appendRaw(`]}`)
	if err != nil {
		return nil, err
	}
	return out, nil
}
