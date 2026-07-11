package search_test

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
)

// T10.3 leak test: the per-user RepoSet is compiled into the query, so a
// forbidden repo's shard contributes nothing — across Search and Stream.
func TestPermissionFilterExcludesForbiddenRepos(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := corpus(t, ctx)

	// The predicate is pure mechanism here; the public/granted/always-visible
	// policy lives in serve's closure. Grant exactly one repo.
	granted := map[string]bool{"example.com/plain": true}
	s.Visible = func(context.Context) func(store.Repo) bool {
		return func(r store.Repo) bool { return granted[r.Name] }
	}

	assertOnlyPlain := func(stage string, files []search.FileResult) {
		t.Helper()
		if len(files) == 0 {
			t.Fatalf("%s: no results at all", stage)
		}
		for _, f := range files {
			if f.Repo != "example.com/plain" {
				t.Fatalf("%s: forbidden repo leaked: %+v", stage, f)
			}
		}
	}

	res, err := s.Search(ctx, "phebsNeedle", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyPlain("search", res.Files)

	var streamed []search.FileResult
	if _, err := s.Stream(ctx, "phebsNeedle", search.Options{}, func(r *search.Result) {
		streamed = append(streamed, r.Files...)
	}); err != nil {
		t.Fatal(err)
	}
	assertOnlyPlain("stream", streamed)

	// an explicit repo: filter for the forbidden repo returns nothing, not
	// an error — an error would disclose existence
	res, err = s.Search(ctx, "phebsNeedle repo:forked", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 0 {
		t.Fatalf("repo: filter bypassed the permission set: %+v", res.Files)
	}

	// a nil predicate (administrator) sees everything again
	s.Visible = func(context.Context) func(store.Repo) bool { return nil }
	res, err = s.Search(ctx, "phebsNeedle", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	repos := map[string]bool{}
	for _, f := range res.Files {
		repos[f.Repo] = true
	}
	if !repos["example.com/plain"] || !repos["example.com/forked"] {
		t.Fatalf("admin does not see both repos: %v", repos)
	}
}
