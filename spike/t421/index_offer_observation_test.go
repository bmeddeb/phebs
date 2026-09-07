package t421

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestIndexOfferObservation(t *testing.T) {
	plan := Plan{Schema: PlanV3Schema, PhaseOrder: frozenPhaseOrder()}
	for _, phase := range plan.PhaseOrder {
		plan.WorkEnvelope.Phases = append(plan.WorkEnvelope.Phases, PhaseWorkBounds{Phase: phase, IndexFiles: CounterBound{Maximum: 2}})
	}
	header := "IXB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	for _, test := range []struct {
		name, raw         string
		count             uint64
		healthy, complete bool
	}{
		{"zero", header, 0, true, true},
		{"two", header + "Ib2\nI2\nI2\nIe2:2\n", 2, true, true},
		{"two children", header + "Ib2\nI2\nIe2:1\nIb2\nI2\nIe2:1\n", 2, true, true},
		{"cooperative failure then retry", header + "Ib2\nI2\nIf2:1\nIb2\nI2\nIe2:1\n", 2, true, true},
		{"failed valid prefix", header + "Ib2\nI2\nIe2:1\n", 1, false, false},
		{"missing", "", 0, true, false},
		{"unbound", "Ib2\nI2\n", 0, true, false},
		{"wrong producer", strings.Replace(header, "IXB1:2:", "IXB1:3:", 1), 0, true, false},
		{"wrong phase", header + "Ib5\n", 0, true, false},
		{"no child", header + "I2\n", 0, true, false},
		{"no end", header + "Ib2\nI2\n", 1, true, false},
		{"bad tally", header + "Ib2\nI2\nIe2:0\n", 1, true, false},
		{"partial", header + "Ib2\nI2\nI", 1, true, false},
		{"excess", header + "Ib2\nI2\nI2\nI2\n", 3, true, false},
		{"raw child", header + "ZIB1\n", 0, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionIndexOffers([]byte(test.raw), plan, 2, [32]byte{1}, true, test.healthy)
			if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[1].Offers != test.count {
				t.Fatal(got, err)
			}
		})
	}
	got, err := observeExecutionIndexOffers([]byte(header+"Ib2\nI2\n"), plan, 2, [32]byte{1}, false, true)
	if err == nil || got.Phases[1].Offers != 0 {
		t.Fatal("unjoined buffer inspected", got, err)
	}
}

func TestIndexOfferOutputHeadroom(t *testing.T) {
	plan := accountingTestPlan(t)
	var offers, children, framing uint64
	for _, phase := range executionProducerPhases(2) {
		row := plan.WorkEnvelope.Phases[phase-1]
		offers += row.IndexFiles.Maximum
		for _, role := range row.ControlledDispatchRoles {
			if role.Name == "zoekt-git-index" {
				children += role.Maximum
				framing += role.Maximum * uint64(9+len(strconv.FormatUint(row.IndexFiles.Maximum, 10)))
			}
		}
	}
	if offers != 4_063_208 {
		t.Fatal(offers)
	}
	const binding = 79
	indexBytes := 3*offers + binding + framing
	const sourceBytes = 16_462_223
	if indexBytes+sourceBytes >= 64<<20 {
		t.Fatal("source/index subset exceeds shared cap")
	}
	t.Logf("index offers=%d child ceiling=%d index bytes=%d source+index=%d remaining=%d", offers, children, indexBytes, indexBytes+sourceBytes, (64<<20)-indexBytes-sourceBytes)
	// Candidate/B2/ordinary output and future census events still share this
	// remainder. This is not a full-profile fit claim or a log-cap increase.
}

func TestIndexOfferFinishStablePrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	header := attemptTestBindings() + "IXB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	raw := []byte(header + "Ib2\nI2\nIe2:1\n")
	for _, mode := range []string{"healthy", "process failed", "overflow", "unjoined"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			output := &checkoutCommandOutput{remaining: int64(len(raw)), cancel: cancel}
			if _, err := output.Write(raw); err != nil {
				t.Fatal(err)
			}
			var failure error
			if mode == "process failed" {
				failure = ErrExecutionEpochOne
			}
			if mode == "overflow" {
				if _, err := output.Write([]byte("lost complete line\n")); err == nil || ctx.Err() == nil {
					t.Fatal("overflow did not fail")
				}
				failure = ErrExecutionEpochOne
			}
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, output: output, attemptInput: [32]byte{1}}
			result := ExecutionEpochOneResult{RootJoined: mode != "unjoined"}
			err := run.finishAttemptObservation(&result, failure)
			want := uint64(1)
			if mode == "unjoined" {
				want = 0
			}
			if (err == nil) != (mode == "healthy") || result.IndexOffers.Complete != (mode == "healthy") || result.IndexOffers.Phases[1].Offers != want {
				t.Fatal(result.IndexOffers, err)
			}
		})
	}
}
