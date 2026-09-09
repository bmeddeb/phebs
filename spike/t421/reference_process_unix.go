//go:build darwin || linux

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// runReferenceCommand owns preparation children, not an operational author's
// nested Git dispatches. A successful root exit is insufficient: every recorded
// session must be empty before the caller may release its preparation custody.
func runReferenceCommand(ctx context.Context, command *exec.Cmd) error {
	if ctx == nil || ctx.Err() != nil || command == nil {
		return errors.New("reference preparation context unavailable")
	}
	if err := prepareReferenceCommand(command); err != nil {
		return err
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	waitErr := command.Wait() // Sole native Wait also joins the existing output pumps.
	_, _, err := finishExecutionProcessSession(command.Process.Pid, nil, true, waitErr, time.Now().Add(5*time.Second))
	return errors.Join(err, ctx.Err())
}

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
