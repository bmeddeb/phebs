package observationpublication

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MaxLifecycleRepositories = 4_096
	MaxRepositoryGenerations = 64
	RetainedGenerations      = 2
	GenerationMaxAge         = 14 * 24 * time.Hour
)

type LifecycleResult struct {
	Cursor  string
	Scanned int
	Deleted int
	More    bool
}

type PinChecker interface {
	Pinned(repository, generation string) bool
}

// SweepLifecycle examines one repository and removes entries from at most one
// unrooted generation. The caller must hold the shared backup/publication
// mutation lock. A generation is first renamed out of the publication namespace
// and is then drained with at most deleteLimit filesystem removals per call.
func SweepLifecycle(
	ctx context.Context, root string, now time.Time, cursor string,
	pins PinChecker, deleteLimit int,
) (LifecycleResult, error) {
	var result LifecycleResult
	if !filepath.IsAbs(root) || pins == nil || deleteLimit < 1 {
		return result, invalid("lifecycle input")
	}
	entries, err := boundedDirectory(root, MaxLifecycleRepositories)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var repositories []string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 64 {
			repositories = append(repositories, entry.Name())
		}
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
	repositoryRoot := filepath.Join(root, repositories[index])
	pointerRaw, err := readBoundedRegular(filepath.Join(repositoryRoot, "current.json"), MaxManifestBytes)
	if err != nil {
		return result, err
	}
	var pointer Pointer
	if decodeCanonical(pointerRaw, &pointer) != nil || repositoryHash(pointer.Repository) != repositories[index] {
		return result, invalid("lifecycle pointer")
	}
	markerGeneration := ""
	if raw, markerErr := readBoundedRegular(filepath.Join(repositoryRoot, "publishing.json"), MaxManifestBytes); markerErr == nil {
		var marker Marker
		if decodeCanonical(raw, &marker) != nil || marker.Repository != pointer.Repository {
			return result, invalid("lifecycle marker")
		}
		markerGeneration = marker.GenerationDigest
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return result, markerErr
	}
	generationEntries, err := boundedDirectory(repositoryRoot, MaxRepositoryGenerations+3)
	if err != nil {
		return result, err
	}
	type candidate struct {
		name       string
		generation string
		modified   time.Time
	}
	var generations []candidate
	var collecting []candidate
	for _, entry := range generationEntries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !entry.IsDir() {
			continue
		}
		if len(entry.Name()) == 64 && validLowerHex(entry.Name()) {
			generations = append(generations, candidate{name: entry.Name(), generation: "sha256:" + entry.Name(), modified: entry.ModTime()})
			continue
		}
		const collectingPrefix = "collecting-"
		if strings.HasPrefix(entry.Name(), collectingPrefix) && len(entry.Name()) == len(collectingPrefix)+64 &&
			validLowerHex(strings.TrimPrefix(entry.Name(), collectingPrefix)) {
			collecting = append(collecting, candidate{name: entry.Name(), generation: "sha256:" + strings.TrimPrefix(entry.Name(), collectingPrefix), modified: entry.ModTime()})
		}
	}
	if len(generations) > MaxRepositoryGenerations || len(collecting) > 1 {
		return result, ErrLimit
	}
	if len(collecting) == 1 {
		result.Scanned = 1
		if err := lifecycleFence(root, pointer.Repository, collecting[0].generation, pins); errors.Is(err, ErrPinned) {
			return result, nil
		} else if err != nil {
			return result, err
		}
		deleted, complete, err := deleteGenerationStep(filepath.Join(repositoryRoot, collecting[0].name), deleteLimit)
		result.Deleted = deleted
		result.More = result.More || !complete
		return result, err
	}
	sort.Slice(generations, func(i, j int) bool {
		if !generations[i].modified.Equal(generations[j].modified) {
			return generations[i].modified.After(generations[j].modified)
		}
		return generations[i].name > generations[j].name
	})
	protected := 0
	for _, generation := range generations {
		if generation.generation == pointer.GenerationDigest || generation.generation == markerGeneration {
			continue
		}
		if protected < RetainedGenerations-1 {
			protected++
			continue
		}
		result.Scanned++
		if now.Sub(generation.modified) < GenerationMaxAge && len(generations) <= RetainedGenerations {
			continue
		}
		if err := lifecycleFence(root, pointer.Repository, generation.generation, pins); errors.Is(err, ErrPinned) {
			continue
		} else if err != nil {
			return result, err
		}
		collectingName := "collecting-" + generation.name
		collectingPath := filepath.Join(repositoryRoot, collectingName)
		if err := os.Rename(filepath.Join(repositoryRoot, generation.name), collectingPath); err != nil {
			return result, err
		}
		deleted, complete, err := deleteGenerationStep(collectingPath, deleteLimit)
		result.Deleted = deleted
		result.More = result.More || !complete
		return result, err
	}
	return result, nil
}

