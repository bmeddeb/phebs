//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

// Fixed host-fixture mechanics only. Isolated Python uses exactly one worker;
// native raise and pthread_exit release the GIL. Leader-only pthread_exit does
// not make a natural exit_group0 claim. The actual final signal must be SIGKILL.
const linuxEpochTraceSignalProgram = `import ctypes, os, signal, sys, threading
if threading.get_native_id() != os.getpid():
    os._exit(125)
libc = ctypes.CDLL(None)
native_raise = getattr(libc, "raise")
native_raise.argtypes = [ctypes.c_int]
native_raise.restype = ctypes.c_int
native_exit = libc.pthread_exit
native_exit.argtypes = [ctypes.c_void_p]
native_exit.restype = None
ready = threading.Event()
worker_tid = 0
def worker():
    global worker_tid
    worker_tid = threading.get_native_id()
    print("TRACE_SIGNAL_WORKER", os.getpid(), worker_tid, flush=True)
    ready.set()
    native_raise(signal.SIGUSR1)
    os._exit(126)
threading.Thread(target=worker, name="trace-worker", daemon=True).start()
if not ready.wait(3):
    os._exit(125)
tasks = sorted(int(x) for x in os.listdir("/proc/self/task"))
if tasks != sorted([os.getpid(), worker_tid]) or worker_tid == os.getpid():
    os._exit(125)
print("TRACE_SIGNAL_READY", os.getpid(), worker_tid, len(tasks), flush=True)
if sys.stdin.buffer.read(1) != b"X":
    os._exit(125)
native_exit(None)
os._exit(125)
`

