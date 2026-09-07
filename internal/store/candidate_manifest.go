package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/readaccounting"
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

type candidateManifestObservation struct {
	Before     []cbor.RawMessage  `json:"before" cbor:"before"`
	Active     *bool              `json:"active" cbor:"active"`
	Revision   *bool              `json:"revision" cbor:"revision"`
	Catalogs   []models.RecordID  `json:"catalogs" cbor:"catalogs"`
	Callers    []models.RecordID  `json:"callers" cbor:"callers"`
	Published  []models.RecordID  `json:"published" cbor:"published"`
	Staged     []models.RecordID  `json:"staged" cbor:"staged"`
	Attempts   []models.RecordID  `json:"attempts" cbor:"attempts"`
	Outcomes   []models.RecordID  `json:"outcomes" cbor:"outcomes"`
	Extraction []repoIndexPending `json:"extraction" cbor:"extraction"`
	Resolver   []repoIndexPending `json:"resolver" cbor:"resolver"`
}

func (c candidateManifestObservation) writeRows(clear bool) uint64 {
	rows := uint64(len(c.Catalogs) + len(c.Callers) + len(c.Published) + len(c.Staged) + len(c.Attempts) + len(c.Outcomes))
	if !clear {
		rows++ // The candidate UPSERT is supplied even when its native guard is false.
		if *c.Active {
			rows += 4 // Two selected job bodies and two projected repository RIDs.
		}
	} else if *c.Active {
		rows++ // The selected candidate DELETE.
	}
	if *c.Revision {
		rows++
	}
	if len(c.Callers) == 1 {
		rows++
	}
	if clear && len(c.Catalogs) == 1 {
		rows += 2
	}
	return rows
}

func (s *Surreal) candidateManifestCensus(ctx context.Context, sql string, controls int, vars map[string]any) (candidateManifestObservation, error) {
	results, err := storeQuery[[]candidateManifestObservation](ctx, s.accounting, s.db, sql+"RETURN [$candidate_census];", vars, storeRead())
	if err != nil {
		return candidateManifestObservation{}, err
	}
	rows, err := generationCensusRows(ctx, results, controls)
	if err != nil || len(rows) != 1 {
		return candidateManifestObservation{}, errors.Join(err, errors.New("invalid candidate mutation census"))
	}
	c := rows[0]
	if len(c.Before) != 2 || c.Active == nil || c.Revision == nil {
		return candidateManifestObservation{}, errors.New("invalid candidate mutation preimage")
	}
	for _, vector := range []struct {
		ids   []models.RecordID
		table string
	}{
		{c.Catalogs, "resolver_catalog_publication"}, {c.Callers, "caller_generation_publication"},
		{c.Published, "extraction_run"}, {c.Staged, "extraction_run"},
		{c.Attempts, "extraction_attempt"}, {c.Outcomes, "extraction_domain_outcome"},
	} {
		if vector.ids == nil {
			return candidateManifestObservation{}, errors.New("null candidate mutation vector")
		}
		if len(vector.ids) > restoreClearRows+1 {
			return candidateManifestObservation{}, errors.New("candidate mutation vector overflow")
		}
		prefix := min(len(vector.ids), restoreClearRows)
		if err := validateRestoreClearIDs(vector.ids[:prefix], vector.table, restoreClearRows); err != nil {
			return candidateManifestObservation{}, err
		}
		if len(vector.ids) > prefix {
			if err := validateRestoreClearIDs(vector.ids[prefix:], vector.table, 1); err != nil {
				return candidateManifestObservation{}, err
			}
		}
	}
	for _, pair := range []struct {
		values []repoIndexPending
		table  string
	}{
		{c.Extraction, string(JobExtract)}, {c.Resolver, string(JobResolverCatalog)},
	} {
		if pair.values == nil || len(pair.values) > 1 {
			return candidateManifestObservation{}, errors.New("invalid candidate successor census")
		}
		for _, value := range pair.values {
			if err := validateRestoreClearIDs([]models.RecordID{value.ID}, pair.table, 1); err != nil {
				return candidateManifestObservation{}, err
			}
			if err := validateRestoreClearIDs([]models.RecordID{value.Projection}, "repo", 1); err != nil {
				return candidateManifestObservation{}, err
			}
		}
	}
	return c, ctx.Err()
}

