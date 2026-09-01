package relationshippublication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const RetainedGenerationsV3 = 2

// LifecyclePinStoreV3 removes the exact durable references held by one v3
// relationship generation. Each operation must be idempotent because the
// filesystem confirmation is deliberately a later durable step.
type LifecyclePinStoreV3 interface {
	UnpinRelationshipPublicationV3(
		context.Context,
		string, string, string, string,
		uint64, uint64,
		string,
	) error
	UnpinPartitionedExtractionRun(context.Context, string, string) error
}

type lifecycleControlsV3 struct {
	repository        string
	currentGeneration string
	markerGeneration  string
}

// SweepLifecycleV3 advances at most one repository in the dark v3 shadow
// namespace. It never selects v3 for reads. A retired generation is renamed
// out of authority first, then returned with its exact root so the caller can
// remove catalog and extraction references before confirming bounded drain.
func SweepLifecycleV3(
	ctx context.Context,
	dataDir string,
	now time.Time,
	cursor string,
	pins PinChecker,
	deleteLimit int,
) (LifecycleResult, error) {
	var result LifecycleResult
	if !filepath.IsAbs(dataDir) || pins == nil || deleteLimit < 1 {
		return result, invalidLifecycle("v3 lifecycle input")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root := filepath.Join(dataDir, "relationships")
	base := ShadowBase(root)
	entries, err := boundedLifecycleDirectory(base, MaxLifecycleRepositories)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	repositories := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
			return result, invalidLifecycle("v3 lifecycle repository inventory")
		}
		repositories = append(repositories, entry.Name())
	}
	sort.Strings(repositories)
	if len(repositories) == 0 {
		return result, nil
	}
	index := sort.SearchStrings(repositories, cursor)
	if index < len(repositories) && repositories[index] == cursor {
		index++
	}
	if index == len(repositories) {
		index = 0
	}
	result.Cursor = repositories[index]
	result.More = len(repositories) > 1
	repositoryDirectory := filepath.Join(base, result.Cursor)
	marker, markerPresent, markerErr := readLifecycleMarkerV3(
		repositoryDirectory, result.Cursor,
	)
	if markerErr != nil {
		return result, markerErr
	}
	if markerPresent {
		if _, err := validateLifecycleMarkerRootV3(repositoryDirectory, marker); err != nil {
			return result, err
		}
		// A final marker owns either the installed generation or its exact named
		// stage. Recovery must finish that commit before lifecycle can mutate any
		// stage or shared dependency in this repository.
		result.Scanned = 1
		result.More = true
		return result, nil
	}
	temporary := filepath.Join(repositoryDirectory, "publishing.json.tmp")
	if _, err := os.Lstat(temporary); err == nil {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := removePublishingTemporaryV3(repositoryDirectory); err != nil {
			return result, err
		}
		result.Scanned = 1
		result.Deleted = 1
		result.More = true
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	generations, collecting, stages, err := lifecycleGenerationsV3(repositoryDirectory)
	if err != nil {
		return result, err
	}
	if len(stages) > 0 {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		sort.Slice(stages, func(i, j int) bool {
			if !stages[i].modified.Equal(stages[j].modified) {
				return stages[i].modified.Before(stages[j].modified)
			}
			return stages[i].name < stages[j].name
		})
		result.Scanned = 1
		deleted, complete, drainErr := drainFlatGenerationBounded(
			filepath.Join(repositoryDirectory, stages[0].name), deleteLimit, MaxStageRepairFiles,
		)
		result.Deleted = deleted
		result.More = result.More || !complete || len(stages) > 1
		if drainErr == nil && complete {
			removed, removeErr := removeEmptyLifecycleDirectory(repositoryDirectory)
			if removeErr != nil {
				return result, removeErr
			}
			if removed {
				result.Deleted++
			}
		}
		return result, drainErr
	}
	controls, err := readLifecycleControlsV3(repositoryDirectory, result.Cursor)
	if err != nil {
		return result, err
	}
	if len(collecting) == 1 {
		result.Scanned = 1
		if pins.Pinned(controls.repository, collecting[0].generation) {
			return result, nil
		}
		collectingDirectory := filepath.Join(repositoryDirectory, collecting[0].name)
		unpinned, unpinnedErr := collectionUnpinned(collectingDirectory)
		if unpinnedErr != nil {
			return result, unpinnedErr
		}
		if !unpinned {
			rootValue, rootErr := validateCollectingGenerationV3(
				ctx, collectingDirectory, controls.repository, collecting[0].generation,
			)
			if rootErr != nil {
				return result, rootErr
			}
			result.ReleasedPinOwner = "relationship:" + collecting[0].generation
			result.ReleasedRootV3 = &rootValue
			result.More = true
			return result, nil
		}
		deleted, complete, drainErr := drainUnpinnedCollectionBounded(
			collectingDirectory, deleteLimit, MaxGenerationFilesV3+1,
		)
		result.Deleted = deleted
		result.More = result.More || !complete
		return result, drainErr
	}
	sort.Slice(generations, func(i, j int) bool {
		if !generations[i].modified.Equal(generations[j].modified) {
			return generations[i].modified.After(generations[j].modified)
		}
		return generations[i].name > generations[j].name
	})
	protected := make(map[string]struct{}, RetainedGenerationsV3-1)
	remainingProtection := RetainedGenerationsV3 - 1
	for _, generation := range generations {
		if generation.generation == controls.currentGeneration ||
			generation.generation == controls.markerGeneration {
			continue
		}
		if remainingProtection == 0 {
			break
		}
		protected[generation.generation] = struct{}{}
		remainingProtection--
	}
	candidates := make([]lifecycleGeneration, 0, min(len(generations), MaxRepositoryGenerations))
	for index := len(generations) - 1; index >= 0; index-- {
		generation := generations[index]
		if generation.generation == controls.currentGeneration ||
			generation.generation == controls.markerGeneration {
			continue
		}
		if _, retained := protected[generation.generation]; retained {
			continue
		}
		if len(candidates) == MaxRepositoryGenerations {
			result.More = true
			break
		}
		candidates = append(candidates, generation)
	}
	for _, generation := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Scanned++
		if now.Sub(generation.modified) < GenerationMaxAge &&
			len(generations) <= RetainedGenerationsV3 {
			continue
		}
		releaseRetire, admitted := pins.BeginRetire(controls.repository, generation.generation)
		if !admitted {
			continue
		}
		currentControls, controlErr := readLifecycleControlsV3(repositoryDirectory, result.Cursor)
		if controlErr != nil {
			releaseRetire()
			return result, controlErr
		}
		if currentControls.repository != controls.repository ||
			generation.generation == currentControls.currentGeneration ||
			generation.generation == currentControls.markerGeneration {
			releaseRetire()
			continue
		}
		rootValue, validateErr := validateGenerationForRetireV3(
			ctx, root, controls.repository, generation.generation,
		)
		if validateErr != nil {
			releaseRetire()
			return result, validateErr
		}
		name := "collecting-" + generation.name
		if err := os.Rename(
			filepath.Join(repositoryDirectory, generation.name),
			filepath.Join(repositoryDirectory, name),
		); err != nil {
			releaseRetire()
			return result, err
		}
		if err := syncDirectory(repositoryDirectory); err != nil {
			releaseRetire()
			return result, err
		}
		releaseRetire()
		result.ReleasedPinOwner = "relationship:" + generation.generation
		result.ReleasedRootV3 = &rootValue
		result.More = true
		return result, nil
	}

	// The legacy owner remains the single shared-component collector whenever
	// that repository exists in its namespace. This avoids duplicate steady-
	// state scans while still collecting components for v3-only repositories.
	legacyPresent, err := legacyRelationshipRepositoryPresent(dataDir, result.Cursor)
	if err != nil {
		return result, err
	}
	if legacyPresent {
		return result, nil
	}
	if len(generations) > MaxRepositoryGenerations {
		result.More = true
		return result, nil
	}
	references, err := componentReferencesV3(
		repositoryDirectory, controls.repository, result.Cursor, generations,
	)
	if err != nil {
		return result, err
	}
	deleted, more, scanned, err := sweepOrphanComponent(
		dataDir, result.Cursor, references, deleteLimit,
	)
	result.Deleted += deleted
	result.Scanned += scanned
	result.More = result.More || more
	return result, err
}

