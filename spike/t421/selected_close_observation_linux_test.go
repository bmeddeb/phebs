//go:build linux

package t421

import (
	"context"
	"fmt"
	"testing"
)

// Real controller/config construction, modeled proof shape only: this cannot
// issue operational resource/image authority or run a native qualification.
func TestLinuxEpochSelectedCloseSourceBinding(t *testing.T) {
	for _, epoch := range []uint64{3, 4} {
		for _, change := range []string{"none", "schema", "foreign flow", "no native", "legacy recipe", "not completed", "pressure", "backup", "terminal not requested", "not recovered", "unreserved successor", "expired successor", "wrong epoch", "canceled", "nil context"} {
			t.Run(fmt.Sprintf("epoch%d/%s", epoch, change), func(t *testing.T) {
				adapter, prior, next := linuxEpochCheckpointShape(t)
				plan := typedExtractionTestPlan(t)
				if err := applyTypedExtractionCorrection(&plan); err != nil {
					t.Fatal(err)
				}
				adapter.core.plan = plan
				prior.checkpointAllowed, prior.checkpointUsed = true, true
				prior.terminalEntered, prior.terminalRequested = true, true
				next.epoch.Epoch, next.checkpointUsed, next.warm = 4, true, true
				adapter.checkpoint.reserved.Store(true)
				adapter.checkpoint.released.Store(true)
				run := prior
				if epoch == 4 {
					run = next
				}
				ctx := t.Context()
				want := uint32(7)
				if epoch == 4 {
					want = 8
				}
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
				case "terminal not requested":
					prior.terminalRequested = false
				case "not recovered":
					next.warm = false
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
				if change != "none" && !(epoch == 3 && (change == "not recovered" || change == "unreserved successor" || change == "expired successor")) && !(epoch == 4 && change == "terminal not requested") {
					want = 0
				}
				if got := run.attemptObservationReuseThrough(ctx); got != want {
					t.Fatalf("source-selected close boundary=%d want=%d", got, want)
				}
			})
		}
	}
}
