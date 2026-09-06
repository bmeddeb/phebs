package t421

import (
	"encoding/json"
	"errors"
	"math"
	"slices"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	DispatchMeasurementSchema = "t422-controlled-dispatch-measurement-v1"
	ScopedTeardownSchema      = "t422-scoped-teardown-evidence-v1"
)

// DispatchAccountingMeasurement reports a controller-committed prefix, not
// successful starts or native descendants. Complete requires the owning phase
// fence/checkpoints (or final fenced producer closure), not merely a readable
// counter. A refused permission never increments these counts.
type DispatchAccountingMeasurement struct {
	Schema       string  `json:"schema"`
	Complete     bool    `json:"complete"`
	FailureClass string  `json:"failure_class,omitempty"`
	Roles        []Count `json:"roles"`
}

// SessionCensusEvidence is scoped to the launcher's recorded private execution
// sessions. It excludes the controller and final-signing root sessions, and is
// neither a global descendant inventory nor evidence of memory destruction.
type SessionCensusEvidence struct {
	EventOrdinal      uint64 `json:"event_ordinal"`
	RecordedSessions  uint64 `json:"recorded_sessions"`
	CompletedCensuses uint64 `json:"completed_censuses"`
	ObservedProcesses uint64 `json:"observed_processes"`
	Errors            uint64 `json:"errors"`
}

// ScopedTeardownEvidence records the finite cleanup corridor. Event ordinals
// prove ordering inside the receipt; executable/session custody remains the
// responsibility of the separately admitted launcher.
type ScopedTeardownEvidence struct {
	Schema                         string                `json:"schema"`
	OperationalFenceEventOrdinal   uint64                `json:"operational_fence_event_ordinal"`
	OwnedHandlesJoinedEventOrdinal uint64                `json:"owned_handles_joined_event_ordinal"`
	OwnedHandlesRemaining          uint64                `json:"owned_handles_remaining"`
	OwnedHandleWaitErrors          uint64                `json:"owned_handle_wait_errors"`
	LeaseReleasedEventOrdinal      uint64                `json:"lease_released_event_ordinal"`
	StoreLeasesRemaining           uint64                `json:"store_leases_remaining"`
	BeforeDetach                   SessionCensusEvidence `json:"before_detach"`
	DetachEventOrdinal             uint64                `json:"detach_event_ordinal"`
	DetachNonForced                bool                  `json:"detach_nonforced"`
	ExactImageRemovalEventOrdinal  uint64                `json:"exact_image_removal_event_ordinal"`
	ExactRootRemovalEventOrdinal   uint64                `json:"exact_root_removal_event_ordinal"`
	CustodyLockHeld                bool                  `json:"custody_lock_held"`
	CleanupJoinedEventOrdinal      uint64                `json:"cleanup_joined_event_ordinal"`
	CleanupHandlesRemaining        uint64                `json:"cleanup_handles_remaining"`
	CleanupWaitErrors              uint64                `json:"cleanup_wait_errors"`
	AfterCleanup                   SessionCensusEvidence `json:"after_cleanup"`
	CleanupClosedEventOrdinal      uint64                `json:"cleanup_closed_event_ordinal"`
}

