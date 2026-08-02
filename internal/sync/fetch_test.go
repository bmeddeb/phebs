package sync

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// TestFetchHandler drives the webhook fetch path: an existing mirror is
// re-fetched with the claiming connection's auth and indexing is chained —
// no host listing involved.
func TestFetchHandler(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "f.go"), []byte("package f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "one")

	dataDir := t.TempDir()
	st, err := store.OpenLocalMemory(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	cfg := &config.Config{
		Server:      config.Server{DataDir: dataDir},
		Connections: []config.Connection{{Name: "c1", Type: "git", URL: origin}},
	}
	// initial sync creates the mirror and membership
	names, err := SyncConnection(ctx, st, dataDir, cfg.Connections[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	name := names[0]
	if err := st.SetRepoConnections(ctx, "c1", names); err != nil {
		t.Fatal(err)
	}

	// new commit upstream, then a webhook-style fetch job
	if err := os.WriteFile(filepath.Join(origin, "g.go"), []byte("package f // two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "two")
	newHead := strings.TrimSpace(gitc(t, origin, "rev-parse", "HEAD"))

	if err := FetchHandler(cfg, st)(ctx, store.Job{Target: name}); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	mirrorHead, err := runGit(ctx, RepoDir(dataDir, name), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if mirrorHead != newHead {
		t.Errorf("mirror HEAD = %s, want %s (fetch did not update)", mirrorHead, newHead)
	}

	jobs, err := st.ListJobs(ctx, store.JobIndex, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range jobs {
		found = found || j.Target == name
	}
	if !found {
		t.Errorf("no indexing job chained for %s (jobs %+v)", name, jobs)
	}

	// a repo removed after its job was queued is a terminal no-op
	if err := FetchHandler(cfg, st)(ctx, store.Job{Target: "github.com/x/y"}); err != nil {
		t.Errorf("fetch of deleted repo = %v, want no-op", err)
	}

	// a repo present but claimed by NO configured connection must fail, not
	// fetch unauthenticated (Epic 7 review: this guard was unpinned)
	if err := st.UpsertRepo(ctx, store.Repo{Name: "github.com/or/phan", CloneURL: "https://github.com/or/phan.git"}); err != nil {
		t.Fatal(err)
	}
	err = FetchHandler(cfg, st)(ctx, store.Job{Target: "github.com/or/phan"})
	if err == nil || !strings.Contains(err.Error(), "no configured connection claims") {
		t.Errorf("unclaimed fetch err = %v, want no-claim error", err)
	}

	// a stored clone URL off the claiming connection's origin fails closed:
	// credentials must not follow a hostile URL (SSRF guard, Epic 7 review)
	evil := store.Repo{Name: "gt.internal/acme/core", CloneURL: "http://10.9.9.9:8080/loot.git"}
	if err := st.UpsertRepo(ctx, evil); err != nil {
		t.Fatal(err)
	}
	gtCfg := &config.Config{
		Server: config.Server{DataDir: dataDir},
		Connections: []config.Connection{
			{Name: "gt", Type: "gitea", URL: "http://gt.internal", Token: "sekrit", Orgs: []string{"acme"}},
		},
	}
	if err := st.SetRepoConnections(ctx, "gt", []string{evil.Name}); err != nil {
		t.Fatal(err)
	}
	err = FetchHandler(gtCfg, st)(ctx, store.Job{Target: evil.Name})
	if err == nil || !strings.Contains(err.Error(), "off-origin") {
		t.Errorf("off-origin fetch err = %v, want off-origin refusal", err)
	}
}

// Epic 7 review: the origin guard and per-host auth shapes, unit-level.
func TestCheckCloneURL(t *testing.T) {
	gh := config.Connection{Name: "gh", Type: "github"}
	gt := config.Connection{Name: "gt", Type: "gitea", URL: "http://git.internal:3009"}
	gl := config.Connection{Name: "gl", Type: "gitlab"} // defaults to gitlab.com
	git := config.Connection{Name: "g", Type: "git", URL: "https://anywhere.example/r.git"}

	cases := []struct {
		name string
		conn config.Connection
		url  string
		ok   bool
	}{
		{"github on-origin", gh, "https://github.com/o/r.git", true},
		{"github off-host", gh, "https://evil.example/o/r.git", false},
		{"github scheme downgrade", gh, "http://github.com/o/r.git", false},
		{"gitea on-origin with port", gt, "http://git.internal:3009/acme/core.git", true},
		{"gitea off-host", gt, "http://10.0.0.5:8080/admin/secrets.git", false},
		{"gitea wrong port", gt, "http://git.internal:9999/acme/core.git", false},
		{"gitlab default origin", gl, "https://gitlab.com/g/p.git", true},
		{"gitlab off-host", gl, "https://attacker.example/g/p.git", false},
		{"git type unconstrained", git, "ssh://elsewhere/r.git", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := checkCloneURL(tt.conn, tt.url)
			if tt.ok && err != nil {
				t.Errorf("checkCloneURL(%s) = %v, want ok", tt.url, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("checkCloneURL(%s) accepted an off-origin URL", tt.url)
			}
		})
	}
}

func TestHostPrefix(t *testing.T) {
	cases := []struct{ base, want string }{
		{"https://gitlab.com", "gitlab.com"},
		{"http://git.internal:3009", "git.internal:3009"},
		{"http://example.com/gitea", "example.com/gitea"},
		{"http://example.com/gitea/", "example.com/gitea"},
	}
	for _, tt := range cases {
		got, err := hostPrefix(tt.base)
		if err != nil || got != tt.want {
			t.Errorf("hostPrefix(%q) = %q, %v; want %q", tt.base, got, err, tt.want)
		}
	}
	if _, err := hostPrefix("not a url"); err == nil {
		t.Error("hostPrefix accepted a hostless string")
	}
}

func TestCloneAuthShapes(t *testing.T) {
	ctx := context.Background()
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name string
		conn config.Connection
		want string // expected basic-auth userinfo, "" = no auth args
	}{
		{"github pat", config.Connection{Type: "github", Token: "ghp_x"}, "x-access-token:ghp_x"},
		{"gitlab pat", config.Connection{Type: "gitlab", Token: "glpat_x"}, "oauth2:glpat_x"},
		{"gitea pat", config.Connection{Type: "gitea", Token: "gta_x"}, "gta_x:"},
		{"github anonymous", config.Connection{Type: "github"}, ""},
		{"git type", config.Connection{Type: "git", Token: ""}, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cloneAuth(ctx, tt.conn)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if got != nil {
					t.Fatalf("cloneAuth = %v, want none", got)
				}
				return
			}
			want := []string{"-c", "http.extraheader=Authorization: Basic " + b64(tt.want)}
			if !slices.Equal(got, want) {
				t.Errorf("cloneAuth = %v, want %v", got, want)
			}
		})
	}
}

// TestResync: remote connections are re-enqueued on the cadence; local ones
// are not (watch and boot-sync own local freshness).
func TestResync(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dataDir := t.TempDir()
	st, err := store.OpenLocalMemory(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	cfg := &config.Config{Connections: []config.Connection{
		{Name: "remote-gh", Type: "github", Users: []string{"u"}},
		{Name: "local-git", Type: "git", URL: "/tmp/somewhere"},
	}}

	rctx, rcancel := context.WithCancel(ctx)
	resyncDone := make(chan struct{})
	go func() {
		defer close(resyncDone)
		Resync(rctx, st, cfg, 10*time.Millisecond)
	}()
	defer func() {
		rcancel()
		<-resyncDone
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		jobs, err := st.ListJobs(ctx, store.JobSync, store.StatusPending)
		if err != nil {
			t.Fatal(err)
		}
		var targets []string
		for _, j := range jobs {
			targets = append(targets, j.Target)
		}
		if len(targets) > 0 {
			if targets[0] != "remote-gh" || len(targets) != 1 {
				t.Fatalf("resync enqueued %v, want just remote-gh", targets)
			}
			// let several more ticks fire before trusting the negative:
			// a broken IsRemote filter could enqueue local-git late
			time.Sleep(100 * time.Millisecond)
			jobs, err := st.ListJobs(ctx, store.JobSync, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, j := range jobs {
				if j.Target == "local-git" {
					t.Fatal("resync enqueued the local connection")
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("resync never enqueued a job")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
