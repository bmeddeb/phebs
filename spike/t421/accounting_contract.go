package t421

import (
	"errors"
	"slices"
	"strings"
)

const (
	MaxPlanV3AuthorBytes        = 192 << 10
	ProcessAccountingSchema     = "t422-controlled-dispatch-accounting-v1"
	WorkEnvelopeV3Schema        = "t422-phase-work-envelope-v3"
	ReceiptV3Schema             = "t422-combined-convergence-receipt-v3"
	ExecutionFreezeV3Schema     = "t422-combined-execution-freeze-v3"
	ExecutionProfileV3Schema    = "t422-production-execution-profile-v3"
	PhaseRuntimeBindingV3Schema = "t422-phase-runtime-binding-v3"
	retainedPlanV2SHA256        = "sha256:2275b8cadca8f4e76a46db6d943380d1533a41da70a71c7009850e2c0229b422"
)

// ProcessAccountingContract distinguishes admitted dispatch permissions from
// sampled native records. Neither quantity is a complete process-birth ledger.
type ProcessAccountingContract struct {
	Schema                        string                `json:"schema"`
	SupersedesSHA256              string                `json:"supersedes_sha256"`
	AttemptUnit                   string                `json:"attempt_unit"`
	AdmissionPolicy               string                `json:"admission_policy"`
	PhaseFencePolicy              string                `json:"phase_fence_policy"`
	NativeMeasurementKind         string                `json:"native_measurement_kind"`
	NativeHistory                 string                `json:"native_history"`
	NativePolicy                  string                `json:"native_policy"`
	ToolIdentityPolicy            string                `json:"tool_identity_policy"`
	TeardownPolicy                string                `json:"teardown_policy"`
	FinalSigningPolicy            string                `json:"final_signing_policy"`
	ProductionSiteInventorySHA256 string                `json:"production_site_inventory_sha256"`
	DispatchBudgets               []PhaseDispatchBudget `json:"dispatch_budgets"`
}

