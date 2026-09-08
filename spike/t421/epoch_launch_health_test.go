package t421

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// This is HTTP/control mechanics, not native Phebs or profile admission.
func TestExecutionEpochOneHealthOneShotAndFailure(t *testing.T) {
	for _, mode := range []string{"healthy", "unauthorized", "oversized", "truncated", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			parent, child, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := child.Close(); err != nil {
					t.Error(err)
				}
			}()
			control, err := dispatchadmission.NewPhaseControl(t.Context(), parent, [32]byte{1}, dispatchadmission.PhaseControlConfig{
				OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2, MaximumPhases: 3,
				MaximumWireBytes: 384, Timeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := control.Close(); err != nil {
					t.Error(err)
				}
			}()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/health" ||
					r.Header.Get("Authorization") != "Bearer private-key" ||
					r.Header.Get(dispatchadmission.ProductionRequestHeader) != control.RequestToken() {
					t.Error("health request lost fixed route, authentication or private owner token")
				}
				switch mode {
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
				case "oversized":
					_, _ = fmt.Fprint(w, strings.Repeat("x", 4097))
				case "truncated":
					w.Header().Set("Content-Length", "100")
					_, _ = fmt.Fprint(w, "short")
				default:
					_, _ = fmt.Fprint(w, `{"status":"ok"}`)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			run := &ExecutionEpochOneRun{control: control, stop: make(chan struct{}), done: make(chan struct{}),
				healthLimit: 3 * time.Second, cancelRun: cancel,
				epoch: ExecutionEpochConfig{Listen: strings.TrimPrefix(server.URL, "http://"), APIKey: "private-key"}}
			run.setHealthDeadlineLocked(ctx, time.Now())
			defer run.stopHealthDeadline()
			if mode == "canceled" {
				cancel()
			}
			err = run.Health(ctx)
			if (err == nil) != (mode == "healthy") {
				t.Fatalf("health result = %v", err)
			}
			if mode == "healthy" {
				if !run.healthy || run.healthTimer != nil || run.healthTimerDone != nil {
					t.Fatal("healthy response did not retire the launch deadline")
				}
				select {
				case <-run.stop:
					t.Fatal("healthy request stopped the native lifetime")
				default:
				}
			} else {
				select {
				case <-run.stop:
				default:
					t.Fatal("uncertain health did not stop the lifetime")
				}
				if run.err != ErrExecutionEpochOne {
					t.Fatal("health failure was not sticky")
				}
			}
			if run.Health(ctx) == nil {
				t.Fatal("one-shot health admitted a retry")
			}
			want := int32(1)
			if mode == "canceled" {
				want = 0
			}
			if requests.Load() != want {
				t.Fatalf("HTTP requests = %d, want %d", requests.Load(), want)
			}
		})
	}
}

func TestExecutionEpochOneHealthLaunchDeadline(t *testing.T) {
	for _, stage := range []string{"delayed_health", "bootstrap_delay", "no_health", "phase_deadline", "launch_context_deadline", "health_context_deadline", "ready", "ready_at_expiry", "stop_before_expiry"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				started := time.Now()
				run := &ExecutionEpochOneRun{stop: make(chan struct{}), done: make(chan struct{}), cancelRun: cancel,
					healthLimit: 2 * time.Second, control: &dispatchadmission.PhaseControl{},
					epoch: ExecutionEpochConfig{Listen: "invalid"}}
				launchCtx := ctx
				want := started.Add(2 * time.Second)
				switch stage {
				case "bootstrap_delay":
					// Model time spent inside admitted Start before the shared
					// launch path arms the timer. The original anchor survives.
					time.Sleep(time.Second)
				case "phase_deadline":
					run.coldDeadline = started.Add(time.Second)
					want = run.coldDeadline
				case "launch_context_deadline":
					var launchCancel context.CancelFunc
					launchCtx, launchCancel = context.WithDeadline(ctx, started.Add(time.Second))
					defer launchCancel()
					want = started.Add(time.Second)
				}
				run.setHealthDeadlineLocked(launchCtx, started)
				defer run.stopHealthDeadline()
				if run.healthDeadline != want {
					t.Fatalf("health deadline = %v, want launch-bound %v", run.healthDeadline, want)
				}
				switch stage {
				case "ready", "stop_before_expiry":
					time.Sleep(time.Second)
					if stage == "ready" {
						if run.completeHealth(ctx) != nil || !run.healthy {
							t.Fatal("live readiness did not retire its timer")
						}
					} else {
						run.stopHealthDeadline()
					}
					time.Sleep(2 * time.Second)
					synctest.Wait()
					if ctx.Err() != nil || run.err != nil || run.healthTimer != nil || run.healthTimerDone != nil {
						t.Fatal("retired health deadline stopped the later lifetime")
					}
					select {
					case <-run.stop:
						t.Fatal("retired health timer requested stop")
					default:
					}
					return
				case "no_health", "ready_at_expiry":
					time.Sleep(2 * time.Second)
					synctest.Wait()
					if run.healthUsed || ctx.Err() == nil {
						t.Fatal("absent Health did not independently cancel the lifetime")
					}
					if stage == "ready_at_expiry" && (run.completeHealth(t.Context()) == nil || run.healthy) {
						t.Fatal("expired health timer admitted readiness")
					}
				default:
					healthCtx := t.Context()
					switch stage {
					case "delayed_health":
						time.Sleep(time.Second)
					case "health_context_deadline":
						var healthCancel context.CancelFunc
						want = started.Add(time.Second)
						healthCtx, healthCancel = context.WithDeadline(healthCtx, want)
						defer healthCancel()
					}
					// Exercise Health's actual TCP wait/context path, not a
					// replacement timeout helper or a native-ready assertion.
					if run.Health(healthCtx) == nil || time.Now() != want {
						t.Fatal("Health renewed or widened its original deadline")
					}
					synctest.Wait()
				}
				if run.err != ErrExecutionEpochOne || run.healthy {
					t.Fatal("expired health readiness was not a sticky refusal")
				}
				select {
				case <-run.stop:
				default:
					t.Fatal("health deadline did not request existing stop/join")
				}
			})
		})
	}

	// A fabricated run without an actual launch deadline is not a compatible
	// replacement for a constructed epoch and must not invent a new allowance.
	run := &ExecutionEpochOneRun{stop: make(chan struct{}), done: make(chan struct{}), control: &dispatchadmission.PhaseControl{}}
	if run.Health(t.Context()) == nil || run.healthUsed || !run.healthDeadline.IsZero() {
		t.Fatal("unlaunched run manufactured a health deadline")
	}
}
