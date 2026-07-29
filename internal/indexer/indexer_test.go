package indexer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/repowork"
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
	if _, err := focusedindex.FindBinary(); err != nil {
		dir, err := os.MkdirTemp("", "phebs-focused-index")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		bin := filepath.Join(dir, "phebs-focused-index")
		out, err := exec.Command("go", "build", "-o", bin,
			"github.com/bmeddeb/phebs/cmd/phebs-focused-index").CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build phebs-focused-index: %v\n%s", err, out)
			os.Exit(1)
		}
		_ = os.Setenv("PHEBS_FOCUSED_INDEX", bin)
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
	focusedBin, err := focusedindex.FindBinary()
	if err != nil {
		t.Fatal(err)
	}
	return &indexer.Indexer{
		DataDir: dataDir, Bin: bin, FocusedBin: focusedBin, Store: st,
	}, st, dataDir
}

func shardCount(t *testing.T, dataDir string) int {
	t.Helper()
	shards, err := filepath.Glob(filepath.Join(dataDir, "index", "*.zoekt"))
	if err != nil {
		t.Fatal(err)
	}
	return len(shards)
}

type failIndexedStore struct{ store.Store }

func (failIndexedStore) SetRepoIndexed(context.Context, string, string, time.Time) error {
	return errors.New("injected index state failure")
}

func (failIndexedStore) SetRepoIndexedRevisions(context.Context, string, string, []store.IndexedRevision, time.Time) error {
	return errors.New("injected index state failure")
}

func (failIndexedStore) SetRepoIndexedState(context.Context, string, string, []store.IndexedRevision, *analysisunit.State, time.Time) error {
	return errors.New("injected index state failure")
}

type indexLogStore struct {
	store.Store
	repo store.Repo
}

func (s *indexLogStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	if name != s.repo.Name {
		return nil, store.ErrNotFound
	}
	repo := s.repo
	return &repo, nil
}

func (s *indexLogStore) SetRepoIndexedRevisions(_ context.Context, name, defaultCommit string, revisions []store.IndexedRevision, at time.Time) error {
	return s.SetRepoIndexedState(
		context.Background(), name, defaultCommit, revisions, nil, at,
	)
}

func (s *indexLogStore) SetRepoIndexedState(
	_ context.Context,
	name,
	defaultCommit string,
	revisions []store.IndexedRevision,
	unit *analysisunit.State,
	at time.Time,
) error {
	if name != s.repo.Name {
		return store.ErrNotFound
	}
	s.repo.IndexedCommitHash = defaultCommit
	s.repo.IndexedRevisions = append([]store.IndexedRevision(nil), revisions...)
	s.repo.IndexedAnalysisUnit = analysisunit.CloneState(unit)
	s.repo.IndexedAt = &at
	return nil
}

func TestIndexStateFailureRemovesUncommittedShard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, _ := fixture(t, ctx, st, dataDir)
	ix.Store = failIndexedStore{Store: st}

	err := ix.Index(ctx, store.Repo{Name: name}, false)
	if err == nil || !strings.Contains(err.Error(), "injected index state failure") {
		t.Fatalf("Index error = %v, want injected store failure", err)
	}
	if got := shardCount(t, dataDir); got != 0 {
		t.Fatalf("shards after failed state commit = %d, want 0", got)
	}
	repo, getErr := st.GetRepo(ctx, name)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if repo.IndexedCommitHash != "" {
		t.Fatalf("indexed hash after failed state commit = %q, want empty", repo.IndexedCommitHash)
	}
}

