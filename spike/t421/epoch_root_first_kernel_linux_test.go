//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// Subreaping is confined to a separate test process. The adopter's raw child
// reap below must never be mistaken for the deceased application's Handle.Wait.
func TestLinuxEpochRootFirstKernelControl(t *testing.T) {
	const selector = "^TestLinuxEpochRootFirstKernelControl$"
	const environment = "PHEBS_PRIVATE_ROOT_FIRST_KERNEL_CONTROL"
	if os.Getenv(environment) != "1" {
		command := exec.Command(os.Args[0], "-test.run="+selector, "-test.timeout=20s", "-test.v")
		command.Env = []string{environment + "=1", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
		output, err := command.CombinedOutput()
		t.Logf("isolated kernel control:\n%s", output)
		if err != nil || command.ProcessState == nil || !command.ProcessState.Success() {
			t.Fatal("isolated kernel control genuine Wait", err)
		}
		return
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal("isolated adopter", err)
	}
	runtime.LockOSThread()
	started, rootJoined, childReaped := false, false, false
	defer func() {
		if started && (!rootJoined || !childReaped) {
			t.Error("unknown kernel control custody retained; no cleanup pass")
			for {
				time.Sleep(linuxEpochTraceFallback)
			}
		}
		runtime.UnlockOSThread()
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	shell, err := os.Open("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !started || rootJoined && childReaped {
			if e := shell.Close(); e != nil {
				t.Error("held shell close", e)
			}
		}
	}()
	// One fork, with the inherited shell image held at its actual initial stop.
	// No descendant executes application code before the original root dies.
	command := exec.Command("/bin/sh", "-c", "sleep 30 & wait")
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	command.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	started = true
	rootFD, err := unix.PidfdOpen(command.Process.Pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rootJoined && childReaped {
			if e := unix.Close(rootFD); e != nil {
				t.Error("original root pidfd close", e)
			}
		}
	}()
	root := &linuxEpochTraceRoot{command: command, tid: unix.Gettid(), pidfd: rootFD, gateOwned: true}
	if err = root.initial(ctx); err != nil {
		t.Fatal(err)
	}
	root.original, err = linuxEpochTraceIdentityOf(command.Process.Pid)
	if err != nil || root.original.parent != os.Getpid() || root.original.sid != root.original.pid {
		t.Fatal("original root", root.original, err)
	}
	if err = unix.PtraceSetOptions(root.original.pid, linuxEpochTraceOptions); err != nil {
		t.Fatal(err)
	}
	if err = unix.PtraceCont(root.original.pid, 0); err != nil {
		t.Fatal(err)
	}
	var creation linuxEpochTraceIdentity
	for creation.pid == 0 {
		stop := rootFirstKernelStop(t, ctx, root.original.pid)
		event := int(stop.status) >> 8
		if event != unix.PTRACE_EVENT_FORK && event != unix.PTRACE_EVENT_VFORK && event != unix.PTRACE_EVENT_CLONE {
			t.Fatal("unexpected root creation stop", stop)
		}
		pid, e := unix.PtraceGetEventMsg(root.original.pid)
		if e != nil {
			t.Fatal(e)
		}
		creation, err = linuxEpochTraceIdentityOf(int(pid))
		if err != nil || creation.pid != creation.tgid || creation.parent != root.original.pid || creation.tracer != root.tid || creation.sid != root.original.sid {
			t.Fatal("actual creation identity", creation, err)
		}
	}
	childFD, err := unix.PidfdOpen(creation.pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rootJoined && childReaped {
			if e := unix.Close(childFD); e != nil {
				t.Error("original survivor pidfd close", e)
			}
		}
	}()
	stop := rootFirstKernelStop(t, ctx, creation.pid)
	if stop.status != int32(unix.SIGSTOP) {
		t.Fatal("actual child initial stop", stop)
	}
	if err = unix.PtraceCont(root.original.pid, 0); err != nil {
		t.Fatal(err)
	}
	if err = unix.PidfdSendSignal(rootFD, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	rootFirstKernelTerminal(t, ctx, root.original.pid)
	current, err := linuxEpochTraceIdentityOf(creation.pid)
	if err != nil || current.parent != os.Getpid() || current.parent == creation.parent || current.pid != creation.pid || current.tgid != creation.tgid || current.birth != creation.birth || current.sid != creation.sid || current.tracer != creation.tracer || !os.SameFile(current.namespace, creation.namespace) || creation.sameLifetime(current) {
		t.Fatal("creation identity must stay distinct from current adoption", creation, current, err)
	}
	controller, err := linuxEpochProcessRow(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	root.owner = &linuxEpochTraceOwner{held: &linuxEpochHeldFlow{controllerPID: os.Getpid(), controllerStart: controller.StartIdentity, pidNamespace: creation.namespace}, leaders: make(map[int]*linuxEpochTraceLeader)}
	parent := &linuxEpochTraceLeader{root: root, original: root.original, fd: rootFD, parentBirth: controller.StartIdentity, verified: true}
	child := &linuxEpochTraceLeader{root: root, original: creation, fd: childFD, parentBirth: root.original.birth, verified: true}
	root.owner.leaders[root.original.pid], root.owner.leaders[creation.pid] = parent, child
	if _, err = root.terminalProof(ctx, parent); err != nil {
		t.Fatal("actual dead original root", err)
	}
	if root.liveParent(ctx, child) == nil {
		t.Fatal("dead creation parent accepted as live")
	}
	root.exitSeen = true
	root.tasks = map[int]*linuxEpochTraceTask{root.original.pid: {original: root.original}}
	if root.finalTracerCensus(ctx) == nil {
		t.Fatal("live traced survivor disappeared from final census")
	}
	rows, err := t4013.ObserveProcessTreeRecords(ctx, creation.pid)
	if err != nil || len(rows) != 1 || rows[0].PID != creation.pid || rows[0].ParentPID != current.parent || rows[0].StartIdentity != creation.birth || rows[0].RSSBytes <= 0 {
		t.Fatal("real survivor RSS/current-parent row", rows, err)
	}
	if _, matched := linuxEpochClassifyRow(ctx, rows[0], []linuxEpochClassImage{{class: "shell", file: shell}}); !matched {
		t.Fatal("current held image lost after reparenting")
	}
	waitErr := command.Wait() // Sole genuine exec.Cmd Wait for this direct root.
	rootJoined = true
	var killed *exec.ExitError
	if !errors.As(waitErr, &killed) || command.ProcessState == nil {
		t.Fatal("actual root Wait lost", waitErr)
	}
	status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != unix.SIGKILL {
		t.Fatal("actual root Wait status", status)
	}
	if mask, e := linuxEpochTracePoll(ctx, childFD); e != nil || mask != 0 {
		t.Fatal("root Wait must not retire a live survivor", mask, e)
	}
	if err = unix.PidfdSendSignal(childFD, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	rootFirstKernelTerminal(t, ctx, creation.pid)
	terminal, err := linuxEpochTraceWait(creation.pid, unix.WEXITED)
	if err != nil || !terminal.terminal(creation.pid) || terminal.status != int32(unix.SIGKILL) {
		t.Fatal("isolated adopter's actual reap", terminal, err)
	}
	childReaped = true
	for _, fd := range []int{rootFD, childFD} {
		if mask, e := linuxEpochTracePoll(ctx, fd); e != nil || mask&unix.POLLHUP == 0 {
			t.Fatal("original pidfd reap closure", mask, e)
		}
	}
	t.Logf("creation_parent=%d current_parent=%d survivor_RSS=%d actual_root_SIGKILL_Wait=true survivor_live_after_Wait=true adopter_reap=true original_pidfds_HUP=true", creation.parent, current.parent, rows[0].RSSBytes)
}

func rootFirstKernelStop(t *testing.T, ctx context.Context, pid int) linuxEpochTraceSiginfo {
	t.Helper()
	for ctx.Err() == nil {
		stop, err := linuxEpochTraceWait(pid, unix.WSTOPPED)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatal("owned stop", err)
		}
		if stop.signo != 0 {
			if stop.code != 4 || stop.pid != int32(pid) || stop.uid != uint32(os.Getuid()) {
				t.Fatal("owned stop binding", stop)
			}
			return stop
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("owned stop deadline", ctx.Err())
	return linuxEpochTraceSiginfo{}
}

func rootFirstKernelTerminal(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	for ctx.Err() == nil {
		stop, err := linuxEpochTraceWait(pid, unix.WSTOPPED)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil && !errors.Is(err, unix.ECHILD) {
			t.Fatal("owned terminal stop", err)
		}
		if stop.signo != 0 {
			if stop.code != 4 || stop.pid != int32(pid) || stop.uid != uint32(os.Getuid()) || int(stop.status)>>8 != unix.PTRACE_EVENT_EXIT {
				t.Fatal("unexpected terminal stop", stop)
			}
			if err = unix.PtraceCont(pid, 0); err != nil {
				t.Fatal(err)
			}
		}
		terminal, err := linuxEpochTraceWait(pid, unix.WEXITED|unix.WNOWAIT)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatal("owned terminal observation", err)
		}
		if terminal.terminal(pid) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("owned terminal deadline", ctx.Err())
}
