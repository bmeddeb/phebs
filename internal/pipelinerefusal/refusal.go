// Package pipelinerefusal defines the closed, source-free error envelope used
// at derived-pipeline job boundaries. The envelope is operational authority
// only: it classifies why work refused without carrying repository identity,
// paths, object IDs, digests, child output, or raw error text.
package pipelinerefusal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const Schema = "phebs-pipeline-refusal-v1"

var ErrInvalid = errors.New("invalid pipeline refusal")

type Stage string

const (
	StageUnknown                   Stage = "unknown"
	StageSourcePartitionPlanning   Stage = "source_partition_planning"
	StageObservationPublication    Stage = "observation_publication"
	StageCandidateStrictOpen       Stage = "candidate_strict_open"
	StageDomainInventory           Stage = "domain_inventory"
	StageExtractorExecution        Stage = "extractor_execution"
	StageEvidenceStaging           Stage = "evidence_staging"
	StageFinalPublication          Stage = "final_publication"
	StageCallerPairExecution       Stage = "caller_pair_execution"
	StageCallerArtifactSeal        Stage = "caller_artifact_seal"
	StageCallerArtifactInstall     Stage = "caller_artifact_install"
	StageCallerGenerationAdmission Stage = "caller_generation_admission"
)

type GenerationKind string

const (
	GenerationUnknown          GenerationKind = "unknown"
	GenerationSourcePartition  GenerationKind = "source_partition"
	GenerationObservation      GenerationKind = "observation"
	GenerationCandidate        GenerationKind = "candidate"
	GenerationExtractionDomain GenerationKind = "extraction_domain"
	GenerationCaller           GenerationKind = "caller"
)

type Classification string

const (
	ClassificationLimit   Classification = "limit"
	ClassificationInvalid Classification = "invalid"
	ClassificationUnknown Classification = "unknown"
)

type Dimension string

const (
	DimensionUnknown                        Dimension = "unknown"
	DimensionBlobBytes                      Dimension = "blob_bytes"
	DimensionBlobPlacements                 Dimension = "blob_placements"
	DimensionPartitionCount                 Dimension = "partition_count"
	DimensionAggregateSegments              Dimension = "aggregate_segments"
	DimensionObservationMemberBytes         Dimension = "observation_member_bytes"
	DimensionGenerationBlobBytes            Dimension = "generation_blob_bytes"
	DimensionGenerationEncodedBytes         Dimension = "generation_encoded_bytes"
	DimensionGenerationArtifactBytes        Dimension = "generation_artifact_bytes"
	DimensionGenerationControlBytes         Dimension = "generation_control_bytes"
	DimensionRepositoryGenerations          Dimension = "repository_generations"
	DimensionRepositoryEntries              Dimension = "repository_entries"
	DimensionGenerationEntries              Dimension = "generation_entries"
	DimensionPlanningSpoolBytes             Dimension = "planning_spool_bytes"
	DimensionBatchHeaderBytes               Dimension = "batch_header_bytes"
	DimensionBatchOutputBytes               Dimension = "batch_output_bytes"
	DimensionGenerationRecords              Dimension = "generation_records"
	DimensionInventoryFiles                 Dimension = "inventory_files"
	DimensionInventoryPathBytes             Dimension = "inventory_path_bytes"
	DimensionSymlinkDepth                   Dimension = "symlink_depth"
	DimensionLookupRecords                  Dimension = "lookup_records"
	DimensionLookupPathBytes                Dimension = "lookup_path_bytes"
	DimensionTreeRecordBytes                Dimension = "tree_record_bytes"
	DimensionSourceBlobBytes                Dimension = "source_blob_bytes"
	DimensionSourceReadAttempts             Dimension = "source_read_attempts"
	DimensionSourceReadBytes                Dimension = "source_read_bytes"
	DimensionSourceReadFiles                Dimension = "source_read_files"
	DimensionFacts                          Dimension = "facts"
	DimensionStagedRows                     Dimension = "staged_rows"
	DimensionDomainResultFacts              Dimension = "domain_result_facts"
	DimensionDomainResultRows               Dimension = "domain_result_rows"
	DimensionDomainResultReferences         Dimension = "domain_result_references"
	DimensionDomainCanonicalBytes           Dimension = "domain_result_canonical_bytes"
	DimensionDomainEncodedBytes             Dimension = "domain_result_encoded_bytes"
	DimensionCandidateMemberBytes           Dimension = "candidate_member_bytes"
	DimensionCandidateMembers               Dimension = "candidate_members"
	DimensionCallerPairResults              Dimension = "caller_pair_results"
	DimensionCallerPairAbstentions          Dimension = "caller_pair_abstentions"
	DimensionCallerRecordBytes              Dimension = "caller_record_bytes"
	DimensionCallerPairCanonicalBytes       Dimension = "caller_pair_canonical_bytes"
	DimensionCallerPairStagingBytes         Dimension = "caller_pair_staging_bytes"
	DimensionCallerPairSourceBytes          Dimension = "caller_pair_source_bytes"
	DimensionCallerGenerationPairs          Dimension = "caller_generation_pairs"
	DimensionCallerGenerationArtifacts      Dimension = "caller_generation_artifacts"
	DimensionCallerGenerationResults        Dimension = "caller_generation_results"
	DimensionCallerGenerationAbstentions    Dimension = "caller_generation_abstentions"
	DimensionCallerGenerationCanonicalBytes Dimension = "caller_generation_canonical_bytes"
	DimensionCallerGenerationStagingBytes   Dimension = "caller_generation_staging_bytes"
)

