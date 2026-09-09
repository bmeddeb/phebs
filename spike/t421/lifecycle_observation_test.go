package t421

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func lifecycleTestBindings(producer uint32) string {
	input := "sha256:01" + strings.Repeat("00", 31) + "\n"
	return fmt.Sprintf("ATB1:%d:%sSRB1:%d:%sOPB1:%d:%sLCB1:%d:%sCCB1:%d:%sEPB1:%d:%sRMB1:%d:%sRLB1:%d:%sSBB1:%d:%sGCB1:%d:%s", producer, input, producer, input, producer, input, producer, input, producer, input, producer, input, producer, input, producer, input, producer, input, producer, input)
}

type lifecycleCursorReadFailure struct{}

func (lifecycleCursorReadFailure) GetLifecycleCursor(_ context.Context, key string) (string, uint64, error) {
	if key == "rotation" {
		return "", 0, nil
	}
	return "", 0, errors.New("fixture owner cursor read failed")
}

func (lifecycleCursorReadFailure) CompareAndSwapLifecycleCursor(context.Context, string, uint64, string) error {
	panic("cursor-read refusal must precede every cursor write")
}

func TestExecutionLifecycleNativeCursorFailurePrefix(t *testing.T) {
	controller, err := lifecycle.NewController(lifecycleCursorReadFailure{}, lifecycle.SearchGenerationOwnerImpl{})
	if err != nil {
		t.Fatal(err)
	}
	result := controller.Tick(t.Context())
	if result.Err == nil || result.Owner != lifecycle.SearchOwner || !result.AttemptedAt.IsZero() || result.Scanned != 0 || result.Deleted != 0 {
		t.Fatal("real pre-Sweep failure shape changed", result)
	}
	first := lifecycleTestTurn()
	failure := executionLifecycleEvent{Schema: first.Schema, Epoch: 4, Phase: 9, ReturnedTick: 2,
		Owner: result.Owner, Scanned: result.Scanned, Deleted: result.Deleted, LogicalBytes: result.LogicalBytes,
		RootBytes: result.RootBytes, MemberBytes: result.MemberBytes, Completeness: string(result.Completeness), Failed: result.Err != nil,
		OwnerTurns: 1, TotalDeleted: 1, MaxDeleted: 1}
	plan := accountingTestPlan(t)
	for _, known := range []bool{true, false} {
		if !known {
			failure.Owner = "unknown"
		}
		raw := lifecycleTestBindings(5) + lifecycleTestEvent(t, first) + lifecycleTestEvent(t, failure)
		got, err := observeExecutionAttempts([]byte(raw), plan, 5, [32]byte{1}, true)
		want := ExecutionLifecycleCount{ReturnedTicks: 1, OwnerTurns: 1, Deleted: 1, MaxDeleted: 1}
		if known {
			want.ReturnedTicks, want.FailedTicks = 2, 1
		}
		if (err == nil) != known || got.Lifecycle.Complete != known || got.Lifecycle.Phases[8] != want {
			t.Fatal(known, got, err)
		}
	}
}

func lifecycleTestEvent(t *testing.T, event executionLifecycleEvent) string {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return "LC1:" + string(raw) + "\n"
}

func lifecycleTestTurn() executionLifecycleEvent {
	return executionLifecycleEvent{Schema: "phebs-t422-lifecycle-turn-v1", Epoch: 4, Phase: 9,
		ReturnedTick: 1, Owner: lifecycle.SearchOwner, AttemptedAtNano: 1,
		Scanned: 2, Deleted: 1, Completeness: string(lifecycle.Exact), OwnerTurns: 1, TotalDeleted: 1, MaxDeleted: 1}
}

