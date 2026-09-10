package storeaccounting

import "context"

// WithIdle excludes new SDK calls while a synchronous local measurement runs.
// It is not an accounting checkpoint: phase, counters and wire state do not
// change. The callback must not call this owner, the SDK, or an observation
// sink which inspects this owner. Busy work refuses; it is never drained here.
func (owner *SDKOwner) WithIdle(ctx context.Context, measure func() error) error {
	if owner == nil || owner.client == nil || ctx == nil || measure == nil {
		return ErrConfig
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner.err != nil {
		return owner.err
	}
	if owner.client.Context().Err() != nil {
		return ErrCanceled
	}
	if owner.fenced {
		return ErrFenced
	}
	for _, call := range owner.calls {
		if call != nil {
			return ErrBusy
		}
	}
	for _, tx := range owner.transactions {
		if tx.used {
			return ErrBusy
		}
	}
	if err := measure(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner.client.Context().Err() != nil {
		return ErrCanceled
	}
	return nil
}
