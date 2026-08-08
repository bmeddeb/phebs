package relationshippublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/resolvernamespace"
)

const (
	// MaxRecoveryWork is one coherent startup bound across namespace discovery,
	// directory scans, and repair deletions. It admits 65 entries in every
	// supported repository/component namespace without admitting the full
	// 82-million-entry Cartesian repair envelope.
	MaxRecoveryWork       = 2_000_000
	MaxRecoveryOwners     = MaxLifecycleRepositories * (RetainedGenerations + 2)
	MaxRecoveryNamespaces = MaxLifecycleRepositories * 4
)

type RecoveryReport struct {
	Repositories int
	Completed    int
	Unavailable  int
	Invalid      int
}

type RecoveryPinStore interface {
	PinPartitionedExtractionRun(context.Context, string, string) error
	ReconcilePartitionedExtractionOwners(context.Context, []string) error
}

// RecoverAll completes exact component/root markers under the caller's
// exclusive startup mutation lock. Corrupt derived state is counted and left
// invisible; component-only crash residue is independently discovered.
func RecoverAll(ctx context.Context, dataDir string, pins RecoveryPinStore) (RecoveryReport, error) {
	var report RecoveryReport
	if !filepath.IsAbs(dataDir) || pins == nil {
		return report, ErrInvalid
	}
	root := filepath.Join(dataDir, "relationships")
	base := filepath.Join(root, "relationship-publications")
	hashes, work, invalid, err := discoverRecoveryNamespaces(dataDir)
	if err != nil {
		return report, err
	}
	report.Invalid += invalid
	owners := make(map[string]struct{}, MaxRecoveryOwners)
	for _, hash := range hashes {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		repositoryDirectory := filepath.Join(base, hash)
		componentDirectories := []struct {
			directory string
			prefixed  bool
		}{
			{repositoryDirectory, false},
			{filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces", hash), true},
			{filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings", hash), false},
			{filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings", hash), false},
		}
		removedRepositoryDirectory := false
		repairFailed := false
		for index, component := range componentDirectories {
			removed, consumed, repairErr := repairStageDirectories(
				ctx, component.directory, MaxRecoveryWork-work,
			)
			work += consumed
			if repairErr != nil {
				if errors.Is(repairErr, ErrLimit) {
					return report, repairErr
				}
				report.Invalid++
				repairFailed = true
				break
			}
			if index == 0 {
				removedRepositoryDirectory = removed
			}
		}
		if repairFailed {
			continue
		}
		if removedRepositoryDirectory || !directoryExists(repositoryDirectory) {
			for _, component := range componentDirectories[1:] {
				consumed, cleanupErr := cleanupOrphanComponentNamespace(
					ctx, component.directory, component.prefixed, MaxRecoveryWork-work,
				)
				work += consumed
				if cleanupErr != nil {
					if errors.Is(cleanupErr, ErrLimit) {
						return report, cleanupErr
					}
					report.Invalid++
					break
				}
			}
			continue
		}

		report.Repositories++
		repository, unavailable, err := recoveryRepository(repositoryDirectory, hash)
		if err != nil {
			report.Invalid++
			continue
		}
		if unavailable {
			report.Unavailable++
		}
		resolverRoot := filepath.Join(dataDir, "relationship-resolver-namespaces")
		resolverDirectory := componentDirectories[1].directory
		if _, statErr := os.Lstat(resolverDirectory); statErr == nil {
			if _, recoverErr := resolvernamespace.Recover(ctx, resolverRoot, repository); recoverErr != nil {
				report.Invalid++
				continue
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return report, statErr
		}
		completed, recoverErr := Recover(ctx, root, repository)
		if recoverErr != nil {
			report.Invalid++
			continue
		}
		if completed {
			report.Completed++
		}
		remaining := MaxRecoveryWork - work
		if remaining < 1 {
			return report, ErrLimit
		}
		generationEntries, listErr := boundedLifecycleDirectory(
			repositoryDirectory, min(MaxRepositoryRepairEntries, remaining),
		)
		if listErr != nil {
			return report, listErr
		}
		work += len(generationEntries)
		protected, protectErr := recoveryProtectedGenerations(repositoryDirectory, generationEntries)
		if protectErr != nil {
			report.Invalid++
			continue
		}
		for _, generationEntry := range generationEntries {
			name := generationEntry.Name()
			if _, protect := protected["sha256:"+name]; !protect || !generationEntry.IsDir() ||
				len(name) != 64 || !validLowerHex(name) {
				continue
			}
			raw, rootErr := readRegular(
				filepath.Join(repositoryDirectory, name, "root.json"), MaxRootBytes,
			)
			work++
			if work > MaxRecoveryWork {
				return report, ErrLimit
			}
			if rootErr != nil {
				report.Invalid++
				continue
			}
			var rootValue Root
			if decodeExact(raw, MaxRootBytes, &rootValue) != nil || validateRoot(rootValue) != nil ||
				rootValue.GenerationDigest != "sha256:"+name {
				report.Invalid++
				continue
			}
			if rootValue.Authority.Upstream == nil {
				continue
			}
			owner := "relationship:sha256:" + name
			if _, present := owners[owner]; !present {
				if len(owners) >= MaxRecoveryOwners {
					return report, ErrLimit
				}
				owners[owner] = struct{}{}
			}
			for _, domain := range rootValue.Authority.Upstream.Domains {
				work++
				if work > MaxRecoveryWork {
					return report, ErrLimit
				}
				if pinErr := pins.PinPartitionedExtractionRun(ctx, domain.RunID, owner); pinErr != nil {
					return report, pinErr
				}
			}
		}
	}
	orderedOwners := make([]string, 0, len(owners))
	for owner := range owners {
		orderedOwners = append(orderedOwners, owner)
	}
	sort.Strings(orderedOwners)
	// A visible root was pinned before publication. If any namespace audit is
	// incomplete, preserve the existing global owner set rather than deleting
	// an owner that this recovery pass could not re-prove.
	if report.Invalid != 0 {
		return report, nil
	}
	if err := pins.ReconcilePartitionedExtractionOwners(ctx, orderedOwners); err != nil {
		return report, err
	}
	return report, nil
}

func recoveryNamespaceBases(dataDir string) []string {
	return []string{
		filepath.Join(dataDir, "relationships", "relationship-publications"),
		filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces"),
		filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings"),
		filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings"),
	}
}

func discoverRecoveryNamespaces(dataDir string) ([]string, int, int, error) {
	values := make(map[string]struct{}, MaxRecoveryNamespaces)
	work, invalid := 0, 0
	for _, base := range recoveryNamespaceBases(dataDir) {
		entries, err := boundedLifecycleDirectory(base, MaxLifecycleRepositories)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, work, invalid, err
		}
		work += len(entries)
		for _, entry := range entries {
			if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
				invalid++
				continue
			}
			values[entry.Name()] = struct{}{}
			if len(values) > MaxRecoveryNamespaces {
				return nil, work, invalid, ErrLimit
			}
		}
	}
	hashes := make([]string, 0, len(values))
	for value := range values {
		hashes = append(hashes, value)
	}
	sort.Strings(hashes)
	return hashes, work, invalid, nil
}

// repairStageDirectories removes crash-only, never-authoritative build stages
// within both the per-directory repair envelope and the remaining global
// startup budget. It reports directory removal and charged work.
func repairStageDirectories(
	ctx context.Context, directory string, budget int,
) (bool, int, error) {
	if budget < 1 {
		return false, 0, ErrLimit
	}
	entries, err := boundedLifecycleDirectory(
		directory, min(MaxRepositoryRepairEntries, budget),
	)
	if errors.Is(err, os.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	work := len(entries)
	nonStages := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".stage-") {
			nonStages++
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, work, err
		}
		if len(entry.Name()) == len(".stage-") || !entry.IsDir() {
			return false, work, ErrInvalid
		}
		remaining := budget - work
		if remaining < 1 {
			return false, work, ErrLimit
		}
		deleted, complete, drainErr := drainFlatGeneration(
			filepath.Join(directory, entry.Name()), min(MaxStageRepairFiles+1, remaining),
		)
		work += deleted
		if drainErr != nil {
			return false, work, drainErr
		}
		if !complete {
			return false, work, ErrLimit
		}
	}
	if nonStages != 0 {
		return false, work, nil
	}
	if work >= budget {
		return false, work, ErrLimit
	}
	if err := os.Remove(directory); err != nil {
		return false, work, err
	}
	return true, work + 1, nil
}

func cleanupOrphanComponentNamespace(
	ctx context.Context, directory string, prefixed bool, budget int,
) (int, error) {
	if budget < 1 {
		return 0, ErrLimit
	}
	entries, err := boundedLifecycleDirectory(
		directory, min(MaxRepositoryRepairEntries, budget),
	)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	work := len(entries)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return work, err
		}
		if entry.IsDir() {
			name := entry.Name()
			if prefixed {
				name = strings.TrimPrefix(name, "generation-")
			}
			if len(name) != 64 || !validLowerHex(name) {
				return work, ErrInvalid
			}
			remaining := budget - work
			if remaining < 1 {
				return work, ErrLimit
			}
			deleted, complete, drainErr := drainFlatGeneration(
				filepath.Join(directory, entry.Name()), min(MaxStageRepairFiles+1, remaining),
			)
			work += deleted
			if drainErr != nil {
				return work, drainErr
			}
			if !complete {
				return work, ErrLimit
			}
			continue
		}
		if entry.Name() != "current.json" && entry.Name() != "publishing.json" {
			return work, ErrInvalid
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			return work, ErrInvalid
		}
		if work >= budget {
			return work, ErrLimit
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return work, err
		}
		work++
	}
	if work >= budget {
		return work, ErrLimit
	}
	if err := os.Remove(directory); err != nil {
		return work, err
	}
	return work + 1, nil
}