// Only the V3 outer wire projection suppresses historical fields. In particular,
// a not-run V3 row must omit them even though every measurement is zero. Aliases
// retain the exact original V1/V2 field order and representation.
func (value Receipt) MarshalJSON() ([]byte, error) {
	type plain Receipt
	if value.Schema != ReceiptV3Schema {
		return json.Marshal(plain(value))
	}
	measurements := make([]json.RawMessage, len(value.Measurements))
	for index, measurement := range value.Measurements {
		if measurement.ChildProcessRoles != nil || measurement.Metrics.ChildProcesses != 0 ||
			measurement.Metrics.PeakRSSBytes != 0 || measurement.Metrics.ProcessMeasurementAvailable {
			return nil, errors.New("V3 receipt contains legacy process measurements")
		}
		type metricFields ReceiptMetrics
		metrics, err := json.Marshal(struct {
			metricFields
			ChildProcesses               *uint64     `json:"child_processes,omitempty"`
			PeakRSSBytes                 *uint64     `json:"peak_rss_bytes,omitempty"`
			ProcessMeasurementAvailable  *bool       `json:"process_measurement_available,omitempty"`
			ControlledDispatchAttempts   CountMetric `json:"controlled_dispatch_attempts"`
			DispatchMeasurementAvailable bool        `json:"dispatch_measurement_available"`
			ObservedRSSHighWaterBytes    Bytes       `json:"observed_rss_high_water_bytes"`
			NativeMeasurementAvailable   bool        `json:"native_measurement_available"`
		}{metricFields: metricFields(measurement.Metrics),
			ControlledDispatchAttempts:   measurement.Metrics.ControlledDispatchAttempts,
			DispatchMeasurementAvailable: measurement.Metrics.DispatchMeasurementAvailable,
			ObservedRSSHighWaterBytes:    measurement.Metrics.ObservedRSSHighWaterBytes,
			NativeMeasurementAvailable:   measurement.Metrics.NativeMeasurementAvailable})
		if err != nil {
			return nil, err
		}
		type measurementFields PhaseMeasurement
		measurements[index], err = json.Marshal(struct {
			measurementFields
			ChildProcessRoles []Count         `json:"child_process_roles,omitempty"`
			Metrics           json.RawMessage `json:"metrics"`
		}{measurementFields: measurementFields(measurement), Metrics: metrics})
		if err != nil {
			return nil, err
		}
	}
	if value.Teardown.DescendantsStopped || value.Teardown.ChildrenRemaining != 0 || value.Teardown.DescendantStopErrors != 0 {
		return nil, errors.New("V3 receipt contains global descendant teardown claims")
	}
	type teardownFields ReceiptTeardown
	teardown, err := json.Marshal(struct {
		teardownFields
		DescendantsStopped   *bool   `json:"descendants_stopped,omitempty"`
		ChildrenRemaining    *uint64 `json:"children_remaining,omitempty"`
		DescendantStopErrors *uint64 `json:"descendant_stop_errors,omitempty"`
	}{teardownFields: teardownFields(value.Teardown)})
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		plain
		Measurements []json.RawMessage `json:"measurements"`
		Teardown     json.RawMessage   `json:"teardown"`
	}{plain: plain(value), Measurements: measurements, Teardown: teardown})
}

func validateReceiptAccountingVersion(value Receipt, plan Plan) error {
	wantSchema := map[string]string{PlanSchema: ReceiptSchema, PlanV2Schema: "t422-combined-convergence-receipt-v2", PlanV3Schema: ReceiptV3Schema}[plan.Schema]
	if wantSchema == "" || value.Schema != wantSchema || value.Schema != plan.ReceiptContract.Schema ||
		plan.Schema == PlanV3Schema && (value.Schema != ReceiptV3Schema || plan.ProcessAccounting == nil || plan.WorkEnvelope.Schema != WorkEnvelopeV3Schema) ||
		plan.Schema != PlanV3Schema && value.Schema == ReceiptV3Schema {
		return errors.New("receipt process-accounting version is invalid")
	}
	for _, measurement := range value.Measurements {
		metrics := measurement.Metrics
		if plan.Schema == PlanV3Schema {
			if measurement.ChildProcessRoles != nil || metrics.ChildProcesses != 0 || metrics.PeakRSSBytes != 0 || metrics.ProcessMeasurementAvailable {
				return errors.New("V3 receipt retains legacy process measurements")
			}
		} else if measurement.DispatchAccounting != nil || measurement.NativeObservation != nil ||
			metrics.ControlledDispatchAttempts != 0 || metrics.DispatchMeasurementAvailable ||
			metrics.ObservedRSSHighWaterBytes != 0 || metrics.NativeMeasurementAvailable {
			return errors.New("historical receipt retains V3 process measurements")
		}
	}
	if plan.Schema == PlanV3Schema {
		if value.Teardown.Scoped == nil || value.Teardown.DescendantsStopped || value.Teardown.ChildrenRemaining != 0 || value.Teardown.DescendantStopErrors != 0 {
			return errors.New("V3 receipt teardown scope is invalid")
		}
	} else if value.Teardown.Scoped != nil {
		return errors.New("historical receipt retains V3 teardown evidence")
	}
	return nil
}

func receiptRSSMetric(metrics ReceiptMetrics, schema string) (string, uint64) {
	if schema == PlanV3Schema {
		return "observed_rss_high_water_bytes", uint64(metrics.ObservedRSSHighWaterBytes)
	}
	return "peak_rss_bytes", uint64(metrics.PeakRSSBytes)
}

