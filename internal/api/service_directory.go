package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	serviceDirectoryCapability   = "service-catalog-v2"
	serviceInventorySchema       = "phebs-service-inventory-v1"
	serviceDetailSchema          = "phebs-service-detail-v1"
	serviceCursorSchema          = "phebs-service-inventory-cursor-v1"
	serviceInventoryOrder        = "service_key:asc"
	serviceDefaultPageSize       = 50
	serviceMaxPageSize           = 100
	serviceCursorBytesLimit      = 16 << 10
	serviceResponseBytesLimit    = 1 << 20
	serviceMembershipRoleLimit   = 5
	serviceMembershipRecordLimit = servicecatalog.MaxServicePaths * serviceMembershipRoleLimit
)

type serviceDirectoryStore interface {
	GetServiceStateRead(context.Context, string, string) (*store.ServiceStateRead, error)
	ConfirmServiceStateSnapshot(
		context.Context,
		string,
		servicecatalog.RepositoryState,
	) error
	ListServiceStates(
		context.Context,
		string,
		store.ServiceStateFilter,
		store.ServiceStatePosition,
		int,
	) (*store.ServiceStatePage, error)
}

// ServiceDirectoryService is the one authorization, cursor, projection, and
// response-boundary implementation shared by HTTP and MCP.
type ServiceDirectoryService struct {
	opts   Options
	states serviceDirectoryStore
}

func NewServiceDirectoryService(opts Options) *ServiceDirectoryService {
	states, ok := opts.Store.(serviceDirectoryStore)
	if !ok || states == nil {
		return nil
	}
	return &ServiceDirectoryService{opts: opts, states: states}
}

// ServiceInventoryQuery is the closed filter set for one authorized
// repository-local inventory.
type ServiceInventoryQuery struct {
	Repository     string `json:"repository"`
	Status         string `json:"status,omitempty"`
	Disposition    string `json:"disposition,omitempty"`
	IncludeRemoved bool   `json:"include_removed,omitempty"`
}

// ServiceAuthority is the bounded repository authority projection repeated on
// inventory and detail responses.
type ServiceAuthority struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	Version         string `json:"version"`
	OverrideID      string `json:"override_id,omitempty"`
	OverrideVersion string `json:"override_version,omitempty"`
}

// ServiceRepository is the exact catalog and lifecycle summary for one
// authorized repository.
type ServiceRepository struct {
	Repository             string           `json:"repository"`
	SourceKind             string           `json:"source_kind"`
	SourceCommit           string           `json:"source_commit"`
	SourceFileCount        int              `json:"source_file_count"`
	AcceptedFileCount      int              `json:"accepted_file_count"`
	UnownedFileCount       int              `json:"unowned_file_count"`
	Authority              ServiceAuthority `json:"authority"`
	CatalogDigest          string           `json:"catalog_digest"`
	CatalogGeneration      string           `json:"catalog_generation"`
	CatalogControlRevision uint64           `json:"catalog_control_revision"`
	StateControlRevision   uint64           `json:"state_control_revision"`
	CatalogServiceCount    int              `json:"catalog_service_count"`
	LiveServiceCount       int              `json:"live_service_count"`
	CurrentCount           int              `json:"current_count"`
	StaleCount             int              `json:"stale_count"`
	UnavailableCount       int              `json:"unavailable_count"`
	ConflictCount          int              `json:"conflict_count"`
	TombstoneCount         int              `json:"tombstone_count"`
	PublishedAt            time.Time        `json:"published_at"`
	StateUpdatedAt         time.Time        `json:"state_updated_at"`
}

// ServiceRoleCounts summarizes memberships without returning paths on a list.
type ServiceRoleCounts struct {
	Primary    int `json:"primary"`
	Supporting int `json:"supporting"`
	Shared     int `json:"shared"`
	Generated  int `json:"generated"`
	Typed      int `json:"typed"`
}

