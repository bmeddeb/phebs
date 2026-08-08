package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const (
	PartitionedExtractionDomainSchema       = "phebs-partitioned-extraction-domain-v1"
	MaxPartitionedPlanBytes                 = 4 << 20
	MaxPartitionedRootBytes                 = 4 << 20
	MaxPartitionedFacts               int64 = 49_152
	MaxPartitionedRows                int64 = 98_304
	MaxPartitionedReferences          int64 = 98_304
)

// PartitionedExtractionDomain is the store's opaque atomic binding between a
// completely validated T40.9 plan/root and the T40.7-accounted evidence run.
// Plan and Root retain their exact canonical bytes for T40.11 readers; the
// store independently verifies the physical fact/row/reference populations
// before moving this latest-only pointer.
type PartitionedExtractionDomain struct {
	Schema            string `json:"schema"`
	Repository        string `json:"repository"`
	Domain            string `json:"domain"`
	RunID             string `json:"run_id"`
	PlanDigest        string `json:"plan_digest"`
	RootDigest        string `json:"root_digest"`
	CandidateDigest   string `json:"candidate_digest"`
	SourceDigest      string `json:"source_digest"`
	ObservationDigest string `json:"observation_digest"`
	Facts             int64  `json:"facts"`
	Rows              int64  `json:"rows"`
	References        int64  `json:"references"`
	Plan              string `json:"plan"`
	Root              string `json:"root"`
}

type PartitionedEvidenceStore interface {
	PublishPartitionedExtractionDomain(context.Context, PartitionedExtractionDomain) error
	GetPartitionedExtractionDomain(context.Context, string, string) (*PartitionedExtractionDomain, error)
}

var _ PartitionedEvidenceStore = (*Surreal)(nil)

func (publication PartitionedExtractionDomain) Validate() error {
	if publication.Schema != PartitionedExtractionDomainSchema ||
		strings.TrimSpace(publication.Repository) != publication.Repository || publication.Repository == "" ||
		strings.TrimSpace(publication.Domain) != publication.Domain || publication.Domain == "" ||
		len(publication.Repository) > MaxJobHistoryTargetCharacters || len(publication.Domain) > 128 ||
		strings.TrimSpace(publication.RunID) != publication.RunID || publication.RunID == "" ||
		len(publication.RunID) > maxEvidenceIdentityBytes ||
		!validSHA256Digest(publication.PlanDigest) || !validSHA256Digest(publication.RootDigest) ||
		!validSHA256Digest(publication.CandidateDigest) || !validSHA256Digest(publication.SourceDigest) ||
		!validSHA256Digest(publication.ObservationDigest) ||
		publication.Facts < 0 || publication.Rows < 0 || publication.References < 0 ||
		publication.Facts > MaxPartitionedFacts || publication.Rows > MaxPartitionedRows ||
		publication.References > MaxPartitionedReferences ||
		len(publication.Plan) > MaxPartitionedPlanBytes || len(publication.Root) > MaxPartitionedRootBytes ||
		!json.Valid([]byte(publication.Plan)) || !json.Valid([]byte(publication.Root)) {
		return errors.New("partitioned extraction domain is incomplete or unbounded")
	}
	return nil
}

func (limits PartitionedExtractionRunLimits) validate() error {
	if limits.Facts < 0 || limits.Facts > MaxPartitionedFacts ||
		limits.Rows < 0 || limits.Rows > MaxPartitionedRows ||
		limits.References < 0 || limits.References > MaxPartitionedReferences {
		return errors.New("partitioned extraction run limits are invalid")
	}
	return nil
}

func partitionedDomainID(repository, domain string) models.RecordID {
	identity := hashIdentity("partitioned_extraction_domain_v1_", repository, domain)
	return models.NewRecordID("extraction_domain_root", strings.TrimPrefix(identity, "sha256:"))
}

