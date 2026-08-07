package observationpublication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	PlanningScheduleStage            = "go-source-observation-plan"
	PlanningScheduleMaxAttempts      = 5
	PlanningScheduleRepositoryTokens = 1
	PlanningBindingSchema            = "phebs-observation-planning-binding-v1"
)

// PlanningEnqueue is the closed, source-free result of one bounded request.
// It is operational diagnostics only; it does not establish publication.
type PlanningEnqueue string

const (
	PlanningCurrent  PlanningEnqueue = "current"
	PlanningActive   PlanningEnqueue = "active"
	PlanningFailed   PlanningEnqueue = "failed"
	PlanningEnqueued PlanningEnqueue = "enqueued"
)

type planningBinding struct {
	Schema                 string `json:"schema"`
	Repository             string `json:"repository"`
	ScheduleGeneration     string `json:"schedule_generation"`
	TargetGeneration       string `json:"target_generation"`
	PriorScheduleDigest    string `json:"prior_schedule_digest,omitempty"`
	SourceGenerationDigest string `json:"source_generation_digest"`
	PolicyDigest           string `json:"policy_digest"`
}

// EnqueuePlanning gives the current source generation durable planning
// ownership. It reads bounded controls only; source members and Git objects are
// opened exclusively by HandlePlanning after ownership exists.
func (runtime *Runtime) EnqueuePlanning(
	ctx context.Context, repository string,
) (PlanningEnqueue, error) {
	if err := runtime.validatePlanningRuntime(repository); err != nil {
		return "", planningFailure(err)
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), repository,
	)
	if err != nil {
		return "", planningFailure(err)
	}
	root := filepath.Join(runtime.DataDir, "observations")
	publicationCurrent := currentMatchesSource(root, repository, source.Digest)
	target, policyDigest, err := planningTarget(repository, source.Digest)
	if err != nil {
		return "", planningFailure(err)
	}
	current, err := runtime.Store.GetGenerationSchedule(ctx, repository, PlanningScheduleStage)
	if errors.Is(err, store.ErrNotFound) {
		if publicationCurrent {
			if err := runtime.afterPublish(ctx, repository); err != nil {
				return "", workFailure(err)
			}
			return PlanningCurrent, nil
		}
		return runtime.enqueuePlanning(ctx, planningBinding{
			Schema: PlanningBindingSchema, Repository: repository,
			ScheduleGeneration: target, TargetGeneration: target,
			SourceGenerationDigest: source.Digest, PolicyDigest: policyDigest,
		})
	}
	if err != nil {
		return "", planningFailure(err)
	}
	currentTarget, targetErr := runtime.planningScheduleTargetFor(
		repository, target, *current,
	)
	if targetErr != nil {
		return "", planningFailure(targetErr)
	}
	if currentTarget == target {
		if publicationCurrent {
			if err := runtime.afterPublish(ctx, repository); err != nil {
				return "", workFailure(err)
			}
			return PlanningCurrent, nil
		}
		switch {
		case current.Status == store.GenerationScheduleActive:
			return PlanningActive, nil
		case current.Status == store.GenerationScheduleSettled && current.Failed > 0:
			return PlanningFailed, nil
		case current.Status == store.GenerationScheduleSettled:
			// Publication may have completed after the first bounded pointer
			// read but before this schedule read. Recheck before minting recovery
			// ownership for work that is already current.
			if currentMatchesSource(root, repository, source.Digest) {
				if err := runtime.afterPublish(ctx, repository); err != nil {
					return "", workFailure(err)
				}
				return PlanningCurrent, nil
			}
			owned, ownedErr := runtime.executionOwnsSource(ctx, repository, source.Digest)
			if ownedErr != nil {
				return "", planningFailure(ownedErr)
			}
			if owned {
				return PlanningActive, nil
			}
		}
	}
	return runtime.enqueuePlanning(ctx, planningBinding{
		Schema: PlanningBindingSchema, Repository: repository,
		ScheduleGeneration: recoveryPlanningGeneration(target, current.Digest),
		TargetGeneration:   target, PriorScheduleDigest: current.Digest,
		SourceGenerationDigest: source.Digest, PolicyDigest: policyDigest,
	})
}