// Service is the common bounded row used by list and detail transports.
type Service struct {
	Repository               string            `json:"repository"`
	Key                      string            `json:"key"`
	DisplayName              string            `json:"display_name"`
	Disposition              string            `json:"disposition"`
	Origin                   string            `json:"origin"`
	Reason                   string            `json:"reason,omitempty"`
	SuccessorCount           int               `json:"successor_count"`
	Incarnation              uint64            `json:"incarnation"`
	DesiredGeneration        string            `json:"desired_generation,omitempty"`
	DesiredSourceGeneration  string            `json:"desired_source_generation,omitempty"`
	DesiredCatalogGeneration string            `json:"desired_catalog_generation,omitempty"`
	ActiveDesiredGeneration  string            `json:"active_desired_generation,omitempty"`
	ActiveSourceGeneration   string            `json:"active_source_generation,omitempty"`
	ActiveCatalogGeneration  string            `json:"active_catalog_generation,omitempty"`
	Status                   string            `json:"status"`
	Removed                  bool              `json:"removed"`
	MembershipCount          int               `json:"membership_count"`
	DistinctPathCount        int               `json:"distinct_path_count"`
	RoleCounts               ServiceRoleCounts `json:"role_counts"`
	StateDigest              string            `json:"state_digest"`
	ControlRevision          uint64            `json:"control_revision"`
	ChangedAt                time.Time         `json:"changed_at"`
}

type ServiceMembership struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

