package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
)

type RelationshipGenerationOwner struct {
	DataDir string
	Pins    relationshippublication.PinChecker
	Acquire func(context.Context) (func(), error)
}

func (RelationshipGenerationOwner) Name() string { return RelationshipOwner }

func (owner RelationshipGenerationOwner) Sweep(
	ctx context.Context, now time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.DataDir == "" || owner.Pins == nil || owner.Acquire == nil {
		return OwnerResult{
			Cursor: cursor, Completeness: Unavailable,
			Err: errors.New("relationship lifecycle owner is incomplete"),
		}
	}
	release, err := owner.Acquire(ctx)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	defer release()
	result, err := relationshippublication.SweepLifecycle(
		ctx, owner.DataDir, now, cursor, owner.Pins, limits.Deletes,
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
