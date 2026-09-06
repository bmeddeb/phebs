package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const (
	MaxServiceCatalogV3UpgradeRoots   = 64
	MaxServiceCatalogV3LifecycleRoots = 4_096
	MaxServiceCatalogV3OrphanScan     = 64
	MaxServiceCatalogV3OrphanDelete   = 16
	ServiceCatalogV3Retained          = 3

	serviceCatalogV3Historical = "historical"
	serviceCatalogV3Collecting = "collecting"
)

var ErrInvalidServiceCatalogV3Lifecycle = errors.New(
	"invalid service catalog v3 lifecycle state",
)

type ServiceCatalogV3StartupReport struct {
	RootsScanned   int
	Repaired       int
	OrphansScanned int
	OrphansDeleted int
	More           bool
}

type ServiceCatalogV3PreciousReport struct {
	HistoricalRoots        int
	CollectingRoots        int
	Members                int
	StateRows              int
	StateSummaries         int
	StatePlans             int
	RelationshipReferences int
	LogicalBytes           int64
	RootBytes              int64
	MemberBytes            int64
}

type ServiceCatalogV3LifecycleSweep struct {
	Cursor              string
	Scanned             int
	Deleted             int
	RetiredLogicalBytes int64
	DeletedRootBytes    int64
	DeletedMemberBytes  int64
	More                bool
}

type serviceCatalogV3LifecycleRec struct {
	RootDigest       string           `json:"root_digest"`
	Repository       string           `json:"repository"`
	AuthorityVersion string           `json:"authority_version_id"`
	State            string           `json:"state"`
	MemberCursor     int              `json:"member_cursor"`
	MemberCount      int              `json:"member_count"`
	LogicalBytes     int              `json:"logical_bytes"`
	RootBytes        int              `json:"root_bytes"`
	MemberBytes      int              `json:"member_bytes"`
	RecordedAt       time.Time        `json:"recorded_at"`
	TombstonedAt     *time.Time       `json:"tombstoned_at"`
	RecID            *models.RecordID `json:"id"`
}

type serviceCatalogV3RootMemberRec struct {
	RootDigest   string           `json:"root_digest"`
	MemberDigest string           `json:"member_digest"`
	Ordinal      int              `json:"ordinal"`
	ContentBytes int              `json:"content_bytes"`
	RecID        *models.RecordID `json:"id"`
}

type serviceCatalogV3StateReferenceRec struct {
	Repository      string           `json:"repository"`
	RootDigest      string           `json:"root_digest"`
	Kind            string           `json:"kind"`
	ServiceKey      string           `json:"service_key"`
	StateRootDigest string           `json:"state_root_digest"`
	RecordedAt      time.Time        `json:"recorded_at"`
	RecID           *models.RecordID `json:"id"`
}

func serviceCatalogV3LifecycleID(digest string) models.RecordID {
	return models.NewRecordID("service_catalog_v3_lifecycle", digest[len("sha256:"):])
}

func serviceCatalogV3RootMemberID(rootDigest, memberDigest string) models.RecordID {
	return models.NewRecordID(
		"service_catalog_v3_root_member",
		fmt.Sprintf("%s:%s", rootDigest[len("sha256:"):], memberDigest[len("sha256:"):]),
	)
}

func serviceCatalogV3LifecycleWanted(
	root servicecatalogv3.Root,
	recordedAt time.Time,
) serviceCatalogV3LifecycleRec {
	versionID, _ := serviceCatalogV3AuthorityVersionID(root).ID.(string)
	return serviceCatalogV3LifecycleRec{
		RootDigest: root.Digest, Repository: root.Binding.Repository,
		AuthorityVersion: versionID, State: serviceCatalogV3Historical,
		MemberCount:  len(root.ServiceMembers) + len(root.PlacementMembers),
		LogicalBytes: root.LogicalBytes, RootBytes: root.RootBytes,
		MemberBytes: root.EncodedMemberBytes, RecordedAt: recordedAt,
	}
}

func serviceCatalogV3RootMembers(
	root servicecatalogv3.Root,
) []serviceCatalogV3RootMemberRec {
	descriptors := append(
		append([]servicecatalogv3.MemberDescriptor{}, root.ServiceMembers...),
		root.PlacementMembers...,
	)
	result := make([]serviceCatalogV3RootMemberRec, len(descriptors))
	for index, descriptor := range descriptors {
		result[index] = serviceCatalogV3RootMemberRec{
			RootDigest: root.Digest, MemberDigest: descriptor.Digest,
			Ordinal: index, ContentBytes: descriptor.ContentBytes,
		}
	}
	return result
}

func equalServiceCatalogV3Lifecycle(
	stored, wanted serviceCatalogV3LifecycleRec,
) bool {
	return validServiceCatalogV3RecordID(
		stored.RecID, "service_catalog_v3_lifecycle",
		wanted.RootDigest[len("sha256:"):],
	) && stored.RootDigest == wanted.RootDigest &&
		stored.Repository == wanted.Repository &&
		stored.AuthorityVersion == wanted.AuthorityVersion &&
		stored.State == wanted.State && stored.MemberCursor == 0 &&
		stored.MemberCount == wanted.MemberCount &&
		stored.LogicalBytes == wanted.LogicalBytes &&
		stored.RootBytes == wanted.RootBytes &&
		stored.MemberBytes == wanted.MemberBytes &&
		stored.RecordedAt.Equal(wanted.RecordedAt) && stored.TombstonedAt == nil
}

func equalServiceCatalogV3RootMember(
	stored, wanted serviceCatalogV3RootMemberRec,
) bool {
	identifier, _ := serviceCatalogV3RootMemberID(
		wanted.RootDigest, wanted.MemberDigest,
	).ID.(string)
	return validServiceCatalogV3RecordID(
		stored.RecID, "service_catalog_v3_root_member", identifier,
	) && stored.RootDigest == wanted.RootDigest &&
		stored.MemberDigest == wanted.MemberDigest &&
		stored.Ordinal == wanted.Ordinal && stored.ContentBytes == wanted.ContentBytes
}

func ensureServiceCatalogV3LifecycleMetadata(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	root servicecatalogv3.Root,
	recordedAt time.Time,
	readmitCollecting bool,
) (bool, error) {
	wanted := serviceCatalogV3LifecycleWanted(root, recordedAt)
	results, err := storeQuery[[]serviceCatalogV3LifecycleRec](
		ctx, owner, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3LifecycleID(root.Digest)}, storeRead(),
	)
	if err != nil {
		return false, err
	}
	rows := firstDomainRows(results)
	created := false
	if len(rows) > 1 {
		return false, ErrConflict
	}
	if len(rows) == 1 && !equalServiceCatalogV3Lifecycle(rows[0], wanted) {
		row := rows[0]
		if !readmitCollecting || row.State != serviceCatalogV3Collecting ||
			!validServiceCatalogV3LifecycleRecord(
				row, root, serviceCatalogV3RootRec{RecordedAt: recordedAt},
			) {
			return false, ErrConflict
		}
		updated, updateErr := storeQuery[[]serviceCatalogV3LifecycleRec](
			ctx, owner, tx, `UPDATE $rid SET state = $historical, member_cursor = 0,
				tombstoned_at = NONE
				WHERE root_digest = $root_digest AND repository = $repository
					AND authority_version_id = $authority_version_id
					AND state = $collecting AND member_cursor = $member_cursor
					AND member_count = $member_count AND logical_bytes = $logical_bytes
					AND root_bytes = $root_bytes AND member_bytes = $member_bytes
				RETURN AFTER`, map[string]any{
				"rid":         serviceCatalogV3LifecycleID(root.Digest),
				"historical":  serviceCatalogV3Historical,
				"collecting":  serviceCatalogV3Collecting,
				"root_digest": wanted.RootDigest, "repository": wanted.Repository,
				"authority_version_id": wanted.AuthorityVersion,
				"member_cursor":        row.MemberCursor, "member_count": wanted.MemberCount,
				"logical_bytes": wanted.LogicalBytes, "root_bytes": wanted.RootBytes,
				"member_bytes": wanted.MemberBytes,
			}, storeWrite(1),
		)
		if updateErr != nil {
			return false, updateErr
		}
		updatedRows := firstDomainRows(updated)
		if len(updatedRows) != 1 || !equalServiceCatalogV3Lifecycle(
			updatedRows[0], wanted,
		) {
			return false, ErrConflict
		}
		created = true
	}
	if len(rows) == 0 {
		createdRows, createErr := storeQuery[[]serviceCatalogV3LifecycleRec](
			ctx, owner, tx, `CREATE $rid CONTENT {
				root_digest: $root_digest,
				repository: $repository,
				authority_version_id: $authority_version_id,
				state: $state,
				member_cursor: 0,
				member_count: $member_count,
				logical_bytes: $logical_bytes,
				root_bytes: $root_bytes,
				member_bytes: $member_bytes,
				recorded_at: $recorded_at,
				tombstoned_at: NONE
			} RETURN AFTER`, map[string]any{
				"rid":         serviceCatalogV3LifecycleID(root.Digest),
				"root_digest": wanted.RootDigest, "repository": wanted.Repository,
				"authority_version_id": wanted.AuthorityVersion,
				"state":                wanted.State, "member_count": wanted.MemberCount,
				"logical_bytes": wanted.LogicalBytes, "root_bytes": wanted.RootBytes,
				"member_bytes": wanted.MemberBytes, "recorded_at": wanted.RecordedAt,
			}, storeWrite(1),
		)
		if createErr != nil {
			return false, createErr
		}
		createdResult := firstDomainRows(createdRows)
		if len(createdResult) != 1 || !equalServiceCatalogV3Lifecycle(
			createdResult[0], wanted,
		) {
			return false, ErrConflict
		}
		created = true
	}
	for _, edge := range serviceCatalogV3RootMembers(root) {
		rid := serviceCatalogV3RootMemberID(edge.RootDigest, edge.MemberDigest)
		edgeResults, edgeErr := storeQuery[[]serviceCatalogV3RootMemberRec](
			ctx, owner, tx, "SELECT * FROM $rid", map[string]any{"rid": rid}, storeRead(),
		)
		if edgeErr != nil {
			return false, edgeErr
		}
		edgeRows := firstDomainRows(edgeResults)
		if len(edgeRows) > 1 || len(edgeRows) == 1 &&
			!equalServiceCatalogV3RootMember(edgeRows[0], edge) {
			return false, ErrConflict
		}
		if len(edgeRows) == 0 {
			createdEdges, createErr := storeQuery[[]serviceCatalogV3RootMemberRec](
				ctx, owner, tx, `CREATE $rid CONTENT {
					root_digest: $root_digest,
					member_digest: $member_digest,
					ordinal: $ordinal,
					content_bytes: $content_bytes
				} RETURN AFTER`, map[string]any{
					"rid": rid, "root_digest": edge.RootDigest,
					"member_digest": edge.MemberDigest, "ordinal": edge.Ordinal,
					"content_bytes": edge.ContentBytes,
				}, storeWrite(1),
			)
			if createErr != nil {
				return false, createErr
			}
			createdRows := firstDomainRows(createdEdges)
			if len(createdRows) != 1 || !equalServiceCatalogV3RootMember(
				createdRows[0], edge,
			) {
				return false, ErrConflict
			}
		}
	}
	return created, nil
}

