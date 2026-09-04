package extractionpublication

import (
	"context"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

// StaleLeaseTransitionControlFileReads is the exact immutable-control cost of
// one stale-lease transition snapshot: binding, generation, plan, and result.
const StaleLeaseTransitionControlFileReads uint64 = 4

type staleLeaseTransitionStore interface {
	ReadGenerationStaleLeaseTransition(
		context.Context,
		store.GenerationStaleLeaseTransitionRequest,
	) (store.GenerationStaleLeaseTransition, error)
}

// StaleLeaseTransitionRequest binds one store-owned stale-lease event to the
// immutable result selected by the admitted recovery preparation.
type StaleLeaseTransitionRequest struct {
	Transition          store.GenerationStaleLeaseTransition
	TargetGeneration    string
	PriorScheduleDigest string
	Domain              string
	Ordinal             int
	PlanDigest          string
	ResultIdentity      string
}

// StaleLeaseTransition is the bounded, source-free identity returned after
// the mutable store state is confirmed around the immutable control reads.
type StaleLeaseTransition struct {
	Point               store.GenerationStaleLeaseTransitionPoint `json:"point"`
	TargetGeneration    string                                    `json:"target_generation"`
	ScheduleGeneration  string                                    `json:"schedule_generation"`
	PriorScheduleDigest string                                    `json:"prior_schedule_digest"`
	ScheduleDigest      string                                    `json:"schedule_digest"`
	ChunkIdentity       string                                    `json:"chunk_identity"`
	Domain              string                                    `json:"domain"`
	Ordinal             int                                       `json:"ordinal"`
	PlanDigest          string                                    `json:"plan_digest"`
	ResultIdentity      string                                    `json:"result_identity"`
}

type preparedTransitionTarget struct {
	binding    scheduleBinding
	generation Generation
	descriptor DomainDescriptor
	domain     DomainPlan
	result     candidate.PartitionResult
	ordinal    int
}

// ReadStaleLeaseTransition reads one exact hit or post-completion recovery.
// The store-owned requeue point is synchronization only and is not reportable.
func (runtime *Runtime) ReadStaleLeaseTransition(
	ctx context.Context,
	request StaleLeaseTransitionRequest,
) (StaleLeaseTransition, error) {
	transition := request.Transition
	if ctx == nil || runtime == nil || runtime.validate() != nil ||
		transition.Stage != ScheduleStage ||
		transition.ResourceClass != store.GenerationResourceExtraction ||
		!validDigest(transition.Generation) || !validDigest(transition.ScheduleDigest) ||
		!validDigest(transition.ChunkIdentity) ||
		!validStaleLeaseTransitionEvent(transition) ||
		transition.Length != 1 || transition.Attempt != 0 || transition.Offset < 0 ||
		!validDigest(request.TargetGeneration) ||
		request.TargetGeneration == transition.Generation ||
		!validDigest(request.PriorScheduleDigest) ||
		!boundedIdentity(request.Domain, 128) || request.Ordinal < 0 ||
		!validDigest(request.PlanDigest) || !validDigest(request.ResultIdentity) {
		return StaleLeaseTransition{}, invalid("stale lease transition request")
	}
	reader, ok := runtime.Store.(staleLeaseTransitionStore)
	if !ok {
		return StaleLeaseTransition{}, invalid("stale lease transition store")
	}
	storeRequest := store.GenerationStaleLeaseTransitionRequest{
		Point: transition.Point, Repository: transition.Repository,
		Stage: transition.Stage, Generation: transition.Generation,
		ResourceClass: transition.ResourceClass, ScheduleDigest: transition.ScheduleDigest,
		ChunkIdentity: transition.ChunkIdentity, Offset: transition.Offset,
		Length: transition.Length, Attempt: transition.Attempt,
		StaleBefore: transition.StaleBefore,
	}
	before, err := reader.ReadGenerationStaleLeaseTransition(ctx, storeRequest)
	if err != nil {
		return StaleLeaseTransition{}, err
	}
	if !sameStaleLeaseTransitionTarget(before, transition) ||
		(transition.Point == store.GenerationStaleLeaseTransitionHit && before != transition) {
		return StaleLeaseTransition{}, ErrStale
	}

	target, err := runtime.readPreparedTransitionTarget(
		ctx, transition, request.TargetGeneration, request.PriorScheduleDigest,
		request.Domain, request.Ordinal, request.PlanDigest, request.ResultIdentity,
	)
	if err != nil {
		return StaleLeaseTransition{}, err
	}
	after, err := reader.ReadGenerationStaleLeaseTransition(ctx, storeRequest)
	if err != nil {
		return StaleLeaseTransition{}, err
	}
	if after != before {
		return StaleLeaseTransition{}, ErrStale
	}
	return StaleLeaseTransition{
		Point: transition.Point, TargetGeneration: target.generation.Digest,
		ScheduleGeneration:  target.binding.ScheduleGeneration,
		PriorScheduleDigest: target.binding.PriorSchedule, ScheduleDigest: transition.ScheduleDigest,
		ChunkIdentity: transition.ChunkIdentity, Domain: target.descriptor.Domain,
		Ordinal: target.ordinal, PlanDigest: target.domain.Plan.Digest, ResultIdentity: target.result.Identity,
	}, nil
}

func (runtime *Runtime) readPreparedTransitionTarget(
	ctx context.Context,
	transition store.GenerationStaleLeaseTransition,
	targetGeneration string,
	priorScheduleDigest string,
	domainName string,
	ordinal int,
	planDigest string,
	resultIdentity string,
) (preparedTransitionTarget, error) {
	binding, err := runtime.readBindingContext(ctx, transition.Repository, transition.Generation)
	if err != nil {
		return preparedTransitionTarget{}, err
	}
	if binding.TargetGeneration != targetGeneration || binding.PriorSchedule != priorScheduleDigest {
		return preparedTransitionTarget{}, ErrStale
	}
	directory := runtime.generationDirectory(transition.Repository, targetGeneration)
	generation, err := runtime.openGenerationContext(
		ctx, directory, transition.Repository, targetGeneration,
	)
	if err != nil {
		return preparedTransitionTarget{}, err
	}
	wantSchedule, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: transition.Repository, Stage: ScheduleStage,
		Generation: transition.Generation, ResourceClass: store.GenerationResourceExtraction,
		TotalItems: int64(generation.WorkItems), ChunkItems: ScheduleChunkItems,
		MaxAttempts: ScheduleMaxAttempts, RepositoryTokens: ScheduleRepositoryTokens,
	})
	if err != nil || transition.ScheduleDigest != wantSchedule {
		return preparedTransitionTarget{}, ErrStale
	}
	descriptor, localOrdinal, err := domainForOffset(generation, int(transition.Offset))
	if err != nil {
		return preparedTransitionTarget{}, err
	}
	if descriptor.Domain != domainName || descriptor.PlanDigest != planDigest || localOrdinal != ordinal {
		return preparedTransitionTarget{}, ErrStale
	}
	domain, err := runtime.openDomainPlanContext(ctx, directory, descriptor)
	if err != nil {
		return preparedTransitionTarget{}, err
	}
	if domain.Plan.Repository != transition.Repository {
		return preparedTransitionTarget{}, ErrStale
	}
	result, present, err := readPartitionResultContext(
		ctx,
		filepath.Join(directory, domainKey(descriptor.Domain), resultName(localOrdinal)),
		domain.Plan,
		localOrdinal,
	)
	if err != nil {
		return preparedTransitionTarget{}, err
	}
	if !present || result.Identity != resultIdentity {
		return preparedTransitionTarget{}, ErrStale
	}
	return preparedTransitionTarget{
		binding: binding, generation: generation, descriptor: descriptor,
		domain: domain, result: result, ordinal: localOrdinal,
	}, nil
}

func validStaleLeaseTransitionEvent(value store.GenerationStaleLeaseTransition) bool {
	switch value.Point {
	case store.GenerationStaleLeaseTransitionHit:
		return value.ScheduleStatus == store.GenerationScheduleActive &&
			value.Priority == store.GenerationPriorityNeverRun &&
			value.ChunkStatus == store.GenerationChunkRunning && value.Leased &&
			validDigest(value.ChunkStateDigest) && !value.StaleBefore.IsZero()
	case store.GenerationStaleLeaseTransitionRecovered:
		return value.Priority == store.GenerationPriorityStale &&
			value.ChunkStatus == store.GenerationChunkDone && !value.Leased &&
			value.StaleBefore.IsZero()
	default:
		return false
	}
}

func sameStaleLeaseTransitionTarget(
	left, right store.GenerationStaleLeaseTransition,
) bool {
	return left.Point == right.Point && left.Repository == right.Repository &&
		left.Stage == right.Stage && left.Generation == right.Generation &&
		left.ResourceClass == right.ResourceClass &&
		left.ScheduleDigest == right.ScheduleDigest &&
		left.ChunkIdentity == right.ChunkIdentity && left.Offset == right.Offset &&
		left.Length == right.Length && left.Attempt == right.Attempt
}
