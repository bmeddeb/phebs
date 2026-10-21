//go:build linux

package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"golang.org/x/sys/unix"
)

func TestLocalEngineLinuxRefusesUnowned(t *testing.T) {
	ctx, owner, _ := storeAccountingFixture(t, 2, 1)
	for _, state := range []*Surreal{nil, {}, {accounting: owner}, {engine: &localEngine{}}} {
		if err := state.WithQuiescentLocalEngine(ctx, func(context.Context) error {
			t.Fatal("unowned measurement ran")
			return nil
		}); !errors.Is(err, errLocalEngineQuiescence) {
			t.Fatal(err)
		}
	}
}

// This measures two actual fixture files, not all custody or phase high-water.
// The native engine and SDK are real; no supplied stopped-state/probe is used.
func TestLocalEngineLinuxQuiescentMeasurementNative(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	controller, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: 40, Transactions: 2}},
		Phases:    []storeaccounting.Phase{{ID: 1, Transactions: 170, Rows: 170 * 512}},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, controller, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 1}}, AckTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = transport.Close() }()
	file, config, err := transport.Open(2)
	if err != nil {
		t.Fatal(err)
	}
	client, err := storeaccounting.NewClient(ctx, file, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	owner, err := newStoreCallOwner(client)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runtime, engine, err := startOwnedEngine(ctx, "surrealkv:"+filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.stop()
	state, err := openLocalRootWithOwner(ctx, runtime, owner)
	if err != nil {
		t.Fatal(err)
	}
	// The same concrete engine/connection assembly performed by openLocal;
	// only the genuine fixture SDK owner replaces process-global bootstrap.
	state.engine, state.stop = engine, engine.stop
	engine.removeRuntime, err = PublishLocalRuntime(root, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close(context.Background()) }()
	identity, paused, err := inspectLocalEngine(engine.process)
	if err != nil || paused {
		t.Fatal("engine not initially running", err)
	}
	paths := []string{filepath.Join(root, "sparse"), filepath.Join(root, "small")}
	const logical = int64(8<<20 + 17 + 3)
	for index, size := range []int64{8<<20 + 17, 3} {
		f, err := os.OpenFile(paths[index], os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := errors.Join(f.Truncate(size), f.Sync(), f.Close()); err != nil {
			t.Fatal(err)
		}
	}
	measure := func(context.Context) error {
		observed, stopped, err := inspectLocalEngine(engine.process)
		if err != nil || !stopped || observed != identity {
			return errors.New("actual owned engine was not stopped")
		}
		entries, err := os.ReadDir("/proc/" + strconv.Itoa(engine.process.Pid) + "/task")
		if err != nil || len(entries) < 2 {
			return errors.Join(errors.New("actual engine is not multithreaded"), err)
		}
		var total int64
		for _, path := range paths {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("actual fixture file unavailable")
			}
			total += info.Size()
		}
		if total != logical {
			return errors.New("actual fixture logical bytes differ")
		}
		return nil
	}
	beforeGuard, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WithQuiescentLocalEngine(ctx, measure); err != nil {
		t.Fatal(err)
	}
	afterGuard, err := controller.Snapshot()
	if err != nil || !reflect.DeepEqual(beforeGuard, afterGuard) {
		t.Fatal("guard changed SDK accounting", err)
	}
	t.Logf("actual_surreal_pid=%d group_stopped_and_resumed=true accounting_unchanged=true", engine.process.Pid)
	assertResumed := func(t *testing.T) {
		t.Helper()
		observed, stopped, err := inspectLocalEngine(engine.process)
		if err != nil || stopped || observed != identity {
			t.Fatal("same actual engine not resumed", err)
		}
	}
	t.Run("canceled_before_entry", func(t *testing.T) {
		operation, stop := context.WithCancel(ctx)
		stop()
		if err := state.WithQuiescentLocalEngine(operation, func(context.Context) error {
			t.Fatal("canceled measurement ran")
			return nil
		}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		assertResumed(t)
	})
	t.Run("measured_and_fresh_sdk_blocked", func(t *testing.T) {
		started, queryDone := make(chan struct{}), make(chan error, 1)
		if err := state.WithQuiescentLocalEngine(ctx, func(ctx context.Context) error {
			if err := measure(ctx); err != nil {
				return err
			}
			go func() {
				close(started)
				_, err := storeQuery[[]int](ctx, owner, state.db, "RETURN [1];", nil, storeRead())
				queryDone <- err
			}()
			<-started
			select {
			case err := <-queryDone:
				queryDone <- err
				return errors.Join(errors.New("SDK call completed inside idle lease"), err)
			case <-time.After(20 * time.Millisecond):
				return nil
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := <-queryDone; err != nil {
			t.Fatal(err)
		}
		assertResumed(t)
	})
	for _, mode := range []string{"error", "canceled", "panic"} {
		t.Run(mode, func(t *testing.T) {
			operation, stop := context.WithCancel(ctx)
			defer stop()
			sentinel := errors.New("fixture measurement failed")
			var result error
			func() {
				defer func() {
					value := recover()
					if mode == "panic" && value != sentinel || mode != "panic" && value != nil {
						t.Error("measurement panic changed", value)
					}
				}()
				result = state.WithQuiescentLocalEngine(operation, func(ctx context.Context) error {
					if err := measure(ctx); err != nil {
						return err
					}
					switch mode {
					case "error":
						return sentinel
					case "canceled":
						stop()
						return nil
					default:
						panic(sentinel)
					}
				})
			}()
			if mode == "error" && !errors.Is(result, sentinel) || mode == "canceled" && !errors.Is(result, context.Canceled) {
				t.Fatal(result)
			}
			assertResumed(t)
		})
	}
	t.Run("active_transaction_refuses", func(t *testing.T) {
		tx, err := storeBegin(ctx, owner, state.db)
		if err != nil {
			t.Fatal(err)
		}
		refusal := state.WithQuiescentLocalEngine(ctx, func(context.Context) error { t.Error("busy SDK measured"); return nil })
		cancelErr := storeCancel(ctx, owner, tx)
		if !errors.Is(refusal, storeaccounting.ErrBusy) || cancelErr != nil {
			t.Fatal("busy SDK guard", refusal, cancelErr)
		}
		assertResumed(t)
	})
	t.Run("retired_owner", func(t *testing.T) {
		if err := state.WithRetiredLocalEngine(ctx, func(context.Context) error {
			t.Error("unclosed owner measured as retired")
			return nil
		}); !errors.Is(err, storeaccounting.ErrFenced) {
			t.Fatal(err)
		}
		assertResumed(t)
		if err := owner.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := state.WithQuiescentLocalEngine(ctx, func(context.Context) error {
			t.Error("closed owner measured as live")
			return nil
		}); err == nil {
			t.Fatal("closed owner admitted by idle guard")
		}
		if err := state.WithRetiredLocalEngine(ctx, measure); err != nil {
			t.Fatal(err)
		}
		assertResumed(t)
	})
	t.Run("stop_waits_for_resume", func(t *testing.T) {
		stopStarted, stopDone := make(chan struct{}), make(chan error, 1)
		if err := state.WithRetiredLocalEngine(ctx, func(ctx context.Context) error {
			if err := measure(ctx); err != nil {
				return err
			}
			go func() { close(stopStarted); stopDone <- state.Close(ctx) }()
			<-stopStarted
			select {
			case <-stopDone:
				return errors.New("stop reaped engine during measurement")
			case <-time.After(20 * time.Millisecond):
				_, err := os.Lstat(filepath.Join(root, localRuntimeName))
				return err
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := <-stopDone; err != nil {
			t.Fatal(err)
		}
		if !engine.stopped || engine.paused {
			t.Fatal("engine stop did not follow verified resume")
		}
		if err := engine.process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
			t.Fatal("owned process handle was not joined", err)
		}
		if _, err := os.Lstat(filepath.Join(root, localRuntimeName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("runtime file survived completed shutdown", err)
		}
		if err := state.WithRetiredLocalEngine(ctx, measure); err == nil {
			t.Fatal("stopped engine was reused")
		}
	})
}

// The child is a fresh real Go process. Its extra locked worker proves that a
// leader-only stopped-state observation cannot satisfy these tests.
func TestLocalEngineLinuxChild(t *testing.T) {
	if os.Getenv("PHEBS_LOCAL_ENGINE_LINUX_CHILD") != "1" {
		return
	}
	ready, finish, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(joined)
		close(ready)
		<-finish
	}()
	<-ready
	fmt.Println("ENGINE_READY")
	_, err := io.Copy(io.Discard, os.Stdin)
	close(finish)
	<-joined
	if err != nil {
		os.Exit(125)
	}
	os.Exit(17)
}

func TestLocalEngineLinuxOriginalWaitAndIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.Command(os.Args[0], "-test.run=^TestLocalEngineLinuxChild$")
	command.Env = append(os.Environ(), "PHEBS_LOCAL_ENGINE_LINUX_CHILD=1")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	var client *dispatchadmission.Client
	handle, err := client.Start(ctx, 1, command)
	if err != nil {
		t.Fatal(err)
	}
	joined := false
	var readerDone chan struct{}
	defer func() {
		_ = input.Close()
		if !joined {
			_ = command.Process.Signal(unix.SIGCONT)
			_ = command.Process.Kill()
			waitErr := handle.Wait()
			t.Logf("failure_cleanup_actual_wait=%v state=%v", waitErr, command.ProcessState)
		}
		if readerDone != nil {
			<-readerDone
		}
	}()
	line := make(chan string, 1)
	readerDone = make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(output)
		if scanner.Scan() {
			line <- scanner.Text()
		} else {
			line <- ""
		}
	}()
	select {
	case value := <-line:
		if value != "ENGINE_READY" {
			t.Fatal("missing native child READY", value)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	engine := &localEngine{process: command.Process}
	identity, state, err := inspectLocalEngineGroupContext(ctx, command.Process)
	if err != nil || state != localEngineRunning {
		t.Fatal("actual child not live", err)
	}
	for _, mutate := range []func(*localEngineIdentity){func(v *localEngineIdentity) { v.birth++ }, func(v *localEngineIdentity) { v.parent++ }, func(v *localEngineIdentity) { v.namespaceInode++ }, func(v *localEngineIdentity) { v.imageInode++ }} {
		wrong := identity
		mutate(&wrong)
		if err := awaitLocalEngine(ctx, command.Process, wrong, false); err == nil {
			t.Fatal("changed original identity accepted")
		}
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := awaitLocalEngine(canceled, command.Process, identity, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	engine.mu.Lock()
	measured := 0
	err = engine.measureStopped(ctx, func(context.Context) error {
		measured++
		_, paused, err := inspectLocalEngine(command.Process)
		if err != nil || !paused {
			return errors.Join(errLocalEngineQuiescence, err)
		}
		return nil
	})
	engine.mu.Unlock()
	if err != nil || measured != 1 || engine.paused {
		t.Fatal("native own group guard", err, measured)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	waitErr := handle.Wait()
	joined = true
	exit, exactExit := waitErr.(*exec.ExitError)
	if !exactExit || exit.ProcessState != command.ProcessState || exit.ExitCode() != 17 {
		t.Fatal("original genuine Wait changed", waitErr, command.ProcessState)
	}
	t.Logf("original_pid=%d actual_handle_wait=17 error=%v process_state=%v", command.Process.Pid, waitErr, command.ProcessState)
}

// This uses a real raw engine and SDK-accounting owner, but no SDK connection,
// schema, replay, volume image or whole-workspace high-water assertion.
func TestLocalImportLinuxQuiescentMeasurementNative(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	controller, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: 2, Transactions: 1}},
		Phases:    []storeaccounting.Phase{{ID: 1, Transactions: 2, Rows: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, controller, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 1}}, AckTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = transport.Close() }()
	file, config, err := transport.Open(2)
	if err != nil {
		t.Fatal(err)
	}
	client, err := storeaccounting.NewClient(ctx, file, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	owner, err := storeaccounting.NewSDKOwner(client)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "t422-import-measurement-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if t.Failed() {
			t.Log("retained failed raw-import measurement custody:", root)
		} else if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	}()
	runtime, stop, guard, err := StartLocalImportWithMeasurement(ctx, root, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	// Runtime PID is read-only diagnostic identity here, never signal authority.
	process, err := os.FindProcess(runtime.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Release() }()
	identity, paused, err := inspectLocalEngine(process)
	if err != nil || paused {
		t.Fatal("raw import not running", err)
	}
	assertResumed := func(t *testing.T) {
		t.Helper()
		observed, paused, err := inspectLocalEngine(process)
		if err != nil || paused || observed != identity {
			t.Fatal("same raw import engine not resumed", err)
		}
	}
	measure := func(context.Context) error {
		observed, paused, err := inspectLocalEngine(process)
		if err != nil || !paused || observed != identity {
			return errors.Join(errors.New("actual raw import not stopped"), err)
		}
		if _, err := os.Lstat(filepath.Join(root, localRuntimeName)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("raw import published a runtime descriptor")
		}
		return nil
	}
	before, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := guard(ctx, measure); err != nil {
		t.Fatal(err)
	}
	assertResumed(t)
	after, err := controller.Snapshot()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("measurement changed accounting", before, after, err)
	}
	deadline, _ := ctx.Deadline()
	extended, extendCancel := context.WithDeadline(context.Background(), deadline.Add(time.Second))
	defer extendCancel()
	canceled, cancelOperation := context.WithCancel(ctx)
	cancelOperation()
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{"missing_context", nil},
		{"unbounded_context", context.Background()},
		{"extended_deadline", extended},
		{"canceled_context", canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := guard(test.ctx, func(context.Context) error { t.Error("refused measurement ran"); return nil }); err == nil {
				t.Fatal("invalid measurement context admitted")
			}
			assertResumed(t)
		})
	}
	if err := guard(ctx, nil); !errors.Is(err, errLocalEngineQuiescence) {
		t.Fatal("missing measurement admitted", err)
	}
	t.Run("callback_failure_resumes", func(t *testing.T) {
		sentinel := errors.New("fixture measurement failure")
		if err := guard(ctx, func(ctx context.Context) error {
			if err := measure(ctx); err != nil {
				return err
			}
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		assertResumed(t)
	})
	t.Run("stop_waits_for_measurement", func(t *testing.T) {
		started, done := make(chan struct{}), make(chan struct{})
		if err := guard(ctx, func(ctx context.Context) error {
			if err := measure(ctx); err != nil {
				return err
			}
			go func() { close(started); stop(); close(done) }()
			<-started
			select {
			case <-done:
				return errors.New("raw import stopped inside measurement")
			case <-time.After(20 * time.Millisecond):
				return measure(ctx)
			}
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("raw import stop did not join", ctx.Err())
		}
		if err := guard(ctx, measure); !errors.Is(err, errLocalEngineQuiescence) {
			t.Fatal("stopped raw import reused", err)
		}
		if _, _, err := inspectLocalEngine(process); err == nil {
			t.Fatal("raw import remains after owned stop returned")
		}
	})
}
