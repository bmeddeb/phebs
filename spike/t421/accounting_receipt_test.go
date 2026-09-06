package t421

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"testing"
)

func accountingTestMeasurement(plan Plan, phase string) PhaseMeasurement {
	index := slices.Index(plan.PhaseOrder, phase)
	roles := make([]Count, len(plan.WorkEnvelope.ControlledDispatchRoles))
	for index, role := range plan.WorkEnvelope.ControlledDispatchRoles {
		roles[index].Name = role
	}
	return PhaseMeasurement{
		Phase: phase, StartEventOrdinal: uint64(index*100 + 10), FinishEventOrdinal: uint64(index*100 + 99),
		Metrics:            ReceiptMetrics{DispatchMeasurementAvailable: true, NativeMeasurementAvailable: true, ObservedRSSHighWaterBytes: 1024},
		DispatchAccounting: &DispatchAccountingMeasurement{Schema: DispatchMeasurementSchema, Complete: true, Roles: roles},
		NativeObservation: &ProcessObservation{
			MeasurementKind: "sampled_observation", NativeHistory: "not_established", SimultaneousBounds: "not_established",
			Available: true, CompletedCensuses: 2, ObservedRSSBytes: 512, ObservedRSSHighWaterBytes: 1024,
			Classes: []ProcessObservationClass{{Class: "git"}},
		},
	}
}

func cloneAccountingMeasurement(t *testing.T, value PhaseMeasurement) PhaseMeasurement {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result PhaseMeasurement
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAccountingReceiptV3WireOmitsHistoricalClaims(t *testing.T) {
	plan := accountingTestPlan(t)
	value := Receipt{Schema: ReceiptV3Schema, Measurements: []PhaseMeasurement{accountingTestMeasurement(plan, "cold"), {Phase: "warm_noop"}},
		Teardown: ReceiptTeardown{Scoped: &ScopedTeardownEvidence{Schema: ScopedTeardownSchema}}}
	raw, err := MarshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"child_processes", "child_process_roles", "peak_rss_bytes", "process_measurement_available", "descendants_stopped", "children_remaining", "descendant_stop_errors"} {
		if bytes.Contains(raw, []byte(`"`+field+`":`)) {
			t.Fatalf("V3 retained %s", field)
		}
	}
	for _, field := range []string{"controlled_dispatch_attempts", "dispatch_measurement_available", "observed_rss_high_water_bytes", "native_measurement_available"} {
		if !bytes.Contains(raw, []byte(`"`+field+`":`)) {
			t.Fatalf("V3 omitted %s", field)
		}
	}
	var decoded Receipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil || !reflect.DeepEqual(value, decoded) {
		t.Fatalf("V3 projection round trip: %v", err)
	}
	if err := validateReceiptAccountingVersion(decoded, plan); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"child_processes", "peak_rss_bytes", "process_measurement_available"} {
		// Use structural mutation so explicit legacy zero/false is otherwise valid JSON.
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		metricObject := object["measurements"].([]any)[0].(map[string]any)["metrics"].(map[string]any)
		if field == "process_measurement_available" {
			metricObject[field] = false
		} else {
			metricObject[field] = float64(0)
		}
		encoded, err := json.MarshalIndent(object, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		canonical, err := MarshalCanonical(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(canonical, []byte(`"`+field+`":`)) || bytes.Equal(append(encoded, '\n'), canonical) {
			t.Fatalf("explicit historical zero was canonical: %s", field)
		}
	}

	for _, schema := range []string{ReceiptSchema, "t422-combined-convergence-receipt-v2"} {
		legacy := Receipt{Schema: schema, Measurements: []PhaseMeasurement{{Phase: "not_run"}}}
		type originalFields Receipt
		want, err := json.Marshal(originalFields(legacy))
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(legacy)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("historical field order changed: %v", err)
		}
		for _, field := range []string{"child_processes", "child_process_roles", "peak_rss_bytes", "process_measurement_available", "descendants_stopped", "children_remaining"} {
			if !bytes.Contains(got, []byte(`"`+field+`":`)) {
				t.Fatalf("historical zero field disappeared: %s", field)
			}
		}
	}
}

