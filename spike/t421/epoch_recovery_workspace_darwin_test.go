//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

// Tiny actual phase-eight traversal plus ownership-refusal preservation, not a
// hard-death/engine/restart proof. Real terminal protocol gates remain separate.
func TestEpochRecoveryWorkspaceRefusalRetainsActualWalk(t *testing.T) {
	owner, ctx := custodyByteFixture(t)
	if err := os.WriteFile(filepath.Join(owner.path, "checkpoint"), []byte("actual tiny retained checkpoint bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	observer := custodybytes.NewBorrowed(owner.file, owner.path, owner.info, owner.volume)
	value, err := observer.Sample(ctx, 8)
	if err != nil || value.LogicalBytes == 0 || value.AllocatedBytes == 0 {
		t.Fatal(value, err)
	}
	run := recoveryParentModel()
	run.flow.workspace, run.flow.workspaceBytes = &owner, observer
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	run.flow.mu.Lock()
	err = run.sampleRecoveryParentLocked(ctx)
	run.flow.mu.Unlock()
	got := observer.Snapshot()
	if err == nil || !got.Unavailable || !got.Phases[7].Completed || got.Phases[7].Maximum != value || !run.result.RecoverySamples.Unavailable {
		t.Fatal(err, got)
	}
}