func receiptRSSStopCode(schema string) string {
	if schema == PlanV3Schema {
		return "observed_rss_ceiling"
	}
	return "peak_rss_ceiling"
}

// A failed store submission channel retains one coherent transaction/row
// prefix. None of these three counters may imply completeness independently.
var storeUnavailableMetricNames = []string{
	"max_rows_in_any_transaction", "store_rows", "store_transactions",
}

func hasUnavailableStoreMetrics(values []string) bool {
	for _, name := range storeUnavailableMetricNames {
		if slices.Contains(values, name) {
			return true
		}
	}
	return false
}

func validUnavailableMetricsForPlan(values []string, schema string) bool {
	if schema != PlanV3Schema {
		return validUnavailableMetrics(values)
	}
	if !slices.IsSorted(values) {
		return false
	}
	storeMetrics := 0
	for index, value := range values {
		if index > 0 && values[index-1] == value || !slices.Contains([]string{
			"available_disk_bytes", "controlled_dispatch_attempts", "data_allocated_bytes", "data_logical_bytes",
			"max_rows_in_any_transaction", "observed_rss_high_water_bytes", "store_rows", "store_transactions",
			"total_disk_bytes", "wall_ms",
		}, value) {
			return false
		}
		if slices.Contains(storeUnavailableMetricNames, value) {
			storeMetrics++
		}
	}
	return storeMetrics == 0 || storeMetrics == len(storeUnavailableMetricNames)
}

func validateAccountingMeasurement(value PhaseMeasurement, outcome string, stopped *ReceiptFailure, teardown ReceiptTeardown, plan Plan) error {
	accounting, native := value.DispatchAccounting, value.NativeObservation
	if accounting == nil || native == nil || accounting.Schema != DispatchMeasurementSchema ||
		value.Metrics.DispatchMeasurementAvailable != accounting.Complete ||
		value.Metrics.NativeMeasurementAvailable != native.Available ||
		uint64(value.Metrics.ObservedRSSHighWaterBytes) != native.ObservedRSSHighWaterBytes {
		return errors.New("V3 process measurement projection is invalid")
	}
	unavailable := func(metric string) bool {
		return stopped != nil && stopped.Phase == value.Phase && stopped.Code == "measurement_unavailable" &&
			slices.Contains(stopped.Observation.UnavailableMetrics, metric) ||
			value.Phase == "teardown" && teardown.Outcome == "failed" && slices.Contains(teardown.MeasurementUnavailable, metric)
	}
	internalFailure := stopped != nil && stopped.Phase == value.Phase && stopped.Code == "internal_error" &&
		stopped.Evidence != nil && stopped.Evidence.Internal != nil
	// A measured resource/topology crossing retains its frozen decision
	// precedence if a subsequent required observation fails. The secondary
	// failure remains typed here; it must not erase the earlier positive gauge
	// or controller prefix merely to fit measurement_unavailable's stop code.
	priorityStop := stopped != nil && stopped.Phase == value.Phase &&
		(stopped.Class == "resource" || stopped.Class == "topology")
	if accounting.Complete && accounting.FailureClass != "" || !accounting.Complete &&
		(!slices.Contains([]string{"budget_refused", "invalid_configuration", "invalid_protocol", "transport_unavailable", "canceled", "fenced", "not_quiescent", "incomplete", "panic"}, accounting.FailureClass) ||
			!unavailable("controlled_dispatch_attempts") && !internalFailure && !priorityStop) ||
		accounting.Complete && unavailable("controlled_dispatch_attempts") {
		return errors.New("controlled dispatch prefix completeness is unsubstantiated")
	}
	if !accounting.Complete && internalFailure &&
		(stopped.Evidence.Internal.Stage != "dispatch_admission" || stopped.Evidence.Internal.ErrorClass != accounting.FailureClass) {
		return errors.New("dispatch refusal differs from its typed internal failure")
	}
	if err := validateNativeObservation(*native); err != nil {
		return err
	}
	if native.Available && unavailable("observed_rss_high_water_bytes") ||
		!native.Available && !unavailable("observed_rss_high_water_bytes") && !priorityStop ||
		outcome == "passed" && (!accounting.Complete || !native.Available) ||
		value.Phase == "teardown" && teardown.Outcome == "clean" && (!accounting.Complete || !native.Available) {
		return errors.New("native observation availability lacks exact failure evidence")
	}
	index := slices.IndexFunc(plan.WorkEnvelope.Phases, func(bounds PhaseWorkBounds) bool { return bounds.Phase == value.Phase })
	if index < 0 {
		return errors.New("controlled dispatch phase is absent")
	}
	return validateControlledDispatchCounts(value.Metrics, accounting.Roles, plan.WorkEnvelope.Phases[index], plan.WorkEnvelope)
}

