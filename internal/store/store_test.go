package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/store"
)

func newTestStore(t *testing.T) *store.Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed (https://surrealdb.com/install)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	// Memory engine: identical child/schema/migrations, no surrealkv fsync
	// cost (~6s per fresh data dir). Persistence-dependent tests reopen a
	// fixed directory through store.OpenLocal instead of this helper.
	s, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocalMemory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// rawTestChild is the native-test analogue of the production supervised
// child: exactly one unconditional cleanup owner per child. The owner
// requests shutdown (SIGINT), waits a bounded grace period, escalates to
// SIGKILL when the child ignores the request, and then joins the Wait
// goroutine. The owner is registered with t.Cleanup immediately after Start,
// so every failure path — including a t.Fatalf before any explicit stop —
// still reaps the child. Captured output may only be read after the Wait
// goroutine is joined (waitResult): exec's output-copying goroutines finish
// before Wait returns, and reading the buffer earlier races them.
type rawTestChild struct {
	cmd       *exec.Cmd
	output    *bytes.Buffer
	grace     time.Duration
	waited    chan error
	awaitOnce sync.Once
	waitErr   error
	done      chan struct{}
	stopOnce  sync.Once
}

// startRawTestChild starts cmd and registers its single unconditional cleanup
// owner. The caller must not start, wait on, signal, or kill the process
// itself; every shutdown goes through child.stop (explicitly or via the
// registered cleanup).
func startRawTestChild(t *testing.T, cmd *exec.Cmd, grace time.Duration) *rawTestChild {
	t.Helper()
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test child: %v", err)
	}
	child := &rawTestChild{
		cmd: cmd, output: &output, grace: grace,
		waited: make(chan error, 1), done: make(chan struct{}),
	}
	go func() { child.waited <- cmd.Wait() }()
	t.Cleanup(child.stop)
	return child
}

// await joins the Wait goroutine exactly once. The captured output is fully
// copied when it returns.
func (c *rawTestChild) await() {
	c.awaitOnce.Do(func() {
		c.waitErr = <-c.waited
		close(c.done)
	})
}

// stop is the single unconditional cleanup owner: request shutdown, wait a
// bounded grace period, kill if necessary, then join the Wait goroutine.
func (c *rawTestChild) stop() {
	c.stopOnce.Do(func() {
		go c.await()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-c.done:
		case <-time.After(c.grace):
			_ = c.cmd.Process.Kill()
			<-c.done
		}
	})
}

// waitResult joins the Wait goroutine and returns its fully-copied captured
// output plus the child's exit result. Call only after the child has been
// asked to stop; otherwise it blocks until the registered cleanup stops it.
func (c *rawTestChild) waitResult() (output string, waitErr error) {
	c.await()
	return c.output.String(), c.waitErr
}

// waitTestChildHealthy polls the loopback /health endpoint until timeout.
// Each request carries its own client timeout: the overall deadline cannot
// bound a blocked bare http.Get, so the loop never issues one.
func waitTestChildHealthy(url string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		resp, err := client.Get(url) //nolint:gosec // loopback test child, constructed addr
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// pickLoopbackAddr reserves an ephemeral loopback port and returns its addr.
// The close-then-reuse race is acceptable for a local test child.
func pickLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

// legacyChildCmd builds the pre-credential-binding database bootstrap: the
// historical root/root credential, transported via the environment rather
// than argv (the command line is world-readable through ps).
func legacyChildCmd(ctx context.Context, binary, addr, dir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, "start",
		"--bind", addr, "--user", "root", "--log", "warn",
		"surrealkv:"+filepath.Join(dir, "db"))
	cmd.Env = append(os.Environ(), "SURREAL_PASS=root")
	return cmd
}

