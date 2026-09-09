package t421

import (
	"math"
	"reflect"
	"slices"
	"testing"
)

func workTestUnavailable(groups ...int) []string {
	var names []string
	for _, group := range groups {
		names = append(names, workUnavailableMetricGroups[group]...)
	}
	slices.Sort(names)
	return names
}

func workTestFailure(t *testing.T, plan Plan, phase, metric string, limit, observed uint64, unavailable []string) ReceiptFailure {
	t.Helper()
	failure := ReceiptFailure{Phase: phase, Class: "resource", Code: "phase_work_limit", Observation: FailureObservation{
		Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "counter_crossing", Metric: metric, Limit: limit,
		Observed: observed, UnavailableMetrics: slices.Clone(unavailable),
	}}
	failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
	return failure
}

func TestWorkFailureUnavailableGroups(t *testing.T) {
	for index, group := range workUnavailableMetricGroups {
		if !slices.IsSorted(group) || !validUnavailableMetricsForPlan(group, PlanV3Schema) {
			t.Fatalf("invalid complete group %d", index)
		}
		for _, schema := range []string{PlanSchema, PlanV2Schema} {
			if validUnavailableMetricsForPlan(group, schema) {
				t.Fatalf("historical schema admitted group %d", index)
			}
		}
		if len(group) > 1 && validUnavailableMetricsForPlan(group[:len(group)-1], PlanV3Schema) {
			t.Fatalf("partial group %d admitted", index)
		}
	}
	sharedScan := workTestUnavailable(0, 1, 2, 3, 4, 5, 6, 7, 8, 10)
	if !validUnavailableMetricsForPlan(sharedScan, PlanV3Schema) || !validSecondaryUnavailableMetrics(sharedScan) {
		t.Fatal("shared scan loss refused")
	}
	for _, names := range [][]string{
		append(slices.Clone(sharedScan), sharedScan[len(sharedScan)-1]),
		append(slices.Clone(sharedScan), "unknown_work"),
		{"resolver_blob_reads", "resolver_blob_bytes"},
		{"cache_lookups"},
		{"control_reads"},
		{"source_logical_bytes"},
		{"source_unique_bytes"},
	} {
		if validUnavailableMetricsForPlan(names, PlanV3Schema) {
			t.Fatalf("invalid inventory admitted: %v", names)
		}
	}
	withStore := append(slices.Clone(sharedScan), storeUnavailableMetricNames...)
	slices.Sort(withStore)
	if !validSecondaryUnavailableMetrics(withStore) {
		t.Fatal("independent store and shared scan prefixes refused")
	}
	if validSecondaryUnavailableMetrics([]string{"observed_rss_high_water_bytes"}) || validSecondaryUnavailableMetrics(nil) {
		t.Fatal("secondary gauge or empty inventory gained a new contract")
	}
}

