package t421

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestReceiptRoundTripIsCanonicalExactAndSourceFree(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	receipt := completeTestReceipt(t, plan, binding)
	returned := returnedPackageTestBinding(t, receipt, plan, binding)
	raw, err := MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("completed receipt bytes=%d headroom=%d", len(raw), int64(plan.ReceiptContract.MaximumBytes)-int64(len(raw)))
	if err := ValidateReceipt(receipt, plan, binding, returned); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReceipt(raw, plan, binding, returned)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, receipt) {
		t.Fatal("strict receipt decode changed the value")
	}
	if uint64(len(raw)) > plan.ReceiptContract.MaximumBytes {
		t.Fatalf("receipt bytes = %d", len(raw))
	}

	states, err := expectedStateProjectionDigests(plan)
	if err != nil {
		t.Fatal(err)
	}
	if states["physical_delta_b"] == states["logical_delta_b"] ||
		states["logical_delta_b"] == states["return_a"] {
		t.Fatal("logical B does not have an independent exact state identity")
	}
	logical := slices.IndexFunc(receipt.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "logical_delta_b"
	})
	if logical < 0 || len(receipt.TransitionResults[logical].Injections) != 1 ||
		receipt.TransitionResults[logical].Injections[0].RequeueCount != 0 {
		t.Fatal("retained V1 logical transition gained a requeue")
	}
	returnIndex := slices.IndexFunc(receipt.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "return_a"
	})
	if returnIndex < 0 || receipt.TransitionResults[returnIndex].ReadAccounting != nil ||
		len(receipt.TransitionResults[returnIndex].Injections) != 1 ||
		receipt.TransitionResults[returnIndex].Injections[0].Target.PlanSHA256 != "" ||
		receipt.TransitionResults[returnIndex].Injections[0].Target.ScheduleSHA256 !=
			receipt.TransitionResults[returnIndex].Injections[0].Target.GenerationSHA256 {
		t.Fatal("retained V1 relationship transition changed shape")
	}
	staleIndex := slices.IndexFunc(receipt.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "stale_lease"
	})
	if staleIndex < 0 || receipt.TransitionResults[staleIndex].ReadAccounting != nil {
		t.Fatal("retained V1 stale-lease transition gained read accounting")
	}
	restartIndex := slices.IndexFunc(receipt.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "process_restart"
	})
	if restartIndex < 0 || receipt.TransitionResults[restartIndex].ReadAccounting != nil ||
		len(receipt.TransitionResults[restartIndex].Injections) != 1 ||
		receipt.TransitionResults[restartIndex].Injections[0].Checkpoint == nil {
		t.Fatal("retained V1 checkpoint restart transition gained read accounting")
	}
	legacyCheckpoint := receipt.TransitionResults[restartIndex].Injections[0].Checkpoint
	if legacyCheckpoint.ChunkIdentitySHA256 != "" || legacyCheckpoint.ScheduleStatusAtHit != "" ||
		legacyCheckpoint.ChunkStatusAtHit != "" || legacyCheckpoint.LeasedAtHit ||
		legacyCheckpoint.CurrentAbsentAtHit || legacyCheckpoint.ScheduleStatusAfter != "" ||
		legacyCheckpoint.ChunkStatusAfter != "" || legacyCheckpoint.UnleasedAfter {
		t.Fatal("retained V1 checkpoint restart gained V2 reader state")
	}
	pressure80Index := slices.IndexFunc(receipt.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "pressure_80"
	})
	if pressure80Index < 0 || receipt.TransitionResults[pressure80Index].ReadAccounting != nil {
		t.Fatal("retained V1 pressure-80 transition gained read accounting")
	}
}

func TestLogicalTransitionReadSubtotalMatchesNativeReader(t *testing.T) {
	bound, err := correctedLogicalTransitionReadBound()
	if err != nil {
		t.Fatal(err)
	}
	base := TransitionReadSubtotal{
		Schema: "t422-transition-read-accounting-v1", Class: correctedLogicalTransitionReadClass,
		ReportCalls: bound.Calls.Minimum, ControlFileReads: bound.ControlFileReads.Minimum,
		StoreReadAttempts: bound.StoreReadAttempts.Minimum, MemberReads: bound.MemberReads.Minimum,
		StoreWriteAttempts: bound.StoreWriteAttempts.Minimum,
	}
	if err := validateLogicalTransitionReadSubtotal(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"schema":  func(value *TransitionReadSubtotal) { value.Schema = "wrong" },
		"class":   func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"calls":   func(value *TransitionReadSubtotal) { value.ReportCalls++ },
		"control": func(value *TransitionReadSubtotal) { value.ControlFileReads++ },
		"store":   func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
		"member":  func(value *TransitionReadSubtotal) { value.MemberReads++ },
		"write":   func(value *TransitionReadSubtotal) { value.StoreWriteAttempts++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := validateLogicalTransitionReadSubtotal(changed); err == nil {
				t.Fatal("mutated logical transition subtotal was accepted")
			}
		})
	}
}

func TestReturnTransitionReadSubtotalMatchesNativeReader(t *testing.T) {
	bound, err := correctedReturnTransitionReadBound()
	if err != nil {
		t.Fatal(err)
	}
	if bound.Calls.Minimum != 2 || bound.Calls.Maximum != 2 ||
		bound.ControlFileReads.Minimum !=
			bound.Calls.Minimum*relationshippublication.PublicationTransitionControlFileReadsV3 ||
		bound.ControlFileReads.Maximum != bound.ControlFileReads.Minimum {
		t.Fatal("return transition subtotal does not match the native per-report reader cost")
	}
	base := TransitionReadSubtotal{
		Schema: "t422-transition-read-accounting-v1", Class: correctedReturnTransitionReadClass,
		ReportCalls: bound.Calls.Minimum, ControlFileReads: bound.ControlFileReads.Minimum,
		StoreReadAttempts: bound.StoreReadAttempts.Minimum, MemberReads: bound.MemberReads.Minimum,
		StoreWriteAttempts: bound.StoreWriteAttempts.Minimum,
	}
	if err := validateReturnTransitionReadSubtotal(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"schema":  func(value *TransitionReadSubtotal) { value.Schema = "wrong" },
		"class":   func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"calls":   func(value *TransitionReadSubtotal) { value.ReportCalls++ },
		"control": func(value *TransitionReadSubtotal) { value.ControlFileReads++ },
		"store":   func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
		"member":  func(value *TransitionReadSubtotal) { value.MemberReads++ },
		"write":   func(value *TransitionReadSubtotal) { value.StoreWriteAttempts++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := validateReturnTransitionReadSubtotal(changed); err == nil {
				t.Fatal("mutated return transition subtotal was accepted")
			}
		})
	}
}

func TestStaleLeaseTransitionReadSubtotalMatchesNativeReader(t *testing.T) {
	bound, err := correctedStaleLeaseTransitionReadBound()
	if err != nil {
		t.Fatal(err)
	}
	if bound.Calls != exactInspectionCalls(2) ||
		bound.ControlFileReads != exactInspectionCalls(
			2*extractionpublication.StaleLeaseTransitionControlFileReads,
		) || bound.StoreReadAttempts != exactInspectionCalls(
		2*store.GenerationStaleLeaseTransitionStoreReadAttempts,
	) || bound.MemberReads != exactInspectionCalls(0) ||
		bound.StoreWriteAttempts != exactInspectionCalls(0) {
		t.Fatal("stale lease transition subtotal does not match the native per-report reader cost")
	}
	base := TransitionReadSubtotal{
		Schema: "t422-transition-read-accounting-v1", Class: correctedStaleLeaseTransitionReadClass,
		ReportCalls: bound.Calls.Minimum, ControlFileReads: bound.ControlFileReads.Minimum,
		StoreReadAttempts: bound.StoreReadAttempts.Minimum, MemberReads: bound.MemberReads.Minimum,
		StoreWriteAttempts: bound.StoreWriteAttempts.Minimum,
	}
	if err := validateStaleLeaseTransitionReadSubtotal(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"schema":  func(value *TransitionReadSubtotal) { value.Schema = "wrong" },
		"class":   func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"calls":   func(value *TransitionReadSubtotal) { value.ReportCalls++ },
		"control": func(value *TransitionReadSubtotal) { value.ControlFileReads++ },
		"store":   func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
		"member":  func(value *TransitionReadSubtotal) { value.MemberReads++ },
		"write":   func(value *TransitionReadSubtotal) { value.StoreWriteAttempts++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := validateStaleLeaseTransitionReadSubtotal(changed); err == nil {
				t.Fatal("mutated stale lease transition subtotal was accepted")
			}
		})
	}
}

func TestCheckpointRestartReadSubtotalMatchesNativeReader(t *testing.T) {
	bound, err := correctedCheckpointRestartReadBound()
	if err != nil {
		t.Fatal(err)
	}
	if bound.Calls != exactInspectionCalls(2) ||
		bound.ControlFileReads != exactInspectionCalls(
			2*extractionpublication.CheckpointRestartTransitionControlFileReads,
		) || bound.StoreReadAttempts != exactInspectionCalls(
		2*store.GenerationStaleLeaseTransitionStoreReadAttempts,
	) || bound.MemberReads != exactInspectionCalls(0) ||
		bound.StoreWriteAttempts != exactInspectionCalls(0) {
		t.Fatal("checkpoint restart subtotal does not match the native per-report reader cost")
	}
	base := TransitionReadSubtotal{
		Schema: "t422-transition-read-accounting-v1", Class: correctedCheckpointRestartReadClass,
		ReportCalls: bound.Calls.Minimum, ControlFileReads: bound.ControlFileReads.Minimum,
		StoreReadAttempts: bound.StoreReadAttempts.Minimum, MemberReads: bound.MemberReads.Minimum,
		StoreWriteAttempts: bound.StoreWriteAttempts.Minimum,
	}
	if err := validateCheckpointRestartReadSubtotal(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"schema":  func(value *TransitionReadSubtotal) { value.Schema = "wrong" },
		"class":   func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"calls":   func(value *TransitionReadSubtotal) { value.ReportCalls++ },
		"control": func(value *TransitionReadSubtotal) { value.ControlFileReads++ },
		"store":   func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
		"member":  func(value *TransitionReadSubtotal) { value.MemberReads++ },
		"write":   func(value *TransitionReadSubtotal) { value.StoreWriteAttempts++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := validateCheckpointRestartReadSubtotal(changed); err == nil {
				t.Fatal("mutated checkpoint restart subtotal was accepted")
			}
		})
	}
}

func TestPressure80TransitionReadSubtotalMatchesNativeReader(t *testing.T) {
	bound := correctedPressure80TransitionReadBound()
	if bound.Calls != exactInspectionCalls(lifecycle.Pressure80ReportCalls) ||
		bound.ControlFileReads != exactInspectionCalls(0) ||
		bound.StoreReadAttempts != exactInspectionCalls(0) ||
		bound.MemberReads != exactInspectionCalls(0) ||
		bound.StoreWriteAttempts != exactInspectionCalls(0) {
		t.Fatal("pressure-80 transition subtotal does not match the two in-memory reports")
	}
	base := TransitionReadSubtotal{
		Schema: "t422-transition-read-accounting-v1", Class: correctedPressure80TransitionReadClass,
		ReportCalls: bound.Calls.Minimum, ControlFileReads: bound.ControlFileReads.Minimum,
		StoreReadAttempts: bound.StoreReadAttempts.Minimum, MemberReads: bound.MemberReads.Minimum,
		StoreWriteAttempts: bound.StoreWriteAttempts.Minimum,
	}
	if err := validatePressure80TransitionReadSubtotal(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"schema":  func(value *TransitionReadSubtotal) { value.Schema = "wrong" },
		"class":   func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"calls":   func(value *TransitionReadSubtotal) { value.ReportCalls++ },
		"control": func(value *TransitionReadSubtotal) { value.ControlFileReads++ },
		"store":   func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
		"member":  func(value *TransitionReadSubtotal) { value.MemberReads++ },
		"write":   func(value *TransitionReadSubtotal) { value.StoreWriteAttempts++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := validatePressure80TransitionReadSubtotal(changed); err == nil {
				t.Fatal("mutated pressure-80 transition subtotal was accepted")
			}
		})
	}
}

