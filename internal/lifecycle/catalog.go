package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	CatalogRetained = 3
)

type CatalogStore interface {
	SweepCatalogLifecycle(
		context.Context, string, int, int, int,
	) (store.CatalogLifecycleSweep, error)
}

type CatalogGenerationOwner struct {
	Store   CatalogStore
	Acquire func(context.Context) (func(), error)
}

func (CatalogGenerationOwner) Name() string { return CatalogOwner }

func (owner CatalogGenerationOwner) Sweep(
	ctx context.Context, _ time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.Store == nil || owner.Acquire == nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: errors.New("catalog lifecycle owner is incomplete")}
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
	sweep, err := owner.Store.SweepCatalogLifecycle(
		ctx, cursor, scanLimit, limits.Deletes, CatalogRetained,
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
