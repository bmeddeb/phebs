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
	Key             string    `json:"grant_key"`
	InvestigationID string    `json:"investigation_id"`
	Principal       string    `json:"principal"`
	Role            string    `json:"role"`
	GrantedBy       string    `json:"granted_by"`
	CreatedAt       time.Time `json:"created_at"`
}

// InvestigationCursor is the (principal, investigation) mutable audited
// projection from contract §2. It records the authorization revision it was
// created under; an ownership transfer bumps that revision, which voids the
// cursor without erasing any other principal's state.
type InvestigationCursor struct {
	Key             string    `json:"cursor_key"`
	InvestigationID string    `json:"investigation_id"`
	Principal       string    `json:"principal"`
	Cursor          string    `json:"cursor"`
	AuthzRevision   int       `json:"authz_revision"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
	InvestigationID string `json:"investigation_id"`
	Owner           string `json:"owner"`
	AuthzRevision   int    `json:"authz_revision"`
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
		`SELECT investigation_id, owner, (authz_revision ?? 0) AS authz_revision FROM $rid
			WHERE owner = $principal
			   OR array::len(SELECT id FROM $grant_rid WHERE principal = $principal LIMIT 1) = 1
			LIMIT 1`,
		map[string]any{
			"rid":       investigationRecordID(investigationID),
			"grant_rid": investigationGrantRecordID(investigationID, principal),
			"principal": principal,
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

// readInvestigationOwnerProjection is the owner-only variant used by sharing
// and transfer. A non-owner (including a grantee) gets ErrNotFound so grant
// authority is never disclosed to principals who do not hold it.
func (s *Surreal) readInvestigationOwnerProjection(ctx context.Context, actor, investigationID string) (*investigationAuthz, error) {
	if !validInvestigationPrincipal(actor) || !validULID(investigationID) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]investigationAuthz](ctx, s.db,
		`SELECT investigation_id, owner, (authz_revision ?? 0) AS authz_revision FROM $rid
			WHERE owner = $actor LIMIT 1`,
		map[string]any{"rid": investigationRecordID(investigationID), "actor": actor})
	if err != nil {
		return nil, fmt.Errorf("authorize investigation ownership: %w", err)
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

// authorizeInvestigationObject masks every resolution failure as ErrNotFound:
// an unauthorized principal must not distinguish a missing object from a
// present one, including through integrity or classification errors.
func (s *Surreal) authorizeInvestigationObject(ctx context.Context, principal string, investigationID string, err error) error {
	if err != nil {
		return ErrNotFound
	}
	if _, authErr := s.readInvestigationProjection(ctx, principal, investigationID); authErr != nil {
		return ErrNotFound
	}
	return nil
}

func (s *Surreal) GetInvestigationAs(ctx context.Context, principal, id string) (*Investigation, error) {
	if _, err := s.readInvestigationProjection(ctx, principal, id); err != nil {
		return nil, ErrNotFound
	}
	return s.GetInvestigation(ctx, id)
}

func (s *Surreal) GetRevisionAs(ctx context.Context, principal, id string) (*Revision, error) {
	investigationID, err := s.investigationIDForRevision(ctx, id)
	if err := s.authorizeInvestigationObject(ctx, principal, investigationID, err); err != nil {
		return nil, err
	}
	return s.GetRevision(ctx, id)
}

func (s *Surreal) GetRunAs(ctx context.Context, principal, id string) (*Run, error) {
	investigationID, err := s.investigationIDForRun(ctx, id)
	if err := s.authorizeInvestigationObject(ctx, principal, investigationID, err); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, id)
}

func (s *Surreal) ListRunEventsAs(ctx context.Context, principal, runID string) ([]RunEvent, error) {
	investigationID, err := s.investigationIDForRun(ctx, runID)
	if err := s.authorizeInvestigationObject(ctx, principal, investigationID, err); err != nil {
		return nil, err
	}
	return s.ListRunEvents(ctx, runID)
}

func (s *Surreal) GetRunArtifactAs(ctx context.Context, principal, id string) (*RunArtifact, error) {
	investigationID, err := s.investigationIDForRunArtifact(ctx, id)
	if err := s.authorizeInvestigationObject(ctx, principal, investigationID, err); err != nil {
		return nil, err
	}
	return s.GetRunArtifact(ctx, id)
}

func (s *Surreal) GetDecisionAs(ctx context.Context, principal, id string) (*Decision, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	investigationID, err := s.lookupRecordString(ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1", decisionRecordID(id))
	if err := s.authorizeInvestigationObject(ctx, principal, investigationID, err); err != nil {
		return nil, err
	}
	return s.GetDecision(ctx, id)
}

func (s *Surreal) GetDispositionAs(ctx context.Context, principal, id string) (*Disposition, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	investigationID, err := s.lookupRecordString(ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1", dispositionRecordID(id))
	if err := s.authorizeInvestigationObject(ctx, principal, investigationID, err); err != nil {
		return nil, err
	}
	return s.GetDisposition(ctx, id)
}

func (s *Surreal) GetBaselineDesignationAs(ctx context.Context, principal, id string) (*BaselineDesignation, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	investigationID, err := s.lookupRecordString(ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1", baselineRecordID(id))
	if err := s.authorizeInvestigationObject(ctx, principal, investigationID, err); err != nil {
		return nil, err
	}
	return s.GetBaselineDesignation(ctx, id)
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
	return s.GetWatch(ctx, id)
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
	return s.GetWatchRevision(ctx, id)
}

func (s *Surreal) appendAuthzAudit(ctx context.Context, action, target, actor string) error {
	if err := s.AppendAuditEvent(ctx, AuditEvent{
		Action: action, Target: target, ActorID: actor, Status: 200,
	}); err != nil {
		// The mutation has already committed; surface the audit failure loudly
		// rather than hiding an unrecorded authorization change.
		return fmt.Errorf("%s: audit: %w", action, err)
	}
	return nil
}

func (s *Surreal) GrantInvestigationAccess(ctx context.Context, actor, investigationID, principal, role string) (*InvestigationGrant, error) {
	if role != InvestigationRoleReader {
		return nil, errors.New("grant investigation access: unsupported role")
	}
	if !validInvestigationPrincipal(principal) {
		return nil, errors.New("grant investigation access: invalid principal")
	}
	owner, err := s.readInvestigationOwnerProjection(ctx, actor, investigationID)
	if err != nil {
		return nil, ErrNotFound
	}
	if principal == owner.Owner {
		return nil, fmt.Errorf("grant investigation access: owner does not need a grant: %w", ErrConflict)
	}
	now := storeTimestamp(time.Now())
	results, err := surrealdb.Query[[]InvestigationGrant](ctx, s.db,
		`UPSERT $rid SET grant_key = $grant_key, investigation_id = $investigation_id,
			principal = $principal, role = $role, granted_by = $granted_by,
			created_at = IF created_at = NONE THEN $now ELSE created_at END RETURN AFTER`,
		map[string]any{
			"rid":       investigationGrantRecordID(investigationID, principal),
			"grant_key": hashIdentity("iag_", investigationID, principal),
			"investigation_id": investigationID, "principal": principal,
			"role": role, "granted_by": actor, "now": now,
		})
	if err != nil {
		return nil, fmt.Errorf("grant investigation access: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("grant investigation access returned no row")
	}
	if err := s.appendAuthzAudit(ctx, "investigation.grant", investigationID+" -> "+principal, actor); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

func (s *Surreal) RevokeInvestigationAccess(ctx context.Context, actor, investigationID, principal string) error {
	if !validInvestigationPrincipal(principal) {
		return errors.New("revoke investigation access: invalid principal")
	}
	if _, err := s.readInvestigationOwnerProjection(ctx, actor, investigationID); err != nil {
		return ErrNotFound
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		"DELETE $rid RETURN NONE",
		map[string]any{"rid": investigationGrantRecordID(investigationID, principal)}); err != nil {
		return fmt.Errorf("revoke investigation access: %w", err)
	}
	return s.appendAuthzAudit(ctx, "investigation.revoke", investigationID+" -> "+principal, actor)
}

// TransferInvestigationOwnership is the only ownership path (UpdateInvestigation
// rejects owner changes). It serializes on the current owner and bumps the
// authorization revision, voiding every per-principal cursor for the
// investigation without erasing any principal's stored state (contract §7).
func (s *Surreal) TransferInvestigationOwnership(ctx context.Context, actor, investigationID, newOwner string) (*Investigation, error) {
	if !validInvestigationPrincipal(newOwner) {
		return nil, errors.New("transfer investigation ownership: invalid new owner")
	}
	if _, err := s.readInvestigationOwnerProjection(ctx, actor, investigationID); err != nil {
		return nil, ErrNotFound
	}
	now := storeTimestamp(time.Now())
	results, err := surrealdb.Query[[]Investigation](ctx, s.db,
		`UPDATE $rid SET owner = $new_owner,
			authz_revision = (authz_revision ?? 0) + 1, updated_at = $now
			WHERE owner = $actor RETURN AFTER`,
		map[string]any{
			"rid": investigationRecordID(investigationID),
			"new_owner": newOwner, "actor": actor, "now": now,
		})
	if err != nil {
		return nil, fmt.Errorf("transfer investigation ownership: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		// The owner changed between the projection read and the CAS write; the
		// stale actor learns nothing beyond absence.
		return nil, ErrNotFound
	}
	if err := s.appendAuthzAudit(ctx, "investigation.transfer", investigationID+" -> "+newOwner, actor); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

func (s *Surreal) PutInvestigationCursor(ctx context.Context, principal, investigationID, cursor string) (*InvestigationCursor, error) {
	if err := validateDomainString("cursor", cursor, true); err != nil {
		return nil, fmt.Errorf("put investigation cursor: %w", err)
	}
	projection, err := s.readInvestigationProjection(ctx, principal, investigationID)
	if err != nil {
		return nil, ErrNotFound
	}
	now := storeTimestamp(time.Now())
	results, err := surrealdb.Query[[]InvestigationCursor](ctx, s.db,
		`UPSERT $rid SET cursor_key = $cursor_key, investigation_id = $investigation_id,
			principal = $principal, cursor = $cursor, authz_revision = $authz_revision,
			created_at = IF created_at = NONE THEN $now ELSE created_at END,
			updated_at = $now RETURN AFTER`,
		map[string]any{
			"rid":        investigationCursorRecordID(investigationID, principal),
			"cursor_key": hashIdentity("iac_", investigationID, principal),
			"investigation_id": investigationID, "principal": principal,
			"cursor": cursor, "authz_revision": projection.AuthzRevision, "now": now,
		})
	if err != nil {
		return nil, fmt.Errorf("put investigation cursor: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("put investigation cursor returned no row")
	}
	return &rows[0], nil
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
		"SELECT cursor_key, investigation_id, principal, cursor, authz_revision, created_at, updated_at FROM $rid LIMIT 1",
		map[string]any{"rid": investigationCursorRecordID(investigationID, principal)})
	if err != nil {
		return nil, fmt.Errorf("get investigation cursor: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].AuthzRevision != projection.AuthzRevision {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}
