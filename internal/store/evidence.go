package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

var _ EvidenceStore = (*Surreal)(nil)

const (
	// Store schema is the exact writer generation. Evidence format is the
	// stable read/pin contract: a newer writer may keep the same format when
	// its proof bundle remains backwards-compatible.
	evidenceStoreSchemaVersion         = "t12-store-v9"
	evidencePreviousStoreSchemaVersion = "t12-store-v8"
	// evidenceLegacyUpgradableStoreSchemaVersion is the second supported
	// predecessor, exceptionally kept upgradable for one release: v8 was never
	// released (it landed after the v0.2.0 tag), so v7 is the generation real
	// stores are actually on and quarantining it would silently discard their
	// evidence. Unlike v1–v6 its per-generation upgrade pass did run, so it is
	// migrated rather than retired. It carries no unit digest — the v7 writer
	// had one attempt row per repo/domain — so it maps to the explicit
	// whole-repository scope. This window closes at the next bump, when v7
	// finally joins retiredEvidenceStoreSchemas.
	evidenceLegacyUpgradableStoreSchemaVersion = "t12-store-v7"
	evidenceFormatVersion                      = "t12-evidence-v1"
	evidenceMigrationVersion                   = "t12-evidence-migration-v8"
	evidencePreviousMigrationVersion           = "t12-evidence-migration-v7"
	evidenceWriterGuardEvent                   = "extraction_run_writer_v9"
	reverseAssertionIndexName                  = "assertion_reverse_v6"
	evidenceMigrationBatchSize                 = 128
	evidenceSweepCandidateBatchSize            = 1
	evidenceSweepRowBatchSize                  = 512
	maxEvidenceRowsPerRun                      = 25_000
	// T20.3 raises whole-run admission only. Individual worker transactions
	// remain independently bounded and are currently at most 256 facts.
	maxEvidenceBatchRows              = 10_000
	maxEvidenceRefsPerAssertion       = 4096
	maxEvidenceReferenceEdges         = 20_000
	maxEvidenceIdentityBytes          = 64 << 10
	maxEvidencePathBytes              = 4096
	maxEvidenceOccurrences            = 100
	maxCoverageFileCount              = 10_000_000
	maxCoverageReadBytes        int64 = 1 << 50
	defaultReverseAssertionPage       = 50
	maxReverseAssertionPage           = 100
)

func hashIdentity(prefix string, fields ...string) string {
	h := sha256.New()
	for _, field := range fields {
		_, _ = fmt.Fprintf(h, "%d:%s;", len(field), field) // hash.Hash never errors
	}
	return prefix + hex.EncodeToString(h.Sum(nil))
}

// ComputeAtomID is the deterministic content identity from the PLAN ADR:
// H(schema_version, blob_digest, byte_span, rule_id, extractor_version,
// adapter_config_digest, fact_fingerprint). Repository, commit, path, run,
// and timestamps deliberately do not participate.
func ComputeAtomID(a EvidenceAtom) string {
	return hashIdentity("ea_",
		a.SchemaVersion,
		a.BlobDigest,
		fmt.Sprintf("%d-%d", a.StartByte, a.EndByte),
		a.RuleID,
		a.ExtractorVersion,
		a.AdapterConfigDigest,
		a.FactFingerprint,
	)
}

// ComputeSnapshotEvidenceID identifies one repository placement of an atom.
// It is stable across extractor reruns; the physical association row also
// includes the run id so two retained runs can independently reference it.
func ComputeSnapshotEvidenceID(e SnapshotEvidence) string {
	return hashIdentity("se_",
		e.AtomID, e.Repo, e.Commit, e.Path,
		fmt.Sprintf("%d-%d", e.StartLine, e.EndLine),
		e.VisibilityScope,
	)
}

// ComputeAssertionID identifies the semantic claim. Evidence references,
// confidence/classification attributes, detail, extraction run, and timestamps
// are intentionally excluded: changing proof or a versioned field-name
// attribute does not silently change the identity of the claim it supports.
func ComputeAssertionID(a Assertion) string {
	return hashIdentity("as_",
		a.Predicate, a.Subject, a.Object, a.Lineage, a.Repo,
	)
}

func evidenceAtomRecordID(id string) models.RecordID {
	return models.NewRecordID("evidence_atom", id)
}

func snapshotEvidenceRecordID(runID, occurrenceID string) models.RecordID {
	return models.NewRecordID("snapshot_evidence", hashIdentity("sea_", runID, occurrenceID))
}

func assertionRecordID(runID, assertionID string) models.RecordID {
	return models.NewRecordID("assertion", hashIdentity("ara_", runID, assertionID))
}

func evidencePinRecordID(runID, kind string) models.RecordID {
	return models.NewRecordID("evidence_pin", hashIdentity("ep_", runID, kind))
}

func publishedKey(scope ExtractionScope) string {
	return hashIdentity(
		"published_scope_v2_",
		scope.Repository,
		scope.Commit,
		scope.UnitDigest,
		scope.Domain,
	)
}

func legacyPublishedKey(repo, domain string) string {
	return hashIdentity("published_", repo, domain)
}

func extractionAttemptID(scope ExtractionScope) models.RecordID {
	return models.NewRecordID(
		"extraction_attempt",
		hashIdentity(
			"attempt_scope_v2_",
			scope.Repository,
			scope.Commit,
			scope.UnitDigest,
			scope.Domain,
		),
	)
}

func legacyExtractionAttemptID(repo, domain string) models.RecordID {
	return models.NewRecordID("extraction_attempt", hashIdentity("attempt_", repo, domain))
}

// extractionDomainOutcomeID is latest-only per configured domain. The row
// carries its exact commit/unit scope and generation, so a scope advance makes
// the prior row ineligible immediately without accumulating one row per commit.
func extractionDomainOutcomeID(repository, domain string) models.RecordID {
	return models.NewRecordID(
		"extraction_domain_outcome",
		hashIdentity("domain_outcome_v1_", repository, domain),
	)
}

