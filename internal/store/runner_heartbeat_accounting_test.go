//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

type runnerHeartbeatAdmissionStore struct {
	flakyRunnerStore
	native   *Surreal
	entered  chan context.Context
	submit   chan struct{}
	returned chan error
}

func (state *runnerHeartbeatAdmissionStore) HeartbeatJob(ctx context.Context, job Job) error {
	select {
	case state.entered <- ctx:
	default:
	}
	// Supplied scheduling boundary immediately before the actual source recipe.
	// Deliberately forward cancellation instead of inventing a successful beat.
	<-state.submit
	err := state.native.HeartbeatJob(ctx, job)
	select {
	case state.returned <- err:
	default:
	}
	return err
}

// The real runner and source heartbeat use the selected SDK/SA owner;
// ordinary status persistence and WebSocket engine replies are supplied by
// existing fixtures. No database, native lease, or rehearsal is started.
func TestRunnerSuccessfulHandlerHeartbeatAccountingBoundary(t *testing.T) {
	for _, name := range []string{"heartbeat_reply_before_handler_return", "handler_return_before_heartbeat_submission", "outer_cancel_before_heartbeat_submission"} {
		t.Run(name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			runnerCtx, cancelRunner := context.WithCancel(ctx)
			defer cancelRunner()
			canceled := name == "outer_cancel_before_heartbeat_submission"
			db, native := storeAccountingDB(t, ctx, owner)
			native.call = func(context.Context, *connection.RPCRequest) (any, error) {
				return []surrealdb.QueryResult[[]jobRec]{{Status: "OK", Result: []jobRec{{}}}}, nil
			}
			state := &runnerHeartbeatAdmissionStore{
				native: &Surreal{db: db, accounting: owner}, entered: make(chan context.Context, 1),
				submit: make(chan struct{}), returned: make(chan error, 1),
			}
			handlerReturn := make(chan struct{})
			var releaseHandler, releaseHeartbeat sync.Once
			runner := Runner{Store: state, Kind: JobSync, HeartbeatEvery: time.Second, StaleAfter: 20 * time.Second,
				Handle: func(context.Context, Job) error { <-handlerReturn; return nil }}
			job := Job{ID: "connection_sync_job:heartbeat-boundary", Kind: JobSync, Target: "local/heartbeat-boundary",
				LeaseToken: "fixture-lease", ClaimedBy: "fixture-worker"}
			done := make(chan struct{})
			go func() { defer close(done); runner.execute(runnerCtx, job) }()
			t.Cleanup(func() {
				releaseHandler.Do(func() { close(handlerReturn) })
				releaseHeartbeat.Do(func() { close(state.submit) })
				joinCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				runnerOwnerSignal(t, joinCtx, done)
			})
			var heartbeatCtx context.Context
			select {
			case heartbeatCtx = <-state.entered:
			case <-ctx.Done():
				t.Fatal("heartbeat did not reach supplied pre-submission boundary")
			}
			if name != "heartbeat_reply_before_handler_return" {
				releaseHandler.Do(func() { close(handlerReturn) })
				if canceled {
					cancelRunner()
					runnerOwnerSignal(t, ctx, heartbeatCtx.Done())
					if heartbeatCtx.Err() != context.Canceled || ctx.Err() != nil {
						t.Fatal("outer runner cancellation was confused with deadline or owner cancellation", heartbeatCtx.Err(), ctx.Err())
					}
				} else {
					// The virtual-time Runner regression separately proves the cleanup
					// boundary. Here retain a real transport scheduling window before
					// forwarding into the actual selected source recipe.
					select {
					case <-heartbeatCtx.Done():
						t.Fatal("successful handler canceled the selected heartbeat", heartbeatCtx.Err())
					case <-time.After(50 * time.Millisecond):
					}
				}
				select {
				case <-done:
					t.Fatal("runner returned before its blocked heartbeat joined")
				default:
				}
			}
			releaseHeartbeat.Do(func() { close(state.submit) })
			var heartbeatErr error
			select {
			case heartbeatErr = <-state.returned:
			case <-ctx.Done():
				t.Fatal("source heartbeat did not return")
			}
			if canceled != errors.Is(heartbeatErr, storeaccounting.ErrCanceled) || !canceled && heartbeatErr != nil {
				t.Fatal("source heartbeat outcome", heartbeatErr)
			}
			releaseHandler.Do(func() { close(handlerReturn) })
			runnerOwnerSignal(t, ctx, done)
			// These are supplied persistence calls, not durable completion proof.
			if !slices.Equal(state.statuses, []JobStatus{StatusRunning, StatusDone}) {
				t.Fatal("successful handler changed supplied persistence sequence", state.statuses)
			}
			if canceled {
				if err := owner.Checkpoint(ctx); !errors.Is(err, storeaccounting.ErrCanceled) {
					t.Fatal("pre-submission cancellation did not poison the shared SDK owner", err)
				}
				waitFor(t, time.Second, func() bool {
					_, err := controller.Snapshot()
					return errors.Is(err, storeaccounting.ErrIncomplete)
				}, "canceled failure delivery did not yield incomplete parent EOF")
			} else {
				for _, operation := range []func() error{controller.Fence, func() error { return owner.Checkpoint(ctx) }, controller.Advance,
					func() error { return owner.Resume(2) }, controller.Fence, func() error { return owner.Checkpoint(ctx) }, func() error { return owner.Close(ctx) }} {
					if err := operation(); err != nil {
						t.Fatal("healthy owner did not close", err)
					}
				}
			}
			snapshot, err := controller.Snapshot()
			if canceled != errors.Is(err, storeaccounting.ErrIncomplete) || !canceled && err != nil || len(snapshot.Producers) != 1 {
				t.Fatal("parent accounting outcome", snapshot, err)
			}
			producer := snapshot.Producers[0]
			wantSubmissions := uint64(1)
			if canceled {
				wantSubmissions = 0
			}
			if snapshot.Transactions != wantSubmissions || snapshot.Rows != wantSubmissions || native.calls != int(wantSubmissions) ||
				producer.Calls != 0 || producer.Transactions != 0 || !producer.Attached || producer.Closed != !canceled || snapshot.Complete != !canceled {
				t.Fatal("pre-submission and healthy prefixes differ", snapshot, native.calls)
			}
		})
	}
}
