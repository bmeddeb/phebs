package sourcepartition

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
)

type BuildRequest struct {
	SourceDirectory string
	OutputDirectory string
	Repository      string
	Source          repositoryindex.SourceManifest
	Policy          Policy
	Metrics         *BuildMetrics
	// PriorSuperRootDirectory optionally names a fully validated v2 stage whose
	// unchanged members may be hard-linked into a new v2 build. V1 Build ignores
	// this field.
	PriorSuperRootDirectory string
	SuperRootMetrics        *SuperRootBuildMetrics
}

type BuildMetrics struct {
	SelectedPlacements int
	UniqueBlobs        int
	UniqueBlobBytes    int64
	PeakSpoolBytes     int64
	currentSpoolBytes  int64
}

// SuperRootBuildMetrics makes physical reuse observable rather than inferring
// it from deterministic byte equality.
type SuperRootBuildMetrics struct {
	ReusedMembers  int
	ReusedBytes    int64
	WrittenMembers int
	WrittenBytes   int64
}

type spoolRecord struct {
	ObjectID      string `json:"object_id"`
	DeclaredBytes int64  `json:"declared_bytes"`
	Path          string `json:"path"`
	Mode          string `json:"mode"`
	Revisions     []int  `json:"revisions"`
	Hash          string `json:"hash"`
}

type spool struct {
	path          string
	prefix        string
	bits          int
	placements    int
	declaredBytes int64
	contentBytes  int64
	metrics       *BuildMetrics
}

var sourceBlobHash = productionSourceBlobHash

func productionSourceBlobHash(objectID string) [sha256.Size]byte {
	return sha256.Sum256([]byte(BlobHashPolicy + "\x00" + objectID))
}

func checkPlanLimit(
	dimension pipelinerefusal.Dimension,
	observed,
	limit int64,
	message string,
) error {
	if observed <= limit {
		return nil
	}
	return pipelinerefusal.Measure(
		fmt.Errorf("%w: %s", ErrTooLarge, message),
		dimension, observed, limit,
	)
}

