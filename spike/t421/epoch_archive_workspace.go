package t421

import (
	"context"
	"time"

	"github.com/bmeddeb/phebs/internal/recovery"
)

type archiveWorkspacePoint uint8

const (
	archiveWorkspaceStart archiveWorkspacePoint = iota + 1
	archiveWorkspaceBackupJoined
	archiveWorkspaceRestoreJoined
)

// One parent start hold plus the complete backup failure envelope. FD7 still
// admits only BackupCheckpointMaximum child checkpoints; its allowance is not
// renewed or shared with this extra parent point.
func epochBackupMeasurementMaximum() uint32 { return 1 + recovery.BackupCheckpointMaximum() }

// These three serial points supplement the two existing removal walks. Only
// start has a live engine: its actual retired owner acknowledges HOLD before
// walking and RELEASE after resume. The other two points require native joins.
// No point here supplies archive acceptance or a phase-finish observation.
func (run *ExecutionEpochOneRun) sampleArchiveWorkspace(ctx context.Context, point archiveWorkspacePoint) error {
	flow := run.flow
	if flow.workspace == nil {
		return nil
	}
	if flow.workspaceBytes == nil {
		return ErrExecutionEpochOne
	}
	confirm := func() bool {
		if !run.archiveWorkspaceBoundary(ctx, point) {
			return false
		}
		// The next unopened producer supplies the live parent phase without
		// attaching a process: backup, restore, then restored server.
		next := uint32(10)
		switch point {
		case archiveWorkspaceBackupJoined:
			next = 11
		case archiveWorkspaceRestoreJoined:
			next = 6
		}
		view, err := flow.controller.ProducerLaunch(next)
		if err != nil || view.Phase != 12 {
			return false
		}
		run.mu.Lock()
		current := run.result
		run.mu.Unlock()
		current.Accounting, err = flow.controller.Snapshot()
		if err != nil {
			return false
		}
		current.Store, err = flow.store.Snapshot()
		if err != nil || current.Store.Store.Phase != 12 {
			return false
		}
		switch point {
		case archiveWorkspaceStart:
			// The old server's SDK is genuinely closed before the retired engine
			// guard can bind. No archive SDK has opened at this point.
			if current.Store.Opened != current.Store.TerminalEOF {
				return false
			}
			for _, producer := range current.Store.Store.Producers {
				if producer.Producer == 5 {
					return producer.Attached && producer.Closed && producer.Calls == 0 && producer.Transactions == 0
				}
			}
			return false
		case archiveWorkspaceBackupJoined:
			return epochBackupClosedPrefix(ctx, current)
		case archiveWorkspaceRestoreJoined:
			return epochRestoreClosedPrefix(ctx, current)
		default:
			return false
		}
	}
	if !confirm() {
		_ = flow.workspaceBytes.Fail()
		return ErrExecutionEpochOne
	}
	var guard func(context.Context, func(context.Context) error) error
	if point == archiveWorkspaceStart {
		if run.control == nil {
			_ = flow.workspaceBytes.Fail()
			return ErrExecutionEpochOne
		}
		guard = run.control.WithRetiredBackupMeasurement
	}
	value, err := flow.workspaceBytes.SampleGuarded(ctx, 12, guard, confirm)
	if err != nil || value.LogicalBytes > flow.plan.WorkEnvelope.MaximumDataLogicalBytes || value.AllocatedBytes > flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.archiveWorkspacePoint = point
	run.mu.Unlock()
	return nil
}

// Pre/post confirmation uses ownership locks only; no native traversal,
// protocol exchange or reducer snapshot occurs while they are held. The live
// run (start), unpublished done (backup join), or returnStarting (restore join)
// keeps the borrowed workspace alive throughout the actual walk.
func (run *ExecutionEpochOneRun) archiveWorkspaceBoundary(ctx context.Context, point archiveWorkspacePoint) bool {
	if ctx == nil || ctx.Err() != nil || run == nil || run.flow == nil {
		return false
	}
	deadline, bounded := ctx.Deadline()
	flow := run.flow
	if !bounded || flow.epochs == nil || flow.epochs.author == nil {
		return false
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	if flow.closed || flow.retained != run || author.closed || author.err != nil || author.active || author.borrowedBy != run ||
		!epochs.active || epochs.closed || epochs.err != nil || epochs.released != 4 || run.epoch.Epoch != 4 || run.err != nil || !run.backupRetired ||
		!time.Now().Before(run.phaseDeadline) || run.phaseDeadline.After(run.lifetimeDeadline) || deadline.After(run.phaseDeadline) ||
		point < archiveWorkspaceStart || point > archiveWorkspaceRestoreJoined || run.archiveWorkspacePoint+1 != point {
		return false
	}
	switch point {
	case archiveWorkspaceStart:
		return !run.stopping && run.backupUsed && !run.backupStarted && !run.backupComplete && !run.returnStarting && !run.restoreUsed
	case archiveWorkspaceBackupJoined:
		select {
		case <-run.done:
			return false // A published handoff can already be consumed elsewhere.
		default:
		}
		return run.stopping && run.backupComplete && run.backupJoined && run.backupSessionEmpty &&
			run.result.RootJoined && run.result.SessionEmpty && !run.returnStarting && !run.restoreUsed
	case archiveWorkspaceRestoreJoined:
		return run.returnStarting && run.restoreUsed && run.restoreComplete && run.restoreStarted && run.restoreJoined && run.restoreSessionEmpty &&
			run.backupComplete && run.backupJoined && run.backupSessionEmpty && run.result.RootJoined && run.result.SessionEmpty
	default:
		return false
	}
}
