package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

// twoRepoStore serves one granted and one forbidden repo.
type twoRepoStore struct {
	store.Store
}

func (twoRepoStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{
		{Name: "github.com/ok/visible", CloneURL: "https://github.com/ok/visible.git", IndexedCommitHash: "aaa"},
		{Name: "github.com/no/hidden", CloneURL: "https://github.com/no/hidden.git", IndexedCommitHash: "bbb"},
	}, nil
}

func (twoRepoStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return []store.RepoStatus{
		{Repo: store.Repo{Name: "github.com/ok/visible"}},
		{Repo: store.Repo{Name: "github.com/no/hidden"}},
	}, nil
}

func (twoRepoStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	switch name {
	case "github.com/ok/visible", "github.com/no/hidden":
		return &store.Repo{Name: name, IndexedCommitHash: "aaa"}, nil
	}
	// the real store wraps with the repo name (surreal.go) — mirror it so
	// body-parity assertions test the production message shapes
	return nil, fmt.Errorf("repo %q: %w", name, store.ErrNotFound)
}

// T10.3: every per-repo read surface hides forbidden repos as if they did
// not exist, and listings are filtered server-side.
func TestPermissionFilteringAcrossReadSurfaces(t *testing.T) {
	h := api.New(api.Options{
		Version: "t", Store: twoRepoStore{}, DataDir: t.TempDir(),
		Visible: func(context.Context) func(store.Repo) bool {
			return func(r store.Repo) bool { return r.Name == "github.com/ok/visible" }
		},
	})
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	for _, path := range []string{"/api/repos", "/api/repo-status"} {
		rec := get(path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		var rows []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(rows) != 1 || rows[0]["name"] != "github.com/ok/visible" {
			t.Errorf("%s = %v, want only the granted repo", path, rows)
		}
	}

	// per-repo reads: forbidden is indistinguishable from missing — same 404
	// status AND same body shape (a differing message is an existence oracle)
	for _, tc := range []struct{ forbidden, missing string }{
		{"/api/source?repo=github.com/no/hidden&path=f.go", "/api/source?repo=github.com/no/absent&path=f.go"},
		{"/api/folder_contents?repo=github.com/no/hidden", "/api/folder_contents?repo=github.com/no/absent"},
		{"/api/tree?repo=github.com/no/hidden", "/api/tree?repo=github.com/no/absent"},
		{"/api/blame?repo=github.com/no/hidden&path=f.go", "/api/blame?repo=github.com/no/absent&path=f.go"},
		{"/api/commits?repo=github.com/no/hidden", "/api/commits?repo=github.com/no/absent"},
		{"/api/commit?repo=github.com/no/hidden", "/api/commit?repo=github.com/no/absent"},
		{"/api/diff?repo=github.com/no/hidden", "/api/diff?repo=github.com/no/absent"},
		// codenav endpoints share historyRef and are covered by the paths
		// above; with a nil CodeNav service they 503 before the repo gate,
		// identically for granted and forbidden repos.
	} {
		forbidden, missing := get(tc.forbidden), get(tc.missing)
		if forbidden.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
			t.Errorf("GET %s/%s = %d/%d, want 404/404", tc.forbidden, tc.missing, forbidden.Code, missing.Code)
			continue
		}
		shape := func(s string) string {
			return strings.ReplaceAll(strings.ReplaceAll(s, "github.com/no/hidden", "R"), "github.com/no/absent", "R")
		}
		if shape(forbidden.Body.String()) != shape(missing.Body.String()) {
			t.Errorf("GET %s: forbidden body %q differs from missing body %q — existence oracle",
				tc.forbidden, forbidden.Body.String(), missing.Body.String())
		}
	}

	// the same reads for a granted repo pass the permission gate (they fail
	// later on the missing mirror, proving the gate itself admitted them)
	if rec := get("/api/tree?repo=github.com/ok/visible"); rec.Code == http.StatusNotFound {
		t.Errorf("granted repo blocked by the permission gate: %d %s", rec.Code, rec.Body)
	}
}

// A nil Visible keeps today's behavior: everything is listed.
func TestNoPermissionFilterListsEverything(t *testing.T) {
	h := api.New(api.Options{Version: "t", Store: twoRepoStore{}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/repos", nil))
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("unfiltered repos = %d rows, want 2", len(rows))
	}
}
