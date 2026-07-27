package sync_test

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

func TestRepoName(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://github.com/sourcegraph/zoekt.git", "github.com/sourcegraph/zoekt", false},
		{"https://github.com/sourcegraph/zoekt", "github.com/sourcegraph/zoekt", false},
		{"https://gitlab.com/a/b/c.git", "gitlab.com/a/b/c", false},
		{"git@github.com:foo/bar.git", "github.com/foo/bar", false},
		{"ssh://git@github.com/foo/bar.git", "github.com/foo/bar", false}, // userinfo never lands in the name
		{"file:///tmp/origin", "local/tmp/origin", false},
		{"~/Uber/go-code", "local/Uber/go-code", false},
		{"~/Uber/go-code.git", "local/Uber/go-code", false},
		{"https://example.com/", "", true},
		{"not a url at all", "", true},
		// path traversal must be rejected: a .. segment would let RepoDir
		// escape $DATA and CleanupOrphans RemoveAll outside it.
		{"https://example.com/../../../../etc/x", "", true},
		{"git@example.com:../../../../etc/x", "", true},
		{"https://example.com/a/../b", "", true},
		{"https://example.com/a/./b", "", true},
		{"https://example.com/a//b", "", true},
		{"https://example.com/team.git/repo", "", true},
		{"https:user:secret@example.com/repo.git", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := sync.RepoName(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RepoName(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("RepoName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSafeRepoDirRejectsSymlinkedParent(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	root := filepath.Join(dataDir, "repos")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "example.com")); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.SafeRepoDir(dataDir, "example.com/team/repo"); err == nil {
		t.Fatal("SafeRepoDir accepted a symlinked namespace")
	}
}

// gitc runs git for fixture setup with identity flags.
func gitc(t testing.TB, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSyncGenericEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// fixture origin with one commit
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("one\n"), 0o644); err != nil {
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

	conn := config.Connection{Name: "fixture", Type: "git", URL: "file://" + origin}
	names, err := sync.SyncConnection(ctx, st, dataDir, conn, nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("synced names = %v, want one", names)
	}

	// repo row in DB
	name, _ := sync.RepoName(conn.URL)
	repo, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatalf("repo row missing after sync: %v", err)
	}
	if repo.DefaultBranch != "main" || repo.CloneURL != conn.URL {
		t.Errorf("repo row = %+v, want main branch and clone url", repo)
	}

	// bare mirror on disk, HEAD matches origin
	dir := sync.RepoDir(dataDir, name)
	if head, origHead := gitc(t, dir, "rev-parse", "HEAD"), gitc(t, origin, "rev-parse", "HEAD"); head != origHead {
		t.Errorf("mirror HEAD %s != origin HEAD %s", head, origHead)
	}

	// second commit + resync = incremental fetch picks it up
	if err := os.WriteFile(filepath.Join(origin, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "two")
	if _, err := sync.SyncConnection(ctx, st, dataDir, conn, nil); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if head, origHead := gitc(t, dir, "rev-parse", "HEAD"), gitc(t, origin, "rev-parse", "HEAD"); head != origHead {
		t.Errorf("after resync, mirror HEAD %s != origin HEAD %s", head, origHead)
	}
}

func TestSyncHomeRelativeEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	home := t.TempDir()
	t.Setenv("HOME", home)
	origin := filepath.Join(home, "Uber", "go-code")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "initial")

	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	conn := config.Connection{
		Name: "portable", Type: "git", URL: "~/Uber/go-code", Watch: true,
	}
	names, err := sync.SyncConnection(ctx, st, dataDir, conn, nil)
	if err != nil {
		t.Fatalf("sync home-relative repository: %v", err)
	}
	if len(names) != 1 || names[0] != "local/Uber/go-code" {
		t.Fatalf("synced names = %v, want stable home-relative identity", names)
	}
	if sync.IsRemote(conn) {
		t.Fatal("home-relative connection classified as remote")
	}

	repo, err := st.GetRepo(ctx, names[0])
	if err != nil {
		t.Fatal(err)
	}
	if repo.CloneURL != origin {
		t.Fatalf("persisted clone url = %q, want resolved absolute path %q", repo.CloneURL, origin)
	}
	if strings.Contains(repo.Name, filepath.Base(home)) || strings.Contains(repo.Name, "~") {
		t.Fatalf("repository identity leaked machine home: %q", repo.Name)
	}
	mirror := sync.RepoDir(dataDir, names[0])
	if got, want := gitc(t, mirror, "rev-parse", "HEAD"), gitc(t, origin, "rev-parse", "HEAD"); got != want {
		t.Fatalf("mirror HEAD = %s, want %s", got, want)
	}

	gitc(t, origin, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(origin, "feature.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "feature")
	if _, err := sync.SyncConnection(ctx, st, dataDir, conn, nil); err != nil {
		t.Fatalf("resync home-relative repository: %v", err)
	}
	repo, err = st.GetRepo(ctx, names[0])
	if err != nil {
		t.Fatal(err)
	}
	if repo.DefaultBranch != "feature" {
		t.Fatalf("default branch after source switch = %q, want feature", repo.DefaultBranch)
	}
	if got, want := gitc(t, mirror, "rev-parse", "HEAD"), gitc(t, origin, "rev-parse", "HEAD"); got != want {
		t.Fatalf("mirror HEAD after source switch = %s, want %s", got, want)
	}
}

func TestRepoNameHomeRelativeIsHomeIndependent(t *testing.T) {
	var names []string
	for _, home := range []string{t.TempDir(), t.TempDir()} {
		t.Setenv("HOME", home)
		name, err := sync.RepoName("~/Uber/go-code")
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if names[0] != "local/Uber/go-code" || names[1] != names[0] {
		t.Fatalf("home-relative identities = %v, want one stable identity", names)
	}
}

// TestOrchestration drives EnqueueMissing → Handler → membership → orphan
// cleanup, the exact path serve wires up.
func TestOrchestration(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")

	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	cfg := &config.Config{
		Server:      config.Server{DataDir: dataDir},
		Sync:        config.Sync{CleanupOrphans: true},
		Connections: []config.Connection{{Name: "c1", Type: "git", URL: "file://" + origin}},
	}

	// boot enqueue is idempotent while a job is in flight
	if err := sync.EnqueueMissing(ctx, st, cfg); err != nil {
		t.Fatal(err)
	}
	if err := sync.EnqueueMissing(ctx, st, cfg); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.ListJobs(ctx, store.JobSync, store.StatusPending)
	if len(pending) != 1 {
		t.Fatalf("pending jobs = %d, want 1 (dedupe)", len(pending))
	}

	// execute like the runner would
	job, err := st.ClaimJob(ctx, store.JobSync, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := sync.Handler(cfg, st)(ctx, *job); err != nil {
		t.Fatalf("handler: %v", err)
	}

	name, _ := sync.RepoName("file://" + origin)
	statuses, err := st.RepoStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Name != name || statuses[0].Orphaned {
		t.Fatalf("statuses = %+v, want %s owned by c1", statuses, name)
	}

	// connection dropped from config → prune → orphan cleanup removes row + mirror
	cfg.Connections = nil
	if err := st.PruneConnections(ctx, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := sync.CleanupOrphans(ctx, st, dataDir); err != nil {
		t.Fatal(err)
	}
	if repos, _ := st.ListRepos(ctx); len(repos) != 0 {
		t.Errorf("repos after cleanup = %+v, want none", repos)
	}
	if _, err := os.Stat(sync.RepoDir(dataDir, name)); !os.IsNotExist(err) {
		t.Errorf("mirror still on disk after cleanup: %v", err)
	}
}

func TestSyncUnsupportedType(t *testing.T) {
	_, err := sync.SyncConnection(context.Background(), nil, t.TempDir(),
		config.Connection{Name: "x", Type: "svn"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("err = %v, want unsupported type", err)
	}
}

func TestRemoveShards(t *testing.T) {
	dataDir := t.TempDir()
	idx := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	// zoekt names shards url.QueryEscape(name)_v<ver>.<n>.zoekt
	write := func(fname string) {
		if err := os.WriteFile(filepath.Join(idx, fname), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	esc := url.QueryEscape("h/foo")       // h%2Ffoo
	escBar := url.QueryEscape("h/foobar") // h%2Ffoobar (foo is a prefix)
	escVictim := url.QueryEscape("h/foo_victim")
	write(esc + "_v16.00000.zoekt")
	write(esc + "_v16.00001.zoekt")
	write(escBar + "_v16.00000.zoekt")
	write(escVictim + "_v16.00000.zoekt")

	if err := sync.RemoveShards(dataDir, "h/foo"); err != nil {
		t.Fatal(err)
	}
	left, _ := filepath.Glob(filepath.Join(idx, "*.zoekt"))
	if len(left) != 2 {
		t.Fatalf("after RemoveShards(h/foo), remaining = %v; want both neighboring repos", left)
	}
	remaining := map[string]bool{}
	for _, path := range left {
		remaining[filepath.Base(path)] = true
	}
	for _, want := range []string{escBar + "_v16.00000.zoekt", escVictim + "_v16.00000.zoekt"} {
		if !remaining[want] {
			t.Errorf("after RemoveShards(h/foo), remaining = %v; missing %s", left, want)
		}
	}
	if err := sync.RemoveShards(dataDir, "h/foo"); err != nil {
		t.Errorf("second RemoveShards errored: %v", err)
	}

	longName := "gitlab.example.com/" + strings.Repeat("nested/", 35) + "repo"
	prefix := url.QueryEscape(longName)
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(prefix)))
	longShard := prefix[:200] + hash[:8] + "_v16.00000.zoekt"
	write(longShard)
	if err := sync.RemoveShards(dataDir, longName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(idx, longShard)); !os.IsNotExist(err) {
		t.Errorf("long-name shard still exists after cleanup: %v", err)
	}
}

func TestRemoveShardsTreatsDataDirMetacharactersLiterally(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data[*]")
	sibling := filepath.Join(base, "dataX")
	name := url.QueryEscape("h/foo") + "_v16.00000.zoekt"
	for _, root := range []string{dataDir, sibling} {
		if err := os.MkdirAll(filepath.Join(root, "index"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "index", name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := sync.RemoveShards(dataDir, "h/foo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "index", name)); !os.IsNotExist(err) {
		t.Fatalf("literal data-dir shard was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sibling, "index", name)); err != nil {
		t.Fatalf("sibling shard was touched through glob metacharacters: %v", err)
	}
}

func TestRemoveShardsRejectsSymlinkedIndexEntry(t *testing.T) {
	dataDir := t.TempDir()
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.zoekt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(indexDir, "h%2Ffoo_v1.zoekt")); err != nil {
		t.Fatal(err)
	}
	if err := sync.RemoveShards(dataDir, "h/foo"); err == nil {
		t.Fatal("RemoveShards accepted a symlinked shard")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("outside shard target was touched: %v", err)
	}
}
