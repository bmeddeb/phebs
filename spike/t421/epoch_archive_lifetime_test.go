package t421

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestEpochArchiveExecutionBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bounds   func(Plan) (epochOneLimits, error)
		lifetime time.Duration
		pairs    uint64
		phases   []int
	}{
		{"backup", checkpointBackupEpochBounds, 9 * time.Hour, 28, []int{7, 8, 9, 10, 11}},
		{"restored", restoredExecutionBounds, 8*time.Hour + 20*time.Minute, 15, []int{11, 12, 13}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
			bounds, err := tc.bounds(plan)
			if err != nil || bounds.lifetime != tc.lifetime || bounds.controlPairs != tc.pairs || bounds.health != 15*time.Minute || bounds.outputBytes != 64<<20 {
				t.Fatal(bounds, err)
			}
			for _, i := range tc.phases {
				plan.PhaseDeadlines[i].DeadlineMS++
				if _, err := tc.bounds(plan); err == nil {
					t.Fatal("changed phase deadline admitted", i)
				}
				plan.PhaseDeadlines[i].DeadlineMS--
			}
			for _, schema := range []string{PlanSchema, PlanV2Schema, ""} {
				plan.Schema = schema
				if _, err := tc.bounds(plan); err == nil {
					t.Fatal("historical schema admitted", schema)
				}
			}
			plan.Schema, plan.PhaseDeadlines = PlanV3Schema, nil
			if _, err := tc.bounds(plan); err == nil {
				t.Fatal("missing phase deadlines admitted")
			}
		})
	}
	// Actual restored PC schedule: Drain/Open/Fence; two handoffs each with
	// Pause/Checkpoint/Resume/Open/Fence; terminal Pause and receiver EOF.
	pairs := uint64(3 + 5 + 5 + 2)
	if pairs*2*dispatchadmission.FrameBytes != 1920 {
		t.Fatal("restored control recipe changed")
	}
}

func TestEpochArchiveLifetimeDeadline(t *testing.T) {
	now := time.Now()
	for _, mode := range []string{"phase", "global", "caller", "expired_global", "expired_phase", "canceled", "nil", "missing_author", "future_author", "changed_wall", "v2"} {
		t.Run(mode, func(t *testing.T) {
			plan := Plan{Schema: PlanV3Schema, SafetyEnvelope: frozenSafetyEnvelope()}
			started, deadline := now.Add(-time.Hour), now.Add(8*time.Hour)
			ctx := t.Context()
			want := deadline
			switch mode {
			case "global":
				started = now.Add(-17 * time.Hour)
				want = now.Add(time.Hour)
			case "caller":
				var cancel context.CancelFunc
				want = now.Add(time.Minute)
				ctx, cancel = context.WithDeadline(ctx, want)
				defer cancel()
			case "expired_global":
				started = now.Add(-18 * time.Hour)
			case "expired_phase":
				deadline = now
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil":
				ctx = nil
			case "missing_author":
				started = time.Time{}
			case "future_author":
				started = now.Add(time.Hour)
			case "changed_wall":
				plan.SafetyEnvelope.MaximumTotalWallMS++
			case "v2":
				plan.Schema = PlanV2Schema
			}
			got, err := archiveLifetimeDeadline(ctx, plan, started, deadline)
			valid := mode == "phase" || mode == "global" || mode == "caller"
			if valid && (err != nil || !got.Equal(want)) || !valid && err == nil {
				t.Fatal(got, want, err)
			}
		})
	}
	// Starting later never renews the archive phase or its successor reserve.
	plan := Plan{Schema: PlanV3Schema, SafetyEnvelope: frozenSafetyEnvelope()}
	originalPhase := now.Add(10 * time.Minute)
	candidate := originalPhase.Add(4*time.Hour + 20*time.Minute)
	first, err := archiveLifetimeDeadline(t.Context(), plan, now.Add(-time.Hour), candidate)
	second, laterErr := archiveLifetimeDeadline(t.Context(), plan, now.Add(-time.Hour), candidate)
	if err != nil || laterErr != nil || !first.Equal(candidate) || !second.Equal(first) {
		t.Fatal("restart renewed original archive clock", first, second, err, laterErr)
	}
}