func TestPressure80LifecycleRequiresDrainedDurableJobs(t *testing.T) {
	plan := correctedTestPlan(t)
	owners, capacity := testLifecycleOwners(plan, 1_000)
	value := PressureTransition{
		LifecycleFenceUnixMS: 1_000, CapacityObservedUnixMS: capacity,
		LifecycleOwnerTurns: uint64(len(owners)), Owners: owners,
	}
	var err error
	value.LifecycleCycleSHA256, err = pressureLifecycleCycleSHA256(value)
	if err != nil {
		t.Fatal(err)
	}
	metrics := ReceiptMetrics{LifecycleOwnerTurns: CountMetric(len(owners))}
	if err := validatePressure80Lifecycle(value, metrics, plan); err != nil {
		t.Fatal(err)
	}
	job := slices.IndexFunc(value.Owners, func(owner LifecycleOwnerResult) bool {
		return owner.Name == lifecycle.JobOwner
	})
	if job < 0 {
		t.Fatal("durable-job lifecycle owner is absent")
	}
	value.Owners[job].Backlog = true
	value.LifecycleCycleSHA256, err = pressureLifecycleCycleSHA256(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePressureLifecycle(value, metrics, plan); err != nil {
		t.Fatalf("shared lifecycle validation rejected its allowed lower-bound backlog: %v", err)
	}
	if err := validatePressure80Lifecycle(value, metrics, plan); err == nil {
		t.Fatal("pressure 80 accepted durable-job backlog")
	}

	retained := frozenTestPlan(t)
	owners, capacity = testLifecycleOwners(retained, 1_000)
	job = slices.IndexFunc(owners, func(owner LifecycleOwnerResult) bool {
		return owner.Name == lifecycle.JobOwner
	})
	if job < 0 {
		t.Fatal("retained durable-job lifecycle owner is absent")
	}
	owners[job].Backlog = true
	value = PressureTransition{
		LifecycleFenceUnixMS: 1_000, CapacityObservedUnixMS: capacity,
		LifecycleOwnerTurns: uint64(len(owners)), Owners: owners,
	}
	value.LifecycleCycleSHA256, err = pressureLifecycleCycleSHA256(value)
	if err != nil {
		t.Fatal(err)
	}
	metrics = ReceiptMetrics{LifecycleOwnerTurns: CountMetric(len(owners))}
	if err := validatePressure80Lifecycle(value, metrics, retained); err != nil {
		t.Fatalf("retained V1 pressure lifecycle semantics changed: %v", err)
	}
}

func TestLifecycleOwnerTimestampOrderingIsVersioned(t *testing.T) {
	for _, test := range []struct {
		name string
		plan Plan
	}{
		{name: "v1", plan: frozenTestPlan(t)},
		{name: "v2", plan: correctedTestPlan(t)},
	} {
		t.Run(test.name+"_equal_owner_milliseconds", func(t *testing.T) {
			const fence = uint64(1_000)
			owners, _ := testLifecycleOwners(test.plan, fence)
			for index := range owners {
				owners[index].AttemptedAtUnixMS = fence
			}
			_, err := validateLifecycleOwners(owners, test.plan, fence, fence+1)
			if test.plan.Schema == PlanSchema && err == nil {
				t.Fatal("retained V1 accepted equal owner milliseconds")
			}
			if test.plan.Schema == PlanV2Schema && err != nil {
				t.Fatalf("V2 rejected equal owner milliseconds: %v", err)
			}
		})

		t.Run(test.name+"_equal_capacity_milliseconds", func(t *testing.T) {
			const fence = uint64(1_000)
			owners, _ := testLifecycleOwners(test.plan, fence)
			capacity := owners[len(owners)-1].AttemptedAtUnixMS
			_, err := validateLifecycleOwners(owners, test.plan, fence, capacity)
			if test.plan.Schema == PlanSchema && err == nil {
				t.Fatal("retained V1 accepted an equal capacity millisecond")
			}
			if test.plan.Schema == PlanV2Schema && err != nil {
				t.Fatalf("V2 rejected an equal capacity millisecond: %v", err)
			}
		})
	}

	v2 := correctedTestPlan(t)
	owners, capacity := testLifecycleOwners(v2, 1_000)
	owners[0].AttemptedAtUnixMS = 999
	if _, err := validateLifecycleOwners(owners, v2, 1_000, capacity); err == nil {
		t.Fatal("V2 accepted a backwards owner timestamp")
	}
	owners, _ = testLifecycleOwners(v2, 1_000)
	if _, err := validateLifecycleOwners(
		owners, v2, 1_000, owners[len(owners)-1].AttemptedAtUnixMS-1,
	); err == nil {
		t.Fatal("V2 accepted a backwards capacity timestamp")
	}
}

func TestInterruptedPublicationV2AuthorityAndTargetMapping(t *testing.T) {
	prior := AuthorityPhaseResult{
		Phase: "logical_delta_b", Outcome: "passed",
		AuthorityState: AuthorityState{
			LogicalRevision:              "b",
			SourceGenerationSHA256:       testDigest("prior", "source"),
			SearchGenerationSHA256:       testDigest("prior", "search"),
			CatalogRootSHA256:            testDigest("prior", "catalog"),
			CallerGenerationSHA256:       testDigest("prior", "caller-generation"),
			CallerRootSHA256:             testDigest("prior", "caller-root"),
			RelationshipGenerationSHA256: testDigest("prior", "relationship-generation"),
			RelationshipRootSHA256:       testDigest("prior", "relationship-root"),
			RelationshipProvenanceSHA256: testDigest("prior", "relationship-provenance"),
			Current:                      true,
		},
	}
	final := prior
	final.Phase = "return_a"
	final.LogicalRevision = "a-return"
	final.SourceGenerationSHA256 = testDigest("final", "source")
	final.SearchGenerationSHA256 = testDigest("final", "search")
	final.CatalogRootSHA256 = testDigest("final", "catalog")
	final.CallerGenerationSHA256 = testDigest("final", "caller-generation")
	final.CallerRootSHA256 = testDigest("final", "caller-root")
	final.RelationshipGenerationSHA256 = testDigest("final", "relationship-generation")
	final.RelationshipRootSHA256 = testDigest("final", "relationship-root")
	final.RelationshipProvenanceSHA256 = testDigest("final", "relationship-provenance")

	want := final
	want.CallerGenerationSHA256 = prior.CallerGenerationSHA256
	want.CallerRootSHA256 = prior.CallerRootSHA256
	want.RelationshipGenerationSHA256 = prior.RelationshipGenerationSHA256
	want.RelationshipRootSHA256 = prior.RelationshipRootSHA256
	want.RelationshipProvenanceSHA256 = prior.RelationshipProvenanceSHA256
	wantSHA256, ok := authorityIdentitySHA256(want)
	if !ok {
		t.Fatal("mixed authority digest is unavailable")
	}
	priorSHA256, _ := authorityIdentitySHA256(prior)
	finalSHA256, _ := authorityIdentitySHA256(final)
	gotSHA256, ok := interruptedPublicationAuthorityAtHitSHA256(prior, final, Plan{Schema: PlanV2Schema})
	if !ok || gotSHA256 != wantSHA256 || gotSHA256 == priorSHA256 || gotSHA256 == finalSHA256 {
		t.Fatal("V2 interrupted-publication authority is not the exact mixed boundary")
	}
	legacySHA256, ok := interruptedPublicationAuthorityAtHitSHA256(prior, final, Plan{Schema: PlanSchema})
	if !ok || legacySHA256 != priorSHA256 {
		t.Fatal("V1 interrupted-publication authority changed")
	}

	target := InjectionTargetProjection{
		GenerationSHA256: testDigest("return", "output-generation"),
		UnitSHA256:       testDigest("return", "output-root"),
		PlanSHA256:       testDigest("return", "runtime-target"),
		ScheduleSHA256:   testDigest("return", "schedule"),
	}
	if !validInterruptedPublicationTargetMapping(target, Plan{Schema: PlanV2Schema}) {
		t.Fatal("V2 production target mapping was rejected")
	}
	legacy := target
	legacy.PlanSHA256 = ""
	legacy.ScheduleSHA256 = legacy.GenerationSHA256
	if validInterruptedPublicationTargetMapping(legacy, Plan{Schema: PlanV2Schema}) {
		t.Fatal("V2 accepted the legacy generation/schedule alias")
	}
	if !validInterruptedPublicationTargetMapping(legacy, Plan{Schema: PlanSchema}) {
		t.Fatal("V1 legacy target mapping changed")
	}
}

func TestStaleLeaseV2AuthorityAndTargetMapping(t *testing.T) {
	prior := AuthorityPhaseResult{
		Phase: "return_a", Outcome: "passed",
		AuthorityState: AuthorityState{
			LogicalRevision: "a-return", RelationshipRootSHA256: testDigest("stale", "relationship"),
			Current: true,
		},
	}
	final := prior
	final.Phase = "stale_lease"
	wantSHA256, _ := authorityIdentitySHA256(prior)
	gotSHA256, ok := staleLeaseAuthorityAtHitSHA256(prior, final)
	if !ok || gotSHA256 != wantSHA256 {
		t.Fatal("stale lease did not retain exact return-A authority")
	}
	changedAuthority := final
	changedAuthority.RelationshipRootSHA256 = testDigest("stale", "mixed-relationship")
	if _, ok := staleLeaseAuthorityAtHitSHA256(prior, changedAuthority); ok {
		t.Fatal("stale lease accepted mixed authority")
	}

	generation := testDigest("stale", "generation")
	plan := testDigest("stale", "plan")
	schedule := testDigest("stale", "recovery-schedule")
	unit := testDigest("stale", "result")
	root := ExtractionRootResult{
		Domain: "grpc-caller", GenerationSHA256: generation, PlanSHA256: plan,
		PartitionResults: []ExtractionPartitionResult{{
			Ordinal: 0, Kind: "candidate_member", MemberOrdinal: 0,
			SourceStart: 1, SourceEnd: 2, ResultIdentitySHA256: unit,
		}},
	}
	value := InjectionTransition{
		Target: InjectionTargetProjection{
			Domain: "grpc-caller", GenerationSHA256: generation, PlanSHA256: plan,
			ScheduleSHA256: schedule, UnitSHA256: unit, Ordinal: 0,
			Kind: "candidate_member", MemberOrdinal: 0, SourceStart: 1, SourceEnd: 2,
		},
		Preparation: &RecoveryPreparationResult{RecoveryScheduleSHA256: schedule},
	}
	if !injectionTargetMatchesPreparedExtraction(value, root) {
		t.Fatal("stale lease production identity mapping was rejected")
	}
	for name, mutate := range map[string]func(*InjectionTransition, *ExtractionRootResult){
		"generation": func(value *InjectionTransition, _ *ExtractionRootResult) {
			value.Target.GenerationSHA256 = testDigest("stale", "other-generation")
		},
		"plan": func(value *InjectionTransition, _ *ExtractionRootResult) {
			value.Target.PlanSHA256 = testDigest("stale", "other-plan")
		},
		"schedule": func(value *InjectionTransition, _ *ExtractionRootResult) {
			value.Target.ScheduleSHA256 = testDigest("stale", "other-schedule")
		},
		"unit": func(value *InjectionTransition, _ *ExtractionRootResult) {
			value.Target.UnitSHA256 = testDigest("stale", "other-result")
		},
		"legacy_root_schedule": func(_ *InjectionTransition, root *ExtractionRootResult) {
			root.ScheduleSHA256 = schedule
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedValue := value
			changedPreparation := *value.Preparation
			changedValue.Preparation = &changedPreparation
			changedRoot := root
			changedRoot.PartitionResults = slices.Clone(root.PartitionResults)
			mutate(&changedValue, &changedRoot)
			if injectionTargetMatchesPreparedExtraction(changedValue, changedRoot) {
				t.Fatal("stale lease accepted a mixed target identity")
			}
		})
	}
}

func TestBindExecutionFreezeForReceiptOwnsAndValidatesFreeze(t *testing.T) {
	plan := frozenTestPlan(t)
	commits := executionFreezeTestCommits()
	tools := executionFreezeTestTools(plan, commits)
	host := executionFreezeTestHost()
	freeze := mustExecutionFreeze(t, plan, commits)
	checkout := executionFreezeTestCheckout(t, commits, tools)
	profileAdmission := executionProfileTestAdmission(t, plan, tools, host)
	binding, err := BindExecutionFreezeForReceipt(
		freeze, plan, commits, executionFreezeTestSigner(), checkout, profileAdmission,
		executionFreezeTestAdmission(t, plan, freeze),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := cloneExecutionFreeze(t, binding.freeze)
	freeze.Tools[0].Version = "mutated"
	freeze.Profile.Commands[0].NormalizedArgv[0] = "mutated"
	freeze.Pressure.Targets[0].TargetUsedPercent++
	if !reflect.DeepEqual(binding.freeze, want) {
		t.Fatal("receipt binding retained caller-owned freeze storage")
	}

	changed := cloneExecutionFreeze(t, want)
	changed.Pressure.Targets[0].TargetUsedPercent++
	if _, err := BindExecutionFreezeForReceipt(
		changed, plan, commits, executionFreezeTestSigner(), checkout, profileAdmission,
		executionFreezeTestAdmission(t, plan, changed),
	); err == nil {
		t.Fatal("public receipt binder accepted a mutated execution freeze")
	}
}

func TestReceiptAuthorityRejectsMixedCandidateGenerationAndLogicalRootChange(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	receipt := completeTestReceipt(t, plan, binding)
	outcomes := make(map[string]string, len(receipt.PhaseResults))
	for _, phase := range receipt.PhaseResults {
		outcomes[phase.Name] = phase.Outcome
	}
	operational := plan.PhaseOrder[1 : len(plan.PhaseOrder)-1]
	authorities, err := resolveAuthorityResults(
		receipt.Authority.ExtractionRootSnapshots, receipt.Authority.Snapshots,
		receipt.Authority.Results, operational, outcomes,
	)
	if err != nil {
		t.Fatal(err)
	}
	byPhase := make(map[string]AuthorityPhaseResult, len(authorities))
	for _, authority := range authorities {
		byPhase[authority.Phase] = authority
	}

	cold := byPhase["cold"]
	cold.ExtractionRoots = slices.Clone(cold.ExtractionRoots)
	cold.ExtractionRoots[0].CandidateGenerationSHA256 = testDigest("mixed", "candidate-generation")
	cold.ExtractionRootsSHA256 = mustReceiptSHA256(t, cold.ExtractionRoots)
	physical, _ := namedPhysicalRevision(plan.Revisions.Physical, cold.PhysicalRevision)
	if err := validateAuthorityCoverage(cold, physical, plan); err == nil {
		t.Fatal("mixed candidate generations were accepted")
	}

	logical := byPhase["logical_delta_b"]
	logical.ExtractionRoots = slices.Clone(logical.ExtractionRoots)
	logical.ExtractionRoots[0].PlanSHA256 = testDigest("logical", "changed-plan")
	logical.ExtractionRootsSHA256 = mustReceiptSHA256(t, logical.ExtractionRoots)
	byPhase[logical.Phase] = logical
	if err := validateAuthorityContinuity(byPhase, plan); err == nil {
		t.Fatal("logical-only delta changed extraction authority")
	}
}

func TestReceiptRejectsUnauthenticatedAndMutatedExactEvidence(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	base := completeTestReceipt(t, plan, binding)
	if err := ValidateReceipt(base, plan, binding, ReturnedPackageBinding{}); err == nil {
		t.Fatal("receipt without returned-package verification was accepted")
	}

	stateIndex := slices.IndexFunc(base.StateResults, func(value ExactPhaseEvidence) bool {
		return value.Phase == "logical_delta_b"
	})
	pressureIndex := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "pressure_80"
	})
	readerIndex := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "physical_delta_b"
	})
	warmIndex := slices.Index(plan.PhaseOrder, "warm_noop")
	warmBound := plan.WorkEnvelope.Phases[warmIndex].MemberReads.Maximum
	productWorkIndex := slices.IndexFunc(plan.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
		return value.Phase == "product_queries"
	})
	if productWorkIndex < 0 || readerIndex < 0 || base.TransitionResults[readerIndex].Reader == nil {
		t.Fatal("product query work bounds are absent")
	}
	productBounds := plan.WorkEnvelope.Phases[productWorkIndex]
	hiddenIndex := slices.IndexFunc(plan.Oracle.QueryCases, func(value QueryCase) bool { return value.ExpectedStatus == 404 })
	visibleIndex := slices.IndexFunc(plan.Oracle.QueryCases, func(value QueryCase) bool { return value.ExpectedStatus != 404 })
	mutations := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{"plan authority", func(value *Receipt) { value.Authority.PlanSHA256 = zeroDigest() }},
		{"authored commit bytes", func(value *Receipt) { value.RevisionResults[0].AuthoredManifest.CommitBytesSHA256 = zeroDigest() }},
		{"logical B state", func(value *Receipt) {
			value.StateResults[stateIndex].ObservedSHA256 = value.StateResults[stateIndex-1].ObservedSHA256
		}},
		{"runtime binding", func(value *Receipt) {
			value.StateResults[stateIndex].Runtime.ServerEpoch++
			value.StateResults[stateIndex].RuntimeSHA256 = mustReceiptSHA256(t, *value.StateResults[stateIndex].Runtime)
		}},
		{"epoch 3 process identity recipe", func(value *Receipt) {
			for index := range value.StateResults {
				if value.StateResults[index].Runtime != nil && value.StateResults[index].Runtime.ServerEpoch == 3 {
					value.StateResults[index].Runtime.ProcessIdentitySHA256 = testDigest("arbitrary", "epoch-3")
					value.StateResults[index].RuntimeSHA256 = mustReceiptSHA256(t, *value.StateResults[index].Runtime)
				}
			}
		}},
		{"passed state retained projection", func(value *Receipt) {
			projection := testPhaseStateProjection(plan, value.StateResults[stateIndex].Phase)
			value.StateResults[stateIndex].Observed.Projection = &projection
			value.StateResults[stateIndex].ObservedSHA256 = mustReceiptSHA256(t, *value.StateResults[stateIndex].Observed)
		}},
		{"projection digest", func(value *Receipt) {
			value.StateResults[stateIndex].Observed.ProjectionSHA256 = zeroDigest()
			value.StateResults[stateIndex].ObservedProjectionSHA256 = zeroDigest()
			value.StateResults[stateIndex].ObservedSHA256 = mustReceiptSHA256(t, *value.StateResults[stateIndex].Observed)
		}},
		{"duplicate root snapshot", func(value *Receipt) {
			value.Authority.ExtractionRootSnapshots = append(value.Authority.ExtractionRootSnapshots, value.Authority.ExtractionRootSnapshots[0])
			slices.SortFunc(value.Authority.ExtractionRootSnapshots, func(left, right ExtractionRootSnapshot) int {
				return strings.Compare(left.SHA256, right.SHA256)
			})
		}},
		{"unsorted root snapshots", func(value *Receipt) {
			value.Authority.ExtractionRootSnapshots[0], value.Authority.ExtractionRootSnapshots[1] =
				value.Authority.ExtractionRootSnapshots[1], value.Authority.ExtractionRootSnapshots[0]
		}},
		{"missing root snapshot", func(value *Receipt) {
			value.Authority.ExtractionRootSnapshots = value.Authority.ExtractionRootSnapshots[1:]
		}},
		{"unreferenced root snapshot", func(value *Receipt) {
			roots := slices.Clone(value.Authority.ExtractionRootSnapshots[0].Roots)
			roots[0].RootSHA256 = testDigest("unreferenced", "root")
			value.Authority.ExtractionRootSnapshots = append(value.Authority.ExtractionRootSnapshots,
				ExtractionRootSnapshot{SHA256: mustReceiptSHA256(t, roots), Roots: roots})
			slices.SortFunc(value.Authority.ExtractionRootSnapshots, func(left, right ExtractionRootSnapshot) int {
				return strings.Compare(left.SHA256, right.SHA256)
			})
		}},
		{"query authority", func(value *Receipt) { value.QueryResults.Results[0].HTTP.AuthorityBeforeSHA256 = zeroDigest() }},
		{"hidden query control reads", func(value *Receipt) { value.QueryResults.Results[hiddenIndex].HTTP.ControlReads = 1 }},
		{"hidden query member reads", func(value *Receipt) { value.QueryResults.Results[hiddenIndex].MCP.MemberReads = 1 }},
		{"visible query zero control reads", func(value *Receipt) {
			value.QueryResults.Results[visibleIndex].HTTP.ControlReads = 0
		}},
		{"visible query zero member reads", func(value *Receipt) {
			value.QueryResults.Results[visibleIndex].MCP.MemberReads = 0
		}},
		{"visible query control bound", func(value *Receipt) {
			value.QueryResults.Results[visibleIndex].MCP.ControlReads = productBounds.ControlReads.Maximum + 1
		}},
		{"visible query member bound", func(value *Receipt) {
			value.QueryResults.Results[visibleIndex].HTTP.MemberReads = productBounds.MemberReads.Maximum + 1
		}},
		{"aggregate query reads", func(value *Receipt) {
			value.QueryResults.Results[visibleIndex].HTTP.ControlReads = productBounds.ControlReads.Maximum
		}},
		{"relationship authority", func(value *Receipt) { value.RelationshipResults.AuthorityAfterSHA256 = zeroDigest() }},
		{"pressure sequence", func(value *Receipt) { value.TransitionResults[pressureIndex].Pressure.VolumeAvailableBytesBefore-- }},
		{"v2 reader fact in v1", func(value *Receipt) {
			value.TransitionResults[readerIndex].Reader.OldRoleAfterReplacement = "prior"
		}},
		{"v2 transition reads in v1", func(value *Receipt) {
			value.TransitionResults[readerIndex].ReadAccounting = &TransitionReadSubtotal{Schema: "t422-transition-read-accounting-v1"}
		}},
		{"work meter", func(value *Receipt) { value.Measurements[warmIndex].Metrics.MemberReads = CountMetric(warmBound + 1) }},
		{"teardown attempt", func(value *Receipt) { value.Teardown.Attempted = false }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			test.mutate(&changed)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("mutated exact evidence was accepted")
			}
		})
	}
}