// HandlePlanning performs the formerly synchronous source-partition census.
// The short final fence, not the long census, holds the repository mirror lock.
func (runtime *Runtime) HandlePlanning(ctx context.Context, chunk store.GenerationChunk) error {
	if err := runtime.validatePlanningRuntime(chunk.Repository); err != nil ||
		chunk.Stage != PlanningScheduleStage || chunk.Offset != 0 || chunk.Length != 1 ||
		!validDigest(chunk.Generation) || !validDigest(chunk.ScheduleDigest) {
		return terminalPlanningFailure(errors.Join(err, invalid("planning runtime chunk")))
	}
	binding, err := runtime.readPlanningBinding(chunk.Repository, chunk.Generation)
	if err != nil {
		return terminalPlanningFailure(errors.Join(err, invalid("planning runtime binding")))
	}
	if err := runtime.checkPlanningFence(ctx, chunk, binding, false); err != nil {
		return planningFailure(err)
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), chunk.Repository,
	)
	if err != nil || source.Digest != binding.SourceGenerationDigest {
		return planningFailure(errors.Join(err, store.ErrGenerationStale))
	}
	err = runtime.reconcileSource(
		ctx, chunk.Repository, filepath.Join(runtime.DataDir, "index"), source,
		func(fenceCtx context.Context) (func(), error) {
			return runtime.acquirePlanningFence(fenceCtx, chunk, binding)
		},
	)
	if err == nil {
		return nil
	}
	if fenceErr := runtime.checkPlanningFence(ctx, chunk, binding, false); fenceErr != nil {
		return planningFailure(fenceErr)
	}
	return terminalPlanningFailure(err)
}

func (runtime *Runtime) enqueuePlanning(
	ctx context.Context, binding planningBinding,
) (PlanningEnqueue, error) {
	if err := runtime.writePlanningBinding(binding); err != nil {
		return "", planningFailure(err)
	}
	_, err := runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: binding.Repository, Stage: PlanningScheduleStage,
		Generation: binding.ScheduleGeneration, ResourceClass: store.GenerationResourceIO,
		TotalItems: 1, ChunkItems: 1, MaxAttempts: PlanningScheduleMaxAttempts,
		RepositoryTokens: PlanningScheduleRepositoryTokens,
	})
	if err == nil {
		return PlanningEnqueued, nil
	}
	if !errors.Is(err, store.ErrGenerationStale) {
		return "", planningFailure(err)
	}
	current, currentErr := runtime.Store.GetGenerationSchedule(
		ctx, binding.Repository, PlanningScheduleStage,
	)
	if currentErr != nil {
		return "", planningFailure(errors.Join(err, currentErr))
	}
	currentTarget, targetErr := runtime.planningScheduleTargetFor(
		binding.Repository, binding.TargetGeneration, *current,
	)
	if targetErr != nil {
		return "", planningFailure(errors.Join(err, targetErr))
	}
	if currentTarget == binding.TargetGeneration {
		if current.Status == store.GenerationScheduleActive {
			return PlanningActive, nil
		}
		if current.Status == store.GenerationScheduleSettled && current.Failed > 0 {
			return PlanningFailed, nil
		}
		if current.Status == store.GenerationScheduleSettled {
			if currentMatchesSource(
				filepath.Join(runtime.DataDir, "observations"),
				binding.Repository,
				binding.SourceGenerationDigest,
			) {
				if err := runtime.afterPublish(ctx, binding.Repository); err != nil {
					return "", workFailure(err)
				}
				return PlanningCurrent, nil
			}
			owned, ownedErr := runtime.executionOwnsSource(
				ctx, binding.Repository, binding.SourceGenerationDigest,
			)
			if ownedErr != nil {
				return "", planningFailure(ownedErr)
			}
			if owned {
				return PlanningActive, nil
			}
		}
	}
	recovery := binding
	recovery.ScheduleGeneration = recoveryPlanningGeneration(binding.TargetGeneration, current.Digest)
	recovery.PriorScheduleDigest = current.Digest
	if recovery.ScheduleGeneration == binding.ScheduleGeneration {
		return "", planningFailure(err)
	}
	if err := runtime.writePlanningBinding(recovery); err != nil {
		return "", planningFailure(err)
	}
	_, err = runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: recovery.Repository, Stage: PlanningScheduleStage,
		Generation: recovery.ScheduleGeneration, ResourceClass: store.GenerationResourceIO,
		TotalItems: 1, ChunkItems: 1, MaxAttempts: PlanningScheduleMaxAttempts,
		RepositoryTokens: PlanningScheduleRepositoryTokens,
	})
	if err != nil {
		return "", planningFailure(err)
	}
	return PlanningEnqueued, nil
}

