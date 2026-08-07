package observationpublication

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

type InventoryBuildRequestV2 struct {
	OutputDirectory     string
	RepositoryDirectory string
	Plan                *sourcepartition.SuperPlan
	PriorDirectory      string
	Metrics             *InventoryMetricsV2
}

type InventoryMetricsV2 struct {
	ReadBlobs          int
	ParsedBlobs        int
	ReusedMembers      int
	ReusedObservations int
	WrittenMemberBytes int64
	LinkedMemberBytes  int64
}

type inventorySourceMemberV2 struct {
	segment int
	ordinal int
	member  sourcepartition.Member
	plan    *sourcepartition.Plan
}

var inventoryBuildStripes [64]sync.Mutex

func BuildInventoryStageV2(ctx context.Context, request InventoryBuildRequestV2) (InventoryRootV2, error) {
	if request.Metrics != nil {
		*request.Metrics = InventoryMetricsV2{}
	}
	metrics := request.Metrics
	if metrics == nil {
		metrics = &InventoryMetricsV2{}
	}
	if request.Plan == nil || !request.Plan.Complete() || !filepath.IsAbs(request.OutputDirectory) ||
		!filepath.IsAbs(request.RepositoryDirectory) {
		return InventoryRootV2{}, invalid("inventory v2 build input")
	}
	stripe := sha256.Sum256([]byte(filepath.Clean(request.OutputDirectory)))
	buildLock := &inventoryBuildStripes[int(stripe[0])%len(inventoryBuildStripes)]
	buildLock.Lock()
	defer buildLock.Unlock()
	source := request.Plan.Root()
	if existing, err := ReadInventoryRootV2(request.OutputDirectory, source.Repository); err == nil {
		if existing.SourcePartitionRootDigest != source.Digest {
			return InventoryRootV2{}, invalid("inventory v2 output belongs to another source root")
		}
		if err := ValidateInventoryStageV2(ctx, request.OutputDirectory, existing); err != nil {
			return InventoryRootV2{}, err
		}
		if err := ValidateInventorySourceV2(request.OutputDirectory, existing, request.Plan); err != nil {
			return InventoryRootV2{}, err
		}
		return existing, nil
	}
	if err := os.MkdirAll(request.OutputDirectory, 0o700); err != nil {
		return InventoryRootV2{}, err
	}
	if err := validateInventoryBuildNamespaceV2(request.OutputDirectory); err != nil {
		return InventoryRootV2{}, err
	}
	members, err := inventorySourceMembersV2(request.Plan)
	if err != nil {
		return InventoryRootV2{}, err
	}
	groups, err := packInventoryMembersV2(members)
	if err != nil {
		return InventoryRootV2{}, err
	}
	generation, err := InventoryGenerationDigestV2(source)
	if err != nil {
		return InventoryRootV2{}, err
	}
	var prior *InventoryPublicationV2
	if request.PriorDirectory != "" {
		if !filepath.IsAbs(request.PriorDirectory) {
			return InventoryRootV2{}, invalid("inventory v2 prior directory")
		}
		priorRoot, readErr := ReadInventoryRootV2(request.PriorDirectory, source.Repository)
		if readErr != nil {
			return InventoryRootV2{}, readErr
		}
		prior, err = OpenInventoryV2(ctx, request.PriorDirectory, priorRoot)
		if err != nil {
			return InventoryRootV2{}, err
		}
	}
	root := InventoryRootV2{
		Schema: InventoryRootSchemaV2, Repository: source.Repository, Language: LanguagePack,
		SourceGenerationDigest:    source.SourceGenerationDigest,
		SourcePartitionGeneration: source.GenerationDigest,
		SourcePartitionRootDigest: source.Digest, PartitionPolicyDigest: source.PolicyDigest,
		ObservationPolicyDigest: sourceobservation.PolicyDigest(), InventoryPolicy: frozenInventoryPolicyV2(),
		InventoryPolicyDigest: inventoryPolicyDigestV2(), GenerationDigest: generation,
		Segments: []InventorySegmentEntryV2{}, OperationReceipt: emptyOperationReceipt(),
	}
	for ordinal, group := range groups {
		if err := ctx.Err(); err != nil {
			return InventoryRootV2{}, err
		}
		segmentDirectory := filepath.Join(request.OutputDirectory, inventorySegmentDirectoryV2(ordinal))
		segment, segmentErr := reuseCompletedInventorySegmentV2(
			ctx, segmentDirectory, root, ordinal, len(groups), group,
		)
		if segmentErr != nil {
			if err := os.RemoveAll(segmentDirectory); err != nil {
				return InventoryRootV2{}, err
			}
			segment, err = buildInventorySegmentV2(
				ctx, request, root, ordinal, len(groups), group, prior, metrics,
			)
			if err != nil {
				return InventoryRootV2{}, err
			}
		}
		entry := inventorySegmentEntryV2(segment)
		if root.RecordCount > MaxInventoryRecordsV2-entry.RecordCount {
			return InventoryRootV2{}, checkPublicationLimit(
				ErrLimit, pipelinerefusal.DimensionGenerationRecords,
				int64(root.RecordCount+entry.RecordCount), MaxInventoryRecordsV2,
			)
		}
		if root.EncodedMemberBytes > MaxGenerationBytes-entry.EncodedMemberBytes {
			return InventoryRootV2{}, checkPublicationLimit(
				ErrLimit, pipelinerefusal.DimensionGenerationEncodedBytes,
				root.EncodedMemberBytes+entry.EncodedMemberBytes, MaxGenerationBytes,
			)
		}
		if root.ObservationBytes > MaxGenerationBytes-entry.ObservationBytes {
			return InventoryRootV2{}, checkPublicationLimit(
				ErrLimit, pipelinerefusal.DimensionGenerationArtifactBytes,
				root.ObservationBytes+entry.ObservationBytes, MaxGenerationBytes,
			)
		}
		root.Segments = append(root.Segments, entry)
		root.MemberCount += entry.MemberCount
		root.RecordCount += entry.RecordCount
		root.ObservedCount += entry.ObservedCount
		root.UnsupportedCount += entry.UnsupportedCount
		root.EncodedMemberBytes += entry.EncodedMemberBytes
		root.ObservationCount += entry.ObservationCount
		root.ObservationBytes += entry.ObservationBytes
		mergeOperationReceipt(&root.OperationReceipt, segment.OperationReceipt)
	}
	root.Digest, err = InventoryRootDigestV2(root)
	if err != nil || ValidateInventoryRootV2(root) != nil {
		return InventoryRootV2{}, errors.Join(err, invalid("inventory v2 built root"))
	}
	if err := writeExclusiveCanonical(filepath.Join(request.OutputDirectory, InventoryRootNameV2), root); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return InventoryRootV2{}, err
		}
		existing, readErr := ReadInventoryRootV2(request.OutputDirectory, source.Repository)
		if readErr != nil || existing.Digest != root.Digest {
			return InventoryRootV2{}, errors.Join(readErr, invalid("inventory v2 concurrent root mismatch"))
		}
		root = existing
	}
	if err := syncDirectory(request.OutputDirectory); err != nil {
		return InventoryRootV2{}, err
	}
	if err := ValidateInventoryStageV2(ctx, request.OutputDirectory, root); err != nil {
		return InventoryRootV2{}, err
	}
	if err := ValidateInventorySourceV2(request.OutputDirectory, root, request.Plan); err != nil {
		return InventoryRootV2{}, err
	}
	return root, nil
}

