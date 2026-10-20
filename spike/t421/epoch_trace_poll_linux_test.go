//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Inject only a lower interrupted result. Every successful readiness result
// must come from the shared engine's next actual same-fd, same-context poll.
func linuxEpochTraceInterruptedPoll(ctx context.Context, fd int) (int16, error) {
	result := linuxEpochClassifierPollContinue(ctx, fd, linuxEpochClassifierPollResult{n: 7, revents: unix.POLLERR | unix.POLLHUP, syscallErr: unix.EINTR})
	return linuxEpochTracePollResult(result)
}

func TestLinuxEpochTracePollInterruptedThenActualSelf(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	ctx, cancel := context.WithTimeout(t.Context(), epochProcessProbeTimeout)
	defer cancel()
	if mask, e := linuxEpochTracePoll(ctx, fd); e != nil || mask != 0 {
		t.Fatal("actual original self pin not live", mask, e)
	}
	if mask, e := linuxEpochTraceInterruptedPoll(ctx, fd); e != nil || mask != 0 {
		t.Fatal("interrupted fields leaked into actual live result", mask, e)
	}
	if mask, e := linuxEpochTracePollOnce(fd); e != nil || mask != 0 {
		t.Fatal("one actual cleanup attempt on live pin", mask, e)
	}
	// The cleanup adapter does not continue an interrupted result itself. Even
	// a stale apparent HUP must remain an error with no returned readiness.
	mask, err := linuxEpochTracePollResult(linuxEpochClassifierPollResult{n: 1, revents: unix.POLLHUP, syscallErr: unix.EINTR})
	if mask != 0 || !errors.Is(err, unix.EINTR) {
		t.Fatal("strict interrupted attempt invented readiness", mask, err)
	}
	for _, result := range []linuxEpochClassifierPollResult{
		{n: -1, syscallErr: unix.EBADF},
		{n: -1, syscallErr: unix.EINVAL},
		{n: 1, revents: unix.POLLERR},
		{n: 1, revents: unix.POLLNVAL},
		{n: 1, revents: unix.POLLOUT},
		{n: -1}, {n: 2, revents: unix.POLLIN},
		{n: 0, revents: unix.POLLHUP}, {n: 1},
	} {
		continued := linuxEpochClassifierPollContinue(ctx, fd, result)
		if continued != result {
			t.Fatal("non-EINTR refusal retried or changed", result, continued)
		}
		if mask, e := linuxEpochTracePollResult(continued); mask != 0 || e == nil {
			t.Fatal("invalid error/count/mask accepted", result, mask, e)
		}
	}
}

func TestLinuxEpochTracePollClosedInvalidAndCanceled(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "trace-closed-pidfd")
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	// File.Fd is invalid after Close independently of slot reuse elsewhere.
	closed := int(file.Fd())
	if mask, e := linuxEpochTracePoll(t.Context(), closed); mask != 0 || !errors.Is(e, unix.EBADF) {
		t.Fatal("closed pin gained readiness", mask, e)
	}
	var limit unix.Rlimit
	if err = unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil || limit.Cur == 0 || limit.Cur > math.MaxInt32 {
		t.Fatal("real invalid-fd control unavailable", limit.Cur, err)
	}
	if mask, e := linuxEpochTracePoll(t.Context(), int(limit.Cur)); mask != 0 || e == nil || !strings.Contains(e.Error(), "poll_revents=32") {
		t.Fatal("kernel invalid-fd mask accepted or unrecorded", mask, e)
	}
	//nolint:staticcheck // Deliberately verify that a nil context fails closed.
	if mask, e := linuxEpochTracePoll(nil, closed); mask != 0 || !errors.Is(e, ErrExecutionEpochOne) {
		t.Fatal("nil context accepted", mask, e)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if mask, e := linuxEpochTracePoll(canceled, closed); mask != 0 || !errors.Is(e, context.Canceled) || errors.Is(e, unix.EBADF) {
		t.Fatal("canceled caller polled or gained readiness", mask, e)
	}
	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	if mask, e := linuxEpochTraceInterruptedPoll(expired, closed); mask != 0 || !errors.Is(e, context.DeadlineExceeded) || !errors.Is(e, unix.EINTR) {
		t.Fatal("interrupted retry escaped its original deadline", mask, e)
	}
}

func TestLinuxEpochTracePollOwnedChildLiveUnreapedAndHUP(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	childInput, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output, childOutput, err := os.Pipe()
	if err != nil {
		_ = childInput.Close()
		_ = input.Close()
		t.Fatal(err)
	}
	defer func() { _ = childInput.Close(); _ = input.Close(); _ = output.Close(); _ = childOutput.Close() }()
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochClassifierFixedChild$")
	command.Env = []string{linuxEpochClassifierChildEnvironment + "=gate", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.Stdin, command.Stdout, command.Stderr = childInput, childOutput, os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	fd, waited := -1, false
	defer func() {
		if !waited {
			if fd >= 0 {
				_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
			} else {
				// No Wait has begun: the original direct child is unreaped.
				_ = command.Process.Kill()
			}
			_ = command.Wait()
		}
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}()
	fd, err = unix.PidfdOpen(command.Process.Pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if childInput.Close() != nil || childOutput.Close() != nil {
		t.Fatal("parent child-end closure")
	}
	if err = output.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [6]byte
	if _, err = io.ReadFull(output, ready[:]); err != nil || string(ready[:]) != "READY\n" {
		t.Fatal("actual live own child barrier", string(ready[:]), err)
	}
	if mask, e := linuxEpochTraceInterruptedPoll(ctx, fd); e != nil || mask != 0 {
		t.Fatal("same original child pin not actually live", mask, e)
	}
	if _, err = input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	for {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		var info unix.Siginfo
		err = unix.Waitid(unix.P_PIDFD, fd, &info, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		child := linuxEpochTraceChildInfo(&info)
		if child.terminal(command.Process.Pid) {
			if child.code != 1 || child.status != 17 {
				t.Fatal("actual child terminal code", child)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	if command.ProcessState != nil {
		t.Fatal("WNOWAIT manufactured a genuine Wait")
	}
	if mask, e := linuxEpochTraceInterruptedPoll(ctx, fd); e != nil || mask&unix.POLLIN == 0 || mask&unix.POLLHUP != 0 {
		t.Fatal("actual exited-unreaped pin lost its readiness", mask, e)
	}
	waitErr := command.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != 17 || command.ProcessState == nil || command.ProcessState.ExitCode() != 17 {
		t.Fatal("sole actual own child Wait17", waitErr)
	}
	if mask, e := linuxEpochTraceInterruptedPoll(ctx, fd); e != nil || mask&unix.POLLHUP == 0 {
		t.Fatal("actual original reap/HUP not observed", mask, e)
	}
	if mask, e := linuxEpochTracePollOnce(fd); e != nil || mask&unix.POLLHUP == 0 {
		t.Fatal("strict cleanup attempt lacks actual HUP", mask, e)
	}
	t.Logf("originalChild=%d realLive=true WNOWAIT17=true soleWait17=true originalHUP=true; initial synthetic EINTR always followed by an actual same-fd poll", command.Process.Pid)
}
