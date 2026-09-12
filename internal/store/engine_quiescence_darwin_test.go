//go:build darwin

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestLocalEngineQuiescenceRefusesUnowned(t *testing.T) {
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
func TestLocalEngineQuiescentMeasurementNative(t *testing.T) {
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
	state, err := openLocalRootWithOwner(ctx, runtime.Endpoint, owner)
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
