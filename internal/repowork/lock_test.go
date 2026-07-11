package repowork

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCanonicalKeyNormalizesCaseAndUnicodeComposition(t *testing.T) {
	left := CanonicalKey("/data/CAF\u00c9/repo.git")
	right := CanonicalKey("/data/cafe\u0301/repo.git")
	if left != right {
		t.Fatalf("canonical keys differ: %q != %q", left, right)
	}
}

func TestCanonicalKeyConcurrent(t *testing.T) {
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if got := CanonicalKey("/data/CAF\u00c9/repo.git"); got != "/data/caf\u00e9/repo.git" {
					errs <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for got := range errs {
		t.Fatalf("concurrent canonical key = %q", got)
	}
}

func TestLockSerializesCanonicalAliasesAndReleasesRegistryEntry(t *testing.T) {
	firstUnlock := Lock("/data/CAF\u00c9/repo.git")
	acquired := make(chan func(), 1)
	started := make(chan struct{})
	go func() {
		close(started)
		acquired <- Lock("/data/cafe\u0301/repo.git")
	}()
	<-started

	deadline := time.Now().Add(time.Second)
	for {
		lockRegistry.Lock()
		entry := lockRegistry.entries[CanonicalKey("/data/CAF\u00c9/repo.git")]
		waiterRegistered := entry != nil && entry.refs == 2
		lockRegistry.Unlock()
		if waiterRegistered {
			break
		}
		if time.Now().After(deadline) {
			firstUnlock()
			t.Fatal("second lock attempt was not registered")
		}
		runtime.Gosched()
	}

	select {
	case unlock := <-acquired:
		unlock()
		firstUnlock()
		t.Fatal("canonical alias acquired while the first lock was held")
	default:
	}

	firstUnlock()
	secondUnlock := <-acquired
	secondUnlock()

	lockRegistry.Lock()
	defer lockRegistry.Unlock()
	if len(lockRegistry.entries) != 0 {
		t.Fatalf("idle lock registry has %d entries, want 0", len(lockRegistry.entries))
	}
}

func TestUnlockIsIdempotent(t *testing.T) {
	unlock := Lock("/data/repo.git")
	unlock()
	unlock()

	lockRegistry.Lock()
	defer lockRegistry.Unlock()
	if len(lockRegistry.entries) != 0 {
		t.Fatalf("idle lock registry has %d entries, want 0", len(lockRegistry.entries))
	}
}

func TestLockRegistryDoesNotRetainIdleKeys(t *testing.T) {
	for i := range 1_000 {
		unlock := Lock(fmt.Sprintf("/data/repo-%d.git", i))
		unlock()
	}

	lockRegistry.Lock()
	defer lockRegistry.Unlock()
	if len(lockRegistry.entries) != 0 {
		t.Fatalf("idle lock registry has %d entries, want 0", len(lockRegistry.entries))
	}
}

func TestLockContextCancellationReleasesWaiter(t *testing.T) {
	unlock := Lock("/data/repo.git")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LockContext(ctx, "/data/repo.git"); err != context.Canceled {
		unlock()
		t.Fatalf("LockContext error = %v, want context.Canceled", err)
	}
	unlock()

	lockRegistry.Lock()
	defer lockRegistry.Unlock()
	if len(lockRegistry.entries) != 0 {
		t.Fatalf("idle lock registry has %d entries, want 0", len(lockRegistry.entries))
	}
}
