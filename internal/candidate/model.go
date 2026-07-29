// Package candidate builds and validates the commit-bound candidate census
// shared by focused extraction and repository-overlay caller planning.
package candidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	ManifestSchema = "phebs-candidate-manifest-v1"
	RecordSchema   = "phebs-candidate-record-v1"
	StateSchema    = "phebs-candidate-state-v1"

	EnumerationPolicyVersion          = "phebs-candidate-enumeration-v1"
	CallerHashPolicy                  = "phebs-caller-path-v1"
	InitialCallerPrefixBits           = 2
	MaxPolicies                       = 64
	MaxCorpusEntries                  = 10_000_000
	MaxRecordsPerArtifact             = 4096
	MaxDeclaredBytesPerArtifact int64 = 64 << 20

	maxManifestBytes = 8 << 20
	maxArtifactBytes = 128 << 20
)

var (
	ErrInvalidPolicy     = errors.New("invalid candidate policy")
	ErrInvalidManifest   = errors.New("invalid candidate manifest")
	ErrCorpusTooLarge    = errors.New("candidate corpus exceeds entry limit")
	ErrCandidateTooLarge = errors.New("candidate exceeds declared-byte limit")
	ErrHashCollision     = errors.New("caller candidates cannot split at 256 bits")
	ErrSelectedPath      = errors.New("selected analysis-unit path is missing or special")
	ErrPublishing        = errors.New("candidate publication is incomplete")
)

// Plane identifies how a domain consumes candidates. Local and repository
// policies share one bounded artifact plane; caller policies use hash leaves.
type Plane string

const (
	PlaneLocal      Plane = "local"
	PlaneRepository Plane = "repository"
	PlaneCaller     Plane = "caller"
)

// Policy supplies pure path predicates for one versioned evidence domain.
// EnumerationPolicy names the predicate generation and is part of identity.
type Policy struct {
	Domain            string
	Version           string
	EnumerationPolicy string
	SymlinkPolicy     string
	Plane             Plane
	Enumerate         func(string) bool
	Required          func(string) bool
	RejectSymlink     func(string) bool
}

// PolicyIdentity is the serializable, content-addressed policy contract.
type PolicyIdentity struct {
	Domain            string `json:"domain"`
	Version           string `json:"version"`
	EnumerationPolicy string `json:"enumeration_policy"`
	SymlinkPolicy     string `json:"symlink_policy"`
	Plane             Plane  `json:"plane"`
}

// PartitionPolicy freezes every caller assignment and artifact bound.
type PartitionPolicy struct {
	EnumerationPolicy string `json:"enumeration_policy"`
	CallerHashPolicy  string `json:"caller_hash_policy"`
	InitialPrefixBits int    `json:"initial_prefix_bits"`
	MaxRecords        int    `json:"max_records"`
	MaxDeclaredBytes  int64  `json:"max_declared_bytes"`
	RecordOrdering    string `json:"record_ordering"`
	SplitRule         string `json:"split_rule"`
}

func frozenPartitionPolicy() PartitionPolicy {
	return PartitionPolicy{
		EnumerationPolicy: EnumerationPolicyVersion,
		CallerHashPolicy:  CallerHashPolicy,
		InitialPrefixBits: InitialCallerPrefixBits,
		MaxRecords:        MaxRecordsPerArtifact,
		MaxDeclaredBytes:  MaxDeclaredBytesPerArtifact,
		RecordOrdering:    "hash-path-oid-v1",
		SplitRule:         "next-hash-bit-v1",
	}
}

// Record is one regular Git blob selected by at least one policy. Required is
// a domain-view projection and is not persisted.
type Record struct {
	Schema          string   `json:"schema"`
	Path            string   `json:"path"`
	OID             string   `json:"oid"`
	DeclaredBytes   int64    `json:"declared_bytes"`
	Domains         []string `json:"domains"`
	RequiredDomains []string `json:"required_domains"`
	InUnit          bool     `json:"in_unit"`
	Shared          bool     `json:"shared"`
	Hash            string   `json:"hash,omitempty"`
	Required        bool     `json:"-"`
}

type CorpusSummary struct {
	RegularCount         int    `json:"regular_count"`
	RegularDeclaredBytes int64  `json:"regular_declared_bytes"`
	RegularDigest        string `json:"regular_digest"`
	GitlinkCount         int    `json:"gitlink_count"`
	GitlinkDeclaredBytes int64  `json:"gitlink_declared_bytes"`
	GitlinkDigest        string `json:"gitlink_digest"`
	SymlinkCount         int    `json:"symlink_count"`
	SymlinkDeclaredBytes int64  `json:"symlink_declared_bytes"`
	SymlinkDigest        string `json:"symlink_digest"`
}

