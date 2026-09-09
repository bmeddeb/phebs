package t421

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestResolverCompactParser(t *testing.T) {
	plan := accountingTestPlan(t)
	header := "RMB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	other := strings.Replace(attemptTestBindings(), header, "", 1)
	one := "RM1:2:2:000000000000000a\n"
	for _, test := range []struct {
		name, raw   string
		reads, size uint64
		complete    bool
	}{
		{"zero", header, 0, 0, true},
		{"zero bytes", header + "RM1:2:2:0000000000000000\n", 1, 0, true},
		{"repeat", header + one + one, 2, 20, true},
		{"missing", "", 0, 0, false}, {"unbound", one, 0, 0, false},
		{"duplicate", header + header, 0, 0, false},
		{"input", strings.Replace(header, "01", "02", 1), 0, 0, false},
		{"binding version", strings.Replace(header, "RMB1", "RMB2", 1), 0, 0, false},
		{"malformed binding", header + "RMBbroken\n", 0, 0, false},
		{"event version", header + strings.Replace(one, "RM1", "RM2", 1), 0, 0, false},
		{"producer", header + strings.Replace(one, "1:2:2", "1:3:2", 1), 0, 0, false},
		{"phase", header + strings.Replace(one, "1:2:2", "1:2:5", 1), 0, 0, false},
		{"uppercase bytes", header + strings.Replace(one, "a\n", "A\n", 1), 0, 0, false},
		{"nonhex", header + strings.Replace(one, "a\n", "g\n", 1), 0, 0, false},
		{"short", header + strings.Replace(one, "0000", "000", 1), 0, 0, false},
		{"wide", header + strings.Replace(one, "0000", "00000", 1), 0, 0, false},
		{"embedded", header + "ordinary " + one, 0, 0, false},
		{"split", header + strings.Repeat("z", maxExecutionAttemptLine-2) + one, 0, 0, false},
		{"partial", header + one + "RM1:2:", 1, 10, false},
		{"later refusal", header + one + "RMBbroken\n", 1, 10, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(other+test.raw), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[1].ResolverBlobReads != test.reads || got.Phases[1].ResolverBlobBytes != test.size {
				t.Fatal(got, err)
			}
		})
	}
	for _, kind := range []string{"reads", "bytes", "read overflow", "byte overflow"} {
		t.Run(kind, func(t *testing.T) {
			candidate := accountingTestPlan(t)
			out := ExecutionAttemptObservation{ResolverBound: true}
			want := ExecutionAttemptCount{ResolverBlobReads: 1, ResolverBlobBytes: 10}
			switch kind {
			case "reads":
				candidate.WorkEnvelope.Phases[1].ResolverBlobReads.Maximum = 0
			case "bytes":
				candidate.WorkEnvelope.Phases[1].ResolverBlobBytes.Maximum = 9
			case "read overflow":
				out.Phases[1].ResolverBlobReads = math.MaxUint64
				want = out.Phases[1]
			case "byte overflow":
				out.Phases[1].ResolverBlobBytes = math.MaxUint64
				want = out.Phases[1]
			}
			if seen, err := observeResolverEvent([]byte(one), candidate, 2, "unused", &out); !seen || err == nil || out.Phases[1] != want {
				t.Fatal("excess/overflow prefix changed", out, err)
			}
		})
	}
}

func TestResolverProducerBinding(t *testing.T) {
	plan := accountingTestPlan(t)
	for producer := uint32(2); producer <= 6; producer++ {
		raw := lifecycleTestBindings(producer)
		got, err := observeExecutionAttempts([]byte(raw), plan, producer, [32]byte{1}, true)
		if err != nil || !got.Complete || !got.ResolverBound {
			t.Fatal("bound zero refused", producer, got, err)
		}
		for _, phase := range executionProducerPhases(producer) {
			got, err := observeExecutionAttempts([]byte(raw+fmt.Sprintf("RM1:%d:%X:0000000000000001\n", producer, phase)), plan, producer, [32]byte{1}, true)
			bound := plan.WorkEnvelope.Phases[phase-1]
			if (err == nil) != (bound.ResolverBlobReads.Maximum > 0 && bound.ResolverBlobBytes.Maximum > 0) || got.Phases[phase-1].ResolverBlobReads != 1 || got.Phases[phase-1].ResolverBlobBytes != 1 {
				t.Fatal(producer, phase, got, err)
			}
		}
	}
}

func TestResolverTerminalFence(t *testing.T) {
	prefix, footer := terminalPrefixTestBytes()
	for _, tail := range []string{"RM1:4:8:0000000000000001\n", "RMBbroken\n", "junkRM1:4:8:0000000000000001\n"} {
		if seen, err := executionTerminalFooter([]byte(prefix+footer+tail), [32]byte{1}); err == nil || !seen {
			t.Fatal(tail, seen, err)
		}
	}
}
