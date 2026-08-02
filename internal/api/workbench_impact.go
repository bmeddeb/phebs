package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	workbenchImpactSchemaVersion = "workbench-impact-inventory-v2"
	workbenchImpactCursorVersion = "workbench-impact-cursor-v2"
	workbenchImpactCursorLimit   = 64 << 10
	workbenchImpactCaveat        = "Cited static evidence within the displayed authorization and extraction snapshots; rows, empty pages, and completed pagination do not establish runtime use, completeness, migration completion, or decommissioning safety."
)

type workbenchImpactReader interface {
	Read(context.Context, string, string) (*store.WorkbenchView, error)
}

type workbenchImpactCatalog interface {
	OperationForProtocol(
		context.Context,
		string,
		string,
		string,
		string,
	) (*ContractCatalogOperation, error)
}

type workbenchImpactCallerMap interface {
	List(
		context.Context,
		CallerMapQuery,
		int,
		string,
	) (*CallerMapPage, error)
	confirmWorkbenchCallerSnapshot(
		context.Context,
		CallerMapQuery,
		int,
		string,
	) (exactCallerSnapshotConfirmation, error)
}

type workbenchImpactComparison interface {
	Compare(
		context.Context,
		CallerComparisonQuery,
		int,
		string,
	) (*CallerComparisonPage, error)
	confirmWorkbenchComparisonSnapshot(
		context.Context,
		CallerComparisonQuery,
		int,
		string,
	) (exactCallerComparisonSnapshotConfirmation, error)
}

type workbenchImpactFieldReferences interface {
	List(
		context.Context,
		FieldReferenceQuery,
		int,
		string,
	) (*FieldReferencePage, error)
}

type workbenchImpactCompatibility interface {
	ReadCompatibility(
		context.Context,
		string,
		string,
		string,
		string,
	) (*store.WorkbenchCompatibilityAnalysis, error)
}

// WorkbenchImpactService composes existing authorization-scoped read
// services. It has no evidence-store field and therefore cannot bypass those
// services or mint proof bundles.
type WorkbenchImpactService struct {
	opts          Options
	cursorSecret  [sha256.Size]byte
	workbench     workbenchImpactReader
	catalog       workbenchImpactCatalog
	callers       workbenchImpactCallerMap
	comparison    workbenchImpactComparison
	fields        workbenchImpactFieldReferences
	compatibility workbenchImpactCompatibility
	resources     *ResourcePlaneRegistry
}

func NewWorkbenchImpactService(opts Options) *WorkbenchImpactService {
	if opts.Workbench == nil || opts.Principal == nil {
		return nil
	}
	catalog := opts.ContractCatalog
	if catalog == nil {
		catalog = NewContractCatalogService(opts)
	}
	callers := opts.CallerMap
	if callers == nil {
		callers = NewCallerMapService(opts)
	}
	if callers == nil || callers.exact == nil {
		return nil
	}
	comparison := opts.CallerComparison
	if comparison == nil {
		// The comparison and Caller Map must share one HMAC secret, binding
		// registry, reverse-index cache, and exact citation reader.
		exactOpts := opts
		exactOpts.CallerMap = callers
		comparison = NewCallerComparisonService(exactOpts)
	}
	if comparison == nil || comparison.exact != callers.exact {
		return nil
	}
	fields := opts.FieldReferences
	if fields == nil {
		fields = NewFieldReferenceService(opts)
	}
	if catalog == nil || fields == nil {
		return nil
	}
	resources := opts.ResourcePlanes
	if resources == nil {
		resources = DefaultWorkbenchResourcePlanes()
	}
	compatibility, _ := opts.Workbench.(workbenchImpactCompatibility)
	return &WorkbenchImpactService{
		opts:          opts,
		cursorSecret:  callers.exact.secret,
		workbench:     opts.Workbench,
		catalog:       catalog,
		callers:       callers,
		comparison:    comparison,
		fields:        fields,
		compatibility: compatibility,
		resources:     resources,
	}
}

