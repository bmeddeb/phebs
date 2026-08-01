package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	callerComparisonSchemaVersion       = "caller-comparison-v2"
	legacyCallerComparisonSchemaVersion = "caller-comparison-v1"
	callerComparisonCursorVersion       = "caller-comparison-cursor-v1"
	callerComparisonCapability          = "contract-caller-comparison"
	callerComparisonCitationLimit       = 4
)

// CallerComparisonService is the shared old-to-replacement classification
// engine. The public constructor binds the same exact repository-overlay
// engine as Caller Map. The legacy constructor exists only for Workbench
// until T30.6l composes that product's revision snapshot with exact callers.
type CallerComparisonService struct {
	opts  Options
	exact *exactCallerMapService
}

func NewCallerComparisonService(opts Options) *CallerComparisonService {
	if !opts.CallerMapEnabled ||
		opts.Store == nil || opts.Evidence == nil || opts.Principal == nil ||
		opts.CallerReader == nil || strings.TrimSpace(opts.DataDir) == "" {
		return nil
	}
	callerMap := opts.CallerMap
	if callerMap == nil {
		callerMap = NewCallerMapService(opts)
	}
	if callerMap == nil || callerMap.exact == nil {
		return nil
	}
	return &CallerComparisonService{opts: opts, exact: callerMap.exact}
}

// NewLegacyCallerComparisonService retains the pre-T30.6k evidence-backed
// comparison solely for Workbench Impact's separately scheduled T30.6l
// migration and historical acceptance fixtures.
func NewLegacyCallerComparisonService(opts Options) *CallerComparisonService {
	if !opts.CallerMapEnabled ||
		opts.Store == nil || opts.Evidence == nil || opts.Principal == nil {
		return nil
	}
	return &CallerComparisonService{opts: opts}
}

type CallerComparisonQuery struct {
	Old            CallerMapEndpoint `json:"old"`
	Replacement    CallerMapEndpoint `json:"replacement"`
	Unit           string            `json:"unit,omitempty"`
	Owner          string            `json:"owner,omitempty"`
	PathPrefix     string            `json:"path_prefix,omitempty"`
	CodeRole       string            `json:"code_role,omitempty"`
	Tier           string            `json:"tier,omitempty"`
	Freshness      string            `json:"freshness,omitempty"`
	Resolution     string            `json:"resolution,omitempty"`
	Ordering       string            `json:"ordering,omitempty"`
	Level          string            `json:"level"` // occurrence | unit
	Classification string            `json:"classification,omitempty"`
}

type CallerComparisonSnapshot struct {
	Endpoint          CallerMapEndpoint     `json:"endpoint"`
	Declaration       *ContractCatalogClaim `json:"declaration,omitempty"`
	Generation        *CallerMapGeneration  `json:"generation,omitempty"`
	MatchingRowsState string                `json:"matching_rows_state,omitempty"`
	CoverageDigest    string                `json:"coverage_digest,omitempty"`
	AttributionDigest string                `json:"attribution_digest,omitempty"`
}

type CallerComparisonSide struct {
	OccurrenceCount int            `json:"occurrence_count"`
	Rows            []CallerMapRow `json:"rows"`
	RowsTruncated   bool           `json:"rows_truncated"`
}

type CallerComparisonRow struct {
	Level          string                  `json:"level"`
	Key            string                  `json:"key"`
	Classification string                  `json:"classification"`
	Unit           *CallerMapUnitCandidate `json:"unit,omitempty"`
	Old            CallerComparisonSide    `json:"old"`
	Replacement    CallerComparisonSide    `json:"replacement"`
}

type CallerComparisonPage struct {
	SchemaVersion     string                       `json:"schema_version"`
	Query             CallerComparisonQuery        `json:"query"`
	Old               CallerComparisonSnapshot     `json:"old"`
	Replacement       CallerComparisonSnapshot     `json:"replacement"`
	Rows              []CallerComparisonRow        `json:"rows"`
	TotalRows         *int                         `json:"total_rows,omitempty"`
	MatchingRowsState string                       `json:"matching_rows_state,omitempty"`
	Pagination        CallerMapPagination          `json:"pagination"`
	Coverage          *extract.CoverageCertificate `json:"coverage,omitempty"`
	Caveat            string                       `json:"caveat"`
}

