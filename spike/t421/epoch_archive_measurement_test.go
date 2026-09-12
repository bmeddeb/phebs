package t421

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/recovery"
)

func TestEpochArchiveMeasurementMaximum(t *testing.T) {
	for _, plan := range lifecyclePolicyPlans(t) {
		for _, producer := range []uint32{10, 11} {
			got, err := archiveCheckpointMaximum(plan, producer)
			if plan.Schema != PlanV3Schema {
				if err == nil || got != 0 {
					t.Fatal("historical archive acquired new measurement authority")
				}
				continue
			}
			want := recovery.BackupCheckpointMaximum()
			if producer == 11 {
				want = 34 + 2*(100_000-2)
			}
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
		}
	}
	for _, transactions := range []uint64{0, 1, 2, 100_000, (math.MaxUint32-34)/2 + 2, (math.MaxUint32-34)/2 + 3, math.MaxUint64} {
		plan := accountingTestPlan(t)
		for i := range plan.WorkEnvelope.Phases {
			if plan.WorkEnvelope.Phases[i].Phase == "archive_restore" {
				plan.WorkEnvelope.Phases[i].StoreTransactions.Maximum = transactions
			}
		}
		want, wantErr := recovery.RestoreCheckpointMaximum(transactions)
		got, err := archiveCheckpointMaximum(plan, 11)
		if got != want || (err == nil) != (wantErr == nil) {
			t.Fatal(transactions, got, err, want, wantErr)
		}
	}
	plan := accountingTestPlan(t)
	plan.WorkEnvelope.Phases = nil
	if _, err := archiveCheckpointMaximum(plan, 11); err == nil {
		t.Fatal("missing archive allowance admitted")
	}
}

func TestEpochArchiveWorkspaceOwningBoundary(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, producer := range []uint32{10, 11} {
		for _, mode := range []string{"absent", "binding_only", "completed", "insufficient", "wrong_shape", "failed_native", "partial"} {
			raw := archiveWorkTestBindings(producer)
			if mode != "absent" {
				raw += workspaceTestBinding(producer)
			}
			switch mode {
			case "completed", "wrong_shape":
				count := uint64(15)
				if producer == 11 {
					count = 34
				}
				if mode == "wrong_shape" {
					count++
				}
				for sequence := uint64(1); sequence <= count; sequence++ {
					raw += workspaceTestPair(producer, 12, sequence, 4, 8)
				}
			case "insufficient", "failed_native", "partial":
				raw += workspaceTestPair(producer, 12, 1, 4, 8)
			}
			if mode == "partial" {
				raw += workspaceTestEvent(producer, 12, 'B', 2, 0, 0)
			}
			output := &checkoutCommandOutput{}
			output.buffer.WriteString(raw)
			out, _ := observeArchiveWork(output, plan, producer, [32]byte{1}, true, mode != "failed_native")
			if got := epochArchiveWorkspaceComplete(out.WorkspaceBytes, producer); got != (mode == "completed") {
				t.Fatal(producer, mode, out.WorkspaceBytes)
			}
			if mode == "failed_native" || mode == "partial" {
				row := out.WorkspaceBytes.Phases[11]
				if row.Completed != 1 || row.Maximum.LogicalBytes != 4 || row.Maximum.AllocatedBytes != 8 {
					t.Fatal("failed positive prefix lost", mode, row)
				}
			}
		}
	}
}

// Ownership predicates only: these modeled states do not claim actual native
// joins, controller history, engine state or measured bytes.
func TestEpochArchiveRemovalBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "flow_closed", "wrong_retained", "not_starting", "not_used", "already_started", "run_error", "epoch", "not_retired", "incomplete", "unjoined", "session", "author_active", "author_closed", "borrow", "epoch_inactive", "successor", "expired", "renewed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			flow := &ExecutionEpochOne{epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}, active: true, released: 4}}
			run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 4}, returnStarting: true, restoreUsed: true, backupRetired: true, backupComplete: true, backupJoined: true, backupSessionEmpty: true, phaseDeadline: time.Now().Add(time.Minute), lifetimeDeadline: time.Now().Add(2 * time.Minute)}
			flow.retained, flow.epochs.author.borrowedBy = run, run
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if !run.retiredRemovalBoundary(ctx) {
				t.Fatal("valid initial ownership refused")
			}
			switch mode {
			case "flow_closed":
				flow.closed = true
			case "wrong_retained":
				flow.retained = nil
			case "not_starting":
				run.returnStarting = false
			case "not_used":
				run.restoreUsed = false
			case "already_started":
				run.restoreStarted = true
			case "run_error":
				run.err = ErrExecutionEpochOne
			case "epoch":
				run.epoch.Epoch = 3
			case "not_retired":
				run.backupRetired = false
			case "incomplete":
				run.backupComplete = false
			case "unjoined":
				run.backupJoined = false
			case "session":
				run.backupSessionEmpty = false
			case "author_active":
				flow.epochs.author.active = true
			case "author_closed":
				flow.epochs.author.closed = true
			case "borrow":
				flow.epochs.author.borrowedBy = nil
			case "epoch_inactive":
				flow.epochs.active = false
			case "successor":
				flow.epochs.released = 5
			case "expired":
				run.phaseDeadline = time.Now().Add(-time.Second)
			case "renewed":
				run.phaseDeadline = run.lifetimeDeadline.Add(time.Second)
			case "canceled":
				cancel()
			}
			if got := run.retiredRemovalBoundary(ctx); got != (mode == "valid") {
				t.Fatal(mode, got)
			}
		})
	}
}