func validateControlledDispatchCounts(metrics ReceiptMetrics, roles []Count, bounds PhaseWorkBounds, envelope WorkEnvelope) error {
	if metrics.ChildProcesses != 0 || bounds.ChildProcessRoles != nil || envelope.ChildProcessRoles != nil ||
		envelope.MaximumChildProcessesPerPhase != 0 || len(roles) != len(envelope.ControlledDispatchRoles) ||
		len(roles) != len(bounds.ControlledDispatchRoles) || len(roles) == 0 {
		return errors.New("controlled dispatch role inventory is incomplete")
	}
	var total, maximum uint64
	for index, role := range roles {
		bound := bounds.ControlledDispatchRoles[index]
		if role.Name != envelope.ControlledDispatchRoles[index] || role.Name != bound.Name || bound.Minimum != 0 ||
			role.Count > bound.Maximum || role.Count > math.MaxUint64-total || bound.Maximum > math.MaxUint64-maximum {
			return errors.New("controlled dispatch role count exceeds accepted-permission budget")
		}
		total += role.Count
		maximum += bound.Maximum
	}
	if total != uint64(metrics.ControlledDispatchAttempts) || total > maximum || maximum > envelope.MaximumControlledDispatchAttemptsPerPhase {
		return errors.New("controlled dispatch total differs from accepted role counts")
	}
	return nil
}

func validateNativeObservation(value ProcessObservation) error {
	maximum := uint64(t4013.MaxNativeProcessRecords - 1)
	if value.MeasurementKind != "sampled_observation" || value.NativeHistory != "not_established" ||
		value.SimultaneousBounds != "not_established" || len(value.Classes) == 0 || len(value.Classes) > 14 ||
		value.ObservedDescendants > value.ObservedDescendantsHighWater || value.ObservedDescendantsHighWater > maximum ||
		value.ObservedRSSBytes > value.ObservedRSSHighWaterBytes || value.Available && (value.FailureClass != "" || value.CompletedCensuses == 0) ||
		!value.Available && !slices.Contains([]string{"measurement_unavailable", "invalid_census", "root_identity_mismatch", "unknown_classification", "counter_overflow"}, value.FailureClass) {
		return errors.New("native sampled observation is invalid")
	}
	var total, classHighWaterTotal uint64
	for index, class := range value.Classes {
		if !validObservedProcessClass(class.Class) || index > 0 && value.Classes[index-1].Class >= class.Class ||
			class.ObservedRows > class.ObservedHighWater || class.ObservedHighWater > value.ObservedDescendantsHighWater ||
			class.ObservedRows > maximum-total || value.CompletedCensuses == 0 && class.ObservedHighWater != 0 ||
			value.CompletedCensuses == 1 && class.ObservedRows != class.ObservedHighWater {
			return errors.New("native observation class inventory is invalid")
		}
		total += class.ObservedRows
		classHighWaterTotal += class.ObservedHighWater
	}
	if total != value.ObservedDescendants || classHighWaterTotal < value.ObservedDescendantsHighWater ||
		value.CompletedCensuses == 1 && (value.ObservedDescendants != value.ObservedDescendantsHighWater || value.ObservedRSSBytes != value.ObservedRSSHighWaterBytes) ||
		value.CompletedCensuses == 0 &&
			(value.ObservedDescendantsHighWater != 0 || value.ObservedRSSBytes != 0 || value.ObservedRSSHighWaterBytes != 0) ||
		value.CompletedCensuses != 0 && value.ObservedRSSBytes == 0 {
		return errors.New("native observation aggregate is incoherent")
	}
	if value.CompletedCensuses != 0 && value.ObservedDescendantsHighWater != 0 {
		// Each class maximum above its last count needs capacity in an earlier
		// completed census. Division avoids multiplying an untrusted uint64
		// census count; class sums themselves are bounded by 14 * 128 rows.
		earlierRows := classHighWaterTotal - total
		requiredEarlierCensuses := earlierRows / value.ObservedDescendantsHighWater
		if earlierRows%value.ObservedDescendantsHighWater != 0 {
			requiredEarlierCensuses++
		}
		if requiredEarlierCensuses > value.CompletedCensuses-1 {
			return errors.New("native class high-water marks exceed completed census capacity")
		}
	}
	return nil
}

