package t421

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestEpochFirstObservedFailurePreservesErrorAndPrivacy(t *testing.T) {
	var absent *ExecutionEpochOneRun
	absent.recordObservedFailure("logical/admission", nil)
	concurrent := &ExecutionEpochOneRun{}
	start := make(chan struct{})
	var writers sync.WaitGroup
	for index := range 8 {
		writers.Go(func() {
			<-start
			if index%2 == 0 {
				concurrent.recordObservedFailure("logical/predecessor stop", context.Canceled)
			} else {
				concurrent.recordObservedFailure("finish/closed prefix", nil)
			}
		})
	}
	close(start)
	writers.Wait()
	first := concurrent.observedFailure.Load()
	if first == nil || *first != (epochObservedFailure{Stage: "logical/predecessor stop", Class: "canceled"}) &&
		*first != (epochObservedFailure{Stage: "finish/closed prefix", Class: "unspecified"}) {
		t.Fatal("concurrent first writers published a missing or mixed record", first)
	}
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			run := &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 3}}
			err := epochFinishFailure(run, terminal, nil, "attempt observation", errExecutionAttempts)
			if !terminal && err != ErrExecutionEpochOne {
				t.Fatal("ordinary sentinel identity changed")
			}
			if terminal {
				prior := checkpointRestartError("prior finish attempt observation", errExecutionAttempts)
				if err.Error() != prior.Error() || errors.Is(err, errExecutionAttempts) != errors.Is(prior, errExecutionAttempts) {
					t.Fatal("terminal wrapping changed")
				}
			}
			first := run.observedFailure.Load()
			if first == nil || *first != (epochObservedFailure{Stage: "finish/attempt observation", Class: "attempt_observation"}) {
				t.Fatal("ordinary parser refusal lost its first observed stage", first)
			}
			var joined sync.WaitGroup
			for range 8 {
				joined.Go(func() { run.recordObservedFailure("logical/predecessor stop", context.Canceled) })
			}
			joined.Wait()
			if run.observedFailure.Load() != first {
				t.Fatal("later teardown/handoff replaced the first observation")
			}
			original := errors.New("private diagnostic URL/body/token must not escape")
			if epochFinishFailure(run, false, original, "closed prefix", nil) != original {
				t.Fatal("existing ordinary failure replaced")
			}
		})
	}
	for _, test := range []struct {
		cause error
		class string
	}{{nil, "unspecified"}, {context.DeadlineExceeded, "deadline"}, {context.Canceled, "canceled"},
		{ErrExecutionEpochOne, "epoch_refusal"}, {errEpochInspection, "inspection"}, {errors.New("secret-URL-body-token"), "refused"}} {
		run := &ExecutionEpochOneRun{}
		run.recordObservedFailure("secret-URL-body-token", test.cause)
		got := run.observedFailure.Load()
		if got == nil || got.Stage != "unclassified" || got.Class != test.class || strings.Contains(fmt.Sprint(got), "secret") {
			t.Fatal("unbounded diagnostic stage or error content retained", got)
		}
	}
}

// Supplied joined-stop states isolate the guarded refusal stages. They grant
// no native join, protected source, accounting acceptance, or successor claim.
func TestEpochLogicalFirstObservedRefusal(t *testing.T) {
	for _, mode := range []string{"admission", "predecessor stop", "prior prefix", "prior catalog", "parent checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			closed := make(chan struct{})
			close(closed)
			flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}}
			reader := &executionEpochInspection{finalUsed: true, retentionUsed: true, physicalAuthority: AuthorityPhaseResult{Phase: "physical_delta_b"}}
			run := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), done: closed, physicalUsed: true, physicalDone: closed,
				inspection: reader, phaseTimer: time.NewTimer(time.Hour), phaseDeadline: time.Now().Add(time.Hour)}
			defer run.phaseTimer.Stop()
			switch mode {
			case "admission":
				run.physicalUsed = false
			case "predecessor stop":
				run.done = make(chan struct{})
				joined := make(chan struct{})
				go func() {
					defer close(joined)
					<-run.stop
					run.mu.Lock()
					run.err = errExecutionAttempts
					run.mu.Unlock()
					close(run.done)
				}()
				t.Cleanup(func() {
					run.stopOnce.Do(func() { close(run.stop) })
					<-joined
				})
			case "prior prefix":
				reader.retentionUsed = false
			case "parent checkpoint":
				reader.evidence.rows = []ExecutionPhaseInspection{{Phase: "physical_delta_b", SelectorAccepted: true,
					Final: &ExecutionInspectionFinal{Ordinal: 1, Authority: AuthorityState{Current: true}, Projection: PhaseStateProjection{Phase: "physical_delta_b"}}}}
			}
			next, err := run.StartLogicalB(t.Context())
			first := run.observedFailure.Load()
			if next != nil || err != ErrExecutionEpochOne || first == nil || first.Stage != "logical/"+mode {
				t.Fatal("logical refusal erased the earliest guarded stage", mode, first, err)
			}
		})
	}
}