// The exact native observation is repeated in the write transaction. Returned
// identity counts are bounded; the original predicate scans and scalar values
// are not a response-byte, physical-scan, or engine-memory bound.
const candidateManifestCensusFenceSQL = `
IF $candidate_census != $candidate_expected
 OR array::len($candidate_expected.catalogs) != array::len(array::distinct($candidate_expected.catalogs))
 OR array::len($candidate_expected.callers) != array::len(array::distinct($candidate_expected.callers))
 OR array::len($candidate_expected.published) != array::len(array::distinct($candidate_expected.published))
 OR array::len($candidate_expected.staged) != array::len(array::distinct($candidate_expected.staged))
 OR array::len($candidate_expected.attempts) != array::len(array::distinct($candidate_expected.attempts))
 OR array::len($candidate_expected.outcomes) != array::len(array::distinct($candidate_expected.outcomes)) {
 THROW 'phebs-conflict: candidate mutation census changed';
};
`

const candidatePublishCensusControls = 28

const candidatePublishCensusSQL = `
LET $cm_caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
LET $cm_repo_state = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $cm_current = (SELECT * FROM $publication_rid)[0];
LET $cm_repo_ok = $cm_repo_state != NONE
	AND ($cm_repo_state.deleting = NONE OR $cm_repo_state.deleting = false)
	AND $cm_repo_state.indexed_commit_hash = $head_commit
	AND ($cm_repo_state.indexed_analysis_unit.digest ?? '') = $unit_digest;
LET $cm_same_scope = $cm_current != NONE
	AND $cm_current.repository = $repository
	AND $cm_current.head_commit = $head_commit
	AND $cm_current.unit_digest = $unit_digest
	AND $cm_current.policy_digest = $policy_digest;
LET $cm_same_publication = $cm_same_scope
	AND $cm_current.manifest_digest = $manifest_digest
	AND $cm_current.generation_digest = $generation_digest
	AND $cm_current.manifest_path = $manifest_path;
LET $cm_current_control_revision = $cm_current.control_revision ?? 0;
LET $cm_control_advanced = $cm_same_publication
	AND $requested_control_revision = $cm_current_control_revision + 1;
LET $cm_control_ok = IF $cm_current = NONE THEN
		$requested_control_revision IN [0, 1]
	ELSE IF $cm_same_publication THEN
		$requested_control_revision = 0
			OR $requested_control_revision = $cm_current_control_revision
			OR $cm_control_advanced
	ELSE
		$requested_control_revision = 0
			OR $requested_control_revision = $cm_current_control_revision + 1
	END;
LET $cm_next_control_revision = IF $cm_current = NONE THEN 1
	ELSE IF $cm_same_publication AND $cm_control_advanced = false
		THEN $cm_current_control_revision
	ELSE $cm_current_control_revision + 1 END;
LET $cm_acceptable = $cm_caller_writer_ok AND $cm_repo_ok
	AND ($cm_same_scope = false OR $cm_same_publication = true)
	AND $cm_control_ok;
LET $cm_published = IF $cm_acceptable THEN [$cm_current] ELSE [] END;
LET $cm_invalidated_runs = (IF array::len($cm_published) = 1 THEN
	(SELECT id, run_id, domain FROM extraction_run
		WHERE repo = $repository AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND status IN ['published', 'staged']
			AND (status = 'published' OR $cm_same_publication = false)
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND retention_quarantined = false
			AND run_id = record::id(id)
			AND (
				(((partition_plan_digest ?? '') = '')
					AND ((partition_active ?? false) = false)
					AND ((partition_sealed ?? false) = false)
					AND (coverage.candidate_manifest_digest ?? '') != $manifest_digest)
				OR (((partition_active ?? false) = true)
					AND ((partition_sealed ?? false) = false)
					AND (partition_candidate_digest ?? '') != $manifest_digest)
			))
	ELSE [] END) ?? [];
LET $cm_invalidated_run_rids = $cm_invalidated_runs.map(|$run| $run.id);
LET $cm_invalidated_run_ids = $cm_invalidated_runs.map(|$run| $run.run_id);
LET $cm_invalidated_domains = $cm_invalidated_runs.map(|$run| $run.domain);
LET $cm_invalidated_attempts = (IF array::len($cm_invalidated_runs) > 0 THEN
	(SELECT id, run_id, domain FROM extraction_attempt
		WHERE repo = $repository AND commit = $head_commit
			AND unit_digest = $unit_digest
			AND domain IN $cm_invalidated_domains
			AND run_id IN $cm_invalidated_run_ids
			AND store_schema_version = $evidence_store_schema
			AND evidence_format_version = $evidence_format
			AND evidence_migration_version = $evidence_migration)
	ELSE [] END) ?? [];
LET $cm_invalidated_attempt_rids = $cm_invalidated_attempts.map(|$attempt| $attempt.id);
LET $cm_retire = array::len($cm_published) = 1 AND ($cm_same_publication = false OR $cm_control_advanced);
LET $cm_catalogs = SELECT VALUE id FROM resolver_catalog_publication
 WHERE $cm_retire AND repository = $repository ORDER BY id LIMIT $candidate_limit;
LET $cm_callers = SELECT VALUE id FROM caller_generation_publication
 WHERE $cm_retire AND repository = $repository ORDER BY id LIMIT $candidate_limit;
LET $cm_published_ids = SELECT VALUE id FROM extraction_run
 WHERE id IN $cm_invalidated_run_rids AND status = 'published'
 AND ` + evidenceRunHasNoAmbiguousClaimantSQL + ` ORDER BY id LIMIT $candidate_limit;
LET $cm_staged_ids = SELECT VALUE id FROM extraction_run
 WHERE id IN $cm_invalidated_run_rids AND status = 'staged'
 AND ` + evidenceRunHasNoAmbiguousClaimantSQL + ` ORDER BY id LIMIT $candidate_limit;
LET $cm_attempt_ids = SELECT VALUE id FROM extraction_attempt
 WHERE id IN $cm_invalidated_attempt_rids ORDER BY id LIMIT $candidate_limit;
LET $cm_outcome_ids = SELECT VALUE id FROM extraction_domain_outcome
 WHERE array::len($cm_published) = 1 AND $cm_control_advanced
 AND repo = $repository AND commit = $head_commit AND unit_digest = $unit_digest
 AND candidate_control_failure = true
 AND generation.candidate_manifest_digest = $manifest_digest
 AND generation.candidate_policy_digest = $policy_digest
 AND generation.candidate_control_revision = $cm_current_control_revision
 AND store_schema_version = $evidence_store_schema
 AND evidence_migration_version = $evidence_migration ORDER BY id LIMIT $candidate_limit;
LET $cm_extraction = SELECT id, type::record('repo', target) AS projection FROM
 (SELECT id, created_at, target FROM extraction_job
 WHERE array::len($cm_published) = 1 AND pending_key = $repository AND status = 'pending' ORDER BY created_at LIMIT 1);
LET $cm_resolver = SELECT id, type::record('repo', target) AS projection FROM
 (SELECT id, created_at, target FROM resolver_catalog_job
 WHERE array::len($cm_published) = 1 AND pending_key = $repository AND status = 'pending' ORDER BY created_at LIMIT 1);
LET $candidate_census = {
 before: [$cm_repo_state, $cm_current], active: $cm_acceptable,
 revision: array::len($cm_published_ids) > 0 OR array::len($cm_staged_ids) > 0
   OR array::len($cm_attempt_ids) > 0 OR (array::len($cm_published) = 1 AND $cm_same_publication = false),
 catalogs: $cm_catalogs, callers: $cm_callers, published: $cm_published_ids, staged: $cm_staged_ids,
 attempts: $cm_attempt_ids, outcomes: $cm_outcome_ids, extraction: $cm_extraction, resolver: $cm_resolver
};
`

