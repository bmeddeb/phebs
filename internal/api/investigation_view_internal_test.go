package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func investigationFixtureSource(t *testing.T) InvestigationViewSource {
	t.Helper()
	source, err := NewInvestigationFixtureViews(filepath.Join("..", "..", "docs", "fixtures", "investigations"))
	if err != nil {
		t.Fatalf("load investigation fixture views: %v", err)
	}
	return source
}

func TestInvestigationFixtureViewsLoadFiveDistinctCanonicalStates(t *testing.T) {
	source := investigationFixtureSource(t)
	views, err := source.ListInvestigationViewsAs(context.Background(), "user:fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 5 {
		t.Fatalf("fixture summaries = %d, want 5", len(views))
	}
	states := make(map[string]bool, len(views))
	for _, summary := range views {
		states[summary.State] = true
		document, err := source.GetInvestigationViewAs(context.Background(), "user:fixture", summary.ID)
		if err != nil {
			t.Fatalf("get fixture %s: %v", summary.ID, err)
		}
		var envelope struct {
			Outcome string `json:"outcome"`
		}
		if err := json.Unmarshal(document.Envelope, &envelope); err != nil {
			t.Fatalf("decode fixture %s envelope: %v", summary.ID, err)
		}
		if envelope.Outcome != summary.Outcome {
			t.Fatalf("fixture %s outcome = %q, summary %q", summary.ID, envelope.Outcome, summary.Outcome)
		}
	}
	if len(states) != 5 {
		t.Fatalf("fixture states are not distinct: %+v", states)
	}
	if _, err := source.ListInvestigationViewsAs(context.Background(), ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("anonymous fixture list error = %v, want ErrNotFound", err)
	}
	if _, err := source.GetInvestigationViewAs(context.Background(), "user:fixture", "99"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown fixture error = %v, want ErrNotFound", err)
	}
}

func TestInvestigationViewAPIIsAuthenticatedCapabilityGatedAndDarkByDefault(t *testing.T) {
	source := investigationFixtureSource(t)
	store := &investigationRepoStore{}
	enabled := New(Options{
		Version: "test", Store: store, InvestigationViews: source,
		Principal: func(context.Context) string { return "user:fixture" },
	})

	version := httptest.NewRecorder()
	enabled.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if version.Code != http.StatusOK || !strings.Contains(version.Body.String(), investigationCoreViewsCapability) {
		t.Fatalf("enabled version = %d %s", version.Code, version.Body)
	}
	list := httptest.NewRecorder()
	enabled.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/investigation_views", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list views = %d %s", list.Code, list.Body)
	}
	var summaries []InvestigationViewSummary
	if err := json.Unmarshal(list.Body.Bytes(), &summaries); err != nil || len(summaries) != 5 {
		t.Fatalf("view summaries = %+v, %v", summaries, err)
	}
	get := httptest.NewRecorder()
	enabled.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/investigation_views/04", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"unresolved_attribution"`) ||
		!strings.Contains(get.Body.String(), `"ATTRIBUTION_UNRESOLVED"`) {
		t.Fatalf("get fixture view = %d %s", get.Code, get.Body)
	}

	anonymous := New(Options{
		Version: "test", Store: store, InvestigationViews: source,
		Principal: func(context.Context) string { return "" },
	})
	version = httptest.NewRecorder()
	anonymous.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if strings.Contains(version.Body.String(), investigationCoreViewsCapability) {
		t.Fatalf("anonymous version leaked Investigation capability: %s", version.Body)
	}
	list = httptest.NewRecorder()
	anonymous.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/investigation_views", nil))
	if list.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d %s, want 401", list.Code, list.Body)
	}

	dark := New(Options{
		Version: "test", Store: store,
		Principal: func(context.Context) string { return "user:fixture" },
	})
	for _, target := range []string{"/api/investigation_views", "/api/investigation_views/04"} {
		recorder := httptest.NewRecorder()
		dark.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("dark %s = %d %s", target, recorder.Code, recorder.Body)
		}
	}
}
