package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// PartitionedAssertionAuthority binds a page to the exact current domain
// selected by a native partitioned reader. It is not a legacy publication key
// or a permission to read arbitrary staged runs. One run spans its bounded
// domain partitions; this API grants no repository-wide attribution authority.
type PartitionedAssertionAuthority struct {
	Repository              string
	Domain                  string
	RunID                   string
	PlanDigest              string
	RootDigest              string
	Commit                  string
	UnitDigest              string
	CandidateManifestDigest string
	CandidatePolicyDigest   string
}

// This compact point-read projection deliberately does not fetch or decode the
// potentially multi-megabyte immutable plan/root on each assertion page. The
// declaration selector validates those controls once; every page independently
// checks their persisted identity and the native staged/sealed run fence.
const partitionedAssertionFenceSQL = `SELECT VALUE run_id FROM $root_rid
WHERE schema = $root_schema AND repository = $repo AND domain = $domain
	AND run_id = $run_id AND plan_digest = $plan_digest AND root_digest = $root_digest
	AND candidate_digest = $candidate_digest
	AND run_id IN (SELECT VALUE run_id FROM $run_rid
		WHERE run_id = $run_id AND run_id = record::id(id)
		AND repo = $repo AND domain = $domain AND commit = $commit
		AND (unit_digest ?? '') = $unit_digest
		AND status = 'staged' AND published_key = NONE
		AND (partition_sealed ?? false) = true AND (partition_active ?? false) = false
		AND partition_plan_digest = $plan_digest AND partition_root_digest = $root_digest
		AND partition_candidate_digest = $candidate_digest
		AND retention_quarantined = false
		AND store_schema_version = $store_schema_version
		AND evidence_format_version = $evidence_format_version
		AND evidence_migration_version = $evidence_migration_version
		AND ` + evidenceRunProbeHasNoClaimantSQL + ` LIMIT 1)
	AND array::len(SELECT id FROM $candidate_rid
		WHERE repository = $repo AND head_commit = $commit
		AND (unit_digest ?? '') = $unit_digest AND manifest_digest = $candidate_digest
		AND policy_digest = $candidate_policy_digest LIMIT 1) = 1
	AND array::len(SELECT id FROM $repo_rid
		WHERE name = $repo AND indexed_commit_hash = $commit
		AND (indexed_analysis_unit.digest ?? '') = $unit_digest
		AND (deleting ?? false) = false LIMIT 1) = 1
LIMIT 1`

