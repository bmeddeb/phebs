package indexer_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

// TestMain ensures a zoekt-git-index binary exists: discovery first, else a
// one-off module-aware build into a temp dir (cached by the go build cache).
func TestMain(m *testing.M) {
	if _, err := indexer.FindBinary(); err != nil {
		dir, err := os.MkdirTemp("", "phebs-zoekt")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		bin := filepath.Join(dir, "zoekt-git-index")
		out, err := exec.Command("go", "build", "-o", bin,
			"github.com/sourcegraph/zoekt/cmd/zoekt-git-index").CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build zoekt-git-index: %v\n%s", err, out)
			os.Exit(1)
		}
		_ = os.Setenv("PHEBS_ZOEKT_GIT_INDEX", bin)
	}
	os.Exit(m.Run())
}

func gitc(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixture builds an origin repo, mirrors it into dataDir, and upserts the row.
func fixture(t *testing.T, ctx context.Context, st store.Store, dataDir string) (name, head string) {
	t.Helper()
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "main.go"),
		[]byte("package main\n\nfunc phebsNeedle() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "one")

	name, err := sync.RepoName("file://" + origin)
	if err != nil {
		t.Fatal(err)
	}
	if err := sync.Mirror(ctx, "file://"+origin, sync.RepoDir(dataDir, name)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRepo(ctx, store.Repo{Name: name, CloneURL: "file://" + origin}); err != nil {
		t.Fatal(err)
	}
	return name, gitc(t, origin, "rev-parse", "HEAD")
}

func newIndexer(t *testing.T, ctx context.Context) (*indexer.Indexer, store.Store, string) {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	bin, err := indexer.FindBinary()
	if err != nil {
		t.Fatal(err)
	}
	return &indexer.Indexer{DataDir: dataDir, Bin: bin, Store: st}, st, dataDir
}

func shardCount(t *testing.T, dataDir string) int {
	t.Helper()
	shards, err := filepath.Glob(filepath.Join(dataDir, "index", "*.zoekt"))
	if err != nil {
		t.Fatal(err)
	}
	return len(shards)
}

// T3.1 AC: index a synced mirror through the job system; a shard appears and
// the repo row records the indexed commit.
func TestIndexViaJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, head := fixture(t, ctx, st, dataDir)

	if _, err := st.CreateJob(ctx, store.JobIndex, name); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimJob(ctx, store.JobIndex, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Handle(ctx, *job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if n := shardCount(t, dataDir); n == 0 {
		t.Error("no shard files under $DATA/index")
	}
	repo, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if repo.IndexedCommitHash != head || repo.IndexedAt == nil || repo.LatestJobStatus != "done" {
		t.Errorf("repo state = %+v, want indexed at %s", repo, head)
	}
}

// shardStamps maps shard file → mtime, to prove a no-op touched nothing.
func shardStamps(t *testing.T, dataDir string) map[string]time.Time {
	t.Helper()
	shards, err := filepath.Glob(filepath.Join(dataDir, "index", "*.zoekt"))
	if err != nil {
		t.Fatal(err)
	}
	stamps := map[string]time.Time{}
	for _, s := range shards {
		fi, err := os.Stat(s)
		if err != nil {
			t.Fatal(err)
		}
		stamps[s] = fi.ModTime()
	}
	return stamps
}

// T3.2 AC: reindexing an unchanged HEAD is a no-op in <100ms that leaves
// shards untouched; force rebuilds anyway.
func TestShortCircuitAndForce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, _ := fixture(t, ctx, st, dataDir)

	repo, _ := st.GetRepo(ctx, name)
	if err := ix.Index(ctx, *repo, false); err != nil {
		t.Fatal(err)
	}
	before := shardStamps(t, dataDir)
	if len(before) == 0 {
		t.Fatal("no shards after first index")
	}

	repo, _ = st.GetRepo(ctx, name) // re-read: row now carries indexed_commit_hash
	start := time.Now()
	if err := ix.Index(ctx, *repo, false); err != nil {
		t.Fatal(err)
	}
	if noop := time.Since(start); noop > 100*time.Millisecond {
		t.Errorf("no-op reindex took %v, want <100ms", noop)
	}
	for s, mt := range shardStamps(t, dataDir) {
		if !mt.Equal(before[s]) {
			t.Errorf("no-op touched shard %s", s)
		}
	}

	time.Sleep(1100 * time.Millisecond) // ensure a distinguishable shard mtime
	if err := ix.Index(ctx, *repo, true); err != nil {
		t.Fatalf("forced: %v", err)
	}
	rebuilt := false
	for s, mt := range shardStamps(t, dataDir) {
		if !mt.Equal(before[s]) {
			rebuilt = true
		}
	}
	if !rebuilt {
		t.Error("force did not rebuild any shard")
	}
}

func TestIndexMissingRepo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ix, _, _ := newIndexer(t, ctx)

	err := ix.Index(ctx, store.Repo{Name: "github.com/no/such"}, false)
	if err == nil || !strings.Contains(err.Error(), "resolve HEAD") {
		t.Errorf("err = %v, want resolve HEAD failure", err)
	}
}
