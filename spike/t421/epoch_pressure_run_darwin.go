//go:build darwin

package t421

import (
	"context"
	"fmt"
	"time"
)

// Pressure executes only the fixed 80/90/75 sequence on the actual recovered
// endpoint. The zero ballast inode must already belong to preparation custody.
// It returns before the separate backup/retirement operation, never calls Wait
// while finish owns this operation's join, and never retries a native mutation.
func (run *ExecutionEpochOneRun) Pressure(ctx context.Context, volume *executionPressureVolume) (retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || volume == nil || run.flow == nil {
		return ErrExecutionEpochOne
	}
	volume.mu.Lock()
	ballast := volume.ballast
	valid := volume.flow == run.flow && volume.ready && volume.borrowed && volume.check() == nil && ballast != nil &&
		ballast.volume == volume && ballast.file != nil && ballast.next == 0 && !ballast.failed && !ballast.removed
	volume.mu.Unlock()
	if !valid {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || !run.pressureAllowed || run.pressureUsed || run.epoch.Epoch != 4 || !run.healthy || !run.warm ||
		run.checkpointDone == nil || run.inspection == nil || run.control == nil || run.control.RequestToken() != "" || !time.Now().Before(run.phaseDeadline) {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.checkpointDone:
	default:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	op, cancel := context.WithDeadline(ctx, run.lifetimeDeadline)
	done := make(chan struct{})
	run.pressureUsed, run.pressureCancel, run.pressureDone = true, cancel, done
	run.mu.Unlock()
	defer func() {
		cancel()
		if retErr != nil {
			run.inspection.mu.Lock()
			run.inspection.pressure.samples.Complete = false
			if !run.inspection.pressure.samples.LimitExceeded {
				run.inspection.pressure.samples.Unavailable = true
			}
			run.inspection.mu.Unlock()
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			run.stopOnce.Do(func() { close(run.stop) })
		}
		close(done)
	}()
	for phase := uint32(9); phase <= 11; phase++ {
		if err := run.pressurePhase(op, ballast, phase); err != nil {
			return err
		}
	}
	run.inspection.mu.Lock()
	run.inspection.pressure.samples.Complete = run.inspection.pressure.sampleOrdinal == 11
	complete := run.inspection.pressure.samples.Complete
	run.inspection.mu.Unlock()
	if !complete || op.Err() != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

func (run *ExecutionEpochOneRun) pressurePhase(ctx context.Context, ballast *executionPressureBallast, phase uint32) error {
	reader := run.inspection
	reader.mu.Lock()
	priorSamples := []uint8{0, 4, 7}[phase-9]
	valid := reader.err == nil && reader.finalUsed && reader.pressureBaseline != nil && reader.pressure.sampleOrdinal == priorSamples &&
		len(reader.evidence.rows) > 0 && reader.evidence.rows[len(reader.evidence.rows)-1].SelectorAccepted
	reader.mu.Unlock()
	if !valid || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || run.phaseTimer == nil || !time.Now().Before(run.phaseDeadline) || !run.phaseTimer.Stop() {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	close(run.phaseDone)
	deadline := time.Now().Add(time.Duration(run.flow.plan.PhaseDeadlines[phase-1].DeadlineMS) * time.Millisecond)
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	run.setPhaseDeadlineLocked(deadline) // All handoff I/O belongs to the new phase.
	run.mu.Unlock()
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if run.advanceReturnPhase(ctx, phase) != nil || reader.beginPressure(phase) != nil || run.control.OpenRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	workspace, err := reader.pressureSample(ctx, "start")
	if err != nil {
		return ErrExecutionEpochOne
	}
	if phase == 9 {
		if reader.pressureCommand(ctx, "drive-normal", time.Time{}) != nil || reader.pressureRead(ctx, "normal-cycle", time.Time{}) != nil {
			return ErrExecutionEpochOne
		}
		workspace, err = reader.pressureSample(ctx, "normalized")
		limits := run.flow.plan.SafetyEnvelope
		if err != nil || workspace.AllocatedBytes < limits.MinimumPrePressureBytes || workspace.AllocatedBytes > limits.MaximumPrePressureBytes {
			return ErrExecutionEpochOne
		}
	}
	if run.control.FenceRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	mutation, err := ballast.nextTarget(ctx, run, workspace)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrExecutionEpochOne, err)
	}
	if run.control.OpenRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	if _, err := reader.pressureSample(ctx, "ballast"); err != nil {
		return ErrExecutionEpochOne
	}
	operation := []string{"pressure-80", "pressure-90", "pressure-75"}[phase-9]
	if reader.pressureRead(ctx, operation, mutation.Fence) != nil {
		return ErrExecutionEpochOne
	}
	if phase == 11 {
		if run.control.FenceRequests(ctx) != nil {
			return ErrExecutionEpochOne
		}
		removed, err := ballast.remove(ctx, run)
		if err != nil || run.control.OpenRequests(ctx) != nil {
			return ErrExecutionEpochOne
		}
		if _, err := reader.pressureSample(ctx, "removed"); err != nil {
			return ErrExecutionEpochOne
		}
		if reader.pressureCommand(ctx, "drive-recovery", removed.Fence) != nil || reader.pressureRead(ctx, "recovery-cycle", time.Time{}) != nil ||
			reader.pressureRead(ctx, "recovered-normal", time.Time{}) != nil {
			return ErrExecutionEpochOne
		}
	}
	if phase != 10 {
		if _, _, err := reader.LifecycleStatus(ctx); err != nil {
			return ErrExecutionEpochOne
		}
	}
	// The pressure inventory permits one X and one T, not convergence retries.
	progress, _, err := reader.Progress(ctx)
	if err != nil || progress.Progress == nil || progress.Progress.State != "current" {
		return ErrExecutionEpochOne
	}
	tail, _, err := reader.Tail(ctx)
	if err != nil || tail.Status != "ready" {
		return ErrExecutionEpochOne
	}
	if _, _, _, err := reader.Final(ctx); err != nil {
		return ErrExecutionEpochOne
	}
	if _, err := reader.pressureSample(ctx, "finish"); err != nil {
		return ErrExecutionEpochOne
	}
	if run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	return reader.acceptInspectionPhase(ctx)
}
