package repositoryindex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/repopath"
)

func validateSourceMembers(
	ctx context.Context,
	directory string,
	manifest SourceManifest,
	visit func(SourceRecord) error,
) error {
	revisionHashes := make([]hash.Hash, len(manifest.Revisions))
	revisionCounts := make([]RevisionMember, len(manifest.Revisions))
	for index := range revisionHashes {
		revisionHashes[index] = sha256.New()
		_, _ = revisionHashes[index].Write(
			[]byte("phebs-repository-source-revision-v1\x00"),
		)
	}
	owners, placements := 0, 0
	regular, symlinks, gitlinks := 0, 0, 0
	var regularBytes, encodedBytes int64
	previousKey, previousPath := "", ""
	pathRevisions := make(map[int]bool, len(manifest.Revisions))
	for _, member := range manifest.Members {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(directory, member.Name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() ||
			info.Size() != member.ContentBytes {
			return invalidf("source member %q identity mismatch", member.Name)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			_ = file.Close()
			return invalidf("source member %q changed while opening", member.Name)
		}
		hasher := sha256.New()
		reader := bufio.NewReaderSize(io.TeeReader(file, hasher), 64<<10)
		memberRecords := 0
		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return err
			}
			line, done, readErr := readNewlineRecord(
				reader, maxSourceRecordBytes,
			)
			if readErr != nil {
				_ = file.Close()
				return readErr
			}
			if done {
				break
			}
			if len(line) == 0 {
				_ = file.Close()
				return invalidf("source member contains an empty record")
			}
			var record SourceRecord
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&record); err != nil {
				_ = file.Close()
				return invalidf("decode source member: %v", err)
			}
			if err := readaccounting.Charge(ctx, readaccounting.MemberVisit, 1); err != nil {
				_ = file.Close()
				return err
			}
			if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				_ = file.Close()
				return invalidf("source member has trailing JSON")
			}
			canonical, err := json.Marshal(record)
			if err != nil || !bytes.Equal(canonical, line) {
				_ = file.Close()
				return invalidf("source member record is not canonical")
			}
			if err := validateRecord(record, len(manifest.Revisions)); err != nil {
				_ = file.Close()
				return err
			}
			key := recordKey(record)
			if key <= previousKey {
				_ = file.Close()
				return invalidf("source records are duplicated or unordered")
			}
			if record.Path != previousPath {
				clear(pathRevisions)
				previousPath = record.Path
			}
			for _, revision := range record.Revisions {
				if pathRevisions[revision] {
					_ = file.Close()
					return invalidf(
						"path %q has multiple physical owners in revision %d",
						record.Path, revision,
					)
				}
				pathRevisions[revision] = true
				current := treeRecord{
					mode: record.Mode, kind: record.Kind,
					objectID: record.ObjectID, path: record.Path,
					declaredBytes: record.DeclaredBytes,
				}
				hashPlacement(revisionHashes[revision], current)
				revisionCounts[revision].PlacementCount++
				switch record.Kind {
				case "regular":
					revisionCounts[revision].RegularCount++
				case "symlink":
					revisionCounts[revision].SymlinkCount++
				case "gitlink":
					revisionCounts[revision].GitlinkCount++
				}
			}
			owners++
			placements += len(record.Revisions)
			switch record.Kind {
			case "regular":
				regular++
				if record.DeclaredBytes > math.MaxInt64-regularBytes {
					_ = file.Close()
					return invalidf("regular source bytes overflow")
				}
				regularBytes += record.DeclaredBytes
			case "symlink":
				symlinks++
			case "gitlink":
				gitlinks++
			}
			memberRecords++
			previousKey = key
			if visit != nil {
				if err := visit(record); err != nil {
					_ = file.Close()
					return err
				}
			}
		}
		after, statErr := file.Stat()
		closeErr := file.Close()
		current, currentErr := os.Lstat(path)
		if statErr != nil || closeErr != nil || currentErr != nil ||
			!os.SameFile(opened, after) || !os.SameFile(after, current) ||
			!current.Mode().IsRegular() || after.Size() != info.Size() ||
			!after.ModTime().Equal(info.ModTime()) || current.Size() != after.Size() ||
			!current.ModTime().Equal(after.ModTime()) ||
			memberRecords != member.RecordCount ||
			"sha256:"+hex.EncodeToString(hasher.Sum(nil)) != member.Digest {
			return invalidf("source member %q content mismatch", member.Name)
		}
		encodedBytes += member.ContentBytes
	}
	if owners != manifest.OwnerCount || placements != manifest.PlacementCount ||
		regular != manifest.RegularOwnerCount ||
		symlinks != manifest.SymlinkOwnerCount ||
		gitlinks != manifest.GitlinkOwnerCount ||
		regularBytes != manifest.RegularDeclaredBytes ||
		encodedBytes != manifest.EncodedMemberBytes {
		return invalidf("source member census disagrees with its root")
	}
	for ordinal, expected := range manifest.RevisionMembers {
		counts := revisionCounts[ordinal]
		gotDigest := "sha256:" + hex.EncodeToString(
			revisionHashes[ordinal].Sum(nil),
		)
		if counts.PlacementCount != expected.PlacementCount ||
			counts.RegularCount != expected.RegularCount ||
			counts.SymlinkCount != expected.SymlinkCount ||
			counts.GitlinkCount != expected.GitlinkCount ||
			gotDigest != expected.Digest {
			return invalidf("source revision %d disagrees with its root", ordinal)
		}
	}
	return nil
}

