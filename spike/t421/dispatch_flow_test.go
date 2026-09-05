package t421

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func testExecutionDispatchBindings() (bindings [executionProducerCount][32]byte) {
	for index := range bindings {
		bindings[index][0] = byte(index + 1)
	}
	return bindings
}

func TestExecutionDispatchFlowExactBudgetsAndTopology(t *testing.T) {
	plan := accountingTestPlan(t)
	config, err := executionDispatchConfig(plan, testExecutionDispatchBindings())
	if err != nil {
		t.Fatal(err)
	}
	want := dispatchadmission.Limits{Producers: 11, Sites: 120, Roles: 7, Phases: 15,
		ActivePerProducer: 61, Attempts: 547195, WireBytes: 140090368, AckTimeout: 5 * time.Second}
	if config.Limits != want || len(config.Producers) != 11 || len(config.Phases) != 15 {
		t.Fatalf("operational construction differs: %+v", config.Limits)
	}
	controller, err := dispatchadmission.New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Complete || snapshot.Attempts != 0 || snapshot.ReservedWireBytes != 0 {
		t.Fatalf("construction invented execution: %+v / %v", snapshot, err)
	}
	for index, producer := range config.Producers {
		if producer.ID != uint32(index+1) || producer.Binding != testExecutionDispatchBindings()[index] {
			t.Fatal("configured producer identity changed")
		}
		var sites []dispatchadmission.Site
		switch producer.ID {
		case 1:
			sites = executionRootSites()
		case 7, 8, 9:
			sites = dispatchadmission.AuthorSites()
		default:
			sites = dispatchadmission.ProductionSites()
		}
		if !slices.Equal(sites, producer.Sites) {
			t.Fatal("producer gained another role/site recipe")
		}
	}
	for index, phase := range config.Phases {
		if phase.ID != uint32(index+1) || len(phase.Roles) != 7 {
			t.Fatal("configured phase order changed")
		}
		var sum uint64
		for roleIndex, role := range phase.Roles {
			if role.Role != uint32(roleIndex+1) {
				t.Fatal("numeric roles are not the closed sorted inventory")
			}
			found := false
			for _, bound := range plan.ProcessAccounting.DispatchBudgets[index].Roles {
				if executionDispatchRole(bound.Name) == role.Role {
					found = true
					if role.Attempts != bound.Maximum {
						t.Fatal("construction changed a frozen role ceiling")
					}
				}
			}
			if !found {
				t.Fatal("construction invented a role")
			}
			sum += role.Attempts
		}
		if sum != plan.ProcessAccounting.DispatchBudgets[index].MaximumAttempts {
			t.Fatal("phase subtotal differs")
		}
	}
	wantPhases := [][]uint32{{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		{2, 3, 4}, {5}, {6, 7, 8}, {8, 9, 10, 11}, {12, 13, 14}, {2}, {4}, {6}, {12}, {12}}
	for index, want := range wantPhases {
		if !slices.Equal(executionProducerPhases(uint32(index+1)), want) {
			t.Fatal("producer lifetime gained an epoch or phase")
		}
	}
	if executionProducerPhases(0) != nil || executionProducerPhases(12) != nil {
		t.Fatal("unknown producer acquired a phase recipe")
	}
	// Every returned slice is copied; one consumer cannot mutate the next
	// independently built configuration or its fixed lifetime inventory.
	original, err := executionDispatchConfig(plan, testExecutionDispatchBindings())
	if err != nil {
		t.Fatal(err)
	}
	config.Producers[0].Sites[0].Role = 999
	config.Phases[0].Roles[0].Attempts++
	phases := executionProducerPhases(2)
	phases[0] = 999
	fresh, err := executionDispatchConfig(plan, testExecutionDispatchBindings())
	if err != nil || !reflect.DeepEqual(original, fresh) || executionProducerPhases(2)[0] != 2 {
		t.Fatal("caller mutation changed a later fixed recipe")
	}
}

func TestExecutionDispatchFlowRefusesUnboundOrChangedPlans(t *testing.T) {
	for _, mode := range []string{"zero-binding", "duplicate-binding", "v2", "missing-accounting", "role", "attempts", "phase", "selected-domains"} {
		t.Run(mode, func(t *testing.T) {
			plan := accountingTestPlan(t)
			bindings := testExecutionDispatchBindings()
			switch mode {
			case "zero-binding":
				bindings[7] = [32]byte{}
			case "duplicate-binding":
				bindings[8] = bindings[1]
			case "v2":
				plan.Schema = PlanV2Schema
			case "missing-accounting":
				plan.ProcessAccounting = nil
			case "role":
				plan.ProcessAccounting.DispatchBudgets[0].Roles[0].Name = "invented"
			case "attempts":
				plan.ProcessAccounting.DispatchBudgets[1].MaximumAttempts++
			case "phase":
				plan.PhaseOrder[0] = "invented"
			case "selected-domains":
				plan.Profile.Pipeline.ExtractionDomains = plan.Profile.Pipeline.ExtractionDomains[:1]
			}
			if result, err := executionDispatchConfig(plan, bindings); err == nil || !reflect.DeepEqual(result, dispatchadmission.Config{}) {
				t.Fatal("unbound or changed flow admitted")
			}
		})
	}
}

func TestExecutionDispatchFlowRefusesMalformedPhysicalHistory(t *testing.T) {
	for _, count := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			plan := accountingTestPlan(t)
			if count == 4 {
				plan.Revisions.Physical = append(plan.Revisions.Physical, plan.Revisions.Physical[0])
			} else {
				plan.Revisions.Physical = plan.Revisions.Physical[:count]
			}
			// Exercise the shared trusted-revision fast path directly, too: the
			// shape guard belongs there, not solely at this constructor's edge.
			if err := validatePlan(plan, &plan.Revisions); err == nil {
				t.Fatal("shared validator admitted malformed physical history")
			}
			if result, err := executionDispatchConfig(plan, testExecutionDispatchBindings()); err == nil || !reflect.DeepEqual(result, dispatchadmission.Config{}) {
				t.Fatal("malformed physical history acquired dispatch configuration")
			}
		})
	}
}
