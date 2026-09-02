package t4110

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	phebssearch "github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const productHTTPHost = "127.0.0.1"

func (h *liveHarness) querySelectedProductTransports(
	ctx context.Context,
	serviceKey string,
	cost *PhaseCost,
) error {
	searcher, err := phebssearch.Open(filepath.Join(h.dataDir, "index"), h.state)
	if err != nil {
		return fmt.Errorf("open product transport searcher: %w", err)
	}
	defer searcher.Close()
	visible := func(context.Context) func(store.Repo) bool {
		return func(repository store.Repo) bool {
			return repository.Name == liveRepository
		}
	}
	searcher.Visible = visible
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := store.NewServiceStateV3Reader(h.state, cache)
	if err != nil {
		return err
	}
	scoped, err := phebssearch.NewRuntimeScopedSearcher(searcher, reader)
	if err != nil {
		return err
	}
	apiOptions := api.Options{
		Version: h.phebsVersion, Store: h.state, ScopedSearch: scoped,
		Visible: visible, AuthorizationProvider: "t4110-live-visible-v1",
	}
	directory := api.NewRuntimeServiceDirectoryService(apiOptions, reader)
	if directory == nil {
		return errors.New("construct live service directory")
	}
	apiOptions.ServiceDirectory = directory
	handler := api.New(apiOptions)

	query := url.Values{"repository": {liveRepository}, "page_size": {"1"}}
	httpInventory, err := getProductJSON[api.ServiceInventory](
		ctx, handler, "/api/services?"+query.Encode(),
	)
	if err != nil {
		return fmt.Errorf("HTTP service inventory: %w", err)
	}
	detailQuery := url.Values{
		"repository": {liveRepository}, "service_key": {serviceKey},
	}
	httpDetail, err := getProductJSON[api.ServiceDetail](
		ctx, handler, "/api/service?"+detailQuery.Encode(),
	)
	if err != nil {
		return fmt.Errorf("HTTP service detail: %w", err)
	}
	searchQuery := url.Values{
		"q": {"t411-neutral-fixture-v1"}, "scope": {phebssearch.ScopeService},
		"repository": {liveRepository}, "service_key": {serviceKey},
		"max_matches": {"10"},
	}
	httpSearch, err := getProductJSON[phebssearch.Result](
		ctx, handler, api.SearchPath+"?"+searchQuery.Encode(),
	)
	if err != nil {
		return fmt.Errorf("HTTP service search: %w", err)
	}

	mcpServer := phebsmcp.NewServer(phebsmcp.Options{
		Version: h.phebsVersion, Store: h.state, ScopedSearch: scoped,
		ServiceDirectory: directory, Visible: visible,
	})
	transportCtx, cancelTransport := context.WithCancel(ctx)
	defer cancelTransport()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	type serverConnection struct {
		session *sdk.ServerSession
		err     error
	}
	connected := make(chan serverConnection, 1)
	go func() {
		session, connectErr := mcpServer.Connect(transportCtx, serverTransport, nil)
		connected <- serverConnection{session: session, err: connectErr}
	}()
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t4110-live-gate", Version: h.phebsVersion}, nil,
	)
	clientSession, err := client.Connect(transportCtx, clientTransport, nil)
	if err != nil {
		cancelTransport()
		serverResult := <-connected
		if serverResult.session != nil {
			_ = serverResult.session.Close()
		}
		return errors.Join(fmt.Errorf("connect live MCP client: %w", err), serverResult.err)
	}
	serverResult := <-connected
	if serverResult.err != nil {
		_ = clientSession.Close()
		return fmt.Errorf("connect live MCP server: %w", serverResult.err)
	}
	defer func() { _ = serverResult.session.Close() }()
	defer func() { _ = clientSession.Close() }()
	mcpInventory, err := callProductTool[api.ServiceInventory](
		ctx, clientSession, "list_services", map[string]any{
			"repository": liveRepository, "page_size": 1,
		},
	)
	if err != nil {
		return err
	}
	mcpDetail, err := callProductTool[api.ServiceDetail](
		ctx, clientSession, "get_service", map[string]any{
			"repository": liveRepository, "service_key": serviceKey,
		},
	)
	if err != nil {
		return err
	}
	mcpSearch, err := callProductTool[phebssearch.Result](
		ctx, clientSession, "search_code", map[string]any{
			"query": "t411-neutral-fixture-v1", "scope": phebssearch.ScopeService,
			"repository": liveRepository, "service_key": serviceKey,
			"max_matches": 10,
		},
	)
	if err != nil {
		return err
	}

	expectedState, err := h.state.GetServiceStateV3PointSnapshot(
		ctx, liveRepository, serviceKey,
		h.selector.StateControlRevision, h.selector.StateSummaryDigest,
	)
	if err != nil {
		return fmt.Errorf("read selected product oracle: %w", err)
	}
	if err := h.validateSelectedDirectory(
		serviceKey, expectedState, httpInventory, httpDetail,
	); err != nil {
		return fmt.Errorf("HTTP selected directory: %w", err)
	}
	if err := h.validateSelectedSearchResultWithState(
		serviceKey, expectedState, &httpSearch,
	); err != nil {
		return fmt.Errorf("HTTP selected search: %w", err)
	}
	if !reflect.DeepEqual(httpInventory, mcpInventory) ||
		!reflect.DeepEqual(httpDetail, mcpDetail) ||
		!reflect.DeepEqual(httpSearch.Files, mcpSearch.Files) ||
		httpSearch.Stats.FileCount != mcpSearch.Stats.FileCount ||
		httpSearch.Stats.MatchCount != mcpSearch.Stats.MatchCount ||
		!reflect.DeepEqual(httpSearch.Scope, mcpSearch.Scope) {
		return errors.New("live HTTP and MCP product results differ")
	}
	if err := clientSession.Close(); err != nil {
		return fmt.Errorf("close live MCP client: %w", err)
	}
	if err := serverResult.session.Close(); err != nil {
		return fmt.Errorf("close live MCP server: %w", err)
	}
	browserReport, err := h.runLiveUIProbe(ctx, serviceKey, apiOptions)
	if err != nil {
		return fmt.Errorf("live UI product reads: %w", err)
	}
	stats := cache.Stats()
	if stats.RootLeases != 0 || stats.MemberLeases != 0 {
		return errors.New("live product transports retained state-reader leases")
	}
	*cost = PhaseCost{
		SelectedStateRootReads:         stats.RootReads,
		SelectedStateMemberReads:       stats.MemberReads,
		SelectedStateRootValidations:   stats.RootValidations,
		SelectedStateMemberValidations: stats.MemberValidations,
		ProductQueries:                 9,
		BrowserProductReads:            uint64(browserReport.InventoryRequests + browserReport.DetailRequests),
	}
	return nil
}

