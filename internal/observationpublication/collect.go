package observationpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

// CollectScanLimit bounds the directory names one collection pass examines,
// and callers bound removals separately. Repeated bounded passes converge on
// a namespace that leaked before collection existed.
const CollectScanLimit = 512

// CollectRemovalLimit bounds removals per owned planning run.
const CollectRemovalLimit = 8

type CollectResult struct {
	Plans    int
	Stages   int
	Bindings int
	Aborted  bool
}

// collectSnapshot captures every authority the collector must not race:
// both durable schedules, the current source, and the publication controls.
// Any change between enumeration and removal aborts the pass.
type collectSnapshot struct {
	planningPresent     bool
	planningGeneration  string
	planningDigest      string
	planningStatus      store.GenerationScheduleStatus
	inventoryPresent    bool
	inventoryGeneration string
	inventoryDigest     string
	inventoryStatus     store.GenerationScheduleStatus
	executionPresent    bool
	executionGeneration string
	executionDigest     string
	executionStatus     store.GenerationScheduleStatus
	sourcePresent       bool
	sourceDigest        string
	markerPresent       bool
	markerGeneration    string
	pointerPresent      bool
	pointerGeneration   string
}

type collectProtection struct {
	plans            map[string]bool
	scheduleBindings map[string]bool
	planningBindings map[string]bool
}

type collectDirectory struct {
	name    string
	retired bool
}

