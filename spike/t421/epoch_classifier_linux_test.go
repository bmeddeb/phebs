//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const linuxEpochClassifierChildEnvironment = "PHEBS_PRIVATE_EPOCH_CLASSIFIER_CHILD"

// Fixed own-image child only; this supplies no image/resource admission.
func TestLinuxEpochClassifierFixedChild(t *testing.T) {
	if os.Getenv(linuxEpochClassifierChildEnvironment) != "gate" {
		return
	}
	if _, err := io.WriteString(os.Stdout, "READY\n"); err != nil {
		os.Exit(125)
	}
	var input [1]byte
	if _, err := io.ReadFull(os.Stdin, input[:]); err != nil || input[0] != 1 {
		os.Exit(125)
	}
	os.Exit(17)
}

func TestLinuxEpochClassifierPollSelfAndInterruptedContinuation(t *testing.T) {
	pidfd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Close(pidfd); err != nil {
			t.Error(err)
		}
	}()
	for _, tc := range []struct {
		name        string
		interrupted bool
	}{{"real-poll", false}, {"interrupted-then-real-poll", true}} {
		t.Run(tc.name, func(t *testing.T) {
			result := linuxEpochClassifierPoll(t.Context(), pidfd)
			if tc.interrupted {
				// This lower syscall-result seam injects only a refusal. Every accepted
				// live result comes from the subsequent real same-fd zero-timeout poll.
				result = linuxEpochClassifierPollContinue(t.Context(), pidfd, linuxEpochClassifierPollResult{n: 7, revents: unix.POLLERR | unix.POLLNVAL, syscallErr: unix.EINTR})
			}
			if !result.live() || result.n != 0 || result.revents != 0 || result.syscallErr != nil || result.contextErr != nil {
				t.Fatal("actual self pidfd not live or interrupted fields leaked", result)
			}
		})
	}
	// Non-EINTR results must not poll again and convert an actual refusal into
	// this self pidfd's live state. These are refusal controls, never fake alive.
	for _, tc := range []struct {
		name   string
		result linuxEpochClassifierPollResult
	}{
		{"EBADF", linuxEpochClassifierPollResult{n: -1, syscallErr: unix.EBADF}},
		{"POLLERR", linuxEpochClassifierPollResult{n: 1, revents: unix.POLLERR}},
		{"POLLNVAL", linuxEpochClassifierPollResult{n: 1, revents: unix.POLLNVAL}},
		{"readiness", linuxEpochClassifierPollResult{n: 1, revents: unix.POLLIN}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := linuxEpochClassifierPollContinue(t.Context(), pidfd, tc.result)
			if result.live() || result.n != tc.result.n || result.revents != tc.result.revents || result.syscallErr != tc.result.syscallErr {
				t.Fatal("non-interrupted refusal retried/changed", result)
			}
		})
	}
}

