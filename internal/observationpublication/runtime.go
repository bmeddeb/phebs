package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	ScheduleStage            = "go-source-observation"
	ScheduleChunkItems       = 8
	ScheduleMaxAttempts      = 5
	ScheduleRepositoryTokens = 2
)

var ErrWorkUnavailable = errors.New("observation partition is unavailable")

type Runtime struct {
	DataDir string
	Store   store.GenerationSchedulerStore
	// Cache pins a prevalidated historical generation while planning performs
	// the short final reactivation fence. It is required by EnqueuePlanning and
	// HandlePlanning; legacy v1 Reconcile/Handle do not depend on it.
	Cache *Cache
	// AcquireTransition excludes lifecycle/index publication mutation for the
	// short final source/schedule re-fence and pointer/ownership transition.
	AcquireTransition func(context.Context) (func(), error)
	Admit             func(context.Context) error
	// OnPublished runs after an exact current observation is present. A
	// failure keeps the scheduler chunk retryable without rolling back the
	// already-complete content-addressed observation publication.
	OnPublished func(context.Context, string) error

	mu             sync.Mutex
	plans          map[string]*sourcepartition.Plan
	activeBuilds   map[string]int
	retiringBuilds map[string]bool
	planningStages map[string]bool
	// planMutation serializes the short destructive planning-namespace fence
	// with binding publication and in-process build/stage admission.
	planMutation sync.Mutex
	transition   sync.Mutex
}

// Reconcile binds the current complete repository source generation to one
// durable observation schedule. Callers serialize it with repository index
// publication; the normal index completion hook already holds that lock.
func (runtime *Runtime) Reconcile(ctx context.Context, repository string) error {
	if runtime == nil || !filepath.IsAbs(runtime.DataDir) || runtime.Store == nil || validateRepository(repository) != nil {
		return invalid("runtime configuration")
	}
	sourceDirectory := filepath.Join(runtime.DataDir, "index")
	source, err := repositoryindex.ReadSourceManifest(sourceDirectory, repository)
	if err != nil {
		return planningFailure(err)
	}
	return runtime.reconcileSource(ctx, repository, sourceDirectory, source, nil)
}

type reconcileFence func(context.Context) (func(), error)

func runReconcileFence(ctx context.Context, fence reconcileFence) (func(), error) {
	if fence == nil {
		return func() {}, nil
	}
	return fence(ctx)
}

