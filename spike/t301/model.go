// Package t301 contains the generated neutral focused-index spike for T30.1.
//
// It is retained engineering evidence, not production indexing code.
package t301

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion           = "t301-focused-index-spike-v1"
	ScopeVersion            = "analysis-unit-v1"
	BuilderPolicyVersion    = "t301-focused-builder-v1"
	ManifestVersion         = "t301-shard-set-v1"
	CorpusVersion           = "t301-neutral-monorepo-v1"
	ZoektModuleVersion      = "v0.0.0-20260709064101-33f1f18af292"
	CurrentCorpusFileLimit  = 200_000
	CurrentInventoryByteCap = 16 << 20
	DefaultIrrelevantFiles  = 200_001
)

var (
	ErrInvalidScope = errors.New("invalid analysis-unit scope")
	ErrMissingScope = errors.New("selected scope path is missing")
	ErrSpecialEntry = errors.New("selected scope contains a non-regular entry")
	ErrShardSet     = errors.New("invalid focused shard set")
)

// Scope is the stable semantic analysis-unit identity.
type Scope struct {
	Repository string   `json:"repository"`
	Name       string   `json:"name"`
	Primary    []string `json:"primary"`
	Supporting []string `json:"supporting"`
}

// Revision is one ordered search lane. Evidence remains HEAD-bound outside
// this spike.
type Revision struct {
	Selector string `json:"selector"`
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
}

