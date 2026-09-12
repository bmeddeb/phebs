package t421

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func workspaceTestBinding(producer uint32) string {
	return fmt.Sprintf("WBB1:%d:sha256:01%s\n", producer, strings.Repeat("00", 31))
}

func workspaceTestEvent(producer, phase uint32, kind byte, sequence, logical, allocated uint64) string {
	if kind == 'S' {
		return fmt.Sprintf("WB1:%X:%X%c:%016x:%016x:%016x\n", producer, phase, kind, sequence, logical, allocated)
	}
	return fmt.Sprintf("WB1:%X:%X%c:%016x\n", producer, phase, kind, sequence)
}

func workspaceTestPair(producer, phase uint32, sequence, logical, allocated uint64) string {
	return workspaceTestEvent(producer, phase, 'B', sequence, 0, 0) + workspaceTestEvent(producer, phase, 'S', sequence, logical, allocated)
}

// Borrowing FD6 alone admits no event. Only explicitly wired positions have
// derived slots; unsupported early positions still refuse.
func TestExecutionWorkspaceEarlyEventsRemainRefused(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, row := range []struct{ producer, phase uint32 }{{2, 5}, {3, 4}, {3, 6}, {4, 9}, {5, 7}, {6, 8}} {
		t.Run(fmt.Sprintf("%d/%d", row.producer, row.phase), func(t *testing.T) {
			var out ExecutionWorkspaceByteObservation
			input := "sha256:01" + strings.Repeat("00", 31)
			_, err := observeWorkspaceByteEvent([]byte(workspaceTestBinding(row.producer)), plan, row.producer, input, &out)
			if err != nil {
				t.Fatal("existing binding refused", err)
			}
			_, err = observeWorkspaceByteEvent([]byte(workspaceTestEvent(row.producer, row.phase, 'B', 1, 0, 0)), plan, row.producer, input, &out)
			if err == nil || !out.Unavailable || out.Complete || out.Phases != ([15]ExecutionWorkspaceBytePhase{}) || workspaceCheckpointMaximum(row.producer, row.phase) != 0 {
				t.Fatal("descriptor prerequisite invented sample coverage", out, err)
			}
		})
	}
}

func TestExecutionWorkspaceBytesJoined(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, producer := range []uint32{5, 6} {
		phase := uint32(9)
		if producer == 6 {
			phase = 13
		}
		raw := lifecycleTestBindings(producer) + workspaceTestBinding(producer) + workspaceTestPair(producer, phase, 1, 50, 100) + workspaceTestPair(producer, phase, 2, 10, 200)
		got, err := observeExecutionAttempts([]byte(raw), plan, producer, [32]byte{1}, true)
		want := ExecutionWorkspaceBytePhase{Attempts: 2, Completed: 2, Maximum: custodybytes.Sample{LogicalBytes: 50, AllocatedBytes: 200}}
		if err != nil || !got.Complete || !got.WorkspaceBytes.Complete || got.WorkspaceBytes.Unavailable || got.WorkspaceBytes.Phases[phase-1] != want {
			t.Fatal(got.WorkspaceBytes, err)
		}
		zero, err := observeExecutionAttempts([]byte(lifecycleTestBindings(producer)+workspaceTestBinding(producer)), plan, producer, [32]byte{1}, true)
		if err != nil || !zero.WorkspaceBytes.Complete || zero.WorkspaceBytes.Phases != ([15]ExecutionWorkspaceBytePhase{}) {
			t.Fatal("zero stream is not phase coverage", zero.WorkspaceBytes, err)
		}
		missing, err := observeExecutionAttempts([]byte(lifecycleTestBindings(producer)), plan, producer, [32]byte{1}, true)
		if err != nil || missing.WorkspaceBytes.Complete || missing.WorkspaceBytes.Bound {
			t.Fatal("missing stream is not bound zero", missing.WorkspaceBytes, err)
		}
	}
}

