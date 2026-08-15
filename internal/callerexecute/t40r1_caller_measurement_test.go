package callerexecute

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/spike/t401"
)

const (
	t40r1CallerMeasurementSchema     = "t4013-take19-caller-failure-point-v2"
	t40r1CallerDiagnosticEnvironment = "T40R1_CALLER_FAILURE_POINT_DIAGNOSTIC"
)

type t40r1CallerFamilyMeasurement struct {
	Family            string `json:"family"`
	Inputs            uint64 `json:"inputs"`
	ResultRecords     int64  `json:"result_records"`
	AbstentionRecords int64  `json:"abstention_records"`
	CanonicalBytes    int64  `json:"canonical_bytes"`
}

type t40r1CallerTotals struct {
	CandidateInputs   int64 `json:"candidate_inputs"`
	Protocols         int64 `json:"protocols"`
	ResultRecords     int64 `json:"result_records"`
	AbstentionRecords int64 `json:"abstention_records"`
	CanonicalBytes    int64 `json:"canonical_bytes"`
	StagingBytes      int64 `json:"staging_bytes"`
}

type t40r1CallerLimits struct {
	CandidateRecordsPerLeaf    int   `json:"candidate_records_per_leaf"`
	ResultRecordsPerPair       int   `json:"result_records_per_pair"`
	AbstentionRecordsPerPair   int   `json:"abstention_records_per_pair"`
	RecordBytes                int   `json:"record_bytes"`
	CanonicalBytesPerPair      int64 `json:"canonical_bytes_per_pair"`
	StagingBytesPerPair        int64 `json:"staging_bytes_per_pair"`
	SourceBlobBytesPerPair     int64 `json:"source_blob_bytes_per_pair"`
	AggregateResultRecords     int   `json:"aggregate_result_records"`
	AggregateAbstentionRecords int   `json:"aggregate_abstention_records"`
	AggregateCanonicalBytes    int64 `json:"aggregate_canonical_bytes"`
	AggregateStagingBytes      int64 `json:"aggregate_staging_bytes"`
}

type t40r1CallerOverage struct {
	AbstentionRecords int64 `json:"abstention_records"`
	CanonicalBytes    int64 `json:"canonical_bytes"`
	StagingBytes      int64 `json:"staging_bytes"`
}

type t40r1CallerPairMaximum struct {
	ResultRecords     int64 `json:"result_records"`
	AbstentionRecords int64 `json:"abstention_records"`
	RecordBytes       int64 `json:"record_bytes"`
	CanonicalBytes    int64 `json:"canonical_bytes"`
	StagingBytes      int64 `json:"staging_bytes"`
	SourceBlobBytes   int64 `json:"source_blob_bytes"`
}

type t40r1CallerProfileMeasurement struct {
	Profile                  string                         `json:"profile"`
	ProfileDigest            string                         `json:"profile_digest"`
	ResolverDescriptors      map[string]int                 `json:"resolver_descriptors"`
	Families                 []t40r1CallerFamilyMeasurement `json:"families"`
	ExactOutput              t40r1CallerTotals              `json:"exact_output"`
	CurrentLimits            t40r1CallerLimits              `json:"current_limits"`
	ConservativePairMaximum  t40r1CallerPairMaximum         `json:"conservative_pair_maximum"`
	PerPairBoundsFit         bool                           `json:"per_pair_bounds_fit"`
	FailureDimensions        []string                       `json:"failure_dimensions"`
	AggregateOverage         t40r1CallerOverage             `json:"aggregate_overage"`
	RequiredAggregateMinimum t40r1CallerTotals              `json:"required_aggregate_minimum"`
}

type t40r1CallerMeasurement struct {
	Schema               string                          `json:"schema"`
	Method               string                          `json:"method"`
	Profiles             []t40r1CallerProfileMeasurement `json:"profiles"`
	EstablishesScalePass bool                            `json:"establishes_scale_pass"`
	AuthorizesFreeze     bool                            `json:"authorizes_freeze"`
}

type t40r1CallerFixture struct {
	family      string
	inputs      uint64
	path        string
	content     []byte
	lastPath    string
	lastContent []byte
	readsSource bool
}