func Build(ctx context.Context, request BuildRequest) (Manifest, error) {
	if request.Metrics != nil {
		*request.Metrics = BuildMetrics{}
	}
	metrics := request.Metrics
	if metrics == nil {
		metrics = &BuildMetrics{}
	}
	if !filepath.IsAbs(request.SourceDirectory) ||
		!filepath.IsAbs(request.OutputDirectory) {
		return Manifest{}, invalidf("build directories must be absolute")
	}
	if err := ValidatePolicy(request.Policy); err != nil {
		return Manifest{}, err
	}
	if err := repositoryindex.ValidateSourceManifest(request.Source); err != nil ||
		request.Source.Repository != request.Repository {
		return Manifest{}, invalidf("source generation identity is invalid")
	}
	opened, err := repositoryindex.ReadSourceManifest(
		request.SourceDirectory, request.Repository,
	)
	if err != nil || opened.Digest != request.Source.Digest {
		return Manifest{}, invalidf("source generation changed before planning")
	}
	if err := requireEmptyDirectory(request.OutputDirectory); err != nil {
		return Manifest{}, err
	}
	policyDigest, err := PolicyDigest(request.Policy)
	if err != nil {
		return Manifest{}, err
	}
	generationDigest, err := GenerationDigest(
		request.Repository, request.Source.Digest, policyDigest,
	)
	if err != nil {
		return Manifest{}, err
	}
	spoolDirectory, err := os.MkdirTemp(request.OutputDirectory, ".source-partition-spool-")
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = os.RemoveAll(spoolDirectory) }()

	leaves, err := planLeaves(ctx, request, metrics, spoolDirectory)
	if err != nil {
		return Manifest{}, err
	}

	members := make([]Member, 0, len(leaves))
	manifest := Manifest{
		Schema: ManifestSchema, Repository: request.Repository,
		SourceGenerationDigest: request.Source.Digest,
		RevisionCount:          len(request.Source.Revisions),
		Policy:                 request.Policy, PolicyDigest: policyDigest,
		PartitionPolicy: frozenPolicy(), GenerationDigest: generationDigest,
		Members: []Member{},
	}
	for ordinal, leaf := range leaves {
		member, err := writeMember(
			ctx, request.OutputDirectory, request.Repository,
			generationDigest, len(leaves), ordinal, leaf,
		)
		if err != nil {
			return Manifest{}, err
		}
		members = append(members, member)
		manifest.BlobCount += member.BlobCount
		manifest.PlacementCount += member.PlacementCount
		if member.DeclaredBytes > MaxGenerationBlobBytes-manifest.DeclaredBytes {
			return Manifest{}, checkPlanLimit(
				pipelinerefusal.DimensionGenerationBlobBytes,
				manifest.DeclaredBytes+member.DeclaredBytes,
				MaxGenerationBlobBytes,
				"generation declared bytes exceed their bound",
			)
		}
		if member.ContentBytes > MaxGenerationContentBytes-manifest.EncodedMemberBytes {
			return Manifest{}, checkPlanLimit(
				pipelinerefusal.DimensionGenerationEncodedBytes,
				manifest.EncodedMemberBytes+member.ContentBytes,
				MaxGenerationContentBytes,
				"generation encoded bytes exceed their bound",
			)
		}
		manifest.DeclaredBytes += member.DeclaredBytes
		manifest.EncodedMemberBytes += member.ContentBytes
		metrics.UniqueBlobs += member.BlobCount
		metrics.UniqueBlobBytes += member.DeclaredBytes
		if err := removeSpool(leaf); err != nil {
			return Manifest{}, err
		}
	}
	manifest.Members = members
	manifest.Digest, err = ManifestDigest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := writeCanonicalJSON(
		filepath.Join(request.OutputDirectory, ManifestName(request.Repository)),
		manifest, MaxManifestBytes,
	); err != nil {
		return Manifest{}, err
	}
	if err := syncDirectory(request.OutputDirectory); err != nil {
		return Manifest{}, err
	}
	if err := os.RemoveAll(spoolDirectory); err != nil {
		return Manifest{}, err
	}
	if err := ValidateStage(ctx, request.OutputDirectory, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// planLeaves performs the one streamed repository-source pass: every selected
// record is spooled by hash prefix and split into bounded ordered leaves. Both
// the v1 and v2 builders consume the same pass; neither reopens the source.
func planLeaves(
	ctx context.Context,
	request BuildRequest,
	metrics *BuildMetrics,
	spoolDirectory string,
) ([]*spool, error) {
	roots := make(map[string]*spool)
	_, err := repositoryindex.WalkPublishedSource(
		ctx, request.SourceDirectory, request.Repository,
		func(record repositoryindex.SourceRecord) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if record.Kind != "regular" || !matches(request.Policy, record.Path) {
				return nil
			}
			if record.DeclaredBytes > MaxBlobBytes {
				return checkPlanLimit(
					pipelinerefusal.DimensionBlobBytes,
					record.DeclaredBytes, MaxBlobBytes,
					"selected blob exceeds the per-blob bound",
				)
			}
			if metrics.SelectedPlacements == math.MaxInt {
				return fmt.Errorf("%w: selected placement count overflows", ErrTooLarge)
			}
			hash := sourceBlobHash(record.ObjectID)
			prefix := bitPrefix(hash[:], InitialPrefixBits)
			current := roots[prefix]
			if current == nil {
				current = &spool{
					path:   filepath.Join(spoolDirectory, "root-"+prefix+".jsonl"),
					prefix: prefix, bits: InitialPrefixBits, metrics: metrics,
				}
				roots[prefix] = current
			}
			if err := appendSpool(current, spoolRecord{
				ObjectID: record.ObjectID, DeclaredBytes: record.DeclaredBytes,
				Path: record.Path, Mode: record.Mode,
				Revisions: slices.Clone(record.Revisions),
				Hash:      hex.EncodeToString(hash[:]),
			}); err != nil {
				return err
			}
			metrics.SelectedPlacements++
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	prefixes := make([]string, 0, len(roots))
	for prefix := range roots {
		prefixes = append(prefixes, prefix)
	}
	slices.Sort(prefixes)
	leaves := make([]*spool, 0)
	for _, prefix := range prefixes {
		if err := splitSpool(ctx, spoolDirectory, roots[prefix], &leaves); err != nil {
			return nil, err
		}
	}
	if len(leaves) > MaxPartitions {
		return nil, checkPlanLimit(
			pipelinerefusal.DimensionPartitionCount,
			int64(len(leaves)), MaxPartitions,
			"partition count exceeds its bound",
		)
	}
	return leaves, nil
}

func appendSpool(destination *spool, record spoolRecord) error {
	if destination.placements == math.MaxInt ||
		record.DeclaredBytes > math.MaxInt64-destination.declaredBytes {
		return fmt.Errorf("%w: spool summary overflows", ErrTooLarge)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if len(payload) > MaxRecordBytes {
		return invalidf("spool record violates its encoded bound")
	}
	if destination.metrics != nil &&
		int64(len(payload)) > MaxSpoolBytes-destination.metrics.currentSpoolBytes {
		return checkPlanLimit(
			pipelinerefusal.DimensionPlanningSpoolBytes,
			destination.metrics.currentSpoolBytes+int64(len(payload)),
			MaxSpoolBytes, "planning spool bytes exceed their bound",
		)
	}
	file, err := os.OpenFile(
		destination.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600,
	)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(payload)
	closeErr := file.Close()
	if written > 0 {
		destination.contentBytes += int64(written)
		if destination.metrics != nil {
			destination.metrics.currentSpoolBytes += int64(written)
			if destination.metrics.currentSpoolBytes > destination.metrics.PeakSpoolBytes {
				destination.metrics.PeakSpoolBytes = destination.metrics.currentSpoolBytes
			}
		}
	}
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	destination.placements++
	destination.declaredBytes += record.DeclaredBytes
	return nil
}

func splitSpool(ctx context.Context, directory string, current *spool, leaves *[]*spool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	allSameObject, allSameHash, err := spoolIdentity(ctx, current)
	if err != nil {
		return err
	}
	if allSameObject {
		if current.placements > MaxPlacementsPerPartition {
			return checkPlanLimit(
				pipelinerefusal.DimensionBlobPlacements,
				int64(current.placements), MaxPlacementsPerPartition,
				"one blob has too many placements",
			)
		}
		if len(*leaves) >= MaxPartitions {
			return checkPlanLimit(
				pipelinerefusal.DimensionPartitionCount,
				int64(len(*leaves)+1), MaxPartitions,
				"partition count exceeds its bound",
			)
		}
		*leaves = append(*leaves, current)
		return nil
	}
	if current.placements <= MaxPlacementsPerPartition &&
		current.declaredBytes <= MaxPartitionBlobBytes {
		if len(*leaves) >= MaxPartitions {
			return checkPlanLimit(
				pipelinerefusal.DimensionPartitionCount,
				int64(len(*leaves)+1), MaxPartitions,
				"partition count exceeds its bound",
			)
		}
		*leaves = append(*leaves, current)
		return nil
	}
	if current.bits >= sha256.Size*8 || allSameHash {
		return ErrHashCollision
	}
	left := &spool{
		path:   filepath.Join(directory, "split-"+current.prefix+"0.jsonl"),
		prefix: current.prefix + "0", bits: current.bits + 1, metrics: current.metrics,
	}
	right := &spool{
		path:   filepath.Join(directory, "split-"+current.prefix+"1.jsonl"),
		prefix: current.prefix + "1", bits: current.bits + 1, metrics: current.metrics,
	}
	if err := forEachSpool(ctx, current.path, func(record spoolRecord) error {
		hash, err := hex.DecodeString(record.Hash)
		if err != nil || len(hash) != sha256.Size {
			return invalidf("spool hash is invalid")
		}
		destination := left
		if hashBit(hash, current.bits) == 1 {
			destination = right
		}
		return appendSpool(destination, record)
	}); err != nil {
		return err
	}
	if err := removeSpool(current); err != nil {
		return err
	}
	for _, child := range []*spool{left, right} {
		if child.placements == 0 {
			continue
		}
		if err := splitSpool(ctx, directory, child, leaves); err != nil {
			return err
		}
	}
	return nil
}

func spoolIdentity(ctx context.Context, current *spool) (bool, bool, error) {
	firstObject, firstHash := "", ""
	allSameObject, allSameHash := true, true
	err := forEachSpool(ctx, current.path, func(record spoolRecord) error {
		if firstObject == "" {
			firstObject, firstHash = record.ObjectID, record.Hash
			return nil
		}
		allSameObject = allSameObject && record.ObjectID == firstObject
		allSameHash = allSameHash && record.Hash == firstHash
		return nil
	})
	return allSameObject, allSameHash, err
}

func forEachSpool(ctx context.Context, path string, visit func(spoolRecord) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return err
		}
		line, err := readLine(
			reader, MaxRecordBytes,
			pipelinerefusal.DimensionUnknown,
		)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = file.Close()
			return err
		}
		var record spoolRecord
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			_ = file.Close()
			return invalidf("decode spool record")
		}
		if !gitObjectAndHashValid(record) {
			_ = file.Close()
			return invalidf("spool record identity is invalid")
		}
		if err := visit(record); err != nil {
			_ = file.Close()
			return err
		}
	}
	return file.Close()
}

