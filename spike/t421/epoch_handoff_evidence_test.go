package t421

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Reports, HTTP terminals and process-join facts are supplied models. Parsing,
// work projection and the independent HTTP/log join use the production path;
// this fixture performs no deletion and claims no native store/SA proof.
func modeledHandoffEvidence(t *testing.T, plan Plan, deleted, transactions [4]uint64) (executionJoinedWork, []ExecutionPhaseInspection, executionReceiptMetrics) {
	t.Helper()
	input := [32]byte{1}
	inputDigest := "sha256:" + hex.EncodeToString(input[:])
	logs := map[uint32]string{2: lifecycleTestBindings(2), 3: lifecycleTestBindings(3), 4: lifecycleTestBindings(4)}
	var rows []ExecutionPhaseInspection
	for slot, bound := range plan.SelectorHandoffCleanup.Phases {
		index := -1
		for i, phase := range plan.PhaseOrder {
			if phase == bound.Phase {
				index = i
			}
		}
		if index < 0 {
			t.Fatal("cleanup phase missing")
		}
		producer := uint32(bound.ServerEpoch + 1)
		if bound.Phase == "physical_delta_b" {
			for attempt := uint64(1); attempt <= 2; attempt++ {
				logs[producer] += modeledHandoffLine(t, handoffRetentionPrefix, executionRetentionEvent{
					Schema: "t422-retention-turn-v1", Epoch: 1, Phase: 4,
					epochRetentionSweep: epochRetentionSweep{Attempt: attempt, Completeness: "exact"},
				})
			}
		}
		value := SelectorCleanupEvidence{Schema: SelectorHandoffCleanupSchema, Phase: uint32(index + 1),
			InputSHA256: inputDigest, SelectedRuntimeSHA256: testDigest(bound.Phase + "-runtime")}
		remaining := deleted[slot]
		for remaining > 16 {
			value.Turns++
			value.Deleted += 16
			value.MaxDeleted = 16
			value.StoreReadAttempts += 7
			value.StoreWriteAttempts++
			logs[producer] += modeledHandoffLine(t, handoffCleanupPrefix, value)
			remaining -= 16
		}
		value.Turns++
		value.Deleted += remaining
		value.MaxDeleted = max(value.MaxDeleted, remaining)
		value.Done = true
		if deleted[slot] == 0 {
			value.StoreReadAttempts += 3
		} else {
			value.StoreReadAttempts += 9
			value.StoreWriteAttempts++
			if remaining > 1 {
				value.StoreWriteAttempts++
			}
		}
		logs[producer] += modeledHandoffLine(t, handoffCleanupPrefix, value)
		rows = append(rows, ExecutionPhaseInspection{ServerEpoch: bound.ServerEpoch, Phase: bound.Phase,
			SelectorAccepted: true, SelectorCleanup: &value,
			Final: &ExecutionInspectionFinal{Projection: PhaseStateProjection{Phase: bound.Phase}}})
	}
	var work executionJoinedWork
	var out executionReceiptMetrics
	for _, producer := range []uint32{2, 3, 4} {
		attempts, err := observeExecutionAttempts([]byte(logs[producer]), plan, producer, input, true)
		if err != nil || !attempts.Complete || !attempts.Handoff.ScanComplete {
			t.Fatalf("parse modeled producer %d: %v", producer, err)
		}
		record := executionJoinedWorkRecord{Producer: producer, Input: input, Joined: true, SessionEmpty: true, Attempts: attempts}
		work.Records[executionJoinedWorkSlot(producer)] = record
		for _, phase := range executionProducerPhases(producer) {
			if !addExecutionWorkRecordMetrics(&out.Metrics[phase-1], record, int(phase-1)) {
				t.Fatal("project parsed native work")
			}
		}
	}
	// Explicit supplied SA snapshot, independent of the native work parser.
	// The native projection must not manufacture transactions or submitted rows.
	for slot, row := range rows {
		metric := &out.Metrics[row.SelectorCleanup.Phase-1]
		if metric.StoreTransactions != 0 || metric.StoreRows != 0 {
			t.Fatal("native handoff projection manufactured SA work")
		}
		metric.StoreTransactions, metric.StoreRows = CountMetric(transactions[slot]), CountMetric(deleted[slot])
	}
	return work, rows, out
}

