package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	serviceRelationshipsCapability  = "service-relationships-v1"
	relationshipPageSchema          = "phebs-service-relationship-page-v1"
	relationshipSnapshotSchema      = "phebs-service-relationship-snapshot-v1"
	relationshipComparisonSchema    = "phebs-service-relationship-comparison-v1"
	relationshipCitationSchema      = "phebs-service-relationship-citation-v1"
	relationshipCursorSchema        = "phebs-service-relationship-cursor-v1"
	relationshipDefaultPage         = 50
	relationshipMaxPage             = 100
	relationshipMaxRepositories     = 32
	relationshipMaxListReferences   = 20_000
	relationshipMaxCompareRefs      = 40_000
	relationshipMaxBindings         = 8
	relationshipMaxBindingEntries   = 80_000
	relationshipBindingLifetime     = 5 * time.Minute
	relationshipResponseBytes       = 2 << 20
	relationshipTokenBytes          = 16 << 10
	relationshipReadConcurrency     = 8
	relationshipCitationConcurrency = 2
)

// RelationshipQuery is one closed service-local query projected across an
// explicitly selected or complete visible repository set.
type RelationshipQuery struct {
	Repositories []string `json:"repositories"`
	ServiceKey   string   `json:"service_key"`
	View         string   `json:"view"` // all | dependencies | callers | topics
	Kind         string   `json:"kind,omitempty"`
	Plane        string   `json:"plane,omitempty"`
	LookupKey    string   `json:"lookup_key,omitempty"`
}

type RelationshipComparisonQuery struct {
	Repository       string `json:"repository"`
	ServiceKey       string `json:"service_key"`
	BeforeGeneration string `json:"before_generation"`
	BeforeRootDigest string `json:"before_root_digest"`
	AfterGeneration  string `json:"after_generation"`
	AfterRootDigest  string `json:"after_root_digest"`
	View             string `json:"view"`
	Kind             string `json:"kind,omitempty"`
	Plane            string `json:"plane,omitempty"`
	LookupKey        string `json:"lookup_key,omitempty"`
}

type RelationshipRootReceipt struct {
	Repository          string                            `json:"repository"`
	State               string                            `json:"state"` // complete | empty | failed | unavailable
	Reason              string                            `json:"reason,omitempty"`
	RootSchema          string                            `json:"root_schema,omitempty"`
	Generation          string                            `json:"generation,omitempty"`
	RootDigest          string                            `json:"root_digest,omitempty"`
	AuthorityDigest     string                            `json:"authority_digest,omitempty"`
	Authority           *RelationshipAuthority            `json:"authority,omitempty"`
	Unavailable         *RelationshipUnavailableAuthority `json:"unavailable,omitempty"`
	ServiceKey          string                            `json:"service_key"`
	ServiceIncarnation  uint64                            `json:"service_incarnation,omitempty"`
	ServiceGeneration   string                            `json:"service_generation,omitempty"`
	ReferenceCount      int                               `json:"reference_count"`
	RepositoryComplete  bool                              `json:"repository_complete,omitempty"`
	AllServicesComplete bool                              `json:"all_services_complete,omitempty"`
	FailedServiceCount  int                               `json:"failed_service_count,omitempty"`
}

type RelationshipAuthority struct {
	Repository                  string                         `json:"repository"`
	CatalogGenerationDigest     string                         `json:"catalog_generation_digest"`
	CatalogDigest               string                         `json:"catalog_digest"`
	CatalogSourceGeneration     string                         `json:"catalog_source_generation"`
	ServiceStateSetDigest       string                         `json:"service_state_set_digest"`
	ServiceStateSummaryDigest   string                         `json:"service_state_summary_digest,omitempty"`
	ServiceStateControlRevision uint64                         `json:"service_state_control_revision,omitempty"`
	ObservationGenerationDigest string                         `json:"observation_generation_digest"`
	ObservationManifestDigest   string                         `json:"observation_manifest_digest"`
	ObservationSourceDigest     string                         `json:"observation_source_digest"`
	ResolverGenerationDigest    string                         `json:"resolver_generation_digest"`
	ResolverRootDigest          string                         `json:"resolver_root_digest"`
	RPCGenerationDigest         string                         `json:"rpc_generation_digest"`
	RPCRootDigest               string                         `json:"rpc_root_digest"`
	KafkaGenerationDigest       string                         `json:"kafka_generation_digest"`
	KafkaRootDigest             string                         `json:"kafka_root_digest"`
	PolicyDigest                string                         `json:"policy_digest"`
	Upstream                    *RelationshipUpstreamAuthority `json:"upstream,omitempty"`
}

// RelationshipUpstreamAuthority is the complete source-free T40 generation
// seam. It is intentionally repeated in product receipts so a v2 relationship
// generation cannot be presented as if the legacy observation triplet alone
// were its authority.
type RelationshipUpstreamAuthority struct {
	Schema               string                           `json:"schema"`
	Repository           string                           `json:"repository"`
	Observation          RelationshipObservationAuthority `json:"observation"`
	RequiredDomainCount  int                              `json:"required_domain_count"`
	PublishedDomainCount int                              `json:"published_domain_count"`
	Gaps                 []RelationshipDomainGapAuthority `json:"gaps"`
	Digest               string                           `json:"digest"`
}

type RelationshipObservationAuthority struct {
	Version                     string `json:"version"`
	Repository                  string `json:"repository"`
	SourceGenerationDigest      string `json:"source_generation_digest"`
	SourceRootDigest            string `json:"source_root_digest"`
	ObservationGenerationDigest string `json:"observation_generation_digest"`
	ObservationRootDigest       string `json:"observation_root_digest"`
	PartitionPolicyDigest       string `json:"partition_policy_digest"`
	ObservationPolicyDigest     string `json:"observation_policy_digest"`
	InventoryPolicyDigest       string `json:"inventory_policy_digest,omitempty"`
	RecordCount                 int    `json:"record_count"`
	ObservedCount               int    `json:"observed_count"`
}

type RelationshipDomainGapAuthority struct {
	Domain      string `json:"domain"`
	Version     string `json:"version"`
	Disposition string `json:"disposition"`
}

type RelationshipUnavailableAuthority struct {
	Schema   string                        `json:"schema"`
	Reason   string                        `json:"reason"`
	Digest   string                        `json:"digest"`
	Upstream RelationshipUpstreamAuthority `json:"upstream"`
}

