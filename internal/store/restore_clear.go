package store

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const restoreClearRows = 512

type restoreClearFamily uint8

const (
	restoreClearSchedules restoreClearFamily = iota + 1
	restoreClearCaller
	restoreClearCandidate
	restoreClearResolver
)

// These are exclusively the offline restore clears. A failed restore already
// retains its target and refuses another import over it. Each committed prefix
// therefore remains unavailable until the owning recovery workflow succeeds;
// this is not an online, all-tables atomic clear.
func (s *Surreal) clearRestoreState(ctx context.Context, family restoreClearFamily) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	guard, vars, err := restoreClearGuard(family)
	if err != nil {
		return err
	}
	if family == restoreClearSchedules {
		if err := s.resetRestoreRepoProjections(ctx); err != nil {
			return err
		}
		for _, table := range []string{
			"generation_schedule_current", "generation_schedule_repository",
			"generation_schedule_chunk", "generation_schedule",
			"service_state_v3_plan", "extraction_domain_root",
		} {
			if err := s.clearRestoreTable(ctx, table, "", guard, vars); err != nil {
				return err
			}
		}
		return nil
	}
	if err := s.clearRestoreCallerPointers(ctx, guard, vars); err != nil {
		return err
	}
	switch family {
	case restoreClearCaller:
		for _, table := range []string{"caller_leaf_outcome", "caller_generation_admission"} {
			if err := s.clearRestoreTable(ctx, table, "", guard, vars); err != nil {
				return err
			}
		}
	case restoreClearCandidate:
		for _, table := range []string{"candidate_manifest_publication", "resolver_catalog_publication"} {
			if err := s.clearRestoreTable(ctx, table, "", guard, vars); err != nil {
				return err
			}
		}
		return s.clearRestoreTable(ctx, "extraction_domain_outcome", `
 WHERE candidate_control_failure = true
 AND store_schema_version = $store_schema_version
 AND evidence_migration_version = $evidence_migration_version`, guard, vars)
	case restoreClearResolver:
		return s.clearRestoreTable(ctx, "resolver_catalog_publication", "", guard, vars)
	}
	return nil
}

func restoreClearGuard(family restoreClearFamily) (string, map[string]any, error) {
	vars := map[string]any{}
	if family == restoreClearSchedules {
		return "", vars, nil
	}
	vars["caller_migration_rid"] = callerGenerationPublicationMigrationID()
	vars["caller_migration_version"] = callerGenerationPublicationMigrationVersion
	var marker models.RecordID
	var version, message string
	switch family {
	case restoreClearCaller:
		marker, version = callerLeafMigrationID(), callerLeafWriterMigrationVersion
		message = "caller publication writer generation is not active"
	case restoreClearCandidate:
		marker, version = evidenceMigrationStateID(), evidenceMigrationVersion
		vars["store_schema_version"] = evidenceStoreSchemaVersion
		vars["evidence_migration_version"] = evidenceMigrationVersion
		message = "evidence writer generation is not active"
	case restoreClearResolver:
		marker, version = resolverCatalogMigrationID(), resolverCatalogWriterMigrationVersion
		message = "resolver catalog writer generation is not active"
	default:
		return "", nil, errors.New("unknown restore clear family")
	}
	vars["migration_rid"], vars["migration_version"] = marker, version
	return `
LET $writer_ok = array::len(SELECT id FROM $migration_rid
 WHERE version = $migration_version LIMIT 1) = 1;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
 WHERE version = $caller_migration_version LIMIT 1) = 1;
IF !$writer_ok OR !$caller_writer_ok {
 THROW 'phebs-permanent: ` + message + `';
};
`, vars, nil
}

// table, predicate and guard come only from the closed recipes above. No row
// body is decoded, including for deliberately malformed imported publications.
func (s *Surreal) clearRestoreTable(
	ctx context.Context, table, predicate, guard string, vars map[string]any,
) error {
	selection := "SELECT VALUE id FROM " + table + predicate + " ORDER BY id LIMIT $limit"
	vars = maps.Clone(vars)
	vars["limit"] = restoreClearRows
	for {
		delete(vars, "ids")
		ids, err := s.restoreClearIDs(ctx, table, guard+selection+";", vars, restoreClearRows)
		if err != nil || len(ids) == 0 {
			return err
		}
		vars["ids"] = ids
		if err := s.restoreClearWrite(ctx, "BEGIN;"+guard+`
LET $actual = `+selection+`;
IF $actual != $ids OR array::len(array::distinct($ids)) != array::len($ids) {
 THROW 'phebs-permanent: restore clear page changed';
};
FOR $rid IN $ids { DELETE $rid RETURN NONE; };
COMMIT;`, vars); err != nil {
			return err
		}
	}
}