type DomainSummary struct {
	Domain                   string `json:"domain"`
	Version                  string `json:"version"`
	EnumerationPolicy        string `json:"enumeration_policy"`
	SymlinkPolicy            string `json:"symlink_policy"`
	Plane                    Plane  `json:"plane"`
	RepositoryCandidateCount int    `json:"repository_candidate_count"`
	RepositoryRequiredCount  int    `json:"repository_required_count"`
	RepositoryDeclaredBytes  int64  `json:"repository_declared_bytes"`
	RepositoryDigest         string `json:"repository_digest"`
	UnitCandidateCount       int    `json:"unit_candidate_count"`
	UnitRequiredCount        int    `json:"unit_required_count"`
	UnitDeclaredBytes        int64  `json:"unit_declared_bytes"`
	UnitDigest               string `json:"unit_digest"`
}

type Artifact struct {
	Name          string `json:"name"`
	Ordinal       int    `json:"ordinal"`
	RecordCount   int    `json:"record_count"`
	DeclaredBytes int64  `json:"declared_bytes"`
	ContentBytes  int64  `json:"content_bytes"`
	ContentDigest string `json:"content_digest"`
}

type CallerLeaf struct {
	Artifact
	Prefix     string `json:"prefix"`
	PrefixBits int    `json:"prefix_bits"`
}

// Manifest is the sole visibility authority for one candidate generation.
type Manifest struct {
	Schema            string           `json:"schema"`
	Repository        string           `json:"repository"`
	Commit            string           `json:"commit"`
	UnitDigest        string           `json:"unit_digest"`
	PolicyDigest      string           `json:"policy_digest"`
	GenerationDigest  string           `json:"generation_digest"`
	PartitionPolicy   PartitionPolicy  `json:"partition_policy"`
	Policies          []PolicyIdentity `json:"policies"`
	Corpus            CorpusSummary    `json:"corpus"`
	Domains           []DomainSummary  `json:"domains"`
	RepositoryMembers []Artifact       `json:"repository_members"`
	CallerLeaves      []CallerLeaf     `json:"caller_leaves"`
	Digest            string           `json:"digest"`
}

// State is the primitive persisted publication pointer. Manifest is a stable
// repository-relative filename, while all other fields bind its exact bytes.
type State struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	Commit           string `json:"commit"`
	UnitDigest       string `json:"unit_digest"`
	PolicyDigest     string `json:"policy_digest"`
	GenerationDigest string `json:"generation_digest"`
	ManifestDigest   string `json:"manifest_digest"`
	Manifest         string `json:"manifest"`
}

// Expected supplies caller-known identity when opening or publishing.
type Expected struct {
	Repository       string
	Commit           string
	Unit             *analysisunit.State
	Policies         []PolicyIdentity
	PolicyDigest     string
	GenerationDigest string
	ManifestDigest   string
}

func PolicyIdentities(policies []Policy) ([]PolicyIdentity, error) {
	identities := make([]PolicyIdentity, 0, len(policies))
	for _, policy := range policies {
		identity := PolicyIdentity{
			Domain: policy.Domain, Version: policy.Version,
			EnumerationPolicy: policy.EnumerationPolicy,
			SymlinkPolicy:     policy.SymlinkPolicy, Plane: policy.Plane,
		}
		if identity.SymlinkPolicy == "" && policy.RejectSymlink == nil {
			identity.SymlinkPolicy = "none"
		}
		if err := validatePolicyIdentity(identity); err != nil {
			return nil, err
		}
		if policy.Enumerate == nil {
			return nil, fmt.Errorf("%w: %s has no enumeration predicate", ErrInvalidPolicy, policy.Domain)
		}
		if identity.SymlinkPolicy == "none" && policy.RejectSymlink != nil ||
			identity.SymlinkPolicy != "none" && policy.RejectSymlink == nil {
			return nil, fmt.Errorf(
				"%w: %s symlink policy and predicate disagree",
				ErrInvalidPolicy, policy.Domain,
			)
		}
		identities = append(identities, identity)
	}
	if err := canonicalizePolicyIdentities(identities); err != nil {
		return nil, err
	}
	return identities, nil
}

func PolicyDigest(identities []PolicyIdentity) (string, error) {
	canonical := slices.Clone(identities)
	if err := canonicalizePolicyIdentities(canonical); err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Schema    string           `json:"schema"`
		Partition PartitionPolicy  `json:"partition_policy"`
		Policies  []PolicyIdentity `json:"policies"`
	}{
		Schema: ManifestSchema, Partition: frozenPartitionPolicy(), Policies: canonical,
	})
	if err != nil {
		return "", err
	}
	return digest("phebs-candidate-policy-v1\x00", payload), nil
}

