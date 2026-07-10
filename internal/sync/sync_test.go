package sync_test

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
		{"https://example.com/", "", true},
		{"not a url at all", "", true},
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

// gitc runs git for fixture setup with identity flags.
func gitc(t *testing.T, dir string, args ...string) string {
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
	if err := sync.SyncConnection(ctx, st, dataDir, conn); err != nil {
		t.Fatalf("first sync: %v", err)
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
	if err := sync.SyncConnection(ctx, st, dataDir, conn); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if head, origHead := gitc(t, dir, "rev-parse", "HEAD"), gitc(t, origin, "rev-parse", "HEAD"); head != origHead {
		t.Errorf("after resync, mirror HEAD %s != origin HEAD %s", head, origHead)
	}
}

func TestSyncUnsupportedType(t *testing.T) {
	err := sync.SyncConnection(context.Background(), nil, t.TempDir(),
		config.Connection{Name: "x", Type: "svn"})
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("err = %v, want unsupported type", err)
	}
}
