package focusedindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchSelectedCleanupLargeBatch(t *testing.T) {
	directory := t.TempDir()
	for index := range 70 {
		path := filepath.Join(directory, fmt.Sprintf("phebs-whole-%04d.zoekt", index))
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deleted, complete, err := deleteSearchGenerationStepContext(t.Context(), directory, 64)
	if err != nil || deleted != 64 || complete {
		t.Fatalf("first drain = %d/%v, %v", deleted, complete, err)
	}
	deleted, complete, err = deleteSearchGenerationStepContext(t.Context(), directory, 64)
	if err != nil || deleted != 7 || !complete {
		t.Fatalf("final drain = %d/%v, %v", deleted, complete, err)
	}
}

type searchCleanupCancelContext struct {
	context.Context
	cancel context.CancelFunc
	first  string
}

func (ctx searchCleanupCancelContext) Err() error {
	if _, err := os.Lstat(ctx.first); errors.Is(err, os.ErrNotExist) {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestSearchSelectedCleanupCancellationRetainsPrefix(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "phebs-whole-0000.zoekt")
	for index := range 3 {
		path := filepath.Join(directory, fmt.Sprintf("phebs-whole-%04d.zoekt", index))
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := searchCleanupCancelContext{Context: base, cancel: cancel, first: first}
	deleted, complete, err := deleteSearchGenerationStepContext(ctx, directory, 64)
	if !errors.Is(err, context.Canceled) || deleted != 1 || complete {
		t.Fatalf("canceled drain = %d/%v, %v", deleted, complete, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("retained files = %d, %v", len(entries), err)
	}
	if deleted, complete, err := deleteSearchGenerationStepContext(t.Context(), directory, 64); err != nil || !complete || deleted != 3 {
		t.Fatalf("resumed drain = %d/%v, %v", deleted, complete, err)
	}
}
