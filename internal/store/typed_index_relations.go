package store

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

type typedControlRelations struct {
	Selected  []TypedIndexControl     `json:"selected"`
	Relations typedAttemptObservation `json:"relations"`
}

// InspectTypedIndexControlRelations checks one selected historical control and
// its direct immutable relations. It grants no live admission, filesystem
// readiness, quiescence or deletion authority. No current repository is read.
// Cost: 2 SDK reads for an empty intent or plan; 3 for request/attempt; 4 for
// state/current/nonempty intent. The final read-only transaction contains at
// most six direct record lookups, including the selected-row recheck. There are
// no scans, mutation operands, schema changes, process locks or child processes.
// The maximum is nine point lookups and 92 KiB of valid body payload across all
// replies (a nonempty intent); decoded values and scalar projections add bounded
// overhead. Work is independent of repository count and source corpus size.
func (s *Surreal) InspectTypedIndexControlRelations(ctx context.Context, kind TypedIndexControlKind, key string) error {
	return s.inspectTypedIndexControlRelations(ctx, kind, key, nil)
}

func (s *Surreal) typedRelationPoint(ctx context.Context, kind TypedIndexControlKind, key string) ([]TypedIndexControl, error) {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return nil, err
	}
	result, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, "SELECT "+typedControlProjection+" FROM $rid LIMIT 1;", map[string]any{"rid": typedID(string(kind), key)}, storeRead())
	if err != nil {
		return nil, typedError(ctx, err)
	}
	rows := firstDomainRows(result)
	if len(rows) > 1 {
		return nil, typedindex.Invalid
	}
	if len(rows) == 1 {
		if rows[0].ID != key {
			return nil, typedindex.Invalid
		}
		if err = validateTypedCensusControl(ctx, kind, rows[0]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (s *Surreal) inspectTypedIndexControlRelations(ctx context.Context, kind TypedIndexControlKind, key string, beforeSnapshot func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !typedControlKind(kind) || !typedControlKey(kind, key) {
		return typedindex.Invalid
	}
	rows, err := s.typedRelationPoint(ctx, kind, key)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrNotFound
	}
	selected := rows[0]
	var attemptRow, requestRow []TypedIndexControl
	var attempt typedIndexAttempt
	var intent TypedIndexIntent
	var current typedIndexCurrent
	root, requestDigest := "", ""
	switch kind {
	case TypedIndexAttempts:
		attemptRow = rows
	case TypedIndexStates, TypedIndexCurrents:
		var attemptDigest string
		if kind == TypedIndexStates {
			if typedDecode(selected.Body, maxTypedControlBytes, &attemptDigest) != nil {
				return typedindex.Invalid
			}
		} else {
			current, err = decodeTypedIndexCurrent(selected.Body)
			if err != nil {
				return err
			}
			attemptDigest = current.AttemptDigest
		}
		attemptRow, err = s.typedRelationPoint(ctx, TypedIndexAttempts, attemptDigest)
		if err != nil {
			return err
		}
		if len(attemptRow) != 1 || attemptRow[0].Repository != selected.Repository {
			return typedindex.Invalid
		}
	case TypedIndexRequests:
		root, requestDigest = selected.Root, selected.ID
	case TypedIndexPlans:
		root, requestDigest = selected.Root, selected.Root
	case TypedIndexIntents:
		if typedDecode(selected.Body, maxTypedIntentBytes, &intent) != nil {
			return typedindex.Invalid
		}
		if intent.Desired != "" {
			requestRow, err = s.typedRelationPoint(ctx, TypedIndexRequests, intent.Desired)
			if err != nil {
				return err
			}
			if len(requestRow) != 1 || requestRow[0].Repository != selected.Repository {
				return typedindex.Invalid
			}
			root, requestDigest = requestRow[0].Root, requestRow[0].ID
		}
	}
	if len(attemptRow) == 1 {
		if typedDecode(attemptRow[0].Body, maxTypedControlBytes, &attempt) != nil {
			return typedindex.Invalid
		}
		root, requestDigest = attempt.Root, attempt.Request
		if root == requestDigest && attempt.Stage != TypedPreflight && attempt.Stage != TypedPlanning {
			return typedindex.Invalid
		}
	}
	var plans []TypedIndexControl
	if kind == TypedIndexPlans {
		plans = rows
	} else if root != "" {
		plans, err = s.typedRelationPoint(ctx, TypedIndexPlans, root)
		if err != nil {
			return err
		}
	}
	successor := root
	if len(plans) == 1 {
		var plan typedIndexPlan
		if plans[0].Repository != selected.Repository || plans[0].Root != root || typedDecode(plans[0].Body, maxTypedControlBytes, &plan) != nil {
			return typedindex.Invalid
		}
		successor = plan.Successor
	}
	if beforeSnapshot != nil {
		beforeSnapshot()
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	vars := map[string]any{"selected": typedID(string(kind), key)}
	relations := "{attempt:[],parent:[],request:[],plan:[],successor:[]}"
	if root != "" {
		vars["parent"] = typedID(string(TypedIndexRequests), root)
		vars["request"] = typedID(string(TypedIndexRequests), requestDigest)
		vars["plan"] = typedID(string(TypedIndexPlans), root)
		vars["successor"] = typedID(string(TypedIndexRequests), successor)
		attemptSQL := "[]"
		if len(attemptRow) == 1 {
			vars["attempt"] = typedID(string(TypedIndexAttempts), attemptRow[0].ID)
			attemptSQL = "(SELECT " + typedControlProjection + " FROM $attempt LIMIT 1)"
		}
		relations = "{attempt:" + attemptSQL + ",parent:(SELECT " + typedControlProjection + " FROM $parent LIMIT 1),request:(SELECT " + typedControlProjection + " FROM $request LIMIT 1),plan:(SELECT " + typedControlProjection + " FROM $plan LIMIT 1),successor:(SELECT " + typedControlProjection + " FROM $successor LIMIT 1)}"
	}
	if err = readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return err
	}
	results, err := storeQuery[[]typedControlRelations](ctx, s.accounting, s.db, "BEGIN TRANSACTION; RETURN [{selected:(SELECT "+typedControlProjection+" FROM $selected LIMIT 1),relations:"+relations+"}]; COMMIT TRANSACTION;", vars, storeRead())
	if err != nil {
		return typedError(ctx, err)
	}
	snapshots := firstDomainRows(results)
	if len(snapshots) != 1 || len(snapshots[0].Selected) != 1 || snapshots[0].Selected[0] != selected {
		return typedindex.Stale
	}
	o := snapshots[0].Relations
	if !sameTypedControlRows(plans, o.Plan) || !sameTypedControlRows(attemptRow, o.Attempt) || len(requestRow) == 1 && !sameTypedControlRows(requestRow, o.Request) {
		return typedindex.Stale
	}
	if root == "" {
		return ctx.Err()
	}
	proof, err := inspectTypedRequestRelations(ctx, selected.Repository, root, requestDigest, o)
	if err != nil {
		return err
	}
	// Retirement always protects current and protects desired unless canceled or
	// awaiting restore. Historical controls may still reference collecting roots.
	activePointer := kind == TypedIndexCurrents || kind == TypedIndexIntents && !intent.Canceled && !intent.RestoreRequired
	if activePointer && o.Parent[0].State != "live" {
		return typedindex.Invalid
	}
	if len(attemptRow) == 1 && attempt.Custody != nil && attempt.Custody.Revision == 3 && (attempt.Custody.PublicationRequestDigest != requestDigest || attempt.Custody.PublicationPlanDigest != proof.PlanDigest) {
		return typedindex.Invalid
	}
	if kind == TypedIndexCurrents {
		b := current.Pointer.Binding
		if proof.Parent.Schema == typedindex.ManagedRequestSchema && proof.Parent.Purpose != typedindex.Publish {
			return typedindex.Invalid
		}
		if attempt.Stage != TypedComplete || !typedCustodyMatches(attempt.Custody, current.Pointer) || b.RequestDigest != requestDigest || b.Source != proof.Parent.Source || b.ProfileDigest != proof.Parent.ProfileDigest || b.ToolsDigest != proof.Parent.ToolsDigest || b.PlanDigest != proof.PlanDigest {
			return typedindex.Invalid
		}
	}
	if kind == TypedIndexIntents {
		profile, e := typedindex.DecodeProfile(ctx, []byte(intent.ProfileJSON))
		if e != nil {
			return e
		}
		r := proof.Parent
		// Match the stored intent, not today's repository incarnation or source.
		want := typedindex.NewRequest(r.Source, profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, r.IdempotencyKey)
		if r.Schema == typedindex.ManagedRequestSchema {
			want = typedindex.NewManagedRequest(r.Source, profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, r.Purpose)
		}
		if r != want {
			return typedindex.Invalid
		}
	}
	return ctx.Err()
}

func sameTypedControlRows(a, b []TypedIndexControl) bool {
	return len(a) == len(b) && (len(a) == 0 || len(a) == 1 && a[0] == b[0])
}
