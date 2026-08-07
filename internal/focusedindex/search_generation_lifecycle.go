package focusedindex

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SearchGenerationLifecycleResult struct {
	Cursor         string
	Scanned        int
	Deleted        int
	More           bool
	LogicalBytes   int64
	AllocatedBytes int64
	AllocatedState string
}

type SearchGenerationPinChecker interface {
	Pinned(repository, generation string) bool
}

// SweepSearchGenerationLifecycle examines one bounded repository namespace.
// Current, rollback, a transition marker, and active reader pins are exact
// roots. One stale generation is renamed before at most deleteLimit regular
// files are removed; a later fair turn resumes the collecting directory.
func SweepSearchGenerationLifecycle(
	ctx context.Context,
	indexDir string,
	now time.Time,
	cursor string,
	pins SearchGenerationPinChecker,
	deleteLimit int,
) (SearchGenerationLifecycleResult, error) {
	var result SearchGenerationLifecycleResult
	if !filepath.IsAbs(indexDir) || pins == nil || deleteLimit < 1 {
		return result, errors.New("invalid search lifecycle input")
	}
	base := SearchGenerationRootDirectory(indexDir)
	entries, err := boundedSearchDirectory(base, MaxSearchLifecycleRepositories)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var repositories []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			len(entry.Name()) != 64 || !isLowerHex(entry.Name()) {
			return result, errors.New("invalid search lifecycle repository inventory")
		}
		repositories = append(repositories, entry.Name())
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
	repositoryDirectory := filepath.Join(base, result.Cursor)
	if err := ensureRealDirectory(repositoryDirectory); err != nil {
		return result, err
	}
	generationEntries, err := boundedSearchDirectory(
		repositoryDirectory, MaxSearchRepositoryGenerations+8,
	)
	if err != nil {
		return result, err
	}
	type generationCandidate struct {
		name, digest string
		modified     time.Time
	}
	var generations, collecting []generationCandidate
	var abandonedStages []string
	for _, entry := range generationEntries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return result, errors.New("invalid search lifecycle generation inventory")
		}
		name := entry.Name()
		raw := strings.TrimPrefix(name, "collecting-")
		if len(raw) != 64 || !isLowerHex(raw) {
			if strings.HasPrefix(name, ".stage-") || strings.HasPrefix(name, ".legacy-") {
				if !strings.Contains(name, "-"+lifecycleOwner+"-") {
					abandonedStages = append(abandonedStages, name)
				}
				continue
			}
			return result, errors.New("invalid search lifecycle generation name")
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return result, err
		}
		candidate := generationCandidate{
			name: name, digest: "sha256:" + raw, modified: entryInfo.ModTime(),
		}
		if strings.HasPrefix(name, "collecting-") {
			collecting = append(collecting, candidate)
		} else {
			generations = append(generations, candidate)
		}
	}
	if len(abandonedStages) > 0 {
		sort.Strings(abandonedStages)
		result.Scanned = 1
		deleted, complete, err := deleteSearchGenerationStep(
			filepath.Join(repositoryDirectory, abandonedStages[0]), deleteLimit,
		)
		result.Deleted = deleted
		result.More = result.More || !complete || len(abandonedStages) > 1
		return result, err
	}
	repository := ""
	probe := generationCandidate{}
	if len(generations) > 0 {
		probe = generations[0]
	} else if len(collecting) > 0 {
		probe = collecting[0]
	}
	if probe.name != "" {
		receipt, err := readSearchLifecycleReceipt(
			filepath.Join(repositoryDirectory, probe.name), result.Cursor, probe.digest,
		)
		if err != nil {
			return result, err
		}
		repository = receipt.Repository
	}
	root, rootErr := ReadSearchGenerationRoot(indexDir, repository)
	rootMissing := errors.Is(rootErr, os.ErrNotExist)
	if repository != "" && rootErr != nil && !rootMissing {
		return result, rootErr
	}
	if !rootMissing && repository != "" {
		result.LogicalBytes = root.Current.LogicalBytes
		result.AllocatedState = root.Current.AllocatedState
		result.AllocatedBytes = root.Current.AllocatedBytes
		if root.Prior != nil {
			result.LogicalBytes += root.Prior.LogicalBytes
			if result.AllocatedState != "exact" || root.Prior.AllocatedState != "exact" {
				result.AllocatedState = "unavailable"
				result.AllocatedBytes = 0
			} else {
				result.AllocatedBytes += root.Prior.AllocatedBytes
			}
		}
	} else {
		result.AllocatedState = "unavailable"
	}
	marker, markerErr := readSearchGenerationMarker(indexDir, repository)
	if repository != "" && markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return result, markerErr
	}
	if len(collecting) > 0 {
		sort.Slice(collecting, func(i, j int) bool { return collecting[i].name < collecting[j].name })
		result.More = result.More || len(collecting) > 1
		for _, candidate := range collecting {
			result.Scanned++
			if _, err := readSearchLifecycleReceipt(
				filepath.Join(repositoryDirectory, candidate.name), result.Cursor, candidate.digest,
			); err != nil {
				return result, err
			}
			if pins.Pinned(repository, candidate.digest) {
				continue
			}
			deleted, complete, err := deleteSearchGenerationStep(
				filepath.Join(repositoryDirectory, candidate.name), deleteLimit,
			)
			result.Deleted = deleted
			result.More = result.More || !complete
			return result, err
		}
		return result, nil
	}
	if len(generations) > MaxSearchRepositoryGenerations {
		return result, errors.New("search lifecycle generation count exceeds policy")
	}
	sort.Slice(generations, func(i, j int) bool {
		if !generations[i].modified.Equal(generations[j].modified) {
			return generations[i].modified.After(generations[j].modified)
		}
		return generations[i].name > generations[j].name
	})
	for _, candidate := range generations {
		if !rootMissing && (root.Current.GenerationDigest == candidate.digest ||
			(root.Prior != nil && root.Prior.GenerationDigest == candidate.digest)) {
			continue
		}
		if markerErr == nil && (marker.Candidate.GenerationDigest == candidate.digest ||
			(marker.Previous != nil && (marker.Previous.Current.GenerationDigest == candidate.digest ||
				marker.Previous.Prior != nil && marker.Previous.Prior.GenerationDigest == candidate.digest))) {
			continue
		}
		result.Scanned++
		if !rootMissing && now.Sub(candidate.modified) < SearchGenerationMaxAge && len(generations) <= 2 {
			continue
		}
		if pins.Pinned(repository, candidate.digest) {
			continue
		}
		if _, err := readSearchLifecycleReceipt(
			filepath.Join(repositoryDirectory, candidate.name), result.Cursor, candidate.digest,
		); err != nil {
			return result, err
		}
		if err := searchGenerationLifecycleFence(
			indexDir, repository, candidate.digest, pins,
		); err != nil {
			if errors.Is(err, errSearchGenerationPinned) {
				continue
			}
			return result, err
		}
		collectingName := "collecting-" + candidate.name
		collectingPath := filepath.Join(repositoryDirectory, collectingName)
		if err := os.Rename(filepath.Join(repositoryDirectory, candidate.name), collectingPath); err != nil {
			return result, err
		}
		if err := syncDirectory(repositoryDirectory); err != nil {
			return result, err
		}
		deleted, complete, err := deleteSearchGenerationStep(collectingPath, deleteLimit)
		result.Deleted = deleted
		result.More = result.More || !complete
		return result, err
	}
	return result, nil
}

