//go:build linux

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

const rootFirstGroupFixtureEnv = "PHEBS_PRIVATE_ROOT_FIRST_DYING_GROUP"

type rootFirstGroupReport struct {
	Child, Creator, Held int
}

// Both groups use the actual Go runtime. The child retains its parent-death
// SIGKILL; no production subreaper or parent-death exception is introduced.
func TestLinuxEpochRootFirstDyingGroupFixture(t *testing.T) {
	mode := os.Getenv(rootFirstGroupFixtureEnv)
	if mode == "" {
		return
	}
	runtime.LockOSThread()
	if mode == "child" {
		start := make(chan os.Signal, 1)
		signal.Notify(start, unix.SIGUSR1)
		defer signal.Stop(start)
		_, _ = fmt.Fprintln(os.Stdout, "ROOT_FIRST_CHILD_READY")
		<-start
		for range 8 {
			go func() { runtime.LockOSThread(); select {} }()
		}
		select {}
	}
	if mode != "parent" {
		t.Fatal("unknown fixed fixture mode")
	}
	held := make(chan int, 1)
	go func() { runtime.LockOSThread(); held <- unix.Gettid(); select {} }()
	report := rootFirstGroupReport{Creator: unix.Gettid(), Held: <-held}
	child := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochRootFirstDyingGroupFixture$", "-test.timeout=20s")
	child.Env = []string{rootFirstGroupFixtureEnv + "=child", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	child.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	report.Child = child.Process.Pid
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "ROOT_FIRST_PARENT "+string(raw))
	_ = child.Wait() // This original application Wait must not be invented after its death.
}

