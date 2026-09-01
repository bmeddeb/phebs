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
	"time"

	"github.com/bmeddeb/phebs/internal/reponame"
)

const publishPinRollbackTimeoutV3 = 5 * time.Second

type PublicationV3 struct {
	base       string
	directory  string
	rootValue  RootV3
	pointer    PointerV3
	pointerRaw []byte
}

// RecoveryPinStoreV3 can reconstruct every durable owner from a completely
// validated marker/root pair without requiring destructive store methods.
type RecoveryPinStoreV3 interface {
	RecoverRelationshipPublicationV3(
		context.Context,
		string, string, string, string,
		uint64, uint64,
		string,
	) error
	PinPartitionedExtractionRun(context.Context, string, string) error
}

// PublishPinStoreV3 is the mandatory pre-pointer pin boundary. The primitive
// identity keeps the filesystem package independent of the store package.
type PublishPinStoreV3 interface {
	PinRelationshipPublicationV3(
		context.Context,
		string, string, string, string,
		uint64, uint64,
		string,
	) error
	PinPartitionedExtractionRun(context.Context, string, string) error
	UnpinRelationshipPublicationV3(
		context.Context,
		string, string, string, string,
		uint64, uint64,
		string,
	) error
	UnpinPartitionedExtractionRun(context.Context, string, string) error
}

func (prepared *PreparedV3) Root() RootV3 {
	if prepared == nil {
		return RootV3{}
	}
	return cloneRootV3(prepared.rootValue)
}

func (prepared *PreparedV3) abort() error {
	if prepared == nil || prepared.closed {
		return nil
	}
	base, err := RepositoryRootV3(prepared.root, prepared.repository)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(prepared.directory); err != nil {
		return err
	}
	if err := syncDirectory(base); err != nil {
		return err
	}
	prepared.closed = true
	return nil
}

func writePublicationStageV3(
	ctx context.Context,
	root string,
	authority AuthorityV3,
	authorityDigest string,
	prior *PublicationV3,
	accumulator *buildAccumulator,
) (_ *PreparedV3, retErr error) {
	directory, err := stageDirectoryV3(root, authority.Repository)
	if err != nil {
		return nil, err
	}
	prepared := &PreparedV3{
		root: root, repository: authority.Repository, directory: directory,
	}
	failed := true
	defer func() {
		if !failed {
			return
		}
		if cleanupErr := prepared.abort(); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("clean relationship v3 stage: %w", cleanupErr))
		}
	}()
	value := RootV3{
		Schema: RootSchemaV3, Authority: authority, AuthorityDigest: authorityDigest,
		Policy: FrozenPolicyV3(), RepositoryComplete: true,
		RepositoryMembers: []RepositoryReceiptV3{}, ServiceMembers: []ServiceRangeReceiptV3{},
		ProjectionCount: accumulator.projectionCount,
	}
	if err := writeRepositoryMembersV3(ctx, directory, prior, accumulator, &value); err != nil {
		return nil, err
	}
	records, err := serviceRecordsV3(accumulator.services)
	if err != nil {
		return nil, err
	}
	if err := writeServiceMembersV3(ctx, directory, prior, records, &value); err != nil {
		return nil, err
	}
	value.AllServicesComplete = value.FailedServiceCount == 0
	value.GenerationDigest, err = generationDigestV3(value)
	if err != nil {
		return nil, err
	}
	value.Digest, err = rootDigestV3(value)
	if err != nil || ValidateRootV3(value) != nil {
		return nil, fmt.Errorf("%w: v3 root", ErrInvalid)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxRootBytesV3 {
		return nil, ErrLimit
	}
	if err := writeExclusive(filepath.Join(directory, "root.json"), raw); err != nil {
		return nil, err
	}
	if err := syncDirectory(directory); err != nil {
		return nil, err
	}
	prepared.rootValue = value
	failed = false
	return prepared, nil
}

func writeRepositoryMembersV3(
	ctx context.Context,
	directory string,
	prior *PublicationV3,
	accumulator *buildAccumulator,
	root *RootV3,
) error {
	buckets := make([]int, 0, len(accumulator.repository))
	for bucket := range accumulator.repository {
		buckets = append(buckets, bucket)
	}
	slices.Sort(buckets)
	for _, bucket := range buckets {
		if err := ctx.Err(); err != nil {
			return err
		}
		projections := accumulator.repository[bucket]
		slices.SortFunc(projections, func(left, right Projection) int {
			return strings.Compare(left.Digest, right.Digest)
		})
		envelopeBytes, err := repositoryMemberEnvelopeBytesV3(bucket)
		if err != nil {
			return err
		}
		fragments := make([]ProjectionBucketV3, 0, len(projections))
		fragmentCount, fragmentBytes, memberBytes := 0, 0, 0
		for index := range projections {
			bucketsForProjection, projectionBytes, err := projectionBucketsV3(projections[index])
			// Every fragment owns its claims. Release the consumed unbucketed
			// projection before retaining its fragments in the member.
			projections[index] = Projection{}
			if err != nil {
				return err
			}
			fragmentCount, fragmentBytes, memberBytes, err = admitRepositoryFragmentsV3(
				envelopeBytes, fragmentCount, fragmentBytes,
				len(bucketsForProjection), projectionBytes, MaxRepositoryMemberBytes,
			)
			if err != nil {
				return err
			}
			fragments = append(fragments, bucketsForProjection...)
		}
		if int64(memberBytes) > MaxGenerationBytes-root.EncodedRepositoryBytes-root.EncodedServiceBytes {
			return ErrLimit
		}
		member := RepositoryMemberV3{
			Schema: RepositoryMemberSchemaV3, Bucket: bucket, Fragments: fragments,
		}
		member.Digest, err = digestValue(member)
		if err != nil {
			return err
		}
		if err := validateRepositoryMemberV3(member); err != nil {
			return err
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return err
		}
		if len(raw) != memberBytes {
			return fmt.Errorf("%w: v3 repository member encoded size", ErrInvalid)
		}
		name := repositoryMemberName(bucket)
		if !reuseRelationshipMemberV3(prior, name, raw, filepath.Join(directory, name)) {
			if err := writeExclusive(filepath.Join(directory, name), raw); err != nil {
				return err
			}
		}
		root.RepositoryMembers = append(root.RepositoryMembers, RepositoryReceiptV3{
			Bucket: bucket, Name: name, ProjectionCount: len(projections),
			FragmentCount: len(fragments), ContentBytes: int64(len(raw)),
			ContentDigest: member.Digest,
		})
		root.ProjectionFragmentCount += len(fragments)
		root.EncodedRepositoryBytes += int64(len(raw))
		delete(accumulator.repository, bucket)
	}
	return nil
}

