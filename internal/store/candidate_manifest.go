package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/reponame"
)

// CandidateManifestPublication is the strict database pointer to one
// commit-bound filesystem publication. ManifestPath is relative to the
// candidate artifact root so restoring the database never binds a new
// installation to the old data directory.
//
// UnitDigest is empty for an unscoped repository. All other digest fields use
// canonical "sha256:<lowercase hex>" encoding.
type CandidateManifestPublication struct {
	Repository       string `json:"repository"`
	HeadCommit       string `json:"head_commit"`
	UnitDigest       string `json:"unit_digest"`
	PolicyDigest     string `json:"policy_digest"`
	ManifestDigest   string `json:"manifest_digest"`
	GenerationDigest string `json:"generation_digest"`
	ManifestPath     string `json:"manifest_path"`
	// ControlRevision advances only after a strict validated publication
	// transition. Zero on input asks the store to preserve an exact pointer or
	// assign the next revision; persisted pointers always carry a non-zero
	// value.
	ControlRevision uint64    `json:"control_revision"`
	PublishedAt     time.Time `json:"published_at"`
}

// CandidateManifestPublicationStore is deliberately narrower than Store.
// Candidate planning can depend on this capability without widening every
// sync/search/store test double.
type CandidateManifestPublicationStore interface {
	GetCandidateManifestPublication(ctx context.Context, repository string) (*CandidateManifestPublication, error)
	PublishCandidateManifest(ctx context.Context, publication CandidateManifestPublication) error
	ClearCandidateManifestPublication(ctx context.Context, repository string) error
	ListCandidateManifestPublications(ctx context.Context) ([]CandidateManifestPublication, error)
}

// CandidateControlOutcomeStore is the optional restart-safe repair signal used
// by the candidate worker. Keeping it separate avoids widening consumers that
// only resolve publication pointers.
type CandidateControlOutcomeStore interface {
	CandidateControlRepairNeeded(
		ctx context.Context,
		publication CandidateManifestPublication,
	) (bool, error)
}

var _ CandidateManifestPublicationStore = (*Surreal)(nil)
var _ CandidateControlOutcomeStore = (*Surreal)(nil)

type candidateManifestPublicationRec struct {
	CandidateManifestPublication
	RecID *models.RecordID `json:"id"`
}

func candidateManifestPublicationID(repository string) models.RecordID {
	return models.NewRecordID("candidate_manifest_publication", repository)
}

func validateCandidateManifestPublication(publication CandidateManifestPublication, requireTimestamp bool) error {
	if err := validateCandidateRepository(publication.Repository); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if !validGitObjectID(publication.HeadCommit) {
		return errors.New("head_commit must be a canonical SHA-1 or SHA-256 object ID")
	}
	if publication.UnitDigest != "" && !validSHA256Digest(publication.UnitDigest) {
		return errors.New("unit_digest must be empty or canonical sha256")
	}
	if !validSHA256Digest(publication.PolicyDigest) {
		return errors.New("policy_digest must be canonical sha256")
	}
	if !validSHA256Digest(publication.ManifestDigest) {
		return errors.New("manifest_digest must be canonical sha256")
	}
	if !validSHA256Digest(publication.GenerationDigest) {
		return errors.New("generation_digest must be canonical sha256")
	}
	if publication.ManifestPath != candidateid.ManifestName(publication.Repository) {
		return errors.New("manifest_path must be the stable repository manifest name")
	}
	if requireTimestamp && publication.ControlRevision == 0 {
		return errors.New("control_revision is required")
	}
	if requireTimestamp && publication.PublishedAt.IsZero() {
		return errors.New("published_at is required")
	}
	return nil
}

