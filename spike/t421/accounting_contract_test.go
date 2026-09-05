package t421

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
)

func accountingTestPlan(t *testing.T) Plan {
	t.Helper()
	raw, err := os.ReadFile("plan-v2.json")
	if err != nil || SHA256(raw) != retainedPlanV2SHA256 {
		t.Fatalf("retained V2: %v", err)
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if err := applyProcessAccountingCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestAccountingV3RetainsHistoricalCanonicalBytes(t *testing.T) {
	for _, test := range []struct{ path, digest, schema string }{
		{"plan.json", retainedPlanSHA256, PlanSchema},
		{"plan-v2.json", retainedPlanV2SHA256, PlanV2Schema},
	} {
		t.Run(test.schema, func(t *testing.T) {
			raw, err := os.ReadFile(test.path)
			if err != nil || SHA256(raw) != test.digest {
				t.Fatalf("retained identity: %v", err)
			}
			var plan Plan
			if err := json.Unmarshal(raw, &plan); err != nil {
				t.Fatal(err)
			}
			encoded, err := MarshalCanonical(plan)
			if err != nil || !bytes.Equal(raw, encoded) || plan.ProcessAccounting != nil {
				t.Fatalf("historical encoding changed: %v", err)
			}
		})
	}
}

func TestAccountingV3CanonicalContract(t *testing.T) {
	plan := accountingTestPlan(t)
	if err := validatePlan(plan, &plan.Revisions); err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalCanonical(plan)
	if err != nil || len(raw) > MaxPlanV3AuthorBytes || bytes.Count(raw, []byte{'\n'}) != 1 {
		t.Fatalf("V3 canonical size=%d err=%v", len(raw), err)
	}
	for _, key := range []string{`"child_process_roles":`, `"maximum_child_processes_per_phase":`, `"child_budgets":`, `"stop_descendants":`, `"require_zero_children":`} {
		if bytes.Contains(raw, []byte(key)) {
			t.Fatalf("V3 retained historical process field %s", key)
		}
	}
	if plan.ProcessAccounting.NativeHistory != "not_established" ||
		plan.ProcessAccounting.NativeMeasurementKind != "sampled_observation" ||
		plan.ProcessAccounting.SupersedesSHA256 != retainedPlanV2SHA256 ||
		plan.Correction.SupersedesSHA256 != retainedPlanSHA256 ||
		slices.Contains(plan.ReceiptContract.RequiredMetrics, "child_processes") ||
		slices.Contains(plan.ReceiptContract.RequiredMetrics, "peak_rss_bytes") {
		t.Fatal("V3 conflates admission and native-history evidence")
	}
	var decoded Plan
	if err := json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(decoded, plan) {
		t.Fatalf("V3 structural round trip: %v", err)
	}
	byPointer, err := MarshalCanonical(&plan)
	if err != nil || !bytes.Equal(byPointer, raw) {
		t.Fatal("V3 pointer encoding differs")
	}
	t.Logf("unsealed V3 plan bytes=%d remaining=%d", len(raw), MaxPlanBytes-len(raw))
}

func TestAccountingV3RejectsCrossVersionAndPolicyMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Plan)
	}{
		{"legacy_schema", func(p *Plan) { p.Schema = PlanV2Schema }},
		{"unknown_schema", func(p *Plan) { p.Schema = "t421-combined-gate-plan-v99" }},
		{"native_history_claim", func(p *Plan) { p.ProcessAccounting.NativeHistory = "complete" }},
		{"simultaneous_bound_claim", func(p *Plan) { p.ProcessAccounting.NativeMeasurementKind = "sampled_lower_bound" }},
		{"legacy_child_limit", func(p *Plan) { p.WorkEnvelope.MaximumChildProcessesPerPhase = 1 }},
		{"global_teardown_claim", func(p *Plan) { p.Teardown.RequireZeroChildren = true }},
		{"unbound_sites", func(p *Plan) { p.ProcessAccounting.ProductionSiteInventorySHA256 = "" }},
		{"unbound_budget", func(p *Plan) { p.WorkEnvelope.Phases[1].ControlledDispatchRoles[1].Maximum++ }},
		{"legacy_marker_trigger", func(p *Plan) { p.FailurePoints[1].Trigger = "relationship_publication_exact_control_v1" }},
		{"legacy_marker_recovery", func(p *Plan) {
			p.FailurePoints[1].RecoveryAction = "recover_marker_owned_then_publish_exact_relationship_root"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := accountingTestPlan(t)
			test.mutate(&plan)
			if err := validatePlanExecutionContract(plan); err == nil {
				t.Fatal("mutated V3 contract was accepted")
			}
		})
	}
	if err := ValidateFrozenPlan(Plan{Schema: "unknown"}); err == nil {
		t.Fatal("unknown schema reached a historical generator")
	}
}

