package main

import (
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

type t422PublicationFailure struct {
	Stage         readaccounting.PublicationFailureStage
	Cause, Caller string
	Flags         readaccounting.PublicationFailureFlags
	logged        bool
}

func t422PublicationCause(err error) string {
	switch {
	case errors.Is(err, store.ErrConflict):
		return "store_conflict"
	case store.IsDeferral(err):
		return "store_deferral"
	case store.IsTerminal(err):
		return "store_terminal"
	default:
		return t422RecoveryCause(err)
	}
}
func (control *t422CheckpointRecoveryControl) recordPublicationFailure(ctx context.Context, stage readaccounting.PublicationFailureStage, err error, flags readaccounting.PublicationFailureFlags) {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.publicationFailure != nil {
		return
	}
	control.publicationFailure = &t422PublicationFailure{Stage: stage, Cause: t422PublicationCause(err), Caller: t422RecoveryContext(ctx), Flags: flags}
}

// Logging follows the handler's defers/locks; scheduler error handling is still
// subsequent. Mark before I/O so later failed callers never wait for this log.
func (control *t422CheckpointRecoveryControl) logPublicationFailure() {
	control.mu.Lock()
	saved := control.publicationFailure
	if saved == nil || saved.logged {
		control.mu.Unlock()
		return
	}
	saved.logged = true
	value := *saved
	control.mu.Unlock()
	diagnostics.Logf("private selected checkpoint publication first failure: stage=%s cause=%s caller=%s results_present=%t single_result=%t",
		value.Stage.String(), value.Cause, value.Caller, value.Flags.ResultsPresent, value.Flags.SingleResult)
}
func (control *t422CheckpointRecoveryControl) publicationHandler(next generationscheduler.Handler) generationscheduler.Handler {
	if next == nil {
		return nil
	} // Preserve the scheduler's existing invalid-handler rejection.
	return func(ctx context.Context, chunk store.GenerationChunk, budget generationscheduler.Budget) error {
		hit := control.input.Hit
		if ctx == nil || chunk.Repository != control.launch.request.Repository || chunk.Stage != extractionpublication.ScheduleStage ||
			chunk.ResourceClass != store.GenerationResourceExtraction || chunk.Generation != hit.ScheduleGeneration || chunk.ScheduleDigest != hit.ScheduleDigest ||
			chunk.Identity != hit.ChunkIdentity || chunk.Offset != int64(control.input.Offset) || chunk.Length != 1 || chunk.Attempt != 0 {
			return next(ctx, chunk, budget)
		}
		observed := readaccounting.WithPublicationFailureObserver(ctx, func(stage readaccounting.PublicationFailureStage, err error, flags readaccounting.PublicationFailureFlags) {
			control.recordPublicationFailure(ctx, stage, err, flags)
		})
		err := next(observed, chunk, budget)
		if err != nil {
			readaccounting.NotePublicationFailure(observed, readaccounting.PublicationFailureHandler, err, readaccounting.PublicationFailureFlags{})
		}
		control.logPublicationFailure()
		return err
	}
}
