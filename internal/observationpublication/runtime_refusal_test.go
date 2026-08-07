package observationpublication

import (
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

func TestRuntimeBoundaryRefusalsAreClosedAndPreserveCauses(t *testing.T) {
	privateCause := errors.New("private /source/path object deadbeef child stderr")
	tests := []struct {
		name           string
		err            error
		stage          pipelinerefusal.Stage
		generation     pipelinerefusal.GenerationKind
		classification pipelinerefusal.Classification
		dimension      pipelinerefusal.Dimension
		observed       int64
		limit          int64
	}{
		{
			name: "source partition generation bytes",
			err: planningFailure(pipelinerefusal.Measure(
				errors.Join(sourcepartition.ErrTooLarge, privateCause),
				pipelinerefusal.DimensionGenerationBlobBytes,
				sourcepartition.MaxGenerationBlobBytes+1,
				sourcepartition.MaxGenerationBlobBytes,
			)),
			stage:          pipelinerefusal.StageSourcePartitionPlanning,
			generation:     pipelinerefusal.GenerationSourcePartition,
			classification: pipelinerefusal.ClassificationLimit,
			dimension:      pipelinerefusal.DimensionGenerationBlobBytes,
			observed:       sourcepartition.MaxGenerationBlobBytes + 1,
			limit:          sourcepartition.MaxGenerationBlobBytes,
		},
		{
			name: "observation record count",
			err: workFailure(pipelinerefusal.Measure(
				errors.Join(ErrLimit, privateCause),
				pipelinerefusal.DimensionGenerationRecords,
				MaxGenerationRecords+1, MaxGenerationRecords,
			)),
			stage:          pipelinerefusal.StageObservationPublication,
			generation:     pipelinerefusal.GenerationObservation,
			classification: pipelinerefusal.ClassificationLimit,
			dimension:      pipelinerefusal.DimensionGenerationRecords,
			observed:       MaxGenerationRecords + 1,
			limit:          MaxGenerationRecords,
		},
		{
			name:           "unclassified observation failure",
			err:            workFailure(privateCause),
			stage:          pipelinerefusal.StageObservationPublication,
			generation:     pipelinerefusal.GenerationObservation,
			classification: pipelinerefusal.ClassificationUnknown,
			dimension:      pipelinerefusal.DimensionUnknown,
		},
		{
			name: "invalid source partition",
			err: planningFailure(errors.Join(
				sourcepartition.ErrInvalid, privateCause,
			)),
			stage:          pipelinerefusal.StageSourcePartitionPlanning,
			generation:     pipelinerefusal.GenerationSourcePartition,
			classification: pipelinerefusal.ClassificationInvalid,
			dimension:      pipelinerefusal.DimensionUnknown,
		},
		{
			name: "invalid observation publication",
			err: workFailure(errors.Join(
				ErrInvalid, privateCause,
			)),
			stage:          pipelinerefusal.StageObservationPublication,
			generation:     pipelinerefusal.GenerationObservation,
			classification: pipelinerefusal.ClassificationInvalid,
			dimension:      pipelinerefusal.DimensionUnknown,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if !errors.Is(testCase.err, privateCause) ||
				!errors.Is(testCase.err, ErrWorkUnavailable) {
				t.Fatalf("refusal lost cause: %v", testCase.err)
			}
			for _, private := range []string{"/source/path", "deadbeef", "child stderr"} {
				if strings.Contains(testCase.err.Error(), private) {
					t.Fatalf("refusal leaked %q: %v", private, testCase.err)
				}
			}
			receipt, ok := pipelinerefusal.From(testCase.err)
			if !ok || receipt.Stage != testCase.stage ||
				receipt.GenerationKind != testCase.generation ||
				receipt.Classification != testCase.classification ||
				receipt.Dimension != testCase.dimension ||
				receipt.Observed != testCase.observed ||
				receipt.Limit != testCase.limit {
				t.Fatalf("receipt = %+v, present=%t", receipt, ok)
			}
		})
	}
}

func TestObservationLimitsAcceptExactAndRefuseOneOver(t *testing.T) {
	tests := []struct {
		name      string
		dimension pipelinerefusal.Dimension
		limit     int64
	}{
		{name: "generation records", dimension: pipelinerefusal.DimensionGenerationRecords, limit: MaxGenerationRecords},
		{name: "repository generations", dimension: pipelinerefusal.DimensionRepositoryGenerations, limit: MaxRepositoryGenerations},
		{name: "repository entries", dimension: pipelinerefusal.DimensionRepositoryEntries, limit: MaxRepositoryGenerations + 2},
		{name: "generation entries", dimension: pipelinerefusal.DimensionGenerationEntries, limit: 12},
		{name: "observation member bytes", dimension: pipelinerefusal.DimensionObservationMemberBytes, limit: MaxMemberBytes},
		{name: "generation artifact bytes", dimension: pipelinerefusal.DimensionGenerationArtifactBytes, limit: MaxGenerationBytes},
		{name: "generation encoded bytes", dimension: pipelinerefusal.DimensionGenerationEncodedBytes, limit: MaxGenerationBytes},
		{name: "generation control bytes", dimension: pipelinerefusal.DimensionGenerationControlBytes, limit: MaxManifestBytes},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			for _, observed := range []int64{testCase.limit - 1, testCase.limit} {
				if err := checkPublicationLimit(
					ErrLimit, testCase.dimension, observed, testCase.limit,
				); err != nil {
					t.Fatalf("observed %d: %v", observed, err)
				}
			}
			err := checkPublicationLimit(
				ErrLimit, testCase.dimension, testCase.limit+1, testCase.limit,
			)
			var measurement *pipelinerefusal.Measurement
			if !errors.Is(err, ErrLimit) || !errors.As(err, &measurement) ||
				measurement == nil || measurement.Dimension != testCase.dimension ||
				measurement.Observed != testCase.limit+1 ||
				measurement.Limit != testCase.limit {
				t.Fatalf("one-over refusal = %v, measurement=%+v", err, measurement)
			}
		})
	}
}
