package dispatchadmission

import (
	"context"
	"sync"
	"time"
)

// One selected receiver-owned callback, not an ordinary owner or a request.
// No extra goroutine, control pair, SDK operation, or descriptor is created.
type warmStartWorkspace struct {
	mu       sync.Mutex
	callback func(context.Context) error
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
}

// BindWarmStartWorkspace installs only the authenticated full producer-two
// profile's fixed callback. Omitted profiles retain the nil/no-op path.
func BindWarmStartWorkspace(callback func(context.Context) error) error {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.warmWorkspace == nil {
		return nil
	}
	w := lifetime.warmWorkspace
	w.mu.Lock()
	valid := !w.closed && w.callback == nil && w.done == nil && callback != nil
	if valid {
		w.callback = callback
	}
	w.mu.Unlock()
	if !valid {
		return lifetime.client.fail(ErrProtocol)
	}
	return nil
}

// ProductionWarmStartWorkspaceState is available only inside the actual
// post-ACK callback context, with BOTH admission classes genuinely fenced.
// It is not an HTTP token, and cannot authorize an ordinary request.
func ProductionWarmStartWorkspaceState(ctx context.Context) (ProductionSemanticSnapshot, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	return lifetime.warmStartWorkspaceState(ctx)
}

func (lifetime *ProductionLifetime) warmStartWorkspaceState(ctx context.Context) (ProductionSemanticSnapshot, error) {
	if ctx == nil || ctx.Err() != nil || lifetime.warmWorkspace == nil {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	w := lifetime.warmWorkspace
	w.mu.Lock()
	valid := !w.closed && w.ctx == ctx && w.done != nil
	w.mu.Unlock()
	client := lifetime.client
	client.mu.Lock()
	defer client.mu.Unlock()
	valid = valid && lifetime.program == ProgramPhebs && lifetime.semanticMode == ProductionSemanticV3 &&
		lifetime.producerID == 2 && client.phase == 3 && !client.closed && client.err == nil &&
		client.ctx.Err() == nil && client.ownersRequired && !client.ownerRequestsOpen
	owners := client.owners
	if owners == nil {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	owners.mu.Lock()
	valid = valid && owners.err == nil && owners.ctx.Err() == nil && owners.paused &&
		owners.pausedReady && owners.active == 0 && owners.requestsFenced && owners.requestsReady && owners.requests == 0
	owners.mu.Unlock()
	if !valid {
		return ProductionSemanticSnapshot{}, ErrProductionBootstrap
	}
	return ProductionSemanticSnapshot{Mode: lifetime.semanticMode, InputSHA256: lifetime.inputSHA256,
		ProducerID: 2, Phase: 3, RequestSequence: client.ownerRequestSequence, OrdinaryOwnersDrained: true}, nil
}

func (lifetime *ProductionLifetime) runWarmStartWorkspace(ctx context.Context, nanos int64) (retErr error) {
	w := lifetime.warmWorkspace
	if w == nil || ctx == nil || ctx.Err() != nil || nanos <= 0 || !time.Now().Before(time.Unix(0, nanos)) {
		return ErrProtocol
	}
	// Inherited lifetime cancellation/deadline clips the exact parent instant.
	// The already-canceled per-frame opCtx is deliberately not used here.
	w.mu.Lock()
	if w.closed || w.done != nil || w.callback == nil {
		w.mu.Unlock()
		return ErrProtocol
	}
	operation, cancel := context.WithDeadline(ctx, time.Unix(0, nanos))
	w.ctx, w.cancel, w.done = operation, cancel, make(chan struct{})
	callback, done := w.callback, w.done
	w.mu.Unlock()
	defer func() {
		cancel()
		if recover() != nil {
			retErr = ErrPanic
		}
		close(done)
	}()
	if _, err := lifetime.warmStartWorkspaceState(operation); err != nil {
		return err
	}
	if err := callback(operation); err != nil {
		return err
	}
	if _, err := lifetime.warmStartWorkspaceState(operation); err != nil {
		return err
	}
	return operation.Err()
}

func (lifetime *ProductionLifetime) closeWarmStartWorkspace(ctx context.Context) error {
	w := lifetime.warmWorkspace
	if w == nil {
		return nil
	}
	w.mu.Lock()
	w.closed = true
	done := w.done
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ErrCanceled
	}
}