func (runtime *Runtime) reconcileSource(
	ctx context.Context,
	repository,
	sourceDirectory string,
	source repositoryindex.SourceManifest,
	fence reconcileFence,
) error {
	root := filepath.Join(runtime.DataDir, "observations")
	if currentMatchesSource(root, repository, source.Digest) {
		release, err := runReconcileFence(ctx, fence)
		if err != nil {
			return planningFailure(err)
		}
		if !currentMatchesSource(root, repository, source.Digest) {
			release()
			return planningFailure(store.ErrGenerationStale)
		}
		release()
		if fence == nil {
			runtime.cleanupSettledSchedule(ctx, repository)
		}
		return runtime.afterPublish(ctx, repository)
	}
	runtime.cleanupSettledSchedule(ctx, repository)
	if runtime.Admit != nil {
		if err := runtime.Admit(ctx); err != nil {
			return workFailure(err)
		}
	}
	plan, _, cleanupPlanStage, err := runtime.buildPlan(
		ctx, repository, sourceDirectory, source,
	)
	if err != nil {
		return planningFailure(err)
	}
	defer cleanupPlanStage()
	partition := plan.Manifest()
	if err := checkPublicationLimit(
		ErrLimit, pipelinerefusal.DimensionGenerationRecords,
		int64(partition.BlobCount), MaxGenerationRecords,
	); err != nil {
		return workFailure(err)
	}
	generation, err := GenerationDigest(partition)
	if err != nil {
		return workFailure(err)
	}
	markerBeforeFence, markerBeforeFencePresent, err := readMarker(root, repository)
	if err != nil {
		return workFailure(err)
	}
	if markerBeforeFencePresent && markerBeforeFence.GenerationDigest != generation {
		releaseRetirement, err := runtime.quiesceBuild(
			ctx, repository, markerBeforeFence.GenerationDigest,
		)
		if err != nil {
			return workFailure(err)
		}
		defer releaseRetirement()
	}
	var historical *Lease
	if fence != nil {
		if runtime.Cache == nil {
			return planningFailure(invalid("runtime historical cache"))
		}
		historical, err = runtime.Cache.AcquireGeneration(ctx, root, repository, generation)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return workFailure(err)
		}
		if err != nil {
			historical = nil
		}
		if historical != nil {
			defer historical.Release()
		}
	}
	planDirectory := runtime.planDirectory(repository, generation)
	var existingPlan *sourcepartition.Plan
	if historical == nil {
		info, statErr := os.Lstat(planDirectory)
		if statErr == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return workFailure(invalid("runtime plan directory"))
			}
			existingPlan, err = sourcepartition.Open(ctx, planDirectory, partition)
			if err != nil {
				return workFailure(err)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return workFailure(statErr)
		}
	}
	releaseFence, err := runReconcileFence(ctx, fence)
	if err != nil {
		return planningFailure(err)
	}
	runtime.transition.Lock()
	released := false
	releaseTransition := func() {
		if released {
			return
		}
		released = true
		runtime.transition.Unlock()
		releaseFence()
	}
	defer releaseTransition()
	publication := (*Publication)(nil)
	if historical != nil {
		publication = historical.Publication()
		if err := confirmHistoricalPublicationControl(root, publication); err != nil {
			return workFailure(err)
		}
	} else if fence == nil {
		publication, _ = openGenerationDigest(ctx, root, repository, generation)
	} else if _, err := os.Lstat(filepath.Join(
		generationDirectory(root, repository, generation), manifestName(),
	)); err == nil {
		// A concurrent v1 worker completed after the pre-lock open. Retry so the
		// next planning attempt validates and pins it outside the mirror lock.
		return planningFailure(store.ErrGenerationStale)
	} else if !errors.Is(err, os.ErrNotExist) {
		return workFailure(err)
	}
	if publication != nil {
		marker, present, err := readMarker(root, repository)
		if err != nil {
			return workFailure(err)
		}
		if present && marker.GenerationDigest != generation {
			if err := runtime.detachStage(root, repository, marker.GenerationDigest); err != nil {
				return workFailure(err)
			}
		}
		if err := activate(root, publication.manifest); err != nil {
			return workFailure(err)
		}
		if err := clearMarker(root, repository, generation); err != nil {
			return workFailure(err)
		}
		runtime.dropPlan(repository, generation)
		releaseTransition()
		return runtime.afterPublish(ctx, repository)
	}
	marker, markerPresent, err := readMarker(root, repository)
	if err != nil {
		return workFailure(err)
	}
	if markerPresent && marker.GenerationDigest != generation {
		if err := runtime.detachStage(root, repository, marker.GenerationDigest); err != nil {
			return workFailure(err)
		}
		markerPresent = false
	}
	if markerPresent {
		if _, err := os.Lstat(generationDirectory(root, repository, generation)); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return workFailure(err)
			}
			if err := os.Remove(markerPath(root, repository)); err != nil {
				return workFailure(err)
			}
			markerPresent = false
		}
	}
	if !markerPresent {
		if _, err := Begin(root, partition); err != nil {
			return workFailure(err)
		}
	}
	// The durable publication marker owns the final content-addressed plan
	// before it becomes visible. Until this point each planner retains only its
	// unique staging directory, so a stale creator cannot delete a replacement
	// worker's adopted plan.
	if existingPlan != nil {
		confirmed, confirmErr := sourcepartition.ReadManifest(planDirectory, repository)
		if confirmErr != nil || confirmed.Digest != partition.Digest {
			return workFailure(errors.Join(confirmErr, invalid("runtime plan changed before ownership")))
		}
		plan = existingPlan
	} else {
		plan, err = sourcepartition.MoveValidatedStage(plan, planDirectory)
		if err != nil {
			return workFailure(err)
		}
	}
	runtime.rememberPlan(repository, generation, plan)
	if len(partition.Members) == 0 {
		stage := resumedStage(root, partition, generation)
		if _, err := stage.Finalize(ctx); err != nil {
			return workFailure(err)
		}
		runtime.dropPlan(repository, generation)
		releaseTransition()
		_ = os.RemoveAll(planDirectory)
		return runtime.afterPublish(ctx, repository)
	}
	return workFailure(runtime.enqueueExecutionSchedule(ctx, repository, generation, partition))
}