func TestExecutionWorkspaceBytesRefusedSuffixPreservesMaxima(t *testing.T) {
	plan := accountingTestPlan(t)
	prefix := lifecycleTestBindings(5) + workspaceTestBinding(5) + workspaceTestPair(5, 9, 1, 50, 100)
	begin := workspaceTestEvent(5, 9, 'B', 2, 0, 0)
	success := workspaceTestEvent(5, 9, 'S', 2, 1, 1)
	for _, test := range []struct{ name, tail string }{
		{"missing success", begin},
		{"failure", begin + workspaceTestEvent(5, 9, 'F', 2, 0, 0)},
		{"success without begin", success},
		{"duplicate sequence", workspaceTestPair(5, 9, 1, 200, 300)},
		{"missing pair", workspaceTestPair(5, 9, 3, 200, 300)},
		{"wrong result sequence", begin + workspaceTestEvent(5, 9, 'S', 3, 200, 300)},
		{"overlapping begin", begin + begin},
		{"producer drift", workspaceTestPair(6, 13, 2, 200, 300)},
		{"phase drift", begin + workspaceTestEvent(5, 11, 'S', 2, 200, 300)},
		{"wrong phase", workspaceTestPair(5, 8, 2, 200, 300)},
		{"wrong phase hex", strings.Replace(begin, ":9B:", ":GB:", 1)},
		{"noncanonical hex", begin + strings.Replace(success, "0000000000000001", "000000000000000A", 1)},
		{"zero sequence", workspaceTestPair(5, 9, 0, 200, 300)},
		{"unknown kind", workspaceTestEvent(5, 9, 'Q', 2, 0, 0)},
		{"partial", strings.TrimSuffix(begin, "\n")},
		{"duplicate binding", workspaceTestBinding(5)},
		{"embedded", "ordinary " + begin},
		{"long fragmented reserved", strings.Repeat("x", maxExecutionAttemptLine-1) + begin},
		{"unrelated truncated tail", "ordinary incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(prefix+test.tail), plan, 5, [32]byte{1}, true)
			if err == nil || got.WorkspaceBytes.Complete || !got.WorkspaceBytes.Unavailable || got.WorkspaceBytes.Phases[8].Completed != 1 ||
				got.WorkspaceBytes.Phases[8].Maximum != (custodybytes.Sample{LogicalBytes: 50, AllocatedBytes: 100}) {
				t.Fatal(got.WorkspaceBytes, err)
			}
		})
	}
	for _, raw := range []string{workspaceTestPair(5, 9, 1, 1, 1), strings.Replace(workspaceTestBinding(5), "sha256:01", "sha256:02", 1)} {
		got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(5)+raw), plan, 5, [32]byte{1}, true)
		if err == nil || got.WorkspaceBytes.Bound || got.WorkspaceBytes.Phases != ([15]ExecutionWorkspaceBytePhase{}) {
			t.Fatal("unbound input", got.WorkspaceBytes, err)
		}
	}
	for _, producer := range []uint32{1, 7, 8, 9} {
		var out ExecutionWorkspaceByteObservation
		if seen, err := observeWorkspaceByteEvent([]byte(workspaceTestBinding(producer)), plan, producer, "sha256:01"+strings.Repeat("00", 31), &out); !seen || err == nil || out.Bound {
			t.Fatal("unsupported producer", producer, out, err)
		}
	}
}

func TestExecutionWorkspaceBytesLimits(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, test := range []struct {
		producer, phase uint32
		maximum         uint64
	}{
		{5, 9, uint64(lifecycle.MaxCycleObservationTurns) + 1 + 4},
		{5, 10, 1 + 3},
		{5, 11, uint64(lifecycle.MaxCycleObservationTurns) + 2 + 4},
		{6, 12, 1},
		{6, 13, uint64(lifecycle.MaxCycleObservationTurns) + 2},
		{6, 14, 2},
	} {
		out := ExecutionWorkspaceByteObservation{Bound: true, sequence: test.maximum - 1, phase: test.phase}
		out.Phases[test.phase-1].Attempts = test.maximum - 1
		out.Phases[test.phase-1].Completed = test.maximum - 1
		for _, event := range []string{workspaceTestEvent(test.producer, test.phase, 'B', test.maximum, 0, 0), workspaceTestEvent(test.producer, test.phase, 'S', test.maximum, 1, 2)} {
			if _, err := observeWorkspaceByteEvent([]byte(event), plan, test.producer, "", &out); err != nil {
				t.Fatal("last derived slot refused", test, out, err)
			}
		}
		if _, err := observeWorkspaceByteEvent([]byte(workspaceTestEvent(test.producer, test.phase, 'B', test.maximum+1, 0, 0)), plan, test.producer, "", &out); err == nil || out.Phases[test.phase-1].Completed != test.maximum {
			t.Fatal("extra checkpoint admitted", test, out, err)
		}
	}
	for _, sample := range []custodybytes.Sample{
		{LogicalBytes: plan.WorkEnvelope.MaximumDataLogicalBytes + 1, AllocatedBytes: 1},
		{LogicalBytes: 1, AllocatedBytes: plan.SafetyEnvelope.MaximumDataAllocatedBytes + 1},
	} {
		raw := lifecycleTestBindings(5) + workspaceTestBinding(5) + workspaceTestPair(5, 9, 1, sample.LogicalBytes, sample.AllocatedBytes)
		got, err := observeExecutionAttempts([]byte(raw), plan, 5, [32]byte{1}, true)
		if err == nil || !got.WorkspaceBytes.LimitExceeded || got.WorkspaceBytes.Unavailable || got.WorkspaceBytes.Phases[8].Maximum != sample || got.WorkspaceBytes.Phases[8].Completed != 1 {
			t.Fatal("completed excess lost or called unavailable", got.WorkspaceBytes, err)
		}
	}
	footer := "TFE1:4:8:sha256:01" + strings.Repeat("00", 31) + "\n"
	if seen, err := executionTerminalFooter([]byte(footer+workspaceTestBinding(5)), [32]byte{1}); !seen || err == nil {
		t.Fatal("post-terminal byte record accepted", seen, err)
	}
}

