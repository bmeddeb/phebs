package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/glossary"
	"github.com/bmeddeb/phebs/internal/store"
)

// WorkbenchChecklistQueries is the narrow T21.9 mutation boundary. The MCP
// adapter deliberately has no checklist/evidence read method: agents drill
// into evidence through the existing Epic 20 and core tools, while this
// service validates the submitted deterministic suggestion against the
// current shared projection before appending a Disposition.
type WorkbenchChecklistQueries interface {
	RecordDisposition(
		context.Context,
		string,
		api.WorkbenchDispositionMutation,
	) (*store.WorkbenchDisposition, error)
}

// WorkbenchImpactQueries is the shared T38.3 evidence boundary. The MCP
// adapter forwards the exact HTTP request and response types so service
// authority, gaps, pagination, authorization, and errors cannot drift.
type WorkbenchImpactQueries interface {
	Read(
		context.Context,
		string,
		api.WorkbenchImpactRequest,
	) (*api.WorkbenchImpactPage, error)
}

type getChangeWorkbenchInput struct {
	InvestigationID string `json:"investigation_id" jsonschema:"exact Investigation id returned by create_change_workbench or an authorized prior read"`
}

// These transport fields intentionally mirror api.WorkbenchImpactRequest
// without reusing its reflected schema. The MCP SDK keeps a process-global
// reflection cache, and reusing the nested API filter type would rewrite the
// already-pinned Workbench disposition schema depending on registration
// order. The handler below performs the explicit 1:1 projection.
type workbenchImpactFiltersInput struct {
	Unit              string `json:"unit,omitempty"`
	Owner             string `json:"owner,omitempty"`
	PathPrefix        string `json:"path_prefix,omitempty"`
	CodeRole          string `json:"code_role,omitempty"`
	Tier              string `json:"tier,omitempty"`
	Freshness         string `json:"freshness,omitempty"`
	Resolution        string `json:"resolution,omitempty"`
	Ordering          string `json:"ordering,omitempty"`
	Level             string `json:"level,omitempty"`
	ServiceRepository string `json:"service_repository,omitempty"`
	SourceService     string `json:"source_service,omitempty"`
	TargetService     string `json:"target_service,omitempty"`
}

type workbenchImpactInput struct {
	InvestigationID  string                      `json:"investigation_id"`
	RevisionID       string                      `json:"revision_id"`
	CompatibilityRun string                      `json:"compatibility_run_id,omitempty"`
	Filters          workbenchImpactFiltersInput `json:"filters,omitempty"`
	PageSize         int                         `json:"page_size,omitempty"`
	Cursor           string                      `json:"cursor,omitempty"`
}