// Corpus identifies the deterministic generated repository and its adversarial
// commits. Expanded bulk files are never retained in the source tree.
type Corpus struct {
	Schema             string     `json:"schema"`
	Repository         string     `json:"repository"`
	Head               string     `json:"head"`
	Branch             string     `json:"branch"`
	Tag                string     `json:"tag"`
	MissingScopeCommit string     `json:"missing_scope_commit"`
	SymlinkCommit      string     `json:"symlink_commit"`
	GitlinkCommit      string     `json:"gitlink_commit"`
	Revisions          []Revision `json:"revisions"`
	RegularFiles       int        `json:"regular_files"`
	InventoryPathBytes int64      `json:"inventory_path_bytes"`
	GitObjectBytes     int64      `json:"git_object_bytes"`
	Digest             string     `json:"digest"`
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

type ShardManifest struct {
	Schema           string        `json:"schema"`
	Repository       string        `json:"repository"`
	CorpusDigest     string        `json:"corpus_digest"`
	UnitDigest       string        `json:"unit_digest"`
	GenerationDigest string        `json:"generation_digest"`
	BuilderPolicy    string        `json:"builder_policy"`
	Members          []ShardMember `json:"members"`
	Digest           string        `json:"digest"`
}

type BuildResult struct {
	Schema              string   `json:"schema"`
	Repository          string   `json:"repository"`
	CorpusDigest        string   `json:"corpus_digest"`
	UnitDigest          string   `json:"unit_digest"`
	GenerationDigest    string   `json:"generation_digest"`
	ManifestDigest      string   `json:"manifest_digest"`
	OpenedPaths         []string `json:"opened_paths"`
	OpenedBlobBytes     int64    `json:"opened_blob_bytes"`
	AdmittedDocuments   int      `json:"admitted_documents"`
	AdmittedSourceBytes int64    `json:"admitted_source_bytes"`
	ShardCount          int      `json:"shard_count"`
	ShardBytes          int64    `json:"shard_bytes"`
	BuildWallNanos      int64    `json:"build_wall_nanos"`
}

type Results struct {
	Schema                 string      `json:"schema"`
	Decision               string      `json:"decision"`
	Reason                 string      `json:"reason"`
	GoVersion              string      `json:"go_version"`
	GOOS                   string      `json:"goos"`
	GOARCH                 string      `json:"goarch"`
	ZoektModule            string      `json:"zoekt_module"`
	Corpus                 Corpus      `json:"corpus"`
	Scope                  Scope       `json:"scope"`
	Run1                   BuildResult `json:"run1"`
	Run2                   BuildResult `json:"run2"`
	EquivalentSearch       bool        `json:"equivalent_search"`
	MissingMemberRefused   bool        `json:"missing_member_refused"`
	ExtraMemberRefused     bool        `json:"extra_member_refused"`
	MixedMemberRefused     bool        `json:"mixed_member_refused"`
	StaleMemberRefused     bool        `json:"stale_member_refused"`
	TrailingJSONRefused    bool        `json:"trailing_json_refused"`
	InterruptedRefused     bool        `json:"interrupted_refused"`
	MissingRevisionRefused bool        `json:"missing_revision_refused"`
	SymlinkRefused         bool        `json:"symlink_refused"`
	GitlinkRefused         bool        `json:"gitlink_refused"`
	ReplacementIgnored     bool        `json:"replacement_ignored"`
	LazyFetchRefused       bool        `json:"lazy_fetch_refused"`
	OutOfUnitBlobReads     int         `json:"out_of_unit_blob_reads"`
	GenerationWallNanos    int64       `json:"generation_wall_nanos"`
	PeakRSSBytes           int64       `json:"peak_rss_bytes"`
	SearchWallNanos        int64       `json:"search_wall_nanos"`
}

func (scope Scope) Canonical() ([]byte, error) {
	if !validIdentityText(scope.Repository) || !validIdentityText(scope.Name) {
		return nil, fmt.Errorf("%w: repository and unit name are required", ErrInvalidScope)
	}
	primary, err := canonicalPaths(scope.Primary, true)
	if err != nil {
		return nil, fmt.Errorf("primary paths: %w", err)
	}
	supporting, err := canonicalPaths(scope.Supporting, false)
	if err != nil {
		return nil, fmt.Errorf("supporting paths: %w", err)
	}
	all := append(append([]string(nil), primary...), supporting...)
	slices.Sort(all)
	seen := make(map[string]struct{}, len(all))
	for _, current := range all {
		if _, duplicate := seen[current]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate path %q",
				ErrInvalidScope, current,
			)
		}
		seen[current] = struct{}{}
	}
	for _, current := range all {
		for parent := path.Dir(current); parent != "."; parent = path.Dir(parent) {
			if _, overlap := seen[parent]; overlap {
				return nil, fmt.Errorf(
					"%w: overlapping paths %q and %q",
					ErrInvalidScope, parent, current,
				)
			}
		}
	}
	canonical := struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Name       string   `json:"name"`
		Primary    []string `json:"primary"`
		Supporting []string `json:"supporting"`
	}{
		Schema: ScopeVersion, Repository: scope.Repository, Name: scope.Name,
		Primary: primary, Supporting: supporting,
	}
	return json.Marshal(canonical)
}

func (scope Scope) Digest() (string, error) {
	canonical, err := scope.Canonical()
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-analysis-unit-v1\x00", canonical), nil
}

func GenerationDigest(scope Scope, revisions []Revision) (string, error) {
	unitDigest, err := scope.Digest()
	if err != nil {
		return "", err
	}
	copyRevisions := append([]Revision(nil), revisions...)
	if err := validateRevisions(copyRevisions); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		Schema        string     `json:"schema"`
		Repository    string     `json:"repository"`
		UnitDigest    string     `json:"unit_digest"`
		BuilderPolicy string     `json:"builder_policy"`
		Revisions     []Revision `json:"revisions"`
	}{
		Schema: "t301-index-generation-v1", Repository: scope.Repository,
		UnitDigest: unitDigest, BuilderPolicy: BuilderPolicyVersion,
		Revisions: copyRevisions,
	})
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-index-generation-v1\x00", canonical), nil
}

