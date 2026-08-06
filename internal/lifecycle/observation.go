package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/observationpublication"
)

type ObservationGenerationOwner struct {
	Root    string
	Pins    observationpublication.PinChecker
	Acquire func(context.Context) (func(), error)
}

func (ObservationGenerationOwner) Name() string { return ObservationOwner }

func (owner ObservationGenerationOwner) Sweep(
	ctx context.Context, now time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.Root == "" || owner.Pins == nil || owner.Acquire == nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: errors.New("observation lifecycle owner is incomplete")}
	}
	release, err := owner.Acquire(ctx)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	defer release()
	result, err := observationpublication.SweepLifecycle(ctx, owner.Root, now, cursor, owner.Pins, limits.Deletes)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	completeness := Exact
	if result.More {
		completeness = LowerBound
	}
	return OwnerResult{Cursor: result.Cursor, Scanned: result.Scanned, Deleted: result.Deleted, More: result.More, Completeness: completeness}
}
