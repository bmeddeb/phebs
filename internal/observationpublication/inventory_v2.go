package observationpublication

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

const (
	InventoryRootSchemaV2    = "phebs-observation-inventory-root-v2"
	InventorySegmentSchemaV2 = "phebs-observation-inventory-segment-v2"
	InventoryRootNameV2      = "inventory-root-v2.json"

	// Sixteen retained v1 record envelopes admit 4,000,000 records, more than
	// 15x the frozen T40.1 semantic profile's 262,144 distinct Go blobs. The
	// aggregate byte ceilings remain the shipped v1 generation ceilings.
	MaxInventorySegmentsV2     = 16
	MaxInventorySegmentRecords = MaxGenerationRecords
	MaxInventoryRecordsV2      = MaxInventorySegmentsV2 * MaxInventorySegmentRecords
	MaxInventoryRootBytesV2    = 1 << 20
	MaxInventorySegmentBytesV2 = MaxManifestBytes
)

type InventoryMemberV2 struct {
	Ordinal            int    `json:"ordinal"`
	Count              int    `json:"count"`
	Name               string `json:"name"`
	SourceSegment      int    `json:"source_segment"`
	SourceOrdinal      int    `json:"source_ordinal"`
	SourceMemberDigest string `json:"source_member_digest"`
	SourcePrefix       string `json:"source_prefix"`
	SourcePrefixBits   int    `json:"source_prefix_bits"`
	RecordCount        int    `json:"record_count"`
	ObservedCount      int    `json:"observed_count"`
	UnsupportedCount   int    `json:"unsupported_count"`
	ContentBytes       int64  `json:"content_bytes"`
	Digest             string `json:"digest"`
}

type InventorySegmentV2 struct {
	Schema                    string              `json:"schema"`
	Repository                string              `json:"repository"`
	GenerationDigest          string              `json:"generation_digest"`
	SourcePartitionRootDigest string              `json:"source_partition_root_digest"`
	ObservationPolicyDigest   string              `json:"observation_policy_digest"`
	Ordinal                   int                 `json:"ordinal"`
	Count                     int                 `json:"count"`
	Members                   []InventoryMemberV2 `json:"members"`
	RecordCount               int                 `json:"record_count"`
	ObservedCount             int                 `json:"observed_count"`
	UnsupportedCount          int                 `json:"unsupported_count"`
	EncodedMemberBytes        int64               `json:"encoded_member_bytes"`
	ObservationCount          int                 `json:"observation_count"`
	ObservationBytes          int64               `json:"observation_bytes"`
	OperationReceipt          OperationReceipt    `json:"operation_receipt"`
	Digest                    string              `json:"digest"`
}

type InventorySegmentEntryV2 struct {
	Ordinal            int    `json:"ordinal"`
	Count              int    `json:"count"`
	Directory          string `json:"directory"`
	SegmentDigest      string `json:"segment_digest"`
	MemberCount        int    `json:"member_count"`
	FirstPrefix        string `json:"first_prefix"`
	LastPrefix         string `json:"last_prefix"`
	RecordCount        int    `json:"record_count"`
	ObservedCount      int    `json:"observed_count"`
	UnsupportedCount   int    `json:"unsupported_count"`
	EncodedMemberBytes int64  `json:"encoded_member_bytes"`
	ObservationCount   int    `json:"observation_count"`
	ObservationBytes   int64  `json:"observation_bytes"`
}

type InventoryPolicyV2 struct {
	Name                     string `json:"name"`
	MaxSegments              int    `json:"max_segments"`
	MaxSegmentRecords        int    `json:"max_segment_records"`
	MaxAggregateRecords      int    `json:"max_aggregate_records"`
	MaxMemberBytes           int64  `json:"max_member_bytes"`
	MaxAggregateEncodedBytes int64  `json:"max_aggregate_encoded_bytes"`
	MaxAggregateObjectBytes  int64  `json:"max_aggregate_object_bytes"`
	ObjectByteKind           string `json:"object_byte_kind"`
}

