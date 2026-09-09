package t421

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// This builds only bounded validator inputs, not the native full-receipt
// identity constructor. Full sequence/authentication cases live alongside the
// existing constructor test and are gated separately by the lead.
func failureCompositionMeasurements(plan Plan, phase string) ([]PhaseMeasurement, map[string]string, ExecutionFreeze) {
	values := make([]PhaseMeasurement, len(plan.PhaseOrder))
	outcomes := make(map[string]string, len(values))
	freeze := ExecutionFreeze{}
	freeze.Host.PressureTotalDiskBytes = 96 << 30
	for i, name := range plan.PhaseOrder {
		values[i].Phase = name
		outcomes[name] = "not_run"
		if name != phase && name != "teardown" {
			continue
		}
		values[i] = accountingTestMeasurement(plan, name)
		values[i].Metrics.WallMS = 1
		values[i].Metrics.AvailableDiskBytes = 1
		values[i].Metrics.TotalDiskBytes = Bytes(freeze.Host.PressureTotalDiskBytes)
		values[i].Metrics.AllocationMeasurementAvailable = true
		values[i].Metrics.DataAllocatedBytes, values[i].Metrics.DataLogicalBytes = 1, 1
		outcomes[name] = "stopped"
		if name == "teardown" {
			outcomes[name] = "attempted"
		}
	}
	return values, outcomes, freeze
}

func TestFailureCompositionLogicalPrimary(t *testing.T) {
	for _, mode := range []string{"logical", "work", "topology", "multiple", "wrong_multiple", "wrong_single", "wrong_work", "wrong_topology", "passed"} {
		t.Run(mode, func(t *testing.T) {
			plan := accountingTestPlan(t)
			phase := "product_queries"
			index := slices.Index(plan.PhaseOrder, phase)
			values, outcomes, freeze := failureCompositionMeasurements(plan, phase)
			metrics := &values[index].Metrics
			metrics.DataLogicalBytes = Bytes(plan.WorkEnvelope.MaximumDataLogicalBytes + 17)
			failure := ReceiptFailure{Phase: phase, Class: "resource", Code: "data_logical_ceiling", Observation: FailureObservation{Schema: plan.ReceiptContract.FailureObservationSchema, Kind: "gauge_limit", Metric: "data_logical_bytes", Limit: plan.WorkEnvelope.MaximumDataLogicalBytes, Observed: uint64(metrics.DataLogicalBytes)}}
			wantPriority := uint64(2)
			switch mode {
			case "work", "wrong_work":
				bounds := plan.WorkEnvelope.Phases[index]
				metrics.ResolverBlobBytes = Bytes(bounds.ResolverBlobBytes.Maximum + 73)
				failure = workTestFailure(t, plan, phase, "resolver_blob_bytes", bounds.ResolverBlobBytes.Maximum, uint64(metrics.ResolverBlobBytes), nil)
				wantPriority = 4
				if mode == "wrong_work" {
					failure.Observation.Observed++
				}
			case "topology", "wrong_topology":
				metrics.MaterializedOwnerPairs = 1
				failure.Class, failure.Code = "topology", "materialized_cartesian_owner_pairs_nonzero"
				failure.Observation.Kind, failure.Observation.Metric, failure.Observation.Limit, failure.Observation.Observed = "counter_crossing", "materialized_cartesian_owner_pairs", 0, 1
				wantPriority = 1
				if mode == "wrong_topology" {
					failure.Observation.Observed = 2
				}
			case "multiple", "wrong_multiple", "wrong_single":
				metrics.ObservedRSSHighWaterBytes = Bytes(plan.SafetyEnvelope.MaximumPeakRSSBytes + 100)
				values[index].NativeObservation.ObservedRSSHighWaterBytes = uint64(metrics.ObservedRSSHighWaterBytes)
				failure.Code, failure.Observation.Kind, failure.Observation.Metric = "multiple_resource_ceilings", "gauge_limit", "multiple_resource_ceilings"
				failure.Observation.Limit, failure.Observation.Observed = 0, 1
				wantPriority = 4
				if mode == "wrong_multiple" {
					failure.Observation.Observed = 2
				}
				if mode == "wrong_single" {
					failure.Code = "data_logical_ceiling"
					failure.Observation.Metric = "data_logical_bytes"
					failure.Observation.Limit = plan.WorkEnvelope.MaximumDataLogicalBytes
					failure.Observation.Observed = uint64(metrics.DataLogicalBytes)
				}
			case "passed":
				outcomes[phase] = "passed"
			}
			failure.Observation.EvidenceSHA256 = ""
			failure.Observation.EvidenceSHA256 = mustReceiptSHA256(t, failure.Observation)
			err := validateReceiptMeasurements(values, outcomes, &failure, ReceiptTeardown{Outcome: "failed"}, 0, plan, freeze)
			if err == nil {
				err = validateStoppedFailureEvidence(Receipt{Measurements: values}, &failure, nil, plan, freeze)
			}
			want := mode == "logical" || mode == "work" || mode == "topology" || mode == "multiple"
			if (err == nil) != want {
				t.Fatal(mode, err)
			}
			if want {
				_, priority, err := expectedStoppedDecision(failure, values, plan)
				if err != nil || priority != wantPriority {
					t.Fatal(priority, err)
				}
			}
			if metrics.DataLogicalBytes != Bytes(plan.WorkEnvelope.MaximumDataLogicalBytes+17) {
				t.Fatal("positive gauge changed")
			}
		})
	}
}

