package api

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
)

// OperationImpactSchemaVersion is the independent operation report contract.
const OperationImpactSchemaVersion = "contract-impact-operation-v1"
const operationImpactCapability = "contract-impact-operation"
const operationImpactShape = "operation_callers"

type operationImpactCallers interface {
	List(context.Context, CallerMapQuery, int, string) (*CallerMapPage, error)
}

// OperationImpactService wraps the already-constructed exact Caller Map
// service. It owns no reader, index, cursor secret, cache, store or classifier.
type OperationImpactService struct{ callers operationImpactCallers }

func NewOperationImpactService(opts Options) *OperationImpactService {
	if !opts.OperationImpactEnabled || opts.CallerMap == nil || opts.CallerMap.exact == nil {
		return nil
	}
	return &OperationImpactService{callers: opts.CallerMap}
}

type OperationImpactConclusion struct {
	State            string         `json:"state" enum:"evidence_present,exact_empty,unavailable"`
	Query            CallerMapQuery `json:"query"`
	GenerationDigest string         `json:"generation_digest,omitempty"`
	Qualification    string         `json:"qualification"`
}

type OperationImpactUnavailableSections struct {
	FieldReferences        string `json:"field_references" enum:"not_in_supported_shape"`
	ProposedChange         string `json:"proposed_change" enum:"not_in_supported_shape"`
	Implementations        string `json:"implementations" enum:"not_in_supported_shape"`
	WireVerdict            string `json:"wire_verdict" enum:"not_in_supported_shape"`
	MatchingSyntacticCalls string `json:"matching_syntactic_calls" enum:"not_in_supported_shape"`
	SavedReport            string `json:"saved_report" enum:"not_in_supported_shape"`
}

type OperationImpactReport struct {
	SchemaVersion       string                             `json:"schema_version" enum:"contract-impact-operation-v1"`
	Shape               string                             `json:"shape" enum:"operation_callers"`
	CallerPage          *CallerMapPage                     `json:"caller_page"`
	Conclusion          OperationImpactConclusion          `json:"conclusion"`
	UnavailableSections OperationImpactUnavailableSections `json:"unavailable_sections"`
}

func (service *OperationImpactService) Build(ctx context.Context, shape string, query CallerMapQuery, pageSize int, cursor string) (*OperationImpactReport, error) {
	if service == nil || service.callers == nil {
		return nil, huma.Error503ServiceUnavailable("operation impact report unavailable")
	}
	if shape != operationImpactShape {
		if stringIn(shape, "field_references", "proposed_change", "implementations", "wire_verdict", "matching_syntactic_calls", "saved_report", "field", "change", "saved") {
			return nil, huma.Error503ServiceUnavailable("report shape is not supported by the operation impact recipe")
		}
		return nil, huma.Error400BadRequest("unknown operation impact report shape")
	}
	if query.Endpoint.Protocol != "protobuf" {
		return nil, huma.Error400BadRequest("operation impact requires an exact protobuf endpoint")
	}
	query = normalizeCallerMapQuery(query)
	page, err := service.callers.List(ctx, query, pageSize, cursor)
	if err != nil {
		return nil, err
	}
	if err := validateOperationImpactPage(page, query, pageSize); err != nil {
		return nil, huma.Error500InternalServerError("operation impact source response is inconsistent", err)
	}
	state, qualification := "unavailable", "Caller evidence is unavailable for this generation; no zero-caller or absence conclusion is made."
	if page.MatchingRowsState == "exact" {
		state, qualification = "evidence_present", "Static caller evidence matches this endpoint scope; per-row resolution applies. This is not runtime impact, completeness or retirement safety."
		if *page.TotalMatchingRows == 0 {
			state, qualification = "exact_empty", "No matching static caller evidence in this exact filtered source scope; this does not establish absence."
		}
	}
	return &OperationImpactReport{
		SchemaVersion: OperationImpactSchemaVersion, Shape: operationImpactShape,
		CallerPage: page,
		Conclusion: OperationImpactConclusion{State: state, Query: page.Query, GenerationDigest: page.Generation.GenerationDigest, Qualification: qualification},
		UnavailableSections: OperationImpactUnavailableSections{
			FieldReferences: "not_in_supported_shape", ProposedChange: "not_in_supported_shape", Implementations: "not_in_supported_shape", WireVerdict: "not_in_supported_shape", MatchingSyntacticCalls: "not_in_supported_shape", SavedReport: "not_in_supported_shape",
		},
	}, nil
}