type WorkbenchImpactFilters struct {
	Unit       string `json:"unit,omitempty"`
	Owner      string `json:"owner,omitempty"`
	PathPrefix string `json:"path_prefix,omitempty"`
	CodeRole   string `json:"code_role,omitempty"`
	Tier       string `json:"tier,omitempty"`
	Freshness  string `json:"freshness,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Ordering   string `json:"ordering,omitempty"`
	Level      string `json:"level,omitempty"`
}

type WorkbenchImpactRequest struct {
	InvestigationID  string                 `json:"investigation_id"`
	RevisionID       string                 `json:"revision_id"`
	CompatibilityRun string                 `json:"compatibility_run_id,omitempty"`
	Filters          WorkbenchImpactFilters `json:"filters"`
	PageSize         int                    `json:"page_size,omitempty"`
	Cursor           string                 `json:"cursor,omitempty"`
}

type WorkbenchAtlasImpact struct {
	Selection              store.ChangeBriefContractSelection `json:"selection"`
	Declaration            ContractCatalogClaim               `json:"declaration"`
	FactDetail             ContractCatalogOperationFactDetail `json:"fact_detail"`
	Request                ContractCatalogMessage             `json:"request"`
	Response               ContractCatalogMessage             `json:"response"`
	Implementations        []ContractCatalogRelationship      `json:"implementations"`
	NameMatches            []ContractCatalogRelationship      `json:"name_matches"`
	ExtractorAbstentions   []ContractCatalogRelationship      `json:"extractor_abstentions"`
	RelationshipsTruncated bool                               `json:"relationships_truncated"`
	ShapeTruncated         bool                               `json:"shape_truncated"`
	CoverageDigest         string                             `json:"coverage_digest"`
}

type WorkbenchCallerImpact struct {
	Selection            store.ChangeBriefContractSelection `json:"selection"`
	Query                CallerMapQuery                     `json:"query"`
	Declaration          *ContractCatalogClaim              `json:"declaration,omitempty"`
	Generation           CallerMapGeneration                `json:"generation"`
	MatchingRowsState    string                             `json:"matching_rows_state" enum:"exact,unavailable"`
	ResolvedCallers      []CallerMapRow                     `json:"resolved_callers"`
	ExtractorAbstentions []CallerMapRow                     `json:"extractor_abstentions"`
	Groups               []CallerMapGroup                   `json:"groups,omitempty"`
	TotalMatchingRows    *int                               `json:"total_matching_rows,omitempty"`
	Pagination           CallerMapPagination                `json:"pagination"`
	Caveat               string                             `json:"caveat"`
}

type WorkbenchCompatibilityImpact struct {
	Status         string                 `json:"status"`
	RunID          string                 `json:"run_id,omitempty"`
	Compatible     *bool                  `json:"compatible,omitempty"`
	Violations     []compat.Violation     `json:"violations"`
	AffectedFields []compat.FieldIdentity `json:"affected_fields"`
	Failure        string                 `json:"failure,omitempty"`
	ContentDigest  string                 `json:"content_digest,omitempty"`
}

type WorkbenchImpactCoverage struct {
	Capability  string                      `json:"capability"`
	Target      string                      `json:"target"`
	Digest      string                      `json:"digest"`
	Certificate extract.CoverageCertificate `json:"certificate"`
}

type WorkbenchImpactCapability struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type WorkbenchImpactGap struct {
	Capability string `json:"capability"`
	Target     string `json:"target,omitempty"`
	State      string `json:"state"`
	Code       string `json:"code"`
}

type WorkbenchAnalysisScope struct {
	Coverage     []WorkbenchImpactCoverage   `json:"coverage"`
	Capabilities []WorkbenchImpactCapability `json:"capabilities"`
	Gaps         []WorkbenchImpactGap        `json:"gaps"`
}

type WorkbenchImpactPagination struct {
	Complete   bool   `json:"complete"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type WorkbenchImpactPage struct {
	SchemaVersion    string                        `json:"schema_version"`
	InvestigationID  string                        `json:"investigation_id"`
	RevisionID       string                        `json:"revision_id"`
	TicketKind       store.ChangeBriefTicketKind   `json:"ticket_kind"`
	ScenarioEmphasis []string                      `json:"scenario_emphasis"`
	Atlas            []WorkbenchAtlasImpact        `json:"atlas"`
	Callers          []WorkbenchCallerImpact       `json:"callers"`
	Comparison       *CallerComparisonExactPage    `json:"comparison,omitempty"`
	Compatibility    *WorkbenchCompatibilityImpact `json:"compatibility,omitempty"`
	FieldReferences  *FieldReferencePage           `json:"field_references,omitempty"`
	ResourcePlanes   []ResourcePlaneSnapshot       `json:"resource_planes"`
	AnalysisScope    WorkbenchAnalysisScope        `json:"analysis_scope"`
	Pagination       WorkbenchImpactPagination     `json:"pagination"`
	Caveat           string                        `json:"caveat"`
}

type workbenchImpactStreamCursor struct {
	Complete  bool   `json:"complete"`
	Next      string `json:"next,omitempty"`
	Snapshot  string `json:"snapshot"`
	Authority string `json:"authority,omitempty"`
}

type workbenchImpactCursor struct {
	Schema              string                                 `json:"schema"`
	QueryDigest         string                                 `json:"query_digest"`
	Principal           string                                 `json:"principal"`
	RevisionDigest      string                                 `json:"revision_digest"`
	AtlasDigest         string                                 `json:"atlas_digest"`
	CompatibilityDigest string                                 `json:"compatibility_digest"`
	ResourceDigest      string                                 `json:"resource_digest"`
	Streams             map[string]workbenchImpactStreamCursor `json:"streams"`
}

func (service *WorkbenchImpactService) Read(
	ctx context.Context,
	principal string,
	request WorkbenchImpactRequest,
) (result *WorkbenchImpactPage, resultErr error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable(
			"workbench impact inventory unavailable",
		)
	}
	if strings.TrimSpace(principal) == "" ||
		service.opts.Principal == nil ||
		strings.TrimSpace(service.opts.Principal(ctx)) != principal {
		return nil, store.ErrNotFound
	}
	if strings.TrimSpace(request.InvestigationID) !=
		request.InvestigationID ||
		request.InvestigationID == "" ||
		strings.TrimSpace(request.RevisionID) != request.RevisionID ||
		request.RevisionID == "" {
		return nil, huma.Error400BadRequest(
			"investigation_id and revision_id are required",
		)
	}
	if request.PageSize == 0 {
		request.PageSize = callerMapDefaultPage
	}
	if request.PageSize < 1 || request.PageSize > callerMapMaxPage {
		return nil, huma.Error400BadRequest(
			fmt.Sprintf(
				"page_size must be from 1 through %d",
				callerMapMaxPage,
			),
		)
	}
	request.Filters = normalizeWorkbenchImpactFilters(request.Filters)
	view, err := service.workbench.Read(
		ctx,
		principal,
		request.InvestigationID,
	)
	if err != nil {
		return nil, err
	}
	if !workbenchImpactViewCurrent(view, request) {
		return nil, store.ErrNotFound
	}
	revisionDigest := workbenchImpactRevisionDigest(view)
	// Every subordinate service has its own result-time repository/evidence
	// fence. Repeat the Investigation authority last as well so a revocation or
	// revision advance during composition cannot leak the subordinate error or
	// serialize evidence under an obsolete Workbench revision.
	defer func() {
		confirmed, err := service.workbench.Read(
			ctx, principal, request.InvestigationID,
		)
		if err != nil || !workbenchImpactViewCurrent(confirmed, request) {
			result = nil
			if errors.Is(err, store.ErrNotFound) || err == nil {
				resultErr = store.ErrNotFound
			} else {
				resultErr = err
			}
			return
		}
		if workbenchImpactRevisionDigest(confirmed) != revisionDigest {
			result = nil
			resultErr = huma.Error409Conflict(
				"workbench impact revision changed while building the response",
			)
		}
	}()
	if request.CompatibilityRun != "" &&
		view.Brief.TicketKind != store.ChangeBriefModify {
		return nil, huma.Error400BadRequest(
			"compatibility_run_id is valid only for a modify ticket",
		)
	}
	queryDigest := digestJSON(struct {
		InvestigationID  string                 `json:"investigation_id"`
		RevisionID       string                 `json:"revision_id"`
		CompatibilityRun string                 `json:"compatibility_run_id,omitempty"`
		Filters          WorkbenchImpactFilters `json:"filters"`
		PageSize         int                    `json:"page_size"`
	}{
		InvestigationID:  request.InvestigationID,
		RevisionID:       request.RevisionID,
		CompatibilityRun: request.CompatibilityRun,
		Filters:          request.Filters,
		PageSize:         request.PageSize,
	})
	cursor, err := service.decodeWorkbenchImpactCursor(
		request.Cursor,
		queryDigest,
		principal,
		revisionDigest,
	)
	if err != nil {
		return nil, err
	}

	page := &WorkbenchImpactPage{
		SchemaVersion:   workbenchImpactSchemaVersion,
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		TicketKind:      view.Brief.TicketKind,
		ScenarioEmphasis: workbenchScenarioEmphasis(
			view.Brief.TicketKind,
		),
		Atlas:          []WorkbenchAtlasImpact{},
		Callers:        []WorkbenchCallerImpact{},
		ResourcePlanes: []ResourcePlaneSnapshot{},
		AnalysisScope: WorkbenchAnalysisScope{
			Coverage:     []WorkbenchImpactCoverage{},
			Capabilities: []WorkbenchImpactCapability{},
			Gaps:         []WorkbenchImpactGap{},
		},
		Caveat: workbenchImpactCaveat,
	}
	next := workbenchImpactCursor{
		Schema:         workbenchImpactCursorVersion,
		QueryDigest:    queryDigest,
		Principal:      principal,
		RevisionDigest: revisionDigest,
		Streams:        make(map[string]workbenchImpactStreamCursor),
	}
	if cursor != nil {
		next.AtlasDigest = cursor.AtlasDigest
		next.CompatibilityDigest = cursor.CompatibilityDigest
		next.ResourceDigest = cursor.ResourceDigest
	}

	atlasDigest, err := service.readWorkbenchAtlas(
		ctx,
		view.Brief,
		page,
	)
	if err != nil {
		return nil, err
	}
	if err := bindWorkbenchImpactDigest(
		"Atlas",
		&next.AtlasDigest,
		atlasDigest,
		request.Cursor != "",
	); err != nil {
		return nil, err
	}

	compatibility, compatibilityDigest, err :=
		service.readWorkbenchCompatibility(
			ctx,
			principal,
			request,
			view.Brief,
			page,
		)
	if err != nil {
		return nil, err
	}
	page.Compatibility = compatibility
	if err := bindWorkbenchImpactDigest(
		"compatibility",
		&next.CompatibilityDigest,
		compatibilityDigest,
		request.Cursor != "",
	); err != nil {
		return nil, err
	}

	resourceContext := ResourcePlaneContext{
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		TicketKind:      view.Brief.TicketKind,
		Selections: slices.Clone(
			view.Brief.What.Selections,
		),
	}
	var resourceVisible func(string) bool
	if service.opts.Visible != nil {
		allow := service.opts.Visible(ctx)
		if allow != nil {
			resourceVisible = func(repository string) bool {
				return allow(store.Repo{Name: repository})
			}
		}
	}
	page.ResourcePlanes, err = service.resources.Snapshot(
		ctx,
		resourceContext,
		resourceVisible,
	)
	if err != nil {
		return nil, err
	}
	resourceDigest := digestJSON(page.ResourcePlanes)
	if err := bindWorkbenchImpactDigest(
		"resource planes",
		&next.ResourceDigest,
		resourceDigest,
		request.Cursor != "",
	); err != nil {
		return nil, err
	}
	workbenchResourcePlaneScope(page)

	expectedStreams := make(map[string]struct{})
	switch view.Brief.TicketKind {
	case store.ChangeBriefAdd:
		// Add intentionally has no caller stream: an empty caller set for a
		// not-yet-existing declaration is expected, not an impact finding.
	case store.ChangeBriefModify, store.ChangeBriefRetire:
		current, ok := workbenchSelection(
			view.Brief.What.Selections,
			store.ChangeBriefCurrent,
		)
		if !ok {
			return nil, errors.New(
				"workbench impact inventory has no current endpoint",
			)
		}
		key := workbenchCallerStreamKey(current)
		expectedStreams[key] = struct{}{}
		state, emitted, err := service.readWorkbenchCallerStream(
			ctx,
			request,
			current,
			key,
			cursor,
			page,
		)
		if err != nil {
			return nil, err
		}
		next.Streams[key] = state
		if emitted != nil {
			page.Callers = append(page.Callers, *emitted)
		}
	case store.ChangeBriefMigrate:
		current, currentOK := workbenchSelection(
			view.Brief.What.Selections,
			store.ChangeBriefCurrent,
		)
		replacement, replacementOK := workbenchSelection(
			view.Brief.What.Selections,
			store.ChangeBriefReplacement,
		)
		if !currentOK || !replacementOK {
			return nil, errors.New(
				"workbench impact inventory has incomplete migration endpoints",
			)
		}
		key := "caller-comparison"
		expectedStreams[key] = struct{}{}
		state, emitted, err := service.readWorkbenchComparisonStream(
			ctx,
			request,
			current,
			replacement,
			key,
			cursor,
			page,
		)
		if err != nil {
			return nil, err
		}
		next.Streams[key] = state
		page.Comparison = emitted
	default:
		return nil, errors.New(
			"workbench impact inventory has an invalid ticket kind",
		)
	}
	if compatibility != nil &&
		compatibility.Status == "published" &&
		len(compatibility.AffectedFields) > 0 {
		key := "field-references"
		expectedStreams[key] = struct{}{}
		state, emitted, err := service.readWorkbenchFieldStream(
			ctx,
			request,
			compatibility.AffectedFields,
			key,
			cursor,
			page,
		)
		if err != nil {
			return nil, err
		}
		next.Streams[key] = state
		page.FieldReferences = emitted
	}
	if cursor != nil {
		if err := validateWorkbenchImpactStreamSet(
			cursor.Streams,
			expectedStreams,
		); err != nil {
			return nil, err
		}
	}
	if err := validateWorkbenchImpactStreamSet(
		next.Streams,
		expectedStreams,
	); err != nil {
		return nil, err
	}
	sortWorkbenchImpactScope(&page.AnalysisScope)
	page.Pagination.Complete = true
	for _, state := range next.Streams {
		if !state.Complete {
			page.Pagination.Complete = false
			break
		}
	}
	if !page.Pagination.Complete {
		page.Pagination.NextCursor, err = service.encodeWorkbenchImpactCursor(next)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"encode workbench impact cursor",
				err,
			)
		}
	}
	return page, nil
}

