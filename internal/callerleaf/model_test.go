package callerleaf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
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
		dimension pipelinerefusal.Dimension
		observed  int64
		limit     int64
	}{
		{"pair", AggregateReceipt{PairCount: MaxExpectedPairs}, Receipt{}, pipelinerefusal.DimensionCallerGenerationPairs, MaxExpectedPairs + 1, MaxExpectedPairs},
		{"result", AggregateReceipt{ResultCount: MaxAggregateResultRecords}, Receipt{ResultCount: 1}, pipelinerefusal.DimensionCallerGenerationResults, MaxAggregateResultRecords + 1, MaxAggregateResultRecords},
		{"abstention", AggregateReceipt{AbstentionCount: MaxAggregateAbstentionRecords}, Receipt{AbstentionCount: 1}, pipelinerefusal.DimensionCallerGenerationAbstentions, MaxAggregateAbstentionRecords + 1, MaxAggregateAbstentionRecords},
		{"content", AggregateReceipt{CanonicalBytes: MaxAggregateCanonicalBytes}, Receipt{ContentBytes: 1}, pipelinerefusal.DimensionCallerGenerationCanonicalBytes, MaxAggregateCanonicalBytes + 1, MaxAggregateCanonicalBytes},
		{"stage", AggregateReceipt{StagingBytes: MaxAggregateStagingBytes}, Receipt{StagingBytes: 1}, pipelinerefusal.DimensionCallerGenerationStagingBytes, MaxAggregateStagingBytes + 1, MaxAggregateStagingBytes},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.aggregate.Add(test.receipt)
			var measurement *pipelinerefusal.Measurement
			if !errors.Is(err, ErrLimit) || !errors.As(err, &measurement) ||
				measurement.Dimension != test.dimension ||
				measurement.Observed != test.observed || measurement.Limit != test.limit {
				t.Fatalf("Add = %v, want ErrLimit", err)
			}
		})
	}
}

