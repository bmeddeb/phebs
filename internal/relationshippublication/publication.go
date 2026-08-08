package relationshippublication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/reponame"
)

var ErrServiceUnavailable = errors.New("service relationship partition unavailable")

type Publication struct {
	base       string
	directory  string
	rootValue  Root
	pointer    Pointer
	pointerRaw []byte
}

func (prepared *Prepared) Root() Root {
	if prepared == nil {
		return Root{}
	}
	return cloneRoot(prepared.rootValue)
}

func cloneRoot(value Root) Root {
	value.RepositoryMembers = slices.Clone(value.RepositoryMembers)
	value.Services = slices.Clone(value.Services)
	if value.Authority.Upstream != nil {
		upstream := *value.Authority.Upstream
		upstream.Required = slices.Clone(upstream.Required)
		upstream.Domains = slices.Clone(upstream.Domains)
		value.Authority.Upstream = &upstream
	}
	return value
}

func writePublicationStage(
	ctx context.Context,
	root string,
	authority Authority,
	authorityDigest string,
	accumulator *buildAccumulator,
) (*Prepared, error) {
	return writePublicationStageVersioned(
		ctx, root, RootSchema, authority, authorityDigest, nil, accumulator,
	)
}

func writePublicationStageVersioned(
	ctx context.Context,
	root string,
	rootSchema string,
	authority Authority,
	authorityDigest string,
	prior *Publication,
	accumulator *buildAccumulator,
) (*Prepared, error) {
	directory, err := stageDirectory(root, authority.Repository)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(directory)
		}
	}()
	value := Root{
		Schema: rootSchema, Authority: authority, AuthorityDigest: authorityDigest,
		Policy: FrozenPolicy(), RepositoryComplete: true,
		RepositoryMembers: []MemberReceipt{}, Services: []ServiceReceipt{},
		ProjectionCount: accumulator.projectionCount,
	}
	buckets := make([]int, 0, len(accumulator.repository))
	for bucket := range accumulator.repository {
		buckets = append(buckets, bucket)
	}
	slices.Sort(buckets)
	for _, bucket := range buckets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		projections := accumulator.repository[bucket]
		slices.SortFunc(projections, func(left, right Projection) int {
			return strings.Compare(left.Digest, right.Digest)
		})
		memberSchema, memberAuthority := RepositoryMemberSchema, authorityDigest
		if rootSchema == RootSchemaV2 {
			memberSchema = RepositoryMemberSchemaV2
			memberAuthority, err = repositoryPartitionAuthority(bucket, projections)
			if err != nil {
				return nil, err
			}
		}
		member := RepositoryMember{
			Schema: memberSchema, AuthorityDigest: memberAuthority,
			Bucket: bucket, Projections: projections,
		}
		member.Digest, err = digestValue(RepositoryMember{
			Schema: member.Schema, AuthorityDigest: member.AuthorityDigest,
			Bucket: member.Bucket, Projections: member.Projections,
		})
		if err != nil || validateRepositoryMember(member) != nil {
			return nil, fmt.Errorf("%w: repository member", ErrInvalid)
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxRepositoryMemberBytes || int64(len(raw)) > MaxGenerationBytes-value.EncodedRepositoryBytes {
			return nil, ErrLimit
		}
		name := repositoryMemberName(bucket)
		if !reuseRelationshipMember(prior, name, raw, memberPath(directory, name)) {
			if err := writeExclusive(memberPath(directory, name), raw); err != nil {
				return nil, err
			}
		}
		value.RepositoryMembers = append(value.RepositoryMembers, MemberReceipt{
			Bucket: bucket, Name: name, RecordCount: len(projections),
			ContentBytes: int64(len(raw)), ContentDigest: member.Digest,
		})
		value.EncodedRepositoryBytes += int64(len(raw))
	}

	for _, key := range sortedServiceKeys(accumulator.services) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		service := accumulator.services[key]
		receipt := ServiceReceipt{
			ServiceKey: key, Incarnation: service.state.Incarnation,
			ServiceGeneration: service.state.DesiredGeneration,
		}
		value.ServiceCount++
		if service.failed {
			receipt.State, receipt.Reason = "failed", service.reason
			value.FailedServiceCount++
			value.Services = append(value.Services, receipt)
			continue
		}
		slices.SortFunc(service.refs, func(left, right ServiceReference) int {
			return strings.Compare(left.Digest, right.Digest)
		})
		memberSchema, memberAuthority := ServiceMemberSchema, authorityDigest
		if rootSchema == RootSchemaV2 {
			memberSchema = ServiceMemberSchemaV2
			memberAuthority, err = servicePartitionAuthority(
				key, service.state.Incarnation, service.state.DesiredGeneration, service.refs,
			)
			if err != nil {
				return nil, err
			}
		}
		member := ServiceMember{
			Schema: memberSchema, AuthorityDigest: memberAuthority,
			ServiceKey: key, Incarnation: service.state.Incarnation,
			ServiceGeneration: service.state.DesiredGeneration,
			References:        service.refs,
		}
		member.Digest, err = digestValue(ServiceMember{
			Schema: member.Schema, AuthorityDigest: member.AuthorityDigest,
			ServiceKey: member.ServiceKey, Incarnation: member.Incarnation,
			ServiceGeneration: member.ServiceGeneration, References: member.References,
		})
		if err != nil || validateServiceMember(member) != nil {
			return nil, fmt.Errorf("%w: service member", ErrInvalid)
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxServiceMemberBytes ||
			int64(len(raw)) > MaxGenerationBytes-value.EncodedRepositoryBytes-value.EncodedServiceBytes {
			receipt.State, receipt.Reason = "failed", "encoded_limit"
			value.FailedServiceCount++
			value.Services = append(value.Services, receipt)
			continue
		}
		name := serviceMemberName(key)
		if !reuseRelationshipMember(prior, name, raw, memberPath(directory, name)) {
			if err := writeExclusive(memberPath(directory, name), raw); err != nil {
				return nil, err
			}
		}
		receipt.Name, receipt.ReferenceCount = name, len(service.refs)
		receipt.ContentBytes, receipt.ContentDigest = int64(len(raw)), member.Digest
		if len(service.refs) == 0 {
			receipt.State = "empty"
			value.EmptyServiceCount++
		} else {
			receipt.State = "complete"
			value.CompleteServiceCount++
		}
		value.ServiceReferenceCount += len(service.refs)
		value.EncodedServiceBytes += int64(len(raw))
		value.Services = append(value.Services, receipt)
	}
	value.AllServicesComplete = value.FailedServiceCount == 0
	value.GenerationDigest, err = generationDigest(value.Schema, authorityDigest)
	if err != nil {
		return nil, err
	}
	value.Digest, err = rootDigest(value)
	if err != nil || validateRoot(value) != nil {
		return nil, fmt.Errorf("%w: root", ErrInvalid)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxRootBytes {
		return nil, ErrLimit
	}
	if err := writeExclusive(filepath.Join(directory, "root.json"), raw); err != nil {
		return nil, err
	}
	if err := syncDirectory(directory); err != nil {
		return nil, err
	}
	prepared := &Prepared{root: root, repository: authority.Repository, directory: directory, rootValue: value}
	if _, err := openDirectoryComplete(ctx, directory, value); err != nil {
		return nil, err
	}
	failed = false
	return prepared, nil
}

