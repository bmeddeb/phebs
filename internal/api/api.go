// Package api exposes the huma v2 HTTP API (T1.4).
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
)

type Options struct {
	Version string
	APIKey  string // empty = open API; serve logs the warning
	Store   store.Store
	Search  *search.Searcher // nil = search endpoints answer 503
}

// New builds the /api/* handler: health, version, repos, plus the OpenAPI
// document at /api/openapi.json and docs UI at /api/docs.
func New(opts Options) http.Handler {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("phebs", opts.Version)
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = "/api/docs"
	api := humago.New(mux, cfg)

	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if opts.APIKey == "" || openPath(ctx.URL().Path) || bearerOK(ctx, opts.APIKey) {
			next(ctx)
			return
		}
		_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "invalid or missing bearer token")
	})

	type healthOut struct {
		Body struct {
			Status string `json:"status" example:"ok"`
		}
	}
	huma.Get(api, "/api/health", func(context.Context, *struct{}) (*healthOut, error) {
		out := &healthOut{}
		out.Body.Status = "ok"
		return out, nil
	})

	type versionOut struct {
		Body struct {
			Version string `json:"version" example:"0.1.0-dev"`
		}
	}
	huma.Get(api, "/api/version", func(context.Context, *struct{}) (*versionOut, error) {
		out := &versionOut{}
		out.Body.Version = opts.Version
		return out, nil
	})

	type reposOut struct {
		Body []store.Repo
	}
	huma.Get(api, "/api/repos", func(ctx context.Context, _ *struct{}) (*reposOut, error) {
		repos, err := opts.Store.ListRepos(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list repos", err)
		}
		return &reposOut{Body: repos}, nil
	})

	type searchIn struct {
		Q            string `query:"q" required:"true" example:"phebsNeedle repo:foo lang:go"`
		MaxMatches   int    `query:"max_matches" doc:"documents shown, default 50, cap 500"`
		ContextLines int    `query:"context_lines" doc:"context lines per match, cap 10"`
	}
	type searchOut struct {
		Body *search.Result
	}
	huma.Get(api, "/api/search", func(ctx context.Context, in *searchIn) (*searchOut, error) {
		if opts.Search == nil {
			return nil, huma.Error503ServiceUnavailable("search unavailable")
		}
		res, err := opts.Search.Search(ctx, in.Q,
			search.Options{MaxMatches: in.MaxMatches, ContextLines: in.ContextLines})
		if err != nil {
			if strings.Contains(err.Error(), "parse query") {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, huma.Error500InternalServerError("search", err)
		}
		return &searchOut{Body: res}, nil
	})

	type reindexIn struct {
		Body struct {
			Repo  string `json:"repo" required:"true" example:"github.com/foo/bar"`
			Force bool   `json:"force,omitempty" doc:"rebuild even when HEAD is already indexed"`
		}
	}
	type reindexOut struct {
		Body struct {
			Enqueued bool `json:"enqueued"`
		}
	}
	huma.Post(api, "/api/reindex", func(ctx context.Context, in *reindexIn) (*reindexOut, error) {
		if in.Body.Force {
			// clearing the recorded commit defeats the T3.2 short-circuit
			if err := opts.Store.ClearRepoIndexState(ctx, in.Body.Repo); err != nil {
				return nil, reindexErr(in.Body.Repo, err)
			}
		} else if _, err := opts.Store.GetRepo(ctx, in.Body.Repo); err != nil {
			return nil, reindexErr(in.Body.Repo, err)
		}
		if err := store.EnqueueUnlessInFlight(ctx, opts.Store, store.JobIndex, in.Body.Repo); err != nil {
			return nil, huma.Error500InternalServerError("enqueue", err)
		}
		out := &reindexOut{}
		out.Body.Enqueued = true
		return out, nil
	})

	type repoStatusOut struct {
		Body []store.RepoStatus
	}
	huma.Get(api, "/api/repo-status", func(ctx context.Context, _ *struct{}) (*repoStatusOut, error) {
		statuses, err := opts.Store.RepoStatuses(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("repo statuses", err)
		}
		return &repoStatusOut{Body: statuses}, nil
	})

	return mux
}

func reindexErr(repo string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return huma.Error404NotFound("unknown repo " + repo)
	}
	return huma.Error500InternalServerError("reindex", err)
}

// openPath: liveness and API discovery stay unauthenticated.
func openPath(p string) bool {
	return p == "/api/health" || p == "/api/version" ||
		strings.HasPrefix(p, "/api/openapi") || p == "/api/docs"
}

func bearerOK(ctx huma.Context, key string) bool {
	tok, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(tok), []byte(key)) == 1
}
