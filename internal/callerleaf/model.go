// Package callerleaf owns immutable, independently invisible caller-leaf
// execution artifacts. Complete-generation visibility belongs to T30.6i.
package callerleaf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority/authorityvalidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	GenerationSchema       = "phebs-caller-generation-v1"
	GenerationSchemaV2     = "phebs-caller-generation-v2"
	PairSchema             = "phebs-caller-leaf-pair-v1"
	RecordSchema           = "phebs-caller-leaf-record-v1"
	CoverageRecordSchema   = "phebs-caller-leaf-coverage-record-v1"
	CoverageRecordSchemaV2 = "phebs-caller-leaf-coverage-record-v2"
	CoverageSchema         = "phebs-caller-leaf-coverage-v1"
	CoverageSchemaV2       = "phebs-caller-leaf-coverage-v2"
	MetadataSchema         = "phebs-caller-leaf-metadata-v1"
	PolicyName             = "phebs-direct-caller-leaf-policy-v1"
	PolicyNameV2           = "phebs-direct-caller-leaf-policy-v2"
	PolicyNameV3           = "phebs-direct-caller-leaf-policy-v3"
	LeafAdapterV1          = "direct-syntax-base-v1"
	LeafAdapterV2          = "direct-syntax-compact-coverage-v2"
	LeafAdapterV3          = "direct-syntax-zero-fact-coverage-v3"
	CurrentLeafAdapter     = LeafAdapterV3

	RecordResult                     = "result"
	RecordAbstention                 = "abstention"
	RecordCoverage                   = "coverage"
	AbstentionReasonNoDirectCaller   = "no_direct_caller"
	AbstentionReasonUnresolvedCaller = "unresolved_caller"

	CoverageReasonNoResolverDescriptors = "no_resolver_descriptors"
	CoverageReasonZeroCallerFacts       = "zero_caller_facts"
	CoverageGapCatalogOwnedInput        = "catalog_owned_input"
	CoverageGapDomainUnselected         = "domain_unselected"
	CoverageGapExcludedGoTest           = "excluded_go_test"
	CoverageGapInvalidUTF8              = "invalid_utf8"
	CoverageGapResolverGeneratedInput   = "resolver_generated_input"
	CoverageGapSourceTooLarge           = "source_too_large"

	MaxCallerDomains              = 16
	MaxExpectedPairs              = 16_384
	MaxResultRecordsPerPair       = 12_500
	MaxAbstentionRecordsPerPair   = 4_096
	MaxRecordBytes                = 1 << 20
	MaxCanonicalBytesPerPair      = int64(64 << 20)
	MaxMetadataBytes              = 64 << 10
	MaxStagingBytesPerPair        = int64(65 << 20)
	MaxAggregateResultRecords     = 100_000
	MaxAggregateAbstentionRecords = 100_000
	MaxAggregateCanonicalBytes    = int64(512 << 20)
	MaxAggregateStagingBytes      = int64(520 << 20)
	MaxCoverageRecordsPerPair     = 1
	MaxAggregateCoverageRecords   = MaxExpectedPairs
	MaxAggregateCoveredCandidates = MaxExpectedPairs * candidate.MaxRecordsPerArtifact
	MaxRepositoryDirectoryEntries = 65_536
	MaxDirectSourceBytes          = int64(4 << 20)
	MaxSourceBlobBytesPerPair     = int64(64 << 20)
	PublicationMemoryDesignBytes  = int64(128 << 20)
	// MaxOpenFiles is structural: the pinned repository root, stage file,
	// candidate member, and separate bounded Git stdout/stderr pipes.
	MaxOpenFiles  = 5
	WorkerTimeout = 5 * time.Minute
)

var (
	ErrInvalidArtifact  = errors.New("invalid caller leaf artifact")
	ErrLimit            = errors.New("caller leaf bound exceeded")
	ErrCapacity         = errors.New("caller leaf repository capacity unavailable")
	ErrNondeterministic = errors.New("caller leaf output is nondeterministic")
)

