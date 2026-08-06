package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

// ServiceDirectoryQueries is the complete transport-neutral T33.4 boundary.
// MCP forwards shared inputs and outputs without rebuilding authorization,
// cursors, membership projections, or response bounds.
type ServiceDirectoryQueries interface {
	List(
		context.Context,
		api.ServiceInventoryQuery,
		int,
		string,
	) (*api.ServiceInventory, error)
	Detail(context.Context, string, string) (*api.ServiceDetail, error)
}

func registerServiceDirectoryTools(s *sdk.Server, opts Options) {
	if opts.ServiceDirectory == nil {
		return
	}
	type listInput struct {
		Repository     string `json:"repository" jsonschema:"exact visible repository identity"`
		Status         string `json:"status,omitempty" jsonschema:"optional lifecycle status: current, stale, unavailable, conflict, or removed"`
		Disposition    string `json:"disposition,omitempty" jsonschema:"optional catalog disposition: accepted, proposal, conflict, or rejected"`
		IncludeRemoved bool   `json:"include_removed,omitempty" jsonschema:"include retained removed tombstones"`
		PageSize       int    `json:"page_size,omitempty" jsonschema:"bounded services per page; default 50, maximum 100"`
		Cursor         string `json:"cursor,omitempty" jsonschema:"opaque continuation cursor returned by the preceding page"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "list_services",
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: workbenchBool(false),
		},
		Description: "List one visible repository's exact catalog-backed service lifecycle rows. " +
			"Returns the same bounded page and cursor as HTTP; removed rows are opt-in. " +
			"The result is evidence, not task, Decision, completeness, or safety authority.",
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		input listInput,
	) (*sdk.CallToolResult, api.ServiceInventory, error) {
		result, err := opts.ServiceDirectory.List(ctx, api.ServiceInventoryQuery{
			Repository: input.Repository, Status: input.Status,
			Disposition: input.Disposition, IncludeRemoved: input.IncludeRemoved,
		}, input.PageSize, input.Cursor)
		if err != nil {
			return nil, api.ServiceInventory{}, err
		}
		return nil, *result, nil
	})

	type detailInput struct {
		Repository string `json:"repository" jsonschema:"exact visible repository identity returned by list_repos"`
		ServiceKey string `json:"service_key" jsonschema:"exact repository-local service key returned by list_services"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_service",
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: workbenchBool(false),
		},
		Description: "Read one exact visible service with its lifecycle identities, " +
			"successors, and bounded catalog membership paths. The result is evidence, " +
			"not task, Decision, completeness, or safety authority.",
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		input detailInput,
	) (*sdk.CallToolResult, api.ServiceDetail, error) {
		result, err := opts.ServiceDirectory.Detail(
			ctx, input.Repository, input.ServiceKey,
		)
		if err != nil {
			return nil, api.ServiceDetail{}, err
		}
		return nil, *result, nil
	})
}
