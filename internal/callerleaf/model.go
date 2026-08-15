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
	GenerationSchema   = "phebs-caller-generation-v1"
	GenerationSchemaV2 = "phebs-caller-generation-v2"
	PairSchema         = "phebs-caller-leaf-pair-v1"
	RecordSchema       = "phebs-caller-leaf-record-v1"
	MetadataSchema     = "phebs-caller-leaf-metadata-v1"
	PolicyName         = "phebs-direct-caller-leaf-policy-v1"
	LeafAdapterV1      = "direct-syntax-base-v1"

	RecordResult     = "result"
	RecordAbstention = "abstention"

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
}

func FrozenPolicy() Policy {
	return Policy{
		Name: PolicyName, LeafAdapter: LeafAdapterV1,
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
}

// AggregateReceipt is the complete-generation admission input handed to
// T30.6i. It is not a visibility pointer and names no consumer surface.
type AggregateReceipt struct {
	PairCount       int   `json:"pair_count"`
	ArtifactCount   int   `json:"artifact_count"`
	ResultCount     int   `json:"result_count"`
	AbstentionCount int   `json:"abstention_count"`
	CanonicalBytes  int64 `json:"canonical_bytes"`
	StagingBytes    int64 `json:"staging_bytes"`
	PeakOpenFiles   int   `json:"peak_open_files"`
}

type Record struct {
	Schema     string               `json:"schema"`
	Kind       string               `json:"kind"`
	Path       string               `json:"path"`
	ObjectID   string               `json:"object_id"`
	SourceLane candidate.SourceLane `json:"source_lane"`
	Fact       *sdk.Fact            `json:"fact,omitempty"`
	Reason     string               `json:"reason,omitempty"`
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
	input.CallerPolicy = FrozenPolicy()
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
	for index, current := range input.Extractors {
		if !validToken(current.Domain, 128) || !validToken(current.Version, 64) ||
			current.LeafAdapterVersion != LeafAdapterV1 ||
			index > 0 && input.Extractors[index-1].Domain >= current.Domain {
			return GenerationIdentity{}, errors.New("caller generation extractor set is invalid or unordered")
		}
	}
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
	found := false
	for _, current := range generation.Extractors {
		if current.Domain == domain && current.Version == version {
			found = true
			break
		}
	}
	if !found || !validLeafDescriptor(leaf) {
		return PairIdentity{}, errors.New("caller pair is outside its generation")
	}
	pair := PairIdentity{
		Schema: PairSchema, GenerationDigest: generation.Digest,
		Domain: domain, ExtractorVersion: version,
		LeafAdapterVersion: LeafAdapterV1, Leaf: leaf,
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
	if record.Schema != RecordSchema || repopath.Validate(record.Path) != nil ||
		!gitobj.IsObjectID(record.ObjectID) || record.SourceLane != candidate.SourceLaneBase {
		return fmt.Errorf("%w: malformed record envelope", ErrInvalidArtifact)
	}
	switch record.Kind {
	case RecordResult:
		if record.Fact == nil || record.Reason != "" || record.Fact.Path != record.Path ||
			record.Fact.Atom.BlobDigest == "" ||
			!validDigest(record.Fact.Atom.BlobDigest) ||
			record.Fact.Atom.StartByte < 0 ||
			record.Fact.Atom.EndByte <= record.Fact.Atom.StartByte ||
			record.Fact.Assertion.Predicate == "" {
			return fmt.Errorf("%w: malformed result record", ErrInvalidArtifact)
		}
	case RecordAbstention:
		if !validToken(record.Reason, 256) ||
			record.Fact != nil && (record.Fact.Path != record.Path ||
				record.Fact.Assertion.Predicate != "UNRESOLVED_CALLER" ||
				!validDigest(record.Fact.Atom.BlobDigest) ||
				record.Fact.Atom.StartByte < 0 ||
				record.Fact.Atom.EndByte <= record.Fact.Atom.StartByte) {
			return fmt.Errorf("%w: malformed abstention record", ErrInvalidArtifact)
		}
	default:
		return fmt.Errorf("%w: unknown record kind", ErrInvalidArtifact)
	}
	return nil
}

func ValidateReceipt(generation GenerationIdentity, pair PairIdentity, receipt Receipt) error {
	_, metadataDigest, err := MetadataFor(generation, pair)
	if err != nil {
		return err
	}
	if receipt.Name != callerleafid.ArtifactName(pair.Digest, receipt.ContentDigest) ||
		receipt.RecordCount != receipt.ResultCount+receipt.AbstentionCount ||
		receipt.ResultCount < 0 || receipt.ResultCount > MaxResultRecordsPerPair ||
		receipt.AbstentionCount < 0 || receipt.AbstentionCount > MaxAbstentionRecordsPerPair ||
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
