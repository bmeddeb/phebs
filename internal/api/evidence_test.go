package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

type evidenceViewStore struct {
	store.Store
	store.EvidenceStore
	repos           []store.Repo
	runs            map[string]store.ExtractionRun
	assertions      map[string][]store.Assertion
	latestErrors    map[string]error
	assertionErrors map[string]error
	calls           []string
}

func (s *evidenceViewStore) ListRepos(context.Context) ([]store.Repo, error) {
	return append([]store.Repo(nil), s.repos...), nil
}

func (s *evidenceViewStore) LatestPublishedRun(
	_ context.Context, repo, domain string,
) (*store.ExtractionRun, error) {
	s.calls = append(s.calls, "latest:"+repo+":"+domain)
	if err := s.latestErrors[repo]; err != nil {
		return nil, err
	}
	run, ok := s.runs[repo]
	if !ok {
		return nil, store.ErrNotFound
	}
	copy := run
	return &copy, nil
}

func (s *evidenceViewStore) ListAssertions(
	_ context.Context, query store.AssertionQuery,
) ([]store.Assertion, error) {
	s.calls = append(s.calls, "assertions:"+query.Repo+":"+query.RunID)
	if err := s.assertionErrors[query.Repo]; err != nil {
		return nil, err
	}
	run, ok := s.runs[query.Repo]
	if !ok || query.RunID != run.ID {
		return nil, errors.New("test received an unscoped assertion query")
	}
	return append([]store.Assertion(nil), s.assertions[query.Repo]...), nil
}

func publishedEvidenceRun(repo, id string, assertions int) store.ExtractionRun {
	published := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	return store.ExtractionRun{
		ID: id, Repo: repo, Commit: "commit-" + id, Domain: "proto-contract",
		Extractor: "proto", Status: "published", PublishedAt: &published,
		Coverage: store.CoverageManifest{
			Protocols:      []string{"z-protocol", "a-protocol"},
			Failures:       []string{"z-failure", "a-failure"},
			AssertionCount: assertions, AtomCount: assertions,
		},
	}
}

func TestEvidenceViewFiltersBeforeStorageAndAggregatesVisibleRowsOnly(t *testing.T) {
	const (
		visibleA = "github.com/allowed/a"
		visibleB = "github.com/allowed/b"
		noRun    = "github.com/allowed/no-run"
		hidden   = "github.com/secret/hidden-service-name"
		deleting = "github.com/allowed/deleting-secret-name"
	)
	st := &evidenceViewStore{
		repos: []store.Repo{
			{Name: hidden}, {Name: visibleB}, {Name: deleting, Deleting: true},
			{Name: noRun}, {Name: visibleA},
		},
		runs: map[string]store.ExtractionRun{
			visibleA: publishedEvidenceRun(visibleA, "run-a", 2),
			visibleB: publishedEvidenceRun(visibleB, "run-b", 1),
			hidden:   publishedEvidenceRun(hidden, "run-hidden", 999),
			deleting: publishedEvidenceRun(deleting, "run-deleting", 777),
		},
		assertions: map[string][]store.Assertion{
			visibleA: {
				{ID: "z", Predicate: "DECLARES_OPERATION", Subject: "a.proto", Object: "visible.Service/Z", Repo: visibleA, RunID: "run-a", Supporting: []string{"ea_z", "ea_a"}},
				{ID: "a", Predicate: "DECLARES_OPERATION", Subject: "a.proto", Object: "visible.Service/A", Repo: visibleA, RunID: "run-a", Supporting: []string{"ea_b"}},
			},
			visibleB: {{ID: "b", Predicate: "DECLARES_FIELD", Subject: "b.proto", Object: "visible.Message#1", Repo: visibleB, RunID: "run-b"}},
			hidden:   {{ID: "hidden", Object: "secret.HiddenService/Call", Repo: hidden, RunID: "run-hidden"}},
			deleting: {{ID: "deleting", Object: "secret.DeletingService/Call", Repo: deleting, RunID: "run-deleting"}},
		},
	}
	handler := api.New(api.Options{
		Version: "test", Store: st, Evidence: st,
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repo store.Repo) bool { return strings.HasPrefix(repo.Name, "github.com/allowed/") }
		},
	})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET evidence = %d %s", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), "hidden-service-name") ||
		strings.Contains(recorder.Body.String(), "HiddenService") ||
		strings.Contains(recorder.Body.String(), "deleting-secret-name") ||
		strings.Contains(recorder.Body.String(), "DeletingService") ||
		strings.Contains(recorder.Body.String(), "999") || strings.Contains(recorder.Body.String(), "777") {
		t.Fatalf("evidence view leaked hidden/deleting data or counts: %s", recorder.Body)
	}
	for _, call := range st.calls {
		if strings.Contains(call, hidden) || strings.Contains(call, deleting) {
			t.Fatalf("evidence storage queried a filtered repository: %v", st.calls)
		}
	}
	wantCalls := []string{
		"latest:" + visibleA + ":proto-contract", "assertions:" + visibleA + ":run-a",
		"latest:" + visibleB + ":proto-contract", "assertions:" + visibleB + ":run-b",
		"latest:" + noRun + ":proto-contract",
	}
	if !slices.Equal(st.calls, wantCalls) {
		t.Fatalf("evidence call order/scopes = %v, want %v", st.calls, wantCalls)
	}

	var view api.EvidenceView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.VisibleRepositoryCount != 3 || view.PublishedRunCount != 2 || view.AssertionCount != 3 {
		t.Fatalf("visible counts = %+v", view)
	}
	if len(view.Repositories) != 2 || view.Repositories[0].Repository != visibleA || view.Repositories[1].Repository != visibleB {
		t.Fatalf("repository order = %+v", view.Repositories)
	}
	if got := []string{view.Repositories[0].Assertions[0].ID, view.Repositories[0].Assertions[1].ID}; !slices.Equal(got, []string{"a", "z"}) {
		t.Fatalf("assertion order = %v", got)
	}
	if got := view.Repositories[0].Assertions[1].Supporting; !slices.Equal(got, []string{"ea_a", "ea_z"}) {
		t.Fatalf("support order = %v", got)
	}
	if got := view.Repositories[0].Run.Coverage.Protocols; !slices.Equal(got, []string{"a-protocol", "z-protocol"}) {
		t.Fatalf("protocol order = %v", got)
	}
}

