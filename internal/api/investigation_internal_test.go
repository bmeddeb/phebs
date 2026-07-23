package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func testInvestigationPlan() InvestigationPlan {
	return InvestigationPlan{
		Referent:           "grpc:/example.Payment/Authorize",
		ClaimFamily:        "consumer-census",
		Title:              "Payment migration",
		NormalizedQuestion: "which consumers call Authorize?",
		DecisionSought:     "approve migration inventory",
		Repositories:       []string{"github.com/example/visible"},
		SnapshotPolicy:     "exact indexed commits",
		BuildConfiguration: "linux/amd64",
		PackVersions:       []string{"grpc-go@1"},
		EnumerationMethod:  "authorized repository list",
	}
}

func TestBuildInvestigationScopePreviewIsDeterministicAndPermissionFirst(t *testing.T) {
	visible := []store.Repo{
		{Name: "github.com/example/z", IndexedCommitHash: "zzzz"},
		{Name: "github.com/example/visible", IndexedCommitHash: "aaaa"},
	}
	plan := testInvestigationPlan()
	first, err := BuildInvestigationScopePreview(visible, plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildInvestigationScopePreview([]store.Repo{visible[1], visible[0]}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Ready || first.PreviewDigest == "" || first.PreviewDigest != second.PreviewDigest ||
		len(first.Repositories) != 1 || first.Repositories[0].Commit != "aaaa" {
		t.Fatalf("deterministic preview = %+v / %+v", first, second)
	}

	for name, requested := range map[string]string{
		"hidden":  "github.com/example/hidden",
		"missing": "github.com/example/missing",
	} {
		t.Run(name, func(t *testing.T) {
			blockedPlan := plan
			blockedPlan.Repositories = []string{requested}
			blocked, err := BuildInvestigationScopePreview(visible, blockedPlan)
			if err != nil {
				t.Fatal(err)
			}
			if blocked.Ready || blocked.PreviewDigest != "" || len(blocked.Repositories) != 0 ||
				len(blocked.Blockers) != 1 ||
				blocked.Blockers[0].Code != "SCOPE_REPOSITORY_NOT_AVAILABLE" {
				t.Fatalf("blocked preview = %+v", blocked)
			}
		})
	}
}

type investigationRepoStore struct {
	store.Store
	repos []store.Repo
}

func (s *investigationRepoStore) ListRepos(context.Context) ([]store.Repo, error) {
	return append([]store.Repo(nil), s.repos...), nil
}

type investigationWorkflowFake struct {
	store.InvestigationWorkflowStore
	created  []store.GuidedInvestigationRequest
	run      store.Run
	events   []store.RunEvent
	artifact *store.RunArtifact
	canceled []string
}

func (s *investigationWorkflowFake) CreateGuidedInvestigation(_ context.Context, request store.GuidedInvestigationRequest) (*store.GuidedInvestigationCreation, error) {
	s.created = append(s.created, request)
	return &store.GuidedInvestigationCreation{
		Investigation: store.Investigation{ID: "01JYZZZZZZZZZZZZZZZZZZZZZZ", Owner: request.Principal, Lifecycle: "active"},
		Revision:      store.Revision{ID: "ivr_test"},
		Run:           store.Run{ID: "iru_test", State: store.RunQueued},
	}, nil
}

func (s *investigationWorkflowFake) GetRunAs(_ context.Context, principal, id string) (*store.Run, error) {
	if principal != "user:owner" || id != s.run.ID {
		return nil, store.ErrNotFound
	}
	out := s.run
	return &out, nil
}

func (s *investigationWorkflowFake) ListRunEventsAs(_ context.Context, principal, id string) ([]store.RunEvent, error) {
	if principal != "user:owner" || id != s.run.ID {
		return nil, store.ErrNotFound
	}
	return append([]store.RunEvent(nil), s.events...), nil
}

func (s *investigationWorkflowFake) GetRunArtifactForRunAs(_ context.Context, principal, id string) (*store.RunArtifact, error) {
	if principal != "user:owner" || id != s.run.ID {
		return nil, store.ErrNotFound
	}
	if s.artifact == nil {
		return nil, store.ErrNotFound
	}
	out := *s.artifact
	return &out, nil
}

func (s *investigationWorkflowFake) CancelInvestigationRunAs(_ context.Context, principal, id, reason string) (*store.RunEvent, error) {
	if principal != "user:owner" || id != s.run.ID {
		return nil, store.ErrNotFound
	}
	s.canceled = append(s.canceled, reason)
	return &store.RunEvent{RunID: id, NewState: store.RunCanceled, Reason: reason}, nil
}

func postInvestigationJSON(t *testing.T, handler http.Handler, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestInvestigationCreationAPIRepreflightsAndStaysDarkWithoutStore(t *testing.T) {
	repos := &investigationRepoStore{repos: []store.Repo{
		{Name: "github.com/example/visible", IndexedCommitHash: "aaaa"},
		{Name: "github.com/example/hidden", IndexedCommitHash: "bbbb"},
	}}
	workflow := &investigationWorkflowFake{
		run:    store.Run{ID: "iru_test", State: store.RunQueued},
		events: []store.RunEvent{{RunID: "iru_test", NewState: store.RunQueued}},
	}
	coverage, err := store.CanonicalInvestigationCoverageLedger(store.InvestigationCoverageLedger{
		EligibleUnits: 2,
		Analyzed:      1,
		Partial:       1,
		Failures: []store.InvestigationCoverageFailure{
			{Unit: "repo:github.com/example/visible@aaaa", Code: "EXTRACTOR_PARTIAL"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workflow.artifact = &store.RunArtifact{
		ID: "ira_test", RunID: workflow.run.ID, CoverageLedger: coverage,
	}
	handler := New(Options{
		Version: "test", Store: repos, Investigations: workflow,
		Principal: func(context.Context) string { return "user:owner" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repo store.Repo) bool { return !strings.Contains(repo.Name, "hidden") }
		},
	})
	plan := testInvestigationPlan()
	previewRecorder := postInvestigationJSON(t, handler, "/api/investigation_previews", plan)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", previewRecorder.Code, previewRecorder.Body)
	}
	var preview InvestigationScopePreview
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Ready || preview.PreviewDigest == "" || len(preview.Repositories) != 1 {
		t.Fatalf("preview = %+v", preview)
	}

	createBody := map[string]any{
		"plan": plan, "preview_digest": preview.PreviewDigest,
		"idempotency_key": "api-request-1",
	}
	createRecorder := postInvestigationJSON(t, handler, "/api/investigations", createBody)
	if createRecorder.Code != http.StatusAccepted {
		t.Fatalf("create = %d %s", createRecorder.Code, createRecorder.Body)
	}
	if len(workflow.created) != 1 || workflow.created[0].Principal != "user:owner" ||
		len(workflow.created[0].ResolvedInputIdentities) != 1 ||
		strings.Contains(workflow.created[0].RequestedSnapshot, "hidden") {
		t.Fatalf("stored creation request = %+v", workflow.created)
	}

	createBody["preview_digest"] = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	stale := postInvestigationJSON(t, handler, "/api/investigations", createBody)
	if stale.Code != http.StatusConflict || len(workflow.created) != 1 {
		t.Fatalf("stale preview = %d %s; creates=%d", stale.Code, stale.Body, len(workflow.created))
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/investigation_runs/iru_test", nil)
	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, statusRequest)
	if statusRecorder.Code != http.StatusOK ||
		!strings.Contains(statusRecorder.Body.String(), `"queued"`) ||
		!strings.Contains(statusRecorder.Body.String(), `"partial":1`) ||
		!strings.Contains(statusRecorder.Body.String(), `"EXTRACTOR_PARTIAL"`) {
		t.Fatalf("run status = %d %s", statusRecorder.Code, statusRecorder.Body)
	}
	cancel := postInvestigationJSON(t, handler, "/api/investigation_runs/iru_test/cancel",
		map[string]string{"reason": "question withdrawn"})
	if cancel.Code != http.StatusOK || len(workflow.canceled) != 1 {
		t.Fatalf("cancel = %d %s; calls=%v", cancel.Code, cancel.Body, workflow.canceled)
	}

	dark := New(Options{Version: "test", Store: repos,
		Principal: func(context.Context) string { return "user:owner" }})
	for _, target := range []string{"/api/investigation_previews", "/api/investigations"} {
		recorder := postInvestigationJSON(t, dark, target, plan)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("dark POST %s = %d %s", target, recorder.Code, recorder.Body)
		}
	}
}

func TestInvestigationAPIUnknownAndUnauthorizedRunReadsMatch(t *testing.T) {
	repos := &investigationRepoStore{}
	workflow := &investigationWorkflowFake{run: store.Run{ID: "iru_known", State: store.RunQueued}}
	handler := New(Options{
		Version: "test", Store: repos, Investigations: workflow,
		Principal: func(context.Context) string { return "user:stranger" },
	})
	var bodies [][]byte
	for _, id := range []string{"iru_known", "iru_unknown"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/investigation_runs/"+id, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d %s", id, recorder.Code, recorder.Body)
		}
		bodies = append(bodies, recorder.Body.Bytes())
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("unauthorized and unknown bodies differ:\n%s\n%s", bodies[0], bodies[1])
	}
}
