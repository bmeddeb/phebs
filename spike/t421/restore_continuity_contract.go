package t421

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const callerRestoreContinuityPolicy = "caller-restore-continuity-v1:full-validated-current-manifest;equal-generation-and-normalized-manifest-sha256;normalize-only-manifest-self-digest,upstream-domain-run-ids,upstream-provenance-digest;preserve-all-pairs,receipts,aggregates,and-other-fields;retain-and-authenticate-each-actual-manifest-root;archive-only;no-transport-byte-relaxation"

const handoffLifecycleAccountingPolicy = "handoff-lifecycle-accounting-v1:joined-native-retention-and-selector-cleanup-prefixes;phase-producer-input-bound;accepted-cleanup-HTTP-equals-native-terminal;physical-reader-exact-two-zero-delete-turns;whole-phase-exact-reader-plus-cleanup;cleanup-read-attempts-counted-once;independent-SA-store-counts;stopped-prefix-not-success;no-V1-V4-change"

const v5ArchiveTailReadinessPolicy = "archive-tail-readiness-v5:epoch-five-phase12-only;authenticated-settled-v1-opt-in;current-observation-and-extraction-upstream;current-resolver-and-catalog-state;V3-schedule-stable-absent-or-successfully-settled-across-current-target-read;ready-C7-S21-to-275;historical-T-exact"

const v5SearchWarmPolicy = "phase14-search-warm-v1:V5-epoch-five-opt-in;one-native-parent-command-after-F-before-corridor;shared-all-code-validation-and-selected-exact-reader;product-warming-timeout-bound;selected-generation-equals-F;corridor-queries-and-exact-accounting-unchanged;fail-closed"

const restoredOwnerDrainPolicy = "restored-owner-drain-v1:V5-epoch-five-initial-phase12-only;authenticated-original-phase-deadline;no-deadline-renewal;all-other-control-exchanges-30s;unchanged-PC01-wire-and-pair-count;fail-closed"

// This prospective correction does not modify any retained V1-V4 recipe.
func applyCallerRestoreContinuityCorrection(plan *Plan) error {
	if plan == nil || plan.Schema != PlanV4Schema || plan.Correction == nil ||
		plan.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV4Schema || plan.ReceiptContract.Schema != ReceiptV4Schema {
		return errors.New("V5 caller restore continuity requires complete V4")
	}
	if err := validatePlanExecutionContract(*plan); err != nil {
		return fmt.Errorf("validate V4 caller-continuity preimage: %w", err)
	}
	digest, err := inspectionInventorySHA256(Plan{Schema: PlanV5Schema, Profile: plan.Profile}, planTailReadinessTransitions(PlanV5Schema))
	if err != nil {
		return err
	}
	// The prior plan may share its correction pointer with a retained fixture.
	correction := *plan.Correction
	correction.IdentityDerivations = callerRestoreIdentityDerivations()
	correction.InspectionInventorySHA256 = digest
	const oldTailReads = ";T-C=4;T-S=4;T-M=0;T-W=0;"
	const v5TailReads = ";T-default-C=4;T-default-S=4;T-archive-v5-C=7;T-archive-v5-S=[21,275];T-M=0;T-W=0;"
	if strings.Count(correction.ReadAccountingPolicy, oldTailReads) != 1 {
		return errors.New("V5 archive tail read-accounting preimage changed")
	}
	correction.ReadAccountingPolicy = strings.Replace(correction.ReadAccountingPolicy, oldTailReads, v5TailReads, 1)
	correction.RequiredReadiness = append(slices.Clone(correction.RequiredReadiness), callerRestoreContinuityPolicy, handoffLifecycleAccountingPolicy, v5ArchiveTailReadinessPolicy, restoredOwnerDrainPolicy, v5SearchWarmPolicy)
	plan.Correction = &correction
	plan.Schema = PlanV5Schema
	plan.ToolPolicy.ExecutionFreezeSchema = ExecutionFreezeV5Schema
	plan.ReceiptContract.Schema = ReceiptV5Schema
	plan.WorkEnvelope.Phases = slices.Clone(plan.WorkEnvelope.Phases)
	return applyCorrectedPhaseReadMaximums(&plan.WorkEnvelope, *plan)
}

func planTailReadinessTransitions(schema string) []tailReadinessTransition {
	rows := correctedTailReadinessTransitions()
	if schema == PlanV5Schema {
		for index := range rows {
			if rows[index].Phase == "archive_restore" {
				// T only waits for current native authority. F proves complete
				// manifest continuity before archive acceptance can advance.
				rows[index].Caller = "generation_equal_manifest_continuity_at_final"
			}
		}
	}
	return rows
}

func callerRestoreIdentityDerivations() []IdentityDerivation {
	rows := frozenIdentityDerivations()
	for index := range rows {
		if rows[index].Identity == "caller_root_sha256" {
			rows[index].Inputs = append(rows[index].Inputs, "full upstream execution provenance")
			rows[index].ChangedInputs["archive_restore"] = "fresh extraction RunIDs/provenance only; equal caller_continuity_sha256 required"
		}
	}
	return append(rows, IdentityDerivation{
		Identity: "caller_continuity_sha256", Constructor: "callerpublication.Publication.RestoreContinuitySHA256",
		Inputs:        []string{callerRestoreContinuityPolicy},
		ChangedInputs: map[string]string{"physical_delta_b": "source-bound generation/pair content", "return_a": "source-bound generation/pair content"},
	})
}

func validCallerContinuityObservation(schema, digest string, required bool) bool {
	if schema != PlanV5Schema {
		return digest == ""
	}
	return validDigest(digest) || !required && digest == ""
}
