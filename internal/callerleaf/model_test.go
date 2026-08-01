package callerleaf

import (
	"errors"
	"testing"
)

func TestAggregateReceiptCapAndCapPlusOne(t *testing.T) {
	if MaxOpenFiles != 5 || FrozenPolicy().MaxOpenFiles != 5 {
		t.Fatalf(
			"structural descriptor/pipe peak = (%d, %d), want 5",
			MaxOpenFiles, FrozenPolicy().MaxOpenFiles,
		)
	}
	receipt := Receipt{ResultCount: 1, AbstentionCount: 1, ContentBytes: 2, StagingBytes: 2}
	aggregate := AggregateReceipt{}
	if err := aggregate.Add(receipt); err != nil {
		t.Fatal(err)
	}
	if aggregate.PairCount != 1 || aggregate.ArtifactCount != 1 ||
		aggregate.ResultCount != 1 || aggregate.AbstentionCount != 1 ||
		aggregate.CanonicalBytes != 2 || aggregate.PeakOpenFiles != MaxOpenFiles {
		t.Fatalf("aggregate = %+v", aggregate)
	}
	for _, test := range []struct {
		name      string
		aggregate AggregateReceipt
		receipt   Receipt
	}{
		{"pair", AggregateReceipt{PairCount: MaxExpectedPairs}, Receipt{}},
		{"result", AggregateReceipt{ResultCount: MaxAggregateResultRecords}, Receipt{ResultCount: 1}},
		{"abstention", AggregateReceipt{AbstentionCount: MaxAggregateAbstentionRecords}, Receipt{AbstentionCount: 1}},
		{"content", AggregateReceipt{CanonicalBytes: MaxAggregateCanonicalBytes}, Receipt{ContentBytes: 1}},
		{"stage", AggregateReceipt{StagingBytes: MaxAggregateStagingBytes}, Receipt{StagingBytes: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.aggregate.Add(test.receipt); !errors.Is(err, ErrLimit) {
				t.Fatalf("Add = %v, want ErrLimit", err)
			}
		})
	}
}

func TestGenerationValidationRejectsNoncanonicalPolicyEnvelope(t *testing.T) {
	generation, _ := testIdentity(t)
	for _, mutate := range []func(*GenerationIdentity){
		func(value *GenerationIdentity) { value.Schema = "forged" },
		func(value *GenerationIdentity) { value.SourceLanePolicy = "forged" },
		func(value *GenerationIdentity) { value.CallerPolicy.MaxOpenFiles-- },
	} {
		forged := generation
		mutate(&forged)
		if err := ValidateGenerationIdentity(forged); err == nil {
			t.Fatalf("ValidateGenerationIdentity accepted forged envelope: %+v", forged)
		}
	}
}
