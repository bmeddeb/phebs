//go:build linux

package t421

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

// This fixture enters below production resource/image issuance. Its ordinary
// own image and host Git prove kernel/DA mechanics only, never current-source
// author/reference/resource qualification. No runtime hook accepts this state.
type linuxEpochTraceMechanical struct {
	root   *linuxEpochTraceRoot
	parent *dispatchadmission.LocalProducer
	output *os.File
	cancel context.CancelFunc
	joined bool
}

func linuxEpochTraceMechanicalStart(t *testing.T, mode string, refuseImage bool) *linuxEpochTraceMechanical {
	return linuxEpochTraceMechanicalStartEngine(t, mode, refuseImage, "")
}

// A native measurement control may supply its own already-built engine image.
// This is test-only held-image selection, never production tool admission.
func linuxEpochTraceMechanicalStartEngine(t *testing.T, mode string, refuseImage bool, engineImage string) *linuxEpochTraceMechanical {
	t.Helper()
	repository := ""
	if mode == "git-maintenance-parent" {
		repository = linuxEpochMaintenanceRepository(t)
	}
	settlement, settleCancel := context.WithCancel(context.Background())
	t.Cleanup(settleCancel)
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
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := parent.Close(ctx); e != nil {
			t.Error("genuine local producer close", e)
		}
	})
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = image.Close() })
	shell, err := os.Open("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	gitPath := "/usr/bin/git"
	if mode == "git-maintenance-parent" {
		gitPath = "/usr/lib/git-core/git"
	}
	git, err := os.Open(gitPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = git.Close() })
	unused := shell
	if mode == "git-maintenance-parent" {
		// This mode really executes sh; unexecuted application roles must not
		// alias its image and manufacture an ambiguous classification.
		unused, err = os.CreateTemp("", "epoch-trace-unused-image-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = unused.Close() })
	}
	namespace, err := os.Stat("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	controllerRow, err := linuxEpochProcessRow(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	held := &linuxEpochHeldFlow{controllerPID: os.Getpid(), controllerStart: controllerRow.StartIdentity, controllerImage: image, shellImage: shell, pidNamespace: namespace}
	for index := range held.images {
		held.images[index] = unused
	}
	for index := range held.gitImages {
		held.gitImages[index] = git
	}
	createdEngineCopy := false
	if mode == "engine-parent" || mode == "engine-exit-parent" || mode == "engine-burst-parent" {
		if engineImage == "" {
			copyFile, copyErr := os.CreateTemp("", "epoch-engine-image-")
			if copyErr != nil {
				t.Fatal(copyErr)
			}
			engineImage = copyFile.Name()
			createdEngineCopy = true
			if _, copyErr = image.Seek(0, 0); copyErr == nil {
				_, copyErr = io.Copy(copyFile, image)
			}
			if copyErr != nil {
				t.Fatal(copyErr)
			}
			if copyErr = copyFile.Chmod(0o500); copyErr != nil {
				t.Fatal(copyErr)
			}
			if copyErr = copyFile.Close(); copyErr != nil {
				t.Fatal(copyErr)
			}
		}
		held.images[3], err = os.Open(engineImage)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = held.images[3].Close() })
		// The supplied native image belongs to its enclosing test's TempDir.
		if createdEngineCopy {
			t.Cleanup(func() { _ = os.Remove(engineImage) })
		}
	}
	operation, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	admission := &linuxEpochResourceAdmission{held: held, probeGate: make(chan struct{}, 1), cancel: cancel}
	held.resources = admission
	owner := allocateLinuxEpochTraceOwner(admission)
	admission.trace = owner
	output, err := os.CreateTemp("", "epoch-trace-output-")
	if err != nil {
		t.Fatal(err)
	}
	// Retain both failed and successful diagnostics under wrapper-owned TMPDIR.
	t.Logf("retained private trace fixture output %s", output.Name())
	t.Cleanup(func() { _ = output.Close() })
	command := exec.Command(os.Args[0], "-test.run="+linuxEpochTraceFixtureSelector, "-test.timeout=15s")
	command.Env = []string{linuxEpochTraceFixtureEnvironment + "=" + mode, "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	if engineImage != "" {
		command.Env = append(command.Env, "PHEBS_PRIVATE_EPOCH_ENGINE_IMAGE="+engineImage)
	}
	if mode == "git-maintenance-parent" {
		command.Env = append(command.Env, "PHEBS_PRIVATE_EPOCH_TRACE_GIT_REPO="+repository)
	}
	command.Stdout, command.Stderr = output, output
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
	expected := image
	if refuseImage {
		expected = git
	}
	root := &linuxEpochTraceRoot{owner: owner, command: command, parent: parent, phase: 2, site: ExecutionParentAuthorSite(), image: expected, pidfd: -1, tasks: make(map[int]*linuxEpochTraceTask), pending: make(map[int]*linuxEpochTraceLeader), wake: make(chan struct{}, 1), stop: make(chan struct{}, 1), grace: make(chan struct{}, 1), started: make(chan error, 1), done: make(chan error, 1), exited: make(chan struct{}), observerReady: make(chan struct{}), observerDone: make(chan struct{}), callbackDone: make(chan struct{})}
	close(root.observerReady)
	owner.mu.Lock()
	owner.roots[root] = true
	owner.mu.Unlock()
	root.stopCallback = context.AfterFunc(operation, func() { defer close(root.callbackDone); root.requestStop() })
	fixture := &linuxEpochTraceMechanical{root: root, parent: parent, output: output, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		if !fixture.joined {
			root.requestStop()
			// No numeric PID/EXITKILL fallback can manufacture custody. A mechanism
			// refusal deliberately keeps the actor; the test/tool deadline owns failure.
			select {
			case <-root.done:
				fixture.joined = true
			case <-time.After(5 * time.Second):
				t.Errorf("unknown trace closure retained; no join claimed; resource=%q trace=%+v", admission.privateRefusal(), owner.failure())
				// Keep images, notifier, DA receiver and locked actor scope
				// alive until the outer test deadline. Do not contaminate a
				// later mode with an abandoned source-owned prefix.
				for {
					time.Sleep(linuxEpochTraceFallback)
				}
			}
		}
		if err := owner.close(); err != nil {
			t.Error("actual trace owner/notifier close", err)
		}
	})
	go func() {
		runtime.LockOSThread()
		root.tid = unix.Gettid()
		for !root.tryGate() {
			time.Sleep(time.Millisecond)
		}
		root.startOnThread(operation) // Mechanics below the genuine production issuer.
		runtime.UnlockOSThread()
		root.publishDone()
	}()
	startErr := <-root.started
	if refuseImage {
		if startErr == nil || !root.startedNative {
			t.Fatal("capture refusal did not preserve real Start")
		}
	} else if startErr != nil {
		t.Fatal("kernel initial capture refused", startErr)
	}
	return fixture
}

