package t421

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

func handoffTestPlan(t *testing.T) Plan {
	t.Helper()
	plan := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func handoffTestLine(t *testing.T, prefix string, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return "2026/09/24 01:02:03 " + prefix + string(raw) + "\n"
}

func handoffTestCleanup(phase uint32) SelectorCleanupEvidence {
	return SelectorCleanupEvidence{Schema: SelectorHandoffCleanupSchema, Phase: phase,
		InputSHA256: "sha256:01" + strings.Repeat("00", 31), SelectedRuntimeSHA256: testDigest("selected", fmt.Sprint(phase)),
		Turns: 1, StoreReadAttempts: 3, Done: true}
}

func handoffTestCleanupRows(t *testing.T, phase uint32, rows uint64) string {
	t.Helper()
	value := handoffTestCleanup(phase)
	if rows == 0 {
		return handoffTestLine(t, handoffCleanupPrefix, value)
	}
	var raw strings.Builder
	value.Turns, value.StoreReadAttempts, value.Done = 0, 0, false
	for rows >= 16 {
		value.Turns++
		value.Deleted += 16
		value.MaxDeleted = 16
		value.StoreReadAttempts += 7
		value.StoreWriteAttempts++
		raw.WriteString(handoffTestLine(t, handoffCleanupPrefix, value))
		rows -= 16
	}
	value.Turns++
	value.Deleted += rows + 1
	value.MaxDeleted = max(value.MaxDeleted, rows+1)
	value.StoreReadAttempts += 9
	value.StoreWriteAttempts++
	if rows != 0 {
		value.StoreWriteAttempts++
	}
	value.Done = true
	raw.WriteString(handoffTestLine(t, handoffCleanupPrefix, value))
	return raw.String()
}

func handoffTestRetention(t *testing.T) string {
	t.Helper()
	var raw strings.Builder
	for attempt := uint64(1); attempt <= 2; attempt++ {
		raw.WriteString(handoffTestLine(t, handoffRetentionPrefix, executionRetentionEvent{
			Schema: "t422-retention-turn-v1", Epoch: 1, Phase: 4,
			epochRetentionSweep: epochRetentionSweep{Attempt: attempt, Completeness: "exact"},
		}))
	}
	return raw.String()
}

func TestExecutionHandoffNativePrefixes(t *testing.T) {
	plan := handoffTestPlan(t)
	for _, test := range []struct {
		name                           string
		producer                       uint32
		phase                          int
		raw                            string
		turns, deleted, reads, maximum uint64
	}{
		{"cold and physical", 2, 4, handoffTestCleanupRows(t, 2, 0) + handoffTestRetention(t) + handoffTestCleanupRows(t, 4, 32), 5, 33, 23, 16},
		{"logical", 3, 5, handoffTestCleanupRows(t, 5, 1), 1, 2, 9, 2},
		{"return", 4, 6, handoffTestCleanupRows(t, 6, 10000), 626, 10001, 4384, 16},
		{"unrelated server", 6, 13, "", 0, 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(test.producer)+test.raw), plan, test.producer, [32]byte{1}, true)
			if err != nil || !got.Complete || !got.Handoff.ScanComplete {
				t.Fatalf("native handoff prefix incomplete: %+v %v", got.Handoff, err)
			}
			record := executionJoinedWorkRecord{Producer: test.producer, Input: [32]byte{1}, Attempts: got}
			if !validExecutionHandoffRecord(plan, record) || !executionHandoffPhaseClosed(plan, record, test.phase-1) {
				t.Fatal("complete native handoff was not closed")
			}
			var metrics ReceiptMetrics
			if !addExecutionWorkRecordMetrics(&metrics, record, test.phase-1) || uint64(metrics.LifecycleOwnerTurns) != test.turns ||
				uint64(metrics.LifecycleDeleted) != test.deleted || uint64(metrics.ControlReads) != test.reads ||
				uint64(metrics.MaxLifecycleDeletesTurn) != test.maximum || metrics.StoreTransactions != 0 || metrics.StoreRows != 0 {
				t.Fatalf("native counts were not added exactly once or minted SA work: %+v", metrics)
			}
		})
	}
}