func repositoryMemberEnvelopeBytesV3(bucket int) (int, error) {
	placeholder := RepositoryMemberV3{
		Schema:    RepositoryMemberSchemaV3,
		Bucket:    bucket,
		Fragments: []ProjectionBucketV3{},
		Digest:    "sha256:" + strings.Repeat("0", sha256.Size*2),
	}
	raw, err := json.Marshal(placeholder)
	if err != nil {
		return 0, err
	}
	// The brackets in the empty fragments array remain part of the fixed
	// envelope; nonempty members add only fragment bytes and commas.
	return len(raw), nil
}

func admitRepositoryFragmentsV3(
	envelopeBytes, currentCount, currentBytes, addedCount, addedBytes, limit int,
) (int, int, int, error) {
	const maxFragments = MaxProjectionRecords * MaxProjectionBucketsV3
	if envelopeBytes < 0 || limit < 0 || currentCount < 0 || addedCount < 1 ||
		addedCount > maxFragments || currentCount > maxFragments-addedCount ||
		currentBytes < 0 || addedBytes < 0 ||
		envelopeBytes > limit || currentBytes > limit || addedBytes > limit-currentBytes {
		return 0, 0, 0, ErrLimit
	}
	nextCount := currentCount + addedCount
	nextBytes := currentBytes + addedBytes
	separators := nextCount - 1
	if nextBytes > limit-envelopeBytes || separators > limit-envelopeBytes-nextBytes {
		return 0, 0, 0, ErrLimit
	}
	return nextCount, nextBytes, envelopeBytes + nextBytes + separators, nil
}