// This enters below production resource/image issuance. The installed host
// interpreter is held only as a mechanical identity; no Python execution or
// observation issues production tool, resource, or operational admission.
func TestLinuxEpochTraceNativeReconcilesOwnedSignalStop(t *testing.T) {
	runtime.LockOSThread()
	started, waited, released := false, false, false
	defer func() {
		if started && !released {
			t.Error("unknown native signal fixture retained on creating task; genuineWaitReturned=", waited)
			for {
				time.Sleep(linuxEpochTraceFallback)
			}
		}
		runtime.UnlockOSThread()
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	settlement, settleCancel := context.WithCancel(context.Background())
	defer func() {
		if started && !released {
			t.Error("retaining live DA settlement and interpreter/pipe/trace custody")
			for {
				time.Sleep(linuxEpochTraceFallback)
			}
		}
		settleCancel()
	}()
	controller, err := dispatchadmission.New(settlement, dispatchadmission.Config{
		Limits:    dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1, Attempts: 1, WireBytes: 16 << 10, AckTimeout: 2 * time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{ExecutionParentAuthorSite()}}},
		Phases:    []dispatchadmission.Phase{{ID: 2, Roles: []dispatchadmission.RoleBudget{{Role: executionRoleAuthor, Attempts: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(settlement, 1)
	if err != nil {
		t.Fatal(err)
	}
	parentClosed := false
	defer func() {
		if !started && !parentClosed {
			c, cc := context.WithTimeout(context.Background(), 3*time.Second)
			defer cc()
			if e := parent.Close(c); e != nil {
				t.Error(e)
			}
		}
	}()
	image, err := os.Open("/usr/bin/python3")
	if err != nil {
		t.Fatal("fixed installed interpreter unavailable", err)
	}
	defer func() {
		if !started || released {
			_ = image.Close()
		}
	}()
	self, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !started || released {
			_ = self.Close()
		}
	}()
	namespace, err := os.Stat("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	controllerRow, err := linuxEpochProcessRow(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	held := &linuxEpochHeldFlow{controllerPID: os.Getpid(), controllerStart: controllerRow.StartIdentity, controllerImage: self, shellImage: image, pidNamespace: namespace}
	for i := range held.images {
		held.images[i] = image
	}
	for i := range held.gitImages {
		held.gitImages[i] = image
	}
	admission := &linuxEpochResourceAdmission{held: held, probeGate: make(chan struct{}, 1)}
	held.resources = admission
	owner := allocateLinuxEpochTraceOwner(admission)
	admission.trace = owner
	defer func() {
		if !started || released {
			if e := owner.close(); e != nil {
				t.Error(e)
			}
		}
	}()
	input, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !started || released {
			_ = input.Close()
			_ = release.Close()
		}
	}()
	output, err := os.CreateTemp("", "epoch-trace-signal-output-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained private signal fixture output %s", output.Name())
	defer func() {
		if !started || released {
			_ = output.Close()
		}
	}()
	command := exec.Command("/usr/bin/python3", "-I", "-S", "-B", "-c", linuxEpochTraceSignalProgram)
	command.Env = []string{"LC_ALL=C"}
	command.Stdin, command.Stdout, command.Stderr = input, output, output
	command.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setsid: true}
	handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), command)
	if err != nil {
		t.Fatal(err)
	}
	started = true
	root := &linuxEpochTraceRoot{owner: owner, command: command, handle: handle, tid: unix.Gettid(), pidfd: -1, tasks: make(map[int]*linuxEpochTraceTask), pending: make(map[int]*linuxEpochTraceLeader), exited: make(chan struct{}), wake: make(chan struct{}, 1)}
	if !root.tryGate() {
		t.Fatal("empty private census gate unavailable")
	}
	root.pidfd, err = unix.PidfdOpen(command.Process.Pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.initial(ctx); err != nil {
		t.Fatal(err)
	}
	root.original, err = linuxEpochTraceIdentityOf(command.Process.Pid)
	if err != nil || root.original.tracer != root.tid || root.original.parent != os.Getpid() || root.original.pid != root.original.sid {
		t.Fatal("actual initial interpreter binding", root.original, err)
	}
	if err = root.allowedImage(ctx, root.original.pid, image); err != nil {
		t.Fatal("actual interpreter differs from held file", err)
	}
	if err = unix.PtraceSetOptions(root.original.pid, linuxEpochTraceOptions); err != nil {
		t.Fatal(err)
	}
	if err = root.publishRoot(); err != nil {
		t.Fatal(err)
	}
	if err = unix.PtraceCont(root.original.pid, 0); err != nil {
		t.Fatal(err)
	}
	root.continued = true
	worker, birth := 0, ""
	for worker == 0 {
		if ctx.Err() != nil {
			t.Fatal("actual worker signal stop unavailable", ctx.Err())
		}
		reported, ready, e := linuxEpochTraceSignalReport(output.Name(), root.original.pid)
		if e != nil {
			t.Fatal(e)
		}
		ids := make([]int, 0, len(root.tasks))
		for tid := range root.tasks {
			ids = append(ids, tid)
		}
		sort.Ints(ids)
		for _, tid := range ids {
			task := root.tasks[tid]
			peek, e := linuxEpochTraceWait(tid, unix.WSTOPPED|unix.WNOWAIT)
			if errors.Is(e, unix.EINTR) {
				continue
			}
			if e != nil || peek.signo != 0 && (peek.code != 4 || peek.pid != int32(tid) || peek.uid != uint32(os.Getuid())) {
				t.Fatal("targeted original stop peek", peek, e)
			}
			if peek.signo == 0 {
				continue
			}
			if int(peek.status)>>8 == 0 && int(peek.status)&255 == int(unix.SIGUSR1) {
				if !ready {
					continue // Preserve the real stop while main publishes its report.
				}
				fresh, e := linuxEpochTraceIdentityOf(tid)
				if e != nil || reported != tid || !task.original.sameLifetime(fresh) || fresh.pid == fresh.tgid || fresh.tgid != root.original.pid || fresh.tracer != root.tid || fresh.state != "t" {
					t.Fatal("actual held signal worker binding", reported, fresh, e)
				}
				if e = root.allowedImage(ctx, tid, image); e != nil {
					t.Fatal(e)
				}
				linuxEpochTraceSignalTwoTasks(t, root.original.pid, tid)
				worker, birth = tid, fresh.birth
				// Mechanical missing-record control only. WNOWAIT preserved the
				// actual owned delivery stop for the production wrapper to consume.
				root.removeTask(tid)
				break
			}
			consumed, e := linuxEpochTraceWait(tid, unix.WSTOPPED)
			if e != nil || consumed != peek {
				t.Fatal("exact original stop consumption", consumed, peek, e)
			}
			if e = root.stopped(ctx, tid, task, consumed); e != nil {
				t.Fatal(e)
			}
		}
		time.Sleep(time.Millisecond)
	}
	if n, e := release.Write([]byte{'X'}); e != nil || n != 1 {
		t.Fatal("fixed owned main-thread release", n, e)
	}
	partial, signalBranch, terminal := false, false, false
	for !terminal {
		if ctx.Err() != nil {
			t.Fatal("original group completion unavailable", ctx.Err())
		}
		terminal, err = root.turn(ctx)
		if err != nil {
			t.Fatal("actual signal fixture event turn", err)
		}
		leader := owner.leaders[root.original.pid]
		if !partial && leader != nil && leader.exiting && len(root.tasks) == 1 {
			anchor, inspect, e := root.exitingGroupAnchor(ctx, leader)
			if e != nil {
				t.Fatal(e)
			}
			if inspect {
				linuxEpochTraceSignalTwoTasks(t, root.original.pid, worker)
				fresh, e := linuxEpochTraceIdentityOf(worker)
				if e != nil || fresh.birth != birth || !root.reconcilableThread(leader, anchor, fresh) {
					t.Fatal("fresh live-mm worker under original Z leader", fresh, e)
				}
				if e = root.allowedImage(ctx, worker, image); e != nil {
					t.Fatal(e)
				}
				peek, e := linuxEpochTraceWait(worker, unix.WSTOPPED|unix.WNOWAIT)
				if e != nil || peek.signo != int32(unix.SIGCHLD) || peek.code != 4 || peek.pid != int32(worker) || peek.uid != uint32(os.Getuid()) || peek.status != int32(unix.SIGUSR1) {
					t.Fatal("preserved actual SIGUSR1 delivery stop", peek, e)
				}
				if _, e = root.terminalProof(ctx, leader); e == nil {
					t.Fatal("partial Z leader gained actual group-terminal authority")
				}
				partial = true
			}
		}
		before := root.events
		if err = root.reconcileExitingGroups(ctx); err != nil {
			t.Fatal("production current-group reconciliation wrapper", err)
		}
		if partial && root.events == before+1 {
			event := root.recent[before%linuxEpochTraceRecentEvents]
			task := root.tasks[worker]
			if event.tid == worker && event.group == root.original.pid && event.birth == birth && event.event == 0 && task != nil && task.reconciled {
				signalBranch = true // Successful wrapper return includes actual CONT(SIGKILL).
			}
		}
		time.Sleep(time.Millisecond)
	}
	if !root.exitSeen || len(root.tasks) != 1 || len(root.pending) != 0 {
		t.Fatal("actual original terminal custody unavailable", root.exitSeen, len(root.tasks), len(root.pending))
	}
	if !partial || !signalBranch {
		t.Error("required real event0 branch not proven", partial, signalBranch)
	}
	if empty, e := root.sessionEmpty(ctx); e != nil || !empty {
		t.Fatal("actual original anchored session empty", empty, e)
	}
	waitErr := handle.Wait()
	waited = true
	status, validStatus := syscall.WaitStatus(0), false
	if command.ProcessState != nil {
		status, validStatus = command.ProcessState.Sys().(syscall.WaitStatus)
	}
	if !linuxEpochTraceSignalWaitOnly(waitErr, command.ProcessState) || !validStatus || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Error("original genuine Wait must report actual SIGKILL", status, validStatus, waitErr)
	}
	if linuxEpochTraceSignalWaitOnly(errors.Join(waitErr, ErrExecutionEpochOne), command.ProcessState) || linuxEpochTraceSignalWaitOnly(errors.Join(waitErr), command.ProcessState) {
		t.Error("extra settlement error or unexpected nested Wait shape was excused")
	}
	mask, err := linuxEpochTracePoll(ctx, root.pidfd)
	if err != nil || mask&unix.POLLHUP == 0 {
		t.Fatal("original pidfd actual reap", mask, err)
	}
	delete(owner.leaders, root.original.pid)
	root.removeTask(root.original.pid)
	if err = unix.Close(root.pidfd); err != nil {
		t.Fatal(err)
	}
	root.releaseGate()
	if err = owner.close(); err != nil {
		t.Fatal("actual notifier join", err)
	}
	count, err := parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 {
		t.Fatal("genuine original DA settlement", count, err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	err = parent.Close(closeCtx)
	closeCancel()
	if err != nil {
		t.Fatal("actual local producer close", err)
	}
	parentClosed, released = true, true
	t.Logf("original=%d worker=%d birth=%s exactly2tasks=true partialZRefused=%t consumedEvent=0 signal=SIGUSR1 reconciliationBranch=signal-kill-cont branchProven=%t actualWaitStatus=%d actualSignal=%v waitError=%v originalHUP=true SIDempty=true DAOrdinal=%d DAActive=%d", root.original.pid, worker, birth, partial, signalBranch, status, status.Signal(), waitErr, count.Ordinal, count.Active)
}

func linuxEpochTraceSignalTwoTasks(t *testing.T, leader, worker int) {
	t.Helper()
	ids, err := linuxEpochTraceGroupEntries(leader, 2)
	sort.Ints(ids)
	expected := []int{leader, worker}
	sort.Ints(expected)
	if err != nil || len(ids) != 2 || ids[0] != expected[0] || ids[1] != expected[1] {
		t.Fatal("actual fixed two-thread group required", ids, expected, err)
	}
}

func linuxEpochTraceSignalReport(path string, leader int) (int, bool, error) {
	raw, err := linuxEpochTraceRead(path, 4096)
	if err != nil {
		return 0, false, err
	}
	// A regular-file read need not coincide with Python's multi-part print.
	// Only complete records can assert readiness; a bounded tail stays pending.
	end := strings.LastIndexByte(string(raw), '\n')
	if end < 0 {
		return 0, false, nil
	}
	worker, ready := 0, false
	for _, line := range strings.Split(string(raw[:end]), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "TRACE_SIGNAL_WORKER", "TRACE_SIGNAL_READY":
			if len(fields) != 3 && len(fields) != 4 || fields[1] != strconv.Itoa(leader) {
				return 0, false, ErrExecutionEpochOne
			}
			tid, e := strconv.Atoi(fields[2])
			if e != nil || tid <= 0 || tid == leader || worker != 0 && worker != tid {
				return 0, false, ErrExecutionEpochOne
			}
			worker = tid
			if fields[0] == "TRACE_SIGNAL_READY" {
				if len(fields) != 4 || fields[3] != "2" || ready {
					return 0, false, ErrExecutionEpochOne
				}
				ready = true
			} else if len(fields) != 3 {
				return 0, false, ErrExecutionEpochOne
			}
		}
	}
	return worker, ready, nil
}

// Genuine LocalProducer Handle.Wait joins exactly native Wait and settlement.
// The one expected native failure may not conceal any extra accounting error.
func linuxEpochTraceSignalWaitOnly(err error, state *os.ProcessState) bool {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || state == nil {
		return false
	}
	parts := joined.Unwrap()
	if len(parts) != 1 {
		return false
	}
	exit, ok := parts[0].(*exec.ExitError)
	if !ok || exit.ProcessState != state {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}