func normalizeWorkbenchImpactFilters(
	filters WorkbenchImpactFilters,
) WorkbenchImpactFilters {
	if filters.Freshness == "" {
		filters.Freshness = "any"
	}
	if filters.Resolution == "" {
		filters.Resolution = "any"
	}
	if filters.Ordering == "" {
		filters.Ordering = "source"
	}
	if filters.Level == "" {
		filters.Level = "occurrence"
	}
	return filters
}

func (service *WorkbenchImpactService) readWorkbenchAtlas(
	ctx context.Context,
	brief store.ChangeBrief,
	page *WorkbenchImpactPage,
) (string, error) {
	digests := make([]string, 0, len(brief.What.Selections))
	includeCallerEvidence := brief.TicketKind != store.ChangeBriefAdd
	for _, selection := range brief.What.Selections {
		detail, err := service.catalog.OperationForProtocol(
			ctx,
			selection.Protocol,
			selection.Repository,
			selection.DeclarationLineage,
			selection.CanonicalOperation,
		)
		if err != nil {
			return "", err
		}
		if detail == nil ||
			detail.Protocol != selection.Protocol ||
			detail.Repository != selection.Repository ||
			detail.DeclarationLineage !=
				selection.DeclarationLineage ||
			detail.Operation != selection.CanonicalOperation {
			return "", errors.New(
				"workbench Atlas returned a mismatched endpoint",
			)
		}
		nameMatches := []ContractCatalogRelationship{}
		abstentions := []ContractCatalogRelationship{}
		if includeCallerEvidence {
			for _, relationship := range detail.Callers {
				switch relationship.Classification {
				case "resolved_caller":
					// The Caller Map/comparison is the one exact-caller
					// projection; do not duplicate its rows here.
				case "unresolved_name_match":
					nameMatches = append(nameMatches, relationship)
				default:
					return "", errors.New(
						"workbench Atlas returned an unknown caller classification",
					)
				}
			}
			for _, relationship := range detail.UnresolvedCandidates {
				if relationship.Classification != "extractor_abstention" {
					return "", errors.New(
						"workbench Atlas returned an unknown abstention classification",
					)
				}
				abstentions = append(abstentions, relationship)
			}
		}
		relationshipsTruncated := detail.RelationshipsTruncated
		if brief.TicketKind == store.ChangeBriefAdd &&
			len(detail.Implementations) < catalogRelationshipLimit {
			// Atlas detail shares one relationship budget across
			// implementations and callers. Add does not ask a caller
			// question, so caller-only clipping is not an Add finding.
			relationshipsTruncated = false
		}
		impact := WorkbenchAtlasImpact{
			Selection:              selection,
			Declaration:            detail.Declaration,
			FactDetail:             detail.FactDetail,
			Request:                detail.Request,
			Response:               detail.Response,
			Implementations:        slices.Clone(detail.Implementations),
			NameMatches:            nameMatches,
			ExtractorAbstentions:   abstentions,
			RelationshipsTruncated: relationshipsTruncated,
			ShapeTruncated:         detail.ShapeTruncated,
			CoverageDigest:         detail.CoverageDigest,
		}
		page.Atlas = append(page.Atlas, impact)
		target := workbenchSelectionTarget(selection)
		workbenchAddCoverage(
			page,
			"contract-atlas",
			target,
			detail.Coverage,
		)
		if relationshipsTruncated {
			workbenchAddGap(
				page,
				"contract-atlas",
				target,
				"enabled",
				"relationship_limit",
			)
		}
		if detail.ShapeTruncated {
			workbenchAddGap(
				page,
				"contract-atlas",
				target,
				"enabled",
				"shape_limit",
			)
		}
		digests = append(digests, digestJSON(impact))
	}
	page.AnalysisScope.Capabilities = append(
		page.AnalysisScope.Capabilities,
		WorkbenchImpactCapability{
			ID:    "contract-atlas",
			State: "enabled",
		},
	)
	return digestJSON(digests), nil
}

