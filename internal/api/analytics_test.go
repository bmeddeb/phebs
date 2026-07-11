package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

type fakeAnalytics struct {
	events []store.UsageEvent
	since  time.Time
}

func (f *fakeAnalytics) RecordUsageEvent(context.Context, store.UsageEvent) error { return nil }
func (f *fakeAnalytics) PruneUsageEvents(context.Context, time.Time) (int, error) { return 0, nil }
func (f *fakeAnalytics) ListUsageEvents(_ context.Context, since time.Time) ([]store.UsageEvent, error) {
	f.since = since
	var out []store.UsageEvent
	for _, e := range f.events {
		if e.CreatedAt.After(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func TestAnalyticsEndpoint(t *testing.T) {
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)
	analytics := &fakeAnalytics{events: []store.UsageEvent{
		{Kind: "search", Repos: []string{"github.com/foo/bar"}, DurationMS: 10, CreatedAt: now},
		{Kind: "search", Repos: []string{"github.com/foo/bar", "github.com/baz/qux"}, DurationMS: 30, CreatedAt: now},
		{Kind: "search", Repos: []string{"github.com/baz/qux"}, DurationMS: 20, CreatedAt: yesterday},
		{Kind: "search", CreatedAt: now.AddDate(0, 0, -40)}, // outside a 30d window
	}}

	t.Run("requires administrator", func(t *testing.T) {
		h := api.New(api.Options{Version: "t", Store: &fakeStore{}, Analytics: analytics,
			IsAdmin: func(context.Context) bool { return false }})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/analytics", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin analytics = %d, want 403", rec.Code)
		}
	})

	t.Run("nil analytics answers 503", func(t *testing.T) {
		h := api.New(api.Options{Version: "t", Store: &fakeStore{}})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/analytics", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("analytics without store = %d, want 503", rec.Code)
		}
	})

	t.Run("aggregates window", func(t *testing.T) {
		h := api.New(api.Options{Version: "t", Store: &fakeStore{}, Analytics: analytics})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/analytics?days=30", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("analytics = %d (%s)", rec.Code, rec.Body)
		}
		var out struct {
			TotalSearches int   `json:"total_searches"`
			AvgDurationMS int64 `json:"avg_duration_ms"`
			Daily         []struct {
				Date  string `json:"date"`
				Count int    `json:"count"`
			} `json:"daily"`
			TopRepos []struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			} `json:"top_repos"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.TotalSearches != 3 {
			t.Errorf("total_searches = %d, want 3 (40d-old event excluded)", out.TotalSearches)
		}
		if out.AvgDurationMS != 20 {
			t.Errorf("avg_duration_ms = %d, want 20", out.AvgDurationMS)
		}
		if len(out.Daily) != 30 {
			t.Fatalf("daily buckets = %d, want 30 (zero-filled)", len(out.Daily))
		}
		last := out.Daily[len(out.Daily)-1]
		if last.Date != now.Format(time.DateOnly) || last.Count != 2 {
			t.Errorf("today bucket = %+v, want %s count 2", last, now.Format(time.DateOnly))
		}
		if len(out.TopRepos) != 2 || out.TopRepos[0].Count != 2 {
			t.Fatalf("top_repos = %+v, want two repos with top count 2", out.TopRepos)
		}
		// count tie broken by name: both repos appear in 2 events each? foo/bar
		// appears twice, baz/qux twice → tie → baz/qux sorts first by name.
		if out.TopRepos[0].Name != "github.com/baz/qux" {
			t.Errorf("tie-break order = %+v", out.TopRepos)
		}
	})
}