func (h *liveHarness) validateSelectedDirectory(
	serviceKey string,
	expectedState servicecatalog.ServiceState,
	inventory api.ServiceInventory,
	detail api.ServiceDetail,
) error {
	if inventory.SchemaVersion != "phebs-service-inventory-v1" ||
		inventory.Filters.Repository != liveRepository ||
		inventory.Repository.Repository != liveRepository ||
		inventory.Repository.SourceCommit != h.commit ||
		inventory.Repository.CatalogGeneration != h.selector.CatalogRootDigest ||
		inventory.Repository.CatalogControlRevision != h.selector.CatalogControlRevision ||
		inventory.Repository.StateControlRevision != h.selector.StateControlRevision ||
		inventory.Repository.CatalogServiceCount != len(h.catalog.Services) ||
		len(inventory.Services) != 1 || inventory.Services[0].Key != serviceKey ||
		inventory.Pagination.Order != "service_key:asc" ||
		inventory.Pagination.PageSize != 1 || inventory.Pagination.Returned != 1 ||
		inventory.Pagination.NextCursor == "" {
		return errors.New("inventory response differs from selected authority")
	}
	if detail.SchemaVersion != "phebs-service-detail-v1" ||
		detail.Repository != inventory.Repository || detail.Service != inventory.Services[0] {
		return errors.New("detail response differs from inventory authority")
	}
	if detail.Service.Status != expectedState.Status ||
		detail.Service.Incarnation != expectedState.Incarnation ||
		detail.Service.StateDigest != expectedState.StateDigest ||
		detail.Service.ControlRevision != expectedState.ControlRevision ||
		detail.Service.ActiveCatalogGeneration != expectedState.ActiveCatalogGeneration ||
		detail.Service.ActiveSourceGeneration != expectedState.ActiveSourceGeneration ||
		detail.Service.ActiveDesiredGeneration != expectedState.ActiveDesiredGeneration {
		return errors.New("detail service state differs from selected state")
	}
	for _, service := range h.catalog.Services {
		if service.Key != serviceKey {
			continue
		}
		if !slices.Equal(detail.Successors, service.Successors) {
			return errors.New("detail successors differ from selected catalog")
		}
		expectedMemberships := make([]api.ServiceMembership, 0)
		for _, membership := range h.catalog.Memberships {
			if membership.ServiceKey == serviceKey {
				expectedMemberships = append(expectedMemberships, api.ServiceMembership{
					Path: membership.Path, Role: membership.Role, Origin: membership.Origin,
				})
			}
		}
		if !slices.Equal(detail.Memberships, expectedMemberships) {
			return errors.New("detail memberships differ from selected catalog")
		}
		return nil
	}
	return errors.New("selected service is absent from final catalog")
}