var errSearchGenerationPinned = errors.New("search generation is pinned")

func readSearchLifecycleReceipt(
	directory, repositoryHash, generation string,
) (SearchGenerationReceipt, error) {
	var envelope SearchGenerationReceipt
	if err := readControlFile(
		filepath.Join(directory, searchGenerationReceiptName), &envelope,
	); err != nil {
		return SearchGenerationReceipt{}, err
	}
	if envelope.Repository == "" || repositoryKey(envelope.Repository) != repositoryHash {
		return SearchGenerationReceipt{}, errors.New("search lifecycle receipt repository mismatch")
	}
	receipt, err := readSearchGenerationReceipt(directory, envelope.Repository)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	if receipt.SearchDigest != generation {
		return SearchGenerationReceipt{}, errors.New("search lifecycle generation identity mismatch")
	}
	return receipt, nil
}

func searchGenerationLifecycleFence(
	indexDir, repository, generation string, pins SearchGenerationPinChecker,
) error {
	root, err := ReadSearchGenerationRoot(indexDir, repository)
	if err == nil && (root.Current.GenerationDigest == generation ||
		(root.Prior != nil && root.Prior.GenerationDigest == generation)) {
		return errSearchGenerationPinned
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	marker, err := readSearchGenerationMarker(indexDir, repository)
	if err == nil && (marker.Candidate.GenerationDigest == generation ||
		(marker.Previous != nil && (marker.Previous.Current.GenerationDigest == generation ||
			marker.Previous.Prior != nil && marker.Previous.Prior.GenerationDigest == generation))) {
		return errSearchGenerationPinned
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if pins.Pinned(repository, generation) {
		return errSearchGenerationPinned
	}
	return nil
}

func deleteSearchGenerationStep(directory string, budget int) (deleted int, complete bool, err error) {
	if budget < 1 {
		return 0, false, errors.New("invalid search generation deletion budget")
	}
	file, err := os.Open(directory)
	if err != nil {
		return 0, false, err
	}
	entries, readErr := file.ReadDir(budget + 2)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return 0, false, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return 0, false, closeErr
	}
	more := len(entries) == budget+2 && readErr == nil
	sort.Slice(entries, func(i, j int) bool {
		leftControl := searchGenerationControlDeletePriority(entries[i].Name())
		rightControl := searchGenerationControlDeletePriority(entries[j].Name())
		if leftControl != rightControl {
			return leftControl < rightControl
		}
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if deleted == budget {
			return deleted, false, nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!validSearchGenerationEntry(entry.Name()) {
			return deleted, false, errors.New("search lifecycle generation contains a special or unknown entry")
		}
		if more && entry.Name() == searchGenerationReceiptName {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		before, err := os.Lstat(path)
		if err != nil || !before.Mode().IsRegular() {
			return deleted, false, errors.New("search lifecycle entry changed before removal")
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(before, current) || !current.Mode().IsRegular() {
			return deleted, false, errors.New("search lifecycle entry identity changed before removal")
		}
		if err := os.Remove(path); err != nil {
			return deleted, false, err
		}
		deleted++
	}
	if more || deleted == budget {
		return deleted, false, nil
	}
	if err := os.Remove(directory); err != nil {
		return deleted, false, err
	}
	return deleted + 1, true, nil
}

func validSearchGenerationEntry(name string) bool {
	return name == searchGenerationReceiptName ||
		strings.HasPrefix(name, "phebs-source-") && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".manifest.json")) ||
		strings.HasPrefix(name, "phebs-whole-") && (strings.HasSuffix(name, ".zoekt") || strings.HasSuffix(name, ".manifest.json")) ||
		strings.HasPrefix(name, "phebs-search-") && strings.HasSuffix(name, ".manifest.json")
}

func searchGenerationControlDeletePriority(name string) int {
	switch {
	case strings.HasSuffix(name, ".zoekt"), strings.HasSuffix(name, ".jsonl"):
		return 0
	case name == searchGenerationReceiptName:
		return 2
	default:
		return 1
	}
}

func boundedSearchDirectory(path string, limit int) ([]os.DirEntry, error) {
	if limit < 1 {
		return nil, errors.New("invalid bounded search directory limit")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("search lifecycle path is not a real directory")
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(limit + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > limit {
		return nil, errors.New("search lifecycle directory exceeds policy")
	}
	return entries, nil
}
