package observationpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func inventoryCacheFixtureV2(t *testing.T) (string, InventoryRootV2) {
	t.Helper()
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-cache-v2")
	directory := t.TempDir()
	root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory: directory, RepositoryDirectory: repository, Plan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	return directory, root
}

func TestInventoryV2CacheBoundsIdleGenerationsAndKeepsLiveAndWarmRoots(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	cache := &InventoryCacheV2{}
	var live *InventoryLeaseV2
	var latestDirectory string
	var latestRoot InventoryRootV2
	var latestPublication *InventoryPublicationV2
	var oldestIdleDirectory string
	var oldestIdleRoot InventoryRootV2
	var reusedDirectory string
	var reusedRoot InventoryRootV2
	var reusedPublication *InventoryPublicationV2
	for index := range maxIdleInventoryRootsV2 + 4 {
		if index > 0 {
			if err := os.WriteFile(filepath.Join(repository, "a.go"),
				fmt.Appendf(nil, "package demo\nconst A = %d\n", index+1), 0o600); err != nil {
				t.Fatal(err)
			}
			runObservationGit(t, repository, "add", "a.go")
			runObservationGit(t, repository, "commit", "-m", fmt.Sprint(index))
			commit = strings.TrimSpace(runObservationGit(t, repository, "rev-parse", "HEAD"))
		}
		_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/inventory-cache-history-v2")
		directory := t.TempDir()
		root, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
			OutputDirectory: directory, RepositoryDirectory: repository, Plan: plan,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := cache.Acquire(t.Context(), directory, root)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			live = lease
			defer live.Release()
			continue
		}
		latestDirectory, latestRoot, latestPublication = directory, root, lease.Publication()
		if index == 1 {
			oldestIdleDirectory, oldestIdleRoot = directory, root
		}
		if index == 2 {
			reusedDirectory, reusedRoot, reusedPublication = directory, root, lease.Publication()
		}
		if index == maxIdleInventoryRootsV2 {
			warm, err := cache.Acquire(t.Context(), reusedDirectory, reusedRoot)
			if err != nil || warm.Publication() != reusedPublication {
				t.Fatalf("idle root did not reuse its validated publication: %v", err)
			}
			warm.Release()
		}
		lease.Release()
		lease.Release() // Release remains idempotent, including the idle queue.
		if len(cache.idle) > maxIdleInventoryRootsV2 || len(cache.entries) > maxIdleInventoryRootsV2+1 {
			t.Fatalf("history retained %d idle / %d total roots", len(cache.idle), len(cache.entries))
		}
	}
	if !cache.PinnedGeneration(live.entry.repository, live.entry.generation) || cache.entries[live.key] != live.entry {
		t.Fatal("idle eviction lost an active lease")
	}
	if cache.entries[oldestIdleDirectory+"\x00"+oldestIdleRoot.Digest] != nil {
		t.Fatal("oldest idle generation was not evicted")
	}
	if entry := cache.entries[reusedDirectory+"\x00"+reusedRoot.Digest]; entry == nil || entry.publication != reusedPublication {
		t.Fatal("recent reuse did not refresh idle eviction order")
	}
	if err := os.WriteFile(filepath.Join(oldestIdleDirectory, oldestIdleRoot.Segments[0].Directory, inventoryMemberNameV2(0)), []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Acquire(t.Context(), oldestIdleDirectory, oldestIdleRoot); err == nil {
		t.Fatal("evicted generation bypassed cold validation")
	}
	// The most recently used current root remains warm without a member reread.
	member := filepath.Join(latestDirectory, latestRoot.Segments[0].Directory, inventoryMemberNameV2(0))
	if err := os.WriteFile(member, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	warm, err := cache.Acquire(t.Context(), latestDirectory, latestRoot)
	if err != nil || warm.Publication() != latestPublication {
		t.Fatalf("recent root lost warm reuse: %v", err)
	}
	warm.Release()
	if _, err := (&InventoryCacheV2{}).Acquire(t.Context(), latestDirectory, latestRoot); err == nil {
		t.Fatal("control cold cache admitted corrupt bytes")
	}
	live.Release()
	if len(cache.idle) != maxIdleInventoryRootsV2 || len(cache.entries) != maxIdleInventoryRootsV2 {
		t.Fatal("last live release did not enforce the idle bound")
	}
}

func TestInventoryV2CacheRetirementFencesLeasesAndClearsFailedRename(t *testing.T) {
	directory, root := inventoryCacheFixtureV2(t)
	key := directory + "\x00" + root.Digest
	entry := &inventoryCacheEntryV2{repository: root.Repository, generation: root.GenerationDigest, ready: make(chan struct{})}
	cache := &InventoryCacheV2{entries: map[string]*inventoryCacheEntryV2{key: entry}}
	if release, ok := cache.beginRetireGeneration(root.Repository, root.GenerationDigest); ok || release != nil {
		t.Fatal("retirement evicted a provisional entry")
	}
	delete(cache.entries, key)
	lease, err := cache.Acquire(t.Context(), directory, root)
	if err != nil {
		t.Fatal(err)
	}
	if release, ok := cache.beginRetireGeneration(root.Repository, root.GenerationDigest); ok || release != nil {
		t.Fatal("retirement evicted a live lease")
	}
	lease.Release()
	release, ok := cache.beginRetireGeneration(root.Repository, root.GenerationDigest)
	if !ok {
		t.Fatal("idle root refused retirement")
	}
	if len(cache.entries) != 0 || len(cache.idle) != 0 {
		t.Fatal("retiring root remained cached")
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if acquired, err := cache.Acquire(t.Context(), directory, root); !errors.Is(err, ErrStale) || acquired != nil {
				t.Errorf("acquired root during retirement: %v", err)
			}
		})
	}
	workers.Wait()
	release()
	pins := InventoryPinsV2{Cache: cache}
	if _, err := renameInventoryGenerationV2(pins, root.Repository, root.GenerationDigest,
		filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "retired")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing retirement source was not reported", err)
	}
	lease, err = cache.Acquire(t.Context(), directory, root)
	if err != nil {
		t.Fatal("failed rename left a stale retirement fence", err)
	}
	lease.Release()
}

