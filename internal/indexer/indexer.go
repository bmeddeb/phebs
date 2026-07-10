// Package indexer builds zoekt shards via a same-SHA zoekt-git-index child
// (Epic 3). The child is compiled from this module's go.mod
// (`go build github.com/sourcegraph/zoekt/cmd/zoekt-git-index`, pinned by the
// go.mod tool directive), so reader/writer shard skew is structurally
// impossible (PLAN §1.1). The child process is the OOM isolation boundary.
package indexer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

// FindBinary locates zoekt-git-index: env override, next to our executable,
// then PATH. `make dev` builds it and sets the env var.
func FindBinary() (string, error) {
	if p := os.Getenv("PHEBS_ZOEKT_GIT_INDEX"); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "zoekt-git-index")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return exec.LookPath("zoekt-git-index")
}

type Indexer struct {
	DataDir string // mirrors under DataDir/repos, shards under DataDir/index
	Bin     string // zoekt-git-index path (FindBinary)
	Store   store.Store
}

// Handle adapts Index to the store.Runner: the job target is the repo name.
func (ix *Indexer) Handle(ctx context.Context, job store.Job) error {
	repo, err := ix.Store.GetRepo(ctx, job.Target)
	if err != nil {
		return err
	}
	return ix.Index(ctx, *repo, false)
}

// Index runs the child builder over the repo's bare mirror (HEAD-only, the
// child's default) and records the indexed commit on success.
func (ix *Indexer) Index(ctx context.Context, repo store.Repo, force bool) error {
	dir := sync.RepoDir(ix.DataDir, repo.Name)
	head, err := sync.Head(ctx, dir)
	if err != nil {
		return fmt.Errorf("index %s: resolve HEAD: %w", repo.Name, err)
	}

	indexDir := filepath.Join(ix.DataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("index %s: %w", repo.Name, err)
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, ix.Bin, "-index", indexDir, dir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("index %s: zoekt-git-index: %w\n%s", repo.Name, err, out.String())
	}
	if err := ix.Store.SetRepoIndexed(ctx, repo.Name, head, time.Now().UTC()); err != nil {
		return fmt.Errorf("index %s: record state: %w", repo.Name, err)
	}
	_ = start // duration metric lands with T3.3
	return nil
}
