package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	ObservationProgressPath          = "/api/observation-progress"
	ObservationProgressResponseLimit = 64 << 10

	ObservationProgressDetailStale         = "observation progress changed; retry"
	ObservationProgressDetailControlAbsent = "observation progress unavailable"
	ObservationProgressDetailStore         = "read observation progress"
	ObservationProgressDetailProjection    = "invalid observation progress"
	ObservationProgressDetailEncode        = "encode observation progress"
	ObservationProgressDetailBound         = "observation progress exceeds response bound"
	ObservationProgressDetailAuthority     = "observation authority changed while building the response; retry"
	ObservationProgressDetailAuthorize     = "authorize observation progress"
)

type ObservationProgressReader interface {
	Read(context.Context, string) (observationpublication.Progress, error)
}

// ObservationProgressService is the authorization, snapshot-fence, and
// response-bound implementation shared by HTTP and MCP.
type ObservationProgressService struct {
	opts   Options
	reader ObservationProgressReader
}

func NewObservationProgressService(
	opts Options, reader ObservationProgressReader,
) *ObservationProgressService {
	if opts.Store == nil || reader == nil {
		return nil
	}
	return &ObservationProgressService{opts: opts, reader: reader}
}

func (service *ObservationProgressService) Read(
	ctx context.Context, repositoryName string,
) (*observationpublication.Progress, error) {
	if service == nil || service.reader == nil {
		return nil, huma.Error503ServiceUnavailable("observation progress unavailable")
	}
	repository, authorization, err := service.authorize(ctx, repositoryName)
	if err != nil {
		return nil, err
	}
	progress, err := service.reader.Read(ctx, repository.Name)
	if err != nil {
		return nil, observationProgressError(err)
	}
	if err := observationpublication.ValidateProgress(progress); err != nil {
		return nil, huma.Error500InternalServerError(ObservationProgressDetailProjection, err)
	}
	if progress.Repository != repository.Name {
		return nil, huma.Error500InternalServerError(
			ObservationProgressDetailProjection, errors.New("repository projection mismatch"),
		)
	}
	confirmed, confirmedAuthorization, err := service.authorize(ctx, repository.Name)
	if err != nil {
		return nil, err
	}
	if confirmedAuthorization != authorization ||
		confirmed.IndexedCommitHash != repository.IndexedCommitHash ||
		confirmed.EvidenceRevision != repository.EvidenceRevision ||
		!equalOptionalTime(confirmed.IndexedAt, repository.IndexedAt) {
		return nil, huma.Error409Conflict(ObservationProgressDetailAuthority)
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		return nil, huma.Error500InternalServerError(ObservationProgressDetailEncode, err)
	}
	if len(encoded) > ObservationProgressResponseLimit {
		return nil, huma.Error500InternalServerError(
			ObservationProgressDetailBound, errors.New("encoded response is too large"),
		)
	}
	return &progress, nil
}

func (service *ObservationProgressService) authorize(
	ctx context.Context, repositoryName string,
) (store.Repo, VisibilityContext, error) {
	allow := repoFilter(ctx, service.opts)
	if reponame.Validate(repositoryName) != nil || service.opts.Store == nil {
		return store.Repo{}, VisibilityContext{}, huma.Error404NotFound("observation progress not found")
	}
	repository, err := service.opts.Store.GetRepo(ctx, repositoryName)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Repo{}, VisibilityContext{}, huma.Error500InternalServerError(
			ObservationProgressDetailAuthorize, err,
		)
	}
	if err == nil && repository == nil {
		return store.Repo{}, VisibilityContext{}, huma.Error500InternalServerError(
			ObservationProgressDetailAuthorize, errors.New("repository lookup returned nil"),
		)
	}
	if err != nil || repository.Deleting || repository.IndexedCommitHash == "" ||
		allow != nil && !allow(*repository) {
		return store.Repo{}, VisibilityContext{}, huma.Error404NotFound("observation progress not found")
	}
	return *repository,
		catalogVisibilityContext(ctx, service.opts, []store.Repo{*repository}), nil
}

func observationProgressError(err error) error {
	switch {
	case errors.Is(err, observationpublication.ErrStale):
		return huma.Error409Conflict(ObservationProgressDetailStale)
	case errors.Is(err, os.ErrNotExist), errors.Is(err, store.ErrNotFound):
		return huma.Error409Conflict(ObservationProgressDetailControlAbsent)
	case errors.Is(err, observationpublication.ErrInvalid):
		return huma.Error500InternalServerError(ObservationProgressDetailProjection, err)
	default:
		return huma.Error500InternalServerError(ObservationProgressDetailStore, err)
	}
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func registerObservationProgressAPI(api huma.API, opts Options) {
	service := opts.ObservationProgress
	if service == nil {
		return
	}
	type input struct {
		Repository string `query:"repository" required:"true"`
	}
	type output struct {
		Body observationpublication.Progress
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-observation-progress", Method: http.MethodGet,
		Path:    ObservationProgressPath,
		Summary: "Read one authorized repository's source-observation progress",
	}, func(ctx context.Context, input *input) (*output, error) {
		progress, err := service.Read(ctx, input.Repository)
		if err != nil {
			return nil, err
		}
		return &output{Body: *progress}, nil
	})
}
