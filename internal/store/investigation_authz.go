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

// InvestigationAuthzStore is the T16.3 boundary: a query-time principal
// projection over every investigation-domain read, plus the explicit sharing,
// transfer, and per-principal cursor operations of contract §7.
//
// Non-disclosure invariant: an unknown object and an object the principal is
// not authorized to read produce the identical ErrNotFound. No method
// discloses counts, existence, scope, or integrity state to an unauthorized
// principal; integrity errors surface only after authorization succeeds.
type InvestigationAuthzStore interface {
	GrantInvestigationAccess(ctx context.Context, actor, investigationID, principal, role string) (*InvestigationGrant, error)
	RevokeInvestigationAccess(ctx context.Context, actor, investigationID, principal string) error
	TransferInvestigationOwnership(ctx context.Context, actor, investigationID, newOwner string) (*Investigation, error)
	PutInvestigationCursor(ctx context.Context, principal, investigationID, cursor string) (*InvestigationCursor, error)
	GetInvestigationCursor(ctx context.Context, principal, investigationID string) (*InvestigationCursor, error)
	GetInvestigationAs(ctx context.Context, principal, id string) (*Investigation, error)
	GetRevisionAs(ctx context.Context, principal, id string) (*Revision, error)
	GetRunAs(ctx context.Context, principal, id string) (*Run, error)
	ListRunEventsAs(ctx context.Context, principal, runID string) ([]RunEvent, error)
	GetRunArtifactAs(ctx context.Context, principal, id string) (*RunArtifact, error)
	GetDecisionAs(ctx context.Context, principal, id string) (*Decision, error)
	GetDispositionAs(ctx context.Context, principal, id string) (*Disposition, error)
	GetBaselineDesignationAs(ctx context.Context, principal, id string) (*BaselineDesignation, error)
	GetWatchAs(ctx context.Context, principal, id string) (*Watch, error)
	GetWatchRevisionAs(ctx context.Context, principal, id string) (*WatchRevision, error)
}

var _ InvestigationAuthzStore = (*Surreal)(nil)

// InvestigationRoleReader is the only grantable role in this slice. Ownership
// is never a grant row: it lives on the Investigation and moves only through
// TransferInvestigationOwnership.
const InvestigationRoleReader = "reader"

// InvestigationGrant is an explicit, owner-authorized sharing edge. Grants
// carry object authorization only (contract §7): evidence and eligibility are
// recomputed for the recipient's own universe by later tickets.
type InvestigationGrant struct {
	Key                     string    `json:"grant_key"`
	InvestigationID         string    `json:"investigation_id"`
	Principal               string    `json:"principal"`
	Role                    string    `json:"role"`
	GrantedBy               string    `json:"granted_by"`
	AuthorizationGeneration string    `json:"authorization_generation"`
	CreatedAt               time.Time `json:"created_at"`
}