// CollectSupersededPlans removes plan directories and schedule/planning
// bindings that no current authority names: not the active or settled current
// schedules, not a recovery identity a concurrent enqueue may be minting, not
// the current publication pointer or marker, and not an in-process build. It
// renews the exact planning lease inside the destructive namespace fence and
// re-reads every authority there, aborting on any change.
func (runtime *Runtime) CollectSupersededPlans(
	ctx context.Context, chunk store.GenerationChunk, limit int,
) (CollectResult, error) {
	repository := chunk.Repository
	if runtime == nil || !filepath.IsAbs(runtime.DataDir) || runtime.Store == nil ||
		validateRepository(repository) != nil || limit < 1 ||
		(chunk.Stage != PlanningScheduleStage && chunk.Stage != InventoryScheduleStageV2) ||
		chunk.Offset != 0 || chunk.Length != 1 ||
		!validDigest(chunk.Generation) || !validDigest(chunk.ScheduleDigest) {
		return CollectResult{}, invalid("collector configuration")
	}
	snapshot, protection, err := runtime.collectProtection(ctx, repository)
	if err != nil {
		return CollectResult{}, err
	}
	directory := filepath.Join(
		runtime.DataDir, "observation-plans", repositoryHash(repository),
	)
	names, err := boundedNames(directory, CollectScanLimit)
	if errors.Is(err, os.ErrNotExist) {
		return CollectResult{}, nil
	}
	if err != nil {
		return CollectResult{}, err
	}
	var plans, stages []collectDirectory
	var bindings []string
	for _, name := range names {
		switch {
		case validLowerHex(name) && len(name) == 64:
			if !protection.plans[name] {
				plans = append(plans, collectDirectory{name: name})
			}
		case strings.HasPrefix(name, "schedule-") && strings.HasSuffix(name, ".json"):
			generation := strings.TrimSuffix(strings.TrimPrefix(name, "schedule-"), ".json")
			if len(generation) != 64 || !validLowerHex(generation) {
				return CollectResult{}, invalid("planning namespace contains a malformed schedule binding")
			}
			if !protection.scheduleBindings[generation] {
				bindings = append(bindings, name)
			}
		case strings.HasPrefix(name, "planning-") && strings.HasSuffix(name, ".json"):
			generation := strings.TrimSuffix(strings.TrimPrefix(name, "planning-"), ".json")
			if len(generation) != 64 || !validLowerHex(generation) {
				return CollectResult{}, invalid("planning namespace contains a malformed planning binding")
			}
			if !protection.planningBindings[generation] {
				bindings = append(bindings, name)
			}
		case strings.HasPrefix(name, ".planning-"):
			stages = append(stages, collectDirectory{name: name})
		case validCollectingName(name, ".collecting-plan-"):
			plans = append(plans, collectDirectory{name: name, retired: true})
		case validCollectingName(name, ".collecting-stage-"):
			stages = append(stages, collectDirectory{name: name, retired: true})
		default:
			return CollectResult{}, invalid("planning namespace contains an unknown artifact")
		}
	}
	if len(plans)+len(stages)+len(bindings) == 0 {
		return CollectResult{}, nil
	}
	// No binding publication or in-process build/stage admission can cross this
	// point. Renewing the exact lease and confirming authority inside the same
	// short fence makes the subsequent removals one linearized mutation.
	runtime.planMutation.Lock()
	result, drains, mutationErr := func() (CollectResult, []string, error) {
		if err := runtime.Store.HeartbeatGenerationChunk(ctx, chunk); err != nil {
			return CollectResult{}, nil, err
		}
		confirmed, _, err := runtime.collectProtection(ctx, repository)
		if err != nil {
			return CollectResult{}, nil, err
		}
		if confirmed != snapshot {
			return CollectResult{Aborted: true}, nil, nil
		}
		result := CollectResult{}
		var drains []string
		for _, candidate := range plans {
			if result.Plans+result.Stages+result.Bindings >= limit {
				break
			}
			if !candidate.retired && runtime.buildInFlight(repository, "sha256:"+candidate.name) {
				continue
			}
			path, present, err := retirePlanningDirectory(directory, "plan", candidate)
			if err != nil {
				return result, drains, err
			}
			if present {
				drains = append(drains, path)
				result.Plans++
			}
		}
		for _, candidate := range stages {
			if result.Plans+result.Stages+result.Bindings >= limit {
				break
			}
			path := filepath.Join(directory, candidate.name)
			if !candidate.retired && runtime.planningStageInFlight(path) {
				continue
			}
			path, present, err := retirePlanningDirectory(directory, "stage", candidate)
			if err != nil {
				return result, drains, err
			}
			if present {
				drains = append(drains, path)
				result.Stages++
			}
		}
		for _, name := range bindings {
			if result.Plans+result.Stages+result.Bindings >= limit {
				break
			}
			if err := os.Remove(filepath.Join(directory, name)); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return result, drains, err
			}
			result.Bindings++
		}
		return result, drains, syncDirectory(directory)
	}()
	runtime.planMutation.Unlock()
	if mutationErr != nil || result.Aborted {
		return result, mutationErr
	}
	// Directory retirement above is one same-parent rename under the short
	// fence. Potentially large recursive deletion happens after admission and
	// binding publication have resumed; a crash leaves a closed collecting name
	// that the next bounded pass drains.
	for _, path := range drains {
		if err := os.RemoveAll(path); err != nil {
			return result, err
		}
	}
	return result, syncDirectory(directory)
}

func validCollectingName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := strings.TrimPrefix(name, prefix)
	digestPart, suffix, found := strings.Cut(rest, "-")
	if len(digestPart) != 64 || !validLowerHex(digestPart) {
		return false
	}
	if !found {
		return true
	}
	if suffix == "" || len(suffix) > 2 {
		return false
	}
	for _, value := range suffix {
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}

func retirePlanningDirectory(
	directory, kind string, candidate collectDirectory,
) (string, bool, error) {
	path := filepath.Join(directory, candidate.name)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, invalid("planning directory is missing or special")
	}
	if candidate.retired {
		return path, true, nil
	}
	identity := strings.TrimPrefix(digest("phebs-planning-collection-v1", kind+"\x00"+candidate.name), "sha256:")
	for ordinal := 0; ordinal <= 64; ordinal++ {
		name := ".collecting-" + kind + "-" + identity
		if ordinal > 0 {
			name += fmt.Sprintf("-%d", ordinal)
		}
		destination := filepath.Join(directory, name)
		if _, statErr := os.Lstat(destination); statErr == nil {
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", false, statErr
		}
		if err := os.Rename(path, destination); err != nil {
			return "", false, err
		}
		return destination, true, nil
	}
	return "", false, invalid("planning collection incarnations exceed their bound")
}

func (runtime *Runtime) planningStageInFlight(path string) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.planningStages[path]
}