func TestReceiptStoppedRuntimeUsesAdmittedIdentityRecipe(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	receipt := completeTestReceipt(t, plan, binding)
	phase := "process_restart"
	stopTestReceipt(t, &receipt, plan, phase, ReceiptFailure{Phase: phase})
	stateIndex := slices.IndexFunc(receipt.StateResults, func(value ExactPhaseEvidence) bool {
		return value.Phase == phase
	})
	if stateIndex < 0 || receipt.StateResults[stateIndex].Runtime == nil {
		t.Fatal("stopped runtime fixture is absent")
	}
	receipt.StateResults[stateIndex].Runtime.ProcessIdentitySHA256 = testDigest("arbitrary", "stopped")
	receipt.StateResults[stateIndex].RuntimeSHA256 = mustReceiptSHA256(
		t, *receipt.StateResults[stateIndex].Runtime,
	)
	outcomes := make(map[string]string, len(receipt.PhaseResults))
	for _, result := range receipt.PhaseResults {
		outcomes[result.Name] = result.Outcome
	}
	if err := validatePhaseRuntimeBindings(
		receipt.StateResults, plan.PhaseOrder[1:len(plan.PhaseOrder)-1],
		outcomes, receipt.Measurements, binding.freeze,
	); err == nil {
		t.Fatal("stopped runtime accepted an arbitrary process identity")
	}
}

func TestReceiptVisibleQueryReadsUseProductPhaseBounds(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	receipt := completeTestReceipt(t, plan, binding)
	queryIndex := slices.IndexFunc(plan.Oracle.QueryCases, func(value QueryCase) bool {
		return value.ExpectedStatus != 404
	})
	workIndex := slices.IndexFunc(plan.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
		return value.Phase == "product_queries"
	})
	measurementIndex := slices.Index(plan.PhaseOrder, "product_queries")
	if queryIndex < 0 || workIndex < 0 || measurementIndex < 0 {
		t.Fatal("visible query fixture is incomplete")
	}
	const memberReads = uint64(4_097)
	if memberReads > plan.WorkEnvelope.Phases[workIndex].MemberReads.Maximum {
		t.Fatal("product phase member-read maximum does not cover the regression value")
	}
	transport := &receipt.QueryResults.Results[queryIndex].HTTP
	delta := memberReads - transport.MemberReads
	transport.MemberReads = memberReads
	receipt.Measurements[measurementIndex].Metrics.MemberReads += CountMetric(delta)
	if err := ValidateReceipt(
		receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
	); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptStoppedDecisionsAndTeardownCorridor(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	base := completeTestReceipt(t, plan, binding)

	t.Run("data logical resource", func(t *testing.T) {
		receipt := cloneTestReceipt(t, base)
		stopTestReceipt(t, &receipt, plan, "pressure_90", ReceiptFailure{
			Phase: "pressure_90", Class: "resource", Code: "data_logical_ceiling",
			Observation: failureObservation(t, plan, "gauge_limit", "data_logical_bytes",
				plan.WorkEnvelope.MaximumDataLogicalBytes,
				plan.WorkEnvelope.MaximumDataLogicalBytes+1),
		})
		index := slices.Index(plan.PhaseOrder, "pressure_90")
		receipt.Measurements[index].Metrics.DataLogicalBytes = Bytes(plan.WorkEnvelope.MaximumDataLogicalBytes + 1)
		receipt.Decision.Selected, receipt.Decision.RulePriority = "cohort_experiment", 2
		if err := ValidateReceipt(
			receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
		); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("materialized topology", func(t *testing.T) {
		receipt := cloneTestReceipt(t, base)
		stopTestReceipt(t, &receipt, plan, "pressure_80", ReceiptFailure{
			Phase: "pressure_80", Class: "topology", Code: "materialized_cartesian_owner_pairs_nonzero",
			Observation: failureObservation(t, plan, "counter_crossing",
				"materialized_cartesian_owner_pairs", 0, 1),
		})
		index := slices.Index(plan.PhaseOrder, "pressure_80")
		receipt.Measurements[index].Metrics.MaterializedOwnerPairs = 1
		receipt.Decision.Selected, receipt.Decision.RulePriority = "p6_investigation", 1
		if err := ValidateReceipt(
			receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
		); err != nil {
			t.Fatal(err)
		}

		stateIndex := slices.IndexFunc(receipt.StateResults, func(value ExactPhaseEvidence) bool {
			return value.Phase == "pressure_80"
		})
		receipt.StateResults[stateIndex].Runtime = nil
		receipt.StateResults[stateIndex].RuntimeSHA256 = ""
		if err := ValidateReceipt(
			receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
		); err == nil {
			t.Fatal("post-launch stopped phase without runtime evidence was accepted")
		}
	})

	t.Run("prelaunch stop", func(t *testing.T) {
		phase := "process_restart"
		observation := FailureObservation{
			Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "measurement_unavailable",
			UnavailableMetrics: []string{"peak_rss_bytes"},
		}
		observation.EvidenceSHA256 = mustReceiptSHA256(t, observation)
		receipt := cloneTestReceipt(t, base)
		stopTestReceipt(t, &receipt, plan, phase, ReceiptFailure{
			Phase: phase, Class: "internal", Code: "measurement_unavailable", Observation: observation,
		})
		phaseIndex := slices.Index(plan.PhaseOrder, phase)
		measurement := &receipt.Measurements[phaseIndex]
		measurement.Metrics.ProcessMeasurementAvailable = false
		measurement.Metrics.PeakRSSBytes = 0
		childIndex := slices.IndexFunc(measurement.ChildProcessRoles, func(value Count) bool {
			return value.Name == "phebs"
		})
		if childIndex < 0 {
			t.Fatal("prelaunch fixture lacks the phebs child role")
		}
		measurement.Metrics.ChildProcesses -= CountMetric(measurement.ChildProcessRoles[childIndex].Count)
		measurement.ChildProcessRoles[childIndex].Count = 0
		stateIndex := slices.IndexFunc(receipt.StateResults, func(value ExactPhaseEvidence) bool {
			return value.Phase == phase
		})
		receipt.StateResults[stateIndex].Runtime = nil
		receipt.StateResults[stateIndex].RuntimeSHA256 = ""
		if err := ValidateReceipt(
			receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
		); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("failed teardown", func(t *testing.T) {
		receipt := cloneTestReceipt(t, base)
		receipt.Teardown.Completed = false
		receipt.Teardown.Outcome = "failed"
		receipt.Teardown.Failure = teardownFailure(t, plan, "teardown_incomplete")
		receipt.Decision = ReceiptDecision{
			Outcome: "stopped", Selected: "reduce", RulePriority: 4,
			Reason: "teardown_failed", Substantiated: true,
			Gate2V2: plan.Claims.Gate2V2, ReleasePosture: plan.Claims.ReleasePosture,
		}
		if err := ValidateReceipt(
			receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
		); err != nil {
			t.Fatal(err)
		}

		mutated := cloneTestReceipt(t, receipt)
		remaining := plan.SafetyEnvelope.MaximumTotalWallMS + 1
		for index := range mutated.Measurements {
			phase := mutated.Measurements[index].Phase
			if phase == "teardown" || remaining == 0 {
				break
			}
			deadlineIndex := slices.IndexFunc(plan.PhaseDeadlines, func(value PhaseDeadline) bool {
				return value.Phase == phase
			})
			if deadlineIndex < 0 {
				t.Fatalf("phase %q lacks a deadline", phase)
			}
			wall := min(plan.PhaseDeadlines[deadlineIndex].DeadlineMS, remaining)
			mutated.Measurements[index].Metrics.WallMS = Milliseconds(wall)
			remaining -= wall
		}
		if remaining != 0 {
			t.Fatalf("phase deadlines leave %d ms of aggregate ceiling mutation", remaining)
		}
		if err := ValidateReceipt(
			mutated, plan, binding, returnedPackageTestBinding(t, mutated, plan, binding),
		); err == nil {
			t.Fatal("failed teardown masked a pre-teardown aggregate wall crossing")
		}
	})
}

func TestReceiptExactOracleStopRequiresFullProjection(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	base := completeTestReceipt(t, plan, binding)
	phase := "product_queries"
	phaseIndex := slices.Index(plan.PhaseOrder, phase)
	stateIndex := slices.IndexFunc(base.StateResults, func(value ExactPhaseEvidence) bool { return value.Phase == phase })
	projection := testPhaseStateProjection(plan, phase)
	if phaseIndex < 0 || stateIndex < 0 || len(projection.ExtractionRoots) == 0 || len(projection.RelationshipResults) == 0 {
		t.Fatal("exact-oracle fixture is incomplete")
	}
	projection.ProductRelationship.TotalProjections++
	projectionSHA256 := mustReceiptSHA256(t, projection)
	observation := FailureObservation{
		Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "exact_mismatch", Metric: "state_projection",
		ExpectedSHA256: base.StateResults[stateIndex].ExpectedSHA256, ObservedSHA256: projectionSHA256,
	}
	observation.EvidenceSHA256 = mustReceiptSHA256(t, observation)
	receipt := cloneTestReceipt(t, base)
	stopTestReceipt(t, &receipt, plan, phase, ReceiptFailure{
		Phase: phase, Class: "oracle", Code: "exact_oracle_mismatch", Observation: observation,
	})
	observed := *base.StateResults[stateIndex].Observed
	observed.ProjectionSHA256, observed.Projection = projectionSHA256, &projection
	runtime := *base.StateResults[stateIndex].Runtime
	receipt.StateResults[stateIndex].ObservedProjectionSHA256 = projectionSHA256
	receipt.StateResults[stateIndex].Observed = &observed
	receipt.StateResults[stateIndex].ObservedSHA256 = mustReceiptSHA256(t, observed)
	receipt.StateResults[stateIndex].Runtime = &runtime
	receipt.StateResults[stateIndex].RuntimeSHA256 = mustReceiptSHA256(t, runtime)
	if err := ValidateReceipt(
		receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
	); err != nil {
		t.Fatal(err)
	}

	rebindProjection := func(t *testing.T, changed *Receipt) {
		t.Helper()
		state := &changed.StateResults[stateIndex]
		projectionSHA256 := mustReceiptSHA256(t, *state.Observed.Projection)
		state.ObservedProjectionSHA256 = projectionSHA256
		state.Observed.ProjectionSHA256 = projectionSHA256
		state.ObservedSHA256 = mustReceiptSHA256(t, *state.Observed)
		failure := changed.PhaseResults[phaseIndex].Failure
		failure.Observation.ObservedSHA256 = projectionSHA256
		failure.Observation.EvidenceSHA256 = ""
		failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
	}
	for _, test := range []struct {
		name   string
		mutate func(*PhaseStateProjection)
	}{
		{
			name: "catalog source schema",
			mutate: func(value *PhaseStateProjection) {
				value.CatalogSource.Schema = "arbitrary"
			},
		},
		{
			name: "extraction root domain",
			mutate: func(value *PhaseStateProjection) {
				value.ExtractionRoots[0].Domain = "arbitrary"
			},
		},
		{
			name: "extraction root availability",
			mutate: func(value *PhaseStateProjection) {
				value.ExtractionRoots[0].Availability = "arbitrary"
			},
		},
		{
			name: "relationship name",
			mutate: func(value *PhaseStateProjection) {
				value.RelationshipResults[0].Name = "arbitrary"
			},
		},
		{
			name: "product canonicalization",
			mutate: func(value *PhaseStateProjection) {
				value.ProductRelationship.Canonicalization = "arbitrary"
			},
		},
		{
			name: "malformed set digest",
			mutate: func(value *PhaseStateProjection) {
				value.Catalog.SHA256 = "malformed"
			},
		},
		{
			name: "malformed projection digest",
			mutate: func(value *PhaseStateProjection) {
				value.SemanticSHA256 = "malformed"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneTestReceipt(t, receipt)
			test.mutate(changed.StateResults[stateIndex].Observed.Projection)
			rebindProjection(t, &changed)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("exact-oracle stop with arbitrary diagnostic projection text was accepted")
			}
		})
	}

	receipt.StateResults[stateIndex].Observed.Projection = nil
	receipt.StateResults[stateIndex].ObservedSHA256 = mustReceiptSHA256(t, *receipt.StateResults[stateIndex].Observed)
	if err := ValidateReceipt(
		receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
	); err == nil {
		t.Fatal("exact-oracle stop without its full projection was accepted")
	}
}

func TestReceiptLifecycleErrorUsesNativeBoundedProjection(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	receipt := completeTestReceipt(t, plan, binding)
	phase := "pressure_80"
	phaseIndex := slices.Index(plan.PhaseOrder, phase)
	if phaseIndex < 0 {
		t.Fatal("lifecycle failure phase is absent")
	}
	evidence := FailureEvidenceProjection{
		Schema: plan.ReceiptContract.FailureObservationSchema + "/public-projection-v1",
		Kind:   "lifecycle",
		Lifecycle: &LifecycleFailureEvidence{
			Phase: phase,
			Owner: LifecycleOwnerResult{
				Name: plan.WorkEnvelope.LifecycleOwners[0], State: "error",
				Completeness: string(lifecycle.Unavailable), AttemptedAtUnixMS: 1,
			},
			CapacityCompleteness: "unavailable", CapacityPressure: "unavailable",
			ErrorClass: "io", EventOrdinal: receipt.Measurements[phaseIndex].StartEventOrdinal + 1,
		},
	}
	observedSHA256 := mustReceiptSHA256(t, evidence)
	observation := FailureObservation{
		Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "typed_error",
		Metric: "lifecycle_error", ObservedSHA256: observedSHA256,
	}
	observation.EvidenceSHA256 = mustReceiptSHA256(t, observation)
	failure := ReceiptFailure{
		Phase: phase, Class: "lifecycle", Code: "lifecycle_error",
		Observation: observation, Evidence: &evidence,
	}
	stopTestReceipt(t, &receipt, plan, phase, failure)
	if err := ValidateReceipt(
		receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
	); err != nil {
		t.Fatal(err)
	}

	mutated := cloneTestReceipt(t, receipt)
	mutatedFailure := mutated.PhaseResults[phaseIndex].Failure
	mutatedFailure.Evidence.Lifecycle.Owner.Completeness = "private_source_note"
	mutatedFailure.Observation.ObservedSHA256 = mustReceiptSHA256(t, *mutatedFailure.Evidence)
	mutatedFailure.Observation.EvidenceSHA256 = ""
	mutatedFailure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, mutatedFailure.Observation)
	if err := ValidateReceipt(
		mutated, plan, binding, returnedPackageTestBinding(t, mutated, plan, binding),
	); err == nil {
		t.Fatal("lifecycle error accepted a non-native owner projection")
	}

	mutated = cloneTestReceipt(t, receipt)
	mutated.Teardown.Completed = false
	mutated.Teardown.Outcome = "failed"
	mutated.Teardown.Failure = teardownFailure(t, plan, "teardown_incomplete")
	mutated.Decision = ReceiptDecision{
		Outcome: "stopped", Selected: "reduce", RulePriority: 4,
		Reason: "teardown_failed", Substantiated: true,
		Gate2V2: plan.Claims.Gate2V2, ReleasePosture: plan.Claims.ReleasePosture,
	}
	remaining := plan.SafetyEnvelope.MaximumTotalWallMS + 1
	for index := 0; index <= phaseIndex && remaining != 0; index++ {
		wall := min(plan.PhaseDeadlines[index].DeadlineMS, remaining)
		mutated.Measurements[index].Metrics.WallMS = Milliseconds(wall)
		remaining -= wall
	}
	if remaining != 0 {
		t.Fatalf("stopped phase deadlines leave %d ms of aggregate ceiling mutation", remaining)
	}
	if err := ValidateReceipt(
		mutated, plan, binding, returnedPackageTestBinding(t, mutated, plan, binding),
	); err == nil {
		t.Fatal("lifecycle stop plus failed teardown masked a pre-teardown aggregate wall crossing")
	}

	forged := cloneTestReceipt(t, mutated)
	forgedFailure := forged.PhaseResults[phaseIndex].Failure
	forgedFailure.Class = "resource"
	forgedFailure.Code = "total_wall_ceiling"
	forgedFailure.Evidence = nil
	forgedFailure.Observation = FailureObservation{
		Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "gauge_limit",
		Metric: "total_wall_ms", Limit: plan.SafetyEnvelope.MaximumTotalWallMS,
		Observed: plan.SafetyEnvelope.MaximumTotalWallMS + 2,
	}
	forgedFailure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, forgedFailure.Observation)
	if err := ValidateReceipt(
		forged, plan, binding, returnedPackageTestBinding(t, forged, plan, binding),
	); err == nil {
		t.Fatal("failed teardown accepted a forged aggregate wall observation")
	}
}

func TestReceiptTransitionMismatchRoundTripUsesContentAddressedAuthority(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	receipt := completeTestReceipt(t, plan, binding)
	phase := "pressure_80"
	phaseIndex := slices.Index(plan.PhaseOrder, phase)
	expectedSHA256, err := transitionExpectationSHA256(phase, plan, binding.freeze)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := FailureObservation{
		Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "exact_mismatch", Metric: "transition",
		ExpectedSHA256: expectedSHA256, ObservedSHA256: testDigest("transition", "placeholder"),
	}
	placeholder.EvidenceSHA256 = mustReceiptSHA256(t, placeholder)
	stopTestReceipt(t, &receipt, plan, phase, ReceiptFailure{
		Phase: phase, Class: "oracle", Code: "transition_mismatch", Observation: placeholder,
	})
	outcomes := make(map[string]string, len(receipt.PhaseResults))
	for _, result := range receipt.PhaseResults {
		outcomes[result.Name] = result.Outcome
	}
	operational := plan.PhaseOrder[1 : len(plan.PhaseOrder)-1]
	authorities, err := resolveAuthorityResults(
		receipt.Authority.ExtractionRootSnapshots, receipt.Authority.Snapshots,
		receipt.Authority.Results, operational, outcomes,
	)
	if err != nil {
		t.Fatal(err)
	}
	authorityIndex := slices.IndexFunc(authorities, func(value AuthorityPhaseResult) bool {
		return value.Phase == phase
	})
	transitionIndex := slices.IndexFunc(receipt.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == phase
	})
	if authorityIndex < 0 || transitionIndex < 0 || len(authorities[authorityIndex].ExtractionRoots) == 0 {
		t.Fatal("transition mismatch fixture lacks hydrated authority")
	}
	transition := &receipt.TransitionResults[transitionIndex]
	projection := TransitionFailureProjection{
		Schema: plan.ReceiptContract.TransitionSchema + "/failed-projection-v1", Phase: phase,
		Boundary: "pressure_gate_and_lifecycle_fence", LastCompletedStep: "lifecycle_fenced",
		EventOrdinal: transition.StartEventOrdinal + 1, Authority: authorities[authorityIndex],
	}
	transition.FailureProjection = &projection
	failure := receipt.PhaseResults[phaseIndex].Failure
	failure.Observation.ObservedSHA256 = mustReceiptSHA256(t, projection)
	failure.Observation.EvidenceSHA256 = ""
	failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
	returned := returnedPackageTestBinding(t, receipt, plan, binding)
	if err := ValidateReceipt(receipt, plan, binding, returned); err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReceipt(raw, plan, binding, returned)
	if err != nil {
		t.Fatal(err)
	}
	decodedTransition := decoded.TransitionResults[transitionIndex]
	if decodedTransition.FailureProjection == nil || decodedTransition.FailureProjection.Authority.ExtractionRoots != nil ||
		len(decoded.Authority.ExtractionRootSnapshots) == 0 {
		t.Fatal("decoded transition mismatch did not retain compact content-addressed authority")
	}
	roundTrip, err := MarshalCanonical(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundTrip, raw) {
		t.Fatal("transition mismatch receipt changed across canonical round trip")
	}
}

