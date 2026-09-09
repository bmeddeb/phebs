package readaccounting

import "context"

// CacheEvent names a classified native decision, not an API call or waiter.
// Validation means the existing returned-source result admission attempt,
// including read errors; it does not assert a successful decode or insertion.
type CacheEvent byte

const (
	CacheHit              CacheEvent = 'H'
	CacheRootLoad         CacheEvent = 'R'
	CacheMemberLoad       CacheEvent = 'M'
	CacheRootValidation   CacheEvent = 'r'
	CacheMemberValidation CacheEvent = 'm'
)

type cacheObserverKey struct{}

// WithCacheObserver attaches only the fixed cache stream. The returned phase
// binds each real load to its later same-phase result admission.
func WithCacheObserver(ctx context.Context, observe func(CacheEvent, uint32) (uint32, error)) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(cacheObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, cacheObserverKey{}, observe), nil
}

func ObserveCache(ctx context.Context, required bool, event CacheEvent, phase uint32) (observedPhase uint32, err error) {
	var observe func(CacheEvent, uint32) (uint32, error)
	if ctx != nil {
		observe, _ = ctx.Value(cacheObserverKey{}).(func(CacheEvent, uint32) (uint32, error))
	}
	if observe == nil {
		if required {
			return 0, ErrScope
		}
		return 0, nil
	}
	validation := event == CacheRootValidation || event == CacheMemberValidation
	if !validation && event != CacheHit && event != CacheRootLoad && event != CacheMemberLoad || validation != (phase != 0) {
		return 0, ErrEvent
	}
	// A returned result must still be observed when its caller canceled during
	// the native read. No further source work is enabled by this observation.
	if !validation && ctx.Err() != nil {
		return 0, ctx.Err()
	}
	defer func() {
		if recover() != nil {
			observedPhase, err = 0, ErrEvent
		}
	}()
	observedPhase, err = observe(event, phase)
	if err != nil {
		return observedPhase, err
	}
	if observedPhase < 1 || observedPhase > 15 || validation && observedPhase != phase {
		return observedPhase, ErrEvent
	}
	return observedPhase, ctx.Err()
}
