//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

type linuxEpochEngineResult struct {
	identity dispatchadmission.EngineMeasurementIdentity
	err      error
}
type linuxEpochEngineRequest struct {
	ctx     context.Context
	release bool
	reply   chan linuxEpochEngineResult
}
type linuxEpochEngineLease struct {
	leader                           *linuxEpochTraceLeader
	hold, release                    *linuxEpochEngineRequest
	identity                         dispatchadmission.EngineMeasurementIdentity
	acknowledged, continued, aborted bool
}

// The endpoint never waits for root.done. Its request is joined by the creating
// actor, which continues serving waits and releases probeGate during the walk.
func (root *linuxEpochTraceRoot) engineMeasurementRequest(ctx context.Context, release bool) (dispatchadmission.EngineMeasurementIdentity, error) {
	var zero dispatchadmission.EngineMeasurementIdentity
	if root == nil || ctx == nil || root.engineRequests == nil || root.engineDone == nil {
		return zero, ErrExecutionEpochOne
	}
	if _, bounded := ctx.Deadline(); !bounded || ctx.Err() != nil {
		return zero, errors.Join(ErrExecutionEpochOne, ctx.Err())
	}
	request := &linuxEpochEngineRequest{ctx: ctx, release: release, reply: make(chan linuxEpochEngineResult, 1)}
	select {
	case root.engineRequests <- request:
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-root.engineDone:
		return zero, ErrExecutionEpochOne
	}
	// After transfer, retain the caller until the actor resolves actual ownership.
	select {
	case result := <-request.reply:
		return result.identity, result.err
	case <-root.engineDone:
		return zero, ErrExecutionEpochOne
	}
}

func (root *linuxEpochTraceRoot) withEngineMeasurement(ctx context.Context, measure func(context.Context, dispatchadmission.EngineMeasurementIdentity) error) (retErr error) {
	identity, err := root.engineMeasurementRequest(ctx, false)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, err := root.engineMeasurementRequest(cleanup, true)
		retErr = errors.Join(retErr, err, ctx.Err())
	}()
	return measure(ctx, identity)
}

func (root *linuxEpochTraceRoot) selectMeasurementEngine(ctx context.Context) (*linuxEpochTraceLeader, error) {
	var selected *linuxEpochTraceLeader
	for _, leader := range root.owner.leaders {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if leader.root != root || leader.original.parent != root.original.pid {
			continue
		}
		if !leader.verified || leader.exiting || leader.fd < 0 || root.pending[leader.original.pid] != nil || root.liveLeader(ctx, leader) != nil || root.liveParent(ctx, leader) != nil {
			return nil, ErrExecutionEpochOne
		}
		// A current held executable, never a name or a supplied PID, selects the engine.
		if err := root.allowedImage(ctx, leader.original.pid, nil); err != nil {
			return nil, err
		}
		actual, err := os.Open("/proc/" + strconv.Itoa(leader.original.pid) + "/exe")
		if err != nil {
			return nil, err
		}
		info, statErr := actual.Stat()
		closeErr := actual.Close()
		expected, expectedErr := root.owner.held.images[3].Stat()
		if statErr != nil || closeErr != nil || expectedErr != nil {
			return nil, errors.Join(ErrExecutionEpochOne, statErr, closeErr, expectedErr)
		}
		if !os.SameFile(info, expected) {
			continue
		}
		if selected != nil {
			return nil, ErrExecutionEpochOne
		}
		selected = leader
	}
	if selected == nil {
		return nil, ErrExecutionEpochOne
	}
	return selected, nil
}

