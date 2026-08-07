package sourcepartition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/reponame"
)

// The v2 super-root retains every v1 per-blob, per-member, and per-partition
// bound and evolves only the single independent 4-GiB generation declared and
// encoded bounds. The aggregate ceilings are selected from the frozen T40.1
// profiles: the structural profile declares 9,216,000,000 repository-lane Go
// bytes (which exceeds one v1 generation bound) and the semantic profile holds
// 262,144 distinct Go blobs. Sixteen v1-bounded segments give a 64-GiB
// aggregate ceiling (~7x the measured structural bytes) without raising any
// retained v1 partition, member, or blob limit.
const (
	SuperRootSchema     = "phebs-source-partition-superroot-v2"
	AggregatePolicyName = "segmented-v1-generation-bounds-v2"

	MaxSegments               = 16
	MaxAggregateMembers       = MaxPartitions
	MaxAggregateBlobs         = MaxPartitions * MaxBlobsPerPartition
	MaxAggregatePlacements    = MaxPartitions * MaxPlacementsPerPartition
	MaxAggregateDeclaredBytes = MaxSegments * MaxGenerationBlobBytes
	MaxAggregateEncodedBytes  = MaxSegments * MaxGenerationContentBytes
)

// FrozenAggregatePolicy records every aggregate bound that shapes super-root
// identity, mirroring FrozenPolicy for the retained v1 envelope.
type FrozenAggregatePolicy struct {
	Name                      string `json:"name"`
	MaxSegments               int    `json:"max_segments"`
	MaxAggregateMembers       int    `json:"max_aggregate_members"`
	MaxAggregateBlobs         int    `json:"max_aggregate_blobs"`
	MaxAggregatePlacements    int    `json:"max_aggregate_placements"`
	MaxAggregateDeclaredBytes int64  `json:"max_aggregate_declared_bytes"`
	MaxAggregateEncodedBytes  int64  `json:"max_aggregate_encoded_bytes"`
	SegmentRule               string `json:"segment_rule"`
}

func frozenAggregatePolicy() FrozenAggregatePolicy {
	return FrozenAggregatePolicy{
		Name:                      AggregatePolicyName,
		MaxSegments:               MaxSegments,
		MaxAggregateMembers:       MaxAggregateMembers,
		MaxAggregateBlobs:         MaxAggregateBlobs,
		MaxAggregatePlacements:    MaxAggregatePlacements,
		MaxAggregateDeclaredBytes: MaxAggregateDeclaredBytes,
		MaxAggregateEncodedBytes:  MaxAggregateEncodedBytes,
		SegmentRule:               "greedy-ordered-v1-generation-bounds-v2",
	}
}

// SegmentEntry binds one independently bounded v1-compatible segment. Member
// digests are bound transitively through ManifestDigest, so the root itself
// never carries a corpus-sized member or blob map.
type SegmentEntry struct {
	Ordinal            int    `json:"ordinal"`
	Count              int    `json:"count"`
	Directory          string `json:"directory"`
	ManifestDigest     string `json:"manifest_digest"`
	MemberCount        int    `json:"member_count"`
	FirstPrefix        string `json:"first_prefix"`
	LastPrefix         string `json:"last_prefix"`
	BlobCount          int    `json:"blob_count"`
	PlacementCount     int    `json:"placement_count"`
	DeclaredBytes      int64  `json:"declared_bytes"`
	EncodedMemberBytes int64  `json:"encoded_member_bytes"`
}

type SuperRoot struct {
	Schema                 string                `json:"schema"`
	Repository             string                `json:"repository"`
	SourceGenerationDigest string                `json:"source_generation_digest"`
	RevisionCount          int                   `json:"revision_count"`
	Policy                 Policy                `json:"policy"`
	PolicyDigest           string                `json:"policy_digest"`
	PartitionPolicy        FrozenPolicy          `json:"partition_policy"`
	AggregatePolicy        FrozenAggregatePolicy `json:"aggregate_policy"`
	GenerationDigest       string                `json:"generation_digest"`
	Segments               []SegmentEntry        `json:"segments"`
	MemberCount            int                   `json:"member_count"`
	BlobCount              int                   `json:"blob_count"`
	PlacementCount         int                   `json:"placement_count"`
	DeclaredBytes          int64                 `json:"declared_bytes"`
	EncodedMemberBytes     int64                 `json:"encoded_member_bytes"`
	Digest                 string                `json:"digest"`
}

