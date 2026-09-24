package t421

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
)

func receiptHandoffTestPlan(t *testing.T) Plan {
	t.Helper()
	plan := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestReceiptSelectorCleanupV5Composition(t *testing.T) {
	plan := receiptHandoffTestPlan(t)
	measurements := accountingFixtureWorkProjection(t, plan)
	for _, phase := range []string{"cold", "physical_delta_b", "logical_delta_b", "return_a"} {
		t.Run(phase, func(t *testing.T) {
			measurement := measurements[slices.Index(plan.PhaseOrder, phase)]
			if err := validatePhaseSelectorCleanup(measurement, "passed", plan); err != nil {
				t.Fatal("empty cleanup rejected", err)
			}
			if phase == "cold" {
				return
			}
			// These are observed-counter fixtures, not measurements inferred
			// from maxima. The summary-only deletion is a valid one-turn case.
			value := *measurement.SelectorCleanup
			value.Deleted, value.MaxDeleted = 1, 1
			value.StoreReadAttempts, value.StoreWriteAttempts = 9, 1
			measurement.SelectorCleanup = &value
			measurement.Metrics.LifecycleDeleted, measurement.Metrics.MaxLifecycleDeletesTurn = 1, 1
			measurement.Metrics.ControlReads = max(measurement.Metrics.ControlReads, CountMetric(value.StoreReadAttempts))
			measurement.Metrics.StoreRows = max(measurement.Metrics.StoreRows, CountMetric(value.Deleted))
			if err := validatePhaseSelectorCleanup(measurement, "passed", plan); err != nil {
				t.Fatal("positive cleanup rejected", err)
			}
			if phase == "physical_delta_b" {
				before := measurement.Metrics
				reader, err := readerTransitionMetrics(measurement, plan)
				if err != nil || reader.LifecycleOwnerTurns != 2 || reader.LifecycleDeleted != 0 || reader.MaxLifecycleDeletesTurn != 0 ||
					measurement.Metrics != before {
					t.Fatal("reader subtotal did not preserve exact isolated work", err)
				}
			}
		})
	}

	base := measurements[slices.Index(plan.PhaseOrder, "physical_delta_b")]
	for _, test := range []struct {
		name   string
		mutate func(*PhaseMeasurement)
	}{
		{"missing", func(v *PhaseMeasurement) { v.SelectorCleanup = nil }},
		{"schema", func(v *PhaseMeasurement) { v.SelectorCleanup.Schema = "unknown" }},
		{"phase", func(v *PhaseMeasurement) { v.SelectorCleanup.Phase++ }},
		{"input", func(v *PhaseMeasurement) { v.SelectorCleanup.InputSHA256 = "bad" }},
		{"selected_runtime", func(v *PhaseMeasurement) { v.SelectorCleanup.SelectedRuntimeSHA256 = "bad" }},
		{"not_done", func(v *PhaseMeasurement) { v.SelectorCleanup.Done = false }},
		{"failed", func(v *PhaseMeasurement) { v.SelectorCleanup.Failed = true }},
		{"missing_turn", func(v *PhaseMeasurement) { v.SelectorCleanup.Turns = 0 }},
		{"extra_turn", func(v *PhaseMeasurement) { v.SelectorCleanup.Turns++; v.Metrics.LifecycleOwnerTurns++ }},
		{"reads", func(v *PhaseMeasurement) { v.SelectorCleanup.StoreReadAttempts++ }},
		{"writes", func(v *PhaseMeasurement) { v.SelectorCleanup.StoreWriteAttempts++ }},
		{"deletion_without_maximum", func(v *PhaseMeasurement) { v.SelectorCleanup.Deleted++ }},
		{"maximum_without_deletion", func(v *PhaseMeasurement) { v.SelectorCleanup.MaxDeleted++ }},
		{"whole_turn_underflow", func(v *PhaseMeasurement) { v.Metrics.LifecycleOwnerTurns = 0 }},
		{"whole_turn_excess", func(v *PhaseMeasurement) { v.Metrics.LifecycleOwnerTurns++ }},
		{"whole_deletions", func(v *PhaseMeasurement) { v.Metrics.LifecycleDeleted++ }},
		{"whole_maximum", func(v *PhaseMeasurement) { v.Metrics.MaxLifecycleDeletesTurn++ }},
		{"whole_reads", func(v *PhaseMeasurement) { v.Metrics.ControlReads = 2 }},
		{"whole_transactions", func(v *PhaseMeasurement) { v.Metrics.StoreTransactions = 0 }},
		{"overflow", func(v *PhaseMeasurement) { v.SelectorCleanup.Turns = math.MaxUint64; v.Metrics.LifecycleOwnerTurns = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := cloneAccountingMeasurement(t, base)
			test.mutate(&value)
			if validatePhaseSelectorCleanup(value, "passed", plan) == nil {
				t.Fatal("invalid cleanup composition accepted")
			}
			if _, err := readerTransitionMetrics(value, plan); err == nil {
				t.Fatal("invalid cleanup supplied an exact reader subtotal")
			}
		})
	}
	positive := cloneAccountingMeasurement(t, base)
	positive.SelectorCleanup.Deleted, positive.SelectorCleanup.MaxDeleted = 1, 1
	positive.SelectorCleanup.StoreReadAttempts, positive.SelectorCleanup.StoreWriteAttempts = 9, 1
	positive.Metrics.LifecycleDeleted, positive.Metrics.MaxLifecycleDeletesTurn = 1, 1
	positive.Metrics.ControlReads = max(positive.Metrics.ControlReads, 9)
	positive.Metrics.StoreRows = 0
	if validatePhaseSelectorCleanup(positive, "passed", plan) == nil {
		t.Fatal("cleanup deletion exceeded independently observed store rows")
	}
}

func TestReceiptSelectorCleanupHistoricalAndIncompleteRefusal(t *testing.T) {
	plan := receiptHandoffTestPlan(t)
	base := accountingFixtureWorkProjection(t, plan)[slices.Index(plan.PhaseOrder, "physical_delta_b")]
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema, PlanV4Schema} {
		t.Run(schema, func(t *testing.T) {
			legacy := plan
			legacy.Schema = schema
			if validatePhaseSelectorCleanup(base, "passed", legacy) == nil {
				t.Fatal("historical schema accepted V5 evidence")
			}
			value := base
			value.SelectorCleanup = nil
			if err := validatePhaseSelectorCleanup(value, "passed", legacy); err != nil {
				t.Fatal("historical omitted field refused", err)
			}
			metrics, err := readerTransitionMetrics(value, legacy)
			if err != nil || metrics != value.Metrics {
				t.Fatal("historical metrics were normalized", err)
			}
		})
	}
	for _, outcome := range []string{"stopped", "not_run"} {
		if validatePhaseSelectorCleanup(base, outcome, plan) == nil {
			t.Fatal("incomplete phase claimed successful cleanup", outcome)
		}
		prefix := base
		prefix.SelectorCleanup = nil
		if err := validatePhaseSelectorCleanup(prefix, outcome, plan); err != nil {
			t.Fatal("absence of successful proof refused", outcome, err)
		}
	}
	for _, phase := range []string{"preflight", "warm_noop", "archive_restore", "teardown"} {
		value := base
		value.Phase = phase
		if validatePhaseSelectorCleanup(value, "passed", plan) == nil {
			t.Fatal("unrelated phase accepted cleanup proof", phase)
		}
	}

	// Appending an omitted field must not alter the existing measurement wire.
	value := base
	value.SelectorCleanup = nil
	legacy := struct {
		Phase              string                         `json:"phase"`
		StartEventOrdinal  uint64                         `json:"start_event_ordinal"`
		FinishEventOrdinal uint64                         `json:"finish_event_ordinal"`
		Metrics            ReceiptMetrics                 `json:"metrics"`
		ChildProcessRoles  []Count                        `json:"child_process_roles"`
		DispatchAccounting *DispatchAccountingMeasurement `json:"dispatch_accounting,omitempty"`
		NativeObservation  *ProcessObservation            `json:"native_observation,omitempty"`
	}{value.Phase, value.StartEventOrdinal, value.FinishEventOrdinal, value.Metrics,
		value.ChildProcessRoles, value.DispatchAccounting, value.NativeObservation}
	want, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(value)
	if err != nil || string(got) != string(want) {
		t.Fatal("historical measurement bytes changed", err)
	}
}

