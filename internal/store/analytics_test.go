package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestUsageEventsRecordListPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	for i, event := range []store.UsageEvent{
		{Kind: "search", ActorID: "u1", Repos: []string{"github.com/foo/bar"},
			MatchCount: 3, FileCount: 2, DurationMS: 12, CreatedAt: base},
		{Kind: "search", APIKeyID: "k1", Repos: []string{"github.com/foo/bar", "github.com/baz/qux"},
			MatchCount: 1, FileCount: 1, DurationMS: 4, CreatedAt: base.Add(time.Hour)},
		{Kind: "search", ActorID: "u1", CreatedAt: base.Add(2 * time.Hour)}, // no matches
	} {
		if err := s.RecordUsageEvent(ctx, event); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got, err := s.ListUsageEvents(ctx, base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	// oldest first, fields round-trip
	if got[0].MatchCount != 3 || got[0].Repos[0] != "github.com/foo/bar" || got[0].ActorID != "u1" {
		t.Errorf("event[0] = %+v", got[0])
	}
	if len(got[1].Repos) != 2 || got[1].APIKeyID != "k1" {
		t.Errorf("event[1] = %+v", got[1])
	}
	if len(got[2].Repos) != 0 {
		t.Errorf("event[2].Repos = %v, want empty", got[2].Repos)
	}

	// window excludes events at or before since
	windowed, err := s.ListUsageEvents(ctx, base)
	if err != nil {
		t.Fatalf("list windowed: %v", err)
	}
	if len(windowed) != 2 {
		t.Fatalf("windowed = %d events, want 2", len(windowed))
	}

	n, err := s.PruneUsageEvents(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Errorf("pruned %d, want 2", n)
	}
	got, err = s.ListUsageEvents(ctx, base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("list after prune: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after prune = %d events, want 1", len(got))
	}
}