func frozenInventoryPolicyV2() InventoryPolicyV2 {
	return InventoryPolicyV2{
		Name: "segmented-observation-inventory-v2", MaxSegments: MaxInventorySegmentsV2,
		MaxSegmentRecords: MaxInventorySegmentRecords, MaxAggregateRecords: MaxInventoryRecordsV2,
		MaxMemberBytes: MaxMemberBytes, MaxAggregateEncodedBytes: MaxGenerationBytes,
		MaxAggregateObjectBytes: MaxGenerationBytes, ObjectByteKind: "segment_charged_logical",
	}
}

type InventoryRootV2 struct {
	Schema                    string                    `json:"schema"`
	Repository                string                    `json:"repository"`
	Language                  string                    `json:"language"`
	SourceGenerationDigest    string                    `json:"source_generation_digest"`
	SourcePartitionGeneration string                    `json:"source_partition_generation"`
	SourcePartitionRootDigest string                    `json:"source_partition_root_digest"`
	PartitionPolicyDigest     string                    `json:"partition_policy_digest"`
	ObservationPolicyDigest   string                    `json:"observation_policy_digest"`
	InventoryPolicy           InventoryPolicyV2         `json:"inventory_policy"`
	InventoryPolicyDigest     string                    `json:"inventory_policy_digest"`
	GenerationDigest          string                    `json:"generation_digest"`
	Segments                  []InventorySegmentEntryV2 `json:"segments"`
	MemberCount               int                       `json:"member_count"`
	RecordCount               int                       `json:"record_count"`
	ObservedCount             int                       `json:"observed_count"`
	UnsupportedCount          int                       `json:"unsupported_count"`
	EncodedMemberBytes        int64                     `json:"encoded_member_bytes"`
	ObservationCount          int                       `json:"observation_count"`
	ObservationBytes          int64                     `json:"observation_bytes"`
	OperationReceipt          OperationReceipt          `json:"operation_receipt"`
	Digest                    string                    `json:"digest"`
}

type InventoryPublicationV2 struct {
	directory string
	root      InventoryRootV2
}

func InventoryGenerationDigestV2(source sourcepartition.SuperRoot) (string, error) {
	if err := sourcepartition.ValidateSuperRoot(source); err != nil {
		return "", err
	}
	return inventoryGenerationDigestFieldsV2(
		source.Repository, source.SourceGenerationDigest, source.GenerationDigest,
		source.Digest, source.PolicyDigest,
	), nil
}

func inventoryGenerationDigestFieldsV2(
	repository, sourceGeneration, partitionGeneration, rootDigest, partitionPolicy string,
) string {
	return digest("phebs-observation-generation-v2", strings.Join([]string{
		repository, sourceGeneration, partitionGeneration,
		rootDigest, partitionPolicy, sourceobservation.PolicyDigest(),
		inventoryPolicyDigestV2(), LanguagePack,
	}, "\x00"))
}

func inventoryPolicyDigestV2() string {
	raw, _ := json.Marshal(frozenInventoryPolicyV2())
	return digest("phebs-observation-inventory-policy-v2", string(raw))
}

func inventorySegmentDigestV2(segment InventorySegmentV2) (string, error) {
	segment.Digest = ""
	raw, err := json.Marshal(segment)
	if err != nil {
		return "", err
	}
	return digest("phebs-observation-inventory-segment-v2", string(raw)), nil
}

func InventoryRootDigestV2(root InventoryRootV2) (string, error) {
	root.Digest = ""
	raw, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return digest("phebs-observation-inventory-root-v2", string(raw)), nil
}

func inventorySegmentDirectoryV2(ordinal int) string {
	return "segment-" + formatOrdinal(ordinal)
}

func formatOrdinal(ordinal int) string {
	if ordinal < 0 || ordinal > 99999 {
		return "invalid"
	}
	return fmt.Sprintf("%05d", ordinal)
}

