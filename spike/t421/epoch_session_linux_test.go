//go:build linux

package t421

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func TestLinuxEpochSessionCaptureRefusalKeepsRootCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	lifetime, stopFlow := context.WithCancel(context.Background())
	defer stopFlow()
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochSessionFixedChild$", "--", "t422-linux-epoch-leaf")
	command.Env = []string{"GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	server := &linuxEpochServer{pidfd: -1}
	server.pidfd, server.pinErr = pinExecutionRootDescriptor(command)
	joined := false
	defer func() {
		if server.pidfd >= 0 {
			_ = unix.PidfdSendSignal(server.pidfd, unix.SIGKILL, nil, 0)
		}
		if !joined {
			_ = command.Wait()
		}
		if err := server.closeRoot(); err != nil {
			t.Error("root callback/descriptor retirement", err)
		}
	}()
	if server.pinErr != nil {
		t.Fatal("actual root pin", server.pinErr)
	}
	run := &ExecutionEpochOneRun{command: command}
	// Missing namespace metadata forces the actual anchor-capture refusal.
	held := &linuxEpochHeldFlow{controllerPID: os.Getpid(), servers: map[*ExecutionEpochOneRun]*linuxEpochServer{run: server}}
	_, server.sessionErr = captureLinuxEpochSession(held, run, server)
	if server.sessionErr == nil || server.pinErr != nil || server.session != nil || server.sessionEmpty {
		t.Fatal("capture refusal conflated with root pin or session evidence")
	}
	server.armCancellation(ctx, lifetime)
	stopFlow()
	poll := []unix.PollFd{{Fd: int32(server.pidfd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(poll, 20)
		if err != nil && !errors.Is(err, unix.EINTR) {
			t.Fatal("owned root exit observation", err)
		}
		if n == 1 && poll[0].Revents == unix.POLLIN {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("flow cancellation did not stop pinned root")
		}
	}
	if command.ProcessState != nil || server.session != nil || server.sessionEmpty || ctx.Err() != nil {
		t.Fatal("exit readiness fabricated Wait/session completion")
	}
	waitErr := command.Wait()
	joined = true
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || command.ProcessState == nil {
		t.Fatal("actual root Wait missing", waitErr)
	}
	status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("flow callback did not deliver actual root SIGKILL", waitErr)
	}
}

// Fixed own-image diagnostic children; this does not issue tool provenance.
func TestLinuxEpochSessionFixedChild(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "t422-linux-epoch-leaf" {
		time.Sleep(30 * time.Second)
		os.Exit(19)
	}
	if mode != "t422-linux-epoch-root" {
		return
	}
	unix.CloseOnExec(3)
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochSessionFixedChild$", "--", "t422-linux-epoch-leaf")
	command.Env = []string{"GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	if command.Start() != nil {
		os.Exit(20)
	}
	report := os.NewFile(3, "fixed-session-report")
	if _, err := fmt.Fprintln(report, command.Process.Pid); err != nil {
		os.Exit(21)
	}
	_ = report.Close()
	os.Exit(0)
}
func TestLinuxEpochSessionPinsSurvivingDescendant(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()
	shellPath, err := filepath.EvalSymlinks("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	shell, err := os.Open(shellPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shell.Close() }()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochSessionFixedChild$", "--", "t422-linux-epoch-root")
	command.ExtraFiles = []*os.File{writer}
	command.Env = []string{"GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pidfd, pinErr := pinExecutionRootDescriptor(command)
	joined := false
	defer func() {
		if pidfd >= 0 {
			_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		}
		if !joined {
			_ = command.Wait()
		}
		if pidfd >= 0 {
			_ = unix.Close(pidfd)
		}
	}()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	leaf, parseErr := strconv.Atoi(line[:max(0, len(line)-1)])
	if err != nil || parseErr != nil || leaf <= 0 || pinErr != nil {
		t.Fatal("fixed child report/pin", err, parseErr, pinErr)
	}
	leafFD, err := unix.PidfdOpen(leaf, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.PidfdSendSignal(leafFD, unix.SIGKILL, nil, 0); _ = unix.Close(leafFD) }()
	namespace, err := os.Stat("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	held := &linuxEpochHeldFlow{controllerPID: os.Getpid(), controllerImage: image, shellImage: shell, pidNamespace: namespace}
	for i := range held.images {
		held.images[i] = shell
	}
	for i := range held.gitImages {
		held.gitImages[i] = shell
	}
	run := &ExecutionEpochOneRun{command: command}
	server := &linuxEpochServer{pidfd: pidfd}
	held.servers = map[*ExecutionEpochOneRun]*linuxEpochServer{run: server}
	authority, err := captureLinuxEpochSession(held, run, server)
	if err != nil {
		t.Fatal("exact unreaped root anchor unavailable", err)
	}
	var information unix.Siginfo
	for {
		err = unix.Waitid(unix.P_PIDFD, pidfd, &information, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			break
		}
	}
	if err != nil || information.Signo != int32(unix.SIGCHLD) || command.ProcessState != nil {
		t.Fatal("kernel exit observation substituted for actual Wait", err)
	}
	authority.exited = true
	// Unissued or wrong-birth observations cannot signal the surviving leaf.
	for _, invalid := range []*linuxEpochSessionAuthority{nil, {}, {held: held, run: run, server: server, pid: authority.pid, pidfd: authority.pidfd, birth: authority.birth, exited: true}} {
		if empty, err := held.stopSession(ctx, invalid); empty || err == nil {
			t.Fatal("unissued session anchor admitted")
		}
	}
	beforeBirth := authority.birth
	authority.birth = "1"
	if empty, err := held.stopSession(ctx, authority); empty || err == nil {
		t.Fatal("wrong-birth anchor admitted")
	}
	authority.birth = beforeBirth
	ownedPID := authority.pid
	authority.pid = os.Getpid()
	if empty, err := held.stopSession(ctx, authority); empty || err == nil {
		t.Fatal("foreign session selector replaced the original root")
	}
	authority.pid = ownedPID
	leafPoll := []unix.PollFd{{Fd: int32(leafFD), Events: unix.POLLIN}}
	if n, err := unix.Poll(leafPoll, 0); err != nil || n != 0 {
		t.Fatal("anchor refusal signaled a live leaf", n, err)
	}
	// Root exit is observed without reaping. Its reserved PID anchors the SID
	// until the remaining descendant has actually exited and the census is empty.
	count, err := t4013.PrivateProcessSessionMembers(command.Process.Pid)
	if err != nil || count != 1 {
		t.Fatal("surviving descendant missing", count, err)
	}
	for {
		empty, err := held.stopSession(ctx, authority)
		if empty && err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("session did not drain under unreaped root anchor")
		}
		time.Sleep(10 * time.Millisecond)
	}
	polls := []unix.PollFd{{Fd: int32(leafFD), Events: unix.POLLIN}}
	if n, err := unix.Poll(polls, 0); err != nil || n != 1 || polls[0].Revents&unix.POLLIN == 0 {
		t.Fatal("signaling substituted for descendant exit", n, err)
	}
	if err := command.Wait(); err != nil {
		joined = true
		t.Fatal("actual root Wait", err)
	}
	joined = true
	if empty, err := held.stopSession(ctx, authority); empty || err == nil {
		t.Fatal("reaped root retained SID signal authority")
	}
}
