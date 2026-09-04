package relationshippublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type cacheEntryV3 struct {
	publication *PublicationV3
	generation  string
	rootDigest  string
	leases      int
	retired     bool
	ready       chan struct{}
	openErr     error
}

type CacheV3 struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntryV3
	retired  []*cacheEntryV3
	retiring map[string]struct{}
	reserved map[string]int
}

type LeaseV3 struct {
	cache      *CacheV3
	repository string
	entry      *cacheEntryV3
	once       sync.Once
}

func (cache *CacheV3) Acquire(
	ctx context.Context,
	root, repository string,
) (*LeaseV3, error) {
	if cache == nil {
		return nil, errors.New("relationship v3 cache is nil")
	}
	pointer, err := ReadPointerV3(ctx, root, repository)
	if err != nil {
		return nil, err
	}
	releaseReservation, reserved := cache.reserveGeneration(repository, pointer.GenerationDigest)
	if !reserved {
		return nil, ErrNotFound
	}
	defer releaseReservation()

	lease, err := cache.acquireCurrentPointer(ctx, root, repository, pointer)
	if err != nil {
		return nil, err
	}
	confirmed, err := ReadPointerV3(ctx, root, repository)
	if err != nil {
		lease.Release()
		return nil, err
	}
	if confirmed != pointer {
		lease.Release()
		return nil, ErrPublishing
	}
	return lease, nil
}

func (cache *CacheV3) AcquireGeneration(
	ctx context.Context,
	root, repository, generation, rootDigest string,
) (*LeaseV3, error) {
	if cache == nil {
		return nil, errors.New("relationship v3 cache is nil")
	}
	key := repository + "\x00" + generation
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntryV3)
	}
	if _, retiring := cache.retiring[key]; retiring {
		cache.mu.Unlock()
		return nil, ErrNotFound
	}
	if current := cache.entries[key]; current != nil && !current.retired {
		if current.generation != generation || current.rootDigest != rootDigest {
			cache.mu.Unlock()
			return nil, fmt.Errorf("%w: relationship v3 cached generation identity", ErrInvalid)
		}
		current.leases++
		lease := &LeaseV3{cache: cache, repository: key, entry: current}
		ready := current.ready
		cache.mu.Unlock()
		if ready != nil {
			select {
			case <-ready:
			case <-ctx.Done():
				lease.Release()
				return nil, ctx.Err()
			}
		}
		if current.openErr != nil || current.publication == nil {
			lease.Release()
			if current.openErr != nil {
				return nil, current.openErr
			}
			return nil, errors.New("relationship v3 generation cache open produced no publication")
		}
		return lease, nil
	}
	entry := &cacheEntryV3{
		generation: generation, rootDigest: rootDigest, leases: 1, ready: make(chan struct{}),
	}
	cache.entries[key] = entry
	cache.mu.Unlock()

	publication, err := OpenGenerationV3(ctx, root, repository, generation, rootDigest)
	cache.mu.Lock()
	entry.publication = publication
	entry.openErr = err
	close(entry.ready)
	entry.ready = nil
	if err != nil {
		if cache.entries[key] == entry {
			delete(cache.entries, key)
		}
		entry.retired = true
	}
	cache.mu.Unlock()
	if err != nil {
		(&LeaseV3{cache: cache, repository: key, entry: entry}).Release()
		return nil, err
	}
	return &LeaseV3{cache: cache, repository: key, entry: entry}, nil
}

func (cache *CacheV3) acquirePublication(
	repository string,
	publication *PublicationV3,
) (*LeaseV3, error) {
	generation := publication.rootValue.GenerationDigest
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntryV3)
	}
	if _, retiring := cache.retiring[repository+"\x00"+generation]; retiring {
		return nil, ErrNotFound
	}
	if current := cache.entries[repository]; current != nil && !current.retired &&
		current.generation == generation {
		if current.rootDigest != publication.rootValue.Digest {
			return nil, fmt.Errorf("%w: relationship v3 cached current identity", ErrInvalid)
		}
		current.leases++
		return &LeaseV3{cache: cache, repository: repository, entry: current}, nil
	}
	entry := &cacheEntryV3{
		publication: publication, generation: generation,
		rootDigest: publication.rootValue.Digest, leases: 1,
	}
	if current := cache.entries[repository]; current != nil {
		current.retired = true
		if current.leases != 0 {
			cache.retired = append(cache.retired, current)
		}
	}
	cache.entries[repository] = entry
	return &LeaseV3{cache: cache, repository: repository, entry: entry}, nil
}

func (cache *CacheV3) acquireCurrentPointer(
	ctx context.Context,
	root, repository string,
	pointer PointerV3,
) (*LeaseV3, error) {
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntryV3)
	}
	if current := cache.entries[repository]; current != nil && !current.retired &&
		current.generation == pointer.GenerationDigest {
		if current.rootDigest != pointer.RootDigest {
			cache.mu.Unlock()
			return nil, fmt.Errorf("%w: relationship v3 cached current identity", ErrInvalid)
		}
		current.leases++
		lease := &LeaseV3{cache: cache, repository: repository, entry: current}
		cache.mu.Unlock()
		return lease, nil
	}
	cache.mu.Unlock()

	publication, err := OpenGenerationV3(
		ctx, root, repository, pointer.GenerationDigest, pointer.RootDigest,
	)
	if err != nil {
		return nil, err
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		return nil, err
	}
	pointerRaw, err := json.Marshal(pointer)
	if err != nil {
		return nil, err
	}
	publication.base = base
	publication.pointer = pointer
	publication.pointerRaw = pointerRaw
	return cache.acquirePublication(repository, publication)
}