func (runtime *Runtime) collectProtection(
	ctx context.Context, repository string,
) (collectSnapshot, collectProtection, error) {
	snapshot := collectSnapshot{}
	protection := collectProtection{
		plans:            map[string]bool{},
		scheduleBindings: map[string]bool{},
		planningBindings: map[string]bool{},
	}
	protect := func(set map[string]bool, generation string) {
		if validDigest(generation) {
			set[strings.TrimPrefix(generation, "sha256:")] = true
		}
	}
	planning, err := runtime.Store.GetGenerationSchedule(ctx, repository, PlanningScheduleStage)
	if err == nil {
		snapshot.planningPresent = true
		snapshot.planningGeneration = planning.Generation
		snapshot.planningDigest = planning.Digest
		snapshot.planningStatus = planning.Status
		protect(protection.planningBindings, planning.Generation)
	} else if !errors.Is(err, store.ErrNotFound) {
		return snapshot, protection, err
	}
	inventory, err := runtime.Store.GetGenerationSchedule(ctx, repository, InventoryScheduleStageV2)
	if err == nil {
		snapshot.inventoryPresent = true
		snapshot.inventoryGeneration = inventory.Generation
		snapshot.inventoryDigest = inventory.Digest
		snapshot.inventoryStatus = inventory.Status
		protect(protection.planningBindings, inventory.Generation)
	} else if !errors.Is(err, store.ErrNotFound) {
		return snapshot, protection, err
	}
	execution, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if err == nil {
		snapshot.executionPresent = true
		snapshot.executionGeneration = execution.Generation
		snapshot.executionDigest = execution.Digest
		snapshot.executionStatus = execution.Status
		protect(protection.scheduleBindings, execution.Generation)
		protect(protection.plans, execution.Generation)
		if binding, bindingErr := runtime.readScheduleBinding(
			repository, execution.Generation,
		); bindingErr == nil {
			protect(protection.plans, binding.PublicationGeneration)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return snapshot, protection, err
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(runtime.DataDir, "index"), repository,
	)
	if err == nil {
		snapshot.sourcePresent = true
		snapshot.sourceDigest = source.Digest
		if target, _, targetErr := planningTarget(repository, source.Digest); targetErr == nil {
			protect(protection.planningBindings, target)
			if snapshot.planningPresent {
				protect(protection.planningBindings, recoveryPlanningGeneration(
					target, snapshot.planningDigest,
				))
			}
			if snapshot.inventoryPresent {
				protect(protection.planningBindings, recoveryPlanningGeneration(
					target, snapshot.inventoryDigest,
				))
			}
		}
	}
	root := filepath.Join(runtime.DataDir, "observations")
	marker, markerPresent, err := readMarker(root, repository)
	if err != nil {
		return snapshot, protection, err
	}
	if markerPresent {
		snapshot.markerPresent = true
		snapshot.markerGeneration = marker.GenerationDigest
		protect(protection.plans, marker.GenerationDigest)
		protect(protection.scheduleBindings, marker.GenerationDigest)
		if snapshot.executionPresent {
			protect(protection.scheduleBindings, recoveryScheduleGeneration(
				marker.GenerationDigest, snapshot.executionDigest,
			))
		}
	}
	pointer, err := readPointer(root, repository)
	if err == nil {
		snapshot.pointerPresent = true
		snapshot.pointerGeneration = pointer.GenerationDigest
		protect(protection.plans, pointer.GenerationDigest)
		protect(protection.scheduleBindings, pointer.GenerationDigest)
		if snapshot.executionPresent {
			protect(protection.scheduleBindings, recoveryScheduleGeneration(
				pointer.GenerationDigest, snapshot.executionDigest,
			))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return snapshot, protection, err
	}
	return snapshot, protection, nil
}

// buildInFlight reports whether this process currently plans, builds, or
// retires the exact generation, in which case its directory stays.
func (runtime *Runtime) buildInFlight(repository, generation string) bool {
	key := planKey(repository, generation)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.plans[key] != nil || runtime.activeBuilds[key] > 0 ||
		runtime.retiringBuilds[key]
}
