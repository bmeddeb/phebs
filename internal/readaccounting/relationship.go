package readaccounting

import "context"

type RelationshipEvent byte

const (
	RelationshipBuild      RelationshipEvent = 'B'
	RelationshipProjection RelationshipEvent = 'P'
	RelationshipReferences RelationshipEvent = 'R'
)

type relationshipObserverKey struct{}

// WithRelationshipObserver binds actual builder/projector entries and references
// in successfully installed service members, not final authority changes.
func WithRelationshipObserver(ctx context.Context, observe func(RelationshipEvent, uint64) error) (context.Context, error) {
	if ctx == nil || observe == nil || ctx.Value(relationshipObserverKey{}) != nil {
		return nil, ErrScope
	}
	return context.WithValue(ctx, relationshipObserverKey{}, observe), nil
}

func ObserveRelationship(ctx context.Context, required bool, event RelationshipEvent, quantity uint64) (err error) {
	var observe func(RelationshipEvent, uint64) error
	if ctx != nil {
		observe, _ = ctx.Value(relationshipObserverKey{}).(func(RelationshipEvent, uint64) error)
	}
	if observe == nil {
		if required {
			return ErrScope
		}
		return nil
	}
	if event != RelationshipReferences && (event != RelationshipBuild && event != RelationshipProjection || quantity != 1) {
		return ErrEvent
	}
	defer func() {
		if recover() != nil {
			err = ErrEvent
		}
	}()
	// The entry or member installation has already happened. Retain that work,
	// including zero-reference coverage, before refusing subsequent native work.
	if err := observe(event, quantity); err != nil {
		return err
	}
	return ctx.Err()
}
