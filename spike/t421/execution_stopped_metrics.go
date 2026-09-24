package t421

import (
	"math"
	"slices"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// A truthful joined prefix, including positive crossings and incomplete rows.
// Native/disk/timing and parent workspace remain their independent owners.
// Unavailable is per phase; it never grants receipt acceptance by itself.
type executionStoppedMetricPrefix struct {
	Metrics     executionReceiptMetrics
	Unavailable [15][]string
}

func (out *executionStoppedMetricPrefix) unavailable(index int, names ...string) {
	out.Unavailable[index] = append(out.Unavailable[index], names...)
	slices.Sort(out.Unavailable[index])
	out.Unavailable[index] = slices.Compact(out.Unavailable[index])
}

func composeExecutionStoppedMetricPrefix(plan Plan, stoppedPhase string, work executionJoinedWork, dispatch dispatchadmission.Snapshot, store storeaccounting.WireSnapshot, inspection []ExecutionPhaseInspection) (executionStoppedMetricPrefix, error) {
	var out executionStoppedMetricPrefix
	stop := slices.Index(plan.PhaseOrder, stoppedPhase)
	if !processAccountingPlanSemantics(plan.Schema) || stop < 0 || len(plan.PhaseOrder) != 15 || len(plan.WorkEnvelope.Phases) != 15 || len(inspection) > 14 {
		return out, errExecutionReceiptMetrics
	}
	var accepted [15]bool
	for _, row := range inspection {
		index := slices.Index(plan.PhaseOrder, row.Phase)
		if index < 0 || index > stop {
			return out, errExecutionReceiptMetrics
		}
		accepted[index] = accepted[index] || row.SelectorAccepted
	}
	var workComplete [15]bool
	for index := 1; index <= min(stop, 13); index++ {
		workComplete[index] = true
	}
	for slot, producer := range []uint32{2, 3, 4, 5, 6, 10, 11} {
		record := work.Records[slot]
		present := record.Producer == producer && record.Input != ([32]byte{})
		if record.Producer != 0 && !present {
			return out, errExecutionReceiptMetrics
		}
		for _, phase := range executionProducerPhases(producer) {
			index := int(phase - 1)
			if present && !addExecutionWorkRecordMetrics(&out.Metrics.Metrics[index], record, index) {
				return out, errExecutionReceiptMetrics
			}
			if index > stop {
				if out.Metrics.Metrics[index] != (ReceiptMetrics{}) {
					return out, errExecutionReceiptMetrics
				}
				continue
			}
			phaseAccepted := false
			for _, row := range inspection {
				if row.Phase == plan.PhaseOrder[index] && row.ServerEpoch == uint64(producer-1) && row.SelectorAccepted {
					phaseAccepted = true
				}
			}
			complete := present && record.Joined && record.SessionEmpty && (validExecutionJoinedWorkRecord(plan, record) || phaseAccepted && executionWorkPhaseClosed(plan, record, index)) && work.Err == nil
			if !complete {
				workComplete[index] = false
				for groupIndex, group := range workUnavailableMetricGroups {
					if groupIndex != 9 {
						out.unavailable(index, group...)
					}
				}
			}
			// V5 cleanup reads share the scoped control-read metric but come
			// from this native prefix, not the independent HTTP read ledger.
			if plan.Schema == PlanV5Schema {
				if bound, applies := SelectorHandoffCleanupForPhase(plan, plan.PhaseOrder[index]); applies &&
					uint64(producer) == bound.ServerEpoch+1 && !executionHandoffPhaseClosed(plan, record, index) {
					out.unavailable(index, "control_reads")
				}
			}
			if present {
				row := record.Attempts.WorkspaceBytes.Phases[index]
				if row.Completed > 0 {
					out.Metrics.Metrics[index].DataLogicalBytes = max(out.Metrics.Metrics[index].DataLogicalBytes, Bytes(row.Maximum.LogicalBytes))
					out.Metrics.Metrics[index].DataAllocatedBytes = max(out.Metrics.Metrics[index].DataAllocatedBytes, Bytes(row.Maximum.AllocatedBytes))
				}
				workspaceClosed := complete && record.Attempts.WorkspaceBytes.Bound && row.Attempts > 0 && row.Completed == row.Attempts
				if workspaceClosed {
					out.Metrics.Coverage[index].Workspace = true
					out.Metrics.Metrics[index].AllocationMeasurementAvailable = true
				} else {
					out.unavailable(index, "data_logical_bytes", "data_allocated_bytes")
				}
			} else {
				out.unavailable(index, "data_logical_bytes", "data_allocated_bytes")
			}
		}
	}
	for index, complete := range workComplete {
		out.Metrics.Coverage[index].Producer = complete
	}
	if !composeStoppedDispatchPrefix(plan, dispatch, accepted, stop, &out) || !composeStoppedStorePrefix(store, accepted, stop, &out) {
		return out, errExecutionReceiptMetrics
	}
	var inspectionSeen [15]bool
	for _, row := range inspection {
		index := slices.Index(plan.PhaseOrder, row.Phase)
		if row.NextOrdinal < row.FirstOrdinal || row.AcceptedReports != row.NextOrdinal-row.FirstOrdinal || row.Reads.StoreWriteAttempts != 0 {
			return out, errExecutionReceiptMetrics
		}
		metric := &out.Metrics.Metrics[index]
		if !addExecutionJoinedMetric(&metric.ControlReads, row.Reads.ControlFileReads) || !addExecutionJoinedMetric(&metric.ControlReads, row.Reads.StoreReadAttempts) || !addExecutionJoinedMetric(&metric.MemberReads, row.Reads.MemberVisits) {
			return out, errExecutionReceiptMetrics
		}
		inspectionSeen[index] = true
		if row.LogicalChanges.Complete {
			metric.ChangedLogicalServices = CountMetric(row.LogicalChanges.ChangedAcceptedServices)
		}
	}
	for index := 1; index <= min(stop, 13); index++ {
		out.Metrics.Coverage[index].Inspection = inspectionSeen[index] && accepted[index]
		if !out.Metrics.Coverage[index].Inspection {
			out.unavailable(index, workUnavailableMetricGroups[9]...)
		}
	}
	for index, unavailable := range out.Unavailable {
		if slices.Contains(unavailable, "data_logical_bytes") {
			out.Metrics.Coverage[index].Workspace = false
			out.Metrics.Metrics[index].AllocationMeasurementAvailable = false
		}
	}
	return out, nil
}

// A successfully accepted selector is the real phase quiescence fence. Joined
// clean EOF establishes every emitted synchronous report was decoded; each
// family's phase rows must independently close. Future unentered phases need
// no fabricated reuse/unsupported-source observations.
func executionWorkPhaseClosed(plan Plan, record executionJoinedWorkRecord, index int) bool {
	a := record.Attempts
	if !executionHandoffPhaseClosed(plan, record, index) || !a.ScanComplete || !a.SourceBound || !a.ObservationBound || !a.PublicationBound || !a.ResolverBound || !a.RelationshipBound || !a.Cache.Bound || !a.SourceCensus.Bound || !a.CatalogCensus.Bound {
		return false
	}
	c := a.Cache.Phases[index]
	if c.RootReads != c.RootValidations || c.MemberReads != c.MemberValidations || c.Hits > math.MaxUint64-c.Misses || c.Lookups != c.Hits+c.Misses || c.RootReads > math.MaxUint64-c.MemberReads || c.Misses != c.RootReads+c.MemberReads {
		return false
	}
	v := a.Phases[index]
	if v.Retries > v.JobAttempts || v.SourceUniqueBytes > v.SourceLogicalBytes || a.SourceCensus.Started[index] != a.SourceCensus.Finished[index] || a.SourceCensus.Succeeded[index] > a.SourceCensus.Finished[index] || a.CatalogCensus.Started[index] != a.CatalogCensus.Finished[index] || a.CatalogCensus.ClosedChildren[index] != v.CensusChildren {
		return false
	}
	if record.Producer >= 10 {
		return a.Complete
	}
	ix := record.IndexOffers.Phases[index]
	life := a.Lifecycle.Phases[index]
	return a.AttemptBound && a.Lifecycle.Bound && a.Reuse.Bound && a.UnsupportedSource.Bound && validExecutionJoinedReusePhase(a.Reuse.Phases[index]) && v.UnsupportedSourceFiles == 0 &&
		(plan.WorkEnvelope.Phases[index].ObservationParses.Maximum == 0 || a.UnsupportedSource.Reports[index] > 0) &&
		record.IndexOffers.ScanComplete && record.IndexOffers.Bound && ix.StartedChildren == ix.EndedChildren && ix.FailedChildren <= ix.EndedChildren && ix.Offers == ix.SettledOffers && (ix.Offers == 0 || ix.StartedChildren > 0) &&
		life.FailedTicks <= life.ReturnedTicks && life.OwnerTurns <= life.ReturnedTicks && validateTotalAgainstMeasuredMaximum("retries", v.Retries, v.JobAttempts, v.MaxRetriesUnit) == nil && validateTotalAgainstMeasuredMaximum("lifecycle", life.Deleted, life.OwnerTurns, life.MaxDeleted) == nil
}

func composeStoppedStorePrefix(snapshot storeaccounting.WireSnapshot, accepted [15]bool, stop int, out *executionStoppedMetricPrefix) bool {
	seen := [15]bool{}
	var transactions, rows, maximum uint64
	for _, row := range snapshot.Store.Phases {
		if row.Phase == 0 || row.Phase > 15 || seen[row.Phase-1] || row.Transactions > math.MaxUint64-transactions || row.Rows > math.MaxUint64-rows {
			return false
		}
		index := int(row.Phase - 1)
		seen[index] = true
		transactions += row.Transactions
		rows += row.Rows
		maximum = max(maximum, row.MaximumRows)
		metric := &out.Metrics.Metrics[index]
		metric.StoreRows = CountMetric(row.Rows)
		metric.StoreTransactions = CountMetric(row.Transactions)
		metric.MaxRowsTransaction = CountMetric(row.MaximumRows)
		complete := snapshot.PrefixesClosed && snapshot.Store.PrefixesClosed || index < stop && accepted[index] && snapshot.Store.Phase > row.Phase
		out.Metrics.Coverage[index].Store = complete
	}
	if transactions != snapshot.Store.Transactions || rows != snapshot.Store.Rows || maximum != snapshot.Store.MaximumRows {
		return false
	}
	for index := 0; index <= stop; index++ {
		if !seen[index] || !out.Metrics.Coverage[index].Store {
			out.unavailable(index, storeUnavailableMetricNames...)
		}
	}
	return true
}

func composeStoppedDispatchPrefix(plan Plan, snapshot dispatchadmission.Snapshot, accepted [15]bool, stop int, out *executionStoppedMetricPrefix) bool {
	seen := [15]bool{}
	var total uint64
	for _, row := range snapshot.Phases {
		if row.Phase == 0 || row.Phase > 15 || seen[row.Phase-1] || row.Attempts > math.MaxUint64-total {
			return false
		}
		index := int(row.Phase - 1)
		seen[index] = true
		total += row.Attempts
		roles := [8]uint64{}
		roleSeen := [8]bool{}
		var sum uint64
		for _, role := range row.Roles {
			if role.Role == 0 || role.Role >= 8 || roleSeen[role.Role] || role.Attempts > math.MaxUint64-sum {
				return false
			}
			roleSeen[role.Role] = true
			roles[role.Role] = role.Attempts
			sum += role.Attempts
		}
		if sum != row.Attempts {
			return false
		}
		complete := (snapshot.Complete || index < stop && accepted[index]) && len(row.Roles) == 7
		measurement := DispatchAccountingMeasurement{Schema: DispatchMeasurementSchema, Complete: complete}
		for _, name := range plan.WorkEnvelope.ControlledDispatchRoles {
			role := executionDispatchRole(name)
			if role == 0 && name != "phebs-focused-index" {
				return false
			}
			measurement.Roles = append(measurement.Roles, Count{Name: name, Count: roles[role]})
		}
		out.Metrics.Dispatch[index] = measurement
		out.Metrics.Metrics[index].ControlledDispatchAttempts = CountMetric(row.Attempts)
		out.Metrics.Metrics[index].DispatchMeasurementAvailable = complete
		out.Metrics.Coverage[index].Dispatch = complete
	}
	if total != snapshot.Attempts {
		return false
	}
	for index := 0; index <= stop; index++ {
		if !seen[index] || !out.Metrics.Coverage[index].Dispatch {
			out.unavailable(index, "controlled_dispatch_attempts")
		}
	}
	return true
}