func (runtime *Runtime) enqueueExecutionSchedule(
	ctx context.Context, repository, generation string, partition sourcepartition.Manifest,
) error {
	runtime.planMutation.Lock()
	defer runtime.planMutation.Unlock()
	scheduleGeneration, priorScheduleDigest, err := runtime.scheduleGeneration(ctx, repository, generation)
	if err != nil {
		return err
	}
	binding := scheduleBinding{
		Schema: BindingSchema, Repository: repository,
		ScheduleGeneration: scheduleGeneration, PublicationGeneration: generation,
		PriorScheduleDigest: priorScheduleDigest, SourceGenerationDigest: partition.SourceGenerationDigest,
		PartitionManifestDigest: partition.Digest,
	}
	if err := runtime.writeScheduleBinding(binding); err != nil {
		return err
	}
	enqueue := func(schedule string) error {
		_, enqueueErr := runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
			Repository: repository, Stage: ScheduleStage, Generation: schedule,
			ResourceClass: store.GenerationResourceCPU, TotalItems: int64(len(partition.Members)),
			ChunkItems: ScheduleChunkItems, MaxAttempts: ScheduleMaxAttempts,
			RepositoryTokens: ScheduleRepositoryTokens,
		})
		return enqueueErr
	}
	err = enqueue(scheduleGeneration)
	if !errors.Is(err, store.ErrGenerationStale) || scheduleGeneration != generation {
		return err
	}
	current, currentErr := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if currentErr != nil {
		return errors.Join(err, currentErr)
	}
	binding.ScheduleGeneration = recoveryScheduleGeneration(generation, current.Digest)
	binding.PriorScheduleDigest = current.Digest
	if bindingErr := runtime.writeScheduleBinding(binding); bindingErr != nil {
		return bindingErr
	}
	return enqueue(binding.ScheduleGeneration)
}

// Handle executes one durable scheduler chunk. Publication is attempted only
// after the final member exists. A slower earlier chunk also observes that
// sentinel and can finalize, so correctness does not depend on completion order.
func (runtime *Runtime) Handle(ctx context.Context, chunk store.GenerationChunk) error {
	if runtime == nil || chunk.Stage != ScheduleStage || !validDigest(chunk.Generation) ||
		chunk.Offset < 0 || chunk.Length < 1 {
		return workFailure(invalid("runtime chunk"))
	}
	targetGeneration, err := runtime.scheduleTarget(chunk.Repository, chunk.Generation)
	if err != nil {
		return workFailure(err)
	}
	releaseBuild, err := runtime.beginBuild(chunk.Repository, targetGeneration)
	if err != nil {
		return workFailure(err)
	}
	defer releaseBuild()
	root := filepath.Join(runtime.DataDir, "observations")
	if publication, err := openGenerationDigest(ctx, root, chunk.Repository, targetGeneration); err == nil {
		runtime.transition.Lock()
		defer runtime.transition.Unlock()
		if err := runtime.fenceChunk(ctx, chunk, publication.manifest.SourceGenerationDigest); err != nil {
			return workFailure(err)
		}
		if err := activate(root, publication.manifest); err != nil {
			return workFailure(err)
		}
		if err := clearMarker(root, chunk.Repository, targetGeneration); err != nil {
			return workFailure(err)
		}
		runtime.dropPlan(chunk.Repository, targetGeneration)
		return runtime.afterPublish(ctx, chunk.Repository)
	}
	plan, err := runtime.openPlan(ctx, chunk.Repository, targetGeneration)
	if err != nil {
		return workFailure(err)
	}
	partition := plan.Manifest()
	end := chunk.Offset + int64(chunk.Length)
	if end > int64(len(partition.Members)) {
		return workFailure(invalid("runtime chunk range"))
	}
	repositoryDirectory, err := phebssync.SafeRepoDir(runtime.DataDir, chunk.Repository)
	if err != nil {
		return workFailure(err)
	}
	stage := resumedStage(root, partition, targetGeneration)
	for ordinal := int(chunk.Offset); ordinal < int(end); ordinal++ {
		if err := stage.BuildPartition(ctx, plan, repositoryDirectory, ordinal, nil); err != nil {
			return workFailure(err)
		}
	}
	lastMember := filepath.Join(
		stage.directory, memberName(len(partition.Members)-1),
	)
	if _, err := os.Lstat(lastMember); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return workFailure(err)
	}
	runtime.transition.Lock()
	defer runtime.transition.Unlock()
	if err := runtime.fenceChunk(ctx, chunk, partition.SourceGenerationDigest); err != nil {
		return workFailure(err)
	}
	if publication, err := openGenerationDigest(ctx, root, chunk.Repository, targetGeneration); err == nil {
		if err := activate(root, publication.manifest); err != nil {
			return workFailure(err)
		}
	} else {
		if _, err := stage.Finalize(ctx); err != nil {
			return workFailure(err)
		}
	}
	runtime.dropPlan(chunk.Repository, targetGeneration)
	return runtime.afterPublish(ctx, chunk.Repository)
}

