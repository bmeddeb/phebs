package store

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type lifecycleRunnerStore struct {
	flakyRunnerStore
	job     Job
	claimed bool
}

func (s *lifecycleRunnerStore) ReapStale(
	context.Context,
	JobKind,
	time.Duration,
	int,
) (int, error) {
	return 0, nil
}

func (s *lifecycleRunnerStore) ClaimJob(
	_ context.Context,
	_ JobKind,
	who string,
) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed {
		return nil, ErrNotFound
	}
	s.claimed = true
	claimed := s.job
	now := time.Now().UTC()
	claimed.ClaimedAt = &now
	claimed.ClaimedBy = who
	claimed.LeaseToken = "diagnostic-lease"
	return &claimed, nil
}

func TestRunnerLifecycleFollowsPersistedSuccess(t *testing.T) {
	created := time.Now().UTC().Add(-time.Second)
	state := &lifecycleRunnerStore{job: Job{
		ID: "connection_sync_job:lifecycle", Kind: JobSync,
		Target: "example.com/acme/service", CreatedAt: created,
	}}
	var mu sync.Mutex
	var reports []JobLifecycleReport
	done := make(chan struct{})
	runner := &Runner{
		Store: state, Kind: JobSync,
		Handle:         func(context.Context, Job) error { return nil },
		Interval:       time.Millisecond,
		HeartbeatEvery: time.Second,
		Who:            "diagnostic-worker",
		LifecycleReports: func(raw []byte) error {
			var report JobLifecycleReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return err
			}
			mu.Lock()
			reports = append(reports, report)
			if report.Event == "done" {
				close(done)
			}
			mu.Unlock()
			return nil
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go runner.Run(ctx)
	select {
	case <-done:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not complete diagnostic job")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 3 || reports[0].Event != "claimed" ||
		reports[1].Event != "started" || reports[2].Event != "done" ||
		reports[2].Outcome != "success" {
		t.Fatalf("lifecycle reports = %+v", reports)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.statuses) != 2 || state.statuses[0] != StatusRunning ||
		state.statuses[1] != StatusDone {
		t.Fatalf("persisted statuses = %v", state.statuses)
	}
}

func TestJobLifecycleReceiptUsesEligibilityAndBoundedFields(t *testing.T) {
	created := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	notBefore := created.Add(2 * time.Minute)
	claimed := notBefore.Add(7 * time.Second)
	next := claimed.Add(4 * time.Minute)
	job := Job{
		ID: "caller_leaf_job:receipt", Kind: JobCallerLeaf,
		Target: "example.com/acme/service", Attempts: 1,
		CreatedAt: created, NotBefore: &notBefore, ClaimedAt: &claimed,
	}
	var raw []byte
	runner := &Runner{LifecycleReports: func(report []byte) error {
		raw = append([]byte(nil), report...)
		return nil
	}}
	runner.emitLifecycle("requeued", job, 3*time.Second+4*time.Millisecond, "retryable", &next)

	var report JobLifecycleReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != JobLifecycleSchema || report.Event != "requeued" ||
		report.JobID != job.ID || report.Kind != JobCallerLeaf ||
		report.Attempt != 2 || report.QueueWaitMS != 7000 ||
		report.HandleMS != 3004 || report.Outcome != "retryable" ||
		report.NextNotBefore != next.Format(time.RFC3339Nano) {
		t.Fatalf("receipt = %+v", report)
	}
	for _, forbidden := range []string{"error", "source_path", "lease_token"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("receipt contains %q: %s", forbidden, raw)
		}
	}
}

func TestJobLifecycleReceiptCapDropsOversizedTarget(t *testing.T) {
	called := false
	runner := &Runner{LifecycleReports: func([]byte) error {
		called = true
		return nil
	}}
	runner.emitLifecycle("claimed", Job{
		ID: "job:oversized", Kind: JobSync,
		Target: strings.Repeat("x", MaxJobLifecycleReportSize),
	}, 0, "claimed", nil)
	if called {
		t.Fatal("oversized job lifecycle receipt reached sink")
	}
}
