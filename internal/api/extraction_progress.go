package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	ExtractionProgressPath            = "/api/extraction-progress"
	ExtractionProgressResponseLimit   = 4 << 10
	ExtractionProgressDetailStale     = "extraction progress changed; retry"
	ExtractionProgressDetailRead      = "read extraction progress"
	ExtractionProgressDetailInvalid   = "invalid extraction progress"
	ExtractionProgressDetailAuthority = "extraction authority changed while building the response; retry"
)

type ExtractionProgressReader interface {
	Progress(context.Context, string) (extractionpublication.Progress, error)
}

type ExtractionProgressService struct {
	opts   Options
	reader ExtractionProgressReader
}

func NewExtractionProgressService(opts Options, reader ExtractionProgressReader) *ExtractionProgressService {
	if opts.Store == nil || reader == nil {
		return nil
	}
	return &ExtractionProgressService{opts: opts, reader: reader}
}

func (service *ExtractionProgressService) Read(
	ctx context.Context, repositoryName string,
) (*extractionpublication.Progress, error) {
	if service == nil || service.reader == nil || reponame.Validate(repositoryName) != nil {
		return nil, huma.Error404NotFound("extraction progress not found")
	}
	repository, authorization, err := service.authorize(ctx, repositoryName)
	if err != nil {
		return nil, err
	}
	progress, err := service.reader.Progress(ctx, repository.Name)
	if errors.Is(err, extractionpublication.ErrStale) {
		return nil, huma.Error409Conflict(ExtractionProgressDetailStale)
	}
	if err != nil {
		return nil, huma.Error500InternalServerError(ExtractionProgressDetailRead, err)
	}
	if extractionpublication.ValidateProgress(progress) != nil {
		return nil, huma.Error500InternalServerError(ExtractionProgressDetailInvalid)
	}
	confirmed, confirmedAuthorization, err := service.authorize(ctx, repository.Name)
	if err != nil {
		return nil, err
	}
	if confirmedAuthorization != authorization ||
		confirmed.IndexedCommitHash != repository.IndexedCommitHash ||
		confirmed.EvidenceRevision != repository.EvidenceRevision ||
		!equalOptionalTime(confirmed.IndexedAt, repository.IndexedAt) {
		return nil, huma.Error409Conflict(ExtractionProgressDetailAuthority)
	}
	encoded, err := json.Marshal(progress)
	if err != nil || len(encoded) > ExtractionProgressResponseLimit {
		return nil, huma.Error500InternalServerError(ExtractionProgressDetailInvalid, err)
	}
	return &progress, nil
}

func (service *ExtractionProgressService) authorize(
	ctx context.Context, repositoryName string,
) (store.Repo, VisibilityContext, error) {
	allow := repoFilter(ctx, service.opts)
	if reponame.Validate(repositoryName) != nil || service.opts.Store == nil {
		return store.Repo{}, VisibilityContext{}, huma.Error404NotFound("extraction progress not found")
	}
	repository, err := service.opts.Store.GetRepo(ctx, repositoryName)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Repo{}, VisibilityContext{}, huma.Error500InternalServerError(
			ExtractionProgressDetailRead, err,
		)
	}
	if err == nil && repository == nil {
		return store.Repo{}, VisibilityContext{}, huma.Error500InternalServerError(
			ExtractionProgressDetailRead, errors.New("repository lookup returned nil"),
		)
	}
	if err != nil || repository.Deleting || repository.IndexedCommitHash == "" ||
		allow != nil && !allow(*repository) {
		return store.Repo{}, VisibilityContext{}, huma.Error404NotFound("extraction progress not found")
	}
	return *repository,
		catalogVisibilityContext(ctx, service.opts, []store.Repo{*repository}), nil
}

func registerExtractionProgressAPI(api huma.API, opts Options) {
	if opts.ExtractionProgress == nil {
		return
	}
	type input struct {
		Repository string `query:"repository" required:"true"`
	}
	type output struct {
		Body extractionpublication.Progress
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-extraction-progress", Method: http.MethodGet,
		Path: ExtractionProgressPath, Summary: "Read one authorized repository's extraction progress",
	}, func(ctx context.Context, input *input) (*output, error) {
		progress, err := opts.ExtractionProgress.Read(ctx, input.Repository)
		if err != nil {
			return nil, err
		}
		return &output{Body: *progress}, nil
	})
}