func modeledHandoffLine(t *testing.T, prefix string, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return "2026/09/24 01:02:03 " + prefix + string(raw) + "\n"
}

func TestExecutionHandoffEvidenceModeledComposition(t *testing.T) {
	plan := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	for _, counts := range []struct {
		name         string
		deleted      [4]uint64
		transactions [4]uint64
		turns        [4]uint64
		reads        [4]uint64
		maximum      [4]uint64
	}{
		{"empty", [4]uint64{}, [4]uint64{1, 1, 1, 1}, [4]uint64{1, 3, 1, 1}, [4]uint64{3, 3, 3, 3}, [4]uint64{}},
		{"nonempty", [4]uint64{0, 17, 2, 18}, [4]uint64{1, 2, 1, 2}, [4]uint64{1, 4, 1, 2}, [4]uint64{3, 16, 9, 16}, [4]uint64{0, 16, 2, 16}},
	} {
		t.Run(counts.name, func(t *testing.T) {
			work, rows, out := modeledHandoffEvidence(t, plan, counts.deleted, counts.transactions)
			if err := composeExecutionHandoffEvidence(plan, work, rows, true, &out); err != nil {
				t.Fatal("join modeled HTTP terminals to parsed logs", err)
			}
			for slot, row := range rows {
				index := row.SelectorCleanup.Phase - 1
				metric := out.Metrics[index]
				if uint64(metric.LifecycleOwnerTurns) != counts.turns[slot] || uint64(metric.LifecycleDeleted) != counts.deleted[slot] ||
					uint64(metric.MaxLifecycleDeletesTurn) != counts.maximum[slot] || uint64(metric.ControlReads) != counts.reads[slot] ||
					out.SelectorCleanup[index] == nil || *out.SelectorCleanup[index] != *row.SelectorCleanup {
					t.Fatalf("%s lost exact observed work: %+v", row.Phase, metric)
				}
			}
			cloned := cloneInspectionEvidence(rows)
			cloned[0].SelectorCleanup.Deleted++
			if cloned[0].SelectorCleanup == rows[0].SelectorCleanup || rows[0].SelectorCleanup.Deleted != 0 {
				t.Fatal("inspection clone aliases cleanup evidence")
			}
			out.SelectorCleanup[1].Deleted++
			if rows[0].SelectorCleanup.Deleted != 0 || work.Records[0].Attempts.Handoff.Cleanup[1].Deleted != 0 {
				t.Fatal("composed evidence aliases HTTP or native log")
			}
		})
	}
}