func TestExecutionLifecycleJoinedCollector(t *testing.T) {
	plan := accountingTestPlan(t)
	first := lifecycleTestTurn()
	next := first
	next.ReturnedTick, next.OwnerTurns, next.TotalDeleted = 2, 2, 2
	next.Failed, next.Completeness = true, string(lifecycle.Unavailable)
	last := next
	last.ReturnedTick, last.OwnerTurns, last.TotalDeleted, last.Failed = 3, 3, 3, false
	last.Completeness = string(lifecycle.Exact)
	raw := lifecycleTestBindings(5) + lifecycleTestEvent(t, first) + lifecycleTestEvent(t, next) + lifecycleTestEvent(t, last)
	got, err := observeExecutionAttempts([]byte(raw), plan, 5, [32]byte{1}, true)
	want := ExecutionLifecycleCount{ReturnedTicks: 3, OwnerTurns: 3, Deleted: 3, MaxDeleted: 1, FailedTicks: 1}
	if err != nil || !got.Lifecycle.Complete || got.Lifecycle.Phases[8] != want {
		t.Fatal(got, err)
	}
	for producer := uint32(2); producer <= 6; producer++ {
		zero, err := observeExecutionAttempts([]byte(lifecycleTestBindings(producer)), plan, producer, [32]byte{1}, true)
		if err != nil || !zero.Lifecycle.Complete || zero.Lifecycle.Phases != ([15]ExecutionLifecycleCount{}) {
			t.Fatal("bound zero", producer, zero, err)
		}
	}
	for _, phase := range []uint32{11, 13} {
		event := first
		event.Phase = phase
		producer := uint32(5)
		if phase == 13 {
			event.Epoch, producer = 5, 6
		}
		got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(producer)+lifecycleTestEvent(t, event)), plan, producer, [32]byte{1}, true)
		if err != nil || !got.Lifecycle.Complete || got.Lifecycle.Phases[phase-1].OwnerTurns != 1 {
			t.Fatal(phase, got, err)
		}
	}
}

func TestExecutionLifecycleRefusalsAndPositivePrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	first := lifecycleTestTurn()
	valid := lifecycleTestEvent(t, first)
	for _, test := range []struct {
		name string
		edit func(*executionLifecycleEvent)
	}{
		{"epoch", func(v *executionLifecycleEvent) { v.Epoch = 3 }},
		{"phase", func(v *executionLifecycleEvent) { v.Phase = 10 }},
		{"schema", func(v *executionLifecycleEvent) { v.Schema += "x" }},
		{"unknown owner", func(v *executionLifecycleEvent) { v.Owner = "unknown" }},
		{"timestamp", func(v *executionLifecycleEvent) { v.AttemptedAtNano = 0 }},
		{"negative", func(v *executionLifecycleEvent) { v.LogicalBytes = -1 }},
		{"completeness", func(v *executionLifecycleEvent) { v.Completeness = "unknown" }},
		{"skip", func(v *executionLifecycleEvent) { v.ReturnedTick = 3 }},
		{"owner prefix", func(v *executionLifecycleEvent) { v.OwnerTurns = 1 }},
		{"delete prefix", func(v *executionLifecycleEvent) { v.TotalDeleted = 1 }},
		{"maximum prefix", func(v *executionLifecycleEvent) { v.MaxDeleted = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			next := first
			next.ReturnedTick, next.OwnerTurns, next.TotalDeleted = 2, 2, 2
			test.edit(&next)
			got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(5)+valid+lifecycleTestEvent(t, next)), plan, 5, [32]byte{1}, true)
			if err == nil || got.Lifecycle.Complete || got.Lifecycle.Phases[8].OwnerTurns != 1 {
				t.Fatal(got, err)
			}
		})
	}
	for _, tail := range []string{
		valid, "junk" + valid, strings.TrimSuffix(valid, "\n"), "LC2:{}\n", "exact lifecycle turn: {}\n",
		strings.Replace(valid, "\"schema\":", "\"extra\":0,\"schema\":", 1),
		strings.Repeat("x", maxExecutionAttemptLine-2) + valid,
		strings.Replace(valid, "\"epoch\":4", "\"epoch\":4,\"epoch\":4", 1),
		"LCB1:5:sha256:01" + strings.Repeat("00", 31) + "\n",
	} {
		got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(5)+valid+tail), plan, 5, [32]byte{1}, true)
		if err == nil || got.Lifecycle.Complete || got.Lifecycle.Phases[8].OwnerTurns != 1 {
			t.Fatal(tail, got, err)
		}
	}
	for _, prefix := range []string{"", "LCB1:5:sha256:02" + strings.Repeat("00", 31) + "\n"} {
		var out ExecutionLifecycleObservation
		_, err := observeLifecycleEvent([]byte(prefix+valid), plan, 5, "sha256:01"+strings.Repeat("00", 31), &out)
		if err == nil || out.Bound || out.Phases[8].ReturnedTicks != 0 {
			t.Fatal(out, err)
		}
	}
	missing := strings.Replace(lifecycleTestBindings(5), "LCB1:5:sha256:01"+strings.Repeat("00", 31)+"\n", "", 1)
	got, err := observeExecutionAttempts([]byte(missing), plan, 5, [32]byte{1}, true)
	if err != nil || got.Lifecycle.Complete || got.Lifecycle.Bound {
		t.Fatal("independent missing observation must not acquire a zero proof", got, err)
	}
}

