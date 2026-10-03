package main

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedexecutor"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

// Installation is a trusted composition seam, deliberately left nil by serve.
// The Bazel registration gate must provide exact installed custody and lookup;
// no configuration, environment or browser input can enable this seam yet.
type typedServeInstallation struct {
	Workspace, Socket, Image string
	Bundle                   typedexecutor.BundleLookup
}

type typedServeRuntime struct {
	runtime    *typedexecutor.Runtime
	owner      typedexecutor.LifecycleOwner
	resolver   *typedCodeNavigationResolver
	pending    atomic.Bool
	recovering atomic.Bool
}

func prepareServeTypedIndex(d *serveDeps) error {
	if d.typedInstallation == nil {
		return nil
	}
	if d.typedRuntime != nil || d.semanticLaunch != nil || d.exactReads || d.exactReports || runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		return typedServeSafeError(typedexecutor.ErrUnavailable)
	}
	i := *d.typedInstallation
	c, err := typedexecutor.New(typedexecutor.Config{
		Store: d.st, Workspace: i.Workspace, Acquire: d.acquireLifecycleMutation,
		Socket: i.Socket, Image: i.Image,
	})
	if err != nil {
		return typedServeSafeError(err)
	}
	r, err := typedexecutor.NewRuntime(c, i.Bundle)
	if err != nil {
		return typedServeSafeError(err)
	}
	// Reconcile authenticates the complete custody census before stale reaping
	// or recovery. No coordinator, scheduler or reader is installed before it.
	if err := r.Reconcile(d.ctx); err != nil {
		return typedServeSafeError(err)
	}
	resolver, err := newTypedCodeNavigationResolver(d.st, i.Workspace)
	if err != nil {
		return typedServeSafeError(err)
	}
	r.Report = func(_ typedexecutor.Outcome, err error) {
		diagnostics.Logf("typed-index execution state=%s", typedServeReason(err))
	}
	d.typedRuntime = &typedServeRuntime{runtime: r, owner: typedexecutor.LifecycleOwner{Controller: c}, resolver: resolver}
	return nil
}

func (r *typedServeRuntime) Name() string { return r.owner.Name() }
func (r *typedServeRuntime) Sweep(ctx context.Context, now time.Time, cursor string, limits lifecycle.Limits) lifecycle.OwnerResult {
	result := r.owner.Sweep(ctx, now, cursor, limits)
	result.Err = typedServeSafeError(result.Err)
	return result
}

func (r *typedServeRuntime) bindScheduler(s *generationscheduler.Scheduler) {
	class := s.Classes[store.GenerationResourceTypedIndex]
	handle, settle := class.Handle, class.AfterSettlement
	class.Handle = func(ctx context.Context, chunk store.GenerationChunk, budget generationscheduler.Budget) error {
		if r.pending.Load() || r.recovering.Load() {
			return typedServeSafeError(store.WithDeferral(typedexecutor.ErrHeld))
		}
		return typedServeSafeError(handle(ctx, chunk, budget))
	}
	class.AfterSettlement = func(ctx context.Context, chunk store.GenerationChunk) error {
		err := settle(ctx, chunk)
		if err != nil {
			r.pending.Store(true)
		}
		return typedServeSafeError(err)
	}
	s.Classes[store.GenerationResourceTypedIndex] = class
}

func startServeTypedIndex(d *serveDeps) error {
	r := d.typedRuntime
	if r == nil {
		return nil
	}
	scheduler, err := r.runtime.Scheduler(d.ctx)
	if err != nil {
		return typedServeSafeError(err)
	}
	r.bindScheduler(scheduler)
	scheduler.Owners = d.startup.Owners()
	scheduler.PollEvery = d.cfg.Sync.Interval()
	scheduler.WorkerPrefix = "typed-index-worker"
	scheduler.Diagnostics = d.cfg.Diagnostics.Jobs
	scheduler.Report = func(err error) {
		diagnostics.Logf("typed-index scheduler state=%s", typedServeReason(err))
	}
	coordinator := &store.Runner{
		Store: d.st, Kind: store.JobTypedIndex, Owners: d.startup.Owners(),
		Interval: d.cfg.Sync.Interval(), Diagnostics: d.cfg.Diagnostics.Jobs,
		Handle: func(ctx context.Context, job store.Job) error {
			return typedServeSafeError(r.runtime.Coordinator(ctx, job))
		},
	}
	runStoreRunner(d.ctx, d.runBackground, coordinator)
	d.runBackground(func() {
		if err := scheduler.Run(d.ctx); err != nil && d.ctx.Err() == nil {
			diagnostics.Logf("typed-index scheduler stopped state=%s", typedServeReason(err))
		}
	})
	d.runBackground(func() {
		ticker := time.NewTicker(d.cfg.Sync.Interval())
		defer ticker.Stop()
		for {
			select {
			case <-d.ctx.Done():
				return
			case <-ticker.C:
			}
			if !r.pending.Load() {
				continue
			}
			// Block admission before consuming the pending failure, including
			// the interval before Reconcile acquires the controller guard.
			// A new failure during recovery remains pending afterward.
			// Idle ticks perform no SDK or filesystem work.
			r.recovering.Store(true)
			r.pending.Store(false)
			turn, err := d.startup.Owners().Enter(d.ctx)
			if err != nil {
				r.recovering.Store(false)
				return
			}
			err = r.runtime.Reconcile(d.ctx)
			turn.End()
			if err != nil {
				r.pending.Store(true)
				if d.ctx.Err() == nil {
					diagnostics.Logf("typed-index recovery state=%s", typedServeReason(err))
				}
			}
			r.recovering.Store(false)
		}
	})
	return nil
}

// Preserve scheduler/runner classification through Unwrap while keeping native
// diagnostics and paths out of ordinary logs and durable job error text.
type typedServeError struct{ cause error }

func (e typedServeError) Error() string            { return "typed-index runtime: " + typedServeReason(e.cause) }
func (e typedServeError) Unwrap() error            { return e.cause }
func (e typedServeError) DurableErrorText() string { return e.Error() }
func typedServeSafeError(err error) error {
	if err == nil {
		return nil
	}
	return typedServeError{err}
}
func typedServeReason(err error) string {
	if err == nil {
		return "ok"
	}
	for _, reason := range []typedindex.Refusal{typedindex.Disabled, typedindex.Forbidden, typedindex.Invalid, typedindex.Unsupported, typedindex.Stale, typedindex.Capacity, typedindex.Unprepared, typedindex.WallLimit, typedindex.ExecutionFailed, typedindex.Containment, typedindex.Canceled} {
		if errors.Is(err, reason) {
			return string(reason)
		}
	}
	switch {
	case errors.Is(err, typedexecutor.ErrHeld):
		return "custody_held"
	case errors.Is(err, typedexecutor.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "wall_limit"
	case errors.Is(err, lifecycle.ErrPressureRefusal), errors.Is(err, lifecycle.ErrCapacityUnavailable):
		return "capacity_refused"
	default:
		return "failed"
	}
}
