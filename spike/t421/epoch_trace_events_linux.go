//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
	"unsafe"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// Every wait is for a currently registered original TID. The actor continues
// servicing stops while it owns the sample gate across a terminal-copy/HUP turn.
func (root *linuxEpochTraceRoot) serve() {
	ticker := time.NewTicker(linuxEpochTraceFallback)
	defer ticker.Stop()
	for {
		select {
		case <-root.stop:
			root.stopping = true
		default:
		}
		select {
		case <-root.grace:
			root.graceful = true
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), epochProcessProbeTimeout)
		if err := root.acquireGate(ctx); err != nil {
			// No kernel wait/CONT occurred. Keep any older pending/exit token
			// and all loans; later bounded cleanup can still service the root.
			root.refuse("probe-gate-acquire", err)
			root.stopping = true
		} else {
			if err := root.receiveHardDeath(ctx); err != nil {
				root.refuse("hard-death-request", err)
				root.stopping = true
			}
			if err := root.receiveEngineMeasurement(ctx); err != nil {
				root.refuse("engine-measurement-request", err)
				root.stopping = true
			}
			terminal, err := root.turn(ctx)
			if err != nil {
				root.refuse("event-turn", err)
				root.unknown = true
				root.stopping = true
			}
			if err = root.retireReaped(ctx); err != nil {
				root.refuse("parent-reap", err)
				root.unknown = true
			}
			if err = root.reconcileExitingGroups(ctx); err != nil {
				root.refuse("group-reconciliation", err)
				root.unknown = true
			}
			if err = root.progressEngineMeasurement(ctx); err != nil {
				root.refuse("engine-measurement-progress", err)
				root.stopping = true
			}
			if err = root.signalLeaves(ctx); err != nil {
				root.refuse("owned-stop", err)
				root.unknown = true
			}
			if terminal && !root.unknown && len(root.pending) == 0 && len(root.tasks) == 1 {
				if !root.observerJoined {
					empty, sessionErr := root.sessionEmpty(ctx)
					if sessionErr != nil {
						root.refuse("anchored-session", sessionErr)
						root.unknown = true
					}
					if empty && !root.unknown {
						cancel()
						root.releaseGate()
						// No meter mutex under probeGate: its pending Sample
						// may own that mutex while waiting for this gate.
						<-root.observerDone
						root.observerJoined = true
						// A fresh normal turn queues for the gate and performs the
						// final proof within that turn's existing two-second budget.
						continue
					}
				}
				if root.observerJoined {
					if root.serverRun != nil && root.serverRun.engineMeasurementFinished != nil {
						select {
						case <-root.serverRun.engineMeasurementFinished:
						default:
							root.releaseGate()
							cancel()
							select {
							case <-root.wake:
							case <-ticker.C:
							}
							continue // Endpoint cleanup may still need this actor, never root.done.
						}
					}
					empty, e := root.sessionEmpty(ctx)
					if empty && e == nil {
						cancel()
						root.waitRoot(false)
						return
					}
					root.refuse("final-session", e)
					root.unknown = true
				}
			}
			// Once a tracer terminal copy has made the application parent's native
			// reap possible, no sample may enter until that original pidfd reaches HUP.
			if len(root.pending) == 0 && !root.holdExitingGroupGate() {
				root.releaseGate()
			}
		}
		cancel()
		select {
		case <-root.wake:
		case <-ticker.C:
		}
	}
}