type extractionAttemptRec struct {
	RunID      string    `json:"run_id"`
	Repo       string    `json:"repo"`
	Commit     string    `json:"commit"`
	UnitDigest string    `json:"unit_digest"`
	Domain     string    `json:"domain"`
	Extractor  string    `json:"extractor"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
}

func (r extractionAttemptRec) attempt() ExtractionAttempt {
	return ExtractionAttempt(r)
}

type extractionDomainOutcomeRec struct {
	Repo                    string                       `json:"repo"`
	Commit                  string                       `json:"commit"`
	UnitDigest              string                       `json:"unit_digest"`
	Domain                  string                       `json:"domain"`
	Disposition             DomainOutcomeDisposition     `json:"disposition"`
	Generation              ExtractionGenerationIdentity `json:"generation"`
	CandidateControlFailure bool                         `json:"candidate_control_failure"`
	RunID                   string                       `json:"run_id"`
	ReceiptSchema           string                       `json:"receipt_schema"`
	Receipt                 string                       `json:"receipt"`
	RecordedAt              time.Time                    `json:"recorded_at"`
	StoreSchema             string                       `json:"store_schema_version"`
	Migration               string                       `json:"evidence_migration_version"`
}

func (row extractionDomainOutcomeRec) outcome() ExtractionDomainOutcome {
	return ExtractionDomainOutcome{
		Scope: ExtractionScope{
			Repository: row.Repo,
			Commit:     row.Commit,
			UnitDigest: row.UnitDigest,
			Domain:     row.Domain,
		},
		Disposition:             row.Disposition,
		Generation:              row.Generation,
		CandidateControlFailure: row.CandidateControlFailure,
		RunID:                   row.RunID,
		ReceiptSchema:           row.ReceiptSchema,
		Receipt:                 row.Receipt,
		RecordedAt:              row.RecordedAt,
	}
}

func prepareExtractionDomainOutcome(
	outcome ExtractionDomainOutcome,
) (ExtractionDomainOutcome, error) {
	outcome.RecordedAt = time.Time{}
	outcome.Generation.Digest =
		ComputeExtractionGenerationDigest(outcome.Generation)
	if err := outcome.Validate(); err != nil {
		return ExtractionDomainOutcome{}, err
	}
	return outcome, nil
}

type extractionRunRec struct {
	RecID       *models.RecordID `json:"id"`
	RunID       string           `json:"run_id"`
	Repo        string           `json:"repo"`
	Commit      string           `json:"commit"`
	UnitDigest  string           `json:"unit_digest"`
	Domain      string           `json:"domain"`
	Extractor   string           `json:"extractor"`
	Status      string           `json:"status"`
	StartedAt   time.Time        `json:"started_at"`
	PublishedAt *time.Time       `json:"published_at,omitempty"`
	Coverage    CoverageManifest `json:"coverage"`
	StoreSchema string           `json:"store_schema_version"`
	Format      string           `json:"evidence_format_version"`
}

func (r extractionRunRec) run() ExtractionRun {
	id := r.RunID
	if id == "" && r.RecID != nil {
		if value, ok := r.RecID.ID.(string); ok {
			id = value
		}
	}
	return ExtractionRun{
		ID: id, Repo: r.Repo, Commit: r.Commit, Domain: r.Domain,
		UnitDigest: r.UnitDigest, Extractor: r.Extractor, Status: r.Status,
		StartedAt: r.StartedAt, PublishedAt: r.PublishedAt, Coverage: r.Coverage,
	}
}

func extractionRunID(id string) models.RecordID { return models.NewRecordID("extraction_run", id) }

func firstExtractionRows(results *[]surrealdb.QueryResult[[]extractionRunRec]) []extractionRunRec {
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

func validateExtractionScope(scope ExtractionScope) error {
	for name, value := range map[string]string{
		"repository": scope.Repository,
		"commit":     scope.Commit,
		"domain":     scope.Domain,
	} {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) ||
			len(value) > maxEvidenceIdentityBytes {
			return fmt.Errorf("%s is not valid bounded UTF-8", name)
		}
	}
	if scope.UnitDigest != "" && !validSHA256Digest(scope.UnitDigest) {
		return errors.New("unit digest must be empty or canonical sha256")
	}
	return nil
}

func scopeForRun(run *ExtractionRun) ExtractionScope {
	if run == nil {
		return ExtractionScope{}
	}
	return ExtractionScope{
		Repository: run.Repo,
		Commit:     run.Commit,
		UnitDigest: run.UnitDigest,
		Domain:     run.Domain,
	}
}

func (s *Surreal) BeginExtractionRun(
	ctx context.Context,
	scope ExtractionScope,
	extractor string,
) (*ExtractionRun, error) {
	if err := validateExtractionScope(scope); err != nil {
		return nil, fmt.Errorf("begin extraction run: %w", err)
	}
	if strings.TrimSpace(extractor) == "" || !utf8.ValidString(extractor) ||
		len(extractor) > maxEvidenceIdentityBytes {
		return nil, errors.New("begin extraction run: extractor is not valid bounded UTF-8")
	}
	id, err := authRandomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
		`BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
LET $existing_attempt = (SELECT repo, commit, unit_digest, domain,
		store_schema_version, evidence_format_version,
		evidence_migration_version
	FROM $attempt_rid)[0];
LET $attempt_ok = $existing_attempt = NONE OR (
	($existing_attempt.repo ?? '') = $repo
	AND ($existing_attempt.commit ?? '') = $commit
	AND ($existing_attempt.unit_digest ?? '') = $unit_digest
	AND ($existing_attempt.domain ?? '') = $domain
	AND ($existing_attempt.store_schema_version ?? '') = $store_schema_version
	AND ($existing_attempt.evidence_format_version ?? '') = $evidence_format_version
	AND ($existing_attempt.evidence_migration_version ?? '') = $evidence_migration_version
);
LET $created = IF $writer_ok AND $attempt_ok THEN
	(CREATE $rid SET run_id = $run_id, repo = $repo, commit = $commit,
		unit_digest = $unit_digest, domain = $domain,
		extractor = $extractor, status = 'staged', started_at = $now,
		store_schema_version = $store_schema_version,
		evidence_format_version = $evidence_format_version,
		evidence_migration_version = $evidence_migration_version,
		retention_quarantined = false, retention_phase = NONE,
		staged_revision = 0, retention_revision = 0 RETURN AFTER)
	ELSE [] END;
IF $writer_ok AND $attempt_ok {
	UPSERT $attempt_rid SET run_id = $run_id, repo = $repo, commit = $commit,
		unit_digest = $unit_digest, domain = $domain,
		extractor = $extractor, status = 'staged', started_at = $now,
		store_schema_version = $store_schema_version,
		evidence_format_version = $evidence_format_version,
		evidence_migration_version = $evidence_migration_version RETURN NONE
};
RETURN $created;
COMMIT;`,
		map[string]any{
			"rid": extractionRunID(id), "run_id": id,
			"repo": scope.Repository, "commit": scope.Commit,
			"unit_digest": scope.UnitDigest, "domain": scope.Domain,
			"extractor": extractor, "now": now,
			"attempt_rid":                extractionAttemptID(scope),
			"migration_rid":              evidenceMigrationStateID(),
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_format_version":    evidenceFormatVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		})
	if err != nil {
		return nil, fmt.Errorf("begin extraction run: %w", err)
	}
	rows := firstExtractionRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("begin extraction run: writer generation is not active: %w", ErrConflict)
	}
	run := rows[0].run()
	return &run, nil
}

// A claimant can retain another run's logical id either directly (an unknown
// writer that migration must not touch) or through a quarantine marker after
// its physical row is canonicalized. Child rows carry only that logical id, so
// the physical owner must fail closed in both cases; otherwise claimant proof
// is indistinguishable from owner proof on reads and retention. IF guards the
// byte casts so malformed schemaless values cannot make eligibility error.
const evidenceRunHasNoAmbiguousClaimantSQL = `evidence_migration_ambiguous_run_id = NONE
	AND record::id(id) NOT IN (
	SELECT VALUE evidence_migration_ambiguous_run_id FROM extraction_run
	WHERE IF type::is_string(evidence_migration_ambiguous_run_id)
		THEN evidence_migration_ambiguous_run_id != ''
			AND bytes::len(<bytes>evidence_migration_ambiguous_run_id) <= $max_evidence_identity_bytes
		ELSE false END
	)
	AND record::id(id) NOT IN (
	SELECT VALUE run_id FROM extraction_run
	WHERE IF type::is_string(run_id)
		THEN run_id != ''
			AND bytes::len(<bytes>run_id) <= $max_evidence_identity_bytes
			AND run_id != record::id(id)
		ELSE false END
)`

// evidenceRunProbeHasNoClaimantSQL is the same fail-closed guard for
// statements that operate on one known run: $probe_run_id / $probe_rid name
// the run the statement targets, so both claimant lookups become indexed
// equality probes instead of the scan-form subqueries above. Equality against
// a real bounded run id preserves the scan form's type and byte-cap
// semantics (a non-string or oversized value can never equal one).
const evidenceRunProbeHasNoClaimantSQL = `evidence_migration_ambiguous_run_id = NONE
	AND array::len(SELECT id FROM extraction_run
		WHERE evidence_migration_ambiguous_run_id = $probe_run_id LIMIT 1) = 0
	AND array::len(SELECT id FROM extraction_run
		WHERE run_id = $probe_run_id AND id != $probe_rid LIMIT 1) = 0`

func addProbeVars(vars map[string]any, runID string) {
	vars["probe_run_id"] = runID
	vars["probe_rid"] = extractionRunID(runID)
}

func (s *Surreal) getRun(ctx context.Context, runID string) (*ExtractionRun, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, ErrNotFound
	}
	vars := map[string]any{
		"rid":                     extractionRunID(runID),
		"store_schema_version":    evidenceStoreSchemaVersion,
		"evidence_format_version": evidenceFormatVersion,
	}
	addProbeVars(vars, runID)
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
		`SELECT * FROM $rid
			WHERE store_schema_version = $store_schema_version
				  AND evidence_format_version = $evidence_format_version
				  AND type::is_string(unit_digest)
				  AND retention_quarantined = false
				  AND run_id = record::id(id)
				  AND `+evidenceRunProbeHasNoClaimantSQL+`
				  AND ((status = 'published' AND published_key != NONE)
				OR (status != 'published' AND published_key = NONE))`, vars)
	if err != nil {
		return nil, fmt.Errorf("get extraction run: %w", err)
	}
	rows := firstExtractionRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	run := rows[0].run()
	return &run, nil
}

type extractionRunIdentityRec struct {
	RecID *models.RecordID `json:"id"`
}

// extractionRunExists intentionally decodes only the physical identity. It is
// used to distinguish a hidden incompatible/quarantined row from a missing
// row without decoding legacy or future payload fields.
func (s *Surreal) extractionRunExists(ctx context.Context, runID string) (bool, error) {
	results, err := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
		"SELECT id FROM $rid", map[string]any{"rid": extractionRunID(runID)})
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

func normalizeAtom(a EvidenceAtom, now time.Time) (EvidenceAtom, error) {
	if a.SchemaVersion == "" || a.BlobDigest == "" || a.RuleID == "" ||
		a.ExtractorVersion == "" || a.AdapterConfigDigest == "" || a.FactFingerprint == "" {
		return EvidenceAtom{}, errors.New("all atom identity fields are required")
	}
	if a.StartByte < 0 || a.EndByte <= a.StartByte {
		return EvidenceAtom{}, fmt.Errorf("invalid byte span [%d,%d)", a.StartByte, a.EndByte)
	}
	for name, value := range map[string]string{
		"schema_version": a.SchemaVersion, "blob_digest": a.BlobDigest,
		"rule_id": a.RuleID, "extractor_version": a.ExtractorVersion,
		"adapter_config_digest": a.AdapterConfigDigest, "fact_fingerprint": a.FactFingerprint,
	} {
		if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes {
			return EvidenceAtom{}, fmt.Errorf("%s is not valid bounded UTF-8", name)
		}
	}
	want := ComputeAtomID(a)
	if a.ID != "" && a.ID != want {
		return EvidenceAtom{}, fmt.Errorf("atom id %q does not match content id %q", a.ID, want)
	}
	a.ID = want
	// Store receipt time is authoritative; extractors cannot backdate proof.
	a.FirstSeen = now
	return a, nil
}

func validTier(tier string) bool {
	return tier == TierExact || tier == TierDerived || tier == TierHeuristic || tier == TierUnresolved
}

// Gitlink-boundary sample bounds mirror the trusted walker's (T19.8).
const (
	maxGitlinkSamplePaths = 64
	maxGitlinkSampleBytes = 4 << 10
	maxInventoryPolicyLen = 128
)

// validateGitlinkBoundaries checks canonical digest shape, bounds, sorted
// uniqueness, and count/sample consistency. The store cannot independently
// recompute the Git tree; recalculation authority remains the trusted walker.
// A run without an inventory policy is legacy and must carry no boundary
// fields — absence of the policy means boundary status is unknown, not zero.
func validateGitlinkBoundaries(coverage CoverageManifest) error {
	if coverage.InventoryPolicy == "" {
		if coverage.GitlinkCount != 0 || coverage.GitlinkDigest != "" ||
			len(coverage.GitlinkSamplePaths) != 0 || coverage.GitlinkSampleTruncated {
			return errors.New("gitlink boundary fields require an inventory policy")
		}
		return nil
	}
	if len(coverage.InventoryPolicy) > maxInventoryPolicyLen ||
		!utf8.ValidString(coverage.InventoryPolicy) ||
		strings.TrimSpace(coverage.InventoryPolicy) != coverage.InventoryPolicy {
		return errors.New("inventory policy is not a bounded token")
	}
	if coverage.GitlinkCount < 0 || coverage.GitlinkCount > maxCoverageFileCount {
		return errors.New("gitlink count is negative or unbounded")
	}
	if !validSHA256Digest(coverage.GitlinkDigest) {
		return errors.New("gitlink digest must be canonical sha256")
	}
	if len(coverage.GitlinkSamplePaths) > maxGitlinkSamplePaths ||
		len(coverage.GitlinkSamplePaths) > coverage.GitlinkCount {
		return errors.New("gitlink sample exceeds its bounds or its count")
	}
	sampleBytes := 0
	for index, samplePath := range coverage.GitlinkSamplePaths {
		if samplePath == "" || !utf8.ValidString(samplePath) ||
			len(samplePath) > maxEvidencePathBytes {
			return errors.New("gitlink sample path is not valid bounded UTF-8")
		}
		if index > 0 && coverage.GitlinkSamplePaths[index-1] >= samplePath {
			return errors.New("gitlink sample paths must be sorted and unique")
		}
		sampleBytes += len(samplePath)
	}
	if sampleBytes > maxGitlinkSampleBytes {
		return errors.New("gitlink sample exceeds its byte bound")
	}
	if coverage.GitlinkSampleTruncated && coverage.GitlinkCount <= len(coverage.GitlinkSamplePaths) {
		return errors.New("gitlink sample truncation is inconsistent with its count")
	}
	return nil
}

func validateCandidateCoverage(coverage CoverageManifest, run *ExtractionRun) error {
	hasCandidateScope := coverage.CandidateManifestDigest != ""
	if !hasCandidateScope {
		if coverage.ScopePosture != "" || coverage.CandidatePlane != "" ||
			coverage.ScopeCorpusFileCount != 0 ||
			coverage.ScopeCorpusDeclaredBytes != 0 ||
			coverage.ScopeCorpusDigest != "" ||
			coverage.PlannedFileCount != 0 ||
			coverage.PlannedRequiredFileCount != 0 ||
			coverage.PlannedDeclaredBytes != 0 ||
			coverage.PlannedScopeDigest != "" ||
			coverage.ExcludedSourceFileCount != 0 ||
			coverage.ExcludedSourceRequiredCount != 0 ||
			coverage.ExcludedSourceDeclaredBytes != 0 ||
			coverage.ExcludedSCIPDocumentCount != 0 ||
			coverage.ExcludedSCIPDefinitionCount != 0 ||
			coverage.ExcludedSCIPOccurrenceCount != 0 ||
			coverage.TypedInputKind != "" ||
			coverage.TypedInputPath != "" ||
			coverage.TypedInputObjectID != "" ||
			coverage.TypedInputDeclaredBytes != 0 ||
			coverage.TypedInputDigest != "" ||
			coverage.TypedInputPresent {
			return errors.New("candidate coverage fields require a manifest digest")
		}
		if run != nil && run.UnitDigest != "" {
			return errors.New("focused coverage requires a candidate manifest digest")
		}
		return nil
	}
	if run == nil {
		return errors.New("candidate coverage requires an extraction run")
	}
	if !validSHA256Digest(coverage.CandidateManifestDigest) ||
		!validSHA256Digest(coverage.ScopeCorpusDigest) ||
		!validSHA256Digest(coverage.PlannedScopeDigest) {
		return errors.New("candidate coverage digests must be canonical sha256")
	}
	if coverage.CandidatePlane != "local" &&
		coverage.CandidatePlane != "repository" &&
		coverage.CandidatePlane != "caller" {
		return errors.New("candidate coverage has an unknown plane")
	}
	expectedPosture := "whole-repository"
	if run.UnitDigest != "" {
		expectedPosture = "repository-overlay"
		if coverage.CandidatePlane == "local" {
			expectedPosture = "focused-local"
		}
	}
	if coverage.ScopePosture != expectedPosture {
		return errors.New("candidate coverage posture disagrees with the run scope")
	}
	if coverage.ScopeCorpusFileCount < 0 ||
		coverage.ScopeCorpusFileCount > maxCoverageFileCount ||
		coverage.ScopeCorpusFileCount != coverage.CorpusFileCount ||
		coverage.ScopeCorpusDeclaredBytes < 0 ||
		coverage.ScopeCorpusDeclaredBytes > maxCoverageReadBytes ||
		coverage.PlannedFileCount < 0 ||
		coverage.PlannedFileCount > coverage.ScopeCorpusFileCount ||
		coverage.PlannedRequiredFileCount < 0 ||
		coverage.PlannedRequiredFileCount > coverage.PlannedFileCount ||
		coverage.PlannedDeclaredBytes < 0 ||
		coverage.PlannedDeclaredBytes > coverage.ScopeCorpusDeclaredBytes {
		return errors.New("candidate coverage counts or bytes are inconsistent")
	}
	if coverage.ExcludedSourceFileCount < 0 ||
		coverage.ExcludedSourceFileCount > coverage.PlannedFileCount ||
		coverage.ExcludedSourceRequiredCount < 0 ||
		coverage.ExcludedSourceRequiredCount >
			coverage.ExcludedSourceFileCount ||
		coverage.ExcludedSourceRequiredCount >
			coverage.PlannedRequiredFileCount ||
		coverage.ExcludedSourceDeclaredBytes < 0 ||
		coverage.ExcludedSourceDeclaredBytes >
			coverage.PlannedDeclaredBytes ||
		coverage.ExcludedSourceFileCount == 0 &&
			coverage.ExcludedSourceDeclaredBytes != 0 ||
		coverage.CandidateFileCount !=
			coverage.PlannedRequiredFileCount-
				coverage.ExcludedSourceRequiredCount ||
		coverage.ExcludedSCIPDocumentCount < 0 ||
		coverage.ExcludedSCIPDocumentCount > maxCoverageFileCount ||
		coverage.ExcludedSCIPDefinitionCount < 0 ||
		coverage.ExcludedSCIPDefinitionCount > maxCoverageFileCount ||
		coverage.ExcludedSCIPOccurrenceCount < 0 ||
		coverage.ExcludedSCIPOccurrenceCount > maxCoverageFileCount ||
		coverage.ExcludedSCIPDefinitionCount >
			coverage.ExcludedSCIPOccurrenceCount ||
		coverage.ExcludedSCIPDocumentCount == 0 &&
			(coverage.ExcludedSCIPDefinitionCount != 0 ||
				coverage.ExcludedSCIPOccurrenceCount != 0) {
		return errors.New("candidate exclusion coverage is inconsistent")
	}
	hasExclusions := coverage.ExcludedSourceFileCount != 0 ||
		coverage.ExcludedSourceRequiredCount != 0 ||
		coverage.ExcludedSourceDeclaredBytes != 0 ||
		coverage.ExcludedSCIPDocumentCount != 0 ||
		coverage.ExcludedSCIPDefinitionCount != 0 ||
		coverage.ExcludedSCIPOccurrenceCount != 0
	if hasExclusions && coverage.ScopePosture != "focused-local" {
		return errors.New("candidate exclusions require focused-local coverage")
	}
	hasSCIPExclusions := coverage.ExcludedSCIPDocumentCount != 0 ||
		coverage.ExcludedSCIPDefinitionCount != 0 ||
		coverage.ExcludedSCIPOccurrenceCount != 0
	if hasSCIPExclusions && coverage.TypedInputKind != "scip" {
		return errors.New("SCIP exclusions require a SCIP typed input")
	}
	if coverage.TypedInputKind == "" {
		if coverage.TypedInputPath != "" ||
			coverage.TypedInputObjectID != "" ||
			coverage.TypedInputDeclaredBytes != 0 ||
			coverage.TypedInputDigest != "" ||
			coverage.TypedInputPresent {
			return errors.New("typed-input coverage requires a kind")
		}
		return nil
	}
	if coverage.TypedInputKind != "scip" {
		return errors.New("candidate coverage has an unknown typed-input kind")
	}
	if coverage.TypedInputPath != "" {
		if err := validateEvidencePath(coverage.TypedInputPath); err != nil {
			return fmt.Errorf("typed-input coverage: %w", err)
		}
	}
	if !coverage.TypedInputPresent {
		if coverage.TypedInputObjectID != "" ||
			coverage.TypedInputDeclaredBytes != 0 ||
			coverage.TypedInputDigest != "" {
			return errors.New("absent typed-input coverage carries content identity")
		}
		return nil
	}
	if coverage.TypedInputPath == "" ||
		!validCoverageGitObjectID(coverage.TypedInputObjectID) ||
		coverage.TypedInputDeclaredBytes < 0 ||
		coverage.TypedInputDeclaredBytes > maxCoverageReadBytes ||
		!validSHA256Digest(coverage.TypedInputDigest) {
		return errors.New("present typed-input coverage is incomplete or unbounded")
	}
	return nil
}

// validatePublishedCoverage is the shared write/read admission boundary for a
// published run's honesty record. Publication rejects malformed coverage
// before visibility flips, and reads repeat the same checks so raw-row damage
// cannot turn into a false no-op, certificate, or API response.
func validatePublishedCoverage(
	coverage CoverageManifest,
	run *ExtractionRun,
) error {
	if run == nil {
		return errors.New("coverage requires an extraction run")
	}
	if coverage.AssertionCount < 0 ||
		coverage.AtomCount < 0 ||
		coverage.UnresolvedCount < 0 {
		return errors.New("coverage counts cannot be negative")
	}
	if coverage.CorpusFileCount < 0 ||
		coverage.CandidateFileCount < 0 ||
		coverage.ReadFileCount < 0 ||
		coverage.ReadBytes < 0 ||
		coverage.CandidateFileCount > coverage.ReadFileCount ||
		coverage.ReadFileCount > coverage.CorpusFileCount ||
		coverage.CorpusFileCount > maxCoverageFileCount ||
		coverage.ReadBytes > maxCoverageReadBytes {
		return errors.New("invalid or unbounded source coverage")
	}
	if !validSHA256Digest(coverage.SourceScopeDigest) {
		return errors.New("source_scope_digest must be canonical sha256")
	}
	if len(coverage.Protocols) > maxEvidenceRefsPerAssertion ||
		len(coverage.Failures) > maxEvidenceRefsPerAssertion {
		return fmt.Errorf(
			"coverage arrays exceed %d entries",
			maxEvidenceRefsPerAssertion,
		)
	}
	if len(coverage.Failures) > 0 {
		return errors.New("coverage contains extraction failures")
	}
	for _, value := range append(
		append([]string(nil), coverage.Protocols...),
		coverage.Failures...,
	) {
		if !utf8.ValidString(value) ||
			len(value) > maxEvidenceIdentityBytes {
			return errors.New(
				"coverage entry is not valid bounded UTF-8",
			)
		}
	}
	if err := validateGitlinkBoundaries(coverage); err != nil {
		return err
	}
	return validateCandidateCoverage(coverage, run)
}

func (s *Surreal) validatePublishedCandidatePointer(
	ctx context.Context,
	run *ExtractionRun,
) error {
	if run == nil || run.Coverage.CandidateManifestDigest == "" {
		return nil
	}
	repository, err := s.GetRepo(ctx, run.Repo)
	if err != nil {
		return fmt.Errorf(
			"candidate publication repository state: %w",
			err,
		)
	}
	currentUnitDigest := ""
	if repository.IndexedAnalysisUnit != nil {
		currentUnitDigest = repository.IndexedAnalysisUnit.Digest
	}
	// Historical exact-scope runs remain readable for rollback and retained
	// proof. The singleton candidate pointer represents only the repository's
	// current indexed commit/unit, so it can corroborate current runs only.
	if repository.IndexedCommitHash != run.Commit ||
		currentUnitDigest != run.UnitDigest {
		return nil
	}
	publication, err := s.GetCandidateManifestPublication(
		ctx,
		run.Repo,
	)
	if err != nil {
		return fmt.Errorf("candidate publication receipt: %w", err)
	}
	if publication.Repository != run.Repo ||
		publication.HeadCommit != run.Commit ||
		publication.UnitDigest != run.UnitDigest ||
		publication.ManifestDigest !=
			run.Coverage.CandidateManifestDigest {
		return errors.New(
			"candidate publication receipt disagrees with the current pointer",
		)
	}
	return nil
}

func validCoverageGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && "sha256:"+hex.EncodeToString(decoded) == value
}

func canonicalAtomRefs(refs []string) ([]string, error) {
	copyOfRefs := make([]string, len(refs))
	copy(copyOfRefs, refs)
	refs = copyOfRefs
	for _, ref := range refs {
		if ref == "" {
			return nil, errors.New("empty atom reference")
		}
	}
	slices.Sort(refs)
	return slices.Compact(refs), nil
}

func mergeAtomRefs(a, b []string) []string {
	// Keep the canonical empty set non-nil. The SurrealDB SDK serializes a
	// nil slice as NULL, which is not a valid array::union operand. This path
	// is exercised when two source atoms support one semantic assertion.
	merged := make([]string, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	slices.Sort(merged)
	return slices.Compact(merged)
}

func atomRefsOverlap(a, b []string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			return true
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return false
}

func validateEvidencePath(value string) error {
	if value == "" || len(value) > maxEvidencePathBytes || !utf8.ValidString(value) ||
		strings.HasPrefix(value, "/") || strings.HasPrefix(value, "-") || strings.Contains(value, `\`) {
		return errors.New("path is not a bounded repository-relative UTF-8 path")
	}
	if path.Clean(value) != value {
		return errors.New("path is not canonical")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("path contains a non-canonical component")
		}
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errors.New("path contains control bytes")
		}
	}
	return nil
}

type normalizedEvidenceBatch struct {
	atoms   []map[string]any
	assocs  []map[string]any
	asserts []map[string]any
}

func normalizeEvidenceBatch(run *ExtractionRun, atoms []EvidenceAtom, assocs []SnapshotEvidence, asserts []Assertion, now time.Time) (normalizedEvidenceBatch, error) {
	batch := normalizedEvidenceBatch{}
	if len(atoms) > maxEvidenceBatchRows || len(assocs) > maxEvidenceBatchRows || len(asserts) > maxEvidenceBatchRows {
		return batch, fmt.Errorf("evidence batch exceeds %d rows of one kind", maxEvidenceBatchRows)
	}
	referenceEdges := 0
	for _, assertion := range asserts {
		referenceEdges += len(assertion.Supporting) + len(assertion.Contradicting)
		if referenceEdges > maxEvidenceReferenceEdges {
			return batch, fmt.Errorf("evidence batch exceeds %d assertion-reference edges", maxEvidenceReferenceEdges)
		}
	}
	atomIDs := make(map[string]bool, len(atoms))
	for i, input := range atoms {
		a, err := normalizeAtom(input, now)
		if err != nil {
			return batch, fmt.Errorf("atom %d: %w", i, err)
		}
		if atomIDs[a.ID] {
			continue
		}
		atomIDs[a.ID] = true
		batch.atoms = append(batch.atoms, map[string]any{
			"rid": evidenceAtomRecordID(a.ID), "atom_id": a.ID,
			"schema_version": a.SchemaVersion, "blob_digest": a.BlobDigest,
			"start_byte": a.StartByte, "end_byte": a.EndByte,
			"rule_id": a.RuleID, "extractor_version": a.ExtractorVersion,
			"adapter_config_digest": a.AdapterConfigDigest,
			"fact_fingerprint":      a.FactFingerprint, "first_seen": a.FirstSeen,
		})
	}

	assocIDs := make(map[string]bool, len(assocs))
	associatedAtoms := make(map[string]bool, len(assocs))
	visibility := "repo:" + run.Repo
	for i, input := range assocs {
		e := input
		if e.RunID != "" && e.RunID != run.ID {
			return batch, fmt.Errorf("association %d: run_id %q does not match run %q", i, e.RunID, run.ID)
		}
		if e.Repo != run.Repo || e.Commit != run.Commit {
			return batch, fmt.Errorf("association %d: repo/commit does not match run", i)
		}
		if e.VisibilityScope != visibility {
			return batch, fmt.Errorf("association %d: visibility_scope must be %q", i, visibility)
		}
		if err := validateEvidencePath(e.Path); err != nil {
			return batch, fmt.Errorf("association %d: %w", i, err)
		}
		if e.StartLine < 0 || e.EndLine < e.StartLine {
			return batch, fmt.Errorf("association %d: invalid line span", i)
		}
		if !atomIDs[e.AtomID] {
			return batch, fmt.Errorf("association %d: atom %q is not present in this batch", i, e.AtomID)
		}
		e.RunID = run.ID
		e.ObservedAt = now
		want := ComputeSnapshotEvidenceID(e)
		if e.ID != "" && e.ID != want {
			return batch, fmt.Errorf("association %d: occurrence id %q does not match %q", i, e.ID, want)
		}
		e.ID = want
		if assocIDs[e.ID] {
			continue
		}
		assocIDs[e.ID] = true
		associatedAtoms[e.AtomID] = true
		associationKey := hashIdentity("association_", run.ID, e.ID)
		batch.assocs = append(batch.assocs, map[string]any{
			"rid":           snapshotEvidenceRecordID(run.ID, e.ID),
			"occurrence_id": e.ID, "association_key": associationKey,
			"atom_id": e.AtomID, "atom_record": evidenceAtomRecordID(e.AtomID),
			"repo": e.Repo, "commit": e.Commit,
			"path": e.Path, "start_line": e.StartLine, "end_line": e.EndLine,
			"visibility_scope": e.VisibilityScope, "run_id": run.ID,
			"observed_at": e.ObservedAt,
		})
	}
	for atomID := range atomIDs {
		if !associatedAtoms[atomID] {
			return batch, fmt.Errorf("atom %q has no occurrence association in this batch", atomID)
		}
	}

	normalized := make(map[string]Assertion, len(asserts))
	var assertionOrder []string
	for i, input := range asserts {
		a := input
		if a.RunID != "" && a.RunID != run.ID {
			return batch, fmt.Errorf("assertion %d: run_id %q does not match run %q", i, a.RunID, run.ID)
		}
		if a.Repo != run.Repo {
			return batch, fmt.Errorf("assertion %d: repo %q does not match run repo %q", i, a.Repo, run.Repo)
		}
		if a.Predicate == "" || a.Subject == "" || a.Object == "" || !validTier(a.Tier) {
			return batch, fmt.Errorf("assertion %d: predicate, subject, object, and a valid tier are required", i)
		}
		for name, value := range map[string]string{
			"predicate": a.Predicate, "subject": a.Subject, "object": a.Object,
			"lineage": a.Lineage, "code_role": a.CodeRole, "detail": a.Detail,
		} {
			if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes {
				return batch, fmt.Errorf("assertion %d: %s is not valid bounded UTF-8", i, name)
			}
		}
		if len(a.Supporting) > maxEvidenceRefsPerAssertion || len(a.Contradicting) > maxEvidenceRefsPerAssertion {
			return batch, fmt.Errorf("assertion %d: more than %d evidence references", i, maxEvidenceRefsPerAssertion)
		}
		var err error
		a.Supporting, err = canonicalAtomRefs(a.Supporting)
		if err != nil {
			return batch, fmt.Errorf("assertion %d supporting: %w", i, err)
		}
		a.Contradicting, err = canonicalAtomRefs(a.Contradicting)
		if err != nil {
			return batch, fmt.Errorf("assertion %d contradicting: %w", i, err)
		}
		if len(a.Supporting)+len(a.Contradicting) == 0 {
			return batch, fmt.Errorf("assertion %d: at least one evidence reference is required", i)
		}
		seen := map[string]bool{}
		for _, ref := range a.Supporting {
			if !associatedAtoms[ref] {
				return batch, fmt.Errorf("assertion %d: supporting atom %q has no association in this batch", i, ref)
			}
			seen[ref] = true
		}
		for _, ref := range a.Contradicting {
			if !associatedAtoms[ref] {
				return batch, fmt.Errorf("assertion %d: contradicting atom %q has no association in this batch", i, ref)
			}
			if seen[ref] {
				return batch, fmt.Errorf("assertion %d: atom %q cannot both support and contradict", i, ref)
			}
		}
		a.RunID = run.ID
		want := ComputeAssertionID(a)
		if a.ID != "" && a.ID != want {
			return batch, fmt.Errorf("assertion %d: semantic id %q does not match %q", i, a.ID, want)
		}
		a.ID = want
		if previous, ok := normalized[a.ID]; ok {
			if previous.Tier != a.Tier || previous.CodeRole != a.CodeRole || previous.Detail != a.Detail {
				return batch, fmt.Errorf("assertion %d: duplicate semantic id has conflicting attributes", i)
			}
			previous.Supporting = mergeAtomRefs(previous.Supporting, a.Supporting)
			previous.Contradicting = mergeAtomRefs(previous.Contradicting, a.Contradicting)
			if atomRefsOverlap(previous.Supporting, previous.Contradicting) {
				return batch, fmt.Errorf("assertion %d: merged evidence both supports and contradicts the claim", i)
			}
			normalized[a.ID] = previous
			continue
		}
		normalized[a.ID] = a
		assertionOrder = append(assertionOrder, a.ID)
	}
	for _, id := range assertionOrder {
		a := normalized[id]
		assertionKey := hashIdentity("assertion_", run.ID, a.ID)
		attributeKey := hashIdentity("assertion_attributes_", a.Tier, a.CodeRole, a.Detail)
		batch.asserts = append(batch.asserts, map[string]any{
			"rid":          assertionRecordID(run.ID, a.ID),
			"assertion_id": a.ID, "assertion_key": assertionKey,
			"attribute_key": attributeKey,
			"predicate":     a.Predicate, "subject": a.Subject, "object": a.Object,
			"lineage": a.Lineage, "tier": a.Tier, "code_role": a.CodeRole,
			"repo": a.Repo, "supporting": a.Supporting,
			"contradicting": a.Contradicting, "run_id": run.ID, "detail": a.Detail,
		})
	}
	return batch, nil
}

const addEvidenceSQL = `
BEGIN;
LET $locked = UPDATE $run SET staged_revision += 1
    WHERE status = 'staged'
      AND array::len(SELECT id FROM $migration_rid
	      WHERE version = $evidence_migration_version LIMIT 1) = 1
      AND store_schema_version = $store_schema_version
	      AND evidence_format_version = $evidence_format_version
		  AND type::is_string(unit_digest)
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
		  AND published_key = NONE
      AND retention_quarantined = false RETURN AFTER;
LET $safe_atoms = IF array::len($locked) = 1 THEN $atoms ELSE [] END;
FOR $a IN $safe_atoms {
    UPSERT $a.rid SET atom_id = $a.atom_id, schema_version = $a.schema_version,
        blob_digest = $a.blob_digest, start_byte = $a.start_byte, end_byte = $a.end_byte,
        rule_id = $a.rule_id, extractor_version = $a.extractor_version,
        adapter_config_digest = $a.adapter_config_digest, fact_fingerprint = $a.fact_fingerprint,
        first_seen = IF first_seen = NONE THEN $a.first_seen ELSE first_seen END,
        retention_revision = (retention_revision ?? 0) + 1 RETURN NONE
};
LET $safe_assocs = IF array::len($locked) = 1 THEN $assocs ELSE [] END;
FOR $e IN $safe_assocs {
    UPSERT $e.rid SET occurrence_id = $e.occurrence_id, association_key = $e.association_key,
        atom_id = $e.atom_id, atom_record = $e.atom_record,
        repo = $e.repo, commit = $e.commit, path = $e.path,
        start_line = $e.start_line, end_line = $e.end_line,
        visibility_scope = $e.visibility_scope, run_id = $e.run_id,
        observed_at = IF observed_at = NONE THEN $e.observed_at ELSE observed_at END RETURN NONE
};
LET $safe_asserts = IF array::len($locked) = 1 THEN $asserts ELSE [] END;
FOR $x IN $safe_asserts {
    LET $existing = (SELECT attribute_key, supporting, contradicting
        FROM assertion WHERE id = $x.rid LIMIT 1)[0];
    IF $existing != NONE AND
       ($existing.attribute_key = NONE OR $existing.attribute_key != $x.attribute_key) {
        THROW 'phebs-permanent: duplicate semantic assertion has conflicting attributes'
    };
    IF $existing != NONE AND
       (array::len(array::intersect($existing.supporting ?? [], $x.contradicting)) > 0 OR
        array::len(array::intersect($existing.contradicting ?? [], $x.supporting)) > 0) {
        THROW 'phebs-permanent: assertion evidence cannot both support and contradict'
    };
    UPSERT $x.rid SET assertion_id = $x.assertion_id, assertion_key = $x.assertion_key,
        attribute_key = $x.attribute_key,
        predicate = $x.predicate, subject = $x.subject, object = $x.object,
        lineage = $x.lineage, tier = $x.tier, code_role = $x.code_role, repo = $x.repo,
        supporting = array::union(supporting ?? [], $x.supporting ?? []),
        contradicting = array::union(contradicting ?? [], $x.contradicting ?? []),
        run_id = $x.run_id, detail = $x.detail RETURN NONE
};
LET $run_rows = array::len(SELECT VALUE id FROM snapshot_evidence WHERE run_id = $run_id) +
    array::len(SELECT VALUE id FROM assertion WHERE run_id = $run_id);
LET $reference_edges =
    array::len(array::flatten(SELECT VALUE supporting FROM assertion WHERE run_id = $run_id)) +
    array::len(array::flatten(SELECT VALUE contradicting FROM assertion WHERE run_id = $run_id));
IF $run_rows > $max_run_rows OR $reference_edges > $max_reference_edges {
    THROW 'phebs-permanent: evidence run exceeds a bounded storage limit'
};
RETURN IF array::len($locked) = 1 THEN $locked ELSE [] END;
COMMIT;`

// AddEvidence writes a self-contained batch under a staged run. Updating the
// run record is the serialization point against publish, abort, deletion, and
// sweep; a transaction that loses that race cannot append visible rows.
func (s *Surreal) AddEvidence(ctx context.Context, runID string, atoms []EvidenceAtom, assocs []SnapshotEvidence, asserts []Assertion) error {
	run, err := s.getRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("add evidence: %w", err)
	}
	if run.Status != "staged" {
		return fmt.Errorf("add evidence: run %s is %s, not staged: %w", runID, run.Status, ErrConflict)
	}
	now := time.Now().UTC()
	batch, err := normalizeEvidenceBatch(run, atoms, assocs, asserts, now)
	if err != nil {
		return fmt.Errorf("add evidence to run %s: %w", runID, err)
	}
	vars := map[string]any{
		"run": extractionRunID(runID), "run_id": runID, "atoms": batch.atoms,
		"assocs": batch.assocs, "asserts": batch.asserts,
		"max_run_rows": maxEvidenceRowsPerRun, "max_reference_edges": maxEvidenceReferenceEdges,
		"migration_rid":              evidenceMigrationStateID(),
		"store_schema_version":       evidenceStoreSchemaVersion,
		"evidence_format_version":    evidenceFormatVersion,
		"evidence_migration_version": evidenceMigrationVersion,
	}
	addProbeVars(vars, runID)
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]extractionRunRec](ctx, s.db, addEvidenceSQL, vars)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			msg := queryErr.Error()
			if strings.Contains(msg, "conflicting attributes") ||
				strings.Contains(msg, "support and contradict") {
				return fmt.Errorf("add evidence to run %s: %w: %v", runID, ErrConflict, queryErr)
			}
			return fmt.Errorf("add evidence to run %s: %w", runID, queryErr)
		}
		if len(firstExtractionRows(results)) == 1 {
			return nil
		}
		current, stateErr := s.getRun(ctx, runID)
		if errors.Is(stateErr, ErrNotFound) {
			return fmt.Errorf("add evidence: run %s: %w", runID, ErrNotFound)
		}
		if stateErr != nil {
			return fmt.Errorf("add evidence: check run %s: %w", runID, stateErr)
		}
		return fmt.Errorf("add evidence: run %s is %s, not staged: %w", runID, current.Status, ErrConflict)
	}
}

const publishExtractionRunSQL = `
BEGIN;
LET $locked = UPDATE $rid SET staged_revision += 1
    WHERE status = 'staged'
      AND array::len(SELECT id FROM $migration_rid
	      WHERE version = $evidence_migration_version LIMIT 1) = 1
      AND store_schema_version = $store_schema_version
	      AND evidence_format_version = $evidence_format_version
		  AND type::is_string(unit_digest)
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
		  AND published_key = NONE
	  AND retention_quarantined = false
	  AND array::len(SELECT id FROM $attempt_rid
	      WHERE run_id = $run_id AND status = 'staged'
			AND repo = $repo AND commit = $commit
			AND unit_digest = $unit_digest AND domain = $domain
			AND store_schema_version = $store_schema_version
			AND evidence_format_version = $evidence_format_version
			AND evidence_migration_version = $evidence_migration_version) = 1 RETURN AFTER;
LET $assertion_count = array::len(SELECT VALUE assertion_id FROM assertion WHERE run_id = $run_id);
LET $association_count = array::len(SELECT VALUE occurrence_id FROM snapshot_evidence WHERE run_id = $run_id);
LET $atom_count = array::len(array::distinct(SELECT VALUE atom_id FROM snapshot_evidence WHERE run_id = $run_id));
LET $unresolved_count = array::len(SELECT VALUE assertion_id FROM assertion
    WHERE run_id = $run_id AND tier = 'unresolved');
LET $max_occurrences = ((SELECT atom_id, count() AS count FROM snapshot_evidence
    WHERE run_id = $run_id GROUP BY atom_id ORDER BY count DESC LIMIT 1)[0].count ?? 0);
LET $reference_edges =
    array::len(array::flatten(SELECT VALUE supporting FROM assertion WHERE run_id = $run_id)) +
    array::len(array::flatten(SELECT VALUE contradicting FROM assertion WHERE run_id = $run_id));
LET $counts_ok = $assertion_count = $want_assertions AND $atom_count = $want_atoms
	AND $unresolved_count = $want_unresolved AND $max_occurrences <= $max_occurrences_per_atom
	AND ($association_count + $assertion_count) <= $max_run_rows
	AND $reference_edges <= $max_reference_edges;
LET $candidate = (SELECT repository, head_commit, unit_digest, policy_digest,
	manifest_digest, control_revision FROM $candidate_rid)[0];
LET $candidate_ok = IF $write_outcome = false
		OR $outcome_candidate_manifest_digest = '' THEN true
	ELSE $candidate != NONE
		AND $candidate.repository = $repo
		AND $candidate.head_commit = $commit
		AND $candidate.unit_digest = $unit_digest
		AND $candidate.policy_digest = $outcome_candidate_policy_digest
		AND $candidate.manifest_digest = $outcome_candidate_manifest_digest
		AND $candidate.control_revision =
			$outcome_candidate_control_revision
	END;
LET $repo_ok = IF array::len($locked) = 1 AND $counts_ok
		AND $candidate_ok THEN
    (UPDATE $repo_rid SET evidence_revision = (evidence_revision ?? 0) + 1
        WHERE (deleting = NONE OR deleting = false)
		  AND indexed_commit_hash = $commit
		  AND (indexed_analysis_unit.digest ?? '') = $unit_digest RETURN AFTER)
    ELSE [] END;
LET $ready = array::len($locked) = 1 AND array::len($repo_ok) = 1
	AND $counts_ok AND $candidate_ok;
LET $old = IF $ready THEN
    (UPDATE extraction_run SET status = 'superseded', published_key = NONE
        WHERE repo = $repo AND commit = $commit AND unit_digest = $unit_digest
		  AND domain = $domain AND status = 'published' AND run_id != $run_id
	          AND store_schema_version = $store_schema_version
	          AND evidence_format_version = $evidence_format_version
			  AND run_id = record::id(id) AND published_key = $published_key
			  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
	          AND retention_quarantined = false RETURN AFTER)
    ELSE [] END;
LET $published = IF $ready THEN
    (UPDATE $rid SET status = 'published', published_key = $published_key,
			published_at = $now, coverage = $coverage
			WHERE status = 'staged' AND run_id = record::id(id)
			  AND ` + evidenceRunProbeHasNoClaimantSQL + `
			  AND published_key = NONE RETURN AFTER)
    ELSE [] END;
LET $attempt = IF array::len($published) = 1 THEN
	(UPDATE $attempt_rid SET status = 'published'
		WHERE run_id = $run_id AND status = 'staged'
		  AND repo = $repo AND commit = $commit
		  AND unit_digest = $unit_digest AND domain = $domain
		  AND store_schema_version = $store_schema_version
		  AND evidence_format_version = $evidence_format_version
		  AND evidence_migration_version = $evidence_migration_version RETURN AFTER)
	ELSE [] END;
LET $outcome = IF array::len($attempt) != 1 THEN []
	ELSE IF $write_outcome = false THEN $published
	ELSE
		(UPSERT $outcome_rid CONTENT {
			repo: $repo,
			commit: $commit,
			unit_digest: $unit_digest,
			domain: $domain,
			disposition: $outcome_disposition,
			generation: $outcome_generation,
			candidate_control_failure: false,
			run_id: $run_id,
			receipt_schema: $outcome_receipt_schema,
			receipt: $outcome_receipt,
			recorded_at: $now,
			store_schema_version: $store_schema_version,
			evidence_migration_version: $evidence_migration_version
		} RETURN AFTER)
	END;
LET $catalog = (SELECT declarations FROM $catalog_rid)[0];
LET $catalog_depends = $catalog != NONE
	AND $domain IN $catalog.declarations.map(
		|$declaration| $declaration.domain);
LET $catalog_relevant = $write_outcome
	AND $domain IN ['proto-contract', 'thrift-contract'];
LET $catalog_trigger = $catalog_depends OR $catalog_relevant;
LET $retired_catalog = IF array::len($outcome) = 1
		AND $catalog_trigger AND $catalog != NONE THEN
	(DELETE $catalog_rid RETURN BEFORE)
	ELSE [] END;
LET $pending_catalog = IF array::len($outcome) = 1
		AND $catalog_trigger THEN
	(SELECT id, created_at FROM resolver_catalog_job
		WHERE pending_key = $repo AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $catalog_fanout = IF array::len($outcome) != 1
		OR $catalog_trigger = false THEN []
	ELSE IF $pending_catalog != NONE THEN
		(UPDATE $pending_catalog SET force = true,
			recovery_lease = NONE RETURN AFTER)
	ELSE
		(CREATE resolver_catalog_job CONTENT {
			target: $repo,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $repo,
			force: true
		} RETURN AFTER)
	END;
RETURN IF array::len($outcome) = 1
	AND ($catalog_trigger = false OR array::len($catalog_fanout) = 1)
	THEN $published ELSE [] END;
COMMIT;`

// PublishExtractionRun validates and flips visibility in one transaction.
// Touching both run and repo serializes against AddEvidence, reindex, deleting,
// and DeleteRepo. Coverage counts are checked against stored run rows rather
// than trusted from the extractor.
func (s *Surreal) PublishExtractionRun(ctx context.Context, runID string, coverage CoverageManifest) error {
	return s.publishExtractionRun(ctx, runID, coverage, nil)
}

// PublishExtractionRunWithOutcome is the worker-facing publication transition.
// The diagnostic receipt cannot fail after visibility flips: validation and
// bounding happen before the transaction, and the outcome UPSERT is inside the
// same commit as the status/attempt transitions.
func (s *Surreal) PublishExtractionRunWithOutcome(
	ctx context.Context,
	runID string,
	coverage CoverageManifest,
	outcome ExtractionDomainOutcome,
) error {
	prepared, err := prepareExtractionDomainOutcome(outcome)
	if err != nil {
		return fmt.Errorf("publish extraction run outcome: %w", err)
	}
	if prepared.Disposition != DomainOutcomePublished {
		return errors.New(
			"publish extraction run outcome requires published disposition")
	}
	return s.publishExtractionRun(ctx, runID, coverage, &prepared)
}

func (s *Surreal) publishExtractionRun(
	ctx context.Context,
	runID string,
	coverage CoverageManifest,
	outcome *ExtractionDomainOutcome,
) error {
	run, err := s.getRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("publish extraction run: %w", err)
	}
	if run.Status != "staged" {
		return fmt.Errorf("publish extraction run: run %s is %s, not staged: %w", runID, run.Status, ErrConflict)
	}
	if err := validatePublishedCoverage(coverage, run); err != nil {
		return fmt.Errorf("publish extraction run: %w", err)
	}
	scope := scopeForRun(run)
	if outcome != nil &&
		(outcome.RunID != runID || outcome.Scope != scope ||
			outcome.Generation.Extractor != run.Extractor ||
			coverage.CandidateManifestDigest !=
				outcome.Generation.CandidateManifestDigest) {
		return errors.New(
			"publish extraction run outcome disagrees with the staged run")
	}
	vars := map[string]any{
		"rid": extractionRunID(runID), "run_id": runID,
		"attempt_rid":   extractionAttemptID(scope),
		"outcome_rid":   extractionDomainOutcomeID(scope.Repository, scope.Domain),
		"candidate_rid": candidateManifestPublicationID(scope.Repository),
		"catalog_rid":   resolverCatalogPublicationID(scope.Repository),
		"repo_rid":      repoID(run.Repo), "repo": run.Repo, "commit": run.Commit,
		"unit_digest": run.UnitDigest, "domain": run.Domain,
		"published_key": publishedKey(scope),
		"now":           time.Now().UTC(), "coverage": coverage,
		"want_assertions": coverage.AssertionCount, "want_atoms": coverage.AtomCount,
		"want_unresolved":                    coverage.UnresolvedCount,
		"max_occurrences_per_atom":           maxEvidenceOccurrences,
		"max_run_rows":                       maxEvidenceRowsPerRun,
		"max_reference_edges":                maxEvidenceReferenceEdges,
		"migration_rid":                      evidenceMigrationStateID(),
		"store_schema_version":               evidenceStoreSchemaVersion,
		"evidence_format_version":            evidenceFormatVersion,
		"evidence_migration_version":         evidenceMigrationVersion,
		"max_evidence_identity_bytes":        maxEvidenceIdentityBytes,
		"write_outcome":                      outcome != nil,
		"outcome_disposition":                "",
		"outcome_generation":                 ExtractionGenerationIdentity{},
		"outcome_receipt_schema":             "",
		"outcome_receipt":                    "",
		"outcome_candidate_manifest_digest":  "",
		"outcome_candidate_policy_digest":    "",
		"outcome_candidate_control_revision": uint64(0),
	}
	if outcome != nil {
		vars["outcome_disposition"] = string(outcome.Disposition)
		vars["outcome_generation"] = outcome.Generation
		vars["outcome_receipt_schema"] = outcome.ReceiptSchema
		vars["outcome_receipt"] = outcome.Receipt
		vars["outcome_candidate_manifest_digest"] =
			outcome.Generation.CandidateManifestDigest
		vars["outcome_candidate_policy_digest"] =
			outcome.Generation.CandidatePolicyDigest
		vars["outcome_candidate_control_revision"] =
			outcome.Generation.CandidateControlRevision
	}
	addProbeVars(vars, runID)
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]extractionRunRec](ctx, s.db, publishExtractionRunSQL, vars)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("publish extraction run %s: %w", runID, queryErr)
		}
		if len(firstExtractionRows(results)) == 1 {
			return nil
		}
		current, stateErr := s.getRun(ctx, runID)
		if errors.Is(stateErr, ErrNotFound) {
			return fmt.Errorf("publish extraction run %s: %w", runID, ErrNotFound)
		}
		if stateErr != nil {
			return fmt.Errorf("publish extraction run %s: check state: %w", runID, stateErr)
		}
		return fmt.Errorf("publish extraction run %s: run status, repository revision/deletion, or coverage validation changed (status %s): %w", runID, current.Status, ErrConflict)
	}
}

const recordExtractionDomainOutcomeSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
LET $repo_state = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $repo_ok = $repo_state != NONE
	AND ($repo_state.deleting = NONE OR $repo_state.deleting = false)
	AND $repo_state.indexed_commit_hash = $commit
	AND ($repo_state.indexed_analysis_unit.digest ?? '') = $unit_digest;
LET $candidate = (SELECT repository, head_commit, unit_digest, policy_digest,
	manifest_digest, control_revision FROM $candidate_rid)[0];
LET $candidate_ok = IF $candidate_manifest_digest = '' THEN true
	ELSE $candidate != NONE
		AND $candidate.repository = $repo
		AND $candidate.head_commit = $commit
		AND $candidate.unit_digest = $unit_digest
		AND $candidate.policy_digest = $candidate_policy_digest
		AND $candidate.manifest_digest = $candidate_manifest_digest
		AND $candidate.control_revision = $candidate_control_revision
	END;
LET $existing = (SELECT disposition, generation, run_id
	FROM $outcome_rid)[0];
LET $same_generation = $existing != NONE
	AND ($existing.generation.digest ?? '') = $generation.digest;
LET $settled_existing = $same_generation
	AND $existing.disposition IN [
		'published', 'unavailable_prerequisite',
		'terminal_generation_refusal'
	];
LET $attempt = IF $run_id = '' THEN NONE
	ELSE (SELECT run_id, status, repo, commit, unit_digest, domain,
		store_schema_version, evidence_format_version,
		evidence_migration_version
		FROM $attempt_rid)[0] END;
LET $attempt_ok = IF $run_id = '' THEN true
	ELSE $attempt != NONE
		AND $attempt.run_id = $run_id
		AND $attempt.status IN ['staged', 'aborted']
		AND $attempt.repo = $repo
		AND $attempt.commit = $commit
		AND $attempt.unit_digest = $unit_digest
		AND $attempt.domain = $domain
		AND $attempt.store_schema_version = $store_schema_version
		AND $attempt.evidence_format_version = $evidence_format_version
		AND $attempt.evidence_migration_version =
			$evidence_migration_version
	END;
LET $retryable_ok = IF $disposition != 'retryable_failure'
		OR $run_id != '' THEN true
	ELSE $settled_existing = false
		AND ($same_generation = false
			OR ($existing.run_id ?? '') = '')
	END;
LET $recorded = IF $writer_ok AND $repo_ok AND $candidate_ok
		AND $attempt_ok AND $retryable_ok THEN
	(UPSERT $outcome_rid CONTENT {
		repo: $repo,
		commit: $commit,
		unit_digest: $unit_digest,
		domain: $domain,
		disposition: $disposition,
		generation: $generation,
		candidate_control_failure: $candidate_control_failure,
		run_id: $run_id,
		receipt_schema: $receipt_schema,
		receipt: $receipt,
		recorded_at: $now,
		store_schema_version: $store_schema_version,
		evidence_migration_version: $evidence_migration_version
	} RETURN AFTER)
	ELSE [] END;
LET $pending = IF array::len($recorded) != 1
		OR $candidate_control_failure = false THEN NONE
	ELSE
		(SELECT id, created_at FROM candidate_manifest_job
			WHERE pending_key = $repo AND status = 'pending'
			ORDER BY created_at LIMIT 1)[0].id
	END;
LET $repair = IF array::len($recorded) != 1
		OR $candidate_control_failure = false THEN []
	ELSE IF $pending != NONE THEN
		(UPDATE $pending SET force = true,
			recovery_lease = NONE RETURN AFTER)
	ELSE
		(CREATE candidate_manifest_job CONTENT {
			target: $repo,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $repo,
			force: true
		} RETURN AFTER)
	END;
LET $catalog = (SELECT declarations FROM $catalog_rid)[0];
LET $catalog_depends = $catalog != NONE
	AND $domain IN $catalog.declarations.map(
		|$declaration| $declaration.domain);
LET $catalog_trigger = $catalog_depends;
LET $retired_catalog = IF array::len($recorded) = 1
		AND $catalog_trigger AND $catalog != NONE THEN
	(DELETE $catalog_rid RETURN BEFORE)
	ELSE [] END;
LET $pending_catalog = IF array::len($recorded) = 1
		AND $catalog_trigger THEN
	(SELECT id, created_at FROM resolver_catalog_job
		WHERE pending_key = $repo AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $catalog_fanout = IF array::len($recorded) != 1
		OR $catalog_trigger = false THEN []
	ELSE IF $pending_catalog != NONE THEN
		(UPDATE $pending_catalog SET force = true,
			recovery_lease = NONE RETURN AFTER)
	ELSE
		(CREATE resolver_catalog_job CONTENT {
			target: $repo,
			status: 'pending',
			attempts: 0,
			created_at: time::now(),
			pending_key: $repo,
			force: true
		} RETURN AFTER)
	END;
RETURN IF array::len($recorded) = 1
	AND ($candidate_control_failure = false OR array::len($repair) = 1)
	AND ($catalog_trigger = false OR array::len($catalog_fanout) = 1)
	THEN $recorded ELSE [] END;
COMMIT;`

// RecordExtractionDomainOutcome records only non-published dispositions. The
// previous published run is deliberately untouched; T30.5's existing
// commit/unit/candidate fences remain its sole visibility authority.
func (s *Surreal) RecordExtractionDomainOutcome(
	ctx context.Context,
	outcome ExtractionDomainOutcome,
) error {
	prepared, err := prepareExtractionDomainOutcome(outcome)
	if err != nil {
		return fmt.Errorf("record extraction domain outcome: %w", err)
	}
	if prepared.Disposition == DomainOutcomePublished {
		return errors.New(
			"record extraction domain outcome cannot publish evidence")
	}
	vars := map[string]any{
		"repo_rid":      repoID(prepared.Scope.Repository),
		"candidate_rid": candidateManifestPublicationID(prepared.Scope.Repository),
		"catalog_rid":   resolverCatalogPublicationID(prepared.Scope.Repository),
		"attempt_rid":   extractionAttemptID(prepared.Scope),
		"outcome_rid": extractionDomainOutcomeID(
			prepared.Scope.Repository, prepared.Scope.Domain,
		),
		"repo":                       prepared.Scope.Repository,
		"commit":                     prepared.Scope.Commit,
		"unit_digest":                prepared.Scope.UnitDigest,
		"domain":                     prepared.Scope.Domain,
		"disposition":                string(prepared.Disposition),
		"generation":                 prepared.Generation,
		"candidate_manifest_digest":  prepared.Generation.CandidateManifestDigest,
		"candidate_policy_digest":    prepared.Generation.CandidatePolicyDigest,
		"candidate_control_revision": prepared.Generation.CandidateControlRevision,
		"candidate_control_failure":  prepared.CandidateControlFailure,
		"run_id":                     prepared.RunID,
		"receipt_schema":             prepared.ReceiptSchema,
		"receipt":                    prepared.Receipt,
		"now":                        time.Now().UTC(),
		"migration_rid":              evidenceMigrationStateID(),
		"store_schema_version":       evidenceStoreSchemaVersion,
		"evidence_format_version":    evidenceFormatVersion,
		"evidence_migration_version": evidenceMigrationVersion,
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]extractionDomainOutcomeRec](
			ctx, s.db, recordExtractionDomainOutcomeSQL, vars,
		)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil &&
				attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("record extraction domain outcome: %w", queryErr)
		}
		if len(firstOutcomeRows(results)) == 1 {
			return nil
		}
		return fmt.Errorf(
			"record extraction domain outcome: writer, repository, candidate, attempt, or prior outcome changed: %w",
			ErrConflict,
		)
	}
}