func getProductJSON[T any](
	ctx context.Context,
	handler http.Handler,
	path string,
) (T, error) {
	var result T
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return result, err
	}
	request.Host = productHTTPHost
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		return result, fmt.Errorf("status %d", recorder.Code)
	}
	schemaPath, err := productResponseSchemaLink(recorder.Header().Values("Link"))
	if err != nil {
		return result, err
	}
	if err := decodeProductHTTPJSON(recorder.Body.Bytes(), schemaPath, &result); err != nil {
		return result, err
	}
	return result, nil
}

func callProductTool[T any](
	ctx context.Context,
	session *sdk.ClientSession,
	name string,
	arguments map[string]any,
) (T, error) {
	var result T
	response, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: name, Arguments: arguments,
	})
	if err != nil {
		return result, fmt.Errorf("call live MCP tool %s: %w", name, err)
	}
	if response.IsError || response.StructuredContent == nil {
		return result, fmt.Errorf("live MCP tool %s returned an error", name)
	}
	raw, err := json.Marshal(response.StructuredContent)
	if err != nil {
		return result, fmt.Errorf("encode live MCP tool %s result: %w", name, err)
	}
	if err := decodeProductJSON(raw, &result); err != nil {
		return result, fmt.Errorf("decode live MCP tool %s result: %w", name, err)
	}
	return result, nil
}

func decodeProductJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("product response contains trailing JSON")
	}
	return nil
}

func productResponseSchemaLink(headers []string) (string, error) {
	if len(headers) != 1 {
		return "", errors.New("product HTTP response schema header is invalid")
	}
	value, ok := strings.CutPrefix(headers[0], "<")
	if !ok {
		return "", errors.New("product HTTP response schema header is invalid")
	}
	value, ok = strings.CutSuffix(value, `>; rel="describedBy"`)
	if !ok || strings.ContainsAny(value, "?#") {
		return "", errors.New("product HTTP response schema header is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" ||
		parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		!strings.HasPrefix(parsed.Path, "/schemas/") {
		return "", errors.New("product HTTP response schema link is invalid")
	}
	name := strings.TrimPrefix(parsed.Path, "/schemas/")
	if len(name) < len("A.json") || len(name) > 160 || strings.Contains(name, "/") ||
		!strings.HasSuffix(name, ".json") {
		return "", errors.New("product HTTP response schema name is invalid")
	}
	for _, character := range strings.TrimSuffix(name, ".json") {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return "", errors.New("product HTTP response schema name is invalid")
	}
	return parsed.Path, nil
}

func decodeProductHTTPJSON(data []byte, schemaPath string, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("product HTTP response is not an object")
	}
	seen := make(map[string]struct{}, 16)
	var body bytes.Buffer
	body.Grow(len(data))
	body.WriteByte('{')
	first := true
	schemaSeen := false
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return errors.New("product HTTP response key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("product HTTP response has a duplicate key")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return errors.New("product HTTP response value is invalid")
		}
		if key == "$schema" {
			var actual string
			if err := decodeProductJSON(value, &actual); err != nil || strings.ContainsAny(actual, "?#") {
				return errors.New("product HTTP response schema link is invalid")
			}
			parsed, parseErr := url.Parse(actual)
			if parseErr != nil || parsed.Scheme != "http" || parsed.Host != productHTTPHost ||
				parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
				parsed.Path != schemaPath {
				return errors.New("product HTTP response schema link differs from its header")
			}
			schemaSeen = true
			continue
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return err
		}
		if !first {
			body.WriteByte(',')
		}
		first = false
		body.Write(encodedKey)
		body.WriteByte(':')
		body.Write(value)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || !schemaSeen {
		return errors.New("product HTTP response lacks its schema link")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("product HTTP response contains trailing JSON")
	}
	body.WriteByte('}')
	return decodeProductJSON(body.Bytes(), destination)
}
