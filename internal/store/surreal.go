package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

//go:embed schema.surql
var schema string

// Surreal implements Store over the official SDK (WebSocket RPC).
type Surreal struct {
	db   *surrealdb.DB
	stop func() // non-nil when we supervise a local child
}

var _ Store = (*Surreal)(nil)

// OpenLocal starts a supervised surreal child storing under dataDir and
// connects to it. This is the single-node default (dev and prod).
func OpenLocal(ctx context.Context, dataDir string) (*Surreal, error) {
	return openLocal(ctx, dataDir, "", false)
}

// OpenLocalMemory is the test bootstrap seam: the identical supervised child,
// WS session, runtime descriptor, schema, and migrations as OpenLocal, but on
// SurrealDB's volatile in-process "memory" engine. A fresh surrealkv data
// directory pays ~6s of per-DEFINE fsync cost applying the schema; memory
// applies it in ~40ms. Nothing survives Close, so production servers and any
// test that reopens a data directory must use OpenLocal.
func OpenLocalMemory(ctx context.Context, dataDir string) (*Surreal, error) {
	return openLocal(ctx, dataDir, "", true)
}

// OpenLocalWithConfig starts the production local store and binds its runtime
// descriptor to the exact raw configuration bytes used by the server. Live
// backup refuses a different config that merely points at the same data dir.
func OpenLocalWithConfig(ctx context.Context, dataDir, configSHA256 string) (*Surreal, error) {
	if !validSHA256(configSHA256) {
		return nil, errors.New("open local store: config digest is invalid")
	}
	return openLocal(ctx, dataDir, configSHA256, false)
}

func openLocal(ctx context.Context, dataDir, configSHA256 string, memory bool) (*Surreal, error) {
	if err := checkLocalRuntimeAvailable(dataDir); err != nil {
		return nil, err
	}
	var runtime LocalRuntime
	var stop func()
	var err error
	if memory {
		runtime, stop, err = startEngine(ctx, "memory")
	} else {
		runtime, stop, err = startLocal(ctx, dataDir)
	}
	if err != nil {
		return nil, err
	}
	runtime.ConfigSHA256 = configSHA256
	s, err := Open(ctx, runtime.Endpoint, "root", "root", "phebs", "phebs")
	if err != nil {
		stop()
		return nil, err
	}
	removeRuntime, err := PublishLocalRuntime(dataDir, runtime)
	if err != nil {
		_ = s.db.Close(context.Background())
		stop()
		return nil, err
	}
	s.stop = func() {
		removeRuntime()
		stop()
	}
	return s, nil
}

// Open connects to a running SurrealDB, selects ns/db, and applies the
// schema idempotently. Server-mode path for the fleet profile.
func Open(ctx context.Context, endpoint, user, pass, namespace, database string) (*Surreal, error) {
	db, err := surrealdb.FromEndpointURLString(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", endpoint, err)
	}
	return openConnected(ctx, db, user, pass, namespace, database)
}

func openConnected(
	ctx context.Context,
	db *surrealdb.DB,
	user, pass, namespace, database string,
) (_ *Surreal, retErr error) {
	failed := true
	defer func() {
		if !failed {
			return
		}
		if err := db.Close(ctx); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close store connection: %w", err))
		}
	}()
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: user, Password: pass}); err != nil {
		return nil, fmt.Errorf("signin: %w", err)
	}
	if err := db.Use(ctx, namespace, database); err != nil {
		return nil, fmt.Errorf("use %s/%s: %w", namespace, database, err)
	}
	s := &Surreal{db: db}
	if err := s.applySchema(ctx); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	failed = false
	return s, nil
}

func (s *Surreal) applySchema(ctx context.Context) error {
	results, err := surrealdb.Query[any](ctx, s.db, schema, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("statement %d: %s", i, r.Error.Message)
		}
	}
	if err := s.migrateGenerationResourceClasses(ctx); err != nil {
		return err
	}
	if err := s.migrateServiceCatalogV3Schema(ctx); err != nil {
		return err
	}
	if err := s.migrateServiceCatalogV3LifecycleSchema(ctx); err != nil {
		return err
	}
	if err := s.migrateServiceStateV3Schema(ctx); err != nil {
		return err
	}
	if err := s.migrateCandidateControlRevisions(ctx); err != nil {
		return err
	}
	if err := s.migrateServiceStateV3SnapshotSchema(ctx); err != nil {
		return err
	}
	if err := s.migrateServiceCatalogV3RelationshipReferenceSchema(ctx); err != nil {
		return err
	}
	if err := s.migrateServiceRuntimeSelectorSchema(ctx); err != nil {
		return err
	}
	results, err = surrealdb.Query[any](ctx, s.db, apiKeyCapabilityPreMigrationSchema, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("API key capability pre-migration statement %d: %s", i, r.Error.Message)
		}
	}
	if err := s.migrateAPIKeyCapabilities(ctx); err != nil {
		return err
	}
	if err := s.validateServiceRuntimeSelectorStore(ctx); err != nil {
		return err
	}
	if err := s.migrateResolverCatalogWriter(ctx); err != nil {
		return err
	}
	if err := s.migrateCallerLeafWriter(ctx); err != nil {
		return err
	}
	if err := s.migrateCallerGenerationPublications(ctx); err != nil {
		return err
	}
	results, err = surrealdb.Query[any](ctx, s.db, apiKeyCapabilitySchema, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("API key capability schema statement %d: %s", i, r.Error.Message)
		}
	}
	results, err = surrealdb.Query[any](ctx, s.db, evidencePreMigrationSchema, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("evidence pre-migration statement %d: %s", i, r.Error.Message)
		}
	}
	if err := s.migrateLegacyJobs(ctx); err != nil {
		return err
	}
	if err := s.migrateEvidenceRuns(ctx); err != nil {
		return err
	}
	results, err = surrealdb.Query[any](ctx, s.db, evidenceIndexes, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("evidence index statement %d: %s", i, r.Error.Message)
		}
	}
	return nil
}

const candidateControlRevisionMigrationVersion = "t30.6b-candidate-control-v1"

func candidateControlRevisionMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "candidate_control_revision")
}

// migrateCandidateControlRevisions gives pre-T30.6b derived pointers their
// initial durable control identity. The fixed completion row keeps steady-state
// startup off the publication table.
func (s *Surreal) migrateCandidateControlRevisions(ctx context.Context) error {
	marker := candidateControlRevisionMigrationID()
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $version = (SELECT version FROM $marker LIMIT 1)[0].version;
UPDATE candidate_manifest_publication SET control_revision = 1
	WHERE $version = NONE
		AND (control_revision = NONE OR control_revision < 1)
	RETURN NONE;
UPSERT $marker SET
	version = IF $version = NONE THEN $wanted ELSE $version END,
	completed_at = IF $version = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"marker": marker,
		"wanted": candidateControlRevisionMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate candidate control revisions: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate candidate control revisions statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	check, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $marker", map[string]any{"marker": marker})
	if err != nil {
		return fmt.Errorf("migrate candidate control revisions: verify: %w", err)
	}
	var markerVersion string
	for _, result := range *check {
		for _, row := range result.Result {
			markerVersion = row.Version
		}
	}
	if markerVersion == candidateControlRevisionMigrationVersion ||
		markerVersion == serviceRuntimeSelectorCompatibilityMigrationVersion ||
		markerVersion == serviceStateV3SnapshotCompatibilityMigrationVersion {
		return nil
	}
	if markerVersion != "" {
		return fmt.Errorf(
			"migrate candidate control revisions: unsupported marker %q",
			markerVersion,
		)
	}
	return errors.New(
		"migrate candidate control revisions: completion marker missing")
}

const apiKeyCapabilityMigrationVersion = "t21.12-api-key-capabilities-v1"

type apiKeyCapabilityMigrationStateRec struct {
	Version string `json:"version"`
}

func apiKeyCapabilityMigrationStateID() models.RecordID {
	return models.NewRecordID("store_migration", "api_key_capabilities")
}

const apiKeyCapabilityPreMigrationSchema = `
DEFINE FIELD IF NOT EXISTS capabilities ON api_key TYPE option<array<string>>;`

const apiKeyCapabilitySchema = `
DEFINE FIELD OVERWRITE capabilities ON api_key TYPE array<string> DEFAULT []
	ASSERT $value = [] OR $value = ['investigation:write'];
DEFINE EVENT IF NOT EXISTS api_key_capabilities_immutable ON TABLE api_key
	WHEN $event = 'UPDATE'
	  AND $before.capabilities != NONE
	  AND $before.capabilities != $after.capabilities
	THEN {
		THROW 'phebs-permanent: API key capabilities are immutable'
	};`

// migrateAPIKeyCapabilities gives every pre-T21.12 row the explicit empty
// capability set. The completion marker keeps steady-state Open off the key
// table. Its version is checked again inside the transaction so a concurrent
// later binary cannot have its marker overwritten by this generation.
func (s *Surreal) migrateAPIKeyCapabilities(ctx context.Context) error {
	complete, err := s.apiKeyCapabilityMigrationComplete(ctx)
	if err != nil {
		return fmt.Errorf("migrate API key capabilities: %w", err)
	}
	if complete {
		return nil
	}

	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $marker_version = (SELECT version FROM $marker LIMIT 1)[0].version;
UPDATE api_key SET capabilities = []
	WHERE $marker_version = NONE AND capabilities = NONE RETURN NONE;
UPSERT $marker SET
	version = IF $marker_version = NONE THEN $version ELSE $marker_version END,
	completed_at = IF $marker_version = NONE
		THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"marker":  apiKeyCapabilityMigrationStateID(),
		"version": apiKeyCapabilityMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate API key capabilities: %w", err)
	}
	for i, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate API key capabilities statement %d: %s",
				i,
				result.Error.Message,
			)
		}
	}
	complete, err = s.apiKeyCapabilityMigrationComplete(ctx)
	if err != nil {
		return fmt.Errorf("migrate API key capabilities: %w", err)
	}
	if !complete {
		return errors.New("migrate API key capabilities: completion marker missing")
	}
	return nil
}

func (s *Surreal) apiKeyCapabilityMigrationComplete(
	ctx context.Context,
) (bool, error) {
	results, err := surrealdb.Query[[]apiKeyCapabilityMigrationStateRec](
		ctx,
		s.db,
		"SELECT version FROM $rid",
		map[string]any{"rid": apiKeyCapabilityMigrationStateID()},
	)
	if err != nil {
		return false, err
	}
	for index, result := range *results {
		if result.Error != nil {
			return false, fmt.Errorf(
				"read completion marker statement %d: %s",
				index,
				result.Error.Message,
			)
		}
		for _, row := range result.Result {
			if row.Version == apiKeyCapabilityMigrationVersion {
				return true, nil
			}
			return false, fmt.Errorf(
				"unsupported completion marker version %q",
				row.Version,
			)
		}
	}
	return false, nil
}

const pendingJobIndexes = `
DEFINE INDEX IF NOT EXISTS connection_sync_job_pending_key ON connection_sync_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS indexing_job_pending_key ON indexing_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS repo_fetch_job_pending_key ON repo_fetch_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS candidate_manifest_job_pending_key ON candidate_manifest_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS extraction_job_pending_key ON extraction_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS resolver_catalog_job_pending_key ON resolver_catalog_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS caller_leaf_job_pending_key ON caller_leaf_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_run_job_pending_key ON investigation_run_job FIELDS pending_key UNIQUE;`

// retiredEvidenceStoreSchemas are the writer generations this binary neither
// writes nor upgrades: they are skipped/retracted generations or have aged
// beyond the explicit two-predecessor migration window, so their rows are
// quarantined. Supported predecessors are retired for writes but upgraded
// rather than quarantined, so they are named separately and appended by
// retiredEvidenceWriterGenerations.
//
// This is the single source of truth on purpose. Four hand-maintained copies
// previously had to be advanced together, and missing one strands a published
// row against its unique published_key forever — the exact failure recorded for
// v3–v5 (see migrateEvidenceRuns).
var retiredEvidenceStoreSchemas = []string{
	"t12-store-v1", "t12-store-v2", "t12-store-v3",
	"t12-store-v4", "t12-store-v5", "t12-store-v6",
}

// retiredEvidenceWriterGenerations is every generation this binary refuses to
// write: the quarantined set plus every upgraded-in-place predecessor. A row
// at a supported predecessor is migrated forward, but nothing may create a new
// one.
func retiredEvidenceWriterGenerations() []string {
	return append(slices.Clone(retiredEvidenceStoreSchemas),
		evidencePreviousStoreSchemaVersion,
		evidenceLegacyUpgradableStoreSchemaVersion,
		evidencePreUnitUpgradableStoreSchemaVersion)
}

// surrealStringList renders a SurrealQL array literal. Every caller passes
// package constants, so a value carrying a quote or backslash is a programming
// error in this file rather than untrusted input; failing at package
// initialization is preferable to emitting SQL that silently means something
// else.
func surrealStringList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		if strings.ContainsAny(value, "'\"\\") {
			panic("schema identifier is not a bare token: " + value)
		}
		quoted[i] = "'" + value + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// This generation-specific event is intentionally installed before migration
// and never overwritten by later generations. Unlike a field assertion, an
// older binary does not know its name and therefore cannot weaken it while
// reapplying its own schema. The synchronous THROW rolls the attempted retired
// writer mutation back in the same transaction. The compound index is likewise
// generation-named so IF NOT EXISTS is an idempotent one-time build.
var evidencePreMigrationSchema = fmt.Sprintf(`
DEFINE EVENT IF NOT EXISTS %s ON TABLE extraction_run
	WHEN $event != 'DELETE'
	  AND $after.store_schema_version IN %s
	THEN {
		THROW 'phebs-permanent: retired evidence writer generation'
	};
DEFINE INDEX IF NOT EXISTS %s ON TABLE assertion
	FIELDS run_id, predicate, object, repo, lineage, subject, assertion_id;`,
	evidenceWriterGuardEvent,
	surrealStringList(retiredEvidenceWriterGenerations()),
	reverseAssertionIndexName)

var evidenceIndexes = fmt.Sprintf(`
DEFINE FIELD OVERWRITE status ON extraction_run TYPE string
    ASSERT $value INSIDE ['staged', 'published', 'superseded', 'aborted', 'deleting']
        OR $this.evidence_format_version != '%s';
DEFINE FIELD OVERWRITE store_schema_version ON extraction_run TYPE string
	ASSERT $value NOT IN %s;
DEFINE INDEX IF NOT EXISTS extraction_run_published_key ON extraction_run FIELDS published_key UNIQUE;
DEFINE FIELD OVERWRITE status ON extraction_attempt TYPE string
    ASSERT $value INSIDE ['staged', 'published', 'aborted'];`,
	evidenceFormatVersion,
	surrealStringList(retiredEvidenceWriterGenerations()))

const (
	jobActiveMigrationVersion = "t30.6n-active-jobs-v1"
	// maxJobActiveMigrationRows is an installation-wide safety/refusal bound
	// for unsupported pre-fence active state, not a runtime queue-cardinality
	// or retention claim. The supported predecessor already has pending guards.
	// The cap is per table and excludes terminal history through the existing
	// status index; a larger legacy active set refuses instead of widening Open.
	maxJobActiveMigrationRows = 131_072
)

