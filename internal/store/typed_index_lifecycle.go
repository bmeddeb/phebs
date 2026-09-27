// Typed root inventory has no registered runtime caller. Each page is one SDK
// read of at most 65 bounded controls (requests <=9 KiB, others <=4 KiB).
// Retirement inspection is four SDK reads: root, intent, current, then one
// exact bounded observation with two direct request lookups and a running-lease
// existence probe. Mutation repeats that observation in one transaction and
// writes three supplied operands: parent scalar, exact schedule supersession,
// and exact scheduler-current removal. Two additional direct scheduler lookups
// occur inside that mutation transaction. No chunks or counters are changed; it
// never deletes or hashes files. No SQL body filter,
// full-corpus count, cache, child, filesystem work or process lock is added.
// Typed scheduler/no-op root checks add one direct read; atomic writer fences
// add root reads inside existing transactions (typed heartbeat now wraps its
// single write in a transaction). Ordinary worker statements bind nine immutable
// lease-descriptor scalars without another SDK call or row mutation.
package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// These are prospective store-control bounds, not native execution limits.
const MaxTypedIndexLifecycleRows = 64

type TypedIndexControlKind string

const (
	TypedIndexRequests TypedIndexControlKind = "typed_index_request"
	TypedIndexAttempts TypedIndexControlKind = "typed_index_attempt"
	TypedIndexPlans    TypedIndexControlKind = "typed_index_plan"
)

// TypedIndexControl is bounded control metadata, never filesystem authority.
type TypedIndexControl struct {
	ID         string `json:"key"`
	StoredKey  string `json:"stored_key"`
	Repository string `json:"repository"`
	Root       string `json:"request_root"`
	Parent     bool   `json:"is_parent"`
	State      string `json:"custody_state"`
	Body       string `json:"body"`
}
type TypedIndexControlPage struct {
	Rows []TypedIndexControl
	Next string
}

const typedControlProjection = `id, control_key, record::id(id) AS key, control_key ?? '' AS stored_key, repository, request_root, is_parent ?? false AS is_parent, custody_state ?? '' AS custody_state, body`

func validateTypedControl(kind TypedIndexControlKind, row TypedIndexControl) error {
	if !validSHA256(row.ID) || row.StoredKey != row.ID || !validSHA256(row.Root) || reponame.Validate(row.Repository) != nil || len(row.Repository) > 512 {
		return typedindex.Invalid
	}
	switch kind {
	case TypedIndexRequests:
		var r typedIndexRequest
		var wire typedindex.Request
		if typedDecode(row.Body, typedindex.MaxRequestBytes+1024, &r) != nil || r.Root != row.Root || r.SourceEpoch < 1 || typedDecode(r.Raw, typedindex.MaxRequestBytes, &wire) != nil || typedDigest([]byte(r.Raw)) != row.ID || wire.Source.Repository != row.Repository {
			return typedindex.Invalid
		}
		if row.Parent {
			if row.ID != row.Root || wire.Action != typedindex.Plan || wire.ParentRequestDigest != "" || (row.State != "live" && row.State != "collecting") {
				return typedindex.Invalid
			}
		} else if row.ID == row.Root || wire.Action != typedindex.Execute || wire.ParentRequestDigest != row.Root || row.State != "" {
			return typedindex.Invalid
		}
	case TypedIndexAttempts:
		var a typedIndexAttempt
		if typedDecode(row.Body, maxTypedControlBytes, &a) != nil || !validTypedAttempt(a) || a.Root != row.Root || row.Parent || row.State != "" {
			return typedindex.Invalid
		}
	case TypedIndexPlans:
		var p typedIndexPlan
		if typedDecode(row.Body, maxTypedControlBytes, &p) != nil || !validSHA256(p.Digest) || !validSHA256(p.Successor) || row.ID != row.Root || row.Parent || row.State != "" {
			return typedindex.Invalid
		}
	default:
		return typedindex.Invalid
	}
	return nil
}

// Exact control reads validate lifecycle projections before ordinary execution
// can interpret old or corrupt rows. Missing projection is not a migration.
func (s *Surreal) typedReadControl(ctx context.Context, kind TypedIndexControlKind, key string) (string, error) {
	if kind != TypedIndexRequests && kind != TypedIndexAttempts && kind != TypedIndexPlans {
		return "", typedindex.Invalid
	}
	if !validSHA256(key) {
		return "", typedindex.Invalid
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return "", err
	}
	result, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, "SELECT "+typedControlProjection+" FROM $rid LIMIT 1", map[string]any{"rid": typedID(string(kind), key)}, storeRead())
	if err != nil {
		return "", typedError(ctx, err)
	}
	rows := firstDomainRows(result)
	if len(rows) == 0 {
		return "", nil
	}
	if len(rows) != 1 || rows[0].ID != key || validateTypedControl(kind, rows[0]) != nil {
		return "", typedindex.Invalid
	}
	return rows[0].Body, nil
}

