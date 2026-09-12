package t421

import (
	"context"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

// Fixed operation-bound points: stale start/pre-response/finish; checkpoint
// start/pre-response; joined hard-death before restart; recovered finish.
// These are not observations inside the five-second lease callbacks, atomic
// filesystem peaks, or proof of every transient preparation/restart mutation.
type ExecutionRecoveryWorkspaceSamples struct {
	Points                     [7]ExecutionWorkspaceBytePhase
	Unavailable, LimitExceeded bool
}

type epochRecoveryWorkspaceSample struct {
	LogicalBytes   uint64 `json:"logical_bytes"`
	AllocatedBytes uint64 `json:"allocated_bytes"`
}

func (run *ExecutionEpochOneRun) recoveryWorkspaceSelected() bool {
	return run != nil && run.flow != nil && run.flow.workspace != nil &&
		(run.epoch.Epoch == 3 && run.staleAllowed || run.epoch.Epoch == 4)
}

func (samples *ExecutionRecoveryWorkspaceSamples) record(plan Plan, point uint8, value custodybytes.Sample) error {
	if point > 6 || samples.Unavailable || samples.LimitExceeded || samples.Points[point].Attempts != 1 || samples.Points[point].Completed != 0 {
		return errEpochInspection
	}
	row := &samples.Points[point]
	row.Completed = 1
	row.Maximum = value
	if value.LogicalBytes > plan.WorkEnvelope.MaximumDataLogicalBytes || value.AllocatedBytes > plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		samples.LimitExceeded = true
		return errEpochInspection
	}
	return nil
}

// Caller already owns reader.mu in prepareRecovery. A missing response object
// is never bound-zero. Successful S remains in joined output if this response
// or its later sink/arming tail fails.
func (reader *executionEpochInspection) recordRecoveryPreparationWorkspace(value *epochRecoveryWorkspaceSample, checkpoint bool) (retErr error) {
	if !reader.run.recoveryWorkspaceSelected() {
		if value != nil {
			return errEpochInspection
		}
		return nil
	}
	defer func() {
		if retErr != nil && !reader.recoverySamples.LimitExceeded {
			reader.recoverySamples.Unavailable = true
		}
	}()
	point := uint8(1)
	if checkpoint {
		point = 4
	}
	samples := &reader.recoverySamples
	if value == nil || samples.Unavailable || samples.LimitExceeded || samples.Points[point].Attempts != 0 ||
		samples.Points[point-1].Attempts != 1 || samples.Points[point-1].Completed != 1 {
		return errEpochInspection
	}
	samples.Points[point].Attempts = 1
	return samples.record(reader.plan, point, custodybytes.Sample{LogicalBytes: value.LogicalBytes, AllocatedBytes: value.AllocatedBytes})
}

func (reader *executionEpochInspection) sampleRecoveryWorkspace(ctx context.Context, point uint8) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.run == nil || reader.run.flow == nil {
		return errEpochInspection
	}
	if !reader.run.recoveryWorkspaceSelected() {
		return nil
	}
	defer func() {
		reader.fail(retErr)
		if retErr != nil && !reader.recoverySamples.LimitExceeded {
			reader.recoverySamples.Unavailable = true
		}
	}()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.plan.Schema != PlanV3Schema ||
		reader.run.control == nil || point != 0 && point != 2 && point != 3 && point != 6 ||
		reader.recoverySamples.Unavailable || reader.recoverySamples.LimitExceeded || reader.recoverySamples.Points[point].Attempts != 0 {
		return errEpochInspection
	}
	phase, epoch, final := "stale_lease", uint64(3), point == 2 || point == 6
	if point >= 3 {
		phase = "process_restart"
	}
	if point == 6 {
		epoch = 4
	}
	if reader.run.epoch.Epoch != epoch || reader.projection.Phase != phase || reader.finalUsed != final ||
		point == 0 && reader.stalePrepared || point == 3 && reader.checkpointPrepared {
		return errEpochInspection
	}
	for i := uint8(0); i < point; i++ {
		if reader.recoverySamples.Points[i].Attempts != 1 || reader.recoverySamples.Points[i].Completed != 1 {
			return errEpochInspection
		}
	}
	name := "start"
	if final {
		name = "finish"
	}
	reader.recoverySamples.Points[point].Attempts = 1
	value, err := reader.readWorkspaceSample(ctx, name)
	if err != nil {
		return err
	}
	if err := reader.recoverySamples.record(reader.plan, point, value); err != nil {
		return err
	}
	return ctx.Err()
}

