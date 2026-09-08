package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422CheckpointRecoveredPath = "/api/t422/checkpoint/recovered"

// Only the genuine parent's previously observed scalar identities are carried.
// This input grants no authority to assert a kill, lease change or recovery.
type t422CheckpointRecoveryInput struct {
	Prior  t421FinalAuthorityState                           `json:"prior"`
	Roots  [9]extractionpublication.RecoveryPreparationRoot  `json:"roots"`
	Offset int                                               `json:"offset"`
	Hit    extractionpublication.CheckpointRestartTransition `json:"hit"`
}

func validT422CheckpointRecoveryInput(input t422CheckpointRecoveryInput) bool {
	hit, prior := input.Hit, input.Prior
	if input.Offset < 0 || hit.Domain != "proto-contract" || hit.Ordinal != 2 ||
		hit.Point != store.GenerationStaleLeaseTransitionCheckpointHit || hit.Attempt != 0 || hit.Priority != store.GenerationPriorityNeverRun ||
		hit.ScheduleStatus != store.GenerationScheduleActive || hit.ChunkStatus != store.GenerationChunkRunning || !hit.Leased ||
		!hit.CanonicalResultExists || !hit.CompletionFileExists || hit.CompletionBitSet || hit.RootExists || hit.Current || hit.RootDigest != "" ||
		hit.CheckpointStateDigest != "" || hit.PrivateLeaseTokenDigest != "" || hit.TargetGeneration == hit.ScheduleGeneration ||
		hit.PriorScheduleDigest == hit.ScheduleDigest || !prior.Current ||
		hit.CandidateGenerationDigest != prior.CandidateGenerationSHA256 || hit.SourceGenerationDigest != prior.SourceGenerationSHA256 ||
		hit.ObservationGenerationDigest != prior.ObservationGenerationSHA256 || len(hit.ExtractorVersion) == 0 || len(hit.ExtractorVersion) > 128 ||
		strings.ContainsAny(hit.ExtractorVersion, "\x00\r\n") {
		return false
	}
	for _, oid := range []string{prior.PhysicalCommit, prior.PhysicalTree} {
		raw, err := hex.DecodeString(oid)
		if err != nil || len(oid) != 40 || hex.EncodeToString(raw) != oid {
			return false
		}
	}
	for _, digest := range []string{hit.TargetGeneration, hit.ScheduleGeneration, hit.PriorScheduleDigest, hit.ScheduleDigest, hit.ChunkIdentity,
		hit.PlanDigest, hit.ResultIdentity, hit.ResultDigest, hit.ExpectationDigest, hit.PartitionDigest, hit.ExtractionPolicyDigest,
		prior.SourceGenerationSHA256, prior.SearchGenerationSHA256, prior.ObservationGenerationSHA256, prior.CandidateGenerationSHA256,
		prior.CatalogRootSHA256, prior.CatalogActivationPlanSHA256, prior.CatalogActivationScheduleSHA256, prior.CatalogActivationUnitSHA256,
		prior.ResolverCatalogGenerationSHA256, prior.ResolverCatalogRootSHA256, prior.CallerGenerationSHA256, prior.CallerRootSHA256,
		prior.RelationshipGenerationSHA256, prior.RelationshipRootSHA256, prior.RelationshipProvenanceSHA256,
		prior.SearchInventory.SHA256, prior.ObservationInputInventory.SHA256, prior.ExtractionRootsSHA256} {
		if !t422SemanticDigest(digest) {
			return false
		}
	}
	identity, err := store.GenerationChunkIdentity(hit.ScheduleDigest, int64(input.Offset), 0)
	if err != nil || identity != hit.ChunkIdentity {
		return false
	}
	found := false
	for i, root := range input.Roots {
		if root.Domain == "" || len(root.Domain) > 128 || strings.ContainsAny(root.Domain, "\x00\r\n") ||
			(i > 0 && root.Domain <= input.Roots[i-1].Domain) || !t422SemanticDigest(root.PlanDigest) || !t422SemanticDigest(root.RootDigest) {
			return false
		}
		if root.Domain == hit.Domain {
			found = root.PlanDigest == hit.PlanDigest
		}
	}
	return found
}

type t422CheckpointRecoveryControl struct {
	*t422StaleControl // Reuse bounded lifetime/barrier coordination, not phase-seven methods.
	input             t422CheckpointRecoveryInput
	killed            store.GenerationStaleLeaseTransition
	finalSeen         bool
	readerReady       chan context.Context
}

