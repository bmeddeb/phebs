package sync

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

func aclFixtureRepo(t *testing.T) string {
	t.Helper()
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")
	return origin
}

func aclStore(t *testing.T, ctx context.Context, dataDir string) *store.Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	return st
}

// TestGitHubACLMirroring: private repos get collaborator edges, public repos
// never trigger an ACL call, a failed listing keeps stale edges, and a
// successful shrunken listing revokes.
func TestGitHubACLMirroring(t *testing.T) {
	allowFileClones(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	origin := aclFixtureRepo(t)

	var publicACLCalls, failListing atomic.Int32
	collaborators := atomic.Pointer[[]ghUser]{}
	initial := []ghUser{{Login: "Ben"}, {Login: "Carol"}}
	collaborators.Store(&initial)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/ben/repos":
			_ = json.NewEncoder(w).Encode([]ghRepo{
				{ID: 1, FullName: "ben/secret", CloneURL: "file://" + origin, DefaultBranch: "main", Private: true},
				{ID: 2, FullName: "ben/open", CloneURL: "file://" + origin, DefaultBranch: "main", Private: false},
			})
		case "/repos/ben/secret/collaborators":
			if failListing.Load() > 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(*collaborators.Load())
		case "/repos/ben/open/collaborators":
			publicACLCalls.Add(1)
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := ghAPIBase
	ghAPIBase = srv.URL
	t.Cleanup(func() { ghAPIBase = old })

	dataDir := t.TempDir()
	st := aclStore(t, ctx, dataDir)
	conn := config.Connection{Name: "gh", Type: "github", Users: []string{"ben"}}
	sync := func(stage string) {
		t.Helper()
		if _, err := SyncConnection(ctx, st, dataDir, conn, st); err != nil {
			t.Fatalf("%s: sync: %v", stage, err)
		}
	}

	sync("initial")
	if publicACLCalls.Load() != 0 {
		t.Error("public repo ACL was listed; expected no call")
	}
	for _, id := range []string{"github.com:ben", "github.com:CAROL"} {
		names, err := st.ListPermittedRepos(ctx, []string{id})
		if err != nil || !slices.Equal(names, []string{"github.com/ben/secret"}) {
			t.Errorf("%s sees %v (%v), want ben/secret", id, names, err)
		}
	}

	// transient listing failure keeps the previous grants
	failListing.Store(1)
	sync("failed listing")
	if names, _ := st.ListPermittedRepos(ctx, []string{"github.com:carol"}); len(names) != 1 {
		t.Errorf("stale grants dropped on listing failure: %v", names)
	}

	// a successful shrunken listing revokes carol
	failListing.Store(0)
	shrunk := []ghUser{{Login: "Ben"}}
	collaborators.Store(&shrunk)
	sync("revocation")
	if names, _ := st.ListPermittedRepos(ctx, []string{"github.com:carol"}); len(names) != 0 {
		t.Errorf("carol still granted after revocation: %v", names)
	}
	if names, _ := st.ListPermittedRepos(ctx, []string{"github.com:ben"}); len(names) != 1 {
		t.Errorf("ben lost grants on revocation of carol: %v", names)
	}
}

// TestGitLabACLMirroring: members/all filtered to active Reporter+.
func TestGitLabACLMirroring(t *testing.T) {
	allowFileClones(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	origin := aclFixtureRepo(t)

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/dev/projects":
			_ = json.NewEncoder(w).Encode([]glProject{{
				ID: 7, PathWithNamespace: "dev/hidden", HTTPURLToRepo: "file://" + origin,
				WebURL: srvURL + "/dev/hidden", DefaultBranch: "main", Visibility: "private",
			}})
		case "/api/v4/projects/7/members/all":
			_ = json.NewEncoder(w).Encode([]glMember{
				{Username: "Dev", State: "active", AccessLevel: 50},
				{Username: "guest", State: "active", AccessLevel: 10}, // below Reporter
				{Username: "gone", State: "blocked", AccessLevel: 40}, // not active
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	host := srv.Listener.Addr().String()

	dataDir := t.TempDir()
	st := aclStore(t, ctx, dataDir)
	conn := config.Connection{Name: "gl", Type: "gitlab", URL: srv.URL, Token: "glt", Users: []string{"dev"}}
	if _, err := SyncConnection(ctx, st, dataDir, conn, st); err != nil {
		t.Fatalf("sync: %v", err)
	}

	names, err := st.ListPermittedRepos(ctx, []string{host + ":dev"})
	if err != nil || !slices.Equal(names, []string{host + "/dev/hidden"}) {
		t.Errorf("dev sees %v (%v)", names, err)
	}
	for _, id := range []string{host + ":guest", host + ":gone"} {
		if names, _ := st.ListPermittedRepos(ctx, []string{id}); len(names) != 0 {
			t.Errorf("%s granted %v, want nothing", id, names)
		}
	}
}

// TestGiteaACLMirroring: collaborators plus the owner, keyed by the
// instance host.
func TestGiteaACLMirroring(t *testing.T) {
	allowFileClones(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	origin := aclFixtureRepo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/repos":
			_ = json.NewEncoder(w).Encode([]gtRepo{{
				ID: 1, FullName: "acme/core", CloneURL: "file://" + origin,
				DefaultBranch: "main", Private: true,
			}})
		case "/api/v1/repos/acme/core/collaborators":
			_ = json.NewEncoder(w).Encode([]gtUser{{Login: "Carol"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	host := srv.Listener.Addr().String()

	dataDir := t.TempDir()
	st := aclStore(t, ctx, dataDir)
	conn := config.Connection{Name: "gt", Type: "gitea", URL: srv.URL, Token: "gta", Orgs: []string{"acme"}}
	if _, err := SyncConnection(ctx, st, dataDir, conn, st); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var granted []string
	for _, id := range []string{host + ":carol", host + ":acme"} {
		names, err := st.ListPermittedRepos(ctx, []string{id})
		if err != nil {
			t.Fatal(err)
		}
		granted = append(granted, names...)
	}
	sort.Strings(granted)
	if !slices.Equal(granted, []string{host + "/acme/core", host + "/acme/core"}) {
		t.Errorf("collaborator+owner grants = %v", granted)
	}
}
