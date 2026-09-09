package dispatchadmission

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// ObserveProductionResolverBlob makes unavailable selected native-return
// coverage sticky without relabeling failed reads or changing their result.
func ObserveProductionResolverBlob(ctx context.Context, bytes uint64) error {
	selected := ProductionSemanticSelected()
	err := readaccounting.ObserveResolverBlob(ctx, selected, bytes)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return lifetime.client.fail(ErrProtocol)
		}
		return ErrProductionBootstrap
	}
	return err
}