func firstOutcomeRows(
	results *[]surrealdb.QueryResult[[]extractionDomainOutcomeRec],
) []extractionDomainOutcomeRec {
	if results == nil {
		return nil
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

// LatestExtractionDomainOutcome reads a current-schema row only when the exact
// scope agrees. A stale latest-only row therefore behaves as absent.
func (s *Surreal) LatestExtractionDomainOutcome(
	ctx context.Context,
	scope ExtractionScope,
) (*ExtractionDomainOutcome, error) {
	if err := validateExtractionScope(scope); err != nil {
		return nil, fmt.Errorf("latest extraction domain outcome: %w", err)
	}
	results, err := surrealdb.Query[[]extractionDomainOutcomeRec](
		ctx, s.db,
		`SELECT * FROM $rid WHERE repo = $repo AND commit = $commit
			AND unit_digest = $unit_digest AND domain = $domain
			AND store_schema_version = $store_schema_version
			AND evidence_migration_version = $evidence_migration_version
			LIMIT 1`,
		map[string]any{
			"rid":  extractionDomainOutcomeID(scope.Repository, scope.Domain),
			"repo": scope.Repository, "commit": scope.Commit,
			"unit_digest": scope.UnitDigest, "domain": scope.Domain,
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("latest extraction domain outcome: %w", err)
	}
	rows := firstOutcomeRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf(
			"extraction domain outcome for %q/%q: %w",
			scope.Repository, scope.Domain, ErrNotFound,
		)
	}
	outcome := rows[0].outcome()
	if err := outcome.Validate(); err != nil {
		return nil, fmt.Errorf(
			"latest extraction domain outcome: invalid persisted row: %w", err)
	}
	if outcome.Generation.Digest !=
		ComputeExtractionGenerationDigest(outcome.Generation) {
		return nil, errors.New(
			"latest extraction domain outcome: generation digest mismatch")
	}
	outcome.RecordedAt = outcome.RecordedAt.UTC()
	return &outcome, nil
}

// CandidateControlRepairNeeded reports whether any current domain is settled
// on a terminal control failure for this exact durable pointer revision.
func (s *Surreal) CandidateControlRepairNeeded(
	ctx context.Context,
	publication CandidateManifestPublication,
) (bool, error) {
	if err := validateCandidateManifestPublication(publication, true); err != nil {
		return false, fmt.Errorf("candidate control repair: %w", err)
	}
	results, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db,
		`SELECT count() AS count FROM extraction_domain_outcome
			WHERE repo = $repo
				AND commit = $commit
				AND unit_digest = $unit_digest
				AND candidate_control_failure = true
				AND disposition = $terminal
				AND generation.candidate_manifest_digest = $manifest_digest
				AND generation.candidate_policy_digest = $policy_digest
				AND generation.candidate_control_revision = $control_revision
				AND store_schema_version = $store_schema_version
				AND evidence_migration_version = $evidence_migration_version
			GROUP ALL`,
		map[string]any{
			"repo":                       publication.Repository,
			"commit":                     publication.HeadCommit,
			"unit_digest":                publication.UnitDigest,
			"manifest_digest":            publication.ManifestDigest,
			"policy_digest":              publication.PolicyDigest,
			"control_revision":           publication.ControlRevision,
			"terminal":                   string(DomainOutcomeTerminalGenerationRefusal),
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		})
	if err != nil {
		return false, fmt.Errorf("candidate control repair: %w", err)
	}
	for _, result := range *results {
		for _, row := range result.Result {
			return row.Count > 0, nil
		}
	}
	return false, nil
}

func (s *Surreal) AbortExtractionRun(ctx context.Context, runID string) error {
	run, err := s.getRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("abort extraction run %s: %w", runID, err)
	}
	for attempt := 0; ; attempt++ {
		vars := map[string]any{
			"rid":                        extractionRunID(runID),
			"attempt_rid":                extractionAttemptID(scopeForRun(run)),
			"run_id":                     runID,
			"repo":                       run.Repo,
			"commit":                     run.Commit,
			"unit_digest":                run.UnitDigest,
			"domain":                     run.Domain,
			"migration_rid":              evidenceMigrationStateID(),
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_format_version":    evidenceFormatVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		}
		addProbeVars(vars, runID)
		results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
			`BEGIN;
LET $aborted = UPDATE $rid SET status = 'aborted', published_key = NONE
				WHERE status = 'staged'
				  AND array::len(SELECT id FROM $migration_rid
					  WHERE version = $evidence_migration_version LIMIT 1) = 1
				  AND store_schema_version = $store_schema_version
					  AND evidence_format_version = $evidence_format_version
					  AND type::is_string(unit_digest)
					  AND run_id = record::id(id)
					  AND `+evidenceRunProbeHasNoClaimantSQL+`
					  AND published_key = NONE
				  AND retention_quarantined = false RETURN AFTER;
LET $attempt = IF array::len($aborted) = 1 THEN
	(UPDATE $attempt_rid SET status = 'aborted'
		WHERE run_id = $run_id AND status = 'staged'
		  AND repo = $repo AND commit = $commit
		  AND unit_digest = $unit_digest AND domain = $domain
		  AND store_schema_version = $store_schema_version
		  AND evidence_format_version = $evidence_format_version
		  AND evidence_migration_version = $evidence_migration_version RETURN AFTER)
	ELSE [] END;
RETURN $aborted;
COMMIT;`, vars)
		if err != nil {
			if isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("abort extraction run %s: %w", runID, err)
		}
		if len(firstExtractionRows(results)) == 1 {
			return nil
		}
		current, stateErr := s.getRun(ctx, runID)
		if errors.Is(stateErr, ErrNotFound) {
			return fmt.Errorf("abort extraction run %s: %w", runID, ErrNotFound)
		}
		if stateErr != nil {
			return fmt.Errorf("abort extraction run %s: check state: %w", runID, stateErr)
		}
		return fmt.Errorf("abort extraction run %s: run is %s, not staged: %w", runID, current.Status, ErrConflict)
	}
}