type RelationshipRoleClaim struct {
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

type RelationshipServiceClaim struct {
	ServiceKey  string                  `json:"service_key"`
	Disposition string                  `json:"disposition"`
	Roles       []RelationshipRoleClaim `json:"roles"`
}

type RelationshipPlacement struct {
	Path    string                     `json:"path"`
	Unowned bool                       `json:"unowned"`
	Claims  []RelationshipServiceClaim `json:"claims"`
}

type RelationshipSpan struct {
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type RelationshipEvidence struct {
	Kind                  string           `json:"kind"`
	Plane                 string           `json:"plane"`
	Class                 string           `json:"class"`
	Reason                string           `json:"reason,omitempty"`
	Path                  string           `json:"path"`
	ObjectID              string           `json:"object_id"`
	ContentDigest         string           `json:"content_digest"`
	Span                  RelationshipSpan `json:"span"`
	SourceRole            string           `json:"source_role"`
	Operation             string           `json:"operation,omitempty"`
	CandidateOperations   []string         `json:"candidate_operations,omitempty"`
	DeclarationPath       string           `json:"declaration_path,omitempty"`
	DeclarationLineage    string           `json:"declaration_lineage,omitempty"`
	ResolverRecordDigests []string         `json:"resolver_record_digests,omitempty"`
	TopicSpelling         string           `json:"topic_spelling,omitempty"`
	GroupIDSpelling       string           `json:"group_id_spelling,omitempty"`
	Library               string           `json:"library,omitempty"`
	Shape                 string           `json:"shape,omitempty"`
	Binding               string           `json:"binding,omitempty"`
	PostingDigest         string           `json:"posting_digest"`
}

type RelationshipProjection struct {
	Kind          string                 `json:"kind"`
	PostingDigest string                 `json:"posting_digest"`
	Class         string                 `json:"class"`
	Plane         string                 `json:"plane"`
	LookupKey     string                 `json:"lookup_key,omitempty"`
	Source        RelationshipPlacement  `json:"source"`
	Target        *RelationshipPlacement `json:"target,omitempty"`
	Digest        string                 `json:"digest"`
}

type RelationshipProofCoverage struct {
	SchemaVersion          string                    `json:"schema"`
	Visibility             VisibilityContext         `json:"visibility"`
	VisibleRepositoryCount int                       `json:"visible_repository_count"`
	ExactRootCount         int                       `json:"exact_root_count"`
	GapCount               int                       `json:"gap_count"`
	State                  string                    `json:"state"` // exact | gap
	Reason                 string                    `json:"reason,omitempty"`
	Roots                  []RelationshipRootReceipt `json:"roots"`
	Digest                 string                    `json:"digest"`
}

type RelationshipCoverage struct {
	AuthorizedRepositories int  `json:"authorized_repositories"`
	CompleteRoots          int  `json:"complete_roots"`
	EmptyRoots             int  `json:"empty_roots"`
	FailedRoots            int  `json:"failed_roots"`
	UnavailableRoots       int  `json:"unavailable_roots"`
	ScannedReferences      int  `json:"scanned_references"`
	ReturnedRows           int  `json:"returned_rows"`
	Truncated              bool `json:"truncated"`
}

type RelationshipPagination struct {
	Order      string `json:"order"`
	PageSize   int    `json:"page_size"`
	Returned   int    `json:"returned"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type RelationshipRow struct {
	Repository          string                 `json:"repository"`
	ServiceKey          string                 `json:"service_key"`
	ServiceIncarnation  uint64                 `json:"service_incarnation"`
	ServiceGeneration   string                 `json:"service_generation"`
	Kind                string                 `json:"kind"`
	Plane               string                 `json:"plane"`
	Class               string                 `json:"class"`
	LookupKey           string                 `json:"lookup_key,omitempty"`
	Participation       []string               `json:"participation"`
	CounterpartServices []string               `json:"counterpart_services"`
	ProjectionDigest    string                 `json:"projection_digest"`
	PostingDigest       string                 `json:"posting_digest"`
	Source              RelationshipPlacement  `json:"source"`
	Target              *RelationshipPlacement `json:"target,omitempty"`
	Evidence            RelationshipEvidence   `json:"evidence"`
	Citation            string                 `json:"citation"`
}

type RelationshipPage struct {
	SchemaVersion string                    `json:"schema"`
	Query         RelationshipQuery         `json:"query"`
	RowsState     string                    `json:"rows_state"` // nonempty | empty | gap
	Roots         []RelationshipRootReceipt `json:"roots"`
	Rows          []RelationshipRow         `json:"rows"`
	Coverage      RelationshipCoverage      `json:"coverage"`
	Pagination    RelationshipPagination    `json:"pagination"`
	Caveat        string                    `json:"caveat"`
}

// RelationshipSnapshotRow is the citation-free projection used only by
// server-side compositions such as Workbench. It carries the same exact
// source and root identity as a product row without retaining a process-local
// cursor binding after the composition returns.
type RelationshipSnapshotRow struct {
	Repository          string                 `json:"repository"`
	ServiceKey          string                 `json:"service_key"`
	ServiceIncarnation  uint64                 `json:"service_incarnation"`
	ServiceGeneration   string                 `json:"service_generation"`
	Kind                string                 `json:"kind"`
	Plane               string                 `json:"plane"`
	Class               string                 `json:"class"`
	LookupKey           string                 `json:"lookup_key,omitempty"`
	Participation       []string               `json:"participation"`
	CounterpartServices []string               `json:"counterpart_services"`
	ProjectionDigest    string                 `json:"projection_digest"`
	PostingDigest       string                 `json:"posting_digest"`
	Source              RelationshipPlacement  `json:"source"`
	Target              *RelationshipPlacement `json:"target,omitempty"`
	Evidence            RelationshipEvidence   `json:"evidence"`
}

// RelationshipSnapshotPage is a one-shot, citation-free relationship page.
// It never enters the retained cursor/citation binding cache; callers that
// need an immutable source span must open the dedicated relationship reader.
type RelationshipSnapshotPage struct {
	SchemaVersion string                    `json:"schema"`
	Query         RelationshipQuery         `json:"query"`
	RowsState     string                    `json:"rows_state"`
	Roots         []RelationshipRootReceipt `json:"roots"`
	Rows          []RelationshipSnapshotRow `json:"rows"`
	Coverage      RelationshipCoverage      `json:"coverage"`
	Truncated     bool                      `json:"truncated"`
	Caveat        string                    `json:"caveat"`
}

type RelationshipComparisonRow struct {
	Change string           `json:"change"` // added | removed | unchanged
	Before *RelationshipRow `json:"before,omitempty"`
	After  *RelationshipRow `json:"after,omitempty"`
}

type RelationshipComparisonPage struct {
	SchemaVersion string                      `json:"schema"`
	Query         RelationshipComparisonQuery `json:"query"`
	RowsState     string                      `json:"rows_state"`
	Roots         []RelationshipRootReceipt   `json:"roots"`
	Rows          []RelationshipComparisonRow `json:"rows"`
	Coverage      RelationshipCoverage        `json:"coverage"`
	Pagination    RelationshipPagination      `json:"pagination"`
	Caveat        string                      `json:"caveat"`
}

type RelationshipCitation struct {
	SchemaVersion   string                 `json:"schema"`
	Repository      string                 `json:"repository"`
	RootSchema      string                 `json:"root_schema"`
	Generation      string                 `json:"generation"`
	RootDigest      string                 `json:"root_digest"`
	AuthorityDigest string                 `json:"authority_digest"`
	Projection      RelationshipProjection `json:"projection"`
	Evidence        RelationshipEvidence   `json:"evidence"`
	Content         string                 `json:"content"`
}

type relationshipSource struct {
	repository  store.Repo
	lease       *relationshippublication.Lease
	publication *relationshippublication.Publication
	unavailable *relationshippublication.Unavailable
	root        relationshippublication.Root
	receipt     relationshippublication.ServiceReceipt
	evidence    *relationshippublication.EvidenceReader
	current     bool
	state       string
	reason      string
}

type relationshipLocator struct {
	source    int
	reference relationshippublication.ServiceReference
}

type relationshipEntry struct {
	identity string
	before   *relationshipLocator
	after    *relationshipLocator
}

type relationshipBinding struct {
	id            string
	createdAt     time.Time
	queryDigest   string
	authorization VisibilityContext
	repositories  []store.Repo
	sources       []relationshipSource
	entries       []relationshipEntry
	truncated     bool
	uses          int
	retired       bool
	finalized     bool
}

type relationshipCursor struct {
	Schema      string `json:"schema"`
	Binding     string `json:"binding"`
	QueryDigest string `json:"query_digest"`
	Offset      int    `json:"offset"`
}

type relationshipCitationToken struct {
	Schema     string `json:"schema"`
	Binding    string `json:"binding"`
	Repository string `json:"repository"`
	Source     int    `json:"source"`
	Projection string `json:"projection"`
}

// RelationshipService is the shared authorization, cursor, comparison,
// citation, and response-boundary implementation used by HTTP and MCP.
type RelationshipService struct {
	opts           Options
	cache          *relationshippublication.Cache
	secret         [sha256.Size]byte
	reads          chan struct{}
	citations      chan struct{}
	mu             sync.Mutex
	bindings       map[string]*relationshipBinding
	bindingEntries int
}

type RelationshipQueries interface {
	List(context.Context, RelationshipQuery, int, string) (*RelationshipPage, error)
	Compare(context.Context, RelationshipComparisonQuery, int, string) (*RelationshipComparisonPage, error)
	ReadCitation(context.Context, string) (*RelationshipCitation, error)
	RootCoverage(context.Context, []string) (*RelationshipProofCoverage, error)
}

type RelationshipSnapshotQueries interface {
	Snapshot(context.Context, RelationshipQuery, int) (*RelationshipSnapshotPage, error)
}

func NewRelationshipService(
	opts Options,
	cache *relationshippublication.Cache,
) *RelationshipService {
	if opts.Store == nil || opts.DataDir == "" || cache == nil || !filepath.IsAbs(opts.DataDir) {
		return nil
	}
	service := &RelationshipService{
		opts: opts, cache: cache, reads: make(chan struct{}, relationshipReadConcurrency),
		citations: make(chan struct{}, relationshipCitationConcurrency),
		bindings:  make(map[string]*relationshipBinding),
	}
	if _, err := rand.Read(service.secret[:]); err != nil {
		return nil
	}
	return service
}

func (service *RelationshipService) List(
	ctx context.Context,
	query RelationshipQuery,
	pageSize int,
	cursor string,
) (*RelationshipPage, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable("service relationships unavailable")
	}
	pageSize, err := validateRelationshipQuery(&query, pageSize)
	if err != nil {
		return nil, err
	}
	repositories, authorization, err := service.authorizeRepositories(ctx, query.Repositories)
	if err != nil {
		return nil, err
	}
	query.Repositories = repositoryNames(repositories)
	queryDigest := digestJSON(struct {
		Schema   string            `json:"schema"`
		Query    RelationshipQuery `json:"query"`
		PageSize int               `json:"page_size"`
	}{relationshipPageSchema, query, pageSize})
	finish, err := beginRelationshipRead(ctx, service.reads)
	if err != nil {
		return nil, err
	}
	defer finish()
	var binding *relationshipBinding
	offset := 0
	if cursor == "" {
		binding, err = service.buildCurrentBinding(ctx, query, queryDigest, repositories, authorization)
		if err == nil {
			err = service.admitBinding(binding)
		}
	} else {
		var decoded relationshipCursor
		decoded, err = service.decodeCursor(cursor)
		if err == nil && decoded.QueryDigest != queryDigest {
			err = huma.Error409Conflict("service relationship cursor does not match the query")
		}
		if err == nil {
			binding = service.acquireBinding(decoded.Binding)
			if binding == nil {
				err = huma.Error409Conflict("service relationship cursor expired; restart the query")
			} else {
				defer service.releaseBinding(binding)
				offset = decoded.Offset
			}
		}
	}
	if err != nil {
		if binding != nil && cursor == "" {
			service.releaseUnadmittedBinding(binding)
		}
		return nil, err
	}
	if cursor == "" {
		binding = service.acquireBinding(binding.id)
		if binding == nil {
			return nil, huma.Error503ServiceUnavailable("service relationship binding capacity is busy; retry")
		}
		defer service.releaseBinding(binding)
	}
	rows, next, err := service.relationshipRows(ctx, binding, offset, pageSize)
	if err != nil {
		return nil, relationshipReadError("read service relationships", err)
	}
	page := &RelationshipPage{
		SchemaVersion: relationshipPageSchema, Query: query,
		Roots: relationshipReceipts(binding.sources), Rows: rows,
		Coverage:   relationshipCoverage(binding, len(rows)),
		Pagination: RelationshipPagination{Order: "repository,reference_digest:asc", PageSize: pageSize, Returned: len(rows)},
		Caveat:     relationshipCaveat,
	}
	page.RowsState = relationshipRowsState(page.Roots, len(rows), binding.truncated)
	if next < len(binding.entries) {
		page.Pagination.NextCursor = service.encodeSigned(relationshipCursor{
			Schema: relationshipCursorSchema, Binding: binding.id,
			QueryDigest: queryDigest, Offset: next,
		})
	}
	if err := service.confirmBinding(ctx, binding, authorization); err != nil {
		return nil, err
	}
	if err := validateRelationshipResponse(page); err != nil {
		return nil, err
	}
	return page, nil
}

// Snapshot performs the same authorization, publication, service-state, and
// result-time fences as List, but releases every generation lease before it
// returns. It is deliberately citation-free and has no continuation cursor,
// so a server-side composition cannot consume the process binding budget.
func (service *RelationshipService) Snapshot(
	ctx context.Context,
	query RelationshipQuery,
	pageSize int,
) (*RelationshipSnapshotPage, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable("service relationships unavailable")
	}
	pageSize, err := validateRelationshipQuery(&query, pageSize)
	if err != nil {
		return nil, err
	}
	repositories, authorization, err := service.authorizeRepositories(ctx, query.Repositories)
	if err != nil {
		return nil, err
	}
	query.Repositories = repositoryNames(repositories)
	queryDigest := digestJSON(struct {
		Schema   string            `json:"schema"`
		Query    RelationshipQuery `json:"query"`
		PageSize int               `json:"page_size"`
	}{relationshipSnapshotSchema, query, pageSize})
	finish, err := beginRelationshipRead(ctx, service.reads)
	if err != nil {
		return nil, err
	}
	defer finish()
	binding, err := service.buildCurrentBinding(
		ctx, query, queryDigest, repositories, authorization,
	)
	if err != nil {
		return nil, err
	}
	defer service.releaseUnadmittedBinding(binding)
	rows, next, err := service.relationshipRows(ctx, binding, 0, pageSize)
	if err != nil {
		return nil, relationshipReadError("read service relationship snapshot", err)
	}
	projected := make([]RelationshipSnapshotRow, len(rows))
	for index, row := range rows {
		projected[index] = relationshipSnapshotRow(row)
	}
	coverage := relationshipCoverage(binding, len(projected))
	truncated := next < len(binding.entries)
	coverage.Truncated = coverage.Truncated || truncated
	page := &RelationshipSnapshotPage{
		SchemaVersion: relationshipSnapshotSchema,
		Query:         query,
		Roots:         relationshipReceipts(binding.sources),
		Rows:          projected,
		Coverage:      coverage,
		Truncated:     truncated,
		Caveat:        relationshipCaveat,
	}
	page.RowsState = relationshipRowsState(
		page.Roots, len(page.Rows), page.Coverage.Truncated,
	)
	if err := service.confirmBinding(ctx, binding, authorization); err != nil {
		return nil, err
	}
	if err := validateRelationshipResponse(page); err != nil {
		return nil, err
	}
	return page, nil
}

const relationshipCaveat = "Static source evidence only; no runtime topology, completeness, migration-complete, decommissioning-safety, or release claim."

func (service *RelationshipService) Compare(
	ctx context.Context,
	query RelationshipComparisonQuery,
	pageSize int,
	cursor string,
) (*RelationshipComparisonPage, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable("service relationship comparison unavailable")
	}
	pageSize, err := validateRelationshipComparisonQuery(&query, pageSize)
	if err != nil {
		return nil, err
	}
	repositories, authorization, err := service.authorizeRepositories(ctx, []string{query.Repository})
	if err != nil {
		return nil, err
	}
	query.Repository = repositories[0].Name
	queryDigest := digestJSON(struct {
		Schema   string                      `json:"schema"`
		Query    RelationshipComparisonQuery `json:"query"`
		PageSize int                         `json:"page_size"`
	}{relationshipComparisonSchema, query, pageSize})
	finish, err := beginRelationshipRead(ctx, service.reads)
	if err != nil {
		return nil, err
	}
	defer finish()
	var binding *relationshipBinding
	offset := 0
	if cursor == "" {
		binding, err = service.buildComparisonBinding(ctx, query, queryDigest, repositories[0], authorization)
		if err == nil {
			err = service.admitBinding(binding)
		}
	} else {
		var decoded relationshipCursor
		decoded, err = service.decodeCursor(cursor)
		if err == nil && decoded.QueryDigest != queryDigest {
			err = huma.Error409Conflict("relationship comparison cursor does not match the query")
		}
		if err == nil {
			binding = service.acquireBinding(decoded.Binding)
			if binding == nil {
				err = huma.Error409Conflict("relationship comparison cursor expired; restart the query")
			} else {
				defer service.releaseBinding(binding)
				offset = decoded.Offset
			}
		}
	}
	if err != nil {
		if binding != nil && cursor == "" {
			service.releaseUnadmittedBinding(binding)
		}
		return nil, err
	}
	if cursor == "" {
		binding = service.acquireBinding(binding.id)
		if binding == nil {
			return nil, huma.Error503ServiceUnavailable("relationship comparison binding capacity is busy; retry")
		}
		defer service.releaseBinding(binding)
	}
	rows, next, err := service.comparisonRows(ctx, binding, offset, pageSize)
	if err != nil {
		return nil, relationshipReadError("read relationship comparison", err)
	}
	page := &RelationshipComparisonPage{
		SchemaVersion: relationshipComparisonSchema, Query: query,
		Roots: relationshipReceipts(binding.sources), Rows: rows,
		Coverage:   relationshipCoverage(binding, len(rows)),
		Pagination: RelationshipPagination{Order: "change_identity:asc", PageSize: pageSize, Returned: len(rows)},
		Caveat:     relationshipCaveat,
	}
	page.RowsState = relationshipRowsState(page.Roots, len(rows), binding.truncated)
	if next < len(binding.entries) {
		page.Pagination.NextCursor = service.encodeSigned(relationshipCursor{
			Schema: relationshipCursorSchema, Binding: binding.id,
			QueryDigest: queryDigest, Offset: next,
		})
	}
	if err := service.confirmBinding(ctx, binding, authorization); err != nil {
		return nil, err
	}
	if err := validateRelationshipResponse(page); err != nil {
		return nil, err
	}
	return page, nil
}

func (service *RelationshipService) ReadCitation(
	ctx context.Context,
	encoded string,
) (*RelationshipCitation, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable("service relationship citation unavailable")
	}
	finish, err := beginRelationshipRead(ctx, service.citations)
	if err != nil {
		return nil, err
	}
	defer finish()
	var token relationshipCitationToken
	if len(encoded) > relationshipTokenBytes || service.decodeSigned(encoded, &token) != nil ||
		token.Schema != relationshipCitationSchema || token.Repository == "" {
		return nil, huma.Error400BadRequest("service relationship citation is invalid")
	}
	authorized, _, authorizationErr := service.authorizeRepositories(
		ctx, []string{token.Repository},
	)
	if authorizationErr != nil || len(authorized) != 1 || authorized[0].Name != token.Repository {
		return nil, huma.Error404NotFound("service relationship citation not found")
	}
	binding := service.acquireBinding(token.Binding)
	if binding == nil {
		return nil, huma.Error409Conflict("service relationship citation expired; rerun the query")
	}
	defer service.releaseBinding(binding)
	if token.Source < 0 || token.Source >= len(binding.sources) {
		return nil, huma.Error400BadRequest("service relationship citation is invalid")
	}
	source := &binding.sources[token.Source]
	if source.repository.Name != token.Repository ||
		!sameRelationshipRepo(source.repository, authorized[0]) {
		return nil, huma.Error404NotFound("service relationship citation not found")
	}
	projection, err := source.publication.ReadProjection(ctx, token.Projection)
	if err != nil {
		return nil, relationshipReadError("read relationship citation projection", err)
	}
	evidence, err := source.publication.ReadEvidence(ctx, service.opts.DataDir, projection)
	if err != nil {
		return nil, relationshipReadError("read relationship citation evidence", err)
	}
	content, err := relationshippublication.ReadEvidenceContent(
		ctx, service.opts.DataDir, source.repository.Name, evidence,
	)
	if err != nil {
		return nil, relationshipReadError("read relationship citation content", err)
	}
	return &RelationshipCitation{
		SchemaVersion: relationshipCitationSchema, Repository: source.repository.Name,
		RootSchema: source.root.Schema, Generation: source.root.GenerationDigest,
		RootDigest: source.root.Digest, AuthorityDigest: source.root.AuthorityDigest,
		Projection: projectRelationshipProjection(projection),
		Evidence:   projectRelationshipEvidence(evidence), Content: string(content),
	}, nil
}

// RootCoverage is the source-free proof/Workbench annex. Authorization for
// every repository is resolved before any relationship pointer is opened,
// and every opened current pointer is rechecked before the receipt emits.
func (service *RelationshipService) RootCoverage(
	ctx context.Context,
	repositories []string,
) (*RelationshipProofCoverage, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable("service relationship coverage unavailable")
	}
	resolved, authorization, err := service.authorizeRepositories(ctx, repositories)
	if err != nil {
		return nil, err
	}
	result := &RelationshipProofCoverage{
		SchemaVersion: "phebs-relationship-proof-coverage-v1",
		Visibility:    authorization, VisibleRepositoryCount: len(resolved),
		Roots: []RelationshipRootReceipt{}, State: "exact",
	}
	publications := make([]*relationshippublication.Publication, len(resolved))
	unavailable := make([]relationshippublication.Unavailable, len(resolved))
	unavailablePresent := make([]bool, len(resolved))
	leases := []*relationshippublication.Lease{}
	defer func() {
		for _, lease := range leases {
			lease.Release()
		}
	}()
	for index, repository := range resolved {
		receipt := RelationshipRootReceipt{Repository: repository.Name, State: "unavailable", Reason: "relationship_root_unavailable"}
		lease, openErr := service.cache.Acquire(
			ctx, filepath.Join(service.opts.DataDir, "relationships"), repository.Name,
		)
		if openErr == nil {
			publication := lease.Publication()
			if publication == nil {
				lease.Release()
				return nil, errors.New("relationship coverage lease has no publication")
			}
			root := publication.Root()
			authority := projectRelationshipAuthority(root.Authority)
			receipt.RootSchema = root.Schema
			receipt.Generation, receipt.RootDigest = root.GenerationDigest, root.Digest
			receipt.AuthorityDigest, receipt.Authority = root.AuthorityDigest, &authority
			receipt.RepositoryComplete, receipt.AllServicesComplete = root.RepositoryComplete, root.AllServicesComplete
			receipt.FailedServiceCount = root.FailedServiceCount
			if root.AllServicesComplete {
				receipt.State, receipt.Reason = "complete", ""
			} else {
				receipt.State, receipt.Reason = "failed", "service_partition_failed"
			}
			leases = append(leases, lease)
			publications[index] = publication
		} else if errors.Is(openErr, relationshippublication.ErrNotFound) {
			marker, present, markerErr := relationshippublication.ReadUnavailable(
				ctx, filepath.Join(service.opts.DataDir, "relationships"), repository.Name,
			)
			if markerErr != nil {
				return nil, relationshipReadError("read relationship unavailable authority", markerErr)
			}
			if present {
				receipt.Reason = marker.Reason
				receipt.Unavailable = projectRelationshipUnavailable(marker)
				unavailable[index], unavailablePresent[index] = marker, true
			}
		} else {
			return nil, relationshipReadError("read relationship proof coverage", openErr)
		}
		if receipt.State == "complete" {
			result.ExactRootCount++
		} else {
			result.GapCount++
			result.State = "gap"
		}
		result.Roots = append(result.Roots, receipt)
	}
	confirmed, confirmedAuthorization, err := service.authorizeRepositories(ctx, repositoryNames(resolved))
	if err != nil {
		return nil, err
	}
	if confirmedAuthorization != authorization || len(confirmed) != len(resolved) {
		return nil, huma.Error409Conflict("relationship coverage authorization changed; retry")
	}
	for index := range confirmed {
		if !sameRelationshipRepo(confirmed[index], resolved[index]) {
			return nil, huma.Error409Conflict("relationship coverage repository authority changed; retry")
		}
	}
	for index, publication := range publications {
		if publication != nil {
			if err := publication.ConfirmCurrent(); err != nil {
				return nil, huma.Error409Conflict("relationship coverage changed; retry")
			}
			continue
		}
		var expected *relationshippublication.Unavailable
		if unavailablePresent[index] {
			expected = &unavailable[index]
		}
		if err := relationshippublication.ConfirmUnavailable(
			ctx, filepath.Join(service.opts.DataDir, "relationships"), resolved[index].Name, expected,
		); err != nil {
			if errors.Is(err, relationshippublication.ErrPublishing) {
				return nil, huma.Error409Conflict("relationship coverage changed; retry")
			}
			return nil, relationshipReadError("confirm relationship proof coverage", err)
		}
	}
	result.Digest = digestJSON(struct {
		Schema                 string                    `json:"schema"`
		Visibility             VisibilityContext         `json:"visibility"`
		VisibleRepositoryCount int                       `json:"visible_repository_count"`
		ExactRootCount         int                       `json:"exact_root_count"`
		GapCount               int                       `json:"gap_count"`
		State                  string                    `json:"state"`
		Reason                 string                    `json:"reason,omitempty"`
		Roots                  []RelationshipRootReceipt `json:"roots"`
	}{result.SchemaVersion, result.Visibility, result.VisibleRepositoryCount, result.ExactRootCount, result.GapCount, result.State, result.Reason, result.Roots})
	if err := validateRelationshipResponse(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (service *RelationshipService) buildCurrentBinding(
	ctx context.Context,
	query RelationshipQuery,
	queryDigest string,
	repositories []store.Repo,
	authorization VisibilityContext,
) (*relationshipBinding, error) {
	binding := &relationshipBinding{
		createdAt: time.Now(), queryDigest: queryDigest, authorization: authorization,
		repositories: slices.Clone(repositories), sources: []relationshipSource{}, entries: []relationshipEntry{},
	}
	for _, repository := range repositories {
		source, refs, err := service.openCurrentSource(ctx, repository, query.ServiceKey)
		if err != nil {
			binding.release()
			return nil, relationshipReadError("open service relationship root", err)
		}
		sourceIndex := len(binding.sources)
		binding.sources = append(binding.sources, source)
		for _, reference := range refs {
			if !relationshipReferenceMatches(reference, query.View, query.Kind, query.Plane, query.LookupKey) {
				continue
			}
			if len(binding.entries) == relationshipMaxListReferences {
				binding.truncated = true
				break
			}
			locator := &relationshipLocator{source: sourceIndex, reference: reference}
			binding.entries = append(binding.entries, relationshipEntry{
				identity: repository.Name + "\x00" + reference.Digest, after: locator,
			})
		}
	}
	sort.Slice(binding.entries, func(i, j int) bool { return binding.entries[i].identity < binding.entries[j].identity })
	return binding, nil
}

func (service *RelationshipService) buildComparisonBinding(
	ctx context.Context,
	query RelationshipComparisonQuery,
	queryDigest string,
	repository store.Repo,
	authorization VisibilityContext,
) (*relationshipBinding, error) {
	binding := &relationshipBinding{
		createdAt: time.Now(), queryDigest: queryDigest, authorization: authorization,
		repositories: []store.Repo{repository}, sources: []relationshipSource{}, entries: []relationshipEntry{},
	}
	before, beforeRefs, err := service.openGenerationSource(
		ctx, repository, query.ServiceKey, query.BeforeGeneration, query.BeforeRootDigest,
	)
	if err != nil {
		return nil, relationshipReadError("open before relationship root", err)
	}
	binding.sources = append(binding.sources, before)
	after, afterRefs, err := service.openGenerationSource(
		ctx, repository, query.ServiceKey, query.AfterGeneration, query.AfterRootDigest,
	)
	if err != nil {
		binding.release()
		return nil, relationshipReadError("open after relationship root", err)
	}
	binding.sources = append(binding.sources, after)
	values := make(map[string]*relationshipEntry)
	add := func(source int, reference relationshippublication.ServiceReference, before bool) {
		if !relationshipReferenceMatches(reference, query.View, query.Kind, query.Plane, query.LookupKey) {
			return
		}
		entry := values[reference.Digest]
		if entry == nil {
			entry = &relationshipEntry{identity: reference.Digest}
			values[reference.Digest] = entry
		}
		locator := &relationshipLocator{source: source, reference: reference}
		if before {
			entry.before = locator
		} else {
			entry.after = locator
		}
	}
	for _, reference := range beforeRefs {
		add(0, reference, true)
	}
	for _, reference := range afterRefs {
		add(1, reference, false)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > relationshipMaxCompareRefs {
		keys = keys[:relationshipMaxCompareRefs]
		binding.truncated = true
	}
	for _, key := range keys {
		binding.entries = append(binding.entries, *values[key])
	}
	return binding, nil
}

func (service *RelationshipService) openCurrentSource(
	ctx context.Context,
	repository store.Repo,
	serviceKey string,
) (relationshipSource, []relationshippublication.ServiceReference, error) {
	source := relationshipSource{repository: repository, current: true, state: "unavailable"}
	source.receipt.ServiceKey = serviceKey
	lease, err := service.cache.Acquire(
		ctx, filepath.Join(service.opts.DataDir, "relationships"), repository.Name,
	)
	if errors.Is(err, relationshippublication.ErrNotFound) {
		marker, present, markerErr := relationshippublication.ReadUnavailable(
			ctx, filepath.Join(service.opts.DataDir, "relationships"), repository.Name,
		)
		if markerErr != nil {
			return source, nil, markerErr
		}
		if present {
			source.unavailable, source.reason = &marker, marker.Reason
		} else {
			source.reason = "relationship_root_unavailable"
		}
		return source, []relationshippublication.ServiceReference{}, nil
	}
	if err != nil {
		return source, nil, err
	}
	source.lease, source.publication = lease, lease.Publication()
	if source.publication == nil {
		source.lease.Release()
		return source, nil, errors.New("relationship lease has no publication")
	}
	source.root = source.publication.Root()
	// The cache lease pins this immutable generation. The shared result-time
	// control snapshot below is the only mutable-current fence needed here.
	receipt, member, err := source.publication.ReadService(ctx, serviceKey)
	source.receipt = receipt
	switch {
	case errors.Is(err, relationshippublication.ErrNotFound):
		source.reason = "service_not_in_relationship_root"
		return source, []relationshippublication.ServiceReference{}, nil
	case errors.Is(err, relationshippublication.ErrServiceUnavailable):
		source.state, source.reason = "failed", receipt.Reason
		return source, []relationshippublication.ServiceReference{}, nil
	case err != nil:
		return source, nil, err
	}
	return service.finishSource(ctx, source, member)
}

func (service *RelationshipService) openGenerationSource(
	ctx context.Context,
	repository store.Repo,
	serviceKey, generation, rootDigest string,
) (relationshipSource, []relationshippublication.ServiceReference, error) {
	source := relationshipSource{repository: repository, state: "unavailable"}
	source.receipt.ServiceKey = serviceKey
	lease, err := service.cache.AcquireGeneration(
		ctx, filepath.Join(service.opts.DataDir, "relationships"), repository.Name,
		generation, rootDigest,
	)
	if err != nil {
		return source, nil, err
	}
	source.lease, source.publication = lease, lease.Publication()
	if source.publication == nil {
		source.lease.Release()
		return source, nil, errors.New("relationship generation lease has no publication")
	}
	source.root = source.publication.Root()
	receipt, member, err := source.publication.ReadService(ctx, serviceKey)
	source.receipt = receipt
	switch {
	case errors.Is(err, relationshippublication.ErrNotFound):
		source.reason = "service_not_in_relationship_root"
		return source, []relationshippublication.ServiceReference{}, nil
	case errors.Is(err, relationshippublication.ErrServiceUnavailable):
		source.state, source.reason = "failed", receipt.Reason
		return source, []relationshippublication.ServiceReference{}, nil
	case err != nil:
		return source, nil, err
	}
	return service.finishSource(ctx, source, member)
}

func (service *RelationshipService) finishSource(
	ctx context.Context,
	source relationshipSource,
	member *relationshippublication.ServiceMember,
) (relationshipSource, []relationshippublication.ServiceReference, error) {
	if member == nil {
		return source, nil, errors.New("relationship service member is nil")
	}
	if len(member.References) == 0 {
		source.state = "empty"
		return source, []relationshippublication.ServiceReference{}, nil
	}
	reader, err := source.publication.OpenEvidenceReader(ctx, service.opts.DataDir)
	if err != nil {
		return source, nil, err
	}
	source.state, source.evidence = "complete", reader
	return source, slices.Clone(member.References), nil
}

func (service *RelationshipService) relationshipRows(
	ctx context.Context,
	binding *relationshipBinding,
	offset, pageSize int,
) ([]RelationshipRow, int, error) {
	if offset < 0 || offset > len(binding.entries) {
		return nil, 0, errors.New("relationship cursor offset is invalid")
	}
	end := min(offset+pageSize, len(binding.entries))
	locators := make([]*relationshipLocator, 0, end-offset)
	for index := offset; index < end; index++ {
		locators = append(locators, binding.entries[index].after)
	}
	rows, err := service.hydrateLocators(ctx, binding, locators)
	return rows, end, err
}

func (service *RelationshipService) comparisonRows(
	ctx context.Context,
	binding *relationshipBinding,
	offset, pageSize int,
) ([]RelationshipComparisonRow, int, error) {
	if offset < 0 || offset > len(binding.entries) {
		return nil, 0, errors.New("comparison cursor offset is invalid")
	}
	end := min(offset+pageSize, len(binding.entries))
	locators := []*relationshipLocator{}
	for index := offset; index < end; index++ {
		if binding.entries[index].before != nil {
			locators = append(locators, binding.entries[index].before)
		}
		if binding.entries[index].after != nil {
			locators = append(locators, binding.entries[index].after)
		}
	}
	rows, err := service.hydrateLocators(ctx, binding, locators)
	if err != nil {
		return nil, 0, err
	}
	result := make([]RelationshipComparisonRow, 0, end-offset)
	position := 0
	for index := offset; index < end; index++ {
		entry := binding.entries[index]
		row := RelationshipComparisonRow{}
		if entry.before != nil {
			value := rows[position]
			position++
			row.Before = &value
		}
		if entry.after != nil {
			value := rows[position]
			position++
			row.After = &value
		}
		switch {
		case row.Before == nil:
			row.Change = "added"
		case row.After == nil:
			row.Change = "removed"
		default:
			row.Change = "unchanged"
		}
		result = append(result, row)
	}
	return result, end, nil
}

func (service *RelationshipService) hydrateLocators(
	ctx context.Context,
	binding *relationshipBinding,
	locators []*relationshipLocator,
) ([]RelationshipRow, error) {
	groups := make(map[int][]*relationshipLocator)
	for _, locator := range locators {
		if locator != nil {
			groups[locator.source] = append(groups[locator.source], locator)
		}
	}
	byKey := make(map[string]RelationshipRow, len(locators))
	for sourceIndex, values := range groups {
		if sourceIndex < 0 || sourceIndex >= len(binding.sources) {
			return nil, errors.New("relationship source index is invalid")
		}
		source := &binding.sources[sourceIndex]
		digests := make([]string, len(values))
		for index, locator := range values {
			digests[index] = locator.reference.ProjectionDigest
		}
		projections, err := source.publication.ReadProjections(ctx, digests)
		if err != nil {
			return nil, err
		}
		projectionValues := make([]relationshippublication.Projection, 0, len(values))
		for _, locator := range values {
			projectionValues = append(projectionValues, projections[locator.reference.ProjectionDigest])
		}
		evidence, err := source.evidence.ReadPage(ctx, projectionValues)
		if err != nil {
			return nil, err
		}
		for _, locator := range values {
			projection := projections[locator.reference.ProjectionDigest]
			row := RelationshipRow{
				Repository: source.repository.Name, ServiceKey: source.receipt.ServiceKey,
				ServiceIncarnation: source.receipt.Incarnation, ServiceGeneration: source.receipt.ServiceGeneration,
				Kind: projection.Kind, Plane: projection.Plane, Class: projection.Class,
				LookupKey: projection.LookupKey, Participation: slices.Clone(locator.reference.Participation),
				CounterpartServices: relationshipCounterparts(source.receipt.ServiceKey, locator.reference.Participation, projection),
				ProjectionDigest:    projection.Digest, PostingDigest: projection.PostingDigest,
				Source:   projectRelationshipPlacement(projection.Source),
				Target:   projectOptionalRelationshipPlacement(projection.Target),
				Evidence: projectRelationshipEvidence(evidence[projection.Digest]),
			}
			row.Citation = service.encodeSigned(relationshipCitationToken{
				Schema: relationshipCitationSchema, Binding: binding.id,
				Repository: source.repository.Name, Source: sourceIndex, Projection: projection.Digest,
			})
			byKey[fmt.Sprintf("%d\x00%s", sourceIndex, projection.Digest)] = row
		}
	}
	result := make([]RelationshipRow, 0, len(locators))
	for _, locator := range locators {
		if locator == nil {
			continue
		}
		row, present := byKey[fmt.Sprintf("%d\x00%s", locator.source, locator.reference.ProjectionDigest)]
		if !present {
			return nil, relationshippublication.ErrNotFound
		}
		result = append(result, row)
	}
	return result, nil
}

func relationshipCounterparts(
	serviceKey string,
	participation []string,
	projection relationshippublication.Projection,
) []string {
	values := map[string]struct{}{}
	add := func(placement relationshippublication.Placement) {
		for _, claim := range placement.Claims {
			if claim.Disposition == "accepted" && claim.ServiceKey != serviceKey {
				values[claim.ServiceKey] = struct{}{}
			}
		}
	}
	if slices.Contains(participation, "source") && projection.Target != nil {
		add(*projection.Target)
	}
	if slices.Contains(participation, "target") {
		add(projection.Source)
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func relationshipReferenceMatches(
	value relationshippublication.ServiceReference,
	view, kind, plane, lookup string,
) bool {
	if kind != "" && value.Kind != kind || plane != "" && value.Plane != plane ||
		lookup != "" && value.LookupKey != lookup {
		return false
	}
	switch view {
	case "dependencies":
		return value.Kind == "rpc" && slices.Contains(value.Participation, "source")
	case "callers":
		return value.Kind == "rpc" && slices.Contains(value.Participation, "target")
	case "topics":
		return value.Kind == "kafka" && slices.Contains(value.Participation, "source")
	default:
		return true
	}
}

func (service *RelationshipService) authorizeRepositories(
	ctx context.Context,
	names []string,
) ([]store.Repo, VisibilityContext, error) {
	allow := repoFilter(ctx, service.opts)
	result := []store.Repo{}
	if len(names) == 0 {
		repositories, err := service.opts.Store.ListRepos(ctx)
		if err != nil {
			return nil, VisibilityContext{}, huma.Error500InternalServerError("authorize service relationships", err)
		}
		for _, repository := range repositories {
			if !repository.Deleting && repository.IndexedCommitHash != "" && (allow == nil || allow(repository)) {
				result = append(result, repository)
			}
		}
	} else {
		if len(names) > relationshipMaxRepositories {
			return nil, VisibilityContext{}, huma.Error400BadRequest("repositories exceed the 32-repository bound")
		}
		canonical := slices.Clone(names)
		sort.Strings(canonical)
		for index, name := range canonical {
			if reponame.Validate(name) != nil || index > 0 && canonical[index-1] == name {
				return nil, VisibilityContext{}, huma.Error404NotFound("service relationships not found")
			}
			repository, err := service.opts.Store.GetRepo(ctx, name)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, VisibilityContext{}, huma.Error500InternalServerError("authorize service relationships", err)
			}
			if err != nil || repository == nil || repository.Deleting || repository.IndexedCommitHash == "" || allow != nil && !allow(*repository) {
				return nil, VisibilityContext{}, huma.Error404NotFound("service relationships not found")
			}
			result = append(result, *repository)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	if len(result) > relationshipMaxRepositories {
		return nil, VisibilityContext{}, huma.Error422UnprocessableEntity("visible repository set exceeds 32; select repositories explicitly")
	}
	return result, catalogVisibilityContext(ctx, service.opts, result), nil
}

func (service *RelationshipService) confirmBinding(
	ctx context.Context,
	binding *relationshipBinding,
	authorization VisibilityContext,
) error {
	repositories, confirmedAuthorization, err := service.authorizeRepositories(ctx, repositoryNames(binding.repositories))
	if err != nil {
		return err
	}
	if confirmedAuthorization != authorization || authorization != binding.authorization || len(repositories) != len(binding.repositories) {
		return huma.Error409Conflict("relationship authorization changed while building the response; retry")
	}
	for index := range repositories {
		if !sameRelationshipRepo(repositories[index], binding.repositories[index]) {
			return huma.Error409Conflict("relationship repository authority changed while building the response; retry")
		}
	}
	for index := range binding.sources {
		source := &binding.sources[index]
		if !source.current {
			continue
		}
		if source.publication != nil {
			if err := source.publication.ConfirmCurrent(); err == nil {
				continue
			}
			return huma.Error409Conflict("relationship publication changed while building the response; retry")
		}
		if err := relationshippublication.ConfirmUnavailable(
			ctx, filepath.Join(service.opts.DataDir, "relationships"),
			source.repository.Name, source.unavailable,
		); err != nil {
			if errors.Is(err, relationshippublication.ErrPublishing) {
				return huma.Error409Conflict("relationship publication changed while building the response; retry")
			}
			return relationshipReadError("confirm relationship publication", err)
		}
	}
	return nil
}

func sameRelationshipRepo(left, right store.Repo) bool {
	return left.Name == right.Name && left.IndexedCommitHash == right.IndexedCommitHash &&
		left.EvidenceRevision == right.EvidenceRevision && equalOptionalTime(left.IndexedAt, right.IndexedAt)
}

func repositoryNames(values []store.Repo) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = values[i].Name
	}
	return result
}

func validateRelationshipQuery(query *RelationshipQuery, pageSize int) (int, error) {
	if query == nil || !validRelationshipText(query.ServiceKey) {
		return 0, huma.Error400BadRequest("service_key is required and must be bounded text")
	}
	if query.View == "" {
		query.View = "all"
	}
	if query.View != "all" && query.View != "dependencies" && query.View != "callers" && query.View != "topics" {
		return 0, huma.Error400BadRequest("view must be all, dependencies, callers, or topics")
	}
	if query.Kind != "" && query.Kind != "rpc" && query.Kind != "kafka" {
		return 0, huma.Error400BadRequest("kind must be rpc or kafka")
	}
	for _, value := range []string{query.Plane, query.LookupKey} {
		if value != "" && !validRelationshipText(value) {
			return 0, huma.Error400BadRequest("relationship filter is invalid")
		}
	}
	if pageSize == 0 {
		pageSize = relationshipDefaultPage
	}
	if pageSize < 1 || pageSize > relationshipMaxPage {
		return 0, huma.Error400BadRequest("page_size must be from 1 through 100")
	}
	return pageSize, nil
}

func validateRelationshipComparisonQuery(query *RelationshipComparisonQuery, pageSize int) (int, error) {
	base := RelationshipQuery{Repositories: []string{query.Repository}, ServiceKey: query.ServiceKey, View: query.View, Kind: query.Kind, Plane: query.Plane, LookupKey: query.LookupKey}
	pageSize, err := validateRelationshipQuery(&base, pageSize)
	if err != nil {
		return 0, err
	}
	query.View = base.View
	for _, value := range []string{query.BeforeGeneration, query.BeforeRootDigest, query.AfterGeneration, query.AfterRootDigest} {
		if !validSHA256Digest(value) {
			return 0, huma.Error400BadRequest("comparison generations and root digests must be sha256 identities")
		}
	}
	return pageSize, nil
}

func validRelationshipText(value string) bool {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func relationshipReceipts(sources []relationshipSource) []RelationshipRootReceipt {
	result := make([]RelationshipRootReceipt, len(sources))
	for index, source := range sources {
		result[index] = RelationshipRootReceipt{Repository: source.repository.Name, State: source.state, Reason: source.reason, ServiceKey: source.receipt.ServiceKey, ServiceIncarnation: source.receipt.Incarnation, ServiceGeneration: source.receipt.ServiceGeneration, ReferenceCount: source.receipt.ReferenceCount}
		if source.publication != nil {
			authority := projectRelationshipAuthority(source.root.Authority)
			result[index].RootSchema = source.root.Schema
			result[index].Generation, result[index].RootDigest = source.root.GenerationDigest, source.root.Digest
			result[index].AuthorityDigest, result[index].Authority = source.root.AuthorityDigest, &authority
			result[index].RepositoryComplete = source.root.RepositoryComplete
			result[index].AllServicesComplete = source.root.AllServicesComplete
			result[index].FailedServiceCount = source.root.FailedServiceCount
			if result[index].ServiceKey == "" {
				result[index].ServiceKey = source.receipt.ServiceKey
			}
		} else if source.unavailable != nil {
			result[index].Unavailable = projectRelationshipUnavailable(*source.unavailable)
		}
	}
	return result
}

func projectRelationshipAuthority(value relationshippublication.Authority) RelationshipAuthority {
	result := RelationshipAuthority{
		Repository:                  value.Repository,
		CatalogGenerationDigest:     value.CatalogGenerationDigest,
		CatalogDigest:               value.CatalogDigest,
		CatalogSourceGeneration:     value.CatalogSourceGeneration,
		ServiceStateSetDigest:       value.ServiceStateSetDigest,
		ServiceStateSummaryDigest:   value.ServiceStateSummaryDigest,
		ServiceStateControlRevision: value.ServiceStateControlRevision,
		ObservationGenerationDigest: value.ObservationGenerationDigest,
		ObservationManifestDigest:   value.ObservationManifestDigest,
		ObservationSourceDigest:     value.ObservationSourceDigest,
		ResolverGenerationDigest:    value.ResolverGenerationDigest,
		ResolverRootDigest:          value.ResolverRootDigest,
		RPCGenerationDigest:         value.RPCGenerationDigest,
		RPCRootDigest:               value.RPCRootDigest,
		KafkaGenerationDigest:       value.KafkaGenerationDigest,
		KafkaRootDigest:             value.KafkaRootDigest,
		PolicyDigest:                value.PolicyDigest,
	}
	if value.Upstream != nil {
		upstream := projectRelationshipUpstream(*value.Upstream)
		result.Upstream = &upstream
	}
	return result
}

func projectRelationshipUpstream(value downstreamauthority.Authority) RelationshipUpstreamAuthority {
	result := RelationshipUpstreamAuthority{
		Schema: value.Schema, Repository: value.Repository, Digest: value.Digest,
		Observation: RelationshipObservationAuthority{
			Version: value.Observation.Version, Repository: value.Observation.Repository,
			SourceGenerationDigest:      value.Observation.SourceGenerationDigest,
			SourceRootDigest:            value.Observation.SourceRootDigest,
			ObservationGenerationDigest: value.Observation.ObservationGenerationDigest,
			ObservationRootDigest:       value.Observation.ObservationRootDigest,
			PartitionPolicyDigest:       value.Observation.PartitionPolicyDigest,
			ObservationPolicyDigest:     value.Observation.ObservationPolicyDigest,
			InventoryPolicyDigest:       value.Observation.InventoryPolicyDigest,
			RecordCount:                 value.Observation.RecordCount, ObservedCount: value.Observation.ObservedCount,
		},
		RequiredDomainCount: len(value.Required), PublishedDomainCount: len(value.Domains),
		Gaps: []RelationshipDomainGapAuthority{},
	}
	domainIndex := 0
	for _, required := range value.Required {
		if domainIndex >= len(value.Domains) || value.Domains[domainIndex].Domain != required.Domain ||
			value.Domains[domainIndex].Version != required.Version {
			result.Gaps = append(result.Gaps, RelationshipDomainGapAuthority{
				Domain: required.Domain, Version: required.Version, Disposition: "absent",
			})
			continue
		}
		domain := value.Domains[domainIndex]
		domainIndex++
		if domain.Disposition != "success" && domain.Disposition != "empty" {
			result.Gaps = append(result.Gaps, RelationshipDomainGapAuthority{
				Domain: domain.Domain, Version: domain.Version, Disposition: domain.Disposition,
			})
		}
	}
	return result
}

func projectRelationshipUnavailable(
	value relationshippublication.Unavailable,
) *RelationshipUnavailableAuthority {
	return &RelationshipUnavailableAuthority{
		Schema: value.Schema, Reason: value.Reason, Digest: value.Digest,
		Upstream: projectRelationshipUpstream(value.Upstream),
	}
}

func projectRelationshipPlacement(value relationshippublication.Placement) RelationshipPlacement {
	result := RelationshipPlacement{
		Path: value.Path, Unowned: value.Unowned,
		Claims: make([]RelationshipServiceClaim, len(value.Claims)),
	}
	for index, claim := range value.Claims {
		result.Claims[index] = RelationshipServiceClaim{
			ServiceKey: claim.ServiceKey, Disposition: claim.Disposition,
			Roles: make([]RelationshipRoleClaim, len(claim.Roles)),
		}
		for roleIndex, role := range claim.Roles {
			result.Claims[index].Roles[roleIndex] = RelationshipRoleClaim{
				Role: role.Role, Origin: role.Origin,
			}
		}
	}
	return result
}

func projectOptionalRelationshipPlacement(
	value *relationshippublication.Placement,
) *RelationshipPlacement {
	if value == nil {
		return nil
	}
	result := projectRelationshipPlacement(*value)
	return &result
}

func projectRelationshipEvidence(value relationshippublication.Evidence) RelationshipEvidence {
	return RelationshipEvidence{
		Kind: value.Kind, Plane: value.Plane, Class: value.Class, Reason: value.Reason,
		Path: value.Path, ObjectID: value.ObjectID, ContentDigest: value.ContentDigest,
		Span:       RelationshipSpan{StartByte: value.Span.StartByte, EndByte: value.Span.EndByte, StartLine: value.Span.StartLine, EndLine: value.Span.EndLine},
		SourceRole: value.SourceRole, Operation: value.Operation,
		CandidateOperations: slices.Clone(value.CandidateOperations),
		DeclarationPath:     value.DeclarationPath, DeclarationLineage: value.DeclarationLineage,
		ResolverRecordDigests: slices.Clone(value.ResolverRecordDigests),
		TopicSpelling:         value.TopicSpelling, GroupIDSpelling: value.GroupIDSpelling,
		Library: value.Library, Shape: value.Shape, Binding: value.Binding,
		PostingDigest: value.PostingDigest,
	}
}

func projectRelationshipProjection(value relationshippublication.Projection) RelationshipProjection {
	return RelationshipProjection{
		Kind: value.Kind, PostingDigest: value.PostingDigest, Class: value.Class,
		Plane: value.Plane, LookupKey: value.LookupKey,
		Source: projectRelationshipPlacement(value.Source),
		Target: projectOptionalRelationshipPlacement(value.Target), Digest: value.Digest,
	}
}

func relationshipSnapshotRow(value RelationshipRow) RelationshipSnapshotRow {
	return RelationshipSnapshotRow{
		Repository:          value.Repository,
		ServiceKey:          value.ServiceKey,
		ServiceIncarnation:  value.ServiceIncarnation,
		ServiceGeneration:   value.ServiceGeneration,
		Kind:                value.Kind,
		Plane:               value.Plane,
		Class:               value.Class,
		LookupKey:           value.LookupKey,
		Participation:       slices.Clone(value.Participation),
		CounterpartServices: slices.Clone(value.CounterpartServices),
		ProjectionDigest:    value.ProjectionDigest,
		PostingDigest:       value.PostingDigest,
		Source:              value.Source,
		Target:              value.Target,
		Evidence:            value.Evidence,
	}
}

func relationshipCoverage(binding *relationshipBinding, returned int) RelationshipCoverage {
	value := RelationshipCoverage{AuthorizedRepositories: len(binding.repositories), ScannedReferences: len(binding.entries), ReturnedRows: returned, Truncated: binding.truncated}
	for _, source := range binding.sources {
		switch source.state {
		case "complete":
			value.CompleteRoots++
		case "empty":
			value.EmptyRoots++
		case "failed":
			value.FailedRoots++
		default:
			value.UnavailableRoots++
		}
	}
	return value
}

func relationshipRowsState(roots []RelationshipRootReceipt, rows int, truncated bool) string {
	if rows > 0 {
		return "nonempty"
	}
	for _, root := range roots {
		if root.State == "failed" || root.State == "unavailable" {
			return "gap"
		}
	}
	return "empty"
}

func validateRelationshipResponse(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return huma.Error500InternalServerError("encode service relationship response", err)
	}
	if len(raw) > relationshipResponseBytes {
		return huma.Error500InternalServerError("service relationship response exceeds its 2 MiB bound")
	}
	return nil
}

func relationshipReadError(operation string, err error) error {
	switch {
	case errors.Is(err, relationshippublication.ErrPublishing):
		return huma.Error409Conflict("service relationship authority changed; retry")
	case errors.Is(err, relationshippublication.ErrNotFound), errors.Is(err, relationshippublication.ErrServiceUnavailable):
		return huma.Error409Conflict("service relationship authority unavailable")
	case errors.Is(err, relationshippublication.ErrLimit):
		return huma.Error422UnprocessableEntity("service relationship read exceeded a bounded limit")
	default:
		return huma.Error500InternalServerError(operation, err)
	}
}

func beginRelationshipRead(ctx context.Context, semaphore chan struct{}) (func(), error) {
	select {
	case semaphore <- struct{}{}:
		return func() { <-semaphore }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (service *RelationshipService) admitBinding(binding *relationshipBinding) error {
	if binding == nil || len(binding.entries) > relationshipMaxCompareRefs {
		return errors.New("relationship binding is invalid")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.expireBindingsLocked(time.Now())
	need := len(binding.entries)
	type victimValue struct {
		id      string
		created time.Time
		entries int
	}
	victims := []victimValue{}
	for id, candidate := range service.bindings {
		if candidate.uses == 0 && !candidate.retired {
			victims = append(victims, victimValue{id, candidate.createdAt, len(candidate.entries)})
		}
	}
	sort.Slice(victims, func(i, j int) bool {
		if victims[i].created.Equal(victims[j].created) {
			return victims[i].id < victims[j].id
		}
		return victims[i].created.Before(victims[j].created)
	})
	for (len(service.bindings) >= relationshipMaxBindings || service.bindingEntries+need > relationshipMaxBindingEntries) && len(victims) > 0 {
		victim := service.bindings[victims[0].id]
		victims = victims[1:]
		service.retireBindingLocked(victim)
	}
	if len(service.bindings) >= relationshipMaxBindings || service.bindingEntries+need > relationshipMaxBindingEntries {
		return huma.Error503ServiceUnavailable("service relationship binding capacity is busy; retry")
	}
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	binding.id = base64.RawURLEncoding.EncodeToString(raw)
	service.bindings[binding.id] = binding
	service.bindingEntries += need
	return nil
}

func (service *RelationshipService) acquireBinding(id string) *relationshipBinding {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.expireBindingsLocked(time.Now())
	binding := service.bindings[id]
	if binding == nil || binding.retired || binding.finalized {
		return nil
	}
	binding.uses++
	return binding
}
func (service *RelationshipService) releaseBinding(binding *relationshipBinding) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if binding == nil || binding.uses < 1 || binding.finalized {
		return
	}
	binding.uses--
	if binding.retired && binding.uses == 0 {
		service.finalizeBindingLocked(binding)
	}
}
func (service *RelationshipService) releaseUnadmittedBinding(binding *relationshipBinding) {
	if binding != nil {
		binding.release()
	}
}
func (service *RelationshipService) expireBindingsLocked(now time.Time) {
	for _, binding := range service.bindings {
		if now.Sub(binding.createdAt) > relationshipBindingLifetime {
			service.retireBindingLocked(binding)
		}
	}
}
func (service *RelationshipService) retireBindingLocked(binding *relationshipBinding) {
	if binding == nil || binding.retired || binding.finalized {
		return
	}
	binding.retired = true
	if service.bindings[binding.id] == binding {
		delete(service.bindings, binding.id)
	}
	if binding.uses == 0 {
		service.finalizeBindingLocked(binding)
	}
}
func (service *RelationshipService) finalizeBindingLocked(binding *relationshipBinding) {
	if binding == nil || binding.finalized {
		return
	}
	binding.finalized = true
	service.bindingEntries -= len(binding.entries)
	binding.release()
}
func (binding *relationshipBinding) release() {
	for index := range binding.sources {
		if binding.sources[index].lease != nil {
			binding.sources[index].lease.Release()
			binding.sources[index].lease = nil
		}
	}
}

func (service *RelationshipService) encodeSigned(value any) string {
	raw, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, service.secret[:])
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (service *RelationshipService) decodeCursor(value string) (relationshipCursor, error) {
	var cursor relationshipCursor
	if len(value) > relationshipTokenBytes || service.decodeSigned(value, &cursor) != nil || cursor.Schema != relationshipCursorSchema || cursor.Binding == "" || cursor.Offset < 0 {
		return relationshipCursor{}, huma.Error400BadRequest("service relationship cursor is invalid")
	}
	return cursor, nil
}
func (service *RelationshipService) decodeSigned(value string, destination any) error {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok {
		return errors.New("signed value has no authentication")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != payload || len(raw) > relationshipTokenBytes {
		return errors.New("signed value is invalid")
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || base64.RawURLEncoding.EncodeToString(provided) != signature {
		return errors.New("signed value signature is invalid")
	}
	mac := hmac.New(sha256.New, service.secret[:])
	_, _ = mac.Write(raw)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return errors.New("signed value authentication differs")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("signed value has trailing JSON")
	}
	return nil
}

func registerRelationshipAPI(api huma.API, opts Options) {
	service := opts.Relationships
	if service == nil {
		return
	}
	type listInput struct {
		Repositories []string `query:"repository"`
		ServiceKey   string   `query:"service_key" required:"true"`
		View         string   `query:"view"`
		Kind         string   `query:"kind"`
		Plane        string   `query:"plane"`
		LookupKey    string   `query:"lookup_key"`
		PageSize     int      `query:"page_size"`
		Cursor       string   `query:"cursor"`
	}
	type listOutput struct{ Body RelationshipPage }
	huma.Register(api, huma.Operation{OperationID: "list-service-relationships", Method: http.MethodGet, Path: "/api/service-relationships", Summary: "Read paged exact service dependency, caller, and topic relationships"}, func(ctx context.Context, input *listInput) (*listOutput, error) {
		page, err := service.List(ctx, RelationshipQuery{Repositories: input.Repositories, ServiceKey: input.ServiceKey, View: input.View, Kind: input.Kind, Plane: input.Plane, LookupKey: input.LookupKey}, input.PageSize, input.Cursor)
		if err != nil {
			return nil, err
		}
		return &listOutput{Body: *page}, nil
	})
	type compareInput struct {
		Repository       string `query:"repository" required:"true"`
		ServiceKey       string `query:"service_key" required:"true"`
		BeforeGeneration string `query:"before_generation" required:"true"`
		BeforeRootDigest string `query:"before_root_digest" required:"true"`
		AfterGeneration  string `query:"after_generation" required:"true"`
		AfterRootDigest  string `query:"after_root_digest" required:"true"`
		View             string `query:"view"`
		Kind             string `query:"kind"`
		Plane            string `query:"plane"`
		LookupKey        string `query:"lookup_key"`
		PageSize         int    `query:"page_size"`
		Cursor           string `query:"cursor"`
	}
	type compareOutput struct{ Body RelationshipComparisonPage }
	huma.Register(api, huma.Operation{OperationID: "compare-service-relationships", Method: http.MethodGet, Path: "/api/service-relationship-comparison", Summary: "Compare two exact service relationship generations"}, func(ctx context.Context, input *compareInput) (*compareOutput, error) {
		page, err := service.Compare(ctx, RelationshipComparisonQuery{Repository: input.Repository, ServiceKey: input.ServiceKey, BeforeGeneration: input.BeforeGeneration, BeforeRootDigest: input.BeforeRootDigest, AfterGeneration: input.AfterGeneration, AfterRootDigest: input.AfterRootDigest, View: input.View, Kind: input.Kind, Plane: input.Plane, LookupKey: input.LookupKey}, input.PageSize, input.Cursor)
		if err != nil {
			return nil, err
		}
		return &compareOutput{Body: *page}, nil
	})
	type citationInput struct {
		Citation string `query:"citation" required:"true"`
	}
	type citationOutput struct{ Body RelationshipCitation }
	huma.Register(api, huma.Operation{OperationID: "read-service-relationship-citation", Method: http.MethodGet, Path: "/api/service-relationship-citation", Summary: "Read one lease-pinned exact relationship source span"}, func(ctx context.Context, input *citationInput) (*citationOutput, error) {
		value, err := service.ReadCitation(ctx, input.Citation)
		if err != nil {
			return nil, err
		}
		return &citationOutput{Body: *value}, nil
	})
}
