// Package indexer builds zoekt shards via a same-SHA zoekt-git-index child
// (Epic 3). The child is compiled from this module's go.mod
// (`go build github.com/sourcegraph/zoekt/cmd/zoekt-git-index`, pinned by the
// go.mod tool directive), so reader/writer shard skew is structurally
// impossible (PLAN §1.1). The child process is the OOM isolation boundary.
package indexer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

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
	if !force && head != "" && head == repo.IndexedCommitHash {
		return nil // T3.2: HEAD unchanged, shards current
	}

	indexDir := filepath.Join(ix.DataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("index %s: %w", repo.Name, err)
	}
	args := []string{"-index", indexDir}
	if force {
		// defeat the child's own shard-freshness check too
		args = append(args, "-incremental=false")
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, ix.Bin, append(args, dir)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return classifyChild(fmt.Errorf("index %s: zoekt-git-index: %w\n%s", repo.Name, err, out.String()), err, out.String())
	}
	indexDuration.Observe(time.Since(start).Seconds())
	shardBytes.Set(dirBytes(indexDir))
	if err := ix.Store.SetRepoIndexed(ctx, repo.Name, head, time.Now().UTC()); err != nil {
		return fmt.Errorf("index %s: record state: %w", repo.Name, err)
	}
	return nil
}

// classifyChild tags child-builder failures per the T3.3 taxonomy: SIGKILL
// means the OOM reaper got it (the whole point of the process boundary);
// integrity complaints mean a corrupt shard.
func classifyChild(wrapped, raw error, output string) error {
	var ee *exec.ExitError
	if errors.As(raw, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL {
			return store.WithClass(store.ClassOOM, wrapped)
		}
	}
	lower := strings.ToLower(output)
	if strings.Contains(lower, "corrupt") || strings.Contains(lower, "checksum mismatch") {
		return store.WithClass(store.ClassCorrupt, wrapped)
	}
	return wrapped
}

func dirBytes(dir string) float64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !fi.IsDir() {
			total += fi.Size()
		}
	}
	return float64(total)
}

var (
	indexDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "phebs_index_duration_seconds",
		Help:    "Wall time of successful zoekt-git-index child runs.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 12), // 100ms .. ~3.4min
	})
	shardBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "phebs_index_shard_bytes",
		Help: "Total bytes of shard files under $DATA/index.",
	})
)
