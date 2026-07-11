package api

import (
	"context"
	"errors"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type optionalIntParam struct {
	Value int
	IsSet bool
}

func (o optionalIntParam) Schema(registry huma.Registry) *huma.Schema {
	return huma.SchemaFromType(registry, reflect.TypeOf(o.Value))
}

func (o *optionalIntParam) Receiver() reflect.Value {
	return reflect.ValueOf(o).Elem().FieldByName("Value")
}

func (o *optionalIntParam) OnParamSet(isSet bool, _ any) {
	o.IsSet = isSet
}

func registerHistory(api huma.API, opts Options) {
	service := phebssync.NewHistoryService(opts.DataDir)

	type blameIn struct {
		Repo string `query:"repo" required:"true"`
		Ref  string `query:"ref" doc:"commit-ish; defaults to the indexed commit"`
		Path string `query:"path" required:"true"`
	}
	type blameOut struct{ Body phebssync.BlameResult }
	huma.Get(api, "/api/blame", func(ctx context.Context, in *blameIn) (*blameOut, error) {
		ref, err := historyRef(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, gitErr(err)
		}
		result, err := service.Blame(ctx, phebssync.BlameRequest{Repo: in.Repo, Ref: ref, Path: in.Path})
		if err != nil {
			return nil, historyErr(err)
		}
		return &blameOut{Body: result}, nil
	})

	type commitsIn struct {
		Repo   string `query:"repo" required:"true"`
		Ref    string `query:"ref" doc:"commit-ish; defaults to the indexed commit"`
		Path   string `query:"path" doc:"optional file path; follows renames"`
		Limit  int    `query:"limit" minimum:"0" maximum:"200"`
		Offset int    `query:"offset" minimum:"0"`
	}
	type commitsOut struct{ Body phebssync.CommitListResult }
	huma.Get(api, "/api/commits", func(ctx context.Context, in *commitsIn) (*commitsOut, error) {
		ref, err := historyRef(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, gitErr(err)
		}
		result, err := service.Commits(ctx, phebssync.CommitListRequest{
			Repo: in.Repo, Ref: ref, Path: in.Path, Limit: in.Limit, Offset: in.Offset,
		})
		if err != nil {
			return nil, historyErr(err)
		}
		return &commitsOut{Body: result}, nil
	})

	type commitIn struct {
		Repo string `query:"repo" required:"true"`
		Ref  string `query:"ref" doc:"commit-ish; defaults to the indexed commit"`
	}
	type commitOut struct{ Body phebssync.CommitResult }
	huma.Get(api, "/api/commit", func(ctx context.Context, in *commitIn) (*commitOut, error) {
		ref, err := historyRef(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, gitErr(err)
		}
		result, err := service.Commit(ctx, phebssync.CommitRequest{Repo: in.Repo, Ref: ref})
		if err != nil {
			return nil, historyErr(err)
		}
		return &commitOut{Body: result}, nil
	})

	type diffIn struct {
		Repo         string           `query:"repo" required:"true"`
		Base         string           `query:"base" doc:"base commit; defaults to head's first parent"`
		Head         string           `query:"head" doc:"head commit; defaults to the indexed commit"`
		Path         string           `query:"path" doc:"optional path filter"`
		ContextLines optionalIntParam `query:"context_lines" minimum:"0" maximum:"20"`
	}
	type diffOut struct{ Body phebssync.DiffResult }
	huma.Get(api, "/api/diff", func(ctx context.Context, in *diffIn) (*diffOut, error) {
		head, err := historyRef(ctx, opts, in.Repo, in.Head)
		if err != nil {
			return nil, gitErr(err)
		}
		request := phebssync.DiffRequest{Repo: in.Repo, Base: in.Base, Head: head, Path: in.Path}
		if in.ContextLines.IsSet {
			request.ContextLines = in.ContextLines.Value
			request.ContextLinesSet = true
		}
		result, err := service.Diff(ctx, request)
		if err != nil {
			return nil, historyErr(err)
		}
		return &diffOut{Body: result}, nil
	})
}

func historyRef(ctx context.Context, opts Options, repoName, requested string) (string, error) {
	repo, err := opts.Store.GetRepo(ctx, repoName)
	if err != nil {
		return "", err
	}
	if repo.Deleting {
		return "", store.ErrNotFound
	}
	// T10.3: permission denials are indistinguishable from missing repos.
	if allow := repoFilter(ctx, opts); allow != nil && !allow(*repo) {
		return "", store.ErrNotFound
	}
	if requested != "" {
		return requested, nil
	}
	if repo.IndexedCommitHash == "" {
		return "", store.ErrNotFound
	}
	return repo.IndexedCommitHash, nil
}

func historyErr(err error) error {
	if errors.Is(err, phebssync.ErrBinaryFile) {
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return gitErr(err)
}
