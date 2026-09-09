package t421

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestExecutionCatalogCensusPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	b, s, d, e := "GC1:2:2B\n", "GC1:2:2S\n", "GC1:2:2D:000000000000000a\n", "GC1:2:2E\n"
	for _, test := range []struct {
		name, events      string
		complete          bool
		children, records uint64
	}{
		{"bound_zero", "", true, 0, 0}, {"no_child", b + "GC1:2:2N\n", true, 0, 0}, {"empty_child", b + s + e, true, 1, 0},
		{"actual", b + s + d, true, 1, 10}, {"missing_final", b + s, false, 1, 0},
		{"missing_begin", s, false, 0, 0}, {"duplicate_child", b + s + s, false, 1, 0},
		{"unstarted_records", b + d, false, 0, 0}, {"wrong_phase", b + s + strings.Replace(d, ":2D", ":3D", 1), false, 1, 0},
		{"duplicate_records", b + s + d + d, false, 1, 10},
		{"borrow_closed_child", b + s + e + b + d, false, 1, 0},
		{"wrong_no_child", b + s + "GC1:2:2N\n", false, 1, 0},
		{"concurrent_no_child", b + b + s + "GC1:2:2N\n" + d, true, 1, 10},
		{"concurrent_child_first", b + b + s + e + "GC1:2:2N\n", true, 1, 0},
		{"no_child_inside", b + s + b + "GC1:2:2N\n" + d, true, 1, 10},
		{"no_child_then_child", b + "GC1:2:2N\n" + b + s + d, true, 1, 10},
		{"no_child_cannot_supply_records", b + "GC1:2:2N\n" + b + d, false, 0, 0},
		{"extra_end", b + s + d + e, false, 1, 10}, {"partial", b + s + d + "GC", false, 1, 10},
		{"embedded", b + s + d + "private " + e, false, 1, 10}, {"split", b + s + d + strings.Repeat("x", maxExecutionAttemptLine-1) + e, false, 1, 10},
		{"uppercase", b + s + "GC1:2:2D:000000000000000A\n", false, 1, 0},
		{"zero_record", b + s + "GC1:2:2D:0000000000000000\n", false, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(attemptTestBindings()+test.events), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.CatalogCensus.Complete != test.complete || got.Phases[1].CensusChildren != test.children || got.Phases[1].CensusRecords != test.records {
				t.Fatal(got, err)
			}
		})
	}
	missing := strings.Replace(attemptTestBindings(), "GCB1:2:sha256:01"+strings.Repeat("00", 31)+"\n", "", 1)
	if got, err := observeExecutionAttempts([]byte(missing), plan, 2, [32]byte{1}, true); err == nil || got.CatalogCensus.Bound {
		t.Fatal(got, err)
	}
	plan.WorkEnvelope.Phases[1].CensusRecords.Maximum = 9
	if got, err := observeExecutionAttempts([]byte(attemptTestBindings()+b+s+d), plan, 2, [32]byte{1}, true); err == nil || got.Phases[1].CensusRecords != 10 || got.CatalogCensus.Complete {
		t.Fatal(got, err)
	}
	plan = accountingTestPlan(t)
	if got, err := observeExecutionAttempts([]byte(attemptTestBindings()+b+s+d+b+s), plan, 2, [32]byte{1}, true); err == nil || got.Phases[1].CensusChildren != 2 {
		t.Fatal("repeat child crossing lost", got, err)
	}
}

func TestExecutionCatalogCensusOverflowFooter(t *testing.T) {
	plan := accountingTestPlan(t)
	input := "sha256:01" + strings.Repeat("00", 31)
	for _, mode := range []string{"begin", "records"} {
		out := ExecutionAttemptObservation{CatalogCensus: ExecutionCatalogCensusObservation{Bound: true}}
		line := "GC1:2:2B\n"
		if mode == "begin" {
			out.CatalogCensus.Started[1] = math.MaxUint64
		} else {
			out.CatalogCensus.Started[1] = 1
			out.Phases[1].CensusChildren = 1
			out.Phases[1].CensusRecords = math.MaxUint64
			line = "GC1:2:2D:0000000000000001\n"
		}
		if ok, err := observeCatalogCensusEvent([]byte(line), plan, 2, input, &out); !ok || err == nil {
			t.Fatal(mode, out, err)
		}
	}
	for _, line := range []string{"GCB1:4:" + input + "\n", "GC1:4:8B\n", "GC1:4:8S\n", "GC1:4:8D:0000000000000001\n", "GC1:4:8E\n"} {
		if _, err := executionTerminalFooter([]byte(fmt.Sprintf("TFE1:4:8:%s\n", input)+line), [32]byte{1}); err == nil {
			t.Fatal("post-footer accepted", line)
		}
	}
}
