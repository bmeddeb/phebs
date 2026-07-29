package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// fakeStore panics on methods the API never touches.
type fakeStore struct {
	store.Store
	cleared  []string
	enqueued []string
	forces   []bool
}

type urlStore struct {
	fakeStore
	cloneURL        string
	webURL          string
	externalHostURL string
}

type historyStore struct {
	fakeStore
	hash string
}

type analysisUnitAPIStore struct {
	fakeStore
	unit *analysisunit.State
}

func (s *analysisUnitAPIStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{{
		Name: "github.com/foo/bar", IndexedAnalysisUnit: analysisunit.CloneState(s.unit),
	}}, nil
}

func (s *analysisUnitAPIStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return []store.RepoStatus{{
		Repo: store.Repo{
			Name:                "github.com/foo/bar",
			IndexedAnalysisUnit: analysisunit.CloneState(s.unit),
		},
		AnalysisUnit: analysisunit.CloneState(s.unit),
	}}, nil
}

func (s *historyStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	if name != "github.com/foo/bar" {
		return nil, store.ErrNotFound
	}
	return &store.Repo{Name: name, IndexedCommitHash: s.hash}, nil
}

func (s *urlStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{{Name: "github.com/foo/bar", CloneURL: s.cloneURL, WebURL: s.webURL, ExternalHostURL: s.externalHostURL}}, nil
}

func (s *urlStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return []store.RepoStatus{{
		Repo:     store.Repo{Name: "github.com/foo/bar", CloneURL: s.cloneURL, WebURL: s.webURL, ExternalHostURL: s.externalHostURL},
		Orphaned: true,
	}}, nil
}

func (fakeStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{{Name: "github.com/foo/bar", CloneURL: "https://user:api-secret@github.com/foo/bar.git"}}, nil
}

func TestRepoResponsesStripURLCredentials(t *testing.T) {
	for _, cloneURL := range []string{
		"https://user:api-secret@github.com/foo/bar.git",
		"https://user:api-secret@%zz/foo/bar.git",
		"https:user:api-secret@github.com/foo/bar.git",
		"https://github.com/foo/bar.git?access_token=api-secret",
		"ssh://user:api-secret@github.com/foo/bar.git",
	} {
		h := api.New(api.Options{Version: "t", Store: &urlStore{
			cloneURL:        cloneURL,
			webURL:          "https://user:web-secret@github.com/foo/bar?token=web-secret",
			externalHostURL: "https://user:host-secret@github.com#host-secret",
		}})
		for _, path := range []string{"/api/repos", "/api/repo-status"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d", path, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "user:") || strings.Contains(rec.Body.String(), "?token=") {
				t.Errorf("%s leaked URL credentials: %s", path, rec.Body.String())
			}
		}
	}
}

func TestAnalysisUnitAppearsOnlyInRepoStatusWithoutSourceContent(t *testing.T) {
	unit, err := (analysisunit.Scope{
		Repository: "github.com/foo/bar",
		Name:       "payments",
		Primary:    []string{"services/payments/src"},
		Supporting: []string{"contracts/payment.proto"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	handler := api.New(api.Options{
		Version: "t", Store: &analysisUnitAPIStore{unit: unit},
	})
	repos := httptest.NewRecorder()
	handler.ServeHTTP(
		repos,
		httptest.NewRequest(http.MethodGet, "/api/repos", nil),
	)
	if repos.Code != http.StatusOK ||
		strings.Contains(repos.Body.String(), "analysis_unit") ||
		strings.Contains(repos.Body.String(), "services/payments") {
		t.Fatalf("/api/repos leaked analysis-unit state: %d %s", repos.Code, repos.Body)
	}

	status := httptest.NewRecorder()
	handler.ServeHTTP(
		status,
		httptest.NewRequest(http.MethodGet, "/api/repo-status", nil),
	)
	body := status.Body.String()
	for _, expected := range []string{
		`"name":"payments"`,
		`"primary_paths":["services/payments/src"]`,
		`"primary_path_count":1`,
		`"supporting_path_count":1`,
		`"search_index_posture":"focused"`,
		`"typed_index_posture":"repository-root-unbound"`,
	} {
		if status.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("/api/repo-status = %d %s; missing %s", status.Code, body, expected)
		}
	}
	for _, forbidden := range []string{
		"package payments",
		"source_content",
		"blob_content",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("/api/repo-status leaked source content marker %q: %s", forbidden, body)
		}
	}
	openapi := httptest.NewRecorder()
	handler.ServeHTTP(
		openapi,
		httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil),
	)
	if strings.Contains(openapi.Body.String(), "indexed_analysis_unit") ||
		!strings.Contains(openapi.Body.String(), "analysis_unit") {
		t.Fatalf("OpenAPI exposed internal state or omitted status projection: %s", openapi.Body)
	}
}