func (root *linuxEpochTraceRoot) turn(ctx context.Context) (bool, error) {
	root.currentTID = 0 // Detached retirement can refuse before a task is selected.
	if !root.gateOwned {
		return false, fmt.Errorf("turn/gate: %w", ErrExecutionEpochOne)
	}
	ids := make([]int, 0, len(root.tasks))
	for tid := range root.tasks {
		ids = append(ids, tid)
	}
	sort.Ints(ids)
	terminalRoot := false
	if err := root.retireDetached(ctx); err != nil {
		return false, fmt.Errorf("turn/detached: %w", err)
	}
	for _, tid := range ids {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		root.currentTID = tid
		task := root.tasks[tid]
		if task == nil {
			continue
		}
		stop, err := linuxEpochTraceWait(tid, unix.WSTOPPED)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		fallback := errors.Is(err, unix.ECHILD)
		if err != nil && !fallback {
			return false, fmt.Errorf("turn/wait-stop tid=%d: %w", tid, err)
		}
		if stop.signo != 0 {
			if stop.code != 4 || stop.pid != int32(tid) || stop.uid != uint32(os.Getuid()) {
				return false, fmt.Errorf("turn/stop-record tid=%d code=%d pid=%d: %w", tid, stop.code, stop.pid, ErrExecutionEpochOne)
			}
			if err = root.stopped(ctx, tid, task, stop); err != nil {
				return false, fmt.Errorf("turn/stopped tid=%d status=%d: %w", tid, stop.status, err)
			}
			continue
		}

		terminal, err := linuxEpochTraceWait(tid, unix.WEXITED|unix.WNOWAIT)
		if errors.Is(err, unix.EINTR) && !fallback {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("turn/wait-terminal tid=%d: %w", tid, err)
		}
		if terminal.signo == 0 || terminal.code == 4 {
			if fallback {
				fresh, e := linuxEpochTraceIdentityOf(tid)
				if e != nil || !root.sameOwnedLifetime(ctx, task.original, fresh) {
					return false, fmt.Errorf("turn/fallback-lifetime tid=%d: %w", tid, errors.Join(ErrExecutionEpochOne, e))
				}
				if fresh.state == "Z" && task.leader != nil {
					root.markExiting(task.leader)
				}
				if terminal.signo != 0 && (terminal.signo != int32(unix.SIGCHLD) || terminal.pid != int32(tid) || terminal.uid != uint32(os.Getuid())) {
					return false, fmt.Errorf("turn/fallback-record tid=%d info=%+v: %w", tid, terminal, ErrExecutionEpochOne)
				}
			}
			continue
		}
		if !terminal.terminal(tid) {
			return false, fmt.Errorf("turn/terminal-record tid=%d info=%+v: %w", tid, terminal, ErrExecutionEpochOne)
		}
		fresh, err := linuxEpochTraceIdentityOf(tid)
		if err != nil || !root.sameOwnedLifetime(ctx, task.original, fresh) {
			return false, fmt.Errorf("turn/terminal-identity tid=%d fresh=%+v: %w", tid, fresh, errors.Join(ErrExecutionEpochOne, err))
		}
		if tid == root.original.pid {
			if _, err = root.terminalProof(ctx, task.leader); err != nil {
				return false, err
			}
			root.retireDetachedGroup(tid)
			task.leader.exiting = false
			terminalRoot = true
			if !root.exitSeen {
				root.exitSeen = true
				close(root.exited)
			}
			continue // WNOWAIT is not retirement; sole genuine Handle.Wait remains.
		}
		if fresh.pid == fresh.tgid {
			if task.leader == nil || task.leader.original.parent == root.owner.held.controllerPID {
				return false, fmt.Errorf("turn/leader-binding tid=%d leader-present=%t: %w", tid, task.leader != nil, ErrExecutionEpochOne)
			}
			if _, err = root.terminalProof(ctx, task.leader); err != nil {
				return false, err
			}
			if err = root.liveParent(ctx, task.leader); err != nil {
				return false, fmt.Errorf("turn/parent-live tid=%d: %w", tid, err)
			}
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		consumed, err := linuxEpochTraceWait(tid, unix.WEXITED)
		if err != nil || consumed != terminal {
			return false, fmt.Errorf("turn/terminal-consume tid=%d peek=%+v consumed=%+v: %w", tid, terminal, consumed, errors.Join(ErrExecutionEpochOne, err))
		}
		// Commit the actual bookkeeping immediately. A later failed proof must not
		// target a status that has already been consumed.
		root.removeTask(tid)
		if task.leader != nil {
			root.retireDetachedGroup(tid)
			task.leader.exiting = false
			root.pending[tid] = task.leader
		}
	}
	return terminalRoot, nil
}

// The stop has been consumed by this creating actor. Mechanical kernel tests
// use the same dispatcher while withholding one known CLONE notification.
func (root *linuxEpochTraceRoot) stopped(ctx context.Context, tid int, task *linuxEpochTraceTask, stop linuxEpochTraceSiginfo) error {
	event, signal := int(stop.status)>>8, int(stop.status)&255
	if err := root.record(tid, task.original.tgid, event, task.original.birth); err != nil {
		return err
	}
	if task.reconciled {
		return root.continueReconciled(ctx, tid, task, event, signal)
	}
	if event != 0 {
		return root.event(ctx, tid, task, event)
	}
	if task.initial && signal == int(unix.SIGSTOP) {
		if err := root.allowedImage(ctx, tid, nil); err != nil {
			return err
		}
		task.initial = false
		return unix.PtraceCont(tid, 0)
	}
	task.initial = false
	var information unix.Siginfo
	_, _, errno := unix.Syscall6(unix.SYS_PTRACE, unix.PTRACE_GETSIGINFO, uintptr(tid), 0, uintptr(unsafe.Pointer(&information)), 0, 0)
	if errno == unix.EINVAL && signal == int(unix.SIGSTOP) && root.engineLease != nil && task.original.tgid == root.engineLease.leader.original.pid {
		fresh, err := linuxEpochTraceIdentityOf(tid)
		if task.engineHeld || err != nil || ctx.Err() != nil || !root.sameOwnedLifetime(ctx, task.original, fresh) || fresh.state != "t" {
			return fmt.Errorf("stop/engine-group-witness tid=%d held=%t state=%s: %w", tid, task.engineHeld, fresh.state, errors.Join(ErrExecutionEpochOne, err, ctx.Err()))
		}
		if root.engineLease.aborted {
			return unix.PtraceCont(tid, 0) // Owned kill already sent; no measurement may commit.
		}
		task.engineHeld = true
		return nil // Classic group-stop is held until our owned SIGCONT + CONT.
	}
	if errno != 0 || information.Signo != int32(signal) {
		return fmt.Errorf("stop/siginfo tid=%d signal=%d errno=%d signo=%d initial=%t lease=%t held=%t: %w", tid, signal, errno, information.Signo, task.initial, root.engineLease != nil, task.engineHeld, ErrExecutionEpochOne)
	}
	// Genuine application/runtime delivery stops are forwarded, including
	// SIGTRAP. An auto-child SIGSTOP is suppressed only in its initial slot.
	return unix.PtraceCont(tid, signal)
}

func (root *linuxEpochTraceRoot) event(ctx context.Context, tid int, task *linuxEpochTraceTask, event int) error {
	fresh, err := linuxEpochTraceIdentityOf(tid)
	if err != nil || !root.sameOwnedLifetime(ctx, task.original, fresh) {
		return fmt.Errorf("event/identity tid=%d event=%d fresh=%+v: %w", tid, event, fresh, errors.Join(ErrExecutionEpochOne, err))
	}
	switch event {
	case unix.PTRACE_EVENT_FORK, unix.PTRACE_EVENT_VFORK, unix.PTRACE_EVENT_CLONE:
		child, e := unix.PtraceGetEventMsg(tid)
		if e != nil || child == 0 || child > uint(^uint(0)>>1) {
			return fmt.Errorf("event/creation-message creator=%d child=%d: %w", tid, child, errors.Join(ErrExecutionEpochOne, e))
		}
		if e = root.register(ctx, int(child), task); e != nil {
			return fmt.Errorf("event/creation-register creator=%d child=%d: %w", tid, child, e)
		}
	case unix.PTRACE_EVENT_EXEC:
		former, e := unix.PtraceGetEventMsg(tid)
		if e != nil || int(former) != tid || tid != fresh.tgid {
			return ErrExecutionEpochOne
		}
		if task.leader == nil {
			return ErrExecutionEpochOne
		}
		task.leader.verified = false
		if e = root.allowedImage(ctx, tid, nil); e != nil {
			return e
		}
		task.leader.verified = true
	case unix.PTRACE_EVENT_VFORK_DONE:
		// A child may already be retired/reaped by the time the parent reports
		// VFORK_DONE. The event grants no new PID or terminal authority.
		if _, e := unix.PtraceGetEventMsg(tid); e != nil {
			return e
		}
	case unix.PTRACE_EVENT_EXIT:
		if fresh.pid == fresh.tgid {
			root.markExiting(task.leader)
			// Keep the real parent stopped before exit_mm until already-reaped
			// children have their original HUP/live-parent proof recorded.
			if e := root.retireReaped(ctx); e != nil {
				return e
			}
			for _, child := range root.pending {
				if child.original.parent == tid {
					return ErrExecutionEpochOne
				}
			}
		}
		if fresh.pid != fresh.tgid {
			// Resource rows are process leaders. No all-TID EXIT-image claim is made.
			// Group-terminal pidfd/CLD proof is still required for the retained leader.
			if e := unix.PtraceDetach(tid); e != nil {
				return e
			}
			root.rememberDetached(tid, fresh)
			return nil
		}
	default:
		return ErrExecutionEpochOne
	}
	if err := unix.PtraceCont(tid, 0); err != nil {
		return fmt.Errorf("event/continue tid=%d event=%d: %w", tid, event, err)
	}
	return nil
}

func (root *linuxEpochTraceRoot) register(ctx context.Context, tid int, creator *linuxEpochTraceTask) error {
	if !root.gateOwned || root.tasks[tid] != nil || root.owner.leaders[tid] != nil {
		return fmt.Errorf("register/existing tid=%d gate=%t task=%t leader=%t: %w", tid, root.gateOwned, root.tasks[tid] != nil, root.owner.leaders[tid] != nil, ErrExecutionEpochOne)
	}
	fresh, err := linuxEpochTraceIdentityOf(tid)
	if err != nil || fresh.tracer != root.tid || fresh.namespace == nil || !os.SameFile(fresh.namespace, root.owner.held.pidNamespace) || fresh.sid != root.original.sid {
		return fmt.Errorf("register/identity tid=%d tgid=%d parent=%d tracer=%d expected-tracer=%d sid=%d expected-sid=%d state=%s: %w", tid, fresh.tgid, fresh.parent, fresh.tracer, root.tid, fresh.sid, root.original.sid, fresh.state, errors.Join(ErrExecutionEpochOne, err))
	}
	root.owner.mu.Lock()
	full := root.owner.tids >= linuxEpochTraceMaxTIDs
	root.owner.mu.Unlock()
	if full {
		return fmt.Errorf("register/TID-cap tid=%d: %w", tid, ErrExecutionEpochOne)
	}
	var leader *linuxEpochTraceLeader
	if fresh.pid == fresh.tgid {
		parent := root.owner.leaders[fresh.parent]
		if fresh.parent != creator.original.tgid || parent == nil || parent.root != root || len(root.owner.leaders) >= linuxEpochTraceMaxLeaders {
			return fmt.Errorf("register/leader-parent tid=%d parent=%d creator-group=%d parent-known=%t: %w", tid, fresh.parent, creator.original.tgid, parent != nil, ErrExecutionEpochOne)
		}
		if err = root.liveLeader(ctx, parent); err != nil {
			return err
		}
		fd, e := unix.PidfdOpen(tid, 0)
		if e != nil {
			return e
		}
		// Publish only after both inherited actual image and original relationship
		// checks succeed. On uncertainty the fd and task stay retained by the actor.
		leader = &linuxEpochTraceLeader{root: root, original: fresh, fd: fd, parentBirth: parent.original.birth}
	} else {
		group := root.owner.leaders[fresh.tgid]
		if group == nil || group.root != root {
			return fmt.Errorf("register/thread-group tid=%d tgid=%d known=%t: %w", tid, fresh.tgid, group != nil, ErrExecutionEpochOne)
		}
	}
	root.tasks[tid] = &linuxEpochTraceTask{original: fresh, initial: true, leader: leader}
	root.owner.mu.Lock()
	root.owner.tids++
	root.owner.mu.Unlock()
	if leader != nil {
		root.owner.leaders[tid] = leader
	}
	// This check occurs before either the creator or auto-stopped child can
	// continue. Unknown inherited images are refusals, never a dead-row waiver.
	err = root.allowedImage(ctx, tid, nil)
	if err == nil && leader != nil {
		leader.verified = true
	}
	return err
}

func (root *linuxEpochTraceRoot) removeTask(tid int) {
	if root.tasks[tid] == nil {
		return
	}
	delete(root.tasks, tid)
	root.owner.mu.Lock()
	root.owner.tids--
	root.owner.mu.Unlock()
}
func (root *linuxEpochTraceRoot) liveLeader(ctx context.Context, leader *linuxEpochTraceLeader) error {
	if ctx == nil {
		return ErrExecutionEpochOne
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if leader == nil || leader.root != root || leader.fd < 0 {
		return ErrExecutionEpochOne
	}
	before, err := linuxEpochTraceIdentityOf(leader.original.pid)
	if err != nil || !root.sameOwnedLifetime(ctx, leader.original, before) || before.state == "Z" {
		return fmt.Errorf("leader/live-before pid=%d fresh=%+v: %w", leader.original.pid, before, errors.Join(ErrExecutionEpochOne, err))
	}
	mask, err := linuxEpochTracePoll(ctx, leader.fd)
	if err != nil || mask != 0 {
		return fmt.Errorf("leader/live-pidfd mask=%d: %w", mask, errors.Join(ErrExecutionEpochOne, err))
	}
	after, err := linuxEpochTraceIdentityOf(leader.original.pid)
	if err != nil || !before.sameLifetime(after) || after.state == "Z" {
		return fmt.Errorf("leader/live-after pid=%d state=%s: %w", leader.original.pid, after.state, errors.Join(ErrExecutionEpochOne, err))
	}
	return ctx.Err()
}
func (root *linuxEpochTraceRoot) liveParent(ctx context.Context, leader *linuxEpochTraceLeader) error {
	if leader != nil && leader.root == root && root.owner.leaders[leader.original.pid] == leader && leader.original.pid != root.original.pid && root.hardDeathIssued {
		return root.hardDeathAnchor(ctx)
	}
	parent := root.owner.leaders[leader.original.parent]
	if parent == nil || parent.original.birth != leader.parentBirth {
		return ErrExecutionEpochOne
	}
	return root.liveLeader(ctx, parent)
}
func (root *linuxEpochTraceRoot) retireReaped(ctx context.Context) error {
	for pid, leader := range root.pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if leader.fd < 0 {
			return ErrExecutionEpochOne
		}
		if err := root.liveParent(ctx, leader); err != nil {
			return fmt.Errorf("retire/parent-before: %w", err)
		}
		mask, err := linuxEpochTracePoll(ctx, leader.fd)
		if err != nil {
			return err
		}
		if mask&unix.POLLHUP == 0 {
			continue
		}
		if err = root.liveParent(ctx, leader); err != nil {
			return fmt.Errorf("retire/parent-after: %w", err)
		}
		if err = leader.closeFD(); err != nil {
			return err
		}
		delete(root.owner.leaders, pid)
		delete(root.pending, pid)
	}
	return nil
}

func (leader *linuxEpochTraceLeader) closeFD() error {
	if leader == nil || leader.fd < 0 {
		return ErrExecutionEpochOne
	}
	fd := leader.fd
	// A failed Linux close may release the slot; never reuse its integer.
	leader.fd = -1
	return unix.Close(fd)
}

func (root *linuxEpochTraceRoot) signalLeaves(ctx context.Context) error {
	if !root.stopping && !root.graceful {
		return nil
	}
	if root.hardDeathIssued && root.hardDeathAnchor(ctx) != nil {
		return nil // The selected root must actually die before survivor signals.
	}
	for pid, leader := range root.owner.leaders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if leader.root != root || root.pending[pid] != nil || leader.exiting || pid == root.original.pid && root.exitSeen {
			continue
		}
		root.currentTID = pid
		if !root.stopping {
			// Graceful server shutdown must reach the root while persistent
			// Surreal is still live: root shutdown owns its real child Wait.
			if pid != root.original.pid {
				continue
			}
		} else {
			child := false
			for _, other := range root.owner.leaders {
				if other.root == root && other.original.parent == pid {
					child = true
					break
				}
			}
			if child {
				continue
			} // Failure escalation preserves real-parent Waits.
		}
		signal := unix.SIGKILL
		if !root.stopping {
			signal = unix.SIGTERM
		}
		// A successful original-fd signal deliberately makes live proof
		// transient/unavailable. Already sent means no further probe/signal;
		// the separate terminal/parent/session/Wait path still owns closure.
		if signal == unix.SIGTERM && leader.termSent || signal == unix.SIGKILL && leader.killSent {
			continue
		}
		mask, err := linuxEpochTracePoll(ctx, leader.fd)
		if err != nil {
			return fmt.Errorf("stop/pidfd-poll pid=%d: %w", pid, err)
		}
		if mask != 0 {
			continue
		}
		if err = root.liveLeader(ctx, leader); err != nil {
			return fmt.Errorf("stop/live-leader pid=%d: %w", pid, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = unix.PidfdSendSignal(leader.fd, signal, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("stop/pidfd-signal pid=%d signal=%d: %w", pid, signal, err)
		}
		if signal == unix.SIGTERM {
			leader.termSent = true
		} else {
			leader.killSent = true
		}
	}
	return nil
}

func (root *linuxEpochTraceRoot) sessionEmpty(ctx context.Context) (bool, error) {
	leader := root.owner.leaders[root.original.pid]
	if _, err := root.terminalProof(ctx, leader); err != nil {
		return false, err
	}
	pids, err := t4013.ObserveLinuxPrivateProcessSessionPIDs(root.original.pid)
	if err != nil {
		return false, err
	}
	if len(pids) != 0 {
		return false, ErrExecutionEpochOne
	}
	if err = root.finalTracerCensus(ctx); err != nil {
		return false, err
	}
	_, err = root.terminalProof(ctx, leader)
	return err == nil, err
}

// The original direct root is never raw-reaped. A separate fixed watcher
// releases the census gate at actual native reap; it does not publish DA/pump
// settlement. The creating actor stays locked and owns the genuine Handle.Wait.
func (root *linuxEpochTraceRoot) waitRoot(initial bool) {
	if !root.gateOwned || len(root.pending) != 0 || initial && root.continued || !initial && len(root.tasks) != 1 {
		root.refuse("wait-shape")
		// A shape refusal is not terminal ownership. Preserve actor and gate.
		for {
			time.Sleep(linuxEpochTraceFallback)
		}
	}
	root.gateOwned = false // Transfer this one gate token to the joined watcher.
	released := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(linuxEpochTraceFallback)
		defer ticker.Stop()
		for {
			mask, err := linuxEpochTracePollOnce(root.pidfd)
			if err != nil {
				// Refusal never transfers/retires the original root or gate.
				// This watcher stays joined to the still-owned genuine Wait.
				if !errors.Is(err, unix.EINTR) {
					root.owner.admission.failDetail("trace/root-reap-poll")
				}
				// EINTR proves nothing; a fresh real attempt follows only
				// the existing cleanup tick, with gate/pidfd/loans retained.
				<-ticker.C
				continue
			}
			if mask&unix.POLLHUP != 0 {
				delete(root.owner.leaders, root.command.Process.Pid)
				if root.owner.hardRoot == root {
					root.owner.hardRoot = nil
				}
				<-root.owner.admission.probeGate
				released <- nil
				return
			}
			<-ticker.C
		}
	}()
	waitErr := root.handle.Wait()
	root.waitReturned = true
	root.death.RootJoined = true
	root.death.ProcessState = root.command.ProcessState
	root.death.WaitErr = waitErr
	// Even a failed native/DA Wait is a real returned Wait, never a zero status.
	reapErr := <-released
	<-root.observerDone
	root.removeTask(root.command.Process.Pid)
	if root.stopCallback != nil && !root.stopCallback() {
		<-root.callbackDone
	}
	closeErr := unix.Close(root.pidfd)
	if closeErr != nil {
		root.owner.admission.failDetail("trace/root-pidfd-close")
		// Descriptor custody is unknown; actual native Wait remains recorded,
		// but the actor/root stays retained rather than releasing whole loans.
		for {
			time.Sleep(linuxEpochTraceFallback)
		}
	}
	root.pidfd = -1
	// The ordinary path proved the original session empty before Wait. Initial
	// capture cleanup never continued application code or admitted descendants.
	root.death.SessionEmpty = true
	root.cleanupErr = errors.Join(root.err, reapErr, closeErr)
	root.completionErr = errors.Join(root.cleanupErr, waitErr)
}