func (service *WorkbenchImpactService) readWorkbenchCompatibility(
	ctx context.Context,
	principal string,
	request WorkbenchImpactRequest,
	brief store.ChangeBrief,
	page *WorkbenchImpactPage,
) (*WorkbenchCompatibilityImpact, string, error) {
	if brief.TicketKind != store.ChangeBriefModify {
		return nil, digestJSON(nil), nil
	}
	if request.CompatibilityRun == "" {
		page.AnalysisScope.Capabilities = append(
			page.AnalysisScope.Capabilities,
			WorkbenchImpactCapability{
				ID:     "contract-compatibility",
				State:  "unsupported",
				Reason: "retained_analysis_not_selected",
			},
		)
		workbenchAddGap(
			page,
			"contract-compatibility",
			"",
			"unsupported",
			"retained_analysis_not_selected",
		)
		return nil, digestJSON("retained_analysis_not_selected"), nil
	}
	if service.compatibility == nil {
		return nil, "", huma.Error503ServiceUnavailable(
			"retained Workbench compatibility unavailable",
		)
	}
	analysis, err := service.compatibility.ReadCompatibility(
		ctx,
		principal,
		request.InvestigationID,
		request.RevisionID,
		request.CompatibilityRun,
	)
	if err != nil {
		return nil, "", err
	}
	if analysis == nil ||
		analysis.InvestigationID != request.InvestigationID ||
		analysis.RevisionID != request.RevisionID ||
		analysis.Run.ID != request.CompatibilityRun ||
		analysis.Artifact.RunID != request.CompatibilityRun {
		return nil, "", errors.New(
			"retained Workbench compatibility binding is inconsistent",
		)
	}
	result := &WorkbenchCompatibilityImpact{
		Status:         analysis.Status,
		RunID:          analysis.Run.ID,
		Violations:     []compat.Violation{},
		AffectedFields: []compat.FieldIdentity{},
		Failure:        analysis.Failure,
		ContentDigest:  analysis.Artifact.ContentDigest,
	}
	state := "enabled"
	if analysis.Status == "failed" {
		state = "failed"
		workbenchAddGap(
			page,
			"contract-compatibility",
			"",
			"failed",
			"retained_analysis_failed",
		)
	} else if analysis.Status != "published" ||
		analysis.Compatibility == nil {
		return nil, "", errors.New(
			"retained Workbench compatibility result is inconsistent",
		)
	} else {
		compatible := analysis.Compatibility.Compatible
		result.Compatible = &compatible
		result.Violations = slices.Clone(
			analysis.Compatibility.Violations,
		)
		result.AffectedFields = slices.Clone(
			analysis.Compatibility.AffectedFields,
		)
	}
	page.AnalysisScope.Capabilities = append(
		page.AnalysisScope.Capabilities,
		WorkbenchImpactCapability{
			ID:    "contract-compatibility",
			State: state,
		},
	)
	return result, digestJSON(analysis), nil
}

