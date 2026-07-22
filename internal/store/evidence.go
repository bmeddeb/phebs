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
	evidenceStoreSchemaVersion               = "t12-store-v4"
	evidencePreviousStoreSchemaVersion       = "t12-store-v3"
	evidenceFormatVersion                    = "t12-evidence-v1"
	evidenceMigrationVersion                 = "t12-evidence-migration-v2"
	evidenceMigrationBatchSize               = 128
	evidenceSweepBatchSize                   = 1
	maxEvidenceRowsPerRun                    = 10_000
	maxEvidenceBatchRows                     = maxEvidenceRowsPerRun
	maxEvidenceRefsPerAssertion              = 4096
	maxEvidenceReferenceEdges                = 20_000
	maxEvidenceIdentityBytes                 = 64 << 10
	maxEvidencePathBytes                     = 4096
	maxEvidenceOccurrences                   = 100
	maxCoverageFileCount                     = 10_000_000
	maxCoverageReadBytes               int64 = 1 << 50
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

func publishedKey(repo, domain string) string {
	return hashIdentity("published_", repo, domain)
}

func extractionAttemptID(repo, domain string) models.RecordID {
	return models.NewRecordID("extraction_attempt", hashIdentity("attempt_", repo, domain))
}

type extractionAttemptRec struct {
	RunID     string    `json:"run_id"`
	Repo      string    `json:"repo"`
	Commit    string    `json:"commit"`
	Domain    string    `json:"domain"`
	Extractor string    `json:"extractor"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

func (r extractionAttemptRec) attempt() ExtractionAttempt {
	return ExtractionAttempt(r)
}

type extractionRunRec struct {
	RecID       *models.RecordID `json:"id"`
	RunID       string           `json:"run_id"`
	Repo        string           `json:"repo"`
	Commit      string           `json:"commit"`
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
		Extractor: r.Extractor, Status: r.Status,
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

func (s *Surreal) BeginExtractionRun(ctx context.Context, repo, commit, domain, extractor string) (*ExtractionRun, error) {
	if strings.TrimSpace(repo) == "" || strings.TrimSpace(commit) == "" ||
		strings.TrimSpace(domain) == "" || strings.TrimSpace(extractor) == "" {
		return nil, errors.New("begin extraction run: repo, commit, domain, extractor all required")
	}
	for name, value := range map[string]string{
		"repo": repo, "commit": commit, "domain": domain, "extractor": extractor,
	} {
		if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes {
			return nil, fmt.Errorf("begin extraction run: %s is not valid bounded UTF-8", name)
		}
	}
	id, err := authRandomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
		`BEGIN;
LET $created = CREATE $rid SET run_id = $run_id, repo = $repo, commit = $commit, domain = $domain,
            extractor = $extractor, status = 'staged', started_at = $now,
            store_schema_version = $store_schema_version,
			evidence_format_version = $evidence_format_version,
			evidence_migration_version = $evidence_migration_version,
            retention_quarantined = false, staged_revision = 0, retention_revision = 0 RETURN AFTER;
UPSERT $attempt_rid SET run_id = $run_id, repo = $repo, commit = $commit, domain = $domain,
            extractor = $extractor, status = 'staged', started_at = $now,
            store_schema_version = $store_schema_version,
            evidence_format_version = $evidence_format_version RETURN NONE;
RETURN $created;
COMMIT;`,
		map[string]any{
			"rid": extractionRunID(id), "run_id": id, "repo": repo, "commit": commit,
			"domain": domain, "extractor": extractor, "now": now,
			"attempt_rid":                extractionAttemptID(repo, domain),
			"store_schema_version":       evidenceStoreSchemaVersion,
			"evidence_format_version":    evidenceFormatVersion,
			"evidence_migration_version": evidenceMigrationVersion,
		})
	if err != nil {
		return nil, fmt.Errorf("begin extraction run: %w", err)
	}
	rows := firstExtractionRows(results)
	if len(rows) == 0 {
		return nil, errors.New("begin extraction run returned no row")
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
	merged := append(append([]string(nil), a...), b...)
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
      AND store_schema_version = $store_schema_version
	      AND evidence_format_version = $evidence_format_version
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
        supporting = array::union(supporting ?? [], $x.supporting),
        contradicting = array::union(contradicting ?? [], $x.contradicting),
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
		"store_schema_version":    evidenceStoreSchemaVersion,
		"evidence_format_version": evidenceFormatVersion,
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
      AND store_schema_version = $store_schema_version
	      AND evidence_format_version = $evidence_format_version
		  AND run_id = record::id(id)
		  AND ` + evidenceRunProbeHasNoClaimantSQL + `
		  AND published_key = NONE
	  AND retention_quarantined = false
	  AND array::len(SELECT id FROM $attempt_rid
	      WHERE run_id = $run_id AND status = 'staged'
	        AND evidence_format_version = $evidence_format_version) = 1 RETURN AFTER;
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
LET $repo_ok = IF array::len($locked) = 1 AND $counts_ok THEN
    (UPDATE $repo_rid SET evidence_revision = (evidence_revision ?? 0) + 1
        WHERE (deleting = NONE OR deleting = false) AND indexed_commit_hash = $commit RETURN AFTER)
    ELSE [] END;
LET $ready = array::len($locked) = 1 AND array::len($repo_ok) = 1 AND $counts_ok;
LET $old = IF $ready THEN
    (UPDATE extraction_run SET status = 'superseded', published_key = NONE
        WHERE repo = $repo AND domain = $domain AND status = 'published' AND run_id != $run_id
	          AND store_schema_version = $store_schema_version
	          AND evidence_format_version = $evidence_format_version
			  AND run_id = record::id(id) AND published_key != NONE
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
		  AND evidence_format_version = $evidence_format_version RETURN AFTER)
	ELSE [] END;
RETURN IF array::len($attempt) = 1 THEN $published ELSE [] END;
COMMIT;`

// PublishExtractionRun validates and flips visibility in one transaction.
// Touching both run and repo serializes against AddEvidence, reindex, deleting,
// and DeleteRepo. Coverage counts are checked against stored run rows rather
// than trusted from the extractor.
func (s *Surreal) PublishExtractionRun(ctx context.Context, runID string, coverage CoverageManifest) error {
	if coverage.AssertionCount < 0 || coverage.AtomCount < 0 || coverage.UnresolvedCount < 0 {
		return errors.New("publish extraction run: coverage counts cannot be negative")
	}
	if coverage.CorpusFileCount < 0 || coverage.CandidateFileCount < 0 || coverage.ReadFileCount < 0 ||
		coverage.ReadBytes < 0 || coverage.CandidateFileCount > coverage.ReadFileCount ||
		coverage.ReadFileCount > coverage.CorpusFileCount || coverage.CorpusFileCount > maxCoverageFileCount ||
		coverage.ReadBytes > maxCoverageReadBytes {
		return errors.New("publish extraction run: invalid or unbounded source coverage")
	}
	if !validSHA256Digest(coverage.SourceScopeDigest) {
		return errors.New("publish extraction run: source_scope_digest must be canonical sha256")
	}
	if len(coverage.Protocols) > maxEvidenceRefsPerAssertion || len(coverage.Failures) > maxEvidenceRefsPerAssertion {
		return fmt.Errorf("publish extraction run: coverage arrays exceed %d entries", maxEvidenceRefsPerAssertion)
	}
	if len(coverage.Failures) > 0 {
		return errors.New("publish extraction run: coverage contains extraction failures")
	}
	for _, value := range append(append([]string(nil), coverage.Protocols...), coverage.Failures...) {
		if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes {
			return errors.New("publish extraction run: coverage entry is not valid bounded UTF-8")
		}
	}
	run, err := s.getRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("publish extraction run: %w", err)
	}
	if run.Status != "staged" {
		return fmt.Errorf("publish extraction run: run %s is %s, not staged: %w", runID, run.Status, ErrConflict)
	}
	vars := map[string]any{
		"rid": extractionRunID(runID), "run_id": runID,
		"attempt_rid": extractionAttemptID(run.Repo, run.Domain),
		"repo_rid":    repoID(run.Repo), "repo": run.Repo, "commit": run.Commit,
		"domain": run.Domain, "published_key": publishedKey(run.Repo, run.Domain),
		"now": time.Now().UTC(), "coverage": coverage,
		"want_assertions": coverage.AssertionCount, "want_atoms": coverage.AtomCount,
		"want_unresolved":             coverage.UnresolvedCount,
		"max_occurrences_per_atom":    maxEvidenceOccurrences,
		"max_run_rows":                maxEvidenceRowsPerRun,
		"max_reference_edges":         maxEvidenceReferenceEdges,
		"store_schema_version":        evidenceStoreSchemaVersion,
		"evidence_format_version":     evidenceFormatVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
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

func (s *Surreal) AbortExtractionRun(ctx context.Context, runID string) error {
	run, err := s.getRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("abort extraction run %s: %w", runID, err)
	}
	for attempt := 0; ; attempt++ {
		vars := map[string]any{
			"rid":                     extractionRunID(runID),
			"attempt_rid":             extractionAttemptID(run.Repo, run.Domain),
			"run_id":                  runID,
			"store_schema_version":    evidenceStoreSchemaVersion,
			"evidence_format_version": evidenceFormatVersion,
		}
		addProbeVars(vars, runID)
		results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
			`BEGIN;
LET $aborted = UPDATE $rid SET status = 'aborted', published_key = NONE
				WHERE status = 'staged'
				  AND store_schema_version = $store_schema_version
					  AND evidence_format_version = $evidence_format_version
					  AND run_id = record::id(id)
					  AND `+evidenceRunProbeHasNoClaimantSQL+`
					  AND published_key = NONE
				  AND retention_quarantined = false RETURN AFTER;
LET $attempt = IF array::len($aborted) = 1 THEN
	(UPDATE $attempt_rid SET status = 'aborted'
		WHERE run_id = $run_id AND status = 'staged'
		  AND evidence_format_version = $evidence_format_version RETURN AFTER)
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

// LatestExtractionAttempt returns the durable latest attempt for a
// (repository, domain) pair. Existing stores created before attempt markers
// were introduced fall back to the latest publication until a new attempt is
// started.
func (s *Surreal) LatestExtractionAttempt(ctx context.Context, repo, domain string) (*ExtractionAttempt, error) {
	results, err := surrealdb.Query[[]extractionAttemptRec](ctx, s.db,
		`SELECT * FROM $rid WHERE repo = $repo AND domain = $domain
			AND evidence_format_version = $evidence_format_version LIMIT 1`,
		map[string]any{
			"rid": extractionAttemptID(repo, domain), "repo": repo, "domain": domain,
			"evidence_format_version": evidenceFormatVersion,
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
	run, err := s.LatestPublishedRun(ctx, repo, domain)
	if err != nil {
		return nil, err
	}
	return &ExtractionAttempt{
		RunID: run.ID, Repo: run.Repo, Commit: run.Commit, Domain: run.Domain,
		Extractor: run.Extractor, Status: "published", StartedAt: run.StartedAt,
	}, nil
}

func (s *Surreal) LatestPublishedRun(ctx context.Context, repo, domain string) (*ExtractionRun, error) {
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
		`SELECT * FROM extraction_run WHERE repo = $repo AND domain = $domain
			AND status = 'published'
				AND evidence_format_version = $evidence_format_version
				AND retention_quarantined = false
				AND run_id = record::id(id)
				AND `+evidenceRunHasNoAmbiguousClaimantSQL+`
				AND published_key = $published_key
			ORDER BY published_at DESC, run_id LIMIT 1`,
		map[string]any{
			"repo": repo, "domain": domain,
			"evidence_format_version":     evidenceFormatVersion,
			"published_key":               publishedKey(repo, domain),
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

// ListAssertions performs publication filtering in the same database
// statement as assertion selection. A two-query Go-side join could return a
// run that was superseded between queries. Stable ordering precedes LIMIT.
func (s *Surreal) ListAssertions(ctx context.Context, q AssertionQuery) ([]Assertion, error) {
	if strings.TrimSpace(q.Repo) == "" {
		return nil, errors.New("list assertions: repo scope required")
	}
	if !utf8.ValidString(q.Repo) || len(q.Repo) > maxEvidenceIdentityBytes {
		return nil, errors.New("list assertions: invalid repo scope")
	}
	if q.RunID != "" && (!utf8.ValidString(q.RunID) || len(q.RunID) > maxEvidenceIdentityBytes) {
		return nil, errors.New("list assertions: invalid run scope")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		return nil, fmt.Errorf("list assertions: maximum limit is 5000: %w", ErrResultLimit)
	}
	where := `run_id IN (SELECT VALUE run_id FROM extraction_run
		WHERE status = 'published'
		  AND repo = $repo
			  AND evidence_format_version = $evidence_format_version
			  AND retention_quarantined = false
			  AND run_id = record::id(id)
			  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
			  AND published_key != NONE)`
	vars := map[string]any{
		"limit":                       limit + 1,
		"evidence_format_version":     evidenceFormatVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	if q.RunID != "" {
		where = `run_id = $run_id AND run_id IN (SELECT VALUE run_id FROM extraction_run
			WHERE id = $run_rid
			  AND run_id = $run_id
			  AND status = 'published'
			  AND repo = $repo
			  AND evidence_format_version = $evidence_format_version
			  AND retention_quarantined = false
			  AND run_id = record::id(id)
			  AND ` + evidenceRunProbeHasNoClaimantSQL + `
			  AND published_key != NONE)`
		vars["run_id"] = q.RunID
		vars["run_rid"] = extractionRunID(q.RunID)
		addProbeVars(vars, q.RunID)
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
	if q.Lineage != "" {
		where += " AND lineage = $lineage"
		vars["lineage"] = q.Lineage
	}
	if q.Repo != "" {
		where += " AND repo = $repo"
		vars["repo"] = q.Repo
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
			id := row.AssertionID
			if id == "" && row.RecID != nil {
				id, _ = row.RecID.ID.(string)
			}
			out = append(out, Assertion{
				ID: id, Predicate: row.Predicate, Subject: row.Subject,
				Object: row.Object, Lineage: row.Lineage, Tier: row.Tier, CodeRole: row.CodeRole,
				Repo: row.Repo, Supporting: row.Supporting,
				Contradicting: row.Contradicting, RunID: row.RunID, Detail: row.Detail,
			})
		}
	}
	if len(out) > limit {
		return nil, fmt.Errorf("list assertions: more than %d rows; use a narrower query: %w", limit, ErrResultLimit)
	}
	return out, nil
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

const sweepRunSQL = `
BEGIN;
LET $locked = UPDATE $rid SET retention_revision = (retention_revision ?? 0) + 1
    WHERE (status IN ['aborted', 'superseded'] OR (status = 'staged' AND started_at <= $cutoff))
		AND evidence_format_version = $evidence_format_version
		AND retention_quarantined = false
		AND run_id = record::id(id)
		AND ` + evidenceRunProbeHasNoClaimantSQL + `
		AND published_key = NONE
    AND array::len(SELECT id FROM evidence_pin WHERE run_id = $run LIMIT 1) = 0 RETURN AFTER;
LET $attempt = IF array::len($locked) = 1 AND $locked[0].status = 'staged' THEN
	(UPDATE extraction_attempt SET status = 'aborted'
		WHERE run_id = $run AND status = 'staged'
		  AND evidence_format_version = $evidence_format_version RETURN AFTER)
	ELSE [] END;
LET $gone = SELECT atom_id FROM snapshot_evidence
    WHERE run_id = $run AND array::len($locked) = 1;
DELETE snapshot_evidence WHERE run_id = $run AND array::len($locked) = 1 RETURN NONE;
DELETE assertion WHERE run_id = $run AND array::len($locked) = 1 RETURN NONE;
FOR $row IN $gone {
    LET $aid = $row.atom_id;
    IF ((SELECT count() FROM snapshot_evidence WHERE atom_id = $aid GROUP ALL)[0].count ?? 0) = 0 {
        DELETE evidence_atom WHERE atom_id = $aid RETURN NONE
    }
};
LET $deleted = IF array::len($locked) = 1 THEN (DELETE $rid RETURN BEFORE) ELSE [] END;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`

type evidenceSweepCandidateRec struct {
	RecID *models.RecordID `json:"id"`
	RunID string           `json:"run_id"`
}

type evidenceSweepResultRec struct {
	Deleted int `json:"deleted"`
}

// SweepEvidence implements proof-aware, bounded retention. Candidate
// discovery is only an optimization: status/age and absence of a pin are
// rechecked after locking each run inside the deletion transaction.
func (s *Surreal) SweepEvidence(ctx context.Context, now time.Time, staleStagedAfter time.Duration) (int, error) {
	if staleStagedAfter < 0 {
		return 0, errors.New("sweep evidence: stale duration cannot be negative")
	}
	cutoff := now.UTC().Add(-staleStagedAfter)
	runResults, err := surrealdb.Query[[]evidenceSweepCandidateRec](ctx, s.db,
		`SELECT id, run_id, started_at OMIT started_at FROM extraction_run
            WHERE (status IN ['aborted', 'superseded'] OR (status = 'staged' AND started_at <= $cutoff))
				  AND evidence_format_version = $evidence_format_version
				  AND retention_quarantined = false
				  AND run_id = record::id(id)
				  AND `+evidenceRunHasNoAmbiguousClaimantSQL+`
				  AND published_key = NONE
				  AND run_id NOT IN (SELECT VALUE run_id FROM evidence_pin)
			ORDER BY started_at, run_id LIMIT $limit`,
		map[string]any{
			"cutoff": cutoff, "limit": evidenceSweepBatchSize,
			"evidence_format_version":     evidenceFormatVersion,
			"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
		})
	if err != nil {
		return 0, fmt.Errorf("sweep evidence: candidates: %w", err)
	}
	var candidates []evidenceSweepCandidateRec
	for _, result := range *runResults {
		for _, row := range result.Result {
			if row.RecID != nil && row.RunID != "" {
				candidates = append(candidates, row)
			}
		}
	}
	deleted := 0
	for _, candidate := range candidates {
		runID := candidate.RunID
		vars := map[string]any{
			"rid": *candidate.RecID, "run": runID, "cutoff": cutoff,
			"evidence_format_version": evidenceFormatVersion,
		}
		addProbeVars(vars, runID)
		for attempt := 0; ; attempt++ {
			results, queryErr := surrealdb.Query[[]evidenceSweepResultRec](ctx, s.db, sweepRunSQL, vars)
			if queryErr != nil {
				if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
					continue
				}
				return deleted, fmt.Errorf("sweep evidence: run %s: %w", runID, queryErr)
			}
			for _, result := range *results {
				for _, row := range result.Result {
					deleted += row.Deleted
				}
			}
			break
		}
	}
	return deleted, nil
}