func TestEpochJoinedFailurePrefixUsesOnlySavedJoinedValues(t *testing.T) {
	run := &ExecutionEpochOneRun{done: make(chan struct{}), result: ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true}}
	run.mu.Lock()
	if _, ok := run.joinedFailurePrefix(); ok {
		t.Fatal("unjoined state exposed")
	}
	run.mu.Unlock()
	run.recordObservedFailure("finish/closed prefix", nil)
	run.result.Accounting.Producers = []dispatchadmission.ProducerCount{
		{Producer: 1, Attached: true, Ordinal: 3}, {Producer: 2, Attached: true, Closed: true}, {Producer: 8, Attached: true, Closed: true, Ordinal: 3},
		{Producer: 99, Ordinal: 1234},
	}
	run.result.Store = storeaccounting.WireSnapshot{Opened: 1, TerminalEOF: 1, Store: storeaccounting.Snapshot{Producers: []storeaccounting.ProducerCount{{Producer: 2, Attached: true, Closed: true, Ordinal: 17}}}}
	run.result.Attempts.ResolverBound = true
	run.result.Attempts.Phases[1].ResolverBlobBytes = 9_776_409
	run.result.Attempts.WorkspaceBytes.Unavailable = true
	run.result.IndexOffers.ScanComplete = true
	close(run.done)
	prefix, ok := run.joinedFailurePrefix()
	if !ok || !prefix.RootJoined || !prefix.SessionEmpty || prefix.FirstObserved.Stage != "finish/closed prefix" || prefix.StoreOpened != 1 || prefix.StoreEOF != 1 ||
		!prefix.Dispatch[0].Present || prefix.Dispatch[0].Count.Ordinal != 3 || !prefix.Dispatch[1].Count.Closed || !prefix.Dispatch[2].Count.Closed ||
		!prefix.Store.Present || prefix.Store.Count.Ordinal != 17 || !prefix.Flags.Resolver || !prefix.Flags.IndexScan || prefix.Flags.Attempts ||
		!prefix.Flags.WorkspaceUnavailable || prefix.Cold.ResolverBlobBytes != 9_776_409 {
		t.Fatal("saved partial/counter refusal was lost", prefix)
	}
	prefix.Dispatch[0].Count.Ordinal = 99
	prefix.FirstObserved.Stage = "caller mutation"
	again, _ := run.joinedFailurePrefix()
	if again.Dispatch[0].Count.Ordinal != 3 || again.FirstObserved.Stage != "finish/closed prefix" {
		t.Fatal("caller mutated owned saved evidence")
	}
}

func TestEpochResolverRepeatedNativeBytesRetainOverflow(t *testing.T) {
	plan := accountingTestPlan(t)
	bound := plan.WorkEnvelope.Phases[1].ResolverBlobBytes.Maximum
	// Native v12's first over-budget successful return was 781B. The preceding
	// successful prefix and this return remain charged, even though reads fit.
	observation := ExecutionAttemptObservation{ResolverBound: true}
	observation.Phases[1] = ExecutionAttemptCount{ResolverBlobReads: 7_486, ResolverBlobBytes: bound - 465}
	observed, err := observeResolverEvent([]byte("RM1:2:2:000000000000030d\n"), plan, 2, "unused-bound-input", &observation)
	if !observed || err != errExecutionAttempts || observation.Phases[1].ResolverBlobReads != 7_487 || observation.Phases[1].ResolverBlobBytes != bound+316 ||
		observation.Phases[1].ResolverBlobReads > plan.WorkEnvelope.Phases[1].ResolverBlobReads.Maximum {
		t.Fatal("real repeated byte excess was clamped, ignored, or misclassified", observation, err)
	}
}

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
