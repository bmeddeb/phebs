// Package repowork serializes operations that mutate or consume a bare mirror.
package repowork

import (
	"path/filepath"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var locks sync.Map

// CanonicalKey normalizes case and Unicode composition for managed artifact
// identity. APFS can resolve NFC/NFD spellings to one inode even though Go
// strings differ, so every lock and collision check must share this form.
func CanonicalKey(key string) string {
	return norm.NFC.String(cases.Fold().String(filepath.Clean(key)))
}

// Lock acquires the process-wide lock for key and returns its unlock function.
// Callers use the canonical bare-repository directory as the key.
func Lock(key string) func() {
	// Mirror identity is case-insensitive in the persisted layout audit. Use
	// the same key here so two differently-cased names cannot mutate one path
	// concurrently on a case-insensitive filesystem.
	key = CanonicalKey(key)
	mu, _ := locks.LoadOrStore(key, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}