func (runtime *Runtime) afterPublish(ctx context.Context, repository string) error {
	if runtime.OnPublished == nil {
		return nil
	}
	return runtime.OnPublished(ctx, repository)
}

func (runtime *Runtime) cleanupSettledSchedule(ctx context.Context, repository string) {
	schedule, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if err != nil || schedule.Status != store.GenerationScheduleSettled || schedule.Failed != 0 {
		return
	}
	binding, err := runtime.readScheduleBinding(repository, schedule.Generation)
	if err != nil {
		return
	}
	runtime.dropPlan(repository, binding.PublicationGeneration)
	_ = os.RemoveAll(runtime.planDirectory(repository, binding.PublicationGeneration))
	_ = runtime.removeScheduleBinding(repository, schedule.Generation)
}

func (runtime *Runtime) scheduleGeneration(
	ctx context.Context, repository, target string,
) (string, string, error) {
	current, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if errors.Is(err, store.ErrNotFound) {
		return target, "", nil
	}
	if err != nil {
		return "", "", err
	}
	currentTarget, targetErr := runtime.scheduleTarget(repository, current.Generation)
	if targetErr == nil && currentTarget == target {
		if current.Status == store.GenerationScheduleActive {
			if current.Generation == target {
				return current.Generation, "", nil
			}
			binding, bindingErr := runtime.readScheduleBinding(repository, current.Generation)
			if bindingErr != nil {
				return "", "", bindingErr
			}
			return current.Generation, binding.PriorScheduleDigest, nil
		}
		return recoveryScheduleGeneration(target, current.Digest), current.Digest, nil
	}
	return target, "", nil
}

func recoveryScheduleGeneration(target, priorScheduleDigest string) string {
	return digest("phebs-observation-recovery-schedule-v1", target+"\x00"+priorScheduleDigest)
}

func (runtime *Runtime) scheduleTarget(repository, scheduleGeneration string) (string, error) {
	binding, err := runtime.readScheduleBinding(repository, scheduleGeneration)
	if err == nil {
		return binding.PublicationGeneration, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	marker, present, markerErr := readMarker(filepath.Join(runtime.DataDir, "observations"), repository)
	if markerErr != nil {
		return "", markerErr
	}
	if present && marker.GenerationDigest == scheduleGeneration {
		return scheduleGeneration, nil
	}
	pointer, pointerErr := readPointer(filepath.Join(runtime.DataDir, "observations"), repository)
	if pointerErr == nil && pointer.GenerationDigest == scheduleGeneration {
		return scheduleGeneration, nil
	}
	return "", invalid("runtime schedule binding")
}

func (runtime *Runtime) writeScheduleBinding(binding scheduleBinding) error {
	if validateScheduleBinding(binding) != nil {
		return invalid("runtime schedule binding")
	}
	path := runtime.scheduleBindingPath(binding.Repository, binding.ScheduleGeneration)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := writeExclusiveCanonical(path, binding); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := runtime.readScheduleBinding(binding.Repository, binding.ScheduleGeneration)
		if readErr != nil || existing != binding {
			return errors.Join(readErr, invalid("runtime schedule binding collision"))
		}
	}
	return syncDirectory(filepath.Dir(path))
}

