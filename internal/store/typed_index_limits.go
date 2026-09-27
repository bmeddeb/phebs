package store

import (
	"context"
	"encoding/json"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const MaxTypedIndexRootsPerRepository = 64
const MaxTypedIndexAttemptsPerRoot = 64

// A repository census includes successors and malformed parent projections;
// filtering is_parent would silently omit unknown controls. Neither query uses
// COUNT, body predicates, or sorting. The physical index stops at cap+sentinel.
const typedAdmissionProjection = `record::id(id) AS key, control_key ?? '' AS stored_key, repository, request_root, is_parent ?? false AS is_parent, custody_state ?? '' AS custody_state, growth_key ?? '' AS growth_key, body`
const typedRootAdmissionSQL = `SELECT ` + typedAdmissionProjection + ` FROM typed_index_request WHERE repository=$repository LIMIT $scan_limit`
const typedAttemptAdmissionSQL = `SELECT ` + typedAdmissionProjection + ` FROM typed_index_attempt WHERE request_root=$root LIMIT $scan_limit`

func (s *Surreal) typedRetainedControls(ctx context.Context, roots bool, vars map[string]any) ([]TypedIndexControl, map[string]bool, error) {
	query, kind, limit := typedAttemptAdmissionSQL, TypedIndexAttempts, MaxTypedIndexAttemptsPerRoot+1
	if roots {
		query, kind, limit = typedRootAdmissionSQL, TypedIndexRequests, 2*MaxTypedIndexRootsPerRepository+1
	}
	vars["scan_limit"] = limit
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return nil, nil, err
	}
	r, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, query, vars, storeRead())
	if err != nil {
		return nil, nil, typedError(ctx, err)
	}
	rows := firstDomainRows(r)
	parents := map[string]bool{}
	for _, row := range rows {
		if validateTypedControl(kind, row) != nil || row.Repository != vars["repository"] || !roots && row.Root != vars["root"] {
			return nil, nil, typedindex.Invalid
		}
		if row.Parent {
			parents[row.ID] = true
		}
	}
	return rows, parents, nil
}

func (s *Surreal) typedAdmissionSnapshot(ctx context.Context, roots bool, vars map[string]any) (string, error) {
	rows, parents, err := s.typedRetainedControls(ctx, roots, vars)
	if err != nil {
		return "", err
	}
	query, limit := typedAttemptAdmissionSQL, MaxTypedIndexAttemptsPerRoot+1
	if roots {
		query, limit = typedRootAdmissionSQL, 2*MaxTypedIndexRootsPerRepository+1
	}
	if roots {
		if len(rows) >= limit || len(parents) >= MaxTypedIndexRootsPerRepository {
			return "", typedindex.Capacity
		}
		for _, row := range rows {
			if !parents[row.Root] {
				return "", typedindex.Invalid
			}
		}
	} else if len(rows) >= MaxTypedIndexAttemptsPerRoot {
		return "", typedindex.Capacity
	}
	// Equality is a bounded observation fence. Common intent/state writes also
	// serialize phantom insertions: independent workers cannot both cross 63->65.
	raw, _ := json.Marshal(rows)
	var snapshot []map[string]any
	_ = json.Unmarshal(raw, &snapshot)
	if snapshot == nil {
		snapshot = []map[string]any{}
	}
	vars["control_snapshot"] = snapshot
	return `IF (` + query + `) != $control_snapshot { THROW 'typed-stale'; };`, nil
}

// InspectTypedIndexRetainedLimits checks persisted quotas after the unfiltered
// global census. Exactly64 retained roots/attempts are valid; admitting another
// still uses the stricter typedAdmissionSnapshot fence. The selected parent
// must belong to the repository, including obsolete/deleted incarnations.
// Two indexed reads return at most129 request and65 attempt bodies (1421KiB
// valid payload before decoding/SDK overhead), with a bounded parent-key set.
// These are observations, not growth admission, cleanup or current authority.
func (s *Surreal) InspectTypedIndexRetainedLimits(ctx context.Context, repository, root string) error {
	if ctx == nil || !typedControlKey(TypedIndexIntents, repository) || !typedStoredDigest(root) {
		return typedindex.Invalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	vars := map[string]any{"repository": repository, "root": root}
	rows, parents, err := s.typedRetainedControls(ctx, true, vars)
	if err != nil {
		return err
	}
	if len(rows) > 2*MaxTypedIndexRootsPerRepository || len(parents) > MaxTypedIndexRootsPerRepository {
		return typedindex.Capacity
	}
	if !parents[root] {
		return ErrNotFound
	}
	for _, row := range rows {
		if !parents[row.Root] || validateTypedCensusControl(ctx, TypedIndexRequests, row) != nil {
			return typedindex.Invalid
		}
	}
	attempts, _, err := s.typedRetainedControls(ctx, false, vars)
	if err != nil {
		return err
	}
	if len(attempts) > MaxTypedIndexAttemptsPerRoot {
		return typedindex.Capacity
	}
	for _, row := range attempts {
		if validateTypedCensusControl(ctx, TypedIndexAttempts, row) != nil {
			return typedindex.Invalid
		}
	}
	return nil
}
