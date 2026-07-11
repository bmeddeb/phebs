package sync

// Internal tests: they swap ghAPIBase for a fake GitHub server.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// allowFileClones lets e2e fixtures clone from file:// origins that the
// clone-URL origin check (Epic 7 review) would rightly reject in prod.
func allowFileClones(t *testing.T) {
	t.Helper()
	testAllowOffOriginClones = true
	t.Cleanup(func() { testAllowOffOriginClones = false })
}

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
		name           string
		full           string
		archived, fork bool
		ex             config.Exclude
		want           bool
	}{
		{"no filters", "a/b", false, false, config.Exclude{}, false},
		{"archived out", "a/b", true, false, config.Exclude{Archived: true}, true},
		{"archived kept when off", "a/b", true, false, config.Exclude{}, false},
		{"fork out", "a/b", false, true, config.Exclude{Forks: true}, true},
		{"glob match", "a/b-mirror", false, false, config.Exclude{Repos: []string{"*/*-mirror"}}, true},
		{"glob miss", "a/b", false, false, config.Exclude{Repos: []string{"c/*"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excluded(tt.full, tt.archived, tt.fork, tt.ex); got != tt.want {
				t.Errorf("excluded(%q, %v, %v, %+v) = %v, want %v", tt.full, tt.archived, tt.fork, tt.ex, got, tt.want)
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
			w.Header().Set("Link", `<?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]ghRepo{{FullName: "o/r1"}, {FullName: "o/r2"}})
		case "2":
			_ = json.NewEncoder(w).Encode([]ghRepo{{FullName: "o/r3"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &hostClient{base: srv.URL, auth: "Bearer tok"}
	repos, err := listPages[ghRepo](context.Background(), c, "/orgs/o/repos")
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

func TestListPagesRejectsOffOriginBeforeSendingAuth(t *testing.T) {
	var offOriginHits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offOriginHits++
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("credential forwarded off-origin: %q", auth)
		}
		_ = json.NewEncoder(w).Encode([]ghRepo{})
	}))
	defer evil.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<`+evil.URL+`/repos?page=2>; rel="next"`)
		_ = json.NewEncoder(w).Encode([]ghRepo{{FullName: "o/r1"}})
	}))
	defer api.Close()

	c := &hostClient{base: api.URL, auth: "Bearer private-token"}
	_, err := listPages[ghRepo](context.Background(), c, "/repos")
	if err == nil || !strings.Contains(err.Error(), "off-origin") {
		t.Fatalf("listPages error = %v, want off-origin rejection", err)
	}
	if offOriginHits != 0 {
		t.Fatalf("off-origin server received %d requests, want 0", offOriginHits)
	}
}

func TestListPagesRejectsPaginationCycle(t *testing.T) {
	var hits int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Link", `</repos>; rel="next"`)
		_ = json.NewEncoder(w).Encode([]ghRepo{{FullName: "o/r1"}})
	}))
	defer api.Close()

	c := &hostClient{base: api.URL}
	_, err := listPages[ghRepo](context.Background(), c, "/repos")
	if err == nil || !strings.Contains(err.Error(), "pagination cycle") {
		t.Fatalf("listPages error = %v, want cycle rejection", err)
	}
	if hits != 1 {
		t.Errorf("requests = %d, want 1 before detecting cycle", hits)
	}
}

func TestListPagesCapsPageCount(t *testing.T) {
	var calls int
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		header := http.Header{}
		header.Set("Link", fmt.Sprintf(`</repos?page=%d>; rel="next"`, calls+1))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader("[]")),
			Request:    req,
		}, nil
	})
	c := &hostClient{base: "https://api.example", client: &http.Client{Transport: transport}}
	_, err := listPages[ghRepo](context.Background(), c, "/repos?page=1")
	if err == nil || !strings.Contains(err.Error(), "pagination exceeds") {
		t.Fatalf("listPages error = %v, want page-cap rejection", err)
	}
	if calls != maxHostPages {
		t.Errorf("requests = %d, want cap %d", calls, maxHostPages)
	}
}

func TestHostClientRejectsOffOriginRedirect(t *testing.T) {
	var offOriginHits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offOriginHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	}))
	defer api.Close()

	c := &hostClient{base: api.URL, auth: "Bearer private-token"}
	var out []ghRepo
	if _, err := c.getJSON(context.Background(), api.URL+"/repos", &out); err == nil || !strings.Contains(err.Error(), "off-origin") {
		t.Fatalf("getJSON error = %v, want off-origin redirect rejection", err)
	}
	if offOriginHits != 0 {
		t.Fatalf("redirect target received %d requests, want 0", offOriginHits)
	}
}