const serviceCatalogV3LifecycleSchemaMigrationVersion = "t41.4-service-catalog-v3-lifecycle-schema-v1"

func serviceCatalogV3LifecycleSchemaMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "service_catalog_v3_lifecycle_schema")
}

const serviceCatalogV3LifecyclePreflightSchema = `
DEFINE TABLE IF NOT EXISTS service_catalog_v3_lifecycle SCHEMALESS;
DEFINE TABLE IF NOT EXISTS service_catalog_v3_root_member SCHEMALESS;
DEFINE TABLE IF NOT EXISTS service_catalog_v3_state_reference SCHEMALESS;`

const serviceCatalogV3LifecycleSchema = `
DEFINE TABLE OVERWRITE service_catalog_v3_lifecycle SCHEMAFULL;
DEFINE FIELD OVERWRITE root_digest ON service_catalog_v3_lifecycle TYPE string;
DEFINE FIELD OVERWRITE repository ON service_catalog_v3_lifecycle TYPE string;
DEFINE FIELD OVERWRITE authority_version_id ON service_catalog_v3_lifecycle TYPE string;
DEFINE FIELD OVERWRITE state ON service_catalog_v3_lifecycle TYPE string ASSERT $value INSIDE ['historical', 'collecting'];
DEFINE FIELD OVERWRITE member_cursor ON service_catalog_v3_lifecycle TYPE int ASSERT $value >= 0 AND $value <= 64;
DEFINE FIELD OVERWRITE member_count ON service_catalog_v3_lifecycle TYPE int ASSERT $value >= 0 AND $value <= 64;
DEFINE FIELD OVERWRITE logical_bytes ON service_catalog_v3_lifecycle TYPE int ASSERT $value >= 1 AND $value <= 16777216;
DEFINE FIELD OVERWRITE root_bytes ON service_catalog_v3_lifecycle TYPE int ASSERT $value >= 1 AND $value <= 262144;
DEFINE FIELD OVERWRITE member_bytes ON service_catalog_v3_lifecycle TYPE int ASSERT $value >= 0 AND $value <= 33554432;
DEFINE FIELD OVERWRITE recorded_at ON service_catalog_v3_lifecycle TYPE datetime;
DEFINE FIELD OVERWRITE tombstoned_at ON service_catalog_v3_lifecycle TYPE option<datetime>;
DEFINE INDEX OVERWRITE service_catalog_v3_lifecycle_repository ON service_catalog_v3_lifecycle FIELDS repository;

DEFINE TABLE OVERWRITE service_catalog_v3_root_member SCHEMAFULL;
DEFINE FIELD OVERWRITE root_digest ON service_catalog_v3_root_member TYPE string;
DEFINE FIELD OVERWRITE member_digest ON service_catalog_v3_root_member TYPE string;
DEFINE FIELD OVERWRITE ordinal ON service_catalog_v3_root_member TYPE int ASSERT $value >= 0 AND $value < 64;
DEFINE FIELD OVERWRITE content_bytes ON service_catalog_v3_root_member TYPE int ASSERT $value >= 1 AND $value <= 2097152;
DEFINE INDEX OVERWRITE service_catalog_v3_root_member_root ON service_catalog_v3_root_member FIELDS root_digest, ordinal UNIQUE;
DEFINE INDEX OVERWRITE service_catalog_v3_root_member_member ON service_catalog_v3_root_member FIELDS member_digest;

DEFINE TABLE OVERWRITE service_catalog_v3_state_reference SCHEMAFULL;
DEFINE FIELD OVERWRITE repository ON service_catalog_v3_state_reference TYPE string;
DEFINE FIELD OVERWRITE root_digest ON service_catalog_v3_state_reference TYPE string;
DEFINE FIELD OVERWRITE kind ON service_catalog_v3_state_reference TYPE string ASSERT $value INSIDE ['current', 'desired', 'active'];
DEFINE FIELD OVERWRITE service_key ON service_catalog_v3_state_reference TYPE string;
DEFINE FIELD OVERWRITE state_root_digest ON service_catalog_v3_state_reference TYPE string;
DEFINE FIELD OVERWRITE recorded_at ON service_catalog_v3_state_reference TYPE datetime;
DEFINE INDEX OVERWRITE service_catalog_v3_state_reference_root ON service_catalog_v3_state_reference FIELDS root_digest;
`