func TestAccountingV3MarkerRecoveryKeepsEpochAndObservationBounds(t *testing.T) {
	plan := accountingTestPlan(t)
	point := plan.FailurePoints[1]
	if point.Name != "interrupted_publication" || point.Phase != "return_a" ||
		point.Trigger != "relationship_publication_same_attempt_exact_control_v3" ||
		point.RecoveryAction != "unwind_publication_then_exclusive_exact_marker_recovery_then_advance_same_attempt" {
		t.Fatal("V3 marker continuation is not explicit")
	}
	legacy := frozenFailurePoints()[1]
	point.Trigger, point.RecoveryAction = legacy.Trigger, legacy.RecoveryAction
	if !reflect.DeepEqual(point, legacy) || len(correctedExecutionServerEpochs()) != 5 || len(plan.PhaseOrder) != 15 {
		t.Fatal("marker correction changed target, deadlines, epochs or phases")
	}
	if bound, err := correctedReturnTransitionReadBound(); err != nil || bound.Calls.Minimum != 2 || bound.Calls.Maximum != 2 ||
		bound.ControlFileReads.Minimum != 10 || bound.ControlFileReads.Maximum != 10 {
		t.Fatal("marker correction changed the two C5 observations")
	}
}

func TestAccountingV3FullFrozenRoundTrip(t *testing.T) {
	plan, err := BuildPlanV3(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(raw)
	if err != nil || !reflect.DeepEqual(decoded, plan) {
		t.Fatalf("V3 full frozen round trip: %v", err)
	}
}

func TestAccountingV3ProfileBindsAccounting(t *testing.T) {
	plan := accountingTestPlan(t)
	tools, host := executionFreezeTestTools(plan, executionFreezeTestCommits()), executionFreezeTestHost()
	admission := executionProfileTestAdmission(t, plan, tools, host)
	profile, err := expectedExecutionProfile(plan, tools, host, admission)
	if err != nil || profile.ProcessAccountingSHA256 == "" || len(profile.Epochs) != 5 ||
		profile.RuntimeBindingSchema != PhaseRuntimeBindingV3Schema || profile.Roots.HomeRootRole == "" {
		t.Fatalf("V3 profile binding: %v", err)
	}
	for _, mutate := range []func(*ExecutionProfileAdmissionBinding){
		func(a *ExecutionProfileAdmissionBinding) { a.processAccountingSHA256 = "" },
		func(a *ExecutionProfileAdmissionBinding) { a.schema = ExecutionProfileSchema },
		func(a *ExecutionProfileAdmissionBinding) { a.invocationSHA256 = SHA256([]byte("V2 invocation")) },
	} {
		changed := admission
		mutate(&changed)
		if _, err := expectedExecutionProfile(plan, tools, host, changed); err == nil {
			t.Fatal("unbound or cross-version profile admission accepted")
		}
	}
}

func TestAccountingCanonicalArtifactsUseClosedVersionRouting(t *testing.T) {
	for _, value := range []any{
		ExecutionFreeze{Schema: ExecutionFreezeV3Schema}, &ExecutionFreeze{Schema: ExecutionFreezeV3Schema},
		Receipt{Schema: ReceiptV3Schema}, &Receipt{Schema: ReceiptV3Schema},
	} {
		raw, err := MarshalCanonical(value)
		if err != nil || bytes.Count(raw, []byte{'\n'}) != 1 {
			t.Fatalf("V3 artifact not compact canonical: %T, %v", value, err)
		}
	}
	for _, value := range []any{
		ExecutionFreeze{Schema: ExecutionFreezeSchema}, Receipt{Schema: ReceiptSchema},
		Receipt{Schema: "t422-combined-convergence-receipt-v2"},
	} {
		raw, err := MarshalCanonical(value)
		original, originalErr := json.MarshalIndent(value, "", "  ")
		if err != nil || originalErr != nil || !bytes.Equal(raw, append(original, '\n')) {
			t.Fatalf("historical artifact canonicalization changed: %T", value)
		}
	}
}

func accountingRuntimeInventory(t *testing.T) (ExecutionFreeze, []ExactPhaseEvidence, []string, map[string]string, []PhaseMeasurement) {
	t.Helper()
	freeze, states, phases, outcomes, measurements := startupRuntimeTestInventory(t, true)
	freeze.Profile.RuntimeBindingSchema = PhaseRuntimeBindingV3Schema
	profileSHA256 := mustReceiptSHA256(t, freeze.Profile)
	for index := range states {
		binding := states[index].Runtime
		binding.Schema = PhaseRuntimeBindingV3Schema
		binding.ProfileSHA256 = profileSHA256
		native := SHA256([]byte(fmt.Sprintf("private-test-native-lifetime-%d", binding.ServerEpoch)))
		binding.ProcessIdentitySHA256 = recipeDigest("t422-phebs-process-identity-v3", binding.ProcessImageSHA256, native)
		_, launch, ok := expectedPhaseRuntime(freeze.Profile.Epochs, phases[index])
		if !ok {
			t.Fatal("test epoch missing")
		}
		if launch == phases[index] {
			binding.OwnedStart = &OwnedServerStartEvidence{
				Schema: "t422-owned-server-start-v1", ServerEpoch: binding.ServerEpoch,
				StartEventOrdinal: binding.StartEventOrdinal, ProcessImageSHA256: binding.ProcessImageSHA256,
				NativeIdentitySHA256: native, NativeIdentityAvailable: true, OwnedStartSucceeded: true,
			}
		}
		states[index].RuntimeSHA256 = mustReceiptSHA256(t, *binding)
	}
	for index := range measurements {
		measurements[index].ChildProcessRoles = nil
		_, launch, _ := expectedPhaseRuntime(freeze.Profile.Epochs, measurements[index].Phase)
		if launch == measurements[index].Phase {
			measurements[index].DispatchAccounting = &DispatchAccountingMeasurement{Roles: []Count{{Name: "phebs", Count: 1}}}
			measurements[index].Metrics.ControlledDispatchAttempts = 1
		}
	}
	return freeze, states, phases, outcomes, measurements
}

func TestAccountingV3RuntimeRequiresOwnedStartNotAdmission(t *testing.T) {
	freeze, states, phases, outcomes, measurements := accountingRuntimeInventory(t)
	if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*OwnedServerStartEvidence)
	}{
		{"failed_start", func(v *OwnedServerStartEvidence) { v.OwnedStartSucceeded = false }},
		{"missing_native", func(v *OwnedServerStartEvidence) { v.NativeIdentityAvailable = false; v.NativeIdentitySHA256 = "" }},
		{"invented_native", func(v *OwnedServerStartEvidence) { v.NativeIdentitySHA256 = "" }},
		{"other_image", func(v *OwnedServerStartEvidence) { v.ProcessImageSHA256 = SHA256([]byte("other")) }},
		{"other_epoch", func(v *OwnedServerStartEvidence) { v.ServerEpoch++ }},
		{"other_event", func(v *OwnedServerStartEvidence) { v.StartEventOrdinal++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			freeze, states, phases, outcomes, measurements := accountingRuntimeInventory(t)
			binding := states[0].Runtime
			test.mutate(binding.OwnedStart)
			states[0].RuntimeSHA256 = mustReceiptSHA256(t, *binding)
			if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err == nil {
				t.Fatal("invalid owned start passed after recomputing digest")
			}
		})
	}
}

func TestAccountingV3RuntimePreservesStartedWithoutNativeIdentity(t *testing.T) {
	freeze, states, phases, outcomes, measurements := accountingRuntimeInventory(t)
	for index := range states {
		if index == 0 {
			states[index].Outcome = "stopped"
			binding := states[index].Runtime
			binding.OwnedStart.NativeIdentityAvailable = false
			binding.OwnedStart.NativeIdentitySHA256 = ""
			binding.ProcessIdentitySHA256 = ""
			binding.Startup.Outcome = "not_ready"
			binding.Startup.ElapsedMS = nil
			states[index].RuntimeSHA256 = mustReceiptSHA256(t, *binding)
		} else {
			states[index].Outcome, states[index].Runtime, states[index].RuntimeSHA256 = "not_run", nil, ""
		}
		outcomes[states[index].Phase] = states[index].Outcome
	}
	if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err != nil {
		t.Fatalf("owned successful Start was lost because native identity was unavailable: %v", err)
	}
}
