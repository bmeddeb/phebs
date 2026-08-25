package servicecatalogv3

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

type catalogStats struct {
	dispositions DispositionCounts
	roles        RoleCounts
	paths        int
	claims       int
	successors   int
}

// normalize retains the v2 record rules while applying only the expanded v3
// aggregate ceilings. Small v2 validation batches keep one implementation of
// field, role, per-service path, and authority semantics.
func normalize(catalog servicecatalog.Catalog) (servicecatalog.Catalog, catalogStats, error) {
	if catalog.Schema != servicecatalog.Schema || len(catalog.Services) > MaxTotalServices ||
		len(catalog.Memberships) > MaxMemberships || len(catalog.Unowned) > MaxDistinctPaths {
		return servicecatalog.Catalog{}, catalogStats{}, limitf("catalog aggregate count")
	}
	successors := 0
	for _, service := range catalog.Services {
		if len(service.Successors) > MaxServiceSuccessors || len(service.Successors) > MaxSuccessorEdges-successors {
			return servicecatalog.Catalog{}, catalogStats{}, limitf("successor edges")
		}
		successors += len(service.Successors)
	}
	normalized := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: catalog.Authority,
		Override: cloneOverride(catalog.Override),
		Services: slices.Clone(catalog.Services), Memberships: slices.Clone(catalog.Memberships),
		Unowned: slices.Clone(catalog.Unowned),
	}
	for index := range normalized.Services {
		normalized.Services[index].Successors = slices.Clone(normalized.Services[index].Successors)
		slices.Sort(normalized.Services[index].Successors)
	}
	sort.Slice(normalized.Services, func(i, j int) bool { return normalized.Services[i].Key < normalized.Services[j].Key })
	sort.Slice(normalized.Memberships, func(i, j int) bool {
		left, right := normalized.Memberships[i], normalized.Memberships[j]
		return left.ServiceKey < right.ServiceKey || left.ServiceKey == right.ServiceKey &&
			(left.Path < right.Path || left.Path == right.Path &&
				(left.Role < right.Role || left.Role == right.Role && left.Origin < right.Origin))
	})
	sort.Slice(normalized.Unowned, func(i, j int) bool {
		return normalized.Unowned[i].Path < normalized.Unowned[j].Path ||
			normalized.Unowned[i].Path == normalized.Unowned[j].Path && normalized.Unowned[i].Origin < normalized.Unowned[j].Origin
	})

	services := make(map[string]servicecatalog.Service, len(normalized.Services))
	for _, service := range normalized.Services {
		if _, duplicate := services[service.Key]; duplicate {
			return servicecatalog.Catalog{}, catalogStats{}, invalidf("duplicate service %q", service.Key)
		}
		services[service.Key] = service
	}

	byService := make(map[string][]servicecatalog.Membership, len(services))
	for _, membership := range normalized.Memberships {
		byService[membership.ServiceKey] = append(byService[membership.ServiceKey], membership)
	}
	// Thirty-one maximum-width services remain below every retained v2 batch
	// ceiling, so the established validator can be reused without weakening a
	// field or per-service invariant.
	for start := 0; start < len(normalized.Services); start += 31 {
		end := min(start+31, len(normalized.Services))
		batch := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: normalized.Authority, Override: cloneOverride(normalized.Override)}
		batch.Services = slices.Clone(normalized.Services[start:end])
		for index := range batch.Services {
			batch.Services[index].Successors = nil
			batch.Memberships = append(batch.Memberships, byService[batch.Services[index].Key]...)
		}
		if err := servicecatalog.Validate(batch); err != nil {
			return servicecatalog.Catalog{}, catalogStats{}, fmt.Errorf("%w: service batch: %v", ErrInvalid, err)
		}
	}
	if len(normalized.Services) == 0 {
		if err := servicecatalog.Validate(servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: normalized.Authority, Override: cloneOverride(normalized.Override)}); err != nil {
			return servicecatalog.Catalog{}, catalogStats{}, fmt.Errorf("%w: authority: %v", ErrInvalid, err)
		}
	}
	for start := 0; start < len(normalized.Unowned); start += servicecatalog.MaxDistinctPaths {
		end := min(start+servicecatalog.MaxDistinctPaths, len(normalized.Unowned))
		batch := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: normalized.Authority, Override: cloneOverride(normalized.Override), Unowned: slices.Clone(normalized.Unowned[start:end])}
		if err := servicecatalog.Validate(batch); err != nil {
			return servicecatalog.Catalog{}, catalogStats{}, fmt.Errorf("%w: unowned batch: %v", ErrInvalid, err)
		}
	}

	stats := catalogStats{}
	for _, service := range normalized.Services {
		if service.Disposition != servicecatalog.DispositionRejected && len(service.Successors) != 0 {
			return servicecatalog.Catalog{}, catalogStats{}, invalidf("non-rejected service %q has successors", service.Key)
		}
		switch service.Disposition {
		case servicecatalog.DispositionAccepted:
			stats.dispositions.Accepted++
		case servicecatalog.DispositionProposal:
			stats.dispositions.Proposal++
		case servicecatalog.DispositionConflict:
			stats.dispositions.Conflict++
		case servicecatalog.DispositionRejected:
			stats.dispositions.Rejected++
		}
		stats.successors += len(service.Successors)
	}
	if err := validateSuccessors(services); err != nil {
		return servicecatalog.Catalog{}, catalogStats{}, err
	}

	distinct := make(map[string]struct{}, min(MaxDistinctPaths, len(normalized.Memberships)+len(normalized.Unowned)))
	acceptedPaths := make(map[string]struct{})
	fanout := make(map[string]map[string]struct{})
	claims := make(map[string]struct{}, len(normalized.Memberships))
	for _, membership := range normalized.Memberships {
		service, exists := services[membership.ServiceKey]
		if !exists {
			return servicecatalog.Catalog{}, catalogStats{}, invalidf("membership references unknown service %q", membership.ServiceKey)
		}
		if _, seen := distinct[membership.Path]; !seen {
			if len(distinct) >= MaxDistinctPaths {
				return servicecatalog.Catalog{}, catalogStats{}, limitf("distinct paths")
			}
			distinct[membership.Path] = struct{}{}
		}
		claimKey := membership.ServiceKey + "\x00" + membership.Path
		claims[claimKey] = struct{}{}
		switch membership.Role {
		case servicecatalog.RolePrimary:
			stats.roles.Primary++
		case servicecatalog.RoleSupporting:
			stats.roles.Supporting++
		case servicecatalog.RoleShared:
			stats.roles.Shared++
		case servicecatalog.RoleGenerated:
			stats.roles.Generated++
		case servicecatalog.RoleTyped:
			stats.roles.Typed++
		}
		if service.Disposition == servicecatalog.DispositionAccepted {
			acceptedPaths[membership.Path] = struct{}{}
			set := fanout[membership.Path]
			if set == nil {
				set = make(map[string]struct{})
				fanout[membership.Path] = set
			}
			if _, exists := set[membership.ServiceKey]; !exists && len(set) >= servicecatalog.MaxAcceptedPathFanout {
				return servicecatalog.Catalog{}, catalogStats{}, limitf("accepted path fanout")
			}
			set[membership.ServiceKey] = struct{}{}
		}
	}
	stats.claims = len(claims)
	for _, set := range fanout {
		_ = set
	}
	unowned := make([]string, len(normalized.Unowned))
	for index, placement := range normalized.Unowned {
		unowned[index] = placement.Path
		if _, seen := distinct[placement.Path]; !seen {
			if len(distinct) >= MaxDistinctPaths {
				return servicecatalog.Catalog{}, catalogStats{}, limitf("distinct paths")
			}
			distinct[placement.Path] = struct{}{}
		}
	}
	for index := 1; index < len(unowned); index++ {
		if unowned[index] == unowned[index-1] || strings.HasPrefix(unowned[index], unowned[index-1]+"/") {
			return servicecatalog.Catalog{}, catalogStats{}, invalidf("unowned paths overlap")
		}
	}
	acceptedSorted := make([]string, 0, len(acceptedPaths))
	for value := range acceptedPaths {
		acceptedSorted = append(acceptedSorted, value)
	}
	slices.Sort(acceptedSorted)
	for _, value := range unowned {
		if _, exact := acceptedPaths[value]; exact {
			return servicecatalog.Catalog{}, catalogStats{}, invalidf("unowned path overlaps accepted membership")
		}
		for parent := path.Dir(value); parent != "."; parent = path.Dir(parent) {
			if _, exists := acceptedPaths[parent]; exists {
				return servicecatalog.Catalog{}, catalogStats{}, invalidf("unowned path overlaps accepted membership")
			}
		}
		prefix := value + "/"
		index, _ := slices.BinarySearch(acceptedSorted, prefix)
		if index < len(acceptedSorted) && strings.HasPrefix(acceptedSorted[index], prefix) {
			return servicecatalog.Catalog{}, catalogStats{}, invalidf("unowned path overlaps accepted membership")
		}
	}
	stats.paths = len(distinct)
	if stats.claims > MaxMemberships {
		return servicecatalog.Catalog{}, catalogStats{}, limitf("claims")
	}
	return normalized, stats, nil
}