func serviceRecordsV3(services map[string]*serviceAccumulator) ([]ServiceRecordV3, error) {
	result := make([]ServiceRecordV3, 0, len(services))
	for _, key := range sortedServiceKeys(services) {
		service := services[key]
		slices.SortFunc(service.refs, func(left, right ServiceReference) int {
			return strings.Compare(left.Digest, right.Digest)
		})
		record := ServiceRecordV3{
			Schema: ServiceRecordSchemaV3, ServiceKey: key,
			Incarnation:       service.state.Incarnation,
			ServiceGeneration: service.state.DesiredGeneration,
			References:        service.refs,
		}
		switch {
		case service.failed:
			record.State, record.Reason, record.References = "failed", service.reason, nil
		case len(record.References) == 0:
			record.State = "empty"
		default:
			record.State = "complete"
		}
		if serviceRecordTooLargeV3(record) {
			record.State, record.Reason, record.References = "failed", "encoded_limit", nil
		}
		record.Digest = ""
		record.Digest, _ = digestValue(record)
		if err := validateServiceRecordV3(record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func serviceRecordTooLargeV3(value ServiceRecordV3) bool {
	// Refuse before marshaling a potentially enormous aggregate. Individual
	// references are already bounded and canonical.
	const memberEnvelope = 4 << 10
	bytesLeft := MaxServiceMemberBytes - memberEnvelope
	for _, reference := range value.References {
		raw, err := json.Marshal(reference)
		if err != nil || len(raw)+1 > bytesLeft {
			return true
		}
		bytesLeft -= len(raw) + 1
	}
	raw, err := json.Marshal(ServiceRecordV3{
		Schema: value.Schema, ServiceKey: value.ServiceKey, Incarnation: value.Incarnation,
		ServiceGeneration: value.ServiceGeneration, State: value.State, Reason: value.Reason,
		References: nil,
	})
	return err != nil || len(raw) > bytesLeft
}

func writeServiceMembersV3(
	ctx context.Context,
	directory string,
	prior *PublicationV3,
	records []ServiceRecordV3,
	root *RootV3,
) error {
	ranges, err := packServiceRecordsV3(records)
	if err != nil {
		return err
	}
	if len(ranges) > MaxServiceMembersV3 {
		return ErrLimit
	}
	for ordinal, recordsForMember := range ranges {
		if err := ctx.Err(); err != nil {
			return err
		}
		member := ServiceMemberV3{
			Schema: ServiceMemberSchemaV3, Ordinal: ordinal, Count: len(ranges),
			FirstKey: recordsForMember[0].ServiceKey,
			LastKey:  recordsForMember[len(recordsForMember)-1].ServiceKey,
			Services: recordsForMember,
		}
		member.Digest, _ = digestValue(member)
		if err := validateServiceMemberV3(member); err != nil {
			return err
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return err
		}
		if len(raw) > MaxServiceMemberBytes ||
			int64(len(raw)) > MaxGenerationBytes-root.EncodedRepositoryBytes-root.EncodedServiceBytes {
			return ErrLimit
		}
		name := serviceRangeMemberNameV3(ordinal)
		if !reuseRelationshipMemberV3(prior, name, raw, filepath.Join(directory, name)) {
			if err := writeExclusive(filepath.Join(directory, name), raw); err != nil {
				return err
			}
		}
		receipt := ServiceRangeReceiptV3{
			Ordinal: ordinal, Count: len(ranges), FirstKey: member.FirstKey, LastKey: member.LastKey,
			ServiceCount: len(member.Services), Name: name,
			ContentBytes: int64(len(raw)), ContentDigest: member.Digest,
		}
		for _, record := range member.Services {
			switch record.State {
			case "complete":
				receipt.CompleteCount++
				root.CompleteServiceCount++
			case "empty":
				receipt.EmptyCount++
				root.EmptyServiceCount++
			case "failed":
				receipt.FailedCount++
				root.FailedServiceCount++
			}
			receipt.ReferenceCount += len(record.References)
		}
		root.ServiceMembers = append(root.ServiceMembers, receipt)
		root.ServiceCount += receipt.ServiceCount
		root.ServiceReferenceCount += receipt.ReferenceCount
		root.EncodedServiceBytes += receipt.ContentBytes
	}
	return nil
}

func packServiceRecordsV3(records []ServiceRecordV3) ([][]ServiceRecordV3, error) {
	if len(records) == 0 {
		return nil, nil
	}
	result := make([][]ServiceRecordV3, 0, (len(records)+MaxServicesPerServiceMemberV3-1)/MaxServicesPerServiceMemberV3)
	current := make([]ServiceRecordV3, 0, min(len(records), MaxServicesPerServiceMemberV3))
	currentBytes := 4 << 10
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		if len(raw)+(4<<10) > MaxServiceMemberBytes {
			return nil, ErrLimit
		}
		if len(current) == MaxServicesPerServiceMemberV3 ||
			len(current) > 0 && currentBytes+len(raw)+1 > MaxServiceMemberBytes {
			result = append(result, current)
			current = make([]ServiceRecordV3, 0, MaxServicesPerServiceMemberV3)
			currentBytes = 4 << 10
		}
		current = append(current, record)
		currentBytes += len(raw) + 1
	}
	if len(current) != 0 {
		result = append(result, current)
	}
	return result, nil
}

func reuseRelationshipMemberV3(
	prior *PublicationV3,
	name string,
	raw []byte,
	destination string,
) bool {
	if prior == nil {
		return false
	}
	priorRaw, err := readRegular(filepath.Join(prior.directory, name), len(raw))
	if err != nil || !bytes.Equal(priorRaw, raw) {
		return false
	}
	return os.Link(filepath.Join(prior.directory, name), destination) == nil
}

// PublishV3 is the only exported pointer-swap path. Current-fenced pins are
// installed while the completely validated bytes remain a private stage. A
// durable marker then owns that stage before its atomic generation rename;
// once the marker can exist, every error leaves recovery authority in place.
func PublishV3(
	ctx context.Context,
	prepared *PreparedV3,
	pins PublishPinStoreV3,
) (*PublicationV3, error) {
	if prepared == nil || prepared.closed || pins == nil {
		return nil, errors.New("relationship v3 stage is closed or pins are unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base, err := RepositoryRootV3(prepared.root, prepared.repository)
	if err != nil {
		return nil, err
	}
	target, err := GenerationPathV3(
		prepared.root, prepared.repository, prepared.rootValue.GenerationDigest,
	)
	if err != nil {
		return nil, err
	}
	if markerPresentV3(base) {
		return nil, ErrPublishing
	}
	current, readErr := ReadPointerV3(ctx, prepared.root, prepared.repository)
	if readErr == nil {
		if current.GenerationDigest == prepared.rootValue.GenerationDigest &&
			current.RootDigest == prepared.rootValue.Digest {
			publication, openErr := ValidateGenerationV3(
				ctx, prepared.root, prepared.repository,
				current.GenerationDigest, current.RootDigest,
			)
			if openErr != nil {
				return nil, openErr
			}
			if err := prepared.abort(); err != nil {
				return nil, err
			}
			publication.base, publication.pointer = base, current
			publication.pointerRaw, _ = json.Marshal(current)
			return publication, nil
		}
	} else if !errors.Is(readErr, ErrNotFound) {
		return nil, readErr
	}
	targetExists := false
	if _, err := os.Lstat(target); err == nil {
		if _, openErr := openDirectoryCompleteV3(ctx, target, prepared.rootValue); openErr != nil {
			return nil, fmt.Errorf("existing relationship v3 generation conflicts: %w", openErr)
		}
		targetExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !targetExists {
		if _, err := openDirectoryCompleteV3(
			ctx, prepared.directory, prepared.rootValue,
		); err != nil {
			return nil, err
		}
	}
	pointer, err := newPointerV3(prepared.rootValue)
	if err != nil {
		return nil, err
	}
	_, markerRaw, pointerRaw, err := publicationControlsV3(
		pointer, filepath.Base(prepared.directory),
	)
	if err != nil {
		return nil, err
	}
	owner := "relationship:" + prepared.rootValue.GenerationDigest
	pinnedRuns := make([]string, 0, len(prepared.rootValue.Authority.Upstream.Domains))
	catalogAttempted := true
	if err := pinRelationshipV3(ctx, pins, prepared.rootValue); err != nil {
		return nil, abortPublishBeforeMarkerV3(
			prepared, pins, prepared.rootValue, owner, pinnedRuns,
			catalogAttempted, targetExists, err,
		)
	}
	for _, domain := range prepared.rootValue.Authority.Upstream.Domains {
		pinnedRuns = append(pinnedRuns, domain.RunID)
		if err := pins.PinPartitionedExtractionRun(ctx, domain.RunID, owner); err != nil {
			return nil, abortPublishBeforeMarkerV3(
				prepared, pins, prepared.rootValue, owner, pinnedRuns,
				catalogAttempted, targetExists, err,
			)
		}
	}
	// replaceFile syncs the repository directory. Even an ambiguous sync error
	// must prevent the caller from deleting the possibly marker-owned stage.
	// If the marker did not become durable, startup drains the stage and global
	// store reconciliation removes its now-unrooted pins.
	if err := replaceFile(filepath.Join(base, "publishing.json"), markerRaw); err != nil {
		return nil, resolveMarkerInstallErrorV3(
			prepared, pins, prepared.rootValue, owner, pinnedRuns,
			targetExists, markerRaw, err,
		)
	}
	prepared.closed = true
	if targetExists {
		if err := os.RemoveAll(prepared.directory); err != nil {
			return nil, err
		}
	} else if err := os.Rename(prepared.directory, target); err != nil {
		return nil, err
	}
	if err := syncDirectory(base); err != nil {
		return nil, err
	}
	if err := replaceFile(filepath.Join(base, "current.json"), pointerRaw); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(base, "publishing.json")); err != nil {
		return nil, err
	}
	if err := syncDirectory(base); err != nil {
		return nil, err
	}
	return &PublicationV3{
		base: base, directory: target, rootValue: prepared.rootValue,
		pointer: pointer, pointerRaw: pointerRaw,
	}, nil
}

func pinRelationshipV3(ctx context.Context, pins PublishPinStoreV3, root RootV3) error {
	return pins.PinRelationshipPublicationV3(
		ctx, root.Authority.Repository, root.GenerationDigest, root.Digest,
		root.Authority.CatalogRootDigest, root.Authority.CatalogControlRevision,
		root.Authority.ServiceStateControlRevision, root.Authority.ServiceStateSummaryDigest,
	)
}

func recoverRelationshipV3(ctx context.Context, pins RecoveryPinStoreV3, root RootV3) error {
	return pins.RecoverRelationshipPublicationV3(
		ctx, root.Authority.Repository, root.GenerationDigest, root.Digest,
		root.Authority.CatalogRootDigest, root.Authority.CatalogControlRevision,
		root.Authority.ServiceStateControlRevision, root.Authority.ServiceStateSummaryDigest,
	)
}

func unpinRelationshipV3(ctx context.Context, pins PublishPinStoreV3, root RootV3) error {
	return pins.UnpinRelationshipPublicationV3(
		ctx, root.Authority.Repository, root.GenerationDigest, root.Digest,
		root.Authority.CatalogRootDigest, root.Authority.CatalogControlRevision,
		root.Authority.ServiceStateControlRevision, root.Authority.ServiceStateSummaryDigest,
	)
}

func rollbackPublishPinsV3(
	pins PublishPinStoreV3,
	root RootV3,
	owner string,
	runs []string,
	catalogAttempted bool,
) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), publishPinRollbackTimeoutV3)
	defer cancel()
	var result error
	for index := len(runs) - 1; index >= 0; index-- {
		if err := pins.UnpinPartitionedExtractionRun(rollbackCtx, runs[index], owner); err != nil {
			result = errors.Join(result, err)
		}
	}
	if catalogAttempted {
		result = errors.Join(result, unpinRelationshipV3(rollbackCtx, pins, root))
	}
	return result
}

func abortPublishBeforeMarkerV3(
	prepared *PreparedV3,
	pins PublishPinStoreV3,
	root RootV3,
	owner string,
	runs []string,
	catalogAttempted bool,
	preservePins bool,
	cause error,
) error {
	var rollbackErr error
	if !preservePins {
		rollbackErr = rollbackPublishPinsV3(pins, root, owner, runs, catalogAttempted)
	}
	// An already-installed retained target can own the same deterministic pins.
	// Store-call failures are also ambiguous after commit, so never unpin that
	// identity; startup's complete protected-set reconciliation resolves it.
	abortErr := prepared.abort()
	return errors.Join(cause, rollbackErr, abortErr)
}

func resolveMarkerInstallErrorV3(
	prepared *PreparedV3,
	pins PublishPinStoreV3,
	root RootV3,
	owner string,
	runs []string,
	preservePins bool,
	markerRaw []byte,
	cause error,
) error {
	base, baseErr := RepositoryRootV3(prepared.root, prepared.repository)
	if baseErr != nil {
		prepared.closed = true
		return errors.Join(cause, baseErr)
	}
	raw, readErr := readRegular(filepath.Join(base, "publishing.json"), MaxRootBytesV3)
	if readErr == nil && bytes.Equal(raw, markerRaw) {
		prepared.closed = true
		return cause
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		// The replace result is ambiguous. Preserve the pins and stage because
		// the intended marker may exist but be temporarily unreadable.
		prepared.closed = true
		return errors.Join(cause, readErr)
	}
	rollbackErr := abortPublishBeforeMarkerV3(
		prepared, pins, root, owner, runs, true, preservePins, cause,
	)
	return errors.Join(rollbackErr, removePublishingTemporaryV3(base))
}

func removePublishingTemporaryV3(base string) error {
	path := filepath.Join(base, "publishing.json.tmp")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() > int64(MaxRootBytesV3) {
		return fmt.Errorf("%w: v3 publishing temporary", ErrInvalid)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(base)
}

func RecoverV3(
	ctx context.Context,
	root, repository string,
	pins RecoveryPinStoreV3,
) (bool, error) {
	if pins == nil {
		return false, errors.New("relationship v3 recovery pins are unavailable")
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		return false, err
	}
	if err := validateDirectory(base); err != nil {
		return false, err
	}
	raw, err := readRegular(filepath.Join(base, "publishing.json"), MaxRootBytesV3)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var marker MarkerV3
	if err := decodeExact(raw, MaxRootBytesV3, &marker); err != nil ||
		validateMarkerV3(marker) != nil || marker.Pointer.Repository != repository {
		return false, fmt.Errorf("%w: v3 publishing marker", ErrInvalid)
	}
	canonical, _ := json.Marshal(marker)
	if !slices.Equal(raw, canonical) {
		return false, fmt.Errorf("%w: v3 publishing marker bytes", ErrInvalid)
	}
	target, err := GenerationPathV3(
		root, repository, marker.Pointer.GenerationDigest,
	)
	if err != nil {
		return false, err
	}
	var publication *PublicationV3
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		if marker.StageName == "" {
			return false, fmt.Errorf("%w: v3 publishing generation absent", ErrInvalid)
		}
		stage := filepath.Join(base, marker.StageName)
		publication, err = openStagedGenerationCompleteV3(
			ctx, stage, marker.Pointer,
		)
		if err != nil {
			return false, err
		}
		if err := os.Rename(stage, target); err != nil {
			return false, err
		}
		if err := syncDirectory(base); err != nil {
			return false, err
		}
		publication.directory = target
	} else if err != nil {
		return false, err
	} else {
		publication, err = ValidateGenerationV3(
			ctx, root, repository, marker.Pointer.GenerationDigest, marker.Pointer.RootDigest,
		)
		if err != nil {
			return false, err
		}
	}
	if err := recoverRelationshipV3(ctx, pins, publication.rootValue); err != nil {
		return false, recoveryStoreError{err}
	}
	owner := "relationship:" + publication.rootValue.GenerationDigest
	for _, domain := range publication.rootValue.Authority.Upstream.Domains {
		if err := pins.PinPartitionedExtractionRun(ctx, domain.RunID, owner); err != nil {
			return false, recoveryStoreError{err}
		}
	}
	pointerRaw, err := json.Marshal(marker.Pointer)
	if err != nil {
		return false, err
	}
	if err := replaceFile(filepath.Join(base, "current.json"), pointerRaw); err != nil {
		return false, err
	}
	if err := removePublishingTemporaryV3(base); err != nil {
		return false, err
	}
	if err := os.Remove(filepath.Join(base, "publishing.json")); err != nil {
		return false, err
	}
	return true, syncDirectory(base)
}

func ReadPointerV3(ctx context.Context, root, repository string) (PointerV3, error) {
	if err := ctx.Err(); err != nil {
		return PointerV3{}, err
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		return PointerV3{}, err
	}
	if err := validateDirectory(base); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PointerV3{}, ErrNotFound
		}
		return PointerV3{}, err
	}
	raw, err := readRegular(filepath.Join(base, "current.json"), MaxRootBytesV3)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PointerV3{}, ErrNotFound
		}
		return PointerV3{}, err
	}
	var pointer PointerV3
	if err := decodeExact(raw, MaxRootBytesV3, &pointer); err != nil ||
		validatePointerV3(pointer) != nil || pointer.Repository != repository {
		return PointerV3{}, fmt.Errorf("%w: v3 current pointer", ErrInvalid)
	}
	canonical, _ := json.Marshal(pointer)
	if !slices.Equal(raw, canonical) {
		return PointerV3{}, fmt.Errorf("%w: v3 current pointer bytes", ErrInvalid)
	}
	return pointer, nil
}

