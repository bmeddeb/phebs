package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestT422CheckpointRecoveryFirstRefusal(t *testing.T) {
	for _, mode := range []string{"blocked", "panic"} {
		t.Run(mode, func(t *testing.T) {
			base, failures := t422StaleTestControl(t)
			control := &t422CheckpointRecoveryControl{t422StaleControl: base, input: t422CheckpointRecoveryFixture()}
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
			private := strings.Repeat("private\nexact read accounting: forged", 1000)
			event := store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionPoint(private), Repository: private, PrivateLeaseTokenDigest: private}
			cause := errors.Join(context.Canceled, errors.New(private))
			go func() { defer close(done); _ = control.refuse(t.Context(), t422RecoveryTransitionState, cause, &event) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("diagnostic writer was not reached")
			}
			if control.ctx.Err() == nil || failures.Load() != 1 {
				t.Fatal("logging preceded failure propagation")
			}
			control.mu.Lock()
			first := *control.failure
			control.mu.Unlock()
			if first.Stage != t422RecoveryTransitionState || first.Cause != "canceled" || first.Caller != "live" || first.Lifetime != "live" ||
				first.Hit != "absent" || first.Recovered != "absent" || first.Event.Point != "unknown" || !first.Event.Present || first.Reported {
				t.Fatal("bad first snapshot", first)
			}
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() { _ = control.refuse(control.ctx, t422RecoveryReadWait, context.DeadlineExceeded, nil) })
			}
			joined := make(chan struct{})
			go func() { workers.Wait(); close(joined) }()
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Fatal("later refusals waited for diagnostic output")
			}
			control.mu.Lock()
			unchanged := *control.failure == first
			control.mu.Unlock()
			if !unchanged || failures.Load() != 1 || control.recovered.reported {
				t.Fatal("first refusal changed or supplied report credit")
			}
			unblock()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("first refusal did not return")
			}
			if mode == "blocked" && (strings.Count(output.String(), "\n") != 1 || output.Len() > 2048 || strings.Contains(output.String(), "forged") || !strings.Contains(output.String(), "stage=transition_state cause=canceled")) {
				t.Fatal("diagnostic unbounded or contains private input")
			}
		})
	}
}

func TestT422CheckpointRecoveryFailureClasses(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, "none"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline"},
		{extractionpublication.ErrInvalid, "runtime_invalid"}, {extractionpublication.ErrStale, "runtime_stale"},
		{store.ErrGenerationLeaseLost, "store_lease"}, {store.ErrGenerationStale, "store_stale"},
		{readaccounting.ErrLimit, "read_accounting"}, {errT422StaleControl, "stale_control"}, {errors.New("private"), "other"},
	} {
		t.Run(test.want, func(t *testing.T) {
			if got := t422RecoveryCause(test.err); got != test.want {
				t.Fatal(got)
			}
			if test.err != nil && test.want != "other" && t422RecoveryCause(errors.Join(test.err, errT422StaleControl)) != test.want {
				t.Fatal("joined refusal hid the original class")
			}
		})
	}
	for _, test := range []struct {
		name, state, deadline string
		duration              time.Duration
		cancel                bool
	}{
		{"absent", "absent", "absent", 0, false}, {"unbounded", "live", "absent", 0, false},
		{"live", "live", "within_5s", 4 * time.Second, false}, {"over", "live", "over_5s", time.Hour, false},
		{"expired", "deadline", "expired", -time.Second, false}, {"canceled", "canceled", "within_5s", 4 * time.Second, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ctx context.Context
			if test.name != "absent" {
				ctx = context.Background()
				if test.duration != 0 {
					bounded, cancel := context.WithTimeout(ctx, test.duration)
					defer cancel()
					ctx = bounded
					if test.cancel {
						cancel()
					}
				}
			}
			if t422RecoveryContext(ctx) != test.state || t422RecoveryDeadline(ctx) != test.deadline {
				t.Fatal("context/deadline classification differs")
			}
		})
	}
	if t422RecoveryFailureStage(255).String() != "unknown" {
		t.Fatal("unknown stage escaped closed labels")
	}
}

// Differential controls pin the copied release predicate to its unchanged
// shared implementation. Diagnostics must not add or remove acceptance.
func TestT422CheckpointRecoveryReportReleaseParity(t *testing.T) {
	for _, mode := range []string{"complete", "not_reading", "already_reported", "missing_observer", "canceled_observer", "canceled_lifetime", "foreign_barrier"} {
		t.Run(mode, func(t *testing.T) {
			var accepted [2]bool
			for i := range 2 {
				base, failures := t422StaleTestControl(t)
				control := &t422CheckpointRecoveryControl{t422StaleControl: base}
				observer, cancel := context.WithCancel(t.Context())
				defer cancel()
				barrier := &base.recovered
				barrier.reading, barrier.observer = true, observer
				switch mode {
				case "not_reading":
					barrier.reading = false
				case "already_reported":
					barrier.reported = true
				case "missing_observer":
					barrier.observer = nil
				case "canceled_observer":
					cancel()
				case "canceled_lifetime":
					base.cancel()
				case "foreign_barrier":
					barrier = &t422StaleBarrier{}
				}
				var err error
				if i == 0 {
					err = base.finishReport(barrier)
				} else {
					err = control.finishRecoveryReport(t.Context(), barrier)
					if (control.failure == nil) != (mode == "complete") || control.failure != nil && control.failure.Stage != t422RecoveryReportRelease {
						t.Fatal("release diagnostic missing or fabricated")
					}
				}
				accepted[i] = err == nil
				select {
				case <-base.recovered.release:
					if mode != "complete" {
						t.Fatal("refusal released the observer")
					}
				default:
					if mode == "complete" {
						t.Fatal("valid report did not release observer")
					}
				}
				if (failures.Load() == 0) != (mode == "complete") {
					t.Fatal("failure propagation changed")
				}
			}
			if accepted[0] != accepted[1] || accepted[1] != (mode == "complete") {
				t.Fatal("selected release changed shared acceptance")
			}
		})
	}
}