func (fakeStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return []store.RepoStatus{{
		Repo:     store.Repo{Name: "github.com/foo/bar", CloneURL: "https://user:api-secret@github.com/foo/bar.git"},
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

func (f *fakeStore) EnqueuePending(_ context.Context, kind store.JobKind, target string, force bool) (*store.Job, error) {
	f.enqueued = append(f.enqueued, target)
	f.forces = append(f.forces, force)
	return &store.Job{Kind: kind, Target: target, Force: force}, nil
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

// SSE wiring: an empty index streams no results but must emit a `done`
// event with stats over text/event-stream. (Progressive multi-event behavior
// is covered by search.TestStream and the epic demo.)
func TestStreamSearchSSE(t *testing.T) {
	searcher, err := search.Open(t.TempDir(), &fakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	h := api.New(api.Options{Version: "t", Store: &fakeStore{}, Search: searcher})
	req := httptest.NewRequest(http.MethodGet, "/api/stream_search?q=needle", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: done") || !strings.Contains(body, `"match_count":0`) {
		t.Errorf("stream body missing done event with stats:\n%s", body)
	}
}

// TestFileServing drives /api/source, /api/folder_contents, and /api/tree
// against a real mirrored fixture (regression: embedded input structs made
// huma drop the repo query param).
func TestFileServing(t *testing.T) {
	origin := t.TempDir()
	run := func(args ...string) {
		full := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", origin}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("hi there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "x")

	dataDir := t.TempDir()
	if err := phebssync.Mirror(context.Background(), "file://"+origin,
		phebssync.RepoDir(dataDir, "github.com/foo/bar")); err != nil {
		t.Fatal(err)
	}
	h := api.New(api.Options{Version: "t", Store: &fakeStore{}, DataDir: dataDir})

	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	if code, body := get("/api/source?repo=github.com/foo/bar&path=hello.txt"); code != 200 ||
		!strings.Contains(body, `"content":"hi there\n"`) || !strings.Contains(body, `"encoding":"utf8"`) {
		t.Errorf("source = %d %s", code, body)
	}
	if code, body := get("/api/folder_contents?repo=github.com/foo/bar"); code != 200 ||
		!strings.Contains(body, `"name":"hello.txt"`) {
		t.Errorf("folder_contents = %d %s", code, body)
	}
	if code, body := get("/api/tree?repo=github.com/foo/bar"); code != 200 ||
		!strings.Contains(body, `"paths":["hello.txt"]`) {
		t.Errorf("tree = %d %s", code, body)
	}
	if code, _ := get("/api/source?repo=github.com/foo/bar&path=../../etc/passwd"); code != 400 {
		t.Errorf("traversal path status = %d, want 400", code)
	}
	if code, _ := get("/api/source?repo=github.com/no/pe&path=x"); code != 404 {
		t.Errorf("unknown repo status = %d, want 404", code)
	}
}

func TestHistoryEndpointsUseIndexedRevision(t *testing.T) {
	origin := t.TempDir()
	run := func(args ...string) string {
		full := append([]string{"-c", "user.name=History User", "-c", "user.email=history@example.com", "-C", origin}, args...)
		out, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "first")
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "second")
	head := run("rev-parse", "HEAD")

	dataDir := t.TempDir()
	if err := phebssync.Mirror(context.Background(), "file://"+origin,
		phebssync.RepoDir(dataDir, "github.com/foo/bar")); err != nil {
		t.Fatal(err)
	}
	h := api.New(api.Options{
		Version: "t", Store: &historyStore{hash: head}, DataDir: dataDir,
		CodeNav: codenav.New(codenav.Options{DataDir: dataDir}),
	})

	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/api/blame?repo=github.com/foo/bar&path=hello.txt", `"content":"second"`},
		{"/api/commits?repo=github.com/foo/bar&path=hello.txt", `"subject":"second"`},
		{"/api/commit?repo=github.com/foo/bar", `"status":"modified"`},
		{"/api/diff?repo=github.com/foo/bar", `+second`},
	} {
		code, body := get(tc.path)
		if code != http.StatusOK || !strings.Contains(body, tc.want) || !strings.Contains(body, head) {
			t.Errorf("GET %s = %d %s, want %q and indexed hash", tc.path, code, body, tc.want)
		}
	}
	code, body := get("/api/diff?repo=github.com/foo/bar&context_lines=0")
	var zeroContext phebssync.DiffResult
	if err := json.Unmarshal([]byte(body), &zeroContext); err != nil {
		t.Fatalf("decode zero-context diff: %v (%s)", err, body)
	}
	if code != http.StatusOK || !strings.Contains(zeroContext.Patch, "+second") ||
		strings.Contains(zeroContext.Patch, "\n first\n") {
		t.Errorf("zero-context API diff = %d %q", code, zeroContext.Patch)
	}
	if code, _ := get("/api/blame?repo=github.com/foo/bar&path=../../etc/passwd"); code != http.StatusBadRequest {
		t.Errorf("blame traversal status = %d, want 400", code)
	}
	for _, endpoint := range []string{"find_definitions", "find_references", "hover"} {
		path := "/api/" + endpoint + "?repo=github.com/foo/bar&path=hello.txt&line=0&character=1"
		if code, body := get(path); code != http.StatusOK || !strings.Contains(body, `"available":false`) {
			t.Errorf("GET %s = %d %s, want graceful unavailable SCIP result", path, code, body)
		}
	}
	if code, _ := get("/api/hover?repo=github.com/foo/bar&ref=HEAD&path=hello.txt&line=0&character=1"); code != http.StatusBadRequest {
		t.Errorf("hover symbolic ref status = %d, want 400", code)
	}
	missingRef := strings.Repeat("f", 40)
	if code, _ := get("/api/hover?repo=github.com/foo/bar&ref=" + missingRef + "&path=hello.txt&line=0&character=1"); code != http.StatusNotFound {
		t.Errorf("hover missing full ref status = %d, want 404", code)
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
		{"forced reindex", `{"repo":"github.com/foo/bar","force":true}`, 200, 0, 1},
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
			if tt.name == "forced reindex" && (len(fs.forces) != 1 || !fs.forces[0]) {
				t.Errorf("forced reindex did not persist force on the pending job: %v", fs.forces)
			}
		})
	}
}

func TestReindexRequiresAdministratorWhenConfigured(t *testing.T) {
	h := api.New(api.Options{
		Version: "t", Store: &fakeStore{},
		IsAdmin: func(context.Context) bool { return false },
	})
	req := httptest.NewRequest(http.MethodPost, "/api/reindex", strings.NewReader(`{"repo":"github.com/foo/bar"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "administrator") {
		t.Fatalf("reindex as non-admin = %d %s, want 403", rec.Code, rec.Body)
	}
}
