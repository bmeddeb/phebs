//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const rootWaitChildEnvironment = "PHEBS_T42_ROOT_WAIT_TEST_CHILD"

func TestLinuxRootWaitFixedChild(t *testing.T) {
	switch os.Getenv(rootWaitChildEnvironment) {
	case "exit":
		os.Exit(17)
	case "pin-limit":
		if err := rootWaitPinFailureChild(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(125)
		}
		_, _ = fmt.Fprintln(os.Stdout, "pin error still delivered actual Wait")
		os.Exit(0)
	}
}

func rootWaitFixedCommand(ctx context.Context, mode string) *exec.Cmd {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxRootWaitFixedChild$")
	command.Env = []string{rootWaitChildEnvironment + "=" + mode, "GOMAXPROCS=2"}
	return command
}

func rootWaitObserveUnreaped(command *exec.Cmd) error {
	var information unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, command.Process.Pid, &information, unix.WEXITED|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("observe unreaped fixed child: %w", err)
		}
		if information.Signo != int32(unix.SIGCHLD) {
			return errors.New("fixed child exit was not observed")
		}
		return nil
	}
}

func rootWaitRequireExit17(command *exec.Cmd, err error) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 || command.ProcessState == nil || command.ProcessState.ExitCode() != 17 {
		return fmt.Errorf("actual Wait did not deliver fixed exit17: %v", err)
	}
	return nil
}

func TestLinuxRootWaitPinsExitedUnreapedChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := rootWaitFixedCommand(ctx, "exit")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var waited <-chan error
	consumed := false
	t.Cleanup(func() {
		if !consumed {
			if waited != nil {
				<-waited
			} else {
				// No Wait has started: this still-unreaped child owns its PID.
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		}
	})
	if err := rootWaitObserveUnreaped(command); err != nil {
		t.Fatal(err)
	}
	pidfd, pinErr, result := pinExecutionProcessRoot(command)
	waited = result
	if pinErr != nil {
		t.Fatal("unreaped zombie could not be pinned", pinErr)
	}
	t.Cleanup(func() {
		if unix.Close(pidfd) != nil {
			t.Error("owned pidfd close")
		}
	})
	waitErr := <-waited
	consumed = true
	if err := rootWaitRequireExit17(command, waitErr); err != nil {
		t.Fatal(err)
	}
	poll := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	count, err := unix.Poll(poll, 0)
	if err != nil || count != 1 || poll[0].Revents&unix.POLLIN == 0 {
		t.Fatal("actual exited root pidfd not ready", err)
	}
	for _, signal := range []unix.Signal{0, unix.SIGKILL} {
		if err := unix.PidfdSendSignal(pidfd, signal, nil, 0); !errors.Is(err, unix.ESRCH) {
			t.Fatal("joined root pidfd no longer names a live process", signal, err)
		}
	}
	if err := command.Wait(); err == nil {
		t.Fatal("a second Cmd.Wait was accepted")
	}
}

func TestLinuxRootWaitPinFailureStillWaits(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := rootWaitFixedCommand(ctx, "pin-limit").CombinedOutput()
	if err != nil || string(output) != "pin error still delivered actual Wait\n" {
		t.Fatal("isolated EMFILE pin failure lost native Wait", err, string(output))
	}
}

// RLIMIT_NOFILE changes only this fixed test subprocess. Observe the grandchild
// without reaping, force the real pidfd_open error, and restore before checking
// Wait. No global hook or production pin implementation is substituted.
func rootWaitPinFailureChild() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := rootWaitFixedCommand(ctx, "exit")
	if err := command.Start(); err != nil {
		return err
	}
	var waited <-chan error
	consumed := false
	defer func() {
		if !consumed {
			if waited != nil {
				<-waited
			} else {
				// No Wait has started: this still-unreaped child owns its PID.
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		}
	}()
	if err := rootWaitObserveUnreaped(command); err != nil {
		return err
	}
	var previous unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &previous); err != nil {
		return err
	}
	limited := previous
	limited.Cur = 0
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limited); err != nil {
		return err
	}
	pidfd, pinErr, result := pinExecutionProcessRoot(command)
	waited = result
	restoreErr := unix.Setrlimit(unix.RLIMIT_NOFILE, &previous)
	if pidfd >= 0 {
		_ = unix.Close(pidfd)
	}
	waitErr := <-waited
	consumed = true
	if restoreErr != nil || pidfd != -1 || !errors.Is(pinErr, unix.EMFILE) {
		return fmt.Errorf("fixed EMFILE pin control: fd=%d pin=%v restore=%v", pidfd, pinErr, restoreErr)
	}
	return rootWaitRequireExit17(command, waitErr)
}