func inventorySourceMembersV2(plan *sourcepartition.SuperPlan) ([]inventorySourceMemberV2, error) {
	root := plan.Root()
	result := make([]inventorySourceMemberV2, 0, root.MemberCount)
	for segment := range root.Segments {
		segmentPlan, err := plan.Segment(segment)
		if err != nil {
			return nil, err
		}
		manifest := segmentPlan.Manifest()
		for ordinal, member := range manifest.Members {
			result = append(result, inventorySourceMemberV2{
				segment: segment, ordinal: ordinal, member: member, plan: segmentPlan,
			})
		}
	}
	if len(result) != root.MemberCount {
		return nil, invalid("inventory v2 source member count")
	}
	return result, nil
}

func packInventoryMembersV2(members []inventorySourceMemberV2) ([][]inventorySourceMemberV2, error) {
	if len(members) == 0 {
		return nil, nil
	}
	groups := [][]inventorySourceMemberV2{{}}
	records := 0
	for _, member := range members {
		if member.member.BlobCount > MaxInventorySegmentRecords {
			return nil, invalid("inventory v2 source member exceeds segment")
		}
		if records > MaxInventorySegmentRecords-member.member.BlobCount {
			if len(groups) >= MaxInventorySegmentsV2 {
				return nil, checkPublicationLimit(
					ErrLimit, pipelinerefusal.DimensionAggregateSegments,
					int64(len(groups)+1), MaxInventorySegmentsV2,
				)
			}
			groups = append(groups, []inventorySourceMemberV2{})
			records = 0
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], member)
		records += member.member.BlobCount
	}
	return groups, nil
}