func TestAccountingReceiptRejectsCrossVersionAndUnknownSchema(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Receipt, *Plan)
	}{
		{"legacy_plan", func(r *Receipt, p *Plan) { p.Schema = PlanV2Schema }},
		{"legacy_receipt", func(r *Receipt, p *Plan) { r.Schema = ReceiptSchema }},
		{"unknown_pair", func(r *Receipt, p *Plan) {
			p.Schema = "unknown"
			r.Schema = "unknown"
			p.ReceiptContract.Schema = "unknown"
		}},
		{"unknown_receipt", func(r *Receipt, p *Plan) { r.Schema = "unknown"; p.ReceiptContract.Schema = "unknown" }},
		{"legacy_count", func(r *Receipt, p *Plan) { r.Measurements[0].Metrics.ChildProcesses = 1 }},
		{"legacy_roles", func(r *Receipt, p *Plan) { r.Measurements[0].ChildProcessRoles = []Count{} }},
		{"global_zero_claim", func(r *Receipt, p *Plan) { r.Teardown.DescendantsStopped = true }},
		{"missing_scope", func(r *Receipt, p *Plan) { r.Teardown.Scoped = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := accountingTestPlan(t)
			value := Receipt{Schema: ReceiptV3Schema, Measurements: []PhaseMeasurement{accountingTestMeasurement(plan, "cold")}, Teardown: ReceiptTeardown{Scoped: &ScopedTeardownEvidence{Schema: ScopedTeardownSchema}}}
			test.mutate(&value, &plan)
			if err := validateReceiptAccountingVersion(value, plan); err == nil {
				t.Fatal("cross-version evidence accepted")
			}
		})
	}
}

func TestAccountingMeasurementRetainsPositiveFailedPrefixes(t *testing.T) {
	plan := accountingTestPlan(t)
	base := accountingTestMeasurement(plan, "cold")
	gitIndex := slices.IndexFunc(base.DispatchAccounting.Roles, func(value Count) bool { return value.Name == "git" })
	maximum := plan.WorkEnvelope.Phases[slices.Index(plan.PhaseOrder, "cold")].ControlledDispatchRoles[gitIndex].Maximum
	base.DispatchAccounting.Roles[gitIndex].Count = maximum
	base.Metrics.ControlledDispatchAttempts = CountMetric(maximum)
	if err := validateAccountingMeasurement(base, "passed", nil, ReceiptTeardown{}, plan); err != nil {
		t.Fatal(err)
	}
	failed := cloneAccountingMeasurement(t, base)
	failed.DispatchAccounting.Complete = false
	failed.DispatchAccounting.FailureClass = "budget_refused"
	failed.Metrics.DispatchMeasurementAvailable = false
	stopped := &ReceiptFailure{Phase: "cold", Code: "internal_error", Evidence: &FailureEvidenceProjection{Internal: &InternalFailureEvidence{Stage: "dispatch_admission", ErrorClass: "budget_refused"}}}
	if err := validateAccountingMeasurement(failed, "stopped", stopped, ReceiptTeardown{}, plan); err != nil {
		t.Fatalf("positive refused prefix: %v", err)
	}
	failed.NativeObservation.Available = false
	failed.NativeObservation.FailureClass = "measurement_unavailable"
	failed.Metrics.NativeMeasurementAvailable = false
	stopped.Code = "measurement_unavailable"
	stopped.Evidence = nil
	stopped.Observation = FailureObservation{Kind: "measurement_unavailable", UnavailableMetrics: []string{"controlled_dispatch_attempts", "observed_rss_high_water_bytes"}}
	if err := validateAccountingMeasurement(failed, "stopped", stopped, ReceiptTeardown{}, plan); err != nil {
		t.Fatalf("positive prior native sample: %v", err)
	}
	if failed.Metrics.ControlledDispatchAttempts == 0 || failed.Metrics.ObservedRSSHighWaterBytes == 0 {
		t.Fatal("positive prefix lost")
	}
	for _, test := range []struct {
		name   string
		mutate func(*PhaseMeasurement)
	}{
		{"invented_refused_attempt", func(v *PhaseMeasurement) {
			v.DispatchAccounting.Roles[gitIndex].Count++
			v.Metrics.ControlledDispatchAttempts++
		}},
		{"complete_with_failure", func(v *PhaseMeasurement) {
			v.DispatchAccounting.Complete = true
			v.Metrics.DispatchMeasurementAvailable = true
		}},
		{"unknown_failure", func(v *PhaseMeasurement) { v.DispatchAccounting.FailureClass = "private host failure" }},
		{"lost_positive_rss", func(v *PhaseMeasurement) { v.Metrics.ObservedRSSHighWaterBytes = 0 }},
		{"wrong_total", func(v *PhaseMeasurement) { v.Metrics.ControlledDispatchAttempts-- }},
		{"missing_role", func(v *PhaseMeasurement) { v.DispatchAccounting.Roles = v.DispatchAccounting.Roles[1:] }},
		{"role_overflow", func(v *PhaseMeasurement) { v.DispatchAccounting.Roles[gitIndex].Count = math.MaxUint64 }},
		{"unavailable_claimed_pass", func(v *PhaseMeasurement) { v.NativeObservation.Available = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := cloneAccountingMeasurement(t, failed)
			test.mutate(&value)
			if err := validateAccountingMeasurement(value, "stopped", stopped, ReceiptTeardown{}, plan); err == nil {
				t.Fatal("invalid prefix accepted")
			}
		})
	}
}