var durableJobKinds = [...]JobKind{
	JobSync, JobIndex, JobFetch, JobCandidate, JobExtract,
	JobResolverCatalog, JobCallerLeaf, JobInvestigate,
}

func jobActiveMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "active_jobs")
}

type jobActiveMigrationState struct {
	Version string `json:"version"`
}

// migrateLegacyJobs reads a cap-plus-one active window through the status
// index and marks completion durably. Sorting that bounded window in Go avoids
// a server-side sort over either active or terminal history. Old rows had no
// lease or pending slot, and may contain an active job plus a successor. Keep
// the oldest pending row, cancel duplicates, then requeue only an unfenced
// active row that has no successor. Terminal rows are never selected, decoded,
// sorted, or rewritten.
func (s *Surreal) migrateLegacyJobs(ctx context.Context) error {
	complete, err := s.jobActiveMigrationComplete(ctx)
	if err != nil {
		return fmt.Errorf("migrate active jobs: %w", err)
	}
	if complete {
		return nil
	}
	missingPendingIndexes, err := s.missingPendingJobIndexes(ctx)
	if err != nil {
		return fmt.Errorf("migrate active jobs: inspect pending indexes: %w", err)
	}
	for _, kind := range missingPendingIndexes {
		nonempty, queryErr := s.jobTableNonempty(ctx, kind)
		if queryErr != nil {
			return fmt.Errorf("migrate active jobs: inspect %s: %w", kind, queryErr)
		}
		if nonempty {
			return fmt.Errorf(
				"migrate active jobs: nonempty %s lacks its required pending-key index",
				kind,
			)
		}
	}

	for _, kind := range durableJobKinds {
		jobs, err := s.listActiveJobsForMigration(ctx, kind)
		if err != nil {
			return fmt.Errorf("migrate %s: list: %w", kind, err)
		}
		pending := make(map[string]bool)
		for _, job := range jobs {
			if job.Status != StatusPending {
				continue
			}
			if pending[job.Target] {
				if err := s.updateLegacyJob(ctx, job.ID,
					`status = 'canceled', error = 'migration: duplicate pending job',
					 finished_at = time::now(), pending_key = NONE`, StatusPending); err != nil {
					return fmt.Errorf("migrate %s duplicate %s: %w", kind, job.ID, err)
				}
				continue
			}
			if err := s.updateLegacyJob(ctx, job.ID, "pending_key = $target", StatusPending,
				"target", job.Target); err != nil {
				return fmt.Errorf("migrate %s pending %s: %w", kind, job.ID, err)
			}
			pending[job.Target] = true
		}

		for _, job := range jobs {
			if (job.Status != StatusClaimed && job.Status != StatusRunning) || job.LeaseToken != "" {
				continue
			}
			if pending[job.Target] {
				if err := s.updateLegacyJob(ctx, job.ID,
					`status = 'canceled', attempts = $attempts,
					 error = 'migration: unfenced job superseded', finished_at = time::now(),
					 claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE,
					 lease_token = NONE, pending_key = NONE`, job.Status,
					"attempts", job.Attempts+1); err != nil {
					return fmt.Errorf("migrate %s superseded %s: %w", kind, job.ID, err)
				}
				continue
			}
			if err := s.updateLegacyJob(ctx, job.ID,
				`status = 'pending', attempts = $attempts,
				 error = 'requeued: legacy claim without lease', not_before = NONE,
				 claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE,
				 lease_token = NONE, pending_key = $target`, job.Status,
				"attempts", job.Attempts+1, "target", job.Target); err != nil {
				return fmt.Errorf("migrate %s active %s: %w", kind, job.ID, err)
			}
			pending[job.Target] = true
		}
	}

	results, err := surrealdb.Query[any](ctx, s.db, pendingJobIndexes, nil)
	if err != nil {
		return fmt.Errorf("migrate active jobs: install pending indexes: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate active jobs: pending index statement %d: %s",
				index,
				result.Error.Message,
			)
		}
	}

	results, err = surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $marker LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted {
	THROW 'phebs-permanent: unsupported active-job migration generation'
};
UPSERT $marker SET
	version = IF $current = NONE THEN $wanted ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"marker": jobActiveMigrationID(), "wanted": jobActiveMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate active jobs: record completion: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate active jobs: completion statement %d: %s",
				index,
				result.Error.Message,
			)
		}
	}
	complete, err = s.jobActiveMigrationComplete(ctx)
	if err != nil {
		return fmt.Errorf("migrate active jobs: verify completion: %w", err)
	}
	if !complete {
		return errors.New("migrate active jobs: completion marker missing")
	}
	return nil
}

func (s *Surreal) jobActiveMigrationComplete(ctx context.Context) (bool, error) {
	results, err := surrealdb.Query[[]jobActiveMigrationState](ctx, s.db,
		"SELECT version FROM $marker LIMIT 1",
		map[string]any{"marker": jobActiveMigrationID()})
	if err != nil {
		return false, err
	}
	var version string
	for _, result := range *results {
		if len(result.Result) > 0 {
			version = result.Result[0].Version
		}
	}
	switch version {
	case "":
		return false, nil
	case jobActiveMigrationVersion:
		return true, nil
	default:
		return false, fmt.Errorf("unsupported marker %q", version)
	}
}