// Receipt is the complete durable projection of one refusal. Observed and
// Limit are meaningful only for the limit classification; every other class
// carries canonical zeroes rather than invented measurements.
type Receipt struct {
	Schema         string         `json:"schema"`
	Stage          Stage          `json:"stage"`
	GenerationKind GenerationKind `json:"generation_kind"`
	Classification Classification `json:"classification"`
	Dimension      Dimension      `json:"dimension"`
	Observed       int64          `json:"observed"`
	Limit          int64          `json:"limit"`
}

// Error keeps the raw cause matchable through errors.Is/errors.As while its
// rendered form contains only the closed receipt.
type Error struct {
	receipt Receipt
	cause   error
}

// Measurement is an internal, stage-free limit cause. Lower-level bounded
// readers attach it where the exact scalar is known; the owning job boundary
// later promotes it with At so stage and generation authority cannot be
// guessed in a utility package.
type Measurement struct {
	Dimension Dimension
	Observed  int64
	Limit     int64
	cause     error
}

// invalidMeasurement keeps invalid stage-free constructor input source-free
// while preserving both the programmer error and original cause for matching.
// At deliberately promotes it as an unknown refusal because no trustworthy
// scalar authority exists.
type invalidMeasurement struct {
	cause error
}

func (*invalidMeasurement) Error() string { return ErrInvalid.Error() }

func (measurement *invalidMeasurement) Unwrap() []error {
	return []error{ErrInvalid, measurement.cause}
}

func (measurement *Measurement) Error() string {
	return fmt.Sprintf(
		"pipeline limit: dimension=%s observed=%d limit=%d",
		measurement.Dimension, measurement.Observed, measurement.Limit,
	)
}

func (measurement *Measurement) Unwrap() error { return measurement.cause }

func (failure *Error) Error() string {
	encoded, err := json.Marshal(failure.receipt)
	if err != nil {
		return ErrInvalid.Error()
	}
	return "pipeline refusal: " + string(encoded)
}

// DurableErrorText returns the canonical source-free form that durable job
// boundaries may persist even when this refusal is nested inside wrappers.
func (failure *Error) DurableErrorText() string { return failure.Error() }

func (failure *Error) Unwrap() error { return failure.cause }

func (failure *Error) Receipt() Receipt { return failure.receipt }