const candidatePublishLegacyFanoutSQL = `LET $pending = IF array::len($published) = 1 THEN
	(SELECT id, created_at FROM extraction_job
		WHERE pending_key = $repository AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
	LET $fanout = IF array::len($published) != 1 THEN []
	ELSE IF $pending != NONE THEN
		(UPDATE $pending SET force = force,
			recovery_lease = NONE RETURN AFTER)
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
			force = IF $catalog_force THEN true ELSE force END,
			recovery_lease = NONE
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
	END;` + projectExtractionJobSQL + projectResolverJobSQL + `
`

const candidateExtractionProjectionSQL = `
LET $extraction_projected = IF array::len($fanout) = 1
	THEN $fanout[0] ELSE NONE END;
IF $extraction_projected != NONE {
	UPDATE $candidate_extraction_projection
	SET latest_extraction_job = $extraction_projected.id,
		latest_extraction_job_created_at = $extraction_projected.created_at,
		latest_extraction_job_projection_version = 't40r1-extraction-job-latest-v1'
	WHERE latest_extraction_job_created_at = NONE
		OR latest_extraction_job_created_at < $extraction_projected.created_at
		OR (latest_extraction_job_created_at = $extraction_projected.created_at
			AND latest_extraction_job < $extraction_projected.id)
	RETURN NONE;
};`
const candidateResolverProjectionSQL = `
LET $resolver_projected = IF array::len($catalog_fanout) = 1
	THEN $catalog_fanout[0] ELSE NONE END;
IF $resolver_projected != NONE {
	UPDATE $candidate_resolver_projection
	SET latest_resolver_job = $resolver_projected.id,
		latest_resolver_job_created_at = $resolver_projected.created_at,
		latest_resolver_job_projection_version = 't40r1-resolver-job-latest-v1'
	WHERE latest_resolver_job_created_at = NONE
		OR latest_resolver_job_created_at < $resolver_projected.created_at
		OR (latest_resolver_job_created_at = $resolver_projected.created_at
			AND latest_resolver_job < $resolver_projected.id)
	RETURN NONE;
};`