func GenerationDigest(
	repository, commit, unitDigest string,
	identities []PolicyIdentity,
) (string, error) {
	if !safeRepository(repository) || !gitobj.IsObjectID(commit) ||
		(unitDigest != "" && !validDigest(unitDigest)) {
		return "", errors.New("invalid candidate generation identity")
	}
	policyDigest, err := PolicyDigest(identities)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Schema       string `json:"schema"`
		Repository   string `json:"repository"`
		Commit       string `json:"commit"`
		UnitDigest   string `json:"unit_digest"`
		PolicyDigest string `json:"policy_digest"`
	}{
		Schema: ManifestSchema, Repository: repository, Commit: commit,
		UnitDigest: unitDigest, PolicyDigest: policyDigest,
	})
	if err != nil {
		return "", err
	}
	return digest("phebs-candidate-generation-v1\x00", payload), nil
}

func ManifestDigest(manifest Manifest) (string, error) {
	manifest.Digest = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digest("phebs-candidate-manifest-v1\x00", payload), nil
}

func (manifest Manifest) State() State {
	return State{
		Schema: StateSchema, Repository: manifest.Repository, Commit: manifest.Commit,
		UnitDigest: manifest.UnitDigest, PolicyDigest: manifest.PolicyDigest,
		GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest,
		Manifest: ManifestName(manifest.Repository),
	}
}

func (state State) Validate() error {
	if state.Schema != StateSchema || !safeRepository(state.Repository) ||
		!gitobj.IsObjectID(state.Commit) ||
		(state.UnitDigest != "" && !validDigest(state.UnitDigest)) ||
		!validDigest(state.PolicyDigest) || !validDigest(state.GenerationDigest) ||
		!validDigest(state.ManifestDigest) || state.Manifest != ManifestName(state.Repository) {
		return errors.New("invalid candidate publication state")
	}
	return nil
}

func canonicalizePolicyIdentities(identities []PolicyIdentity) error {
	if len(identities) == 0 {
		return fmt.Errorf("%w: at least one policy is required", ErrInvalidPolicy)
	}
	if len(identities) > MaxPolicies {
		return fmt.Errorf(
			"%w: at most %d policies are allowed", ErrInvalidPolicy, MaxPolicies,
		)
	}
	slices.SortFunc(identities, func(a, b PolicyIdentity) int {
		if value := strings.Compare(a.Domain, b.Domain); value != 0 {
			return value
		}
		if value := strings.Compare(a.Version, b.Version); value != 0 {
			return value
		}
		if value := strings.Compare(string(a.Plane), string(b.Plane)); value != 0 {
			return value
		}
		if value := strings.Compare(a.EnumerationPolicy, b.EnumerationPolicy); value != 0 {
			return value
		}
		return strings.Compare(a.SymlinkPolicy, b.SymlinkPolicy)
	})
	for index, identity := range identities {
		if err := validatePolicyIdentity(identity); err != nil {
			return err
		}
		if index > 0 && identities[index-1].Domain == identity.Domain {
			return fmt.Errorf(
				"%w: duplicate domain %s", ErrInvalidPolicy, identity.Domain,
			)
		}
	}
	return nil
}

func validatePolicyIdentity(identity PolicyIdentity) error {
	if !validToken(identity.Domain, 128) || !validToken(identity.Version, 128) ||
		!validToken(identity.EnumerationPolicy, 256) ||
		!validToken(identity.SymlinkPolicy, 256) ||
		(identity.Plane != PlaneLocal && identity.Plane != PlaneRepository && identity.Plane != PlaneCaller) {
		return fmt.Errorf("%w: malformed identity", ErrInvalidPolicy)
	}
	return nil
}

func validateUnit(repository string, unit *analysisunit.State) (string, error) {
	if unit == nil {
		return "", nil
	}
	if err := unit.Validate(repository); err != nil {
		return "", err
	}
	if !validDigest(unit.Digest) {
		return "", errors.New("analysis-unit digest is invalid")
	}
	return unit.Digest, nil
}

func digest(domain string, payload []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	decoded, err := hex.DecodeString(encoded)
	return err == nil && hex.EncodeToString(decoded) == encoded
}

func validToken(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func safeRepository(value string) bool {
	return reponame.Validate(value) == nil
}

func safePath(value string) bool {
	return repopath.Validate(value) == nil
}

func strictDecode(reader io.Reader, limit int64, destination any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return errors.New("candidate control input exceeds its byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("candidate control input has trailing JSON")
		}
		return err
	}
	return nil
}
