package observationpublication

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	InventoryScheduleStageV2            = "go-source-observation-inventory-v2"
	InventoryScheduleMaxAttemptsV2      = 5
	InventoryScheduleRepositoryTokensV2 = 1
)

var inventoryRuntimeStripes [64]sync.Mutex

// enqueueInventoryV2 gives the current source generation direct durable
// ownership for construction of the joint source-super-root and segmented
// observation inventory. A selected v2 runtime never requires a fresh v1
// publication: v1 remains historical authority, while the current source and
// the v2 schedule/pointer are the complete cutover fence.
func (runtime *Runtime) enqueueInventoryV2(
	ctx context.Context, repository string,
) (PlanningEnqueue, error) {
	if runtime == nil || !runtime.InventoryV2 || runtime.Store == nil ||
		!filepath.IsAbs(runtime.DataDir) || validateRepository(repository) != nil {
		return "", planningFailure(invalid("inventory v2 runtime configuration"))
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), repository,
	)
	if err != nil {
		return "", planningFailure(err)
	}
	if current, currentErr := CurrentInventoryDownstreamAuthorityV2(
		ctx, filepath.Join(runtime.DataDir, "observations"), repository,
	); currentErr == nil {
		if current.SourceGenerationDigest == source.Digest {
			return PlanningCurrent, nil
		}
	} else if !errors.Is(currentErr, os.ErrNotExist) {
		return "", planningFailure(currentErr)
	}
	target, policyDigest, err := planningTarget(repository, source.Digest)
	if err != nil {
		return "", planningFailure(err)
	}
	current, err := runtime.Store.GetGenerationSchedule(ctx, repository, InventoryScheduleStageV2)
	if errors.Is(err, store.ErrNotFound) {
		return runtime.enqueueInventoryScheduleV2(ctx, planningBinding{
			Schema: PlanningBindingSchema, Repository: repository,
			ScheduleGeneration: target, TargetGeneration: target,
			SourceGenerationDigest: source.Digest, PolicyDigest: policyDigest,
		}, PlanningEnqueued)
	}
	if err != nil {
		return "", planningFailure(err)
	}
	currentTarget, targetErr := runtime.planningScheduleTargetFor(repository, target, *current)
	if targetErr != nil {
		return "", planningFailure(targetErr)
	}
	if currentTarget == target {
		switch {
		case current.Status == store.GenerationScheduleActive:
			return PlanningActive, nil
		case current.Status == store.GenerationScheduleSettled && current.Failed > 0:
			return PlanningFailed, nil
		case current.Status == store.GenerationScheduleSettled:
			// A completed worker may have crossed its final filesystem rename but
			// lost the store completion response. Recheck current authority before
			// minting a distinguishable recovery schedule.
			if selected, selectedErr := CurrentInventoryDownstreamAuthorityV2(
				ctx, filepath.Join(runtime.DataDir, "observations"), repository,
			); selectedErr == nil && selected.SourceGenerationDigest == source.Digest {
				return PlanningCurrent, nil
			} else if selectedErr != nil && !errors.Is(selectedErr, os.ErrNotExist) {
				return "", planningFailure(selectedErr)
			}
		}
	}
	recovery := planningBinding{
		Schema: PlanningBindingSchema, Repository: repository,
		ScheduleGeneration: recoveryPlanningGeneration(target, current.Digest),
		TargetGeneration:   target, PriorScheduleDigest: current.Digest,
		SourceGenerationDigest: source.Digest, PolicyDigest: policyDigest,
	}
	return runtime.enqueueInventoryScheduleV2(ctx, recovery, PlanningEnqueued)
}

func (runtime *Runtime) enqueueInventoryScheduleV2(
	ctx context.Context,
	binding planningBinding,
	disposition PlanningEnqueue,
) (PlanningEnqueue, error) {
	if err := runtime.writePlanningBinding(binding); err != nil {
		return "", planningFailure(err)
	}
	_, err := runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: binding.Repository, Stage: InventoryScheduleStageV2,
		Generation: binding.ScheduleGeneration, ResourceClass: store.GenerationResourceIO,
		TotalItems: 1, ChunkItems: 1, MaxAttempts: InventoryScheduleMaxAttemptsV2,
		RepositoryTokens: InventoryScheduleRepositoryTokensV2,
	})
	if err == nil {
		return disposition, nil
	}
	if !errors.Is(err, store.ErrGenerationStale) {
		return "", planningFailure(err)
	}
	current, currentErr := runtime.Store.GetGenerationSchedule(
		ctx, binding.Repository, InventoryScheduleStageV2,
	)
	if currentErr != nil {
		return "", planningFailure(errors.Join(err, currentErr))
	}
	currentTarget, targetErr := runtime.planningScheduleTargetFor(
		binding.Repository, binding.TargetGeneration, *current,
	)
	if targetErr != nil || currentTarget != binding.TargetGeneration {
		return "", planningFailure(errors.Join(err, targetErr))
	}
	if current.Status == store.GenerationScheduleActive {
		return PlanningActive, nil
	}
	if current.Status == store.GenerationScheduleSettled && current.Failed > 0 {
		return PlanningFailed, nil
	}
	return "", planningFailure(err)
}

