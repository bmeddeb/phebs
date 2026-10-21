package observationpublication

import (
	"context"
	"errors"
	"slices"
	"sync"
)

const maxIdleInventoryRootsV2 = 8

type inventoryCacheEntryV2 struct {
	publication *InventoryPublicationV2
	repository  string
	generation  string
	leases      int
	ready       chan struct{}
	err         error
}

// InventoryCacheV2 validates an immutable stage once per exact root digest.
// Warm leases perform no segment, member, or observation reread; T40.6 may use
// Pinned when it gives these non-authoritative stages lifecycle ownership.
// The eight most recently released roots stay warm; live/provisional leases
// are never evicted and do not count toward that idle bound.
type InventoryCacheV2 struct {
	mu       sync.Mutex
	entries  map[string]*inventoryCacheEntryV2
	idle     []string // Oldest first; at most maxIdleInventoryRootsV2.
	retiring map[string]bool
}

type InventoryLeaseV2 struct {
	cache *InventoryCacheV2
	key   string
	entry *inventoryCacheEntryV2
	once  sync.Once
}

func (cache *InventoryCacheV2) Acquire(
	ctx context.Context, directory string, expected InventoryRootV2,
) (*InventoryLeaseV2, error) {
	if cache == nil || ValidateInventoryRootV2(expected) != nil {
		return nil, invalid("inventory v2 cache input")
	}
	key := directory + "\x00" + expected.Digest
	cache.mu.Lock()
	if cache.retiring[expected.Repository+"\x00"+expected.GenerationDigest] {
		cache.mu.Unlock()
		return nil, ErrStale
	}
	if cache.entries == nil {
		cache.entries = make(map[string]*inventoryCacheEntryV2)
	}
	if entry := cache.entries[key]; entry != nil {
		cache.removeIdleLocked(key)
		entry.leases++
		lease := &InventoryLeaseV2{cache: cache, key: key, entry: entry}
		ready := entry.ready
		cache.mu.Unlock()
		if ready != nil {
			select {
			case <-ready:
			case <-ctx.Done():
				lease.Release()
				return nil, ctx.Err()
			}
		}
		if entry.err != nil || entry.publication == nil {
			lease.Release()
			return nil, errors.Join(entry.err, invalid("inventory v2 cache open"))
		}
		return lease, nil
	}
	entry := &inventoryCacheEntryV2{
		repository: expected.Repository, generation: expected.GenerationDigest,
		leases: 1, ready: make(chan struct{}),
	}
	cache.entries[key] = entry
	cache.mu.Unlock()

	publication, err := OpenInventoryV2(ctx, directory, expected)
	cache.mu.Lock()
	entry.publication, entry.err = publication, err
	close(entry.ready)
	entry.ready = nil
	if err != nil && cache.entries[key] == entry {
		delete(cache.entries, key)
	}
	cache.mu.Unlock()
	if err != nil {
		(&InventoryLeaseV2{cache: cache, key: key, entry: entry}).Release()
		return nil, err
	}
	return &InventoryLeaseV2{cache: cache, key: key, entry: entry}, nil
}

func (lease *InventoryLeaseV2) Publication() *InventoryPublicationV2 {
	if lease == nil || lease.entry == nil {
		return nil
	}
	return lease.entry.publication
}

func (lease *InventoryLeaseV2) Release() {
	if lease == nil || lease.cache == nil || lease.entry == nil {
		return
	}
	lease.once.Do(func() {
		lease.cache.mu.Lock()
		if lease.entry.leases > 0 {
			lease.entry.leases--
		}
		if lease.entry.leases == 0 && lease.entry.ready == nil &&
			lease.entry.err == nil && lease.entry.publication != nil &&
			lease.cache.entries[lease.key] == lease.entry {
			if len(lease.cache.idle) == maxIdleInventoryRootsV2 {
				delete(lease.cache.entries, lease.cache.idle[0])
				lease.cache.idle = slices.Delete(lease.cache.idle, 0, 1)
			}
			lease.cache.idle = append(lease.cache.idle, lease.key)
		}
		lease.cache.mu.Unlock()
	})
}

func (cache *InventoryCacheV2) removeIdleLocked(key string) {
	if index := slices.Index(cache.idle, key); index >= 0 {
		cache.idle = slices.Delete(cache.idle, index, index+1)
	}
}

// Exclude new leases through the retirement rename without holding mu over I/O.
func (cache *InventoryCacheV2) beginRetireGeneration(repository, generation string) (func(), bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	identity := repository + "\x00" + generation
	if cache.retiring[identity] {
		return nil, false
	}
	for _, entry := range cache.entries {
		if entry.repository == repository && entry.generation == generation &&
			(entry.leases > 0 || entry.ready != nil) {
			return nil, false
		}
	}
	for key, entry := range cache.entries {
		if entry.repository == repository && entry.generation == generation {
			delete(cache.entries, key)
			cache.removeIdleLocked(key)
		}
	}
	if cache.retiring == nil {
		cache.retiring = make(map[string]bool)
	}
	cache.retiring[identity] = true
	return func() {
		cache.mu.Lock()
		delete(cache.retiring, identity)
		cache.mu.Unlock()
	}, true
}

func (cache *InventoryCacheV2) Pinned(directory, digestValue string) bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.entries[directory+"\x00"+digestValue]
	return entry != nil && entry.leases > 0
}

// PinnedGeneration is the lifecycle-facing identity lookup. Cache keys remain
// exact directory/root-digest pairs, but lifecycle authority is the bound
// repository/generation pair stored inside the validated publication.
func (cache *InventoryCacheV2) PinnedGeneration(repository, generation string) bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for _, entry := range cache.entries {
		if entry == nil || entry.leases < 1 {
			continue
		}
		if entry.repository == repository && entry.generation == generation {
			return true
		}
	}
	return false
}
