package t421

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// FirstObserved is troubleshooting order, not causal attribution. No arbitrary
// error text, input digest, URL, body or log content enters this fixed record.
type epochObservedFailure struct {
	Stage, Class string
}

func (run *ExecutionEpochOneRun) recordObservedFailure(stage string, cause error) {
	if run == nil || run.observedFailure.Load() != nil {
		return
	}
	switch stage {
	case "logical/admission", "logical/predecessor stop", "logical/prior prefix", "logical/prior catalog",
		"logical/retained binding", "logical/parent checkpoint", "logical/store fence", "logical/dispatch advance",
		"logical/store advance", "logical/parent resume", "logical/parent sample", "logical/launch",
		"finish/supplied failure", "finish/run context", "finish/dispatch context", "finish/native wait",
		"finish/existing state", "finish/owner drain", "finish/owner pause", "finish/parent pause", "finish/dispatch fence",
		"finish/process observation close", "finish/partial terminal setup", "finish/native stop",
		"finish/engine measurement endpoint", "finish/dispatch receiver", "finish/dispatch join", "finish/terminal request",
		"finish/process state", "finish/hard-death close", "finish/store join", "finish/phase control close", "finish/parent close",
		"finish/process observation snapshot", "finish/health-stopped event", "finish/backup join", "finish/session empty",
		"finish/dispatch snapshot", "finish/store snapshot", "finish/terminal context", "finish/final state",
		"finish/closed prefix", "finish/attempt observation", "finish/joined work":
	default:
		stage = "unclassified"
	}
	class := "refused"
	switch {
	case cause == nil:
		class = "unspecified"
	case errors.Is(cause, context.DeadlineExceeded):
		class = "deadline"
	case errors.Is(cause, context.Canceled):
		class = "canceled"
	case errors.Is(cause, errExecutionAttempts):
		class = "attempt_observation"
	case errors.Is(cause, errEpochInspection):
		class = "inspection"
	case errors.Is(cause, ErrExecutionEpochOne):
		class = "epoch_refusal"
	}
	run.observedFailure.CompareAndSwap(nil, &epochObservedFailure{Stage: stage, Class: class})
}

type epochJoinedDispatchPrefix struct {
	Present bool
	Count   dispatchadmission.ProducerCount
}

type epochJoinedStorePrefix struct {
	Present bool
	Count   storeaccounting.ProducerCount
}

type epochJoinedMetricFlags struct {
	Attempts, Scan, IndexBound, IndexScan, Index                            bool
	Source, Attempt, Observation, Publication, Resolver, Relationship       bool
	Lifecycle, Cache, SourceCensus, CatalogCensus, Unsupported, Reuse       bool
	WorkspaceBound, WorkspaceComplete, WorkspaceUnavailable, WorkspaceLimit bool
	EarlyUnavailable, EarlyLimit, MidphaseUnavailable, MidphaseLimit        bool
	ParentUnavailable, ParentLimit                                          bool
}

// Only fixed values from the saved result; no controller reads, probes,
// parsing, original logs, or slice/pointer ownership escapes this snapshot.
type epochJoinedFailurePrefix struct {
	FirstObserved                         epochObservedFailure
	RootStarted, RootJoined, SessionEmpty bool
	Dispatch                              [3]epochJoinedDispatchPrefix // Producers one, two, eight only.
	Store                                 epochJoinedStorePrefix       // Producer two only.
	StoreOpened, StoreEOF                 int
	Flags                                 epochJoinedMetricFlags
	Cold, Physical                        ExecutionAttemptCount
}

func (run *ExecutionEpochOneRun) joinedFailurePrefix() (epochJoinedFailurePrefix, bool) {
	if run == nil || run.done == nil {
		return epochJoinedFailurePrefix{}, false
	}
	select {
	case <-run.done:
	default:
		return epochJoinedFailurePrefix{}, false
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	result := &run.result
	prefix := epochJoinedFailurePrefix{RootStarted: result.RootStarted, RootJoined: result.RootJoined, SessionEmpty: result.SessionEmpty,
		StoreOpened: result.Store.Opened, StoreEOF: result.Store.TerminalEOF, Cold: result.Attempts.Phases[1], Physical: result.Attempts.Phases[3]}
	if first := run.observedFailure.Load(); first != nil {
		prefix.FirstObserved = *first
	}
	for _, row := range result.Accounting.Producers {
		for index, producer := range [...]uint32{1, 2, 8} {
			if row.Producer == producer {
				prefix.Dispatch[index] = epochJoinedDispatchPrefix{Present: true, Count: row}
			}
		}
	}
	for _, row := range result.Store.Store.Producers {
		if row.Producer == 2 {
			prefix.Store = epochJoinedStorePrefix{Present: true, Count: row}
		}
	}
	a, w := &result.Attempts, &result.Attempts.WorkspaceBytes
	prefix.Flags = epochJoinedMetricFlags{Attempts: a.Complete, Scan: a.ScanComplete,
		IndexBound: result.IndexOffers.Bound, IndexScan: result.IndexOffers.ScanComplete, Index: result.IndexOffers.Complete,
		Source: a.SourceBound, Attempt: a.AttemptBound, Observation: a.ObservationBound, Publication: a.PublicationBound,
		Resolver: a.ResolverBound, Relationship: a.RelationshipBound, Lifecycle: a.Lifecycle.Complete, Cache: a.Cache.Complete,
		SourceCensus: a.SourceCensus.Complete, CatalogCensus: a.CatalogCensus.Complete, Unsupported: a.UnsupportedSource.Complete,
		Reuse: a.Reuse.Complete, WorkspaceBound: w.Bound, WorkspaceComplete: w.Complete, WorkspaceUnavailable: w.Unavailable, WorkspaceLimit: w.LimitExceeded,
		EarlyUnavailable: result.EarlyFinishSamples.Unavailable, EarlyLimit: result.EarlyFinishSamples.LimitExceeded,
		MidphaseUnavailable: result.MidphaseSamples.Unavailable, MidphaseLimit: result.MidphaseSamples.LimitExceeded,
		ParentUnavailable: result.ParentMidphaseSamples.Unavailable, ParentLimit: result.ParentMidphaseSamples.LimitExceeded}
	return prefix, true
}

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