// InvestigationCursor is the (principal, investigation) mutable audited
// projection from contract §2. It records the authorization revision it was
// created under; an ownership transfer bumps that revision, which voids the
// cursor without erasing any other principal's state.
type InvestigationCursor struct {
	Key                     string    `json:"cursor_key"`
	InvestigationID         string    `json:"investigation_id"`
	Principal               string    `json:"principal"`
	Cursor                  string    `json:"cursor"`
	AuthzRevision           int       `json:"authz_revision"`
	AuthorizationGeneration string    `json:"authorization_generation"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func investigationGrantRecordID(investigationID, principal string) models.RecordID {
	return models.NewRecordID("investigation_grant", hashIdentity("iag_", investigationID, principal))
}

func investigationCursorRecordID(investigationID, principal string) models.RecordID {
	return models.NewRecordID("investigation_cursor", hashIdentity("iac_", investigationID, principal))
}

func validInvestigationPrincipal(principal string) bool {
	return validateDomainString("principal", principal, true) == nil &&
		strings.TrimSpace(principal) == principal
}

// investigationAuthz is the query-time projection for one (principal,
// investigation) pair. It exists only when the principal is authorized.
type investigationAuthz struct {
	InvestigationID         string `json:"investigation_id"`
	Owner                   string `json:"owner"`
	AuthzRevision           int    `json:"authz_revision"`
	AuthorizationGeneration string `json:"authorization_generation"`
}

func (a investigationAuthz) sameAuthorization(other *investigationAuthz) bool {
	return other != nil && a.InvestigationID == other.InvestigationID &&
		a.Owner == other.Owner && a.AuthzRevision == other.AuthzRevision &&
		a.AuthorizationGeneration == other.AuthorizationGeneration
}

// readInvestigationProjection authorizes one principal against one
// investigation in a single statement, so an unknown investigation and an
// unauthorized principal traverse the same code path and produce the same
// empty result. All failures are ErrNotFound; nothing else may leak.
func (s *Surreal) readInvestigationProjection(ctx context.Context, principal, investigationID string) (*investigationAuthz, error) {
	if !validInvestigationPrincipal(principal) || !validULID(investigationID) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]investigationAuthz](ctx, s.db,
		`SELECT investigation_id, owner, (authz_revision ?? 0) AS authz_revision,
			IF owner = $principal THEN '' ELSE
				((SELECT VALUE (authorization_generation ?? '') FROM $grant_rid
					WHERE grant_key = $grant_key AND investigation_id = $investigation_id
					  AND principal = $principal AND role = 'reader' LIMIT 1)[0] ?? '')
			END AS authorization_generation
			FROM $rid
			WHERE investigation_id = $investigation_id AND
			  (owner = $principal
			   OR array::len(SELECT id FROM $grant_rid
				  WHERE grant_key = $grant_key AND investigation_id = $investigation_id
					AND principal = $principal AND role = 'reader' LIMIT 1) = 1)
			LIMIT 1`,
		map[string]any{
			"rid": investigationRecordID(investigationID), "investigation_id": investigationID,
			"grant_rid": investigationGrantRecordID(investigationID, principal),
			"grant_key": hashIdentity("iag_", investigationID, principal), "principal": principal,
		})
	if err != nil {
		return nil, fmt.Errorf("authorize investigation read: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

// lookupRecordString reads one raw string binding from a record without any
// digest validation, so an object's parent investigation can be resolved and
// authorized before integrity errors are allowed to surface.
func (s *Surreal) lookupRecordString(ctx context.Context, query string, rid models.RecordID) (string, error) {
	type rawValue struct {
		Value string `json:"value"`
	}
	results, err := surrealdb.Query[[]rawValue](ctx, s.db, query, map[string]any{"rid": rid})
	if err != nil {
		return "", fmt.Errorf("resolve authorization binding: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Value == "" {
		return "", ErrNotFound
	}
	return rows[0].Value, nil
}

func (s *Surreal) investigationIDForRevision(ctx context.Context, id string) (string, error) {
	if !strings.HasPrefix(id, "ivr_") {
		return "", ErrNotFound
	}
	return s.lookupRecordString(ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1", revisionRecordID(id))
}

func (s *Surreal) investigationIDForRun(ctx context.Context, id string) (string, error) {
	if !strings.HasPrefix(id, "iru_") {
		return "", ErrNotFound
	}
	revisionID, err := s.lookupRecordString(ctx,
		"SELECT revision_id AS value FROM $rid LIMIT 1", runRecordID(id))
	if err != nil {
		return "", err
	}
	return s.investigationIDForRevision(ctx, revisionID)
}

func (s *Surreal) investigationIDForRunArtifact(ctx context.Context, id string) (string, error) {
	if !strings.HasPrefix(id, "ira_") {
		return "", ErrNotFound
	}
	runID, err := s.lookupRecordString(ctx,
		"SELECT run_id AS value FROM $rid LIMIT 1", runArtifactRecordID(id))
	if err != nil {
		return "", err
	}
	return s.investigationIDForRun(ctx, runID)
}

// confirmInvestigationProjection is the optimistic commit check for a read.
// It closes the window between authorization and integrity-checked retrieval:
// a transfer, revoke, or revoke/regrant ABA changes one of the bound epochs and
// masks both the value and any integrity error as ErrNotFound.
func (s *Surreal) confirmInvestigationProjection(ctx context.Context, principal string, expected *investigationAuthz) error {
	if expected == nil {
		return ErrNotFound
	}
	current, err := s.readInvestigationProjection(ctx, principal, expected.InvestigationID)
	if err != nil || !expected.sameAuthorization(current) {
		return ErrNotFound
	}
	return nil
}

func (s *Surreal) readAuthorizedProjection(ctx context.Context, principal, investigationID string, resolutionErr error) (*investigationAuthz, error) {
	if resolutionErr != nil {
		return nil, ErrNotFound
	}
	projection, err := s.readInvestigationProjection(ctx, principal, investigationID)
	if err != nil {
		return nil, ErrNotFound
	}
	return projection, nil
}

func (s *Surreal) GetInvestigationAs(ctx context.Context, principal, id string) (*Investigation, error) {
	projection, err := s.readInvestigationProjection(ctx, principal, id)
	if err != nil {
		return nil, ErrNotFound
	}
	value, readErr := s.GetInvestigation(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) GetRevisionAs(ctx context.Context, principal, id string) (*Revision, error) {
	investigationID, resolutionErr := s.investigationIDForRevision(ctx, id)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.GetRevision(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) GetRunAs(ctx context.Context, principal, id string) (*Run, error) {
	investigationID, resolutionErr := s.investigationIDForRun(ctx, id)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.GetRun(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) ListRunEventsAs(ctx context.Context, principal, runID string) ([]RunEvent, error) {
	investigationID, resolutionErr := s.investigationIDForRun(ctx, runID)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.ListRunEvents(ctx, runID)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) GetRunArtifactAs(ctx context.Context, principal, id string) (*RunArtifact, error) {
	investigationID, resolutionErr := s.investigationIDForRunArtifact(ctx, id)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.GetRunArtifact(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) GetDecisionAs(ctx context.Context, principal, id string) (*Decision, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	investigationID, resolutionErr := s.lookupRecordString(ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1", decisionRecordID(id))
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.GetDecision(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) GetDispositionAs(ctx context.Context, principal, id string) (*Disposition, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	investigationID, resolutionErr := s.lookupRecordString(ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1", dispositionRecordID(id))
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.GetDisposition(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) GetBaselineDesignationAs(ctx context.Context, principal, id string) (*BaselineDesignation, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	investigationID, resolutionErr := s.lookupRecordString(ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1", baselineRecordID(id))
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.GetBaselineDesignation(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

// GetWatchAs is owner-only: Watches are personal projections and investigation
// grants do not extend to them (contract §11 noise/trigger state is the
// owner's own).
func (s *Surreal) GetWatchAs(ctx context.Context, principal, id string) (*Watch, error) {
	if !validInvestigationPrincipal(principal) || !validULID(id) {
		return nil, ErrNotFound
	}
	owner, err := s.lookupRecordString(ctx,
		"SELECT owner AS value FROM $rid LIMIT 1", watchRecordID(id))
	if err != nil || owner != principal {
		return nil, ErrNotFound
	}
	value, readErr := s.GetWatch(ctx, id)
	currentOwner, confirmErr := s.lookupRecordString(ctx,
		"SELECT owner AS value FROM $rid LIMIT 1", watchRecordID(id))
	if confirmErr != nil || currentOwner != principal || currentOwner != owner {
		return nil, ErrNotFound
	}
	return value, readErr
}

func (s *Surreal) GetWatchRevisionAs(ctx context.Context, principal, id string) (*WatchRevision, error) {
	if !validInvestigationPrincipal(principal) || !strings.HasPrefix(id, "iwr_") {
		return nil, ErrNotFound
	}
	watchID, err := s.lookupRecordString(ctx,
		"SELECT watch_id AS value FROM $rid LIMIT 1", watchRevisionRecordID(id))
	if err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.GetWatchAs(ctx, principal, watchID); err != nil {
		return nil, ErrNotFound
	}
	value, readErr := s.GetWatchRevision(ctx, id)
	if _, err := s.GetWatchAs(ctx, principal, watchID); err != nil {
		return nil, ErrNotFound
	}
	return value, readErr
}

func addInvestigationAuditVars(vars map[string]any, action, target, actor string, now time.Time) error {
	id, err := authRandomID()
	if err != nil {
		return err
	}
	vars["audit_rid"] = auditEventID(id)
	vars["audit_action"] = action
	vars["audit_target"] = target
	vars["audit_actor"] = actor
	vars["now"] = now
	return nil
}

const grantInvestigationAccessSQL = `
BEGIN;
LET $locked = UPDATE $investigation_rid
	SET authz_write_revision = (authz_write_revision ?? 0) + 1
	WHERE investigation_id = $investigation_id AND owner = $actor AND owner != $principal
	RETURN AFTER;
LET $existing = SELECT grant_key, investigation_id, principal, role,
	granted_by, authorization_generation, created_at FROM $grant_rid LIMIT 1;
LET $compatible = array::len($existing) = 0 OR
	($existing[0].grant_key = $grant_key
	 AND $existing[0].investigation_id = $investigation_id
	 AND $existing[0].principal = $principal AND $existing[0].role = $role);
LET $saved = IF array::len($locked) = 1 AND $compatible THEN
	(UPSERT $grant_rid SET grant_key = $grant_key, investigation_id = $investigation_id,
		principal = $principal, role = $role,
		granted_by = IF granted_by = NONE OR granted_by = '' THEN $actor ELSE granted_by END,
		authorization_generation = IF authorization_generation = NONE OR authorization_generation = ''
			THEN $authorization_generation ELSE authorization_generation END,
		created_at = IF created_at = NONE THEN $now ELSE created_at END RETURN AFTER)
	ELSE [] END;
IF array::len($saved) = 1 {
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE
};
RETURN $saved;
COMMIT;`

func (s *Surreal) GrantInvestigationAccess(ctx context.Context, actor, investigationID, principal, role string) (*InvestigationGrant, error) {
	if role != InvestigationRoleReader {
		return nil, errors.New("grant investigation access: unsupported role")
	}
	if !validInvestigationPrincipal(principal) {
		return nil, errors.New("grant investigation access: invalid principal")
	}
	if !validInvestigationPrincipal(actor) || !validULID(investigationID) {
		return nil, ErrNotFound
	}
	generation, err := authRandomID()
	if err != nil {
		return nil, fmt.Errorf("grant investigation access: generation: %w", err)
	}
	now := storeTimestamp(time.Now())
	vars := map[string]any{
		"investigation_rid": investigationRecordID(investigationID),
		"grant_rid":         investigationGrantRecordID(investigationID, principal),
		"grant_key":         hashIdentity("iag_", investigationID, principal),
		"investigation_id":  investigationID, "principal": principal,
		"role": role, "actor": actor, "authorization_generation": generation,
	}
	if err := addInvestigationAuditVars(vars, "investigation.grant", investigationID+" -> "+principal, actor, now); err != nil {
		return nil, fmt.Errorf("grant investigation access: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]InvestigationGrant](ctx, s.db, grantInvestigationAccessSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("grant investigation access: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, ErrNotFound
		}
		return &rows[0], nil
	}
}

type investigationAuthzMutationResult struct {
	Authorized int `json:"authorized"`
	Deleted    int `json:"deleted"`
}

const revokeInvestigationAccessSQL = `
BEGIN;
LET $locked = UPDATE $investigation_rid
	SET authz_write_revision = (authz_write_revision ?? 0) + 1
	WHERE investigation_id = $investigation_id AND owner = $actor RETURN AFTER;
LET $deleted = IF array::len($locked) = 1 THEN
	(DELETE $grant_rid WHERE grant_key = $grant_key
		AND investigation_id = $investigation_id AND principal = $principal RETURN BEFORE)
	ELSE [] END;
IF array::len($locked) = 1 {
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE
};
RETURN [{ authorized: array::len($locked), deleted: array::len($deleted) }];
COMMIT;`

func (s *Surreal) RevokeInvestigationAccess(ctx context.Context, actor, investigationID, principal string) error {
	if !validInvestigationPrincipal(principal) {
		return errors.New("revoke investigation access: invalid principal")
	}
	if !validInvestigationPrincipal(actor) || !validULID(investigationID) {
		return ErrNotFound
	}
	now := storeTimestamp(time.Now())
	vars := map[string]any{
		"investigation_rid": investigationRecordID(investigationID),
		"grant_rid":         investigationGrantRecordID(investigationID, principal),
		"grant_key":         hashIdentity("iag_", investigationID, principal),
		"investigation_id":  investigationID, "principal": principal, "actor": actor,
	}
	if err := addInvestigationAuditVars(vars, "investigation.revoke", investigationID+" -> "+principal, actor, now); err != nil {
		return fmt.Errorf("revoke investigation access: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]investigationAuthzMutationResult](ctx, s.db, revokeInvestigationAccessSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("revoke investigation access: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 || rows[0].Authorized != 1 {
			return ErrNotFound
		}
		return nil
	}
}

const transferInvestigationOwnershipSQL = `
BEGIN;
LET $moved = UPDATE $investigation_rid SET owner = $new_owner,
	authz_revision = (authz_revision ?? 0) + 1,
	authz_write_revision = (authz_write_revision ?? 0) + 1, updated_at = $now
	WHERE investigation_id = $investigation_id AND owner = $actor RETURN AFTER;
IF array::len($moved) = 1 {
	DELETE $new_owner_grant_rid WHERE grant_key = $new_owner_grant_key
		AND investigation_id = $investigation_id AND principal = $new_owner RETURN NONE;
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE
};
RETURN $moved;
COMMIT;`

// TransferInvestigationOwnership is the only ownership path (UpdateInvestigation
// rejects owner changes). It serializes on the current owner and bumps the
// authorization revision, voiding every per-principal cursor for the
// investigation without erasing any principal's stored state (contract §7).
func (s *Surreal) TransferInvestigationOwnership(ctx context.Context, actor, investigationID, newOwner string) (*Investigation, error) {
	if !validInvestigationPrincipal(newOwner) {
		return nil, errors.New("transfer investigation ownership: invalid new owner")
	}
	if !validInvestigationPrincipal(actor) || !validULID(investigationID) {
		return nil, ErrNotFound
	}
	now := storeTimestamp(time.Now())
	vars := map[string]any{
		"investigation_rid": investigationRecordID(investigationID),
		"investigation_id":  investigationID, "new_owner": newOwner, "actor": actor,
		"new_owner_grant_rid": investigationGrantRecordID(investigationID, newOwner),
		"new_owner_grant_key": hashIdentity("iag_", investigationID, newOwner),
	}
	if err := addInvestigationAuditVars(vars, "investigation.transfer", investigationID+" -> "+newOwner, actor, now); err != nil {
		return nil, fmt.Errorf("transfer investigation ownership: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]Investigation](ctx, s.db, transferInvestigationOwnershipSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("transfer investigation ownership: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, ErrNotFound
		}
		return &rows[0], nil
	}
}

const putInvestigationCursorSQL = `
BEGIN;
LET $locked = UPDATE $investigation_rid
	SET authz_write_revision = (authz_write_revision ?? 0) + 1
	WHERE investigation_id = $investigation_id AND
	  (owner = $principal OR array::len(SELECT id FROM $grant_rid
		WHERE grant_key = $grant_key AND investigation_id = $investigation_id
		  AND principal = $principal AND role = 'reader' LIMIT 1) = 1)
	RETURN AFTER;
LET $grant = SELECT authorization_generation FROM $grant_rid
	WHERE grant_key = $grant_key AND investigation_id = $investigation_id
	  AND principal = $principal AND role = 'reader' LIMIT 1;
LET $authorization_generation = IF array::len($locked) = 1 AND $locked[0].owner = $principal
	THEN '' ELSE ($grant[0].authorization_generation ?? '') END;
LET $saved = IF array::len($locked) = 1 THEN
	(UPSERT $cursor_rid SET cursor_key = $cursor_key, investigation_id = $investigation_id,
		principal = $principal, cursor = $cursor,
		authz_revision = ($locked[0].authz_revision ?? 0),
		authorization_generation = $authorization_generation,
		created_at = IF created_at = NONE THEN $now ELSE created_at END,
		updated_at = $now RETURN AFTER)
	ELSE [] END;
IF array::len($saved) = 1 {
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE
};
RETURN $saved;
COMMIT;`

func (s *Surreal) PutInvestigationCursor(ctx context.Context, principal, investigationID, cursor string) (*InvestigationCursor, error) {
	if err := validateDomainString("cursor", cursor, true); err != nil {
		return nil, fmt.Errorf("put investigation cursor: %w", err)
	}
	if !validInvestigationPrincipal(principal) || !validULID(investigationID) {
		return nil, ErrNotFound
	}
	now := storeTimestamp(time.Now())
	vars := map[string]any{
		"investigation_rid": investigationRecordID(investigationID),
		"grant_rid":         investigationGrantRecordID(investigationID, principal),
		"grant_key":         hashIdentity("iag_", investigationID, principal),
		"cursor_rid":        investigationCursorRecordID(investigationID, principal),
		"cursor_key":        hashIdentity("iac_", investigationID, principal),
		"investigation_id":  investigationID, "principal": principal, "cursor": cursor,
	}
	if err := addInvestigationAuditVars(vars, "investigation.cursor", investigationID+" -> "+principal, principal, now); err != nil {
		return nil, fmt.Errorf("put investigation cursor: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]InvestigationCursor](ctx, s.db, putInvestigationCursorSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("put investigation cursor: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, ErrNotFound
		}
		return &rows[0], nil
	}
}

// GetInvestigationCursor returns ErrNotFound for a cursor recorded under an
// earlier authorization revision: an ownership transfer voids continuation
// state without disclosing why.
func (s *Surreal) GetInvestigationCursor(ctx context.Context, principal, investigationID string) (*InvestigationCursor, error) {
	projection, err := s.readInvestigationProjection(ctx, principal, investigationID)
	if err != nil {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]InvestigationCursor](ctx, s.db,
		"SELECT cursor_key, investigation_id, principal, cursor, authz_revision, authorization_generation, created_at, updated_at FROM $rid LIMIT 1",
		map[string]any{"rid": investigationCursorRecordID(investigationID, principal)})
	if err != nil {
		return nil, fmt.Errorf("get investigation cursor: %w", err)
	}
	rows := firstDomainRows(results)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	wantKey := hashIdentity("iac_", investigationID, principal)
	if len(rows) != 1 || rows[0].Key != wantKey || rows[0].InvestigationID != investigationID ||
		rows[0].Principal != principal || rows[0].AuthzRevision != projection.AuthzRevision ||
		rows[0].AuthorizationGeneration != projection.AuthorizationGeneration {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}