type Policy struct {
	Name                          string `json:"name"`
	LeafAdapter                   string `json:"leaf_adapter"`
	SourceLanePolicy              string `json:"source_lane_policy"`
	MaxCallerDomains              int    `json:"max_caller_domains"`
	MaxExpectedPairs              int    `json:"max_expected_pairs"`
	MaxResultRecordsPerPair       int    `json:"max_result_records_per_pair"`
	MaxAbstentionRecordsPerPair   int    `json:"max_abstention_records_per_pair"`
	MaxRecordBytes                int    `json:"max_record_bytes"`
	MaxCanonicalBytesPerPair      int64  `json:"max_canonical_bytes_per_pair"`
	MaxMetadataBytes              int    `json:"max_metadata_bytes"`
	MaxStagingBytesPerPair        int64  `json:"max_staging_bytes_per_pair"`
	MaxAggregateResultRecords     int    `json:"max_aggregate_result_records"`
	MaxAggregateAbstentionRecords int    `json:"max_aggregate_abstention_records"`
	MaxAggregateCanonicalBytes    int64  `json:"max_aggregate_canonical_bytes"`
	MaxAggregateStagingBytes      int64  `json:"max_aggregate_staging_bytes"`
	MaxRepositoryDirectoryEntries int    `json:"max_repository_directory_entries"`
	MaxDirectSourceBytes          int64  `json:"max_direct_source_bytes"`
	MaxSourceBlobBytesPerPair     int64  `json:"max_source_blob_bytes_per_pair"`
	PublicationMemoryDesignBytes  int64  `json:"publication_memory_design_bytes"`
	MaxOpenFiles                  int    `json:"max_open_files"`
	WorkerTimeoutMilliseconds     int64  `json:"worker_timeout_milliseconds"`
	MaxCoverageRecordsPerPair     int    `json:"max_coverage_records_per_pair,omitempty"`
	MaxAggregateCoverageRecords   int    `json:"max_aggregate_coverage_records,omitempty"`
	MaxAggregateCoveredCandidates int    `json:"max_aggregate_covered_candidates,omitempty"`
}

func FrozenPolicy() Policy {
	return frozenPolicy(LeafAdapterV1)
}

func CurrentPolicy() Policy {
	return frozenPolicy(CurrentLeafAdapter)
}

func frozenPolicy(adapter string) Policy {
	name := PolicyName
	coveragePerPair := 0
	aggregateCoverage := 0
	aggregateCandidates := 0
	if adapter == LeafAdapterV2 || adapter == LeafAdapterV3 {
		name = PolicyNameV2
		if adapter == LeafAdapterV3 {
			name = PolicyNameV3
		}
		coveragePerPair = MaxCoverageRecordsPerPair
		aggregateCoverage = MaxAggregateCoverageRecords
		aggregateCandidates = MaxAggregateCoveredCandidates
	}
	return Policy{
		Name: name, LeafAdapter: adapter,
		SourceLanePolicy: callerleafid.SourceLanePolicy,
		MaxCallerDomains: MaxCallerDomains, MaxExpectedPairs: MaxExpectedPairs,
		MaxResultRecordsPerPair:       MaxResultRecordsPerPair,
		MaxAbstentionRecordsPerPair:   MaxAbstentionRecordsPerPair,
		MaxRecordBytes:                MaxRecordBytes,
		MaxCanonicalBytesPerPair:      MaxCanonicalBytesPerPair,
		MaxMetadataBytes:              MaxMetadataBytes,
		MaxStagingBytesPerPair:        MaxStagingBytesPerPair,
		MaxAggregateResultRecords:     MaxAggregateResultRecords,
		MaxAggregateAbstentionRecords: MaxAggregateAbstentionRecords,
		MaxAggregateCanonicalBytes:    MaxAggregateCanonicalBytes,
		MaxAggregateStagingBytes:      MaxAggregateStagingBytes,
		MaxRepositoryDirectoryEntries: MaxRepositoryDirectoryEntries,
		MaxDirectSourceBytes:          MaxDirectSourceBytes,
		MaxSourceBlobBytesPerPair:     MaxSourceBlobBytesPerPair,
		PublicationMemoryDesignBytes:  PublicationMemoryDesignBytes,
		MaxOpenFiles:                  MaxOpenFiles,
		WorkerTimeoutMilliseconds:     WorkerTimeout.Milliseconds(),
		MaxCoverageRecordsPerPair:     coveragePerPair,
		MaxAggregateCoverageRecords:   aggregateCoverage,
		MaxAggregateCoveredCandidates: aggregateCandidates,
	}
}

type ExtractorIdentity struct {
	Domain             string `json:"domain"`
	Version            string `json:"version"`
	LeafAdapterVersion string `json:"leaf_adapter_version"`
}

type GenerationIdentity struct {
	Schema                   string              `json:"schema"`
	Repository               string              `json:"repository"`
	HeadCommit               string              `json:"head_commit"`
	UnitDigest               string              `json:"unit_digest"`
	DeclarationSetDigest     string              `json:"declaration_set_digest"`
	CandidateManifestDigest  string              `json:"candidate_manifest_digest"`
	CandidatePolicyDigest    string              `json:"candidate_policy_digest"`
	SourceLanePolicy         string              `json:"source_lane_policy"`
	ResolverGenerationDigest string              `json:"resolver_generation_digest"`
	ResolverManifestDigest   string              `json:"resolver_manifest_digest"`
	CallerPolicy             Policy              `json:"caller_policy"`
	CallerPolicyDigest       string              `json:"caller_policy_digest"`
	Extractors               []ExtractorIdentity `json:"extractors"`
	ExtractorSetDigest       string              `json:"extractor_set_digest"`
	Digest                   string              `json:"digest"`
	Upstream                 json.RawMessage     `json:"upstream,omitempty"`
	UpstreamDigest           string              `json:"upstream_digest,omitempty"`
}

