package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

// CallerComparisonQueries is T20.13's transport-neutral comparison boundary.
// MCP supplies the complete query and cannot classify or aggregate evidence.
type CallerComparisonQueries interface {
	Compare(
		context.Context,
		api.CallerComparisonQuery,
		int,
		string,
	) (*api.CallerComparisonPage, error)
}

func registerCallerComparisonTool(s *sdk.Server, opts Options) {
	if opts.ContractCatalog == nil || opts.CallerMap == nil ||
		opts.CallerComparison == nil {
		return
	}
	type comparisonIn struct {
		OldProtocol           string `json:"old_protocol" jsonschema:"old endpoint protocol returned by search_contract_operations"`
		OldRepository         string `json:"old_repository" jsonschema:"old endpoint declaration repository"`
		OldLineage            string `json:"old_declaration_lineage" jsonschema:"old endpoint declaration lineage"`
		OldOperation          string `json:"old_operation" jsonschema:"old endpoint canonical operation"`
		ReplacementProtocol   string `json:"replacement_protocol" jsonschema:"replacement endpoint protocol returned by search_contract_operations"`
		ReplacementRepository string `json:"replacement_repository" jsonschema:"replacement endpoint declaration repository"`
		ReplacementLineage    string `json:"replacement_declaration_lineage" jsonschema:"replacement endpoint declaration lineage"`
		ReplacementOperation  string `json:"replacement_operation" jsonschema:"replacement endpoint canonical operation"`
		Unit                  string `json:"unit,omitempty" jsonschema:"optional exact attributed unit candidate id"`
		Owner                 string `json:"owner,omitempty" jsonschema:"optional exact owner from unit attribution"`
		PathPrefix            string `json:"path_prefix,omitempty" jsonschema:"optional source path prefix"`
		CodeRole              string `json:"code_role,omitempty" jsonschema:"optional code role filter"`
		Tier                  string `json:"tier,omitempty" jsonschema:"optional evidence tier filter"`
		Freshness             string `json:"freshness,omitempty" jsonschema:"any, fresh, or stale"`
		Resolution            string `json:"resolution,omitempty" jsonschema:"any, scip, syntax, or unresolved"`
		Ordering              string `json:"ordering,omitempty" jsonschema:"source or unit stable ordering"`
		Level                 string `json:"level,omitempty" jsonschema:"occurrence or unit; default occurrence"`
		Classification        string `json:"classification,omitempty" jsonschema:"old_only_evidence, both_evidence, new_only_evidence, or unresolved"`
		PageSize              int    `json:"page_size,omitempty" jsonschema:"comparison rows per page; default 50, maximum 100"`
		Cursor                string `json:"cursor,omitempty" jsonschema:"opaque continuation cursor returned by the preceding page"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "compare_operation_callers",
		Description: "Compare two exact Contract Atlas endpoint identities through two jointly fenced, authorized " +
			"repository-overlay caller generations. Pages occurrence- or unit-level old_only_evidence, " +
			"both_evidence, new_only_evidence, and unresolved classifications with bounded immutable-range " +
			"citations and one cursor bound to both generations. If either generation is unavailable, rows, " +
			"classifications, and totals are unavailable rather than inferred from absence. These labels do not " +
			"establish migration completion, runtime use, completeness, or decommissioning safety.",
		OutputSchema: callerComparisonOutputSchema(),
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in comparisonIn,
	) (*sdk.CallToolResult, api.CallerComparisonExactPage, error) {
		result, err := opts.CallerComparison.Compare(
			ctx,
			api.CallerComparisonQuery{
				Old: api.CallerMapEndpoint{
					Protocol: in.OldProtocol, Repository: in.OldRepository,
					Lineage: in.OldLineage, Operation: in.OldOperation,
				},
				Replacement: api.CallerMapEndpoint{
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
			},
			in.PageSize,
			in.Cursor,
		)
		if err != nil {
			return nil, api.CallerComparisonExactPage{}, err
		}
		exact, err := api.AsExactCallerComparisonPage(result)
		if err != nil {
			return nil, api.CallerComparisonExactPage{}, err
		}
		return nil, *exact, nil
	})
}

func callerComparisonOutputSchema() map[string]any {
	snapshot := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"endpoint", "generation", "matching_rows_state",
		},
		"properties": map[string]any{
			"endpoint":    map[string]any{"type": "object"},
			"declaration": map[string]any{"type": "object"},
			"generation":  map[string]any{"type": "object"},
			"matching_rows_state": map[string]any{
				"type": "string", "enum": []string{"exact", "unavailable"},
			},
		},
	}
	return topLevelOutputSchema(
		[]string{
			"schema_version", "query", "old", "replacement", "rows",
			"matching_rows_state", "pagination", "caveat",
		},
		map[string]any{
			"schema_version": map[string]any{"type": "string"},
			"query":          map[string]any{"type": "object"},
			"old":            snapshot,
			"replacement":    snapshot,
			"rows":           objectArraySchema(),
			"total_rows":     map[string]any{"type": "integer"},
			"matching_rows_state": map[string]any{
				"type": "string", "enum": []string{"exact", "unavailable"},
			},
			"pagination": map[string]any{"type": "object"},
			"caveat":     map[string]any{"type": "string"},
		},
	)
}
