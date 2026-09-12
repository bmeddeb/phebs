package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t422StalePreparePath   = "/api/t422/stale-lease/prepare"
	t422StaleHitPath       = "/api/t422/stale-lease/hit"
	t422StaleRecoveredPath = "/api/t422/stale-lease/recovered"
	t422StaleMaximum       = 4 * time.Hour
)

var errT422StaleControl = errors.New("T42.2 selected stale lease control refused")

// Only scalar native F identities survive its request. No candidate, result,
// projection, or reader graph is retained across the phase boundary.
type t422StaleFinal struct {
	authority  extractionpublication.PlanningAuthority
	final      t421FinalAuthorityState
	generation string
	roots      [9]extractionpublication.RecoveryPreparationRoot
}

type t422StaleBarrier struct {
	t422MarkerBarrier
	observer   context.Context
	transition store.GenerationStaleLeaseTransition
}

type t422StaleControl struct {
	ctx                                   context.Context
	cancel                                context.CancelFunc
	launch                                *t422SemanticLaunch
	reconciler                            *extractionpublication.Reconciler
	sink                                  func([]byte) error
	workspacePreparation                  func(context.Context) (*t422WorkspaceSampleResponse, error)
	mu                                    sync.Mutex
	final                                 t422StaleFinal
	captured, confirmed, preparing, armed bool
	target                                extractionpublication.RecoveryPreparationTarget
	old                                   store.GenerationChunk
	reclaimed                             bool
	reclaimedLease                        string
	phaseEnd                              time.Time
	requeued                              chan struct{}
	requeueSeen                           bool
	hit, recovered                        t422StaleBarrier
	err                                   error
	diagnose                              bool // Stale component only; embedded checkpoint controls preserve terminal output.
	privateFailure                        *t422StaleFailure
}

func newT422StaleControl(ctx context.Context, launch *t422SemanticLaunch, reconciler *extractionpublication.Reconciler) (*t422StaleControl, error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || launch.request.ServerEpoch != 3 ||
		launch.initial.ProducerID != 4 || launch.initial.Phase != 6 || err != nil || !launch.matches(current) || current.Phase != 6 ||
		reconciler == nil || reconciler.Runtime == nil || reconciler.Runtime.Fence == nil || !reconciler.StoreAccounting ||
		reconciler.RecoveryPreparationEnabled {
		return nil, errT422StaleControl
	}
	lifetime, cancel := context.WithCancel(ctx)
	control := &t422StaleControl{ctx: lifetime, cancel: cancel, launch: launch, reconciler: reconciler,
		sink: t4013ExactReportSink("exact stale preparation: "), requeued: make(chan struct{}), diagnose: true}
	for _, barrier := range []*t422StaleBarrier{&control.hit, &control.recovered} {
		barrier.ready, barrier.release = make(chan struct{}), make(chan struct{})
	}
	reconciler.RecoveryPreparationEnabled = true
	return control, nil
}

func (control *t422StaleControl) current(ctx context.Context, phase uint32, request, drained bool) bool {
	if ctx == nil || ctx.Err() != nil || control.ctx.Err() != nil {
		return false
	}
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil || !control.launch.matches(current) || current.ProducerID != 4 || current.Phase != phase ||
		(drained && !current.OrdinaryOwnersDrained) {
		return false
	}
	if !request {
		return true
	}
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	return ok && control.launch.sameRequest(admitted, current) && (!drained || admitted.OrdinaryOwnersDrained)
}

// Called after the native F's last confirmation, without additional I/O. Its
// authority remains unusable until the shared exact body/report tail joins.
func (control *t422StaleControl) captureFinal(ctx context.Context, state candidate.State, response t421FinalAuthorityResponse) error {
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		return control.stop(errT422StaleControl)
	}
	if current.Phase != 6 && current.Phase != 7 {
		return nil // This control owns only the phase-six/seven boundary.
	}
	if !control.current(ctx, current.Phase, true, true) {
		return control.stop(errT422StaleControl)
	}
	value, err := t422StaleFinalSnapshot(state, response)
	if err != nil {
		return control.stop(err)
	}
	control.mu.Lock()
	if current.Phase == 7 {
		valid := control.err == nil && control.recovered.reported && value == control.final
		control.mu.Unlock()
		if !valid {
			return control.stop(errT422StaleControl)
		}
		return nil
	}
	valid := control.err == nil && !control.captured
	if valid {
		control.final, control.captured = value, true
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl)
	}
	return nil
}

