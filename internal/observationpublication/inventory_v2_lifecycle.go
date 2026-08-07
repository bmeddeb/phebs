package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

type InventoryLifecycleResultV2 struct {
	Cursor                  string
	Scanned                 int
	Deleted                 int
	More                    bool
	CurrentRecords          int
	CurrentEncodedBytes     int64
	CurrentObservationBytes int64
}

const MaxInventoryLifecycleCandidatesV2 = 64

// InventoryPinCheckerV2 combines active reader leases with durable proof and
// Investigation ownership. The lifecycle owner invokes it only after exact
// root/marker revalidation while holding the common mutation/backup lock.
type InventoryPinCheckerV2 interface {
	PinnedInventoryV2(context.Context, string, string) (bool, error)
}

type InventoryDurablePinCheckerV2 interface {
	PinnedInventoryProofV2(context.Context, string, string) (bool, error)
	PinnedInventoryInvestigationV2(context.Context, string, string) (bool, error)
}

type InventoryPinsV2 struct {
	Cache   *InventoryCacheV2
	Durable InventoryDurablePinCheckerV2
}

func (pins InventoryPinsV2) PinnedInventoryV2(
	ctx context.Context, repository, generation string,
) (bool, error) {
	if pins.Cache != nil && pins.Cache.PinnedGeneration(repository, generation) {
		return true, nil
	}
	if pins.Durable == nil {
		return false, nil
	}
	pinned, err := pins.Durable.PinnedInventoryProofV2(ctx, repository, generation)
	if err != nil || pinned {
		return pinned, err
	}
	return pins.Durable.PinnedInventoryInvestigationV2(ctx, repository, generation)
}