func TestWorkFailureCachePrefixCoherence(t *testing.T) {
	plan := accountingTestPlan(t)
	phase := "product_queries"
	base := accountingTestMeasurement(plan, phase)
	bounds := plan.WorkEnvelope.Phases[slices.Index(plan.PhaseOrder, phase)]
	base.Metrics.CacheLookups, base.Metrics.CacheMisses, base.Metrics.CacheRootReads = 1, 1, 1
	failure := FailureObservation{Kind: "measurement_unavailable", UnavailableMetrics: workTestUnavailable(3)}
	for _, test := range []struct {
		name    string
		outcome string
		mutate  func(*ReceiptMetrics, *FailureObservation, *WorkEnvelope)
		want    bool
	}{
		{"pending_validation", "stopped", func(*ReceiptMetrics, *FailureObservation, *WorkEnvelope) {}, true},
		{"completed_validation", "stopped", func(m *ReceiptMetrics, _ *FailureObservation, _ *WorkEnvelope) { m.CacheRootValidations = 1 }, true},
		{"validation_without_read", "stopped", func(m *ReceiptMetrics, _ *FailureObservation, _ *WorkEnvelope) { m.CacheRootValidations = 2 }, false},
		{"lost_lookup_identity", "stopped", func(m *ReceiptMetrics, _ *FailureObservation, _ *WorkEnvelope) { m.CacheLookups = 2 }, false},
		{"lost_miss_identity", "stopped", func(m *ReceiptMetrics, _ *FailureObservation, _ *WorkEnvelope) { m.CacheMisses = 0 }, false},
		{"unknown_coverage", "stopped", func(_ *ReceiptMetrics, f *FailureObservation, _ *WorkEnvelope) { f.UnavailableMetrics = nil }, false},
		{"partial_coverage_group", "stopped", func(_ *ReceiptMetrics, f *FailureObservation, _ *WorkEnvelope) {
			f.UnavailableMetrics = []string{"cache_lookups"}
		}, false},
		{"complete_pass", "passed", func(m *ReceiptMetrics, _ *FailureObservation, _ *WorkEnvelope) { m.CacheRootValidations = 1 }, false},
		{"failed_not_stopped", "failed", func(*ReceiptMetrics, *FailureObservation, *WorkEnvelope) {}, false},
		{"legacy", "stopped", func(_ *ReceiptMetrics, _ *FailureObservation, e *WorkEnvelope) {
			e.Schema = "t422-phase-work-envelope-v2"
		}, false},
		{"addition_overflow", "stopped", func(m *ReceiptMetrics, f *FailureObservation, _ *WorkEnvelope) {
			m.CacheRootReads, m.CacheMemberReads, m.CacheMisses, m.CacheLookups = math.MaxUint64, 1, 0, 0
			f.Kind, f.Metric = "counter_crossing", "cache_root_reads"
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, observation, envelope := base.Metrics, failure, plan.WorkEnvelope
			test.mutate(&value, &observation, &envelope)
			err := validatePhaseWorkMetrics(value, base.DispatchAccounting.Roles, bounds, test.outcome, &observation, envelope)
			if (err == nil) != test.want {
				t.Fatalf("valid=%t want=%t: %v", err == nil, test.want, err)
			}
		})
	}
}

