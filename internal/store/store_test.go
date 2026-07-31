package store_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
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
	s, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
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
		store.JobResolverCatalog,
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
	if err := s.RequeueJob(ctx, *active, "retry", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	pending, err = s.ListJobs(ctx, store.JobIndex, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Attempts != 1 || !pending[0].Force {
		t.Fatalf("merged retry successor = %+v, want one forced job with attempts=1", pending)
	}

	if n, err := s.CancelPendingJobs(ctx, store.JobIndex, active.Target); err != nil || n != 1 {
		t.Fatalf("CancelPendingJobs = %d, %v; want 1", n, err)
	}
	canceled, err := s.ListJobs(ctx, store.JobIndex, store.StatusCanceled)
	if err != nil || len(canceled) != 2 {
		t.Fatalf("canceled jobs = %+v, %v; want superseded active and canceled successor", canceled, err)
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
