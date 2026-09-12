package t421

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/recovery"
)

func TestEpochArchiveWorkspaceControlAllowance(t *testing.T) {
	child := recovery.BackupCheckpointMaximum()
	parent := epochBackupMeasurementMaximum()
	if child != 16 || parent != child+1 || uint64(parent)*4*dispatchadmission.FrameBytes != 4352 {
		t.Fatal("parent start must add one hold without renewing child budget", child, parent)
	}
	if wire, err := dispatchadmission.ArchiveMeasurementWireBytes(child); err != nil || wire != 1176 {
		t.Fatal("child FD7 allowance changed", wire, err)
	}
}

// These are ownership state models, not native join, engine or byte evidence.
// Actual walker/quiescence remains the existing observer and PC implementation.
func TestEpochArchiveWorkspaceBoundary(t *testing.T) {
	for _, point := range []archiveWorkspacePoint{archiveWorkspaceStart, archiveWorkspaceBackupJoined, archiveWorkspaceRestoreJoined} {
		for _, mode := range []string{"valid", "canceled", "unbounded", "deadline_renewed", "expired", "flow_closed", "borrow_lost", "author_active", "successor", "failed", "duplicate", "skipped", "not_retired", "active_wrong_boundary"} {
			t.Run(string(rune('0'+point))+"/"+mode, func(t *testing.T) {
				run := archiveWorkspaceModel(point)
				ctx, cancel := context.WithDeadline(t.Context(), run.phaseDeadline)
				defer cancel()
				switch mode {
				case "canceled":
					cancel()
				case "unbounded":
					ctx = context.Background()
				case "deadline_renewed":
					run.phaseDeadline = run.phaseDeadline.Add(-time.Second)
				case "expired":
					run.phaseDeadline = time.Now().Add(-time.Second)
				case "flow_closed":
					run.flow.closed = true
				case "borrow_lost":
					run.flow.epochs.author.borrowedBy = nil
				case "author_active":
					run.flow.epochs.author.active = true
				case "successor":
					run.flow.epochs.released = 5
				case "failed":
					run.err = ErrExecutionEpochOne
				case "duplicate":
					run.archiveWorkspacePoint = point
				case "skipped":
					run.archiveWorkspacePoint = point + 1
				case "not_retired":
					run.backupRetired = false
				case "active_wrong_boundary":
					switch point {
					case archiveWorkspaceStart:
						run.stopping = true
					case archiveWorkspaceBackupJoined:
						close(run.done)
					default:
						run.returnStarting = false
					}
				}
				if got := run.archiveWorkspaceBoundary(ctx, point); got != (mode == "valid") {
					t.Fatal(point, mode, got)
				}
			})
		}
	}
}

func archiveWorkspaceModel(point archiveWorkspacePoint) *ExecutionEpochOneRun {
	flow := &ExecutionEpochOne{epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}, active: true, released: 4}}
	run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 4}, done: make(chan struct{}), backupUsed: true, backupRetired: true,
		phaseDeadline: time.Now().Add(time.Minute), lifetimeDeadline: time.Now().Add(2 * time.Minute), archiveWorkspacePoint: point - 1}
	flow.retained, flow.epochs.author.borrowedBy = run, run
	if point >= archiveWorkspaceBackupJoined {
		run.stopping, run.backupComplete, run.backupJoined, run.backupSessionEmpty = true, true, true, true
		run.result.RootJoined, run.result.SessionEmpty = true, true
	}
	if point == archiveWorkspaceRestoreJoined {
		run.returnStarting, run.restoreUsed, run.restoreComplete, run.restoreStarted, run.restoreJoined, run.restoreSessionEmpty = true, true, true, true, true, true
		close(run.done)
	}
	return run
}

func TestEpochArchiveWorkspaceAbsent(t *testing.T) {
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{}}
	for _, point := range []archiveWorkspacePoint{archiveWorkspaceStart, archiveWorkspaceBackupJoined, archiveWorkspaceRestoreJoined} {
		if err := run.sampleArchiveWorkspace(t.Context(), point); err != nil || run.archiveWorkspacePoint != 0 {
			t.Fatal("legacy unbound path gained measurement", point, err)
		}
	}
	run.flow.workspace = &productionRoot{}
	if err := run.sampleArchiveWorkspace(t.Context(), archiveWorkspaceStart); err == nil {
		t.Fatal("bound workspace without actual observer admitted")
	}
}