func gitObjectAndHashValid(record spoolRecord) bool {
	if !gitobj.IsObjectID(record.ObjectID) || len(record.Hash) != sha256.Size*2 ||
		repopath.Validate(record.Path) != nil ||
		(record.Mode != "100644" && record.Mode != "100755") ||
		len(record.Revisions) == 0 || !slices.IsSorted(record.Revisions) ||
		record.DeclaredBytes < 0 ||
		record.DeclaredBytes > MaxBlobBytes {
		return false
	}
	hash, err := hex.DecodeString(record.Hash)
	if err != nil || len(hash) != sha256.Size {
		return false
	}
	want := sourceBlobHash(record.ObjectID)
	return hex.EncodeToString(want[:]) == record.Hash
}

func writeMember(
	ctx context.Context,
	directory, repository, generation string,
	count, ordinal int,
	leaf *spool,
) (Member, error) {
	name := memberName(repository, generation, ordinal)
	member, err := writeMemberAt(ctx, filepath.Join(directory, name), leaf)
	if err != nil {
		return Member{}, err
	}
	member.Ordinal, member.Count, member.Name = ordinal, count, name
	return member, nil
}

// writeMemberAt encodes one ordered coalesced member and hashes exactly the
// bytes it writes. Identity fields that depend on final placement (ordinal,
// count, name) stay unset: member content is placement-independent, which is
// what makes byte-exact reuse across generations possible.
func writeMemberAt(ctx context.Context, path string, leaf *spool) (Member, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Member{}, err
	}
	member, err := encodeMember(ctx, file, leaf)
	if err != nil {
		_ = file.Close()
		return Member{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return Member{}, err
	}
	if err := file.Close(); err != nil {
		return Member{}, err
	}
	return member, nil
}

