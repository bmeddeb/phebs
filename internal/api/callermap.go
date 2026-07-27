package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	callerMapSchemaVersion    = "caller-map-v1"
	callerMapCursorVersion    = "caller-map-cursor-v1"
	callerMapCapability       = "contract-caller-map"
	callerMapDefaultPage      = 50
	callerMapMaxPage          = 100
	callerMapScanLimit        = 50_000
	callerMapCursorLimit      = 16 << 10
	callerMapBuildAttempts    = 3
	callerMapExtractorVersion = "1.2.0"
)

// CallerMapService is the shared, transport-neutral exact-caller read engine.
// Huma is only an adapter; T20.11 binds MCP to these same methods.
type CallerMapService struct {
	opts Options
}

func NewCallerMapService(opts Options) *CallerMapService {
	if !opts.CallerMapEnabled ||
		opts.Store == nil || opts.Evidence == nil || opts.Principal == nil {
		return nil
	}
	return &CallerMapService{opts: opts}
}

type CallerMapEndpoint struct {
	Protocol   string `json:"protocol"`
	Repository string `json:"repository"`
	Lineage    string `json:"declaration_lineage"`
	Operation  string `json:"operation"`
}

type CallerMapQuery struct {
	Endpoint   CallerMapEndpoint `json:"endpoint"`
	Unit       string            `json:"unit,omitempty"`
	Owner      string            `json:"owner,omitempty"`
	PathPrefix string            `json:"path_prefix,omitempty"`
	CodeRole   string            `json:"code_role,omitempty"`
	Tier       string            `json:"tier,omitempty"`
	Freshness  string            `json:"freshness,omitempty"`  // any | fresh | stale
	Resolution string            `json:"resolution,omitempty"` // any | scip | syntax | unresolved
	Ordering   string            `json:"ordering,omitempty"`   // source | unit
}

type CallerMapUnitCandidate struct {
	ID              string   `json:"id"`
	BuildTargets    []string `json:"build_targets,omitempty"`
	Deployables     []string `json:"deployables,omitempty"`
	LogicalServices []string `json:"logical_services,omitempty"`
	Owners          []string `json:"owners,omitempty"`
}

type CallerMapUnitAttribution struct {
	State      string                   `json:"state"`
	Reason     string                   `json:"reason,omitempty"`
	Candidates []CallerMapUnitCandidate `json:"candidates,omitempty"`
	// CandidateTotal is the pre-truncation candidate count. The response
	// carries at most callerMapUnitCandidateLimit candidates so one
	// ambiguous row cannot blow the UI's frozen 500-node inventory bound;
	// a larger total makes the truncation explicit, never silent.
	CandidateTotal int `json:"candidate_total,omitempty"`
}

// callerMapUnitCandidateLimit bounds the candidates serialized per row —
// the T20.12 one-open-list expansion bound the BACKLOG froze at 64.
const callerMapUnitCandidateLimit = 64

