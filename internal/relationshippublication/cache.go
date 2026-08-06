package relationshippublication

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type cacheEntry struct {
	publication *Publication
	generation  string
	leases      int
	retired     bool
	ready       chan struct{}
	openErr     error
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
	return cache.acquirePublication(repository, publication), nil
}

// AcquireGeneration pins an exact immutable historical generation. The entry
// is installed before filesystem access so a lifecycle sweep that starts
// afterward observes the pin; if a sweep already renamed the generation, the
// open fails and the provisional entry is removed.
func (cache *Cache) AcquireGeneration(
	ctx context.Context,
	root, repository, generation, rootDigest string,
) (*Lease, error) {
	if cache == nil {
		return nil, errors.New("relationship cache is nil")
	}
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntry)
	}
	key := repository + "\x00" + generation
	if current := cache.entries[key]; current != nil && !current.retired &&
		current.generation == generation {
		current.leases++
		lease := &Lease{cache: cache, repository: key, entry: current}
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
			return nil, errors.New("relationship generation cache open produced no publication")
		}
		return lease, nil
	}
	entry := &cacheEntry{generation: generation, leases: 1, ready: make(chan struct{})}
	cache.entries[key] = entry
	cache.mu.Unlock()

	publication, err := OpenGeneration(ctx, root, repository, generation, rootDigest)
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
		(&Lease{cache: cache, repository: key, entry: entry}).Release()
		return nil, err
	}
	return &Lease{cache: cache, repository: key, entry: entry}, nil
}

func (cache *Cache) acquirePublication(repository string, publication *Publication) *Lease {
	generation := publication.rootValue.GenerationDigest
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntry)
	}
	if current := cache.entries[repository]; current != nil && !current.retired &&
		current.generation == generation {
		current.leases++
		return &Lease{cache: cache, repository: repository, entry: current}
	}
	entry := &cacheEntry{publication: publication, generation: generation, leases: 1}
	if current := cache.entries[repository]; current != nil {
		current.retired = true
		if current.leases != 0 {
			cache.retired = append(cache.retired, current)
		}
	}
	cache.entries[repository] = entry
	return &Lease{cache: cache, repository: repository, entry: entry}
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