func registerWorkbenchTools(server *sdk.Server, opts Options) {
	if opts.Workbench == nil || opts.WorkbenchChecklist == nil ||
		opts.Principal == nil {
		return
	}

	sdk.AddTool(server, &sdk.Tool{
		Name: "preview_change_workbench",
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: workbenchBool(false),
		},
		Description: "Read-only preview of one Change Workbench plan through the shared service. " +
			"It writes no Investigation, revision, evidence, or Disposition, but requires a named API key with " +
			"investigation:write because its digest can bind a later durable creation. " +
			workbenchGlossaryDescription(glossary.TermSuccessCriterion),
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		plan store.WorkbenchPlan,
	) (*sdk.CallToolResult, store.WorkbenchPreview, error) {
		if err := requireWorkbenchMutationCredential(ctx, opts); err != nil {
			return nil, store.WorkbenchPreview{}, err
		}
		principal, err := workbenchPrincipal(ctx, opts)
		if err != nil {
			return nil, store.WorkbenchPreview{}, err
		}
		result, err := opts.Workbench.Preview(ctx, principal, plan)
		if err != nil {
			return nil, store.WorkbenchPreview{}, workbenchToolError(err)
		}
		return nil, *result, nil
	})

	sdk.AddTool(server, &sdk.Tool{
		Name: "get_change_workbench",
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: workbenchBool(false),
		},
		Description: "Read one authorized current Change Workbench through the shared service. " +
			"The result preserves its exact Investigation revision, human-authored brief, endpoint/source commitments, " +
			"and authority boundaries; the read creates no evidence or durable state. " +
			workbenchGlossaryDescription(glossary.TermImplementationEvidence),
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in getChangeWorkbenchInput,
	) (*sdk.CallToolResult, store.WorkbenchView, error) {
		principal, err := workbenchPrincipal(ctx, opts)
		if err != nil {
			return nil, store.WorkbenchView{}, err
		}
		result, err := opts.Workbench.Read(
			ctx,
			principal,
			in.InvestigationID,
		)
		if err != nil {
			return nil, store.WorkbenchView{}, workbenchToolError(err)
		}
		return nil, *result, nil
	})

	if !opts.AdvertiseWorkbenchMutations {
		registerWorkbenchImpactTool(server, opts)
		return
	}

	sdk.AddTool(server, &sdk.Tool{
		Name: "create_change_workbench",
		Annotations: &sdk.ToolAnnotations{
			DestructiveHint: workbenchBool(false),
			IdempotentHint:  true,
			OpenWorldHint:   workbenchBool(false),
		},
		Description: "Durably create one preview-bound Change Workbench Investigation and initial immutable revision " +
			"through the shared service. This is an explicit write: it requires a named API key with " +
			"investigation:write, an unchanged ready preview digest, and an idempotency key; retries and conflicts " +
			"retain the existing principal, authorization, snapshot, and revision checks.",
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in store.WorkbenchMutationRequest,
	) (*sdk.CallToolResult, store.WorkbenchView, error) {
		if err := requireWorkbenchMutationCredential(ctx, opts); err != nil {
			return nil, store.WorkbenchView{}, err
		}
		principal, err := workbenchPrincipal(ctx, opts)
		if err != nil {
			return nil, store.WorkbenchView{}, err
		}
		result, err := opts.Workbench.Create(ctx, principal, in)
		if err != nil {
			return nil, store.WorkbenchView{}, workbenchToolError(err)
		}
		return nil, *result, nil
	})

	sdk.AddTool(server, &sdk.Tool{
		Name: "record_change_disposition",
		Annotations: &sdk.ToolAnnotations{
			DestructiveHint: workbenchBool(false),
			IdempotentHint:  true,
			OpenWorldHint:   workbenchBool(false),
		},
		Description: "Durably append one immutable human Disposition for an exact current Workbench suggestion " +
			"through the shared checklist service. This is an explicit write: it requires a named API key with " +
			"investigation:write, owner authority, the expected current revision, the exact evidence-bound suggestion, " +
			"and an idempotency key. It creates no task, comment, completion Decision, or safety conclusion.",
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in api.WorkbenchDispositionMutation,
	) (*sdk.CallToolResult, store.WorkbenchDisposition, error) {
		if err := requireWorkbenchMutationCredential(ctx, opts); err != nil {
			return nil, store.WorkbenchDisposition{}, err
		}
		principal, err := workbenchPrincipal(ctx, opts)
		if err != nil {
			return nil, store.WorkbenchDisposition{}, err
		}
		result, err := opts.WorkbenchChecklist.RecordDisposition(
			ctx,
			principal,
			in,
		)
		if err != nil {
			return nil, store.WorkbenchDisposition{}, workbenchToolError(err)
		}
		return nil, *result, nil
	})
	registerWorkbenchImpactTool(server, opts)
}

