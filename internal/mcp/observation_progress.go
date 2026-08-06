package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/observationpublication"
)

type ObservationProgressQueries interface {
	Read(context.Context, string) (*observationpublication.Progress, error)
}

func registerObservationProgressTool(s *sdk.Server, opts Options) {
	if opts.ObservationProgress == nil {
		return
	}
	type input struct {
		Repository string `json:"repository" jsonschema:"exact visible repository identity returned by list_repos"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_observation_progress",
		Description: "Read one visible repository's bounded source-observation state, " +
			"durable partition progress, and source-free input/read/reuse/unsupported receipt.",
	}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		input input,
	) (*sdk.CallToolResult, observationpublication.Progress, error) {
		progress, err := opts.ObservationProgress.Read(ctx, input.Repository)
		if err != nil {
			return nil, observationpublication.Progress{}, err
		}
		return nil, *progress, nil
	})
}
