//go:build linux

package t421

import (
	"context"
	"errors"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

type linuxEpochHardDeathRequest struct {
	ctx   context.Context
	reply chan error
}

func (root *linuxEpochTraceRoot) requestHardDeath(ctx context.Context) error {
	if root == nil || ctx == nil || ctx.Err() != nil || root.hardRequests == nil {
		return ErrExecutionEpochOne
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrExecutionEpochOne
	}
	request := &linuxEpochHardDeathRequest{ctx: ctx, reply: make(chan error, 1)}
	select {
	case root.hardRequests <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-root.engineDone:
		return ErrExecutionEpochOne
	}
	// Transfer owns a real actor decision, even after request cancellation.
	return <-request.reply
}

func (root *linuxEpochTraceRoot) receiveHardDeath(ctx context.Context) error {
	select {
	case request := <-root.hardRequests:
		err := root.killOriginalRoot(ctx, request.ctx)
		request.reply <- err
		return err
	default:
		return nil
	}
}

// Only the creating actor calls this with probeGate held. Runtime entry is
// additionally bound to the exact checkpoint run by the native finish adapter.
func (root *linuxEpochTraceRoot) killOriginalRoot(ctx, operation context.Context) error {
	if ctx == nil || operation == nil || ctx.Err() != nil || operation.Err() != nil || !root.gateOwned || root.hardDeathIssued || root.owner == nil || root.owner.hardRoot != nil || root.err != nil || root.unknown || root.stopping || root.graceful || root.engineLease != nil {
		return ErrExecutionEpochOne
	}
	if root.serverRun != nil && root.serverRun.engineMeasurementFinished != nil {
		select {
		case <-root.serverRun.engineMeasurementFinished:
		default:
			return ErrExecutionEpochOne // Accepted measurement owners still own this actor.
		}
	}
	leader := root.owner.leaders[root.original.pid]
	if root.liveLeader(ctx, leader) != nil || operation.Err() != nil {
		return ErrExecutionEpochOne
	}
	if err := unix.PidfdSendSignal(leader.fd, unix.SIGKILL, nil, 0); err != nil {
		return err
	}
	root.hardDeathIssued, leader.killSent = true, true
	root.owner.hardRoot = root
	root.stopping = true
	return nil
}

func (root *linuxEpochTraceRoot) hardDeathAnchor(ctx context.Context) error {
	if root == nil || ctx == nil || ctx.Err() != nil || !root.hardDeathIssued || root.owner == nil || root.owner.hardRoot != root {
		return ErrExecutionEpochOne
	}
	leader := root.owner.leaders[root.original.pid]
	if leader == nil || leader.root != root || leader.fd < 0 || leader.fd != root.pidfd {
		return ErrExecutionEpochOne
	}
	if root.hardAnchorProven {
		mask, err := linuxEpochTracePoll(ctx, leader.fd)
		if err != nil || mask != unix.POLLIN {
			return errors.Join(ErrExecutionEpochOne, err)
		}
		return nil // A proven terminal lifetime cannot resume before this original fd's HUP.
	}
	_, err := root.terminalProof(ctx, leader)
	if err == nil {
		root.hardAnchorProven = true
	}
	return err
}

// Creation parent remains immutable. A selected source-issued root death may
// change only the separately observed current parent of an already-owned task.
func (root *linuxEpochTraceRoot) sameOwnedLifetime(ctx context.Context, original, current linuxEpochTraceIdentity) bool {
	if original.sameLifetime(current) {
		return true
	}
	if original.pid == root.original.pid || current.parent <= 0 || original.pid != current.pid || original.tgid != current.tgid || original.tracer != current.tracer || original.sid != current.sid || original.birth != current.birth || original.namespace == nil || current.namespace == nil || !os.SameFile(original.namespace, current.namespace) || root.hardDeathAnchor(ctx) != nil {
		return false
	}
	task := root.tasks[original.pid]
	return task != nil && task.original.sameLifetime(original)
}

// probeGate is held by the whole meter. Session membership supplies candidates
// only: every candidate needs its exact source-created leader and current
// birth/parent/SID/namespace/tracer proof before it can enter the RSS sum.
func (owner *linuxEpochTraceOwner) observeWholeCohort(ctx context.Context, controller int) ([]t4013.NativeProcessRecord, map[t4013.NativeProcessRecord]bool, error) {
	selected := owner.hardRoot
	if selected == nil {
		rows, err := t4013.ObserveProcessTreeRecords(ctx, controller)
		return rows, nil, err
	}
	if controller != owner.held.controllerPID || selected.owner != owner || !selected.hardDeathIssued {
		return nil, nil, ErrExecutionEpochOne
	}
	// The signal may still be completing. Current registered parent identity is
	// strict until the same original root actually reaches terminal CLD_*.
	rows, candidates, err := t4013.ObserveLinuxProcessSessionCohortRecords(ctx, controller, selected.original.sid)
	if err != nil {
		return nil, nil, err
	}
	byPID := make(map[int]t4013.NativeProcessRecord, len(rows))
	for _, row := range rows {
		byPID[row.PID] = row
	}
	proofs := make(map[t4013.NativeProcessRecord]bool, len(candidates))
	for _, pid := range candidates {
		leader := owner.leaders[pid]
		row, found := byPID[pid]
		if !found || leader == nil || leader.root != selected || !leader.verified || leader.fd < 0 || row.StartIdentity != leader.original.birth {
			return nil, nil, ErrExecutionEpochOne
		}
		fresh, e := linuxEpochTraceIdentityOf(pid)
		if e != nil || fresh.parent != row.ParentPID || fresh.name != row.ObservedName || !selected.sameOwnedLifetime(ctx, leader.original, fresh) {
			return nil, nil, errors.Join(ErrExecutionEpochOne, e)
		}
		proofs[row] = true
	}
	for pid, leader := range owner.leaders {
		if leader.root == selected {
			if _, found := byPID[pid]; !found {
				return nil, nil, ErrExecutionEpochOne // No vanished leader grants retirement.
			}
		}
	}
	return rows, proofs, ctx.Err()
}
