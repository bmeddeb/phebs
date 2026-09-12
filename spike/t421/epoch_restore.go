package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// This startup-only prerequisite reserves DrainOwners/Pause/receiver EOF, not
// future archive R/F, lifecycle collection or product-query choreography.
func restoredStartupBounds(plan Plan) (epochOneLimits, error) {
	deadlines := frozenPhaseDeadlines()
	if plan.Schema != PlanV3Schema || len(plan.PhaseDeadlines) != len(deadlines) || plan.PhaseDeadlines[11] != deadlines[11] ||
		plan.SafetyEnvelope.ServerHealthDeadlineMS != frozenSafetyEnvelope().ServerHealthDeadlineMS {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	return epochOneLimits{lifetime: time.Duration(deadlines[11].DeadlineMS) * time.Millisecond,
		health: time.Duration(plan.SafetyEnvelope.ServerHealthDeadlineMS) * time.Millisecond, outputBytes: 64 << 20, controlPairs: 3}, nil
}

// StartRestored consumes the successful joined archive handoff once and starts
// the actual protected fifth server. Health and Stop use their existing native
// paths. This startup-only lifetime ends at the original phase-twelve deadline;
// neither launch nor HTTP readiness establishes archive R/F or phase acceptance.
func (run *ExecutionEpochOneRun) StartRestored(ctx context.Context) (_ *ExecutionEpochOneRun, retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.done == nil || run.flow.epochs == nil ||
		run.flow.epochs.author == nil || run.flow.controller == nil || run.flow.store == nil || run.flow.parent == nil {
		return nil, ErrExecutionEpochOne
	}
	select {
	case <-run.done:
	default:
		return nil, ErrExecutionEpochOne
	}
	flow := run.flow
	flow.mu.Lock()
	run.mu.Lock()
	bounds, err := restoredStartupBounds(flow.plan)
	now := time.Now()
	valid := err == nil && !flow.closed && flow.retained == run && !run.returnStarting && !run.restoredStartUsed && run.err == nil && run.epoch.Epoch == 4 &&
		run.backupComplete && run.backupStarted && run.backupJoined && run.backupSessionEmpty && run.backupRetired &&
		run.restoreUsed && run.restoreComplete && run.restoreStarted && run.restoreJoined && run.restoreSessionEmpty &&
		run.result.RootJoined && run.result.SessionEmpty && run.result.BackupWork.Complete && run.result.RestoreWork.Complete &&
		validDigest(run.backupManifestSHA256) && run.restoreManifestSHA256 == run.backupManifestSHA256 &&
		now.Before(run.phaseDeadline) && !run.phaseDeadline.After(run.lifetimeDeadline) && run.phaseDeadline.Sub(now) <= bounds.lifetime
	if !valid {
		run.mu.Unlock()
		flow.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	deadline := run.phaseDeadline
	prior := run.result
	// The predecessor's finish has already canceled its own context. Borrow
	// the caller independently, while retaining the established phase clock.
	lifetime, cancel := context.WithDeadline(ctx, deadline)
	done := make(chan struct{})
	run.restoredStartUsed, run.returnStarting = true, true
	run.returnStartCancel, run.returnStartDone = cancel, done
	run.mu.Unlock()
	flow.mu.Unlock()
	defer func() {
		if retErr != nil {
			cancel()
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			run.fenceFailedBackup(cleanup)
			stop()
		}
		flow.mu.Lock()
		run.returnStarting, run.returnStartCancel = false, nil
		close(done)
		flow.mu.Unlock()
	}()
	flow.mu.Lock()
	defer flow.mu.Unlock()
	prior.Accounting, err = flow.controller.Snapshot()
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	prior.Store, err = flow.store.Snapshot()
	view, launchErr := flow.controller.ProducerLaunch(6)
	if err != nil || launchErr != nil || view.Phase != 12 || flow.closed || flow.retained != run || !epochRestoreClosedPrefix(lifetime, prior) {
		return nil, ErrExecutionEpochOne
	}
	archive, authority, err := run.restoredArchiveBinding(lifetime)
	if err != nil {
		return nil, err
	}
	// No Advance/Resume: the same parent already owns the open phase twelve.
	next := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), done: make(chan struct{}), healthLimit: bounds.health,
		coldDeadline: deadline, lifetimeDeadline: deadline, cancelRun: cancel, backupWork: prior.BackupWork,
		archiveInput: archive, archivePrior: authority,
		result: ExecutionEpochOneResult{RestoreWork: prior.RestoreWork}}
	next.setPhaseDeadlineLocked(deadline)
	result, err := flow.launchEpoch(lifetime, lifetime, cancel, next, bounds, 5)
	if result == nil {
		next.stopPhaseDeadline()
	}
	return result, err
}

