package store

// Internal tests: the reaper test manipulates heartbeat timestamps directly.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

func newRunnerStore(t *testing.T) *Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// waitFor polls cond until true or the deadline hits.
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestRunnerDefaultsKeepFastPollingLeaseSafe(t *testing.T) {
	tests := []struct {
		name           string
		interval       time.Duration
		heartbeatEvery time.Duration
		staleAfter     time.Duration
		wantHeartbeat  time.Duration
		wantStale      time.Duration
	}{
		{name: "fast poll", interval: 250 * time.Millisecond, wantHeartbeat: 5 * time.Second, wantStale: 20 * time.Second},
		{name: "ordinary default", wantHeartbeat: 5 * time.Second, wantStale: 20 * time.Second},
		{name: "slow poll", interval: 60 * time.Second, wantHeartbeat: 20 * time.Second, wantStale: 80 * time.Second},
		{name: "explicit lease", interval: 250 * time.Millisecond, heartbeatEvery: 2 * time.Second, staleAfter: 30 * time.Second, wantHeartbeat: 2 * time.Second, wantStale: 30 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := Runner{
				Interval: test.interval, HeartbeatEvery: test.heartbeatEvery,
				StaleAfter: test.staleAfter,
			}
			runner.defaults()
			if runner.HeartbeatEvery != test.wantHeartbeat || runner.StaleAfter != test.wantStale {
				t.Fatalf("lease defaults = heartbeat %s, stale %s; want %s, %s",
					runner.HeartbeatEvery, runner.StaleAfter, test.wantHeartbeat, test.wantStale)
			}
		})
	}
}

