package generationscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

type terminalTestStore struct {
	*schedulerStore
	beat  func(context.Context, store.GenerationChunk) error
	beats atomic.Int32
}

func (state *terminalTestStore) HeartbeatGenerationChunk(ctx context.Context, chunk store.GenerationChunk) error {
	state.beats.Add(1)
	if state.beat != nil {
		return state.beat(ctx, chunk)
	}
	return nil
}

type terminalTestRun struct {
	owners     *dispatchadmission.Owners
	state      *terminalTestStore
	cap        *TerminalHeartbeat
	handlerCtx context.Context
	finish     chan struct{}
	done       chan struct{}
	cancel     context.CancelFunc
	mu         sync.Mutex
	reports    []ChunkLifecycleReport
	failures   int
}

func terminalTestStart(t *testing.T, enabled bool, beat func(context.Context, store.GenerationChunk) error) *terminalTestRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 3, Requests: 2})
	if err != nil {
		t.Fatal(err)
	}
	chunk := store.GenerationChunk{Identity: "native-chunk", Repository: "repo", Stage: "stage", Generation: "generation", ScheduleDigest: "schedule",
		ResourceClass: store.GenerationResourceExtraction, Offset: 6, Length: 1, Status: store.GenerationChunkRunning, LeaseToken: "private-native-lease"}
	run := &terminalTestRun{owners: owners, state: &terminalTestStore{schedulerStore: &schedulerStore{chunks: []store.GenerationChunk{chunk}}, beat: beat},
		finish: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	entered := make(chan struct{})
	scheduler := &Scheduler{Store: run.state, Owners: owners, PollEvery: time.Hour, HeartbeatEvery: time.Second, StaleAfter: 20 * time.Second, StoreCallTimeout: 5 * time.Second,
		ChunkReports: func(raw []byte) error {
			var report ChunkLifecycleReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return err
			}
			run.mu.Lock()
			run.reports = append(run.reports, report)
			run.mu.Unlock()
			return nil
		},
		ChunkReportFailure: func(error) { run.mu.Lock(); run.failures++; run.mu.Unlock() }}
	go func() {
		defer close(run.done)
		scheduler.work(ctx, store.GenerationResourceExtraction, Class{TerminalHeartbeat: enabled, Handle: func(ctx context.Context, _ store.GenerationChunk, _ Budget) error {
			run.cap, run.handlerCtx = TerminalHeartbeatFromContext(ctx), ctx
			close(entered)
			select {
			case <-run.finish:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, 0)
	}()
	<-entered
	t.Cleanup(func() {
		cancel()
		select {
		case <-run.finish:
		default:
			close(run.finish)
		}
		<-run.done
	})
	if enabled && run.cap == nil || !enabled && run.cap != nil {
		t.Fatal("capability selection changed")
	}
	return run
}

func (run *terminalTestRun) assertHeld(t *testing.T) {
	t.Helper()
	run.state.mu.Lock()
	mutations := run.state.completed + run.state.retried + run.state.failed + run.state.released + run.state.deferred
	run.state.mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	if mutations != 0 || len(run.reports) != 1 || run.reports[0].Event != "started" {
		t.Fatal("terminal hold manufactured settlement/report", mutations, run.reports)
	}
}

func TestTerminalHeartbeatDrainsOwnersBeforeStopping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := terminalTestStart(t, true, nil)
		other, _ := run.owners.Enter(t.Context())
		request, _ := run.owners.EnterRequest(t.Context())
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- run.cap.Quiesce(ctx) }()
		synctest.Wait()
		if _, err := run.owners.EnterRequest(ctx); !errors.Is(err, dispatchadmission.ErrFenced) {
			t.Fatal("request entry not fenced", err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if run.state.beats.Load() < 1 {
			t.Fatal("heartbeat stopped before other owners joined")
		}
		other.End()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatal("skipped request tail", err)
		default:
		}
		request.End()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		count := run.state.beats.Load()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if run.state.beats.Load() != count || run.handlerCtx.Err() != nil {
			t.Fatal("quiescence restarted heartbeat or canceled held handler")
		}
		identity, err := run.cap.ClaimIdentity()
		if err != nil || identity.ChunkIdentity != "native-chunk" || identity.Offset != 6 || identity.LeaseTokenDigest != store.GenerationLeaseTokenDigest("private-native-lease") {
			t.Fatal(identity, err)
		}
		encoded, _ := json.Marshal(identity)
		identity.Repository = "caller-mutated-copy"
		again, _ := run.cap.ClaimIdentity()
		if strings.Contains(string(encoded), "private-native-lease") || again.Repository != "repo" {
			t.Fatal("claim copy leaked raw lease or granted writable authority")
		}
		run.assertHeld(t)
		close(run.finish)
		<-run.done
		run.assertHeld(t)
		if run.owners.Err() != nil || run.failures != 1 {
			t.Fatal("held owner ended or failure missing", run.owners.Err(), run.failures)
		}
		if run.owners.Resume() == nil {
			t.Fatal("terminal owner became ordinary drainage")
		}
	})
}