func inventoryMemberNameV2(ordinal int) string { return "member-" + formatOrdinal(ordinal) + ".jsonl" }

func ValidateInventoryRootV2(root InventoryRootV2) error {
	if root.Schema != InventoryRootSchemaV2 || validateRepository(root.Repository) != nil ||
		root.Language != LanguagePack || !validDigest(root.SourceGenerationDigest) ||
		!validDigest(root.SourcePartitionGeneration) || !validDigest(root.SourcePartitionRootDigest) ||
		!validDigest(root.PartitionPolicyDigest) || root.ObservationPolicyDigest != sourceobservation.PolicyDigest() ||
		root.InventoryPolicyDigest != inventoryPolicyDigestV2() ||
		!validDigest(root.GenerationDigest) || !validDigest(root.Digest) || root.Segments == nil ||
		len(root.Segments) > MaxInventorySegmentsV2 || root.MemberCount < 0 ||
		root.MemberCount > sourcepartition.MaxAggregateMembers || root.RecordCount < 0 ||
		root.RecordCount > MaxInventoryRecordsV2 || root.RecordCount != root.ObservedCount+root.UnsupportedCount ||
		root.EncodedMemberBytes < 0 || root.EncodedMemberBytes > MaxGenerationBytes ||
		root.ObservationCount < 0 || root.ObservationCount > root.ObservedCount ||
		root.ObservationBytes < 0 || root.ObservationBytes > MaxGenerationBytes {
		return invalid("inventory v2 root identity")
	}
	if !reflect.DeepEqual(root.InventoryPolicy, frozenInventoryPolicyV2()) {
		return invalid("inventory v2 policy")
	}
	if root.GenerationDigest != inventoryGenerationDigestFieldsV2(
		root.Repository, root.SourceGenerationDigest, root.SourcePartitionGeneration,
		root.SourcePartitionRootDigest, root.PartitionPolicyDigest,
	) {
		return invalid("inventory v2 generation")
	}
	if (root.RecordCount == 0) != (len(root.Segments) == 0) ||
		(root.ObservedCount == 0) != (root.ObservationCount == 0) ||
		(root.ObservationCount == 0) != (root.ObservationBytes == 0) ||
		validateOperationReceipt(root.OperationReceipt, Manifest{
			RecordCount: root.RecordCount, ObservedCount: root.ObservedCount,
			UnsupportedCount: root.UnsupportedCount,
		}) != nil {
		return invalid("inventory v2 root receipt")
	}
	var members, records, observed, unsupported, observations int
	var encoded, observationBytes int64
	previous := ""
	for ordinal, entry := range root.Segments {
		if entry.Ordinal != ordinal || entry.Count != len(root.Segments) ||
			entry.Directory != inventorySegmentDirectoryV2(ordinal) || !validDigest(entry.SegmentDigest) ||
			entry.MemberCount < 1 || entry.RecordCount < 1 || entry.RecordCount > MaxInventorySegmentRecords ||
			entry.RecordCount != entry.ObservedCount+entry.UnsupportedCount ||
			entry.EncodedMemberBytes < 1 || entry.EncodedMemberBytes > MaxGenerationBytes ||
			entry.ObservationCount < 0 || entry.ObservationCount > entry.ObservedCount ||
			entry.ObservationBytes < 0 || entry.ObservationBytes > MaxGenerationBytes ||
			(entry.ObservedCount == 0) != (entry.ObservationCount == 0) ||
			(entry.ObservationCount == 0) != (entry.ObservationBytes == 0) ||
			!validInventoryPrefixV2(entry.FirstPrefix) || !validInventoryPrefixV2(entry.LastPrefix) ||
			entry.LastPrefix < entry.FirstPrefix ||
			(ordinal > 0 && (entry.FirstPrefix <= previous || strings.HasPrefix(entry.FirstPrefix, previous))) {
			return invalid("inventory v2 root segment")
		}
		members += entry.MemberCount
		records += entry.RecordCount
		observed += entry.ObservedCount
		unsupported += entry.UnsupportedCount
		encoded += entry.EncodedMemberBytes
		observations += entry.ObservationCount
		observationBytes += entry.ObservationBytes
		previous = entry.LastPrefix
	}
	if members != root.MemberCount || records != root.RecordCount || observed != root.ObservedCount ||
		unsupported != root.UnsupportedCount || encoded != root.EncodedMemberBytes ||
		observations != root.ObservationCount || observationBytes != root.ObservationBytes {
		return invalid("inventory v2 root totals")
	}
	want, err := InventoryRootDigestV2(root)
	if err != nil || want != root.Digest {
		return invalid("inventory v2 root digest")
	}
	return nil
}

