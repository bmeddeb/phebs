//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// Real held-root walks and actual empty DA/SA phase advancement; AuthorA's
// ownership states and file mutation are modeled, not a real author process.
func TestEpochAuthorWorkspaceActualWalks(t *testing.T) {
	for _, mode := range []string{"valid", "canceled_after_start", "active_author", "repeat"} {
		t.Run(mode, func(t *testing.T) {
			owner, ctx := custodyByteFixture(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			plan := accountingTestPlan(t)
			bindings := testExecutionDispatchBindings()
			da, err := executionDispatchConfig(plan, bindings)
			if err != nil {
				t.Fatal(err)
			}
			controller, err := dispatchadmission.New(ctx, da)
			if err != nil {
				t.Fatal(err)
			}
			parent, err := controller.NewLocalProducer(ctx, executionRootProducer)
			if err != nil {
				t.Fatal(err)
			}
			sa, wire, err := executionStoreConfig(plan, bindings)
			if err != nil {
				t.Fatal(err)
			}
			store, err := storeaccounting.New(ctx, sa)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := storeaccounting.NewTransport(ctx, store, wire)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transport.Close() }()
			if parent.Pause(ctx) != nil || controller.Fence() != nil || parent.Checkpoint(ctx) != nil ||
				transport.Fence() != nil || transport.Advance() != nil || controller.Advance() != nil || parent.Resume(2) != nil {
				t.Fatal("actual empty phase handoff")
			}
			observer := custodybytes.NewBorrowed(owner.file, owner.path, owner.info, owner.volume)
			flow := &ExecutionEpochOne{plan: plan, controller: controller, parent: parent, store: transport,
				workspace: &owner, workspaceBytes: observer, authorStarted: time.Now(),
				epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}}}
			if err := os.WriteFile(filepath.Join(owner.path, "preparation"), []byte("real preparation bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			flow.mu.Lock()
			firstErr := flow.sampleAuthorWorkspaceLocked(ctx, 0)
			flow.mu.Unlock()
			prior := observer.Snapshot()
			if firstErr != nil || !prior.Phases[1].Completed || prior.Phases[1].Maximum.LogicalBytes == 0 {
				t.Fatal(firstErr, prior)
			}
			if err := os.WriteFile(filepath.Join(owner.path, "authored"), []byte("actual additional source fixture bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			flow.authored, flow.epochs.author.next = true, 1
			point := uint8(1)
			switch mode {
			case "canceled_after_start":
				cancel()
			case "active_author":
				flow.epochs.author.active = true
			case "repeat":
				point = 0
			}
			flow.mu.Lock()
			err = flow.sampleAuthorWorkspaceLocked(ctx, point)
			flow.mu.Unlock()
			got := observer.Snapshot()
			if mode == "valid" {
				if err != nil || got.Unavailable || flow.authorBytePoint != 2 ||
					got.Phases[1].Maximum.LogicalBytes <= prior.Phases[1].Maximum.LogicalBytes {
					t.Fatal(err, got)
				}
			} else if err == nil || !got.Unavailable || got.Phases[1] != prior.Phases[1] {
				t.Fatal("failed boundary lost prior actual maximum", err, got, prior)
			}
		})
	}
}
