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

// fakeStore panics on methods the API never touches.
type fakeStore struct {
	store.Store
	cleared  []string
	enqueued []string
}

func (fakeStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{{Name: "github.com/foo/bar", CloneURL: "https://github.com/foo/bar.git"}}, nil
}

func (fakeStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return []store.RepoStatus{{
		Repo:     store.Repo{Name: "github.com/foo/bar"},
		Orphaned: true,
	}}, nil
}

func (f *fakeStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	if name != "github.com/foo/bar" {
		return nil, store.ErrNotFound
	}
	return &store.Repo{Name: name}, nil
}

func (f *fakeStore) ClearRepoIndexState(_ context.Context, name string) error {
	if name != "github.com/foo/bar" {
		return store.ErrNotFound
	}
	f.cleared = append(f.cleared, name)
	return nil
}

func (f *fakeStore) ListJobs(context.Context, store.JobKind, store.JobStatus) ([]store.Job, error) {
	return nil, nil
}

func (f *fakeStore) CreateJob(_ context.Context, _ store.JobKind, target string) (*store.Job, error) {
	f.enqueued = append(f.enqueued, target)
	return &store.Job{Target: target}, nil
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
			h := api.New(api.Options{Version: "test-1", APIKey: tt.key, Store: &fakeStore{}})
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

func TestReindex(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantStatus   int
		wantCleared  int
		wantEnqueued int
	}{
		{"unknown repo", `{"repo":"github.com/no/pe"}`, 404, 0, 0},
		{"plain reindex", `{"repo":"github.com/foo/bar"}`, 200, 0, 1},
		{"forced reindex", `{"repo":"github.com/foo/bar","force":true}`, 200, 1, 1},
		{"unknown forced", `{"repo":"github.com/no/pe","force":true}`, 404, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &fakeStore{}
			h := api.New(api.Options{Version: "t", APIKey: "", Store: fs})
			req := httptest.NewRequest(http.MethodPost, "/api/reindex", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body)
			}
			if len(fs.cleared) != tt.wantCleared {
				t.Errorf("cleared = %v, want %d calls", fs.cleared, tt.wantCleared)
			}
			if len(fs.enqueued) != tt.wantEnqueued {
				t.Errorf("enqueued = %v, want %d jobs", fs.enqueued, tt.wantEnqueued)
			}
		})
	}
}
