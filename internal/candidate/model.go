// Package candidate builds and validates the commit-bound candidate census
// shared by focused extraction and repository-overlay caller planning.
package candidate

import (
	"bytes"
	"context"
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
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	ManifestSchema = "phebs-candidate-manifest-v4"
	RecordSchema   = "phebs-candidate-record-v2"
	StateSchema    = "phebs-candidate-state-v4"

	EnumerationPolicyVersion                     = "phebs-candidate-enumeration-v3"
	CallerHashPolicy                             = "phebs-caller-path-v1"
	LocalProjectionPolicy                        = "focused-domain-source-lane-v2"
	WholeRepositoryExecutionSubrangePolicy       = "whole-repository-execution-subrange-v1"
	InitialCallerPrefixBits                      = 2
	MaxPolicies                                  = 64
	MaxCorpusEntries                             = 10_000_000
	MaxRecordsPerArtifact                        = 4096
	MaxDeclaredBytesPerArtifact            int64 = 64 << 20
	MaxLocalProjectionArtifacts                  = 16_384
	MaxLocalProjectionContentBytes               = int64(4 << 30)

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

// Plane identifies how a domain consumes candidates. Repository policies use
// the canonical repository plane, focused local policies use exact durable
// projections, and caller policies use hash leaves.
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
	// MaxRecords optionally reduces focused-local artifact cardinality below
	// the repository-wide frozen maximum. Zero preserves the historical
	// MaxRecordsPerArtifact contract. Only PlaneLocal may reduce this value.
	MaxRecords int
	// ExecutionMaxRecords is a separate whole-repository execution bound. It
	// never repacks the shared candidate member or changes focused-local
	// projection identity; the exact version token binds how sparse execution
	// partitions cover one member in contiguous subranges. Only PlaneLocal may
	// opt in because this contract exists for local domains consuming the
	// unitless shared-repository route.
	ExecutionPartitionPolicy string
	ExecutionMaxRecords      int
	Enumerate                func(string) bool
	Required                 func(string) bool
	RejectSymlink            func(string) bool
	// TypedInputs names parser inputs consumed outside the ordinary source
	// replay. T30.5 supports only "scip"; the selected path is supplied by
	// the analysis-unit state (or the whole-repository legacy default).
	TypedInputs []string
}

// PolicyIdentity is the serializable, content-addressed policy contract.
type PolicyIdentity struct {
	Domain                   string   `json:"domain"`
	Version                  string   `json:"version"`
	EnumerationPolicy        string   `json:"enumeration_policy"`
	SymlinkPolicy            string   `json:"symlink_policy"`
	Plane                    Plane    `json:"plane"`
	MaxRecords               int      `json:"max_records,omitempty"`
	ExecutionPartitionPolicy string   `json:"execution_partition_policy,omitempty"`
	ExecutionMaxRecords      int      `json:"execution_max_records,omitempty"`
	TypedInputs              []string `json:"typed_inputs,omitempty"`
}

// TypedIndexSelection is the generation-bound designation of one typed input.
// It is separate from the stable analysis-unit digest: switching which
// already-supported file is the typed index must still create a new candidate
// generation.
type TypedIndexSelection struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// TypedInput is the strictly validated Git-blob envelope supplied to the
// domains that declare the matching Kind. It is intentionally not a fake
// ordinary source record and always preserves the actual committed path.
type TypedInput struct {
	Kind          string   `json:"kind"`
	Path          string   `json:"path"`
	OID           string   `json:"oid,omitempty"`
	DeclaredBytes int64    `json:"declared_bytes"`
	Domains       []string `json:"domains"`
	Present       bool     `json:"present"`
	InUnit        bool     `json:"in_unit"`
	Shared        bool     `json:"shared"`
}