func validateCandidateRepository(repository string) error {
	if err := reponame.Validate(repository); err != nil {
		return errors.New("must be a bounded canonical repository name")
	}
	return nil
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

const publishCandidateManifestSQL = `
BEGIN;
LET $repo_state = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $current = (SELECT * FROM $publication_rid)[0];
LET $repo_ok = $repo_state != NONE
	AND ($repo_state.deleting = NONE OR $repo_state.deleting = false)
	AND $repo_state.indexed_commit_hash = $head_commit
	AND ($repo_state.indexed_analysis_unit.digest ?? '') = $unit_digest;
LET $same_scope = $current != NONE
	AND $current.repository = $repository
	AND $current.head_commit = $head_commit
	AND $current.unit_digest = $unit_digest
	AND $current.policy_digest = $policy_digest;
LET $same_publication = $same_scope
	AND $current.manifest_digest = $manifest_digest
	AND $current.generation_digest = $generation_digest
	AND $current.manifest_path = $manifest_path;
LET $current_control_revision = $current.control_revision ?? 0;
LET $control_advanced = $same_publication
	AND $requested_control_revision = $current_control_revision + 1;
LET $control_ok = IF $current = NONE THEN
		$requested_control_revision IN [0, 1]
	ELSE IF $same_publication THEN
		$requested_control_revision = 0
			OR $requested_control_revision = $current_control_revision
			OR $control_advanced
	ELSE
		$requested_control_revision = 0
			OR $requested_control_revision = $current_control_revision + 1
	END;
LET $next_control_revision = IF $current = NONE THEN 1
	ELSE IF $same_publication AND $control_advanced = false
		THEN $current_control_revision
	ELSE $current_control_revision + 1 END;
LET $acceptable = $repo_ok
	AND ($same_scope = false OR $same_publication = true)
	AND $control_ok;
LET $published = IF $acceptable = false THEN []
	ELSE IF $same_publication = true AND $control_advanced = false THEN
		[$current]
	ELSE
		(UPSERT $publication_rid SET
			repository = $repository,
			head_commit = $head_commit,
			unit_digest = $unit_digest,
			policy_digest = $policy_digest,
			manifest_digest = $manifest_digest,
			generation_digest = $generation_digest,
			manifest_path = $manifest_path,
			control_revision = $next_control_revision,
			published_at = $published_at
			RETURN AFTER)
	END;
LET $retired_catalog = IF array::len($published) = 1
		AND ($same_publication = false OR $control_advanced) THEN
	(DELETE resolver_catalog_publication
		WHERE repository = $repository RETURN BEFORE)
	ELSE [] END;
LET $invalidated_runs = (IF array::len($published) = 1 THEN
	(SELECT id, run_id, domain FROM extraction_run
		WHERE repo = $repository AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND status IN ['published', 'staged']
			AND (status = 'published' OR $same_publication = false)
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND (coverage.candidate_manifest_digest ?? '') != $manifest_digest)
	ELSE [] END) ?? [];
LET $invalidated_run_rids = $invalidated_runs.map(|$run| $run.id);
LET $invalidated_run_ids = $invalidated_runs.map(|$run| $run.run_id);
LET $invalidated_domains = $invalidated_runs.map(|$run| $run.domain);
LET $invalidated_attempts = (IF array::len($invalidated_runs) > 0 THEN
	(SELECT id, run_id, domain FROM extraction_attempt
		WHERE repo = $repository AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND domain IN $invalidated_domains
			AND run_id IN $invalidated_run_ids
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND evidence_migration_version = $evidence_migration)
	ELSE [] END) ?? [];
LET $invalidated_attempt_rids = $invalidated_attempts.map(|$attempt| $attempt.id);
LET $retired = IF array::len($invalidated_runs) > 0 THEN
	(UPDATE extraction_run SET status = 'superseded', published_key = NONE
		WHERE id IN $invalidated_run_rids
			AND repo = $repository AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND domain IN $invalidated_domains
			AND run_id IN $invalidated_run_ids
			AND status = 'published'
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
		RETURN AFTER)
	ELSE [] END;
LET $aborted = IF array::len($invalidated_runs) > 0 THEN
	(UPDATE extraction_run SET status = 'aborted', published_key = NONE
		WHERE id IN $invalidated_run_rids
			AND repo = $repository AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND domain IN $invalidated_domains
			AND run_id IN $invalidated_run_ids
			AND status = 'staged'
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
		RETURN AFTER)
	ELSE [] END;
LET $cleared_attempts = IF array::len($invalidated_attempt_rids) > 0 THEN
	(DELETE extraction_attempt
		WHERE id IN $invalidated_attempt_rids
			AND repo = $repository AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND domain IN $invalidated_domains
			AND run_id IN $invalidated_run_ids
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND evidence_migration_version = $evidence_migration
		RETURN BEFORE)
	ELSE [] END;
LET $cleared_control_outcomes = IF array::len($published) = 1
		AND $control_advanced THEN
	(DELETE extraction_domain_outcome
		WHERE repo = $repository
			AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND candidate_control_failure = true
			AND generation.candidate_manifest_digest = $manifest_digest
			AND generation.candidate_policy_digest = $policy_digest
			AND generation.candidate_control_revision =
				$current_control_revision
			AND store_schema_version = $evidence_store_schema
			AND evidence_migration_version = $evidence_migration
		RETURN BEFORE)
	ELSE [] END;
LET $evidence_changed = array::len($retired) > 0
	OR array::len($aborted) > 0
	OR array::len($cleared_attempts) > 0
	OR (array::len($published) = 1 AND $same_publication = false);
LET $evidence_revision = IF $evidence_changed THEN
	(UPDATE $repo_rid SET evidence_revision = (evidence_revision ?? 0) + 1
		RETURN AFTER)
	ELSE [] END;
LET $pending = IF array::len($published) = 1 THEN
	(SELECT id, created_at FROM extraction_job
		WHERE pending_key = $repository AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $fanout = IF array::len($published) != 1 THEN []
	ELSE IF $pending != NONE THEN
		(UPDATE $pending SET force = force RETURN AFTER)
	ELSE
		(CREATE extraction_job CONTENT {
			target: $repository,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $repository,
			force: false
		} RETURN AFTER)
	END;
LET $pending_catalog = IF array::len($published) = 1 THEN
	(SELECT id, created_at FROM resolver_catalog_job
		WHERE pending_key = $repository AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $catalog_force = ($same_publication = false) OR $control_advanced;
LET $catalog_fanout = IF array::len($published) != 1 THEN []
	ELSE IF $pending_catalog != NONE THEN
		(UPDATE $pending_catalog SET
			force = IF $catalog_force THEN true ELSE force END
			RETURN AFTER)
	ELSE
		(CREATE resolver_catalog_job CONTENT {
			target: $repository,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $repository,
			force: $catalog_force
		} RETURN AFTER)
	END;
RETURN IF array::len($fanout) = 1
	AND array::len($catalog_fanout) = 1
	THEN $published ELSE [] END;
COMMIT;`

// PublishCandidateManifest atomically guards publication against the current
// authoritative indexed HEAD and committed unit, advances the pointer, and
// ensures pending extraction and resolver-catalog successors. Candidate
// generation/control transitions force resolver replacement; an exact retry
// repairs a missing non-forced successor without downgrading an existing
// forced request. Any
// published or staged evidence for the same semantic scope but a different
// candidate-manifest digest is atomically retired with its latest-attempt row;
// historical rows remain for retention and pinning, but cannot satisfy a
// current consumer. Retrying the exact pointer keeps its original PublishedAt
// and matching evidence while repairing missing successors. A different
// manifest for the same HEAD/unit/policy is rejected as planner nondeterminism.
func (s *Surreal) PublishCandidateManifest(
	ctx context.Context,
	publication CandidateManifestPublication,
) error {
	// Receipt time belongs to the store, not the planner.
	publication.PublishedAt = time.Time{}
	if err := validateCandidateManifestPublication(publication, false); err != nil {
		return fmt.Errorf("publish candidate manifest: %w", err)
	}
	vars := map[string]any{
		"repo_rid":                    repoID(publication.Repository),
		"publication_rid":             candidateManifestPublicationID(publication.Repository),
		"repository":                  publication.Repository,
		"head_commit":                 publication.HeadCommit,
		"unit_digest":                 publication.UnitDigest,
		"policy_digest":               publication.PolicyDigest,
		"manifest_digest":             publication.ManifestDigest,
		"generation_digest":           publication.GenerationDigest,
		"manifest_path":               publication.ManifestPath,
		"requested_control_revision":  publication.ControlRevision,
		"published_at":                storeTimestamp(time.Now()),
		"evidence_store_schema":       evidenceStoreSchemaVersion,
		"evidence_format":             evidenceFormatVersion,
		"evidence_migration":          evidenceMigrationVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]candidateManifestPublicationRec](
			ctx, s.db, publishCandidateManifestSQL, vars,
		)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("publish candidate manifest: %w", err)
		}
		rows := firstDomainRows(results)
		if len(rows) == 1 {
			if err := validateCandidateManifestPublication(
				rows[0].CandidateManifestPublication, true,
			); err != nil {
				return fmt.Errorf("publish candidate manifest: persisted pointer: %w", err)
			}
			return nil
		}
		return fmt.Errorf(
			"publish candidate manifest: repository state is stale or immutable publication conflicts: %w",
			ErrConflict,
		)
	}
}