// T2.3 AC: no double-execution under 3 concurrent pollers.
func TestRunnerNoDoubleExecution(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const jobs = 12
	for i := range jobs {
		if _, err := s.CreateJob(ctx, JobSync, fmt.Sprintf("conn-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	executions := map[string]int{}
	handler := func(_ context.Context, job Job) error {
		mu.Lock()
		executions[job.Target]++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond) // hold the job long enough to overlap pollers
		return nil
	}

	for i := range 3 {
		r := &Runner{Store: s, Kind: JobSync, Handle: handler,
			Interval: 50 * time.Millisecond, Who: fmt.Sprintf("p%d", i)}
		go r.Run(ctx)
	}

	waitFor(t, 15*time.Second, func() bool {
		done, err := s.ListJobs(ctx, JobSync, StatusDone)
		return err == nil && len(done) == jobs
	}, "jobs never all completed")
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(executions) != jobs {
		t.Errorf("executed %d distinct jobs, want %d", len(executions), jobs)
	}
	for target, n := range executions {
		if n != 1 {
			t.Errorf("%s executed %d times, want exactly 1", target, n)
		}
	}
}

func TestRunnerDependencyDeferralWaitsBeforeReexecution(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job, err := s.EnqueuePending(ctx, JobCallerLeaf, "repo", false)
	if err != nil {
		t.Fatal(err)
	}
	executions := make(chan Job, 1)
	var runs atomic.Int32
	runner := &Runner{
		Store: s, Kind: JobCallerLeaf,
		Handle: func(_ context.Context, current Job) error {
			executions <- current
			if runs.Add(1) == 1 {
				return WithDeferral(errors.New("resolver is not current"))
			}
			return nil
		},
		Interval: 2 * time.Second, HeartbeatEvery: 5 * time.Second,
		StaleAfter: 20 * time.Second, Who: "deferral-runner",
	}
	go runner.Run(ctx)

	first := <-executions
	if first.ID != job.ID || first.Attempts != 0 {
		t.Fatalf("first execution = %+v, want original job at attempt zero", first)
	}
	waitFor(t, 5*time.Second, func() bool {
		pending, listErr := s.ListJobs(ctx, JobCallerLeaf, StatusPending)
		return listErr == nil && len(pending) == 1 &&
			pending[0].ID == job.ID && pending[0].Attempts == 0 &&
			pending[0].NotBefore != nil
	}, "dependency deferral was not persisted with a claim delay")

	select {
	case repeated := <-executions:
		t.Fatalf("deferred job re-executed before its delay: %+v", repeated)
	case <-time.After(900 * time.Millisecond):
	}

	var second Job
	select {
	case second = <-executions:
	case <-time.After(6 * time.Second):
		t.Fatal("deferred job did not execute after its delay")
	}
	if second.ID != job.ID || second.Attempts != 0 {
		t.Fatalf("second execution = %+v, want same job and attempt count", second)
	}
	waitFor(t, 5*time.Second, func() bool {
		done, listErr := s.ListJobs(ctx, JobCallerLeaf, StatusDone)
		return listErr == nil && len(done) == 1 && done[0].ID == job.ID
	}, "recovered deferred job did not complete")
	cancel()
}

func TestRunnerDependencyDeferralDoesNotStarveReadySibling(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := s.EnqueuePending(ctx, JobCallerLeaf, "blocked", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueuePending(ctx, JobCallerLeaf, "ready", false); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{}, 1)
	ready := make(chan struct{}, 1)
	runner := &Runner{
		Store: s, Kind: JobCallerLeaf,
		Handle: func(_ context.Context, job Job) error {
			switch job.Target {
			case "blocked":
				blocked <- struct{}{}
				return WithDeferral(errors.New("resolver is not current"))
			case "ready":
				ready <- struct{}{}
				return nil
			default:
				return fmt.Errorf("unexpected target %q", job.Target)
			}
		},
		Interval: 2 * time.Second, HeartbeatEvery: 5 * time.Second,
		StaleAfter: 20 * time.Second, Who: "fair-deferral-runner",
	}
	go runner.Run(ctx)

	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("oldest blocked job did not execute")
	}
	select {
	case <-ready:
	case <-time.After(900 * time.Millisecond):
		t.Fatal("ready sibling waited for another jittered runner poll")
	}
	waitFor(t, 5*time.Second, func() bool {
		done, listErr := s.ListJobs(ctx, JobCallerLeaf, StatusDone)
		return listErr == nil && len(done) == 1 && done[0].Target == "ready"
	}, "ready sibling did not complete in the deferral drain turn")
	cancel()
}

type delayedDeferStore struct {
	Store
	delay time.Duration
}

func (state delayedDeferStore) DeferJob(
	ctx context.Context,
	job Job,
	errMsg string,
	delay time.Duration,
) (time.Time, error) {
	fence, err := state.Store.DeferJob(ctx, job, errMsg, delay)
	if err == nil {
		time.Sleep(state.delay)
	}
	return fence, err
}

func TestRunnerDependencyDeferralResponseLatencyDoesNotStarveSibling(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := s.EnqueuePending(ctx, JobCallerLeaf, "blocked-a", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := s.EnqueuePending(ctx, JobCallerLeaf, "blocked-b", false); err != nil {
		t.Fatal(err)
	}
	// Unbuffered delivery anchors the assertion window to the exact handler
	// start even when the host scheduler is contended.
	started := make(chan string)
	runner := &Runner{
		Store: delayedDeferStore{Store: s, delay: 700 * time.Millisecond},
		Kind:  JobCallerLeaf,
		Handle: func(_ context.Context, job Job) error {
			started <- job.Target
			return WithDeferral(errors.New("resolver is not current"))
		},
		Interval: 600 * time.Millisecond, HeartbeatEvery: 2 * time.Second,
		StaleAfter: 8 * time.Second, Who: "bounded-deferral-drain-runner",
	}
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		runner.Run(ctx)
	}()
	first := <-started
	second := <-started
	cancel()
	select {
	case <-runnerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not stop after response-latency assertion")
	}
	if first != "blocked-a" || second != "blocked-b" {
		t.Fatalf("delayed-response fairness starts = %q, %q, want blocked-a then blocked-b", first, second)
	}
}

func TestDeferJobPreservesImmediatelyPendingFreshnessEvent(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	if _, err := s.EnqueuePending(ctx, JobCallerLeaf, "repo", true); err != nil {
		t.Fatal(err)
	}
	active, err := s.ClaimJob(ctx, JobCallerLeaf, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *active, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.EnqueuePending(ctx, JobCallerLeaf, "repo", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeferJob(
		ctx, *active, "resolver is not current", time.Hour,
	); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimJob(ctx, JobCallerLeaf, "fresh-worker")
	if err != nil {
		t.Fatalf("freshness successor was delayed: %v", err)
	}
	if claimed.ID != fresh.ID || !claimed.Force || claimed.Attempts != 0 {
		t.Fatalf("claimed freshness successor = %+v, want %+v", claimed, fresh)
	}
}

func TestDeferJobFencesLeaseAndFreshEventWakesDeferredRow(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	queued, err := s.EnqueuePending(ctx, JobCallerLeaf, "repo", false)
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.ClaimJob(ctx, JobCallerLeaf, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *active, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	wrongOwner := *active
	wrongOwner.ClaimedBy = "other-worker"
	if _, err := s.DeferJob(
		ctx, wrongOwner, "pending", time.Hour,
	); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong-owner deferral = %v, want ErrLeaseLost", err)
	}
	if _, err := s.DeferJob(
		ctx, *active, "pending", time.Hour,
	); err != nil {
		t.Fatal(err)
	}
	woken, err := s.EnqueuePending(ctx, JobCallerLeaf, "repo", true)
	if err != nil {
		t.Fatal(err)
	}
	if woken.ID != queued.ID || woken.NotBefore != nil || !woken.Force {
		t.Fatalf("woken deferred row = %+v, want same immediate forced row", woken)
	}
	claimed, err := s.ClaimJob(ctx, JobCallerLeaf, "next-worker")
	if err != nil {
		t.Fatalf("fresh event did not wake deferred row: %v", err)
	}
	if claimed.ID != queued.ID || claimed.Attempts != 0 || !claimed.Force {
		t.Fatalf("claimed woken row = %+v", claimed)
	}
}

// Failing handlers retry with backoff, then land in failed with the error.
func TestRunnerRetriesThenFails(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	runs := 0
	r := &Runner{Store: s, Kind: JobSync,
		Handle: func(context.Context, Job) error {
			mu.Lock()
			runs++
			mu.Unlock()
			return errors.New("boom")
		},
		Interval: 30 * time.Millisecond, MaxAttempts: 2,
		Backoff: func(error, int) time.Duration { return time.Millisecond },
	}
	if _, err := s.CreateJob(ctx, JobSync, "doomed"); err != nil {
		t.Fatal(err)
	}
	go r.Run(ctx)

	waitFor(t, 15*time.Second, func() bool {
		failed, err := s.ListJobs(ctx, JobSync, StatusFailed)
		return err == nil && len(failed) == 1
	}, "job never failed permanently")
	cancel()

	mu.Lock()
	if runs != 2 {
		t.Errorf("handler ran %d times, want 2 (MaxAttempts)", runs)
	}
	failed, _ := s.ListJobs(context.Background(), JobSync, StatusFailed)
	if len(failed) != 1 || failed[0].Error != "boom" || failed[0].Attempts != 1 {
		t.Errorf("failed job = %+v, want error boom recorded", failed)
	}
	mu.Unlock()

	t.Run("handler-ensured successor", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var mu sync.Mutex
		runs := 0
		r := &Runner{
			Store: s, Kind: JobCallerLeaf,
			Handle: func(ctx context.Context, job Job) error {
				mu.Lock()
				runs++
				mu.Unlock()
				if _, err := s.EnsureJobSuccessor(ctx, job, false); err != nil {
					return err
				}
				return WithSuccessorRetry(errors.New("persistent install failure"))
			},
			Interval: 20 * time.Millisecond, HeartbeatEvery: 5 * time.Second,
			StaleAfter: 20 * time.Second, MaxAttempts: 3,
			Backoff: func(error, int) time.Duration { return time.Millisecond },
			Who:     "successor-exhaustion-worker",
		}
		if _, err := s.EnqueuePending(ctx, JobCallerLeaf, "repo", false); err != nil {
			t.Fatal(err)
		}
		go r.Run(ctx)

		waitFor(t, 15*time.Second, func() bool {
			failed, err := s.ListJobs(ctx, JobCallerLeaf, StatusFailed)
			return err == nil && len(failed) == 1 && failed[0].Attempts == 3
		}, "handler-ensured successor reset the exhausted attempt budget")
		cancel()

		mu.Lock()
		defer mu.Unlock()
		if runs != 3 {
			t.Fatalf("handler ran %d times, want 3", runs)
		}
		pending, err := s.ListJobs(context.Background(), JobCallerLeaf, StatusPending)
		if err != nil || len(pending) != 0 {
			t.Fatalf("pending successors after exhaustion = %+v, %v", pending, err)
		}
		failed, err := s.ListJobs(context.Background(), JobCallerLeaf, StatusFailed)
		if err != nil || len(failed) != 1 ||
			failed[0].Error != "persistent install failure" {
			t.Fatalf("exhausted successor = %+v, %v", failed, err)
		}
	})
}

func TestRunnerShutdownReleasesWithoutConsumingAttempt(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	r := &Runner{
		Store: s,
		Kind:  JobIndex,
		Handle: func(ctx context.Context, _ Job) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
		Interval: 20 * time.Millisecond,
		Who:      "shutdown-worker",
	}
	if _, err := s.EnqueuePending(ctx, JobIndex, "interrupted", false); err != nil {
		t.Fatal(err)
	}
	go r.Run(ctx)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never started")
	}
	cancel()

	waitFor(t, 10*time.Second, func() bool {
		jobs, err := s.ListJobs(context.Background(), JobIndex, StatusPending)
		return err == nil && len(jobs) == 1 && jobs[0].Attempts == 0
	}, "canceled job was not released without an attempt")
}

// T2.3 AC: a worker killed mid-job (simulated: stale heartbeat, no live
// heartbeater) is recovered by the reaper and re-executed by someone else.
func TestReaperRecoversDeadWorker(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()

	if _, err := s.CreateJob(ctx, JobSync, "victim"); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimJob(ctx, JobSync, "dead-worker")
	if err != nil {
		t.Fatal(err)
	}
	// simulate kill -9: claim exists, heartbeat frozen in the past
	if _, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE type::record($id) SET heartbeat_at = time::now() - 1h",
		map[string]any{"id": job.ID}); err != nil {
		t.Fatal(err)
	}
	// too fresh to reap with a 2h cutoff
	if n, err := s.ReapStale(ctx, JobSync, 2*time.Hour, 3); err != nil || n != 0 {
		t.Fatalf("ReapStale(2h) = %d, %v; want 0 reaped", n, err)
	}
	// stale for a 10min cutoff → back to pending
	n, err := s.ReapStale(ctx, JobSync, 10*time.Minute, 3)
	if err != nil || n != 1 {
		t.Fatalf("ReapStale(10m) = %d, %v; want 1 reaped", n, err)
	}

	rescued, err := s.ClaimJob(ctx, JobSync, "live-worker")
	if err != nil {
		t.Fatalf("rescued job not claimable: %v", err)
	}
	if rescued.Target != "victim" || rescued.Attempts != 1 || rescued.ClaimedBy != "live-worker" {
		t.Errorf("rescued = %+v, want victim with attempts=1 claimed by live-worker", rescued)
	}

	// exhausted attempts → reaper fails it instead of requeueing
	if _, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE type::record($id) SET heartbeat_at = time::now() - 1h, attempts = 5",
		map[string]any{"id": rescued.ID}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReapStale(ctx, JobSync, 10*time.Minute, 3); err != nil || n != 1 {
		t.Fatalf("ReapStale exhausted = %d, %v; want 1", n, err)
	}
	failed, _ := s.ListJobs(ctx, JobSync, StatusFailed)
	if len(failed) != 1 || failed[0].FinishedAt == nil {
		t.Errorf("failed = %+v, want the exhausted job finished", failed)
	}

	t.Run("final attempt preserves independent successor", func(t *testing.T) {
		if _, err := s.EnqueuePending(ctx, JobCallerLeaf, "repo", false); err != nil {
			t.Fatal(err)
		}
		active, err := s.ClaimJob(ctx, JobCallerLeaf, "dead-caller-worker")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetJobStatus(ctx, *active, StatusRunning, ""); err != nil {
			t.Fatal(err)
		}
		successor, err := s.EnsureJobSuccessor(ctx, *active, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := surrealdb.Query[any](ctx, s.db,
			"UPDATE type::record($id) SET heartbeat_at = time::now() - 1h, attempts = 2",
			map[string]any{"id": active.ID}); err != nil {
			t.Fatal(err)
		}
		if n, err := s.ReapStale(ctx, JobCallerLeaf, 10*time.Minute, 3); err != nil || n != 1 {
			t.Fatalf("final caller reap = %d, %v; want 1", n, err)
		}
		failed, err := s.ListJobs(ctx, JobCallerLeaf, StatusFailed)
		if err != nil || len(failed) != 1 || failed[0].ID != active.ID {
			t.Fatalf("reaped active = %+v, %v", failed, err)
		}
		pending, err := s.ListJobs(ctx, JobCallerLeaf, StatusPending)
		if err != nil || len(pending) != 1 || pending[0].ID != successor.ID ||
			pending[0].Attempts != 0 {
			t.Fatalf("crash successor = %+v, %v", pending, err)
		}
		rescued, err := s.ClaimJob(ctx, JobCallerLeaf, "reconciliation-worker")
		if err != nil || rescued.ID != successor.ID {
			t.Fatalf("claim crash successor = %+v, %v", rescued, err)
		}
	})
}

func TestReaperDoesNotStealRefreshedLease(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()

	for _, tt := range []struct {
		name        string
		attempts    int
		maxAttempts int
	}{
		{name: "requeue path", attempts: 0, maxAttempts: 3},
		{name: "exhausted path", attempts: 2, maxAttempts: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target := "refreshed-" + tt.name
			if _, err := s.CreateJob(ctx, JobSync, target); err != nil {
				t.Fatal(err)
			}
			claimed, err := s.ClaimJob(ctx, JobSync, "live-worker")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
				t.Fatal(err)
			}

			oldHeartbeat := time.Now().UTC().Add(-time.Hour)
			if _, err := surrealdb.Query[any](ctx, s.db,
				"UPDATE type::record($id) SET heartbeat_at = $heartbeat, attempts = $attempts",
				map[string]any{"id": claimed.ID, "heartbeat": oldHeartbeat, "attempts": tt.attempts}); err != nil {
				t.Fatal(err)
			}
			rows, err := s.ListJobs(ctx, JobSync, StatusRunning)
			if err != nil {
				t.Fatal(err)
			}
			var observed Job
			for _, row := range rows {
				if row.ID == claimed.ID {
					observed = row
				}
			}
			if observed.HeartbeatAt == nil {
				t.Fatal("stale candidate was not observed")
			}
			// ListJobs is a diagnostic projection and deliberately omits lease
			// authority. ReapStale's private active-row query carries the exact
			// lease fields; restore those here to exercise the heartbeat CAS.
			observed.LeaseToken = claimed.LeaseToken
			observed.ClaimedBy = claimed.ClaimedBy
			cutoff := time.Now().UTC().Add(-30 * time.Minute)

			// This heartbeat lands after the reaper's SELECT but before its
			// transition. The observed-heartbeat predicate must reject the reap.
			if err := s.HeartbeatJob(ctx, *claimed); err != nil {
				t.Fatal(err)
			}
			if err := s.reapOne(ctx, observed, cutoff, tt.maxAttempts); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("reap after refreshed heartbeat = %v, want ErrLeaseLost", err)
			}

			rows, err = s.ListJobs(ctx, JobSync, StatusRunning)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].ID != claimed.ID || rows[0].HeartbeatAt == nil || !rows[0].HeartbeatAt.After(cutoff) {
				t.Fatalf("running job after rejected reap = %+v", rows)
			}
			if err := s.SetJobStatus(ctx, *claimed, StatusDone, ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSchemaRequeuesLegacyUnfencedJob(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	job, err := s.CreateJob(ctx, JobSync, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE type::record($id) SET status = 'running', claimed_by = 'old-worker',
		 claimed_at = time::now(), heartbeat_at = time::now(), lease_token = NONE,
		 pending_key = NONE;
		 DELETE $marker`, map[string]any{
			"id": job.ID, "marker": jobActiveMigrationID(),
		}); err != nil {
		t.Fatal(err)
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListJobs(ctx, JobSync, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Attempts != 1 || pending[0].ClaimedBy != "" {
		t.Fatalf("migrated legacy job = %+v, want pending attempts=1 without owner", pending)
	}
}

func TestSchemaKeepsLegacyPendingSuccessor(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	active, err := s.CreateJob(ctx, JobSync, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE type::record($id) SET status = 'running', claimed_by = 'old-worker',
		 claimed_at = time::now(), heartbeat_at = time::now(), lease_token = NONE,
		 pending_key = NONE`, map[string]any{"id": active.ID}); err != nil {
		t.Fatal(err)
	}
	successor, err := s.CreateJob(ctx, JobSync, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE type::record($id) SET pending_key = NONE;
		 DELETE $marker`,
		map[string]any{"id": successor.ID, "marker": jobActiveMigrationID()}); err != nil {
		t.Fatal(err)
	}

	if err := s.applySchema(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListJobs(ctx, JobSync, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != successor.ID {
		t.Fatalf("pending after migration = %+v, want original successor", pending)
	}
	canceled, err := s.ListJobs(ctx, JobSync, StatusCanceled)
	if err != nil || len(canceled) != 1 || canceled[0].ID != active.ID {
		t.Fatalf("canceled after migration = %+v, %v; want legacy active", canceled, err)
	}
}

// Backoff gate: a requeued job is invisible to claims until not_before passes.
func TestRequeueBackoffGate(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()

	job, err := s.CreateJob(ctx, JobIndex, "slow")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimJob(ctx, JobIndex, "w1")
	if err != nil {
		t.Fatal(err)
	}
	gate := time.Now().UTC().Add(3 * time.Second)
	if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RequeueJob(ctx, *claimed, "try later", gate); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ClaimJob(ctx, JobIndex, "w2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("claim before backoff = %v, want ErrNotFound", err)
	}
	waitFor(t, 15*time.Second, func() bool {
		j, err := s.ClaimJob(ctx, JobIndex, "w2")
		return err == nil && j.ID == job.ID
	}, "job never became claimable after backoff")
}

type flakyRunnerStore struct {
	Store
	mu                 sync.Mutex
	terminalFailures   int
	statuses           []JobStatus
	statusErrors       []string
	releases           []Job
	deferrals          []Job
	deferredUntil      time.Time
	successorFailures  []Job
	heartbeatErr       error
	heartbeatLeaseLost bool
}

func (s *flakyRunnerStore) SetJobStatus(_ context.Context, _ Job, status JobStatus, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = append(s.statuses, status)
	s.statusErrors = append(s.statusErrors, message)
	if status != StatusRunning && s.terminalFailures > 0 {
		s.terminalFailures--
		return errors.New("temporary store failure")
	}
	return nil
}

func (s *flakyRunnerStore) HeartbeatJob(context.Context, Job) error {
	if s.heartbeatLeaseLost {
		return ErrLeaseLost
	}
	return s.heartbeatErr
}

func (s *flakyRunnerStore) FailJobWithSuccessor(
	_ context.Context,
	job Job,
	_ string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successorFailures = append(s.successorFailures, job)
	return nil
}

func (s *flakyRunnerStore) ReleaseJob(
	_ context.Context,
	job Job,
	_ string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, job)
	return nil
}

func (s *flakyRunnerStore) DeferJob(
	_ context.Context,
	job Job,
	_ string,
	_ time.Duration,
) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deferrals = append(s.deferrals, job)
	s.deferredUntil = time.Now().UTC().Add(time.Second)
	return s.deferredUntil, nil
}

func TestRunnerPersistsStructuralDurableErrorText(t *testing.T) {
	privateCause := errors.New("private /repo/path object deadbeef")
	refusal := pipelinerefusal.Unknown(
		privateCause,
		pipelinerefusal.StageObservationPublication,
		pipelinerefusal.GenerationObservation,
	)
	wrappedRefusal := WithTerminal(fmt.Errorf("outer private token: %w", refusal))
	if !errors.Is(wrappedRefusal, privateCause) {
		t.Fatal("outer wrapper lost the refusal cause")
	}
	ordinary := WithTerminal(fmt.Errorf("ordinary wrapper: %w", errors.New("ordinary cause")))

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "inner refusal", err: wrappedRefusal, want: refusal.Error()},
		{name: "ordinary error", err: ordinary, want: ordinary.Error()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := &flakyRunnerStore{}
			runner := &Runner{
				Store: st, Kind: JobExtract,
				Handle:         func(context.Context, Job) error { return test.err },
				HeartbeatEvery: time.Hour, MaxAttempts: 3, Who: "durable-error-worker",
			}
			runner.execute(context.Background(), Job{
				ID: "extraction_job:durable-error", Kind: JobExtract, Target: "repo",
				ClaimedBy: runner.Who, LeaseToken: "lease",
			})

			st.mu.Lock()
			defer st.mu.Unlock()
			if len(st.statuses) != 2 || st.statuses[1] != StatusFailed ||
				len(st.statusErrors) != 2 || st.statusErrors[1] != test.want {
				t.Fatalf("status/errors = %v/%q, want failed message %q", st.statuses, st.statusErrors, test.want)
			}
		})
	}
}

func TestRunnerRetriesTerminalPersistence(t *testing.T) {
	st := &flakyRunnerStore{terminalFailures: 2}
	r := &Runner{
		Store:          st,
		Kind:           JobSync,
		Handle:         func(context.Context, Job) error { return nil },
		HeartbeatEvery: time.Second,
		Who:            "terminal-retry",
	}
	r.execute(context.Background(), Job{
		ID: "connection_sync_job:test", Kind: JobSync, Target: "conn",
		ClaimedBy: "terminal-retry", LeaseToken: "lease",
	})

	st.mu.Lock()
	defer st.mu.Unlock()
	doneWrites := 0
	for _, status := range st.statuses {
		if status == StatusDone {
			doneWrites++
		}
	}
	if doneWrites != 3 {
		t.Errorf("done writes = %d, want 3 (two retries)", doneWrites)
	}
}

func TestRunnerTerminalMarkerFailsOnFirstExecution(t *testing.T) {
	st := &flakyRunnerStore{}
	r := &Runner{
		Store: st,
		Kind:  JobExtract,
		Handle: func(context.Context, Job) error {
			return WithTerminal(errors.New("deterministic refusal"))
		},
		HeartbeatEvery: time.Second,
		MaxAttempts:    3,
		Who:            "terminal-worker",
	}
	r.execute(context.Background(), Job{
		ID: "extraction_job:terminal", Kind: JobExtract, Target: "repo",
		ClaimedBy: "terminal-worker", LeaseToken: "lease",
	})

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.statuses) != 2 ||
		st.statuses[0] != StatusRunning ||
		st.statuses[1] != StatusFailed ||
		len(st.releases) != 0 {
		t.Fatalf("terminal transitions = %v releases=%d",
			st.statuses, len(st.releases))
	}
}

func TestRunnerTerminalSuccessorMarkerUsesProvenanceAwareFailure(t *testing.T) {
	st := &flakyRunnerStore{}
	r := &Runner{
		Store: st,
		Kind:  JobResolverCatalog,
		Handle: func(context.Context, Job) error {
			return WithTerminal(WithSuccessorRetry(
				errors.New("nondeterministic publication"),
			))
		},
		HeartbeatEvery: time.Second,
		MaxAttempts:    3,
		Who:            "terminal-successor-worker",
	}
	job := Job{
		ID:   "resolver_catalog_job:terminal-successor",
		Kind: JobResolverCatalog, Target: "repo",
		ClaimedBy: r.Who, LeaseToken: "lease",
	}
	r.execute(context.Background(), job)

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.statuses) != 1 || st.statuses[0] != StatusRunning ||
		len(st.successorFailures) != 1 ||
		st.successorFailures[0].ID != job.ID {
		t.Fatalf("terminal successor transitions = statuses:%v failures:%+v",
			st.statuses, st.successorFailures)
	}
}

func TestRunnerYieldReleasesWithoutConsumingAttempt(t *testing.T) {
	st := &flakyRunnerStore{}
	r := &Runner{
		Store: st,
		Kind:  JobExtract,
		Handle: func(context.Context, Job) error {
			return WithYield(errors.New("bounded work remains"))
		},
		HeartbeatEvery: time.Second,
		MaxAttempts:    3,
		Who:            "yield-worker",
	}
	job := Job{
		ID: "extraction_job:yield", Kind: JobExtract, Target: "repo",
		Attempts: 2, ClaimedBy: "yield-worker", LeaseToken: "lease",
	}
	r.execute(context.Background(), job)

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.statuses) != 1 || st.statuses[0] != StatusRunning ||
		len(st.releases) != 1 || st.releases[0].Attempts != job.Attempts {
		t.Fatalf("yield transitions = %v releases=%+v",
			st.statuses, st.releases)
	}
}

func TestRunnerDependencyDeferralPreservesAttempt(t *testing.T) {
	st := &flakyRunnerStore{}
	var reports []JobLifecycleReport
	r := &Runner{
		Store: st,
		Kind:  JobCallerLeaf,
		Handle: func(context.Context, Job) error {
			return WithDeferral(errors.New("resolver is not current"))
		},
		Interval:       time.Second,
		HeartbeatEvery: time.Second,
		MaxAttempts:    3,
		Who:            "dependency-worker",
		LifecycleReports: func(raw []byte) error {
			var report JobLifecycleReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return err
			}
			reports = append(reports, report)
			return nil
		},
	}
	job := Job{
		ID: "caller_leaf_job:deferred", Kind: JobCallerLeaf, Target: "repo",
		Attempts: 2, ClaimedBy: "dependency-worker", LeaseToken: "lease",
	}
	r.execute(context.Background(), job)

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.statuses) != 1 || st.statuses[0] != StatusRunning ||
		len(st.deferrals) != 1 || st.deferrals[0].Attempts != job.Attempts {
		t.Fatalf("deferral transitions = %v deferrals=%+v", st.statuses, st.deferrals)
	}
	if len(reports) != 2 || reports[1].Event != "deferred" ||
		reports[1].NextNotBefore != st.deferredUntil.Format(time.RFC3339Nano) {
		t.Fatalf("deferral lifecycle = %+v, fence=%s", reports, st.deferredUntil)
	}
}

func TestRunnerStopsHandlerWhenHeartbeatLosesLease(t *testing.T) {
	st := &flakyRunnerStore{heartbeatLeaseLost: true}
	handlerStopped := make(chan struct{})
	r := &Runner{
		Store: st,
		Kind:  JobSync,
		Handle: func(ctx context.Context, _ Job) error {
			<-ctx.Done()
			close(handlerStopped)
			return ctx.Err()
		},
		HeartbeatEvery: 10 * time.Millisecond,
		Who:            "stale-worker",
	}
	r.execute(context.Background(), Job{
		ID: "connection_sync_job:test", Kind: JobSync, Target: "conn",
		ClaimedBy: "stale-worker", LeaseToken: "old-lease",
	})
	select {
	case <-handlerStopped:
	default:
		t.Fatal("handler context was not canceled after lease loss")
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.statuses) != 1 || st.statuses[0] != StatusRunning {
		t.Errorf("status writes = %v, want only running transition", st.statuses)
	}
}

func TestRunnerTreatsHeartbeatReplacementAsPossiblyCommittedSuccessor(t *testing.T) {
	st := &flakyRunnerStore{heartbeatErr: errors.New("heartbeat unavailable")}
	r := &Runner{
		Store: st,
		Kind:  JobCallerLeaf,
		Handle: func(ctx context.Context, _ Job) error {
			<-ctx.Done()
			return nil
		},
		HeartbeatEvery: 5 * time.Millisecond,
		MaxAttempts:    3,
		Who:            "heartbeat-successor-worker",
	}
	job := Job{
		ID: "caller_leaf_job:test", Kind: JobCallerLeaf, Target: "repo",
		Attempts: 2, ClaimedBy: r.Who, LeaseToken: "lease",
	}
	r.execute(context.Background(), job)

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.statuses) != 1 || st.statuses[0] != StatusRunning ||
		len(st.successorFailures) != 1 ||
		st.successorFailures[0].ID != job.ID {
		t.Fatalf("heartbeat successor transitions = statuses:%v failures:%+v",
			st.statuses, st.successorFailures)
	}
}

// T30.6h scheduling repair: a successor created by a successfully completed
// turn starts with a fresh attempt budget, while a requeued failing job with
// a pending successor still merges its attempts forward. Success resets the
// chain; only consecutive failures on the same work can exhaust it.
func TestSuccessorAfterDoneTurnStartsWithFreshAttemptBudget(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	if _, err := s.CreateJob(ctx, JobCallerLeaf, "github.com/acme/turns"); err != nil {
		t.Fatal(err)
	}
	claim := func(wantAttempts int) Job {
		t.Helper()
		job, err := s.ClaimJob(ctx, JobCallerLeaf, "runner-test")
		if err != nil {
			t.Fatal(err)
		}
		if job.Attempts != wantAttempts {
			t.Fatalf("claimed attempts = %d, want %d", job.Attempts, wantAttempts)
		}
		if err := s.SetJobStatus(ctx, *job, StatusRunning, ""); err != nil {
			t.Fatal(err)
		}
		return *job
	}

	// Burn one attempt the way a deadline-expired replay turn does.
	job := claim(0)
	if err := s.RequeueJob(
		ctx, job, "worker deadline", time.Now().UTC().Add(-time.Second),
	); err != nil {
		t.Fatal(err)
	}

	// The retried claim carries the burned attempt; completing that turn
	// after ensuring a successor must not pass the count forward.
	job = claim(1)
	if _, err := s.EnsureJobSuccessor(ctx, job, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, job, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	successor := claim(0)

	// A failing turn that already ensured a successor still merges its
	// incremented attempts into the pending row: the path a genuinely
	// oversized pair uses to eventually exhaust its budget.
	if _, err := s.EnsureJobSuccessor(ctx, successor, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RequeueJob(
		ctx, successor, "worker deadline", time.Now().UTC().Add(-time.Second),
	); err != nil {
		t.Fatal(err)
	}
	claim(1)
}
