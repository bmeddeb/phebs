package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	workbenchEvidenceQueryBytes   = 64 << 10
	workbenchDispositionBodyBytes = 512 << 10
)

// WorkbenchImpactReader, WorkbenchImplementationReader, and
// WorkbenchChecklistReader are the narrow conditional HTTP projection. The
// concrete shared services satisfy these interfaces; tests and embedders can
// prove registration without recreating their evidence semantics.
type WorkbenchImpactReader interface {
	Read(
		context.Context,
		string,
		WorkbenchImpactRequest,
	) (*WorkbenchImpactPage, error)
}

type WorkbenchImplementationReader interface {
	Read(
		context.Context,
		string,
		WorkbenchImplementationRequest,
	) (*WorkbenchImplementationPage, error)
}

type WorkbenchChecklistReader interface {
	Read(
		context.Context,
		string,
		WorkbenchChecklistRequest,
	) (*WorkbenchChecklistPage, error)
	RecordDisposition(
		context.Context,
		string,
		WorkbenchDispositionMutation,
	) (*store.WorkbenchDisposition, error)
}

type workbenchImpactPageInput struct {
	InvestigationID  string `path:"investigation_id"`
	RevisionID       string `path:"revision_id"`
	CompatibilityRun string `query:"compatibility_run_id" maxLength:"128"`
	Filters          string `query:"filters" maxLength:"65536"`
	PageSize         int    `query:"page_size" minimum:"0" maximum:"100"`
	Cursor           string `query:"cursor" maxLength:"65536"`
}

type workbenchImplementationPageInput struct {
	InvestigationID string `path:"investigation_id"`
	RevisionID      string `path:"revision_id"`
	Anchors         string `query:"anchors" maxLength:"65536"`
	PageSize        int    `query:"page_size" minimum:"0" maximum:"100"`
	Cursor          string `query:"cursor" maxLength:"65536"`
}

type workbenchChecklistPageInput struct {
	InvestigationID string `path:"investigation_id"`
	RevisionID      string `path:"revision_id"`
	Evidence        string `query:"evidence" maxLength:"65536"`
	PageSize        int    `query:"page_size" minimum:"0" maximum:"100"`
	Cursor          string `query:"cursor" maxLength:"65536"`
}

type workbenchImpactPageOutput struct {
	Body WorkbenchImpactPage
}

type workbenchImplementationPageOutput struct {
	Body WorkbenchImplementationPage
}

type workbenchChecklistPageOutput struct {
	Body WorkbenchChecklistPage
}

type workbenchDispositionInput struct {
	InvestigationID string `path:"investigation_id"`
	RevisionID      string `path:"revision_id"`
	Body            WorkbenchDispositionMutation
}

type workbenchDispositionOutput struct {
	Body store.WorkbenchDisposition
}

func decodeWorkbenchEvidenceQuery[T any](
	name string,
	value string,
	target *T,
) error {
	if value == "" {
		return nil
	}
	if len(value) > workbenchEvidenceQueryBytes {
		return huma.Error400BadRequest(
			fmt.Sprintf("%s exceeds %d bytes", name, workbenchEvidenceQueryBytes),
		)
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return huma.Error400BadRequest(
			fmt.Sprintf("invalid %s: %v", name, err),
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return huma.Error400BadRequest(name + " has trailing JSON")
		}
		return huma.Error400BadRequest(
			fmt.Sprintf("invalid %s: %v", name, err),
		)
	}
	return nil
}

func workbenchEvidenceError(action string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return huma.Error404NotFound("workbench resource not found")
	}
	var statusError huma.StatusError
	if errors.As(err, &statusError) {
		return err
	}
	return huma.Error500InternalServerError(action, err)
}

