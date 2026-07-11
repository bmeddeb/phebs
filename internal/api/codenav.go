package api

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/codenav"
)

// registerCodeNavigation exposes SCIP lookups at the immutable revision that
// produced the repository's current search index. Clients may provide a full
// object ID explicitly, but symbolic refs are deliberately rejected by the
// codenav service so results cannot drift between related requests.
func registerCodeNavigation(api huma.API, opts Options) {
	type positionIn struct {
		Repo      string                   `query:"repo" required:"true"`
		Ref       string                   `query:"ref" doc:"full commit object ID; defaults to the indexed commit"`
		Path      string                   `query:"path" required:"true"`
		Line      int32                    `query:"line" minimum:"0" doc:"zero-based source line"`
		Character int32                    `query:"character" minimum:"0" doc:"zero-based character offset"`
		Encoding  codenav.PositionEncoding `query:"encoding" enum:"utf8,utf16,utf32" default:"utf16"`
	}

	query := func(ctx context.Context, in *positionIn) (codenav.Query, error) {
		if opts.CodeNav == nil {
			return codenav.Query{}, huma.Error503ServiceUnavailable("code navigation unavailable")
		}
		ref, err := historyRef(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return codenav.Query{}, gitErr(err)
		}
		return codenav.Query{
			Repo: in.Repo, Revision: ref, Path: in.Path,
			Line: in.Line, Character: in.Character, Encoding: in.Encoding,
		}, nil
	}

	type definitionOut struct{ Body codenav.DefinitionResult }
	huma.Get(api, "/api/find_definitions", func(ctx context.Context, in *positionIn) (*definitionOut, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, err
		}
		result, err := opts.CodeNav.Definition(ctx, q)
		if err != nil {
			return nil, codeNavErr(err)
		}
		return &definitionOut{Body: result}, nil
	})

	type referencesOut struct{ Body codenav.ReferencesResult }
	huma.Get(api, "/api/find_references", func(ctx context.Context, in *positionIn) (*referencesOut, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, err
		}
		result, err := opts.CodeNav.References(ctx, q)
		if err != nil {
			return nil, codeNavErr(err)
		}
		return &referencesOut{Body: result}, nil
	})

	type hoverOut struct{ Body codenav.HoverResult }
	huma.Get(api, "/api/hover", func(ctx context.Context, in *positionIn) (*hoverOut, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, err
		}
		result, err := opts.CodeNav.Hover(ctx, q)
		if err != nil {
			return nil, codeNavErr(err)
		}
		return &hoverOut{Body: result}, nil
	})
}

func codeNavErr(err error) error {
	switch {
	case errors.Is(err, codenav.ErrRevisionNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, codenav.ErrInvalidInput), errors.Is(err, codenav.ErrUnsupportedEncoding):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, codenav.ErrIndexTooLarge), errors.Is(err, codenav.ErrSourceTooLarge),
		errors.Is(err, codenav.ErrQuerySourceBudget), errors.Is(err, codenav.ErrCacheBudget),
		errors.Is(err, codenav.ErrSemanticLimit), errors.Is(err, codenav.ErrHoverTooLarge):
		return huma.Error413RequestEntityTooLarge(err.Error())
	default:
		return huma.Error500InternalServerError("code navigation", err)
	}
}
