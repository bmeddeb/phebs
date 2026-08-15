package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/reponame"
)

const (
	// CallerLeafWriterSchema is persisted on every T30.6h state row. Filesystem
	// artifact schemas are separate; this names only the store writer contract.
	CallerLeafWriterSchema = "phebs-caller-leaf-store-v1"
	// CallerBaseSourceLanePolicy names T30.6h's base-only execution generation.
	// Retained go_test candidate records require a separately authorized future
	// generation and cannot alias this identity.
	CallerBaseSourceLanePolicy = "candidate-source-lane-base-v1"
	callerLeafAdapterV2        = "direct-syntax-compact-coverage-v2"

	callerLeafWriterMigrationVersion = "t30.6h-caller-leaf-writer-v1"

	maxCallerCandidateRecords            = 4096
	maxCallerCandidateDeclaredBytes      = int64(64 << 20)
	maxCallerCandidateContentBytes       = int64(128 << 20)
	maxCallerPairResults                 = 12_500
	maxCallerPairAbstentions             = 4096
	maxCallerPairCanonicalBytes          = int64(64 << 20)
	maxCallerPairStagingBytes            = int64(65 << 20)
	maxCallerPairSourceBytes             = int64(64 << 20)
	maxCallerPairCoverageRecords         = 1
	maxCallerPairCoveredCandidates       = maxCallerCandidateRecords
	maxCallerGenerationPairs             = 16_384
	maxCallerGenerationResults           = 100_000
	maxCallerGenerationAbstentions       = 100_000
	maxCallerGenerationCanonical         = int64(512 << 20)
	maxCallerGenerationStaging           = int64(520 << 20)
	maxCallerGenerationCoverageRecords   = maxCallerGenerationPairs
	maxCallerGenerationCoveredCandidates = maxCallerGenerationPairs * maxCallerCandidateRecords
	maxCallerPeakOpenFiles               = 5
	maxCallerRefusalSummaries            = 32
)

var ErrInvalidCallerLeafState = errors.New("invalid caller-leaf state")

// CallerGenerationIdentity is the complete immutable input generation for
// direct caller-leaf execution. T30.6h records per-pair work against it;
// T30.6i may coordinate those rows but owns the product-visible publication.
// Digest is always recomputed by this package.
type CallerGenerationIdentity struct {
	Repository               string `json:"repository"`
	HeadCommit               string `json:"head_commit"`
	UnitDigest               string `json:"unit_digest"`
	DeclarationSetDigest     string `json:"declaration_set_digest"`
	CandidateManifestDigest  string `json:"candidate_manifest_digest"`
	CandidatePolicyDigest    string `json:"candidate_policy_digest"`
	CandidateControlRevision uint64 `json:"candidate_control_revision"`
	ResolverGenerationDigest string `json:"resolver_generation_digest"`
	ResolverManifestDigest   string `json:"resolver_manifest_digest"`
	ResolverControlRevision  uint64 `json:"resolver_control_revision"`
	ResolverWriterSchema     string `json:"resolver_writer_schema"`
	SourceLanePolicy         string `json:"source_lane_policy"`
	CallerPolicyDigest       string `json:"caller_policy_digest"`
	ExtractorSetDigest       string `json:"extractor_set_digest"`
	UpstreamDigest           string `json:"upstream_digest,omitempty"`
	Digest                   string `json:"digest"`
}

// ComputeCallerGenerationDigest is the sole store-side generation digest.
func ComputeCallerGenerationDigest(identity CallerGenerationIdentity) string {
	if identity.UpstreamDigest != "" {
		return callerLeafDigestFields(
			"phebs-caller-generation-v2\x00",
			identity.Repository, identity.HeadCommit, identity.UnitDigest,
			identity.DeclarationSetDigest, identity.CandidateManifestDigest,
			identity.CandidatePolicyDigest, identity.SourceLanePolicy,
			identity.ResolverGenerationDigest, identity.ResolverManifestDigest,
			identity.CallerPolicyDigest, identity.ExtractorSetDigest, identity.UpstreamDigest,
		)
	}
	return callerLeafDigestFields(
		"phebs-caller-generation-v1\x00",
		identity.Repository,
		identity.HeadCommit,
		identity.UnitDigest,
		identity.DeclarationSetDigest,
		identity.CandidateManifestDigest,
		identity.CandidatePolicyDigest,
		identity.SourceLanePolicy,
		identity.ResolverGenerationDigest,
		identity.ResolverManifestDigest,
		identity.CallerPolicyDigest,
		identity.ExtractorSetDigest,
	)
}

// CallerLeafPair is the exact candidate leaf descriptor assigned to one
// caller-domain adapter. Its cached PairDigest is never trusted on write.
type CallerLeafPair struct {
	Domain                 string `json:"domain"`
	ExtractorVersion       string `json:"extractor_version"`
	LeafAdapterVersion     string `json:"leaf_adapter_version"`
	LeafOrdinal            int    `json:"leaf_ordinal"`
	LeafPrefix             string `json:"leaf_prefix"`
	LeafPrefixBits         int    `json:"leaf_prefix_bits"`
	CandidateMemberName    string `json:"candidate_member_name"`
	CandidateRecordCount   int    `json:"candidate_record_count"`
	CandidateDeclaredBytes int64  `json:"candidate_declared_bytes"`
	CandidateContentBytes  int64  `json:"candidate_content_bytes"`
	CandidateContentDigest string `json:"candidate_content_digest"`
	PairDigest             string `json:"digest"`
}

// ComputeCallerLeafPairDigest binds the domain, adapter, and complete
// candidate-member envelope, preventing a descriptor swap from aliasing a
// previously successful pair.
func ComputeCallerLeafPairDigest(
	generation CallerGenerationIdentity,
	pair CallerLeafPair,
) string {
	return callerLeafDigestFields(
		"phebs-caller-leaf-pair-v1\x00",
		ComputeCallerGenerationDigest(generation),
		pair.Domain,
		pair.ExtractorVersion,
		pair.LeafAdapterVersion,
		strconv.Itoa(pair.LeafOrdinal),
		pair.LeafPrefix,
		strconv.Itoa(pair.LeafPrefixBits),
		pair.CandidateMemberName,
		strconv.Itoa(pair.CandidateRecordCount),
		strconv.FormatInt(pair.CandidateDeclaredBytes, 10),
		strconv.FormatInt(pair.CandidateContentBytes, 10),
		pair.CandidateContentDigest,
	)
}

// ComputeCallerLeafPairSetDigest validates canonical pair order through the
// same path as admission and returns a digest over every exact pair envelope.
func ComputeCallerLeafPairSetDigest(
	generation CallerGenerationIdentity,
	pairs []CallerLeafPair,
) (string, error) {
	preparedGeneration, err := prepareCallerGeneration(generation)
	if err != nil {
		return "", err
	}
	prepared, err := prepareCallerLeafPairs(preparedGeneration, pairs)
	if err != nil {
		return "", err
	}
	fields := make([]string, len(prepared))
	for index := range prepared {
		fields[index] = prepared[index].PairDigest
	}
	return callerLeafDigestFields("phebs-caller-leaf-pair-set-v1\x00", fields...), nil
}

