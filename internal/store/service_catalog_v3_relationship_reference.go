package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const (
	// MaxServiceCatalogV3RelationshipReferences admits the bounded recovery
	// inventory: current, rollback, and interrupted publication roots for at
	// most MaxServiceCatalogV3LifecycleRoots repositories.
	MaxServiceCatalogV3RelationshipReferences = 16_384

	serviceCatalogV3RelationshipReferenceSchemaMigrationVersion = "t41.8-service-catalog-v3-relationship-reference-schema-v1"
)

// ServiceCatalogV3RelationshipReference is the store pin retained by one
// validated relationship-v3 generation. Relationship bytes and pointers stay
// in their filesystem namespace; this row only prevents collection of the
// exact catalog root that authored them.
type ServiceCatalogV3RelationshipReference struct {
	Repository                   string
	RelationshipGenerationDigest string
	RelationshipRootDigest       string
	CatalogRootDigest            string
	CatalogControlRevision       uint64
	StateControlRevision         uint64
	StateSummaryDigest           string
}

type ServiceCatalogV3RelationshipReferenceStore interface {
	PinServiceCatalogV3RelationshipReference(
		context.Context,
		ServiceCatalogV3RelationshipReference,
	) error
	RecoverServiceCatalogV3RelationshipReference(
		context.Context,
		ServiceCatalogV3RelationshipReference,
	) error
	ReconcileServiceCatalogV3RelationshipReferences(
		context.Context,
		[]ServiceCatalogV3RelationshipReference,
	) error
	UnpinServiceCatalogV3RelationshipReference(
		context.Context,
		ServiceCatalogV3RelationshipReference,
	) error
}

var _ ServiceCatalogV3RelationshipReferenceStore = (*Surreal)(nil)

func relationshipPublicationV3Reference(
	repository, relationshipGeneration, relationshipRoot, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) ServiceCatalogV3RelationshipReference {
	return ServiceCatalogV3RelationshipReference{
		Repository:                   repository,
		RelationshipGenerationDigest: relationshipGeneration,
		RelationshipRootDigest:       relationshipRoot,
		CatalogRootDigest:            catalogRoot,
		CatalogControlRevision:       catalogRevision,
		StateControlRevision:         stateRevision,
		StateSummaryDigest:           stateSummary,
	}
}

type serviceCatalogV3RelationshipReferenceRec struct {
	Repository                   string           `json:"repository"`
	RelationshipGenerationDigest string           `json:"relationship_generation_digest"`
	RelationshipRootDigest       string           `json:"relationship_root_digest"`
	CatalogRootDigest            string           `json:"catalog_root_digest"`
	CatalogControlRevision       uint64           `json:"catalog_control_revision"`
	StateControlRevision         uint64           `json:"state_control_revision"`
	StateSummaryDigest           string           `json:"state_summary_digest"`
	RecordedAt                   time.Time        `json:"recorded_at"`
	RecID                        *models.RecordID `json:"id"`
}

type serviceCatalogV3RelationshipUnpinResult struct {
	Found   int `json:"found"`
	Deleted int `json:"deleted"`
}

func serviceCatalogV3RelationshipReferenceSchemaMigrationID() models.RecordID {
	return models.NewRecordID(
		"store_migration", "service_catalog_v3_relationship_reference_schema",
	)
}

func serviceCatalogV3RelationshipReferenceID(
	relationshipGenerationDigest string,
) models.RecordID {
	return models.NewRecordID(
		"service_catalog_v3_relationship_reference",
		relationshipGenerationDigest[len("sha256:"):],
	)
}