func TestT40R1ExactCallerFailurePointMeasurement(t *testing.T) {
	measurement := measureT40R1CallerFailurePoint(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "spike", "t4013", "take19-caller-failure-point.json"))
	if err != nil {
		t.Fatal(err)
	}
	var retained t40r1CallerMeasurement
	if json.Unmarshal(raw, &retained) != nil || !reflect.DeepEqual(retained, measurement) {
		encoded, _ := json.MarshalIndent(measurement, "", "  ")
		t.Fatalf("retained caller measurement differs; exact measurement:\n%s", encoded)
	}
	if os.Getenv(t40r1CallerDiagnosticEnvironment) == "1" {
		encoded, marshalErr := json.MarshalIndent(measurement, "", "  ")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		t.Log(string(encoded))
	}
}

func measureT40R1CallerFailurePoint(t *testing.T) t40r1CallerMeasurement {
	t.Helper()
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	measurement := t40r1CallerMeasurement{
		Schema: t40r1CallerMeasurementSchema,
		Method: "production ExecutePair replays the first and last input of each frozen source family plus each control for both caller protocols; equal endpoint receipts and frozen cardinalities provide the exact projection",
	}
	for _, profile := range profiles {
		measurement.Profiles = append(
			measurement.Profiles, measureT40R1CallerProfile(t, profile),
		)
	}
	if len(measurement.Profiles) != 2 {
		t.Fatal("caller failure-point measurement requires both frozen profiles")
	}
	return measurement
}