func TestInventoryV2CacheConcurrentWarmLeasesAndRetirement(t *testing.T) {
	directory, root := inventoryCacheFixtureV2(t)
	cache := &InventoryCacheV2{}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 40 {
				lease, err := cache.Acquire(t.Context(), directory, root)
				if errors.Is(err, ErrStale) {
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				if !cache.Pinned(directory, root.Digest) || lease.Publication().root.Digest != root.Digest {
					t.Error("live lease lost its root or pin")
				}
				lease.Release()
			}
		})
	}
	workers.Go(func() {
		for range 80 {
			if release, ok := cache.beginRetireGeneration(root.Repository, root.GenerationDigest); ok {
				release()
			}
		}
	})
	workers.Wait()
	if len(cache.entries) > 1 || len(cache.idle) > 1 || cache.PinnedGeneration(root.Repository, root.GenerationDigest) || len(cache.retiring) != 0 {
		t.Fatal("concurrent releases/retirement retained pins or duplicate idle roots")
	}
}

func TestInventoryV2CacheCanceledWaitPreservesProvisionalLease(t *testing.T) {
	directory, root := inventoryCacheFixtureV2(t)
	key := directory + "\x00" + root.Digest
	entry := &inventoryCacheEntryV2{repository: root.Repository, generation: root.GenerationDigest, leases: 1, ready: make(chan struct{})}
	cache := &InventoryCacheV2{entries: map[string]*inventoryCacheEntryV2{key: entry}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.Acquire(ctx, directory, root); !errors.Is(err, context.Canceled) {
		t.Fatal("provisional wait ignored cancellation", err)
	}
	if entry.leases != 1 || len(cache.idle) != 0 || !cache.PinnedGeneration(root.Repository, root.GenerationDigest) {
		t.Fatal("canceled waiter retired another opener's provisional pin")
	}
}
