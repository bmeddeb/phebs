package generationscheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

type settlementStore struct {
	*schedulerStore
	operationErr error
}

func (s *settlementStore) CompleteGenerationChunk(ctx context.Context, c store.GenerationChunk) error {
	_ = s.schedulerStore.CompleteGenerationChunk(ctx, c)
	return s.operationErr
}
func (s *settlementStore) FailGenerationChunk(ctx context.Context, c store.GenerationChunk, v string) error {
	_ = s.schedulerStore.FailGenerationChunk(ctx, c, v)
	return s.operationErr
}
func (s *settlementStore) DeferGenerationChunk(ctx context.Context, c store.GenerationChunk, v string, d time.Duration) error {
	_ = s.schedulerStore.DeferGenerationChunk(ctx, c, v, d)
	return s.operationErr
}

func TestTypedSettlementEveryPath(t *testing.T) {
	boom := errors.New("injected")
	for _, name := range []string{"complete", "fail", "defer", "retry", "exhaust", "release", "complete-error", "fail-error", "defer-error", "retry-error", "release-error", "heartbeat", "pre-heartbeat", "hook-error", "nil"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			base := &schedulerStore{}
			fake := &settlementStore{schedulerStore: base}
			if strings.HasSuffix(name, "-error") && name != "hook-error" {
				fake.operationErr = boom
				base.retryErr = boom
				base.releaseErr = boom
			}
			if name == "exhaust" {
				base.retryErr = store.ErrGenerationExhausted
			}
			if name == "heartbeat" {
				base.heartbeatErr = store.ErrGenerationLeaseLost
			}
			chunk := store.GenerationChunk{Identity: "original", LeaseToken: "old-lease", ClaimedBy: "worker"}
			callbacks, reports := 0, 0
			scheduler := &Scheduler{Store: fake, HeartbeatEvery: time.Millisecond, StaleAfter: time.Second, StoreCallTimeout: time.Second, Backoff: func(int) time.Duration { return time.Millisecond }, Report: func(error) { reports++ }}
			configuration := Class{Handle: func(ctx context.Context, _ store.GenerationChunk, _ Budget) error {
				switch name {
				case "heartbeat":
					<-ctx.Done()
					return ctx.Err()
				case "release", "release-error":
					cancel()
					return ctx.Err()
				case "fail", "fail-error":
					return store.WithTerminal(boom)
				case "defer", "defer-error":
					return store.WithDeferral(boom)
				case "retry", "retry-error", "exhaust":
					return boom
				}
				return nil
			}, AfterSettlement: func(ctx context.Context, got store.GenerationChunk) error {
				callbacks++
				if got != chunk || ctx.Err() != nil {
					t.Fatal("lost original lease or stop context")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded hook")
				}
				base.mu.Lock()
				transitions := base.completed + base.failed + base.deferred + base.retried + base.released
				base.mu.Unlock()
				if name != "heartbeat" && name != "pre-heartbeat" && transitions != 1 {
					t.Fatalf("hook before settlement: %d", transitions)
				}
				if name == "hook-error" {
					return boom
				}
				return nil
			}}
			if name == "pre-heartbeat" {
				configuration.BeforeLeaseHeartbeat = func(context.Context, store.GenerationChunk) error { return boom }
			}
			if name == "nil" {
				configuration.AfterSettlement = nil
			}
			scheduler.execute(ctx, configuration, chunk)
			expected := 1
			if name == "nil" {
				expected = 0
			}
			if callbacks != expected {
				t.Fatalf("callbacks %d", callbacks)
			}
			if name == "hook-error" && (base.completed != 1 || reports != 1) {
				t.Fatalf("completion rewritten or failure hidden: %d %d", base.completed, reports)
			}
		})
	}
}
