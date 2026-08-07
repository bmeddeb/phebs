package store

import (
	"context"
	"errors"
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

	Interval       time.Duration                               // base poll cadence, jittered to [0.5x, 1.5x); default 15s
	HeartbeatEvery time.Duration                               // default Interval/3
	StaleAfter     time.Duration                               // reaper cutoff; default 4x HeartbeatEvery
	MaxAttempts    int                                         // executions before a job is failed; default 3
	Backoff        func(err error, attempts int) time.Duration // default DefaultBackoff (per-class)
	Who            string                                      // claim owner label; default host-pid

	Diagnostics      bool
	LifecycleReports JobLifecycleSink
}

type durableErrorTexter interface {
	DurableErrorText() string
}

// DurableErrorText selects a structurally supplied persistence-safe message
// from anywhere in an error chain. Errors without that method retain their
// ordinary rendered message exactly.
func DurableErrorText(err error) string {
	if err == nil {
		return ""
	}
	var durable durableErrorTexter
	if errors.As(err, &durable) {
		return durable.DurableErrorText()
	}
	return err.Error()
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
		if n, err := r.Store.ReapStale(ctx, r.Kind, r.StaleAfter, r.MaxAttempts); err != nil {
			if ctx.Err() == nil {
				log.Printf("runner %s: reap stale %s: %v", r.Who, r.Kind, err)
			}
		} else if n > 0 {
			log.Printf("runner %s: reaped %d stale %s", r.Who, n, r.Kind)
			jobsTotal.WithLabelValues(string(r.Kind), "reaped").Add(float64(n))
		}
		for ctx.Err() == nil {
			job, err := r.Store.ClaimJob(ctx, r.Kind, r.Who)
			if err != nil {
				if !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
					log.Printf("runner %s: claim %s: %v", r.Who, r.Kind, err)
				}
				break // drained, canceled, or store error; next poll retries
			}
			r.emitLifecycle("claimed", *job, 0, "claimed", nil)
			r.execute(ctx, *job)
		}
	}
}