func (root *linuxEpochTraceRoot) receiveEngineMeasurement(ctx context.Context) error {
	select {
	case request := <-root.engineRequests:
		if request.release {
			if root.engineLease == nil || root.engineLease.release != nil || !root.engineLease.acknowledged {
				request.reply <- linuxEpochEngineResult{err: ErrExecutionEpochOne}
				return ErrExecutionEpochOne
			}
			root.engineLease.release = request
			return nil
		}
		if root.engineLease != nil || root.stopping || root.graceful || request.ctx.Err() != nil || root.err != nil {
			request.reply <- linuxEpochEngineResult{err: ErrExecutionEpochOne}
			return ErrExecutionEpochOne
		}
		leader, err := root.selectMeasurementEngine(ctx)
		if err != nil {
			request.reply <- linuxEpochEngineResult{err: err}
			return err
		}
		birth, err := strconv.ParseUint(leader.original.birth, 10, 64)
		if err != nil || birth == 0 {
			request.reply <- linuxEpochEngineResult{err: ErrExecutionEpochOne}
			return ErrExecutionEpochOne
		}
		lease := &linuxEpochEngineLease{leader: leader, hold: request, identity: dispatchadmission.EngineMeasurementIdentity{PID: uint32(leader.original.pid), Birth: birth, Tracer: uint32(root.tid)}}
		root.engineLease = lease
		if err = unix.PidfdSendSignal(leader.fd, unix.SIGSTOP, nil, 0); err != nil {
			return root.abortEngineMeasurement(ctx, err)
		}
	default:
	}
	return nil
}

// Every current member must already have source-owned creation history. Two
// equal bounded censuses bracket original identity, held-mm and real stop facts.
func (root *linuxEpochTraceRoot) engineGroupState(ctx context.Context, paused bool) (bool, error) {
	lease := root.engineLease
	if lease == nil || lease.leader == nil || !root.gateOwned {
		return false, fmt.Errorf("measurement/no-held-gate: %w", ErrExecutionEpochOne)
	}
	leader := lease.leader
	if root.liveLeader(ctx, leader) != nil || root.liveParent(ctx, leader) != nil || leader.exiting {
		return false, fmt.Errorf("measurement/leader-parent-live: %w", ErrExecutionEpochOne)
	}
	before, err := linuxEpochTraceGroupEntries(leader.original.pid, linuxEpochTraceMaxTIDs)
	if err != nil {
		return false, err
	}
	slices.Sort(before)
	ready := true
	for _, tid := range before {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		task := root.tasks[tid]
		if task == nil || task.initial {
			return false, nil // Only a later actual creation/initial-stop event may resolve this member.
		}
		if task.reconciled || task.original.tgid != leader.original.pid || task.original.parent != root.original.pid || task.original.tracer != root.tid || task.original.sid != root.original.sid {
			return false, fmt.Errorf("measurement/task-binding tid=%d: %w", tid, ErrExecutionEpochOne)
		}
		fresh, e := linuxEpochTraceIdentityOf(tid)
		if e != nil || !task.original.sameLifetime(fresh) {
			return false, fmt.Errorf("measurement/task-lifetime tid=%d: %w", tid, errors.Join(ErrExecutionEpochOne, e))
		}
		if fresh.state != "R" && fresh.state != "S" && fresh.state != "D" && fresh.state != "I" && fresh.state != "T" && fresh.state != "t" {
			return false, fmt.Errorf("measurement/task-state tid=%d state=%s: %w", tid, fresh.state, ErrExecutionEpochOne)
		}
		if err = root.allowedImage(ctx, tid, root.owner.held.images[3]); err != nil {
			return false, fmt.Errorf("measurement/task-image tid=%d: %w", tid, err)
		}
		if paused {
			ready = ready && task.engineHeld && fresh.state == "t"
		} else {
			ready = ready && !task.engineHeld && fresh.state != "t" && fresh.state != "T" && fresh.state != "Z" && fresh.state != "X"
		}
	}
	for tid, task := range root.tasks {
		if task.original.tgid == leader.original.pid {
			if _, found := slices.BinarySearch(before, tid); !found {
				return false, nil
			}
		}
	}
	after, err := linuxEpochTraceGroupEntries(leader.original.pid, linuxEpochTraceMaxTIDs)
	slices.Sort(after)
	if err != nil || root.liveLeader(ctx, leader) != nil || root.liveParent(ctx, leader) != nil {
		return false, fmt.Errorf("measurement/group-after: %w", errors.Join(ErrExecutionEpochOne, err))
	}
	if !slices.Equal(before, after) {
		return false, nil
	} // No proof until one complete unchanged current union.
	return ready, ctx.Err()
}

