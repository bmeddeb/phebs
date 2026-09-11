package observationpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInventorySelectedCleanupLargeBatch(t *testing.T) {
	directory := t.TempDir()
	seedInventoryCleanupObjects(t, directory, 1050)
	deleted, complete, err := deleteInventoryTreeStepV2Context(t.Context(), directory, 1024, "")
	if err != nil || deleted != 1024 || complete {
		t.Fatalf("first drain = %d/%v, %v", deleted, complete, err)
	}
	deleted, complete, err = deleteInventoryTreeStepV2Context(t.Context(), directory, 1024, "")
	if err != nil || deleted != 30 || !complete { // 26 files plus four directories.
		t.Fatalf("final drain = %d/%v, %v", deleted, complete, err)
	}
}

// Cancel at the next deletion boundary after the first actual file removal.
// This is deterministic and checks the returned prefix against the real disk.
type inventoryCleanupCancelContext struct {
	context.Context
	cancel context.CancelFunc
	first  string
}

func (ctx inventoryCleanupCancelContext) Err() error {
	if _, err := os.Lstat(ctx.first); errors.Is(err, os.ErrNotExist) {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestInventorySelectedCleanupCancellationRetainsPrefix(t *testing.T) {
	directory := t.TempDir()
	first := seedInventoryCleanupObjects(t, directory, 4)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := inventoryCleanupCancelContext{Context: base, cancel: cancel, first: first}
	deleted, complete, err := deleteInventoryTreeStepV2Context(ctx, directory, 1024, "")
	if !errors.Is(err, context.Canceled) || deleted != 1 || complete {
		t.Fatalf("canceled drain = %d/%v, %v", deleted, complete, err)
	}
	remaining, err := os.ReadDir(filepath.Dir(first))
	if err != nil || len(remaining) != 3 {
		t.Fatalf("retained files = %d, %v", len(remaining), err)
	}
	if deleted, complete, err := deleteInventoryTreeStepV2Context(t.Context(), directory, 1024, ""); err != nil || !complete || deleted != 7 {
		t.Fatalf("resumed drain = %d/%v, %v", deleted, complete, err)
	}
}

func TestInventorySelectedCleanupPreservesForeignEntry(t *testing.T) {
	directory, outside := t.TempDir(), t.TempDir()
	first := seedInventoryCleanupObjects(t, directory, 1)
	foreign := filepath.Join(outside, "keep")
	if err := os.WriteFile(foreign, []byte("operator data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, first); err != nil {
		t.Fatal(err)
	}
	if deleted, complete, err := deleteInventoryTreeStepV2Context(t.Context(), directory, 1024, ""); err == nil || complete || deleted != 0 {
		t.Fatalf("special entry accepted = %d/%v, %v", deleted, complete, err)
	}
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "operator data" {
		t.Fatal("foreign target changed", err)
	}
}

func seedInventoryCleanupObjects(t *testing.T, directory string, count int) string {
	t.Helper()
	objects := filepath.Join(directory, InventoryPublicationInventoryNameV2, "segment-00000", "objects")
	if err := os.MkdirAll(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	var first string
	for index := range count {
		path := filepath.Join(objects, fmt.Sprintf("%016x-%064x.json", index, index))
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = path
		}
	}
	return first
}