func (s *Surreal) listActiveJobsForMigration(
	ctx context.Context,
	kind JobKind,
) ([]Job, error) {
	if !validJobKind(kind) {
		return nil, fmt.Errorf("invalid job kind %q", kind)
	}
	statement := fmt.Sprintf(`SELECT id, target, status, attempts, created_at, lease_token
		FROM %s WITH INDEX %s_status
		WHERE status IN ['pending', 'claimed', 'running']
		LIMIT $limit`, kind, kind)
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, statement,
		map[string]any{"limit": maxJobActiveMigrationRows + 1})
	if err != nil {
		return nil, err
	}
	var rows []jobRec
	for _, result := range *results {
		rows = append(rows, result.Result...)
	}
	if len(rows) > maxJobActiveMigrationRows {
		return nil, fmt.Errorf(
			"active row count exceeds the per-table migration bound of %d",
			maxJobActiveMigrationRows,
		)
	}
	jobs := make([]Job, len(rows))
	for index, row := range rows {
		jobs[index] = row.toJob(kind)
	}
	slices.SortFunc(jobs, func(left, right Job) int {
		if order := left.CreatedAt.Compare(right.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	return jobs, nil
}

func (s *Surreal) missingPendingJobIndexes(ctx context.Context) ([]JobKind, error) {
	missing := make([]JobKind, 0, len(durableJobKinds))
	for _, kind := range durableJobKinds {
		statement := fmt.Sprintf("INFO FOR TABLE %s", kind)
		results, err := surrealdb.Query[map[string]any](ctx, s.db, statement, nil)
		if err != nil {
			return nil, err
		}
		present := false
		for _, result := range *results {
			indexes, ok := result.Result["indexes"].(map[string]any)
			if !ok {
				continue
			}
			_, present = indexes[string(kind)+"_pending_key"]
		}
		if !present {
			missing = append(missing, kind)
		}
	}
	return missing, nil
}

func (s *Surreal) jobTableNonempty(ctx context.Context, kind JobKind) (bool, error) {
	if !validJobKind(kind) {
		return false, fmt.Errorf("invalid job kind %q", kind)
	}
	results, err := surrealdb.Query[[]struct {
		RecID *models.RecordID `json:"id"`
	}](ctx, s.db, fmt.Sprintf("SELECT id FROM %s LIMIT 1", kind), nil)
	if err != nil {
		return false, err
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func validJobKind(kind JobKind) bool {
	for _, candidate := range durableJobKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

type evidenceRunMigrationRec struct {
	RecID                *models.RecordID `json:"id"`
	RunID                any              `json:"run_id"`
	Repo                 any              `json:"repo"`
	Commit               any              `json:"commit"`
	UnitDigest           any              `json:"unit_digest"`
	Domain               any              `json:"domain"`
	Status               any              `json:"status"`
	StoreSchema          any              `json:"store_schema_version"`
	Format               any              `json:"evidence_format_version"`
	AmbiguousRunID       any              `json:"evidence_migration_ambiguous_run_id"`
	RetentionQuarantined any              `json:"retention_quarantined"`
	RetentionPhase       any              `json:"retention_phase"`
}

type evidenceMigrationStateRec struct {
	RecID   *models.RecordID `json:"id"`
	Version any              `json:"version"`
}

type evidencePinMigrationRec struct {
	RecID *models.RecordID `json:"id"`
	Kind  any              `json:"kind"`
}

type evidenceAttemptMigrationRec struct {
	RecID       *models.RecordID `json:"id"`
	RunID       any              `json:"run_id"`
	Repo        any              `json:"repo"`
	Commit      any              `json:"commit"`
	UnitDigest  any              `json:"unit_digest"`
	Domain      any              `json:"domain"`
	Extractor   any              `json:"extractor"`
	Status      any              `json:"status"`
	StartedAt   any              `json:"started_at"`
	StoreSchema any              `json:"store_schema_version"`
	Format      any              `json:"evidence_format_version"`
}

// retiredAttemptSchema marks an attempt row this binary will not migrate. It is
// deliberately generation-neutral: the row is inert either way, so no future
// writer bump has to advance this literal. Rows retired by earlier binaries
// carry a generation-specific marker and stay equally invisible.
const retiredAttemptSchema = "t12-store-retired-attempt"

func (s *Surreal) retireEvidenceAttempt(ctx context.Context, rid models.RecordID) error {
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE $rid SET store_schema_version = $retired_schema,
			evidence_migration_version = $migration RETURN NONE`,
		map[string]any{
			"rid": rid, "retired_schema": retiredAttemptSchema,
			"migration": evidenceMigrationVersion,
		}); err != nil {
		return fmt.Errorf("retire malformed attempt: %w", err)
	}
	return nil
}

// evidenceAttemptMigrationFields validates the fields every migration path
// needs. A row failing any of them is retired rather than carried forward:
// stamping it current would make a malformed diagnostic visible.
func evidenceAttemptMigrationFields(
	row evidenceAttemptMigrationRec,
) (runID, repo, commit, domain, extractor, status string, ok bool) {
	runID, runOK := validMigrationIdentity(row.RunID)
	repo, repoOK := validMigrationIdentity(row.Repo)
	commit, commitOK := validMigrationIdentity(row.Commit)
	domain, domainOK := validMigrationIdentity(row.Domain)
	extractor, extractorOK := validMigrationIdentity(row.Extractor)
	status, statusOK := migrationString(row.Status)
	format, formatOK := migrationString(row.Format)
	ok = runOK && repoOK && commitOK && domainOK && extractorOK &&
		statusOK && (status == "staged" || status == "published" || status == "aborted") &&
		formatOK && format == evidenceFormatVersion && row.StartedAt != nil
	return runID, repo, commit, domain, extractor, status, ok
}

func validMigrationIdentity(value any) (string, bool) {
	text, ok := migrationString(value)
	return text, ok && strings.TrimSpace(text) != "" && utf8.ValidString(text) &&
		len(text) <= maxEvidenceIdentityBytes
}

// migrateEvidenceAttempts carries latest-attempt diagnostics forward from all
// supported predecessors. Their shapes need different treatment and must not be
// conflated: binding the pre-unit reshape to "the previous generation" instead
// of to its own literal is what would force a correctly keyed unit-scoped row
// into the whole-repository slot on the next bump, and hard-fail Open on the
// resulting collision.
func (s *Surreal) migrateEvidenceAttempts(ctx context.Context) error {
	if err := s.stampEvidenceAttempts(ctx); err != nil {
		return err
	}
	return s.reshapeLegacyEvidenceAttempts(ctx)
}

// stampEvidenceAttempts advances rows already written in the current shape.
// Their record id and unit digest are already correct, so the row is stamped in
// place: no move means no destination to collide with, and the pass is
// idempotent across a crash.
func (s *Surreal) stampEvidenceAttempts(ctx context.Context) error {
	for {
		results, err := surrealdb.Query[[]evidenceAttemptMigrationRec](ctx, s.db,
			`SELECT id, run_id, repo, commit, unit_digest, domain, extractor, status,
				started_at, store_schema_version, evidence_format_version
			FROM extraction_attempt
			WHERE store_schema_version IN $previous_schemas
			ORDER BY id LIMIT $limit`, map[string]any{
				"previous_schemas": []string{
					evidencePreviousStoreSchemaVersion,
					evidenceLegacyUpgradableStoreSchemaVersion,
				},
				"limit": evidenceMigrationBatchSize,
			})
		if err != nil {
			return err
		}
		var candidates []evidenceAttemptMigrationRec
		for _, result := range *results {
			candidates = append(candidates, result.Result...)
		}
		if len(candidates) == 0 {
			return nil
		}

		for _, row := range candidates {
			if row.RecID == nil {
				return errors.New("attempt has no physical record id")
			}
			_, _, _, _, _, _, fieldsOK := evidenceAttemptMigrationFields(row)
			unitDigest, unitOK := migrationString(row.UnitDigest)
			if !fieldsOK || !unitOK ||
				(unitDigest != "" && !validSHA256Digest(unitDigest)) {
				if err := s.retireEvidenceAttempt(ctx, *row.RecID); err != nil {
					return err
				}
				continue
			}
			if _, err := surrealdb.Query[any](ctx, s.db,
				`UPDATE $rid SET store_schema_version = $store_schema_version,
					evidence_migration_version = $evidence_migration_version
					WHERE store_schema_version IN $previous_schemas RETURN NONE`,
				map[string]any{
					"rid": *row.RecID,
					"previous_schemas": []string{
						evidencePreviousStoreSchemaVersion,
						evidenceLegacyUpgradableStoreSchemaVersion,
					},
					"store_schema_version":       evidenceStoreSchemaVersion,
					"evidence_migration_version": evidenceMigrationVersion,
				}); err != nil {
				return fmt.Errorf("stamp attempt %s: %w", row.RecID, err)
			}
		}
	}
}

// reshapeLegacyEvidenceAttempts migrates the pre-unit writer, which kept one row
// per repo/domain. Every compatible row therefore belongs to the explicit
// whole-repository unit; no current repository state participates in that
// classification. This is bound to its own generation literal, not to
// "previous", because the reshape describes that writer's shape and nothing else.
func (s *Surreal) reshapeLegacyEvidenceAttempts(ctx context.Context) error {
	for {
		results, err := surrealdb.Query[[]evidenceAttemptMigrationRec](ctx, s.db,
			`SELECT id, run_id, repo, commit, domain, extractor, status, started_at,
				store_schema_version, evidence_format_version
			FROM extraction_attempt
			WHERE store_schema_version = $legacy_schema
			ORDER BY id LIMIT $limit`, map[string]any{
				"legacy_schema": evidencePreUnitUpgradableStoreSchemaVersion,
				"limit":         evidenceMigrationBatchSize,
			})
		if err != nil {
			return err
		}
		var candidates []evidenceAttemptMigrationRec
		for _, result := range *results {
			candidates = append(candidates, result.Result...)
		}
		if len(candidates) == 0 {
			return nil
		}

		for _, row := range candidates {
			if row.RecID == nil {
				return errors.New("attempt has no physical record id")
			}
			runID, repo, commit, domain, extractor, status, fieldsOK :=
				evidenceAttemptMigrationFields(row)
			if !fieldsOK {
				if err := s.retireEvidenceAttempt(ctx, *row.RecID); err != nil {
					return err
				}
				continue
			}

			scope := ExtractionScope{
				Repository: repo,
				Commit:     commit,
				UnitDigest: "",
				Domain:     domain,
			}
			moved, err := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
				`BEGIN;
				LET $ready = array::len(SELECT id FROM $old_rid
					WHERE store_schema_version = $legacy_schema LIMIT 1) = 1
					AND array::len(SELECT id FROM $new_rid LIMIT 1) = 0;
				DELETE $old_rid WHERE $ready RETURN NONE;
				LET $created = IF $ready THEN
					(CREATE $new_rid SET run_id = $run_id, repo = $repo, commit = $commit,
						unit_digest = '', domain = $domain, extractor = $extractor,
						status = $status, started_at = $started_at,
						store_schema_version = $store_schema_version,
						evidence_format_version = $evidence_format_version,
						evidence_migration_version = $evidence_migration_version RETURN AFTER)
					ELSE [] END;
				RETURN $created;
				COMMIT;`, map[string]any{
					"old_rid": *row.RecID, "new_rid": extractionAttemptID(scope),
					"run_id": runID, "repo": repo, "commit": commit,
					"domain": domain, "extractor": extractor,
					"status": status, "started_at": row.StartedAt,
					"legacy_schema":              evidencePreUnitUpgradableStoreSchemaVersion,
					"store_schema_version":       evidenceStoreSchemaVersion,
					"evidence_format_version":    evidenceFormatVersion,
					"evidence_migration_version": evidenceMigrationVersion,
				})
			if err != nil {
				return fmt.Errorf("move attempt for %s: %w", repo, err)
			}
			progressed := false
			for _, result := range *moved {
				progressed = progressed || len(result.Result) == 1
			}
			if !progressed {
				// The whole-repository slot is already occupied — a current-shape
				// attempt for the same repository, commit, and domain outranks a
				// pre-unit diagnostic, so retire the legacy row instead of
				// failing Open. The retirement is itself guarded by both facts:
				// a source that changed after our read is never overwritten, and
				// a missing destination is not mislabeled as a collision.
				retired, retireErr := surrealdb.Query[[]extractionRunIdentityRec](
					ctx, s.db,
					`LET $retired = UPDATE $old_rid SET
						store_schema_version = $retired_schema,
						evidence_migration_version = $migration
						WHERE store_schema_version = $legacy_schema
							AND array::len(SELECT id FROM $new_rid LIMIT 1) = 1
						RETURN AFTER;
					RETURN $retired;`,
					map[string]any{
						"old_rid":        *row.RecID,
						"new_rid":        extractionAttemptID(scope),
						"retired_schema": retiredAttemptSchema,
						"migration":      evidenceMigrationVersion,
						"legacy_schema":  evidencePreUnitUpgradableStoreSchemaVersion,
					},
				)
				if retireErr != nil {
					return fmt.Errorf(
						"retire colliding attempt for %s: %w", repo, retireErr)
				}
				retiredRow := false
				for _, result := range *retired {
					retiredRow = retiredRow || len(result.Result) == 1
				}
				if retiredRow {
					continue
				}
				stillLegacy, checkErr := surrealdb.Query[[]struct {
					StoreSchema string `json:"store_schema_version"`
				}](ctx, s.db,
					`SELECT store_schema_version FROM $rid
						WHERE store_schema_version = $legacy_schema LIMIT 1`,
					map[string]any{
						"rid":           *row.RecID,
						"legacy_schema": evidencePreUnitUpgradableStoreSchemaVersion,
					},
				)
				if checkErr != nil {
					return fmt.Errorf(
						"check unchanged attempt for %s: %w", repo, checkErr)
				}
				for _, result := range *stillLegacy {
					if len(result.Result) > 0 {
						return fmt.Errorf(
							"move attempt for %s made no progress without a destination collision",
							repo,
						)
					}
				}
			}
		}
	}
}

func (s *Surreal) migrateEvidencePins(ctx context.Context, oldRunID, runID string) error {
	for {
		results, err := surrealdb.Query[[]evidencePinMigrationRec](ctx, s.db,
			`SELECT id, kind FROM evidence_pin WHERE run_id = $old_run_id
				ORDER BY id LIMIT $limit`, map[string]any{
				"old_run_id": oldRunID, "limit": evidenceMigrationBatchSize,
			})
		if err != nil {
			return err
		}
		var pins []evidencePinMigrationRec
		for _, result := range *results {
			pins = append(pins, result.Result...)
		}
		if len(pins) == 0 {
			return nil
		}

		canonical := make([]map[string]any, 0, len(pins))
		raw := make([]map[string]any, 0, len(pins))
		for _, pin := range pins {
			if pin.RecID == nil {
				return errors.New("pin has no physical record id")
			}
			kind, kindOK := migrationString(pin.Kind)
			if !kindOK || kind == "" || kind != strings.TrimSpace(kind) ||
				!utf8.ValidString(kind) || len(kind) > maxEvidenceIdentityBytes {
				raw = append(raw, map[string]any{"rid": *pin.RecID})
				continue
			}
			canonical = append(canonical, map[string]any{
				"old_rid": *pin.RecID, "new_rid": evidencePinRecordID(runID, kind),
				"pin_key": hashIdentity("pin_", runID, kind), "kind": kind,
			})
		}

		migrationResults, err := surrealdb.Query[any](ctx, s.db,
			`BEGIN;
			FOR $p IN $canonical {
				LET $old_created_at = (SELECT VALUE created_at FROM $p.old_rid LIMIT 1)[0];
				DELETE $p.old_rid RETURN NONE;
				UPSERT $p.new_rid SET pin_key = $p.pin_key, run_id = $run_id, kind = $p.kind,
					created_at = IF created_at = NONE THEN ($old_created_at ?? time::now())
						ELSE created_at END RETURN NONE;
			};
			FOR $p IN $raw {
				UPDATE $p.rid SET run_id = $run_id RETURN NONE;
			};
			COMMIT;`, map[string]any{
				"canonical": canonical, "raw": raw, "run_id": runID,
			})
		if err != nil {
			return err
		}
		for i, result := range *migrationResults {
			if result.Error != nil {
				return fmt.Errorf("pin batch statement %d: %s", i, result.Error.Message)
			}
		}
	}
}

func migrationString(value any) (string, bool) {
	valueString, ok := value.(string)
	return valueString, ok
}

func migrationBool(value any) (bool, bool) {
	valueBool, ok := value.(bool)
	return valueBool, ok
}

func evidenceMigrationStateID() models.RecordID {
	return models.NewRecordID("store_migration", "evidence_runs")
}

func (s *Surreal) evidenceMigrationComplete(ctx context.Context) (bool, error) {
	results, err := surrealdb.Query[[]evidenceMigrationStateRec](ctx, s.db,
		"SELECT id, version FROM $rid", map[string]any{"rid": evidenceMigrationStateID()})
	if err != nil {
		return false, err
	}
	for _, result := range *results {
		for _, row := range result.Result {
			if version, ok := migrationString(row.Version); ok && version == evidenceMigrationVersion {
				return true, nil
			}
		}
	}
	return false, nil
}

func evidenceMigrationPhysicalID(row evidenceRunMigrationRec) (models.RecordID, string, error) {
	if row.RecID == nil {
		return models.RecordID{}, "", errors.New("row has no physical record id")
	}
	id, ok := row.RecID.ID.(string)
	if !ok || strings.TrimSpace(id) == "" || !utf8.ValidString(id) || len(id) > maxEvidenceIdentityBytes {
		return models.RecordID{}, "", errors.New("row physical record id is not bounded UTF-8")
	}
	return *row.RecID, id, nil
}

func isLegacyEvidenceStoreSchema(schema string, present bool) bool {
	return !present || schema == "" ||
		slices.Contains(retiredEvidenceStoreSchemas, schema)
}

// evidenceWriterIsUpgradable reports whether a row's writer generation is one
// this binary migrates forward in place rather than quarantines.
func evidenceWriterIsUpgradable(schema string) bool {
	return schema == evidenceStoreSchemaVersion ||
		schema == evidencePreviousStoreSchemaVersion ||
		schema == evidenceLegacyUpgradableStoreSchemaVersion ||
		schema == evidencePreUnitUpgradableStoreSchemaVersion
}

// evidenceWriterCarriesUnitDigest reports whether a row's writer generation
// recorded a per-unit extraction scope. Generations before v8 predate focused
// units entirely, so their rows belong to the explicit whole-repository scope
// and an empty unit digest is the correct reading — not a malformed one.
func evidenceWriterCarriesUnitDigest(schema string) bool {
	return schema == evidenceStoreSchemaVersion ||
		schema == evidencePreviousStoreSchemaVersion ||
		schema == evidenceLegacyUpgradableStoreSchemaVersion
}

func validEvidenceRunStatus(status string) bool {
	return status == "staged" || status == "published" || status == "superseded" ||
		status == "aborted" || status == "deleting"
}

// migrateEvidenceRuns upgrades only formats this binary understands. Rows
// from the retracted implementation and from skipped intermediate writer
// generations (v3–v5, whose per-generation upgrade passes never ran on this
// store) are retired and quarantined — a stranded published row would
// otherwise hold its unique published_key forever and block every replacement
// publication. The immediately preceding compatible writer is upgraded in
// place. Unknown future writer/format pairs are deliberately neither decoded
// nor mutated.
//
// The physical record id is authoritative. Per-row migration markers make a
// crash resume monotonically; the global marker is written only after the
// bounded candidate scan is empty, so steady-state Open never scans run rows.
func (s *Surreal) migrateEvidenceRuns(ctx context.Context) error {
	complete, err := s.evidenceMigrationComplete(ctx)
	if err != nil {
		return fmt.Errorf("migrate evidence runs: read completion marker: %w", err)
	}
	if complete {
		return nil
	}

	for {
		results, err := surrealdb.Query[[]evidenceRunMigrationRec](ctx, s.db,
			`SELECT id, run_id, repo, commit, unit_digest, domain, status, store_schema_version,
				evidence_format_version, evidence_migration_ambiguous_run_id,
				retention_quarantined, retention_phase
			FROM extraction_run
			WHERE store_schema_version = NONE
			   OR NOT (type::is_string(store_schema_version))
			   OR store_schema_version = ''
			   OR store_schema_version IN $legacy_schemas
			   OR (store_schema_version IN $upgradable_predecessors
				   AND (evidence_format_version = NONE
					OR NOT (type::is_string(evidence_format_version))
					OR evidence_format_version = ''
					OR evidence_format_version = $format))
			   OR (store_schema_version = $schema
				   AND (evidence_format_version = NONE
					OR NOT (type::is_string(evidence_format_version))
					OR evidence_format_version = ''
					OR (evidence_format_version = $format AND (
						evidence_migration_version = NONE
						OR evidence_migration_version != $migration
						OR run_id = NONE OR NOT (type::is_string(run_id))
						OR run_id = '' OR run_id != record::id(id)
						OR retention_quarantined = NONE
						OR retention_quarantined NOT IN [true, false]
						OR unit_digest = NONE
						OR NOT (type::is_string(unit_digest))
						OR status = NONE OR NOT (type::is_string(status))
							OR status NOT IN ['staged', 'published', 'superseded', 'aborted', 'deleting']
							OR (status = 'deleting' AND (
								retention_phase = NONE
								OR NOT (type::is_string(retention_phase))
								OR retention_phase NOT IN ['associations', 'assertions', 'chunks', 'finalize']))
							OR (status != 'deleting' AND retention_phase != NONE)
							OR (status = 'published' AND published_key = NONE)
							OR (status != 'published' AND published_key != NONE)
							OR (retention_quarantined = true AND published_key != NONE)
							OR (evidence_migration_ambiguous_run_id != NONE AND (
								NOT (type::is_string(evidence_migration_ambiguous_run_id))
								OR evidence_migration_ambiguous_run_id = ''
								OR retention_quarantined != true))
						))))
			ORDER BY id LIMIT $limit`, map[string]any{
				"limit": evidenceMigrationBatchSize, "schema": evidenceStoreSchemaVersion,
				"upgradable_predecessors": []string{
					evidencePreviousStoreSchemaVersion,
					evidenceLegacyUpgradableStoreSchemaVersion,
					evidencePreUnitUpgradableStoreSchemaVersion,
				},
				"legacy_schemas": retiredEvidenceStoreSchemas,
				"format":         evidenceFormatVersion,
				"migration":      evidenceMigrationVersion,
			})
		if err != nil {
			return fmt.Errorf("migrate evidence runs: list: %w", err)
		}
		var candidates []evidenceRunMigrationRec
		for _, result := range *results {
			candidates = append(candidates, result.Result...)
		}
		if len(candidates) == 0 {
			if err := s.migrateEvidenceAttempts(ctx); err != nil {
				return fmt.Errorf("migrate evidence runs: attempts: %w", err)
			}
			if _, err := surrealdb.Query[any](ctx, s.db,
				`UPSERT $rid SET version = $version, completed_at = time::now() RETURN NONE`,
				map[string]any{"rid": evidenceMigrationStateID(), "version": evidenceMigrationVersion}); err != nil {
				return fmt.Errorf("migrate evidence runs: write completion marker: %w", err)
			}
			return nil
		}

		for _, row := range candidates {
			rid, runID, idErr := evidenceMigrationPhysicalID(row)
			if idErr != nil {
				return fmt.Errorf("migrate evidence runs: %w", idErr)
			}
			schema, schemaPresent := migrationString(row.StoreSchema)
			legacy := isLegacyEvidenceStoreSchema(schema, schemaPresent)
			if !legacy && !evidenceWriterIsUpgradable(schema) {
				return fmt.Errorf("migrate evidence run %s: candidate has unknown writer schema", runID)
			}

			oldRunID, oldRunIDPresent := migrationString(row.RunID)
			oldRunIDUsable := oldRunIDPresent && oldRunID != "" && utf8.ValidString(oldRunID) &&
				len(oldRunID) <= maxEvidenceIdentityBytes
			rewriteRunID := oldRunIDUsable && oldRunID != runID

			format, formatPresent := migrationString(row.Format)
			knownWriter := evidenceWriterIsUpgradable(schema)
			malformedFormat := knownWriter && ((formatPresent && format == "") ||
				(!formatPresent && row.Format != nil))
			// The unit digest must be read for every generation that recorded
			// one, not only the current writer: reading it only at the current
			// generation would recompute a predecessor's published_key against
			// an empty unit and collide its focused publication with the
			// whole-repository slot for the same repository, commit, and domain.
			unitDigest := ""
			if evidenceWriterCarriesUnitDigest(schema) {
				var unitOK bool
				unitDigest, unitOK = migrationString(row.UnitDigest)
				if !unitOK || (unitDigest != "" && !validSHA256Digest(unitDigest)) {
					malformedFormat = true
					unitDigest = ""
				}
			}
			status, statusPresent := migrationString(row.Status)
			retentionPhase, retentionPhasePresent := migrationString(row.RetentionPhase)
			quarantined := legacy || malformedFormat
			ambiguousMarkerPresent := row.AmbiguousRunID != nil
			ambiguousRunID, hasAmbiguousRunID := migrationString(row.AmbiguousRunID)
			hasAmbiguousRunID = hasAmbiguousRunID && ambiguousRunID != "" &&
				utf8.ValidString(ambiguousRunID) && len(ambiguousRunID) <= maxEvidenceIdentityBytes
			quarantined = quarantined || ambiguousMarkerPresent
			if !legacy {
				if existing, ok := migrationBool(row.RetentionQuarantined); ok {
					quarantined = quarantined || existing
				} else if row.RetentionQuarantined != nil {
					quarantined = true
				}
			}

			if legacy {
				switch status {
				case "published":
					status = "superseded"
				case "superseded":
					status = "superseded"
				case "staged", "aborted":
					status = "aborted"
				default:
					status = "aborted"
				}
			} else if schema != evidenceStoreSchemaVersion && status == "deleting" {
				// Restart predecessor retention from the durable owner. Proof-row
				// deletes are idempotent, and the new ledger phase then drains zero
				// rows for a pre-accounting generation.
				status = "aborted"
				retentionPhase = ""
			} else if !statusPresent || !validEvidenceRunStatus(status) ||
				(status == "deleting" && (schema != evidenceStoreSchemaVersion ||
					!retentionPhasePresent ||
					(retentionPhase != "associations" && retentionPhase != "assertions" &&
						retentionPhase != "chunks" && retentionPhase != "finalize"))) {
				status = "aborted"
				retentionPhase = ""
				quarantined = true
			} else if schema != evidenceStoreSchemaVersion && status == "staged" {
				// Pre-T40.7 staged rows have neither a trusted chunk identity nor
				// reconstructible fact charges. Preserve visible terminal history,
				// but retire incomplete work for a clean exact-generation retry.
				status = "aborted"
				retentionPhase = ""
			} else if status != "deleting" && row.RetentionPhase != nil {
				retentionPhase = ""
				quarantined = true
			}

			ambiguousOwnership := false
			if rewriteRunID && !quarantined {
				owners, ownerErr := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
					`SELECT id FROM extraction_run
						WHERE id != $rid AND (
							id = $old_rid OR run_id = $old_run_id
							OR evidence_migration_ambiguous_run_id = $old_run_id)
						LIMIT 1`, map[string]any{
						"rid": rid, "old_rid": extractionRunID(oldRunID), "old_run_id": oldRunID,
					})
				if ownerErr != nil {
					return fmt.Errorf("migrate evidence run %s: ownership lookup: %w", runID, ownerErr)
				}
				for _, result := range *owners {
					ambiguousOwnership = ambiguousOwnership || len(result.Result) > 0
				}
				if ambiguousOwnership {
					rewriteRunID = false
					quarantined = true
					ambiguousRunID = oldRunID
					hasAmbiguousRunID = true
				}
			}

			// A valid old string can be repaired transactionally across all proof
			// rows only when no other physical/claiming owner exists. Missing,
			// malformed, ambiguous, or staged identities are retired rather than
			// reopened for writes.
			if !legacy && oldRunID != runID && (!oldRunIDUsable || status == "staged") {
				quarantined = true
			}
			key := ""
			if status == "published" && !quarantined {
				repo, repoOK := migrationString(row.Repo)
				commit, commitOK := migrationString(row.Commit)
				domain, domainOK := migrationString(row.Domain)
				if !repoOK || !commitOK || !domainOK ||
					repo == "" || commit == "" || domain == "" ||
					!utf8.ValidString(repo) || !utf8.ValidString(commit) || !utf8.ValidString(domain) ||
					len(repo) > maxEvidenceIdentityBytes ||
					len(commit) > maxEvidenceIdentityBytes ||
					len(domain) > maxEvidenceIdentityBytes {
					status = "superseded"
					quarantined = true
				} else {
					canonicalKey := publishedKey(ExtractionScope{
						Repository: repo,
						Commit:     commit,
						UnitDigest: unitDigest,
						Domain:     domain,
					})
					owners, lookupErr := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
						`SELECT id FROM extraction_run
							WHERE published_key = $published_key AND id != $rid LIMIT 1`, map[string]any{
							"rid": rid, "published_key": canonicalKey,
						})
					if lookupErr != nil {
						return fmt.Errorf("migrate evidence run %s: publication lookup: %w", runID, lookupErr)
					}
					ownerExists := false
					for _, result := range *owners {
						ownerExists = ownerExists || len(result.Result) > 0
					}
					if ownerExists {
						status = "superseded"
					} else {
						key = canonicalKey
					}
				}
			}
			if quarantined {
				switch status {
				case "published":
					status = "superseded"
				case "staged":
					status = "aborted"
				}
				// Pre-bound or otherwise untrusted rows retain their original
				// child/pin logical ids for audit. Only bounded compatible repairs
				// are eligible to relink proof rows.
				rewriteRunID = false
			}
			// Canonicalizing a terminal claimant while intentionally retaining its
			// children under a usable old logical id must reserve that id. Without
			// the marker, a canonical physical owner could read or sweep the
			// indistinguishable proof after this row stops directly claiming it.
			if oldRunIDUsable && oldRunID != runID && !rewriteRunID {
				if hasAmbiguousRunID && ambiguousRunID != oldRunID {
					return fmt.Errorf("migrate evidence run %s: conflicting ambiguity markers %q and %q",
						runID, ambiguousRunID, oldRunID)
				}
				ambiguousRunID = oldRunID
				hasAmbiguousRunID = true
			}

			if rewriteRunID {
				pinErr := s.migrateEvidencePins(ctx, oldRunID, runID)
				if pinErr != nil {
					return fmt.Errorf("migrate evidence run %s: pins: %w", runID, pinErr)
				}
			}

			vars := map[string]any{
				"rid": rid, "run_id": runID, "old_run_id": oldRunID,
				"rewrite_run_id": rewriteRunID, "status": status,
				"store_schema_version":       evidenceStoreSchemaVersion,
				"evidence_format_version":    evidenceFormatVersion,
				"evidence_migration_version": evidenceMigrationVersion,
				"unit_digest":                unitDigest,
				"retention_quarantined":      quarantined,
				"has_published_key":          key != "", "published_key": key,
				"has_ambiguous_run_id": hasAmbiguousRunID,
				"ambiguous_run_id":     ambiguousRunID,
				"has_retention_phase":  status == "deleting" && retentionPhase != "",
				"retention_phase":      retentionPhase,
			}
			updated, updateErr := surrealdb.Query[[]evidenceMigrationStateRec](ctx, s.db,
				`BEGIN;
				LET $updated = UPDATE $rid SET run_id = $run_id, status = $status,
					unit_digest = $unit_digest,
					staged_fact_count = 0,
					staged_row_count = 0,
					staged_reference_count = 0,
					staged_chunk_count = 0,
					store_schema_version = $store_schema_version,
					evidence_format_version = $evidence_format_version,
					evidence_migration_version = $evidence_migration_version,
					retention_quarantined = $retention_quarantined,
					evidence_migration_ambiguous_run_id = IF $has_ambiguous_run_id
						THEN $ambiguous_run_id ELSE NONE END,
					retention_phase = IF $has_retention_phase
						THEN $retention_phase ELSE NONE END,
					published_key = IF $has_published_key THEN $published_key ELSE NONE END
					RETURN AFTER;
				UPDATE snapshot_evidence SET run_id = $run_id
					WHERE $rewrite_run_id AND run_id = $old_run_id RETURN NONE;
				UPDATE assertion SET run_id = $run_id
					WHERE $rewrite_run_id AND run_id = $old_run_id RETURN NONE;
				UPDATE evidence_chunk SET run_id = $run_id
					WHERE $rewrite_run_id AND run_id = $old_run_id RETURN NONE;
				RETURN $updated;
				COMMIT;`, vars)
			if updateErr != nil {
				return fmt.Errorf("migrate evidence run %s: %w", runID, updateErr)
			}
			progressed := false
			for _, result := range *updated {
				progressed = progressed || len(result.Result) == 1
			}
			if !progressed {
				return fmt.Errorf("migrate evidence run %s: physical record update made no progress", runID)
			}
		}
	}
}

func (s *Surreal) updateLegacyJob(ctx context.Context, id, set string, expected JobStatus, pairs ...any) error {
	vars := map[string]any{"id": id, "expected": string(expected)}
	for i := 0; i < len(pairs); i += 2 {
		vars[pairs[i].(string)] = pairs[i+1]
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		"UPDATE type::record($id) SET "+set+" WHERE status = $expected RETURN AFTER", vars)
	if err != nil {
		return err
	}
	if len(firstNonEmpty(results)) == 0 {
		return fmt.Errorf("job %q changed during migration", id)
	}
	return nil
}

func (s *Surreal) Close(ctx context.Context) error {
	err := s.db.Close(ctx)
	if s.stop != nil {
		s.stop()
	}
	return err
}

// --- repos ---

// repoID builds the record id repo:⟨name⟩; passing it as a typed param
// sidesteps escaping of names like "github.com/foo/bar".
func repoID(name string) models.RecordID { return models.NewRecordID("repo", name) }

func (s *Surreal) UpsertRepo(ctx context.Context, r Repo) error {
	_, err := surrealdb.Query[any](ctx, s.db,
		`UPSERT $rid SET
			name = $name,
			display_name = $display_name,
			clone_url = $clone_url,
			web_url = $web_url,
			default_branch = $default_branch,
			is_fork = $is_fork,
			is_archived = $is_archived,
			is_public = $is_public,
			metadata = $metadata,
			pushed_at = $pushed_at,
			external_id = $external_id,
			external_code_host_type = $external_host_type,
			external_code_host_url = $external_host_url`,
		map[string]any{
			"rid":                repoID(r.Name),
			"name":               r.Name,
			"display_name":       r.DisplayName,
			"clone_url":          r.CloneURL,
			"web_url":            r.WebURL,
			"default_branch":     r.DefaultBranch,
			"is_fork":            r.IsFork,
			"is_archived":        r.IsArchived,
			"is_public":          r.IsPublic,
			"metadata":           r.Metadata,
			"pushed_at":          r.PushedAt,
			"external_id":        r.ExternalID,
			"external_host_type": r.ExternalHostType,
			"external_host_url":  r.ExternalHostURL,
		})
	return err
}

func (s *Surreal) GetRepo(ctx context.Context, name string) (*Repo, error) {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		"SELECT * FROM $rid",
		map[string]any{"rid": repoID(name)})
	if err != nil {
		return nil, err
	}
	rows := (*results)[0].Result
	if len(rows) == 0 {
		return nil, fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	if err := validateRepoAnalysisUnit(&rows[0]); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

func (s *Surreal) ListRepos(ctx context.Context) ([]Repo, error) {
	results, err := surrealdb.Query[[]Repo](ctx, s.db, "SELECT * FROM repo ORDER BY name", nil)
	if err != nil {
		return nil, err
	}
	repositories := (*results)[0].Result
	for index := range repositories {
		if err := validateRepoAnalysisUnit(&repositories[index]); err != nil {
			return nil, err
		}
	}
	return repositories, nil
}

func validateRepoAnalysisUnit(repository *Repo) error {
	if repository == nil {
		return errors.New("repository row is nil")
	}
	if err := repository.IndexedAnalysisUnit.Validate(repository.Name); err != nil {
		return fmt.Errorf(
			"repo %q: invalid committed analysis unit: %w",
			repository.Name, err,
		)
	}
	return nil
}

func (s *Surreal) DeleteRepo(ctx context.Context, name string) error {
	// The caller has already destroyed disk artifacts, so an unretried
	// optimistic-transaction conflict would roll the repo back to live while
	// its mirror and shards are gone. Retry like every other multi-statement
	// writer in this store.
	for attempt := 0; ; attempt++ {
		_, err := surrealdb.Query[any](ctx, s.db,
			`BEGIN;
UPDATE extraction_run SET status = 'superseded', published_key = NONE
    WHERE repo = $name AND status = 'published' RETURN NONE;
UPDATE extraction_run SET status = 'aborted', published_key = NONE
    WHERE repo = $name AND status = 'staged' RETURN NONE;
DELETE extraction_attempt WHERE repo = $name RETURN NONE;
DELETE extraction_domain_outcome WHERE repo = $name RETURN NONE;
UPDATE extraction_job SET status = 'canceled', error = 'repository deleting',
    finished_at = time::now(), not_before = NONE, pending_key = NONE
    WHERE target = $name AND status = 'pending' RETURN NONE;
UPDATE candidate_manifest_job SET status = 'canceled', error = 'repository deleting',
    finished_at = time::now(), not_before = NONE, pending_key = NONE
    WHERE target = $name AND status = 'pending' RETURN NONE;
UPDATE resolver_catalog_job SET status = 'canceled', error = 'repository deleting',
    finished_at = time::now(), not_before = NONE, pending_key = NONE
    WHERE target = $name AND status = 'pending' RETURN NONE;
UPDATE caller_leaf_job SET status = 'canceled', error = 'repository deleting',
    finished_at = time::now(), not_before = NONE, pending_key = NONE
    WHERE target = $name AND status = 'pending' RETURN NONE;
UPDATE generation_schedule SET status = 'superseded', updated_at = time::now()
    WHERE repository = $name AND status = 'active'
        AND stage IN [$state_reconcile_stage, $state_activate_stage,
            $relationship_v3_stage] RETURN NONE;
UPDATE service_state_v3_plan SET state = 'superseded', updated_at = time::now()
    WHERE repository = $name AND state = 'running'
        AND phase IN ['reconcile', 'activate'] RETURN NONE;
DELETE generation_schedule_current WHERE repository = $name
    AND stage IN [$state_reconcile_stage, $state_activate_stage,
        $relationship_v3_stage] RETURN NONE;
DELETE candidate_manifest_publication WHERE repository = $name RETURN NONE;
DELETE resolver_catalog_publication WHERE repository = $name RETURN NONE;
DELETE caller_generation_publication WHERE repository = $name RETURN NONE;
DELETE caller_leaf_outcome WHERE repository = $name RETURN NONE;
DELETE caller_generation_admission WHERE repository = $name RETURN NONE;
DELETE repo_permission WHERE repo = $name RETURN NONE;
DELETE repo_connection WHERE repo = $name RETURN NONE;
DELETE $rid RETURN NONE;
COMMIT;`, map[string]any{
				"rid": repoID(name), "name": name,
				"state_reconcile_stage": ServiceStateV3ReconcileStage,
				"state_activate_stage":  ServiceStateV3ActivateStage,
				"relationship_v3_stage": ServiceRelationshipV3ScheduleStage,
			})
		if err != nil && isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
			continue
		}
		return err
	}
}

// SetRepoDeleting retires complete caller authority when deletion begins. A
// true-to-false reactivation atomically coalesces a forced caller successor so
// a failed cleanup cannot make the repository live between state and queue
// commits; an already-active false-to-false refresh remains queue-neutral.
func (s *Surreal) SetRepoDeleting(ctx context.Context, name string, deleting bool) error {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		`BEGIN;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
IF $caller_writer_ok = false {
	THROW 'phebs-permanent: caller-generation publication writer is not active'
};
LET $before = (SELECT deleting FROM $rid)[0];
LET $updated = UPDATE $rid SET deleting = $deleting RETURN AFTER;
LET $retired_caller = IF $deleting AND array::len($updated) = 1 THEN
	(DELETE caller_generation_publication
		WHERE repository = $repository RETURN BEFORE)
	ELSE [] END;
LET $caller_revision = IF array::len($retired_caller) = 1 THEN
	(UPDATE $rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN AFTER)
	ELSE [] END;
LET $final = IF array::len($retired_caller) = 1 THEN
	$caller_revision ELSE $updated END;
LET $reactivated = array::len($updated) = 1 AND $deleting = false
	AND ($before.deleting ?? false) = true;
LET $pending_caller = IF $reactivated THEN
	(SELECT id, created_at FROM caller_leaf_job
		WHERE pending_key = $repository AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $caller_fanout = IF $reactivated = false THEN []
	ELSE IF $pending_caller != NONE THEN
		(UPDATE $pending_caller SET force = true,
			recovery_lease = NONE RETURN AFTER)
	ELSE
		(CREATE caller_leaf_job CONTENT {
			target: $repository,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $repository,
			force: true
		} RETURN AFTER)
	END;
LET $caller_projected = IF $reactivated AND array::len($caller_fanout) = 1
	THEN $caller_fanout[0] ELSE NONE END;`+projectCallerJobSQL+`
RETURN IF array::len($final) = 1
	AND (array::len($retired_caller) = 0
		OR array::len($caller_revision) = 1)
	AND ($reactivated = false OR array::len($caller_fanout) = 1)
	THEN $final ELSE [] END;
COMMIT;`,
		map[string]any{
			"rid": repoID(name), "repository": name, "deleting": deleting,
			"caller_migration_rid":     callerGenerationPublicationMigrationID(),
			"caller_migration_version": callerGenerationPublicationMigrationVersion,
		})
	if err != nil {
		return err
	}
	if len(firstDomainRows(results)) == 0 {
		return fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Surreal) SetRepoIndexed(ctx context.Context, name, commitHash string, at time.Time) error {
	return s.SetRepoIndexedRevisions(ctx, name, commitHash, []IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commitHash,
	}}, at)
}

func (s *Surreal) SetRepoIndexedRevisions(ctx context.Context, name, defaultCommit string, revisions []IndexedRevision, at time.Time) error {
	return s.SetRepoIndexedState(ctx, name, defaultCommit, revisions, nil, at)
}

func (s *Surreal) SetRepoIndexedState(
	ctx context.Context,
	name,
	defaultCommit string,
	revisions []IndexedRevision,
	unit *analysisunit.State,
	at time.Time,
) error {
	if err := unit.Validate(name); err != nil {
		return fmt.Errorf("repo %q: analysis unit: %w", name, err)
	}
	statement := `BEGIN;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
IF $caller_writer_ok = false {
	THROW 'phebs-permanent: caller-generation publication writer is not active'
};
LET $before = (SELECT indexed_commit_hash, indexed_analysis_unit FROM $rid)[0];
LET $updated = UPDATE $rid SET indexed_commit_hash = $hash,
	indexed_revisions = $revisions, indexed_analysis_unit = NONE,
	indexed_at = $at, latest_indexing_job_status = 'done' RETURN AFTER;
LET $scope_unchanged = ($before.indexed_commit_hash ?? '') = $hash
	AND ($before.indexed_analysis_unit.digest ?? '') = $unit_digest;
LET $same_scope_state_changed = array::len($updated) = 1
	AND $scope_unchanged
	AND $before.indexed_analysis_unit != NONE;
LET $identity_changed = array::len($updated) = 1
	AND ($scope_unchanged = false OR $same_scope_state_changed);
LET $retired_catalog = IF $identity_changed THEN
	(DELETE resolver_catalog_publication
		WHERE repository = $name RETURN BEFORE)
	ELSE [] END;
LET $retired_caller = IF $identity_changed THEN
	(DELETE caller_generation_publication
		WHERE repository = $name RETURN BEFORE)
	ELSE [] END;
IF $identity_changed {
	DELETE $publication_rid RETURN NONE
};
IF $same_scope_state_changed {
	UPDATE extraction_run SET status = 'superseded', published_key = NONE
		WHERE repo = $name AND commit = $hash AND unit_digest = $unit_digest
			AND status = 'published'
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND ` + evidenceRunHasNoAmbiguousClaimantSQL + ` RETURN NONE;
	UPDATE extraction_run SET status = 'aborted', published_key = NONE
		WHERE repo = $name AND commit = $hash AND unit_digest = $unit_digest
			AND status = 'staged'
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND ` + evidenceRunHasNoAmbiguousClaimantSQL + ` RETURN NONE;
	DELETE extraction_attempt
		WHERE repo = $name AND commit = $hash AND unit_digest = $unit_digest
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND evidence_migration_version = $evidence_migration
		RETURN NONE
	;
	DELETE extraction_domain_outcome
		WHERE repo = $name RETURN NONE
};
LET $final = IF $identity_changed THEN
	(UPDATE $rid SET evidence_revision = (evidence_revision ?? 0) + 1
		RETURN AFTER)
	ELSE $updated END;
LET $caller_revision = IF array::len($retired_caller) = 1 THEN
	(UPDATE $rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN AFTER)
	ELSE [] END;
LET $pending_catalog = IF array::len($retired_catalog) = 1 THEN
	(SELECT id, created_at FROM resolver_catalog_job
		WHERE pending_key = $name AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $catalog_fanout = IF array::len($retired_catalog) != 1 THEN []
	ELSE IF $pending_catalog != NONE THEN
		(UPDATE $pending_catalog SET force = true,
			recovery_lease = NONE RETURN AFTER)
	ELSE
		(CREATE resolver_catalog_job CONTENT {
			target: $name,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $name,
			force: true
		} RETURN AFTER)
	END;` + projectResolverJobSQL + `
RETURN IF array::len($final) = 1
	AND (array::len($retired_catalog) = 0
		OR array::len($catalog_fanout) = 1)
	AND (array::len($retired_caller) = 0
		OR array::len($caller_revision) = 1)
	THEN $final ELSE [] END;
COMMIT;`
	vars := map[string]any{
		"rid": repoID(name), "name": name, "hash": defaultCommit,
		"revisions": revisions, "at": at, "unit_digest": "",
		"publication_rid":             candidateManifestPublicationID(name),
		"evidence_store_schema":       evidenceStoreSchemaVersion,
		"evidence_format":             evidenceFormatVersion,
		"evidence_migration":          evidenceMigrationVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
		"caller_migration_rid":        callerGenerationPublicationMigrationID(),
		"caller_migration_version":    callerGenerationPublicationMigrationVersion,
	}
	if unit != nil {
		statement = `BEGIN;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
IF $caller_writer_ok = false {
	THROW 'phebs-permanent: caller-generation publication writer is not active'
};
LET $before = (SELECT indexed_commit_hash, indexed_analysis_unit FROM $rid)[0];
LET $updated = UPDATE $rid SET indexed_commit_hash = $hash,
	indexed_revisions = $revisions, indexed_analysis_unit = $unit,
	indexed_at = $at, latest_indexing_job_status = 'done' RETURN AFTER;
LET $scope_unchanged = ($before.indexed_commit_hash ?? '') = $hash
	AND ($before.indexed_analysis_unit.digest ?? '') = $unit_digest;
LET $same_scope_state_changed = array::len($updated) = 1
	AND $scope_unchanged
	AND $before.indexed_analysis_unit != $unit;
LET $identity_changed = array::len($updated) = 1
	AND ($scope_unchanged = false OR $same_scope_state_changed);
LET $retired_catalog = IF $identity_changed THEN
	(DELETE resolver_catalog_publication
		WHERE repository = $name RETURN BEFORE)
	ELSE [] END;
LET $retired_caller = IF $identity_changed THEN
	(DELETE caller_generation_publication
		WHERE repository = $name RETURN BEFORE)
	ELSE [] END;
IF $identity_changed {
	DELETE $publication_rid RETURN NONE
};
IF $same_scope_state_changed {
	UPDATE extraction_run SET status = 'superseded', published_key = NONE
		WHERE repo = $name AND commit = $hash AND unit_digest = $unit_digest
			AND status = 'published'
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND ` + evidenceRunHasNoAmbiguousClaimantSQL + ` RETURN NONE;
	UPDATE extraction_run SET status = 'aborted', published_key = NONE
		WHERE repo = $name AND commit = $hash AND unit_digest = $unit_digest
			AND status = 'staged'
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND ` + evidenceRunHasNoAmbiguousClaimantSQL + ` RETURN NONE;
	DELETE extraction_attempt
		WHERE repo = $name AND commit = $hash AND unit_digest = $unit_digest
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND evidence_migration_version = $evidence_migration
		RETURN NONE
	;
	DELETE extraction_domain_outcome
		WHERE repo = $name RETURN NONE
};
LET $final = IF $identity_changed THEN
	(UPDATE $rid SET evidence_revision = (evidence_revision ?? 0) + 1
		RETURN AFTER)
	ELSE $updated END;
LET $caller_revision = IF array::len($retired_caller) = 1 THEN
	(UPDATE $rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN AFTER)
	ELSE [] END;
LET $pending_catalog = IF array::len($retired_catalog) = 1 THEN
	(SELECT id, created_at FROM resolver_catalog_job
		WHERE pending_key = $name AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $catalog_fanout = IF array::len($retired_catalog) != 1 THEN []
	ELSE IF $pending_catalog != NONE THEN
		(UPDATE $pending_catalog SET force = true,
			recovery_lease = NONE RETURN AFTER)
	ELSE
		(CREATE resolver_catalog_job CONTENT {
			target: $name,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $name,
			force: true
		} RETURN AFTER)
	END;` + projectResolverJobSQL + `
RETURN IF array::len($final) = 1
	AND (array::len($retired_catalog) = 0
		OR array::len($catalog_fanout) = 1)
	AND (array::len($retired_caller) = 0
		OR array::len($caller_revision) = 1)
	THEN $final ELSE [] END;
COMMIT;`
		vars["unit"] = analysisunit.CloneState(unit)
		vars["unit_digest"] = unit.Digest
	}
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		statement, vars)
	if err != nil {
		return err
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	if err := validateRepoAnalysisUnit(&rows[0]); err != nil {
		return err
	}
	return nil
}

func (s *Surreal) ClearRepoIndexState(ctx context.Context, name string) error {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		`BEGIN;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
IF $caller_writer_ok = false {
	THROW 'phebs-permanent: caller-generation publication writer is not active'
};
LET $before = (SELECT indexed_commit_hash, indexed_analysis_unit FROM $rid)[0];
LET $publication = (SELECT id FROM $publication_rid)[0];
LET $catalog = (SELECT id FROM $catalog_rid)[0];
LET $caller = (SELECT id FROM $caller_rid)[0];
LET $visibility_changed = ($before != NONE
	AND (($before.indexed_commit_hash ?? '') != ''
		OR $before.indexed_analysis_unit != NONE))
	OR $publication != NONE OR $catalog != NONE OR $caller != NONE;
LET $updated = UPDATE $rid SET indexed_commit_hash = NONE, indexed_revisions = NONE,
	indexed_analysis_unit = NONE, indexed_at = NONE RETURN AFTER;
LET $retired_catalog = IF array::len($updated) = 1 THEN
	(DELETE resolver_catalog_publication
		WHERE repository = $name RETURN BEFORE)
	ELSE [] END;
LET $retired_caller = IF array::len($updated) = 1 THEN
	(DELETE caller_generation_publication
		WHERE repository = $name RETURN BEFORE)
	ELSE [] END;
IF array::len($updated) = 1 {
	DELETE $publication_rid RETURN NONE;
	DELETE extraction_domain_outcome WHERE repo = $name RETURN NONE
};
LET $final = IF array::len($updated) = 1 AND $visibility_changed THEN
	(UPDATE $rid SET evidence_revision = (evidence_revision ?? 0) + 1
		RETURN AFTER)
	ELSE $updated END;
LET $caller_revision = IF array::len($retired_caller) = 1 THEN
	(UPDATE $rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN AFTER)
	ELSE [] END;
LET $pending_catalog = IF array::len($retired_catalog) = 1 THEN
	(SELECT id, created_at FROM resolver_catalog_job
		WHERE pending_key = $name AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $catalog_fanout = IF array::len($retired_catalog) != 1 THEN []
	ELSE IF $pending_catalog != NONE THEN
		(UPDATE $pending_catalog SET force = true,
			recovery_lease = NONE RETURN AFTER)
	ELSE
		(CREATE resolver_catalog_job CONTENT {
			target: $name,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $name,
			force: true
		} RETURN AFTER)
	END;`+projectResolverJobSQL+`
RETURN IF array::len($final) = 1
	AND (array::len($retired_catalog) = 0
		OR array::len($catalog_fanout) = 1)
	AND (array::len($retired_caller) = 0
		OR array::len($caller_revision) = 1)
	THEN $final ELSE [] END;
COMMIT;`,
		map[string]any{
			"rid":                      repoID(name),
			"publication_rid":          candidateManifestPublicationID(name),
			"catalog_rid":              resolverCatalogPublicationID(name),
			"caller_rid":               callerGenerationPublicationID(name),
			"name":                     name,
			"caller_migration_rid":     callerGenerationPublicationMigrationID(),
			"caller_migration_version": callerGenerationPublicationMigrationVersion,
		})
	if err != nil {
		return err
	}
	if len(firstDomainRows(results)) == 0 {
		return fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	return nil
}

// --- connection membership + status ---

func (s *Surreal) SetRepoConnections(ctx context.Context, conn string, repos []string) error {
	_, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
DELETE repo_connection WHERE connection = $conn;
FOR $r IN $repos { CREATE repo_connection CONTENT { connection: $conn, repo: $r } };
COMMIT;`,
		map[string]any{"conn": conn, "repos": repos})
	return err
}

func (s *Surreal) PruneConnections(ctx context.Context, keep []string) error {
	_, err := surrealdb.Query[any](ctx, s.db,
		"DELETE repo_connection WHERE connection NOT IN $keep",
		map[string]any{"keep": keep})
	return err
}

func (s *Surreal) GetRepoConnections(ctx context.Context, repo string) ([]string, error) {
	memb, err := surrealdb.Query[[]struct {
		Connection string `json:"connection"`
	}](ctx, s.db, "SELECT connection FROM repo_connection WHERE repo = $repo",
		map[string]any{"repo": repo})
	if err != nil {
		return nil, err
	}
	var conns []string
	for _, m := range (*memb)[0].Result {
		conns = append(conns, m.Connection)
	}
	return conns, nil
}

const indexingJobProjectionVersion = "t30.6n-indexing-job-latest-v1"
const extractionJobProjectionVersion = "t40r1-extraction-job-latest-v1"
const callerJobProjectionVersion = "t40r1-caller-job-latest-v1"
const resolverJobProjectionVersion = "t40r1-resolver-job-latest-v1"

type repoStatusRec struct {
	Repo
	ProjectedJob                         *jobRec `json:"projected_job"`
	LatestIndexJobProjectionVersion      string  `json:"latest_index_job_projection_version"`
	ProjectedExtractionJob               *jobRec `json:"projected_extraction_job"`
	LatestExtractionJobProjectionVersion string  `json:"latest_extraction_job_projection_version"`
	ProjectedCallerJob                   *jobRec `json:"projected_caller_job"`
	LatestCallerJobProjectionVersion     string  `json:"latest_caller_job_projection_version"`
	ProjectedResolverJob                 *jobRec `json:"projected_resolver_job"`
	LatestResolverJobProjectionVersion   string  `json:"latest_resolver_job_projection_version"`
}

var _ CallerJobProjectionStore = (*Surreal)(nil)

func callerJobProjection(row repoStatusRec) (JobProjectionState, *CallerJobProjection) {
	if row.LatestCallerJobProjectionVersion != callerJobProjectionVersion ||
		row.ProjectedCallerJob == nil {
		return JobProjectionUnavailable, nil
	}
	job := row.ProjectedCallerJob.toJob(JobCallerLeaf)
	if job.ID == "" || job.Target != row.Name || job.TargetTruncated {
		return JobProjectionUnavailable, nil
	}
	return JobProjectionExact, &CallerJobProjection{
		Status: job.Status, Attempts: job.Attempts,
	}
}

func resolverJobProjection(row repoStatusRec) (JobProjectionState, *ResolverJobProjection) {
	if row.LatestResolverJobProjectionVersion != resolverJobProjectionVersion ||
		row.ProjectedResolverJob == nil {
		return JobProjectionUnavailable, nil
	}
	job := row.ProjectedResolverJob.toJob(JobResolverCatalog)
	if job.ID == "" || job.Target != row.Name || job.TargetTruncated {
		return JobProjectionUnavailable, nil
	}
	return JobProjectionExact, &ResolverJobProjection{
		Status: job.Status, Attempts: job.Attempts,
	}
}

// GetResolverJobProjection reads only one repository's creation-linked
// resolver-catalog job — the caller pipeline's immediate upstream — with the
// same single-record discipline as the caller read.
func (s *Surreal) GetResolverJobProjection(
	ctx context.Context,
	repository string,
) (JobProjectionState, *ResolverJobProjection, error) {
	results, err := surrealdb.Query[[]repoStatusRec](ctx, s.db, `
		SELECT name, latest_resolver_job_projection_version, {
			id: latest_resolver_job,
			target: IF type::is_string(latest_resolver_job.target)
				THEN string::slice(latest_resolver_job.target, 0, $max_target_characters)
				ELSE '' END,
			target_truncated: IF type::is_string(latest_resolver_job.target)
				THEN string::len(latest_resolver_job.target) > $max_target_characters
				ELSE false END,
			status: latest_resolver_job.status,
			attempts: latest_resolver_job.attempts,
			created_at: latest_resolver_job.created_at
		} AS projected_resolver_job FROM $repository`, map[string]any{
		"repository":            repoID(repository),
		"max_target_characters": MaxJobHistoryTargetCharacters,
	})
	if err != nil {
		return JobProjectionUnavailable, nil, fmt.Errorf(
			"get resolver job projection: %w", err,
		)
	}
	if results == nil || len(*results) != 1 {
		return JobProjectionUnavailable, nil, errors.New(
			"get resolver job projection: invalid query result",
		)
	}
	rows := (*results)[0].Result
	if len(rows) == 0 {
		return JobProjectionUnavailable, nil, fmt.Errorf(
			"repo %q: %w", repository, ErrNotFound,
		)
	}
	state, projection := resolverJobProjection(rows[0])
	return state, projection, nil
}

// GetCallerJobProjection reads only one repository's creation-linked caller
// job. It never scans caller history or materializes unrelated repositories.
func (s *Surreal) GetCallerJobProjection(
	ctx context.Context,
	repository string,
) (JobProjectionState, *CallerJobProjection, error) {
	results, err := surrealdb.Query[[]repoStatusRec](ctx, s.db, `
		SELECT name, latest_caller_job_projection_version, {
			id: latest_caller_job,
			target: IF type::is_string(latest_caller_job.target)
				THEN string::slice(latest_caller_job.target, 0, $max_target_characters)
				ELSE '' END,
			target_truncated: IF type::is_string(latest_caller_job.target)
				THEN string::len(latest_caller_job.target) > $max_target_characters
				ELSE false END,
			status: latest_caller_job.status,
			attempts: latest_caller_job.attempts,
			created_at: latest_caller_job.created_at
		} AS projected_caller_job FROM $repository`, map[string]any{
		"repository":            repoID(repository),
		"max_target_characters": MaxJobHistoryTargetCharacters,
	})
	if err != nil {
		return JobProjectionUnavailable, nil, fmt.Errorf(
			"get caller job projection: %w", err,
		)
	}
	if results == nil || len(*results) != 1 {
		return JobProjectionUnavailable, nil, errors.New(
			"get caller job projection: invalid query result",
		)
	}
	rows := (*results)[0].Result
	if len(rows) == 0 {
		return JobProjectionUnavailable, nil, fmt.Errorf(
			"repo %q: %w", repository, ErrNotFound,
		)
	}
	state, projection := callerJobProjection(rows[0])
	return state, projection, nil
}

// RepoStatuses joins current repos and membership with one prospective record
// link per repo. It never reads the indexing-job table as history. A repository
// without this writer generation's CREATE projection is explicitly unavailable
// until a new indexing job establishes current authority.
func (s *Surreal) RepoStatuses(ctx context.Context) ([]RepoStatus, error) {
	repoResults, err := surrealdb.Query[[]repoStatusRec](ctx, s.db,
		`SELECT *, {
			id: latest_index_job,
			target: IF type::is_string(latest_index_job.target)
				THEN string::slice(latest_index_job.target, 0, $max_target_characters)
				ELSE '' END,
			target_truncated: IF type::is_string(latest_index_job.target)
				THEN string::len(latest_index_job.target) > $max_target_characters
				ELSE false END,
			status: latest_index_job.status,
			attempts: latest_index_job.attempts,
			error: IF type::is_string(latest_index_job.error)
				THEN string::slice(latest_index_job.error, 0, $max_error_characters)
				ELSE '' END,
			error_truncated: IF type::is_string(latest_index_job.error)
				THEN string::len(latest_index_job.error) > $max_error_characters
				ELSE false END,
			created_at: latest_index_job.created_at,
			not_before: latest_index_job.not_before,
			claimed_by: IF type::is_string(latest_index_job.claimed_by)
				THEN string::slice(latest_index_job.claimed_by, 0, $max_claimed_by_characters)
				ELSE '' END,
			claimed_by_truncated: IF type::is_string(latest_index_job.claimed_by)
				THEN string::len(latest_index_job.claimed_by) > $max_claimed_by_characters
				ELSE false END,
			claimed_at: latest_index_job.claimed_at,
			heartbeat_at: latest_index_job.heartbeat_at,
			finished_at: latest_index_job.finished_at,
			force: latest_index_job.force
		} AS projected_job, {
			id: latest_extraction_job,
			target: IF type::is_string(latest_extraction_job.target)
				THEN string::slice(latest_extraction_job.target, 0, $max_target_characters)
				ELSE '' END,
			target_truncated: IF type::is_string(latest_extraction_job.target)
				THEN string::len(latest_extraction_job.target) > $max_target_characters
				ELSE false END,
			status: latest_extraction_job.status,
			attempts: latest_extraction_job.attempts,
			error: IF type::is_string(latest_extraction_job.error)
				THEN string::slice(latest_extraction_job.error, 0, $max_error_characters)
				ELSE '' END,
			error_truncated: IF type::is_string(latest_extraction_job.error)
				THEN string::len(latest_extraction_job.error) > $max_error_characters
				ELSE false END,
			created_at: latest_extraction_job.created_at
		} AS projected_extraction_job, {
			id: latest_caller_job,
			target: IF type::is_string(latest_caller_job.target)
				THEN string::slice(latest_caller_job.target, 0, $max_target_characters)
				ELSE '' END,
			target_truncated: IF type::is_string(latest_caller_job.target)
				THEN string::len(latest_caller_job.target) > $max_target_characters
				ELSE false END,
			status: latest_caller_job.status,
			attempts: latest_caller_job.attempts,
			created_at: latest_caller_job.created_at
		} AS projected_caller_job, {
			id: latest_resolver_job,
			target: IF type::is_string(latest_resolver_job.target)
				THEN string::slice(latest_resolver_job.target, 0, $max_target_characters)
				ELSE '' END,
			target_truncated: IF type::is_string(latest_resolver_job.target)
				THEN string::len(latest_resolver_job.target) > $max_target_characters
				ELSE false END,
			status: latest_resolver_job.status,
			attempts: latest_resolver_job.attempts,
			created_at: latest_resolver_job.created_at
		} AS projected_resolver_job
		FROM repo ORDER BY name`, map[string]any{
			"max_target_characters":     MaxJobHistoryTargetCharacters,
			"max_error_characters":      MaxJobHistoryErrorCharacters,
			"max_claimed_by_characters": MaxJobHistoryClaimedByCharacters,
		})
	if err != nil {
		return nil, err
	}
	repos := (*repoResults)[0].Result
	for index := range repos {
		if err := validateRepoAnalysisUnit(&repos[index].Repo); err != nil {
			return nil, err
		}
	}
	memb, err := surrealdb.Query[[]struct {
		Connection string `json:"connection"`
		Repo       string `json:"repo"`
	}](ctx, s.db, "SELECT connection, repo FROM repo_connection", nil)
	if err != nil {
		return nil, err
	}
	conns := map[string][]string{}
	for _, m := range (*memb)[0].Result {
		conns[m.Repo] = append(conns[m.Repo], m.Connection)
	}

	statuses := make([]RepoStatus, len(repos))
	for i, row := range repos {
		state := JobProjectionUnavailable
		var latest *Job
		if row.LatestIndexJobProjectionVersion == indexingJobProjectionVersion &&
			row.ProjectedJob != nil {
			job := row.ProjectedJob.toJob(JobIndex)
			if job.ID != "" && job.Target == row.Name {
				state = JobProjectionExact
				latest = &job
			}
		}
		extractionState := JobProjectionUnavailable
		var extraction *ExtractionJobProjection
		if row.LatestExtractionJobProjectionVersion == extractionJobProjectionVersion &&
			row.ProjectedExtractionJob != nil {
			job := row.ProjectedExtractionJob.toJob(JobExtract)
			if job.ID != "" && job.Target == row.Name && !job.TargetTruncated && !job.ErrorTruncated {
				extractionState = JobProjectionExact
				extraction = &ExtractionJobProjection{Status: job.Status, Attempts: job.Attempts}
				if refusal, ok := pipelinerefusal.ParseDurableErrorText(job.Error); ok {
					extraction.Refusal = &refusal
				}
			}
		}
		callerState, caller := callerJobProjection(row)
		resolverState, resolver := resolverJobProjection(row)
		statuses[i] = RepoStatus{
			Repo:                   row.Repo,
			Connections:            conns[row.Name],
			Orphaned:               len(conns[row.Name]) == 0,
			LastIndexJob:           latest,
			LastIndexJobState:      state,
			LastExtractionJob:      extraction,
			LastExtractionJobState: extractionState,
			LastCallerJob:          caller,
			LastCallerJobState:     callerState,
			LastResolverJob:        resolver,
			LastResolverJobState:   resolverState,
			AnalysisUnit:           analysisunit.CloneState(row.IndexedAnalysisUnit),
		}
	}
	return statuses, nil
}

// --- jobs ---

// jobRec pairs Job's fields with the SurrealDB record id for decoding.
type jobRec struct {
	Job
	RecID *models.RecordID `json:"id"`
}

func (j jobRec) toJob(kind JobKind) Job {
	out := j.Job
	out.Kind = kind
	if j.RecID != nil {
		out.ID = j.RecID.String()
	}
	return out
}

// projectLatestIndexJobSQL is embedded in every generic queue transaction that
// can create a job. Keeping the projection in those writer transactions avoids
// replaying a table event for every retained job during a database restore.
// Coalesced downstream rows are repaired separately below; an existing index
// row deliberately does not establish current writer authority.
const projectLatestIndexJobSQL = `
IF $table = 'indexing_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_index_job = $created_job.id,
		latest_index_job_created_at = $created_job.created_at,
		latest_index_job_projection_version = 't30.6n-indexing-job-latest-v1'
	WHERE latest_index_job_created_at = NONE
		OR latest_index_job_created_at < $created_job.created_at
		OR (latest_index_job_created_at = $created_job.created_at
			AND latest_index_job < $created_job.id)
	RETURN NONE;
};
IF $table = 'extraction_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_extraction_job = $created_job.id,
		latest_extraction_job_created_at = $created_job.created_at,
		latest_extraction_job_projection_version = 't40r1-extraction-job-latest-v1'
	WHERE latest_extraction_job_created_at = NONE
		OR latest_extraction_job_created_at < $created_job.created_at
		OR (latest_extraction_job_created_at = $created_job.created_at
			AND latest_extraction_job < $created_job.id)
	RETURN NONE;
};
IF $table = 'caller_leaf_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_caller_job = $created_job.id,
		latest_caller_job_created_at = $created_job.created_at,
		latest_caller_job_projection_version = 't40r1-caller-job-latest-v1'
	WHERE latest_caller_job_created_at = NONE
		OR latest_caller_job_created_at < $created_job.created_at
		OR (latest_caller_job_created_at = $created_job.created_at
			AND latest_caller_job < $created_job.id)
	RETURN NONE;
};
IF $table = 'resolver_catalog_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_resolver_job = $created_job.id,
		latest_resolver_job_created_at = $created_job.created_at,
		latest_resolver_job_projection_version = 't40r1-resolver-job-latest-v1'
	WHERE latest_resolver_job_created_at = NONE
		OR latest_resolver_job_created_at < $created_job.created_at
		OR (latest_resolver_job_created_at = $created_job.created_at
			AND latest_resolver_job < $created_job.id)
	RETURN NONE;
};`

// projectExtractionJobSQL projects the exact extraction successor returned by
// candidate publication. That transaction can create or coalesce the row, so
// the returned row rather than job-table history is the current writer proof.
const projectExtractionJobSQL = `
LET $extraction_projected = IF array::len($fanout) = 1
	THEN $fanout[0] ELSE NONE END;
IF $extraction_projected != NONE {
	UPDATE type::record('repo', $extraction_projected.target)
	SET latest_extraction_job = $extraction_projected.id,
		latest_extraction_job_created_at = $extraction_projected.created_at,
		latest_extraction_job_projection_version = 't40r1-extraction-job-latest-v1'
	WHERE latest_extraction_job_created_at = NONE
		OR latest_extraction_job_created_at < $extraction_projected.created_at
		OR (latest_extraction_job_created_at = $extraction_projected.created_at
			AND latest_extraction_job < $extraction_projected.id)
	RETURN NONE;
};`

// projectCallerJobSQL mirrors the caller_leaf_job arm of
// projectLatestIndexJobSQL for domain transactions that create or coalesce a
// caller successor outside the generic queue. Projecting the exact returned
// pending row also repairs a pre-projection job after an upgrade.
const projectCallerJobSQL = `
IF $caller_projected != NONE {
	UPDATE type::record('repo', $repository)
	SET latest_caller_job = $caller_projected.id,
		latest_caller_job_created_at = $caller_projected.created_at,
		latest_caller_job_projection_version = 't40r1-caller-job-latest-v1'
	WHERE latest_caller_job_created_at = NONE
		OR latest_caller_job_created_at < $caller_projected.created_at
		OR (latest_caller_job_created_at = $caller_projected.created_at
			AND latest_caller_job < $caller_projected.id)
	RETURN NONE;
};`

// projectExistingJobProjectionSQL is appended only where $job may be a
// coalesced pending downstream row. Newly created jobs are already handled by
// projectLatestIndexJobSQL; these arms establish the current writer marker for
// the exact returned row without scanning job history.
const projectExistingJobProjectionSQL = `
IF $table = 'extraction_job' AND $created_job = NONE AND array::len($job) = 1 {
	UPDATE type::record('repo', $target)
	SET latest_extraction_job = $job[0].id,
		latest_extraction_job_created_at = $job[0].created_at,
		latest_extraction_job_projection_version = 't40r1-extraction-job-latest-v1'
	WHERE latest_extraction_job_created_at = NONE
		OR latest_extraction_job_created_at < $job[0].created_at
		OR (latest_extraction_job_created_at = $job[0].created_at
			AND latest_extraction_job < $job[0].id)
	RETURN NONE;
};
IF $table = 'caller_leaf_job' AND $created_job = NONE AND array::len($job) = 1 {
	UPDATE type::record('repo', $target)
	SET latest_caller_job = $job[0].id,
		latest_caller_job_created_at = $job[0].created_at,
		latest_caller_job_projection_version = 't40r1-caller-job-latest-v1'
	WHERE latest_caller_job_created_at = NONE
		OR latest_caller_job_created_at < $job[0].created_at
		OR (latest_caller_job_created_at = $job[0].created_at
			AND latest_caller_job < $job[0].id)
	RETURN NONE;
};
IF $table = 'resolver_catalog_job' AND $created_job = NONE AND array::len($job) = 1 {
	UPDATE type::record('repo', $target)
	SET latest_resolver_job = $job[0].id,
		latest_resolver_job_created_at = $job[0].created_at,
		latest_resolver_job_projection_version = 't40r1-resolver-job-latest-v1'
	WHERE latest_resolver_job_created_at = NONE
		OR latest_resolver_job_created_at < $job[0].created_at
		OR (latest_resolver_job_created_at = $job[0].created_at
			AND latest_resolver_job < $job[0].id)
	RETURN NONE;
};`

// projectResolverJobSQL mirrors projectCallerJobSQL for the seven domain
// transactions that mint or coalesce resolver-catalog successors through the
// shared $pending_catalog/$catalog_fanout shape (candidate manifest
// publication and retirement, indexed-state transitions and clearing,
// evidence chunks, extraction-run publication). It is site-agnostic: the
// fan-out row's own target names the repository, and projecting the coalesced
// row also repairs an exact pre-cutover pending successor.
const projectResolverJobSQL = `
LET $resolver_projected = IF array::len($catalog_fanout) = 1
	THEN $catalog_fanout[0] ELSE NONE END;
IF $resolver_projected != NONE {
	UPDATE type::record('repo', $resolver_projected.target)
	SET latest_resolver_job = $resolver_projected.id,
		latest_resolver_job_created_at = $resolver_projected.created_at,
		latest_resolver_job_projection_version = 't40r1-resolver-job-latest-v1'
	WHERE latest_resolver_job_created_at = NONE
		OR latest_resolver_job_created_at < $resolver_projected.created_at
		OR (latest_resolver_job_created_at = $resolver_projected.created_at
			AND latest_resolver_job < $resolver_projected.id)
	RETURN NONE;
};`

func (s *Surreal) CreateJob(ctx context.Context, kind JobKind, target string) (*Job, error) {
	created := time.Now().UTC()
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`BEGIN;
LET $created_job = (CREATE type::table($table) CONTENT {
			target: $target, status: 'pending', attempts: 0,
			created_at: $created, pending_key: $target, force: false
		} RETURN AFTER)[0];`+projectLatestIndexJobSQL+`
RETURN [$created_job];
COMMIT;`,
		map[string]any{"table": string(kind), "target": target, "created": created})
	if err != nil {
		return nil, err
	}
	rows := firstNonEmpty(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("create %s: empty result", kind)
	}
	out := rows[0].toJob(kind)
	return &out, nil
}

const enqueuePendingSQL = `
BEGIN;
LET $pending = (SELECT id, created_at FROM type::table($table)
    WHERE pending_key = $target AND status = 'pending'
    ORDER BY created_at LIMIT 1)[0].id;
LET $job = IF $pending != NONE THEN
    (UPDATE $pending SET force = IF $force THEN true ELSE force END,
		recovery_lease = NONE, not_before = NONE RETURN AFTER)
ELSE
    (CREATE type::table($table) CONTENT {
        target: $target,
        status: 'pending',
        attempts: 0,
        created_at: time::now(),
        pending_key: $target,
        force: $force
    } RETURN AFTER)
END;
LET $created_job = IF $pending = NONE THEN $job[0] ELSE NONE END;` +
	projectLatestIndexJobSQL + projectExistingJobProjectionSQL + `
RETURN $job;
COMMIT;`

const maxQueueRetries = 64

// EnqueuePending atomically ensures that target has one immediately claimable
// pending job. A fresh event clears an existing retry backoff while preserving
// its consumed attempt count. An already-running job is deliberately ignored:
// the pending row is its single successor, preserving events that arrive after
// the worker took its snapshot.
func (s *Surreal) EnqueuePending(ctx context.Context, kind JobKind, target string, force bool) (*Job, error) {
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]jobRec](ctx, s.db, enqueuePendingSQL,
			map[string]any{"table": string(kind), "target": target, "force": force})
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, err
		}
		rows := firstNonEmpty(results)
		if len(rows) == 0 {
			return nil, fmt.Errorf("enqueue %s %q: empty result", kind, target)
		}
		job := rows[0].toJob(kind)
		return &job, nil
	}
}

const ensureJobSuccessorSQL = `
BEGIN;
LET $owned = (SELECT id FROM type::record($id)
    WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who)[0].id;
LET $pending = (SELECT id, created_at FROM type::table($table)
    WHERE pending_key = $target AND status = 'pending'
    ORDER BY created_at LIMIT 1)[0].id;
LET $job = IF $owned = NONE THEN []
ELSE IF $pending != NONE THEN
    (UPDATE $pending SET force = IF $force THEN true ELSE force END RETURN AFTER)
ELSE
    (CREATE type::table($table) CONTENT {
        target: $target,
        status: 'pending',
        attempts: 0,
        created_at: time::now(),
        pending_key: $target,
        force: $force,
        recovery_lease: $lease
    } RETURN AFTER)
END;
LET $created_job = IF $owned != NONE AND $pending = NONE THEN $job[0] ELSE NONE END;` +
	projectLatestIndexJobSQL + projectExistingJobProjectionSQL + `
RETURN $job;
COMMIT;`

// EnsureJobSuccessor creates a crash-recovery successor owned by this active
// lease. It deliberately does not claim an already-pending event as recovery
// work: that row may represent fresher candidate/resolver authority. A later
// ordinary EnqueuePending clears ownership even when it only coalesces into the
// same row, giving final-attempt failure a safe compare-and-set boundary. Every
// returned error carries SuccessorRetry because a transport/context failure may
// arrive after the transaction committed; the provenance-aware final transition
// is safe whether the tagged row exists or not.
func (s *Surreal) EnsureJobSuccessor(
	ctx context.Context,
	job Job,
	force bool,
) (*Job, error) {
	if job.ID == "" || job.LeaseToken == "" || job.ClaimedBy == "" ||
		job.Kind == "" || job.Target == "" {
		return nil, WithSuccessorRetry(fmt.Errorf(
			"job %q: %w", job.ID, ErrLeaseLost,
		))
	}
	vars := map[string]any{
		"id": job.ID, "table": string(job.Kind), "target": job.Target,
		"lease": job.LeaseToken, "who": job.ClaimedBy, "force": force,
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]jobRec](
			ctx, s.db, ensureJobSuccessorSQL, vars,
		)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil &&
				attempt+1 < maxQueueRetries {
				continue
			}
			return nil, WithSuccessorRetry(err)
		}
		rows := firstNonEmpty(results)
		if len(rows) == 0 {
			return nil, WithSuccessorRetry(fmt.Errorf(
				"job %q: %w", job.ID, ErrLeaseLost,
			))
		}
		out := rows[0].toJob(job.Kind)
		return &out, nil
	}
}

