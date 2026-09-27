package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

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
		if !typedCheckRelation(a, execution.Parent, planDigest) {
			return typedindex.Invalid
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
	_, err := s.collectDrainedTypedIndexControls(ctx, c, drainedAttempts, len(c.observation.Attempts))
	return err
}

// TypedIndexCollectionBatch counts actual submitted mutation operands. Mutations
// includes an inactive-intent rewrite, when present, as well as deleted rows.
// More reports surviving attempts, not physical custody or tombstone eligibility.
type TypedIndexCollectionBatch struct {
	Mutations int
	More      bool
}

// CollectDrainedTypedIndexControlsBatch has the same physical-proof and global
// census prerequisites as CollectDrainedTypedIndexControls. The entire root must
// be absent before the FIRST batch: deleting attempt rows must never discard the
// last authority for surviving files. Each transaction retains the complete
// snapshot fence, removes at most12 attempts and at most4 fixed controls, and
// supplies no more than16 mutation operands. Reinspect before every subsequent
// batch. State is removed only with its owner; plan/successor/intent survive until
// the last attempt batch. A stale snapshot makes no progress.
func (s *Surreal) CollectDrainedTypedIndexControlsBatch(ctx context.Context, c TypedIndexCollection, drainedAttempts []string) (TypedIndexCollectionBatch, error) {
	return s.collectDrainedTypedIndexControls(ctx, c, drainedAttempts, min(12, len(c.observation.Attempts)))
}

func (s *Surreal) collectDrainedTypedIndexControls(ctx context.Context, c TypedIndexCollection, drainedAttempts []string, count int) (batch TypedIndexCollectionBatch, err error) {
	if ctx == nil {
		return batch, typedindex.Invalid
	}
	if err := ctx.Err(); err != nil {
		return batch, err
	}
	if err := validateTypedCollection(ctx, c); err != nil {
		return batch, err
	}
	if !slices.Equal(drainedAttempts, c.Attempts()) {
		return batch, typedindex.Invalid
	}
	stateID := "absent"
	if len(c.observation.StateOwner) == 1 {
		stateID = c.observation.StateOwner[0].ID
	}
	vars, err := collectionVariables(c.retirement, stateID)
	if err != nil {
		return batch, err
	}
	vars["expected"] = c.expected
	ids := make([]models.RecordID, 0, len(drainedAttempts)+3)
	batch.More = count < len(drainedAttempts)
	selected := drainedAttempts[:count]
	for _, id := range selected {
		ids = append(ids, typedID(string(TypedIndexAttempts), id))
	}
	if !batch.More && len(c.observation.Plan) == 1 {
		ids = append(ids, typedID(string(TypedIndexPlans), c.retirement.root))
	}
	for _, row := range c.observation.Requests {
		if !batch.More && !row.Parent {
			ids = append(ids, typedID(string(TypedIndexRequests), row.ID))
		}
	}
	if len(c.observation.StateOwner) == 1 && c.observation.StateOwner[0].Root == c.retirement.root && slices.Contains(selected, c.observation.StateOwner[0].ID) {
		ids = append(ids, typedID(string(TypedIndexStates), c.retirement.repository))
	}
	vars["ids"] = ids
	statement := typedCollectionObservationSQL + `IF $collection != $expected OR $observation.state != 'collecting' OR $observation.running { THROW 'typed-stale'; };`
	operands := uint64(len(ids))
	var intent TypedIndexIntent
	raw := c.observation.Retirement.Intent
	if raw != "" {
		if typedDecode(raw, maxTypedIntentBytes, &intent) != nil {
			return batch, typedindex.Invalid
		}
		if !batch.More && intent.Desired != "" && c.observation.Retirement.DesiredRoot == c.retirement.root {
			if !intent.Canceled && !intent.RestoreRequired {
				return batch, typedindex.Stale
			}
			intent.Desired = ""
			body, e := typedEncode(intent, maxTypedIntentBytes)
			if e != nil {
				return batch, e
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
		return batch, s.typedFence(ctx, statement, vars)
	}
	if err = s.typedWrite(ctx, statement, vars, operands); err != nil {
		return TypedIndexCollectionBatch{}, err
	}
	batch.Mutations = int(operands)
	return batch, nil
}

// TypedIndexTombstoneExpiry is a coherent database selection, not evidence that
// filesystem custody has been drained. It cannot authorize deletion on its own.
type TypedIndexTombstoneExpiry struct {
	collection  TypedIndexCollection
	observation typedTombstoneObservation
	expected    map[string]any
}

type typedTombstoneObservation struct {
	Collection      typedCollectionObservation      `json:"collection"`
	Source          []typedSourceRecord             `json:"source"`
	Schedule        bool                            `json:"schedule"`
	Chunks          bool                            `json:"chunks"`
	ScheduleCurrent []typedTombstoneScheduleCurrent `json:"schedule_current"`
}
type typedTombstoneScheduleCurrent struct {
	Repository string `json:"repository"`
	Stage      string `json:"stage"`
	Digest     string `json:"schedule_digest"`
	Generation string `json:"generation"`
}

const typedTombstoneChunksSQL = `SELECT id FROM generation_schedule_chunk WHERE generation=$root AND stage='typed-index' LIMIT 1`
const typedTombstoneObservationSQL = typedCollectionObservationSQL + `
LET $expiry = {collection:$collection,
 source:(SELECT name ?? '' AS name, indexed_commit_hash ?? '' AS indexed_commit_hash,
 typed_incarnation ?? '' AS typed_incarnation, typed_source_epoch ?? 0 AS typed_source_epoch,
 deleting ?? false AS deleting, typed_incarnation != NONE AS has_incarnation,
 typed_source_epoch != NONE AS has_epoch FROM $repository_record LIMIT 1),
 schedule:array::len(SELECT id FROM $schedule LIMIT 1)>0,
 chunks:array::len(` + typedTombstoneChunksSQL + `)>0,
 schedule_current:(SELECT repository ?? '' AS repository, stage ?? '' AS stage,
 schedule_digest ?? '' AS schedule_digest, generation ?? '' AS generation FROM $schedule_current LIMIT 1)};
`

func tombstoneVariables(c TypedIndexCollection) (map[string]any, error) {
	stateID := "absent"
	if len(c.observation.StateOwner) == 1 {
		stateID = c.observation.StateOwner[0].ID
	}
	vars, err := collectionVariables(c.retirement, stateID)
	if err != nil {
		return nil, err
	}
	repo, root := c.retirement.repository, c.retirement.root
	digest := generationScheduleDigest(GenerationScheduleSpec{Repository: repo, Stage: TypedIndexScheduleStage, Generation: root, ResourceClass: GenerationResourceTypedIndex, TotalItems: 1, ChunkItems: 1, MaxAttempts: 3, RepositoryTokens: 1})
	vars["repository_record"] = repoID(repo)
	vars["schedule"] = models.NewRecordID("generation_schedule", strings.TrimPrefix(digest, "sha256:"))
	vars["schedule_current"] = models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(repo, TypedIndexScheduleStage), "sha256:"))
	return vars, nil
}

