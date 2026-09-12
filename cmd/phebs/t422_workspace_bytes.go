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
		if control.collector != nil || control.launch.request.ServerEpoch >= 1 && control.launch.request.ServerEpoch <= 3 {
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
	// The HTTP caller and two fixed callbacks share the observer, reporter and
	// real engine/SDK guard. Only HTTP uses request admission; callbacks have
	// their own post-ACK, genuinely fenced-state proof.
	// Only explicitly closed early positions admit a traversal. Descriptor
	// inheritance alone does not establish phase completeness.
	sample := func(ctx context.Context, admitted dispatchadmission.ProductionSemanticSnapshot, confirm func() bool) (custodybytes.Sample, error) {
		if reports == nil || control.workspaceBytes == nil || st == nil || !confirm() {
			if control.workspaceBytes != nil {
				_ = control.workspaceBytes.Fail()
			}
			return custodybytes.Sample{}, control.stop()
		}
		if err := reports.begin(admitted); err != nil {
			_ = control.workspaceBytes.Fail()
			return custodybytes.Sample{}, control.stop()
		}
		value, err := control.workspaceBytes.SampleGuarded(ctx, admitted.Phase, st.WithQuiescentLocalEngine, confirm)
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
	control.workspaceSample = func(ctx context.Context) (custodybytes.Sample, error) {
		if ctx == nil {
			return custodybytes.Sample{}, control.stop()
		}
		admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
		return sample(ctx, admitted, func() bool { return ok && control.current(ctx, true) })
	}
	if err := dispatchadmission.BindWarmStartWorkspace(func(ctx context.Context) error {
		admitted, err := dispatchadmission.ProductionWarmStartWorkspaceState(ctx)
		control.mu.Lock()
		valid := err == nil && control.err == nil && !control.busy && control.workspacePoint == 1 &&
			control.step == 0 && control.launch.request.ServerEpoch == 1 && control.runner != nil
		if valid {
			control.busy = true
		}
		control.mu.Unlock()
		if !valid || control.runner.Park(ctx) != nil {
			return control.stop()
		}
		confirm := func() bool {
			current, err := dispatchadmission.ProductionWarmStartWorkspaceState(ctx)
			control.mu.Lock()
			valid := control.err == nil && control.busy && control.workspacePoint == 1
			control.mu.Unlock()
			return valid && err == nil && current == admitted && ctx.Err() == nil && control.ctx.Err() == nil
		}
		if _, err := sample(ctx, admitted, confirm); err != nil || !confirm() {
			return control.stop()
		}
		control.mu.Lock()
		control.busy = false // HTTP warm finish retains its independent point1.
		control.mu.Unlock()
		return nil
	}); err != nil {
		return control.stop()
	}
	if err := dispatchadmission.BindPhysicalPostAuthorWorkspace(func(ctx context.Context, reopen func(context.Context) error) error {
		admitted, err := dispatchadmission.ProductionPhysicalPostAuthorWorkspaceState(ctx)
		control.mu.Lock()
		valid := err == nil && control.err == nil && !control.busy && control.workspacePoint == 3 &&
			control.step == 0 && control.launch.request.ServerEpoch == 1 && control.runner != nil
		if valid {
			control.busy = true
		}
		control.mu.Unlock()
		if !valid || control.runner.Park(ctx) != nil {
			return control.stop()
		}
		confirm := func() bool {
			current, err := dispatchadmission.ProductionPhysicalPostAuthorWorkspaceState(ctx)
			control.mu.Lock()
			valid := control.err == nil && control.busy && control.workspacePoint == 3
			control.mu.Unlock()
			return valid && err == nil && current == admitted && ctx.Err() == nil && control.ctx.Err() == nil
		}
		// Retain S before reopening: the actual completed walk survives a
		// later failure. Only the separate R can release the parent's wait.
		if _, err := sample(ctx, admitted, confirm); err != nil {
			return control.stop()
		}
		if err := reopen(ctx); err != nil || ctx.Err() != nil || control.ctx.Err() != nil {
			_ = control.workspaceBytes.Fail()
			_ = reports.failed()
			return control.stop()
		}
		if err := reports.physicalReopenReady(); err != nil {
			_ = control.workspaceBytes.Fail()
			return control.stop()
		}
		control.mu.Lock()
		control.busy = false // HTTP physical finish retains its own point3.
		control.mu.Unlock()
		return nil
	}); err != nil {
		return control.stop()
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