func TestExecutionHandoffEvidenceRefusals(t *testing.T) {
	plan := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	baseWork, baseRows, baseOut := modeledHandoffEvidence(t, plan, [4]uint64{0, 17, 2, 18}, [4]uint64{1, 2, 1, 2})
	for _, mode := range []string{"missing-row", "duplicate-row", "wrong-phase", "wrong-epoch", "wrong-input", "wrong-selector", "missing-http", "changed-http", "missing-log", "not-terminal", "failed-terminal", "incomplete-scan", "unbound-source", "incomplete-attempt-scan", "unjoined", "live-session", "missing-final", "wrong-final", "underreported-work", "underreported-reads", "underreported-transactions", "underreported-rows", "missing-retention"} {
		t.Run(mode, func(t *testing.T) {
			work, rows, out := baseWork, cloneInspectionEvidence(baseRows), baseOut
			switch mode {
			case "missing-row":
				rows = rows[1:]
			case "duplicate-row":
				rows = append(rows, rows[0])
			case "wrong-phase":
				rows[0].Phase = "warm_noop"
			case "wrong-epoch":
				rows[0].ServerEpoch++
			case "wrong-input":
				work.Records[0].Input[0]++
			case "wrong-selector":
				rows[0].SelectorCleanup.SelectedRuntimeSHA256 = testDigest("other-selector")
			case "missing-http":
				rows[0].SelectorCleanup = nil
			case "changed-http":
				rows[0].SelectorCleanup.StoreReadAttempts++
			case "missing-log":
				work.Records[0].Attempts.Handoff.Cleanup[1] = SelectorCleanupEvidence{}
			case "not-terminal":
				rows[0].SelectorCleanup.Done = false
				work.Records[0].Attempts.Handoff.Cleanup[1].Done = false
			case "failed-terminal":
				rows[0].SelectorCleanup.Failed = true
				work.Records[0].Attempts.Handoff.Cleanup[1].Failed = true
			case "incomplete-scan":
				work.Records[0].Attempts.Handoff.ScanComplete = false
			case "unbound-source":
				work.Records[0].Attempts.SourceBound = false
			case "incomplete-attempt-scan":
				work.Records[0].Attempts.ScanComplete = false
			case "unjoined":
				work.Records[0].Joined = false
			case "live-session":
				work.Records[0].SessionEmpty = false
			case "missing-final":
				rows[0].Final = nil
			case "wrong-final":
				rows[0].Final.Projection.Phase = "physical_delta_b"
			case "underreported-work":
				out.Metrics[1].LifecycleOwnerTurns--
			case "underreported-reads":
				out.Metrics[1].ControlReads--
			case "underreported-transactions":
				out.Metrics[1].StoreTransactions--
			case "underreported-rows":
				out.Metrics[3].StoreRows--
			case "missing-retention":
				work.Records[0].Attempts.Handoff.Retention[1] = epochRetentionSweep{}
			}
			if err := composeExecutionHandoffEvidence(plan, work, rows, true, &out); err == nil {
				t.Fatal("unsubstantiated successful handoff accepted")
			}
		})
	}
	rows := cloneInspectionEvidence(baseRows)
	rows[3].SelectorAccepted = false
	partial := baseOut
	if err := composeExecutionHandoffEvidence(plan, baseWork, rows, false, &partial); err != nil || partial.SelectorCleanup[5] != nil || partial.SelectorCleanup[4] == nil {
		t.Fatal("stopped phase promoted or prior success discarded", err)
	}
	if err := composeExecutionHandoffEvidence(plan, baseWork, rows, true, &partial); err == nil {
		t.Fatal("stopped handoff satisfied complete execution")
	}
}

func TestExecutionHandoffEvidenceHistoricalOmission(t *testing.T) {
	plan := restoreContinuityTestPlan(t)
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema, PlanV4Schema} {
		t.Run(schema, func(t *testing.T) {
			plan := plan
			plan.Schema = schema // Isolate only the new field's historical gate.
			measurement := PhaseMeasurement{Phase: "cold"}
			raw, err := json.Marshal(measurement)
			if err != nil || strings.Contains(string(raw), "selector_cleanup") || validatePhaseSelectorCleanup(measurement, "passed", plan) != nil {
				t.Fatal("historical omitted field changed", err)
			}
			measurement.SelectorCleanup = &SelectorCleanupEvidence{}
			if validatePhaseSelectorCleanup(measurement, "passed", plan) == nil {
				t.Fatal("historical receipt admitted V5 evidence")
			}
			rows := []ExecutionPhaseInspection{{Phase: "cold", SelectorCleanup: measurement.SelectorCleanup}}
			var out executionReceiptMetrics
			if composeExecutionHandoffEvidence(plan, executionJoinedWork{}, rows, false, &out) == nil {
				t.Fatal("historical composer admitted V5 evidence")
			}
			rows[0].SelectorCleanup = nil
			if composeExecutionHandoffEvidence(plan, executionJoinedWork{}, rows, false, &out) != nil || !reflect.DeepEqual(out, executionReceiptMetrics{}) {
				t.Fatal("historical omission manufactured work")
			}
		})
	}
}