func OpenCurrentV3(ctx context.Context, root, repository string) (*PublicationV3, error) {
	pointer, err := ReadPointerV3(ctx, root, repository)
	if err != nil {
		return nil, err
	}
	publication, err := OpenGenerationV3(
		ctx, root, repository, pointer.GenerationDigest, pointer.RootDigest,
	)
	if err != nil {
		return nil, err
	}
	base, _ := RepositoryRootV3(root, repository)
	publication.base = base
	publication.pointer = pointer
	publication.pointerRaw, _ = json.Marshal(pointer)
	return publication, nil
}

// OpenGenerationV3 opens only the bounded control root. Selected repository
// and service members remain sparse reads; callers needing a complete audit
// use ValidateGenerationV3.
func OpenGenerationV3(
	ctx context.Context,
	root, repository, generation, rootDigestValue string,
) (*PublicationV3, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := GenerationPathV3(root, repository, generation)
	if err != nil || !validDigest(rootDigestValue) {
		return nil, fmt.Errorf("%w: v3 generation lookup", ErrInvalid)
	}
	if err := validateDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	raw, err := readRegular(filepath.Join(directory, "root.json"), MaxRootBytesV3)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var value RootV3
	if err := decodeExact(raw, MaxRootBytesV3, &value); err != nil ||
		ValidateRootV3(value) != nil || value.Authority.Repository != repository ||
		value.GenerationDigest != generation || value.Digest != rootDigestValue {
		return nil, fmt.Errorf("%w: v3 generation root", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: v3 generation root canonical bytes", ErrInvalid)
	}
	return &PublicationV3{directory: directory, rootValue: value}, nil
}

func ValidateGenerationV3(
	ctx context.Context,
	root, repository, generation, rootDigestValue string,
) (*PublicationV3, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := GenerationPathV3(root, repository, generation)
	if err != nil || !validDigest(rootDigestValue) {
		return nil, fmt.Errorf("%w: v3 generation lookup", ErrInvalid)
	}
	return openDirectoryCompleteIdentityV3(
		ctx, directory, repository, generation, rootDigestValue, true,
	)
}

func (publication *PublicationV3) Root() RootV3 {
	if publication == nil {
		return RootV3{}
	}
	return cloneRootV3(publication.rootValue)
}

func (publication *PublicationV3) ReadService(
	ctx context.Context,
	serviceKey string,
) (ServiceRecordV3, error) {
	if publication == nil || !validText(serviceKey) {
		return ServiceRecordV3{}, fmt.Errorf("%w: v3 service lookup", ErrInvalid)
	}
	index := slices.IndexFunc(publication.rootValue.ServiceMembers, func(
		receipt ServiceRangeReceiptV3,
	) bool {
		return receipt.LastKey >= serviceKey
	})
	if index < 0 || serviceKey < publication.rootValue.ServiceMembers[index].FirstKey {
		return ServiceRecordV3{}, ErrNotFound
	}
	member, err := publication.openServiceMemberV3(ctx, publication.rootValue.ServiceMembers[index])
	if err != nil {
		return ServiceRecordV3{}, err
	}
	recordIndex, found := slices.BinarySearchFunc(
		member.Services, serviceKey,
		func(value ServiceRecordV3, key string) int { return strings.Compare(value.ServiceKey, key) },
	)
	if !found {
		return ServiceRecordV3{}, fmt.Errorf("%w: v3 service range gap", ErrInvalid)
	}
	record := cloneServiceRecordV3(member.Services[recordIndex])
	if record.State == "failed" {
		return record, ErrServiceUnavailable
	}
	return record, nil
}

func (publication *PublicationV3) OpenService(
	ctx context.Context,
	serviceKey string,
) (ServiceRecordV3, error) {
	record, err := publication.ReadService(ctx, serviceKey)
	if err != nil {
		return record, err
	}
	if err := publication.ConfirmCurrent(); err != nil {
		return ServiceRecordV3{}, err
	}
	return record, nil
}

func (publication *PublicationV3) ReadProjection(
	ctx context.Context,
	digest string,
) (Projection, error) {
	if publication == nil || !validDigest(digest) {
		return Projection{}, fmt.Errorf("%w: v3 projection lookup", ErrInvalid)
	}
	bucket := projectionBucket(digest)
	index, found := slices.BinarySearchFunc(
		publication.rootValue.RepositoryMembers, bucket,
		func(value RepositoryReceiptV3, target int) int { return value.Bucket - target },
	)
	if !found {
		return Projection{}, ErrNotFound
	}
	member, err := publication.openRepositoryMemberV3(
		ctx, publication.rootValue.RepositoryMembers[index],
	)
	if err != nil {
		return Projection{}, err
	}
	fragmentIndex := slices.IndexFunc(member.Fragments, func(value ProjectionBucketV3) bool {
		return value.ProjectionDigest >= digest
	})
	if fragmentIndex < 0 || member.Fragments[fragmentIndex].ProjectionDigest != digest ||
		member.Fragments[fragmentIndex].Ordinal != 0 {
		return Projection{}, ErrNotFound
	}
	count := member.Fragments[fragmentIndex].Count
	if fragmentIndex+count > len(member.Fragments) {
		return Projection{}, fmt.Errorf("%w: v3 projection fragment range", ErrInvalid)
	}
	return flattenProjectionBucketsV3(member.Fragments[fragmentIndex : fragmentIndex+count])
}

func (publication *PublicationV3) ConfirmCurrent() error {
	if publication == nil || publication.base == "" || len(publication.pointerRaw) == 0 {
		return fmt.Errorf("%w: v3 current confirmation", ErrInvalid)
	}
	raw, err := readRegular(filepath.Join(publication.base, "current.json"), MaxRootBytesV3)
	if err != nil || !bytes.Equal(raw, publication.pointerRaw) {
		return ErrPublishing
	}
	return nil
}

func openDirectoryCompleteV3(
	ctx context.Context,
	directory string,
	expected RootV3,
) (*PublicationV3, error) {
	return openDirectoryCompleteIdentityV3(
		ctx, directory, expected.Authority.Repository,
		expected.GenerationDigest, expected.Digest, false,
	)
}

func openDirectoryCompleteIdentityV3(
	ctx context.Context,
	directory, repository, generation, rootDigestValue string,
	missingControlIsNotFound bool,
) (*PublicationV3, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		if missingControlIsNotFound && errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("stat v3 generation directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: v3 generation directory", ErrInvalid)
	}
	raw, err := readRegular(filepath.Join(directory, "root.json"), MaxRootBytesV3)
	if err != nil {
		if missingControlIsNotFound && errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var value RootV3
	if err := decodeExact(raw, MaxRootBytesV3, &value); err != nil ||
		ValidateRootV3(value) != nil || value.Authority.Repository != repository ||
		value.GenerationDigest != generation || value.Digest != rootDigestValue {
		return nil, fmt.Errorf("%w: complete v3 root", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: noncanonical v3 root", ErrInvalid)
	}
	publication := &PublicationV3{directory: directory, rootValue: value}
	wanted := map[string]struct{}{"root.json": {}}
	for _, receipt := range value.RepositoryMembers {
		wanted[receipt.Name] = struct{}{}
	}
	for _, receipt := range value.ServiceMembers {
		wanted[receipt.Name] = struct{}{}
	}
	entries, err := readGenerationInventoryV3(directory, info)
	if err != nil {
		return nil, fmt.Errorf("read v3 generation inventory: %w", err)
	}
	if len(entries) != len(wanted) {
		return nil, fmt.Errorf("%w: v3 generation inventory", ErrInvalid)
	}
	for _, entry := range entries {
		if _, present := wanted[entry.Name()]; !present || entry.IsDir() ||
			entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: unexpected v3 generation entry", ErrInvalid)
		}
	}
	serviceSet := make([]serviceSetIdentityV3, 0, value.ServiceCount)
	serviceRecords := make(map[string]*serviceValidationV3, value.ServiceCount)
	var services, complete, empty, failed, references int
	var serviceBytes int64
	for _, receipt := range value.ServiceMembers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		member, err := publication.openServiceMemberV3(ctx, receipt)
		if err != nil {
			return nil, err
		}
		services += len(member.Services)
		serviceBytes += receipt.ContentBytes
		for _, record := range member.Services {
			if _, duplicate := serviceRecords[record.ServiceKey]; duplicate {
				return nil, fmt.Errorf("%w: duplicate v3 service", ErrInvalid)
			}
			validation := &serviceValidationV3{state: record.State}
			for _, reference := range record.References {
				validation.actual.add(reference.Digest)
			}
			serviceRecords[record.ServiceKey] = validation
			serviceSet = append(serviceSet, serviceSetIdentityV3{
				ServiceKey: record.ServiceKey, Incarnation: record.Incarnation,
				DesiredGeneration: record.ServiceGeneration,
			})
			switch record.State {
			case "complete":
				complete++
			case "empty":
				empty++
			case "failed":
				failed++
			}
			references += len(record.References)
		}
	}
	var projections, fragments int
	var repositoryBytes int64
	for _, receipt := range value.RepositoryMembers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		member, err := publication.openRepositoryMemberV3(ctx, receipt)
		if err != nil {
			return nil, err
		}
		fragments += len(member.Fragments)
		repositoryBytes += receipt.ContentBytes
		for index := 0; index < len(member.Fragments); {
			count := member.Fragments[index].Count
			projection, flattenErr := flattenProjectionBucketsV3(member.Fragments[index : index+count])
			if flattenErr != nil {
				return nil, flattenErr
			}
			projections++
			if joinErr := accumulateProjectionReferencesV3(projection, serviceRecords); joinErr != nil {
				return nil, joinErr
			}
			index += count
		}
	}
	wantServiceSet, _ := digestServiceSetV3(serviceSet)
	if projections != value.ProjectionCount || fragments != value.ProjectionFragmentCount ||
		repositoryBytes != value.EncodedRepositoryBytes || services != value.ServiceCount ||
		complete != value.CompleteServiceCount || empty != value.EmptyServiceCount ||
		failed != value.FailedServiceCount || references != value.ServiceReferenceCount ||
		serviceBytes != value.EncodedServiceBytes ||
		wantServiceSet != value.Authority.ServiceStateSetDigest {
		return nil, fmt.Errorf("%w: v3 generation totals", ErrInvalid)
	}
	for _, validation := range serviceRecords {
		if validation.state != "failed" && validation.actual != validation.expected {
			return nil, fmt.Errorf("%w: v3 service reference join", ErrInvalid)
		}
	}
	return publication, nil
}