func validateServiceCatalogV3RelationshipReference(
	reference ServiceCatalogV3RelationshipReference,
) error {
	if validateCandidateRepository(reference.Repository) != nil ||
		!validSHA256Digest(reference.RelationshipGenerationDigest) ||
		!validSHA256Digest(reference.RelationshipRootDigest) ||
		!validSHA256Digest(reference.CatalogRootDigest) ||
		reference.CatalogControlRevision == 0 ||
		reference.StateControlRevision == 0 ||
		!validSHA256Digest(reference.StateSummaryDigest) {
		return ErrInvalidServiceCatalogV3Lifecycle
	}
	return nil
}

func equalServiceCatalogV3RelationshipReference(
	stored serviceCatalogV3RelationshipReferenceRec,
	wanted ServiceCatalogV3RelationshipReference,
) bool {
	return validServiceCatalogV3RecordID(
		stored.RecID,
		"service_catalog_v3_relationship_reference",
		wanted.RelationshipGenerationDigest[len("sha256:"):],
	) && stored.Repository == wanted.Repository &&
		stored.RelationshipGenerationDigest == wanted.RelationshipGenerationDigest &&
		stored.RelationshipRootDigest == wanted.RelationshipRootDigest &&
		stored.CatalogRootDigest == wanted.CatalogRootDigest &&
		stored.CatalogControlRevision == wanted.CatalogControlRevision &&
		stored.StateControlRevision == wanted.StateControlRevision &&
		stored.StateSummaryDigest == wanted.StateSummaryDigest &&
		!stored.RecordedAt.IsZero()
}

const serviceCatalogV3RelationshipReferenceSchema = `
DEFINE TABLE OVERWRITE service_catalog_v3_relationship_reference SCHEMAFULL;
DEFINE FIELD OVERWRITE repository ON service_catalog_v3_relationship_reference TYPE string;
DEFINE FIELD OVERWRITE relationship_generation_digest ON service_catalog_v3_relationship_reference TYPE string;
DEFINE FIELD OVERWRITE relationship_root_digest ON service_catalog_v3_relationship_reference TYPE string;
DEFINE FIELD OVERWRITE catalog_root_digest ON service_catalog_v3_relationship_reference TYPE string;
DEFINE FIELD OVERWRITE catalog_control_revision ON service_catalog_v3_relationship_reference TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE state_control_revision ON service_catalog_v3_relationship_reference TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE state_summary_digest ON service_catalog_v3_relationship_reference TYPE string;
DEFINE FIELD OVERWRITE recorded_at ON service_catalog_v3_relationship_reference TYPE datetime;
DEFINE INDEX OVERWRITE service_catalog_v3_relationship_reference_generation ON service_catalog_v3_relationship_reference FIELDS relationship_generation_digest UNIQUE;
DEFINE INDEX OVERWRITE service_catalog_v3_relationship_reference_catalog_root ON service_catalog_v3_relationship_reference FIELDS catalog_root_digest;
DEFINE INDEX OVERWRITE service_catalog_v3_relationship_reference_repository ON service_catalog_v3_relationship_reference FIELDS repository;
`

