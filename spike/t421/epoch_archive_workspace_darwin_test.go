//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

func TestEpochArchiveWorkspaceRefusalRetainsNativePrefix(t *testing.T) {
	for _, mode := range []string{"canceled", "successor", "borrow_lost", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			owner, ctx := custodyByteFixture(t)
			if err := os.WriteFile(filepath.Join(owner.path, "actual-prefix"), []byte("actual linked workspace bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			observer := custodybytes.NewBorrowed(owner.file, owner.path, owner.info, owner.volume)
			prior, err := observer.Sample(ctx, 12)
			if err != nil || prior.LogicalBytes == 0 || prior.AllocatedBytes == 0 {
				t.Fatal(prior, err)
			}
			// Only ownership is modeled. The retained prefix above came from
			// the real walker, not authored totals or a modeled engine.
			run := archiveWorkspaceModel(archiveWorkspaceBackupJoined)
			run.flow.workspace, run.flow.workspaceBytes = &owner, observer
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			switch mode {
			case "canceled":
				cancel()
			case "successor":
				run.flow.epochs.released = 5
			case "borrow_lost":
				run.flow.epochs.author.borrowedBy = nil
			case "duplicate":
				run.archiveWorkspacePoint = archiveWorkspaceBackupJoined
			}
			if err := run.sampleArchiveWorkspace(ctx, archiveWorkspaceBackupJoined); err == nil {
				t.Fatal("unsafe joined boundary admitted")
			}
			got := observer.Snapshot()
			if !got.Unavailable || !got.Phases[11].Completed || got.Phases[11].Maximum != prior {
				t.Fatal("actual completed prefix lost", got, prior)
			}
			if _, err := observer.Sample(ctx, 12); err == nil {
				t.Fatal("sticky refusal cleared")
			}
		})
	}
}
