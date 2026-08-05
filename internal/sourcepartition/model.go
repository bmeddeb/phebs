// Package sourcepartition defines the bounded immutable-Git input contract for
// repository-wide source observations. It is deliberately independent of any
// parser, scheduler, store row, or product-visible publication.
package sourcepartition

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	ManifestSchema  = "phebs-source-partition-manifest-v1"
	MemberSchema    = "phebs-source-partition-member-v1"
	PolicySchema    = "phebs-source-partition-policy-v1"
	PartitionPolicy = "immutable-blob-hash-prefix-v1"
	BlobHashPolicy  = "phebs-source-partition-blob-v1"

	InitialPrefixBits               = 4
	MaxSuffixes                     = 16
	MaxSuffixBytes                  = 32
	MaxBlobsPerPartition            = 4_096
	MaxPlacementsPerPartition       = 4_096
	MaxPartitions                   = 16_384
	MaxBlobBytes              int64 = 4 << 20
	MaxPartitionBlobBytes     int64 = 64 << 20
	MaxGenerationBlobBytes    int64 = 4 << 30
	MaxGenerationContentBytes int64 = 4 << 30
	MaxMemberContentBytes     int64 = 64 << 20
	MaxManifestBytes                = 8 << 20
	MaxRecordBytes                  = 4 << 20
	MaxSpoolBytes             int64 = 8 << 30
	MaxBatchOutputBytes       int64 = 65 << 20
	MaxBatchDescriptors             = 3
	MaxBatchDuration                = 5 * time.Minute
)

var (
	ErrInvalid       = errors.New("invalid source partition")
	ErrTooLarge      = errors.New("source partition limit exceeded")
	ErrHashCollision = errors.New("source partition hash cannot split")
	ErrMissingObject = errors.New("immutable git blob is missing")
	ErrNotBlob       = errors.New("immutable git object is not a blob")
	ErrSizeMismatch  = errors.New("immutable git blob size changed")
)