type ServicePagination struct {
	Order      string `json:"order"`
	PageSize   int    `json:"page_size"`
	Returned   int    `json:"returned"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type ServiceInventory struct {
	SchemaVersion string                `json:"schema"`
	Repository    ServiceRepository     `json:"repository"`
	Filters       ServiceInventoryQuery `json:"filters"`
	Services      []Service             `json:"services"`
	Pagination    ServicePagination     `json:"pagination"`
}

type ServiceDetail struct {
	SchemaVersion string              `json:"schema"`
	Repository    ServiceRepository   `json:"repository"`
	Service       Service             `json:"service"`
	Successors    []string            `json:"successors"`
	Memberships   []ServiceMembership `json:"memberships"`
}

type serviceInventoryCursor struct {
	Schema                 string            `json:"schema"`
	QueryDigest            string            `json:"query_digest"`
	Authorization          VisibilityContext `json:"authorization"`
	CatalogGeneration      string            `json:"catalog_generation"`
	CatalogControlRevision uint64            `json:"catalog_control_revision"`
	SummaryDigest          string            `json:"summary_digest"`
	SummaryControlRevision uint64            `json:"summary_control_revision"`
	Order                  string            `json:"order"`
	AfterServiceKey        string            `json:"after_service_key"`
	AfterIncarnation       uint64            `json:"after_incarnation"`
	Checksum               string            `json:"checksum"`
}

func (service *ServiceDirectoryService) List(
	ctx context.Context,
	query ServiceInventoryQuery,
	pageSize int,
	encodedCursor string,
) (*ServiceInventory, error) {
	if service == nil || service.states == nil {
		return nil, huma.Error503ServiceUnavailable("service catalog unavailable")
	}
	repository, authorization, err := service.authorize(ctx, query.Repository)
	if err != nil {
		return nil, err
	}
	query.Repository = repository.Name
	pageSize, filter, err := validateServiceInventoryQuery(query, pageSize)
	if err != nil {
		return nil, err
	}
	queryDigest := digestJSON(struct {
		Schema string                `json:"schema"`
		Order  string                `json:"order"`
		Query  ServiceInventoryQuery `json:"query"`
	}{serviceInventorySchema, serviceInventoryOrder, query})
	cursor, err := decodeServiceInventoryCursor(
		encodedCursor, queryDigest, authorization,
	)
	if err != nil {
		return nil, err
	}
	after := store.ServiceStatePosition{}
	if cursor != nil {
		after.ServiceKey = cursor.AfterServiceKey
		after.Incarnation = cursor.AfterIncarnation
	}
	page, err := service.states.ListServiceStates(
		ctx, query.Repository, filter, after, pageSize+1,
	)
	if err != nil {
		return nil, serviceDirectoryReadError("list service inventory", err)
	}
	if err := validateServiceCursorSnapshot(cursor, page); err != nil {
		return nil, err
	}
	more := len(page.Entries) > pageSize
	entries := page.Entries
	if more {
		entries = entries[:pageSize]
	}
	rows := make([]Service, 0, len(entries))
	for _, entry := range entries {
		row, projectionErr := projectServiceRow(entry)
		if projectionErr != nil {
			return nil, huma.Error500InternalServerError("project service inventory", projectionErr)
		}
		rows = append(rows, row)
	}
	result := &ServiceInventory{
		SchemaVersion: serviceInventorySchema,
		Repository:    projectServiceRepository(page.Publication, page.Summary),
		Filters:       query,
		Services:      rows,
		Pagination: ServicePagination{
			Order: serviceInventoryOrder, PageSize: pageSize, Returned: len(rows),
		},
	}
	position := store.ServiceStatePosition{}
	if more {
		last := entries[len(entries)-1].State
		position = store.ServiceStatePosition{
			ServiceKey: last.ServiceKey, Incarnation: last.Incarnation,
		}
	} else if page.Continuation != nil {
		position = *page.Continuation
	}
	if position.ServiceKey != "" {
		result.Pagination.NextCursor, err = encodeServiceInventoryCursor(
			serviceInventoryCursor{
				Schema: serviceCursorSchema, QueryDigest: queryDigest,
				Authorization:          authorization,
				CatalogGeneration:      page.Publication.GenerationDigest,
				CatalogControlRevision: page.Publication.ControlRevision,
				SummaryDigest:          page.Summary.SummaryDigest,
				SummaryControlRevision: page.Summary.ControlRevision,
				Order:                  serviceInventoryOrder, AfterServiceKey: position.ServiceKey,
				AfterIncarnation: position.Incarnation,
			},
		)
		if err != nil {
			return nil, huma.Error500InternalServerError("encode service inventory cursor", err)
		}
	}
	if err := service.confirm(ctx, repository, authorization, page.Summary); err != nil {
		return nil, err
	}
	if err := validateServiceResponseSize(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (service *ServiceDirectoryService) Detail(
	ctx context.Context,
	repositoryName, serviceKey string,
) (*ServiceDetail, error) {
	if service == nil || service.states == nil {
		return nil, huma.Error503ServiceUnavailable("service catalog unavailable")
	}
	repository, authorization, err := service.authorize(ctx, repositoryName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(serviceKey) == "" {
		return nil, huma.Error404NotFound("service not found")
	}
	read, err := service.states.GetServiceStateRead(ctx, repository.Name, serviceKey)
	if err != nil {
		return nil, serviceDirectoryReadError("read service detail", err)
	}
	row, err := projectServiceRow(read.Entry)
	if err != nil {
		return nil, huma.Error500InternalServerError("project service detail", err)
	}
	detail := &ServiceDetail{
		SchemaVersion: serviceDetailSchema,
		Repository:    projectServiceRepository(read.Publication, read.Summary),
		Service:       row,
		Successors:    append([]string{}, read.Entry.State.Successors...),
		Memberships:   projectServiceMemberships(read.Entry.Projection),
	}
	if err := validateServiceDetailPaths(detail); err != nil {
		return nil, huma.Error500InternalServerError("project service detail", err)
	}
	if err := service.confirm(ctx, repository, authorization, read.Summary); err != nil {
		return nil, err
	}
	if err := validateServiceResponseSize(detail); err != nil {
		return nil, err
	}
	return detail, nil
}

func (service *ServiceDirectoryService) authorize(
	ctx context.Context,
	repositoryName string,
) (store.Repo, VisibilityContext, error) {
	allow := repoFilter(ctx, service.opts)
	if reponame.Validate(repositoryName) != nil || service.opts.Store == nil {
		return store.Repo{}, VisibilityContext{}, huma.Error404NotFound("service catalog not found")
	}
	repository, err := service.opts.Store.GetRepo(ctx, repositoryName)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Repo{}, VisibilityContext{}, huma.Error500InternalServerError(
			"authorize service catalog", err,
		)
	}
	if err == nil && repository == nil {
		return store.Repo{}, VisibilityContext{}, huma.Error500InternalServerError(
			"authorize service catalog", errors.New("repository lookup returned nil"),
		)
	}
	if err != nil || repository.Deleting ||
		allow != nil && !allow(*repository) {
		return store.Repo{}, VisibilityContext{}, huma.Error404NotFound("service catalog not found")
	}
	return *repository,
		catalogVisibilityContext(ctx, service.opts, []store.Repo{*repository}), nil
}

func (service *ServiceDirectoryService) confirm(
	ctx context.Context,
	repository store.Repo,
	authorization VisibilityContext,
	summary servicecatalog.RepositoryState,
) error {
	confirmedRepository, confirmedAuthorization, err := service.authorize(
		ctx, repository.Name,
	)
	if err != nil {
		return err
	}
	if confirmedRepository.Name != repository.Name || confirmedAuthorization != authorization {
		return huma.Error409Conflict("service catalog authorization changed while building the response; retry")
	}
	if err := service.states.ConfirmServiceStateSnapshot(
		ctx, repository.Name, summary,
	); err != nil {
		return serviceDirectoryReadError("confirm service inventory", err)
	}
	return nil
}

func validateServiceInventoryQuery(
	query ServiceInventoryQuery,
	pageSize int,
) (int, store.ServiceStateFilter, error) {
	if pageSize == 0 {
		pageSize = serviceDefaultPageSize
	}
	if pageSize < 1 || pageSize > serviceMaxPageSize {
		return 0, store.ServiceStateFilter{}, huma.Error400BadRequest("service page size is invalid")
	}
	filter := store.ServiceStateFilter{
		Status: query.Status, Disposition: query.Disposition,
		IncludeRemoved: query.IncludeRemoved,
	}
	validStatus := map[string]bool{
		"": true, servicecatalog.StatusCurrent: true, servicecatalog.StatusStale: true,
		servicecatalog.StatusUnavailable: true, servicecatalog.StatusConflict: true,
		servicecatalog.StatusRemoved: true,
	}
	validDisposition := map[string]bool{
		"": true, servicecatalog.DispositionAccepted: true,
		servicecatalog.DispositionProposal: true, servicecatalog.DispositionConflict: true,
		servicecatalog.DispositionRejected: true,
	}
	if !validStatus[query.Status] || !validDisposition[query.Disposition] ||
		query.Status == servicecatalog.StatusRemoved && !query.IncludeRemoved {
		return 0, store.ServiceStateFilter{}, huma.Error400BadRequest("service inventory filter is invalid")
	}
	return pageSize, filter, nil
}

func projectServiceRepository(
	publication servicecatalog.Publication,
	summary servicecatalog.RepositoryState,
) ServiceRepository {
	authority := ServiceAuthority{
		Kind: publication.Authority.Kind, ID: publication.Authority.ID,
		Version: publication.Authority.Version,
	}
	if publication.Override != nil {
		authority.OverrideID = publication.Override.ID
		authority.OverrideVersion = publication.Override.Version
	}
	return ServiceRepository{
		Repository: publication.Repository, SourceKind: publication.SourceKind,
		SourceCommit:           publication.SourceCommit,
		SourceFileCount:        publication.SourceFileCount,
		AcceptedFileCount:      publication.AcceptedFileCount,
		UnownedFileCount:       publication.UnownedFileCount,
		Authority:              authority,
		CatalogDigest:          publication.CatalogDigest,
		CatalogGeneration:      publication.GenerationDigest,
		CatalogControlRevision: publication.ControlRevision,
		StateControlRevision:   summary.ControlRevision,
		CatalogServiceCount:    summary.CatalogServiceCount,
		LiveServiceCount:       summary.LiveServiceCount, CurrentCount: summary.CurrentCount,
		StaleCount: summary.StaleCount, UnavailableCount: summary.UnavailableCount,
		ConflictCount: summary.ConflictCount, TombstoneCount: summary.TombstoneCount,
		PublishedAt: publication.PublishedAt, StateUpdatedAt: summary.UpdatedAt,
	}
}

func projectServiceRow(entry store.ServiceStateEntry) (Service, error) {
	state := entry.State
	if len(state.Successors) > servicecatalog.MaxSuccessorEdges {
		return Service{}, errors.New("service successor limit exceeded")
	}
	row := Service{
		Repository: state.Repository, Key: state.ServiceKey, DisplayName: state.DisplayName,
		Disposition: state.Disposition, Origin: state.Origin, Reason: state.Reason,
		SuccessorCount: len(state.Successors), Incarnation: state.Incarnation,
		DesiredGeneration:        state.DesiredGeneration,
		DesiredSourceGeneration:  state.DesiredSourceGeneration,
		DesiredCatalogGeneration: state.DesiredCatalogGeneration,
		ActiveDesiredGeneration:  state.ActiveDesiredGeneration,
		ActiveSourceGeneration:   state.ActiveSourceGeneration,
		ActiveCatalogGeneration:  state.ActiveCatalogGeneration,
		Status:                   state.Status, Removed: state.Removed, StateDigest: state.StateDigest,
		ControlRevision: state.ControlRevision, ChangedAt: state.ChangedAt,
	}
	if entry.Projection == nil {
		return row, nil
	}
	if len(entry.Projection.Memberships) > serviceMembershipRecordLimit {
		return Service{}, errors.New("service membership record limit exceeded")
	}
	paths := make(map[string]struct{}, servicecatalog.MaxServicePaths)
	pathBytes := 0
	for _, membership := range entry.Projection.Memberships {
		row.MembershipCount++
		if _, seen := paths[membership.Path]; !seen {
			if len(paths) == servicecatalog.MaxServicePaths ||
				pathBytes+len(membership.Path) > servicecatalog.MaxServicePathBytes {
				return Service{}, errors.New("service membership path limit exceeded")
			}
			paths[membership.Path] = struct{}{}
			pathBytes += len(membership.Path)
		}
		switch membership.Role {
		case servicecatalog.RolePrimary:
			row.RoleCounts.Primary++
		case servicecatalog.RoleSupporting:
			row.RoleCounts.Supporting++
		case servicecatalog.RoleShared:
			row.RoleCounts.Shared++
		case servicecatalog.RoleGenerated:
			row.RoleCounts.Generated++
		case servicecatalog.RoleTyped:
			row.RoleCounts.Typed++
		default:
			return Service{}, fmt.Errorf("unsupported service membership role %q", membership.Role)
		}
	}
	row.DistinctPathCount = len(paths)
	return row, nil
}

func projectServiceMemberships(
	projection *servicecatalog.ServiceProjection,
) []ServiceMembership {
	if projection == nil {
		return []ServiceMembership{}
	}
	memberships := make([]ServiceMembership, 0, len(projection.Memberships))
	for _, membership := range projection.Memberships {
		memberships = append(memberships, ServiceMembership{
			Path: membership.Path, Role: membership.Role, Origin: membership.Origin,
		})
	}
	return memberships
}

func validateServiceDetailPaths(detail *ServiceDetail) error {
	if len(detail.Memberships) > serviceMembershipRecordLimit ||
		len(detail.Successors) > servicecatalog.MaxSuccessorEdges {
		return errors.New("service detail collection limit exceeded")
	}
	paths := make(map[string]struct{}, servicecatalog.MaxServicePaths)
	pathBytes := 0
	for _, membership := range detail.Memberships {
		if _, seen := paths[membership.Path]; !seen {
			paths[membership.Path] = struct{}{}
			pathBytes += len(membership.Path)
		}
	}
	if len(paths) > servicecatalog.MaxServicePaths || pathBytes > servicecatalog.MaxServicePathBytes {
		return errors.New("service detail path limit exceeded")
	}
	return nil
}

func validateServiceCursorSnapshot(
	cursor *serviceInventoryCursor,
	page *store.ServiceStatePage,
) error {
	if cursor == nil {
		return nil
	}
	if cursor.CatalogGeneration != page.Publication.GenerationDigest ||
		cursor.CatalogControlRevision != page.Publication.ControlRevision ||
		cursor.SummaryDigest != page.Summary.SummaryDigest ||
		cursor.SummaryControlRevision != page.Summary.ControlRevision {
		return huma.Error409Conflict("service inventory cursor is no longer valid")
	}
	return nil
}

func decodeServiceInventoryCursor(
	encoded, queryDigest string,
	authorization VisibilityContext,
) (*serviceInventoryCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > serviceCursorBytesLimit {
		return nil, huma.Error400BadRequest("service inventory cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > serviceCursorBytesLimit || !json.Valid(raw) {
		return nil, huma.Error400BadRequest("service inventory cursor is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor serviceInventoryCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, huma.Error400BadRequest("service inventory cursor is invalid")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, huma.Error400BadRequest("service inventory cursor is invalid")
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if cursor.Schema != serviceCursorSchema || cursor.QueryDigest != queryDigest ||
		cursor.Authorization != authorization || cursor.Order != serviceInventoryOrder ||
		cursor.AfterServiceKey == "" || cursor.AfterIncarnation == 0 || checksum == "" ||
		checksum != digestJSON(cursor) {
		return nil, huma.Error409Conflict("service inventory cursor is no longer valid")
	}
	cursor.Checksum = checksum
	return &cursor, nil
}

func encodeServiceInventoryCursor(cursor serviceInventoryCursor) (string, error) {
	cursor.Checksum = ""
	cursor.Checksum = digestJSON(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(raw) > serviceCursorBytesLimit || len(encoded) > serviceCursorBytesLimit {
		return "", errors.New("service inventory cursor exceeded its limit")
	}
	return encoded, nil
}

func validateServiceResponseSize(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return huma.Error500InternalServerError("encode service response", err)
	}
	if len(encoded) > serviceResponseBytesLimit {
		return huma.Error500InternalServerError(
			"encode service response", errors.New("service response exceeded its limit"),
		)
	}
	return nil
}

func serviceDirectoryReadError(operation string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("service not found")
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict("service catalog changed; retry")
	default:
		return huma.Error500InternalServerError(operation, err)
	}
}

func registerServiceDirectoryAPI(api huma.API, opts Options) {
	service := opts.ServiceDirectory
	if service == nil {
		return
	}
	type listInput struct {
		Repository     string `query:"repository" required:"true"`
		Status         string `query:"status"`
		Disposition    string `query:"disposition"`
		IncludeRemoved bool   `query:"include_removed"`
		PageSize       int    `query:"page_size"`
		Cursor         string `query:"cursor"`
	}
	type listOutput struct {
		Body ServiceInventory
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-services", Method: http.MethodGet,
		Path: "/api/services", Summary: "List one authorized repository's services",
	}, func(ctx context.Context, input *listInput) (*listOutput, error) {
		result, err := service.List(ctx, ServiceInventoryQuery{
			Repository: input.Repository, Status: input.Status,
			Disposition: input.Disposition, IncludeRemoved: input.IncludeRemoved,
		}, input.PageSize, input.Cursor)
		if err != nil {
			return nil, err
		}
		return &listOutput{Body: *result}, nil
	})

	type detailInput struct {
		Repository string `query:"repository" required:"true"`
		ServiceKey string `query:"service_key" required:"true"`
	}
	type detailOutput struct {
		Body ServiceDetail
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-service", Method: http.MethodGet,
		Path: "/api/service", Summary: "Read one exact authorized service",
	}, func(ctx context.Context, input *detailInput) (*detailOutput, error) {
		result, err := service.Detail(ctx, input.Repository, input.ServiceKey)
		if err != nil {
			return nil, err
		}
		return &detailOutput{Body: *result}, nil
	})
}