func readGenerationInventoryV3(
	directory string,
	expected os.FileInfo,
) ([]os.DirEntry, error) {
	opened, err := os.Open(directory)
	if err != nil {
		return nil, err
	}
	info, statErr := opened.Stat()
	if statErr != nil || !os.SameFile(expected, info) || !info.IsDir() {
		_ = opened.Close()
		if statErr != nil {
			return nil, statErr
		}
		return nil, fmt.Errorf("%w: v3 generation directory identity", ErrInvalid)
	}
	entries, readErr := opened.ReadDir(MaxGenerationFilesV3 + 1)
	closeErr := opened.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > MaxGenerationFilesV3 {
		// This is immutable generation shape corruption, not exhaustion of
		// RecoverAll's independent global work budget.
		return nil, fmt.Errorf("%w: v3 generation inventory limit", ErrInvalid)
	}
	return entries, nil
}

func (publication *PublicationV3) openRepositoryMemberV3(
	ctx context.Context,
	receipt RepositoryReceiptV3,
) (RepositoryMemberV3, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryMemberV3{}, err
	}
	raw, err := readRegular(
		filepath.Join(publication.directory, receipt.Name), MaxRepositoryMemberBytes,
	)
	if err != nil {
		return RepositoryMemberV3{}, fmt.Errorf("read v3 repository member: %w", err)
	}
	if int64(len(raw)) != receipt.ContentBytes {
		return RepositoryMemberV3{}, fmt.Errorf("%w: v3 repository member bytes", ErrInvalid)
	}
	var value RepositoryMemberV3
	if err := decodeExact(raw, MaxRepositoryMemberBytes, &value); err != nil ||
		validateRepositoryMemberV3(value) != nil || value.Bucket != receipt.Bucket ||
		len(value.Fragments) != receipt.FragmentCount || value.Digest != receipt.ContentDigest {
		return RepositoryMemberV3{}, fmt.Errorf("%w: v3 repository member", ErrInvalid)
	}
	projections := 0
	for index := 0; index < len(value.Fragments); {
		projections++
		index += value.Fragments[index].Count
	}
	if projections != receipt.ProjectionCount {
		return RepositoryMemberV3{}, fmt.Errorf("%w: v3 repository projection count", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return RepositoryMemberV3{}, fmt.Errorf("%w: v3 repository canonical bytes", ErrInvalid)
	}
	return value, nil
}