func ReadInventoryRootV2(directory, repository string) (InventoryRootV2, error) {
	return ReadInventoryRootV2Context(context.Background(), directory, repository)
}

// ReadInventoryRootV2Context reads one compact inventory-root control,
// including its failed preflight/open attempt. It does not open members.
func ReadInventoryRootV2Context(ctx context.Context, directory, repository string) (InventoryRootV2, error) {
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return InventoryRootV2{}, err
	}
	raw, err := readBoundedRegular(filepath.Join(directory, InventoryRootNameV2), MaxInventoryRootBytesV2)
	if err != nil {
		return InventoryRootV2{}, err
	}
	var root InventoryRootV2
	if decodeCanonical(raw, &root) != nil || ValidateInventoryRootV2(root) != nil || root.Repository != repository {
		return InventoryRootV2{}, invalid("inventory v2 root")
	}
	return root, nil
}

func readInventorySegmentV2(directory string, root InventoryRootV2, entry InventorySegmentEntryV2) (InventorySegmentV2, error) {
	raw, err := readBoundedRegular(filepath.Join(directory, entry.Directory, "segment.json"), MaxInventorySegmentBytesV2)
	if err != nil {
		return InventorySegmentV2{}, err
	}
	var segment InventorySegmentV2
	if decodeCanonical(raw, &segment) != nil || validateInventorySegmentControlV2(root, entry, segment) != nil {
		return InventorySegmentV2{}, invalid("inventory v2 segment")
	}
	return segment, nil
}

func validateInventorySegmentControlV2(root InventoryRootV2, entry InventorySegmentEntryV2, segment InventorySegmentV2) error {
	if segment.Schema != InventorySegmentSchemaV2 || segment.Repository != root.Repository ||
		segment.GenerationDigest != root.GenerationDigest || segment.SourcePartitionRootDigest != root.SourcePartitionRootDigest ||
		segment.ObservationPolicyDigest != root.ObservationPolicyDigest || segment.Ordinal != entry.Ordinal ||
		segment.Count != entry.Count || segment.Members == nil || len(segment.Members) != entry.MemberCount ||
		segment.RecordCount != entry.RecordCount || segment.ObservedCount != entry.ObservedCount ||
		segment.UnsupportedCount != entry.UnsupportedCount || segment.EncodedMemberBytes != entry.EncodedMemberBytes ||
		segment.ObservationCount != entry.ObservationCount || segment.ObservationBytes != entry.ObservationBytes ||
		segment.Digest != entry.SegmentDigest || validateOperationReceipt(segment.OperationReceipt, Manifest{
		RecordCount: segment.RecordCount, ObservedCount: segment.ObservedCount,
		UnsupportedCount: segment.UnsupportedCount,
	}) != nil {
		return invalid("inventory v2 segment binding")
	}
	var records, observed, unsupported int
	var encoded int64
	previous := ""
	for ordinal, member := range segment.Members {
		if member.Ordinal != ordinal || member.Count != len(segment.Members) || member.Name != inventoryMemberNameV2(ordinal) ||
			member.SourceSegment < 0 || member.SourceOrdinal < 0 || !validDigest(member.SourceMemberDigest) ||
			!validInventoryPrefixV2(member.SourcePrefix) || member.SourcePrefixBits != len(member.SourcePrefix) ||
			member.RecordCount < 1 || member.RecordCount > sourcepartition.MaxBlobsPerPartition ||
			member.RecordCount != member.ObservedCount+member.UnsupportedCount || member.ContentBytes < 1 ||
			member.ContentBytes > MaxMemberBytes || !validDigest(member.Digest) ||
			(ordinal > 0 && (member.SourcePrefix <= previous || strings.HasPrefix(member.SourcePrefix, previous))) {
			return invalid("inventory v2 member control")
		}
		records += member.RecordCount
		observed += member.ObservedCount
		unsupported += member.UnsupportedCount
		encoded += member.ContentBytes
		previous = member.SourcePrefix
	}
	if records != segment.RecordCount || observed != segment.ObservedCount ||
		unsupported != segment.UnsupportedCount || encoded != segment.EncodedMemberBytes {
		return invalid("inventory v2 segment totals")
	}
	want, err := inventorySegmentDigestV2(segment)
	if err != nil || want != segment.Digest {
		return invalid("inventory v2 segment digest")
	}
	return nil
}