func measureT40R1CallerProfile(
	t *testing.T,
	profile t401.Profile,
) t40r1CallerProfileMeasurement {
	t.Helper()
	profileDigest, err := t401.ProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	protocols := []struct{ domain, protocol string }{
		{domain: "grpc-caller", protocol: "grpc"},
		{domain: "thrift-caller", protocol: "thrift"},
	}
	fixtures := t40r1CallerFixtures(t, profile)
	result := t40r1CallerProfileMeasurement{
		Profile: profile.Name, ProfileDigest: profileDigest,
		ResolverDescriptors: map[string]int{"grpc": 0, "thrift": 0},
		CurrentLimits: t40r1CallerLimits{
			CandidateRecordsPerLeaf:    candidate.MaxRecordsPerArtifact,
			ResultRecordsPerPair:       callerleaf.MaxResultRecordsPerPair,
			AbstentionRecordsPerPair:   callerleaf.MaxAbstentionRecordsPerPair,
			RecordBytes:                callerleaf.MaxRecordBytes,
			CanonicalBytesPerPair:      callerleaf.MaxCanonicalBytesPerPair,
			StagingBytesPerPair:        callerleaf.MaxStagingBytesPerPair,
			SourceBlobBytesPerPair:     callerleaf.MaxSourceBlobBytesPerPair,
			AggregateResultRecords:     callerleaf.MaxAggregateResultRecords,
			AggregateAbstentionRecords: callerleaf.MaxAggregateAbstentionRecords,
			AggregateCanonicalBytes:    callerleaf.MaxAggregateCanonicalBytes,
			AggregateStagingBytes:      callerleaf.MaxAggregateStagingBytes,
		},
	}
	maxRecordBytes := int64(0)
	maxSourceBytes := int64(0)
	maxResultsPerInput := 0
	maxAbstentionsPerInput := 0
	for fixtureIndex, fixture := range fixtures {
		current := t40r1CallerFamilyMeasurement{Family: fixture.family, Inputs: fixture.inputs}
		for _, adapter := range protocols {
			receipt := executeT40R1CallerSample(
				t, profile, fixture, fixtureIndex, adapter.domain, adapter.protocol,
			)
			if fixture.inputs > 1 {
				last := fixture
				last.path, last.content = fixture.lastPath, fixture.lastContent
				lastReceipt := executeT40R1CallerSample(
					t, profile, last, fixtureIndex+len(fixtures), adapter.domain, adapter.protocol,
				)
				if receipt.ResultCount != lastReceipt.ResultCount ||
					receipt.AbstentionCount != lastReceipt.AbstentionCount ||
					receipt.ContentBytes != lastReceipt.ContentBytes ||
					receipt.StagingBytes != lastReceipt.StagingBytes ||
					receipt.SourceBlobReads != lastReceipt.SourceBlobReads ||
					receipt.SourceBlobBytes != lastReceipt.SourceBlobBytes {
					t.Fatalf("%s/%s frozen endpoint receipts differ: first %+v last %+v",
						fixture.family, adapter.protocol, receipt, lastReceipt)
				}
			}
			wantReads := 0
			wantBytes := int64(0)
			if fixture.readsSource {
				wantReads, wantBytes = 1, int64(len(fixture.content))
			}
			if receipt.ResultCount != 0 || receipt.AbstentionCount != 1 ||
				receipt.RecordCount != 1 || receipt.SourceBlobReads != wantReads ||
				receipt.SourceBlobBytes != wantBytes {
				t.Fatalf("%s/%s sample receipt = %+v", fixture.family, adapter.protocol, receipt)
			}
			maxRecordBytes = max(maxRecordBytes, receipt.ContentBytes)
			maxSourceBytes = max(maxSourceBytes, receipt.SourceBlobBytes)
			maxResultsPerInput = max(maxResultsPerInput, receipt.ResultCount)
			maxAbstentionsPerInput = max(maxAbstentionsPerInput, receipt.AbstentionCount)
			multiplier := int64(fixture.inputs)
			current.ResultRecords += int64(receipt.ResultCount) * multiplier
			current.AbstentionRecords += int64(receipt.AbstentionCount) * multiplier
			current.CanonicalBytes += receipt.ContentBytes * multiplier
			result.ExactOutput.ResultRecords += int64(receipt.ResultCount) * multiplier
			result.ExactOutput.AbstentionRecords += int64(receipt.AbstentionCount) * multiplier
			result.ExactOutput.CanonicalBytes += receipt.ContentBytes * multiplier
			result.ExactOutput.StagingBytes += receipt.ContentBytes * multiplier
		}
		result.Families = append(result.Families, current)
		result.ExactOutput.CandidateInputs += int64(fixture.inputs)
	}
	result.ExactOutput.Protocols = int64(len(protocols))
	result.ConservativePairMaximum = t40r1CallerPairMaximum{
		ResultRecords:     int64(maxResultsPerInput * candidate.MaxRecordsPerArtifact),
		AbstentionRecords: int64(maxAbstentionsPerInput * candidate.MaxRecordsPerArtifact),
		RecordBytes:       maxRecordBytes,
		CanonicalBytes:    maxRecordBytes * int64(candidate.MaxRecordsPerArtifact),
		StagingBytes:      maxRecordBytes * int64(candidate.MaxRecordsPerArtifact),
		SourceBlobBytes:   maxSourceBytes * int64(candidate.MaxRecordsPerArtifact),
	}
	result.PerPairBoundsFit = maxResultsPerInput*candidate.MaxRecordsPerArtifact <= callerleaf.MaxResultRecordsPerPair &&
		maxAbstentionsPerInput*candidate.MaxRecordsPerArtifact <= callerleaf.MaxAbstentionRecordsPerPair &&
		maxRecordBytes <= int64(callerleaf.MaxRecordBytes) &&
		maxRecordBytes*int64(candidate.MaxRecordsPerArtifact) <= callerleaf.MaxCanonicalBytesPerPair &&
		maxRecordBytes*int64(candidate.MaxRecordsPerArtifact) <= callerleaf.MaxStagingBytesPerPair &&
		maxSourceBytes*int64(candidate.MaxRecordsPerArtifact) <= callerleaf.MaxSourceBlobBytesPerPair
	result.AggregateOverage = t40r1CallerOverage{
		AbstentionRecords: max(0, result.ExactOutput.AbstentionRecords-int64(callerleaf.MaxAggregateAbstentionRecords)),
		CanonicalBytes:    max(0, result.ExactOutput.CanonicalBytes-callerleaf.MaxAggregateCanonicalBytes),
		StagingBytes:      max(0, result.ExactOutput.StagingBytes-callerleaf.MaxAggregateStagingBytes),
	}
	if result.AggregateOverage.AbstentionRecords > 0 {
		result.FailureDimensions = append(result.FailureDimensions, "aggregate_abstention_records")
	}
	if result.AggregateOverage.CanonicalBytes > 0 {
		result.FailureDimensions = append(result.FailureDimensions, "aggregate_canonical_bytes")
	}
	if result.AggregateOverage.StagingBytes > 0 {
		result.FailureDimensions = append(result.FailureDimensions, "aggregate_staging_bytes")
	}
	result.RequiredAggregateMinimum = result.ExactOutput
	if !result.PerPairBoundsFit || len(result.FailureDimensions) == 0 {
		t.Fatalf("caller failure-point classification is no longer exact: %+v", result)
	}
	return result
}