func TestReceiptMeasurementUnavailableIsPluralSortedAndUnique(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	base := completeTestReceipt(t, plan, binding)
	phase := "product_queries"
	observation := FailureObservation{
		Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "measurement_unavailable",
		UnavailableMetrics: []string{"data_allocated_bytes", "peak_rss_bytes"},
	}
	observation.EvidenceSHA256 = mustReceiptSHA256(t, observation)
	receipt := cloneTestReceipt(t, base)
	stopTestReceipt(t, &receipt, plan, phase, ReceiptFailure{
		Phase: phase, Class: "internal", Code: "measurement_unavailable", Observation: observation,
	})
	measurementIndex := slices.Index(plan.PhaseOrder, phase)
	receipt.Measurements[measurementIndex].Metrics.AllocationMeasurementAvailable = false
	receipt.Measurements[measurementIndex].Metrics.DataAllocatedBytes = 0
	receipt.Measurements[measurementIndex].Metrics.ProcessMeasurementAvailable = false
	receipt.Measurements[measurementIndex].Metrics.PeakRSSBytes = 0
	if err := ValidateReceipt(
		receipt, plan, binding, returnedPackageTestBinding(t, receipt, plan, binding),
	); err != nil {
		t.Fatal(err)
	}

	for _, metrics := range [][]string{
		{"peak_rss_bytes", "data_allocated_bytes"},
		{"data_allocated_bytes", "data_allocated_bytes"},
	} {
		changed := cloneTestReceipt(t, receipt)
		failure := changed.PhaseResults[measurementIndex].Failure
		failure.Observation.UnavailableMetrics = metrics
		failure.Observation.EvidenceSHA256 = ""
		failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatalf("invalid unavailable metric inventory %v was accepted", metrics)
		}
	}
}

func TestDecodeReceiptRejectsUnknownTrailingAndNoncanonical(t *testing.T) {
	plan := frozenTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	receipt := completeTestReceipt(t, plan, binding)
	returned := returnedPackageTestBinding(t, receipt, plan, binding)
	raw, err := MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	invalid := [][]byte{
		append([]byte("{\n  \"unknown\": true,"), raw[1:]...),
		append(bytes.Clone(raw), []byte("{}")...),
		bytes.Repeat([]byte{'x'}, int(plan.ReceiptContract.MaximumBytes)+1),
	}
	compact, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	invalid = append(invalid, compact)
	for _, value := range invalid {
		if _, err := DecodeReceipt(value, plan, binding, returned); err == nil {
			t.Fatal("invalid receipt bytes were accepted")
		}
	}
}

func frozenReceiptTestBinding(t *testing.T, plan Plan) ExecutionFreezeBinding {
	t.Helper()
	return admittedExecutionFreezeTestBinding(t, plan, executionFreezeTestCommits())
}

