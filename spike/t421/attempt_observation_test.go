package t421

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func TestExecutionAttemptObservedTransitions(t *testing.T) {
	plan := accountingTestPlan(t)
	raw := "A2j1\nA2j1\nA2j1\nA2r1\nA2j2\nA2c0\nA2t1\nA2c1\nA2c1\nA3j3\nA3c4\n"
	got, err := observeExecutionAttempts([]byte(attemptTestBindings()+raw), plan, 2, [32]byte{1}, true)
	if err != nil || !got.Complete || got.Phases[1] != (ExecutionAttemptCount{JobAttempts: 7, Retries: 2, MaxRetriesUnit: 1}) || got.Phases[2] != (ExecutionAttemptCount{JobAttempts: 2}) {
		t.Fatal(got, err)
	}
}
func attemptTestBindings() string {
	return lifecycleTestBindings(2)
}
func TestExecutionAttemptFailedPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, test := range []struct {
		name, tail string
		complete   bool
		starts     uint64
	}{
		{"zero", "", true, 1}, {"repeat", "A2j1\n", true, 2}, {"retry", "A2r1\n", true, 1},
		{"chunk retry", "A2t4\n", true, 1}, {"unknown opcode", "A2x1\n", false, 1},
		{"unknown phase", "AZj1\n", false, 1}, {"other producer phase", "A5j1\n", false, 1},
		{"job zero", "A2j0\n", false, 1}, {"job max", "A2j4\n", false, 1}, {"retry max", "A2r3\n", false, 1},
		{"chunk max", "A2c5\n", false, 1}, {"chunk retry zero", "A2t0\n", false, 1}, {"chunk retry max", "A2t5\n", false, 1},
		{"partial", "A2j", false, 1}, {"partial marker", "A", false, 1}, {"embedded", "junkA2j1\n", false, 1},
		{"duplicate binding", attemptTestBindings(), false, 1}, {"unknown version", "ATB2:2:sha256:01\n", false, 1},
		{"old envelope", "exact attempt: {}\n", false, 1}, {"old job", "job lifecycle: {}\n", false, 1},
		{"old chunk", "generation chunk lifecycle: {}\n", false, 1}, {"partial unrelated", "unclosed diagnostic", false, 1},
		{"long unrelated", strings.Repeat("z", maxExecutionAttemptLine*3) + "\n", true, 1},
		{"ordinary digest name", "2026/09/07 checksum SHA256\n", true, 1},
		{"split marker", strings.Repeat("z", maxExecutionAttemptLine-2) + "A2j1\n", false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(attemptTestBindings()+"A2j1\n"+test.tail), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[1].JobAttempts != test.starts {
				t.Fatal(got, err)
			}
		})
	}
	for _, raw := range []string{"", "A2j1\n", strings.Replace(attemptTestBindings(), "ATB1:2:", "ATB1:3:", 1), strings.Replace(attemptTestBindings(), "ATB1:2:sha256:01", "ATB1:2:sha256:02", 1), strings.Replace(attemptTestBindings(), "ATB1:", "ATB0:", 1)} {
		if got, err := observeExecutionAttempts([]byte(raw), plan, 2, [32]byte{1}, true); err == nil || got.Complete {
			t.Fatal(got, err)
		}
	}
	plan.WorkEnvelope.Phases[1].JobAttempts.Maximum = 0
	got, err := observeExecutionAttempts([]byte(attemptTestBindings()+"A2j1\n"), plan, 2, [32]byte{1}, true)
	if err == nil || got.Phases[1].JobAttempts != 1 {
		t.Fatal(got, err)
	}
}
func TestExecutionAttemptSimultaneousHeadroom(t *testing.T) {
	plan := accountingTestPlan(t)
	expected := []uint64{32864807, 600395, 19938998, 10012813, 13165061}
	combined := []uint64{33946097, 961101, 21020672, 11453563, 13525767}
	withResolver := []uint64{34446276, 961180, 21270801, 11453642, 13775896}
	for producer := uint32(2); producer <= 6; producer++ {
		var starts, source, index, observation, cache, publication, resolver uint64
		for _, phase := range executionProducerPhases(producer) {
			row := plan.WorkEnvelope.Phases[phase-1]
			starts += row.JobAttempts.Maximum
			source += 8 * row.GitReads.Maximum
			index += 3 * row.IndexFiles.Maximum
			observation += 8 * row.ObservationParses.Maximum
			cache += 9 * (row.CacheLookups.Maximum + row.CacheMisses.Maximum)
			publication += 8 * row.PublicationWrites.Maximum
			resolver += 25 * row.ResolverBlobReads.Maximum
			for _, role := range row.ControlledDispatchRoles {
				if role.Name == "zoekt-git-index" {
					index += role.Maximum * uint64(9+len(strconv.FormatUint(row.IndexFiles.Maximum, 10)))
				}
			}
		}
		total := source + index + observation + 10*starts + 5*79
		// There are exactly two native drives in epoch four and one in
		// epoch five; each emits at most 4096 bounded JSON returned ticks.
		drives := uint64(0)
		switch producer {
		case 5:
			drives = 2
		case 6:
			drives = 1
		}
		total += drives * uint64(lifecycle.MaxCycleObservationTurns) * (maxExecutionLifecycleEvent + 5)
		if producer == 4 {
			total += 81 // One terminal phase-eight footer, no extra PC pair.
		}
		if total != expected[producer-2] || total >= 64<<20 {
			t.Fatalf("producer %d total %d", producer, total)
		}
		// The new cache stream adds one binding and at most one nine-byte
		// decision per lookup plus one result admission per classified miss.
		total += 79 + cache
		// One independent binding plus eight bytes per actual PublishDomain
		// call, including failed and exact-current recount invocations.
		total += 79 + publication
		if total != combined[producer-2] || total >= 64<<20 {
			t.Fatalf("seven-family output bound changed: producer %d total %d", producer, total)
		}
		total += 79 + resolver
		if total != withResolver[producer-2] || total >= 64<<20 {
			t.Fatalf("eight-family output bound changed: producer %d total %d", producer, total)
		}
		t.Logf("producer=%d starts=%d combined=%d remaining=%d", producer, starts, total, (64<<20)-total)
	}
	// At most one retry per emitted start in the same held owner turn. This
	// proves only source/index/attempt/parse/lifecycle/cache/publication/resolver fit;
	// candidate/ordinary/future logs remain.
}
func TestExecutionAttemptFinishStablePrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	line := []byte("A2j1\nOP1:2:2\nEP1:2:2\nRM1:2:2:000000000000000a\n")
	for _, mode := range []string{"healthy", "empty", "process failed", "overflow at newline", "truncated", "not joined", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			header := []byte(attemptTestBindings() + "IXB1:2:sha256:01" + strings.Repeat("00", 31) + "\n")
			output := &checkoutCommandOutput{remaining: int64(len(line) + len(header)), cancel: cancel}
			if _, err := output.Write(header); err != nil {
				t.Fatal(err)
			}
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
			err := run.finishAttemptObservation(ctx, &result, executionProcessDeath{}, failure)
			wantComplete := mode == "healthy" || mode == "empty"
			wantCount := uint64(1)
			if mode == "empty" || mode == "unbound" || mode == "not joined" {
				wantCount = 0
			}
			if (err == nil) != wantComplete || result.Attempts.Complete != wantComplete || result.Attempts.Phases[1].JobAttempts != wantCount || result.Attempts.Phases[1].ObservationParses != wantCount || result.Attempts.Phases[1].PublicationWrites != wantCount || result.Attempts.Phases[1].ResolverBlobReads != wantCount || result.Attempts.Phases[1].ResolverBlobBytes != 10*wantCount {
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