// ScanTypedIndexRoots walks record IDs, including successors so a malformed or
// missing parent projection cannot hide a row. Callers select Parent rows after
// validation. One SDK read, at most limit+1 bounded bodies; no JSON predicates.
// ScanTypedIndexControls is the global, closed-table inventory path. Recovery
// must walk it for each kind before relying on root-indexed absence: a missing
// projection is deliberately invisible to that index and must fail this census.
func (s *Surreal) ScanTypedIndexControls(ctx context.Context, kind TypedIndexControlKind, after string, limit int) (TypedIndexControlPage, error) {
	return s.scanTypedIndexControls(ctx, kind, "", after, limit)
}

func (s *Surreal) ScanTypedIndexRoots(ctx context.Context, after string, limit int) (TypedIndexControlPage, error) {
	return s.scanTypedIndexControls(ctx, TypedIndexRequests, "", after, limit)
}
func (s *Surreal) ScanTypedIndexRootChildren(ctx context.Context, root string, kind TypedIndexControlKind, after string, limit int) (TypedIndexControlPage, error) {
	if !validSHA256(root) {
		return TypedIndexControlPage{}, typedindex.Invalid
	}
	return s.scanTypedIndexControls(ctx, kind, root, after, limit)
}
func (s *Surreal) scanTypedIndexControls(ctx context.Context, kind TypedIndexControlKind, root, after string, limit int) (TypedIndexControlPage, error) {
	if kind != TypedIndexRequests && kind != TypedIndexAttempts && kind != TypedIndexPlans || limit < 1 || limit > MaxTypedIndexLifecycleRows || after != "" && !validSHA256(after) {
		return TypedIndexControlPage{}, typedindex.Invalid
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return TypedIndexControlPage{}, err
	}
	table := string(kind)
	from := table
	if after != "" {
		cursorID := typedID(table, after)
		from = cursorID.String() + ">.."
	}
	statement := "SELECT " + typedControlProjection + " FROM " + from
	vars := map[string]any{"limit": limit + 1, "root": root}
	if root != "" {
		vars["after"] = after
		statement = "SELECT " + typedControlProjection + " FROM " + table + " WHERE request_root=$root AND control_key>$after ORDER BY control_key LIMIT $limit;"
	} else {
		statement += " ORDER BY id LIMIT $limit;"
	}
	results, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, statement, vars, storeRead())
	if err != nil {
		return TypedIndexControlPage{}, typedError(ctx, err)
	}
	rows := firstDomainRows(results)
	if len(rows) > limit+1 {
		return TypedIndexControlPage{}, typedindex.Invalid
	}
	previous := after
	for _, row := range rows {
		if validateTypedControl(kind, row) != nil || row.ID <= previous || root != "" && row.Root != root {
			return TypedIndexControlPage{}, typedindex.Invalid
		}
		previous = row.ID
	}
	page := TypedIndexControlPage{Rows: rows}
	if len(rows) > limit {
		page.Rows = rows[:limit]
		page.Next = page.Rows[limit-1].ID
	}
	return page, nil
}

// Every root-affecting typed writer uses this inside its mutation transaction.
const typedRootFenceSQL = `
LET $typed_root_row = (SELECT repository, request_root, control_key, is_parent, custody_state FROM $typed_root LIMIT 1)[0];
IF $typed_root_row = NONE OR $typed_root_row.repository != $repository OR
 $typed_root_row.request_root != record::id($typed_root) OR $typed_root_row.control_key != record::id($typed_root) OR $typed_root_row.is_parent != true OR
 $typed_root_row.custody_state != 'live' { THROW 'typed-stale'; };
`

func (s *Surreal) typedLiveRoot(ctx context.Context, repository, root string) error {
	if !validSHA256(root) {
		return typedindex.Invalid
	}
	return s.typedFence(ctx, typedRootFenceSQL, map[string]any{"typed_root": typedID(string(TypedIndexRequests), root), "repository": repository})
}