func TestReceiptSelectorCleanupV5ReaderRemainsExact(t *testing.T) {
	plan := receiptHandoffTestPlan(t)
	measurements := accountingFixtureWorkProjection(t, plan)
	measurement := measurements[slices.Index(plan.PhaseOrder, "physical_delta_b")]
	authorities := []AuthorityPhaseResult{
		{Phase: "warm_noop", Outcome: "passed", AuthorityState: AuthorityState{Current: true, SearchGenerationSHA256: testDigest("before")}},
		{Phase: "physical_delta_b", Outcome: "passed", AuthorityState: AuthorityState{Current: true, SearchGenerationSHA256: testDigest("after")}},
	}
	host := executionFreezeTestHost()
	pressure, err := expectedExecutionPressureGeometry(plan, host)
	if err != nil {
		t.Fatal(err)
	}
	// This isolates the reader protocol and supplies no native injection facts.
	projection := plan
	projection.FailurePoints = nil
	transitions := completeTestTransitions(t, projection,
		ExecutionFreeze{Host: host, Pressure: pressure, Profile: ExecutionProfile{Epochs: correctedExecutionServerEpochs()}}, authorities, measurements)
	transition := transitions[slices.Index(transitionPhases, measurement.Phase)]
	authority := map[string]AuthorityPhaseResult{authorities[0].Phase: authorities[0], authorities[1].Phase: authorities[1]}
	metrics, err := readerTransitionMetrics(measurement, plan)
	if err != nil || validateReaderTransition(*transition.Reader, transition.StartEventOrdinal, transition.FinishEventOrdinal, authority, metrics, plan) != nil {
		t.Fatal("isolated exact reader rejected", err)
	}
	if validateReaderTransition(*transition.Reader, transition.StartEventOrdinal, transition.FinishEventOrdinal, authority, measurement.Metrics, plan) == nil {
		t.Fatal("raw combined total bypassed exact reader oracle")
	}
	for _, mutate := range []func(*ReaderTransition){
		func(v *ReaderTransition) { v.LifecycleAttemptsWhileHeld++ },
		func(v *ReaderTransition) { v.LifecycleAttemptsAfterRelease++ },
		func(v *ReaderTransition) { v.HeldLifecycleScanned = ptr(uint64(1)) },
		func(v *ReaderTransition) { v.PostReleaseLifecycleScanned = ptr(uint64(1)) },
		func(v *ReaderTransition) { v.PostReleaseOldOutcome = "deleted" },
	} {
		changed := *transition.Reader
		mutate(&changed)
		if validateReaderTransition(changed, transition.StartEventOrdinal, transition.FinishEventOrdinal, authority, metrics, plan) == nil {
			t.Fatal("cleanup composition relaxed reader facts")
		}
	}
	if !reflect.DeepEqual(measurement, measurements[slices.Index(plan.PhaseOrder, measurement.Phase)]) {
		t.Fatal("reader validation mutated the measurement")
	}
}

