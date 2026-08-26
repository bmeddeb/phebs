//go:build darwin || linux

package t4013

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func isolatePrivateServerSession(command *exec.Cmd) error {
	if command == nil || command.SysProcAttr != nil {
		return errors.New("T40.13 private process session is unavailable")
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.WaitDelay = 5 * time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		foundAny := false
		for attempt := 0; attempt < 10; attempt++ {
			found, err := signalPrivateServerSession(command.Process.Pid, syscall.SIGKILL)
			if err != nil {
				return fmt.Errorf("T40.13 cancel private process session: %w", err)
			}
			foundAny = foundAny || found
			if !found {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !foundAny {
			return os.ErrProcessDone
		}
		return nil
	}
	return nil
}

// Graceful shutdown signals only phebs. The server owns the ordering for its
// supervised SurrealDB child and background writers.
func interruptPrivateServerRoot(process *os.Process) (bool, error) {
	if process == nil || process.Pid <= 0 {
		return false, errors.New("T40.13 private server process is invalid")
	}
	if err := process.Signal(os.Interrupt); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return false, nil
		}
		return false, fmt.Errorf("T40.13 interrupt private server process: %w", err)
	}
	return true, nil
}

func killPrivateServerSession(sessionID int) error {
	if sessionID <= 0 {
		return errors.New("T40.13 private process session is invalid")
	}
	for attempt := 0; attempt < 10; attempt++ {
		found, err := signalPrivateServerSession(sessionID, syscall.SIGKILL)
		if err != nil || !found {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func privateServerSessionAlive(sessionID int) (bool, error) {
	pids, err := privateServerSessionPIDs(sessionID)
	return len(pids) > 0, err
}

func signalPrivateServerSession(sessionID int, signal syscall.Signal) (bool, error) {
	pids, err := privateServerSessionPIDs(sessionID)
	if err != nil {
		return false, err
	}
	var result error
	for _, pid := range pids {
		if err := syscall.Kill(pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			result = errors.Join(result, err)
		}
	}
	return len(pids) > 0, result
}

func expectedPrivateServerInterrupt(waitErr error) bool {
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGINT
}
