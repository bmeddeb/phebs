package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t422CheckpointPreparePath = "/api/t422/checkpoint/prepare"
	t422CheckpointHitPath     = "/api/t422/checkpoint/hit"
)

// Reuse only the existing scalar preparation, cancellation and barrier storage.
// This is an epoch-three hit control, not an epoch-four recovery witness.
type t422CheckpointControl struct {
	*t422StaleControl
	heartbeat        *generationscheduler.TerminalHeartbeat
	observed         extractionpublication.CheckpointRestartTransition
	parked, terminal bool
}

func newT422CheckpointControl(ctx context.Context, stale *t422StaleControl) (*t422CheckpointControl, error) {
	phase, err := dispatchadmission.ProductionTerminalPhase()
	if err != nil || phase != 8 || stale == nil || !stale.current(ctx, 6, false, false) ||
		stale.launch.request.ServerEpoch != 3 || stale.launch.initial.ProducerID != 4 || stale.launch.initial.Phase != 6 ||
		stale.reconciler == nil || !stale.reconciler.RecoveryPreparationEnabled || !stale.reconciler.StoreAccounting {
		return nil, errT422StaleControl
	}
	lifetime, cancel := context.WithCancel(ctx)
	base := &t422StaleControl{ctx: lifetime, cancel: cancel, launch: stale.launch, reconciler: stale.reconciler,
		sink: t4013ExactReportSink("exact checkpoint preparation: "), workspacePreparation: stale.workspacePreparation}
	base.hit.ready, base.hit.release = make(chan struct{}), make(chan struct{})
	return &t422CheckpointControl{t422StaleControl: base}, nil
}

func (control *t422CheckpointControl) captureFinal(ctx context.Context, state candidate.State, response t421FinalAuthorityResponse) error {
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		return control.stop(err)
	}
	if current.Phase != 7 {
		return nil
	}
	if !control.current(ctx, 7, true, true) {
		return control.stop(errT422StaleControl)
	}
	value, err := t422StaleFinalSnapshot(state, response)
	if err != nil {
		return control.stop(err)
	}
	found := false
	for _, root := range response.ExtractionRoots {
		found = found || root.Domain == "proto-contract" && root.ApplicablePartitions > 2
	}
	control.mu.Lock()
	valid := found && control.err == nil && !control.captured
	if valid {
		control.final, control.captured = value, true
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl)
	}
	return nil
}

