//go:build darwin || linux

package storeaccounting

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

// These use the genuine SDK owner and SA client/socket/transport/controller.
// Native replies alone are scripted: no database, runner or stale callback is
// exercised, and matching a counter pattern does not identify a rehearsal cause.
func TestStoreAccountingSDKCancellationDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name                       string
		read, beforeSubmit         bool
		wantNative, wantOpen       int
		wantTransactions, wantRows uint64
	}{
		{name: "native_read_zero_open", read: true, wantNative: 1},
		{name: "write_before_submit_zero_open", beforeSubmit: true},
		{name: "native_write_one_open", wantNative: 1, wantOpen: 1, wantTransactions: 1, wantRows: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := log.Writer()
			t.Cleanup(func() { log.SetOutput(previous) })
			var output bytes.Buffer
			log.SetOutput(&output)
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			callCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			entered := make(chan struct{})
			native.call = func(ctx context.Context, request *connection.RPCRequest) (any, error) {
				if request.Method != "query" {
					return nil, errors.New("unexpected native method")
				}
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			if test.beforeSubmit {
				cancel(errors.New("private-cancellation-cause"))
			}
			result := make(chan error, 1)
			go func() {
				recipe := SDKWrite(1)
				if test.read {
					recipe = SDKRead()
				}
				_, err := SDKQuery[[]int](callCtx, owner, db, "private-generic-query", map[string]any{"secret": "private-bind-value"}, recipe)
				result <- err
			}()
			if !test.beforeSubmit {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("scripted native call was not reached")
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != test.wantTransactions || prefix.Rows != test.wantRows ||
					prefix.Producers[0].Calls != test.wantOpen {
					t.Fatalf("pre-cancel admission: %+v / %v", prefix, err)
				}
				cancel(errors.New("private-cancellation-cause"))
			}
			wantErr, wantReason := ErrTransport, "transport"
			if test.beforeSubmit {
				wantErr, wantReason = ErrCanceled, "canceled"
			}
			select {
			case err := <-result:
				if !errors.Is(err, wantErr) {
					t.Fatalf("canceled SDK call: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("canceled SDK call did not return")
			}
			diagnostic, ok := owner.PrivateRefusal()
			if !ok || diagnostic.Reason != wantReason || diagnostic.ContextStatus != "canceled" ||
				diagnostic.OwnerContextStatus != "active" || diagnostic.Method != "" || diagnostic.SQLPrefix != "" {
				t.Fatalf("generic diagnostic: %+v", diagnostic)
			}
			found := false
			frames := runtime.CallersFrames(diagnostic.Callers[:])
			for {
				frame, more := frames.Next()
				found = found || strings.Contains(frame.Function, "TestStoreAccountingSDKCancellationDiagnostics")
				if !more {
					break
				}
			}
			if !found {
				t.Fatal("bounded stack omitted the SDK call site")
			}
			if strings.Contains(output.String(), "private-") || strings.Count(output.String(), "\n") != 1 ||
				!strings.Contains(output.String(), "caller_lines=[") || strings.Contains(output.String(), "caller_lines=[0 0 0 0 0 0]") {
				t.Fatalf("generic log leaked inputs or omitted source lines: %q", output.String())
			}
			final, parentErr := waitSDKFailure(t, ctx, controller)
			if !errors.Is(parentErr, ErrIncomplete) || final.Producers[0].Calls != test.wantOpen ||
				final.Producers[0].Transactions != 0 || final.Transactions != test.wantTransactions || final.Rows != test.wantRows ||
				final.Complete || ctx.Err() != nil || native.calls != test.wantNative || !errors.Is(owner.Check(ctx), wantErr) {
				t.Fatalf("signature native=%d parent=%+v / %v context=%v", native.calls, final, parentErr, ctx.Err())
			}
		})
	}
}

func TestStoreAccountingSDKHealthyHasNoDiagnostic(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	for _, recipe := range []SDKQueryRecipe{SDKRead(), SDKWrite(1), SDKRead()} {
		if _, err := SDKQuery[[]int](ctx, owner, db, "scripted success", nil, recipe); err != nil {
			t.Fatal(err)
		}
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	final, err := controller.Snapshot()
	if err != nil || native.calls != 3 || final.Transactions != 1 || final.Rows != 1 ||
		final.Producers[0].Calls != 0 || final.Producers[0].Transactions != 0 || !final.Producers[0].Closed {
		t.Fatalf("normal native=%d snapshot=%+v / %v", native.calls, final, err)
	}
	if _, ok := owner.PrivateRefusal(); ok {
		t.Fatal("successful SDK work invented a failure record")
	}
}

func waitSDKFailure(t *testing.T, ctx context.Context, controller *Controller) (Snapshot, error) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := controller.Snapshot()
		if err != nil {
			return snapshot, err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("parent did not observe failure before fixture cleanup")
		}
	}
}

