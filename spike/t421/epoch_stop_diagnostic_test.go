package t421

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestExecutionEpochAdmissionFailurePreservesCheckDeadline(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				run := &ExecutionEpochOneRun{}
				if expired {
					time.Sleep(6 * time.Second)
				}
				run.recordAdmissionFailure(ctx, 17, "epoch_config", ErrExecutionEpochConfigs)
				first := run.admissionFailure.Load()
				if first == nil || first.Site != 17 || first.Check != ErrExecutionEpochConfigs || first.DeadlineExpired != expired ||
					(first.Context == context.DeadlineExceeded) != expired {
					t.Fatal("check failure lost deadline distinction", first)
				}
				before := run.observeStopDiagnostic(ctx, "dispatch_context", ErrExecutionEpochOne, nil)
				cancel()
				run.recordAdmissionFailure(ctx, 18, "epoch_tools", context.Canceled)
				if run.admissionFailure.Load() != first || before.Before.Admission != first {
					t.Fatal("cleanup replaced original check")
				}
			})
		})
	}
}

func TestExecutionEpochStopDiagnosticPreservesBeforeTeardown(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, mode := range []string{"dispatch", "store", "context", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(context.Canceled)
			config, err := executionDispatchConfig(plan, testExecutionDispatchBindings())
			if err != nil {
				t.Fatal(err)
			}
			dispatch, err := dispatchadmission.New(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			sa, wire, err := executionStoreConfig(plan, testExecutionDispatchBindings())
			if err != nil {
				t.Fatal(err)
			}
			store, err := storeaccounting.New(ctx, sa)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := storeaccounting.NewTransport(ctx, store, wire)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{controller: dispatch, store: transport}, inspection: &executionEpochInspection{}}
			switch mode {
			case "dispatch":
				if dispatch.Advance() != dispatchadmission.ErrProtocol {
					t.Fatal("fixture must refuse unfenced advance")
				}
			case "store":
				_ = store.Fail(storeaccounting.ErrLimit)
			case "context":
				cancel(context.DeadlineExceeded)
			}
			// A real inspection owns this mutex throughout HTTP. The stop
			// snapshot must not wait for the cancellation it has yet to issue.
			run.inspection.mu.Lock()
			locked := true
			defer func() {
				if locked {
					run.inspection.mu.Unlock()
				}
			}()
			done := make(chan epochStopDiagnostic, 1)
			go func() { done <- run.observeStopDiagnostic(ctx, "stop_requested", nil, nil) }()
			var diagnostic epochStopDiagnostic
			select {
			case diagnostic = <-done:
			case <-time.After(time.Second):
				t.Fatal("diagnostic waited for live reader")
			}
			run.inspection.mu.Unlock()
			locked = false
			before := diagnostic.Before
			if (before.Dispatch == dispatchadmission.ErrProtocol) != (mode == "dispatch") ||
				(before.Store == storeaccounting.ErrLimit) != (mode == "store") ||
				(before.Context == context.DeadlineExceeded) != (mode == "context") {
				t.Fatalf("initial causes lost: %+v", diagnostic)
			}
			cancel(context.Canceled)
			diagnostic.DispatchFinal = context.Cause(dispatch.Context())
			_, diagnostic.StoreFinal = transport.Snapshot()
			diagnostic.NativeStop = errors.New("later forced shutdown")
			if diagnostic.Before != before || diagnostic.Wake != "stop_requested" {
				t.Fatal("cleanup overwrote pre-teardown observations")
			}
			if mode == "healthy" && (before.Context != nil || before.Dispatch != nil || before.Store != nil) {
				t.Fatal("healthy pre-stop invented failure", diagnostic)
			}
		})
	}
}

func TestEpochInspectionFailureDiagnosticBeforeHTTP(t *testing.T) {
	for _, mode := range []string{"context", "run", "token", "http"} {
		t.Run(mode, func(t *testing.T) {
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP") }))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantStage, wantNext := "preflight", uint64(1)
			switch mode {
			case "context":
				cancel()
			case "run":
				reader.run.stopping, wantStage = true, "run_unavailable"
			case "token":
				_ = reader.run.control.Close()
				wantStage = "request_token"
			case "http":
				reader.run.epoch.Listen = "127.0.0.1:0"
				wantStage, wantNext = "http_exchange", 2
			}
			if _, _, _, err := reader.read(ctx, "/private?secret=must-not-retain", 4096, epochInspectionReport{}); err != errEpochInspection {
				t.Fatal(err)
			}
			first := reader.readFailure
			if first.Stage != wantStage || first.Ordinal != 1 || first.Cause == nil || reader.next != wantNext || reader.failureStatus != 0 || reader.reports != 0 {
				t.Fatal("pre-HTTP diagnostic or refusal prefix lost", first, reader.next)
			}
			if strings.Contains(fmt.Sprint(first), "must-not-retain") || strings.Contains(fmt.Sprint(first), "/private") {
				t.Fatal("diagnostic retained request URL")
			}
			if mode == "context" && !errors.Is(first.Cause, context.Canceled) {
				t.Fatal("context cause lost")
			}
			reader.fail(errEpochInspection)
			cancel()
			_, _, _, _ = reader.read(ctx, "/later", 4096, epochInspectionReport{})
			if reader.readFailure != first {
				t.Fatal("later cancellation replaced initial refusal")
			}
		})
	}
}