func CorpusDigest(corpus Corpus) (string, error) {
	if corpus.Schema != SchemaVersion ||
		!validIdentityText(corpus.Repository) ||
		!validObjectID(corpus.Head) ||
		!validObjectID(corpus.Branch) ||
		!validObjectID(corpus.Tag) ||
		!validObjectID(corpus.MissingScopeCommit) ||
		!validObjectID(corpus.SymlinkCommit) ||
		!validObjectID(corpus.GitlinkCommit) ||
		corpus.RegularFiles < 1 ||
		corpus.InventoryPathBytes < 1 {
		return "", errors.New("invalid generated corpus identity")
	}
	if err := validateRevisions(corpus.Revisions); err != nil {
		return "", fmt.Errorf("invalid generated corpus revisions: %w", err)
	}
	if len(corpus.Revisions) != 3 ||
		corpus.Revisions[0].Commit != corpus.Head ||
		corpus.Revisions[1].Commit != corpus.Branch ||
		corpus.Revisions[2].Commit != corpus.Tag {
		return "", errors.New("generated corpus revision identity mismatch")
	}
	canonical, err := json.Marshal(struct {
		Schema             string     `json:"schema"`
		Repository         string     `json:"repository"`
		Head               string     `json:"head"`
		Branch             string     `json:"branch"`
		Tag                string     `json:"tag"`
		MissingScopeCommit string     `json:"missing_scope_commit"`
		SymlinkCommit      string     `json:"symlink_commit"`
		GitlinkCommit      string     `json:"gitlink_commit"`
		Revisions          []Revision `json:"revisions"`
		RegularFiles       int        `json:"regular_files"`
		InventoryPathBytes int64      `json:"inventory_path_bytes"`
	}{
		Schema: CorpusVersion, Repository: corpus.Repository,
		Head: corpus.Head, Branch: corpus.Branch, Tag: corpus.Tag,
		MissingScopeCommit: corpus.MissingScopeCommit,
		SymlinkCommit:      corpus.SymlinkCommit,
		GitlinkCommit:      corpus.GitlinkCommit,
		Revisions:          corpus.Revisions,
		RegularFiles:       corpus.RegularFiles,
		InventoryPathBytes: corpus.InventoryPathBytes,
	})
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-t301-corpus-v1\x00", canonical), nil
}

func ManifestDigest(manifest ShardManifest) (string, error) {
	manifest.Digest = ""
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-shard-set-v1\x00", canonical), nil
}

func canonicalPaths(input []string, required bool) ([]string, error) {
	if required && len(input) == 0 {
		return nil, fmt.Errorf("%w: at least one path is required", ErrInvalidScope)
	}
	result := append([]string(nil), input...)
	slices.Sort(result)
	for _, value := range result {
		if value == "" || value == "." || path.IsAbs(value) ||
			path.Clean(value) != value || strings.Contains(value, `\`) ||
			!utf8.ValidString(value) {
			return nil, fmt.Errorf("%w: unsafe path %q", ErrInvalidScope, value)
		}
		for _, char := range value {
			if char < 0x20 || char == 0x7f {
				return nil, fmt.Errorf("%w: unsafe path %q", ErrInvalidScope, value)
			}
		}
	}
	return result, nil
}

func validIdentityText(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func validateRevisions(revisions []Revision) error {
	if len(revisions) == 0 {
		return errors.New("index generation requires at least HEAD")
	}
	selectors := make(map[string]struct{}, len(revisions))
	branches := make(map[string]struct{}, len(revisions))
	for index, revision := range revisions {
		if !validIdentityText(revision.Selector) ||
			!validIdentityText(revision.Branch) ||
			!validObjectID(revision.Commit) {
			return fmt.Errorf("invalid revision at index %d", index)
		}
		if index == 0 && revision.Selector != "HEAD" {
			return errors.New("first revision must be HEAD")
		}
		if _, duplicate := selectors[revision.Selector]; duplicate {
			return fmt.Errorf("duplicate revision selector %q", revision.Selector)
		}
		if _, duplicate := branches[revision.Branch]; duplicate {
			return fmt.Errorf("duplicate shard branch %q", revision.Branch)
		}
		selectors[revision.Selector] = struct{}{}
		branches[revision.Branch] = struct{}{}
	}
	return nil
}

func digestBytes(domain string, data []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(data)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
