package t421

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestAccountingV3RestoreRelationshipWork(t *testing.T) {
	plan := accountingTestPlan(t)
	raw, err := os.ReadFile("plan-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var prior Plan
	if err := json.Unmarshal(raw, &prior); err != nil {
		t.Fatal(err)
	}
	legacy := prior.WorkEnvelope
	// Normalize the separately approved V3 read and cleanup corrections before
	// comparing every remaining field against the unchanged V2 recipe.
	if err := applyCorrectedPhaseReadMaximums(&legacy, plan); err != nil {
		t.Fatal(err)
	}
	for index, got := range plan.WorkEnvelope.Phases {
		want := legacy.Phases[index]
		switch want.Phase {
		case "pressure_80", "pressure_75", "lifecycle_collection":
			// Independent expected arithmetic, not the production correction
			// helper: unrelated fields must still compare exactly below.
			want.StoreTransactions.Maximum += 4096 * (2 + 65)
			want.StoreRows.Maximum += 4096 * (2 + 512)
			want.LifecycleDeleted.Maximum = 4096 * 1024
		}
		got.ChildProcessRoles = want.ChildProcessRoles
		got.ControlledDispatchRoles = want.ControlledDispatchRoles
		if got.Phase == "archive_restore" {
			for _, field := range []struct {
				name          string
				actual, prior CounterBound
				quantity      uint64
			}{
				{"builds", got.RelationshipBuildAttempts, want.RelationshipBuildAttempts, 1},
				{"projections", got.RelationshipProjections, want.RelationshipProjections, plan.Profile.Pipeline.RelationshipProjections},
				{"references", got.ServiceReferences, want.ServiceReferences, plan.Profile.Pipeline.ServiceReferences},
			} {
				if field.prior != (CounterBound{}) || field.actual != (CounterBound{Minimum: field.quantity, Maximum: field.quantity}) {
					t.Fatalf("%s: V2=%+v V3=%+v", field.name, field.prior, field.actual)
				}
			}
			if got.RelationshipProjections.Maximum != 20_999 || got.ServiceReferences.Maximum != 31_998 {
				t.Fatal("approved frozen corpus quantities changed", got)
			}
			got.RelationshipBuildAttempts, got.RelationshipProjections, got.ServiceReferences = want.RelationshipBuildAttempts, want.RelationshipProjections, want.ServiceReferences
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unrelated work changed for %s", got.Phase)
		}
	}
}

func TestAccountingV3RestoreRelationshipRejectsSlack(t *testing.T) {
	for _, test := range []struct {
		name  string
		field func(*PhaseWorkBounds) *CounterBound
	}{
		{"builds", func(row *PhaseWorkBounds) *CounterBound { return &row.RelationshipBuildAttempts }},
		{"projections", func(row *PhaseWorkBounds) *CounterBound { return &row.RelationshipProjections }},
		{"references", func(row *PhaseWorkBounds) *CounterBound { return &row.ServiceReferences }},
	} {
		for _, change := range []string{"zero", "retry_slack"} {
			t.Run(test.name+"/"+change, func(t *testing.T) {
				plan := accountingTestPlan(t)
				if err := validatePlanExecutionContract(plan); err != nil {
					t.Fatal(err)
				}
				for index := range plan.WorkEnvelope.Phases {
					row := &plan.WorkEnvelope.Phases[index]
					if row.Phase == "archive_restore" {
						field := test.field(row)
						if change == "zero" {
							*field = CounterBound{}
						} else {
							field.Maximum++
						}
					}
				}
				if err := validatePlanExecutionContract(plan); err == nil {
					t.Fatal("unapproved restore work admitted")
				}
			})
		}
	}
}
