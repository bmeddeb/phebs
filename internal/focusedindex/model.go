// Package focusedindex builds and validates zoekt shard sets whose Git input
// is restricted to one configured analysis unit.
package focusedindex

import (
	"bytes"
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
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	RequestSchema  = "phebs-focused-index-request-v1"
	ResultSchema   = "phebs-focused-index-result-v1"
	ManifestSchema = "phebs-focused-shard-set-v1"
	BuilderPolicy  = "phebs-focused-builder-v2"

	MemberSuffix = ".phebs-member.json"

	maxControlBytes = 1 << 20
)

var (
	ErrMissingScope = errors.New("selected analysis-unit path is missing")
	ErrSpecialEntry = errors.New("selected analysis-unit path contains a non-regular Git entry")
	ErrShardSet     = errors.New("invalid focused shard set")
)

// Request is the strict parent-to-child build contract. OutputDir must be an
// unpublished staging directory.
type Request struct {
	Schema    string                  `json:"schema"`
	RepoDir   string                  `json:"repo_dir"`
	OutputDir string                  `json:"output_dir"`
	Scope     analysisunit.Scope      `json:"scope"`
	Revisions []store.IndexedRevision `json:"revisions"`
}

// Result is written only after the complete staged shard set and its manifest
// are durable.
type Result struct {
	Schema              string `json:"schema"`
	Repository          string `json:"repository"`
	UnitDigest          string `json:"unit_digest"`
	GenerationDigest    string `json:"generation_digest"`
	ManifestDigest      string `json:"manifest_digest"`
	OpenedBlobCount     int    `json:"opened_blob_count"`
	OpenedBlobBytes     int64  `json:"opened_blob_bytes"`
	OutOfUnitBlobReads  int    `json:"out_of_unit_blob_reads"`
	AdmittedDocuments   int    `json:"admitted_documents"`
	AdmittedSourceBytes int64  `json:"admitted_source_bytes"`
	ShardCount          int    `json:"shard_count"`
	ShardBytes          int64  `json:"shard_bytes"`
	BuildWallNanos      int64  `json:"build_wall_nanos"`
}

type ShardMember struct {
	Ordinal          int    `json:"ordinal"`
	Count            int    `json:"count"`
	Name             string `json:"name"`
	ContentDigest    string `json:"content_digest"`
	MetadataDigest   string `json:"metadata_digest"`
	UnitDigest       string `json:"unit_digest"`
	GenerationDigest string `json:"generation_digest"`
}

// Manifest is the search visibility authority for one configured repository.
// Digest is a publication identity: builder timestamps may change shard bytes
// across semantically equivalent rebuilds.
type Manifest struct {
	Schema           string                  `json:"schema"`
	Repository       string                  `json:"repository"`
	Scope            analysisunit.Scope      `json:"scope"`
	UnitDigest       string                  `json:"unit_digest"`
	GenerationDigest string                  `json:"generation_digest"`
	BuilderPolicy    string                  `json:"builder_policy"`
	Revisions        []store.IndexedRevision `json:"revisions"`
	Members          []ShardMember           `json:"members"`
	Digest           string                  `json:"digest"`
}

func GenerationDigest(scope analysisunit.Scope, revisions []store.IndexedRevision) (string, error) {
	unitDigest, err := scope.Digest()
	if err != nil {
		return "", err
	}
	if err := ValidateRevisions(revisions); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		Schema        string                  `json:"schema"`
		Repository    string                  `json:"repository"`
		UnitDigest    string                  `json:"unit_digest"`
		BuilderPolicy string                  `json:"builder_policy"`
		Revisions     []store.IndexedRevision `json:"revisions"`
	}{
		Schema:        "phebs-index-generation-v1",
		Repository:    scope.Repository,
		UnitDigest:    unitDigest,
		BuilderPolicy: BuilderPolicy,
		Revisions:     revisions,
	})
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-index-generation-v1\x00", canonical), nil
}

func ManifestDigest(manifest Manifest) (string, error) {
	manifest.Digest = ""
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-focused-shard-set-v1\x00", canonical), nil
}

func ValidateRevisions(revisions []store.IndexedRevision) error {
	if len(revisions) == 0 {
		return errors.New("focused index generation requires at least HEAD")
	}
	selectors := make(map[string]bool, len(revisions))
	branches := make(map[string]bool, len(revisions))
	for index, revision := range revisions {
		if !validText(revision.Selector) || !validText(revision.Branch) ||
			!gitobj.IsObjectID(revision.Commit) {
			return fmt.Errorf("invalid focused revision at index %d", index)
		}
		if index == 0 && (revision.Selector != "HEAD" || revision.Branch != "HEAD") {
			return errors.New("first focused revision must be HEAD on the HEAD shard branch")
		}
		if selectors[revision.Selector] {
			return fmt.Errorf("duplicate focused revision selector %q", revision.Selector)
		}
		if branches[revision.Branch] {
			return fmt.Errorf("duplicate focused shard branch %q", revision.Branch)
		}
		selectors[revision.Selector] = true
		branches[revision.Branch] = true
	}
	return nil
}