func validInventoryPrefixV2(value string) bool {
	if len(value) < sourcepartition.InitialPrefixBits || len(value) > sha256.Size*8 {
		return false
	}
	for _, digit := range value {
		if digit != '0' && digit != '1' {
			return false
		}
	}
	return true
}

func OpenInventoryV2Keyed(directory, repository string) (*InventoryPublicationV2, error) {
	if !filepath.IsAbs(directory) || validateRepository(repository) != nil {
		return nil, invalid("inventory v2 keyed open")
	}
	root, err := ReadInventoryRootV2(directory, repository)
	if err != nil {
		return nil, err
	}
	return &InventoryPublicationV2{directory: directory, root: root}, nil
}

func OpenInventoryV2(ctx context.Context, directory string, expected InventoryRootV2) (*InventoryPublicationV2, error) {
	if err := ValidateInventoryStageV2(ctx, directory, expected); err != nil {
		return nil, err
	}
	return &InventoryPublicationV2{directory: directory, root: cloneInventoryRootV2(expected)}, nil
}

// ValidateInventorySourceV2 proves that every observation member binds the
// exact ordered member of the complete source super-root named by the v2 root.
// It reads only root/segment controls; source and observation member bytes are
// already transitively bound by their digests.
func ValidateInventorySourceV2(
	directory string, root InventoryRootV2, plan *sourcepartition.SuperPlan,
) error {
	if plan == nil || !plan.Complete() {
		return invalid("inventory v2 source plan is not complete")
	}
	source := plan.Root()
	if root.Repository != source.Repository || root.SourceGenerationDigest != source.SourceGenerationDigest ||
		root.SourcePartitionGeneration != source.GenerationDigest || root.SourcePartitionRootDigest != source.Digest ||
		root.PartitionPolicyDigest != source.PolicyDigest || root.MemberCount != source.MemberCount ||
		root.RecordCount != source.BlobCount {
		return invalid("inventory v2 source root binding")
	}
	type sourceControl struct {
		segment int
		ordinal int
		member  sourcepartition.Member
	}
	expected := make([]sourceControl, 0, source.MemberCount)
	for segment := range source.Segments {
		manifest, err := plan.SegmentManifest(segment)
		if err != nil {
			return err
		}
		for ordinal, member := range manifest.Members {
			expected = append(expected, sourceControl{segment: segment, ordinal: ordinal, member: member})
		}
	}
	index := 0
	for _, entry := range root.Segments {
		segment, err := readInventorySegmentV2(directory, root, entry)
		if err != nil {
			return err
		}
		for _, member := range segment.Members {
			if index >= len(expected) {
				return invalid("inventory v2 has extra source members")
			}
			want := expected[index]
			if member.SourceSegment != want.segment || member.SourceOrdinal != want.ordinal ||
				member.SourceMemberDigest != want.member.Digest || member.SourcePrefix != want.member.Prefix ||
				member.SourcePrefixBits != want.member.PrefixBits || member.RecordCount != want.member.BlobCount {
				return invalid("inventory v2 source member binding")
			}
			index++
		}
	}
	if index != len(expected) {
		return invalid("inventory v2 source members are incomplete")
	}
	return nil
}

