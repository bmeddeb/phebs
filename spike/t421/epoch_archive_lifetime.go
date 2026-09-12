package t421

import (
	"context"
	"time"
)

// Reserve the already-frozen archive phase before the fourth server starts.
// Backup cannot renew a lifetime after that server has consumed it.
func checkpointBackupEpochBounds(plan Plan) (epochOneLimits, error) {
	bounds, err := checkpointPressureEpochBounds(plan)
	deadlines := frozenPhaseDeadlines()
	if err != nil || plan.PhaseDeadlines[11] != deadlines[11] {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	bounds.lifetime += time.Duration(deadlines[11].DeadlineMS) * time.Millisecond
	return bounds, nil
}

func restoredExecutionBounds(plan Plan) (epochOneLimits, error) {
	bounds, err := restoredStartupBounds(plan)
	if err != nil {
		return epochOneLimits{}, err
	}
	deadlines := frozenPhaseDeadlines()
	for _, i := range []int{12, 13} {
		if plan.PhaseDeadlines[i] != deadlines[i] {
			return epochOneLimits{}, ErrExecutionEpochOne
		}
		bounds.lifetime += time.Duration(deadlines[i].DeadlineMS) * time.Millisecond
	}
	// Initial Drain; three Open/Fence windows; two Pause/Checkpoint/Resume
	// handoffs; final Pause; receiver idle/EOF. HTTP reads add no PC pairs.
	bounds.controlPairs = 1 + 3*2 + 2*3 + 1 + 1
	return bounds, nil
}

// Bound a newly owned server lifetime by the genuine caller and the original
// author-start clock. This never grants another phase or refreshes a deadline.
func archiveLifetimeDeadline(ctx context.Context, plan Plan, authorStarted, deadline time.Time) (time.Time, error) {
	now := time.Now()
	if ctx == nil || ctx.Err() != nil || plan.Schema != PlanV3Schema || authorStarted.IsZero() || authorStarted.After(now) ||
		plan.SafetyEnvelope.MaximumTotalWallMS != frozenSafetyEnvelope().MaximumTotalWallMS {
		return time.Time{}, ErrExecutionEpochOne
	}
	global := authorStarted.Add(time.Duration(plan.SafetyEnvelope.MaximumTotalWallMS) * time.Millisecond)
	if global.Before(deadline) {
		deadline = global
	}
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	if !now.Before(deadline) {
		return time.Time{}, ErrExecutionEpochOne
	}
	return deadline, nil
}