func candidatePublishFanoutSQL(c *candidateManifestObservation) string {
	if c == nil {
		return candidatePublishLegacyFanoutSQL
	}
	if !*c.Active {
		return "LET $fanout = []; LET $catalog_fanout = [];\n"
	}
	extraction := `(CREATE extraction_job CONTENT {
			target: $repository, status: 'pending', attempts: 0,
			created_at: time::now(), pending_key: $repository, force: false
		} RETURN AFTER)`
	if len(c.Extraction) == 1 {
		extraction = "(UPDATE $pending SET force = force, recovery_lease = NONE RETURN AFTER)"
	}
	resolver := `(CREATE resolver_catalog_job CONTENT {
			target: $repository, status: 'pending', attempts: 0,
			created_at: time::now(), pending_key: $repository, force: $catalog_force
		} RETURN AFTER)`
	if len(c.Resolver) == 1 {
		resolver = `(UPDATE $pending_catalog SET force = IF $catalog_force THEN true ELSE force END,
			recovery_lease = NONE RETURN AFTER)`
	}
	return `
LET $pending = IF array::len($published) = 1 THEN $candidate_expected.extraction[0].id ELSE NONE END;
LET $fanout = IF array::len($published) != 1 THEN [] ELSE ` + extraction + ` END;
LET $pending_catalog = IF array::len($published) = 1 THEN $candidate_expected.resolver[0].id ELSE NONE END;
LET $catalog_force = ($same_publication = false) OR $control_advanced;
LET $catalog_fanout = IF array::len($published) != 1 THEN [] ELSE ` + resolver + ` END;` +
		candidateExtractionProjectionSQL + candidateResolverProjectionSQL + "\n"
}

