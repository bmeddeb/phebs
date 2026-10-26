package t421

import (
	"fmt"
	"strings"
	"testing"
)

// Model the real report shape, not a completed lifetime: interrupted producer
// four has final authorities 6/7; its recovered successor has only authority 8.
func selectedCloseTestOutput(t *testing.T, producer uint32) string {
	t.Helper()
	var raw strings.Builder
	for _, line := range strings.SplitAfter(lifecycleTestBindings(producer), "\n") {
		if !strings.HasPrefix(line, "RU1:") {
			raw.WriteString(line)
		}
	}
	if producer == 4 {
		raw.WriteString("RU1:4:6:ccccc\nRU1:4:7:ccccc\n")
		raw.WriteString(handoffTestCleanupRows(t, 6, 0))
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
	for _, producer := range []uint32{4, 5} {
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
		for _, wrong := range []uint32{1, 5, 9, 15} {
			if _, err := observeExecutionAttemptsForClose([]byte(raw), plan, producer, [32]byte{1}, true, wrong); err == nil {
				t.Fatal("unselected boundary accepted", wrong)
			}
		}
	}
}
