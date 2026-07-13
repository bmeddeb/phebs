package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestClassifyAndBackoff(t *testing.T) {
	base := errors.New("boom")
	tests := []struct {
		name        string
		err         error
		wantClass   ErrClass
		wantBackoff time.Duration // at attempts=1
	}{
		{"unclassified", base, ClassGeneric, time.Minute},
		{"auth", WithClass(ClassAuth, base), ClassAuth, 20 * time.Minute},
		{"oom", WithClass(ClassOOM, base), ClassOOM, 10 * time.Minute},
		{"corrupt", WithClass(ClassCorrupt, base), ClassCorrupt, 2 * time.Second},
		{"extract", WithClass(ClassExtract, base), ClassExtract, 4 * time.Minute},
		{"wrapped classified", fmt.Errorf("outer: %w", WithClass(ClassAuth, base)), ClassAuth, 20 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.wantClass {
				t.Errorf("Classify = %q, want %q", got, tt.wantClass)
			}
			if got := DefaultBackoff(tt.err, 1); got != tt.wantBackoff {
				t.Errorf("DefaultBackoff(attempts=1) = %v, want %v", got, tt.wantBackoff)
			}
		})
	}

	if got := DefaultBackoff(WithClass(ClassAuth, base), 10); got != time.Hour {
		t.Errorf("backoff cap = %v, want 1h", got)
	}
	if !errors.Is(WithClass(ClassAuth, base), base) {
		t.Error("WithClass broke the error chain")
	}
}

// T3.3 AC: a classified failure lands with its class's retry schedule and
// shows up under the right metrics label.
func TestClassifiedFailureSchedule(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &Runner{Store: s, Kind: JobSync,
		Handle: func(context.Context, Job) error {
			return WithClass(ClassAuth, errors.New("bad token"))
		},
		Interval: 30 * time.Millisecond, MaxAttempts: 3,
	}
	if _, err := s.CreateJob(ctx, JobSync, "locked-out"); err != nil {
		t.Fatal(err)
	}
	go r.Run(ctx)

	var requeued *Job
	waitFor(t, 15*time.Second, func() bool {
		jobs, err := s.ListJobs(ctx, JobSync, StatusPending)
		if err == nil && len(jobs) == 1 && jobs[0].Attempts == 1 {
			requeued = &jobs[0]
			return true
		}
		return false
	}, "job never requeued with attempts=1")
	cancel()

	// attempts=1 on an auth failure → next try no sooner than 10m << 1 = 20m
	if requeued.NotBefore == nil {
		t.Fatal("requeued job has no backoff gate")
	}
	if wait := time.Until(*requeued.NotBefore); wait < 15*time.Minute || wait > 25*time.Minute {
		t.Errorf("auth-class backoff = %v, want ~20m", wait)
	}
	if requeued.Error != "auth: bad token" {
		t.Errorf("recorded error = %q, want class-prefixed message", requeued.Error)
	}

	// The row transition and metric update are consecutive but not atomic;
	// wait until the runner records the successful requeue.
	waitFor(t, 5*time.Second, func() bool {
		families, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			return false
		}
		for _, mf := range families {
			if mf.GetName() != "phebs_job_errors_total" {
				continue
			}
			for _, m := range mf.GetMetric() {
				labels := map[string]string{}
				for _, l := range m.GetLabel() {
					labels[l.GetName()] = l.GetValue()
				}
				if labels["class"] == "auth" && strings.Contains(labels["kind"], "sync") && m.GetCounter().GetValue() >= 1 {
					return true
				}
			}
		}
		return false
	}, `phebs_job_errors_total{class="auth"} not incremented`)
}