func (control *t422CheckpointControl) finalTail(ctx context.Context, prior func(error)) (func(error), error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		return nil, control.stop(err)
	}
	if current.Phase != 7 {
		return prior, nil
	}
	if !control.current(ctx, 7, true, true) {
		return nil, control.stop(errT422StaleControl)
	}
	return func(cause error) {
		if prior != nil {
			prior(cause)
		}
		valid := cause == nil && control.current(ctx, 7, true, true)
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

func t422CheckpointPreparationLimits() readaccounting.Counts {
	counts := t422StalePreparationLimits()
	counts.ControlFileReads++ // Existing checkpoint completion read, not a new ceiling.
	return counts
}

func t422CheckpointRequest(request *http.Request, prepare bool) bool {
	path, method := t422CheckpointHitPath, http.MethodGet
	if prepare {
		path, method = t422CheckpointPreparePath, http.MethodPost
	}
	return request != nil && request.URL != nil && request.URL.Path == path && request.URL.EscapedPath() == path &&
		request.Method == method && request.URL.RawQuery == "" && !request.URL.ForceQuery && request.ContentLength == 0 && len(request.TransferEncoding) == 0
}

func (control *t422CheckpointControl) command(writer http.ResponseWriter, request *http.Request) {
	principal, authenticated := auth.PrincipalFromContext(request.Context())
	if !t422CheckpointRequest(request, true) || !authenticated || !t421ExactReadLegacyPrincipal(principal) ||
		len(request.Header.Values(t421ExactReadActivationHeader)) != 0 || len(request.Header.Values(t421ExactReadOrdinalHeader)) != 0 ||
		!control.current(request.Context(), 8, true, true) {
		_ = control.stop(errT422StaleControl)
		http.Error(writer, "checkpoint preparation refused", http.StatusConflict)
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
	complete := false
	defer func() {
		if !complete {
			_ = control.stop(errT422StaleControl)
		}
	}()
	if !valid {
		http.Error(writer, "checkpoint preparation refused", http.StatusConflict)
		return
	}
	operation, finish := control.operationContext(request.Context(), nil)
	defer finish()
	ctx, ledger, err := readaccounting.Start(operation, t422CheckpointPreparationLimits())
	if err != nil {
		return
	}
	target, preparationErr := control.reconciler.PrepareCurrentRecovery(ctx, extractionpublication.CurrentRecoveryPreparationRequest{
		Authority: value.authority, GenerationDigest: value.generation, Roots: value.roots[:],
		Mode: extractionpublication.RecoveryPreparationCheckpoint, TargetDomain: "proto-contract", TargetOrdinal: 2})
	counts, accountingErr := ledger.Finish()
	if preparationErr != nil || accountingErr != nil || operation.Err() != nil || !control.current(request.Context(), 8, true, true) {
		_ = control.stop(errors.Join(preparationErr, accountingErr, operation.Err()))
		http.Error(writer, "checkpoint preparation refused", http.StatusConflict)
		return
	}
	var workspace *t422WorkspaceSampleResponse
	if control.workspacePreparation != nil {
		workspace, err = control.workspacePreparation(operation)
		if err != nil {
			return
		}
	}
	body, err := json.Marshal(t422StalePreparationObservation{Schema: "t422-checkpoint-preparation-observation-v1", Authority: value.final,
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
	if err != nil || n != len(body) || control.sink(body) != nil || operation.Err() != nil || !control.current(request.Context(), 8, true, true) {
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

func t422CheckpointClaimMatches(claim generationscheduler.TerminalClaim, event store.GenerationStaleLeaseTransition, target extractionpublication.RecoveryPreparationTarget) bool {
	return event.Point == store.GenerationStaleLeaseTransitionCheckpointHit && event.Repository == target.Schedule.Repository &&
		event.Stage == extractionpublication.ScheduleStage && event.ResourceClass == store.GenerationResourceExtraction &&
		event.Generation == target.Schedule.Generation && event.ScheduleDigest == target.Schedule.Digest &&
		event.Offset == int64(target.Offset) && event.Length == 1 && event.Attempt == 0 && event.Priority == store.GenerationPriorityNeverRun &&
		event.ScheduleStatus == store.GenerationScheduleActive && event.ChunkStatus == store.GenerationChunkRunning && event.Leased && event.StaleBefore.IsZero() &&
		t422SemanticDigest(event.ChunkIdentity) &&
		t422SemanticDigest(event.PrivateLeaseTokenDigest) && target.Domain == "proto-contract" && target.Ordinal == 2 &&
		claim.Repository == event.Repository && claim.Stage == event.Stage && claim.ResourceClass == event.ResourceClass &&
		claim.Generation == event.Generation && claim.ScheduleDigest == event.ScheduleDigest && claim.ChunkIdentity == event.ChunkIdentity &&
		claim.Offset == event.Offset && claim.Length == event.Length && claim.Attempt == event.Attempt && claim.Priority == event.Priority &&
		claim.LeaseTokenDigest == event.PrivateLeaseTokenDigest
}

// The actual reused-result hook stays inside its original handler. Report
// completion never releases it into assembly; only cancellation/death ends it.
func (control *t422CheckpointControl) checkpoint(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
	control.mu.Lock()
	armed, target := control.armed, control.target
	control.mu.Unlock()
	if !armed || event.Generation != target.Schedule.Generation || event.Offset != int64(target.Offset) {
		return nil
	}
	capability := generationscheduler.TerminalHeartbeatFromContext(ctx)
	claim, err := capability.ClaimIdentity()
	if err != nil || !control.current(ctx, 8, false, false) || !t422CheckpointClaimMatches(claim, event, target) {
		return control.stop(errT422StaleControl)
	}
	control.mu.Lock()
	valid := control.err == nil && !control.parked && control.heartbeat == nil
	if valid {
		control.heartbeat, control.parked = capability, true
		control.hit.observer, control.hit.transition = ctx, event
		close(control.hit.ready)
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl)
	}
	operation, finish := control.operationContext(ctx, nil)
	defer finish()
	<-operation.Done()
	control.mu.Lock()
	control.parked = false
	control.mu.Unlock()
	return control.stop(operation.Err())
}

func (control *t422CheckpointControl) read(ctx context.Context) ([]byte, func(error), error) {
	if !control.current(ctx, 8, true, false) {
		return nil, nil, control.stop(errT422StaleControl)
	}
	control.mu.Lock()
	valid := control.err == nil && control.armed && !control.hit.reading
	if valid {
		control.hit.reading = true
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
	case <-control.hit.ready:
	}
	finishWait()
	control.mu.Lock()
	observer, event, target := control.hit.observer, control.hit.transition, control.target
	control.mu.Unlock()
	operation, finish := control.operationContext(ctx, observer)
	value, err := control.reconciler.Runtime.ReadCheckpointRestartTransition(operation, extractionpublication.CheckpointRestartTransitionRequest{
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
		if cause != nil || operation.Err() != nil || !control.current(ctx, 8, true, false) {
			_ = control.stop(errors.Join(cause, operation.Err(), errT422StaleControl))
			return
		}
		_ = control.finishHit(value)
	}, nil
}

func (control *t422CheckpointControl) finishHit(value extractionpublication.CheckpointRestartTransition) error {
	control.mu.Lock()
	valid := control.err == nil && control.ctx.Err() == nil && control.parked && control.hit.reading && !control.hit.reported &&
		control.hit.observer != nil && control.hit.observer.Err() == nil && value.Point == store.GenerationStaleLeaseTransitionCheckpointHit &&
		value.PrivateLeaseTokenDigest == control.hit.transition.PrivateLeaseTokenDigest && t422SemanticDigest(value.CheckpointStateDigest)
	if valid {
		control.observed, control.hit.reported = value, true
		close(control.hit.release) // Report joined, NOT permission to resume the hook.
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl)
	}
	return nil
}

// Invoked only by authenticated terminal PC, never by the HTTP request. The
// shared R callback has joined its report; genuine FenceTerminal inside Quiesce
// then joins the outer request owner/tail before stopping the heartbeat.
func (control *t422CheckpointControl) quiesce(ctx context.Context) error {
	phase, err := dispatchadmission.ProductionTerminalPhase()
	if err != nil || phase != 8 || !control.current(ctx, 8, false, false) {
		return control.stop(errT422StaleControl)
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return control.stop(errT422StaleControl)
	}
	operation, finish := control.operationContext(ctx, nil)
	defer finish()
	select {
	case <-operation.Done():
		return control.stop(operation.Err())
	case <-control.hit.release:
	}
	control.mu.Lock()
	valid := control.err == nil && control.hit.reported && control.parked && !control.terminal
	capability, event, target := control.heartbeat, control.hit.transition, control.target
	if valid {
		control.terminal = true
	}
	control.mu.Unlock()
	claim, claimErr := capability.ClaimIdentity()
	if !valid || claimErr != nil || !t422CheckpointClaimMatches(claim, event, target) {
		return control.stop(errT422StaleControl)
	}
	if err := capability.Quiesce(operation); err != nil {
		return control.stop(err)
	}
	control.mu.Lock()
	valid = control.err == nil && control.parked && control.hit.observer.Err() == nil
	control.mu.Unlock()
	if !valid || operation.Err() != nil || !control.current(ctx, 8, false, false) {
		return control.stop(errT422StaleControl)
	}
	return nil
}

func (state *t421ExactReadAccountingState) checkpointRead(request *http.Request) func(context.Context) ([]byte, func(error), error) {
	if state.checkpoint == nil || state.semantic == nil || state.checkpoint.launch != state.semantic || !t422CheckpointRequest(request, false) {
		return nil
	}
	return state.checkpoint.read
}
