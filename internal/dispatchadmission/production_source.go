package dispatchadmission

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// ObserveProductionSourceRead is called at the three native content submission
// boundaries, never for metadata. Missing selected coverage is sticky even if
// a worker later consumes the read error as an ordinary job failure.
func ObserveProductionSourceRead(ctx context.Context) error {
	selected := ProductionWorkSelected()
	err := readaccounting.ObserveSource(ctx, selected)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return lifetime.client.fail(ErrProtocol)
		}
		return ErrProductionBootstrap
	}
	return err
}

// ObserveProductionCache keeps missing selected coverage sticky, including a
// cache error which its product caller subsequently converts to unavailable.
func ObserveProductionCache(ctx context.Context, event readaccounting.CacheEvent, phase uint32) (uint32, error) {
	selected := ProductionWorkSelected()
	observed, err := readaccounting.ObserveCache(ctx, selected, event, phase)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return observed, lifetime.client.fail(ErrProtocol)
		}
		return observed, ErrProductionBootstrap
	}
	return observed, err
}
