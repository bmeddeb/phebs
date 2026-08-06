package observationpublication

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
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

type Publication struct {
	root     string
	manifest Manifest
}

func (publication *Publication) Manifest() Manifest {
	if publication == nil {
		return Manifest{}
	}
	value := publication.manifest
	value.Members = append([]Member(nil), value.Members...)
	if value.OperationReceipt != nil {
		receipt := *value.OperationReceipt
		receipt.UnsupportedReasons = append([]ReasonCount(nil), receipt.UnsupportedReasons...)
		value.OperationReceipt = &receipt
	}
	return value
}

func (stage *Stage) Finalize(ctx context.Context) (*Publication, error) {
	if stage == nil {
		return nil, invalid("nil stage")
	}
	manifest := Manifest{
		Schema: ManifestSchema, Repository: stage.repository, Language: LanguagePack,
		SourceGenerationDigest:    stage.partition.SourceGenerationDigest,
		PartitionGenerationDigest: stage.partition.GenerationDigest,
		PartitionManifestDigest:   stage.partition.Digest,
		PartitionPolicyDigest:     stage.partition.PolicyDigest,
		ObservationPolicyDigest:   sourceobservation.PolicyDigest(),
		GenerationDigest:          stage.generation, Members: []Member{},
	}
	objects := make(map[string]int64)
	receipt := newReceiptAccumulator()
	for ordinal, sourceMember := range stage.partition.Members {
		member, records, err := readMember(ctx, stage.directory, ordinal, len(stage.partition.Members))
		if err != nil {
			return nil, err
		}
		member.SourceMemberDigest = sourceMember.Digest
		if member.RecordCount > MaxGenerationRecords-manifest.RecordCount {
			return nil, ErrLimit
		}
		manifest.Members = append(manifest.Members, member)
		manifest.RecordCount += member.RecordCount
		manifest.ObservedCount += member.ObservedCount
		manifest.UnsupportedCount += member.UnsupportedCount
		manifest.EncodedMemberBytes += member.ContentBytes
		for _, record := range records {
			if err := receipt.add(record); err != nil {
				return nil, err
			}
			if record.State != "observed" {
				continue
			}
			if _, present := objects[record.ObservationName]; present {
				continue
			}
			info, err := os.Lstat(filepath.Join(stage.directory, record.ObservationName))
			if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxObservationBytes {
				return nil, invalid("observation artifact")
			}
			objects[record.ObservationName] = info.Size()
			if info.Size() > MaxGenerationBytes-manifest.ObservationBytes {
				return nil, ErrLimit
			}
			manifest.ObservationBytes += info.Size()
		}
	}
	if manifest.RecordCount != stage.partition.BlobCount ||
		manifest.RecordCount != manifest.ObservedCount+manifest.UnsupportedCount ||
		manifest.EncodedMemberBytes > MaxGenerationBytes {
		return nil, invalid("generation totals")
	}
	manifest.OperationReceipt = receipt.value()
	digestValue, err := ManifestDigest(manifest)
	if err != nil {
		return nil, err
	}
	manifest.Digest = digestValue
	raw, err := json.Marshal(manifest)
	if err != nil || len(raw) > MaxManifestBytes {
		return nil, ErrLimit
	}
	if err := writeExclusiveBytes(filepath.Join(stage.directory, manifestName()), raw); err != nil {
		return nil, err
	}
	publication, err := openGeneration(ctx, stage.root, manifest)
	if err != nil {
		return nil, err
	}
	if err := syncDirectory(filepath.Join(stage.directory, "objects")); err != nil {
		return nil, err
	}
	if err := syncDirectory(stage.directory); err != nil {
		return nil, err
	}
	pointer := Pointer{
		Schema: PointerSchema, Repository: stage.repository,
		GenerationDigest: stage.generation, ManifestDigest: manifest.Digest,
		ManifestName: filepath.Join(strings.TrimPrefix(stage.generation, "sha256:"), manifestName()),
	}
	if err := writeAtomicCanonical(pointerPath(stage.root, stage.repository), pointer); err != nil {
		return nil, err
	}
	if err := os.Remove(markerPath(stage.root, stage.repository)); err != nil {
		return nil, err
	}
	if err := syncDirectory(repositoryDirectory(stage.root, stage.repository)); err != nil {
		return nil, err
	}
	return publication, nil
}

