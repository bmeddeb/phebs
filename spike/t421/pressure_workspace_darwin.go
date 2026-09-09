//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
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
	if !v.borrowed || v.flow != nil || !v.ready || v.check() != nil || flow.closed || flow.used || flow.authored ||
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
	if !v.borrowed || v.flow == nil || run.flow != v.flow || v.check() != nil {
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
