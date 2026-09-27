package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// TypedIndexCollection is an opaque database observation, never physical
// drainage or native quiescence proof. Its collecting parent is retained forever
// by this API; obsolete-authority tombstone policy is a separate boundary.
type TypedIndexCollection struct {
	retirement  TypedIndexRetirement
	observation typedCollectionObservation
	expected    map[string]any
}

// Attempts returns the exact sorted attempt identities covered by the snapshot.
// The trusted controller must independently drain or verify physical absence for
// every identity, and verify the entire root namespace is drained.
func (c TypedIndexCollection) Attempts() []string {
	ids := make([]string, len(c.observation.Attempts))
	for i, row := range c.observation.Attempts {
		ids[i] = row.ID
	}
	return ids
}

type typedCollectionObservation struct {
	Retirement typedRetirementObservation `json:"retirement"`
	Requests   []TypedIndexControl        `json:"requests"`
	Attempts   []TypedIndexControl        `json:"attempts"`
	Plan       []TypedIndexControl        `json:"plan"`
	State      []TypedIndexControl        `json:"state"`
	StateOwner []TypedIndexControl        `json:"state_owner"`
}

// Normalize every optional field and omit undeclared SDK projection keys so
// the SQL object is byte-for-field identical to the typed snapshot on replay.
const typedCollectionProjection = `record::id(id) AS key, control_key ?? '' AS stored_key, repository ?? '' AS repository, request_root ?? '' AS request_root, is_parent ?? false AS is_parent, custody_state ?? '' AS custody_state, growth_key ?? '' AS growth_key, body ?? '' AS body`

const typedCollectionRequestsSQL = `SELECT ` + typedCollectionProjection + `, control_key FROM typed_index_request WHERE request_root=$root AND control_key>$after ORDER BY control_key LIMIT $request_limit`
const typedCollectionAttemptsSQL = `SELECT ` + typedCollectionProjection + `, control_key FROM typed_index_attempt WHERE request_root=$root AND control_key>$after ORDER BY control_key LIMIT $attempt_limit`
const typedCollectionObservationSQL = typedRetirementObservationSQL + `
LET $collection = {retirement:$observation,
 requests:(` + typedCollectionRequestsSQL + `), attempts:(` + typedCollectionAttemptsSQL + `),
 plan:(SELECT ` + typedCollectionProjection + ` FROM $plan LIMIT 1),
 state:(SELECT ` + typedCollectionProjection + ` FROM $state LIMIT 1),
 state_owner:(SELECT ` + typedCollectionProjection + ` FROM $state_owner LIMIT 1)};
`

func collectionObject(value typedCollectionObservation) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, typedindex.Invalid
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, typedindex.Invalid
	}
	// Surreal requires the physical ORDER BY field in each child selection.
	// Preserve that redundant scalar in the SQL equality snapshot as well.
	for _, field := range []string{"requests", "attempts"} {
		rows, _ := out[field].([]any)
		for _, value := range rows {
			row, ok := value.(map[string]any)
			if !ok {
				return nil, typedindex.Invalid
			}
			row["control_key"] = row["stored_key"]
		}
	}
	return out, nil
}

