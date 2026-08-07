package pipelinerefusal

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestLimitReceiptIsClosedAndPreservesCause(t *testing.T) {
	cause := errors.New("private /repo/path object deadbeef child stderr")
	err := Limit(
		cause, StageSourcePartitionPlanning, GenerationSourcePartition,
		DimensionGenerationBlobBytes, 4_294_967_297, 4_294_967_296,
	)
	if !errors.Is(err, cause) {
		t.Fatal("classified refusal lost its errors.Is cause")
	}
	for _, private := range []string{"/repo/path", "deadbeef", "child stderr"} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("rendered refusal leaked %q: %s", private, err)
		}
	}
	receipt, ok := From(err)
	if !ok || Validate(receipt) != nil || receipt.Stage != StageSourcePartitionPlanning ||
		receipt.GenerationKind != GenerationSourcePartition ||
		receipt.Classification != ClassificationLimit ||
		receipt.Dimension != DimensionGenerationBlobBytes ||
		receipt.Observed != 4_294_967_297 || receipt.Limit != 4_294_967_296 {
		t.Fatalf("receipt = %+v, present=%t", receipt, ok)
	}
}

func TestUnknownRefusalDoesNotInventScalars(t *testing.T) {
	cause := errors.New("raw diagnostic")
	err := Unknown(cause, StageCandidateStrictOpen, GenerationCandidate)
	receipt, ok := From(err)
	if !ok || receipt.Classification != ClassificationUnknown ||
		receipt.Dimension != DimensionUnknown || receipt.Observed != 0 ||
		receipt.Limit != 0 || strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("unknown refusal = %+v, %v", receipt, err)
	}
}

func TestMeasuredLimitIsPromotedOnlyAtBoundary(t *testing.T) {
	cause := errors.New("private record")
	measured := Measure(cause, DimensionGenerationRecords, 250_001, 250_000)
	if !errors.Is(measured, cause) || strings.Contains(measured.Error(), cause.Error()) {
		t.Fatalf("measurement = %v", measured)
	}
	err := At(measured, StageObservationPublication, GenerationObservation)
	receipt, ok := From(err)
	if !ok || receipt.Classification != ClassificationLimit ||
		receipt.Dimension != DimensionGenerationRecords ||
		receipt.Observed != 250_001 || receipt.Limit != 250_000 ||
		!errors.Is(err, cause) {
		t.Fatalf("promoted refusal = %+v, %v", receipt, err)
	}
}

func TestAtResealsPrivateOuterWrapperWithoutRelabeling(t *testing.T) {
	privateCause := errors.New("private object deadbeef")
	inner := Unknown(
		privateCause, StageCandidateStrictOpen, GenerationCandidate,
	)
	outer := fmt.Errorf("private /repository/path: %w", inner)
	sealed := At(
		outer, StageFinalPublication, GenerationExtractionDomain,
	)
	if !errors.Is(sealed, privateCause) || !errors.Is(sealed, outer) {
		t.Fatalf("sealed refusal lost causes: %v", sealed)
	}
	for _, private := range []string{"/repository/path", "deadbeef"} {
		if strings.Contains(sealed.Error(), private) {
			t.Fatalf("sealed refusal leaked %q: %v", private, sealed)
		}
	}
	receipt, ok := From(sealed)
	if !ok || receipt.Stage != StageCandidateStrictOpen ||
		receipt.GenerationKind != GenerationCandidate {
		t.Fatalf("sealed receipt = %+v, present=%t", receipt, ok)
	}
}

func TestInvalidReceiptFailsClosed(t *testing.T) {
	cause := errors.New("secret")
	err := Wrap(cause, Receipt{
		Stage: StageDomainInventory, GenerationKind: GenerationExtractionDomain,
		Classification: ClassificationLimit, Dimension: DimensionInventoryFiles,
		Observed: 1, Limit: 2,
	})
	receipt, ok := From(err)
	if !ok || receipt.Classification != ClassificationUnknown ||
		receipt.Stage != StageDomainInventory ||
		receipt.GenerationKind != GenerationExtractionDomain ||
		!errors.Is(err, cause) || !errors.Is(err, ErrInvalid) ||
		strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("invalid refusal fallback = %+v, %v", receipt, err)
	}
}

func TestInvalidBoundaryIdentityFallsBackToExplicitUnknown(t *testing.T) {
	cause := errors.New("private")
	err := Wrap(cause, Receipt{
		Stage: "invented", GenerationKind: "invented",
		Classification: ClassificationUnknown, Dimension: DimensionUnknown,
	})
	receipt, ok := From(err)
	if !ok || receipt.Stage != StageUnknown ||
		receipt.GenerationKind != GenerationUnknown ||
		receipt.Classification != ClassificationUnknown ||
		!errors.Is(err, ErrInvalid) || !errors.Is(err, cause) {
		t.Fatalf("fallback receipt = %+v, present=%t, error=%v", receipt, ok, err)
	}
}