func SuperRootDigest(root SuperRoot) (string, error) {
	root.Digest = ""
	raw, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return digest("phebs-source-partition-superroot-v2\x00", raw), nil
}

func SuperRootName(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return "phebs-source-partitions-" + hex.EncodeToString(sum[:]) + ".root.json"
}

func segmentDirectoryName(ordinal int) string {
	return fmt.Sprintf("segment-%05d", ordinal)
}

func ValidateSuperRoot(root SuperRoot) error {
	if root.Schema != SuperRootSchema || reponame.Validate(root.Repository) != nil ||
		!validDigest(root.SourceGenerationDigest) || root.RevisionCount < 1 ||
		root.RevisionCount > 8 || !validDigest(root.PolicyDigest) ||
		!validDigest(root.GenerationDigest) || !validDigest(root.Digest) ||
		root.Segments == nil || len(root.Segments) > MaxSegments ||
		root.MemberCount < 0 || root.MemberCount > MaxAggregateMembers ||
		root.BlobCount < 0 || root.BlobCount > MaxAggregateBlobs ||
		root.PlacementCount < root.BlobCount ||
		root.PlacementCount > MaxAggregatePlacements ||
		root.DeclaredBytes < 0 || root.DeclaredBytes > MaxAggregateDeclaredBytes ||
		root.EncodedMemberBytes < 0 ||
		root.EncodedMemberBytes > MaxAggregateEncodedBytes {
		return invalidf("super-root identity or totals are invalid")
	}
	if err := ValidatePolicy(root.Policy); err != nil {
		return err
	}
	policyDigest, err := PolicyDigest(root.Policy)
	if err != nil || policyDigest != root.PolicyDigest ||
		!reflect.DeepEqual(root.PartitionPolicy, frozenPolicy()) ||
		!reflect.DeepEqual(root.AggregatePolicy, frozenAggregatePolicy()) {
		return invalidf("super-root policy binding is invalid")
	}
	generation, err := GenerationDigest(
		root.Repository, root.SourceGenerationDigest, root.PolicyDigest,
	)
	if err != nil || generation != root.GenerationDigest {
		return invalidf("super-root generation binding is invalid")
	}
	if (root.BlobCount == 0) != (len(root.Segments) == 0) ||
		(root.BlobCount == 0) != (root.MemberCount == 0) ||
		(root.BlobCount == 0) != (root.PlacementCount == 0) ||
		(root.BlobCount == 0) != (root.DeclaredBytes == 0) ||
		(root.BlobCount == 0) != (root.EncodedMemberBytes == 0) {
		return invalidf("empty super-root shape is inconsistent")
	}
	members, blobs, placements := 0, 0, 0
	var declared, encoded int64
	previousLast := ""
	for ordinal, entry := range root.Segments {
		if entry.Ordinal != ordinal || entry.Count != len(root.Segments) ||
			entry.Directory != segmentDirectoryName(ordinal) ||
			!validDigest(entry.ManifestDigest) ||
			entry.MemberCount < 1 || entry.MemberCount > MaxPartitions ||
			entry.BlobCount < 1 || entry.BlobCount > entry.MemberCount*MaxBlobsPerPartition ||
			entry.PlacementCount < entry.BlobCount ||
			entry.DeclaredBytes < 0 || entry.DeclaredBytes > MaxGenerationBlobBytes ||
			entry.EncodedMemberBytes < 1 ||
			entry.EncodedMemberBytes > MaxGenerationContentBytes ||
			!validSegmentPrefix(entry.FirstPrefix) || !validSegmentPrefix(entry.LastPrefix) ||
			entry.LastPrefix < entry.FirstPrefix ||
			(entry.MemberCount == 1) != (entry.FirstPrefix == entry.LastPrefix) ||
			(entry.FirstPrefix != entry.LastPrefix &&
				strings.HasPrefix(entry.LastPrefix, entry.FirstPrefix)) ||
			(ordinal > 0 && (entry.FirstPrefix <= previousLast ||
				strings.HasPrefix(entry.FirstPrefix, previousLast))) {
			return invalidf("super-root segment is invalid or overlaps another range")
		}
		members += entry.MemberCount
		blobs += entry.BlobCount
		placements += entry.PlacementCount
		declared += entry.DeclaredBytes
		encoded += entry.EncodedMemberBytes
		previousLast = entry.LastPrefix
	}
	if members != root.MemberCount || blobs != root.BlobCount ||
		placements != root.PlacementCount || declared != root.DeclaredBytes ||
		encoded != root.EncodedMemberBytes {
		return invalidf("super-root segment totals are inconsistent")
	}
	want, err := SuperRootDigest(root)
	if err != nil || want != root.Digest {
		return invalidf("super-root digest mismatch")
	}
	return nil
}