// CallerComparisonExactSnapshot is the public T30.6k endpoint contract. The
// legacy Workbench snapshot remains separately represented by
// CallerComparisonSnapshot until T30.6l, but public HTTP and MCP schemas must
// not make exact generation/state fields optional merely for that migration.
type CallerComparisonExactSnapshot struct {
	Endpoint          CallerMapEndpoint     `json:"endpoint"`
	Declaration       *ContractCatalogClaim `json:"declaration,omitempty"`
	Generation        CallerMapGeneration   `json:"generation"`
	MatchingRowsState string                `json:"matching_rows_state" enum:"exact,unavailable"`
}

// CallerComparisonExactPage excludes every legacy evidence-plane digest and
// coverage field. TotalRows remains optional because an unavailable side is a
// typed gap, never an exact zero.
type CallerComparisonExactPage struct {
	SchemaVersion     string                        `json:"schema_version" enum:"caller-comparison-v2"`
	Query             CallerComparisonQuery         `json:"query"`
	Old               CallerComparisonExactSnapshot `json:"old"`
	Replacement       CallerComparisonExactSnapshot `json:"replacement"`
	Rows              []CallerComparisonRow         `json:"rows"`
	TotalRows         *int                          `json:"total_rows,omitempty"`
	MatchingRowsState string                        `json:"matching_rows_state" enum:"exact,unavailable"`
	Pagination        CallerMapPagination           `json:"pagination"`
	Caveat            string                        `json:"caveat"`
}

// AsExactCallerComparisonPage validates and projects the shared service result
// onto its public v2 contract. It is exported so the MCP adapter can use the
// identical envelope instead of independently reclassifying or reshaping it.
func AsExactCallerComparisonPage(
	page *CallerComparisonPage,
) (*CallerComparisonExactPage, error) {
	if page == nil || page.SchemaVersion != callerComparisonSchemaVersion ||
		page.Old.Generation == nil || page.Replacement.Generation == nil ||
		!stringIn(page.MatchingRowsState, "exact", "unavailable") ||
		!stringIn(page.Old.MatchingRowsState, "exact", "unavailable") ||
		!stringIn(page.Replacement.MatchingRowsState, "exact", "unavailable") {
		return nil, errors.New("caller comparison exact envelope is invalid")
	}
	if page.MatchingRowsState == "exact" {
		if page.Old.MatchingRowsState != "exact" ||
			page.Replacement.MatchingRowsState != "exact" || page.TotalRows == nil {
			return nil, errors.New("caller comparison exact envelope is inconsistent")
		}
	} else if page.TotalRows != nil || len(page.Rows) != 0 ||
		page.Pagination.NextCursor != "" || !page.Pagination.Complete ||
		page.Old.MatchingRowsState == "exact" &&
			page.Replacement.MatchingRowsState == "exact" {
		return nil, errors.New("caller comparison gap envelope is inconsistent")
	}
	return &CallerComparisonExactPage{
		SchemaVersion: page.SchemaVersion, Query: page.Query,
		Old: CallerComparisonExactSnapshot{
			Endpoint: page.Old.Endpoint, Declaration: page.Old.Declaration,
			Generation:        *page.Old.Generation,
			MatchingRowsState: page.Old.MatchingRowsState,
		},
		Replacement: CallerComparisonExactSnapshot{
			Endpoint:          page.Replacement.Endpoint,
			Declaration:       page.Replacement.Declaration,
			Generation:        *page.Replacement.Generation,
			MatchingRowsState: page.Replacement.MatchingRowsState,
		},
		Rows: page.Rows, TotalRows: page.TotalRows,
		MatchingRowsState: page.MatchingRowsState,
		Pagination:        page.Pagination, Caveat: page.Caveat,
	}, nil
}

