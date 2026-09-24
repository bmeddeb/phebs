//go:build darwin

package t421

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"testing"
)

// Full V5 receipt acceptance over the existing source-bound constructor fixture.
// Measurements, signatures and the provenance-only caller rebuild are modeled;
// this is neither a native restored run nor cryptographic package verification.
func TestAssembleExecutionReceiptV5RestoredContinuity(t *testing.T) {
	plan := completeV5ReceiptTestPlan(t)
	binding := frozenReceiptTestBinding(t, plan)
	fixture := completeTestReceipt(t, plan, binding)
	evidence := modeledReceiptEvidence(t, fixture, plan)
	outcomes, _, err := validateReceiptPhases(evidence.Phases, plan)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the real accepted-owner and deduplication adapters, not a
	// hand-assembled snapshot table that could omit the new commitment.
	evidence.Authorities, err = composeExecutionSequenceAuthorities(plan, outcomes, evidence.Authorities, evidence.Revisions)
	if err != nil {
		t.Fatal(err)
	}
	prior := evidence.Authorities[slices.Index(plan.PhaseOrder[1:14], "pressure_75")]
	restored := evidence.Authorities[slices.Index(plan.PhaseOrder[1:14], "archive_restore")]
	if prior.CallerRootSHA256 == restored.CallerRootSHA256 || prior.CallerGenerationSHA256 != restored.CallerGenerationSHA256 ||
		!validDigest(prior.CallerContinuitySHA256) || prior.CallerContinuitySHA256 != restored.CallerContinuitySHA256 {
		t.Fatal("fixture does not model a provenance-only caller rebuild")
	}
	value, err := assembleExecutionReceipt(plan, binding, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if value.Decision.Outcome != "passed" || value.Teardown.Outcome != "clean" || value.Schema != ReceiptV5Schema ||
		len(value.Authority.Snapshots) >= len(evidence.Authorities) {
		t.Fatal("V5 successful receipt lost its outcome, version or deduplication")
	}
	returned := returnedPackageTestBinding(t, value, plan, binding)
	raw, err := MarshalCanonical(value)
	if err != nil || len(raw) > MaxReceiptBytes || uint64(len(raw)) > plan.ReceiptContract.MaximumBytes || bytes.Count(raw, []byte{'\n'}) != 1 {
		t.Fatalf("V5 receipt output size=%d error=%v", len(raw), err)
	}
	decoded, err := DecodeReceipt(raw, plan, binding, returned)
	if err != nil || !reflect.DeepEqual(decoded, value) {
		t.Fatal("V5 successful receipt strict roundtrip", err)
	}
	roundTrip := modeledReceiptEvidence(t, decoded, plan)
	if !reflect.DeepEqual(roundTrip.Authorities, evidence.Authorities) ||
		decoded.RelationshipResults.Caller.RootSHA256 != restored.CallerRootSHA256 {
		t.Fatal("V5 deduplication lost actual roots or continuity evidence")
	}
	if err := ValidateReceipt(value, plan, binding, ReturnedPackageBinding{}); err == nil {
		t.Fatal("modeled receipt authenticated itself without a package binding")
	}
	for _, mutation := range []struct {
		name string
		edit func([]AuthorityPhaseResult)
	}{
		{"missing commitment", func(rows []AuthorityPhaseResult) { rows[10].CallerContinuitySHA256 = "" }},
		{"changed content", func(rows []AuthorityPhaseResult) {
			for i := 10; i < len(rows); i++ {
				rows[i].CallerContinuitySHA256 = testDigest("different-caller-content")
			}
		}},
		{"downstream root movement", func(rows []AuthorityPhaseResult) { rows[11].CallerRootSHA256 = testDigest("unaccepted-root") }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			changed := modeledReceiptEvidence(t, cloneTestReceipt(t, fixture), plan)
			mutation.edit(changed.Authorities)
			if _, err := assembleExecutionReceipt(plan, binding, changed); err == nil {
				t.Fatal("V5 full assembly accepted invalid restored continuity")
			}
		})
	}
	t.Logf("modeled V5 receipt bytes=%d/%d", len(raw), plan.ReceiptContract.MaximumBytes)
}

