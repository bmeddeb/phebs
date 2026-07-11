package sync

// Internal tests: the fake Gitea server is reached via conn.URL, like the
// GitLab tests. A live-container verification ran for T7.2 (see PLAN ADR).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// TestSyncGiteaEndToEnd drives the adapter against a fake API: org + user
// listings, an explicit repo, fork exclusion, token auth header, and §5
// metadata of a private org repo.
func TestSyncGiteaEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "t.go"), []byte("package t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")

	updated := time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)
	repo := func(id int64, full string, private, fork bool) gtRepo {
		return gtRepo{
			ID: id, FullName: full, CloneURL: "file://" + origin,
			HTMLURL: "https://gitea.example.com/" + full, DefaultBranch: "main",
			Private: private, Fork: fork, UpdatedAt: &updated,
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token gta" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/orgs/acme/repos":
			_ = json.NewEncoder(w).Encode([]gtRepo{
				repo(1, "acme/core", true, false),
				repo(2, "acme/fork-of-thing", false, true), // excluded: fork
			})
		case "/api/v1/users/dev/repos":
			_ = json.NewEncoder(w).Encode([]gtRepo{repo(3, "dev/notes", true, false)})
		case "/api/v1/repos/solo/tool":
			_ = json.NewEncoder(w).Encode(repo(4, "solo/tool", false, false))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	host := srv.Listener.Addr().String()
	conn := config.Connection{
		Name: "gt", Type: "gitea", Token: "gta", URL: srv.URL,
		Orgs: []string{"acme"}, Users: []string{"dev"}, Repos: []string{"solo/tool"},
		Exclude: config.Exclude{Forks: true},
	}
	names, err := SyncConnection(ctx, st, dataDir, conn)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	sort.Strings(names)
	want := []string{host + "/acme/core", host + "/dev/notes", host + "/solo/tool"}
	if !slices.Equal(names, want) {
		t.Fatalf("synced names = %v, want %v", names, want)
	}

	got, err := st.GetRepo(ctx, host+"/acme/core")
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultBranch != "main" || got.ExternalID != "1" || got.ExternalHostType != "gitea" ||
		got.ExternalHostURL != srv.URL || got.WebURL != "https://gitea.example.com/acme/core" ||
		got.IsPublic || got.PushedAt == nil || !got.PushedAt.Equal(updated) {
		t.Errorf("§5 metadata not persisted, got %+v", got)
	}

	if _, err := st.GetRepo(ctx, host+"/acme/fork-of-thing"); err == nil {
		t.Error("fork was synced despite exclude.forks")
	}
}