func TestTerminalHeartbeatJoinsInflightWithoutCancel(t *testing.T) {
	for _, mode := range []string{"joined", "deadline", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan context.Context, 1), make(chan struct{})
				run := terminalTestStart(t, true, func(ctx context.Context, _ store.GenerationChunk) error {
					entered <- ctx
					select {
					case <-release:
						if mode == "uncertain" {
							return errors.New("native reply uncertain")
						}
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				beatCtx := <-entered
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- run.cap.Quiesce(ctx) }()
				synctest.Wait()
				select {
				case err := <-result:
					t.Fatal("in-flight beat consumed early", err)
				default:
				}
				if mode == "deadline" {
					if err := <-result; err == nil {
						t.Fatal("deadline became quiescent")
					}
					if beatCtx.Err() != nil || run.handlerCtx.Err() != nil {
						t.Fatal("quiesce deadline canceled native/handler context")
					}
					close(run.finish)
					synctest.Wait()
					select {
					case <-run.done:
						t.Fatal("cleanup skipped in-flight beat")
					default:
					}
					if beatCtx.Err() != nil {
						t.Fatal("cleanup canceled naturally in-flight terminal beat")
					}
					close(release)
				} else {
					close(release)
					if err := <-result; (err == nil) != (mode == "joined") {
						t.Fatal("uncertain reply reclassified healthy", err)
					}
					if run.handlerCtx.Err() != nil {
						t.Fatal("held handler canceled")
					}
					close(run.finish)
				}
				<-run.done
				run.assertHeld(t)
				if run.failures != 1 || run.state.beats.Load() != 1 {
					t.Fatal("terminal retry or missing failure", run.failures, run.state.beats.Load())
				}
			})
		})
	}
}

func TestTerminalHeartbeatPriorErrorAndInvalidAcquisition(t *testing.T) {
	for _, mode := range []string{"prior_error", "nil", "unbounded", "canceled", "replay", "copy"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var beat func(context.Context, store.GenerationChunk) error
				if mode == "prior_error" {
					beat = func(context.Context, store.GenerationChunk) error { return errors.New("uncertain old beat") }
				}
				run := terminalTestStart(t, true, beat)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				selected := ctx
				switch mode {
				case "prior_error":
					time.Sleep(time.Second)
					synctest.Wait()
				case "nil":
					selected = nil
				case "unbounded":
					selected = t.Context()
				case "canceled":
					cancel()
				case "replay":
					if run.cap.Quiesce(ctx) != nil {
						t.Fatal("initial quiescence")
					}
				case "copy":
					copied := *run.cap
					if copied.Quiesce(ctx) != nil {
						t.Fatal("initial copied capability quiescence")
					}
				}
				if run.cap.Quiesce(selected) == nil {
					t.Fatal("invalid terminal acquisition accepted")
				}
				close(run.finish)
				<-run.done
				run.assertHeld(t)
				if run.failures != 1 {
					t.Fatal("terminal failure was not sticky")
				}
			})
		})
	}
	var absent context.Context
	if (*TerminalHeartbeat)(nil).Quiesce(t.Context()) == nil || (&TerminalHeartbeat{}).Quiesce(t.Context()) == nil || TerminalHeartbeatFromContext(absent) != nil || TerminalHeartbeatFromContext(t.Context()) != nil {
		t.Fatal("forged/absent capability admitted")
	}
}