func (runtime *Runtime) readScheduleBinding(repository, scheduleGeneration string) (scheduleBinding, error) {
	raw, err := readBoundedRegular(runtime.scheduleBindingPath(repository, scheduleGeneration), MaxManifestBytes)
	if err != nil {
		return scheduleBinding{}, err
	}
	var binding scheduleBinding
	if decodeCanonical(raw, &binding) != nil || validateScheduleBinding(binding) != nil ||
		binding.Repository != repository || binding.ScheduleGeneration != scheduleGeneration {
		return scheduleBinding{}, invalid("runtime schedule binding")
	}
	return binding, nil
}

func validateScheduleBinding(binding scheduleBinding) error {
	if binding.Schema != BindingSchema || validateRepository(binding.Repository) != nil ||
		!validDigest(binding.ScheduleGeneration) || !validDigest(binding.PublicationGeneration) ||
		!validDigest(binding.SourceGenerationDigest) || !validDigest(binding.PartitionManifestDigest) {
		return invalid("schedule binding")
	}
	if binding.ScheduleGeneration == binding.PublicationGeneration {
		if binding.PriorScheduleDigest != "" {
			return invalid("initial schedule binding")
		}
	} else if !validDigest(binding.PriorScheduleDigest) ||
		binding.ScheduleGeneration != recoveryScheduleGeneration(binding.PublicationGeneration, binding.PriorScheduleDigest) {
		return invalid("recovery schedule binding")
	}
	return nil
}

func (runtime *Runtime) removeScheduleBinding(repository, scheduleGeneration string) error {
	path := runtime.scheduleBindingPath(repository, scheduleGeneration)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func (runtime *Runtime) scheduleBindingPath(repository, scheduleGeneration string) string {
	return filepath.Join(
		runtime.DataDir, "observation-plans", repositoryHash(repository),
		"schedule-"+strings.TrimPrefix(scheduleGeneration, "sha256:")+".json",
	)
}

func (runtime *Runtime) fenceChunk(
	ctx context.Context, chunk store.GenerationChunk, sourceDigest string,
) error {
	schedule, err := runtime.Store.GetGenerationSchedule(ctx, chunk.Repository, ScheduleStage)
	if err != nil {
		return err
	}
	if schedule.Generation != chunk.Generation || schedule.Status != store.GenerationScheduleActive {
		return store.ErrGenerationStale
	}
	source, err := repositoryindex.ReadSourceManifest(filepath.Join(runtime.DataDir, "index"), chunk.Repository)
	if err != nil {
		return err
	}
	if source.Digest != sourceDigest {
		return store.ErrGenerationStale
	}
	return nil
}

func (runtime *Runtime) beginBuild(repository, generation string) (func(), error) {
	runtime.planMutation.Lock()
	defer runtime.planMutation.Unlock()
	key := planKey(repository, generation)
	runtime.mu.Lock()
	if runtime.retiringBuilds[key] {
		runtime.mu.Unlock()
		return nil, store.ErrGenerationStale
	}
	if runtime.activeBuilds == nil {
		runtime.activeBuilds = make(map[string]int)
	}
	runtime.activeBuilds[key]++
	runtime.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			runtime.mu.Lock()
			if runtime.activeBuilds[key] <= 1 {
				delete(runtime.activeBuilds, key)
			} else {
				runtime.activeBuilds[key]--
			}
			runtime.mu.Unlock()
		})
	}, nil
}

// quiesceBuild prevents new v1 CPU handlers from opening one stale stage and
// waits for already-admitted handlers to return before the directory is moved
// into lifecycle ownership. The scheduler heartbeat continues to own and
// cancel the waiting planning handler.
func (runtime *Runtime) quiesceBuild(
	ctx context.Context, repository, generation string,
) (func(), error) {
	key := planKey(repository, generation)
	runtime.mu.Lock()
	if runtime.retiringBuilds == nil {
		runtime.retiringBuilds = make(map[string]bool)
	}
	if runtime.retiringBuilds[key] {
		runtime.mu.Unlock()
		return nil, store.ErrGenerationStale
	}
	runtime.retiringBuilds[key] = true
	runtime.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			runtime.mu.Lock()
			delete(runtime.retiringBuilds, key)
			runtime.mu.Unlock()
		})
	}
	for {
		runtime.mu.Lock()
		active := runtime.activeBuilds[key]
		runtime.mu.Unlock()
		if active == 0 {
			return release, nil
		}
		retry := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !retry.Stop() {
				<-retry.C
			}
			release()
			return nil, ctx.Err()
		case <-retry.C:
		}
	}
}