// TypedIndexRetirement is an opaque exact observation. It grants no filesystem
// cleanup authority or quiescence proof; reader pins remain the owner's job.
type TypedIndexRetirement struct {
	root, repository          string
	expected                  map[string]any
	current, desired, running bool
	collecting                bool
}

func (r TypedIndexRetirement) Protected() bool  { return r.current || r.desired || r.running }
func (r TypedIndexRetirement) Collecting() bool { return r.collecting }
func (r TypedIndexRetirement) Protection() (current, desired, running bool) {
	return r.current, r.desired, r.running
}

type typedRetirementObservation struct {
	RootBody          string `json:"root_body"`
	RootRepository    string `json:"root_repository"`
	RootProjection    string `json:"root_projection"`
	RootKey           string `json:"root_key"`
	Parent            bool   `json:"parent"`
	State             string `json:"state"`
	Intent            string `json:"intent"`
	Current           string `json:"current"`
	DesiredBody       string `json:"desired_body"`
	DesiredRoot       string `json:"desired_root"`
	DesiredRepository string `json:"desired_repository"`
	DesiredKey        string `json:"desired_key"`
	CurrentBody       string `json:"current_body"`
	CurrentRoot       string `json:"current_root"`
	CurrentRepository string `json:"current_repository"`
	CurrentKey        string `json:"current_key"`
	Running           bool   `json:"running"`
}

const typedRetirementObservationSQL = `
LET $r = (SELECT body, repository, request_root, control_key, is_parent, custody_state FROM $typed_root LIMIT 1)[0];
LET $d = (SELECT body, request_root, control_key, repository FROM $desired_request LIMIT 1)[0];
LET $c = (SELECT body, request_root, control_key, repository FROM $current_request LIMIT 1)[0];
LET $observation = {
 root_body:$r.body ?? '', root_repository:$r.repository ?? '', root_projection:$r.request_root ?? '', root_key:$r.control_key ?? '',
 parent:$r.is_parent ?? false, state:$r.custody_state ?? '',
 intent:(SELECT body FROM $intent LIMIT 1)[0].body ?? '',
 current:(SELECT body FROM $current LIMIT 1)[0].body ?? '',
 desired_body:$d.body ?? '', desired_root:$d.request_root ?? '', desired_repository:$d.repository ?? '', desired_key:$d.control_key ?? '',
 current_body:$c.body ?? '', current_root:$c.request_root ?? '', current_repository:$c.repository ?? '', current_key:$c.control_key ?? '',
 running:array::len(SELECT id FROM generation_schedule_chunk WHERE generation=$root
 AND stage='typed-index' AND status='running' LIMIT 1)>0
};
`

