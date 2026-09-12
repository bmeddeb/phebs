package t421

import (
	"context"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

// Fixed HTTP observations: physical pre-pin and finish, logical finish,
// return finish. PostAuthor is the separately acknowledged native callback.
// Transient marker coverage remains separate.
type ExecutionMidphaseSamples struct {
	Points                     [4]ExecutionWorkspaceBytePhase
	PostAuthor                 ExecutionWorkspaceBytePhase
	Unavailable, LimitExceeded bool
}

// Logical start after predecessor join/advance, then return before/after
// author nine. Actual observations propagate across genuine successor borrows.
type ExecutionMidphaseParentSamples struct {
	Points                     [3]ExecutionWorkspaceBytePhase
	Unavailable, LimitExceeded bool
}

func (run *ExecutionEpochOneRun) midphaseWorkspaceSelected() bool {
	return run != nil && run.flow != nil && run.flow.workspace != nil &&
		(run.epoch.Epoch == 1 && run.physicalAllowed || run.epoch.Epoch == 2 || run.epoch.Epoch == 3 && run.checkpointAllowed)
}

func (run *ExecutionEpochOneRun) failMidphaseParent() {
	if run == nil || run.flow == nil || run.flow.workspace == nil {
		return
	}
	if run.flow.workspaceBytes != nil {
		_ = run.flow.workspaceBytes.Fail()
	}
	run.mu.Lock()
	run.result.ParentMidphaseSamples.Unavailable = true
	run.mu.Unlock()
}

// Caller retains flow.mu over this walk, excluding custody release/new launch;
// author/epoch/run locks only span pre/post checks. No engine remains alive.
// The ctx is the already-created handoff phase clock, never a new walk budget.
func (run *ExecutionEpochOneRun) sampleMidphaseParentLocked(ctx context.Context, point uint8) error {
	flow := run.flow
	if flow.workspace == nil {
		return nil
	}
	refuse := func() error { run.failMidphaseParent(); return ErrExecutionEpochOne }
	if point > 2 || flow.workspaceBytes == nil || flow.epochs == nil || flow.epochs.author == nil ||
		flow.controller == nil || flow.store == nil || flow.plan.Schema != PlanV3Schema {
		return refuse()
	}
	phase, predecessor := uint32(6), 2
	if point == 0 {
		phase, predecessor = 5, 1
	}
	confirm := func() bool {
		if !run.midphaseParentBoundaryLocked(ctx, point) {
			return false
		}
		view, err := flow.controller.ProducerLaunch(uint32(predecessor) + 2)
		accounting, daErr := flow.controller.Snapshot()
		state, saErr := flow.store.Snapshot()
		if err != nil || daErr != nil || saErr != nil || view.Phase != phase || state.Store.Phase != phase ||
			state.Opened != predecessor || state.TerminalEOF != predecessor {
			return false
		}
		daClosed, saClosed := false, false
		for _, p := range accounting.Producers {
			if p.Producer == uint32(predecessor)+1 {
				daClosed = p.Attached && p.Closed && p.Active == 0
			}
		}
		for _, p := range state.Store.Producers {
			if p.Producer == uint32(predecessor)+1 {
				saClosed = p.Attached && p.Closed && p.Calls == 0 && p.Transactions == 0
			}
		}
		return daClosed && saClosed
	}
	if !confirm() {
		return refuse()
	}
	run.mu.Lock()
	run.result.ParentMidphaseSamples.Points[point].Attempts++
	run.mu.Unlock()
	value, err := flow.workspaceBytes.SampleConfirmed(ctx, phase, confirm)
	if err != nil {
		return refuse()
	}
	run.mu.Lock()
	row := &run.result.ParentMidphaseSamples.Points[point]
	row.Completed++
	row.Maximum = value
	excess := value.LogicalBytes > flow.plan.WorkEnvelope.MaximumDataLogicalBytes ||
		value.AllocatedBytes > flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes
	run.result.ParentMidphaseSamples.LimitExceeded = excess
	run.mu.Unlock()
	if excess {
		return ErrExecutionEpochOne
	}
	return nil
}

func (reader *executionEpochInspection) sampleMidphaseWorkspace(ctx context.Context, point uint8) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.run == nil || reader.run.flow == nil {
		return errEpochInspection
	}
	run := reader.run
	if !run.midphaseWorkspaceSelected() {
		return nil
	}
	defer func() {
		reader.fail(retErr)
		if retErr != nil && !reader.midphaseSamples.LimitExceeded {
			reader.midphaseSamples.Unavailable = true
		}
	}()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || point > 3 || reader.plan.Schema != PlanV3Schema ||
		run.control == nil || reader.midphaseSamples.Points[point].Attempts != 0 {
		return errEpochInspection
	}
	expected := [...]struct {
		epoch uint64
		phase string
	}{
		{1, "warm_noop"}, {1, "physical_delta_b"}, {2, "logical_delta_b"}, {3, "return_a"},
	}
	// Point zero precedes beginPhysical, but the native semantic/SA phase is
	// already four after advancePhysical. The guarded child checks that phase.
	if run.epoch.Epoch != expected[point].epoch || reader.projection.Phase != expected[point].phase || !reader.finalUsed ||
		point == 0 && (reader.warmAuthority.Phase != "warm_noop" || reader.earlyFinishSamples.Phases[1].Completed != 1) ||
		point == 1 && (reader.midphaseSamples.Points[0].Completed != 1 || reader.midphaseSamples.PostAuthor.Completed != 1 || !reader.retentionUsed) ||
		point != 0 && reader.plan.SelectorHandoffCleanup != nil && reader.selectorCleanupPhase != expected[point].phase {
		return errEpochInspection
	}
	run.mu.Lock()
	parent := run.result.ParentMidphaseSamples
	run.mu.Unlock()
	if point >= 2 {
		last := 0
		if point == 3 {
			last = 2
		}
		if parent.Unavailable || parent.LimitExceeded {
			return errEpochInspection
		}
		for i := 0; i <= last; i++ {
			if parent.Points[i].Attempts != 1 || parent.Points[i].Completed != 1 {
				return errEpochInspection
			}
		}
	}
	row := &reader.midphaseSamples.Points[point]
	row.Attempts++
	name := "finish"
	if point == 0 {
		name = "start"
	}
	value, err := reader.readWorkspaceSample(ctx, name)
	if err != nil {
		return err
	}
	row.Completed++
	row.Maximum = value
	if value.LogicalBytes > reader.plan.WorkEnvelope.MaximumDataLogicalBytes || value.AllocatedBytes > reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		reader.midphaseSamples.LimitExceeded = true
		return errEpochInspection
	}
	return ctx.Err()
}