type LeafDescriptor struct {
	Name          string `json:"name"`
	Ordinal       int    `json:"ordinal"`
	Prefix        string `json:"prefix"`
	PrefixBits    int    `json:"prefix_bits"`
	RecordCount   int    `json:"record_count"`
	DeclaredBytes int64  `json:"declared_bytes"`
	ContentBytes  int64  `json:"content_bytes"`
	ContentDigest string `json:"content_digest"`
}

type PairIdentity struct {
	Schema             string         `json:"schema"`
	GenerationDigest   string         `json:"generation_digest"`
	Domain             string         `json:"domain"`
	ExtractorVersion   string         `json:"extractor_version"`
	LeafAdapterVersion string         `json:"leaf_adapter_version"`
	Leaf               LeafDescriptor `json:"leaf"`
	Digest             string         `json:"digest"`
}

type Metadata struct {
	Schema           string `json:"schema"`
	GenerationDigest string `json:"generation_digest"`
	PairDigest       string `json:"pair_digest"`
	PolicyDigest     string `json:"policy_digest"`
}

type Receipt struct {
	Name                  string `json:"name"`
	RecordCount           int    `json:"record_count"`
	ResultCount           int    `json:"result_count"`
	AbstentionCount       int    `json:"abstention_count"`
	ContentBytes          int64  `json:"content_bytes"`
	ContentDigest         string `json:"content_digest"`
	MetadataDigest        string `json:"metadata_digest"`
	StagingBytes          int64  `json:"staging_bytes"`
	ExcludedGoTestRecords int    `json:"excluded_go_test_records"`
	SourceBlobReads       int    `json:"source_blob_reads"`
	SourceBlobBytes       int64  `json:"source_blob_bytes"`
	OutOfLeafReads        int    `json:"out_of_leaf_reads"`
	CoverageRecordCount   int    `json:"coverage_record_count,omitempty"`
	CoveredCandidateCount int    `json:"covered_candidate_count,omitempty"`
	CoverageReason        string `json:"coverage_reason,omitempty"`
}

// AggregateReceipt is the complete-generation admission input handed to
// T30.6i. It is not a visibility pointer and names no consumer surface.
type AggregateReceipt struct {
	PairCount             int   `json:"pair_count"`
	ArtifactCount         int   `json:"artifact_count"`
	ResultCount           int   `json:"result_count"`
	AbstentionCount       int   `json:"abstention_count"`
	CanonicalBytes        int64 `json:"canonical_bytes"`
	StagingBytes          int64 `json:"staging_bytes"`
	PeakOpenFiles         int   `json:"peak_open_files"`
	CoverageRecordCount   int   `json:"coverage_record_count,omitempty"`
	CoveredCandidateCount int   `json:"covered_candidate_count,omitempty"`
}