func TestLinuxEpochClassifierPollClosedInvalidAndCanceled(t *testing.T) {
	pidfd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(pidfd), "classifier-closed-pidfd")
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	// File.Fd after Close is invalid independently of numeric fd reuse. Poll's
	// convention of ignoring negative descriptors must never mean "live".
	result := linuxEpochClassifierPoll(t.Context(), int(file.Fd()))
	if result.live() || !errors.Is(result.syscallErr, unix.EBADF) {
		t.Fatal("closed descriptor accepted", result)
	}
	// A real kernel POLLNVAL, using an fd at/above the process's allocation
	// ceiling. No host setting/rlimit changes and no recycled fd assumptions.
	var limit unix.Rlimit
	if err = unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if limit.Cur == 0 || limit.Cur > math.MaxInt32 {
		t.Fatal("fixed invalid-fd control unavailable", limit.Cur)
	}
	invalid := linuxEpochClassifierPoll(t.Context(), int(limit.Cur))
	if invalid.live() || invalid.n != 1 || invalid.revents&unix.POLLNVAL == 0 || invalid.syscallErr != nil {
		t.Fatal("actual invalid-fd readiness accepted", invalid)
	}
	for _, stage := range []string{"live-before", "live-after"} {
		detail := invalid.refusal(stage)
		if !strings.HasPrefix(detail, stage+" ") || !strings.Contains(detail, "poll_n=1") || !strings.Contains(detail, fmt.Sprintf("poll_revents=%d", uint16(invalid.revents))) || !strings.Contains(detail, "poll_errno=0") {
			t.Fatal("private native detail missing", detail)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	canceled := linuxEpochClassifierPoll(ctx, int(file.Fd()))
	if canceled.live() || !errors.Is(canceled.contextErr, context.Canceled) || canceled.syscallErr != nil || canceled.n != -1 {
		t.Fatal("canceled-before-poll was not refused", canceled)
	}
	deadline, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	expired := linuxEpochClassifierPollContinue(deadline, int(limit.Cur), linuxEpochClassifierPollResult{n: -1, syscallErr: unix.EINTR})
	if expired.live() || !errors.Is(expired.contextErr, context.DeadlineExceeded) || !errors.Is(expired.syscallErr, unix.EINTR) {
		t.Fatal("interrupted retry replaced the caller's expired context", expired)
	}
	if !strings.Contains(expired.refusal("live-after"), "poll_errno=4") || !strings.Contains(expired.refusal("live-after"), "context_deadline=true") {
		t.Fatal("interrupted deadline detail lost", expired.refusal("live-after"))
	}
}

func TestLinuxEpochClassifierPollOwnedChildAndUnreapedExit(t *testing.T) {
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
	pidfd := -1
	waited := false
	defer func() {
		if !waited {
			if pidfd >= 0 {
				_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
			} else {
				// No Wait has run, so this original direct child still owns its PID.
				_ = command.Process.Kill()
			}
			_ = command.Wait()
			waited = true
		}
		if pidfd >= 0 {
			if e := unix.Close(pidfd); e != nil {
				t.Error(e)
			}
		}
	}()
	pidfd, err = unix.PidfdOpen(command.Process.Pid, 0)
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
		t.Fatal("actual live child barrier", string(ready[:]), err)
	}
	row, err := linuxEpochProcessRow(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()
	images := []linuxEpochClassImage{{file: image, class: "controller"}}
	if class, refusal := linuxEpochClassifyRowDetail(ctx, row, images); class != "controller" || refusal != "" {
		t.Fatal("actual own child image/lifetime fence", class, refusal)
	}
	live := linuxEpochClassifierPollContinue(ctx, pidfd, linuxEpochClassifierPollResult{n: -1, revents: unix.POLLNVAL, syscallErr: unix.EINTR})
	if !live.live() {
		t.Fatal("interrupted actual child poll did not retry live", live)
	}
	if _, err = input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	// Observe actual kernel exit without consuming it. There is no tracer, and
	// the sole genuine Cmd.Wait below still owns this child's terminal status.
	for {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		var info unix.Siginfo
		err = unix.Waitid(unix.P_PIDFD, pidfd, &info, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.Signo == int32(unix.SIGCHLD) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if command.ProcessState != nil {
		t.Fatal("WNOWAIT manufactured genuine Wait")
	}
	exited := linuxEpochClassifierPollContinue(ctx, pidfd, linuxEpochClassifierPollResult{n: -1, syscallErr: unix.EINTR})
	if exited.live() || exited.n != 1 || exited.revents&unix.POLLIN == 0 || exited.syscallErr != nil || exited.contextErr != nil {
		t.Fatal("actual exited-unreaped readiness accepted", exited)
	}
	if _, refusal := linuxEpochClassifyRowDetail(ctx, row, images); refusal == "" {
		t.Fatal("original row accepted after actual exit")
	}
	waitErr := command.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != 17 || command.ProcessState == nil || command.ProcessState.ExitCode() != 17 {
		t.Fatal("actual child Wait17 missing", waitErr)
	}
	final := linuxEpochClassifierPoll(ctx, pidfd)
	if final.live() || final.revents&unix.POLLHUP == 0 {
		t.Fatal("original child pin lacks actual reap readiness", final)
	}
	t.Logf("actual child=%d live→unreaped-readable→Wait17/HUP; EINTR control used a subsequent real poll", command.Process.Pid)
}
