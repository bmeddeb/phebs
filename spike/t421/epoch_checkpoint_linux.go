//go:build linux

package t421

import (
	"context"
	"sync/atomic"
	"time"
)

// Issued once after the actual prior run joins and the shared checkpoint path
// completes both same-phase cutovers and its held-parent sample. The proof is
// immutable across source-borrow transfer; only launch/release are one-shot.
type linuxEpochCheckpointSuccessor struct {
	coordinator           *linuxEpochCoordinator
	recipe                *linuxEpochRecipeIdentity
	prior, next           *ExecutionEpochOneRun
	config                ExecutionEpochConfig
	recovery              *epochCheckpointRecoveryInput
	priorAuthority        *AuthorityPhaseResult
	processPrior          *ProcessObservation
	lifetime              context.Context
	deadline, runDeadline time.Time
	reserved, released    atomic.Bool
}

func (adapter *linuxEpochCoordinator) checkpointSelected() bool {
	return adapter.issued() && adapter.recipe.recipe == linuxEpochCheckpointV13
}

func (adapter *linuxEpochCoordinator) checkpointReturn(ctx context.Context, run *ExecutionEpochOneRun, stale, checkpoint bool) error {
	if ctx == nil || ctx.Err() != nil || !adapter.issued() || run == nil || run.flow != adapter.core {
		return ErrExecutionEpochOne
	}
	if adapter.checkpointSelected() {
		if !stale || !checkpoint {
			return ErrExecutionEpochOne
		}
	} else if stale || checkpoint {
		return ErrExecutionEpochOne
	}
	return nil
}

func (adapter *linuxEpochCoordinator) checkpointEntry(ctx context.Context, run *ExecutionEpochOneRun, pressure, backup bool) error {
	if !adapter.checkpointSelected() || pressure || backup || adapter.checkpoint != nil || adapter.held.resources == nil || adapter.held.resources.trace == nil || !adapter.held.resources.recipeBound() || run == nil || run.flow != adapter.core || run.epoch.Epoch != 3 || adapter.check(ctx) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// Caller owns core.mu; no shared author/epoch lock is held at issuance.
func (adapter *linuxEpochCoordinator) checkpointSuccessor(ctx, lifetime context.Context, prior, next *ExecutionEpochOneRun) error {
	if lifetime == nil || lifetime.Err() != nil || adapter.checkpointEntry(ctx, prior, false, false) != nil || next == nil || next == prior || next.flow != adapter.core || adapter.core.closed || adapter.core.retained != prior || next.checkpointRecovery == nil || next.checkpointPrior == nil || next.processPrior == nil || next.pressureAllowed || next.backupAllowed || next.cancelRun == nil || next.phaseDeadline.IsZero() || !time.Now().Before(next.phaseDeadline) {
		return ErrExecutionEpochOne
	}
	deadline, bounded := lifetime.Deadline()
	if !bounded || deadline.After(next.lifetimeDeadline) {
		return ErrExecutionEpochOne
	}
	select {
	case <-prior.done:
	default:
		return ErrExecutionEpochOne
	}
	prior.mu.Lock()
	result, runErr := prior.result, prior.err
	terminal := prior.checkpointAllowed && prior.checkpointUsed && prior.terminalEntered && prior.terminalRequested && prior.retainParent
	prior.mu.Unlock()
	if runErr != nil || !terminal || epochCheckpointClosedPrefixError(ctx, result, true, 8) != nil || result.RecoverySamples.Unavailable || result.RecoverySamples.LimitExceeded {
		return ErrExecutionEpochOne
	}
	for i := 0; i <= 5; i++ {
		if result.RecoverySamples.Points[i].Attempts != 1 || result.RecoverySamples.Points[i].Completed != 1 {
			return ErrExecutionEpochOne
		}
	}
	server := adapter.held.servers[prior]
	if server == nil || server.trace == nil || server.trace.serverRun != prior || server.trace.owner != adapter.held.resources.trace || !server.trace.hardDeathIssued || !server.trace.waitReturned || server.trace.cleanupErr != nil || !server.trace.death.RootJoined || !server.trace.death.SessionEmpty || prior.command == nil || prior.command.Process == nil || server.trace.death.ProcessState != prior.command.ProcessState || !executionProcessSIGKILL(server.trace.death.WaitErr, server.trace.death.ProcessState, prior.command.Process.Pid) || !server.measurements.completed(prior, 3) {
		return ErrExecutionEpochOne
	}
	// The preceding parent sample confirms genuine current DA/SA successor
	// views. It only succeeds after both one-shot cutovers; no supplied result
	// or joinedEmpty flag can reach this source-owned issuance by itself.
	adapter.checkpoint = &linuxEpochCheckpointSuccessor{coordinator: adapter, recipe: adapter.recipe, prior: prior, next: next, config: adapter.held.epochs.epochs[3], recovery: next.checkpointRecovery, priorAuthority: next.checkpointPrior, processPrior: next.processPrior, lifetime: lifetime, deadline: deadline, runDeadline: next.lifetimeDeadline}
	return nil
}

func (adapter *linuxEpochCoordinator) checkpointEpoch(epoch uint64) bool {
	if adapter == nil {
		return false
	}
	if epoch >= 1 && epoch <= 3 {
		return true
	}
	proof := adapter.checkpoint
	if proof == nil || proof.lifetime == nil || proof.lifetime.Err() != nil || !time.Now().Before(proof.deadline) {
		return false
	}
	deadline, bounded := proof.lifetime.Deadline()
	if !bounded || deadline != proof.deadline {
		return false
	}
	return epoch == 4 && adapter.checkpointSelected() && proof != nil && proof.coordinator == adapter && proof.recipe == adapter.recipe && proof.reserved.Load() && proof.config == adapter.held.epochs.epochs[3] && proof.config == adapter.core.epochs.epochs[3]
}

func (adapter *linuxEpochCoordinator) checkpointRun(ctx context.Context, run *ExecutionEpochOneRun, epoch uint64) error {
	if ctx == nil || ctx.Err() != nil || run == nil || !adapter.issued() || run.flow != adapter.core || epoch < 1 || epoch > 4 {
		return ErrExecutionEpochOne
	}
	if epoch <= 3 {
		if epoch == 3 && adapter.checkpointSelected() != run.checkpointAllowed {
			return ErrExecutionEpochOne
		}
		return nil
	}
	proof := adapter.checkpoint
	if !adapter.checkpointEpoch(epoch) || proof.next != run || proof.prior == run || proof.lifetime == nil || proof.lifetime.Err() != nil || run.checkpointRecovery != proof.recovery || run.checkpointPrior != proof.priorAuthority || run.processPrior != proof.processPrior || run.pressureAllowed || run.backupAllowed || run.archiveInput != nil || run.lifetimeDeadline != proof.runDeadline || !time.Now().Before(proof.deadline) {
		return ErrExecutionEpochOne
	}
	return nil
}

func (adapter *linuxEpochCoordinator) checkpointLaunch(ctx context.Context, run *ExecutionEpochOneRun, epoch uint64) error {
	if adapter == nil {
		return ErrExecutionEpochOne
	}
	if epoch != 4 {
		return adapter.checkpointRun(ctx, run, epoch)
	}
	proof := adapter.checkpoint
	if !adapter.checkpointSelected() || proof == nil || proof.next != run || !proof.reserved.CompareAndSwap(false, true) {
		return ErrExecutionEpochOne
	}
	return adapter.checkpointRun(ctx, run, epoch)
}
