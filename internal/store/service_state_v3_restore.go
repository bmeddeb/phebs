package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/fxamacker/cbor/v2"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

// RestoreSelectedServiceStateV3ForRestore rolls selected imported state back
// to its exact v3 selector snapshot and discards unselected rebuildable state.
// Restore calls this before deleting the plans that explain partial rows.
func (s *Surreal) RestoreSelectedServiceStateV3ForRestore(ctx context.Context) error {
	if _, err := s.ValidateServiceCatalogV3Precious(ctx); err != nil {
		return fmt.Errorf("restore selected service state v3: validate: %w", err)
	}
	selectors, err := s.ListServiceRuntimeSelectors(ctx)
	if err != nil {
		return fmt.Errorf("restore selected service state v3: selectors: %w", err)
	}
	for _, selector := range selectors {
		if selector.Backend != ServiceRuntimeV3 {
			continue
		}
		summary, summaryErr := s.GetServiceStateV3SummarySnapshot(
			ctx,
			selector.Repository,
			selector.StateControlRevision,
			selector.StateSummaryDigest,
		)
		if summaryErr != nil {
			return fmt.Errorf("restore selected service state v3 for %q: summary: %w", selector.Repository, summaryErr)
		}
		if err := s.restoreSelectedServiceStateV3Snapshot(ctx, selector, summary); err != nil {
			return err
		}
	}
	return s.discardUnselectedServiceStateV3ForRestore(ctx, selectors)
}