func (runtime *Runtime) executionOwnsSource(
	ctx context.Context, repository, sourceDigest string,
) (bool, error) {
	schedule, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if schedule.Status != store.GenerationScheduleActive {
		return false, nil
	}
	binding, err := runtime.readScheduleBinding(repository, schedule.Generation)
	if err == nil {
		return binding.SourceGenerationDigest == sourceDigest, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	marker, present, markerErr := readMarker(filepath.Join(runtime.DataDir, "observations"), repository)
	if markerErr != nil || !present || marker.GenerationDigest != schedule.Generation {
		return false, markerErr
	}
	manifest, manifestErr := sourcepartition.ReadManifest(
		runtime.planDirectory(repository, schedule.Generation), repository,
	)
	return manifestErr == nil && manifest.SourceGenerationDigest == sourceDigest, manifestErr
}

func (runtime *Runtime) acquirePlanningFence(
	ctx context.Context, chunk store.GenerationChunk, binding planningBinding,
) (func(), error) {
	repositoryDirectory, err := phebssync.SafeRepoDir(runtime.DataDir, chunk.Repository)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		releaseTransition, err := runtime.AcquireTransition(ctx)
		if err != nil {
			return nil, err
		}
		// Index publication takes the repository lock before its short shared
		// mutation fence, while lifecycle reconciliation takes the shared fence
		// before the repository lock. Never wait for the repository while holding
		// the exclusive fence: probe briefly, release, and retry so neither
		// existing order can form a cycle with planning.
		probeCtx, cancelProbe := context.WithTimeout(ctx, 25*time.Millisecond)
		releaseRepository, lockErr := repowork.LockContext(probeCtx, repositoryDirectory)
		cancelProbe()
		if lockErr == nil {
			if err := runtime.checkPlanningFence(ctx, chunk, binding, true); err != nil {
				releaseRepository()
				releaseTransition()
				return nil, err
			}
			return func() {
				releaseRepository()
				releaseTransition()
			}, nil
		}
		releaseTransition()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !errors.Is(lockErr, context.DeadlineExceeded) &&
			!errors.Is(lockErr, context.Canceled) {
			return nil, lockErr
		}
		retry := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !retry.Stop() {
				<-retry.C
			}
			return nil, ctx.Err()
		case <-retry.C:
		}
	}
}

