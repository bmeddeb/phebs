package dispatchadmission

import (
	"context"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func ObserveProductionCatalogCensus(ctx context.Context, event readaccounting.CatalogCensusEvent, phase uint32, records uint64) (uint32, error) {
	selected := ProductionSemanticSelected()
	observed, err := readaccounting.ObserveCatalogCensus(ctx, selected, event, phase, records)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return observed, lifetime.client.fail(ErrProtocol)
		}
		return observed, ErrProductionBootstrap
	}
	return observed, err
}