func (runtime *Runtime) buildRetiring(repository, generation string) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.retiringBuilds[planKey(repository, generation)]
}

func (runtime *Runtime) buildPlan(
	ctx context.Context, repository, sourceDirectory string, source repositoryindex.SourceManifest,
) (*sourcepartition.Plan, string, func(), error) {
	planRoot := filepath.Join(runtime.DataDir, "observation-plans", repositoryHash(repository))
	if err := os.MkdirAll(planRoot, 0o700); err != nil {
		return nil, "", nil, err
	}
	temporary, err := os.MkdirTemp(planRoot, ".planning-")
	if err != nil {
		return nil, "", nil, err
	}
	cleanup := runtime.trackPlanningStage(temporary)
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	manifest, err := sourcepartition.Build(ctx, sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: temporary,
		Repository: repository, Source: source,
		Policy: planningPolicy(),
	})
	if err != nil {
		return nil, "", nil, err
	}
	plan, err := sourcepartition.Open(ctx, temporary, manifest)
	if err != nil {
		return nil, "", nil, err
	}
	keep = true
	return plan, temporary, cleanup, nil
}

func (runtime *Runtime) trackPlanningStage(path string) func() {
	runtime.planMutation.Lock()
	runtime.mu.Lock()
	if runtime.planningStages == nil {
		runtime.planningStages = make(map[string]bool)
	}
	runtime.planningStages[path] = true
	runtime.mu.Unlock()
	runtime.planMutation.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			runtime.planMutation.Lock()
			_ = os.RemoveAll(path)
			runtime.mu.Lock()
			delete(runtime.planningStages, path)
			runtime.mu.Unlock()
			runtime.planMutation.Unlock()
		})
	}
}

func (runtime *Runtime) openPlan(ctx context.Context, repository, generation string) (*sourcepartition.Plan, error) {
	key := planKey(repository, generation)
	runtime.mu.Lock()
	if plan := runtime.plans[key]; plan != nil {
		runtime.mu.Unlock()
		return plan, nil
	}
	runtime.mu.Unlock()
	directory := runtime.planDirectory(repository, generation)
	manifest, err := sourcepartition.ReadManifest(directory, repository)
	if err != nil {
		return nil, err
	}
	want, err := GenerationDigest(manifest)
	if err != nil || want != generation {
		return nil, invalid("runtime plan generation")
	}
	plan, err := sourcepartition.Open(ctx, directory, manifest)
	if err != nil {
		return nil, err
	}
	runtime.rememberPlan(repository, generation, plan)
	return plan, nil
}

func (runtime *Runtime) rememberPlan(repository, generation string, plan *sourcepartition.Plan) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.plans == nil {
		runtime.plans = make(map[string]*sourcepartition.Plan)
	}
	runtime.plans[planKey(repository, generation)] = plan
}

func (runtime *Runtime) dropPlan(repository, generation string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	delete(runtime.plans, planKey(repository, generation))
}

func (runtime *Runtime) planDirectory(repository, generation string) string {
	return filepath.Join(runtime.DataDir, "observation-plans", repositoryHash(repository), strings.TrimPrefix(generation, "sha256:"))
}

func planKey(repository, generation string) string { return repository + "\x00" + generation }

func resumedStage(root string, partition sourcepartition.Manifest, generation string) *Stage {
	return &Stage{
		root: root, repository: partition.Repository, generation: generation,
		partition: partition, directory: generationDirectory(root, partition.Repository, generation),
	}
}

func currentMatchesSource(root, repository, sourceDigest string) bool {
	pointer, err := readPointer(root, repository)
	if err != nil {
		return false
	}
	raw, err := readBoundedRegular(filepath.Join(repositoryDirectory(root, repository), pointer.ManifestName), MaxManifestBytes)
	if err != nil {
		return false
	}
	var manifest Manifest
	return decodeCanonical(raw, &manifest) == nil && validateManifest(manifest) == nil &&
		manifest.Digest == pointer.ManifestDigest && manifest.GenerationDigest == pointer.GenerationDigest &&
		manifest.SourceGenerationDigest == sourceDigest
}