const publishPartitionedExtractionDomainSQL = `
BEGIN;
LET $existing = (SELECT * FROM $root_rid LIMIT 1)[0];
LET $run = (SELECT * FROM $run_rid WHERE run_id = $run_id
	AND run_id = record::id(id) AND repo = $repository AND domain = $domain
	AND store_schema_version = $store_schema_version
	AND evidence_format_version = $evidence_format_version
	AND evidence_migration_version = $evidence_migration_version
	AND retention_quarantined = false AND published_key = NONE
	AND status = 'staged' LIMIT 1)[0];
LET $candidate = (SELECT repository, head_commit, unit_digest, manifest_digest
	FROM $candidate_rid LIMIT 1)[0];
LET $current_ok = $candidate != NONE
	AND $candidate.repository = $repository
	AND $candidate.manifest_digest = $candidate_digest
	AND $run != NONE AND $run.commit = $candidate.head_commit
	AND ($run.unit_digest ?? '') = ($candidate.unit_digest ?? '');
LET $chunk_totals = (SELECT count() AS chunks, math::sum(fact_count) AS facts,
	math::sum(row_delta) AS rows, math::sum(reference_delta) AS references
	FROM evidence_chunk WHERE run_id = $run_id GROUP ALL)[0];
LET $assertion_count = array::len(SELECT VALUE assertion_id FROM assertion WHERE run_id = $run_id);
LET $association_count = array::len(SELECT VALUE occurrence_id FROM snapshot_evidence WHERE run_id = $run_id);
LET $reference_count = array::len(array::flatten(SELECT VALUE supporting FROM assertion WHERE run_id = $run_id))
	+ array::len(array::flatten(SELECT VALUE contradicting FROM assertion WHERE run_id = $run_id));
LET $counts_ok = $run != NONE
	AND ($run.staged_fact_count ?? -1) = $facts
	AND ($run.staged_row_count ?? -1) = $rows
	AND ($run.staged_reference_count ?? -1) = $references
	AND ($chunk_totals.facts ?? 0) = $facts
	AND ($chunk_totals.rows ?? 0) = $rows
	AND ($chunk_totals.references ?? 0) = $references
	AND $assertion_count + $association_count = $rows
	AND $reference_count = $references
	AND $facts <= ($run.partition_fact_limit ?? -1)
	AND $rows <= ($run.partition_row_limit ?? -1)
	AND $references <= ($run.partition_reference_limit ?? -1);
LET $same = $existing != NONE AND $run != NONE
	AND $existing.run_id = $run_id AND $existing.plan_digest = $plan_digest
	AND $existing.root_digest = $root_digest AND $existing.root = $root
	AND $existing.plan = $plan;

LET $run_owned = $run != NONE AND $run.partition_plan_digest = $plan_digest
	AND $run.partition_candidate_digest = $candidate_digest
	AND (($run.partition_active ?? false) = true OR ($run.partition_sealed ?? false) = true)
	AND (($run.partition_root_digest ?? $root_digest) = $root_digest);
LET $ready = $current_ok AND $counts_ok AND $run_owned;
LET $sealed = $run != NONE AND (($run.partition_sealed ?? false) = true);
LET $marked = IF $ready AND !$same AND !$sealed THEN
	(UPDATE $run_rid SET partition_active = false, partition_sealed = true,
		partition_plan_digest = $plan_digest,
		partition_root_digest = $root_digest WHERE status = 'staged'
			AND ((partition_sealed ?? false) = false) RETURN AFTER)
	ELSE IF $ready AND ($same OR $sealed) THEN [$run] ELSE [] END;
LET $published = IF array::len($marked) = 1 AND !$same THEN
	(UPSERT $root_rid CONTENT {
		schema: $schema, repository: $repository, domain: $domain,
		run_id: $run_id, plan_digest: $plan_digest, root_digest: $root_digest,
		candidate_digest: $candidate_digest, source_digest: $source_digest,
		observation_digest: $observation_digest, facts: $facts, rows: $rows,
		references: $references, plan: $plan, root: $root,
		published_at: time::now()
	} RETURN AFTER)
	ELSE IF $ready AND $same THEN [$existing] ELSE [] END;
RETURN $published;
COMMIT;`

