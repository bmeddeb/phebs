// Package api exposes the huma v2 HTTP API (T1.4).
package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/bmeddeb/phebs/internal/store"
)

type Options struct {
	Version string
	APIKey  string // empty = open API; serve logs the warning
	Store   store.Store
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

// openPath: liveness and API discovery stay unauthenticated.
func openPath(p string) bool {
	return p == "/api/health" || p == "/api/version" ||
		strings.HasPrefix(p, "/api/openapi") || p == "/api/docs"
}

func bearerOK(ctx huma.Context, key string) bool {
	tok, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(tok), []byte(key)) == 1
}
