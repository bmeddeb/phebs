package callerexecute

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/spike/t401"
)

const (
	t40r1CompactCoverageSchema = "t4013-take19-caller-compact-coverage-v1"
	t40r1CompactCoverageEnv    = "T40R1_CALLER_COMPACT_COVERAGE_DIAGNOSTIC"
)

type t40r1CompactPairReceipt struct {
	Protocol          string `json:"protocol"`
	CandidateRecords  int    `json:"candidate_records"`
	CoverageRecords   int    `json:"coverage_records"`
	CoveredCandidates int    `json:"covered_candidates"`
	CoverageGapKinds  int    `json:"coverage_gap_kinds"`
	ResultRecords     int    `json:"result_records"`
	AbstentionRecords int    `json:"abstention_records"`
	CanonicalBytes    int64  `json:"canonical_bytes"`
	StagingBytes      int64  `json:"staging_bytes"`
	SourceBlobReads   int    `json:"source_blob_reads"`
	SourceBlobBytes   int64  `json:"source_blob_bytes"`
	OutOfLeafReads    int    `json:"out_of_leaf_reads"`
}

type t40r1CompactGenerationBound struct {
	CoverageRecords   int   `json:"coverage_records"`
	CoveredCandidates int   `json:"covered_candidates"`
	CanonicalBytes    int64 `json:"canonical_bytes"`
	StagingBytes      int64 `json:"staging_bytes"`
}

type t40r1CompactProfileProof struct {
	Profile                     string `json:"profile"`
	ProfileDigest               string `json:"profile_digest"`
	CandidateInputs             int64  `json:"candidate_inputs"`
	Protocols                   int64  `json:"protocols"`
	ExactCoveredCandidateCount  int64  `json:"exact_covered_candidate_count"`
	PhysicalCoverageRecordUpper int    `json:"physical_coverage_record_upper"`
	AggregateCoverageBoundsFit  bool   `json:"aggregate_coverage_bounds_fit"`
	AggregateCanonicalBoundsFit bool   `json:"aggregate_canonical_bounds_fit"`
	AggregateStagingBoundsFit   bool   `json:"aggregate_staging_bounds_fit"`
}

type t40r1CompactCoverageMeasurement struct {
	Schema                 string                      `json:"schema"`
	Method                 string                      `json:"method"`
	LeafAdapter            string                      `json:"leaf_adapter"`
	Policy                 string                      `json:"policy"`
	PairSamples            []t40r1CompactPairReceipt   `json:"pair_samples"`
	WorstCaseGeneration    t40r1CompactGenerationBound `json:"worst_case_generation"`
	Profiles               []t40r1CompactProfileProof  `json:"profiles"`
	ClearsScopedCallerGate bool                        `json:"clears_scoped_caller_gate"`
	EstablishesScalePass   bool                        `json:"establishes_scale_pass"`
	AuthorizesFreeze       bool                        `json:"authorizes_freeze"`
}

