package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

var errLocalEngineQuiescence = errors.New("owned local engine quiescence unavailable")

const localEngineStopTimeout = 5 * time.Second

// Wait is owned only here. Holding mu keeps the child unreaped throughout a
// measurement, so its PID cannot be recycled between native identity checks.
type localEngine struct {
	mu            sync.Mutex
	process       *os.Process
	handle        dispatchadmission.Handle
	stopped       bool
	paused        bool
	removeRuntime func()
}

func (engine *localEngine) stop() {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.stopLocked()
}

func (engine *localEngine) stopLocked() {
	if engine.stopped {
		return
	}
	engine.stopped = true
	if engine.removeRuntime != nil {
		engine.removeRuntime()
	}
	if engine.paused {
		_ = resumeLocalEngine(engine.process)
	}
	_ = engine.process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = engine.handle.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(localEngineStopTimeout):
		_ = engine.process.Kill()
		<-done
	}
}

// WithQuiescentLocalEngine runs a synchronous local measurement while this
// selected store's actual owned engine is stopped and its SDK is idle-locked.
// It supplies neither a whole-custody writer fence nor a measurement policy.
// The caller must already exclude every other custody writer and request. The
// callback must not call the SDK, this method, Close, or semantic/report sinks
// that inspect the SDK owner. No PID/path supplied by the caller is signalled.
// The callback must honor ctx and return: native operations and this callback
// are cooperative, and concurrent shutdown waits for them rather than claiming
// a hard deadline. The separate cleanup timeout bounds resumed-state polling.
func (s *Surreal) WithQuiescentLocalEngine(ctx context.Context, measure func(context.Context) error) error {
	if s == nil || s.accounting == nil {
		return errLocalEngineQuiescence
	}
	return s.engine.withQuiescent(ctx, s.accounting.SDKOwner, measure)
}

func (engine *localEngine) withQuiescent(ctx context.Context, owner *storeaccounting.SDKOwner, measure func(context.Context) error) error {
	if owner == nil {
		return errLocalEngineQuiescence
	}
	return engine.withMeasurement(ctx, measure, owner.WithIdle)
}

// WithRetiredLocalEngine measures this still-owned engine after its selected
// SDK owner has successfully closed. Its caller must separately prove the
// retired producer and exclude every other custody writer. Callback, deadline,
// process ownership and shutdown rules match WithQuiescentLocalEngine; this
// does not reopen the SDK or republish a live-backup runtime descriptor.
func (s *Surreal) WithRetiredLocalEngine(ctx context.Context, measure func(context.Context) error) error {
	if s == nil || s.accounting == nil || s.accounting.SDKOwner == nil {
		return errLocalEngineQuiescence
	}
	return s.engine.withMeasurement(ctx, measure, s.accounting.WithClosed)
}

func (engine *localEngine) withMeasurement(ctx context.Context, measure func(context.Context) error, withOwner func(context.Context, func() error) error) error {
	if engine == nil || ctx == nil || measure == nil {
		return errLocalEngineQuiescence
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return errLocalEngineQuiescence
	}
	// Same lock order as Close: owned engine, then SDK owner. Runtime-file
	// removal and connection shutdown cannot race the measurement interval.
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.stopped || engine.paused || ctx.Err() != nil {
		return errors.Join(errLocalEngineQuiescence, ctx.Err())
	}
	return withOwner(ctx, func() error {
		return engine.measureStopped(ctx, measure)
	})
}