func (s *Surreal) migrateServiceCatalogV3LifecycleSchema(ctx context.Context) error {
	marker := serviceCatalogV3LifecycleSchemaMigrationID()
	markerResults, err := storeQuery[[]struct {
		Version string `json:"version"`
	}](ctx, s.accounting, s.db, "SELECT version FROM $rid", map[string]any{"rid": marker}, storeRead())
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 lifecycle schema: marker: %w", err)
	}
	markerRows := firstDomainRows(markerResults)
	if len(markerRows) == 1 {
		if markerRows[0].Version == serviceCatalogV3LifecycleSchemaMigrationVersion {
			return nil
		}
		return fmt.Errorf(
			"migrate service catalog v3 lifecycle schema: unsupported marker %q",
			markerRows[0].Version,
		)
	}
	if len(markerRows) > 1 {
		return errors.New("migrate service catalog v3 lifecycle schema: duplicate marker")
	}
	if err := s.applySchemaBatch(ctx, serviceCatalogV3LifecyclePreflightSchema, "migrate service catalog v3 lifecycle preflight "); err != nil {
		return fmt.Errorf("migrate service catalog v3 lifecycle schema: preflight schema: %w", err)
	}
	probe, err := storeQuery[[]struct {
		Count int `json:"count"`
	}](ctx, s.accounting, s.db, `RETURN [{ count:
		array::len(SELECT id FROM service_catalog_v3_lifecycle LIMIT 1) +
		array::len(SELECT id FROM service_catalog_v3_root_member LIMIT 1) +
		array::len(SELECT id FROM service_catalog_v3_state_reference LIMIT 1)
	}];`, nil, storeRead())
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 lifecycle schema: preflight: %w", err)
	}
	probeRows := firstDomainRows(probe)
	if len(probeRows) != 1 || probeRows[0].Count != 0 {
		return errors.New("migrate service catalog v3 lifecycle schema: unowned pre-migration rows")
	}
	if err := s.applySchemaBatch(ctx, serviceCatalogV3LifecycleSchema, "migrate service catalog v3 lifecycle schema "); err != nil {
		return fmt.Errorf("migrate service catalog v3 lifecycle schema: define: %w", err)
	}
	written, err := storeQuery[any](ctx, s.accounting, s.db, `
BEGIN;
LET $current = (SELECT version FROM $rid LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted {
	THROW 'phebs-permanent: unsupported service catalog v3 lifecycle schema migration'
};
UPSERT $rid SET
	version = IF $current = NONE THEN $wanted ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"rid": marker, "wanted": serviceCatalogV3LifecycleSchemaMigrationVersion,
	}, storeWrite(1))
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 lifecycle schema: marker write: %w", err)
	}
	for index, result := range *written {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 lifecycle schema marker statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	verified, err := storeQuery[[]struct {
		Version string `json:"version"`
	}](ctx, s.accounting, s.db, "SELECT version FROM $rid", map[string]any{"rid": marker}, storeRead())
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 lifecycle schema: verify: %w", err)
	}
	rows := firstDomainRows(verified)
	if len(rows) != 1 || rows[0].Version != serviceCatalogV3LifecycleSchemaMigrationVersion {
		return errors.New("migrate service catalog v3 lifecycle schema: completion marker missing")
	}
	return nil
}

func (s *Surreal) openServiceCatalogV3Generation(
	ctx context.Context,
	digest string,
) (servicecatalogv3.Generation, serviceCatalogV3RootRec, error) {
	if !validSHA256Digest(digest) {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
			ErrInvalidServiceCatalogV3Lifecycle
	}
	rootResults, err := storeQuery[[]serviceCatalogV3RootRec](
		ctx, s.accounting, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3RootID(digest)}, storeRead(),
	)
	if err != nil {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{}, err
	}
	rootRows := firstDomainRows(rootResults)
	if len(rootRows) != 1 {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
			ErrInvalidServiceCatalogV3Lifecycle
	}
	rootRecord := rootRows[0]
	root, err := servicecatalogv3.DecodeRoot([]byte(rootRecord.RootJSON))
	if err != nil || root.Digest != digest ||
		!equalServiceCatalogV3Root(rootRecord, serviceCatalogV3RootRec{
			RootDigest: root.Digest, Repository: root.Binding.Repository,
			RootBytes: len(rootRecord.RootJSON), RootJSON: rootRecord.RootJSON,
		}, digest[len("sha256:"):]) {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
			ErrInvalidServiceCatalogV3Lifecycle
	}
	overrideID, overrideVersion := serviceCatalogV3Override(root)
	versionID := serviceCatalogV3AuthorityVersionID(root)
	versionResults, err := storeQuery[[]serviceCatalogV3AuthorityVersionRec](
		ctx, s.accounting, s.db, "SELECT * FROM $rid", map[string]any{"rid": versionID}, storeRead(),
	)
	if err != nil {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{}, err
	}
	versionRows := firstDomainRows(versionResults)
	versionIdentifier, _ := versionID.ID.(string)
	if len(versionRows) != 1 || !equalServiceCatalogV3AuthorityVersion(
		versionRows[0], serviceCatalogV3AuthorityVersionRec{
			Repository:       root.Binding.Repository,
			AuthorityKind:    root.Binding.Authority.Kind,
			AuthorityID:      root.Binding.Authority.ID,
			AuthorityVersion: root.Binding.Authority.Version,
			OverrideID:       overrideID, OverrideVersion: overrideVersion,
			LogicalDigest: root.LogicalDigest,
		}, versionIdentifier,
	) {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
			ErrInvalidServiceCatalogV3Lifecycle
	}
	descriptors := append(
		append([]servicecatalogv3.MemberDescriptor{}, root.ServiceMembers...),
		root.PlacementMembers...,
	)
	digests := make([]string, len(descriptors))
	for index, descriptor := range descriptors {
		digests[index] = descriptor.Digest
	}
	memberResults, err := storeQuery[[]serviceCatalogV3MemberRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_member
	WHERE member_digest IN $digests LIMIT $limit`, map[string]any{
		"digests": digests, "limit": servicecatalogv3.MaxMembers + 1,
	}, storeRead())
	if err != nil {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{}, err
	}
	memberRows := firstDomainRows(memberResults)
	if len(memberRows) != len(descriptors) {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
			ErrInvalidServiceCatalogV3Lifecycle
	}
	byDigest := make(map[string]serviceCatalogV3MemberRec, len(memberRows))
	for _, row := range memberRows {
		if _, duplicate := byDigest[row.MemberDigest]; duplicate {
			return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
				ErrInvalidServiceCatalogV3Lifecycle
		}
		byDigest[row.MemberDigest] = row
	}
	members := make([]servicecatalogv3.EncodedMember, 0, len(descriptors))
	for _, descriptor := range descriptors {
		row, ok := byDigest[descriptor.Digest]
		if !ok || row.ContentBytes != len(row.Content) ||
			!equalServiceCatalogV3Member(row, serviceCatalogV3MemberRec{
				MemberDigest: descriptor.Digest, Kind: descriptor.Kind,
				Ordinal: descriptor.Ordinal, ContentBytes: descriptor.ContentBytes,
				Content: row.Content,
			}, descriptor.Digest[len("sha256:"):]) {
			return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
				ErrInvalidServiceCatalogV3Lifecycle
		}
		members = append(members, servicecatalogv3.EncodedMember{
			Kind: descriptor.Kind, Ordinal: descriptor.Ordinal,
			Content: []byte(row.Content),
		})
	}
	generation := servicecatalogv3.Generation{Root: root, Members: members}
	if err := servicecatalogv3.ValidateGeneration(generation); err != nil {
		return servicecatalogv3.Generation{}, serviceCatalogV3RootRec{},
			ErrInvalidServiceCatalogV3Lifecycle
	}
	return generation, rootRecord, nil
}

const serviceCatalogV3LifecycleRepairVersion = "t41.4-service-catalog-v3-lifecycle-repair-v1"

func serviceCatalogV3LifecycleRepairID() models.RecordID {
	return models.NewRecordID("store_migration", "service_catalog_v3_lifecycle_repair")
}

