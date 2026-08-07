package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
)

type searchLifecycleNoPins struct{}

func (searchLifecycleNoPins) Pinned(string, string) bool { return false }

func TestSearchGenerationOwnerAdvancesPastMalformedRepository(t *testing.T) {
	indexDir := t.TempDir()
	base := focusedindex.SearchGenerationRootDirectory(indexDir)
	bad := strings.Repeat("0", 64)
	good := strings.Repeat("f", 64)
	for _, name := range []string{bad, good} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(base, bad, "foreign.txt"), []byte("operator-owned"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	owner := SearchGenerationOwnerImpl{
		IndexDir: indexDir, Pins: searchLifecycleNoPins{},
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
	}
	store := newMemoryCursorStore()
	controller, err := NewController(store, owner)
	if err != nil {
		t.Fatal(err)
	}
	controller.now = func() time.Time { return time.Unix(1, 0).UTC() }
	first := controller.Tick(t.Context())
	if first.Err == nil || !first.AdvanceOnError || first.Cursor != bad {
		t.Fatalf("malformed repository turn = %+v", first)
	}
	if got := store.values["owner:"+SearchOwner]; got != bad {
		t.Fatalf("persisted malformed cursor = %q, want %q", got, bad)
	}
	second := controller.Tick(t.Context())
	if second.Err != nil || second.Cursor != good {
		t.Fatalf("healthy sibling turn = %+v", second)
	}
}
