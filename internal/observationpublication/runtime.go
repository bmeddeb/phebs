package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
	Admit   func(context.Context) error

	mu         sync.Mutex
	plans      map[string]*sourcepartition.Plan
	transition sync.Mutex
}

// Reconcile binds the current complete repository source generation to one
// durable observation schedule. Callers serialize it with repository index
// publication; the normal index completion hook already holds that lock.
func (runtime *Runtime) Reconcile(ctx context.Context, repository string) error {
	if runtime == nil || !filepath.IsAbs(runtime.DataDir) || runtime.Store == nil || validateRepository(repository) != nil {
		return invalid("runtime configuration")
	}
	root := filepath.Join(runtime.DataDir, "observations")
	sourceDirectory := filepath.Join(runtime.DataDir, "index")
	source, err := repositoryindex.ReadSourceManifest(sourceDirectory, repository)
	if err != nil {
		return workFailure(err)
	}
	if currentMatchesSource(root, repository, source.Digest) {
		return nil
	}
	if runtime.Admit != nil {
		if err := runtime.Admit(ctx); err != nil {
			return workFailure(err)
		}
	}
	plan, planDirectory, err := runtime.buildPlan(ctx, repository, sourceDirectory, source)
	if err != nil {
		return workFailure(err)
	}
	partition := plan.Manifest()
	if partition.BlobCount > MaxGenerationRecords {
		return workFailure(ErrLimit)
	}
	generation, err := GenerationDigest(partition)
	if err != nil {
		return workFailure(err)
	}
	runtime.transition.Lock()
	defer runtime.transition.Unlock()
	if publication, openErr := openGenerationDigest(ctx, root, repository, generation); openErr == nil {
		marker, present, err := readMarker(root, repository)
		if err != nil {
			return workFailure(err)
		}
		if present && marker.GenerationDigest != generation {
			if err := discardStage(root, repository, marker.GenerationDigest); err != nil {
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
		_ = os.RemoveAll(planDirectory)
		return nil
	}
	marker, markerPresent, err := readMarker(root, repository)
	if err != nil {
		return workFailure(err)
	}
	if markerPresent && marker.GenerationDigest != generation {
		if err := discardStage(root, repository, marker.GenerationDigest); err != nil {
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
	runtime.rememberPlan(repository, generation, plan)
	if len(partition.Members) == 0 {
		stage := resumedStage(root, partition, generation)
		if _, err := stage.Finalize(ctx); err != nil {
			return workFailure(err)
		}
		runtime.dropPlan(repository, generation)
		_ = os.RemoveAll(planDirectory)
		return nil
	}
	_, err = runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: repository, Stage: ScheduleStage, Generation: generation,
		ResourceClass: store.GenerationResourceCPU, TotalItems: int64(len(partition.Members)),
		ChunkItems: ScheduleChunkItems, MaxAttempts: ScheduleMaxAttempts,
		RepositoryTokens: ScheduleRepositoryTokens,
	})
	return workFailure(err)
}

// Handle executes one durable scheduler chunk. Publication is attempted only
// after the final member exists. A slower earlier chunk also observes that
// sentinel and can finalize, so correctness does not depend on completion order.
func (runtime *Runtime) Handle(ctx context.Context, chunk store.GenerationChunk) error {
	if runtime == nil || chunk.Stage != ScheduleStage || chunk.Generation == "" ||
		chunk.Offset < 0 || chunk.Length < 1 {
		return workFailure(invalid("runtime chunk"))
	}
	root := filepath.Join(runtime.DataDir, "observations")
	if publication, err := openGenerationDigest(ctx, root, chunk.Repository, chunk.Generation); err == nil {
		runtime.transition.Lock()
		defer runtime.transition.Unlock()
		if err := runtime.fenceChunk(ctx, chunk, publication.manifest.SourceGenerationDigest); err != nil {
			return workFailure(err)
		}
		if err := activate(root, publication.manifest); err != nil {
			return workFailure(err)
		}
		if err := clearMarker(root, chunk.Repository, chunk.Generation); err != nil {
			return workFailure(err)
		}
		return nil
	}
	plan, err := runtime.openPlan(ctx, chunk.Repository, chunk.Generation)
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
	stage := resumedStage(root, partition, chunk.Generation)
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
	if publication, err := openGenerationDigest(ctx, root, chunk.Repository, chunk.Generation); err == nil {
		if err := activate(root, publication.manifest); err != nil {
			return workFailure(err)
		}
	} else {
		if _, err := stage.Finalize(ctx); err != nil {
			return workFailure(err)
		}
	}
	runtime.dropPlan(chunk.Repository, chunk.Generation)
	_ = os.RemoveAll(runtime.planDirectory(chunk.Repository, chunk.Generation))
	return nil
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

func (runtime *Runtime) buildPlan(
	ctx context.Context, repository, sourceDirectory string, source repositoryindex.SourceManifest,
) (*sourcepartition.Plan, string, error) {
	planRoot := filepath.Join(runtime.DataDir, "observation-plans", repositoryHash(repository))
	if err := os.MkdirAll(planRoot, 0o700); err != nil {
		return nil, "", err
	}
	temporary, err := os.MkdirTemp(planRoot, ".planning-")
	if err != nil {
		return nil, "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	manifest, err := sourcepartition.Build(ctx, sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: temporary,
		Repository: repository, Source: source,
		Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		return nil, "", err
	}
	generation, err := GenerationDigest(manifest)
	if err != nil {
		return nil, "", err
	}
	destination := runtime.planDirectory(repository, generation)
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporary, destination); err != nil {
			return nil, "", err
		}
		keep = true
	} else if err != nil {
		return nil, "", err
	}
	plan, err := sourcepartition.Open(ctx, destination, manifest)
	if err != nil {
		return nil, "", err
	}
	return plan, destination, nil
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

func discardStage(root, repository, generation string) error {
	if err := os.RemoveAll(generationDirectory(root, repository, generation)); err != nil {
		return err
	}
	if err := os.Remove(markerPath(root, repository)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(repositoryDirectory(root, repository))
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

type workBoundary struct{ cause error }

func (workBoundary) Error() string { return ErrWorkUnavailable.Error() }
func (failure workBoundary) Unwrap() error {
	return errors.Join(ErrWorkUnavailable, failure.cause)
}

func workFailure(err error) error {
	if err == nil {
		return nil
	}
	return workBoundary{cause: err}
}
