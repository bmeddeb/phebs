package api

import (
	"context"
	"net"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
)

// T10.1: every mutating huma operation is recorded through Options.AuditRecord.
// Handlers with an action-specific target annotate it via setAuditTarget; the
// event's actor is resolved by the closure serve injects (the api package
// stays free of an internal/auth import, like Options.IsAdmin).

type auditTarget struct{ value string }
type auditTargetKey struct{}

// setAuditTarget annotates the in-flight mutating request's audit event.
// It is a no-op outside the audit middleware (tests, GET handlers).
func setAuditTarget(ctx context.Context, target string) {
	if t, ok := ctx.Value(auditTargetKey{}).(*auditTarget); ok {
		t.value = target
	}
}

func registerAuditMiddleware(a huma.API, opts Options) {
	if opts.AuditRecord == nil {
		return
	}
	a.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		switch op.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next(ctx)
			return
		}
		target := &auditTarget{}
		ctx = huma.WithValue(ctx, auditTargetKey{}, target)
		next(ctx)
		opts.AuditRecord(ctx.Context(), store.AuditEvent{
			Action:   op.OperationID,
			Target:   target.value,
			SourceIP: remoteHost(ctx.RemoteAddr()),
			Status:   ctx.Status(),
		})
	})
}

func registerAudit(a huma.API, opts Options) {
	type auditIn struct {
		Offset int `query:"offset" minimum:"0"`
		Limit  int `query:"limit" minimum:"0" maximum:"200" doc:"events per page, default 50"`
	}
	type auditOut struct {
		Body struct {
			Events  []store.AuditEvent `json:"events"`
			HasMore bool               `json:"has_more"`
		}
	}
	huma.Get(a, "/api/audit", func(ctx context.Context, in *auditIn) (*auditOut, error) {
		if opts.IsAdmin != nil && !opts.IsAdmin(ctx) {
			return nil, huma.Error403Forbidden("administrator access required")
		}
		if opts.AuditLog == nil {
			return nil, huma.Error503ServiceUnavailable("audit log unavailable")
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		events, err := opts.AuditLog.ListAuditEvents(ctx, in.Offset, limit+1)
		if err != nil {
			return nil, huma.Error500InternalServerError("list audit events", err)
		}
		out := &auditOut{}
		out.Body.HasMore = len(events) > limit
		if out.Body.HasMore {
			events = events[:limit]
		}
		if events == nil {
			events = []store.AuditEvent{}
		}
		out.Body.Events = events
		return out, nil
	})
}

func remoteHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		return host
	}
	return addr
}