func TestHostErrorsDoNotReflectUntrustedSecrets(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", fmt.Sprintf(`<http://reflected-private-token@%s/repos?page=2>; rel="next"`, r.Host))
		_ = json.NewEncoder(w).Encode([]ghRepo{})
	}))
	defer api.Close()
	c := &hostClient{base: api.URL, auth: "Bearer private-token"}
	if _, err := listPages[ghRepo](context.Background(), c, "/repos"); err == nil {
		t.Fatal("listPages accepted pagination URL userinfo")
	} else if strings.Contains(err.Error(), "reflected-private-token") || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("pagination error leaked reflected secret: %v", err)
	}

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 reflected-private-token",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})
	c = &hostClient{base: "https://api.example", client: &http.Client{Transport: transport}}
	var out []ghRepo
	if _, err := c.getJSON(context.Background(), "https://api.example/repos", &out); err == nil {
		t.Fatal("getJSON accepted HTTP 500")
	} else if strings.Contains(err.Error(), "reflected-private-token") {
		t.Fatalf("status error leaked reflected secret: %v", err)
	}
}

func TestFilterInstallationUsers(t *testing.T) {
	repos := []ghRepo{
		{FullName: "Ben/private", Private: true},
		{FullName: "other/repo"},
		{FullName: "malformed"},
	}
	got := filterInstallationUsers(repos, []string{"ben"})
	if len(got) != 1 || got[0].FullName != "Ben/private" || !got[0].Private {
		t.Fatalf("filterInstallationUsers = %+v, want private Ben repo", got)
	}
}