func (s *Surreal) migrateServiceCatalogV3RelationshipReferenceSchema(
	ctx context.Context,
) error {
	marker := serviceCatalogV3RelationshipReferenceSchemaMigrationID()
	markerResults, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{"rid": marker})
	if err != nil {
		return fmt.Errorf(
			"migrate service catalog v3 relationship reference schema: marker: %w", err,
		)
	}
	markerRows := firstDomainRows(markerResults)
	if len(markerRows) == 1 {
		if markerRows[0].Version ==
			serviceCatalogV3RelationshipReferenceSchemaMigrationVersion {
			return nil
		}
		return fmt.Errorf(
			"migrate service catalog v3 relationship reference schema: unsupported marker %q",
			markerRows[0].Version,
		)
	}
	if len(markerRows) > 1 {
		return errors.New(
			"migrate service catalog v3 relationship reference schema: duplicate marker",
		)
	}
	preflight, err := surrealdb.Query[any](ctx, s.db, `
DEFINE TABLE IF NOT EXISTS service_catalog_v3_relationship_reference SCHEMALESS;`, nil)
	if err != nil {
		return fmt.Errorf(
			"migrate service catalog v3 relationship reference schema: preflight schema: %w",
			err,
		)
	}
	for index, result := range *preflight {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 relationship reference preflight statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	probe, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db, `RETURN [{ count: array::len(
		SELECT id FROM service_catalog_v3_relationship_reference LIMIT 1
	) }];`, nil)
	if err != nil {
		return fmt.Errorf(
			"migrate service catalog v3 relationship reference schema: preflight: %w", err,
		)
	}
	probeRows := firstDomainRows(probe)
	if len(probeRows) != 1 || probeRows[0].Count != 0 {
		return errors.New(
			"migrate service catalog v3 relationship reference schema: unowned pre-migration rows",
		)
	}
	results, err := surrealdb.Query[any](
		ctx, s.db, serviceCatalogV3RelationshipReferenceSchema, nil,
	)
	if err != nil {
		return fmt.Errorf(
			"migrate service catalog v3 relationship reference schema: define: %w", err,
		)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 relationship reference schema statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	written, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $rid LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted {
	THROW 'phebs-permanent: unsupported service catalog v3 relationship reference schema migration'
};
UPSERT $rid SET
	version = IF $current = NONE THEN $wanted ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"rid":    marker,
		"wanted": serviceCatalogV3RelationshipReferenceSchemaMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf(
			"migrate service catalog v3 relationship reference schema: marker write: %w",
			err,
		)
	}
	for index, result := range *written {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 relationship reference schema marker statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	verified, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{"rid": marker})
	if err != nil {
		return fmt.Errorf(
			"migrate service catalog v3 relationship reference schema: verify: %w", err,
		)
	}
	rows := firstDomainRows(verified)
	if len(rows) != 1 || rows[0].Version !=
		serviceCatalogV3RelationshipReferenceSchemaMigrationVersion {
		return errors.New(
			"migrate service catalog v3 relationship reference schema: completion marker missing",
		)
	}
	return nil
}

// PinServiceCatalogV3RelationshipReference atomically checks the exact current
// v3 catalog and state-summary fence before installing an immutable catalog
// lifecycle reference. The filesystem pointer swap occurs only after this
// call succeeds.
func (s *Surreal) PinServiceCatalogV3RelationshipReference(
	ctx context.Context,
	reference ServiceCatalogV3RelationshipReference,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateServiceCatalogV3RelationshipReference(reference); err != nil {
		return fmt.Errorf("pin service catalog v3 relationship reference: %w", err)
	}
	rows, err := s.writeServiceCatalogV3RelationshipReference(ctx, reference, true)
	if err != nil {
		return fmt.Errorf("pin service catalog v3 relationship reference: %w", err)
	}
	if len(rows) != 1 || !equalServiceCatalogV3RelationshipReference(
		rows[0], reference,
	) {
		return fmt.Errorf(
			"pin service catalog v3 relationship reference: authority changed: %w",
			ErrConflict,
		)
	}
	return nil
}

// PinRelationshipPublicationV3 is the cycle-free publication-store adapter.
// The relationship package owns the interface while the store owns the row
// representation and exact authority transaction.
func (s *Surreal) PinRelationshipPublicationV3(
	ctx context.Context,
	repository, relationshipGeneration, relationshipRoot, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) error {
	return s.PinServiceCatalogV3RelationshipReference(
		ctx,
		relationshipPublicationV3Reference(
			repository, relationshipGeneration, relationshipRoot, catalogRoot,
			catalogRevision, stateRevision, stateSummary,
		),
	)
}

