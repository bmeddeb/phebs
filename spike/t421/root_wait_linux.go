//go:build linux

package t421

import (
	"os/exec"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

// Call immediately after successful Start, before any Wait or release of this
// exclusively owned child. An unreaped exited child still reserves its PID;
// pin it before the sole Wait can reap it and permit numeric PID reuse.
// A pin error never substitutes for native Wait or permits a bare-PID fallback.
func pinExecutionProcessRoot(command *exec.Cmd) (int, <-chan error, error) {
	pidfd, pinErr := pinExecutionRootDescriptor(command)
	waited := make(chan error, 1)
	go func() {
		if command == nil {
			waited <- unix.EINVAL
			return
		}
		waited <- command.Wait()
	}()
	return pidfd, waited, pinErr
}

// Same pin-before-reap boundary, but the genuine admitted handle owns native
// Wait and parent DA settlement. Never substitute Cmd.Wait for this path.
func pinAdmittedExecutionProcessRoot(command *exec.Cmd, handle dispatchadmission.Handle) (int, <-chan error, error) {
	pidfd, pinErr := pinExecutionRootDescriptor(command)
	waited := make(chan error, 1)
	go func() {
		waited <- handle.Wait()
	}()
	return pidfd, waited, pinErr
}
func pinExecutionRootDescriptor(command *exec.Cmd) (int, error) {
	if command == nil || command.Process == nil || command.Process.Pid <= 0 {
		return -1, unix.EINVAL
	}
	return unix.PidfdOpen(command.Process.Pid, 0)
}