// summarizeMember performs the canonical encoding and digest pass without
// materializing bytes. A matching validated prior member can then be linked
// directly; only misses perform the second pass that writes new bytes.
func summarizeMember(ctx context.Context, leaf *spool) (Member, error) {
	return encodeMember(ctx, io.Discard, leaf)
}

func encodeMember(ctx context.Context, output io.Writer, leaf *spool) (Member, error) {
	records := make([]spoolRecord, 0, leaf.placements)
	if err := forEachSpool(ctx, leaf.path, func(record spoolRecord) error {
		records = append(records, record)
		return nil
	}); err != nil {
		return Member{}, err
	}
	sort.Slice(records, func(left, right int) bool {
		return compareSpoolRecord(records[left], records[right]) < 0
	})
	blobs := make([]BlobRecord, 0, len(records))
	for _, record := range records {
		if len(blobs) == 0 || blobs[len(blobs)-1].ObjectID != record.ObjectID {
			blobs = append(blobs, BlobRecord{
				Schema: MemberSchema, ObjectID: record.ObjectID,
				DeclaredBytes: record.DeclaredBytes, Placements: []Placement{},
			})
		} else if blobs[len(blobs)-1].DeclaredBytes != record.DeclaredBytes {
			return Member{}, invalidf("one object has inconsistent declared sizes")
		}
		current := &blobs[len(blobs)-1]
		placement := Placement{
			Path: record.Path, Mode: record.Mode,
			Revisions: slices.Clone(record.Revisions),
		}
		if len(current.Placements) > 0 &&
			comparePlacement(current.Placements[len(current.Placements)-1], placement) >= 0 {
			return Member{}, invalidf("duplicate or unordered placement")
		}
		current.Placements = append(current.Placements, placement)
	}
	if len(blobs) > MaxBlobsPerPartition {
		return Member{}, invalidf("planned member blob count violates its partition")
	}
	if len(records) > MaxPlacementsPerPartition {
		return Member{}, invalidf("planned member placements violate their partition")
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("phebs-source-partition-member-v1\x00"))
	writtenOutput := io.MultiWriter(output, hasher)
	member := Member{
		Prefix: leaf.prefix, PrefixBits: leaf.bits,
		PlacementCount: len(records), BlobCount: len(blobs),
	}
	for index, blob := range blobs {
		if err := ctx.Err(); err != nil {
			return Member{}, err
		}
		if blob.DeclaredBytes > MaxPartitionBlobBytes-member.DeclaredBytes {
			return Member{}, invalidf("planned member bytes violate their partition")
		}
		payload, err := json.Marshal(blob)
		if err != nil {
			return Member{}, err
		}
		payload = append(payload, '\n')
		if int64(len(payload)) > MaxMemberContentBytes-member.ContentBytes {
			return Member{}, invalidf("planned member metadata violates its partition")
		}
		written, err := writtenOutput.Write(payload)
		if err != nil || written != len(payload) {
			return Member{}, errors.Join(err, io.ErrShortWrite)
		}
		member.DeclaredBytes += blob.DeclaredBytes
		member.ContentBytes += int64(written)
		if index == 0 {
			member.FirstObjectID = blob.ObjectID
		}
		member.LastObjectID = blob.ObjectID
	}
	member.Digest = "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	return member, nil
}

