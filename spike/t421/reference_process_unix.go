//go:build darwin || linux

package t421

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Go's compiler/linker children inherit this private process group. Cooperative
// cancellation kills that group before the parent is reaped and custody removed.
// This is not the later launcher's durable hard-death/session supervision.
func prepareReferenceCommand(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
