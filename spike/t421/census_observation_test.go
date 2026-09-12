package t421

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestExecutionSourceCensusObservedPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	begin, end := "SB2:2:2B\n", "SB2:2:2E\n"
	batch := "SB2:2:2D:000000000000000a:0000000000000003\n"
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
		{"embedded", begin + batch + "private SB2:2:2E\n", false, 10, 3, 1, 0},
		{"split", begin + batch + strings.Repeat("x", maxExecutionAttemptLine-1) + end, false, 10, 3, 1, 0},
		{"duplicate_binding", begin + batch + attemptTestBindings(), false, 10, 3, 1, 0},
		{"uppercase", begin + "SB2:2:2D:000000000000000A:0000000000000003\n", false, 0, 0, 1, 0},
		{"unique_exceeds", begin + "SB2:2:2D:0000000000000001:0000000000000003\n", false, 0, 0, 1, 0},
		{"zero_batch", begin + "SB2:2:2D:0000000000000000:0000000000000000\n", false, 0, 0, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(attemptTestBindings()+test.events), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.SourceCensus.Complete != test.complete || got.Phases[1].SourceLogicalBytes != test.logical || got.Phases[1].SourceUniqueBytes != test.unique || got.SourceCensus.Started[1] != test.started || got.SourceCensus.Finished[1] != test.finished {
				t.Fatal(got, err)
			}
		})
	}
	missing := strings.Replace(attemptTestBindings(), "SBB2:2:sha256:01"+strings.Repeat("00", 31)+"\n", "", 1)
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
		line := "SB2:2:2B\n"
		switch mode {
		case "start":
			out.SourceCensus.Started[1] = math.MaxUint64
		case "logical":
			out.SourceCensus.Started[1] = 1
			out.Phases[1].SourceLogicalBytes = math.MaxUint64
			line = "SB2:2:2D:0000000000000001:0000000000000001\n"
		case "unique":
			out.SourceCensus.Started[1] = 1
			out.Phases[1].SourceUniqueBytes = math.MaxUint64
			line = "SB2:2:2D:0000000000000001:0000000000000001\n"
		}
		if ok, err := observeCensusEvent([]byte(line), plan, 2, input, &out); !ok || err == nil {
			t.Fatal(mode, out, err)
		}
	}
	for _, line := range []string{"SBB2:4:" + input + "\n", "SB2:4:8B\n", "SB2:4:8D:0000000000000001:0000000000000001\n", "SB2:4:8E\n"} {
		if _, err := executionTerminalFooter([]byte(fmt.Sprintf("TFE1:4:8:%s\n", input)+line), [32]byte{1}); err == nil {
			t.Fatal("post-footer source work accepted", line)
		}
	}
}

// Supplied wire proves retained subset accounting, not actual census or phase work.
func TestExecutionSourceCensusSuccessfulTerminal(t *testing.T) {
	plan := accountingTestPlan(t)
	begin, failed := "SB2:2:2B\n", "SB2:2:2E\n"
	batch := "SB2:2:2D:000000000000000a:0000000000000003\n"
	complete := "SB2:2:2C:0000000000000004\n"
	zero := "SB2:2:2C:0000000000000000\n"
	for _, test := range []struct {
		name, events                                  string
		valid                                         bool
		started, finished, succeeded, owners, logical uint64
	}{
		{"zero", begin + zero, true, 1, 1, 1, 0, 0},
		{"complete", begin + batch + complete, true, 1, 1, 1, 4, 10},
		{"failed", begin + batch + failed, true, 1, 1, 0, 0, 10},
		{"mixed", begin + begin + batch + complete + failed, true, 2, 2, 1, 4, 10},
		{"repeat", begin + batch + complete + begin + batch + complete, true, 2, 2, 2, 8, 20},
		{"missing_terminal", begin + batch, false, 1, 0, 0, 0, 10},
		{"no_begin", complete, false, 0, 0, 0, 0, 0},
		{"double_terminal", begin + complete + failed, false, 1, 1, 1, 4, 0},
		{"duplicate_complete", begin + complete + complete, false, 1, 1, 1, 4, 0},
		{"late_failure", begin + batch + complete + "SB2:2:2?\n", false, 1, 1, 1, 4, 10},
		{"wrong_phase", begin + "SB2:2:3C:0000000000000004\n", false, 1, 0, 0, 0, 0},
		{"uppercase", begin + "SB2:2:2C:000000000000000A\n", false, 1, 0, 0, 0, 0},
		{"oversized", begin + "SB2:2:2C:10000000000000000\n", false, 1, 0, 0, 0, 0},
		{"old_record", begin + "SB1:2:2E\n", false, 1, 0, 0, 0, 0},
		{"old_binding", "SBB1:2:sha256:01" + strings.Repeat("00", 31) + "\n", false, 0, 0, 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(attemptTestBindings()+test.events), plan, 2, [32]byte{1}, true)
			census := got.SourceCensus
			if (err == nil) != test.valid || got.Complete != test.valid || census.Started[1] != test.started || census.Finished[1] != test.finished || census.Succeeded[1] != test.succeeded || census.RegularOwners[1] != test.owners || got.Phases[1].SourceLogicalBytes != test.logical {
				t.Fatal(got, err)
			}
		})
	}
	input := "sha256:01" + strings.Repeat("00", 31)
	for _, mode := range []string{"max", "owners_overflow", "success_overflow"} {
		out := ExecutionAttemptObservation{SourceCensus: ExecutionSourceCensusObservation{Bound: true}}
		out.SourceCensus.Started[1] = 1
		line := "SB2:2:2C:ffffffffffffffff\n"
		switch mode {
		case "owners_overflow":
			out.SourceCensus.RegularOwners[1] = 1
		case "success_overflow":
			out.SourceCensus.Succeeded[1] = math.MaxUint64
		}
		before := out.SourceCensus
		ok, err := observeCensusEvent([]byte(line), plan, 2, input, &out)
		if !ok || (err == nil) != (mode == "max") {
			t.Fatal(mode, out, err)
		}
		if mode == "max" {
			if out.SourceCensus.RegularOwners[1] != math.MaxUint64 || out.SourceCensus.Succeeded[1] != 1 || out.SourceCensus.Finished[1] != 1 {
				t.Fatal(out)
			}
		} else if out.SourceCensus != before {
			t.Fatal("overflow changed retained prefix", out)
		}
	}
	if _, err := executionTerminalFooter([]byte("TFE1:4:8:"+input+"\nSB2:4:8C:0000000000000004\n"), [32]byte{1}); err == nil {
		t.Fatal("post-footer successful census admitted")
	}
}
