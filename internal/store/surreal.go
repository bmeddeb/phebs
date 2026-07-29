package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/analysisunit"
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
	return openLocal(ctx, dataDir, "")
}

// OpenLocalWithConfig starts the production local store and binds its runtime
// descriptor to the exact raw configuration bytes used by the server. Live
// backup refuses a different config that merely points at the same data dir.
func OpenLocalWithConfig(ctx context.Context, dataDir, configSHA256 string) (*Surreal, error) {
	if !validSHA256(configSHA256) {
		return nil, errors.New("open local store: config digest is invalid")
	}
	return openLocal(ctx, dataDir, configSHA256)
}

func openLocal(ctx context.Context, dataDir, configSHA256 string) (*Surreal, error) {
	if err := checkLocalRuntimeAvailable(dataDir); err != nil {
		return nil, err
	}
	runtime, stop, err := startLocal(ctx, dataDir)
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
	results, err = surrealdb.Query[any](ctx, s.db, pendingJobIndexes+evidenceIndexes, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("pending index statement %d: %s", i, r.Error.Message)
		}
	}
	return nil
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
DEFINE INDEX IF NOT EXISTS investigation_run_job_pending_key ON investigation_run_job FIELDS pending_key UNIQUE;`

// This generation-specific event is intentionally installed before migration
// and never overwritten by later generations. Unlike a field assertion, an
// older binary does not know its name and therefore cannot weaken it while
// reapplying its own schema. The synchronous THROW rolls the attempted retired
// writer mutation back in the same transaction. The compound index is likewise
// generation-named so IF NOT EXISTS is an idempotent one-time build.
var evidencePreMigrationSchema = fmt.Sprintf(`
DEFINE EVENT IF NOT EXISTS %s ON TABLE extraction_run
	WHEN $event != 'DELETE'
	  AND $after.store_schema_version IN
		['t12-store-v1', 't12-store-v2', 't12-store-v3', 't12-store-v4',
		 't12-store-v5', 't12-store-v6', '%s']
	THEN {
		THROW 'phebs-permanent: retired evidence writer generation'
	};
DEFINE INDEX IF NOT EXISTS %s ON TABLE assertion
	FIELDS run_id, predicate, object, repo, lineage, subject, assertion_id;`,
	evidenceWriterGuardEvent, evidencePreviousStoreSchemaVersion, reverseAssertionIndexName)

var evidenceIndexes = fmt.Sprintf(`
DEFINE FIELD OVERWRITE status ON extraction_run TYPE string
    ASSERT $value INSIDE ['staged', 'published', 'superseded', 'aborted', 'deleting']
        OR $this.evidence_format_version != '%s';
DEFINE FIELD OVERWRITE store_schema_version ON extraction_run TYPE string
	ASSERT $value NOT IN
		['t12-store-v1', 't12-store-v2', 't12-store-v3', 't12-store-v4',
		 't12-store-v5', 't12-store-v6', '%s'];
