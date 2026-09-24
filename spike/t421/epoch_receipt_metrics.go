package t421

import (
	"errors"
	"math"
	"slices"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

var errExecutionReceiptMetrics = errors.New("execution receipt metrics incomplete")

type executionReceiptMetricCoverage struct {
	Producer, Dispatch, Store, Inspection, Workspace, Native bool
}

// executionReceiptMetrics is a joined, source-preserving projection. Phases
// one and fifteen remain partial until their non-server work, byte, native and
// timing owners are joined; this composer does not turn their absent families
// into observed zero.
type executionReceiptMetrics struct {
	Metrics  [15]ReceiptMetrics
	Dispatch [15]DispatchAccountingMeasurement
	Native   [15]ProcessObservation
	Coverage [15]executionReceiptMetricCoverage
	// JoinedFamilies records only completeness of the six families this
	// composer owns. It is not a complete ReceiptMetrics claim.
	JoinedFamilies  [15]bool
	SelectorCleanup [15]*SelectorCleanupEvidence
}

// composeExecutionReceiptMetrics combines only already-joined bounded streams.
// It performs no sampling, parsing, I/O or fallback inference.
func composeExecutionReceiptMetrics(
	plan Plan,
	work executionJoinedWork,
	dispatch dispatchadmission.Snapshot,
	store storeaccounting.WireSnapshot,
	inspection []ExecutionPhaseInspection,
	processes []ExecutionServerProcessObservation,
) (executionReceiptMetrics, error) {
	var out executionReceiptMetrics
	if !processAccountingPlanSemantics(plan.Schema) || plan.ProcessAccounting == nil || validatePlan(plan, &plan.Revisions) != nil ||
		len(plan.PhaseOrder) != len(out.Metrics) ||
		len(plan.WorkEnvelope.Phases) != len(out.Metrics) || !slices.Equal(plan.PhaseOrder, frozenPhaseOrder()) {
		return out, errExecutionReceiptMetrics
	}
	joined, err := work.receiptMetrics(plan)
	if err != nil {
		return out, errExecutionReceiptMetrics
	}
	out.Metrics = joined.Metrics
	for index, covered := range joined.Covered {
		out.Coverage[index].Producer = covered
	}
	if !composeExecutionDispatchMetrics(plan, dispatch, &out) || !composeExecutionStoreMetrics(plan, store, &out) ||
		!composeExecutionInspectionMetrics(plan, inspection, &out) || !composeExecutionWorkspaceMetrics(plan, work, &out) ||
		!composeExecutionNativeMetrics(plan, processes, &out) || !validateExecutionJoinedMetricBounds(plan, out) {
		return executionReceiptMetrics{}, errExecutionReceiptMetrics
	}
	if err := composeExecutionHandoffEvidence(plan, work, inspection, true, &out); err != nil {
		return executionReceiptMetrics{}, err
	}
	for index := range out.JoinedFamilies {
		coverage := out.Coverage[index]
		out.JoinedFamilies[index] = coverage.Producer && coverage.Dispatch && coverage.Store &&
			coverage.Inspection && coverage.Workspace && coverage.Native
	}
	return out, nil
}

func composeExecutionDispatchMetrics(plan Plan, snapshot dispatchadmission.Snapshot, out *executionReceiptMetrics) bool {
	if !snapshot.Complete || len(snapshot.Phases) != len(out.Metrics) || len(snapshot.Producers) != executionProducerCount ||
		len(plan.WorkEnvelope.ControlledDispatchRoles) != 8 {
		return false
	}
	for index, producer := range snapshot.Producers {
		if producer.Producer != uint32(index+1) || !producer.Attached || !producer.Closed || producer.Active != 0 {
			return false
		}
	}
	var total uint64
	for index, row := range snapshot.Phases {
		if row.Phase != uint32(index+1) || len(row.Roles) != 7 || index > 0 && snapshot.Phases[index-1].Phase >= row.Phase {
			return false
		}
		var counts [8]uint64
		var seen [8]bool
		var phaseTotal uint64
		for roleIndex, role := range row.Roles {
			if role.Role == 0 || role.Role >= uint32(len(counts)) || seen[role.Role] ||
				roleIndex > 0 && row.Roles[roleIndex-1].Role >= role.Role || role.Attempts > math.MaxUint64-phaseTotal {
				return false
			}
			counts[role.Role], seen[role.Role] = role.Attempts, true
			phaseTotal += role.Attempts
		}
		if phaseTotal != row.Attempts || row.Attempts > math.MaxUint64-total {
			return false
		}
		total += row.Attempts
		measurement := DispatchAccountingMeasurement{Schema: DispatchMeasurementSchema, Complete: true, Roles: make([]Count, 0, 8)}
		for _, name := range plan.WorkEnvelope.ControlledDispatchRoles {
			role := executionDispatchRole(name)
			if role == 0 {
				if name != "phebs-focused-index" {
					return false
				}
				measurement.Roles = append(measurement.Roles, Count{Name: name})
				continue
			}
			if role >= uint32(len(seen)) || !seen[role] {
				return false
			}
			measurement.Roles = append(measurement.Roles, Count{Name: name, Count: counts[role]})
		}
		out.Dispatch[index] = measurement
		out.Metrics[index].ControlledDispatchAttempts = CountMetric(phaseTotal)
		out.Metrics[index].DispatchMeasurementAvailable = true
		if validateControlledDispatchCounts(out.Metrics[index], measurement.Roles,
			plan.WorkEnvelope.Phases[index], plan.WorkEnvelope) != nil {
			return false
		}
		out.Coverage[index].Dispatch = true
	}
	return total == snapshot.Attempts
}

func composeExecutionStoreMetrics(plan Plan, snapshot storeaccounting.WireSnapshot, out *executionReceiptMetrics) bool {
	if !snapshot.PrefixesClosed || !snapshot.Store.PrefixesClosed || snapshot.Store.Phase != uint32(len(out.Metrics)) ||
		len(snapshot.Store.Phases) != len(out.Metrics) || len(snapshot.Store.Producers) != len(executionJoinedWork{}.Records) ||
		snapshot.Opened != len(executionJoinedWork{}.Records) || snapshot.TerminalEOF != snapshot.Opened {
		return false
	}
	for index, producer := range snapshot.Store.Producers {
		want := [...]uint32{2, 3, 4, 5, 6, 10, 11}[index]
		terminal := want == 4
		if producer.Producer != want || !producer.Attached || producer.Calls != 0 || producer.Transactions != 0 ||
			terminal && (producer.Closed || !producer.TerminalFencedEOF || producer.TerminalPhase != 8) ||
			!terminal && (!producer.Closed || producer.TerminalFencedEOF || producer.TerminalPhase != 0) {
			return false
		}
	}
	var transactions, rows, maximum uint64
	for index, row := range snapshot.Store.Phases {
		bounds := plan.WorkEnvelope.Phases[index]
		if row.Phase != uint32(index+1) || index > 0 && snapshot.Store.Phases[index-1].Phase >= row.Phase ||
			row.Transactions > math.MaxUint64-transactions || row.Rows > math.MaxUint64-rows ||
			row.Transactions > bounds.StoreTransactions.Maximum || row.Rows > bounds.StoreRows.Maximum ||
			row.MaximumRows > plan.WorkEnvelope.MaximumStoreRowsPerTransaction ||
			validateTotalAgainstMeasuredMaximum("store rows", row.Rows, row.Transactions, row.MaximumRows) != nil {
			return false
		}
		transactions += row.Transactions
		rows += row.Rows
		maximum = max(maximum, row.MaximumRows)
		out.Metrics[index].StoreTransactions = CountMetric(row.Transactions)
		out.Metrics[index].StoreRows = CountMetric(row.Rows)
		out.Metrics[index].MaxRowsTransaction = CountMetric(row.MaximumRows)
		out.Coverage[index].Store = true
	}
	return transactions == snapshot.Store.Transactions && rows == snapshot.Store.Rows && maximum == snapshot.Store.MaximumRows
}

func composeExecutionInspectionMetrics(plan Plan, rows []ExecutionPhaseInspection, out *executionReceiptMetrics) bool {
	wantPhase := [...]uint32{2, 3, 4, 5, 6, 7, 8, 8, 9, 10, 11, 12, 13, 14}
	wantEpoch := [...]uint64{1, 1, 1, 2, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5}
	if len(rows) != len(wantPhase) || len(plan.Revisions.Logical) != 3 {
		return false
	}
	var epoch, next uint64
	for offset, row := range rows {
		index := int(wantPhase[offset] - 1)
		predecessor := offset == 6
		if row.Phase != plan.PhaseOrder[index] || row.ServerEpoch != wantEpoch[offset] ||
			predecessor && (row.SelectorAccepted || row.Final != nil || row.LogicalChanges != (ExecutionLogicalChangeObservation{})) ||
			!predecessor && (!row.SelectorAccepted || row.Final == nil || row.Final.Projection.Phase != row.Phase) ||
			row.NextOrdinal <= row.FirstOrdinal ||
			row.AcceptedReports == 0 || row.AcceptedReports != row.NextOrdinal-row.FirstOrdinal ||
			!predecessor && (row.Final.Ordinal < row.FirstOrdinal || row.Final.Ordinal >= row.NextOrdinal) {
			return false
		}
		if row.Reads.StoreWriteAttempts != 0 {
			return false
		}
		if row.ServerEpoch != epoch {
			epoch, next = row.ServerEpoch, 1
		}
		if row.FirstOrdinal != next {
			return false
		}
		next = row.NextOrdinal
		controls, err := checkedInspectionReadSum(row.Reads.ControlFileReads, row.Reads.StoreReadAttempts,
			uint64(out.Metrics[index].ControlReads))
		members, memberErr := checkedInspectionReadSum(row.Reads.MemberVisits, uint64(out.Metrics[index].MemberReads))
		bounds := plan.WorkEnvelope.Phases[index]
		if err != nil || memberErr != nil || controls > bounds.ControlReads.Maximum || members > bounds.MemberReads.Maximum {
			return false
		}
		if !predecessor && !composeExecutionLogicalChangeMetric(plan, row, index, &out.Metrics[index]) {
			return false
		}
		out.Metrics[index].ControlReads = CountMetric(controls)
		out.Metrics[index].MemberReads = CountMetric(members)
		if !predecessor {
			out.Coverage[index].Inspection = true
		}
	}
	return true
}

func composeExecutionLogicalChangeMetric(plan Plan, row ExecutionPhaseInspection, index int, metrics *ReceiptMetrics) bool {
	logicalIndex := slices.Index([]string{"physical_delta_b", "logical_delta_b", "return_a"}, row.Phase)
	if logicalIndex < 0 {
		return row.LogicalChanges == (ExecutionLogicalChangeObservation{})
	}
	priorIndex := 0
	if logicalIndex == 2 {
		priorIndex = 1
	}
	want := plan.Revisions.Logical[logicalIndex]
	if !row.LogicalChanges.Complete || row.LogicalChanges.Prior != plan.Revisions.Logical[priorIndex].CatalogSource ||
		row.LogicalChanges.Current != want.CatalogSource ||
		row.LogicalChanges.ChangedAcceptedServices != want.ChangedLogicalServices || index < 0 || index >= len(plan.WorkEnvelope.Phases) ||
		row.LogicalChanges.ChangedAcceptedServices > plan.WorkEnvelope.Phases[index].ChangedLogicalServices.Maximum {
		return false
	}
	metrics.ChangedLogicalServices = CountMetric(row.LogicalChanges.ChangedAcceptedServices)
	return true
}

func composeExecutionWorkspaceMetrics(plan Plan, work executionJoinedWork, out *executionReceiptMetrics) bool {
	for _, record := range work.Records {
		observation := record.Attempts.WorkspaceBytes
		if !observation.Bound || !observation.Complete || observation.Unavailable || observation.LimitExceeded {
			return false
		}
		owned := [15]bool{}
		for _, phase := range executionProducerPhases(record.Producer) {
			index := phase - 1
			owned[index] = true
			row := observation.Phases[index]
			if row.Attempts == 0 || row.Completed != row.Attempts || row.Maximum.LogicalBytes == 0 || row.Maximum.AllocatedBytes == 0 ||
				row.Maximum.LogicalBytes > plan.WorkEnvelope.MaximumDataLogicalBytes ||
				row.Maximum.AllocatedBytes > plan.SafetyEnvelope.MaximumDataAllocatedBytes {
				return false
			}
			out.Metrics[index].DataLogicalBytes = Bytes(max(uint64(out.Metrics[index].DataLogicalBytes), row.Maximum.LogicalBytes))
			out.Metrics[index].DataAllocatedBytes = Bytes(max(uint64(out.Metrics[index].DataAllocatedBytes), row.Maximum.AllocatedBytes))
			out.Metrics[index].AllocationMeasurementAvailable = true
			out.Coverage[index].Workspace = true
		}
		for index, row := range observation.Phases {
			if !owned[index] && row != (ExecutionWorkspaceBytePhase{}) {
				return false
			}
		}
	}
	return true
}

func composeExecutionNativeMetrics(plan Plan, values []ExecutionServerProcessObservation, out *executionReceiptMetrics) bool {
	want := [][]uint32{{2, 3, 4}, {5}, {6, 7, 8}, {8, 9, 10, 11}, {12, 13, 14}}
	if len(values) != len(want) {
		return false
	}
	var phaseEight ProcessObservation
	for valueIndex, value := range values {
		if !value.Joined || value.RSSLimitExceeded || len(value.Phases) != len(want[valueIndex]) {
			return false
		}
		for phaseIndex, row := range value.Phases {
			if row.Phase != want[valueIndex][phaseIndex] || validateNativeObservation(row.Observation) != nil || !row.Observation.Available ||
				row.Observation.ObservedRSSHighWaterBytes > plan.SafetyEnvelope.MaximumPeakRSSBytes {
				return false
			}
			if row.Phase == 8 && valueIndex == 2 {
				phaseEight = row.Observation
				continue
			}
			if row.Phase == 8 && !executionNativeSupersedes(row.Observation, phaseEight) {
				return false
			}
			index := row.Phase - 1
			observation := row.Observation
			observation.Classes = slices.Clone(observation.Classes)
			out.Native[index] = observation
			out.Metrics[index].ObservedRSSHighWaterBytes = Bytes(observation.ObservedRSSHighWaterBytes)
			out.Metrics[index].NativeMeasurementAvailable = true
			out.Coverage[index].Native = true
		}
	}
	return true
}

func executionNativeSupersedes(next, prior ProcessObservation) bool {
	if !prior.Available || !next.Available || next.CompletedCensuses <= prior.CompletedCensuses ||
		next.ObservedDescendantsHighWater < prior.ObservedDescendantsHighWater ||
		next.ObservedRSSHighWaterBytes < prior.ObservedRSSHighWaterBytes || len(next.Classes) != len(prior.Classes) {
		return false
	}
	for index, class := range prior.Classes {
		if next.Classes[index].Class != class.Class || next.Classes[index].ObservedHighWater < class.ObservedHighWater {
			return false
		}
	}
	return true
}

func validateExecutionJoinedMetricBounds(plan Plan, out executionReceiptMetrics) bool {
	for index, coverage := range out.Coverage {
		if !coverage.Producer {
			continue
		}
		metrics, bounds := out.Metrics[index], plan.WorkEnvelope.Phases[index]
		for _, value := range boundedPhaseMetricValues(metrics, bounds) {
			if executionJoinedMetric(value.name) && value.value > value.bound.Maximum {
				return false
			}
		}
		if uint64(metrics.MaxRetriesUnit) > plan.WorkEnvelope.MaximumRetriesPerUnit ||
			uint64(metrics.MaxLifecycleDeletesTurn) > plan.WorkEnvelope.MaximumLifecycleDeletesPerTurn ||
			metrics.CacheRootValidations != metrics.CacheRootReads || metrics.CacheMemberValidations != metrics.CacheMemberReads ||
			uint64(metrics.CacheRootReads) > math.MaxUint64-uint64(metrics.CacheMemberReads) ||
			uint64(metrics.CacheMisses) != uint64(metrics.CacheRootReads)+uint64(metrics.CacheMemberReads) ||
			uint64(metrics.CacheHits) > math.MaxUint64-uint64(metrics.CacheMisses) ||
			uint64(metrics.CacheLookups) != uint64(metrics.CacheHits)+uint64(metrics.CacheMisses) ||
			validateTotalAgainstMeasuredMaximum("retries", uint64(metrics.Retries), uint64(metrics.JobAttempts), uint64(metrics.MaxRetriesUnit)) != nil ||
			validateTotalAgainstMeasuredMaximum("lifecycle deletion", uint64(metrics.LifecycleDeleted), uint64(metrics.LifecycleOwnerTurns), uint64(metrics.MaxLifecycleDeletesTurn)) != nil {
			return false
		}
	}
	return true
}

func executionJoinedMetric(name string) bool {
	switch name {
	case "git_reads", "census_children", "census_records", "index_files", "observation_parses",
		"source_logical_bytes", "source_unique_bytes", "changed_logical_services", "job_attempts", "cache_root_reads", "cache_member_reads",
		"cache_lookups", "publication_writes", "relationship_build_attempts", "lifecycle_owner_turns",
		"lifecycle_deleted", "cache_hits", "cache_misses", "relationship_projections", "resolver_blob_bytes",
		"resolver_blob_reads", "reuse_decisions", "service_references", "unsupported_source_files":
		return true
	default:
		return false
	}
}