func (service *WorkbenchImpactService) readWorkbenchCallerStream(
	ctx context.Context,
	request WorkbenchImpactRequest,
	selection store.ChangeBriefContractSelection,
	key string,
	cursor *workbenchImpactCursor,
	page *WorkbenchImpactPage,
) (workbenchImpactStreamCursor, *WorkbenchCallerImpact, error) {
	previous, hadPrevious := workbenchImpactPreviousStream(cursor, key)
	query := workbenchCallerQuery(selection, request.Filters)
	if hadPrevious && previous.Complete {
		confirmed, err := service.callers.confirmWorkbenchCallerSnapshot(
			ctx, query, request.PageSize, previous.Authority,
		)
		if err != nil {
			return workbenchImpactStreamCursor{}, nil, err
		}
		if confirmed.Snapshot == "" ||
			confirmed.Snapshot != previous.Snapshot {
			return workbenchImpactStreamCursor{}, nil,
				huma.Error409Conflict(
					"workbench impact caller snapshot is no longer valid",
				)
		}
		if err := workbenchCallerGenerationScope(
			page, "contract-caller-map",
			workbenchSelectionTarget(selection),
			confirmed.MatchingRowsState, confirmed.Generation,
		); err != nil {
			return workbenchImpactStreamCursor{}, nil, err
		}
		return previous, nil, nil
	}
	value, err := service.callers.List(
		ctx,
		query,
		request.PageSize,
		previous.Next,
	)
	if err != nil {
		return workbenchImpactStreamCursor{}, nil, err
	}
	if err := validateWorkbenchExactCallerPage(value, query); err != nil {
		return workbenchImpactStreamCursor{}, nil,
			huma.Error409Conflict(
				"workbench impact caller snapshot is unavailable", err,
			)
	}
	if hadPrevious && (value.exactSnapshot != previous.Snapshot ||
		value.exactAuthority != previous.Authority) {
		return workbenchImpactStreamCursor{}, nil,
			huma.Error409Conflict(
				"workbench impact caller snapshot is no longer valid",
			)
	}
	if err := workbenchCallerGenerationScope(
		page,
		"contract-caller-map",
		workbenchSelectionTarget(selection),
		value.MatchingRowsState, *value.Generation,
	); err != nil {
		return workbenchImpactStreamCursor{}, nil, err
	}
	state := workbenchImpactStreamCursor{
		Complete:  value.Pagination.Complete,
		Next:      value.Pagination.NextCursor,
		Snapshot:  value.exactSnapshot,
		Authority: value.exactAuthority,
	}
	resolved := []CallerMapRow{}
	abstentions := []CallerMapRow{}
	for _, row := range value.Rows {
		switch row.Classification {
		case "resolved_caller":
			resolved = append(resolved, row)
		case "extractor_abstention":
			abstentions = append(abstentions, row)
		default:
			return workbenchImpactStreamCursor{}, nil, errors.New(
				"caller map returned an unknown classification",
			)
		}
	}
	return state, &WorkbenchCallerImpact{
		Selection:            selection,
		Query:                value.Query,
		Declaration:          value.Declaration,
		Generation:           *value.Generation,
		MatchingRowsState:    value.MatchingRowsState,
		ResolvedCallers:      resolved,
		ExtractorAbstentions: abstentions,
		Groups:               slices.Clone(value.Groups),
		TotalMatchingRows:    value.TotalMatchingRows,
		Pagination:           value.Pagination,
		Caveat:               value.Caveat,
	}, nil
}

func (service *WorkbenchImpactService) readWorkbenchComparisonStream(
	ctx context.Context,
	request WorkbenchImpactRequest,
	current, replacement store.ChangeBriefContractSelection,
	key string,
	cursor *workbenchImpactCursor,
	page *WorkbenchImpactPage,
) (workbenchImpactStreamCursor, *CallerComparisonExactPage, error) {
	previous, hadPrevious := workbenchImpactPreviousStream(cursor, key)
	query := workbenchComparisonQuery(
		current,
		replacement,
		request.Filters,
	)
	if hadPrevious && previous.Complete {
		confirmed, err := service.comparison.
			confirmWorkbenchComparisonSnapshot(
				ctx, query, request.PageSize, previous.Authority,
			)
		if err != nil {
			return workbenchImpactStreamCursor{}, nil, err
		}
		if confirmed.Snapshot == "" ||
			confirmed.Snapshot != previous.Snapshot {
			return workbenchImpactStreamCursor{}, nil,
				huma.Error409Conflict(
					"workbench impact comparison snapshot is no longer valid",
				)
		}
		if err := workbenchComparisonGenerationScope(
			page, current, replacement, confirmed,
		); err != nil {
			return workbenchImpactStreamCursor{}, nil, err
		}
		return previous, nil, nil
	}
	value, err := service.comparison.Compare(
		ctx,
		query,
		request.PageSize,
		previous.Next,
	)
	if err != nil {
		return workbenchImpactStreamCursor{}, nil, err
	}
	exact, err := AsExactCallerComparisonPage(value)
	if err != nil || value.exactSnapshot == "" || value.exactAuthority == "" ||
		exact.Query != normalizeCallerComparisonQuery(query) {
		return workbenchImpactStreamCursor{}, nil,
			huma.Error409Conflict(
				"workbench impact comparison snapshot is unavailable", err,
			)
	}
	if hadPrevious && (value.exactSnapshot != previous.Snapshot ||
		value.exactAuthority != previous.Authority) {
		return workbenchImpactStreamCursor{}, nil,
			huma.Error409Conflict(
				"workbench impact comparison snapshot is no longer valid",
			)
	}
	confirmed := exactCallerComparisonSnapshotConfirmation{
		Snapshot:          value.exactSnapshot,
		MatchingRowsState: exact.MatchingRowsState,
		Old:               exact.Old,
		Replacement:       exact.Replacement,
	}
	if err := workbenchComparisonGenerationScope(
		page, current, replacement, confirmed,
	); err != nil {
		return workbenchImpactStreamCursor{}, nil, err
	}
	state := workbenchImpactStreamCursor{
		Complete:  value.Pagination.Complete,
		Next:      value.Pagination.NextCursor,
		Snapshot:  value.exactSnapshot,
		Authority: value.exactAuthority,
	}
	return state, exact, nil
}