func TestAccountingNativeObservationIsNotASimultaneousBound(t *testing.T) {
	plan := accountingTestPlan(t)
	base := *accountingTestMeasurement(plan, "cold").NativeObservation
	base.CompletedCensuses = 3
	base.ObservedDescendants = 1
	base.ObservedDescendantsHighWater = 2
	base.Classes = []ProcessObservationClass{{Class: "git", ObservedRows: 1, ObservedHighWater: 2}, {Class: "surreal", ObservedHighWater: 2}}
	if err := validateNativeObservation(base); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*ProcessObservation)
	}{
		{"lower_bound", func(v *ProcessObservation) { v.MeasurementKind = "sampled_lower_bound" }},
		{"native_history", func(v *ProcessObservation) { v.NativeHistory = "complete" }},
		{"simultaneous_bound", func(v *ProcessObservation) { v.SimultaneousBounds = "upper_bound" }},
		{"unknown_class", func(v *ProcessObservation) { v.Classes[0].Class = "secret-host" }},
		{"duplicate_class", func(v *ProcessObservation) { v.Classes[1].Class = v.Classes[0].Class }},
		{"too_many_rows", func(v *ProcessObservation) { v.ObservedDescendantsHighWater = 129 }},
		{"incoherent_rows", func(v *ProcessObservation) { v.ObservedDescendants = 2 }},
		{"no_sample_positive", func(v *ProcessObservation) { v.CompletedCensuses = 0 }},
		{"first_sample_high_water_drift", func(v *ProcessObservation) { v.CompletedCensuses = 1 }},
		{"insufficient_prior_censuses", func(v *ProcessObservation) { v.CompletedCensuses = 2 }},
		{"missing_class_high_water", func(v *ProcessObservation) { v.Classes[0].ObservedHighWater = 1; v.Classes[1].ObservedHighWater = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.Classes = slices.Clone(base.Classes)
			test.mutate(&value)
			if err := validateNativeObservation(value); err == nil {
				t.Fatal("invalid sampled claim accepted")
			}
		})
	}
	for _, test := range []struct {
		schema string
		names  []string
		want   bool
	}{
		{PlanSchema, []string{"peak_rss_bytes"}, true}, {PlanV2Schema, []string{"observed_rss_high_water_bytes"}, false},
		{PlanV3Schema, []string{"peak_rss_bytes"}, false}, {PlanV3Schema, []string{"controlled_dispatch_attempts", "observed_rss_high_water_bytes"}, true},
		{PlanV3Schema, []string{"controlled_dispatch_attempts", "controlled_dispatch_attempts"}, false},
	} {
		if got := validUnavailableMetricsForPlan(test.names, test.schema); got != test.want {
			t.Fatalf("metric vocabulary %s %v", test.schema, test.names)
		}
	}
}

func TestAccountingMixedResourceFailureReducesWithoutErasingCrossing(t *testing.T) {
	plan := accountingTestPlan(t)
	phase := "product_queries"
	base := accountingTestMeasurement(plan, phase)
	base.Metrics.ObservedRSSHighWaterBytes = Bytes(plan.SafetyEnvelope.MaximumPeakRSSBytes + 1)
	failure := ReceiptFailure{Phase: phase, Class: "resource", Code: "observed_rss_ceiling", Observation: FailureObservation{
		Kind: "gauge_limit", Metric: "observed_rss_high_water_bytes", Limit: plan.SafetyEnvelope.MaximumPeakRSSBytes,
		Observed: uint64(base.Metrics.ObservedRSSHighWaterBytes),
	}}
	for _, test := range []struct {
		name             string
		native, dispatch bool
		priority         uint64
	}{
		{"resource_only", true, true, 2}, {"native_unavailable", false, true, 4},
		{"dispatch_incomplete", true, false, 4}, {"both_unavailable", false, false, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			measurement := base
			measurement.Metrics.NativeMeasurementAvailable = test.native
			measurement.Metrics.DispatchMeasurementAvailable = test.dispatch
			selected, priority, err := expectedStoppedDecision(failure, []PhaseMeasurement{measurement}, plan)
			if err != nil || priority != test.priority || selected != plan.StopRules[test.priority-1].Decision {
				t.Fatalf("mixed failure priority=%d decision=%s err=%v", priority, selected, err)
			}
			if measurement.Metrics.ObservedRSSHighWaterBytes != base.Metrics.ObservedRSSHighWaterBytes {
				t.Fatal("overshoot erased")
			}
		})
	}
}

