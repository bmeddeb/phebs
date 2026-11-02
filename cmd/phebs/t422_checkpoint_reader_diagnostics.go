package main

import (
	"context"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// Private first reader failure: no raw error, row, ID, token, query or context
// survives. Logging uses the existing first-refusal call after cancellation.
type t422CheckpointReaderFailure struct {
	Present bool
	Side    readaccounting.TransitionReadSide
	Stage   readaccounting.TransitionReadStage
	Cause   string
	Flags   readaccounting.TransitionReadFailureFlags
}

func (control *t422CheckpointRecoveryControl) recordReaderFailure(side readaccounting.TransitionReadSide, stage readaccounting.TransitionReadStage, err error, flags readaccounting.TransitionReadFailureFlags) {
	if err == nil {
		return
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.readerFailure != nil || control.err != nil {
		return
	}
	control.readerFailure = &t422CheckpointReaderFailure{Present: true, Side: side, Stage: stage, Cause: t422RecoveryCause(err), Flags: flags}
}
func (control *t422CheckpointRecoveryControl) observeReaderFailure(ctx context.Context) context.Context {
	return readaccounting.WithTransitionReadFailureObserver(ctx, control.recordReaderFailure)
}