func (publication *PublicationV3) openServiceMemberV3(
	ctx context.Context,
	receipt ServiceRangeReceiptV3,
) (ServiceMemberV3, error) {
	if err := ctx.Err(); err != nil {
		return ServiceMemberV3{}, err
	}
	raw, err := readRegular(
		filepath.Join(publication.directory, receipt.Name), MaxServiceMemberBytes,
	)
	if err != nil {
		return ServiceMemberV3{}, fmt.Errorf("read v3 service member: %w", err)
	}
	if int64(len(raw)) != receipt.ContentBytes {
		return ServiceMemberV3{}, fmt.Errorf("%w: v3 service member bytes", ErrInvalid)
	}
	var value ServiceMemberV3
	if err := decodeExact(raw, MaxServiceMemberBytes, &value); err != nil ||
		validateServiceMemberV3(value) != nil || value.Ordinal != receipt.Ordinal ||
		value.Count != receipt.Count || value.FirstKey != receipt.FirstKey ||
		value.LastKey != receipt.LastKey || len(value.Services) != receipt.ServiceCount ||
		value.Digest != receipt.ContentDigest {
		return ServiceMemberV3{}, fmt.Errorf("%w: v3 service member", ErrInvalid)
	}
	var complete, empty, failed, references int
	for _, record := range value.Services {
		switch record.State {
		case "complete":
			complete++
		case "empty":
			empty++
		case "failed":
			failed++
		}
		references += len(record.References)
	}
	if complete != receipt.CompleteCount || empty != receipt.EmptyCount ||
		failed != receipt.FailedCount || references != receipt.ReferenceCount {
		return ServiceMemberV3{}, fmt.Errorf("%w: v3 service member totals", ErrInvalid)
	}
	canonical, _ := json.Marshal(value)
	if !slices.Equal(raw, canonical) {
		return ServiceMemberV3{}, fmt.Errorf("%w: v3 service member canonical bytes", ErrInvalid)
	}
	return value, nil
}