func Open(ctx context.Context, root, repository string) (*Publication, error) {
	if !filepath.IsAbs(root) || validateRepository(repository) != nil {
		return nil, invalid("open identity")
	}
	pointer, err := readPointer(root, repository)
	if err != nil {
		return nil, err
	}
	raw, err := readBoundedRegular(filepath.Join(repositoryDirectory(root, repository), pointer.ManifestName), MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil || manifest.Digest != pointer.ManifestDigest ||
		manifest.GenerationDigest != pointer.GenerationDigest || manifest.Repository != repository {
		return nil, invalid("pointer manifest mismatch")
	}
	return openGeneration(ctx, root, manifest)
}

func openGeneration(ctx context.Context, root string, manifest Manifest) (*Publication, error) {
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	directory := generationDirectory(root, manifest.Repository, manifest.GenerationDigest)
	objects := make(map[string]struct{})
	receipt := newReceiptAccumulator()
	var observationBytes int64
	for ordinal, expected := range manifest.Members {
		member, records, err := readMember(ctx, directory, ordinal, len(manifest.Members))
		member.SourceMemberDigest = expected.SourceMemberDigest
		if err != nil || member != expected {
			return nil, invalid("member mismatch")
		}
		for _, record := range records {
			if err := receipt.add(record); err != nil {
				return nil, err
			}
			if record.State != "observed" {
				continue
			}
			if _, seen := objects[record.ObservationName]; seen {
				continue
			}
			objects[record.ObservationName] = struct{}{}
			if err := validateObservation(directory, record); err != nil {
				return nil, err
			}
			info, err := os.Lstat(filepath.Join(directory, record.ObservationName))
			if err != nil || info.Size() > MaxGenerationBytes-observationBytes {
				return nil, invalid("observation byte totals")
			}
			observationBytes += info.Size()
		}
	}
	if observationBytes != manifest.ObservationBytes {
		return nil, invalid("observation byte total mismatch")
	}
	wantReceipt := receipt.value()
	if manifest.OperationReceipt != nil && (wantReceipt == nil ||
		!equalOperationReceipt(*wantReceipt, *manifest.OperationReceipt)) {
		return nil, invalid("operation receipt mismatch")
	}
	if err := validateGenerationInventory(directory, manifest, objects); err != nil {
		return nil, err
	}
	return &Publication{root: root, manifest: manifest}, nil
}

func validateGenerationInventory(
	directory string, manifest Manifest, objects map[string]struct{},
) error {
	entries, err := boundedDirectory(directory, len(manifest.Members)+2)
	if err != nil {
		return err
	}
	expected := make(map[string]struct{}, len(manifest.Members)+2)
	expected[manifestName()] = struct{}{}
	expected["objects"] = struct{}{}
	for _, member := range manifest.Members {
		expected[member.Name] = struct{}{}
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok {
			return invalid("unreferenced generation artifact")
		}
		if entry.Name() == "objects" {
			if !entry.IsDir() {
				return invalid("observation object directory")
			}
		} else if !entry.Mode().IsRegular() {
			return invalid("generation artifact is special")
		}
		delete(expected, entry.Name())
	}
	if len(expected) != 0 {
		return invalid("generation artifact is absent")
	}
	objectDirectory, err := os.Open(filepath.Join(directory, "objects"))
	if err != nil {
		return err
	}
	defer func() { _ = objectDirectory.Close() }()
	seen := 0
	for {
		entries, err := objectDirectory.Readdir(4_096)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			name := filepath.Join("objects", entry.Name())
			if _, ok := objects[name]; !ok || !entry.Mode().IsRegular() || !validObservationBasename(entry.Name()) {
				return invalid("unreferenced observation artifact")
			}
			seen++
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if seen != len(objects) {
		return invalid("observation inventory mismatch")
	}
	return nil
}

func validateManifest(value Manifest) error {
	expectedGeneration := digest("phebs-observation-generation-v1", strings.Join([]string{
		value.Repository, value.SourceGenerationDigest, value.PartitionGenerationDigest,
		value.PartitionManifestDigest, value.PartitionPolicyDigest,
		value.ObservationPolicyDigest, value.Language,
	}, "\x00"))
	if value.Schema != ManifestSchema || validateRepository(value.Repository) != nil ||
		value.Language != LanguagePack || !validDigest(value.SourceGenerationDigest) ||
		!validDigest(value.PartitionGenerationDigest) || !validDigest(value.PartitionManifestDigest) ||
		!validDigest(value.PartitionPolicyDigest) || value.ObservationPolicyDigest != sourceobservation.PolicyDigest() ||
		!validDigest(value.GenerationDigest) || value.GenerationDigest != expectedGeneration ||
		!validDigest(value.Digest) || value.Members == nil ||
		len(value.Members) > sourcepartition.MaxPartitions || value.RecordCount < 0 ||
		value.RecordCount > MaxGenerationRecords ||
		value.ObservedCount < 0 || value.UnsupportedCount < 0 ||
		value.RecordCount != value.ObservedCount+value.UnsupportedCount ||
		value.EncodedMemberBytes < 0 || value.EncodedMemberBytes > MaxGenerationBytes ||
		value.ObservationBytes < 0 || value.ObservationBytes > MaxGenerationBytes {
		return invalid("manifest identity")
	}
	if value.OperationReceipt != nil && validateOperationReceipt(*value.OperationReceipt, value) != nil {
		return invalid("manifest operation receipt")
	}
	var records, observed, unsupported int
	var encoded int64
	for ordinal, member := range value.Members {
		if member.Ordinal != ordinal || member.Count != len(value.Members) || member.Name != memberName(ordinal) ||
			!validDigest(member.SourceMemberDigest) || !validDigest(member.Digest) || member.RecordCount < 1 ||
			member.RecordCount > sourcepartition.MaxBlobsPerPartition ||
			member.RecordCount != member.ObservedCount+member.UnsupportedCount ||
			member.ContentBytes < 1 || member.ContentBytes > MaxMemberBytes {
			return invalid("manifest member")
		}
		records += member.RecordCount
		observed += member.ObservedCount
		unsupported += member.UnsupportedCount
		encoded += member.ContentBytes
	}
	if records != value.RecordCount || observed != value.ObservedCount || unsupported != value.UnsupportedCount || encoded != value.EncodedMemberBytes {
		return invalid("manifest totals")
	}
	want, err := ManifestDigest(value)
	if err != nil || want != value.Digest {
		return invalid("manifest digest")
	}
	return nil
}

func readMember(ctx context.Context, directory string, ordinal, count int) (Member, []Record, error) {
	name := memberName(ordinal)
	raw, err := readBoundedRegular(filepath.Join(directory, name), int(MaxMemberBytes))
	if err != nil {
		return Member{}, nil, err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("phebs-observation-member-v1\x00"))
	_, _ = hasher.Write(raw)
	member := Member{Ordinal: ordinal, Count: count, Name: name, ContentBytes: int64(len(raw)), Digest: "sha256:" + hex.EncodeToString(hasher.Sum(nil))}
	reader := bufio.NewReader(bytes.NewReader(raw))
	var records []Record
	previous := ""
	for {
		if err := ctx.Err(); err != nil {
			return Member{}, nil, err
		}
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(line) == 0 {
			break
		}
		if err != nil || len(line) < 2 || line[len(line)-1] != '\n' {
			return Member{}, nil, invalid("member framing")
		}
		line = line[:len(line)-1]
		var record Record
		if err := decodeCanonical(line, &record); err != nil || validateRecord(record) != nil || record.ObjectID <= previous {
			return Member{}, nil, invalid("member record")
		}
		previous = record.ObjectID
		records = append(records, record)
		member.RecordCount++
		if record.State == "observed" {
			member.ObservedCount++
		} else {
			member.UnsupportedCount++
		}
	}
	if member.RecordCount == 0 || member.RecordCount > sourcepartition.MaxBlobsPerPartition {
		return Member{}, nil, invalid("member record count")
	}
	return member, records, nil
}

func validateRecord(value Record) error {
	if value.Schema != RecordSchema || !gitobj.IsObjectID(value.ObjectID) || value.DeclaredBytes < 0 ||
		value.DeclaredBytes > sourcepartition.MaxBlobBytes || len(value.Placements) < 1 ||
		len(value.Placements) > sourcepartition.MaxPlacementsPerPartition || !validDigest(value.ContentDigest) {
		return invalid("record identity")
	}
	for index, placement := range value.Placements {
		if repopath.Validate(placement.Path) != nil || (placement.Mode != "100644" && placement.Mode != "100755") ||
			len(placement.Revisions) == 0 || index > 0 && placement.Path <= value.Placements[index-1].Path {
			return invalid("record placement")
		}
	}
	switch value.State {
	case "observed":
		if value.Reason != "" || !validDigest(value.ObservationDigest) || value.ObservationName == "" ||
			filepath.Clean(value.ObservationName) != value.ObservationName || filepath.IsAbs(value.ObservationName) ||
			filepath.Dir(value.ObservationName) != "objects" || !validObservationBasename(filepath.Base(value.ObservationName)) ||
			value.ObservationOrigin != "" && value.ObservationOrigin != "parsed" && value.ObservationOrigin != "reused" {
			return invalid("observed record")
		}
	case "unsupported":
		if !validUnsupportedReason(value.Reason) || value.ObservationDigest != "" || value.ObservationName != "" ||
			value.ObservationOrigin != "" {
			return invalid("unsupported record")
		}
	default:
		return invalid("record state")
	}
	return nil
}

type receiptAccumulator struct {
	input       int
	observed    int
	unsupported int
	legacy      bool
	origins     map[string]string
	reasons     map[string]int
}

func newReceiptAccumulator() *receiptAccumulator {
	return &receiptAccumulator{
		origins: make(map[string]string), reasons: make(map[string]int),
	}
}

func (receipt *receiptAccumulator) add(record Record) error {
	receipt.input++
	if record.State == "unsupported" {
		receipt.unsupported++
		receipt.reasons[record.Reason]++
		return nil
	}
	receipt.observed++
	if record.ObservationOrigin == "" {
		receipt.legacy = true
		return nil
	}
	if prior, present := receipt.origins[record.ObservationName]; present && prior != record.ObservationOrigin {
		return invalid("observation origin mismatch")
	}
	receipt.origins[record.ObservationName] = record.ObservationOrigin
	return nil
}

func (receipt *receiptAccumulator) value() *OperationReceipt {
	if receipt.legacy {
		return nil
	}
	result := &OperationReceipt{
		Schema: ReceiptSchema, InputBlobs: receipt.input,
		SourceBlobReads: receipt.input, ObservedBlobs: receipt.observed,
		UnsupportedBlobs: receipt.unsupported, UnsupportedReasons: []ReasonCount{},
	}
	for _, origin := range receipt.origins {
		if origin == "reused" {
			result.ReusedObservations++
		} else {
			result.ParsedObservations++
		}
	}
	for reason, count := range receipt.reasons {
		result.UnsupportedReasons = append(result.UnsupportedReasons, ReasonCount{Reason: reason, Count: count})
	}
	sort.Slice(result.UnsupportedReasons, func(left, right int) bool {
		return result.UnsupportedReasons[left].Reason < result.UnsupportedReasons[right].Reason
	})
	return result
}

func validateOperationReceipt(receipt OperationReceipt, manifest Manifest) error {
	if receipt.Schema != ReceiptSchema || receipt.InputBlobs != manifest.RecordCount ||
		receipt.SourceBlobReads != receipt.InputBlobs || receipt.ObservedBlobs != manifest.ObservedCount ||
		receipt.UnsupportedBlobs != manifest.UnsupportedCount || receipt.ParsedObservations < 0 ||
		receipt.ReusedObservations < 0 || receipt.ParsedObservations+receipt.ReusedObservations > receipt.ObservedBlobs ||
		receipt.UnsupportedReasons == nil {
		return invalid("operation receipt totals")
	}
	total := 0
	for index, reason := range receipt.UnsupportedReasons {
		if !validUnsupportedReason(reason.Reason) || reason.Count < 1 ||
			index > 0 && reason.Reason <= receipt.UnsupportedReasons[index-1].Reason {
			return invalid("operation receipt reason")
		}
		total += reason.Count
	}
	if total != receipt.UnsupportedBlobs {
		return invalid("operation receipt unsupported total")
	}
	return nil
}

func equalOperationReceipt(left, right OperationReceipt) bool {
	return left.Schema == right.Schema && left.InputBlobs == right.InputBlobs &&
		left.SourceBlobReads == right.SourceBlobReads && left.ParsedObservations == right.ParsedObservations &&
		left.ReusedObservations == right.ReusedObservations && left.ObservedBlobs == right.ObservedBlobs &&
		left.UnsupportedBlobs == right.UnsupportedBlobs && slices.Equal(left.UnsupportedReasons, right.UnsupportedReasons)
}

func validUnsupportedReason(reason string) bool {
	switch reason {
	case "source_limit", "invalid_utf8", "parse_error", "unsupported_source", "observation_too_large":
		return true
	default:
		return false
	}
}

func validateObservation(directory string, record Record) error {
	raw, err := readBoundedRegular(filepath.Join(directory, record.ObservationName), MaxObservationBytes)
	if err != nil || digest("phebs-observation-bytes-v1", string(raw)) != record.ObservationDigest {
		return invalid("observation bytes")
	}
	var observation sourceobservation.Observation
	if err := decodeCanonical(raw, &observation); err != nil || sourceobservation.Validate(observation) != nil ||
		observation.ContentDigest != record.ContentDigest || observation.PolicyDigest != sourceobservation.PolicyDigest() {
		return invalid("observation contract")
	}
	return nil
}

func readPointer(root, repository string) (Pointer, error) {
	raw, err := readBoundedRegular(pointerPath(root, repository), MaxManifestBytes)
	if err != nil {
		return Pointer{}, err
	}
	var pointer Pointer
	if err := decodeCanonical(raw, &pointer); err != nil || pointer.Schema != PointerSchema ||
		pointer.Repository != repository || !validDigest(pointer.GenerationDigest) ||
		!validDigest(pointer.ManifestDigest) || filepath.Clean(pointer.ManifestName) != pointer.ManifestName ||
		filepath.IsAbs(pointer.ManifestName) || pointer.ManifestName != filepath.Join(
		strings.TrimPrefix(pointer.GenerationDigest, "sha256:"), manifestName(),
	) {
		return Pointer{}, invalid("current pointer")
	}
	return pointer, nil
}

func readBoundedRegular(path string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > int64(limit) {
		return nil, errors.Join(err, invalid("artifact is missing, special, or oversized"))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.Join(err, invalid("artifact changed while opening"))
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if readErr != nil || statErr != nil || closeErr != nil || currentErr != nil ||
		!os.SameFile(opened, after) || !os.SameFile(after, current) ||
		!current.Mode().IsRegular() || after.Size() != info.Size() ||
		!after.ModTime().Equal(info.ModTime()) || current.Size() != after.Size() ||
		!current.ModTime().Equal(after.ModTime()) || int64(len(raw)) != info.Size() {
		return nil, errors.Join(readErr, statErr, closeErr, currentErr, invalid("artifact changed while reading"))
	}
	return raw, nil
}

func decodeCanonical(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return invalid("trailing JSON")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(canonical, raw) {
		return invalid("noncanonical JSON")
	}
	return nil
}

func writeAtomicCanonical(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pointer-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	written, writeErr := temporary.Write(raw)
	if writeErr != nil || written != len(raw) {
		_ = temporary.Close()
		return errors.Join(writeErr, io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