type callerComparisonCursor struct {
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

type callerComparisonEntry struct {
	key         string
	sourceSort  string
	unitSort    string
	unresolved  bool
	unit        *CallerMapUnitCandidate
	old         []callerMapProjection
	replacement []callerMapProjection
}

func (s *CallerComparisonService) Compare(
	ctx context.Context,
	query CallerComparisonQuery,
	pageSize int,
	cursor string,
) (*CallerComparisonPage, error) {
	if s != nil && s.exact != nil {
		return s.compareExact(ctx, query, pageSize, cursor)
	}
	return s.compareLegacy(ctx, query, pageSize, cursor)
}

func (s *CallerComparisonService) compareLegacy(
	ctx context.Context,
	query CallerComparisonQuery,
	pageSize int,
	cursor string,
) (*CallerComparisonPage, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("caller comparison unavailable")
	}
	if !catalogAuthenticated(ctx, s.opts) {
		return nil, huma.Error404NotFound("caller comparison not found")
	}
	query = normalizeCallerComparisonQuery(query)
	oldPack, err := validateCallerMapQuery(comparisonCallerQuery(query, query.Old))
	if err != nil {
		return nil, huma.Error400BadRequest("old endpoint: " + err.Error())
	}
	replacementPack, err := validateCallerMapQuery(
		comparisonCallerQuery(query, query.Replacement),
	)
	if err != nil {
		return nil, huma.Error400BadRequest("replacement endpoint: " + err.Error())
	}
	if query.Level != "occurrence" && query.Level != "unit" {
		return nil, huma.Error400BadRequest("level must be occurrence or unit")
	}
	if query.Classification != "" && !stringIn(
		query.Classification,
		"old_only_evidence", "both_evidence", "new_only_evidence", "unresolved",
	) {
		return nil, huma.Error400BadRequest("classification is invalid")
	}
	if pageSize == 0 {
		pageSize = callerMapDefaultPage
	}
	if pageSize < 1 || pageSize > callerMapMaxPage {
		return nil, huma.Error400BadRequest(
			fmt.Sprintf("page_size must be from 1 through %d", callerMapMaxPage),
		)
	}
	queryDigest := digestJSON(struct {
		Query    CallerComparisonQuery `json:"query"`
		PageSize int                   `json:"page_size"`
	}{query, pageSize})

	visible, err := visibleRepositories(ctx, s.opts)
	if err != nil {
		return nil, err
	}
	if !catalogRepositoryVisible(visible, query.Old.Repository) ||
		!catalogRepositoryVisible(visible, query.Replacement.Repository) {
		return nil, huma.Error404NotFound("caller comparison endpoint not found")
	}
	domains := comparisonDomains(oldPack, replacementPack)
	for attempt := 0; attempt < callerMapBuildAttempts; attempt++ {
		certificate, err := extract.BuildCoverageCertificate(
			ctx, s.opts.Evidence, visible, domains,
		)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"build caller comparison coverage", err,
			)
		}
		binding := visibilityContext(ctx, s.opts, certificate)
		oldAttribution, err := callerMapAttributionDigest(
			certificate, oldPack.callerDomain,
		)
		if err != nil {
			return nil, comparisonAttributionError(err)
		}
		replacementAttribution, err := callerMapAttributionDigest(
			certificate, replacementPack.callerDomain,
		)
		if err != nil {
			return nil, comparisonAttributionError(err)
		}
		attributionDigest := digestJSON(struct {
			Old         string `json:"old"`
			Replacement string `json:"replacement"`
		}{oldAttribution, replacementAttribution})
		offset, err := decodeCallerComparisonCursor(
			cursor, queryDigest, binding, certificate.Digest, attributionDigest,
		)
		if err != nil {
			return nil, err
		}

		// One scan budget spans both sides: the documented bound is a
		// combined 50,000-row scan, not 50,000 per endpoint.
		scanned := 0
		oldDeclaration, oldRows, oldErr := collectCallerMap(
			ctx, s.opts.Evidence, certificate, oldPack,
			comparisonCallerQuery(query, query.Old), &scanned,
		)
		replacementDeclaration, replacementRows, replacementErr := collectCallerMap(
			ctx, s.opts.Evidence, certificate, replacementPack,
			comparisonCallerQuery(query, query.Replacement), &scanned,
		)

		confirmedVisible, err := visibleRepositories(ctx, s.opts)
		if err != nil {
			return nil, err
		}
		if catalogVisibilityContext(ctx, s.opts, confirmedVisible) != binding {
			return nil, huma.Error409Conflict(
				"caller comparison authorization changed while building the response; retry",
			)
		}
		confirmed, err := extract.BuildCoverageCertificate(
			ctx, s.opts.Evidence, confirmedVisible, domains,
		)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"confirm caller comparison coverage", err,
			)
		}
		confirmedOldAttribution, oldAttrErr := callerMapAttributionDigest(
			confirmed, oldPack.callerDomain,
		)
		confirmedReplacementAttribution, replacementAttrErr :=
			callerMapAttributionDigest(confirmed, replacementPack.callerDomain)
		if oldAttrErr != nil {
			return nil, comparisonAttributionError(oldAttrErr)
		}
		if replacementAttrErr != nil {
			return nil, comparisonAttributionError(replacementAttrErr)
		}
		if confirmed.Digest != certificate.Digest ||
			confirmedOldAttribution != oldAttribution ||
			confirmedReplacementAttribution != replacementAttribution {
			if cursor != "" {
				return nil, huma.Error409Conflict(
					"caller comparison cursor is no longer valid",
				)
			}
			continue
		}
		if collectErr := errors.Join(oldErr, replacementErr); collectErr != nil {
			switch {
			case errors.Is(collectErr, store.ErrResultLimit):
				return nil, huma.Error422UnprocessableEntity(
					"caller comparison exceeded its bounded row scan; narrow the filters",
				)
			case errors.Is(collectErr, store.ErrConflict):
				continue
			case errors.Is(collectErr, store.ErrNotFound):
				return nil, huma.Error404NotFound("caller comparison endpoint not found")
			default:
				return nil, huma.Error500InternalServerError(
					"build caller comparison", collectErr,
				)
			}
		}
		entries, err := buildCallerComparisonEntries(query, oldRows, replacementRows)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"classify caller comparison", err,
			)
		}
		if query.Classification != "" {
			filtered := entries[:0]
			for _, entry := range entries {
				if callerComparisonClassification(entry) == query.Classification {
					filtered = append(filtered, entry)
				}
			}
			entries = filtered
		}
		sortCallerComparisonEntries(entries, query.Ordering)
		if offset < 0 || offset > len(entries) {
			return nil, huma.Error409Conflict(
				"caller comparison cursor is no longer valid",
			)
		}
		end := min(offset+pageSize, len(entries))
		rows := make([]CallerComparisonRow, 0, end-offset)
		for _, entry := range entries[offset:end] {
			row, err := s.resolveCallerComparisonEntry(
				ctx, query, oldPack, replacementPack, entry,
			)
			if err != nil {
				return nil, huma.Error500InternalServerError(
					"resolve caller comparison citation", err,
				)
			}
			rows = append(rows, row)
		}
		pagination := CallerMapPagination{Complete: end == len(entries)}
		if !pagination.Complete {
			pagination.NextCursor, err = encodeCallerComparisonCursor(
				queryDigest, binding, certificate.Digest, attributionDigest, end,
			)
			if err != nil {
				return nil, huma.Error500InternalServerError(
					"encode caller comparison cursor", err,
				)
			}
		}
		totalRows := len(entries)
		return &CallerComparisonPage{
			SchemaVersion: legacyCallerComparisonSchemaVersion,
			Query:         query,
			Old: CallerComparisonSnapshot{
				Endpoint: query.Old, Declaration: &oldDeclaration,
				CoverageDigest:    certificate.Digest,
				AttributionDigest: oldAttribution,
			},
			Replacement: CallerComparisonSnapshot{
				Endpoint: query.Replacement, Declaration: &replacementDeclaration,
				CoverageDigest:    certificate.Digest,
				AttributionDigest: replacementAttribution,
			},
			Rows: rows, TotalRows: &totalRows, Pagination: pagination,
			Coverage: certificate,
			Caveat:   "Static evidence comparison only; old-only, both, new-only, and unresolved describe matching source evidence within the displayed scope, not migration completion, runtime use, completeness, or decommissioning safety.",
		}, nil
	}
	return nil, huma.Error409Conflict(
		"evidence changed while building the caller comparison; retry",
	)
}