func compareSpoolRecord(left, right spoolRecord) int {
	if value := strings.Compare(left.ObjectID, right.ObjectID); value != 0 {
		return value
	}
	return comparePlacement(
		Placement{Path: left.Path, Mode: left.Mode, Revisions: left.Revisions},
		Placement{Path: right.Path, Mode: right.Mode, Revisions: right.Revisions},
	)
}

func removeSpool(current *spool) error {
	if err := os.Remove(current.path); err != nil {
		return err
	}
	if current.metrics != nil {
		current.metrics.currentSpoolBytes -= current.contentBytes
		if current.metrics.currentSpoolBytes < 0 {
			current.metrics.currentSpoolBytes = 0
		}
	}
	return nil
}

func bitPrefix(hash []byte, bits int) string {
	var builder strings.Builder
	builder.Grow(bits)
	for bit := 0; bit < bits; bit++ {
		builder.WriteByte('0' + hashBit(hash, bit))
	}
	return builder.String()
}

func hashBit(hash []byte, bit int) byte {
	return (hash[bit/8] >> (7 - uint(bit%8))) & 1
}

func readLine(
	reader *bufio.Reader,
	maximum int,
	dimension pipelinerefusal.Dimension,
) ([]byte, error) {
	var result []byte
	for {
		fragment, prefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(fragment) > maximum-len(result) {
			if dimension == pipelinerefusal.DimensionUnknown {
				return nil, invalidf("encoded line violates its bound")
			}
			return nil, checkPlanLimit(
				dimension, int64(len(result)+len(fragment)), int64(maximum),
				"line exceeds its byte bound",
			)
		}
		result = append(result, fragment...)
		if !prefix {
			return result, nil
		}
	}
}

func requireEmptyDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return invalidf("output directory is missing or special")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return invalidf("output directory must be empty")
	}
	return nil
}

func writeCanonicalJSON(path string, value any, maximum int) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maximum {
		return checkPlanLimit(
			pipelinerefusal.DimensionGenerationControlBytes,
			int64(len(raw)), int64(maximum),
			"control bytes exceed their bound",
		)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(raw)
	if writeErr != nil || written != len(raw) {
		_ = file.Close()
		return errors.Join(writeErr, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	err = file.Sync()
	return errors.Join(err, file.Close())
}