// RestoreBackup consumes the joined backup handoff once. It empties only the
// held installation directory's children, preserves that root inode and runs
// the real restore recipe with the SAME epoch-four config. Failed partial
// installation/archive/source custody remains retained. No epoch-five launch,
// archive R report, restored authority or complete phase receipt is claimed.
func (run *ExecutionEpochOneRun) RestoreBackup(ctx context.Context) (result ExecutionEpochOneResult, retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.done == nil || run.flow.epochs == nil || run.flow.epochs.author == nil || run.flow.controller == nil || run.flow.store == nil || run.flow.parent == nil {
		return result, ErrExecutionEpochOne
	}
	select {
	case <-run.done:
	default:
		return result, ErrExecutionEpochOne
	}
	flow := run.flow
	flow.mu.Lock()
	run.mu.Lock()
	valid := !flow.closed && flow.retained == run && !run.returnStarting && !run.restoreUsed && run.err == nil && run.epoch.Epoch == 4 &&
		run.backupRetired && run.backupComplete && run.backupStarted && run.backupJoined && run.backupSessionEmpty && run.result.RootJoined && run.result.SessionEmpty &&
		validDigest(run.backupManifestSHA256) && run.backupOutput != nil && run.backupOutput.server != nil && time.Now().Before(run.phaseDeadline) && time.Now().Before(run.lifetimeDeadline)
	if !valid {
		run.mu.Unlock()
		flow.mu.Unlock()
		return result, ErrExecutionEpochOne
	}
	deadline := run.phaseDeadline
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	operation, cancel := context.WithDeadline(ctx, deadline)
	done := make(chan struct{})
	run.restoreUsed, run.returnStarting = true, true
	run.returnStartCancel, run.returnStartDone = cancel, done
	run.mu.Unlock()
	flow.mu.Unlock()
	defer cancel()
	defer func() {
		// The helper's native Wait/session/receiver joins precede this handoff.
		accounting, dispatchErr := flow.controller.Snapshot()
		store, storeErr := flow.store.Snapshot()
		run.mu.Lock()
		run.result.Accounting, run.result.Store = accounting, store
		if run.restoreStarted && (!run.restoreJoined || !run.restoreSessionEmpty) {
			run.result.SessionEmpty = false
		}
		if dispatchErr != nil || storeErr != nil || !run.restoreComplete || !epochRestoreClosedPrefix(operation, run.result) {
			retErr = ErrExecutionEpochOne
		}
		if retErr != nil {
			run.err = ErrExecutionEpochOne
		}
		run.mu.Unlock()
		// Snapshot/prefix failures are late failures too; leave no open work.
		if retErr != nil {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			run.fenceFailedBackup(cleanup)
			stop()
		}
		flow.mu.Lock()
		run.returnStarting, run.returnStartCancel = false, nil
		close(done)
		flow.mu.Unlock()
		result, _ = run.Wait(context.Background())
	}()
	// Establish the live phase and borrowed exact target before any removal.
	view, err := flow.controller.ProducerLaunch(11)
	if err != nil || view.Phase != 12 {
		return result, ErrExecutionEpochOne
	}
	run.mu.Lock()
	current := run.result
	run.mu.Unlock()
	current.Accounting, err = flow.controller.Snapshot()
	if err != nil {
		return result, ErrExecutionEpochOne
	}
	current.Store, err = flow.store.Snapshot()
	if err != nil || !epochBackupClosedPrefix(operation, current) {
		return result, ErrExecutionEpochOne
	}
	run.backupOutput.mu.Lock()
	if run.backupOutput.server.err != nil {
		run.backupOutput.mu.Unlock()
		return result, ErrExecutionEpochOne
	}
	run.backupOutput.restore = &checkoutCommandOutput{remaining: run.backupOutput.remaining, cancel: cancel}
	run.backupOutput.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	valid = epochs.active && author.borrowedBy == run && epochs.checkLocked(operation, 4) == nil &&
		run.epoch.DataRoot == epochs.roots[0].path && run.epoch.BackupRoot == epochs.roots[3].path && run.epoch.ConfigPath == epochs.epochs[3].ConfigPath
	held := epochs.roots[0]
	epochs.mu.Unlock()
	author.mu.Unlock()
	if !valid || run.sampleRetiredRemoval(operation) != nil {
		return result, ErrExecutionEpochOne
	}
	author.mu.Lock()
	epochs.mu.Lock()
	if epochs.active && author.borrowedBy == run && epochs.checkLocked(operation, 4) == nil && os.SameFile(held.info, epochs.roots[0].info) {
		err = emptyRetiredDataRoot(operation, held)
	} else {
		err = ErrExecutionEpochOne
	}
	epochs.mu.Unlock()
	author.mu.Unlock()
	// A failed removal still owns its required terminal sample. Cancellation
	// refuses that traversal and retains prior completed maxima; no retry or
	// per-child observation is hidden inside the recursive removal loop.
	err = errors.Join(err, run.sampleRetiredRemoval(operation))
	if err != nil {
		return result, err
	}
	if err = run.runNativeArchive(operation, true); err != nil {
		return result, err
	}
	if operation.Err() != nil {
		return result, ErrExecutionEpochOne
	}
	// Restore must keep the admitted root rather than replacing its inode.
	author.mu.Lock()
	epochs.mu.Lock()
	err = epochs.checkLocked(operation, 4)
	epochs.mu.Unlock()
	author.mu.Unlock()
	if err != nil {
		return result, ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.restoreManifestSHA256 != run.backupManifestSHA256 {
		run.mu.Unlock()
		return result, ErrExecutionEpochOne
	}
	run.restoreComplete = true
	run.mu.Unlock()
	if err := run.sampleArchiveWorkspace(operation, archiveWorkspaceRestoreJoined); err != nil {
		return result, err
	}
	return result, nil
}

// The retained restore operation, not a caller-supplied phase, owns these two
// boundary walks. returnStarting prevents source release and successor launch;
// the ordinary joined sampler intentionally refuses that active operation.
func (run *ExecutionEpochOneRun) sampleRetiredRemoval(ctx context.Context) error {
	flow := run.flow
	if flow.workspace == nil {
		return nil
	}
	if flow.workspaceBytes == nil {
		return ErrExecutionEpochOne
	}
	confirm := func() bool {
		if !run.retiredRemovalBoundary(ctx) {
			return false
		}
		run.mu.Lock()
		current := run.result
		run.mu.Unlock()
		var err error
		current.Accounting, err = flow.controller.Snapshot()
		if err != nil {
			return false
		}
		current.Store, err = flow.store.Snapshot()
		return err == nil && epochBackupClosedPrefix(ctx, current)
	}
	if !confirm() {
		_ = flow.workspaceBytes.Fail()
		return ErrExecutionEpochOne
	}
	// The confirmed SA snapshot is phase twelve; no authored zero phase row or
	// data-only substitute is supplied. The descriptor's identity is rechecked
	// by the existing native whole-workspace observer on this actual traversal.
	value, err := flow.workspaceBytes.SampleConfirmed(ctx, 12, confirm)
	if err != nil || value.LogicalBytes > flow.plan.WorkEnvelope.MaximumDataLogicalBytes || value.AllocatedBytes > flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		return ErrExecutionEpochOne
	}
	return nil
}