func t422StaleFinalSnapshot(state candidate.State, response t421FinalAuthorityResponse) (t422StaleFinal, error) {
	var value t422StaleFinal
	policy, err := candidate.ExtractionPolicyDigest(state.PolicyDigest, true)
	if err != nil || response.Schema != t421FinalAuthoritySchema || !response.Authority.Current || len(response.ExtractionRoots) != len(value.roots) ||
		state.GenerationDigest != response.Authority.CandidateGenerationSHA256 || state.Commit != response.Authority.PhysicalCommit ||
		!t422SemanticDigest(state.ManifestDigest) || !t422SemanticDigest(response.Authority.SourceGenerationSHA256) ||
		!t422SemanticDigest(response.Authority.ObservationGenerationSHA256) {
		return value, errT422StaleControl
	}
	value.authority = extractionpublication.PlanningAuthority{Repository: state.Repository,
		CandidateManifestDigest: state.ManifestDigest, CandidateGenerationDigest: state.GenerationDigest,
		CandidatePolicyDigest: state.PolicyDigest, ExtractionPolicyDigest: policy,
		SourceGenerationDigest: response.Authority.SourceGenerationSHA256, ObservationGenerationDigest: response.Authority.ObservationGenerationSHA256}
	value.final = response.Authority
	var partitions uint64
	found := false
	for i, root := range response.ExtractionRoots {
		if !root.Current || !t422SemanticDigest(root.GenerationSHA256) || !t422SemanticDigest(root.PlanSHA256) || !t422SemanticDigest(root.RootSHA256) ||
			root.CandidateGenerationSHA256 != value.authority.CandidateGenerationDigest || root.SourceGenerationSHA256 != value.authority.SourceGenerationDigest ||
			root.ObservationGenerationSHA256 != value.authority.ObservationGenerationDigest || root.ScheduleSHA256 != "" ||
			(i != 0 && (root.GenerationSHA256 != value.generation || root.Domain <= value.roots[i-1].Domain)) || root.ApplicablePartitions > 56 {
			return t422StaleFinal{}, errT422StaleControl
		}
		value.generation = root.GenerationSHA256
		value.roots[i] = extractionpublication.RecoveryPreparationRoot{Domain: root.Domain, PlanDigest: root.PlanSHA256, RootDigest: root.RootSHA256}
		partitions += root.ApplicablePartitions
		found = found || root.Domain == "grpc-caller" && root.ApplicablePartitions > 6
	}
	if !found || partitions != 56 {
		return t422StaleFinal{}, errT422StaleControl
	}
	return value, nil
}

func (control *t422StaleControl) finalTail(ctx context.Context) (func(error), error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil || current.Phase != 6 {
		return nil, nil
	}
	if !control.current(ctx, 6, true, true) {
		return nil, control.stop(errT422StaleControl)
	}
	return func(cause error) {
		valid := cause == nil && control.current(ctx, 6, true, true)
		control.mu.Lock()
		valid = valid && control.err == nil && control.captured && !control.confirmed
		if valid {
			control.confirmed = true
		}
		control.mu.Unlock()
		if !valid {
			_ = control.stop(errors.Join(cause, errT422StaleControl))
		}
	}, nil
}

// These are the unchanged native V3 preparation ceilings for D=9/P=56:
// C=(24+4D+P)+one binding branch+one cold manifest, S=D+10+5*64.
func t422StalePreparationLimits() readaccounting.Counts {
	return readaccounting.Counts{ControlFileReads: 24 + 4*9 + 56 + 2, StoreReadAttempts: 9 + 10 + 5*64,
		MemberVisits: 353296640, StoreWriteAttempts: 64}
}

