package t421

import "context"

// The actual marker S survives later readiness or process failure. Readiness
// is separate and can never be inferred from a completed byte sample.
type ExecutionMarkerWorkspace struct {
	Sample                            ExecutionWorkspaceBytePhase
	Ready, Unavailable, LimitExceeded bool
}

func newEpochMarkerWorkspaceOutput(output *checkoutCommandOutput, plan Plan, input [32]byte) *epochWarmWorkspaceOutput {
	out := newEpochWarmWorkspaceOutput(output, plan, input)
	out.producer = 4
	return out
}

func (out *epochWarmWorkspaceOutput) armMarker() error {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.producer != 4 || out.armed || out.err != nil || out.observation.Phases[5].Attempts != 0 {
		return ErrExecutionEpochOne
	}
	out.armed = true
	return nil
}

func (out *epochWarmWorkspaceOutput) waitMarker(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ErrExecutionEpochOne
	case <-out.ready:
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.producer != 4 || ctx.Err() != nil || out.err != nil || !out.armed || !out.observation.markerReady || out.markerSample.Completed != 1 {
		return ErrExecutionEpochOne
	}
	return nil
}

func (out *epochWarmWorkspaceOutput) markerSnapshot() ExecutionMarkerWorkspace {
	out.mu.Lock()
	defer out.mu.Unlock()
	row := out.markerSample
	return ExecutionMarkerWorkspace{Sample: row, Ready: out.observation.markerReady,
		Unavailable:   out.markerInvalid || row.Completed != 1 && (out.err != nil || out.armed),
		LimitExceeded: row.Completed == 1 && (row.Maximum.LogicalBytes > out.plan.WorkEnvelope.MaximumDataLogicalBytes || row.Maximum.AllocatedBytes > out.plan.SafetyEnvelope.MaximumDataAllocatedBytes)}
}

func (run *ExecutionEpochOneRun) markerWorkspaceSnapshot() ExecutionMarkerWorkspace {
	if run.markerWorkspace != nil {
		return run.markerWorkspace.markerSnapshot()
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.result.MarkerWorkspace
}