// Called both before and after traversal. These locks protect ownership only;
// the native walk and controller snapshots run after all have been released.
func (run *ExecutionEpochOneRun) retiredRemovalBoundary(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	flow := run.flow
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	return !flow.closed && flow.retained == run && run.returnStarting && run.restoreUsed && !run.restoreStarted && run.err == nil &&
		run.epoch.Epoch == 4 && run.backupRetired && run.backupComplete && run.backupJoined && run.backupSessionEmpty &&
		!author.closed && author.err == nil && !author.active && author.borrowedBy == run && epochs.active && !epochs.closed && epochs.err == nil && epochs.released == 4 &&
		time.Now().Before(run.phaseDeadline) && !run.phaseDeadline.After(run.lifetimeDeadline)
}

// Only the already-held data root reaches this helper. os.Root confines all
// recursive removal to that directory, including raced symlinks. One name is
// retained at a time; each subtree's stdlib removal is cooperative only at its
// boundary, not an invented hard syscall deadline. The root itself is never
// removed. Failures retain whatever native prefix was actually removed.
func emptyRetiredDataRoot(ctx context.Context, held productionRoot) (retErr error) {
	if ctx == nil || ctx.Err() != nil || held.file == nil || held.info == nil || held.path == "" || held.path == string(filepath.Separator) || !filepath.IsAbs(held.path) {
		return ErrExecutionEpochOne
	}
	check := func() error {
		if ctx.Err() != nil {
			return ErrExecutionEpochOne
		}
		info, e := held.file.Stat()
		current, pathErr := os.Lstat(held.path)
		volume, volumeErr := inputCustodyVolume(held.file)
		canonical, canonicalErr := filepath.EvalSymlinks(held.path)
		if e != nil || pathErr != nil || volumeErr != nil || canonicalErr != nil || canonical != held.path || volume != held.volume || !os.SameFile(held.info, info) || !os.SameFile(info, current) || !current.IsDir() || !inputCustodyOwned(current) || current.Mode().Perm() != 0o700 {
			return ErrExecutionEpochOne
		}
		return nil
	}
	if check() != nil {
		return ErrExecutionEpochOne
	}
	root, err := os.OpenRoot(held.path)
	if err != nil {
		return ErrExecutionEpochOne
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(held.info, info) || check() != nil {
		return ErrExecutionEpochOne
	}
	entries, err := root.Open(".")
	if err != nil {
		return ErrExecutionEpochOne
	}
	defer func() { retErr = errors.Join(retErr, entries.Close()) }()
	for {
		if check() != nil {
			return ErrExecutionEpochOne
		}
		names, readErr := entries.Readdirnames(1)
		if readErr != nil && readErr != io.EOF {
			return ErrExecutionEpochOne
		}
		if len(names) == 0 {
			if readErr != io.EOF {
				return ErrExecutionEpochOne
			}
			break
		}
		if names[0] == "." || names[0] == ".." || filepath.Base(names[0]) != names[0] || root.RemoveAll(names[0]) != nil {
			return ErrExecutionEpochOne
		}
	}
	if entries.Sync() != nil || check() != nil {
		return ErrExecutionEpochOne
	}
	// A fresh cursor proves no child remained or appeared during the sweep.
	verify, err := root.Open(".")
	if err != nil {
		return ErrExecutionEpochOne
	}
	names, readErr := verify.Readdirnames(1)
	closeErr := verify.Close()
	if len(names) != 0 || readErr != io.EOF || closeErr != nil || check() != nil {
		return ErrExecutionEpochOne
	}
	return nil
}