func callerLeafDigestFields(domain string, fields ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	for _, field := range fields {
		_, _ = fmt.Fprintf(hash, "%d:%s;", len(field), field)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type CallerLeafDisposition string

const (
	CallerLeafSucceeded                 CallerLeafDisposition = "succeeded"
	CallerLeafTerminalGenerationRefusal CallerLeafDisposition = "terminal_generation_refusal"
)

// CallerLeafArtifactReceipt is the exact immutable artifact envelope for one
// successful pair. Empty and abstention-only pairs still publish one artifact.
type CallerLeafArtifactReceipt struct {
	ArtifactName          string `json:"artifact_name"`
	ArtifactCount         int    `json:"artifact_count"`
	ResultCount           int    `json:"result_count"`
	AbstentionCount       int    `json:"abstention_count"`
	CanonicalBytes        int64  `json:"canonical_bytes"`
	StagingBytes          int64  `json:"staging_bytes"`
	ContentDigest         string `json:"content_digest"`
	MetadataDigest        string `json:"metadata_digest"`
	ExcludedGoTestRecords int    `json:"excluded_go_test_records"`
	SourceBlobReads       int    `json:"source_blob_reads"`
	SourceBlobBytes       int64  `json:"source_blob_bytes"`
	OutOfLeafReads        int    `json:"out_of_leaf_reads"`
	CoverageRecordCount   int    `json:"coverage_record_count,omitempty"`
	CoveredCandidateCount int    `json:"covered_candidate_count,omitempty"`
}

// CallerLeafOutcome is immutable multi-generation execution state. RecordedAt
// and WriterSchema are store-owned on write.
type CallerLeafOutcome struct {
	Generation   CallerGenerationIdentity   `json:"generation"`
	Pair         CallerLeafPair             `json:"pair"`
	Disposition  CallerLeafDisposition      `json:"disposition"`
	Receipt      *CallerLeafArtifactReceipt `json:"receipt,omitempty"`
	Refusal      *pipelinerefusal.Receipt   `json:"refusal,omitempty"`
	WriterSchema string                     `json:"writer_schema"`
	RecordedAt   time.Time                  `json:"recorded_at"`
}

// CallerGenerationRefusalSummary is a bounded aggregation over exact typed
// pair refusals. OutcomeCount normally counts pair outcomes carrying the same
// closed receipt. When more than 32 distinct receipts exist, the last slot is
// an explicit generation-admission unknown that counts the omitted groups;
// zero is reserved for a generation-wide aggregate refusal reached by the
// final successful pair.
type CallerGenerationRefusalSummary struct {
	Refusal      pipelinerefusal.Receipt `json:"refusal"`
	OutcomeCount int                     `json:"outcome_count"`
}

// CallerLeafOutcomeProgress is the bounded scalar projection of all durable
// pair outcomes for one exact caller generation. It deliberately carries no
// pair descriptors or receipts, so product status reads cannot materialize up
// to maxCallerGenerationPairs rows merely to report settlement progress.
type CallerLeafOutcomeProgress struct {
	SettledCount   int `json:"settled_count"`
	SucceededCount int `json:"succeeded_count"`
	RefusedCount   int `json:"refused_count"`
}

type CallerGenerationAdmissionDisposition string

const (
	CallerGenerationAdmitted                  CallerGenerationAdmissionDisposition = "admitted"
	CallerGenerationTerminalGenerationRefusal CallerGenerationAdmissionDisposition = "terminal_generation_refusal"
)

// CallerGenerationAdmission is aggregate pre-publication state. Pair and
// aggregate fields are recomputed from immutable outcomes; PeakOpenFiles is
// the frozen structural worker maximum, not an operating-system FD meter. It
// creates no caller publication pointer.
type CallerGenerationAdmission struct {
	Generation            CallerGenerationIdentity             `json:"generation"`
	Disposition           CallerGenerationAdmissionDisposition `json:"disposition"`
	PairSetDigest         string                               `json:"pair_set_digest"`
	PairCount             int                                  `json:"pair_count"`
	ArtifactCount         int                                  `json:"artifact_count"`
	ResultCount           int                                  `json:"result_count"`
	AbstentionCount       int                                  `json:"abstention_count"`
	CanonicalBytes        int64                                `json:"canonical_bytes"`
	StagingBytes          int64                                `json:"staging_bytes"`
	PeakOpenFiles         int                                  `json:"peak_open_files"`
	CoverageRecordCount   int                                  `json:"coverage_record_count,omitempty"`
	CoveredCandidateCount int                                  `json:"covered_candidate_count,omitempty"`
	Refusals              []CallerGenerationRefusalSummary     `json:"refusals"`
	WriterSchema          string                               `json:"writer_schema"`
	RecordedAt            time.Time                            `json:"recorded_at"`
}

type CallerLeafStore interface {
	RecordCallerLeafOutcome(context.Context, Job, CallerLeafOutcome) error
	GetCallerLeafOutcome(context.Context, CallerGenerationIdentity, CallerLeafPair) (*CallerLeafOutcome, error)
	ListCallerLeafOutcomes(context.Context, CallerGenerationIdentity) ([]CallerLeafOutcome, error)
	GetCallerLeafOutcomeProgress(context.Context, CallerGenerationIdentity) (CallerLeafOutcomeProgress, error)
	RecordCallerGenerationAdmission(context.Context, Job, CallerGenerationAdmission, []CallerLeafPair) error
	GetCallerGenerationAdmission(context.Context, CallerGenerationIdentity) (*CallerGenerationAdmission, error)
	ListCallerGenerationAdmissions(context.Context, string) ([]CallerGenerationAdmission, error)
	ClearCallerLeafOutcome(context.Context, CallerGenerationIdentity, CallerLeafPair) error
	ClearCallerLeafGeneration(context.Context, CallerGenerationIdentity) error
	ClearAllCallerLeafState(context.Context, string) error
	ClearAllCallerLeafStateForRestore(context.Context) error
}

var _ CallerLeafStore = (*Surreal)(nil)

func callerLeafMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "caller_leaf_writer")
}

func callerLeafOutcomeID(generation CallerGenerationIdentity, pair CallerLeafPair) models.RecordID {
	return models.NewRecordID("caller_leaf_outcome", hashIdentity(
		"clo_", generation.Repository, generation.Digest, pair.PairDigest,
	))
}

func callerGenerationAdmissionID(generation CallerGenerationIdentity) models.RecordID {
	return models.NewRecordID("caller_generation_admission", hashIdentity(
		"cga_", generation.Repository, generation.Digest,
	))
}

func (s *Surreal) migrateCallerLeafWriter(ctx context.Context) error {
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $marker LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted {
	THROW 'phebs-permanent: unsupported caller-leaf writer generation'
};
DELETE caller_leaf_outcome
	WHERE $current = NONE AND (writer_schema ?? '') != $writer_schema RETURN NONE;
DELETE caller_generation_admission
	WHERE $current = NONE AND (writer_schema ?? '') != $writer_schema RETURN NONE;
UPSERT $marker SET
	version = IF $current = NONE THEN $wanted ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"marker": callerLeafMigrationID(), "wanted": callerLeafWriterMigrationVersion,
		"writer_schema": CallerLeafWriterSchema,
	})
	if err != nil {
		return fmt.Errorf("migrate caller-leaf writer: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf("migrate caller-leaf writer statement %d: %s", index, result.Error.Message)
		}
	}
	return nil
}

func prepareCallerGeneration(identity CallerGenerationIdentity) (CallerGenerationIdentity, error) {
	if err := reponame.Validate(identity.Repository); err != nil {
		return identity, fmt.Errorf("repository: %w", err)
	}
	if !validGitObjectID(identity.HeadCommit) {
		return identity, errors.New("head_commit must be a canonical object ID")
	}
	if identity.UnitDigest != "" && !validSHA256Digest(identity.UnitDigest) {
		return identity, errors.New("unit_digest must be empty or canonical sha256")
	}
	for name, digest := range map[string]string{
		"declaration_set_digest":     identity.DeclarationSetDigest,
		"candidate_manifest_digest":  identity.CandidateManifestDigest,
		"candidate_policy_digest":    identity.CandidatePolicyDigest,
		"resolver_generation_digest": identity.ResolverGenerationDigest,
		"resolver_manifest_digest":   identity.ResolverManifestDigest,
		"caller_policy_digest":       identity.CallerPolicyDigest,
		"extractor_set_digest":       identity.ExtractorSetDigest,
	} {
		if !validSHA256Digest(digest) {
			return identity, fmt.Errorf("%s must be canonical sha256", name)
		}
	}
	if identity.UpstreamDigest != "" && !validSHA256Digest(identity.UpstreamDigest) {
		return identity, errors.New("upstream_digest must be empty or canonical sha256")
	}
	if identity.CandidateControlRevision == 0 || identity.ResolverControlRevision == 0 {
		return identity, errors.New("candidate and resolver control revisions are required")
	}
	if identity.ResolverWriterSchema != resolverCatalogWriterSchema {
		return identity, errors.New("resolver writer schema is incompatible")
	}
	if identity.SourceLanePolicy != CallerBaseSourceLanePolicy {
		return identity, errors.New("source lane policy must be candidate-source-lane-base-v1")
	}
	identity.Digest = ComputeCallerGenerationDigest(identity)
	return identity, nil
}

func sameCallerGenerationSemantics(left, right CallerGenerationIdentity) bool {
	return ComputeCallerGenerationDigest(left) == ComputeCallerGenerationDigest(right)
}

