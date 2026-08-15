package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

// ContractCatalogQueries is T20.11's endpoint-discovery boundary. Its methods
// are implemented by api.ContractCatalogService, keeping MCP unable to
// enumerate evidence or reinterpret declaration identity.
type ContractCatalogQueries interface {
	List(
		context.Context,
		api.ContractCatalogQuery,
		int,
		string,
	) (*api.ContractCatalogList, error)
	OperationForProtocol(
		context.Context,
		string,
		string,
		string,
		string,
	) (*api.ContractCatalogOperation, error)
}

// CallerMapQueries is the transport-neutral paged read boundary. T30.6j moves
// the public implementation to one exact complete repository generation.
// MCP forwards the complete query and returns its response without filtering,
// regrouping, cursor translation, or summary.
type CallerMapQueries interface {
	List(
		context.Context,
		api.CallerMapQuery,
		int,
		string,
	) (*api.CallerMapPage, error)
}

type callerCitationQueries interface {
	CitationAvailable() bool
	ReadCitation(context.Context, string) (*api.CallerMapCitation, error)
}

type contractOperationResult struct {
	Endpoint api.CallerMapEndpoint        `json:"endpoint"`
	Detail   api.ContractCatalogOperation `json:"detail"`
}

func registerCallerMapTools(s *sdk.Server, opts Options) {
	if opts.ContractCatalog == nil || opts.CallerMap == nil {
		return
	}

	type searchOperationsIn struct {
		Repository string `json:"repository,omitempty" jsonschema:"optional exact visible repository identity"`
		Package    string `json:"package,omitempty" jsonschema:"optional exact package or Thrift scope"`
		Protocol   string `json:"protocol,omitempty" jsonschema:"optional contract protocol: protobuf or thrift"`
		Lineage    string `json:"lineage,omitempty" jsonschema:"optional exact declaration lineage"`
		PageSize   int    `json:"page_size,omitempty" jsonschema:"bounded catalog rows per page; default 50, maximum 100"`
		Cursor     string `json:"cursor,omitempty" jsonschema:"opaque continuation cursor returned by the preceding page"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "search_contract_operations",
		Description: "Discover selectable contract declarations through the permission-scoped Contract Atlas. " +
			"Returns the shared bounded catalog page, including full protocol, repository, declaration-lineage, " +
			"and canonical-operation identities plus coverage and an opaque continuation cursor. Select rows with kind=operation.",
		OutputSchema: contractCatalogListOutputSchema(),
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in searchOperationsIn,
	) (*sdk.CallToolResult, api.ContractCatalogList, error) {
		result, err := opts.ContractCatalog.List(
			ctx,
			api.ContractCatalogQuery{
				Repository: in.Repository,
				Package:    in.Package,
				Protocol:   in.Protocol,
				Lineage:    in.Lineage,
			},
			in.PageSize,
			in.Cursor,
		)
		if err != nil {
			return nil, api.ContractCatalogList{}, err
		}
		return nil, *result, nil
	})

	type operationDetailIn struct {
		Protocol   string `json:"protocol" jsonschema:"exact protocol returned by search_contract_operations"`
		Repository string `json:"repository" jsonschema:"exact visible repository returned by search_contract_operations"`
		Lineage    string `json:"declaration_lineage" jsonschema:"exact declaration lineage returned by search_contract_operations"`
		Operation  string `json:"operation" jsonschema:"exact canonical operation /scope.Service/method returned by search_contract_operations"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_contract_operation",
		Description: "Read one exact Contract Atlas operation selected by its complete protocol, repository, " +
			"declaration-lineage, and canonical-operation identity. Returns request/response shapes, immutable " +
			"declaration citations, adjacent relationship evidence, coverage, and the selected endpoint identity.",
		OutputSchema: contractOperationOutputSchema(),
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in operationDetailIn,
	) (*sdk.CallToolResult, contractOperationResult, error) {
		detail, err := opts.ContractCatalog.OperationForProtocol(
			ctx,
			in.Protocol,
			in.Repository,
			in.Lineage,
			in.Operation,
		)
		if err != nil {
			return nil, contractOperationResult{}, err
		}
		if detail.Protocol != in.Protocol {
			return nil, contractOperationResult{}, errors.New(
				"contract operation service returned a different protocol identity",
			)
		}
		return nil, contractOperationResult{
			Endpoint: api.CallerMapEndpoint{
				Protocol: in.Protocol, Repository: in.Repository,
				Lineage: in.Lineage, Operation: in.Operation,
			},
			Detail: *detail,
		}, nil
	})

	type operationCallersIn struct {
		Protocol   string `json:"protocol" jsonschema:"exact protocol returned by search_contract_operations"`
		Repository string `json:"repository" jsonschema:"exact declaration repository returned by search_contract_operations"`
		Lineage    string `json:"declaration_lineage" jsonschema:"exact declaration lineage returned by search_contract_operations"`
		Operation  string `json:"operation" jsonschema:"exact canonical operation returned by search_contract_operations"`
		Unit       string `json:"unit,omitempty" jsonschema:"optional exact attributed unit candidate id"`
		Owner      string `json:"owner,omitempty" jsonschema:"optional exact owner from unit attribution"`
		PathPrefix string `json:"path_prefix,omitempty" jsonschema:"optional source path prefix"`
		CodeRole   string `json:"code_role,omitempty" jsonschema:"optional code role filter"`
		Tier       string `json:"tier,omitempty" jsonschema:"optional evidence tier filter"`
		Freshness  string `json:"freshness,omitempty" jsonschema:"any, fresh, or stale"`
		Resolution string `json:"resolution,omitempty" jsonschema:"any, scip, syntax, or unresolved"`
		Ordering   string `json:"ordering,omitempty" jsonschema:"source or unit stable ordering"`
		PageSize   int    `json:"page_size,omitempty" jsonschema:"caller rows per page; default 50, maximum 100"`
		Cursor     string `json:"cursor,omitempty" jsonschema:"opaque continuation cursor returned by the preceding page"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "list_operation_callers",
		Description: "Page the exact declaration-proven Caller Map for one selected endpoint. Returns every " +
			"shared-service row verbatim with source citation, unit-attribution ambiguity, extractor abstention, " +
			"complete-generation state, exact-or-unavailable total state, applied filters, caveat, and " +
			"revision-bound opaque continuation cursor. " +
			"Static evidence does not establish runtime use, completeness, absence, or migration safety.",
		OutputSchema: callerMapPageOutputSchema(),
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in operationCallersIn,
	) (*sdk.CallToolResult, api.CallerMapPage, error) {
		result, err := opts.CallerMap.List(
			ctx,
			api.CallerMapQuery{
				Endpoint: api.CallerMapEndpoint{
					Protocol: in.Protocol, Repository: in.Repository,
					Lineage: in.Lineage, Operation: in.Operation,
				},
				Unit: in.Unit, Owner: in.Owner, PathPrefix: in.PathPrefix,
				CodeRole: in.CodeRole, Tier: in.Tier,
				Freshness: in.Freshness, Resolution: in.Resolution,
				Ordering: in.Ordering,
			},
			in.PageSize,
			in.Cursor,
		)
		if err != nil {
			return nil, api.CallerMapPage{}, err
		}
		return nil, *result, nil
	})

	citations, ok := opts.CallerMap.(callerCitationQueries)
	if !ok || !citations.CitationAvailable() {
		return
	}
	type callerCitationIn struct {
		Citation string `json:"citation" jsonschema:"opaque exact-range citation returned by list_operation_callers"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "read_operation_caller_citation",
		Description: "Read only the immutable byte range named by one repository-overlay Caller Map citation. " +
			"The shared service rechecks repository authorization, full caller generation identity, publication " +
			"revision, record position, commit/path object identity, and blob digest. It grants no tree, directory, " +
			"or unrelated source access.",
		OutputSchema: callerCitationOutputSchema(),
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		in callerCitationIn,
	) (*sdk.CallToolResult, api.CallerMapCitation, error) {
		result, err := citations.ReadCitation(ctx, in.Citation)
		if err != nil {
			return nil, api.CallerMapCitation{}, err
		}
		return nil, *result, nil
	})
}

