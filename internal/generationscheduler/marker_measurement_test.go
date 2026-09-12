package generationscheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

// Real scheduler/owner/heartbeat state machines, supplied store outcomes and
// fake time. This is not a native engine, durable reaper or publication proof.
func TestMarkerMeasurementSchedulerContinuation(t *testing.T) {
	for _, mode := range []string{"success", "sample_failure", "refresh_failure", "ready_failure", "canceled", "replay", "wrong_claim", "handler_failure"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 3, Requests: 1})
				if err != nil {
					t.Fatal(err)
				}
				turn, _ := owners.Enter(ctx)
				request, _ := owners.EnterRequest(ctx)
				chunk := store.GenerationChunk{Identity: "chunk", Repository: "repository", Stage: "relationship-v3", Generation: "generation",
					ScheduleDigest: "schedule", Status: store.GenerationChunkRunning, LeaseToken: "lease", ClaimedBy: "worker", Length: 1}
				state := &terminalTestStore{schedulerStore: &schedulerStore{}}
				var sampled, refreshed, ready atomic.Bool
				peerDone := make(chan struct{})
				state.beat = func(_ context.Context, actual store.GenerationChunk) error {
					if actual.Identity != chunk.Identity || actual.LeaseToken != chunk.LeaseToken || actual.ClaimedBy != chunk.ClaimedBy {
						t.Error("claim changed")
					}
					if sampled.Load() {
						if mode == "refresh_failure" {
							return store.ErrGenerationLeaseLost
						}
						refreshed.Store(true)
					}
					return nil
				}
				scheduler := &Scheduler{Store: state, Owners: owners, HeartbeatEvery: time.Second, StaleAfter: 2 * time.Second,
					StoreCallTimeout: time.Second, ChunkReports: func([]byte) error { return nil }, ChunkReportFailure: func(error) {}}
				entered, done := make(chan struct{}), make(chan struct{})
				var retained bool
				go func() {
					defer close(done)
					retained = scheduler.executeOwned(ctx, Class{MarkerMeasurement: func(context.Context, store.GenerationChunk) bool { return true },
						Handle: func(worker context.Context, actual store.GenerationChunk, _ Budget) error {
							capability := MarkerMeasurementFromContext(worker)
							if capability == nil || !capability.Matches(actual) {
								return errors.New("missing actual capability")
							}
							if mode == "wrong_claim" {
								wrong := actual
								wrong.LeaseToken = "different"
								if capability.Matches(wrong) {
									t.Error("accepted wrong claim")
								}
							}
							close(entered)
							sample := func(measureCtx context.Context, confirm func() bool) error {
								deadline, _ := measureCtx.Deadline()
								original, _ := ctx.Deadline()
								if deadline != original || !confirm() {
									return errors.New("deadline or retained proof")
								}
								before := state.beats.Load()
								go func() {
									defer close(peerDone)
									peer, err := owners.Enter(ctx)
									if err == nil {
										if !refreshed.Load() {
											t.Error("peer admitted before same-lease natural heartbeat")
										}
										peer.End()
									}
								}()
								time.Sleep(3 * time.Second) // Past modeled stale cutoff, with reapers fenced.
								if state.beats.Load() != before || !confirm() {
									return errors.New("heartbeat wrote during measurement")
								}
								sampled.Store(true)
								if mode == "canceled" {
									cancel()
									return ctx.Err()
								}
								if mode == "sample_failure" {
									return errors.New("sample refused")
								}
								return nil
							}
							report := func() error {
								if !refreshed.Load() {
									return errors.New("ready preceded same-claim heartbeat")
								}
								probe, err := owners.EnterRequest(ctx)
								if err != nil {
									return err
								}
								probe.End()
								if mode == "ready_failure" {
									return errors.New("ready sink lost")
								}
								ready.Store(true)
								return nil
							}
							err := capability.Measure(ctx, sample, report)
							if err == nil && mode == "replay" {
								err = capability.Measure(ctx, sample, report)
							}
							if err == nil && mode == "handler_failure" {
								return errors.New("later recovery failed")
							}
							return err
						}}, chunk, turn)
				}()
				<-entered
				synctest.Wait()
				if sampled.Load() {
					t.Fatal("sample ignored HIT request tail")
				}
				request.End()
				<-done
				wantSuccess := mode == "success" || mode == "wrong_claim"
				state.mu.Lock()
				completed, others := state.completed, state.released+state.retried+state.failed+state.deferred
				state.mu.Unlock()
				if retained == wantSuccess || completed != boolInt(wantSuccess) || others != 0 || !sampled.Load() || wantSuccess && !ready.Load() {
					t.Fatal("settlement/prefix mismatch", retained, completed, others, sampled.Load(), ready.Load())
				}
				if !retained {
					turn.End()
				}
				cancel()
				<-peerDone
			})
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestMarkerMeasurementJoinsAdmittedHeartbeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
		if err != nil {
			t.Fatal(err)
		}
		turn, _ := owners.Enter(ctx)
		chunk := store.GenerationChunk{Identity: "chunk", Repository: "repo", Stage: "stage", Generation: "gen", ScheduleDigest: "schedule",
			Status: store.GenerationChunkRunning, LeaseToken: "lease", ClaimedBy: "worker"}
		measurement, err := newMarkerMeasurement(ctx, turn, chunk)
		if err != nil {
			t.Fatal(err)
		}
		entered, release, measured, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		state := &terminalTestStore{schedulerStore: &schedulerStore{}}
		state.beat = func(call context.Context, _ store.GenerationChunk) error {
			if state.beats.Load() == 1 {
				close(entered)
				<-release
				if call.Err() != nil {
					t.Error("inflight heartbeat canceled locally")
				}
			}
			return nil
		}
		scheduler := &Scheduler{Store: state, HeartbeatEvery: time.Second, StoreCallTimeout: 10 * time.Second, StaleAfter: 20 * time.Second}
		go measurement.beat(scheduler, cancel)
		<-entered
		go func() {
			defer close(done)
			err = measurement.Measure(ctx, func(_ context.Context, confirm func() bool) error {
				if !confirm() {
					return errors.New("missing retained proof")
				}
				close(measured)
				return nil
			}, func() error { return nil })
		}()
		synctest.Wait()
		select {
		case <-measured:
			t.Fatal("walk preceded admitted heartbeat return")
		default:
		}
		close(release)
		<-done
		if err != nil {
			t.Fatal(err)
		}
		if measurement.finish(cancel) != nil {
			t.Fatal("finish")
		}
		turn.End()
	})
}

