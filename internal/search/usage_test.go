package search_test

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
)

// T10.2: both Search and Stream emit one usage event per completed search.
func TestUsageEventPerSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := corpus(t, ctx)

	var events []store.UsageEvent
	s.Usage = func(_ context.Context, e store.UsageEvent) { events = append(events, e) }

	res, err := s.Search(ctx, "phebsNeedle", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("Search recorded %d events, want 1", len(events))
	}
	e := events[0]
	if e.Kind != "search" || e.MatchCount != res.Stats.MatchCount || e.FileCount != res.Stats.FileCount {
		t.Errorf("event = %+v, want stats matching result %+v", e, res.Stats)
	}
	if len(e.Repos) == 0 {
		t.Error("event carries no matched repos")
	}
	seen := map[string]bool{}
	for _, name := range e.Repos {
		if seen[name] {
			t.Errorf("repo %q recorded twice", name)
		}
		seen[name] = true
	}

	events = nil
	if _, err := s.Stream(ctx, "phebsNeedle", search.Options{}, func(*search.Result) {}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("Stream recorded %d events, want 1", len(events))
	}
	if events[0].Kind != "search" || len(events[0].Repos) == 0 {
		t.Errorf("stream event = %+v", events[0])
	}

	// a compile error records nothing
	events = nil
	if _, err := s.Search(ctx, "context:unknown-name phebsNeedle", search.Options{}); err == nil {
		t.Fatal("unknown context should error")
	}
	if len(events) != 0 {
		t.Fatalf("failed search recorded %d events, want 0", len(events))
	}
}
