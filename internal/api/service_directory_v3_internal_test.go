package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const serviceDirectoryV3TestRepository = "example.com/acme/mono"

type serviceDirectoryV3TestStore struct {
	store.Store
	repository store.Repo
}

func (fake *serviceDirectoryV3TestStore) GetRepo(
	_ context.Context,
	repository string,
) (*store.Repo, error) {
	if repository != fake.repository.Name {
		return nil, store.ErrNotFound
	}
	result := fake.repository
	return &result, nil
}

type serviceDirectoryV3TestSource struct {
	generation servicecatalogv3.Generation
	members    map[string][]byte
	pointer    store.ServiceCatalogV3Pointer
	summary    servicecatalog.RepositoryState
	states     []servicecatalog.ServiceState
	byKey      map[string]servicecatalog.ServiceState

	calls                    []string
	confirms                 int
	missingPointer           bool
	missingRoot              bool
	missingMember            bool
	missingSummary           bool
	rootError                error
	memberError              error
	pointError               error
	listError                error
	confirmErrorAt           int
	confirmError             error
	pointerError             error
	publishAfterFirstConfirm bool
}

func (fake *serviceDirectoryV3TestSource) ReadServiceCatalogV3Root(
	_ context.Context,
	repository, digest string,
) (servicecatalogv3.Root, error) {
	fake.calls = append(fake.calls, "root")
	if fake.rootError != nil {
		return servicecatalogv3.Root{}, fake.rootError
	}
	if fake.missingRoot || repository != fake.pointer.Repository ||
		digest != fake.generation.Root.Digest {
		return servicecatalogv3.Root{}, store.ErrNotFound
	}
	return fake.generation.Root, nil
}

func (fake *serviceDirectoryV3TestSource) ReadServiceCatalogV3Member(
	_ context.Context,
	descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	fake.calls = append(fake.calls, "member")
	if fake.memberError != nil {
		return nil, fake.memberError
	}
	raw, ok := fake.members[descriptor.Digest]
	if fake.missingMember || !ok {
		return nil, store.ErrNotFound
	}
	return append([]byte(nil), raw...), nil
}

func (fake *serviceDirectoryV3TestSource) GetServiceCatalogV3CandidatePointer(
	_ context.Context,
	repository string,
) (store.ServiceCatalogV3Pointer, error) {
	fake.calls = append(fake.calls, "pointer")
	if fake.pointerError != nil {
		return store.ServiceCatalogV3Pointer{}, fake.pointerError
	}
	if fake.missingPointer || repository != fake.pointer.Repository {
		return store.ServiceCatalogV3Pointer{}, store.ErrNotFound
	}
	return fake.pointer, nil
}

func (fake *serviceDirectoryV3TestSource) GetServiceStateV3SummaryPoint(
	_ context.Context,
	repository string,
) (servicecatalog.RepositoryState, error) {
	fake.calls = append(fake.calls, "summary")
	if fake.missingSummary || repository != fake.summary.Repository {
		return servicecatalog.RepositoryState{}, store.ErrNotFound
	}
	return fake.summary, nil
}

func (fake *serviceDirectoryV3TestSource) GetServiceStateV3Point(
	_ context.Context,
	repository, serviceKey string,
) (servicecatalog.ServiceState, error) {
	fake.calls = append(fake.calls, "state:"+serviceKey)
	if fake.pointError != nil {
		return servicecatalog.ServiceState{}, fake.pointError
	}
	state, ok := fake.byKey[serviceKey]
	if !ok || repository != state.Repository {
		return servicecatalog.ServiceState{}, store.ErrNotFound
	}
	state.Successors = append([]string(nil), state.Successors...)
	return state, nil
}

