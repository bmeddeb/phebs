//go:build darwin || linux

package t401

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

func isolateProcessGroup(command *exec.Cmd) error {
	if command == nil || command.SysProcAttr != nil {
		return errors.New("T40.1 cancellation process group is unavailable")
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func stopProcessGroup(pid int) error {
	if pid <= 0 {
		return errors.New("T40.1 cancellation process group is invalid")
	}
	if err := syscall.Kill(-pid, syscall.SIGSTOP); err != nil {
		return fmt.Errorf("T40.1 stop cancellation process group: %w", err)
	}
	return nil
}

func continueProcessGroup(pid int) error {
	if pid <= 0 {
		return errors.New("T40.1 cancellation process group is invalid")
	}
	if err := syscall.Kill(-pid, syscall.SIGCONT); err != nil {
		return fmt.Errorf("T40.1 continue cancellation process group: %w", err)
	}
	return nil
}

func resumeProcesses(pids []int) error {
	for _, pid := range pids {
		if pid <= 0 {
			return errors.New("T40.1 cancellation Git process is invalid")
		}
		if err := syscall.Kill(pid, syscall.SIGCONT); err != nil &&
			!errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("T40.1 resume cancellation Git process: %w", err)
		}
	}
	return nil
}