func (s *Surreal) discardUnselectedServiceStateV3ForRestore(
	ctx context.Context,
	selectors []ServiceRuntimeSelector,
) error {
	selected := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		if selector.Backend == ServiceRuntimeV3 {
			selected[selector.Repository] = struct{}{}
		}
	}
	type stateKey struct {
		Repository string `json:"repository"`
		ServiceKey string `json:"service_key"`
	}
	const maxRows = servicecatalogv3.MaxTotalServices * 2
	rowResults, err := storeQuery[[]stateKey](ctx, s.accounting, s.db, `
SELECT repository, service_key FROM service_state_v3_current
	ORDER BY repository, service_key LIMIT $limit`, map[string]any{
		"limit": maxRows + 1,
	}, storeRead())
	if err != nil {
		return fmt.Errorf("discard unselected service state v3: rows: %w", err)
	}
	rows := firstDomainRows(rowResults)
	if len(rows) > maxRows {
		return fmt.Errorf("discard unselected service state v3: rows: %w", ErrInvalidServiceStateV3)
	}
	type summaryKey struct {
		Repository string `json:"repository"`
	}
	summaryResults, err := storeQuery[[]summaryKey](ctx, s.accounting, s.db, `
SELECT repository FROM service_state_v3_repository
	ORDER BY repository LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3LifecycleRoots + 1,
	}, storeRead())
	if err != nil {
		return fmt.Errorf("discard unselected service state v3: summaries: %w", err)
	}
	summaries := firstDomainRows(summaryResults)
	if len(summaries) > MaxServiceCatalogV3LifecycleRoots {
		return fmt.Errorf("discard unselected service state v3: summaries: %w", ErrInvalidServiceStateV3)
	}
	unselected := make(map[string]struct{})
	for _, row := range rows {
		if _, retained := selected[row.Repository]; !retained {
			unselected[row.Repository] = struct{}{}
		}
	}
	for _, summary := range summaries {
		if _, retained := selected[summary.Repository]; !retained {
			unselected[summary.Repository] = struct{}{}
		}
	}
	repositories := make([]string, 0, len(unselected))
	for repository := range unselected {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	for _, repository := range repositories {
		const guard = `
LET $selected = SELECT id FROM service_runtime_selector
	WHERE repository = $repository AND backend = 'v3' LIMIT 1;
IF array::len($selected) != 0 {
	THROW 'phebs-permanent: unselected service state v3 restore fence changed';
};`
		for _, table := range []string{
			"service_state_v3_current", "service_state_v3_repository",
			"service_state_v3_preimage", "service_state_v3_repository_preimage",
		} {
			if err := s.clearRestoreTable(ctx, table, " WHERE repository = $repository", guard,
				map[string]any{"repository": repository}); err != nil {
				return fmt.Errorf("discard unselected service state v3 for %q: %w", repository, err)
			}
		}
	}
	return nil
}

func (s *Surreal) restoreSelectedServiceStateV3Snapshot(
	ctx context.Context,
	selector ServiceRuntimeSelector,
	summary servicecatalog.RepositoryState,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if validateServiceRuntimeSelector(selector) != nil || selector.Backend != ServiceRuntimeV3 ||
		servicecatalogv3.ValidateRepositoryState(summary, true) != nil ||
		summary.Repository != selector.Repository ||
		summary.ControlRevision != selector.StateControlRevision ||
		summary.SummaryDigest != selector.StateSummaryDigest {
		return fmt.Errorf("restore selected service state v3 for %q: %w", selector.Repository, ErrInvalidServiceStateV3)
	}
	const maxRows = servicecatalogv3.MaxTotalServices * 2
	vars := map[string]any{
		"repository": selector.Repository, "snapshot_revision": selector.StateControlRevision,
		"snapshot_digest": selector.StateSummaryDigest, "limit": maxRows + 1,
		"selector_rid":      serviceRuntimeSelectorID(selector.Repository),
		"expected_selector": serviceRuntimeSelectorContent(selector),
		"summary_rid":       serviceStateV3RepositoryID(selector.Repository),
		"summary_content":   serviceRepositoryStateContent(summary),
	}
	summaries, err := s.restoreStateV3RawRows(ctx, `SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 2`, vars, 1)
	if err != nil {
		return fmt.Errorf("restore selected service state v3 for %q: summary preimage: %w", selector.Repository, err)
	}
	preimages, err := s.restoreStateV3RawRows(ctx, `SELECT * FROM service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest
	ORDER BY service_key LIMIT $limit`, vars, maxRows)
	if err != nil {
		return fmt.Errorf("restore selected service state v3 for %q: rows: %w", selector.Repository, err)
	}
	future, err := s.restoreStateV3RawRows(ctx, `SELECT `+restoreStateV3FutureFields+` FROM service_state_v3_current
	WHERE repository = $repository AND visible_from > $snapshot_revision
	ORDER BY id LIMIT $limit`, vars, maxRows)
	if err != nil {
		return fmt.Errorf("restore selected service state v3 for %q: future rows: %w", selector.Repository, err)
	}
	if len(summaries) == 0 && len(preimages) == 0 && len(future) == 0 {
		return nil
	}
	// ponytail: this offline, unserved target retains one bounded census (at most
	// 25,000 preimages and future projections), not a new staging schema. Full
	// preimage bodies retain their existing row-shape cost; the row limit is not
	// a pre-decode byte or historical native-scan bound. Future projections bind
	// supported writers' native identity/revision/digest controls, not arbitrary
	// direct-SQL changes to omitted fields. Revisit streaming only if the existing
	// restore admission's memory profile requires it.
	codec := surrealcbor.New()
	futureIDs := make([]models.RecordID, 0, len(future))
	futureKeys := make(map[string]bool, len(future))
	for _, raw := range future {
		if err := ctx.Err(); err != nil {
			return err
		}
		var row serviceStateRec
		if err := codec.Unmarshal(raw, &row); err != nil {
			return fmt.Errorf("decode restore future row: %w", err)
		}
		expected := serviceStateV3ID(selector.Repository, row.ServiceKey)
		identifier, _ := expected.ID.(string)
		if row.Repository != selector.Repository || row.ServiceKey == "" ||
			len(row.ServiceKey) > servicecatalog.MaxServiceKeyBytes || row.ControlRevision == 0 ||
			!validSHA256Digest(row.StateDigest) || row.VisibleFrom <= selector.StateControlRevision ||
			!validServiceCatalogV3RecordID(row.RecID, "service_state_v3_current", identifier) ||
			futureKeys[row.ServiceKey] {
			return ErrInvalidServiceStateV3
		}
		futureKeys[row.ServiceKey] = true
		futureIDs = append(futureIDs, *row.RecID)
	}
	updates := make([]map[string]any, 0, len(preimages))
	previousKey := ""
	for _, raw := range preimages {
		if err := ctx.Err(); err != nil {
			return err
		}
		var preimage serviceStateRec
		if err := codec.Unmarshal(raw, &preimage); err != nil {
			return fmt.Errorf("decode restore preimage: %w", err)
		}
		state, stateErr := decodeServiceStateV3Preimage(
			selector.Repository,
			preimage.ServiceKey,
			selector.StateControlRevision,
			selector.StateSummaryDigest,
			preimage,
		)
		if stateErr != nil || preimage.ServiceKey <= previousKey || !futureKeys[preimage.ServiceKey] {
			return fmt.Errorf("restore selected service state v3 for %q: row: %w", selector.Repository, ErrInvalidServiceStateV3)
		}
		previousKey = preimage.ServiceKey
		content := serviceStateContent(state)
		content["visible_from"] = preimage.VisibleFrom
		updates = append(updates, map[string]any{
			"rid": serviceStateV3ID(selector.Repository, preimage.ServiceKey), "content": content,
			"preimage_rid": *preimage.RecID, "preimage": raw,
		})
	}
	summaryIDs := make([]models.RecordID, 0, len(summaries))
	for _, raw := range summaries {
		var row serviceRepositoryStateRec
		if err := codec.Unmarshal(raw, &row); err != nil {
			return fmt.Errorf("decode restore summary preimage: %w", err)
		}
		prior, err := serviceStateV3RepositoryFromRec(row)
		if err != nil || !sameServiceStateV3Summary(*prior, summary) ||
			row.SnapshotRevision != selector.StateControlRevision || row.SnapshotDigest != selector.StateSummaryDigest ||
			!validServiceStateV3PreimageRecord(row.RecID, "service_state_v3_repository_preimage") {
			return ErrInvalidServiceStateV3
		}
		summaryIDs = append(summaryIDs, *row.RecID)
	}
	currentSummary, err := s.restoreStateV3RawRows(ctx, "SELECT * FROM $summary_rid LIMIT 2", vars, 1)
	if err != nil {
		return fmt.Errorf("read restore current summary: %w", err)
	}
	vars["expected_summaries"], vars["expected_current_summary"] = summaries, currentSummary
	vars["summary_preimage_ids"] = summaryIDs
	// Every payload target is prevalidated before the first mutation. The exact
	// imported native preimages are echoed without re-encoding their values.
	for start := 0; start < len(future); start += restoreClearRows {
		end := min(start+restoreClearRows, len(future))
		ids := futureIDs[start:end]
		vars["future_ids"], vars["expected_future"] = ids, future[start:end]
		if err := s.restoreClearWrite(ctx, restoreStateV3DeleteFutureSQL, vars, uint64(len(ids))); err != nil {
			return err
		}
	}
	delete(vars, "future_ids")
	delete(vars, "expected_future")
	for start := 0; start < len(updates); start += restoreClearRows / 2 {
		pairs := updates[start:min(start+restoreClearRows/2, len(updates))]
		vars["updates"] = pairs
		if err := s.restoreClearWrite(ctx, restoreStateV3PairsSQL, vars, uint64(2*len(pairs))); err != nil {
			return err
		}
	}
	delete(vars, "updates")
	return s.restoreClearWrite(ctx, restoreStateV3SummarySQL, vars, uint64(1+len(summaryIDs)))
}

// Native raw values are kept for exact per-page comparison, not treated as new
// authority. The only caller has already validated the imported precious state.
func (s *Surreal) restoreStateV3RawRows(ctx context.Context, query string, vars map[string]any, maximum int) ([]cbor.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results, err := storeQuery[[]cbor.RawMessage](ctx, s.accounting, s.db, query, vars, storeRead())
	if err != nil {
		return nil, err
	}
	if results == nil || len(*results) != 1 || (*results)[0].Status != "OK" || (*results)[0].Error != nil {
		return nil, ErrInvalidServiceStateV3
	}
	rows := (*results)[0].Result
	if rows == nil || len(rows) > maximum {
		return nil, ErrInvalidServiceStateV3
	}
	return rows, ctx.Err()
}

const restoreStateV3FutureFields = `id, repository, service_key, control_revision, state_digest, visible_from`

const restoreStateV3Guard = `
LET $selector = (SELECT schema, repository, backend,
	catalog_generation_digest, catalog_root_digest, catalog_control_revision,
	state_control_revision, state_summary_digest, search_generation_digest,
	relationship_generation_digest, relationship_root_digest,
	control_revision, digest, changed_at FROM $selector_rid LIMIT 1)[0];
LET $selector_ok = $selector != NONE
	AND $selector.schema = $expected_selector.schema
	AND $selector.repository = $expected_selector.repository
	AND $selector.backend = $expected_selector.backend
	AND $selector.catalog_generation_digest = $expected_selector.catalog_generation_digest
	AND $selector.catalog_root_digest = $expected_selector.catalog_root_digest
	AND $selector.catalog_control_revision = $expected_selector.catalog_control_revision
	AND $selector.state_control_revision = $expected_selector.state_control_revision
	AND $selector.state_summary_digest = $expected_selector.state_summary_digest
	AND $selector.search_generation_digest = $expected_selector.search_generation_digest
	AND $selector.relationship_generation_digest = $expected_selector.relationship_generation_digest
	AND $selector.relationship_root_digest = $expected_selector.relationship_root_digest
	AND $selector.control_revision = $expected_selector.control_revision
	AND $selector.digest = $expected_selector.digest
	AND $selector.changed_at = $expected_selector.changed_at;
LET $summaries = SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 2;
LET $current_summary = SELECT * FROM $summary_rid LIMIT 2;
IF !$selector_ok OR $summaries != $expected_summaries OR $current_summary != $expected_current_summary {
	THROW 'phebs-permanent: selected service state v3 restore fence changed';
};`

const restoreStateV3DeleteFutureSQL = "BEGIN;" + restoreStateV3Guard + `
LET $actual = SELECT ` + restoreStateV3FutureFields + ` FROM $future_ids ORDER BY id;
IF $actual != $expected_future {
 THROW 'phebs-permanent: selected service state v3 future row changed';
};
FOR $rid IN $future_ids { DELETE $rid RETURN NONE; };
COMMIT;`

const restoreStateV3PairsSQL = "BEGIN;" + restoreStateV3Guard + `
FOR $update IN $updates {
	LET $actual = (SELECT * FROM $update.preimage_rid LIMIT 1)[0];
	LET $current = SELECT id FROM $update.rid LIMIT 1;
	IF $actual != $update.preimage OR array::len($current) != 0 {
		THROW 'phebs-permanent: selected service state v3 restore row changed';
	};
	UPSERT $update.rid CONTENT $update.content RETURN NONE;
	DELETE $update.preimage_rid RETURN NONE;
};
COMMIT;`

const restoreStateV3SummarySQL = "BEGIN;" + restoreStateV3Guard + `
LET $preimages = SELECT id FROM service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 1;
LET $future = SELECT id FROM service_state_v3_current
	WHERE repository = $repository AND visible_from > $snapshot_revision LIMIT 1;
IF array::len($preimages) != 0 OR array::len($future) != 0 {
	THROW 'phebs-permanent: selected service state v3 restore is not drained';
};
UPSERT $summary_rid CONTENT $summary_content RETURN NONE;
FOR $rid IN $summary_preimage_ids { DELETE $rid RETURN NONE; };
COMMIT;`