func (r *Runner) execute(ctx context.Context, job Job) {
	if ctx.Err() != nil {
		if r.persist(job, "release claimed job", func(writeCtx context.Context) error {
			return r.Store.ReleaseJob(writeCtx, job, "runner shutting down")
		}) {
			r.emitLifecycle("released", job, 0, "canceled", nil)
		}
		return
	}
	if !r.persist(job, "start job", func(writeCtx context.Context) error {
		return r.Store.SetJobStatus(writeCtx, job, StatusRunning, "")
	}) {
		return
	}
	r.emitLifecycle("started", job, 0, "running", nil)
	if ctx.Err() != nil {
		if r.persist(job, "release started job", func(writeCtx context.Context) error {
			return r.Store.ReleaseJob(writeCtx, job, "runner shutting down")
		}) {
			r.emitLifecycle("released", job, 0, "canceled", nil)
		}
		return
	}

	handleCtx, stopHandle := context.WithCancel(ctx)
	hbDone := make(chan error, 1)
	go func() {
		t := time.NewTicker(r.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-handleCtx.Done():
				hbDone <- nil
				return
			case <-t.C:
				hbCtx, cancel := context.WithTimeout(handleCtx, r.HeartbeatEvery)
				err := r.Store.HeartbeatJob(hbCtx, job)
				cancel()
				if err != nil {
					if handleCtx.Err() != nil {
						hbDone <- nil
						return
					}
					stopHandle()
					hbDone <- err
					return
				}
			}
		}
	}()

	handleStarted := time.Time{}
	if r.Diagnostics || r.LifecycleReports != nil {
		handleStarted = time.Now()
	}
	err := r.Handle(handleCtx, job)
	handleDuration := time.Duration(0)
	if !handleStarted.IsZero() {
		handleDuration = time.Since(handleStarted)
	}
	stopHandle()
	hbErr := <-hbDone
	if hbErr != nil {
		if errors.Is(hbErr, ErrLeaseLost) {
			log.Printf("runner %s: lease lost for %s %s", r.Who, job.Kind, job.Target)
			return
		}
		log.Printf("runner %s: heartbeat %s %s: %v", r.Who, job.Kind, job.Target, hbErr)
		if err == nil || errors.Is(err, context.Canceled) {
			err = fmt.Errorf("heartbeat: %w", hbErr)
			// A handler may have committed its recovery successor and returned nil
			// just before this heartbeat failure won the race. The provenance-aware
			// transition is also safe when no successor exists.
			err = WithSuccessorRetry(err)
		}
	}

	switch {
	case err == nil:
		if r.persist(job, "complete job", func(writeCtx context.Context) error {
			return r.Store.SetJobStatus(writeCtx, job, StatusDone, "")
		}) {
			recordJob(r.Kind, "done")
			r.emitLifecycle("done", job, handleDuration, "success", nil)
		}
	case ctx.Err() != nil:
		if r.persist(job, "release canceled job", func(writeCtx context.Context) error {
			return r.Store.ReleaseJob(writeCtx, job, ctx.Err().Error())
		}) {
			recordJob(r.Kind, "released")
			r.emitLifecycle("released", job, handleDuration, "canceled", nil)
		}
	case IsTerminal(err):
		log.Printf("runner %s: %s %s refused terminally: %v",
			r.Who, job.Kind, job.Target, err)
		action := "fail terminal job"
		if IsSuccessorRetry(err) {
			action = "fail terminal job with pending successor"
		}
		if r.persist(job, action, func(writeCtx context.Context) error {
			if IsSuccessorRetry(err) {
				return r.Store.FailJobWithSuccessor(writeCtx, job, DurableErrorText(err))
			}
			return r.Store.SetJobStatus(writeCtx, job, StatusFailed, DurableErrorText(err))
		}) {
			recordJob(r.Kind, "failed")
			recordJobError(r.Kind, err)
			r.emitLifecycle("failed", job, handleDuration, "terminal", nil)
		}
	case IsYield(err):
		if r.persist(job, "yield job", func(writeCtx context.Context) error {
			return r.Store.ReleaseJob(writeCtx, job, DurableErrorText(err))
		}) {
			recordJob(r.Kind, "deferred")
			r.emitLifecycle("yielded", job, handleDuration, "yield", nil)
		}
	case job.Attempts+1 >= r.MaxAttempts:
		log.Printf("runner %s: %s %s failed permanently: %v", r.Who, job.Kind, job.Target, err)
		action := "fail job"
		if IsSuccessorRetry(err) {
			action = "fail job with pending successor"
		}
		if r.persist(job, action, func(writeCtx context.Context) error {
			if IsSuccessorRetry(err) {
				return r.Store.FailJobWithSuccessor(writeCtx, job, DurableErrorText(err))
			}
			return r.Store.SetJobStatus(writeCtx, job, StatusFailed, DurableErrorText(err))
		}) {
			recordJob(r.Kind, "failed")
			recordJobError(r.Kind, err)
			r.emitLifecycle("failed", job, handleDuration, "attempts_exhausted", nil)
		}
	default:
		var nextNotBefore time.Time
		if r.persist(job, "requeue job", func(writeCtx context.Context) error {
			nextNotBefore = time.Now().UTC().Add(
				r.Backoff(err, job.Attempts+1),
			)
			return r.Store.RequeueJob(writeCtx, job, DurableErrorText(err), nextNotBefore)
		}) {
			recordJob(r.Kind, "requeued")
			recordJobError(r.Kind, err)
			r.emitLifecycle("requeued", job, handleDuration, "retryable", &nextNotBefore)
		}
	}
}

// persist retries state writes on a context detached from shutdown. Lease loss
// is definitive; all other failures are retried briefly and reported.
func (r *Runner) persist(job Job, action string, write func(context.Context) error) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var last error
	for {
		last = write(ctx)
		if last == nil {
			return true
		}
		if errors.Is(last, ErrLeaseLost) {
			log.Printf("runner %s: %s %s %s: %v", r.Who, action, job.Kind, job.Target, last)
			return false
		}
		select {
		case <-ctx.Done():
			log.Printf("runner %s: %s %s %s after retries: %v", r.Who, action, job.Kind, job.Target, last)
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// jitter draws uniformly from [0.5x, 1.5x) so pollers spread out instead of
// stampeding the queue together.
func jitter(base time.Duration) time.Duration {
	return base/2 + rand.N(base)
}
