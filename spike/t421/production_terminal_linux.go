//go:build linux

package t421

import (
	"os"
	"os/exec"
	"syscall"
)

// Accept the exact native error, or Handle.Wait's one-child settlement join.
// Searching an error tree could hide accounting/transport/cancellation siblings.
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
