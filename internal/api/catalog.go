package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	contractCatalogSchemaVersion = "contract-atlas-v1"
	contractCatalogCapability    = "contract-atlas"
	// v2: the cursor gained a protocol-pack index when the catalog became
	// multi-protocol (T19.4). Stale v1 cursors fail decode as invalid.
	contractCatalogCursorVersion = "contract-atlas-cursor-v2"
	contractCatalogCaveat        = "Provisional source evidence only; unresolved joins are not runtime relationships, and no absence or completeness conclusion is implied."

	catalogDefaultPageSize       = 50
	catalogMaxPageSize           = 100
	catalogAssertionScanLimit    = 500
	catalogLocatorLimit          = 500
	catalogLocatorsPerClaimLimit = 16
	catalogRelationshipLimit     = 200
	catalogMessageDepthLimit     = 6
	catalogMessageNodeLimit      = 256
	catalogFieldsPerMessageLimit = 100
	catalogBuildAttempts         = 3
	catalogCursorBytesLimit      = 16 << 10
)

// protocolPack is the one generalization seam between the catalog and a
// contract protocol: pure data, no behavior (T19.4). List iteration, detail
// schema literals, relationship predicates, and field-number bounds all read
// from this table; adding a protocol must not add catalog control flow.
type protocolPack struct {
	protocol          string
	declarationDomain string
	consumerDomain    string
	operationSchema   string
	fieldSchema       string
	// registersPredicate/unresolvedCallPredicate parameterize the
	// relationship filter triple; CALLS_OPERATION is shared across packs and
	// scoped by consumerDomain.
	registersPredicate      string
	unresolvedCallPredicate string
	// Inclusive declared-field bounds. Protobuf field numbers start at 1;
	// Thrift result structs use field 0 as the wire success slot and cap at
	// the positive i16 range.
	fieldMin, fieldMax int
}

var protocolPacks = []protocolPack{
	{
		protocol: "protobuf", declarationDomain: "proto-contract",
		consumerDomain: "grpc-consumer", operationSchema: "proto-operation-detail-v1",
		fieldSchema: "proto-field-detail-v2", registersPredicate: "REGISTERS_GRPC_SERVICE",
		unresolvedCallPredicate: "UNRESOLVED_GRPC_CALL", fieldMin: 1, fieldMax: 536_870_911,
	},
	{
		protocol: "thrift", declarationDomain: "thrift-contract",
		consumerDomain: "thrift-consumer", operationSchema: "thrift-operation-detail-v1",
		fieldSchema: "thrift-field-detail-v1", registersPredicate: "REGISTERS_THRIFT_SERVICE",
		unresolvedCallPredicate: "UNRESOLVED_THRIFT_CALL", fieldMin: 0, fieldMax: 32_767,
	},
}

var catalogDomains = catalogPackDomains()

func catalogPackDomains() []string {
	var domains []string
	for _, pack := range protocolPacks {
		domains = append(domains, pack.declarationDomain, pack.consumerDomain)
	}
	sort.Strings(domains)
	return domains
}

func catalogProtocols() []string {
	protocols := make([]string, len(protocolPacks))
	for index, pack := range protocolPacks {
		protocols[index] = pack.protocol
	}
	return protocols
}

func packForProtocol(protocol string) (protocolPack, bool) {
	for _, pack := range protocolPacks {
		if pack.protocol == protocol {
			return pack, true
		}
	}
	return protocolPack{}, false
}

// ContractCatalogService is the read-only, ephemeral Contract Atlas query
// engine. It persists no response and is available only when the provisional
// evidence store and an authenticated-principal resolver are both bound.
type ContractCatalogService struct {
	opts Options
}

func NewContractCatalogService(opts Options) *ContractCatalogService {
	if opts.Store == nil || opts.Principal == nil ||
		opts.Evidence == nil && opts.ContractCatalogFixture == nil {
		return nil
	}
	return &ContractCatalogService{opts: opts}
}

type ContractCatalogQuery struct {
	Repository string `json:"repository,omitempty"`
	Package    string `json:"package,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Lineage    string `json:"lineage,omitempty"`
}

type ContractCatalogSource struct {
	Repository  string `json:"repository"`
	Commit      string `json:"commit"`
	Path        string `json:"path"`
	StartByte   int    `json:"start_byte"`
	EndByte     int    `json:"end_byte"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	AssertionID string `json:"assertion_id"`
	RunID       string `json:"run_id"`
	AtomID      string `json:"atom_id"`
}

type ContractCatalogClaim struct {
	AssertionID      string                  `json:"assertion_id"`
	RunID            string                  `json:"run_id"`
	Predicate        string                  `json:"predicate"`
	Object           string                  `json:"object"`
	Lineage          string                  `json:"lineage,omitempty"`
	Tier             string                  `json:"tier"`
	CodeRole         string                  `json:"code_role,omitempty"`
	Detail           json.RawMessage         `json:"detail,omitempty"`
	Sources          []ContractCatalogSource `json:"sources"`
	SourcesTruncated bool                    `json:"sources_truncated"`
}

type ContractCatalogItem struct {
	Kind        string               `json:"kind"` // service | operation
	Protocol    string               `json:"protocol"`
	Repository  string               `json:"repository"`
	Lineage     string               `json:"declaration_lineage"`
	Package     string               `json:"package,omitempty"`
	ServiceFQN  string               `json:"service_fqn"`
	Method      string               `json:"method,omitempty"`
	Operation   string               `json:"operation,omitempty"`
	Declaration ContractCatalogClaim `json:"declaration"`
}

