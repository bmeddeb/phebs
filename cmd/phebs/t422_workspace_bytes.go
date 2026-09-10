package main

import (
	"context"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

// Bind before the runner starts. Legacy selected startup/park can lack a
// workspace, but requesting a measured capacity checkpoint cannot fall back
// to data-only bytes or a zero observation.
func (control *t422LifecycleControl) bindWorkspaceBytes(st *store.Surreal) error {
	if control == nil || control.collector == nil {
		return nil
	}
	file, path, info, volume, err := dispatchadmission.ProductionWorkspace()
	var reports *t422WorkspaceReports
	if err == nil {
		control.workspaceBytes = custodybytes.NewBorrowed(file, path, info, volume)
		initial, stateErr := dispatchadmission.ProductionSemanticState()
		if stateErr == nil {
			reports, stateErr = newT422WorkspaceReports(initial)
		}
		if stateErr != nil {
			_ = control.workspaceBytes.Fail()
			return control.stop()
		}
	}
	return control.collector.SetCapacityCheckpoint(func(ctx context.Context) error {
		if control.workspaceBytes == nil || st == nil || !control.current(ctx, true) {
			if control.workspaceBytes != nil {
				_ = control.workspaceBytes.Fail()
			}
			return control.stop()
		}
		admitted := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
		if err := reports.begin(admitted); err != nil {
			_ = control.workspaceBytes.Fail()
			return control.stop()
		}
		value, err := control.workspaceBytes.SampleGuarded(ctx, admitted.Phase, st.WithQuiescentLocalEngine, func() bool {
			return control.current(ctx, true)
		})
		// Semantic checks and failure reporting happen after engine/SDK unlock.
		if err != nil {
			_ = reports.failed()
			return control.stop()
		}
		if err := reports.complete(value); err != nil {
			_ = control.workspaceBytes.Fail()
			return control.stop()
		}
		return nil
	})
}

func (control *t422LifecycleControl) workspaceByteSnapshot() custodybytes.Snapshot {
	if control == nil {
		return custodybytes.Snapshot{Unavailable: true}
	}
	return control.workspaceBytes.Snapshot()
}
