package t421

import (
	"slices"
	"testing"
)

func TestCorrectedRuntimeRequiresExactStartupInventory(t *testing.T) {
	freeze, states, phases, outcomes, measurements := startupRuntimeTestInventory(t, true)
	if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err != nil {
		t.Fatal(err)
	}
	if freeze.Profile.RuntimeBindingSchema != PhaseRuntimeBindingV2Schema || len(freeze.Profile.Epochs) != 5 {
		t.Fatal("corrected runtime omitted its versioned five-epoch inventory")
	}
	for _, epoch := range freeze.Profile.Epochs {
		t.Run("missing_"+epoch.LaunchPhase, func(t *testing.T) {
			freeze, states, phases, outcomes, measurements := startupRuntimeTestInventory(t, true)
			index := slices.Index(phases, epoch.LaunchPhase)
			states[index].Runtime.Startup = nil
			states[index].RuntimeSHA256 = mustReceiptSHA256(t, *states[index].Runtime)
			if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err == nil {
				t.Fatal("passed epoch without readiness evidence was accepted")
			}
		})
	}
	for _, test := range []struct {
		name string
		edit func(*ServerStartupEvidence, *PhaseMeasurement, ExecutionServerEpochProfile)
	}{
		{"late", func(value *ServerStartupEvidence, measurement *PhaseMeasurement, epoch ExecutionServerEpochProfile) {
			*value.ElapsedMS = epoch.ServerHealthDeadlineMS + 1
			measurement.Metrics.WallMS = Milliseconds(*value.ElapsedMS)
		}},
		{"wrong_epoch", func(value *ServerStartupEvidence, _ *PhaseMeasurement, _ ExecutionServerEpochProfile) {
			value.ServerEpoch++
		}},
		{"before_launch", func(value *ServerStartupEvidence, _ *PhaseMeasurement, _ ExecutionServerEpochProfile) {
			value.FinishEventOrdinal -= 2
		}},
		{"after_phase", func(value *ServerStartupEvidence, measurement *PhaseMeasurement, _ ExecutionServerEpochProfile) {
			value.FinishEventOrdinal = measurement.FinishEventOrdinal
		}},
		{"unknown_outcome", func(value *ServerStartupEvidence, _ *PhaseMeasurement, _ ExecutionServerEpochProfile) {
			value.Outcome = "probably_ready"
		}},
		{"not_ready", func(value *ServerStartupEvidence, _ *PhaseMeasurement, _ ExecutionServerEpochProfile) {
			value.Outcome = "not_ready"
		}},
		{"unmeasured_ready", func(value *ServerStartupEvidence, _ *PhaseMeasurement, _ ExecutionServerEpochProfile) {
			value.ElapsedMS = nil
		}},
		{"longer_than_phase", func(value *ServerStartupEvidence, measurement *PhaseMeasurement, _ ExecutionServerEpochProfile) {
			*value.ElapsedMS = uint64(measurement.Metrics.WallMS) + 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			freeze, states, phases, outcomes, measurements := startupRuntimeTestInventory(t, true)
			index := slices.Index(phases, "logical_delta_b")
			measurementIndex := slices.IndexFunc(measurements, func(value PhaseMeasurement) bool { return value.Phase == "logical_delta_b" })
			test.edit(states[index].Runtime.Startup, &measurements[measurementIndex], freeze.Profile.Epochs[1])
			states[index].RuntimeSHA256 = mustReceiptSHA256(t, *states[index].Runtime)
			if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err == nil {
				t.Fatal("invalid readiness evidence with recomputed binding digest was accepted")
			}
		})
	}
	t.Run("repeated_on_nonlaunch_phase", func(t *testing.T) {
		freeze, states, phases, outcomes, measurements := startupRuntimeTestInventory(t, true)
		index := slices.Index(phases, "warm_noop")
		states[index].Runtime.Startup = states[slices.Index(phases, "cold")].Runtime.Startup
		states[index].RuntimeSHA256 = mustReceiptSHA256(t, *states[index].Runtime)
		if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err == nil {
			t.Fatal("duplicated startup inventory was accepted")
		}
	})
}