func validateWorkbenchExactCallerPage(
	page *CallerMapPage,
	query CallerMapQuery,
) error {
	if page == nil || page.SchemaVersion != exactCallerMapSchemaVersion ||
		page.Query != normalizeCallerMapQuery(query) || page.Generation == nil ||
		page.exactSnapshot == "" || page.exactAuthority == "" ||
		!stringIn(page.MatchingRowsState, "exact", "unavailable") ||
		page.Coverage != nil || page.CoverageDigest != "" ||
		page.AttributionDigest != "" ||
		page.Generation.Plane != exactCallerMapPlane ||
		page.Generation.Repository != query.Endpoint.Repository {
		return errors.New("caller map exact envelope is invalid")
	}
	switch page.MatchingRowsState {
	case "exact":
		if page.Generation.State != string(callerexecute.PublicationCurrent) ||
			page.Declaration == nil || page.TotalMatchingRows == nil {
			return errors.New("caller map exact envelope is inconsistent")
		}
	case "unavailable":
		if !stringIn(
			page.Generation.State,
			string(callerexecute.PublicationMissing),
			string(callerexecute.PublicationFailed),
			string(callerexecute.PublicationStale),
		) || page.TotalMatchingRows != nil || len(page.Rows) != 0 ||
			len(page.Groups) != 0 || !page.Pagination.Complete ||
			page.Pagination.NextCursor != "" {
			return errors.New("caller map gap envelope is inconsistent")
		}
	}
	return nil
}

func workbenchCallerGenerationScope(
	page *WorkbenchImpactPage,
	capability string,
	target string,
	matchingRowsState string,
	generation CallerMapGeneration,
) error {
	if matchingRowsState == "exact" &&
		generation.State == string(callerexecute.PublicationCurrent) {
		workbenchAddCapability(page, capability, "enabled", "")
		return nil
	}
	if matchingRowsState != "unavailable" || !stringIn(
		generation.State,
		string(callerexecute.PublicationMissing),
		string(callerexecute.PublicationFailed),
		string(callerexecute.PublicationStale),
	) {
		return errors.New("caller generation scope is inconsistent")
	}
	workbenchAddCapability(
		page, capability, "unavailable", generation.State,
	)
	workbenchAddGap(
		page, capability, target, generation.State,
		"caller_generation_"+generation.State,
	)
	return nil
}

func workbenchComparisonGenerationScope(
	page *WorkbenchImpactPage,
	current store.ChangeBriefContractSelection,
	replacement store.ChangeBriefContractSelection,
	confirmed exactCallerComparisonSnapshotConfirmation,
) error {
	if confirmed.Old.Endpoint != workbenchCallerQuery(
		current, WorkbenchImpactFilters{},
	).Endpoint || confirmed.Replacement.Endpoint != workbenchCallerQuery(
		replacement, WorkbenchImpactFilters{},
	).Endpoint || confirmed.Old.Generation.Plane != exactCallerMapPlane ||
		confirmed.Replacement.Generation.Plane != exactCallerMapPlane ||
		confirmed.Old.Generation.Repository != current.Repository ||
		confirmed.Replacement.Generation.Repository != replacement.Repository {
		return errors.New("caller comparison generation scope is mismatched")
	}
	capability := "contract-caller-comparison"
	if confirmed.MatchingRowsState == "exact" {
		if confirmed.Old.MatchingRowsState != "exact" ||
			confirmed.Replacement.MatchingRowsState != "exact" ||
			confirmed.Old.Generation.State !=
				string(callerexecute.PublicationCurrent) ||
			confirmed.Replacement.Generation.State !=
				string(callerexecute.PublicationCurrent) {
			return errors.New("caller comparison exact scope is inconsistent")
		}
		workbenchAddCapability(page, capability, "enabled", "")
		return nil
	}
	if confirmed.MatchingRowsState != "unavailable" {
		return errors.New("caller comparison scope is inconsistent")
	}
	workbenchAddCapability(page, capability, "unavailable", "caller generation gap")
	for _, side := range []struct {
		selection store.ChangeBriefContractSelection
		snapshot  CallerComparisonExactSnapshot
	}{
		{current, confirmed.Old},
		{replacement, confirmed.Replacement},
	} {
		if side.snapshot.MatchingRowsState == "exact" &&
			side.snapshot.Generation.State ==
				string(callerexecute.PublicationCurrent) {
			continue
		}
		if side.snapshot.MatchingRowsState != "unavailable" ||
			!stringIn(
				side.snapshot.Generation.State,
				string(callerexecute.PublicationMissing),
				string(callerexecute.PublicationFailed),
				string(callerexecute.PublicationStale),
			) {
			return errors.New("caller comparison gap scope is inconsistent")
		}
		workbenchAddGap(
			page, capability, workbenchSelectionTarget(side.selection),
			side.snapshot.Generation.State,
			"caller_generation_"+side.snapshot.Generation.State,
		)
	}
	return nil
}

