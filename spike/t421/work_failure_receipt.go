package t421

import "slices"

// These are observation coverage groups, not new work units or limits. A lost
// shared output scan reports the union of its families; a zero prefix remains
// explicitly incomplete. Scoped inspection reads have their own separate pair.
var workUnavailableMetricGroups = [][]string{
	{"job_attempts", "max_retries_on_any_unit", "retries"},
	{"git_reads"},
	{"observation_parses"},
	{"cache_hits", "cache_lookups", "cache_member_reads", "cache_member_validations", "cache_misses", "cache_root_reads", "cache_root_validations"},
	{"lifecycle_deleted", "lifecycle_owner_turns", "max_lifecycle_deletes_in_any_turn"},
	{"publication_writes"},
	{"resolver_blob_bytes", "resolver_blob_reads"},
	{"relationship_build_attempts", "relationship_projections", "service_references"},
	{"index_files"},
	{"control_reads", "member_reads"},
	{"source_logical_bytes", "source_unique_bytes"},
}

func workUnavailableMetric(name string) bool {
	for _, group := range workUnavailableMetricGroups {
		if slices.Contains(group, name) {
			return true
		}
	}
	return false
}

func hasUnavailableWorkMetrics(names []string) bool {
	return slices.ContainsFunc(names, workUnavailableMetric)
}

func validWorkUnavailableGroups(names []string) bool {
	for _, group := range workUnavailableMetricGroups {
		count := 0
		for _, name := range group {
			if slices.Contains(names, name) {
				count++
			}
		}
		if count != 0 && count != len(group) {
			return false
		}
	}
	return true
}

// Primary resource/topology evidence may coexist with incomplete work/store
// prefixes. Gauge and dispatch availability retain their separate contracts.
func validSecondaryUnavailableMetrics(names []string) bool {
	if len(names) == 0 || !validUnavailableMetricsForPlan(names, PlanV3Schema) {
		return false
	}
	for _, name := range names {
		if !workUnavailableMetric(name) && !slices.Contains(storeUnavailableMetricNames, name) {
			return false
		}
	}
	return true
}

func v3WorkMetric(name string) bool {
	for _, value := range boundedPhaseMetricValues(ReceiptMetrics{}, PhaseWorkBounds{}) {
		if name == value.name {
			return name != "store_rows" && name != "store_transactions"
		}
	}
	return name == "max_retries_on_any_unit" || name == "max_lifecycle_deletes_in_any_turn"
}

func workCounterObservationMatches(value FailureObservation, metric string, limit, observed uint64, v3 bool) bool {
	return counterObservationMatches(value, metric, limit, observed) || v3 && v3WorkMetric(metric) &&
		crossingObservationMatches(value, metric, limit, observed)
}

func measuredV3WorkCrossing(metrics ReceiptMetrics, bounds PhaseWorkBounds, envelope WorkEnvelope) bool {
	for _, value := range boundedPhaseMetricValues(metrics, bounds) {
		if value.name != "store_rows" && value.name != "store_transactions" && value.value > value.bound.Maximum {
			return true
		}
	}
	return uint64(metrics.MaxRetriesUnit) > envelope.MaximumRetriesPerUnit ||
		uint64(metrics.MaxLifecycleDeletesTurn) > envelope.MaximumLifecycleDeletesPerTurn
}

// This admits only a failed measurement, never further work or a PASS. The
// primary observation is independently matched to its retained metric and
// frozen limit by stopped-failure validation. Other positive work crossings
// survive too: joined producer prefixes establish no global first-event order.
// Store/dispatch submission admission keeps its existing specialised proof.
func retainsV3WorkCrossing(name, outcome string, observation *FailureObservation, envelope WorkEnvelope) bool {
	if envelope.Schema != WorkEnvelopeV3Schema || outcome != "stopped" || observation == nil || !v3WorkMetric(name) {
		return false
	}
	switch observation.Kind {
	case "counter_crossing":
		return v3WorkMetric(observation.Metric) || observation.Metric == "materialized_cartesian_owner_pairs"
	case "counter_limit":
		return v3WorkMetric(observation.Metric) || observation.Metric == "direct_recovery_topology_limits"
	case "gauge_limit":
		return slices.Contains([]string{"observed_rss_high_water_bytes", "data_allocated_bytes", "data_logical_bytes", "total_wall_ms", "phase_wall_ms", "multiple_resource_ceilings"}, observation.Metric)
	default:
		return false
	}
}
