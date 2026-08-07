package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
)

type SearchGenerationOwnerImpl struct {
	IndexDir string
	Pins     focusedindex.SearchGenerationPinChecker
	Acquire  func(context.Context) (func(), error)
}

func (SearchGenerationOwnerImpl) Name() string { return SearchOwner }

func (owner SearchGenerationOwnerImpl) Sweep(
	ctx context.Context, now time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.IndexDir == "" || owner.Pins == nil || owner.Acquire == nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: errors.New("search lifecycle owner is incomplete")}
	}
	release, err := owner.Acquire(ctx)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	defer release()
	result, err := focusedindex.SweepSearchGenerationLifecycle(
		ctx, owner.IndexDir, now, cursor, owner.Pins, limits.Deletes,
	)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	completeness := Exact
	if result.More {
		completeness = LowerBound
	}
	return OwnerResult{
		Cursor: result.Cursor, Scanned: result.Scanned, Deleted: result.Deleted,
		More: result.More, Completeness: completeness,
	}
}