func t40r1CallerFixtures(t *testing.T, profile t401.Profile) []t40r1CallerFixture {
	t.Helper()
	fixtures := make([]t40r1CallerFixture, 0, len(profile.Families)+1)
	switch profile.Kind {
	case "structural":
		path, content, err := t401.FrozenStructuralGoFixture(profile, 0)
		if err != nil {
			t.Fatal(err)
		}
		lastPath, lastContent, err := t401.FrozenStructuralGoFixture(
			profile, profile.Aggregate.EligibleGoFiles-1,
		)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, t40r1CallerFixture{
			family: "metadata_reused_go", inputs: profile.Aggregate.EligibleGoFiles,
			path: path, content: content, lastPath: lastPath, lastContent: lastContent,
			readsSource: true,
		})
	case "semantic":
		starts := map[string]uint64{
			"go_rpc_resolved": 0, "go_kafka_literal": 65_536,
			"go_dynamic_call": 131_072, "go_dynamic_topic": 196_608,
		}
		for _, family := range profile.Families {
			if family.Plane != "go" {
				continue
			}
			start, admitted := starts[family.Name]
			if !admitted || family.Inputs != 65_536 {
				t.Fatalf("unexpected frozen Go family %+v", family)
			}
			path, content, fixtureErr := t401.FrozenSemanticGoFixture(profile, family.Name, start)
			if fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
			lastPath, lastContent, fixtureErr := t401.FrozenSemanticGoFixture(
				profile, family.Name, start+family.Inputs-1,
			)
			if fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
			fixtures = append(fixtures, t40r1CallerFixture{
				family: family.Name, inputs: family.Inputs,
				path: path, content: content, lastPath: lastPath, lastContent: lastContent,
				readsSource: true,
			})
		}
	default:
		t.Fatalf("unexpected frozen profile kind %q", profile.Kind)
	}
	controlPath, controlContent, err := t401.FrozenCallerControlFixture(profile)
	if err != nil {
		t.Fatal(err)
	}
	fixtures = append(fixtures, t40r1CallerFixture{
		family: "go_mod_control", inputs: 1,
		path: controlPath, content: controlContent,
	})
	return fixtures
}

func executeT40R1CallerSample(
	t *testing.T,
	profile t401.Profile,
	fixture t40r1CallerFixture,
	fixtureIndex int,
	domain string,
	protocol string,
) callerleaf.Receipt {
	t.Helper()
	version := "1.5.0"
	digestA := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	generation, err := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository:           "github.com/phebs/" + profile.Name,
		HeadCommit:           "0123456789012345678901234567890123456789",
		DeclarationSetDigest: digestA, CandidateManifestDigest: digestB,
		CandidatePolicyDigest: digestA, ResolverGenerationDigest: digestB,
		ResolverManifestDigest: digestA,
		Extractors: []callerleaf.ExtractorIdentity{{
			Domain: domain, Version: version, LeafAdapterVersion: callerleaf.LeafAdapterV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf := extract.CandidateCallerLeaf{
		Name: "caller.ndjson", Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount: 1, DeclaredBytes: int64(len(fixture.content)), ContentBytes: 100,
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	objectID := fmt.Sprintf("%040x", fixtureIndex+1)
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: domain, Version: version}},
		leaf:    leaf,
		files: []extract.CandidateManifestFile{{
			Path: fixture.path, ObjectID: objectID, DeclaredBytes: int64(len(fixture.content)),
			SourceLane: candidate.SourceLaneBase,
		}},
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: domain, Version: version, Protocol: protocol}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(filepath.Join(t.TempDir(), "caller-leaves"), generation, pairs[0].Identity)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := gocaller.NewDirectResolver(t.Context(), protocol, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0], Protocol: protocol,
		ResolverCatalogDigest: generation.ResolverManifestDigest, Resolver: resolver,
		ReadBlob: func(_ context.Context, _ string, wanted string, limit int64) ([]byte, error) {
			if !fixture.readsSource {
				t.Fatalf("%s/%s unexpectedly read a control blob", fixture.family, protocol)
			}
			if wanted != objectID || limit != int64(len(fixture.content)) {
				t.Fatalf("%s/%s read = %s/%d", fixture.family, protocol, wanted, limit)
			}
			return fixture.content, nil
		},
		Stage: stage,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	return prepared.Receipt()
}