const jobHistoryScanRows = 256

const boundedJobHistoryFields = `id,
	IF type::is_string(target)
		THEN string::slice(target, 0, $max_target_characters)
		ELSE '' END AS target,
	IF type::is_string(target)
		THEN string::len(target) > $max_target_characters
		ELSE false END AS target_truncated,
	status, attempts,
	IF type::is_string(error)
		THEN string::slice(error, 0, $max_error_characters)
		ELSE '' END AS error,
	IF type::is_string(error)
		THEN string::len(error) > $max_error_characters
		ELSE false END AS error_truncated,
	created_at, not_before,
	IF type::is_string(claimed_by)
		THEN string::slice(claimed_by, 0, $max_claimed_by_characters)
		ELSE '' END AS claimed_by,
	IF type::is_string(claimed_by)
		THEN string::len(claimed_by) > $max_claimed_by_characters
		ELSE false END AS claimed_by_truncated,
	claimed_at, heartbeat_at, finished_at, force`

var _ JobHistoryStore = (*Surreal)(nil)

// ListJobsPage scans one fixed record-id window, then applies the optional
// status filter in Go. Filtering in Surreal would either scan the complete
// historical status index at a late cursor or scan forward without a physical
// bound when the requested status is sparse. Continuations use SurrealDB's
// record-range syntax so the storage engine seeks to the cursor instead of
// filtering a table scan from its first key; LIMIT bounds the forward scan and
// no server-side sort is required.
func (s *Surreal) ListJobsPage(ctx context.Context, query JobPageQuery) (*JobPage, error) {
	if !validJobKind(query.Kind) {
		return nil, fmt.Errorf("list jobs: invalid kind %q", query.Kind)
	}
	if !validJobStatus(query.Status, true) {
		return nil, fmt.Errorf("list jobs: invalid status %q", query.Status)
	}
	if query.Limit < 1 || query.Limit > MaxJobHistoryPageRows {
		return nil, fmt.Errorf(
			"list jobs: limit must be from 1 through %d",
			MaxJobHistoryPageRows,
		)
	}
	vars := map[string]any{
		"table": string(query.Kind), "scan_limit": jobHistoryScanRows + 1,
		"max_target_characters":     MaxJobHistoryTargetCharacters,
		"max_error_characters":      MaxJobHistoryErrorCharacters,
		"max_claimed_by_characters": MaxJobHistoryClaimedByCharacters,
	}
	statement := `SELECT ` + boundedJobHistoryFields + `
		FROM type::table($table) ORDER BY id LIMIT $scan_limit`
	if query.After != nil {
		if query.After.Kind != query.Kind || query.After.Status != query.Status ||
			!validJobCursorID(query.After.ID) {
			return nil, errors.New("list jobs: cursor does not match query scope")
		}
		cursorRecord := models.NewRecordID(string(query.Kind), query.After.ID)
		statement = `SELECT ` + boundedJobHistoryFields + `
			FROM ` + cursorRecord.String() + `>..
			ORDER BY id LIMIT $scan_limit`
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, statement, vars)
	if err != nil {
		return nil, err
	}
	var scanned []jobRec
	for _, result := range *results {
		scanned = append(scanned, result.Result...)
	}
	hasMore := len(scanned) > jobHistoryScanRows
	if hasMore {
		scanned = scanned[:jobHistoryScanRows]
	}

	page := &JobPage{Jobs: make([]Job, 0, query.Limit)}
	var lastReturnedID string
	for _, row := range scanned {
		if query.Status != "" && row.Status != query.Status {
			continue
		}
		cursorID, cursorErr := jobRecordCursorID(row)
		if cursorErr != nil {
			return nil, fmt.Errorf("list jobs: %w", cursorErr)
		}
		if len(page.Jobs) == query.Limit {
			page.Next = &JobCursor{
				Kind: query.Kind, Status: query.Status, ID: lastReturnedID,
			}
			return page, nil
		}
		page.Jobs = append(page.Jobs, row.toJob(query.Kind))
		lastReturnedID = cursorID
	}
	if hasMore {
		cursorID, cursorErr := jobRecordCursorID(scanned[len(scanned)-1])
		if cursorErr != nil {
			return nil, fmt.Errorf("list jobs: %w", cursorErr)
		}
		page.Next = &JobCursor{
			Kind: query.Kind, Status: query.Status, ID: cursorID,
		}
	}
	return page, nil
}