func validSegmentPrefix(value string) bool {
	return len(value) >= InitialPrefixBits && len(value) <= sha256.Size*8 &&
		binaryPrefix(value)
}

func ReadSuperRoot(directory, repository string) (SuperRoot, error) {
	var root SuperRoot
	if err := readCanonicalJSON(
		filepath.Join(directory, SuperRootName(repository)), MaxManifestBytes, &root,
	); err != nil {
		return SuperRoot{}, err
	}
	if err := ValidateSuperRoot(root); err != nil {
		return SuperRoot{}, err
	}
	if root.Repository != repository {
		return SuperRoot{}, invalidf("super-root repository mismatch")
	}
	return root, nil
}

// checkSegmentBinding proves one opened segment manifest is exactly the
// segment the root committed: same identity chain, same totals, same range.
// A member staged for another generation carries a name derived from that
// generation and can therefore never satisfy the bound manifest.
func checkSegmentBinding(root SuperRoot, entry SegmentEntry, manifest Manifest) error {
	if manifest.Digest != entry.ManifestDigest ||
		manifest.Repository != root.Repository ||
		manifest.SourceGenerationDigest != root.SourceGenerationDigest ||
		manifest.RevisionCount != root.RevisionCount ||
		manifest.PolicyDigest != root.PolicyDigest ||
		manifest.GenerationDigest != root.GenerationDigest ||
		len(manifest.Members) != entry.MemberCount ||
		manifest.BlobCount != entry.BlobCount ||
		manifest.PlacementCount != entry.PlacementCount ||
		manifest.DeclaredBytes != entry.DeclaredBytes ||
		manifest.EncodedMemberBytes != entry.EncodedMemberBytes ||
		manifest.Members[0].Prefix != entry.FirstPrefix ||
		manifest.Members[len(manifest.Members)-1].Prefix != entry.LastPrefix {
		return invalidf("segment manifest disagrees with its super-root entry")
	}
	return nil
}

// SuperPlan is a validated v2 super-root stage. As with Plan, its fields stay
// private so consumers cannot stream from an unvalidated stage.
type SuperPlan struct {
	directory string
	root      SuperRoot
	complete  bool
}

func (plan *SuperPlan) Root() SuperRoot {
	if plan == nil {
		return SuperRoot{}
	}
	return cloneSuperRoot(plan.root)
}

// OpenSuperRootKeyed admits keyed point reads after validating only the small
// root control. Segment controls and members are validated exactly when one is
// read; a keyed open never reopens or hashes the complete corpus.
func OpenSuperRootKeyed(directory, repository string) (*SuperPlan, error) {
	if !filepath.IsAbs(directory) {
		return nil, invalidf("super-root directory must be absolute")
	}
	root, err := ReadSuperRoot(directory, repository)
	if err != nil {
		return nil, err
	}
	return &SuperPlan{directory: directory, root: root}, nil
}

// OpenSuperRoot proves the complete stage — every segment control and every
// member streamed exactly once — before any authority may bind it.
func OpenSuperRoot(ctx context.Context, directory string, expected SuperRoot) (*SuperPlan, error) {
	if !filepath.IsAbs(directory) {
		return nil, invalidf("super-root directory must be absolute")
	}
	if err := ValidateSuperRootStage(ctx, directory, expected); err != nil {
		return nil, err
	}
	return &SuperPlan{directory: directory, root: cloneSuperRoot(expected), complete: true}, nil
}

// Complete reports whether every segment and member was validated when this
// handle opened. Consumers that build new authority must reject keyed-only
// handles and may then read individual source members without revalidating the
// complete source generation on every partition.
func (plan *SuperPlan) Complete() bool { return plan != nil && plan.complete }

