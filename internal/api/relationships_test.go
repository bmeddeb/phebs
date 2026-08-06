package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestRelationshipServiceAuthorizationGapAndRetainedProofShape(t *testing.T) {
	visible := store.Repo{
		Name: "example.com/acme/visible", IndexedCommitHash: strings.Repeat("a", 40),
		EvidenceRevision: 7,
	}
	hidden := store.Repo{
		Name: "example.com/acme/hidden", IndexedCommitHash: strings.Repeat("b", 40),
		EvidenceRevision: 9,
	}
	state := &proofAPIStore{repos: []store.Repo{hidden, visible}}
	opts := api.Options{
		Store: state, DataDir: t.TempDir(),
		Principal:             func(context.Context) string { return "reader" },
		AuthorizationProvider: "test-policy",
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool { return repository.Name == visible.Name }
		},
	}
	service := api.NewRelationshipService(opts, &relationshippublication.Cache{})
	if service == nil {
		t.Fatal("relationship service was not constructed")
	}
	page, err := service.List(t.Context(), api.RelationshipQuery{
		ServiceKey: "orders", View: "dependencies",
	}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.RowsState != "gap" || len(page.Rows) != 0 || len(page.Roots) != 1 ||
		page.Roots[0].Repository != visible.Name || page.Roots[0].State != "unavailable" ||
		page.Coverage.AuthorizedRepositories != 1 || page.Coverage.UnavailableRoots != 1 {
		t.Fatalf("gap page = %+v", page)
	}
	if _, err := service.List(t.Context(), api.RelationshipQuery{
		Repositories: []string{hidden.Name}, ServiceKey: "orders", View: "all",
	}, 1, ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("hidden repository = %v", err)
	}
	coverage, err := service.RootCoverage(t.Context(), []string{visible.Name})
	if err != nil || coverage.VisibleRepositoryCount != 1 || coverage.ExactRootCount != 0 ||
		coverage.GapCount != 1 || coverage.Digest == "" {
		t.Fatalf("root coverage = %+v, %v", coverage, err)
	}

	// The optional annex must not rewrite retained proof-bundle-v1 bytes.
	legacy := api.ProofBundle{SchemaVersion: "proof-bundle-v1"}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "relationship_coverage") {
		t.Fatalf("legacy proof shape changed: %s", raw)
	}
}
