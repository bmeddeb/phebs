package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestEvidenceUnindexedRepositorySkipsBothPublicationReads(t *testing.T) {
	const repository = "evaluation.local/discovered"
	st := &evidenceViewStore{
		repos:        []store.Repo{{Name: repository}, {Name: "hidden.example/private"}},
		latestErrors: map[string]error{repository: errors.New("unindexed scope must not reach storage")},
		// Retained old state cannot become authority for an unindexed source.
		runs: map[string]store.ExtractionRun{repository: publishedEvidenceRun(repository, "retained-old-run", 99)},
	}
	lists := 0
	st.onListRepos = func() { lists++ }
	handler := api.New(api.Options{Version: "test", APIKey: "discovery-test-key", Store: st, Evidence: st,
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repo store.Repo) bool { return repo.Name == repository }
		},
	})
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
	if anonymous.Code != http.StatusUnauthorized || lists != 0 || len(st.calls) != 0 {
		t.Fatalf("anonymous request read repository state: status=%d lists=%d reads=%v", anonymous.Code, lists, st.calls)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil)
	request.Header.Set("Authorization", "Bearer discovery-test-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unindexed raw evidence=%d %s", response.Code, response.Body)
	}
	var view api.EvidenceView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if lists != 2 || len(st.calls) != 0 || view.VisibleRepositoryCount != 1 || view.PublishedRunCount != 0 || view.PartitionedRootCount != 0 || view.AssertionCount != 0 || len(view.Repositories) != 0 || view.Repositories == nil {
		t.Fatalf("unindexed raw evidence claimed publication: %+v lists=%d reads=%v", view, lists, st.calls)
	}
	if strings.Contains(response.Body.String(), "retained-old-run") || strings.Contains(response.Body.String(), "hidden.example") {
		t.Fatal("unindexed/hidden retained state entered response")
	}
}

func TestEvidenceUnindexedFinalSourceAndPermissionFences(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*evidenceViewStore, *bool)
	}{
		{"index published", func(st *evidenceViewStore, _ *bool) { st.repos[0].IndexedCommitHash = strings.Repeat("a", 40) }},
		{"permission removed", func(_ *evidenceViewStore, allowed *bool) { *allowed = false }},
		{"publication revision changed", func(st *evidenceViewStore, _ *bool) { st.repos[0].EvidenceRevision++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := &evidenceViewStore{repos: []store.Repo{{Name: "evaluation.local/discovered"}}}
			allowed, lists := true, 0
			st.onListRepos = func() {
				lists++
				if lists == 2 {
					test.change(st, &allowed)
				}
			}
			handler := api.New(api.Options{Version: "test", Store: st, Evidence: st,
				Visible: func(context.Context) func(store.Repo) bool { return func(store.Repo) bool { return allowed } },
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
			if response.Code != http.StatusConflict || len(st.calls) != 0 {
				t.Fatalf("source transition accepted: status=%d body=%s reads=%v", response.Code, response.Body, st.calls)
			}
		})
	}
}

func TestEvidenceNonemptyCommitFailureIsNotUnpublished(t *testing.T) {
	for _, commit := range []string{" ", strings.Repeat("a", 40)} {
		st := &evidenceViewStore{repos: []store.Repo{{Name: "evaluation.local/discovered", IndexedCommitHash: commit}}, latestErrors: map[string]error{"evaluation.local/discovered": errors.New("injected strict-scope or storage failure")}}
		response := httptest.NewRecorder()
		api.New(api.Options{Version: "test", Store: st, Evidence: st}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
		if response.Code != http.StatusInternalServerError || len(st.calls) != 1 {
			t.Fatalf("nonempty failure became unpublished: status=%d body=%s reads=%v", response.Code, response.Body, st.calls)
		}
	}
}
