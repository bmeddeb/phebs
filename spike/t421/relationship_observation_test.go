package t421

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestRelationshipCompactParser(t *testing.T) {
	plan := accountingTestPlan(t)
	header := "RLB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	other := strings.Replace(attemptTestBindings(), header, "", 1)
	for _, test := range []struct {
		name, raw string
		count     uint64
		complete  bool
	}{
		{"zero", header, 0, true},
		{"ordinary public key", header + "2026/09/09 12:00:00 PUBLIC KEY diagnostic\n", 0, true},
		{"repeat", header + "RL1:2:2P\nRL1:2:2P\n", 2, true},
		{"unknown event", header + "RL1:2:2X\n", 0, false},
		{"missing", "", 0, false}, {"unbound", "RL1:2:2P\n", 0, false},
		{"duplicate", header + header, 0, false},
		{"wrong input", strings.Replace(header, "01", "02", 1), 0, false},
		{"binding version", strings.Replace(header, "RLB1:", "RLB2:", 1), 0, false},
		{"malformed binding", header + "RLBbroken\n", 0, false},
		{"event version", header + "RL2:2:2P\n", 0, false},
		{"producer", header + "RL1:3:2P\n", 0, false},
		{"phase", header + "RL1:2:5P\n", 0, false},
		{"phase zero", header + "RL1:2:0P\n", 0, false},
		{"phase malformed", header + "RL1:2:ZP\n", 0, false},
		{"embedded", header + "diagnostic RL1:2:2P\n", 0, false},
		{"split", header + strings.Repeat("x", maxExecutionAttemptLine-2) + "RL1:2:2P\n", 0, false},
		{"partial", header + "RL1:2:2P\nRL1:2:", 1, false},
		{"bad tail", header + "RL1:2:2P\nRL2:2:2P\n", 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(other+test.raw), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[1].RelationshipProjections != test.count || got.Phases[1].SourceBlobAttempts != 0 || got.Phases[1].ObservationParses != 0 {
				t.Fatal(got, err)
			}
		})
	}
	plan.WorkEnvelope.Phases[1].RelationshipProjections.Maximum = 1
	got, err := observeExecutionAttempts([]byte(other+header+"RL1:2:2P\nRL1:2:2P\n"), plan, 2, [32]byte{1}, true)
	if err == nil || got.Complete || got.Phases[1].RelationshipProjections != 2 {
		t.Fatal("first actual excess lost", got, err)
	}
	out := ExecutionAttemptObservation{RelationshipBound: true}
	out.Phases[1].RelationshipProjections = math.MaxUint64
	if recognized, err := observeRelationshipEvent([]byte("RL1:2:2P\n"), plan, 2, "unused", &out); !recognized || err == nil || out.Phases[1].RelationshipProjections != math.MaxUint64 {
		t.Fatal("counter overflow", out, err)
	}
}

func TestRelationshipProducerBinding(t *testing.T) {
	plan := accountingTestPlan(t)
	for producer := uint32(2); producer <= 6; producer++ {
		raw := lifecycleTestBindings(producer)
		got, err := observeExecutionAttempts([]byte(raw), plan, producer, [32]byte{1}, true)
		if err != nil || !got.Complete || !got.RelationshipBound {
			t.Fatal("bound zero refused", producer, got, err)
		}
		for _, phase := range executionProducerPhases(producer) {
			got, err := observeExecutionAttempts([]byte(raw+fmt.Sprintf("RL1:%d:%XP\n", producer, phase)), plan, producer, [32]byte{1}, true)
			if (err == nil) != (plan.WorkEnvelope.Phases[phase-1].RelationshipProjections.Maximum > 0) || got.Phases[phase-1].RelationshipProjections != 1 {
				t.Fatal("phase or exact-zero ceiling", producer, phase, got, err)
			}
			got, err = observeExecutionAttempts([]byte(raw+fmt.Sprintf("RL1:%d:%XB\n", producer, phase)), plan, producer, [32]byte{1}, true)
			if (err == nil) != (plan.WorkEnvelope.Phases[phase-1].RelationshipBuildAttempts.Maximum > 0) || got.Phases[phase-1].RelationshipBuildAttempts != 1 || got.Phases[phase-1].RelationshipProjections != 0 {
				t.Fatal("build phase or exact-zero ceiling", producer, phase, got, err)
			}
		}
	}
}

func TestRelationshipTerminalFence(t *testing.T) {
	prefix, footer := terminalPrefixTestBytes()
	for _, tail := range []string{"RL1:4:8P\n", "RLB1:4:sha256:01" + strings.Repeat("00", 31) + "\n", "junkRL1:4:8P\n", "RLBbroken\n"} {
		if seen, err := executionTerminalFooter([]byte(prefix+footer+tail), [32]byte{1}); err == nil || !seen {
			t.Fatal("relationship after terminal accepted", tail, seen, err)
		}
	}
}

func TestRelationshipIndependentCounters(t *testing.T) {
	for _, event := range []byte{'B', 'P'} {
		for _, mode := range []string{"exact", "excess", "overflow"} {
			t.Run(fmt.Sprintf("%c/%s", event, mode), func(t *testing.T) {
				plan := accountingTestPlan(t)
				out := ExecutionAttemptObservation{RelationshipBound: true}
				count := &out.Phases[1].RelationshipBuildAttempts
				if event == 'P' {
					count = &out.Phases[1].RelationshipProjections
				}
				plan.WorkEnvelope.Phases[1].RelationshipBuildAttempts.Maximum = 1
				plan.WorkEnvelope.Phases[1].RelationshipProjections.Maximum = 1
				if mode == "excess" {
					*count = 1
				}
				if mode == "overflow" {
					*count = math.MaxUint64
				}
				want := *count
				if mode != "overflow" {
					want++
				}
				seen, err := observeRelationshipEvent([]byte(fmt.Sprintf("RL1:2:2%c\n", event)), plan, 2, "unused", &out)
				if !seen || (err == nil) != (mode == "exact") || *count != want {
					t.Fatal("independent entry/excess prefix changed", out, err)
				}
				other := out.Phases[1].RelationshipBuildAttempts
				if event == 'B' {
					other = out.Phases[1].RelationshipProjections
				}
				if other != 0 {
					t.Fatal("invented event pair", out)
				}
			})
		}
	}
}
