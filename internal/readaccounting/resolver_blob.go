package readaccounting

import "context"

type resolverBlobObserverKey struct{}

// WithResolverBlobObserver binds each native successful resolver reader return
// and its actual byte length, not failed attempts or final catalog cardinality.
func WithResolverBlobObserver(ctx context.Context, observe func(uint64) error) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(resolverBlobObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, resolverBlobObserverKey{}, observe), nil
}

func ObserveResolverBlob(ctx context.Context, required bool, bytes uint64) (err error) {
	var observe func(uint64) error
	if ctx != nil {
		observe, _ = ctx.Value(resolverBlobObserverKey{}).(func(uint64) error)
	}
	if observe == nil {
		if required {
			return ErrScope
		}
		return nil
	}
	defer func() {
		if recover() != nil {
			err = ErrEvent
		}
	}()
	// A successful native return has happened even if the caller canceled
	// during the read. Preserve that event, then refuse subsequent work.
	if err := observe(bytes); err != nil {
		return err
	}
	return ctx.Err()
}
