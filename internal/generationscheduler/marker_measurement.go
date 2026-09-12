package generationscheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

var ErrMarkerMeasurement = errors.New("generation marker measurement unavailable or failed")

type markerMeasurementKey struct{}

// MarkerMeasurement exists only inside one actually claimed, opted-in handler.
// It parks that heartbeat at a safe boundary, not its active owner slot. The
// ticker and original claim persist; resumption requires a natural successful
// heartbeat before any reaper or peer is readmitted.
type MarkerMeasurement struct {
	mu                                           sync.Mutex
	ctx                                          context.Context
	turn                                         dispatchadmission.OwnerTurn
	chunk                                        store.GenerationChunk
	pause, parked, resume, refreshed, stop, done chan struct{}
	stopOnce                                     sync.Once
	used, complete, measuring, ended             bool
	err, firstBeat                               error
}

func MarkerMeasurementFromContext(ctx context.Context) *MarkerMeasurement {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(markerMeasurementKey{}).(*MarkerMeasurement)
	return value
}

func newMarkerMeasurement(ctx context.Context, turn dispatchadmission.OwnerTurn, chunk store.GenerationChunk) (*MarkerMeasurement, error) {
	if ctx == nil || ctx.Err() != nil || turn == (dispatchadmission.OwnerTurn{}) || chunk.Status != store.GenerationChunkRunning ||
		chunk.Identity == "" || chunk.ScheduleDigest == "" || chunk.LeaseToken == "" || chunk.ClaimedBy == "" {
		return nil, ErrMarkerMeasurement
	}
	return &MarkerMeasurement{ctx: ctx, turn: turn, chunk: chunk, pause: make(chan struct{}), parked: make(chan struct{}),
		resume: make(chan struct{}), refreshed: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}, nil
}

func (measurement *MarkerMeasurement) Matches(chunk store.GenerationChunk) bool {
	if measurement == nil {
		return false
	}
	measurement.mu.Lock()
	defer measurement.mu.Unlock()
	actual := measurement.chunk
	return !measurement.ended && measurement.err == nil && measurement.ctx.Err() == nil &&
		actual.Identity == chunk.Identity && actual.ScheduleDigest == chunk.ScheduleDigest && actual.Repository == chunk.Repository &&
		actual.Stage == chunk.Stage && actual.Generation == chunk.Generation && actual.LeaseToken == chunk.LeaseToken && actual.ClaimedBy == chunk.ClaimedBy
}

// Measure's callback is synchronous and must return before the original
// deadline. confirm supplies retained-owner evidence, never ordinary drainage.
// ready runs only after actual engine/SDK release, heartbeat confirmation and
// owner/request reopening. A positive sample is not itself readiness.
func (measurement *MarkerMeasurement) Measure(ctx context.Context, sample func(context.Context, func() bool) error, ready func() error) (retErr error) {
	if measurement == nil {
		return ErrMarkerMeasurement
	}
	measurement.mu.Lock()
	valid := ctx != nil && ctx.Err() == nil && measurement.ctx.Err() == nil && !measurement.used && !measurement.ended &&
		measurement.err == nil && measurement.firstBeat == nil && sample != nil && ready != nil
	if valid {
		_, valid = ctx.Deadline()
	}
	if !valid {
		measurement.err = ErrMarkerMeasurement
		measurement.mu.Unlock()
		return ErrMarkerMeasurement
	}
	measurement.used = true
	measurement.mu.Unlock()
	completed := false
	defer func() {
		measurement.mu.Lock()
		measurement.measuring = false
		if !completed {
			measurement.err = errors.Join(ErrMarkerMeasurement, retErr)
		}
		measurement.mu.Unlock()
	}()
	owners, err := measurement.turn.FenceMeasurement(ctx)
	if err != nil {
		return err
	}
	close(measurement.pause)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-measurement.ctx.Done():
		return measurement.ctx.Err()
	case <-measurement.done:
		return ErrMarkerMeasurement
	case <-measurement.parked:
	}
	measurement.mu.Lock()
	measurement.measuring = true
	measurement.mu.Unlock()
	confirm := func() bool {
		measurement.mu.Lock()
		valid := measurement.measuring && !measurement.ended && measurement.err == nil && measurement.firstBeat == nil && measurement.ctx.Err() == nil
		measurement.mu.Unlock()
		return valid && owners.Quiescent(ctx)
	}
	if !confirm() {
		return ErrMarkerMeasurement
	}
	if err := sample(ctx, confirm); err != nil {
		return err
	}
	if !confirm() {
		return ErrMarkerMeasurement
	}
	measurement.mu.Lock()
	measurement.measuring = false
	measurement.mu.Unlock()
	close(measurement.resume)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-measurement.ctx.Done():
		return measurement.ctx.Err()
	case <-measurement.done:
		return ErrMarkerMeasurement
	case <-measurement.refreshed:
	}
	measurement.mu.Lock()
	healthy := measurement.err == nil && measurement.firstBeat == nil && !measurement.ended && measurement.ctx.Err() == nil
	measurement.mu.Unlock()
	if !healthy || ctx.Err() != nil {
		return ErrMarkerMeasurement
	}
	if err := owners.Resume(ctx); err != nil {
		return err
	}
	if err := ready(); err != nil {
		return err
	}
	if ctx.Err() != nil || measurement.ctx.Err() != nil {
		return ErrMarkerMeasurement
	}
	measurement.mu.Lock()
	measurement.complete = true
	measurement.mu.Unlock()
	completed = true
	return nil
}