func recoveryProtectedGenerations(
	directory string, entries []os.DirEntry,
) (map[string]struct{}, error) {
	protected := make(map[string]struct{}, RetainedGenerations+2)
	if raw, err := readRegular(filepath.Join(directory, "current.json"), MaxRootBytes); err == nil {
		var pointer Pointer
		if decodeExact(raw, MaxRootBytes, &pointer) != nil || validatePointer(pointer) != nil {
			return nil, ErrInvalid
		}
		protected[pointer.GenerationDigest] = struct{}{}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if raw, err := readRegular(filepath.Join(directory, UnavailableName), MaxRootBytes); err == nil {
		var unavailable Unavailable
		if decodeExact(raw, MaxRootBytes, &unavailable) != nil ||
			validateUnavailable(unavailable, unavailable.Upstream.Repository) != nil {
			return nil, ErrInvalid
		}
		if unavailable.Prior != nil {
			protected[unavailable.Prior.GenerationDigest] = struct{}{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var generations []lifecycleGeneration
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != 64 || !validLowerHex(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		generations = append(generations, lifecycleGeneration{
			name: name, generation: "sha256:" + name, modified: info.ModTime(),
		})
	}
	sort.Slice(generations, func(i, j int) bool {
		if !generations[i].modified.Equal(generations[j].modified) {
			return generations[i].modified.After(generations[j].modified)
		}
		return generations[i].name > generations[j].name
	})
	remaining := RetainedGenerations - 1
	for _, generation := range generations {
		if _, present := protected[generation.generation]; present {
			continue
		}
		if remaining == 0 {
			break
		}
		protected[generation.generation] = struct{}{}
		remaining--
	}
	return protected, nil
}

func directoryExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func recoveryRepository(directory, hash string) (string, bool, error) {
	if raw, err := readRegular(filepath.Join(directory, UnavailableName), MaxRootBytes); err == nil {
		var value Unavailable
		if decodeExact(raw, MaxRootBytes, &value) != nil ||
			validateUnavailable(value, value.Upstream.Repository) != nil ||
			repositoryHash(value.Upstream.Repository) != hash {
			return "", false, ErrInvalid
		}
		return value.Upstream.Repository, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	for _, name := range []string{"publishing.json", "current.json"} {
		raw, err := readRegular(filepath.Join(directory, name), MaxRootBytes)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if name == "publishing.json" {
			var marker Marker
			if decodeExact(raw, MaxRootBytes, &marker) != nil || validateMarker(marker) != nil ||
				repositoryHash(marker.Pointer.Repository) != hash {
				return "", false, ErrInvalid
			}
			return marker.Pointer.Repository, false, nil
		}
		var pointer Pointer
		if decodeExact(raw, MaxRootBytes, &pointer) != nil || validatePointer(pointer) != nil ||
			repositoryHash(pointer.Repository) != hash {
			return "", false, ErrInvalid
		}
		return pointer.Repository, false, nil
	}
	return "", false, ErrInvalid
}
