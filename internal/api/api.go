// Package api exposes the huma v2 HTTP API (T1.4).
package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type Options struct {
	Version string
	APIKey  string // empty = open API; serve logs the warning
	Store   store.Store
	Search  *search.Searcher // nil = search endpoints answer 503
	CodeNav *codenav.Service // nil = precise code navigation answers 503
	DataDir string           // bare mirrors for file serving (T4.4)
	// IsAdmin is set by serve to gate operational mutations. Nil preserves the
	// standalone handler's test and embedding behavior.
	IsAdmin func(context.Context) bool

	// T10.1 audit log. AuditRecord is called for every mutating huma operation
	// (serve resolves the actor from the request context); nil disables
	// recording. AuditLog serves GET /api/audit; nil answers 503.
	AuditRecord func(ctx context.Context, event store.AuditEvent)
	AuditLog    store.AuditStore

	// T10.2 analytics: serves GET /api/analytics; nil answers 503.
	Analytics store.AnalyticsStore

	// Evidence enables the permission-filtered provisional evidence view. A
	// nil store leaves the route unregistered so the dark-launch default has
	// no discoverable read surface.
	Evidence store.EvidenceStore
	// ProofBundles persists T14.1 immutable bundles. It is kept separate from
	// Evidence so embedding/tests can expose the legacy evidence view without
	// accidentally enabling the proof API.
	ProofBundles store.ProofBundleStore
	// ProofBundleRetention is the opt-in lifetime since a bundle was last
	// materialized. Zero keeps bundles indefinitely.
	ProofBundleRetention time.Duration
	// Compatibility supplies the pinned, sandboxed Buf checker. Nil keeps the
	// HTTP operation and corresponding MCP capability undiscoverable.
	Compatibility compat.Service
	// Principal returns the stable authenticated subject recorded in a bundle.
	// AuthorizationProvider names the visibility policy generation.
	Principal             func(context.Context) string
	AuthorizationProvider string

	// Visible resolves the caller's repo visibility (T10.3): it returns this
	// request's predicate, or nil when the caller may see everything. A nil
	// field disables permission filtering (tests, permissions block absent).
	Visible func(ctx context.Context) func(store.Repo) bool

	// T7.4 webhook: empty secret leaves POST /api/webhook unregistered.
	// ResyncConnections are the code-host connection names to re-sync on
	// repository-membership events.
	WebhookSecret     string
	ResyncConnections []string
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
	registerAuditMiddleware(api, opts)

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
			Version      string   `json:"version" example:"0.1.0-dev"`
			Capabilities []string `json:"capabilities,omitempty"`
		}
	}
	huma.Get(api, "/api/version", func(context.Context, *struct{}) (*versionOut, error) {
		out := &versionOut{}
		out.Body.Version = opts.Version
		out.Body.Capabilities = proofCapabilities(opts)
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
		allow := repoFilter(ctx, opts)
		visible := repos[:0]
		for _, repo := range repos {
			if !repo.Deleting && (allow == nil || allow(repo)) {
				visible = append(visible, sanitizeRepo(repo))
			}
		}
		return &reposOut{Body: visible}, nil
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

	type streamErr struct {
		Message string `json:"message"`
	}
	sse.Register(api, huma.Operation{
		OperationID: "stream-search",
		Method:      http.MethodGet,
		Path:        "/api/stream_search",
		Summary:     "Streamed search (SSE): `results` events per shard batch, then `done` with aggregate stats",
	}, map[string]any{
		"results": &search.Result{},
		"done":    &search.Stats{},
		"error":   &streamErr{},
	}, func(ctx context.Context, in *searchIn, send sse.Sender) {
		if opts.Search == nil {
			_ = send(sse.Message{Data: &streamErr{Message: "search unavailable"}})
			return
		}
		stats, err := opts.Search.Stream(ctx, in.Q,
			search.Options{MaxMatches: in.MaxMatches, ContextLines: in.ContextLines},
			func(r *search.Result) { _ = send(sse.Message{Data: r}) })
		if err != nil {
			_ = send(sse.Message{Data: &streamErr{Message: err.Error()}})
			return
		}
		_ = send(sse.Message{Data: stats})
	})

	// --- file serving (T4.4): git plumbing over bare mirrors ---

	type repoRefIn struct {
		Repo string `query:"repo" required:"true" example:"github.com/foo/bar"`
		Ref  string `query:"ref" doc:"commit-ish; default HEAD"`
	}
	// no struct embedding: huma resolves query params on flat fields only
	type sourceIn struct {
		Repo string `query:"repo" required:"true" example:"github.com/foo/bar"`
		Ref  string `query:"ref" doc:"commit-ish; default HEAD"`
		Path string `query:"path" required:"true"`
	}
	type sourceOut struct {
		Body struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding" enum:"utf8,base64"`
			Size     int    `json:"size"`
		}
	}
	huma.Get(api, "/api/source", func(ctx context.Context, in *sourceIn) (*sourceOut, error) {
		if err := requireReadableRepo(ctx, opts, in.Repo); err != nil {
			return nil, gitErr(err)
		}
		content, err := phebssync.CatFile(ctx, opts.DataDir, in.Repo, in.Ref, in.Path)
		if err != nil {
			return nil, gitErr(err)
		}
		out := &sourceOut{}
		out.Body.Size = len(content)
		if utf8.Valid(content) {
			out.Body.Content, out.Body.Encoding = string(content), "utf8"
		} else {
			out.Body.Content, out.Body.Encoding = base64.StdEncoding.EncodeToString(content), "base64"
		}
		return out, nil
	})

	type folderIn struct {
		Repo string `query:"repo" required:"true" example:"github.com/foo/bar"`
		Ref  string `query:"ref" doc:"commit-ish; default HEAD"`
		Path string `query:"path" doc:"directory; default repo root"`
	}
	type folderOut struct {
		Body struct {
			Entries []phebssync.TreeEntry `json:"entries"`
		}
	}
	huma.Get(api, "/api/folder_contents", func(ctx context.Context, in *folderIn) (*folderOut, error) {
		if err := requireReadableRepo(ctx, opts, in.Repo); err != nil {
			return nil, gitErr(err)
		}
		entries, err := phebssync.FolderContents(ctx, opts.DataDir, in.Repo, in.Ref, in.Path)
		if err != nil {
			return nil, gitErr(err)
		}
		out := &folderOut{}
		out.Body.Entries = entries
		return out, nil
	})

	type treeOut struct {
		Body struct {
			Paths []string `json:"paths"`
		}
	}
	huma.Get(api, "/api/tree", func(ctx context.Context, in *repoRefIn) (*treeOut, error) {
		if err := requireReadableRepo(ctx, opts, in.Repo); err != nil {
			return nil, gitErr(err)
		}
		paths, err := phebssync.TreePaths(ctx, opts.DataDir, in.Repo, in.Ref)
		if err != nil {
			return nil, gitErr(err)
		}
		out := &treeOut{}
		out.Body.Paths = paths
		return out, nil
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
		if opts.IsAdmin != nil && !opts.IsAdmin(ctx) {
			return nil, huma.Error403Forbidden("administrator access required")
		}
		if err := phebssync.ValidateRepoName(in.Body.Repo); err != nil {
			return nil, huma.Error400BadRequest("invalid repository name")
		}
		setAuditTarget(ctx, in.Body.Repo)
		repo, err := opts.Store.GetRepo(ctx, in.Body.Repo)
		if err != nil {
			return nil, reindexErr(in.Body.Repo, err)
		}
		if repo.Deleting {
			return nil, huma.Error409Conflict("repository is being deleted")
		}
		if err := store.EnqueuePending(ctx, opts.Store, store.JobIndex, in.Body.Repo, in.Body.Force); err != nil {
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
		allow := repoFilter(ctx, opts)
		visible := statuses[:0]
		for _, status := range statuses {
			if !status.Deleting && (allow == nil || allow(status.Repo)) {
				status.Repo = sanitizeRepo(status.Repo)
				visible = append(visible, status)
			}
		}
		return &repoStatusOut{Body: visible}, nil
	})

	registerHistory(api, opts)
	registerCodeNavigation(api, opts)
	registerAudit(api, opts)
	registerAnalytics(api, opts)
	registerEvidence(api, opts)

	// raw handler, not huma: HMAC over the exact body bytes is the auth
	if opts.WebhookSecret != "" {
		mux.HandleFunc("POST /api/webhook", webhookHandler(opts))
	}

	return mux
}

func requireReadableRepo(ctx context.Context, opts Options, name string) error {
	// T10.3: a permission denial is indistinguishable from a missing repo —
	// disclosing existence would itself be a leak. The predicate resolves
	// before the existence check so both paths cost the same work.
	allow := repoFilter(ctx, opts)
	repo, err := opts.Store.GetRepo(ctx, name)
	if err != nil {
		return err
	}
	if repo.Deleting {
		return fmt.Errorf("repo %q: %w", name, store.ErrNotFound)
	}
	if allow != nil && !allow(*repo) {
		return fmt.Errorf("repo %q: %w", name, store.ErrNotFound)
	}
	return nil
}

// repoFilter resolves the request's visibility predicate; nil = everything.
func repoFilter(ctx context.Context, opts Options) func(store.Repo) bool {
	if opts.Visible == nil {
		return nil
	}
	return opts.Visible(ctx)
}

func sanitizeRepo(repo store.Repo) store.Repo {
	safe, err := phebssync.SanitizeURL(repo.CloneURL)
	if err != nil {
		// A legacy malformed URL may contain credentials but cannot be parsed
		// safely enough to retain any portion in an API response.
		repo.CloneURL = ""
	} else {
		repo.CloneURL = safe
	}
	if repo.WebURL, err = phebssync.SanitizeHTTPURL(repo.WebURL); err != nil {
		repo.WebURL = ""
	}
	if repo.ExternalHostURL, err = phebssync.SanitizeHTTPURL(repo.ExternalHostURL); err != nil {
		repo.ExternalHostURL = ""
	}
	return repo
}

// gitErr maps read-path failures onto HTTP: bad input 400, missing
// repo/ref/path 404, the rest 500.
func gitErr(err error) error {
	switch {
	case errors.Is(err, phebssync.ErrBadInput):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, phebssync.ErrTooLarge):
		return huma.Error413RequestEntityTooLarge(err.Error())
	default:
		return huma.Error500InternalServerError("git read", err)
	}
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
		strings.HasPrefix(p, "/api/openapi") || strings.HasPrefix(p, "/api/docs")
}

// IsAuthenticationExempt reports paths whose own trust boundary does not use
// user/session authentication. Webhooks verify the provider signature over
// their raw body; liveness and API discovery remain public.
func IsAuthenticationExempt(p string) bool {
	return openPath(p) || p == "/api/webhook"
}

func bearerOK(ctx huma.Context, key string) bool {
	tok, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(tok), []byte(key)) == 1
}
