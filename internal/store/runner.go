package store

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"time"
)

// Runner is a jittered polling worker over one job table (PLAN §1: polling,
// never LIVE SELECT wakeups). Multiple runners — in one process or many —
// coordinate purely through ClaimJob.
type Runner struct {
	Store  Store
	Kind   JobKind
	Handle func(ctx context.Context, job Job) error

	Interval       time.Duration // base poll cadence, jittered to [0.5x, 1.5x); default 15s
	HeartbeatEvery time.Duration // default Interval/3
	StaleAfter     time.Duration // reaper cutoff; default 4x HeartbeatEvery
	MaxAttempts    int           // executions before a job is failed; default 3
	Backoff        func(err error, attempts int) time.Duration // default DefaultBackoff (per-class)
	Who            string                                      // claim owner label; default host-pid
}

func (r *Runner) defaults() {
	if r.Interval == 0 {
		r.Interval = 15 * time.Second
	}
	if r.HeartbeatEvery == 0 {
		r.HeartbeatEvery = r.Interval / 3
	}
	if r.StaleAfter == 0 {
		r.StaleAfter = 4 * r.HeartbeatEvery
	}
	if r.MaxAttempts == 0 {
		r.MaxAttempts = 3
	}
	if r.Backoff == nil {
		r.Backoff = DefaultBackoff
	}
	if r.Who == "" {
		host, _ := os.Hostname()
		r.Who = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
}

// Run polls until ctx is cancelled: reap stale claims, then drain the queue.
func (r *Runner) Run(ctx context.Context) {
	r.defaults()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(r.Interval)):
		}
		if n, err := r.Store.ReapStale(ctx, r.Kind, r.StaleAfter, r.MaxAttempts); err == nil && n > 0 {
			log.Printf("runner %s: reaped %d stale %s", r.Who, n, r.Kind)
			jobsTotal.WithLabelValues(string(r.Kind), "reaped").Add(float64(n))
		}
		for ctx.Err() == nil {
			job, err := r.Store.ClaimJob(ctx, r.Kind, r.Who)
			if err != nil {
				break // drained (ErrNotFound), cancelled, or store error — next poll retries
			}
			r.execute(ctx, *job)
		}
	}
}

func (r *Runner) execute(ctx context.Context, job Job) {
	_ = r.Store.SetJobStatus(ctx, job.ID, StatusRunning, "")

	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go func() {
		t := time.NewTicker(r.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				_ = r.Store.HeartbeatJob(hbCtx, job.ID)
			}
		}
	}()

	err := r.Handle(ctx, job)
	stopHB()
	switch {
	case err == nil:
		recordJob(r.Kind, "done")
		_ = r.Store.SetJobStatus(ctx, job.ID, StatusDone, "")
	case job.Attempts+1 >= r.MaxAttempts:
		log.Printf("runner %s: %s %s failed permanently: %v", r.Who, job.Kind, job.Target, err)
		recordJob(r.Kind, "failed")
		recordJobError(r.Kind, err)
		_ = r.Store.SetJobStatus(ctx, job.ID, StatusFailed, err.Error())
	default:
		recordJob(r.Kind, "requeued")
		recordJobError(r.Kind, err)
		_ = r.Store.RequeueJob(ctx, job.ID, err.Error(), time.Now().UTC().Add(r.Backoff(err, job.Attempts+1)))
	}
}

// jitter draws uniformly from [0.5x, 1.5x) so pollers spread out instead of
// stampeding the queue together.
func jitter(base time.Duration) time.Duration {
	return base/2 + rand.N(base)
}
