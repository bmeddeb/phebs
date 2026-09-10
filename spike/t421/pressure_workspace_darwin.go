//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// borrowWorkspace keeps the volume unavailable to Close/removal throughout
// preparation and execution. Failed preparation retains that borrow; no caller
// boolean or arbitrary cleanup callback can release populated custody.
func (v *executionPressureVolume) borrowWorkspace(ctx context.Context) (context.Context, string, error) {
	if v == nil || ctx == nil || ctx.Err() != nil {
		return ctx, "", errPressureVolume
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.ready || v.borrowed || v.flow != nil || v.check() != nil {
		return ctx, "", errPressureVolume
	}
	v.borrowed = true
	if _, err := v.samplePreparationLocked(ctx); err != nil {
		return ctx, "", err
	}
	return withExecutionPreparationParent(ctx, v.workspace.path), v.workspace.path, nil
}

// bindRehearsal is before AuthorA, not an after-the-fact teardown assertion.
// Existing constructors issue the actual input/flow objects. Bind their native
// parent and filesystem to this volume without rereading source/module trees.
func (v *executionPressureVolume) bindRehearsal(ctx context.Context, flow *ExecutionEpochOne) error {
	if v == nil || ctx == nil || ctx.Err() != nil || flow == nil || flow.epochs == nil || flow.epochs.author == nil {
		return errPressureVolume
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if !v.borrowed || v.flow != nil || flow.workspace != nil || !v.ready || v.check() != nil || v.bytes == nil || v.byteErr != nil || !v.preparationBytes.Completed || flow.closed || flow.used || flow.authored ||
		flow.controller == nil || flow.parent == nil || flow.store == nil || author.parent != v.workspace.path ||
		author.active || author.borrowedBy != nil || author.closed || author.err != nil || epochs.active || epochs.closed || epochs.err != nil ||
		len(author.roots) != 4 || len(epochs.roots) != 4 || author.request.Builds == nil || author.request.Git == nil {
		return errPressureVolume
	}
	for _, roots := range [][]productionRoot{author.roots, epochs.roots} {
		if pressureRootsUnchanged(roots...) != nil {
			return errPressureVolume
		}
		for _, root := range roots {
			if root.volume != v.workspace.volume || root.path != v.workspace.path && filepath.Dir(root.path) != v.workspace.path {
				return errPressureVolume
			}
		}
	}
	inputs := []*ExecutionInputCustody{author.request.Plan, author.request.Git.input, epochs.catalogs, epochs.configs}
	for _, tool := range []*ExecutionToolCustody{author.request.Author, flow.phebs, flow.zoekt, flow.surreal} {
		if tool == nil || tool.input == nil {
			return errPressureVolume
		}
		inputs = append(inputs, tool.input)
	}
	for _, input := range inputs {
		if !v.inputOnWorkspace(input) {
			return errPressureVolume
		}
	}
	builds := author.request.Builds
	builds.mu.Lock()
	defer builds.mu.Unlock()
	if builds.closed || builds.err != nil || builds.volume != v.workspace.volume || filepath.Dir(builds.directory) != v.workspace.path ||
		!os.SameFile(builds.parentInfo, v.workspace.info) {
		return errPressureVolume
	}
	v.flow = flow
	root := v.workspace
	flow.workspace = &root
	return nil
}

func (v *executionPressureVolume) inputOnWorkspace(input *ExecutionInputCustody) bool {
	if input == nil {
		return false
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	return !input.closed && input.err == nil && input.volume == v.workspace.volume &&
		filepath.Dir(input.directory) == v.workspace.path && os.SameFile(input.parentInfo, v.workspace.info)
}

// Preparation has no authenticated flow phase yet. Keep its actual sampled
// maximum separately; constructing a flow already advances empty SA phase one.
func (v *executionPressureVolume) samplePreparation(ctx context.Context) (custodyByteSample, error) {
	if v == nil {
		return custodyByteSample{}, errPressureVolume
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.samplePreparationLocked(ctx)
}

func (v *executionPressureVolume) samplePreparationLocked(ctx context.Context) (custodyByteSample, error) {
	if ctx == nil {
		return custodyByteSample{}, v.failBytes()
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || ctx.Err() != nil || !time.Now().Before(deadline) || !v.ready || v.flow != nil || v.byteErr != nil || v.check() != nil {
		return custodyByteSample{}, v.failBytes()
	}
	if v.bytes == nil {
		v.bytes = newCustodyByteObservation(v.workspace)
	}
	value, err := walkCustodyBytes(ctx, v.workspace)
	if err != nil || ctx.Err() != nil || v.check() != nil {
		return custodyByteSample{}, v.failBytes()
	}
	v.preparationBytes.Completed = true
	v.preparationBytes.Maximum.LogicalBytes = max(v.preparationBytes.Maximum.LogicalBytes, value.LogicalBytes)
	v.preparationBytes.Maximum.AllocatedBytes = max(v.preparationBytes.Maximum.AllocatedBytes, value.AllocatedBytes)
	return value, nil
}

// Called under volume.mu. Failure never releases a borrow or deletes custody.
func (v *executionPressureVolume) failBytes() error {
	v.byteErr = errCustodyByteObservation
	if v.bytes != nil {
		_ = v.bytes.fail()
	}
	return v.byteErr
}

func (v *executionPressureVolume) refuseBytes() (custodyByteSample, error) {
	if v == nil {
		return custodyByteSample{}, errPressureVolume
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return custodyByteSample{}, v.failBytes()
}

// sampleFlow is used only after actual AuthorA. Later live server scans need
// their owning phase/lifetime boundary and are deliberately not wired here.
func (v *executionPressureVolume) sampleFlow(ctx context.Context) (custodyByteSample, error) {
	if v == nil || ctx == nil {
		return v.refuseBytes()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.flow == nil {
		return custodyByteSample{}, v.failBytes()
	}
	flow := v.flow
	flow.mu.Lock()
	valid := !flow.closed && !flow.used && flow.authored && !flow.authorStarted.IsZero() && flow.store != nil && len(flow.plan.PhaseDeadlines) >= 2
	started := flow.authorStarted
	var deadline time.Time
	if valid && !started.IsZero() {
		deadline = started.Add(time.Duration(flow.plan.PhaseDeadlines[1].DeadlineMS) * time.Millisecond)
	}
	flow.mu.Unlock()
	if !valid {
		return custodyByteSample{}, v.failBytes()
	}
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	return v.sampleControllerLocked(ctx, 2, func() bool { return v.sampleBoundary(nil, started) })
}

// Sample only after the actual final run joins, before input/volume closure.
// The caller cannot choose a phase or renew the already-running deadline.
func (v *executionPressureVolume) sampleJoined(ctx context.Context, run *ExecutionEpochOneRun) (custodyByteSample, error) {
	if v == nil || ctx == nil || run == nil || run.done == nil {
		return v.refuseBytes()
	}
	select {
	case <-run.done:
	default:
		return v.refuseBytes()
	}
	result, err := run.Wait(ctx)
	if err != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty {
		return v.refuseBytes()
	}
	run.mu.Lock()
	deadline := run.lifetimeDeadline
	if !run.phaseDeadline.IsZero() && (deadline.IsZero() || run.phaseDeadline.Before(deadline)) {
		deadline = run.phaseDeadline
	}
	run.mu.Unlock()
	if deadline.IsZero() {
		return v.refuseBytes()
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.flow == nil || run.flow != v.flow {
		return custodyByteSample{}, v.failBytes()
	}
	return v.sampleControllerLocked(ctx, result.Store.Store.Phase, func() bool { return v.sampleBoundary(run, time.Time{}) })
}

// Inspect the actual owning boundary before and after traversal, including
// same-phase successors which a controller phase comparison cannot detect.
// Lock order matches flow.Close; none of these locks spans filesystem I/O.
func (v *executionPressureVolume) sampleBoundary(run *ExecutionEpochOneRun, started time.Time) bool {
	flow := v.flow
	if flow == nil || flow.epochs == nil || flow.epochs.author == nil {
		return false
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if flow.closed || author.closed || author.err != nil || author.active || epochs.closed || epochs.err != nil {
		return false
	}
	if run == nil {
		return !flow.used && flow.authored && !started.IsZero() && flow.authorStarted.Equal(started) &&
			epochs.released == 0 && !epochs.active && author.borrowedBy == nil && flow.retained == nil
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.flow != flow || run.returnStarting || run.err != nil || run.epoch.Epoch == 0 || epochs.released != run.epoch.Epoch ||
		!run.result.RootJoined || !run.result.SessionEmpty {
		return false
	}
	if epochs.active {
		return flow.retained == run && author.borrowedBy == run
	}
	return author.borrowedBy == nil && (flow.retained == nil || flow.retained == run)
}

// Volume ownership excludes removal throughout the scan. The before/after SA
// snapshots bind monotonic controller phase; no flow/run/SDK/lifecycle lock is
// held across traversal. This proves only these sampled boundary points.
func (v *executionPressureVolume) sampleControllerLocked(ctx context.Context, required uint32, boundary func() bool) (custodyByteSample, error) {
	if v.byteErr != nil || v.bytes == nil || !v.borrowed || !v.ready || v.flow == nil || v.flow.store == nil || boundary == nil || !boundary() || v.check() != nil {
		return custodyByteSample{}, v.failBytes()
	}
	before, err := v.flow.store.Snapshot()
	if err != nil || before.Store.Phase != required {
		return custodyByteSample{}, v.failBytes()
	}
	value, err := v.bytes.sample(ctx, before.Store.Phase, func() bool {
		after, err := v.flow.store.Snapshot()
		return err == nil && after.Store.Phase == before.Store.Phase && boundary() && v.check() == nil
	})
	if err != nil {
		return custodyByteSample{}, v.failBytes()
	}
	return value, nil
}

func (v *executionPressureVolume) byteSnapshot() (custodyBytePhase, custodyByteSnapshot) {
	if v == nil {
		return custodyBytePhase{}, custodyByteSnapshot{Unavailable: true}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.preparationBytes, v.bytes.Snapshot()
}

// finishRehearsal requires the actual joined successful final run and its
// bound flow, then closes every existing input owner and reacquires the native
// source lease. It never promotes a failed run into permission to delete it.
// This scoped rehearsal release is not full launcher accounting/admission.
func (v *executionPressureVolume) finishRehearsal(ctx context.Context, run *ExecutionEpochOneRun) error {
	if v == nil || ctx == nil || ctx.Err() != nil || run == nil {
		return errPressureVolume
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ballast != nil && !v.ballast.removed {
		return errPressureVolume
	}
	if !v.borrowed || v.flow == nil || run.flow != v.flow || v.check() != nil || v.byteErr != nil || v.bytes == nil || v.bytes.Snapshot().Unavailable {
		return errPressureVolume
	}
	select {
	case <-run.done:
	default:
		return errPressureVolume
	}
	result, err := run.Wait(ctx)
	if err != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || !run.healthy {
		return errPressureVolume
	}
	flow := v.flow
	epochs, author := flow.epochs, flow.epochs.author
	epochs.mu.Lock()
	latest := epochs.released == run.epoch.Epoch
	epochs.mu.Unlock()
	if !latest || flow.Close() != nil || epochs.Close() != nil || author.Close() != nil || author.request.Plan.Close() != nil {
		return errPressureVolume
	}
	for _, tool := range []*ExecutionToolCustody{author.request.Author, flow.phebs, flow.zoekt, flow.surreal} {
		if tool.Close() != nil {
			return errPressureVolume
		}
	}
	if author.request.Builds.Close() != nil || author.request.Git.Close() != nil {
		return errPressureVolume
	}
	lease, err := acquireProductionSourceLease(v.workspace.path)
	if err != nil {
		return errPressureVolume
	}
	info, statErr := lease.Stat()
	closeErr := lease.Close() // No mounted lease/owner descriptor may block detach.
	if statErr != nil || closeErr != nil || !inputCustodySame(author.leaseInfo, info) || ctx.Err() != nil {
		return errPressureVolume
	}
	v.borrowed = false
	return v.remove(ctx, false)
}
