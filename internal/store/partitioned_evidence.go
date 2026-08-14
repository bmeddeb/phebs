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
	PartitionedExtractionDomainSchema = "phebs-partitioned-extraction-domain-v1"
	MaxPartitionedPlanBytes           = 4 << 20
	MaxPartitionedRootBytes           = 4 << 20
	// Historical v1/v2 store maxima: the T40.9 per-domain aggregate contract
	// every extraction domain shares. The store deliberately does not import
	// internal/candidate; these mirror candidate.MaxDomainResultFacts/Rows/
	// References and a candidate contract bump must move them together.
	MaxPartitionedFacts      int64 = 49_152
	MaxPartitionedRows       int64 = 98_304
	MaxPartitionedReferences int64 = 98_304
	// T40.R1's measured all-dimension kafka-producer correction is the only
	// (domain, plan-schema) binding admitted above the historical maxima.
	// These mirror candidate.DomainResultPlanSchemaV3, DomainResultPlanV3Domain,
	// and MaxDomainResult{Facts,Rows,References}V3.
	PartitionedPlanSchemaV3          = "phebs-extraction-domain-result-plan-v3"
	PartitionedV3Domain              = "kafka-producer"
	MaxPartitionedFactsV3      int64 = 262_144
	MaxPartitionedRowsV3       int64 = 524_288
	MaxPartitionedReferencesV3 int64 = 262_144
)

// partitionedRunMaxima dispatches the store envelope on the exact
// (domain, plan-schema) binding: only the measured T40.R1 kafka-producer v3
// contract may use the raised aggregate ceilings; every other domain and any
// absent or historical schema keeps the v1/v2 maxima.
func partitionedRunMaxima(domain, planSchema string) (facts, rows, references int64) {
	if domain == PartitionedV3Domain && planSchema == PartitionedPlanSchemaV3 {
		return MaxPartitionedFactsV3, MaxPartitionedRowsV3, MaxPartitionedReferencesV3
	}
	return MaxPartitionedFacts, MaxPartitionedRows, MaxPartitionedReferences
}

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
	PriorRunID        string `json:"prior_run_id,omitempty"`
	PriorPlanDigest   string `json:"prior_plan_digest,omitempty"`
	PriorRootDigest   string `json:"prior_root_digest,omitempty"`
}

type PartitionedEvidenceStore interface {
	PublishPartitionedExtractionDomain(context.Context, PartitionedExtractionDomain) error
	GetPartitionedExtractionDomain(context.Context, string, string) (*PartitionedExtractionDomain, error)
	PinPartitionedExtractionRun(context.Context, string, string) error
	UnpinPartitionedExtractionRun(context.Context, string, string) error
	UnpinPartitionedExtractionOwner(context.Context, string) error
	ReconcilePartitionedExtractionOwners(context.Context, []string) error
}

func (s *Surreal) PinPartitionedExtractionRun(ctx context.Context, runID, owner string) error {
	if strings.TrimSpace(runID) != runID || runID == "" || len(runID) > maxEvidenceIdentityBytes ||
		strings.TrimSpace(owner) != owner || owner == "" || len(owner) > maxEvidenceIdentityBytes {
		return errors.New("pin partitioned extraction run: invalid identity")
	}
	const query = `
BEGIN;
LET $run = (SELECT * FROM $run_rid WHERE run_id = $run_id AND run_id = record::id(id)
	AND status = 'staged' AND (partition_sealed ?? false) = true
	AND retention_quarantined = false LIMIT 1)[0];
LET $rooted = array::len(SELECT id FROM extraction_domain_root
	WHERE run_id = $run_id OR prior_run_id = $run_id LIMIT 1) = 1;
LET $existing = array::len(SELECT id FROM $pin_rid WHERE run_id = $run_id AND kind = $owner LIMIT 1) = 1;
LET $pinned = IF $run != NONE AND ($rooted OR $existing) THEN
	(UPSERT $pin_rid CONTENT { pin_key: $pin_key, run_id: $run_id,
		kind: $owner, created_at: time::now() } RETURN AFTER) ELSE [] END;
RETURN $pinned;
COMMIT;`
	pinKey := hashIdentity("pin_", runID, owner)
	results, err := surrealdb.Query[[]evidencePinRec](ctx, s.db, query, map[string]any{
		"run_rid": extractionRunID(runID), "run_id": runID,
		"pin_rid": evidencePinRecordID(runID, owner), "pin_key": pinKey, "owner": owner,
	})
	if err != nil {
		return fmt.Errorf("pin partitioned extraction run: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return nil
		}
	}
	return fmt.Errorf("pin partitioned extraction run: authority changed: %w", ErrConflict)
}

func (s *Surreal) UnpinPartitionedExtractionRun(ctx context.Context, runID, owner string) error {
	if strings.TrimSpace(runID) != runID || runID == "" || len(runID) > maxEvidenceIdentityBytes ||
		strings.TrimSpace(owner) != owner || owner == "" || len(owner) > maxEvidenceIdentityBytes {
		return errors.New("unpin partitioned extraction run: invalid identity")
	}
	_, err := surrealdb.Query[[]evidencePinRec](ctx, s.db,
		"DELETE $rid WHERE run_id = $run_id AND kind = $owner RETURN BEFORE",
		map[string]any{"rid": evidencePinRecordID(runID, owner), "run_id": runID, "owner": owner})
	if err != nil {
		return fmt.Errorf("unpin partitioned extraction run: %w", err)
	}
	return nil
}