func (service *WorkbenchImpactService) readWorkbenchFieldStream(
	ctx context.Context,
	request WorkbenchImpactRequest,
	fields []compat.FieldIdentity,
	key string,
	cursor *workbenchImpactCursor,
	page *WorkbenchImpactPage,
) (workbenchImpactStreamCursor, *FieldReferencePage, error) {
	previous, hadPrevious := workbenchImpactPreviousStream(cursor, key)
	delegate := previous.Next
	emit := true
	if hadPrevious && previous.Complete {
		delegate = ""
		emit = false
	}
	value, err := service.fields.List(
		ctx,
		FieldReferenceQuery{Fields: fields},
		request.PageSize,
		delegate,
	)
	if err != nil {
		return workbenchImpactStreamCursor{}, nil, err
	}
	snapshot := digestJSON(struct {
		Query      FieldReferenceQuery `json:"query"`
		Total      int                 `json:"total"`
		Coverage   string              `json:"coverage"`
		Visibility VisibilityContext   `json:"visibility"`
	}{
		Query:      value.Query,
		Total:      value.TotalRows,
		Coverage:   value.CoverageDigest,
		Visibility: value.VisibilityContext,
	})
	if hadPrevious && snapshot != previous.Snapshot {
		return workbenchImpactStreamCursor{}, nil,
			huma.Error409Conflict(
				"workbench impact field snapshot is no longer valid",
			)
	}
	workbenchAddCoverage(
		page,
		"proto-field-references",
		"affected-stable-fields",
		value.Coverage,
	)
	workbenchAddCapability(
		page,
		"proto-field-references",
		"enabled",
		"",
	)
	state := workbenchImpactStreamCursor{
		Complete: value.Pagination.Complete,
		Next:     value.Pagination.NextCursor,
		Snapshot: snapshot,
	}
	if !emit {
		return previous, nil, nil
	}
	return state, value, nil
}

func workbenchCallerQuery(
	selection store.ChangeBriefContractSelection,
	filters WorkbenchImpactFilters,
) CallerMapQuery {
	return CallerMapQuery{
		Endpoint: CallerMapEndpoint{
			Protocol:   selection.Protocol,
			Repository: selection.Repository,
			Lineage:    selection.DeclarationLineage,
			Operation:  selection.CanonicalOperation,
		},
		Unit:       filters.Unit,
		Owner:      filters.Owner,
		PathPrefix: filters.PathPrefix,
		CodeRole:   filters.CodeRole,
		Tier:       filters.Tier,
		Freshness:  filters.Freshness,
		Resolution: filters.Resolution,
		Ordering:   filters.Ordering,
	}
}

func workbenchImpactViewCurrent(
	view *store.WorkbenchView,
	request WorkbenchImpactRequest,
) bool {
	return view != nil &&
		view.Investigation.ID == request.InvestigationID &&
		view.Investigation.CurrentRevisionID == request.RevisionID &&
		view.Revision.ID == request.RevisionID &&
		view.Revision.InvestigationID == request.InvestigationID &&
		view.Brief.RevisionID == request.RevisionID &&
		view.Brief.InvestigationID == request.InvestigationID
}

func workbenchImpactRevisionDigest(view *store.WorkbenchView) string {
	if view == nil {
		return ""
	}
	return digestJSON(struct {
		Revision store.Revision    `json:"revision"`
		Brief    store.ChangeBrief `json:"brief"`
	}{Revision: view.Revision, Brief: view.Brief})
}

func workbenchComparisonQuery(
	current, replacement store.ChangeBriefContractSelection,
	filters WorkbenchImpactFilters,
) CallerComparisonQuery {
	return CallerComparisonQuery{
		Old:            workbenchCallerQuery(current, filters).Endpoint,
		Replacement:    workbenchCallerQuery(replacement, filters).Endpoint,
		Unit:           filters.Unit,
		Owner:          filters.Owner,
		PathPrefix:     filters.PathPrefix,
		CodeRole:       filters.CodeRole,
		Tier:           filters.Tier,
		Freshness:      filters.Freshness,
		Resolution:     filters.Resolution,
		Ordering:       filters.Ordering,
		Level:          filters.Level,
		Classification: "",
	}
}

func workbenchSelection(
	selections []store.ChangeBriefContractSelection,
	role store.ChangeBriefSelectionRole,
) (store.ChangeBriefContractSelection, bool) {
	for _, selection := range selections {
		if selection.Role == role {
			return selection, true
		}
	}
	return store.ChangeBriefContractSelection{}, false
}

func workbenchCallerStreamKey(
	selection store.ChangeBriefContractSelection,
) string {
	return "caller:" + digestJSON(selection)
}

func workbenchSelectionTarget(
	selection store.ChangeBriefContractSelection,
) string {
	return strings.Join([]string{
		string(selection.Role),
		selection.Protocol,
		selection.Repository,
		selection.DeclarationLineage,
		selection.CanonicalOperation,
	}, ":")
}

func workbenchScenarioEmphasis(
	kind store.ChangeBriefTicketKind,
) []string {
	switch kind {
	case store.ChangeBriefAdd:
		return []string{
			"analogous_declarations",
			"implementations",
			"selected_resource_dependencies",
		}
	case store.ChangeBriefModify:
		return []string{
			"compatibility_findings",
			"affected_stable_fields",
			"resolved_callers",
			"name_matches",
			"extractor_abstentions",
		}
	case store.ChangeBriefMigrate:
		return []string{
			"old_only_evidence",
			"both_evidence",
			"new_only_evidence",
			"unresolved",
		}
	case store.ChangeBriefRetire:
		return []string{
			"resolved_callers",
			"name_matches",
			"extractor_abstentions",
			"unsupported_planes",
			"analysis_gaps",
		}
	default:
		return []string{}
	}
}

func workbenchImpactPreviousStream(
	cursor *workbenchImpactCursor,
	key string,
) (workbenchImpactStreamCursor, bool) {
	if cursor == nil {
		return workbenchImpactStreamCursor{}, false
	}
	value, ok := cursor.Streams[key]
	return value, ok
}

func validateWorkbenchImpactStreamSet(
	streams map[string]workbenchImpactStreamCursor,
	expected map[string]struct{},
) error {
	if len(streams) != len(expected) {
		return huma.Error409Conflict(
			"workbench impact cursor is no longer valid",
		)
	}
	for key := range expected {
		value, ok := streams[key]
		if !ok || value.Snapshot == "" ||
			value.Complete != (value.Next == "") ||
			len(value.Next) > callerMapCursorLimit ||
			len(value.Authority) > callerMapCursorLimit ||
			(strings.HasPrefix(key, "caller:") ||
				key == "caller-comparison") && value.Authority == "" {
			return huma.Error409Conflict(
				"workbench impact cursor is no longer valid",
			)
		}
	}
	return nil
}