// HandleInventoryV2 builds the joint v2 authority outside the mutation fence,
// then renews the exact scheduler lease and installs it while holding the same
// exclusive boundary used by index, lifecycle, backup, and relationship work.
func (runtime *Runtime) HandleInventoryV2(ctx context.Context, chunk store.GenerationChunk) error {
	if runtime == nil || !runtime.InventoryV2 || chunk.Stage != InventoryScheduleStageV2 ||
		chunk.Offset != 0 || chunk.Length != 1 || !validDigest(chunk.Generation) ||
		!validDigest(chunk.ScheduleDigest) {
		return terminalInventoryFailure(invalid("inventory v2 runtime chunk"))
	}
	hash := sha256.Sum256([]byte(chunk.Repository))
	stripe := &inventoryRuntimeStripes[int(hash[0])%len(inventoryRuntimeStripes)]
	stripe.Lock()
	defer stripe.Unlock()
	source, err := runtime.checkInventoryV2Fence(ctx, chunk, false)
	if err != nil {
		return terminalInventoryFailure(err)
	}
	if _, err := runtime.CollectSupersededPlans(ctx, chunk, CollectRemovalLimit); err != nil {
		return terminalInventoryFailure(err)
	}
	root := filepath.Join(runtime.DataDir, "observations")
	if current, currentErr := CurrentInventoryDownstreamAuthorityV2(ctx, root, chunk.Repository); currentErr == nil {
		if current.SourceGenerationDigest == source.Digest {
			return runtime.notifyPublished(ctx, chunk.Repository)
		}
	} else if !errors.Is(currentErr, os.ErrNotExist) {
		return terminalInventoryFailure(currentErr)
	}

	priorSource, priorInventory := runtime.inventoryV2PriorDirectories(root, chunk.Repository)
	transition, err := BeginInventoryPublicationV2(root, chunk.Repository)
	if err != nil {
		return terminalInventoryFailure(err)
	}
	plan, transition, err := runtime.buildInventoryV2Source(
		ctx, chunk, source, transition, priorSource,
	)
	if err != nil {
		return terminalInventoryFailure(err)
	}
	repositoryDirectory, err := phebssync.SafeRepoDir(runtime.DataDir, chunk.Repository)
	if err != nil {
		return terminalInventoryFailure(err)
	}
	if _, err := BuildInventoryStageV2(ctx, InventoryBuildRequestV2{
		OutputDirectory:     transition.InventoryDirectory,
		RepositoryDirectory: repositoryDirectory,
		Plan:                plan, PriorDirectory: priorInventory,
	}); err != nil {
		return terminalInventoryFailure(err)
	}
	release, err := runtime.acquireInventoryV2Fence(ctx, chunk, source.Digest)
	if err != nil {
		return terminalInventoryFailure(err)
	}
	_, publishErr := CompleteInventoryPublicationV2(
		ctx, root, chunk.Repository, transition.TransitionID, nil,
	)
	release()
	if publishErr != nil {
		return terminalInventoryFailure(publishErr)
	}
	return runtime.notifyPublished(ctx, chunk.Repository)
}

// terminalInventoryFailure stops only closed deterministic v2 refusals on the
// first attempt. Stale leases, crossed source generations, context failures,
// store unavailability, and downstream notification errors remain retryable.
func terminalInventoryFailure(err error) error {
	if err == nil {
		return nil
	}
	closed := workFailure(err)
	receipt, ok := pipelinerefusal.From(closed)
	if ok && (receipt.Classification == pipelinerefusal.ClassificationLimit ||
		receipt.Classification == pipelinerefusal.ClassificationInvalid) {
		return store.WithTerminal(closed)
	}
	return closed
}

