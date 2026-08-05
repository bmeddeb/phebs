package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const GenerationScheduleOwner = "generation-schedules"

type GenerationScheduleStore interface {
	SweepGenerationScheduleLifecycle(
		context.Context, string, int, int, int,
	) (store.GenerationLifecycleSweep, error)
}

type GenerationOwner struct {
	Store   GenerationScheduleStore
	Acquire func(context.Context) (func(), error)
}

func (GenerationOwner) Name() string { return GenerationScheduleOwner }

func (owner GenerationOwner) Sweep(
	ctx context.Context, _ time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.Store == nil {
		return OwnerResult{Completeness: Unavailable, Err: errors.New("generation lifecycle store is nil")}
	}
	if owner.Acquire == nil {
		return OwnerResult{Completeness: Unavailable, Err: errors.New("generation lifecycle mutation lock is nil")}
	}
	release, err := owner.Acquire(ctx)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	defer release()
	scanLimit := limits.Candidates
	if scanLimit > MaxOwnerQueriesPerTick-1 {
		scanLimit = MaxOwnerQueriesPerTick - 1
	}
	sweep, err := owner.Store.SweepGenerationScheduleLifecycle(
		ctx, cursor, scanLimit, limits.Deletes, GenerationScheduleRetained,
	)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	completeness := Exact
	if sweep.More {
		completeness = LowerBound
	}
	return OwnerResult{
		Cursor: sweep.Cursor, Scanned: sweep.Scanned, Deleted: sweep.Deleted,
		More: sweep.More, Completeness: completeness,
	}
}