func (s *Surreal) RepairServiceCatalogV3Startup(
	ctx context.Context,
) (ServiceCatalogV3StartupReport, error) {
	if err := ctx.Err(); err != nil {
		return ServiceCatalogV3StartupReport{}, err
	}
	report := ServiceCatalogV3StartupReport{}
	markerResults, err := storeQuery[[]struct {
		Version string `json:"version"`
	}](ctx, s.accounting, s.db, "SELECT version FROM $rid", map[string]any{
		"rid": serviceCatalogV3LifecycleRepairID(),
	}, storeRead())
	if err != nil {
		return report, fmt.Errorf("repair service catalog v3 startup: marker: %w", err)
	}
	markerRows := firstDomainRows(markerResults)
	if len(markerRows) > 1 {
		return report, errors.New("repair service catalog v3 startup: duplicate marker")
	}
	if len(markerRows) == 1 && markerRows[0].Version != serviceCatalogV3LifecycleRepairVersion {
		return report, fmt.Errorf(
			"repair service catalog v3 startup: unsupported marker %q",
			markerRows[0].Version,
		)
	}
	if len(markerRows) == 0 {
		type rootSummary struct {
			RootDigest string `json:"root_digest"`
		}
		rootResults, queryErr := storeQuery[[]rootSummary](ctx, s.accounting, s.db, `
SELECT root_digest FROM service_catalog_v3_root
	ORDER BY root_digest LIMIT $limit`, map[string]any{
			"limit": MaxServiceCatalogV3UpgradeRoots + 1,
		}, storeRead())
		if queryErr != nil {
			return report, fmt.Errorf("repair service catalog v3 startup: roots: %w", queryErr)
		}
		roots := firstDomainRows(rootResults)
		if len(roots) > MaxServiceCatalogV3UpgradeRoots {
			return report, fmt.Errorf(
				"repair service catalog v3 startup: more than %d pre-lifecycle roots: %w",
				MaxServiceCatalogV3UpgradeRoots, ErrInvalidServiceCatalogV3Lifecycle,
			)
		}
		for _, summary := range roots {
			report.RootsScanned++
			generation, rootRecord, openErr := s.openServiceCatalogV3Generation(
				ctx, summary.RootDigest,
			)
			if openErr != nil {
				return report, fmt.Errorf(
					"repair service catalog v3 startup: strict-open %q: %w",
					summary.RootDigest, openErr,
				)
			}
			tx, beginErr := storeBegin(ctx, s.accounting, s.db)
			if beginErr != nil {
				return report, beginErr
			}
			created, repairErr := ensureServiceCatalogV3LifecycleMetadata(
				ctx, s.accounting, tx, generation.Root, rootRecord.RecordedAt, false,
			)
			if repairErr == nil {
				repairErr = storeCommit(ctx, s.accounting, tx)
			} else {
				_ = storeCancel(context.WithoutCancel(ctx), s.accounting, tx)
			}
			if repairErr != nil {
				return report, fmt.Errorf(
					"repair service catalog v3 startup: metadata %q: %w",
					summary.RootDigest, repairErr,
				)
			}
			if created {
				report.Repaired++
			}
		}
		candidateResults, queryErr := storeQuery[[]serviceCatalogV3CandidateRec](
			ctx, s.accounting, s.db, `SELECT * FROM service_catalog_v3_candidate
				ORDER BY repository LIMIT $limit`, map[string]any{
				"limit": MaxServiceCatalogV3UpgradeRoots + 1,
			}, storeRead(),
		)
		if queryErr != nil {
			return report, fmt.Errorf("repair service catalog v3 startup: candidates: %w", queryErr)
		}
		candidates := firstDomainRows(candidateResults)
		if len(candidates) > MaxServiceCatalogV3UpgradeRoots {
			return report, fmt.Errorf(
				"repair service catalog v3 startup: more than %d dark candidates: %w",
				MaxServiceCatalogV3UpgradeRoots, ErrInvalidServiceCatalogV3Lifecycle,
			)
		}
		for _, candidate := range candidates {
			if !validServiceCatalogV3CandidateRecord(candidate, candidate.Repository) {
				return report, ErrInvalidServiceCatalogV3Lifecycle
			}
			if _, openErr := s.GetServiceCatalogV3Candidate(ctx, candidate.Repository); openErr != nil {
				return report, fmt.Errorf(
					"repair service catalog v3 startup: candidate %q: %w",
					candidate.Repository, openErr,
				)
			}
		}
		written, writeErr := storeQuery[any](ctx, s.accounting, s.db, `
BEGIN;
LET $current = (SELECT version FROM $rid LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted {
	THROW 'phebs-permanent: unsupported service catalog v3 lifecycle repair'
};
UPSERT $rid SET
	version = IF $current = NONE THEN $wanted ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
			"rid":    serviceCatalogV3LifecycleRepairID(),
			"wanted": serviceCatalogV3LifecycleRepairVersion,
		}, storeWrite(1))
		if writeErr != nil {
			return report, fmt.Errorf("repair service catalog v3 startup: marker write: %w", writeErr)
		}
		for index, result := range *written {
			if result.Error != nil {
				return report, fmt.Errorf(
					"repair service catalog v3 startup marker statement %d: %s",
					index, result.Error.Message,
				)
			}
		}
	}
	if err := s.removeServiceCatalogV3Orphans(ctx, &report); err != nil {
		return report, err
	}
	return report, nil
}

type serviceCatalogV3OrphanDelete struct {
	Deleted int `json:"deleted"`
}

type serviceCatalogV3OrphanMember struct {
	MemberDigest string           `json:"member_digest"`
	RecID        *models.RecordID `json:"id"`
}

type serviceCatalogV3OrphanAuthority struct {
	RecID *models.RecordID `json:"id"`
}

func (s *Surreal) removeServiceCatalogV3Orphans(
	ctx context.Context,
	report *ServiceCatalogV3StartupReport,
) error {
	edgeResults, err := storeQuery[[]serviceCatalogV3RootMemberRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_root_member
	ORDER BY root_digest, ordinal LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3OrphanScan + 1,
	}, storeRead())
	if err != nil {
		return fmt.Errorf("repair service catalog v3 startup: scan orphan edges: %w", err)
	}
	edges := firstDomainRows(edgeResults)
	if len(edges) > MaxServiceCatalogV3OrphanScan {
		report.More = true
		edges = edges[:MaxServiceCatalogV3OrphanScan]
	}
	for _, edge := range edges {
		report.OrphansScanned++
		if report.OrphansDeleted >= MaxServiceCatalogV3OrphanDelete {
			report.More = true
			break
		}
		if !validSHA256Digest(edge.RootDigest) ||
			!validSHA256Digest(edge.MemberDigest) || edge.Ordinal < 0 ||
			edge.Ordinal >= servicecatalogv3.MaxMembers || edge.ContentBytes < 1 ||
			edge.ContentBytes > servicecatalogv3.MaxMemberBytes ||
			!equalServiceCatalogV3RootMember(edge, edge) {
			return ErrInvalidServiceCatalogV3Lifecycle
		}
		deleted, deleteErr := s.deleteServiceCatalogV3Orphan(ctx,
			"service_catalog_v3_lifecycle",
			"SELECT VALUE id FROM service_catalog_v3_lifecycle WHERE root_digest = $root_digest LIMIT 1", `
BEGIN;
LET $owned = array::len(SELECT id FROM service_catalog_v3_lifecycle
	WHERE root_digest = $root_digest LIMIT 1);
LET $deleted = IF $owned = 0 THEN DELETE $rid RETURN BEFORE ELSE [] END;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`, map[string]any{
				"rid": edge.RecID, "root_digest": edge.RootDigest,
			})
		if deleteErr != nil {
			return fmt.Errorf("repair service catalog v3 startup: orphan edge: %w", deleteErr)
		}
		report.OrphansDeleted += deleted
	}

	memberResults, err := storeQuery[[]serviceCatalogV3OrphanMember](ctx, s.accounting, s.db, `
SELECT id, member_digest FROM service_catalog_v3_member
	ORDER BY member_digest LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3OrphanScan + 1,
	}, storeRead())
	if err != nil {
		return fmt.Errorf("repair service catalog v3 startup: scan orphan members: %w", err)
	}
	members := firstDomainRows(memberResults)
	if len(members) > MaxServiceCatalogV3OrphanScan {
		report.More = true
		members = members[:MaxServiceCatalogV3OrphanScan]
	}
	for _, member := range members {
		report.OrphansScanned++
		if report.OrphansDeleted >= MaxServiceCatalogV3OrphanDelete {
			report.More = true
			break
		}
		if !validSHA256Digest(member.MemberDigest) ||
			!validServiceCatalogV3RecordID(
				member.RecID, "service_catalog_v3_member",
				member.MemberDigest[len("sha256:"):],
			) {
			return ErrInvalidServiceCatalogV3Lifecycle
		}
		deleted, deleteErr := s.deleteServiceCatalogV3Orphan(ctx,
			"service_catalog_v3_root_member",
			"SELECT VALUE id FROM service_catalog_v3_root_member WHERE member_digest = $member_digest LIMIT 1", `
BEGIN;
LET $owned = array::len(SELECT id FROM service_catalog_v3_root_member
	WHERE member_digest = $member_digest LIMIT 1);
LET $deleted = IF $owned = 0 THEN DELETE $rid RETURN BEFORE ELSE [] END;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`, map[string]any{
				"rid": member.RecID, "member_digest": member.MemberDigest,
			})
		if deleteErr != nil {
			return fmt.Errorf("repair service catalog v3 startup: orphan member: %w", deleteErr)
		}
		report.OrphansDeleted += deleted
	}

	versionResults, err := storeQuery[[]serviceCatalogV3OrphanAuthority](ctx, s.accounting, s.db, `
SELECT id FROM service_catalog_v3_authority_version
	ORDER BY id LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3OrphanScan + 1,
	}, storeRead())
	if err != nil {
		return fmt.Errorf("repair service catalog v3 startup: scan orphan authority versions: %w", err)
	}
	versions := firstDomainRows(versionResults)
	if len(versions) > MaxServiceCatalogV3OrphanScan {
		report.More = true
		versions = versions[:MaxServiceCatalogV3OrphanScan]
	}
	for _, version := range versions {
		report.OrphansScanned++
		if report.OrphansDeleted >= MaxServiceCatalogV3OrphanDelete {
			report.More = true
			break
		}
		if version.RecID == nil || version.RecID.Table != "service_catalog_v3_authority_version" {
			return ErrInvalidServiceCatalogV3Lifecycle
		}
		identifier, ok := version.RecID.ID.(string)
		if !ok || identifier == "" {
			return ErrInvalidServiceCatalogV3Lifecycle
		}
		deleted, deleteErr := s.deleteServiceCatalogV3Orphan(ctx,
			"service_catalog_v3_lifecycle",
			"SELECT VALUE id FROM service_catalog_v3_lifecycle WHERE authority_version_id = $authority_version_id LIMIT 1", `
BEGIN;
LET $owned = array::len(SELECT id FROM service_catalog_v3_lifecycle
	WHERE authority_version_id = $authority_version_id LIMIT 1);
LET $deleted = IF $owned = 0 THEN DELETE $rid RETURN BEFORE ELSE [] END;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`, map[string]any{
				"rid": version.RecID, "authority_version_id": identifier,
			})
		if deleteErr != nil {
			return fmt.Errorf("repair service catalog v3 startup: orphan authority version: %w", deleteErr)
		}
		report.OrphansDeleted += deleted
	}
	return nil
}

func (s *Surreal) deleteServiceCatalogV3Orphan(
	ctx context.Context,
	ownerTable, ownership, statement string,
	vars map[string]any,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// The three caller-owned recipes submit no write while a real owner exists.
	// An empty read is only eligibility: the unchanged transaction rechecks
	// ownership before deleting, including an owner created after this read.
	probe, err := storeQuery[[]models.RecordID](ctx, s.accounting, s.db, ownership, vars, storeRead())
	if err != nil {
		return 0, err
	}
	if probe == nil || len(*probe) != 1 || (*probe)[0].Status != "OK" ||
		(*probe)[0].Error != nil || (*probe)[0].Result == nil || len((*probe)[0].Result) > 1 {
		return 0, ErrInvalidServiceCatalogV3Lifecycle
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if owners := (*probe)[0].Result; len(owners) != 0 {
		identity, ok := owners[0].ID.(string)
		if owners[0].Table != ownerTable || !ok || identity == "" {
			return 0, ErrInvalidServiceCatalogV3Lifecycle
		}
		return 0, nil
	}
	results, err := storeQuery[[]serviceCatalogV3OrphanDelete](
		ctx, s.accounting, s.db, statement, vars, storeWrite(1),
	)
	if err != nil {
		return 0, err
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Deleted < 0 || rows[0].Deleted > 1 {
		return 0, ErrInvalidServiceCatalogV3Lifecycle
	}
	return rows[0].Deleted, nil
}

func validServiceCatalogV3LifecycleRecord(
	record serviceCatalogV3LifecycleRec,
	root servicecatalogv3.Root,
	rootRecord serviceCatalogV3RootRec,
) bool {
	wanted := serviceCatalogV3LifecycleWanted(root, rootRecord.RecordedAt)
	if !validServiceCatalogV3RecordID(
		record.RecID, "service_catalog_v3_lifecycle",
		root.Digest[len("sha256:"):],
	) || record.RootDigest != wanted.RootDigest ||
		record.Repository != wanted.Repository ||
		record.AuthorityVersion != wanted.AuthorityVersion ||
		record.MemberCount != wanted.MemberCount ||
		record.LogicalBytes != wanted.LogicalBytes ||
		record.RootBytes != wanted.RootBytes ||
		record.MemberBytes != wanted.MemberBytes ||
		!record.RecordedAt.Equal(wanted.RecordedAt) ||
		record.MemberCursor < 0 || record.MemberCursor > record.MemberCount {
		return false
	}
	if record.State == serviceCatalogV3Historical {
		return record.MemberCursor == 0 && record.TombstonedAt == nil
	}
	return record.State == serviceCatalogV3Collecting &&
		record.TombstonedAt != nil && !record.TombstonedAt.IsZero()
}

func (s *Surreal) validateServiceCatalogV3Edges(
	ctx context.Context,
	record serviceCatalogV3LifecycleRec,
	root servicecatalogv3.Root,
	validateMembers bool,
) (int, int64, error) {
	edgeResults, err := storeQuery[[]serviceCatalogV3RootMemberRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_root_member
	WHERE root_digest = $root_digest ORDER BY ordinal LIMIT $limit`, map[string]any{
		"root_digest": record.RootDigest,
		"limit":       servicecatalogv3.MaxMembers + 1,
	}, storeRead())
	if err != nil {
		return 0, 0, err
	}
	edges := firstDomainRows(edgeResults)
	wanted := serviceCatalogV3RootMembers(root)
	descriptors := append(
		append([]servicecatalogv3.MemberDescriptor{}, root.ServiceMembers...),
		root.PlacementMembers...,
	)
	if len(edges) != len(wanted)-record.MemberCursor {
		return 0, 0, ErrInvalidServiceCatalogV3Lifecycle
	}
	var bytes int64
	for index, edge := range edges {
		if !equalServiceCatalogV3RootMember(
			edge, wanted[index+record.MemberCursor],
		) {
			return 0, 0, ErrInvalidServiceCatalogV3Lifecycle
		}
		bytes += int64(edge.ContentBytes)
	}
	if !validateMembers {
		return len(edges), bytes, nil
	}
	digests := make([]string, len(edges))
	for index, edge := range edges {
		digests[index] = edge.MemberDigest
	}
	memberResults, err := storeQuery[[]serviceCatalogV3MemberRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_member
	WHERE member_digest IN $digests LIMIT $limit`, map[string]any{
		"digests": digests, "limit": servicecatalogv3.MaxMembers + 1,
	}, storeRead())
	if err != nil {
		return 0, 0, err
	}
	members := firstDomainRows(memberResults)
	if len(members) != len(edges) {
		return 0, 0, ErrInvalidServiceCatalogV3Lifecycle
	}
	byDigest := make(map[string]serviceCatalogV3MemberRec, len(members))
	for _, member := range members {
		if _, duplicate := byDigest[member.MemberDigest]; duplicate {
			return 0, 0, ErrInvalidServiceCatalogV3Lifecycle
		}
		byDigest[member.MemberDigest] = member
	}
	for index, edge := range edges {
		descriptor := descriptors[index+record.MemberCursor]
		member, ok := byDigest[edge.MemberDigest]
		if !ok || member.ContentBytes != len(member.Content) ||
			!equalServiceCatalogV3Member(member, serviceCatalogV3MemberRec{
				MemberDigest: edge.MemberDigest, Kind: descriptor.Kind,
				Ordinal: descriptor.Ordinal, ContentBytes: descriptor.ContentBytes,
				Content: member.Content,
			}, edge.MemberDigest[len("sha256:"):]) ||
			servicecatalogv3.ValidateMember(
				root, descriptor, []byte(member.Content),
			) != nil {
			return 0, 0, ErrInvalidServiceCatalogV3Lifecycle
		}
	}
	return len(edges), bytes, nil
}

func (s *Surreal) ValidateServiceCatalogV3Precious(
	ctx context.Context,
) (ServiceCatalogV3PreciousReport, error) {
	if err := ctx.Err(); err != nil {
		return ServiceCatalogV3PreciousReport{}, err
	}
	results, err := storeQuery[[]serviceCatalogV3LifecycleRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_lifecycle
	ORDER BY root_digest LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3LifecycleRoots + 1,
	}, storeRead())
	if err != nil {
		return ServiceCatalogV3PreciousReport{}, fmt.Errorf(
			"validate service catalog v3 precious lifecycle: %w", err,
		)
	}
	rows := firstDomainRows(results)
	if len(rows) > MaxServiceCatalogV3LifecycleRoots {
		return ServiceCatalogV3PreciousReport{}, fmt.Errorf(
			"validate service catalog v3 precious: more than %d lifecycle roots: %w",
			MaxServiceCatalogV3LifecycleRoots, ErrInvalidServiceCatalogV3Lifecycle,
		)
	}
	report := ServiceCatalogV3PreciousReport{}
	states := make(map[string]string, len(rows))
	repositories := make(map[string]string, len(rows))
	for _, record := range rows {
		if !validSHA256Digest(record.RootDigest) {
			return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
		}
		rootResults, queryErr := storeQuery[[]serviceCatalogV3RootRec](
			ctx, s.accounting, s.db, "SELECT * FROM $rid",
			map[string]any{"rid": serviceCatalogV3RootID(record.RootDigest)}, storeRead(),
		)
		if queryErr != nil {
			return ServiceCatalogV3PreciousReport{}, queryErr
		}
		rootRows := firstDomainRows(rootResults)
		if len(rootRows) != 1 {
			return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
		}
		rootRecord := rootRows[0]
		root, decodeErr := servicecatalogv3.DecodeRoot([]byte(rootRecord.RootJSON))
		if decodeErr != nil || root.Digest != record.RootDigest ||
			!validServiceCatalogV3LifecycleRecord(record, root, rootRecord) {
			return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
		}
		if record.State == serviceCatalogV3Historical {
			generation, _, openErr := s.openServiceCatalogV3Generation(ctx, record.RootDigest)
			if openErr != nil || generation.Root.Digest != record.RootDigest {
				return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
			}
		}
		members, memberBytes, validateErr := s.validateServiceCatalogV3Edges(
			ctx, record, root, record.State == serviceCatalogV3Collecting,
		)
		if validateErr != nil {
			return ServiceCatalogV3PreciousReport{}, validateErr
		}
		states[record.RootDigest] = record.State
		repositories[record.RootDigest] = record.Repository
		report.RootBytes += int64(record.RootBytes)
		report.MemberBytes += memberBytes
		report.Members += members
		if record.State == serviceCatalogV3Historical {
			report.HistoricalRoots++
			report.LogicalBytes += int64(record.LogicalBytes)
		} else {
			report.CollectingRoots++
		}
	}
	type rootSummary struct {
		RootDigest string `json:"root_digest"`
	}
	rootInventoryResults, err := storeQuery[[]rootSummary](ctx, s.accounting, s.db, `
SELECT root_digest FROM service_catalog_v3_root
	ORDER BY root_digest LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3LifecycleRoots + 1,
	}, storeRead())
	if err != nil {
		return ServiceCatalogV3PreciousReport{}, err
	}
	rootInventory := firstDomainRows(rootInventoryResults)
	if len(rootInventory) != len(states) {
		return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
	}
	for _, root := range rootInventory {
		if _, ok := states[root.RootDigest]; !ok {
			return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
		}
	}
	candidateResults, err := storeQuery[[]serviceCatalogV3CandidateRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_candidate
	ORDER BY repository LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3LifecycleRoots + 1,
	}, storeRead())
	if err != nil {
		return ServiceCatalogV3PreciousReport{}, err
	}
	candidates := firstDomainRows(candidateResults)
	if len(candidates) > MaxServiceCatalogV3LifecycleRoots {
		return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
	}
	candidateRoots := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		if !validServiceCatalogV3CandidateRecord(candidate, candidate.Repository) ||
			states[candidate.RootDigest] != serviceCatalogV3Historical {
			return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
		}
		if _, openErr := s.GetServiceCatalogV3Candidate(ctx, candidate.Repository); openErr != nil {
			return ServiceCatalogV3PreciousReport{}, openErr
		}
		candidateRoots[candidate.Repository] = candidate.RootDigest
	}
	const maxStateReferences = servicecatalogv3.MaxTotalServices*2 + MaxServiceRuntimeSelectors
	referenceResults, err := storeQuery[[]serviceCatalogV3StateReferenceRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_state_reference
	ORDER BY id LIMIT $limit`, map[string]any{"limit": maxStateReferences + 1}, storeRead())
	if err != nil {
		return ServiceCatalogV3PreciousReport{}, err
	}
	references := firstDomainRows(referenceResults)
	if len(references) > maxStateReferences {
		return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
	}
	for _, reference := range references {
		stateKind := reference.Kind == "desired" || reference.Kind == "active"
		if reference.RecID == nil ||
			reference.RecID.Table != "service_catalog_v3_state_reference" ||
			validateCandidateRepository(reference.Repository) != nil ||
			!validSHA256Digest(reference.RootDigest) ||
			states[reference.RootDigest] != serviceCatalogV3Historical ||
			repositories[reference.RootDigest] != reference.Repository ||
			reference.RecordedAt.IsZero() ||
			reference.Kind == "current" &&
				(reference.ServiceKey != "" || reference.StateRootDigest != "") ||
			stateKind &&
				(reference.ServiceKey == "" || !validSHA256Digest(reference.StateRootDigest)) ||
			!stateKind && reference.Kind != "current" {
			return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
		}
	}
	relationshipReferenceResults, err := storeQuery[[]serviceCatalogV3RelationshipReferenceRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_relationship_reference
	ORDER BY id LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3RelationshipReferences + 1,
	}, storeRead())
	if err != nil {
		return ServiceCatalogV3PreciousReport{}, err
	}
	relationshipReferences := firstDomainRows(relationshipReferenceResults)
	if len(relationshipReferences) > MaxServiceCatalogV3RelationshipReferences {
		return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
	}
	for _, reference := range relationshipReferences {
		wanted := ServiceCatalogV3RelationshipReference{
			Repository:                   reference.Repository,
			RelationshipGenerationDigest: reference.RelationshipGenerationDigest,
			RelationshipRootDigest:       reference.RelationshipRootDigest,
			CatalogRootDigest:            reference.CatalogRootDigest,
			CatalogControlRevision:       reference.CatalogControlRevision,
			StateControlRevision:         reference.StateControlRevision,
			StateSummaryDigest:           reference.StateSummaryDigest,
		}
		if validateServiceCatalogV3RelationshipReference(wanted) != nil ||
			!equalServiceCatalogV3RelationshipReference(reference, wanted) ||
			states[reference.CatalogRootDigest] != serviceCatalogV3Historical ||
			repositories[reference.CatalogRootDigest] != reference.Repository {
			return ServiceCatalogV3PreciousReport{}, ErrInvalidServiceCatalogV3Lifecycle
		}
	}
	stateRows, stateSummaries, statePlans, err := s.validateServiceStateV3Precious(
		ctx, states, repositories, candidateRoots,
	)
	if err != nil {
		return ServiceCatalogV3PreciousReport{}, err
	}
	report.StateRows = stateRows
	report.StateSummaries = stateSummaries
	report.StatePlans = statePlans
	report.RelationshipReferences = len(relationshipReferences)
	return report, nil
}

type serviceCatalogV3RetireResult struct {
	Transitioned int `json:"transitioned"`
}

type serviceCatalogV3DrainResult struct {
	Advanced      int `json:"advanced"`
	DeletedMember int `json:"deleted_member"`
}

type serviceCatalogV3FinalizeResult struct {
	DeletedRoot int `json:"deleted_root"`
}

func (s *Surreal) SweepServiceCatalogV3Lifecycle(
	ctx context.Context,
	after string,
	scanLimit, deleteLimit, retained int,
) (ServiceCatalogV3LifecycleSweep, error) {
	if after != "" && !validSHA256Digest(after) {
		return ServiceCatalogV3LifecycleSweep{},
			errors.New("sweep service catalog v3 lifecycle: cursor is invalid")
	}
	if scanLimit < 1 || scanLimit > 64 || deleteLimit < 1 || deleteLimit > 16 ||
		retained < 1 || retained > 8 {
		return ServiceCatalogV3LifecycleSweep{},
			errors.New("sweep service catalog v3 lifecycle: limits are invalid")
	}
	results, err := storeQuery[[]serviceCatalogV3LifecycleRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_lifecycle
	WHERE root_digest > $after ORDER BY root_digest LIMIT $limit`, map[string]any{
		"after": after, "limit": scanLimit,
	}, storeRead())
	if err != nil {
		return ServiceCatalogV3LifecycleSweep{}, fmt.Errorf(
			"scan service catalog v3 lifecycle: %w", err,
		)
	}
	candidates := firstDomainRows(results)
	sweep := ServiceCatalogV3LifecycleSweep{Scanned: len(candidates)}
	if len(candidates) == 0 {
		if after != "" {
			sweep.More = true
		}
		return sweep, nil
	}
	for _, candidate := range candidates {
		if !validSHA256Digest(candidate.RootDigest) {
			return sweep, ErrInvalidServiceCatalogV3Lifecycle
		}
		sweep.Cursor = candidate.RootDigest
		switch candidate.State {
		case serviceCatalogV3Historical:
			preimagesDeleted, preimageErr := s.drainServiceStateV3Preimages(
				ctx,
				candidate,
				deleteLimit,
			)
			if preimageErr != nil {
				return sweep, preimageErr
			}
			if preimagesDeleted > 0 {
				sweep.Deleted += preimagesDeleted
				sweep.More = true
				return sweep, nil
			}
			generation, rootRecord, openErr := s.openServiceCatalogV3Generation(
				ctx, candidate.RootDigest,
			)
			if openErr != nil || !validServiceCatalogV3LifecycleRecord(
				candidate, generation.Root, rootRecord,
			) {
				return sweep, ErrInvalidServiceCatalogV3Lifecycle
			}
			if _, _, edgeErr := s.validateServiceCatalogV3Edges(
				ctx, candidate, generation.Root, false,
			); edgeErr != nil {
				return sweep, edgeErr
			}
			transitioned, retireErr := s.retireServiceCatalogV3Generation(
				ctx, candidate, retained,
			)
			if retireErr != nil {
				return sweep, retireErr
			}
			if transitioned {
				sweep.RetiredLogicalBytes = int64(candidate.LogicalBytes)
				sweep.More = true
				return sweep, nil
			}
		case serviceCatalogV3Collecting:
			deleted, rootBytes, memberBytes, more, drainErr :=
				s.drainServiceCatalogV3Generation(ctx, candidate, deleteLimit)
			if drainErr != nil {
				return sweep, drainErr
			}
			sweep.Deleted += deleted
			sweep.DeletedRootBytes += rootBytes
			sweep.DeletedMemberBytes += memberBytes
			if more || deleted > 0 {
				sweep.More = true
				return sweep, nil
			}
		default:
			return sweep, ErrInvalidServiceCatalogV3Lifecycle
		}
	}
	if len(candidates) == scanLimit {
		sweep.More = true
		return sweep, nil
	}
	sweep.Cursor = ""
	if after != "" {
		sweep.More = true
	}
	return sweep, nil
}