func TestFailureCompositionLegacyLogicalOnly(t *testing.T) {
	for _, path := range []string{"plan.json", "plan-v2.json"} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var plan Plan
			if json.Unmarshal(raw, &plan) != nil {
				t.Fatal("retained plan")
			}
			values, outcomes, freeze := failureCompositionMeasurements(plan, "product_queries")
			for i := range values {
				if outcomes[values[i].Phase] == "not_run" {
					continue
				}
				values[i].DispatchAccounting, values[i].NativeObservation = nil, nil
				values[i].Metrics.DispatchMeasurementAvailable, values[i].Metrics.NativeMeasurementAvailable = false, false
				values[i].Metrics.ObservedRSSHighWaterBytes = 0
				values[i].Metrics.ProcessMeasurementAvailable = true
				values[i].Metrics.PeakRSSBytes = 1024
				for _, name := range plan.WorkEnvelope.ChildProcessRoles {
					values[i].ChildProcessRoles = append(values[i].ChildProcessRoles, Count{Name: name})
				}
			}
			index := slices.Index(plan.PhaseOrder, "product_queries")
			values[index].Metrics.DataLogicalBytes = Bytes(plan.WorkEnvelope.MaximumDataLogicalBytes + 17)
			failure := ReceiptFailure{Phase: "product_queries", Class: "resource", Code: "data_logical_ceiling", Observation: FailureObservation{Kind: "gauge_limit", Metric: "data_logical_bytes", Limit: plan.WorkEnvelope.MaximumDataLogicalBytes, Observed: uint64(values[index].Metrics.DataLogicalBytes)}}
			if err := validateReceiptMeasurements(values, outcomes, &failure, ReceiptTeardown{Outcome: "failed"}, 0, plan, freeze); err != nil {
				t.Fatal("historical logical-only control", err)
			}
			values[index].Metrics.MaterializedOwnerPairs = 1
			failure.Class, failure.Code = "topology", "materialized_cartesian_owner_pairs_nonzero"
			failure.Observation.Kind, failure.Observation.Metric, failure.Observation.Limit, failure.Observation.Observed = "counter_crossing", "materialized_cartesian_owner_pairs", 0, 1
			if err := validateReceiptMeasurements(values, outcomes, &failure, ReceiptTeardown{Outcome: "failed"}, 0, plan, freeze); err == nil {
				t.Fatal("historical logical-primary rule relaxed")
			}
		})
	}
}