func prepareCallerLeafPair(
	generation CallerGenerationIdentity,
	pair CallerLeafPair,
) (CallerLeafPair, error) {
	if !validCallerToken(pair.Domain, 128) ||
		!validCallerToken(pair.ExtractorVersion, 128) ||
		!validCallerToken(pair.LeafAdapterVersion, 128) {
		return pair, errors.New("domain or adapter version is missing or unbounded")
	}
	if pair.LeafOrdinal < 0 || pair.LeafOrdinal > maxCallerGenerationPairs {
		return pair, errors.New("leaf ordinal is outside its bound")
	}
	if pair.LeafPrefixBits < 2 || pair.LeafPrefixBits > 256 ||
		len(pair.LeafPrefix) != pair.LeafPrefixBits {
		return pair, errors.New("leaf prefix has an invalid bit length")
	}
	for _, value := range pair.LeafPrefix {
		if value != '0' && value != '1' {
			return pair, errors.New("leaf prefix is not binary")
		}
	}
	if !validCallerArtifactName(pair.CandidateMemberName) ||
		!strings.HasSuffix(pair.CandidateMemberName, ".ndjson") {
		return pair, errors.New("candidate member name is invalid")
	}
	if pair.CandidateRecordCount <= 0 || pair.CandidateRecordCount > maxCallerCandidateRecords ||
		pair.CandidateDeclaredBytes < 0 || pair.CandidateDeclaredBytes > maxCallerCandidateDeclaredBytes ||
		pair.CandidateContentBytes <= 0 || pair.CandidateContentBytes > maxCallerCandidateContentBytes ||
		!validSHA256Digest(pair.CandidateContentDigest) {
		return pair, errors.New("candidate member envelope is invalid")
	}
	pair.PairDigest = ComputeCallerLeafPairDigest(generation, pair)
	return pair, nil
}

func prepareCallerLeafPairs(
	generation CallerGenerationIdentity,
	pairs []CallerLeafPair,
) ([]CallerLeafPair, error) {
	if pairs == nil || len(pairs) > maxCallerGenerationPairs {
		return nil, errors.New("caller leaf pairs must be an explicit bounded set")
	}
	prepared := make([]CallerLeafPair, len(pairs))
	for index := range pairs {
		var err error
		prepared[index], err = prepareCallerLeafPair(generation, pairs[index])
		if err != nil {
			return nil, fmt.Errorf("pair %d: %w", index, err)
		}
		if index > 0 && !callerPairLess(prepared[index-1], prepared[index]) {
			return nil, fmt.Errorf("pair %d is duplicate or unordered", index)
		}
	}
	return prepared, nil
}

func callerPairLess(left, right CallerLeafPair) bool {
	if left.Domain != right.Domain {
		return left.Domain < right.Domain
	}
	return left.LeafPrefix < right.LeafPrefix
}

func validCallerToken(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) ||
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

func validCallerArtifactName(value string) bool {
	return validCallerToken(value, 255) && value != "." && value != ".."
}

func validateCallerReceipt(receipt CallerLeafArtifactReceipt) error {
	if !validCallerArtifactName(receipt.ArtifactName) ||
		!strings.HasSuffix(receipt.ArtifactName, ".ndjson") {
		return errors.New("artifact name is invalid")
	}
	if receipt.ArtifactCount != 1 {
		return errors.New("a successful pair must name exactly one artifact")
	}
	if receipt.ResultCount < 0 || receipt.ResultCount > maxCallerPairResults ||
		receipt.AbstentionCount < 0 || receipt.AbstentionCount > maxCallerPairAbstentions ||
		receipt.CoverageRecordCount < 0 ||
		receipt.CoverageRecordCount > maxCallerPairCoverageRecords ||
		receipt.CoveredCandidateCount < 0 ||
		receipt.CoveredCandidateCount > maxCallerPairCoveredCandidates {
		return errors.New("result or abstention count is outside its bound")
	}
	if receipt.CanonicalBytes < 0 || receipt.CanonicalBytes > maxCallerPairCanonicalBytes ||
		receipt.StagingBytes != receipt.CanonicalBytes ||
		receipt.StagingBytes > maxCallerPairStagingBytes {
		return errors.New("artifact byte receipt is outside its bound")
	}
	if !validSHA256Digest(receipt.ContentDigest) || !validSHA256Digest(receipt.MetadataDigest) {
		return errors.New("artifact digests must be canonical sha256")
	}
	if receipt.ExcludedGoTestRecords < 0 || receipt.SourceBlobReads < 0 ||
		receipt.SourceBlobBytes < 0 || receipt.SourceBlobBytes > maxCallerPairSourceBytes ||
		receipt.OutOfLeafReads != 0 {
		return errors.New("source-read receipt is invalid or contains an out-of-leaf read")
	}
	return nil
}

func prepareCallerLeafOutcome(outcome CallerLeafOutcome) (CallerLeafOutcome, error) {
	var err error
	outcome.Generation, err = prepareCallerGeneration(outcome.Generation)
	if err != nil {
		return outcome, fmt.Errorf("generation: %w", err)
	}
	outcome.Pair, err = prepareCallerLeafPair(outcome.Generation, outcome.Pair)
	if err != nil {
		return outcome, fmt.Errorf("pair: %w", err)
	}
	switch outcome.Disposition {
	case CallerLeafSucceeded:
		if outcome.Receipt == nil || outcome.Refusal != nil {
			return outcome, errors.New("successful caller leaf requires an artifact receipt")
		}
		if err := validateCallerReceipt(*outcome.Receipt); err != nil {
			return outcome, fmt.Errorf("receipt: %w", err)
		}
		if outcome.Receipt.ArtifactName != callerleafid.ArtifactName(
			outcome.Pair.PairDigest, outcome.Receipt.ContentDigest,
		) {
			return outcome, errors.New("receipt artifact name is outside its exact pair")
		}
		if outcome.Receipt.ExcludedGoTestRecords > outcome.Pair.CandidateRecordCount ||
			outcome.Receipt.SourceBlobReads > outcome.Pair.CandidateRecordCount ||
			outcome.Receipt.ExcludedGoTestRecords >
				outcome.Pair.CandidateRecordCount-outcome.Receipt.SourceBlobReads ||
			outcome.Receipt.SourceBlobBytes > outcome.Pair.CandidateDeclaredBytes {
			return outcome, errors.New("source-read receipt exceeds its candidate leaf")
		}
		if outcome.Receipt.CoverageRecordCount == 0 {
			if outcome.Receipt.CoveredCandidateCount != 0 {
				return outcome, errors.New("success receipt has detached coverage")
			}
		} else if outcome.Pair.LeafAdapterVersion != callerLeafAdapterV2 ||
			outcome.Receipt.CoverageRecordCount != 1 ||
			outcome.Receipt.CoveredCandidateCount != outcome.Pair.CandidateRecordCount ||
			outcome.Receipt.ResultCount != 0 || outcome.Receipt.AbstentionCount != 0 ||
			outcome.Receipt.SourceBlobReads != 0 || outcome.Receipt.SourceBlobBytes != 0 {
			return outcome, errors.New("success receipt has invalid compact coverage")
		}
	case CallerLeafTerminalGenerationRefusal:
		if outcome.Receipt != nil {
			return outcome, errors.New("terminal caller leaf cannot carry an artifact receipt")
		}
		if outcome.Refusal == nil {
			unknown := pipelinerefusal.Receipt{
				Schema:         pipelinerefusal.Schema,
				Stage:          pipelinerefusal.StageCallerPairExecution,
				GenerationKind: pipelinerefusal.GenerationCaller,
				Classification: pipelinerefusal.ClassificationUnknown,
				Dimension:      pipelinerefusal.DimensionUnknown,
			}
			outcome.Refusal = &unknown
		}
		if pipelinerefusal.Validate(*outcome.Refusal) != nil ||
			outcome.Refusal.GenerationKind != pipelinerefusal.GenerationCaller {
			return outcome, errors.New("terminal caller leaf has an invalid refusal")
		}
		owned := *outcome.Refusal
		outcome.Refusal = &owned
	default:
		return outcome, errors.New("caller leaf disposition is not in the frozen set")
	}
	outcome.WriterSchema = CallerLeafWriterSchema
	outcome.RecordedAt = time.Time{}
	return outcome, nil
}

func validateCallerJob(job Job, repository string) error {
	recordID, recordErr := models.ParseRecordID(job.ID)
	if job.Kind != JobCallerLeaf || job.Target != repository || job.ID == "" ||
		job.LeaseToken == "" || job.ClaimedBy == "" ||
		(job.Status != StatusClaimed && job.Status != StatusRunning) ||
		recordErr != nil || recordID.Table != string(JobCallerLeaf) {
		return fmt.Errorf("caller leaf job %q: %w", job.ID, ErrLeaseLost)
	}
	return nil
}

type callerLeafOutcomeRec struct {
	CallerLeafOutcome
	Repository       string           `json:"repository"`
	GenerationDigest string           `json:"generation_digest"`
	Domain           string           `json:"domain"`
	LeafPrefix       string           `json:"leaf_prefix"`
	RecID            *models.RecordID `json:"id"`
}

const recordCallerLeafOutcomeSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $repo = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $candidate = (SELECT head_commit, unit_digest, manifest_digest,
	policy_digest, control_revision
	FROM $candidate_rid)[0];
