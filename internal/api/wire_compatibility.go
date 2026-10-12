package api

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/compat"
)

// CheckWireCompatibility returns only the pinned Buf WIRE verdict over the
// request's own source bytes (T53.2). It reads no repository, store, proof
// bundle or field-reference evidence, so unreleased field evidence can never
// reach a caller through it, and the verdict is bound to the before/after
// content digests it reports.
func CheckWireCompatibility(ctx context.Context, checker compat.Service, request compat.Request) (*compat.CompatibilityResult, error) {
	if checker == nil {
		return nil, huma.Error503ServiceUnavailable("wire compatibility unavailable")
	}
	result, err := checker.Check(ctx, request)
	if err != nil {
		return nil, compatibilityCheckError(err)
	}
	return result, nil
}

// compatibilityCheckError maps checker refusals to the shared HTTP statuses,
// so the proof-enriched and standalone surfaces refuse identically.
func compatibilityCheckError(err error) error {
	switch {
	case errors.Is(err, compat.ErrInvalidInput):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, compat.ErrLimit):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, compat.ErrUnavailable):
		return huma.Error503ServiceUnavailable(err.Error())
	default:
		return huma.Error422UnprocessableEntity("compatibility engine could not produce a verdict", err)
	}
}

func registerWireCompatibilityAPI(api huma.API, opts Options) {
	if opts.WireCompatibility == nil {
		return
	}
	type wireIn struct {
		Body compat.Request
	}
	type wireOut struct {
		Body compat.CompatibilityResult
	}
	huma.Register(api, huma.Operation{
		OperationID: "check-wire-compatibility",
		Method:      "POST", Path: "/api/check_wire_compatibility",
		Summary:      "Check protobuf wire compatibility with the pinned Buf WIRE policy",
		MaxBodyBytes: compatibilityBodyLimit,
	}, func(ctx context.Context, in *wireIn) (*wireOut, error) {
		result, err := CheckWireCompatibility(ctx, opts.WireCompatibility, in.Body)
		if err != nil {
			return nil, err
		}
		return &wireOut{Body: *result}, nil
	})
}
