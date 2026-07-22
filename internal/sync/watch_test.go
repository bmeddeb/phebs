package sync_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

type watchQueueStore struct {
	store.Store
	mu      stdsync.Mutex
	pending bool
}

func (s *watchQueueStore) EnqueuePending(_ context.Context, kind store.JobKind, target string, force bool) (*store.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = true
	return &store.Job{Kind: kind, Target: target, Force: force, Status: store.StatusPending}, nil
}

func (s *watchQueueStore) ListJobs(_ context.Context, kind store.JobKind, status store.JobStatus) ([]store.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pending || status != store.StatusPending {
		return nil, nil
	}
	return []store.Job{{Kind: kind, Target: "w", Status: store.StatusPending}}, nil
}

func (s *watchQueueStore) CancelPendingJobs(context.Context, store.JobKind, string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pending {
		return 0, nil
	}
	s.pending = false
	return 1, nil
}

func TestRepoNamePlainPath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"/Users/ben/src/myproject", "local/Users/ben/src/myproject", false},
		{"/Users/ben/src/myproject.git", "local/Users/ben/src/myproject", false},
		{"/a/b/../c", "local/a/c", false},
		{"/", "", true},
	}
	for _, tt := range tests {
		got, err := sync.RepoName(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("RepoName(%q) = %q, %v; want %q, wantErr %v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

// A watched mirror follows the branch the source repo has checked out.
func TestSyncFollowsSourceBranch(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "on main")

	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	conn := config.Connection{Name: "w", Type: "git", URL: origin, Watch: true} // plain path
	if _, err := sync.SyncConnection(ctx, st, dataDir, conn, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	name, _ := sync.RepoName(origin)
	repo, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if repo.DefaultBranch != "main" {
		t.Fatalf("branch = %q, want main", repo.DefaultBranch)
	}

	// switch the source to a feature branch with a new commit
	gitc(t, origin, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(origin, "b.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "on feature")

	if _, err := sync.SyncConnection(ctx, st, dataDir, conn, nil); err != nil {
		t.Fatalf("resync: %v", err)
	}
	repo, _ = st.GetRepo(ctx, name)
	if repo.DefaultBranch != "feature" {
		t.Errorf("after branch switch, mirror branch = %q, want feature", repo.DefaultBranch)
	}
	dir := sync.RepoDir(dataDir, name)
	if head, srcHead := gitc(t, dir, "rev-parse", "HEAD"), gitc(t, origin, "rev-parse", "HEAD"); head != srcHead {
		t.Errorf("mirror HEAD %s != source HEAD %s", head, srcHead)
	}
}

// The watcher enqueues exactly one sync when HEAD moves, none when idle.
func TestWatcherEnqueuesOnHeadMove(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "one")

	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if _, err := st.EnqueuePending(ctx, store.JobSync, "w", false); err != nil {
		t.Fatal(err)
	}

	conn := config.Connection{Name: "w", Type: "git", URL: origin, Watch: true}
	w := &sync.Watcher{Store: st, Conns: []config.Connection{conn}, Interval: 40 * time.Millisecond}
	go w.Run(ctx)

	pendingCount := func() int {
		jobs, err := st.ListJobs(ctx, store.JobSync, store.StatusPending)
		if err != nil {
			return -1
		}
		return len(jobs)
	}

	// idle: several ticks, the watcher reuses the boot-time pending job
	time.Sleep(300 * time.Millisecond)
	if n := pendingCount(); n != 1 {
		t.Fatalf("idle watcher has %d pending jobs, want the single boot job", n)
	}

	// commit → exactly one sync job, deduped across subsequent ticks
	if err := os.WriteFile(filepath.Join(origin, "b.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "two")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && pendingCount() == 0 {
		time.Sleep(25 * time.Millisecond)
	}
	if n := pendingCount(); n != 1 {
		t.Fatalf("after HEAD move, pending = %d, want 1", n)
	}
	time.Sleep(300 * time.Millisecond) // more ticks; dedupe must hold
	if n := pendingCount(); n != 1 {
		t.Errorf("dedupe failed: pending = %d, want 1", n)
	}
}

func TestWatcherEnqueuesWhenAllowlistedRefMovesWithoutHEAD(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "one")
	gitc(t, origin, "branch", "release")
	repoName, err := sync.RepoName(origin)
	if err != nil {
		t.Fatal(err)
	}

	st := &watchQueueStore{}
	conn := config.Connection{Name: "w", Type: "git", URL: origin, Watch: true}
	w := &sync.Watcher{
		Store: st, Conns: []config.Connection{conn}, Interval: 30 * time.Millisecond,
		Revisions: config.RevisionAllowlist{repoName: {"release": "refs/heads/release"}},
	}
	go w.Run(ctx)
	pendingCount := func() int {
		jobs, err := st.ListJobs(ctx, store.JobSync, store.StatusPending)
		if err != nil {
			return -1
		}
		return len(jobs)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && pendingCount() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if n := pendingCount(); n != 1 {
		t.Fatalf("initial watcher baseline pending = %d, want 1", n)
	}
	if _, err := st.CancelPendingJobs(ctx, store.JobSync, conn.Name); err != nil {
		t.Fatal(err)
	}

	// Create and install a commit directly on release. The checked-out HEAD
	// remains main throughout, so only the allowlisted-ref fingerprint moves.
	tree := gitc(t, origin, "rev-parse", "refs/heads/release^{tree}")
	commit := gitc(t, origin, "commit-tree", tree, "-p", "refs/heads/release", "-m", "move release")
	gitc(t, origin, "update-ref", "refs/heads/release", commit)
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && pendingCount() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if n := pendingCount(); n != 1 {
		t.Fatalf("allowlisted ref move pending = %d, want 1", n)
	}
}

func TestWatchConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // error substring; "" = valid
	}{
		{"local path ok", "connections:\n  - {name: a, type: git, url: /tmp/x, watch: true}\n", ""},
		{"file url ok", "connections:\n  - {name: a, type: git, url: file:///tmp/x, watch: true}\n", ""},
		{"remote url rejected", "connections:\n  - {name: a, type: git, url: https://github.com/a/b.git, watch: true}\n", "watch requires a local url"},
		{"github rejected", "connections:\n  - {name: a, type: github, users: [u], watch: true}\n", "watch is only valid for local git"},
		{"bad poll interval", "sync:\n  poll_interval: fast\n", "not a positive Go duration"},
		{"good poll interval", "sync:\n  poll_interval: 2s\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse([]byte(tt.in))
			if tt.want == "" && err != nil {
				t.Fatalf("Parse err = %v, want nil", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("Parse err = %v, want substring %q", err, tt.want)
			}
		})
	}
}
