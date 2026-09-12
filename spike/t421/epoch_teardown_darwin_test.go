//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// These are ownership-entry refusals only, not a modeled successful teardown.
func TestExecutionTeardownRequiresOwnedRun(t *testing.T) {
	for _, test := range []struct {
		name   string
		volume *executionPressureVolume
		run    *ExecutionEpochOneRun
		ctx    context.Context
	}{
		{"no_volume", nil, nil, t.Context()},
		{"no_context", &executionPressureVolume{}, nil, nil},
		{"no_run", &executionPressureVolume{}, nil, t.Context()},
		{"unbound_run", &executionPressureVolume{}, &ExecutionEpochOneRun{}, t.Context()},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.volume.finishRestored(test.ctx, test.run)
			if err == nil || !result.Started.IsZero() || result.Joined || result.CustodyAbsent || result.CleanupClosed ||
				test.volume != nil && test.volume.teardownRun != nil {
				t.Fatal("invalid entry acquired teardown ownership or fabricated evidence")
			}
		})
	}
}

// Real tiny filesystem observations, not a mounted-volume teardown. The test
// thresholds are supplied only to exercise classification without a large file.
func TestExecutionTeardownByteEvidenceSurvivesFailure(t *testing.T) {
	for _, mode := range []string{"complete", "logical_excess", "allocated_excess", "late_close", "later_walk_refused"} {
		t.Run(mode, func(t *testing.T) {
			owner, ctx := custodyByteFixture(t)
			if err := os.WriteFile(filepath.Join(owner.path, "actual"), []byte("retained positive bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			v := &executionPressureVolume{bytes: newCustodyByteObservation(owner), flow: &ExecutionEpochOne{}}
			value, err := v.bytes.sample(ctx, 15, nil)
			if err != nil || value.LogicalBytes == 0 || value.AllocatedBytes == 0 {
				t.Fatal(value, err)
			}
			v.flow.plan.WorkEnvelope.MaximumDataLogicalBytes = value.LogicalBytes
			v.flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes = value.AllocatedBytes
			observed, observationErr := value, error(nil)
			switch mode {
			case "logical_excess":
				v.flow.plan.WorkEnvelope.MaximumDataLogicalBytes--
			case "allocated_excess":
				v.flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes--
			case "late_close":
				// An actual already-closed descriptor makes existing Close fail.
				// It must not poison the previously completed byte observation.
				v.root = owner
				// These prior removal flags are supplied only for result-composition
				// coverage; this tiny fixture performs no image detach/removal.
				v.teardownDetached, v.teardownImageRemoved, v.removed = true, true, true
				if err := owner.file.Close(); err != nil {
					t.Fatal(err)
				}
				if err := v.Close(); err == nil {
					t.Fatal("late descriptor failure hidden")
				}
			case "later_walk_refused":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				observed, observationErr = v.bytes.sample(canceled, 15, nil)
				if observationErr == nil {
					t.Fatal("canceled walk admitted")
				}
			}
			exceeded := mode == "logical_excess" || mode == "allocated_excess"
			if _, err := v.teardownSampleResult(observed, observationErr); (err != nil) != (exceeded || observationErr != nil) {
				t.Fatal("wrong operational sample disposition", err)
			}
			var result executionTeardownResult
			v.mu.Lock()
			v.teardownEvidence(&result)
			v.mu.Unlock()
			if !result.Bytes.Completed || result.Bytes.Maximum != value || result.ByteLimitExceeded != exceeded ||
				result.ByteUnavailable != (mode == "later_walk_refused") || result.CustodyAbsent != (mode == "late_close") || result.CleanupClosed {
				t.Fatal("observation/operation failure conflated", result)
			}
		})
	}
}

// Invalid SID refusal uses the actual native preflight and starts no process.
// Round assignment is modeled; only its actual failed census is copied. This
// proves prefix retention, not successful session teardown or controller EOFs.
func TestExecutionTeardownCensusFailureRetained(t *testing.T) {
	for _, round := range []string{"initial", "before", "post_detach", "after", "close"} {
		t.Run(round, func(t *testing.T) {
			_, ctx := custodyByteFixture(t)
			v := &executionPressureVolume{sessions: []int{0}, flow: &ExecutionEpochOne{}}
			value, err := v.censusTeardownSessions(ctx, false)
			if err == nil || value.RecordedSessions != 1 || value.CompletedCensuses != 0 || value.Errors != 1 {
				t.Fatal(value, err)
			}
			switch round {
			case "initial":
				v.teardownInitial = value
			case "before":
				v.teardownBefore = value
			case "post_detach":
				v.teardownPostDetach = value
			case "after":
				v.teardownAfter = value
			case "close":
				v.teardownRun = &ExecutionEpochOneRun{teardownContext: ctx}
				if err := v.Close(); err == nil {
					t.Fatal("actual final census failure hidden")
				}
			}
			var result executionTeardownResult
			v.teardownEvidence(&result)
			got := map[string]SessionCensusEvidence{"initial": result.InitialJoined, "before": result.BeforeDetach,
				"post_detach": result.AfterDetach, "after": result.AfterCleanup, "close": result.FinalClose}[round]
			if got != value || result.CustodyAbsent || result.CleanupClosed {
				t.Fatal("failed census prefix lost", result)
			}
		})
	}
}
