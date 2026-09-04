// Package repositoryindex defines the immutable repository source and search
// generation receipts used by the multi-service search pipeline.
package repositoryindex

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
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	SourceManifestSchema = "phebs-repository-source-generation-v1"
	SourceMemberSchema   = "phebs-repository-source-member-v1"
	SearchManifestSchema = "phebs-repository-search-generation-v1"
	CensusPolicy         = "git-ls-tree-physical-owner-v1"
	DirectTopologyPolicy = "zoekt-direct-whole-repository-v1"

	MaxOwners                 = 10_000_000
	MaxPlacements             = MaxOwners * 8
	MaxRecordsPerMember       = 4_096
	MaxSourceMembers          = 16_384
	MaxMemberBytes      int64 = 64 << 20
	MaxGenerationBytes        = int64(4 << 30)

	maxControlBytes       = 8 << 20
	maxGitTreeRecordBytes = repopath.MaxBytes + 128
	maxSourceRecordBytes  = repopath.MaxBytes*6 + 512
)

var ErrInvalidGeneration = errors.New("invalid repository generation")

// SourceRecord gives one immutable Git tree object one physical owner and
// lists every indexed revision placement that reuses it. Records are ordered
// by path, mode, kind, and object ID; revision ordinals are ordered and unique.
type SourceRecord struct {
	Schema        string `json:"schema"`
	Path          string `json:"path"`
	Mode          string `json:"mode"`
	Kind          string `json:"kind"`
	ObjectID      string `json:"object_id"`
	DeclaredBytes int64  `json:"declared_bytes"`
	Revisions     []int  `json:"revisions"`
}

type RevisionMember struct {
	Ordinal        int    `json:"ordinal"`
	Selector       string `json:"selector"`
	Branch         string `json:"branch"`
	Commit         string `json:"commit"`
	PlacementCount int    `json:"placement_count"`
	RegularCount   int    `json:"regular_count"`
	SymlinkCount   int    `json:"symlink_count"`
	GitlinkCount   int    `json:"gitlink_count"`
	Digest         string `json:"digest"`
}

type SourceMember struct {
	Ordinal      int    `json:"ordinal"`
	Count        int    `json:"count"`
	Name         string `json:"name"`
	RecordCount  int    `json:"record_count"`
	ContentBytes int64  `json:"content_bytes"`
	Digest       string `json:"digest"`
	FirstKey     string `json:"first_key"`
	LastKey      string `json:"last_key"`
}

// SourceManifest is a content-addressed root over the exact indexed revision
// set and its streamed, deduplicated physical-owner census.
type SourceManifest struct {
	Schema               string                  `json:"schema"`
	Repository           string                  `json:"repository"`
	CensusPolicy         string                  `json:"census_policy"`
	Revisions            []store.IndexedRevision `json:"revisions"`
	RevisionMembers      []RevisionMember        `json:"revision_members"`
	Members              []SourceMember          `json:"members"`
	OwnerCount           int                     `json:"owner_count"`
	PlacementCount       int                     `json:"placement_count"`
	RegularOwnerCount    int                     `json:"regular_owner_count"`
	SymlinkOwnerCount    int                     `json:"symlink_owner_count"`
	GitlinkOwnerCount    int                     `json:"gitlink_owner_count"`
	RegularDeclaredBytes int64                   `json:"regular_declared_bytes"`
	EncodedMemberBytes   int64                   `json:"encoded_member_bytes"`
	Digest               string                  `json:"digest"`
}

type PhysicalMember struct {
	Ordinal        int    `json:"ordinal"`
	Count          int    `json:"count"`
	Name           string `json:"name"`
	ContentDigest  string `json:"content_digest"`
	ContentBytes   int64  `json:"content_bytes"`
	MetadataDigest string `json:"metadata_digest"`
}

type PhysicalRoot struct {
	Schema         string           `json:"schema"`
	ManifestName   string           `json:"manifest_name"`
	ManifestDigest string           `json:"manifest_digest"`
	Members        []PhysicalMember `json:"members"`
}

