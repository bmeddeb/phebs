package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

type CatalogV3Store interface {
	SweepServiceCatalogV3Lifecycle(
		context.Context, string, int, int, int,
	) (store.ServiceCatalogV3LifecycleSweep, error)
}

type CatalogV3GenerationOwner struct {
	Store   CatalogV3Store
	Acquire func(context.Context) (func(), error)
}

func (CatalogV3GenerationOwner) Name() string { return CatalogV3Owner }

func (owner CatalogV3GenerationOwner) Sweep(
	ctx context.Context, _ time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.Store == nil || owner.Acquire == nil {
		return OwnerResult{
			Cursor: cursor, Completeness: Unavailable,
			Err: errors.New("catalog v3 lifecycle owner is incomplete"),
		}
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
	sweep, err := owner.Store.SweepServiceCatalogV3Lifecycle(
		ctx, cursor, scanLimit, limits.Deletes, store.ServiceCatalogV3Retained,
	)
	if err != nil {
		resultCursor := cursor
		advance := sweep.Cursor != "" && sweep.Cursor != cursor
		if advance {
			resultCursor = sweep.Cursor
		}
		return OwnerResult{
			Cursor: resultCursor, Scanned: sweep.Scanned, Deleted: sweep.Deleted,
			More: true, Completeness: Unavailable, Err: err,
			AdvanceOnError: advance,
			LogicalBytes:   sweep.RetiredLogicalBytes,
			RootBytes:      sweep.DeletedRootBytes,
			MemberBytes:    sweep.DeletedMemberBytes,
		}
	}
	completeness := Exact
	if sweep.More {
		completeness = LowerBound
	}
	return OwnerResult{
		Cursor: sweep.Cursor, Scanned: sweep.Scanned, Deleted: sweep.Deleted,
		More: sweep.More, Completeness: completeness,
		LogicalBytes: sweep.RetiredLogicalBytes,
		RootBytes:    sweep.DeletedRootBytes,
		MemberBytes:  sweep.DeletedMemberBytes,
	}
}
