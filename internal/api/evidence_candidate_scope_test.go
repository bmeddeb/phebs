package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestEvidenceViewDistinguishesZeroCandidateScopeFromLegacy(t *testing.T) {
	const (
		legacyRepo = "github.com/visible/legacy"
		scopedRepo = "github.com/visible/scoped-zero"
	)
	digest := "sha256:" + strings.Repeat("a", 64)
	legacy := publishedEvidenceRun(legacyRepo, "legacy", 0)
	scoped := publishedEvidenceRun(scopedRepo, "scoped", 0)
	scoped.Coverage = store.CoverageManifest{
		SourceScopeDigest:       digest,
		ScopePosture:            "whole-repository",
		CandidateManifestDigest: digest,
		CandidatePlane:          "repository",
		ScopeCorpusDigest:       digest,
		PlannedScopeDigest:      digest,
		TypedInputKind:          analysisunit.TypedIndexKindSCIP,
	}
	st := &evidenceViewStore{
		repos: []store.Repo{
			{Name: scopedRepo, IndexedCommitHash: scoped.Commit},
			{Name: legacyRepo, IndexedCommitHash: legacy.Commit},
		},
		runs: map[string]store.ExtractionRun{
			legacyRepo: legacy,
			scopedRepo: scoped,
		},
		assertions: map[string][]store.Assertion{
			legacyRepo: {},
			scopedRepo: {},
		},
	}
	handler := api.New(api.Options{
		Version:  "test",
		Store:    st,
		Evidence: st,
		Visible: func(context.Context) func(store.Repo) bool {
			return func(store.Repo) bool { return true }
		},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/evidence?domain=proto-contract",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"GET candidate-scope evidence = %d %s",
			recorder.Code,
			recorder.Body,
		)
	}
	var view api.EvidenceView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Repositories) != 2 ||
		view.Repositories[0].Repository != legacyRepo ||
		view.Repositories[1].Repository != scopedRepo {
		t.Fatalf("repository projection = %+v", view.Repositories)
	}
	if view.Repositories[0].CandidateScope != nil {
		t.Fatalf(
			"legacy run acquired candidate scope: %+v",
			view.Repositories[0].CandidateScope,
		)
	}
	scope := view.Repositories[1].CandidateScope
	if scope == nil ||
		scope.CorpusFileCount != 0 ||
		scope.CorpusDeclaredBytes != 0 ||
		scope.PlannedFileCount != 0 ||
		scope.PlannedRequiredCount != 0 ||
		scope.PlannedDeclaredBytes != 0 ||
		scope.TypedInput == nil ||
		scope.TypedInput.Kind != analysisunit.TypedIndexKindSCIP ||
		scope.TypedInput.Present ||
		scope.TypedInput.DeclaredBytes != 0 {
		t.Fatalf("explicit zero candidate scope = %+v", scope)
	}
	if !strings.Contains(
		recorder.Body.String(),
		`"typed_input":{"kind":"scip","declared_bytes":0,"present":false}`,
	) {
		t.Fatalf(
			"raw evidence response omitted explicit zero receipt: %s",
			recorder.Body,
		)
	}
}