func (cache *CacheV3) reserveGeneration(
	repository, generation string,
) (func(), bool) {
	key := repository + "\x00" + generation
	cache.mu.Lock()
	if _, retiring := cache.retiring[key]; retiring {
		cache.mu.Unlock()
		return nil, false
	}
	if cache.reserved == nil {
		cache.reserved = make(map[string]int)
	}
	cache.reserved[key]++
	cache.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			cache.mu.Lock()
			cache.reserved[key]--
			if cache.reserved[key] == 0 {
				delete(cache.reserved, key)
			}
			cache.mu.Unlock()
		})
	}, true
}

func (lease *LeaseV3) Publication() *PublicationV3 {
	if lease == nil || lease.entry == nil {
		return nil
	}
	return lease.entry.publication
}

// ReadCurrentSemanticSnapshot acquires and holds the exact current generation
// while validating every immutable member, then confirms the pointer again
// before exposing the completed snapshot.
func (cache *CacheV3) ReadCurrentSemanticSnapshot(
	ctx context.Context,
	root, repository string,
) (SemanticSnapshotV3, error) {
	lease, err := cache.Acquire(ctx, root, repository)
	if err != nil {
		return SemanticSnapshotV3{}, err
	}
	defer lease.Release()
	publication := lease.Publication()
	if publication == nil {
		return SemanticSnapshotV3{}, fmt.Errorf("%w: current semantic snapshot lease", ErrInvalid)
	}
	return publication.readCurrentSemanticSnapshotV3(ctx)
}

// ConfirmCurrentSemanticSnapshot is the final control-only fence for a
// completely validated semantic snapshot. It rechecks only the mutable
// current pointer; immutable generation members are not opened or decoded a
// second time.
func (cache *CacheV3) ConfirmCurrentSemanticSnapshot(
	ctx context.Context,
	root, repository string,
	snapshot SemanticSnapshotV3,
) error {
	if cache == nil {
		return errors.New("relationship v3 cache is nil")
	}
	if ctx == nil || ValidateRootV3(snapshot.Root) != nil ||
		snapshot.Root.Authority.Repository != repository ||
		len(snapshot.Projections) != snapshot.Root.ProjectionCount {
		return fmt.Errorf("%w: current semantic snapshot confirmation", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pointer, err := ReadPointerV3(ctx, root, repository)
	if err != nil {
		return err
	}
	if pointer.GenerationDigest != snapshot.Root.GenerationDigest ||
		pointer.RootDigest != snapshot.Root.Digest {
		return ErrPublishing
	}
	return nil
}

func (lease *LeaseV3) Release() {
	if lease == nil || lease.cache == nil || lease.entry == nil {
		return
	}
	lease.once.Do(func() {
		lease.cache.mu.Lock()
		defer lease.cache.mu.Unlock()
		if lease.entry.leases > 0 {
			lease.entry.leases--
		}
		if lease.entry.leases == 0 && strings.ContainsRune(lease.repository, '\x00') {
			if lease.cache.entries[lease.repository] == lease.entry {
				delete(lease.cache.entries, lease.repository)
			}
			lease.entry.retired = true
		}
		if lease.entry.retired && lease.entry.leases == 0 {
			for index, entry := range lease.cache.retired {
				if entry == lease.entry {
					lease.cache.retired = append(
						lease.cache.retired[:index], lease.cache.retired[index+1:]...,
					)
					break
				}
			}
		}
	})
}

func (cache *CacheV3) Pinned(repository, generation string) bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.pinnedLocked(repository, generation)
}

func (cache *CacheV3) BeginRetire(repository, generation string) (func(), bool) {
	if cache == nil {
		return nil, false
	}
	key := repository + "\x00" + generation
	cache.mu.Lock()
	if cache.pinnedLocked(repository, generation) {
		cache.mu.Unlock()
		return nil, false
	}
	if cache.retiring == nil {
		cache.retiring = make(map[string]struct{})
	}
	if _, present := cache.retiring[key]; present {
		cache.mu.Unlock()
		return nil, false
	}
	cache.retiring[key] = struct{}{}
	cache.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			cache.mu.Lock()
			delete(cache.retiring, key)
			cache.mu.Unlock()
		})
	}, true
}

func (cache *CacheV3) pinnedLocked(repository, generation string) bool {
	if cache.reserved[repository+"\x00"+generation] != 0 {
		return true
	}
	if entry := cache.entries[repository]; entry != nil &&
		entry.generation == generation && entry.leases != 0 {
		return true
	}
	if entry := cache.entries[repository+"\x00"+generation]; entry != nil &&
		entry.generation == generation && entry.leases != 0 {
		return true
	}
	for _, entry := range cache.retired {
		if entry.generation == generation && entry.leases != 0 {
			return true
		}
	}
	return false
}