// Preparation finishes before any trace owner/notifier exists, including failure.
func linuxEpochMaintenanceRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repository, origin := filepath.Join(root, "repo.git"), filepath.Join(root, "origin.git")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	run := func(input string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "/usr/bin/git", args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		cmd.Stdin = strings.NewReader(input)
		if out, err := cmd.CombinedOutput(); err != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
			t.Fatal("maintenance fixture preparation genuine Wait", err, string(out))
		}
	}
	run("", "init", "--bare", origin)
	run("blob\nmark :1\ndata 8\nneutral\n\ncommit refs/heads/main\ncommitter Neutral <neutral@example.invalid> 1700000000 +0000\ndata 7\nneutral\nM 100644 :1 file.txt\n\ndone\n", "-C", origin, "fast-import", "--quiet")
	run("", "-C", origin, "symbolic-ref", "HEAD", "refs/heads/main")
	run("", "init", "--bare", repository)
	run("", "-C", repository, "remote", "add", "origin", origin)
	for i := 0; i < 8; i++ {
		run(fmt.Sprintf("neutral-%d\n", i), "-C", repository, "hash-object", "-w", "--stdin")
	}
	before, err := filepath.Glob(filepath.Join(repository, "objects", "pack", "*.pack"))
	if err != nil || len(before) != 0 {
		t.Fatal("fixture already packed", before, err)
	}
	return repository
}