func TestExecutionHandoffRefusesMalformedAndPreservesNativeExcess(t *testing.T) {
	plan := handoffTestPlan(t)
	for _, test := range []struct {
		name    string
		edit    func(*SelectorCleanupEvidence)
		retains bool
	}{
		{"input", func(v *SelectorCleanupEvidence) { v.InputSHA256 = testDigest("wrong") }, false},
		{"phase", func(v *SelectorCleanupEvidence) { v.Phase = 6 }, false},
		{"schema", func(v *SelectorCleanupEvidence) { v.Schema += "-bad" }, false},
		{"selector", func(v *SelectorCleanupEvidence) { v.SelectedRuntimeSHA256 = "" }, false},
		{"skipped turn", func(v *SelectorCleanupEvidence) { v.Turns = 2 }, false},
		{"max mismatch", func(v *SelectorCleanupEvidence) { v.MaxDeleted = 1 }, false},
		{"native delete excess", func(v *SelectorCleanupEvidence) { v.Deleted, v.MaxDeleted = 17, 17 }, true},
		{"native read excess", func(v *SelectorCleanupEvidence) { v.StoreReadAttempts = 10 }, true},
		{"native write excess", func(v *SelectorCleanupEvidence) { v.StoreWriteAttempts = 3 }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := handoffTestCleanup(5)
			test.edit(&value)
			got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(3)+handoffTestLine(t, handoffCleanupPrefix, value)), plan, 3, [32]byte{1}, true)
			if err == nil || got.Complete || got.Handoff.ScanComplete || (got.Handoff.Cleanup[4] == value) != test.retains {
				t.Fatalf("malformed/excess prefix handling differs: %+v %v", got.Handoff, err)
			}
		})
	}
	valid := handoffTestLine(t, handoffCleanupPrefix, handoffTestCleanup(5))
	for _, tail := range []string{valid + valid, strings.TrimSuffix(valid, "\n"), "junk" + valid,
		strings.Replace(valid, `"done":true`, `"unknown":1,"done":true`, 1),
		strings.Replace(valid, `"done":true`, `"done": true`, 1),
		strings.Repeat("z", maxExecutionAttemptLine-4) + valid,
	} {
		if got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(3)+tail), plan, 3, [32]byte{1}, true); err == nil || got.Complete {
			t.Fatal("invalid exact native line accepted", err)
		}
	}
	if _, err := observeExecutionAttempts([]byte(valid+lifecycleTestBindings(3)), plan, 3, [32]byte{1}, true); err == nil {
		t.Fatal("unbound native handoff accepted")
	}
}

func TestExecutionHandoffStoppedPrefixAndLegacyFence(t *testing.T) {
	plan := handoffTestPlan(t)
	failed := handoffTestCleanup(4)
	failed.Deleted, failed.MaxDeleted, failed.StoreReadAttempts, failed.StoreWriteAttempts = 16, 16, 7, 1
	failed.Done, failed.Failed = false, true
	raw := lifecycleTestBindings(2) + handoffTestCleanupRows(t, 2, 0) + handoffTestRetention(t) + handoffTestLine(t, handoffCleanupPrefix, failed)
	got, err := observeExecutionAttempts([]byte(raw), plan, 2, [32]byte{1}, true)
	record := executionJoinedWorkRecord{Producer: 2, Input: [32]byte{1}, Attempts: got}
	if err == nil || got.Complete || !got.Handoff.ScanComplete || !executionHandoffPhaseClosed(plan, record, 1) || executionHandoffPhaseClosed(plan, record, 3) {
		t.Fatalf("failed prefix changed prior phase closure: %+v %v", got.Handoff, err)
	}
	var metrics ReceiptMetrics
	if !addExecutionWorkRecordMetrics(&metrics, record, 3) || metrics.LifecycleOwnerTurns != 3 || metrics.LifecycleDeleted != 16 || metrics.ControlReads != 7 {
		t.Fatal("failed native work lost", metrics)
	}
	for _, schema := range []string{PlanV3Schema, PlanV4Schema} {
		legacy := plan
		legacy.Schema = schema
		observed, err := observeExecutionAttempts([]byte(raw), legacy, 2, [32]byte{1}, true)
		if err != nil || observed.Handoff != (ExecutionHandoffObservation{}) {
			t.Fatal("historical log interpretation changed", schema, err)
		}
	}
	footer := "TFE1:4:8:sha256:01" + strings.Repeat("00", 31) + "\n"
	tail := handoffTestCleanupRows(t, 6, 0)
	if seen, err := executionTerminalFooterForPlan([]byte(footer+tail), [32]byte{1}, PlanV5Schema); !seen || err == nil {
		t.Fatal("V5 handoff report crossed the hard-death terminal fence")
	}
	if seen, err := executionTerminalFooterForPlan([]byte(footer+tail), [32]byte{1}, PlanV4Schema); !seen || err != nil {
		t.Fatal("historical footer interpretation changed", err)
	}
}