func ManifestName(repository string) string {
	return "phebs-focus-" + repositoryKey(repository) + ".manifest.json"
}

func PublishingName(repository string) string {
	return "phebs-focus-" + repositoryKey(repository) + ".publishing"
}

func ShardPrefix(repository, generationDigest string) string {
	generation := strings.TrimPrefix(generationDigest, "sha256:")
	if len(generation) > 16 {
		generation = generation[:16]
	}
	return "phebs-focus-" + repositoryKey(repository) + "-" + generation
}

func RepositoryPrefix(repository string) string {
	return "phebs-focus-" + repositoryKey(repository) + "-"
}

func ArtifactBase(repository string) string {
	return "phebs-focus-" + repositoryKey(repository)
}

// IsManagedShardName reports whether name belongs to the cryptographic
// repository namespace used by focused or whole-repository publications.
// Decoded shard metadata must never override this ownership boundary.
func IsManagedShardName(name string) bool {
	return managedHashShardName(name, "phebs-focus-", '-') ||
		managedHashShardName(name, "phebs-whole-", '_')
}

func managedHashShardName(name, prefix string, separator byte) bool {
	if filepath.Base(name) != name ||
		!strings.HasPrefix(name, prefix) ||
		!strings.HasSuffix(name, ".zoekt") {
		return false
	}
	identity := strings.TrimPrefix(name, prefix)
	if len(identity) <= sha256.Size*2 ||
		identity[sha256.Size*2] != separator {
		return false
	}
	_, err := hex.DecodeString(identity[:sha256.Size*2])
	return err == nil
}

func repositoryKey(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return hex.EncodeToString(sum[:])
}

func WriteControlFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxControlBytes {
		return fmt.Errorf("focused control file exceeds its %d-byte limit", maxControlBytes)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func ReadRequest(path string) (Request, error) {
	var request Request
	if err := readControlFile(path, &request); err != nil {
		return Request{}, err
	}
	if request.Schema != RequestSchema || request.RepoDir == "" || request.OutputDir == "" {
		return Request{}, errors.New("focused build request envelope is invalid")
	}
	if !filepath.IsAbs(request.RepoDir) || !filepath.IsAbs(request.OutputDir) {
		return Request{}, errors.New("focused build paths must be absolute")
	}
	if _, err := request.Scope.Canonical(); err != nil {
		return Request{}, err
	}
	if err := ValidateRevisions(request.Revisions); err != nil {
		return Request{}, err
	}
	return request, nil
}

func ReadResult(path string) (Result, error) {
	var result Result
	if err := readControlFile(path, &result); err != nil {
		return Result{}, err
	}
	if result.Schema != ResultSchema || result.Repository == "" ||
		!validDigest(result.UnitDigest) || !validDigest(result.GenerationDigest) ||
		!validDigest(result.ManifestDigest) || result.OpenedBlobCount < 1 ||
		result.OpenedBlobBytes < 0 || result.OutOfUnitBlobReads != 0 ||
		result.AdmittedDocuments < 1 ||
		result.OpenedBlobCount != result.AdmittedDocuments ||
		result.AdmittedSourceBytes < 0 ||
		result.OpenedBlobBytes != result.AdmittedSourceBytes ||
		result.ShardCount < 1 || result.ShardBytes < 1 || result.BuildWallNanos < 0 {
		return Result{}, errors.New("focused build result envelope is invalid")
	}
	return result, nil
}

func readControlFile(path string, destination any) error {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() ||
		before.Size() < 0 || before.Size() > maxControlBytes {
		return errors.New("focused control file is missing, special, or exceeds its limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !sameControlFileIdentity(before, opened) {
		_ = file.Close()
		return errors.New("focused control file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxControlBytes+1))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if err != nil || afterErr != nil || closeErr != nil || currentErr != nil {
		return errors.Join(err, afterErr, closeErr, currentErr)
	}
	if len(raw) > maxControlBytes {
		return errors.New("focused control file exceeds its limit")
	}
	if int64(len(raw)) != before.Size() {
		return errors.New("focused control file changed while reading")
	}
	if !sameControlFileIdentity(before, after) ||
		!sameControlFileIdentity(after, current) {
		return errors.New("focused control file changed while reading")
	}
	return decodeJSONStrict(raw, destination)
}

func sameControlFileIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil &&
		left.Mode().IsRegular() && right.Mode().IsRegular() &&
		left.Mode() == right.Mode() &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime()) &&
		os.SameFile(left, right)
}

func decodeJSONStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func validText(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func digestBytes(domain string, data []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(data)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func cloneRevisions(input []store.IndexedRevision) []store.IndexedRevision {
	return slices.Clone(input)
}