LET $resolver = (SELECT head_commit, unit_digest, candidate_manifest_digest,
	declaration_set_digest, generation_digest, manifest_digest,
	control_revision, writer_schema FROM $resolver_rid)[0];
LET $owned = (SELECT id FROM type::record($job_id)
	WHERE target = $repository AND status IN ['claimed', 'running']
		AND lease_token = $lease AND claimed_by = $claimed_by LIMIT 1)[0].id;
LET $current = (SELECT * FROM $outcome_rid)[0];
LET $normalized_receipt = IF $receipt = NULL THEN NONE ELSE $receipt END;
LET $normalized_refusal = IF $refusal = NULL THEN NONE ELSE $refusal END;
LET $authority_ok = $writer_ok AND $owned != NONE AND $repo != NONE
	AND ($repo.deleting = NONE OR $repo.deleting = false)
	AND $repo.indexed_commit_hash = $head_commit
	AND ($repo.indexed_analysis_unit.digest ?? '') = $unit_digest
	AND $candidate != NONE
	AND $candidate.head_commit = $head_commit
	AND $candidate.unit_digest = $unit_digest
	AND $candidate.manifest_digest = $candidate_manifest_digest
	AND $candidate.policy_digest = $candidate_policy_digest
	AND $candidate.control_revision = $candidate_control_revision
	AND $resolver != NONE
	AND $resolver.head_commit = $head_commit
	AND $resolver.unit_digest = $unit_digest
	AND $resolver.candidate_manifest_digest = $candidate_manifest_digest
	AND $resolver.declaration_set_digest = $declaration_set_digest
	AND $resolver.generation_digest = $resolver_generation_digest
	AND $resolver.manifest_digest = $resolver_manifest_digest
	AND $resolver.control_revision = $resolver_control_revision
	AND $resolver.writer_schema = $resolver_writer_schema;
LET $same = $current != NONE
	AND $current.repository = $repository
	AND $current.generation_digest = $generation_digest
	AND $current.pair.digest = $pair.digest
	AND $current.domain = $domain
	AND $current.leaf_prefix = $leaf_prefix
	AND $current.disposition = $disposition
	AND $current.receipt = $normalized_receipt
	AND $current.refusal = $normalized_refusal
	AND $current.writer_schema = $writer_schema;
LET $written = IF $authority_ok = false THEN []
	ELSE IF $current != NONE AND $same = false THEN []
	ELSE IF $same THEN [$current]
	ELSE (CREATE $outcome_rid CONTENT {
		repository: $repository,
		generation: $generation,
		generation_digest: $generation_digest,
		pair: $pair,
		domain: $domain,
		leaf_prefix: $leaf_prefix,
		disposition: $disposition,
		receipt: $normalized_receipt,
		refusal: $normalized_refusal,
		writer_schema: $writer_schema,
		recorded_at: time::now()
	} RETURN AFTER) END;
