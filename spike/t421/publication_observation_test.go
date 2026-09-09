package t421

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestPublicationCompactParser(t *testing.T) {
	plan := accountingTestPlan(t)
	header := "EPB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	other := strings.Replace(attemptTestBindings(), header, "", 1)
	for _, test := range []struct {
		name, raw string
		count     uint64
		complete  bool
	}{
		{"zero", header, 0, true},
		{"ordinary public key", header + "2026/09/09 12:00:00 PUBLIC KEY diagnostic\n", 0, true},
		{"repeat", header + "EP1:2:2\nEP1:2:2\n", 2, true},
		{"missing", "", 0, false}, {"unbound", "EP1:2:2\n", 0, false},
		{"duplicate", header + header, 0, false},
		{"wrong input", strings.Replace(header, "01", "02", 1), 0, false},
		{"binding version", strings.Replace(header, "EPB1:", "EPB2:", 1), 0, false},
		{"malformed binding", header + "EPBbroken\n", 0, false},
		{"event version", header + "EP2:2:2\n", 0, false},
		{"producer", header + "EP1:3:2\n", 0, false},
		{"phase", header + "EP1:2:5\n", 0, false},
		{"phase zero", header + "EP1:2:0\n", 0, false},
		{"phase malformed", header + "EP1:2:Z\n", 0, false},
		{"embedded", header + "diagnostic EP1:2:2\n", 0, false},
		{"split", header + strings.Repeat("x", maxExecutionAttemptLine-2) + "EP1:2:2\n", 0, false},
		{"partial", header + "EP1:2:2\nEP1:2:", 1, false},
		{"bad tail", header + "EP1:2:2\nEP2:2:2\n", 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(other+test.raw), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[1].PublicationWrites != test.count || got.Phases[1].SourceBlobAttempts != 0 || got.Phases[1].ObservationParses != 0 {
				t.Fatal(got, err)
			}
		})
	}
	plan.WorkEnvelope.Phases[1].PublicationWrites.Maximum = 1
	got, err := observeExecutionAttempts([]byte(other+header+"EP1:2:2\nEP1:2:2\n"), plan, 2, [32]byte{1}, true)
	if err == nil || got.Complete || got.Phases[1].PublicationWrites != 2 {
		t.Fatal("first actual excess lost", got, err)
	}
	out := ExecutionAttemptObservation{PublicationBound: true}
	out.Phases[1].PublicationWrites = math.MaxUint64
	if recognized, err := observeBlobEvent([]byte("EP1:2:2\n"), plan, 2, "unused", &out); !recognized || err == nil || out.Phases[1].PublicationWrites != math.MaxUint64 {
		t.Fatal("counter overflow", out, err)
	}
}

func TestPublicationProducerBinding(t *testing.T) {
	plan := accountingTestPlan(t)
	for producer := uint32(2); producer <= 6; producer++ {
		raw := lifecycleTestBindings(producer)
		got, err := observeExecutionAttempts([]byte(raw), plan, producer, [32]byte{1}, true)
		if err != nil || !got.Complete || !got.PublicationBound {
			t.Fatal("bound zero refused", producer, got, err)
		}
		for _, phase := range executionProducerPhases(producer) {
			got, err := observeExecutionAttempts([]byte(raw+fmt.Sprintf("EP1:%d:%X\n", producer, phase)), plan, producer, [32]byte{1}, true)
			if (err == nil) != (plan.WorkEnvelope.Phases[phase-1].PublicationWrites.Maximum > 0) || got.Phases[phase-1].PublicationWrites != 1 {
				t.Fatal("phase or exact-zero ceiling", producer, phase, got, err)
			}
		}
	}
}

func TestPublicationTerminalFence(t *testing.T) {
	prefix, footer := terminalPrefixTestBytes()
	for _, tail := range []string{"EP1:4:8\n", "EPB1:4:sha256:01" + strings.Repeat("00", 31) + "\n", "junkEP1:4:8\n", "EPBbroken\n"} {
		if seen, err := executionTerminalFooter([]byte(prefix+footer+tail), [32]byte{1}); err == nil || !seen {
			t.Fatal("publication after terminal accepted", tail, seen, err)
		}
	}
}