// ListJobs is a fixed-window compatibility helper for tests and small
// diagnostic callers. It never continues across history implicitly; callers
// requiring complete traversal must use ListJobsPage and its explicit cursor.
func (s *Surreal) ListJobs(ctx context.Context, kind JobKind, status JobStatus) ([]Job, error) {
	page, err := s.ListJobsPage(ctx, JobPageQuery{
		Kind: kind, Status: status, Limit: MaxJobHistoryPageRows,
	})
	if err != nil {
		return nil, err
	}
	if page.Next != nil {
		return nil, fmt.Errorf("list %s jobs: %w", kind, ErrResultLimit)
	}
	return page.Jobs, nil
}

func validJobStatus(status JobStatus, allowEmpty bool) bool {
	if status == "" {
		return allowEmpty
	}
	switch status {
	case StatusPending, StatusClaimed, StatusRunning,
		StatusDone, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

func validJobCursorID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, character := range id {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func jobRecordCursorID(row jobRec) (string, error) {
	if row.RecID == nil {
		return "", errors.New("job row has no record id")
	}
	id, ok := row.RecID.ID.(string)
	if row.RecID.Table == "" || !ok || !validJobCursorID(id) {
		return "", errors.New("job row has an unsupported record id")
	}
	return id, nil
}

// claimSQL is the T1.3 spike winner: optimistic conditional update, no
// explicit transaction. The UPDATE re-checks status = 'pending' so a lost
// race returns empty instead of double-claiming; losing costs one cheap read
// versus a server-side transaction abort (see PLAN.md 2026-07-09 ADR).
const claimSQL = `
LET $cand = (SELECT id, created_at FROM type::table($table)
    WHERE status = 'pending' AND (not_before = NONE OR not_before <= time::now())
    ORDER BY created_at LIMIT 1)[0].id;
RETURN IF $cand != NONE THEN
    (UPDATE $cand SET status = 'claimed', claimed_by = $who, lease_token = $lease, pending_key = NONE,
     claimed_at = time::now(), heartbeat_at = time::now(), recovery_lease = NONE
     WHERE status = 'pending' RETURN AFTER)
ELSE [] END;`

// ClaimJob atomically claims the oldest pending job of kind for who. It
// retries internally on lost races and returns ErrNotFound once no pending
// jobs remain.
func (s *Surreal) ClaimJob(ctx context.Context, kind JobKind, who string) (*Job, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lease, err := newLeaseToken()
		if err != nil {
			return nil, err
		}
		res, err := surrealdb.Query[[]jobRec](ctx, s.db, claimSQL,
			map[string]any{"table": string(kind), "who": who, "lease": lease})
		if err != nil {
			if isRetryable(err) {
				continue
			}
			return nil, err
		}
		rows := firstNonEmpty(res)
		if len(rows) > 0 {
			j := rows[0].toJob(kind)
			return &j, nil
		}
		n, err := s.countPending(ctx, kind)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("no pending %s: %w", kind, ErrNotFound)
		}
	}
}

func newLeaseToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate job lease: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

// firstNonEmpty returns the first statement result carrying rows; the RETURN
// statement's position differs with and without BEGIN..COMMIT.
func firstNonEmpty(res *[]surrealdb.QueryResult[[]jobRec]) []jobRec {
	for _, r := range *res {
		if len(r.Result) > 0 {
			return r.Result
		}
	}
	return nil
}

// permanentErrMarker prefixes THROW messages for deterministic rule
// violations. Without it, THROW text like "conflicting attributes" matches
// the transient substrings below and a permanent error is retried to the cap.
const permanentErrMarker = "phebs-permanent:"

func isRetryable(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, permanentErrMarker) {
		return false
	}
	return strings.Contains(msg, "conflict") || strings.Contains(msg, "retry")
}