// SweepInventoryLifecycleV2 examines one repository's joint source/
// observation namespace. Current, rollback-floor, marker stage, active lease,
// proof, and Investigation roots outrank collection. A candidate is renamed
// out of the publication namespace before bounded deletion, and interrupted
// collecting/stage drains resume on later fair turns.
func SweepInventoryLifecycleV2(
	ctx context.Context, root string, now time.Time, cursor string,
	pins InventoryPinCheckerV2, candidateLimit, deleteLimit int,
) (InventoryLifecycleResultV2, error) {
	var result InventoryLifecycleResultV2
	if !filepath.IsAbs(root) || pins == nil || candidateLimit < 1 ||
		candidateLimit > MaxInventoryLifecycleCandidatesV2 || deleteLimit < 1 {
		return result, invalid("inventory v2 lifecycle input")
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
		if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
			continue
		}
		if info, statErr := os.Lstat(filepath.Join(root, entry.Name(), InventoryPublicationDirectoryV2)); statErr == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return result, invalid("inventory v2 lifecycle repository namespace")
			}
			repositories = append(repositories, entry.Name())
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return result, statErr
		}
	}
	sort.Strings(repositories)
	if len(repositories) == 0 {
		return result, nil
	}
	position := sort.SearchStrings(repositories, cursor)
	if position < len(repositories) && repositories[position] == cursor {
		position++
	}
	if position == len(repositories) {
		position = 0
	}
	result.Cursor = repositories[position]
	result.More = len(repositories) > 1
	directory := filepath.Join(root, result.Cursor, InventoryPublicationDirectoryV2)

	var publicationRoot InventoryPublicationRootV2
	rootPresent := false
	raw, readErr := readBoundedRegular(filepath.Join(directory, InventoryPublicationRootNameV2), MaxManifestBytes)
	if readErr == nil {
		if decodeCanonical(raw, &publicationRoot) != nil ||
			validateInventoryPublicationRootV2(publicationRoot, publicationRoot.Repository) != nil ||
			repositoryHash(publicationRoot.Repository) != result.Cursor {
			return result, invalid("inventory v2 lifecycle root")
		}
		rootPresent = true
		result.CurrentRecords = publicationRoot.Current.Records
		result.CurrentEncodedBytes = publicationRoot.Current.EncodedBytes
		result.CurrentObservationBytes = publicationRoot.Current.ObservationBytes
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return result, readErr
	}
	marker, markerPresent, err := readInventoryLifecycleMarkerV2(directory, result.Cursor)
	if err != nil {
		return result, err
	}
	repository := publicationRoot.Repository
	if markerPresent {
		if repository != "" && repository != marker.Repository {
			return result, invalid("inventory v2 lifecycle authority mismatch")
		}
		repository = marker.Repository
	}
	if repository == "" {
		// An abandoned markerless stage does not disclose its repository through
		// the one-way directory hash. It can still be drained because it is not a
		// canonical generation and no reader can acquire it.
		repository = "unknown"
	}

	generationEntries, err := boundedDirectory(
		directory, MaxInventoryPublicationEntriesV2,
	)
	if err != nil {
		return result, err
	}
	type candidate struct {
		name, generation string
		modified         time.Time
	}
	var generations, collecting, stages []candidate
	for _, entry := range generationEntries {
		name := entry.Name()
		if name == InventoryPublicationRootNameV2 || name == InventoryPublicationMarkerNameV2 {
			if !entry.Mode().IsRegular() {
				return result, invalid("inventory v2 lifecycle control")
			}
			continue
		}
		if !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			return result, invalid("inventory v2 lifecycle entry")
		}
		info := entry
		switch {
		case len(name) == 64 && validLowerHex(name):
			generations = append(generations, candidate{name: name, generation: "sha256:" + name, modified: info.ModTime()})
		case strings.HasPrefix(name, "collecting-") && len(name) == len("collecting-")+64 && validLowerHex(strings.TrimPrefix(name, "collecting-")):
			value := strings.TrimPrefix(name, "collecting-")
			collecting = append(collecting, candidate{name: name, generation: "sha256:" + value, modified: info.ModTime()})
		case strings.HasPrefix(name, ".stage-") && len(name) == len(".stage-")+32 && validLowerHex(strings.TrimPrefix(name, ".stage-")):
			stages = append(stages, candidate{name: name, modified: info.ModTime()})
		case strings.HasPrefix(name, "collecting-stage-") && len(name) == len("collecting-stage-")+32 && validLowerHex(strings.TrimPrefix(name, "collecting-stage-")):
			collecting = append(collecting, candidate{name: name, modified: info.ModTime()})
		default:
			return result, invalid("inventory v2 lifecycle directory name")
		}
	}
	if len(collecting) > 0 {
		sort.Slice(collecting, func(i, j int) bool { return collecting[i].name < collecting[j].name })
		result.Scanned = 1
		deleted, complete, err := deleteInventoryTreeStepV2(
			filepath.Join(directory, collecting[0].name), deleteLimit, "",
		)
		result.Deleted = deleted
		result.More = result.More || !complete || len(collecting) > 1
		return result, err
	}
	if len(stages) > 0 {
		sort.Slice(stages, func(i, j int) bool { return stages[i].name < stages[j].name })
		for _, stage := range stages {
			if markerPresent && marker.Stage == stage.name {
				continue
			}
			if result.Scanned == candidateLimit {
				result.More = true
				return result, nil
			}
			result.Scanned++
			destination := filepath.Join(directory, "collecting-stage-"+strings.TrimPrefix(stage.name, ".stage-"))
			if err := os.Rename(filepath.Join(directory, stage.name), destination); err != nil {
				return result, err
			}
			if err := syncDirectory(directory); err != nil {
				return result, err
			}
			deleted, complete, err := deleteInventoryTreeStepV2(destination, deleteLimit, "")
			result.Deleted = deleted
			result.More = result.More || !complete
			return result, err
		}
	}
	sort.Slice(generations, func(i, j int) bool {
		if !generations[i].modified.Equal(generations[j].modified) {
			return generations[i].modified.After(generations[j].modified)
		}
		return generations[i].name > generations[j].name
	})
	for _, generation := range generations {
		if rootPresent && (publicationRoot.Current.GenerationDigest == generation.generation ||
			publicationRoot.Prior != nil && publicationRoot.Prior.GenerationDigest == generation.generation) {
			continue
		}
		if markerPresent && marker.Candidate != nil &&
			marker.Candidate.GenerationDigest == generation.generation {
			continue
		}
		if result.Scanned == candidateLimit {
			result.More = true
			return result, nil
		}
		result.Scanned++
		if now.Sub(generation.modified) < GenerationMaxAge && len(generations) <= RetainedGenerations {
			continue
		}
		pinned, err := pins.PinnedInventoryV2(ctx, repository, generation.generation)
		if err != nil {
			return result, err
		}
		if pinned {
			continue
		}
		transition := InventoryPublicationTransitionV2{
			Repository: repository, Directory: filepath.Join(directory, generation.name),
			SourceDirectory:    filepath.Join(directory, generation.name, InventoryPublicationSourceNameV2),
			InventoryDirectory: filepath.Join(directory, generation.name, InventoryPublicationInventoryNameV2),
		}
		if _, err := validateInventoryPublicationStageV2(ctx, transition); err != nil {
			return result, err
		}
		// Re-read both controls and the durable pin provider immediately before
		// the same-parent retirement rename.
		confirmedRoot, rootErr := ReadInventoryPublicationRootV2(root, repository)
		if rootErr == nil && (confirmedRoot.Current.GenerationDigest == generation.generation ||
			confirmedRoot.Prior != nil && confirmedRoot.Prior.GenerationDigest == generation.generation) {
			continue
		}
		if rootErr != nil && !errors.Is(rootErr, os.ErrNotExist) {
			return result, rootErr
		}
		confirmedMarker, present, markerErr := readInventoryPublicationMarkerV2(root, repository)
		if markerErr != nil {
			return result, markerErr
		}
		if present && confirmedMarker.Previous != nil &&
			(confirmedMarker.Previous.Current.GenerationDigest == generation.generation ||
				confirmedMarker.Previous.Prior != nil && confirmedMarker.Previous.Prior.GenerationDigest == generation.generation) {
			continue
		}
		if present && confirmedMarker.Candidate != nil &&
			confirmedMarker.Candidate.GenerationDigest == generation.generation {
			continue
		}
		pinned, err = pins.PinnedInventoryV2(ctx, repository, generation.generation)
		if err != nil || pinned {
			return result, err
		}
		destination := filepath.Join(directory, "collecting-"+generation.name)
		if err := os.Rename(filepath.Join(directory, generation.name), destination); err != nil {
			return result, err
		}
		if err := syncDirectory(directory); err != nil {
			return result, err
		}
		deleted, complete, err := deleteInventoryTreeStepV2(destination, deleteLimit, "")
		result.Deleted = deleted
		result.More = result.More || !complete
		return result, err
	}
	return result, nil
}