// PartitionPolicy freezes every caller assignment, focused-local projection,
// and artifact bound.
type PartitionPolicy struct {
	EnumerationPolicy              string `json:"enumeration_policy"`
	CallerHashPolicy               string `json:"caller_hash_policy"`
	LocalProjectionPolicy          string `json:"local_projection_policy"`
	InitialPrefixBits              int    `json:"initial_prefix_bits"`
	MaxRecords                     int    `json:"max_records"`
	MaxDeclaredBytes               int64  `json:"max_declared_bytes"`
	MaxLocalProjectionArtifacts    int    `json:"max_local_projection_artifacts"`
	MaxLocalProjectionContentBytes int64  `json:"max_local_projection_content_bytes"`
	RecordOrdering                 string `json:"record_ordering"`
	SplitRule                      string `json:"split_rule"`
}

func frozenPartitionPolicy() PartitionPolicy {
	return PartitionPolicy{
		EnumerationPolicy:              EnumerationPolicyVersion,
		CallerHashPolicy:               CallerHashPolicy,
		LocalProjectionPolicy:          LocalProjectionPolicy,
		InitialPrefixBits:              InitialCallerPrefixBits,
		MaxRecords:                     MaxRecordsPerArtifact,
		MaxDeclaredBytes:               MaxDeclaredBytesPerArtifact,
		MaxLocalProjectionArtifacts:    MaxLocalProjectionArtifacts,
		MaxLocalProjectionContentBytes: MaxLocalProjectionContentBytes,
		RecordOrdering:                 "hash-path-oid-v2",
		SplitRule:                      "next-hash-bit-v1",
	}
}

// SourceLane is the path-derived ordinary-source classification retained for
// later local-evidence and caller-leaf consumers. It is independent of
// semantic analysis-unit scope and richer extractor code roles.
type SourceLane string

const (
	SourceLaneBase   SourceLane = "base"
	SourceLaneGoTest SourceLane = "go_test"
)