// The existing full constructor fixture uses modeled external signature
// bindings. These tests exercise complete receipt validation and authenticated
// returned-byte binding, not a new issuer, native run or ceremony signature.
func TestWorkFailureFullReceiptRoundTrip(t *testing.T) {
	plan := clonePlan(t, correctedTestPlan(t))
	if err := applyProcessAccountingCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	binding := frozenReceiptTestBinding(t, plan)
	base := completeTestReceipt(t, plan, binding)
	phase := "product_queries"
	index := slices.Index(plan.PhaseOrder, phase)
	bounds := plan.WorkEnvelope.Phases[index]
	for _, scenario := range []string{
		"resolver_atomic_crossings", "resolver_batch_with_cache_prefix", "lateral_crossings", "reference_batch", "scoped_read_batch", "cache_atomic_crossings",
		"lifecycle_atomic_crossings", "shared_scan_positive_prefix", "shared_scan_zero_prefix",
		"resource_with_work", "resource_with_work_and_unavailable", "typed_internal_scan_failure",
		"logical_with_work", "logical_with_topology", "logical_multiple_resource",
	} {
		t.Run(scenario, func(t *testing.T) {
			value := cloneTestReceipt(t, base)
			measurement := &value.Measurements[index]
			metrics := &measurement.Metrics
			failure := workTestFailure(t, plan, phase, "resolver_blob_bytes", bounds.ResolverBlobBytes.Maximum, bounds.ResolverBlobBytes.Maximum+73, nil)
			switch scenario {
			case "logical_with_work", "logical_with_topology", "logical_multiple_resource":
				metrics.DataLogicalBytes = Bytes(plan.WorkEnvelope.MaximumDataLogicalBytes + 17)
				switch scenario {
				case "logical_with_work":
					metrics.ResolverBlobBytes = Bytes(bounds.ResolverBlobBytes.Maximum + 73)
				case "logical_with_topology":
					metrics.MaterializedOwnerPairs = 1
					failure.Class, failure.Code = "topology", "materialized_cartesian_owner_pairs_nonzero"
					failure.Observation.Metric, failure.Observation.Limit, failure.Observation.Observed = "materialized_cartesian_owner_pairs", 0, 1
				case "logical_multiple_resource":
					metrics.ObservedRSSHighWaterBytes = Bytes(plan.SafetyEnvelope.MaximumPeakRSSBytes + 100)
					measurement.NativeObservation.ObservedRSSHighWaterBytes = uint64(metrics.ObservedRSSHighWaterBytes)
					failure.Code, failure.Observation.Kind, failure.Observation.Metric = "multiple_resource_ceilings", "gauge_limit", "multiple_resource_ceilings"
					failure.Observation.Limit, failure.Observation.Observed = 0, 1
				}
			case "resolver_atomic_crossings", "resolver_batch_with_cache_prefix", "lateral_crossings":
				metrics.ResolverBlobReads, metrics.ResolverBlobBytes = CountMetric(bounds.ResolverBlobReads.Maximum+1), Bytes(bounds.ResolverBlobBytes.Maximum+73)
				if scenario == "resolver_batch_with_cache_prefix" {
					metrics.CacheLookups, metrics.CacheMisses, metrics.CacheRootReads = 1, 1, 1
					metrics.CacheHits, metrics.CacheMemberReads, metrics.CacheRootValidations, metrics.CacheMemberValidations = 0, 0, 0, 0
					failure.Observation.UnavailableMetrics = workTestUnavailable(3, 6)
				}
				if scenario == "lateral_crossings" {
					metrics.ServiceReferences = CountMetric(bounds.ServiceReferences.Maximum + 19)
				}
			case "reference_batch":
				metrics.ServiceReferences = CountMetric(bounds.ServiceReferences.Maximum + 19)
				failure = workTestFailure(t, plan, phase, "service_references", bounds.ServiceReferences.Maximum, uint64(metrics.ServiceReferences), nil)
			case "scoped_read_batch":
				metrics.ControlReads, metrics.MemberReads = CountMetric(bounds.ControlReads.Maximum+7), CountMetric(bounds.MemberReads.Maximum+83)
				failure = workTestFailure(t, plan, phase, "member_reads", bounds.MemberReads.Maximum, uint64(metrics.MemberReads), nil)
			case "cache_atomic_crossings":
				// Both load lanes were exactly at their limits. One native R
				// event increments root reads, misses and lookups together;
				// its subsequent validation has not returned.
				metrics.CacheRootReads, metrics.CacheMemberReads = CountMetric(bounds.CacheRootReads.Maximum+1), CountMetric(bounds.CacheMemberReads.Maximum)
				metrics.CacheLookups, metrics.CacheMisses = metrics.CacheRootReads+metrics.CacheMemberReads, metrics.CacheRootReads+metrics.CacheMemberReads
				metrics.CacheHits, metrics.CacheRootValidations, metrics.CacheMemberValidations = 0, metrics.CacheRootReads-1, metrics.CacheMemberReads
				failure = workTestFailure(t, plan, phase, "cache_root_reads", bounds.CacheRootReads.Maximum, uint64(metrics.CacheRootReads), workTestUnavailable(3))
			case "lifecycle_atomic_crossings":
				metrics.LifecycleOwnerTurns = CountMetric(bounds.LifecycleOwnerTurns.Maximum + 1)
				metrics.LifecycleDeleted = CountMetric(max(bounds.LifecycleDeleted.Maximum, plan.WorkEnvelope.MaximumLifecycleDeletesPerTurn) + 7)
				metrics.MaxLifecycleDeletesTurn = metrics.LifecycleDeleted
				failure = workTestFailure(t, plan, phase, "lifecycle_deleted", bounds.LifecycleDeleted.Maximum, uint64(metrics.LifecycleDeleted), workTestUnavailable(4))
			case "shared_scan_positive_prefix", "shared_scan_zero_prefix", "typed_internal_scan_failure":
				metrics.CacheLookups, metrics.CacheMisses, metrics.CacheRootReads = 1, 1, 1
				metrics.CacheHits, metrics.CacheMemberReads, metrics.CacheRootValidations, metrics.CacheMemberValidations = 0, 0, 0, 0
				if scenario == "shared_scan_zero_prefix" {
					metrics.CacheLookups, metrics.CacheMisses, metrics.CacheRootReads = 0, 0, 0
				}
				failure = ReceiptFailure{Phase: phase, Class: "internal", Code: "measurement_unavailable", Observation: FailureObservation{
					Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "measurement_unavailable",
					UnavailableMetrics: workTestUnavailable(0, 1, 2, 3, 4, 5, 6, 7, 8),
				}}
				if scenario == "typed_internal_scan_failure" {
					failure.Code, failure.Observation.Kind, failure.Observation.Metric = "internal_error", "typed_error", "internal_error"
					failure.Evidence = &FailureEvidenceProjection{Schema: plan.ReceiptContract.FailureObservationSchema + "/public-projection-v1", Kind: "internal",
						Internal: &InternalFailureEvidence{Phase: phase, Stage: "joined_work_observation", ErrorClass: "incomplete", EventOrdinal: measurement.StartEventOrdinal + 1}}
					failure.Observation.ObservedSHA256 = mustReceiptSHA256(t, *failure.Evidence)
				}
			default:
				metrics.ResolverBlobReads, metrics.ResolverBlobBytes = CountMetric(bounds.ResolverBlobReads.Maximum+1), Bytes(bounds.ResolverBlobBytes.Maximum+73)
				metrics.ObservedRSSHighWaterBytes = Bytes(plan.SafetyEnvelope.MaximumPeakRSSBytes + 100)
				measurement.NativeObservation.ObservedRSSHighWaterBytes = uint64(metrics.ObservedRSSHighWaterBytes)
				failure = ReceiptFailure{Phase: phase, Class: "resource", Code: "observed_rss_ceiling", Observation: FailureObservation{
					Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "gauge_limit", Metric: "observed_rss_high_water_bytes",
					Limit: plan.SafetyEnvelope.MaximumPeakRSSBytes, Observed: uint64(metrics.ObservedRSSHighWaterBytes),
				}}
				if scenario == "resource_with_work_and_unavailable" {
					failure.Observation.UnavailableMetrics = workTestUnavailable(6)
				}
			}
			failure.Observation.EvidenceSHA256 = ""
			failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
			stopTestReceipt(t, &value, plan, phase, failure)
			if scenario == "logical_with_topology" {
				value.Decision.Selected, value.Decision.RulePriority = "p6_investigation", 1
			}
			returned := returnedPackageTestBinding(t, value, plan, binding)
			if err := ValidateReceipt(value, plan, binding, returned); err != nil {
				t.Fatal(err)
			}
			raw, err := MarshalCanonical(value)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeReceipt(raw, plan, binding, returned)
			if err != nil || !reflect.DeepEqual(value, decoded) {
				t.Fatalf("full failed-prefix roundtrip: %v", err)
			}
			// Altering a genuine positive prefix cannot survive returned binding.
			value.Measurements[index].Metrics.ServiceReferences++
			if ValidateReceipt(value, plan, binding, returned) == nil {
				t.Fatal("mutated returned prefix retained authentication")
			}
		})
	}
	for _, scenario := range []string{"wrong_primary_limit", "wrong_primary_value", "forged_pass", "partial_cache_coverage", "validation_exceeds_reads", "missing_cache_coverage", "extra_store_work"} {
		t.Run("refuse_"+scenario, func(t *testing.T) {
			value := cloneTestReceipt(t, base)
			metrics := &value.Measurements[index].Metrics
			metrics.ResolverBlobReads, metrics.ResolverBlobBytes = CountMetric(bounds.ResolverBlobReads.Maximum+1), Bytes(bounds.ResolverBlobBytes.Maximum+73)
			metrics.CacheLookups, metrics.CacheMisses, metrics.CacheRootReads = 1, 1, 1
			metrics.CacheHits, metrics.CacheMemberReads, metrics.CacheRootValidations, metrics.CacheMemberValidations = 0, 0, 0, 0
			failure := workTestFailure(t, plan, phase, "resolver_blob_bytes", bounds.ResolverBlobBytes.Maximum, uint64(metrics.ResolverBlobBytes), workTestUnavailable(3, 6))
			switch scenario {
			case "wrong_primary_limit":
				failure.Observation.Limit++
			case "wrong_primary_value":
				failure.Observation.Observed++
			case "partial_cache_coverage":
				failure.Observation.UnavailableMetrics = []string{"cache_lookups"}
			case "validation_exceeds_reads":
				metrics.CacheRootValidations = 2
			case "missing_cache_coverage":
				failure.Observation.UnavailableMetrics = workTestUnavailable(6)
			case "extra_store_work":
				metrics.StoreTransactions = CountMetric(bounds.StoreTransactions.Maximum + 1)
				metrics.StoreRows, metrics.MaxRowsTransaction = metrics.StoreTransactions, 1
			}
			failure.Observation.EvidenceSHA256 = ""
			failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
			stopTestReceipt(t, &value, plan, phase, failure)
			if scenario == "forged_pass" {
				value.PhaseResults[index].Outcome = "passed"
			}
			// Rebind the changed bytes: rejection must come from the contract,
			// not merely an intentionally stale returned-package digest.
			returned := returnedPackageTestBinding(t, value, plan, binding)
			if ValidateReceipt(value, plan, binding, returned) == nil {
				t.Fatal("invalid failed-prefix receipt passed complete validation")
			}
		})
	}
}

