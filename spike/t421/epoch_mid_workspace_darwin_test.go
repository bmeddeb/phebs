//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

// The positive prefix is a real tiny parent workspace traversal. This does
// not fabricate successful DA/SA/native joins: the new operation is refused
// without those prerequisites, retaining the actual prior maximum.
func TestEpochMidphaseRefusalRetainsActualWalk(t *testing.T) {
	for _, phase := range []uint32{5, 6} {
		t.Run(string(rune('0'+phase)), func(t *testing.T) {
			owner, ctx := custodyByteFixture(t)
			if err := os.WriteFile(filepath.Join(owner.path, "actual-source"), []byte("actual tiny source bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			observer := custodybytes.NewBorrowed(owner.file, owner.path, owner.info, owner.volume)
			value, err := observer.Sample(ctx, phase)
			if err != nil || value.LogicalBytes == 0 || value.AllocatedBytes == 0 {
				t.Fatal(value, err)
			}
			point := uint8(0)
			if phase == 6 {
				point = 1
			}
			run := midphaseParentModel(point)
			run.flow.workspace, run.flow.workspaceBytes = &owner, observer
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			run.flow.mu.Lock()
			err = run.sampleMidphaseParentLocked(ctx, point)
			run.flow.mu.Unlock()
			got := observer.Snapshot()
			if err == nil || !got.Unavailable || !got.Phases[phase-1].Completed || got.Phases[phase-1].Maximum != value ||
				!run.result.ParentMidphaseSamples.Unavailable {
				t.Fatal(err, got)
			}
		})
	}
}
