package t421

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
)

func attemptTestLine(t *testing.T, phase uint32, report any) []byte {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	input := [32]byte{1}
	kind := "job"
	if _, ok := report.(generationscheduler.ChunkLifecycleReport); ok {
		kind = "chunk"
	}
	encoded, err := json.Marshal(executionAttemptReport{Schema: executionAttemptSchema, Producer: 2, Phase: phase,
		InputSHA256: "sha256:" + hex.EncodeToString(input[:]), Kind: kind, Report: raw})
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte("2026/09/07 10:00:00 "+executionAttemptPrefix), encoded...), '\n')
}

func attemptTestJob(event string, attempt int) store.JobLifecycleReport {
	return store.JobLifecycleReport{Schema: store.JobLifecycleSchema, Event: event, JobID: "job:neutral",
		Kind: store.JobCandidate, Target: "example.test/neutral", Attempt: attempt, Outcome: executionJobOutcome(event)}
}

func attemptTestChunk(event, outcome string, attempt int) generationscheduler.ChunkLifecycleReport {
	return generationscheduler.ChunkLifecycleReport{Schema: generationscheduler.ChunkLifecycleSchema, Event: event,
		Identity: "sha256:" + strings.Repeat("1", 64), Stage: extractionpublication.ScheduleStage,
		Generation: "sha256:" + strings.Repeat("2", 64), Attempt: attempt, Outcome: outcome}
}

func TestExecutionAttemptObservedTransitions(t *testing.T) {
	plan := accountingTestPlan(t)
	var raw []byte
	for _, report := range []any{
		attemptTestJob("claimed", 1), attemptTestJob("started", 1), attemptTestJob("deferred", 1),
		attemptTestJob("started", 1), attemptTestJob("yielded", 1), attemptTestJob("started", 1), attemptTestJob("requeued", 1),
		attemptTestJob("started", 2), attemptTestJob("done", 2),
		attemptTestChunk("started", "running", 0), attemptTestChunk("settled", "retried", 0),
		attemptTestChunk("started", "running", 1), attemptTestChunk("settled", "deferred", 1),
		attemptTestChunk("started", "running", 1), attemptTestChunk("settled", "completed", 1),
	} {
		raw = append(raw, attemptTestLine(t, 2, report)...)
	}
	// Resumed retry depth is not a history of events in this phase. These
	// starts count once, despite native depths inherited from earlier work.
	raw = append(raw, attemptTestLine(t, 3, attemptTestJob("started", 3))...)
	raw = append(raw, attemptTestLine(t, 3, attemptTestChunk("started", "running", 4))...)
	got, err := observeExecutionAttempts(raw, plan, 2, [32]byte{1}, true)
	if err != nil || !got.Complete || got.Phases[1] != (ExecutionAttemptCount{JobAttempts: 7, Retries: 2, MaxRetriesUnit: 1}) ||
		got.Phases[2] != (ExecutionAttemptCount{JobAttempts: 2}) {
		t.Fatalf("observed transition counts: %+v %v", got, err)
	}
}

func TestExecutionAttemptFailedPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	first := attemptTestLine(t, 2, attemptTestJob("started", 1))
	second := attemptTestLine(t, 2, attemptTestJob("done", 1))
	for _, mode := range []string{"success", "not joined", "wrong producer", "wrong input", "wrong phase", "unknown kind", "unknown field", "duplicate field", "truncated", "trailing", "untagged", "untagged chunk", "split marker", "long unrelated", "partial unrelated", "ceiling"} {
		t.Run(mode, func(t *testing.T) {
			candidate := plan
			candidate.WorkEnvelope.Phases = append([]PhaseWorkBounds(nil), plan.WorkEnvelope.Phases...)
			tail := string(second)
			joined := true
			switch mode {
			case "not joined":
				joined = false
			case "wrong producer":
				tail = strings.Replace(tail, `"producer":2`, `"producer":3`, 1)
			case "wrong input":
				tail = strings.Replace(tail, `"input_sha256":"sha256:01`, `"input_sha256":"sha256:02`, 1)
			case "wrong phase":
				tail = strings.Replace(tail, `"phase":2`, `"phase":5`, 1)
			case "unknown kind":
				tail = strings.Replace(tail, `"kind":"job"`, `"kind":"other"`, 1)
			case "unknown field":
				tail = strings.Replace(tail, `"producer":2`, `"unknown":2,"producer":2`, 1)
			case "duplicate field":
				tail = strings.Replace(tail, `"producer":2`, `"producer":2,"producer":2`, 1)
			case "truncated":
				tail = strings.TrimSuffix(tail, "\n")
			case "trailing":
				tail = strings.TrimSuffix(tail, "\n") + "{}\n"
			case "untagged":
				tail = "job lifecycle: {}\n"
			case "untagged chunk":
				tail = "generation chunk lifecycle: {}\n"
			case "split marker":
				tail = strings.Repeat("z", maxExecutionAttemptLine-4) + "job lifecycle: {}\n"
			case "long unrelated":
				tail = strings.Repeat("z", maxExecutionAttemptLine*3) + "\n" + tail
			case "partial unrelated":
				tail = "unclosed diagnostic"
			case "ceiling":
				candidate.WorkEnvelope.Phases[1].JobAttempts.Maximum = 0
			}
			got, err := observeExecutionAttempts(append(append([]byte(nil), first...), tail...), candidate, 2, [32]byte{1}, joined)
			wantOK := mode == "success" || mode == "long unrelated"
			want := uint64(1)
			if !joined {
				want = 0
			}
			if (err == nil) != wantOK || got.Complete != wantOK || got.Phases[1].JobAttempts != want {
				t.Fatalf("prefix %+v error=%v", got, err)
			}
		})
	}
}

