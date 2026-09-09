//go:build darwin

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// killExecutionProcessSession consumes only the caller's existing Handle.Wait
// channel. The caller has exclusive consumption and has already armed the
// exact PC/DA/SA terminal protocols; this helper cannot establish those facts.
// An already received/natural exit cannot become an owned terminal kill.
func killExecutionProcessSession(ctx context.Context, command *exec.Cmd, waited <-chan error) (result executionProcessDeath, retErr error) {
	if ctx == nil || ctx.Err() != nil || command == nil || command.Process == nil || command.Process.Pid <= 0 ||
		command.SysProcAttr == nil || !command.SysProcAttr.Setsid || waited == nil {
		return result, ErrExecutionProductionCustody
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || !time.Now().Before(deadline) {
		return result, ErrExecutionProductionCustody
	}
	if maximum := time.Now().Add(30 * time.Second); maximum.Before(deadline) {
		deadline = maximum
	}
	pid := command.Process.Pid
	var killErr error
	select {
	case value, ok := <-waited:
		if !ok {
			return result, ErrExecutionProductionCustody
		}
		result.WaitErr, result.RootJoined = value, true
		retErr = ErrExecutionProductionCustody
	default:
		// Do not read command.ProcessState here: Cmd.Wait writes it before
		// returning, concurrently with this Kill attempt and session cleanup.
		killErr = command.Process.Kill()
	}
	// Kill descendants promptly, including different process groups. Waiting
	// first could leave inherited output pipes open behind an orphaned child.
	sessionKillErr := t4013.KillPrivateProcessSession(pid)
	if !result.RootJoined {
		result.WaitErr, result.RootJoined = waitExecutionTerminalRoot(waited, deadline)
	}
	sessionErr := t4013.WaitPrivateProcessSession(pid, deadline)
	finalSessionErr := sessionErr
	if !result.RootJoined || sessionErr != nil {
		// The established six-second forced cleanup is never a fresh successful
		// measurement window, even if it eventually joins and empties custody.
		retErr = ErrExecutionProductionCustody
		forcedDeadline := time.Now().Add(6 * time.Second)
		sessionKillErr = errors.Join(sessionKillErr, t4013.KillPrivateProcessSession(pid))
		if !result.RootJoined {
			result.WaitErr, result.RootJoined = waitExecutionTerminalRoot(waited, forcedDeadline)
		}
		finalSessionErr = t4013.WaitPrivateProcessSession(pid, forcedDeadline)
	}
	if result.RootJoined {
		result.ProcessState = command.ProcessState // Sole Wait consumption joins this write.
	}
	result.SessionEmpty = finalSessionErr == nil
	if retErr != nil || killErr != nil || sessionKillErr != nil || sessionErr != nil || finalSessionErr != nil ||
		ctx.Err() != nil || !time.Now().Before(deadline) || !result.RootJoined || !executionProcessSIGKILL(result.WaitErr, result.ProcessState, pid) {
		return result, errors.Join(ErrExecutionProductionCustody, killErr, sessionKillErr, sessionErr, finalSessionErr, result.WaitErr, ctx.Err())
	}
	return result, nil
}

// Unlike the older cleanup-only wait helper, a closed channel with no actual
// result cannot establish a join or authorize reading Cmd.ProcessState.
func waitExecutionTerminalRoot(waited <-chan error, deadline time.Time) (error, bool) {
	timer := time.NewTimer(max(0, time.Until(deadline)))
	defer timer.Stop()
	select {
	case err, received := <-waited:
		return err, received
	case <-timer.C:
		return nil, false
	}
}

// Handle.Wait returns the native error directly without a client, or one
// errors.Join layer with its DA settlement. Accept exactly that native shape;
// no errors.As search can hide a cancellation/accounting/transport sibling.
func executionProcessSIGKILL(waitErr error, state *os.ProcessState, pid int) bool {
	if state == nil || state.Pid() != pid {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		return false
	}
	if joined, ok := waitErr.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) != 1 {
			return false
		}
		waitErr = children[0]
	}
	exit, ok := waitErr.(*exec.ExitError)
	return ok && exit != nil && exit.ProcessState == state
}
