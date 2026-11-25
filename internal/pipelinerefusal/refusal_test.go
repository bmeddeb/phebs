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

// PHEBS-43: inventory v2 plans source partitions inside observation
// publication, so a planning limit keeps its measurement at that stage.
func TestObservationStageKeepsSourcePlanningLimits(t *testing.T) {
	for _, dimension := range []Dimension{
		DimensionBlobBytes, DimensionBlobPlacements, DimensionPartitionCount,
		DimensionAggregateSegments, DimensionGenerationBlobBytes, DimensionPlanningSpoolBytes,
	} {
		err := At(
			Measure(errors.New("private"), dimension, 6_051_418, 4<<20),
			StageObservationPublication, GenerationObservation,
		)
		receipt, ok := From(err)
		if !ok || receipt.Classification != ClassificationLimit || receipt.Dimension != dimension ||
			receipt.Observed != 6_051_418 || receipt.Limit != 4<<20 {
			t.Fatalf("%s at observation = %+v, %v", dimension, receipt, ok)
		}
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

func TestParseDurableErrorTextAcceptsOnlyCanonicalClosedReceipt(t *testing.T) {
	err := Limit(
		errors.New("private"), StageDomainInventory, GenerationExtractionDomain,
		DimensionCandidateMemberBytes, 792_000_000, 64<<20,
	)
	receipt, ok := ParseDurableErrorText(err.Error())
	if !ok || receipt.Dimension != DimensionCandidateMemberBytes ||
		receipt.Observed != 792_000_000 || receipt.Limit != 64<<20 {
		t.Fatalf("parsed receipt = %+v, present=%t", receipt, ok)
	}
	for _, invalid := range []string{
		"outer: " + err.Error(), err.Error() + " ",
		strings.Replace(err.Error(), `"limit":67108864`, `"limit":67108864,"private":"x"`, 1),
		"private raw error",
	} {
		if _, present := ParseDurableErrorText(invalid); present {
			t.Fatalf("accepted noncanonical durable error %q", invalid)
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
		{StageCallerPairExecution, GenerationCaller},
		{StageCallerArtifactSeal, GenerationCaller},
		{StageCallerArtifactInstall, GenerationCaller},
		{StageCallerGenerationAdmission, GenerationCaller},
		{StageRelationshipKafkaProjection, GenerationRelationship},
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

func TestCallerStagesOwnOnlyTheirExactLimitDimensions(t *testing.T) {
	tests := []struct {
		stage     Stage
		dimension Dimension
	}{
		{StageCallerPairExecution, DimensionCallerPairAbstentions},
		{StageCallerArtifactSeal, DimensionCallerPairStagingBytes},
		{StageCallerArtifactInstall, DimensionCallerPairCanonicalBytes},
		{StageCallerGenerationAdmission, DimensionCallerGenerationAbstentions},
	}
	for _, test := range tests {
		receipt := Receipt{
			Schema: Schema, Stage: test.stage, GenerationKind: GenerationCaller,
			Classification: ClassificationLimit, Dimension: test.dimension,
			Observed: 2, Limit: 1,
		}
		if err := Validate(receipt); err != nil {
			t.Fatalf("caller stage %q dimension %q: %v", test.stage, test.dimension, err)
		}
	}
	crossed := Receipt{
		Schema: Schema, Stage: StageCallerPairExecution, GenerationKind: GenerationCaller,
		Classification: ClassificationLimit,
		Dimension:      DimensionCallerGenerationAbstentions, Observed: 2, Limit: 1,
	}
	if err := Validate(crossed); !errors.Is(err, ErrInvalid) {
		t.Fatalf("crossed caller dimension validated: %v", err)
	}
}

func TestRelationshipComponentStagesOwnResidentBytes(t *testing.T) {
	for _, stage := range []Stage{
		StageRelationshipResolverNamespaces,
		StageRelationshipRPCPostings,
		StageRelationshipKafkaProjection,
	} {
		t.Run(string(stage), func(t *testing.T) {
			receipt := Receipt{
				Schema: Schema, Stage: stage,
				GenerationKind: GenerationRelationship,
				Classification: ClassificationLimit, Dimension: DimensionResidentBytes,
				Observed: 128<<20 + 1, Limit: 128 << 20,
			}
			if err := Validate(receipt); err != nil {
				t.Fatal(err)
			}
			receipt.Dimension = DimensionGenerationEncodedBytes
			if err := Validate(receipt); !errors.Is(err, ErrInvalid) {
				t.Fatalf("relationship component accepted foreign dimension: %v", err)
			}
		})
	}
}

func TestEvidenceStagingOwnsEncodedByteLimit(t *testing.T) {
	receipt := Receipt{
		Schema: Schema, Stage: StageEvidenceStaging,
		GenerationKind: GenerationExtractionDomain,
		Classification: ClassificationLimit,
		Dimension:      DimensionGenerationEncodedBytes, Observed: 2, Limit: 1,
	}
	if err := Validate(receipt); err != nil {
		t.Fatalf("encoded staging receipt = %v", err)
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

func TestUnsupportedInputIsPromotedOnlyAtExtractorExecution(t *testing.T) {
	private := errors.New("generated document private/path.go: private header")
	for _, dimension := range []Dimension{DimensionSCIPProducer, DimensionGeneratorProvenance} {
		marked := UnsupportedInput(private, dimension)
		if marked.Error() != private.Error() || !errors.Is(marked, private) {
			t.Fatalf("UnsupportedInput changed the in-process cause: %v", marked)
		}
		closed := At(
			fmt.Errorf("outer %s: %w", "private/repo", marked),
			StageExtractorExecution, GenerationExtractionDomain,
		)
		receipt, ok := From(closed)
		if !ok || receipt.Classification != ClassificationUnsupportedInput ||
			receipt.Dimension != dimension || receipt.Observed != 0 || receipt.Limit != 0 {
			t.Fatalf("At(%s) = %+v, %v", dimension, receipt, ok)
		}
		if strings.Contains(closed.Error(), "private") || !errors.Is(closed, private) {
			t.Fatalf("closed admission refusal leaked or lost its cause: %v", closed)
		}
		parsed, ok := ParseDurableErrorText(closed.Error())
		if !ok || parsed != receipt {
			t.Fatalf("durable admission refusal did not round-trip: %+v, %v", parsed, ok)
		}
		other, _ := From(At(marked, StageEvidenceStaging, GenerationExtractionDomain))
		if other.Classification != ClassificationUnknown || other.Dimension != DimensionUnknown {
			t.Fatalf("admission refusal outside extractor execution = %+v", other)
		}
	}
	if unmarked := UnsupportedInput(private, DimensionStagedRows); unmarked != private {
		t.Fatalf("UnsupportedInput accepted a non-admission dimension: %v", unmarked)
	}
	if UnsupportedInput(nil, DimensionSCIPProducer) != nil {
		t.Fatal("UnsupportedInput(nil) is not nil")
	}
}

func TestUnsupportedInputReceiptShape(t *testing.T) {
	valid := Receipt{
		Schema: Schema, Stage: StageExtractorExecution,
		GenerationKind: GenerationExtractionDomain,
		Classification: ClassificationUnsupportedInput,
		Dimension:      DimensionGeneratorProvenance,
	}
	if err := Validate(valid); err != nil {
		t.Fatalf("valid admission receipt: %v", err)
	}
	for name, mutate := range map[string]func(*Receipt){
		"unknown dimension":  func(r *Receipt) { r.Dimension = DimensionUnknown },
		"limit dimension":    func(r *Receipt) { r.Dimension = DimensionStagedRows },
		"other stage":        func(r *Receipt) { r.Stage = StageEvidenceStaging },
		"invented scalar":    func(r *Receipt) { r.Observed, r.Limit = 2, 1 },
		"crossed generation": func(r *Receipt) { r.GenerationKind = GenerationCaller },
	} {
		receipt := valid
		mutate(&receipt)
		if Validate(receipt) == nil {
			t.Fatalf("%s: admission receipt validated: %+v", name, receipt)
		}
	}
	for _, dimension := range []Dimension{DimensionSCIPProducer, DimensionGeneratorProvenance} {
		limit := Receipt{
			Schema: Schema, Stage: StageExtractorExecution,
			GenerationKind: GenerationExtractionDomain,
			Classification: ClassificationLimit, Dimension: dimension,
			Observed: 2, Limit: 1,
		}
		if Validate(limit) == nil {
			t.Fatalf("admission dimension %q validated as a limit", dimension)
		}
	}
}