func TestTerminalHeartbeatCleanupJoinsConcurrentOwnerFence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := terminalTestStart(t, true, nil)
		other, _ := run.owners.Enter(t.Context())
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- run.cap.Quiesce(ctx) }()
		synctest.Wait()
		started := time.Now()
		close(run.finish)
		<-run.done
		if err := <-result; err == nil || time.Since(started) != 0 {
			t.Fatal("handler cleanup waited for the external fence deadline", err)
		}
		other.End()
		run.assertHeld(t)
		if run.failures != 1 || run.owners.Err() == nil {
			t.Fatal("failed terminal fence lost sticky owner failure")
		}
	})
}

func TestTerminalHeartbeatUnusedPreservesSettlement(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "selected_unused"}[enabled], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := terminalTestStart(t, enabled, nil)
				close(run.finish)
				synctest.Wait()
				run.cancel()
				<-run.done
				if run.state.completed != 1 || len(run.reports) != 2 || run.reports[0].Event != "started" || run.reports[1].Outcome != "completed" || run.failures != 0 || run.owners.Err() != nil {
					t.Fatal("unused opt-in changed settlement", run.reports, run.failures)
				}
				if enabled {
					if _, err := run.cap.ClaimIdentity(); err == nil || run.cap.Quiesce(t.Context()) == nil {
						t.Fatal("ended handler retained usable capability")
					}
				}
			})
		})
	}
}

func TestTerminalHeartbeatConfigurationAndClaimRefusal(t *testing.T) {
	for _, mode := range []string{"valid", "owners", "sink", "failure"} {
		t.Run(mode, func(t *testing.T) {
			owners, _ := dispatchadmission.NewOwners(t.Context(), dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
			scheduler := &Scheduler{Store: &schedulerStore{}, Owners: owners, ChunkReports: func([]byte) error { return nil }, ChunkReportFailure: func(error) {}, Classes: map[store.GenerationResourceClass]Class{store.GenerationResourceExtraction: {Concurrency: 1, Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 1}, Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil }, TerminalHeartbeat: true}}}
			switch mode {
			case "owners":
				scheduler.Owners = nil
			case "sink":
				scheduler.ChunkReports = nil
			case "failure":
				scheduler.ChunkReportFailure = nil
			}
			if _, err := scheduler.validate(); (err == nil) != (mode == "valid") {
				t.Fatal("invalid selected constructor", err)
			}
		})
	}
	if value, err := newTerminalHeartbeat(t.Context(), dispatchadmission.OwnerTurn{}, store.GenerationChunk{}); value != nil || err == nil {
		t.Fatal("caller-authored owner/claim accepted")
	}
}

func TestTerminalHeartbeatUnusedKeepsHeartbeatPrecedence(t *testing.T) {
	for _, definitive := range []bool{false, true} {
		t.Run(map[bool]string{false: "transient", true: "lease_lost"}[definitive], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := terminalTestStart(t, true, func(context.Context, store.GenerationChunk) error {
					if definitive {
						return store.ErrGenerationLeaseLost
					}
					return errors.New("transient native error")
				})
				time.Sleep(time.Second)
				synctest.Wait()
				if !definitive {
					if run.handlerCtx.Err() != nil {
						t.Fatal("unused selected handler lost ordinary transient tolerance")
					}
					close(run.finish)
					synctest.Wait()
				}
				run.cancel()
				<-run.done
				wantOutcome, wantCompleted := "completed", 1
				if definitive {
					wantOutcome, wantCompleted = "stale_fenced", 0
				}
				if run.state.completed != wantCompleted || run.state.released != 0 || len(run.reports) != 2 || run.reports[1].Outcome != wantOutcome || run.failures != 0 || run.owners.Err() != nil {
					t.Fatal("unused selected heartbeat changed ordinary precedence", run.reports, run.state.completed)
				}
			})
		})
	}
}