func normalizeCallerComparisonQuery(query CallerComparisonQuery) CallerComparisonQuery {
	if query.Freshness == "" {
		query.Freshness = "any"
	}
	if query.Resolution == "" {
		query.Resolution = "any"
	}
	if query.Ordering == "" {
		query.Ordering = "source"
	}
	if query.Level == "" {
		query.Level = "occurrence"
	}
	return query
}

func comparisonCallerQuery(
	query CallerComparisonQuery,
	endpoint CallerMapEndpoint,
) CallerMapQuery {
	return CallerMapQuery{
		Endpoint: endpoint,
		Unit:     query.Unit, Owner: query.Owner, PathPrefix: query.PathPrefix,
		CodeRole: query.CodeRole, Tier: query.Tier,
		Freshness: query.Freshness, Resolution: query.Resolution,
		Ordering: query.Ordering,
	}
}

func comparisonDomains(oldPack, replacementPack protocolPack) []string {
	set := map[string]struct{}{
		oldPack.declarationDomain:         {},
		oldPack.callerDomain:              {},
		replacementPack.declarationDomain: {},
		replacementPack.callerDomain:      {},
	}
	domains := make([]string, 0, len(set))
	for domain := range set {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

func comparisonAttributionError(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return huma.Error409Conflict(
			"caller evidence must be republished by the current extractor",
		)
	}
	return huma.Error500InternalServerError("bind caller comparison attribution", err)
}

func buildCallerComparisonEntries(
	query CallerComparisonQuery,
	oldRows, replacementRows []callerMapProjection,
) ([]callerComparisonEntry, error) {
	entries := make(map[string]*callerComparisonEntry)
	add := func(rows []callerMapProjection, replacement bool) error {
		for _, row := range rows {
			key, exposed, sourceSort, unitSort, unit, unresolved :=
				callerComparisonIdentity(query.Level, row)
			entry := entries[key]
			if entry != nil && entry.unit != nil && unit != nil &&
				digestJSON(entry.unit) != digestJSON(unit) {
				// The same repository-namespaced unit carries divergent
				// candidate metadata across the two caller domains — a
				// legitimate state when the domains were extracted against
				// different unit-inventory snapshots. A unit bucket admits
				// rows only under one consistent candidate, so the
				// divergent row is demoted to its unique unresolved
				// occurrence key instead of contaminating the unit or
				// failing the whole read. Collection order is
				// deterministic, so the demotion is too.
				key, exposed, sourceSort, unitSort, unit, unresolved =
					callerComparisonUnresolvedIdentity(row)
				entry = entries[key]
			}
			if entry == nil {
				entry = &callerComparisonEntry{
					key: exposed, sourceSort: sourceSort, unitSort: unitSort,
					unresolved: unresolved, unit: unit,
				}
				entries[key] = entry
			} else {
				entry.unresolved = entry.unresolved || unresolved
				if sourceSort < entry.sourceSort {
					entry.sourceSort = sourceSort
				}
				if unitSort < entry.unitSort {
					entry.unitSort = unitSort
				}
			}
			if replacement {
				entry.replacement = append(entry.replacement, row)
			} else {
				entry.old = append(entry.old, row)
			}
		}
		return nil
	}
	if err := add(oldRows, false); err != nil {
		return nil, err
	}
	if err := add(replacementRows, true); err != nil {
		return nil, err
	}
	result := make([]callerComparisonEntry, 0, len(entries))
	for _, entry := range entries {
		sortCallerMapProjections(entry.old, "source")
		sortCallerMapProjections(entry.replacement, "source")
		result = append(result, *entry)
	}
	return result, nil
}

func callerComparisonIdentity(
	level string,
	row callerMapProjection,
) (
	internalKey, exposedKey, sourceSort, unitSort string,
	unit *CallerMapUnitCandidate,
	unresolved bool,
) {
	occurrence := fmt.Sprintf(
		"%s@%s:%s:%d-%d",
		row.assertion.Repo, row.commit, row.path, row.start, row.end,
	)
	resolvedSource := row.assertion.Predicate == "CALLS_OPERATION"
	if level == "occurrence" {
		return "occurrence\x00" + occurrence, occurrence, occurrence,
			row.group + "\x00" + occurrence, nil, !resolvedSource
	}
	if resolvedSource && row.detail.UnitState == "resolved" &&
		len(row.detail.UnitCandidates) == 1 {
		candidate := row.detail.UnitCandidates[0]
		exposed := row.assertion.Repo + ":" + candidate.ID
		// The internal key separates repository and unit id with NUL:
		// repository names may legally contain ':' (port-bearing hosts),
		// so the display form alone cannot namespace units safely.
		return "unit\x00" + row.assertion.Repo + "\x00" + candidate.ID,
			exposed, occurrence, exposed, &candidate, false
	}
	return callerComparisonUnresolvedIdentity(row)
}

// callerComparisonUnresolvedIdentity keys a row by its unique unresolved
// occurrence identity — the bucket for everything a unit cannot admit.
func callerComparisonUnresolvedIdentity(row callerMapProjection) (
	internalKey, exposedKey, sourceSort, unitSort string,
	unit *CallerMapUnitCandidate,
	unresolved bool,
) {
	occurrence := fmt.Sprintf(
		"%s@%s:%s:%d-%d",
		row.assertion.Repo, row.commit, row.path, row.start, row.end,
	)
	exposed := "unresolved:" + occurrence
	return "unresolved\x00" + occurrence, exposed, occurrence, exposed,
		nil, true
}

func callerComparisonClassification(entry callerComparisonEntry) string {
	if entry.unresolved {
		return "unresolved"
	}
	switch {
	case len(entry.old) > 0 && len(entry.replacement) > 0:
		return "both_evidence"
	case len(entry.old) > 0:
		return "old_only_evidence"
	default:
		return "new_only_evidence"
	}
}

func sortCallerComparisonEntries(entries []callerComparisonEntry, ordering string) {
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		leftSort, rightSort := left.sourceSort, right.sourceSort
		if ordering == "unit" {
			leftSort, rightSort = left.unitSort, right.unitSort
		}
		if leftSort != rightSort {
			return leftSort < rightSort
		}
		return left.key < right.key
	})
}