func validateSuccessors(services map[string]servicecatalog.Service) error {
	indegree := make(map[string]int, len(services))
	adjacency := make(map[string][]string, len(services))
	for key := range services {
		indegree[key] = 0
	}
	for key, service := range services {
		seen := make(map[string]struct{}, len(service.Successors))
		for _, successor := range service.Successors {
			if successor == key {
				return invalidf("service %q is its own successor", key)
			}
			if _, duplicate := seen[successor]; duplicate {
				return invalidf("service %q repeats successor %q", key, successor)
			}
			seen[successor] = struct{}{}
			target, exists := services[successor]
			if !exists || target.Disposition != servicecatalog.DispositionAccepted && target.Disposition != servicecatalog.DispositionRejected {
				return invalidf("service %q has non-live successor %q", key, successor)
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
		key := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		visited++
		for _, successor := range adjacency[key] {
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

func logicalIdentity(catalog servicecatalog.Catalog) (int, string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("phebs-service-catalog-v3-logical\x00"))
	writer := &limitedWriter{limit: MaxLogicalBytes, writers: []io.Writer{h}}
	if err := writeLogical(writer, catalog); err != nil {
		return 0, "", err
	}
	return writer.written, "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

type limitedWriter struct {
	limit, written int
	writers        []io.Writer
}

func (writer *limitedWriter) Write(raw []byte) (int, error) {
	if len(raw) > writer.limit-writer.written {
		return 0, limitf("logical canonical bytes")
	}
	for _, destination := range writer.writers {
		if _, err := destination.Write(raw); err != nil {
			return 0, err
		}
	}
	writer.written += len(raw)
	return len(raw), nil
}

func writeLogical(writer io.Writer, catalog servicecatalog.Catalog) error {
	write := func(raw []byte) error { _, err := writer.Write(raw); return err }
	marshal := func(value any) ([]byte, error) { return json.Marshal(value) }
	if err := write([]byte(`{"schema":`)); err != nil {
		return err
	}
	raw, err := marshal(catalog.Schema)
	if err != nil {
		return err
	}
	if err = write(raw); err != nil {
		return err
	}
	if err = write([]byte(`,"authority":`)); err != nil {
		return err
	}
	raw, err = marshal(catalog.Authority)
	if err != nil {
		return err
	}
	if err = write(raw); err != nil {
		return err
	}
	if catalog.Override != nil {
		if err = write([]byte(`,"override":`)); err != nil {
			return err
		}
		raw, err = marshal(catalog.Override)
		if err != nil {
			return err
		}
		if err = write(raw); err != nil {
			return err
		}
	}
	if err = write([]byte(`,"services":[`)); err != nil {
		return err
	}
	for index, value := range catalog.Services {
		if index > 0 {
			if err = write([]byte{','}); err != nil {
				return err
			}
		}
		raw, err = marshal(value)
		if err != nil {
			return err
		}
		if err = write(raw); err != nil {
			return err
		}
	}
	if err = write([]byte(`],"memberships":[`)); err != nil {
		return err
	}
	for index, value := range catalog.Memberships {
		if index > 0 {
			if err = write([]byte{','}); err != nil {
				return err
			}
		}
		raw, err = marshal(value)
		if err != nil {
			return err
		}
		if err = write(raw); err != nil {
			return err
		}
	}
	if err = write([]byte(`],"unowned":[`)); err != nil {
		return err
	}
	for index, value := range catalog.Unowned {
		if index > 0 {
			if err = write([]byte{','}); err != nil {
				return err
			}
		}
		raw, err = marshal(value)
		if err != nil {
			return err
		}
		if err = write(raw); err != nil {
			return err
		}
	}
	return write([]byte(`]}`))
}

func ValidateRoot(root Root) error {
	if root.Schema != RootSchema || reponame.Validate(root.Binding.Repository) != nil || validateBinding(root.Binding) != nil ||
		root.PolicyDigest != PolicyDigest() || !reflect.DeepEqual(root.Policy, FrozenPolicy()) ||
		!validDigest(root.LogicalDigest) || root.MappedV2Digest != "" && !validDigest(root.MappedV2Digest) ||
		root.Services < 0 || root.Services > MaxTotalServices || root.Memberships < 0 || root.Memberships > MaxMemberships ||
		root.Paths < 0 || root.Paths > MaxDistinctPaths || root.Successors < 0 || root.Successors > MaxSuccessorEdges ||
		root.Claims < 0 || root.Claims > MaxMemberships || root.Unowned < 0 || root.Unowned > root.Paths ||
		len(root.ServiceMembers)+len(root.PlacementMembers) > MaxMembers || root.EncodedMemberBytes < 0 ||
		root.RootBytes < 1 || root.RootBytes > MaxRootBytes || root.EncodedBytes != root.RootBytes+root.EncodedMemberBytes ||
		root.EncodedBytes > MaxPublicationBytes || !validDigest(root.Digest) {
		return invalidf("root identity or bounds")
	}
	if root.Dispositions.Accepted+root.Dispositions.Proposal+root.Dispositions.Conflict+root.Dispositions.Rejected != root.Services ||
		root.Roles.Primary+root.Roles.Supporting+root.Roles.Shared+root.Roles.Generated+root.Roles.Typed != root.Memberships {
		return invalidf("root aggregate counts")
	}
	if err := validateDescriptors(root.ServiceMembers, "service", root.Services); err != nil {
		return err
	}
	if err := validateDescriptors(root.PlacementMembers, "placement", root.Paths); err != nil {
		return err
	}
	memberBytes, memberships, claims := 0, 0, 0
	for _, descriptor := range append(slices.Clone(root.ServiceMembers), root.PlacementMembers...) {
		memberBytes += descriptor.ContentBytes
	}
	for _, descriptor := range root.ServiceMembers {
		memberships += descriptor.Memberships
	}
	for _, descriptor := range root.PlacementMembers {
		claims += descriptor.Claims
	}
	if memberBytes != root.EncodedMemberBytes || memberships != root.Memberships || claims != root.Claims {
		return invalidf("root member totals")
	}
	raw, err := canonical(root)
	if err != nil || len(raw) != root.RootBytes {
		return invalidf("root byte count")
	}
	want, err := RootDigest(root)
	if err != nil || want != root.Digest {
		return invalidf("root digest")
	}
	return nil
}

func validateDescriptors(descriptors []MemberDescriptor, kind string, records int) error {
	if records == 0 && len(descriptors) != 0 || records > 0 && len(descriptors) == 0 {
		return invalidf("%s descriptor emptiness", kind)
	}
	total := 0
	previous := ""
	for ordinal, descriptor := range descriptors {
		maxRecords := MaxServicesPerMember
		if kind == "placement" {
			maxRecords = MaxPathsPerMember
		}
		if descriptor.Kind != kind || descriptor.Ordinal != ordinal || descriptor.Count != len(descriptors) ||
			descriptor.Records < 1 || descriptor.Records > maxRecords || descriptor.First == "" || descriptor.Last < descriptor.First ||
			ordinal > 0 && descriptor.First <= previous || descriptor.ContentBytes < 1 || descriptor.ContentBytes > MaxMemberBytes ||
			!validDigest(descriptor.Digest) || descriptor.Memberships < 0 || descriptor.Claims < 0 || descriptor.PreludeClaims < 0 {
			return invalidf("%s descriptor", kind)
		}
		total += descriptor.Records
		previous = descriptor.Last
	}
	if total != records {
		return invalidf("%s descriptor records", kind)
	}
	return nil
}

func cloneOverride(value *servicecatalog.OperatorOverride) *servicecatalog.OperatorOverride {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func equalCatalog(left, right servicecatalog.Catalog) bool { return reflect.DeepEqual(left, right) }
