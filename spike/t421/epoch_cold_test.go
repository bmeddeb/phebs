package t421

import (
	"context"
	"testing"
	"time"
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
	}
	if _, err := epochOneBounds(plan, 0); err == nil {
		t.Fatal("unknown launch mode admitted")
	}
}

func TestExecutionEpochOneColdRefusesWithoutLiveHealthyColdOwner(t *testing.T) {
	for _, flow := range []*ExecutionEpochOne{nil, {}} {
		if run, err := flow.StartCold(t.Context()); run != nil || err == nil {
			t.Fatal("missing cold ownership admitted")
		}
	}
	for _, run := range []*ExecutionEpochOneRun{nil, {}} {
		for _, ctx := range []context.Context{nil, t.Context()} {
			if run.ColdToWarm(ctx) == nil {
				t.Fatal("missing cold run admitted")
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if epochInspectionDelay(ctx) == nil {
		t.Fatal("canceled poll delay continued")
	}
}
