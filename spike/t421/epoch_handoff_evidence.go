package t421

import "encoding/hex"

// Join the independently retained HTTP terminal to its actual producer's
// closed native log. Neither a frozen ceiling nor an accepted F supplies work.
// Partial executions retain successful earlier phases only; stopped-phase
// counters remain in the native prefix and are never promoted to a proof.
func composeExecutionHandoffEvidence(plan Plan, work executionJoinedWork, rows []ExecutionPhaseInspection, complete bool, out *executionReceiptMetrics) error {
	var seen [15]bool
	for _, row := range rows {
		bound, applies := SelectorHandoffCleanupForPhase(plan, row.Phase)
		if plan.Schema != PlanV5Schema || !applies {
			if row.SelectorCleanup != nil {
				return errExecutionReceiptMetrics
			}
			continue
		}
		index := -1
		for i, phase := range plan.PhaseOrder {
			if phase == row.Phase {
				index = i
			}
		}
		if index < 0 || index >= len(seen) || seen[index] || row.ServerEpoch != bound.ServerEpoch {
			return errExecutionReceiptMetrics
		}
		seen[index] = true
		if !row.SelectorAccepted {
			if complete {
				return errExecutionReceiptMetrics
			}
			continue
		}
		if row.Final == nil || row.Final.Projection.Phase != row.Phase || row.SelectorCleanup == nil {
			return errExecutionReceiptMetrics
		}
		slot := executionJoinedWorkSlot(uint32(bound.ServerEpoch + 1))
		if slot < 0 {
			return errExecutionReceiptMetrics
		}
		record := work.Records[slot]
		if record.Producer != uint32(bound.ServerEpoch+1) || !record.Joined || !record.SessionEmpty ||
			!record.Attempts.SourceBound || !record.Attempts.ScanComplete ||
			record.Input == ([32]byte{}) || !executionHandoffPhaseClosed(plan, record, index) ||
			*row.SelectorCleanup != record.Attempts.Handoff.Cleanup[index] {
			return errExecutionReceiptMetrics
		}
		value := *row.SelectorCleanup
		if err := validateSelectorCleanupObservation(plan, value, bound, "sha256:"+hex.EncodeToString(record.Input[:]), value.SelectedRuntimeSHA256); err != nil {
			return errExecutionReceiptMetrics
		}
		// The HTTP owner already checked this selector against its native tail.
		// This exact terminal comparison prevents swapping a log from another
		// selected runtime; public receipt syntax alone is not authentication.
		measurement := PhaseMeasurement{Phase: row.Phase, Metrics: out.Metrics[index], SelectorCleanup: &value}
		if err := validatePhaseSelectorCleanup(measurement, "passed", plan); err != nil {
			return errExecutionReceiptMetrics
		}
		out.SelectorCleanup[index] = &value
	}
	if complete && plan.Schema == PlanV5Schema {
		for index, phase := range plan.PhaseOrder {
			if _, applies := SelectorHandoffCleanupForPhase(plan, phase); applies && (index >= len(seen) || !seen[index] || out.SelectorCleanup[index] == nil) {
				return errExecutionReceiptMetrics
			}
		}
	}
	return nil
}