func bindWorkbenchImpactDigest(
	name string,
	bound *string,
	current string,
	hasCursor bool,
) error {
	if !hasCursor {
		*bound = current
		return nil
	}
	if *bound != current {
		return huma.Error409Conflict(
			fmt.Sprintf(
				"workbench impact %s snapshot is no longer valid",
				name,
			),
		)
	}
	return nil
}

func (service *WorkbenchImpactService) decodeWorkbenchImpactCursor(
	encoded, queryDigest, principal, revisionDigest string,
) (*workbenchImpactCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > workbenchImpactCursorLimit {
		return nil, huma.Error400BadRequest(
			"workbench impact cursor is invalid",
		)
	}
	payload, encodedSignature, ok := strings.Cut(encoded, ".")
	if !ok {
		return nil, huma.Error400BadRequest(
			"workbench impact cursor is invalid",
		)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	signature, signatureErr := base64.RawURLEncoding.DecodeString(
		encodedSignature,
	)
	if err != nil || signatureErr != nil ||
		base64.RawURLEncoding.EncodeToString(raw) != payload ||
		base64.RawURLEncoding.EncodeToString(signature) != encodedSignature ||
		len(raw) > workbenchImpactCursorLimit ||
		!json.Valid(raw) {
		return nil, huma.Error400BadRequest(
			"workbench impact cursor is invalid",
		)
	}
	mac := hmac.New(sha256.New, service.cursorSecret[:])
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, huma.Error400BadRequest(
			"workbench impact cursor is invalid",
		)
	}
	var cursor workbenchImpactCursor
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return nil, huma.Error400BadRequest(
			"workbench impact cursor is invalid",
		)
	}
	if cursor.Schema != workbenchImpactCursorVersion ||
		cursor.Streams == nil {
		return nil, huma.Error400BadRequest(
			"workbench impact cursor is invalid",
		)
	}
	if cursor.QueryDigest != queryDigest {
		return nil, huma.Error400BadRequest(
			"workbench impact cursor does not match the query",
		)
	}
	if cursor.Principal != principal ||
		cursor.RevisionDigest != revisionDigest {
		return nil, huma.Error409Conflict(
			"workbench impact cursor is no longer valid",
		)
	}
	return &cursor, nil
}

func (service *WorkbenchImpactService) encodeWorkbenchImpactCursor(
	cursor workbenchImpactCursor,
) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	if len(raw) > workbenchImpactCursorLimit {
		return "", errors.New("workbench impact cursor exceeds its bound")
	}
	mac := hmac.New(sha256.New, service.cursorSecret[:])
	_, _ = mac.Write(raw)
	encoded := base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(encoded) > workbenchImpactCursorLimit {
		return "", errors.New("workbench impact cursor exceeds its bound")
	}
	return encoded, nil
}

func workbenchAddCoverage(
	page *WorkbenchImpactPage,
	capability, target string,
	certificate extract.CoverageCertificate,
) {
	page.AnalysisScope.Coverage = append(
		page.AnalysisScope.Coverage,
		WorkbenchImpactCoverage{
			Capability:  capability,
			Target:      target,
			Digest:      certificate.Digest,
			Certificate: certificate,
		},
	)
	for _, repository := range certificate.Repositories {
		for _, run := range repository.Runs {
			runTarget := strings.Join([]string{
				target,
				repository.Repository,
				run.Domain,
			}, ":")
			if run.Status != "published" {
				workbenchAddGap(
					page,
					capability,
					runTarget,
					"unsupported",
					"no_published_run",
				)
			} else if !run.Fresh {
				workbenchAddGap(
					page,
					capability,
					runTarget,
					"stale",
					"published_run_stale",
				)
			}
			if run.LatestAttempt != nil &&
				run.LatestAttempt.Status == "aborted" {
				workbenchAddGap(
					page,
					capability,
					runTarget,
					"failed",
					"latest_attempt_failed",
				)
			}
		}
	}
}

func workbenchAddGap(
	page *WorkbenchImpactPage,
	capability, target, state, code string,
) {
	page.AnalysisScope.Gaps = append(
		page.AnalysisScope.Gaps,
		WorkbenchImpactGap{
			Capability: capability,
			Target:     target,
			State:      state,
			Code:       code,
		},
	)
}

func workbenchAddCapability(
	page *WorkbenchImpactPage,
	id, state, reason string,
) {
	for _, capability := range page.AnalysisScope.Capabilities {
		if capability.ID == id {
			return
		}
	}
	page.AnalysisScope.Capabilities = append(
		page.AnalysisScope.Capabilities,
		WorkbenchImpactCapability{
			ID: id, State: state, Reason: reason,
		},
	)
}

func workbenchResourcePlaneScope(page *WorkbenchImpactPage) {
	for _, plane := range page.ResourcePlanes {
		page.AnalysisScope.Capabilities = append(
			page.AnalysisScope.Capabilities,
			WorkbenchImpactCapability{
				ID:     "resource:" + plane.ID,
				State:  string(plane.State),
				Reason: plane.Reason,
			},
		)
		if plane.State != ResourcePlaneEnabled {
			workbenchAddGap(
				page,
				"resource:"+plane.ID,
				"",
				string(plane.State),
				plane.Reason,
			)
		}
	}
}

func sortWorkbenchImpactScope(scope *WorkbenchAnalysisScope) {
	sort.Slice(scope.Coverage, func(left, right int) bool {
		if scope.Coverage[left].Capability !=
			scope.Coverage[right].Capability {
			return scope.Coverage[left].Capability <
				scope.Coverage[right].Capability
		}
		return scope.Coverage[left].Target <
			scope.Coverage[right].Target
	})
	sort.Slice(scope.Capabilities, func(left, right int) bool {
		return scope.Capabilities[left].ID <
			scope.Capabilities[right].ID
	})
	sort.Slice(scope.Gaps, func(left, right int) bool {
		for _, comparison := range []int{
			strings.Compare(
				scope.Gaps[left].Capability,
				scope.Gaps[right].Capability,
			),
			strings.Compare(
				scope.Gaps[left].Target,
				scope.Gaps[right].Target,
			),
			strings.Compare(
				scope.Gaps[left].State,
				scope.Gaps[right].State,
			),
			strings.Compare(
				scope.Gaps[left].Code,
				scope.Gaps[right].Code,
			),
		} {
			if comparison != 0 {
				return comparison < 0
			}
		}
		return false
	})
	scope.Gaps = slices.Compact(scope.Gaps)
}
