package relationshippublication

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

// Acquire pins the exact current relationship root. Sparse service readers
// may share this bounded control open; selected members remain independently
// validated by Publication.OpenService.
func (cache *Cache) Acquire(ctx context.Context, root, repository string) (*Lease, error) {
	if cache == nil {
		return nil, errors.New("relationship cache is nil")
	}
	publication, err := OpenCurrent(ctx, root, repository)
	if err != nil {
		return nil, err
	}
	generation := publication.rootValue.GenerationDigest
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntry)
	}
	if current := cache.entries[repository]; current != nil && !current.retired &&
		current.generation == generation {
		current.leases++
		return &Lease{cache: cache, repository: repository, entry: current}, nil
	}
	entry := &cacheEntry{publication: publication, generation: generation, leases: 1}
	if current := cache.entries[repository]; current != nil {
		current.retired = true
		if current.leases != 0 {
			cache.retired = append(cache.retired, current)
		}
	}
	cache.entries[repository] = entry
	return &Lease{cache: cache, repository: repository, entry: entry}, nil
}

func (lease *Lease) Publication() *Publication {
	if lease == nil || lease.entry == nil {
		return nil
	}
	return lease.entry.publication
}

func (lease *Lease) Release() {
	if lease == nil || lease.cache == nil || lease.entry == nil {
		return
	}
	lease.once.Do(func() {
		lease.cache.mu.Lock()
		defer lease.cache.mu.Unlock()
		if lease.entry.leases > 0 {
			lease.entry.leases--
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

func (cache *Cache) Pinned(repository, generation string) bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry := cache.entries[repository]; entry != nil &&
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
