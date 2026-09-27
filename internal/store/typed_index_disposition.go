package store

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

// TypedIndexDisposition is a live-lease observation, not execution or filesystem
// authority. Its private history is compared again when acquiring growth.
type TypedIndexDisposition struct {
	parent              typedindex.Admission
	state               TypedIndexDispositionState
	rows, current       []TypedIndexControl
	attempt, repository string
	valid               bool
}

func (d TypedIndexDisposition) State() TypedIndexDispositionState { return d.state }
func (d TypedIndexDisposition) Parent() typedindex.Admission      { return d.parent }

type TypedIndexDispositionState string

const (
	TypedIndexFresh            TypedIndexDispositionState = "fresh"
	TypedIndexAlreadyPublished TypedIndexDispositionState = "already_published"
	TypedIndexAlreadyChecked   TypedIndexDispositionState = "already_checked"
	TypedIndexInterrupted      TypedIndexDispositionState = "interrupted"
)

// InspectTypedIndexDisposition inspects all retained attempts of the selected
// root, even if a different root overwrote the last-state pointer. The root
// index physically limits the range to 65 rows (64 accepted plus sentinel).
// No attempt or allowance is created. Startup's complete six-table census is
// required separately because a root index cannot discover corrupt projections.
func (s *Surreal) InspectTypedIndexDisposition(ctx context.Context, chunk GenerationChunk) (TypedIndexDisposition, error) {
	return s.inspectTypedIndexDisposition(ctx, chunk, nil)
}
func (s *Surreal) inspectTypedIndexDisposition(ctx context.Context, chunk GenerationChunk, beforeFence func()) (TypedIndexDisposition, error) {
	var out TypedIndexDisposition
	if ctx == nil {
		return out, typedindex.Invalid
	}
	x, err := s.typedExecution(ctx, chunk, false)
	if err != nil {
		return out, err
	}
	out = TypedIndexDisposition{parent: x.work.Parent, state: TypedIndexFresh, attempt: x.work.AttemptDigest, repository: chunk.Repository, valid: true}
	x.vars["after"] = ""
	x.vars["attempt_limit"] = 65
	if err = readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return TypedIndexDisposition{}, err
	}
	r, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, typedCollectionAttemptsSQL, x.vars, storeRead())
	if err != nil {
		return TypedIndexDisposition{}, typedError(ctx, err)
	}
	out.rows = firstDomainRows(r)
	if len(out.rows) > 64 {
		return TypedIndexDisposition{}, typedindex.Capacity
	}
	checked, interrupted := 0, false
	for _, row := range out.rows {
		if err = validateTypedCensusControl(ctx, TypedIndexAttempts, row); err != nil {
			return TypedIndexDisposition{}, err
		}
		var a typedIndexAttempt
		if typedDecode(row.Body, maxTypedControlBytes, &a) != nil || row.Repository != chunk.Repository || row.Root != chunk.Generation || a.Request != chunk.Generation && a.Request != x.plan.Successor {
			return TypedIndexDisposition{}, typedindex.Invalid
		}
		if !typedCheckRelation(a, x.work.Parent.Request(), x.plan.Digest) {
			return TypedIndexDisposition{}, typedindex.Invalid
		}
		if a.Stage == TypedChecked {
			if a.Request != x.work.Admission.Digest() {
				return TypedIndexDisposition{}, typedindex.Invalid
			}
			checked++
		} else if a.Stage != TypedPreflight {
			interrupted = true
			out.state = TypedIndexInterrupted
		}
	}
	if checked > 1 || checked == 1 && interrupted {
		return TypedIndexDisposition{}, typedindex.Invalid
	}
	if checked == 1 {
		out.state = TypedIndexAlreadyChecked
	}
	out.current, err = s.typedRelationPoint(ctx, TypedIndexCurrents, chunk.Repository)
	if err != nil {
		return TypedIndexDisposition{}, err
	}
	if len(out.current) == 1 {
		stored, e := decodeTypedIndexCurrent(out.current[0].Body)
		if e != nil {
			return TypedIndexDisposition{}, e
		}
		for _, row := range out.rows {
			if row.ID == stored.AttemptDigest {
				var a typedIndexAttempt
				_ = typedDecode(row.Body, maxTypedControlBytes, &a)
				if a.Stage != TypedComplete || !typedCustodyMatches(a.Custody, stored.Pointer) || a.Request != x.work.Admission.Digest() {
					return TypedIndexDisposition{}, typedindex.Invalid
				}
				// The ordinary resolver proves exact current/completed owner, source,
				// profile and plan admission; the final range/current fence preserves it.
				resolved, e := s.ResolveTypedIndexCurrentCustody(ctx, chunk.Repository)
				if e != nil {
					return TypedIndexDisposition{}, e
				}
				if resolved.Parent.Digest() != chunk.Generation || resolved.AttemptDigest != row.ID {
					return TypedIndexDisposition{}, typedindex.Stale
				}
				out.state = TypedIndexAlreadyPublished
			}
		}
	}
	guard, err := out.fence(x)
	if err != nil {
		return TypedIndexDisposition{}, err
	}
	if beforeFence != nil {
		beforeFence()
	}
	if err = s.typedFence(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+guard, x.vars); err != nil {
		return TypedIndexDisposition{}, err
	}
	return out, nil
}
func (d TypedIndexDisposition) fence(x typedExecution) (string, error) {
	if !d.valid || d.attempt != x.work.AttemptDigest || d.repository != x.work.Parent.Request().Source.Repository || d.parent.Digest() != x.work.RootDigest || len(d.rows) > 64 || len(d.current) > 1 {
		return "", typedindex.Stale
	}
	rows, err := replacementRows(d.rows)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		row["control_key"] = row["stored_key"]
	}
	currents, err := replacementRows(d.current)
	if err != nil {
		return "", err
	}
	parent, err := typedEncode(x.parent, typedindex.MaxRequestBytes+1024)
	if err != nil {
		return "", err
	}
	request, err := typedEncode(x.request, typedindex.MaxRequestBytes+1024)
	if err != nil {
		return "", err
	}
	plan := ""
	if x.plan.Digest != "" {
		plan, err = typedEncode(x.plan, maxTypedControlBytes)
		if err != nil {
			return "", err
		}
	}
	x.vars["after"] = ""
	x.vars["attempt_limit"] = 65
	x.vars["disposition_rows"] = rows
	x.vars["disposition_current"] = typedID(string(TypedIndexCurrents), d.repository)
	x.vars["disposition_currents"] = currents
	x.vars["disposition_parent"] = parent
	x.vars["disposition_request"] = typedID(string(TypedIndexRequests), x.authority.intent.Desired)
	x.vars["disposition_request_body"] = request
	x.vars["disposition_plan"] = typedID(string(TypedIndexPlans), x.work.RootDigest)
	x.vars["disposition_plan_body"] = plan
	return `IF (` + typedCollectionAttemptsSQL + `) != $disposition_rows { THROW 'typed-stale'; };
 IF (SELECT ` + replacementProjection + ` FROM $disposition_current LIMIT 1) != $disposition_currents { THROW 'typed-stale'; };
 IF (SELECT body FROM $typed_root LIMIT 1)[0].body != $disposition_parent { THROW 'typed-stale'; };
 IF (SELECT body FROM $disposition_request LIMIT 1)[0].body != $disposition_request_body { THROW 'typed-stale'; };
 IF ((SELECT body FROM $disposition_plan LIMIT 1)[0].body ?? '') != $disposition_plan_body { THROW 'typed-stale'; };`, nil
}

// AcquireFreshTypedIndexGrowth atomically repeats the exact root-history
// observation with the existing unique-holder acquisition/reopen predicates.
func (s *Surreal) AcquireFreshTypedIndexGrowth(ctx context.Context, chunk GenerationChunk, spec TypedIndexGrowthSpec, d TypedIndexDisposition) (TypedIndexGrowth, error) {
	if d.state != TypedIndexFresh {
		return TypedIndexGrowth{}, typedindex.Unprepared
	}
	return s.acquireTypedIndexGrowth(ctx, chunk, spec, &d)
}
