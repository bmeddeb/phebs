package t421

import (
	"errors"
	"fmt"
	"slices"
)

const callerRestoreContinuityPolicy = "caller-restore-continuity-v1:full-validated-current-manifest;equal-generation-and-normalized-manifest-sha256;normalize-only-manifest-self-digest,upstream-domain-run-ids,upstream-provenance-digest;preserve-all-pairs,receipts,aggregates,and-other-fields;retain-and-authenticate-each-actual-manifest-root;archive-only;no-transport-byte-relaxation"

const handoffLifecycleAccountingPolicy = "handoff-lifecycle-accounting-v1:joined-native-retention-and-selector-cleanup-prefixes;phase-producer-input-bound;accepted-cleanup-HTTP-equals-native-terminal;physical-reader-exact-two-zero-delete-turns;whole-phase-exact-reader-plus-cleanup;cleanup-read-attempts-counted-once;independent-SA-store-counts;stopped-prefix-not-success;no-V1-V4-change"

// This prospective correction does not modify any retained V1-V4 recipe.
func applyCallerRestoreContinuityCorrection(plan *Plan) error {
	if plan == nil || plan.Schema != PlanV4Schema || plan.Correction == nil ||
		plan.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV4Schema || plan.ReceiptContract.Schema != ReceiptV4Schema {
		return errors.New("V5 caller restore continuity requires complete V4")
	}
	if err := validatePlanExecutionContract(*plan); err != nil {
		return fmt.Errorf("validate V4 caller-continuity preimage: %w", err)
	}
	digest, err := inspectionInventorySHA256(plan.Profile, planTailReadinessTransitions(PlanV5Schema))
	if err != nil {
		return err
	}
	// The prior plan may share its correction pointer with a retained fixture.
	correction := *plan.Correction
	correction.IdentityDerivations = callerRestoreIdentityDerivations()
	correction.InspectionInventorySHA256 = digest
	correction.RequiredReadiness = append(slices.Clone(correction.RequiredReadiness), callerRestoreContinuityPolicy, handoffLifecycleAccountingPolicy)
	plan.Correction = &correction
	plan.Schema = PlanV5Schema
	plan.ToolPolicy.ExecutionFreezeSchema = ExecutionFreezeV5Schema
	plan.ReceiptContract.Schema = ReceiptV5Schema
	return nil
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
