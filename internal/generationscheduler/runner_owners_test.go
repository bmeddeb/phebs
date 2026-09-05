package generationscheduler

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

type ownerSchedulerStore struct {
	*schedulerStore
	boundary string
	entered  chan struct{}
	release  chan struct{}
	calls    atomic.Int32
}

func (state *ownerSchedulerStore) block(ctx context.Context) error {
	if state.calls.Add(1) == 1 {
		close(state.entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-state.release:
		}
	}
	return nil
}

func (state *ownerSchedulerStore) ExpandNextGenerationSchedule(ctx context.Context, _ store.GenerationResourceClass) (*store.GenerationSchedule, error) {
	if state.boundary == "plan" {
		return nil, state.block(ctx)
	}
	return nil, store.ErrNotFound
}

func (state *ownerSchedulerStore) ReapStaleGenerationChunks(ctx context.Context, _ store.GenerationResourceClass, _ time.Duration) (int, error) {
	if state.boundary == "reap" {
		return 0, state.block(ctx)
	}
	return 0, nil
}

func (state *ownerSchedulerStore) CompleteGenerationChunk(ctx context.Context, chunk store.GenerationChunk) error {
	if state.boundary == "settlement" {
		if err := state.block(ctx); err != nil {
			return err
		}
	}
	return state.schedulerStore.CompleteGenerationChunk(ctx, chunk)
}

func TestSchedulerOwnersCoverPlanReapSettlementAndReports(t *testing.T) {
	for _, boundary := range []string{"plan", "reap", "settlement", "report"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
			if err != nil {
				t.Fatal(err)
			}
			state := &ownerSchedulerStore{schedulerStore: &schedulerStore{}, boundary: boundary,
				entered: make(chan struct{}), release: make(chan struct{})}
			state.chunks = []store.GenerationChunk{{Identity: "owner", ResourceClass: store.GenerationResourceIO}}
			scheduler := &Scheduler{Store: state, Owners: owners, PollEvery: time.Millisecond,
				HeartbeatEvery: time.Second, StaleAfter: time.Minute, StoreCallTimeout: time.Second,
				ChunkReports: func(raw []byte) error {
					var report ChunkLifecycleReport
					if err := json.Unmarshal(raw, &report); err != nil {
						return err
					}
					if boundary == "report" && report.Event == "settled" {
						return state.block(ctx)
					}
					return nil
				}}
			done := make(chan struct{})
			go func() {
				defer close(done)
				switch boundary {
				case "plan":
					scheduler.plan(ctx, store.GenerationResourceIO)
				case "reap":
					scheduler.reap(ctx, store.GenerationResourceIO, nil)
				default:
					scheduler.work(ctx, store.GenerationResourceIO, Class{
						Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil },
					}, 0)
				}
			}()
			t.Cleanup(func() { cancel(); <-done })
			select {
			case <-state.entered:
			case <-ctx.Done():
				t.Fatal("native owner operation did not arrive")
			}
			paused := make(chan error, 1)
			go func() { paused <- owners.Pause(ctx) }()
			for {
				probeCtx, stop := context.WithTimeout(ctx, time.Millisecond)
				probe, err := owners.Enter(probeCtx)
				stop()
				if err != nil {
					break
				}
				probe.End()
			}
			select {
			case err := <-paused:
				t.Fatalf("pause preceded complete %s: %v", boundary, err)
			default:
			}
			close(state.release)
			select {
			case err := <-paused:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("scheduler turn did not drain")
			}
			if owners.Err() != nil {
				t.Fatal(owners.Err())
			}
			if err := owners.Resume(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
