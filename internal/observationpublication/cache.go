package observationpublication

import (
	"context"
	"errors"
	"sync"
)

type cacheEntry struct {
	publication *Publication
	generation  string
	leases      int
	retired     bool
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	retired []*cacheEntry
}

type Lease struct {
	cache      *Cache
	repository string
	entry      *cacheEntry
	once       sync.Once
}

func (cache *Cache) Acquire(ctx context.Context, root, repository string) (*Lease, error) {
	if cache == nil {
		return nil, errors.New("observation cache is nil")
	}
	pointer, err := readPointer(root, repository)
	if err != nil {
		return nil, err
	}
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntry)
	}
	if entry := cache.entries[repository]; entry != nil && !entry.retired &&
		entry.generation == pointer.GenerationDigest && entry.publication != nil &&
		entry.publication.manifest.Digest == pointer.ManifestDigest {
		entry.leases++
		cache.mu.Unlock()
		return &Lease{cache: cache, repository: repository, entry: entry}, nil
	}
	cache.mu.Unlock()

	publication, err := Open(ctx, root, repository)
	if err != nil {
		return nil, err
	}
	generation := publication.manifest.GenerationDigest
	entry := &cacheEntry{publication: publication, generation: generation, leases: 1}
	cache.mu.Lock()
	if current := cache.entries[repository]; current != nil {
		if !current.retired && current.generation == generation {
			current.leases++
			cache.mu.Unlock()
			return &Lease{cache: cache, repository: repository, entry: current}, nil
		}
		current.retired = true
		if current.leases > 0 {
			cache.retired = append(cache.retired, current)
		}
	}
	cache.entries[repository] = entry
	cache.mu.Unlock()
	return &Lease{cache: cache, repository: repository, entry: entry}, nil
}

func (lease *Lease) Publication() *Publication {
	if lease == nil || lease.entry == nil {
		return nil
	}
	return lease.entry.publication
}

func (lease *Lease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		lease.cache.mu.Lock()
		if lease.entry.leases > 0 {
			lease.entry.leases--
		}
		if lease.entry.retired && lease.entry.leases == 0 {
			for index, entry := range lease.cache.retired {
				if entry == lease.entry {
					lease.cache.retired = append(lease.cache.retired[:index], lease.cache.retired[index+1:]...)
					break
				}
			}
		}
		lease.cache.mu.Unlock()
	})
}

func (cache *Cache) Pinned(repository, generation string) bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for _, entry := range cache.entries {
		if entry.publication != nil && entry.publication.manifest.Repository == repository &&
			entry.generation == generation && entry.leases > 0 {
			return true
		}
	}
	for _, entry := range cache.retired {
		if entry.publication != nil && entry.publication.manifest.Repository == repository &&
			entry.generation == generation && entry.leases > 0 {
			return true
		}
	}
	return false
}

func (cache *Cache) Retire(repository string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	if entry := cache.entries[repository]; entry != nil {
		entry.retired = true
	}
	cache.mu.Unlock()
}