func (s *Surreal) UnpinPartitionedExtractionOwner(ctx context.Context, owner string) error {
	if strings.TrimSpace(owner) != owner || owner == "" || len(owner) > maxEvidenceIdentityBytes {
		return errors.New("unpin partitioned extraction owner: invalid identity")
	}
	_, err := surrealdb.Query[[]evidencePinRec](ctx, s.db,
		"DELETE evidence_pin WHERE kind = $owner RETURN BEFORE", map[string]any{"owner": owner})
	if err != nil {
		return fmt.Errorf("unpin partitioned extraction owner: %w", err)
	}
	return nil
}

func (s *Surreal) ReconcilePartitionedExtractionOwners(ctx context.Context, owners []string) error {
	if len(owners) > 262_144 {
		return errors.New("reconcile partitioned extraction owners: owner set exceeds bound")
	}
	for _, owner := range owners {
		if strings.TrimSpace(owner) != owner || !strings.HasPrefix(owner, "relationship:sha256:") ||
			len(owner) > maxEvidenceIdentityBytes {
			return errors.New("reconcile partitioned extraction owners: invalid owner")
		}
	}
	_, err := surrealdb.Query[[]evidencePinRec](ctx, s.db,
		"DELETE evidence_pin WHERE string::starts_with(kind, 'relationship:') AND kind NOT IN $owners RETURN BEFORE",
		map[string]any{"owners": owners})
	if err != nil {
		return fmt.Errorf("reconcile partitioned extraction owners: %w", err)
	}
	return nil
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
		len(publication.Plan) > MaxPartitionedPlanBytes || len(publication.Root) > MaxPartitionedRootBytes ||
		!json.Valid([]byte(publication.Plan)) || !json.Valid([]byte(publication.Root)) ||
		((publication.PriorRunID == "") != (publication.PriorPlanDigest == "") ||
			(publication.PriorRunID == "") != (publication.PriorRootDigest == "")) ||
		(publication.PriorRunID != "" && (len(publication.PriorRunID) > maxEvidenceIdentityBytes ||
			!validSHA256Digest(publication.PriorPlanDigest) || !validSHA256Digest(publication.PriorRootDigest))) {
		return errors.New("partitioned extraction domain is incomplete or unbounded")
	}
	facts, rows, references := partitionedPublicationMaxima(publication)
	if publication.Facts > facts || publication.Rows > rows || publication.References > references {
		return errors.New("partitioned extraction domain exceeds its exact contract envelope")
	}
	return nil
}

// partitionedPublicationMaxima keeps every historical control on the v1/v2
// store maxima without inspecting plan bytes. A publication whose totals
// exceed them must prove the exact kafka-producer v3 binding from its own
// retained canonical plan bytes — never from a caller claim — so the raise
// stays exact to the measured T40.R1 contract and no other domain or schema
// can use it.
func partitionedPublicationMaxima(publication PartitionedExtractionDomain) (facts, rows, references int64) {
	facts, rows, references = MaxPartitionedFacts, MaxPartitionedRows, MaxPartitionedReferences
	if publication.Facts <= facts && publication.Rows <= rows && publication.References <= references {
		return facts, rows, references
	}
	if publication.Domain != PartitionedV3Domain {
		return facts, rows, references
	}
	var plan struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal([]byte(publication.Plan), &plan); err != nil ||
		plan.Schema != PartitionedPlanSchemaV3 {
		return facts, rows, references
	}
	return MaxPartitionedFactsV3, MaxPartitionedRowsV3, MaxPartitionedReferencesV3
}

func (limits PartitionedExtractionRunLimits) validate(domain, planSchema string) error {
	facts, rows, references := partitionedRunMaxima(domain, planSchema)
	if limits.Facts < 0 || limits.Facts > facts ||
		limits.Rows < 0 || limits.Rows > rows ||
		limits.References < 0 || limits.References > references {
		return errors.New("partitioned extraction run limits exceed their exact contract binding")
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
		prior_run_id: IF $existing != NONE AND $existing.run_id != $run_id THEN $existing.run_id ELSE $existing.prior_run_id ?? NONE END,
		prior_plan_digest: IF $existing != NONE AND $existing.run_id != $run_id THEN $existing.plan_digest ELSE $existing.prior_plan_digest ?? NONE END,
		prior_root_digest: IF $existing != NONE AND $existing.run_id != $run_id THEN $existing.root_digest ELSE $existing.prior_root_digest ?? NONE END,
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

// ReleaseOneUnrootedPartitionRun transfers at most one sealed historical run
// to the existing evidence sweeper. Current and one per-domain rollback floor
// remain sealed; evidence pins still outrank collection in the later sweep.
func (s *Surreal) ReleaseOneUnrootedPartitionRun(ctx context.Context) (bool, error) {
	const query = `
BEGIN;
LET $candidate = (SELECT id, run_id, started_at FROM extraction_run
	WHERE status = 'staged' AND (partition_sealed ?? false) = true
		AND (partition_active ?? false) = false
		AND retention_quarantined = false
		AND run_id NOT IN (SELECT VALUE run_id FROM evidence_pin)
		AND run_id NOT IN (SELECT VALUE run_id FROM extraction_domain_root)
		AND run_id NOT IN (SELECT VALUE prior_run_id FROM extraction_domain_root
			WHERE prior_run_id != NONE)
	ORDER BY started_at, run_id LIMIT 1)[0];
LET $released = IF $candidate != NONE THEN
	(UPDATE $candidate.id SET partition_sealed = false RETURN AFTER) ELSE [] END;
RETURN $released;
COMMIT;`
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db, query, nil)
	if err != nil {
		return false, fmt.Errorf("release unrooted partition run: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			return true, nil
		}
	}
	return false, nil
}