DEFINE INDEX IF NOT EXISTS extraction_run_published_key ON extraction_run FIELDS published_key UNIQUE;
DEFINE FIELD OVERWRITE status ON extraction_attempt TYPE string
    ASSERT $value INSIDE ['staged', 'published', 'aborted'];`,
	evidenceFormatVersion, evidencePreviousStoreSchemaVersion)

// migrateLegacyJobs runs before the pending-key indexes are installed. Old
// rows had no lease or pending slot, and may contain an active job plus a
// successor. Keep the oldest pending row, cancel duplicates, then requeue only
// an unfenced active row that has no successor.
func (s *Surreal) migrateLegacyJobs(ctx context.Context) error {
	for _, kind := range []JobKind{
		JobSync, JobIndex, JobFetch, JobCandidate, JobExtract, JobInvestigate,
	} {
		jobs, err := s.ListJobs(ctx, kind, "")
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
	return nil
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
	Domain      any              `json:"domain"`
	Extractor   any              `json:"extractor"`
	Status      any              `json:"status"`
	StartedAt   any              `json:"started_at"`
	StoreSchema any              `json:"store_schema_version"`
	Format      any              `json:"evidence_format_version"`
}

func validMigrationIdentity(value any) (string, bool) {
	text, ok := migrationString(value)
	return text, ok && strings.TrimSpace(text) != "" && utf8.ValidString(text) &&
		len(text) <= maxEvidenceIdentityBytes
}

// migrateEvidenceAttempts preserves the immediately preceding writer's
// latest-attempt diagnostics. The old writer had one row per repo/domain, so
// every compatible row belongs to the explicit whole-repository unit. No
// current repository state participates in that classification.
func (s *Surreal) migrateEvidenceAttempts(ctx context.Context) error {
	const retiredAttemptSchema = "t12-store-retired-v7-attempt"
	for {
		results, err := surrealdb.Query[[]evidenceAttemptMigrationRec](ctx, s.db,
			`SELECT id, run_id, repo, commit, domain, extractor, status, started_at,
				store_schema_version, evidence_format_version
			FROM extraction_attempt
			WHERE store_schema_version = $previous_schema
			ORDER BY id LIMIT $limit`, map[string]any{
				"previous_schema": evidencePreviousStoreSchemaVersion,
				"limit":           evidenceMigrationBatchSize,
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
			runID, runOK := validMigrationIdentity(row.RunID)
			repo, repoOK := validMigrationIdentity(row.Repo)
			commit, commitOK := validMigrationIdentity(row.Commit)
			domain, domainOK := validMigrationIdentity(row.Domain)
			extractor, extractorOK := validMigrationIdentity(row.Extractor)
			status, statusOK := migrationString(row.Status)
			format, formatOK := migrationString(row.Format)
			if !runOK || !repoOK || !commitOK || !domainOK || !extractorOK ||
				!statusOK || (status != "staged" && status != "published" && status != "aborted") ||
				!formatOK || format != evidenceFormatVersion || row.StartedAt == nil {
				if _, err := surrealdb.Query[any](ctx, s.db,
					`UPDATE $rid SET store_schema_version = $retired_schema,
						evidence_migration_version = $migration RETURN NONE`,
					map[string]any{
						"rid": *row.RecID, "retired_schema": retiredAttemptSchema,
						"migration": evidenceMigrationVersion,
					}); err != nil {
					return fmt.Errorf("retire malformed attempt: %w", err)
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
					WHERE store_schema_version = $previous_schema LIMIT 1) = 1
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
					"previous_schema":            evidencePreviousStoreSchemaVersion,
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
				return fmt.Errorf("move attempt for %s: destination collision or source changed", repo)
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
	return !present || schema == "" || schema == "t12-store-v1" || schema == "t12-store-v2" ||
		schema == "t12-store-v3" || schema == "t12-store-v4" || schema == "t12-store-v5" ||
		schema == "t12-store-v6"
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
			   OR (store_schema_version = $previous_schema
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
								OR retention_phase NOT IN ['associations', 'assertions', 'finalize']))
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
				"previous_schema": evidencePreviousStoreSchemaVersion,
				"legacy_schemas": []string{"t12-store-v1", "t12-store-v2", "t12-store-v3",
					"t12-store-v4", "t12-store-v5", "t12-store-v6"},
				"format":    evidenceFormatVersion,
				"migration": evidenceMigrationVersion,
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
			if !legacy && schema != evidencePreviousStoreSchemaVersion && schema != evidenceStoreSchemaVersion {
				return fmt.Errorf("migrate evidence run %s: candidate has unknown writer schema", runID)
			}

			oldRunID, oldRunIDPresent := migrationString(row.RunID)
			oldRunIDUsable := oldRunIDPresent && oldRunID != "" && utf8.ValidString(oldRunID) &&
				len(oldRunID) <= maxEvidenceIdentityBytes
			rewriteRunID := oldRunIDUsable && oldRunID != runID

			format, formatPresent := migrationString(row.Format)
			knownWriter := schema == evidencePreviousStoreSchemaVersion || schema == evidenceStoreSchemaVersion
			malformedFormat := knownWriter && ((formatPresent && format == "") ||
				(!formatPresent && row.Format != nil))
			unitDigest := ""
			if schema == evidenceStoreSchemaVersion {
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
			} else if !statusPresent || !validEvidenceRunStatus(status) ||
				(status == "deleting" && (schema != evidenceStoreSchemaVersion ||
					!retentionPhasePresent ||
					(retentionPhase != "associations" && retentionPhase != "assertions" &&
						retentionPhase != "finalize"))) {
				status = "aborted"
				retentionPhase = ""
				quarantined = true
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
UPDATE extraction_job SET status = 'canceled', error = 'repository deleting',
    finished_at = time::now(), not_before = NONE, pending_key = NONE
    WHERE target = $name AND status = 'pending' RETURN NONE;
UPDATE candidate_manifest_job SET status = 'canceled', error = 'repository deleting',
    finished_at = time::now(), not_before = NONE, pending_key = NONE
    WHERE target = $name AND status = 'pending' RETURN NONE;
DELETE candidate_manifest_publication WHERE repository = $name RETURN NONE;
DELETE repo_permission WHERE repo = $name RETURN NONE;
DELETE repo_connection WHERE repo = $name RETURN NONE;
DELETE $rid RETURN NONE;
COMMIT;`, map[string]any{"rid": repoID(name), "name": name})
		if err != nil && isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
			continue
		}
		return err
	}
}