func TestExecutionAttemptNativeVocabulary(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, report := range []any{attemptTestJob("started", 0), attemptTestJob("started", 4), attemptTestJob("requeued", 3),
		attemptTestJob("unknown", 1), attemptTestChunk("started", "running", -1), attemptTestChunk("started", "running", 5),
		attemptTestChunk("settled", "retried", 4), attemptTestChunk("settled", "unknown", 0)} {
		if got, err := observeExecutionAttempts(attemptTestLine(t, 2, report), plan, 2, [32]byte{1}, true); err == nil || got.Complete {
			t.Fatalf("invalid native report accepted: %+v", report)
		}
	}
	for _, outcome := range []string{"handler_failed", "heartbeat_failed", "stale_fenced", "released", "release_failed", "pre_heartbeat_failed", "completed", "completion_failed", "terminal", "terminal_record_failed", "deferred", "deferral_failed", "exhausted"} {
		raw := attemptTestLine(t, 2, attemptTestChunk("started", "running", 0))
		raw = append(raw, attemptTestLine(t, 2, attemptTestChunk("settled", outcome, 0))...)
		if got, err := observeExecutionAttempts(raw, plan, 2, [32]byte{1}, true); err != nil || got.Phases[1] != (ExecutionAttemptCount{JobAttempts: 1}) {
			t.Fatalf("native non-retry %s: %+v %v", outcome, got, err)
		}
	}
	for _, stage := range []string{store.ServiceStateV3ReconcileStage, store.ServiceStateV3ActivateStage} {
		report := attemptTestChunk("started", "running", 0)
		report.Stage = stage
		got, err := observeExecutionAttempts(attemptTestLine(t, 2, report), plan, 2, [32]byte{1}, true)
		if err != nil || !got.Complete || got.Phases[1].JobAttempts != 1 {
			t.Fatalf("reachable service stage %s refused: %+v %v", stage, got, err)
		}
	}
}

func TestExecutionAttemptFinishStablePrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	line := attemptTestLine(t, 2, attemptTestJob("started", 1))
	for _, mode := range []string{"healthy", "empty", "process failed", "overflow at newline", "truncated", "not joined", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			output := &checkoutCommandOutput{remaining: int64(len(line)), cancel: cancel}
			if mode != "empty" {
				if _, err := output.Write(line); err != nil {
					t.Fatal(err)
				}
			}
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, output: output, attemptInput: [32]byte{1}}
			result := ExecutionEpochOneResult{RootJoined: mode != "not joined"}
			var failure error
			switch mode {
			case "process failed":
				failure = ErrExecutionEpochOne
			case "overflow at newline":
				if _, err := output.Write([]byte("missing report\n")); err == nil || ctx.Err() == nil {
					t.Fatal("actual output refusal did not cancel")
				}
				failure = ErrExecutionEpochOne // Native finish preserves this pump/context failure.
			case "truncated":
				_, _ = output.buffer.WriteString("partial")
			case "unbound":
				run.attemptInput = [32]byte{}
			}
			err := run.finishAttemptObservation(&result, failure)
			wantComplete := mode == "healthy" || mode == "empty"
			wantCount := uint64(1)
			if mode == "empty" || mode == "unbound" || mode == "not joined" {
				wantCount = 0
			}
			if (err == nil) != wantComplete || result.Attempts.Complete != wantComplete || result.Attempts.Phases[1].JobAttempts != wantCount {
				t.Fatalf("joined/lossless distinction: %+v %v", result.Attempts, err)
			}
		})
	}
}

func TestExecutionAttemptPlanPhaseBinding(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, mode := range []string{"order", "work", "dispatch", "absent"} {
		t.Run(mode, func(t *testing.T) {
			candidate := plan
			candidate.PhaseOrder = append([]string(nil), plan.PhaseOrder...)
			candidate.WorkEnvelope.Phases = append([]PhaseWorkBounds(nil), plan.WorkEnvelope.Phases...)
			accounting := *plan.ProcessAccounting
			accounting.DispatchBudgets = append([]PhaseDispatchBudget(nil), accounting.DispatchBudgets...)
			candidate.ProcessAccounting = &accounting
			switch mode {
			case "order":
				candidate.PhaseOrder[1] = "unknown"
			case "work":
				candidate.WorkEnvelope.Phases[1].Phase = "unknown"
			case "dispatch":
				candidate.ProcessAccounting.DispatchBudgets[1].Phase = "unknown"
			case "absent":
				candidate.ProcessAccounting = nil
			}
			if got, err := observeExecutionAttempts(nil, candidate, 2, [32]byte{1}, true); err == nil || got.Complete {
				t.Fatal("missing plan phase coverage accepted")
			}
		})
	}
}