func TestWorkFailureCrossingGuards(t *testing.T) {
	plan := accountingTestPlan(t)
	phase := "product_queries"
	measurement := accountingTestMeasurement(plan, phase)
	bounds := plan.WorkEnvelope.Phases[slices.Index(plan.PhaseOrder, phase)]
	measurement.Metrics.ResolverBlobReads, measurement.Metrics.ResolverBlobBytes = 1, Bytes(bounds.ResolverBlobBytes.Maximum+31)
	failure := workTestFailure(t, plan, phase, "resolver_blob_bytes", bounds.ResolverBlobBytes.Maximum, uint64(measurement.Metrics.ResolverBlobBytes), nil)
	for _, test := range []struct {
		name   string
		mutate func(*ReceiptFailure, *Plan)
		want   bool
	}{
		{"full_batch", func(*ReceiptFailure, *Plan) {}, true},
		{"wrong_limit", func(f *ReceiptFailure, _ *Plan) { f.Observation.Limit++ }, false},
		{"wrong_observed", func(f *ReceiptFailure, _ *Plan) { f.Observation.Observed++ }, false},
		{"legacy_v1", func(_ *ReceiptFailure, p *Plan) { p.Schema = PlanSchema }, false},
		{"legacy_v2", func(_ *ReceiptFailure, p *Plan) { p.Schema = PlanV2Schema }, false},
		{"counter_limit_still_exact_one", func(f *ReceiptFailure, _ *Plan) { f.Observation.Kind = "counter_limit" }, false},
		{"unknown_metric", func(f *ReceiptFailure, _ *Plan) { f.Observation.Metric = "unobserved_work" }, false},
		{"store_still_specialised", func(f *ReceiptFailure, _ *Plan) { f.Observation.Metric = "store_rows" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate, candidatePlan := failure, plan
			test.mutate(&candidate, &candidatePlan)
			candidate.Observation.EvidenceSHA256 = ""
			candidate.Observation.EvidenceSHA256 = mustReceiptSHA256(t, candidate.Observation)
			valid := validReceiptFailure(candidate, phase, candidatePlan) && validateStoppedFailureEvidence(Receipt{Measurements: []PhaseMeasurement{measurement}}, &candidate, nil, candidatePlan, ExecutionFreeze{}) == nil
			if valid != test.want {
				t.Fatalf("valid=%t want=%t", valid, test.want)
			}
		})
	}
	for _, outcome := range []string{"passed", "failed"} {
		if validatePhaseWorkMetrics(measurement.Metrics, measurement.DispatchAccounting.Roles, bounds, outcome, &failure.Observation, plan.WorkEnvelope) == nil {
			t.Fatalf("crossing accepted as %s", outcome)
		}
	}
	unavailable := FailureObservation{Kind: "measurement_unavailable", UnavailableMetrics: workTestUnavailable(6)}
	if validatePhaseWorkMetrics(measurement.Metrics, measurement.DispatchAccounting.Roles, bounds, "stopped", &unavailable, plan.WorkEnvelope) == nil {
		t.Fatal("unavailable-only stop hid a known crossing")
	}
	measurement.Metrics.StoreTransactions = CountMetric(bounds.StoreTransactions.Maximum + 1)
	measurement.Metrics.StoreRows, measurement.Metrics.MaxRowsTransaction = measurement.Metrics.StoreTransactions, 1
	if validatePhaseWorkMetrics(measurement.Metrics, measurement.DispatchAccounting.Roles, bounds, "stopped", &failure.Observation, plan.WorkEnvelope) == nil {
		t.Fatal("work crossing relaxed store admission")
	}
}

