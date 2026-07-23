package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// InvestigationRetentionStore is the T16.2 boundary for explicit artifact
// ownership. Callers authorize an owner before recording it; the store then
// prevents collection until an append-only release or an approved override.
type InvestigationRetentionStore interface {
	PutRunArtifactRetentionOwner(context.Context, RunArtifactRetentionOwner) (*RunArtifactRetentionOwner, error)
	ReleaseRunArtifactRetentionOwner(context.Context, string, string, string) (*RunArtifactRetentionRelease, error)
	PutRunArtifactRetentionOverride(context.Context, RunArtifactRetentionOverride) (*RunArtifactRetentionOverride, error)
	SweepRunArtifacts(context.Context, time.Time) (int, error)
}

var _ InvestigationRetentionStore = (*Surreal)(nil)

type RunArtifactOwnerKind string

const (
	RunArtifactOwnerInvestigation RunArtifactOwnerKind = "investigation"
	RunArtifactOwnerBaseline      RunArtifactOwnerKind = "baseline"
	RunArtifactOwnerDossier       RunArtifactOwnerKind = "dossier"
)

type RunArtifactOverridePolicy string

const (
	RunArtifactOverrideRevocation        RunArtifactOverridePolicy = "revocation"
	RunArtifactOverrideMandatoryDeletion RunArtifactOverridePolicy = "mandatory_deletion"
	RunArtifactOverrideLegalPolicy       RunArtifactOverridePolicy = "legal_policy"
	RunArtifactOverrideApprovedRetention RunArtifactOverridePolicy = "approved_retention"
)

// RunArtifactRetentionOwner is an immutable, caller-authorized claim. Its key
// is deterministic for the semantic owner, making exact retries idempotent.
type RunArtifactRetentionOwner struct {
	Key           string               `json:"owner_key"`
	ArtifactID    string               `json:"artifact_id"`
	Kind          RunArtifactOwnerKind `json:"owner_kind"`
	OwnerID       string               `json:"owner_id"`
	AuthorizedBy  string               `json:"authorized_by"`
	Reason        string               `json:"reason"`
	CreatedAt     time.Time            `json:"created_at"`
	ContentDigest string               `json:"content_digest"`
}