func buildInventorySegmentV2(
	ctx context.Context, request InventoryBuildRequestV2, root InventoryRootV2,
	ordinal, count int, group []inventorySourceMemberV2,
	prior *InventoryPublicationV2, metrics *InventoryMetricsV2,
) (InventorySegmentV2, error) {
	directory := filepath.Join(request.OutputDirectory, inventorySegmentDirectoryV2(ordinal))
	if err := os.Mkdir(directory, 0o700); err != nil {
		return InventorySegmentV2{}, err
	}
	if err := os.Mkdir(filepath.Join(directory, "objects"), 0o700); err != nil {
		return InventorySegmentV2{}, err
	}
	var priorSegment *InventorySegmentV2
	priorSegmentDirectory := ""
	priorMembers := map[string]int{}
	if prior != nil && ordinal < len(prior.root.Segments) {
		entry := prior.root.Segments[ordinal]
		opened, openErr := readInventorySegmentV2(prior.directory, prior.root, entry)
		if openErr != nil {
			return InventorySegmentV2{}, openErr
		}
		priorSegment = &opened
		priorSegmentDirectory = filepath.Join(prior.directory, entry.Directory)
		for index, member := range opened.Members {
			priorMembers[member.SourceMemberDigest] = index
		}
	}
	for local, source := range group {
		if err := buildInventoryMemberV2(
			ctx, request, root.Repository, directory, local, source,
			priorSegment, priorSegmentDirectory, priorMembers, metrics,
		); err != nil {
			return InventorySegmentV2{}, err
		}
	}
	segment := InventorySegmentV2{
		Schema: InventorySegmentSchemaV2, Repository: root.Repository,
		GenerationDigest: root.GenerationDigest, SourcePartitionRootDigest: root.SourcePartitionRootDigest,
		ObservationPolicyDigest: root.ObservationPolicyDigest, Ordinal: ordinal, Count: count,
		Members: []InventoryMemberV2{}, OperationReceipt: emptyOperationReceipt(),
	}
	objects := make(map[string]struct{})
	receipt := newReceiptAccumulator()
	for local, source := range group {
		member, records, err := readMember(ctx, directory, local, len(group))
		if err != nil {
			return InventorySegmentV2{}, err
		}
		member.SourceMemberDigest = source.member.Digest
		if member.RecordCount != source.member.BlobCount || records[0].ObjectID != source.member.FirstObjectID ||
			records[len(records)-1].ObjectID != source.member.LastObjectID {
			return InventorySegmentV2{}, invalid("inventory v2 built member source binding")
		}
		control := InventoryMemberV2{
			Ordinal: local, Count: len(group), Name: inventoryMemberNameV2(local),
			SourceSegment: source.segment, SourceOrdinal: source.ordinal,
			SourceMemberDigest: source.member.Digest, SourcePrefix: source.member.Prefix,
			SourcePrefixBits: source.member.PrefixBits, RecordCount: member.RecordCount,
			ObservedCount: member.ObservedCount, UnsupportedCount: member.UnsupportedCount,
			ContentBytes: member.ContentBytes, Digest: member.Digest,
		}
		segment.Members = append(segment.Members, control)
		segment.RecordCount += member.RecordCount
		segment.ObservedCount += member.ObservedCount
		segment.UnsupportedCount += member.UnsupportedCount
		segment.EncodedMemberBytes += member.ContentBytes
		for _, record := range records {
			if err := receipt.add(record); err != nil {
				return InventorySegmentV2{}, err
			}
			if record.State == "observed" {
				objects[record.ObservationName] = struct{}{}
			}
		}
	}
	segment.OperationReceipt = *receipt.value()
	segment.ObservationCount = len(objects)
	for name := range objects {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxObservationBytes {
			return InventorySegmentV2{}, invalid("inventory v2 built object")
		}
		segment.ObservationBytes += info.Size()
	}
	var err error
	segment.Digest, err = inventorySegmentDigestV2(segment)
	if err != nil {
		return InventorySegmentV2{}, err
	}
	entry := inventorySegmentEntryV2(segment)
	provisional := root
	provisional.Segments = make([]InventorySegmentEntryV2, count)
	provisional.Segments[ordinal] = entry
	if validateInventorySegmentControlV2(provisional, entry, segment) != nil {
		return InventorySegmentV2{}, invalid("inventory v2 built segment")
	}
	if err := writeExclusiveCanonical(filepath.Join(directory, "segment.json"), segment); err != nil {
		return InventorySegmentV2{}, err
	}
	if err := syncDirectory(filepath.Join(directory, "objects")); err != nil {
		return InventorySegmentV2{}, err
	}
	return segment, syncDirectory(directory)
}

