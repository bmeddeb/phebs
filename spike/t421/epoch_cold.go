package t421

import (
	"context"
	"time"
)

type epochOneMode uint8

const (
	epochOneStartup epochOneMode = iota + 1
	epochOneCold
	epochOneColdWarm
	epochOnePhysicalB
)

type epochOneLimits struct {
	lifetime, health, cold, physical time.Duration
	outputBytes                      int64
	controlPairs                     uint64
}

func epochOneBounds(plan Plan, mode epochOneMode) (epochOneLimits, error) {
	if mode == epochOneStartup {
		return epochOneLimits{lifetime: 20 * time.Minute, health: 5 * time.Minute, outputBytes: 1 << 20, controlPairs: 3}, nil
	}
	if mode != epochOneCold && mode != epochOneColdWarm && mode != epochOnePhysicalB || plan.Schema != PlanV3Schema {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	// The constructor already validates the private plan. Recheck the exact
	// inherited deadline identities before converting them to durations.
	deadlines := frozenPhaseDeadlines()
	if len(plan.PhaseDeadlines) != len(deadlines) || plan.PhaseDeadlines[1] != deadlines[1] ||
		plan.PhaseDeadlines[2] != deadlines[2] || plan.SafetyEnvelope.ServerHealthDeadlineMS != frozenSafetyEnvelope().ServerHealthDeadlineMS {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	cold := time.Duration(deadlines[1].DeadlineMS) * time.Millisecond
	warm := time.Duration(deadlines[2].DeadlineMS) * time.Millisecond
	pairs := uint64(8)
	if mode == epochOneColdWarm {
		pairs += 4 // ReopenOwners, DrainOwners, OpenRequests, FenceRequests.
	}
	var physical time.Duration
	if mode == epochOnePhysicalB {
		if plan.PhaseDeadlines[3] != deadlines[3] {
			return epochOneLimits{}, ErrExecutionEpochOne
		}
		physical, pairs = time.Duration(deadlines[3].DeadlineMS)*time.Millisecond, 21
	}
	return epochOneLimits{lifetime: cold + warm + physical, physical: physical, health: time.Duration(plan.SafetyEnvelope.ServerHealthDeadlineMS) * time.Millisecond,
		cold: cold, outputBytes: 64 << 20, controlPairs: pairs}, nil
}

// StartCold opts into the cold-convergence/first-handoff slice. It preserves
// Start's smaller startup-only limits. Cold starts before AuthorA, not after
// tool checks or HTTP readiness. The 64-MiB combined-output cap is a private
// diagnostic refusal ceiling, not proof of full log fit or phase-work capture.
func (flow *ExecutionEpochOne) StartCold(ctx context.Context) (*ExecutionEpochOneRun, error) {
	return flow.start(ctx, epochOneCold)
}

// StartColdWarm additionally reserves one phase-three observation window.
// It changes neither StartCold's allowance nor any frozen phase/work limit.
func (flow *ExecutionEpochOne) StartColdWarm(ctx context.Context) (*ExecutionEpochOneRun, error) {
	return flow.start(ctx, epochOneColdWarm)
}

// ObserveWarm reopens ordinary owners, takes exactly one current X and ready T,
// then drains owners for one F equal to actual cold authority. There is no soak
// or work-metrics claim. Success leaves owners/requests fenced in phase three;
// Stop cancels and joins this one-shot operation before using phase control.
func (run *ExecutionEpochOneRun) ObserveWarm(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || run.control == nil || run.stop == nil || run.done == nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || !run.warmAllowed || run.warmUsed || !run.warm ||
		run.coldDone == nil || run.inspection == nil || run.phaseDeadline.IsZero() {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.coldDone:
	default:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	default:
	}
	ctx, cancel := context.WithDeadline(ctx, run.phaseDeadline)
	done := make(chan struct{})
	run.warmUsed, run.warmCancel, run.warmDone = true, cancel, done
	inspection := run.inspection
	run.mu.Unlock()
	defer func() {
		cancel()
		if retErr != nil {
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			run.stopOnce.Do(func() { close(run.stop) })
		}
		close(done)
	}()
	if ctx.Err() != nil || inspection.beginWarm() != nil || run.control.ReopenOwners(ctx) != nil {
		return ErrExecutionEpochOne
	}
	progress, _, err := inspection.Progress(ctx)
	if err != nil || progress.Progress == nil || progress.Progress.State != "current" {
		return ErrExecutionEpochOne
	}
	tail, _, err := inspection.Tail(ctx)
	if err != nil || tail.Status != "ready" {
		return ErrExecutionEpochOne
	}
	if run.control.DrainOwners(ctx) != nil || run.control.OpenRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	if _, _, _, err := inspection.Final(ctx); err != nil {
		return ErrExecutionEpochOne
	}
	if run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// ColdToWarm performs X -> T -> drained F exactly once, then advances both
// reducers and the live server into phase three. Ordinary owners and requests
// remain fenced; no warm observation, full receipt or ceremony pass is issued.
// Stop cancels and joins this operation before touching the control endpoint.
func (run *ExecutionEpochOneRun) ColdToWarm(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || run.flow == nil || run.control == nil || run.stop == nil || run.done == nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || run.coldUsed || run.coldDeadline.IsZero() || !run.healthy || run.healthDone == nil {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.healthDone:
	default:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	default:
	}
	ctx, cancel := context.WithDeadline(ctx, run.coldDeadline)
	done := make(chan struct{})
	run.coldUsed, run.coldCancel, run.coldDone = true, cancel, done
	run.mu.Unlock()
	defer func() {
		cancel()
		if retErr != nil {
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			run.stopOnce.Do(func() { close(run.stop) })
		}
		close(done)
	}()
	inspection, err := run.newEpochInspection(ctx)
	if err != nil {
		return ErrExecutionEpochOne
	}
	for {
		value, _, err := inspection.Progress(ctx)
		if err != nil {
			return ErrExecutionEpochOne
		}
		if value.Progress != nil && value.Progress.State == "current" {
			break
		}
		if epochInspectionDelay(ctx) != nil {
			return ErrExecutionEpochOne
		}
	}
	for {
		value, _, err := inspection.Tail(ctx)
		if err != nil {
			return ErrExecutionEpochOne
		}
		if value.Status == "ready" {
			break
		}
		if epochInspectionDelay(ctx) != nil {
			return ErrExecutionEpochOne
		}
	}
	if run.control.DrainOwners(ctx) != nil || run.control.OpenRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	if _, _, _, err := inspection.Final(ctx); err != nil {
		return ErrExecutionEpochOne
	}
	// FenceRequests joins the exact request's report/commit tail; receiving
	// the body and trailer alone does not establish synchronous sink success.
	if run.control.FenceRequests(ctx) != nil || run.advanceCold(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	return run.completeCold(ctx)
}

// One phase timer covers bootstrap, health, inspection and idle time alike.
// The caller holds run.mu; cleanup joins an already-started callback outside it.
func (run *ExecutionEpochOneRun) setPhaseDeadlineLocked(deadline time.Time) {
	done := make(chan struct{})
	run.phaseDeadline, run.phaseDone = deadline, done
	run.phaseTimer = time.AfterFunc(time.Until(deadline), func() {
		defer close(done)
		run.mu.Lock()
		run.err = ErrExecutionEpochOne
		run.mu.Unlock()
		run.cancelRun()
		run.stopOnce.Do(func() { close(run.stop) })
	})
}

// Retire the cold deadline only after the coordinated handoff succeeds. A
// callback that has started wins the race, even if still waiting for run.mu.
func (run *ExecutionEpochOneRun) completeCold(ctx context.Context) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	if ctx.Err() != nil || run.stopping || run.err != nil || run.phaseTimer == nil ||
		!time.Now().Before(run.coldDeadline) || !run.phaseTimer.Stop() {
		return ErrExecutionEpochOne
	}
	close(run.phaseDone) // Stop won: no callback owns this completion.
	now := time.Now()
	if !now.Before(run.coldDeadline) {
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		return ErrExecutionEpochOne
	default:
	}
	deadline := now.Add(run.warmLimit)
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	run.warm = true
	run.setPhaseDeadlineLocked(deadline)
	return nil
}

func (run *ExecutionEpochOneRun) stopPhaseDeadline() {
	run.mu.Lock()
	timer, done := run.phaseTimer, run.phaseDone
	run.phaseTimer, run.phaseDone = nil, nil
	if timer != nil && !time.Now().Before(run.phaseDeadline) {
		run.err = ErrExecutionEpochOne
	}
	run.mu.Unlock()
	if timer != nil {
		if timer.Stop() {
			close(done)
		} else {
			<-done
		}
	}
}

func epochInspectionDelay(ctx context.Context) error {
	timer := time.NewTimer(time.Duration(correctedInspectionPollMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The caller has joined the final read and closed the observation window.
// A checkpoint carries persistent handles only, never live store calls/UUIDs.
func (run *ExecutionEpochOneRun) advanceCold(ctx context.Context) error {
	flow := run.flow
	if run.control.Pause(ctx) != nil || flow.parent.Pause(ctx) != nil ||
		flow.controller.Fence() != nil || flow.store.Fence() != nil ||
		run.control.Checkpoint(ctx) != nil || flow.parent.Checkpoint(ctx) != nil ||
		flow.controller.Advance() != nil || flow.store.Advance() != nil ||
		flow.parent.Resume(3) != nil || run.control.Resume(ctx) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}
