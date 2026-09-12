package t421

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Supplied writer fragments exercise actual bounded tee/parser/order behavior,
// not a native observation. The joined complete parser remains independently required.
func TestEpochWarmWorkspaceStream(t *testing.T) {
	for _, mode := range []string{"valid", "split", "wrong_binding", "early", "failed", "overshoot", "long_reserved", "missing", "write_limit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			output := &checkoutCommandOutput{remaining: 64 << 20, cancel: cancel}
			plan := accountingTestPlan(t)
			tap := newEpochWarmWorkspaceOutput(output, plan, [32]byte{1})
			initial := workspaceTestBinding(2) + workspaceTestPair(2, 2, 1, 11, 22)
			if mode == "wrong_binding" {
				initial = strings.Replace(initial, "sha256:01", "sha256:02", 1)
			}
			write := func(raw string) error {
				if mode == "split" {
					for _, b := range []byte(raw) {
						if _, err := tap.Write([]byte{b}); err != nil {
							return err
						}
					}
					return nil
				}
				_, err := tap.Write([]byte(raw))
				return err
			}
			if err := write(initial); err != nil {
				if mode != "wrong_binding" {
					t.Fatal(err)
				}
				return
			}
			if mode != "early" && tap.arm() != nil {
				t.Fatal("arm")
			}
			logical := uint64(33)
			if mode == "overshoot" {
				logical = plan.WorkEnvelope.MaximumDataLogicalBytes + 1
			}
			raw := workspaceTestPair(2, 3, 2, logical, 44)
			switch mode {
			case "failed":
				raw = strings.SplitAfter(raw, "\n")[0] + "WB1:2:3F:0000000000000002\n"
			case "long_reserved":
				raw = strings.Repeat("x", 90) + "WB1:\n"
			case "missing":
				raw = "unrelated partial"
				cancel()
			case "write_limit":
				output.remaining = 0
			}
			writeErr := write(raw)
			waitErr := tap.wait(ctx)
			valid := mode == "valid" || mode == "split"
			if (waitErr == nil) != valid || valid && writeErr != nil {
				t.Fatal(mode, writeErr, waitErr)
			}
			row, unavailable, exceeded := tap.snapshot()
			if mode == "overshoot" && (row.Completed != 1 || row.Maximum.LogicalBytes != logical || unavailable || !exceeded) {
				t.Fatal("measured excess lost or recast as unavailable", row, unavailable)
			}
			if valid && (row.Completed != 1 || row.Maximum.LogicalBytes != 33) {
				t.Fatal(row)
			}
			if valid {
				// Joined warm maximum can exceed either endpoint independently.
				if write(workspaceTestPair(2, 3, 3, 20, 60)) != nil {
					t.Fatal("finish")
				}
				after, _, _ := tap.snapshot()
				if after != row {
					t.Fatal("later finish overwrote start evidence")
				}
				_ = write("WB1:bad\n")
				after, unavailable, _ := tap.snapshot()
				if after != row || unavailable {
					t.Fatal("late output error revoked completed start")
				}
			}
		})
	}
}

func TestEpochWarmClockBeforeResume(t *testing.T) {
	for _, mode := range []string{"valid", "caller_clip", "expired_cold", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			run := &ExecutionEpochOneRun{stop: make(chan struct{}), coldDeadline: time.Now().Add(time.Minute),
				lifetimeDeadline: time.Now().Add(time.Hour), warmLimit: 20 * time.Minute, cancelRun: cancel}
			run.setPhaseDeadlineLocked(run.coldDeadline)
			defer run.stopPhaseDeadline()
			if mode == "expired_cold" {
				run.coldDeadline = time.Now().Add(-time.Second)
			}
			if mode == "canceled" {
				cancel()
			}
			caller := context.Context(ctx)
			if mode == "valid" {
				caller = context.WithoutCancel(ctx)
			}
			err := run.completeColdAtBoundary(caller, true)
			if (err == nil) != (mode == "valid" || mode == "caller_clip") {
				t.Fatal(mode, err)
			}
			if err == nil {
				if !run.warm || !time.Now().Before(run.phaseDeadline) {
					t.Fatal("warm clock absent")
				}
				if mode == "caller_clip" {
					d, _ := ctx.Deadline()
					if !run.phaseDeadline.Equal(d) {
						t.Fatal("caller renewed")
					}
				}
			}
		})
	}
}

func TestEpochWarmWorkspaceKeepsControlBudget(t *testing.T) {
	plan := accountingTestPlan(t)
	bounds, err := epochOneBounds(plan, epochOnePhysicalB)
	if err != nil || bounds.controlPairs != 21 || bounds.outputBytes != 64<<20 ||
		!strings.Contains(plan.ProcessAccounting.PhaseFencePolicy, "WarmStartWorkspace-Resume3-PC01-reserved24:32") {
		t.Fatal("fixed byte coordination changed admission or lost recipe binding", bounds, err)
	}
}

