package t421

import "time"

// Each passed row is projected from its owning native epoch. Unrun and generic
// stopped rows carry only their observed outcome/events, as the v3 contract
// explicitly forbids passing transition payloads on an incomplete phase.
func composeExecutionReceiptTransitions(plan Plan, freeze ExecutionFreeze, phases []PhaseResult,
	measurements []PhaseMeasurement, authorities []AuthorityPhaseResult, servers []ExecutionEpochOneResult,
	events map[string]uint64, times map[string]time.Time, archive *ArchiveTransition,
	failure *TransitionFailureProjection,
) ([]TransitionResult, error) {
	outcomes, stopped, err := validateReceiptPhases(phases, plan)
	if err != nil {
		return nil, errExecutionReceiptTransition
	}
	authority := make(map[string]AuthorityPhaseResult, len(authorities))
	for _, row := range authorities {
		authority[row.Phase] = row
	}
	meter := make(map[string]PhaseMeasurement, len(measurements))
	for _, row := range measurements {
		meter[row.Phase] = row
	}
	epochs := make(map[uint64]ExecutionEpochOneResult, len(servers))
	for _, server := range servers {
		epoch := server.ServerProcesses.ServerEpoch
		if epoch == 0 {
			continue
		}
		if _, ok := epochs[epoch]; ok {
			return nil, errExecutionReceiptTransition
		}
		epochs[epoch] = server
	}
	pressure, err := composeExecutionPressureTransitions(plan, freeze, outcomes, authority, epochs[4].transitionObservations.pressure, epochs[4].ServerProcesses.ServerEpoch, events)
	if err != nil {
		return nil, err
	}
	result := make([]TransitionResult, len(transitionPhases))
	for index, phase := range transitionPhases {
		measurement, ok := meter[phase]
		if !ok {
			return nil, errExecutionReceiptTransition
		}
		value := TransitionResult{Phase: phase, Outcome: outcomes[phase], StartEventOrdinal: measurement.StartEventOrdinal, FinishEventOrdinal: measurement.FinishEventOrdinal}
		if value.Outcome != "passed" {
			if value.Outcome == "stopped" && stopped != nil && stopped.Code == "transition_mismatch" && stopped.Phase == phase {
				value.FailureProjection = failure
			}
			result[index] = value
			continue
		}
		value.ReadAccounting, err = executionReceiptTransitionReadPrefix(phase, servers)
		if err != nil {
			return nil, err
		}
		switch phase {
		case "physical_delta_b":
			if plan.Schema == PlanV5Schema && epochs[1].Attempts.Handoff.Retention != ([2]epochRetentionSweep{
				epochs[1].transitionObservations.physical.Held, epochs[1].transitionObservations.physical.Released,
			}) {
				return nil, errExecutionReceiptTransition
			}
			observed, e := composeExecutionReaderTransition(plan, measurement, authority, epochs[1].transitionObservations.physical, events)
			err = e
			value.Reader = &observed
		case "logical_delta_b", "return_a":
			epoch := uint64(2)
			if phase == "return_a" {
				epoch = 3
			}
			observed, e := composeExecutionPublicationInjection(plan, measurement, authority, epochs[epoch].transitionObservations, events, times)
			err = e
			value.Injections = []InjectionTransition{observed}
		case "stale_lease", "process_restart":
			observed := epochs[3].transitionObservations
			if phase == "process_restart" {
				observed.checkpointRecovered = epochs[4].transitionObservations.checkpointRecovered
			}
			injection, e := composeExecutionRecoveryInjection(plan, freeze, measurement, authority, observed, events, times, epochs[3], epochs[4])
			err = e
			value.Injections = []InjectionTransition{injection}
		case "pressure_80", "pressure_90", "pressure_75":
			observed, ok := pressure[phase]
			if !ok {
				return nil, errExecutionReceiptTransition
			}
			value.Pressure = &observed
		case "archive_restore":
			if archive == nil {
				return nil, errExecutionReceiptTransition
			}
			value.Archive = archive
		case "lifecycle_collection":
			observed, e := composeExecutionLifecycleTransition(plan, measurement, authority, epochs[5].transitionObservations.collectionCycle, events)
			err = e
			value.Lifecycle = &observed
		default:
			return nil, errExecutionReceiptTransition
		}
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	if validateTransitionResults(result, outcomes, stopped, authorities, measurements, plan, freeze) != nil {
		return nil, errExecutionReceiptTransition
	}
	return result, nil
}

func executionReceiptTransitionReadPrefix(phase string, servers []ExecutionEpochOneResult) (*TransitionReadSubtotal, error) {
	var result *TransitionReadSubtotal
	for _, server := range servers {
		for _, row := range server.Inspection {
			if row.Phase != phase || row.TransitionReads == nil {
				continue
			}
			actual := row.TransitionReads
			if result == nil {
				value := *actual
				result = &value
				continue
			}
			if result.Schema != actual.Schema || result.Class != actual.Class {
				return nil, errExecutionReceiptTransition
			}
			for _, count := range []struct {
				target *uint64
				add    uint64
			}{
				{&result.ReportCalls, actual.ReportCalls}, {&result.ControlFileReads, actual.ControlFileReads}, {&result.StoreReadAttempts, actual.StoreReadAttempts},
				{&result.MemberReads, actual.MemberReads}, {&result.StoreWriteAttempts, actual.StoreWriteAttempts},
			} {
				var err error
				*count.target, err = checkedInspectionReadSum(*count.target, count.add)
				if err != nil {
					return nil, errExecutionReceiptTransition
				}
			}
		}
	}
	if result == nil {
		return nil, errExecutionReceiptTransition
	}
	return result, nil
}