// SearchManifest binds the source generation to the selected direct topology
// and the complete existing whole-shard publication root. It is side-by-side
// authority until T34.3 owns the v1/v2 reader cutover.
type SearchManifest struct {
	Schema                 string                  `json:"schema"`
	Repository             string                  `json:"repository"`
	Revisions              []store.IndexedRevision `json:"revisions"`
	SourceManifestName     string                  `json:"source_manifest_name"`
	SourceGenerationDigest string                  `json:"source_generation_digest"`
	TopologyPolicy         string                  `json:"topology_policy"`
	PhysicalRoot           PhysicalRoot            `json:"physical_root"`
	Digest                 string                  `json:"digest"`
}

func SourceManifestName(repository string) string {
	return "phebs-source-" + repositoryKey(repository) + ".manifest.json"
}

func SearchManifestName(repository string) string {
	return "phebs-search-" + repositoryKey(repository) + ".manifest.json"
}

func SourceMemberPrefix(repository string) string {
	return "phebs-source-" + repositoryKey(repository) + "-"
}

func sourceMemberName(
	repository string,
	revisions []store.IndexedRevision,
	ordinal int,
) (string, error) {
	revisionSet, err := revisionSetDigest(repository, revisions)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s%s.%05d.jsonl", SourceMemberPrefix(repository),
		strings.TrimPrefix(revisionSet, "sha256:")[:16], ordinal,
	), nil
}

func IsArtifactName(repository, name string) bool {
	if filepath.Base(name) != name {
		return false
	}
	return name == SourceManifestName(repository) ||
		name == SearchManifestName(repository) ||
		strings.HasPrefix(name, SourceMemberPrefix(repository)) &&
			strings.HasSuffix(name, ".jsonl")
}

func SourceManifestDigest(manifest SourceManifest) (string, error) {
	manifest.Digest = ""
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digest("phebs-repository-source-generation-v1\x00", raw), nil
}

func SearchManifestDigest(manifest SearchManifest) (string, error) {
	manifest.Digest = ""
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digest("phebs-repository-search-generation-v1\x00", raw), nil
}

func ValidateSourceManifest(manifest SourceManifest) error {
	if manifest.Schema != SourceManifestSchema ||
		manifest.CensusPolicy != CensusPolicy {
		return invalidf("source schema or census policy mismatch")
	}
	if err := reponame.Validate(manifest.Repository); err != nil {
		return invalidf("source repository is not canonical")
	}
	if err := validateRevisions(manifest.Revisions); err != nil {
		return err
	}
	if len(manifest.RevisionMembers) != len(manifest.Revisions) ||
		manifest.Members == nil || len(manifest.Members) > MaxSourceMembers ||
		manifest.OwnerCount < 0 || manifest.OwnerCount > MaxOwners ||
		manifest.PlacementCount < manifest.OwnerCount ||
		manifest.PlacementCount > MaxPlacements ||
		manifest.RegularOwnerCount+manifest.SymlinkOwnerCount+
			manifest.GitlinkOwnerCount != manifest.OwnerCount ||
		manifest.RegularDeclaredBytes < 0 ||
		manifest.EncodedMemberBytes < 0 ||
		manifest.EncodedMemberBytes > MaxGenerationBytes ||
		!validDigest(manifest.Digest) {
		return invalidf("source manifest counts or digest are invalid")
	}
	if (manifest.OwnerCount == 0) != (len(manifest.Members) == 0) ||
		(manifest.OwnerCount == 0) != (manifest.EncodedMemberBytes == 0) {
		return invalidf("empty source manifest shape is inconsistent")
	}
	placements := 0
	for ordinal, member := range manifest.RevisionMembers {
		revision := manifest.Revisions[ordinal]
		if member.Ordinal != ordinal || member.Selector != revision.Selector ||
			member.Branch != revision.Branch || member.Commit != revision.Commit ||
			member.PlacementCount < 0 ||
			member.RegularCount+member.SymlinkCount+member.GitlinkCount !=
				member.PlacementCount || !validDigest(member.Digest) {
			return invalidf("source revision member %d is invalid", ordinal)
		}
		placements += member.PlacementCount
	}
	if placements != manifest.PlacementCount {
		return invalidf("source revision placement total is inconsistent")
	}
	records := 0
	var encoded int64
	previousName, previousLast := "", ""
	for ordinal, member := range manifest.Members {
		expectedName, err := sourceMemberName(
			manifest.Repository, manifest.Revisions, ordinal,
		)
		if err != nil {
			return err
		}
		if member.Ordinal != ordinal || member.Count != len(manifest.Members) ||
			member.Name != expectedName || filepath.Base(member.Name) != member.Name ||
			member.RecordCount < 1 || member.RecordCount > MaxRecordsPerMember ||
			member.ContentBytes < 1 || member.ContentBytes > MaxMemberBytes ||
			!validDigest(member.Digest) || member.FirstKey == "" ||
			member.LastKey < member.FirstKey ||
			(ordinal > 0 && (member.Name <= previousName ||
				member.FirstKey <= previousLast)) {
			return invalidf("source member %d is invalid", ordinal)
		}
		records += member.RecordCount
		encoded += member.ContentBytes
		previousName, previousLast = member.Name, member.LastKey
	}
	if records != manifest.OwnerCount || encoded != manifest.EncodedMemberBytes {
		return invalidf("source member totals are inconsistent")
	}
	want, err := SourceManifestDigest(manifest)
	if err != nil || want != manifest.Digest {
		return invalidf("source manifest digest mismatch")
	}
	return nil
}

