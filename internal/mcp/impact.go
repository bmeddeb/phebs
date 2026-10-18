package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

type OperationImpactQueries interface {
	Build(context.Context, string, api.CallerMapQuery, int, string) (*api.OperationImpactReport, error)
}

func registerOperationImpactTool(server *sdk.Server, opts Options) {
	if opts.OperationImpact == nil {
		return
	}
	callerPageSchema := callerMapPageOutputSchema()
	properties := callerPageSchema["properties"].(map[string]any)
	for _, legacy := range []string{"coverage", "coverage_digest", "attribution_digest"} {
		delete(properties, legacy)
	}
	properties["schema_version"] = map[string]any{"type": "string", "const": "caller-map-v2"}
	sections := map[string]any{}
	sectionNames := []string{"field_references", "proposed_change", "implementations", "wire_verdict", "matching_syntactic_calls", "saved_report"}
	for _, name := range sectionNames {
		sections[name] = map[string]any{"type": "string", "const": "not_in_supported_shape"}
	}
	type input struct {
		Shape      string `json:"shape" jsonschema:"operation_callers; other report shapes are unsupported"`
		Protocol   string `json:"protocol" jsonschema:"protobuf"`
		Repository string `json:"repository" jsonschema:"exact visible declaration repository"`
		Lineage    string `json:"declaration_lineage" jsonschema:"exact declaration lineage; distinct from a SCIP field family"`
		Operation  string `json:"operation" jsonschema:"canonical /package.Service/Method selected from Contracts"`
		Unit       string `json:"unit,omitempty"`
		Owner      string `json:"owner,omitempty"`
		PathPrefix string `json:"path_prefix,omitempty"`
		CodeRole   string `json:"code_role,omitempty"`
		Tier       string `json:"tier,omitempty"`
		Freshness  string `json:"freshness,omitempty"`
		Resolution string `json:"resolution,omitempty"`
		Ordering   string `json:"ordering,omitempty"`
		PageSize   int    `json:"page_size,omitempty" jsonschema:"default50, maximum100"`
		Cursor     string `json:"cursor,omitempty" jsonschema:"opaque exact Caller Map continuation cursor"`
	}
	sdk.AddTool(server, &sdk.Tool{
		Name:        "build_operation_impact_report",
		Description: "Inspect one exact protobuf endpoint's paged static Caller Map evidence, source authority and unchanged resolution/unit uncertainty. Does not establish runtime impact, completeness, absence, migration completion or retirement safety. Field/change/implementation/WIRE/matching/saved shapes remain unavailable. Read citations through read_operation_caller_citation.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
		OutputSchema: topLevelOutputSchema([]string{"schema_version", "shape", "caller_page", "conclusion", "unavailable_sections"}, map[string]any{
			"schema_version": map[string]any{"type": "string", "const": api.OperationImpactSchemaVersion},
			"shape":          map[string]any{"type": "string", "const": "operation_callers"},
			"caller_page":    callerPageSchema,
			"conclusion": topLevelOutputSchema([]string{"state", "query", "qualification"}, map[string]any{
				"state": map[string]any{"type": "string", "enum": []string{"evidence_present", "exact_empty", "unavailable"}},
				"query": map[string]any{"type": "object"}, "generation_digest": map[string]any{"type": "string"}, "qualification": map[string]any{"type": "string"},
			}),
			"unavailable_sections": topLevelOutputSchema(sectionNames, sections),
		}),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in input) (*sdk.CallToolResult, api.OperationImpactReport, error) {
		report, err := opts.OperationImpact.Build(ctx, in.Shape, api.CallerMapQuery{
			Endpoint: api.CallerMapEndpoint{Protocol: in.Protocol, Repository: in.Repository, Lineage: in.Lineage, Operation: in.Operation},
			Unit:     in.Unit, Owner: in.Owner, PathPrefix: in.PathPrefix, CodeRole: in.CodeRole, Tier: in.Tier, Freshness: in.Freshness, Resolution: in.Resolution, Ordering: in.Ordering,
		}, in.PageSize, in.Cursor)
		if err != nil {
			return nil, api.OperationImpactReport{}, err
		}
		return nil, *report, nil
	})
}
