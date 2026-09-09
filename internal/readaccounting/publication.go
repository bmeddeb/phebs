package readaccounting

import "context"

type publicationObserverKey struct{}

// WithPublicationObserver binds actual extraction StorePublisher.PublishDomain
// invocation attempts, independently of exact-read ledgers or source reads.
func WithPublicationObserver(ctx context.Context, observe func() error) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(publicationObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, publicationObserverKey{}, observe), nil
}

// ObservePublication precedes validation and native publication work. A
// successful event retains the attempt even if that work later fails.
func ObservePublication(ctx context.Context, required bool) (err error) {
	var observe func() error
	if ctx != nil {
		observe, _ = ctx.Value(publicationObserverKey{}).(func() error)
	}
	if observe == nil {
		if required {
			return ErrScope
		}
		return nil
	}
	// This invocation has already entered StorePublisher, even if its caller
	// canceled first. Observe that actual attempt before refusing native work.
	defer func() {
		if recover() != nil {
			err = ErrEvent
		}
	}()
	if err := observe(); err != nil {
		return err
	}
	// Retain the entered attempt, but do not proceed to publication after
	// cancellation before or during its synchronous report.
	return ctx.Err()
}
