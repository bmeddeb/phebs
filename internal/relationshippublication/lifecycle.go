package relationshippublication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
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

type lifecycleGeneration struct {
	name       string
	generation string
	modified   time.Time
}

// SweepLifecycle removes at most one unrooted relationship or orphaned
// component generation. The caller holds the shared publication/backup lock.
// Relationship roots are renamed out of authority before bounded draining;
// component generations are deleted only after no remaining root references
// them and no resolver pointer/marker protects them.
func SweepLifecycle(
	ctx context.Context,
	dataDir string,
	now time.Time,
	cursor string,
	pins PinChecker,
	deleteLimit int,
) (LifecycleResult, error) {
	var result LifecycleResult
	if !filepath.IsAbs(dataDir) || pins == nil || deleteLimit < 1 {
		return result, invalidLifecycle("lifecycle input")
	}
	root := filepath.Join(dataDir, "relationships")
	base := filepath.Join(root, "relationship-publications")
	entries, err := boundedLifecycleDirectory(base, MaxLifecycleRepositories)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var repositories []string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 64 && validLowerHex(entry.Name()) {
			repositories = append(repositories, entry.Name())
		} else {
			return result, invalidLifecycle("lifecycle repository inventory")
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
	repositoryDirectory := filepath.Join(base, result.Cursor)
	pointerRaw, err := readRegular(filepath.Join(repositoryDirectory, "current.json"), MaxRootBytes)
	if err != nil {
		return result, err
	}
	var pointer Pointer
	if decodeExact(pointerRaw, MaxRootBytes, &pointer) != nil ||
		validatePointer(pointer) != nil || repositoryHash(pointer.Repository) != result.Cursor {
		return result, invalidLifecycle("lifecycle pointer")
	}
	markerGeneration := ""
	if raw, markerErr := readRegular(filepath.Join(repositoryDirectory, "publishing.json"), MaxRootBytes); markerErr == nil {
		var marker Marker
		if decodeExact(raw, MaxRootBytes, &marker) != nil || validateMarker(marker) != nil ||
			marker.Pointer.Repository != pointer.Repository {
			return result, invalidLifecycle("lifecycle marker")
		}
		markerGeneration = marker.Pointer.GenerationDigest
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return result, markerErr
	}
	generations, collecting, err := lifecycleGenerations(repositoryDirectory, false)
	if err != nil {
		return result, err
	}
	if len(collecting) == 1 {
		result.Scanned = 1
		if pins.Pinned(pointer.Repository, collecting[0].generation) {
			return result, nil
		}
		deleted, complete, err := drainFlatGeneration(
			filepath.Join(repositoryDirectory, collecting[0].name), deleteLimit,
		)
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
		if pins.Pinned(pointer.Repository, generation.generation) {
			continue
		}
		if current, readErr := readCurrentPointer(root, pointer.Repository); readErr != nil ||
			current.GenerationDigest == generation.generation {
			if readErr != nil {
				return result, readErr
			}
			continue
		}
		name := "collecting-" + generation.name
		if err := os.Rename(
			filepath.Join(repositoryDirectory, generation.name),
			filepath.Join(repositoryDirectory, name),
		); err != nil {
			return result, err
		}
		deleted, complete, err := drainFlatGeneration(filepath.Join(repositoryDirectory, name), deleteLimit)
		result.Deleted = deleted
		result.More = result.More || !complete
		return result, err
	}

	references, err := componentReferences(repositoryDirectory, generations)
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

type componentReferenceSet struct {
	resolver map[string]struct{}
	rpc      map[string]struct{}
	kafka    map[string]struct{}
}

func componentReferences(
	repositoryDirectory string, generations []lifecycleGeneration,
) (componentReferenceSet, error) {
	result := componentReferenceSet{
		resolver: make(map[string]struct{}), rpc: make(map[string]struct{}),
		kafka: make(map[string]struct{}),
	}
	for _, generation := range generations {
		raw, err := readRegular(filepath.Join(repositoryDirectory, generation.name, "root.json"), MaxRootBytes)
		if err != nil {
			return result, err
		}
		var root Root
		if decodeExact(raw, MaxRootBytes, &root) != nil || validateRoot(root) != nil {
			return result, invalidLifecycle("lifecycle relationship root")
		}
		canonical, err := jsonMarshal(root)
		if err != nil || !bytes.Equal(raw, canonical) {
			return result, invalidLifecycle("lifecycle relationship root bytes")
		}
		result.resolver[root.Authority.ResolverGenerationDigest] = struct{}{}
		result.rpc[root.Authority.RPCGenerationDigest] = struct{}{}
		result.kafka[root.Authority.KafkaGenerationDigest] = struct{}{}
	}
	return result, nil
}

func sweepOrphanComponent(
	dataDir, repositoryHashValue string,
	references componentReferenceSet,
	deleteLimit int,
) (deleted int, more bool, scanned int, err error) {
	type component struct {
		base       string
		prefix     string
		references map[string]struct{}
		pointer    bool
	}
	components := []component{
		{
			base:   filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces", repositoryHashValue),
			prefix: "generation-", references: references.resolver, pointer: true,
		},
		{
			base:       filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings", repositoryHashValue),
			references: references.rpc,
		},
		{
			base:       filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings", repositoryHashValue),
			references: references.kafka,
		},
	}
	for _, current := range components {
		entries, readErr := boundedLifecycleDirectory(current.base, MaxRepositoryGenerations+3)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return 0, false, scanned, readErr
		}
		collectingCount := 0
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "collecting-") {
				continue
			}
			name := strings.TrimPrefix(entry.Name(), "collecting-")
			name = strings.TrimPrefix(name, current.prefix)
			if len(name) != 64 || !validLowerHex(name) {
				return 0, false, scanned, invalidLifecycle("collecting component")
			}
			collectingCount++
			if collectingCount > 1 {
				return 0, false, scanned, ErrLimit
			}
			deleted, complete, err := drainFlatGeneration(
				filepath.Join(current.base, entry.Name()), deleteLimit,
			)
			return deleted, !complete, scanned + 1, err
		}
		protected := make(map[string]struct{})
		if current.pointer {
			for _, name := range []string{"current.json", "publishing.json"} {
				raw, readErr := readRegular(filepath.Join(current.base, name), MaxRootBytes)
				if errors.Is(readErr, os.ErrNotExist) {
					continue
				}
				if readErr != nil {
					return 0, false, scanned, readErr
				}
				var control struct {
					GenerationDigest string `json:"generation_digest"`
					Pointer          struct {
						GenerationDigest string `json:"generation_digest"`
					} `json:"pointer"`
				}
				if json.Unmarshal(raw, &control) != nil {
					return 0, false, scanned, invalidLifecycle("component control")
				}
				generation := control.GenerationDigest
				if generation == "" {
					generation = control.Pointer.GenerationDigest
				}
				if !validDigest(generation) {
					return 0, false, scanned, invalidLifecycle("component control generation")
				}
				protected[generation] = struct{}{}
			}
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), "collecting-") {
				continue
			}
			name := strings.TrimPrefix(entry.Name(), current.prefix)
			if len(name) != 64 || !validLowerHex(name) {
				continue
			}
			generation := "sha256:" + name
			if _, live := current.references[generation]; live {
				continue
			}
			if _, live := protected[generation]; live {
				continue
			}
			scanned++
			collecting := filepath.Join(current.base, "collecting-"+entry.Name())
			if err := os.Rename(filepath.Join(current.base, entry.Name()), collecting); err != nil {
				return 0, false, scanned, err
			}
			deleted, complete, err := drainFlatGeneration(collecting, deleteLimit)
			return deleted, !complete, scanned, err
		}
	}
	return 0, false, scanned, nil
}

