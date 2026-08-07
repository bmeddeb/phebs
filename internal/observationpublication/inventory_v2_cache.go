package observationpublication

import (
	"context"
	"errors"
	"sync"
)

type inventoryCacheEntryV2 struct {
	publication *InventoryPublicationV2
	leases      int
	ready       chan struct{}
	err         error
}

// InventoryCacheV2 validates an immutable stage once per exact root digest.
// Warm leases perform no segment, member, or observation reread; T40.6 may use
// Pinned when it gives these non-authoritative stages lifecycle ownership.
type InventoryCacheV2 struct {
	mu      sync.Mutex
	entries map[string]*inventoryCacheEntryV2
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
	if cache == nil || !validDigest(expected.Digest) {
		return nil, invalid("inventory v2 cache input")
	}
	key := directory + "\x00" + expected.Digest
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*inventoryCacheEntryV2)
	}
	if entry := cache.entries[key]; entry != nil {
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
	entry := &inventoryCacheEntryV2{leases: 1, ready: make(chan struct{})}
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
		lease.cache.mu.Unlock()
	})
}

func (cache *InventoryCacheV2) Pinned(directory, digestValue string) bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.entries[directory+"\x00"+digestValue]
	return entry != nil && entry.publication != nil && entry.leases > 0
}
