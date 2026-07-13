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

	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

// FindBinary locates zoekt-git-index: env override, next to our executable,
// ./bin beside the executable (the `make build` layout), then PATH.
func FindBinary() (string, error) {
	if p := os.Getenv("PHEBS_ZOEKT_GIT_INDEX"); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{
			filepath.Join(dir, "zoekt-git-index"),
			filepath.Join(dir, "bin", "zoekt-git-index"),
		} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return exec.LookPath("zoekt-git-index")
}

type Indexer struct {
	DataDir string // mirrors under DataDir/repos, shards under DataDir/index
	Bin     string // zoekt-git-index path (FindBinary)
	Store   store.Store

	// OnIndexed, when set, runs once the indexed state is known current — the
	// index→extract chain hook (T12.2), mirroring how sync chains index. It also
	// runs on an unchanged-HEAD short circuit: if persisting the successor job
	// failed after a prior index commit, the retried index job must be able to
	// repair that missing chain without rebuilding the shard.
	OnIndexed func(ctx context.Context, repoName, commit string) error
}

// Handle adapts Index to the store.Runner: the job target is the repo name.
func (ix *Indexer) Handle(ctx context.Context, job store.Job) error {
	return ix.Index(ctx, store.Repo{Name: job.Target}, job.Force)
}

// Index runs the child builder over the repo's bare mirror (HEAD-only, the
// child's default) and records the indexed commit on success.
func (ix *Indexer) Index(ctx context.Context, repo store.Repo, force bool) error {
	target := repo.Name
	dir, err := sync.SafeRepoDir(ix.DataDir, target)
	if err != nil {
		return fmt.Errorf("index %s: %w", target, err)
	}
	unlock, err := repowork.LockContext(ctx, dir)
	if err != nil {
		return fmt.Errorf("index %s: lock mirror: %w", target, err)
	}
	defer unlock()

	fresh, err := ix.Store.GetRepo(ctx, target)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("index %s: reload repo: %w", target, err)
	}
	if fresh.Deleting {
		return nil
	}
	if fresh.Name != target {
		return fmt.Errorf("index %s: stored repository name mismatch", target)
	}
	repo = *fresh

	head, err := sync.Head(ctx, dir)
	if err != nil {
		return fmt.Errorf("index %s: resolve HEAD: %w", repo.Name, err)
	}
	if !force && head != "" && head == repo.IndexedCommitHash {
		return ix.afterIndexed(ctx, repo.Name, head) // T3.2: shards current; repair/confirm the chain
	}

	indexDir := filepath.Join(ix.DataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("index %s: %w", repo.Name, err)
	}
	// zoekt.name makes shard repo names equal store names, which the T4.1
	// RepoSet pre-pass depends on; the child reads it from the repo config
	if _, err := sync.GitConfig(ctx, dir, "zoekt.name", repo.Name); err != nil {
		return fmt.Errorf("index %s: set zoekt.name: %w", repo.Name, err)
	}
	// Always -incremental=false. phebs's own short-circuit above (indexed hash
	// == HEAD) already skips redundant runs, so by the time we invoke the
	// child a real build is wanted. Leaving zoekt's own incremental skip on
	// silently no-ops a force job when HEAD and the on-disk shard are unchanged.
	args := []string{"-index", indexDir, "-incremental=false"}
	start := time.Now()
	cmd := exec.CommandContext(ctx, ix.Bin, append(args, dir)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		wrapped := fmt.Errorf("index %s: zoekt-git-index: %w\n%s", repo.Name, err, out.String())
		if ctx.Err() != nil {
			return fmt.Errorf("%v: %w", wrapped, ctx.Err())
		}
		return classifyChild(wrapped, err, out.String())
	}
	indexDuration.Observe(time.Since(start).Seconds())
	shardBytes.Set(dirBytes(indexDir))
	if err := ix.Store.SetRepoIndexed(ctx, repo.Name, head, time.Now().UTC()); err != nil {
		// The child has already replaced the shard. If the DB commit fails,
		// remove both sides of the claimed state so search cannot serve revision
		// B while MCP defaults to the previously recorded revision A.
		clearErr := ix.Store.ClearRepoIndexState(ctx, repo.Name)
		removeErr := sync.RemoveShards(ix.DataDir, repo.Name)
		return errors.Join(
			fmt.Errorf("index %s: record state: %w", repo.Name, err),
			wrapIfError("clear index state", clearErr),
			wrapIfError("remove uncommitted shards", removeErr),
		)
	}
	return ix.afterIndexed(ctx, repo.Name, head)
}

func (ix *Indexer) afterIndexed(ctx context.Context, repoName, commit string) error {
	if ix.OnIndexed == nil {
		return nil
	}
	if err := ix.OnIndexed(ctx, repoName, commit); err != nil {
		return fmt.Errorf("index %s: chain post-index work: %w", repoName, err)
	}
	return nil
}

func wrapIfError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
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
