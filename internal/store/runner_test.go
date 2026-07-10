package store

// Internal tests: the reaper test manipulates heartbeat timestamps directly.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"

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
		Backoff: func(int) time.Duration { return time.Millisecond },
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
	defer mu.Unlock()
	if runs != 2 {
		t.Errorf("handler ran %d times, want 2 (MaxAttempts)", runs)
	}
	failed, _ := s.ListJobs(context.Background(), JobSync, StatusFailed)
	if len(failed) != 1 || failed[0].Error != "boom" || failed[0].Attempts != 1 {
		t.Errorf("failed job = %+v, want error boom recorded", failed)
	}
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
	if err := s.RequeueJob(ctx, claimed.ID, "try later", gate); err != nil {
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
