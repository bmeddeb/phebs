package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

// fakeStore panics on anything but ListRepos — all the skeleton needs.
type fakeStore struct{ store.Store }

func (fakeStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{{Name: "github.com/foo/bar", CloneURL: "https://github.com/foo/bar.git"}}, nil
}

func (fakeStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return []store.RepoStatus{{
		Repo:     store.Repo{Name: "github.com/foo/bar"},
		Orphaned: true,
	}}, nil
}

func TestAPI(t *testing.T) {
	tests := []struct {
		name       string
		key        string // configured api key
		path       string
		auth       string // Authorization header
		wantStatus int
		wantBody   string // substring
	}{
		{"health open", "sekrit", "/api/health", "", 200, `"status":"ok"`},
		{"version open", "sekrit", "/api/version", "", 200, `"version":"test-1"`},
		{"openapi renders", "sekrit", "/api/openapi.json", "", 200, `"openapi"`},
		{"repos no token", "sekrit", "/api/repos", "", 401, "bearer token"},
		{"repos wrong token", "sekrit", "/api/repos", "Bearer nope", 401, "bearer token"},
		{"repos right token", "sekrit", "/api/repos", "Bearer sekrit", 200, "github.com/foo/bar"},
		{"repos open when no key", "", "/api/repos", "", 200, "github.com/foo/bar"},
		{"repo-status", "sekrit", "/api/repo-status", "Bearer sekrit", 200, `"orphaned":true`},
		{"repo-status no token", "sekrit", "/api/repo-status", "", 401, "bearer token"},
		{"unknown route", "sekrit", "/api/nope", "Bearer sekrit", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := api.New(api.Options{Version: "test-1", APIKey: tt.key, Store: fakeStore{}})
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("%s: status = %d, want %d (body %s)", tt.path, rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("%s: body %q missing %q", tt.path, rec.Body, tt.wantBody)
			}
		})
	}
}
