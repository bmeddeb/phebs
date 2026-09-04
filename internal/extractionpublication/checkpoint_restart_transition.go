package extractionpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/store"
)

// CheckpointRestartTransitionControlFileReads is the exact immutable-control
// cost of one prepared-checkpoint transition snapshot: binding, generation,
// plan, result, completion, root, and current pointer.
const CheckpointRestartTransitionControlFileReads uint64 = 7

// CheckpointRestartTransitionRequest binds one process-owned checkpoint event
// to the immutable result selected by the admitted recovery preparation.
type CheckpointRestartTransitionRequest struct {
	Transition          store.GenerationStaleLeaseTransition
	TargetGeneration    string
	PriorScheduleDigest string
	Domain              string
	Ordinal             int
	PlanDigest          string
	ResultIdentity      string
}

// CheckpointRestartTransition is the bounded source-free prepared or restored
// state returned after the mutable store row is confirmed around all controls.
type CheckpointRestartTransition struct {
	Point                       store.GenerationStaleLeaseTransitionPoint `json:"point"`
	TargetGeneration            string                                    `json:"target_generation"`
	ScheduleGeneration          string                                    `json:"schedule_generation"`
	PriorScheduleDigest         string                                    `json:"prior_schedule_digest"`
	ScheduleDigest              string                                    `json:"schedule_digest"`
	ChunkIdentity               string                                    `json:"chunk_identity"`
	Domain                      string                                    `json:"domain"`
	Ordinal                     int                                       `json:"ordinal"`
	PlanDigest                  string                                    `json:"plan_digest"`
	ResultIdentity              string                                    `json:"result_identity"`
	ResultDigest                string                                    `json:"result_digest"`
	ExpectationDigest           string                                    `json:"expectation_digest"`
	PartitionDigest             string                                    `json:"partition_digest"`
	CandidateGenerationDigest   string                                    `json:"candidate_generation_digest"`
	SourceGenerationDigest      string                                    `json:"source_generation_digest"`
	ObservationGenerationDigest string                                    `json:"observation_generation_digest"`
	ExtractorVersion            string                                    `json:"extractor_version"`
	ExtractionPolicyDigest      string                                    `json:"extraction_policy_digest"`
	ScheduleStatus              store.GenerationScheduleStatus            `json:"schedule_status"`
	Priority                    int                                       `json:"priority"`
	Attempt                     int                                       `json:"attempt"`
	ChunkStatus                 store.GenerationChunkStatus               `json:"chunk_status"`
	Leased                      bool                                      `json:"leased"`
	CheckpointStateDigest       string                                    `json:"-"`
	PrivateLeaseTokenDigest     string                                    `json:"-"`
	CanonicalResultExists       bool                                      `json:"canonical_result_exists"`
	CompletionFileExists        bool                                      `json:"completion_file_exists"`
	CompletionBitSet            bool                                      `json:"completion_bit_set"`
	RootExists                  bool                                      `json:"root_exists"`
	RootDigest                  string                                    `json:"root_digest,omitempty"`
	Current                     bool                                      `json:"current"`
}

