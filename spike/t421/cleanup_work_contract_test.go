package t421

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func TestSelectedCleanupWorkDerivation(t *testing.T) {
	raw, err := os.ReadFile("plan-v2.json")
	if err != nil || SHA256(raw) != retainedPlanV2SHA256 {
		t.Fatalf("retained V2: %v", err)
	}
	var base Plan
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	plan := accountingTestPlan(t)
	if plan.WorkEnvelope.MaximumStoreRowsPerTransaction != 512 ||
		plan.WorkEnvelope.MaximumLifecycleDeletesPerTurn != 1024 ||
		uint64(lifecycle.MaxCycleObservationTurns) != 4096 {
		t.Fatal("selected cleanup changed transaction or turn geometry")
	}
	if base.WorkEnvelope.MaximumLifecycleDeletesPerTurn != 16 {
		t.Fatal("retained V2 deletion limit changed")
	}
	for index, phase := range plan.WorkEnvelope.Phases {
		prior := base.WorkEnvelope.Phases[index]
		t.Run(phase.Phase, func(t *testing.T) {
			switch phase.Phase {
			case "pressure_80", "pressure_75", "lifecycle_collection":
				if phase.StoreTransactions.Maximum != prior.StoreTransactions.Maximum+274432 ||
					phase.StoreRows.Maximum != prior.StoreRows.Maximum+2105344 ||
					phase.StoreTransactions.Minimum != prior.StoreTransactions.Minimum ||
					phase.StoreRows.Minimum != prior.StoreRows.Minimum ||
					phase.LifecycleDeleted.Minimum != prior.LifecycleDeleted.Minimum ||
					phase.LifecycleDeleted.Maximum != 4194304 ||
					!reflect.DeepEqual(phase.LifecycleOwnerTurns, prior.LifecycleOwnerTurns) {
					t.Fatalf("cleanup reserve/minima mismatch: %+v", phase)
				}
			default:
				if phase.StoreTransactions != prior.StoreTransactions || phase.StoreRows != prior.StoreRows ||
					phase.LifecycleDeleted != prior.LifecycleDeleted || phase.LifecycleOwnerTurns != prior.LifecycleOwnerTurns {
					t.Fatal("cleanup correction escaped its three phases")
				}
			}
		})
	}
}

// Recipe maxima are independent: no assumed fair rotation, actual-deletion
// substitution for submitted operands, or nominal twelve-query enforcement.
func TestSelectedCleanupStoreRecipeReserve(t *testing.T) {
	for _, recipe := range []struct {
		name               string
		transactions, rows uint64
	}{
		{"catalog_v3", 2 * 11, 10 + 16},
		{"generation_schedules", 5, 5 * 16},
		{"jobs", 1, 16},
		{"legacy_relationship", 1, 512},
		{"relationship_v3", 64 + 1, 64 + 1},
		{"filesystem_and_static", 0, 0},
	} {
		t.Run(recipe.name, func(t *testing.T) {
			if 4096*(2+recipe.transactions) > 274432 || 4096*(2+recipe.rows) > 2105344 {
				t.Fatal("owner recipe exceeds conservative cleanup reserve")
			}
		})
	}
}