func (publication *InventoryPublicationV2) Root() InventoryRootV2 {
	if publication == nil {
		return InventoryRootV2{}
	}
	return cloneInventoryRootV2(publication.root)
}

func (publication *InventoryPublicationV2) Lookup(ctx context.Context, objectID string) (Record, *sourceobservation.Observation, error) {
	if publication == nil || !gitobj.IsObjectID(objectID) {
		return Record{}, nil, invalid("inventory v2 lookup")
	}
	fullPrefix, err := sourcepartition.ObjectPrefix(objectID, sha256.Size*8)
	if err != nil {
		return Record{}, nil, err
	}
	entryIndex := sort.Search(len(publication.root.Segments), func(index int) bool {
		last := publication.root.Segments[index].LastPrefix
		return fullPrefix <= last || strings.HasPrefix(fullPrefix, last)
	})
	if entryIndex >= len(publication.root.Segments) || fullPrefix < publication.root.Segments[entryIndex].FirstPrefix {
		return Record{}, nil, os.ErrNotExist
	}
	entry := publication.root.Segments[entryIndex]
	segment, err := readInventorySegmentV2(publication.directory, publication.root, entry)
	if err != nil {
		return Record{}, nil, err
	}
	for ordinal, control := range segment.Members {
		prefix, err := sourcepartition.ObjectPrefix(objectID, control.SourcePrefixBits)
		if err != nil {
			return Record{}, nil, err
		}
		if prefix != control.SourcePrefix {
			continue
		}
		member, records, err := readMember(ctx, filepath.Join(publication.directory, entry.Directory), ordinal, len(segment.Members))
		member.SourceMemberDigest = control.SourceMemberDigest
		if err != nil || inventoryMemberFromV1(member, control) != control {
			return Record{}, nil, invalid("inventory v2 keyed member")
		}
		index, found := slices.BinarySearchFunc(records, objectID, func(record Record, target string) int {
			return strings.Compare(record.ObjectID, target)
		})
		if !found {
			return Record{}, nil, os.ErrNotExist
		}
		record := cloneRecord(records[index])
		if record.State != "observed" {
			return record, nil, nil
		}
		observation, err := readObservation(filepath.Join(publication.directory, entry.Directory), record)
		return record, &observation, err
	}
	return Record{}, nil, os.ErrNotExist
}

func ValidateInventoryStageV2(ctx context.Context, directory string, expected InventoryRootV2) error {
	if !filepath.IsAbs(directory) || ValidateInventoryRootV2(expected) != nil {
		return invalid("inventory v2 validation input")
	}
	root, err := ReadInventoryRootV2(directory, expected.Repository)
	if err != nil || root.Digest != expected.Digest {
		return errors.Join(err, invalid("inventory v2 root changed"))
	}
	want := map[string]bool{InventoryRootNameV2: true}
	for _, entry := range root.Segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		segment, err := readInventorySegmentV2(directory, root, entry)
		if err != nil {
			return err
		}
		if err := validateInventorySegmentFilesV2(ctx, filepath.Join(directory, entry.Directory), segment); err != nil {
			return err
		}
		want[entry.Directory] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(want) {
		return errors.Join(err, invalid("inventory v2 stage entries"))
	}
	for _, entry := range entries {
		if !want[entry.Name()] || entry.Type()&os.ModeSymlink != 0 || (entry.Name() != InventoryRootNameV2 && !entry.IsDir()) {
			return invalid("inventory v2 stage contains an unknown or special artifact")
		}
	}
	return nil
}