func TestStoreAccountingSDKDiagnosticConcurrentFirst(t *testing.T) {
	previous := log.Writer()
	t.Cleanup(func() { log.SetOutput(previous) })
	var output bytes.Buffer
	log.SetOutput(&output)
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	var returned [16]error
	for index := range returned {
		group.Go(func() {
			<-start
			reason := ErrTransport
			if index%2 == 0 {
				reason = ErrLimit
			}
			returned[index] = owner.fail(ctx, reason)
		})
	}
	close(start)
	group.Wait()
	wantReason := "transport"
	if returned[0] == ErrLimit {
		wantReason = "limit"
	}
	first, ok := owner.PrivateRefusal()
	if !ok || first.Reason != wantReason ||
		first.ContextStatus != "active" || first.OwnerContextStatus != "active" ||
		first.Method != "" || first.SQLPrefix != "" || first.Callers[0] == 0 {
		t.Fatalf("first concurrent diagnostic: %+v", first)
	}
	for _, err := range returned {
		if err != returned[0] {
			t.Fatal("concurrent failures replaced the first error")
		}
	}
	_, parentErr := waitSDKFailure(t, ctx, controller)
	if !errors.Is(parentErr, returned[0]) {
		t.Fatalf("parent failure changed: %v", parentErr)
	}
	_ = owner.failRecipe(ctx, "later private SQL")
	if later, _ := owner.PrivateRefusal(); later != first || strings.Count(output.String(), "\n") != 1 ||
		strings.Contains(output.String(), "later private SQL") {
		t.Fatal("first record was replaced or output more than once")
	}
}

func TestStoreAccountingSDKDiagnosticContextPrivacy(t *testing.T) {
	for _, state := range []string{"active", "canceled", "deadline_exceeded", "missing"} {
		t.Run(state, func(t *testing.T) {
			previous := log.Writer()
			t.Cleanup(func() { log.SetOutput(previous) })
			var output bytes.Buffer
			log.SetOutput(&output)
			ctx, owner, _ := storeAccountingFixture(t, 40, 2)
			callCtx := ctx
			switch state {
			case "canceled":
				var cancel context.CancelCauseFunc
				callCtx, cancel = context.WithCancelCause(ctx)
				cancel(errors.New("private-context-cause"))
			case "deadline_exceeded":
				var cancel context.CancelFunc
				callCtx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			case "missing":
				callCtx = nil
			}
			reason := errors.New("private-arbitrary-error")
			if err := owner.fail(callCtx, reason); err != reason {
				t.Fatal("diagnostic changed the original error")
			}
			diagnostic, ok := owner.PrivateRefusal()
			if !ok || diagnostic.Reason != "protocol" || diagnostic.ContextStatus != state ||
				diagnostic.OwnerContextStatus != "active" || strings.Contains(output.String(), "private-context-cause") ||
				strings.Contains(output.String(), "private-arbitrary-error") {
				t.Fatalf("unclosed diagnostic vocabulary: %+v", diagnostic)
			}
		})
	}
}

type sdkFailureWriter func([]byte) (int, error)

func (write sdkFailureWriter) Write(data []byte) (int, error) { return write(data) }

func TestStoreAccountingSDKDiagnosticLoggerFailure(t *testing.T) {
	for _, descriptor := range []bool{false, true} {
		for _, mode := range []string{"blocked", "panic"} {
			name := "generic/" + mode
			if descriptor {
				name = "descriptor/" + mode
			}
			t.Run(name, func(t *testing.T) {
				previous := log.Writer()
				t.Cleanup(func() { log.SetOutput(previous) })
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				defer unblock()
				log.SetOutput(sdkFailureWriter(func(data []byte) (int, error) {
					close(entered)
					if mode == "panic" {
						panic("broken advisory logger")
					}
					select {
					case <-release:
					case <-ctx.Done():
					}
					return len(data), nil
				}))
				result := make(chan error, 1)
				go func() {
					if descriptor {
						result <- owner.failRecipe(ctx, "diagnostic descriptor")
						return
					}
					result <- owner.fail(ctx, ErrTransport)
				}()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("advisory output was not reached")
				}
				want := ErrTransport
				if descriptor {
					want = ErrDescriptor
				}
				_, parentErr := waitSDKFailure(t, ctx, controller)
				if !errors.Is(parentErr, want) || ctx.Err() != nil {
					t.Fatalf("logger blocked or changed parent failure: %v / %v", parentErr, ctx.Err())
				}
				if _, ok := owner.PrivateRefusal(); !ok {
					t.Fatal("capture was not available before logger returned")
				}
				if mode == "blocked" {
					select {
					case <-result:
						t.Fatal("logger was not actually blocked")
					default:
					}
				}
				unblock()
				select {
				case err := <-result:
					if !errors.Is(err, want) {
						t.Fatalf("logger changed SDK error: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("advisory output did not finish")
				}
			})
		}
	}
}
