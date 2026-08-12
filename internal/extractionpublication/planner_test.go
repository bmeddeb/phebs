package extractionpublication

import (
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestCandidateMemberAggregateMeasurementPinsFrozenStructuralProfile(t *testing.T) {
	partitions := make([]candidate.ExtractionPartition, 489)
	for index := range partitions {
		bytes := int64(4096 * 396)
		if index == len(partitions)-1 {
			bytes = 1152 * 396
		}
		partitions[index].Member = &candidate.Artifact{ContentBytes: bytes}
	}
	err := measureCandidateMemberBytes(partitions, candidate.MaxDomainResultMemberBytes)
	var measurement *pipelinerefusal.Measurement
	if !errors.Is(err, ErrLimit) || !errors.Is(err, candidate.ErrInvalidDomainResult) ||
		!errors.As(err, &measurement) ||
		measurement.Dimension != pipelinerefusal.DimensionCandidateMemberBytes ||
		measurement.Observed != 792_000_000 || measurement.Limit != 67_108_864 {
		t.Fatalf("measurement = %+v, error=%v", measurement, err)
	}
}

func TestCandidateMemberAggregateExactBoundAndOneOver(t *testing.T) {
	for _, test := range []struct {
		name      string
		bytes     int64
		wantLimit bool
	}{
		{name: "exact", bytes: candidate.MaxDomainResultMemberBytes},
		{name: "one over", bytes: candidate.MaxDomainResultMemberBytes + 1, wantLimit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := measureCandidateMemberBytes([]candidate.ExtractionPartition{{
				Member: &candidate.Artifact{ContentBytes: test.bytes},
			}}, candidate.MaxDomainResultMemberBytes)
			if errors.Is(err, ErrLimit) != test.wantLimit {
				t.Fatalf("error = %v, want limit=%t", err, test.wantLimit)
			}
		})
	}
}

func TestPlanningLimitIsClosedTerminalAndDurable(t *testing.T) {
	private := errors.New("private repository/path")
	measured := pipelinerefusal.Measure(
		private, pipelinerefusal.DimensionCandidateMemberBytes,
		792_000_000, 67_108_864,
	)
	err := terminalPlanFailure(errors.Join(ErrLimit, measured))
	if !store.IsTerminal(err) || !errors.Is(err, private) {
		t.Fatalf("terminal error = %v", err)
	}
	receipt, ok := pipelinerefusal.From(err)
	if !ok || receipt.Stage != pipelinerefusal.StageDomainInventory ||
		receipt.GenerationKind != pipelinerefusal.GenerationExtractionDomain ||
		receipt.Classification != pipelinerefusal.ClassificationLimit ||
		receipt.Dimension != pipelinerefusal.DimensionCandidateMemberBytes ||
		strings.Contains(store.DurableErrorText(err), "private") {
		t.Fatalf("durable refusal = %+v text=%q", receipt, store.DurableErrorText(err))
	}
}