func TestExecutionLifecycleBoundsAndPreSweepError(t *testing.T) {
	plan := accountingTestPlan(t)
	event := lifecycleTestTurn()
	for _, mode := range []string{"turns", "deleted", "per turn", "overflow", "tick bound"} {
		t.Run(mode, func(t *testing.T) {
			p := plan
			p.WorkEnvelope.Phases = append([]PhaseWorkBounds(nil), plan.WorkEnvelope.Phases...)
			out := ExecutionLifecycleObservation{Bound: true}
			switch mode {
			case "turns":
				p.WorkEnvelope.Phases[8].LifecycleOwnerTurns.Maximum = 0
			case "deleted":
				p.WorkEnvelope.Phases[8].LifecycleDeleted.Maximum = 0
			case "per turn":
				p.WorkEnvelope.MaximumLifecycleDeletesPerTurn = 0
			case "overflow":
				out.Phases[8].Deleted = math.MaxUint64
			case "tick bound":
				out.Phases[8].ReturnedTicks = uint64(lifecycle.MaxCycleObservationTurns)
			}
			_, err := observeLifecycleEvent([]byte(lifecycleTestEvent(t, event)), p, 5, "", &out)
			if err == nil || mode != "overflow" && mode != "tick bound" && out.Phases[8].OwnerTurns != 1 {
				t.Fatal(out, err)
			}
		})
	}
	event = executionLifecycleEvent{Schema: event.Schema, Epoch: 4, Phase: 9, ReturnedTick: 1, Failed: true, Completeness: string(lifecycle.Unavailable)}
	got, err := observeExecutionAttempts([]byte(lifecycleTestBindings(5)+lifecycleTestEvent(t, event)), plan, 5, [32]byte{1}, true)
	if err != nil || got.Lifecycle.Phases[8] != (ExecutionLifecycleCount{ReturnedTicks: 1, FailedTicks: 1}) {
		t.Fatal(got, err)
	}
	footer := "TFE1:4:8:sha256:01" + strings.Repeat("00", 31) + "\n"
	if seen, err := executionTerminalFooter([]byte(footer+"LCB1:4:sha256:01"+strings.Repeat("00", 31)+"\n"), [32]byte{1}); err == nil || !seen {
		t.Fatal("post-footer lifecycle binding accepted", seen, err)
	}
}

func TestExecutionLifecycleFinishRequiresJoinedBoundHealthyOutput(t *testing.T) {
	plan := accountingTestPlan(t)
	bindings := lifecycleTestBindings(5) + "IXB1:5:sha256:01" + strings.Repeat("00", 31) + "\n"
	for _, mode := range []string{"healthy", "native failure", "output refusal", "unjoined", "missing binding", "missing zero binding", "partial tail"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			raw := bindings + lifecycleTestEvent(t, lifecycleTestTurn())
			if mode == "missing zero binding" {
				raw = bindings
			}
			if mode == "missing binding" || mode == "missing zero binding" {
				raw = strings.Replace(raw, "LCB1:5:sha256:01"+strings.Repeat("00", 31)+"\n", "", 1)
			}
			if mode == "partial tail" {
				raw += "LC1:"
			}
			output := &checkoutCommandOutput{remaining: int64(len(raw)), cancel: cancel}
			if _, err := output.Write([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			var failure error
			if mode == "native failure" {
				failure = ErrExecutionEpochOne
			}
			if mode == "output refusal" {
				_, _ = output.Write([]byte("lost\n"))
			}
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, epoch: ExecutionEpochConfig{Epoch: 4}, output: output, attemptInput: [32]byte{1}}
			result := ExecutionEpochOneResult{RootJoined: mode != "unjoined"}
			err := run.finishAttemptObservation(ctx, &result, executionProcessDeath{}, failure)
			want := uint64(1)
			if mode == "unjoined" || mode == "missing binding" || mode == "missing zero binding" {
				want = 0
			}
			if (err == nil) != (mode == "healthy") || result.Attempts.Lifecycle.Complete != (mode == "healthy") || result.Attempts.Lifecycle.Phases[8].OwnerTurns != want {
				t.Fatal(result.Attempts.Lifecycle, err)
			}
		})
	}
}
