package sync

// Internal tests: they swap ghAPIBase for a fake GitHub server.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// gitc runs git for fixture setup (internal-test twin of the one in sync_test.go).
func gitc(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestExcluded(t *testing.T) {
	tests := []struct {
		name string
		repo ghRepo
		ex   config.Exclude
		want bool
	}{
		{"no filters", ghRepo{FullName: "a/b"}, config.Exclude{}, false},
		{"archived out", ghRepo{FullName: "a/b", Archived: true}, config.Exclude{Archived: true}, true},
		{"archived kept when off", ghRepo{FullName: "a/b", Archived: true}, config.Exclude{}, false},
		{"fork out", ghRepo{FullName: "a/b", Fork: true}, config.Exclude{Forks: true}, true},
		{"glob match", ghRepo{FullName: "a/b-mirror"}, config.Exclude{Repos: []string{"*/*-mirror"}}, true},
		{"glob miss", ghRepo{FullName: "a/b"}, config.Exclude{Repos: []string{"c/*"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excluded(tt.repo, tt.ex); got != tt.want {
				t.Errorf("excluded(%+v, %+v) = %v, want %v", tt.repo, tt.ex, got, tt.want)
			}
		})
	}
}

func TestNextLink(t *testing.T) {
	link := `<https://api.example/repos?page=2>; rel="next", <https://api.example/repos?page=9>; rel="last"`
	if got := nextLink(link); got != "https://api.example/repos?page=2" {
		t.Errorf("nextLink = %q", got)
	}
	if got := nextLink(`<https://api.example/x>; rel="last"`); got != "" {
		t.Errorf("nextLink without next = %q", got)
	}
}

func TestListReposPaginationAndRateLimit(t *testing.T) {
	var rateLimitHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Query().Get("page") {
		case "", "1":
			if rateLimitHits == 0 { // first hit: stall the client once
				rateLimitHits++
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Unix())) // resets immediately
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/orgs/o/repos?page=2>; rel="next"`, r.Host))
			_ = json.NewEncoder(w).Encode([]ghRepo{{FullName: "o/r1"}, {FullName: "o/r2"}})
		case "2":
			_ = json.NewEncoder(w).Encode([]ghRepo{{FullName: "o/r3"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &ghClient{base: srv.URL, token: "tok"}
	repos, err := c.listRepos(context.Background(), "/orgs/o/repos")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 3 || repos[2].FullName != "o/r3" {
		t.Errorf("repos = %+v, want r1..r3 across two pages", repos)
	}
	if rateLimitHits != 1 {
		t.Errorf("rate limit exercised %d times, want 1", rateLimitHits)
	}
}

// TestSyncGitHubEndToEnd drives the full adapter: fake API lists two repos
// (one archived) whose clone_urls are local fixtures; sync must mirror the
// kept repo, persist §5 metadata, and skip the excluded one.
func TestSyncGitHubEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "trunk")
	if err := os.WriteFile(filepath.Join(origin, "f.go"), []byte("package f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")

	pushed := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/ben/repos" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]ghRepo{
			{ID: 42, FullName: "ben/keep", CloneURL: "file://" + origin, HTMLURL: "https://github.com/ben/keep",
				DefaultBranch: "trunk", Private: false, PushedAt: &pushed},
			{ID: 43, FullName: "ben/old", CloneURL: "file:///nonexistent", Archived: true},
		})
	}))
	defer srv.Close()

	old := ghAPIBase
	ghAPIBase = srv.URL
	t.Cleanup(func() { ghAPIBase = old })

	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	conn := config.Connection{Name: "gh", Type: "github", Users: []string{"ben"},
		Exclude: config.Exclude{Archived: true}}
	if err := SyncConnection(ctx, st, dataDir, conn); err != nil {
		t.Fatalf("sync: %v", err)
	}

	repo, err := st.GetRepo(ctx, "github.com/ben/keep")
	if err != nil {
		t.Fatal(err)
	}
	if repo.DefaultBranch != "trunk" || repo.ExternalID != "42" || repo.ExternalHostType != "github" ||
		repo.WebURL != "https://github.com/ben/keep" || !repo.IsPublic ||
		repo.PushedAt == nil || !repo.PushedAt.Equal(pushed) {
		t.Errorf("§5 metadata not persisted, got %+v", repo)
	}
	if _, err := os.Stat(filepath.Join(RepoDir(dataDir, "github.com/ben/keep"), "HEAD")); err != nil {
		t.Errorf("mirror missing on disk: %v", err)
	}

	if _, err := st.GetRepo(ctx, "github.com/ben/old"); err == nil {
		t.Error("archived repo was synced despite exclude.archived")
	}
	all, _ := st.ListRepos(ctx)
	if len(all) != 1 {
		t.Errorf("ListRepos = %d rows, want 1", len(all))
	}
}
