package t421

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// Actual bounded live/joined parsers over supplied fragments, not native WB.
func TestEpochMarkerWorkspaceHandshake(t *testing.T) {
	for _, mode := range []string{"valid", "split", "early", "missing_ready", "duplicate_ready", "wrong_phase", "canceled", "later_excess", "own_excess"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			plan := accountingTestPlan(t)
			output := &checkoutCommandOutput{remaining: 64 << 20, cancel: cancel}
			tap := newEpochMarkerWorkspaceOutput(output, plan, [32]byte{1})
			if mode != "early" && tap.armMarker() != nil {
				t.Fatal("arm")
			}
			logical := uint64(55)
			if mode == "own_excess" {
				logical = plan.WorkEnvelope.MaximumDataLogicalBytes + 1
			}
			raw := workspaceTestBinding(4) + workspaceTestPair(4, 6, 1, logical, 44)
			if mode != "missing_ready" {
				raw += "WB1:4:6R:0000000000000001\n"
			}
			switch mode {
			case "duplicate_ready":
				raw += "WB1:4:6R:0000000000000001\n"
			case "wrong_phase":
				raw = strings.Replace(raw, "WB1:4:6R", "WB1:4:7R", 1)
			case "later_excess":
				raw += workspaceTestPair(4, 6, 2, plan.WorkEnvelope.MaximumDataLogicalBytes+1, 3)
			}
			if mode == "split" {
				for _, value := range []byte(raw) {
					if _, err := tap.Write([]byte{value}); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				_, _ = tap.Write([]byte(raw))
			}
			if mode == "missing_ready" || mode == "canceled" {
				cancel()
			}
			err := tap.waitMarker(ctx)
			if (err == nil) != (mode == "valid" || mode == "split") {
				t.Fatal(mode, err)
			}
			row := tap.markerSnapshot()
			if mode != "early" && (row.Sample.Completed != 1 || row.Sample.Maximum.LogicalBytes != logical) {
				t.Fatal("positive prefix lost", row)
			}
			if row.LimitExceeded != (mode == "own_excess") {
				t.Fatal("later limit relabeled marker", row)
			}
			if mode == "valid" || mode == "split" {
				finish := workspaceTestPair(4, 6, 2, 200, 2)
				if _, err := tap.Write([]byte(finish)); err != nil {
					t.Fatal(err)
				}
				var joined ExecutionWorkspaceByteObservation
				for _, line := range strings.SplitAfter(output.buffer.String(), "\n") {
					if line != "" {
						if _, err := observeWorkspaceByteEvent([]byte(line), plan, 4, "sha256:01"+strings.Repeat("00", 31), &joined); err != nil {
							t.Fatal(err)
						}
					}
				}
				joined.finish()
				var samples ExecutionMidphaseSamples
				samples.Points[3] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
				samples.Points[3].Maximum.LogicalBytes, samples.Points[3].Maximum.AllocatedBytes = 200, 2
				if !midphaseWorkspacePrefix(4, joined, samples, row) {
					t.Fatal("exact joined prefix")
				}
				row.Sample.Maximum.LogicalBytes++ // Masked by the larger finish, still must refuse.
				if midphaseWorkspacePrefix(4, joined, samples, row) {
					t.Fatal("masked marker mismatch")
				}
			}
		})
	}
}

