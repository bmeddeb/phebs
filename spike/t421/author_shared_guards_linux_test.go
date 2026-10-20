//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

func TestLinuxAuthorSharedScopeAndModeRefusals(t *testing.T) {
	for _, mode := range []string{"foreign-controller", "wrong-root", "wrong-phase", "wrong-producer", "standalone-to-shared", "shared-to-standalone", "rebound-controller"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			controller, err := dispatchadmission.New(ctx, linuxAuthorSharedConfig())
			if err != nil {
				t.Fatal(err)
			}
			parent, err := controller.NewLocalProducer(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(context.Background()) }()
			// Preserve one real committed parent attempt across every preflight refusal.
			handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), exec.Command("/usr/bin/true"))
			if err != nil || handle.Wait() != nil {
				t.Fatal("actual initial prefix", err)
			}
			owner, _ := linuxAuthorSharedFixture(t)
			selectedController, selectedParent, producer := controller, parent, uint32(7)
			switch mode {
			case "foreign-controller":
				selectedController = &dispatchadmission.Controller{}
			case "wrong-root":
				selectedParent, err = controller.NewLocalProducer(ctx, 2)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = selectedParent.Close(context.Background()) }()
			case "wrong-phase":
				owner.projection.phases[0] = 4
			case "wrong-producer":
				producer = 8
			case "standalone-to-shared":
				owner.mode = linuxAuthorStandalone
			case "shared-to-standalone":
				owner.mode = linuxAuthorShared
				owner.controller, owner.parent = controller, parent
			case "rebound-controller":
				owner.mode = linuxAuthorShared
				owner.controller, owner.parent = &dispatchadmission.Controller{}, &dispatchadmission.LocalProducer{}
			}
			var result ExecutionAuthorResult
			if mode == "shared-to-standalone" {
				result, err = owner.AuthorNext(ctx)
			} else {
				result, err = owner.AuthorNextOn(ctx, selectedController, selectedParent, producer)
			}
			if err == nil || result.RootStarted || result.Completed || owner.err != nil || owner.active != nil || owner.next != 0 || len(owner.Results()) != 0 {
				t.Fatal("preflight refusal touched source/work state", mode, result, err)
			}
			count, err := parent.Count()
			if err != nil || count.Ordinal != 1 || count.Active != 0 {
				t.Fatal("refusal lost actual prefix")
			}
			if mode != "foreign-controller" && mode != "shared-to-standalone" && result.Accounting.Attempts != 1 {
				t.Fatal("refusal erased controller prefix")
			}
			snapshot, err := controller.Snapshot()
			if err != nil || snapshot.Attempts != 1 {
				t.Fatal("preflight refusal fenced or failed controller", snapshot, err)
			}
		})
	}
}

func TestLinuxAuthorSharedAdmittedEarlyExitPinsBeforeWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	controller, err := dispatchadmission.New(ctx, authorSharedTestConfig(0))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(context.Background()) }()
	command := rootWaitFixedCommand(ctx, "exit")
	handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), command)
	if err != nil {
		t.Fatal(err)
	}
	var waited <-chan error
	consumed := false
	defer func() {
		if !consumed {
			if waited != nil {
				<-waited
			} else {
				_ = handle.Wait()
			}
		}
	}()
	if rootWaitObserveUnreaped(command) != nil {
		t.Fatal("WNOWAIT fixed exited root")
	}
	before, err := parent.Count()
	if err != nil || before.Active != 1 || before.Ordinal != 1 {
		t.Fatal("root was settled before actual Wait")
	}
	fd, result, pinErr := pinAdmittedExecutionProcessRoot(command, handle)
	waited = result
	if pinErr != nil {
		t.Fatal(pinErr)
	}
	defer func() { _ = unix.Close(fd) }()
	waitErr := <-waited
	consumed = true
	if rootWaitRequireExit17(command, waitErr) != nil {
		t.Fatal("admitted root lost native exit17", waitErr)
	}
	after, err := parent.Count()
	if err != nil || after.Active != 0 || after.Ordinal != 1 {
		t.Fatal("Handle.Wait lost parent settlement")
	}
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	count, err := unix.Poll(poll, 0)
	if err != nil || count != 1 || poll[0].Revents&unix.POLLIN == 0 {
		t.Fatal("exited root pidfd not ready")
	}
	if !errors.Is(unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0), unix.ESRCH) {
		t.Fatal("owned joined pidfd still names a live root")
	}
}