// ValidateSourceGeneration proves that one expected staged or published root
// and every byte it names still agree before another authority binds it.
func ValidateSourceGeneration(
	ctx context.Context,
	directory string,
	expected SourceManifest,
) error {
	if err := ValidateSourceManifest(expected); err != nil {
		return err
	}
	opened, err := ReadSourceManifest(directory, expected.Repository)
	if err != nil {
		return err
	}
	if opened.Digest != expected.Digest ||
		!slices.Equal(opened.Revisions, expected.Revisions) {
		return invalidf("source generation changed before binding")
	}
	return validateSourceMembers(ctx, directory, opened, nil)
}

func validateRecord(record SourceRecord, revisionCount int) error {
	if record.Schema != SourceMemberSchema ||
		record.Path == "" || record.Mode == "" || record.ObjectID == "" ||
		record.DeclaredBytes < 0 || len(record.Revisions) == 0 ||
		len(record.Revisions) > revisionCount ||
		!gitobj.IsObjectID(record.ObjectID) {
		return invalidf("source record identity is invalid")
	}
	if err := repopath.Validate(record.Path); err != nil {
		return invalidf("source record path is not canonical")
	}
	switch record.Kind {
	case "regular":
		if record.Mode != "100644" && record.Mode != "100755" {
			return invalidf("regular source record mode is invalid")
		}
	case "symlink":
		if record.Mode != "120000" {
			return invalidf("symlink source record mode is invalid")
		}
	case "gitlink":
		if record.Mode != "160000" || record.DeclaredBytes != 0 {
			return invalidf("gitlink source record is invalid")
		}
	default:
		return invalidf("source record kind is invalid")
	}
	if !slices.IsSorted(record.Revisions) {
		return invalidf("source record revisions are unordered")
	}
	for index, revision := range record.Revisions {
		if revision < 0 || revision >= revisionCount ||
			(index > 0 && record.Revisions[index-1] == revision) {
			return invalidf("source record revision is invalid or duplicated")
		}
	}
	return nil
}

// WalkPublishedSource validates the complete generation before yielding any
// record to a consumer. Callers therefore never observe a prefix from a
// corrupt member set.
func WalkPublishedSource(
	ctx context.Context,
	directory, repository string,
	visit func(SourceRecord) error,
) (SourceManifest, error) {
	manifest, err := ReadSourceManifestContext(ctx, directory, repository)
	if err != nil {
		return SourceManifest{}, err
	}
	// The validation pass is intentionally separate from delivery. Catalog and
	// later source consumers can rely on an exact root rather than processing a
	// prefix before discovering a corrupt final member.
	if err := validateSourceMembers(ctx, directory, manifest, nil); err != nil {
		return SourceManifest{}, err
	}
	if visit != nil {
		if err := validateSourceMembers(ctx, directory, manifest, visit); err != nil {
			return SourceManifest{}, err
		}
	}
	return manifest, nil
}
