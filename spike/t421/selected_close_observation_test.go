package t421

import (
	"fmt"
	"strings"
	"testing"
)

// Producer four emits RU8 before TFE in quiesceAndReport. Its recovered
// successor emits only RU8; future pressure/backup terminals are absent.
func selectedCloseTestOutput(t *testing.T, producer uint32) string {
	t.Helper()
	var raw strings.Builder
	for _, line := range strings.SplitAfter(lifecycleTestBindings(producer), "\n") {
		if !strings.HasPrefix(line, "RU1:") {
			raw.WriteString(line)
		}
	}
	if producer == 4 {
		raw.WriteString("RU1:4:6:ccccc\n")
		raw.WriteString(handoffTestCleanupRows(t, 6, 0))
		raw.WriteString("RU1:4:7:ccccc\nRU1:4:8:00000\nTFE1:4:8:sha256:01" + strings.Repeat("00", 31) + "\n")
	} else {
		raw.WriteString("RU1:5:8:ccccc\n")
	}
	return raw.String()
}

func TestExecutionSelectedCloseJoinedObservation(t *testing.T) {
	plan := typedExtractionTestPlan(t)
	if err := applyTypedExtractionCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	for _, producer := range []uint32{5} {
		through := producer + 3
		raw := selectedCloseTestOutput(t, producer)
		for _, test := range []struct {
			name         string
			edit         func(string) string
			joined, want bool
		}{
			{name: "executed prefix", joined: true, want: true},
			{name: "unjoined"},
			{name: "missing executed final", joined: true, edit: func(raw string) string {
				return strings.ReplaceAll(raw, fmt.Sprintf("RU1:%d:%X:ccccc\n", producer, through), "")
			}},
			{name: "future final", joined: true, edit: func(raw string) string { return raw + fmt.Sprintf("RU1:%d:%X:ccccc\n", producer, through+1) }},
			{name: "duplicate final", joined: true, edit: func(raw string) string { return raw + fmt.Sprintf("RU1:%d:%X:ccccc\n", producer, through) }},
			{name: "partial line", joined: true, edit: func(raw string) string { return raw + "partial" }},
		} {
			t.Run(fmt.Sprintf("producer%d/%s", producer, test.name), func(t *testing.T) {
				observed := raw
				if test.edit != nil {
					observed = test.edit(observed)
				}
				got, err := observeExecutionAttemptsForClose([]byte(observed), plan, producer, [32]byte{1}, test.joined, through)
				if (err == nil) != test.want {
					t.Fatalf("selected joined prefix accepted=%t want=%t: %v", err == nil, test.want, err)
				}
				if got.Complete || got.Reuse.Complete {
					t.Fatal("selected prefix manufactured full lifetime completeness")
				}
				if test.want {
					for _, phase := range executionProducerPhases(producer) {
						if got.Reuse.Phases[phase-1].Complete != (phase <= through) {
							t.Fatal("executed terminal lost or future terminal invented", phase)
						}
					}
					if validExecutionJoinedWorkRecord(plan, executionJoinedWorkRecord{Producer: producer, Input: [32]byte{1}, Attempts: got, IndexOffers: ExecutionIndexObservation{Bound: true, Complete: true, ScanComplete: true}}) {
						t.Fatal("prefix accepted as full receipt work")
					}
				}
			})
		}
		if got, err := observeExecutionAttempts([]byte(raw), plan, producer, [32]byte{1}, true); err == nil || got.Complete || got.Reuse.Complete {
			t.Fatal("default full parser accepted a selected prefix")
		}
		for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema, PlanV4Schema, PlanV5Schema} {
			old := plan
			old.Schema = schema
			if _, err := observeExecutionAttemptsForClose([]byte(raw), old, producer, [32]byte{1}, true, through); err == nil {
				t.Fatal("selected close widened historical schema", schema)
			}
		}
		for _, wrong := range []uint32{1, 5, 6, 7, 9, 15} {
			if _, err := observeExecutionAttemptsForClose([]byte(raw), plan, producer, [32]byte{1}, true, wrong); err == nil {
				t.Fatal("unselected boundary accepted", wrong)
			}
		}
	}
}

func TestExecutionCheckpointPredecessorReuseTerminal(t *testing.T) {
	plan := typedExtractionTestPlan(t)
	if err := applyTypedExtractionCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	raw := selectedCloseTestOutput(t, 4)
	for _, mode := range []string{"terminal present", "missing terminal", "duplicate terminal"} {
		t.Run(mode, func(t *testing.T) {
			observed := raw
			if mode == "missing terminal" {
				observed = strings.ReplaceAll(raw, "RU1:4:8:00000\n", "")
			}
			if mode == "duplicate terminal" {
				observed = strings.ReplaceAll(raw, "RU1:4:8:00000\n", "RU1:4:8:00000\nRU1:4:8:00000\n")
			}
			got, err := observeExecutionAttempts([]byte(observed), plan, 4, [32]byte{1}, true)
			want := mode == "terminal present"
			if (err == nil) != want || got.Complete != want || got.Reuse.Complete != want {
				t.Fatal("checkpoint predecessor terminal strictness changed", mode, err)
			}
		})
	}
	for _, invalid := range []uint32{6, 7, 8} {
		if _, err := observeExecutionAttemptsForClose([]byte(raw), plan, 4, [32]byte{1}, true, invalid); err == nil {
			t.Fatal("checkpoint predecessor was weakened into prefix close", invalid)
		}
	}
}