func (s *Surreal) GetCandidateManifestPublication(
	ctx context.Context,
	repository string,
) (*CandidateManifestPublication, error) {
	if err := validateCandidateRepository(repository); err != nil {
		return nil, fmt.Errorf("get candidate manifest: repository: %w", err)
	}
	results, err := surrealdb.Query[[]candidateManifestPublicationRec](
		ctx,
		s.db,
		"SELECT * FROM $rid",
		map[string]any{"rid": candidateManifestPublicationID(repository)},
	)
	if err != nil {
		return nil, fmt.Errorf("get candidate manifest: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("candidate manifest for %q: %w", repository, ErrNotFound)
	}
	publication := rows[0].CandidateManifestPublication
	if publication.Repository != repository {
		return nil, fmt.Errorf(
			"get candidate manifest: %w: persisted repository identity is inconsistent",
			ErrInvalidCandidateManifestPublication,
		)
	}
	if err := validateCandidateManifestPublication(publication, true); err != nil {
		return nil, fmt.Errorf(
			"get candidate manifest: %w: %v",
			ErrInvalidCandidateManifestPublication, err,
		)
	}
	publication.PublishedAt = publication.PublishedAt.UTC()
	return &publication, nil
}

func (s *Surreal) ListCandidateManifestPublications(
	ctx context.Context,
) ([]CandidateManifestPublication, error) {
	results, err := surrealdb.Query[[]candidateManifestPublicationRec](
		ctx, s.db,
		"SELECT * FROM candidate_manifest_publication ORDER BY repository",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("list candidate manifests: %w", err)
	}
	rows := firstDomainRows(results)
	publications := make([]CandidateManifestPublication, len(rows))
	for index := range rows {
		publication := rows[index].CandidateManifestPublication
		if err := validateCandidateManifestPublication(publication, true); err != nil {
			return nil, fmt.Errorf(
				"list candidate manifests: row %d: %w: %v",
				index, ErrInvalidCandidateManifestPublication, err,
			)
		}
		publication.PublishedAt = publication.PublishedAt.UTC()
		publications[index] = publication
	}
	return publications, nil
}

func (s *Surreal) ClearCandidateManifestPublication(
	ctx context.Context,
	repository string,
) error {
	if err := validateCandidateRepository(repository); err != nil {
		return fmt.Errorf("clear candidate manifest: repository: %w", err)
	}
	_, err := surrealdb.Query[any](
		ctx,
		s.db,
		`BEGIN;
		LET $current = SELECT id FROM $rid;
		LET $retired_catalog = IF array::len($current) = 1 THEN
			(DELETE resolver_catalog_publication
				WHERE repository = $repository RETURN BEFORE)
			ELSE [] END;
		IF array::len($current) = 1 {
			DELETE $rid RETURN NONE;
			DELETE extraction_domain_outcome
				WHERE repo = $repository RETURN NONE;
			UPDATE $repo_rid
				SET evidence_revision = (evidence_revision ?? 0) + 1
				WHERE name = $repository
				RETURN NONE;
		};
		LET $pending_catalog = IF array::len($retired_catalog) = 1 THEN
			(SELECT id, created_at FROM resolver_catalog_job
				WHERE pending_key = $repository AND status = 'pending'
				ORDER BY created_at LIMIT 1)[0].id
			ELSE NONE END;
		LET $catalog_fanout = IF array::len($retired_catalog) != 1 THEN []
			ELSE IF $pending_catalog != NONE THEN
				(UPDATE $pending_catalog SET force = true RETURN AFTER)
			ELSE
				(CREATE resolver_catalog_job CONTENT {
					target: $repository,
					status: 'pending',
					attempts: 0,
					created_at: time::now(),
					pending_key: $repository,
					force: true
				} RETURN AFTER)
			END;
		IF array::len($retired_catalog) = 1
			AND array::len($catalog_fanout) != 1 {
			THROW 'phebs-retryable: resolver catalog successor was not persisted'
		};
		COMMIT;`,
		map[string]any{
			"rid":        candidateManifestPublicationID(repository),
			"repo_rid":   repoID(repository),
			"repository": repository,
		},
	)
	if err != nil {
		return fmt.Errorf("clear candidate manifest: %w", err)
	}
	return nil
}

// ClearAllCandidateManifestPublications removes the derived pointer table
// without decoding its rows. Restore must be able to discard even a malformed
// imported pointer because candidate publications are never backup authority.
// Ordinary durable outcomes remain precious restored state. Control-failure
// outcomes are discarded because rebuilding starts a fresh control lineage;
// retaining an old revision would let a restored revision-1 refusal become
// authoritative over newly rebuilt bytes.
func (s *Surreal) ClearAllCandidateManifestPublications(
	ctx context.Context,
) error {
	_, err := surrealdb.Query[any](
		ctx,
		s.db,
		`BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
IF $writer_ok = false {
	THROW 'phebs-permanent: evidence writer generation is not active'
};
DELETE candidate_manifest_publication RETURN NONE;
DELETE resolver_catalog_publication RETURN NONE;
DELETE extraction_domain_outcome
	WHERE candidate_control_failure = true
		AND store_schema_version = $store_schema_version
		AND evidence_migration_version = $evidence_migration_version
	RETURN NONE;
COMMIT;`,
		map[string]any{
			"migration_rid":              evidenceMigrationStateID(),
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		},
	)
	if err != nil {
		return fmt.Errorf("clear all candidate manifests: %w", err)
	}
	return nil
}
