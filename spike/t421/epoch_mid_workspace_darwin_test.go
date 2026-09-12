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

// Actual helper/native process session and DA/SDK protocol joins followed by a
// real held-root phase-five walk. Author/config bookkeeping and semantic HTTP
// responses are supplied. No Surreal, author process, full finish,
// StartLogicalB/protected constructor or actual successor launch is claimed.
func TestEpochMidphaseParentJoinedNativeWalk(t *testing.T) {
	owner, _ := custodyByteFixture(t)
	const content = "actual phase-five workspace bytes"
	if err := os.WriteFile(filepath.Join(owner.path, "source"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	plan := accountingTestPlan(t)
	testEpochInheritedHandoff(t, "warm_physical", func(ctx context.Context, run *ExecutionEpochOneRun) {
		if run.err != nil || !run.result.RootStarted || !run.result.RootJoined || !run.result.SessionEmpty ||
			run.result.Store.Opened != 1 || run.result.Store.TerminalEOF != 1 {
			t.Fatal("missing actual predecessor join", run.err, run.result)
		}
		flow := run.flow
		// Only author/source/config ownership is modeled; real transport state,
		// phase advancement, joined flags and held native root are not supplied.
		flow.plan, flow.logicalUsed, flow.retained = plan, true, run
		run.epoch.Epoch = 1
		flow.epochs = &ExecutionEpochConfigCustody{active: true, released: 1,
			author: &ExecutionAuthorCustody{next: 2, borrowedBy: run}}
		flow.workspace = &owner
		flow.workspaceBytes = custodybytes.NewBorrowed(owner.file, owner.path, owner.info, owner.volume)
		flow.mu.Lock()
		err := run.advanceLogical(ctx)
		if err == nil {
			err = run.sampleMidphaseParentLocked(ctx, 0)
		}
		flow.mu.Unlock()
		if err != nil {
			t.Fatal("actual joined phase-five parent walk", err)
		}
		view, daErr := flow.controller.ProducerLaunch(3)
		sa, saErr := flow.store.Snapshot()
		prefix := run.midphaseParentPrefix()
		row := prefix.Points[0]
		observed := flow.workspaceBytes.Snapshot()
		if daErr != nil || saErr != nil || view.Phase != 5 || sa.Store.Phase != 5 ||
			sa.Opened != 1 || sa.TerminalEOF != 1 || prefix.Unavailable || prefix.LimitExceeded ||
			row.Attempts != 1 || row.Completed != 1 || row.Maximum.LogicalBytes != uint64(len(content)) || row.Maximum.AllocatedBytes == 0 ||
			observed.Unavailable || !observed.Phases[4].Completed || observed.Phases[4].Maximum != row.Maximum {
			t.Fatal("actual phase-five prefix", prefix, observed, daErr, saErr)
		}
		// Same value-copy used by StartLogicalB. This models successor storage,
		// not a launch or public terminal completion; terminal notification stays unset.
		// The separate stale_stop_join regression exercises real finish/Wait.
		next := &ExecutionEpochOneRun{}
		next.result.ParentMidphaseSamples = run.midphaseParentPrefix()
		copied := run.midphaseParentPrefix()
		if copied != prefix || next.result.ParentMidphaseSamples != prefix || run.err != nil {
			t.Fatal("private sample prefix lost or terminal state manufactured", copied, run.err)
		}
		select {
		case <-run.done:
			t.Fatal("protocol-only fixture manufactured terminal completion")
		default:
		}
		copied.Points[0].Maximum.LogicalBytes++
		next.result.ParentMidphaseSamples.Points[0].Maximum.AllocatedBytes++
		if run.midphaseParentPrefix() != prefix {
			t.Fatal("private or modeled successor prefix aliases predecessor")
		}
	})
}
