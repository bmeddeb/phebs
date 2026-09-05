package relationshippublication

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/store"
)

type selectedRecoveryStoreV3 interface {
	RecoveryPinStoreV3
	ReadGenerationStaleLeaseTransition(context.Context, store.GenerationStaleLeaseTransitionRequest) (store.GenerationStaleLeaseTransition, error)
}

type selectedRecoveryV3 struct {
	expected PublicationTransitionTargetV3
	chunk    store.GenerationChunk
	state    selectedRecoveryStoreV3
}

// RecoverSelectedV3 continues exactly the marker hit captured from HandleV3,
// without completing, retrying, or reclaiming its scheduler attempt. The caller
// must first let HandleV3 unwind, then hold the external exclusive publication
// mutation fence through this method and its observer. It must keep the original
// scheduler heartbeat alive and advance the service selector only after releasing
// that fence. This method neither acquires that lock nor establishes startup or
// process-restart evidence.
//
// A true result means recovery reached its durable commit point, even if the
// observer then fails. Every error is terminal for the exact continuation; a
// caller must not recreate a marker or retry an observation after commit.
func (runtime *Runtime) RecoverSelectedV3(
	ctx context.Context,
	chunk store.GenerationChunk,
	expected PublicationTransitionTargetV3,
	observe PublicationTransitionRecoveryObserverV3,
) (bool, error) {
	if ctx == nil || observe == nil || expected.Request.Point != PublicationTransitionHitV3 ||
		expected.Request.Repository != chunk.Repository || validatePublicationTransitionRequestV3(expected.Request) != nil {
		return false, fmt.Errorf("%w: selected relationship v3 recovery request", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := runtime.validateV3(chunk.Repository); err != nil {
		return false, err
	}
	state, ok := runtime.Store.(selectedRecoveryStoreV3)
	if !ok {
		return false, fmt.Errorf("%w: selected relationship v3 recovery store", ErrInvalid)
	}
	binding, err := runtime.readRuntimeBindingV3(chunk.Repository, chunk.Generation)
	if err != nil {
		return false, err
	}
	plan, schedule, err := publicationTransitionRuntimeIdentityV3(chunk, binding)
	if err != nil || expected.PlanDigest != plan || expected.ScheduleDigest != schedule {
		return false, errors.Join(err, fmt.Errorf("%w: selected relationship v3 recovery binding", ErrInvalid))
	}
	selected := selectedRecoveryV3{expected: expected, chunk: chunk, state: state}
	recovered, err := recoverMarkerV3(ctx, runtime.relationshipRoot(), chunk.Repository, state,
		func(ctx context.Context, target PublicationTransitionRecoveryTargetV3) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := observe(ctx, target); err != nil {
				return err
			}
			return ctx.Err()
		}, &selected)
	// The ordinary recovery driver uses this private marker to classify fatal
	// store/report errors. All selected errors are terminal; preserve the cause.
	if fatal, ok := err.(recoveryStoreError); ok {
		err = fatal.error
	}
	return recovered, err
}

func (selected *selectedRecoveryV3) confirmMarker(
	ctx context.Context,
	root, base, target string,
	marker MarkerV3,
) error {
	prior, err := ReadPointerV3(ctx, root, selected.chunk.Repository)
	if err != nil {
		return err
	}
	actual, err := publicationTransitionTargetV3(
		prior, marker, selected.expected.PlanDigest, selected.expected.ScheduleDigest,
	)
	if err != nil || actual != selected.expected {
		return errors.Join(err, fmt.Errorf("%w: selected relationship v3 marker changed", ErrInvalid))
	}
	// The selected hit already installed and synced its target. In particular,
	// never enter ordinary recovery's stage-rename branch for this continuation.
	if err := validateDirectory(target); err != nil {
		return err
	}
	for _, name := range []string{marker.StageName, "publishing.json.tmp"} {
		if err := requirePublicationTransitionAbsentV3(filepath.Join(base, name), "selected residue"); err != nil {
			return err
		}
	}
	return nil
}

func (selected *selectedRecoveryV3) confirmLease(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	chunk := selected.chunk
	// Reuse the existing bounded running-attempt reader, not checkpoint evidence.
	// Its schedule plus transactional chunk/current reads cost healthy S2 and
	// at most S65 (64 schedule retries plus one chunk query), outside either C5
	// report, after full target validation and before any write. The existing
	// operation context still bounds all attempts; no retry policy changes.
	request := store.GenerationStaleLeaseTransitionRequest{
		Point:      store.GenerationStaleLeaseTransitionCheckpointHit,
		Repository: chunk.Repository, Stage: chunk.Stage, Generation: chunk.Generation,
		ResourceClass: chunk.ResourceClass, ScheduleDigest: chunk.ScheduleDigest,
		ChunkIdentity: chunk.Identity, Offset: chunk.Offset, Length: chunk.Length, Attempt: chunk.Attempt,
	}
	current, err := selected.state.ReadGenerationStaleLeaseTransition(ctx, request)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if current.Point != request.Point || current.Repository != chunk.Repository ||
		current.Stage != chunk.Stage || current.Generation != chunk.Generation ||
		current.ResourceClass != chunk.ResourceClass || current.ScheduleDigest != chunk.ScheduleDigest ||
		current.ChunkIdentity != chunk.Identity || current.Offset != chunk.Offset ||
		current.Length != chunk.Length || current.Attempt != chunk.Attempt ||
		current.ScheduleStatus != store.GenerationScheduleActive || current.ChunkStatus != store.GenerationChunkRunning ||
		current.Priority != store.GenerationPriorityNeverRun || !current.Leased ||
		current.PrivateLeaseTokenDigest != store.GenerationLeaseTokenDigest(chunk.LeaseToken) {
		return store.ErrGenerationLeaseLost
	}
	return nil
}