func (prepared *Prepared) Publish(ctx context.Context) (*Publication, error) {
	if prepared == nil || prepared.closed {
		return nil, errors.New("relationship publication stage is closed")
	}
	base := repositoryRoot(prepared.root, prepared.repository)
	target := generationPath(prepared.root, prepared.repository, prepared.rootValue.GenerationDigest)
	if _, err := os.Lstat(target); err == nil {
		if _, openErr := openDirectoryComplete(ctx, target, prepared.rootValue); openErr != nil {
			return nil, fmt.Errorf("existing relationship generation conflicts: %w", openErr)
		}
		if err := os.RemoveAll(prepared.directory); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if err := os.Rename(prepared.directory, target); err != nil {
		return nil, err
	}
	prepared.closed = true
	if err := syncDirectory(base); err != nil {
		return nil, err
	}
	pointer, err := newPointer(prepared.rootValue)
	if err != nil {
		return nil, err
	}
	marker := Marker{Schema: MarkerSchema, Pointer: pointer}
	marker.Digest, err = digestValue(Marker{Schema: marker.Schema, Pointer: marker.Pointer})
	if err != nil {
		return nil, err
	}
	markerRaw, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	if err := replaceFile(filepath.Join(base, "publishing.json"), markerRaw); err != nil {
		return nil, err
	}
	if _, err := openDirectoryComplete(ctx, target, prepared.rootValue); err != nil {
		return nil, err
	}
	pointerRaw, err := json.Marshal(pointer)
	if err != nil {
		return nil, err
	}
	if err := replaceFile(filepath.Join(base, "current.json"), pointerRaw); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(base, UnavailableName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Remove(filepath.Join(base, "publishing.json")); err != nil {
		return nil, err
	}
	if err := syncDirectory(base); err != nil {
		return nil, err
	}
	publication := &Publication{
		base: base, directory: target, rootValue: prepared.rootValue,
		pointer: pointer, pointerRaw: pointerRaw,
	}
	return publication, nil
}

func Recover(ctx context.Context, root, repository string) (bool, error) {
	base := repositoryRoot(root, repository)
	if _, unavailable, unavailableErr := readUnavailable(root, repository); unavailableErr != nil {
		return false, unavailableErr
	} else if unavailable {
		if err := os.Remove(filepath.Join(base, "current.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		// The unavailable marker was installed after any surviving publication
		// marker and therefore wins recovery ordering. Never resurrect the older
		// generation across restart.
		if err := os.Remove(filepath.Join(base, "publishing.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return false, syncDirectory(base)
	}
	raw, err := readRegular(filepath.Join(base, "publishing.json"), MaxRootBytes)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var marker Marker
	if err := decodeExact(raw, MaxRootBytes, &marker); err != nil || validateMarker(marker) != nil ||
		marker.Pointer.Repository != repository {
		return false, fmt.Errorf("%w: publishing marker", ErrInvalid)
	}
	directory := generationPath(root, repository, marker.Pointer.GenerationDigest)
	rootValue, err := readRoot(directory, marker.Pointer)
	if err != nil {
		return false, err
	}
	if _, err := openDirectoryComplete(ctx, directory, rootValue); err != nil {
		return false, err
	}
	pointerRaw, _ := json.Marshal(marker.Pointer)
	if err := replaceFile(filepath.Join(base, "current.json"), pointerRaw); err != nil {
		return false, err
	}
	if err := os.Remove(filepath.Join(base, UnavailableName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.Remove(filepath.Join(base, "publishing.json")); err != nil {
		return false, err
	}
	return true, syncDirectory(base)
}

func OpenCurrent(ctx context.Context, root, repository string) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base := repositoryRoot(root, repository)
	if _, unavailable, err := readUnavailable(root, repository); err != nil {
		return nil, err
	} else if unavailable {
		return nil, ErrNotFound
	}
	raw, err := readRegular(filepath.Join(base, "current.json"), MaxRootBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var pointer Pointer
	if err := decodeExact(raw, MaxRootBytes, &pointer); err != nil || validatePointer(pointer) != nil ||
		pointer.Repository != repository {
		return nil, fmt.Errorf("%w: current pointer", ErrInvalid)
	}
	directory := generationPath(root, repository, pointer.GenerationDigest)
	rootValue, err := readRoot(directory, pointer)
	if err != nil {
		return nil, err
	}
	return &Publication{
		base: base, directory: directory, rootValue: rootValue,
		pointer: pointer, pointerRaw: raw,
	}, nil
}

// OpenGeneration opens one exact immutable relationship generation without
// consulting the mutable current pointer. Callers that expose the result must
// hold a Cache lease for the same generation so lifecycle collection cannot
// retire its bytes while a cursor, comparison, or citation still references
// them.
func OpenGeneration(
	ctx context.Context,
	root, repository, generation, rootDigestValue string,
) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reponame.Validate(repository) != nil || !validDigest(generation) ||
		!validDigest(rootDigestValue) {
		return nil, fmt.Errorf("%w: generation lookup", ErrInvalid)
	}
	directory := generationPath(root, repository, generation)
	raw, err := readRegular(filepath.Join(directory, "root.json"), MaxRootBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var value Root
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil || validateRoot(value) != nil ||
		value.Authority.Repository != repository || value.GenerationDigest != generation ||
		value.Digest != rootDigestValue {
		return nil, fmt.Errorf("%w: generation root", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: generation root canonical bytes", ErrInvalid)
	}
	return &Publication{directory: directory, rootValue: value}, nil
}

func (publication *Publication) Root() Root {
	if publication == nil {
		return Root{}
	}
	value := publication.rootValue
	value.RepositoryMembers = slices.Clone(value.RepositoryMembers)
	value.Services = slices.Clone(value.Services)
	return value
}

func (publication *Publication) OpenService(
	ctx context.Context,
	serviceKey string,
) (ServiceReceipt, *ServiceMember, error) {
	receipt, member, err := publication.ReadService(ctx, serviceKey)
	if err != nil {
		return receipt, member, err
	}
	if err := publication.ConfirmCurrent(); err != nil {
		return ServiceReceipt{}, nil, err
	}
	return receipt, member, nil
}

// ReadService validates one selected service member without consulting the
// mutable pointer. It is for a caller holding an exact generation lease.
func (publication *Publication) ReadService(
	ctx context.Context,
	serviceKey string,
) (ServiceReceipt, *ServiceMember, error) {
	if publication == nil || !validText(serviceKey) {
		return ServiceReceipt{}, nil, fmt.Errorf("%w: service lookup", ErrInvalid)
	}
	index, found := slices.BinarySearchFunc(publication.rootValue.Services, serviceKey, func(
		value ServiceReceipt,
		key string,
	) int {
		return strings.Compare(value.ServiceKey, key)
	})
	if !found {
		return ServiceReceipt{}, nil, ErrNotFound
	}
	receipt := publication.rootValue.Services[index]
	if receipt.State == "failed" {
		return receipt, nil, ErrServiceUnavailable
	}
	member, err := publication.openServiceMember(ctx, receipt)
	if err != nil {
		return ServiceReceipt{}, nil, err
	}
	return receipt, &member, nil
}

// ReadProjection validates and returns exactly one repository projection.
// It deliberately does not consult current.json: the caller may be resolving
// a citation against a lease-pinned historical generation. Current response
// paths call ConfirmCurrent immediately before emission.
func (publication *Publication) ReadProjection(
	ctx context.Context,
	digest string,
) (Projection, error) {
	if publication == nil || !validDigest(digest) {
		return Projection{}, fmt.Errorf("%w: projection lookup", ErrInvalid)
	}
	bucket := projectionBucket(digest)
	index, found := slices.BinarySearchFunc(
		publication.rootValue.RepositoryMembers,
		bucket,
		func(value MemberReceipt, wanted int) int { return value.Bucket - wanted },
	)
	if !found {
		return Projection{}, ErrNotFound
	}
	member, err := publication.openRepositoryMember(
		ctx, publication.rootValue.RepositoryMembers[index],
	)
	if err != nil {
		return Projection{}, err
	}
	projectionIndex, found := slices.BinarySearchFunc(
		member.Projections,
		digest,
		func(value Projection, wanted string) int {
			return strings.Compare(value.Digest, wanted)
		},
	)
	if !found {
		return Projection{}, ErrNotFound
	}
	return cloneProjection(member.Projections[projectionIndex]), nil
}

// ReadProjections batches one page of projection lookups by bucket so a page
// never rereads the same repository member once per row.
func (publication *Publication) ReadProjections(
	ctx context.Context,
	digests []string,
) (map[string]Projection, error) {
	if publication == nil || len(digests) == 0 || len(digests) > 100 {
		return nil, fmt.Errorf("%w: projection page", ErrInvalid)
	}
	wanted := make(map[int]map[string]struct{})
	for _, digest := range digests {
		if !validDigest(digest) {
			return nil, fmt.Errorf("%w: projection page digest", ErrInvalid)
		}
		bucket := projectionBucket(digest)
		if wanted[bucket] == nil {
			wanted[bucket] = make(map[string]struct{})
		}
		wanted[bucket][digest] = struct{}{}
	}
	result := make(map[string]Projection, len(digests))
	for bucket, bucketWanted := range wanted {
		index, found := slices.BinarySearchFunc(
			publication.rootValue.RepositoryMembers,
			bucket,
			func(value MemberReceipt, wanted int) int { return value.Bucket - wanted },
		)
		if !found {
			return nil, ErrNotFound
		}
		member, err := publication.openRepositoryMember(
			ctx, publication.rootValue.RepositoryMembers[index],
		)
		if err != nil {
			return nil, err
		}
		for _, projection := range member.Projections {
			if _, present := bucketWanted[projection.Digest]; present {
				result[projection.Digest] = cloneProjection(projection)
			}
		}
	}
	if len(result) != len(digests) {
		return nil, ErrNotFound
	}
	return result, nil
}

// ConfirmCurrent is the final response fence for a current-root read.
func (publication *Publication) ConfirmCurrent() error {
	if publication == nil || len(publication.pointerRaw) == 0 || publication.base == "" {
		return fmt.Errorf("%w: current publication fence", ErrInvalid)
	}
	current, err := readRegular(filepath.Join(publication.base, "current.json"), MaxRootBytes)
	if err != nil || !slices.Equal(current, publication.pointerRaw) {
		return ErrPublishing
	}
	return nil
}

func openDirectoryComplete(ctx context.Context, directory string, expected Root) (*Publication, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: generation directory", ErrInvalid)
	}
	raw, err := readRegular(filepath.Join(directory, "root.json"), MaxRootBytes)
	if err != nil {
		return nil, err
	}
	var value Root
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil || validateRoot(value) != nil ||
		value.Digest != expected.Digest || value.GenerationDigest != expected.GenerationDigest {
		return nil, fmt.Errorf("%w: complete root", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: noncanonical root", ErrInvalid)
	}
	publication := &Publication{directory: directory, rootValue: value}
	wanted := map[string]struct{}{"root.json": {}}
	var projections, references int
	var repositoryBytes, serviceBytes int64
	for _, receipt := range value.RepositoryMembers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wanted[receipt.Name] = struct{}{}
		member, err := publication.openRepositoryMember(ctx, receipt)
		if err != nil {
			return nil, err
		}
		projections += len(member.Projections)
		repositoryBytes += receipt.ContentBytes
	}
	for _, receipt := range value.Services {
		if receipt.State == "failed" {
			continue
		}
		wanted[receipt.Name] = struct{}{}
		member, err := publication.openServiceMember(ctx, receipt)
		if err != nil {
			return nil, err
		}
		references += len(member.References)
		serviceBytes += receipt.ContentBytes
	}
	if projections != value.ProjectionCount || references != value.ServiceReferenceCount ||
		repositoryBytes != value.EncodedRepositoryBytes || serviceBytes != value.EncodedServiceBytes {
		return nil, fmt.Errorf("%w: generation totals", ErrInvalid)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(wanted) {
		return nil, fmt.Errorf("%w: generation inventory", ErrInvalid)
	}
	for _, entry := range entries {
		if _, present := wanted[entry.Name()]; !present || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: unexpected generation entry", ErrInvalid)
		}
	}
	return publication, nil
}

func (publication *Publication) openRepositoryMember(
	ctx context.Context,
	receipt MemberReceipt,
) (RepositoryMember, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryMember{}, err
	}
	raw, err := readRegular(filepath.Join(publication.directory, receipt.Name), MaxRepositoryMemberBytes)
	if err != nil || int64(len(raw)) != receipt.ContentBytes {
		return RepositoryMember{}, fmt.Errorf("%w: repository member bytes", ErrInvalid)
	}
	var value RepositoryMember
	if err := decodeExact(raw, MaxRepositoryMemberBytes, &value); err != nil ||
		validateRepositoryMember(value) != nil || value.Bucket != receipt.Bucket ||
		len(value.Projections) != receipt.RecordCount || value.Digest != receipt.ContentDigest ||
		(publication.rootValue.Schema == RootSchema && value.AuthorityDigest != publication.rootValue.AuthorityDigest) {
		return RepositoryMember{}, fmt.Errorf("%w: repository member", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return RepositoryMember{}, fmt.Errorf("%w: repository member canonical bytes", ErrInvalid)
	}
	return value, nil
}

func (publication *Publication) openServiceMember(
	ctx context.Context,
	receipt ServiceReceipt,
) (ServiceMember, error) {
	if err := ctx.Err(); err != nil {
		return ServiceMember{}, err
	}
	raw, err := readRegular(filepath.Join(publication.directory, receipt.Name), MaxServiceMemberBytes)
	if err != nil || int64(len(raw)) != receipt.ContentBytes {
		return ServiceMember{}, fmt.Errorf("%w: service member bytes", ErrInvalid)
	}
	var value ServiceMember
	if err := decodeExact(raw, MaxServiceMemberBytes, &value); err != nil ||
		validateServiceMember(value) != nil || value.ServiceKey != receipt.ServiceKey ||
		value.Incarnation != receipt.Incarnation || value.ServiceGeneration != receipt.ServiceGeneration ||
		len(value.References) != receipt.ReferenceCount || value.Digest != receipt.ContentDigest ||
		(publication.rootValue.Schema == RootSchema && value.AuthorityDigest != publication.rootValue.AuthorityDigest) {
		return ServiceMember{}, fmt.Errorf("%w: service member", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return ServiceMember{}, fmt.Errorf("%w: service member canonical bytes", ErrInvalid)
	}
	return value, nil
}

func validateRepositoryMember(value RepositoryMember) error {
	if (value.Schema != RepositoryMemberSchema && value.Schema != RepositoryMemberSchemaV2) ||
		!validDigest(value.AuthorityDigest) ||
		value.Bucket < 0 || value.Bucket >= RepositoryBuckets || len(value.Projections) < 1 {
		return fmt.Errorf("%w: repository member", ErrInvalid)
	}
	prior := ""
	for _, projection := range value.Projections {
		if validateProjection(projection) != nil || projection.Digest <= prior ||
			projectionBucket(projection.Digest) != value.Bucket {
			return fmt.Errorf("%w: repository projection", ErrInvalid)
		}
		prior = projection.Digest
	}
	if value.Schema == RepositoryMemberSchemaV2 {
		want, err := repositoryPartitionAuthority(value.Bucket, value.Projections)
		if err != nil || want != value.AuthorityDigest {
			return fmt.Errorf("%w: repository partition authority", ErrInvalid)
		}
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: repository member digest", ErrInvalid)
	}
	return nil
}

func validateServiceMember(value ServiceMember) error {
	if (value.Schema != ServiceMemberSchema && value.Schema != ServiceMemberSchemaV2) ||
		!validDigest(value.AuthorityDigest) ||
		!validText(value.ServiceKey) || value.Incarnation == 0 ||
		!validDigest(value.ServiceGeneration) || len(value.References) > MaxServiceReferences {
		return fmt.Errorf("%w: service member", ErrInvalid)
	}
	prior := ""
	for _, reference := range value.References {
		if validateServiceReference(reference) != nil || reference.Digest <= prior {
			return fmt.Errorf("%w: service reference", ErrInvalid)
		}
		prior = reference.Digest
	}
	if value.Schema == ServiceMemberSchemaV2 {
		want, err := servicePartitionAuthority(
			value.ServiceKey, value.Incarnation, value.ServiceGeneration, value.References,
		)
		if err != nil || want != value.AuthorityDigest {
			return fmt.Errorf("%w: service partition authority", ErrInvalid)
		}
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: service member digest", ErrInvalid)
	}
	return nil
}

func repositoryPartitionAuthority(bucket int, projections []Projection) (string, error) {
	return digestValue(struct {
		Schema      string       `json:"schema"`
		Bucket      int          `json:"bucket"`
		Projections []Projection `json:"projections"`
	}{
		Schema: "phebs-relationship-repository-partition-authority-v2",
		Bucket: bucket, Projections: projections,
	})
}

func servicePartitionAuthority(
	serviceKey string, incarnation uint64, generation string, references []ServiceReference,
) (string, error) {
	return digestValue(struct {
		Schema      string             `json:"schema"`
		ServiceKey  string             `json:"service_key"`
		Incarnation uint64             `json:"incarnation"`
		Generation  string             `json:"generation"`
		References  []ServiceReference `json:"references"`
	}{
		Schema:     "phebs-relationship-service-partition-authority-v2",
		ServiceKey: serviceKey, Incarnation: incarnation,
		Generation: generation, References: references,
	})
}

func reuseRelationshipMember(prior *Publication, name string, raw []byte, destination string) bool {
	if prior == nil || prior.rootValue.Schema != RootSchemaV2 {
		return false
	}
	priorRaw, err := readRegular(filepath.Join(prior.directory, name), len(raw))
	if err != nil || !bytes.Equal(priorRaw, raw) {
		return false
	}
	return os.Link(filepath.Join(prior.directory, name), destination) == nil
}

func validateRoot(value Root) error {
	if (value.Schema != RootSchema && value.Schema != RootSchemaV2) ||
		validateAuthority(value.Schema, value.Authority) != nil ||
		value.Policy != FrozenPolicy() || !value.RepositoryComplete ||
		!validDigest(value.AuthorityDigest) || len(value.RepositoryMembers) > RepositoryBuckets ||
		len(value.Services) > MaxServices || value.ServiceCount != len(value.Services) ||
		value.ProjectionCount < 0 || value.ProjectionCount > MaxProjectionRecords ||
		value.ServiceReferenceCount < 0 || value.ServiceReferenceCount > MaxTotalServiceReferences ||
		value.EncodedRepositoryBytes < 0 || value.EncodedServiceBytes < 0 ||
		value.EncodedRepositoryBytes > MaxGenerationBytes ||
		value.EncodedServiceBytes > MaxGenerationBytes-value.EncodedRepositoryBytes ||
		!validDigest(value.GenerationDigest) || !validDigest(value.Digest) {
		return fmt.Errorf("%w: root", ErrInvalid)
	}
	wantAuthority, _ := digestValue(value.Authority)
	wantGeneration, _ := generationDigest(value.Schema, value.AuthorityDigest)
	if wantAuthority != value.AuthorityDigest || wantGeneration != value.GenerationDigest ||
		value.CompleteServiceCount+value.EmptyServiceCount+value.FailedServiceCount != value.ServiceCount ||
		value.AllServicesComplete != (value.FailedServiceCount == 0) {
		return fmt.Errorf("%w: root authority totals", ErrInvalid)
	}
	var projections int
	var repositoryBytes int64
	priorBucket := -1
	for _, receipt := range value.RepositoryMembers {
		if receipt.Bucket <= priorBucket || receipt.Bucket >= RepositoryBuckets ||
			receipt.Name != repositoryMemberName(receipt.Bucket) || receipt.RecordCount < 1 ||
			receipt.ContentBytes < 1 || receipt.ContentBytes > MaxRepositoryMemberBytes ||
			!validDigest(receipt.ContentDigest) {
			return fmt.Errorf("%w: repository receipt", ErrInvalid)
		}
		projections += receipt.RecordCount
		repositoryBytes += receipt.ContentBytes
		priorBucket = receipt.Bucket
	}
	if projections != value.ProjectionCount || repositoryBytes != value.EncodedRepositoryBytes {
		return fmt.Errorf("%w: repository totals", ErrInvalid)
	}
	priorService := ""
	var complete, empty, failed, references int
	var serviceBytes int64
	serviceSet := make([]serviceSetIdentity, 0, len(value.Services))
	for _, receipt := range value.Services {
		if !validText(receipt.ServiceKey) || receipt.ServiceKey <= priorService || receipt.Incarnation == 0 ||
			!validDigest(receipt.ServiceGeneration) {
			return fmt.Errorf("%w: service receipt identity", ErrInvalid)
		}
		switch receipt.State {
		case "complete", "empty":
			if receipt.Reason != "" || receipt.Name != serviceMemberName(receipt.ServiceKey) ||
				receipt.ContentBytes < 1 || receipt.ContentBytes > MaxServiceMemberBytes ||
				!validDigest(receipt.ContentDigest) || receipt.ReferenceCount < 0 ||
				receipt.ReferenceCount > MaxServiceReferences ||
				receipt.State == "complete" && receipt.ReferenceCount == 0 ||
				receipt.State == "empty" && receipt.ReferenceCount != 0 {
				return fmt.Errorf("%w: complete service receipt", ErrInvalid)
			}
			if receipt.State == "complete" {
				complete++
			} else {
				empty++
			}
			references += receipt.ReferenceCount
			serviceBytes += receipt.ContentBytes
		case "failed":
			if (receipt.Reason != "reference_limit" && receipt.Reason != "encoded_limit" &&
				receipt.Reason != "resident_limit") ||
				receipt.Name != "" || receipt.ReferenceCount != 0 || receipt.ContentBytes != 0 ||
				receipt.ContentDigest != "" {
				return fmt.Errorf("%w: failed service receipt", ErrInvalid)
			}
			failed++
		default:
			return fmt.Errorf("%w: service receipt state", ErrInvalid)
		}
		priorService = receipt.ServiceKey
		serviceSet = append(serviceSet, serviceSetIdentity{
			ServiceKey: receipt.ServiceKey, Incarnation: receipt.Incarnation,
			DesiredGeneration: receipt.ServiceGeneration,
		})
	}
	wantServiceSet, _ := digestServiceSet(serviceSet)
	if complete != value.CompleteServiceCount || empty != value.EmptyServiceCount ||
		failed != value.FailedServiceCount || references != value.ServiceReferenceCount ||
		serviceBytes != value.EncodedServiceBytes ||
		wantServiceSet != value.Authority.ServiceStateSetDigest {
		return fmt.Errorf("%w: service totals", ErrInvalid)
	}
	wantDigest, _ := rootDigest(value)
	if wantDigest != value.Digest {
		return fmt.Errorf("%w: root digest", ErrInvalid)
	}
	return nil
}

// ValidateRoot exposes the closed root validator to later authorized readers.
func ValidateRoot(value Root) error { return validateRoot(value) }

func generationDigest(schema, authorityDigest string) (string, error) {
	return digestValue(struct {
		Schema          string `json:"schema"`
		AuthorityDigest string `json:"authority_digest"`
	}{Schema: schema, AuthorityDigest: authorityDigest})
}

func rootDigest(value Root) (string, error) {
	value.Digest = ""
	return digestValue(value)
}

func newPointer(value Root) (Pointer, error) {
	pointer := Pointer{
		Schema: PointerSchema, Repository: value.Authority.Repository,
		GenerationDigest: value.GenerationDigest, RootDigest: value.Digest, RootName: "root.json",
	}
	digest, err := digestValue(pointer)
	if err != nil {
		return Pointer{}, err
	}
	pointer.Digest = digest
	return pointer, validatePointer(pointer)
}

func validatePointer(value Pointer) error {
	if value.Schema != PointerSchema || reponame.Validate(value.Repository) != nil ||
		!validDigest(value.GenerationDigest) || !validDigest(value.RootDigest) ||
		value.RootName != "root.json" || !validDigest(value.Digest) {
		return fmt.Errorf("%w: pointer", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, _ := digestValue(copyValue)
	if digest != value.Digest {
		return fmt.Errorf("%w: pointer digest", ErrInvalid)
	}
	return nil
}

func validateMarker(value Marker) error {
	if value.Schema != MarkerSchema || validatePointer(value.Pointer) != nil || !validDigest(value.Digest) {
		return fmt.Errorf("%w: marker", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, _ := digestValue(copyValue)
	if digest != value.Digest {
		return fmt.Errorf("%w: marker digest", ErrInvalid)
	}
	return nil
}

func readRoot(directory string, pointer Pointer) (Root, error) {
	raw, err := readRegular(filepath.Join(directory, pointer.RootName), MaxRootBytes)
	if err != nil {
		return Root{}, err
	}
	var value Root
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil || validateRoot(value) != nil ||
		value.Authority.Repository != pointer.Repository || value.GenerationDigest != pointer.GenerationDigest ||
		value.Digest != pointer.RootDigest {
		return Root{}, fmt.Errorf("%w: root control", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return Root{}, fmt.Errorf("%w: root canonical bytes", ErrInvalid)
	}
	return value, nil
}

func repositoryRoot(root, repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return filepath.Join(root, "relationship-publications", hex.EncodeToString(sum[:]))
}

func generationPath(root, repository, generation string) string {
	return filepath.Join(repositoryRoot(root, repository), strings.TrimPrefix(generation, "sha256:"))
}

func ensureRoot(root string) error {
	if err := validateDirectory(root); err != nil {
		return err
	}
	base := filepath.Join(root, "relationship-publications")
	if err := os.Mkdir(base, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validateDirectory(base)
}

func validateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: real directory required", ErrInvalid)
	}
	return nil
}

func writeExclusive(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

func replaceFile(path string, raw []byte) error {
	temporary := path + ".tmp"
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeExclusive(temporary, raw); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func readRegular(path string, limit int) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > int64(limit) {
		return nil, fmt.Errorf("%w: bounded regular file required", ErrInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: file identity changed", ErrInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	afterOpen, openErr := file.Stat()
	afterPath, pathErr := os.Lstat(path)
	if openErr != nil || pathErr != nil || len(raw) > limit || !os.SameFile(opened, afterOpen) ||
		!os.SameFile(opened, afterPath) || afterPath.Mode()&os.ModeSymlink != 0 ||
		int64(len(raw)) != afterOpen.Size() || opened.Size() != afterOpen.Size() ||
		!opened.ModTime().Equal(afterOpen.ModTime()) {
		return nil, fmt.Errorf("%w: file changed while read", ErrInvalid)
	}
	return raw, nil
}

func decodeExact(raw []byte, limit int, target any) error {
	if len(raw) == 0 || len(raw) > limit {
		return ErrLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON", ErrInvalid)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