func TestAccountingStoreUnavailableVocabulary(t *testing.T) {
	family := []string{"max_rows_in_any_transaction", "store_rows", "store_transactions"}
	if !slices.Equal(family, storeUnavailableMetricNames) {
		t.Fatal("store metric family changed")
	}
	for _, test := range []struct {
		name, schema string
		names        []string
		want         bool
	}{
		{"complete", PlanV3Schema, nil, true},
		{"retained_prefix", PlanV3Schema, family, true},
		{"mixed_unavailable", PlanV3Schema, slices.Concat([]string{"controlled_dispatch_attempts"}, family), true},
		{"missing_maximum", PlanV3Schema, family[1:], false},
		{"missing_rows", PlanV3Schema, []string{family[0], family[2]}, false},
		{"missing_transactions", PlanV3Schema, family[:2], false},
		{"unsorted", PlanV3Schema, []string{family[2], family[1], family[0]}, false},
		{"duplicate", PlanV3Schema, slices.Concat(family, []string{family[2]}), false},
		{"unknown", PlanV3Schema, slices.Concat(family, []string{"unobserved_rows"}), false},
		{"historical_v1", PlanSchema, family, false},
		{"historical_v2", PlanV2Schema, family, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validUnavailableMetricsForPlan(test.names, test.schema); got != test.want {
				t.Fatalf("unavailable vocabulary = %t, want %t", got, test.want)
			}
		})
	}
}

func accountingStoreTestFailure(t *testing.T, plan Plan, measurement PhaseMeasurement, reason string) ReceiptFailure {
	t.Helper()
	failure := ReceiptFailure{Phase: measurement.Phase, Class: "internal", Code: "measurement_unavailable",
		Observation: FailureObservation{Schema: plan.ReceiptContract.FailureObservationSchema,
			Kind: "measurement_unavailable", UnavailableMetrics: slices.Clone(storeUnavailableMetricNames)}}
	if reason != "" {
		failure.Code, failure.Observation.Kind, failure.Observation.Metric = "internal_error", "typed_error", "internal_error"
		failure.Evidence = &FailureEvidenceProjection{Schema: plan.ReceiptContract.FailureObservationSchema + "/public-projection-v1",
			Kind: "internal", Internal: &InternalFailureEvidence{Phase: measurement.Phase,
				Stage: "store_submission", ErrorClass: reason, EventOrdinal: measurement.StartEventOrdinal + 1}}
		failure.Observation.ObservedSHA256 = mustReceiptSHA256(t, *failure.Evidence)
	}
	failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
	return failure
}