func (s *Surreal) SetRepoDeleting(ctx context.Context, name string, deleting bool) error {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		"UPDATE $rid SET deleting = $deleting RETURN AFTER",
		map[string]any{"rid": repoID(name), "deleting": deleting})
	if err != nil {
		return err
	}
	if len((*results)[0].Result) == 0 {
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
};
LET $final = IF $identity_changed THEN
	(UPDATE $rid SET evidence_revision = (evidence_revision ?? 0) + 1
		RETURN AFTER)
	ELSE $updated END;
RETURN $final;
COMMIT;`
	vars := map[string]any{
		"rid": repoID(name), "name": name, "hash": defaultCommit,
		"revisions": revisions, "at": at, "unit_digest": "",
		"publication_rid":             candidateManifestPublicationID(name),
		"evidence_store_schema":       evidenceStoreSchemaVersion,
		"evidence_format":             evidenceFormatVersion,
		"evidence_migration":          evidenceMigrationVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	if unit != nil {
		statement = `BEGIN;
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
};
LET $final = IF $identity_changed THEN
	(UPDATE $rid SET evidence_revision = (evidence_revision ?? 0) + 1
		RETURN AFTER)
	ELSE $updated END;
RETURN $final;
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
LET $before = (SELECT indexed_commit_hash, indexed_analysis_unit FROM $rid)[0];
LET $publication = (SELECT id FROM $publication_rid)[0];
LET $visibility_changed = ($before != NONE
	AND (($before.indexed_commit_hash ?? '') != ''
		OR $before.indexed_analysis_unit != NONE))
	OR $publication != NONE;
LET $updated = UPDATE $rid SET indexed_commit_hash = NONE, indexed_revisions = NONE,
	indexed_analysis_unit = NONE, indexed_at = NONE RETURN AFTER;
IF array::len($updated) = 1 {
	DELETE $publication_rid RETURN NONE
};
LET $final = IF array::len($updated) = 1 AND $visibility_changed THEN
	(UPDATE $rid SET evidence_revision = (evidence_revision ?? 0) + 1
		RETURN AFTER)
	ELSE $updated END;