type t422StalePreparationObservation struct {
	Schema             string                       `json:"schema"`
	Authority          t421FinalAuthorityState      `json:"authority"`
	TargetGeneration   string                       `json:"target_generation"`
	PriorSchedule      string                       `json:"prior_schedule"`
	RecoveryGeneration string                       `json:"recovery_generation"`
	RecoverySchedule   string                       `json:"recovery_schedule"`
	Domain             string                       `json:"domain"`
	Ordinal            int                          `json:"ordinal"`
	Offset             int                          `json:"offset"`
	PlanDigest         string                       `json:"plan_digest"`
	ResultIdentity     string                       `json:"result_identity"`
	ControlFileReads   uint64                       `json:"control_file_reads"`
	StoreReadAttempts  uint64                       `json:"store_read_attempts"`
	MemberReads        uint64                       `json:"member_reads"`
	StoreWriteAttempts uint64                       `json:"store_write_attempts"`
	Workspace          *t422WorkspaceSampleResponse `json:"workspace,omitempty"`
}

func (control *t422StaleControl) command(writer http.ResponseWriter, request *http.Request) {
	principal, authenticated := auth.PrincipalFromContext(request.Context())
	if !t422StaleRequest(request, t422StalePreparePath) || !authenticated || !t421ExactReadLegacyPrincipal(principal) ||
		len(request.Header.Values(t421ExactReadActivationHeader)) != 0 || len(request.Header.Values(t421ExactReadOrdinalHeader)) != 0 ||
		!control.current(request.Context(), 7, true, true) {
		_ = control.stop(errT422StaleControl)
		http.Error(writer, "stale preparation refused", http.StatusConflict)
		return
	}
	control.mu.Lock()
	valid := control.err == nil && control.confirmed && !control.preparing && !control.armed
	if valid {
		control.preparing = true
		control.phaseEnd = time.Now().Add(t422StaleMaximum)
	}
	value := control.final
	control.mu.Unlock()
	if !valid {
		_ = control.stop(errT422StaleControl)
		http.Error(writer, "stale preparation refused", http.StatusConflict)
		return
	}
	complete := false
	defer func() {
		if !complete {
			_ = control.stop(errT422StaleControl)
		}
	}()
	operation, finish := control.operationContext(request.Context(), nil)
	defer finish()
	ctx, ledger, err := readaccounting.Start(operation, t422StalePreparationLimits())
	if err != nil {
		return
	}
	target, preparationErr := control.reconciler.PrepareCurrentRecovery(ctx, extractionpublication.CurrentRecoveryPreparationRequest{
		Authority: value.authority, GenerationDigest: value.generation, Roots: value.roots[:],
		Mode: extractionpublication.RecoveryPreparationScheduleOnly, TargetDomain: "grpc-caller", TargetOrdinal: 6})
	counts, accountingErr := ledger.Finish()
	if preparationErr != nil || accountingErr != nil || operation.Err() != nil || !control.current(request.Context(), 7, true, true) {
		_ = control.stop(errors.Join(preparationErr, accountingErr, operation.Err()))
		http.Error(writer, "stale preparation refused", http.StatusConflict)
		return
	}
	var workspace *t422WorkspaceSampleResponse
	if control.workspacePreparation != nil {
		workspace, err = control.workspacePreparation(operation)
		if err != nil {
			return
		}
	}
	body, err := json.Marshal(t422StalePreparationObservation{Schema: "t422-stale-preparation-observation-v1", Authority: value.final,
		TargetGeneration: target.TargetGeneration, PriorSchedule: target.PriorScheduleDigest,
		RecoveryGeneration: target.Schedule.Generation, RecoverySchedule: target.Schedule.Digest,
		Domain: target.Domain, Ordinal: target.Ordinal, Offset: target.Offset, PlanDigest: target.PlanDigest, ResultIdentity: target.ResultIdentity,
		ControlFileReads: counts.ControlFileReads, StoreReadAttempts: counts.StoreReadAttempts, MemberReads: counts.MemberVisits, StoreWriteAttempts: counts.StoreWriteAttempts, Workspace: workspace})
	if err != nil {
		return
	}
	body = append(body, '\n')
	writer.Header().Set("Content-Type", "application/json")
	n, err := writer.Write(body)
	if err != nil || n != len(body) || control.sink(body) != nil || operation.Err() != nil || !control.current(request.Context(), 7, true, true) {
		return
	}
	control.mu.Lock()
	valid = control.err == nil && control.preparing && !control.armed
	if valid {
		control.target, control.armed, control.preparing = target, true, false
	}
	control.mu.Unlock()
	complete = valid
}