func buildInventoryMemberV2(
	ctx context.Context, request InventoryBuildRequestV2, repository, directory string,
	local int, source inventorySourceMemberV2,
	prior *InventorySegmentV2, priorDirectory string, priorMembers map[string]int,
	metrics *InventoryMetricsV2,
) error {
	destination := filepath.Join(directory, inventoryMemberNameV2(local))
	if priorMember, priorOrdinal, ok := reusableInventoryMemberV2(prior, priorMembers, source.member.Digest); ok {
		if err := os.Link(filepath.Join(priorDirectory, priorMember.Name), destination); err != nil {
			return err
		}
		_, records, err := readMember(ctx, priorDirectory, priorOrdinal, priorMember.Count)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.State != "observed" {
				continue
			}
			if err := os.Link(filepath.Join(priorDirectory, record.ObservationName), filepath.Join(directory, record.ObservationName)); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
		}
		metrics.ReusedMembers++
		metrics.LinkedMemberBytes += priorMember.ContentBytes
		metrics.ReusedObservations += priorMember.ObservedCount
		return nil
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(destination)
		}
	}()
	writer := bufio.NewWriterSize(file, 64<<10)
	var contentBytes int64
	observer := &Stage{
		root: filepath.Join(request.OutputDirectory, ".no-v1-authority"), repository: repository,
		directory: directory,
	}
	stats, err := source.plan.ReadPartition(
		ctx, request.RepositoryDirectory, source.ordinal,
		func(blob sourcepartition.BlobRecord, content []byte) error {
			metrics.ReadBlobs++
			v1Metrics := &Metrics{}
			record, _, err := observer.observe(ctx, blob, content, v1Metrics)
			if err != nil {
				return err
			}
			metrics.ParsedBlobs += v1Metrics.ParsedBlobs
			line, err := json.Marshal(record)
			if err != nil || int64(len(line)+1) > MaxMemberBytes-contentBytes {
				return errors.Join(err, invalid("inventory v2 member bytes"))
			}
			if _, err := writer.Write(line); err != nil {
				return err
			}
			if err := writer.WriteByte('\n'); err != nil {
				return err
			}
			contentBytes += int64(len(line) + 1)
			return nil
		},
	)
	if err != nil || stats.Blobs != source.member.BlobCount {
		return errors.Join(err, invalid("inventory v2 source totals"))
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	metrics.WrittenMemberBytes += contentBytes
	return nil
}

func reusableInventoryMemberV2(
	prior *InventorySegmentV2, members map[string]int, sourceDigest string,
) (InventoryMemberV2, int, bool) {
	ordinal, ok := members[sourceDigest]
	if prior == nil || !ok || ordinal < 0 || ordinal >= len(prior.Members) {
		return InventoryMemberV2{}, 0, false
	}
	return prior.Members[ordinal], ordinal, true
}