func isRetryableEnqueue(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, permanentErrMarker) {
		return false
	}
	return isRetryable(err) ||
		strings.Contains(msg, "unique") || strings.Contains(msg, "already contains")
}

func (s *Surreal) countPending(ctx context.Context, kind JobKind) (int, error) {
	res, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db,
		"SELECT count() AS count FROM type::table($table) WHERE status = 'pending' AND (not_before = NONE OR not_before <= time::now()) GROUP ALL",
		map[string]any{"table": string(kind)})
	if err != nil {
		return 0, err
	}
	rows := (*res)[0].Result
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}

// HeartbeatJob refreshes a running lease. A zero-row update is never benign:
// the row may have been reaped and reclaimed, so the old worker must stop.
func (s *Surreal) HeartbeatJob(ctx context.Context, job Job) error {
	return s.updateLease(ctx, job,
		"UPDATE type::record($id) SET heartbeat_at = time::now() WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who RETURN AFTER",
		nil)
}

// RequeueJob returns a failed execution to the queue with attempts+1 and a
// backoff gate; claim state is cleared.
func (s *Surreal) RequeueJob(ctx context.Context, job Job, errMsg string, notBefore time.Time) error {
	return s.returnToPending(ctx, job, errMsg, notBefore, true, nil)
}

const failJobWithSuccessorSQL = `
BEGIN;
LET $owned = (SELECT id FROM type::record($id)
    WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who)[0].id;
LET $successor = (SELECT id, created_at FROM type::table($table)
    WHERE pending_key = $target AND status = 'pending'
    AND recovery_lease = $lease
    ORDER BY created_at LIMIT 1)[0].id;
LET $failed_successor = IF $owned != NONE AND $successor != NONE THEN
    (UPDATE $successor SET status = 'failed', attempts = $attempts,
        error = $err, not_before = NONE, finished_at = time::now(),
        pending_key = NONE RETURN AFTER)
ELSE [] END;
RETURN IF $owned = NONE THEN []
ELSE IF $successor != NONE THEN
    (UPDATE type::record($id) SET status = 'canceled', error = $superseded,
        finished_at = time::now(), lease_token = NONE, pending_key = NONE
     WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who
     RETURN AFTER)
ELSE
    (UPDATE type::record($id) SET status = 'failed', attempts = $attempts,
        error = $err, not_before = NONE, finished_at = time::now(),
        lease_token = NONE, pending_key = NONE
     WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who
     RETURN AFTER)
END;
COMMIT;`