func TestT40R1CompactCallerCoverageMeasurement(t *testing.T) {
	measurement := measureT40R1CompactCallerCoverage(t)
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "spike", "t4013", "take19-caller-compact-coverage.json",
	))
	if err != nil {
		encoded, _ := json.MarshalIndent(measurement, "", "  ")
		t.Fatalf("retained compact caller measurement unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1CompactCoverageMeasurement
	if json.Unmarshal(raw, &retained) != nil || !reflect.DeepEqual(retained, measurement) {
		encoded, _ := json.MarshalIndent(measurement, "", "  ")
		t.Fatalf("retained compact caller measurement differs; exact measurement:\n%s", encoded)
	}
	if os.Getenv(t40r1CompactCoverageEnv) == "1" {
		encoded, marshalErr := json.MarshalIndent(measurement, "", "  ")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		t.Log(string(encoded))
	}
}

func measureT40R1CompactCallerCoverage(
	t *testing.T,
) t40r1CompactCoverageMeasurement {
	t.Helper()
	protocols := []struct{ domain, protocol string }{
		{domain: "grpc-caller", protocol: "grpc"},
		{domain: "thrift-caller", protocol: "thrift"},
	}
	measurement := t40r1CompactCoverageMeasurement{
		Schema:      t40r1CompactCoverageSchema,
		Method:      "production ExecutePair replays one exact maximal 4,096-record candidate member with the maximum legal member-name length and all four gap kinds for each caller protocol under an exact zero-descriptor resolver; the unchanged 16,384-pair ceiling then provides the conservative physical-record and byte upper bound while frozen profile cardinalities provide exact logical coverage",
		LeafAdapter: callerleaf.CurrentLeafAdapter,
		Policy:      callerleaf.CurrentPolicy().Name,
	}
	maxCanonical := int64(0)
	maxStaging := int64(0)
	for _, protocol := range protocols {
		receipt, gapKinds := executeT40R1CompactMaxPair(
			t, protocol.domain, protocol.protocol,
		)
		measurement.PairSamples = append(measurement.PairSamples, t40r1CompactPairReceipt{
			Protocol: protocol.protocol, CandidateRecords: candidate.MaxRecordsPerArtifact,
			CoverageRecords:   receipt.CoverageRecordCount,
			CoveredCandidates: receipt.CoveredCandidateCount,
			CoverageGapKinds:  gapKinds,
			ResultRecords:     receipt.ResultCount, AbstentionRecords: receipt.AbstentionCount,
			CanonicalBytes: receipt.ContentBytes, StagingBytes: receipt.StagingBytes,
			SourceBlobReads: receipt.SourceBlobReads, SourceBlobBytes: receipt.SourceBlobBytes,
			OutOfLeafReads: receipt.OutOfLeafReads,
		})
		maxCanonical = max(maxCanonical, receipt.ContentBytes)
		maxStaging = max(maxStaging, receipt.StagingBytes)
	}
	measurement.WorstCaseGeneration = t40r1CompactGenerationBound{
		CoverageRecords:   callerleaf.MaxExpectedPairs,
		CoveredCandidates: callerleaf.MaxAggregateCoveredCandidates,
		CanonicalBytes:    maxCanonical * int64(callerleaf.MaxExpectedPairs),
		StagingBytes:      maxStaging * int64(callerleaf.MaxExpectedPairs),
	}
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		profileDigest, digestErr := t401.ProfileDigest(profile)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		candidateInputs := int64(0)
		for _, fixture := range t40r1CallerFixtures(t, profile) {
			candidateInputs += int64(fixture.inputs)
		}
		exactCovered := candidateInputs * int64(len(protocols))
		proof := t40r1CompactProfileProof{
			Profile: profile.Name, ProfileDigest: profileDigest,
			CandidateInputs: candidateInputs, Protocols: int64(len(protocols)),
			ExactCoveredCandidateCount:  exactCovered,
			PhysicalCoverageRecordUpper: callerleaf.MaxExpectedPairs,
			AggregateCoverageBoundsFit: exactCovered <= int64(callerleaf.MaxAggregateCoveredCandidates) &&
				callerleaf.MaxExpectedPairs <= callerleaf.MaxAggregateCoverageRecords,
			AggregateCanonicalBoundsFit: measurement.WorstCaseGeneration.CanonicalBytes <=
				callerleaf.MaxAggregateCanonicalBytes,
			AggregateStagingBoundsFit: measurement.WorstCaseGeneration.StagingBytes <=
				callerleaf.MaxAggregateStagingBytes,
		}
		measurement.Profiles = append(measurement.Profiles, proof)
	}
	measurement.ClearsScopedCallerGate = len(measurement.PairSamples) == len(protocols) &&
		len(measurement.Profiles) == 2
	for _, sample := range measurement.PairSamples {
		measurement.ClearsScopedCallerGate = measurement.ClearsScopedCallerGate &&
			sample.CandidateRecords == candidate.MaxRecordsPerArtifact &&
			sample.CoverageRecords == 1 &&
			sample.CoveredCandidates == candidate.MaxRecordsPerArtifact &&
			sample.CoverageGapKinds == 4 &&
			sample.ResultRecords == 0 && sample.AbstentionRecords == 0 &&
			sample.SourceBlobReads == 0 && sample.SourceBlobBytes == 0 &&
			sample.OutOfLeafReads == 0
	}
	for _, profile := range measurement.Profiles {
		measurement.ClearsScopedCallerGate = measurement.ClearsScopedCallerGate &&
			profile.AggregateCoverageBoundsFit &&
			profile.AggregateCanonicalBoundsFit && profile.AggregateStagingBoundsFit
	}
	if !measurement.ClearsScopedCallerGate {
		t.Fatalf("compact caller gate did not clear: %+v", measurement)
	}
	return measurement
}
func executeT40R1CompactMaxPair(
	t *testing.T,
	domain,
	protocol string,
) (callerleaf.Receipt, int) {
	t.Helper()
	version := "1.5.0"
	digestA := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	generation, err := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository:           "github.com/phebs/t40r1-compact-" + protocol,
		HeadCommit:           "0123456789012345678901234567890123456789",
		DeclarationSetDigest: digestA, CandidateManifestDigest: digestB,
		CandidatePolicyDigest: digestA, ResolverGenerationDigest: digestB,
		ResolverManifestDigest: digestA,
		Extractors: []callerleaf.ExtractorIdentity{{
			Domain: domain, Version: version,
			LeafAdapterVersion: callerleaf.CurrentLeafAdapter,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf := extract.CandidateCallerLeaf{
		Name:    strings.Repeat("c", 248) + ".ndjson",
		Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount:   candidate.MaxRecordsPerArtifact,
		DeclaredBytes: int64(candidate.MaxRecordsPerArtifact),
		ContentBytes:  int64(candidate.MaxRecordsPerArtifact),
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	const gapPopulation = 1000
	files := make([]extract.CandidateManifestFile, 3*gapPopulation+96)
	generated := make([]gocaller.GeneratedSourceIdentity, 0, gapPopulation)
	for index := range files {
		files[index] = extract.CandidateManifestFile{
			Path:     fmt.Sprintf("scope/input-%04d.go", index),
			ObjectID: fmt.Sprintf("%040x", index+1), DeclaredBytes: 1,
			SourceLane: candidate.SourceLaneBase,
		}
		switch {
		case index < gapPopulation:
			files[index].SourceLane = candidate.SourceLaneGoTest
		case index < 2*gapPopulation:
			files[index].Path = fmt.Sprintf("scope/input-%04d.mod", index)
		case index < 3*gapPopulation:
			generated = append(generated, gocaller.GeneratedSourceIdentity{
				Path: files[index].Path, ObjectID: files[index].ObjectID,
			})
		}
	}
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: domain, Version: version}},
		leaf:    leaf, files: files,
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: domain, Version: version, Protocol: protocol}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(
		filepath.Join(t.TempDir(), "caller-leaves"), generation, pairs[0].Identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Discard() })
	resolver, err := gocaller.NewDirectResolverWithGeneratedSources(
		t.Context(), protocol, nil, generated,
	)
	if err != nil || resolver.DescriptorCount() != 0 {
		t.Fatalf("empty %s resolver = %v, descriptors %d", protocol, err, resolver.DescriptorCount())
	}
	if err := ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0], Protocol: protocol,
		ResolverCatalogDigest: generation.ResolverManifestDigest, Resolver: resolver,
		ReadBlob: func(context.Context, string, string, int64) ([]byte, error) {
			t.Fatalf("compact %s maximal pair read a source blob", protocol)
			return nil, nil
		}, Stage: stage,
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	gapKinds := -1
	if err := publication.ScanRecords(
		t.Context(), generation, pairs[0].Identity,
		func(_ callerleaf.RecordReference, record callerleaf.Record) error {
			if record.Coverage == nil || gapKinds != -1 {
				t.Fatal("maximal compact pair did not emit exactly one coverage record")
			}
			gapKinds = len(record.Coverage.Gaps)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	return prepared.Receipt(), gapKinds
}