func lifecycleFence(root, repository, generation string, pins PinChecker) error {
	current, err := readPointer(root, repository)
	if err != nil {
		return err
	}
	if current.GenerationDigest == generation {
		return ErrStale
	}
	raw, err := readBoundedRegular(markerPath(root, repository), MaxManifestBytes)
	if err == nil {
		var marker Marker
		if decodeCanonical(raw, &marker) != nil || marker.Repository != repository || marker.GenerationDigest == generation {
			return ErrStale
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if pins.Pinned(repository, generation) {
		return ErrPinned
	}
	return nil
}

// deleteGenerationStep removes regular publication files in dependency order:
// observation objects, member files, the manifest, and finally the generation
// directory. It never follows symlinks and never exceeds the supplied budget.
func deleteGenerationStep(directory string, budget int) (deleted int, complete bool, err error) {
	objects := filepath.Join(directory, "objects")
	info, statErr := os.Lstat(objects)
	if statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return 0, false, invalid("lifecycle objects directory")
		}
		names, readErr := boundedNames(objects, budget)
		if readErr != nil {
			return 0, false, readErr
		}
		for _, name := range names {
			if !validObservationBasename(name) {
				return deleted, false, invalid("lifecycle observation name")
			}
			if err := removeRegular(filepath.Join(objects, name)); err != nil {
				return deleted, false, err
			}
			deleted++
			if deleted == budget {
				return deleted, false, nil
			}
		}
		if err := os.Remove(objects); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return deleted, false, err
			}
		} else {
			deleted++
			if deleted == budget {
				return deleted, false, nil
			}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return 0, false, statErr
	}

	names, err := boundedNames(directory, budget-deleted+1)
	if err != nil {
		return deleted, false, err
	}
	members := make([]string, 0, len(names))
	hasManifest := false
	for _, name := range names {
		switch {
		case name == manifestName():
			hasManifest = true
		case validMemberBasename(name):
			members = append(members, name)
		default:
			return deleted, false, invalid("lifecycle generation entry")
		}
	}
	sort.Strings(members)
	for _, name := range members {
		if deleted == budget {
			return deleted, false, nil
		}
		if err := removeRegular(filepath.Join(directory, name)); err != nil {
			return deleted, false, err
		}
		deleted++
	}
	if len(members) > 0 {
		return deleted, false, nil
	}
	if hasManifest {
		if deleted == budget {
			return deleted, false, nil
		}
		if err := removeRegular(filepath.Join(directory, manifestName())); err != nil {
			return deleted, false, err
		}
		deleted++
		return deleted, false, nil
	}
	if deleted == budget {
		return deleted, false, nil
	}
	if err := os.Remove(directory); err != nil {
		return deleted, false, err
	}
	return deleted + 1, true, nil
}

func boundedNames(path string, limit int) ([]string, error) {
	if limit < 1 {
		return nil, invalid("lifecycle deletion budget")
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	names, err := directory.Readdirnames(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return names, nil
}

func removeRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return invalid("lifecycle non-regular file")
	}
	return os.Remove(path)
}

func validObservationBasename(name string) bool {
	if len(name) != 16+1+64+len(".json") || name[16] != '-' || !strings.HasSuffix(name, ".json") {
		return false
	}
	return validLowerHex(name[:16]) && validLowerHex(name[17:17+64])
}

func validMemberBasename(name string) bool {
	if len(name) != len("member-00000.jsonl") || !strings.HasPrefix(name, "member-") || !strings.HasSuffix(name, ".jsonl") {
		return false
	}
	ordinal, err := strconv.Atoi(name[len("member-") : len("member-")+5])
	return err == nil && ordinal >= 0 && ordinal < 100_000
}

func validLowerHex(value string) bool {
	for _, character := range value {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return value != ""
}

func boundedDirectory(path string, limit int) ([]os.FileInfo, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.Readdir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > limit {
		return nil, ErrLimit
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), string(filepath.Separator)) {
			return nil, invalid("directory entry")
		}
	}
	return entries, nil
}