func (runtime *Runtime) buildInventoryV2Source(
	ctx context.Context,
	chunk store.GenerationChunk,
	source repositoryindex.SourceManifest,
	transition InventoryPublicationTransitionV2,
	priorSource string,
) (*sourcepartition.SuperPlan, InventoryPublicationTransitionV2, error) {
	opened, err := sourcepartition.ReadSuperRoot(transition.SourceDirectory, chunk.Repository)
	if err == nil && opened.SourceGenerationDigest == source.Digest {
		plan, openErr := sourcepartition.OpenSuperRoot(ctx, transition.SourceDirectory, opened)
		return plan, transition, openErr
	}
	entries, readErr := os.ReadDir(transition.SourceDirectory)
	if readErr != nil {
		return nil, transition, readErr
	}
	if err == nil || len(entries) != 0 {
		restarted, restartErr := runtime.restartInventoryV2Transition(
			ctx, chunk, source.Digest, transition,
		)
		if restartErr != nil {
			return nil, transition, restartErr
		}
		transition = restarted
	}
	root, err := sourcepartition.BuildSuperRoot(ctx, sourcepartition.BuildRequest{
		SourceDirectory: filepath.Join(runtime.DataDir, "index"),
		OutputDirectory: transition.SourceDirectory,
		Repository:      chunk.Repository, Source: source, Policy: planningPolicy(),
		PriorSuperRootDirectory: priorSource,
	})
	if err != nil {
		return nil, transition, err
	}
	plan, err := sourcepartition.OpenSuperRoot(ctx, transition.SourceDirectory, root)
	return plan, transition, err
}

func (runtime *Runtime) restartInventoryV2Transition(
	ctx context.Context,
	chunk store.GenerationChunk,
	sourceDigest string,
	transition InventoryPublicationTransitionV2,
) (InventoryPublicationTransitionV2, error) {
	release, err := runtime.acquireInventoryV2Fence(ctx, chunk, sourceDigest)
	if err != nil {
		return InventoryPublicationTransitionV2{}, err
	}
	restarted, restartErr := RestartInventoryPublicationV2(
		ctx, filepath.Join(runtime.DataDir, "observations"), chunk.Repository,
		transition.TransitionID, nil,
	)
	release()
	return restarted, restartErr
}

func (runtime *Runtime) acquireInventoryV2Fence(
	ctx context.Context,
	chunk store.GenerationChunk,
	sourceDigest string,
) (func(), error) {
	if runtime.AcquireTransition == nil {
		return nil, invalid("inventory v2 transition fence")
	}
	release, err := runtime.AcquireTransition(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := runtime.checkInventoryV2Fence(ctx, chunk, true); err != nil {
		release()
		return nil, err
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), chunk.Repository,
	)
	if err != nil || source.Digest != sourceDigest {
		release()
		return nil, errors.Join(err, store.ErrGenerationStale)
	}
	return release, nil
}

func (runtime *Runtime) checkInventoryV2Fence(
	ctx context.Context,
	chunk store.GenerationChunk,
	heartbeat bool,
) (repositoryindex.SourceManifest, error) {
	schedule, err := runtime.Store.GetGenerationSchedule(
		ctx, chunk.Repository, InventoryScheduleStageV2,
	)
	if err != nil || schedule.Generation != chunk.Generation ||
		schedule.Digest != chunk.ScheduleDigest || schedule.Status != store.GenerationScheduleActive {
		return repositoryindex.SourceManifest{}, errors.Join(err, store.ErrGenerationStale)
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), chunk.Repository,
	)
	if err != nil {
		return repositoryindex.SourceManifest{}, err
	}
	binding, bindingErr := runtime.readPlanningBinding(chunk.Repository, chunk.Generation)
	if bindingErr != nil || binding.SourceGenerationDigest != source.Digest {
		return repositoryindex.SourceManifest{}, errors.Join(bindingErr, store.ErrGenerationStale)
	}
	target, _, err := planningTarget(chunk.Repository, source.Digest)
	if err != nil || target != binding.TargetGeneration {
		return repositoryindex.SourceManifest{}, errors.Join(err, store.ErrGenerationStale)
	}
	if heartbeat {
		if err := runtime.Store.HeartbeatGenerationChunk(ctx, chunk); err != nil {
			return repositoryindex.SourceManifest{}, err
		}
	}
	return source, nil
}

func (runtime *Runtime) inventoryV2PriorDirectories(root, repository string) (string, string) {
	current, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil {
		return "", ""
	}
	directory := inventoryGenerationDirectoryV2(root, repository, current.Current.GenerationDigest)
	return filepath.Join(directory, InventoryPublicationSourceNameV2),
		filepath.Join(directory, InventoryPublicationInventoryNameV2)
}