func (fake *serviceDirectoryV3TestSource) ListServiceStateV3Rows(
	_ context.Context,
	repository, after string,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	fake.calls = append(fake.calls, "list:"+after)
	if fake.listError != nil {
		return nil, fake.listError
	}
	result := make([]servicecatalog.ServiceState, 0, min(limit, len(fake.states)))
	for _, state := range fake.states {
		if state.Repository != repository || state.ServiceKey <= after {
			continue
		}
		state.Successors = append([]string(nil), state.Successors...)
		result = append(result, state)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (fake *serviceDirectoryV3TestSource) ListAcceptedServiceStateV3Rows(
	_ context.Context,
	repository string,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	result := make([]servicecatalog.ServiceState, 0, min(limit, len(fake.states)))
	for _, state := range fake.states {
		if state.Repository != repository || state.Removed ||
			state.Disposition != servicecatalog.DispositionAccepted {
			continue
		}
		state.Successors = append([]string(nil), state.Successors...)
		result = append(result, state)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (fake *serviceDirectoryV3TestSource) ConfirmServiceStateV3Snapshot(
	_ context.Context,
	pointer store.ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) error {
	fake.calls = append(fake.calls, "confirm")
	fake.confirms++
	if fake.confirmError != nil && fake.confirms == fake.confirmErrorAt {
		return fake.confirmError
	}
	if pointer != fake.pointer || summary != fake.summary {
		return store.ErrConflict
	}
	if fake.publishAfterFirstConfirm && fake.confirms == 1 {
		fake.pointer.ControlRevision++
	}
	return nil
}

type serviceDirectoryV3TestFixture struct {
	store  *serviceDirectoryV3TestStore
	source *serviceDirectoryV3TestSource
	reader *store.ServiceStateV3Reader
}

func newServiceDirectoryV3TestFixture(
	t *testing.T,
	services []servicecatalog.Service,
) *serviceDirectoryV3TestFixture {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "v3-test", Version: "v1",
	}
	memberships := make([]servicecatalog.Membership, 0, len(services))
	for _, service := range services {
		if service.Disposition != servicecatalog.DispositionAccepted {
			continue
		}
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: service.Key, Path: "services/" + service.Key,
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: serviceDirectoryV3TestRepository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator, Path: "/catalog.json",
			Commit:       "1111111111111111111111111111111111111111",
			CensusDigest: "sha256:" + strings.Repeat("c", 64),
			FileCount:    len(memberships), AcceptedFileCount: len(memberships),
		},
		Authority: authority,
	}, servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: services, Memberships: memberships,
	})
	if err != nil {
		t.Fatal(err)
	}
	members := make(map[string][]byte, len(generation.Members))
	for index, descriptor := range append(
		append([]servicecatalogv3.MemberDescriptor(nil), generation.Root.ServiceMembers...),
		generation.Root.PlacementMembers...,
	) {
		members[descriptor.Digest] = append([]byte(nil), generation.Members[index].Content...)
	}

	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	states := make([]servicecatalog.ServiceState, 0, generation.Root.Services)
	byKey := make(map[string]servicecatalog.ServiceState, generation.Root.Services)
	for index, descriptor := range generation.Root.ServiceMembers {
		projections, projectionErr := servicecatalogv3.ProjectServiceMember(
			generation.Root, descriptor, generation.Members[index].Content,
		)
		if projectionErr != nil {
			t.Fatal(projectionErr)
		}
		for _, projection := range projections {
			desired, desiredErr := servicecatalogv3.ServiceDesiredGeneration(projection, 1)
			if desiredErr != nil {
				t.Fatal(desiredErr)
			}
			state := servicecatalog.ServiceState{
				Schema:     servicecatalogv3.ServiceStateSchema,
				Repository: serviceDirectoryV3TestRepository,
				ServiceKey: projection.Service.Key, DisplayName: projection.Service.DisplayName,
				Disposition: projection.Service.Disposition, Origin: projection.Service.Origin,
				Reason:      projection.Service.Reason,
				Successors:  append([]string(nil), projection.Service.Successors...),
				Incarnation: 1, DesiredGeneration: desired,
				DesiredSourceGeneration:  projection.SourceGeneration,
				DesiredCatalogGeneration: projection.CatalogGeneration,
				Status:                   servicecatalog.StatusUnavailable,
				Removed:                  projection.Removed,
			}
			if state.Removed {
				state.Status = servicecatalog.StatusRemoved
			}
			if err := servicecatalogv3.SetServiceStateDigest(&state); err != nil {
				t.Fatal(err)
			}
			state.ControlRevision = uint64(len(states) + 1)
			state.ChangedAt = now.Add(time.Duration(len(states)) * time.Second)
			states = append(states, state)
			byKey[state.ServiceKey] = state
		}
	}
	summary := servicecatalog.RepositoryState{
		Schema:            servicecatalogv3.RepositoryStateSchema,
		Repository:        serviceDirectoryV3TestRepository,
		CatalogGeneration: generation.Root.Digest, CatalogControlRevision: 7,
		CatalogServiceCount: generation.Root.Services,
	}
	for _, state := range states {
		switch state.Status {
		case servicecatalog.StatusUnavailable:
			summary.LiveServiceCount++
			summary.UnavailableCount++
		case servicecatalog.StatusRemoved:
			summary.TombstoneCount++
		}
	}
	if err := servicecatalogv3.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	summary.ControlRevision = 11
	summary.UpdatedAt = now
	source := &serviceDirectoryV3TestSource{
		generation: generation, members: members,
		pointer: store.ServiceCatalogV3Pointer{
			Repository: serviceDirectoryV3TestRepository,
			RootDigest: generation.Root.Digest, ControlRevision: 7, PublishedAt: now,
		},
		summary: summary, states: states, byKey: byKey,
	}
	reader, err := store.NewServiceStateV3Reader(
		source, servicecatalogv3.NewDefaultReadCache(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &serviceDirectoryV3TestFixture{
		store: &serviceDirectoryV3TestStore{
			repository: store.Repo{Name: serviceDirectoryV3TestRepository},
		},
		source: source, reader: reader,
	}
}

func (fixture *serviceDirectoryV3TestFixture) service(
	opts Options,
) *ServiceDirectoryService {
	opts.Store = fixture.store
	return NewServiceDirectoryServiceV3(opts, fixture.reader)
}

func serviceDirectoryV3Accepted(key string) servicecatalog.Service {
	return servicecatalog.Service{
		Key: key, DisplayName: strings.ToUpper(key[:1]) + key[1:],
		Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
	}
}

func TestServiceDirectoryV3AuthorizationPrecedesAuthorityReads(t *testing.T) {
	fixture := newServiceDirectoryV3TestFixture(t, []servicecatalog.Service{
		serviceDirectoryV3Accepted("orders"),
	})
	service := fixture.service(Options{
		Visible: func(context.Context) func(store.Repo) bool {
			return func(store.Repo) bool { return false }
		},
	})
	_, listErr := service.List(t.Context(), ServiceInventoryQuery{
		Repository: serviceDirectoryV3TestRepository,
	}, serviceMaxPageSize+1, "not-a-cursor")
	_, detailErr := service.Detail(
		t.Context(), serviceDirectoryV3TestRepository, "missing",
	)
	if humaStatus(listErr) != http.StatusNotFound ||
		humaStatus(detailErr) != http.StatusNotFound {
		t.Fatalf("hidden list/detail = %v / %v, want 404 / 404", listErr, detailErr)
	}
	if len(fixture.source.calls) != 0 {
		t.Fatalf("hidden service performed v3 authority reads: %v", fixture.source.calls)
	}
}

func TestServiceDirectoryV3HTTPUsesSharedProjection(t *testing.T) {
	fixture := newServiceDirectoryV3TestFixture(t, []servicecatalog.Service{
		serviceDirectoryV3Accepted("orders"), serviceDirectoryV3Accepted("payments"),
	})
	handler := New(Options{
		Version: "test", Store: fixture.store,
		ServiceDirectory: fixture.service(Options{}),
	})
	code, body := serviceDirectoryHTTP(t, handler,
		"/api/services?repository="+serviceDirectoryV3TestRepository+"&page_size=1")
	var inventory ServiceInventory
	if code != http.StatusOK || json.Unmarshal([]byte(body), &inventory) != nil ||
		len(inventory.Services) != 1 || inventory.Services[0].Key != "orders" ||
		inventory.Pagination.NextCursor == "" {
		t.Fatalf("v3 HTTP inventory = %d %s", code, body)
	}
	code, body = serviceDirectoryHTTP(t, handler,
		"/api/service?repository="+serviceDirectoryV3TestRepository+"&service_key=orders")
	var detail ServiceDetail
	if code != http.StatusOK || json.Unmarshal([]byte(body), &detail) != nil ||
		detail.Service != inventory.Services[0] || len(detail.Memberships) != 1 {
		t.Fatalf("v3 HTTP detail = %d %s", code, body)
	}
}

func TestServiceDirectoryV3CursorSignaturePageSizeAndMemberRange(t *testing.T) {
	fixture := newServiceDirectoryV3TestFixture(t, []servicecatalog.Service{
		serviceDirectoryV3Accepted("orders"), serviceDirectoryV3Accepted("payments"),
	})
	service := fixture.service(Options{})
	query := ServiceInventoryQuery{Repository: serviceDirectoryV3TestRepository}
	first, err := service.List(t.Context(), query, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	payload, signature, ok := strings.Cut(first.Pagination.NextCursor, ".")
	if !ok {
		t.Fatalf("cursor omitted signature: %q", first.Pagination.NextCursor)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var cursor serviceInventoryCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.CatalogView != serviceCatalogViewV3 ||
		cursor.MemberRangeDigest == "" || cursor.AfterServiceKey != "orders" {
		t.Fatalf("v3 cursor binding = %+v", cursor)
	}

	tampered := cursor
	tampered.AfterServiceKey = "payments"
	tamperedRaw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	forged := base64.RawURLEncoding.EncodeToString(tamperedRaw) + "." + signature
	if _, err := service.List(t.Context(), query, 1, forged); humaStatus(err) != http.StatusBadRequest {
		t.Fatalf("signature forgery = %v, want 400", err)
	}
	if _, err := service.List(
		t.Context(), query, 2, first.Pagination.NextCursor,
	); humaStatus(err) != http.StatusConflict {
		t.Fatalf("page-size cursor replay = %v, want 409", err)
	}

	wrongRange := cursor
	wrongRange.MemberRangeDigest = "sha256:" + strings.Repeat("f", 64)
	signedWrongRange, err := service.encodeServiceInventoryCursor(wrongRange)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(
		t.Context(), query, 1, signedWrongRange,
	); humaStatus(err) != http.StatusConflict {
		t.Fatalf("member-range cursor replay = %v, want 409", err)
	}
	fixture.source.pointError = fmt.Errorf("transient state read")
	if _, err := service.List(
		t.Context(), query, 1, first.Pagination.NextCursor,
	); humaStatus(err) != http.StatusInternalServerError {
		t.Fatalf("cursor state read failure = %v, want 500", err)
	}
	fixture.source.pointError = store.ErrInvalidServiceStateV3
	if _, err := service.List(
		t.Context(), query, 1, first.Pagination.NextCursor,
	); humaStatus(err) != http.StatusConflict {
		t.Fatalf("malformed cursor state = %v, want 409", err)
	}
	fixture.source.pointError = nil
	second, err := service.List(t.Context(), query, 1, first.Pagination.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Services) != 1 || second.Services[0].Key != "payments" ||
		second.Pagination.NextCursor != "" {
		t.Fatalf("valid v3 continuation = %+v", second)
	}
}

func TestServiceDirectoryV3FinalAuthorizationAndPublicationFences(t *testing.T) {
	services := []servicecatalog.Service{
		serviceDirectoryV3Accepted("orders"), serviceDirectoryV3Accepted("payments"),
	}
	t.Run("reauthorization", func(t *testing.T) {
		fixture := newServiceDirectoryV3TestFixture(t, services)
		visibilityChecks := 0
		service := fixture.service(Options{
			Visible: func(context.Context) func(store.Repo) bool {
				return func(store.Repo) bool {
					visibilityChecks++
					return visibilityChecks == 1
				}
			},
		})
		_, err := service.List(t.Context(), ServiceInventoryQuery{
			Repository: serviceDirectoryV3TestRepository,
		}, 1, "")
		if humaStatus(err) != http.StatusNotFound || visibilityChecks != 2 {
			t.Fatalf("final reauthorization = %v, checks %d", err, visibilityChecks)
		}
		if fixture.source.confirms != 1 {
			t.Fatalf("v3 confirms after revocation = %d, want reader-only confirm", fixture.source.confirms)
		}
	})
	t.Run("publication", func(t *testing.T) {
		fixture := newServiceDirectoryV3TestFixture(t, services)
		fixture.source.publishAfterFirstConfirm = true
		service := fixture.service(Options{})
		_, err := service.List(t.Context(), ServiceInventoryQuery{
			Repository: serviceDirectoryV3TestRepository,
		}, 1, "")
		if humaStatus(err) != http.StatusConflict {
			t.Fatalf("concurrent publication = %v, want 409", err)
		}
		if fixture.source.confirms != 2 {
			t.Fatalf("v3 confirms = %d, want reader and response fences", fixture.source.confirms)
		}
	})
	t.Run("authority disappears", func(t *testing.T) {
		fixture := newServiceDirectoryV3TestFixture(t, services)
		fixture.source.confirmErrorAt = 2
		fixture.source.confirmError = store.ErrNotFound
		_, err := fixture.service(Options{}).List(t.Context(), ServiceInventoryQuery{
			Repository: serviceDirectoryV3TestRepository,
		}, 1, "")
		if humaStatus(err) != http.StatusConflict {
			t.Fatalf("final authority disappearance = %v, want 409", err)
		}
	})
}

func TestServiceDirectoryV3DetailReturnsMaximumSuccessors(t *testing.T) {
	successors := make([]string, servicecatalogv3.MaxServiceSuccessors)
	services := make([]servicecatalog.Service, 0, len(successors)+1)
	for index := range successors {
		key := fmt.Sprintf("successor-%03d", index)
		successors[index] = key
		services = append(services, serviceDirectoryV3Accepted(key))
	}
	services = append(services, servicecatalog.Service{
		Key: "legacy", DisplayName: "Legacy",
		Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase,
		Reason: "replaced", Successors: append([]string(nil), successors...),
	})
	fixture := newServiceDirectoryV3TestFixture(t, services)
	detail, err := fixture.service(Options{}).Detail(
		t.Context(), serviceDirectoryV3TestRepository, "legacy",
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Service.SuccessorCount != servicecatalogv3.MaxServiceSuccessors ||
		len(detail.Successors) != servicecatalogv3.MaxServiceSuccessors {
		t.Fatalf("successor detail counts = %d / %d", detail.Service.SuccessorCount, len(detail.Successors))
	}
	for index := range successors {
		if detail.Successors[index] != successors[index] {
			t.Fatalf("successor %d = %q, want %q", index, detail.Successors[index], successors[index])
		}
	}
}

func TestServiceDirectoryV3TenThousandServicePagination(t *testing.T) {
	services := make([]servicecatalog.Service, 10_000)
	for index := range services {
		services[index] = serviceDirectoryV3Accepted(fmt.Sprintf("service-%05d", index))
	}
	fixture := newServiceDirectoryV3TestFixture(t, services)
	service := fixture.service(Options{})
	query := ServiceInventoryQuery{Repository: serviceDirectoryV3TestRepository}
	cursor := ""
	seen := 0
	for {
		page, err := service.List(t.Context(), query, serviceMaxPageSize, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Services) > serviceMaxPageSize {
			t.Fatalf("page %d returned %d services", seen/serviceMaxPageSize, len(page.Services))
		}
		for _, row := range page.Services {
			want := fmt.Sprintf("service-%05d", seen)
			if row.Key != want {
				t.Fatalf("service %d = %q, want %q", seen, row.Key, want)
			}
			seen++
		}
		cursor = page.Pagination.NextCursor
		if cursor == "" {
			break
		}
	}
	if seen != len(services) {
		t.Fatalf("paged services = %d, want %d", seen, len(services))
	}
}

func TestServiceDirectoryV3MissingAuthorityClassification(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		status  int
		prepare func(*serviceDirectoryV3TestSource)
	}{
		{name: "pointer", key: "orders", status: http.StatusNotFound,
			prepare: func(source *serviceDirectoryV3TestSource) { source.missingPointer = true }},
		{name: "malformed pointer", key: "orders", status: http.StatusConflict,
			prepare: func(source *serviceDirectoryV3TestSource) {
				source.pointerError = store.ErrInvalidServiceCatalogV3Candidate
			}},
		{name: "requested service", key: "missing", status: http.StatusNotFound,
			prepare: func(*serviceDirectoryV3TestSource) {}},
		{name: "malformed service state", key: "orders", status: http.StatusConflict,
			prepare: func(source *serviceDirectoryV3TestSource) {
				source.pointError = store.ErrInvalidServiceStateV3
			}},
		{name: "root", key: "orders", status: http.StatusConflict,
			prepare: func(source *serviceDirectoryV3TestSource) { source.missingRoot = true }},
		{name: "real root error", key: "orders", status: http.StatusConflict,
			prepare: func(source *serviceDirectoryV3TestSource) {
				source.rootError = store.ErrInvalidServiceCatalogV3Candidate
			}},
		{name: "member", key: "orders", status: http.StatusConflict,
			prepare: func(source *serviceDirectoryV3TestSource) { source.missingMember = true }},
		{name: "real member error", key: "orders", status: http.StatusConflict,
			prepare: func(source *serviceDirectoryV3TestSource) {
				source.memberError = store.ErrInvalidServiceCatalogV3Candidate
			}},
		{name: "summary", key: "orders", status: http.StatusConflict,
			prepare: func(source *serviceDirectoryV3TestSource) { source.missingSummary = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceDirectoryV3TestFixture(t, []servicecatalog.Service{
				serviceDirectoryV3Accepted("orders"),
			})
			test.prepare(fixture.source)
			_, err := fixture.service(Options{}).Detail(
				t.Context(), serviceDirectoryV3TestRepository, test.key,
			)
			if humaStatus(err) != test.status {
				t.Fatalf("missing %s = %v, want status %d", test.name, err, test.status)
			}
		})
	}
	fixture := newServiceDirectoryV3TestFixture(t, []servicecatalog.Service{
		serviceDirectoryV3Accepted("orders"),
	})
	fixture.source.pointerError = store.ErrInvalidServiceCatalogV3Candidate
	if _, err := fixture.service(Options{}).List(t.Context(), ServiceInventoryQuery{
		Repository: serviceDirectoryV3TestRepository,
	}, 1, ""); humaStatus(err) != http.StatusConflict {
		t.Fatalf("malformed list pointer = %v, want 409", err)
	}
	fixture = newServiceDirectoryV3TestFixture(t, []servicecatalog.Service{
		serviceDirectoryV3Accepted("orders"),
	})
	fixture.source.listError = store.ErrInvalidServiceStateV3
	if _, err := fixture.service(Options{}).List(t.Context(), ServiceInventoryQuery{
		Repository: serviceDirectoryV3TestRepository,
	}, 1, ""); humaStatus(err) != http.StatusConflict {
		t.Fatalf("malformed list rows = %v, want 409", err)
	}
}