LET $pending = IF array::len($written) = 1 THEN
	(SELECT id, created_at FROM caller_leaf_job
		WHERE pending_key = $repository AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $successor = IF array::len($written) != 1 THEN []
	ELSE IF $pending != NONE THEN
		(UPDATE $pending SET force = force RETURN AFTER)
	ELSE (CREATE caller_leaf_job CONTENT {
		target: $repository,
		status: 'pending',
		attempts: 0,
		created_at: time::now(),
		pending_key: $repository,
		force: false,
		recovery_lease: $lease
	} RETURN AFTER) END;
RETURN IF array::len($written) = 1 AND array::len($successor) = 1
	THEN $written ELSE [] END;
COMMIT;`

func (s *Surreal) RecordCallerLeafOutcome(ctx context.Context, job Job, outcome CallerLeafOutcome) error {
	prepared, err := prepareCallerLeafOutcome(outcome)
	if err != nil {
		return fmt.Errorf("record caller leaf outcome: %w: %v", ErrInvalidCallerLeafState, err)
	}
	if err := validateCallerJob(job, prepared.Generation.Repository); err != nil {
		return fmt.Errorf("record caller leaf outcome: %w", err)
	}
	results, err := surrealdb.Query[[]callerLeafOutcomeRec](ctx, s.db, recordCallerLeafOutcomeSQL, map[string]any{
		"migration_rid":     callerLeafMigrationID(),
		"migration_version": callerLeafWriterMigrationVersion,
		"repo_rid":          repoID(prepared.Generation.Repository),
		"candidate_rid":     candidateManifestPublicationID(prepared.Generation.Repository),
		"resolver_rid":      resolverCatalogPublicationID(prepared.Generation.Repository),
		"outcome_rid":       callerLeafOutcomeID(prepared.Generation, prepared.Pair),
		"job_id":            job.ID, "lease": job.LeaseToken, "claimed_by": job.ClaimedBy,
		"repository":                 prepared.Generation.Repository,
		"head_commit":                prepared.Generation.HeadCommit,
		"unit_digest":                prepared.Generation.UnitDigest,
		"declaration_set_digest":     prepared.Generation.DeclarationSetDigest,
		"candidate_manifest_digest":  prepared.Generation.CandidateManifestDigest,
		"candidate_policy_digest":    prepared.Generation.CandidatePolicyDigest,
		"candidate_control_revision": prepared.Generation.CandidateControlRevision,
		"resolver_generation_digest": prepared.Generation.ResolverGenerationDigest,
		"resolver_manifest_digest":   prepared.Generation.ResolverManifestDigest,
		"resolver_control_revision":  prepared.Generation.ResolverControlRevision,
		"resolver_writer_schema":     prepared.Generation.ResolverWriterSchema,
		"generation":                 prepared.Generation,
		"generation_digest":          prepared.Generation.Digest,
		"pair":                       prepared.Pair,
		"domain":                     prepared.Pair.Domain,
		"leaf_prefix":                prepared.Pair.LeafPrefix,
		"disposition":                prepared.Disposition,
		"receipt":                    prepared.Receipt,
		"refusal":                    prepared.Refusal,
		"writer_schema":              CallerLeafWriterSchema,
	})
	if err != nil {
		return fmt.Errorf("record caller leaf outcome: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return fmt.Errorf("record caller leaf outcome: stale or conflicting authority: %w", ErrConflict)
	}
	if _, err := validatePersistedCallerLeafOutcome(rows[0]); err != nil {
		return fmt.Errorf("record caller leaf outcome: persisted row: %w", err)
	}
	return nil
}

func validatePersistedCallerLeafOutcome(row callerLeafOutcomeRec) (CallerLeafOutcome, error) {
	originalGenerationDigest := row.Generation.Digest
	originalPairDigest := row.Pair.PairDigest
	originalRefusal := row.Refusal
	prepared, err := prepareCallerLeafOutcome(row.CallerLeafOutcome)
	if err != nil {
		return CallerLeafOutcome{}, err
	}
	if originalGenerationDigest != prepared.Generation.Digest ||
		originalPairDigest != prepared.Pair.PairDigest ||
		row.GenerationDigest != prepared.Generation.Digest ||
		row.Repository != prepared.Generation.Repository ||
		row.Domain != prepared.Pair.Domain || row.LeafPrefix != prepared.Pair.LeafPrefix ||
		prepared.Disposition == CallerLeafTerminalGenerationRefusal && originalRefusal == nil ||
		row.WriterSchema != CallerLeafWriterSchema || row.RecordedAt.IsZero() {
		return CallerLeafOutcome{}, ErrInvalidCallerLeafState
	}
	prepared.RecordedAt = row.RecordedAt.UTC()
	prepared.WriterSchema = row.WriterSchema
	return prepared, nil
}

func (s *Surreal) GetCallerLeafOutcome(ctx context.Context, generation CallerGenerationIdentity, pair CallerLeafPair) (*CallerLeafOutcome, error) {
	preparedGeneration, err := prepareCallerGeneration(generation)
	if err != nil {
		return nil, fmt.Errorf("get caller leaf outcome: %w", err)
	}
	preparedPair, err := prepareCallerLeafPair(preparedGeneration, pair)
	if err != nil {
		return nil, fmt.Errorf("get caller leaf outcome: %w", err)
	}
	results, err := surrealdb.Query[[]callerLeafOutcomeRec](ctx, s.db,
		"SELECT * FROM $rid", map[string]any{"rid": callerLeafOutcomeID(preparedGeneration, preparedPair)})
	if err != nil {
		return nil, fmt.Errorf("get caller leaf outcome: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("caller leaf outcome: %w", ErrNotFound)
	}
	outcome, err := validatePersistedCallerLeafOutcome(rows[0])
	if err != nil || !sameCallerGenerationSemantics(outcome.Generation, preparedGeneration) ||
		outcome.Pair.PairDigest != preparedPair.PairDigest {
		return nil, fmt.Errorf("get caller leaf outcome: %w", ErrInvalidCallerLeafState)
	}
	outcome.Generation = preparedGeneration
	outcome.Pair = preparedPair
	return &outcome, nil
}

func (s *Surreal) ListCallerLeafOutcomes(ctx context.Context, generation CallerGenerationIdentity) ([]CallerLeafOutcome, error) {
	preparedGeneration, err := prepareCallerGeneration(generation)
	if err != nil {
		return nil, fmt.Errorf("list caller leaf outcomes: %w", err)
	}
	results, err := surrealdb.Query[[]callerLeafOutcomeRec](ctx, s.db,
		`SELECT * FROM caller_leaf_outcome
		 WHERE repository = $repository AND generation_digest = $generation_digest
		 ORDER BY domain, leaf_prefix`, map[string]any{
			"repository":        preparedGeneration.Repository,
			"generation_digest": preparedGeneration.Digest,
		})
	if err != nil {
		return nil, fmt.Errorf("list caller leaf outcomes: %w", err)
	}
	rows := firstDomainRows(results)
	outcomes := make([]CallerLeafOutcome, len(rows))
	for index := range rows {
		outcomes[index], err = validatePersistedCallerLeafOutcome(rows[index])
		if err != nil || !sameCallerGenerationSemantics(outcomes[index].Generation, preparedGeneration) ||
			(index > 0 && !callerPairLess(outcomes[index-1].Pair, outcomes[index].Pair)) {
			return nil, fmt.Errorf("list caller leaf outcomes row %d: %w", index, ErrInvalidCallerLeafState)
		}
		normalizedPair, pairErr := prepareCallerLeafPair(preparedGeneration, outcomes[index].Pair)
		if pairErr != nil || normalizedPair.PairDigest != outcomes[index].Pair.PairDigest {
			return nil, fmt.Errorf("list caller leaf outcomes row %d: %w", index, ErrInvalidCallerLeafState)
		}
		outcomes[index].Generation = preparedGeneration
		outcomes[index].Pair = normalizedPair
	}
	return outcomes, nil
}

// GetCallerLeafOutcomeProgress counts the exact generation inside SurrealDB
// and returns one fixed-size row. The selected generation is bounded by the
// frozen pair ceiling, while no pair payload, receipt, or artifact identity
// crosses the WebSocket boundary.
func (s *Surreal) GetCallerLeafOutcomeProgress(
	ctx context.Context,
	generation CallerGenerationIdentity,
) (CallerLeafOutcomeProgress, error) {
	prepared, err := prepareCallerGeneration(generation)
	if err != nil {
		return CallerLeafOutcomeProgress{}, fmt.Errorf(
			"get caller leaf outcome progress: %w", err,
		)
	}
	type progressRow struct {
		CallerLeafOutcomeProgress
		ShapeCount int `json:"shape_count"`
	}
	results, err := surrealdb.Query[[]progressRow](ctx, s.db, `
SELECT
	count() AS settled_count,
	count(disposition = $succeeded) AS succeeded_count,
	count(disposition = $refused) AS refused_count,
	count(writer_schema = $writer_schema
		AND generation.repository = $repository
		AND generation.digest = $generation_digest
		AND pair.digest != NONE) AS shape_count
FROM caller_leaf_outcome
WHERE repository = $repository AND generation_digest = $generation_digest
GROUP ALL`, map[string]any{
		"repository":        prepared.Repository,
		"generation_digest": prepared.Digest,
		"succeeded":         string(CallerLeafSucceeded),
		"refused":           string(CallerLeafTerminalGenerationRefusal),
		"writer_schema":     CallerLeafWriterSchema,
	})
	if err != nil {
		return CallerLeafOutcomeProgress{}, fmt.Errorf(
			"get caller leaf outcome progress: %w", err,
		)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return CallerLeafOutcomeProgress{}, fmt.Errorf(
			"get caller leaf outcome progress: %w", ErrInvalidCallerLeafState,
		)
	}
	progress := rows[0].CallerLeafOutcomeProgress
	if progress.SettledCount < 0 ||
		progress.SettledCount > maxCallerGenerationPairs ||
		progress.SucceededCount < 0 ||
		progress.RefusedCount < 0 ||
		progress.SucceededCount+progress.RefusedCount != progress.SettledCount ||
		rows[0].ShapeCount != progress.SettledCount {
		return CallerLeafOutcomeProgress{}, fmt.Errorf(
			"get caller leaf outcome progress: %w", ErrInvalidCallerLeafState,
		)
	}
	return progress, nil
}

type callerLeafOutcomeSummary struct {
	PairDigest  string                `json:"pair_digest"`
	Domain      string                `json:"domain"`
	LeafPrefix  string                `json:"leaf_prefix"`
	Disposition CallerLeafDisposition `json:"disposition"`
}

func prepareCallerGenerationAdmission(
	admission CallerGenerationAdmission,
	pairs []CallerLeafPair,
	outcomes []CallerLeafOutcome,
) (CallerGenerationAdmission, []callerLeafOutcomeSummary, error) {
	var err error
	admission.Generation, err = prepareCallerGeneration(admission.Generation)
	if err != nil {
		return admission, nil, fmt.Errorf("generation: %w", err)
	}
	preparedPairs, err := prepareCallerLeafPairs(admission.Generation, pairs)
	if err != nil {
		return admission, nil, err
	}
	if len(outcomes) != len(preparedPairs) {
		return admission, nil, errors.New("every expected pair must have one immutable outcome")
	}
	if admission.PeakOpenFiles < 0 || admission.PeakOpenFiles > maxCallerPeakOpenFiles {
		return admission, nil, errors.New("peak open files is outside its bound")
	}
	admission.Refusals = nil
	summaries := make([]callerLeafOutcomeSummary, len(outcomes))
	refusals := make(map[string]CallerGenerationRefusalSummary)
	allSucceeded := true
	for index := range outcomes {
		if outcomes[index].Generation != admission.Generation || outcomes[index].Pair != preparedPairs[index] {
			return admission, nil, fmt.Errorf("outcome %d does not match its expected pair", index)
		}
		summaries[index] = callerLeafOutcomeSummary{
			PairDigest:  outcomes[index].Pair.PairDigest,
			Domain:      outcomes[index].Pair.Domain,
			LeafPrefix:  outcomes[index].Pair.LeafPrefix,
			Disposition: outcomes[index].Disposition,
		}
		if outcomes[index].Disposition != CallerLeafSucceeded {
			if outcomes[index].Refusal == nil ||
				pipelinerefusal.Validate(*outcomes[index].Refusal) != nil ||
				outcomes[index].Refusal.GenerationKind != pipelinerefusal.GenerationCaller {
				return admission, nil, fmt.Errorf("outcome %d has no valid caller refusal", index)
			}
			addCallerRefusalSummary(refusals, *outcomes[index].Refusal, 1)
			allSucceeded = false
			continue
		}
		receipt := outcomes[index].Receipt
		if receipt == nil {
			return admission, nil, fmt.Errorf("outcome %d has no success receipt", index)
		}
		if err := addCallerCount(
			&admission.ArtifactCount, receipt.ArtifactCount,
			maxCallerGenerationPairs, "artifact",
		); err != nil {
			return admission, nil, err
		}
		if err := addCallerCount(
			&admission.ResultCount, receipt.ResultCount,
			maxCallerGenerationResults+maxCallerPairResults, "result",
		); err != nil {
			return admission, nil, err
		}
		if err := addCallerCount(
			&admission.AbstentionCount, receipt.AbstentionCount,
			maxCallerGenerationAbstentions+maxCallerPairAbstentions,
			"abstention",
		); err != nil {
			return admission, nil, err
		}
		if err := addCallerCount(
			&admission.CoverageRecordCount, receipt.CoverageRecordCount,
			maxCallerGenerationCoverageRecords, "coverage record",
		); err != nil {
			return admission, nil, err
		}
		if err := addCallerCount(
			&admission.CoveredCandidateCount, receipt.CoveredCandidateCount,
			maxCallerGenerationCoveredCandidates, "covered candidate",
		); err != nil {
			return admission, nil, err
		}
		if err := addCallerBytes(
			&admission.CanonicalBytes, receipt.CanonicalBytes,
			maxCallerGenerationCanonical+maxCallerPairCanonicalBytes,
			"canonical",
		); err != nil {
			return admission, nil, err
		}
		if err := addCallerBytes(
			&admission.StagingBytes, receipt.StagingBytes,
			maxCallerGenerationStaging+maxCallerPairStagingBytes,
			"staging",
		); err != nil {
			return admission, nil, err
		}
	}
	aggregateExceeded := admission.ResultCount > maxCallerGenerationResults ||
		admission.AbstentionCount > maxCallerGenerationAbstentions ||
		admission.CanonicalBytes > maxCallerGenerationCanonical ||
		admission.StagingBytes > maxCallerGenerationStaging
	if allSucceeded && aggregateExceeded {
		refusal := callerAggregateRefusal(admission)
		if refusal == nil {
			return admission, nil, errors.New("caller aggregate refusal has no dimension")
		}
		addCallerRefusalSummary(refusals, *refusal, 0)
	}
	admission.Refusals = callerRefusalSummaries(refusals)
	if len(admission.Refusals) > maxCallerRefusalSummaries {
		return admission, nil, errors.New("caller refusal summary exceeds its bound")
	}
	switch admission.Disposition {
	case CallerGenerationAdmitted:
		if !allSucceeded {
			return admission, nil, errors.New("a generation with a terminal pair cannot be admitted")
		}
		if aggregateExceeded {
			return admission, nil, errors.New("admitted caller generation exceeds an aggregate bound")
		}
		if len(admission.Refusals) != 0 {
			return admission, nil, errors.New("admitted caller generation carries a refusal")
		}
	case CallerGenerationTerminalGenerationRefusal:
		if allSucceeded && !aggregateExceeded {
			return admission, nil, errors.New(
				"terminal caller generation has neither a terminal pair nor an aggregate refusal",
			)
		}
		if len(admission.Refusals) == 0 {
			return admission, nil, errors.New("terminal caller generation lacks a typed refusal")
		}
	default:
		return admission, nil, errors.New("caller generation admission disposition is not in the frozen set")
	}
	admission.PairCount = len(preparedPairs)
	fields := make([]string, len(preparedPairs))
	for index := range preparedPairs {
		fields[index] = preparedPairs[index].PairDigest
	}
	admission.PairSetDigest = callerLeafDigestFields(
		"phebs-caller-leaf-pair-set-v1\x00", fields...,
	)
	admission.WriterSchema = CallerLeafWriterSchema
	admission.RecordedAt = time.Time{}
	return admission, summaries, nil
}

func addCallerRefusalSummary(
	values map[string]CallerGenerationRefusalSummary,
	refusal pipelinerefusal.Receipt,
	outcomes int,
) {
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%d",
		refusal.Stage, refusal.GenerationKind, refusal.Classification,
		refusal.Dimension, refusal.Observed, refusal.Limit)
	current := values[key]
	current.Refusal = refusal
	current.OutcomeCount += outcomes
	values[key] = current
}

func callerRefusalSummaries(
	values map[string]CallerGenerationRefusalSummary,
) []CallerGenerationRefusalSummary {
	result := make([]CallerGenerationRefusalSummary, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	slices.SortFunc(result, func(left, right CallerGenerationRefusalSummary) int {
		return strings.Compare(callerRefusalSortKey(left), callerRefusalSortKey(right))
	})
	if len(result) > maxCallerRefusalSummaries {
		overflowOutcomes := 0
		for _, value := range result[maxCallerRefusalSummaries-1:] {
			overflowOutcomes += value.OutcomeCount
		}
		result = append(result[:maxCallerRefusalSummaries-1], CallerGenerationRefusalSummary{
			Refusal: pipelinerefusal.Receipt{
				Schema:         pipelinerefusal.Schema,
				Stage:          pipelinerefusal.StageCallerGenerationAdmission,
				GenerationKind: pipelinerefusal.GenerationCaller,
				Classification: pipelinerefusal.ClassificationUnknown,
				Dimension:      pipelinerefusal.DimensionUnknown,
			},
			OutcomeCount: overflowOutcomes,
		})
		slices.SortFunc(result, func(left, right CallerGenerationRefusalSummary) int {
			return strings.Compare(callerRefusalSortKey(left), callerRefusalSortKey(right))
		})
	}
	return result
}

func callerRefusalSortKey(value CallerGenerationRefusalSummary) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%020d\x00%020d",
		value.Refusal.Stage, value.Refusal.GenerationKind,
		value.Refusal.Classification, value.Refusal.Dimension,
		value.Refusal.Observed, value.Refusal.Limit)
}

func callerAggregateRefusal(
	admission CallerGenerationAdmission,
) *pipelinerefusal.Receipt {
	values := []struct {
		dimension pipelinerefusal.Dimension
		observed  int64
		limit     int64
	}{
		{pipelinerefusal.DimensionCallerGenerationResults, int64(admission.ResultCount), maxCallerGenerationResults},
		{pipelinerefusal.DimensionCallerGenerationAbstentions, int64(admission.AbstentionCount), maxCallerGenerationAbstentions},
		{pipelinerefusal.DimensionCallerGenerationCanonicalBytes, admission.CanonicalBytes, maxCallerGenerationCanonical},
		{pipelinerefusal.DimensionCallerGenerationStagingBytes, admission.StagingBytes, maxCallerGenerationStaging},
	}
	for _, value := range values {
		if value.observed > value.limit {
			result := pipelinerefusal.Receipt{
				Schema:         pipelinerefusal.Schema,
				Stage:          pipelinerefusal.StageCallerGenerationAdmission,
				GenerationKind: pipelinerefusal.GenerationCaller,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      value.dimension,
				Observed:       value.observed, Limit: value.limit,
			}
			return &result
		}
	}
	return nil
}

// ValidateCallerGenerationAdmission recomputes every immutable aggregate field
// from the exact expected pair/outcome set. RecordedAt remains store-owned and
// is intentionally excluded from the equality check.
func ValidateCallerGenerationAdmission(
	admission CallerGenerationAdmission,
	pairs []CallerLeafPair,
	outcomes []CallerLeafOutcome,
) error {
	input := admission
	input.PairSetDigest = ""
	input.PairCount = 0
	input.ArtifactCount = 0
	input.ResultCount = 0
	input.AbstentionCount = 0
	input.CoverageRecordCount = 0
	input.CoveredCandidateCount = 0
	input.CanonicalBytes = 0
	input.StagingBytes = 0
	input.Refusals = nil
	input.WriterSchema = ""
	input.RecordedAt = time.Time{}
	expected, _, err := prepareCallerGenerationAdmission(input, pairs, outcomes)
	if err != nil {
		return err
	}
	if admission.Generation != expected.Generation ||
		admission.Disposition != expected.Disposition ||
		admission.PairSetDigest != expected.PairSetDigest ||
		admission.PairCount != expected.PairCount ||
		admission.ArtifactCount != expected.ArtifactCount ||
		admission.ResultCount != expected.ResultCount ||
		admission.AbstentionCount != expected.AbstentionCount ||
		admission.CoverageRecordCount != expected.CoverageRecordCount ||
		admission.CoveredCandidateCount != expected.CoveredCandidateCount ||
		admission.CanonicalBytes != expected.CanonicalBytes ||
		admission.StagingBytes != expected.StagingBytes ||
		admission.PeakOpenFiles != expected.PeakOpenFiles ||
		!slices.Equal(admission.Refusals, expected.Refusals) ||
		admission.WriterSchema != expected.WriterSchema {
		return ErrInvalidCallerLeafState
	}
	return nil
}

func addCallerCount(total *int, value, limit int, name string) error {
	if value < 0 || *total < 0 || value > limit-*total {
		return fmt.Errorf("caller generation %s count overflows its bound", name)
	}
	*total += value
	return nil
}

func addCallerBytes(total *int64, value, limit int64, name string) error {
	if value < 0 || *total < 0 || value > limit-*total {
		return fmt.Errorf("caller generation %s bytes overflow their bound", name)
	}
	*total += value
	return nil
}

type callerGenerationAdmissionRec struct {
	CallerGenerationAdmission
	Repository       string           `json:"repository"`
	GenerationDigest string           `json:"generation_digest"`
	RecID            *models.RecordID `json:"id"`
}

const recordCallerGenerationAdmissionSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $repo = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $candidate = (SELECT head_commit, unit_digest, manifest_digest,
	policy_digest, control_revision
	FROM $candidate_rid)[0];
LET $resolver = (SELECT head_commit, unit_digest, candidate_manifest_digest,
	declaration_set_digest, generation_digest, manifest_digest,
	control_revision, writer_schema FROM $resolver_rid)[0];
LET $owned = (SELECT id FROM type::record($job_id)
	WHERE target = $repository AND status IN ['claimed', 'running']
		AND lease_token = $lease AND claimed_by = $claimed_by LIMIT 1)[0].id;
LET $outcomes = (SELECT pair.digest AS pair_digest, domain, leaf_prefix, disposition
	FROM caller_leaf_outcome
	WHERE repository = $repository AND generation_digest = $generation_digest
	ORDER BY domain, leaf_prefix);
LET $current = (SELECT * FROM $admission_rid)[0];
LET $authority_ok = $writer_ok AND $owned != NONE AND $repo != NONE
	AND ($repo.deleting = NONE OR $repo.deleting = false)
	AND $repo.indexed_commit_hash = $head_commit
	AND ($repo.indexed_analysis_unit.digest ?? '') = $unit_digest
	AND $candidate != NONE
	AND $candidate.head_commit = $head_commit
	AND $candidate.unit_digest = $unit_digest
	AND $candidate.manifest_digest = $candidate_manifest_digest
	AND $candidate.policy_digest = $candidate_policy_digest
	AND $candidate.control_revision = $candidate_control_revision
	AND $resolver != NONE
	AND $resolver.head_commit = $head_commit
	AND $resolver.unit_digest = $unit_digest
	AND $resolver.candidate_manifest_digest = $candidate_manifest_digest
	AND $resolver.declaration_set_digest = $declaration_set_digest
	AND $resolver.generation_digest = $resolver_generation_digest
	AND $resolver.manifest_digest = $resolver_manifest_digest
	AND $resolver.control_revision = $resolver_control_revision
	AND $resolver.writer_schema = $resolver_writer_schema
	AND $outcomes = $expected_outcomes;
LET $same = $current != NONE
	AND $current.repository = $repository
	AND $current.generation_digest = $generation_digest
	AND $current.disposition = $disposition
	AND $current.pair_set_digest = $pair_set_digest
	AND $current.pair_count = $pair_count
	AND $current.artifact_count = $artifact_count
	AND $current.result_count = $result_count
	AND $current.abstention_count = $abstention_count
	AND ($current.coverage_record_count ?? 0) = $coverage_record_count
	AND ($current.covered_candidate_count ?? 0) = $covered_candidate_count
	AND $current.canonical_bytes = $canonical_bytes
	AND $current.staging_bytes = $staging_bytes
	AND $current.peak_open_files = $peak_open_files
	AND $current.refusals = $refusals
	AND $current.writer_schema = $writer_schema;
LET $written = IF $authority_ok = false THEN []
	ELSE IF $current != NONE AND $same = false THEN []
	ELSE IF $same THEN [$current]
	ELSE (CREATE $admission_rid CONTENT {
		repository: $repository,
		generation: $generation,
		generation_digest: $generation_digest,
		disposition: $disposition,
		pair_set_digest: $pair_set_digest,
		pair_count: $pair_count,
		artifact_count: $artifact_count,
		result_count: $result_count,
		abstention_count: $abstention_count,
		coverage_record_count: $coverage_record_count,
		covered_candidate_count: $covered_candidate_count,
		canonical_bytes: $canonical_bytes,
		staging_bytes: $staging_bytes,
		peak_open_files: $peak_open_files,
		refusals: $refusals,
		writer_schema: $writer_schema,
		recorded_at: time::now()
	} RETURN AFTER) END;
RETURN $written;
COMMIT;`

func (s *Surreal) RecordCallerGenerationAdmission(
	ctx context.Context,
	job Job,
	admission CallerGenerationAdmission,
	pairs []CallerLeafPair,
) error {
	preparedGeneration, err := prepareCallerGeneration(admission.Generation)
	if err != nil {
		return fmt.Errorf("record caller generation admission: %w", err)
	}
	outcomes, err := s.ListCallerLeafOutcomes(ctx, preparedGeneration)
	if err != nil {
		return fmt.Errorf("record caller generation admission: %w", err)
	}
	// Aggregate fields are output, never caller authority.
	admission.PairSetDigest = ""
	admission.PairCount = 0
	admission.ArtifactCount = 0
	admission.ResultCount = 0
	admission.AbstentionCount = 0
	admission.CoverageRecordCount = 0
	admission.CoveredCandidateCount = 0
	admission.CanonicalBytes = 0
	admission.StagingBytes = 0
	admission.Refusals = nil
	prepared, summaries, err := prepareCallerGenerationAdmission(admission, pairs, outcomes)
	if err != nil {
		return fmt.Errorf("record caller generation admission: %w: %v", ErrInvalidCallerLeafState, err)
	}
	if err := validateCallerJob(job, prepared.Generation.Repository); err != nil {
		return fmt.Errorf("record caller generation admission: %w", err)
	}
	results, err := surrealdb.Query[[]callerGenerationAdmissionRec](ctx, s.db,
		recordCallerGenerationAdmissionSQL, map[string]any{
			"migration_rid":     callerLeafMigrationID(),
			"migration_version": callerLeafWriterMigrationVersion,
			"repo_rid":          repoID(prepared.Generation.Repository),
			"candidate_rid":     candidateManifestPublicationID(prepared.Generation.Repository),
			"resolver_rid":      resolverCatalogPublicationID(prepared.Generation.Repository),
			"admission_rid":     callerGenerationAdmissionID(prepared.Generation),
			"job_id":            job.ID, "lease": job.LeaseToken, "claimed_by": job.ClaimedBy,
			"repository":                 prepared.Generation.Repository,
			"head_commit":                prepared.Generation.HeadCommit,
			"unit_digest":                prepared.Generation.UnitDigest,
			"declaration_set_digest":     prepared.Generation.DeclarationSetDigest,
			"candidate_manifest_digest":  prepared.Generation.CandidateManifestDigest,
			"candidate_policy_digest":    prepared.Generation.CandidatePolicyDigest,
			"candidate_control_revision": prepared.Generation.CandidateControlRevision,
			"resolver_generation_digest": prepared.Generation.ResolverGenerationDigest,
			"resolver_manifest_digest":   prepared.Generation.ResolverManifestDigest,
			"resolver_control_revision":  prepared.Generation.ResolverControlRevision,
			"resolver_writer_schema":     prepared.Generation.ResolverWriterSchema,
			"generation":                 prepared.Generation,
			"generation_digest":          prepared.Generation.Digest,
			"expected_outcomes":          summaries,
			"disposition":                prepared.Disposition,
			"pair_set_digest":            prepared.PairSetDigest,
			"pair_count":                 prepared.PairCount,
			"artifact_count":             prepared.ArtifactCount,
			"result_count":               prepared.ResultCount,
			"abstention_count":           prepared.AbstentionCount,
			"coverage_record_count":      prepared.CoverageRecordCount,
			"covered_candidate_count":    prepared.CoveredCandidateCount,
			"canonical_bytes":            prepared.CanonicalBytes,
			"staging_bytes":              prepared.StagingBytes,
			"peak_open_files":            prepared.PeakOpenFiles,
			"refusals":                   prepared.Refusals,
			"writer_schema":              CallerLeafWriterSchema,
		})
	if err != nil {
		return fmt.Errorf("record caller generation admission: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return fmt.Errorf("record caller generation admission: stale or conflicting authority: %w", ErrConflict)
	}
	if _, err := validatePersistedCallerGenerationAdmission(rows[0]); err != nil {
		return fmt.Errorf("record caller generation admission: persisted row: %w", err)
	}
	return nil
}

func validatePersistedCallerGenerationAdmission(row callerGenerationAdmissionRec) (CallerGenerationAdmission, error) {
	originalDigest := row.Generation.Digest
	preparedGeneration, err := prepareCallerGeneration(row.Generation)
	if err != nil {
		return CallerGenerationAdmission{}, err
	}
	if originalDigest != preparedGeneration.Digest || row.GenerationDigest != preparedGeneration.Digest ||
		row.Repository != preparedGeneration.Repository || row.WriterSchema != CallerLeafWriterSchema ||
		row.RecordedAt.IsZero() || row.PairCount < 0 || row.PairCount > maxCallerGenerationPairs ||
		row.ArtifactCount < 0 || row.ArtifactCount > row.PairCount ||
		row.ResultCount < 0 || row.ResultCount > maxCallerGenerationResults+maxCallerPairResults ||
		row.AbstentionCount < 0 || row.AbstentionCount > maxCallerGenerationAbstentions+maxCallerPairAbstentions ||
		row.CoverageRecordCount < 0 || row.CoverageRecordCount > maxCallerGenerationCoverageRecords ||
		row.CoverageRecordCount > row.PairCount ||
		row.CoveredCandidateCount < 0 || row.CoveredCandidateCount > maxCallerGenerationCoveredCandidates ||
		row.CoveredCandidateCount > row.PairCount*maxCallerCandidateRecords ||
		(row.CoverageRecordCount == 0) != (row.CoveredCandidateCount == 0) ||
		row.CanonicalBytes < 0 || row.CanonicalBytes > maxCallerGenerationCanonical+maxCallerPairCanonicalBytes ||
		row.StagingBytes < 0 || row.StagingBytes > maxCallerGenerationStaging+maxCallerPairStagingBytes ||
		row.PeakOpenFiles < 0 || row.PeakOpenFiles > maxCallerPeakOpenFiles ||
		!validSHA256Digest(row.PairSetDigest) {
		return CallerGenerationAdmission{}, ErrInvalidCallerLeafState
	}
	if row.Disposition != CallerGenerationAdmitted &&
		row.Disposition != CallerGenerationTerminalGenerationRefusal {
		return CallerGenerationAdmission{}, ErrInvalidCallerLeafState
	}
	refusalOutcomes := 0
	if len(row.Refusals) > maxCallerRefusalSummaries {
		return CallerGenerationAdmission{}, ErrInvalidCallerLeafState
	}
	for index, summary := range row.Refusals {
		if pipelinerefusal.Validate(summary.Refusal) != nil ||
			summary.Refusal.GenerationKind != pipelinerefusal.GenerationCaller ||
			summary.OutcomeCount < 0 || summary.OutcomeCount > row.PairCount-refusalOutcomes ||
			index > 0 && !callerRefusalSummaryLess(row.Refusals[index-1], summary) {
			return CallerGenerationAdmission{}, ErrInvalidCallerLeafState
		}
		refusalOutcomes += summary.OutcomeCount
	}
	if row.Disposition == CallerGenerationAdmitted && len(row.Refusals) != 0 ||
		row.Disposition == CallerGenerationTerminalGenerationRefusal && len(row.Refusals) == 0 {
		return CallerGenerationAdmission{}, ErrInvalidCallerLeafState
	}
	if row.Disposition == CallerGenerationAdmitted &&
		(row.ResultCount > maxCallerGenerationResults ||
			row.AbstentionCount > maxCallerGenerationAbstentions ||
			row.CanonicalBytes > maxCallerGenerationCanonical ||
			row.StagingBytes > maxCallerGenerationStaging) {
		return CallerGenerationAdmission{}, ErrInvalidCallerLeafState
	}
	row.Generation = preparedGeneration
	row.RecordedAt = row.RecordedAt.UTC()
	return row.CallerGenerationAdmission, nil
}

func callerRefusalSummaryLess(
	left,
	right CallerGenerationRefusalSummary,
) bool {
	return callerRefusalSortKey(left) < callerRefusalSortKey(right)
}

func (s *Surreal) GetCallerGenerationAdmission(ctx context.Context, generation CallerGenerationIdentity) (*CallerGenerationAdmission, error) {
	prepared, err := prepareCallerGeneration(generation)
	if err != nil {
		return nil, fmt.Errorf("get caller generation admission: %w", err)
	}
	results, err := surrealdb.Query[[]callerGenerationAdmissionRec](ctx, s.db,
		"SELECT * FROM $rid", map[string]any{"rid": callerGenerationAdmissionID(prepared)})
	if err != nil {
		return nil, fmt.Errorf("get caller generation admission: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("caller generation admission: %w", ErrNotFound)
	}
	admission, err := validatePersistedCallerGenerationAdmission(rows[0])
	if err != nil || !sameCallerGenerationSemantics(admission.Generation, prepared) {
		return nil, fmt.Errorf("get caller generation admission: %w", ErrInvalidCallerLeafState)
	}
	admission.Generation = prepared
	return &admission, nil
}

func (s *Surreal) ListCallerGenerationAdmissions(ctx context.Context, repository string) ([]CallerGenerationAdmission, error) {
	if err := reponame.Validate(repository); err != nil {
		return nil, fmt.Errorf("list caller generation admissions: %w", err)
	}
	results, err := surrealdb.Query[[]callerGenerationAdmissionRec](ctx, s.db,
		`SELECT * FROM caller_generation_admission
		 WHERE repository = $repository ORDER BY recorded_at, generation_digest`,
		map[string]any{"repository": repository})
	if err != nil {
		return nil, fmt.Errorf("list caller generation admissions: %w", err)
	}
	rows := firstDomainRows(results)
	admissions := make([]CallerGenerationAdmission, len(rows))
	for index := range rows {
		admissions[index], err = validatePersistedCallerGenerationAdmission(rows[index])
		if err != nil || admissions[index].Generation.Repository != repository {
			return nil, fmt.Errorf("list caller generation admissions row %d: %w", index, ErrInvalidCallerLeafState)
		}
	}
	return admissions, nil
}

func (s *Surreal) ClearCallerLeafOutcome(ctx context.Context, generation CallerGenerationIdentity, pair CallerLeafPair) error {
	preparedGeneration, err := prepareCallerGeneration(generation)
	if err != nil {
		return fmt.Errorf("clear caller leaf outcome: %w", err)
	}
	preparedPair, err := prepareCallerLeafPair(preparedGeneration, pair)
	if err != nil {
		return fmt.Errorf("clear caller leaf outcome: %w", err)
	}
	return s.clearCallerLeafState(ctx, `
		DELETE $outcome_rid RETURN NONE;
		DELETE $admission_rid RETURN NONE;`, map[string]any{
		"outcome_rid":   callerLeafOutcomeID(preparedGeneration, preparedPair),
		"admission_rid": callerGenerationAdmissionID(preparedGeneration),
	}, preparedGeneration.Repository, preparedGeneration.Digest, false,
		"clear caller leaf outcome")
}

func (s *Surreal) ClearCallerLeafGeneration(ctx context.Context, generation CallerGenerationIdentity) error {
	prepared, err := prepareCallerGeneration(generation)
	if err != nil {
		return fmt.Errorf("clear caller leaf generation: %w", err)
	}
	return s.clearCallerLeafState(ctx, `
		DELETE caller_leaf_outcome WHERE repository = $repository
			AND generation_digest = $generation_digest RETURN NONE;
		DELETE $admission_rid RETURN NONE;`, map[string]any{
		"repository":        prepared.Repository,
		"generation_digest": prepared.Digest,
		"admission_rid":     callerGenerationAdmissionID(prepared),
	}, prepared.Repository, prepared.Digest, false,
		"clear caller leaf generation")
}

func (s *Surreal) ClearAllCallerLeafState(ctx context.Context, repository string) error {
	if err := reponame.Validate(repository); err != nil {
		return fmt.Errorf("clear all caller leaf state: %w", err)
	}
	return s.clearCallerLeafState(ctx, `
		DELETE caller_leaf_outcome WHERE repository = $repository RETURN NONE;
		DELETE caller_generation_admission WHERE repository = $repository RETURN NONE;`,
		map[string]any{"repository": repository}, repository, "", true,
		"clear all caller leaf state")
}

// ClearAllCallerLeafStateForRestore is the compatibility entry point for the
// unified raw restore clear. It retires imported publication authority before
// removing caller-leaf outcomes and admissions without decoding any row.
func (s *Surreal) ClearAllCallerLeafStateForRestore(ctx context.Context) error {
	return s.ClearAllCallerPublicationStateForRestore(ctx)
}

func (s *Surreal) clearCallerLeafState(
	ctx context.Context,
	body string,
	vars map[string]any,
	repository,
	generationDigest string,
	clearAll bool,
	operation string,
) error {
	statement := `BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $publication_writer_ok = array::len(SELECT id FROM $publication_migration_rid
	WHERE version = $publication_migration_version LIMIT 1) = 1;
IF $writer_ok = false OR $publication_writer_ok = false {
	THROW 'phebs-permanent: caller-leaf writer generation is not active'
};
LET $publication = (SELECT generation_digest FROM $publication_rid)[0];
LET $retire_publication = $publication != NONE
	AND ($clear_all OR $publication.generation_digest = $generation_digest);
` + body + `
LET $retired_publication = IF $retire_publication THEN
	(DELETE $publication_rid RETURN BEFORE) ELSE [] END;
IF array::len($retired_publication) = 1 {
	UPDATE $repo_rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN NONE;
};
COMMIT;`
	vars["migration_rid"] = callerLeafMigrationID()
	vars["migration_version"] = callerLeafWriterMigrationVersion
	vars["publication_migration_rid"] = callerGenerationPublicationMigrationID()
	vars["publication_migration_version"] = callerGenerationPublicationMigrationVersion
	vars["publication_rid"] = callerGenerationPublicationID(repository)
	vars["repo_rid"] = repoID(repository)
	vars["generation_digest"] = generationDigest
	vars["clear_all"] = clearAll
	results, err := surrealdb.Query[any](ctx, s.db, statement, vars)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf("%s statement %d: %s", operation, index, result.Error.Message)
		}
	}
	return nil
}