// The selected old claim stays genuinely leased, owner-held and without a
// heartbeat. Only the real reaper's committed requeue releases this worker.
func (control *t422StaleControl) beforeHeartbeat(ctx context.Context, chunk store.GenerationChunk) error {
	control.mu.Lock()
	armed, target := control.armed, control.target
	control.mu.Unlock()
	if !armed || chunk.Repository != control.launch.request.Repository || chunk.Generation != target.Schedule.Generation || chunk.Offset != int64(target.Offset) {
		return nil
	}
	if !control.current(ctx, 7, false, false) {
		return control.stop(errT422StaleControl)
	}
	control.mu.Lock()
	valid := control.err == nil && t422StaleChunkMatches(chunk, target) && chunk.Status == store.GenerationChunkRunning && chunk.LeaseToken != ""
	recovered := chunk.Priority == store.GenerationPriorityStale
	observer := control.hit.observer
	requeueSeen := control.requeueSeen
	if recovered {
		valid = valid && observer != nil && control.hit.reported && !control.reclaimed && chunk.Identity == control.old.Identity && chunk.LeaseToken != control.old.LeaseToken
	} else {
		valid = valid && chunk.Priority == store.GenerationPriorityNeverRun && control.old.Identity == ""
		if valid {
			control.old = chunk
		}
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl)
	}
	if recovered {
		// COMMIT makes the new claim visible before the reaper's callback.
		// Rendezvous on that actual callback under its original turn deadline.
		if !requeueSeen {
			operation, finish := control.operationContext(ctx, observer)
			defer finish()
			select {
			case <-control.requeued:
			case <-operation.Done():
			}
		}
		control.mu.Lock()
		valid = control.err == nil && control.requeueSeen && !control.reclaimed
		if valid {
			control.reclaimed = true
			control.reclaimedLease = store.GenerationLeaseTokenDigest(chunk.LeaseToken)
		}
		control.mu.Unlock()
		// A completed reaper turn may cancel its context after the callback;
		// the reclaimed worker now belongs to its own caller/phase lifetime.
		if !valid || !control.current(ctx, 7, false, false) {
			return control.stop(errT422StaleControl)
		}
		return nil
	}
	operation, finish := control.operationContext(ctx, nil)
	defer finish()
	select {
	case <-operation.Done():
		return control.stop(operation.Err())
	case <-control.requeued:
	}
	control.mu.Lock()
	valid = control.err == nil && control.requeueSeen && control.hit.reported
	control.mu.Unlock()
	if !valid || operation.Err() != nil || !control.current(ctx, 7, false, false) {
		return control.stop(errT422StaleControl)
	}
	return store.ErrGenerationLeaseLost
}

func t422StaleChunkMatches(chunk store.GenerationChunk, target extractionpublication.RecoveryPreparationTarget) bool {
	return chunk.Repository == target.Schedule.Repository && chunk.Stage == extractionpublication.ScheduleStage &&
		chunk.ResourceClass == store.GenerationResourceExtraction && chunk.Generation == target.Schedule.Generation &&
		chunk.ScheduleDigest == target.Schedule.Digest && chunk.Offset == int64(target.Offset) && chunk.Length == 1 && chunk.Attempt == 0
}

