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

func (s *Surreal) typedAdmissionSnapshot(ctx context.Context, roots bool, vars map[string]any) (string, error) {
	query, kind, limit := typedAttemptAdmissionSQL, TypedIndexAttempts, MaxTypedIndexAttemptsPerRoot+1
	if roots {
		query, kind, limit = typedRootAdmissionSQL, TypedIndexRequests, 2*MaxTypedIndexRootsPerRepository+1
	}
	vars["scan_limit"] = limit
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return "", err
	}
	r, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, query, vars, storeRead())
	if err != nil {
		return "", typedError(ctx, err)
	}
	rows := firstDomainRows(r)
	parents := map[string]bool{}
	for _, row := range rows {
		if validateTypedControl(kind, row) != nil || row.Repository != vars["repository"] || !roots && row.Root != vars["root"] {
			return "", typedindex.Invalid
		}
		if row.Parent {
			parents[row.ID] = true
		}
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
