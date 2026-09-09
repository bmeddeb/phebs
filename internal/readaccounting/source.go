package readaccounting

import "context"

type sourceObserverKey struct{}

// WithSourceObserver attaches only an immutable-content attempt sink. It is
// independent of Start's HTTP ledger: nested exact-read scopes preserve it.
func WithSourceObserver(ctx context.Context, observe func() error) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(sourceObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, sourceObserverKey{}, observe), nil
}

// ObserveSource records an attempt before content submission, not success or
// bytes. The native selected boundary requires coverage and owns its sticky
// failure latch. Ordinary unbound calls perform no observation.
func ObserveSource(ctx context.Context, required bool) (err error) {
	var observe func() error
	if ctx != nil {
		observe, _ = ctx.Value(sourceObserverKey{}).(func() error)
	}
	if observe == nil {
		if required {
			return ErrScope
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = ErrEvent
		}
	}()
	if err := observe(); err != nil {
		return err
	}
	// The synchronous pipe write may have outlived the caller's deadline.
	// Preserve its reported attempt, but do not forward content afterward.
	return ctx.Err()
}