// Record is one regular Git blob selected by at least one policy. SourceLane
// is recomputed from Path during strict validation. Required is a domain-view
// projection and is not persisted.
type Record struct {
	Schema          string     `json:"schema"`
	Path            string     `json:"path"`
	OID             string     `json:"oid"`
	DeclaredBytes   int64      `json:"declared_bytes"`
	SourceLane      SourceLane `json:"source_lane"`
	Domains         []string   `json:"domains"`
	RequiredDomains []string   `json:"required_domains"`
	InUnit          bool       `json:"in_unit"`
	Shared          bool       `json:"shared"`
	Hash            string     `json:"hash,omitempty"`
	Required        bool       `json:"-"`
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

// ScopeSummary is the exact manifest-bound corpus and candidate projection a
// domain receives. Local domains use the unit projection when one is active;
// caller/repository planes retain the repository view until T30.6.
type ScopeSummary struct {
	ManifestDigest string
	UnitDigest     string
	Corpus         CorpusSummary
	Domain         DomainSummary
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

// LocalProjection is the durable, domain-addressed focused-unit replay plane.
// PolicyOrdinal binds its filesystem namespace to the canonical policy set;
// an empty Members slice explicitly commits that the domain has no candidates.
type LocalProjection struct {
	Domain        string     `json:"domain"`
	Version       string     `json:"version"`
	PolicyOrdinal int        `json:"policy_ordinal"`
	Members       []Artifact `json:"members"`
}

// Manifest is the sole visibility authority for one candidate generation.
type Manifest struct {
	Schema            string               `json:"schema"`
	Repository        string               `json:"repository"`
	Commit            string               `json:"commit"`
	UnitDigest        string               `json:"unit_digest"`
	PolicyDigest      string               `json:"policy_digest"`
	GenerationDigest  string               `json:"generation_digest"`
	TypedIndex        *TypedIndexSelection `json:"typed_index,omitempty"`
	PartitionPolicy   PartitionPolicy      `json:"partition_policy"`
	Policies          []PolicyIdentity     `json:"policies"`
	Corpus            CorpusSummary        `json:"corpus"`
	UnitCorpus        CorpusSummary        `json:"unit_corpus"`
	Domains           []DomainSummary      `json:"domains"`
	TypedInputs       []TypedInput         `json:"typed_inputs"`
	RepositoryMembers []Artifact           `json:"repository_members"`
	LocalProjections  []LocalProjection    `json:"local_projections"`
	CallerLeaves      []CallerLeaf         `json:"caller_leaves"`
	Digest            string               `json:"digest"`
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
			MaxRecords:               policy.MaxRecords,
			ExecutionPartitionPolicy: policy.ExecutionPartitionPolicy,
			ExecutionMaxRecords:      policy.ExecutionMaxRecords,
			TypedInputs:              slices.Clone(policy.TypedInputs),
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
	canonical := clonePolicyIdentities(identities)
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
	return digest("phebs-candidate-policy-v2\x00", payload), nil
}

func clonePolicyIdentities(input []PolicyIdentity) []PolicyIdentity {
	result := slices.Clone(input)
	for index := range result {
		result[index].TypedInputs = slices.Clone(input[index].TypedInputs)
	}
	return result
}

// EqualPolicyIdentities compares the complete serializable policy contract.
func EqualPolicyIdentities(left, right []PolicyIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Domain != right[index].Domain ||
			left[index].Version != right[index].Version ||
			left[index].EnumerationPolicy != right[index].EnumerationPolicy ||
			left[index].SymlinkPolicy != right[index].SymlinkPolicy ||
			left[index].Plane != right[index].Plane ||
			left[index].MaxRecords != right[index].MaxRecords ||
			left[index].ExecutionPartitionPolicy != right[index].ExecutionPartitionPolicy ||
			left[index].ExecutionMaxRecords != right[index].ExecutionMaxRecords ||
			!slices.Equal(left[index].TypedInputs, right[index].TypedInputs) {
			return false
		}
	}
	return true
}

func GenerationDigest(
	repository, commit string,
	unit *analysisunit.State,
	identities []PolicyIdentity,
) (string, error) {
	unitDigest, err := validateUnit(repository, unit)
	if err != nil {
		return "", err
	}
	selection, err := typedIndexSelection(unit, identities)
	if err != nil {
		return "", err
	}
	return generationDigest(
		repository, commit, unitDigest, selection, identities,
	)
}

func generationDigest(
	repository, commit, unitDigest string,
	selection *TypedIndexSelection,
	identities []PolicyIdentity,
) (string, error) {
	if !safeRepository(repository) || !gitobj.IsObjectID(commit) ||
		(unitDigest != "" && !validDigest(unitDigest)) {
		return "", errors.New("invalid candidate generation identity")
	}
	if err := validateTypedIndexSelection(selection); err != nil {
		return "", err
	}
	policyDigest, err := PolicyDigest(identities)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Schema       string               `json:"schema"`
		Repository   string               `json:"repository"`
		Commit       string               `json:"commit"`
		UnitDigest   string               `json:"unit_digest"`
		PolicyDigest string               `json:"policy_digest"`
		TypedIndex   *TypedIndexSelection `json:"typed_index,omitempty"`
	}{
		Schema: ManifestSchema, Repository: repository, Commit: commit,
		UnitDigest: unitDigest, PolicyDigest: policyDigest,
		TypedIndex: cloneTypedIndexSelection(selection),
	})
	if err != nil {
		return "", err
	}
	return digest("phebs-candidate-generation-v2\x00", payload), nil
}

func ManifestDigest(manifest Manifest) (string, error) {
	manifest.Digest = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digest("phebs-candidate-manifest-v2\x00", payload), nil
}