// FailJobWithSuccessor exhausts an error returned after the handler created a
// pending successor. It may consume only a row still carrying this exact
// active lease's recovery provenance. An ordinary enqueue clears that field,
// so fresher coalesced work survives while the exhausted active row fails.
func (s *Surreal) FailJobWithSuccessor(
	ctx context.Context,
	job Job,
	errMsg string,
) error {
	if job.ID == "" || job.LeaseToken == "" || job.ClaimedBy == "" {
		return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	vars := map[string]any{
		"id": job.ID, "table": string(job.Kind), "target": job.Target,
		"lease": job.LeaseToken, "who": job.ClaimedBy,
		"attempts":   job.Attempts + 1,
		"err":        errMsg,
		"superseded": "attempts exhausted by pending successor: " + errMsg,
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]jobRec](
			ctx, s.db, failJobWithSuccessorSQL, vars,
		)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil &&
				attempt+1 < maxQueueRetries {
				continue
			}
			return err
		}
		if !queryContainsJob(results, job.ID) {
			return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
		}
		return nil
	}
}

// ReleaseJob is the shutdown path: work returns to pending immediately without
// being counted as a failed attempt.
func (s *Surreal) ReleaseJob(ctx context.Context, job Job, errMsg string) error {
	return s.returnToPending(ctx, job, errMsg, time.Time{}, false, nil)
}