type CallerMapSource struct {
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

type CallerMapRow struct {
	Classification     string                   `json:"classification"` // resolved_caller | extractor_abstention
	Resolution         string                   `json:"resolution"`
	Protocol           string                   `json:"protocol"`
	Operation          string                   `json:"operation"`
	DeclarationLineage string                   `json:"declaration_lineage,omitempty"`
	Tier               string                   `json:"tier"`
	CodeRole           string                   `json:"code_role,omitempty"`
	Fresh              bool                     `json:"fresh"`
	UnitGroup          string                   `json:"unit_group"`
	Unit               CallerMapUnitAttribution `json:"unit"`
	Source             CallerMapSource          `json:"source"`
	UnresolvedReason   string                   `json:"unresolved_reason,omitempty"`
}

type CallerMapGroup struct {
	Key   string `json:"key"`
	State string `json:"state"`
	Count int    `json:"count"`
}

type CallerMapPagination struct {
	Complete   bool   `json:"complete"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type CallerMapPage struct {
	SchemaVersion     string                      `json:"schema_version"`
	Query             CallerMapQuery              `json:"query"`
	Declaration       ContractCatalogClaim        `json:"declaration"`
	Rows              []CallerMapRow              `json:"rows"`
	Groups            []CallerMapGroup            `json:"groups,omitempty"`
	TotalMatchingRows int                         `json:"total_matching_rows"`
	Pagination        CallerMapPagination         `json:"pagination"`
	CoverageDigest    string                      `json:"coverage_digest"`
	AttributionDigest string                      `json:"attribution_digest"`
	Coverage          extract.CoverageCertificate `json:"coverage"`
	Caveat            string                      `json:"caveat"`
}

type callerMapDetail struct {
	Schema            string                   `json:"schema"`
	Resolution        string                   `json:"resolution"`
	Protocol          string                   `json:"protocol"`
	AttributionDigest string                   `json:"attribution_digest,omitempty"`
	UnresolvedReason  string                   `json:"unresolved_reason,omitempty"`
	UnitState         string                   `json:"unit_state"`
	UnitReason        string                   `json:"unit_reason,omitempty"`
	UnitCandidates    []CallerMapUnitCandidate `json:"unit_candidates,omitempty"`
}

type callerMapProjection struct {
	assertion store.Assertion
	commit    string
	fresh     bool
	path      string
	start     int
	end       int
	detail    callerMapDetail
	group     string
}

type callerMapCursor struct {
	Schema                     string `json:"schema"`
	QueryDigest                string `json:"query_digest"`
	Principal                  string `json:"principal"`
	AuthorizationProvider      string `json:"authorization_provider"`
	PermissionSnapshot         string `json:"permission_snapshot"`
	VisibleRepositorySetDigest string `json:"visible_repository_set_digest"`
	CoverageDigest             string `json:"coverage_digest"`
	AttributionDigest          string `json:"attribution_digest"`
	Offset                     int    `json:"offset"`
	Checksum                   string `json:"checksum"`
}

func (s *CallerMapService) List(
	ctx context.Context,
	query CallerMapQuery,
	pageSize int,
	cursor string,
) (*CallerMapPage, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("caller map unavailable")
	}
	if !catalogAuthenticated(ctx, s.opts) {
		return nil, huma.Error404NotFound("caller map not found")
	}
	pack, err := validateCallerMapQuery(query)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if pageSize == 0 {
		pageSize = callerMapDefaultPage
	}
	if pageSize < 1 || pageSize > callerMapMaxPage {
		return nil, huma.Error400BadRequest(
			fmt.Sprintf("page_size must be from 1 through %d", callerMapMaxPage),
		)
	}
	query = normalizeCallerMapQuery(query)
	queryDigest := digestJSON(struct {
		Query    CallerMapQuery `json:"query"`
		PageSize int            `json:"page_size"`
	}{query, pageSize})

	visible, err := visibleRepositories(ctx, s.opts)
	if err != nil {
		return nil, err
	}
	if !catalogRepositoryVisible(visible, query.Endpoint.Repository) {
		return nil, huma.Error404NotFound("caller map endpoint not found")
	}
	domains := []string{pack.declarationDomain, pack.callerDomain}
	for attempt := 0; attempt < callerMapBuildAttempts; attempt++ {
		certificate, err := extract.BuildCoverageCertificate(
			ctx, s.opts.Evidence, visible, domains,
		)
		if err != nil {
			return nil, huma.Error500InternalServerError("build caller map coverage", err)
		}
		binding := visibilityContext(ctx, s.opts, certificate)
		attributionDigest, err := callerMapAttributionDigest(certificate, pack.callerDomain)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				return nil, huma.Error409Conflict(
					"caller evidence must be republished by the current extractor",
				)
			}
			return nil, huma.Error500InternalServerError("bind caller attribution", err)
		}
		offset, err := decodeCallerMapCursor(
			cursor, queryDigest, binding, certificate.Digest, attributionDigest,
		)
		if err != nil {
			return nil, err
		}
		scanned := 0
		declaration, projections, collectErr := collectCallerMap(
			ctx, s.opts.Evidence, certificate, pack, query, &scanned,
		)

		confirmedVisible, visibleErr := visibleRepositories(ctx, s.opts)
		if visibleErr != nil {
			return nil, visibleErr
		}
		if catalogVisibilityContext(ctx, s.opts, confirmedVisible) != binding {
			return nil, huma.Error409Conflict(
				"caller map authorization changed while building the response; retry",
			)
		}
		confirmed, confirmErr := extract.BuildCoverageCertificate(
			ctx, s.opts.Evidence, confirmedVisible, domains,
		)
		if confirmErr != nil {
			return nil, huma.Error500InternalServerError("confirm caller map coverage", confirmErr)
		}
		confirmedAttribution, attrErr := callerMapAttributionDigest(
			confirmed, pack.callerDomain,
		)
		if attrErr != nil {
			if errors.Is(attrErr, store.ErrConflict) {
				return nil, huma.Error409Conflict(
					"caller evidence must be republished by the current extractor",
				)
			}
			return nil, huma.Error500InternalServerError("confirm caller attribution", attrErr)
		}
		if confirmed.Digest != certificate.Digest ||
			confirmedAttribution != attributionDigest {
			if cursor != "" {
				return nil, huma.Error409Conflict("caller map cursor is no longer valid")
			}
			continue
		}
		if collectErr != nil {
			switch {
			case errors.Is(collectErr, store.ErrNotFound):
				return nil, huma.Error404NotFound("caller map endpoint not found")
			case errors.Is(collectErr, store.ErrConflict):
				continue
			case errors.Is(collectErr, store.ErrResultLimit):
				return nil, huma.Error422UnprocessableEntity(
					"caller map exceeded its bounded row scan; narrow the filters",
				)
			default:
				return nil, huma.Error500InternalServerError("build caller map", collectErr)
			}
		}
		sortCallerMapProjections(projections, query.Ordering)
		if offset < 0 || offset > len(projections) {
			return nil, huma.Error409Conflict("caller map cursor is no longer valid")
		}
		end := min(offset+pageSize, len(projections))
		rows := make([]CallerMapRow, 0, end-offset)
		for _, projection := range projections[offset:end] {
			row, err := resolveCallerMapProjection(
				ctx, s.opts.Evidence, pack.protocol, projection,
			)
			if err != nil {
				return nil, huma.Error500InternalServerError(
					"resolve caller map citation", err,
				)
			}
			rows = append(rows, row)
		}
		pagination := CallerMapPagination{Complete: end == len(projections)}
		if !pagination.Complete {
			pagination.NextCursor, err = encodeCallerMapCursor(
				queryDigest, binding, certificate.Digest, attributionDigest, end,
			)
			if err != nil {
				return nil, huma.Error500InternalServerError(
					"encode caller map cursor", err,
				)
			}
		}
		return &CallerMapPage{
			SchemaVersion: callerMapSchemaVersion, Query: query,
			Declaration: declaration, Rows: rows,
			Groups:            callerMapGroups(rows, query.Ordering),
			TotalMatchingRows: len(projections), Pagination: pagination,
			CoverageDigest:    certificate.Digest,
			AttributionDigest: attributionDigest, Coverage: *certificate,
			Caveat: "Static source evidence only; unresolved and unattributed rows are retained, and no absence or runtime-completeness conclusion is implied.",
		}, nil
	}
	return nil, huma.Error409Conflict("evidence changed while building the caller map; retry")
}

func normalizeCallerMapQuery(query CallerMapQuery) CallerMapQuery {
	if query.Freshness == "" {
		query.Freshness = "any"
	}
	if query.Resolution == "" {
		query.Resolution = "any"
	}
	if query.Ordering == "" {
		query.Ordering = "source"
	}
	return query
}

func validateCallerMapQuery(query CallerMapQuery) (protocolPack, error) {
	pack, ok := packForProtocol(query.Endpoint.Protocol)
	if !ok {
		return protocolPack{}, fmt.Errorf(
			"protocol must be one of: %s", strings.Join(catalogProtocols(), ", "),
		)
	}
	if err := validateCatalogRepository(query.Endpoint.Repository); err != nil {
		return protocolPack{}, err
	}
	if !validQueryIdentity(query.Endpoint.Lineage) {
		return protocolPack{}, errors.New("declaration_lineage must be a bounded contract identity")
	}
	if err := validateOperation(query.Endpoint.Operation); err != nil {
		return protocolPack{}, err
	}
	for name, value := range map[string]string{
		"unit": query.Unit, "owner": query.Owner, "path_prefix": query.PathPrefix,
	} {
		if value != "" && !validCatalogFilter(value) {
			return protocolPack{}, fmt.Errorf("%s must be a bounded filter", name)
		}
	}
	if query.CodeRole != "" && !stringIn(query.CodeRole,
		"production", "test", "mock", "generated", "vendor") {
		return protocolPack{}, errors.New("code_role is invalid")
	}
	if query.Tier != "" && !stringIn(query.Tier,
		store.TierExact, store.TierDerived, store.TierHeuristic, store.TierUnresolved) {
		return protocolPack{}, errors.New("tier is invalid")
	}
	if query.Freshness != "" && !stringIn(query.Freshness, "any", "fresh", "stale") {
		return protocolPack{}, errors.New("freshness is invalid")
	}
	if query.Resolution != "" &&
		!stringIn(query.Resolution, "any", "scip", "syntax", "unresolved") {
		return protocolPack{}, errors.New("resolution is invalid")
	}
	if query.Ordering != "" && !stringIn(query.Ordering, "source", "unit") {
		return protocolPack{}, errors.New("ordering is invalid")
	}
	return pack, nil
}

// collectCallerMap accumulates scanned-row counts into *scanned so callers
// composing multiple collections (the T20.13 comparison) share one bounded
// budget: "combined 50,000" means combined, not per side.
func collectCallerMap(
	ctx context.Context,
	source store.EvidenceStore,
	certificate *extract.CoverageCertificate,
	pack protocolPack,
	query CallerMapQuery,
	scanned *int,
) (ContractCatalogClaim, []callerMapProjection, error) {
	repository, ok := catalogCertificateRepository(
		certificate, query.Endpoint.Repository,
	)
	if !ok {
		return ContractCatalogClaim{}, nil, store.ErrNotFound
	}
	declarationRun, ok := certificateRun(repository, pack.declarationDomain)
	if !ok || declarationRun.Status != "published" {
		return ContractCatalogClaim{}, nil, store.ErrNotFound
	}
	declarations, err := source.ListAssertions(ctx, store.AssertionQuery{
		Repo: query.Endpoint.Repository, RunID: declarationRun.RunID,
		Predicate: "DECLARES_OPERATION",
		Object:    strings.TrimPrefix(query.Endpoint.Operation, "/"),
		Lineage:   query.Endpoint.Lineage, Limit: 1,
	})
	if err != nil {
		return ContractCatalogClaim{}, nil, err
	}
	if len(declarations) != 1 {
		return ContractCatalogClaim{}, nil, store.ErrNotFound
	}
	declaration, _, err := resolveCatalogClaim(
		ctx, source, declarations[0], declarationRun.Commit,
		catalogLocatorsPerClaimLimit,
	)
	if err != nil {
		return ContractCatalogClaim{}, nil, err
	}

	projections := make([]callerMapProjection, 0)
	for _, coveredRepository := range certificate.Repositories {
		run, ok := certificateRun(coveredRepository, pack.callerDomain)
		if !ok || run.Status != "published" {
			continue
		}
		filters := []store.ReverseAssertionQuery{
			{
				Repo: coveredRepository.Repository, RunID: run.RunID,
				Predicate: "CALLS_OPERATION", Object: query.Endpoint.Operation,
				Lineage: query.Endpoint.Lineage, Limit: callerMapMaxPage,
			},
			{
				Repo: coveredRepository.Repository, RunID: run.RunID,
				Predicate: "UNRESOLVED_CALLER", Object: query.Endpoint.Operation,
				Limit: callerMapMaxPage,
			},
		}
		for _, filter := range filters {
			for {
				page, err := source.ListReverseAssertions(ctx, filter)
				if err != nil {
					return ContractCatalogClaim{}, nil, err
				}
				for _, assertion := range page.Assertions {
					*scanned++
					if *scanned > callerMapScanLimit {
						return ContractCatalogClaim{}, nil, store.ErrResultLimit
					}
					projection, include, err := projectCallerMapAssertion(
						assertion, coveredRepository.Repository, run, pack, query,
					)
					if err != nil {
						return ContractCatalogClaim{}, nil, err
					}
					if include {
						projections = append(projections, projection)
					}
				}
				if page.Next == nil {
					break
				}
				filter.After = page.Next
			}
		}
	}
	return declaration, projections, nil
}

func projectCallerMapAssertion(
	assertion store.Assertion,
	repository string,
	run extract.CertificateRun,
	pack protocolPack,
	query CallerMapQuery,
) (callerMapProjection, bool, error) {
	if err := validateCatalogAssertionScope(assertion, repository, run.RunID); err != nil {
		return callerMapProjection{}, false, err
	}
	if assertion.Object != query.Endpoint.Operation ||
		assertion.Predicate == "CALLS_OPERATION" &&
			assertion.Lineage != query.Endpoint.Lineage ||
		assertion.Predicate == "UNRESOLVED_CALLER" && assertion.Lineage != "" {
		return callerMapProjection{}, false, errors.New("caller assertion identity is inconsistent")
	}
	var detail callerMapDetail
	if err := decodeCatalogDetail(assertion.Detail, "go-caller-detail-v1", &detail); err != nil {
		return callerMapProjection{}, false, err
	}
	if detail.Protocol != protocolExtractorName(pack.protocol) ||
		!stringIn(detail.Resolution, "scip", "syntax") {
		return callerMapProjection{}, false, errors.New("caller assertion detail is inconsistent")
	}
	attributionDigest, err := callerRunAttribution(run)
	if err != nil {
		return callerMapProjection{}, false, err
	}
	if attributionDigest == "unavailable" && detail.AttributionDigest != "" ||
		attributionDigest != "unavailable" && detail.AttributionDigest != attributionDigest {
		return callerMapProjection{}, false, errors.New("caller attribution binding is inconsistent")
	}
	pathValue, start, end, err := parseCallerSubject(assertion.Subject)
	if err != nil {
		return callerMapProjection{}, false, err
	}
	group := callerUnitGroup(detail)
	projection := callerMapProjection{
		assertion: assertion, commit: run.Commit, fresh: run.Fresh,
		path: pathValue, start: start, end: end, detail: detail, group: group,
	}
	if !callerMapProjectionMatches(projection, query) {
		return callerMapProjection{}, false, nil
	}
	return projection, true, nil
}

func protocolExtractorName(protocol string) string {
	if protocol == "protobuf" {
		return "grpc"
	}
	return protocol
}

func parseCallerSubject(subject string) (string, int, int, error) {
	colon := strings.LastIndex(subject, ":")
	dash := strings.LastIndex(subject, "-")
	if colon <= 0 || dash <= colon+1 || dash == len(subject)-1 {
		return "", 0, 0, errors.New("caller assertion subject is inconsistent")
	}
	start, startErr := strconv.Atoi(subject[colon+1 : dash])
	end, endErr := strconv.Atoi(subject[dash+1:])
	if startErr != nil || endErr != nil || start < 0 || end <= start {
		return "", 0, 0, errors.New("caller assertion span is inconsistent")
	}
	return subject[:colon], start, end, nil
}

func callerMapProjectionMatches(
	projection callerMapProjection,
	query CallerMapQuery,
) bool {
	if query.PathPrefix != "" && !strings.HasPrefix(projection.path, query.PathPrefix) ||
		query.CodeRole != "" && projection.assertion.CodeRole != query.CodeRole ||
		query.Tier != "" && projection.assertion.Tier != query.Tier ||
		query.Freshness == "fresh" && !projection.fresh ||
		query.Freshness == "stale" && projection.fresh {
		return false
	}
	resolution := projection.detail.Resolution
	if projection.assertion.Predicate == "UNRESOLVED_CALLER" {
		resolution = "unresolved"
	}
	if query.Resolution != "any" && query.Resolution != resolution {
		return false
	}
	if query.Unit == "" && query.Owner == "" {
		return true
	}
	for _, candidate := range projection.detail.UnitCandidates {
		if query.Unit != "" && !callerUnitMatches(candidate, query.Unit) {
			continue
		}
		if query.Owner != "" && !stringSliceContains(candidate.Owners, query.Owner) {
			continue
		}
		return true
	}
	return false
}

func callerUnitMatches(candidate CallerMapUnitCandidate, value string) bool {
	return candidate.ID == value ||
		stringSliceContains(candidate.BuildTargets, value) ||
		stringSliceContains(candidate.Deployables, value) ||
		stringSliceContains(candidate.LogicalServices, value)
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func callerUnitGroup(detail callerMapDetail) string {
	if len(detail.UnitCandidates) == 0 {
		return detail.UnitState
	}
	ids := make([]string, 0, len(detail.UnitCandidates))
	for _, candidate := range detail.UnitCandidates {
		ids = append(ids, candidate.ID)
	}
	sort.Strings(ids)
	return detail.UnitState + ":" + strings.Join(ids, ",")
}

func sortCallerMapProjections(rows []callerMapProjection, ordering string) {
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if ordering == "unit" && left.group != right.group {
			return left.group < right.group
		}
		for _, pair := range [][2]string{
			{left.assertion.Repo, right.assertion.Repo},
			{left.path, right.path},
		} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		if left.start != right.start {
			return left.start < right.start
		}
		return left.assertion.ID < right.assertion.ID
	})
}

func resolveCallerMapProjection(
	ctx context.Context,
	source store.EvidenceStore,
	protocol string,
	projection callerMapProjection,
) (CallerMapRow, error) {
	if len(projection.assertion.Supporting) == 0 {
		return CallerMapRow{}, errors.New("caller assertion has no supporting evidence")
	}
	atomIDs := append([]string(nil), projection.assertion.Supporting...)
	sort.Strings(atomIDs)
	for _, atomID := range atomIDs {
		resolution, err := source.ResolveEvidence(
			ctx, projection.assertion.Repo, projection.assertion.RunID, atomID,
		)
		if err != nil {
			return CallerMapRow{}, err
		}
		if resolution == nil || resolution.Atom.ID != atomID ||
			resolution.Atom.StartByte != projection.start ||
			resolution.Atom.EndByte != projection.end {
			continue
		}
		for _, occurrence := range resolution.Occurrences {
			if occurrence.Repo != projection.assertion.Repo ||
				occurrence.RunID != projection.assertion.RunID ||
				occurrence.AtomID != atomID ||
				occurrence.Commit != projection.commit ||
				occurrence.Path != projection.path ||
				occurrence.VisibilityScope != "repo:"+projection.assertion.Repo ||
				occurrence.StartLine < 1 ||
				occurrence.EndLine < occurrence.StartLine {
				continue
			}
			classification := "resolved_caller"
			if projection.assertion.Predicate == "UNRESOLVED_CALLER" {
				classification = "extractor_abstention"
			}
			resolutionName := projection.detail.Resolution
			if classification == "extractor_abstention" {
				resolutionName = "unresolved"
			}
			return CallerMapRow{
				Classification: classification,
				Resolution:     resolutionName,
				Protocol:       protocol, Operation: projection.assertion.Object,
				DeclarationLineage: projection.assertion.Lineage,
				Tier:               projection.assertion.Tier,
				CodeRole:           projection.assertion.CodeRole,
				Fresh:              projection.fresh, UnitGroup: projection.group,
				Unit: boundedUnitAttribution(
					projection.detail.UnitState,
					projection.detail.UnitReason,
					projection.detail.UnitCandidates,
				),
				Source: CallerMapSource{
					Repository: projection.assertion.Repo,
					Commit:     projection.commit, Path: projection.path,
					StartByte: projection.start, EndByte: projection.end,
					StartLine: occurrence.StartLine, EndLine: occurrence.EndLine,
					AssertionID: projection.assertion.ID,
					RunID:       projection.assertion.RunID, AtomID: atomID,
				},
				UnresolvedReason: projection.detail.UnresolvedReason,
			}, nil
		}
	}
	return CallerMapRow{}, errors.New("caller assertion has no exact source locator")
}

func boundedUnitAttribution(state, reason string, candidates []CallerMapUnitCandidate) CallerMapUnitAttribution {
	attribution := CallerMapUnitAttribution{
		State:          state,
		Reason:         reason,
		CandidateTotal: len(candidates),
		Candidates:     append([]CallerMapUnitCandidate(nil), candidates...),
	}
	if len(attribution.Candidates) > callerMapUnitCandidateLimit {
		attribution.Candidates = attribution.Candidates[:callerMapUnitCandidateLimit]
	}
	return attribution
}

func callerMapGroups(rows []CallerMapRow, ordering string) []CallerMapGroup {
	if ordering != "unit" {
		return nil
	}
	var groups []CallerMapGroup
	for _, row := range rows {
		if len(groups) == 0 || groups[len(groups)-1].Key != row.UnitGroup {
			groups = append(groups, CallerMapGroup{
				Key: row.UnitGroup, State: row.Unit.State,
			})
		}
		groups[len(groups)-1].Count++
	}
	return groups
}

func callerMapAttributionDigest(
	certificate *extract.CoverageCertificate,
	domain string,
) (string, error) {
	type binding struct {
		Repository string `json:"repository"`
		RunID      string `json:"run_id,omitempty"`
		Digest     string `json:"digest"`
	}
	values := make([]binding, 0, len(certificate.Repositories))
	for _, repository := range certificate.Repositories {
		run, ok := certificateRun(repository, domain)
		if !ok || run.Status != "published" {
			values = append(values, binding{
				Repository: repository.Repository, Digest: "unpublished",
			})
			continue
		}
		digest, err := callerRunAttribution(run)
		if err != nil {
			return "", err
		}
		values = append(values, binding{
			Repository: repository.Repository, RunID: run.RunID, Digest: digest,
		})
	}
	return digestJSON(values), nil
}

func callerRunAttribution(run extract.CertificateRun) (string, error) {
	digest := "unavailable"
	for _, protocol := range run.Protocols {
		if !strings.HasPrefix(protocol, "attribution-") {
			continue
		}
		if digest != "unavailable" {
			return "", errors.New("caller run has multiple attribution bindings")
		}
		if run.Extractor != callerMapExtractorVersion {
			return "", fmt.Errorf(
				"caller run %s binds attribution under extractor %q: %w",
				run.RunID, run.Extractor, store.ErrConflict,
			)
		}
		raw := strings.TrimPrefix(protocol, "attribution-")
		decoded, err := hex.DecodeString(raw)
		if err != nil || len(decoded) != 32 || raw != strings.ToLower(raw) {
			return "", errors.New("caller run attribution binding is malformed")
		}
		digest = "sha256:" + raw
	}
	return digest, nil
}

func decodeCallerMapCursor(
	encoded, queryDigest string,
	binding VisibilityContext,
	coverageDigest, attributionDigest string,
) (int, error) {
	if encoded == "" {
		return 0, nil
	}
	if len(encoded) > callerMapCursorLimit {
		return 0, huma.Error400BadRequest("caller map cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > callerMapCursorLimit || !json.Valid(raw) {
		return 0, huma.Error400BadRequest("caller map cursor is invalid")
	}
	var cursor callerMapCursor
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return 0, huma.Error400BadRequest("caller map cursor is invalid")
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if cursor.Schema != callerMapCursorVersion || checksum == "" ||
		checksum != digestJSON(cursor) || cursor.Offset < 1 {
		return 0, huma.Error400BadRequest("caller map cursor is invalid")
	}
	if cursor.QueryDigest != queryDigest {
		return 0, huma.Error400BadRequest("caller map cursor does not match the query")
	}
	if cursor.Principal != binding.Principal ||
		cursor.AuthorizationProvider != binding.AuthorizationProvider ||
		cursor.PermissionSnapshot != binding.PermissionSnapshot ||
		cursor.VisibleRepositorySetDigest != binding.VisibleRepositorySetDigest ||
		cursor.CoverageDigest != coverageDigest ||
		cursor.AttributionDigest != attributionDigest {
		return 0, huma.Error409Conflict("caller map cursor is no longer valid")
	}
	return cursor.Offset, nil
}

func encodeCallerMapCursor(
	queryDigest string,
	binding VisibilityContext,
	coverageDigest, attributionDigest string,
	offset int,
) (string, error) {
	cursor := callerMapCursor{
		Schema: callerMapCursorVersion, QueryDigest: queryDigest,
		Principal:                  binding.Principal,
		AuthorizationProvider:      binding.AuthorizationProvider,
		PermissionSnapshot:         binding.PermissionSnapshot,
		VisibleRepositorySetDigest: binding.VisibleRepositorySetDigest,
		CoverageDigest:             coverageDigest, AttributionDigest: attributionDigest,
		Offset: offset,
	}
	cursor.Checksum = digestJSON(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func stringIn(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func registerCallerMapAPI(api huma.API, opts Options) {
	service := opts.CallerMap
	if service == nil {
		service = NewCallerMapService(opts)
	}
	if service == nil {
		return
	}
	type callerMapIn struct {
		Protocol   string `query:"protocol" required:"true" maxLength:"128"`
		Repository string `query:"repository" required:"true" maxLength:"1024"`
		Lineage    string `query:"lineage" required:"true" maxLength:"1024"`
		Operation  string `query:"operation" required:"true" maxLength:"1024"`
		Unit       string `query:"unit" maxLength:"1024"`
		Owner      string `query:"owner" maxLength:"1024"`
		PathPrefix string `query:"path_prefix" maxLength:"1024"`
		CodeRole   string `query:"code_role" maxLength:"32"`
		Tier       string `query:"tier" maxLength:"32"`
		Freshness  string `query:"freshness" maxLength:"16"`
		Resolution string `query:"resolution" maxLength:"16"`
		Ordering   string `query:"ordering" maxLength:"16"`
		PageSize   int    `query:"page_size" minimum:"0" maximum:"100"`
		Cursor     string `query:"cursor" maxLength:"16384"`
	}
	type callerMapOut struct {
		Body CallerMapPage
	}
	huma.Get(api, "/api/contract_callers", func(
		ctx context.Context,
		in *callerMapIn,
	) (*callerMapOut, error) {
		result, err := service.List(ctx, CallerMapQuery{
			Endpoint: CallerMapEndpoint{
				Protocol: in.Protocol, Repository: in.Repository,
				Lineage: in.Lineage, Operation: in.Operation,
			},
			Unit: in.Unit, Owner: in.Owner, PathPrefix: in.PathPrefix,
			CodeRole: in.CodeRole, Tier: in.Tier, Freshness: in.Freshness,
			Resolution: in.Resolution, Ordering: in.Ordering,
		}, in.PageSize, in.Cursor)
		if err != nil {
			return nil, err
		}
		return &callerMapOut{Body: *result}, nil
	})
}