func readInventoryLifecycleMarkerV2(
	directory, repositoryHashValue string,
) (InventoryPublicationMarkerV2, bool, error) {
	raw, err := readBoundedRegular(filepath.Join(directory, InventoryPublicationMarkerNameV2), MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return InventoryPublicationMarkerV2{}, false, nil
	}
	if err != nil {
		return InventoryPublicationMarkerV2{}, false, err
	}
	var marker InventoryPublicationMarkerV2
	if decodeCanonical(raw, &marker) != nil || validateInventoryPublicationMarkerV2(marker) != nil ||
		repositoryHash(marker.Repository) != repositoryHashValue {
		return InventoryPublicationMarkerV2{}, false, invalid("inventory v2 lifecycle marker")
	}
	return marker, true, nil
}

func deleteInventoryTreeStepV2(
	directory string, budget int, relative string,
) (deleted int, complete bool, err error) {
	if budget < 1 {
		return 0, false, invalid("inventory v2 lifecycle delete budget")
	}
	names, err := boundedNames(directory, budget+1)
	if err != nil {
		return 0, false, err
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil {
			return deleted, false, err
		}
		childRelative := filepath.Join(relative, name)
		if !validInventoryDeleteEntryV2(relative, name, info.IsDir()) || info.Mode()&os.ModeSymlink != 0 {
			return deleted, false, invalid("inventory v2 lifecycle unknown or special artifact")
		}
		if info.IsDir() {
			childDeleted, childComplete, childErr := deleteInventoryTreeStepV2(
				path, budget-deleted, childRelative,
			)
			deleted += childDeleted
			if childErr != nil || !childComplete || deleted == budget {
				return deleted, false, childErr
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return deleted, false, invalid("inventory v2 lifecycle special artifact")
		}
		if err := os.Remove(path); err != nil {
			return deleted, false, err
		}
		deleted++
		if deleted == budget {
			return deleted, false, nil
		}
	}
	remaining, err := boundedNames(directory, 1)
	if err != nil {
		return deleted, false, err
	}
	if len(remaining) != 0 {
		return deleted, false, nil
	}
	if err := os.Remove(directory); err != nil {
		return deleted, false, err
	}
	return deleted + 1, true, nil
}

func validInventoryDeleteEntryV2(parent, name string, directory bool) bool {
	switch filepath.ToSlash(parent) {
	case "":
		return directory && (name == InventoryPublicationSourceNameV2 || name == InventoryPublicationInventoryNameV2)
	case InventoryPublicationSourceNameV2:
		return directory && (validSegmentDirectoryNameV2(name) ||
			validSourcePartitionTemporaryDirectoryV2(name)) ||
			!directory && validSourcePartitionControlNameV2(name, ".root.json")
	case InventoryPublicationInventoryNameV2:
		return directory && validSegmentDirectoryNameV2(name) || !directory && name == InventoryRootNameV2
	}
	parts := strings.Split(filepath.ToSlash(parent), "/")
	if len(parts) == 2 && parts[0] == InventoryPublicationSourceNameV2 && validSegmentDirectoryNameV2(parts[1]) {
		return !directory && (validSourcePartitionControlNameV2(name, ".manifest.json") ||
			validSourcePartitionMemberNameV2(name))
	}
	if len(parts) == 2 && parts[0] == InventoryPublicationSourceNameV2 &&
		strings.HasPrefix(parts[1], ".source-partition-spool-") {
		return !directory && validSourcePartitionSpoolNameV2(name)
	}
	if len(parts) == 2 && parts[0] == InventoryPublicationSourceNameV2 &&
		strings.HasPrefix(parts[1], ".source-partition-members-") {
		return !directory && validMemberBasename(name)
	}
	if len(parts) == 2 && parts[0] == InventoryPublicationInventoryNameV2 && validSegmentDirectoryNameV2(parts[1]) {
		return directory && name == "objects" || !directory && (name == "segment.json" || validMemberBasename(name))
	}
	if len(parts) == 3 && parts[0] == InventoryPublicationInventoryNameV2 &&
		validSegmentDirectoryNameV2(parts[1]) && parts[2] == "objects" {
		return !directory && validObservationBasename(name)
	}
	return false
}

func validSegmentDirectoryNameV2(name string) bool {
	if len(name) != len("segment-00000") || !strings.HasPrefix(name, "segment-") {
		return false
	}
	for _, value := range name[len("segment-"):] {
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}

func validSourcePartitionControlNameV2(name, suffix string) bool {
	const prefix = "phebs-source-partitions-"
	if len(name) != len(prefix)+64+len(suffix) || !strings.HasPrefix(name, prefix) ||
		!strings.HasSuffix(name, suffix) {
		return false
	}
	return validLowerHex(name[len(prefix) : len(prefix)+64])
}

func validSourcePartitionMemberNameV2(name string) bool {
	const prefix = "phebs-source-partitions-"
	const suffix = ".jsonl"
	if len(name) != len(prefix)+16+1+16+1+5+len(suffix) ||
		!strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	parts := strings.Split(value, ".")
	if len(parts) != 2 || len(parts[1]) != 5 {
		return false
	}
	digests := strings.Split(parts[0], "-")
	if len(digests) != 2 || len(digests[0]) != 16 || len(digests[1]) != 16 ||
		!validLowerHex(digests[0]) || !validLowerHex(digests[1]) {
		return false
	}
	for _, value := range parts[1] {
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}

func validSourcePartitionTemporaryDirectoryV2(name string) bool {
	for _, prefix := range []string{".source-partition-spool-", ".source-partition-members-"} {
		if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) || len(name) > len(prefix)+32 {
			continue
		}
		for _, value := range name[len(prefix):] {
			if (value < '0' || value > '9') &&
				(value < 'A' || value > 'Z') &&
				(value < 'a' || value > 'z') {
				return false
			}
		}
		return true
	}
	return false
}

func validSourcePartitionSpoolNameV2(name string) bool {
	if !strings.HasSuffix(name, ".jsonl") {
		return false
	}
	value := strings.TrimSuffix(name, ".jsonl")
	for _, prefix := range []string{"root-", "split-"} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		bits := strings.TrimPrefix(value, prefix)
		if len(bits) < sourcepartition.InitialPrefixBits || len(bits) > 256 {
			return false
		}
		for _, bit := range bits {
			if bit != '0' && bit != '1' {
				return false
			}
		}
		return true
	}
	return false
}
