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
	if control == nil || control.collector == nil && (control.launch == nil || control.launch.request.ServerEpoch < 1 || control.launch.request.ServerEpoch > 3) {
		return nil
	}
	if control.collector != nil {
		var retiredGuard func(context.Context, func(context.Context) error) error
		if st != nil {
			retiredGuard = st.WithRetiredLocalEngine
		}
		if err := dispatchadmission.BindRetiredBackupMeasurement(retiredGuard); err != nil {
			return err
		}
	}
	file, path, info, volume, err := dispatchadmission.ProductionWorkspace()
	var reports *t422WorkspaceReports
	if err == nil {
		control.workspaceBytes = custodybytes.NewBorrowed(file, path, info, volume)
		if control.collector != nil || control.launch.request.ServerEpoch == 1 {
			initial, stateErr := dispatchadmission.ProductionSemanticState()
			if stateErr == nil {
				reports, stateErr = newT422WorkspaceReports(initial)
			}
			if stateErr != nil {
				_ = control.workspaceBytes.Fail()
				return control.stop()
			}
		}
	}
	control.workspaceSample = func(ctx context.Context) (custodybytes.Sample, error) {
		// Only the closed epoch-one finish pair is newly enabled. Other early
		// descriptor profiles still admit no WB event or traversal.
		if reports == nil || control.workspaceBytes == nil || st == nil || !control.current(ctx, true) {
			if control.workspaceBytes != nil {
				_ = control.workspaceBytes.Fail()
			}
			return custodybytes.Sample{}, control.stop()
		}
		admitted := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
		if err := reports.begin(admitted); err != nil {
			_ = control.workspaceBytes.Fail()
			return custodybytes.Sample{}, control.stop()
		}
		value, err := control.workspaceBytes.SampleGuarded(ctx, admitted.Phase, st.WithQuiescentLocalEngine, func() bool {
			return control.current(ctx, true)
		})
		// Semantic checks and failure reporting happen after engine/SDK unlock.
		if err != nil {
			_ = reports.failed()
			return custodybytes.Sample{}, control.stop()
		}
		if err := reports.complete(value); err != nil {
			_ = control.workspaceBytes.Fail()
			return custodybytes.Sample{}, control.stop()
		}
		return value, nil
	}
	if control.collector == nil {
		return nil
	}
	return control.collector.SetCapacityCheckpoint(func(ctx context.Context) error {
		_, err := control.workspaceSample(ctx)
		return err
	})
}

func (control *t422LifecycleControl) workspaceByteSnapshot() custodybytes.Snapshot {
	if control == nil {
		return custodybytes.Snapshot{Unavailable: true}
	}
	return control.workspaceBytes.Snapshot()
}
