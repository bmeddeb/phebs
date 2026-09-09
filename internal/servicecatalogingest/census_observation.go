package servicecatalogingest

import (
	"context"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

type catalogCensusObservation struct {
	phase   uint32
	failed  bool
	started bool
}

func beginCatalogCensus(ctx context.Context) (*catalogCensusObservation, error) {
	phase, err := dispatchadmission.ObserveProductionCatalogCensus(ctx, readaccounting.CatalogCensusBegin, 0, 0)
	if err != nil || phase == 0 {
		return nil, err
	}
	return &catalogCensusObservation{phase: phase}, nil
}

func (observation *catalogCensusObservation) child(ctx context.Context) error {
	if observation == nil {
		return nil
	}
	_, err := dispatchadmission.ObserveProductionCatalogCensus(ctx, readaccounting.CatalogCensusChild, observation.phase, 0)
	observation.failed = err != nil
	observation.started = err == nil
	return err
}

func (observation *catalogCensusObservation) finish(ctx context.Context, records int) error {
	if observation == nil {
		return nil
	}
	if observation.failed {
		return readaccounting.ErrEvent
	}
	if records < 0 || records > 0 && !observation.started {
		// Deliberately invalid: the closed observer rejects before invoking the
		// sink and selected dispatch latches missing measurement coverage.
		_, err := dispatchadmission.ObserveProductionCatalogCensus(ctx, 0, observation.phase, 0)
		return err
	}
	if records > 0 {
		_, err := dispatchadmission.ObserveProductionCatalogCensus(ctx, readaccounting.CatalogCensusRecords, observation.phase, uint64(records))
		return err
	}
	event := readaccounting.CatalogCensusNoChild
	if observation.started {
		event = readaccounting.CatalogCensusEnd
	}
	_, err := dispatchadmission.ObserveProductionCatalogCensus(ctx, event, observation.phase, 0)
	return err
}
