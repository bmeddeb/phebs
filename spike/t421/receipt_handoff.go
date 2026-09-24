package t421

import "errors"

var errReceiptSelectorCleanup = errors.New("selector cleanup evidence does not compose with phase work")

// V5 retains one completed native cleanup per admitted handoff. Its identities
// were bound to the exact input and selected runtime by the native reader;
// receipt validation checks their syntax, phase and counters, not a private
// runtime identity reconstructed from public evidence.
func validatePhaseSelectorCleanup(measurement PhaseMeasurement, outcome string, plan Plan) error {
	var handoff bool
	switch measurement.Phase {
	case "cold", "physical_delta_b", "logical_delta_b", "return_a":
		handoff = true
	}
	if plan.Schema != PlanV5Schema || outcome != "passed" || !handoff {
		if measurement.SelectorCleanup != nil {
			return errReceiptSelectorCleanup
		}
		return nil
	}
	bound, ok := SelectorHandoffCleanupForPhase(plan, measurement.Phase)
	if !ok || measurement.SelectorCleanup == nil {
		return errReceiptSelectorCleanup
	}
	value := *measurement.SelectorCleanup
	if !validDigest(value.InputSHA256) || validateSelectorCleanupObservation(plan, value, bound,
		value.InputSHA256, value.SelectedRuntimeSHA256) != nil {
		return errReceiptSelectorCleanup
	}
	readerTurns := uint64(0)
	if measurement.Phase == "physical_delta_b" {
		readerTurns = 2
	}
	turns, err := checkedInspectionReadSum(readerTurns, value.Turns)
	if err != nil || uint64(measurement.Metrics.LifecycleOwnerTurns) != turns ||
		uint64(measurement.Metrics.LifecycleDeleted) != value.Deleted ||
		uint64(measurement.Metrics.MaxLifecycleDeletesTurn) != value.MaxDeleted ||
		uint64(measurement.Metrics.ControlReads) < value.StoreReadAttempts ||
		uint64(measurement.Metrics.StoreTransactions) < value.Turns ||
		uint64(measurement.Metrics.StoreRows) < value.Deleted {
		return errReceiptSelectorCleanup
	}
	return nil
}

// Keep the historical reader validator exact. Only a validated V5 composition
// may remove its distinct cleanup subtotal; maxima are compared, not subtracted.
func readerTransitionMetrics(measurement PhaseMeasurement, plan Plan) (ReceiptMetrics, error) {
	if err := validatePhaseSelectorCleanup(measurement, "passed", plan); err != nil {
		return ReceiptMetrics{}, err
	}
	metrics := measurement.Metrics
	if plan.Schema != PlanV5Schema {
		return metrics, nil
	}
	if measurement.Phase != "physical_delta_b" || measurement.SelectorCleanup == nil {
		return ReceiptMetrics{}, errReceiptSelectorCleanup
	}
	metrics.LifecycleOwnerTurns -= CountMetric(measurement.SelectorCleanup.Turns)
	metrics.LifecycleDeleted -= CountMetric(measurement.SelectorCleanup.Deleted)
	metrics.MaxLifecycleDeletesTurn = 0
	return metrics, nil
}