type Record struct {
	Schema     string               `json:"schema"`
	Kind       string               `json:"kind"`
	Path       string               `json:"path"`
	ObjectID   string               `json:"object_id"`
	SourceLane candidate.SourceLane `json:"source_lane"`
	Fact       *sdk.Fact            `json:"fact,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	Coverage   *Coverage            `json:"coverage,omitempty"`
}

// Coverage is the exact compact replacement for per-candidate abstentions.
// Pair and candidate-member commitments prevent a certificate from being
// replayed across immutable leaves. Covered, excluded, and unselected counts
// partition the complete candidate member so an empty result cannot be
// mistaken for missing work. V2 zero-fact coverage additionally binds the
// exact source-byte receipt retained after the complete descriptor scan.
type Coverage struct {
	Schema                 string        `json:"schema"`
	PairDigest             string        `json:"pair_digest"`
	CandidateMemberName    string        `json:"candidate_member_name"`
	CandidateContentDigest string        `json:"candidate_content_digest"`
	CandidateRecordCount   int           `json:"candidate_record_count"`
	CoveredCandidateCount  int           `json:"covered_candidate_count"`
	NoDirectCandidateCount int           `json:"no_direct_candidate_count"`
	Gaps                   []CoverageGap `json:"gaps,omitempty"`
	Reason                 string        `json:"reason"`
	SourceBlobBytes        int64         `json:"source_blob_bytes,omitempty"`
}

type CoverageGap struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

func NewGenerationIdentity(input GenerationIdentity) (GenerationIdentity, error) {
	input.Schema = GenerationSchema
	if len(input.Upstream) != 0 {
		input.Schema = GenerationSchemaV2
		if len(input.Upstream) > 1<<20 || !json.Valid(input.Upstream) || !validDigest(input.UpstreamDigest) {
			return GenerationIdentity{}, errors.New("caller generation has invalid upstream authority")
		}
		upstream, validateErr := authorityvalidate.Canonical(input.Upstream)
		if validateErr != nil || !upstream.Usable || upstream.Repository != input.Repository ||
			upstream.Digest != input.UpstreamDigest {
			return GenerationIdentity{}, errors.New("caller generation has invalid upstream authority")
		}
		input.Upstream = slices.Clone(input.Upstream)
	} else if input.UpstreamDigest != "" {
		return GenerationIdentity{}, errors.New("caller generation has detached upstream digest")
	}
	input.SourceLanePolicy = callerleafid.SourceLanePolicy
	input.Digest = ""
	if reponame.Validate(input.Repository) != nil ||
		!gitobj.IsObjectID(input.HeadCommit) ||
		input.UnitDigest != "" && !validDigest(input.UnitDigest) ||
		!validDigest(input.DeclarationSetDigest) ||
		!validDigest(input.CandidateManifestDigest) ||
		!validDigest(input.CandidatePolicyDigest) ||
		!validDigest(input.ResolverGenerationDigest) ||
		!validDigest(input.ResolverManifestDigest) {
		return GenerationIdentity{}, errors.New("caller generation has invalid authority")
	}
	if len(input.Extractors) == 0 || len(input.Extractors) > MaxCallerDomains {
		return GenerationIdentity{}, errors.New("caller generation extractor set is empty or unbounded")
	}
	input.Extractors = slices.Clone(input.Extractors)
	adapter := ""
	for index, current := range input.Extractors {
		if !validToken(current.Domain, 128) || !validToken(current.Version, 64) ||
			(current.LeafAdapterVersion != LeafAdapterV1 &&
				current.LeafAdapterVersion != LeafAdapterV2 &&
				current.LeafAdapterVersion != LeafAdapterV3) ||
			index > 0 && input.Extractors[index-1].Domain >= current.Domain {
			return GenerationIdentity{}, errors.New("caller generation extractor set is invalid or unordered")
		}
		if adapter == "" {
			adapter = current.LeafAdapterVersion
		} else if adapter != current.LeafAdapterVersion {
			return GenerationIdentity{}, errors.New("caller generation mixes leaf adapter versions")
		}
	}
	input.CallerPolicy = frozenPolicy(adapter)
	policyDigest, err := digestJSON("phebs-caller-leaf-policy-v1\x00", input.CallerPolicy)
	if err != nil {
		return GenerationIdentity{}, err
	}
	input.CallerPolicyDigest = policyDigest
	extractorDigest, err := digestJSON("phebs-caller-extractor-set-v1\x00", input.Extractors)
	if err != nil {
		return GenerationIdentity{}, err
	}
	input.ExtractorSetDigest = extractorDigest
	input.Digest, err = generationDigest(input)
	return input, err
}

func NewPairIdentity(
	generation GenerationIdentity,
	domain, version string,
	leaf LeafDescriptor,
) (PairIdentity, error) {
	if err := ValidateGenerationIdentity(generation); err != nil {
		return PairIdentity{}, err
	}
	adapter := ""
	for _, current := range generation.Extractors {
		if current.Domain == domain && current.Version == version {
			adapter = current.LeafAdapterVersion
			break
		}
	}
	if adapter == "" || !validLeafDescriptor(leaf) {
		return PairIdentity{}, errors.New("caller pair is outside its generation")
	}
	pair := PairIdentity{
		Schema: PairSchema, GenerationDigest: generation.Digest,
		Domain: domain, ExtractorVersion: version,
		LeafAdapterVersion: adapter, Leaf: leaf,
	}
	pair.Digest = digestFields(
		"phebs-caller-leaf-pair-v1\x00",
		pair.GenerationDigest, pair.Domain, pair.ExtractorVersion,
		pair.LeafAdapterVersion, strconv.Itoa(pair.Leaf.Ordinal),
		pair.Leaf.Prefix, strconv.Itoa(pair.Leaf.PrefixBits), pair.Leaf.Name,
		strconv.Itoa(pair.Leaf.RecordCount),
		strconv.FormatInt(pair.Leaf.DeclaredBytes, 10),
		strconv.FormatInt(pair.Leaf.ContentBytes, 10), pair.Leaf.ContentDigest,
	)
	return pair, nil
}

func ValidateGenerationIdentity(input GenerationIdentity) error {
	wanted, err := NewGenerationIdentity(input)
	if err != nil {
		return err
	}
	if wanted.Schema != input.Schema ||
		wanted.SourceLanePolicy != input.SourceLanePolicy ||
		wanted.CallerPolicy != input.CallerPolicy ||
		wanted.Digest != input.Digest ||
		wanted.CallerPolicyDigest != input.CallerPolicyDigest ||
		wanted.ExtractorSetDigest != input.ExtractorSetDigest ||
		!bytes.Equal(wanted.Upstream, input.Upstream) ||
		wanted.UpstreamDigest != input.UpstreamDigest {
		return errors.New("caller generation digest mismatch")
	}
	return nil
}

func ValidatePairIdentity(generation GenerationIdentity, pair PairIdentity) error {
	wanted, err := NewPairIdentity(
		generation, pair.Domain, pair.ExtractorVersion, pair.Leaf,
	)
	if err != nil {
		return err
	}
	if wanted != pair {
		return errors.New("caller pair digest mismatch")
	}
	return nil
}

func PairSetDigest(generation GenerationIdentity, pairs []PairIdentity) (string, error) {
	if err := ValidateGenerationIdentity(generation); err != nil {
		return "", err
	}
	if pairs == nil || len(pairs) > MaxExpectedPairs {
		return "", errors.New("caller pair set is nil or unbounded")
	}
	fields := make([]string, len(pairs))
	for index, pair := range pairs {
		if err := ValidatePairIdentity(generation, pair); err != nil {
			return "", err
		}
		if index > 0 && !pairLess(pairs[index-1], pair) {
			return "", errors.New("caller pair set is duplicated or unordered")
		}
		fields[index] = pair.Digest
	}
	return digestFields("phebs-caller-leaf-pair-set-v1\x00", fields...), nil
}

func MetadataFor(generation GenerationIdentity, pair PairIdentity) (Metadata, string, error) {
	if err := ValidatePairIdentity(generation, pair); err != nil {
		return Metadata{}, "", err
	}
	metadata := Metadata{
		Schema: MetadataSchema, GenerationDigest: generation.Digest,
		PairDigest: pair.Digest, PolicyDigest: generation.CallerPolicyDigest,
	}
	digest, err := digestJSON("phebs-caller-leaf-metadata-v1\x00", metadata)
	return metadata, digest, err
}

func ValidateRecord(record Record) error {
	switch record.Kind {
	case RecordResult:
		if record.Schema != RecordSchema || repopath.Validate(record.Path) != nil ||
			!gitobj.IsObjectID(record.ObjectID) ||
			record.SourceLane != candidate.SourceLaneBase || record.Coverage != nil ||
			record.Fact == nil || record.Reason != "" || record.Fact.Path != record.Path ||
			record.Fact.Atom.BlobDigest == "" ||
			!validDigest(record.Fact.Atom.BlobDigest) ||
			record.Fact.Atom.StartByte < 0 ||
			record.Fact.Atom.EndByte <= record.Fact.Atom.StartByte ||
			record.Fact.Assertion.Predicate == "" {
			return fmt.Errorf("%w: malformed result record", ErrInvalidArtifact)
		}
	case RecordAbstention:
		if record.Schema != RecordSchema || repopath.Validate(record.Path) != nil ||
			!gitobj.IsObjectID(record.ObjectID) ||
			record.SourceLane != candidate.SourceLaneBase || record.Coverage != nil ||
			!validToken(record.Reason, 256) ||
			record.Fact != nil && (record.Fact.Path != record.Path ||
				record.Fact.Assertion.Predicate != "UNRESOLVED_CALLER" ||
				!validDigest(record.Fact.Atom.BlobDigest) ||
				record.Fact.Atom.StartByte < 0 ||
				record.Fact.Atom.EndByte <= record.Fact.Atom.StartByte) {
			return fmt.Errorf("%w: malformed abstention record", ErrInvalidArtifact)
		}
	case RecordCoverage:
		if (record.Schema != CoverageRecordSchema &&
			record.Schema != CoverageRecordSchemaV2) || record.Path != "" ||
			record.ObjectID != "" || record.SourceLane != "" || record.Fact != nil ||
			record.Reason != "" || record.Coverage == nil ||
			validateCoverage(*record.Coverage) != nil ||
			record.Schema == CoverageRecordSchema &&
				record.Coverage.Schema != CoverageSchema ||
			record.Schema == CoverageRecordSchemaV2 &&
				record.Coverage.Schema != CoverageSchemaV2 {
			return fmt.Errorf("%w: malformed coverage record", ErrInvalidArtifact)
		}
	default:
		return fmt.Errorf("%w: unknown record kind", ErrInvalidArtifact)
	}
	return nil
}

func validateCoverage(coverage Coverage) error {
	if (coverage.Schema != CoverageSchema && coverage.Schema != CoverageSchemaV2) ||
		!validDigest(coverage.PairDigest) ||
		coverage.CandidateMemberName == "" || len(coverage.CandidateMemberName) > 255 ||
		strings.ContainsAny(coverage.CandidateMemberName, "/\\") ||
		!validDigest(coverage.CandidateContentDigest) ||
		coverage.CandidateRecordCount <= 0 ||
		coverage.CandidateRecordCount > candidate.MaxRecordsPerArtifact ||
		coverage.CoveredCandidateCount != coverage.CandidateRecordCount ||
		coverage.NoDirectCandidateCount < 0 ||
		coverage.NoDirectCandidateCount > coverage.CandidateRecordCount ||
		coverage.SourceBlobBytes < 0 ||
		coverage.SourceBlobBytes > MaxSourceBlobBytesPerPair ||
		len(coverage.Gaps) > 6 ||
		(coverage.Reason != CoverageReasonNoResolverDescriptors &&
			coverage.Reason != CoverageReasonZeroCallerFacts) ||
		coverage.Schema == CoverageSchema &&
			coverage.Reason != CoverageReasonNoResolverDescriptors ||
		coverage.Reason == CoverageReasonNoResolverDescriptors &&
			coverage.SourceBlobBytes != 0 {
		return ErrInvalidArtifact
	}
	accounted := coverage.NoDirectCandidateCount
	for index, gap := range coverage.Gaps {
		if gap.Count <= 0 || !validCoverageGapReason(coverage, gap.Reason) ||
			index > 0 && coverage.Gaps[index-1].Reason >= gap.Reason ||
			gap.Count > coverage.CandidateRecordCount-accounted {
			return ErrInvalidArtifact
		}
		accounted += gap.Count
	}
	if accounted != coverage.CandidateRecordCount {
		return ErrInvalidArtifact
	}
	return nil
}

func validCoverageGapReason(coverage Coverage, reason string) bool {
	switch reason {
	case CoverageGapCatalogOwnedInput, CoverageGapDomainUnselected,
		CoverageGapExcludedGoTest, CoverageGapResolverGeneratedInput:
		return true
	case CoverageGapInvalidUTF8, CoverageGapSourceTooLarge:
		return coverage.Schema == CoverageSchemaV2 &&
			coverage.Reason == CoverageReasonZeroCallerFacts
	default:
		return false
	}
}

func coverageGapCount(coverage Coverage, reason string) int {
	for _, gap := range coverage.Gaps {
		if gap.Reason == reason {
			return gap.Count
		}
	}
	return 0
}

func validateCoverageForPair(coverage Coverage, pair PairIdentity) error {
	adapterMatches := coverage.Schema == CoverageSchema &&
		pair.LeafAdapterVersion == LeafAdapterV2 ||
		coverage.Schema == CoverageSchemaV2 &&
			pair.LeafAdapterVersion == LeafAdapterV3
	if validateCoverage(coverage) != nil || !adapterMatches ||
		coverage.PairDigest != pair.Digest ||
		coverage.CandidateMemberName != pair.Leaf.Name ||
		coverage.CandidateContentDigest != pair.Leaf.ContentDigest ||
		coverage.CandidateRecordCount != pair.Leaf.RecordCount ||
		coverage.SourceBlobBytes > pair.Leaf.DeclaredBytes {
		return fmt.Errorf("%w: coverage record differs from its pair", ErrInvalidArtifact)
	}
	return nil
}

func ValidateReceipt(generation GenerationIdentity, pair PairIdentity, receipt Receipt) error {
	_, metadataDigest, err := MetadataFor(generation, pair)
	if err != nil {
		return err
	}
	if receipt.Name != callerleafid.ArtifactName(pair.Digest, receipt.ContentDigest) ||
		receipt.RecordCount != receipt.ResultCount+receipt.AbstentionCount+
			receipt.CoverageRecordCount ||
		receipt.ResultCount < 0 || receipt.ResultCount > MaxResultRecordsPerPair ||
		receipt.AbstentionCount < 0 || receipt.AbstentionCount > MaxAbstentionRecordsPerPair ||
		receipt.CoverageRecordCount < 0 ||
		receipt.CoverageRecordCount > MaxCoverageRecordsPerPair ||
		receipt.CoveredCandidateCount < 0 ||
		receipt.CoveredCandidateCount > pair.Leaf.RecordCount ||
		receipt.ContentBytes < 0 || receipt.ContentBytes > MaxCanonicalBytesPerPair ||
		receipt.StagingBytes != receipt.ContentBytes ||
		!validDigest(receipt.ContentDigest) || receipt.MetadataDigest != metadataDigest ||
		receipt.ExcludedGoTestRecords < 0 || receipt.SourceBlobReads < 0 ||
		receipt.SourceBlobBytes < 0 || receipt.OutOfLeafReads != 0 ||
		receipt.ExcludedGoTestRecords > pair.Leaf.RecordCount ||
		receipt.SourceBlobReads > pair.Leaf.RecordCount ||
		receipt.ExcludedGoTestRecords > pair.Leaf.RecordCount-receipt.SourceBlobReads ||
		receipt.SourceBlobBytes > MaxSourceBlobBytesPerPair ||
		receipt.SourceBlobBytes > pair.Leaf.DeclaredBytes {
		return fmt.Errorf("%w: malformed receipt", ErrInvalidArtifact)
	}
	if receipt.CoverageRecordCount == 0 {
		if receipt.CoveredCandidateCount != 0 || receipt.CoverageReason != "" {
			return fmt.Errorf("%w: detached coverage receipt", ErrInvalidArtifact)
		}
		return nil
	}
	if (pair.LeafAdapterVersion != LeafAdapterV2 &&
		pair.LeafAdapterVersion != LeafAdapterV3) ||
		receipt.CoverageRecordCount != 1 ||
		receipt.ResultCount != 0 || receipt.AbstentionCount != 0 ||
		receipt.CoveredCandidateCount != pair.Leaf.RecordCount {
		return fmt.Errorf("%w: malformed compact coverage receipt", ErrInvalidArtifact)
	}
	if err := validateCompactReceiptMode(pair, receipt); err != nil {
		return err
	}
	return nil
}

func validateCompactReceiptMode(pair PairIdentity, receipt Receipt) error {
	switch pair.LeafAdapterVersion {
	case LeafAdapterV2:
		if receipt.CoverageReason != "" || receipt.SourceBlobReads != 0 ||
			receipt.SourceBlobBytes != 0 {
			return fmt.Errorf("%w: historical compact coverage has invalid source receipt", ErrInvalidArtifact)
		}
	case LeafAdapterV3:
		switch receipt.CoverageReason {
		case CoverageReasonNoResolverDescriptors:
			if receipt.SourceBlobReads != 0 || receipt.SourceBlobBytes != 0 {
				return fmt.Errorf("%w: no-resolver coverage read source blobs", ErrInvalidArtifact)
			}
		case CoverageReasonZeroCallerFacts:
		default:
			return fmt.Errorf("%w: V3 compact coverage reason is invalid", ErrInvalidArtifact)
		}
	default:
		return fmt.Errorf("%w: compact coverage adapter is invalid", ErrInvalidArtifact)
	}
	return nil
}

func validateCoverageReceipt(
	coverage Coverage,
	pair PairIdentity,
	receipt Receipt,
) error {
	if err := validateCoverageForPair(coverage, pair); err != nil {
		return err
	}
	wantReason := ""
	if pair.LeafAdapterVersion == LeafAdapterV3 {
		wantReason = coverage.Reason
	}
	if receipt.CoverageReason != wantReason {
		return fmt.Errorf("%w: compact coverage reason differs from its receipt", ErrInvalidArtifact)
	}
	switch coverage.Reason {
	case CoverageReasonNoResolverDescriptors:
		if receipt.SourceBlobReads != 0 || receipt.SourceBlobBytes != 0 ||
			coverage.SourceBlobBytes != 0 {
			return fmt.Errorf("%w: no-resolver coverage read source blobs", ErrInvalidArtifact)
		}
	case CoverageReasonZeroCallerFacts:
		if receipt.SourceBlobReads != coverage.NoDirectCandidateCount+
			coverageGapCount(coverage, CoverageGapInvalidUTF8) ||
			receipt.SourceBlobBytes != coverage.SourceBlobBytes {
			return fmt.Errorf("%w: zero-fact coverage source receipt differs", ErrInvalidArtifact)
		}
	default:
		return fmt.Errorf("%w: unknown compact coverage reason", ErrInvalidArtifact)
	}
	return nil
}

func (aggregate *AggregateReceipt) Add(receipt Receipt) error {
	if aggregate == nil {
		return errors.New("caller aggregate receipt is nil")
	}
	if aggregate.PairCount >= MaxExpectedPairs {
		return callerLimit(
			pipelinerefusal.DimensionCallerGenerationPairs,
			int64(aggregate.PairCount)+1, MaxExpectedPairs,
		)
	}
	if aggregate.ArtifactCount >= MaxExpectedPairs {
		return callerLimit(
			pipelinerefusal.DimensionCallerGenerationArtifacts,
			int64(aggregate.ArtifactCount)+1, MaxExpectedPairs,
		)
	}
	if receipt.ResultCount > MaxAggregateResultRecords-aggregate.ResultCount {
		return callerLimit(
			pipelinerefusal.DimensionCallerGenerationResults,
			int64(aggregate.ResultCount)+int64(receipt.ResultCount),
			MaxAggregateResultRecords,
		)
	}
	if receipt.AbstentionCount > MaxAggregateAbstentionRecords-aggregate.AbstentionCount {
		return callerLimit(
			pipelinerefusal.DimensionCallerGenerationAbstentions,
			int64(aggregate.AbstentionCount)+int64(receipt.AbstentionCount),
			MaxAggregateAbstentionRecords,
		)
	}
	if receipt.CoverageRecordCount >
		MaxAggregateCoverageRecords-aggregate.CoverageRecordCount ||
		receipt.CoveredCandidateCount >
			MaxAggregateCoveredCandidates-aggregate.CoveredCandidateCount {
		return ErrLimit
	}
	if receipt.ContentBytes > MaxAggregateCanonicalBytes-aggregate.CanonicalBytes {
		return callerByteLimit(
			pipelinerefusal.DimensionCallerGenerationCanonicalBytes,
			aggregate.CanonicalBytes+receipt.ContentBytes,
			MaxAggregateCanonicalBytes,
		)
	}
	if receipt.StagingBytes > MaxAggregateStagingBytes-aggregate.StagingBytes {
		return callerByteLimit(
			pipelinerefusal.DimensionCallerGenerationStagingBytes,
			aggregate.StagingBytes+receipt.StagingBytes,
			MaxAggregateStagingBytes,
		)
	}
	aggregate.PairCount++
	aggregate.ArtifactCount++
	aggregate.ResultCount += receipt.ResultCount
	aggregate.AbstentionCount += receipt.AbstentionCount
	aggregate.CoverageRecordCount += receipt.CoverageRecordCount
	aggregate.CoveredCandidateCount += receipt.CoveredCandidateCount
	aggregate.CanonicalBytes += receipt.ContentBytes
	aggregate.StagingBytes += receipt.StagingBytes
	if aggregate.PeakOpenFiles < MaxOpenFiles {
		aggregate.PeakOpenFiles = MaxOpenFiles
	}
	return nil
}

func callerLimit(
	dimension pipelinerefusal.Dimension,
	observed int64,
	limit int,
) error {
	return pipelinerefusal.Measure(ErrLimit, dimension, observed, int64(limit))
}

func callerByteLimit(
	dimension pipelinerefusal.Dimension,
	observed,
	limit int64,
) error {
	return pipelinerefusal.Measure(ErrLimit, dimension, observed, limit)
}

func generationDigest(input GenerationIdentity) (string, error) {
	if len(input.Upstream) != 0 {
		return digestFields(
			"phebs-caller-generation-v2\x00",
			input.Repository, input.HeadCommit, input.UnitDigest,
			input.DeclarationSetDigest, input.CandidateManifestDigest,
			input.CandidatePolicyDigest, input.SourceLanePolicy,
			input.ResolverGenerationDigest, input.ResolverManifestDigest,
			input.CallerPolicyDigest, input.ExtractorSetDigest, input.UpstreamDigest,
		), nil
	}
	return digestFields(
		"phebs-caller-generation-v1\x00",
		input.Repository, input.HeadCommit, input.UnitDigest,
		input.DeclarationSetDigest, input.CandidateManifestDigest,
		input.CandidatePolicyDigest, input.SourceLanePolicy,
		input.ResolverGenerationDigest, input.ResolverManifestDigest,
		input.CallerPolicyDigest, input.ExtractorSetDigest,
	), nil
}

func digestJSON(domain string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(raw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func digestFields(domain string, fields ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	for _, field := range fields {
		_, _ = fmt.Fprintf(hash, "%d:%s;", len(field), field)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validLeafDescriptor(leaf LeafDescriptor) bool {
	if leaf.Name == "" || len(leaf.Name) > 255 || strings.ContainsAny(leaf.Name, "/\\") ||
		leaf.Ordinal < 0 || leaf.PrefixBits < 2 || leaf.PrefixBits > sha256.Size*8 ||
		len(leaf.Prefix) != leaf.PrefixBits || leaf.RecordCount <= 0 ||
		leaf.RecordCount > candidate.MaxRecordsPerArtifact ||
		leaf.DeclaredBytes < 0 || leaf.DeclaredBytes > candidate.MaxDeclaredBytesPerArtifact ||
		leaf.ContentBytes <= 0 || !validDigest(leaf.ContentDigest) {
		return false
	}
	for _, value := range leaf.Prefix {
		if value != '0' && value != '1' {
			return false
		}
	}
	return true
}

func pairLess(left, right PairIdentity) bool {
	if left.Domain != right.Domain {
		return left.Domain < right.Domain
	}
	if left.Leaf.Ordinal != right.Leaf.Ordinal {
		return left.Leaf.Ordinal < right.Leaf.Ordinal
	}
	return left.Digest < right.Digest
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && hex.EncodeToString(decoded) == strings.TrimPrefix(value, "sha256:")
}

func validToken(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, current := range value {
		if current <= 0x20 || current == 0x7f || current == '/' || current == '\\' {
			return false
		}
	}
	return true
}
