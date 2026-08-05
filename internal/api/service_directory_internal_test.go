package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

type serviceDirectoryTestStore struct {
	store.Store
	repositories map[string]store.Repo
	publication  servicecatalog.Publication
	summary      servicecatalog.RepositoryState
	entries      []store.ServiceStateEntry
	scanLimit    int
	calls        []string
}

func (fake *serviceDirectoryTestStore) GetRepo(
	_ context.Context,
	repository string,
) (*store.Repo, error) {
	fake.calls = append(fake.calls, "get-repository:"+repository)
	value, ok := fake.repositories[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &value, nil
}

func (fake *serviceDirectoryTestStore) ConfirmServiceStateSnapshot(
	_ context.Context,
	_ string,
	expected servicecatalog.RepositoryState,
) error {
	fake.calls = append(fake.calls, "confirm-summary")
	if expected.CatalogGeneration != fake.summary.CatalogGeneration ||
		expected.CatalogControlRevision != fake.summary.CatalogControlRevision ||
		expected.ControlRevision != fake.summary.ControlRevision ||
		expected.SummaryDigest != fake.summary.SummaryDigest {
		return store.ErrConflict
	}
	return nil
}

func (fake *serviceDirectoryTestStore) GetServiceStateRead(
	_ context.Context,
	repository, serviceKey string,
) (*store.ServiceStateRead, error) {
	fake.calls = append(fake.calls, "detail:"+serviceKey)
	for _, entry := range fake.entries {
		if entry.State.Repository == repository && entry.State.ServiceKey == serviceKey {
			return &store.ServiceStateRead{
				Publication: fake.publication, Summary: fake.summary, Entry: entry,
			}, nil
		}
	}
	return nil, store.ErrNotFound
}

func (fake *serviceDirectoryTestStore) ListServiceStates(
	_ context.Context,
	repository string,
	filter store.ServiceStateFilter,
	after store.ServiceStatePosition,
	limit int,
) (*store.ServiceStatePage, error) {
	fake.calls = append(fake.calls, "list:"+after.ServiceKey)
	entries := make([]store.ServiceStateEntry, 0, limit)
	scanned := 0
	var continuation *store.ServiceStatePosition
	for index, entry := range fake.entries {
		state := entry.State
		if state.Repository != repository || state.ServiceKey <= after.ServiceKey {
			continue
		}
		scanned++
		if filter.Status != "" && state.Status != filter.Status ||
			filter.Disposition != "" && state.Disposition != filter.Disposition ||
			!filter.IncludeRemoved && state.Removed {
			if fake.scanLimit > 0 && scanned == fake.scanLimit && index < len(fake.entries)-1 {
				continuation = &store.ServiceStatePosition{
					ServiceKey: state.ServiceKey, Incarnation: state.Incarnation,
				}
				break
			}
			continue
		}
		entries = append(entries, entry)
		if len(entries) == limit {
			break
		}
		if fake.scanLimit > 0 && scanned == fake.scanLimit && index < len(fake.entries)-1 {
			continuation = &store.ServiceStatePosition{
				ServiceKey: state.ServiceKey, Incarnation: state.Incarnation,
			}
			break
		}
	}
	return &store.ServiceStatePage{
		Publication: fake.publication, Summary: fake.summary, Entries: entries,
		Continuation: continuation,
	}, nil
}

func TestServiceDirectoryAuthorizationCursorAndDetail(t *testing.T) {
	fake := newServiceDirectoryTestStore()
	visibleCalls := 0
	handler := New(Options{
		Version: "test", Store: fake,
		Principal:             func(context.Context) string { return "user:test" },
		AuthorizationProvider: "test-permissions-v1",
		Visible: func(context.Context) func(store.Repo) bool {
			visibleCalls++
			fake.calls = append(fake.calls, "resolve-visibility")
			return func(repository store.Repo) bool {
				fake.calls = append(fake.calls, "check-visibility:"+repository.Name)
				return repository.Name == fake.publication.Repository
			}
		},
	})

	firstCode, firstBody := serviceDirectoryHTTP(t, handler,
		"/api/services?repository="+url.QueryEscape(fake.publication.Repository)+"&page_size=1")
	if firstCode != http.StatusOK {
		t.Fatalf("first page = %d %s", firstCode, firstBody)
	}
	var first ServiceInventory
	if err := json.Unmarshal([]byte(firstBody), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Services) != 1 || first.Services[0].Key != "orders" ||
		first.Services[0].MembershipCount != 1 || first.Pagination.NextCursor == "" ||
		first.Repository.CatalogGeneration != fake.publication.GenerationDigest {
		t.Fatalf("first page = %+v", first)
	}

	secondCode, secondBody := serviceDirectoryHTTP(t, handler,
		"/api/services?repository="+url.QueryEscape(fake.publication.Repository)+
			"&page_size=1&cursor="+url.QueryEscape(first.Pagination.NextCursor))
	if secondCode != http.StatusOK {
		t.Fatalf("second page = %d %s", secondCode, secondBody)
	}
	var second ServiceInventory
	if err := json.Unmarshal([]byte(secondBody), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Services) != 1 || second.Services[0].Key != "payments" ||
		second.Pagination.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}

	mismatchCode, _ := serviceDirectoryHTTP(t, handler,
		"/api/services?repository="+url.QueryEscape(fake.publication.Repository)+
			"&page_size=1&status=current&cursor="+url.QueryEscape(first.Pagination.NextCursor))
	if mismatchCode != http.StatusConflict {
		t.Fatalf("filter-mismatched cursor = %d, want 409", mismatchCode)
	}

	detailCode, detailBody := serviceDirectoryHTTP(t, handler,
		"/api/service?repository="+url.QueryEscape(fake.publication.Repository)+
			"&service_key=orders")
	if detailCode != http.StatusOK {
		t.Fatalf("detail = %d %s", detailCode, detailBody)
	}
	var detail ServiceDetail
	if err := json.Unmarshal([]byte(detailBody), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Service != first.Services[0] || len(detail.Memberships) != 1 ||
		detail.Memberships[0].Path != "services/orders" || detail.Successors == nil {
		t.Fatalf("detail = %+v", detail)
	}

	if visibleCalls < 7 {
		t.Fatalf("visibility was not rechecked around reads: %d", visibleCalls)
	}
	if !slices.Contains(fake.calls, "confirm-summary") {
		t.Fatalf("response did not confirm state snapshot: %v", fake.calls)
	}
}

func TestServiceDirectoryHiddenAndAbsentPrecedeInputAndStateWork(t *testing.T) {
	fake := newServiceDirectoryTestStore()
	handler := New(Options{
		Version: "test", Store: fake,
		Visible: func(context.Context) func(store.Repo) bool {
			fake.calls = append(fake.calls, "resolve-visibility")
			return func(repository store.Repo) bool {
				fake.calls = append(fake.calls, "check-visibility:"+repository.Name)
				return repository.Name == fake.publication.Repository
			}
		},
	})
	for _, endpoint := range []string{"/api/services", "/api/service"} {
		for _, repository := range []string{"example.com/acme/hidden", "example.com/acme/absent"} {
			fake.calls = nil
			path := endpoint + "?repository=" + url.QueryEscape(repository)
			if endpoint == "/api/services" {
				path += "&page_size=100000&cursor=not-base64"
			} else {
				path += "&service_key=not-present"
			}
			code, body := serviceDirectoryHTTP(t, handler, path)
			if code != http.StatusNotFound || !strings.Contains(body, "service catalog not found") {
				t.Fatalf("%s %s = %d %s", endpoint, repository, code, body)
			}
			for _, call := range fake.calls {
				if strings.HasPrefix(call, "list:") || strings.HasPrefix(call, "detail:") ||
					call == "confirm-summary" {
					t.Fatalf("unauthorized %s performed state work: %v", endpoint, fake.calls)
				}
			}
			if len(fake.calls) < 2 || fake.calls[0] != "resolve-visibility" ||
				!strings.HasPrefix(fake.calls[1], "get-repository:") {
				t.Fatalf("authorization order = %v", fake.calls)
			}
		}
	}
}

func TestServiceDirectoryCursorBindsSnapshotAndIncarnation(t *testing.T) {
	fake := newServiceDirectoryTestStore()
	principal := "user:test"
	service := NewServiceDirectoryService(Options{
		Store: fake, Principal: func(context.Context) string { return principal },
	})
	first, err := service.List(t.Context(), ServiceInventoryQuery{
		Repository: fake.publication.Repository,
	}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	principal = "user:other"
	if _, err := service.List(t.Context(), ServiceInventoryQuery{
		Repository: fake.publication.Repository,
	}, 1, first.Pagination.NextCursor); humaStatus(err) != http.StatusConflict {
		t.Fatalf("authorization-transition cursor = %v, want 409", err)
	}
	principal = "user:test"
	fake.summary.ControlRevision++
	if _, err := service.List(t.Context(), ServiceInventoryQuery{
		Repository: fake.publication.Repository,
	}, 1, first.Pagination.NextCursor); humaStatus(err) != http.StatusConflict {
		t.Fatalf("summary-transition cursor = %v, want 409", err)
	}

	fake = newServiceDirectoryTestStore()
	service = NewServiceDirectoryService(Options{Store: fake})
	first, err = service.List(t.Context(), ServiceInventoryQuery{
		Repository: fake.publication.Repository,
	}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	for index := range fake.entries {
		if fake.entries[index].State.ServiceKey == "orders" {
			fake.entries[index].State.Incarnation++
		}
	}
	// The production store rejects this at its seek boundary; the fake mirrors
	// that explicit invariant for the transport test.
	decoded, err := decodeServiceInventoryCursor(
		first.Pagination.NextCursor,
		digestJSON(struct {
			Schema string                `json:"schema"`
			Order  string                `json:"order"`
			Query  ServiceInventoryQuery `json:"query"`
		}{serviceInventorySchema, serviceInventoryOrder, ServiceInventoryQuery{
			Repository: fake.publication.Repository,
		}}),
		catalogVisibilityContext(t.Context(), service.opts, []store.Repo{
			fake.repositories[fake.publication.Repository],
		}),
	)
	if err != nil || decoded.AfterIncarnation == fake.entries[0].State.Incarnation {
		t.Fatalf("cursor incarnation binding = %+v, err %v", decoded, err)
	}
}

func TestServiceDirectorySparseFilterContinuesAfterEmptyPage(t *testing.T) {
	fake := newServiceDirectoryTestStore()
	fake.scanLimit = 1
	fake.entries[1].State.Status = servicecatalog.StatusCurrent
	service := NewServiceDirectoryService(Options{Store: fake})
	query := ServiceInventoryQuery{
		Repository: fake.publication.Repository,
		Status:     servicecatalog.StatusCurrent,
	}
	first, err := service.List(t.Context(), query, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Services) != 0 || first.Pagination.NextCursor == "" {
		t.Fatalf("sparse first page = %+v", first)
	}
	second, err := service.List(t.Context(), query, 1, first.Pagination.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Services) != 1 || second.Services[0].Key != "payments" ||
		second.Pagination.NextCursor != "" {
		t.Fatalf("sparse second page = %+v", second)
	}
}

func TestServiceDirectoryOpenAPIAndCapabilityRegistration(t *testing.T) {
	fake := newServiceDirectoryTestStore()
	enabled := New(Options{
		Version: "test", Store: fake,
		Principal: func(context.Context) string { return "user:test" },
	})
	code, openAPI := serviceDirectoryHTTP(t, enabled, "/api/openapi.json")
	if code != http.StatusOK {
		t.Fatalf("enabled OpenAPI = %d %s", code, openAPI)
	}
	for _, expected := range []string{
		`"/api/services"`, `"/api/service"`, `"ServiceInventory"`,
		`"ServiceDetail"`, `"memberships"`, `"catalog_generation"`,
	} {
		if !strings.Contains(openAPI, expected) {
			t.Errorf("enabled OpenAPI omitted %s", expected)
		}
	}
	code, version := serviceDirectoryHTTP(t, enabled, "/api/version")
	if code != http.StatusOK || !strings.Contains(version, serviceDirectoryCapability) {
		t.Fatalf("enabled capability = %d %s", code, version)
	}

	dark := New(Options{Version: "test"})
	_, darkOpenAPI := serviceDirectoryHTTP(t, dark, "/api/openapi.json")
	if strings.Contains(darkOpenAPI, `"/api/services"`) ||
		strings.Contains(darkOpenAPI, `"/api/service"`) {
		t.Fatalf("dark OpenAPI exposed service directory: %s", darkOpenAPI)
	}
}

func TestServiceDirectoryBounds(t *testing.T) {
	if _, _, err := validateServiceInventoryQuery(
		ServiceInventoryQuery{}, serviceMaxPageSize+1,
	); humaStatus(err) != http.StatusBadRequest {
		t.Fatalf("oversized page = %v, want 400", err)
	}
	memberships := make([]servicecatalog.Membership, serviceMembershipRecordLimit+1)
	for index := range memberships {
		memberships[index] = servicecatalog.Membership{
			ServiceKey: "orders", Path: fmt.Sprintf("p/%04d", index),
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		}
	}
	if _, err := projectServiceRow(store.ServiceStateEntry{
		State:      servicecatalog.ServiceState{ServiceKey: "orders"},
		Projection: &servicecatalog.ServiceProjection{Memberships: memberships},
	}); err == nil {
		t.Fatal("oversized membership projection succeeded")
	}
	memberships = memberships[:servicecatalog.MaxServicePaths+1]
	for index := range memberships {
		memberships[index].Path = fmt.Sprintf("distinct/%04d", index)
	}
	if _, err := projectServiceRow(store.ServiceStateEntry{
		State:      servicecatalog.ServiceState{ServiceKey: "orders"},
		Projection: &servicecatalog.ServiceProjection{Memberships: memberships},
	}); err == nil {
		t.Fatal("oversized distinct-path projection succeeded")
	}
	detail := ServiceDetail{Memberships: make([]ServiceMembership, 17)}
	for index := range detail.Memberships {
		detail.Memberships[index].Path = fmt.Sprintf("%04d-%s", index, strings.Repeat("p", 4091))
	}
	if err := validateServiceDetailPaths(&detail); err == nil {
		t.Fatal("oversized distinct path bytes succeeded")
	}
	if err := validateServiceResponseSize(ServiceDetail{
		Service: Service{Reason: strings.Repeat("x", serviceResponseBytesLimit)},
	}); humaStatus(err) != http.StatusInternalServerError {
		t.Fatalf("oversized response = %v, want 500", err)
	}
}

func serviceDirectoryHTTP(
	t *testing.T,
	handler http.Handler,
	path string,
) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Code, recorder.Body.String()
}

func humaStatus(err error) int {
	var model *huma.ErrorModel
	if errors.As(err, &model) {
		return model.Status
	}
	return 0
}

func newServiceDirectoryTestStore() *serviceDirectoryTestStore {
	repository := "example.com/acme/mono"
	now := time.Date(2026, 8, 5, 18, 0, 0, 0, time.UTC)
	digest := func(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind:   servicecatalog.SourceOperator,
		SourceCommit: strings.Repeat("1", 40),
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "catalog", Version: "v1",
		},
		CatalogDigest: digest("a"), GenerationDigest: digest("b"),
		ControlRevision: 2, PublishedAt: now,
	}
	summary := servicecatalog.RepositoryState{
		Schema: servicecatalog.RepositoryStateSchema, Repository: repository,
		CatalogGeneration:      publication.GenerationDigest,
		CatalogControlRevision: publication.ControlRevision,
		CatalogServiceCount:    2, LiveServiceCount: 2, UnavailableCount: 2,
		SummaryDigest: digest("c"), ControlRevision: 3, UpdatedAt: now,
	}
	entries := make([]store.ServiceStateEntry, 0, 2)
	for index, key := range []string{"orders", "payments"} {
		state := servicecatalog.ServiceState{
			Schema: servicecatalog.ServiceStateSchema, Repository: repository,
			ServiceKey: key, DisplayName: strings.ToUpper(key[:1]) + key[1:],
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
			Successors: []string{}, Incarnation: 1,
			DesiredGeneration:        digest(fmt.Sprintf("%x", index+4)),
			DesiredSourceGeneration:  digest("d"),
			DesiredCatalogGeneration: publication.GenerationDigest,
			Status:                   servicecatalog.StatusUnavailable, StateDigest: digest(fmt.Sprintf("%x", index+6)),
			ControlRevision: 1, ChangedAt: now,
		}
		projection := servicecatalog.ServiceProjection{
			Repository: repository,
			Service: servicecatalog.Service{
				Key: key, DisplayName: state.DisplayName,
				Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
			},
			Memberships: []servicecatalog.Membership{{
				ServiceKey: key, Path: "services/" + key,
				Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
			}},
			SourceGeneration:  state.DesiredSourceGeneration,
			CatalogGeneration: publication.GenerationDigest,
			GenerationDigest:  digest(fmt.Sprintf("%x", index+8)),
		}
		entries = append(entries, store.ServiceStateEntry{State: state, Projection: &projection})
	}
	return &serviceDirectoryTestStore{
		repositories: map[string]store.Repo{
			repository:                {Name: repository},
			"example.com/acme/hidden": {Name: "example.com/acme/hidden"},
		},
		publication: publication, summary: summary, entries: entries,
	}
}
