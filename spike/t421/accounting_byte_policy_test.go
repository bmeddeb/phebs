package t421

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestAccountingV3SampledBytePolicy(t *testing.T) {
	plan := accountingTestPlan(t)
	if err := validatePlanExecutionContract(plan); err != nil {
		t.Fatal(err)
	}
	for _, clause := range []string{
		"phase_sampled_traversal_v3:",
		"logical=max_completed_traversal_regular_file_apparent_byte_sum",
		"allocated=max_completed_traversal_linked_entry_st_blocks_times_512_sum",
		"scope=full_data_custody_including_ballast",
		"allocated_includes_directories_and_symlinks",
		"hardlink_aliases=count_each_linked_path",
		"symlinks=not_followed",
		"non_atomic_sequential_observations",
		"not_point_in_time_usage_or_instantaneous_high_water",
		"not_unique_physical_or_APFS_clone_exclusive_usage",
		"sample_phase_start,after_each_custody_mutation,at_each_pressure_or_lifecycle_capacity_checkpoint,and_phase_finish",
		"any_required_sample_unavailable_is_fail_closed",
		"retain_prior_completed_positive_max_on_later_refusal",
		"receipt_records_max_completed_observations_not_endpoint",
	} {
		t.Run(clause, func(t *testing.T) {
			if strings.Count(plan.MeterPolicy.ByteGaugeSemantics, clause) != 1 {
				t.Fatal("missing or duplicated sampled byte policy clause")
			}
			changed := plan
			changed.MeterPolicy.ByteGaugeSemantics = strings.Replace(plan.MeterPolicy.ByteGaugeSemantics, clause, "", 1)
			if err := validatePlanExecutionContract(changed); err == nil {
				t.Fatal("incomplete sampled byte policy admitted")
			}
		})
	}
	plan.MeterPolicy.ByteGaugeSemantics = frozenMeterPolicy().ByteGaugeSemantics
	if err := validatePlanExecutionContract(plan); err == nil {
		t.Fatal("V3 admitted historical coherent high-water claim")
	}
}

func TestAccountingV3SampledBytesPreserveHistoricalPolicyAndBounds(t *testing.T) {
	current := accountingTestPlan(t)
	for _, path := range []string{"plan.json", "plan-v2.json"} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var prior Plan
			if err := json.Unmarshal(raw, &prior); err != nil {
				t.Fatal(err)
			}
			if prior.MeterPolicy.ByteGaugeSemantics != frozenMeterPolicy().ByteGaugeSemantics ||
				!reflect.DeepEqual(prior.SafetyEnvelope, current.SafetyEnvelope) ||
				prior.WorkEnvelope.MaximumDataLogicalBytes != current.WorkEnvelope.MaximumDataLogicalBytes {
				t.Fatal("sampled policy changed historical semantics or numerical safety/byte bounds")
			}
			prior.MeterPolicy.ByteGaugeSemantics = current.MeterPolicy.ByteGaugeSemantics
			if err := validatePlanExecutionContract(prior); err == nil {
				t.Fatal("historical plan admitted V3 sampled semantics")
			}
		})
	}
}
