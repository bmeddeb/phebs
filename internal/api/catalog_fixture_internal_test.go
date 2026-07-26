package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

type contractFixtureRepoStore struct {
	store.Store
	repos []store.Repo
	calls int
}

func (s *contractFixtureRepoStore) ListRepos(context.Context) ([]store.Repo, error) {
	s.calls++
	return append([]store.Repo(nil), s.repos...), nil
}

func TestContractCatalogFixtureExplicitBindingAndPinnedProjection(t *testing.T) {
	fixture, err := LoadContractCatalogFixture(filepath.Join(
		"..", "..", "docs", "fixtures", "contracts", "contract-atlas.json",
	))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repos := &contractFixtureRepoStore{repos: []store.Repo{{
		Name: "github.com/sourcegraph/zoekt", IndexedCommitHash: commit,
	}}}
	opts := Options{
		Version: "test", Store: repos, ContractCatalogFixture: fixture,
		Principal:             func(context.Context) string { return "user:fixture" },
		AuthorizationProvider: "fixture-test-v1",
	}
	service := NewContractCatalogService(opts)
	if service == nil {
		t.Fatal("explicit fixture did not enable contract catalog service")
	}

	first, err := service.List(context.Background(), ContractCatalogQuery{}, 1, "")
	if err != nil {
		t.Fatalf("first fixture page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].Kind != "service" ||
		first.Pagination.NextCursor == "" || first.Pagination.Complete {
		t.Fatalf("first fixture page = %+v", first)
	}
	second, err := service.List(
		context.Background(), ContractCatalogQuery{}, 1, first.Pagination.NextCursor,
	)
	if err != nil {
		t.Fatalf("second fixture page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Operation != "/demo.search.v1.CodeSearch/Search" ||
		!second.Pagination.Complete {
		t.Fatalf("second fixture page = %+v", second)
	}

	detail, err := service.Operation(
		context.Background(), second.Items[0].Repository,
		second.Items[0].Lineage, second.Items[0].Operation,
	)
	if err != nil {
		t.Fatalf("fixture operation: %v", err)
	}
	if detail.Request.State != "resolved" || detail.Response.State != "resolved" ||
		len(detail.Request.Fields) != 3 || len(detail.Response.Fields) != 3 ||
		len(detail.Implementations) != 1 || len(detail.Callers) != 1 ||
		len(detail.UnresolvedCandidates) != 1 {
		t.Fatalf("fixture detail shape = %+v", detail)
	}
	for _, claim := range []ContractCatalogClaim{
		detail.Declaration,
		detail.Request.Fields[0].Declaration,
		detail.Response.Fields[0].Declaration,
		detail.Implementations[0].Claim,
		detail.Callers[0].Claim,
		detail.UnresolvedCandidates[0].Claim,
	} {
		if len(claim.Sources) != 1 || claim.Sources[0].Repository != repos.repos[0].Name ||
			claim.Sources[0].Commit != commit || claim.Sources[0].Path != "go.mod" {
			t.Fatalf("claim did not bind a visible pinned source: %+v", claim)
		}
	}

	handler := New(opts)
	for target, want := range map[string]string{
		"/api/version":      `"contract-atlas"`,
		"/api/openapi.json": `"/api/contract_atlas"`,
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("%s = %d %s, want %s", target, recorder.Code, recorder.Body.String(), want)
		}
	}
}

func TestContractCatalogFixtureThriftProjection(t *testing.T) {
	fixture := &ContractCatalogFixture{
		SchemaVersion: contractCatalogFixtureSchema,
		Protocol:      "thrift",
		Package:       "agent",
		ServiceFQN:    "agent.Agent",
		Lineage:       "fixture_thrift_lineage",
		SourcePath:    "agent.thrift",
		SourceLine:    1,
		Operations: []contractCatalogFixtureOperation{{
			Method: "emitBatch",
			OneWay: true,
			Request: contractCatalogFixtureMessage{
				Name: "agent.Agent.emitBatch_args", Kind: "union", Synthetic: true,
				Fields: []contractCatalogFixtureField{{
					Name: "batch", Number: 1, Type: "agent.Batch", Cardinality: "required",
				}},
			},
			Response: contractCatalogFixtureMessage{
				Name: "void", Kind: "struct", Synthetic: true,
			},
		}},
	}
	if err := fixture.validate(); err != nil {
		t.Fatal(err)
	}
	const commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	repos := &contractFixtureRepoStore{repos: []store.Repo{{
		Name: "github.com/acme/thrift", IndexedCommitHash: commit,
	}}}
	service := NewContractCatalogService(Options{
		Version: "test", Store: repos, ContractCatalogFixture: fixture,
		Principal:             func(context.Context) string { return "user:fixture" },
		AuthorizationProvider: "fixture-test-v1",
	})
	list, err := service.List(context.Background(), ContractCatalogQuery{Protocol: "thrift"}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[0].Protocol != "thrift" ||
		list.Items[0].Declaration.RunID != fixtureCatalogRunID(repos.repos[0].Name, "thrift-contract") {
		t.Fatalf("thrift fixture list = %+v", list.Items)
	}
	detail, err := service.Operation(
		context.Background(), repos.repos[0].Name, fixture.Lineage,
		"/agent.Agent/emitBatch",
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.FactDetail.OneWay == nil || !*detail.FactDetail.OneWay ||
		detail.FactDetail.Response.Resolution != "intrinsic" ||
		detail.Request.Kind != "union" || !detail.Request.Synthetic ||
		detail.Response.Raw != "void" ||
		detail.Implementations[0].Classification != "resolved_implementation" ||
		detail.Implementations[0].Claim.Predicate != "REGISTERS_THRIFT_SERVICE" ||
		detail.UnresolvedCandidates[0].Claim.Predicate != "UNRESOLVED_THRIFT_CALL" ||
		detail.UnresolvedCandidates[0].Claim.Object != "/agent.Agent/emitBatch" {
		t.Fatalf("thrift fixture detail = %+v", detail)
	}
	status := map[string]string{}
	for _, run := range list.Coverage.Repositories[0].Runs {
		status[run.Domain] = run.Status
	}
	if status["thrift-contract"] != "published" ||
		status["thrift-consumer"] != "published" ||
		status["proto-contract"] != "unpublished" ||
		status["grpc-consumer"] != "unpublished" {
		t.Fatalf("thrift fixture coverage = %+v", status)
	}
}

func TestContractCatalogFixtureFailsClosed(t *testing.T) {
	t.Run("malformed file", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "fixture.json")
		if err := os.WriteFile(filename, []byte(`{"schema_version":"wrong"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadContractCatalogFixture(filename); err == nil {
			t.Fatal("malformed fixture was accepted")
		}
	})

	t.Run("anonymous does not query repositories", func(t *testing.T) {
		fixture, err := LoadContractCatalogFixture(filepath.Join(
			"..", "..", "docs", "fixtures", "contracts", "contract-atlas.json",
		))
		if err != nil {
			t.Fatal(err)
		}
		repos := &contractFixtureRepoStore{repos: []store.Repo{{
			Name: "hidden", IndexedCommitHash: strings.Repeat("a", 40),
		}}}
		service := NewContractCatalogService(Options{
			Store: repos, ContractCatalogFixture: fixture,
			Principal: func(context.Context) string { return "" },
		})
		_, err = service.List(context.Background(), ContractCatalogQuery{}, 10, "")
		var statusError interface{ GetStatus() int }
		if !errors.As(err, &statusError) || statusError.GetStatus() != http.StatusNotFound ||
			repos.calls != 0 {
			t.Fatalf("anonymous fixture = %v, repo calls=%d", err, repos.calls)
		}
	})
}