// Supplied records exercise the generic parser separately from the full
// parent's mandatory marker/finish proof. This is not a native FD6 gate.
func TestEpochMarkerWorkspaceOptionalStreamCompatibility(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, mode := range []string{"legacy_finish", "missing_ready", "missing_finish", "full", "pending_finish", "excess"} {
		t.Run(mode, func(t *testing.T) {
			logical := uint64(55)
			if mode == "excess" {
				logical = plan.WorkEnvelope.MaximumDataLogicalBytes + 1
			}
			raw := workspaceTestBinding(4) + workspaceTestPair(4, 6, 1, logical, 44)
			if mode == "missing_finish" || mode == "full" || mode == "pending_finish" {
				raw += workspaceTestEvent(4, 6, 'R', 1, 0, 0)
			}
			switch mode {
			case "missing_ready", "full":
				raw += workspaceTestPair(4, 6, 2, 200, 2)
			case "pending_finish":
				raw += workspaceTestEvent(4, 6, 'B', 2, 0, 0)
			}
			var joined ExecutionWorkspaceByteObservation
			var parseErr error
			for _, line := range strings.SplitAfter(raw, "\n") {
				if line != "" {
					if _, parseErr = observeWorkspaceByteEvent([]byte(line), plan, 4, "sha256:01"+strings.Repeat("00", 31), &joined); parseErr != nil {
						break
					}
				}
			}
			complete := joined.finish()
			if (parseErr != nil) != (mode == "missing_ready" || mode == "excess") ||
				complete != (mode == "legacy_finish" || mode == "missing_finish" || mode == "full") {
				t.Fatal("generic stream classification", mode, parseErr, joined)
			}
			var samples ExecutionMidphaseSamples
			samples.Points[3] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
			samples.Points[3].Maximum.LogicalBytes, samples.Points[3].Maximum.AllocatedBytes = 200, 2
			marker := ExecutionMarkerWorkspace{Ready: mode != "legacy_finish" && mode != "missing_ready"}
			marker.Sample = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
			marker.Sample.Maximum.LogicalBytes, marker.Sample.Maximum.AllocatedBytes = logical, 44
			if midphaseWorkspacePrefix(4, joined, samples, marker) != (mode == "full") {
				t.Fatal("full parent weakened", mode, joined)
			}
			wantCompleted, wantMaximum := uint64(1), logical
			if mode == "full" {
				wantCompleted, wantMaximum = 2, 200
			}
			if joined.Phases[5].Completed != wantCompleted || joined.Phases[5].Maximum.LogicalBytes != wantMaximum ||
				joined.LimitExceeded != (mode == "excess") {
				t.Fatal("completed prefix or excess classification lost", mode, joined)
			}
		})
	}
}

func TestEpochMarkerWorkspaceDeadlineInput(t *testing.T) {
	epoch := ExecutionEpochConfig{Epoch: 3, Repository: "repo", ReturnSourceCommit: strings.Repeat("a", 40)}
	legacy, err := epochSemanticInput("sha256:"+strings.Repeat("1", 64), epoch, nil)
	if err != nil || bytes.Contains(legacy, []byte("marker_deadline")) {
		t.Fatal("legacy input", err)
	}
	epoch.MarkerDeadlineUnixNano = int64(9223372036854775807)
	raw, err := epochSemanticInput("sha256:"+strings.Repeat("1", 64), epoch, nil)
	if err != nil || len(raw)-len(legacy) != 48 || !bytes.Contains(raw, []byte(`"marker_deadline_unix_nano":9223372036854775807`)) {
		t.Fatal("bounded actual-deadline encoding", len(raw)-len(legacy), err)
	}
	for _, number := range []uint64{1, 2, 4, 5} {
		epoch.Epoch = number
		epoch.ReturnSourceCommit = ""
		var recovery *epochCheckpointRecoveryInput
		if number == 4 {
			recovery = &epochCheckpointRecoveryInput{}
		} // Refusal-only shape, not observed recovery.
		epoch.MarkerDeadlineUnixNano = 0
		if _, err := epochSemanticInput("sha256:"+strings.Repeat("1", 64), epoch, recovery); err != nil {
			t.Fatal("legacy shape", number, err)
		}
		epoch.MarkerDeadlineUnixNano = int64(9223372036854775807)
		if _, err := epochSemanticInput("sha256:"+strings.Repeat("1", 64), epoch, recovery); err == nil {
			t.Fatal("deadline crossed epoch", number)
		}
	}
}