func hasOwnedServerStart(states []ExactPhaseEvidence) bool {
	return slices.ContainsFunc(states, func(state ExactPhaseEvidence) bool {
		return state.Runtime != nil && state.Runtime.OwnedStart != nil && state.Runtime.OwnedStart.OwnedStartSucceeded
	})
}

func appendAccountingTeardownFailures(failed []string, value ReceiptTeardown, measurements []PhaseMeasurement, ownedServerStarted bool) ([]string, error) {
	scope := value.Scoped
	index := slices.IndexFunc(measurements, func(value PhaseMeasurement) bool { return value.Phase == "teardown" })
	if scope == nil || scope.Schema != ScopedTeardownSchema || index < 0 || value.DescendantsStopped || value.ChildrenRemaining != 0 || value.DescendantStopErrors != 0 {
		return nil, errors.New("scoped teardown evidence is invalid")
	}
	measurement := measurements[index]
	prior := measurement.StartEventOrdinal
	for _, event := range []uint64{scope.OperationalFenceEventOrdinal, scope.OwnedHandlesJoinedEventOrdinal,
		scope.LeaseReleasedEventOrdinal, scope.BeforeDetach.EventOrdinal, scope.DetachEventOrdinal,
		scope.ExactImageRemovalEventOrdinal, scope.ExactRootRemovalEventOrdinal, scope.CleanupJoinedEventOrdinal,
		scope.AfterCleanup.EventOrdinal, scope.CleanupClosedEventOrdinal} {
		if event == 0 {
			continue
		}
		if event <= prior || event >= measurement.FinishEventOrdinal {
			return nil, errors.New("scoped teardown event order is invalid")
		}
		prior = event
	}
	add := func(name string, condition bool) {
		if condition {
			failed = append(failed, name)
		}
	}
	add("operational_admissions_not_fenced", scope.OperationalFenceEventOrdinal == 0)
	add("owned_handles_not_joined", scope.OwnedHandlesJoinedEventOrdinal == 0 || scope.OwnedHandlesRemaining != 0 || scope.OwnedHandleWaitErrors != 0)
	add("store_lease_not_released", scope.LeaseReleasedEventOrdinal == 0 || scope.StoreLeasesRemaining != 0)
	add("execution_session_scope_missing", ownedServerStarted && (scope.BeforeDetach.RecordedSessions == 0 || scope.AfterCleanup.RecordedSessions == 0))
	for index, census := range []SessionCensusEvidence{scope.BeforeDetach, scope.AfterCleanup} {
		if census.CompletedCensuses > census.RecordedSessions {
			return nil, errors.New("session census count exceeds its recorded scope")
		}
		name := []string{"execution_sessions", "cleanup_sessions"}[index]
		add(name+"_unavailable", census.EventOrdinal == 0 || census.CompletedCensuses != census.RecordedSessions || census.Errors != 0)
		add(name+"_remain", census.ObservedProcesses != 0)
	}
	if scope.AfterCleanup.RecordedSessions < scope.BeforeDetach.RecordedSessions {
		return nil, errors.New("cleanup census lost a recorded execution session")
	}
	add("pressure_detach_not_nonforced", scope.DetachEventOrdinal == 0 || !scope.DetachNonForced)
	add("exact_custody_removal_unproven", scope.ExactImageRemovalEventOrdinal == 0 || scope.ExactRootRemovalEventOrdinal == 0 || !scope.CustodyLockHeld)
	add("cleanup_not_closed", scope.CleanupJoinedEventOrdinal == 0 || scope.CleanupClosedEventOrdinal == 0 || scope.CleanupHandlesRemaining != 0 || scope.CleanupWaitErrors != 0 ||
		measurement.DispatchAccounting == nil || !measurement.DispatchAccounting.Complete)
	return failed, nil
}
