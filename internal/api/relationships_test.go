package api_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/observationpublication"
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

func TestRelationshipServiceProjectsAndFencesUnavailableV2Authority(t *testing.T) {
	visible := store.Repo{
		Name: "example.com/acme/visible", IndexedCommitHash: strings.Repeat("a", 40),
		EvidenceRevision: 7,
	}
	dataDir := t.TempDir()
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: visible.Name,
		SourceGenerationDigest:      "sha256:" + strings.Repeat("1", 64),
		SourceRootDigest:            "sha256:" + strings.Repeat("2", 64),
		ObservationGenerationDigest: "sha256:" + strings.Repeat("3", 64),
		ObservationRootDigest:       "sha256:" + strings.Repeat("4", 64),
		PartitionPolicyDigest:       "sha256:" + strings.Repeat("5", 64),
		ObservationPolicyDigest:     "sha256:" + strings.Repeat("6", 64),
		InventoryPolicyDigest:       "sha256:" + strings.Repeat("7", 64),
		RecordCount:                 10, ObservedCount: 9,
	}
	upstream, err := downstreamauthority.BuildRequired(
		observation,
		[]downstreamauthority.DomainIdentity{{Domain: "scip", Version: "v1"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := relationshippublication.MarkUnavailable(
		t.Context(), filepath.Join(dataDir, "relationships"), visible.Name, upstream,
	)
	if err != nil {
		t.Fatal(err)
	}
	state := &proofAPIStore{repos: []store.Repo{visible}}
	service := api.NewRelationshipService(api.Options{
		Store: state, DataDir: dataDir,
		Principal:             func(context.Context) string { return "reader" },
		AuthorizationProvider: "test-policy",
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool { return repository.Name == visible.Name }
		},
	}, &relationshippublication.Cache{})
	page, err := service.List(t.Context(), api.RelationshipQuery{
		Repositories: []string{visible.Name}, ServiceKey: "orders", View: "all",
	}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.RowsState != "gap" || len(page.Roots) != 1 ||
		page.Roots[0].State != "unavailable" || page.Roots[0].Reason != "upstream_unavailable" ||
		page.Roots[0].Unavailable == nil || page.Roots[0].Unavailable.Digest != marker.Digest ||
		page.Roots[0].Unavailable.Upstream.Digest != upstream.Digest ||
		page.Roots[0].Unavailable.Upstream.RequiredDomainCount != 1 ||
		page.Roots[0].Unavailable.Upstream.PublishedDomainCount != 0 ||
		len(page.Roots[0].Unavailable.Upstream.Gaps) != 1 ||
		page.Roots[0].Unavailable.Upstream.Gaps[0].Disposition != "absent" {
		t.Fatalf("unavailable relationship page = %+v", page)
	}
	coverage, err := service.RootCoverage(t.Context(), []string{visible.Name})
	if err != nil || coverage.State != "gap" || len(coverage.Roots) != 1 ||
		coverage.Roots[0].Unavailable == nil ||
		coverage.Roots[0].Unavailable.Upstream.Digest != upstream.Digest {
		t.Fatalf("unavailable coverage = %+v, %v", coverage, err)
	}

	changedObservation := observation
	changedObservation.ObservationRootDigest = "sha256:" + strings.Repeat("8", 64)
	changedUpstream, err := downstreamauthority.BuildRequired(
		changedObservation,
		[]downstreamauthority.DomainIdentity{{Domain: "scip", Version: "v1"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	getCalls := 0
	var transitionErr error
	state.onGetRepo = func(string) {
		getCalls++
		if getCalls == 2 {
			_, transitionErr = relationshippublication.MarkUnavailable(
				t.Context(), filepath.Join(dataDir, "relationships"), visible.Name, changedUpstream,
			)
		}
	}
	_, err = service.List(t.Context(), api.RelationshipQuery{
		Repositories: []string{visible.Name}, ServiceKey: "orders", View: "all",
	}, 1, "")
	if transitionErr != nil || err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("unavailable result fence = transition %v, list %v", transitionErr, err)
	}
}