func registerWorkbenchImpactTool(server *sdk.Server, opts Options) {
	if opts.WorkbenchImpact == nil {
		return
	}
	sdk.AddTool(server, &sdk.Tool{
		Name: "get_change_workbench_impact",
		// Contract catalog messages are recursively nested, so the SDK's
		// reflection builder cannot derive this schema. The strict top level
		// names every shared page field; nested objects remain governed by the
		// bounded shared API service that produced them.
		OutputSchema: workbenchImpactOutputSchema(),
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: workbenchBool(false),
		},
		Description: "Read one authorized exact Change Workbench impact page through the shared HTTP service. " +
			"The result preserves service scope, relationship authority, gaps, pagination, and evidence caveats. " +
			"It creates no write, task, Decision, runtime-topology claim, completeness claim, or safety conclusion.",
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in workbenchImpactInput,
	) (*sdk.CallToolResult, api.WorkbenchImpactPage, error) {
		principal, err := workbenchPrincipal(ctx, opts)
		if err != nil {
			return nil, api.WorkbenchImpactPage{}, err
		}
		result, err := opts.WorkbenchImpact.Read(ctx, principal, api.WorkbenchImpactRequest{
			InvestigationID:  in.InvestigationID,
			RevisionID:       in.RevisionID,
			CompatibilityRun: in.CompatibilityRun,
			Filters: api.WorkbenchImpactFilters{
				Unit:              in.Filters.Unit,
				Owner:             in.Filters.Owner,
				PathPrefix:        in.Filters.PathPrefix,
				CodeRole:          in.Filters.CodeRole,
				Tier:              in.Filters.Tier,
				Freshness:         in.Filters.Freshness,
				Resolution:        in.Filters.Resolution,
				Ordering:          in.Filters.Ordering,
				Level:             in.Filters.Level,
				ServiceRepository: in.Filters.ServiceRepository,
				SourceService:     in.Filters.SourceService,
				TargetService:     in.Filters.TargetService,
			},
			PageSize: in.PageSize,
			Cursor:   in.Cursor,
		})
		if err != nil {
			return nil, api.WorkbenchImpactPage{}, workbenchToolError(err)
		}
		if err := api.ValidateWorkbenchImpactResponse(result); err != nil {
			return nil, api.WorkbenchImpactPage{}, err
		}
		return nil, *result, nil
	})
}

func workbenchImpactOutputSchema() map[string]any {
	stringProperty := map[string]any{"type": "string"}
	objectProperty := map[string]any{"type": "object"}
	return topLevelOutputSchema(
		[]string{
			"schema_version", "investigation_id", "revision_id", "ticket_kind",
			"scenario_emphasis", "atlas", "callers", "resource_planes",
			"analysis_scope", "pagination", "caveat",
		},
		map[string]any{
			"schema_version":        stringProperty,
			"investigation_id":      stringProperty,
			"revision_id":           stringProperty,
			"ticket_kind":           stringProperty,
			"scenario_emphasis":     map[string]any{"type": "array", "items": stringProperty},
			"atlas":                 objectArraySchema(),
			"callers":               objectArraySchema(),
			"comparison":            objectProperty,
			"compatibility":         objectProperty,
			"field_references":      objectProperty,
			"resource_planes":       objectArraySchema(),
			"relationship_coverage": objectProperty,
			"service_impact":        objectProperty,
			"analysis_scope":        objectProperty,
			"pagination":            objectProperty,
			"caveat":                stringProperty,
		},
	)
}

func workbenchBool(value bool) *bool {
	return &value
}

func workbenchPrincipal(ctx context.Context, opts Options) (string, error) {
	principal := strings.TrimSpace(opts.Principal(ctx))
	if principal == "" {
		return "", errors.New("workbench resource not found")
	}
	return principal, nil
}

func requireWorkbenchMutationCredential(
	ctx context.Context,
	opts Options,
) error {
	if opts.InvestigationMutation == nil ||
		!opts.InvestigationMutation(ctx) {
		return errors.New("workbench resource not found")
	}
	return nil
}

func workbenchToolError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("workbench resource not found")
	}
	var statusError huma.StatusError
	if errors.As(err, &statusError) &&
		statusError.GetStatus() == 404 {
		return errors.New("workbench resource not found")
	}
	return err
}

func workbenchGlossaryDescription(id glossary.TermID) string {
	for _, term := range glossary.Terms {
		if term.ID != id {
			continue
		}
		return strings.Join([]string{
			term.ShortHelp,
			term.ExpandedHelp,
			"Evidence boundary: " + term.EvidenceBoundary,
			"Authority boundary: " + term.AuthorityBoundary,
		}, " ")
	}
	panic("missing generated Workbench glossary term " + string(id))
}
