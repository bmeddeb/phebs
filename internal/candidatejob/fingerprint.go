package candidatejob

import (
	"context"
	"sync"

	"github.com/bmeddeb/phebs/internal/candidate"
)

type controlFingerprintCache struct {
	mu      sync.RWMutex
	entries map[string]controlFingerprintEntry
}

type controlFingerprintEntry struct {
	state       candidate.State
	fingerprint *candidate.ControlFingerprint
}

func newControlFingerprintCache() *controlFingerprintCache {
	return &controlFingerprintCache{
		entries: make(map[string]controlFingerprintEntry),
	}
}

func (cache *controlFingerprintCache) matches(
	ctx context.Context,
	root string,
	state candidate.State,
) (known, unchanged bool, err error) {
	cache.mu.RLock()
	entry, known := cache.entries[state.Repository]
	cache.mu.RUnlock()
	if !known || entry.state != state {
		return false, false, nil
	}
	unchanged, err = entry.fingerprint.MatchesContext(ctx, root, state)
	return true, unchanged, err
}

func (cache *controlFingerprintCache) remember(
	state candidate.State,
	fingerprint *candidate.ControlFingerprint,
) {
	cache.mu.Lock()
	cache.entries[state.Repository] = controlFingerprintEntry{
		state: state, fingerprint: fingerprint,
	}
	cache.mu.Unlock()
}

func (cache *controlFingerprintCache) forget(repository string) {
	cache.mu.Lock()
	delete(cache.entries, repository)
	cache.mu.Unlock()
}
