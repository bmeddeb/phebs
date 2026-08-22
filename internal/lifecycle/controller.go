package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

const rotationCursorKey = "rotation"

type CursorStore interface {
	GetLifecycleCursor(context.Context, string) (string, uint64, error)
	CompareAndSwapLifecycleCursor(context.Context, string, uint64, string) error
}

type Controller struct {
	store  CursorStore
	owners []registeredOwner
	limits Limits
	now    func() time.Time
}

func NewController(store CursorStore, owners ...Owner) (*Controller, error) {
	if store == nil {
		return nil, errors.New("lifecycle cursor store is nil")
	}
	registered := make([]registeredOwner, 0, len(owners))
	for _, owner := range owners {
		if owner == nil {
			return nil, errors.New("lifecycle owner is nil")
		}
		registered = append(registered, registeredOwner{name: owner.Name(), owner: owner})
	}
	normalized, err := normalizeOwners(registered)
	if err != nil {
		return nil, err
	}
	return &Controller{
		store: store, owners: normalized, limits: DefaultLimits(), now: time.Now,
	}, nil
}

// Tick advances exactly one owner. The durable rotation cursor prevents a
// frequently restarted process from always beginning with the first owner;
// each owner separately persists its restart cursor. Repeated work after a
// cursor-CAS failure is safe because owner deletion must be idempotent and
// root-rechecked.
func (controller *Controller) Tick(ctx context.Context) OwnerResult {
	if err := ctx.Err(); err != nil {
		return OwnerResult{Completeness: Unavailable, Err: err}
	}
	if controller == nil || controller.store == nil || len(controller.owners) == 0 {
		return OwnerResult{Completeness: Unavailable, Err: errors.New("lifecycle controller is not configured")}
	}
	if err := controller.limits.validate(); err != nil {
		return OwnerResult{Completeness: Unavailable, Err: err}
	}
	rotation, rotationRevision, err := controller.store.GetLifecycleCursor(ctx, rotationCursorKey)
	if err != nil {
		return OwnerResult{Completeness: Unavailable, Err: fmt.Errorf("read lifecycle rotation: %w", err)}
	}
	owner := controller.nextOwner(rotation)
	cursorKey := "owner:" + owner.name
	cursor, cursorRevision, err := controller.store.GetLifecycleCursor(ctx, cursorKey)
	if err != nil {
		return OwnerResult{Owner: owner.name, Completeness: Unavailable, Err: fmt.Errorf("read lifecycle owner cursor: %w", err)}
	}
	result := owner.owner.Sweep(ctx, controller.now().UTC(), cursor, controller.limits)
	result.Owner = owner.name
	result.CycleStart = owner.name == controller.owners[0].name
	result.CycleComplete = owner.name == controller.owners[len(controller.owners)-1].name
	if result.Err == nil {
		if result.Completeness == "" {
			result.Completeness = Exact
		}
		if result.Scanned < 0 || result.Scanned > controller.limits.Candidates ||
			result.Deleted < 0 || result.Deleted > controller.limits.Deletes ||
			result.Cursor == cursor && result.More && result.Scanned == 0 && result.Deleted == 0 {
			result.Err = errors.New("lifecycle owner returned an invalid bounded result")
			result.Completeness = Unavailable
		}
	}
	if result.Err != nil && result.AdvanceOnError &&
		(result.Cursor == "" || result.Cursor == cursor) {
		result.Err = errors.Join(
			result.Err,
			errors.New("lifecycle owner requested invalid cursor advancement on error"),
		)
		result.AdvanceOnError = false
	}
	if result.Err == nil || result.AdvanceOnError {
		if err := controller.store.CompareAndSwapLifecycleCursor(
			ctx, cursorKey, cursorRevision, result.Cursor,
		); err != nil {
			result.Err = errors.Join(
				result.Err, fmt.Errorf("save lifecycle owner cursor: %w", err),
			)
			result.Completeness = Unavailable
		}
	}
	// Failure is localized to this owner. Advancing the durable rotation even
	// on failure gives every other owner a turn; the failed owner's cursor is
	// unchanged and will retry on its next fair turn.
	if err := controller.store.CompareAndSwapLifecycleCursor(
		ctx, rotationCursorKey, rotationRevision, owner.name,
	); err != nil {
		result.Err = errors.Join(result.Err, fmt.Errorf("save lifecycle rotation: %w", err))
		result.Completeness = Unavailable
	}
	return result
}

func (controller *Controller) nextOwner(previous string) registeredOwner {
	index := sort.Search(len(controller.owners), func(index int) bool {
		return controller.owners[index].name > previous
	})
	if index == len(controller.owners) {
		index = 0
	}
	return controller.owners[index]
}