// SegmentManifest returns one root-bound v1 segment control. It reads no
// member bytes and preserves the keyed-open cost boundary.
func (plan *SuperPlan) SegmentManifest(segment int) (Manifest, error) {
	opened, err := plan.openSegment(segment)
	if err != nil {
		return Manifest{}, err
	}
	return opened.Manifest(), nil
}

// Segment opens one root-bound v1 segment control once so a sequential
// consumer can read several members without reopening that control per member.
// The returned Plan retains the ordinary one-member validation boundary.
func (plan *SuperPlan) Segment(segment int) (*Plan, error) {
	return plan.openSegment(segment)
}

// ObjectPrefix returns the frozen source-partition hash prefix used to locate
// one object in a hierarchical consumer without opening sibling segments.
func ObjectPrefix(objectID string, bits int) (string, error) {
	if !gitobj.IsObjectID(objectID) || bits < InitialPrefixBits || bits > sha256.Size*8 {
		return "", invalidf("object prefix input is invalid")
	}
	hash := sourceBlobHash(objectID)
	return bitPrefix(hash[:], bits), nil
}

// openSegment reads exactly one segment control and binds it to the root.
// The returned Plan streams no member here; ReadPartition validates the one
// requested member completely before delivering a byte.
func (plan *SuperPlan) openSegment(segment int) (*Plan, error) {
	if plan == nil {
		return nil, invalidf("super plan is nil")
	}
	if segment < 0 || segment >= len(plan.root.Segments) {
		return nil, invalidf("super plan segment is out of range")
	}
	entry := plan.root.Segments[segment]
	segmentDirectory := filepath.Join(plan.directory, entry.Directory)
	manifest, err := ReadManifest(segmentDirectory, plan.root.Repository)
	if err != nil {
		return nil, err
	}
	if err := checkSegmentBinding(plan.root, entry, manifest); err != nil {
		return nil, err
	}
	return &Plan{directory: segmentDirectory, manifest: cloneManifest(manifest)}, nil
}

// ReadSegmentPartition performs one keyed partition read: the already-read
// root, one segment control, and one member admitted through the retained v1
// immutable-Git batch reader with its child-process defenses.
func (plan *SuperPlan) ReadSegmentPartition(
	ctx context.Context,
	repositoryDirectory string,
	segment, ordinal int,
	visit func(BlobRecord, []byte) error,
) (ReadStats, error) {
	segmentPlan, err := plan.openSegment(segment)
	if err != nil {
		return ReadStats{}, err
	}
	return segmentPlan.ReadPartition(ctx, repositoryDirectory, ordinal, visit)
}

// ValidateSuperRootStage streams the complete stage once: the root control,
// each segment control bound to its entry, and every member through the
// retained v1 stage validation. Gaps, overlaps, extras, symlinks,
// non-canonical controls, cross-generation members, and digest or byte
// mismatches all refuse before any authority change.
func ValidateSuperRootStage(ctx context.Context, directory string, expected SuperRoot) error {
	if err := ValidateSuperRoot(expected); err != nil {
		return err
	}
	root, err := ReadSuperRoot(directory, expected.Repository)
	if err != nil {
		return err
	}
	if root.Digest != expected.Digest {
		return invalidf("super-root changed before validation")
	}
	wantNames := map[string]bool{SuperRootName(expected.Repository): true}
	for _, entry := range root.Segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		segmentDirectory := filepath.Join(directory, entry.Directory)
		info, statErr := os.Lstat(segmentDirectory)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return invalidf("segment directory is missing or special")
		}
		manifest, err := ReadManifest(segmentDirectory, root.Repository)
		if err != nil {
			return err
		}
		if err := checkSegmentBinding(root, entry, manifest); err != nil {
			return err
		}
		if err := validateOpenedStage(ctx, segmentDirectory, manifest); err != nil {
			return err
		}
		wantNames[entry.Directory] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != len(wantNames) {
		return invalidf("super-root stage contains an incomplete or extra artifact")
	}
	for _, entry := range entries {
		if !wantNames[entry.Name()] || entry.Type()&os.ModeSymlink != 0 {
			return invalidf("super-root stage contains an unknown or special artifact")
		}
	}
	return nil
}

func cloneSuperRoot(root SuperRoot) SuperRoot {
	result := root
	result.Policy.IncludeSuffixes = slices.Clone(root.Policy.IncludeSuffixes)
	result.Segments = slices.Clone(root.Segments)
	return result
}