func TestCorrectedRuntimePreservesStoppedStartupTruth(t *testing.T) {
	for _, test := range []struct {
		name       string
		ready      bool
		late       bool
		unmeasured bool
		unstarted  bool
	}{
		{name: "not_started", unstarted: true},
		{name: "stopped_before_ready"},
		{name: "stopped_after_deadline", late: true},
		{name: "stopped_unavailable_duration", unmeasured: true},
		{name: "stopped_after_ready", ready: true},
		{name: "stopped_after_late_ready", ready: true, late: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			freeze, states, phases, outcomes, measurements := startupRuntimeTestInventory(t, true)
			stop := slices.Index(phases, "logical_delta_b")
			for index := stop; index < len(states); index++ {
				value := &states[index]
				if index == stop {
					value.Outcome = "stopped"
				} else {
					value.Outcome, value.Runtime, value.RuntimeSHA256 = "not_run", nil, ""
				}
				outcomes[value.Phase] = value.Outcome
				measurementIndex := slices.IndexFunc(measurements, func(measurement PhaseMeasurement) bool { return measurement.Phase == value.Phase })
				if index > stop {
					measurements[measurementIndex] = PhaseMeasurement{Phase: value.Phase}
					continue
				}
				if test.unstarted {
					value.Runtime, value.RuntimeSHA256 = nil, ""
					measurements[measurementIndex].ChildProcessRoles = nil
					continue
				}
				startup := value.Runtime.Startup
				if !test.ready {
					startup.Outcome = "not_ready"
				}
				if test.late {
					*startup.ElapsedMS = freeze.Profile.Epochs[1].ServerHealthDeadlineMS + 1
					measurements[measurementIndex].Metrics.WallMS = Milliseconds(*startup.ElapsedMS)
				}
				if test.unmeasured {
					startup.ElapsedMS = nil
					measurements[measurementIndex].Metrics.WallMS = 0
				}
				value.RuntimeSHA256 = mustReceiptSHA256(t, *value.Runtime)
			}
			if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err != nil {
				t.Fatalf("truthful stopped startup or not-run epoch was refused: %v", err)
			}
		})
	}
}

func TestRuntimeV1PreservesAbsentStartupEvidence(t *testing.T) {
	freeze, states, phases, outcomes, measurements := startupRuntimeTestInventory(t, false)
	if freeze.Profile.RuntimeBindingSchema != PhaseRuntimeBindingSchema || len(freeze.Profile.Epochs) != 3 {
		t.Fatal("retained runtime schema or epoch inventory changed")
	}
	if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err != nil {
		t.Fatal(err)
	}
	for _, value := range states {
		if value.Runtime.Startup != nil {
			t.Fatal("retained runtime invented prospective startup evidence")
		}
	}
	states[0].Runtime.Startup = &ServerStartupEvidence{ServerEpoch: 1, Outcome: "ready"}
	states[0].RuntimeSHA256 = mustReceiptSHA256(t, *states[0].Runtime)
	if err := validatePhaseRuntimeBindings(states, phases, outcomes, measurements, freeze); err == nil {
		t.Fatal("V1 accepted a prospective startup field")
	}
}

func startupRuntimeTestInventory(t *testing.T, corrected bool) (ExecutionFreeze, []ExactPhaseEvidence, []string, map[string]string, []PhaseMeasurement) {
	t.Helper()
	plan := retainedWorkPlan(t)
	if corrected {
		plan.Schema, plan.PhaseStates = PlanV2Schema, correctedPhaseStates()
		var err error
		plan.WorkEnvelope, _, err = correctedWorkEnvelope(plan.Profile)
		if err != nil {
			t.Fatal(err)
		}
	}
	tools, host := executionFreezeTestTools(plan, executionFreezeTestCommits()), executionFreezeTestHost()
	profile, err := expectedExecutionProfile(plan, tools, host, executionProfileTestAdmission(t, plan, tools, host))
	if err != nil {
		t.Fatal(err)
	}
	freeze := ExecutionFreeze{Profile: profile, Tools: tools, Host: host}
	measurements := make([]PhaseMeasurement, len(plan.PhaseOrder))
	for index := range measurements {
		measurements[index] = testPhaseMeasurement(plan, freeze, index)
	}
	phases := slices.Clone(plan.PhaseOrder[1 : len(plan.PhaseOrder)-1])
	states := make([]ExactPhaseEvidence, len(phases))
	outcomes := make(map[string]string, len(phases))
	for index, phase := range phases {
		runtime := testPhaseRuntime(t, freeze, phase, measurements)
		states[index] = ExactPhaseEvidence{Phase: phase, Outcome: "passed", Runtime: &runtime, RuntimeSHA256: mustReceiptSHA256(t, runtime)}
		outcomes[phase] = "passed"
	}
	return freeze, states, phases, outcomes, measurements
}