func contractCatalogListOutputSchema() map[string]any {
	return topLevelOutputSchema(
		[]string{
			"schema_version", "query", "items", "pagination",
			"coverage_digest", "coverage", "caveat",
		},
		map[string]any{
			"schema_version":  map[string]any{"type": "string"},
			"query":           map[string]any{"type": "object"},
			"items":           objectArraySchema(),
			"pagination":      map[string]any{"type": "object"},
			"coverage_digest": map[string]any{"type": "string"},
			"coverage":        map[string]any{"type": "object"},
			"caveat":          map[string]any{"type": "string"},
		},
	)
}

func callerMapPageOutputSchema() map[string]any {
	return topLevelOutputSchema(
		[]string{
			"schema_version", "query", "rows", "pagination", "generation",
			"scope", "matching_rows_state", "caveat",
		},
		map[string]any{
			"schema_version":      map[string]any{"type": "string"},
			"query":               map[string]any{"type": "object"},
			"declaration":         map[string]any{"type": "object"},
			"rows":                objectArraySchema(),
			"groups":              objectArraySchema(),
			"total_matching_rows": map[string]any{"type": "integer"},
			"pagination":          map[string]any{"type": "object"},
			"generation":          callerMapGenerationOutputSchema(),
			"scope":               callerMapScopeOutputSchema(),
			"matching_rows_state": map[string]any{"type": "string"},
			"coverage_digest":     map[string]any{"type": "string"},
			"attribution_digest":  map[string]any{"type": "string"},
			"coverage":            map[string]any{"type": "object"},
			"caveat":              map[string]any{"type": "string"},
		},
	)
}

