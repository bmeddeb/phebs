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

type getChangeWorkbenchInput struct {
	InvestigationID string `json:"investigation_id" jsonschema:"exact Investigation id returned by create_change_workbench or an authorized prior read"`
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