func (control *t422StaleControl) transition(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
	if !control.current(ctx, 7, false, false) {
		return control.stop(errT422StaleControl)
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
		return control.stop(errT422StaleControl)
	}
	control.mu.Lock()
	old := control.old
	valid := control.err == nil && control.armed && old.Identity != "" && event.Repository == old.Repository && event.Stage == old.Stage &&
		event.ResourceClass == old.ResourceClass && event.Generation == old.Generation && event.ScheduleDigest == old.ScheduleDigest &&
		event.ChunkIdentity == old.Identity && event.Offset == old.Offset && event.Length == 1 && event.Attempt == 0
	var barrier *t422StaleBarrier
	switch event.Point {
	case store.GenerationStaleLeaseTransitionHit:
		valid = valid && control.hit.observer == nil && !control.requeueSeen && event.Priority == store.GenerationPriorityNeverRun &&
			event.ChunkStatus == store.GenerationChunkRunning && event.Leased && event.PrivateLeaseTokenDigest == store.GenerationLeaseTokenDigest(old.LeaseToken)
		barrier = &control.hit
	case store.GenerationStaleLeaseTransitionRequeued:
		valid = valid && control.hit.reported && !control.requeueSeen && event.Priority == store.GenerationPriorityStale &&
			event.ChunkStatus == store.GenerationChunkPending && !event.Leased
		if valid {
			control.requeueSeen = true
			close(control.requeued)
		}
	case store.GenerationStaleLeaseTransitionRecovered:
		valid = valid && control.requeueSeen && control.reclaimed && control.recovered.observer == nil &&
			event.Priority == store.GenerationPriorityStale && event.ChunkStatus == store.GenerationChunkDone && !event.Leased &&
			event.PrivateLeaseTokenDigest == control.reclaimedLease
		barrier = &control.recovered
	default:
		valid = false
	}
	if valid && barrier != nil {
		barrier.observer, barrier.transition = ctx, event
		close(barrier.ready)
	}
	var checks t422StaleFailedChecks
	if !valid {
		// Inspect the same locked state as the refusal, without formatting or I/O.
		checks = control.transitionFailedChecks(event)
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl, checks)
	}
	if barrier == nil {
		return nil
	}
	operation, finish := control.operationContext(ctx, nil)
	defer finish()
	select {
	case <-operation.Done():
		return control.stop(operation.Err())
	case <-barrier.release:
	}
	control.mu.Lock()
	valid = control.err == nil && barrier.reported
	control.mu.Unlock()
	if !valid || operation.Err() != nil || !control.current(ctx, 7, false, false) {
		return control.stop(errT422StaleControl)
	}
	return nil
}

func (control *t422StaleControl) read(ctx context.Context, point store.GenerationStaleLeaseTransitionPoint) ([]byte, func(error), error) {
	if !control.current(ctx, 7, true, false) {
		return nil, nil, control.stop(errT422StaleControl)
	}
	barrier := &control.hit
	if point == store.GenerationStaleLeaseTransitionRecovered {
		barrier = &control.recovered
	}
	control.mu.Lock()
	valid := control.err == nil && control.armed && !barrier.reading &&
		(point == store.GenerationStaleLeaseTransitionHit || point == store.GenerationStaleLeaseTransitionRecovered && control.hit.reported)
	if valid {
		barrier.reading = true
	}
	control.mu.Unlock()
	if !valid {
		return nil, nil, control.stop(errT422StaleControl)
	}
	wait, finishWait := control.operationContext(ctx, nil)
	select {
	case <-wait.Done():
		finishWait()
		return nil, nil, control.stop(wait.Err())
	case <-barrier.ready:
	}
	control.mu.Lock()
	observer, event, target := barrier.observer, barrier.transition, control.target
	control.mu.Unlock()
	finishWait()
	operation, finish := control.operationContext(ctx, observer)
	value, err := control.reconciler.Runtime.ReadStaleLeaseTransition(operation, extractionpublication.StaleLeaseTransitionRequest{
		Transition: event, TargetGeneration: target.TargetGeneration, PriorScheduleDigest: target.PriorScheduleDigest,
		Domain: target.Domain, Ordinal: target.Ordinal, PlanDigest: target.PlanDigest, ResultIdentity: target.ResultIdentity})
	if err != nil {
		finish()
		return nil, nil, control.stop(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		finish()
		return nil, nil, control.stop(err)
	}
	return append(body, '\n'), func(cause error) {
		defer finish()
		if cause != nil || operation.Err() != nil || !control.current(ctx, 7, true, false) {
			_ = control.stop(errors.Join(cause, operation.Err(), errT422StaleControl))
			return
		}
		_ = control.finishReport(barrier)
	}, nil
}

func (control *t422StaleControl) finishReport(barrier *t422StaleBarrier) error {
	control.mu.Lock()
	valid := (barrier == &control.hit || barrier == &control.recovered) && control.err == nil && control.ctx.Err() == nil &&
		barrier.reading && !barrier.reported && barrier.observer != nil && barrier.observer.Err() == nil
	if valid {
		barrier.reported = true
		close(barrier.release)
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl)
	}
	return nil
}