func validateInventorySegmentFilesV2(ctx context.Context, directory string, segment InventorySegmentV2) error {
	objects := make(map[string]struct{}, segment.ObservationCount)
	receipt := newReceiptAccumulator()
	for ordinal, expected := range segment.Members {
		member, records, err := readMember(ctx, directory, ordinal, len(segment.Members))
		member.SourceMemberDigest = expected.SourceMemberDigest
		if err != nil || inventoryMemberFromV1(member, expected) != expected {
			return invalid("inventory v2 member mismatch")
		}
		for _, record := range records {
			if err := receipt.add(record); err != nil {
				return err
			}
			if record.State == "observed" {
				if _, seen := objects[record.ObservationName]; !seen {
					if err := validateObservation(directory, record); err != nil {
						return err
					}
					objects[record.ObservationName] = struct{}{}
				}
			}
		}
	}
	if len(objects) != segment.ObservationCount || receipt.value() == nil ||
		!equalOperationReceipt(*receipt.value(), segment.OperationReceipt) {
		return invalid("inventory v2 segment inventory")
	}
	return validateSegmentInventoryV2(directory, segment, objects)
}

func validateSegmentInventoryV2(directory string, segment InventorySegmentV2, objects map[string]struct{}) error {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(segment.Members)+2 {
		return errors.Join(err, invalid("inventory v2 segment entries"))
	}
	want := map[string]bool{"segment.json": true, "objects": true}
	for _, member := range segment.Members {
		want[member.Name] = true
	}
	for _, entry := range entries {
		if !want[entry.Name()] || entry.Type()&os.ModeSymlink != 0 || (entry.Name() == "objects") != entry.IsDir() {
			return invalid("inventory v2 segment artifact")
		}
	}
	objectDirectory, err := os.Open(filepath.Join(directory, "objects"))
	if err != nil {
		return err
	}
	defer func() { _ = objectDirectory.Close() }()
	seen := 0
	var bytesTotal int64
	for {
		objectEntries, readErr := objectDirectory.Readdir(4_096)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, entry := range objectEntries {
			name := filepath.Join("objects", entry.Name())
			_, expected := objects[name]
			if !entry.Mode().IsRegular() || !validObservationBasename(entry.Name()) || !expected {
				return invalid("inventory v2 object artifact")
			}
			seen++
			bytesTotal += entry.Size()
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if seen != len(objects) || bytesTotal != segment.ObservationBytes {
		return invalid("inventory v2 object byte total")
	}
	return nil
}

func inventoryMemberFromV1(member Member, control InventoryMemberV2) InventoryMemberV2 {
	return InventoryMemberV2{
		Ordinal: control.Ordinal, Count: control.Count, Name: control.Name,
		SourceSegment: control.SourceSegment, SourceOrdinal: control.SourceOrdinal,
		SourceMemberDigest: member.SourceMemberDigest, SourcePrefix: control.SourcePrefix,
		SourcePrefixBits: control.SourcePrefixBits, RecordCount: member.RecordCount,
		ObservedCount: member.ObservedCount, UnsupportedCount: member.UnsupportedCount,
		ContentBytes: member.ContentBytes, Digest: member.Digest,
	}
}

func cloneInventoryRootV2(root InventoryRootV2) InventoryRootV2 {
	result := root
	result.Segments = slices.Clone(root.Segments)
	result.OperationReceipt.UnsupportedReasons = slices.Clone(root.OperationReceipt.UnsupportedReasons)
	return result
}

func cloneRecord(record Record) Record {
	record.Placements = slices.Clone(record.Placements)
	for index := range record.Placements {
		record.Placements[index].Revisions = slices.Clone(record.Placements[index].Revisions)
	}
	return record
}
