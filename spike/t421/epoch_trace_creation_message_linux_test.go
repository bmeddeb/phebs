//go:build linux

package t421

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const creationMessageFixtureEnv = "PHEBS_PRIVATE_CREATION_MESSAGE_FIXTURE"

func TestLinuxEpochTraceCreationMessageFixture(t *testing.T) {
	if os.Getenv(creationMessageFixtureEnv) == "" {
		return
	}
	for range 4 {
		go func() { runtime.LockOSThread(); select {} }()
	}
	select {}
}

// A real creator SIGKILLed while held in its CLONE stop leaves that stop
// without our restart; its event message then belongs to the EXIT stop and
// must not be read as a new task.
func TestLinuxEpochTraceCreationMessageLeavesOnSIGKILL(t *testing.T) {
	runtime.LockOSThread() // Never unlocked: the tracer thread ends with this goroutine.
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochTraceCreationMessageFixture$", "-test.timeout=20s")
	command.Env = []string{creationMessageFixtureEnv + "=1", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Pdeathsig: syscall.SIGKILL}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := command.Process.Pid
	killed := false
	defer func() {
		if !killed {
			_ = unix.Kill(pid, unix.SIGKILL)
		}
		creationMessageReap(t, pid)
		_ = command.Wait()
	}()
	deadline := time.Now().Add(10 * time.Second)
	stop := func() unix.WaitStatus {
		t.Helper()
		for time.Now().Before(deadline) {
			var status unix.WaitStatus
			got, err := unix.Wait4(pid, &status, unix.WALL|unix.WNOHANG, nil)
			if err != nil && err != unix.EINTR {
				t.Fatal("wait leader", err)
			}
			if got == pid {
				if !status.Stopped() {
					t.Fatal("leader left before its stops", status)
				}
				return status
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("leader stop deadline")
		return 0
	}
	if status := stop(); status.StopSignal() != unix.SIGTRAP {
		t.Fatal("exec stop", status)
	}
	if err := unix.PtraceSetOptions(pid, unix.PTRACE_O_TRACECLONE|unix.PTRACE_O_TRACEEXIT); err != nil {
		t.Fatal(err)
	}
	if err := unix.PtraceCont(pid, 0); err != nil {
		t.Fatal(err)
	}
	for {
		status := stop()
		if status.TrapCause() == unix.PTRACE_EVENT_CLONE {
			break
		}
		signal := 0
		if status.TrapCause() <= 0 && status.StopSignal() != unix.SIGTRAP && status.StopSignal() != unix.SIGSTOP {
			signal = int(status.StopSignal()) // Runtime signals (e.g. SIGURG) are delivered.
		}
		if err := unix.PtraceCont(pid, signal); err != nil {
			t.Fatal(err)
		}
	}
	child, left, err := linuxEpochTraceCreationMessage(pid, unix.PTRACE_EVENT_CLONE)
	if err != nil || left || child == 0 {
		t.Fatal("held CLONE stop message", child, left, err)
	}
	if _, err = os.Stat("/proc/" + strconv.Itoa(pid) + "/task/" + strconv.FormatUint(uint64(child), 10)); err != nil {
		t.Fatal("message names no new thread of the creator", child, err)
	}
	if _, left, err = linuxEpochTraceCreationMessage(pid, unix.PTRACE_EVENT_FORK); err != nil || !left {
		t.Fatal("a CLONE stop was accepted as a FORK stop", left, err)
	}
	if err = unix.Kill(pid, unix.SIGKILL); err != nil {
		t.Fatal(err)
	}
	killed = true
	if status := stop(); status.TrapCause() != unix.PTRACE_EVENT_EXIT {
		t.Fatal("SIGKILL did not move the held creator to its EXIT stop", status)
	}
	stale, _ := unix.PtraceGetEventMsg(pid)
	child, left, err = linuxEpochTraceCreationMessage(pid, unix.PTRACE_EVENT_CLONE)
	if err != nil || !left || child != 0 {
		t.Fatal("left CLONE stop still trusted", child, left, err, "raw message", stale)
	}
}

// Every task of the killed fixture is our tracee. Continue each stop (the
// leader's EXIT stop was already consumed above) and reap only its non-leader
// TIDs, never another test's children; the caller's Wait reaps the leader.
func creationMessageReap(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/task")
		if err != nil || len(entries) == 0 || len(entries) == 1 && entries[0].Name() == strconv.Itoa(pid) {
			_ = unix.PtraceCont(pid, 0)
			return
		}
		for _, entry := range entries {
			tid, e := strconv.Atoi(entry.Name())
			if e != nil {
				continue
			}
			_ = unix.PtraceCont(tid, 0) // ESRCH unless held in a consumed stop.
			if tid == pid {
				continue
			}
			var status unix.WaitStatus
			if got, _ := unix.Wait4(tid, &status, unix.WALL|unix.WNOHANG, nil); got == tid && status.Stopped() {
				_ = unix.PtraceCont(tid, 0)
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("killed fixture tasks not retired")
}
