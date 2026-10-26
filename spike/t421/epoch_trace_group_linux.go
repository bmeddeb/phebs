//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A leader's EXIT stop is not group completion. Keep the census gate until
// actual CLD_* proves the group terminal: a Z leader can still have live mm
// siblings, and the native leader row alone would report zero RSS for them.
func (root *linuxEpochTraceRoot) markExiting(leader *linuxEpochTraceLeader) {
	if leader != nil && !leader.exiting {
		leader.exiting = true
	}
}

func (root *linuxEpochTraceRoot) holdExitingGroupGate() bool {
	for _, leader := range root.owner.leaders {
		if leader.root == root && leader.exiting {
			return true
		}
	}
	return false
}

// The original leader is still unreaped and pinned. This grants only a bounded
// current-thread-group cleanup inspection, never authority over a new leader,
// recovered creation history, or acceptance of a partial-group zero-RSS row.
func (root *linuxEpochTraceRoot) exitingGroupAnchor(ctx context.Context, leader *linuxEpochTraceLeader) (linuxEpochTraceIdentity, bool, error) {
	var zero linuxEpochTraceIdentity
	if ctx == nil || ctx.Err() != nil || !root.gateOwned || leader == nil || leader.root != root || !leader.exiting || !leader.verified || leader.fd < 0 || root.owner.leaders[leader.original.pid] != leader || leader.original.pid != leader.original.tgid {
		return zero, false, ErrExecutionEpochOne
	}
	before, err := linuxEpochTraceIdentityOf(leader.original.pid)
	if err != nil || !root.sameOwnedLifetime(ctx, leader.original, before) || before.tracer != root.tid || before.sid != root.original.sid || before.namespace == nil || !os.SameFile(before.namespace, root.owner.held.pidNamespace) {
		return zero, false, errors.Join(ErrExecutionEpochOne, err)
	}
	mask, err := linuxEpochTracePoll(ctx, leader.fd)
	if err != nil || mask&unix.POLLHUP != 0 {
		return zero, false, errors.Join(ErrExecutionEpochOne, err)
	}
	// Ready means the existing terminal path must prove CLD_*; it is not a
	// reason to inspect/adopt a task after process identity can be retired.
	if mask&unix.POLLIN != 0 {
		return zero, false, nil
	}
	if before.pid == root.original.pid {
		controller, e := linuxEpochProcessRow(before.parent)
		if before.pid != root.original.pid || leader.parentBirth != root.owner.held.controllerStart || e != nil || controller.StartIdentity != leader.parentBirth {
			return zero, false, errors.Join(ErrExecutionEpochOne, e)
		}
	} else if err = root.liveParent(ctx, leader); err != nil {
		return zero, false, err
	}
	after, err := linuxEpochTraceIdentityOf(before.pid)
	if err != nil || !before.sameLifetime(after) || before.name != after.name || ctx.Err() != nil {
		return zero, false, errors.Join(ErrExecutionEpochOne, err)
	}
	return after, after.state == "Z", nil
}

// Reconcile only after all previously registered sibling tasks have retired.
// A creator can no longer publish a late CLONE stop then; this is deliberately
// narrower than discovering live work. Across groups one turn reads at most
// the global TID cap plus one overflow sentinel, with no recursive/host scan.
func (root *linuxEpochTraceRoot) reconcileExitingGroups(ctx context.Context) error {
	if !root.gateOwned || ctx == nil {
		return ErrExecutionEpochOne
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	remaining := linuxEpochTraceMaxTIDs
	for _, leader := range root.owner.leaders {
		if leader.root != root || !leader.exiting {
			continue
		}
		// SIGKILL issue is not process-group terminal proof. A Z original
		// root can still have stopped mm siblings and an unreadable pidfd.
		// Keep its own group progressing, but do not ask a dying descendant
		// to prove that root as terminal until the actual CLD path did so.
		// No task, ownership, RSS or terminal authority is retired here.
		if root.hardDeathIssued && !root.exitSeen && leader.original.pid != root.original.pid {
			continue
		}
		sibling := false
		for tid, task := range root.tasks {
			if tid != leader.original.pid && task.original.tgid == leader.original.pid {
				sibling = true
				break
			}
		}
		if sibling {
			continue
		}
		anchor, inspect, err := root.exitingGroupAnchor(ctx, leader)
		if err != nil {
			return fmt.Errorf("group/anchor: %w", err)
		}
		if !inspect {
			continue
		}
		entries, err := linuxEpochTraceGroupEntries(anchor.pid, remaining)
		if err != nil {
			return fmt.Errorf("group/task-census: %w", err)
		}
		remaining -= len(entries)
		for _, tid := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if tid == anchor.pid {
				continue
			}
			if witness, found := root.detached[tid]; found {
				present, e := root.detachedCurrent(ctx, witness)
				if e != nil {
					return e
				}
				if present {
					continue // Actual witnessed EXIT: no continuation/adoption.
				}
				root.forgetDetached(tid)
				continue // A vanished directory entry grants no new authority.
			}
			if err = root.reconcileThread(ctx, leader, anchor, tid); err != nil {
				return fmt.Errorf("group/thread tid=%d: %w", tid, err)
			}
		}
		if _, _, err = root.exitingGroupAnchor(ctx, leader); err != nil {
			return fmt.Errorf("group/anchor-after: %w", err)
		}
	}
	return nil
}

