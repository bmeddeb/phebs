package dispatchadmission

import (
	"context"
	"sync"
	"time"
)

// BindPhysicalPostAuthorWorkspace selects only the full producer-two FD6
// recipe's one phase-four continuation. reopen must be invoked exactly once,
// after the real guarded traversal. Its actual S remains retained on failure;
// only a separate post-reopen R releases the parent's wait.
func BindPhysicalPostAuthorWorkspace(callback func(context.Context, func(context.Context) error) error) error {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.physicalWorkspace == nil {
		return nil
	}
	w := lifetime.physicalWorkspace
	w.mu.Lock()
	valid := !w.closed && w.physical == nil && w.done == nil && callback != nil
	if valid {
		w.physical = callback
	}
	w.mu.Unlock()
	if !valid {
		return lifetime.client.fail(ErrProtocol)
	}
	return nil
}

// ProductionPhysicalPostAuthorWorkspaceState proves the actual callback still
// owns both fences; it never supplies an HTTP token or ordinary work permission.
func ProductionPhysicalPostAuthorWorkspaceState(ctx context.Context) (ProductionSemanticSnapshot, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	return lifetime.fencedWorkspaceState(ctx, lifetime.physicalWorkspace, 4)
}

// The receiver already echoed this selected Reopen frame within its ordinary
// timeout. The original phase deadline now owns the continuation; the actual
// fences remain closed until the callback invokes the real reopen.
func (lifetime *ProductionLifetime) runPhysicalPostAuthorWorkspace(ctx context.Context, frame phaseControlFrame) (retErr error) {
	w := lifetime.physicalWorkspace
	if w == nil || ctx == nil || ctx.Err() != nil || frame.op != phaseOwnersReopen || frame.phase != 4 ||
		frame.deadlineUnixNano <= 0 || !time.Now().Before(time.Unix(0, frame.deadlineUnixNano)) {
		return ErrProtocol
	}
	w.mu.Lock()
	if w.closed || w.done != nil || w.physical == nil {
		w.mu.Unlock()
		return ErrProtocol
	}
	operation, cancel := context.WithDeadline(ctx, time.Unix(0, frame.deadlineUnixNano))
	w.ctx, w.cancel, w.done = operation, cancel, make(chan struct{})
	callback, done := w.physical, w.done
	w.mu.Unlock()
	// This lock covers only the reopen call, never the traversal. It also
	// joins an in-flight escaped reopen before marking the callback joined.
	var mu sync.Mutex
	var called, returned bool
	var reopenErr error
	defer func() {
		cancel()
		mu.Lock()
		returned = true
		mu.Unlock()
		if recover() != nil {
			retErr = ErrPanic
		}
		close(done)
	}()
	reopen := func(call context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if returned || called || call != operation {
			reopenErr = ErrProtocol
			return reopenErr
		}
		called = true
		if _, reopenErr = lifetime.fencedWorkspaceState(call, w, 4); reopenErr == nil {
			reopenErr = lifetime.client.controlOwners(call, frame)
		}
		if reopenErr == nil && !lifetime.physicalWorkspaceReopened(call, frame) {
			reopenErr = ErrProtocol
		}
		return reopenErr
	}
	if _, err := lifetime.fencedWorkspaceState(operation, w, 4); err != nil {
		return err
	}
	if err := callback(operation, reopen); err != nil {
		return err
	}
	mu.Lock()
	valid := called && reopenErr == nil
	returned = true
	mu.Unlock()
	if !valid {
		return ErrProtocol
	}
	return operation.Err()
}

func (lifetime *ProductionLifetime) closePhysicalPostAuthorWorkspace(ctx context.Context) error {
	return lifetime.physicalWorkspace.close(ctx)
}

// Reopening permits real workers to enter immediately, so active counts are
// deliberately not required to stay zero. Check the actual admission state,
// exact request sequence, callback lifetime and cancellation, not work totals.
func (lifetime *ProductionLifetime) physicalWorkspaceReopened(ctx context.Context, frame phaseControlFrame) bool {
	w := lifetime.physicalWorkspace
	w.mu.Lock()
	valid := !w.closed && w.ctx == ctx && ctx.Err() == nil
	w.mu.Unlock()
	client := lifetime.client
	client.mu.Lock()
	defer client.mu.Unlock()
	valid = valid && client.phase == 4 && !client.closed && client.err == nil &&
		client.ctx.Err() == nil && client.ownerRequestsOpen && client.ownerRequestSequence == frame.sequence
	owners := client.owners
	if owners == nil {
		return false
	}
	owners.mu.Lock()
	valid = valid && owners.err == nil && owners.ctx.Err() == nil && !owners.paused &&
		!owners.pausedReady && !owners.requestsFenced && !owners.requestsReady
	owners.mu.Unlock()
	return valid
}