func TestLeafAdapterV2PreservesHistoricalPolicyEncoding(t *testing.T) {
	legacy, err := json.Marshal(FrozenPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "coverage") ||
		FrozenPolicy().Name != PolicyName || FrozenPolicy().LeafAdapter != LeafAdapterV1 {
		t.Fatalf("historical policy encoding changed: %s", legacy)
	}
	current := CurrentPolicy()
	if current.Name != PolicyNameV2 || current.LeafAdapter != LeafAdapterV2 ||
		current.MaxCoverageRecordsPerPair != 1 ||
		current.MaxAggregateCoverageRecords != MaxExpectedPairs ||
		current.MaxAggregateCoveredCandidates != MaxAggregateCoveredCandidates {
		t.Fatalf("current policy = %+v", current)
	}
	legacyGeneration, _ := testIdentity(t)
	if err := ValidateGenerationIdentity(legacyGeneration); err != nil {
		t.Fatalf("historical V1 generation no longer validates: %v", err)
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

func TestGenerationV2BindsCanonicalUsableUpstreamAuthority(t *testing.T) {
	generation, _ := testIdentity(t)
	digest := func(char string) string { return "sha256:" + strings.Repeat(char, 64) }
	type testObservation struct {
		Version                     string `json:"version"`
		Repository                  string `json:"repository"`
		SourceGenerationDigest      string `json:"source_generation_digest"`
		SourceRootDigest            string `json:"source_root_digest"`
		ObservationGenerationDigest string `json:"observation_generation_digest"`
		ObservationRootDigest       string `json:"observation_root_digest"`
		PartitionPolicyDigest       string `json:"partition_policy_digest"`
		ObservationPolicyDigest     string `json:"observation_policy_digest"`
		InventoryPolicyDigest       string `json:"inventory_policy_digest,omitempty"`
		RecordCount                 int    `json:"record_count"`
		ObservedCount               int    `json:"observed_count"`
	}
	type testRequired struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	}
	type testUpstream struct {
		Schema      string                                `json:"schema"`
		Repository  string                                `json:"repository"`
		Observation testObservation                       `json:"observation"`
		Required    []testRequired                        `json:"required"`
		Domains     []candidate.DownstreamDomainAuthority `json:"domains"`
		Digest      string                                `json:"digest"`
	}
	observation := testObservation{
		Version: "observation-v2", Repository: generation.Repository,
		SourceGenerationDigest: digest("1"), SourceRootDigest: digest("2"),
		ObservationGenerationDigest: digest("3"), ObservationRootDigest: digest("4"),
		PartitionPolicyDigest: digest("5"), ObservationPolicyDigest: digest("6"),
		InventoryPolicyDigest: digest("7"), RecordCount: 8, ObservedCount: 7,
	}
	domain := candidate.DownstreamDomainAuthority{
		Domain: "grpc-caller", Version: "1.5.0", PlanDigest: digest("8"), RootDigest: digest("9"),
		RunID: "caller-run", Disposition: candidate.PartitionResultEmpty,
		CandidateManifestDigest: digest("a"), CandidatePartitionRootDigest: digest("b"),
		CandidatePolicyDigest: digest("c"), SourceGenerationDigest: observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      digest("d"), DomainIndexDigest: digest("e"),
		DomainScheduleDigest: digest("f"),
	}
	upstream := testUpstream{
		Schema: "phebs-downstream-upstream-authority-v1", Repository: generation.Repository,
		Observation: observation,
		Required:    []testRequired{{Domain: domain.Domain, Version: domain.Version}},
		Domains:     []candidate.DownstreamDomainAuthority{domain},
	}
	unsigned, err := json.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte("phebs-downstream-upstream-authority-v1\x00"), unsigned...))
	upstream.Digest = "sha256:" + hex.EncodeToString(sum[:])
	raw, err := json.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}
	generation.Upstream, generation.UpstreamDigest = raw, upstream.Digest
	v2, err := NewGenerationIdentity(generation)
	if err != nil || v2.Schema != GenerationSchemaV2 || ValidateGenerationIdentity(v2) != nil {
		t.Fatalf("v2 generation = %+v, %v", v2, err)
	}
	want := digestFields(
		"phebs-caller-generation-v2\x00",
		v2.Repository, v2.HeadCommit, v2.UnitDigest,
		v2.DeclarationSetDigest, v2.CandidateManifestDigest,
		v2.CandidatePolicyDigest, v2.SourceLanePolicy,
		v2.ResolverGenerationDigest, v2.ResolverManifestDigest,
		v2.CallerPolicyDigest, v2.ExtractorSetDigest, v2.UpstreamDigest,
	)
	if v2.Digest != want {
		t.Fatalf("v2 digest = %q, want store-compatible %q", v2.Digest, want)
	}

	// A complete unavailable-prerequisite root is explicit gap authority. The
	// typed downstream contract accepts it, so the dependency-low canonical
	// validator used by caller artifacts must make the same decision.
	upstream.Domains[0].Disposition = candidate.PartitionResultUnavailablePrerequisite
	upstream.Digest = ""
	unsigned, err = json.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(append([]byte("phebs-downstream-upstream-authority-v1\x00"), unsigned...))
	upstream.Digest = "sha256:" + hex.EncodeToString(sum[:])
	generation.Upstream, err = json.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}
	generation.UpstreamDigest = upstream.Digest
	if gap, gapErr := NewGenerationIdentity(generation); gapErr != nil ||
		gap.Schema != GenerationSchemaV2 || ValidateGenerationIdentity(gap) != nil {
		t.Fatalf("explicit-gap v2 generation = %+v, %v", gap, gapErr)
	}

	detached := generation
	detached.UpstreamDigest = digest("0")
	if _, err := NewGenerationIdentity(detached); err == nil {
		t.Fatal("detached upstream digest was accepted")
	}
	mutated := upstream
	mutated.Observation.ObservationRootDigest = digest("0")
	detached.Upstream, _ = json.Marshal(mutated)
	detached.UpstreamDigest = upstream.Digest
	if _, err := NewGenerationIdentity(detached); err == nil {
		t.Fatal("mutated embedded authority with unchanged digest was accepted")
	}
}