// assertChildMatching fails unless a process carrying pattern in its command
// line appears within the deadline.
func assertChildMatching(t *testing.T, pattern string) {
	t.Helper()
	quoted := regexp.QuoteMeta(pattern)
	for deadline := time.Now().Add(10 * time.Second); ; {
		if err := exec.Command("pgrep", "-f", quoted).Run(); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no child matching %q appeared", pattern)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// assertNoChildMatching fails if any process still carries pattern in its
// command line. pgrep exits 1 when nothing matches; anything else is a hard
// failure. The pattern is matched as a fixed string so data-directory paths
// cannot act as regex.
func assertNoChildMatching(t *testing.T, pattern string) {
	t.Helper()
	quoted := regexp.QuoteMeta(pattern)
	for deadline := time.Now().Add(10 * time.Second); ; {
		err := exec.Command("pgrep", "-f", quoted).Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				return
			}
			t.Fatalf("pgrep -f %q: %v", pattern, err)
		}
		if time.Now().After(deadline) {
			out, _ := exec.Command("pgrep", "-af", quoted).CombinedOutput()
			t.Fatalf("child still alive matching %q: %s", pattern, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSchemaIdempotent(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	for i := range 2 { // second open re-applies the schema over persisted data
		s, err := store.OpenLocal(ctx, dir)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

// TestOpenLocalReopenReadsPersistedData is the engine-backed regression for
// the child-password lifetime fix: the supervised engine's root password is
// bound to the database directory, so closing and reopening the same
// directory must sign in successfully and see previously written data.
// (SurrealDB only initializes the root user when none exists; a fresh
// password per start could never sign in to an existing database.)
func TestOpenLocalReopenReadsPersistedData(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Registered immediately: Close is idempotent, so any later failure —
	// including a t.Fatalf before the explicit close below — still stops the
	// supervised child instead of orphaning it.
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	repo := store.Repo{Name: "example.com/reopen", CloneURL: "https://example.com/reopen.git", DefaultBranch: "main"}
	if err := s.UpsertRepo(ctx, repo); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	got, err := reopened.GetRepo(ctx, repo.Name)
	if err != nil {
		t.Fatalf("GetRepo after reopen: %v", err)
	}
	if got.CloneURL != repo.CloneURL {
		t.Fatalf("GetRepo after reopen = %+v, want the persisted repo", got)
	}
}

// TestOpenLocalUpgradesLegacyRootDatabase is the engine-backed upgrade
// regression: a database initialized the historical way (root/root, no
// persisted child password) must keep working when first opened under the
// new credential binding, and stay consistent across later restarts.
func TestOpenLocalUpgradesLegacyRootDatabase(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	dir := t.TempDir()
	identity, err := store.FindSurrealBinary()
	if err != nil {
		t.Fatalf("FindSurrealBinary: %v", err)
	}
	// Initialize the database the pre-fix way through the raw bootstrap.
	// The raw child has exactly one unconditional cleanup owner, registered
	// inside startRawTestChild before the health check runs: whatever fails
	// below, the child is asked to stop, killed after a bounded grace if it
	// ignores the request, and its Wait goroutine is joined before the test
	// — and the temporary data directory — goes away.
	addr := pickLoopbackAddr(t)
	child := startRawTestChild(t, legacyChildCmd(ctx, identity.Path, addr, dir), 10*time.Second)
	if !waitTestChildHealthy("http://"+addr+"/health", 30*time.Second) {
		child.stop()
		// Output is read only after the Wait goroutine is joined: exec's
		// output-copying goroutines finish before Wait returns.
		output, _ := child.waitResult()
		t.Fatalf("legacy child never became healthy: %s", output)
	}
	child.stop()
	if output, waitErr := child.waitResult(); waitErr != nil {
		t.Fatalf("legacy child exit: %v\n%s", waitErr, output)
	}

	s, err := store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	// Registered immediately, like every other acquisition: the old code
	// only closed on the success path, so a t.Fatalf before the explicit
	// close below orphaned the supervised child.
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	repo := store.Repo{Name: "example.com/legacy", CloneURL: "https://example.com/legacy.git"}
	if err := s.UpsertRepo(ctx, repo); err != nil {
		t.Fatalf("upsert on legacy database: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatalf("reopen legacy database: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	if _, err := reopened.GetRepo(ctx, repo.Name); err != nil {
		t.Fatalf("GetRepo after legacy upgrade reopen: %v", err)
	}
}

// TestOpenLocalEarlyFailureStillCleansUpChild covers the defect the cleanup
// registration fixes: the upsert fails before any explicit close (the old
// t.Fatalf site), and the subtest returns without closing the store. The
// cleanup registered immediately after acquisition must still stop the
// supervised child; no surreal child for the data directory may survive.
func TestOpenLocalEarlyFailureStillCleansUpChild(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dir := t.TempDir()
	t.Run("early failure", func(t *testing.T) {
		s, err := store.OpenLocal(ctx, dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		// Registered before any operation can fail: the old code closed the
		// store only on the success path, so a fatal here orphaned the
		// production child, which deliberately outlives caller-context
		// cancellation.
		t.Cleanup(func() { _ = s.Close(context.Background()) })
		cancelled, stop := context.WithCancel(ctx)
		stop()
		if err := s.UpsertRepo(cancelled, store.Repo{Name: "example.com/early"}); err == nil {
			t.Fatal("UpsertRepo with a cancelled context succeeded; want an error")
		}
		// No explicit close: this is the old t.Fatalf bypass. The registered
		// cleanup owns the child from here.
	})
	assertNoChildMatching(t, "surrealkv:"+filepath.Join(dir, "db"))
}

// TestRawChildUnhealthyBootstrapStopsChild starts a real surreal child but
// probes an address nothing listens on, so the bootstrap deems the child
// unhealthy while the child itself stays alive and healthy on its real port.
// The single cleanup owner must still shut it down; no child may survive.
func TestRawChildUnhealthyBootstrapStopsChild(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dir := t.TempDir()
	identity, err := store.FindSurrealBinary()
	if err != nil {
		t.Fatalf("FindSurrealBinary: %v", err)
	}
	addr := pickLoopbackAddr(t)
	engineArg := "surrealkv:" + filepath.Join(dir, "db")
	child := startRawTestChild(t, legacyChildCmd(ctx, identity.Path, addr, dir), 10*time.Second)
	// The child is genuinely alive (positive control); only the bootstrap's
	// health view of it is broken.
	assertChildMatching(t, engineArg)
	if waitTestChildHealthy("http://127.0.0.1:1/health", 5*time.Second) {
		t.Fatal("health probe against an unbound port succeeded; want failure")
	}
	child.stop()
	if output, waitErr := child.waitResult(); waitErr != nil {
		t.Fatalf("child exit: %v\n%s", waitErr, output)
	}
	assertNoChildMatching(t, engineArg)
}

// TestRawChildShutdownIgnoringChildIsKilled starts a child that traps and
// ignores SIGINT, so the owner's shutdown request cannot work. The owner must
// escalate to SIGKILL after its bounded grace period; no child may survive.
func TestRawChildShutdownIgnoringChildIsKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The marker becomes the child's argv[0] via exec -a so pgrep can
	// identify exactly this child; nothing else on the host carries it. The
	// shell replaces itself with sleep (exec), so the child is a single
	// process with no grandchildren that could outlive it holding pipes
	// open. trap "" INT survives the exec, so the child ignores the owner's
	// shutdown request and forces the SIGKILL escalation path.
	const marker = "phebs-test-ignore-sigint-probe"
	cmd := exec.CommandContext(ctx, "bash", "-c",
		`trap "" INT; exec -a `+marker+` sleep 60`)
	child := startRawTestChild(t, cmd, 2*time.Second)
	assertChildMatching(t, marker)
	child.stop()
	_, waitErr := child.waitResult()
	if waitErr == nil {
		t.Fatal("shutdown-ignoring child exited cleanly; want a kill")
	}
	assertNoChildMatching(t, marker)
}

func TestRepoCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	repos := []store.Repo{
		{Name: "github.com/foo/bar", CloneURL: "https://github.com/foo/bar.git",
			DefaultBranch: "main", IsPublic: true, PushedAt: &now,
			Metadata: map[string]any{"stars": int64(7)}},
		{Name: "example.com/baz", CloneURL: "https://example.com/baz.git", IsFork: true},
	}
	for _, r := range repos {
		if err := s.UpsertRepo(ctx, r); err != nil {
			t.Fatalf("upsert %s: %v", r.Name, err)
		}
	}

	got, err := s.GetRepo(ctx, "github.com/foo/bar")
	if err != nil {
		t.Fatal(err)
	}
	if got.CloneURL != repos[0].CloneURL || !got.IsPublic || got.DefaultBranch != "main" {
		t.Errorf("GetRepo = %+v, want fields of %+v", got, repos[0])
	}
	if got.PushedAt == nil || !got.PushedAt.Equal(now) {
		t.Errorf("PushedAt = %v, want %v", got.PushedAt, now)
	}

	// upsert same name = update in place, not a duplicate
	if err := s.SetRepoIndexed(ctx, repos[0].Name, "abc123", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoDeleting(ctx, repos[0].Name, true); err != nil {
		t.Fatal(err)
	}
	repos[0].DefaultBranch = "trunk"
	if err := s.UpsertRepo(ctx, repos[0]); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListRepos len = %d, want 2", len(all))
	}
	if all[1].DefaultBranch != "trunk" { // ordered by name: example.com first
		t.Errorf("updated DefaultBranch = %q, want trunk", all[1].DefaultBranch)
	}
	if all[1].IndexedCommitHash != "abc123" || all[1].IndexedAt == nil ||
		all[1].LatestJobStatus != "done" || !all[1].Deleting {
		t.Errorf("sync upsert erased index/deletion state: %+v", all[1])
	}
	if want := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: "abc123"}}; !reflect.DeepEqual(all[1].IndexedRevisions, want) {
		t.Errorf("indexed revisions = %+v, want %+v", all[1].IndexedRevisions, want)
	}
	revisions := []store.IndexedRevision{
		{Selector: "HEAD", Branch: "HEAD", Commit: "abc123"},
		{Selector: "release", Branch: "refs/heads/release", Commit: "def456"},
	}
	if err := s.SetRepoIndexedRevisions(ctx, repos[0].Name, "abc123", revisions, now); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRepo(ctx, repos[0].Name)
	if err != nil || !reflect.DeepEqual(got.IndexedRevisions, revisions) {
		t.Fatalf("multi-revision state = %+v, %v; want %+v", got, err, revisions)
	}
	unit, err := (analysisunit.Scope{
		Repository: repos[0].Name,
		Name:       "payments",
		Primary:    []string{"services/payments/src"},
		Supporting: []string{"services/payments/go.mod"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexedState(
		ctx, repos[0].Name, "abc123", revisions, unit, now,
	); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRepo(ctx, repos[0].Name)
	if err != nil || !analysisunit.EqualState(got.IndexedAnalysisUnit, unit) {
		t.Fatalf("analysis-unit state = %+v, %v; want %+v", got, err, unit)
	}
	invalidUnit := analysisunit.CloneState(unit)
	invalidUnit.Digest = "sha256:tampered"
	if err := s.SetRepoIndexedState(
		ctx, repos[0].Name, "changed", revisions, invalidUnit, now,
	); !errors.Is(err, analysisunit.ErrInvalidScope) {
		t.Fatalf("invalid analysis-unit state error = %v, want ErrInvalidScope", err)
	}
	got, err = s.GetRepo(ctx, repos[0].Name)
	if err != nil || got.IndexedCommitHash != "abc123" ||
		!analysisunit.EqualState(got.IndexedAnalysisUnit, unit) {
		t.Fatalf("invalid state mutated committed row: %+v, %v", got, err)
	}
	statuses, err := s.RepoStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var unitStatus *analysisunit.State
	for _, status := range statuses {
		if status.Name == repos[0].Name {
			unitStatus = status.AnalysisUnit
		}
	}
	if !analysisunit.EqualState(unitStatus, unit) {
		t.Fatalf("repo status analysis unit = %+v, want %+v", unitStatus, unit)
	}
	if err := s.ClearRepoIndexState(ctx, repos[0].Name); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRepo(ctx, repos[0].Name)
	if err != nil || got.IndexedCommitHash != "" || len(got.IndexedRevisions) != 0 ||
		got.IndexedAnalysisUnit != nil || got.IndexedAt != nil {
		t.Fatalf("cleared index state = %+v, %v", got, err)
	}

	if err := s.DeleteRepo(ctx, "example.com/baz"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRepo(ctx, "example.com/baz"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete, err = %v, want ErrNotFound", err)
	}
}

func TestAnalysisUnitStateSurvivesUpgradeReopen(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	directory := t.TempDir()
	repository := "example.com/reopen/unit"
	commit := "0123456789012345678901234567890123456789"

	current, err := store.OpenLocal(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/reopen/unit.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := current.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(ctx); err != nil {
		t.Fatal(err)
	}

	upgraded, err := store.OpenLocal(ctx, directory)
	if err != nil {
		t.Fatalf("reopen legacy row: %v", err)
	}
	legacy, err := upgraded.GetRepo(ctx, repository)
	if err != nil || legacy.IndexedAnalysisUnit != nil {
		t.Fatalf("legacy row changed on upgrade: %+v, %v", legacy, err)
	}
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service/src"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	if err := upgraded.SetRepoIndexedState(
		ctx, repository, commit, legacy.IndexedRevisions, unit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.OpenLocal(ctx, directory)
	if err != nil {
		t.Fatalf("reopen analysis-unit row: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(context.Background()); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	got, err := reopened.GetRepo(ctx, repository)
	if err != nil || !analysisunit.EqualState(got.IndexedAnalysisUnit, unit) {
		t.Fatalf("reopened state = %+v, %v; want %+v", got, err, unit)
	}
}

func TestJobLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, kind := range []store.JobKind{
		store.JobSync, store.JobIndex, store.JobCandidate,
		store.JobResolverCatalog, store.JobCallerLeaf, store.JobTypedIndex,
	} {
		t.Run(string(kind), func(t *testing.T) {
			job, err := s.CreateJob(ctx, kind, "target-1")
			if err != nil {
				t.Fatalf("CreateJob: %v", err)
			}
			if job.ID == "" || job.Status != store.StatusPending {
				t.Fatalf("created job = %+v, want id set and pending", job)
			}

			pending, err := s.ListJobs(ctx, kind, store.StatusPending)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || pending[0].Target != "target-1" {
				t.Fatalf("pending = %+v, want the created job", pending)
			}

			claimed, err := s.ClaimJob(ctx, kind, "lifecycle-test")
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			if claimed.LeaseToken == "" {
				t.Fatal("claimed job has no lease token")
			}
			if err := s.SetJobStatus(ctx, *claimed, store.StatusRunning, ""); err != nil {
				t.Fatalf("to running: %v", err)
			}
			if err := s.SetJobStatus(ctx, *claimed, store.StatusFailed, "boom"); err != nil {
				t.Fatalf("to failed: %v", err)
			}

			failed, err := s.ListJobs(ctx, kind, store.StatusFailed)
			if err != nil {
				t.Fatal(err)
			}
			if len(failed) != 1 || failed[0].Error != "boom" || failed[0].FinishedAt == nil {
				t.Fatalf("failed job = %+v, want error recorded and finished_at set", failed)
			}

			missing := *claimed
			missing.ID = string(kind) + ":nope"
			if err := s.SetJobStatus(ctx, missing, store.StatusDone, ""); !errors.Is(err, store.ErrLeaseLost) {
				t.Errorf("unknown lease err = %v, want ErrLeaseLost", err)
			}
		})
	}
}

func TestEnqueuePendingCollapsesConcurrentSuccessorsAndUpgradesForce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	enqueueMany := func(forceOne bool) {
		t.Helper()
		const callers = 32
		errs := make(chan error, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Add(1)
			go func(force bool) {
				defer wg.Done()
				_, err := s.EnqueuePending(ctx, store.JobIndex, "github.com/acme/repo", force)
				errs <- err
			}(forceOne && i == callers-1)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("EnqueuePending: %v", err)
			}
		}
	}

	enqueueMany(true)
	pending, err := s.ListJobs(ctx, store.JobIndex, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || !pending[0].Force {
		t.Fatalf("initial pending jobs = %+v, want one forced job", pending)
	}

	active, err := s.ClaimJob(ctx, store.JobIndex, "active-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *active, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}

	// Events arriving after the claim collapse into exactly one successor.
	enqueueMany(true)
	pending, err = s.ListJobs(ctx, store.JobIndex, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || !pending[0].Force || pending[0].ID == active.ID {
		t.Fatalf("successor jobs = %+v, want one distinct forced pending job", pending)
	}
	retryGate := time.Now().UTC().Add(time.Minute)
	if err := s.RequeueJob(ctx, *active, "retry", retryGate); err != nil {
		t.Fatal(err)
	}
	pending, err = s.ListJobs(ctx, store.JobIndex, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Attempts != 1 || !pending[0].Force ||
		pending[0].Error != "retry" || pending[0].NotBefore == nil ||
		pending[0].NotBefore.Before(retryGate.Add(-time.Second)) ||
		pending[0].NotBefore.After(retryGate.Add(time.Second)) {
		t.Fatalf(
			"merged retry successor = %+v, want forced attempts=1/error/backoff",
			pending,
		)
	}

	if n, err := s.CancelPendingJobs(ctx, store.JobIndex, active.Target); err != nil || n != 1 {
		t.Fatalf("CancelPendingJobs = %d, %v; want 1", n, err)
	}
	canceled, err := s.ListJobs(ctx, store.JobIndex, store.StatusCanceled)
	if err != nil || len(canceled) != 2 {
		t.Fatalf("canceled jobs = %+v, %v; want superseded active and canceled successor", canceled, err)
	}

	for _, test := range []struct {
		name           string
		externalBefore bool
		force          bool
	}{
		{name: "external before recovery", externalBefore: true},
		{name: "external before recovery forced", externalBefore: true, force: true},
		{name: "external after recovery"},
		{name: "external after recovery forced", force: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := "github.com/acme/" + strings.ReplaceAll(test.name, " ", "-")
			if _, err := s.EnqueuePending(ctx, store.JobCallerLeaf, target, false); err != nil {
				t.Fatal(err)
			}
			active, err := s.ClaimJob(ctx, store.JobCallerLeaf, "active-worker")
			if err != nil {
				t.Fatal(err)
			}
			active.Attempts = 2
			if err := s.SetJobStatus(ctx, *active, store.StatusRunning, ""); err != nil {
				t.Fatal(err)
			}

			var recovery, external *store.Job
			if test.externalBefore {
				external, err = s.EnqueuePending(
					ctx, store.JobCallerLeaf, target, test.force,
				)
				if err == nil {
					recovery, err = s.EnsureJobSuccessor(ctx, *active, false)
				}
			} else {
				recovery, err = s.EnsureJobSuccessor(ctx, *active, false)
				if err == nil {
					external, err = s.EnqueuePending(
						ctx, store.JobCallerLeaf, target, test.force,
					)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if recovery.ID != external.ID {
				t.Fatalf("coalesced successor ids = recovery:%s external:%s",
					recovery.ID, external.ID)
			}
			if err := s.FailJobWithSuccessor(
				ctx, *active, "persistent install failure",
			); err != nil {
				t.Fatal(err)
			}

			pending, err := s.ListJobs(ctx, store.JobCallerLeaf, store.StatusPending)
			if err != nil {
				t.Fatal(err)
			}
			pending = slices.DeleteFunc(pending, func(job store.Job) bool {
				return job.Target != target
			})
			if len(pending) != 1 || pending[0].ID != external.ID ||
				pending[0].Force != test.force || pending[0].Attempts != 0 {
				t.Fatalf("fresh successor after final failure = %+v", pending)
			}
			failed, err := s.ListJobs(ctx, store.JobCallerLeaf, store.StatusFailed)
			if err != nil {
				t.Fatal(err)
			}
			failed = slices.DeleteFunc(failed, func(job store.Job) bool {
				return job.Target != target
			})
			if len(failed) != 1 || failed[0].ID != active.ID ||
				failed[0].Attempts != 3 {
				t.Fatalf("exhausted active job = %+v", failed)
			}
			if count, err := s.CancelPendingJobs(
				ctx, store.JobCallerLeaf, target,
			); err != nil || count != 1 {
				t.Fatalf("cleanup preserved successor = %d, %v", count, err)
			}
		})
	}
	t.Run("candidate fanout clears recovery provenance", func(t *testing.T) {
		assertCandidateFanoutPreservesFreshResolverWork(t, ctx, s)
	})
}

func TestEnqueuePendingWakesBackedOffJobForFreshEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if _, err := s.EnqueuePending(ctx, store.JobResolverCatalog, "github.com/acme/wake", false); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimJob(ctx, store.JobResolverCatalog, "resolver-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *claimed, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RequeueJob(
		ctx, *claimed, "authority pending", time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueuePending(
		ctx, store.JobResolverCatalog, claimed.Target, false,
	); err != nil {
		t.Fatal(err)
	}
	woken, err := s.ClaimJob(ctx, store.JobResolverCatalog, "resolver-worker-2")
	if err != nil {
		t.Fatal(err)
	}
	if woken.ID != claimed.ID || woken.Attempts != 1 || woken.NotBefore != nil {
		t.Fatalf("woken job = %+v, want same row at attempt 1 without backoff", woken)
	}
}

func TestLeaseFencesReapedWorker(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.EnqueuePending(ctx, store.JobIndex, "repo", false); err != nil {
		t.Fatal(err)
	}
	old, err := s.ClaimJob(ctx, store.JobIndex, "old-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *old, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReapStale(ctx, store.JobIndex, -time.Second, 3); err != nil || n != 1 {
		t.Fatalf("ReapStale = %d, %v; want 1", n, err)
	}

	current, err := s.ClaimJob(ctx, store.JobIndex, "new-worker")
	if err != nil {
		t.Fatal(err)
	}
	if old.LeaseToken == current.LeaseToken {
		t.Fatal("reclaimed job reused its lease token")
	}
	if err := s.SetJobStatus(ctx, *current, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func() error{
		"heartbeat": func() error { return s.HeartbeatJob(ctx, *old) },
		"complete":  func() error { return s.SetJobStatus(ctx, *old, store.StatusDone, "") },
		"requeue": func() error {
			return s.RequeueJob(ctx, *old, "stale", time.Now().UTC())
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := mutate(); !errors.Is(err, store.ErrLeaseLost) {
				t.Errorf("stale mutation error = %v, want ErrLeaseLost", err)
			}
		})
	}
	if err := s.SetJobStatus(ctx, *current, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRepoStatusesAndOrphans(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"h/a", "h/b", "h/c"} {
		if err := s.UpsertRepo(ctx, store.Repo{Name: name, CloneURL: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetRepoConnections(ctx, "conn1", []string{"h/a", "h/b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoConnections(ctx, "conn2", []string{"h/b"}); err != nil {
		t.Fatal(err)
	}
	job, err := s.CreateJob(ctx, store.JobIndex, "h/a")
	if err != nil {
		t.Fatal(err)
	}
	_ = job

	statuses, err := s.RepoStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]store.RepoStatus{}
	for _, st := range statuses {
		byName[st.Name] = st
	}
	if st := byName["h/a"]; st.Orphaned || len(st.Connections) != 1 || st.LastIndexJob == nil {
		t.Errorf("h/a = %+v, want conn1 membership and an index job", st)
	}
	if st := byName["h/b"]; st.Orphaned || len(st.Connections) != 2 {
		t.Errorf("h/b = %+v, want two connections", st)
	}
	if st := byName["h/c"]; !st.Orphaned {
		t.Errorf("h/c = %+v, want orphaned", st)
	}

	// replacement semantics: conn1 drops h/a → only conn2 memberships remain relevant
	if err := s.SetRepoConnections(ctx, "conn1", []string{"h/b"}); err != nil {
		t.Fatal(err)
	}
	// prune: conn2 removed from config entirely
	if err := s.PruneConnections(ctx, []string{"conn1"}); err != nil {
		t.Fatal(err)
	}
	statuses, _ = s.RepoStatuses(ctx)
	byName = map[string]store.RepoStatus{}
	for _, st := range statuses {
		byName[st.Name] = st
	}
	if !byName["h/a"].Orphaned {
		t.Error("h/a should be orphaned after conn1 dropped it")
	}
	if st := byName["h/b"]; st.Orphaned || len(st.Connections) != 1 || st.Connections[0] != "conn1" {
		t.Errorf("h/b = %+v, want only conn1 after prune", st)
	}
}

func TestSetRepoConnectionsRollsBackFailedReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SetRepoConnections(ctx, "conn", []string{"h/original"}); err != nil {
		t.Fatal(err)
	}
	// The unique pair index rejects the duplicate create. The preceding delete
	// must roll back with it rather than exposing an empty membership snapshot.
	if err := s.SetRepoConnections(ctx, "conn", []string{"h/replacement", "h/replacement"}); err == nil {
		t.Fatal("duplicate replacement unexpectedly succeeded")
	}
	connections, err := s.GetRepoConnections(ctx, "h/original")
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0] != "conn" {
		t.Fatalf("original connections = %v, want [conn] after rollback", connections)
	}
	connections, err = s.GetRepoConnections(ctx, "h/replacement")
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 0 {
		t.Fatalf("replacement connections = %v, want none after rollback", connections)
	}
}

// TestClaimJobConcurrent is the T1.3 AC: N concurrent pollers drain the
// queue through the shipped ClaimJob with zero double-claims.
func TestClaimJobConcurrent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const jobs, workers = 100, 5

	for i := range jobs {
		if _, err := s.CreateJob(ctx, store.JobIndex, fmt.Sprintf("repo-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	claimed := map[string]string{}
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(who string) {
			defer wg.Done()
			for {
				job, err := s.ClaimJob(ctx, store.JobIndex, who)
				if errors.Is(err, store.ErrNotFound) {
					return
				}
				if err != nil {
					t.Errorf("%s: %v", who, err)
					return
				}
				if job.Status != store.StatusClaimed || job.ClaimedBy != who {
					t.Errorf("claimed job = %+v, want status claimed by %s", job, who)
				}
				mu.Lock()
				if prev, dup := claimed[job.ID]; dup {
					t.Errorf("double claim: %s by %s and %s", job.ID, prev, who)
				}
				claimed[job.ID] = who
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(claimed) != jobs {
		t.Errorf("claimed %d unique jobs, want %d", len(claimed), jobs)
	}
	if left, _ := s.ListJobs(ctx, store.JobIndex, store.StatusPending); len(left) != 0 {
		t.Errorf("%d jobs still pending", len(left))
	}
}

func TestJobStatusEnumEnforced(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateJob(context.Background(), store.JobSync, "t")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimJob(context.Background(), store.JobSync, "enum-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(context.Background(), *claimed, "bogus", ""); err == nil {
		t.Error("bogus status accepted; schema ASSERT should reject it")
	}
}