func (s *Surreal) InspectTypedIndexRetirement(ctx context.Context, root string) (TypedIndexRetirement, error) {
	var out TypedIndexRetirement
	if !validSHA256(root) {
		return out, typedindex.Invalid
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	results, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, "SELECT "+typedControlProjection+" FROM $rid LIMIT 1", map[string]any{"rid": typedID(string(TypedIndexRequests), root)}, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || validateTypedControl(TypedIndexRequests, rows[0]) != nil || !rows[0].Parent {
		return out, typedindex.Invalid
	}
	row := rows[0]
	intentRaw, err := s.typedRead(ctx, "typed_index_intent", row.Repository)
	if err != nil {
		return out, err
	}
	currentRaw, err := s.typedRead(ctx, "typed_index_current", row.Repository)
	if err != nil {
		return out, err
	}
	var intent TypedIndexIntent
	var current typedindex.PublicationPointer
	if intentRaw != "" && (typedDecode(intentRaw, maxTypedIntentBytes, &intent) != nil || intent.Repository != row.Repository || intent.Desired != "" && !validSHA256(intent.Desired)) {
		return out, typedindex.Invalid
	}
	if intentRaw != "" {
		profile, err := typedindex.DecodeProfile(ctx, []byte(intent.ProfileJSON))
		if err != nil || intent.ProfileEpoch < 1 || profile.Digest() != intent.ProfileDigest || !validSHA256(intent.UniverseDigest) {
			return out, typedindex.Invalid
		}
	}
	if currentRaw != "" && (typedDecode(currentRaw, maxTypedControlBytes, &current) != nil || !validSHA256(current.Binding.RequestDigest) || !validSHA256(current.RootDigest) || current.Epoch < 1) {
		return out, typedindex.Invalid
	}
	vars := typedRetirementVariables(root, row.Repository, intent.Desired, current.Binding.RequestDigest)
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	observed, err := storeQuery[[]typedRetirementObservation](ctx, s.accounting, s.db, typedRetirementObservationSQL+"RETURN [$observation];", vars, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	observations := firstDomainRows(observed)
	if len(observations) != 1 {
		return out, typedindex.Invalid
	}
	o := observations[0]
	if o.RootBody != row.Body || o.RootRepository != row.Repository || o.RootProjection != root || o.RootKey != root || !o.Parent || o.State != row.State || o.Intent != intentRaw || o.Current != currentRaw {
		return out, typedindex.Stale
	}
	for _, ref := range []struct{ id, body, projection, repository, key string }{{intent.Desired, o.DesiredBody, o.DesiredRoot, o.DesiredRepository, o.DesiredKey}, {current.Binding.RequestDigest, o.CurrentBody, o.CurrentRoot, o.CurrentRepository, o.CurrentKey}} {
		if ref.id == "" {
			continue
		}
		var request typedIndexRequest
		if typedDecode(ref.body, typedindex.MaxRequestBytes+1024, &request) != nil || request.Root != ref.projection || !validSHA256(request.Root) || ref.repository != row.Repository || ref.key != ref.id || typedDigest([]byte(request.Raw)) != ref.id {
			return out, typedindex.Invalid
		}
		var wire typedindex.Request
		if typedDecode(request.Raw, typedindex.MaxRequestBytes, &wire) != nil || wire.Source.Repository != row.Repository ||
			(wire.Action == typedindex.Plan && (request.Root != ref.id || wire.ParentRequestDigest != "")) ||
			(wire.Action == typedindex.Execute && wire.ParentRequestDigest != request.Root) ||
			(wire.Action != typedindex.Plan && wire.Action != typedindex.Execute) {
			return out, typedindex.Invalid
		}
	}
	if currentRaw != "" {
		var request typedIndexRequest
		var wire typedindex.Request
		if typedDecode(o.CurrentBody, typedindex.MaxRequestBytes+1024, &request) != nil || typedDecode(request.Raw, typedindex.MaxRequestBytes, &wire) != nil || wire.Action != typedindex.Execute || current.Binding.Source != wire.Source || current.Binding.ProfileDigest != wire.ProfileDigest || current.Binding.ToolsDigest != wire.ToolsDigest || current.Binding.PlanDigest != wire.PlanDigest {
			return out, typedindex.Invalid
		}
	}
	raw, _ := json.Marshal(o)
	var expected map[string]any
	if json.Unmarshal(raw, &expected) != nil {
		return out, typedindex.Invalid
	}
	out = TypedIndexRetirement{root: root, repository: row.Repository, expected: expected, current: currentRaw != "" && o.CurrentRoot == root, desired: !intent.Canceled && !intent.RestoreRequired && intent.Desired != "" && o.DesiredRoot == root, running: o.Running, collecting: o.State == "collecting"}
	return out, nil
}
func typedRetirementVariables(root, repository, desired, current string) map[string]any {
	// Absent references use an impossible fixed key in the same closed table.
	if desired == "" {
		desired = "absent"
	}
	if current == "" {
		current = "absent"
	}
	return map[string]any{"root": root, "repository": repository, "typed_root": typedID("typed_index_request", root), "intent": typedID("typed_index_intent", repository), "current": typedID("typed_index_current", repository), "desired_request": typedID("typed_index_request", desired), "current_request": typedID("typed_index_request", current)}
}

// BeginTypedIndexRetirement marks one parent irreversibly collecting. No rows
// or bytes of typed custody are deleted; its replay tombstone remains after
// later cleanup. Its exact scheduler current is retired in the same transaction.
// Caller must separately hold the publication mutation guard before unlinking.
func (s *Surreal) BeginTypedIndexRetirement(ctx context.Context, selection TypedIndexRetirement) error {
	if !validSHA256(selection.root) || selection.expected == nil || selection.Protected() {
		return typedindex.Stale
	}
	var intent TypedIndexIntent
	var current typedindex.PublicationPointer
	if raw, _ := selection.expected["intent"].(string); raw != "" {
		if typedDecode(raw, maxTypedIntentBytes, &intent) != nil {
			return typedindex.Invalid
		}
	}
	if raw, _ := selection.expected["current"].(string); raw != "" {
		if typedDecode(raw, maxTypedControlBytes, &current) != nil {
			return typedindex.Invalid
		}
	}
	vars := typedRetirementVariables(selection.root, selection.repository, intent.Desired, current.Binding.RequestDigest)
	vars["expected"] = selection.expected
	digest := generationScheduleDigest(GenerationScheduleSpec{Repository: selection.repository, Stage: TypedIndexScheduleStage, Generation: selection.root, ResourceClass: GenerationResourceTypedIndex, TotalItems: 1, ChunkItems: 1, MaxAttempts: 3, RepositoryTokens: 1})
	vars["schedule_digest"] = digest
	vars["retiring_schedule"] = models.NewRecordID("generation_schedule", strings.TrimPrefix(digest, "sha256:"))
	vars["schedule_current"] = models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(selection.repository, TypedIndexScheduleStage), "sha256:"))
	return s.typedWrite(ctx, typedRetirementObservationSQL+`
IF $observation != $expected OR $observation.running OR $observation.state NOT IN ['live','collecting'] { THROW 'typed-stale'; };
UPDATE $typed_root SET custody_state='collecting' RETURN NONE;
LET $current_digest = (SELECT schedule_digest FROM $schedule_current
 WHERE repository=$repository AND stage='typed-index' AND generation=$root LIMIT 1)[0].schedule_digest;
LET $stored_digest = (SELECT digest FROM $retiring_schedule
 WHERE repository=$repository AND stage='typed-index' AND generation=$root
 AND digest=$schedule_digest LIMIT 1)[0].digest;
IF $current_digest=$schedule_digest AND $stored_digest=$schedule_digest {
 UPDATE $retiring_schedule SET status='superseded', updated_at=time::now()
 WHERE status='active' RETURN NONE;
 DELETE $schedule_current WHERE schedule_digest=$schedule_digest
 AND repository=$repository AND stage='typed-index' AND generation=$root RETURN NONE;
};`, vars, 3)
}