func ValidateSearchManifest(manifest SearchManifest) error {
	if manifest.Schema != SearchManifestSchema ||
		manifest.TopologyPolicy != DirectTopologyPolicy ||
		manifest.SourceManifestName != SourceManifestName(manifest.Repository) ||
		!validDigest(manifest.SourceGenerationDigest) ||
		manifest.PhysicalRoot.Schema == "" ||
		filepath.Base(manifest.PhysicalRoot.ManifestName) !=
			manifest.PhysicalRoot.ManifestName ||
		!validDigest(manifest.PhysicalRoot.ManifestDigest) ||
		len(manifest.PhysicalRoot.Members) == 0 ||
		!validDigest(manifest.Digest) {
		return invalidf("search manifest identity is invalid")
	}
	if err := reponame.Validate(manifest.Repository); err != nil {
		return invalidf("search repository is not canonical")
	}
	if err := validateRevisions(manifest.Revisions); err != nil {
		return err
	}
	previous := ""
	for ordinal, member := range manifest.PhysicalRoot.Members {
		if member.Ordinal != ordinal || member.Count != len(manifest.PhysicalRoot.Members) ||
			filepath.Base(member.Name) != member.Name || member.Name <= previous ||
			member.ContentBytes < 1 || !validDigest(member.ContentDigest) ||
			!validDigest(member.MetadataDigest) {
			return invalidf("physical member %d is invalid", ordinal)
		}
		previous = member.Name
	}
	want, err := SearchManifestDigest(manifest)
	if err != nil || want != manifest.Digest {
		return invalidf("search manifest digest mismatch")
	}
	return nil
}

func ReadSourceManifest(directory, repository string) (SourceManifest, error) {
	return ReadSourceManifestContext(context.Background(), directory, repository)
}

// ReadSourceManifestContext is the exact-read form of ReadSourceManifest.
// The manifest is one bounded control read; source-member payloads are counted
// separately by their streaming reader.
func ReadSourceManifestContext(
	ctx context.Context,
	directory, repository string,
) (SourceManifest, error) {
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return SourceManifest{}, err
	}
	var manifest SourceManifest
	if err := readControl(filepath.Join(directory, SourceManifestName(repository)), &manifest); err != nil {
		return SourceManifest{}, err
	}
	if err := ValidateSourceManifest(manifest); err != nil {
		return SourceManifest{}, err
	}
	if manifest.Repository != repository {
		return SourceManifest{}, invalidf("source repository mismatch")
	}
	return manifest, nil
}

func ReadSearchManifest(directory, repository string) (SearchManifest, error) {
	return ReadSearchManifestContext(context.Background(), directory, repository)
}

// ReadSearchManifestContext is the exact-read form of ReadSearchManifest.
func ReadSearchManifestContext(
	ctx context.Context,
	directory, repository string,
) (SearchManifest, error) {
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return SearchManifest{}, err
	}
	var manifest SearchManifest
	if err := readControl(filepath.Join(directory, SearchManifestName(repository)), &manifest); err != nil {
		return SearchManifest{}, err
	}
	if err := ValidateSearchManifest(manifest); err != nil {
		return SearchManifest{}, err
	}
	if manifest.Repository != repository {
		return SearchManifest{}, invalidf("search repository mismatch")
	}
	return manifest, nil
}

