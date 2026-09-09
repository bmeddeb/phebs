package dispatchadmission

import (
	"context"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func ObserveProductionSourceCensus(ctx context.Context, event readaccounting.SourceCensusEvent, phase uint32, logical, unique uint64) (uint32, error) {
	selected := ProductionSemanticSelected()
	observed, err := readaccounting.ObserveSourceCensus(ctx, selected, event, phase, logical, unique)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return observed, lifetime.client.fail(ErrProtocol)
		}
		return observed, ErrProductionBootstrap
	}
	return observed, err
}
