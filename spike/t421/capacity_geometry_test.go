package t421

import (
	"slices"
	"testing"
)

// These are bounded validator inputs, not native byte observations or an
// externally admitted execution freeze. Historical plans use SHA-verified bytes.
func TestCapacityVersionedReceiptBoundary(t *testing.T) {
	for _, plan := range lifecyclePolicyPlans(t) {
		schema := plan.Schema
		t.Run(schema, func(t *testing.T) {
			want := uint64(96 << 30)
			if schema == PlanV3Schema {
				want = 128 << 30
			}
			if plan.SafetyEnvelope.MaximumDataAllocatedBytes != want {
				t.Fatal("wrong versioned allocation ceiling")
			}
			for _, excess := range []uint64{0, 1} {
				values, outcomes, freeze := failureCompositionMeasurements(plan, "product_queries")
				if schema != PlanV3Schema {
					for i := range values {
						if outcomes[values[i].Phase] == "not_run" {
							continue
						}
						values[i].DispatchAccounting, values[i].NativeObservation = nil, nil
						values[i].Metrics.DispatchMeasurementAvailable, values[i].Metrics.NativeMeasurementAvailable = false, false
						values[i].Metrics.ObservedRSSHighWaterBytes = 0
						values[i].Metrics.ProcessMeasurementAvailable, values[i].Metrics.PeakRSSBytes = true, 1024
						for _, role := range plan.WorkEnvelope.ChildProcessRoles {
							values[i].ChildProcessRoles = append(values[i].ChildProcessRoles, Count{Name: role})
						}
					}
				}
				index := slices.Index(plan.PhaseOrder, "product_queries")
				values[index] = testPhaseMeasurement(plan, freeze, index)
				values[index].Metrics.AvailableDiskBytes = 1
				values[index].Metrics.DataLogicalBytes = 1
				values[index].Metrics.DataAllocatedBytes = Bytes(want + excess)
				outcomes["product_queries"] = "passed"
				err := validateReceiptMeasurements(values, outcomes, nil, ReceiptTeardown{Outcome: "failed"}, 0, plan, freeze)
				if (err == nil) != (excess == 0) {
					t.Fatalf("allocation excess %d: %v", excess, err)
				}
				failure := ReceiptFailure{Phase: "product_queries", Class: "resource", Code: "data_allocated_ceiling", Observation: FailureObservation{
					Kind: "gauge_limit", Metric: "data_allocated_bytes", Limit: want, Observed: want + excess,
				}}
				_, priority, err := expectedStoppedDecision(failure, values, plan)
				if (err == nil) != (excess == 1) || err == nil && priority != 2 {
					t.Fatalf("allocation stop excess %d priority %d: %v", excess, priority, err)
				}
				if excess == 1 {
					outcomes["product_queries"] = "stopped"
					if err := validateReceiptMeasurements(values, outcomes, &failure, ReceiptTeardown{Outcome: "failed"}, 0, plan, freeze); err != nil {
						t.Fatal("truthful allocation crossing refused", err)
					}
					failure.Observation.Limit--
					if _, _, err := expectedStoppedDecision(failure, values, plan); err == nil {
						t.Fatal("resource observation accepted a different plan limit")
					}
				}
			}
		})
	}
}

func TestCapacityVersionedGeometry(t *testing.T) {
	for _, plan := range lifecyclePolicyPlans(t) {
		schema := plan.Schema
		t.Run(schema, func(t *testing.T) {
			geometry, err := expectedExecutionPressureGeometry(plan, executionFreezeTestHost())
			if err != nil {
				t.Fatal(err)
			}
			want := uint64(10_823_317_586)
			if schema == PlanV3Schema {
				want = 45_183_055_954
			}
			if geometry.CustodyMarginBytes != want || geometry.PressureVolumeBytes != 96<<30 ||
				plan.WorkEnvelope.MaximumDataLogicalBytes != 128<<30 ||
				geometry.Targets[1].TargetUsedBytes != 92_255_897_518 ||
				geometry.CustodyMarginBytes != plan.SafetyEnvelope.MaximumDataAllocatedBytes-geometry.Targets[1].TargetUsedBytes {
				t.Fatal("versioned accounting margin changed physical/logical geometry", geometry)
			}
			values, outcomes, freeze := failureCompositionMeasurements(plan, "preflight")
			freeze.Host, freeze.Pressure = executionFreezeTestHost(), geometry
			values[0] = testPhaseMeasurement(plan, freeze, 0)
			values[0].Metrics.DataAllocatedBytes, values[0].Metrics.DataLogicalBytes = 1, 1
			outcomes["preflight"] = "passed"
			// Keep the unrelated teardown unexecuted. This isolates actual receipt
			// preflight-to-freeze geometry binding without external admission.
			last := len(values) - 1
			values[last], outcomes["teardown"] = PhaseMeasurement{Phase: "teardown"}, "not_run"
			if err := validateReceiptMeasurements(values, outcomes, nil, ReceiptTeardown{Outcome: "failed"}, 0, plan, freeze); err != nil {
				t.Fatal("valid preflight geometry", err)
			}
			freeze.Pressure.CustodyMarginBytes++
			if err := validateReceiptMeasurements(values, outcomes, nil, ReceiptTeardown{Outcome: "failed"}, 0, plan, freeze); err == nil {
				t.Fatal("receipt accepted changed freeze margin")
			}
		})
	}
}