func (s *Surreal) PublishPartitionedExtractionDomain(
	ctx context.Context,
	publication PartitionedExtractionDomain,
) error {
	if err := publication.Validate(); err != nil {
		return fmt.Errorf("publish partitioned extraction domain: %w", err)
	}
	variables := map[string]any{
		"root_rid": partitionedDomainID(publication.Repository, publication.Domain),
		"run_rid":  extractionRunID(publication.RunID), "run_id": publication.RunID,
		"candidate_rid": candidateManifestPublicationID(publication.Repository),
		"schema":        publication.Schema, "repository": publication.Repository,
		"domain": publication.Domain, "plan_digest": publication.PlanDigest,
		"root_digest": publication.RootDigest, "candidate_digest": publication.CandidateDigest,
		"source_digest": publication.SourceDigest, "observation_digest": publication.ObservationDigest,
		"facts": publication.Facts, "rows": publication.Rows, "references": publication.References,
		"plan": publication.Plan, "root": publication.Root,
		"store_schema_version":       evidenceStoreSchemaVersion,
		"evidence_format_version":    evidenceFormatVersion,
		"evidence_migration_version": evidenceMigrationVersion,
	}
	results, err := surrealdb.Query[[]partitionedDomainRec](ctx, s.db, publishPartitionedExtractionDomainSQL, variables)
	if err != nil {
		return fmt.Errorf("publish partitioned extraction domain: %w", err)
	}
	rows := firstPartitionedDomainRows(results)
	if len(rows) != 1 {
		return fmt.Errorf("publish partitioned extraction domain: run, counters, or candidate authority changed: %w", ErrConflict)
	}
	stored := rows[0].publication()
	if !samePartitionedDomain(stored, publication) {
		return fmt.Errorf("publish partitioned extraction domain: immutable collision: %w", ErrConflict)
	}
	return nil
}

type partitionedDomainRec struct {
	PartitionedExtractionDomain
}

func (record partitionedDomainRec) publication() PartitionedExtractionDomain {
	return record.PartitionedExtractionDomain
}

func firstPartitionedDomainRows(results *[]surrealdb.QueryResult[[]partitionedDomainRec]) []partitionedDomainRec {
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

func samePartitionedDomain(left, right PartitionedExtractionDomain) bool {
	return left.Schema == right.Schema && left.Repository == right.Repository &&
		left.Domain == right.Domain && left.RunID == right.RunID &&
		left.PlanDigest == right.PlanDigest && left.RootDigest == right.RootDigest &&
		left.CandidateDigest == right.CandidateDigest && left.SourceDigest == right.SourceDigest &&
		left.ObservationDigest == right.ObservationDigest && left.Facts == right.Facts &&
		left.Rows == right.Rows && left.References == right.References &&
		left.Plan == right.Plan && left.Root == right.Root
}

func (s *Surreal) GetPartitionedExtractionDomain(
	ctx context.Context,
	repository,
	domain string,
) (*PartitionedExtractionDomain, error) {
	if strings.TrimSpace(repository) != repository || repository == "" ||
		strings.TrimSpace(domain) != domain || domain == "" {
		return nil, errors.New("get partitioned extraction domain: invalid scope")
	}
	results, err := surrealdb.Query[[]partitionedDomainRec](ctx, s.db,
		"SELECT * FROM $rid LIMIT 1", map[string]any{"rid": partitionedDomainID(repository, domain)})
	if err != nil {
		return nil, fmt.Errorf("get partitioned extraction domain: %w", err)
	}
	rows := firstPartitionedDomainRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("get partitioned extraction domain: %w", ErrNotFound)
	}
	publication := rows[0].publication()
	if publication.Repository != repository || publication.Domain != domain || publication.Validate() != nil {
		return nil, errors.New("get partitioned extraction domain: invalid stored authority")
	}
	return &publication, nil
}