func (fixture *linuxEpochTraceMechanical) report(t *testing.T, prefix string) (linuxEpochTraceFixtureReport, bool) {
	t.Helper()
	raw, err := linuxEpochTraceRead(fixture.output.Name(), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	var result linuxEpochTraceFixtureReport
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if found || len(line) > 512+len(prefix) {
			t.Fatal("bounded unique report refused")
		}
		if err = json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &result); err != nil {
			// A still-live regular file may end in a partial write. It is not a
			// complete report and does not grant ACK or native completion.
			if !strings.HasSuffix(string(raw), "\n") {
				return result, false
			}
			t.Fatal(err)
		}
		found = true
	}
	return result, found
}

func TestLinuxEpochTraceGoAndGitGenuineWaits(t *testing.T) {
	for _, mode := range []string{"go-parent", "git-parent", "git-maintenance-parent"} {
		t.Run(mode, func(t *testing.T) {
			fixture := linuxEpochTraceMechanicalStart(t, mode, false)
			root := fixture.root
			held := root.owner.held
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			var report linuxEpochTraceFixtureReport
			samples := 0
			sampleStarted := time.Now()
			for {
				select {
				case root.owner.admission.probeGate <- struct{}{}:
				case <-ctx.Done():
					t.Fatalf("sample gate: root=%d tracer=%d samples=%d elapsed=%v context=%v", root.original.pid, root.tid, samples, time.Since(sampleStarted), ctx.Err())
				}
				gauge, _, err := held.gauge(ctx, held.controllerPID, held.controllerStart)
				constructorFailed := err != nil
				if err == nil {
					_, err = gauge.Sample(ctx)
				}
				<-root.owner.admission.probeGate
				if err != nil {
					gaugeRefusal := "constructor-not-returned"
					var observation ProcessObservation
					if gauge != nil {
						gaugeRefusal = gauge.privateRefusal()
						observation = gauge.Observation()
					}
					// PID/TID are immutable after initial Start publication. Samples
					// and elapsed are local; both retained diagnostic copies lock.
					t.Fatalf("complete concurrent native census/classifier: root=%d tracer=%d samples=%d elapsed=%v context=%v constructorFailed=%t error=%v resource=%q gauge=%q observation=%+v trace=%+v", root.original.pid, root.tid, samples, time.Since(sampleStarted), ctx.Err(), constructorFailed, err, held.resources.privateRefusal(), gaugeRefusal, observation, root.owner.failure())
				}
				samples++
				var ready bool
				report, ready = fixture.report(t, linuxEpochTraceFixturePreACK)
				if ready {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("fixed genuine-parent report unavailable: root=%d tracer=%d samples=%d elapsed=%v context=%v", root.original.pid, root.tid, samples, time.Since(sampleStarted), ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			want := 17
			if mode == "git-parent" || mode == "git-maintenance-parent" {
				want = 0
			}
			if report.PID != root.original.pid || report.WaitCode != want || report.ChildPID <= 0 || report.Ordinal != 1 || report.Active != 0 || report.Tasks < 2 || report.AckConsumed || samples == 0 {
				t.Fatal("actual app/DA report", report, samples)
			}
			if report.MaintenancePacked != (mode == "git-maintenance-parent") {
				t.Fatal("actual automatic maintenance work missing", report)
			}
			// The fixture holds its real parent alive after actual nested Wait. Verify
			// retirement and unregistered/foreign/invalid-pin refusal without touching actor fds.
			select {
			case root.owner.admission.probeGate <- struct{}{}:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if root.owner.leaders[report.ChildPID] != nil {
				<-root.owner.admission.probeGate
				t.Fatal("app native reap not retired before report")
			}
			row, e := linuxEpochProcessRow(root.original.pid)
			gauge, _, ge := held.gauge(ctx, held.controllerPID, held.controllerStart)
			if e != nil || ge != nil {
				<-root.owner.admission.probeGate
				t.Fatal(e, ge)
			}
			row.StartIdentity += "-foreign"
			if _, ok := root.owner.defunctClass(ctx, gauge, row); ok {
				<-root.owner.admission.probeGate
				t.Fatal("foreign lifetime gained death authority")
			}
			fake := &linuxEpochTraceLeader{root: root, original: root.original, fd: -1, verified: true}
			if _, e = root.terminalProof(ctx, fake); e == nil {
				<-root.owner.admission.probeGate
				t.Fatal("unregistered original pin accepted")
			}
			original := root.owner.leaders[root.original.pid]
			if original == nil || original.root != root || root.liveLeader(ctx, original) != nil {
				<-root.owner.admission.probeGate
				t.Fatal("original live ACK handle unavailable")
			}
			ackFD, e := unix.FcntlInt(uintptr(original.fd), unix.F_DUPFD_CLOEXEC, 0)
			<-root.owner.admission.probeGate
			if e != nil {
				t.Fatal(e)
			}
			if e = unix.PidfdSendSignal(ackFD, unix.SIGUSR2, nil, 0); e != nil {
				_ = unix.Close(ackFD)
				t.Fatal(e)
			}
			select {
			case e = <-root.done:
				fixture.joined = true
			case <-ctx.Done():
				_ = unix.Close(ackFD)
				t.Fatal("genuine root Wait unavailable", ctx.Err())
			}
			mask, pe := linuxEpochTracePoll(ctx, ackFD)
			ce := unix.Close(ackFD)
			if e != nil || pe != nil || ce != nil || mask&unix.POLLHUP == 0 || !root.waitReturned || root.command.ProcessState == nil || root.command.ProcessState.ExitCode() != 0 {
				t.Fatal("actual root Wait/HUP", e, pe, ce, mask)
			}
			assertLinuxEpochNativeWaitFacts(t, fixture, e)
			final, ok := fixture.report(t, linuxEpochTraceFixtureFinal)
			if !ok || !final.AckConsumed || final.ChildPID != report.ChildPID {
				t.Fatal("final app report", final, ok)
			}
			count, e := fixture.parent.Count()
			if e != nil || count.Ordinal != 1 || count.Active != 0 {
				t.Fatal("original DA settlement", count, e)
			}
			t.Logf("mode=%s root=%d app-child=%d genuineWait=%d concurrentSamples=%d traceEvents=%d", mode, root.original.pid, report.ChildPID, report.WaitCode, samples, root.events)
		})
	}
}

func TestLinuxEpochTraceInitialRefusalStillGenuinelyWaits(t *testing.T) {
	fixture := linuxEpochTraceMechanicalStart(t, "capture-live", true)
	var completionErr error
	select {
	case err := <-fixture.root.done:
		completionErr = err
		fixture.joined = true
		if err == nil {
			t.Fatal("capture refusal disappeared")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture refusal lost actual native cleanup")
	}
	if !fixture.root.waitReturned || fixture.root.command.ProcessState == nil {
		t.Fatal("actual original Handle.Wait absent")
	}
	status, ok := fixture.root.command.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL || fixture.root.continued {
		t.Fatal("pre-CONT original kill was not observed", status)
	}
	assertLinuxEpochNativeWaitFacts(t, fixture, completionErr)
	count, err := fixture.parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 {
		t.Fatal("failed capture DA settlement", count, err)
	}
}

func TestLinuxEpochTraceCanceledLiveRootRetainsRealWait(t *testing.T) {
	fixture := linuxEpochTraceMechanicalStart(t, "capture-live", false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := fixture.report(t, linuxEpochTraceFixturePreACK); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("live parent barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fixture.cancel()
	var completionErr error
	select {
	case err := <-fixture.root.done:
		completionErr = err
		fixture.joined = true
		if err == nil {
			t.Fatal("owned SIGKILL status disappeared")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("canceled original root did not actually join; resource=%q trace=%+v", fixture.root.owner.admission.privateRefusal(), fixture.root.owner.failure())
	}
	if !fixture.root.waitReturned || fixture.root.command.ProcessState == nil {
		t.Fatal("cancellation fabricated completion")
	}
	status, ok := fixture.root.command.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("cancellation did not produce owned SIGKILL", status)
	}
	assertLinuxEpochNativeWaitFacts(t, fixture, completionErr)
	count, err := fixture.parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 {
		t.Fatal("canceled root DA settlement", count, err)
	}
}

func TestLinuxEpochTraceClosedShapeAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name         string
		attrs        *syscall.SysProcAttr
		author, want bool
	}{
		{"nil", nil, true, true}, {"server", &syscall.SysProcAttr{Setsid: true}, false, true}, {"author", &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}, true, true},
		{"namespace", &syscall.SysProcAttr{Setsid: true, Cloneflags: unix.CLONE_NEWNS}, true, false}, {"credentials", &syscall.SysProcAttr{Setsid: true, Credential: &syscall.Credential{Uid: 1}}, false, false}, {"unexpected-process-group", &syscall.SysProcAttr{Setsid: true, Setpgid: true}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command("/not/executed")
			command.SysProcAttr = tc.attrs
			if linuxEpochTraceAttributes(command, tc.author) != tc.want {
				t.Fatal("closed attribute shape")
			}
		})
	}
	root := &linuxEpochTraceRoot{events: math.MaxUint64}
	before := root.recent
	if root.record(1, 1, 1, "1") == nil || root.events != math.MaxUint64 || root.recent != before {
		t.Fatal("counter overflow changed evidence")
	}
	if _, err := newLinuxEpochTraceOwner(nil); err == nil {
		t.Fatal("nil scope issued owner")
	}
	if _, err := (&linuxEpochTraceOwner{}).start(t.Context(), t.Context(), exec.Command("/not/executed"), nil, 2, ExecutionParentAuthorSite(), nil, nil); err == nil {
		t.Fatal("unissued owner accepted Start")
	}
	if _, err := (&linuxEpochTraceRoot{}).terminalProof(t.Context(), nil); err == nil {
		t.Fatal("invalid terminal authority")
	}
}

// A duplicate signal is a no-op, not a claim of native death or settlement.
// Invalid fd makes the regression fail deterministically if live probing is
// moved back ahead of the already-sent guard; no process is started or signaled.
func TestLinuxEpochTraceAlreadySignaledDoesNotDemandLive(t *testing.T) {
	for _, tc := range []struct {
		name                                              string
		stopping, graceful, termSent, killSent, wantError bool
	}{
		{name: "killed", stopping: true, killSent: true},
		{name: "term-sent", graceful: true, termSent: true},
		{name: "escalation-needs-new-proof", stopping: true, termSent: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &linuxEpochTraceRoot{original: linuxEpochTraceIdentity{pid: 123}, stopping: tc.stopping, graceful: tc.graceful, pending: make(map[int]*linuxEpochTraceLeader)}
			leader := &linuxEpochTraceLeader{root: root, original: linuxEpochTraceIdentity{pid: 123}, fd: -1, termSent: tc.termSent, killSent: tc.killSent}
			root.owner = &linuxEpochTraceOwner{leaders: map[int]*linuxEpochTraceLeader{123: leader}}
			err := root.signalLeaves(t.Context())
			if (err != nil) != tc.wantError {
				t.Fatal("duplicate/no-new-signal guard", err)
			}
			if leader.termSent != tc.termSent || leader.killSent != tc.killSent || root.waitReturned || root.exitSeen {
				t.Fatal("no-op fabricated signal or native completion")
			}
		})
	}
}
