//go:build darwin || linux

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
)

// Only the scheduler operations overridden below are allowed. An unexpected
// operation on the nil embedded interface fails the helper rather than faking
// successful storage. This supplies one durable-shaped unit, not a database.
type t422RecoverySchedulerStore struct {
	store.GenerationSchedulerStore
	control          *t422CheckpointRecoveryControl
	hit, requeued    store.GenerationStaleLeaseTransition
	available        chan struct{}
	recoveredEntered chan struct{}
	reaperContext    chan context.Context
	beforeRequeue    bool
	reaped, claimed  atomic.Bool
	completed        atomic.Bool
}

func (state *t422RecoverySchedulerStore) ExpandNextGenerationSchedule(context.Context, store.GenerationResourceClass) (*store.GenerationSchedule, error) {
	return nil, store.ErrNotFound
}

func (state *t422RecoverySchedulerStore) ReapStaleGenerationChunksObserved(ctx context.Context, _ store.GenerationResourceClass, _ time.Duration, observer store.GenerationStaleLeaseTransitionObserver) (int, error) {
	if state.reaped.Swap(true) {
		return 0, nil
	}
	state.reaperContext <- ctx
	if err := observer(ctx, state.hit); err != nil {
		return 0, err
	}
	if state.beforeRequeue {
		// Model the durable commit releasing the row before its post-commit
		// observer runs. The worker can finish concurrently with that observer.
		close(state.available)
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-state.recoveredEntered:
		}
		// Entry to the wrapper is insufficient: require the production
		// callback's actual latch before publishing Requeued.
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			state.control.mu.Lock()
			entered := state.control.recovered.observer != nil
			state.control.mu.Unlock()
			if entered {
				break
			}
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if err := observer(ctx, state.requeued); err != nil {
		return 1, err
	}
	if !state.beforeRequeue {
		close(state.available)
	}
	return 1, nil // The actual scheduler cancels this call's context next.
}

func (state *t422RecoverySchedulerStore) ClaimGenerationChunk(ctx context.Context, _ store.GenerationResourceClass, _ string) (*store.GenerationChunk, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-state.available:
	}
	if state.claimed.Swap(true) {
		return nil, store.ErrNotFound
	}
	if !state.beforeRequeue {
		// Do not manufacture cancellation: require the real scheduler to
		// finish and cancel its own observed reaper call before reclaiming.
		reaper := <-state.reaperContext
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-reaper.Done():
		}
	}
	event := state.hit
	return &store.GenerationChunk{ID: "bounded-recovery-fixture", Identity: event.ChunkIdentity,
		Repository: event.Repository, Stage: event.Stage, Generation: event.Generation,
		ResourceClass: event.ResourceClass, ScheduleDigest: event.ScheduleDigest,
		Offset: event.Offset, Length: event.Length, Attempt: event.Attempt,
		Priority: store.GenerationPriorityStale, Status: store.GenerationChunkRunning,
		LeaseToken: "new-native-fixture-lease"}, nil
}

func (state *t422RecoverySchedulerStore) HeartbeatGenerationChunk(context.Context, store.GenerationChunk) error {
	return nil
}

func (state *t422RecoverySchedulerStore) CompleteGenerationChunk(context.Context, store.GenerationChunk) error {
	state.completed.Store(true)
	return nil
}

// Exercise the actual scheduler's five-second callback creation/cancellation
// and owner/heartbeat joins against the inherited checkpoint control. Native R
// data and HTTP accounting below remain supplied fixtures, not native proof.
func startT422RecoveryScheduler(t *testing.T, ctx context.Context, owners *dispatchadmission.Owners, control *t422CheckpointRecoveryControl, hit, requeued store.GenerationStaleLeaseTransition, beforeRequeue bool) (<-chan error, func()) {
	t.Helper()
	state := &t422RecoverySchedulerStore{control: control, hit: hit, requeued: requeued, available: make(chan struct{}),
		recoveredEntered: make(chan struct{}), reaperContext: make(chan context.Context, 1), beforeRequeue: beforeRequeue}
	finished := make(chan error, 1)
	reports := make(chan error, 8)
	scheduler := &generationscheduler.Scheduler{Store: state, Owners: owners,
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{store.GenerationResourceExtraction: {
			Concurrency: 1, Budget: generationscheduler.Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
			Handle: func(context.Context, store.GenerationChunk, generationscheduler.Budget) error { return nil },
			OnStaleLeaseTransition: func(callback context.Context, event store.GenerationStaleLeaseTransition) error {
				if event.Point == store.GenerationStaleLeaseTransitionRecovered {
					close(state.recoveredEntered)
				}
				err := control.transition(callback, event)
				if event.Point == store.GenerationStaleLeaseTransitionRecovered {
					finished <- err
				}
				return err
			},
		}}, PollEvery: time.Hour, HeartbeatEvery: time.Hour, StaleAfter: 2 * time.Hour,
		Report: func(err error) { reports <- err }}
	runCtx, cancel := context.WithCancel(ctx)
	joined := make(chan error, 1)
	go func() { joined <- scheduler.Run(runCtx) }()
	return finished, func() {
		cancel()
		if err := <-joined; err != nil {
			t.Fatal("real scheduler join", err)
		}
		if !state.reaped.Load() || !state.claimed.Load() || !state.completed.Load() {
			t.Fatal("scheduler did not execute the supplied durable transition")
		}
		select {
		case err := <-reports:
			t.Fatal("unexpected scheduler refusal", err)
		default:
		}
	}
}
