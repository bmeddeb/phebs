package mcp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicequery"
)

type scopedSearchToolFixture struct {
	result   search.Result
	calls    int
	selector search.ScopeSelector
	query    string
	opts     search.Options
}

func (fixture *scopedSearchToolFixture) SearchScoped(
	_ context.Context,
	selector search.ScopeSelector,
	query string,
	opts search.Options,
) (*search.Result, error) {
	fixture.calls++
	fixture.selector = selector
	fixture.query = query
	fixture.opts = opts
	result := fixture.result
	return &result, nil
}

func (*scopedSearchToolFixture) StreamScoped(
	context.Context,
	search.ScopeSelector,
	string,
	search.Options,
	func(*search.Result),
) (*search.Stats, *search.ScopeReceipt, error) {
	panic("MCP search_code must not select the streaming boundary")
}

func TestSearchCodeSelectsScopedSearchAndDefaultsToSearch(t *testing.T) {
	t.Run("preconstructed scoped search", func(t *testing.T) {
		authority := servicequery.Authority{
			Schema:                   servicequery.AuthoritySchema,
			Repository:               "example.com/acme/mono",
			ServiceKey:               "orders",
			Status:                   "stale",
			CurrentCatalogGeneration: "sha256:" + strings.Repeat("a", 64),
			ActiveCatalogGeneration:  "sha256:" + strings.Repeat("b", 64),
		}
		want := search.Result{Scope: &search.ScopeReceipt{
			Schema: search.ScopeReceiptSchema, Kind: search.ScopeService,
			Repository: "example.com/acme/mono", ServiceKey: "orders",
			ServiceStatus: "stale", Authority: &authority,
		}}
		fixture := &scopedSearchToolFixture{result: want}
		got, response := callTool[search.Result](t, NewServer(Options{
			Version: "test", ScopedSearch: fixture,
		}), "search_code", map[string]any{
			"query": "needle", "scope": "service",
			"repository": "example.com/acme/mono", "service_key": "orders",
			"max_matches": 7, "context_lines": 2,
		})
		if response.IsError {
			t.Fatalf("search_code errored: %+v", response.Content)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("search_code result = %+v, want %+v", got, want)
		}
		wantSelector := search.ScopeSelector{
			Kind: search.ScopeService, Repository: "example.com/acme/mono",
			ServiceKey: "orders",
		}
		if fixture.calls != 1 || fixture.selector != wantSelector ||
			fixture.query != "needle" ||
			fixture.opts != (search.Options{MaxMatches: 7, ContextLines: 2}) {
			t.Fatalf(
				"scoped search call = calls %d selector %+v query %q opts %+v",
				fixture.calls, fixture.selector, fixture.query, fixture.opts,
			)
		}
	})

	t.Run("ordinary search remains default", func(t *testing.T) {
		indexDir := filepath.Join(t.TempDir(), "index")
		if err := os.Mkdir(indexDir, 0o755); err != nil {
			t.Fatal(err)
		}
		ordinary, err := search.Open(indexDir, fakeStore{})
		if err != nil {
			t.Fatal(err)
		}
		defer ordinary.Close()
		got, response := callTool[search.Result](t, NewServer(Options{
			Version: "test", Search: ordinary,
		}), "search_code", map[string]any{"query": "needle"})
		if response.IsError {
			t.Fatalf("default search_code errored: %+v", response.Content)
		}
		if got.Scope == nil || got.Scope.Schema != search.ScopeReceiptSchema ||
			got.Scope.Kind != search.ScopeAllCode {
			t.Fatalf("default search scope = %+v", got.Scope)
		}
	})
}
