// Package repowork serializes operations that mutate or consume a bare mirror.
package repowork

import (
	"context"
	"path/filepath"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type lockEntry struct {
	token chan struct{}
	refs  int
}

var lockRegistry = struct {
	sync.Mutex
	entries map[string]*lockEntry
}{entries: make(map[string]*lockEntry)}

// CanonicalKey normalizes case and Unicode composition for managed artifact
// identity. APFS can resolve NFC/NFD spellings to one inode even though Go
// strings differ, so every lock and collision check must share this form.
func CanonicalKey(key string) string {
	return norm.NFC.String(cases.Fold().String(filepath.Clean(key)))
}

// Lock acquires the process-wide lock for key and returns its unlock function.
// Callers use the canonical bare-repository directory as the key.
func Lock(key string) func() {
	unlock, _ := LockContext(context.Background(), key)
	return unlock
}

// LockContext acquires the process-wide lock for key or returns when ctx is
// canceled. Its unlock function is idempotent.
func LockContext(ctx context.Context, key string) (func(), error) {
	// Mirror identity is case-insensitive in the persisted layout audit. Use
	// the same key here so two differently-cased names cannot mutate one path
	// concurrently on a case-insensitive filesystem.
	key = CanonicalKey(key)
	lockRegistry.Lock()
	entry := lockRegistry.entries[key]
	if entry == nil {
		entry = &lockEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		lockRegistry.entries[key] = entry
	}
	entry.refs++
	lockRegistry.Unlock()

	select {
	case <-ctx.Done():
		releaseEntry(key, entry)
		return nil, ctx.Err()
	case <-entry.token:
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			entry.token <- struct{}{}
			releaseEntry(key, entry)
		})
	}, nil
}

func releaseEntry(key string, entry *lockEntry) {
	lockRegistry.Lock()
	entry.refs--
	if entry.refs == 0 && lockRegistry.entries[key] == entry {
		delete(lockRegistry.entries, key)
	}
	lockRegistry.Unlock()
}