func TestEvidenceViewZeroAccessMakesNoEvidenceCalls(t *testing.T) {
	const hidden = "github.com/secret/zero-access-service"
	st := &evidenceViewStore{
		repos:      []store.Repo{{Name: hidden}},
		runs:       map[string]store.ExtractionRun{hidden: publishedEvidenceRun(hidden, "hidden-run", 42)},
		assertions: map[string][]store.Assertion{hidden: {{ID: "hidden", Object: "secret.Service/Call"}}},
	}
	handler := api.New(api.Options{
		Version: "test", Store: st, Evidence: st,
		Visible: func(context.Context) func(store.Repo) bool {
			return func(store.Repo) bool { return false }
		},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET zero-access evidence = %d %s", recorder.Code, recorder.Body)
	}
	if len(st.calls) != 0 {
		t.Fatalf("zero-access request touched evidence storage: %v", st.calls)
	}
	if strings.Contains(recorder.Body.String(), "zero-access-service") || strings.Contains(recorder.Body.String(), "42") {
		t.Fatalf("zero-access response leaked corpus shape: %s", recorder.Body)
	}
	var view api.EvidenceView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.VisibleRepositoryCount != 0 || view.PublishedRunCount != 0 || view.AssertionCount != 0 || len(view.Repositories) != 0 {
		t.Fatalf("zero-access view = %+v", view)
	}
}

func TestEvidenceViewFailsWholeAndDarkDefaultHasNoRoute(t *testing.T) {
	t.Run("dark default", func(t *testing.T) {
		st := &evidenceViewStore{}
		handler := api.New(api.Options{Version: "test", Store: st})
		for _, target := range []string{"/api/evidence?domain=proto-contract", "/api/openapi.json"} {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
			if target != "/api/openapi.json" && recorder.Code != http.StatusNotFound {
				t.Fatalf("dark evidence route = %d %s", recorder.Code, recorder.Body)
			}
			if strings.Contains(recorder.Body.String(), `"/api/evidence"`) {
				t.Fatalf("dark OpenAPI advertised evidence: %s", recorder.Body)
			}
		}
	})

	t.Run("read error returns no partial rows", func(t *testing.T) {
		const first, second = "github.com/visible/a", "github.com/visible/z"
		st := &evidenceViewStore{
			repos: []store.Repo{{Name: second}, {Name: first}},
			runs: map[string]store.ExtractionRun{
				first: publishedEvidenceRun(first, "run-first", 1),
			},
			assertions: map[string][]store.Assertion{
				first: {{ID: "first", Object: "visible-partial-service", Repo: first, RunID: "run-first"}},
			},
			latestErrors: map[string]error{second: errors.New("injected evidence read failure")},
		}
		handler := api.New(api.Options{Version: "test", Store: st, Evidence: st})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("failed evidence view = %d %s", recorder.Code, recorder.Body)
		}
		if strings.Contains(recorder.Body.String(), "visible-partial-service") || strings.Contains(recorder.Body.String(), "run-first") {
			t.Fatalf("failed evidence view returned partial rows: %s", recorder.Body)
		}
	})
}
