package t421

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestExecutionCacheJoinedDecisions(t *testing.T) {
	plan := accountingTestPlan(t)
	raw := attemptTestBindings() + "CC1:2:2R\nCC1:2:2r\nCC1:2:2M\nCC1:2:2m\nCC1:2:2H\nCC1:2:2H\n"
	got, err := observeExecutionAttempts([]byte(raw), plan, 2, [32]byte{1}, true)
	want := ExecutionCacheCount{Lookups: 4, Hits: 2, Misses: 2, RootReads: 1, RootValidations: 1, MemberReads: 1, MemberValidations: 1}
	if err != nil || !got.Cache.Bound || !got.Cache.Complete || got.Cache.Phases[1] != want {
		t.Fatal(got.Cache, err)
	}
	for producer := uint32(2); producer <= 6; producer++ {
		bindings := strings.ReplaceAll(attemptTestBindings(), ":2:", ":"+string(rune('0'+producer))+":")
		zero, err := observeExecutionAttempts([]byte(bindings), plan, producer, [32]byte{1}, true)
		if err != nil || !zero.Cache.Complete || zero.Cache.Phases != ([15]ExecutionCacheCount{}) {
			t.Fatal("mandatory native bound zero", producer, zero, err)
		}
	}
	if got, err := observeExecutionAttempts([]byte(raw), plan, 2, [32]byte{1}, false); err == nil || got.Cache.Bound || got.Cache.Complete {
		t.Fatal("unjoined output inspected", got, err)
	}
}

func TestExecutionCacheRefusalsRetainPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, tail := range []string{
		"CC1:2:2R\n", // Interrupted load is not given an invented validation.
		"CC1:2:2r\n", "CC1:2:2m\n", "CC1:2:3r\n", "CC1:3:2H\n", "CC1:2:5H\n", "CC1:2:ZH\n",
		"CC1:2:2x\n", "CC2:2:2H\n", "CC1:2:2H", "junkCC1:2:2H\n",
		"CCB2:2:sha256:01\n", "CCB1:2:sha256:01" + strings.Repeat("00", 31) + "\n",
		strings.Repeat("z", maxExecutionAttemptLine-2) + "CC1:2:2H\n",
	} {
		t.Run(tail[:min(len(tail), 24)], func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(attemptTestBindings()+"CC1:2:2H\n"+tail), plan, 2, [32]byte{1}, true)
			if err == nil || got.Complete || got.Cache.Complete || got.Cache.Phases[1].Hits != 1 {
				t.Fatal(got.Cache, err)
			}
		})
	}
	for _, mode := range []string{"lookup", "hit", "miss", "root", "member"} {
		changed := accountingTestPlan(t)
		op := "H"
		switch mode {
		case "lookup":
			changed.WorkEnvelope.Phases[1].CacheLookups.Maximum = 0
		case "hit":
			changed.WorkEnvelope.Phases[1].CacheHits.Maximum = 0
		case "miss":
			changed.WorkEnvelope.Phases[1].CacheMisses.Maximum, op = 0, "R"
		case "root":
			changed.WorkEnvelope.Phases[1].CacheRootReads.Maximum, op = 0, "R"
		case "member":
			changed.WorkEnvelope.Phases[1].CacheMemberReads.Maximum, op = 0, "M"
		}
		got, err := observeExecutionAttempts([]byte(attemptTestBindings()+"CC1:2:2"+op+"\n"), changed, 2, [32]byte{1}, true)
		if err == nil || got.Cache.Complete || got.Cache.Phases[1].Lookups != 1 {
			t.Fatal("first actual excess lost", mode, got.Cache, err)
		}
	}
	var out ExecutionCacheObservation
	out.Bound, out.Phases[1].Hits = true, math.MaxUint64
	if _, err := observeCacheEvent([]byte("CC1:2:2H\n"), plan, 2, "unused", &out); err == nil || out.Phases[1].Hits != math.MaxUint64 {
		t.Fatal("overflow wrapped", out, err)
	}
}

func TestExecutionCacheTerminalFence(t *testing.T) {
	footer := "TFE1:4:8:sha256:01" + strings.Repeat("00", 31) + "\n"
	for _, suffix := range []string{"CC1:4:8H\n", "CCB1:4:sha256:01" + strings.Repeat("00", 31) + "\n", "junkCC1:4:8H\n"} {
		if _, err := executionTerminalFooter([]byte(footer+suffix), [32]byte{1}); err == nil {
			t.Fatal("cache event after native terminal accepted")
		}
	}
}

func TestExecutionCacheFinishRequiresCompleteNativePrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, mode := range []string{"healthy", "bound-zero", "missing-binding", "unpaired-load", "native-failure", "unjoined"} {
		t.Run(mode, func(t *testing.T) {
			raw := attemptTestBindings() + "IXB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
			if mode != "bound-zero" && mode != "missing-binding" {
				raw += "CC1:2:2R\n"
				if mode != "unpaired-load" {
					raw += "CC1:2:2r\n"
				}
			}
			if mode == "missing-binding" {
				raw = strings.Replace(raw, "CCB1:2:sha256:01"+strings.Repeat("00", 31)+"\n", "", 1)
			}
			output := &checkoutCommandOutput{remaining: int64(len(raw)), cancel: func() {}}
			if _, err := output.Write([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, output: output, attemptInput: [32]byte{1}}
			result := ExecutionEpochOneResult{RootJoined: mode != "unjoined"}
			var failure error
			if mode == "native-failure" {
				failure = ErrExecutionEpochOne
			}
			err := run.finishAttemptObservation(context.Background(), &result, executionProcessDeath{}, failure)
			wantComplete := mode == "healthy" || mode == "bound-zero"
			wantReads := uint64(1)
			if mode == "unjoined" || mode == "bound-zero" || mode == "missing-binding" {
				wantReads = 0
			}
			if (err == nil) != wantComplete || result.Attempts.Complete != wantComplete || result.Attempts.Cache.Complete != wantComplete || result.Attempts.Cache.Phases[1].RootReads != wantReads {
				t.Fatal(mode, result.Attempts.Cache, err)
			}
		})
	}
}
