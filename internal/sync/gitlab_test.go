package sync

// Internal tests: the fake GitLab server is reached via conn.URL (the
// self-hosted base-URL path), so no swap var is needed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// TestSyncGitLabEndToEnd drives the full adapter against a fake API: a group
// listing paginated across two pages (with one rate-limit stall), a user
// listing, an explicit project, an archived exclusion, and §5 metadata of a
// private subgroup project.
func TestSyncGitLabEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	allowFileClones(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "g.go"), []byte("package g\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")

	active := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)
	proj := func(id int64, path, visibility string, archived bool) glProject {
		return glProject{
			ID: id, PathWithNamespace: path, HTTPURLToRepo: "file://" + origin,
			WebURL: "https://git.example.com/" + path, DefaultBranch: "main",
			Visibility: visibility, Archived: archived, LastActivityAt: &active,
		}
	}

	var rateLimitHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer glpat" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// group and project path params arrive URL-encoded
		switch r.URL.EscapedPath() {
		case "/api/v4/groups/team%2Fplatform/projects":
			if r.URL.Query().Get("include_subgroups") != "true" {
				t.Error("group listing missing include_subgroups=true")
			}
			if r.URL.Query().Get("with_shared") != "false" {
				t.Error("group listing missing with_shared=false (shared foreign projects would be synced)")
			}
			switch r.URL.Query().Get("page") {
			case "", "1":
				if rateLimitHits == 0 { // first hit: stall the client once
					rateLimitHits++
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.Header().Set("Link", fmt.Sprintf(`<http://%s/api/v4/groups/team%%2Fplatform/projects?page=2&include_subgroups=true&with_shared=false>; rel="next"`, r.Host))
				_ = json.NewEncoder(w).Encode([]glProject{
					proj(1, "team/platform/api", "private", false),
					proj(2, "team/platform/old", "private", true), // excluded: archived
				})
			case "2":
				_ = json.NewEncoder(w).Encode([]glProject{proj(3, "team/platform/infra/tf", "internal", false)})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		case "/api/v4/users/dev/projects":
			_ = json.NewEncoder(w).Encode([]glProject{proj(4, "dev/dotfiles", "public", false)})
		case "/api/v4/projects/solo%2Ftool":
			_ = json.NewEncoder(w).Encode(proj(5, "solo/tool", "private", false))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	st, err := store.OpenLocalMemory(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	host := srv.Listener.Addr().String()
	conn := config.Connection{
		Name: "gl", Type: "gitlab", Token: "glpat", URL: srv.URL,
		Groups: []string{"team/platform"}, Users: []string{"dev"}, Repos: []string{"solo/tool"},
		Exclude: config.Exclude{Archived: true},
	}
	names, err := SyncConnection(ctx, st, dataDir, conn, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	sort.Strings(names)
	want := []string{
		host + "/dev/dotfiles",
		host + "/solo/tool",
		host + "/team/platform/api",
		host + "/team/platform/infra/tf",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("synced names = %v, want %v", names, want)
	}
	if rateLimitHits != 1 {
		t.Errorf("rate limit exercised %d times, want 1", rateLimitHits)
	}

	// §5 metadata of the private subgroup project
	repo, err := st.GetRepo(ctx, host+"/team/platform/api")
	if err != nil {
		t.Fatal(err)
	}
	if repo.DefaultBranch != "main" || repo.ExternalID != "1" || repo.ExternalHostType != "gitlab" ||
		repo.ExternalHostURL != srv.URL || repo.WebURL != "https://git.example.com/team/platform/api" ||
		repo.IsPublic || repo.PushedAt == nil || !repo.PushedAt.Equal(active) {
		t.Errorf("§5 metadata not persisted, got %+v", repo)
	}
	if _, err := os.Stat(filepath.Join(RepoDir(dataDir, host+"/team/platform/api"), "HEAD")); err != nil {
		t.Errorf("mirror missing on disk: %v", err)
	}

	// archived project excluded end-to-end
	if _, err := st.GetRepo(ctx, host+"/team/platform/old"); err == nil {
		t.Error("archived project was synced despite exclude.archived")
	}
	all, _ := st.ListRepos(ctx)
	if len(all) != 4 {
		t.Errorf("ListRepos = %d rows, want 4", len(all))
	}
}

// A project path from a hostile self-hosted server must not escape $DATA.
func TestSyncGitLabRejectsTraversal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]glProject{{ID: 9, PathWithNamespace: "../../../etc", HTTPURLToRepo: "https://x/e.git"}})
	}))
	defer srv.Close()

	conn := config.Connection{Name: "gl", Type: "gitlab", URL: srv.URL, Users: []string{"x"}}
	_, err := syncGitLab(context.Background(), nil, t.TempDir(), conn, nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe repo name") {
		t.Fatalf("want unsafe-repo-name error, got %v", err)
	}
}