func reuseCompletedInventorySegmentV2(
	ctx context.Context, directory string, root InventoryRootV2,
	ordinal, count int, group []inventorySourceMemberV2,
) (InventorySegmentV2, error) {
	raw, err := readBoundedRegular(filepath.Join(directory, "segment.json"), MaxInventorySegmentBytesV2)
	if err != nil {
		return InventorySegmentV2{}, err
	}
	var segment InventorySegmentV2
	if decodeCanonical(raw, &segment) != nil || segment.Ordinal != ordinal || segment.Count != count ||
		segment.Repository != root.Repository || segment.GenerationDigest != root.GenerationDigest ||
		segment.SourcePartitionRootDigest != root.SourcePartitionRootDigest || len(segment.Members) != len(group) {
		return InventorySegmentV2{}, invalid("inventory v2 restart segment identity")
	}
	for index, source := range group {
		if segment.Members[index].SourceMemberDigest != source.member.Digest ||
			segment.Members[index].SourceSegment != source.segment || segment.Members[index].SourceOrdinal != source.ordinal {
			return InventorySegmentV2{}, invalid("inventory v2 restart source member")
		}
	}
	entry := inventorySegmentEntryV2(segment)
	provisional := root
	provisional.Segments = make([]InventorySegmentEntryV2, count)
	provisional.Segments[ordinal] = entry
	if validateInventorySegmentControlV2(provisional, entry, segment) != nil ||
		validateInventorySegmentFilesV2(ctx, directory, segment) != nil {
		return InventorySegmentV2{}, invalid("inventory v2 restart segment validation")
	}
	return segment, nil
}

func inventorySegmentEntryV2(segment InventorySegmentV2) InventorySegmentEntryV2 {
	entry := InventorySegmentEntryV2{
		Ordinal: segment.Ordinal, Count: segment.Count, Directory: inventorySegmentDirectoryV2(segment.Ordinal),
		SegmentDigest: segment.Digest, MemberCount: len(segment.Members), RecordCount: segment.RecordCount,
		ObservedCount: segment.ObservedCount, UnsupportedCount: segment.UnsupportedCount,
		EncodedMemberBytes: segment.EncodedMemberBytes, ObservationCount: segment.ObservationCount,
		ObservationBytes: segment.ObservationBytes,
	}
	if len(segment.Members) > 0 {
		entry.FirstPrefix = segment.Members[0].SourcePrefix
		entry.LastPrefix = segment.Members[len(segment.Members)-1].SourcePrefix
	}
	return entry
}

func validateInventoryBuildNamespaceV2(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == InventoryRootNameV2 || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!strings.HasPrefix(entry.Name(), "segment-") {
			return invalid("inventory v2 build namespace")
		}
	}
	return nil
}

func emptyOperationReceipt() OperationReceipt {
	return OperationReceipt{Schema: ReceiptSchema, UnsupportedReasons: []ReasonCount{}}
}

func mergeOperationReceipt(target *OperationReceipt, source OperationReceipt) {
	target.InputBlobs += source.InputBlobs
	target.SourceBlobReads += source.SourceBlobReads
	target.ParsedObservations += source.ParsedObservations
	target.ReusedObservations += source.ReusedObservations
	target.ObservedBlobs += source.ObservedBlobs
	target.UnsupportedBlobs += source.UnsupportedBlobs
	counts := make(map[string]int, len(target.UnsupportedReasons)+len(source.UnsupportedReasons))
	for _, value := range target.UnsupportedReasons {
		counts[value.Reason] += value.Count
	}
	for _, value := range source.UnsupportedReasons {
		counts[value.Reason] += value.Count
	}
	target.UnsupportedReasons = target.UnsupportedReasons[:0]
	for reason, count := range counts {
		target.UnsupportedReasons = append(target.UnsupportedReasons, ReasonCount{Reason: reason, Count: count})
	}
	sort.Slice(target.UnsupportedReasons, func(left, right int) bool {
		return target.UnsupportedReasons[left].Reason < target.UnsupportedReasons[right].Reason
	})
}
