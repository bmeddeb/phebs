package dispatchadmission

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// ObserveProductionSourceRead is called at the three native content submission
// boundaries, never for metadata. Missing selected coverage is sticky even if
// a worker later consumes the read error as an ordinary job failure.
func ObserveProductionSourceRead(ctx context.Context) error {
	selected := ProductionSemanticSelected()
	err := readaccounting.ObserveSource(ctx, selected)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return lifetime.client.fail(ErrProtocol)
		}
		return ErrProductionBootstrap
	}
	return err
}
