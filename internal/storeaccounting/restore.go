package storeaccounting

import (
	"context"
	"errors"
)

// RestoreReplayWrite accounts one complete source-owned native HTTP transaction.
// The caller constructs and bounds the request before calling, and submit must
// encompass the actual HTTP submission, strict write/COMMIT result validation,
// and joined request/response body closure. It must neither retry nor redirect.
// A successful callback is the only settled outcome; every error is terminal.
// This shares the producer's ALL-call owner with later SDK schema/repair work.
func (owner *SDKOwner) RestoreReplayWrite(ctx context.Context, rows uint64, submit func(context.Context) error) (err error) {
	if owner == nil || owner.client == nil {
		return ErrConfig
	}
	if rows == 0 || rows > MaximumRows || submit == nil {
		return owner.fail(ctx, ErrDescriptor)
	}
	call, err := owner.acquire(ctx, ImplicitWrite, nil)
	if err != nil {
		return err
	}
	ctx, finishContext := owner.callContext(ctx)
	defer finishContext()
	defer func() {
		if recover() != nil {
			err = owner.fail(ctx, ErrProtocol)
			return
		}
		if err != nil {
			err = errors.Join(err, owner.fail(ctx, ErrTransport))
			return
		}
		if ctx.Err() != nil {
			err = owner.fail(ctx, ErrCanceled)
			return
		}
		err = call.finish(ctx, nil, nil)
	}()
	call.submission, err = owner.client.Submit(ctx, ImplicitWrite, 0, rows)
	if err != nil {
		return err
	}
	call.consumed = true
	err = submit(ctx)
	call.replied = err == nil
	return err
}