type referenceAccumulatorV3 struct {
	Count uint64
	Sum   [sha256.Size]byte
}

type serviceValidationV3 struct {
	state    string
	actual   referenceAccumulatorV3
	expected referenceAccumulatorV3
}

func (value *referenceAccumulatorV3) add(referenceDigest string) {
	item := decodeDigestV3(referenceDigest)
	carry := uint16(0)
	for index := len(value.Sum) - 1; index >= 0; index-- {
		sum := uint16(value.Sum[index]) + uint16(item[index]) + carry
		value.Sum[index] = byte(sum)
		carry = sum >> 8
	}
	value.Count++
}

func decodeDigestV3(value string) [sha256.Size]byte {
	var result [sha256.Size]byte
	encoded := strings.TrimPrefix(value, "sha256:")
	for index := range result {
		result[index] = hexNibbleV3(encoded[index*2])<<4 | hexNibbleV3(encoded[index*2+1])
	}
	return result
}

func hexNibbleV3(value byte) byte {
	if value >= '0' && value <= '9' {
		return value - '0'
	}
	return value - 'a' + 10
}

func accumulateProjectionReferencesV3(
	projection Projection,
	services map[string]*serviceValidationV3,
) error {
	participation := make(map[string][]string)
	for _, claim := range projection.Source.Claims {
		if claim.Disposition == "accepted" {
			participation[claim.ServiceKey] = append(participation[claim.ServiceKey], "source")
		}
	}
	if projection.Target != nil {
		for _, claim := range projection.Target.Claims {
			if claim.Disposition == "accepted" {
				participation[claim.ServiceKey] = append(participation[claim.ServiceKey], "target")
			}
		}
	}
	for serviceKey, roles := range participation {
		service := services[serviceKey]
		if service == nil {
			return fmt.Errorf("%w: v3 projection references absent service", ErrInvalid)
		}
		if service.state == "failed" {
			continue
		}
		slices.Sort(roles)
		roles = slices.Compact(roles)
		reference := ServiceReference{
			Schema: ServiceReferenceSchema, ProjectionDigest: projection.Digest,
			PostingDigest: projection.PostingDigest, Kind: projection.Kind,
			Plane: projection.Plane, LookupKey: projection.LookupKey,
			Participation: roles,
		}
		reference.Digest, _ = digestValue(reference)
		service.expected.add(reference.Digest)
	}
	return nil
}