// RecoverRelationshipPublicationV3 adapts marker recovery to the add-only
// historical reference path. Ordinary publication continues to use the
// current catalog/state fence in PinRelationshipPublicationV3.
func (s *Surreal) RecoverRelationshipPublicationV3(
	ctx context.Context,
	repository, relationshipGeneration, relationshipRoot, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) error {
	return s.RecoverServiceCatalogV3RelationshipReference(
		ctx,
		relationshipPublicationV3Reference(
			repository, relationshipGeneration, relationshipRoot, catalogRoot,
			catalogRevision, stateRevision, stateSummary,
		),
	)
}

func (s *Surreal) writeServiceCatalogV3RelationshipReference(
	ctx context.Context,
	reference ServiceCatalogV3RelationshipReference,
	requireCurrent bool,
) ([]serviceCatalogV3RelationshipReferenceRec, error) {
	results, err := surrealdb.Query[[]serviceCatalogV3RelationshipReferenceRec](
		ctx, s.db, `
BEGIN;
LET $existing = (SELECT * FROM $reference_rid LIMIT 1)[0];
LET $catalog_root = (SELECT root_digest, repository FROM $catalog_root_rid LIMIT 1)[0];
LET $lifecycle = (SELECT root_digest, repository, state, member_cursor, tombstoned_at
	FROM $lifecycle_rid LIMIT 1)[0];
LET $candidate = (SELECT repository, root_digest, control_revision
	FROM $candidate_rid LIMIT 1)[0];
LET $summary = (SELECT repository, catalog_generation, catalog_control_revision,
	control_revision, summary_digest FROM $summary_rid LIMIT 1)[0];
LET $same = $existing != NONE
	AND $existing.repository = $repository
	AND $existing.relationship_generation_digest = $relationship_generation_digest
	AND $existing.relationship_root_digest = $relationship_root_digest
	AND $existing.catalog_root_digest = $catalog_root_digest
	AND $existing.catalog_control_revision = $catalog_control_revision
	AND $existing.state_control_revision = $state_control_revision
	AND $existing.state_summary_digest = $state_summary_digest;
LET $catalog_ready = $catalog_root != NONE
	AND $catalog_root.root_digest = $catalog_root_digest
	AND $catalog_root.repository = $repository
	AND $lifecycle != NONE AND $lifecycle.root_digest = $catalog_root_digest
	AND $lifecycle.repository = $repository AND $lifecycle.state = 'historical'
	AND $lifecycle.member_cursor = 0 AND $lifecycle.tombstoned_at = NONE;
LET $current_ready = $candidate != NONE AND $candidate.repository = $repository
	AND $candidate.root_digest = $catalog_root_digest
	AND $candidate.control_revision = $catalog_control_revision
	AND $summary != NONE AND $summary.repository = $repository
	AND $summary.catalog_generation = $catalog_root_digest
	AND $summary.catalog_control_revision = $catalog_control_revision
	AND $summary.control_revision = $state_control_revision
	AND $summary.summary_digest = $state_summary_digest;
LET $ready = $catalog_ready AND (!$require_current OR $current_ready);
LET $stored = IF $same AND $ready THEN [$existing]
	ELSE IF $existing = NONE AND $ready THEN
		(CREATE $reference_rid CONTENT {
			repository: $repository,
			relationship_generation_digest: $relationship_generation_digest,
			relationship_root_digest: $relationship_root_digest,
			catalog_root_digest: $catalog_root_digest,
			catalog_control_revision: $catalog_control_revision,
			state_control_revision: $state_control_revision,
			state_summary_digest: $state_summary_digest,
			recorded_at: time::now()
		} RETURN AFTER)
	ELSE [] END;
RETURN $stored;
COMMIT;`, map[string]any{
			"reference_rid": serviceCatalogV3RelationshipReferenceID(
				reference.RelationshipGenerationDigest,
			),
			"catalog_root_rid":               serviceCatalogV3RootID(reference.CatalogRootDigest),
			"lifecycle_rid":                  serviceCatalogV3LifecycleID(reference.CatalogRootDigest),
			"candidate_rid":                  serviceCatalogV3CandidateID(reference.Repository),
			"summary_rid":                    serviceStateV3RepositoryID(reference.Repository),
			"repository":                     reference.Repository,
			"relationship_generation_digest": reference.RelationshipGenerationDigest,
			"relationship_root_digest":       reference.RelationshipRootDigest,
			"catalog_root_digest":            reference.CatalogRootDigest,
			"catalog_control_revision":       reference.CatalogControlRevision,
			"state_control_revision":         reference.StateControlRevision,
			"state_summary_digest":           reference.StateSummaryDigest,
			"require_current":                requireCurrent,
		},
	)
	if err != nil {
		return nil, err
	}
	return firstDomainRows(results), nil
}

