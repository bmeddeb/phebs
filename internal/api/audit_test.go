package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestAuditMiddlewareRecordsMutations(t *testing.T) {
	var events []store.AuditEvent
	h := api.New(api.Options{
		Version: "t", Store: &fakeStore{},
		AuditRecord: func(_ context.Context, e store.AuditEvent) { events = append(events, e) },
	})

	// GET requests are never audited
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(events) != 0 {
		t.Fatalf("GET recorded %d audit events, want 0", len(events))
	}

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantTarget string
	}{
		{"successful reindex", `{"repo":"github.com/foo/bar"}`, 200, "github.com/foo/bar"},
		{"unknown repo still audited", `{"repo":"github.com/no/pe"}`, 404, "github.com/no/pe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events = nil
			req := httptest.NewRequest(http.MethodPost, "/api/reindex", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if len(events) != 1 {
				t.Fatalf("recorded %d events, want 1", len(events))
			}
			e := events[0]
			if e.Action != "post-api-reindex" || e.Target != tt.wantTarget || e.Status != tt.wantStatus {
				t.Errorf("event = %+v, want action post-api-reindex target %q status %d", e, tt.wantTarget, tt.wantStatus)
			}
			if e.SourceIP == "" {
				t.Error("event has no source IP")
			}
		})
	}
}

// fakeAuditLog serves ListAuditEvents from a fixed slice.
type fakeAuditLog struct {
	events []store.AuditEvent
}

func (f *fakeAuditLog) AppendAuditEvent(context.Context, store.AuditEvent) error { return nil }
func (f *fakeAuditLog) PruneAuditEvents(context.Context, time.Time) (int, error) {
	return 0, nil
}
func (f *fakeAuditLog) ListAuditEvents(_ context.Context, offset, limit int) ([]store.AuditEvent, error) {
	if offset >= len(f.events) {
		return nil, nil
	}
	end := min(offset+limit, len(f.events))
	return f.events[offset:end], nil
}

func TestAuditEndpoint(t *testing.T) {
	log := &fakeAuditLog{}
	for range 3 {
		log.events = append(log.events, store.AuditEvent{Action: "auth.login", Status: 200})
	}

	t.Run("requires administrator", func(t *testing.T) {
		h := api.New(api.Options{Version: "t", Store: &fakeStore{}, AuditLog: log,
			IsAdmin: func(context.Context) bool { return false }})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin audit read = %d, want 403", rec.Code)
		}
	})

	t.Run("nil audit log answers 503", func(t *testing.T) {
		h := api.New(api.Options{Version: "t", Store: &fakeStore{}})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit read without store = %d, want 503", rec.Code)
		}
	})

	t.Run("pages with has_more", func(t *testing.T) {
		h := api.New(api.Options{Version: "t", Store: &fakeStore{}, AuditLog: log})
		var out struct {
			Events  []store.AuditEvent `json:"events"`
			HasMore bool               `json:"has_more"`
		}
		get := func(path string) {
			t.Helper()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d (%s)", path, rec.Code, rec.Body)
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
		get("/api/audit?limit=2")
		if len(out.Events) != 2 || !out.HasMore {
			t.Fatalf("page 1 = %d events has_more=%v, want 2/true", len(out.Events), out.HasMore)
		}
		get("/api/audit?offset=2&limit=2")
		if len(out.Events) != 1 || out.HasMore {
			t.Fatalf("page 2 = %d events has_more=%v, want 1/false", len(out.Events), out.HasMore)
		}
	})
}
