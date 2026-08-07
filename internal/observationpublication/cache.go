package observationpublication

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

// AcquireGeneration pins and fully validates one exact historical
// generation. The provisional cache entry is visible before filesystem access
// so a lifecycle sweep that starts later observes the pin. A sweep that
// already renamed the generation makes the strict open fail instead.
func (cache *Cache) AcquireGeneration(
	ctx context.Context, root, repository, generation string,
) (*Lease, error) {
	if cache == nil {
		return nil, errors.New("observation cache is nil")
	}
	key := repository + "\x00" + generation
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*cacheEntry)
	}
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
			return nil, errors.New("observation generation cache open produced no publication")
		}
		return lease, nil
	}
	entry := &cacheEntry{generation: generation, leases: 1, ready: make(chan struct{})}
	cache.entries[key] = entry
	cache.mu.Unlock()

	publication, err := openGenerationDigest(ctx, root, repository, generation)
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
		if lease.entry.leases == 0 && strings.ContainsRune(lease.repository, '\x00') {
			if lease.cache.entries[lease.repository] == lease.entry {
				delete(lease.cache.entries, lease.repository)
			}
			lease.entry.retired = true
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
	if entry := cache.entries[repository+"\x00"+generation]; entry != nil &&
		entry.generation == generation && entry.leases > 0 {
		return true
	}
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
