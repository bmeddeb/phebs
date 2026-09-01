package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
)

// RelationshipGenerationOwnerV3 owns only the dark v3 shadow namespace. Its
// presence in lifecycle does not register or select v3 for runtime reads.
type RelationshipGenerationOwnerV3 struct {
	DataDir string
	Pins    *relationshippublication.CacheV3
	// AcquireExclusive excludes publication and the legacy relationship owner
	// while authority, store references, and shared components are inspected.
	AcquireExclusive func(context.Context) (func(), error)
	Store            relationshippublication.LifecyclePinStoreV3
}

func (RelationshipGenerationOwnerV3) Name() string { return RelationshipV3Owner }

func (owner RelationshipGenerationOwnerV3) Sweep(
	ctx context.Context, now time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.DataDir == "" || owner.Pins == nil || owner.AcquireExclusive == nil {
		return OwnerResult{
			Cursor: cursor, Completeness: Unavailable,
			Err: errors.New("relationship v3 lifecycle owner is incomplete"),
		}
	}
	release, err := owner.AcquireExclusive(ctx)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	defer release()
	result, err := relationshippublication.SweepLifecycleV3(
		ctx, owner.DataDir, now, cursor, owner.Pins, limits.Deletes,
	)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	if result.ReleasedRootV3 != nil {
		if owner.Store == nil {
			return OwnerResult{
				Cursor: cursor, Completeness: Unavailable,
				Err: errors.New("relationship v3 store pins are incomplete"),
			}
		}
		if err := relationshippublication.UnpinLifecycleV3(
			ctx, owner.Store, *result.ReleasedRootV3,
		); err != nil {
			return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
		}
		if err := relationshippublication.ConfirmLifecycleUnpinV3(
			ctx, owner.DataDir, result.Cursor, result.ReleasedPinOwner,
		); err != nil {
			return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
		}
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