func (control *t422StaleControl) stop(cause error, checks ...t422StaleFailedChecks) error {
	control.mu.Lock()
	first := control.err == nil
	var diagnostic t422StaleFailure
	if first {
		control.err = errors.Join(errT422StaleControl, cause)
	}
	if first && control.diagnose {
		diagnostic.Cause = t422StaleCause(cause)
		diagnostic.Context = t422StaleCause(control.ctx.Err())
		if len(checks) != 0 {
			diagnostic.Checks = checks[0]
		}
		runtime.Callers(2, diagnostic.Callers[:])
		control.privateFailure = &diagnostic
	}
	err := control.err
	control.mu.Unlock()
	control.cancel()
	if first {
		control.launch.fail(err)
	}
	if first && control.diagnose {
		// Failure propagation precedes advisory output. A blocked destination
		// can delay this returning goroutine, never the failure latch/cancel.
		var sites [3]string
		var lines [3]int
		frames := runtime.CallersFrames(diagnostic.Callers[:])
		for index := range sites {
			frame, more := frames.Next()
			sites[index] = frame.Function[:min(len(frame.Function), 192)]
			lines[index] = frame.Line
			if !more {
				break
			}
		}
		diagnostics.Logf("private selected stale first failure: cause=%q context=%q checks=%+v callers=%q lines=%v", diagnostic.Cause, diagnostic.Context, diagnostic.Checks, sites, lines)
	}
	return err
}

// Preserve the caller's request/ledger/owner values. An observer's original
// deadline and cancellation narrow this operation; they are never restarted.
func (control *t422StaleControl) operationContext(caller, observer context.Context) (context.Context, func()) {
	deadline := time.Now().Add(t422StaleMaximum)
	control.mu.Lock()
	if !control.phaseEnd.IsZero() {
		deadline = control.phaseEnd
	}
	control.mu.Unlock()
	if observer != nil {
		if value, ok := observer.Deadline(); ok && value.Before(deadline) {
			deadline = value
		}
	}
	ctx, cancel := context.WithDeadline(caller, deadline)
	join := func(parent context.Context) func() {
		done := make(chan struct{})
		stop := context.AfterFunc(parent, func() { cancel(); close(done) })
		if parent.Err() != nil {
			cancel()
		}
		return func() {
			if !stop() {
				<-done
			}
		}
	}
	joinControl := join(control.ctx)
	joinObserver := func() {}
	if observer != nil {
		joinObserver = join(observer)
	}
	var once sync.Once
	return ctx, func() { once.Do(func() { cancel(); joinControl(); joinObserver() }) }
}

func t422StaleRequest(request *http.Request, path string) bool {
	method := http.MethodGet
	if path == t422StalePreparePath {
		method = http.MethodPost
	}
	return request != nil && request.URL != nil && request.URL.Path == path && request.URL.EscapedPath() == path &&
		request.Method == method && request.URL.RawQuery == "" && !request.URL.ForceQuery && request.ContentLength == 0 && len(request.TransferEncoding) == 0
}

func (state *t421ExactReadAccountingState) staleRead(request *http.Request) func(context.Context) ([]byte, func(error), error) {
	if state.stale == nil || state.semantic == nil || state.stale.launch != state.semantic {
		return nil
	}
	for _, route := range []struct {
		path  string
		point store.GenerationStaleLeaseTransitionPoint
	}{
		{t422StaleHitPath, store.GenerationStaleLeaseTransitionHit}, {t422StaleRecoveredPath, store.GenerationStaleLeaseTransitionRecovered},
	} {
		if t422StaleRequest(request, route.path) {
			return func(ctx context.Context) ([]byte, func(error), error) { return state.stale.read(ctx, route.point) }
		}
	}
	return nil
}