// UnpinLifecycleV3 removes each exact extraction-run owner before releasing
// the catalog reference. A failed attempt leaves the collecting directory
// unconfirmed, so every operation is retried idempotently on the next turn.
func UnpinLifecycleV3(
	ctx context.Context,
	pins LifecyclePinStoreV3,
	root RootV3,
) error {
	if pins == nil || ValidateRootV3(root) != nil {
		return invalidLifecycle("v3 lifecycle unpin root")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	owner := "relationship:" + root.GenerationDigest
	for _, domain := range root.Authority.Upstream.Domains {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := pins.UnpinPartitionedExtractionRun(ctx, domain.RunID, owner); err != nil {
			return err
		}
	}
	return pins.UnpinRelationshipPublicationV3(
		ctx, root.Authority.Repository, root.GenerationDigest, root.Digest,
		root.Authority.CatalogRootDigest, root.Authority.CatalogControlRevision,
		root.Authority.ServiceStateControlRevision,
		root.Authority.ServiceStateSummaryDigest,
	)
}

// ConfirmLifecycleUnpinV3 durably records that all exact store owners for the
// collecting v3 generation were removed.
func ConfirmLifecycleUnpinV3(
	ctx context.Context,
	dataDir, repositoryHashValue, owner string,
) error {
	return confirmLifecycleUnpinAt(
		ctx, dataDir, RelationshipPublicationsV3Shadow, repositoryHashValue, owner,
	)
}

func readLifecycleControlsV3(
	repositoryDirectory, repositoryHashValue string,
) (lifecycleControlsV3, error) {
	var result lifecycleControlsV3
	if raw, err := readRegular(filepath.Join(repositoryDirectory, "current.json"), MaxRootBytesV3); err == nil {
		var pointer PointerV3
		canonical := []byte(nil)
		if decodeExact(raw, MaxRootBytesV3, &pointer) == nil {
			canonical, _ = json.Marshal(pointer)
		}
		if validatePointerV3(pointer) != nil || !bytes.Equal(raw, canonical) ||
			repositoryHash(pointer.Repository) != repositoryHashValue {
			return result, invalidLifecycle("v3 lifecycle pointer")
		}
		if err := validateLifecycleControlRootV3(repositoryDirectory, pointer); err != nil {
			return result, err
		}
		result.repository = pointer.Repository
		result.currentGeneration = pointer.GenerationDigest
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	marker, markerPresent, err := readLifecycleMarkerV3(
		repositoryDirectory, repositoryHashValue,
	)
	if err != nil {
		return result, err
	}
	if markerPresent {
		if result.repository != "" && result.repository != marker.Pointer.Repository {
			return result, invalidLifecycle("v3 lifecycle marker")
		}
		_, validateErr := validateLifecycleMarkerRootV3(
			repositoryDirectory, marker,
		)
		if validateErr != nil {
			return result, validateErr
		}
		result.repository = marker.Pointer.Repository
		result.markerGeneration = marker.Pointer.GenerationDigest
	}
	if result.repository == "" {
		return result, invalidLifecycle("v3 lifecycle has no current or publishing authority")
	}
	return result, nil
}

func readLifecycleMarkerV3(
	repositoryDirectory, repositoryHashValue string,
) (MarkerV3, bool, error) {
	raw, err := readRegular(filepath.Join(repositoryDirectory, "publishing.json"), MaxRootBytesV3)
	if errors.Is(err, os.ErrNotExist) {
		return MarkerV3{}, false, nil
	}
	if err != nil {
		return MarkerV3{}, false, err
	}
	var marker MarkerV3
	canonical := []byte(nil)
	if decodeExact(raw, MaxRootBytesV3, &marker) == nil {
		canonical, _ = json.Marshal(marker)
	}
	if validateMarkerV3(marker) != nil || !bytes.Equal(raw, canonical) ||
		repositoryHash(marker.Pointer.Repository) != repositoryHashValue {
		return MarkerV3{}, false, invalidLifecycle("v3 lifecycle marker")
	}
	return marker, true, nil
}

func validateLifecycleMarkerRootV3(
	repositoryDirectory string,
	marker MarkerV3,
) (bool, error) {
	err := validateLifecycleControlRootV3(repositoryDirectory, marker.Pointer)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) || marker.StageName == "" {
		return false, err
	}
	value, err := readLifecycleRootV3(
		filepath.Join(repositoryDirectory, marker.StageName), marker.Pointer.Repository,
		marker.Pointer.GenerationDigest,
	)
	if err != nil {
		return false, err
	}
	if value.Digest != marker.Pointer.RootDigest {
		return false, invalidLifecycle("v3 lifecycle marker stage root")
	}
	return true, nil
}

func validateLifecycleControlRootV3(
	repositoryDirectory string,
	pointer PointerV3,
) error {
	value, err := readLifecycleRootV3(
		filepath.Join(
			repositoryDirectory, strings.TrimPrefix(pointer.GenerationDigest, "sha256:"),
		),
		pointer.Repository, pointer.GenerationDigest,
	)
	if err != nil {
		return err
	}
	if value.Digest != pointer.RootDigest {
		return invalidLifecycle("v3 lifecycle control root")
	}
	return nil
}

func lifecycleGenerationsV3(
	directory string,
) ([]lifecycleGeneration, []lifecycleGeneration, []lifecycleGeneration, error) {
	entries, err := boundedLifecycleDirectory(directory, MaxRepositoryRepairEntries)
	if err != nil {
		return nil, nil, nil, err
	}
	var generations, collecting, stages []lifecycleGeneration
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			if name == "current.json" || name == "publishing.json" {
				continue
			}
			return nil, nil, nil, invalidLifecycle("v3 lifecycle repository entry")
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, nil, nil, infoErr
		}
		switch {
		case strings.HasPrefix(name, ".stage-"):
			stages = append(stages, lifecycleGeneration{name: name, modified: info.ModTime()})
		case strings.HasPrefix(name, "collecting-"):
			raw := strings.TrimPrefix(name, "collecting-")
			if len(raw) != 64 || !validLowerHex(raw) {
				return nil, nil, nil, invalidLifecycle("v3 collecting generation")
			}
			collecting = append(collecting, lifecycleGeneration{
				name: name, generation: "sha256:" + raw, modified: info.ModTime(),
			})
		case len(name) == 64 && validLowerHex(name):
			generations = append(generations, lifecycleGeneration{
				name: name, generation: "sha256:" + name, modified: info.ModTime(),
			})
		default:
			return nil, nil, nil, invalidLifecycle("v3 lifecycle generation")
		}
	}
	if len(collecting) > 1 {
		return nil, nil, nil, ErrLimit
	}
	return generations, collecting, stages, nil
}

