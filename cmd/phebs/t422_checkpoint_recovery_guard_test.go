//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

// Runs only inside the real inherited DA/PC helper. Event/store data remain
// supplied fixtures. No actual native checkpoint or database is manufactured.
func testT422CheckpointRecoveryDiagnosticGuards(t *testing.T, launch *t422SemanticLaunch) {
	t.Helper()
	for _, test := range []struct {
		name string
		want t422RecoveryFailureStage
	}{
		{"callback_binding", t422RecoveryTransitionBinding},
		{"callback_unbounded", t422RecoveryTransitionDeadline},
		{"callback_over_budget", t422RecoveryTransitionDeadline},
		{"foreign_target", t422RecoveryTransitionTarget},
		{"unknown_point", t422RecoveryTransitionState},
		{"duplicate_hit", t422RecoveryTransitionState},
		{"rendezvous_expired_hit", t422RecoveryRendezvous},
		{"release_wait_canceled", t422RecoveryReleaseWait},
		{"read_binding", t422RecoveryReadBinding},
		{"duplicate_read", t422RecoveryReadState},
		{"read_wait_canceled", t422RecoveryReadWait},
		{"runtime_read", t422RecoveryRuntimeRead},
		{"reader_binding", t422RecoveryReaderBinding},
		{"reader_expired_phase", t422RecoveryReaderWait},
		{"reader_state", t422RecoveryReaderState},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				selected := *launch
				var failures atomic.Int32
				selected.fail = func(error) { failures.Add(1) }
				reconciler := &extractionpublication.Reconciler{StoreAccounting: true, Runtime: &extractionpublication.Runtime{Fence: &extractionpublication.AuthorityFence{}}}
				control, err := newT422CheckpointRecoveryControl(ctx, &selected, reconciler)
				if err != nil {
					t.Fatal(err)
				}
				defer control.cancel()
				hit := control.input.Hit
				event := store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionHit,
					Repository: selected.request.Repository, Stage: extractionpublication.ScheduleStage, ResourceClass: store.GenerationResourceExtraction,
					Generation: hit.ScheduleGeneration, ScheduleDigest: hit.ScheduleDigest, ChunkIdentity: hit.ChunkIdentity,
					Offset: int64(control.input.Offset), Length: 1, Priority: store.GenerationPriorityNeverRun,
					ScheduleStatus: store.GenerationScheduleActive, ChunkStatus: store.GenerationChunkRunning, Leased: true,
					StaleBefore: time.Now().Add(-time.Second), ChunkStateDigest: t422CheckpointTestDigest, CheckpointStateDigest: t422CheckpointTestDigest,
					PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest("killed-fixture")}
				callback, cancelCallback := context.WithTimeout(ctx, 5*time.Second)
				defer cancelCallback()
				snapshot, err := dispatchadmission.ProductionSemanticState()
				if err != nil {
					t.Fatal(err)
				}
				request := context.WithValue(ctx, t422SemanticRequestKey{}, snapshot)
				var refused error
				switch test.name {
				case "callback_binding":
					cancelCallback()
					refused = control.transition(callback, event)
				case "callback_unbounded":
					refused = control.transition(context.WithoutCancel(callback), event)
				case "callback_over_budget":
					wide, cancelWide := context.WithTimeout(ctx, time.Minute)
					defer cancelWide()
					refused = control.transition(wide, event)
				case "foreign_target":
					event.Offset++
					refused = control.transition(callback, event)
				case "unknown_point":
					event.Point = "untrusted-point-not-for-logs"
					refused = control.transition(callback, event)
				case "duplicate_hit":
					if err := control.transition(callback, event); err != nil {
						t.Fatal(err)
					}
					refused = control.transition(callback, event)
				case "rendezvous_expired_hit", "release_wait_canceled":
					control.killed, control.hit.observer = event, callback
					event.Point, event.Priority, event.ChunkStatus, event.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
					event.StaleBefore = time.Time{}
					event.PrivateLeaseTokenDigest = store.GenerationLeaseTokenDigest("new-fixture")
					if test.name == "rendezvous_expired_hit" {
						old, cancelOld := context.WithCancel(ctx)
						cancelOld()
						control.hit.observer = old
						refused = control.transition(callback, event)
					} else {
						control.requeueSeen = true
						done := make(chan error, 1)
						go func() { done <- control.transition(callback, event) }()
						synctest.Wait() // The actual callback is blocked on report release.
						cancelCallback()
						refused = <-done
					}
				case "read_binding":
					_, _, refused = control.read(ctx)
				case "duplicate_read":
					control.recovered.reading = true
					_, _, refused = control.read(request)
				case "read_wait_canceled":
					waiting, cancelWaiting := context.WithCancel(request)
					done := make(chan error, 1)
					go func() { _, _, err := control.read(waiting); done <- err }()
					synctest.Wait()
					cancelWaiting()
					refused = <-done
				case "runtime_read":
					event.Point, event.Priority, event.ChunkStatus, event.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
					event.ScheduleStatus, event.StaleBefore = "", time.Time{}
					event.ChunkStateDigest, event.CheckpointStateDigest = "", ""
					event.PrivateLeaseTokenDigest = store.GenerationLeaseTokenDigest("new-fixture")
					control.recovered.observer, control.recovered.transition = callback, event
					close(control.recovered.ready)
					accounted, ledger, err := readaccounting.Start(request, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
					if err != nil {
						t.Fatal(err)
					}
					body, afterReport, err := control.read(accounted)
					counts, accountingErr := ledger.Finish()
					if !errors.Is(err, extractionpublication.ErrInvalid) || len(body) != 0 || afterReport != nil || counts != (readaccounting.Counts{}) || accountingErr != nil {
						t.Fatal("runtime refusal manufactured read/report credit", err, counts)
					}
					refused = err
				case "reader_binding":
					cancel()
					refused = control.waitForReader(ctx)
				case "reader_expired_phase":
					control.phaseEnd = time.Now().Add(-time.Second)
					refused = control.waitForReader(ctx)
				case "reader_state":
					control.readerReady <- nil
					refused = control.waitForReader(ctx)
				}
				if !errors.Is(refused, errT422StaleControl) || failures.Load() != 1 || control.failure == nil || control.failure.Stage != test.want || control.recovered.reported || control.finalSeen {
					t.Fatal("refusal/diagnostic differs", refused, control.failure)
				}
				if test.name == "foreign_target" && !control.failure.Event.Offset || test.name == "runtime_read" && control.failure.Cause != "runtime_invalid" || test.name == "rendezvous_expired_hit" && control.failure.Hit != "canceled" {
					t.Fatal("specific failure evidence missing", control.failure)
				}
				select {
				case <-control.recovered.release:
					t.Fatal("refused control released a report")
				default:
				}
				first := *control.failure
				_ = control.refuse(ctx, t422RecoveryUnknown, errors.New("later-refusal"), nil)
				if *control.failure != first || failures.Load() != 1 {
					t.Fatal("later failure overwrote first cause")
				}
			})
		})
	}
}