func (s *Surreal) drainServiceStateV3Preimages(
	ctx context.Context,
	candidate serviceCatalogV3LifecycleRec,
	deleteLimit int,
) (int, error) {
	repository := candidate.Repository
	catalogRoot := candidate.RootDigest
	tx, err := storeBegin(ctx, s.accounting, s.db)
	if err != nil {
		return 0, fmt.Errorf("drain service state v3 preimages: begin: %w", err)
	}
	defer func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = storeCancel(cancelCtx, s.accounting, tx)
	}()
	type ownerScan struct {
		Lifecycle serviceCatalogV3LifecycleRec `json:"lifecycle"`
		Root      serviceCatalogV3RootRec      `json:"root"`
		Summaries []serviceRepositoryStateRec  `json:"summaries"`
	}
	results, err := storeQuery[[]ownerScan](ctx, s.accounting, tx, `
RETURN [{
	lifecycle: (SELECT * FROM $lifecycle_rid LIMIT 1)[0],
	root: (SELECT * FROM $root_rid LIMIT 1)[0],
	summaries: (SELECT * FROM service_state_v3_repository_preimage
		WHERE repository = $repository AND catalog_generation = $catalog_root
		ORDER BY snapshot_revision, snapshot_digest LIMIT 2)
}];`, map[string]any{
		"lifecycle_rid": serviceCatalogV3LifecycleID(catalogRoot),
		"root_rid":      serviceCatalogV3RootID(catalogRoot),
		"repository":    repository, "catalog_root": catalogRoot,
	}, storeRead())
	if err != nil {
		return 0, fmt.Errorf("scan service state v3 preimages: %w", err)
	}
	scans := firstDomainRows(results)
	if len(scans) != 1 {
		return 0, ErrInvalidServiceCatalogV3Lifecycle
	}
	lifecycle := scans[0].Lifecycle
	root := scans[0].Root
	if !validServiceCatalogV3RecordID(
		lifecycle.RecID,
		"service_catalog_v3_lifecycle",
		strings.TrimPrefix(catalogRoot, "sha256:"),
	) || lifecycle.Repository != candidate.Repository ||
		lifecycle.RootDigest != candidate.RootDigest ||
		lifecycle.AuthorityVersion != candidate.AuthorityVersion ||
		lifecycle.MemberCount != candidate.MemberCount ||
		lifecycle.LogicalBytes != candidate.LogicalBytes ||
		lifecycle.RootBytes != candidate.RootBytes ||
		lifecycle.MemberBytes != candidate.MemberBytes ||
		!lifecycle.RecordedAt.Equal(candidate.RecordedAt) ||
		lifecycle.State != serviceCatalogV3Historical || lifecycle.MemberCursor != 0 ||
		lifecycle.TombstonedAt != nil ||
		!validServiceCatalogV3RecordID(
			root.RecID,
			"service_catalog_v3_root",
			strings.TrimPrefix(catalogRoot, "sha256:"),
		) || root.RootDigest != catalogRoot || root.Repository != repository ||
		root.RootBytes != candidate.RootBytes ||
		!root.RecordedAt.Equal(candidate.RecordedAt) {
		return 0, ErrInvalidServiceCatalogV3Lifecycle
	}
	summaries := scans[0].Summaries
	if len(summaries) == 0 {
		return 0, nil
	}
	selectorResults, err := storeQuery[[]serviceRuntimeSelectorRec](
		ctx, s.accounting,
		tx,
		"SELECT * FROM $rid",
		map[string]any{"rid": serviceRuntimeSelectorID(repository)}, storeRead(),
	)
	if err != nil {
		return 0, fmt.Errorf("drain service state v3 preimages: selector: %w", err)
	}
	selectorRows := firstDomainRows(selectorResults)
	if len(selectorRows) > 1 {
		return 0, ErrInvalidServiceStateV3
	}
	var selector *ServiceRuntimeSelector
	if len(selectorRows) == 1 {
		decoded, selectorErr := serviceRuntimeSelectorFromRec(selectorRows[0])
		if selectorErr != nil {
			return 0, ErrInvalidServiceStateV3
		}
		selector = &decoded
	}
	var stale *serviceRepositoryStateRec
	for index := range summaries {
		record := &summaries[index]
		summary, summaryErr := serviceStateV3RepositoryFromRec(*record)
		if summaryErr != nil || summary.Repository != repository ||
			summary.CatalogGeneration != catalogRoot ||
			record.SnapshotRevision != summary.ControlRevision ||
			record.SnapshotDigest != summary.SummaryDigest ||
			!validServiceStateV3PreimageRecord(
				record.RecID,
				"service_state_v3_repository_preimage",
			) {
			return 0, ErrInvalidServiceStateV3
		}
		selected := selector != nil && selector.Backend == ServiceRuntimeV3 &&
			selector.StateControlRevision == record.SnapshotRevision &&
			selector.StateSummaryDigest == record.SnapshotDigest
		if !selected {
			stale = record
			break
		}
	}
	if stale == nil {
		return 0, nil
	}
	rowResults, err := storeQuery[[]struct {
		ServiceKey string           `json:"service_key"`
		RecID      *models.RecordID `json:"id"`
	}](ctx, s.accounting, tx, `
SELECT id, service_key FROM service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": repository, "snapshot_revision": stale.SnapshotRevision,
		"snapshot_digest": stale.SnapshotDigest, "limit": deleteLimit,
	}, storeRead())
	if err != nil {
		return 0, fmt.Errorf("drain service state v3 preimages: rows: %w", err)
	}
	rows := firstDomainRows(rowResults)
	rowIDs := make([]models.RecordID, 0, len(rows))
	priorServiceKey := ""
	for _, row := range rows {
		if row.ServiceKey == "" || row.ServiceKey <= priorServiceKey ||
			!validServiceStateV3PreimageRecord(
				row.RecID,
				"service_state_v3_preimage",
			) {
			return 0, ErrInvalidServiceStateV3
		}
		priorServiceKey = row.ServiceKey
		rowIDs = append(rowIDs, *row.RecID)
	}
	var deletedRows []serviceStateRec
	if len(rowIDs) != 0 {
		deletedResults, deleteErr := storeQuery[[]serviceStateRec](ctx, s.accounting, tx, `
DELETE service_state_v3_preimage WHERE id IN $ids RETURN BEFORE`, map[string]any{
			"ids": rowIDs,
		}, storeWrite(uint64(len(rowIDs))))
		if deleteErr != nil {
			return 0, fmt.Errorf("drain service state v3 preimages: delete rows: %w", deleteErr)
		}
		deletedRows = firstDomainRows(deletedResults)
		if len(deletedRows) != len(rowIDs) {
			return 0, ErrInvalidServiceStateV3
		}
		deletedIDs := make(map[string]struct{}, len(deletedRows))
		for _, deleted := range deletedRows {
			if !validServiceStateV3PreimageRecord(
				deleted.RecID,
				"service_state_v3_preimage",
			) || deleted.Repository != repository ||
				deleted.SnapshotRevision != stale.SnapshotRevision ||
				deleted.SnapshotDigest != stale.SnapshotDigest {
				return 0, ErrInvalidServiceStateV3
			}
			identifier, _ := deleted.RecID.ID.(string)
			if _, duplicate := deletedIDs[identifier]; duplicate {
				return 0, ErrInvalidServiceStateV3
			}
			deletedIDs[identifier] = struct{}{}
		}
	}
	deletedSummary := false
	if len(deletedRows) < deleteLimit {
		remainingResults, remainingErr := storeQuery[[]struct {
			RecID *models.RecordID `json:"id"`
		}](ctx, s.accounting, tx, `
SELECT id FROM service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 1`, map[string]any{
			"repository": repository, "snapshot_revision": stale.SnapshotRevision,
			"snapshot_digest": stale.SnapshotDigest,
		}, storeRead())
		if remainingErr != nil {
			return 0, fmt.Errorf("drain service state v3 preimages: remaining: %w", remainingErr)
		}
		if len(firstDomainRows(remainingResults)) == 0 {
			deleted, deleteErr := storeQuery[any](
				ctx, s.accounting,
				tx,
				"DELETE $rid RETURN NONE",
				map[string]any{"rid": *stale.RecID}, storeWrite(1),
			)
			if deleteErr != nil {
				return 0, fmt.Errorf("drain service state v3 preimages: delete summary: %w", deleteErr)
			}
			for _, result := range *deleted {
				if result.Error != nil {
					return 0, fmt.Errorf(
						"drain service state v3 preimages: delete summary: %s",
						result.Error.Message,
					)
				}
			}
			deletedSummary = true
		}
	}
	if err := storeCommit(ctx, s.accounting, tx); err != nil {
		return 0, fmt.Errorf("drain service state v3 preimages: commit: %w", err)
	}
	deleted := len(deletedRows)
	if deletedSummary {
		deleted++
	}
	return deleted, nil
}

func (s *Surreal) retireServiceCatalogV3Generation(
	ctx context.Context,
	candidate serviceCatalogV3LifecycleRec,
	retained int,
) (bool, error) {
	results, err := storeQuery[[]serviceCatalogV3RetireResult](ctx, s.accounting, s.db, `
BEGIN;
LET $row = (SELECT * FROM $rid LIMIT 1)[0];
LET $candidate = (SELECT root_digest FROM service_catalog_v3_candidate
	WHERE repository = $repository LIMIT 1)[0].root_digest;
LET $state_refs = array::len(SELECT id FROM service_catalog_v3_state_reference
	WHERE root_digest = $digest LIMIT 1);
LET $relationship_refs = array::len(SELECT id
	FROM service_catalog_v3_relationship_reference
	WHERE catalog_root_digest = $digest LIMIT 1);
LET $desired_refs = array::len(SELECT id FROM service_state_v3_current
	WHERE desired_catalog_generation = $digest LIMIT 1);
LET $active_refs = array::len(SELECT id FROM service_state_v3_current
	WHERE active_catalog_generation = $digest LIMIT 1);
LET $preimage_desired_refs = array::len(SELECT id FROM service_state_v3_preimage
	WHERE desired_catalog_generation = $digest LIMIT 1);
LET $preimage_active_refs = array::len(SELECT id FROM service_state_v3_preimage
	WHERE active_catalog_generation = $digest LIMIT 1);
LET $preimage_summary_refs = array::len(
	SELECT id FROM service_state_v3_repository_preimage
		WHERE catalog_generation = $digest LIMIT 1);
LET $prior_retained = IF $candidate = NONE THEN $retained ELSE $retained - 1 END;
LET $newest_prior = SELECT VALUE root_digest FROM service_catalog_v3_lifecycle
	WHERE repository = $repository AND state = 'historical'
		AND root_digest != $candidate
	ORDER BY recorded_at DESC, root_digest DESC LIMIT $prior_retained;
LET $eligible = $row != NONE AND $row.root_digest = $digest
	AND $row.repository = $repository AND $row.state = 'historical'
	AND $row.member_cursor = 0 AND $row.member_count = $member_count
	AND $row.logical_bytes = $logical_bytes AND $row.root_bytes = $root_bytes
	AND $row.member_bytes = $member_bytes AND $candidate != $digest
	AND $state_refs = 0 AND $relationship_refs = 0
	AND $desired_refs = 0 AND $active_refs = 0
	AND $preimage_desired_refs = 0 AND $preimage_active_refs = 0
	AND $preimage_summary_refs = 0
	AND $digest NOT IN $newest_prior;
LET $transitioned = IF $eligible THEN
	(UPDATE $rid SET state = 'collecting', tombstoned_at = time::now()
		RETURN AFTER)
	ELSE [] END;
RETURN [{ transitioned: array::len($transitioned) }];
COMMIT;`, map[string]any{
		"rid":    serviceCatalogV3LifecycleID(candidate.RootDigest),
		"digest": candidate.RootDigest, "repository": candidate.Repository,
		"retained": retained, "member_count": candidate.MemberCount,
		"logical_bytes": candidate.LogicalBytes, "root_bytes": candidate.RootBytes,
		"member_bytes": candidate.MemberBytes,
	}, storeWrite(1))
	if err != nil {
		return false, fmt.Errorf("retire service catalog v3 generation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Transitioned < 0 || rows[0].Transitioned > 1 {
		return false, ErrInvalidServiceCatalogV3Lifecycle
	}
	return rows[0].Transitioned == 1, nil
}

func (s *Surreal) drainServiceCatalogV3Generation(
	ctx context.Context,
	candidate serviceCatalogV3LifecycleRec,
	deleteLimit int,
) (deleted int, rootBytes, memberBytes int64, more bool, retErr error) {
	rootResults, err := storeQuery[[]serviceCatalogV3RootRec](
		ctx, s.accounting, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3RootID(candidate.RootDigest)}, storeRead(),
	)
	if err != nil {
		return 0, 0, 0, false, err
	}
	rootRows := firstDomainRows(rootResults)
	if len(rootRows) != 1 {
		return 0, 0, 0, false, ErrInvalidServiceCatalogV3Lifecycle
	}
	rootRecord := rootRows[0]
	root, err := servicecatalogv3.DecodeRoot([]byte(rootRecord.RootJSON))
	if err != nil || root.Digest != candidate.RootDigest ||
		!validServiceCatalogV3LifecycleRecord(candidate, root, rootRecord) {
		return 0, 0, 0, false, ErrInvalidServiceCatalogV3Lifecycle
	}
	if candidate.MemberCursor == candidate.MemberCount {
		finalized, finalizeErr := s.finalizeServiceCatalogV3Generation(ctx, candidate)
		if finalizeErr != nil {
			return 0, 0, 0, false, finalizeErr
		}
		if finalized {
			return 1, int64(candidate.RootBytes), 0, false, nil
		}
		return 0, 0, 0, true, nil
	}
	descriptors := append(
		append([]servicecatalogv3.MemberDescriptor{}, root.ServiceMembers...),
		root.PlacementMembers...,
	)
	if candidate.MemberCursor < 0 || candidate.MemberCursor >= len(descriptors) {
		return 0, 0, 0, false, ErrInvalidServiceCatalogV3Lifecycle
	}
	descriptor := descriptors[candidate.MemberCursor]
	edgeWanted := serviceCatalogV3RootMembers(root)[candidate.MemberCursor]
	edgeResults, err := storeQuery[[]serviceCatalogV3RootMemberRec](
		ctx, s.accounting, s.db, "SELECT * FROM $rid", map[string]any{
			"rid": serviceCatalogV3RootMemberID(root.Digest, descriptor.Digest),
		}, storeRead(),
	)
	if err != nil {
		return 0, 0, 0, false, err
	}
	edges := firstDomainRows(edgeResults)
	if len(edges) != 1 || !equalServiceCatalogV3RootMember(edges[0], edgeWanted) {
		return 0, 0, 0, false, ErrInvalidServiceCatalogV3Lifecycle
	}
	memberResults, err := storeQuery[[]serviceCatalogV3MemberRec](
		ctx, s.accounting, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3MemberID(descriptor.Digest)}, storeRead(),
	)
	if err != nil {
		return 0, 0, 0, false, err
	}
	members := firstDomainRows(memberResults)
	if len(members) != 1 || members[0].ContentBytes != len(members[0].Content) ||
		!equalServiceCatalogV3Member(members[0], serviceCatalogV3MemberRec{
			MemberDigest: descriptor.Digest, Kind: descriptor.Kind,
			Ordinal: descriptor.Ordinal, ContentBytes: descriptor.ContentBytes,
			Content: members[0].Content,
		}, descriptor.Digest[len("sha256:"):]) ||
		servicecatalogv3.ValidateMember(root, descriptor, []byte(members[0].Content)) != nil {
		return 0, 0, 0, false, ErrInvalidServiceCatalogV3Lifecycle
	}
	results, err := storeQuery[[]serviceCatalogV3DrainResult](ctx, s.accounting, s.db, `
BEGIN;
LET $row = (SELECT * FROM $lifecycle_rid LIMIT 1)[0];
LET $candidate = (SELECT root_digest FROM service_catalog_v3_candidate
	WHERE repository = $repository LIMIT 1)[0].root_digest;
LET $state_refs = array::len(SELECT id FROM service_catalog_v3_state_reference
	WHERE root_digest = $root_digest LIMIT 1);
LET $relationship_refs = array::len(SELECT id
	FROM service_catalog_v3_relationship_reference
	WHERE catalog_root_digest = $root_digest LIMIT 1);
IF $row = NONE OR $row.state != 'collecting'
	OR $row.root_digest != $root_digest OR $row.repository != $repository
	OR $row.member_cursor != $member_cursor OR $row.member_count != $member_count
	OR $candidate = $root_digest OR $state_refs != 0
	OR $relationship_refs != 0 {
	THROW 'phebs-permanent: service catalog v3 collecting fence changed'
};
LET $edge = (SELECT * FROM $edge_rid LIMIT 1)[0];
LET $member = (SELECT member_digest, kind, ordinal, content_bytes
	FROM $member_rid LIMIT 1)[0];
IF $edge = NONE OR $edge.root_digest != $root_digest
	OR $edge.member_digest != $member_digest OR $edge.ordinal != $member_cursor
	OR $edge.content_bytes != $content_bytes OR $member = NONE
	OR $member.member_digest != $member_digest OR $member.kind != $kind
	OR $member.ordinal != $member_ordinal OR $member.content_bytes != $content_bytes {
	THROW 'phebs-permanent: service catalog v3 collecting member changed'
};
LET $shared = array::len(SELECT id FROM service_catalog_v3_root_member
	WHERE member_digest = $member_digest AND root_digest != $root_digest LIMIT 1);
LET $deleted_member = IF $shared = 0 THEN
	(DELETE $member_rid RETURN BEFORE)
	ELSE [] END;
DELETE $edge_rid RETURN NONE;
LET $advanced = UPDATE $lifecycle_rid SET member_cursor = $member_cursor + 1
	WHERE state = 'collecting' AND member_cursor = $member_cursor RETURN AFTER;
RETURN [{ advanced: array::len($advanced), deleted_member: array::len($deleted_member) }];
COMMIT;`, map[string]any{
		"lifecycle_rid": serviceCatalogV3LifecycleID(root.Digest),
		"edge_rid":      serviceCatalogV3RootMemberID(root.Digest, descriptor.Digest),
		"member_rid":    serviceCatalogV3MemberID(descriptor.Digest),
		"repository":    candidate.Repository, "root_digest": root.Digest,
		"member_cursor": candidate.MemberCursor, "member_count": candidate.MemberCount,
		"member_digest": descriptor.Digest, "content_bytes": descriptor.ContentBytes,
		"kind": descriptor.Kind, "member_ordinal": descriptor.Ordinal,
		"delete_limit": deleteLimit,
	}, storeWrite(3))
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("drain service catalog v3 generation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Advanced != 1 ||
		rows[0].DeletedMember < 0 || rows[0].DeletedMember > 1 {
		return 0, 0, 0, false, ErrInvalidServiceCatalogV3Lifecycle
	}
	if rows[0].DeletedMember == 1 {
		deleted = 1
		memberBytes = int64(descriptor.ContentBytes)
	}
	return deleted, 0, memberBytes, true, nil
}

func (s *Surreal) finalizeServiceCatalogV3Generation(
	ctx context.Context,
	candidate serviceCatalogV3LifecycleRec,
) (bool, error) {
	results, err := storeQuery[[]serviceCatalogV3FinalizeResult](ctx, s.accounting, s.db, `
BEGIN;
LET $row = (SELECT * FROM $lifecycle_rid LIMIT 1)[0];
LET $candidate = (SELECT root_digest FROM service_catalog_v3_candidate
	WHERE repository = $repository LIMIT 1)[0].root_digest;
LET $state_refs = array::len(SELECT id FROM service_catalog_v3_state_reference
	WHERE root_digest = $root_digest LIMIT 1);
LET $relationship_refs = array::len(SELECT id
	FROM service_catalog_v3_relationship_reference
	WHERE catalog_root_digest = $root_digest LIMIT 1);
LET $edges = array::len(SELECT id FROM service_catalog_v3_root_member
	WHERE root_digest = $root_digest LIMIT 1);
IF $row = NONE OR $row.state != 'collecting'
	OR $row.member_cursor != $row.member_count OR $candidate = $root_digest
	OR $state_refs != 0 OR $relationship_refs != 0 OR $edges != 0 {
	THROW 'phebs-permanent: service catalog v3 finalize fence changed'
};
LET $deleted_root = DELETE $root_rid RETURN BEFORE;
DELETE $lifecycle_rid RETURN NONE;
LET $other_authority = array::len(SELECT id FROM service_catalog_v3_lifecycle
	WHERE authority_version_id = $authority_version_id LIMIT 1);
IF $other_authority = 0 {
	DELETE $authority_rid RETURN NONE
};
RETURN [{ deleted_root: array::len($deleted_root) }];
COMMIT;`, map[string]any{
		"lifecycle_rid": serviceCatalogV3LifecycleID(candidate.RootDigest),
		"root_rid":      serviceCatalogV3RootID(candidate.RootDigest),
		"authority_rid": models.NewRecordID(
			"service_catalog_v3_authority_version", candidate.AuthorityVersion,
		),
		"root_digest": candidate.RootDigest, "repository": candidate.Repository,
		"authority_version_id": candidate.AuthorityVersion,
	}, storeWrite(3))
	if err != nil {
		return false, fmt.Errorf("finalize service catalog v3 generation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].DeletedRoot != 1 {
		return false, ErrInvalidServiceCatalogV3Lifecycle
	}
	return true, nil
}