func midphaseWorkspacePrefix(producer uint32, stream ExecutionWorkspaceByteObservation, samples ExecutionMidphaseSamples, marker ExecutionMarkerWorkspace) bool {
	if !stream.Bound || !stream.Complete || stream.Unavailable || stream.LimitExceeded || samples.Unavailable || samples.LimitExceeded {
		return false
	}
	first, end, phase := 0, 2, 4
	switch producer {
	case 2:
	case 3:
		first, end, phase = 2, 3, 5
	case 4:
		first, end, phase = 3, 4, 6
	default:
		return false
	}
	var joined ExecutionWorkspaceBytePhase
	for index, row := range samples.Points {
		if index < first || index >= end {
			if row != (ExecutionWorkspaceBytePhase{}) {
				return false
			}
			continue
		}
		if row.Attempts != 1 || row.Completed != 1 || stream.midphaseSamples[index] != row.Maximum {
			return false
		}
		joined.Attempts++
		joined.Completed++
		joined.Maximum = custodybytes.Sample{LogicalBytes: max(joined.Maximum.LogicalBytes, row.Maximum.LogicalBytes),
			AllocatedBytes: max(joined.Maximum.AllocatedBytes, row.Maximum.AllocatedBytes)}
	}
	if producer == 2 {
		row := samples.PostAuthor
		if row.Attempts != 1 || row.Completed != 1 || stream.physicalPostAuthor != row.Maximum || !stream.physicalReady {
			return false
		}
		joined.Attempts++
		joined.Completed++
		joined.Maximum.LogicalBytes = max(joined.Maximum.LogicalBytes, row.Maximum.LogicalBytes)
		joined.Maximum.AllocatedBytes = max(joined.Maximum.AllocatedBytes, row.Maximum.AllocatedBytes)
	} else if samples.PostAuthor != (ExecutionWorkspaceBytePhase{}) {
		return false
	}
	if producer == 4 {
		if marker.Unavailable || marker.LimitExceeded || !marker.Ready || !stream.markerReady || marker.Sample.Attempts != 1 ||
			marker.Sample.Completed != 1 || marker.Sample.Maximum != stream.markerSample {
			return false
		}
		joined.Attempts++
		joined.Completed++
		joined.Maximum.LogicalBytes = max(joined.Maximum.LogicalBytes, marker.Sample.Maximum.LogicalBytes)
		joined.Maximum.AllocatedBytes = max(joined.Maximum.AllocatedBytes, marker.Sample.Maximum.AllocatedBytes)
	} else if marker != (ExecutionMarkerWorkspace{}) {
		return false
	}
	return stream.Phases[phase-1] == joined
}