// ListPartitionedAssertions preserves ListAssertions' ordering and page ceiling
// without relaxing its legacy published-only visibility. An absent/superseded
// initial authority is not-found; an invalidated continuation or an authority
// transition during a page is a conflict, and no partial page is returned.
func (s *Surreal) ListPartitionedAssertions(
	ctx context.Context, q AssertionQuery, authority PartitionedAssertionAuthority,
) ([]Assertion, error) {
	limit, err := validatePartitionedAssertionQuery(q, authority)
	if err != nil {
		return nil, err
	}
	vars := partitionedAssertionVariables(authority)
	vars["limit"] = limit + 1
	current, err := s.partitionedAssertionAuthorityCurrent(ctx, vars)
	if err != nil {
		return nil, err
	}
	if !current {
		cause := ErrNotFound
		if q.After != nil {
			cause = ErrConflict
		}
		return nil, fmt.Errorf("list partitioned assertions: selected authority is not current: %w", cause)
	}
	where := "repo = $repo AND run_id = $run_id AND run_id IN $current"
	for _, filter := range []struct{ field, value string }{
		{"predicate", q.Predicate}, {"subject", q.Subject}, {"object", q.Object}, {"lineage", q.Lineage},
	} {
		if filter.value != "" {
			where += " AND " + filter.field + " = $" + filter.field
			vars[filter.field] = filter.value
		}
	}
	if q.ObjectPrefix != "" {
		where += " AND string::starts_with(object, $object_prefix)"
		vars["object_prefix"] = q.ObjectPrefix
	}
	if q.After != nil {
		where += ` AND (
			predicate > $after_predicate OR
			(predicate = $after_predicate AND subject > $after_subject) OR
			(predicate = $after_predicate AND subject = $after_subject AND object > $after_object) OR
			(predicate = $after_predicate AND subject = $after_subject AND object = $after_object
				AND assertion_id > $after_id) OR
			(predicate = $after_predicate AND subject = $after_subject AND object = $after_object
				AND assertion_id = $after_id AND run_id > $after_run_id))`
		vars["after_predicate"], vars["after_subject"], vars["after_object"] = q.After.Predicate, q.After.Subject, q.After.Object
		vars["after_id"], vars["after_run_id"] = q.After.ID, q.After.RunID
	}
	type page struct {
		Visible bool           `json:"visible"`
		Rows    []assertionRec `json:"rows"`
	}
	results, err := storeQuery[page](ctx, s.accounting, s.db,
		"LET $current = ("+partitionedAssertionFenceSQL+"); RETURN {visible: array::len($current) = 1, rows: (SELECT * FROM assertion WHERE "+where+
			" ORDER BY predicate, subject, object, assertion_id, run_id LIMIT $limit)};", vars, storeRead())
	if err != nil {
		return nil, fmt.Errorf("list partitioned assertions: page: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return nil, errors.New("list partitioned assertions: missing page result")
	}
	value := (*results)[len(*results)-1].Result
	current, err = s.partitionedAssertionAuthorityCurrent(ctx, vars)
	if err != nil {
		return nil, err
	}
	if !value.Visible || !current {
		return nil, fmt.Errorf("list partitioned assertions: authority changed during page: %w", ErrConflict)
	}
	if len(value.Rows) > limit && !q.AllowTruncate {
		return nil, fmt.Errorf("list partitioned assertions: more than %d rows: %w", limit, ErrResultLimit)
	}
	out := make([]Assertion, 0, len(value.Rows))
	for _, row := range value.Rows {
		out = append(out, row.assertion())
	}
	return out, nil
}

func (s *Surreal) partitionedAssertionAuthorityCurrent(ctx context.Context, vars map[string]any) (bool, error) {
	results, err := storeQuery[bool](ctx, s.accounting, s.db, "RETURN array::len("+partitionedAssertionFenceSQL+") = 1", vars, storeRead())
	if err != nil {
		return false, fmt.Errorf("list partitioned assertions: authority: %w", err)
	}
	if results == nil || len(*results) != 1 {
		return false, errors.New("list partitioned assertions: missing authority result")
	}
	return (*results)[0].Result, nil
}

func partitionedAssertionVariables(authority PartitionedAssertionAuthority) map[string]any {
	vars := map[string]any{
		"root_rid": partitionedDomainID(authority.Repository, authority.Domain), "root_schema": PartitionedExtractionDomainSchema,
		"run_rid": extractionRunID(authority.RunID), "run_id": authority.RunID,
		"repo_rid": repoID(authority.Repository), "repo": authority.Repository,
		"domain": authority.Domain, "commit": authority.Commit, "unit_digest": authority.UnitDigest,
		"plan_digest": authority.PlanDigest, "root_digest": authority.RootDigest,
		"candidate_rid":    candidateManifestPublicationID(authority.Repository),
		"candidate_digest": authority.CandidateManifestDigest, "candidate_policy_digest": authority.CandidatePolicyDigest,
		"store_schema_version": evidenceStoreSchemaVersion, "evidence_format_version": evidenceFormatVersion,
		"evidence_migration_version": evidenceMigrationVersion,
	}
	addProbeVars(vars, authority.RunID)
	return vars
}

// ResolvePartitionedEvidence resolves only bounded stored locators, never source
// blobs. Callers must select the native run kind explicitly; legacy resolution
// and historical pin semantics remain owned by ResolveEvidence.
func (s *Surreal) ResolvePartitionedEvidence(
	ctx context.Context, authority PartitionedAssertionAuthority, atomID string,
) (*EvidenceResolution, error) {
	if _, err := validatePartitionedAssertionQuery(AssertionQuery{Repo: authority.Repository, RunID: authority.RunID}, authority); err != nil {
		return nil, err
	}
	if atomID == "" || !utf8.ValidString(atomID) || len(atomID) > maxEvidenceIdentityBytes {
		return nil, errors.New("resolve partitioned evidence: invalid atom identity")
	}
	vars := partitionedAssertionVariables(authority)
	vars["atom"], vars["limit"] = atomID, maxEvidenceOccurrences+1
	current, err := s.partitionedAssertionAuthorityCurrent(ctx, vars)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, fmt.Errorf("resolve partitioned evidence: selected authority is not current: %w", ErrNotFound)
	}
	type resolution struct {
		Visible bool                    `json:"visible"`
		Rows    []evidenceResolutionRec `json:"rows"`
	}
	results, err := storeQuery[resolution](ctx, s.accounting, s.db,
		"LET $current = ("+partitionedAssertionFenceSQL+"); RETURN {visible: array::len($current) = 1, rows: ("+
			`SELECT * FROM snapshot_evidence WHERE repo = $repo AND run_id = $run_id
			AND run_id IN $current AND commit = $commit AND atom_id = $atom
			ORDER BY occurrence_id LIMIT $limit FETCH atom_record)};`, vars, storeRead())
	if err != nil {
		return nil, fmt.Errorf("resolve partitioned evidence: locators: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return nil, errors.New("resolve partitioned evidence: missing locator result")
	}
	value := (*results)[len(*results)-1].Result
	current, err = s.partitionedAssertionAuthorityCurrent(ctx, vars)
	if err != nil {
		return nil, err
	}
	if !value.Visible || !current {
		return nil, fmt.Errorf("resolve partitioned evidence: authority changed: %w", ErrConflict)
	}
	if len(value.Rows) == 0 {
		return nil, ErrNotFound
	}
	if len(value.Rows) > maxEvidenceOccurrences {
		return nil, fmt.Errorf("resolve partitioned evidence: too many occurrences: %w", ErrResultLimit)
	}
	resolved := &EvidenceResolution{Atom: value.Rows[0].Atom, Occurrences: make([]SnapshotEvidence, 0, len(value.Rows))}
	if resolved.Atom.ID != atomID {
		return nil, errors.New("resolve partitioned evidence: inconsistent atom linkage")
	}
	for _, row := range value.Rows {
		if row.Atom.ID != atomID || row.Repo != authority.Repository || row.RunID != authority.RunID ||
			row.Commit != authority.Commit || row.Visibility != "repo:"+authority.Repository {
			return nil, errors.New("resolve partitioned evidence: inconsistent occurrence linkage")
		}
		resolved.Occurrences = append(resolved.Occurrences, SnapshotEvidence{
			ID: row.OccurrenceID, AtomID: row.AtomID, Repo: row.Repo, Commit: row.Commit,
			Path: row.Path, StartLine: row.StartLine, EndLine: row.EndLine,
			VisibilityScope: row.Visibility, RunID: row.RunID, ObservedAt: row.ObservedAt,
		})
	}
	return resolved, nil
}

func validatePartitionedAssertionQuery(q AssertionQuery, authority PartitionedAssertionAuthority) (int, error) {
	scope := ExtractionScope{Repository: authority.Repository, Commit: authority.Commit, UnitDigest: authority.UnitDigest, Domain: authority.Domain}
	if validateExtractionScope(scope) != nil || !validGitObjectID(authority.Commit) ||
		q.Repo != authority.Repository || q.RunID != authority.RunID || q.RunID == "" ||
		q.Scope != nil && *q.Scope != scope || q.Object != "" && q.ObjectPrefix != "" {
		return 0, errors.New("list partitioned assertions: invalid exact scope")
	}
	for _, digest := range []string{authority.PlanDigest, authority.RootDigest, authority.CandidateManifestDigest, authority.CandidatePolicyDigest} {
		if !validSHA256Digest(digest) {
			return 0, errors.New("list partitioned assertions: invalid authority digest")
		}
	}
	for _, value := range []string{authority.Repository, authority.Domain, authority.RunID, q.Predicate, q.Subject, q.Object, q.ObjectPrefix, q.Lineage} {
		if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes || strings.TrimSpace(value) != value {
			return 0, errors.New("list partitioned assertions: invalid bounded query value")
		}
	}
	if len(authority.Domain) > 128 {
		return 0, errors.New("list partitioned assertions: invalid domain")
	}
	if q.After != nil {
		if q.After.RunID != authority.RunID {
			return 0, fmt.Errorf("list partitioned assertions: continuation run changed: %w", ErrConflict)
		}
		if q.After.ID == "" {
			return 0, errors.New("list partitioned assertions: continuation assertion id required")
		}
		for _, value := range []string{q.After.Predicate, q.After.Subject, q.After.Object, q.After.ID, q.After.RunID} {
			if !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes {
				return 0, errors.New("list partitioned assertions: invalid bounded continuation")
			}
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		return 0, fmt.Errorf("list partitioned assertions: maximum limit is 5000: %w", ErrResultLimit)
	}
	return limit, nil
}
