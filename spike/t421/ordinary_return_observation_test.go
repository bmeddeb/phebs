package t421

import (
	"strings"
	"testing"
)

// Ordinary Linux qualification stops after ReturnA. Its genuine producer-four
// output has no final-authority reuse terminals for checkpoint phases 7 or 8.
func TestExecutionOrdinaryReturnJoinedObservation(t *testing.T) {
	plan := typedExtractionTestPlan(t)
	if err := applyTypedExtractionCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	raw := lifecycleTestBindings(4)
	raw = strings.ReplaceAll(raw, "RU1:4:7:00000\n", "")
	raw = strings.ReplaceAll(raw, "RU1:4:8:00000\n", "")
	raw += handoffTestCleanupRows(t, 6, 0)
	raw += "IXB1:4:sha256:01" + strings.Repeat("00", 31) + "\n"
	for _, test := range []struct {
		name                                      string
		tail                                      string
		checkpoint, joined, failed, missing, want bool
	}{
		{name: "ordinary return", joined: true, want: true},
		{name: "checkpoint still needs future terminals", checkpoint: true, joined: true},
		{name: "unjoined"},
		{name: "prior failure", joined: true, failed: true},
		{name: "missing executed terminal", joined: true, missing: true},
		{name: "unexpected future terminal", joined: true, tail: "RU1:4:7:00000\n"},
		{name: "duplicate executed terminal", joined: true, tail: "RU1:4:6:00000\n"},
		{name: "partial line", joined: true, tail: "unfinished"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := &checkoutCommandOutput{}
			observed := raw + test.tail
			if test.missing {
				observed = strings.ReplaceAll(observed, "RU1:4:6:00000\n", "")
			}
			output.buffer.WriteString(observed)
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, epoch: ExecutionEpochConfig{Epoch: 3}, output: output, attemptInput: [32]byte{1}, checkpointAllowed: test.checkpoint}
			result := ExecutionEpochOneResult{RootJoined: test.joined}
			var failure error
			if test.failed {
				failure = ErrExecutionEpochOne
			}
			err := run.finishAttemptObservation(t.Context(), &result, executionProcessDeath{}, failure)
			if (err == nil) != test.want {
				t.Fatalf("ordinary joined prefix acceptance=%t, want %t: %v", err == nil, test.want, err)
			}
			if result.Attempts.Complete || result.Attempts.Reuse.Complete {
				t.Fatal("ordinary prefix manufactured full producer completeness")
			}
			if test.want && (!result.Attempts.Reuse.Phases[5].Complete || !result.IndexOffers.Complete || result.Attempts.Reuse.Phases[6].Complete || result.Attempts.Reuse.Phases[7].Complete) {
				t.Fatal("actual executed terminal or joined index evidence lost, or future phase fabricated")
			}
			if validExecutionJoinedWorkRecord(plan, executionJoinedWorkRecord{Producer: 4, Input: [32]byte{1}, Attempts: result.Attempts, IndexOffers: result.IndexOffers}) {
				t.Fatal("ordinary prefix accepted as full receipt work")
			}
		})
	}
	if got, err := observeExecutionAttempts([]byte(raw), plan, 4, [32]byte{1}, true); err == nil || got.Complete || got.Reuse.Complete {
		t.Fatal("default full parser accepted missing checkpoint terminals")
	}
}
