package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

type scopedSearchTransportFixture struct {
	mu sync.Mutex

	result    search.Result
	searches  int
	streams   int
	selectors []search.ScopeSelector
	queries   []string
	options   []search.Options
	searchErr error
}

func (fixture *scopedSearchTransportFixture) SearchScoped(
	_ context.Context,
	selector search.ScopeSelector,
	query string,
	opts search.Options,
) (*search.Result, error) {
	fixture.mu.Lock()
	fixture.searches++
	fixture.selectors = append(fixture.selectors, selector)
	fixture.queries = append(fixture.queries, query)
	fixture.options = append(fixture.options, opts)
	searchErr := fixture.searchErr
	fixture.mu.Unlock()
	if searchErr != nil {
		return nil, searchErr
	}
	result := cloneTransportSearchResult(fixture.result)
	return &result, nil
}

func TestSearchHTTPOnlyHidesTypedServiceScopeMisses(t *testing.T) {
	tests := []struct {
		name   string
		scope  string
		err    error
		status int
	}{
		{
			name: "service", scope: "service",
			err:    errors.Join(search.ErrScopeNotFound, store.ErrNotFound),
			status: http.StatusNotFound,
		},
		{
			name: "all code", scope: "all_code", err: store.ErrNotFound,
			status: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := &scopedSearchTransportFixture{searchErr: test.err}
			handler := api.New(api.Options{
				Version: "test", Store: &fakeStore{}, ScopedSearch: fixture,
			})
			path := "/api/search?q=needle&scope=" + test.scope
			if test.scope == "service" {
				path += "&repository=example.com%2Facme%2Fmono&service_key=orders"
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != test.status {
				t.Fatalf("%s store miss = %d %s", test.name, recorder.Code, recorder.Body)
			}
		})
	}
}

func (fixture *scopedSearchTransportFixture) StreamScoped(
	_ context.Context,
	selector search.ScopeSelector,
	query string,
	opts search.Options,
	sink func(*search.Result),
) (*search.Stats, *search.ScopeReceipt, error) {
	fixture.mu.Lock()
	fixture.streams++
	fixture.selectors = append(fixture.selectors, selector)
	fixture.queries = append(fixture.queries, query)
	fixture.options = append(fixture.options, opts)
	fixture.mu.Unlock()
	result := cloneTransportSearchResult(fixture.result)
	sink(&result)
	stats := result.Stats
	receipt := *result.Scope
	return &stats, &receipt, nil
}

func (fixture *scopedSearchTransportFixture) counts() (
	int,
	int,
	[]search.ScopeSelector,
	[]string,
	[]search.Options,
) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.searches, fixture.streams,
		append([]search.ScopeSelector{}, fixture.selectors...),
		append([]string{}, fixture.queries...),
		append([]search.Options{}, fixture.options...)
}

func TestV3ScopedSearchSharesHTTPMCPAuthorityAndSSEBoundary(t *testing.T) {
	fixture := &scopedSearchTransportFixture{result: transportServiceSearchResult()}
	path := "/api/search?q=needle&scope=service&repository=example.com%2Facme%2Fmono&service_key=orders&max_matches=7&context_lines=2"
	handler := api.New(api.Options{
		Version: "test", Store: &fakeStore{}, ScopedSearch: fixture,
	})
	httpRecorder := httptest.NewRecorder()
	handler.ServeHTTP(httpRecorder, httptest.NewRequest(http.MethodGet, path, nil))
	if httpRecorder.Code != http.StatusOK {
		t.Fatalf("v3 HTTP search = %d %s", httpRecorder.Code, httpRecorder.Body)
	}
	var httpResult search.Result
	if err := json.Unmarshal(httpRecorder.Body.Bytes(), &httpResult); err != nil {
		t.Fatal(err)
	}

	server := phebsmcp.NewServer(phebsmcp.Options{
		Version: "test", ScopedSearch: fixture,
	})
	mcpResult := callScopedSearchTool(t, server, map[string]any{
		"query": "needle", "scope": "service",
		"repository": "example.com/acme/mono", "service_key": "orders",
		"max_matches": 7, "context_lines": 2,
	})
	if httpResult.Scope == nil || mcpResult.Scope == nil ||
		!reflect.DeepEqual(httpResult.Scope, mcpResult.Scope) ||
		!reflect.DeepEqual(httpResult.Scope.Authority, mcpResult.Scope.Authority) {
		t.Fatalf(
			"HTTP/MCP v3 scope mismatch: http=%+v mcp=%+v",
			httpResult.Scope, mcpResult.Scope,
		)
	}

	streamRecorder := httptest.NewRecorder()
	streamPath := strings.Replace(path, "/api/search?", "/api/stream_search?", 1)
	handler.ServeHTTP(
		streamRecorder, httptest.NewRequest(http.MethodGet, streamPath, nil),
	)
	streamBody := streamRecorder.Body.String()
	for _, expected := range []string{
		"event: results", "event: scope", "event: done",
		`"digest":"sha256:scope-receipt"`,
		`"current_catalog_generation":"sha256:v3-current-root"`,
	} {
		if !strings.Contains(streamBody, expected) {
			t.Fatalf("v3 SSE body omitted %q:\n%s", expected, streamBody)
		}
	}
	resultAt := strings.Index(streamBody, "event: results")
	scopeAt := strings.Index(streamBody, "event: scope")
	doneAt := strings.Index(streamBody, "event: done")
	if resultAt < 0 || scopeAt <= resultAt || doneAt <= scopeAt {
		t.Fatalf("v3 SSE event order = results %d scope %d done %d:\n%s", resultAt, scopeAt, doneAt, streamBody)
	}
	searches, streams, selectors, queries, options := fixture.counts()
	if searches != 2 || streams != 1 || len(selectors) != 3 {
		t.Fatalf(
			"shared v3 transport calls = search %d stream %d selectors %+v",
			searches, streams, selectors,
		)
	}
	for _, selector := range selectors {
		if selector.Kind != search.ScopeService ||
			selector.Repository != "example.com/acme/mono" || selector.ServiceKey != "orders" {
			t.Fatalf("shared v3 selector = %+v", selector)
		}
	}
	for i := range queries {
		if queries[i] != "needle" ||
			options[i] != (search.Options{MaxMatches: 7, ContextLines: 2}) {
			t.Fatalf("shared v3 query %d = %q opts %+v", i, queries[i], options[i])
		}
	}
}

