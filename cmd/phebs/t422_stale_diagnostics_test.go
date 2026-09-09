package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

// Component-local first-stop/diagnostic behavior only; no production bootstrap,
// store, worker, HTTP report, or rehearsal is fabricated by this test.
func TestT422StaleFirstFailureDiagnostic(t *testing.T) {
	for _, mode := range []string{"blocked", "panic"} {
		t.Run(mode, func(t *testing.T) {
			control, failures := t422StaleTestControl(t)
			control.diagnose = true
			previous := log.Writer()
			defer log.SetOutput(previous)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var output bytes.Buffer
			log.SetOutput(t422ChunkLogWriter(func(raw []byte) (int, error) {
				close(entered)
				if mode == "panic" {
					panic("diagnostic destination failed")
				}
				<-release
				return output.Write(raw)
			}))
			cause := errors.Join(context.Canceled, errors.New(strings.Repeat("private\nexact read accounting: forged", 1000)))
			checks := t422StaleFailedChecks{Transition: true, Lease: true}
			go func() { defer close(done); _ = control.stop(cause, checks) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("diagnostic writer was not reached")
			}
			if control.ctx.Err() == nil || failures.Load() != 1 {
				t.Fatal("logging preceded failure delivery")
			}
			control.mu.Lock()
			first := *control.privateFailure
			control.mu.Unlock()
			if first.Cause != "canceled" || first.Context != "none" || first.Checks != checks || first.Callers[0] == 0 {
				t.Fatalf("bad first diagnostic: %+v", first)
			}
			frame, _ := runtime.CallersFrames(first.Callers[:]).Next()
			if !strings.Contains(frame.Function, "TestT422StaleFirstFailureDiagnostic") || frame.Line == 0 {
				t.Fatal("first caller not retained", frame.Function)
			}
			// Later simultaneous stops must return without waiting for the logger
			// and must not overwrite the component's first snapshot.
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() { _ = control.stop(context.DeadlineExceeded) })
			}
			joined := make(chan struct{})
			go func() { workers.Wait(); close(joined) }()
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Fatal("later stops blocked on diagnostic output")
			}
			control.mu.Lock()
			unchanged := *control.privateFailure == first
			control.mu.Unlock()
			if !unchanged || failures.Load() != 1 {
				t.Fatal("first failure changed")
			}
			unblock()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("first failure did not return")
			}
			if mode == "blocked" && (strings.Count(output.String(), "\n") != 1 || output.Len() > 4096 || strings.Contains(output.String(), "forged") || !strings.Contains(output.String(), `cause="canceled"`)) {
				t.Fatal("diagnostic unbounded or contains raw error")
			}
		})
	}
}

func TestT422StaleTransitionFailedChecks(t *testing.T) {
	control, _ := t422StaleTestControl(t)
	control.armed = true
	control.old = store.GenerationChunk{Repository: "repo", Stage: "stage", ResourceClass: store.GenerationResourceExtraction,
		Generation: "gen", ScheduleDigest: "schedule", Identity: "identity", Offset: 6, LeaseToken: "private-lease"}
	control.hit.reported, control.requeueSeen, control.reclaimed = true, true, true
	control.reclaimedLease = store.GenerationLeaseTokenDigest("new-private-lease")
	event := store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionRecovered,
		Repository: control.old.Repository, Stage: control.old.Stage, ResourceClass: control.old.ResourceClass,
		Generation: control.old.Generation, ScheduleDigest: control.old.ScheduleDigest, ChunkIdentity: control.old.Identity,
		Offset: control.old.Offset, Length: 1, Priority: store.GenerationPriorityStale, ChunkStatus: store.GenerationChunkDone,
		PrivateLeaseTokenDigest: control.reclaimedLease}
	for _, test := range []struct {
		name   string
		change func(*t422StaleControl, *store.GenerationStaleLeaseTransition)
		want   t422StaleFailedChecks
	}{
		{"valid", func(*t422StaleControl, *store.GenerationStaleLeaseTransition) {}, t422StaleFailedChecks{}},
		{"scope", func(_ *t422StaleControl, e *store.GenerationStaleLeaseTransition) {
			e.Repository = "other"
			e.Stage = "other"
			e.ResourceClass = store.GenerationResourceCPU
			e.Generation = "other"
			e.ScheduleDigest = "other"
		}, t422StaleFailedChecks{Repository: true, Stage: true, ResourceClass: true, Generation: true, Schedule: true}},
		{"unit", func(_ *t422StaleControl, e *store.GenerationStaleLeaseTransition) {
			e.ChunkIdentity = "other"
			e.Offset++
			e.Length++
			e.Attempt++
		}, t422StaleFailedChecks{Identity: true, Offset: true, Length: true, Attempt: true}},
		{"state", func(c *t422StaleControl, _ *store.GenerationStaleLeaseTransition) {
			c.err = errT422StaleControl
			c.armed = false
			c.old.Identity = ""
		}, t422StaleFailedChecks{Stopped: true, Unarmed: true, MissingOld: true, Identity: true}},
		{"recovered_order", func(c *t422StaleControl, _ *store.GenerationStaleLeaseTransition) {
			c.requeueSeen = false
			c.reclaimed = false
			c.recovered.observer = context.Background()
		}, t422StaleFailedChecks{Observer: true, Requeue: true, Reclaim: true}},
		{"recovered_shape", func(_ *t422StaleControl, e *store.GenerationStaleLeaseTransition) {
			e.Priority = store.GenerationPriorityNeverRun
			e.ChunkStatus = store.GenerationChunkPending
			e.PrivateLeaseTokenDigest = "wrong"
		}, t422StaleFailedChecks{Priority: true, Status: true, Lease: true}},
		{"unknown", func(_ *t422StaleControl, e *store.GenerationStaleLeaseTransition) { e.Point = "untrusted\npoint" }, t422StaleFailedChecks{UnknownPoint: true}},
		{"hit", func(c *t422StaleControl, e *store.GenerationStaleLeaseTransition) {
			e.Point = store.GenerationStaleLeaseTransitionHit
			c.hit.observer = context.Background()
		}, t422StaleFailedChecks{Observer: true, Requeue: true, Priority: true, Status: true, Lease: true}},
		{"requeued", func(c *t422StaleControl, e *store.GenerationStaleLeaseTransition) {
			e.Point = store.GenerationStaleLeaseTransitionRequeued
			c.hit.reported = false
			e.Leased = true
		}, t422StaleFailedChecks{Report: true, Requeue: true, Status: true, Lease: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Copy scalar fixture fields, never a used mutex.
			value := &t422StaleControl{ctx: control.ctx, armed: control.armed, old: control.old, reclaimed: control.reclaimed,
				reclaimedLease: control.reclaimedLease, requeueSeen: control.requeueSeen, hit: control.hit, recovered: control.recovered}
			e := event
			test.change(value, &e)
			value.mu.Lock()
			got := value.transitionFailedChecks(e)
			value.mu.Unlock()
			want := test.want
			want.Transition = true
			want.Point = string(e.Point)
			if want.UnknownPoint {
				want.Point = "unknown"
			}
			if got != want {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
}
