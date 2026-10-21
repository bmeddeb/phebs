//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

const linuxEpochTraceGroupFixtureEnv = "PHEBS_PRIVATE_EPOCH_TRACE_GROUP_FIXTURE"

// A fixed Go process with an already live runtime, then additional real Go
// threads and a natural exit0. The controller withholds one actual auto-attached
// nonleader's initial stop; no syscall result or terminal status is fabricated.
func TestLinuxEpochTraceGroupFixtureChild(t *testing.T) {
	if os.Getenv(linuxEpochTraceGroupFixtureEnv) != "natural-go" {
		return
	}
	runtime.GOMAXPROCS(4)
	runtime.LockOSThread()
	_, _ = fmt.Fprintln(os.Stdout, "TRACE_GROUP_READY")
	for range 8 {
		go func() {
			runtime.LockOSThread()
			select {}
		}()
	}
	// Keep this original thread runnable independently of a withheld newborn
	// worker. This is bounded fixture work, not a production polling mechanism.
	until := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(until) {
	}
	os.Exit(0)
}

func TestLinuxEpochTraceReconcilesOwnedDyingGroup(t *testing.T) {
	runtime.LockOSThread()
	joined := false
	released := false
	started := false
	defer func() {
		if started && !released {
			t.Error("unknown trace group retained on original creating task; genuineWaitReturned=", joined)
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
			t.Error("retaining live DA settlement and all trace loans on unknown group closure")
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
	defer func() {
		if started && !released {
			return // The outer retaining defer prevents lifetime release.
		}
		c, cc := context.WithTimeout(context.Background(), 3*time.Second)
		defer cc()
		if e := parent.Close(c); e != nil {
			t.Error(e)
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
	defer func() {
		if !started || released {
			if e := owner.close(); e != nil {
				t.Error(e)
			}
		}
	}()
	output, err := os.CreateTemp("", "epoch-trace-group-output-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained group fixture output %s", output.Name())
	defer func() {
		if !started || released {
			_ = output.Close()
		}
	}()
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochTraceGroupFixtureChild$", "-test.timeout=6s")
	command.Env = []string{linuxEpochTraceGroupFixtureEnv + "=natural-go", "GOMAXPROCS=4", "GORACE=atexit_sleep_ms=0"}
	command.Stdout, command.Stderr = output, output
	command.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setsid: true}
	handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), command)
	if err != nil {
		t.Fatal(err)
	}
	started = true
	root := &linuxEpochTraceRoot{owner: owner, command: command, handle: handle, tid: unix.Gettid(), pidfd: -1, tasks: make(map[int]*linuxEpochTraceTask), pending: make(map[int]*linuxEpochTraceLeader), exited: make(chan struct{}), wake: make(chan struct{}, 1)}
	if !root.tryGate() {
		t.Fatal("empty private gate unavailable")
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
		t.Fatal("actual initial own group", root.original, err)
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
	omitted := 0
	omittedBirth := ""
	var detachedStat *os.File
	defer func() {
		if detachedStat != nil && (!started || released) {
			_ = detachedStat.Close()
		}
	}()
	// Before withholding exactly one real CLONE child, share the production
	// stop dispatcher. No fake event, current image, or terminal row is supplied.
	for omitted == 0 {
		if ctx.Err() != nil {
			t.Fatal("no real late thread notification", ctx.Err())
		}
		raw, e := linuxEpochTraceRead(output.Name(), 4096)
		if e != nil {
			t.Fatal(e)
		}
		ready := strings.Contains(string(raw), "TRACE_GROUP_READY\n")
		ids := make([]int, 0, len(root.tasks))
		for tid := range root.tasks {
			ids = append(ids, tid)
		}
		sort.Ints(ids)
		for _, tid := range ids {
			task := root.tasks[tid]
			stop, e := linuxEpochTraceWait(tid, unix.WSTOPPED)
			if errors.Is(e, unix.EINTR) {
				continue
			}
			if e != nil || stop.signo != 0 && (stop.code != 4 || stop.pid != int32(tid) || stop.uid != uint32(os.Getuid())) {
				t.Fatal("real registered stop", stop, e)
			}
			if stop.signo == 0 {
				continue
			}
			if ready && int(stop.status)>>8 == unix.PTRACE_EVENT_CLONE {
				child, ce := unix.PtraceGetEventMsg(tid)
				identity, ie := linuxEpochTraceIdentityOf(int(child))
				if ce != nil || ie != nil || identity.pid == identity.tgid || identity.tgid != root.original.pid || identity.tracer != root.tid {
					t.Fatal("real omitted same-group child", child, identity, ce, ie)
				}
				omitted, omittedBirth = identity.pid, identity.birth
				detachedStat, e = os.Open(fmt.Sprintf("/proc/%d/stat", omitted))
				if e != nil {
					t.Fatal(e)
				}
				if e = unix.PtraceCont(tid, 0); e != nil {
					t.Fatal(e)
				}
				break
			}
			if e = root.stopped(ctx, tid, task, stop); e != nil {
				t.Fatal(e)
			}
		}
		time.Sleep(time.Millisecond)
	}
	sawPartial, reconciled, terminal := false, false, false
	sawDetachedRead := false
	checkDetachedRead := func() {
		if sawDetachedRead || root.detached[omitted].birth != omittedBirth {
			return
		}
		// The actual EXIT dispatcher detached this original nonleader. Hold its
		// previously opened stat through disappearance, before any root Wait.
		for {
			_, e := os.Stat(fmt.Sprintf("/proc/%d/stat", omitted))
			if errors.Is(e, os.ErrNotExist) {
				break
			}
			if e != nil || ctx.Err() != nil {
				t.Fatal("witnessed thread did not disappear", e, ctx.Err())
			}
			time.Sleep(time.Millisecond)
		}
		var one [1]byte
		if _, e := detachedStat.Read(one[:]); !errors.Is(e, unix.ESRCH) {
			t.Fatal("held real proc stat must return read-time ESRCH", e)
		}
		if _, gone, e := linuxEpochTraceDetachedRead(detachedStat); e != nil || !gone {
			t.Fatal("real read-time disappearance must retire only metadata", gone, e)
		}
		detachedStat = nil // Production reader closed this actual descriptor.
		beforeTIDs, beforeWitnesses := owner.tids, len(root.detached)
		beforeTasks, beforeLeaders, beforeEvents := len(root.tasks), len(owner.leaders), root.events
		leader, exitSeen := owner.leaders[root.original.pid], root.exitSeen
		if e := root.retireDetached(ctx); e != nil {
			t.Fatal(e)
		}
		if _, present := root.detached[omitted]; present || owner.tids != beforeTIDs-(beforeWitnesses-len(root.detached)) || len(root.tasks) != beforeTasks || len(owner.leaders) != beforeLeaders || root.events != beforeEvents || owner.leaders[root.original.pid] != leader || leader == nil || root.exitSeen != exitSeen || joined {
			t.Fatal("metadata retirement changed live ownership or terminal authority")
		}
		sawDetachedRead = true
	}
	consumedEvent := -1
	reconciliationBranch := "not-observed"
	for !terminal {
		if ctx.Err() != nil {
			t.Fatal("actual group completion unavailable", ctx.Err())
		}
		terminal, err = root.turn(ctx)
		if err != nil {
			t.Fatal("actual group turn", err)
		}
		checkDetachedRead()
		if !sawPartial {
			leader := owner.leaders[root.original.pid]
			if leader != nil && leader.exiting && len(root.tasks) == 1 {
				anchor, inspect, e := root.exitingGroupAnchor(ctx, leader)
				if e != nil {
					t.Fatal(e)
				}
				if inspect {
					missing, e := linuxEpochTraceIdentityOf(omitted)
					if e != nil || missing.state != "t" || missing.birth != omittedBirth || !root.reconcilableThread(leader, anchor, missing) {
						t.Fatal("actual omitted live-mm sibling", missing, e)
					}
					if _, e = root.terminalProof(ctx, leader); e == nil {
						t.Fatal("Z leader with live stopped sibling gained terminal authority")
					}
					linuxEpochTraceGroupNegativeBindings(t, root, leader, anchor, missing)
					previousBirth := leader.original.birth
					leader.original.birth += "0"
					_, _, birthErr := root.exitingGroupAnchor(ctx, leader)
					leader.original.birth = previousBirth
					if birthErr == nil {
						t.Fatal("changed original leader birth gained cleanup authority")
					}
					sawPartial = true
				}
			}
		}
		beforeEvents := root.events
		if err = root.reconcileExitingGroups(ctx); err != nil {
			t.Fatal("bounded current-thread reconciliation", err)
		}
		// Reconciliation may consume an already pending EXIT stop and transfer
		// its current witness synchronously. An active map slot is not required
		// to survive the call. Bind the actual emitted event to this original
		// omitted thread, and distinguish the exercised kernel branch.
		if root.events-beforeEvents > linuxEpochTraceRecentEvents {
			t.Error("reconciliation event coverage exceeded the retained ring")
		} else {
			for index := beforeEvents; index < root.events; index++ {
				event := root.recent[index%linuxEpochTraceRecentEvents]
				if event.tid != omitted || event.group != root.original.pid || event.birth != omittedBirth {
					continue
				}
				switch event.event {
				case unix.PTRACE_EVENT_EXIT:
					witness, found := root.detached[omitted]
					if found && witness.tgid == root.original.pid && witness.birth == omittedBirth {
						reconciled = true
						consumedEvent, reconciliationBranch = event.event, "exit-detach"
					}
				case 0:
					if task := root.tasks[omitted]; task != nil && task.reconciled && task.original.birth == omittedBirth {
						reconciled = true
						consumedEvent, reconciliationBranch = event.event, "signal-kill-cont"
					}
				}
			}
		}
		checkDetachedRead()
		time.Sleep(time.Millisecond)
	}
	if !root.exitSeen || len(root.tasks) != 1 || len(root.pending) != 0 {
		t.Fatal("actual original terminal ownership unavailable", root.exitSeen, len(root.tasks), len(root.pending))
	}
	if !sawPartial || !reconciled || !sawDetachedRead {
		// Actual terminal ownership is already known: a coverage assertion
		// must not prevent the one genuine Wait and all actual custody joins.
		t.Error("required real partial-group and detached-read branches not proven", sawPartial, reconciled, sawDetachedRead)
	}
	if empty, e := root.sessionEmpty(ctx); e != nil || !empty {
		t.Fatal("anchored actual session completion", empty, e)
	}
	// This is the original application's exact result, not a normalized status.
	waitErr := handle.Wait()
	joined = true
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	if command.ProcessState == nil || exitCode != 0 || waitErr != nil {
		t.Error("natural Go group must retain genuine Wait0", command.ProcessState, waitErr)
	}
	mask, err := linuxEpochTracePoll(ctx, root.pidfd)
	if err != nil || mask&unix.POLLHUP == 0 {
		t.Fatal("actual original process reap", mask, err)
	}
	delete(owner.leaders, root.original.pid)
	root.removeTask(root.original.pid)
	if err = unix.Close(root.pidfd); err != nil {
		t.Fatal(err)
	}
	root.releaseGate()
	if err = owner.close(); err != nil {
		t.Fatal("actual trace notifier retirement", err)
	}
	released = true
	count, err := parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 {
		t.Fatal("genuine original DA settlement", count, err)
	}
	t.Logf("original=%d omittedNonleader=%d omittedBirth=%s partialZRefused=%t reconciled=%t detachedReadESRCH=%t consumedEvent=%d reconciliationBranch=%s genuineWait=%d waitError=%v originalHUP=true DAOrdinal=%d DAActive=%d", root.original.pid, omitted, omittedBirth, sawPartial, reconciled, sawDetachedRead, consumedEvent, reconciliationBranch, exitCode, waitErr, count.Ordinal, count.Active)
}

func linuxEpochTraceGroupNegativeBindings(t *testing.T, root *linuxEpochTraceRoot, leader *linuxEpochTraceLeader, anchor, child linuxEpochTraceIdentity) {
	t.Helper()
	for _, test := range []struct {
		name   string
		change func(*linuxEpochTraceIdentity)
	}{
		{"foreign-tracer", func(value *linuxEpochTraceIdentity) { value.tracer++ }},
		{"foreign-group", func(value *linuxEpochTraceIdentity) { value.tgid++ }},
		{"new-leader", func(value *linuxEpochTraceIdentity) { value.pid = value.tgid }},
		{"foreign-parent", func(value *linuxEpochTraceIdentity) { value.parent++ }},
		{"foreign-session", func(value *linuxEpochTraceIdentity) { value.sid++ }},
		{"missing-namespace", func(value *linuxEpochTraceIdentity) { value.namespace = nil }},
		{"not-owned-stop", func(value *linuxEpochTraceIdentity) { value.state = "R" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := child
			test.change(&value)
			if root.reconcilableThread(leader, anchor, value) {
				t.Fatal("invalid current thread binding accepted")
			}
		})
	}
}

func TestLinuxEpochTraceGroupCensusBounds(t *testing.T) {
	for _, bound := range []int{0, -1, linuxEpochTraceMaxTIDs + 1} {
		if _, err := linuxEpochTraceGroupEntries(os.Getpid(), bound); err == nil {
			t.Fatal("invalid group census bound accepted", bound)
		}
	}
	ids, err := linuxEpochTraceGroupEntries(os.Getpid(), linuxEpochTraceMaxTIDs)
	if err != nil || len(ids) < 2 {
		t.Fatal("actual Go task census", ids, err)
	}
	if _, err = linuxEpochTraceGroupEntries(os.Getpid(), 1); err == nil {
		t.Fatal("real task overflow sentinel was ignored")
	}
}

// Validation-only: exitSeen is published by actual terminalProof, not by this
// control. Once published, cancellation must make no further fd probe/signal;
// the separate original Handle.Wait and HUP path still owns all completion.
func TestLinuxEpochTraceTerminalRootDoesNotResignal(t *testing.T) {
	root := &linuxEpochTraceRoot{owner: &linuxEpochTraceOwner{}, original: linuxEpochTraceIdentity{pid: 42}, stopping: true, exitSeen: true}
	leader := &linuxEpochTraceLeader{root: root, original: root.original, fd: -1}
	root.owner.leaders = map[int]*linuxEpochTraceLeader{42: leader}
	if err := root.signalLeaves(t.Context()); err != nil {
		t.Fatal("terminal root attempted another probe/signal", err)
	}
	root.exitSeen = false
	if err := root.signalLeaves(t.Context()); err == nil {
		t.Fatal("unproved root gained terminal no-signal behavior")
	}
}