func (measurement *MarkerMeasurement) beat(scheduler *Scheduler, cancel context.CancelFunc) {
	defer close(measurement.done)
	ticker := time.NewTicker(scheduler.HeartbeatEvery)
	defer ticker.Stop()
	lastConfirmed := time.Now()
	if at := measurement.chunk.HeartbeatAt; at != nil && at.Before(lastConfirmed) {
		lastConfirmed = *at
	}
	pause := measurement.pause
	refresh := false
	for {
		select {
		case <-measurement.ctx.Done():
			return
		case <-measurement.stop:
			return
		case <-pause:
			close(measurement.parked) // Every previously admitted heartbeat has returned.
			select {
			case <-measurement.ctx.Done():
				return
			case <-measurement.stop:
				return
			case <-measurement.resume:
			}
			pause, refresh = nil, true
			continue
		case <-ticker.C:
		}
		// A simultaneously ready pause must win before admission of another beat.
		select {
		case <-pause:
			continue
		default:
		}
		select {
		case <-measurement.stop:
			return
		default:
		}
		if measurement.ctx.Err() != nil {
			return
		}
		started := time.Now()
		ctx, release := context.WithTimeout(measurement.ctx, scheduler.storeCallTimeout())
		err := scheduler.Store.HeartbeatGenerationChunk(ctx, measurement.chunk)
		release()
		if err == nil {
			lastConfirmed = started
			if refresh {
				close(measurement.refreshed)
				refresh = false
			}
			continue
		}
		measurement.mu.Lock()
		if measurement.firstBeat == nil {
			measurement.firstBeat = err
		}
		measurement.mu.Unlock()
		if measurement.ctx.Err() != nil {
			return
		}
		if refresh || errors.Is(err, store.ErrGenerationLeaseLost) || errors.Is(err, store.ErrGenerationStale) || time.Since(lastConfirmed) >= scheduler.StaleAfter {
			measurement.mu.Lock()
			measurement.err = err
			measurement.mu.Unlock()
			cancel()
			return
		}
	}
}

func (measurement *MarkerMeasurement) finish(cancel context.CancelFunc) error {
	measurement.stopOnce.Do(func() { close(measurement.stop) })
	<-measurement.done
	cancel()
	measurement.mu.Lock()
	defer measurement.mu.Unlock()
	measurement.ended = true
	return measurement.err
}

func (measurement *MarkerMeasurement) failedContinuation(handlerErr, contextErr error) bool {
	measurement.mu.Lock()
	defer measurement.mu.Unlock()
	return measurement.err != nil || measurement.used && (!measurement.complete || handlerErr != nil || contextErr != nil)
}