func validateGenerationForRetireV3(
	ctx context.Context,
	root, repository, generation string,
) (RootV3, error) {
	directory, err := GenerationPathV3(root, repository, generation)
	if err != nil {
		return RootV3{}, err
	}
	value, err := readLifecycleRootV3(directory, repository, generation)
	if err != nil {
		return RootV3{}, err
	}
	publication, err := ValidateGenerationV3(ctx, root, repository, generation, value.Digest)
	if err != nil {
		return RootV3{}, err
	}
	return publication.Root(), nil
}

func validateCollectingGenerationV3(
	ctx context.Context,
	directory, repository, generation string,
) (RootV3, error) {
	value, err := readLifecycleRootV3(directory, repository, generation)
	if err != nil {
		return RootV3{}, err
	}
	publication, err := openDirectoryCompleteV3(ctx, directory, value)
	if err != nil {
		return RootV3{}, err
	}
	return publication.Root(), nil
}

func readLifecycleRootV3(
	directory, repository, generation string,
) (RootV3, error) {
	var value RootV3
	raw, err := readRegular(filepath.Join(directory, "root.json"), MaxRootBytesV3)
	if err != nil {
		return value, err
	}
	canonical := []byte(nil)
	if decodeExact(raw, MaxRootBytesV3, &value) == nil {
		canonical, _ = json.Marshal(value)
	}
	if ValidateRootV3(value) != nil || !bytes.Equal(raw, canonical) ||
		(repository != "" && value.Authority.Repository != repository) ||
		value.GenerationDigest != generation {
		return RootV3{}, invalidLifecycle("v3 lifecycle relationship root")
	}
	return value, nil
}

