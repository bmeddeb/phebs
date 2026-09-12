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
		"non_teardown_samples=phase_start,after_each_custody_mutation,at_each_pressure_or_lifecycle_capacity_checkpoint,and_phase_finish",
		"teardown_clock_starts_before_fence_and_shutdown",
		"teardown_samples=first_joined_writer_free_boundary_and_after_owned_input_release_before_detach",
		"teardown_terminal_absence_requires_nonforced_detach_exact_image_and_root_removal_and_closed_cleanup",
		"terminal_absence_is_not_a_zero_traversal_and_never_erases_prior_max_or_refusal",
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
			if prior.SafetyEnvelope.MaximumDataAllocatedBytes != 96<<30 || current.SafetyEnvelope.MaximumDataAllocatedBytes != 128<<30 {
				t.Fatal("versioned allocated ceiling changed")
			}
			remaining := current.SafetyEnvelope
			remaining.MaximumDataAllocatedBytes = prior.SafetyEnvelope.MaximumDataAllocatedBytes
			if prior.MeterPolicy.ByteGaugeSemantics != frozenMeterPolicy().ByteGaugeSemantics ||
				!reflect.DeepEqual(prior.SafetyEnvelope, remaining) ||
				prior.WorkEnvelope.MaximumDataLogicalBytes != current.WorkEnvelope.MaximumDataLogicalBytes {
				t.Fatal("sampled policy changed historical semantics or another safety/byte bound")
			}
			prior.MeterPolicy.ByteGaugeSemantics = current.MeterPolicy.ByteGaugeSemantics
			if err := validatePlanExecutionContract(prior); err == nil {
				t.Fatal("historical plan admitted V3 sampled semantics")
			}
		})
	}
}

func TestAccountingV3AllocatedCapacityPolicy(t *testing.T) {
	for _, plan := range lifecyclePolicyPlans(t) {
		t.Run(plan.Schema, func(t *testing.T) {
			want := uint64(96 << 30)
			if plan.Schema == PlanV3Schema {
				want = 128 << 30
			}
			if plan.SafetyEnvelope.MaximumDataAllocatedBytes != want || plan.SafetyEnvelope.PressureVolumeBytes != 96<<30 ||
				plan.WorkEnvelope.MaximumDataLogicalBytes != 128<<30 || plan.SafetyEnvelope.MinimumAvailableDiskBytes != 120<<30 {
				t.Fatal("allocated, physical, logical or host capacity differs")
			}
			for _, limit := range []uint64{0, 96 << 30, 128<<30 - 1, 128 << 30, 128<<30 + 1, ^uint64(0)} {
				changed := plan
				changed.SafetyEnvelope.MaximumDataAllocatedBytes = limit
				if err := validatePlanExecutionContract(changed); (err == nil) != (limit == want) {
					t.Fatalf("allocated ceiling %d: %v", limit, err)
				}
			}
			changed := plan
			changed.SafetyEnvelope.PressureVolumeBytes = 128 << 30
			if validatePlanExecutionContract(changed) == nil {
				t.Fatal("accounting allowance changed physical volume")
			}
		})
	}
}
