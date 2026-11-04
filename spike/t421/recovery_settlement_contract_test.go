package t421

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestPlanV7RecoverySettlementContract(t *testing.T) {
	v6, err := BuildPlanV6(strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	v7, err := BuildPlanV7(strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	if v7.Schema != PlanV7Schema || v7.ReceiptContract.Schema != ReceiptV7Schema ||
		v7.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV7Schema || validatePlanExecutionContract(v7) != nil {
		t.Fatalf("V7 identity = %s/%s/%s", v7.Schema, v7.ReceiptContract.Schema, v7.ToolPolicy.ExecutionFreezeSchema)
	}
	if v7.Correction.InspectionInventorySHA256 == v6.Correction.InspectionInventorySHA256 ||
		!strings.Contains(v7.Correction.ReadAccountingPolicy, recoverySettlementReadAccounting+";") ||
		v7.Correction.RequiredReadiness[len(v7.Correction.RequiredReadiness)-1] != recoverySettlementPolicy {
		t.Fatal("V7 correction does not bind the settlement barrier")
	}
	rows6, epochs6, err := planInspectionInventory(v6)
	if err != nil {
		t.Fatal(err)
	}
	rows7, epochs7, err := planInspectionInventory(v7)
	if err != nil {
		t.Fatal(err)
	}
	for index := range rows7 {
		row6, row7 := rows6[index], rows7[index]
		if row7.Phase != "process_restart" {
			if !reflect.DeepEqual(row6, row7) {
				t.Fatalf("V7 changed unrelated phase %s", row7.Phase)
			}
			continue
		}
		if row6.ScheduleSettlementCalls != (CounterBound{}) || row7.ScheduleSettlementCalls.Minimum != 1 ||
			row7.ScheduleSettlementCalls.Maximum < 2 ||
			row7.ScheduleSettlementStoreReadAttempts.Maximum != row7.ScheduleSettlementCalls.Maximum*store.MaxGenerationScheduleReadAttempts ||
			*row7.TransitionRead != *row6.TransitionRead {
			t.Fatalf("V7 process_restart Z row = %+v", row7)
		}
		phase6, phase7 := v6.WorkEnvelope.Phases[index], v7.WorkEnvelope.Phases[index]
		if phase7.ControlReads.Maximum != phase6.ControlReads.Maximum+row7.ScheduleSettlementStoreReadAttempts.Maximum {
			t.Fatalf("V7 phase K = %d, V6 %d + Z %d", phase7.ControlReads.Maximum, phase6.ControlReads.Maximum, row7.ScheduleSettlementStoreReadAttempts.Maximum)
		}
		for epochIndex := range epochs7 {
			if epochs7[epochIndex].ServerEpoch != row7.ServerEpoch {
				if epochs7[epochIndex] != epochs6[epochIndex] {
					t.Fatalf("V7 changed epoch %d", epochs7[epochIndex].ServerEpoch)
				}
				continue
			}
			if epochs7[epochIndex].AccountedServerRequestsMaximum != epochs6[epochIndex].AccountedServerRequestsMaximum+row7.ScheduleSettlementCalls.Maximum ||
				epochs7[epochIndex].AccountedServerRequestsMaximum > epochFourInspectionOrdinalMaximum {
				t.Fatalf("V7 epoch %d accounted requests = %d (V6 %d, cap %d)", row7.ServerEpoch,
					epochs7[epochIndex].AccountedServerRequestsMaximum, epochs6[epochIndex].AccountedServerRequestsMaximum, epochFourInspectionOrdinalMaximum)
			}
			t.Logf("epoch %d accounted requests V6=%d V7=%d cap=%d Z=%d", row7.ServerEpoch,
				epochs6[epochIndex].AccountedServerRequestsMaximum, epochs7[epochIndex].AccountedServerRequestsMaximum,
				epochFourInspectionOrdinalMaximum, row7.ScheduleSettlementCalls.Maximum)
		}
	}
}
