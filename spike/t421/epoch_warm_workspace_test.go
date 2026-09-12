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