func lifecycleGenerations(
	directory string, prefixed bool,
) ([]lifecycleGeneration, []lifecycleGeneration, error) {
	entries, err := boundedLifecycleDirectory(directory, MaxRepositoryGenerations+3)
	if err != nil {
		return nil, nil, err
	}
	var generations, collecting []lifecycleGeneration
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "collecting-") {
			raw := strings.TrimPrefix(name, "collecting-")
			if prefixed {
				raw = strings.TrimPrefix(raw, "generation-")
			}
			if len(raw) == 64 && validLowerHex(raw) {
				info, err := entry.Info()
				if err != nil {
					return nil, nil, err
				}
				collecting = append(collecting, lifecycleGeneration{name: name, generation: "sha256:" + raw, modified: info.ModTime()})
			}
			continue
		}
		raw := name
		if prefixed {
			raw = strings.TrimPrefix(raw, "generation-")
		}
		if len(raw) == 64 && validLowerHex(raw) {
			info, err := entry.Info()
			if err != nil {
				return nil, nil, err
			}
			generations = append(generations, lifecycleGeneration{name: name, generation: "sha256:" + raw, modified: info.ModTime()})
		}
	}
	if len(generations) > MaxRepositoryGenerations || len(collecting) > 1 {
		return nil, nil, ErrLimit
	}
	return generations, collecting, nil
}

func drainFlatGeneration(directory string, budget int) (int, bool, error) {
	entries, err := boundedLifecycleDirectory(directory, budget+1)
	if err != nil {
		return 0, false, err
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return deleted, false, invalidLifecycle("lifecycle generation entry")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return deleted, false, invalidLifecycle("lifecycle generation file")
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

func boundedLifecycleDirectory(path string, limit int) ([]os.DirEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, invalidLifecycle("lifecycle directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		return nil, ErrLimit
	}
	return entries, nil
}

func validLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return value != ""
}

func repositoryHash(repository string) string {
	sum := sha256Sum([]byte(repository))
	return hexString(sum)
}

func readCurrentPointer(root, repository string) (Pointer, error) {
	var value Pointer
	raw, err := readRegular(
		filepath.Join(repositoryRoot(root, repository), "current.json"), MaxRootBytes,
	)
	if err != nil {
		return value, err
	}
	if decodeExact(raw, MaxRootBytes, &value) != nil || validatePointer(value) != nil ||
		value.Repository != repository {
		return Pointer{}, invalidLifecycle("current pointer")
	}
	return value, nil
}

func invalidLifecycle(label string) error { return errors.Join(ErrInvalid, errors.New(label)) }

func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }

func sha256Sum(raw []byte) [32]byte { return sha256.Sum256(raw) }

func hexString(raw [32]byte) string { return hex.EncodeToString(raw[:]) }