func TestFocusedChildFailurePreservesOldPublicationAndStateFailureCleansNew(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, _ := fixture(t, ctx, st, dataDir)
	scope := analysisunit.Scope{
		Repository: name, Name: "service", Primary: []string{"main.go"},
	}
	ix.AnalysisUnits = map[string]analysisunit.Scope{name: scope}
	repo, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Index(ctx, *repo, false); err != nil {
		t.Fatal(err)
	}
	committed, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	before := shardStamps(t, dataDir)
	t.Setenv("PHEBS_FOCUSED_INJECT_FAILURE", "1")
	if err := ix.Index(ctx, *committed, true); err == nil {
		t.Fatal("injected focused child failure succeeded")
	}
	afterFailure, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if !analysisunit.EqualState(
		afterFailure.IndexedAnalysisUnit, committed.IndexedAnalysisUnit,
	) || !reflect.DeepEqual(shardStamps(t, dataDir), before) {
		t.Fatal("focused child failure changed committed state or old publication")
	}

	t.Setenv("PHEBS_FOCUSED_INJECT_FAILURE", "")
	changed := scope
	changed.Name = "replacement"
	ix.AnalysisUnits[name] = changed
	ix.Store = failIndexedStore{Store: st}
	if err := ix.Index(ctx, *afterFailure, false); err == nil ||
		!strings.Contains(err.Error(), "injected index state failure") {
		t.Fatalf("focused state failure = %v", err)
	}
	if got := shardCount(t, dataDir); got != 0 {
		t.Fatalf("focused shards after failed state commit = %d, want 0", got)
	}
	if _, err := os.Lstat(filepath.Join(
		dataDir, "index", focusedindex.ManifestName(name),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("focused manifest survived failed state commit: %v", err)
	}
	cleared, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.IndexedCommitHash != "" || cleared.IndexedAnalysisUnit != nil {
		t.Fatalf("focused state survived failed commit: %+v", cleared)
	}
}

func TestIndexVerboseForwardsChildOutputOnlyWhenEnabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "one")

	name, err := sync.RepoName("file://" + origin)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err := sync.Mirror(ctx, "file://"+origin, sync.RepoDir(dataDir, name)); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "verbose-indexer")
	realBin, err := indexer.FindBinary()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nprintf 'wrapper-child-marker\\n'\nexec " + realBin + " \"$@\"\n"
	if err := os.WriteFile(bin, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, verbose := range []bool{false, true} {
		t.Run(fmt.Sprintf("verbose=%t", verbose), func(t *testing.T) {
			var logs bytes.Buffer
			st := &indexLogStore{repo: store.Repo{Name: name}}
			ix := &indexer.Indexer{
				DataDir: dataDir,
				Bin:     bin,
				Store:   st,
				Verbose: verbose,
				Logger:  log.New(&logs, "", 0),
			}
			if err := ix.Index(ctx, st.repo, true); err != nil {
				t.Fatal(err)
			}
			hasMarker := strings.Contains(logs.String(), "wrapper-child-marker")
			if hasMarker != verbose {
				t.Fatalf("child marker present = %t, want %t; logs=%q", hasMarker, verbose, logs.String())
			}
			hasParentPhase := strings.Contains(logs.String(), "starting zoekt-git-index")
			if hasParentPhase != verbose {
				t.Fatalf("parent phase present = %t, want %t; logs=%q", hasParentPhase, verbose, logs.String())
			}
		})
	}
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

// Regression: a force bit persisted on an index job must reach Handle and
// rebuild the shard even when HEAD is unchanged.
func TestForceReindexViaJobRebuilds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, head := fixture(t, ctx, st, dataDir)

	// first index establishes the shard + recorded commit
	if err := ix.Index(ctx, store.Repo{Name: name}, false); err != nil {
		t.Fatal(err)
	}
	before := shardStamps(t, dataDir)
	if len(before) == 0 {
		t.Fatal("no shard after first index")
	}
	time.Sleep(1100 * time.Millisecond) // distinguishable mtime

	// simulate POST /api/reindex {force:true}: enqueue/upgrade, then drain
	if _, err := st.EnqueuePending(ctx, store.JobIndex, name, true); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimJob(ctx, store.JobIndex, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Handle(ctx, *job); err != nil { // HEAD unchanged since first index
		t.Fatalf("Handle: %v", err)
	}

	rebuilt := false
	for s, mt := range shardStamps(t, dataDir) {
		if !mt.Equal(before[s]) {
			rebuilt = true
		}
	}
	if !rebuilt {
		t.Error("force reindex via job did not rebuild the shard (HEAD unchanged, zoekt skipped)")
	}
	repo, _ := st.GetRepo(ctx, name)
	if repo.IndexedCommitHash != head {
		t.Errorf("indexed hash = %q, want re-recorded %q", repo.IndexedCommitHash, head)
	}
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

func TestAnalysisUnitScopeChangeRebuildsSameHead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, _ := fixture(t, ctx, st, dataDir)
	firstScope := analysisunit.Scope{
		Repository: name,
		Name:       "service",
		Primary:    []string{"main.go"},
	}
	ix.AnalysisUnits = map[string]analysisunit.Scope{name: firstScope}
	repo, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Index(ctx, *repo, false); err != nil {
		t.Fatal(err)
	}
	first, err := st.GetRepo(ctx, name)
	if err != nil || first.IndexedAnalysisUnit == nil {
		t.Fatalf("first unit state = %+v, %v", first, err)
	}
	firstDigest := first.IndexedAnalysisUnit.Digest
	before := shardStamps(t, dataDir)

	time.Sleep(1100 * time.Millisecond)
	changedScope := firstScope
	changedScope.Name = "renamed-service"
	ix.AnalysisUnits[name] = changedScope
	if err := ix.Index(ctx, *first, false); err != nil {
		t.Fatal(err)
	}
	changed, err := st.GetRepo(ctx, name)
	if err != nil || changed.IndexedAnalysisUnit == nil ||
		changed.IndexedAnalysisUnit.Digest == firstDigest {
		t.Fatalf("changed unit state = %+v, %v", changed, err)
	}
	rebuilt := false
	for shard, modified := range shardStamps(t, dataDir) {
		if !modified.Equal(before[shard]) {
			rebuilt = true
		}
	}
	if !rebuilt {
		t.Fatal("scope-only change did not rebuild the index")
	}

	delete(ix.AnalysisUnits, name)
	if err := ix.Index(ctx, *changed, false); err != nil {
		t.Fatal(err)
	}
	whole, err := st.GetRepo(ctx, name)
	if err != nil || whole.IndexedAnalysisUnit != nil {
		t.Fatalf("removed unit state = %+v, %v", whole, err)
	}
}

type analysisUnitReconcileStore struct {
	store.Store
	repositories []store.Repo
	enqueued     []store.Job
}

func (s *analysisUnitReconcileStore) ListRepos(context.Context) ([]store.Repo, error) {
	return append([]store.Repo(nil), s.repositories...), nil
}

func (s *analysisUnitReconcileStore) EnqueuePending(
	_ context.Context,
	kind store.JobKind,
	target string,
	force bool,
) (*store.Job, error) {
	job := store.Job{Kind: kind, Target: target, Force: force}
	s.enqueued = append(s.enqueued, job)
	return &job, nil
}

func TestReconcileAnalysisUnitsQueuesOnlyStateChanges(t *testing.T) {
	scope := analysisunit.Scope{
		Repository: "example.com/repo",
		Name:       "service",
		Primary:    []string{"service/src"},
	}
	matching, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	stale := analysisunit.CloneState(matching)
	stale.Name = "old"
	stale.Digest = "sha256:" + strings.Repeat("0", 64)
	testStore := &analysisUnitReconcileStore{repositories: []store.Repo{
		{Name: "example.com/whole", IndexedCommitHash: "a"},
		{Name: "example.com/repo", IndexedCommitHash: "b", IndexedAnalysisUnit: stale},
		{Name: "example.com/matching", IndexedCommitHash: "c",
			IndexedAnalysisUnit: analysisunit.CloneState(matching)},
		{Name: "example.com/removed", IndexedCommitHash: "d",
			IndexedAnalysisUnit: analysisunit.CloneState(matching)},
		{Name: "example.com/deleting", IndexedCommitHash: "e",
			IndexedAnalysisUnit: stale, Deleting: true},
	}}
	matchingScope := scope
	matchingScope.Repository = "example.com/matching"
	matchingState, err := matchingScope.State()
	if err != nil {
		t.Fatal(err)
	}
	testStore.repositories[2].IndexedAnalysisUnit = matchingState

	count, err := indexer.ReconcileAnalysisUnits(
		t.Context(), testStore, map[string]analysisunit.Scope{
			scope.Repository:         scope,
			matchingScope.Repository: matchingScope,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(testStore.enqueued) != 2 {
		t.Fatalf("enqueued = %+v, count=%d", testStore.enqueued, count)
	}
	if testStore.enqueued[0].Target != "example.com/repo" ||
		!testStore.enqueued[0].Force ||
		testStore.enqueued[1].Target != "example.com/removed" ||
		!testStore.enqueued[1].Force {
		t.Fatalf("unexpected rebuild jobs: %+v", testStore.enqueued)
	}
}

// classifyChild: SIGKILL → oom, integrity text → corrupt-shard, else generic.
func TestClassifyChild(t *testing.T) {
	killed := exec.Command("sh", "-c", "kill -KILL $$")
	killErr := killed.Run()
	if killErr == nil {
		t.Fatal("expected SIGKILL failure")
	}

	tests := []struct {
		name   string
		raw    error
		output string
		want   store.ErrClass
	}{
		{"sigkill is oom", killErr, "", store.ClassOOM},
		{"corrupt output", os.ErrInvalid, "shard file corrupt", store.ClassCorrupt},
		{"checksum output", os.ErrInvalid, "checksum mismatch in shard", store.ClassCorrupt},
		{"anything else", os.ErrInvalid, "some other failure", store.ClassGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := indexer.ClassifyChildForTest(tt.raw, tt.output)
			if got := store.Classify(err); got != tt.want {
				t.Errorf("class = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIndexMissingRepoIsIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ix, _, _ := newIndexer(t, ctx)

	err := ix.Index(ctx, store.Repo{Name: "github.com/no/such"}, false)
	if err != nil {
		t.Errorf("err = %v, want deleted repo job ignored", err)
	}
}

func TestHandleDeletingRepoDoesNotTouchMirror(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, _ := fixture(t, ctx, st, dataDir)
	if err := st.SetRepoDeleting(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sync.RepoDir(dataDir, name)); err != nil {
		t.Fatal(err)
	}
	if err := ix.Handle(ctx, store.Job{Target: name}); err != nil {
		t.Errorf("Handle deleting repo: %v", err)
	}
}

func TestCanceledIndexChildIsNotClassifiedOOM(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, _ := fixture(t, ctx, st, dataDir)

	bin := filepath.Join(t.TempDir(), "busy-indexer")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ix.Bin = bin
	runCtx, stop := context.WithCancel(ctx)
	time.AfterFunc(100*time.Millisecond, stop)
	err := ix.Index(runCtx, store.Repo{Name: name}, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := store.Classify(err); got == store.ClassOOM {
		t.Errorf("canceled child classified as %q, want non-OOM", got)
	}
}

// T12.2: OnIndexed (the index→extract chain hook) fires after a successful
// state commit and on an unchanged-HEAD retry, but never on commit failure.
func TestOnIndexedChainsFreshAndCurrentCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, head := fixture(t, ctx, st, dataDir)

	var fired []string
	ix.OnIndexed = func(_ context.Context, repo, commit string) error {
		fired = append(fired, repo+"@"+commit)
		return nil
	}
	if err := ix.Index(ctx, store.Repo{Name: name}, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(fired) != 1 || fired[0] != name+"@"+head {
		t.Fatalf("OnIndexed after fresh index = %v", fired)
	}
	// Unchanged HEAD avoids the shard rebuild but confirms/repairs the chain.
	if err := ix.Index(ctx, store.Repo{Name: name}, false); err != nil {
		t.Fatalf("short-circuit index: %v", err)
	}
	if len(fired) != 2 || fired[1] != name+"@"+head {
		t.Fatalf("OnIndexed after short-circuit = %v", fired)
	}
	// A failed state commit must not fire the chain.
	ix.Store = failIndexedStore{Store: st}
	if err := ix.Index(ctx, store.Repo{Name: name}, true); err == nil {
		t.Fatal("forced index with failing state commit succeeded")
	}
	if len(fired) != 2 {
		t.Fatalf("OnIndexed fired despite state-commit failure: %v", fired)
	}
}

// A post-index enqueue failure must fail the index job. Its retry sees the
// already-current shard and retries only the chain hook, preventing a lost
// extraction event without doing another expensive build.
func TestOnIndexedErrorPropagatesAndRetriesOnShortCircuit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	ix, st, dataDir := newIndexer(t, ctx)
	name, head := fixture(t, ctx, st, dataDir)

	wantErr := errors.New("injected enqueue failure")
	calls := 0
	ix.OnIndexed = func(_ context.Context, repo, commit string) error {
		calls++
		if repo != name || commit != head {
			t.Fatalf("OnIndexed(%q, %q), want (%q, %q)", repo, commit, name, head)
		}
		if calls == 1 {
			return store.WithClass(store.ClassExtract, wantErr)
		}
		return nil
	}

	err := ix.Index(ctx, store.Repo{Name: name}, false)
	if !errors.Is(err, wantErr) {
		t.Fatalf("first index error = %v, want %v", err, wantErr)
	}
	if class := store.Classify(err); class != store.ClassExtract {
		t.Fatalf("first index error class = %q, want %q", class, store.ClassExtract)
	}
	repo, err := st.GetRepo(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if repo.IndexedCommitHash != head {
		t.Fatalf("indexed commit after chain failure = %q, want %q", repo.IndexedCommitHash, head)
	}
	before := shardStamps(t, dataDir)
	if err := ix.Index(ctx, store.Repo{Name: name}, false); err != nil {
		t.Fatalf("retry index: %v", err)
	}
	if calls != 2 {
		t.Fatalf("OnIndexed calls = %d, want 2", calls)
	}
	for shard, stamp := range shardStamps(t, dataDir) {
		if !stamp.Equal(before[shard]) {
			t.Errorf("chain retry rebuilt shard %s", shard)
		}
	}
}

// A held mirror lock (e.g. a long extraction) must not park the serial index
// runner uncancellably: the lock wait honors the job context.
func TestIndexerLockWaitHonorsContext(t *testing.T) {
	dataDir := t.TempDir()
	name := "example.com/held/repo"
	dir, err := sync.SafeRepoDir(dataDir, name)
	if err != nil {
		t.Fatal(err)
	}
	unlock := repowork.Lock(dir)
	defer unlock()

	ix := &indexer.Indexer{DataDir: dataDir}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := ix.Index(ctx, store.Repo{Name: name}, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked index error = %v, want context.DeadlineExceeded", err)
	}
}