// These are bounded section-validator/wire regressions, not a production
// submission channel, private receipt issuer or full constructor replay.
func TestAccountingStorePrefixRefusalDoesNotInventWork(t *testing.T) {
	plan := accountingTestPlan(t)
	phase := "logical_delta_b"
	measurement := accountingTestMeasurement(plan, phase)
	bounds := plan.WorkEnvelope.Phases[slices.Index(plan.PhaseOrder, phase)]
	if bounds.StoreTransactions.Maximum != 170 || plan.WorkEnvelope.MaximumStoreRowsPerTransaction != 512 ||
		plan.MeterPolicy.StoreSemantics != frozenMeterPolicy().StoreSemantics {
		t.Fatal("frozen store unit or safety numbers changed")
	}
	for _, reason := range []string{"", "budget_refused", "invalid_descriptor", "invalid_protocol", "transport_unavailable", "canceled", "incomplete"} {
		t.Run("reason_"+reason, func(t *testing.T) {
			value := measurement
			value.Metrics.StoreTransactions, value.Metrics.StoreRows, value.Metrics.MaxRowsTransaction = 170, 170*512, 512
			failure := accountingStoreTestFailure(t, plan, value, reason)
			if !validReceiptFailure(failure, phase, plan) {
				t.Fatal("positive incomplete store prefix rejected")
			}
			receipt := Receipt{Schema: ReceiptV3Schema, Measurements: []PhaseMeasurement{value}}
			if reason != "" {
				if err := validateFailureEvidenceProjection(receipt, failure, plan); err != nil {
					t.Fatal(err)
				}
			}
			if err := validatePhaseWorkMetrics(value.Metrics, value.DispatchAccounting.Roles, bounds, "stopped", &failure.Observation, plan.WorkEnvelope); err != nil {
				t.Fatal(err)
			}
			if decision, priority, err := expectedStoppedDecision(failure, receipt.Measurements, plan); err != nil || decision != "reduce" || priority != 4 {
				t.Fatalf("store refusal decision = %s/%d: %v", decision, priority, err)
			}
			type storeWire struct {
				Measurement PhaseMeasurement `json:"measurement"`
				Failure     ReceiptFailure   `json:"failure"`
			}
			wire := storeWire{value, failure}
			raw, err := MarshalCanonical(wire)
			if err != nil {
				t.Fatal(err)
			}
			var decoded storeWire
			if err := json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(wire, decoded) {
				t.Fatalf("store prefix wire roundtrip: %v", err)
			}
			value.Metrics.StoreTransactions++
			if err := validatePhaseWorkMetrics(value.Metrics, value.DispatchAccounting.Roles, bounds, "stopped", &failure.Observation, plan.WorkEnvelope); err == nil {
				t.Fatal("refused permission manufactured a cap+1 attempt")
			}
		})
	}
	for _, reason := range []string{"unknown", "native_committed", "private host error"} {
		failure := accountingStoreTestFailure(t, plan, measurement, reason)
		if err := validateFailureEvidenceProjection(Receipt{Measurements: []PhaseMeasurement{measurement}}, failure, plan); err == nil {
			t.Fatalf("unknown store failure %q accepted", reason)
		}
	}
	failure := accountingStoreTestFailure(t, plan, measurement, "budget_refused")
	failure.Observation.UnavailableMetrics = nil
	if err := validateFailureEvidenceProjection(Receipt{Measurements: []PhaseMeasurement{measurement}}, failure, plan); err == nil {
		t.Fatal("store refusal claimed a complete phase")
	}
	// A failure before the first concrete submission retains a truthful zero
	// prefix with explicit incompleteness; no nonzero sentinel is manufactured.
	failure = accountingStoreTestFailure(t, plan, measurement, "")
	if !validReceiptFailure(failure, phase, plan) || validatePhaseWorkMetrics(measurement.Metrics, measurement.DispatchAccounting.Roles, bounds, "stopped", &failure.Observation, plan.WorkEnvelope) != nil {
		t.Fatal("explicitly incomplete pre-submission zero prefix refused")
	}
}

func TestAccountingStoreSecondaryFailureRetainsPrimaryEvidence(t *testing.T) {
	plan := accountingTestPlan(t)
	measurement := accountingTestMeasurement(plan, "product_queries")
	measurement.Metrics.StoreTransactions, measurement.Metrics.StoreRows, measurement.Metrics.MaxRowsTransaction = 2, 3, 2
	measurement.Metrics.ObservedRSSHighWaterBytes = Bytes(plan.SafetyEnvelope.MaximumPeakRSSBytes + 1)
	measurement.NativeObservation.ObservedRSSHighWaterBytes = uint64(measurement.Metrics.ObservedRSSHighWaterBytes)
	failure := ReceiptFailure{Phase: measurement.Phase, Class: "resource", Code: "observed_rss_ceiling",
		Observation: FailureObservation{Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "gauge_limit",
			Metric: "observed_rss_high_water_bytes", Limit: plan.SafetyEnvelope.MaximumPeakRSSBytes,
			Observed: uint64(measurement.Metrics.ObservedRSSHighWaterBytes), UnavailableMetrics: slices.Clone(storeUnavailableMetricNames)}}
	failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
	if !validReceiptFailure(failure, measurement.Phase, plan) {
		t.Fatal("substantiated crossing with an incomplete store prefix refused")
	}
	if err := validateAccountingMeasurement(measurement, "stopped", &failure, ReceiptTeardown{}, plan); err != nil {
		t.Fatal(err)
	}
	if decision, priority, err := expectedStoppedDecision(failure, []PhaseMeasurement{measurement}, plan); err != nil || decision != "reduce" || priority != 4 {
		t.Fatalf("mixed resource/store failure = %s/%d: %v", decision, priority, err)
	}
	for _, schema := range []string{PlanSchema, PlanV2Schema} {
		legacy := plan
		legacy.Schema = schema
		historicalFailure := failure
		historicalFailure.Code, historicalFailure.Observation.Metric = "data_allocated_ceiling", "data_allocated_bytes"
		historicalFailure.Observation.EvidenceSHA256 = ""
		historicalFailure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, historicalFailure.Observation)
		if validReceiptFailure(historicalFailure, measurement.Phase, legacy) {
			t.Fatal("historical receipt accepted V3 secondary store evidence")
		}
		historicalFailure.Observation.UnavailableMetrics, historicalFailure.Observation.EvidenceSHA256 = nil, ""
		historicalFailure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, historicalFailure.Observation)
		if !validReceiptFailure(historicalFailure, measurement.Phase, legacy) {
			t.Fatal("historical primary failure changed")
		}
	}
	for _, names := range [][]string{storeUnavailableMetricNames[:2], {}, {"observed_rss_high_water_bytes"}} {
		invalid := failure
		invalid.Observation.UnavailableMetrics = slices.Clone(names)
		invalid.Observation.EvidenceSHA256 = ""
		invalid.Observation.EvidenceSHA256 = mustReceiptSHA256(t, invalid.Observation)
		if validReceiptFailure(invalid, measurement.Phase, plan) {
			t.Fatal("partial or unrelated secondary unavailable family accepted")
		}
	}
	measurement.Metrics.MaterializedOwnerPairs = 1
	failure.Class, failure.Code = "topology", "materialized_cartesian_owner_pairs_nonzero"
	failure.Observation.Kind, failure.Observation.Metric = "counter_crossing", "materialized_cartesian_owner_pairs"
	failure.Observation.Limit, failure.Observation.Observed, failure.Observation.EvidenceSHA256 = 0, 1, ""
	failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
	if !validReceiptFailure(failure, measurement.Phase, plan) {
		t.Fatal("independent topology evidence refused")
	}
	if decision, priority, err := expectedStoppedDecision(failure, []PhaseMeasurement{measurement}, plan); err != nil || decision != "p6_investigation" || priority != 1 {
		t.Fatalf("independent topology evidence = %s/%d: %v", decision, priority, err)
	}
}