// BuildPlanV3 constructs an unsealed prospective plan. The retained BuildPlan
// and Author entry points continue to build V2; constructing V3 is not admission
// of an executor or authorization to seal/run one.
func BuildPlanV3(sourceCommit string) (Plan, error) {
	plan, err := BuildPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	if err := applyProcessAccountingCorrection(&plan); err != nil {
		return Plan{}, err
	}
	if err := validatePlan(plan, &plan.Revisions); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func knownPlanSchema(schema string) bool {
	switch schema {
	case PlanSchema, PlanV2Schema, PlanV3Schema:
		return true
	default:
		return false
	}
}

// Callers validate the closed plan schema before interpreting its semantics.
// V3 inherits V2's functional authority, never the superseded V1 behavior.
func correctedPlanSemantics(schema string) bool {
	return schema == PlanV2Schema || schema == PlanV3Schema
}

func applyProcessAccountingCorrection(plan *Plan) error {
	if plan.Correction == nil {
		return errors.New("V3 requires the complete V2 functional correction")
	}
	budgets, err := controlledDispatchBudgets(plan.Profile)
	if err != nil {
		return err
	}
	sitesSHA256, err := canonicalSHA256(productionDispatchSites())
	if err != nil {
		return err
	}
	if len(budgets) != len(plan.WorkEnvelope.Phases) {
		return errors.New("V3 dispatch budget phase inventory differs")
	}
	plan.Schema = PlanV3Schema
	if err := applyCorrectedPhaseReadMaximums(&plan.WorkEnvelope, *plan); err != nil {
		return err
	}
	plan.Correction.ReadAccountingPolicy = strings.Replace(plan.Correction.ReadAccountingPolicy,
		"prep-S=D+10+sum(A1..A4),Ai=[1,64]", "prep-S=D+10+sum(A1..A5),Ai=[1,64]", 1)
	// Epoch three must already run the return-A configuration to produce its
	// marker. Epoch four belongs to the later hard-death proof, so V3 recovers
	// this marker in the same scheduler attempt after publication unwinds.
	for index := range plan.FailurePoints {
		point := &plan.FailurePoints[index]
		if point.Name == "interrupted_publication" {
			point.Trigger = "relationship_publication_same_attempt_exact_control_v3"
			point.RecoveryAction = "unwind_publication_then_exclusive_exact_marker_recovery_then_advance_same_attempt"
		}
	}
	plan.WorkEnvelope.Schema = WorkEnvelopeV3Schema
	plan.WorkEnvelope.ChildProcessRoles = nil
	plan.WorkEnvelope.MaximumChildProcessesPerPhase = 0
	for index, budget := range budgets {
		phase := &plan.WorkEnvelope.Phases[index]
		if phase.Phase != budget.Phase {
			return errors.New("V3 dispatch budget phase order differs")
		}
		phase.ChildProcessRoles = nil
		phase.ControlledDispatchRoles = slices.Clone(budget.Roles)
		plan.WorkEnvelope.MaximumControlledDispatchAttemptsPerPhase = max(
			plan.WorkEnvelope.MaximumControlledDispatchAttemptsPerPhase, budget.MaximumAttempts,
		)
	}
	plan.WorkEnvelope.ControlledDispatchRoles = nil
	for _, role := range budgets[0].Roles {
		plan.WorkEnvelope.ControlledDispatchRoles = append(plan.WorkEnvelope.ControlledDispatchRoles, role.Name)
	}
	plan.ProcessAccounting = &ProcessAccountingContract{
		Schema: ProcessAccountingSchema, SupersedesSHA256: retainedPlanV2SHA256,
		AttemptUnit:           "controller-committed-dispatch-admission;not-process-birth-or-successful-start",
		AdmissionPolicy:       "closed-producer-site-role-phase-ordinal;check-image-input-budget-before-start;commit-counters-and-digest-before-ack;lost-ack-still-counts;no-ack-no-start;retry-new-admission;sticky-report-failure;bounded-active-state-no-history",
		PhaseFencePolicy:      "fence-admissions;drain-records;join-or-irrevocably-cancel-every-one-shot-before-checkpoint;no-old-permission-after-checkpoint;only-modeled-started-persistent-handles-span;hard-death-close-controller-committed-prefix-after-owned-wait-and-EOF",
		NativeMeasurementKind: "sampled_observation", NativeHistory: "not_established",
		NativePolicy:                  "bounded-sequential-coherent-rows;observed-counts-and-RSS-sum-high-water-not-simultaneous-peak-bounds;no-cumulative-image-epochs;required-probe-failure-sticky-unavailable;retain-positive-overshoots;zero-only-none-observed;no-T40-lifetime-cap",
		ToolIdentityPolicy:            "twelve-admitted-tool-images;exact-private-direct-dispatch-bindings;observed-name-classification-not-image-hash;trusted-native-tools-not-vendor-attestation-or-complete-helper-history",
		TeardownPolicy:                "private-recorded-execution-sessions;exclude-controller-and-final-signer-roots;fence-operational-producers;close-store-and-join-owned-handles;drain-custody;zero-complete-recorded-session-censuses;nonforced-detach-before-unlink;exact-image-root-removal-under-custody-lock;then-join-cleanup-and-close-meter;no-global-descendant-zero-claim",
		FinalSigningPolicy:            "outside-closed-operational-cleanup-meter;frozen-finite-exact-tool-input-command-recipe;no-automatic-retry;bounded-output-deadline-custody;owned-handle-cleanup;post-sign-status-launcher-local-not-in-signed-input",
		ProductionSiteInventorySHA256: sitesSHA256,
		DispatchBudgets:               budgets,
	}
	plan.Correction.ChildBudgets = nil
	plan.Correction.SourceReadSemantics = "git_reads=native-git-object-content-reads;index_files=zoekt-go-git-input-offers;census_children=ordinary-catalog-ls-tree-invocations;census_records=regular-tree-records-classified;direct-Git-admissions-including-census-and-watcher-count-once;native-helpers-not-completely-observed"
	plan.Correction.NativeGitAdmissionPolicy = "admit-resolved-native-Git-core-image-and-closed-config;direct-path-binding-required;aliases-and-observed-names-do-not-prove-complete-native-helper-history;no-native-helper-exact-counter-or-total-upper-bound"
	plan.Correction.ProcessAccountingPolicy = "controlled-dispatch-accounting-v1;V1-V2-and-historical-T40-exact;V3-process-history-not-established;no-sampling-relabelled-as-exact-execution"
	plan.Correction.RequiredReadiness[1] = "post-logical-restart-authorized-search-caller-and-relationship-reads:zero-resolver-and-caller-materialization;controlled-Git-admissions-include-watcher-census-startup;owned-successful-server-starts-and-native-health-prove-epochs"
	plan.Correction.RequiredReadiness = append(plan.Correction.RequiredReadiness,
		"return-A-same-epoch-same-attempt-marker-hit-unwind-exclusive-exact-target-recovery-and-recovered-R-before-selector-advance;zero-requeue-one-success;not-startup-or-crash-recovery;process_restart-remains-separate-hard-death-proof",
		"source-owned-dispatch-site-inventory-and-checked-operational-admission-preparation-cleanup-signing-budgets",
		"bounded-inherited-transport-bootstrap-for-server-recovery-author-and-controller;settlement-and-phase-checkpoint-loss-refuses",
		"full-V3-completed-and-worst-stopped-constructor-and-byte-cap-replay;retained-V1-V2-byte-exact",
		"real-launcher-hard-death-private-session-lease-busy-detach-path-replacement-and-source-free-failure-rehearsal",
	)
	plan.MeterPolicy.Schema = "t422-combined-meter-policy-v3"
	plan.MeterPolicy.Authority = ProcessAccountingSchema
	plan.MeterPolicy.ProcessGaugeSemantics = "controlled_dispatch_attempts=controller-committed-admissions;observed_rss_high_water_bytes=max_completed_sequential_census_RSS_sum;native_measurement_kind=sampled_observation;native_history=not_established;not-simultaneous-resource-bounds"
	plan.ReceiptContract.Schema = ReceiptV3Schema
	plan.ReceiptContract.FailureObservationSchema = "t422-failure-observation-v3"
	plan.ReceiptContract.TeardownFailureSchema = "t422-teardown-failure-v3"
	plan.ReceiptContract.StateObservationSchema = "t422-observed-phase-state-v6"
	plan.ReceiptContract.RequiredMetrics = accountingReceiptMetricNames()
	plan.MeterPolicy.RequiredMetricsSHA256 = recipeDigest("t422-required-metrics-v3", plan.ReceiptContract.RequiredMetrics...)
	plan.ToolPolicy.ExecutionFreezeSchema = ExecutionFreezeV3Schema
	plan.ToolPolicy.ExecutionProfileSchema = ExecutionProfileV3Schema
	plan.Teardown.StopDescendants, plan.Teardown.RequireZeroChildren = false, false
	plan.Teardown.Scope = "owned-handles-recorded-private-sessions-and-nonforced-detach-v1"
	for index := range plan.StopRules {
		if plan.StopRules[index].Priority == 2 {
			plan.StopRules[index].Trigger += "_with_complete_dispatch_and_store_submission_prefix_and_available_native_measurement;otherwise_preserve_overshoot_and_reduce"
		}
	}
	return nil
}

func accountingReceiptMetricNames() []string {
	names := correctedReceiptMetricNames()
	names = slices.DeleteFunc(names, func(name string) bool {
		return name == "child_processes" || name == "peak_rss_bytes" || name == "process_measurement_available"
	})
	names = append(names, "controlled_dispatch_attempts", "dispatch_measurement_available", "observed_rss_high_water_bytes", "native_measurement_available")
	slices.Sort(names)
	return names
}