// InspectTypedIndexTombstoneExpiry requires the same global census and shared
// lifecycle guard as InspectTypedIndexCollection. It adds one coherent read of
// the collection, current source, exact schedule/current records and one indexed
// chunk-existence probe. Total: seven/eight SDK reads, no writes or filesystem
// work. Existing collection bounds apply; source/current add bounded scalars.
// An absent, deleting, unindexed or malformed current authority is held.
func (s *Surreal) InspectTypedIndexTombstoneExpiry(ctx context.Context, root string) (TypedIndexTombstoneExpiry, error) {
	var out TypedIndexTombstoneExpiry
	c, err := s.InspectTypedIndexCollection(ctx, root)
	if err != nil {
		return out, err
	}
	vars, err := tombstoneVariables(c)
	if err != nil {
		return out, err
	}
	if err = readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	result, err := storeQuery[[]typedTombstoneObservation](ctx, s.accounting, s.db, "BEGIN TRANSACTION;"+typedTombstoneObservationSQL+"RETURN [$expiry]; COMMIT TRANSACTION;", vars, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	rows := firstDomainRows(result)
	if len(rows) != 1 {
		return out, typedindex.Invalid
	}
	observed := rows[0]
	observedCollection, err := collectionObject(observed.Collection)
	if err != nil {
		return out, err
	}
	if !reflect.DeepEqual(observedCollection, c.expected) {
		return out, typedindex.Stale
	}
	raw, err := json.Marshal(observed)
	if err != nil {
		return out, typedindex.Invalid
	}
	var expected map[string]any
	if json.Unmarshal(raw, &expected) != nil {
		return out, typedindex.Invalid
	}
	expected["collection"] = observedCollection
	// json.Unmarshal's float64 must not round a valid source epoch above2^53.
	// Preserve the native integer in the SDK transaction parameter.
	sources, _ := expected["source"].([]any)
	for i, source := range sources {
		fields, ok := source.(map[string]any)
		if !ok {
			return out, typedindex.Invalid
		}
		fields["typed_source_epoch"] = observed.Source[i].Epoch
	}
	out = TypedIndexTombstoneExpiry{c, observed, expected}
	if err = validateTypedTombstone(ctx, out); err != nil {
		return TypedIndexTombstoneExpiry{}, err
	}
	return out, ctx.Err()
}
func validateTypedTombstone(ctx context.Context, selected TypedIndexTombstoneExpiry) error {
	c, o := selected.collection, selected.observation
	if err := validateTypedCollection(ctx, c); err != nil {
		return err
	}
	if selected.expected == nil || len(c.observation.Requests) != 1 || len(c.observation.Attempts) != 0 || len(c.observation.Plan) != 0 || o.Schedule || o.Chunks || len(o.Source) != 1 || len(o.ScheduleCurrent) > 1 {
		return typedindex.Stale
	}
	root, repo := c.retirement.root, c.retirement.repository
	if c.observation.Retirement.DesiredRoot == root || c.observation.Retirement.CurrentRoot == root || len(c.observation.StateOwner) == 1 && c.observation.StateOwner[0].Root == root {
		return typedindex.Stale
	}
	for _, current := range o.ScheduleCurrent {
		if current.Repository != repo || current.Stage != TypedIndexScheduleStage || !typedStoredDigest(current.Digest) || !typedStoredDigest(current.Generation) {
			return typedindex.Invalid
		}
		digest := generationScheduleDigest(GenerationScheduleSpec{Repository: repo, Stage: TypedIndexScheduleStage, Generation: root, ResourceClass: GenerationResourceTypedIndex, TotalItems: 1, ChunkItems: 1, MaxAttempts: 3, RepositoryTokens: 1})
		if current.Generation == root || current.Digest == digest {
			return typedindex.Stale
		}
	}
	source := o.Source[0]
	current, err := typedSource(source)
	if err != nil || !source.HasIncarnation || !source.HasEpoch || current.Repository != repo {
		return typedindex.Invalid
	}
	var stored typedIndexRequest
	var request typedindex.Request
	var intent TypedIndexIntent
	if typedDecode(c.observation.Requests[0].Body, typedindex.MaxRequestBytes+1024, &stored) != nil || typedDecode(stored.Raw, typedindex.MaxRequestBytes, &request) != nil {
		return typedindex.Invalid
	}
	intentRaw := c.observation.Retirement.Intent
	if validateTypedCensusControl(ctx, TypedIndexIntents, TypedIndexControl{ID: repo, Repository: repo, Body: intentRaw}) != nil || typedDecode(intentRaw, maxTypedIntentBytes, &intent) != nil {
		return typedindex.Invalid
	}
	// UpsertRepo preserves an existing incarnation and chooses a fresh random
	// server-owned token only for creation. Source/profile epochs never decrement
	// or wrap. Offline restore removes derived controls before authority reset.
	if current.Incarnation != request.Source.Incarnation {
		return nil
	}
	if source.Epoch < stored.SourceEpoch || uint64(intent.ProfileEpoch) < request.ProfileEpoch {
		return typedindex.Invalid
	}
	if source.Epoch == stored.SourceEpoch && current != request.Source {
		return typedindex.Invalid
	}
	if uint64(intent.ProfileEpoch) == request.ProfileEpoch && (intent.ProfileDigest != request.ProfileDigest || intent.UniverseDigest != request.UniverseDigest) {
		return typedindex.Invalid
	}
	if source.Epoch > stored.SourceEpoch || uint64(intent.ProfileEpoch) > request.ProfileEpoch {
		return nil
	}
	return typedindex.Stale
}

// ExpireDrainedTypedIndexTombstone deletes one empty collecting parent only
// after irreversible source/profile invalidation. Cancellation alone never
// qualifies. The caller must hold the shared lifecycle guard, complete the
// unfiltered census, prove native quiescence, and verify the entire physical root
// is drained/absent. Neither this selection nor database absence supplies that
// proof. Existing scheduler lifecycle must remove its references first.
// One transaction repeats every selected authority/reference observation and
// deletes exactly one parent (one mutation operand). No other root is changed.
func (s *Surreal) ExpireDrainedTypedIndexTombstone(ctx context.Context, selected TypedIndexTombstoneExpiry) error {
	if ctx == nil {
		return typedindex.Invalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTypedTombstone(ctx, selected); err != nil {
		return err
	}
	vars, err := tombstoneVariables(selected.collection)
	if err != nil {
		return err
	}
	vars["expected_expiry"] = selected.expected
	return s.typedWrite(ctx, typedTombstoneObservationSQL+`IF $expiry != $expected_expiry { THROW 'typed-stale'; }; DELETE $typed_root RETURN NONE;`, vars, 1)
}
