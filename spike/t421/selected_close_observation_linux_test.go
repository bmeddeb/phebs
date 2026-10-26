//go:build linux

package t421

import (
	"context"
	"testing"
)

// Real controller/config construction, modeled proof shape only: this cannot
// issue operational resource/image authority or run a native qualification.
func TestLinuxEpochSelectedCloseSourceBinding(t *testing.T) {
	for _, change := range []string{"none", "schema", "foreign flow", "no native", "legacy recipe", "not completed", "pressure", "backup", "terminal predecessor", "not recovered", "unreserved successor", "expired successor", "wrong epoch", "canceled", "nil context"} {
		t.Run(change, func(t *testing.T) {
			adapter, prior, run := linuxEpochCheckpointShape(t)
			plan := typedExtractionTestPlan(t)
			if err := applyTypedExtractionCorrection(&plan); err != nil {
				t.Fatal(err)
			}
			adapter.core.plan = plan
			run.epoch.Epoch, run.checkpointUsed, run.warm = 4, true, true
			adapter.checkpoint.reserved.Store(true)
			adapter.checkpoint.released.Store(true)
			ctx := t.Context()
			switch change {
			case "schema":
				adapter.core.plan.Schema = PlanV5Schema
			case "foreign flow":
				run.flow = &ExecutionEpochOne{plan: plan, native: adapter}
			case "no native":
				adapter.core.native = nil
			case "legacy recipe":
				adapter.held.flow.recipe = linuxEpochLegacyV11
				adapter.recipe = issueLinuxEpochLegacyRecipe(adapter)
			case "not completed":
				run.checkpointUsed = false
			case "pressure":
				run.pressureAllowed = true
			case "backup":
				run.backupAllowed = true
			case "terminal predecessor":
				run = prior
				run.checkpointAllowed, run.checkpointUsed, run.terminalEntered, run.terminalRequested = true, true, true, true
			case "not recovered":
				run.warm = false
			case "unreserved successor":
				adapter.checkpoint.reserved.Store(false)
			case "expired successor":
				expired, cancel := context.WithCancel(adapter.checkpoint.lifetime)
				cancel()
				adapter.checkpoint.lifetime = expired
			case "wrong epoch":
				run.epoch.Epoch = 5
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "nil context":
				ctx = nil
			}
			want := uint32(0)
			if change == "none" {
				want = 8
			}
			if got := run.attemptObservationReuseThrough(ctx); got != want {
				t.Fatalf("source-selected close boundary=%d want=%d", got, want)
			}
		})
	}
}
