package t421

import (
	"fmt"
	"slices"
	"testing"
)

func TestPressureFixtureUsesAdmittedEpochs(t *testing.T) {
	for _, test := range []struct {
		name       string
		plan       func(*testing.T) Plan
		epoch      uint64
		staleEpoch uint64
	}{
		{name: "v1", plan: frozenTestPlan, epoch: 2, staleEpoch: 4},
		{name: "v2", plan: correctedTestPlan, epoch: 4, staleEpoch: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := test.plan(t)
			freeze := admittedExecutionFreezeTestBinding(t, plan, executionFreezeTestCommits()).freeze
			phases := []string{"pressure_80", "pressure_90", "pressure_75"}
			outcomes := make(map[string]string, len(phases))
			authority := make(map[string]AuthorityPhaseResult, len(phases))
			measurements := make([]PhaseMeasurement, len(phases))
			// These are pressure-only modeled inputs, not native authority, worker,
			// lifecycle, process, or disk evidence. Native identity fields remain
			// unset; only the pressure subvalidator's authority continuity is used.
			transitions := []TransitionResult{{
				Phase: "process_restart", Outcome: "passed",
				Injections: []InjectionTransition{{
					FailurePoint: "checkpointed_hard_restart", ProcessEpochAfter: test.epoch,
				}},
			}}
			for index, phase := range phases {
				epoch, _, ok := expectedPhaseRuntime(freeze.Profile.Epochs, phase)
				if !ok || epoch != test.epoch {
					t.Fatalf("%s admitted epoch = %d, present = %v; want %d", phase, epoch, ok, test.epoch)
				}
				outcomes[phase] = "passed"
				authority[phase] = AuthorityPhaseResult{
					Phase: phase, Outcome: "passed", AuthorityState: AuthorityState{Current: true},
				}
				measurements[index] = PhaseMeasurement{Phase: phase}
				start := uint64(index+1) * 100
				transitions = append(transitions, TransitionResult{
					Phase: phase, Outcome: "passed", StartEventOrdinal: start, FinishEventOrdinal: start + 100,
				})
			}
			testPressureTransitions(t, plan, freeze, authority, measurements, transitions)
			metrics := make(map[string]ReceiptMetrics, len(phases))
			for index, phase := range phases {
				metrics[phase] = measurements[index].Metrics
				if got := transitions[index+1].Pressure.ServerEpoch; got != test.epoch {
					t.Fatalf("%s fixture epoch = %d, want %d", phase, got, test.epoch)
				}
			}
			if err := validatePressureTransitions(transitions, outcomes, authority, metrics, plan, freeze); err != nil {
				t.Fatalf("modeled pressure sequence: %v", err)
			}
			for index, phase := range phases {
				t.Run("stale_"+phase, func(t *testing.T) {
					mutated := slices.Clone(transitions)
					prior := SHA256([]byte("t422-pressure-sequence-start-v1"))
					for pressureIndex, pressurePhase := range phases {
						value := *transitions[pressureIndex+1].Pressure
						if pressureIndex == index {
							value.ServerEpoch = test.staleEpoch
						}
						// Keep the entire sequence digest valid so the stale epoch,
						// not stale hash evidence, is the only changed predicate.
						value.PriorGateSequenceSHA256 = prior
						var err error
						value.GateSequenceSHA256, err = pressureSequenceSHA256(pressurePhase, value)
						if err != nil {
							t.Fatal(err)
						}
						mutated[pressureIndex+1].Pressure = &value
						prior = value.GateSequenceSHA256
					}
					err := validatePressureTransitions(mutated, outcomes, authority, metrics, plan, freeze)
					want := fmt.Sprintf("phase %q pressure facts are invalid", phase)
					if err == nil || err.Error() != want {
						t.Fatalf("stale epoch refusal = %v, want %q", err, want)
					}
				})
			}
		})
	}
}