func TestWorkFailureDecisionPrecedence(t *testing.T) {
	plan := accountingTestPlan(t)
	phase := "product_queries"
	bounds := plan.WorkEnvelope.Phases[slices.Index(plan.PhaseOrder, phase)]
	for _, test := range []struct {
		name                       string
		work, incomplete, topology bool
		wantDecision               string
		wantPriority               uint64
	}{
		{"resource_only", false, false, false, plan.StopRules[1].Decision, 2},
		{"resource_and_work", true, false, false, "reduce", 4},
		{"resource_and_incomplete_work", true, true, false, "reduce", 4},
		{"resource_and_incomplete_coverage", false, true, false, "reduce", 4},
		{"topology_and_work", true, false, true, "p6_investigation", 1},
		{"topology_and_incomplete_work", true, true, true, "p6_investigation", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			measurement := accountingTestMeasurement(plan, phase)
			measurement.Metrics.ObservedRSSHighWaterBytes = Bytes(plan.SafetyEnvelope.MaximumPeakRSSBytes + 100)
			measurement.NativeObservation.ObservedRSSHighWaterBytes = uint64(measurement.Metrics.ObservedRSSHighWaterBytes)
			failure := ReceiptFailure{Phase: phase, Class: "resource", Code: "observed_rss_ceiling", Observation: FailureObservation{
				Kind: "gauge_limit", Metric: "observed_rss_high_water_bytes", Limit: plan.SafetyEnvelope.MaximumPeakRSSBytes,
				Observed: uint64(measurement.Metrics.ObservedRSSHighWaterBytes),
			}}
			if test.work {
				measurement.Metrics.ResolverBlobReads = CountMetric(bounds.ResolverBlobReads.Maximum + 1)
				measurement.Metrics.ResolverBlobBytes = Bytes(bounds.ResolverBlobBytes.Maximum + 73)
			}
			if test.incomplete {
				failure.Observation.UnavailableMetrics = workTestUnavailable(6)
			}
			if test.topology {
				measurement.Metrics.MaterializedOwnerPairs = 1
				failure.Class, failure.Code = "topology", "materialized_cartesian_owner_pairs_nonzero"
				failure.Observation.Kind, failure.Observation.Metric = "counter_crossing", "materialized_cartesian_owner_pairs"
				failure.Observation.Limit, failure.Observation.Observed = 0, 1
			}
			decision, priority, err := expectedStoppedDecision(failure, []PhaseMeasurement{measurement}, plan)
			if err != nil || decision != test.wantDecision || priority != test.wantPriority {
				t.Fatalf("decision=%s/%d want=%s/%d: %v", decision, priority, test.wantDecision, test.wantPriority, err)
			}
		})
	}
}