// ParseDurableErrorText accepts only the canonical source-free representation
// emitted by DurableErrorText. Raw worker errors, wrappers, noncanonical JSON,
// and trailing data are never interpreted as a refusal.
func ParseDurableErrorText(value string) (Receipt, bool) {
	const prefix = "pipeline refusal: "
	if !strings.HasPrefix(value, prefix) {
		return Receipt{}, false
	}
	payload := strings.TrimPrefix(value, prefix)
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || Validate(receipt) != nil {
		return Receipt{}, false
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || payload != string(canonical) {
		return Receipt{}, false
	}
	return receipt, true
}

// Wrap returns nil for a nil cause. Invalid programmer-supplied receipt data
// degrades to a closed unknown refusal and joins ErrInvalid to the hidden
// cause; it never falls back to rendering raw error text.
func Wrap(cause error, receipt Receipt) error {
	if cause == nil {
		return nil
	}
	receipt.Schema = Schema
	if err := Validate(receipt); err != nil {
		stage := validOrDefaultStage(receipt.Stage)
		generation := validOrDefaultGeneration(receipt.GenerationKind)
		if !validStageGeneration(stage, generation) {
			stage = StageUnknown
			generation = GenerationUnknown
		}
		receipt = Receipt{
			Schema: Schema, Stage: stage, GenerationKind: generation,
			Classification: ClassificationUnknown,
			Dimension:      DimensionUnknown,
		}
		cause = errors.Join(ErrInvalid, cause)
	}
	return &Error{receipt: receipt, cause: cause}
}

func Limit(
	cause error,
	stage Stage,
	generation GenerationKind,
	dimension Dimension,
	observed,
	limit int64,
) error {
	return Wrap(cause, Receipt{
		Stage: stage, GenerationKind: generation,
		Classification: ClassificationLimit, Dimension: dimension,
		Observed: observed, Limit: limit,
	})
}

func Unknown(cause error, stage Stage, generation GenerationKind) error {
	return Wrap(cause, Receipt{
		Stage: stage, GenerationKind: generation,
		Classification: ClassificationUnknown, Dimension: DimensionUnknown,
	})
}

// Measure binds a lower-level limit to its exact source-free scalar. Invalid
// measurements retain the hidden cause under ErrInvalid and are deliberately
// left unmeasured for At to classify as unknown.
func Measure(cause error, dimension Dimension, observed, limit int64) error {
	if cause == nil {
		return nil
	}
	if !validDimension(dimension) || dimension == DimensionUnknown ||
		observed < 0 || limit < 0 || observed <= limit {
		return &invalidMeasurement{cause: cause}
	}
	return &Measurement{
		Dimension: dimension, Observed: observed, Limit: limit, cause: cause,
	}
}

// At closes any error at an owning stage. A measured limit retains its exact
// scalar; all other causes become an honest unknown with zero scalar fields.
func At(cause error, stage Stage, generation GenerationKind) error {
	if cause == nil {
		return nil
	}
	if receipt, ok := From(cause); ok {
		// The refusal may be nested under a formatter that added private
		// repository or domain text. Re-seal the complete chain while retaining
		// the already-authoritative inner stage and measurement.
		return Wrap(cause, receipt)
	}
	var measurement *Measurement
	if errors.As(cause, &measurement) && measurement != nil {
		return Limit(
			cause, stage, generation, measurement.Dimension,
			measurement.Observed, measurement.Limit,
		)
	}
	return Unknown(cause, stage, generation)
}

func Classified(
	cause error,
	stage Stage,
	generation GenerationKind,
	classification Classification,
	dimension Dimension,
) error {
	return Wrap(cause, Receipt{
		Stage: stage, GenerationKind: generation,
		Classification: classification, Dimension: dimension,
	})
}

// From walks joined and wrapped causes and returns the first closed refusal.
func From(err error) (Receipt, bool) {
	var failure *Error
	if !errors.As(err, &failure) || failure == nil {
		return Receipt{}, false
	}
	return failure.receipt, true
}

func Validate(receipt Receipt) error {
	if receipt.Schema != Schema || !validStage(receipt.Stage) ||
		!validGeneration(receipt.GenerationKind) ||
		!validStageGeneration(receipt.Stage, receipt.GenerationKind) ||
		!validClassification(receipt.Classification) ||
		!validDimension(receipt.Dimension) {
		return ErrInvalid
	}
	if (receipt.Stage == StageUnknown || receipt.GenerationKind == GenerationUnknown) &&
		receipt.Classification != ClassificationUnknown {
		return fmt.Errorf("%w: unknown boundary", ErrInvalid)
	}
	if receipt.Classification == ClassificationLimit {
		if receipt.Dimension == DimensionUnknown || receipt.Observed < 0 ||
			receipt.Limit < 0 || receipt.Observed <= receipt.Limit ||
			!validStageDimension(receipt.Stage, receipt.Dimension) {
			return fmt.Errorf("%w: limit shape", ErrInvalid)
		}
		return nil
	}
	if receipt.Observed != 0 || receipt.Limit != 0 {
		return fmt.Errorf("%w: non-limit scalar", ErrInvalid)
	}
	if receipt.Dimension != DimensionUnknown {
		return fmt.Errorf("%w: non-limit dimension", ErrInvalid)
	}
	return nil
}

func validStageGeneration(stage Stage, generation GenerationKind) bool {
	switch stage {
	case StageUnknown:
		return generation == GenerationUnknown
	case StageSourcePartitionPlanning:
		return generation == GenerationSourcePartition
	case StageObservationPublication:
		return generation == GenerationObservation
	case StageCandidateStrictOpen:
		return generation == GenerationCandidate
	case StageDomainInventory, StageExtractorExecution,
		StageEvidenceStaging, StageFinalPublication:
		return generation == GenerationExtractionDomain
	case StageCallerPairExecution, StageCallerArtifactSeal,
		StageCallerArtifactInstall, StageCallerGenerationAdmission:
		return generation == GenerationCaller
	default:
		return false
	}
}

func validStageDimension(stage Stage, dimension Dimension) bool {
	switch stage {
	case StageSourcePartitionPlanning:
		switch dimension {
		case DimensionBlobBytes, DimensionBlobPlacements,
			DimensionPartitionCount, DimensionAggregateSegments,
			DimensionGenerationBlobBytes,
			DimensionGenerationEncodedBytes, DimensionGenerationControlBytes,
			DimensionPlanningSpoolBytes:
			return true
		}
	case StageObservationPublication:
		switch dimension {
		case DimensionObservationMemberBytes, DimensionGenerationEncodedBytes,
			DimensionGenerationArtifactBytes, DimensionGenerationControlBytes,
			DimensionRepositoryGenerations, DimensionRepositoryEntries,
			DimensionGenerationEntries, DimensionBatchHeaderBytes,
			DimensionBatchOutputBytes, DimensionGenerationRecords:
			return true
		}
	case StageDomainInventory:
		switch dimension {
		case DimensionInventoryFiles, DimensionInventoryPathBytes,
			DimensionSymlinkDepth, DimensionLookupRecords,
			DimensionLookupPathBytes, DimensionTreeRecordBytes,
			DimensionSourceBlobBytes, DimensionDomainResultFacts,
			DimensionDomainResultRows, DimensionDomainResultReferences,
			DimensionDomainCanonicalBytes, DimensionDomainEncodedBytes,
			DimensionCandidateMemberBytes, DimensionCandidateMembers:
			return true
		}
	case StageExtractorExecution:
		switch dimension {
		case DimensionLookupRecords, DimensionLookupPathBytes,
			DimensionTreeRecordBytes, DimensionSourceBlobBytes,
			DimensionSourceReadAttempts,
			DimensionSourceReadBytes, DimensionSourceReadFiles:
			return true
		}
	case StageEvidenceStaging:
		return dimension == DimensionFacts || dimension == DimensionStagedRows ||
			dimension == DimensionGenerationEncodedBytes
	case StageCallerPairExecution:
		switch dimension {
		case DimensionCallerPairResults, DimensionCallerPairAbstentions,
			DimensionCallerRecordBytes, DimensionCallerPairCanonicalBytes,
			DimensionCallerPairSourceBytes:
			return true
		}
	case StageCallerArtifactSeal:
		return dimension == DimensionCallerPairStagingBytes
	case StageCallerArtifactInstall:
		return dimension == DimensionCallerPairCanonicalBytes ||
			dimension == DimensionCallerPairStagingBytes
	case StageCallerGenerationAdmission:
		switch dimension {
		case DimensionCallerGenerationPairs,
			DimensionCallerGenerationArtifacts,
			DimensionCallerGenerationResults,
			DimensionCallerGenerationAbstentions,
			DimensionCallerGenerationCanonicalBytes,
			DimensionCallerGenerationStagingBytes:
			return true
		}
	}
	return false
}

func validOrDefaultStage(stage Stage) Stage {
	if validStage(stage) {
		return stage
	}
	return StageUnknown
}

func validOrDefaultGeneration(generation GenerationKind) GenerationKind {
	if validGeneration(generation) {
		return generation
	}
	return GenerationUnknown
}

func validStage(stage Stage) bool {
	switch stage {
	case StageUnknown, StageSourcePartitionPlanning, StageObservationPublication,
		StageCandidateStrictOpen, StageDomainInventory,
		StageExtractorExecution, StageEvidenceStaging, StageFinalPublication,
		StageCallerPairExecution, StageCallerArtifactSeal,
		StageCallerArtifactInstall, StageCallerGenerationAdmission:
		return true
	default:
		return false
	}
}

func validGeneration(generation GenerationKind) bool {
	switch generation {
	case GenerationUnknown, GenerationSourcePartition,
		GenerationObservation, GenerationCandidate,
		GenerationExtractionDomain, GenerationCaller:
		return true
	default:
		return false
	}
}

func validClassification(classification Classification) bool {
	switch classification {
	case ClassificationLimit, ClassificationInvalid, ClassificationUnknown:
		return true
	default:
		return false
	}
}

func validDimension(dimension Dimension) bool {
	switch dimension {
	case DimensionUnknown, DimensionBlobBytes, DimensionBlobPlacements,
		DimensionPartitionCount, DimensionAggregateSegments,
		DimensionObservationMemberBytes,
		DimensionGenerationBlobBytes, DimensionGenerationEncodedBytes,
		DimensionGenerationArtifactBytes, DimensionGenerationControlBytes,
		DimensionRepositoryGenerations, DimensionRepositoryEntries,
		DimensionGenerationEntries,
		DimensionPlanningSpoolBytes,
		DimensionBatchHeaderBytes, DimensionBatchOutputBytes,
		DimensionGenerationRecords, DimensionInventoryFiles,
		DimensionInventoryPathBytes, DimensionSymlinkDepth,
		DimensionLookupRecords, DimensionLookupPathBytes,
		DimensionTreeRecordBytes, DimensionSourceBlobBytes,
		DimensionSourceReadAttempts, DimensionSourceReadBytes,
		DimensionSourceReadFiles, DimensionFacts, DimensionStagedRows,
		DimensionDomainResultFacts, DimensionDomainResultRows,
		DimensionDomainResultReferences, DimensionDomainCanonicalBytes,
		DimensionDomainEncodedBytes, DimensionCandidateMemberBytes,
		DimensionCandidateMembers, DimensionCallerPairResults,
		DimensionCallerPairAbstentions, DimensionCallerRecordBytes,
		DimensionCallerPairCanonicalBytes, DimensionCallerPairStagingBytes,
		DimensionCallerPairSourceBytes, DimensionCallerGenerationPairs,
		DimensionCallerGenerationArtifacts, DimensionCallerGenerationResults,
		DimensionCallerGenerationAbstentions,
		DimensionCallerGenerationCanonicalBytes,
		DimensionCallerGenerationStagingBytes:
		return true
	default:
		return false
	}
}