// RecoverServiceCatalogV3RelationshipReference idempotently reconstructs one
// exact reference from a fully validated retained filesystem root. Unlike the
// complete-set reconciler, it is add-only: a corrupt sibling namespace cannot
// cause this recovery path to delete any existing reference. Historical is
// required, but the catalog/state identities need not still be current.
func (s *Surreal) RecoverServiceCatalogV3RelationshipReference(
	ctx context.Context,
	reference ServiceCatalogV3RelationshipReference,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateServiceCatalogV3RelationshipReference(reference); err != nil {
		return fmt.Errorf("recover service catalog v3 relationship reference: %w", err)
	}
	rows, err := s.writeServiceCatalogV3RelationshipReference(ctx, reference, false)
	if err != nil {
		return fmt.Errorf("recover service catalog v3 relationship reference: %w", err)
	}
	if len(rows) != 1 || !equalServiceCatalogV3RelationshipReference(
		rows[0], reference,
	) {
		return fmt.Errorf(
			"recover service catalog v3 relationship reference: authority changed: %w",
			ErrConflict,
		)
	}
	return nil
}

// ReconcileServiceCatalogV3RelationshipReferences replaces the store's
// relationship-v3 pin set with the exact roots proven by a complete filesystem
// recovery audit. It may reconstruct a missing row for a retained historical
// root, but never for a collecting or absent catalog generation.
func (s *Surreal) ReconcileServiceCatalogV3RelationshipReferences(
	ctx context.Context,
	references []ServiceCatalogV3RelationshipReference,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(references) > MaxServiceCatalogV3RelationshipReferences {
		return errors.New(
			"reconcile service catalog v3 relationship references: reference set exceeds bound",
		)
	}
	ordered := append([]ServiceCatalogV3RelationshipReference(nil), references...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].RelationshipGenerationDigest <
			ordered[j].RelationshipGenerationDigest
	})
	for index, reference := range ordered {
		if err := validateServiceCatalogV3RelationshipReference(reference); err != nil {
			return fmt.Errorf(
				"reconcile service catalog v3 relationship references: reference %d: %w",
				index, err,
			)
		}
		if index > 0 && ordered[index-1].RelationshipGenerationDigest ==
			reference.RelationshipGenerationDigest {
			return errors.New(
				"reconcile service catalog v3 relationship references: duplicate generation",
			)
		}
	}
	for _, reference := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := s.writeServiceCatalogV3RelationshipReference(
			ctx, reference, false,
		)
		if err != nil {
			return fmt.Errorf(
				"reconcile service catalog v3 relationship references: write: %w", err,
			)
		}
		if len(rows) != 1 || !equalServiceCatalogV3RelationshipReference(
			rows[0], reference,
		) {
			return fmt.Errorf(
				"reconcile service catalog v3 relationship references: authority changed: %w",
				ErrConflict,
			)
		}
	}
	inventory, err := surrealdb.Query[[]struct {
		Generation string `json:"relationship_generation_digest"`
	}](ctx, s.db, `
SELECT relationship_generation_digest
	FROM service_catalog_v3_relationship_reference
	ORDER BY relationship_generation_digest LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3RelationshipReferences + 1,
	})
	if err != nil {
		return fmt.Errorf(
			"reconcile service catalog v3 relationship references: inventory: %w", err,
		)
	}
	if len(firstDomainRows(inventory)) > MaxServiceCatalogV3RelationshipReferences {
		return errors.New(
			"reconcile service catalog v3 relationship references: stored inventory exceeds bound",
		)
	}
	generations := make([]string, len(ordered))
	for index, reference := range ordered {
		generations[index] = reference.RelationshipGenerationDigest
	}
	if _, err := surrealdb.Query[[]serviceCatalogV3RelationshipReferenceRec](
		ctx, s.db, `DELETE service_catalog_v3_relationship_reference
			WHERE relationship_generation_digest NOT IN $generations RETURN BEFORE`,
		map[string]any{"generations": generations},
	); err != nil {
		return fmt.Errorf(
			"reconcile service catalog v3 relationship references: remove stale: %w", err,
		)
	}
	return nil
}

// UnpinServiceCatalogV3RelationshipReference removes exactly one collected
// relationship generation. Repeating the same removal is harmless; an
// immutable-identity mismatch is refused.
func (s *Surreal) UnpinServiceCatalogV3RelationshipReference(
	ctx context.Context,
	reference ServiceCatalogV3RelationshipReference,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateServiceCatalogV3RelationshipReference(reference); err != nil {
		return fmt.Errorf("unpin service catalog v3 relationship reference: %w", err)
	}
	results, err := surrealdb.Query[[]serviceCatalogV3RelationshipUnpinResult](
		ctx, s.db, `
BEGIN;
LET $existing = (SELECT * FROM $rid LIMIT 1)[0];
LET $same = $existing != NONE
	AND $existing.repository = $repository
	AND $existing.relationship_generation_digest = $relationship_generation_digest
	AND $existing.relationship_root_digest = $relationship_root_digest
	AND $existing.catalog_root_digest = $catalog_root_digest
	AND $existing.catalog_control_revision = $catalog_control_revision
	AND $existing.state_control_revision = $state_control_revision
	AND $existing.state_summary_digest = $state_summary_digest;
LET $deleted = IF $same THEN (DELETE $rid RETURN BEFORE) ELSE [] END;
RETURN [{
	found: IF $existing = NONE THEN 0 ELSE 1 END,
	deleted: array::len($deleted)
}];
COMMIT;`, map[string]any{
			"rid": serviceCatalogV3RelationshipReferenceID(
				reference.RelationshipGenerationDigest,
			),
			"repository":                     reference.Repository,
			"relationship_generation_digest": reference.RelationshipGenerationDigest,
			"relationship_root_digest":       reference.RelationshipRootDigest,
			"catalog_root_digest":            reference.CatalogRootDigest,
			"catalog_control_revision":       reference.CatalogControlRevision,
			"state_control_revision":         reference.StateControlRevision,
			"state_summary_digest":           reference.StateSummaryDigest,
		},
	)
	if err != nil {
		return fmt.Errorf("unpin service catalog v3 relationship reference: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Found < 0 || rows[0].Found > 1 ||
		rows[0].Deleted < 0 || rows[0].Deleted > 1 ||
		rows[0].Found == 1 && rows[0].Deleted != 1 {
		return fmt.Errorf(
			"unpin service catalog v3 relationship reference: immutable identity changed: %w",
			ErrConflict,
		)
	}
	return nil
}

// UnpinRelationshipPublicationV3 adapts the publication-owned interface to an
// exact, idempotent store unpin without importing the publication package.
func (s *Surreal) UnpinRelationshipPublicationV3(
	ctx context.Context,
	repository, relationshipGeneration, relationshipRoot, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) error {
	return s.UnpinServiceCatalogV3RelationshipReference(
		ctx,
		relationshipPublicationV3Reference(
			repository, relationshipGeneration, relationshipRoot, catalogRoot,
			catalogRevision, stateRevision, stateSummary,
		),
	)
}
