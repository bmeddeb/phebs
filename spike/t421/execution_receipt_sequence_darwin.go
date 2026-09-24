//go:build darwin

package t421

import (
	"fmt"
	"slices"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// composeExecutionSequenceReceipt joins only closed owner snapshots. Frozen
// rows describe the protocol; measurements and identities come from execution.
// The returned unsigned receipt still must cross the authenticated package gate.
func composeExecutionSequenceReceipt(plan Plan, binding ExecutionFreezeBinding, sequence *executionEpochSequenceResult, flow *ExecutionEpochOne, resources executionWholeResourceEvidence) (Receipt, error) {
	if sequence == nil || flow == nil || !resources.Joined || len(plan.PhaseOrder) != 15 {
		return Receipt{}, errExecutionReceiptAssembly
	}
	events := flow.executionNamedEventEvidence()
	measurements, err := flow.executionPhaseEventEvidence()
	if err != nil {
		return Receipt{}, err
	}
	if len(measurements) != 15 {
		return Receipt{}, errExecutionReceiptAssembly
	}
	phases := make([]PhaseResult, 15)
	outcomes := make(map[string]string, 15)
	stop := -1
	for i, phase := range plan.PhaseOrder {
		outcome := "passed"
		if i == 14 {
			outcome = plan.ReceiptContract.TeardownPhaseOutcome
		} else if events["failure:"+phase] != 0 {
			if stop >= 0 {
				return Receipt{}, fmt.Errorf("%w: repeated phase stop", errExecutionReceiptAssembly)
			}
			stop = i
			outcome = "stopped"
		} else if stop >= 0 {
			outcome = "not_run"
		}
		if outcome != "not_run" && (measurements[i].StartEventOrdinal == 0 || measurements[i].FinishEventOrdinal == 0) {
			return Receipt{}, fmt.Errorf("%w: %s event closure", errExecutionReceiptAssembly, phase)
		}
		if outcome == "not_run" && (measurements[i].StartEventOrdinal != 0 || measurements[i].FinishEventOrdinal != 0 || measurements[i].Metrics != (ReceiptMetrics{})) {
			return Receipt{}, fmt.Errorf("%w: unrun %s retained phase events", errExecutionReceiptAssembly, phase)
		}
		phases[i] = PhaseResult{Name: phase, Outcome: outcome}
		outcomes[phase] = outcome
	}
	servers := make([]ExecutionEpochOneResult, 0, 5)
	inspections := make([]ExecutionPhaseInspection, 0, 14)
	for _, server := range []ExecutionEpochOneResult{sequence.coldPhysical, sequence.logical, sequence.returnCheckpoint, sequence.restore, sequence.final} {
		if server.ServerProcesses.ServerEpoch == 0 {
			continue
		}
		servers = append(servers, server)
		inspections = append(inspections, cloneInspectionEvidence(server.Inspection)...)
	}
	// Successful epoch four is retained both before and after offline restore;
	// the latter owns restore facts and the same closed server observations.
	var metrics executionReceiptMetrics
	var unavailable [15][]string
	if stop < 0 && sequence.teardown.CustodyAbsent && sequence.teardown.CleanupClosed {
		joined, joinErr := composeExecutionEpochSequenceReceiptEvidence(plan, sequence)
		if joinErr != nil {
			return Receipt{}, fmt.Errorf("receipt metrics: %w", joinErr)
		}
		metrics = joined.metrics
	} else {
		last := stop
		if last < 0 {
			last = 13
		}
		joined, joinErr := composeExecutionStoppedMetricPrefix(plan, plan.PhaseOrder[last], sequence.teardown.Work, sequence.teardown.Accounting, sequence.teardown.Store, inspections)
		if joinErr != nil {
			return Receipt{}, fmt.Errorf("receipt stopped metrics: %w", joinErr)
		}
		metrics, unavailable = joined.Metrics, joined.Unavailable
		if err := composeExecutionHandoffEvidence(plan, sequence.teardown.Work, inspections, false, &metrics); err != nil {
			return Receipt{}, fmt.Errorf("receipt stopped handoff evidence: %w", err)
		}
	}
	if outcomes["preflight"] == "passed" {
		composeExecutionClosedPreflightMetrics(sequence.teardown.Accounting, sequence.teardown.Store, &metrics, &unavailable[0])
	}
	var authored []ExecutionAuthorResult
	if flow.epochs != nil && flow.epochs.author != nil {
		authored = flow.epochs.author.Results()
	}
	if err := composeExecutionPopulationMetrics(plan, sequence.teardown.Work, inspections, authored, &metrics); err != nil {
		return Receipt{}, fmt.Errorf("receipt population: %w", err)
	}
	// Parent workspace traversals share the same bounded observer. A completed
	// earlier phase remains usable after a later traversal refuses coverage.
	if flow.workspaceBytes != nil {
		snapshot := flow.workspaceBytes.Snapshot()
		for i, row := range snapshot.Phases {
			if !row.Completed || outcomes[plan.PhaseOrder[i]] == "not_run" {
				continue
			}
			metrics.Metrics[i].DataLogicalBytes = max(metrics.Metrics[i].DataLogicalBytes, Bytes(row.Maximum.LogicalBytes))
			metrics.Metrics[i].DataAllocatedBytes = max(metrics.Metrics[i].DataAllocatedBytes, Bytes(row.Maximum.AllocatedBytes))
			if (!snapshot.Unavailable || i < stop) && (i == 0 || i == 14 || metrics.Coverage[i].Workspace) {
				metrics.Metrics[i].AllocationMeasurementAvailable = true
				metrics.Coverage[i].Workspace = true
				unavailable[i] = slices.DeleteFunc(unavailable[i], byteUnavailableMetric)
			}
		}
	}
	if !composeExecutionPreflightGeometry(plan, binding.freeze, &metrics.Metrics[0]) {
		return Receipt{}, errExecutionReceiptAssembly
	}
	for i, phase := range plan.PhaseOrder {
		if outcomes[phase] == "not_run" {
			unrunMetrics := metrics.Metrics[i]
			unrunMetrics.DispatchMeasurementAvailable = false
			unrunMetrics.NativeMeasurementAvailable = false
			unrunMetrics.AllocationMeasurementAvailable = false
			if unrunMetrics != (ReceiptMetrics{}) || resources.Native[i].CompletedCensuses != 0 || resources.Native[i].Available || resources.Disk[i].Samples != 0 {
				return Receipt{}, fmt.Errorf("%w: unrun %s retained observed work", errExecutionReceiptAssembly, phase)
			}
			measurements[i] = PhaseMeasurement{Phase: phase}
			continue
		}
		wall := measurements[i].Metrics.WallMS
		measurements[i].Metrics = metrics.Metrics[i]
		measurements[i].Metrics.WallMS = wall
		if outcomes[phase] == "passed" {
			measurements[i].SelectorCleanup = metrics.SelectorCleanup[i]
		}
		native := resources.Native[i]
		native.Classes = slices.Clone(native.Classes)
		if err := validateNativeObservation(native); err != nil {
			return Receipt{}, fmt.Errorf("receipt %s native: %w", phase, err)
		}
		measurements[i].NativeObservation = &native
		measurements[i].Metrics.NativeMeasurementAvailable = native.Available
		measurements[i].Metrics.ObservedRSSHighWaterBytes = Bytes(native.ObservedRSSHighWaterBytes)
		dispatch := metrics.Dispatch[i]
		dispatch.Roles = slices.Clone(dispatch.Roles)
		if dispatch.Schema == "" {
			dispatch.Schema = DispatchMeasurementSchema
			for _, name := range plan.WorkEnvelope.ControlledDispatchRoles {
				dispatch.Roles = append(dispatch.Roles, Count{Name: name})
			}
		}
		if !dispatch.Complete {
			dispatch.FailureClass = "incomplete"
			unavailable[i] = append(unavailable[i], "controlled_dispatch_attempts")
		}
		measurements[i].DispatchAccounting = &dispatch
		if !native.Available {
			unavailable[i] = append(unavailable[i], "observed_rss_high_water_bytes")
		}
		disk := resources.Disk[i]
		measurements[i].Metrics.AvailableDiskBytes = Bytes(disk.Available)
		measurements[i].Metrics.TotalDiskBytes = Bytes(disk.Total)
		if i == 0 {
			// Preflight owns the native disk sample authenticated by the freeze.
			composeExecutionPreflightGeometry(plan, binding.freeze, &measurements[i].Metrics)
		} else if disk.Samples == 0 || disk.Unavailable {
			// V3 represents an unavailable disk gauge as zero. The owner's
			// positive checkpoint samples remain in resources.Disk; they are
			// not a complete phase minimum after a later probe fails.
			measurements[i].Metrics.AvailableDiskBytes = 0
			measurements[i].Metrics.TotalDiskBytes = 0
			unavailable[i] = append(unavailable[i], "available_disk_bytes", "total_disk_bytes")
		}
		if wall == 0 {
			unavailable[i] = append(unavailable[i], "wall_ms")
		}
		if !measurements[i].Metrics.AllocationMeasurementAvailable {
			unavailable[i] = append(unavailable[i], "data_allocated_bytes", "data_logical_bytes")
		}
		if !metrics.Coverage[i].Store {
			unavailable[i] = append(unavailable[i], storeUnavailableMetricNames...)
		}
		slices.Sort(unavailable[i])
		unavailable[i] = slices.Compact(unavailable[i])
		if outcomes[phase] == "passed" && len(unavailable[i]) > 0 {
			return Receipt{}, fmt.Errorf("%w: passed %s lacks %v", errExecutionReceiptAssembly, phase, unavailable[i])
		}
	}
	if stop >= 0 {
		failure, failureErr := composeExecutionObservedFailure(plan, measurements, stop, events["failure:"+plan.PhaseOrder[stop]], unavailable[stop])
		if failureErr != nil {
			return Receipt{}, failureErr
		}
		phases[stop].Failure = &failure
	}
	revisions, err := composeExecutionRevisionResults(plan, outcomes, authored)
	if err != nil {
		return Receipt{}, err
	}
	authorities, err := composeExecutionSequenceAuthorities(plan, outcomes, flow.acceptedAuthorityPrefix(), revisions)
	if err != nil {
		return Receipt{}, err
	}
	states, err := composeExecutionReceiptStates(plan, binding.freeze, phases, measurements, authorities, servers)
	if err != nil {
		return Receipt{}, fmt.Errorf("receipt states: %w", err)
	}
	archive, err := composeExecutionSequenceArchiveEvidence(plan, sequence, measurements, authorities, events)
	if err != nil {
		return Receipt{}, fmt.Errorf("receipt archive: %w", err)
	}
	transitions, err := composeExecutionReceiptTransitions(plan, binding.freeze, phases, measurements, authorities, servers, events, flow.executionNamedEventTimes(), archive, nil)
	if err != nil {
		return Receipt{}, fmt.Errorf("receipt transitions: %w", err)
	}
	queries := QueryEvidence{Phase: "product_queries", Outcome: outcomes["product_queries"]}
	relationships := RelationshipEvidence{Phase: "product_queries", Outcome: outcomes["product_queries"]}
	if queries.Outcome == "passed" {
		if sequence.final.QueryResults == nil {
			return Receipt{}, errExecutionReceiptAssembly
		}
		queries = *sequence.final.QueryResults
		relationships, err = composeExecutionRelationshipEvidence(plan, inspections, authorities, sequence.final)
		if err != nil {
			return Receipt{}, fmt.Errorf("receipt relationships: %w", err)
		}
	}
	teardown, err := composeExecutionTeardownEvidence(plan, binding.freeze, sequence, measurements, events, unavailable[14], hasOwnedServerStart(states))
	if err != nil {
		return Receipt{}, fmt.Errorf("receipt teardown: %w", err)
	}
	return assembleExecutionReceipt(plan, binding, executionReceiptEvidence{Phases: phases, Measurements: measurements, Authorities: authorities, States: states, Transitions: transitions, Queries: queries, Relationships: relationships, Revisions: revisions, Teardown: teardown})
}

func composeExecutionSequenceAuthorities(plan Plan, outcomes map[string]string, accepted []AuthorityPhaseResult, revisions []RevisionResult) ([]AuthorityPhaseResult, error) {
	expected, err := expectedAuthorityStates(plan)
	if err != nil {
		return nil, err
	}
	for _, row := range accepted {
		if outcomes[row.Phase] == "not_run" {
			return nil, errExecutionReceiptAuthority
		}
	}
	result := make([]AuthorityPhaseResult, 13)
	for i, phase := range plan.PhaseOrder[1:14] {
		value := AuthorityPhaseResult{Phase: phase, Outcome: outcomes[phase]}
		if value.Outcome == "passed" {
			if i >= len(accepted) || accepted[i].Phase != phase || accepted[i].Outcome != "passed" {
				return nil, errExecutionReceiptAuthority
			}
			value = cloneExecutionAuthorityResult(accepted[i])
		} else if value.Outcome == "stopped" {
			if i < len(accepted) && accepted[i].Phase == phase && accepted[i].Outcome == "passed" {
				value = cloneExecutionAuthorityResult(accepted[i])
				value.Outcome = "stopped"
				result[i] = value
				continue
			}
			value.PhysicalRevision = expected[phase].PhysicalRevision
			value.LogicalRevision = expected[phase].LogicalRevision
			if revision, ok := namedRevisionResult(revisions, value.PhysicalRevision); ok && revision.PhysicalOutcome == "passed" {
				value.PhysicalCommit = revision.PhysicalCommit
				value.PhysicalTree = revision.PhysicalTree
			}
		}
		result[i] = value
	}
	return result, nil
}

func composeExecutionObservedFailure(plan Plan, measurements []PhaseMeasurement, index int, event uint64, unavailable []string) (ReceiptFailure, error) {
	if index < 0 || index >= 14 || event <= measurements[index].StartEventOrdinal || event >= measurements[index].FinishEventOrdinal {
		return ReceiptFailure{}, errExecutionReceiptAssembly
	}
	metrics := measurements[index].Metrics
	value := ReceiptFailure{Phase: plan.PhaseOrder[index], Class: "internal", Code: "measurement_unavailable", Observation: FailureObservation{Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "measurement_unavailable", UnavailableMetrics: slices.Clone(unavailable)}}
	observed := &value.Observation
	primary := func(class, code, kind, metric string, limit, actual uint64) {
		value.Class = class
		value.Code = code
		observed.Kind = kind
		observed.Metric = metric
		observed.Limit = limit
		observed.Observed = actual
	}
	switch {
	case metrics.MaterializedOwnerPairs > 0:
		primary("topology", "materialized_cartesian_owner_pairs_nonzero", "counter_crossing", "materialized_cartesian_owner_pairs", 0, uint64(metrics.MaterializedOwnerPairs))
	case metrics.DirectRecoveryLimits > 0:
		primary("topology", "direct_recovery_topology_limit", "counter_limit", "direct_recovery_topology_limits", 0, uint64(metrics.DirectRecoveryLimits))
	default:
		// Preserve actual work crossings before the gauge-only recommendation rule.
		for _, metric := range boundedPhaseMetricValues(metrics, plan.WorkEnvelope.Phases[index]) {
			if v3WorkMetric(metric.name) && metric.value > metric.bound.Maximum {
				primary("resource", "phase_work_limit", "counter_crossing", metric.name, metric.bound.Maximum, metric.value)
				break
			}
		}
		if value.Class == "internal" {
			for _, metric := range []struct {
				name          string
				actual, limit uint64
			}{
				{"max_retries_on_any_unit", uint64(metrics.MaxRetriesUnit), plan.WorkEnvelope.MaximumRetriesPerUnit},
				{"max_lifecycle_deletes_in_any_turn", uint64(metrics.MaxLifecycleDeletesTurn), plan.WorkEnvelope.MaximumLifecycleDeletesPerTurn},
			} {
				if metric.actual > metric.limit {
					primary("resource", "phase_work_limit", "counter_crossing", metric.name, metric.limit, metric.actual)
					break
				}
			}
		}
		if value.Class == "internal" {
			total, err := measuredWallThrough(measurements, value.Phase)
			if err != nil {
				return ReceiptFailure{}, err
			}
			crossings := 0
			for _, gauge := range []struct {
				code, name    string
				limit, actual uint64
			}{
				{"observed_rss_ceiling", "observed_rss_high_water_bytes", plan.SafetyEnvelope.MaximumPeakRSSBytes, uint64(metrics.ObservedRSSHighWaterBytes)},
				{"data_allocated_ceiling", "data_allocated_bytes", plan.SafetyEnvelope.MaximumDataAllocatedBytes, uint64(metrics.DataAllocatedBytes)},
				{"data_logical_ceiling", "data_logical_bytes", plan.WorkEnvelope.MaximumDataLogicalBytes, uint64(metrics.DataLogicalBytes)},
				{"total_wall_ceiling", "total_wall_ms", plan.SafetyEnvelope.MaximumTotalWallMS, total},
				{"phase_deadline", "phase_wall_ms", plan.PhaseDeadlines[index].DeadlineMS, uint64(metrics.WallMS)},
			} {
				if gauge.actual > gauge.limit {
					crossings++
					primary("resource", gauge.code, "gauge_limit", gauge.name, gauge.limit, gauge.actual)
				}
			}
			if crossings > 1 {
				primary("resource", "multiple_resource_ceilings", "gauge_limit", "multiple_resource_ceilings", 0, 1)
			}
		}
	}
	if value.Class != "internal" {
		observed.UnavailableMetrics = slices.DeleteFunc(observed.UnavailableMetrics, func(name string) bool {
			return name == "observed_rss_high_water_bytes" || name == "controlled_dispatch_attempts"
		})
		if len(observed.UnavailableMetrics) == 0 {
			observed.UnavailableMetrics = nil
		}
	} else if len(unavailable) == 0 {
		value.Code = "internal_error"
		observed.Kind = "typed_error"
		observed.Metric = "internal_error"
		value.Evidence = &FailureEvidenceProjection{Schema: plan.ReceiptContract.FailureObservationSchema + "/public-projection-v1", Kind: "internal", Internal: &InternalFailureEvidence{Phase: value.Phase, Stage: "execution_sequence", ErrorClass: "operation_failed", EventOrdinal: event}}
		var err error
		observed.ObservedSHA256, err = receiptSHA256(value.Evidence)
		if err != nil {
			return ReceiptFailure{}, err
		}
	}
	var err error
	observed.EvidenceSHA256, err = receiptSHA256(*observed)
	if err != nil {
		return ReceiptFailure{}, err
	}
	if !validReceiptFailure(value, value.Phase, plan) {
		return ReceiptFailure{}, fmt.Errorf("%w: observed failure is not representable", errExecutionReceiptAssembly)
	}
	return value, nil
}

// Preparation really checkpointed and advanced phase one before Author A.
// Those monotone controller facts close only preflight, even while root and
// unopened operational producers keep the final shared snapshots incomplete.
func composeExecutionClosedPreflightMetrics(dispatch dispatchadmission.Snapshot, store storeaccounting.WireSnapshot, metrics *executionReceiptMetrics, unavailable *[]string) {
	if metrics == nil || unavailable == nil {
		return
	}
	rootClosed := false
	roots := 0
	for _, producer := range dispatch.Producers {
		if producer.Producer == executionRootProducer {
			roots++
			rootClosed = producer.Attached && producer.Checkpoint >= 1
		}
	}
	phasePresent := false
	for _, row := range dispatch.Phases {
		if row.Phase == 1 && len(row.Roles) == 7 {
			phasePresent = true
		}
	}
	if roots == 1 && rootClosed && phasePresent && metrics.Dispatch[0].Schema == DispatchMeasurementSchema && len(metrics.Dispatch[0].Roles) == 8 {
		metrics.Dispatch[0].Complete = true
		metrics.Dispatch[0].FailureClass = ""
		metrics.Metrics[0].DispatchMeasurementAvailable = true
		metrics.Coverage[0].Dispatch = true
		*unavailable = slices.DeleteFunc(*unavailable, func(name string) bool { return name == "controlled_dispatch_attempts" })
	}
	phasePresent = false
	for _, row := range store.Store.Phases {
		if row.Phase == 1 {
			phasePresent = true
		}
	}
	if store.Store.Phase > 1 && phasePresent {
		metrics.Coverage[0].Store = true
		*unavailable = slices.DeleteFunc(*unavailable, func(name string) bool { return slices.Contains(storeUnavailableMetricNames, name) })
	}
}