// Policy is the closed, serializable selector for one language-pack input.
// Suffix matching is byte-exact and contains no callback whose behavior could
// drift without changing policy identity.
type Policy struct {
	Schema          string   `json:"schema"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	IncludeSuffixes []string `json:"include_suffixes"`
}

// FrozenPolicy records every bound that shapes partition identity.
type FrozenPolicy struct {
	Name                      string        `json:"name"`
	BlobHashPolicy            string        `json:"blob_hash_policy"`
	InitialPrefixBits         int           `json:"initial_prefix_bits"`
	MaxSuffixes               int           `json:"max_suffixes"`
	MaxSuffixBytes            int           `json:"max_suffix_bytes"`
	MaxPartitions             int           `json:"max_partitions"`
	MaxBlobsPerPartition      int           `json:"max_blobs_per_partition"`
	MaxPlacementsPerPartition int           `json:"max_placements_per_partition"`
	MaxBlobBytes              int64         `json:"max_blob_bytes"`
	MaxPartitionBlobBytes     int64         `json:"max_partition_blob_bytes"`
	MaxGenerationBlobBytes    int64         `json:"max_generation_blob_bytes"`
	MaxGenerationContentBytes int64         `json:"max_generation_content_bytes"`
	MaxMemberContentBytes     int64         `json:"max_member_content_bytes"`
	MaxManifestBytes          int           `json:"max_manifest_bytes"`
	MaxRecordBytes            int           `json:"max_record_bytes"`
	MaxSpoolBytes             int64         `json:"max_spool_bytes"`
	MaxBatchOutputBytes       int64         `json:"max_batch_output_bytes"`
	MaxBatchDescriptors       int           `json:"max_batch_descriptors"`
	MaxBatchDuration          time.Duration `json:"max_batch_duration_ns"`
	Ordering                  string        `json:"ordering"`
	SplitRule                 string        `json:"split_rule"`
}

func frozenPolicy() FrozenPolicy {
	return FrozenPolicy{
		Name: PartitionPolicy, BlobHashPolicy: BlobHashPolicy,
		InitialPrefixBits:         InitialPrefixBits,
		MaxSuffixes:               MaxSuffixes,
		MaxSuffixBytes:            MaxSuffixBytes,
		MaxPartitions:             MaxPartitions,
		MaxBlobsPerPartition:      MaxBlobsPerPartition,
		MaxPlacementsPerPartition: MaxPlacementsPerPartition,
		MaxBlobBytes:              MaxBlobBytes,
		MaxPartitionBlobBytes:     MaxPartitionBlobBytes,
		MaxGenerationBlobBytes:    MaxGenerationBlobBytes,
		MaxGenerationContentBytes: MaxGenerationContentBytes,
		MaxMemberContentBytes:     MaxMemberContentBytes,
		MaxManifestBytes:          MaxManifestBytes,
		MaxRecordBytes:            MaxRecordBytes,
		MaxSpoolBytes:             MaxSpoolBytes,
		MaxBatchOutputBytes:       MaxBatchOutputBytes,
		MaxBatchDescriptors:       MaxBatchDescriptors,
		MaxBatchDuration:          MaxBatchDuration,
		Ordering:                  "object-id-then-placement-v1",
		SplitRule:                 "next-domain-separated-object-id-hash-bit-v1",
	}
}

// Placement preserves every source-generation coordinate sharing one blob.
// The blob is read once even when it appears at multiple paths or revisions.
type Placement struct {
	Path      string `json:"path"`
	Mode      string `json:"mode"`
	Revisions []int  `json:"revisions"`
}

// BlobRecord is one immutable object plus all selected physical placements.
type BlobRecord struct {
	Schema        string      `json:"schema"`
	ObjectID      string      `json:"object_id"`
	DeclaredBytes int64       `json:"declared_bytes"`
	Placements    []Placement `json:"placements"`
}

type Member struct {
	Ordinal        int    `json:"ordinal"`
	Count          int    `json:"count"`
	Name           string `json:"name"`
	Prefix         string `json:"prefix"`
	PrefixBits     int    `json:"prefix_bits"`
	BlobCount      int    `json:"blob_count"`
	PlacementCount int    `json:"placement_count"`
	DeclaredBytes  int64  `json:"declared_bytes"`
	ContentBytes   int64  `json:"content_bytes"`
	Digest         string `json:"digest"`
	FirstObjectID  string `json:"first_object_id"`
	LastObjectID   string `json:"last_object_id"`
}

type Manifest struct {
	Schema                 string       `json:"schema"`
	Repository             string       `json:"repository"`
	SourceGenerationDigest string       `json:"source_generation_digest"`
	RevisionCount          int          `json:"revision_count"`
	Policy                 Policy       `json:"policy"`
	PolicyDigest           string       `json:"policy_digest"`
	PartitionPolicy        FrozenPolicy `json:"partition_policy"`
	GenerationDigest       string       `json:"generation_digest"`
	Members                []Member     `json:"members"`
	BlobCount              int          `json:"blob_count"`
	PlacementCount         int          `json:"placement_count"`
	DeclaredBytes          int64        `json:"declared_bytes"`
	EncodedMemberBytes     int64        `json:"encoded_member_bytes"`
	Digest                 string       `json:"digest"`
}

type ReadStats struct {
	Blobs       int   `json:"blobs"`
	Placements  int   `json:"placements"`
	BlobBytes   int64 `json:"blob_bytes"`
	OutputBytes int64 `json:"output_bytes"`
	Descriptors int   `json:"descriptors"`
}

func ValidatePolicy(policy Policy) error {
	if policy.Schema != PolicySchema || !validToken(policy.Name, 128) ||
		!validVersion(policy.Version) || len(policy.IncludeSuffixes) < 1 ||
		len(policy.IncludeSuffixes) > MaxSuffixes ||
		!slices.IsSorted(policy.IncludeSuffixes) {
		return invalidf("policy identity is invalid")
	}
	previous := ""
	for _, suffix := range policy.IncludeSuffixes {
		if suffix == previous || len(suffix) < 2 || len(suffix) > MaxSuffixBytes ||
			!strings.HasPrefix(suffix, ".") || !validSuffix(suffix) {
			return invalidf("policy suffix is invalid")
		}
		previous = suffix
	}
	return nil
}

func PolicyDigest(policy Policy) (string, error) {
	if err := ValidatePolicy(policy); err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Policy    Policy       `json:"policy"`
		Partition FrozenPolicy `json:"partition_policy"`
	}{policy, frozenPolicy()})
	if err != nil {
		return "", err
	}
	return digest("phebs-source-partition-policy-v1\x00", raw), nil
}

func GenerationDigest(repository, sourceDigest, policyDigest string) (string, error) {
	if err := reponame.Validate(repository); err != nil ||
		!validDigest(sourceDigest) || !validDigest(policyDigest) {
		return "", invalidf("generation identity is invalid")
	}
	return digest("phebs-source-partition-generation-v1\x00",
		[]byte(repository+"\x00"+sourceDigest+"\x00"+policyDigest)), nil
}

func ManifestDigest(manifest Manifest) (string, error) {
	manifest.Digest = ""
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digest("phebs-source-partition-manifest-v1\x00", raw), nil
}

func ManifestName(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return "phebs-source-partitions-" + hex.EncodeToString(sum[:]) + ".manifest.json"
}

func memberName(repository, generation string, ordinal int) string {
	repositorySum := sha256.Sum256([]byte(repository))
	generation = strings.TrimPrefix(generation, "sha256:")
	return fmt.Sprintf("phebs-source-partitions-%s-%s.%05d.jsonl",
		hex.EncodeToString(repositorySum[:])[:16], generation[:16], ordinal)
}

func matches(policy Policy, path string) bool {
	for _, suffix := range policy.IncludeSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func validateBlobRecord(record BlobRecord, revisionCount int) error {
	if record.Schema != MemberSchema || !gitobj.IsObjectID(record.ObjectID) ||
		record.DeclaredBytes < 0 || record.DeclaredBytes > MaxBlobBytes ||
		len(record.Placements) < 1 || len(record.Placements) > MaxPlacementsPerPartition {
		return invalidf("blob record identity or bounds are invalid")
	}
	previous := Placement{}
	for index, placement := range record.Placements {
		if repopath.Validate(placement.Path) != nil ||
			(placement.Mode != "100644" && placement.Mode != "100755") ||
			len(placement.Revisions) < 1 || len(placement.Revisions) > revisionCount ||
			!slices.IsSorted(placement.Revisions) ||
			(index > 0 && comparePlacement(previous, placement) >= 0) {
			return invalidf("blob placement is invalid or unordered")
		}
		for revisionIndex, revision := range placement.Revisions {
			if revision < 0 || revision >= revisionCount ||
				(revisionIndex > 0 && placement.Revisions[revisionIndex-1] == revision) {
				return invalidf("blob placement revision is invalid")
			}
		}
		previous = placement
	}
	return nil
}

func comparePlacement(left, right Placement) int {
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	if value := strings.Compare(left.Mode, right.Mode); value != 0 {
		return value
	}
	return slices.Compare(left.Revisions, right.Revisions)
}

func validToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' ||
			current == '-' {
			continue
		}
		return false
	}
	return true
}

func validVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 8 {
			return false
		}
		for _, current := range part {
			if current < '0' || current > '9' {
				return false
			}
		}
	}
	return true
}

func validSuffix(value string) bool {
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' || current == '.' || current == '_' ||
			current == '-' {
			continue
		}
		return false
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func digest(domain string, raw []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(raw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func invalidf(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, values...))
}

func safeBaseName(value string) bool {
	return filepath.Base(value) == value && value != "." && value != ""
}
