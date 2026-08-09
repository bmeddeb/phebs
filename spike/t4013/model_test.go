package t4013

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	minified, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReceipt(minified, plan); err == nil {
		t.Fatal("non-identical receipt bytes used the historical executable-digest exception")
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

func TestV2PlanAndReceiptBindExactHostToolchain(t *testing.T) {
	hostToolchain := fakeHostToolchain()
	plan, err := frozenV2PlanWithHostToolchain(testSourceCommit, hostToolchain)
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(planBytes)
	if err != nil || decoded.Schema != PlanSchemaV2 || !slices.Equal(decoded.HostToolchain, hostToolchain) {
		t.Fatalf("decoded v2 plan = %+v, %v", decoded, err)
	}
	observation := completedObservation()
	observation.Schema = ObservationSchemaV2
	observation.HostToolchain = slices.Clone(hostToolchain)
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, observation), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV2 || !slices.Equal(receipt.HostToolchain, hostToolchain) {
		t.Fatalf("decoded v2 receipt = %+v, %v", receipt, err)
	}
	receipt.HostToolchain[0].SHA256 = "sha256:" + strings.Repeat("f", 64)
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("receipt with a different host executable digest passed")
	}
	receipt.HostToolchain[0] = hostToolchain[0]
	plan.HostToolchain[1].Version = "go tool compile changed"
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("receipt passed a differently frozen host toolchain")
	}
}

