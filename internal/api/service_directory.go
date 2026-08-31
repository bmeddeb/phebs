package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	serviceDirectoryCapability   = "service-catalog-v2"
	serviceInventorySchema       = "phebs-service-inventory-v1"
	serviceDetailSchema          = "phebs-service-detail-v1"
	serviceCursorSchema          = "phebs-service-inventory-cursor-v2"
	serviceInventoryOrder        = "service_key:asc"
	serviceDefaultPageSize       = 50
	serviceMaxPageSize           = 100
	serviceCursorBytesLimit      = 16 << 10
	serviceResponseBytesLimit    = 1 << 20
	serviceMembershipRoleLimit   = 5
	serviceMembershipRecordLimit = servicecatalog.MaxServicePaths * serviceMembershipRoleLimit
	serviceCatalogViewV3         = "segmented-v3"
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
	v3     *store.ServiceStateV3Reader
	secret [sha256.Size]byte
}

func NewServiceDirectoryService(opts Options) *ServiceDirectoryService {
	states, ok := opts.Store.(serviceDirectoryStore)
	if !ok || states == nil {
		return nil
	}
	service := &ServiceDirectoryService{opts: opts, states: states}
	if _, err := rand.Read(service.secret[:]); err != nil {
		return nil
	}
	return service
}

// NewServiceDirectoryServiceV3 constructs the runtime-dark segmented backend.
// T41.9 owns selecting it for production traffic.
func NewServiceDirectoryServiceV3(
	opts Options,
	reader *store.ServiceStateV3Reader,
) *ServiceDirectoryService {
	if opts.Store == nil || reader == nil {
		return nil
	}
	service := &ServiceDirectoryService{opts: opts, v3: reader}
	if _, err := rand.Read(service.secret[:]); err != nil {
		return nil
	}
	return service
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
	CatalogView            string            `json:"catalog_view,omitempty"`
	MemberRangeDigest      string            `json:"member_range_digest,omitempty"`
}

