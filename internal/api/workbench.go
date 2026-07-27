package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
)

const maxWorkbenchRequestBytes = 72 << 20

type workbenchPreviewInput struct {
	Body store.WorkbenchPlan
}

type workbenchPreviewOutput struct {
	Body store.WorkbenchPreview
}

type workbenchMutationInput struct {
	Body store.WorkbenchMutationRequest
}

type workbenchMutationOutput struct {
	Body store.WorkbenchView
}

type workbenchRevisionInput struct {
	InvestigationID string `path:"investigation_id"`
	Body            store.WorkbenchMutationRequest
}

type workbenchReadInput struct {
	InvestigationID string `path:"investigation_id"`
}

type workbenchReadOutput struct {
	Body store.WorkbenchView
}

func workbenchError(action string, err error) error {
	switch {
	case errors.Is(err, store.ErrInvalidWorkbench):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("workbench resource not found")
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict(err.Error())
	default:
		return huma.Error500InternalServerError(action, err)
	}
}

func registerWorkbench(api huma.API, opts Options) {
	if opts.Workbench == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID:  "preview-change-workbench",
		Method:       http.MethodPost,
		Path:         "/api/workbench_previews",
		Summary:      "Preview an authorized Change Workbench revision",
		MaxBodyBytes: maxWorkbenchRequestBytes,
	}, func(
		ctx context.Context,
		input *workbenchPreviewInput,
	) (*workbenchPreviewOutput, error) {
		if err := requireInvestigationMutationCredential(ctx, opts); err != nil {
			return nil, err
		}
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		preview, err := opts.Workbench.Preview(ctx, principal, input.Body)
		if err != nil {
			return nil, workbenchError("preview change workbench", err)
		}
		return &workbenchPreviewOutput{Body: *preview}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-change-workbench",
		Method:        http.MethodPost,
		Path:          "/api/workbenches",
		Summary:       "Create an idempotent Change Workbench",
		MaxBodyBytes:  maxWorkbenchRequestBytes,
		DefaultStatus: http.StatusCreated,
	}, func(
		ctx context.Context,
		input *workbenchMutationInput,
	) (*workbenchMutationOutput, error) {
		if err := requireInvestigationMutationCredential(ctx, opts); err != nil {
			return nil, err
		}
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		value, err := opts.Workbench.Create(ctx, principal, input.Body)
		if err != nil {
			return nil, workbenchError("create change workbench", err)
		}
		return &workbenchMutationOutput{Body: *value}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:  "revise-change-workbench",
		Method:       http.MethodPost,
		Path:         "/api/workbenches/{investigation_id}/revisions",
		Summary:      "Append an idempotent Change Workbench revision",
		MaxBodyBytes: maxWorkbenchRequestBytes,
	}, func(
		ctx context.Context,
		input *workbenchRevisionInput,
	) (*workbenchMutationOutput, error) {
		if err := requireInvestigationMutationCredential(ctx, opts); err != nil {
			return nil, err
		}
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		if input.Body.Plan.InvestigationID != input.InvestigationID {
			return nil, huma.Error400BadRequest(
				"path investigation_id must match the plan",
			)
		}
		value, err := opts.Workbench.Revise(ctx, principal, input.Body)
		if err != nil {
			return nil, workbenchError("revise change workbench", err)
		}
		return &workbenchMutationOutput{Body: *value}, nil
	})

	huma.Get(api, "/api/workbenches/{investigation_id}", func(
		ctx context.Context,
		input *workbenchReadInput,
	) (*workbenchReadOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		value, err := opts.Workbench.Read(
			ctx,
			principal,
			input.InvestigationID,
		)
		if err != nil {
			return nil, workbenchError("read change workbench", err)
		}
		return &workbenchReadOutput{Body: *value}, nil
	})
}
