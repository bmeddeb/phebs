package observationpublication

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"

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

// enqueueInventoryV2 gives one exact current v1 observation durable ownership
// for construction of the joint source-super-root and observation inventory.
// It returns true only when the v2 pointer already names the same source.
func (runtime *Runtime) enqueueInventoryV2(ctx context.Context, repository string) (bool, error) {
	if runtime == nil || !runtime.InventoryV2 || runtime.Store == nil ||
		!filepath.IsAbs(runtime.DataDir) || validateRepository(repository) != nil {
		return false, invalid("inventory v2 runtime configuration")
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), repository,
	)
	if err != nil || !currentMatchesSource(
		filepath.Join(runtime.DataDir, "observations"), repository, source.Digest,
	) {
		return false, errors.Join(err, store.ErrGenerationStale)
	}
	if current, currentErr := CurrentInventoryDownstreamAuthorityV2(
		ctx, filepath.Join(runtime.DataDir, "observations"), repository,
	); currentErr == nil {
		if current.SourceGenerationDigest == source.Digest {
			return true, nil
		}
	} else if !errors.Is(currentErr, os.ErrNotExist) {
		return false, currentErr
	}
	target, _, err := planningTarget(repository, source.Digest)
	if err != nil {
		return false, err
	}
	_, err = runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: repository, Stage: InventoryScheduleStageV2, Generation: target,
		ResourceClass: store.GenerationResourceIO, TotalItems: 1, ChunkItems: 1,
		MaxAttempts:      InventoryScheduleMaxAttemptsV2,
		RepositoryTokens: InventoryScheduleRepositoryTokensV2,
	})
	if errors.Is(err, store.ErrGenerationStale) {
		current, currentErr := runtime.Store.GetGenerationSchedule(
			ctx, repository, InventoryScheduleStageV2,
		)
		if currentErr == nil && current.Generation == target &&
			current.Status == store.GenerationScheduleActive {
			return false, nil
		}
	}
	return false, err
}

// HandleInventoryV2 builds the joint v2 authority outside the mutation fence,
// then renews the exact scheduler lease and installs it while holding the same
// exclusive boundary used by index, lifecycle, backup, and relationship work.
func (runtime *Runtime) HandleInventoryV2(ctx context.Context, chunk store.GenerationChunk) error {
	if runtime == nil || !runtime.InventoryV2 || chunk.Stage != InventoryScheduleStageV2 ||
		chunk.Offset != 0 || chunk.Length != 1 || !validDigest(chunk.Generation) ||
		!validDigest(chunk.ScheduleDigest) {
		return workFailure(invalid("inventory v2 runtime chunk"))
	}
	hash := sha256.Sum256([]byte(chunk.Repository))
	stripe := &inventoryRuntimeStripes[int(hash[0])%len(inventoryRuntimeStripes)]
	stripe.Lock()
	defer stripe.Unlock()
	source, err := runtime.checkInventoryV2Fence(ctx, chunk, false)
	if err != nil {
		return workFailure(err)
	}
	root := filepath.Join(runtime.DataDir, "observations")
	if current, currentErr := CurrentInventoryDownstreamAuthorityV2(ctx, root, chunk.Repository); currentErr == nil {
		if current.SourceGenerationDigest == source.Digest {
			return runtime.notifyPublished(ctx, chunk.Repository)
		}
	} else if !errors.Is(currentErr, os.ErrNotExist) {
		return workFailure(currentErr)
	}

	priorSource, priorInventory := runtime.inventoryV2PriorDirectories(root, chunk.Repository)
	transition, err := BeginInventoryPublicationV2(root, chunk.Repository)
	if err != nil {
		return workFailure(err)
	}
	plan, transition, err := runtime.buildInventoryV2Source(
		ctx, chunk, source, transition, priorSource,
	)
	if err != nil {
		return workFailure(err)
	}
	repositoryDirectory, err := phebssync.SafeRepoDir(runtime.DataDir, chunk.Repository)
	if err != nil {
		return workFailure(err)
	}
	if _, err := BuildInventoryStageV2(ctx, InventoryBuildRequestV2{
		OutputDirectory:     transition.InventoryDirectory,
		RepositoryDirectory: repositoryDirectory,
		Plan:                plan, PriorDirectory: priorInventory,
	}); err != nil {
		return workFailure(err)
	}
	release, err := runtime.acquireInventoryV2Fence(ctx, chunk, source.Digest)
	if err != nil {
		return workFailure(err)
	}
	_, publishErr := CompleteInventoryPublicationV2(
		ctx, root, chunk.Repository, transition.TransitionID, nil,
	)
	release()
	if publishErr != nil {
		return workFailure(publishErr)
	}
	return runtime.notifyPublished(ctx, chunk.Repository)
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
	if err != nil || source.Digest != sourceDigest || !currentMatchesSource(
		filepath.Join(runtime.DataDir, "observations"), chunk.Repository, sourceDigest,
	) {
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
	target, _, err := planningTarget(chunk.Repository, source.Digest)
	if err != nil || target != chunk.Generation {
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