func TestNonLimitClassificationsCarryNoInventedMeasurement(t *testing.T) {
	for _, classification := range []Classification{
		ClassificationUnknown, ClassificationInvalid,
	} {
		receipt := Receipt{
			Schema: Schema, Stage: StageCandidateStrictOpen,
			GenerationKind: GenerationCandidate,
			Classification: classification, Dimension: DimensionUnknown,
		}
		if err := Validate(receipt); err != nil {
			t.Fatalf("classification %q: %v", classification, err)
		}
	}
}

func TestInvalidMeasurementStaysSourceFreeUntilUnknownPromotion(t *testing.T) {
	privateCause := errors.New("private /origin/path object deadbeef")
	measured := Measure(
		privateCause, DimensionUnknown, 2, 1,
	)
	if !errors.Is(measured, ErrInvalid) || !errors.Is(measured, privateCause) {
		t.Fatalf("invalid measurement chain = %v", measured)
	}
	if strings.Contains(measured.Error(), "/origin/path") ||
		strings.Contains(measured.Error(), "deadbeef") {
		t.Fatalf("invalid measurement rendered private cause: %v", measured)
	}
	promoted := At(
		measured, StageExtractorExecution, GenerationExtractionDomain,
	)
	receipt, ok := From(promoted)
	if !ok || receipt.Classification != ClassificationUnknown ||
		receipt.Dimension != DimensionUnknown || receipt.Observed != 0 ||
		receipt.Limit != 0 || !errors.Is(promoted, privateCause) {
		t.Fatalf("promoted invalid measurement = %+v, present=%t", receipt, ok)
	}
}

func TestValidateFrozenSets(t *testing.T) {
	for _, boundary := range []struct {
		stage      Stage
		generation GenerationKind
	}{
		{StageSourcePartitionPlanning, GenerationSourcePartition},
		{StageObservationPublication, GenerationObservation},
		{StageCandidateStrictOpen, GenerationCandidate},
		{StageDomainInventory, GenerationExtractionDomain},
		{StageExtractorExecution, GenerationExtractionDomain},
		{StageEvidenceStaging, GenerationExtractionDomain},
		{StageFinalPublication, GenerationExtractionDomain},
	} {
		receipt := Receipt{
			Schema: Schema, Stage: boundary.stage,
			GenerationKind: boundary.generation,
			Classification: ClassificationUnknown, Dimension: DimensionUnknown,
		}
		if err := Validate(receipt); err != nil {
			t.Fatalf("stage %q: %v", boundary.stage, err)
		}
	}
}

func TestValidateRejectsCrossedBoundaryAuthority(t *testing.T) {
	tests := []Receipt{
		{
			Schema: Schema, Stage: StageSourcePartitionPlanning,
			GenerationKind: GenerationExtractionDomain,
			Classification: ClassificationUnknown, Dimension: DimensionUnknown,
		},
		{
			Schema: Schema, Stage: StageDomainInventory,
			GenerationKind: GenerationExtractionDomain,
			Classification: ClassificationLimit,
			Dimension:      DimensionGenerationRecords, Observed: 2, Limit: 1,
		},
		{
			Schema: Schema, Stage: StageFinalPublication,
			GenerationKind: GenerationExtractionDomain,
			Classification: ClassificationLimit,
			Dimension:      DimensionStagedRows, Observed: 2, Limit: 1,
		},
	}
	for _, receipt := range tests {
		if err := Validate(receipt); !errors.Is(err, ErrInvalid) {
			t.Fatalf("crossed receipt %+v validated: %v", receipt, err)
		}
	}
}

func TestExtractorExecutionOwnsLazyAttributionLookupLimits(t *testing.T) {
	for _, dimension := range []Dimension{
		DimensionLookupRecords,
		DimensionLookupPathBytes,
		DimensionTreeRecordBytes,
	} {
		receipt := Receipt{
			Schema: Schema, Stage: StageExtractorExecution,
			GenerationKind: GenerationExtractionDomain,
			Classification: ClassificationLimit, Dimension: dimension,
			Observed: 2, Limit: 1,
		}
		if err := Validate(receipt); err != nil {
			t.Fatalf("extractor attribution dimension %q: %v", dimension, err)
		}
	}
}

func TestInvalidBoundaryPairFallsBackToFullyUnknownAuthority(t *testing.T) {
	err := Wrap(errors.New("private"), Receipt{
		Stage:          StageSourcePartitionPlanning,
		GenerationKind: GenerationExtractionDomain,
		Classification: ClassificationUnknown, Dimension: DimensionUnknown,
	})
	receipt, ok := From(err)
	if !ok || receipt.Stage != StageUnknown ||
		receipt.GenerationKind != GenerationUnknown ||
		receipt.Classification != ClassificationUnknown ||
		!errors.Is(err, ErrInvalid) || Validate(receipt) != nil {
		t.Fatalf("crossed fallback = %+v, present=%t, error=%v", receipt, ok, err)
	}
}
