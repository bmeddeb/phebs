package store

import (
	"context"
	"encoding/json"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// TypedIndexReplacement is mutation-only, exact old-current custody. It grants
// no reader admission and cannot be constructed from a public pointer.
type TypedIndexReplacement struct {
	pointer             typedindex.PublicationPointer
	current, owner      []TypedIndexControl
	attempt, repository string
	valid               bool
}

// ReadExpectedTypedIndexCurrent preserves the old epoch even when its source or
// profile is obsolete. Malformed custody is never treated as absent.
func (s *Surreal) ReadExpectedTypedIndexCurrent(ctx context.Context, chunk GenerationChunk) (TypedIndexReplacement, error) {
	return s.readExpectedTypedIndexCurrent(ctx, chunk, nil)
}
func (s *Surreal) readExpectedTypedIndexCurrent(ctx context.Context, chunk GenerationChunk, beforeFence func()) (out TypedIndexReplacement, err error) {
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return out, err
	}
	out.current, err = s.typedRelationPoint(ctx, TypedIndexCurrents, chunk.Repository)
	if err != nil {
		return TypedIndexReplacement{}, err
	}
	if len(out.current) == 1 {
		current, e := decodeTypedIndexCurrent(out.current[0].Body)
		if e != nil {
			return TypedIndexReplacement{}, e
		}
		out.owner, e = s.typedRelationPoint(ctx, TypedIndexAttempts, current.AttemptDigest)
		if e != nil {
			return TypedIndexReplacement{}, e
		}
		if len(out.owner) != 1 {
			return TypedIndexReplacement{}, typedindex.Invalid
		}
		if e = s.InspectTypedIndexControlRelations(ctx, TypedIndexCurrents, chunk.Repository); e != nil {
			return TypedIndexReplacement{}, e
		}
		out.pointer = current.Pointer
	}
	out.valid = true
	out.attempt = x.work.AttemptDigest
	out.repository = chunk.Repository
	guard, err := out.fence(x)
	if err != nil {
		return TypedIndexReplacement{}, err
	}
	if beforeFence != nil {
		beforeFence()
	}
	x.vars["attempt_before"] = x.attemptRaw
	if err = s.typedFence(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+`IF (SELECT body FROM $attempt WHERE request_root=$root LIMIT 1)[0].body != $attempt_before { THROW 'typed-stale'; };`+guard, x.vars); err != nil {
		return TypedIndexReplacement{}, err
	}
	return out, nil
}

// This projection matches the decoded snapshot exactly. Raw record IDs and
// NONE fields from the general census projection are intentionally normalized.
const replacementProjection = `record::id(id) AS key, control_key ?? '' AS stored_key, repository, request_root ?? '' AS request_root, is_parent ?? false AS is_parent, custody_state ?? '' AS custody_state, growth_key ?? '' AS growth_key, body`

func replacementRows(rows []TypedIndexControl) ([]map[string]any, error) {
	if rows == nil {
		rows = []TypedIndexControl{}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return nil, typedindex.Invalid
	}
	var out []map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, typedindex.Invalid
	}
	return out, nil
}
func (r TypedIndexReplacement) fence(x typedExecution) (string, error) {
	if !r.valid || r.attempt != x.work.AttemptDigest || r.repository != x.work.Parent.Request().Source.Repository || len(r.current) > 1 || len(r.owner) != len(r.current) {
		return "", typedindex.Stale
	}
	rows, err := replacementRows(r.current)
	if err != nil {
		return "", err
	}
	x.vars["replacement_current"] = typedID(string(TypedIndexCurrents), r.repository)
	x.vars["replacement_rows"] = rows
	guard := `IF (SELECT ` + replacementProjection + ` FROM $replacement_current LIMIT 1) != $replacement_rows { THROW 'typed-stale'; };`
	if len(r.owner) == 1 {
		owners, e := replacementRows(r.owner)
		if e != nil {
			return "", e
		}
		x.vars["replacement_owner"] = typedID(string(TypedIndexAttempts), r.owner[0].ID)
		x.vars["replacement_owners"] = owners
		guard += `IF (SELECT ` + replacementProjection + ` FROM $replacement_owner LIMIT 1) != $replacement_owners { THROW 'typed-stale'; };`
	}
	return guard, nil
}

// PublishTypedIndexReplacement uses the existing publication transaction with
// the exact observed wrapper/owner projections as additional atomic predicates.
func (s *Surreal) PublishTypedIndexReplacement(ctx context.Context, chunk GenerationChunk, expected TypedIndexReplacement, bundle typedindex.Bundle) (typedindex.PublicationPointer, error) {
	return s.publishTypedIndex(ctx, chunk, expected.pointer, bundle, &expected)
}
