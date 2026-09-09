package t421

import (
	"context"
	"time"
)

type epochAdmissionFailure struct {
	Stage           string
	Site            uint32
	Check, Context  error
	DeadlineExpired bool
}

// Failure-only publication avoids taking run.mu under the callback's existing
// author/config locks. No success-path allocation, clock read or new watcher.
func (run *ExecutionEpochOneRun) recordAdmissionFailure(ctx context.Context, site uint32, stage string, err error) {
	deadline, bounded := ctx.Deadline()
	failure := &epochAdmissionFailure{Stage: stage, Site: site, Check: err, Context: context.Cause(ctx),
		DeadlineExpired: bounded && !time.Now().Before(deadline)}
	run.admissionFailure.CompareAndSwap(nil, failure)
}

// Private troubleshooting only: never added to ExecutionEpochOneResult, wire
// evidence or an admission decision. Before records sequential observations
// before parent teardown, not a globally ordered first-failure assertion.
type epochStopDiagnostic struct {
	Wake   string
	Before struct {
		Context, Dispatch, Control, Store, Existing, WakeError, Wait error
		Admission                                                    *epochAdmissionFailure
	}
	AdmissionAfterJoin                            *epochAdmissionFailure
	DispatchReceiver, DispatchJoin, StoreJoin     error
	DispatchFinal, StoreFinal, NativeStop, Output error
}

func (run *ExecutionEpochOneRun) observeStopDiagnostic(ctx context.Context, wake string, wakeErr, waitErr error) epochStopDiagnostic {
	diagnostic := epochStopDiagnostic{Wake: wake}
	diagnostic.Before.Context = context.Cause(ctx)
	diagnostic.Before.Admission = run.admissionFailure.Load()
	diagnostic.Before.WakeError, diagnostic.Before.Wait = wakeErr, waitErr
	if run.flow != nil && run.flow.controller != nil {
		diagnostic.Before.Dispatch = context.Cause(run.flow.controller.Context())
	}
	if run.control != nil && run.control.Context() != nil {
		diagnostic.Before.Control = context.Cause(run.control.Context())
	}
	run.mu.Lock()
	diagnostic.Before.Existing = run.err
	run.mu.Unlock()
	if run.flow != nil && run.flow.store != nil {
		_, diagnostic.Before.Store = run.flow.store.Snapshot()
	}
	return diagnostic
}