// Err captures a valid pre-cancel value, then triggers real cancellation.
// This makes the otherwise racy settlement boundary deterministic; it does
// not fabricate a canceled result while Done remains open.
type markerSettlementContext struct {
	context.Context
	cancel context.CancelFunc
	armed  atomic.Bool
}

func (ctx *markerSettlementContext) Err() error {
	err := ctx.Context.Err()
	if ctx.armed.CompareAndSwap(true, false) {
		ctx.cancel()
	}
	return err
}

func TestMarkerMeasurementCancellationAtReleaseBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		ctx := &markerSettlementContext{Context: base, cancel: cancel}
		owners, err := dispatchadmission.NewOwners(base, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
		if err != nil {
			t.Fatal(err)
		}
		turn, _ := owners.Enter(base)
		chunk := store.GenerationChunk{Identity: "chunk", Repository: "repo", Stage: "stage", Generation: "gen", ScheduleDigest: "schedule",
			Status: store.GenerationChunkRunning, LeaseToken: "lease", ClaimedBy: "worker"}
		state := &terminalTestStore{schedulerStore: &schedulerStore{}}
		var failures int
		scheduler := &Scheduler{Store: state, Owners: owners, HeartbeatEvery: time.Second, StoreCallTimeout: time.Second, StaleAfter: 20 * time.Second,
			ChunkReports: func([]byte) error { return nil }, ChunkReportFailure: func(error) { failures++ }}
		retained := scheduler.executeOwned(ctx, Class{MarkerMeasurement: func(context.Context, store.GenerationChunk) bool { return true },
			Handle: func(worker context.Context, _ store.GenerationChunk, _ Budget) error {
				capability := MarkerMeasurementFromContext(worker)
				if err := capability.Measure(base, func(_ context.Context, confirm func() bool) error {
					if !confirm() {
						return errors.New("retained proof unavailable")
					}
					return nil
				}, func() error { return nil }); err != nil {
					return err
				}
				ctx.armed.Store(true) // Next scheduler Err reads nil, then actual cancellation fires.
				return nil
			}}, chunk, turn)
		state.mu.Lock()
		mutations := state.completed + state.released + state.retried + state.failed + state.deferred
		state.mu.Unlock()
		if base.Err() == nil || !retained || failures != 1 || mutations != 0 {
			t.Fatal("settlement-boundary cancellation released the retained claim", retained, failures, mutations)
		}
	})
}
