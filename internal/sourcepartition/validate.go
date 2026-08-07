package sourcepartition

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/reponame"
)

// Plan is a completely validated partition stage. Its fields stay private so
// consumers cannot bypass complete-stage validation and stream a prefix from
// an incomplete plan.
type Plan struct {
	directory string
	manifest  Manifest
}

// MoveValidatedStage atomically installs a completely validated plan within
// its existing parent directory without reopening corpus-sized members while
// a caller holds its final authority fence. The destination must be absent;
// an existing content-addressed plan must be validated with Open instead.
func MoveValidatedStage(plan *Plan, destination string) (*Plan, error) {
	if plan == nil || !filepath.IsAbs(plan.directory) || !filepath.IsAbs(destination) ||
		filepath.Dir(plan.directory) != filepath.Dir(destination) ||
		filepath.Clean(plan.directory) == filepath.Clean(destination) {
		return nil, invalidf("plan move identity is invalid")
	}
	info, err := os.Lstat(plan.directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(err, invalidf("plan stage is missing or special"))
	}
	if _, err := os.Lstat(destination); err == nil {
		return nil, invalidf("plan destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(plan.directory, destination); err != nil {
		return nil, err
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return nil, err
	}
	return &Plan{directory: destination, manifest: cloneManifest(plan.manifest)}, nil
}

func Open(ctx context.Context, directory string, expected Manifest) (*Plan, error) {
	if !filepath.IsAbs(directory) {
		return nil, invalidf("plan directory must be absolute")
	}
	if err := ValidateStage(ctx, directory, expected); err != nil {
		return nil, err
	}
	return &Plan{directory: directory, manifest: cloneManifest(expected)}, nil
}

func (plan *Plan) Manifest() Manifest {
	if plan == nil {
		return Manifest{}
	}
	return cloneManifest(plan.manifest)
}

func ValidateManifest(manifest Manifest) error {
	if manifest.Schema != ManifestSchema || reponame.Validate(manifest.Repository) != nil ||
		!validDigest(manifest.SourceGenerationDigest) || manifest.RevisionCount < 1 ||
		manifest.RevisionCount > 8 || !validDigest(manifest.PolicyDigest) ||
		!validDigest(manifest.GenerationDigest) || !validDigest(manifest.Digest) ||
		manifest.Members == nil || len(manifest.Members) > MaxPartitions ||
		manifest.BlobCount < 0 || manifest.PlacementCount < manifest.BlobCount ||
		manifest.DeclaredBytes < 0 || manifest.DeclaredBytes > MaxGenerationBlobBytes ||
		manifest.EncodedMemberBytes < 0 ||
		manifest.EncodedMemberBytes > MaxGenerationContentBytes {
		return invalidf("manifest identity or totals are invalid")
	}
	if err := ValidatePolicy(manifest.Policy); err != nil {
		return err
	}
	policyDigest, err := PolicyDigest(manifest.Policy)
	if err != nil || policyDigest != manifest.PolicyDigest ||
		!reflect.DeepEqual(manifest.PartitionPolicy, frozenPolicy()) {
		return invalidf("manifest policy binding is invalid")
	}
	generation, err := GenerationDigest(
		manifest.Repository, manifest.SourceGenerationDigest, manifest.PolicyDigest,
	)
	if err != nil || generation != manifest.GenerationDigest {
		return invalidf("manifest generation binding is invalid")
	}
	if (manifest.BlobCount == 0) != (len(manifest.Members) == 0) ||
		(manifest.BlobCount == 0) != (manifest.PlacementCount == 0) ||
		(manifest.BlobCount == 0) != (manifest.DeclaredBytes == 0) ||
		(manifest.BlobCount == 0) != (manifest.EncodedMemberBytes == 0) {
		return invalidf("empty manifest shape is inconsistent")
	}
	blobs, placements := 0, 0
	var declared, encoded int64
	previousPrefix := ""
	for ordinal, member := range manifest.Members {
		if member.Ordinal != ordinal || member.Count != len(manifest.Members) ||
			member.Name != memberName(manifest.Repository, manifest.GenerationDigest, ordinal) ||
			!safeBaseName(member.Name) || member.PrefixBits != len(member.Prefix) ||
			member.PrefixBits < InitialPrefixBits || member.PrefixBits > sha256.Size*8 ||
			!binaryPrefix(member.Prefix) || member.BlobCount < 1 ||
			member.BlobCount > MaxBlobsPerPartition ||
			member.PlacementCount < member.BlobCount ||
			member.PlacementCount > MaxPlacementsPerPartition ||
			member.DeclaredBytes < 0 || member.DeclaredBytes > MaxPartitionBlobBytes ||
			member.ContentBytes < 1 || member.ContentBytes > MaxMemberContentBytes ||
			!validDigest(member.Digest) || !gitObjectRange(member) ||
			(ordinal > 0 && (member.Prefix <= previousPrefix ||
				strings.HasPrefix(member.Prefix, previousPrefix))) {
			return invalidf("manifest member is invalid or overlaps another prefix")
		}
		blobs += member.BlobCount
		placements += member.PlacementCount
		declared += member.DeclaredBytes
		encoded += member.ContentBytes
		previousPrefix = member.Prefix
	}
	if blobs != manifest.BlobCount || placements != manifest.PlacementCount ||
		declared != manifest.DeclaredBytes || encoded != manifest.EncodedMemberBytes {
		return invalidf("manifest member totals are inconsistent")
	}
	want, err := ManifestDigest(manifest)
	if err != nil || want != manifest.Digest {
		return invalidf("manifest digest mismatch")
	}
	return nil
}

func ReadManifest(directory, repository string) (Manifest, error) {
	var manifest Manifest
	if err := readCanonicalJSON(
		filepath.Join(directory, ManifestName(repository)), MaxManifestBytes, &manifest,
	); err != nil {
		return Manifest{}, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Repository != repository {
		return Manifest{}, invalidf("manifest repository mismatch")
	}
	return manifest, nil
}

// ValidateStage proves the complete non-authoritative partition plan before a
// scheduler or parser may bind it. No record is delivered during validation.
func ValidateStage(ctx context.Context, directory string, expected Manifest) error {
	if err := ValidateManifest(expected); err != nil {
		return err
	}
	opened, err := ReadManifest(directory, expected.Repository)
	if err != nil {
		return err
	}
	if opened.Digest != expected.Digest {
		return invalidf("manifest changed before validation")
	}
	wantNames := map[string]bool{ManifestName(expected.Repository): true}
	for _, member := range opened.Members {
		wantNames[member.Name] = true
		if _, err := readMember(
			ctx, directory, len(opened.Members), opened.RevisionCount, member,
		); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != len(wantNames) {
		return invalidf("stage contains an incomplete or extra artifact")
	}
	for _, entry := range entries {
		if !wantNames[entry.Name()] || entry.Type()&os.ModeType != 0 {
			return invalidf("stage contains an unknown or special artifact")
		}
	}
	return nil
}

func readMember(
	ctx context.Context,
	directory string,
	memberCount, revisionCount int,
	expected Member,
) ([]BlobRecord, error) {
	path := filepath.Join(directory, expected.Name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.ContentBytes {
		return nil, invalidf("partition member is missing, special, or changed")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, invalidf("partition member changed while opening")
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("phebs-source-partition-member-v1\x00"))
	reader := bufio.NewReaderSize(io.TeeReader(file, hasher), 64<<10)
	records := make([]BlobRecord, 0, expected.BlobCount)
	placements := 0
	var declared int64
	previous := ""
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		line, readErr := readLine(
			reader, MaxRecordBytes,
			pipelinerefusal.DimensionUnknown,
		)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			return nil, readErr
		}
		var record BlobRecord
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			_ = file.Close()
			return nil, invalidf("partition member record cannot be decoded")
		}
		canonical, err := json.Marshal(record)
		if err != nil || !bytes.Equal(canonical, line) {
			_ = file.Close()
			return nil, invalidf("partition member record is not canonical")
		}
		if err := validateBlobRecord(record, revisionCount); err != nil ||
			record.ObjectID <= previous || !hashHasPrefix(record.ObjectID, expected.Prefix) {
			_ = file.Close()
			return nil, invalidf("partition member record is invalid or unordered")
		}
		previous = record.ObjectID
		placements += len(record.Placements)
		declared += record.DeclaredBytes
		records = append(records, record)
	}
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if statErr != nil || closeErr != nil || currentErr != nil ||
		!os.SameFile(opened, after) || !os.SameFile(after, current) ||
		!current.Mode().IsRegular() || after.Size() != info.Size() ||
		!after.ModTime().Equal(info.ModTime()) || current.Size() != after.Size() ||
		!current.ModTime().Equal(after.ModTime()) {
		return nil, invalidf("partition member changed while reading")
	}
	if expected.Ordinal < 0 || expected.Ordinal >= memberCount ||
		len(records) != expected.BlobCount || len(records) == 0 ||
		placements != expected.PlacementCount || declared != expected.DeclaredBytes ||
		records[0].ObjectID != expected.FirstObjectID ||
		records[len(records)-1].ObjectID != expected.LastObjectID ||
		"sha256:"+hex.EncodeToString(hasher.Sum(nil)) != expected.Digest {
		return nil, invalidf("partition member disagrees with its manifest")
	}
	return records, nil
}

func readCanonicalJSON(path string, maximum int, value any) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > int64(maximum) {
		return invalidf("control file is missing, special, or oversized")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return invalidf("control file changed while opening")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if readErr != nil || statErr != nil || closeErr != nil || currentErr != nil {
		return errors.Join(readErr, statErr, closeErr, currentErr)
	}
	if !os.SameFile(opened, after) || !os.SameFile(after, current) ||
		!current.Mode().IsRegular() || after.Size() != info.Size() ||
		!after.ModTime().Equal(info.ModTime()) || current.Size() != after.Size() ||
		!current.ModTime().Equal(after.ModTime()) || int64(len(raw)) != info.Size() {
		return invalidf("control file changed while reading")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return invalidf("control file cannot be decoded")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalidf("control file has trailing JSON")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return invalidf("control file is not canonical")
	}
	return nil
}

func hashHasPrefix(objectID, prefix string) bool {
	hash := sourceBlobHash(objectID)
	return bitPrefix(hash[:], len(prefix)) == prefix
}

func binaryPrefix(value string) bool {
	for _, current := range value {
		if current != '0' && current != '1' {
			return false
		}
	}
	return true
}

func gitObjectRange(member Member) bool {
	return gitobj.IsObjectID(member.FirstObjectID) &&
		gitobj.IsObjectID(member.LastObjectID) &&
		member.LastObjectID >= member.FirstObjectID
}

func cloneManifest(manifest Manifest) Manifest {
	result := manifest
	result.Policy.IncludeSuffixes = slices.Clone(manifest.Policy.IncludeSuffixes)
	result.Members = slices.Clone(manifest.Members)
	return result
}
