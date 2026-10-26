//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

type t422HTTPBridgeStore interface {
	store.GenerationSchedulerStore
	store.AuthStore
	RetireCurrentGenerationSchedule(context.Context, store.GenerationSchedule) error
}

// Ordinary SDK component probe, not selected-process SA or native acceptance.
// The immutable three-part image and publication/fencing doubles are inherited
// from the reviewed bridge. Lease rows, CAS, completion, inventory, SDK charges,
// callback contexts, HTTP report and release below execute their real paths.
func TestT422CheckpointRecoveryRealStoreHTTPBridge(t *testing.T) {
	runT422CheckpointHTTPBridgeCases(t, true)
}

func prepareT422RealStoreBridge(t *testing.T, bridge *t422CheckpointBridge, private string) {
	t.Helper()
	ctx := t.Context()
	bridge.DataDir = filepath.Join(private, "database")
	if err := os.Mkdir(bridge.DataDir, 0o700); err != nil {
		t.Fatal("create private test-owned data directory refused")
	}
	state, err := store.OpenLocalMemory(ctx, bridge.DataDir)
	if err != nil {
		t.Fatal("start test-owned real store refused")
	}
	t.Cleanup(func() {
		bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := state.Close(bounded); err != nil {
			t.Error("test-owned store closure refused")
		}
	})
	s := bridge.Schedule
	schedule, err := state.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{Repository: s.Repository, Stage: s.Stage,
		Generation: s.Generation, ResourceClass: s.ResourceClass, TotalItems: s.TotalItems, ChunkItems: s.ChunkItems,
		MaxAttempts: s.MaxAttempts, RepositoryTokens: s.RepositoryTokens})
	if err != nil || schedule == nil || schedule.Digest != s.Digest {
		t.Fatal("actual bridge schedule binding refused")
	}
	if _, err := state.ExpandGenerationSchedule(ctx, s.Repository, s.Stage, s.Generation); err != nil {
		t.Fatal("actual bridge expansion refused")
	}
	for offset := int64(0); offset <= 2; offset++ {
		chunk, err := state.ClaimGenerationChunk(ctx, s.ResourceClass, "real-bridge-checkpoint")
		if err != nil || chunk == nil || chunk.Offset != offset || chunk.Length != 1 || chunk.Attempt != 0 {
			t.Fatal("actual bridge checkpoint claim refused")
		}
		if offset != 2 {
			// The exported immutable image already contains these two results.
			// Seed their actual completion rows; do not claim their production run.
			if err := state.CompleteGenerationChunk(ctx, *chunk); err != nil {
				t.Fatal("actual bridge predecessor completion refused")
			}
			continue
		}
		event, err := state.ReadGenerationStaleLeaseTransition(ctx, store.GenerationStaleLeaseTransitionRequest{
			Point: store.GenerationStaleLeaseTransitionCheckpointHit, Repository: s.Repository, Stage: s.Stage, Generation: s.Generation,
			ResourceClass: s.ResourceClass, ScheduleDigest: s.Digest, ChunkIdentity: chunk.Identity, Offset: 2, Length: 1})
		if err != nil {
			t.Fatal("actual bridge checkpoint row refused")
		}
		runtime := &extractionpublication.Runtime{Root: bridge.RuntimeRoot, Store: state, Source: t422BridgeUnused{}, Executor: t422BridgeUnused{},
			Publisher: t422BridgePublisher{}, Fence: t422BridgeFence{s.Repository, bridge.Input.Hit.SourceGenerationDigest}}
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		accounted, ledger, err := readaccounting.Start(bounded, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
		if err != nil {
			cancel()
			t.Fatal("actual bridge checkpoint ledger refused")
		}
		observed, projection := readaccounting.CaptureRecoverySchedule(accounted)
		hit := bridge.Input.Hit
		value, readErr := runtime.ReadCheckpointRestartTransition(observed, extractionpublication.CheckpointRestartTransitionRequest{
			Transition: event, TargetGeneration: hit.TargetGeneration, PriorScheduleDigest: hit.PriorScheduleDigest,
			Domain: hit.Domain, Ordinal: 2, PlanDigest: hit.PlanDigest, ResultIdentity: hit.ResultIdentity})
		counts, finishErr := ledger.Finish()
		cancel()
		inventory, available := projection.Observation()
		if readErr != nil || finishErr != nil || counts != (readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4}) ||
			!available || inventory.Chunks != 3 || inventory.Successes != 2 || inventory.ScheduleSHA256 != s.Digest {
			t.Fatalf("actual bridge checkpoint read refused: read_error=%t finish_error=%t counts=%+v inventory=%t", readErr != nil, finishErr != nil, counts, available)
		}
		value.ObservedScheduleChunks, value.ObservedScheduleSuccesses = inventory.Chunks, inventory.Successes
		bridge.Input.Hit = value
		t.Logf("real_bridge_checkpoint controls=%d store=%d chunks=%d successes=%d", counts.ControlFileReads, counts.StoreReadAttempts, inventory.Chunks, inventory.Successes)
	}
	// Same twenty-second lease contract. The pinned SDK encodes the query
	// cutoff at whole-second precision, so cross its fractional boundary too.
	timer := time.NewTimer(21*time.Second + 100*time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("actual bridge lease expiry wait canceled")
	}
}