// Real HTTP/token choreography with modeled native response counters. Native
// log observations and their independent accounting join have separate tests.
func TestReceiptSelectorCleanupV5HTTPRetention(t *testing.T) {
	plan := receiptHandoffTestPlan(t)
	for _, mode := range []string{"complete", "missing_row", "wrong_epoch", "missing_final", "already_accepted", "already_retained"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			input := sha256.Sum256([]byte("fixture-input"))
			value := SelectorCleanupEvidence{Schema: SelectorHandoffCleanupSchema, Phase: 2,
				InputSHA256: "sha256:" + hex.EncodeToString(input[:]), SelectedRuntimeSHA256: testDigest("selected"),
				Turns: 1, StoreReadAttempts: 3, Done: true}
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}))
			reader.plan, reader.projection.Phase, reader.finalUsed = plan, "cold", true
			reader.tail = epochTailReadiness{Status: "ready", SelectedRuntimeSHA256: value.SelectedRuntimeSHA256}
			reader.run.epoch.Epoch, reader.run.attemptInput = 1, input
			row := ExecutionPhaseInspection{Phase: "cold", ServerEpoch: 1, Final: &ExecutionInspectionFinal{}}
			switch mode {
			case "wrong_epoch":
				row.ServerEpoch = 2
			case "missing_final":
				row.Final = nil
			case "already_accepted":
				row.SelectorAccepted = true
			case "already_retained":
				row.SelectorCleanup = &value
			}
			if mode != "missing_row" {
				reader.evidence.rows = []ExecutionPhaseInspection{row}
			}
			err := reader.cleanupSelectorHandoff(t.Context())
			if mode != "complete" {
				if err == nil || calls.Load() != 0 {
					t.Fatal("invalid retention custody invoked cleanup", err)
				}
				return
			}
			if err != nil || calls.Load() != 1 || reader.evidence.rows[0].SelectorCleanup == nil ||
				*reader.evidence.rows[0].SelectorCleanup != value {
				t.Fatal("successful HTTP evidence not retained", err)
			}
			clone := cloneInspectionEvidence(reader.evidence.rows)
			clone[0].SelectorCleanup.Turns++
			if *reader.evidence.rows[0].SelectorCleanup != value {
				t.Fatal("returned evidence aliases retained cleanup")
			}
		})
	}
}