func linuxEpochTraceGroupEntries(pid, bound int) ([]int, error) {
	if pid <= 0 || bound < 1 || bound > linuxEpochTraceMaxTIDs {
		return nil, ErrExecutionEpochOne
	}
	dir, err := os.Open("/proc/" + strconv.Itoa(pid) + "/task")
	if err != nil {
		return nil, err
	}
	entries, readErr := dir.ReadDir(bound + 1)
	closeErr := dir.Close()
	if readErr != nil || closeErr != nil || len(entries) > bound {
		return nil, errors.Join(ErrExecutionEpochOne, readErr, closeErr)
	}
	ids := make([]int, 0, len(entries))
	foundLeader := false
	for _, entry := range entries {
		tid, e := strconv.Atoi(entry.Name())
		if e != nil || tid <= 0 || strconv.Itoa(tid) != entry.Name() {
			return nil, ErrExecutionEpochOne
		}
		ids = append(ids, tid)
		foundLeader = foundLeader || tid == pid
	}
	if !foundLeader {
		return nil, ErrExecutionEpochOne
	}
	return ids, nil
}

func (root *linuxEpochTraceRoot) reconcileThread(ctx context.Context, leader *linuxEpochTraceLeader, anchor linuxEpochTraceIdentity, tid int) error {
	if tid == anchor.pid || root.tasks[tid] != nil || root.owner.leaders[tid] != nil {
		return ErrExecutionEpochOne
	}
	before, err := linuxEpochTraceIdentityOf(tid)
	if err != nil || !root.reconcilableThread(leader, anchor, before) {
		return errors.Join(ErrExecutionEpochOne, err)
	}
	// No lost thread is continued until a real owned ptrace stop has actually
	// been consumed and its current mm is one of the still-held exact images.
	stop, err := linuxEpochTraceWait(tid, unix.WSTOPPED)
	if err != nil || stop.code != 4 || stop.signo != int32(unix.SIGCHLD) || stop.pid != int32(tid) || stop.uid != uint32(os.Getuid()) {
		return fmt.Errorf("group/owned-stop info=%+v: %w", stop, errors.Join(ErrExecutionEpochOne, err))
	}
	if err = root.allowedImage(ctx, tid, nil); err != nil {
		return fmt.Errorf("group/current-image: %w", err)
	}
	after, err := linuxEpochTraceIdentityOf(tid)
	if err != nil || !before.sameLifetime(after) || before.name != after.name || !root.reconcilableThread(leader, anchor, after) || ctx.Err() != nil {
		return errors.Join(ErrExecutionEpochOne, err)
	}
	if _, inspect, e := root.exitingGroupAnchor(ctx, leader); e != nil || !inspect {
		return errors.Join(ErrExecutionEpochOne, e)
	}
	root.owner.mu.Lock()
	if root.owner.tids >= linuxEpochTraceMaxTIDs {
		root.owner.mu.Unlock()
		return ErrExecutionEpochOne
	}
	root.owner.tids++
	root.owner.mu.Unlock()
	task := &linuxEpochTraceTask{original: after, reconciled: true}
	root.tasks[tid] = task
	if err = root.record(tid, after.tgid, int(stop.status)>>8, after.birth); err != nil {
		return err
	}
	return root.continueReconciled(ctx, tid, task, int(stop.status)>>8, int(stop.status)&255)
}

func (root *linuxEpochTraceRoot) reconcilableThread(leader *linuxEpochTraceLeader, anchor, thread linuxEpochTraceIdentity) bool {
	return leader != nil && leader.exiting && thread.pid != thread.tgid && thread.tgid == anchor.pid && thread.parent == anchor.parent && thread.tracer == root.tid && thread.sid == anchor.sid && thread.namespace != nil && anchor.namespace != nil && os.SameFile(thread.namespace, anchor.namespace) && thread.state == "t"
}