func newT422CheckpointRecoveryControl(ctx context.Context, launch *t422SemanticLaunch, reconciler *extractionpublication.Reconciler) (*t422CheckpointRecoveryControl, error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || launch.request.CheckpointRecovery == nil ||
		launch.request.ServerEpoch != 4 || launch.initial.ProducerID != 5 || launch.initial.Phase != 8 || err != nil ||
		!launch.matches(current) || current.Phase != 8 || !validT422CheckpointRecoveryInput(*launch.request.CheckpointRecovery) ||
		reconciler == nil || reconciler.Runtime == nil || reconciler.Runtime.Fence == nil || !reconciler.StoreAccounting {
		return nil, errT422StaleControl
	}
	lifetime, cancel := context.WithCancel(ctx)
	base := &t422StaleControl{ctx: lifetime, cancel: cancel, launch: launch, reconciler: reconciler,
		phaseEnd: time.Now().Add(t422StaleMaximum), requeued: make(chan struct{})}
	base.recovered.ready, base.recovered.release = make(chan struct{}), make(chan struct{})
	return &t422CheckpointRecoveryControl{t422StaleControl: base, input: *launch.request.CheckpointRecovery, readerReady: make(chan context.Context, 1)}, nil
}

// Wait before starting the scheduler, without holding an owner, claim or lease.
// HTTP readiness alone is insufficient: the parent still checks custody before
// issuing R. Its admitted one-shot read must be waiting before native recovery
// can complete and start the unchanged five-second report callback deadline.
func (control *t422CheckpointRecoveryControl) waitForReader(ctx context.Context) error {
	if !control.current(ctx, false, false) {
		return control.stop(errT422StaleControl)
	}
	operation, finish := control.operationContext(ctx, nil)
	defer finish()
	var reader context.Context
	select {
	case <-operation.Done():
		return control.stop(operation.Err())
	case reader = <-control.readerReady:
	}
	control.mu.Lock()
	valid := control.err == nil && control.recovered.reading
	control.mu.Unlock()
	if !valid || reader == nil || reader.Err() != nil || operation.Err() != nil || !control.current(ctx, false, false) {
		return control.stop(errT422StaleControl)
	}
	return nil
}

func (control *t422CheckpointRecoveryControl) current(ctx context.Context, request, drained bool) bool {
	if ctx == nil || ctx.Err() != nil || control.ctx.Err() != nil {
		return false
	}
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil || !control.launch.matches(current) || current.ProducerID != 5 || current.Phase != 8 || (drained && !current.OrdinaryOwnersDrained) {
		return false
	}
	if !request {
		return true
	}
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	return ok && control.launch.sameRequest(admitted, current) && (!drained || admitted.OrdinaryOwnersDrained)
}

func (control *t422CheckpointRecoveryControl) eventTarget(event store.GenerationStaleLeaseTransition) bool {
	hit := control.input.Hit
	return event.Repository == control.launch.request.Repository && event.Stage == extractionpublication.ScheduleStage &&
		event.ResourceClass == store.GenerationResourceExtraction && event.Generation == hit.ScheduleGeneration && event.ScheduleDigest == hit.ScheduleDigest &&
		event.ChunkIdentity == hit.ChunkIdentity && event.Offset == int64(control.input.Offset) && event.Length == 1 && event.Attempt == 0
}

// Native Hit/Requeued are private synchronization only. Recovery may race the
// post-COMMIT Requeued callback, so rendezvous on that callback, never infer it.
func (control *t422CheckpointRecoveryControl) transition(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
	if !control.current(ctx, false, false) || !control.eventTarget(event) {
		return control.stop(errT422StaleControl)
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
		return control.stop(errT422StaleControl)
	}
	control.mu.Lock()
	valid := control.err == nil
	switch event.Point {
	case store.GenerationStaleLeaseTransitionHit:
		valid = valid && control.hit.observer == nil && !control.requeueSeen && event.ScheduleStatus == store.GenerationScheduleActive &&
			event.Priority == store.GenerationPriorityNeverRun && event.ChunkStatus == store.GenerationChunkRunning && event.Leased && !event.StaleBefore.IsZero() &&
			t422SemanticDigest(event.ChunkStateDigest) && t422SemanticDigest(event.CheckpointStateDigest) && t422SemanticDigest(event.PrivateLeaseTokenDigest)
		if valid {
			control.killed, control.hit.observer = event, ctx
		}
	case store.GenerationStaleLeaseTransitionRequeued:
		valid = valid && control.hit.observer != nil && control.hit.observer.Err() == nil && !control.requeueSeen &&
			event.ScheduleStatus == store.GenerationScheduleActive && event.Priority == store.GenerationPriorityStale &&
			event.ChunkStatus == store.GenerationChunkPending && !event.Leased && event.StaleBefore.IsZero()
		if valid {
			control.requeueSeen = true
			close(control.requeued)
		}
	case store.GenerationStaleLeaseTransitionRecovered:
		valid = valid && control.hit.observer != nil && control.recovered.observer == nil && event.Priority == store.GenerationPriorityStale &&
			event.ChunkStatus == store.GenerationChunkDone && !event.Leased && event.StaleBefore.IsZero() &&
			t422SemanticDigest(event.PrivateLeaseTokenDigest) && event.PrivateLeaseTokenDigest != control.killed.PrivateLeaseTokenDigest
		if valid {
			control.recovered.observer, control.recovered.transition = ctx, event
		}
	default:
		valid = false
	}
	observer, requeued := control.hit.observer, control.requeueSeen
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422StaleControl)
	}
	if event.Point != store.GenerationStaleLeaseTransitionRecovered {
		return nil
	}
	if !requeued {
		wait, finishWait := control.operationContext(ctx, observer)
		select {
		case <-wait.Done():
		case <-control.requeued:
		}
		finishWait()
	}
	control.mu.Lock()
	valid = control.err == nil && control.requeueSeen
	if valid {
		close(control.recovered.ready)
	}
	control.mu.Unlock()
	if !valid || !control.current(ctx, false, false) {
		return control.stop(errT422StaleControl)
	}
	operation, finish := control.operationContext(ctx, nil)
	defer finish()
	select {
	case <-operation.Done():
		return control.stop(operation.Err())
	case <-control.recovered.release:
	}
	control.mu.Lock()
	valid = control.err == nil && control.recovered.reported
	control.mu.Unlock()
	if !valid || operation.Err() != nil || !control.current(ctx, false, false) {
		return control.stop(errT422StaleControl)
	}
	return nil
}