func cloneServiceRecordV3(value ServiceRecordV3) ServiceRecordV3 {
	value.References = slices.Clone(value.References)
	for index := range value.References {
		value.References[index].Participation = slices.Clone(value.References[index].Participation)
	}
	return value
}

func newPointerV3(value RootV3) (PointerV3, error) {
	pointer := PointerV3{
		Schema: PointerSchemaV3, Repository: value.Authority.Repository,
		GenerationDigest: value.GenerationDigest, RootDigest: value.Digest, RootName: "root.json",
	}
	pointer.Digest, _ = digestValue(pointer)
	return pointer, validatePointerV3(pointer)
}

func validatePointerV3(value PointerV3) error {
	if value.Schema != PointerSchemaV3 || reponame.Validate(value.Repository) != nil ||
		!validDigest(value.GenerationDigest) || !validDigest(value.RootDigest) ||
		value.RootName != "root.json" || !validDigest(value.Digest) {
		return fmt.Errorf("%w: v3 pointer", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	want, _ := digestValue(copyValue)
	if want != value.Digest {
		return fmt.Errorf("%w: v3 pointer digest", ErrInvalid)
	}
	return nil
}

func validateMarkerV3(value MarkerV3) error {
	if value.Schema != MarkerSchemaV3 || validatePointerV3(value.Pointer) != nil ||
		!validMarkerStageNameV3(value.StageName) || !validDigest(value.Digest) {
		return fmt.Errorf("%w: v3 marker", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	want, _ := digestValue(copyValue)
	if want != value.Digest {
		return fmt.Errorf("%w: v3 marker digest", ErrInvalid)
	}
	return nil
}

func publicationControlsV3(
	pointer PointerV3,
	stageName string,
) (MarkerV3, []byte, []byte, error) {
	marker := MarkerV3{Schema: MarkerSchemaV3, Pointer: pointer, StageName: stageName}
	marker.Digest, _ = digestValue(marker)
	if err := validateMarkerV3(marker); err != nil {
		return MarkerV3{}, nil, nil, err
	}
	markerRaw, err := json.Marshal(marker)
	if err != nil {
		return MarkerV3{}, nil, nil, err
	}
	pointerRaw, err := json.Marshal(pointer)
	if err != nil {
		return MarkerV3{}, nil, nil, err
	}
	return marker, markerRaw, pointerRaw, nil
}

func validMarkerStageNameV3(value string) bool {
	if value == "" {
		return true
	}
	return len(value) > len(".stage-") && len(value) <= 128 &&
		strings.HasPrefix(value, ".stage-") && filepath.Base(value) == value &&
		!strings.ContainsAny(value, `/\\`)
}

func openStagedGenerationCompleteV3(
	ctx context.Context,
	directory string,
	pointer PointerV3,
) (*PublicationV3, error) {
	return openDirectoryCompleteIdentityV3(
		ctx, directory, pointer.Repository,
		pointer.GenerationDigest, pointer.RootDigest, false,
	)
}

func markerPresentV3(base string) bool {
	_, err := os.Lstat(filepath.Join(base, "publishing.json"))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func ShadowBase(root string) string {
	return filepath.Join(root, RelationshipPublicationsV3Shadow)
}

func RepositoryRootV3(root, repository string) (string, error) {
	if !filepath.IsAbs(root) || reponame.Validate(repository) != nil {
		return "", fmt.Errorf("%w: v3 repository path", ErrInvalid)
	}
	sum := sha256.Sum256([]byte(repository))
	return filepath.Join(ShadowBase(root), hex.EncodeToString(sum[:])), nil
}

func GenerationPathV3(root, repository, generation string) (string, error) {
	base, err := RepositoryRootV3(root, repository)
	if err != nil || !validDigest(generation) {
		return "", fmt.Errorf("%w: v3 generation path", ErrInvalid)
	}
	return filepath.Join(base, strings.TrimPrefix(generation, "sha256:")), nil
}

func GenerationFilesV3(root RootV3) ([]string, error) {
	if err := ValidateRootV3(root); err != nil {
		return nil, err
	}
	result := make([]string, 0, 1+len(root.RepositoryMembers)+len(root.ServiceMembers))
	result = append(result, "root.json")
	for _, receipt := range root.RepositoryMembers {
		result = append(result, receipt.Name)
	}
	for _, receipt := range root.ServiceMembers {
		result = append(result, receipt.Name)
	}
	if len(result) > MaxGenerationFilesV3 {
		return nil, ErrLimit
	}
	return result, nil
}

func ensureRootV3(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%w: absolute v3 root required", ErrInvalid)
	}
	if err := validateDirectory(root); err != nil {
		return err
	}
	base := ShadowBase(root)
	if err := os.Mkdir(base, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validateDirectory(base)
}

func stageDirectoryV3(root, repository string) (string, error) {
	if err := ensureRootV3(root); err != nil {
		return "", err
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		return "", err
	}
	if err := os.Mkdir(base, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err := validateDirectory(base); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, ".stage-")
}