func componentReferencesV3(
	repositoryDirectory string,
	repository string,
	repositoryHashValue string,
	generations []lifecycleGeneration,
) (componentReferenceSet, error) {
	result := newComponentReferenceSet()
	for _, generation := range generations {
		value, err := readLifecycleRootV3(
			filepath.Join(repositoryDirectory, generation.name),
			repository, generation.generation,
		)
		if err != nil || repositoryHash(value.Authority.Repository) != repositoryHashValue {
			return result, errors.Join(err, invalidLifecycle("v3 component reference root"))
		}
		addComponentAuthorityV3(&result, value.Authority)
	}
	return result, nil
}

func addComponentReferencesV3(
	references *componentReferenceSet,
	repositoryDirectory, repositoryHashValue string,
) (bool, error) {
	_, err := os.Lstat(repositoryDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	marker, markerPresent, err := readLifecycleMarkerV3(
		repositoryDirectory, repositoryHashValue,
	)
	if err != nil {
		return false, err
	}
	if markerPresent {
		if _, err := validateLifecycleMarkerRootV3(repositoryDirectory, marker); err != nil {
			return false, err
		}
		// The staged root can be the only authority retaining shared component
		// generations. The legacy owner must defer its entire union/deletion turn
		// until v3 recovery installs that marker-owned generation.
		return true, nil
	}
	generations, _, _, err := lifecycleGenerationsV3(repositoryDirectory)
	if err != nil {
		return false, err
	}
	if len(generations) > MaxRepositoryGenerations {
		return true, nil
	}
	// A collecting generation is intentionally absent from this retained-root
	// union. BeginRetire first proves no CacheV3 lease, and the durable rename
	// removes the explicit generation path from read authority before store
	// unpin or shared-component collection can proceed.
	for _, generation := range generations {
		value, readErr := readLifecycleRootV3(
			filepath.Join(repositoryDirectory, generation.name), "", generation.generation,
		)
		if readErr != nil || repositoryHash(value.Authority.Repository) != repositoryHashValue {
			return false, errors.Join(readErr, invalidLifecycle("v3 component reference root"))
		}
		addComponentAuthorityV3(references, value.Authority)
	}
	return false, nil
}

func addComponentAuthorityV3(references *componentReferenceSet, authority AuthorityV3) {
	references.resolver[authority.ResolverGenerationDigest] = struct{}{}
	references.rpc[authority.RPCGenerationDigest] = struct{}{}
	references.kafka[authority.KafkaGenerationDigest] = struct{}{}
}

func legacyRelationshipRepositoryPresent(
	dataDir, repositoryHashValue string,
) (bool, error) {
	directory := filepath.Join(
		dataDir, "relationships", "relationship-publications", repositoryHashValue,
	)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, invalidLifecycle("legacy relationship repository")
	}
	return true, nil
}

func drainFlatGenerationBounded(
	directory string,
	budget, inventoryLimit int,
) (int, bool, error) {
	entries, err := boundedLifecycleDirectory(directory, inventoryLimit)
	if err != nil {
		return 0, false, err
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return deleted, false, invalidLifecycle("v3 lifecycle generation entry")
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return deleted, false, errors.Join(
				infoErr, invalidLifecycle("v3 lifecycle generation file"),
			)
		}
		if deleted == budget {
			return deleted, false, nil
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return deleted, false, err
		}
		deleted++
		if deleted == budget {
			return deleted, false, nil
		}
	}
	if err := os.Remove(directory); err != nil {
		return deleted, false, err
	}
	return deleted + 1, true, nil
}