// This fence is injected only for typed workers. Lease revocation by the
// scheduler remains allowed; it grants no new work or publication authority.
func typedChunkStatement(statement string, chunk GenerationChunk) string {
	// The complete immutable descriptor participates in the existing lease
	// predicate for every class. A caller cannot relabel a typed lease or redirect
	// its counters/current pointer or retry successor. Values are structured SDK
	// parameters, never SQL literals. Malformed labels execute no mutation.
	if chunk.Stage == "" || len(chunk.Stage) > MaxGenerationStageBytes || !validGenerationToken(chunk.Stage) || !validGenerationResourceClass(chunk.ResourceClass) {
		return "THROW 'typed-stale';"
	}
	statement = strings.ReplaceAll(statement, "AND lease_token = $lease AND claimed_by = $worker", "AND lease_token = $lease AND claimed_by = $worker"+generationLeaseScopeSQL)
	if chunk.ResourceClass != GenerationResourceTypedIndex && chunk.Stage != TypedIndexScheduleStage {
		return statement
	}
	fence := `
 LET $typed_worker = (SELECT repository,generation,stage,resource_class FROM $chunk LIMIT 1)[0];
 IF $typed_worker.stage != 'typed-index' OR $typed_worker.resource_class != 'typed-index' { THROW 'typed-stale'; };
 LET $worker_root = type::record('typed_index_request',$typed_worker.generation);
 ` + strings.NewReplacer("$typed_root", "$worker_root", "$repository", "$typed_worker.repository").Replace(typedRootFenceSQL)
	if strings.Contains(statement, "BEGIN;") {
		return strings.Replace(statement, "BEGIN;", "BEGIN;"+fence, 1)
	}
	return "BEGIN;" + fence + statement + ";COMMIT;"
}

func generationLeaseScope(chunk GenerationChunk) map[string]any {
	return map[string]any{
		"identity": chunk.Identity, "schedule_digest": chunk.ScheduleDigest,
		"repository": chunk.Repository, "stage": chunk.Stage, "generation": chunk.Generation,
		"resource_class": chunk.ResourceClass, "offset": chunk.Offset, "length": chunk.Length, "attempt": chunk.Attempt,
	}
}

const generationLeaseScopeSQL = ` AND identity=$lease_scope.identity AND schedule_digest=$lease_scope.schedule_digest
 AND repository=$lease_scope.repository AND stage=$lease_scope.stage
 AND generation=$lease_scope.generation AND resource_class=$lease_scope.resource_class
 AND offset=$lease_scope.offset AND length=$lease_scope.length AND attempt=$lease_scope.attempt`
