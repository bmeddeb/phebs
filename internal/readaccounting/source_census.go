package readaccounting

import "context"

type SourceCensusEvent byte

const (
	SourceCensusBegin    SourceCensusEvent = 'B'
	SourceCensusBatch    SourceCensusEvent = 'D'
	SourceCensusEnd      SourceCensusEvent = 'E'
	SourceCensusComplete SourceCensusEvent = 'C'
)

type sourceCensusObserverKey struct{}

func WithSourceCensusObserver(ctx context.Context, observe func(SourceCensusEvent, uint32, uint64, uint64) (uint32, error)) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(sourceCensusObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, sourceCensusObserverKey{}, observe), nil
}

// Begin captures the native phase. Batches retain classified-owner bytes even
// when later work fails; End closes coverage without success. Complete replaces
// End only after a successful source census; its first payload is the actual
// regular-owner count (including zero), and its second payload must be zero.
// This is not source publication or a whole-phase physical-pass verdict.
func ObserveSourceCensus(ctx context.Context, required bool, event SourceCensusEvent, phase uint32, logical, unique uint64) (observed uint32, err error) {
	var observe func(SourceCensusEvent, uint32, uint64, uint64) (uint32, error)
	if ctx != nil {
		observe, _ = ctx.Value(sourceCensusObserverKey{}).(func(SourceCensusEvent, uint32, uint64, uint64) (uint32, error))
	}
	if observe == nil {
		if required {
			return 0, ErrScope
		}
		return 0, nil
	}
	if event != SourceCensusBegin && event != SourceCensusBatch && event != SourceCensusEnd && event != SourceCensusComplete ||
		(event == SourceCensusBegin) != (phase == 0) ||
		event == SourceCensusBatch && unique > logical ||
		event == SourceCensusComplete && unique != 0 ||
		event != SourceCensusBatch && event != SourceCensusComplete && (logical != 0 || unique != 0) {
		return 0, ErrEvent
	}
	if (event == SourceCensusBegin || event == SourceCensusComplete) && ctx.Err() != nil {
		return 0, ctx.Err()
	}
	defer func() {
		if recover() != nil {
			observed, err = 0, ErrEvent
		}
	}()
	observed, err = observe(event, phase, logical, unique)
	if err != nil {
		return observed, err
	}
	if observed < 1 || observed > 15 || event != SourceCensusBegin && phase != observed {
		return observed, ErrEvent
	}
	return observed, ctx.Err()
}
