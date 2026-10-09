package main

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

type withdrawalStore struct {
	sources map[string]typedindex.Source
}

func (s withdrawalStore) GetTypedSource(_ context.Context, name string) (typedindex.Source, error) {
	source, ok := s.sources[name]
	if !ok {
		return typedindex.Source{}, store.ErrNotFound
	}
	return source, nil
}
func (withdrawalStore) GetTypedIndexIntent(context.Context, string) (store.TypedIndexIntent, error) {
	panic("a withdrawn repository must not reach its intent")
}
func (withdrawalStore) InstallTypedProfileExpectedSource(context.Context, typedindex.Source, typedindex.Profile, string, int64) (store.TypedIndexIntent, error) {
	panic("a withdrawn repository must not be installed")
}

// TestManagedSCIPInstallationWithdrawsMovedSources: a repository whose HEAD
// moved, or that was removed, is withdrawn instead of refusing all of serve.
func TestManagedSCIPInstallationWithdrawsMovedSources(t *testing.T) {
	installed := func(repo string) typedInstalledProfile {
		return typedInstalledProfile{entry: typedInstalledRepository{Source: typedindex.Source{
			Repository: repo, Commit: strings.Repeat("a", 40),
		}}}
	}
	r := &typedInstallationRegistry{
		entries: map[string]typedInstalledProfile{"moved": installed("moved"), "removed": installed("removed")},
		order:   []string{"moved", "removed"},
	}
	moved := installed("moved").entry.Source
	moved.Commit = strings.Repeat("b", 40)
	if err := r.install(context.Background(), withdrawalStore{map[string]typedindex.Source{"moved": moved}}, t.TempDir()); err != nil {
		t.Fatalf("install refused startup: %v", err)
	}
	if len(r.entries) != 0 || len(r.order) != 0 {
		t.Fatalf("withdrawn repositories remain registered: %v %v", r.entries, r.order)
	}
}
