package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

type RelationshipQueries interface {
	List(context.Context, api.RelationshipQuery, int, string) (*api.RelationshipPage, error)
	Compare(context.Context, api.RelationshipComparisonQuery, int, string) (*api.RelationshipComparisonPage, error)
	ReadCitation(context.Context, string) (*api.RelationshipCitation, error)
}

func registerRelationshipTools(server *sdk.Server, opts Options) {
	if opts.Relationships == nil {
		return
	}
	type listInput struct {
		Repositories []string `json:"repositories,omitempty" jsonschema:"exact visible repositories; omit to use the complete visible set, bounded at 32"`
		ServiceKey   string   `json:"service_key" jsonschema:"repository-local accepted service key"`
		View         string   `json:"view,omitempty" jsonschema:"all, dependencies, callers, or topics"`
		Kind         string   `json:"kind,omitempty" jsonschema:"rpc or kafka"`
		Plane        string   `json:"plane,omitempty" jsonschema:"grpc, thrift, producer, or consumer"`
		LookupKey    string   `json:"lookup_key,omitempty" jsonschema:"exact canonical operation or source-spelled topic"`
		PageSize     int      `json:"page_size,omitempty" jsonschema:"rows returned, default 50, cap 100"`
		Cursor       string   `json:"cursor,omitempty" jsonschema:"opaque continuation cursor returned by the preceding page"`
	}
	sdk.AddTool(server, &sdk.Tool{
		Name:        "list_service_relationships",
		Description: "Read permission-scoped exact service dependency, caller, and source-spelled Kafka topic rows. Returns explicit root authority, placement claims, coverage, gaps, truncation, and lease-pinned citations; it makes no runtime-topology or completeness claim.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, input listInput) (*sdk.CallToolResult, api.RelationshipPage, error) {
		page, err := opts.Relationships.List(ctx, api.RelationshipQuery{
			Repositories: input.Repositories, ServiceKey: input.ServiceKey,
			View: input.View, Kind: input.Kind, Plane: input.Plane, LookupKey: input.LookupKey,
		}, input.PageSize, input.Cursor)
		if err != nil {
			return nil, api.RelationshipPage{}, err
		}
		return nil, *page, nil
	})
	type compareInput struct {
		Repository       string `json:"repository" jsonschema:"exact visible repository"`
		ServiceKey       string `json:"service_key" jsonschema:"repository-local accepted service key"`
		BeforeGeneration string `json:"before_generation" jsonschema:"exact sha256 relationship generation from an earlier root receipt"`
		BeforeRootDigest string `json:"before_root_digest" jsonschema:"exact sha256 root digest paired with before_generation"`
		AfterGeneration  string `json:"after_generation" jsonschema:"exact sha256 relationship generation from a later root receipt"`
		AfterRootDigest  string `json:"after_root_digest" jsonschema:"exact sha256 root digest paired with after_generation"`
		View             string `json:"view,omitempty" jsonschema:"all, dependencies, callers, or topics"`
		Kind             string `json:"kind,omitempty" jsonschema:"rpc or kafka"`
		Plane            string `json:"plane,omitempty"`
		LookupKey        string `json:"lookup_key,omitempty"`
		PageSize         int    `json:"page_size,omitempty" jsonschema:"rows returned, default 50, cap 100"`
		Cursor           string `json:"cursor,omitempty" jsonschema:"opaque continuation cursor returned by the preceding page"`
	}
	sdk.AddTool(server, &sdk.Tool{
		Name:        "compare_service_relationships",
		Description: "Compare two exact lease-pinned relationship generations for one visible service. Added, removed, and unchanged rows remain occurrence-exact and carry both authorities; failed, empty, unavailable, and truncated states remain explicit.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, input compareInput) (*sdk.CallToolResult, api.RelationshipComparisonPage, error) {
		page, err := opts.Relationships.Compare(ctx, api.RelationshipComparisonQuery{
			Repository: input.Repository, ServiceKey: input.ServiceKey,
			BeforeGeneration: input.BeforeGeneration, BeforeRootDigest: input.BeforeRootDigest,
			AfterGeneration: input.AfterGeneration, AfterRootDigest: input.AfterRootDigest,
			View: input.View, Kind: input.Kind, Plane: input.Plane, LookupKey: input.LookupKey,
		}, input.PageSize, input.Cursor)
		if err != nil {
			return nil, api.RelationshipComparisonPage{}, err
		}
		return nil, *page, nil
	})
	type citationInput struct {
		Citation string `json:"citation" jsonschema:"opaque exact citation returned by list_service_relationships or compare_service_relationships"`
	}
	sdk.AddTool(server, &sdk.Tool{
		Name:        "read_service_relationship_citation",
		Description: "Read only the immutable source byte span named by a lease-pinned service relationship citation. The result repeats exact relationship, projection, posting, blob, and span authority.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, input citationInput) (*sdk.CallToolResult, api.RelationshipCitation, error) {
		citation, err := opts.Relationships.ReadCitation(ctx, input.Citation)
		if err != nil {
			return nil, api.RelationshipCitation{}, err
		}
		return nil, *citation, nil
	})
}