// LatestExtractionAttempt returns the durable latest attempt for one exact
// extraction scope. Compatible stores created before attempt markers were
// introduced fall back only to the publication for that same exact scope.
func (s *Surreal) LatestExtractionAttempt(
	ctx context.Context, scope ExtractionScope,
) (*ExtractionAttempt, error) {
	if err := validateExtractionScope(scope); err != nil {
		return nil, fmt.Errorf("latest extraction attempt: %w", err)
	}
	results, err := surrealdb.Query[[]extractionAttemptRec](ctx, s.db,
		`SELECT * FROM $rid WHERE repo = $repo AND commit = $commit
			AND unit_digest = $unit_digest AND domain = $domain
			AND store_schema_version = $store_schema_version
			AND evidence_format_version = $evidence_format_version
			AND evidence_migration_version = $evidence_migration_version LIMIT 1`,
		map[string]any{
			"rid":  extractionAttemptID(scope),
			"repo": scope.Repository, "commit": scope.Commit,
			"unit_digest": scope.UnitDigest, "domain": scope.Domain,
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_format_version":    evidenceFormatVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		})
	if err != nil {
		return nil, fmt.Errorf("latest extraction attempt: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) == 0 {
			continue
		}
		attempt := result.Result[0].attempt()
		return &attempt, nil
	}
	run, err := s.LatestPublishedRun(ctx, scope)
	if err != nil {
		return nil, err
	}
	return &ExtractionAttempt{
		RunID: run.ID, Repo: run.Repo, Commit: run.Commit,
		UnitDigest: run.UnitDigest, Domain: run.Domain,
		Extractor: run.Extractor, Status: "published", StartedAt: run.StartedAt,
	}, nil
}

func (s *Surreal) LatestPublishedRun(
	ctx context.Context, scope ExtractionScope,
) (*ExtractionRun, error) {
	if err := validateExtractionScope(scope); err != nil {
		return nil, fmt.Errorf("latest published run: %w", err)
	}
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
		`SELECT * FROM extraction_run WHERE repo = $repo AND commit = $commit
			AND unit_digest = $unit_digest AND domain = $domain
			AND status = 'published'
				AND type::is_string(store_schema_version)
				AND store_schema_version = $store_schema_version
				AND evidence_format_version = $evidence_format_version
				AND retention_quarantined = false
				AND run_id = record::id(id)
				AND `+evidenceRunHasNoAmbiguousClaimantSQL+`
				AND published_key = $published_key
			ORDER BY published_at DESC, run_id LIMIT 1`,
		map[string]any{
			"repo": scope.Repository, "commit": scope.Commit,
			"unit_digest": scope.UnitDigest, "domain": scope.Domain,
			"store_schema_version":        evidenceStoreSchemaVersion,
			"evidence_format_version":     evidenceFormatVersion,
			"published_key":               publishedKey(scope),
			"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
		})
	if err != nil {
		return nil, fmt.Errorf("latest published run: %w", err)
	}
	rows := firstExtractionRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	run := rows[0].run()
	if err := validatePublishedCoverage(run.Coverage, &run); err != nil {
		return nil, fmt.Errorf(
			"latest published run: invalid stored coverage: %w",
			err,
		)
	}
	if err := s.validatePublishedCandidatePointer(ctx, &run); err != nil {
		return nil, fmt.Errorf(
			"latest published run: invalid stored coverage: %w",
			err,
		)
	}
	return &run, nil
}

type assertionRec struct {
	RecID         *models.RecordID `json:"id"`
	AssertionID   string           `json:"assertion_id"`
	Predicate     string           `json:"predicate"`
	Subject       string           `json:"subject"`
	Object        string           `json:"object"`
	Lineage       string           `json:"lineage"`
	Tier          string           `json:"tier"`
	CodeRole      string           `json:"code_role"`
	Repo          string           `json:"repo"`
	Supporting    []string         `json:"supporting"`
	Contradicting []string         `json:"contradicting"`
	RunID         string           `json:"run_id"`
	Detail        string           `json:"detail"`
}

func (row assertionRec) assertion() Assertion {
	id := row.AssertionID
	if id == "" && row.RecID != nil {
		id, _ = row.RecID.ID.(string)
	}
	return Assertion{
		ID: id, Predicate: row.Predicate, Subject: row.Subject,
		Object: row.Object, Lineage: row.Lineage, Tier: row.Tier, CodeRole: row.CodeRole,
		Repo: row.Repo, Supporting: row.Supporting,
		Contradicting: row.Contradicting, RunID: row.RunID, Detail: row.Detail,
	}
}

// ListAssertions performs publication filtering in the same database
// statement as assertion selection. A two-query Go-side join could return a
// run that was superseded between queries. Stable ordering precedes LIMIT.
func (s *Surreal) ListAssertions(ctx context.Context, q AssertionQuery) ([]Assertion, error) {
	if strings.TrimSpace(q.Repo) == "" {
		return nil, errors.New("list assertions: repo scope required")
	}
	if q.Object != "" && q.ObjectPrefix != "" {
		return nil, errors.New("list assertions: object and object prefix are mutually exclusive")
	}
	if !utf8.ValidString(q.Repo) || len(q.Repo) > maxEvidenceIdentityBytes {
		return nil, errors.New("list assertions: invalid repo scope")
	}
	if q.RunID != "" && (!utf8.ValidString(q.RunID) || len(q.RunID) > maxEvidenceIdentityBytes) {
		return nil, errors.New("list assertions: invalid run scope")
	}
	if q.Scope != nil {
		if err := validateExtractionScope(*q.Scope); err != nil {
			return nil, fmt.Errorf("list assertions: %w", err)
		}
		if q.Scope.Repository != q.Repo {
			return nil, errors.New("list assertions: extraction scope repository does not match repo")
		}
	} else if q.RunID == "" {
		return nil, errors.New("list assertions: run id or exact extraction scope required")
	}
	if q.ObjectPrefix != "" &&
		(!utf8.ValidString(q.ObjectPrefix) || len(q.ObjectPrefix) > maxEvidenceIdentityBytes) {
		return nil, errors.New("list assertions: invalid object prefix")
	}
	if q.After != nil {
		if q.After.ID == "" || q.After.RunID == "" {
			return nil, errors.New("list assertions: continuation requires assertion and run ids")
		}
		for name, value := range map[string]string{
			"predicate": q.After.Predicate,
			"subject":   q.After.Subject,
			"object":    q.After.Object,
			"id":        q.After.ID,
			"run_id":    q.After.RunID,
		} {
			if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes {
				return nil, fmt.Errorf("list assertions: invalid continuation %s", name)
			}
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		return nil, fmt.Errorf("list assertions: maximum limit is 5000: %w", ErrResultLimit)
	}
	vars := map[string]any{
		"limit":                       limit + 1,
		"store_schema_version":        evidenceStoreSchemaVersion,
		"evidence_format_version":     evidenceFormatVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	var where string
	if q.RunID != "" {
		where = `run_id = $run_id AND run_id IN (SELECT VALUE run_id FROM extraction_run
			WHERE id = $run_rid
			  AND run_id = $run_id
			  AND status = 'published'
			  AND repo = $repo
			  AND evidence_format_version = $evidence_format_version
			  AND type::is_string(unit_digest)
			  AND retention_quarantined = false
			  AND run_id = record::id(id)
			  AND ` + evidenceRunProbeHasNoClaimantSQL + `
			  AND published_key != NONE)`
		vars["run_id"] = q.RunID
		vars["run_rid"] = extractionRunID(q.RunID)
		addProbeVars(vars, q.RunID)
	} else {
		where = `run_id IN (SELECT VALUE run_id FROM extraction_run
			WHERE status = 'published'
			  AND repo = $repo
			  AND commit = $scope_commit
			  AND unit_digest = $scope_unit_digest
			  AND domain = $scope_domain
			  AND store_schema_version = $store_schema_version
			  AND evidence_format_version = $evidence_format_version
			  AND retention_quarantined = false
			  AND run_id = record::id(id)
			  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
			  AND published_key = $published_key)`
	}
	if q.Scope != nil {
		vars["scope_commit"] = q.Scope.Commit
		vars["scope_unit_digest"] = q.Scope.UnitDigest
		vars["scope_domain"] = q.Scope.Domain
		vars["published_key"] = publishedKey(*q.Scope)
		if q.RunID != "" {
			where += ` AND run_id IN (SELECT VALUE run_id FROM extraction_run
				WHERE repo = $repo
				  AND commit = $scope_commit
				  AND unit_digest = $scope_unit_digest
				  AND domain = $scope_domain
				  AND status = 'published'
				  AND store_schema_version = $store_schema_version
				  AND evidence_format_version = $evidence_format_version
				  AND retention_quarantined = false
				  AND published_key = $published_key)`
		}
	}
	if q.Predicate != "" {
		where += " AND predicate = $predicate"
		vars["predicate"] = q.Predicate
	}
	if q.Subject != "" {
		where += " AND subject = $subject"
		vars["subject"] = q.Subject
	}
	if q.Object != "" {
		where += " AND object = $object"
		vars["object"] = q.Object
	}
	if q.ObjectPrefix != "" {
		where += " AND string::starts_with(object, $object_prefix)"
		vars["object_prefix"] = q.ObjectPrefix
	}
	if q.Lineage != "" {
		where += " AND lineage = $lineage"
		vars["lineage"] = q.Lineage
	}
	if q.Repo != "" {
		where += " AND repo = $repo"
		vars["repo"] = q.Repo
	}
	if q.After != nil {
		where += ` AND (
			predicate > $after_predicate OR
			(predicate = $after_predicate AND subject > $after_subject) OR
			(predicate = $after_predicate AND subject = $after_subject AND object > $after_object) OR
			(predicate = $after_predicate AND subject = $after_subject AND object = $after_object
				AND assertion_id > $after_id) OR
			(predicate = $after_predicate AND subject = $after_subject AND object = $after_object
				AND assertion_id = $after_id AND run_id > $after_run_id)
		)`
		vars["after_predicate"] = q.After.Predicate
		vars["after_subject"] = q.After.Subject
		vars["after_object"] = q.After.Object
		vars["after_id"] = q.After.ID
		vars["after_run_id"] = q.After.RunID
	}
	results, err := surrealdb.Query[[]assertionRec](ctx, s.db,
		"SELECT * FROM assertion WHERE "+where+
			" ORDER BY predicate, subject, object, assertion_id, run_id LIMIT $limit", vars)
	if err != nil {
		return nil, fmt.Errorf("list assertions: %w", err)
	}
	var out []Assertion
	for _, result := range *results {
		for _, row := range result.Result {
			out = append(out, row.assertion())
		}
	}
	if len(out) > limit {
		if q.AllowTruncate {
			return out, nil
		}
		return nil, fmt.Errorf("list assertions: more than %d rows; use a narrower query: %w", limit, ErrResultLimit)
	}
	return out, nil
}

func validateReverseAssertionValue(name, value string, required bool) error {
	if required && value == "" {
		return fmt.Errorf("list reverse assertions: %s is required", name)
	}
	if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes {
		return fmt.Errorf("list reverse assertions: %s is not valid bounded UTF-8", name)
	}
	return nil
}

func normalizeReverseAssertionQuery(q ReverseAssertionQuery) (ReverseAssertionQuery, int, error) {
	for _, field := range []struct {
		name     string
		value    string
		required bool
	}{
		{name: "repo", value: q.Repo, required: true},
		{name: "run id", value: q.RunID, required: true},
		{name: "predicate", value: q.Predicate, required: true},
		{name: "object", value: q.Object, required: true},
		{name: "lineage", value: q.Lineage},
	} {
		if err := validateReverseAssertionValue(field.name, field.value, field.required); err != nil {
			return ReverseAssertionQuery{}, 0, err
		}
	}
	limit := q.Limit
	if limit == 0 {
		limit = defaultReverseAssertionPage
	}
	if limit < 1 || limit > maxReverseAssertionPage {
		return ReverseAssertionQuery{}, 0, fmt.Errorf(
			"list reverse assertions: limit must be between 1 and %d: %w",
			maxReverseAssertionPage, ErrResultLimit,
		)
	}
	if q.After == nil {
		return q, limit, nil
	}
	for _, field := range []struct {
		name     string
		value    string
		required bool
	}{
		{name: "cursor repo", value: q.After.Repo, required: true},
		{name: "cursor run id", value: q.After.RunID, required: true},
		{name: "cursor predicate", value: q.After.Predicate, required: true},
		{name: "cursor object", value: q.After.Object, required: true},
		{name: "cursor query lineage", value: q.After.QueryLineage},
		{name: "cursor row lineage", value: q.After.RowLineage},
		{name: "cursor subject", value: q.After.Subject, required: true},
		{name: "cursor assertion id", value: q.After.AssertionID, required: true},
	} {
		if err := validateReverseAssertionValue(field.name, field.value, field.required); err != nil {
			return ReverseAssertionQuery{}, 0, err
		}
	}
	if q.After.Repo != q.Repo || q.After.RunID != q.RunID ||
		q.After.Predicate != q.Predicate || q.After.Object != q.Object ||
		q.After.QueryLineage != q.Lineage {
		return ReverseAssertionQuery{}, 0, errors.New(
			"list reverse assertions: continuation does not match query scope",
		)
	}
	if q.Lineage != "" && q.After.RowLineage != q.Lineage {
		return ReverseAssertionQuery{}, 0, errors.New(
			"list reverse assertions: continuation row does not match lineage scope",
		)
	}
	return q, limit, nil
}

func reverseAssertionQuerySQL(hasLineage, hasAfter, explain bool) string {
	query := `SELECT * FROM assertion WITH INDEX ` + reverseAssertionIndexName + `
		WHERE run_id = $run_id
		  AND predicate = $predicate
		  AND object = $object
		  AND repo = $repo
		  AND run_id IN (SELECT VALUE run_id FROM $run_rid
			WHERE run_id = $run_id
			  AND status = 'published'
			  AND repo = $repo
			  AND evidence_format_version = $evidence_format_version
			  AND type::is_string(unit_digest)
			  AND retention_quarantined = false
			  AND run_id = record::id(id)
			  AND ` + evidenceRunProbeHasNoClaimantSQL + `
			  AND published_key != NONE LIMIT 1)`
	if hasLineage {
		query += `
		  AND lineage = $lineage`
	}
	if hasAfter {
		query += `
		  AND (
			lineage > $after_lineage OR
			(lineage = $after_lineage AND subject > $after_subject) OR
			(lineage = $after_lineage AND subject = $after_subject
				AND assertion_id > $after_assertion_id)
		  )`
	}
	query += `
		ORDER BY lineage, subject, assertion_id
		LIMIT $limit`
	if explain {
		query += " EXPLAIN FULL"
	}
	return query
}

func reverseAssertionQueryVars(q ReverseAssertionQuery, limit int) map[string]any {
	vars := map[string]any{
		"repo": q.Repo, "run_id": q.RunID, "run_rid": extractionRunID(q.RunID),
		"predicate": q.Predicate, "object": q.Object, "lineage": q.Lineage,
		"limit": limit + 1, "evidence_format_version": evidenceFormatVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	addProbeVars(vars, q.RunID)
	if q.After != nil {
		vars["after_lineage"] = q.After.RowLineage
		vars["after_subject"] = q.After.Subject
		vars["after_assertion_id"] = q.After.AssertionID
	}
	return vars
}

func (s *Surreal) requireReverseAssertionIndex(ctx context.Context) error {
	results, err := surrealdb.Query[any](
		ctx,
		s.db,
		"INFO FOR INDEX "+reverseAssertionIndexName+" ON TABLE assertion",
		nil,
	)
	if err != nil {
		return fmt.Errorf("inspect required reverse assertion index: %w", err)
	}
	if results == nil || len(*results) != 1 {
		return errors.New("inspect required reverse assertion index: incomplete catalog response")
	}
	for statement, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"inspect required reverse assertion index: statement %d: %s",
				statement,
				result.Error.Message,
			)
		}
	}
	return nil
}

// ListReverseAssertions is the strict source-row paging primitive used by the
// Caller Map service. Publication authorization and assertion selection share
// one statement. The catalog preflight makes a missing generation fail before
// data access; WITH INDEX restricts the subsequent planner to that generation,
// whose selected plan is pinned by the target-scale acceptance gate.
func (s *Surreal) ListReverseAssertions(
	ctx context.Context, input ReverseAssertionQuery,
) (*ReverseAssertionPage, error) {
	q, limit, err := normalizeReverseAssertionQuery(input)
	if err != nil {
		return nil, err
	}
	if err := s.requireReverseAssertionIndex(ctx); err != nil {
		return nil, fmt.Errorf("list reverse assertions: %w", err)
	}
	results, err := surrealdb.Query[[]assertionRec](
		ctx,
		s.db,
		reverseAssertionQuerySQL(q.Lineage != "", q.After != nil, false),
		reverseAssertionQueryVars(q, limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list reverse assertions: %w", err)
	}
	rows := make([]Assertion, 0, limit+1)
	for statement, result := range *results {
		if result.Error != nil {
			return nil, fmt.Errorf(
				"list reverse assertions: statement %d: %s",
				statement,
				result.Error.Message,
			)
		}
		for _, row := range result.Result {
			rows = append(rows, row.assertion())
		}
	}
	page := &ReverseAssertionPage{Assertions: rows}
	if len(rows) <= limit {
		return page, nil
	}
	page.Assertions = rows[:limit]
	last := page.Assertions[len(page.Assertions)-1]
	page.Next = &ReverseAssertionCursor{
		Repo: q.Repo, RunID: q.RunID, Predicate: q.Predicate, Object: q.Object,
		QueryLineage: q.Lineage, RowLineage: last.Lineage,
		Subject: last.Subject, AssertionID: last.ID,
	}
	return page, nil
}

type evidenceResolutionRec struct {
	OccurrenceID string       `json:"occurrence_id"`
	AtomID       string       `json:"atom_id"`
	Atom         EvidenceAtom `json:"atom_record"`
	Repo         string       `json:"repo"`
	Commit       string       `json:"commit"`
	Path         string       `json:"path"`
	StartLine    int          `json:"start_line"`
	EndLine      int          `json:"end_line"`
	Visibility   string       `json:"visibility_scope"`
	RunID        string       `json:"run_id"`
	ObservedAt   time.Time    `json:"observed_at"`
}

// ResolveEvidence returns every occurrence of one atom within exactly one
// authorized repository/run. The extra row detects truncation instead of
// silently returning incomplete provenance.
func (s *Surreal) ResolveEvidence(ctx context.Context, repo, runID, atomID string) (*EvidenceResolution, error) {
	if strings.TrimSpace(repo) == "" || strings.TrimSpace(runID) == "" || strings.TrimSpace(atomID) == "" {
		return nil, errors.New("resolve evidence: repo, run id, and atom id required")
	}
	if len(repo) > maxEvidenceIdentityBytes || len(runID) > maxEvidenceIdentityBytes ||
		len(atomID) > maxEvidenceIdentityBytes || !utf8.ValidString(repo) ||
		!utf8.ValidString(runID) || !utf8.ValidString(atomID) {
		return nil, errors.New("resolve evidence: invalid bounded identifier")
	}
	vars := map[string]any{
		"repo": repo, "run": runID, "run_rid": extractionRunID(runID), "atom": atomID,
		"limit":                   maxEvidenceOccurrences + 1,
		"evidence_format_version": evidenceFormatVersion,
	}
	addProbeVars(vars, runID)
	results, err := surrealdb.Query[[]evidenceResolutionRec](ctx, s.db,
		`SELECT * FROM snapshot_evidence
            WHERE repo = $repo AND run_id = $run AND atom_id = $atom
	              AND commit IN (SELECT VALUE commit FROM extraction_run
					  WHERE id = $run_rid AND run_id = $run AND repo = $repo
						AND run_id = record::id(id)
						AND `+evidenceRunProbeHasNoClaimantSQL+`)
				  AND run_id IN (SELECT VALUE run_id FROM extraction_run
					  WHERE id = $run_rid AND run_id = $run AND repo = $repo
						AND run_id = record::id(id)
						AND `+evidenceRunProbeHasNoClaimantSQL+`
						AND evidence_format_version = $evidence_format_version
					AND retention_quarantined = false
					AND ((status = 'published' AND published_key != NONE) OR
						(status = 'superseded' AND published_key = NONE AND run_id IN
							(SELECT VALUE run_id FROM evidence_pin WHERE run_id = $run))))
            ORDER BY occurrence_id LIMIT $limit FETCH atom_record`, vars)
	if err != nil {
		return nil, fmt.Errorf("resolve evidence: %w", err)
	}
	var rows []evidenceResolutionRec
	for _, result := range *results {
		rows = append(rows, result.Result...)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) > maxEvidenceOccurrences {
		return nil, fmt.Errorf("resolve evidence: atom has more than %d occurrences", maxEvidenceOccurrences)
	}
	resolved := &EvidenceResolution{Atom: rows[0].Atom, Occurrences: make([]SnapshotEvidence, 0, len(rows))}
	if resolved.Atom.ID != atomID {
		return nil, errors.New("resolve evidence: stored atom linkage is inconsistent")
	}
	for _, row := range rows {
		if row.Atom.ID != atomID || row.Repo != repo || row.RunID != runID || row.Visibility != "repo:"+repo {
			return nil, errors.New("resolve evidence: stored occurrence linkage is inconsistent")
		}
		resolved.Occurrences = append(resolved.Occurrences, SnapshotEvidence{
			ID: row.OccurrenceID, AtomID: row.AtomID, Repo: row.Repo, Commit: row.Commit,
			Path: row.Path, StartLine: row.StartLine, EndLine: row.EndLine,
			VisibilityScope: row.Visibility, RunID: row.RunID, ObservedAt: row.ObservedAt,
		})
	}
	return resolved, nil
}

type evidencePinRec struct {
	RunID string `json:"run_id"`
	Kind  string `json:"kind"`
}

const pinRunSQL = `
BEGIN;
LET $locked = UPDATE $rid SET retention_revision = (retention_revision ?? 0) + 1
	WHERE ((status = 'published' AND published_key != NONE)
		OR (status = 'superseded' AND published_key = NONE))
		  AND evidence_format_version = $evidence_format_version
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
	      AND retention_quarantined = false RETURN AFTER;
RETURN IF array::len($locked) = 1 THEN
    (UPSERT $pin SET pin_key = $pin_key, run_id = $run_id, kind = $kind,
        created_at = IF created_at = NONE THEN $now ELSE created_at END RETURN AFTER)
    ELSE [] END;
COMMIT;`

func (s *Surreal) PinRun(ctx context.Context, runID, kind string) error {
	if strings.TrimSpace(runID) == "" || len(runID) > maxEvidenceIdentityBytes || !utf8.ValidString(runID) {
		return errors.New("pin run: run id must be bounded UTF-8")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" || len(kind) > maxEvidenceIdentityBytes || !utf8.ValidString(kind) {
		return errors.New("pin run: kind must be non-empty bounded UTF-8")
	}
	pinKey := hashIdentity("pin_", runID, kind)
	vars := map[string]any{
		"rid": extractionRunID(runID), "pin": evidencePinRecordID(runID, kind),
		"pin_key": pinKey, "run_id": runID, "kind": kind, "now": time.Now().UTC(),
		"evidence_format_version": evidenceFormatVersion,
	}
	addProbeVars(vars, runID)
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]evidencePinRec](ctx, s.db, pinRunSQL, vars)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("pin run %s: %w", runID, err)
		}
		found := false
		for _, result := range *results {
			found = found || len(result.Result) == 1
		}
		if found {
			return nil
		}
		current, stateErr := s.getRun(ctx, runID)
		if errors.Is(stateErr, ErrNotFound) {
			exists, existsErr := s.extractionRunExists(ctx, runID)
			if existsErr != nil {
				return fmt.Errorf("pin run %s: check existence: %w", runID, existsErr)
			}
			if exists {
				return fmt.Errorf("pin run %s: run is incompatible or quarantined: %w", runID, ErrConflict)
			}
			return fmt.Errorf("pin run %s: %w", runID, ErrNotFound)
		}
		if stateErr != nil {
			return fmt.Errorf("pin run %s: check state: %w", runID, stateErr)
		}
		return fmt.Errorf("pin run %s: run is not a current, non-quarantined published/superseded run (status %s): %w", runID, current.Status, ErrConflict)
	}
}

const markRunDeletingSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
LET $locked = IF $writer_ok THEN
	(UPDATE $rid SET retention_revision = (retention_revision ?? 0) + 1,
		status = 'deleting', retention_phase = 'associations'
		WHERE (status IN ['aborted', 'superseded']
				OR (status = 'staged' AND started_at <= $cutoff))
		  AND evidence_format_version = $evidence_format_version
		  AND retention_quarantined = false
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
		  AND published_key = NONE
		  AND array::len(SELECT id FROM evidence_pin WHERE run_id = $run LIMIT 1) = 0
		RETURN AFTER)
	ELSE [] END;
IF array::len($locked) = 1 AND $prior_status = 'staged' {
	UPDATE $attempt_rid SET status = 'aborted'
		WHERE run_id = $run AND status = 'staged'
		  AND repo = $repo AND commit = $commit
		  AND unit_digest = $unit_digest AND domain = $domain
		  AND store_schema_version = $store_schema_version
		  AND evidence_format_version = $evidence_format_version
		  AND evidence_migration_version = $evidence_migration_version RETURN NONE
};
RETURN [{
	runs_marked_deleting: array::len($locked),
	runs_deleted: 0,
	association_rows_deleted: 0,
	assertion_rows_deleted: 0,
	atom_rows_deleted: 0,
	retention_phases_advanced: 0
}];
COMMIT;`

const sweepAssociationChunkSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
LET $locked = IF $writer_ok THEN
	(UPDATE $rid SET retention_revision = (retention_revision ?? 0) + 1
		WHERE status = 'deleting' AND retention_phase = 'associations'
		  AND evidence_format_version = $evidence_format_version
		  AND retention_quarantined = false
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
		  AND published_key = NONE
		  AND array::len(SELECT id FROM evidence_pin WHERE run_id = $run LIMIT 1) = 0
		RETURN AFTER)
	ELSE [] END;
LET $atom_ids = array::distinct(SELECT VALUE atom_id FROM snapshot_evidence
	WHERE run_id = $run AND array::len($locked) = 1
	ORDER BY id LIMIT $row_limit);
LET $gone = SELECT VALUE id FROM snapshot_evidence
	WHERE run_id = $run AND array::len($locked) = 1
	ORDER BY id LIMIT $row_limit;
LET $atoms_before = SELECT id FROM evidence_atom WHERE atom_id IN $atom_ids;
FOR $row_id IN $gone {
	DELETE $row_id RETURN NONE
};
FOR $aid IN $atom_ids {
	IF array::len(SELECT id FROM snapshot_evidence WHERE atom_id = $aid LIMIT 1) = 0 {
		DELETE evidence_atom WHERE atom_id = $aid RETURN NONE
	}
};
LET $atoms_after = SELECT id FROM evidence_atom WHERE atom_id IN $atom_ids;
LET $advanced = IF array::len($locked) = 1 AND array::len($gone) < $row_limit THEN
	(UPDATE $rid SET retention_phase = 'assertions'
		WHERE status = 'deleting' AND retention_phase = 'associations' RETURN AFTER)
	ELSE [] END;
RETURN [{
	runs_marked_deleting: 0,
	runs_deleted: 0,
	association_rows_deleted: array::len($gone),
	assertion_rows_deleted: 0,
	atom_rows_deleted: array::len($atoms_before) - array::len($atoms_after),
	retention_phases_advanced: array::len($advanced)
}];
COMMIT;`

const sweepAssertionChunkSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
LET $locked = IF $writer_ok THEN
	(UPDATE $rid SET retention_revision = (retention_revision ?? 0) + 1
		WHERE status = 'deleting' AND retention_phase = 'assertions'
		  AND evidence_format_version = $evidence_format_version
		  AND retention_quarantined = false
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
		  AND published_key = NONE
		  AND array::len(SELECT id FROM evidence_pin WHERE run_id = $run LIMIT 1) = 0
		RETURN AFTER)
	ELSE [] END;
LET $gone = SELECT VALUE id FROM assertion
	WHERE run_id = $run AND array::len($locked) = 1
	ORDER BY id LIMIT $row_limit;
FOR $row_id IN $gone {
	DELETE $row_id RETURN NONE
};
LET $advanced = IF array::len($locked) = 1 AND array::len($gone) < $row_limit THEN
	(UPDATE $rid SET retention_phase = 'finalize'
		WHERE status = 'deleting' AND retention_phase = 'assertions' RETURN AFTER)
	ELSE [] END;
RETURN [{
	runs_marked_deleting: 0,
	runs_deleted: 0,
	association_rows_deleted: 0,
	assertion_rows_deleted: array::len($gone),
	atom_rows_deleted: 0,
	retention_phases_advanced: array::len($advanced)
}];
COMMIT;`

const finalizeDeletingRunSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
LET $locked = IF $writer_ok THEN
	(UPDATE $rid SET retention_revision = (retention_revision ?? 0) + 1
		WHERE status = 'deleting' AND retention_phase = 'finalize'
		  AND evidence_format_version = $evidence_format_version
		  AND retention_quarantined = false
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
		  AND published_key = NONE
		  AND array::len(SELECT id FROM evidence_pin WHERE run_id = $run LIMIT 1) = 0
		  AND array::len(SELECT id FROM snapshot_evidence WHERE run_id = $run LIMIT 1) = 0
		  AND array::len(SELECT id FROM assertion WHERE run_id = $run LIMIT 1) = 0
		RETURN AFTER)
	ELSE [] END;
LET $deleted = IF array::len($locked) = 1 THEN
	(DELETE $rid RETURN BEFORE)
	ELSE [] END;
RETURN [{
	runs_marked_deleting: 0,
	runs_deleted: array::len($deleted),
	association_rows_deleted: 0,
	assertion_rows_deleted: 0,
	atom_rows_deleted: 0,
	retention_phases_advanced: 0
}];
COMMIT;`