const deferJobSQL = `
BEGIN;
LET $owned = (SELECT id FROM type::record($id)
    WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who)[0].id;
LET $successor = (SELECT id, created_at FROM type::table($table)
    WHERE pending_key = $target AND status = 'pending'
    ORDER BY created_at LIMIT 1)[0].id;
LET $preserved_successor = IF $owned != NONE AND $successor != NONE THEN
    (UPDATE $successor SET force = IF $force THEN true ELSE force END RETURN AFTER)
ELSE [] END;
RETURN IF $owned = NONE THEN []
ELSE IF $successor != NONE THEN
    (UPDATE type::record($id) SET status = 'canceled', error = $superseded,
        finished_at = time::now(), lease_token = NONE, pending_key = NONE
     WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
     RETURN AFTER)
ELSE
	(UPDATE type::record($id) SET status = 'pending', error = $err,
		not_before = $nb, created_at = time::now(), claimed_by = NONE, claimed_at = NONE,
        heartbeat_at = NONE, lease_token = NONE, recovery_lease = NONE,
        finished_at = NONE, pending_key = $target
     WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
     RETURN AFTER)
END;
COMMIT;`

// DeferJob returns an upstream-blocked active lease to pending without
// consuming an attempt. If a real freshness event arrived while the handler
// was running, its separate pending successor wins unchanged and immediately;
// the stale active row is only canceled. The returned time is the exact
// persisted not_before fence; zero means that successor won. A deferred row
// moves to the pending queue tail so an older blocked target cannot starve
// siblings that were already ready.
func (s *Surreal) DeferJob(
	ctx context.Context,
	job Job,
	errMsg string,
	delay time.Duration,
) (time.Time, error) {
	if job.ID == "" || job.LeaseToken == "" || job.ClaimedBy == "" ||
		job.Kind == "" || job.Target == "" || delay <= 0 {
		return time.Time{}, fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	for attempt := 0; ; attempt++ {
		notBefore := time.Now().UTC().Add(delay)
		vars := map[string]any{
			"id": job.ID, "table": string(job.Kind), "target": job.Target,
			"lease": job.LeaseToken, "who": job.ClaimedBy, "force": job.Force,
			"err": errMsg, "nb": notBefore,
			"superseded": "superseded by pending freshness event: " + errMsg,
		}
		results, err := surrealdb.Query[[]jobRec](ctx, s.db, deferJobSQL, vars)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil &&
				attempt+1 < maxQueueRetries {
				continue
			}
			return time.Time{}, err
		}
		persisted, found := queryJobByID(results, job.ID, job.Kind)
		if !found {
			return time.Time{}, fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
		}
		if persisted.Status == StatusCanceled {
			return time.Time{}, nil
		}
		if persisted.Status != StatusPending || persisted.NotBefore == nil {
			return time.Time{}, fmt.Errorf("job %q: invalid deferred state", job.ID)
		}
		return *persisted.NotBefore, nil
	}
}

func queryJobByID(
	results *[]surrealdb.QueryResult[[]jobRec],
	id string,
	kind JobKind,
) (Job, bool) {
	for _, result := range *results {
		for _, row := range result.Result {
			if row.RecID != nil && row.RecID.String() == id &&
				(row.Status == StatusPending || row.Status == StatusCanceled) {
				return row.toJob(kind), true
			}
		}
	}
	return Job{}, false
}

type heartbeatFence struct {
	observed time.Time
	cutoff   time.Time
}

// Compare heartbeat instants as epoch nanoseconds. Plain time.Time query
// parameters do not round-trip as Surreal datetime values for equality.
const returnToPendingSQL = `
BEGIN;
LET $owned = (SELECT id FROM type::record($id)
    WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
    AND ($reaping = false OR (time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff)))[0].id;
LET $successor = (SELECT id, created_at FROM type::table($table)
    WHERE pending_key = $target AND status = 'pending'
    ORDER BY created_at LIMIT 1)[0].id;
LET $merged = IF $owned != NONE AND $successor != NONE THEN
    (UPDATE $successor SET
        force = IF $force THEN true ELSE force END,
        attempts = IF $increment THEN $attempts ELSE attempts END,
        error = $err,
        not_before = IF $increment THEN $nb ELSE NONE END,
        recovery_lease = NONE
     RETURN AFTER)
ELSE [] END;
RETURN IF $owned = NONE THEN []
ELSE IF $successor != NONE THEN
    (UPDATE type::record($id) SET status = 'canceled', error = $superseded,
        finished_at = time::now(), lease_token = NONE, pending_key = NONE
     WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
     AND ($reaping = false OR (time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff))
     RETURN AFTER)
ELSE
    (UPDATE type::record($id) SET status = 'pending', attempts = $attempts, error = $err,
        not_before = IF $increment THEN $nb ELSE NONE END,
        claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE,
        lease_token = NONE, recovery_lease = NONE,
        finished_at = NONE, pending_key = $target
     WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
     AND ($reaping = false OR (time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff))
     RETURN AFTER)
END;
COMMIT;`

func (s *Surreal) returnToPending(ctx context.Context, job Job, errMsg string, notBefore time.Time, increment bool, fence *heartbeatFence) error {
	if job.ID == "" || job.LeaseToken == "" || job.ClaimedBy == "" {
		return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	vars := map[string]any{
		"id":              job.ID,
		"table":           string(job.Kind),
		"target":          job.Target,
		"lease":           job.LeaseToken,
		"who":             job.ClaimedBy,
		"force":           job.Force,
		"attempts":        job.Attempts + btoi(increment),
		"increment":       increment,
		"err":             errMsg,
		"superseded":      "superseded by pending successor: " + errMsg,
		"nb":              notBefore,
		"reaping":         fence != nil,
		"heartbeat_nanos": int64(0),
		"cutoff":          time.Time{},
	}
	if fence != nil {
		vars["heartbeat_nanos"] = fence.observed.UnixNano()
		vars["cutoff"] = fence.cutoff
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]jobRec](ctx, s.db, returnToPendingSQL, vars)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return err
		}
		if !queryContainsJob(results, job.ID) {
			return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
		}
		return nil
	}
}

func queryContainsJob(results *[]surrealdb.QueryResult[[]jobRec], id string) bool {
	for _, result := range *results {
		for _, row := range result.Result {
			if row.RecID != nil && row.RecID.String() == id {
				return true
			}
		}
	}
	return false
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Surreal) CancelPendingJobs(ctx context.Context, kind JobKind, target string) (int, error) {
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`UPDATE type::table($table) SET status = 'canceled', error = 'repository deleting',
		 finished_at = time::now(), not_before = NONE, pending_key = NONE
		 WHERE target = $target AND status = 'pending' RETURN AFTER`,
		map[string]any{"table": string(kind), "target": target})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, result := range *results {
		n += len(result.Result)
	}
	return n, nil
}

const maxJobReapRows = 256

// ReapStale rescues one fixed active-row batch whose worker died:
// claimed/running rows without a recent heartbeat go back to pending
// (attempts+1), or to failed once maxAttempts is exhausted. The status index
// and limit avoid terminal history and an active-set sort/materialization;
// later runner polls drain further stale batches. A separately pending crash-
// recovery successor is intentionally left fresh on final reap: process death
// cannot reveal whether the worker crossed its install boundary, and
// StaleAfter paces another turn. Returns how many rows it touched.
// ponytail: staleness cutoff computed on the Go clock vs server-side
// heartbeats — same host today (supervised child); revisit for fleet mode.
func (s *Surreal) ReapStale(ctx context.Context, kind JobKind, staleAfter time.Duration, maxAttempts int) (int, error) {
	if !validJobKind(kind) {
		return 0, fmt.Errorf("reap stale jobs: invalid kind %q", kind)
	}
	cutoff := time.Now().UTC().Add(-staleAfter)
	statement := fmt.Sprintf(`SELECT id, target, status, attempts, heartbeat_at,
		claimed_by, lease_token, force FROM %s WITH INDEX %s_status
		WHERE status IN ['claimed', 'running']
			AND heartbeat_at != NONE AND heartbeat_at < $cutoff
		LIMIT $limit`, kind, kind)
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		statement, map[string]any{"cutoff": cutoff, "limit": maxJobReapRows})
	if err != nil {
		return 0, err
	}
	rows := (*results)[0].Result
	n, varErr := 0, error(nil)
	for _, row := range rows {
		job := row.toJob(kind)
		err := s.reapOne(ctx, job, cutoff, maxAttempts)
		switch {
		case err == nil:
			n++
		case errors.Is(err, ErrLeaseLost):
			// The worker recovered or another reaper won the race.
		default:
			if varErr == nil {
				varErr = err
			}
		}
	}
	return n, varErr
}

func (s *Surreal) reapOne(ctx context.Context, job Job, cutoff time.Time, maxAttempts int) error {
	if job.HeartbeatAt == nil {
		return fmt.Errorf("job %q has no observed heartbeat: %w", job.ID, ErrLeaseLost)
	}
	fence := &heartbeatFence{observed: *job.HeartbeatAt, cutoff: cutoff}
	if job.Attempts+1 >= maxAttempts {
		return s.updateLease(ctx, job,
			`UPDATE type::record($id) SET status = 'failed',
			 error = 'reaped: stale claim, attempts exhausted', finished_at = time::now(),
			 lease_token = NONE, pending_key = NONE
			 WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
			 AND time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff
			 RETURN AFTER`,
			map[string]any{"heartbeat_nanos": fence.observed.UnixNano(), "cutoff": fence.cutoff})
	}
	return s.returnToPending(ctx, job, "reaped: stale claim", time.Now().UTC(), true, fence)
}

func (s *Surreal) SetJobStatus(ctx context.Context, job Job, status JobStatus, errMsg string) error {
	var sql string
	switch status {
	case StatusRunning:
		sql = `UPDATE type::record($id) SET status = 'running', error = $err
			WHERE status = 'claimed' AND lease_token = $lease AND claimed_by = $who RETURN AFTER`
	case StatusDone, StatusFailed:
		sql = `UPDATE type::record($id) SET status = $status, error = $err,
			finished_at = time::now(), lease_token = NONE, pending_key = NONE
			WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who RETURN AFTER`
	default:
		return fmt.Errorf("invalid leased job transition to %q", status)
	}
	return s.updateLease(ctx, job, sql,
		map[string]any{"status": string(status), "err": errMsg})
}

func (s *Surreal) updateLease(ctx context.Context, job Job, sql string, extra map[string]any) error {
	if job.ID == "" || job.LeaseToken == "" || job.ClaimedBy == "" {
		return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	vars := map[string]any{"id": job.ID, "lease": job.LeaseToken, "who": job.ClaimedBy}
	for key, value := range extra {
		vars[key] = value
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, sql, vars)
	if err != nil {
		return err
	}
	if len(firstNonEmpty(results)) == 0 {
		return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	return nil
}