func readMarker(root, repository string) (Marker, bool, error) {
	raw, err := readBoundedRegular(markerPath(root, repository), MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Marker{}, false, nil
	}
	if err != nil {
		return Marker{}, false, err
	}
	var marker Marker
	if decodeCanonical(raw, &marker) != nil || marker.Schema != MarkerSchema ||
		marker.Repository != repository || !validDigest(marker.GenerationDigest) {
		return Marker{}, false, invalid("runtime marker")
	}
	return marker, true, nil
}

func (runtime *Runtime) detachStage(root, repository, generation string) error {
	if !validDigest(generation) {
		return invalid("discarded stage generation")
	}
	if !runtime.buildRetiring(repository, generation) {
		return store.ErrGenerationStale
	}
	repositoryRoot := repositoryDirectory(root, repository)
	if pointer, err := readPointer(root, repository); err == nil &&
		pointer.GenerationDigest == generation {
		// A crash after pointer replacement but before marker cleanup leaves the
		// marker naming current authority. Clear only that exact marker; never
		// rename current bytes into the lifecycle collection namespace.
		return clearMarker(root, repository, generation)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source := generationDirectory(root, repository, generation)
	info, sourceErr := os.Lstat(source)
	sourcePresent := sourceErr == nil
	if sourcePresent && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return invalid("discarded stage directory")
	}
	if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
		return sourceErr
	}
	entries, err := boundedDirectory(repositoryRoot, MaxRepositoryGenerations+3)
	if err != nil {
		return err
	}
	collecting := 0
	usedNames := make(map[string]bool, len(entries))
	for _, entry := range entries {
		_, ok := collectingStageGeneration(entry.Name())
		if !ok {
			continue
		}
		collecting++
		usedNames[entry.Name()] = true
		if !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			return invalid("collecting stage entry")
		}
	}
	if !sourcePresent {
		// The marker names no surviving stage. Clearing that exact marker is the
		// only safe recovery; there are no bytes to hand to lifecycle.
		return clearMarker(root, repository, generation)
	}
	if collecting >= MaxRepositoryGenerations {
		return ErrLimit
	}
	destinationName := collectingStageName(generation, -1)
	if usedNames[destinationName] {
		destinationName = ""
		for ordinal := 0; ordinal < MaxRepositoryGenerations; ordinal++ {
			candidate := collectingStageName(generation, ordinal)
			if !usedNames[candidate] {
				destinationName = candidate
				break
			}
		}
		if destinationName == "" {
			return ErrLimit
		}
	}
	if err := os.Rename(source, filepath.Join(repositoryRoot, destinationName)); err != nil {
		return err
	}
	return clearMarker(root, repository, generation)
}

func clearMarker(root, repository, generation string) error {
	marker, present, err := readMarker(root, repository)
	if err != nil || !present {
		return err
	}
	if marker.GenerationDigest != generation {
		return ErrStale
	}
	if err := os.Remove(markerPath(root, repository)); err != nil {
		return err
	}
	return syncDirectory(repositoryDirectory(root, repository))
}

func workFailure(err error) error {
	if err == nil {
		return nil
	}
	cause := errors.Join(ErrWorkUnavailable, err)
	if errors.Is(err, ErrInvalid) || errors.Is(err, sourcepartition.ErrInvalid) {
		return pipelinerefusal.Classified(
			cause,
			pipelinerefusal.StageObservationPublication,
			pipelinerefusal.GenerationObservation,
			pipelinerefusal.ClassificationInvalid,
			pipelinerefusal.DimensionUnknown,
		)
	}
	return pipelinerefusal.At(
		cause,
		pipelinerefusal.StageObservationPublication,
		pipelinerefusal.GenerationObservation,
	)
}

func planningFailure(err error) error {
	if err == nil {
		return nil
	}
	cause := errors.Join(ErrWorkUnavailable, err)
	if errors.Is(err, ErrInvalid) || errors.Is(err, sourcepartition.ErrInvalid) ||
		errors.Is(err, repositoryindex.ErrInvalidGeneration) {
		return pipelinerefusal.Classified(
			cause,
			pipelinerefusal.StageSourcePartitionPlanning,
			pipelinerefusal.GenerationSourcePartition,
			pipelinerefusal.ClassificationInvalid,
			pipelinerefusal.DimensionUnknown,
		)
	}
	return pipelinerefusal.At(
		cause,
		pipelinerefusal.StageSourcePartitionPlanning,
		pipelinerefusal.GenerationSourcePartition,
	)
}
