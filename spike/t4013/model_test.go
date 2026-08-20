package t4013

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
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

func TestFreshV14PlanStampsFreezeDateWithoutChangingHistoricalPlan(t *testing.T) {
	historical, err := frozenV14PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if historical.FrozenOn != "2026-08-08" {
		t.Fatalf("historical freeze date = %q", historical.FrozenOn)
	}
	frozenAt := time.Date(2026, 8, 16, 23, 30, 0, 0, time.FixedZone("PDT", -7*60*60))
	fresh, err := freshV14PlanWithHostToolchain(
		testSourceCommit, fakeHostToolchain(), frozenAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.FrozenOn != "2026-08-17" {
		t.Fatalf("fresh UTC freeze date = %q", fresh.FrozenOn)
	}
	if _, err := freshV14PlanWithHostToolchain(
		testSourceCommit, fakeHostToolchain(), time.Time{},
	); err == nil {
		t.Fatal("fresh plan accepted an absent freeze time")
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

func TestV5PlanFreezesCalculatedFourHourDiagnosticDeadline(t *testing.T) {
	plan, err := frozenV5PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV5 || plan.Safety.ServerHealthDeadlineMS != 15*60*1000 ||
		plan.Safety.FullConvergenceDeadlineMS != 4*60*60*1000 ||
		plan.Safety.RevalidationDeadlineMS != 20*60*1000 ||
		plan.Safety.MaximumTotalWallMS != 8*60*60*1000 {
		t.Fatalf("v5 plan = %+v", plan)
	}
	changed := plan
	changed.Safety.FullConvergenceDeadlineMS--
	if _, err := MarshalPlan(changed); err == nil {
		t.Fatal("changed v5 convergence deadline passed")
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV5 || decoded.Safety != frozenSafetyV5 {
		t.Fatalf("decoded v5 plan = %+v, %v", decoded, err)
	}
}

func TestV6PlanPreservesTakeNineDeadlines(t *testing.T) {
	plan, err := frozenV6PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV6 || plan.Safety != frozenSafetyV5 || plan.Safety != frozenSafetyV6 {
		t.Fatalf("v6 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV6 || decoded.Safety != frozenSafetyV6 {
		t.Fatalf("decoded v6 plan = %+v, %v", decoded, err)
	}
}

func TestV7PlanPreservesTakeTenDeadlines(t *testing.T) {
	plan, err := frozenV7PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV7 || plan.Safety != frozenSafetyV6 || plan.Safety != frozenSafetyV7 {
		t.Fatalf("v7 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV7 || decoded.Safety != frozenSafetyV7 {
		t.Fatalf("decoded v7 plan = %+v, %v", decoded, err)
	}
}

func TestV8PlanPreservesTakeElevenDeadlines(t *testing.T) {
	plan, err := frozenV8PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV8 || plan.Safety != frozenSafetyV7 || plan.Safety != frozenSafetyV8 {
		t.Fatalf("v8 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV8 || decoded.Safety != frozenSafetyV8 {
		t.Fatalf("decoded v8 plan = %+v, %v", decoded, err)
	}
}

func TestV9PlanPreservesTakeTwelveDeadlines(t *testing.T) {
	plan, err := frozenV9PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV9 || plan.Safety != frozenSafetyV8 || plan.Safety != frozenSafetyV9 {
		t.Fatalf("v9 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV9 || decoded.Safety != frozenSafetyV9 {
		t.Fatalf("decoded v9 plan = %+v, %v", decoded, err)
	}
}

func TestV10PlanFreezesDeterministicPressureExercise(t *testing.T) {
	plan, err := frozenV10PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV10 || plan.Safety != frozenSafetyV10 ||
		plan.Safety.PressureTargetUsedPercent != 82 ||
		plan.Safety.MaximumPressureBallastBytes != 80<<30 {
		t.Fatalf("v10 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV10 || decoded.Safety != frozenSafetyV10 {
		t.Fatalf("decoded v10 plan = %+v, %v", decoded, err)
	}
}

func TestV11PlanPreservesTakeFifteenEnvelope(t *testing.T) {
	plan, err := frozenV11PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV11 || plan.Safety != frozenSafetyV10 || plan.Safety != frozenSafetyV11 {
		t.Fatalf("v11 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV11 || decoded.Safety != frozenSafetyV11 {
		t.Fatalf("decoded v11 plan = %+v, %v", decoded, err)
	}
}

func TestV12PlanAddsOnlyExtractionAttribution(t *testing.T) {
	plan, err := frozenV12PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV12 || plan.Safety != frozenSafetyV11 ||
		plan.Safety != frozenSafetyV12 || plan.Claims.RaisesProductionBound {
		t.Fatalf("v12 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV12 || decoded.Safety != frozenSafetyV12 {
		t.Fatalf("decoded v12 plan = %+v, %v", decoded, err)
	}
}

func TestV13PlanAddsOnlyBoundedProgressCoalescing(t *testing.T) {
	plan, err := frozenV13PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV13 || plan.Safety != frozenSafetyV12 ||
		plan.Safety != frozenSafetyV13 || plan.Claims.RaisesProductionBound {
		t.Fatalf("v13 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV13 || decoded.Safety != frozenSafetyV13 {
		t.Fatalf("decoded v13 plan = %+v, %v", decoded, err)
	}
}

func TestV14PlanBindsSelectedObservationV2Diagnostics(t *testing.T) {
	plan, err := frozenV14PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV14 || plan.Safety != frozenSafetyV13 ||
		plan.Safety != frozenSafetyV14 || plan.Claims.RaisesProductionBound {
		t.Fatalf("v14 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV14 || decoded.Safety != frozenSafetyV14 {
		t.Fatalf("decoded v14 plan = %+v, %v", decoded, err)
	}
}

func TestV15PlanVersionsExtractionScheduleAuthorityPrecedence(t *testing.T) {
	plan, err := frozenV15PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV15 || plan.Safety != frozenSafetyV14 ||
		plan.Safety != frozenSafetyV15 || plan.Claims.RaisesProductionBound {
		t.Fatalf("v15 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV15 || decoded.Safety != frozenSafetyV15 ||
		observationSchemaForPlan(decoded) != ObservationSchemaV15 ||
		receiptSchemaForPlan(decoded) != ReceiptSchemaV15 {
		t.Fatalf("decoded v15 plan = %+v, %v", decoded, err)
	}

	progress := ExtractionProgressObservation{
		State: string(store.GenerationScheduleSettled), Total: 32, Materialized: 32,
		Failed: 32, Domains: 4, JobState: string(store.StatusFailed), JobAttempts: 2,
	}
	if expectedExtractionScheduleTerminal(progress, 10) {
		t.Fatal("historical v14 contract accepted schedule precedence over a failed job")
	}
	if !expectedExtractionScheduleTerminal(progress, 11) {
		t.Fatal("v15 contract rejected generation-bound schedule precedence")
	}
	if !expectedExtractionJobTerminal(progress, 10) {
		t.Fatal("historical v14 contract rejected job precedence")
	}
	if expectedExtractionJobTerminal(progress, 11) {
		t.Fatal("v15 contract accepted job precedence over a settled schedule")
	}
	unavailable := ExtractionProgressObservation{
		State: "unavailable", JobState: string(store.StatusFailed), JobAttempts: 2,
	}
	if !expectedExtractionJobTerminal(unavailable, 11) {
		t.Fatal("v15 contract rejected a failed job with no schedule")
	}
	for _, inconclusive := range v15InconclusiveTerminalJobProgress() {
		if expectedExtractionJobTerminal(inconclusive, observationDetailV15) {
			t.Fatalf("v15 contract accepted job precedence over schedule state %q", inconclusive.State)
		}
	}
	progress.RefusalStage = string(pipelinerefusal.StageDomainInventory)
	progress.RefusalGenerationKind = string(pipelinerefusal.GenerationExtractionDomain)
	progress.RefusalClassification = string(pipelinerefusal.ClassificationLimit)
	progress.RefusalDimension = string(pipelinerefusal.DimensionCandidateMemberBytes)
	progress.RefusalObserved, progress.RefusalLimit = 2, 1
	if !expectedExtractionBoundTerminal(progress, 10) ||
		!expectedExtractionBoundTerminal(progress, observationDetailV15) {
		t.Fatal("conclusive typed bound refusal lost terminal authority")
	}
	if expectedExtractionScheduleTerminal(progress, 11) {
		t.Fatal("v15 schedule terminal absorbed a typed bound refusal")
	}
	if expectedExtractionJobTerminal(progress, 10) || expectedExtractionJobTerminal(progress, 11) {
		t.Fatal("extraction job terminal absorbed a typed bound refusal")
	}
	for _, inconclusive := range v15InconclusiveExtractionBoundProgress() {
		if !expectedExtractionBoundTerminal(inconclusive, 10) {
			t.Fatalf("historical v14 contract rejected bound refusal beside schedule state %q", inconclusive.State)
		}
		if expectedExtractionBoundTerminal(inconclusive, observationDetailV15) {
			t.Fatalf("v15 contract accepted bound refusal beside schedule state %q", inconclusive.State)
		}
	}
}

func TestV16PlanVersionsRelationshipPairClassification(t *testing.T) {
	plan, err := frozenV16PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV16 || plan.Safety != frozenSafetyV15 ||
		plan.Safety != frozenSafetyV16 || plan.Claims.RaisesProductionBound {
		t.Fatalf("v16 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV16 || decoded.Safety != frozenSafetyV16 ||
		observationSchemaForPlan(decoded) != ObservationSchemaV16 ||
		receiptSchemaForPlan(decoded) != ReceiptSchemaV16 {
		t.Fatalf("decoded v16 plan = %+v, %v", decoded, err)
	}
}

func TestV17PlanAddsOnlyInterruptionLifecycleEvidence(t *testing.T) {
	plan, err := frozenV17PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV17 || plan.Safety != frozenSafetyV16 ||
		plan.Safety != frozenSafetyV17 || plan.Claims.RaisesProductionBound {
		t.Fatalf("v17 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV17 || decoded.Safety != frozenSafetyV17 ||
		observationSchemaForPlan(decoded) != ObservationSchemaV17 ||
		receiptSchemaForPlan(decoded) != ReceiptSchemaV17 {
		t.Fatalf("decoded v17 plan = %+v, %v", decoded, err)
	}
}

func TestV18PlanAddsBoundedInterruptionProgressEvidence(t *testing.T) {
	plan, err := frozenV18PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV18 || plan.Safety != frozenSafetyV17 ||
		plan.Safety != frozenSafetyV18 || plan.Claims.RaisesProductionBound {
		t.Fatalf("v18 plan = %+v", plan)
	}
	encoded, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(encoded)
	if err != nil || decoded.Schema != PlanSchemaV18 || decoded.Safety != frozenSafetyV18 ||
		observationSchemaForPlan(decoded) != ObservationSchemaV18 ||
		receiptSchemaForPlan(decoded) != ReceiptSchemaV18 {
		t.Fatalf("decoded v18 plan = %+v, %v", decoded, err)
	}
}

func TestV17InterruptionEvidencePreservesHistoricalBytesAndAttemptZero(t *testing.T) {
	historical := completedObservation()
	historicalBytes := marshal(t, historical)
	if bytes.Contains(historicalBytes, []byte(`"interruption":`)) {
		t.Fatal("historical observation acquired interruption evidence")
	}
	historical.Interruption = &InterruptionObservation{
		Schema: interruptionSchemaV1, LastSubstage: "not_started",
	}
	if err := validateInterruptionObservation(historical); err == nil {
		t.Fatal("historical observation accepted interruption evidence")
	}

	attempt := 0
	completed := Observation{
		Schema: ObservationSchemaV17,
		Phases: make([]PhaseObservation, len(phaseOrder)),
		Interruption: &InterruptionObservation{
			Schema: interruptionSchemaV1, LastSubstage: "complete",
			TriggerStage:            extractionpublication.ScheduleStage,
			TriggerGenerationSHA256: "sha256:" + strings.Repeat("a", 64),
			TriggerChunkSHA256:      "sha256:" + strings.Repeat("b", 64),
			TriggerAttempt:          &attempt, TriggerWallMS: 2,
			TriggerRecoveredState: "pending",
		},
		ServerStartups: []ServerStartupObservation{{Label: "interruption-restart"}},
	}
	completed.Phases[5] = PhaseObservation{
		Name: "interruption", Outcome: "succeeded", Metrics: PhaseMetrics{WallMS: 3},
	}
	if err := validateInterruptionObservation(completed); err != nil {
		t.Fatalf("valid V17 interruption evidence refused: %v", err)
	}
	unrecovered := completed
	unrecovered.Interruption = cloneInterruptionObservation(completed.Interruption)
	unrecovered.Interruption.TriggerRecoveredState = "running"
	if err := validateInterruptionObservation(unrecovered); err == nil {
		t.Fatal("running recovered state passed validation")
	}
	unproven := completed
	unproven.Interruption = cloneInterruptionObservation(completed.Interruption)
	unproven.ServerStartups = nil
	if err := validateInterruptionObservation(unproven); err == nil {
		t.Fatal("completed interruption validated without its restart startup observation")
	}
	early := completed
	early.Interruption = cloneInterruptionObservation(completed.Interruption)
	early.Interruption.LastSubstage = "first_stop"
	early.Phases[5].Outcome = "failed"
	if err := validateInterruptionObservation(early); err == nil {
		t.Fatal("recovery evidence accepted before its substage")
	}
	encoded := marshal(t, completed.Interruption)
	if !bytes.Contains(encoded, []byte(`"trigger_attempt":0`)) {
		t.Fatalf("attempt zero was omitted: %s", encoded)
	}
	cloned := cloneInterruptionObservation(completed.Interruption)
	*completed.Interruption.TriggerAttempt = 1
	if cloned.TriggerAttempt == nil || *cloned.TriggerAttempt != 0 {
		t.Fatal("receipt clone retained the mutable interruption attempt pointer")
	}

	failed := completed
	failed.Interruption = &InterruptionObservation{
		Schema: interruptionSchemaV1, LastSubstage: "active_lease_wait",
	}
	failed.Phases[5].Outcome = "failed"
	if err := validateInterruptionObservation(failed); err != nil {
		t.Fatalf("failed V17 interruption lost its terminal substage: %v", err)
	}
	failed.Interruption.LastSubstage = "complete"
	if err := validateInterruptionObservation(failed); err == nil {
		t.Fatal("failed interruption accepted a completed substage")
	}
	premature := completed
	premature.Phases[5].Outcome = "failed"
	premature.Interruption = cloneInterruptionObservation(completed.Interruption)
	premature.Interruption.LastSubstage = "active_lease_wait"
	if err := validateInterruptionObservation(premature); err == nil {
		t.Fatal("pre-trigger substage accepted trigger evidence")
	}
}

func TestV17StoppedReceiptSealsInterruptionTriggerAndSubstage(t *testing.T) {
	plan, err := frozenV17PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	attempt := 0
	value := completedObservation()
	value.Schema = ObservationSchemaV17
	value.HostToolchain = slices.Clone(plan.HostToolchain)
	value.Outcome = "stopped"
	value.Interruption = &InterruptionObservation{
		Schema: interruptionSchemaV1, LastSubstage: "first_stop",
		TriggerStage:            extractionpublication.ScheduleStage,
		TriggerGenerationSHA256: "sha256:" + strings.Repeat("a", 64),
		TriggerChunkSHA256:      "sha256:" + strings.Repeat("b", 64),
		TriggerAttempt:          &attempt, TriggerWallMS: 2,
	}
	value.Phases[5] = PhaseObservation{
		Name: "interruption", Outcome: "failed", Metrics: PhaseMetrics{WallMS: 3},
	}
	for index := 6; index < len(value.Phases)-1; index++ {
		value.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
	}
	value.Checks = frozenChecks(false)
	value.Failures = []FailureObservation{{
		Phase: "interruption", Class: "execution", Code: "operational_failure",
	}}
	value.Decision = DecisionObservation{Selected: "unclassified", Reason: "operational_failure"}
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != ReceiptSchemaV17 || receipt.Interruption == nil ||
		receipt.Interruption.LastSubstage != "first_stop" ||
		receipt.Interruption.TriggerAttempt == nil || *receipt.Interruption.TriggerAttempt != 0 {
		t.Fatalf("V17 interruption receipt = %+v", receipt.Interruption)
	}
}

func TestV17StoppedReceiptSealsUnsatisfiableInterruptionTrigger(t *testing.T) {
	plan, err := frozenV17PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedObservation()
	value.Schema = ObservationSchemaV17
	value.HostToolchain = slices.Clone(plan.HostToolchain)
	value.Outcome = "stopped"
	value.Interruption = &InterruptionObservation{
		Schema: interruptionSchemaV1, LastSubstage: "active_lease_wait",
	}
	value.Phases[5] = PhaseObservation{
		Name: "interruption", Outcome: "failed", Metrics: PhaseMetrics{WallMS: 3},
	}
	for index := 6; index < len(value.Phases)-1; index++ {
		value.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
	}
	value.Checks = frozenChecks(false)
	value.Failures = []FailureObservation{{
		Phase: "interruption", Class: "execution", Code: "interruption_trigger_unsatisfiable",
	}}
	value.Decision = DecisionObservation{
		Selected: "unclassified", Reason: "interruption_trigger_unsatisfiable",
	}
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Interruption.LastSubstage = "first_stop"
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("unsatisfiable trigger receipt accepted an incoherent substage")
	}
}

func TestV18StoppedReceiptSealsTypedNoLeaseProgress(t *testing.T) {
	plan, err := frozenV18PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, class, code, progressClass string
	}{
		{name: "deadline", class: "execution", code: "interruption_trigger_deadline", progressClass: "pending"},
		{name: "terminal", class: "pipeline", code: "interruption_progress_terminal", progressClass: "terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := completedObservation()
			value.Schema = ObservationSchemaV18
			value.HostToolchain = slices.Clone(plan.HostToolchain)
			value.Outcome = "stopped"
			value.Interruption = &InterruptionObservation{
				Schema: interruptionSchemaV1, LastSubstage: "active_lease_wait",
				LastProgressStage: "observation_publication", LastProgressClass: test.progressClass,
				LastProgressSHA256: "sha256:" + strings.Repeat("c", 64),
				LastProgressWallMS: 5, ProgressChanges: 1,
			}
			value.Phases[5] = PhaseObservation{
				Name: "interruption", Outcome: "failed", Metrics: PhaseMetrics{WallMS: 6},
			}
			for index := 6; index < len(value.Phases)-1; index++ {
				value.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
			}
			value.Checks = frozenChecks(false)
			value.Failures = []FailureObservation{{
				Phase: "interruption", Class: test.class, Code: test.code,
			}}
			value.Decision = DecisionObservation{Selected: "unclassified", Reason: test.code}
			receiptBytes, buildErr := BuildReceipt(
				planBytes, marshal(t, value), PlanDigest(planBytes),
			)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			receipt, decodeErr := DecodeReceipt(receiptBytes, plan)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if receipt.Schema != ReceiptSchemaV18 || receipt.Interruption == nil ||
				receipt.Interruption.LastProgressClass != test.progressClass {
				t.Fatalf("V18 interruption receipt = %+v", receipt.Interruption)
			}
		})
	}

	legacy := completedObservation()
	legacy.Schema = ObservationSchemaV17
	legacy.Interruption = &InterruptionObservation{
		Schema: interruptionSchemaV1, LastSubstage: "active_lease_wait",
		LastProgressStage: "observation_publication", LastProgressClass: "pending",
		LastProgressSHA256: "sha256:" + strings.Repeat("d", 64), LastProgressWallMS: 1,
	}
	legacy.Phases[5] = PhaseObservation{Name: "interruption", Outcome: "failed"}
	if err := validateInterruptionObservation(legacy); err == nil {
		t.Fatal("V17 observation accepted V18 progress fields")
	}
}

func TestV14FreezeRequiresConvergentSelectedObservationRoute(t *testing.T) {
	if err := validateV14ConvergenceRoutes(); err != nil {
		t.Fatal(err)
	}
}

func TestV14ObservationProgressRequiresSelectedV2AndSealsRefusal(t *testing.T) {
	base := ObservationProgressObservation{
		State: "failed", SelectedVersion: "v2",
		PlanningState: "failed", PlanningFailed: 1,
		RefusalStage:          string(pipelinerefusal.StageObservationPublication),
		RefusalGenerationKind: string(pipelinerefusal.GenerationObservation),
		RefusalClassification: string(pipelinerefusal.ClassificationLimit),
		RefusalDimension:      string(pipelinerefusal.DimensionGenerationRecords),
		RefusalObserved:       262_144, RefusalLimit: 250_000,
	}
	if err := validateObservationProgress(base, 10); err != nil {
		t.Fatalf("v14 refusal progress = %v", err)
	}
	legacy := base
	legacy.SelectedVersion = ""
	if err := validateObservationProgress(legacy, 10); err == nil {
		t.Fatal("v14 progress accepted an absent selected route")
	}
	if err := validateObservationProgress(base, 9); err == nil {
		t.Fatal("historical progress accepted v14 fields")
	}
}

func TestV10CompletedReceiptRequiresPressureRestart(t *testing.T) {
	plan, err := frozenV10PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedV7Observation(plan)
	value.Schema = ObservationSchemaV10
	value.ServerStartups = slices.Insert(value.ServerStartups, 6, ServerStartupObservation{
		Profile: "structural-2m-v1", Label: "pressure-restart", Outcome: "healthy",
		LastStage: "http_ready", LastHealthClass: "ok", HealthAttempts: 1,
		WallMS: 1, LogBytes: 1, LogSHA256: "sha256:" + strings.Repeat("a", 64),
	})
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV10 || len(receipt.ServerStartups) != 9 {
		t.Fatalf("v10 receipt = %+v, %v", receipt, err)
	}
	value.ServerStartups = slices.Delete(value.ServerStartups, 6, 7)
	if _, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes)); err == nil {
		t.Fatal("v10 completed receipt omitted the pressure restart")
	}
}

func TestV11CompletedReceiptRequiresLiveInterruptionBackupRestart(t *testing.T) {
	plan, err := frozenV11PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedV7Observation(plan)
	value.Schema = ObservationSchemaV11
	value.ServerStartups = slices.Insert(value.ServerStartups, 6, ServerStartupObservation{
		Profile: "structural-2m-v1", Label: "pressure-restart", Outcome: "healthy",
		LastStage: "http_ready", LastHealthClass: "ok", HealthAttempts: 1,
		WallMS: 1, LogBytes: 1, LogSHA256: "sha256:" + strings.Repeat("a", 64),
	})
	value.ServerStartups = slices.Insert(value.ServerStartups, 3, ServerStartupObservation{
		Profile: "semantic-262144-v1", Label: "interruption-backup", Outcome: "healthy",
		LastStage: "http_ready", LastHealthClass: "ok", HealthAttempts: 1,
		WallMS: 1, LogBytes: 1, LogSHA256: "sha256:" + strings.Repeat("a", 64),
	})
	backupWait := value.ConvergenceWaits[5]
	backupWait.Label = "interruption-backup"
	value.ConvergenceWaits = slices.Insert(value.ConvergenceWaits, 5, backupWait)
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV11 || len(receipt.ServerStartups) != 10 ||
		len(receipt.ConvergenceWaits) != 13 {
		t.Fatalf("v11 receipt = %+v, %v", receipt, err)
	}
	value.ServerStartups = slices.Delete(value.ServerStartups, 3, 4)
	value.ConvergenceWaits = slices.Delete(value.ConvergenceWaits, 5, 6)
	if _, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes)); err == nil {
		t.Fatal("v11 completed receipt omitted the live interruption backup restart")
	}
}

func TestHistoricalObservationRejectsPressureRestartDiagnostic(t *testing.T) {
	values := []ServerStartupObservation{{
		Profile: "structural-2m-v1", Label: "pressure-restart", Outcome: "healthy",
		LastStage: "http_ready", LastHealthClass: "ok", HealthAttempts: 1,
		WallMS: 1, LogBytes: 1, LogSHA256: "sha256:" + strings.Repeat("a", 64),
	}}
	if err := validateServerStartups(values, false, false); err == nil {
		t.Fatal("v9 observation acquired the v10 pressure restart label")
	}
	if err := validateServerStartups(values, true, false); err != nil {
		t.Fatalf("v10 pressure restart diagnostic failed: %v", err)
	}
}

func TestV10ObservationRejectsV11InterruptionBackupDiagnostic(t *testing.T) {
	values := []ServerStartupObservation{{
		Profile: "semantic-262144-v1", Label: "interruption-backup", Outcome: "healthy",
		LastStage: "http_ready", LastHealthClass: "ok", HealthAttempts: 1,
		WallMS: 1, LogBytes: 1, LogSHA256: "sha256:" + strings.Repeat("a", 64),
	}}
	if err := validateServerStartups(values, true, false); err == nil {
		t.Fatal("v10 observation acquired the v11 interruption backup label")
	}
	if err := validateServerStartups(values, true, true); err != nil {
		t.Fatalf("v11 interruption backup diagnostic failed: %v", err)
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
	if bytes.Contains(receiptBytes, []byte(`"first_stage"`)) ||
		bytes.Contains(receiptBytes, []byte(`"observation_progress"`)) ||
		bytes.Contains(receiptBytes, []byte(`"inspection_transitions"`)) ||
		bytes.Contains(receiptBytes, []byte(`"last_successful_probe"`)) ||
		bytes.Contains(receiptBytes, []byte(`"transition_limit_exceeded"`)) {
		t.Fatal("historical v4 receipt bytes acquired v5 diagnostics")
	}
	value.ConvergenceWaits[3].Label = "return-a"
	if _, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes)); err == nil {
		t.Fatal("completed v4 receipt with a changed convergence identity passed")
	}
}

func TestV5AndV6StoppedReceiptsRetainVersionedBoundedProgress(t *testing.T) {
	plan, err := frozenV5PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedObservation()
	value.Schema = ObservationSchemaV5
	value.HostToolchain = slices.Clone(plan.HostToolchain)
	value.Outcome = "stopped"
	value.ConvergenceWaits = []ConvergenceWaitObservation{{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a", Outcome: "deadline",
		FirstStage: "repository_index", LastStage: "observation_publication",
		Attempts: 2, ProgressChanges: 1, StageChanges: 1,
		FirstProgressSHA256:       "sha256:" + strings.Repeat("a", 64),
		LastProgressSHA256:        "sha256:" + strings.Repeat("b", 64),
		LastProgressChangeWallMS:  plan.Safety.FullConvergenceDeadlineMS - 1,
		ObservationProgressWallMS: plan.Safety.FullConvergenceDeadlineMS,
		ObservationProgress: &ObservationProgressObservation{
			State: "building", PlanningState: "settled", PlanningSucceeded: 1,
			ScheduleState: "active", ScheduleTotalPartitions: 62, ScheduleMaterialized: 62,
			SchedulePending: 20, ScheduleRunning: 2, ScheduleSucceeded: 40,
		},
		DeadlineMS: plan.Safety.FullConvergenceDeadlineMS,
		WallMS:     plan.Safety.FullConvergenceDeadlineMS,
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
	if err != nil || receipt.Schema != ReceiptSchemaV5 ||
		receipt.ConvergenceWaits[0].ObservationProgress.ScheduleSucceeded != 40 {
		t.Fatalf("v5 stopped receipt = %+v, %v", receipt, err)
	}
	receipt.ConvergenceWaits[0].InspectionTransitions = []ConvergenceTransitionObservation{{
		WallMS: 1, Stage: "repository_index", Class: "pending",
	}}
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("v5 receipt acquired v6 convergence transitions")
	}

	v6Plan, err := frozenV6PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	v6PlanBytes, err := MarshalPlan(v6Plan)
	if err != nil {
		t.Fatal(err)
	}
	value.Schema = ObservationSchemaV6
	value.HostToolchain = slices.Clone(v6Plan.HostToolchain)
	value.ConvergenceWaits[0].LastSuccessfulProbeSHA256 = value.ConvergenceWaits[0].LastProgressSHA256
	value.ConvergenceWaits[0].LastSuccessfulProbeWallMS = value.ConvergenceWaits[0].DeadlineMS - 1
	value.ConvergenceWaits[0].InspectionTransitions = []ConvergenceTransitionObservation{
		{WallMS: 1, Stage: "repository_index", Class: "pending",
			ProgressSHA256: value.ConvergenceWaits[0].FirstProgressSHA256},
		{WallMS: value.ConvergenceWaits[0].LastProgressChangeWallMS,
			Stage: "observation_publication", Class: "pending",
			ProgressSHA256: value.ConvergenceWaits[0].LastProgressSHA256},
	}
	v6ReceiptBytes, err := BuildReceipt(v6PlanBytes, marshal(t, value), PlanDigest(v6PlanBytes))
	if err != nil {
		t.Fatal(err)
	}
	v6Receipt, err := DecodeReceipt(v6ReceiptBytes, v6Plan)
	if err != nil || v6Receipt.Schema != ReceiptSchemaV6 ||
		len(v6Receipt.ConvergenceWaits[0].InspectionTransitions) != 2 {
		t.Fatalf("v6 stopped receipt = %+v, %v", v6Receipt, err)
	}
	serverExitReceipt := v6Receipt
	serverExitReceipt.ConvergenceWaits = slices.Clone(v6Receipt.ConvergenceWaits)
	serverExitReceipt.ConvergenceWaits[0].Outcome = "server_exited"
	serverExitReceipt.Failures = []FailureObservation{{
		Phase: "cold", Class: "execution", Code: "server_exited_during_convergence",
	}}
	serverExitReceipt.Decision = DecisionObservation{
		Selected: "unclassified", Reason: "server_exited_during_convergence",
	}
	if err := ValidateReceipt(serverExitReceipt, v6Plan); err != nil {
		t.Fatalf("v6 server-exit terminal receipt failed: %v", err)
	}
	limitReceipt := v6Receipt
	limitReceipt.ConvergenceWaits = slices.Clone(v6Receipt.ConvergenceWaits)
	limitWait := &limitReceipt.ConvergenceWaits[0]
	limitWait.Outcome = "diagnostic_limit"
	limitWait.Attempts = maxConvergenceTransitions + 1
	limitWait.TransitionLimitExceeded = true
	limitWait.InspectionTransitions = make([]ConvergenceTransitionObservation, maxConvergenceTransitions)
	for index := range limitWait.InspectionTransitions {
		limitWait.InspectionTransitions[index] = ConvergenceTransitionObservation{
			WallMS: int64(index + 1), Stage: limitWait.FirstStage,
			Class:          []string{"pending", "transport"}[index%2],
			ProgressSHA256: limitWait.FirstProgressSHA256,
		}
	}
	limitReceipt.Failures = []FailureObservation{{
		Phase: "cold", Class: "oracle", Code: "convergence_transition_limit_exceeded",
	}}
	limitReceipt.Decision = DecisionObservation{
		Selected: "unclassified", Reason: "convergence_transition_limit_exceeded",
	}
	if err := ValidateReceipt(limitReceipt, v6Plan); err != nil {
		t.Fatalf("v6 fail-closed transition-limit receipt failed: %v", err)
	}

	receipt.ConvergenceWaits[0].InspectionTransitions = nil
	receipt.ConvergenceWaits[0].ObservationProgress.ScheduleSucceeded = 63
	if err := ValidateReceipt(receipt, plan); err == nil {
		t.Fatal("incoherent v5 observation progress passed")
	}
}

func TestV6CompletedReceiptRequiresBoundedConvergenceTransitions(t *testing.T) {
	plan, err := frozenV6PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedV6Observation(plan)
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV6 || len(receipt.ConvergenceWaits) != 12 {
		t.Fatalf("v6 completed receipt = %+v, %v", receipt, err)
	}
	value.ConvergenceWaits[0].InspectionTransitions = nil
	if _, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes)); err == nil {
		t.Fatal("v6 completed receipt without convergence transitions passed")
	}
}

func TestV7ReceiptRequiresClosedLastInspectionDiagnostics(t *testing.T) {
	plan, err := frozenV7PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedV7Observation(plan)
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV7 || len(receipt.ConvergenceWaits) != 12 {
		t.Fatalf("v7 completed receipt = %+v, %v", receipt, err)
	}

	digest := "sha256:" + strings.Repeat("a", 64)
	wait := ConvergenceWaitObservation{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a", Outcome: "deadline",
		FirstStage: "observation_publication", LastStage: "observation_publication",
		Attempts: 3, FirstProgressSHA256: digest, LastProgressSHA256: digest,
		InspectionTransitions: []ConvergenceTransitionObservation{
			{WallMS: 1, Stage: "observation_publication", Class: "pending", ProgressSHA256: digest},
			{WallMS: 2, Stage: "observation_publication", Class: "status", HTTPStatus: 409,
				HTTPReason: httpReason409ControlAbsent, ProgressSHA256: digest},
		},
		LastSuccessfulProbeSHA256: digest, LastSuccessfulProbeWallMS: 1,
		LastInspectionStage: "observation_publication", LastInspectionClass: "status",
		LastInspectionHTTPStatus: 409, LastInspectionHTTPReason: httpReason409ControlAbsent,
		LastInspectionSHA256: digest, LastInspectionWallMS: 9,
		DeadlineMS: 10, WallMS: 10,
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 3); err != nil {
		t.Fatalf("v7 status diagnostic failed: %v", err)
	}
	for _, mutate := range []func(*ConvergenceWaitObservation){
		func(value *ConvergenceWaitObservation) { value.LastInspectionHTTPReason = httpReason500Store },
		func(value *ConvergenceWaitObservation) { value.LastInspectionWallMS = 1 },
		func(value *ConvergenceWaitObservation) { value.InspectionTransitions[1].HTTPStatus = 500 },
		func(value *ConvergenceWaitObservation) {
			value.LastInspectionSHA256 = "sha256:" + strings.Repeat("b", 64)
		},
	} {
		crossed := wait
		crossed.InspectionTransitions = slices.Clone(wait.InspectionTransitions)
		mutate(&crossed)
		if err := validateConvergenceWaits([]ConvergenceWaitObservation{crossed}, 3); err == nil {
			t.Fatalf("invalid v7 diagnostic passed: %+v", crossed)
		}
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 2); err == nil {
		t.Fatal("v6 detail contract accepted v7 status diagnostics")
	}
}

func TestV8ReceiptRetainsClosedProjectionSubstage(t *testing.T) {
	plan, err := frozenV8PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedV7Observation(plan)
	value.Schema = ObservationSchemaV8
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV8 {
		t.Fatalf("v8 completed receipt = %+v, %v", receipt, err)
	}

	digest := "sha256:" + strings.Repeat("a", 64)
	wait := ConvergenceWaitObservation{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a", Outcome: "deadline",
		FirstStage: "observation_publication", LastStage: "observation_publication",
		Attempts: 2, FirstProgressSHA256: digest, LastProgressSHA256: digest,
		InspectionTransitions: []ConvergenceTransitionObservation{
			{WallMS: 1, Stage: "observation_publication", Class: "pending", ProgressSHA256: digest},
			{WallMS: 2, Stage: "observation_publication", Class: "status", HTTPStatus: 500,
				HTTPReason: httpReason500Publication, ProgressSHA256: digest},
		},
		LastSuccessfulProbeSHA256: digest, LastSuccessfulProbeWallMS: 1,
		LastInspectionStage: "observation_publication", LastInspectionClass: "status",
		LastInspectionHTTPStatus: 500, LastInspectionHTTPReason: httpReason500Publication,
		LastInspectionSHA256: digest, LastInspectionWallMS: 2,
		DeadlineMS: 10, WallMS: 10,
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 4); err != nil {
		t.Fatalf("v8 projection substage failed: %v", err)
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 3); err == nil {
		t.Fatal("v7 detail contract accepted a v8 projection substage")
	}
	wait.InspectionTransitions[1].HTTPReason = httpReason500Projection
	wait.LastInspectionHTTPReason = httpReason500Projection
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 4); err == nil {
		t.Fatal("v8 detail contract accepted the legacy undifferentiated projection reason")
	}
}

func TestV9ReceiptRetainsRepositoryIndexTerminalStop(t *testing.T) {
	plan, err := frozenV9PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	value := completedV7Observation(plan)
	value.Schema = ObservationSchemaV9
	value.Outcome = "stopped"
	digest := "sha256:" + strings.Repeat("a", 64)
	value.ConvergenceWaits = []ConvergenceWaitObservation{{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a",
		Outcome: "repository_index_terminal", FirstStage: "repository_index", LastStage: "repository_index",
		Attempts: 2, FirstProgressSHA256: digest, LastProgressSHA256: digest,
		InspectionTransitions: []ConvergenceTransitionObservation{
			{WallMS: 1, Stage: "repository_index", Class: "pending", ProgressSHA256: digest},
			{WallMS: 2, Stage: "repository_index", Class: "terminal", ProgressSHA256: digest},
		},
		LastSuccessfulProbeSHA256: digest, LastSuccessfulProbeWallMS: 1,
		LastInspectionStage: "repository_index", LastInspectionClass: "terminal",
		LastInspectionSHA256: digest, LastInspectionWallMS: 2,
		DeadlineMS: plan.Safety.FullConvergenceDeadlineMS, WallMS: 2,
	}}
	for index := range value.Phases {
		value.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
	}
	value.Phases[0] = succeededPhase("preflight", PhaseMetrics{WallMS: 1})
	value.Phases[1] = PhaseObservation{Name: "cold", Outcome: "failed", Metrics: PhaseMetrics{WallMS: 2, PeakRSSBytes: 1}}
	value.Phases[len(value.Phases)-1] = succeededPhase("teardown", PhaseMetrics{WallMS: 1})
	value.Checks = frozenChecks(false)
	value.Failures = []FailureObservation{{Phase: "cold", Class: "execution", Code: "repository_index_terminal"}}
	value.Decision = DecisionObservation{Selected: "unclassified", Reason: "repository_index_terminal"}
	value.Teardown = TeardownObservation{Completed: true}
	receiptBytes, err := BuildReceipt(planBytes, marshal(t, value), PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes, plan)
	if err != nil || receipt.Schema != ReceiptSchemaV9 {
		t.Fatalf("v9 terminal receipt = %+v, %v", receipt, err)
	}
	if err := validateConvergenceWaits(value.ConvergenceWaits, 4); err == nil {
		t.Fatal("v8 detail contract accepted a v9 repository-index terminal stop")
	}
}

func TestV10TerminalFailureClassIsClosedAndVersioned(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	wait := ConvergenceWaitObservation{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a",
		Outcome: "repository_index_terminal", FirstStage: "repository_index", LastStage: "repository_index",
		Attempts: 2, FirstProgressSHA256: digest, LastProgressSHA256: digest,
		InspectionTransitions: []ConvergenceTransitionObservation{
			{WallMS: 1, Stage: "repository_index", Class: "pending", ProgressSHA256: digest},
			{WallMS: 2, Stage: "repository_index", Class: "terminal", ProgressSHA256: digest},
		},
		LastSuccessfulProbeSHA256: digest, LastSuccessfulProbeWallMS: 1,
		LastInspectionStage: "repository_index", LastInspectionClass: "terminal",
		LastInspectionSHA256: digest, LastInspectionWallMS: 2,
		RepositoryIndexFailureClass: "lease_heartbeat", DeadlineMS: 10, WallMS: 2,
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 6); err != nil {
		t.Fatalf("v10 heartbeat class failed: %v", err)
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 5); err == nil {
		t.Fatal("v9 detail contract accepted a v10 terminal class")
	}
	wait.RepositoryIndexFailureClass = "other"
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 6); err != nil {
		t.Fatalf("v10 other class failed: %v", err)
	}
	wait.RepositoryIndexFailureClass = "private failure"
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 6); err == nil {
		t.Fatal("v10 detail contract accepted a raw terminal class")
	}
}

func TestV5ConvergenceDiagnosticsRejectContradictoryEvidence(t *testing.T) {
	base := ConvergenceWaitObservation{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a", Outcome: "deadline",
		FirstStage: "observation_publication", LastStage: "observation_publication",
		Attempts: 2, ProgressChanges: 1,
		FirstProgressSHA256:      "sha256:" + strings.Repeat("a", 64),
		LastProgressSHA256:       "sha256:" + strings.Repeat("b", 64),
		LastProgressChangeWallMS: 5,
		DeadlineMS:               10, WallMS: 10,
	}
	tests := []struct {
		name   string
		mutate func(*ConvergenceWaitObservation)
	}{
		{name: "changed digest without count", mutate: func(value *ConvergenceWaitObservation) {
			value.ProgressChanges = 0
		}},
		{name: "changed count without time", mutate: func(value *ConvergenceWaitObservation) {
			value.LastProgressChangeWallMS = 0
		}},
		{name: "changed stage without count", mutate: func(value *ConvergenceWaitObservation) {
			value.ProgressChanges = 0
			value.LastProgressSHA256 = value.FirstProgressSHA256
			value.LastStage = "extraction_publication"
			value.LastProgressChangeWallMS = 0
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if err := validateConvergenceWaits([]ConvergenceWaitObservation{value}, 1); err == nil {
				t.Fatal("contradictory convergence evidence passed")
			}
		})
	}
}

func TestV5ObservationProgressRejectsImpossibleProductionStates(t *testing.T) {
	tests := []ObservationProgressObservation{
		{State: "current"},
		{State: "building", PlanningState: "settled", PlanningSucceeded: 1},
		{State: "failed", ScheduleState: "active", ScheduleTotalPartitions: 1},
		{State: "unavailable", PublicationState: "current"},
		{State: "building", ScheduleState: "settled", ScheduleTotalPartitions: 2,
			SchedulePending: 1, ScheduleSucceeded: 1},
		{State: "building", PlanningState: "settled", PlanningPending: 1},
	}
	for index, value := range tests {
		if err := validateObservationProgress(value, 1); err == nil {
			t.Fatalf("impossible progress %d passed: %+v", index, value)
		}
	}
}

func TestV6ConvergenceTransitionBoundary(t *testing.T) {
	wait := ConvergenceWaitObservation{
		Profile: "structural-2m-v1", Label: "cold", Revision: "a", Outcome: "deadline",
		FirstStage: "repository_visibility", LastStage: "repository_visibility",
		Attempts:                  40,
		FirstProgressSHA256:       "sha256:" + strings.Repeat("a", 64),
		LastProgressSHA256:        "sha256:" + strings.Repeat("a", 64),
		LastSuccessfulProbeSHA256: "sha256:" + strings.Repeat("a", 64),
		LastSuccessfulProbeWallMS: 40,
		DeadlineMS:                100, WallMS: 100,
	}
	for index := 8; index < 40; index++ {
		class := "pending"
		if index%2 == 1 {
			class = "transport"
		}
		wait.InspectionTransitions = append(wait.InspectionTransitions, ConvergenceTransitionObservation{
			WallMS: int64(index), Stage: "repository_visibility", Class: class,
			ProgressSHA256: "sha256:" + strings.Repeat("a", 64),
		})
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 2); err != nil {
		t.Fatalf("exact transition boundary failed: %v", err)
	}
	wait.InspectionTransitions[0].ProgressSHA256 = ""
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 2); err == nil {
		t.Fatal("transition without a progress digest passed")
	}
	wait.InspectionTransitions[0].ProgressSHA256 = wait.FirstProgressSHA256
	wait.Outcome = "diagnostic_limit"
	wait.TransitionLimitExceeded = true
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 2); err != nil {
		t.Fatalf("fail-closed transition boundary failed: %v", err)
	}
	wait.InspectionTransitions = append(wait.InspectionTransitions, ConvergenceTransitionObservation{
		WallMS: 40, Stage: "repository_visibility", Class: "pending",
		ProgressSHA256: "sha256:" + strings.Repeat("a", 64),
	})
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 2); err == nil {
		t.Fatal("over-bound convergence transition inventory passed")
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

func completedV6Observation(plan Plan) Observation {
	value := completedV4Observation(plan)
	value.Schema = ObservationSchemaV6
	value.HostToolchain = slices.Clone(plan.HostToolchain)
	for index := range value.ConvergenceWaits {
		wait := &value.ConvergenceWaits[index]
		wait.FirstStage = "complete"
		wait.LastSuccessfulProbeSHA256 = wait.LastProgressSHA256
		wait.LastSuccessfulProbeWallMS = wait.WallMS
		wait.InspectionTransitions = []ConvergenceTransitionObservation{{
			WallMS: wait.WallMS, Stage: "complete", Class: "complete",
			ProgressSHA256: wait.LastProgressSHA256,
		}}
	}
	return value
}

func completedV7Observation(plan Plan) Observation {
	value := completedV6Observation(plan)
	value.Schema = ObservationSchemaV7
	for index := range value.ConvergenceWaits {
		wait := &value.ConvergenceWaits[index]
		wait.LastInspectionStage = "complete"
		wait.LastInspectionClass = "complete"
		wait.LastInspectionSHA256 = wait.LastProgressSHA256
		wait.LastInspectionWallMS = wait.WallMS
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