func TestAccountingStoreUnavailableTeardownRetainsPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	value, measurements := accountingTestTeardown(plan)
	measurements[0].Metrics.StoreTransactions, measurements[0].Metrics.StoreRows, measurements[0].Metrics.MaxRowsTransaction = 2, 3, 2
	value.MeasurementUnavailable, value.MeasurementErrors = slices.Clone(storeUnavailableMetricNames), 3
	if _, err := validateReceiptTeardown(value, measurements, plan, ExecutionFreeze{}, false); err == nil {
		t.Fatal("clean teardown accepted an incomplete store prefix")
	}
	value.Outcome = "failed"
	value.Failure = &TeardownFailure{Schema: plan.ReceiptContract.TeardownFailureSchema, Kind: "multiple"}
	for _, name := range storeUnavailableMetricNames {
		value.Failure.FailedChecks = append(value.Failure.FailedChecks, "measurement_"+name+"_unavailable")
	}
	value.Failure.EvidenceSHA256 = mustReceiptSHA256(t, *value.Failure)
	if clean, err := validateReceiptTeardown(value, measurements, plan, ExecutionFreeze{}, false); err != nil || clean {
		t.Fatalf("truthful incomplete-store teardown refused: %v", err)
	}
	if measurements[0].Metrics.StoreTransactions != 2 || measurements[0].Metrics.StoreRows != 3 || measurements[0].Metrics.MaxRowsTransaction != 2 {
		t.Fatal("incomplete cleanup erased the retained store prefix")
	}
}

