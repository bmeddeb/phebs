package dispatchadmission

import (
	"context"
	"errors"
	"net"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// The seven fixed store lifetimes exclude the launcher and the three authors.
// This is closed mechanical routing, not executable/input or row admission.
func productionStorePhases(producer uint32) uint16 {
	switch producer {
	case 2:
		return 14 // phases 2–4
	case 3:
		return 16 // phase 5
	case 4:
		return 224 // phases 6–8
	case 5:
		return 1920 // phases 8–11
	case 6:
		return 14336 // phases 12–14
	case 10, 11:
		return 2048 // phase 12: backup or restore
	default:
		return 0
	}
}

func (record ProductionBootstrap) validateStore() error {
	config := record.Store
	if config == nil {
		return nil
	}
	mask := productionStorePhases(record.Producer.ID)
	if record.Program != ProgramPhebs || mask == 0 || config.Producer != record.Producer.ID ||
		config.Binding == ([32]byte{}) || config.Phase != record.Phase || config.Phases != mask ||
		config.Phase == 0 || config.Phase > storeaccounting.MaximumPhases || mask&(1<<(config.Phase-1)) == 0 ||
		config.AckTimeout <= 0 || config.WireBytes < 4*2*storeaccounting.FrameBytes {
		return ErrProductionBootstrap
	}
	calls, transactions := 1, 1
	if config.Producer <= 6 {
		calls, transactions = storeaccounting.MaximumCalls, storeaccounting.MaximumTransactions
		if record.SemanticMode != ProductionSemanticV3 || !record.Control.OwnerControl {
			return ErrProductionBootstrap
		}
	} else if record.SemanticMode != "" || record.Control.OwnerControl {
		return ErrProductionBootstrap
	}
	if config.Calls != calls || config.Transactions != transactions {
		return ErrProductionBootstrap
	}
	var actual uint16
	var previous uint32
	for _, phase := range record.Control.Phases {
		if phase <= previous || phase > storeaccounting.MaximumPhases {
			return ErrProductionBootstrap
		}
		actual |= 1 << (phase - 1)
		previous = phase
	}
	if actual != mask {
		return ErrProductionBootstrap
	}
	return nil
}

// Attach uses the bootstrap deadline, while the retained client lives under
// the admitted producer context. Stopping/joining this temporary callback is
// essential: returning from bootstrap must not cancel the installed SA client.
func bootstrapStoreClient(ctx, lifetime context.Context, conn *net.UnixConn, config storeaccounting.ClientConfig) (*storeaccounting.Client, context.CancelFunc, error) {
	file, err := conn.File()
	if err != nil {
		return nil, nil, ErrProductionBootstrap
	}
	storeCtx, cancel := context.WithCancel(lifetime)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { cancel(); close(done) })
	client, err := storeaccounting.NewClient(storeCtx, file, config)
	if !stop() {
		<-done
	}
	if err != nil || ctx.Err() != nil {
		cancel()
		if client != nil {
			_ = client.Close(ctx)
		}
		return nil, nil, ErrProductionBootstrap
	}
	return client, cancel, nil
}

// TakeStoreOwner constructs and transfers the concrete ALL-call owner exactly
// once from this lifetime's authenticated client. No caller supplies a client,
// replacement owner, callback, or claimed drainage summary.
func (lifetime *ProductionLifetime) TakeStoreOwner() (*storeaccounting.SDKOwner, error) {
	if lifetime == nil || lifetime.storeClient == nil {
		return nil, nil
	}
	lifetime.storeMu.Lock()
	defer lifetime.storeMu.Unlock()
	if lifetime.storeTaken || lifetime.storeClosed || lifetime.client.Context().Err() != nil || lifetime.storeClient.Context().Err() != nil {
		return nil, ErrProductionBootstrap
	}
	owner, err := storeaccounting.NewSDKOwner(lifetime.storeClient)
	if err != nil {
		return nil, errors.Join(ErrProductionBootstrap, err)
	}
	lifetime.storeOwner, lifetime.storeTaken = owner, true
	return owner, nil
}

// ProcessStoreOwner returns only the owner already taken from authenticated
// bootstrap. Factories may wrap this exact pointer but cannot install or replace
// it. Ordinary and legacy process lifetimes retain their nil-owner path.
func ProcessStoreOwner() (*storeaccounting.SDKOwner, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.storeClient == nil {
		return nil, nil
	}
	lifetime.storeMu.Lock()
	owner := lifetime.storeOwner
	available := lifetime.storeTaken && !lifetime.storeClosed && owner != nil
	lifetime.storeMu.Unlock()
	if !available || lifetime.client == nil || lifetime.storeClient.Context().Err() != nil {
		return nil, ErrProductionBootstrap
	}
	lifetime.client.mu.Lock()
	available = !lifetime.client.closed && lifetime.client.err == nil && lifetime.client.ctx.Err() == nil
	lifetime.client.mu.Unlock()
	if !available {
		return nil, ErrProductionBootstrap
	}
	return owner, nil
}

func (lifetime *ProductionLifetime) checkpointStore(ctx context.Context) error {
	lifetime.storeMu.Lock()
	owner, closed := lifetime.storeOwner, lifetime.storeClosed
	lifetime.storeMu.Unlock()
	if owner == nil || closed {
		return ErrProductionBootstrap
	}
	// Do not hold the dispatch or lifetime state mutex over SA01 I/O.
	return owner.Checkpoint(ctx)
}

func (lifetime *ProductionLifetime) resumeStore(phase uint32) error {
	lifetime.storeMu.Lock()
	owner, closed := lifetime.storeOwner, lifetime.storeClosed
	lifetime.storeMu.Unlock()
	if owner == nil || closed {
		return ErrProductionBootstrap
	}
	return owner.Resume(phase)
}

func (lifetime *ProductionLifetime) closeStore(ctx context.Context) error {
	if lifetime.storeClient == nil {
		return nil
	}
	lifetime.storeMu.Lock()
	lifetime.storeClosed = true
	client, owner := lifetime.storeClient, lifetime.storeOwner
	lifetime.storeMu.Unlock()
	defer lifetime.cancelStore()
	// Main must return from joined service/store cleanup before this point.
	// This actual ALL-call close is not proof of native connection/engine join.
	if owner == nil {
		return errors.Join(ErrProductionBootstrap, client.Fail(ctx, storeaccounting.ErrIncomplete))
	}
	return owner.Close(ctx)
}