// Supplied report bytes prove the parser and finite accounting only. Actual
// command ordering and native traversals are separate evidence.
func TestExecutionWorkspaceBytesEpochFiveBoundaries(t *testing.T) {
	plan := accountingTestPlan(t)
	raw := lifecycleTestBindings(6) + workspaceTestBinding(6)
	for index, phase := range []uint32{12, 13, 13, 14, 14} {
		raw += workspaceTestPair(6, phase, uint64(index+1), uint64(index+1), 512)
	}
	got, err := observeExecutionAttempts([]byte(raw), plan, 6, [32]byte{1}, true)
	if err != nil || !got.Complete || !got.WorkspaceBytes.Complete || got.WorkspaceBytes.Unavailable {
		t.Fatal("restored boundary stream", got.WorkspaceBytes, err)
	}
	for _, row := range []struct {
		phase          uint32
		count, logical uint64
	}{{12, 1, 1}, {13, 2, 3}, {14, 2, 5}} {
		want := ExecutionWorkspaceBytePhase{Attempts: row.count, Completed: row.count, Maximum: custodybytes.Sample{LogicalBytes: row.logical, AllocatedBytes: 512}}
		if got.WorkspaceBytes.Phases[row.phase-1] != want {
			t.Fatal(row, got.WorkspaceBytes)
		}
	}
	for _, suffix := range []string{
		workspaceTestPair(6, 15, 6, 99, 99),
		workspaceTestPair(6, 14, 6, 99, 99),
		workspaceTestPair(6, 12, 6, 99, 99),
	} {
		got, err := observeExecutionAttempts([]byte(raw+suffix), plan, 6, [32]byte{1}, true)
		if err == nil || got.WorkspaceBytes.Complete || !got.WorkspaceBytes.Unavailable || got.WorkspaceBytes.Phases[13].Completed != 2 || got.WorkspaceBytes.Phases[13].Maximum.LogicalBytes != 5 {
			t.Fatal("unadmitted suffix lost completed boundary prefix", got.WorkspaceBytes, err)
		}
	}
	prefix := lifecycleTestBindings(6) + workspaceTestBinding(6) + workspaceTestPair(6, 12, 1, 7, 512)
	failure := workspaceTestEvent(6, 13, 'B', 2, 0, 0) + workspaceTestEvent(6, 13, 'F', 2, 0, 0)
	got, err = observeExecutionAttempts([]byte(prefix+failure), plan, 6, [32]byte{1}, true)
	if err == nil || got.WorkspaceBytes.Complete || !got.WorkspaceBytes.Unavailable || got.WorkspaceBytes.Phases[11].Completed != 1 || got.WorkspaceBytes.Phases[11].Maximum.LogicalBytes != 7 || got.WorkspaceBytes.Phases[12].Completed != 0 {
		t.Fatal("failed start erased archive sample", got.WorkspaceBytes, err)
	}
}

func TestExecutionWorkspaceBytesEpochFiveCompactHeadroom(t *testing.T) {
	epochFive := workspaceCheckpointMaximum(6, 12) + workspaceCheckpointMaximum(6, 13) + workspaceCheckpointMaximum(6, 14)
	if epochFive != uint64(lifecycle.MaxCycleObservationTurns)+5 || (epochFive-uint64(lifecycle.MaxCycleObservationTurns))*86 != 430 {
		t.Fatal("fixed boundary byte delta", epochFive)
	}
	workspaceSubtotal := 2*uint64(79) + 86*(epochFive+workspaceCheckpointMaximum(5, 9)+workspaceCheckpointMaximum(5, 10)+workspaceCheckpointMaximum(5, 11))
	if workspaceSubtotal != 1_058_646 || workspaceSubtotal >= 64<<20 {
		t.Fatal("bounded server workspace subtotal", workspaceSubtotal)
	}
	// Other compact families, census bodies and ordinary diagnostics share the
	// unchanged output allowance; this is not whole-output fit evidence.
}