func TestLinuxAuthorSharedAdmittedEMFILEChild(t *testing.T) {
	if os.Getenv("PHEBS_T42_SHARED_ADMITTED_EMFILE") != "1" {
		return
	}
	if err := linuxAuthorSharedAdmittedEMFILE(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	_, _ = fmt.Fprintln(os.Stdout, "admitted EMFILE retained actual Wait17 and parent settlement")
	os.Exit(0)
}
func linuxAuthorSharedAdmittedEMFILE() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controller, err := dispatchadmission.New(ctx, authorSharedTestConfig(0))
	if err != nil {
		return err
	}
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close(context.Background()) }()
	command := rootWaitFixedCommand(ctx, "exit")
	handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), command)
	if err != nil {
		return err
	}
	var waited <-chan error
	consumed := false
	defer func() {
		if !consumed {
			if waited != nil {
				<-waited
			} else {
				_ = handle.Wait()
			}
		}
	}()
	if err := rootWaitObserveUnreaped(command); err != nil {
		return err
	}
	var prior unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &prior); err != nil {
		return err
	}
	limited := prior
	limited.Cur = 0
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limited); err != nil {
		return err
	}
	fd, result, pinErr := pinAdmittedExecutionProcessRoot(command, handle)
	waited = result
	restoreErr := unix.Setrlimit(unix.RLIMIT_NOFILE, &prior)
	if fd >= 0 {
		_ = unix.Close(fd)
	}
	waitErr := <-waited
	consumed = true
	if restoreErr != nil || fd != -1 || !errors.Is(pinErr, unix.EMFILE) || rootWaitRequireExit17(command, waitErr) != nil {
		return errors.New("actual admitted EMFILE lost native Wait17")
	}
	count, err := parent.Count()
	if err != nil || count.Active != 0 || count.Ordinal != 1 {
		return errors.New("admitted EMFILE lost genuine parent settlement")
	}
	return nil
}
func TestLinuxAuthorSharedAdmittedEMFILEStillSettles(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxAuthorSharedAdmittedEMFILEChild$")
	command.Env = []string{"PHEBS_T42_SHARED_ADMITTED_EMFILE=1", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "admitted EMFILE retained actual Wait17 and parent settlement\n" {
		t.Fatal("isolated admitted EMFILE control", err, string(output))
	}
}

func TestLinuxAuthorSharedPendingTimeoutRetainsLoansAndPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	controller, err := dispatchadmission.New(ctx, linuxAuthorSharedConfig())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(context.Background()) }()
	handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), exec.Command("/usr/bin/true"))
	if err != nil || handle.Wait() != nil {
		t.Fatal("actual retained parent prefix", err)
	}
	owner, _ := linuxAuthorSharedFixture(t)
	builds := &LinuxExecutionGoBuildCustody{}
	owner.request.Builds = builds
	owner.projection.deadlines[0] = time.Millisecond
	builds.mu.Lock()
	locked := true
	defer func() {
		if locked {
			builds.mu.Unlock()
		}
	}()
	result, err := owner.AuthorNextOn(ctx, controller, parent, 7)
	if err == nil || result.Completed || result.ProducerID != 7 || result.Accounting.Attempts != 1 {
		t.Fatal("pending shared timeout lost its controller prefix", result, err)
	}
	owner.mu.Lock()
	attempt := owner.active
	owner.mu.Unlock()
	if attempt == nil {
		t.Fatal("unknown shared worker discarded")
	}
	deadline, stop := context.WithTimeout(ctx, time.Millisecond)
	closeErr := owner.Close(deadline)
	stop()
	if closeErr == nil || owner.released {
		t.Fatal("timeout substituted for shared worker join")
	}
	for _, root := range owner.roots {
		if _, err := root.file.Stat(); err != nil {
			t.Fatal("unknown shared worker released source")
		}
	}
	builds.mu.Unlock()
	locked = false
	<-attempt.done
	results := owner.Results()
	if len(results) != 1 || results[0].ProducerID != 7 || results[0].Completed || results[0].RootStarted || results[0].Accounting.Attempts != 1 {
		t.Fatal("joined failed shared prefix replaced", results)
	}
	if owner.Close(ctx) == nil || !owner.released {
		t.Fatal("joined shared failure lost sticky Close or retained descriptors")
	}
}