func TestExecutionHandoffMetricOverflow(t *testing.T) {
	record := executionJoinedWorkRecord{Producer: 3}
	record.Attempts.Handoff.Cleanup[4] = handoffTestCleanup(5)
	for _, metrics := range []ReceiptMetrics{
		{LifecycleOwnerTurns: CountMetric(math.MaxUint64)},
		{ControlReads: CountMetric(math.MaxUint64)},
	} {
		if addExecutionWorkRecordMetrics(&metrics, record, 4) {
			t.Fatal("handoff addition overflow accepted")
		}
	}
}

func TestExecutionHandoffStoppedCleanupReadsRemainUnavailable(t *testing.T) {
	plan := handoffTestPlan(t)
	_, work, dispatch, store, inspection, _ := receiptMetricTestEvidence(t)
	failed := handoffTestCleanup(4)
	failed.Done, failed.Failed = false, true
	failed.Deleted, failed.MaxDeleted, failed.StoreReadAttempts, failed.StoreWriteAttempts = 16, 16, 7, 1
	raw := lifecycleTestBindings(2) + handoffTestCleanupRows(t, 2, 0) + handoffTestRetention(t) + handoffTestLine(t, handoffCleanupPrefix, failed)
	observed, err := observeExecutionAttempts([]byte(raw), plan, 2, [32]byte{1}, true)
	if err == nil {
		t.Fatal("failed native cleanup became complete")
	}
	work.Records[0].Input = [32]byte{1}
	work.Records[0].Attempts.Handoff = observed.Handoff
	work.Records[0].Attempts.Complete = false
	work.Records[0].Attempts.ScanComplete = true
	work.Records[0].IndexOffers.ScanComplete = true
	for index := 1; index < len(work.Records); index++ {
		work.Records[index] = executionJoinedWorkRecord{}
	}
	inspection = inspection[:3]
	inspection[0].SelectorCleanup = &observed.Handoff.Cleanup[1]
	inspection[2].SelectorAccepted, inspection[2].Final = false, nil
	got, err := composeExecutionStoppedMetricPrefix(plan, "physical_delta_b", work, dispatch, store, inspection)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metrics.Metrics[3].LifecycleOwnerTurns != 3 || got.Metrics.Metrics[3].LifecycleDeleted != 16 || got.Metrics.Metrics[3].ControlReads != 7 ||
		!slices.Contains(got.Unavailable[3], "control_reads") || !slices.Contains(got.Unavailable[3], "lifecycle_owner_turns") ||
		slices.Contains(got.Unavailable[1], "control_reads") || got.Metrics.Metrics[1].ControlReads != 6 {
		t.Fatalf("native failed reads lost their prefix/unavailable coverage: %+v %+v", got.Metrics.Metrics[3], got.Unavailable)
	}
}