func registerWorkbenchEvidence(api huma.API, opts Options) {
	if opts.Workbench == nil ||
		opts.WorkbenchImpact == nil ||
		opts.WorkbenchImplementation == nil ||
		opts.WorkbenchChecklist == nil {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID: "read-workbench-impact",
		Method:      http.MethodGet,
		Path:        "/api/workbenches/{investigation_id}/revisions/{revision_id}/impact",
		Summary:     "Read one exact Workbench impact-inventory page",
	}, func(
		ctx context.Context,
		input *workbenchImpactPageInput,
	) (*workbenchImpactPageOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		var filters WorkbenchImpactFilters
		if err := decodeWorkbenchEvidenceQuery(
			"filters",
			input.Filters,
			&filters,
		); err != nil {
			return nil, err
		}
		value, err := opts.WorkbenchImpact.Read(
			ctx,
			principal,
			WorkbenchImpactRequest{
				InvestigationID:  input.InvestigationID,
				RevisionID:       input.RevisionID,
				CompatibilityRun: input.CompatibilityRun,
				Filters:          filters,
				PageSize:         input.PageSize,
				Cursor:           input.Cursor,
			},
		)
		if err != nil {
			return nil, workbenchEvidenceError(
				"read workbench impact",
				err,
			)
		}
		return &workbenchImpactPageOutput{Body: *value}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "read-workbench-implementation",
		Method:      http.MethodGet,
		Path:        "/api/workbenches/{investigation_id}/revisions/{revision_id}/implementation",
		Summary:     "Read one exact Workbench implementation-evidence page",
	}, func(
		ctx context.Context,
		input *workbenchImplementationPageInput,
	) (*workbenchImplementationPageOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		anchors := []WorkbenchImplementationAnchor{}
		if err := decodeWorkbenchEvidenceQuery(
			"anchors",
			input.Anchors,
			&anchors,
		); err != nil {
			return nil, err
		}
		value, err := opts.WorkbenchImplementation.Read(
			ctx,
			principal,
			WorkbenchImplementationRequest{
				InvestigationID: input.InvestigationID,
				RevisionID:      input.RevisionID,
				Anchors:         anchors,
				PageSize:        input.PageSize,
				Cursor:          input.Cursor,
			},
		)
		if err != nil {
			return nil, workbenchEvidenceError(
				"read workbench implementation",
				err,
			)
		}
		return &workbenchImplementationPageOutput{Body: *value}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "read-workbench-checklist",
		Method:      http.MethodGet,
		Path:        "/api/workbenches/{investigation_id}/revisions/{revision_id}/checklist",
		Summary:     "Read one exact disposition-backed checklist page",
	}, func(
		ctx context.Context,
		input *workbenchChecklistPageInput,
	) (*workbenchChecklistPageOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		var evidence WorkbenchChecklistEvidenceInput
		if err := decodeWorkbenchEvidenceQuery(
			"evidence",
			input.Evidence,
			&evidence,
		); err != nil {
			return nil, err
		}
		value, err := opts.WorkbenchChecklist.Read(
			ctx,
			principal,
			WorkbenchChecklistRequest{
				InvestigationID: input.InvestigationID,
				RevisionID:      input.RevisionID,
				Evidence:        evidence,
				PageSize:        input.PageSize,
				Cursor:          input.Cursor,
			},
		)
		if err != nil {
			return nil, workbenchEvidenceError(
				"read workbench checklist",
				err,
			)
		}
		return &workbenchChecklistPageOutput{Body: *value}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:  "record-workbench-disposition",
		Method:       http.MethodPost,
		Path:         "/api/workbenches/{investigation_id}/revisions/{revision_id}/dispositions",
		Summary:      "Record one explicit Workbench suggestion disposition",
		MaxBodyBytes: workbenchDispositionBodyBytes,
	}, func(
		ctx context.Context,
		input *workbenchDispositionInput,
	) (*workbenchDispositionOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		if input.Body.InvestigationID != input.InvestigationID ||
			input.Body.ExpectedRevisionID != input.RevisionID {
			return nil, huma.Error400BadRequest(
				"path investigation and revision must match the mutation",
			)
		}
		setAuditTarget(
			ctx,
			input.InvestigationID+"/"+input.RevisionID,
		)
		value, err := opts.WorkbenchChecklist.RecordDisposition(
			ctx,
			principal,
			input.Body,
		)
		if err != nil {
			return nil, workbenchEvidenceError(
				"record workbench disposition",
				err,
			)
		}
		return &workbenchDispositionOutput{Body: *value}, nil
	})
}