// Caller retains flow.mu, just like the existing midphase parent walk. This
// excludes a successor/release through the walk. Author/epoch/run locks are
// held only during confirmation; no engine exists after the actual native join.
func (run *ExecutionEpochOneRun) sampleRecoveryParentLocked(ctx context.Context) error {
	flow := run.flow
	if flow.workspace == nil {
		return nil
	}
	refuse := func() error {
		if flow.workspaceBytes != nil {
			_ = flow.workspaceBytes.Fail()
		}
		run.mu.Lock()
		run.result.RecoverySamples.Unavailable = true
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	if flow.workspaceBytes == nil || flow.epochs == nil || flow.epochs.author == nil ||
		flow.controller == nil || flow.store == nil || flow.plan.Schema != PlanV3Schema {
		return refuse()
	}
	confirm := func() bool {
		if !run.recoveryParentBoundaryLocked(ctx) {
			return false
		}
		view, daErr := flow.controller.ProducerLaunch(5)
		state, saErr := flow.store.Snapshot()
		current, countErr := flow.controller.ProducerCount(4)
		if daErr != nil || saErr != nil || countErr != nil || view.Phase != 8 ||
			!current.Attached || !current.Closed || current.Active != 0 || current.Checkpoint != 8 ||
			state.Store.Phase != 8 || state.Opened != 3 || state.TerminalEOF != 3 {
			return false
		}
		prior, next := false, false
		for _, p := range state.Store.Producers {
			if p.Producer == 4 {
				prior = p.Attached && !p.Closed && p.Calls == 0 && p.Transactions == 0 &&
					p.TerminalFencedEOF && p.TerminalPhase == 8 && p.Checkpoint == 8
			}
			if p.Producer == 5 {
				next = !p.Attached && !p.Closed && p.Calls == 0 && p.Transactions == 0
			}
		}
		return prior && next
	}
	if !confirm() {
		return refuse()
	}
	run.mu.Lock()
	run.result.RecoverySamples.Points[5].Attempts = 1
	run.mu.Unlock()
	value, err := flow.workspaceBytes.SampleConfirmed(ctx, 8, confirm)
	if err != nil {
		return refuse()
	}
	run.mu.Lock()
	err = run.result.RecoverySamples.record(flow.plan, 5, value)
	run.mu.Unlock()
	if err != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// Structural ownership only; actual DA/SA and native session checks remain
// in the operation above and the preceding production finish. No test-supplied
// row alone can bypass those authorities.
func (run *ExecutionEpochOneRun) recoveryParentBoundaryLocked(ctx context.Context) bool {
	if run == nil || run.flow == nil || run.flow.epochs == nil || run.flow.epochs.author == nil ||
		ctx == nil || ctx.Err() != nil {
		return false
	}
	flow := run.flow
	deadline, bounded := ctx.Deadline()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	run.mu.Lock()
	rows := run.result.RecoverySamples
	valid := bounded && !flow.closed && flow.retained == run && run.epoch.Epoch == 3 &&
		!author.active && !author.closed && author.err == nil && author.next == 3 && author.borrowedBy == run &&
		epochs.active && !epochs.closed && epochs.err == nil && epochs.released == 3 &&
		run.err == nil && run.result.RootStarted && run.result.RootJoined && run.result.SessionEmpty &&
		run.checkpointAllowed && run.terminalEntered && run.terminalRequested && run.returnStarting &&
		!run.phaseDeadline.IsZero() && !deadline.After(run.phaseDeadline) &&
		!rows.Unavailable && !rows.LimitExceeded && rows.Points[5].Completed == 0
	for i := 0; i < 5; i++ {
		valid = valid && rows.Points[i].Attempts == 1 && rows.Points[i].Completed == 1
	}
	run.mu.Unlock()
	epochs.mu.Unlock()
	author.mu.Unlock()
	return valid
}

func (run *ExecutionEpochOneRun) recoveryWorkspacePrefixSnapshot() ExecutionRecoveryWorkspaceSamples {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.result.RecoverySamples
}

// Reconcile each real response against its own phase-local joined S payload.
// Epoch-four carries the accepted predecessor prefix by value, not by re-reading
// its output, and cannot turn a missing response into successful preparation.
func recoveryWorkspaceJoinedPrefix(producer uint32, stream ExecutionWorkspaceByteObservation, samples ExecutionRecoveryWorkspaceSamples, checkpoint bool) bool {
	return stream.Bound && stream.Complete && !stream.Unavailable && !stream.LimitExceeded &&
		recoveryWorkspacePointValuesMatch(producer, stream, samples, checkpoint)
}

// A later pressure failure must not relabel already completed phase-seven/eight
// values. Global joined-stream failure is retained separately by the caller.
func recoveryWorkspacePointValuesMatch(producer uint32, stream ExecutionWorkspaceByteObservation, samples ExecutionRecoveryWorkspaceSamples, checkpoint bool) bool {
	if !stream.Bound || samples.Unavailable || samples.LimitExceeded {
		return false
	}
	first, last := 0, 3
	if producer == 4 && checkpoint {
		last = 5
	} else if producer == 5 {
		first, last = 6, 7
	} else if producer != 4 {
		return false
	}
	var phases [2]ExecutionWorkspaceBytePhase
	for i := 0; i < last; i++ {
		row := samples.Points[i]
		if row.Attempts != 1 || row.Completed != 1 {
			return false
		}
		if i < first {
			continue
		}
		slot, phase := i, 0
		if i >= 3 {
			phase = 1
		}
		if i == 6 {
			slot = 5
		}
		if stream.recoverySamples[slot] != row.Maximum {
			return false
		}
		aggregate := &phases[phase]
		aggregate.Attempts++
		aggregate.Completed++
		aggregate.Maximum.LogicalBytes = max(aggregate.Maximum.LogicalBytes, row.Maximum.LogicalBytes)
		aggregate.Maximum.AllocatedBytes = max(aggregate.Maximum.AllocatedBytes, row.Maximum.AllocatedBytes)
	}
	for i := last; i < len(samples.Points); i++ {
		if samples.Points[i] != (ExecutionWorkspaceBytePhase{}) {
			return false
		}
	}
	if producer == 4 {
		return stream.Phases[6] == phases[0] && stream.Phases[7] == phases[1]
	}
	return stream.Phases[7] == phases[1]
}