func callScopedSearchTool(
	t *testing.T,
	server *sdk.Server,
	arguments map[string]any,
) search.Result {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _, _ = server.Connect(t.Context(), serverTransport, nil) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	response, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "search_code", Arguments: arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.IsError || response.StructuredContent == nil {
		t.Fatalf("search_code error response = %+v", response)
	}
	raw, err := json.Marshal(response.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var result search.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func transportServiceSearchResult() search.Result {
	digest := func(value string) string { return "sha256:" + value }
	authority := servicequery.Authority{
		Schema: servicequery.AuthoritySchema, PredicatePolicy: servicequery.PredicatePolicy,
		TopologyPolicy: "direct-shared-whole-repository-v1",
		Repository:     "example.com/acme/mono", ServiceKey: "orders", Status: "stale",
		Incarnation: 2, RevisionSelector: "HEAD", RevisionBranch: "HEAD",
		RevisionCommit: strings.Repeat("c", 40), ExpressionDigest: digest("expression"),
		CurrentCatalogGeneration: digest("v3-current-root"), CatalogControlRevision: 7,
		ActiveCatalogGeneration: digest("v3-active-root"),
		ActiveSourceGeneration:  digest("active-source"),
		ActiveDesiredGeneration: digest("active-desired"),
		ServiceStateDigest:      digest("service-state"), ServiceStateRevision: 5,
		StateSummaryDigest: digest("state-summary"), StateSummaryRevision: 9,
		RepositorySourceGeneration: digest("repository-source"),
		RepositorySearchGeneration: digest("repository-search"),
		PathDigest:                 digest("paths"), PathCount: 1, PathBytes: 15,
		PredicateAtoms: 1, PredicateBytes: 32, Digest: digest("authority"),
	}
	receipt := search.ScopeReceipt{
		Schema: search.ScopeReceiptSchema, Kind: search.ScopeService,
		Repository: "example.com/acme/mono", ServiceKey: "orders",
		ServiceStatus:    "stale",
		MembershipPolicy: "accepted-roles-union-shared-included-unowned-excluded-v1",
		ExpressionDigest: digest("expression"), Authority: &authority,
		Revisions: []search.ScopeRevision{{
			Repository: "example.com/acme/mono", Commit: strings.Repeat("c", 40),
		}},
		ResultSetDigest: digest("results"), ResultFiles: 1, ResultMatches: 1,
		Digest: digest("scope-receipt"),
	}
	return search.Result{
		Files: []search.FileResult{{
			Repo: "example.com/acme/mono", Path: "services/orders/main.go",
			Ref: strings.Repeat("c", 40), Chunks: []search.Chunk{},
		}},
		Stats: search.Stats{MatchCount: 1, FileCount: 1, DurationMS: 3},
		Scope: &receipt,
	}
}

func cloneTransportSearchResult(result search.Result) search.Result {
	clone := result
	clone.Files = append([]search.FileResult{}, result.Files...)
	if result.Scope != nil {
		receipt := *result.Scope
		receipt.Revisions = append([]search.ScopeRevision{}, result.Scope.Revisions...)
		if result.Scope.Authority != nil {
			authority := *result.Scope.Authority
			receipt.Authority = &authority
		}
		clone.Scope = &receipt
	}
	return clone
}