func (service *ServiceDirectoryService) List(
	ctx context.Context,
	query ServiceInventoryQuery,
	pageSize int,
	encodedCursor string,
) (*ServiceInventory, error) {
	if service == nil || service.states == nil && service.v3 == nil {
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
		Schema   string                `json:"schema"`
		Order    string                `json:"order"`
		Query    ServiceInventoryQuery `json:"query"`
		PageSize int                   `json:"page_size"`
	}{serviceInventorySchema, serviceInventoryOrder, query, pageSize})
	cursor, err := service.decodeServiceInventoryCursor(
		encodedCursor, queryDigest, authorization, service.catalogView(),
	)
	if err != nil {
		return nil, err
	}
	after := store.ServiceStatePosition{}
	if cursor != nil {
		after.ServiceKey = cursor.AfterServiceKey
		after.Incarnation = cursor.AfterIncarnation
		after.MemberRangeDigest = cursor.MemberRangeDigest
	}
	var (
		repositoryProjection ServiceRepository
		summary              servicecatalog.RepositoryState
		entries              []store.ServiceStateEntry
		position             store.ServiceStatePosition
		catalogGeneration    string
		catalogRevision      uint64
		v3Page               *store.ServiceStateV3Page
	)
	if service.v3 != nil {
		v3Page, err = service.v3.ListServices(
			ctx, query.Repository, filter, after, pageSize,
		)
		if err != nil {
			return nil, serviceDirectoryReadError("list service inventory", err)
		}
		defer v3Page.Close()
		repositoryProjection = projectServiceRepositoryV3(
			v3Page.Pointer, v3Page.Root, v3Page.Summary,
		)
		summary = v3Page.Summary
		entries = v3Page.Entries
		catalogGeneration = v3Page.Pointer.RootDigest
		catalogRevision = v3Page.Pointer.ControlRevision
		if v3Page.Continuation != nil {
			position = *v3Page.Continuation
		}
	} else {
		var page *store.ServiceStatePage
		page, err = service.states.ListServiceStates(
			ctx, query.Repository, filter, after, pageSize+1,
		)
		if err != nil {
			return nil, serviceDirectoryReadError("list service inventory", err)
		}
		repositoryProjection = projectServiceRepository(page.Publication, page.Summary)
		summary = page.Summary
		catalogGeneration = page.Publication.GenerationDigest
		catalogRevision = page.Publication.ControlRevision
		more := len(page.Entries) > pageSize
		entries = page.Entries
		if more {
			entries = entries[:pageSize]
			last := entries[len(entries)-1].State
			position = store.ServiceStatePosition{
				ServiceKey: last.ServiceKey, Incarnation: last.Incarnation,
			}
		} else if page.Continuation != nil {
			position = *page.Continuation
		}
	}
	if err := validateServiceCursorSnapshot(
		cursor, catalogGeneration, catalogRevision, summary,
	); err != nil {
		return nil, err
	}
	rows := make([]Service, 0, len(entries))
	for _, entry := range entries {
		row, projectionErr := projectServiceRowBounded(entry, service.successorLimit())
		if projectionErr != nil {
			return nil, huma.Error500InternalServerError("project service inventory", projectionErr)
		}
		rows = append(rows, row)
	}
	result := &ServiceInventory{
		SchemaVersion: serviceInventorySchema,
		Repository:    repositoryProjection,
		Filters:       query,
		Services:      rows,
		Pagination: ServicePagination{
			Order: serviceInventoryOrder, PageSize: pageSize, Returned: len(rows),
		},
	}
	if position.ServiceKey != "" {
		result.Pagination.NextCursor, err = service.encodeServiceInventoryCursor(
			serviceInventoryCursor{
				Schema: serviceCursorSchema, QueryDigest: queryDigest,
				Authorization:          authorization,
				CatalogGeneration:      catalogGeneration,
				CatalogControlRevision: catalogRevision,
				SummaryDigest:          summary.SummaryDigest,
				SummaryControlRevision: summary.ControlRevision,
				Order:                  serviceInventoryOrder, AfterServiceKey: position.ServiceKey,
				AfterIncarnation: position.Incarnation, CatalogView: service.catalogView(),
				MemberRangeDigest: position.MemberRangeDigest,
			},
		)
		if err != nil {
			return nil, huma.Error500InternalServerError("encode service inventory cursor", err)
		}
	}
	if err := validateServiceResponseSize(result); err != nil {
		return nil, err
	}
	if err := service.confirmPage(
		ctx, repository, authorization, summary, v3Page,
	); err != nil {
		return nil, err
	}
	return result, nil
}