func callerMapGenerationOutputSchema() map[string]any {
	stringProperty := map[string]any{"type": "string"}
	integerProperty := map[string]any{"type": "integer"}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"state", "plane", "repository"},
		"properties": map[string]any{
			"state":                     stringProperty,
			"reason":                    stringProperty,
			"plane":                     stringProperty,
			"repository":                stringProperty,
			"commit":                    stringProperty,
			"unit_digest":               stringProperty,
			"generation_digest":         stringProperty,
			"declaration_set_digest":    stringProperty,
			"candidate_manifest_digest": stringProperty,
			"resolver_manifest_digest":  stringProperty,
			"pair_set_digest":           stringProperty,
			"manifest_digest":           stringProperty,
			"publication_revision":      integerProperty,
			"pair_count":                integerProperty,
			"result_count":              integerProperty,
			"abstention_count":          integerProperty,
			"canonical_bytes":           integerProperty,
			"excluded_go_test_records":  integerProperty,
			"record_counts":             callerMapRecordCountsOutputSchema(),
			"partition_progress":        callerMapPartitionProgressOutputSchema(),
		},
	}
}

func callerMapRecordCountsOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"candidate_records", "base_records", "excluded_go_test_records",
		},
		"properties": map[string]any{
			"candidate_records":        map[string]any{"type": "integer"},
			"base_records":             map[string]any{"type": "integer"},
			"excluded_go_test_records": map[string]any{"type": "integer"},
		},
	}
}

func callerMapPartitionProgressOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"state", "settled_pair_count", "succeeded_pair_count",
			"refused_pair_count",
		},
		"properties": map[string]any{
			"state":                map[string]any{"type": "string"},
			"settled_pair_count":   map[string]any{"type": "integer"},
			"succeeded_pair_count": map[string]any{"type": "integer"},
			"refused_pair_count":   map[string]any{"type": "integer"},
			"total_pair_count":     map[string]any{"type": "integer"},
			"refusals": map[string]any{
				"type": "array", "maxItems": 32,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{
						"stage", "generation_kind", "classification", "dimension",
						"observed", "limit", "outcome_count",
					},
					"properties": map[string]any{
						"stage":           map[string]any{"type": "string"},
						"generation_kind": map[string]any{"type": "string"},
						"classification":  map[string]any{"type": "string"},
						"dimension":       map[string]any{"type": "string"},
						"observed":        map[string]any{"type": "integer"},
						"limit":           map[string]any{"type": "integer"},
						"outcome_count":   map[string]any{"type": "integer"},
					},
				},
			},
		},
	}
}

func callerMapScopeOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"repository", "scope_posture"},
		"properties": map[string]any{
			"repository":    map[string]any{"type": "string"},
			"commit":        map[string]any{"type": "string"},
			"scope_posture": map[string]any{"type": "string"},
			"analysis_unit": callerMapAnalysisUnitOutputSchema(),
		},
	}
}

func callerMapAnalysisUnitOutputSchema() map[string]any {
	pathArray := func() map[string]any {
		return map[string]any{
			"type":  []string{"array", "null"},
			"items": map[string]any{"type": "string"},
		}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"schema", "name", "digest", "primary_paths", "supporting_paths",
			"primary_path_count", "supporting_path_count", "search_index_posture",
			"typed_index_posture",
		},
		"properties": map[string]any{
			"schema":                map[string]any{"type": "string"},
			"name":                  map[string]any{"type": "string"},
			"digest":                map[string]any{"type": "string"},
			"primary_paths":         pathArray(),
			"supporting_paths":      pathArray(),
			"primary_path_count":    map[string]any{"type": "integer"},
			"supporting_path_count": map[string]any{"type": "integer"},
			"search_index_posture":  map[string]any{"type": "string"},
			"typed_index_posture":   map[string]any{"type": "string"},
			"typed_index": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"kind", "path"},
				"properties": map[string]any{
					"kind": map[string]any{"type": "string"},
					"path": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func callerCitationOutputSchema() map[string]any {
	return topLevelOutputSchema(
		[]string{"schema_version", "generation", "source", "content"},
		map[string]any{
			"schema_version": map[string]any{"type": "string"},
			"generation":     map[string]any{"type": "object"},
			"source":         map[string]any{"type": "object"},
			"content":        map[string]any{"type": "string"},
		},
	)
}

func topLevelOutputSchema(
	required []string,
	properties map[string]any,
) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

func objectArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "object"},
	}
}

// ContractCatalogMessage is recursively nested, while the MCP SDK's default
// reflection schema builder rejects recursive Go types. This explicit schema
// pins the complete top-level v2 detail contract and permits the already
// bounded recursive request/response objects to describe their own nested
// messages without weakening the shared service's runtime validation.
func contractOperationOutputSchema() map[string]any {
	stringProperty := map[string]any{"type": "string"}
	objectProperty := map[string]any{"type": "object"}
	arrayProperty := objectArraySchema()
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"endpoint", "detail"},
		"properties": map[string]any{
			"endpoint": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"protocol", "repository",
					"declaration_lineage", "operation",
				},
				"properties": map[string]any{
					"protocol":            stringProperty,
					"repository":          stringProperty,
					"declaration_lineage": stringProperty,
					"operation":           stringProperty,
				},
			},
			"detail": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"schema_version", "repository", "declaration_lineage",
					"service_fqn", "method", "operation", "declaration",
					"fact_detail", "request", "response", "implementations",
					"callers", "unresolved_candidates",
					"relationships_truncated", "shape_truncated",
					"coverage_digest", "coverage", "caveat",
				},
				"properties": map[string]any{
					"schema_version":            stringProperty,
					"repository":                stringProperty,
					"declaration_lineage":       stringProperty,
					"service_fqn":               stringProperty,
					"method":                    stringProperty,
					"operation":                 stringProperty,
					"declaration":               objectProperty,
					"fact_detail":               objectProperty,
					"request":                   objectProperty,
					"response":                  objectProperty,
					"implementations":           arrayProperty,
					"callers":                   arrayProperty,
					"unresolved_candidates":     arrayProperty,
					"relationships_truncated":   map[string]any{"type": "boolean"},
					"relationship_limit_reason": stringProperty,
					"shape_truncated":           map[string]any{"type": "boolean"},
					"coverage_digest":           stringProperty,
					"coverage":                  objectProperty,
					"caveat":                    stringProperty,
				},
			},
		},
	}
}