type ContractCatalogPagination struct {
	Complete   bool   `json:"complete"`
	Truncated  bool   `json:"truncated"`
	Reason     string `json:"reason,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type ContractCatalogList struct {
	SchemaVersion  string                      `json:"schema_version"`
	Query          ContractCatalogQuery        `json:"query"`
	Items          []ContractCatalogItem       `json:"items"`
	Pagination     ContractCatalogPagination   `json:"pagination"`
	CoverageDigest string                      `json:"coverage_digest"`
	Coverage       extract.CoverageCertificate `json:"coverage"`
	Caveat         string                      `json:"caveat"`
}

type ContractCatalogImportContext struct {
	Count     int      `json:"count"`
	Digest    string   `json:"digest"`
	Paths     []string `json:"paths,omitempty"`
	Truncated bool     `json:"truncated"`
}

type ContractCatalogTypeReference struct {
	Raw         string                        `json:"raw"`
	Kind        string                        `json:"kind,omitempty"`
	Resolution  string                        `json:"resolution"`
	Declaration string                        `json:"declaration,omitempty"`
	Reason      string                        `json:"reason,omitempty"`
	Imports     *ContractCatalogImportContext `json:"imports,omitempty"`
}

type ContractCatalogMapShape struct {
	Key   ContractCatalogTypeReference `json:"key"`
	Value ContractCatalogTypeReference `json:"value"`
}

type ContractCatalogFieldDetail struct {
	Schema      string                        `json:"schema"`
	Name        string                        `json:"name"`
	Type        *ContractCatalogTypeReference `json:"type,omitempty"`
	Cardinality string                        `json:"cardinality"`
	Map         *ContractCatalogMapShape      `json:"map,omitempty"`
	Oneof       string                        `json:"oneof,omitempty"`
}

type ContractCatalogOperationFactDetail struct {
	Schema          string                       `json:"schema"`
	Request         ContractCatalogTypeReference `json:"request"`
	Response        ContractCatalogTypeReference `json:"response"`
	ClientStreaming bool                         `json:"client_streaming"`
	ServerStreaming bool                         `json:"server_streaming"`
}

type ContractCatalogFieldShape struct {
	Object      string                     `json:"object"`
	FieldNumber int                        `json:"field_number"`
	Detail      ContractCatalogFieldDetail `json:"detail"`
	Declaration ContractCatalogClaim       `json:"declaration"`
	Nested      *ContractCatalogMessage    `json:"nested,omitempty"`
}

type ContractCatalogMessage struct {
	Raw               string                      `json:"raw"`
	State             string                      `json:"state"` // resolved | unresolved | cycle | depth_limit | node_limit
	Reason            string                      `json:"reason,omitempty"`
	DeclarationName   string                      `json:"declaration_name,omitempty"`
	Declaration       *ContractCatalogClaim       `json:"declaration,omitempty"`
	Fields            []ContractCatalogFieldShape `json:"fields,omitempty"`
	Truncated         bool                        `json:"truncated"`
	TruncationReasons []string                    `json:"truncation_reasons,omitempty"`
}

type ContractCatalogRelationship struct {
	Kind           string               `json:"kind"` // implementation | caller | unresolved_candidate
	Classification string               `json:"classification"`
	Reason         string               `json:"reason,omitempty"`
	Claim          ContractCatalogClaim `json:"claim"`
}

type ContractCatalogOperation struct {
	SchemaVersion           string                             `json:"schema_version"`
	Repository              string                             `json:"repository"`
	DeclarationLineage      string                             `json:"declaration_lineage"`
	ServiceFQN              string                             `json:"service_fqn"`
	Method                  string                             `json:"method"`
	Operation               string                             `json:"operation"`
	Declaration             ContractCatalogClaim               `json:"declaration"`
	FactDetail              ContractCatalogOperationFactDetail `json:"fact_detail"`
	Request                 ContractCatalogMessage             `json:"request"`
	Response                ContractCatalogMessage             `json:"response"`
	Implementations         []ContractCatalogRelationship      `json:"implementations"`
	Callers                 []ContractCatalogRelationship      `json:"callers"`
	UnresolvedCandidates    []ContractCatalogRelationship      `json:"unresolved_candidates"`
	RelationshipsTruncated  bool                               `json:"relationships_truncated"`
	RelationshipLimitReason string                             `json:"relationship_limit_reason,omitempty"`
	ShapeTruncated          bool                               `json:"shape_truncated"`
	CoverageDigest          string                             `json:"coverage_digest"`
	Coverage                extract.CoverageCertificate        `json:"coverage"`
	Caveat                  string                             `json:"caveat"`
}

type catalogCursor struct {
	Schema                     string                 `json:"schema"`
	QueryDigest                string                 `json:"query_digest"`
	Principal                  string                 `json:"principal"`
	AuthorizationProvider      string                 `json:"authorization_provider"`
	PermissionSnapshot         string                 `json:"permission_snapshot"`
	VisibleRepositorySetDigest string                 `json:"visible_repository_set_digest"`
	CoverageDigest             string                 `json:"coverage_digest"`
	PackIndex                  int                    `json:"pack_index"`
	RepositoryIndex            int                    `json:"repository_index"`
	After                      *store.AssertionCursor `json:"after,omitempty"`
	Checksum                   string                 `json:"checksum"`
}

type catalogPosition struct {
	packIndex       int
	repositoryIndex int
	after           *store.AssertionCursor
	reason          string
}

func (s *ContractCatalogService) List(
	ctx context.Context,
	query ContractCatalogQuery,
	pageSize int,
	cursor string,
) (*ContractCatalogList, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("contract catalog unavailable")
	}
	if !catalogAuthenticated(ctx, s.opts) {
		return nil, huma.Error404NotFound("contract catalog not found")
	}
	if err := validateCatalogQuery(query); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if pageSize == 0 {
		pageSize = catalogDefaultPageSize
	}
	if pageSize < 1 || pageSize > catalogMaxPageSize {
		return nil, huma.Error400BadRequest(
			fmt.Sprintf("page_size must be from 1 through %d", catalogMaxPageSize),
		)
	}
	if s.opts.ContractCatalogFixture != nil {
		return s.opts.ContractCatalogFixture.list(ctx, s.opts, query, pageSize, cursor)
	}

	visible, err := visibleRepositories(ctx, s.opts)
	if err != nil {
		return nil, err
	}
	if query.Repository != "" && !catalogRepositoryVisible(visible, query.Repository) {
		return nil, huma.Error404NotFound("contract catalog scope not found")
	}
	queryDigest := digestJSON(struct {
		Query    ContractCatalogQuery `json:"query"`
		PageSize int                  `json:"page_size"`
	}{query, pageSize})

	for attempt := 0; attempt < catalogBuildAttempts; attempt++ {
		certificate, err := extract.BuildCoverageCertificate(ctx, s.opts.Evidence, visible, catalogDomains)
		if err != nil {
			return nil, huma.Error500InternalServerError("build contract catalog coverage", err)
		}
		binding := visibilityContext(ctx, s.opts, certificate)
		position, err := decodeCatalogCursor(cursor, queryDigest, binding, certificate.Digest)
		if err != nil {
			return nil, err
		}
		scope := catalogCertificateScope(certificate, query.Repository)
		if position.repositoryIndex < 0 || position.repositoryIndex > len(scope) ||
			position.packIndex < 0 || position.packIndex > len(protocolPacks) {
			return nil, huma.Error409Conflict("contract catalog cursor is no longer valid")
		}
		items, next, collectErr := collectCatalogList(
			ctx, s.opts.Evidence, scope, query, pageSize, position,
		)
		confirmedVisible, visibleErr := visibleRepositories(ctx, s.opts)
		if visibleErr != nil {
			return nil, visibleErr
		}
		if catalogVisibilityContext(ctx, s.opts, confirmedVisible) != binding {
			return nil, huma.Error409Conflict("contract catalog authorization changed while building the response; retry")
		}
		confirmed, confirmErr := extract.BuildCoverageCertificate(
			ctx, s.opts.Evidence, confirmedVisible, catalogDomains,
		)
		if confirmErr != nil {
			return nil, huma.Error500InternalServerError("confirm contract catalog coverage", confirmErr)
		}
		if confirmed.Digest != certificate.Digest {
			if cursor != "" {
				return nil, huma.Error409Conflict("contract catalog cursor is no longer valid")
			}
			continue
		}
		if collectErr != nil {
			if errors.Is(collectErr, store.ErrConflict) || errors.Is(collectErr, store.ErrNotFound) {
				continue
			}
			if errors.Is(collectErr, store.ErrResultLimit) {
				return nil, huma.Error422UnprocessableEntity("contract catalog projection exceeded a bounded evidence limit")
			}
			return nil, huma.Error500InternalServerError("build contract catalog", collectErr)
		}

		pagination := ContractCatalogPagination{Complete: true}
		if next != nil {
			encoded, err := encodeCatalogCursor(queryDigest, binding, certificate.Digest, *next)
			if err != nil {
				return nil, huma.Error500InternalServerError("encode contract catalog cursor", err)
			}
			pagination = ContractCatalogPagination{
				Complete: false, Truncated: true, Reason: next.reason, NextCursor: encoded,
			}
		}
		return &ContractCatalogList{
			SchemaVersion: contractCatalogSchemaVersion, Query: query,
			Items: items, Pagination: pagination,
			CoverageDigest: certificate.Digest, Coverage: *certificate,
			Caveat: contractCatalogCaveat,
		}, nil
	}
	return nil, huma.Error409Conflict("evidence changed while building the contract catalog; retry")
}

func (s *ContractCatalogService) Operation(
	ctx context.Context,
	repository, lineage, operation string,
) (*ContractCatalogOperation, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("contract catalog unavailable")
	}
	if !catalogAuthenticated(ctx, s.opts) {
		return nil, huma.Error404NotFound("contract catalog not found")
	}
	if err := validateCatalogRepository(repository); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if !validQueryIdentity(lineage) {
		return nil, huma.Error400BadRequest("lineage must be a bounded contract identity")
	}
	if err := validateOperation(operation); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if s.opts.ContractCatalogFixture != nil {
		return s.opts.ContractCatalogFixture.operation(
			ctx, s.opts, repository, lineage, operation,
		)
	}
	visible, err := visibleRepositories(ctx, s.opts)
	if err != nil {
		return nil, err
	}
	if !catalogRepositoryVisible(visible, repository) {
		return nil, huma.Error404NotFound("contract catalog operation not found")
	}

	for attempt := 0; attempt < catalogBuildAttempts; attempt++ {
		certificate, err := extract.BuildCoverageCertificate(ctx, s.opts.Evidence, visible, catalogDomains)
		if err != nil {
			return nil, huma.Error500InternalServerError("build contract catalog coverage", err)
		}
		binding := visibilityContext(ctx, s.opts, certificate)
		detail, collectErr := collectCatalogOperation(
			ctx, s.opts.Evidence, certificate, repository, lineage, operation,
		)
		confirmedVisible, visibleErr := visibleRepositories(ctx, s.opts)
		if visibleErr != nil {
			return nil, visibleErr
		}
		if catalogVisibilityContext(ctx, s.opts, confirmedVisible) != binding {
			return nil, huma.Error409Conflict("contract catalog authorization changed while building the response; retry")
		}
		confirmed, confirmErr := extract.BuildCoverageCertificate(
			ctx, s.opts.Evidence, confirmedVisible, catalogDomains,
		)
		if confirmErr != nil {
			return nil, huma.Error500InternalServerError("confirm contract catalog coverage", confirmErr)
		}
		if confirmed.Digest != certificate.Digest {
			continue
		}
		if collectErr != nil {
			if errors.Is(collectErr, store.ErrNotFound) {
				return nil, huma.Error404NotFound("contract catalog operation not found")
			}
			if errors.Is(collectErr, store.ErrConflict) {
				continue
			}
			if errors.Is(collectErr, store.ErrResultLimit) {
				return nil, huma.Error422UnprocessableEntity("contract catalog detail exceeded a bounded evidence limit")
			}
			return nil, huma.Error500InternalServerError("build contract catalog operation", collectErr)
		}
		detail.CoverageDigest = certificate.Digest
		detail.Coverage = *certificate
		return detail, nil
	}
	return nil, huma.Error409Conflict("evidence changed while building the contract catalog; retry")
}

func collectCatalogList(
	ctx context.Context,
	source store.EvidenceStore,
	repositories []extract.CertificateRepository,
	query ContractCatalogQuery,
	pageSize int,
	start catalogPosition,
) ([]ContractCatalogItem, *catalogPosition, error) {
	items := make([]ContractCatalogItem, 0, pageSize)
	locatorBudget := catalogLocatorLimit
	scanned := 0
	packIndex := start.packIndex
	repositoryIndex := start.repositoryIndex
	after := cloneAssertionCursor(start.after)

	// Iteration is protocol-major over the registry, then repository-major
	// within each pack, so pagination order is deterministic and the cursor
	// binds (pack, repository, assertion) exactly.
	for packIndex < len(protocolPacks) {
		pack := protocolPacks[packIndex]
		if query.Protocol != "" && query.Protocol != pack.protocol {
			packIndex++
			repositoryIndex = 0
			after = nil
			continue
		}
		for repositoryIndex < len(repositories) {
			repository := repositories[repositoryIndex]
			run, ok := certificateRun(repository, pack.declarationDomain)
			if !ok || run.Status != "published" {
				repositoryIndex++
				after = nil
				continue
			}
			remainingScan := catalogAssertionScanLimit - scanned
			if remainingScan <= 0 {
				return items, &catalogPosition{
					packIndex: packIndex, repositoryIndex: repositoryIndex, after: after,
					reason: "assertion_scan_limit",
				}, nil
			}
			rows, err := source.ListAssertions(ctx, store.AssertionQuery{
				Repo: repository.Repository, RunID: run.RunID,
				Limit: remainingScan, After: after, AllowTruncate: true,
			})
			if err != nil {
				return nil, nil, err
			}
			hasMore := len(rows) > remainingScan
			if hasMore {
				rows = rows[:remainingScan]
			}
			for rowIndex, assertion := range rows {
				if err := validateCatalogAssertionScope(assertion, repository.Repository, run.RunID); err != nil {
					return nil, nil, err
				}
				scanned++
				after = catalogAssertionCursor(assertion)
				if assertion.Predicate != "DECLARES_SERVICE" &&
					assertion.Predicate != "DECLARES_OPERATION" {
					continue
				}
				item, matches, err := catalogListItem(pack, assertion, query)
				if err != nil {
					return nil, nil, err
				}
				if !matches {
					continue
				}
				if locatorBudget == 0 {
					return items, &catalogPosition{
						packIndex: packIndex, repositoryIndex: repositoryIndex,
						after:  catalogCursorBefore(rows, rowIndex),
						reason: "source_locator_limit",
					}, nil
				}
				claim, used, err := resolveCatalogClaim(
					ctx, source, assertion, run.Commit,
					min(catalogLocatorsPerClaimLimit, locatorBudget),
				)
				if err != nil {
					return nil, nil, err
				}
				locatorBudget -= used
				item.Declaration = claim
				items = append(items, item)
				if len(items) == pageSize {
					if rowIndex+1 < len(rows) || hasMore {
						return items, &catalogPosition{
							packIndex: packIndex, repositoryIndex: repositoryIndex,
							after: after, reason: "page_size",
						}, nil
					}
					if next := nextCatalogPosition(repositories, query, packIndex, repositoryIndex+1); next != nil {
						next.reason = "page_size"
						return items, next, nil
					}
					return items, nil, nil
				}
			}
			if hasMore {
				return items, &catalogPosition{
					packIndex: packIndex, repositoryIndex: repositoryIndex, after: after,
					reason: "assertion_scan_limit",
				}, nil
			}
			repositoryIndex++
			after = nil
		}
		packIndex++
		repositoryIndex = 0
		after = nil
	}
	return items, nil, nil
}

// nextCatalogPosition finds the next (pack, repository) pair that can hold
// declaration rows, so a full page never emits a dead continuation cursor.
func nextCatalogPosition(
	repositories []extract.CertificateRepository,
	query ContractCatalogQuery,
	packIndex, repositoryStart int,
) *catalogPosition {
	for ; packIndex < len(protocolPacks); packIndex++ {
		pack := protocolPacks[packIndex]
		if query.Protocol != "" && query.Protocol != pack.protocol {
			repositoryStart = 0
			continue
		}
		for index := repositoryStart; index < len(repositories); index++ {
			run, ok := certificateRun(repositories[index], pack.declarationDomain)
			if ok && run.Status == "published" && run.AssertionCount > 0 {
				return &catalogPosition{packIndex: packIndex, repositoryIndex: index}
			}
		}
		repositoryStart = 0
	}
	return nil
}

func collectCatalogOperation(
	ctx context.Context,
	source store.EvidenceStore,
	certificate *extract.CoverageCertificate,
	repository, lineage, operation string,
) (*ContractCatalogOperation, error) {
	repositoryCoverage, ok := catalogCertificateRepository(certificate, repository)
	if !ok {
		return nil, store.ErrNotFound
	}
	// The operation route's identity is (repository, lineage, operation) with
	// no protocol; the owning pack is found by probing declaration domains in
	// registry order. Provisional lineage hashes the declaring file path, and
	// candidate suffixes are disjoint across packs, so at most one domain can
	// hold the identity.
	storedOperation := strings.TrimPrefix(operation, "/")
	var pack protocolPack
	var run extract.CertificateRun
	var assertion store.Assertion
	found := false
	for _, candidate := range protocolPacks {
		candidateRun, ok := certificateRun(repositoryCoverage, candidate.declarationDomain)
		if !ok || candidateRun.Status != "published" {
			continue
		}
		assertions, err := source.ListAssertions(ctx, store.AssertionQuery{
			Repo: repository, RunID: candidateRun.RunID, Predicate: "DECLARES_OPERATION",
			Object: storedOperation, Lineage: lineage, Limit: 1,
		})
		if err != nil {
			return nil, err
		}
		if len(assertions) != 1 {
			continue
		}
		pack, run, assertion, found = candidate, candidateRun, assertions[0], true
		break
	}
	if !found {
		return nil, store.ErrNotFound
	}
	if err := validateCatalogAssertionScope(assertion, repository, run.RunID); err != nil {
		return nil, err
	}
	declaration, _, err := resolveCatalogClaim(
		ctx, source, assertion, run.Commit, catalogLocatorsPerClaimLimit,
	)
	if err != nil {
		return nil, err
	}
	var factDetail ContractCatalogOperationFactDetail
	if err := decodeCatalogDetail(assertion.Detail, pack.operationSchema, &factDetail); err != nil {
		return nil, err
	}
	service, method, ok := strings.Cut(storedOperation, "/")
	if !ok || service == "" || method == "" {
		return nil, errors.New("contract operation assertion identity is inconsistent")
	}

	shapeState := &catalogShapeState{
		ctx: ctx, source: source, repository: repository, run: run, pack: pack,
		lineage: lineage, locatorBudget: catalogLocatorLimit,
	}
	request, err := shapeState.expandType(factDetail.Request, 0, map[string]bool{})
	if err != nil {
		return nil, err
	}
	response, err := shapeState.expandType(factDetail.Response, 0, map[string]bool{})
	if err != nil {
		return nil, err
	}
	implementations, callers, unresolved, relationshipsTruncated, err :=
		collectCatalogRelationships(ctx, source, certificate, pack, lineage, service, method)
	if err != nil {
		return nil, err
	}
	return &ContractCatalogOperation{
		SchemaVersion: contractCatalogSchemaVersion,
		Repository:    repository, DeclarationLineage: lineage,
		ServiceFQN: service, Method: method, Operation: operation,
		Declaration: declaration, FactDetail: factDetail,
		Request: request, Response: response,
		Implementations: implementations, Callers: callers,
		UnresolvedCandidates:   unresolved,
		RelationshipsTruncated: relationshipsTruncated,
		RelationshipLimitReason: func() string {
			if relationshipsTruncated {
				return "relationship_row_limit"
			}
			return ""
		}(),
		ShapeTruncated: request.Truncated || response.Truncated,
		Caveat:         contractCatalogCaveat,
	}, nil
}

type catalogShapeState struct {
	ctx           context.Context
	source        store.EvidenceStore
	repository    string
	run           extract.CertificateRun
	pack          protocolPack
	lineage       string
	nodes         int
	locatorBudget int
}

func (s *catalogShapeState) expandType(
	reference ContractCatalogTypeReference,
	depth int,
	stack map[string]bool,
) (ContractCatalogMessage, error) {
	result := ContractCatalogMessage{Raw: reference.Raw, State: "unresolved", Reason: reference.Reason}
	if reference.Resolution != "same_file" || reference.Kind != "message" ||
		reference.Declaration == "" {
		if result.Reason == "" {
			result.Reason = "TYPE_NOT_RESOLVED_TO_SAME_FILE_MESSAGE"
		}
		return result, nil
	}
	result.DeclarationName = reference.Declaration
	if stack[reference.Declaration] {
		result.State = "cycle"
		result.Reason = "RECURSIVE_MESSAGE_REFERENCE"
		return result, nil
	}
	if depth > catalogMessageDepthLimit {
		result.State = "depth_limit"
		result.Reason = "MESSAGE_DEPTH_LIMIT"
		result.Truncated = true
		result.TruncationReasons = []string{"message_depth_limit"}
		return result, nil
	}
	if s.nodes >= catalogMessageNodeLimit {
		result.State = "node_limit"
		result.Reason = "MESSAGE_NODE_LIMIT"
		result.Truncated = true
		result.TruncationReasons = []string{"message_node_limit"}
		return result, nil
	}
	s.nodes++

	rows, err := s.source.ListAssertions(s.ctx, store.AssertionQuery{
		Repo: s.repository, RunID: s.run.RunID, Predicate: "DECLARES_MESSAGE",
		Object: reference.Declaration, Lineage: s.lineage, Limit: 1,
	})
	if err != nil {
		return ContractCatalogMessage{}, err
	}
	if len(rows) != 1 {
		return ContractCatalogMessage{}, errors.New("same-file message link has no unique declaration")
	}
	declaration := rows[0]
	if err := validateCatalogAssertionScope(declaration, s.repository, s.run.RunID); err != nil {
		return ContractCatalogMessage{}, err
	}
	if s.locatorBudget <= 0 {
		result.State = "node_limit"
		result.Reason = "SOURCE_LOCATOR_LIMIT"
		result.Truncated = true
		result.TruncationReasons = []string{"source_locator_limit"}
		return result, nil
	}
	claim, used, err := resolveCatalogClaim(
		s.ctx, s.source, declaration, s.run.Commit,
		min(catalogLocatorsPerClaimLimit, s.locatorBudget),
	)
	if err != nil {
		return ContractCatalogMessage{}, err
	}
	s.locatorBudget -= used
	result.State = "resolved"
	result.Declaration = &claim

	fields, err := s.source.ListAssertions(s.ctx, store.AssertionQuery{
		Repo: s.repository, RunID: s.run.RunID, Predicate: "DECLARES_FIELD",
		ObjectPrefix: reference.Declaration + "#", Lineage: s.lineage,
		Limit: catalogFieldsPerMessageLimit, AllowTruncate: true,
	})
	if err != nil {
		return ContractCatalogMessage{}, err
	}
	fieldsTruncated := len(fields) > catalogFieldsPerMessageLimit
	if fieldsTruncated {
		fields = fields[:catalogFieldsPerMessageLimit]
		result.Truncated = true
		result.TruncationReasons = append(result.TruncationReasons, "fields_per_message_limit")
	}
	stackCopy := cloneMessageStack(stack)
	stackCopy[reference.Declaration] = true
	for _, field := range fields {
		if s.nodes >= catalogMessageNodeLimit {
			result.Truncated = true
			result.TruncationReasons = appendUnique(
				result.TruncationReasons, "message_node_limit",
			)
			break
		}
		if s.locatorBudget <= 0 {
			result.Truncated = true
			result.TruncationReasons = appendUnique(
				result.TruncationReasons, "source_locator_limit",
			)
			break
		}
		if err := validateCatalogAssertionScope(field, s.repository, s.run.RunID); err != nil {
			return ContractCatalogMessage{}, err
		}
		s.nodes++
		var detail ContractCatalogFieldDetail
		if err := decodeCatalogDetail(field.Detail, s.pack.fieldSchema, &detail); err != nil {
			return ContractCatalogMessage{}, err
		}
		number, err := catalogFieldNumber(field.Object, reference.Declaration, s.pack)
		if err != nil {
			return ContractCatalogMessage{}, err
		}
		fieldClaim, fieldUsed, err := resolveCatalogClaim(
			s.ctx, s.source, field, s.run.Commit,
			min(catalogLocatorsPerClaimLimit, s.locatorBudget),
		)
		if err != nil {
			return ContractCatalogMessage{}, err
		}
		s.locatorBudget -= fieldUsed
		fieldShape := ContractCatalogFieldShape{
			Object: field.Object, FieldNumber: number,
			Detail: detail, Declaration: fieldClaim,
		}
		nestedReference := catalogFieldMessageReference(detail)
		if nestedReference != nil {
			nested, err := s.expandType(*nestedReference, depth+1, stackCopy)
			if err != nil {
				return ContractCatalogMessage{}, err
			}
			fieldShape.Nested = &nested
			if nested.Truncated {
				result.Truncated = true
				for _, reason := range nested.TruncationReasons {
					result.TruncationReasons = appendUnique(result.TruncationReasons, reason)
				}
			}
		}
		result.Fields = append(result.Fields, fieldShape)
	}
	return result, nil
}

func collectCatalogRelationships(
	ctx context.Context,
	source store.EvidenceStore,
	certificate *extract.CoverageCertificate,
	pack protocolPack,
	declarationLineage, service, method string,
) ([]ContractCatalogRelationship, []ContractCatalogRelationship, []ContractCatalogRelationship, bool, error) {
	// Non-nil so the JSON contract stays "[]", never null (UI spreads these).
	implementations := []ContractCatalogRelationship{}
	callers := []ContractCatalogRelationship{}
	unresolved := []ContractCatalogRelationship{}
	total := 0
	locatorBudget := catalogLocatorLimit
	truncated := false
	// The declaration's protocol is known here, so only its own pack's
	// consumer domain and predicate triple are queried.
	filters := []struct {
		predicate, object, kind string
	}{
		{pack.registersPredicate, service, "implementation"},
		{"CALLS_OPERATION", "/" + service + "/" + method, "caller"},
		{pack.unresolvedCallPredicate, method, "unresolved_candidate"},
	}
	for _, repository := range certificate.Repositories {
		run, ok := certificateRun(repository, pack.consumerDomain)
		if !ok || run.Status != "published" {
			continue
		}
		for _, filter := range filters {
			remaining := catalogRelationshipLimit - total
			if remaining <= 0 || locatorBudget <= 0 {
				truncated = true
				break
			}
			rows, err := source.ListAssertions(ctx, store.AssertionQuery{
				Repo: repository.Repository, RunID: run.RunID,
				Predicate: filter.predicate, Object: filter.object,
				Limit: remaining, AllowTruncate: true,
			})
			if err != nil {
				return nil, nil, nil, false, err
			}
			if len(rows) > remaining {
				rows = rows[:remaining]
				truncated = true
			}
			for _, assertion := range rows {
				if err := validateCatalogAssertionScope(
					assertion, repository.Repository, run.RunID,
				); err != nil {
					return nil, nil, nil, false, err
				}
				claim, used, err := resolveCatalogClaim(
					ctx, source, assertion, run.Commit,
					min(catalogLocatorsPerClaimLimit, locatorBudget),
				)
				if err != nil {
					return nil, nil, nil, false, err
				}
				locatorBudget -= used
				relationship := ContractCatalogRelationship{
					Kind: filter.kind, Claim: claim,
				}
				switch {
				case filter.kind == "unresolved_candidate":
					relationship.Classification = "extractor_abstention"
					relationship.Reason = "OPERATION_DECLARATION_UNRESOLVED"
				case assertion.Lineage == declarationLineage:
					relationship.Classification = "proven"
				default:
					relationship.Classification = "unresolved_name_match"
					relationship.Reason = "DECLARATION_LINEAGE_UNPROVEN"
				}
				switch filter.kind {
				case "implementation":
					implementations = append(implementations, relationship)
				case "caller":
					callers = append(callers, relationship)
				default:
					unresolved = append(unresolved, relationship)
				}
				total++
			}
		}
		if truncated {
			break
		}
	}
	sortCatalogRelationships(implementations)
	sortCatalogRelationships(callers)
	sortCatalogRelationships(unresolved)
	return implementations, callers, unresolved, truncated, nil
}

func resolveCatalogClaim(
	ctx context.Context,
	source store.EvidenceStore,
	assertion store.Assertion,
	expectedCommit string,
	limit int,
) (ContractCatalogClaim, int, error) {
	if assertion.ID == "" || assertion.RunID == "" || assertion.Repo == "" ||
		assertion.Predicate == "" || assertion.Object == "" || len(assertion.Supporting) == 0 {
		return ContractCatalogClaim{}, 0, errors.New("catalog assertion is incomplete")
	}
	claim := ContractCatalogClaim{
		AssertionID: assertion.ID, RunID: assertion.RunID,
		Predicate: assertion.Predicate, Object: assertion.Object,
		Lineage: assertion.Lineage, Tier: assertion.Tier, CodeRole: assertion.CodeRole,
		Sources: make([]ContractCatalogSource, 0),
	}
	if assertion.Detail != "" {
		if !json.Valid([]byte(assertion.Detail)) {
			return ContractCatalogClaim{}, 0, errors.New("catalog assertion detail is not JSON")
		}
		claim.Detail = json.RawMessage(assertion.Detail)
	}
	supporting := append([]string(nil), assertion.Supporting...)
	sort.Strings(supporting)
	for _, atomID := range supporting {
		if len(claim.Sources) >= limit {
			claim.SourcesTruncated = true
			break
		}
		resolution, err := source.ResolveEvidence(
			ctx, assertion.Repo, assertion.RunID, atomID,
		)
		if err != nil {
			return ContractCatalogClaim{}, 0, err
		}
		if resolution == nil || resolution.Atom.ID != atomID ||
			resolution.Atom.StartByte < 0 ||
			resolution.Atom.EndByte <= resolution.Atom.StartByte {
			return ContractCatalogClaim{}, 0, errors.New("catalog evidence atom is inconsistent")
		}
		occurrences := append([]store.SnapshotEvidence(nil), resolution.Occurrences...)
		sort.Slice(occurrences, func(i, j int) bool {
			if occurrences[i].Path != occurrences[j].Path {
				return occurrences[i].Path < occurrences[j].Path
			}
			if occurrences[i].StartLine != occurrences[j].StartLine {
				return occurrences[i].StartLine < occurrences[j].StartLine
			}
			return occurrences[i].ID < occurrences[j].ID
		})
		for _, occurrence := range occurrences {
			if occurrence.Repo != assertion.Repo || occurrence.RunID != assertion.RunID ||
				occurrence.AtomID != atomID || occurrence.Commit != expectedCommit ||
				occurrence.VisibilityScope != "repo:"+assertion.Repo ||
				occurrence.ID == "" || occurrence.Path == "" ||
				occurrence.StartLine < 1 || occurrence.EndLine < occurrence.StartLine {
				return ContractCatalogClaim{}, 0, errors.New("catalog evidence occurrence scope is inconsistent")
			}
			if occurrence.Path != assertion.Subject {
				continue
			}
			if len(claim.Sources) >= limit {
				claim.SourcesTruncated = true
				break
			}
			claim.Sources = append(claim.Sources, ContractCatalogSource{
				Repository: assertion.Repo, Commit: occurrence.Commit,
				Path:      occurrence.Path,
				StartByte: resolution.Atom.StartByte, EndByte: resolution.Atom.EndByte,
				StartLine: occurrence.StartLine, EndLine: occurrence.EndLine,
				AssertionID: assertion.ID, RunID: assertion.RunID, AtomID: atomID,
			})
		}
	}
	if len(claim.Sources) == 0 {
		return ContractCatalogClaim{}, 0, errors.New("catalog assertion has no subject-matching source locator")
	}
	return claim, len(claim.Sources), nil
}

func catalogListItem(
	pack protocolPack,
	assertion store.Assertion,
	query ContractCatalogQuery,
) (ContractCatalogItem, bool, error) {
	service := assertion.Object
	method := ""
	kind := "service"
	if assertion.Predicate == "DECLARES_OPERATION" {
		var ok bool
		service, method, ok = strings.Cut(assertion.Object, "/")
		if !ok || service == "" || method == "" {
			return ContractCatalogItem{}, false, errors.New("catalog operation identity is inconsistent")
		}
		kind = "operation"
	}
	pkg := ""
	if index := strings.LastIndex(service, "."); index >= 0 {
		pkg = service[:index]
	}
	if query.Package != "" && query.Package != pkg ||
		query.Lineage != "" && query.Lineage != assertion.Lineage {
		return ContractCatalogItem{}, false, nil
	}
	item := ContractCatalogItem{
		Kind: kind, Protocol: pack.protocol, Repository: assertion.Repo,
		Lineage: assertion.Lineage, Package: pkg, ServiceFQN: service,
		Method: method,
	}
	if method != "" {
		item.Operation = "/" + service + "/" + method
	}
	return item, true, nil
}

func validateCatalogQuery(query ContractCatalogQuery) error {
	if query.Repository != "" {
		if err := validateCatalogRepository(query.Repository); err != nil {
			return err
		}
	}
	if query.Protocol != "" {
		if _, ok := packForProtocol(query.Protocol); !ok {
			return fmt.Errorf("protocol must be one of: %s", strings.Join(catalogProtocols(), ", "))
		}
	}
	for name, value := range map[string]string{
		"package": query.Package, "lineage": query.Lineage,
	} {
		if value != "" && !validCatalogFilter(value) {
			return fmt.Errorf("%s must be bounded UTF-8 without whitespace controls", name)
		}
	}
	return nil
}

func validateCatalogRepository(repository string) error {
	if !validCatalogFilter(repository) || strings.ContainsAny(repository, "\x00\r\n\t") {
		return errors.New("repository must be a bounded repository identity")
	}
	return nil
}

func validCatalogFilter(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < ' ' || character == 0x7f {
			return false
		}
	}
	return true
}

func catalogAuthenticated(ctx context.Context, opts Options) bool {
	return opts.Principal != nil && strings.TrimSpace(opts.Principal(ctx)) != ""
}

func catalogRepositoryVisible(repositories []store.Repo, name string) bool {
	index := sort.Search(len(repositories), func(index int) bool {
		return repositories[index].Name >= name
	})
	return index < len(repositories) && repositories[index].Name == name
}

func catalogCertificateScope(
	certificate *extract.CoverageCertificate,
	repository string,
) []extract.CertificateRepository {
	if repository == "" {
		return certificate.Repositories
	}
	for _, candidate := range certificate.Repositories {
		if candidate.Repository == repository {
			return []extract.CertificateRepository{candidate}
		}
	}
	return nil
}

func catalogCertificateRepository(
	certificate *extract.CoverageCertificate,
	repository string,
) (extract.CertificateRepository, bool) {
	for _, candidate := range certificate.Repositories {
		if candidate.Repository == repository {
			return candidate, true
		}
	}
	return extract.CertificateRepository{}, false
}

func catalogVisibilityContext(
	ctx context.Context,
	opts Options,
	repositories []store.Repo,
) VisibilityContext {
	certificate := &extract.CoverageCertificate{
		Repositories: make([]extract.CertificateRepository, len(repositories)),
	}
	for index, repository := range repositories {
		certificate.Repositories[index].Repository = repository.Name
	}
	return visibilityContext(ctx, opts, certificate)
}

func decodeCatalogCursor(
	encoded, queryDigest string,
	binding VisibilityContext,
	coverageDigest string,
) (catalogPosition, error) {
	if encoded == "" {
		return catalogPosition{}, nil
	}
	if len(encoded) > catalogCursorBytesLimit {
		return catalogPosition{}, huma.Error400BadRequest("contract catalog cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > catalogCursorBytesLimit {
		return catalogPosition{}, huma.Error400BadRequest("contract catalog cursor is invalid")
	}
	var cursor catalogCursor
	if !json.Valid(raw) {
		return catalogPosition{}, huma.Error400BadRequest("contract catalog cursor is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return catalogPosition{}, huma.Error400BadRequest("contract catalog cursor is invalid")
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if cursor.Schema != contractCatalogCursorVersion || checksum == "" ||
		checksum != digestJSON(cursor) {
		return catalogPosition{}, huma.Error400BadRequest("contract catalog cursor is invalid")
	}
	if cursor.QueryDigest != queryDigest {
		return catalogPosition{}, huma.Error400BadRequest("contract catalog cursor does not match the query")
	}
	if cursor.Principal != binding.Principal ||
		cursor.AuthorizationProvider != binding.AuthorizationProvider ||
		cursor.PermissionSnapshot != binding.PermissionSnapshot ||
		cursor.VisibleRepositorySetDigest != binding.VisibleRepositorySetDigest ||
		cursor.CoverageDigest != coverageDigest {
		return catalogPosition{}, huma.Error409Conflict("contract catalog cursor is no longer valid")
	}
	return catalogPosition{
		packIndex:       cursor.PackIndex,
		repositoryIndex: cursor.RepositoryIndex,
		after:           cloneAssertionCursor(cursor.After),
	}, nil
}

func encodeCatalogCursor(
	queryDigest string,
	binding VisibilityContext,
	coverageDigest string,
	position catalogPosition,
) (string, error) {
	cursor := catalogCursor{
		Schema: contractCatalogCursorVersion, QueryDigest: queryDigest,
		Principal: binding.Principal, AuthorizationProvider: binding.AuthorizationProvider,
		PermissionSnapshot:         binding.PermissionSnapshot,
		VisibleRepositorySetDigest: binding.VisibleRepositorySetDigest,
		CoverageDigest: coverageDigest, PackIndex: position.packIndex,
		RepositoryIndex: position.repositoryIndex,
		After:           cloneAssertionCursor(position.after),
	}
	cursor.Checksum = digestJSON(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validateCatalogAssertionScope(assertion store.Assertion, repository, runID string) error {
	if assertion.Repo != repository || assertion.RunID != runID ||
		assertion.ID == "" || assertion.Predicate == "" || assertion.Object == "" {
		return errors.New("contract catalog assertion scope is inconsistent")
	}
	return nil
}

func catalogAssertionCursor(assertion store.Assertion) *store.AssertionCursor {
	return &store.AssertionCursor{
		Predicate: assertion.Predicate, Subject: assertion.Subject,
		Object: assertion.Object, ID: assertion.ID, RunID: assertion.RunID,
	}
}

func catalogCursorBefore(rows []store.Assertion, index int) *store.AssertionCursor {
	if index == 0 {
		return nil
	}
	return catalogAssertionCursor(rows[index-1])
}

func cloneAssertionCursor(cursor *store.AssertionCursor) *store.AssertionCursor {
	if cursor == nil {
		return nil
	}
	copyOfCursor := *cursor
	return &copyOfCursor
}

func decodeCatalogDetail(raw, schema string, target any) error {
	if !json.Valid([]byte(raw)) {
		return errors.New("contract catalog fact detail is not JSON")
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode contract catalog fact detail: %w", err)
	}
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal([]byte(raw), &header); err != nil || header.Schema != schema {
		return errors.New("contract catalog fact detail schema is inconsistent")
	}
	return nil
}

func catalogFieldNumber(object, message string, pack protocolPack) (int, error) {
	prefix := message + "#"
	if !strings.HasPrefix(object, prefix) {
		return 0, errors.New("contract field identity is inconsistent")
	}
	number, err := strconv.Atoi(strings.TrimPrefix(object, prefix))
	if err != nil || number < pack.fieldMin || number > pack.fieldMax {
		return 0, errors.New("contract field number is inconsistent")
	}
	return number, nil
}

func catalogFieldMessageReference(
	detail ContractCatalogFieldDetail,
) *ContractCatalogTypeReference {
	if detail.Type != nil && detail.Type.Kind == "message" &&
		detail.Type.Resolution == "same_file" {
		copyOfReference := *detail.Type
		return &copyOfReference
	}
	if detail.Map != nil && detail.Map.Value.Kind == "message" &&
		detail.Map.Value.Resolution == "same_file" {
		copyOfReference := detail.Map.Value
		return &copyOfReference
	}
	return nil
}

func cloneMessageStack(stack map[string]bool) map[string]bool {
	copyOfStack := make(map[string]bool, len(stack)+1)
	for key, value := range stack {
		copyOfStack[key] = value
	}
	return copyOfStack
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func sortCatalogRelationships(rows []ContractCatalogRelationship) {
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i].Claim, rows[j].Claim
		if left.Sources[0].Repository != right.Sources[0].Repository {
			return left.Sources[0].Repository < right.Sources[0].Repository
		}
		if left.Predicate != right.Predicate {
			return left.Predicate < right.Predicate
		}
		return left.AssertionID < right.AssertionID
	})
}

func registerContractCatalogAPI(api huma.API, opts Options) {
	service := NewContractCatalogService(opts)
	if service == nil {
		return
	}
	type listIn struct {
		Repository string `query:"repository" maxLength:"1024"`
		Package    string `query:"package" maxLength:"1024"`
		Protocol   string `query:"protocol" maxLength:"128"`
		Lineage    string `query:"lineage" maxLength:"1024"`
		PageSize   int    `query:"page_size" minimum:"0" maximum:"100"`
		Cursor     string `query:"cursor" maxLength:"16384"`
	}
	type listOut struct {
		Body ContractCatalogList
	}
	huma.Get(api, "/api/contract_atlas", func(ctx context.Context, in *listIn) (*listOut, error) {
		result, err := service.List(ctx, ContractCatalogQuery{
			Repository: in.Repository, Package: in.Package,
			Protocol: in.Protocol, Lineage: in.Lineage,
		}, in.PageSize, in.Cursor)
		if err != nil {
			return nil, err
		}
		return &listOut{Body: *result}, nil
	})

	type operationIn struct {
		Repository string `query:"repository" required:"true" minLength:"1" maxLength:"1024"`
		Lineage    string `query:"lineage" required:"true" minLength:"1" maxLength:"1024"`
		Operation  string `query:"operation" required:"true" minLength:"1" maxLength:"1024"`
	}
	type operationOut struct {
		Body ContractCatalogOperation
	}
	huma.Get(api, "/api/contract_atlas/operation", func(ctx context.Context, in *operationIn) (*operationOut, error) {
		result, err := service.Operation(ctx, in.Repository, in.Lineage, in.Operation)
		if err != nil {
			return nil, err
		}
		return &operationOut{Body: *result}, nil
	})
}