// InspectTypedIndexCollection requires the caller's complete unfiltered startup
// census and shared lifecycle guard. Root indexes alone cannot find corrupt
// missing projections. It accepts only an already collecting, unprotected root
// with at most64 attempts, one plan and one successor, and no held growth.
// Cost: four existing retirement reads, one state read, optionally one state-owner
// read, and one coherent bounded snapshot (six or seven SDK reads). No mutation,
// filesystem operation, scan over other roots, or native action occurs. The
// maximum valid-body response budget is462KiB including both overflow sentinels,
// before SDK/JSON/scalar overhead. The selected snapshot and its normalized CAS
// object remain bounded by the same controls. Parent/successor relations are
// validated once per call, not once per attempt.
func (s *Surreal) InspectTypedIndexCollection(ctx context.Context, root string) (TypedIndexCollection, error) {
	return s.inspectTypedIndexCollection(ctx, root, nil)
}
func (s *Surreal) inspectTypedIndexCollection(ctx context.Context, root string, beforeSnapshot func()) (TypedIndexCollection, error) {
	var out TypedIndexCollection
	if ctx == nil {
		return out, typedindex.Invalid
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	retired, err := s.InspectTypedIndexRetirement(ctx, root)
	if err != nil {
		return out, err
	}
	if !retired.Collecting() || retired.Protected() {
		return out, typedindex.Stale
	}
	state, err := s.typedRelationPoint(ctx, TypedIndexStates, retired.repository)
	if err != nil {
		return out, err
	}
	var stateOwner []TypedIndexControl
	stateID := "absent"
	if len(state) == 1 {
		if typedDecode(state[0].Body, maxTypedControlBytes, &stateID) != nil {
			return out, typedindex.Invalid
		}
		stateOwner, err = s.typedRelationPoint(ctx, TypedIndexAttempts, stateID)
		if err != nil {
			return out, err
		}
		if len(stateOwner) != 1 || stateOwner[0].Repository != retired.repository {
			return out, typedindex.Invalid
		}
	}
	vars, err := collectionVariables(retired, stateID)
	if err != nil {
		return out, err
	}
	if beforeSnapshot != nil {
		beforeSnapshot()
	}
	if err = readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	result, err := storeQuery[[]typedCollectionObservation](ctx, s.accounting, s.db, "BEGIN TRANSACTION;"+typedCollectionObservationSQL+"RETURN [$collection]; COMMIT TRANSACTION;", vars, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	rows := firstDomainRows(result)
	if len(rows) != 1 {
		return out, typedindex.Invalid
	}
	observed := rows[0]
	expected, err := collectionObject(observed)
	if err != nil {
		return out, err
	}
	if !reflect.DeepEqual(expected["retirement"], retired.expected) || !sameTypedControlRows(state, observed.State) || !sameTypedControlRows(stateOwner, observed.StateOwner) {
		return out, typedindex.Stale
	}
	out = TypedIndexCollection{retired, observed, expected}
	if err = validateTypedCollection(ctx, out); err != nil {
		return TypedIndexCollection{}, err
	}
	return out, ctx.Err()
}

func collectionVariables(retired TypedIndexRetirement, stateID string) (map[string]any, error) {
	var intent TypedIndexIntent
	var current typedIndexCurrent
	if raw, _ := retired.expected["intent"].(string); raw != "" && typedDecode(raw, maxTypedIntentBytes, &intent) != nil {
		return nil, typedindex.Invalid
	}
	if raw, _ := retired.expected["current"].(string); raw != "" {
		var err error
		current, err = decodeTypedIndexCurrent(raw)
		if err != nil {
			return nil, err
		}
	}
	vars := typedRetirementVariables(retired.root, retired.repository, intent.Desired, current.Pointer.Binding.RequestDigest, current.AttemptDigest)
	vars["plan"] = typedID(string(TypedIndexPlans), retired.root)
	vars["state"] = typedID(string(TypedIndexStates), retired.repository)
	vars["state_owner"] = typedID(string(TypedIndexAttempts), stateID)
	vars["after"], vars["request_limit"], vars["attempt_limit"] = "", 3, MaxTypedIndexAttemptsPerRoot+1
	return vars, nil
}

func validateTypedCollection(ctx context.Context, c TypedIndexCollection) error {
	r, o := c.retirement, c.observation
	if !typedStoredDigest(r.root) || r.repository == "" || r.expected == nil || c.expected == nil || !r.collecting || r.Protected() || o.Retirement.State != "collecting" || o.Retirement.Running {
		return typedindex.Stale
	}
	if len(o.Requests) > 2 || len(o.Attempts) > MaxTypedIndexAttemptsPerRoot {
		return typedindex.Capacity
	}
	if len(o.Plan) > 1 || len(o.State) > 1 || len(o.StateOwner) > 1 {
		return typedindex.Invalid
	}
	var parent, successor []TypedIndexControl
	previous := ""
	for _, row := range o.Requests {
		if validateTypedCensusControl(ctx, TypedIndexRequests, row) != nil || row.ID <= previous || row.Root != r.root || row.Repository != r.repository {
			return typedindex.Invalid
		}
		previous = row.ID
		if row.Parent {
			parent = append(parent, row)
		} else {
			successor = append(successor, row)
		}
	}
	if len(parent) != 1 || parent[0].State != "collecting" || len(successor) > 1 || len(successor) != len(o.Plan) {
		return typedindex.Invalid
	}
	if _, err := inspectTypedRequestRelations(ctx, r.repository, r.root, r.root, typedAttemptObservation{Parent: parent, Request: parent, Plan: o.Plan, Successor: successor}); err != nil {
		return err
	}
	var execution TypedIndexAttemptInspection
	if len(successor) == 1 {
		var err error
		execution, err = inspectTypedRequestRelations(ctx, r.repository, r.root, successor[0].ID, typedAttemptObservation{Parent: parent, Request: successor, Plan: o.Plan, Successor: successor})
		if err != nil {
			return err
		}
	}
	previous = ""
	for _, row := range o.Attempts {
		if validateTypedCensusControl(ctx, TypedIndexAttempts, row) != nil || row.ID <= previous || row.Root != r.root || row.Repository != r.repository {
			return typedindex.Invalid
		}
		previous = row.ID
		var a typedIndexAttempt
		if typedDecode(row.Body, maxTypedControlBytes, &a) != nil || a.Growth != nil && a.Growth.State != "released" {
			return typedindex.Stale
		}
		planDigest := ""
		if a.Request == r.root {
			if a.Stage != TypedPreflight && a.Stage != TypedPlanning {
				return typedindex.Invalid
			}
		} else {
			if a.Request != execution.RequestDigest {
				return typedindex.Invalid
			}
			planDigest = execution.PlanDigest
		}
		if a.Custody != nil && a.Custody.Revision == 3 && (a.Custody.PublicationRequestDigest != a.Request || a.Custody.PublicationPlanDigest != planDigest) {
			return typedindex.Invalid
		}
	}
	if len(o.State) == 1 {
		if validateTypedCensusControl(ctx, TypedIndexStates, o.State[0]) != nil || o.State[0].Repository != r.repository || len(o.StateOwner) != 1 {
			return typedindex.Invalid
		}
		owner := o.StateOwner[0]
		var id string
		if typedDecode(o.State[0].Body, maxTypedControlBytes, &id) != nil || owner.ID != id || owner.Repository != r.repository || validateTypedCensusControl(ctx, TypedIndexAttempts, owner) != nil {
			return typedindex.Invalid
		}
		if owner.Root == r.root && !slices.ContainsFunc(o.Attempts, func(row TypedIndexControl) bool { return row == owner }) {
			return typedindex.Invalid
		}
	} else if len(o.StateOwner) != 0 {
		return typedindex.Invalid
	}
	return ctx.Err()
}

// CollectDrainedTypedIndexControls deletes the exact selected derived controls
// atomically. Only the trusted controller may supply drainedAttempts: it must
// hold the shared lifecycle guard, have completed the global structural census,
// prove native quiescence and zero future growth, and drain the entire physical
// root and every listed attempt (or verify its absence). An empty list requires
// the same proof. This API does not obtain or infer any physical proof itself.
// The sorted list must exactly match Attempts(); extra/missing/reordered IDs
// refuse. The parent tombstone survives and still consumes its retained-root
// quota. No tombstone expiry or full runtime lifecycle completion is implied.
// One transaction repeats the bounded selection and supplies at most68 mutation
// operands (64 attempts, plan, successor, state, intent). A completely empty
// replay uses a read-only fence. No file/hash/child/process lock is added.
func (s *Surreal) CollectDrainedTypedIndexControls(ctx context.Context, c TypedIndexCollection, drainedAttempts []string) error {
	if ctx == nil {
		return typedindex.Invalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTypedCollection(ctx, c); err != nil {
		return err
	}
	if !slices.Equal(drainedAttempts, c.Attempts()) {
		return typedindex.Invalid
	}
	stateID := "absent"
	if len(c.observation.StateOwner) == 1 {
		stateID = c.observation.StateOwner[0].ID
	}
	vars, err := collectionVariables(c.retirement, stateID)
	if err != nil {
		return err
	}
	vars["expected"] = c.expected
	ids := make([]models.RecordID, 0, len(drainedAttempts)+3)
	for _, id := range drainedAttempts {
		ids = append(ids, typedID(string(TypedIndexAttempts), id))
	}
	if len(c.observation.Plan) == 1 {
		ids = append(ids, typedID(string(TypedIndexPlans), c.retirement.root))
	}
	for _, row := range c.observation.Requests {
		if !row.Parent {
			ids = append(ids, typedID(string(TypedIndexRequests), row.ID))
		}
	}
	if len(c.observation.StateOwner) == 1 && c.observation.StateOwner[0].Root == c.retirement.root {
		ids = append(ids, typedID(string(TypedIndexStates), c.retirement.repository))
	}
	vars["ids"] = ids
	statement := typedCollectionObservationSQL + `IF $collection != $expected OR $observation.state != 'collecting' OR $observation.running { THROW 'typed-stale'; };`
	operands := uint64(len(ids))
	var intent TypedIndexIntent
	raw := c.observation.Retirement.Intent
	if raw != "" {
		if typedDecode(raw, maxTypedIntentBytes, &intent) != nil {
			return typedindex.Invalid
		}
		if intent.Desired != "" && c.observation.Retirement.DesiredRoot == c.retirement.root {
			if !intent.Canceled && !intent.RestoreRequired {
				return typedindex.Stale
			}
			intent.Desired = ""
			body, e := typedEncode(intent, maxTypedIntentBytes)
			if e != nil {
				return e
			}
			vars["intent_body"] = body
			statement += `UPDATE $intent SET body=$intent_body RETURN NONE;`
			operands++
		}
	}
	if len(ids) != 0 {
		statement += `FOR $rid IN $ids { DELETE $rid RETURN NONE; };`
	}
	if operands == 0 {
		return s.typedFence(ctx, statement, vars)
	}
	return s.typedWrite(ctx, statement, vars, operands)
}