func (s *Surreal) restoreClearIDs(
	ctx context.Context, table, statement string, vars map[string]any, limit int,
) ([]models.RecordID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results, err := surrealdb.Query[[]models.RecordID](ctx, s.db, statement, vars)
	if err != nil {
		return nil, fmt.Errorf("read restore clear %s: %w", table, err)
	}
	ids := firstDomainRows(results)
	if err := validateRestoreClearIDs(ids, table, limit); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func validateRestoreClearIDs(ids []models.RecordID, table string, limit int) error {
	if limit < 1 || limit > restoreClearRows || len(ids) > limit {
		return errors.New("restore clear page exceeds its row limit")
	}
	for index := range ids {
		if ids[index].Table != table {
			return errors.New("restore clear record belongs to another table")
		}
		// Preserve every native ID shape supported by the SDK, rather than
		// parsing String(), which is ambiguous for composite/quoted IDs.
		if _, err := ids[index].MarshalCBOR(); err != nil {
			return fmt.Errorf("restore clear record identity: %w", err)
		}
	}
	return nil
}

func (s *Surreal) restoreClearWrite(ctx context.Context, statement string, vars map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := surrealdb.Query[any](ctx, s.db, statement, vars); err != nil {
		return fmt.Errorf("commit restore clear page: %w", err)
	}
	return nil
}

const restoreRepoProjectionFields = `latest_extraction_job, latest_extraction_job_created_at,
 latest_extraction_job_projection_version,
 latest_resolver_job, latest_resolver_job_created_at,
 latest_resolver_job_projection_version,
 latest_caller_job, latest_caller_job_created_at,
 latest_caller_job_projection_version`

func (s *Surreal) resetRestoreRepoProjections(ctx context.Context) error {
	vars := map[string]any{"after": models.None, "limit": restoreClearRows}
	const selection = `SELECT VALUE id FROM repo
 WHERE $after = NONE OR id > $after ORDER BY id LIMIT $limit`
	for {
		delete(vars, "ids")
		ids, err := s.restoreClearIDs(ctx, "repo", selection+";", vars, restoreClearRows)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		vars["ids"] = ids
		if err := s.restoreClearWrite(ctx, `BEGIN;
LET $actual = `+selection+`;
IF $actual != $ids OR array::len(array::distinct($ids)) != array::len($ids) {
 THROW 'phebs-permanent: restore repository page changed';
};
FOR $rid IN $ids { UPDATE $rid UNSET `+restoreRepoProjectionFields+` RETURN NONE; };
COMMIT;`, vars); err != nil {
			return err
		}
		vars["after"] = ids[len(ids)-1]
	}
	remaining, err := s.restoreClearIDs(ctx, "repo", `SELECT VALUE id FROM repo WHERE
 latest_extraction_job != NONE OR latest_extraction_job_created_at != NONE OR
 latest_extraction_job_projection_version != NONE OR latest_resolver_job != NONE OR
 latest_resolver_job_created_at != NONE OR latest_resolver_job_projection_version != NONE OR
 latest_caller_job != NONE OR latest_caller_job_created_at != NONE OR
 latest_caller_job_projection_version != NONE LIMIT 1;`, nil, 1)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return errors.New("restore repository projections remain")
	}
	return nil
}

type restoreCallerRepo struct {
	ID models.RecordID `json:"id"`
	// Raw witnesses preserve native scalar/composite values without narrowing
	// the old raw clear to Go string/int64 shapes. SQL compares and increments.
	Name     cbor.RawMessage `json:"name"`
	Revision cbor.RawMessage `json:"revision"`
}

func (s *Surreal) clearRestoreCallerPointers(ctx context.Context, guard string, vars map[string]any) error {
	const selection = `SELECT VALUE id FROM caller_generation_publication ORDER BY id LIMIT $limit`
	const repos = `SELECT id, name, (caller_publication_revision ?? 0) AS revision
 FROM repo WHERE name IN $keys ORDER BY id LIMIT $repo_limit`
	vars = maps.Clone(vars)
	vars["limit"], vars["repo_limit"] = restoreClearRows/2, restoreClearRows/2+1
	for {
		delete(vars, "ids")
		delete(vars, "keys")
		delete(vars, "repos")
		ids, err := s.restoreClearIDs(ctx, "caller_generation_publication", guard+selection+";", vars, restoreClearRows/2)
		if err != nil || len(ids) == 0 {
			return err
		}
		keys := make([]any, len(ids))
		for index := range ids {
			keys[index] = ids[index].ID
		}
		vars["ids"], vars["keys"] = ids, keys
		if err := ctx.Err(); err != nil {
			return err
		}
		results, err := surrealdb.Query[[]restoreCallerRepo](ctx, s.db, guard+repos+";", vars)
		if err != nil {
			return fmt.Errorf("read restore caller repositories: %w", err)
		}
		rows := firstDomainRows(results)
		if rows == nil {
			rows = []restoreCallerRepo{}
		}
		if len(rows) > restoreClearRows/2 || len(rows)+len(ids) > restoreClearRows {
			return errors.New("restore caller page exceeds its row limit")
		}
		for index := range rows {
			if err := validateRestoreClearIDs([]models.RecordID{rows[index].ID}, "repo", 1); err != nil {
				return err
			}
		}
		vars["repos"] = rows
		if err := s.restoreClearWrite(ctx, "BEGIN;"+guard+`
LET $actual = `+selection+`;
LET $actual_repos = `+repos+`;
IF $actual != $ids OR $actual_repos != $repos
 OR array::len(array::distinct($ids)) != array::len($ids) {
 THROW 'phebs-permanent: restore caller page changed';
};
FOR $repository IN $repos {
 UPDATE $repository.id SET caller_publication_revision = $repository.revision + 1 RETURN NONE;
};
FOR $rid IN $ids { DELETE $rid RETURN NONE; };
COMMIT;`, vars); err != nil {
			return err
		}
	}
}