// ReadCheckpointRestartTransition reads either the prepared checkpoint hit or
// the recovered completion. The store-owned requeue remains synchronization.
func (runtime *Runtime) ReadCheckpointRestartTransition(
	ctx context.Context,
	request CheckpointRestartTransitionRequest,
) (CheckpointRestartTransition, error) {
	transition := request.Transition
	if ctx == nil || runtime == nil || runtime.validate() != nil ||
		transition.Stage != ScheduleStage ||
		transition.ResourceClass != store.GenerationResourceExtraction ||
		!validDigest(transition.Generation) || !validDigest(transition.ScheduleDigest) ||
		!validDigest(transition.ChunkIdentity) ||
		!validCheckpointRestartTransitionEvent(transition) ||
		transition.Length != 1 || transition.Attempt != 0 || transition.Offset < 0 ||
		!validDigest(request.TargetGeneration) ||
		request.TargetGeneration == transition.Generation ||
		!validDigest(request.PriorScheduleDigest) ||
		!boundedIdentity(request.Domain, 128) || request.Ordinal < 0 ||
		!validDigest(request.PlanDigest) || !validDigest(request.ResultIdentity) {
		return CheckpointRestartTransition{}, invalid("checkpoint restart transition request")
	}
	reader, ok := runtime.Store.(staleLeaseTransitionStore)
	if !ok {
		return CheckpointRestartTransition{}, invalid("checkpoint restart transition store")
	}
	storeRequest := store.GenerationStaleLeaseTransitionRequest{
		Point: transition.Point, Repository: transition.Repository,
		Stage: transition.Stage, Generation: transition.Generation,
		ResourceClass: transition.ResourceClass, ScheduleDigest: transition.ScheduleDigest,
		ChunkIdentity: transition.ChunkIdentity, Offset: transition.Offset,
		Length: transition.Length, Attempt: transition.Attempt,
	}
	before, err := reader.ReadGenerationStaleLeaseTransition(ctx, storeRequest)
	if err != nil {
		return CheckpointRestartTransition{}, err
	}
	if !sameStaleLeaseTransitionTarget(before, transition) ||
		transition.Point == store.GenerationStaleLeaseTransitionCheckpointHit &&
			(!validDigest(before.CheckpointStateDigest) ||
				before.ScheduleStatus != transition.ScheduleStatus ||
				before.Priority != transition.Priority || before.ChunkStatus != transition.ChunkStatus ||
				before.Leased != transition.Leased ||
				before.PrivateLeaseTokenDigest != transition.PrivateLeaseTokenDigest) {
		return CheckpointRestartTransition{}, ErrStale
	}

	target, err := runtime.readPreparedTransitionTarget(
		ctx, transition, request.TargetGeneration, request.PriorScheduleDigest,
		request.Domain, request.Ordinal, request.PlanDigest, request.ResultIdentity,
	)
	if err != nil {
		return CheckpointRestartTransition{}, err
	}
	resultDirectory := filepath.Join(
		runtime.generationDirectory(transition.Repository, request.TargetGeneration),
		domainKey(target.descriptor.Domain),
	)
	completionBitSet, rootExists, current, rootDigest, err := func() (bool, bool, bool, string, error) {
		lock := runtime.assemblyLock(target.domain.Plan.Digest)
		if err := lockRecoveryPreparation(ctx, lock); err != nil {
			return false, false, false, "", err
		}
		defer lock.Unlock()
		completion, err := readCompletionControlContext(
			ctx, filepath.Join(resultDirectory, completionName()), target.domain.Plan,
		)
		if err != nil {
			return false, false, false, "", err
		}
		mask := byte(1 << (target.ordinal % 8))
		completionBitSet := completion.Bits[target.ordinal/8]&mask != 0
		root, rootExists, err := readDomainRootContext(
			ctx, filepath.Join(resultDirectory, rootName()), target.domain.Plan,
		)
		if err != nil {
			return false, false, false, "", err
		}
		pointer, pointerErr := runtime.readCurrentPointerContext(
			ctx, transition.Repository, target.descriptor.Domain,
		)
		current := pointerErr == nil
		switch transition.Point {
		case store.GenerationStaleLeaseTransitionCheckpointHit:
			if completion.Count != completion.Expected-1 || completionBitSet || rootExists || current {
				return false, false, false, "", ErrStale
			}
			if pointerErr != nil && !errors.Is(pointerErr, os.ErrNotExist) {
				return false, false, false, "", pointerErr
			}
			return completionBitSet, rootExists, current, "", nil
		case store.GenerationStaleLeaseTransitionRecovered:
			if completion.Count != completion.Expected || !completionBitSet || !rootExists ||
				pointerErr != nil || target.ordinal >= len(root.Results) ||
				root.Results[target.ordinal] != target.result ||
				pointer.GenerationDigest != target.generation.Digest ||
				pointer.PlanDigest != target.domain.Plan.Digest || pointer.RootDigest != root.Digest ||
				pointer.RootName != filepath.Join(domainKey(target.descriptor.Domain), rootName()) {
				return false, false, false, "", errors.Join(pointerErr, ErrStale)
			}
			return completionBitSet, rootExists, current, root.Digest, nil
		default:
			return false, false, false, "", ErrStale
		}
	}()
	if err != nil {
		return CheckpointRestartTransition{}, err
	}
	after, err := reader.ReadGenerationStaleLeaseTransition(ctx, storeRequest)
	if err != nil {
		return CheckpointRestartTransition{}, err
	}
	if transition.Point == store.GenerationStaleLeaseTransitionCheckpointHit {
		if !sameCheckpointRestartStoreState(after, before) {
			return CheckpointRestartTransition{}, ErrStale
		}
	} else if after != before {
		return CheckpointRestartTransition{}, ErrStale
	}
	return CheckpointRestartTransition{
		Point: transition.Point, TargetGeneration: target.generation.Digest,
		ScheduleGeneration:  target.binding.ScheduleGeneration,
		PriorScheduleDigest: target.binding.PriorSchedule,
		ScheduleDigest:      transition.ScheduleDigest, ChunkIdentity: transition.ChunkIdentity,
		Domain: target.descriptor.Domain, Ordinal: target.ordinal,
		PlanDigest: target.domain.Plan.Digest, ResultIdentity: target.result.Identity,
		ResultDigest: target.result.Digest, ExpectationDigest: target.result.ExpectationDigest,
		PartitionDigest:             target.result.PartitionDigest,
		CandidateGenerationDigest:   target.result.CandidateGenerationDigest,
		SourceGenerationDigest:      target.result.SourceGenerationDigest,
		ObservationGenerationDigest: target.result.ObservationGenerationDigest,
		ExtractorVersion:            target.result.ExtractorVersion,
		ExtractionPolicyDigest:      target.result.ExtractionPolicyDigest,
		ScheduleStatus:              before.ScheduleStatus, Priority: before.Priority,
		Attempt: before.Attempt, ChunkStatus: before.ChunkStatus, Leased: before.Leased,
		CheckpointStateDigest:   before.CheckpointStateDigest,
		PrivateLeaseTokenDigest: transition.PrivateLeaseTokenDigest,
		CanonicalResultExists:   true, CompletionFileExists: true,
		CompletionBitSet: completionBitSet, RootExists: rootExists,
		RootDigest: rootDigest, Current: current,
	}, nil
}

