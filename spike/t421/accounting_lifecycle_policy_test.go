package t421

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

// These validator fixtures read the retained plans; they do not construct a
// full corpus, receipt, native lifecycle run or new measurement evidence.
func lifecyclePolicyPlans(t *testing.T) []Plan {
	t.Helper()
	var plans []Plan
	for _, file := range []struct{ path, digest string }{
		{"plan.json", retainedPlanSHA256}, {"plan-v2.json", retainedPlanV2SHA256},
	} {
		raw, err := os.ReadFile(file.path)
		if err != nil || SHA256(raw) != file.digest {
			t.Fatalf("retained plan changed: %s / %v", file.path, err)
		}
		var plan Plan
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}
	return append(plans, accountingTestPlan(t))
}

func TestAccountingV3NormalLifecyclePolicy(t *testing.T) {
	plans := lifecyclePolicyPlans(t)
	current := plans[2]
	if err := validatePlanExecutionContract(current); err != nil {
		t.Fatal(err)
	}
	for _, clause := range []string{
		"selected_v3_pressure_80=one_fresh_sorted_error_free_cycle",
		"durable_jobs=truthful_lower_bound_backlog_allowed",
		"other_owners=exact_drained",
		"capacity=exact_normal_after_latest_owner",
		"required_byte_checkpoints_and_8_68_gib_normalization_unchanged",
	} {
		t.Run(clause, func(t *testing.T) {
			if strings.Count(current.MeterPolicy.LifecycleSemantics, clause) != 1 {
				t.Fatal("missing or duplicate selected normal policy clause")
			}
			changed := current
			changed.MeterPolicy.LifecycleSemantics = strings.Replace(current.MeterPolicy.LifecycleSemantics, clause, "", 1)
			if validatePlanExecutionContract(changed) == nil {
				t.Fatal("incomplete V3 lifecycle policy accepted")
			}
		})
	}
	for _, prior := range plans[:2] {
		if prior.SafetyEnvelope.MaximumDataAllocatedBytes != 96<<30 || current.SafetyEnvelope.MaximumDataAllocatedBytes != 128<<30 {
			t.Fatal("versioned allocated ceiling changed")
		}
		remaining := current.SafetyEnvelope
		remaining.MaximumDataAllocatedBytes = prior.SafetyEnvelope.MaximumDataAllocatedBytes
		if prior.MeterPolicy.LifecycleSemantics != frozenMeterPolicy().LifecycleSemantics ||
			!strings.HasPrefix(current.MeterPolicy.LifecycleSemantics, prior.MeterPolicy.LifecycleSemantics+";") ||
			!reflect.DeepEqual(prior.SafetyEnvelope, remaining) ||
			!reflect.DeepEqual(prior.PhaseDeadlines, current.PhaseDeadlines) {
			t.Fatal("historical policy, metric units, safety bounds or deadlines changed")
		}
		if err := validatePlanExecutionContract(prior); err != nil {
			t.Fatal("retained policy refused", err)
		}
		prior.MeterPolicy.LifecycleSemantics = current.MeterPolicy.LifecycleSemantics
		if validatePlanExecutionContract(prior) == nil {
			t.Fatal("historical plan admitted V3 completion policy")
		}
	}
	current.MeterPolicy.LifecycleSemantics = frozenMeterPolicy().LifecycleSemantics
	if validatePlanExecutionContract(current) == nil {
		t.Fatal("V3 admitted absent selected completion policy")
	}
}

func TestPressure80LifecycleBacklogVersioning(t *testing.T) {
	for _, plan := range lifecyclePolicyPlans(t) {
		for _, mode := range []string{"drained", "job-backlog", "job-error", "job-exact", "other-backlog", "other-lower-bound", "late-owner", "missing-positive-total"} {
			t.Run(plan.Schema+"/"+mode, func(t *testing.T) {
				owners, capacity := testLifecycleOwners(plan, 1_000)
				job := slices.IndexFunc(owners, func(owner LifecycleOwnerResult) bool { return owner.Name == lifecycle.JobOwner })
				if job < 0 || owners[0].Name == lifecycle.JobOwner {
					t.Fatal("fixture owner inventory changed")
				}
				switch mode {
				case "job-backlog":
					owners[job].Backlog = true
				case "job-error":
					owners[job].State = "error"
				case "job-exact":
					owners[job].Completeness = string(lifecycle.Exact)
				case "other-backlog":
					owners[0].Backlog = true
				case "other-lower-bound":
					owners[0].Completeness = string(lifecycle.LowerBound)
				case "late-owner":
					owners[len(owners)-1].AttemptedAtUnixMS = capacity + 1
				case "missing-positive-total":
					owners[0].Scanned = 1
				}
				value := PressureTransition{LifecycleFenceUnixMS: 1_000, CapacityObservedUnixMS: capacity,
					LifecycleOwnerTurns: uint64(len(owners)), Owners: owners}
				var err error
				value.LifecycleCycleSHA256, err = pressureLifecycleCycleSHA256(value)
				if err != nil {
					t.Fatal(err)
				}
				metrics := ReceiptMetrics{LifecycleOwnerTurns: CountMetric(len(owners))}
				want := mode == "drained" || mode == "job-backlog" && plan.Schema != PlanV2Schema
				if err := validatePressure80Lifecycle(value, metrics, plan); (err == nil) != want {
					t.Fatalf("phase-nine versioned completion=%v, want accepted=%t", err, want)
				}
				// The existing recovery/fresh owner rule remains independently
				// stricter than just accepting any failed or incomplete cycle.
				want = mode == "drained" || mode == "job-backlog"
				if err := validatePressureLifecycle(value, metrics, plan); (err == nil) != want {
					t.Fatalf("recovery/fresh owner checks changed: %v", err)
				}
			})
		}
	}
}