func (runtime *Runtime) checkPlanningFence(
	ctx context.Context, chunk store.GenerationChunk, binding planningBinding, sourceOnly bool,
) error {
	if !sourceOnly {
		schedule, err := runtime.Store.GetGenerationSchedule(
			ctx, chunk.Repository, PlanningScheduleStage,
		)
		if err != nil {
			return err
		}
		if schedule.Generation != chunk.Generation || schedule.Digest != chunk.ScheduleDigest ||
			schedule.Status != store.GenerationScheduleActive {
			return store.ErrGenerationStale
		}
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), chunk.Repository,
	)
	if err != nil {
		return err
	}
	if source.Digest != binding.SourceGenerationDigest {
		return store.ErrGenerationStale
	}
	if sourceOnly {
		schedule, err := runtime.Store.GetGenerationSchedule(
			ctx, chunk.Repository, PlanningScheduleStage,
		)
		if err != nil || schedule.Generation != chunk.Generation || schedule.Digest != chunk.ScheduleDigest ||
			schedule.Status != store.GenerationScheduleActive {
			return errors.Join(err, store.ErrGenerationStale)
		}
		// Renewing the exact claimed lease makes the filesystem transition's
		// side-effect window linearize after any concurrent reaper. A reaper
		// that already won makes this fail before marker or pointer mutation.
		if err := runtime.Store.HeartbeatGenerationChunk(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func planningPolicy() sourcepartition.Policy {
	return sourcepartition.Policy{
		Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
		IncludeSuffixes: []string{".go"},
	}
}

func planningTarget(repository, sourceDigest string) (string, string, error) {
	policyDigest, err := sourcepartition.PolicyDigest(planningPolicy())
	if err != nil {
		return "", "", err
	}
	target, err := sourcepartition.GenerationDigest(repository, sourceDigest, policyDigest)
	return target, policyDigest, err
}

func recoveryPlanningGeneration(target, priorScheduleDigest string) string {
	return digest("phebs-observation-planning-recovery-v1", target+"\x00"+priorScheduleDigest)
}

func (runtime *Runtime) validatePlanningRuntime(repository string) error {
	if runtime == nil || !filepath.IsAbs(runtime.DataDir) || runtime.Store == nil ||
		runtime.Cache == nil || runtime.AcquireTransition == nil ||
		validateRepository(repository) != nil {
		return invalid("planning runtime configuration")
	}
	return nil
}

func confirmHistoricalPublicationControl(root string, publication *Publication) error {
	if publication == nil {
		return invalid("historical publication")
	}
	want, err := json.Marshal(publication.manifest)
	if err != nil {
		return err
	}
	raw, err := readBoundedRegular(
		filepath.Join(
			generationDirectory(
				root, publication.manifest.Repository, publication.manifest.GenerationDigest,
			),
			manifestName(),
		),
		MaxManifestBytes,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, want) {
		return invalid("historical publication control")
	}
	return nil
}

func (runtime *Runtime) writePlanningBinding(binding planningBinding) error {
	if err := validatePlanningBinding(binding); err != nil {
		return err
	}
	path := runtime.planningBindingPath(binding.Repository, binding.ScheduleGeneration)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := writeExclusiveCanonical(path, binding); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := runtime.readPlanningBinding(
			binding.Repository, binding.ScheduleGeneration,
		)
		if readErr != nil || existing != binding {
			return errors.Join(readErr, invalid("planning binding collision"))
		}
	}
	return syncDirectory(filepath.Dir(path))
}

func (runtime *Runtime) readPlanningBinding(
	repository, scheduleGeneration string,
) (planningBinding, error) {
	raw, err := readBoundedRegular(
		runtime.planningBindingPath(repository, scheduleGeneration), MaxManifestBytes,
	)
	if err != nil {
		return planningBinding{}, err
	}
	var binding planningBinding
	if decodeCanonical(raw, &binding) != nil || validatePlanningBinding(binding) != nil ||
		binding.Repository != repository || binding.ScheduleGeneration != scheduleGeneration {
		return planningBinding{}, invalid("planning binding")
	}
	return binding, nil
}

func (runtime *Runtime) planningScheduleTarget(
	repository string, schedule store.GenerationSchedule,
) (string, error) {
	binding, err := runtime.readPlanningBinding(repository, schedule.Generation)
	if err != nil {
		return "", err
	}
	return binding.TargetGeneration, nil
}

// planningScheduleTargetFor recognizes the initial target directly from the
// immutable schedule generation. Recovery identities still require their
// canonical binding; an unreadable recovery control fails closed rather than
// silently superseding authority of unknown provenance.
func (runtime *Runtime) planningScheduleTargetFor(
	repository, target string, schedule store.GenerationSchedule,
) (string, error) {
	if schedule.Generation == target {
		return target, nil
	}
	return runtime.planningScheduleTarget(repository, schedule)
}

func validatePlanningBinding(binding planningBinding) error {
	if binding.Schema != PlanningBindingSchema || validateRepository(binding.Repository) != nil ||
		!validDigest(binding.ScheduleGeneration) || !validDigest(binding.TargetGeneration) ||
		!validDigest(binding.SourceGenerationDigest) || !validDigest(binding.PolicyDigest) {
		return invalid("planning binding")
	}
	target, policyDigest, err := planningTarget(binding.Repository, binding.SourceGenerationDigest)
	if err != nil || target != binding.TargetGeneration || policyDigest != binding.PolicyDigest {
		return errors.Join(err, invalid("planning binding target"))
	}
	if binding.ScheduleGeneration == binding.TargetGeneration {
		if binding.PriorScheduleDigest != "" {
			return invalid("initial planning binding")
		}
	} else if !validDigest(binding.PriorScheduleDigest) ||
		binding.ScheduleGeneration != recoveryPlanningGeneration(
			binding.TargetGeneration, binding.PriorScheduleDigest,
		) {
		return invalid("recovery planning binding")
	}
	return nil
}

func (runtime *Runtime) planningBindingPath(repository, scheduleGeneration string) string {
	return filepath.Join(
		runtime.DataDir, "observation-plans", repositoryHash(repository),
		"planning-"+strings.TrimPrefix(scheduleGeneration, "sha256:")+".json",
	)
}

func terminalPlanningFailure(err error) error {
	if err == nil {
		return nil
	}
	closed := planningFailure(err)
	receipt, ok := pipelinerefusal.From(closed)
	if ok && (receipt.Classification == pipelinerefusal.ClassificationLimit ||
		receipt.Classification == pipelinerefusal.ClassificationInvalid) {
		return store.WithTerminal(closed)
	}
	return closed
}
