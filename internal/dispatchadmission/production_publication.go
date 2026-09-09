package dispatchadmission

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// ObserveProductionPublication keeps refused selected coverage sticky even
// when a worker would otherwise consume the publication error as a job result.
func ObserveProductionPublication(ctx context.Context) error {
	selected := ProductionSemanticSelected()
	err := readaccounting.ObservePublication(ctx, selected)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return lifetime.client.fail(ErrProtocol)
		}
		return ErrProductionBootstrap
	}
	return err
}