// This fixed metadata check adds no row walk. The sole trusted List call
// already validates every row and its source, unit and publication authority.
func validateOperationImpactPage(page *CallerMapPage, query CallerMapQuery, pageSize int) error {
	if pageSize == 0 {
		pageSize = callerMapDefaultPage
	}
	if page == nil || page.SchemaVersion != exactCallerMapSchemaVersion || page.Query != query || page.Generation == nil || page.Scope == nil || page.Rows == nil || len(page.Rows) > pageSize || !stringIn(page.Generation.State, "current", "missing", "stale", "failed") || page.Generation.Repository != query.Endpoint.Repository || page.Generation.Plane != exactCallerMapPlane || page.Scope.Repository != query.Endpoint.Repository || page.Coverage != nil || page.CoverageDigest != "" || page.AttributionDigest != "" || page.Caveat == "" {
		return errors.New("invalid exact caller page metadata")
	}
	if page.MatchingRowsState == "unavailable" {
		// Scope describes the present indexed repository. An unavailable caller
		// generation may be absent or retained from a different commit/unit.
		// Keep both unchanged, without declaration locators or a zero count.
		if page.Generation.State == "current" || page.Declaration != nil || len(page.Rows) != 0 || page.TotalMatchingRows != nil || !page.Pagination.Complete || page.Pagination.NextCursor != "" {
			return errors.New("unavailable caller page claimed rows or totals")
		}
		return nil
	}
	unitDigest := ""
	if page.Scope.AnalysisUnit != nil {
		unitDigest = page.Scope.AnalysisUnit.Digest
	}
	if page.Scope.Commit != page.Generation.Commit || unitDigest != page.Generation.UnitDigest {
		return errors.New("exact caller scope differs from current generation")
	}
	if page.MatchingRowsState != "exact" || page.Generation.State != "current" || page.Generation.GenerationDigest == "" || page.TotalMatchingRows == nil || *page.TotalMatchingRows < len(page.Rows) || *page.TotalMatchingRows > exactCallerMapBindingRefs || (page.Pagination.Complete && page.Pagination.NextCursor != "") || (!page.Pagination.Complete && (page.Pagination.NextCursor == "" || len(page.Rows) != pageSize)) || (*page.TotalMatchingRows == 0 && !page.Pagination.Complete) || (*page.TotalMatchingRows > 0 && len(page.Rows) == 0) {
		return errors.New("invalid exact caller generation/count metadata")
	}
	return nil
}

type operationImpactIn struct {
	Shape      string `query:"shape" required:"true" maxLength:"64"`
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

func (in *operationImpactIn) Resolve(ctx huma.Context) []error {
	url := ctx.URL()
	for key, values := range url.Query() {
		if len(values) != 1 || !stringIn(key, "shape", "protocol", "repository", "lineage", "operation", "unit", "owner", "path_prefix", "code_role", "tier", "freshness", "resolution", "ordering", "page_size", "cursor") {
			return []error{huma.Error400BadRequest("mixed, repeated or unknown operation impact input")}
		}
	}
	return nil
}

func registerOperationImpactAPI(api huma.API, opts Options) {
	if opts.OperationImpact == nil {
		return
	}
	type output struct{ Body OperationImpactReport }
	huma.Get(api, "/api/operation_impact_report", func(ctx context.Context, in *operationImpactIn) (*output, error) {
		report, err := opts.OperationImpact.Build(ctx, in.Shape, CallerMapQuery{
			Endpoint: CallerMapEndpoint{Protocol: in.Protocol, Repository: in.Repository, Lineage: in.Lineage, Operation: in.Operation},
			Unit:     in.Unit, Owner: in.Owner, PathPrefix: in.PathPrefix, CodeRole: in.CodeRole, Tier: in.Tier, Freshness: in.Freshness, Resolution: in.Resolution, Ordering: in.Ordering,
		}, in.PageSize, in.Cursor)
		if err != nil {
			return nil, err
		}
		return &output{Body: *report}, nil
	})
}