func (s *CallerComparisonService) resolveCallerComparisonEntry(
	ctx context.Context,
	query CallerComparisonQuery,
	oldPack, replacementPack protocolPack,
	entry callerComparisonEntry,
) (CallerComparisonRow, error) {
	oldSide, err := resolveCallerComparisonSide(
		ctx, s.opts.Evidence, oldPack.protocol, entry.old,
	)
	if err != nil {
		return CallerComparisonRow{}, err
	}
	replacementSide, err := resolveCallerComparisonSide(
		ctx, s.opts.Evidence, replacementPack.protocol, entry.replacement,
	)
	if err != nil {
		return CallerComparisonRow{}, err
	}
	return CallerComparisonRow{
		Level: query.Level, Key: entry.key,
		Classification: callerComparisonClassification(entry),
		Unit:           entry.unit, Old: oldSide, Replacement: replacementSide,
	}, nil
}

func resolveCallerComparisonSide(
	ctx context.Context,
	source store.EvidenceStore,
	protocol string,
	projections []callerMapProjection,
) (CallerComparisonSide, error) {
	side := CallerComparisonSide{
		OccurrenceCount: len(projections),
		Rows:            make([]CallerMapRow, 0, min(len(projections), callerComparisonCitationLimit)),
		RowsTruncated:   len(projections) > callerComparisonCitationLimit,
	}
	for _, projection := range projections[:min(len(projections), callerComparisonCitationLimit)] {
		row, err := resolveCallerMapProjection(ctx, source, protocol, projection)
		if err != nil {
			return CallerComparisonSide{}, err
		}
		side.Rows = append(side.Rows, row)
	}
	return side, nil
}