type evidenceSweepCandidateRec struct {
	RecID      *models.RecordID `json:"id"`
	RunID      string           `json:"run_id"`
	Repo       string           `json:"repo"`
	Commit     string           `json:"commit"`
	UnitDigest string           `json:"unit_digest"`
	Domain     string           `json:"domain"`
	Status     string           `json:"status"`
	Phase      string           `json:"retention_phase"`
}

func firstEvidenceSweepCandidate(
	results *[]surrealdb.QueryResult[[]evidenceSweepCandidateRec],
) *evidenceSweepCandidateRec {
	for _, result := range *results {
		for _, row := range result.Result {
			if row.RecID != nil && row.RunID != "" {
				return &row
			}
		}
	}
	return nil
}

func (s *Surreal) nextEvidenceSweepCandidate(
	ctx context.Context, cutoff time.Time,
) (*evidenceSweepCandidateRec, error) {
	baseVars := map[string]any{
		"cutoff": cutoff, "limit": evidenceSweepCandidateBatchSize,
		"evidence_format_version":     evidenceFormatVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	for _, candidateSQL := range []string{
		`SELECT id, run_id, repo, commit, unit_digest, domain,
			status, retention_phase, started_at OMIT started_at
			FROM extraction_run
			WHERE status = 'deleting'
			  AND evidence_format_version = $evidence_format_version
			  AND retention_quarantined = false
			  AND run_id = record::id(id)
			  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
			  AND published_key = NONE
			  AND run_id NOT IN (SELECT VALUE run_id FROM evidence_pin)
			ORDER BY started_at, run_id LIMIT $limit`,
		`SELECT id, run_id, repo, commit, unit_digest, domain,
			status, retention_phase, started_at OMIT started_at
			FROM extraction_run
			WHERE (status IN ['aborted', 'superseded']
					OR (status = 'staged' AND started_at <= $cutoff))
			  AND evidence_format_version = $evidence_format_version
			  AND retention_quarantined = false
			  AND run_id = record::id(id)
			  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
			  AND published_key = NONE
			  AND run_id NOT IN (SELECT VALUE run_id FROM evidence_pin)
			ORDER BY started_at, run_id LIMIT $limit`,
	} {
		results, err := surrealdb.Query[[]evidenceSweepCandidateRec](ctx, s.db, candidateSQL, baseVars)
		if err != nil {
			return nil, err
		}
		if row := firstEvidenceSweepCandidate(results); row != nil {
			return row, nil
		}
	}
	return nil, nil
}

func evidenceSweepStepSQL(candidate evidenceSweepCandidateRec) (string, error) {
	switch candidate.Status {
	case "aborted", "superseded", "staged":
		if candidate.Phase != "" {
			return "", errors.New("eligible run has an unexpected retention phase")
		}
		return markRunDeletingSQL, nil
	case "deleting":
		switch candidate.Phase {
		case "associations":
			return sweepAssociationChunkSQL, nil
		case "assertions":
			return sweepAssertionChunkSQL, nil
		case "finalize":
			return finalizeDeletingRunSQL, nil
		default:
			return "", errors.New("deleting run has an invalid retention phase")
		}
	default:
		return "", errors.New("candidate has an invalid retention status")
	}
}

// SweepEvidence implements one proof-aware retention step. Candidate
// discovery is only an optimization: eligibility and pin absence are rechecked
// while atomically entering the durable non-visible deleting state, and each
// later transaction deletes at most evidenceSweepRowBatchSize associations or
// assertions. A process may stop after any successful call and resume from the
// retained status/phase without replaying deleted evidence.
func (s *Surreal) SweepEvidence(
	ctx context.Context, now time.Time, staleStagedAfter time.Duration,
) (EvidenceSweepProgress, error) {
	if staleStagedAfter < 0 {
		return EvidenceSweepProgress{}, errors.New("sweep evidence: stale duration cannot be negative")
	}
	active, err := s.evidenceMigrationComplete(ctx)
	if err != nil {
		return EvidenceSweepProgress{}, fmt.Errorf("sweep evidence: read writer generation: %w", err)
	}
	if !active {
		return EvidenceSweepProgress{}, fmt.Errorf("sweep evidence: writer generation is not active: %w", ErrConflict)
	}
	cutoff := now.UTC().Add(-staleStagedAfter)
	for selectionAttempt := 0; selectionAttempt < maxQueueRetries; selectionAttempt++ {
		candidate, err := s.nextEvidenceSweepCandidate(ctx, cutoff)
		if err != nil {
			return EvidenceSweepProgress{}, fmt.Errorf("sweep evidence: candidates: %w", err)
		}
		if candidate == nil {
			return EvidenceSweepProgress{}, nil
		}
		stepSQL, err := evidenceSweepStepSQL(*candidate)
		if err != nil {
			return EvidenceSweepProgress{}, fmt.Errorf("sweep evidence: run %s: %w", candidate.RunID, err)
		}
		runID := candidate.RunID
		scope := ExtractionScope{
			Repository: candidate.Repo,
			Commit:     candidate.Commit,
			UnitDigest: candidate.UnitDigest,
			Domain:     candidate.Domain,
		}
		vars := map[string]any{
			"rid": *candidate.RecID, "run": runID,
			"attempt_rid":  extractionAttemptID(scope),
			"repo":         scope.Repository,
			"commit":       scope.Commit,
			"unit_digest":  scope.UnitDigest,
			"domain":       scope.Domain,
			"cutoff":       cutoff,
			"prior_status": candidate.Status, "row_limit": evidenceSweepRowBatchSize,
			"migration_rid":              evidenceMigrationStateID(),
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_format_version":    evidenceFormatVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		}
		addProbeVars(vars, runID)
		for attempt := 0; ; attempt++ {
			results, queryErr := surrealdb.Query[[]EvidenceSweepProgress](ctx, s.db, stepSQL, vars)
			if queryErr != nil {
				if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
					continue
				}
				return EvidenceSweepProgress{}, fmt.Errorf("sweep evidence: run %s: %w", runID, queryErr)
			}
			for _, result := range *results {
				for _, row := range result.Result {
					if row.DidWork() {
						return row, nil
					}
				}
			}
			active, markerErr := s.evidenceMigrationComplete(ctx)
			if markerErr != nil {
				return EvidenceSweepProgress{}, fmt.Errorf(
					"sweep evidence: recheck writer generation: %w", markerErr,
				)
			}
			if !active {
				return EvidenceSweepProgress{}, fmt.Errorf(
					"sweep evidence: writer generation changed: %w", ErrConflict,
				)
			}
			break
		}
	}
	// Another retention worker may have consumed each selected step between
	// discovery and lock acquisition. That is a drained result for this pass;
	// the successful worker owns the durable progress.
	return EvidenceSweepProgress{}, nil
}
