// Package indexer builds zoekt shards via a same-SHA zoekt-git-index child
// (Epic 3). The child is compiled from this module's go.mod
// (`go build github.com/sourcegraph/zoekt/cmd/zoekt-git-index`, pinned by the
// go.mod tool directive), so reader/writer shard skew is structurally
// impossible (PLAN §1.1). The child process is the OOM isolation boundary.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		return executablePath(p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{
			filepath.Join(dir, "zoekt-git-index"),
			filepath.Join(dir, "bin", "zoekt-git-index"),
		} {
			if resolved, err := executablePath(p); err == nil {
				return resolved, nil
			}
		}
	}
	p, err := exec.LookPath("zoekt-git-index")
	if err != nil {
		return "", err
	}
	return executablePath(p)
}

// The index child changes its working directory to the bare mirror. Resolve
// configured relative paths before that chdir, and reject paths that cannot
// be executed, so successful startup proves later index jobs can launch.
func executablePath(candidate string) (string, error) {
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve zoekt-git-index path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve zoekt-git-index binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect zoekt-git-index binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("zoekt-git-index binary is not an executable regular file")
	}
	return resolved, nil
}

type Indexer struct {
	DataDir string // mirrors under DataDir/repos, shards under DataDir/index
	Bin     string // zoekt-git-index path (FindBinary)
	Store   store.Store
	// Verbose forwards the child indexer's line-oriented stdout/stderr and
	// parent phase transitions to Logger. Failure diagnostics retain only a
	// bounded tail regardless of this setting.
	Verbose bool
	// Logger receives verbose indexing output. Nil uses log.Default().
	Logger *log.Logger
	// Revisions is the validated per-repository selector -> full Git ref
	// allowlist. HEAD is always implicit and is never present in this map.
	Revisions map[string]map[string]string

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

// Index runs the child builder over the repo's bare mirror and records the
// exact revision set on success. Repositories without an allowlist retain the
// child's HEAD-only default.
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
	revisions, err := ix.resolveRevisions(ctx, dir, repo.Name, head)
	if err != nil {
		return fmt.Errorf("index %s: resolve revisions: %w", repo.Name, err)
	}
	if !force && head != "" && head == repo.IndexedCommitHash && revisionsEqual(revisions, repo.IndexedRevisions, repo.IndexedCommitHash) {
		ix.verbosef("index %s: already current at %s; skipping child", repo.Name, head)
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
	if len(revisions) > 1 {
		branches := make([]string, 0, len(revisions))
		for _, revision := range revisions {
			branches = append(branches, revision.Branch)
		}
		args = append(args, "-branches="+strings.Join(branches, ","))
	}
	ix.verbosef(
		"index %s: starting zoekt-git-index revisions=%d force=%t",
		repo.Name, len(revisions), force,
	)
	start := time.Now()
	cmd := exec.CommandContext(ctx, ix.Bin, append(args, dir)...)
	out := newChildOutput(ix.logger(), fmt.Sprintf("index %s: zoekt: ", repo.Name), ix.Verbose)
	cmd.Stdout, cmd.Stderr = out, out
	runErr := cmd.Run()
	out.Flush()
	if runErr != nil {
		err := runErr
		wrapped := fmt.Errorf("index %s: zoekt-git-index: %w\n%s", repo.Name, err, out.String())
		if ctx.Err() != nil {
			return fmt.Errorf("%v: %w", wrapped, ctx.Err())
		}
		return classifyChild(wrapped, err, out.String())
	}
	duration := time.Since(start)
	indexDuration.Observe(duration.Seconds())
	totalShardBytes := dirBytes(indexDir)
	shardBytes.Set(totalShardBytes)
	ix.verbosef(
		"index %s: zoekt-git-index complete duration=%s total_shard_bytes=%.0f",
		repo.Name, duration.Round(time.Millisecond), totalShardBytes,
	)
	if err := ix.Store.SetRepoIndexedRevisions(ctx, repo.Name, head, revisions, time.Now().UTC()); err != nil {
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
	ix.verbosef("index %s: committed index state at %s", repo.Name, head)
	return ix.afterIndexed(ctx, repo.Name, head)
}

func (ix *Indexer) logger() *log.Logger {
	if ix.Logger != nil {
		return ix.Logger
	}
	return log.Default()
}

func (ix *Indexer) verbosef(format string, args ...any) {
	if ix.Verbose {
		ix.logger().Printf(format, args...)
	}
}

func (ix *Indexer) resolveRevisions(ctx context.Context, dir, repoName, head string) ([]store.IndexedRevision, error) {
	resolved := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: head}}
	configured := ix.Revisions[repoName]
	selectors := make([]string, 0, len(configured))
	for selector := range configured {
		selectors = append(selectors, selector)
	}
	sort.Strings(selectors)
	for _, selector := range selectors {
		ref := configured[selector]
		commit, err := sync.ResolveCommit(ctx, dir, ref)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", selector, ref, err)
		}
		branch := ref
		if strings.HasPrefix(ref, "refs/tags/") {
			// zoekt's go-git reader otherwise resolves an annotated tag to the
			// tag object and rejects it as a non-commit. Keep the user-facing
			// selector stable while storing the exact peeled shard branch name.
			branch += "^{commit}"
		}
		resolved = append(resolved, store.IndexedRevision{Selector: selector, Branch: branch, Commit: commit})
	}
	return resolved, nil
}

// revisionsEqual accepts legacy HEAD-only rows with no indexed_revisions field
// so an upgrade does not rebuild every shard. Any configured or malformed
// multi-revision state requires a fresh atomic publication.
func revisionsEqual(want, got []store.IndexedRevision, legacyHead string) bool {
	if len(got) == 0 && len(want) == 1 {
		return want[0] == (store.IndexedRevision{Selector: "HEAD", Branch: "HEAD", Commit: legacyHead})
	}
	if len(want) != len(got) {
		return false
	}
	bySelector := make(map[string]store.IndexedRevision, len(got))
	for _, revision := range got {
		if revision.Selector == "" || revision.Branch == "" || revision.Commit == "" || bySelector[revision.Selector].Selector != "" {
			return false
		}
		bySelector[revision.Selector] = revision
	}
	for _, revision := range want {
		if bySelector[revision.Selector] != revision {
			return false
		}
	}
	return true
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
