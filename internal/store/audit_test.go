package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestAuditEventsAppendListPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	events := []store.AuditEvent{
		{Action: "auth.login", ActorID: "u1", ActorEmail: "a@example.com",
			AuthMethod: "session", SourceIP: "10.0.0.1", Status: 200, CreatedAt: base},
		{Action: "auth.key.create", Target: "k1", ActorID: "u1",
			ActorEmail: "a@example.com", AuthMethod: "session", Status: 201,
			CreatedAt: base.Add(time.Minute)},
		{Action: "post-api-reindex", Target: "github.com/foo/bar", ActorID: "u2",
			ActorEmail: "b@example.com", AuthMethod: "api_key", APIKeyID: "key2",
			Status: 200, CreatedAt: base.Add(2 * time.Minute)},
	}
	for _, event := range events {
		if err := s.AppendAuditEvent(ctx, event); err != nil {
			t.Fatalf("append %s: %v", event.Action, err)
		}
	}

	got, err := s.ListAuditEvents(ctx, 0, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	// newest first
	wantActions := []string{"post-api-reindex", "auth.key.create", "auth.login"}
	for i, want := range wantActions {
		if got[i].Action != want {
			t.Errorf("event[%d].Action = %q, want %q", i, got[i].Action, want)
		}
		if got[i].ID == "" {
			t.Errorf("event[%d] has empty ID", i)
		}
	}
	if got[0].Target != "github.com/foo/bar" || got[0].APIKeyID != "key2" {
		t.Errorf("newest event fields not round-tripped: %+v", got[0])
	}

	// offset pagination
	page, err := s.ListAuditEvents(ctx, 2, 10)
	if err != nil {
		t.Fatalf("list offset: %v", err)
	}
	if len(page) != 1 || page[0].Action != "auth.login" {
		t.Fatalf("offset page = %+v, want the oldest login event", page)
	}

	// prune everything at or before base+1m; only the newest survives
	n, err := s.PruneAuditEvents(ctx, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Errorf("pruned %d, want 2", n)
	}
	got, err = s.ListAuditEvents(ctx, 0, 10)
	if err != nil {
		t.Fatalf("list after prune: %v", err)
	}
	if len(got) != 1 || got[0].Action != "post-api-reindex" {
		t.Fatalf("after prune = %+v, want only post-api-reindex", got)
	}
}

func TestAuditEventValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.AppendAuditEvent(ctx, store.AuditEvent{}); err == nil {
		t.Error("append without action should fail")
	}
	if _, err := s.ListAuditEvents(ctx, -1, 10); err == nil {
		t.Error("negative offset should fail")
	}
	if _, err := s.ListAuditEvents(ctx, 0, 0); err == nil {
		t.Error("zero limit should fail")
	}
}
