package sync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	cfg := &config.Config{
		Server:      config.Server{DataDir: dataDir},
		Connections: []config.Connection{{Name: "c1", Type: "git", URL: origin}},
	}
	// initial sync creates the mirror and membership
	names, err := SyncConnection(ctx, st, dataDir, cfg.Connections[0])
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

	// a repo no connection claims must fail, not fetch unauthenticated
	if err := FetchHandler(cfg, st)(ctx, store.Job{Target: "github.com/x/y"}); err == nil {
		t.Error("fetch of unknown repo succeeded, want error")
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
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	cfg := &config.Config{Connections: []config.Connection{
		{Name: "remote-gh", Type: "github", Users: []string{"u"}},
		{Name: "local-git", Type: "git", URL: "/tmp/somewhere"},
	}}

	rctx, rcancel := context.WithCancel(ctx)
	defer rcancel()
	go Resync(rctx, st, cfg, 10*time.Millisecond)

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
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("resync never enqueued a job")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