func (samples *ExecutionMidphaseSamples) failIncomplete(producer uint32) {
	if samples.Unavailable || samples.LimitExceeded {
		return
	}
	if producer == 2 && (samples.PostAuthor.Attempts != 1 || samples.PostAuthor.Completed != 1) {
		samples.Unavailable = true
	}
	first, end := 0, 2
	switch producer {
	case 2:
	case 3:
		first, end = 2, 3
	case 4:
		first, end = 3, 4
	default:
		samples.Unavailable = true
		return
	}
	for i := first; i < end; i++ {
		if samples.Points[i].Attempts != 1 || samples.Points[i].Completed != 1 {
			samples.Unavailable = true
		}
	}
}

// Caller owns flow.mu; model tests exercise these ownership predicates only.
func (run *ExecutionEpochOneRun) midphaseParentBoundaryLocked(ctx context.Context, point uint8) bool {
	if run == nil || run.flow == nil || run.flow.epochs == nil || run.flow.epochs.author == nil || point > 2 {
		return false
	}
	flow := run.flow
	predecessor, nextAuthor := uint64(2), 2
	if point == 0 {
		predecessor = 1
	}
	if point == 2 {
		nextAuthor = 3
	}
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	deadline, bounded := ctx.Deadline()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	run.mu.Lock()
	rows := run.result.ParentMidphaseSamples
	valid := bounded && !flow.closed && flow.retained == run && run.epoch.Epoch == predecessor &&
		!author.active && !author.closed && author.err == nil && author.next == nextAuthor && author.borrowedBy == run &&
		epochs.active && !epochs.closed && epochs.err == nil && epochs.released == predecessor &&
		run.err == nil && run.result.RootStarted && run.result.RootJoined && run.result.SessionEmpty &&
		!run.midphaseDeadline.IsZero() && !deadline.After(run.midphaseDeadline) &&
		!rows.Unavailable && !rows.LimitExceeded && rows.Points[point].Completed == 0 &&
		(point == 0 && flow.logicalUsed || point > 0 && flow.returnUsed && run.returnStarting)
	for i := uint8(0); i < point; i++ {
		valid = valid && rows.Points[i].Attempts == 1 && rows.Points[i].Completed == 1
	}
	run.mu.Unlock()
	epochs.mu.Unlock()
	author.mu.Unlock()
	return valid
}

// Snapshot only the internally retained operation prefix. Values contain no
// slices/pointers; caller mutation cannot alter a successor or later Wait.
func (run *ExecutionEpochOneRun) midphaseParentPrefix() ExecutionMidphaseParentSamples {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.result.ParentMidphaseSamples
}