func TestAccountingProductionFixtureRejectsChangedInputs(t *testing.T) {
	// Use the retained fixture bytes, not another production corpus build.
	prospective := accountingTestPlan(t)
	raw, err := os.ReadFile("plan-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var prior Plan
	if err := json.Unmarshal(raw, &prior); err != nil {
		t.Fatal(err)
	}
	if !productionFixturePlanMatches(prospective, prior) {
		t.Fatal("exact V3 fixture input refused")
	}
	for _, test := range []struct {
		name   string
		mutate func(*Plan)
	}{
		{"source", func(p *Plan) { p.SourceCommit = testDigest("other-source") }},
		{"input", func(p *Plan) { p.Inputs[0].Identity = testDigest("other-input") }},
		{"oracle", func(p *Plan) { p.Oracle.ProductRelationships = ProductRelationships{} }},
		{"failure_point", func(p *Plan) { p.FailurePoints[0].RecoveryDeadlineMS++ }},
		{"profile", func(p *Plan) { p.Profile.Physical.CombinedRegularFiles++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := clonePlan(t, prospective)
			test.mutate(&changed)
			if productionFixturePlanMatches(changed, prior) {
				t.Fatal("native constructor cache accepted changed functional input")
			}
		})
	}
}

func accountingTestTeardown(plan Plan) (ReceiptTeardown, []PhaseMeasurement) {
	measurement := accountingTestMeasurement(plan, "teardown")
	start := measurement.StartEventOrdinal
	value := ReceiptTeardown{Attempted: true, Completed: true, Outcome: "clean", StoreClosed: true,
		PressureVolumeDetached: true, PressureImageRemoved: true, RetainedSourceFreeOnly: true,
		Scoped: &ScopedTeardownEvidence{Schema: ScopedTeardownSchema,
			OperationalFenceEventOrdinal: start + 1, OwnedHandlesJoinedEventOrdinal: start + 2, LeaseReleasedEventOrdinal: start + 3,
			BeforeDetach:       SessionCensusEvidence{EventOrdinal: start + 4, RecordedSessions: 2, CompletedCensuses: 2},
			DetachEventOrdinal: start + 5, DetachNonForced: true, ExactImageRemovalEventOrdinal: start + 6, ExactRootRemovalEventOrdinal: start + 7, CustodyLockHeld: true,
			CleanupJoinedEventOrdinal: start + 8, AfterCleanup: SessionCensusEvidence{EventOrdinal: start + 9, RecordedSessions: 3, CompletedCensuses: 3}, CleanupClosedEventOrdinal: start + 10,
		}}
	return value, []PhaseMeasurement{measurement}
}

func TestAccountingTeardownRequiresScopedOrderedClosure(t *testing.T) {
	plan := accountingTestPlan(t)
	value, measurements := accountingTestTeardown(plan)
	if clean, err := validateReceiptTeardown(value, measurements, plan, ExecutionFreeze{}, false); err != nil || !clean {
		t.Fatalf("clean scoped teardown: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*ScopedTeardownEvidence)
	}{
		{"missing_fence", func(v *ScopedTeardownEvidence) { v.OperationalFenceEventOrdinal = 0 }},
		{"owned_handle", func(v *ScopedTeardownEvidence) { v.OwnedHandlesRemaining = 1 }},
		{"live_lease", func(v *ScopedTeardownEvidence) { v.StoreLeasesRemaining = 1 }},
		{"incomplete_census", func(v *ScopedTeardownEvidence) { v.BeforeDetach.CompletedCensuses-- }},
		{"live_session", func(v *ScopedTeardownEvidence) { v.AfterCleanup.ObservedProcesses = 1 }},
		{"forced_detach", func(v *ScopedTeardownEvidence) { v.DetachNonForced = false }},
		{"remove_before_detach", func(v *ScopedTeardownEvidence) { v.ExactImageRemovalEventOrdinal = v.DetachEventOrdinal - 1 }},
		{"missing_lock", func(v *ScopedTeardownEvidence) { v.CustodyLockHeld = false }},
		{"lost_session", func(v *ScopedTeardownEvidence) {
			v.AfterCleanup.RecordedSessions = 1
			v.AfterCleanup.CompletedCensuses = 1
		}},
		{"cleanup_still_open", func(v *ScopedTeardownEvidence) { v.CleanupClosedEventOrdinal = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := value
			scope := *value.Scoped
			changed.Scoped = &scope
			test.mutate(changed.Scoped)
			if _, err := validateReceiptTeardown(changed, measurements, plan, ExecutionFreeze{}, false); err == nil {
				t.Fatal("unsubstantiated clean teardown accepted")
			}
		})
	}
	value.Outcome = "failed"
	value.Scoped.AfterCleanup.ObservedProcesses = 1
	value.Failure = &TeardownFailure{Schema: plan.ReceiptContract.TeardownFailureSchema, Kind: "cleanup_sessions_remain", FailedChecks: []string{"cleanup_sessions_remain"}}
	value.Failure.EvidenceSHA256 = mustReceiptSHA256(t, *value.Failure)
	if clean, err := validateReceiptTeardown(value, measurements, plan, ExecutionFreeze{}, false); err != nil || clean {
		t.Fatalf("truthful failed scoped teardown: %v", err)
	}
}

func TestAccountingSessionScopeUsesOwnedStartNotAdmission(t *testing.T) {
	plan := accountingTestPlan(t)
	teardown, measurements := accountingTestTeardown(plan)
	teardown.Scoped.BeforeDetach.RecordedSessions, teardown.Scoped.BeforeDetach.CompletedCensuses = 0, 0
	teardown.Scoped.AfterCleanup.RecordedSessions, teardown.Scoped.AfterCleanup.CompletedCensuses = 0, 0
	// An admitted first command may fail Start (or lose its ACK without Start).
	// The counted permission survives, but no private process session exists.
	measurements[0].Metrics.ControlledDispatchAttempts = 1
	roleIndex := slices.IndexFunc(measurements[0].DispatchAccounting.Roles, func(role Count) bool { return role.Name == "hdiutil" })
	measurements[0].DispatchAccounting.Roles[roleIndex].Count = 1
	value := Receipt{Schema: ReceiptV3Schema, Measurements: measurements, Teardown: teardown}
	if err := validateReceiptAccountingVersion(value, plan); err != nil {
		t.Fatalf("failed Start invented a session: %v", err)
	}
	if clean, err := validateReceiptTeardown(teardown, measurements, plan, ExecutionFreeze{}, hasOwnedServerStart(value.StateResults)); err != nil || !clean {
		t.Fatalf("empty prelaunch custody did not close: %v", err)
	}
	value.StateResults = []ExactPhaseEvidence{{Phase: "cold", Runtime: &PhaseRuntimeBinding{OwnedStart: &OwnedServerStartEvidence{OwnedStartSucceeded: true}}}}
	if _, err := validateReceiptTeardown(teardown, measurements, plan, ExecutionFreeze{}, hasOwnedServerStart(value.StateResults)); err == nil {
		t.Fatal("successful owned start accepted zero recorded sessions")
	}
	value.Teardown.Outcome = "failed"
	value.Teardown.Failure = &TeardownFailure{Schema: plan.ReceiptContract.TeardownFailureSchema,
		Kind: "execution_session_scope_missing", FailedChecks: []string{"execution_session_scope_missing"}}
	value.Teardown.Failure.EvidenceSHA256 = mustReceiptSHA256(t, *value.Teardown.Failure)
	if clean, err := validateReceiptTeardown(value.Teardown, measurements, plan, ExecutionFreeze{}, hasOwnedServerStart(value.StateResults)); err != nil || clean {
		t.Fatalf("truthful missing-scope failed cleanup refused: %v", err)
	}
	value.Teardown.Scoped.BeforeDetach.RecordedSessions, value.Teardown.Scoped.AfterCleanup.RecordedSessions = 1, 1
	if err := validateReceiptAccountingVersion(value, plan); err != nil {
		t.Fatalf("owned session scope refused: %v", err)
	}
}

// This fixture reuses the actual V2 constructor graph. Process observations,
// successful starts and signature/package bindings remain explicitly modeled
// test evidence, not a live admission or launcher-readiness result.
func TestAccountingReceiptFullV3RoundTrip(t *testing.T) {
	plan := clonePlan(t, correctedTestPlan(t))
	if err := applyProcessAccountingCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	binding := frozenReceiptTestBinding(t, plan)
	base := completeTestReceipt(t, plan, binding)
	for _, scenario := range []string{"passed", "positive_incomplete_prefix", "rss_overshoot_then_native_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			value := cloneTestReceipt(t, base)
			if scenario != "passed" {
				phase := "product_queries"
				index := slices.Index(plan.PhaseOrder, phase)
				measurement := &value.Measurements[index]
				gitIndex := slices.IndexFunc(measurement.DispatchAccounting.Roles, func(role Count) bool { return role.Name == "git" })
				measurement.DispatchAccounting.Roles[gitIndex].Count = 1
				measurement.Metrics.ControlledDispatchAttempts = 1
				measurement.DispatchAccounting.Complete = false
				measurement.DispatchAccounting.FailureClass = "transport_unavailable"
				measurement.Metrics.DispatchMeasurementAvailable = false
				measurement.NativeObservation.Available = false
				measurement.NativeObservation.FailureClass = "measurement_unavailable"
				measurement.Metrics.NativeMeasurementAvailable = false
				failure := ReceiptFailure{Phase: phase, Class: "internal", Code: "measurement_unavailable", Observation: FailureObservation{
					Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "measurement_unavailable",
					UnavailableMetrics: []string{"controlled_dispatch_attempts", "observed_rss_high_water_bytes"},
				}}
				if scenario == "rss_overshoot_then_native_unavailable" {
					overshoot := plan.SafetyEnvelope.MaximumPeakRSSBytes + 1
					measurement.NativeObservation.ObservedRSSHighWaterBytes = overshoot
					measurement.Metrics.ObservedRSSHighWaterBytes = Bytes(overshoot)
					failure.Class, failure.Code = "resource", "observed_rss_ceiling"
					failure.Observation = FailureObservation{Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "gauge_limit", Metric: "observed_rss_high_water_bytes", Limit: plan.SafetyEnvelope.MaximumPeakRSSBytes, Observed: overshoot}
				}
				failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
				stopTestReceipt(t, &value, plan, phase, failure)
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
				t.Fatalf("full V3 strict roundtrip: %v", err)
			}
			if len(raw) > MaxReceiptBytes || bytes.Count(raw, []byte{'\n'}) != 1 {
				t.Fatalf("invalid V3 compact size %d", len(raw))
			}
			t.Logf("full V3 %s modeled receipt bytes=%d headroom=%d", scenario, len(raw), MaxReceiptBytes-len(raw))
		})
	}
}
