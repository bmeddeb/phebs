//go:build darwin

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// This uses a real raw engine and SDK-accounting owner, but no SDK connection,
// schema, replay, volume image or whole-workspace high-water assertion.
func TestLocalImportQuiescentMeasurementNative(t *testing.T) {
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