func (service *ServiceDirectoryService) Detail(
	ctx context.Context,
	repositoryName, serviceKey string,
) (*ServiceDetail, error) {
	if service == nil || service.states == nil && service.v3 == nil {
		return nil, huma.Error503ServiceUnavailable("service catalog unavailable")
	}
	repository, authorization, err := service.authorize(ctx, repositoryName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(serviceKey) == "" {
		return nil, huma.Error404NotFound("service not found")
	}
	var (
		repositoryProjection ServiceRepository
		summary              servicecatalog.RepositoryState
		entry                store.ServiceStateEntry
		v3Read               *store.ServiceStateV3Read
	)
	if service.v3 != nil {
		v3Read, err = service.v3.OpenService(ctx, repository.Name, serviceKey)
		if err != nil {
			return nil, serviceDirectoryReadError("read service detail", err)
		}
		defer v3Read.Close()
		repositoryProjection = projectServiceRepositoryV3(
			v3Read.Pointer, v3Read.Root, v3Read.Summary,
		)
		summary, entry = v3Read.Summary, v3Read.Entry
	} else {
		var read *store.ServiceStateRead
		read, err = service.states.GetServiceStateRead(ctx, repository.Name, serviceKey)
		if err != nil {
			return nil, serviceDirectoryReadError("read service detail", err)
		}
		repositoryProjection = projectServiceRepository(read.Publication, read.Summary)
		summary, entry = read.Summary, read.Entry
	}
	row, err := projectServiceRowBounded(entry, service.successorLimit())
	if err != nil {
		return nil, huma.Error500InternalServerError("project service detail", err)
	}
	detail := &ServiceDetail{
		SchemaVersion: serviceDetailSchema,
		Repository:    repositoryProjection,
		Service:       row,
		Successors:    append([]string{}, entry.State.Successors...),
		Memberships:   projectServiceMemberships(entry.Projection),
	}
	if err := validateServiceDetailPaths(detail, service.successorLimit()); err != nil {
		return nil, huma.Error500InternalServerError("project service detail", err)
	}
	if err := validateServiceResponseSize(detail); err != nil {
		return nil, err
	}
	if err := service.confirmDetail(
		ctx, repository, authorization, summary, v3Read,
	); err != nil {
		return nil, err
	}
	return detail, nil
}

func (service *ServiceDirectoryService) catalogView() string {
	if service != nil && service.v3 != nil {
		return serviceCatalogViewV3
	}
	return ""
}

func (service *ServiceDirectoryService) successorLimit() int {
	if service != nil && service.v3 != nil {
		return servicecatalogv3.MaxServiceSuccessors
	}
	return servicecatalog.MaxSuccessorEdges
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

func (service *ServiceDirectoryService) confirmAuthorization(
	ctx context.Context,
	repository store.Repo,
	authorization VisibilityContext,
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
	return nil
}

func (service *ServiceDirectoryService) confirmPage(
	ctx context.Context,
	repository store.Repo,
	authorization VisibilityContext,
	summary servicecatalog.RepositoryState,
	page *store.ServiceStateV3Page,
) error {
	if err := service.confirmAuthorization(ctx, repository, authorization); err != nil {
		return err
	}
	if service.v3 != nil {
		if err := service.v3.ConfirmPage(ctx, page); err != nil {
			return serviceDirectoryReadError("confirm service inventory", err)
		}
		return nil
	}
	if err := service.states.ConfirmServiceStateSnapshot(
		ctx, repository.Name, summary,
	); err != nil {
		return serviceDirectoryReadError("confirm service inventory", err)
	}
	return nil
}

func (service *ServiceDirectoryService) confirmDetail(
	ctx context.Context,
	repository store.Repo,
	authorization VisibilityContext,
	summary servicecatalog.RepositoryState,
	read *store.ServiceStateV3Read,
) error {
	if err := service.confirmAuthorization(ctx, repository, authorization); err != nil {
		return err
	}
	if service.v3 != nil {
		if err := service.v3.Confirm(ctx, read); err != nil {
			return serviceDirectoryReadError("confirm service detail", err)
		}
		return nil
	}
	if err := service.states.ConfirmServiceStateSnapshot(
		ctx, repository.Name, summary,
	); err != nil {
		return serviceDirectoryReadError("confirm service detail", err)
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

func projectServiceRepositoryV3(
	pointer store.ServiceCatalogV3Pointer,
	root servicecatalogv3.Root,
	summary servicecatalog.RepositoryState,
) ServiceRepository {
	authority := ServiceAuthority{
		Kind: root.Binding.Authority.Kind, ID: root.Binding.Authority.ID,
		Version: root.Binding.Authority.Version,
	}
	if root.Binding.Override != nil {
		authority.OverrideID = root.Binding.Override.ID
		authority.OverrideVersion = root.Binding.Override.Version
	}
	return ServiceRepository{
		Repository: root.Binding.Repository, SourceKind: root.Binding.Source.Kind,
		SourceCommit:      root.Binding.Source.Commit,
		SourceFileCount:   root.Binding.Source.FileCount,
		AcceptedFileCount: root.Binding.Source.AcceptedFileCount,
		UnownedFileCount:  root.Binding.Source.UnownedFileCount,
		Authority:         authority, CatalogDigest: root.LogicalDigest,
		CatalogGeneration:      pointer.RootDigest,
		CatalogControlRevision: pointer.ControlRevision,
		StateControlRevision:   summary.ControlRevision,
		CatalogServiceCount:    summary.CatalogServiceCount,
		LiveServiceCount:       summary.LiveServiceCount, CurrentCount: summary.CurrentCount,
		StaleCount: summary.StaleCount, UnavailableCount: summary.UnavailableCount,
		ConflictCount: summary.ConflictCount, TombstoneCount: summary.TombstoneCount,
		PublishedAt: pointer.PublishedAt, StateUpdatedAt: summary.UpdatedAt,
	}
}

func projectServiceRow(entry store.ServiceStateEntry) (Service, error) {
	return projectServiceRowBounded(entry, servicecatalog.MaxSuccessorEdges)
}

func projectServiceRowBounded(
	entry store.ServiceStateEntry,
	successorLimit int,
) (Service, error) {
	state := entry.State
	if successorLimit < 0 || len(state.Successors) > successorLimit {
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

func validateServiceDetailPaths(detail *ServiceDetail, successorLimit int) error {
	if len(detail.Memberships) > serviceMembershipRecordLimit ||
		successorLimit < 0 || len(detail.Successors) > successorLimit {
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
	catalogGeneration string,
	catalogRevision uint64,
	summary servicecatalog.RepositoryState,
) error {
	if cursor == nil {
		return nil
	}
	if cursor.CatalogGeneration != catalogGeneration ||
		cursor.CatalogControlRevision != catalogRevision ||
		cursor.SummaryDigest != summary.SummaryDigest ||
		cursor.SummaryControlRevision != summary.ControlRevision {
		return huma.Error409Conflict("service inventory cursor is no longer valid")
	}
	return nil
}

func (service *ServiceDirectoryService) decodeServiceInventoryCursor(
	encoded, queryDigest string,
	authorization VisibilityContext,
	catalogView string,
) (*serviceInventoryCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > serviceCursorBytesLimit {
		return nil, huma.Error400BadRequest("service inventory cursor is invalid")
	}
	payload, signature, ok := strings.Cut(encoded, ".")
	if !ok {
		return nil, huma.Error400BadRequest("service inventory cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	provided, signatureErr := base64.RawURLEncoding.DecodeString(signature)
	mac := hmac.New(sha256.New, service.secret[:])
	_, _ = mac.Write(raw)
	if err != nil || signatureErr != nil ||
		base64.RawURLEncoding.EncodeToString(raw) != payload ||
		base64.RawURLEncoding.EncodeToString(provided) != signature ||
		len(raw) > serviceCursorBytesLimit || !json.Valid(raw) ||
		!hmac.Equal(provided, mac.Sum(nil)) {
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
	if cursor.Schema != serviceCursorSchema || cursor.Order != serviceInventoryOrder ||
		cursor.AfterServiceKey == "" || cursor.AfterIncarnation == 0 ||
		(cursor.CatalogView == serviceCatalogViewV3) != (cursor.MemberRangeDigest != "") {
		return nil, huma.Error400BadRequest("service inventory cursor is invalid")
	}
	if cursor.QueryDigest != queryDigest || cursor.Authorization != authorization ||
		cursor.CatalogView != catalogView {
		return nil, huma.Error409Conflict("service inventory cursor is no longer valid")
	}
	return &cursor, nil
}

func (service *ServiceDirectoryService) encodeServiceInventoryCursor(
	cursor serviceInventoryCursor,
) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, service.secret[:])
	_, _ = mac.Write(raw)
	encoded := base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict("service catalog changed; retry")
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("service not found")
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