// Supplied actual-format records test two-stage completion: S is real byte
// evidence even when R is absent; neither ACK nor S may release the query wait.
func TestEpochPhysicalWorkspaceReadyStream(t *testing.T) {
	for _, mode := range []string{"valid", "missing_ready", "ready_before_sample", "wrong_sequence", "duplicate", "finish_before_ready", "masked_middle"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			plan := accountingTestPlan(t)
			tap := newEpochWarmWorkspaceOutput(&checkoutCommandOutput{remaining: 64 << 20, cancel: cancel}, plan, [32]byte{1})
			write := func(raw string) error { _, err := tap.Write([]byte(raw)); return err }
			if write(workspaceTestBinding(2)+workspaceTestPair(2, 2, 1, 11, 22)) != nil || tap.arm() != nil ||
				write(workspaceTestPair(2, 3, 2, 33, 44)+workspaceTestPair(2, 3, 3, 55, 66)+workspaceTestPair(2, 4, 4, 100, 200)) != nil ||
				tap.armPhysical() != nil {
				t.Fatal("supplied prefix")
			}
			if mode == "ready_before_sample" {
				if write("WB1:2:4R:0000000000000005\n") == nil || tap.waitPhysical(ctx) == nil {
					t.Fatal("R without S accepted")
				}
				return
			}
			if write(workspaceTestPair(2, 4, 5, 75, 80)) != nil {
				t.Fatal("middle S")
			}
			select {
			case <-tap.physicalReady:
				t.Fatal("S alone released parent")
			default:
			}
			row, unavailable, _ := tap.physicalSnapshot()
			if row.Completed != 1 || row.Maximum.LogicalBytes != 75 || row.Maximum.AllocatedBytes != 80 || unavailable {
				t.Fatal("positive completed middle missing")
			}
			switch mode {
			case "missing_ready":
				cancel() // Models a failed/closed native continuation after S.
			case "wrong_sequence":
				_ = write("WB1:2:4R:0000000000000004\n")
			case "finish_before_ready":
				_ = write(workspaceTestPair(2, 4, 6, 50, 300))
			default:
				if write("WB1:2:4R:0000000000000005\n") != nil {
					t.Fatal("ready")
				}
				if mode == "duplicate" {
					_ = write("WB1:2:4R:0000000000000005\n")
				}
			}
			valid := mode == "valid" || mode == "masked_middle"
			if (tap.waitPhysical(ctx) == nil) != valid {
				t.Fatal("readiness predicate", mode)
			}
			after, _, _ := tap.physicalSnapshot()
			if after != row {
				t.Fatal("failed readiness erased real S")
			}
			if valid {
				if write(workspaceTestPair(2, 4, 6, 50, 300)) != nil {
					t.Fatal("finish")
				}
				stream := tap.observation
				if !stream.finish() {
					t.Fatal("complete joined stream")
				}
				samples := ExecutionMidphaseSamples{PostAuthor: row}
				samples.Points[0] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
				samples.Points[0].Maximum.LogicalBytes, samples.Points[0].Maximum.AllocatedBytes = 100, 200
				samples.Points[1] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
				samples.Points[1].Maximum.LogicalBytes, samples.Points[1].Maximum.AllocatedBytes = 50, 300
				if mode == "masked_middle" {
					samples.PostAuthor.Maximum.LogicalBytes++ // Still below phase maximum100.
				}
				if midphaseWorkspacePrefix(2, stream, samples) != (mode == "valid") {
					t.Fatal("individual middle not bound")
				}
			} else if mode == "missing_ready" {
				stream := tap.observation
				if stream.finish() || !stream.Unavailable || stream.Phases[3].Completed != 2 ||
					stream.Phases[3].Maximum.LogicalBytes != 100 {
					t.Fatal("failed reopen lost actual byte prefix", stream)
				}
			}
		})
	}
}

// A later failed sample cannot relabel this earlier completed point. The
// supplied WB stream still fails globally; only point attribution is isolated.
func TestEpochPhysicalWorkspaceExcessAttribution(t *testing.T) {
	for _, mode := range []string{"middle_logical", "middle_allocated", "finish_logical", "finish_allocated"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			plan := accountingTestPlan(t)
			tap := newEpochWarmWorkspaceOutput(&checkoutCommandOutput{remaining: 64 << 20, cancel: cancel}, plan, [32]byte{1})
			write := func(raw string) error { _, err := tap.Write([]byte(raw)); return err }
			if write(workspaceTestBinding(2)+workspaceTestPair(2, 2, 1, 11, 22)) != nil || tap.arm() != nil ||
				write(workspaceTestPair(2, 3, 2, 33, 44)+workspaceTestPair(2, 3, 3, 55, 66)+workspaceTestPair(2, 4, 4, 100, 200)) != nil ||
				tap.armPhysical() != nil {
				t.Fatal("supplied prefix")
			}
			logical, allocated := uint64(75), uint64(80)
			if mode == "middle_logical" {
				logical = plan.WorkEnvelope.MaximumDataLogicalBytes + 1
			}
			if mode == "middle_allocated" {
				allocated = plan.SafetyEnvelope.MaximumDataAllocatedBytes + 1
			}
			err := write(workspaceTestPair(2, 4, 5, logical, allocated))
			middleExcess := strings.HasPrefix(mode, "middle")
			if (err != nil) != middleExcess {
				t.Fatal("middle classification", err)
			}
			if !middleExcess {
				if write("WB1:2:4R:0000000000000005\n") != nil || tap.waitPhysical(ctx) != nil {
					t.Fatal("reopen readiness")
				}
				lastLogical, lastAllocated := uint64(50), uint64(300)
				if mode == "finish_logical" {
					lastLogical = plan.WorkEnvelope.MaximumDataLogicalBytes + 1
				} else {
					lastAllocated = plan.SafetyEnvelope.MaximumDataAllocatedBytes + 1
				}
				if write(workspaceTestPair(2, 4, 6, lastLogical, lastAllocated)) == nil {
					t.Fatal("actual finish excess accepted")
				}
			}
			row, unavailable, exceeded := tap.physicalSnapshot()
			if row.Attempts != 1 || row.Completed != 1 || row.Maximum.LogicalBytes != logical ||
				row.Maximum.AllocatedBytes != allocated || unavailable || exceeded != middleExcess ||
				!tap.observation.LimitExceeded || tap.observation.finish() {
				t.Fatal("point attribution or global failure lost", row, unavailable, exceeded, tap.observation)
			}
		})
	}
}