// ValidateSearchControls proves the closed source/search control roots and
// their physical whole-shard binding without rereading source-member content.
// A reader may use this only after its generation cache performed the complete
// ValidatePublished pass and retained file-identity fences for every member.
func ValidateSearchControls(
	search SearchManifest,
	source SourceManifest,
	revisions []store.IndexedRevision,
	physical PhysicalRoot,
) error {
	if err := ValidateSearchManifest(search); err != nil {
		return err
	}
	if err := ValidateSourceManifest(source); err != nil {
		return err
	}
	if search.Repository != source.Repository ||
		!slices.Equal(search.Revisions, revisions) ||
		!slices.Equal(source.Revisions, revisions) ||
		search.SourceGenerationDigest != source.Digest ||
		!equalPhysicalRoot(search.PhysicalRoot, physical) {
		return invalidf("search generation control binding mismatch")
	}
	return nil
}

// ValidatePublished proves every source member byte and the closed search
// root. The physical shard root is supplied by focusedindex, which owns the
// existing whole-shard format and avoids a package cycle.
func ValidatePublished(
	ctx context.Context,
	directory, repository string,
	revisions []store.IndexedRevision,
	physical PhysicalRoot,
) (SearchManifest, error) {
	search, err := ReadSearchManifestContext(ctx, directory, repository)
	if err != nil {
		return SearchManifest{}, err
	}
	source, err := ReadSourceManifestContext(ctx, directory, repository)
	if err != nil {
		return SearchManifest{}, err
	}
	if err := ValidateSearchControls(search, source, revisions, physical); err != nil {
		return SearchManifest{}, err
	}
	if err := validateSourceMembers(ctx, directory, source, nil); err != nil {
		return SearchManifest{}, err
	}
	return search, nil
}

func equalPhysicalRoot(left, right PhysicalRoot) bool {
	return left.Schema == right.Schema &&
		left.ManifestName == right.ManifestName &&
		left.ManifestDigest == right.ManifestDigest &&
		slices.Equal(left.Members, right.Members)
}

func writeControl(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxControlBytes {
		return invalidf("control file exceeds %d bytes", maxControlBytes)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readControl(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 ||
		info.Size() > maxControlBytes {
		return invalidf("control file is missing, special, or oversized")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return invalidf("control file changed while opening")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maxControlBytes+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if readErr != nil || statErr != nil || closeErr != nil || currentErr != nil {
		return errors.Join(readErr, statErr, closeErr, currentErr)
	}
	if !os.SameFile(opened, after) || !os.SameFile(after, current) ||
		!current.Mode().IsRegular() || after.Size() != info.Size() ||
		!after.ModTime().Equal(info.ModTime()) || current.Size() != after.Size() ||
		!current.ModTime().Equal(after.ModTime()) {
		return invalidf("control file changed while reading")
	}
	if int64(len(raw)) != info.Size() || len(raw) > maxControlBytes {
		return invalidf("control file changed or exceeded its bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return invalidf("decode control file: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalidf("control file contains trailing JSON")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return invalidf("encode control file: %v", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return invalidf("control file is not canonical")
	}
	return nil
}

func validateRevisions(revisions []store.IndexedRevision) error {
	if len(revisions) < 1 || len(revisions) > 8 {
		return invalidf("revision count must be between one and eight")
	}
	selectors, branches := map[string]bool{}, map[string]bool{}
	for ordinal, revision := range revisions {
		if !validText(revision.Selector) || !validText(revision.Branch) ||
			!gitobj.IsObjectID(revision.Commit) || selectors[revision.Selector] ||
			branches[revision.Branch] {
			return invalidf("revision %d is invalid or duplicated", ordinal)
		}
		if ordinal == 0 && (revision.Selector != "HEAD" || revision.Branch != "HEAD") {
			return invalidf("first revision must be HEAD")
		}
		selectors[revision.Selector], branches[revision.Branch] = true, true
	}
	return nil
}

func validText(value string) bool {
	if strings.TrimSpace(value) != value || value == "" ||
		!utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if current < 0x20 || current == 0x7f {
			return false
		}
	}
	return true
}

func recordKey(record SourceRecord) string {
	return record.Path + "\x00" + record.Mode + "\x00" + record.Kind + "\x00" +
		record.ObjectID + "\x00" + strconv.FormatInt(record.DeclaredBytes, 10)
}

func repositoryKey(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return hex.EncodeToString(sum[:])
}

func digest(domain string, raw []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(raw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func invalidf(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGeneration, fmt.Sprintf(format, values...))
}
