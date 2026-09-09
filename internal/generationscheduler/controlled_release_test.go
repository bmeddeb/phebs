package generationscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

type controlledReleaseStore struct {
	schedulerStore
	chunk  store.GenerationChunk
	reason string
	beat   func(context.Context) error
}

func (state *controlledReleaseStore) ReleaseGenerationChunk(_ context.Context, chunk store.GenerationChunk, reason string) error {
	state.released++
	state.chunk, state.reason = chunk, reason
	return state.releaseErr
}

func (state *controlledReleaseStore) HeartbeatGenerationChunk(ctx context.Context, _ store.GenerationChunk) error {
	if state.beat != nil {
		return state.beat(ctx)
	}
	return nil
}

func TestControlledReleaseUsesExistingSameAttemptSettlement(t *testing.T) {
	owned, foreign := errors.New("owned interruption"), errors.New("ordinary handler failure")
	for _, mode := range []string{"release", "release_error", "lost_lease", "foreign", "mixed", "invalid", "nil_hook", "nil_error", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			state := &controlledReleaseStore{}
			if mode == "release_error" {
				state.releaseErr = errors.New("native release failure")
			}
			if mode == "lost_lease" {
				state.releaseErr = store.ErrGenerationLeaseLost
			}
			failures, checks := 0, 0
			var reports []ChunkLifecycleReport
			scheduler := &Scheduler{Store: state, HeartbeatEvery: time.Hour, Backoff: func(int) time.Duration { return time.Second },
				ChunkReportFailure: func(error) { failures++ }, ChunkReports: func(raw []byte) error {
					var value ChunkLifecycleReport
					if err := json.Unmarshal(raw, &value); err != nil {
						return err
					}
					reports = append(reports, value)
					return nil
				}}
			cause := owned
			if mode == "foreign" {
				cause = foreign
			}
			if mode == "mixed" {
				cause = errors.Join(owned, foreign)
			}
			if mode == "nil_error" {
				cause = nil
			}
			chunk := store.GenerationChunk{Identity: "actual-target", Stage: store.ServiceStateV3ActivateStage,
				Generation: "actual-plan", ScheduleDigest: "actual-schedule", Offset: 9, Length: 1, Attempt: 0,
				Priority: store.GenerationPriorityNeverRun, Status: store.GenerationChunkRunning, LeaseToken: "actual-lease"}
			class := Class{Handle: func(context.Context, store.GenerationChunk, Budget) error {
				if mode == "canceled" {
					cancel()
				}
				return cause
			}, ControlledRelease: func(_ context.Context, got store.GenerationChunk, err error) (bool, error) {
				checks++
				if got != chunk {
					t.Fatal("classifier lost actual claimed chunk")
				}
				if mode == "invalid" || mode == "mixed" {
					return false, errors.New("owned release refused")
				}
				return err == owned, nil
			}}
			if mode == "nil_hook" {
				class.ControlledRelease = nil
			}
			scheduler.execute(ctx, class, chunk)
			wantRelease := mode == "release" || mode == "release_error" || mode == "lost_lease" || mode == "canceled"
			if state.released != map[bool]int{false: 0, true: 1}[wantRelease] || state.deferred != 0 || state.failed != 0 {
				t.Fatal("release used another settlement", state.released, state.retried, state.completed, state.deferred, state.failed)
			}
			if wantRelease && state.chunk != chunk {
				t.Fatal("same attempt/lease identity changed before native release")
			}
			if mode == "release" && state.reason != owned.Error() {
				t.Fatal("controlled reason changed")
			}
			wantRetry := mode == "foreign" || mode == "nil_hook"
			if state.retried != map[bool]int{false: 0, true: 1}[wantRetry] || state.completed != map[bool]int{false: 0, true: 1}[mode == "nil_error"] {
				t.Fatal("ordinary/controlled settlement changed")
			}
			wantFailure := mode == "invalid" || mode == "mixed" || mode == "release_error" || mode == "lost_lease"
			if failures != map[bool]int{false: 0, true: 1}[wantFailure] {
				t.Fatal("invalid/native failed release did not latch exactly once", failures)
			}
			if (mode == "nil_hook" || mode == "nil_error" || mode == "canceled") && checks != 0 {
				t.Fatal("classifier ran on ordinary success/cancellation")
			}
			if len(reports) != 2 {
				t.Fatal("lost existing lifecycle reports", reports)
			}
			if mode == "release" && reports[1].Outcome != "released" {
				t.Fatal("invented another release outcome", reports)
			}
		})
	}
}

func TestControlledReleaseWaitsForHeartbeatAndFailurePrecedesIt(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "joined", true: "heartbeat_failed"}[failed], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			entered, handlerReturned, heartbeatReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			state := &controlledReleaseStore{beat: func(context.Context) error {
				once.Do(func() { close(entered) })
				if failed {
					close(heartbeatReturned)
					return store.ErrGenerationLeaseLost
				}
				<-handlerReturned
				close(heartbeatReturned)
				return nil
			}}
			checks := 0
			scheduler := &Scheduler{Store: state, HeartbeatEvery: time.Millisecond, StaleAfter: time.Second}
			scheduler.execute(ctx, Class{Handle: func(handleCtx context.Context, _ store.GenerationChunk, _ Budget) error {
				<-entered
				if failed {
					<-handleCtx.Done()
				}
				close(handlerReturned)
				return errors.New("owned")
			}, ControlledRelease: func(context.Context, store.GenerationChunk, error) (bool, error) {
				checks++
				select {
				case <-heartbeatReturned:
				default:
					t.Fatal("classified before heartbeat returned")
				}
				return true, nil
			}}, store.GenerationChunk{})
			want := 1
			if failed {
				want = 0
			}
			if checks != want || state.released != want || state.retried+state.completed+state.deferred+state.failed != 0 {
				t.Fatal("heartbeat precedence lost", checks, state.released)
			}
		})
	}
}