// These fixtures use the existing native authority constructors with modeled
// measurements and signatures. They test deterministic composition, never a
// live ceremony or authenticated acceptance; run with the package native gate.
func TestAssembleExecutionReceiptModeledEvidence(t *testing.T) {
	plan := clonePlan(t, correctedTestPlan(t))
	if err := applyProcessAccountingCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	binding := frozenReceiptTestBinding(t, plan)
	baseline := completeTestReceipt(t, plan, binding)
	for _, test := range []struct {
		name string
		edit func(*Receipt)
	}{
		{"passed", func(*Receipt) {}},
		{"stopped prefix", func(value *Receipt) {
			stopTestReceipt(t, value, plan, "pressure_80", ReceiptFailure{
				Phase: "pressure_80", Class: "topology", Code: "materialized_cartesian_owner_pairs_nonzero",
				Observation: failureObservation(t, plan, "counter_crossing", "materialized_cartesian_owner_pairs", 0, 1),
			})
			value.Measurements[slices.Index(plan.PhaseOrder, "pressure_80")].Metrics.MaterializedOwnerPairs = 1
		}},
		{"failed cleanup", func(value *Receipt) {
			value.Teardown.Completed = false
			value.Teardown.Outcome = "failed"
			value.Teardown.Failure = teardownFailure(t, plan, "teardown_incomplete")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := cloneTestReceipt(t, baseline)
			test.edit(&fixture)
			evidence := modeledReceiptEvidence(t, fixture, plan)
			value, err := assembleExecutionReceipt(plan, binding, evidence)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateReceipt(value, plan, binding, returnedPackageTestBinding(t, value, plan, binding)); err != nil {
				t.Fatal(err)
			}
			if err := ValidateReceipt(value, plan, binding, ReturnedPackageBinding{}); err == nil {
				t.Fatal("unsigned assembly authenticated itself")
			}
			if !reflect.DeepEqual(value.Measurements, fixture.Measurements) || !reflect.DeepEqual(value.Teardown, fixture.Teardown) {
				t.Fatal("assembly changed observed measurements or cleanup")
			}
			evidence.Measurements[0].Metrics.WallMS++
			evidence.States[0].Runtime.OwnedStart.NativeIdentitySHA256 = testDigest("mutated")
			if value.Measurements[0].Metrics.WallMS == evidence.Measurements[0].Metrics.WallMS ||
				value.StateResults[0].Runtime.OwnedStart.NativeIdentitySHA256 == testDigest("mutated") {
				t.Fatal("assembly aliases its caller")
			}
		})
	}
}

func TestAssembleExecutionReceiptRefusesMissingEvidence(t *testing.T) {
	plan := clonePlan(t, correctedTestPlan(t))
	if err := applyProcessAccountingCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	binding := frozenReceiptTestBinding(t, plan)
	baseline := completeTestReceipt(t, plan, binding)
	for _, test := range []struct {
		name string
		edit func(*executionReceiptEvidence)
	}{
		{"measurement inventory", func(v *executionReceiptEvidence) { v.Measurements = v.Measurements[:14] }},
		{"preflight disk", func(v *executionReceiptEvidence) { v.Measurements[0].Metrics.AvailableDiskBytes = 0 }},
		{"teardown observation", func(v *executionReceiptEvidence) { v.Teardown = ReceiptTeardown{} }},
		{"native runtime", func(v *executionReceiptEvidence) { v.States[0].Runtime = nil }},
		{"caller publication", func(v *executionReceiptEvidence) { v.Relationships.Caller = nil }},
		{"archive", func(v *executionReceiptEvidence) {
			v.Transitions[slices.Index(transitionPhases, "archive_restore")].Archive = nil
		}},
		{"stopped without typed failure", func(v *executionReceiptEvidence) { v.Phases[1].Outcome = "stopped" }},
		{"unrun without stop", func(v *executionReceiptEvidence) { v.Phases[1].Outcome = "not_run" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := modeledReceiptEvidence(t, cloneTestReceipt(t, baseline), plan)
			test.edit(&evidence)
			value, err := assembleExecutionReceipt(plan, binding, evidence)
			if !errors.Is(err, errExecutionReceiptAssembly) || !reflect.DeepEqual(value, Receipt{}) {
				t.Fatal("incomplete evidence accepted", err)
			}
		})
	}
}

func modeledReceiptEvidence(t *testing.T, value Receipt, plan Plan) executionReceiptEvidence {
	t.Helper()
	outcomes, _, err := validateReceiptPhases(value.PhaseResults, plan)
	if err != nil {
		t.Fatal(err)
	}
	authorities, err := resolveAuthorityResults(value.Authority.ExtractionRootSnapshots, value.Authority.Snapshots,
		value.Authority.Results, plan.PhaseOrder[1:len(plan.PhaseOrder)-1], outcomes)
	if err != nil {
		t.Fatal(err)
	}
	return executionReceiptEvidence{Phases: value.PhaseResults, Measurements: value.Measurements, Authorities: authorities,
		States: value.StateResults, Transitions: value.TransitionResults, Queries: value.QueryResults,
		Relationships: value.RelationshipResults, Revisions: value.RevisionResults, Teardown: value.Teardown}
}