func decodeCallerComparisonCursor(
	encoded, queryDigest string,
	binding VisibilityContext,
	coverageDigest, attributionDigest string,
) (int, error) {
	if encoded == "" {
		return 0, nil
	}
	if len(encoded) > callerMapCursorLimit {
		return 0, huma.Error400BadRequest("caller comparison cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > callerMapCursorLimit || !json.Valid(raw) {
		return 0, huma.Error400BadRequest("caller comparison cursor is invalid")
	}
	var cursor callerComparisonCursor
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return 0, huma.Error400BadRequest("caller comparison cursor is invalid")
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if cursor.Schema != callerComparisonCursorVersion || checksum == "" ||
		checksum != digestJSON(cursor) || cursor.Offset < 1 {
		return 0, huma.Error400BadRequest("caller comparison cursor is invalid")
	}
	if cursor.QueryDigest != queryDigest {
		return 0, huma.Error400BadRequest(
			"caller comparison cursor does not match the query",
		)
	}
	if cursor.Principal != binding.Principal ||
		cursor.AuthorizationProvider != binding.AuthorizationProvider ||
		cursor.PermissionSnapshot != binding.PermissionSnapshot ||
		cursor.VisibleRepositorySetDigest != binding.VisibleRepositorySetDigest ||
		cursor.CoverageDigest != coverageDigest ||
		cursor.AttributionDigest != attributionDigest {
		return 0, huma.Error409Conflict(
			"caller comparison cursor is no longer valid",
		)
	}
	return cursor.Offset, nil
}

func encodeCallerComparisonCursor(
	queryDigest string,
	binding VisibilityContext,
	coverageDigest, attributionDigest string,
	offset int,
) (string, error) {
	cursor := callerComparisonCursor{
		Schema: callerComparisonCursorVersion, QueryDigest: queryDigest,
		Principal:                  binding.Principal,
		AuthorizationProvider:      binding.AuthorizationProvider,
		PermissionSnapshot:         binding.PermissionSnapshot,
		VisibleRepositorySetDigest: binding.VisibleRepositorySetDigest,
		CoverageDigest:             coverageDigest,
		AttributionDigest:          attributionDigest,
		Offset:                     offset,
	}
	cursor.Checksum = digestJSON(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func registerCallerComparisonAPI(api huma.API, opts Options) {
	service := opts.CallerComparison
	if service == nil {
		service = NewCallerComparisonService(opts)
	}
	if service == nil {
		return
	}
	type comparisonIn struct {
		OldProtocol           string `query:"old_protocol" required:"true" maxLength:"128"`
		OldRepository         string `query:"old_repository" required:"true" maxLength:"1024"`
		OldLineage            string `query:"old_lineage" required:"true" maxLength:"1024"`
		OldOperation          string `query:"old_operation" required:"true" maxLength:"1024"`
		ReplacementProtocol   string `query:"replacement_protocol" required:"true" maxLength:"128"`
		ReplacementRepository string `query:"replacement_repository" required:"true" maxLength:"1024"`
		ReplacementLineage    string `query:"replacement_lineage" required:"true" maxLength:"1024"`
		ReplacementOperation  string `query:"replacement_operation" required:"true" maxLength:"1024"`
		Unit                  string `query:"unit" maxLength:"1024"`
		Owner                 string `query:"owner" maxLength:"1024"`
		PathPrefix            string `query:"path_prefix" maxLength:"1024"`
		CodeRole              string `query:"code_role" maxLength:"32"`
		Tier                  string `query:"tier" maxLength:"32"`
		Freshness             string `query:"freshness" maxLength:"16"`
		Resolution            string `query:"resolution" maxLength:"16"`
		Ordering              string `query:"ordering" maxLength:"16"`
		Level                 string `query:"level" maxLength:"16"`
		Classification        string `query:"classification" maxLength:"32"`
		PageSize              int    `query:"page_size" minimum:"0" maximum:"100"`
		Cursor                string `query:"cursor" maxLength:"16384"`
	}
	compare := func(
		ctx context.Context,
		in *comparisonIn,
	) (*CallerComparisonPage, error) {
		return service.Compare(ctx, CallerComparisonQuery{
			Old: CallerMapEndpoint{
				Protocol: in.OldProtocol, Repository: in.OldRepository,
				Lineage: in.OldLineage, Operation: in.OldOperation,
			},
			Replacement: CallerMapEndpoint{
				Protocol:   in.ReplacementProtocol,
				Repository: in.ReplacementRepository,
				Lineage:    in.ReplacementLineage,
				Operation:  in.ReplacementOperation,
			},
			Unit: in.Unit, Owner: in.Owner, PathPrefix: in.PathPrefix,
			CodeRole: in.CodeRole, Tier: in.Tier,
			Freshness: in.Freshness, Resolution: in.Resolution,
			Ordering: in.Ordering, Level: in.Level,
			Classification: in.Classification,
		}, in.PageSize, in.Cursor)
	}
	if service.exact == nil {
		// Historical acceptance fixtures and Workbench's explicitly injected
		// legacy reader keep their v1 schema. Production never registers this
		// branch; T30.6l removes the final legacy consumer.
		type legacyComparisonOut struct {
			Body CallerComparisonPage
		}
		huma.Get(api, "/api/compare_operation_callers", func(
			ctx context.Context,
			in *comparisonIn,
		) (*legacyComparisonOut, error) {
			result, err := compare(ctx, in)
			if err != nil {
				return nil, err
			}
			return &legacyComparisonOut{Body: *result}, nil
		})
		return
	}
	type exactComparisonOut struct {
		Body CallerComparisonExactPage
	}
	huma.Get(api, "/api/compare_operation_callers", func(
		ctx context.Context,
		in *comparisonIn,
	) (*exactComparisonOut, error) {
		result, err := compare(ctx, in)
		if err != nil {
			return nil, err
		}
		exact, err := AsExactCallerComparisonPage(result)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"serialize exact caller comparison", err,
			)
		}
		return &exactComparisonOut{Body: *exact}, nil
	})
}
