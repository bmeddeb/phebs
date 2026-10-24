//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// Called only with the shared census gate held. A process leader Z with live
// sibling threads cannot produce actual group-terminal CLD_* and ordinary
// process-pidfd readiness, and therefore never gains a zero-RSS exemption.
func (root *linuxEpochTraceRoot) terminalProof(ctx context.Context, leader *linuxEpochTraceLeader) (linuxEpochTraceIdentity, error) {
	var zero linuxEpochTraceIdentity
	if ctx == nil || ctx.Err() != nil || root == nil || root.owner == nil || leader == nil || leader.root != root || !leader.verified || leader.fd < 0 || root.owner.leaders[leader.original.pid] != leader || leader.original.pid != leader.original.tgid {
		return zero, fmt.Errorf("terminal/shape: %w", ErrExecutionEpochOne)
	}
	before, err := linuxEpochTraceIdentityOf(leader.original.pid)
	if err != nil {
		return zero, fmt.Errorf("terminal/identity-before: %w", err)
	}
	if !root.sameOwnedLifetime(ctx, leader.original, before) || before.state != "Z" || before.tracer != root.tid || before.namespace == nil || !os.SameFile(before.namespace, root.owner.held.pidNamespace) {
		return zero, fmt.Errorf("terminal/binding-before pid=%d group=%d parent=%d tracer=%d sid=%d state=%s birth=%s expected-parent=%d expected-tracer=%d expected-sid=%d expected-birth=%s: %w", before.pid, before.tgid, before.parent, before.tracer, before.sid, before.state, before.birth, leader.original.parent, root.tid, leader.original.sid, leader.original.birth, ErrExecutionEpochOne)
	}
	mask, err := linuxEpochTracePoll(ctx, leader.fd)
	if err != nil || mask&unix.POLLIN == 0 || mask&unix.POLLHUP != 0 {
		return zero, fmt.Errorf("terminal/pidfd mask=%d: %w", mask, errors.Join(ErrExecutionEpochOne, err))
	}
	var information unix.Siginfo
	for {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		err = unix.Waitid(unix.P_PIDFD, leader.fd, &information, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG|unix.WALL, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		break
	}
	if err != nil || !linuxEpochTraceChildInfo(&information).terminal(before.pid) {
		return zero, fmt.Errorf("terminal/waitid info=%+v: %w", linuxEpochTraceChildInfo(&information), errors.Join(ErrExecutionEpochOne, err))
	}
	after, err := linuxEpochTraceIdentityOf(before.pid)
	if err != nil || !before.sameLifetime(after) || before.name != after.name || after.state != "Z" || ctx.Err() != nil {
		return zero, fmt.Errorf("terminal/identity-after: %w", errors.Join(ErrExecutionEpochOne, err))
	}
	if before.pid == root.original.pid {
		if before.pid != root.original.pid || leader.parentBirth != root.owner.held.controllerStart {
			return zero, fmt.Errorf("terminal/controller-binding: %w", ErrExecutionEpochOne)
		}
		controller, e := linuxEpochProcessRow(before.parent)
		if e != nil || controller.StartIdentity != leader.parentBirth {
			return zero, fmt.Errorf("terminal/controller-current: %w", errors.Join(ErrExecutionEpochOne, e))
		}
	} else if err = root.liveParent(ctx, leader); err != nil {
		return zero, fmt.Errorf("terminal/parent-live: %w", errors.Join(ErrExecutionEpochOne, err))
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return after, nil
}

// This is current terminal ownership, not an executable-history category. The
// native row and all of its observed RSS remain intact. Only exact source-event
// leaders can enter it after current kernel proof; missing images elsewhere
// still refuse. A server's own terminal root remains an invalid phase sample.
func (owner *linuxEpochTraceOwner) defunctClass(ctx context.Context, gauge *ProcessObservationGauge, row t4013.NativeProcessRecord) (int, bool) {
	if owner == nil || gauge == nil || row.PID == gauge.rootPID {
		return 0, false
	}
	leader := owner.leaders[row.PID]
	if leader == nil || leader.root == nil || !leader.root.hardDeathIssued && row.ParentPID != leader.original.parent || row.StartIdentity != leader.original.birth {
		return 0, false
	}
	current, err := leader.root.terminalProof(ctx, leader)
	if err != nil || current.parent != row.ParentPID || current.name != row.ObservedName {
		return 0, false
	}
	if gauge.rootPID != owner.held.controllerPID {
		// A nested process must belong to this exact original server root, not to
		// the concurrently running author or to a previous epoch.
		if leader.root.original.pid != gauge.rootPID {
			return 0, false
		}
	}
	for index, class := range gauge.observation.Classes {
		if class.Class == linuxEpochOwnedDefunctClass {
			return index, true
		}
	}
	return 0, false
}