func candidatePublishSQL(c *candidateManifestObservation) string {
	fence := ""
	catalogs, callers := "resolver_catalog_publication", "caller_generation_publication"
	published, staged, attempts, outcomes := "extraction_run", "extraction_run", "extraction_attempt", "extraction_domain_outcome"
	callerRevision := "(UPDATE $repo_rid SET caller_publication_revision =\n\t\t(caller_publication_revision ?? 0) + 1 RETURN AFTER)"
	evidenceRevision := "(UPDATE $repo_rid SET evidence_revision = (evidence_revision ?? 0) + 1\n\t\tRETURN AFTER)"
	if c != nil {
		fence = candidatePublishCensusSQL + candidateManifestCensusFenceSQL
		catalogs, callers = "$candidate_expected.catalogs", "$candidate_expected.callers"
		published, staged = "$candidate_expected.published", "$candidate_expected.staged"
		attempts, outcomes = "$candidate_expected.attempts", "$candidate_expected.outcomes"
		if len(c.Callers) != 1 {
			callerRevision = "[]"
		}
		if !*c.Revision {
			evidenceRevision = "[]"
		}
	}
	return `
BEGIN;` + fence + `
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
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
LET $acceptable = $caller_writer_ok AND $repo_ok
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
	(DELETE ` + catalogs + `
		WHERE repository = $repository RETURN BEFORE)
	ELSE [] END;
LET $retired_caller = IF array::len($published) = 1
		AND ($same_publication = false OR $control_advanced) THEN
	(DELETE ` + callers + `
		WHERE repository = $repository RETURN BEFORE)
	ELSE [] END;
LET $caller_revision = IF array::len($retired_caller) = 1 THEN
	` + callerRevision + `
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
			AND (
				(((partition_plan_digest ?? '') = '')
					AND ((partition_active ?? false) = false)
					AND ((partition_sealed ?? false) = false)
					AND (coverage.candidate_manifest_digest ?? '') != $manifest_digest)
				OR (((partition_active ?? false) = true)
					AND ((partition_sealed ?? false) = false)
					AND (partition_candidate_digest ?? '') != $manifest_digest)
			))
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
	(UPDATE ` + published + ` SET status = 'superseded', published_key = NONE
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
	(UPDATE ` + staged + ` SET status = 'aborted', published_key = NONE,
			partition_active = false
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
	(DELETE ` + attempts + `
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
	(DELETE ` + outcomes + `
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
	` + evidenceRevision + `
	ELSE [] END;
` + candidatePublishFanoutSQL(c) + `
RETURN IF array::len($fanout) = 1
	AND array::len($catalog_fanout) = 1
	AND (array::len($retired_caller) = 0
		OR array::len($caller_revision) = 1)
	THEN $published ELSE [] END;
COMMIT;`
}

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
		"caller_migration_rid":        callerGenerationPublicationMigrationID(),
		"caller_migration_version":    callerGenerationPublicationMigrationVersion,
		"candidate_limit":             restoreClearRows + 1,
	}
	for attempt := 0; ; attempt++ {
		census, err := s.candidateManifestCensus(ctx, candidatePublishCensusSQL, candidatePublishCensusControls, vars)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("publish candidate manifest census: %w", err)
		}
		rows := census.writeRows(false)
		recipe := storeWrite(rows)
		var bounded *candidateManifestObservation
		if rows > restoreClearRows {
			recipe = storeUnsupported()
		} else {
			bounded = &census
			vars["candidate_expected"] = census
			vars["candidate_extraction_projection"] = repoID(publication.Repository)
			vars["candidate_resolver_projection"] = repoID(publication.Repository)
			if len(census.Extraction) == 1 {
				vars["candidate_extraction_projection"] = census.Extraction[0].Projection
			}
			if len(census.Resolver) == 1 {
				vars["candidate_resolver_projection"] = census.Resolver[0].Projection
			}
		}
		results, err := storeQuery[[]candidateManifestPublicationRec](
			ctx, s.accounting, s.db, candidatePublishSQL(bounded), vars, recipe,
		)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("publish candidate manifest: %w", err)
		}
		publishedRows := firstDomainRows(results)
		if len(publishedRows) == 1 {
			if err := validateCandidateManifestPublication(
				publishedRows[0].CandidateManifestPublication, true,
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
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return nil, fmt.Errorf("get candidate manifest: %w", err)
	}
	results, err := storeQuery[[]candidateManifestPublicationRec](
		ctx, s.accounting, s.db,
		"SELECT * FROM $rid",
		map[string]any{"rid": candidateManifestPublicationID(repository)}, storeRead(),
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
	results, err := storeQuery[[]candidateManifestPublicationRec](
		ctx, s.accounting, s.db,
		"SELECT * FROM candidate_manifest_publication ORDER BY repository",
		nil, storeRead(),
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

const candidateClearCensusControls = 7
const candidateClearCensusSQL = `
LET $cm_caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
 WHERE version = $caller_migration_version LIMIT 1) = 1;
LET $cm_current = SELECT id FROM $rid;
LET $cm_catalogs = SELECT VALUE id FROM resolver_catalog_publication
 WHERE array::len($cm_current) = 1 AND repository = $repository ORDER BY id LIMIT $candidate_limit;
LET $cm_callers = SELECT VALUE id FROM caller_generation_publication
 WHERE repository = $repository ORDER BY id LIMIT $candidate_limit;
LET $cm_outcomes = SELECT VALUE id FROM extraction_domain_outcome
 WHERE array::len($cm_current) = 1 AND repo = $repository ORDER BY id LIMIT $candidate_limit;
LET $cm_resolver = SELECT id, type::record('repo', target) AS projection FROM
 (SELECT id, created_at, target FROM resolver_catalog_job
 WHERE array::len($cm_catalogs) = 1 AND pending_key = $repository AND status = 'pending' ORDER BY created_at LIMIT 1);
LET $candidate_census = {
 before: [$cm_caller_writer_ok, $cm_current], active: array::len($cm_current) = 1,
 revision: array::len($cm_current) = 1, catalogs: $cm_catalogs, callers: $cm_callers,
 published: [], staged: [], attempts: [], outcomes: $cm_outcomes, extraction: [], resolver: $cm_resolver
};
`

func candidateClearSQL(c *candidateManifestObservation) string {
	fence := ""
	catalogs, callers, outcomes := "resolver_catalog_publication", "caller_generation_publication", "extraction_domain_outcome"
	callerRevision := `		IF array::len($retired_caller) = 1 {
			UPDATE $repo_rid SET caller_publication_revision =
				(caller_publication_revision ?? 0) + 1 RETURN NONE;
		};
`
	fanout := `		LET $pending_catalog = IF array::len($retired_catalog) = 1 THEN
			(SELECT id, created_at FROM resolver_catalog_job
				WHERE pending_key = $repository AND status = 'pending'
				ORDER BY created_at LIMIT 1)[0].id
			ELSE NONE END;
		LET $catalog_fanout = IF array::len($retired_catalog) != 1 THEN []
			ELSE IF $pending_catalog != NONE THEN
				(UPDATE $pending_catalog SET force = true,
					recovery_lease = NONE RETURN AFTER)
			ELSE
				(CREATE resolver_catalog_job CONTENT {
					target: $repository,
					status: 'pending',
					attempts: 0,
					created_at: time::now(),
					pending_key: $repository,
					force: true
				} RETURN AFTER)
			END;` + projectResolverJobSQL
	if c != nil {
		fence = candidateClearCensusSQL + candidateManifestCensusFenceSQL
		catalogs, callers, outcomes = "$candidate_expected.catalogs", "$candidate_expected.callers", "$candidate_expected.outcomes"
		if len(c.Callers) != 1 {
			callerRevision = ""
		}
		fanout = "LET $catalog_fanout = [];"
		if len(c.Catalogs) == 1 {
			successor := `(CREATE resolver_catalog_job CONTENT {
    target: $repository, status: 'pending', attempts: 0, created_at: time::now(),
    pending_key: $repository, force: true
   } RETURN AFTER)`
			if len(c.Resolver) == 1 {
				successor = "(UPDATE $pending_catalog SET force = true, recovery_lease = NONE RETURN AFTER)"
			}
			fanout = `LET $pending_catalog = IF array::len($retired_catalog) = 1 THEN $candidate_expected.resolver[0].id ELSE NONE END;
LET $catalog_fanout = IF array::len($retired_catalog) != 1 THEN [] ELSE ` + successor + ` END;` + candidateResolverProjectionSQL
		}
	}
	clearCurrent := `		IF array::len($current) = 1 {
			DELETE $rid RETURN NONE;
			DELETE ` + outcomes + `
				WHERE repo = $repository RETURN NONE;
			UPDATE $repo_rid
				SET evidence_revision = (evidence_revision ?? 0) + 1
				WHERE name = $repository
				RETURN NONE;
		};
`
	if c != nil && !*c.Active {
		clearCurrent = ""
	}
	return `BEGIN;` + fence + `
		LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
			WHERE version = $caller_migration_version LIMIT 1) = 1;
		IF $caller_writer_ok = false {
			THROW 'phebs-permanent: caller-generation publication writer is not active'
		};
		LET $current = SELECT id FROM $rid;
		LET $retired_catalog = IF array::len($current) = 1 THEN
			(DELETE ` + catalogs + `
				WHERE repository = $repository RETURN BEFORE)
			ELSE [] END;
		LET $retired_caller = DELETE ` + callers + `
			WHERE repository = $repository RETURN BEFORE;
` + clearCurrent + callerRevision + fanout + `
		IF array::len($retired_catalog) = 1
			AND array::len($catalog_fanout) != 1 {
			THROW 'phebs-retryable: resolver catalog successor was not persisted'
		};
		COMMIT;`
}

func (s *Surreal) ClearCandidateManifestPublication(
	ctx context.Context,
	repository string,
) error {
	if err := validateCandidateRepository(repository); err != nil {
		return fmt.Errorf("clear candidate manifest: repository: %w", err)
	}
	vars := map[string]any{
		"rid":                      candidateManifestPublicationID(repository),
		"repo_rid":                 repoID(repository),
		"repository":               repository,
		"caller_migration_rid":     callerGenerationPublicationMigrationID(),
		"caller_migration_version": callerGenerationPublicationMigrationVersion,
		"candidate_limit":          restoreClearRows + 1,
	}
	census, err := s.candidateManifestCensus(ctx, candidateClearCensusSQL, candidateClearCensusControls, vars)
	if err != nil {
		return fmt.Errorf("clear candidate manifest census: %w", err)
	}
	rows := census.writeRows(true)
	recipe := storeWrite(rows)
	var bounded *candidateManifestObservation
	if rows > restoreClearRows {
		recipe = storeUnsupported()
	} else {
		bounded = &census
		vars["candidate_expected"] = census
		if len(census.Catalogs) == 1 {
			vars["candidate_resolver_projection"] = repoID(repository)
			if len(census.Resolver) == 1 {
				vars["candidate_resolver_projection"] = census.Resolver[0].Projection
			}
		}
	}
	_, err = storeQuery[any](ctx, s.accounting, s.db, candidateClearSQL(bounded), vars, recipe)
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
	if err := s.clearRestoreState(ctx, restoreClearCandidate); err != nil {
		return fmt.Errorf("clear all candidate manifests: %w", err)
	}
	return nil
}