func executionFreezeTestAdmission(t *testing.T, plan Plan, freeze ExecutionFreeze) ExecutionFreezeAdmissionBinding {
	t.Helper()
	freezeSHA256, err := receiptSHA256(freeze)
	if err != nil {
		t.Fatal(err)
	}
	admissionEventSHA256, err := receiptSHA256(struct {
		Schema             string `json:"schema"`
		FreezeSHA256       string `json:"freeze_sha256"`
		SignatureNamespace string `json:"signature_namespace"`
		SignerFingerprint  string `json:"signer_fingerprint"`
		Order              string `json:"order"`
		EventOrdinal       uint64 `json:"event_ordinal"`
	}{
		Schema: plan.ReceiptContract.ExecutionAdmissionSchema, FreezeSHA256: freezeSHA256,
		SignatureNamespace: plan.SealPolicy.FreezeSignatureNamespace,
		SignerFingerprint:  executionFreezeTestSigner(),
		Order:              plan.ReceiptContract.ExecutionAdmissionOrder,
		EventOrdinal:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ExecutionFreezeAdmissionBinding{
		schema: plan.ReceiptContract.ExecutionAdmissionSchema, freezeSHA256: freezeSHA256,
		signatureNamespace: plan.SealPolicy.FreezeSignatureNamespace,
		signerFingerprint:  executionFreezeTestSigner(), admissionEventSHA256: admissionEventSHA256,
		admissionEventOrdinal: 1, signatureVerified: true, verifiedBeforeWork: true,
	}
}

func completeTestReceipt(t *testing.T, plan Plan, binding ExecutionFreezeBinding) Receipt {
	t.Helper()
	freeze := binding.freeze
	outcomes := make(map[string]string, len(plan.PhaseOrder))
	phases := make([]PhaseResult, len(plan.PhaseOrder))
	measurements := make([]PhaseMeasurement, len(plan.PhaseOrder))
	for index, phase := range plan.PhaseOrder {
		outcome := "passed"
		if phase == "teardown" {
			outcome = plan.ReceiptContract.TeardownPhaseOutcome
		}
		outcomes[phase] = outcome
		phases[index] = PhaseResult{Name: phase, Outcome: outcome}
		measurements[index] = testPhaseMeasurement(plan, freeze, index)
	}
	revisions := completeTestRevisionResults(t, plan, outcomes)
	rootSnapshots, authoritySnapshots, authorityReferences, authorities := completeTestAuthorities(t, plan, outcomes, revisions)
	states, err := expectedStateProjectionDigests(plan)
	if err != nil {
		t.Fatal(err)
	}
	operational := plan.PhaseOrder[1 : len(plan.PhaseOrder)-1]
	stateResults := make([]ExactPhaseEvidence, len(operational))
	for index, phase := range operational {
		observed := testObservedPhaseState(t, plan, phase, authorities[index])
		runtime := testPhaseRuntime(t, freeze, phase, measurements)
		stateResults[index] = ExactPhaseEvidence{
			Phase: phase, Outcome: "passed", ExpectedSHA256: states[phase],
			ObservedProjectionSHA256: observed.ProjectionSHA256,
			ObservedSHA256:           mustReceiptSHA256(t, observed), Observed: &observed,
			RuntimeSHA256: mustReceiptSHA256(t, runtime), Runtime: &runtime,
		}
	}
	transitions := completeTestTransitions(t, plan, freeze, authorities, measurements)
	productAuthority, err := authorityResultSHA256(authorities, "product_queries")
	if err != nil {
		t.Fatal(err)
	}
	queries := make([]QueryResult, len(plan.Oracle.QueryCases))
	var queryControlReads, queryMemberReads uint64
	for index, value := range plan.Oracle.QueryCases {
		queries[index] = expectedQueryResult(value, plan.ReceiptContract.QueryTransportSchema, productAuthority, plan.Schema)
		queryControlReads += queries[index].HTTP.ControlReads + queries[index].MCP.ControlReads
		queryMemberReads += queries[index].HTTP.MemberReads + queries[index].MCP.MemberReads
	}
	productMeasurement := slices.IndexFunc(measurements, func(value PhaseMeasurement) bool { return value.Phase == "product_queries" })
	measurements[productMeasurement].Metrics.ControlReads = CountMetric(max(
		uint64(measurements[productMeasurement].Metrics.ControlReads), queryControlReads,
	))
	measurements[productMeasurement].Metrics.MemberReads = CountMetric(max(
		uint64(measurements[productMeasurement].Metrics.MemberReads), queryMemberReads,
	))
	relationships := make([]RelationshipResult, len(plan.Oracle.Relationships))
	for index, value := range plan.Oracle.Relationships {
		relationships[index] = expectedRelationshipResult(value)
	}
	sourceVerificationSHA256 := SHA256([]byte("t422-test-source-verification"))
	return Receipt{
		Schema: plan.ReceiptContract.Schema,
		Authority: ReceiptAuthority{
			PlanSchema: plan.Schema, PlanSHA256: mustReceiptSHA256(t, plan), SourceCommit: plan.SourceCommit,
			ProfileSHA256: mustReceiptSHA256(t, plan.Profile), OracleSHA256: mustReceiptSHA256(t, plan.Oracle),
			RevisionHistorySHA256:    mustReceiptSHA256(t, plan.Revisions),
			MeterPolicySHA256:        mustReceiptSHA256(t, plan.MeterPolicy),
			SourceVerificationSHA256: sourceVerificationSHA256,
			ExtractionRootSnapshots:  rootSnapshots,
			Snapshots:                authoritySnapshots, Results: authorityReferences,
		},
		Decision: ReceiptDecision{
			Outcome: "passed", Selected: "continue", RulePriority: 3,
			Reason: "all_exact_checks_passed", Substantiated: true,
			Gate2V2: plan.Claims.Gate2V2, ReleasePosture: plan.Claims.ReleasePosture,
		},
		Environment: ReceiptEnvironment{Fields: freeze.Host, SHA256: mustReceiptSHA256(t, freeze.Host)},
		ExecutionFreeze: ReceiptExecutionFreeze{
			Schema: freeze.Schema, SHA256: binding.freezeSHA256,
			SignerFingerprint: freeze.SignerFingerprint, AdmissionEventSHA256: binding.admissionEventSHA256,
			AdmissionEventOrdinal: binding.admissionEventOrdinal,
			Commits:               freeze.Commits,
		},
		Implementation: ReceiptImplementation{
			IntegratedMainCommit: freeze.Commits.IntegratedMainCommit,
			T422SourceCommit:     freeze.Commits.T422SourceCommit, CleanTree: true,
			DigestAlgorithm: plan.ToolPolicy.DigestAlgorithm, Tools: slices.Clone(freeze.Tools),
		},
		Inputs: slices.Clone(plan.Inputs), Measurements: measurements,
		NonClaims: receiptNonClaims(plan.Claims), PhaseResults: phases,
		StateResults: stateResults, TransitionResults: transitions,
		QueryResults: QueryEvidence{Phase: "product_queries", Outcome: "passed", Results: queries},
		RelationshipResults: RelationshipEvidence{
			Phase: "product_queries", Outcome: "passed",
			AuthorityBeforeSHA256: productAuthority, AuthorityAfterSHA256: productAuthority,
			RelationshipRootReads: 1, RelationshipGenerationReads: 1,
			Results: relationships, Caller: ptr(testCallerPublication(t, plan, authorities[len(authorities)-1])),
			Product: ptr(expectedProductRelationshipResult(plan.Oracle.ProductRelationships)),
		},
		RevisionResults: revisions,
		Seal: ReceiptSeal{
			PolicySchema: plan.SealPolicy.Schema, SignerFingerprint: freeze.SignerFingerprint,
			FreezeSignatureNamespace:             plan.SealPolicy.FreezeSignatureNamespace,
			SourceVerificationSignatureNamespace: plan.SealPolicy.SourceVerificationSignatureNamespace,
			ReturnedSignatureNamespace:           plan.SealPolicy.ReturnedSignatureNamespace,
			VerificationPosture:                  "freeze_preflight_source_and_returned_signatures_verified_by_external_bindings",
		},
		Teardown: ReceiptTeardown{
			Attempted: true, Completed: true, Outcome: "clean", DescendantsStopped: true, StoreClosed: true,
			PressureVolumeDetached: true, PressureImageRemoved: true,
			BackingVolumeIdentity: freeze.Host.BackingVolumeIdentity, RetainedSourceFreeOnly: true,
		},
		SourceFree: true,
	}
}

func returnedPackageTestBinding(
	t *testing.T,
	receipt Receipt,
	plan Plan,
	binding ExecutionFreezeBinding,
) ReturnedPackageBinding {
	t.Helper()
	return ReturnedPackageBinding{
		signerFingerprint:          binding.expectedSignerFingerprint,
		receiptSHA256:              mustReceiptSHA256(t, receipt),
		packageSHA256:              SHA256([]byte("t422-test-returned-package")),
		inventorySHA256:            SHA256([]byte("t422-test-returned-inventory")),
		exactInventory:             slices.Clone(plan.SealPolicy.ExactInventory),
		returnedSignatureVerified:  true,
		sourceSignatureVerified:    true,
		returnedSignatureNamespace: plan.SealPolicy.ReturnedSignatureNamespace,
		sourceSignatureNamespace:   plan.SealPolicy.SourceVerificationSignatureNamespace,
		sourceVerificationSHA256:   receipt.Authority.SourceVerificationSHA256,
		sourceVerificationSchema:   plan.SealPolicy.SourceVerificationSchema,
		sourcePlanSHA256:           mustReceiptSHA256(t, plan),
		sourceFreezeSHA256:         binding.freezeSHA256,
		sourceExactInventorySHA256: mustReceiptSHA256(t, plan.SealPolicy.ExactInventory),
		revisionResultsSHA256:      mustReceiptSHA256(t, receipt.RevisionResults),
		sourceVerified:             true,
	}
}

func mustReceiptSHA256(t *testing.T, value any) string {
	t.Helper()
	digest, err := receiptSHA256(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func zeroDigest() string { return SHA256(nil) }

func cloneTestReceipt(t *testing.T, value Receipt) Receipt {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result Receipt
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func ptr[T any](value T) *T { return &value }

func completeTestRevisionResults(t *testing.T, plan Plan, outcomes map[string]string) []RevisionResult {
	t.Helper()
	physicalPhases := []string{"cold", "physical_delta_b", "return_a"}
	logicalPhases := []string{"cold", "logical_delta_b", "return_a"}
	result := make([]RevisionResult, 3)
	for index, name := range []string{"a", "b", "a-return"} {
		physical, _ := namedPhysicalRevision(plan.Revisions.Physical, name)
		logical, _ := namedLogicalRevision(plan.Revisions.Logical, name)
		value := RevisionResult{
			Name: name, PhysicalPhase: physicalPhases[index], PhysicalOutcome: outcomes[physicalPhases[index]],
			LogicalPhase: logicalPhases[index], LogicalOutcome: outcomes[logicalPhases[index]],
			PhysicalTreeRecipeSHA256:   physical.SourceTreeRecipeSHA256,
			PhysicalCommitRecipeSHA256: physical.SourceCommitRecipeSHA256,
			CatalogLogicalSHA256:       logical.CatalogLogicalSHA256, SemanticSHA256: logical.SemanticSHA256,
			CatalogSource: logical.CatalogSource,
		}
		if value.PhysicalOutcome == "passed" {
			value.PhysicalCommit, value.PhysicalTree = physical.ExpectedCommit, physical.ExpectedTree
			if index > 0 {
				value.PhysicalParentCommit = result[index-1].PhysicalCommit
			}
			recipe := plan.Revisions.SourceRecipe
			value.AuthoredManifest = AuthoredSourceManifest{
				Schema: recipe.AuthoredManifestSchema, BaseCommit: physical.BaseCommit, BaseTree: physical.BaseTree,
				Overlay:                 SetIdentity{Records: recipe.OverlayRecords, FramedBytes: recipe.OverlayFramedBytes, SHA256: recipe.OverlaySHA256},
				GeneratedMappingRecords: recipe.GeneratedMappingRecords, GeneratedMappingPath: recipe.GeneratedMappingPath,
				GeneratedMappingMode: recipe.GeneratedMappingMode, GeneratedMappingBytes: recipe.GeneratedMappingBytes,
				GeneratedMappingSHA256: recipe.GeneratedMappingSHA256, TypedInputRecords: recipe.TypedInputRecords,
				TypedInputKind: recipe.TypedInputKind, TypedInputPath: recipe.TypedInputPath,
				TypedInputMode: recipe.TypedInputMode, TypedInputBytes: recipe.TypedInputBytes,
				TypedInputSHA256: recipe.TypedInputSHA256, TypedInputBlobOID: recipe.TypedInputBlobOID,
				AddedRegularFiles: recipe.OverlayRecords + plan.Profile.GeneratedMapping.RegularFiles + plan.Profile.TypedIndex.RegularFiles,
				RegularFiles:      plan.Profile.Physical.CombinedRegularFiles, TreeInventory: physical.ExpectedTreeInventory,
				TreeObjectID: physical.ExpectedTree,
			}
			commitBytes, err := canonicalGitCommitBytes(value, physical, recipe)
			if err != nil {
				t.Fatal(err)
			}
			value.AuthoredManifest.CommitBytesSHA256 = SHA256(commitBytes)
		}
		result[index] = value
	}
	return result
}

func completeTestAuthorities(
	t *testing.T,
	plan Plan,
	outcomes map[string]string,
	revisions []RevisionResult,
) ([]ExtractionRootSnapshot, []AuthoritySnapshot, []AuthorityPhaseReference, []AuthorityPhaseResult) {
	t.Helper()
	operational := plan.PhaseOrder[1 : len(plan.PhaseOrder)-1]
	results := make([]AuthorityPhaseResult, len(operational))
	statesByGroup := make(map[string]AuthorityPhaseResult)
	if plan.Schema == PlanV2Schema {
		statesByGroup = productionAuthorityFixture(t, plan)
	}
	for index, phase := range operational {
		stateIndex := slices.IndexFunc(plan.PhaseStates, func(value PhaseState) bool { return value.Phase == phase })
		phaseState := plan.PhaseStates[stateIndex]
		group := testAuthorityGroup(phase)
		template, ok := statesByGroup[group]
		if !ok {
			if plan.Schema == PlanV2Schema {
				t.Fatalf("native constructor fixture omitted authority group %q", group)
			}
			physical, _ := namedPhysicalRevision(plan.Revisions.Physical, phaseState.PhysicalRevision)
			revision, _ := namedRevisionResult(revisions, phaseState.PhysicalRevision)
			state := AuthorityState{
				PhysicalRevision: phaseState.PhysicalRevision, LogicalRevision: phaseState.LogicalRevision,
				PhysicalCommit: revision.PhysicalCommit, PhysicalTree: revision.PhysicalTree,
				SourceGenerationSHA256: testDigest(group, "source"), SearchGenerationSHA256: testDigest(group, "search"),
				ObservationGenerationSHA256:     testDigest(group, "observation"),
				CandidateGenerationSHA256:       testDigest(group, "candidate-generation"),
				CatalogRootSHA256:               testDigest(group, "catalog"),
				CatalogActivationPlanSHA256:     testDigest(group, "activation-plan"),
				CatalogActivationScheduleSHA256: testDigest(group, "activation-schedule"),
				CatalogActivationUnitSHA256:     testDigest(group, "activation-unit"),
				ResolverCatalogGenerationSHA256: testDigest(group, "resolver-generation"),
				ResolverCatalogRootSHA256:       testDigest(group, "resolver-root"),
				CallerGenerationSHA256:          testDigest(group, "caller-generation"), CallerRootSHA256: testDigest(group, "caller-root"),
				RelationshipGenerationSHA256: testDigest(group, "relationship-generation"),
				RelationshipRootSHA256:       testDigest(group, "relationship-root"),
				SearchInventory:              physical.ExpectedTreeInventory,
				ObservationInputInventory:    physical.ExpectedObservationInputInventory, Current: true,
			}
			var roots []ExtractionRootResult
			switch group {
			case "archive":
				base := statesByGroup["a-return"]
				state, roots = base.AuthorityState, base.ExtractionRoots
				if plan.Schema == PlanSchema {
					state.ResolverCatalogGenerationSHA256 = testDigest(group, "resolver-generation")
					state.ResolverCatalogRootSHA256 = testDigest(group, "resolver-root")
					state.CallerGenerationSHA256 = testDigest(group, "caller-generation")
					state.CallerRootSHA256 = testDigest(group, "caller-root")
				}
				state.RelationshipGenerationSHA256 = testDigest(group, "relationship-generation")
				state.RelationshipRootSHA256 = testDigest(group, "relationship-root")
			case "b-logical":
				base := statesByGroup["b-physical"]
				state, roots = base.AuthorityState, base.ExtractionRoots
				state.LogicalRevision = phaseState.LogicalRevision
				state.CatalogRootSHA256 = testDigest(group, "catalog")
				state.CatalogActivationPlanSHA256 = testDigest(group, "activation-plan")
				state.CatalogActivationScheduleSHA256 = testDigest(group, "activation-schedule")
				state.CatalogActivationUnitSHA256 = testDigest(group, "activation-unit")
				if plan.Schema == PlanSchema {
					state.ResolverCatalogGenerationSHA256 = testDigest(group, "resolver-generation")
					state.ResolverCatalogRootSHA256 = testDigest(group, "resolver-root")
					state.CallerGenerationSHA256 = testDigest(group, "caller-generation")
					state.CallerRootSHA256 = testDigest(group, "caller-root")
				}
				state.RelationshipGenerationSHA256 = testDigest(group, "relationship-generation")
				state.RelationshipRootSHA256 = testDigest(group, "relationship-root")
			default:
				roots = testExtractionRoots(t, plan, physical, state, group)
			}
			state.ExtractionRootsSHA256 = mustReceiptSHA256(t, roots)
			template = AuthorityPhaseResult{AuthorityState: state, ExtractionRoots: roots}
			statesByGroup[group] = template
		}
		template.ExtractionRoots = slices.Clone(template.ExtractionRoots)
		for index := range template.ExtractionRoots {
			template.ExtractionRoots[index].PartitionResults = slices.Clone(template.ExtractionRoots[index].PartitionResults)
		}
		template.Phase, template.Outcome = phase, outcomes[phase]
		results[index] = template
	}
	rootSnapshotByDigest := make(map[string]ExtractionRootSnapshot)
	snapshotByDigest := make(map[string]AuthoritySnapshot)
	references := make([]AuthorityPhaseReference, len(results))
	for index, result := range results {
		rootSnapshotByDigest[result.ExtractionRootsSHA256] = ExtractionRootSnapshot{
			SHA256: result.ExtractionRootsSHA256, Roots: result.ExtractionRoots,
		}
		digest := mustReceiptSHA256(t, result.AuthorityState)
		snapshotByDigest[digest] = AuthoritySnapshot{SHA256: digest, AuthorityState: result.AuthorityState}
		references[index] = AuthorityPhaseReference{Phase: result.Phase, Outcome: result.Outcome, SnapshotSHA256: digest}
	}
	rootSnapshots := make([]ExtractionRootSnapshot, 0, len(rootSnapshotByDigest))
	for _, value := range rootSnapshotByDigest {
		rootSnapshots = append(rootSnapshots, value)
	}
	slices.SortFunc(rootSnapshots, func(left, right ExtractionRootSnapshot) int {
		return strings.Compare(left.SHA256, right.SHA256)
	})
	snapshots := make([]AuthoritySnapshot, 0, len(snapshotByDigest))
	for _, value := range snapshotByDigest {
		snapshots = append(snapshots, value)
	}
	slices.SortFunc(snapshots, func(left, right AuthoritySnapshot) int { return strings.Compare(left.SHA256, right.SHA256) })
	return rootSnapshots, snapshots, references, results
}

func testAuthorityGroup(phase string) string {
	switch phase {
	case "cold", "warm_noop":
		return "a"
	case "physical_delta_b":
		return "b-physical"
	case "logical_delta_b":
		return "b-logical"
	case "archive_restore", "lifecycle_collection", "product_queries":
		return "archive"
	default:
		return "a-return"
	}
}

func testExtractionRoots(
	t *testing.T,
	plan Plan,
	physical PhysicalRevision,
	authority AuthorityState,
	group string,
) []ExtractionRootResult {
	t.Helper()
	roots := make([]ExtractionRootResult, len(plan.Profile.Pipeline.ExtractionDomains))
	for index, expected := range plan.Profile.Pipeline.ExtractionDomains {
		partitions := make([]ExtractionPartitionResult, len(expected.Partitions))
		for partitionIndex, profile := range expected.Partitions {
			disposition := "success"
			if profile.Expected == (ResultTotals{}) {
				disposition = "empty"
			}
			partitions[partitionIndex] = ExtractionPartitionResult{
				Ordinal: profile.Ordinal, Kind: profile.Kind, MemberOrdinal: profile.MemberOrdinal,
				CallerPrefix: profile.CallerPrefix, SourceStart: profile.SourceStart, SourceEnd: profile.SourceEnd,
				MemberRecordStart: profile.MemberRecordStart, MemberRecordEnd: profile.MemberRecordEnd,
				AdmittedRecords: profile.AdmittedRecords, Reservation: profile.Reservation,
				Disposition: disposition, Totals: profile.Expected,
				PartitionSHA256:      testDigest(group, expected.Domain, fmt.Sprint(profile.Ordinal), "partition"),
				ExpectationSHA256:    testDigest(group, expected.Domain, fmt.Sprint(profile.Ordinal), "expectation"),
				ResultDigestSHA256:   testDigest(group, expected.Domain, fmt.Sprint(profile.Ordinal), "result"),
				ResultIdentitySHA256: testDigest(group, expected.Domain, fmt.Sprint(profile.Ordinal), "identity"),
			}
		}
		typed, typedOK := namedTypedScopeRevision(expected.TypedScopeRevisions, physical.Name)
		root := ExtractionRootResult{
			Domain: expected.Domain, Current: true, GenerationSHA256: testDigest(group, expected.Domain, "generation"),
			RootSHA256:                  testDigest(group, expected.Domain, "root"),
			CandidateGenerationSHA256:   authority.CandidateGenerationSHA256,
			SourceGenerationSHA256:      authority.SourceGenerationSHA256,
			ObservationGenerationSHA256: authority.ObservationGenerationSHA256,
			PlanSHA256:                  testDigest(group, expected.Domain, "plan"), ScheduleSHA256: testDigest(group, expected.Domain, "schedule"),
			ApplicablePartitions: expected.ApplicablePartitions, MemberPartitions: expected.MemberPartitions,
			TypedPartitions: expected.TypedPartitions, TypedScopeRecords: expected.TypedScopeRecords,
			TypedScopePathBytes: expected.TypedScopePathBytes, TypedScopeEncodedBytes: expected.TypedScopeEncodedBytes,
			Candidates: physical.ExpectedCandidateInventories[index].Candidates,
			Members:    SetIdentity{Records: expected.ApplicablePartitions, FramedBytes: 1, SHA256: testDigest(group, expected.Domain, "members")},
			Reserved:   expected.Reserved, Totals: expected.Expected, PartitionResults: partitions,
		}
		if typedOK {
			root.TypedScopeSHA256, root.TypedScopeContentSHA256 = typed.SHA256, typed.DescriptorContentSHA256
		}
		root.PartitionResultsSHA256 = mustReceiptSHA256(t, root.PartitionResults)
		roots[index] = root
	}
	return roots
}

func testDigest(parts ...string) string { return SHA256([]byte(strings.Join(parts, "/"))) }

func testObservedPhaseState(
	t *testing.T,
	plan Plan,
	phase string,
	authority AuthorityPhaseResult,
) ObservedPhaseState {
	t.Helper()
	projection := testPhaseStateProjection(plan, phase)
	return ObservedPhaseState{
		Schema:                  plan.ReceiptContract.StateObservationSchema,
		ProjectionSHA256:        mustReceiptSHA256(t, projection),
		AuthoritySnapshotSHA256: mustReceiptSHA256(t, authority.AuthorityState),
		SourceAuthorityRecipe:   "source-generation-and-authored-tree-recipe-v1",
		AuthorityReader:         "current-root-then-generation-then-exact-member-inventory-v1",
		SemanticReader:          semanticReaderForObservationSchema(plan.ReceiptContract.StateObservationSchema),
	}
}

func testPhaseStateProjection(plan Plan, phase string) PhaseStateProjection {
	stateIndex := slices.IndexFunc(plan.PhaseStates, func(value PhaseState) bool { return value.Phase == phase })
	state := plan.PhaseStates[stateIndex]
	physical, _ := namedPhysicalRevision(plan.Revisions.Physical, state.PhysicalRevision)
	logical, _ := namedLogicalRevision(plan.Revisions.Logical, state.LogicalRevision)
	rootProjections := make([]ExtractionRootProjection, len(plan.Profile.Pipeline.ExtractionDomains))
	for index, domain := range plan.Profile.Pipeline.ExtractionDomains {
		typed, _ := namedTypedScopeRevision(domain.TypedScopeRevisions, physical.Name)
		rootProjections[index] = ExtractionRootProjection{
			Domain: domain.Domain, Availability: domain.Availability,
			ApplicablePartitions: domain.ApplicablePartitions, MemberPartitions: domain.MemberPartitions,
			TypedPartitions: domain.TypedPartitions, TypedScopeRecords: domain.TypedScopeRecords,
			TypedScopePathBytes: domain.TypedScopePathBytes, TypedScopeEncodedBytes: domain.TypedScopeEncodedBytes,
			TypedScopeSHA256: typed.SHA256, TypedScopeContentSHA256: typed.DescriptorContentSHA256,
			Candidates: physical.ExpectedCandidateInventories[index].Candidates,
			Reserved:   domain.Reserved, Expected: domain.Expected, PartitionShape: domain.PartitionShape,
		}
	}
	relationships := make([]RelationshipResult, len(plan.Oracle.Relationships))
	for index, family := range plan.Oracle.Relationships {
		relationships[index] = expectedRelationshipResult(family)
	}
	return PhaseStateProjection{
		Schema: plan.ReceiptContract.StateProjectionSchema, Phase: phase,
		PhysicalRevision: state.PhysicalRevision, LogicalRevision: state.LogicalRevision,
		CatalogLogicalSHA256: logical.CatalogLogicalSHA256, SemanticSHA256: logical.SemanticSHA256,
		CatalogSource: logical.CatalogSource, Catalog: logical.Catalog, MembershipSet: logical.Memberships,
		Placements: logical.Placements, UnownedPrefixes: logical.UnownedPrefixes, ServiceQueries: logical.ServiceQueries,
		SearchInventory:           physical.ExpectedTreeInventory,
		ObservationInputInventory: physical.ExpectedObservationInputInventory,
		ExtractionRoots:           rootProjections, RelationshipResults: relationships,
		ProductRelationship: expectedProductRelationshipResult(plan.Oracle.ProductRelationships),
	}
}

func testPhaseRuntime(
	t *testing.T,
	freeze ExecutionFreeze,
	phase string,
	measurements []PhaseMeasurement,
) PhaseRuntimeBinding {
	t.Helper()
	epoch, launchPhase, ok := expectedPhaseRuntime(freeze.Profile.Epochs, phase)
	if !ok {
		t.Fatalf("phase %q has no runtime epoch", phase)
	}
	launchIndex := slices.IndexFunc(measurements, func(value PhaseMeasurement) bool { return value.Phase == launchPhase })
	imageIndex := slices.IndexFunc(freeze.Tools, func(value ExecutionToolIdentity) bool { return value.Role == "phebs" })
	if launchIndex < 0 || imageIndex < 0 {
		t.Fatal("runtime fixture lacks launch measurement or phebs image")
	}
	image := freeze.Tools[imageIndex].SHA256
	value := PhaseRuntimeBinding{
		Schema: freeze.Profile.RuntimeBindingSchema, Phase: phase,
		ProfileSHA256: mustReceiptSHA256(t, freeze.Profile), InvocationSHA256: freeze.Profile.InvocationSHA256,
		ProcessImageSHA256:    image,
		ProcessIdentitySHA256: recipeDigest("t422-phebs-process-identity-v1", image, fmt.Sprint(epoch)),
		ServerEpoch:           epoch, StartEventOrdinal: measurements[launchIndex].StartEventOrdinal + 50,
	}
	if value.Schema == PhaseRuntimeBindingV2Schema && phase == launchPhase {
		elapsed := uint64(1)
		value.Startup = &ServerStartupEvidence{
			ServerEpoch: epoch, Outcome: "ready", FinishEventOrdinal: value.StartEventOrdinal + 1, ElapsedMS: &elapsed,
		}
	}
	return value
}

func testPhaseMeasurement(plan Plan, freeze ExecutionFreeze, index int) PhaseMeasurement {
	phase := plan.PhaseOrder[index]
	bound := plan.WorkEnvelope.Phases[index]
	metrics := ReceiptMetrics{
		AllocationMeasurementAvailable: true,
		AvailableDiskBytes:             Bytes(freeze.Host.PressureAvailableDiskBytes),
		TotalDiskBytes:                 Bytes(freeze.Host.PressureTotalDiskBytes),
		ProcessMeasurementAvailable:    true, PeakRSSBytes: 1, WallMS: 1,
		PhysicalCorpusPasses:   CountMetric(bound.PhysicalCorpusPasses.Minimum),
		ChangedPhysicalFiles:   CountMetric(bound.ChangedPhysicalFiles.Minimum),
		ChangedLogicalServices: CountMetric(bound.ChangedLogicalServices.Minimum),
		GitReads:               CountMetric(bound.GitReads.Minimum), IndexFiles: CountMetric(bound.IndexFiles.Minimum),
		ObservationParses:  CountMetric(bound.ObservationParses.Minimum),
		SourceLogicalBytes: Bytes(bound.SourceLogicalBytes.Minimum), SourceUniqueBytes: Bytes(bound.SourceUniqueBytes.Minimum),
		ApplicablePartitions: CountMetric(bound.ApplicablePartitions.Minimum), PublishedDomains: CountMetric(bound.PublishedDomains.Minimum),
		ControlReads: CountMetric(bound.ControlReads.Minimum), MemberReads: CountMetric(bound.MemberReads.Minimum),
		JobAttempts: CountMetric(bound.JobAttempts.Minimum), StoreTransactions: CountMetric(bound.StoreTransactions.Minimum),
		StoreRows: CountMetric(bound.StoreRows.Minimum), PublicationWrites: CountMetric(bound.PublicationWrites.Minimum),
		RelationshipBuildAttempts: CountMetric(bound.RelationshipBuildAttempts.Minimum),
		LifecycleOwnerTurns:       CountMetric(bound.LifecycleOwnerTurns.Minimum), LifecycleDeleted: CountMetric(bound.LifecycleDeleted.Minimum),
		CombinedPhysicalOwners:  CountMetric(bound.CombinedPhysicalOwners.Minimum),
		LogicalMemberships:      CountMetric(bound.LogicalMemberships.Minimum),
		RelationshipProjections: CountMetric(bound.RelationshipProjections.Minimum),
		ResolverBlobBytes:       Bytes(bound.ResolverBlobBytes.Minimum), ResolverBlobReads: CountMetric(bound.ResolverBlobReads.Minimum),
		ServiceReferences: CountMetric(bound.ServiceReferences.Minimum), ServiceRows: CountMetric(bound.ServiceRows.Minimum),
		UnsupportedSourceFiles: CountMetric(bound.UnsupportedSourceFiles.Minimum),
	}
	metrics.CacheRootReads = CountMetric(bound.CacheRootReads.Minimum)
	metrics.CensusChildren = CountMetric(bound.CensusChildren.Minimum)
	metrics.CensusRecords = CountMetric(bound.CensusRecords.Minimum)
	metrics.CacheRootValidations = metrics.CacheRootReads
	metrics.CacheMemberReads = CountMetric(bound.CacheMemberReads.Minimum)
	metrics.CacheMemberValidations = metrics.CacheMemberReads
	metrics.CacheMisses = metrics.CacheRootReads + metrics.CacheMemberReads
	metrics.CacheHits = CountMetric(bound.CacheHits.Minimum)
	metrics.CacheLookups = metrics.CacheHits + metrics.CacheMisses
	if index >= slices.Index(plan.PhaseOrder, "cold") && phase != "teardown" {
		metrics.DataAllocatedBytes, metrics.DataLogicalBytes = 1, 1
	}
	children := make([]Count, len(bound.ChildProcessRoles))
	for childIndex, role := range bound.ChildProcessRoles {
		children[childIndex] = Count{Name: role.Name, Count: role.Minimum}
		metrics.ChildProcesses += CountMetric(role.Minimum)
	}
	if metrics.StoreRows != 0 {
		metrics.MaxRowsTransaction = CountMetric(divCeil(uint64(metrics.StoreRows), uint64(metrics.StoreTransactions)))
	}
	if metrics.Retries != 0 {
		metrics.MaxRetriesUnit = CountMetric(divCeil(uint64(metrics.Retries), uint64(metrics.JobAttempts)))
	}
	if metrics.LifecycleDeleted != 0 {
		metrics.MaxLifecycleDeletesTurn = CountMetric(divCeil(uint64(metrics.LifecycleDeleted), uint64(metrics.LifecycleOwnerTurns)))
	}
	stateIndex := slices.IndexFunc(plan.PhaseStates, func(value PhaseState) bool { return value.Phase == phase })
	if stateIndex >= 0 {
		state := plan.PhaseStates[stateIndex]
		metrics.SourceReuseDecisions = CountMetric(boolCount(state.SourceAction == "reuse"))
		metrics.SearchReuseDecisions = CountMetric(boolCount(state.SearchAction == "reuse"))
		metrics.ObservationReuseDecisions = CountMetric(boolCount(state.ObservationAction == "reuse"))
		metrics.CatalogReuseDecisions = CountMetric(boolCount(state.CatalogAction == "reuse"))
		metrics.RelationshipReuseDecisions = CountMetric(boolCount(state.RelationshipAction == "reuse"))
		metrics.ReuseDecisions = metrics.SourceReuseDecisions + metrics.SearchReuseDecisions +
			metrics.ObservationReuseDecisions + metrics.CatalogReuseDecisions + metrics.RelationshipReuseDecisions
	}
	if phase == "cold" {
		metrics.CombinedPhysicalOwners = CountMetric(plan.Profile.Physical.CombinedPhysicalOwners)
		metrics.LogicalMemberships = CountMetric(plan.Profile.Logical.Memberships)
		metrics.RelationshipProjections = CountMetric(plan.Profile.Pipeline.RelationshipProjections)
		metrics.ResolverBlobBytes = Bytes(plan.Profile.Pipeline.ResolverBlobBytesPerBuild)
		metrics.ResolverBlobReads = CountMetric(plan.Profile.Pipeline.ResolverBlobReadsPerBuild)
		metrics.RelationshipBuildAttempts = 1
		metrics.ServiceReferences = CountMetric(plan.Profile.Pipeline.ServiceReferences)
		metrics.ServiceRows = CountMetric(plan.Profile.Logical.TotalServiceRecords)
		metrics.SourceLogicalBytes = Bytes(plan.Profile.Bytes.CombinedLogicalSourceBytes)
		metrics.SourceUniqueBytes = Bytes(plan.Profile.Bytes.CombinedUniqueContentBytesA)
		metrics.UnsupportedSourceFiles = CountMetric(plan.Profile.Pipeline.UnsupportedSourceFiles)
	}
	if phase == "preflight" {
		pressure := freeze.Pressure
		metrics.BallastCeilingBytes = Bytes(pressure.BallastCeilingBytes)
		metrics.PressureVolumeBytes = Bytes(pressure.PressureVolumeBytes)
		metrics.PressureCustodyMarginBytes = Bytes(pressure.CustodyMarginBytes)
		metrics.MinimumPrePressureUsedBytes = Bytes(pressure.MinimumPrePressureUsedBytes)
		metrics.MaximumPrePressureUsedBytes = Bytes(pressure.MaximumPrePressureUsedBytes)
		metrics.MinimumPrePressureAllocatedBytes = Bytes(pressure.MinimumPrePressureBytes)
		metrics.MaximumPrePressureAllocatedBytes = Bytes(pressure.MaximumPrePressureBytes)
	}
	return PhaseMeasurement{
		Phase: phase, StartEventOrdinal: uint64(index*100 + 10), FinishEventOrdinal: uint64(index*100 + 99),
		Metrics: metrics, ChildProcessRoles: children,
	}
}

func divCeil(value, divisor uint64) uint64 {
	if divisor == 0 {
		return math.MaxUint64
	}
	return (value + divisor - 1) / divisor
}

func completeTestTransitions(
	t *testing.T,
	plan Plan,
	freeze ExecutionFreeze,
	authorities []AuthorityPhaseResult,
	measurements []PhaseMeasurement,
) []TransitionResult {
	t.Helper()
	authority := make(map[string]AuthorityPhaseResult, len(authorities))
	for _, value := range authorities {
		authority[value.Phase] = value
	}
	result := make([]TransitionResult, len(transitionPhases))
	for index, phase := range transitionPhases {
		measurementIndex := slices.IndexFunc(measurements, func(value PhaseMeasurement) bool { return value.Phase == phase })
		measurement := &measurements[measurementIndex]
		transition := TransitionResult{
			Phase: phase, Outcome: "passed", StartEventOrdinal: measurement.StartEventOrdinal,
			FinishEventOrdinal: measurement.FinishEventOrdinal,
		}
		switch phase {
		case "physical_delta_b":
			measurement.Metrics.LifecycleOwnerTurns = 2
			before, _ := authorityIdentitySHA256(authority["warm_noop"])
			after, _ := authorityIdentitySHA256(authority[phase])
			start := transition.StartEventOrdinal
			reader := &ReaderTransition{
				Reader:                    plan.ReaderProbe.Reader,
				QuerySHA256:               plan.ReaderProbe.QuerySHA256,
				OldSearchGenerationSHA256: authority["warm_noop"].SearchGenerationSHA256,
				NewSearchGenerationSHA256: authority[phase].SearchGenerationSHA256,
				OldHeldRecords:            plan.ReaderProbe.ExpectedRecords, NewHeldRecords: plan.ReaderProbe.ExpectedRecords,
				OldHeldProjectionSHA256: plan.ReaderProbe.OldProjectionSHA256,
				NewHeldProjectionSHA256: plan.ReaderProbe.NewProjectionSHA256,
				LeaseAcquired:           1, OldVisibleWhileHeld: true, NewCurrentWhileHeld: true,
				LeaseReleased:            1,
				LeaseAcquireEventOrdinal: start + 1, NewCurrentEventOrdinal: start + 2,
				OldHeldQueryEventOrdinal: start + 4, NewHeldQueryEventOrdinal: start + 5,
				LeaseReleaseEventOrdinal: start + 6, AuthorityBeforeSHA256: before, AuthorityAfterSHA256: after,
			}
			if plan.Schema == PlanV2Schema {
				readBound, err := correctedPhysicalTransitionReadBound(plan.Profile)
				if err != nil {
					t.Fatal(err)
				}
				transition.ReadAccounting = &TransitionReadSubtotal{
					Schema: "t422-transition-read-accounting-v1", Class: correctedPhysicalTransitionReadClass,
					ReportCalls: readBound.Calls.Minimum, ControlFileReads: readBound.ControlFileReads.Minimum,
					StoreReadAttempts: readBound.StoreReadAttempts.Minimum, MemberReads: readBound.MemberReads.Minimum,
					StoreWriteAttempts: readBound.StoreWriteAttempts.Minimum,
				}
				reader.Schema = plan.ReceiptContract.TransitionSchema + "/reader-v2"
				reader.OldRoleAfterReplacement = plan.ReaderProbe.OldRoleAfterReplacement
				reader.NewRoleAfterReplacement = plan.ReaderProbe.NewRoleAfterReplacement
				reader.LifecycleAttemptsWhileHeld, reader.OldRootProtectedWhileHeld = 1, 1
				reader.HeldLifecycleScanned = ptr(uint64(0))
				reader.HeldLifecycleOutcome = "exact_drained"
				reader.LifecycleAttemptsAfterRelease, reader.OldRootProtectedAfterRelease = 1, 1
				reader.PostReleaseLifecycleScanned = ptr(uint64(0))
				reader.PostReleaseLifecycleOutcome = "exact_drained"
				reader.PostReleaseOldRecords = plan.ReaderProbe.ExpectedRecords
				reader.PostReleaseOldProjectionSHA256 = plan.ReaderProbe.OldProjectionSHA256
				reader.PostReleaseOldOutcome = plan.ReaderProbe.PostReleaseOutcome
				reader.OldReaderHeldThroughReprobe = true
				reader.HeldLifecycleEventOrdinal = start + 3
				reader.PostReleaseLifecycleOrdinal = start + 7
				reader.PostReleaseOldQueryOrdinal = start + 8
			} else {
				measurement.Metrics.LifecycleDeleted = 1
				measurement.Metrics.MaxLifecycleDeletesTurn = 1
				reader.Schema = plan.ReceiptContract.TransitionSchema + "/reader-v1"
				reader.PostDeleteOldGenerationOutcome = plan.ReaderProbe.PostDeleteOutcome
				reader.RetirementAttemptsWhileHeld, reader.ProtectedWhileHeld = 1, 1
				reader.RetirementAttemptsAfterRelease, reader.DeletedAfterRelease = 1, 1
				reader.HeldRetirementEventOrdinal = start + 3
				reader.PostReleaseRetirementOrdinal = start + 7
				reader.DeleteEventOrdinal = start + 8
				reader.PostDeleteProbeEventOrdinal = start + 9
			}
			transition.Reader = reader
		case "logical_delta_b", "return_a", "stale_lease", "process_restart":
			for _, point := range failurePointsForPhase(plan.FailurePoints, phase) {
				transition.Injections = append(transition.Injections,
					testInjectionTransition(t, plan, point, transition, authority, freeze, measurement))
			}
			if phase == "logical_delta_b" && plan.Schema == PlanV2Schema {
				readBound, err := correctedLogicalTransitionReadBound()
				if err != nil {
					t.Fatal(err)
				}
				transition.ReadAccounting = &TransitionReadSubtotal{
					Schema: "t422-transition-read-accounting-v1", Class: correctedLogicalTransitionReadClass,
					ReportCalls: readBound.Calls.Minimum, ControlFileReads: readBound.ControlFileReads.Minimum,
					StoreReadAttempts: readBound.StoreReadAttempts.Minimum, MemberReads: readBound.MemberReads.Minimum,
					StoreWriteAttempts: readBound.StoreWriteAttempts.Minimum,
				}
			}
			if phase == "return_a" && plan.Schema == PlanV2Schema {
				readBound, err := correctedReturnTransitionReadBound()
				if err != nil {
					t.Fatal(err)
				}
				transition.ReadAccounting = &TransitionReadSubtotal{
					Schema: "t422-transition-read-accounting-v1", Class: correctedReturnTransitionReadClass,
					ReportCalls: readBound.Calls.Minimum, ControlFileReads: readBound.ControlFileReads.Minimum,
					StoreReadAttempts: readBound.StoreReadAttempts.Minimum, MemberReads: readBound.MemberReads.Minimum,
					StoreWriteAttempts: readBound.StoreWriteAttempts.Minimum,
				}
			}
			if phase == "stale_lease" && plan.Schema == PlanV2Schema {
				readBound, err := correctedStaleLeaseTransitionReadBound()
				if err != nil {
					t.Fatal(err)
				}
				transition.ReadAccounting = &TransitionReadSubtotal{
					Schema: "t422-transition-read-accounting-v1", Class: correctedStaleLeaseTransitionReadClass,
					ReportCalls: readBound.Calls.Minimum, ControlFileReads: readBound.ControlFileReads.Minimum,
					StoreReadAttempts: readBound.StoreReadAttempts.Minimum, MemberReads: readBound.MemberReads.Minimum,
					StoreWriteAttempts: readBound.StoreWriteAttempts.Minimum,
				}
			}
			if phase == "process_restart" && plan.Schema == PlanV2Schema {
				readBound, err := correctedCheckpointRestartReadBound()
				if err != nil {
					t.Fatal(err)
				}
				transition.ReadAccounting = &TransitionReadSubtotal{
					Schema: "t422-transition-read-accounting-v1", Class: correctedCheckpointRestartReadClass,
					ReportCalls: readBound.Calls.Minimum, ControlFileReads: readBound.ControlFileReads.Minimum,
					StoreReadAttempts: readBound.StoreReadAttempts.Minimum, MemberReads: readBound.MemberReads.Minimum,
					StoreWriteAttempts: readBound.StoreWriteAttempts.Minimum,
				}
			}
		case "pressure_80", "pressure_90", "pressure_75":
			// Filled as one contiguous sequence below.
		case "archive_restore":
			transition.Archive = testArchiveTransition(t, plan, transition, authority)
		case "lifecycle_collection":
			transition.Lifecycle = testLifecycleTransition(t, plan, transition, authority, measurement)
		}
		result[index] = transition
	}
	testPressureTransitions(t, plan, freeze, authority, measurements, result)
	return result
}

func testInjectionTransition(
	t *testing.T,
	plan Plan,
	point FailurePoint,
	transition TransitionResult,
	authority map[string]AuthorityPhaseResult,
	freeze ExecutionFreeze,
	measurement *PhaseMeasurement,
) InjectionTransition {
	t.Helper()
	phaseAuthority := authority[point.Phase]
	beforeAuthority := phaseAuthority
	switch point.Name {
	case "partial_service_activation":
		beforeAuthority = authority["physical_delta_b"]
	case "interrupted_publication":
		beforeAuthority = authority["logical_delta_b"]
	case "stale_partition_lease":
		beforeAuthority = authority["return_a"]
	case "checkpointed_hard_restart":
		beforeAuthority = authority["stale_lease"]
	}
	beforeSHA256, _ := authorityIdentitySHA256(beforeAuthority)
	afterSHA256, _ := authorityIdentitySHA256(phaseAuthority)
	target := InjectionTargetProjection{
		Schema: plan.ReceiptContract.TransitionSchema + "/injection-target-v2", Phase: point.Phase, Domain: point.TargetDomain,
		Kind: point.TargetKind, Ordinal: point.TargetOrdinal, ServiceOrdinal: point.TargetServiceOrdinal,
		ServiceKeySHA256: point.TargetServiceKeySHA256, CallerPrefix: point.TargetCallerPrefix,
		SourceStart: point.TargetSourceStart, SourceEnd: point.TargetSourceEnd,
		MemberOrdinal: point.TargetMemberOrdinal, MemberRecordStart: point.TargetMemberStart,
		MemberRecordEnd: point.TargetMemberEnd, AuthoritySHA256: afterSHA256,
	}
	value := InjectionTransition{
		Schema: plan.ReceiptContract.TransitionSchema + "/injection-v2", FailurePoint: point.Name,
		ArmCount: 1, HitCount: 1, RecoveryCount: 1, ResidueAtHit: 1,
		Target: target, ArmEventOrdinal: transition.StartEventOrdinal + 10,
		HitEventOrdinal:       transition.StartEventOrdinal + 20,
		RecoveryEventOrdinal:  transition.StartEventOrdinal + 70,
		ClearEventOrdinal:     transition.StartEventOrdinal + 80,
		AuthorityBeforeSHA256: beforeSHA256, AuthorityAtHitSHA256: beforeSHA256,
		AuthorityAfterSHA256: afterSHA256, RecoveredCandidates: 1,
		ElapsedMS: 1, DeadlineMS: point.RecoveryDeadlineMS,
	}
	switch point.Name {
	case "partial_service_activation":
		value.Target.GenerationSHA256 = phaseAuthority.CatalogRootSHA256
		value.Target.PlanSHA256 = phaseAuthority.CatalogActivationPlanSHA256
		value.Target.ScheduleSHA256 = phaseAuthority.CatalogActivationScheduleSHA256
		value.Target.UnitSHA256 = phaseAuthority.CatalogActivationUnitSHA256
		value.TargetGenerationBefore = beforeAuthority.CatalogRootSHA256
		value.TargetGenerationAfter = phaseAuthority.CatalogRootSHA256
		value.ObservedRecoveryBranch = "resume_activation_schedule"
		if plan.Schema == PlanV2Schema {
			value.RequeueCount = 1
		}
		value.SuccessCount = 1
	case "interrupted_publication":
		value.Target.GenerationSHA256 = phaseAuthority.RelationshipGenerationSHA256
		value.Target.UnitSHA256 = phaseAuthority.RelationshipRootSHA256
		if plan.Schema == PlanV2Schema {
			value.Target.PlanSHA256 = testDigest("return_a", "relationship-runtime-target")
			value.Target.ScheduleSHA256 = testDigest("return_a", "relationship-schedule")
			var ok bool
			value.AuthorityAtHitSHA256, ok = interruptedPublicationAuthorityAtHitSHA256(
				beforeAuthority, phaseAuthority, plan,
			)
			if !ok {
				t.Fatal("return transition lacks its mixed hit authority")
			}
		} else {
			value.Target.ScheduleSHA256 = phaseAuthority.RelationshipGenerationSHA256
		}
		value.TargetGenerationBefore = beforeAuthority.RelationshipGenerationSHA256
		value.TargetGenerationAfter = phaseAuthority.RelationshipGenerationSHA256
		value.ObservedRecoveryBranch = "recover_marker_owned"
		value.SuccessCount = 1
	case "stale_partition_lease", "checkpointed_hard_restart":
		rootIndex := slices.IndexFunc(phaseAuthority.ExtractionRoots, func(root ExtractionRootResult) bool {
			return root.Domain == point.TargetDomain
		})
		root := phaseAuthority.ExtractionRoots[rootIndex]
		partition := root.PartitionResults[point.TargetOrdinal]
		value.Target.GenerationSHA256 = root.GenerationSHA256
		value.Target.PlanSHA256 = root.PlanSHA256
		value.Target.ScheduleSHA256 = root.ScheduleSHA256
		value.Target.UnitSHA256 = partition.ResultIdentitySHA256
		value.TargetGenerationBefore, value.TargetGenerationAfter = root.GenerationSHA256, root.GenerationSHA256
		value.RequeueCount, value.SuccessCount = 1, 1
		if plan.Schema == PlanV2Schema {
			recovery := productionRecoveryScheduleFixture(t, plan, point.Phase)
			preparationIndex := slices.IndexFunc(plan.Correction.RecoveryPreparations, func(row RecoveryPreparation) bool { return row.Phase == point.Phase })
			preparation := plan.Correction.RecoveryPreparations[preparationIndex]
			if recovery.Target != root.GenerationSHA256 {
				t.Fatal("native recovery schedule targets a different immutable generation")
			}
			files, queries, err := recoveryPreparationReadBounds(plan, preparation)
			if err != nil {
				t.Fatal(err)
			}
			value.Preparation = &RecoveryPreparationResult{
				Schema: "t422-native-recovery-preparation-v1", Phase: point.Phase,
				PrepareEventOrdinal: transition.StartEventOrdinal + 5,
				AuthoritySHA256:     beforeSHA256, PreservedRootsSHA256: phaseAuthority.ExtractionRootsSHA256,
				TargetGenerationSHA256: recovery.Target, PriorScheduleSHA256: recovery.Prior,
				RecoveryGenerationSHA256: recovery.RecoveryGeneration, RecoveryScheduleSHA256: recovery.RecoverySchedule,
				ScheduleWrites: preparation.ScheduleWrites, Chunks: preparation.Chunks, Starts: preparation.Starts,
				Successes: preparation.Successes, Requeues: preparation.Requeues,
				PreparationCompletionWrites: preparation.PreparationCompletionWrites, PreparationDeletes: preparation.PreparationDeletes,
				RecoveryCompletionWrites: preparation.RecoveryCompletionWrites, RecoveryRootInstalls: preparation.RecoveryRootInstalls,
				PublicationCalls: preparation.PublicationCalls.Minimum,
				// Read/resource measurements remain a labeled receipt model; the
				// native constructor witness above supplies identities, not a
				// production event ledger or an executable injection protocol.
				ControlFileReads:        files.Minimum,
				StoreReadAttempts:       queries.Minimum,
				StoreWriteAttempts:      1,
				OtherPhaseControlReads:  uint64(measurement.Metrics.ControlReads),
				StoreAuthorityUnchanged: true, PreparedBeforeArm: true, DirectoriesSynced: true, LocksReleasedBeforeWait: true,
			}
			controls := files.Minimum + queries.Minimum
			if uint64(measurement.Metrics.ControlReads) > math.MaxUint64-controls {
				t.Fatal("modeled preparation control total overflows")
			}
			measurement.Metrics.ControlReads += CountMetric(controls)
			value.Target.ScheduleSHA256 = recovery.RecoverySchedule
		}
		if point.Name == "stale_partition_lease" {
			value.ObservedRecoveryBranch = "fence_stale_lease_requeue_then_complete"
		} else {
			value.ObservedRecoveryBranch = "hard_restart_reap_and_reuse_checkpoint"
			value.AuthorityAtHitSHA256 = testDigest("checkpoint", "authority-at-hit")
			value.ProcessEpochBefore, _, _ = expectedPhaseRuntime(freeze.Profile.Epochs, "stale_lease")
			value.ProcessEpochAfter, _, _ = expectedPhaseRuntime(freeze.Profile.Epochs, "process_restart")
			imageIndex := slices.IndexFunc(freeze.Tools, func(tool ExecutionToolIdentity) bool { return tool.Role == "phebs" })
			value.ProcessImageSHA256 = freeze.Tools[imageIndex].SHA256
			value.ProcessIdentityBeforeSHA256 = recipeDigest("t422-phebs-process-identity-v1", value.ProcessImageSHA256, fmt.Sprint(value.ProcessEpochBefore))
			value.ProcessIdentityAfterSHA256 = recipeDigest("t422-phebs-process-identity-v1", value.ProcessImageSHA256, fmt.Sprint(value.ProcessEpochAfter))
			value.ProcessStopEventOrdinal = transition.StartEventOrdinal + 30
			value.ProcessStartEventOrdinal = transition.StartEventOrdinal + 50
			value.Checkpoint = &CheckpointRecovery{
				ResultIdentitySHA256: partition.ResultIdentitySHA256, ResultDigestSHA256: partition.ResultDigestSHA256,
				PlanSHA256: root.PlanSHA256, ExpectationSHA256: partition.ExpectationSHA256,
				PartitionSHA256: partition.PartitionSHA256, CandidateGenerationSHA256: root.CandidateGenerationSHA256,
				SourceGenerationSHA256:      root.SourceGenerationSHA256,
				ObservationGenerationSHA256: phaseAuthority.ObservationGenerationSHA256,
				Domain:                      root.Domain, ExtractorVersion: "v1", ExtractionPolicySHA256: testDigest("checkpoint", "policy"),
				CanonicalResultExistsAtHit: true, ResultDirectorySyncedAtHit: true,
				CompletionAbsentAtHit: true, RootAbsentAtHit: true, SameResultBytesReused: true,
				CompletionExistsAfter: true, RootExistsAfter: true, CurrentAfter: true,
				StartCount: 2, CompletionCount: 1, PriorityAfter: 2, AttemptBefore: 1, AttemptAfter: 1,
				PrivateLeaseTokenChanged: true, HardDeath: true,
			}
			if plan.Schema == PlanV2Schema {
				value.AuthorityAtHitSHA256 = beforeSHA256
				globalOffset := point.TargetOrdinal
				for index := 0; index < rootIndex; index++ {
					globalOffset += uint64(len(phaseAuthority.ExtractionRoots[index].PartitionResults))
				}
				chunkIdentity, err := store.GenerationChunkIdentity(
					value.Target.ScheduleSHA256, int64(globalOffset), 0,
				)
				if err != nil {
					t.Fatal(err)
				}
				value.Checkpoint.ChunkIdentitySHA256 = chunkIdentity
				value.Checkpoint.ScheduleStatusAtHit = store.GenerationScheduleActive
				value.Checkpoint.ChunkStatusAtHit = store.GenerationChunkRunning
				value.Checkpoint.LeasedAtHit = true
				value.Checkpoint.CompletionAbsentAtHit = false
				value.Checkpoint.CompletionFileExistsAtHit = true
				value.Checkpoint.CompletionBitClearAtHit = true
				value.Checkpoint.CurrentAbsentAtHit = true
				value.Checkpoint.ScheduleStatusAfter = store.GenerationScheduleSettled
				value.Checkpoint.ChunkStatusAfter = store.GenerationChunkDone
				value.Checkpoint.UnleasedAfter = true
				value.Checkpoint.AttemptBefore = 0
				value.Checkpoint.AttemptAfter = 0
			}
			childIndex := slices.IndexFunc(measurement.ChildProcessRoles, func(value Count) bool { return value.Name == "phebs" })
			if childIndex >= 0 {
				measurement.Metrics.ChildProcesses -= CountMetric(measurement.ChildProcessRoles[childIndex].Count)
				measurement.ChildProcessRoles[childIndex].Count = 1
				measurement.Metrics.ChildProcesses++
			}
		}
	}
	targetSHA256 := mustReceiptSHA256(t, value.Target)
	selectorSHA256, err := injectionSelectorSHA256(value.Target)
	if err != nil {
		t.Fatal(err)
	}
	value.TargetIdentitySHA256 = targetSHA256
	value.StableTargetSHA256 = recipeDigest(
		"t422-stable-injection-target-v2", selectorSHA256, value.Target.Domain,
		value.Target.GenerationSHA256, value.Target.ScheduleSHA256, value.Target.PlanSHA256, value.Target.UnitSHA256,
	)
	value.TargetSHA256 = recipeDigest(
		"t422-injection-target-binding-v3", point.Phase, point.Name, point.Boundary,
		value.StableTargetSHA256, beforeSHA256, afterSHA256,
	)
	value.HitReportSHA256, err = injectionHitReportSHA256(value, point)
	if err != nil {
		t.Fatal(err)
	}
	value.RecoveryProjectionSHA256, err = injectionRecoveryProjectionSHA256(value, point)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testLifecycleOwners(plan Plan, fence uint64) ([]LifecycleOwnerResult, uint64) {
	owners := make([]LifecycleOwnerResult, len(plan.WorkEnvelope.LifecycleOwners))
	for index, name := range plan.WorkEnvelope.LifecycleOwners {
		completeness := string(lifecycle.Exact)
		if name == lifecycle.JobOwner {
			completeness = string(lifecycle.LowerBound)
		}
		owners[index] = LifecycleOwnerResult{
			Name: name, State: "ok", Completeness: completeness,
			AttemptedAtUnixMS: fence + uint64(index) + 1,
		}
	}
	return owners, fence + uint64(len(owners)) + 1
}

func testLifecycleTransition(
	t *testing.T,
	plan Plan,
	transition TransitionResult,
	authority map[string]AuthorityPhaseResult,
	measurement *PhaseMeasurement,
) *LifecycleTransition {
	t.Helper()
	owners, capacity := testLifecycleOwners(plan, 5_000)
	before, _ := authorityIdentitySHA256(authority["archive_restore"])
	after, _ := authorityIdentitySHA256(authority["lifecycle_collection"])
	ownerTurns := max(uint64(len(owners)), uint64(measurement.Metrics.LifecycleOwnerTurns))
	deleted := uint64(measurement.Metrics.LifecycleDeleted)
	value := &LifecycleTransition{
		Schema:  plan.ReceiptContract.TransitionSchema + "/lifecycle-v1",
		Scanned: deleted, Deleted: deleted, OwnerTurns: ownerTurns,
		LifecycleFenceUnixMS: 5_000, CapacityObservedUnixMS: capacity,
		LifecycleFenceEventOrdinal:   transition.StartEventOrdinal + 10,
		CapacityObservedEventOrdinal: transition.StartEventOrdinal + 20,
		AuthorityBeforeSHA256:        before, AuthorityAfterSHA256: after, Owners: owners,
	}
	value.CycleSHA256, _ = lifecycleCycleSHA256(*value)
	measurement.Metrics.LifecycleOwnerTurns = CountMetric(value.OwnerTurns)
	measurement.Metrics.MaxLifecycleDeletesTurn = CountMetric(divCeil(deleted, ownerTurns))
	return value
}

func testPressureTransitions(
	t *testing.T,
	plan Plan,
	freeze ExecutionFreeze,
	authority map[string]AuthorityPhaseResult,
	measurements []PhaseMeasurement,
	transitions []TransitionResult,
) {
	t.Helper()
	priorSequence := SHA256([]byte("t422-pressure-sequence-start-v1"))
	baseUsed, baseAllocated := freeze.Pressure.MinimumPrePressureUsedBytes, freeze.Pressure.MinimumPrePressureBytes
	priorUsed, priorBallast, priorAllocated := baseUsed, uint64(0), baseAllocated
	for index, phase := range []string{"pressure_80", "pressure_90", "pressure_75"} {
		transitionIndex := slices.IndexFunc(transitions, func(value TransitionResult) bool { return value.Phase == phase })
		measurementIndex := slices.IndexFunc(measurements, func(value PhaseMeasurement) bool { return value.Phase == phase })
		transition, measurement := &transitions[transitionIndex], &measurements[measurementIndex]
		target := freeze.Pressure.Targets[index]
		usedAfter := target.TargetUsedBytes
		ballastAfter := usedAfter - baseUsed
		allocatedAfter := baseAllocated + ballastAfter
		authoritySHA256, _ := authorityIdentitySHA256(authority[phase])
		epoch, _, ok := expectedPhaseRuntime(freeze.Profile.Epochs, phase)
		if !ok {
			t.Fatalf("pressure phase %q has no admitted runtime epoch", phase)
		}
		value := &PressureTransition{
			Schema:            plan.ReceiptContract.TransitionSchema + "/pressure-v1",
			TargetUsedPercent: target.TargetUsedPercent, Action: target.Action,
			ExpectedDisposition: target.ExpectedDisposition, ObservedDisposition: target.ExpectedDisposition,
			PriorGateSequenceSHA256: priorSequence, ServerEpoch: epoch,
			VolumeAvailableBytesBefore: freeze.Pressure.PressureVolumeBytes - priorUsed,
			VolumeAvailableBytesAfter:  freeze.Pressure.PressureVolumeBytes - usedAfter,
			VolumeUsedBytesBefore:      priorUsed, VolumeUsedBytesAfter: usedAfter,
			ObservedUsedPercent:         target.TargetUsedPercent,
			BallastAllocatedBytesBefore: priorBallast, BallastAllocatedBytesAfter: ballastAfter,
			DataAllocatedBytesBefore: priorAllocated, DataAllocatedBytesAtTarget: allocatedAfter,
			BallastMutationEventOrdinal: transition.StartEventOrdinal + 30,
			GateEventOrdinal:            transition.StartEventOrdinal + 40,
			DataVolumeIdentity:          freeze.Host.DataVolumeIdentity, BallastVolumeIdentity: freeze.Host.BallastVolumeIdentity,
			AuthorityBeforeSHA256: authoritySHA256, AuthorityAfterSHA256: authoritySHA256,
		}
		measurement.Metrics.DataLogicalBytes = 1
		switch phase {
		case "pressure_80":
			value.GateOutcome = "success"
			if plan.Schema == PlanV2Schema {
				readBound := correctedPressure80TransitionReadBound()
				transition.ReadAccounting = &TransitionReadSubtotal{
					Schema: "t422-transition-read-accounting-v1", Class: correctedPressure80TransitionReadClass,
					ReportCalls: readBound.Calls.Minimum, ControlFileReads: readBound.ControlFileReads.Minimum,
					StoreReadAttempts: readBound.StoreReadAttempts.Minimum, MemberReads: readBound.MemberReads.Minimum,
					StoreWriteAttempts: readBound.StoreWriteAttempts.Minimum,
				}
			}
			value.PrePressureAllocatedBytes = baseAllocated
			owners, capacity := testLifecycleOwners(plan, 1_000)
			deleted := uint64(measurement.Metrics.LifecycleDeleted)
			ownerTurns := max(uint64(len(owners)), uint64(measurement.Metrics.LifecycleOwnerTurns))
			value.LifecycleFenceUnixMS, value.CapacityObservedUnixMS = 1_000, capacity
			value.LifecycleFenceEventOrdinal = transition.StartEventOrdinal + 10
			value.CapacityObservedEventOrdinal = transition.StartEventOrdinal + 20
			value.LifecycleScanned, value.LifecycleDeleted = deleted, deleted
			value.PrePressureDeletedUnits = deleted
			value.LifecycleOwnerTurns, value.Owners = ownerTurns, owners
			value.LifecycleCycleSHA256, _ = pressureLifecycleCycleSHA256(*value)
			measurement.Metrics.DataAllocatedBytes = Bytes(allocatedAfter)
			measurement.Metrics.LifecycleOwnerTurns = CountMetric(value.LifecycleOwnerTurns)
			measurement.Metrics.MaxLifecycleDeletesTurn = CountMetric(divCeil(deleted, ownerTurns))
		case "pressure_90":
			value.GateOutcome = "err_pressure_refusal"
			measurement.Metrics.DataAllocatedBytes = Bytes(allocatedAfter)
		case "pressure_75":
			value.GateOutcome = "err_pressure_refusal"
			value.RecoveryUsedBytes = baseUsed
			value.RecoveryAvailableBytes = freeze.Pressure.PressureVolumeBytes - baseUsed
			value.RecoveryUsedPercent = usedPercentCeiling(baseUsed, freeze.Pressure.PressureVolumeBytes)
			value.RecoveryDataAllocatedBytes = baseAllocated
			value.RecoveryDisposition = freeze.Pressure.Recovery.ExpectedDisposition
			value.RecoveryGateOutcome = "success"
			value.RecoveryBallastEventOrdinal = transition.StartEventOrdinal + 50
			owners, capacity := testLifecycleOwners(plan, 3_000)
			deleted := uint64(measurement.Metrics.LifecycleDeleted)
			ownerTurns := max(uint64(len(owners)), uint64(measurement.Metrics.LifecycleOwnerTurns))
			value.LifecycleFenceUnixMS, value.CapacityObservedUnixMS = 3_000, capacity
			value.LifecycleFenceEventOrdinal = transition.StartEventOrdinal + 60
			value.CapacityObservedEventOrdinal = transition.StartEventOrdinal + 70
			value.RecoveryGateEventOrdinal = transition.StartEventOrdinal + 80
			value.LifecycleScanned, value.LifecycleDeleted = deleted, deleted
			value.LifecycleOwnerTurns, value.Owners = ownerTurns, owners
			value.LifecycleCycleSHA256, _ = pressureLifecycleCycleSHA256(*value)
			measurement.Metrics.DataAllocatedBytes = Bytes(priorAllocated)
			measurement.Metrics.LifecycleOwnerTurns = CountMetric(value.LifecycleOwnerTurns)
			measurement.Metrics.MaxLifecycleDeletesTurn = CountMetric(divCeil(deleted, ownerTurns))
		}
		value.GateSequenceSHA256, _ = pressureSequenceSHA256(phase, *value)
		transition.Pressure = value
		priorSequence, priorUsed, priorBallast, priorAllocated = value.GateSequenceSHA256, usedAfter, ballastAfter, allocatedAfter
	}
}

func testArchiveTransition(
	t *testing.T,
	plan Plan,
	transition TransitionResult,
	authority map[string]AuthorityPhaseResult,
) *ArchiveTransition {
	t.Helper()
	components := []ArchiveComponent{
		{Name: recovery.DatabaseName, Classification: "precious", MediaType: "application/surrealql", Bytes: 1, SHA256: testDigest("archive", recovery.DatabaseName)},
		{Name: recovery.FocusedIndexName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Bytes: 1, SHA256: testDigest("archive", recovery.FocusedIndexName)},
		{Name: recovery.ResolverCatalogName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Bytes: 1, SHA256: testDigest("archive", recovery.ResolverCatalogName)},
		{Name: recovery.CallerPublicationName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Bytes: 1, SHA256: testDigest("archive", recovery.CallerPublicationName)},
		{Name: recovery.ObservationPublicationName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Bytes: 1, SHA256: testDigest("archive", recovery.ObservationPublicationName)},
		{Name: recovery.RelationshipPublicationName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Bytes: 1, SHA256: testDigest("archive", recovery.RelationshipPublicationName)},
	}
	reports := []ArchiveReportProjection{
		{Name: "focused_index", Schema: recovery.FocusedIndexArchiveReportSchema, Publications: 1},
		{Name: "resolver_catalog", Schema: recovery.ResolverCatalogArchiveReportSchema, Publications: 1},
		{Name: "caller_publication", Schema: recovery.CallerPublicationArchiveReportSchema, Publications: 1},
		{Name: "observation", Schema: recovery.ObservationArchiveReportSchema, Publications: 1, V2Publications: 1, Files: 1, Bytes: 1},
		{Name: "relationship", Schema: recovery.RelationshipArchiveReportSchema, Publications: 1, Files: 1, Bytes: 1},
	}
	manifestInventory, err := archiveManifestInventory(components)
	if err != nil {
		t.Fatal(err)
	}
	reportInventory, err := archiveReportInventory(reports)
	if err != nil {
		t.Fatal(err)
	}
	stateInventory := SetIdentity{Records: 1, FramedBytes: 1, SHA256: testDigest("archive", "state-inventory")}
	before, after := authority["pressure_75"], authority["archive_restore"]
	stateSHA256, _ := expectedArchiveSemanticStateSHA256(plan)
	relationshipSHA256, _ := expectedRelationshipSemanticSHA256(plan)
	value := &ArchiveTransition{
		Schema:        plan.ReceiptContract.TransitionSchema + "/archive-v1",
		ArchiveSHA256: testDigest("archive", "bytes"), ArchiveBytes: 6,
		ManifestSchema: recovery.ManifestSchema, ManifestSHA256: testDigest("archive", "manifest"),
		InventoryCanonicalization: archiveInventoryCanonicalization,
		ManifestInventory:         manifestInventory, ReportInventory: reportInventory,
		StateInventoryBefore: stateInventory, StateInventoryArchived: stateInventory, StateInventoryAfter: stateInventory,
		Components: components, Reports: reports, PreRestoreStateSHA256: stateSHA256, RestoredStateSHA256: stateSHA256,
		RelationshipSemanticSHA256:   relationshipSHA256,
		RelationshipGenerationBefore: before.RelationshipGenerationSHA256,
		RelationshipGenerationAfter:  after.RelationshipGenerationSHA256,
		RelationshipRootBefore:       before.RelationshipRootSHA256, RelationshipRootAfter: after.RelationshipRootSHA256,
		RelationshipRuntimeIdentityDisposition: relationshipRuntimeIdentityDisposition(before, after),
		AuthoritySnapshotBeforeSHA256:          mustReceiptSHA256(t, before.AuthorityState),
		AuthoritySnapshotAfterSHA256:           mustReceiptSHA256(t, after.AuthorityState),
		ArchiveCreatedEventOrdinal:             transition.StartEventOrdinal + 10,
		InstallationDestroyedEventOrdinal:      transition.StartEventOrdinal + 20,
		EmptyRestoreTargetEventOrdinal:         transition.StartEventOrdinal + 30,
		RestoreStartedEventOrdinal:             transition.StartEventOrdinal + 40,
		ComparisonEventOrdinal:                 transition.StartEventOrdinal + 60,
	}
	value.ArchiveBindingSHA256, _ = archiveBindingSHA256(*value)
	return value
}

func testCallerPublication(
	t *testing.T,
	plan Plan,
	authority AuthorityPhaseResult,
) CallerPublicationResult {
	t.Helper()
	expected := plan.Oracle.ProductRelationships
	leaves := make([]CallerPublicationLeafResult, len(expected.CallerLeaves))
	for index, leaf := range expected.CallerLeaves {
		leaves[index] = CallerPublicationLeafResult{
			Prefix: leaf.Prefix, CandidateRecords: leaf.CandidateRecords, Outcome: "success",
			ResolvedPostings: leaf.ResolvedPostings, Abstentions: leaf.Abstentions, Records: leaf.Records,
			CanonicalBytes: leaf.CanonicalBytes, EncodedBytes: leaf.EncodedBytes,
			ResultSHA256: testDigest("caller", leaf.Prefix),
		}
	}
	rootIndex := slices.IndexFunc(authority.ExtractionRoots, func(root ExtractionRootResult) bool { return root.Domain == "grpc-caller" })
	value := CallerPublicationResult{
		Schema: "t422-global-caller-publication-v1", ExecutionPolicy: expected.GlobalCallerPolicy,
		CandidateInventory:              authority.ExtractionRoots[rootIndex].Candidates,
		ResolverCatalogGenerationSHA256: authority.ResolverCatalogGenerationSHA256,
		ResolverCatalogRootSHA256:       authority.ResolverCatalogRootSHA256,
		ResolverDeclarationRecords:      10_100, GeneratedDescriptors: 10_100,
		GenerationSHA256: authority.CallerGenerationSHA256, RootSHA256: authority.CallerRootSHA256,
		ManifestSHA256: testDigest("caller", "manifest"), Current: true, Leaves: leaves,
		ResolvedPostings: expected.RPCProjections, Abstentions: 11_603, Records: 22_602,
		CanonicalBytes: 21_656_043, EncodedBytes: 21_656_043,
		Projection:                   expected.ExpectedRPCProjections,
		RelationshipGenerationSHA256: authority.RelationshipGenerationSHA256,
		RelationshipRootSHA256:       authority.RelationshipRootSHA256,
	}
	value.LeavesSHA256 = mustReceiptSHA256(t, leaves)
	value.ComponentBindingSHA256 = mustReceiptSHA256(t, value)
	return value
}

func failureObservation(
	t *testing.T,
	plan Plan,
	kind, metric string,
	limit, observed uint64,
) FailureObservation {
	t.Helper()
	value := FailureObservation{
		Schema: plan.ReceiptContract.FailureObservationSchema, Kind: kind,
		Metric: metric, Limit: limit, Observed: observed,
	}
	value.EvidenceSHA256 = mustReceiptSHA256(t, value)
	return value
}

func stopTestReceipt(
	t *testing.T,
	receipt *Receipt,
	plan Plan,
	phase string,
	failure ReceiptFailure,
) {
	t.Helper()
	stopIndex := slices.Index(plan.PhaseOrder, phase)
	if stopIndex < 0 {
		t.Fatalf("unknown stop phase %q", phase)
	}
	for index := stopIndex; index < len(plan.PhaseOrder)-1; index++ {
		outcome := "not_run"
		var phaseFailure *ReceiptFailure
		if index == stopIndex {
			outcome, phaseFailure = "stopped", ptr(failure)
		}
		receipt.PhaseResults[index].Outcome = outcome
		receipt.PhaseResults[index].Failure = phaseFailure
		if index > stopIndex {
			receipt.Measurements[index] = PhaseMeasurement{Phase: plan.PhaseOrder[index]}
		}
	}
	for index := range receipt.StateResults {
		phaseIndex := slices.Index(plan.PhaseOrder, receipt.StateResults[index].Phase)
		if phaseIndex < stopIndex {
			continue
		}
		receipt.StateResults[index].Outcome = "not_run"
		if phaseIndex == stopIndex {
			receipt.StateResults[index].Outcome = "stopped"
		}
		receipt.StateResults[index].ObservedProjectionSHA256 = ""
		receipt.StateResults[index].ObservedSHA256 = ""
		receipt.StateResults[index].Observed = nil
		if phaseIndex > stopIndex {
			receipt.StateResults[index].RuntimeSHA256 = ""
			receipt.StateResults[index].Runtime = nil
		}
	}
	for index := range receipt.TransitionResults {
		phaseIndex := slices.Index(plan.PhaseOrder, receipt.TransitionResults[index].Phase)
		if phaseIndex < stopIndex {
			continue
		}
		value := &receipt.TransitionResults[index]
		value.Outcome = "not_run"
		if phaseIndex == stopIndex {
			value.Outcome = "stopped"
		} else {
			value.StartEventOrdinal, value.FinishEventOrdinal = 0, 0
		}
		value.FailureProjection, value.Injections, value.Pressure = nil, nil, nil
		value.Archive, value.Reader, value.Lifecycle, value.ReadAccounting = nil, nil, nil, nil
	}
	usedSnapshots := make(map[string]struct{})
	for index := range receipt.Authority.Results {
		phaseIndex := slices.Index(plan.PhaseOrder, receipt.Authority.Results[index].Phase)
		if phaseIndex > stopIndex {
			receipt.Authority.Results[index].Outcome = "not_run"
			receipt.Authority.Results[index].SnapshotSHA256 = ""
		} else {
			if phaseIndex == stopIndex {
				receipt.Authority.Results[index].Outcome = "stopped"
			}
			usedSnapshots[receipt.Authority.Results[index].SnapshotSHA256] = struct{}{}
		}
	}
	receipt.Authority.Snapshots = slices.DeleteFunc(receipt.Authority.Snapshots, func(value AuthoritySnapshot) bool {
		_, ok := usedSnapshots[value.SHA256]
		return !ok
	})
	if stopIndex <= slices.Index(plan.PhaseOrder, "product_queries") {
		receipt.QueryResults = QueryEvidence{Phase: "product_queries", Outcome: "not_run"}
		receipt.RelationshipResults = RelationshipEvidence{Phase: "product_queries", Outcome: "not_run"}
		if phase == "product_queries" {
			receipt.QueryResults.Outcome, receipt.RelationshipResults.Outcome = "stopped", "stopped"
		}
	}
	receipt.Decision = ReceiptDecision{
		Outcome: "stopped", Selected: "reduce", RulePriority: 4, Reason: failure.Code, Substantiated: true,
		Gate2V2: plan.Claims.Gate2V2, ReleasePosture: plan.Claims.ReleasePosture,
	}
}

func teardownFailure(t *testing.T, plan Plan, check string) *TeardownFailure {
	t.Helper()
	value := TeardownFailure{
		Schema: plan.ReceiptContract.TeardownFailureSchema, Kind: check, FailedChecks: []string{check},
	}
	value.EvidenceSHA256 = mustReceiptSHA256(t, value)
	return &value
}