func TestV3PlanFreezesServerHealthDeadline(t *testing.T) {
	plan, err := frozenPlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV3 || plan.Safety.ServerHealthDeadlineMS != 15*60*1000 {
		t.Fatalf("v3 plan = %+v", plan)
	}
	changed := plan
	changed.Safety.ServerHealthDeadlineMS--
	if _, err := MarshalPlan(changed); err == nil {
		t.Fatal("changed server health deadline passed")
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV3 || decoded.Safety != frozenSafetyV3 {
		t.Fatalf("decoded v3 plan = %+v, %v", decoded, err)
	}
	if bytes.Contains(encoded, []byte("full_convergence_deadline_ms")) ||
		bytes.Contains(encoded, []byte("revalidation_deadline_ms")) {
		t.Fatal("historical v3 plan bytes acquired v4 convergence deadlines")
	}
}

func TestV4PlanFreezesConvergenceDeadlines(t *testing.T) {
	plan, err := frozenV4PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV4 || plan.Safety.ServerHealthDeadlineMS != 15*60*1000 ||
		plan.Safety.FullConvergenceDeadlineMS != 2*60*60*1000 ||
		plan.Safety.RevalidationDeadlineMS != 20*60*1000 {
		t.Fatalf("v4 plan = %+v", plan)
	}
	for _, mutate := range []func(*Plan){
		func(value *Plan) { value.Safety.FullConvergenceDeadlineMS-- },
		func(value *Plan) { value.Safety.RevalidationDeadlineMS-- },
	} {
		changed := plan
		mutate(&changed)
		if _, err := MarshalPlan(changed); err == nil {
			t.Fatal("changed v4 convergence deadline passed")
		}
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV4 || decoded.Safety != frozenSafetyV4 {
		t.Fatalf("decoded v4 plan = %+v, %v", decoded, err)
	}
}

func TestV1PlanBytesDoNotAcquireHostToolchainField(t *testing.T) {
	plan, err := FrozenPlan(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("host_toolchain")) {
		t.Fatal("historical v1 plan bytes acquired the prospective host-toolchain field")
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
	var missingToolchain Receipt
	if err := json.Unmarshal(receipt, &missingToolchain); err != nil {
		t.Fatal(err)
	}
	missingToolchain.Toolchain = nil
	if err := ValidateReceipt(missingToolchain, plan); err == nil {
		t.Fatal("prospective stopped receipt omitted successful-preflight executable identities")
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

func TestStoppedPreflightFailureMayLackExecutableDigests(t *testing.T) {
	plan, planBytes := testPlan(t)
	value := completedObservation()
	value.Outcome = "stopped"
	value.Toolchain = nil
	value.Failures = []FailureObservation{{
		Phase: "preflight", Class: "execution", Code: "operational_failure",
	}}
	value.Decision = DecisionObservation{Selected: "unclassified", Reason: "operational_failure"}
	for index := range value.Phases {
		value.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
	}
	value.Phases[0].Outcome = "failed"
	value.Phases[len(value.Phases)-1] = succeededPhase("teardown", PhaseMetrics{WallMS: 1})
	value.Checks = frozenChecks(false)
	value.Teardown = TeardownObservation{Completed: true}
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReceipt(receiptBytes, plan); err != nil {
		t.Fatal(err)
	}
}

func TestV3StoppedReceiptBindsStartupDeadlineObservation(t *testing.T) {
	plan, err := frozenPlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedObservation()
	value.Schema = ObservationSchemaV3
	value.HostToolchain = slices.Clone(plan.HostToolchain)
	value.Outcome = "stopped"
	value.ServerStartups = []ServerStartupObservation{{
		Profile: "structural-2m-v1", Label: "cold", Outcome: "deadline",
		LastStage: "store_opened", LastHealthClass: "transport", HealthAttempts: 100,
		WallMS: plan.Safety.ServerHealthDeadlineMS, PeakRSSBytes: 1,
		LogBytes: 1, LogSHA256: "sha256:" + strings.Repeat("a", 64),
	}}
	value.Failures = []FailureObservation{{Phase: "cold", Class: "execution", Code: "operational_failure"}}
	value.Decision = DecisionObservation{Selected: "unclassified", Reason: "operational_failure"}
	for index := range value.Phases {
		value.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
	}
	value.Phases[0] = succeededPhase("preflight", PhaseMetrics{WallMS: 1})
	value.Phases[1] = PhaseObservation{Name: "cold", Outcome: "failed", Metrics: PhaseMetrics{
		WallMS: plan.Safety.ServerHealthDeadlineMS, PeakRSSBytes: 1,
	}}
	value.Phases[len(value.Phases)-1] = succeededPhase("teardown", PhaseMetrics{WallMS: 1})
	value.Checks = frozenChecks(false)
	value.Teardown = TeardownObservation{Completed: true}
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV3 || len(receipt.ServerStartups) != 1 {
		t.Fatalf("v3 stopped receipt = %+v, %v", receipt, err)
	}
	receipt.ServerStartups[0].WallMS--
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("startup observation preceding the frozen deadline passed")
	}
}

func TestV4StoppedReceiptBindsConvergenceDeadlineObservation(t *testing.T) {
	plan, err := frozenV4PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedObservation()
	value.Schema = ObservationSchemaV4
	value.HostToolchain = slices.Clone(plan.HostToolchain)
	value.Outcome = "stopped"
	value.ConvergenceWaits = []ConvergenceWaitObservation{{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a", Outcome: "deadline",
		LastStage: "repository_index", Attempts: 1,
		FirstProgressSHA256: "sha256:" + strings.Repeat("a", 64),
		LastProgressSHA256:  "sha256:" + strings.Repeat("a", 64),
		DeadlineMS:          plan.Safety.FullConvergenceDeadlineMS,
		WallMS:              plan.Safety.FullConvergenceDeadlineMS,
	}}
	value.Failures = []FailureObservation{{
		Phase: "cold", Class: "execution", Code: "convergence_deadline_expired",
	}}
	value.Decision = DecisionObservation{Selected: "unclassified", Reason: "convergence_deadline_expired"}
	for index := range value.Phases {
		value.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
	}
	value.Phases[0] = succeededPhase("preflight", PhaseMetrics{WallMS: 1})
	value.Phases[1] = PhaseObservation{Name: "cold", Outcome: "failed", Metrics: PhaseMetrics{
		WallMS: plan.Safety.FullConvergenceDeadlineMS, PeakRSSBytes: 1,
	}}
	value.Phases[len(value.Phases)-1] = succeededPhase("teardown", PhaseMetrics{WallMS: 1})
	value.Checks = frozenChecks(false)
	value.Teardown = TeardownObservation{Completed: true}
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV4 || len(receipt.ConvergenceWaits) != 1 {
		t.Fatalf("v4 stopped receipt = %+v, %v", receipt, err)
	}
	receipt.ConvergenceWaits[0].WallMS--
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("convergence observation preceding the frozen deadline passed")
	}
	receipt.ConvergenceWaits[0].WallMS++
	receipt.ConvergenceWaits = nil
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("convergence deadline failure without its diagnostic passed")
	}
}

func TestV4CompletedReceiptRequiresExactConvergenceInventory(t *testing.T) {
	plan, err := frozenV4PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedV4Observation(plan)
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV4 || len(receipt.ConvergenceWaits) != 12 {
		t.Fatalf("v4 completed receipt = %+v, %v", receipt, err)
	}
	value.ConvergenceWaits[3].Label = "return-a"
	if _, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes)); err == nil {
		t.Fatal("completed v4 receipt with a changed convergence identity passed")
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

func completedV4Observation(plan Plan) Observation {
	value := completedObservation()
	value.Schema = ObservationSchemaV4
	value.HostToolchain = slices.Clone(plan.HostToolchain)
	startupIdentities := [][2]string{
		{"structural-2m-v1", "cold"}, {"semantic-262144-v1", "cold"},
		{"structural-2m-v1", "warm-noop"}, {"semantic-262144-v1", "interruption-first"},
		{"semantic-262144-v1", "interruption-restart"}, {"semantic-262144-v1", "stale-worker"},
		{"structural-2m-v1", "archive-restore"}, {"semantic-262144-v1", "authorized-query"},
	}
	for _, identity := range startupIdentities {
		value.ServerStartups = append(value.ServerStartups, ServerStartupObservation{
			Profile: identity[0], Label: identity[1], Outcome: "healthy", LastStage: "http_ready",
			LastHealthClass: "ok", HealthAttempts: 1, WallMS: 1, LogBytes: 1,
			LogSHA256: "sha256:" + strings.Repeat("a", 64),
		})
	}
	waitIdentities := [][3]string{
		{"structural-2m-v1", "cold", "a"}, {"semantic-262144-v1", "cold", "a"},
		{"structural-2m-v1", "warm-noop", "a"}, {"structural-2m-v1", "delta-b", "b"},
		{"structural-2m-v1", "return-a", "a-return"}, {"semantic-262144-v1", "interruption-restart", "a"},
		{"semantic-262144-v1", "stale-worker", "a"}, {"structural-2m-v1", "pressure", "a-return"},
		{"structural-2m-v1", "archive-restore", "a-return"}, {"structural-2m-v1", "collection", "a-return"},
		{"semantic-262144-v1", "authorized-query-semantic", "a"},
		{"structural-2m-v1", "authorized-query-structural", "a-return"},
	}
	for _, identity := range waitIdentities {
		deadline := plan.Safety.FullConvergenceDeadlineMS
		switch identity[1] {
		case "warm-noop", "pressure", "collection", "authorized-query-semantic", "authorized-query-structural":
			deadline = plan.Safety.RevalidationDeadlineMS
		}
		value.ConvergenceWaits = append(value.ConvergenceWaits, ConvergenceWaitObservation{
			Profile: identity[0], Label: identity[1], Revision: identity[2], Outcome: "converged",
			LastStage: "complete", Attempts: 1,
			FirstProgressSHA256: "sha256:" + strings.Repeat("b", 64),
			LastProgressSHA256:  "sha256:" + strings.Repeat("b", 64),
			DeadlineMS:          deadline, WallMS: 1,
		})
	}
	return value
}

func fakeHostToolchain() []HostToolObservation {
	return []HostToolObservation{
		{Name: "go", Version: "go version go1.26.1 darwin/arm64", SHA256: "sha256:" + strings.Repeat("a", 64)},
		{Name: "go-compile", Version: "compile version go1.26.1", SHA256: "sha256:" + strings.Repeat("b", 64)},
		{Name: "go-link", Version: "link version go1.26.1", SHA256: "sha256:" + strings.Repeat("c", 64)},
		{Name: "git", Version: "git version 2.53.0", SHA256: "sha256:" + strings.Repeat("d", 64)},
		{Name: "surreal", Version: "3.2.3+20260721.40522d1", SHA256: "sha256:" + strings.Repeat("e", 64)},
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
