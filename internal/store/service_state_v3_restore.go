package store

import (
	"context"
	"fmt"
	"sort"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

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
	rowResults, err := surrealdb.Query[[]stateKey](ctx, s.db, `
SELECT repository, service_key FROM service_state_v3_current
	ORDER BY repository, service_key LIMIT $limit`, map[string]any{
		"limit": maxRows + 1,
	})
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
	summaryResults, err := surrealdb.Query[[]summaryKey](ctx, s.db, `
SELECT repository FROM service_state_v3_repository
	ORDER BY repository LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3LifecycleRoots + 1,
	})
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
		results, queryErr := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $selected = SELECT id FROM service_runtime_selector
	WHERE repository = $repository AND backend = 'v3' LIMIT 1;
IF array::len($selected) != 0 {
	THROW 'phebs-permanent: unselected service state v3 restore fence changed';
};
DELETE service_state_v3_current WHERE repository = $repository RETURN NONE;
DELETE service_state_v3_repository WHERE repository = $repository RETURN NONE;
DELETE service_state_v3_preimage WHERE repository = $repository RETURN NONE;
DELETE service_state_v3_repository_preimage WHERE repository = $repository RETURN NONE;
COMMIT;`, map[string]any{"repository": repository})
		if queryErr != nil {
			return fmt.Errorf("discard unselected service state v3 for %q: %w", repository, queryErr)
		}
		for index, result := range *results {
			if result.Error != nil {
				return fmt.Errorf(
					"discard unselected service state v3 for %q statement %d: %s",
					repository,
					index,
					result.Error.Message,
				)
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
	if summary.Repository != selector.Repository ||
		summary.ControlRevision != selector.StateControlRevision ||
		summary.SummaryDigest != selector.StateSummaryDigest {
		return fmt.Errorf("restore selected service state v3 for %q: %w", selector.Repository, ErrInvalidServiceStateV3)
	}
	const maxRows = servicecatalogv3.MaxTotalServices * 2
	type recordID struct {
		RecID *models.RecordID `json:"id"`
	}
	summaryResults, err := surrealdb.Query[[]recordID](ctx, s.db, `
SELECT id FROM service_state_v3_repository_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 2`, map[string]any{
		"repository": selector.Repository, "snapshot_revision": selector.StateControlRevision,
		"snapshot_digest": selector.StateSummaryDigest,
	})
	if err != nil {
		return fmt.Errorf("restore selected service state v3 for %q: summary preimage: %w", selector.Repository, err)
	}
	summaryPreimages := firstDomainRows(summaryResults)
	if len(summaryPreimages) > 1 {
		return fmt.Errorf("restore selected service state v3 for %q: summary preimage: %w", selector.Repository, ErrInvalidServiceStateV3)
	}
	preimageResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": selector.Repository, "snapshot_revision": selector.StateControlRevision,
		"snapshot_digest": selector.StateSummaryDigest, "limit": maxRows + 1,
	})
	if err != nil {
		return fmt.Errorf("restore selected service state v3 for %q: rows: %w", selector.Repository, err)
	}
	preimages := firstDomainRows(preimageResults)
	if len(preimages) > maxRows {
		return fmt.Errorf("restore selected service state v3 for %q: rows: %w", selector.Repository, ErrInvalidServiceStateV3)
	}
	futureResults, err := surrealdb.Query[[]recordID](ctx, s.db, `
SELECT id FROM service_state_v3_current
	WHERE repository = $repository AND visible_from > $snapshot_revision
	LIMIT $limit`, map[string]any{
		"repository": selector.Repository, "snapshot_revision": selector.StateControlRevision,
		"limit": maxRows + 1,
	})
	if err != nil {
		return fmt.Errorf("restore selected service state v3 for %q: future rows: %w", selector.Repository, err)
	}
	future := firstDomainRows(futureResults)
	if len(future) > maxRows {
		return fmt.Errorf("restore selected service state v3 for %q: future rows: %w", selector.Repository, ErrInvalidServiceStateV3)
	}
	if len(summaryPreimages) == 0 && len(preimages) == 0 && len(future) == 0 {
		return nil
	}
	updates := make([]map[string]any, 0, len(preimages))
	for _, preimage := range preimages {
		state, stateErr := decodeServiceStateV3Preimage(
			selector.Repository,
			preimage.ServiceKey,
			selector.StateControlRevision,
			selector.StateSummaryDigest,
			preimage,
		)
		if stateErr != nil {
			return fmt.Errorf("restore selected service state v3 for %q: row: %w", selector.Repository, stateErr)
		}
		content := serviceStateContent(state)
		content["visible_from"] = preimage.VisibleFrom
		updates = append(updates, map[string]any{
			"rid":     serviceStateV3ID(selector.Repository, preimage.ServiceKey),
			"content": content,
		})
	}
	summaryContent := serviceRepositoryStateContent(summary)
	selectorContent := serviceRuntimeSelectorContent(selector)
	mutationResults, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
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
LET $summaries = SELECT id FROM service_state_v3_repository_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 2;
LET $preimages = SELECT id FROM service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT $row_limit;
LET $future = SELECT id FROM service_state_v3_current
	WHERE repository = $repository AND visible_from > $snapshot_revision
	LIMIT $row_limit;
IF !$selector_ok OR array::len($summaries) != $summary_preimage_count OR
	array::len($preimages) != $preimage_count OR
	array::len($future) != $future_count {
	THROW 'phebs-permanent: selected service state v3 restore fence changed';
};
DELETE service_state_v3_current
	WHERE repository = $repository AND visible_from > $snapshot_revision RETURN NONE;
FOR $update IN $updates {
	UPSERT $update.rid CONTENT $update.content RETURN NONE;
};
UPSERT $summary_rid CONTENT $summary_content RETURN NONE;
DELETE service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest RETURN NONE;
DELETE service_state_v3_repository_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest RETURN NONE;
COMMIT;`, map[string]any{
		"selector_rid":           serviceRuntimeSelectorID(selector.Repository),
		"expected_selector":      selectorContent,
		"repository":             selector.Repository,
		"snapshot_revision":      selector.StateControlRevision,
		"snapshot_digest":        selector.StateSummaryDigest,
		"row_limit":              maxRows + 1,
		"summary_preimage_count": len(summaryPreimages),
		"preimage_count":         len(preimages),
		"future_count":           len(future),
		"updates":                updates,
		"summary_rid":            serviceStateV3RepositoryID(selector.Repository),
		"summary_content":        summaryContent,
	})
	if err != nil {
		return fmt.Errorf("restore selected service state v3 for %q: %w", selector.Repository, err)
	}
	for index, result := range *mutationResults {
		if result.Error != nil {
			return fmt.Errorf(
				"restore selected service state v3 for %q statement %d: %s",
				selector.Repository,
				index,
				result.Error.Message,
			)
		}
	}
	return nil
}
