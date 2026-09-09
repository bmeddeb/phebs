package readaccounting

import "context"

type RelationshipEvent byte

const (
	RelationshipBuild      RelationshipEvent = 'B'
	RelationshipProjection RelationshipEvent = 'P'
)

type relationshipObserverKey struct{}

// WithRelationshipObserver binds actual builder/projector entries, not final
// authority changes, deduplicated root sizes, or runtime reconciliation calls.
func WithRelationshipObserver(ctx context.Context, observe func(RelationshipEvent) error) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(relationshipObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, relationshipObserverKey{}, observe), nil
}

func ObserveRelationship(ctx context.Context, required bool, event RelationshipEvent) (err error) {
	var observe func(RelationshipEvent) error
	if ctx != nil {
		observe, _ = ctx.Value(relationshipObserverKey{}).(func(RelationshipEvent) error)
	}
	if observe == nil {
		if required {
			return ErrScope
		}
		return nil
	}
	if event != RelationshipBuild && event != RelationshipProjection {
		return ErrEvent
	}
	defer func() {
		if recover() != nil {
			err = ErrEvent
		}
	}()
	// The function has already been entered, including by a canceled caller.
	// Retain that attempted entry before refusing any subsequent native work.
	if err := observe(event); err != nil {
		return err
	}
	return ctx.Err()
}
