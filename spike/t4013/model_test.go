package t4013

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSourceCommit = "3c4e22e1a907a663367fb29e1a2af998eb2d7729"

func TestRetainedMeasuredStopMatchesFrozenPlan(t *testing.T) {
	planBytes, err := os.ReadFile("plan.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DecodePlan(planBytes)
	if err != nil {
		t.Fatal(err)
	}
	const (
		executionCommit = "b1b4e808e1987b3bf28e4afac21cc83b72aa27f2"
		planDigest      = "sha256:13863ed6e0e19e3edf5cbaa2e6d2f79eef645341661a5d61c0066f7f009974a0"
		receiptDigest   = "sha256:873c373353c540d05e61b243b63befd781e7280b4ec52c0ddd4ef074661e4c85"
	)
	if plan.SourceCommit != executionCommit || PlanDigest(planBytes) != planDigest {
		t.Fatalf("retained plan commit/digest = %q / %q", plan.SourceCommit, PlanDigest(planBytes))
	}
	receiptBytes, err := os.ReadFile("results.json")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil {
		t.Fatal(err)
	}
	if PlanDigest(receiptBytes) != receiptDigest || receipt.SourceCommit != executionCommit ||
		receipt.Outcome != "stopped" || receipt.Decision.Selected != "unclassified" ||
		receipt.Decision.Substantiated ||
		!receipt.Teardown.Completed || receipt.Teardown.DerivedDataRetained ||
		receipt.Teardown.ScratchSourceRetained {
		t.Fatalf("retained stopped receipt = %+v, digest %q", receipt, PlanDigest(receiptBytes))
	}
}

func TestFrozenPlanIsDeterministicAndStrict(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyInputs(root); err != nil {
		t.Fatal(err)
	}
	first, err := FrozenPlan(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := MarshalPlan(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FrozenPlan(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := MarshalPlan(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) || len(firstBytes) > MaxPlanBytes || !digestIdentity(PlanDigest(firstBytes)) {
		t.Fatal("frozen T40.13 plan is not deterministic and bounded")
	}
	decoded, err := DecodePlan(firstBytes)
	if err != nil || decoded.SourceCommit != testSourceCommit || !slicesEqual(decoded.Inputs, expectedInputs) {
		t.Fatalf("decoded plan = %+v, %v", decoded, err)
	}
	var object map[string]any
	if err := json.Unmarshal(firstBytes, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	unknown, _ := json.Marshal(object)
	if _, err := DecodePlan(unknown); err == nil {
		t.Fatal("plan with unknown field passed")
	}
	first.StopRules[0].Trigger = "changed after freeze"
	if _, err := MarshalPlan(first); err == nil {
		t.Fatal("changed stop rule passed")
	}
	first, err = FrozenPlan(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	first.Safety.MinimumAvailableDiskBytes++
	if _, err := MarshalPlan(first); err == nil {
		t.Fatal("changed safety envelope passed")
	}
}

func TestCompletedReceiptRequiresEveryExactGate(t *testing.T) {
	plan, planBytes := testPlan(t)
	observation := completedObservation()
	observationBytes := marshal(t, observation)
	receiptBytes, err := BuildReceipt(planBytes, observationBytes, PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	if len(receiptBytes) > MaxReceiptBytes {
		t.Fatalf("receipt bytes = %d", len(receiptBytes))
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Outcome != "completed" || receipt.Decision.Selected != "continue" ||
		!receipt.Claims.MechanicsEvidenceOnly {
		t.Fatalf("receipt = %+v, %v", receipt, err)
	}
	for _, private := range []string{"/Users/", "/private/", "example.invalid/t401-neutral-scale", "structural/cells/"} {
		if bytes.Contains(receiptBytes, []byte(private)) {
			t.Fatalf("receipt leaked %q", private)
		}
	}

	tests := []struct {
		name   string
		mutate func(*Observation)
	}{
		{"batch reader", func(value *Observation) { value.BlobReaders[0].BatchReads = 1 }},
		{"partial partitions", func(value *Observation) { value.Profiles[0].SettledPartitions-- }},
		{"silent empty", func(value *Observation) { value.Explicit.NoSilentEmpty = false }},
		{"noop child", func(value *Observation) { value.Phases[2].Metrics.IndexChildren = 1 }},
		{"noop write", func(value *Observation) { value.Phases[2].Metrics.PublicationWrites = 1 }},
		{"failed check", func(value *Observation) { value.Checks[0].Passed = false }},
		{"service overflow", func(value *Observation) { value.Service.AcceptedServices = 101 }},
		{"release claim", func(value *Observation) {}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := completedObservation()
			test.mutate(&changed)
			if test.name == "release claim" {
				receiptBytes, err := BuildReceipt(planBytes, marshal(t, changed), PlanDigest(planBytes))
				if err != nil {
					t.Fatal(err)
				}
				var receipt Receipt
				if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
					t.Fatal(err)
				}
				receipt.Claims.AuthorizesRelease = true
				if err := ValidateReceipt(receipt, plan); err == nil {
					t.Fatal("broadened claim passed")
				}
				return
			}
			if _, err := BuildReceipt(planBytes, marshal(t, changed), PlanDigest(planBytes)); err == nil {
				t.Fatal("invalid completed observation passed")
			}
		})
	}
}

func TestStoppedReceiptPreservesFailureAndNotRunAccounting(t *testing.T) {
	_, planBytes := testPlan(t)
	value := completedObservation()
	value.Outcome = "stopped"
	value.Environment.FilesystemAvailableBytes = 30 << 30
	value.Failures = []FailureObservation{{
		Phase: "cold", Class: "oracle", Code: "failed_phase_measurement_unavailable",
	}}
	value.Decision = DecisionObservation{
		Selected: "unclassified", Reason: "failed_phase_measurement_unavailable",
	}
	for index := range value.Phases {
		value.Phases[index].Outcome = "not_run"
		value.Phases[index].OracleExact = false
	}
	value.Phases[0] = succeededPhase("preflight", PhaseMetrics{WallMS: 1})
	value.Phases[1].Outcome = "failed"
	value.Phases[len(value.Phases)-1] = succeededPhase("teardown", PhaseMetrics{WallMS: 1})
	value.Checks = frozenChecks(false)
	value.Teardown = TeardownObservation{Completed: true}
	receipt, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(receipt, []byte(`"outcome": "stopped"`)) ||
		!bytes.Contains(receipt, []byte(`"outcome": "not_run"`)) {
		t.Fatal("stopped receipt hid skipped work")
	}
	plan, err := DecodePlan(planBytes)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{"later phase ran", func(value *Receipt) { value.Phases[2] = succeededPhase("warm_noop", PhaseMetrics{}) }},
		{"failure phase differs", func(value *Receipt) { value.Failures[0].Phase = "warm_noop" }},
		{"decision rule differs", func(value *Receipt) {
			value.Decision = DecisionObservation{
				Selected: "reduce", Reason: "exact_mechanics_oracle_failed", Substantiated: true,
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var changed Receipt
			if err := json.Unmarshal(receipt, &changed); err != nil {
				t.Fatal(err)
			}
			test.mutate(&changed)
			if err := ValidateReceipt(changed, plan); err == nil {
				t.Fatal("incoherent stopped receipt passed")
			}
		})
	}
}

func TestReceiptRefusesWrongPlanDigestAndOversizeInput(t *testing.T) {
	_, planBytes := testPlan(t)
	observation := marshal(t, completedObservation())
	if _, err := BuildReceipt(planBytes, observation, "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong plan digest passed")
	}
	if _, err := DecodeObservation(bytes.Repeat([]byte{'x'}, MaxObservationBytes+1)); err == nil {
		t.Fatal("oversize observation passed")
	}
}

func testPlan(t *testing.T) (Plan, []byte) {
	t.Helper()
	plan, err := FrozenPlan(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	return plan, encoded
}

func completedObservation() Observation {
	phases := make([]PhaseObservation, len(phaseOrder))
	for index, name := range phaseOrder {
		phases[index] = PhaseObservation{
			Name: name, Outcome: "succeeded", OracleExact: true,
			Metrics: PhaseMetrics{WallMS: 1, PeakRSSBytes: 1, DataLogicalBytes: 1, DataAllocatedBytes: 1},
		}
	}
	phases[2].Metrics.ReusedControls = 1
	return Observation{
		Schema: ObservationSchema, MeasuredOn: "2026-08-08", Outcome: "completed",
		Environment: EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30, InitialUsedPercent: 72,
		},
		Toolchain: []ToolchainObservation{
			{Name: "phebs", SHA256: "sha256:" + strings.Repeat("1", 64)},
			{Name: "zoekt-git-index", SHA256: "sha256:" + strings.Repeat("2", 64)},
			{Name: "phebs-focused-index", SHA256: "sha256:" + strings.Repeat("3", 64)},
			{Name: "buf", SHA256: "sha256:" + strings.Repeat("4", 64)},
		},
		Profiles: []ProfileObservation{
			{Name: "structural-2m-v1", RegularFiles: 2_000_002, PhysicalOwners: 2_000_002,
				EligibleGoFiles: 2_000_000, DeclaredSourceBytes: 9_216_000_076,
				SearchPublished: true, ApplicablePartitions: 489, SettledPartitions: 489,
				PublishedDomains: 4, RelationshipPublished: true},
			{Name: "semantic-262144-v1", RegularFiles: 294_914, PhysicalOwners: 294_914,
				EligibleGoFiles: 262_144, IDLCandidates: 32_768, DeclaredSourceBytes: 146_800_716,
				SearchPublished: true, ApplicablePartitions: 72, SettledPartitions: 72,
				PublishedDomains: 4, RelationshipPublished: true},
		},
		BlobReaders: []BlobReaderObservation{
			reader("structural-2m-v1", "a", 2_000_002), reader("structural-2m-v1", "b", 2_000_002),
			reader("structural-2m-v1", "a-return", 2_000_002), reader("semantic-262144-v1", "a", 294_914),
		},
		Service: ServiceControlObservation{AcceptedServices: 100, Memberships: 100, DistinctPaths: 201,
			UnownedPrefixes: 101, WithinV2PathLimit: true, ExactMembershipOracle: true, ExactUnownedOracle: true},
		Explicit: ExplicitStateObservation{AbsentTypedInputs: 4, UnavailableDomains: 4,
			UnsupportedSyntaxFacts: 16_384, GapFacts: 131_072, NoSilentEmpty: true},
		Phases: phases, Checks: frozenChecks(true), Decision: DecisionObservation{
			Selected: "continue", Reason: "all_exact_mechanics_passed", Substantiated: true,
		},
		Teardown: TeardownObservation{Completed: true},
	}
}

func reader(profile, revision string, files uint64) BlobReaderObservation {
	return BlobReaderObservation{Profile: profile, Revision: revision, Mode: "go_git", FilesOffered: files, FallbackReads: files}
}

func frozenChecks(passed bool) []CheckObservation {
	result := make([]CheckObservation, len(checkNames))
	for index, name := range checkNames {
		result[index] = CheckObservation{Name: name, Passed: passed}
	}
	return result
}

func marshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func slicesEqual(left, right []InputBinding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
