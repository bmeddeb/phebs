package readaccounting

import "context"

type parsedBlobObserverKey struct{}

// WithParsedBlobObserver binds successful native ParsedBlobs events, not parser
// invocations. It is independent of the exact-read ledger and source attempts.
func WithParsedBlobObserver(ctx context.Context, observe func() error) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(parsedBlobObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, parsedBlobObserverKey{}, observe), nil
}

// ObserveParsedBlob is called at the existing successful observation event,
// after object validation/write, even when the caller did not request metrics.
// Ordinary unbound calls add no observation. Failure leaves the selected prefix
// unavailable; it cannot undo the native work or be reconstructed from a root.
func ObserveParsedBlob(ctx context.Context, required bool) (err error) {
	var observe func() error
	if ctx != nil {
		observe, _ = ctx.Value(parsedBlobObserverKey{}).(func() error)
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
	return ctx.Err()
}
