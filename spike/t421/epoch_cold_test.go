package t421

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestExecutionEpochOneColdBoundsPreserveStartupAndPlan(t *testing.T) {
	startup, err := epochOneBounds(Plan{}, epochOneStartup)
	if err != nil || startup.lifetime != 20*time.Minute || startup.health != 5*time.Minute ||
		startup.outputBytes != 1<<20 || startup.controlPairs != 3 || startup.cold != 0 {
		t.Fatalf("startup limits changed: %+v %v", startup, err)
	}
	plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
	cold, err := epochOneBounds(plan, epochOneCold)
	if err != nil || cold.lifetime != 260*time.Minute || cold.cold != 4*time.Hour || cold.health != 15*time.Minute ||
		cold.outputBytes != 64<<20 || cold.controlPairs != 8 {
		t.Fatalf("cold limits not plan-bound: %+v %v", cold, err)
	}
	warm, err := epochOneBounds(plan, epochOneColdWarm)
	if err != nil || warm.controlPairs != 12 {
		t.Fatalf("warm control allowance: %+v %v", warm, err)
	}
	warm.controlPairs = cold.controlPairs
	if warm != cold {
		t.Fatal("warm changed inherited non-control limits")
	}
	for _, change := range []func(*Plan){
		func(p *Plan) { p.Schema = PlanV2Schema },
		func(p *Plan) { p.PhaseDeadlines = nil },
		func(p *Plan) { p.PhaseDeadlines[1].DeadlineMS++ },
		func(p *Plan) { p.PhaseDeadlines[2].Phase = "cold" },
		func(p *Plan) { p.SafetyEnvelope.ServerHealthDeadlineMS++ },
	} {
		plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
		change(&plan)
		if _, err := epochOneBounds(plan, epochOneCold); err == nil {
			t.Fatal("changed phase/health contract admitted")
		}
		if _, err := epochOneBounds(plan, epochOneColdWarm); err == nil {
			t.Fatal("warm admitted changed phase/health contract")
		}
	}
	if _, err := epochOneBounds(plan, 0); err == nil {
		t.Fatal("unknown launch mode admitted")
	}
}

func TestExecutionEpochOnePhaseDeadline(t *testing.T) {
	for _, stage := range []string{"bootstrap", "health", "idle_without_inspection", "handoff_at_expiry", "warm", "stop_before_expiry"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				now := time.Now()
				run := &ExecutionEpochOneRun{stop: make(chan struct{}), cancelRun: cancel,
					coldDeadline: now.Add(time.Second), lifetimeDeadline: now.Add(3 * time.Second), warmLimit: time.Second}
				run.mu.Lock()
				run.setPhaseDeadlineLocked(run.coldDeadline)
				run.mu.Unlock()
				defer run.stopPhaseDeadline()
				switch stage {
				case "health":
					// An invalid address refuses TCP immediately, leaving the real
					// Health loop waiting under the shorter cold deadline.
					run.done = make(chan struct{})
					run.control = &dispatchadmission.PhaseControl{}
					run.epoch.Listen = "invalid"
					if run.Health(t.Context()) == nil || time.Since(now) != time.Second {
						t.Fatal("health did not stop at the cold deadline")
					}
				case "idle_without_inspection":
					run.healthy = true
				case "warm":
					time.Sleep(500 * time.Millisecond)
					if run.completeCold(ctx) != nil || !run.warm || !run.phaseDeadline.Equal(now.Add(1500*time.Millisecond)) {
						t.Fatal("successful handoff did not install phase-local warm deadline")
					}
					time.Sleep(600 * time.Millisecond)
					synctest.Wait()
					if ctx.Err() != nil {
						t.Fatal("retired cold timer stopped phase three")
					}
				case "stop_before_expiry":
					run.stopPhaseDeadline()
					time.Sleep(4 * time.Second)
					synctest.Wait()
					if ctx.Err() != nil || run.err != nil {
						t.Fatal("joined timer fired after clean stop")
					}
					return
				}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if ctx.Err() == nil || run.err != ErrExecutionEpochOne {
					t.Fatal("deadline did not independently latch and cancel native lifetime")
				}
				select {
				case <-run.stop:
				default:
					t.Fatal("deadline did not request existing stop/join")
				}
				if stage == "handoff_at_expiry" && (run.completeCold(t.Context()) == nil || run.warm) {
					t.Fatal("expired cold phase admitted handoff")
				}
			})
		})
	}
}

func TestExecutionEpochOneHandoffCannotOutliveTotalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		run := &ExecutionEpochOneRun{stop: make(chan struct{}), cancelRun: cancel,
			coldDeadline: time.Now().Add(time.Second), lifetimeDeadline: time.Now().Add(2 * time.Second), warmLimit: 20 * time.Minute}
		run.mu.Lock()
		run.setPhaseDeadlineLocked(run.coldDeadline)
		run.mu.Unlock()
		defer run.stopPhaseDeadline()
		if run.completeCold(ctx) != nil || run.phaseDeadline != run.lifetimeDeadline {
			t.Fatal("warm phase widened total lifetime")
		}
	})
}

func TestExecutionEpochOneColdRefusesWithoutLiveHealthyColdOwner(t *testing.T) {
	for _, flow := range []*ExecutionEpochOne{nil, {}} {
		if run, err := flow.StartCold(t.Context()); run != nil || err == nil {
			t.Fatal("missing cold ownership admitted")
		}
		if run, err := flow.StartColdWarm(t.Context()); run != nil || err == nil {
			t.Fatal("missing warm ownership admitted")
		}
	}
	for _, run := range []*ExecutionEpochOneRun{nil, {}} {
		for _, ctx := range []context.Context{nil, t.Context()} {
			if run.ColdToWarm(ctx) == nil {
				t.Fatal("missing cold run admitted")
			}
			if run.ObserveWarm(ctx) == nil {
				t.Fatal("missing warm run admitted")
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if epochInspectionDelay(ctx) == nil {
		t.Fatal("canceled poll delay continued")
	}
}

func TestExecutionEpochOneWarmAdmissionRefusal(t *testing.T) {
	for _, mode := range []string{"not_opted_in", "no_handoff", "cold_active", "already_used", "stopping", "stopped", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			done := make(chan struct{})
			if mode != "cold_active" {
				close(done)
			}
			run := &ExecutionEpochOneRun{control: &dispatchadmission.PhaseControl{}, stop: make(chan struct{}), done: make(chan struct{}),
				warmAllowed: true, warm: true, coldDone: done, inspection: &executionEpochInspection{}, phaseDeadline: time.Now().Add(time.Minute)}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "not_opted_in":
				run.warmAllowed = false
			case "no_handoff":
				run.warm = false
			case "already_used":
				run.warmUsed = true
			case "stopping":
				run.stopping = true
			case "stopped":
				close(run.stop)
			case "canceled":
				cancel()
			}
			if run.ObserveWarm(ctx) == nil || run.control.ReservedWireBytes() != 0 {
				t.Fatal("invalid warm admission reached phase control")
			}
			if mode == "canceled" {
				if run.err != ErrExecutionEpochOne || !run.warmUsed {
					t.Fatal("admitted cancellation lost sticky failure")
				}
				select {
				case <-run.warmDone:
				default:
					t.Fatal("canceled operation not joined")
				}
			}
		})
	}
}
