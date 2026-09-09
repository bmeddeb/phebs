package readaccounting

import "context"

type CatalogCensusEvent byte

const (
	CatalogCensusBegin   CatalogCensusEvent = 'B'
	CatalogCensusChild   CatalogCensusEvent = 'S'
	CatalogCensusRecords CatalogCensusEvent = 'D'
	CatalogCensusEnd     CatalogCensusEvent = 'E'
	CatalogCensusNoChild CatalogCensusEvent = 'N'
)

type catalogCensusObserverKey struct{}

func WithCatalogCensusObserver(ctx context.Context, observe func(CatalogCensusEvent, uint32, uint64) (uint32, error)) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(catalogCensusObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, catalogCensusObserverKey{}, observe), nil
}

// Begin captures phase; Records/End close a positive/empty child's coverage,
// and NoChild closes an invocation that never started a child, not success.
// Result events can retain real work before returning caller cancellation.
func ObserveCatalogCensus(ctx context.Context, required bool, event CatalogCensusEvent, phase uint32, records uint64) (observed uint32, err error) {
	var observe func(CatalogCensusEvent, uint32, uint64) (uint32, error)
	if ctx != nil {
		observe, _ = ctx.Value(catalogCensusObserverKey{}).(func(CatalogCensusEvent, uint32, uint64) (uint32, error))
	}
	if observe == nil {
		if required {
			return 0, ErrScope
		}
		return 0, nil
	}
	if event != CatalogCensusBegin && event != CatalogCensusChild && event != CatalogCensusRecords && event != CatalogCensusEnd && event != CatalogCensusNoChild ||
		(event == CatalogCensusBegin) != (phase == 0) || (event == CatalogCensusRecords) != (records > 0) {
		return 0, ErrEvent
	}
	if event == CatalogCensusBegin && ctx.Err() != nil {
		return 0, ctx.Err()
	}
	defer func() {
		if recover() != nil {
			observed, err = 0, ErrEvent
		}
	}()
	observed, err = observe(event, phase, records)
	if err != nil {
		return observed, err
	}
	if observed < 1 || observed > 15 || event != CatalogCensusBegin && phase != observed {
		return observed, ErrEvent
	}
	return observed, ctx.Err()
}
