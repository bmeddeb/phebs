//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
)

// Real collecting-residue filesystem drains, selected controller/cursor SDK,
// authenticated FD6/quiescent workspace sampling and joined LC/WB output. The
// other fourteen owners are explicit no-ops. Shard/object contents are inert;
// this does not build a corpus or establish current/pin publication semantics.
func TestT422WorkspaceCleanupNativeComposition(t *testing.T) {
	started := time.Now()
	testT422ArchiveRetiredNativeEndpoint(t, false, true, true)
	t.Logf("actual cleanup: objects=%d shards=%d deleted_units=%d turns=%d cursor_writes=%d workspace_samples=%d elapsed=%s",
		t422CleanupObservationFiles, t422CleanupSearchFiles, t422CleanupExpectedDeleted,
		t422CleanupExpectedTurns, 2*t422CleanupExpectedTurns, t422CleanupExpectedTurns+2, time.Since(started))
}

type t422CleanupCountOwner struct {
	lifecycle.Owner
	turns *atomic.Uint64
}

func (owner t422CleanupCountOwner) Sweep(ctx context.Context, now time.Time, cursor string, limits lifecycle.Limits) lifecycle.OwnerResult {
	owner.turns.Add(1)
	return owner.Owner.Sweep(ctx, now, cursor, limits)
}

func seedT422CleanupNativeOwners(t *testing.T, root string, turns *atomic.Uint64) ([]lifecycle.Owner, func()) {
	t.Helper()
	observationRoot := filepath.Join(root, "data", "observations")
	collecting := filepath.Join(observationRoot, strings.Repeat("1", 64), observationpublication.InventoryPublicationDirectoryV2,
		"collecting-stage-"+strings.Repeat("2", 32))
	objects := filepath.Join(collecting, observationpublication.InventoryPublicationInventoryNameV2, "segment-00000", "objects")
	indexRoot := filepath.Join(root, "data", "index")
	searchStage := filepath.Join(focusedindex.SearchGenerationRootDirectory(indexRoot), strings.Repeat("3", 64), ".stage-prior-owner-cleanup")
	for _, directory := range []string{objects, searchStage} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for index := range t422CleanupObservationFiles {
		path := filepath.Join(objects, fmt.Sprintf("%016x-%064x.json", index, index))
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for index := range t422CleanupSearchFiles {
		path := filepath.Join(searchStage, fmt.Sprintf("phebs-whole-%04d.zoekt", index))
		if err := os.WriteFile(path, []byte("inert cleanup fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	acquire := func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireMutationLock(ctx, indexRoot)
	}
	owners := []lifecycle.Owner{
		lifecycle.ObservationInventoryOwnerV2{Root: observationRoot, Acquire: acquire, Pins: observationpublication.InventoryPinsV2{Cache: &observationpublication.InventoryCacheV2{}}},
		lifecycle.SearchGenerationOwnerImpl{IndexDir: indexRoot, Acquire: acquire, Pins: &focusedindex.SearchGenerationPins{}},
	}
	for index := range 14 {
		owners = append(owners, lifecycle.StaticOwner{OwnerName: fmt.Sprintf("test-noop-%02d", index), Completeness: lifecycle.Exact})
	}
	for index, owner := range owners {
		owners[index] = t422CleanupCountOwner{Owner: owner, turns: turns}
	}
	return owners, func() {
		for _, path := range []string{collecting, searchStage} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("collecting residue survived: %s: %v", path, err)
			}
		}
	}
}