// RunArtifactRetentionRelease ends one owner claim without mutating it.
type RunArtifactRetentionRelease struct {
	OwnerKey      string    `json:"owner_key"`
	ArtifactID    string    `json:"artifact_id"`
	ReleasedBy    string    `json:"released_by"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
	ContentDigest string    `json:"content_digest"`
}

// RunArtifactRetentionOverride records a policy basis that permits collection
// even while an owner claim remains active. It is audit data, not deletion.
type RunArtifactRetentionOverride struct {
	ID            string                    `json:"override_id"`
	ArtifactID    string                    `json:"artifact_id"`
	Policy        RunArtifactOverridePolicy `json:"policy"`
	AuthorizedBy  string                    `json:"authorized_by"`
	Reason        string                    `json:"reason"`
	CreatedAt     time.Time                 `json:"created_at"`
	ContentDigest string                    `json:"content_digest"`
}

func runArtifactPinKind(artifactID string) string {
	return "investigation-artifact:" + artifactID
}

func retentionOwnerRecordID(key string) models.RecordID {
	return models.NewRecordID("investigation_artifact_owner", key)
}

func retentionReleaseRecordID(ownerKey string) models.RecordID {
	return models.NewRecordID("investigation_artifact_owner_release", hashIdentity("iarr_", ownerKey))
}

func retentionOverrideRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_artifact_retention_override", id)
}

func computeRunArtifactRetentionOwnerKey(artifactID string, kind RunArtifactOwnerKind, ownerID string) string {
	return hashIdentity("irao_", artifactID, string(kind), ownerID)
}

func validRunArtifactOwnerKind(kind RunArtifactOwnerKind) bool {
	switch kind {
	case RunArtifactOwnerInvestigation, RunArtifactOwnerBaseline, RunArtifactOwnerDossier:
		return true
	default:
		return false
	}
}

func validRunArtifactOverridePolicy(policy RunArtifactOverridePolicy) bool {
	switch policy {
	case RunArtifactOverrideRevocation, RunArtifactOverrideMandatoryDeletion,
		RunArtifactOverrideLegalPolicy, RunArtifactOverrideApprovedRetention:
		return true
	default:
		return false
	}
}

func normalizeRunArtifactRetentionOwner(owner RunArtifactRetentionOwner) (RunArtifactRetentionOwner, error) {
	if !strings.HasPrefix(owner.ArtifactID, "ira_") || !validRunArtifactOwnerKind(owner.Kind) {
		return RunArtifactRetentionOwner{}, errors.New("artifact and supported owner kind are required")
	}
	for name, value := range map[string]string{
		"owner id": owner.OwnerID, "authorized by": owner.AuthorizedBy, "reason": owner.Reason,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return RunArtifactRetentionOwner{}, err
		}
	}
	wantKey := computeRunArtifactRetentionOwnerKey(owner.ArtifactID, owner.Kind, owner.OwnerID)
	if owner.Key != "" && owner.Key != wantKey {
		return RunArtifactRetentionOwner{}, errors.New("owner key does not match immutable identity")
	}
	owner.Key = wantKey
	digest, err := contentDigest(struct {
		Key, ArtifactID, Kind, OwnerID, AuthorizedBy, Reason string
	}{owner.Key, owner.ArtifactID, string(owner.Kind), owner.OwnerID, owner.AuthorizedBy, owner.Reason})
	if err != nil {
		return RunArtifactRetentionOwner{}, err
	}
	if owner.ContentDigest != "" && owner.ContentDigest != digest {
		return RunArtifactRetentionOwner{}, errors.New("owner content digest does not match immutable fields")
	}
	owner.ContentDigest = digest
	return owner, nil
}

// Eligibility must be a locking UPDATE, not a SELECT: incrementing each
// pinned run's retention_revision gives this transaction a write conflict
// with sweepRunSQL (which locks the same row before checking pin absence),
// exactly as pinRunSQL does. A read-only check would allow snapshot write
// skew where the sweeper deletes a superseded run while this transaction
// commits a pin to it.
const putRunArtifactWithPinsSQL = `
BEGIN;
LET $eligible = UPDATE extraction_run SET retention_revision = (retention_revision ?? 0) + 1
	WHERE run_id IN $pin_references
	  AND evidence_format_version = $evidence_format_version
	  AND retention_quarantined = false
	  AND run_id = record::id(id)
	  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
	  AND ((status = 'published' AND published_key != NONE)
	    OR (status = 'superseded' AND published_key = NONE)) RETURN AFTER;
LET $existing = SELECT artifact_id, scope, run_id, terminal_status, snapshot_manifest,
	input_manifest, coverage_ledger, fact_references, pin_references,
	eligibility_result, diagnostic_data, created_at, content_digest FROM $rid LIMIT 1;
LET $immutable = array::len($existing) = 0 OR
	($existing[0].artifact_id = $artifact_id AND $existing[0].scope = $scope
	 AND $existing[0].run_id = $run_id AND $existing[0].terminal_status = $terminal_status
	 AND $existing[0].snapshot_manifest = $snapshot_manifest
	 AND $existing[0].input_manifest = $input_manifest
	 AND $existing[0].coverage_ledger = $coverage_ledger
	 AND $existing[0].fact_references = $fact_references
	 AND $existing[0].pin_references = $pin_references
	 AND $existing[0].eligibility_result = $eligibility_result
	 AND $existing[0].diagnostic_data = $diagnostic_data
	 AND $existing[0].content_digest = $content_digest);
LET $ready = array::len($eligible) = array::len($pin_references) AND $immutable;
LET $saved = IF $ready THEN
	(UPSERT $rid SET artifact_id = $artifact_id, scope = $scope, run_id = $run_id,
		terminal_status = $terminal_status, snapshot_manifest = $snapshot_manifest,
		input_manifest = $input_manifest, coverage_ledger = $coverage_ledger,
		fact_references = $fact_references, pin_references = $pin_references,
		eligibility_result = $eligibility_result, diagnostic_data = $diagnostic_data,
		created_at = IF created_at = NONE THEN $created_at ELSE created_at END,
		content_digest = $content_digest RETURN AFTER)
	ELSE [] END;
FOR $pin IN $pins {
	IF $ready {
		UPSERT $pin.rid SET pin_key = $pin.pin_key, run_id = $pin.run_id,
			kind = $pin_kind, created_at = IF created_at = NONE THEN time::now() ELSE created_at END RETURN NONE
	}
};
RETURN $saved;
COMMIT;`

const putRunArtifactRetentionOwnerSQL = `
BEGIN;
LET $locked = UPDATE $artifact_rid SET retention_revision = (retention_revision ?? 0) + 1
	WHERE artifact_id = $artifact_id RETURN AFTER;
LET $existing = SELECT owner_key, artifact_id, owner_kind, owner_id, authorized_by,
	reason, created_at, content_digest FROM $owner_rid LIMIT 1;
LET $released = SELECT VALUE owner_key FROM $release_rid LIMIT 1;
LET $immutable = array::len($existing) = 0 OR
	($existing[0].owner_key = $owner_key AND $existing[0].artifact_id = $artifact_id
	 AND $existing[0].owner_kind = $owner_kind AND $existing[0].owner_id = $owner_id
	 AND $existing[0].authorized_by = $authorized_by AND $existing[0].reason = $reason
	 AND $existing[0].content_digest = $content_digest);
LET $ready = array::len($locked) = 1 AND array::len($released) = 0 AND $immutable;
LET $saved = IF $ready THEN
	(UPSERT $owner_rid SET owner_key = $owner_key, artifact_id = $artifact_id,
		owner_kind = $owner_kind, owner_id = $owner_id, authorized_by = $authorized_by,
		reason = $reason, created_at = IF created_at = NONE THEN $created_at ELSE created_at END,
		content_digest = $content_digest RETURN AFTER)
	ELSE [] END;
RETURN $saved;
COMMIT;`

// Unlike putRunArtifactRetentionOwnerSQL, this statement does not consult the
// release table: an exact baseline retry after its owner claim was released
// still succeeds, but the release row keeps winning the sweep's active-owner
// computation, so the retry does not resurrect retention protection.
const putBaselineWithOwnerSQL = `
BEGIN;
LET $locked = UPDATE $artifact_rid SET retention_revision = (retention_revision ?? 0) + 1
	WHERE artifact_id = $run_artifact_id AND terminal_status = 'published' RETURN AFTER;
LET $existing = SELECT designation_id, investigation_id, claim_scope, workflow_scope,
	revision_id, run_artifact_id, authority, rationale, supersedes, created_at, content_digest
	FROM $rid LIMIT 1;
LET $owner_existing = SELECT owner_key, artifact_id, owner_kind, owner_id, authorized_by,
	reason, content_digest FROM $owner_rid LIMIT 1;
LET $immutable = array::len($existing) = 0 OR
	($existing[0].designation_id = $designation_id
	 AND $existing[0].investigation_id = $investigation_id
	 AND $existing[0].claim_scope = $claim_scope
	 AND $existing[0].workflow_scope = $workflow_scope
	 AND $existing[0].revision_id = $revision_id
	 AND $existing[0].run_artifact_id = $run_artifact_id
	 AND $existing[0].authority = $authority AND $existing[0].rationale = $rationale
	 AND ($existing[0].supersedes ?? '') = $supersedes
	 AND $existing[0].content_digest = $content_digest);
LET $owner_immutable = array::len($owner_existing) = 0 OR
	($owner_existing[0].owner_key = $owner_key
	 AND $owner_existing[0].artifact_id = $run_artifact_id
	 AND $owner_existing[0].owner_kind = $owner_kind
	 AND $owner_existing[0].owner_id = $owner_id
	 AND $owner_existing[0].authorized_by = $authority
	 AND $owner_existing[0].reason = $rationale
	 AND $owner_existing[0].content_digest = $owner_digest);
LET $ready = array::len($locked) = 1 AND $immutable AND $owner_immutable;
LET $saved = IF $ready THEN
	(UPSERT $rid SET designation_id = $designation_id, investigation_id = $investigation_id,
		claim_scope = $claim_scope, workflow_scope = $workflow_scope,
		revision_id = $revision_id, run_artifact_id = $run_artifact_id,
		authority = $authority, rationale = $rationale,
		supersedes = IF $supersedes = '' THEN NONE ELSE $supersedes END,
		created_at = IF created_at = NONE THEN $created_at ELSE created_at END,
		content_digest = $content_digest RETURN AFTER)
	ELSE [] END;
IF $ready {
	UPSERT $owner_rid SET owner_key = $owner_key, artifact_id = $run_artifact_id,
		owner_kind = $owner_kind, owner_id = $owner_id, authorized_by = $authority,
		reason = $rationale, created_at = IF created_at = NONE THEN $created_at ELSE created_at END,
		content_digest = $owner_digest RETURN NONE
};
RETURN $saved;
COMMIT;`

func (s *Surreal) PutRunArtifactRetentionOwner(ctx context.Context, owner RunArtifactRetentionOwner) (*RunArtifactRetentionOwner, error) {
	owner, err := normalizeRunArtifactRetentionOwner(owner)
	if err != nil {
		return nil, fmt.Errorf("put run artifact retention owner: %w", err)
	}
	owner.CreatedAt = storeTimestamp(time.Now())
	vars := map[string]any{
		"artifact_rid": runArtifactRecordID(owner.ArtifactID), "artifact_id": owner.ArtifactID,
		"owner_rid": retentionOwnerRecordID(owner.Key), "release_rid": retentionReleaseRecordID(owner.Key),
		"owner_key": owner.Key, "owner_kind": string(owner.Kind), "owner_id": owner.OwnerID,
		"authorized_by": owner.AuthorizedBy, "reason": owner.Reason,
		"created_at": owner.CreatedAt, "content_digest": owner.ContentDigest,
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]RunArtifactRetentionOwner](ctx, s.db, putRunArtifactRetentionOwnerSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("put run artifact retention owner: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, fmt.Errorf("put run artifact retention owner: artifact is unavailable, owner was released, or immutable content conflicts: %w", ErrConflict)
		}
		return &rows[0], nil
	}
}

func normalizeRunArtifactRetentionRelease(release RunArtifactRetentionRelease) (RunArtifactRetentionRelease, error) {
	if !strings.HasPrefix(release.OwnerKey, "irao_") || !strings.HasPrefix(release.ArtifactID, "ira_") {
		return RunArtifactRetentionRelease{}, errors.New("owner and artifact identities are required")
	}
	for name, value := range map[string]string{"released by": release.ReleasedBy, "reason": release.Reason} {
		if err := validateDomainString(name, value, true); err != nil {
			return RunArtifactRetentionRelease{}, err
		}
	}
	digest, err := contentDigest(struct{ OwnerKey, ArtifactID, ReleasedBy, Reason string }{
		release.OwnerKey, release.ArtifactID, release.ReleasedBy, release.Reason,
	})
	if err != nil {
		return RunArtifactRetentionRelease{}, err
	}
	release.ContentDigest = digest
	return release, nil
}

const releaseRunArtifactRetentionOwnerSQL = `
BEGIN;
LET $owner = SELECT VALUE artifact_id FROM $owner_rid
	WHERE owner_key = $owner_key AND artifact_id = $artifact_id LIMIT 1;
LET $locked = UPDATE $artifact_rid SET retention_revision = (retention_revision ?? 0) + 1
	WHERE artifact_id = $artifact_id AND array::len($owner) = 1 RETURN AFTER;
LET $existing = SELECT owner_key, artifact_id, released_by, reason, created_at, content_digest
	FROM $release_rid LIMIT 1;
LET $immutable = array::len($existing) = 0 OR
	($existing[0].owner_key = $owner_key AND $existing[0].artifact_id = $artifact_id
	 AND $existing[0].released_by = $released_by AND $existing[0].reason = $reason
	 AND $existing[0].content_digest = $content_digest);
LET $ready = array::len($owner) = 1 AND $immutable
	AND (array::len($locked) = 1 OR array::len($existing) = 1);
LET $saved = IF $ready THEN
	(UPSERT $release_rid SET owner_key = $owner_key, artifact_id = $artifact_id,
		released_by = $released_by, reason = $reason,
		created_at = IF created_at = NONE THEN $created_at ELSE created_at END,
		content_digest = $content_digest RETURN AFTER)
	ELSE [] END;
RETURN $saved;
COMMIT;`

// ReleaseRunArtifactRetentionOwner appends one immutable release for an owner
// key. A released semantic owner cannot silently reacquire the same key.
// Once an artifact has been collected through a policy override, its surviving
// owner claims are audit rows that can no longer be released (the artifact row
// the release serializes against is gone); callers must not treat that
// conflict as a retention failure.
func (s *Surreal) ReleaseRunArtifactRetentionOwner(ctx context.Context, ownerKey, releasedBy, reason string) (*RunArtifactRetentionRelease, error) {
	if !strings.HasPrefix(ownerKey, "irao_") {
		return nil, errors.New("release run artifact retention owner: invalid owner key")
	}
	ownerResults, err := surrealdb.Query[[]RunArtifactRetentionOwner](ctx, s.db,
		`SELECT owner_key, artifact_id FROM $rid WHERE owner_key = $owner_key LIMIT 1`,
		map[string]any{"rid": retentionOwnerRecordID(ownerKey), "owner_key": ownerKey})
	if err != nil {
		return nil, fmt.Errorf("release run artifact retention owner: owner: %w", err)
	}
	owners := firstDomainRows(ownerResults)
	if len(owners) != 1 {
		return nil, fmt.Errorf("release run artifact retention owner: owner is unavailable: %w", ErrNotFound)
	}
	release, err := normalizeRunArtifactRetentionRelease(RunArtifactRetentionRelease{
		OwnerKey: ownerKey, ArtifactID: owners[0].ArtifactID, ReleasedBy: releasedBy, Reason: reason,
	})
	if err != nil {
		return nil, fmt.Errorf("release run artifact retention owner: %w", err)
	}
	release.CreatedAt = storeTimestamp(time.Now())
	vars := map[string]any{
		"owner_rid": retentionOwnerRecordID(ownerKey), "release_rid": retentionReleaseRecordID(ownerKey),
		"artifact_rid": runArtifactRecordID(release.ArtifactID), "owner_key": ownerKey,
		"artifact_id": release.ArtifactID, "released_by": release.ReleasedBy, "reason": release.Reason,
		"created_at": release.CreatedAt, "content_digest": release.ContentDigest,
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]RunArtifactRetentionRelease](ctx, s.db, releaseRunArtifactRetentionOwnerSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("release run artifact retention owner: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, fmt.Errorf("release run artifact retention owner: artifact is unavailable or immutable release conflicts: %w", ErrConflict)
		}
		return &rows[0], nil
	}
}

func normalizeRunArtifactRetentionOverride(override RunArtifactRetentionOverride) (RunArtifactRetentionOverride, error) {
	if !validULID(override.ID) || !strings.HasPrefix(override.ArtifactID, "ira_") ||
		!validRunArtifactOverridePolicy(override.Policy) {
		return RunArtifactRetentionOverride{}, errors.New("override identity, artifact, and supported policy are required")
	}
	for name, value := range map[string]string{"authorized by": override.AuthorizedBy, "reason": override.Reason} {
		if err := validateDomainString(name, value, true); err != nil {
			return RunArtifactRetentionOverride{}, err
		}
	}
	digest, err := contentDigest(struct{ ID, ArtifactID, Policy, AuthorizedBy, Reason string }{
		override.ID, override.ArtifactID, string(override.Policy), override.AuthorizedBy, override.Reason,
	})
	if err != nil {
		return RunArtifactRetentionOverride{}, err
	}
	if override.ContentDigest != "" && override.ContentDigest != digest {
		return RunArtifactRetentionOverride{}, errors.New("override content digest does not match immutable fields")
	}
	override.ContentDigest = digest
	return override, nil
}

const putRunArtifactRetentionOverrideSQL = `
BEGIN;
LET $locked = UPDATE $artifact_rid SET retention_revision = (retention_revision ?? 0) + 1
	WHERE artifact_id = $artifact_id RETURN AFTER;
LET $existing = SELECT override_id, artifact_id, policy, authorized_by, reason,
	created_at, content_digest FROM $override_rid LIMIT 1;
LET $immutable = array::len($existing) = 0 OR
	($existing[0].override_id = $override_id AND $existing[0].artifact_id = $artifact_id
	 AND $existing[0].policy = $policy AND $existing[0].authorized_by = $authorized_by
	 AND $existing[0].reason = $reason AND $existing[0].content_digest = $content_digest);
LET $ready = $immutable AND (array::len($locked) = 1 OR array::len($existing) = 1);
LET $saved = IF $ready THEN
	(UPSERT $override_rid SET override_id = $override_id, artifact_id = $artifact_id,
		policy = $policy, authorized_by = $authorized_by, reason = $reason,
		created_at = IF created_at = NONE THEN $created_at ELSE created_at END,
		content_digest = $content_digest RETURN AFTER)
	ELSE [] END;
RETURN $saved;
COMMIT;`

func (s *Surreal) PutRunArtifactRetentionOverride(ctx context.Context, override RunArtifactRetentionOverride) (*RunArtifactRetentionOverride, error) {
	now := storeTimestamp(time.Now())
	if override.ID == "" {
		id, err := newULID(now)
		if err != nil {
			return nil, fmt.Errorf("put run artifact retention override: id: %w", err)
		}
		override.ID = id
	}
	var err error
	override, err = normalizeRunArtifactRetentionOverride(override)
	if err != nil {
		return nil, fmt.Errorf("put run artifact retention override: %w", err)
	}
	override.CreatedAt = now
	vars := map[string]any{
		"artifact_rid": runArtifactRecordID(override.ArtifactID), "artifact_id": override.ArtifactID,
		"override_rid": retentionOverrideRecordID(override.ID), "override_id": override.ID,
		"policy": string(override.Policy), "authorized_by": override.AuthorizedBy,
		"reason": override.Reason, "created_at": override.CreatedAt, "content_digest": override.ContentDigest,
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]RunArtifactRetentionOverride](ctx, s.db, putRunArtifactRetentionOverrideSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("put run artifact retention override: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, fmt.Errorf("put run artifact retention override: artifact is unavailable or immutable content conflicts: %w", ErrConflict)
		}
		return &rows[0], nil
	}
}

type runArtifactSweepCandidate struct {
	RecID      *models.RecordID `json:"id"`
	ArtifactID string           `json:"artifact_id"`
}

type runArtifactSweepResult struct {
	Deleted int `json:"deleted"`
}

const sweepRunArtifactSQL = `
BEGIN;
LET $locked = UPDATE $artifact_rid SET retention_revision = (retention_revision ?? 0) + 1
	WHERE artifact_id = $artifact_id RETURN AFTER;
LET $overridden = array::len(SELECT VALUE override_id
	FROM investigation_artifact_retention_override WHERE artifact_id = $artifact_id LIMIT 1) = 1;
LET $active_owners = SELECT VALUE owner_key FROM investigation_artifact_owner
	WHERE artifact_id = $artifact_id
	  AND owner_key NOT IN (SELECT VALUE owner_key FROM investigation_artifact_owner_release);
LET $collect = array::len($locked) = 1 AND
	($overridden OR ($locked[0].created_at <= $cutoff AND array::len($active_owners) = 0));
IF $collect {
	DELETE evidence_pin WHERE kind = $pin_kind RETURN NONE
};
LET $deleted = IF $collect THEN (DELETE $artifact_rid RETURN BEFORE) ELSE [] END;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`

// SweepRunArtifacts releases at most one artifact and exactly its evidence
// pins. Ordinary expiry requires no active owner; an explicit policy override
// may collect immediately. Extraction evidence remains subject to SweepEvidence.
func (s *Surreal) SweepRunArtifacts(ctx context.Context, cutoff time.Time) (int, error) {
	if cutoff.IsZero() {
		return 0, errors.New("sweep run artifacts: cutoff is required")
	}
	var rows []runArtifactSweepCandidate
	for _, candidateSQL := range []string{
		`SELECT id, artifact_id, created_at FROM investigation_run_artifact
			WHERE artifact_id IN (SELECT VALUE artifact_id FROM investigation_artifact_retention_override)
			ORDER BY created_at, artifact_id LIMIT 1`,
		`SELECT id, artifact_id, created_at FROM investigation_run_artifact
			WHERE created_at <= $cutoff
			  AND artifact_id NOT IN (SELECT VALUE artifact_id FROM investigation_artifact_owner
				WHERE owner_key NOT IN (SELECT VALUE owner_key FROM investigation_artifact_owner_release))
			ORDER BY created_at, artifact_id LIMIT 1`,
	} {
		results, err := surrealdb.Query[[]runArtifactSweepCandidate](ctx, s.db, candidateSQL,
			map[string]any{"cutoff": cutoff.UTC()})
		if err != nil {
			return 0, fmt.Errorf("sweep run artifacts: candidate: %w", err)
		}
		rows = firstDomainRows(results)
		if len(rows) > 0 {
			break
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if rows[0].RecID == nil || !strings.HasPrefix(rows[0].ArtifactID, "ira_") {
		return 0, errors.New("sweep run artifacts: candidate identity is inconsistent")
	}
	candidate := rows[0]
	vars := map[string]any{
		"artifact_rid": *candidate.RecID, "artifact_id": candidate.ArtifactID,
		"pin_kind": runArtifactPinKind(candidate.ArtifactID), "cutoff": cutoff.UTC(),
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]runArtifactSweepResult](ctx, s.db, sweepRunArtifactSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return 0, fmt.Errorf("sweep run artifacts: artifact %s: %w", candidate.ArtifactID, queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 || rows[0].Deleted < 0 || rows[0].Deleted > 1 {
			return 0, errors.New("sweep run artifacts: invalid delete result")
		}
		return rows[0].Deleted, nil
	}
}