func (control *t422CheckpointRecoveryControl) matchesRecovered(value extractionpublication.CheckpointRestartTransition, event store.GenerationStaleLeaseTransition) bool {
	want := control.input.Hit
	want.Point, want.Priority, want.ChunkStatus, want.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
	want.ScheduleStatus = store.GenerationScheduleSettled
	want.CompletionBitSet, want.RootExists, want.Current = true, true, true
	for _, root := range control.input.Roots {
		if root.Domain == want.Domain {
			want.RootDigest = root.RootDigest
		}
	}
	want.PrivateLeaseTokenDigest, want.CheckpointStateDigest = event.PrivateLeaseTokenDigest, value.CheckpointStateDigest
	return t422SemanticDigest(value.CheckpointStateDigest) && value == want
}

func (control *t422CheckpointRecoveryControl) read(ctx context.Context) ([]byte, func(error), error) {
	if !control.current(ctx, true, false) {
		return nil, nil, control.stop(errT422StaleControl)
	}
	control.mu.Lock()
	valid := control.err == nil && !control.recovered.reading
	if valid {
		control.recovered.reading = true
		control.readerReady <- ctx
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
	case <-control.recovered.ready:
	}
	finishWait()
	control.mu.Lock()
	observer, event, hit := control.recovered.observer, control.recovered.transition, control.input.Hit
	control.mu.Unlock()
	operation, finish := control.operationContext(ctx, observer)
	value, err := control.reconciler.Runtime.ReadCheckpointRestartTransition(operation, extractionpublication.CheckpointRestartTransitionRequest{
		Transition: event, TargetGeneration: hit.TargetGeneration, PriorScheduleDigest: hit.PriorScheduleDigest,
		Domain: hit.Domain, Ordinal: hit.Ordinal, PlanDigest: hit.PlanDigest, ResultIdentity: hit.ResultIdentity})
	if err != nil || !control.matchesRecovered(value, event) {
		finish()
		return nil, nil, control.stop(errors.Join(err, errT422StaleControl))
	}
	body, err := json.Marshal(value)
	if err != nil {
		finish()
		return nil, nil, control.stop(err)
	}
	return append(body, '\n'), func(cause error) {
		defer finish()
		if cause != nil || operation.Err() != nil || !control.current(ctx, true, false) {
			_ = control.stop(errors.Join(cause, operation.Err(), errT422StaleControl))
			return
		}
		_ = control.finishReport(&control.recovered)
	}, nil
}

func (control *t422CheckpointRecoveryControl) captureFinal(ctx context.Context, state candidate.State, response t421FinalAuthorityResponse) error {
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		return control.stop(err)
	}
	if current.Phase != 8 {
		return nil
	}
	if !control.current(ctx, true, true) {
		return control.stop(errT422StaleControl)
	}
	value, err := t422StaleFinalSnapshot(state, response)
	control.mu.Lock()
	valid := err == nil && control.err == nil && control.recovered.reported && !control.finalSeen &&
		value.final == control.input.Prior && value.generation == control.input.Hit.TargetGeneration && value.roots == control.input.Roots
	if valid {
		control.finalSeen = true
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errors.Join(err, errT422StaleControl))
	}
	return nil
}

func (state *t421ExactReadAccountingState) checkpointRecoveredRead(request *http.Request) func(context.Context) ([]byte, func(error), error) {
	if state.checkpointRecovery == nil || state.semantic == nil || state.checkpointRecovery.launch != state.semantic || request == nil || request.URL == nil ||
		request.URL.Path != t422CheckpointRecoveredPath || request.URL.EscapedPath() != t422CheckpointRecoveredPath ||
		request.Method != http.MethodGet || request.URL.RawQuery != "" || request.URL.ForceQuery || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		return nil
	}
	return state.checkpointRecovery.read
}