// SourceLaneForPath returns the canonical ordinary-source lane for a validated
// repository-relative path. Exact lowercase _test.go suffix is the complete
// v1 classification rule; directory names and SCIP roles do not override it.
func SourceLaneForPath(path string) SourceLane {
	if strings.HasSuffix(path, "_test.go") {
		return SourceLaneGoTest
	}
	return SourceLaneBase
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
		slices.Sort(identity.TypedInputs)
		identities[index].TypedInputs = slices.Clone(identity.TypedInputs)
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
	if identity.MaxRecords < 0 || identity.MaxRecords >= MaxRecordsPerArtifact ||
		(identity.MaxRecords != 0 && identity.Plane != PlaneLocal) {
		return fmt.Errorf("%w: malformed focused-local record bound", ErrInvalidPolicy)
	}
	if identity.ExecutionPartitionPolicy == "" {
		if identity.ExecutionMaxRecords != 0 {
			return fmt.Errorf("%w: execution record bound has no policy", ErrInvalidPolicy)
		}
	} else if identity.ExecutionPartitionPolicy != WholeRepositoryExecutionSubrangePolicy ||
		identity.ExecutionMaxRecords <= 0 ||
		identity.ExecutionMaxRecords >= MaxRecordsPerArtifact ||
		identity.Plane != PlaneLocal {
		return fmt.Errorf("%w: malformed whole-repository execution bound", ErrInvalidPolicy)
	}
	if !sortedUniqueStrings(identity.TypedInputs) {
		return fmt.Errorf("%w: malformed typed-input identity", ErrInvalidPolicy)
	}
	for _, kind := range identity.TypedInputs {
		if kind != analysisunit.TypedIndexKindSCIP {
			return fmt.Errorf("%w: unsupported typed input %q", ErrInvalidPolicy, kind)
		}
	}
	return nil
}

func policyMaxRecords(identity PolicyIdentity) int {
	if identity.MaxRecords > 0 {
		return identity.MaxRecords
	}
	return MaxRecordsPerArtifact
}

func policyExecutionMaxRecords(identity PolicyIdentity) int {
	if identity.ExecutionPartitionPolicy == WholeRepositoryExecutionSubrangePolicy {
		return identity.ExecutionMaxRecords
	}
	return 0
}

func typedIndexSelection(
	unit *analysisunit.State,
	identities []PolicyIdentity,
) (*TypedIndexSelection, error) {
	if unit != nil && unit.TypedIndex != nil {
		selection := &TypedIndexSelection{
			Kind: unit.TypedIndex.Kind,
			Path: unit.TypedIndex.Path,
		}
		if err := validateTypedIndexSelection(selection); err != nil {
			return nil, err
		}
		return selection, nil
	}
	consumesSCIP := false
	for _, identity := range identities {
		if slices.Contains(identity.TypedInputs, analysisunit.TypedIndexKindSCIP) {
			consumesSCIP = true
			break
		}
	}
	if !consumesSCIP {
		return nil, nil
	}
	if unit == nil {
		return &TypedIndexSelection{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: "index.scip",
		}, nil
	}
	return nil, nil
}

func validateTypedIndexSelection(selection *TypedIndexSelection) error {
	if selection == nil {
		return nil
	}
	if selection.Kind != analysisunit.TypedIndexKindSCIP ||
		!safePath(selection.Path) {
		return errors.New("invalid candidate typed-index selection")
	}
	return nil
}

func cloneTypedIndexSelection(input *TypedIndexSelection) *TypedIndexSelection {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func sortedUniqueStrings(values []string) bool {
	for index, value := range values {
		if !validToken(value, 128) ||
			index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
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
	return strictDecodeMember(nil, reader, limit, destination) //nolint:staticcheck // Control decoding deliberately carries no member-observation context.
}

// A nil member context keeps control decoding uncharged. Record consumers
// charge the first successful decode before trailing or semantic validation.
func strictDecodeMember(memberContext context.Context, reader io.Reader, limit int64, destination any) error {
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
	if memberContext != nil {
		if err := readaccounting.Charge(memberContext, readaccounting.MemberVisit, 1); err != nil {
			return err
		}
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
