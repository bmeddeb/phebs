package t421

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestExecutionSourceCensusObservedPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	begin, end := "SB1:2:2B\n", "SB1:2:2E\n"
	batch := "SB1:2:2D:000000000000000a:0000000000000003\n"
	for _, test := range []struct {
		name, events                       string
		complete                           bool
		logical, unique, started, finished uint64
	}{
		{"bound_zero", "", true, 0, 0, 0, 0},
		{"empty_call", begin + end, true, 0, 0, 1, 1},
		{"actual_bytes", begin + batch + end, true, 10, 3, 1, 1},
		{"repeat", begin + batch + end + begin + batch + end, true, 20, 6, 2, 2},
		{"concurrent", begin + begin + batch + end + batch + end, true, 20, 6, 2, 2},
		{"missing_end", begin + batch, false, 10, 3, 1, 0},
		{"missing_begin", batch, false, 0, 0, 0, 0},
		{"extra_end", begin + batch + end + end, false, 10, 3, 1, 1},
		{"wrong_phase", begin + strings.Replace(batch, ":2D", ":3D", 1), false, 0, 0, 1, 0},
		{"partial", begin + batch + "SB", false, 10, 3, 1, 0},
		{"embedded", begin + batch + "private SB1:2:2E\n", false, 10, 3, 1, 0},
		{"split", begin + batch + strings.Repeat("x", maxExecutionAttemptLine-1) + end, false, 10, 3, 1, 0},
		{"duplicate_binding", begin + batch + attemptTestBindings(), false, 10, 3, 1, 0},
		{"uppercase", begin + "SB1:2:2D:000000000000000A:0000000000000003\n", false, 0, 0, 1, 0},
		{"unique_exceeds", begin + "SB1:2:2D:0000000000000001:0000000000000003\n", false, 0, 0, 1, 0},
		{"zero_batch", begin + "SB1:2:2D:0000000000000000:0000000000000000\n", false, 0, 0, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(attemptTestBindings()+test.events), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.SourceCensus.Complete != test.complete || got.Phases[1].SourceLogicalBytes != test.logical || got.Phases[1].SourceUniqueBytes != test.unique || got.SourceCensus.Started[1] != test.started || got.SourceCensus.Finished[1] != test.finished {
				t.Fatal(got, err)
			}
		})
	}
	missing := strings.Replace(attemptTestBindings(), "SBB1:2:sha256:01"+strings.Repeat("00", 31)+"\n", "", 1)
	if got, err := observeExecutionAttempts([]byte(missing), plan, 2, [32]byte{1}, true); err == nil || got.SourceCensus.Bound {
		t.Fatal(got, err)
	}
	plan.WorkEnvelope.Phases[1].SourceLogicalBytes.Maximum = 9
	got, err := observeExecutionAttempts([]byte(attemptTestBindings()+begin+batch+end), plan, 2, [32]byte{1}, true)
	if err == nil || got.Phases[1].SourceLogicalBytes != 10 || got.Phases[1].SourceUniqueBytes != 3 || got.SourceCensus.Complete {
		t.Fatal(got, err)
	}
}

func TestExecutionSourceCensusOverflowAndFooter(t *testing.T) {
	plan := accountingTestPlan(t)
	input := "sha256:01" + strings.Repeat("00", 31)
	for _, mode := range []string{"start", "logical", "unique"} {
		out := ExecutionAttemptObservation{SourceCensus: ExecutionSourceCensusObservation{Bound: true}}
		line := "SB1:2:2B\n"
		switch mode {
		case "start":
			out.SourceCensus.Started[1] = math.MaxUint64
		case "logical":
			out.SourceCensus.Started[1] = 1
			out.Phases[1].SourceLogicalBytes = math.MaxUint64
			line = "SB1:2:2D:0000000000000001:0000000000000001\n"
		case "unique":
			out.SourceCensus.Started[1] = 1
			out.Phases[1].SourceUniqueBytes = math.MaxUint64
			line = "SB1:2:2D:0000000000000001:0000000000000001\n"
		}
		if ok, err := observeCensusEvent([]byte(line), plan, 2, input, &out); !ok || err == nil {
			t.Fatal(mode, out, err)
		}
	}
	for _, line := range []string{"SBB1:4:" + input + "\n", "SB1:4:8B\n", "SB1:4:8D:0000000000000001:0000000000000001\n", "SB1:4:8E\n"} {
		if _, err := executionTerminalFooter([]byte(fmt.Sprintf("TFE1:4:8:%s\n", input)+line), [32]byte{1}); err == nil {
			t.Fatal("post-footer source work accepted", line)
		}
	}
}
