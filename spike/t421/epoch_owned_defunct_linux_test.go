//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// Mechanical own-image/DA fixture, never an input or resource admission issuer.
type linuxOwnedDefunctFixture struct {
	held      *linuxEpochHeldFlow
	authority *linuxEpochSessionAuthority
	parent    *dispatchadmission.LocalProducer
	handle    dispatchadmission.Handle
	waited    <-chan error
	joined    bool
}

func linuxOwnedDefunctRoot(t *testing.T) *linuxOwnedDefunctFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // Settlement outlives operational cancellation and native Wait.
	controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{
		Limits:    dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1, Attempts: 1, WireBytes: 16 << 10, AckTimeout: 2 * time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{{ID: 1, Role: 1}}}},
		Phases:    []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{{Role: 1, Attempts: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := parent.Close(closeCtx); err != nil {
			t.Error("actual local producer close", err)
		}
	})
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = image.Close() })
	shellPath, err := filepath.EvalSymlinks("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	shell, err := os.Open(shellPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Close() })
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
	held.resources = &linuxEpochResourceAdmission{held: held, probeGate: make(chan struct{}, 1)}
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochSessionFixedChild$", "--", "t422-linux-epoch-leaf")
	command.Env = []string{"GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	handle, err := parent.StartInPhase(ctx, 1, dispatchadmission.Site{ID: 1, Role: 1}, command)
	if err != nil {
		t.Fatal(err)
	}
	server := &linuxEpochServer{pidfd: -1}
	server.pidfd, server.pinErr = pinExecutionRootDescriptor(command)
	fixture := &linuxOwnedDefunctFixture{held: held, parent: parent, handle: handle}
	t.Cleanup(func() {
		if server.pidfd >= 0 {
			_ = unix.PidfdSendSignal(server.pidfd, unix.SIGKILL, nil, 0)
		}
		if !fixture.joined {
			if fixture.waited != nil {
				<-fixture.waited
			} else {
				_ = fixture.handle.Wait()
			}
		}
		if server.closeRoot() != nil {
			t.Error("owned root descriptor retirement")
		}
	})
	if server.pinErr != nil {
		t.Fatal(server.pinErr)
	}
	run := &ExecutionEpochOneRun{command: command}
	held.servers = map[*ExecutionEpochOneRun]*linuxEpochServer{run: server}
	fixture.authority, err = captureLinuxEpochSession(held, run, server)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *linuxOwnedDefunctFixture) stopUnreaped(t *testing.T) {
	t.Helper()
	if err := unix.PidfdSendSignal(fixture.authority.pidfd, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := rootWaitObserveUnreaped(fixture.authority.run.command); err != nil {
		t.Fatal(err)
	}
	if fixture.authority.run.command.ProcessState != nil {
		t.Fatal("WNOWAIT manufactured genuine Wait")
	}
}

func TestLinuxEpochOwnedDefunctCensusAndWaitBarrier(t *testing.T) {
	fixture := linuxOwnedDefunctRoot(t)
	held, authority := fixture.held, fixture.authority
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var liveRows []t4013.NativeProcessRecord
	for {
		rows, err := t4013.ObserveProcessTreeRecords(ctx, held.controllerPID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.PID == authority.pid && row.RSSBytes > 0 {
				liveRows = rows
			}
		}
		if liveRows != nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("no positive live-child census")
		}
		time.Sleep(time.Millisecond)
	}
	serverGauge, _, err := held.gauge(ctx, authority.pid, authority.birth)
	if err != nil || len(serverGauge.observation.Classes) != 7 || validObservedProcessClass(linuxEpochOwnedDefunctClass) {
		t.Fatal("private class widened server or frozen validator", err)
	}
	if err := held.resources.armOwnedStop(ctx, authority); err != nil {
		t.Fatal("live expected-stop arm", err)
	}
	fixture.stopUnreaped(t)
	gauge, _, err := held.gauge(ctx, held.controllerPID, "")
	if err != nil {
		t.Fatal(err)
	}
	held.resources.probeGate <- struct{}{}
	observation, sampleErr := gauge.Sample(ctx) // Real native census retains the Z row.
	<-held.resources.probeGate
	if sampleErr != nil || !observation.Available || observation.ObservedDescendants != 1 {
		t.Fatal("owned dead root disappeared or refused", observation, sampleErr)
	}
	var ownedRows uint64
	for _, class := range observation.Classes {
		if class.Class == linuxEpochOwnedDefunctClass {
			ownedRows = class.ObservedRows
		}
	}
	if ownedRows != 1 {
		t.Fatal("defunct row claimed a live exec class", observation)
	}
	oldGauge, _, err := held.gauge(ctx, held.controllerPID, "")
	if err != nil {
		t.Fatal(err)
	}
	held.resources.probeGate <- struct{}{}
	oldObservation, oldErr := oldGauge.acceptRowsContext(ctx, liveRows)
	<-held.resources.probeGate
	var oldRSS uint64
	for _, row := range liveRows {
		oldRSS += uint64(row.RSSBytes)
	}
	if oldErr != nil || oldObservation.ObservedRSSBytes != oldRSS || oldObservation.ObservedDescendants != uint64(len(liveRows)-1) {
		t.Fatal("live-to-Z classification changed original positive RSS/census", oldObservation, oldErr)
	}
	// Refusals preserve the original authority; none can be learned from a PID.
	var row t4013.NativeProcessRecord
	for _, observed := range liveRows {
		if observed.PID == authority.pid {
			row = observed
		}
	}
	held.resources.probeGate <- struct{}{}
	for _, control := range []string{"unregistered", "forged", "birth", "name", "parent", "namespace"} {
		original := held.resources.ownedStops[authority.pid]
		observed := row
		namespace := held.pidNamespace
		switch control {
		case "unregistered":
			delete(held.resources.ownedStops, authority.pid)
		case "forged":
			copyAuthority := *authority
			held.resources.ownedStops[authority.pid] = &copyAuthority
		case "birth":
			observed.StartIdentity = "1"
		case "name":
			observed.ObservedName = "foreign"
		case "parent":
			observed.ParentPID = 1
		case "namespace":
			held.pidNamespace, err = os.Stat("/proc/self/ns/mnt")
		}
		_, accepted := held.resources.ownedDefunctClass(ctx, gauge, observed)
		held.resources.ownedStops[authority.pid], held.pidNamespace = original, namespace
		if err != nil || accepted {
			<-held.resources.probeGate
			t.Fatal("defunct refusal control accepted", control, err)
		}
	}
	<-held.resources.probeGate
	// Stop a real census inside child classification, then request sole genuine
	// Handle.Wait. It must not reap the original root while this row is in flight.
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	classify := gauge.classify
	gauge.classify = func(checkCtx context.Context, observed t4013.NativeProcessRecord) (int, bool) {
		if observed.PID == authority.pid {
			close(entered)
			<-release
		}
		return classify(checkCtx, observed)
	}
	sampled := make(chan error, 1)
	go func() {
		held.resources.probeGate <- struct{}{}
		_, err := gauge.Sample(ctx)
		<-held.resources.probeGate
		sampled <- err
	}()
	sampleJoined := false
	defer func() {
		unblock()
		if !sampleJoined {
			<-sampled
		}
	}()
	select {
	case <-entered:
	case err := <-sampled:
		sampleJoined = true
		t.Fatal("native census never reached owned child", err)
	case <-ctx.Done():
		t.Fatal("native classification barrier deadline")
	}
	waited := make(chan error, 1)
	fixture.waited = waited
	go func() { waited <- held.resources.waitOwnedRoot(authority, fixture.handle) }()
	select {
	case <-waited:
		fixture.joined = true
		t.Fatal("genuine Wait escaped in-flight census gate")
	case <-time.After(20 * time.Millisecond):
	}
	if err := authority.checkCurrentExit(ctx); err != nil {
		t.Fatal("original root reaped during classification", err)
	}
	unblock()
	sampleErr = <-sampled
	sampleJoined = true
	if sampleErr != nil {
		t.Fatal("in-flight census failed", sampleErr)
	}
	waitErr := <-waited
	fixture.joined = true
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || authority.run.command.ProcessState == nil {
		t.Fatal("actual Handle.Wait missing", waitErr)
	}
	count, err := fixture.parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 || len(held.resources.ownedStops) != 0 || authority.checkCurrentExit(ctx) == nil {
		t.Fatal("reap/DA settlement or retired-anchor refusal missing", count, err)
	}
}

func TestLinuxEpochOwnedDefunctRefusesDeadBeforeArm(t *testing.T) {
	fixture := linuxOwnedDefunctRoot(t)
	fixture.stopUnreaped(t)
	if fixture.held.resources.armOwnedStop(t.Context(), fixture.authority) == nil || len(fixture.held.resources.ownedStops) != 0 {
		t.Fatal("prior unarmed death laundered as expected stop")
	}
}
