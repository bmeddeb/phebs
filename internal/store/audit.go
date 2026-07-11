package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

var _ AuditStore = (*Surreal)(nil)

func auditEventID(id string) models.RecordID { return models.NewRecordID("audit_event", id) }

type auditEventRec struct {
	RecID      *models.RecordID `json:"id"`
	Action     string           `json:"action"`
	Target     string           `json:"target"`
	ActorID    string           `json:"actor_id"`
	ActorEmail string           `json:"actor_email"`
	APIKeyID   string           `json:"api_key_id"`
	AuthMethod string           `json:"auth_method"`
	SourceIP   string           `json:"source_ip"`
	Status     int              `json:"status"`
	CreatedAt  time.Time        `json:"created_at"`
}

func (r auditEventRec) event() AuditEvent {
	id := ""
	if r.RecID != nil {
		id = r.RecID.ID.(string)
	}
	return AuditEvent{
		ID: id, Action: r.Action, Target: r.Target,
		ActorID: r.ActorID, ActorEmail: r.ActorEmail,
		APIKeyID: r.APIKeyID, AuthMethod: r.AuthMethod,
		SourceIP: r.SourceIP, Status: r.Status, CreatedAt: r.CreatedAt,
	}
}

func (s *Surreal) AppendAuditEvent(ctx context.Context, event AuditEvent) error {
	if event.Action == "" {
		return errors.New("audit event needs an action")
	}
	id := event.ID
	if id == "" {
		var err error
		if id, err = authRandomID(); err != nil {
			return err
		}
	}
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	results, err := surrealdb.Query[[]auditEventRec](ctx, s.db,
		`CREATE $rid SET action = $action, target = $target, actor_id = $actor_id,
            actor_email = $actor_email, api_key_id = $api_key_id,
            auth_method = $auth_method, source_ip = $source_ip,
            status = $status, created_at = $created_at RETURN AFTER`,
		map[string]any{
			"rid": auditEventID(id), "action": event.Action, "target": event.Target,
			"actor_id": event.ActorID, "actor_email": event.ActorEmail,
			"api_key_id": event.APIKeyID, "auth_method": event.AuthMethod,
			"source_ip": event.SourceIP, "status": event.Status, "created_at": createdAt,
		})
	if err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			return nil
		}
	}
	return errors.New("append audit event returned no row")
}

func (s *Surreal) ListAuditEvents(ctx context.Context, offset, limit int) ([]AuditEvent, error) {
	if offset < 0 || limit <= 0 {
		return nil, errors.New("audit listing needs offset >= 0 and limit > 0")
	}
	results, err := surrealdb.Query[[]auditEventRec](ctx, s.db,
		"SELECT * FROM audit_event ORDER BY created_at DESC LIMIT $limit START $offset",
		map[string]any{"limit": limit, "offset": offset})
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	var events []AuditEvent
	for _, result := range *results {
		for _, row := range result.Result {
			events = append(events, row.event())
		}
	}
	return events, nil
}

func (s *Surreal) PruneAuditEvents(ctx context.Context, cutoff time.Time) (int, error) {
	results, err := surrealdb.Query[[]auditEventRec](ctx, s.db,
		"DELETE audit_event WHERE created_at <= $cutoff RETURN BEFORE",
		map[string]any{"cutoff": cutoff})
	if err != nil {
		return 0, fmt.Errorf("prune audit events: %w", err)
	}
	n := 0
	for _, result := range *results {
		n += len(result.Result)
	}
	return n, nil
}