// TestSyncGitHubEndToEnd drives the full adapter: fake API lists two repos
// (one archived) whose clone_urls are local fixtures; sync must mirror the
// kept repo, persist §5 metadata, and skip the excluded one.
func TestSyncGitHubEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	allowFileClones(t)
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
	names, err := SyncConnection(ctx, st, dataDir, conn, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(names) != 1 || names[0] != "github.com/ben/keep" {
		t.Errorf("synced names = %v, want just ben/keep", names)
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

// Regression (T6.3, found live): /users/{name}/repos never returns private
// repos, even authenticated. A users: entry naming the token's own login must
// ALSO list /user/repos — union, not replacement, because /user/repos alone
// omits public repos under a select-repositories fine-grained PAT.
func TestSyncGitHubAuthedUserListsPrivate(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	allowFileClones(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "s.go"), []byte("package s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// real GitHub URLs are case-insensitive; the config says "Ben"
		switch strings.ToLower(r.URL.Path) {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"ben"}`))
		case "/user/repos": // token grant: the private repo only
			_ = json.NewEncoder(w).Encode([]ghRepo{
				{ID: 7, FullName: "ben/secret", CloneURL: "file://" + origin,
					DefaultBranch: "main", Private: true},
			})
		case "/users/ben/repos": // public listing: the public repo only
			_ = json.NewEncoder(w).Encode([]ghRepo{
				{ID: 8, FullName: "ben/pub", CloneURL: "file://" + origin,
					DefaultBranch: "main"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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

	// "Ben" vs login "ben": the match must be case-insensitive.
	conn := config.Connection{Name: "gh", Type: "github", Token: "tok", Users: []string{"Ben"}}
	names, err := SyncConnection(ctx, st, dataDir, conn, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	sort.Strings(names)
	want := []string{"github.com/ben/pub", "github.com/ben/secret"}
	if !slices.Equal(names, want) {
		t.Fatalf("synced names = %v, want %v (public + private union)", names, want)
	}
	repo, err := st.GetRepo(ctx, "github.com/ben/secret")
	if err != nil {
		t.Fatal(err)
	}
	if repo.IsPublic {
		t.Error("private repo persisted as public")
	}
}

// Regression: git errors are persisted to the job row and logged, so the
// credential-bearing -c http.extraheader arg must be redacted from them.
func TestRedactArgs(t *testing.T) {
	secret := "aValidLookingBase64Token=="
	args := []string{
		"-c", "http.extraheader=Authorization: Basic " + secret,
		"clone", "--mirror", "https://github.com/foo/bar.git", "/tmp/x",
	}
	got := redactArgs(args)
	if strings.Contains(got, secret) || strings.Contains(got, "Authorization") {
		t.Errorf("redactArgs leaked the credential: %q", got)
	}
	if !strings.Contains(got, "clone") || !strings.Contains(got, "--mirror") {
		t.Errorf("redactArgs dropped non-secret args: %q", got)
	}

	// credentials embedded in a clone URL (the only auth path for a private
	// type:git repo) must also be scrubbed.
	urlSecret := "ghp_urlToken123"
	got = redactArgs([]string{"clone", "--mirror", "https://user:" + urlSecret + "@gitea.internal/team/repo.git", "/tmp/x"})
	if strings.Contains(got, urlSecret) || strings.Contains(got, "user:") {
		t.Errorf("redactArgs leaked URL credential: %q", got)
	}
	if !strings.Contains(got, "gitea.internal/team/repo.git") {
		t.Errorf("redactArgs mangled the host/path: %q", got)
	}

	got = redactArgs([]string{"clone", "https:opaque-user:opaque-secret@git.internal/team/repo.git"})
	if strings.Contains(got, "opaque-user") || strings.Contains(got, "opaque-secret") {
		t.Errorf("redactArgs leaked opaque HTTP credentials: %q", got)
	}
	if !strings.Contains(got, "git.internal/team/repo.git") {
		t.Errorf("redactArgs mangled opaque HTTP host/path: %q", got)
	}
}

func TestRateLimitedNeverSpins(t *testing.T) {
	mk := func(status int, hdr map[string]string) *http.Response {
		h := http.Header{}
		for k, v := range hdr {
			h.Set(k, v)
		}
		return &http.Response{StatusCode: status, Header: h}
	}
	cases := []struct {
		name string
		resp *http.Response
	}{
		{"retry-after seconds", mk(429, map[string]string{"Retry-After": "30"})},
		{"retry-after http-date", mk(429, map[string]string{"Retry-After": time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)})},
		{"retry-after garbage", mk(403, map[string]string{"Retry-After": "soon"})},
		{"ratelimit past reset", mk(403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1"})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wait, limited, err := rateLimited(c.resp)
			if err != nil {
				t.Fatalf("rateLimited error = %v", err)
			}
			if !limited {
				t.Fatalf("expected limited=true")
			}
			if wait < time.Second {
				t.Errorf("wait = %v, want >= 1s (a 0 wait hot-loops the retry)", wait)
			}
		})
	}
	tooLong := []struct {
		name string
		resp *http.Response
	}{
		{"retry-after seconds", mk(429, map[string]string{"Retry-After": "3600"})},
		{"retry-after date", mk(429, map[string]string{"Retry-After": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)})},
		{"rate-limit reset", mk(403, map[string]string{
			"X-RateLimit-Remaining": "0",
			"X-RateLimit-Reset":     fmt.Sprint(time.Now().Add(time.Hour).Unix()),
		})},
	}
	for _, c := range tooLong {
		t.Run("excessive "+c.name, func(t *testing.T) {
			wait, limited, err := rateLimited(c.resp)
			if !limited || err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
				t.Fatalf("rateLimited = (%v, %v, %v), want bounded-wait error", wait, limited, err)
			}
		})
	}
	// a plain 403 with no rate-limit headers is NOT a rate limit
	if _, limited, err := rateLimited(mk(403, nil)); limited || err != nil {
		t.Error("plain 403 should not be treated as rate limited")
	}
}

func TestHostClientFailsExcessiveRateLimitWithoutSleeping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := &hostClient{base: srv.URL}
	started := time.Now()
	var out []ghRepo
	_, err := c.getJSON(context.Background(), srv.URL+"/repos", &out)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("getJSON error = %v, want excessive rate-limit wait error", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("getJSON occupied the runner for %v", elapsed)
	}
}

func TestHostClientBoundsRepeatedRateLimits(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := &hostClient{base: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out []ghRepo
	_, err := c.getJSON(ctx, srv.URL+"/repos", &out)
	if err == nil || !strings.Contains(err.Error(), "retry budget exhausted") {
		t.Fatalf("getJSON error = %v, want retry-budget error", err)
	}
	if hits != maxRateLimitRetries+1 {
		t.Fatalf("rate-limit requests = %d, want %d", hits, maxRateLimitRetries+1)
	}
}
