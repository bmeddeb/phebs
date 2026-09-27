package store

import (
	"context"
	"encoding/json"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// ReadTypedIndexIntent is a read-only selected-provider lookup. ErrNotFound means
// the intent row is absent, including when repository source is unavailable.
// A present malformed/empty row fails closed. One bounded point read; no source
// initialization, attempt scan, filesystem read or persistent cache.
func (s *Surreal) ReadTypedIndexIntent(ctx context.Context, repository string) (TypedIndexIntent, error) {
	if !typedControlKey(TypedIndexIntents, repository) {
		return TypedIndexIntent{}, typedindex.Invalid
	}
	rows, err := s.typedRelationPoint(ctx, TypedIndexIntents, repository)
	if err != nil {
		return TypedIndexIntent{}, err
	}
	if len(rows) == 0 {
		return TypedIndexIntent{}, ErrNotFound
	}
	// typedRelationPoint already checked canonical shape and pinned profile.
	var intent TypedIndexIntent
	if err = json.Unmarshal([]byte(rows[0].Body), &intent); err != nil {
		return TypedIndexIntent{}, typedindex.Invalid
	}
	return intent, nil
}

// ReadTypedIndexCurrentCustody retains the exact current/owner/source/profile
// fences of ResolveTypedIndexCurrentCustody without lazy source initialization.
// A query cannot mint an incarnation after restore. Once source is initialized,
// this is six bounded point/fence reads, independent of retained attempt count.
func (s *Surreal) ReadTypedIndexCurrentCustody(ctx context.Context, repository string) (TypedIndexCurrentCustody, error) {
	a, err := s.readTypedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexCurrentCustody{}, err
	}
	return s.resolveTypedIndexCurrentCustody(ctx, a)
}