func (root *linuxEpochTraceRoot) continueReconciled(ctx context.Context, tid int, task *linuxEpochTraceTask, event, signal int) error {
	leader := root.owner.leaders[task.original.tgid]
	anchor, inspect, err := root.exitingGroupAnchor(ctx, leader)
	if err != nil || !inspect {
		return errors.Join(ErrExecutionEpochOne, err)
	}
	before, err := linuxEpochTraceIdentityOf(tid)
	if err != nil || !root.sameOwnedLifetime(ctx, task.original, before) || task.original.name != before.name || !root.reconcilableThread(leader, anchor, before) {
		return errors.Join(ErrExecutionEpochOne, err)
	}
	if err = root.allowedImage(ctx, tid, nil); err != nil {
		return err
	}
	after, err := linuxEpochTraceIdentityOf(tid)
	if err != nil || !before.sameLifetime(after) || before.name != after.name || !root.reconcilableThread(leader, anchor, after) || ctx.Err() != nil {
		return errors.Join(ErrExecutionEpochOne, err)
	}
	if event == unix.PTRACE_EVENT_EXIT {
		// This dying nonleader cannot execute application code after its actual
		// EXIT stop. Actual group CLD and original Handle.Wait still own closure.
		if err = unix.PtraceDetach(tid); err != nil {
			return err
		}
		root.rememberDetached(tid, after)
		return nil
	}
	if event != 0 {
		return fmt.Errorf("group/unsupported-event event=%d: %w", event, ErrExecutionEpochOne)
	}
	var information unix.Siginfo
	_, _, errno := unix.Syscall6(unix.SYS_PTRACE, unix.PTRACE_GETSIGINFO, uintptr(tid), 0, uintptr(unsafe.Pointer(&information)), 0, 0)
	if errno != 0 || information.Signo != int32(signal) {
		return errors.Join(ErrExecutionEpochOne, errno)
	}
	// No application continuation is granted to a reconciled dying-group
	// thread. SIGKILL's actual effect on group status is never normalized: the
	// original genuine Handle.Wait must still meet the caller's exact oracle.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return unix.PtraceCont(tid, int(unix.SIGKILL))
}

// A witnessed nonleader EXIT transfers its existing global TID slot to a
// current witness until disappearance/new birth or actual group CLD. No
// cumulative ledger or new signal authority is created by these records.
func (root *linuxEpochTraceRoot) rememberDetached(tid int, identity linuxEpochTraceIdentity) {
	if root.detached == nil {
		root.detached = make(map[int]linuxEpochTraceIdentity)
	}
	root.detached[tid] = identity
	delete(root.tasks, tid) // Keep the existing owner.tids reservation.
}
func (root *linuxEpochTraceRoot) forgetDetached(tid int) {
	delete(root.detached, tid)
	root.owner.mu.Lock()
	root.owner.tids--
	root.owner.mu.Unlock()
}
func (root *linuxEpochTraceRoot) retireDetachedGroup(group int) {
	for tid, identity := range root.detached {
		if identity.tgid == group {
			root.forgetDetached(tid)
		}
	}
}
func (root *linuxEpochTraceRoot) retireDetached(ctx context.Context) error {
	for tid, identity := range root.detached {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		present, err := root.detachedCurrent(ctx, identity)
		if err != nil {
			return fmt.Errorf("detached/current tid=%d: %w", tid, err)
		}
		if !present {
			root.forgetDetached(tid)
		}
	}
	return nil
}
func linuxEpochTraceDetachedCurrent(original linuxEpochTraceIdentity) (bool, error) {
	return linuxEpochTraceDetachedCurrentParent(original, nil)
}

func (root *linuxEpochTraceRoot) detachedCurrent(ctx context.Context, original linuxEpochTraceIdentity) (bool, error) {
	return linuxEpochTraceDetachedCurrentParent(original, func() bool {
		witness, found := root.detached[original.pid]
		return found && witness.sameLifetime(original) && root.hardDeathAnchor(ctx) == nil
	})
}

func linuxEpochTraceDetachedCurrentParent(original linuxEpochTraceIdentity, parentChanged func() bool) (bool, error) {
	file, err := os.Open("/proc/" + strconv.Itoa(original.pid) + "/stat")
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, gone, err := linuxEpochTraceDetachedRead(file)
	if err != nil || gone {
		return false, err
	}
	start, end := strings.IndexByte(string(raw), '('), strings.LastIndex(string(raw), ") ")
	if start < 1 || end <= start+1 {
		return false, fmt.Errorf("detached/stat-layout: %w", ErrExecutionEpochOne)
	}
	fields := strings.Fields(string(raw[end+2:]))
	if len(fields) < 22 || strings.TrimSpace(string(raw[:start])) != strconv.Itoa(original.pid) {
		return false, fmt.Errorf("detached/stat-fields: %w", ErrExecutionEpochOne)
	}
	if fields[19] != original.birth {
		return false, nil // Metadata retirement only, never adoption/Wait proof.
	}
	if fields[1] != strconv.Itoa(original.parent) && (parentChanged == nil || !parentChanged()) {
		return false, fmt.Errorf("detached/parent: %w", ErrExecutionEpochOne)
	}
	if fields[3] != strconv.Itoa(original.sid) {
		return false, fmt.Errorf("detached/session: %w", ErrExecutionEpochOne)
	}
	if string(raw[start+1:end]) != original.name {
		return false, fmt.Errorf("detached/name: %w", ErrExecutionEpochOne)
	}
	return true, nil
}

// Only witnessed EXIT metadata may retire on a read-time disappearance.
func linuxEpochTraceDetachedRead(file io.ReadCloser) ([]byte, bool, error) {
	raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if closeErr != nil || len(raw) > 4096 {
		return nil, false, errors.Join(ErrExecutionEpochOne, readErr, closeErr)
	}
	if errors.Is(readErr, os.ErrNotExist) || errors.Is(readErr, unix.ESRCH) {
		return nil, true, nil
	}
	if readErr != nil {
		return nil, false, errors.Join(ErrExecutionEpochOne, readErr)
	}
	return raw, false, nil
}