// A failed incomplete stop cannot claim resume or custody closure. The original
// pidfd kill leaves genuine engine/root Wait and every loan with the actor.
func (root *linuxEpochTraceRoot) abortEngineMeasurement(ctx context.Context, cause error) (retErr error) {
	lease := root.engineLease
	if lease == nil {
		return cause
	}
	defer func() {
		if lease.hold != nil {
			lease.hold.reply <- linuxEpochEngineResult{err: retErr}
			lease.hold = nil
		}
		if lease.release != nil {
			lease.release.reply <- linuxEpochEngineResult{err: retErr}
			lease.release = nil
		}
		root.stopping = true
	}()
	if !lease.aborted {
		lease.aborted = true
		if err := root.liveLeader(ctx, lease.leader); err != nil {
			return errors.Join(cause, err)
		}
		if err := unix.PidfdSendSignal(lease.leader.fd, unix.SIGKILL, nil, 0); err != nil {
			return errors.Join(cause, err)
		}
		lease.leader.killSent = true
		for _, task := range root.tasks {
			if task.original.tgid == lease.leader.original.pid {
				task.engineHeld = false
			}
		}
	}
	return cause
}

func (root *linuxEpochTraceRoot) progressEngineMeasurement(ctx context.Context) error {
	lease := root.engineLease
	if lease == nil {
		return nil
	}
	if lease.aborted {
		if lease.release != nil {
			lease.release.reply <- linuxEpochEngineResult{err: ErrExecutionEpochOne}
			lease.release = nil
		}
		if lease.leader.fd < 0 {
			root.engineLease = nil
		}
		return nil
	}
	if root.stopping || root.graceful || root.err != nil {
		return root.abortEngineMeasurement(ctx, ErrExecutionEpochOne)
	}
	if !lease.acknowledged {
		if err := lease.hold.ctx.Err(); err != nil {
			return root.abortEngineMeasurement(ctx, err)
		}
		ready, err := root.engineGroupState(ctx, true)
		if err != nil {
			return root.abortEngineMeasurement(ctx, err)
		}
		if ready {
			lease.acknowledged = true
			lease.hold.reply <- linuxEpochEngineResult{identity: lease.identity}
			lease.hold = nil
		}
		return nil
	}
	if lease.release == nil {
		return nil
	}
	if err := lease.release.ctx.Err(); err != nil {
		return root.abortEngineMeasurement(ctx, err)
	}
	if !lease.continued {
		ready, err := root.engineGroupState(ctx, true)
		if err != nil || !ready {
			return root.abortEngineMeasurement(ctx, errors.Join(ErrExecutionEpochOne, err))
		}
		if err = unix.PidfdSendSignal(lease.leader.fd, unix.SIGCONT, nil, 0); err != nil {
			return root.abortEngineMeasurement(ctx, err)
		}
		// SIGCONT clears the kernel job stop; CONT releases only our retained stops.
		for tid, task := range root.tasks {
			if task.original.tgid != lease.leader.original.pid || !task.engineHeld {
				continue
			}
			if err = unix.PtraceCont(tid, 0); err != nil {
				return root.abortEngineMeasurement(ctx, err)
			}
			task.engineHeld = false
		}
		lease.continued = true
	}
	ready, err := root.engineGroupState(ctx, false)
	if err != nil {
		return root.abortEngineMeasurement(ctx, err)
	}
	if ready {
		lease.release.reply <- linuxEpochEngineResult{identity: lease.identity}
		root.engineLease = nil
	}
	return nil
}