type t422RealBridgeStore struct {
	*store.Surreal
	sequence *t422RecoverySchedulerStore
}

func openT422RealStoreBridge(t *testing.T, ctx context.Context, dataDir string, sequence *t422RecoverySchedulerStore) *t422RealBridgeStore {
	t.Helper()
	local, err := store.ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal("read private test-owned runtime descriptor refused")
	}
	// This helper has DA/PC admission, not SA bootstrap. Its normal SDK path
	// retains the existing nil-owner contract; no flags or owner checks change.
	state, err := store.Open(ctx, local.Endpoint, "root", local.Pass, "phebs", "phebs")
	if err != nil {
		t.Fatal("connect test-owned ordinary SDK refused")
	}
	t.Cleanup(func() {
		bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := state.Close(bounded); err != nil {
			t.Error("ordinary SDK connection closure refused")
		}
	})
	return &t422RealBridgeStore{state, sequence}
}

func (state *t422RealBridgeStore) ReapStaleGenerationChunksObserved(ctx context.Context, class store.GenerationResourceClass, after time.Duration, observer store.GenerationStaleLeaseTransitionObserver) (int, error) {
	seq := state.sequence
	if seq.reaped.Swap(true) {
		return 0, nil
	}
	seq.reaperContext <- ctx
	count, err := state.Surreal.ReapStaleGenerationChunksObserved(ctx, class, after, func(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
		if event.Point == store.GenerationStaleLeaseTransitionHit {
			seq.hit = event
		}
		if event.Point == store.GenerationStaleLeaseTransitionRequeued && seq.beforeRequeue {
			// The actual store has committed. Delay only observer delivery, while
			// allowing the real worker to reclaim/complete under the new lease.
			close(seq.available)
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				seq.control.mu.Lock()
				entered := seq.control.recovered.observer != nil
				seq.control.mu.Unlock()
				if entered {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
			}
		}
		if err := observer(ctx, event); err != nil {
			return err
		}
		if event.Point == store.GenerationStaleLeaseTransitionRequeued && !seq.beforeRequeue {
			close(seq.available)
		}
		return nil
	})
	if err == nil && count != 1 {
		return count, errors.New("actual bridge reaper did not commit one lease")
	}
	return count, err
}

func (state *t422RealBridgeStore) ClaimGenerationChunk(ctx context.Context, class store.GenerationResourceClass, worker string) (*store.GenerationChunk, error) {
	seq := state.sequence
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-seq.available:
	}
	if seq.claimed.Swap(true) {
		return nil, store.ErrNotFound
	}
	if !seq.beforeRequeue {
		reaper := <-seq.reaperContext
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-reaper.Done():
		}
		if !errors.Is(reaper.Err(), context.Canceled) {
			return nil, errors.New("completed actual reaper was not canceled")
		}
	}
	return state.Surreal.ClaimGenerationChunk(ctx, class, worker)
}

func (state *t422RealBridgeStore) CompleteGenerationChunk(ctx context.Context, chunk store.GenerationChunk) error {
	if err := state.Surreal.CompleteGenerationChunk(ctx, chunk); err != nil {
		return err
	}
	state.sequence.completed.Store(true)
	return nil
}