// A separate control process is the test-only adopter. It withholds one real
// child CLONE notification (as the existing dying-group control does), and one
// known root sibling's actual EXIT stop. Neither pidfd readiness nor wait facts
// are fabricated. Root Z/pidfd0 must mean pending, not terminal or zero RSS.
func TestLinuxEpochRootFirstDyingGroupPending(t *testing.T) {
	const isolated = "PHEBS_PRIVATE_ROOT_FIRST_DYING_GROUP_CONTROL"
	if os.Getenv(isolated) == "" {
		command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochRootFirstDyingGroupPending$", "-test.timeout=25s", "-test.v")
		command.Env = []string{isolated + "=1", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
		output, err := command.CombinedOutput()
		t.Logf("isolated root/group control:\n%s", output)
		if err != nil || command.ProcessState == nil || !command.ProcessState.Success() {
			t.Fatal("isolated root/group genuine Wait", err)
		}
		return
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	started, released := false, false
	defer func() {
		if started && !released {
			t.Error("unknown root/group control retains all owners")
			for {
				time.Sleep(linuxEpochTraceFallback)
			}
		}
		runtime.UnlockOSThread()
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	settlement, settleCancel := context.WithCancel(context.Background())
	defer settleCancel()
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
	defer func() {
		if !started || released {
			if e := parent.Close(ctx); e != nil {
				t.Error(e)
			}
		}
	}()
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !started || released {
			_ = image.Close()
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
	held := &linuxEpochHeldFlow{controllerPID: os.Getpid(), controllerStart: controllerRow.StartIdentity, controllerImage: image, shellImage: image, pidNamespace: namespace}
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
	output, err := os.CreateTemp("", "epoch-root-group-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("private raw control %s", output.Name())
	defer func() {
		if !started || released {
			_ = output.Close()
		}
	}()
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochRootFirstDyingGroupFixture$", "-test.timeout=20s")
	command.Env = []string{rootFirstGroupFixtureEnv + "=parent", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.Stdout, command.Stderr = output, output
	command.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setsid: true, Pdeathsig: syscall.SIGKILL}
	handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), command)
	if err != nil {
		t.Fatal(err)
	}
	started = true
	root := &linuxEpochTraceRoot{owner: owner, command: command, handle: handle, tid: unix.Gettid(), pidfd: -1, tasks: make(map[int]*linuxEpochTraceTask), pending: make(map[int]*linuxEpochTraceLeader), exited: make(chan struct{}), wake: make(chan struct{}, 1)}
	if !root.tryGate() {
		t.Fatal("private gate unavailable")
	}
	root.pidfd, err = unix.PidfdOpen(command.Process.Pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.initial(ctx); err != nil {
		t.Fatal(err)
	}
	root.original, err = linuxEpochTraceIdentityOf(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.allowedImage(ctx, root.original.pid, image); err != nil {
		t.Fatal(err)
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
	var report rootFirstGroupReport
	omitted := 0
	var heldStop linuxEpochTraceSiginfo
	// The fixed current task set is read only by this same creating actor.
	stops := func(withholdClone, withholdExit bool) {
		t.Helper()
		ids := make([]int, 0, len(root.tasks))
		for tid := range root.tasks {
			ids = append(ids, tid)
		}
		sort.Ints(ids)
		for _, tid := range ids {
			task := root.tasks[tid]
			stop, e := linuxEpochTraceWait(tid, unix.WSTOPPED)
			if errors.Is(e, unix.EINTR) || errors.Is(e, unix.ECHILD) {
				continue
			}
			if e != nil {
				t.Fatal("owned stop", tid, e)
			}
			if stop.signo == 0 {
				continue
			}
			if stop.code != 4 || stop.pid != int32(tid) || stop.uid != uint32(os.Getuid()) {
				t.Fatal("owned stop identity", stop)
			}
			if withholdClone && omitted == 0 && task.original.tgid == report.Child && int(stop.status)>>8 == unix.PTRACE_EVENT_CLONE {
				pid, e := unix.PtraceGetEventMsg(tid)
				identity, ie := linuxEpochTraceIdentityOf(int(pid))
				if e != nil || ie != nil || identity.pid == identity.tgid || identity.tgid != report.Child || identity.tracer != root.tid {
					t.Fatal("actual omitted child thread", identity, e, ie)
				}
				omitted = int(pid)
				if e = unix.PtraceCont(tid, 0); e != nil {
					t.Fatal(e)
				}
				continue // Its actual initial stop remains held, never fabricated.
			}
			if withholdExit && tid == report.Held && int(stop.status)>>8 == unix.PTRACE_EVENT_EXIT {
				heldStop = stop
				continue // Registered known root sibling remains held and charged.
			}
			if e = root.stopped(ctx, tid, task, stop); e != nil {
				t.Fatal("production stop dispatcher", tid, e)
			}
		}
	}
	for {
		if ctx.Err() != nil {
			t.Fatal("real nested group fixture", ctx.Err())
		}
		raw, e := linuxEpochTraceRead(output.Name(), 8192)
		if e != nil {
			t.Fatal(e)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "ROOT_FIRST_PARENT ") {
				if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "ROOT_FIRST_PARENT ")), &report); e != nil {
					t.Fatal(e)
				}
			}
		}
		if report.Child != 0 && strings.Contains(string(raw), "ROOT_FIRST_CHILD_READY\n") {
			break
		}
		if _, e = root.turn(ctx); e != nil {
			t.Fatal("production fixture startup", e)
		}
		time.Sleep(time.Millisecond)
	}
	if report.Held == report.Creator || report.Held == root.original.pid || root.tasks[report.Held] == nil || owner.leaders[report.Child] == nil {
		t.Fatal("known original root sibling/child missing", report)
	}
	if err = unix.PidfdSendSignal(owner.leaders[report.Child].fd, unix.SIGUSR1, nil, 0); err != nil {
		t.Fatal("release actual child late-thread creation", err)
	}
	for omitted == 0 {
		if ctx.Err() != nil {
			t.Fatal("actual late child thread", ctx.Err())
		}
		stops(true, false)
		time.Sleep(time.Millisecond)
	}
	// Complete this real newborn's initial stop with the exact held image,
	// without delivering its deliberately withheld creation notification to
	// the production task map. Its later fatal EXIT must then remain a real
	// unread kernel stop until group reconciliation consumes it.
	initial := rootFirstKernelStop(t, ctx, omitted)
	if initial.status != int32(unix.SIGSTOP) {
		t.Fatal("actual omitted thread initial stop", initial)
	}
	if err = root.allowedImage(ctx, omitted, image); err != nil {
		t.Fatal("omitted current held image", err)
	}
	if err = unix.PtraceCont(omitted, 0); err != nil {
		t.Fatal("fixed fixture thread continuation", err)
	}
	for len(root.pending) != 0 {
		for pid, leader := range root.pending {
			current, e := linuxEpochTraceIdentityOf(pid)
			t.Logf("prekill pending pid=%d child=%d original=%+v current=%+v read=%v", pid, report.Child, leader.original, current, e)
		}
		if err = root.retireReaped(ctx); err != nil {
			t.Fatal("production startup reap retirement", err)
		}
		if ctx.Err() != nil {
			t.Fatal("startup reap deadline", ctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	if owner.leaders[report.Child] == nil {
		t.Fatal("registered child disappeared before selected kill")
	}
	if err = root.killOriginalRoot(ctx, ctx); err != nil {
		t.Fatal("actual source-selected root kill", err)
	}
	child := owner.leaders[report.Child]
	for {
		if ctx.Err() != nil {
			t.Fatal("actual pending root/group state", ctx.Err())
		}
		if heldStop.signo == 0 {
			stops(false, true)
		} else if _, err = root.turn(ctx); err != nil {
			t.Fatal("production pending turn with known EXIT held", err)
		}
		identity, e := linuxEpochTraceIdentityOf(root.original.pid)
		mask, pe := linuxEpochTracePoll(ctx, root.pidfd)
		siblings := false
		for tid, task := range root.tasks {
			if tid != report.Child && task.original.tgid == report.Child {
				siblings = true
			}
		}
		if e == nil && pe == nil && identity.state == "Z" && mask == 0 && heldStop.signo != 0 && child.exiting && !siblings {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if root.exitSeen || root.hardAnchorProven || command.ProcessState != nil {
		t.Fatal("pending state acquired terminal authority")
	}
	if _, e := root.terminalProof(ctx, owner.leaders[root.original.pid]); e == nil || !strings.Contains(e.Error(), "terminal/pidfd mask=0") {
		t.Fatal("actual partial root terminal proof", e)
	}
	if _, _, e := root.exitingGroupAnchor(ctx, child); e == nil || !strings.Contains(e.Error(), "terminal/pidfd mask=0") {
		t.Fatal("exact pending parent-anchor route", e)
	}
	beforeTasks, beforeLeaders, beforeTIDs := len(root.tasks), len(owner.leaders), owner.tids
	pendingErr := root.reconcileExitingGroups(ctx)
	if len(root.tasks) != beforeTasks || len(owner.leaders) != beforeLeaders || owner.tids != beforeTIDs || root.exitSeen || root.hardAnchorProven || root.unknown {
		t.Fatal("pending reconciliation changed ownership/terminal authority")
	}
	// Negative controls retain the actual held kernel state, not synthetic rows.
	originalBirth := owner.leaders[root.original.pid].original.birth
	owner.leaders[root.original.pid].original.birth += "x"
	if _, e := root.terminalProof(ctx, owner.leaders[root.original.pid]); e == nil {
		t.Error("wrong root birth accepted")
	}
	owner.leaders[root.original.pid].original.birth = originalBirth
	delete(owner.leaders, report.Child)
	if _, _, e := root.exitingGroupAnchor(ctx, child); e == nil {
		t.Error("missing child leader accepted")
	}
	owner.leaders[report.Child] = child
	if err = root.stopped(ctx, report.Held, root.tasks[report.Held], heldStop); err != nil {
		t.Fatal("release real root sibling EXIT through production", err)
	}
	for !root.exitSeen {
		if ctx.Err() != nil {
			t.Fatal("actual root terminal completion", ctx.Err())
		}
		if _, err = root.turn(ctx); err != nil {
			t.Fatal("released root turn", err)
		}
		time.Sleep(time.Millisecond)
	}
	for len(root.tasks) != 1 || len(root.pending) != 0 {
		if ctx.Err() != nil {
			t.Fatal("actual nested group terminal completion", ctx.Err())
		}
		if _, err = root.turn(ctx); err != nil {
			t.Fatal("nested group turn", err)
		}
		if err = root.retireReaped(ctx); err != nil {
			t.Fatal(err)
		}
		if err = root.reconcileExitingGroups(ctx); err != nil {
			t.Fatal("post-anchor reconciliation", err)
		}
		if err = root.signalLeaves(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if empty, e := root.sessionEmpty(ctx); e != nil || !empty {
		t.Fatal("actual anchored empty session", empty, e)
	}
	waitErr := handle.Wait() // Sole genuine original root Wait, separately from adopter reap.
	var killed *exec.ExitError
	status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
	if !errors.As(waitErr, &killed) || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("genuine root SIGKILL Wait", command.ProcessState, waitErr)
	}
	if mask, e := linuxEpochTracePoll(ctx, root.pidfd); e != nil || mask&unix.POLLHUP == 0 {
		t.Fatal("original root HUP", mask, e)
	}
	delete(owner.leaders, root.original.pid)
	root.removeTask(root.original.pid)
	if err = unix.Close(root.pidfd); err != nil {
		t.Fatal(err)
	}
	root.releaseGate()
	if err = owner.close(); err != nil {
		t.Fatal("actual notifier retirement", err)
	}
	released = true
	count, err := parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 || owner.tids != 0 || len(owner.leaders) != 0 {
		t.Fatal("actual terminal accounting", count, err)
	}
	t.Logf("root_Z_pidfd0=true known_root_EXIT_held=true child_group_registered=true late_child_thread=%d no_premature_terminal=true original_SIGKILL_Wait=true session_empty=true DAOrdinal=%d DAActive=%d pending_error=%v", omitted, count.Ordinal, count.Active, pendingErr)
	if pendingErr != nil {
		t.Error("pending nested reconciliation poisoned root transition", pendingErr)
	}
}
