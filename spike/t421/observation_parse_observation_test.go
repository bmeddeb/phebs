package t421

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestObservationParseCompactParser(t *testing.T) {
	plan := accountingTestPlan(t)
	header := "OPB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	other := strings.Replace(attemptTestBindings(), header, "", 1)
	for _, test := range []struct {
		name, raw string
		count     uint64
		complete  bool
	}{
		{"zero", header, 0, true},
		{"repeat", header + "OP1:2:2\nOP1:2:2\n", 2, true},
		{"missing", "", 0, false}, {"unbound", "OP1:2:2\n", 0, false},
		{"duplicate", header + header, 0, false},
		{"wrong input", strings.Replace(header, "01", "02", 1), 0, false},
		{"uppercase input", strings.Replace(header, "01", "AF", 1), 0, false},
		{"binding version", strings.Replace(header, "OPB1:", "OPB2:", 1), 0, false},
		{"malformed binding", header + "OPBbroken\n", 0, false},
		{"event version", header + "OP2:2:2\n", 0, false},
		{"producer", header + "OP1:3:2\n", 0, false},
		{"phase", header + "OP1:2:5\n", 0, false},
		{"phase zero", header + "OP1:2:0\n", 0, false},
		{"phase malformed", header + "OP1:2:Z\n", 0, false},
		{"zero ceiling", header + "OP1:2:3\n", 0, false},
		{"embedded", header + "diagnostic OP1:2:2\n", 0, false},
		{"split", header + strings.Repeat("x", maxExecutionAttemptLine-2) + "OP1:2:2\n", 0, false},
		{"partial", header + "OP1:2:2\nOP1:2:", 1, false},
		{"bad tail", header + "OP1:2:2\nOP2:2:2\n", 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(other+test.raw), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[1].ObservationParses != test.count || got.Phases[1].SourceBlobAttempts != 0 {
				t.Fatal(got, err)
			}
		})
	}
	plan.WorkEnvelope.Phases[1].ObservationParses.Maximum = 1
	got, err := observeExecutionAttempts([]byte(other+header+"OP1:2:2\nOP1:2:2\n"), plan, 2, [32]byte{1}, true)
	if err == nil || got.Complete || got.Phases[1].ObservationParses != 2 {
		t.Fatal("first actual excess lost", got, err)
	}
	out := ExecutionAttemptObservation{ObservationBound: true}
	out.Phases[1].ObservationParses = math.MaxUint64
	if recognized, err := observeBlobEvent([]byte("OP1:2:2\n"), plan, 2, "unused", &out); !recognized || err == nil || out.Phases[1].ObservationParses != math.MaxUint64 {
		t.Fatal("counter overflow", out, err)
	}
}

func TestObservationParseProducerBinding(t *testing.T) {
	plan := accountingTestPlan(t)
	for producer := uint32(2); producer <= 6; producer++ {
		input := "sha256:01" + strings.Repeat("00", 31) + "\n"
		raw := fmt.Sprintf("ATB1:%d:%sSRB1:%d:%sOPB1:%d:%s", producer, input, producer, input, producer, input)
		got, err := observeExecutionAttempts([]byte(raw), plan, producer, [32]byte{1}, true)
		if err != nil || !got.Complete || !got.ObservationBound {
			t.Fatal("bound zero refused", producer, got, err)
		}
		for _, phase := range executionProducerPhases(producer) {
			got, err := observeExecutionAttempts([]byte(raw+fmt.Sprintf("OP1:%d:%X\n", producer, phase)), plan, producer, [32]byte{1}, true)
			if (err == nil) != (plan.WorkEnvelope.Phases[phase-1].ObservationParses.Maximum > 0) || got.Phases[phase-1].ObservationParses != 1 {
				t.Fatal("allowed phase or exact-zero ceiling", producer, phase, got, err)
			}
		}
	}
}
