package t421

import (
	"context"
	"time"
)

// Only the two actually implemented finish positions. In particular, these
// rows do not claim phase-three start or all early-phase byte coverage.
type ExecutionEarlyFinishSamples struct {
	Phases                     [2]ExecutionWorkspaceBytePhase
	Unavailable, LimitExceeded bool
}

// A later physical/teardown failure cannot revoke earlier completed HTTP
// observations. Overall run and joined-stream validity remain separate.
func (samples *ExecutionEarlyFinishSamples) failIncomplete() {
	if !samples.LimitExceeded && (samples.Phases[0].Completed != 1 || samples.Phases[1].Completed != 1) {
		samples.Unavailable = true
	}
}

// Caller holds flow.mu, as AuthorA already does over its actual author/join.
// The walk retains that lock (never volume.mu); inner author/epoch locks only
// protect the pre/post checks. No engine exists at either boundary.
func (flow *ExecutionEpochOne) sampleAuthorWorkspaceLocked(ctx context.Context, point uint8) error {
	fail := func() error {
		if flow.workspaceBytes != nil {
			_ = flow.workspaceBytes.Fail()
		}
		if flow.controller != nil {
			_ = flow.controller.Fence()
		}
		return ErrExecutionEpochOne
	}
	if ctx == nil || ctx.Err() != nil || flow.workspace == nil || flow.workspaceBytes == nil ||
		flow.epochs == nil || flow.epochs.author == nil || flow.controller == nil || flow.store == nil ||
		flow.plan.Schema != PlanV3Schema || len(flow.plan.PhaseDeadlines) != 15 || point > 1 {
		return fail()
	}
	confirm := func() bool {
		deadline, bounded := ctx.Deadline()
		if !bounded || ctx.Err() != nil || flow.authorStarted.IsZero() ||
			deadline.After(flow.authorStarted.Add(time.Duration(flow.plan.PhaseDeadlines[1].DeadlineMS)*time.Millisecond)) ||
			flow.closed || flow.used || flow.authorBytePoint != point || flow.authored != (point == 1) {
			return false
		}
		author, epochs := flow.epochs.author, flow.epochs
		author.mu.Lock()
		epochs.mu.Lock()
		valid := !author.active && !author.closed && author.err == nil && author.borrowedBy == nil &&
			author.next == int(point) && !epochs.active && !epochs.closed && epochs.err == nil && epochs.released == 0
		epochs.mu.Unlock()
		author.mu.Unlock()
		view, err := flow.controller.ProducerLaunch(2)
		state, storeErr := flow.store.Snapshot()
		return valid && err == nil && view.Phase == 2 && storeErr == nil &&
			state.Store.Phase == 2 && state.Opened == 0 && state.TerminalEOF == 0
	}
	if !confirm() {
		return fail()
	}
	value, err := flow.workspaceBytes.SampleConfirmed(ctx, 2, confirm)
	if err != nil {
		return fail()
	}
	flow.authorBytePoint++ // Actual completed observation survives a later excess.
	if value.LogicalBytes > flow.plan.WorkEnvelope.MaximumDataLogicalBytes ||
		value.AllocatedBytes > flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		_ = flow.controller.Fence()
		return ErrExecutionEpochOne
	}
	return nil
}

// Cold samples after F and selector cleanup; warm after F. Both retain the
// existing drained request window and its final synchronous FenceRequests.
func (reader *executionEpochInspection) sampleEarlyFinish(ctx context.Context) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.run == nil || reader.run.flow == nil {
		return errEpochInspection
	}
	run := reader.run
	if run.flow.workspace == nil || run.epoch.Epoch != 1 || !run.physicalAllowed {
		return nil // Legacy shorter profiles have no FD6 or new sample request.
	}
	defer func() {
		reader.fail(retErr)
		if retErr != nil && !reader.earlyFinishSamples.LimitExceeded {
			reader.earlyFinishSamples.Unavailable = true
		}
	}()
	phase := 0
	if reader.projection.Phase == "warm_noop" {
		phase = 1
	} else if reader.projection.Phase != "cold" {
		return errEpochInspection
	}
	row := &reader.earlyFinishSamples.Phases[phase]
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.plan.Schema != PlanV3Schema || !reader.finalUsed || run.control == nil ||
		run.flow.authorBytePoint != 2 || row.Attempts != 0 ||
		phase == 1 && reader.earlyFinishSamples.Phases[0].Completed != 1 ||
		phase == 0 && reader.plan.SelectorHandoffCleanup != nil && reader.selectorCleanupPhase != "cold" {
		return errEpochInspection
	}
	row.Attempts++
	value, err := reader.readWorkspaceSample(ctx, "finish")
	if err != nil {
		return err
	}
	row.Completed++
	row.Maximum = value
	if value.LogicalBytes > reader.plan.WorkEnvelope.MaximumDataLogicalBytes ||
		value.AllocatedBytes > reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		reader.earlyFinishSamples.LimitExceeded = true
		return errEpochInspection
	}
	return ctx.Err()
}

func earlyWorkspaceFinishPrefix(stream ExecutionWorkspaceByteObservation, samples ExecutionEarlyFinishSamples) bool {
	if !stream.Bound || !stream.Complete || stream.Unavailable || stream.LimitExceeded || samples.Unavailable || samples.LimitExceeded {
		return false
	}
	for index, sample := range samples.Phases {
		actual := stream.Phases[index+1]
		if sample.Attempts != 1 || sample.Completed != 1 || actual != sample {
			return false
		}
	}
	return true
}
