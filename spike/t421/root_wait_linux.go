//go:build linux

package t421

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

// Call immediately after successful Start, before any Wait or release of this
// exclusively owned child. An unreaped exited child still reserves its PID;
// pin it before the sole Wait can reap it and permit numeric PID reuse.
// A pin error never substitutes for native Wait or permits a bare-PID fallback.
func pinExecutionProcessRoot(command *exec.Cmd) (int, <-chan error, error) {
	pidfd, pinErr := -1, error(unix.EINVAL)
	if command != nil && command.Process != nil && command.Process.Pid > 0 {
		pidfd, pinErr = unix.PidfdOpen(command.Process.Pid, 0)
	}
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