RETURN $final;
COMMIT;`,
		map[string]any{
			"rid":             repoID(name),
			"publication_rid": candidateManifestPublicationID(name),
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

// RepoStatuses joins repos, membership, and latest indexing jobs in Go.
// ponytail: three queries + maps, no server-side joins; revisit if repo
// counts make it slow.
func (s *Surreal) RepoStatuses(ctx context.Context) ([]RepoStatus, error) {
	repos, err := s.ListRepos(ctx)
	if err != nil {
		return nil, err
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
	jobs, err := s.ListJobs(ctx, JobIndex, "")
	if err != nil {
		return nil, err
	}
	latest := map[string]*Job{}
	for i := range jobs {
		j := jobs[i]
		if cur, ok := latest[j.Target]; !ok || j.CreatedAt.After(cur.CreatedAt) {
			latest[j.Target] = &j
		}
	}

	statuses := make([]RepoStatus, len(repos))
	for i, r := range repos {
		statuses[i] = RepoStatus{
			Repo:         r,
			Connections:  conns[r.Name],
			Orphaned:     len(conns[r.Name]) == 0,
			LastIndexJob: latest[r.Name],
			AnalysisUnit: analysisunit.CloneState(r.IndexedAnalysisUnit),
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

func (s *Surreal) CreateJob(ctx context.Context, kind JobKind, target string) (*Job, error) {
	created := time.Now().UTC()
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`CREATE type::table($table) CONTENT {
			target: $target, status: 'pending', attempts: 0,
			created_at: $created, pending_key: $target, force: false
		}`,
		map[string]any{"table": string(kind), "target": target, "created": created})
	if err != nil {
		return nil, err
	}
	rows := (*results)[0].Result
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
RETURN IF $pending != NONE THEN
    (UPDATE $pending SET force = IF $force THEN true ELSE force END RETURN AFTER)
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
COMMIT;`

const maxQueueRetries = 64

// EnqueuePending atomically ensures that target has one pending job. An
// already-running job is deliberately ignored: the pending row is its single
// successor, preserving events that arrive after the worker took its snapshot.
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

func (s *Surreal) ListJobs(ctx context.Context, kind JobKind, status JobStatus) ([]Job, error) {
	sql := "SELECT * FROM type::table($table) ORDER BY created_at"
	vars := map[string]any{"table": string(kind)}
	if status != "" {
		sql = "SELECT * FROM type::table($table) WHERE status = $status ORDER BY created_at"
		vars["status"] = string(status)
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, sql, vars)
	if err != nil {
		return nil, err
	}
	rows := (*results)[0].Result
	jobs := make([]Job, len(rows))
	for i, r := range rows {
		jobs[i] = r.toJob(kind)
	}
	return jobs, nil
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
     claimed_at = time::now(), heartbeat_at = time::now()
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

// ReleaseJob is the shutdown path: work returns to pending immediately without
// being counted as a failed attempt.
func (s *Surreal) ReleaseJob(ctx context.Context, job Job, errMsg string) error {
	return s.returnToPending(ctx, job, errMsg, time.Time{}, false, nil)
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
        not_before = IF $increment THEN $nb ELSE NONE END
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
        lease_token = NONE, finished_at = NONE, pending_key = $target
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

// ReapStale rescues jobs whose worker died: claimed/running rows without a
// recent heartbeat go back to pending (attempts+1), or to failed once
// maxAttempts is exhausted. Returns how many rows it touched.
// ponytail: staleness cutoff computed on the Go clock vs server-side
// heartbeats — same host today (supervised child); revisit for fleet mode.
func (s *Surreal) ReapStale(ctx context.Context, kind JobKind, staleAfter time.Duration, maxAttempts int) (int, error) {
	cutoff := time.Now().UTC().Add(-staleAfter)
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`SELECT * FROM type::table($table)
		 WHERE status IN ['claimed', 'running'] AND heartbeat_at != NONE AND heartbeat_at < $cutoff
		 ORDER BY heartbeat_at`,
		map[string]any{"table": string(kind), "cutoff": cutoff})
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