func TestExecutionWorkspaceBytesV3ByteCeilings(t *testing.T) {
	plan := accountingTestPlan(t)
	if plan.Schema != PlanV3Schema || plan.WorkEnvelope.MaximumDataLogicalBytes != 128<<30 || plan.SafetyEnvelope.MaximumDataAllocatedBytes != 128<<30 {
		t.Fatal("prospective V3 byte ceilings changed")
	}
	for _, test := range []struct {
		name   string
		sample custodybytes.Sample
		refuse bool
	}{
		{"above_historical_allocated", custodybytes.Sample{LogicalBytes: 64 << 30, AllocatedBytes: 96<<30 + 1}, false},
		{"both_equal", custodybytes.Sample{LogicalBytes: 128 << 30, AllocatedBytes: 128 << 30}, false},
		{"allocated_one_over", custodybytes.Sample{LogicalBytes: 64 << 30, AllocatedBytes: 128<<30 + 1}, true},
		{"logical_one_over", custodybytes.Sample{LogicalBytes: 128<<30 + 1, AllocatedBytes: 64 << 30}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := lifecycleTestBindings(5) + workspaceTestBinding(5) + workspaceTestPair(5, 9, 1, 4, 8) +
				workspaceTestPair(5, 9, 2, test.sample.LogicalBytes, test.sample.AllocatedBytes)
			got, err := observeExecutionAttempts([]byte(raw), plan, 5, [32]byte{1}, true)
			row := got.WorkspaceBytes.Phases[8]
			if (err != nil) != test.refuse || got.WorkspaceBytes.Complete == test.refuse || got.WorkspaceBytes.LimitExceeded != test.refuse ||
				got.WorkspaceBytes.Unavailable || row.Attempts != 2 || row.Completed != 2 || row.Maximum != test.sample {
				t.Fatal("completed positive sample or limit classification changed", got.WorkspaceBytes, err)
			}
		})
	}
}

func TestExecutionWorkspaceBytesFinishBindsActualWorkspace(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, mode := range []string{"complete", "missing", "missing_recovery", "mismatched_recovery", "failed", "overflow", "unjoined", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			raw := lifecycleTestBindings(5) + "IXB1:5:sha256:01" + strings.Repeat("00", 31) + "\n"
			if mode != "missing" && mode != "legacy" {
				raw += workspaceTestBinding(5) + workspaceTestPair(5, 8, 1, 20, 40) + workspaceTestPair(5, 9, 2, 50, 100)
			}
			output := &checkoutCommandOutput{remaining: int64(len(raw)), cancel: cancel}
			if _, err := output.Write([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			flow := &ExecutionEpochOne{plan: plan}
			if mode != "legacy" {
				flow.workspace = &productionRoot{} // Models required binding, not native admission.
			}
			run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 4}, output: output, attemptInput: [32]byte{1}}
			result := ExecutionEpochOneResult{RootJoined: mode != "unjoined"}
			// Supplied predecessor rows test finish reconciliation, not native
			// preparation or hard-death ownership. The current row must match S.
			for index := range result.RecoverySamples.Points {
				result.RecoverySamples.Points[index] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
			}
			result.RecoverySamples.Points[6].Maximum = custodybytes.Sample{LogicalBytes: 20, AllocatedBytes: 40}
			if mode == "missing_recovery" {
				result.RecoverySamples.Points[5] = ExecutionWorkspaceBytePhase{}
			}
			if mode == "mismatched_recovery" {
				result.RecoverySamples.Points[6].Maximum.LogicalBytes++
			}
			var failure error
			if mode == "failed" {
				failure = ErrExecutionEpochOne
			}
			if mode == "overflow" {
				_, _ = output.Write([]byte("lost\n"))
			}
			err := run.finishAttemptObservation(ctx, &result, executionProcessDeath{}, failure)
			if (err == nil) != (mode == "complete" || mode == "legacy") || result.Attempts.WorkspaceBytes.Complete != (mode == "complete") {
				t.Fatal(mode, result.Attempts.WorkspaceBytes, err)
			}
			if (mode == "failed" || mode == "overflow") && result.Attempts.WorkspaceBytes.Phases[8].Completed != 1 {
				t.Fatal("joined failed positive prefix erased", result.Attempts.WorkspaceBytes)
			}
		})
	}
}