func validCheckpointRestartTransitionEvent(value store.GenerationStaleLeaseTransition) bool {
	switch value.Point {
	case store.GenerationStaleLeaseTransitionCheckpointHit:
		return value.ScheduleStatus == store.GenerationScheduleActive &&
			value.Priority == store.GenerationPriorityNeverRun &&
			value.ChunkStatus == store.GenerationChunkRunning && value.Leased &&
			validDigest(value.PrivateLeaseTokenDigest) && value.StaleBefore.IsZero()
	case store.GenerationStaleLeaseTransitionRecovered:
		return value.Priority == store.GenerationPriorityStale &&
			value.ChunkStatus == store.GenerationChunkDone && !value.Leased &&
			validDigest(value.PrivateLeaseTokenDigest) && value.StaleBefore.IsZero()
	default:
		return false
	}
}

func sameCheckpointRestartStoreState(
	left, right store.GenerationStaleLeaseTransition,
) bool {
	return sameStaleLeaseTransitionTarget(left, right) &&
		left.ScheduleStatus == right.ScheduleStatus && left.Priority == right.Priority &&
		left.ChunkStatus == right.ChunkStatus && left.Leased == right.Leased &&
		left.CheckpointStateDigest == right.CheckpointStateDigest &&
		left.PrivateLeaseTokenDigest == right.PrivateLeaseTokenDigest
}
