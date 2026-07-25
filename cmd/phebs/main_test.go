package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestHTTPHandlerAuthenticationBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: st, Config: config.Auth{
		CookieSecure: &insecure,
		BootstrapUser: config.BootstrapUser{
			Email: "admin@example.com", Password: "integration-password",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	apiHandler := api.New(api.Options{
		Version: "test", Store: st, DataDir: t.TempDir(), WebhookSecret: "hook-secret",
		IsAdmin: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			return ok && principal.IsAdmin
		},
	})
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "metrics") })
	uiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ui") })
	server := httptest.NewServer(newHTTPHandler(authService, apiHandler, mcpHandler, metricsHandler, uiHandler))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	assertStatus(t, client, http.MethodGet, server.URL+"/api/health", "", nil, http.StatusOK, `"status":"ok"`)
	assertStatus(t, client, http.MethodGet, server.URL+"/api/openapi.json", "", nil, http.StatusOK, `"openapi"`)
	assertStatus(t, client, http.MethodGet, server.URL+"/metrics", "", nil, http.StatusOK, "metrics")
	assertStatus(t, client, http.MethodGet, server.URL+"/", "", nil, http.StatusOK, "ui")
	assertStatus(t, client, http.MethodGet, server.URL+"/api/repos", "", nil, http.StatusUnauthorized, "authentication required")
	assertStatus(t, client, http.MethodGet, server.URL+"/api/mcp", "", nil, http.StatusUnauthorized, "authentication required")

	// The provider HMAC, not user auth, is the webhook trust boundary.
	assertStatus(t, client, http.MethodPost, server.URL+"/api/webhook", `{}`, nil, http.StatusUnauthorized, "bad signature")
	hookHeaders := http.Header{"X-Github-Event": []string{"ping"}}
	hookHeaders.Set("X-Hub-Signature-256", webhookSignature([]byte(`{}`), "hook-secret"))
	assertStatus(t, client, http.MethodPost, server.URL+"/api/webhook", `{}`, hookHeaders, http.StatusOK, "pong")

	login := assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"integration-password"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusOK, `"authenticated":true`)
	var loginResult struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login, &loginResult); err != nil || loginResult.CSRFToken == "" {
		t.Fatalf("login response = %s, err %v", login, err)
	}
	assertStatus(t, client, http.MethodGet, server.URL+"/api/repos", "", nil, http.StatusOK, "[]")
	assertStatus(t, client, http.MethodGet, server.URL+"/api/evidence?domain=proto-contract", "", nil,
		http.StatusNotFound, "404")
	assertStatus(t, client, http.MethodPost, server.URL+"/api/reindex", `{"repo":"github.com/no/repo"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusForbidden, "CSRF")
	assertStatus(t, client, http.MethodPost, server.URL+"/api/reindex", `{"repo":"github.com/no/repo"}`,
		http.Header{"Content-Type": []string{"application/json"}, "X-Csrf-Token": []string{loginResult.CSRFToken}},
		http.StatusNotFound, "unknown repo")

	keyHeaders := http.Header{"Content-Type": []string{"application/json"}, "X-Csrf-Token": []string{loginResult.CSRFToken}}
	created := assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/keys", `{"name":"integration"}`,
		keyHeaders, http.StatusCreated, `"token":"phebs_`)
	var keyResult struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created, &keyResult); err != nil || keyResult.Token == "" {
		t.Fatalf("key response = %s, err %v", created, err)
	}
	bearerHeaders := http.Header{"Authorization": []string{"Bearer " + keyResult.Token}}
	bearerClient := &http.Client{}
	assertStatus(t, bearerClient, http.MethodGet, server.URL+"/api/repos", "", bearerHeaders, http.StatusOK, "[]")
	assertStatus(t, bearerClient, http.MethodGet, server.URL+"/api/mcp", "", bearerHeaders, http.StatusNoContent, "")
	assertStatus(t, bearerClient, http.MethodGet, server.URL+"/api/auth/keys", "", bearerHeaders, http.StatusForbidden, "browser session")
}

func TestVersionCapabilitiesRequireAuthenticatedPrincipal(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: st, Config: config.Auth{
		CookieSecure: &insecure,
		BootstrapUser: config.BootstrapUser{
			Email: "admin@example.com", Password: "integration-password",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	apiHandler := api.New(api.Options{
		Version: "test", Store: st, Evidence: st, ProofBundles: st,
		Principal: func(ctx context.Context) string {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok {
				return ""
			}
			if principal.User != nil {
				return "user:" + principal.User.ID
			}
			if principal.APIKeyID != "" {
				return "api-key:" + principal.APIKeyID
			}
			return "authenticated:" + principal.AuthMethod
		},
	})
	notFound := http.NotFoundHandler()
	server := httptest.NewServer(newHTTPHandler(authService, apiHandler, notFound, notFound, notFound))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	anonymous := assertStatus(t, client, http.MethodGet, server.URL+"/api/version", "", nil, http.StatusOK, `"version":"test"`)
	if strings.Contains(string(anonymous), "contract-impact-report") {
		t.Fatalf("anonymous version leaked capabilities: %s", anonymous)
	}
	assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"integration-password"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusOK, `"authenticated":true`)
	assertStatus(t, client, http.MethodGet, server.URL+"/api/version", "", nil,
		http.StatusOK, `"contract-impact-report"`)

	invalid := assertStatus(t, client, http.MethodGet, server.URL+"/api/version", "",
		http.Header{"Authorization": []string{"Bearer invalid"}}, http.StatusOK, `"version":"test"`)
	if strings.Contains(string(invalid), "contract-impact-report") {
		t.Fatalf("invalid credentials leaked capabilities: %s", invalid)
	}
}

func TestPrintVersion(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{name: "exact build identity", want: version + "\n"},
		{name: "reject argument", args: []string{"extra"}, wantErr: "version accepts no arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := printVersion(test.args, &output)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("printVersion error = %v, want %q", err, test.wantErr)
				}
				if output.Len() != 0 {
					t.Fatalf("printVersion output = %q, want empty", output.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("printVersion output = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestEvidenceViewUsesAuthenticatedPrincipal(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	const (
		visibleRepo = "github.com/allowed/evidence"
		hiddenRepo  = "github.com/secret/hidden-evidence-service"
	)
	seedHTTPTestEvidence(t, st, visibleRepo, "visible.Service/Get")
	seedHTTPTestEvidence(t, st, hiddenRepo, "secret.HiddenService/Call")

	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: st, Config: config.Auth{
		CookieSecure: &insecure,
		BootstrapUser: config.BootstrapUser{
			Email: "admin@example.com", Password: "integration-password",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var principalResolutions atomic.Int32
	apiHandler := api.New(api.Options{
		Version: "test", Store: st, Evidence: st,
		Visible: func(ctx context.Context) func(store.Repo) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok || principal.User == nil || principal.User.NormalizedEmail != "admin@example.com" {
				return func(store.Repo) bool { return false }
			}
			principalResolutions.Add(1)
			return func(repo store.Repo) bool { return repo.Name == visibleRepo }
		},
	})
	server := httptest.NewServer(newHTTPHandler(
		authService,
		apiHandler,
		http.NotFoundHandler(),
		http.NotFoundHandler(),
		http.NotFoundHandler(),
	))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	assertStatus(t, client, http.MethodGet, server.URL+"/api/evidence?domain=proto-contract", "", nil,
		http.StatusUnauthorized, "authentication required")
	if principalResolutions.Load() != 0 {
		t.Fatal("unauthenticated evidence request reached the visibility resolver")
	}
	assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"integration-password"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusOK, `"authenticated":true`)
	response := assertStatus(t, client, http.MethodGet, server.URL+"/api/evidence?domain=proto-contract", "", nil,
		http.StatusOK, `"visible_repository_count":1`)
	if principalResolutions.Load() != 1 {
		t.Fatalf("authenticated visibility resolutions = %d, want 1", principalResolutions.Load())
	}
	if !bytes.Contains(response, []byte(visibleRepo)) || bytes.Contains(response, []byte(hiddenRepo)) ||
		bytes.Contains(response, []byte("HiddenService")) {
		t.Fatalf("authenticated evidence visibility = %s", response)
	}
}

func seedHTTPTestEvidence(t *testing.T, st *store.Surreal, repo, object string) {
	t.Helper()
	ctx := t.Context()
	commit := "commit-" + strings.ReplaceAll(repo, "/", "-")
	if err := st.UpsertRepo(ctx, store.Repo{Name: repo, CloneURL: "https://example.test/repo.git"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err := st.BeginExtractionRun(ctx, repo, commit, "proto-contract", "test")
	if err != nil {
		t.Fatal(err)
	}
	atom := store.EvidenceAtom{
		SchemaVersion: "test-v1", BlobDigest: "sha256:" + strings.Repeat("a", 64),
		StartByte: 0, EndByte: 1, RuleID: "test-rule", ExtractorVersion: "test",
		AdapterConfigDigest: "sha256:" + strings.Repeat("b", 64), FactFingerprint: object,
	}
	atom.ID = store.ComputeAtomID(atom)
	if err := st.AddEvidence(ctx, run.ID,
		[]store.EvidenceAtom{atom},
		[]store.SnapshotEvidence{{
			AtomID: atom.ID, Repo: repo, Commit: commit, Path: "api.proto",
			StartLine: 1, EndLine: 1, VisibilityScope: "repo:" + repo,
		}},
		[]store.Assertion{{
			Predicate: "DECLARES_OPERATION", Subject: "api.proto", Object: object,
			Tier: store.TierExact, Repo: repo, Supporting: []string{atom.ID},
		}},
	); err != nil {
		t.Fatal(err)
	}
	coverage := store.CoverageManifest{
		CorpusFileCount: 1, CandidateFileCount: 1, ReadFileCount: 1, ReadBytes: 1,
		SourceScopeDigest: "sha256:" + strings.Repeat("c", 64), AssertionCount: 1, AtomCount: 1,
	}
	if err := st.PublishExtractionRun(ctx, run.ID, coverage); err != nil {
		t.Fatal(err)
	}
}

func assertStatus(t *testing.T, client *http.Client, method, target, body string, headers http.Header, want int, contains string) []byte {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want || contains != "" && !bytes.Contains(data, []byte(contains)) {
		t.Fatalf("%s %s = %d %s, want %d containing %q", method, target, response.StatusCode, data, want, contains)
	}
	return data
}

func webhookSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type extractionBackfillStore struct {
	store.Store
	repos      []store.Repo
	listErr    error
	enqueueErr error
	pending    map[string]store.Job
	created    int
}

func (s *extractionBackfillStore) ListRepos(context.Context) ([]store.Repo, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.repos, nil
}

func (s *extractionBackfillStore) EnqueuePending(
	_ context.Context, kind store.JobKind, target string, force bool,
) (*store.Job, error) {
	if s.enqueueErr != nil {
		return nil, s.enqueueErr
	}
	if kind != store.JobExtract {
		return nil, errors.New("unexpected non-extraction job")
	}
	if s.pending == nil {
		s.pending = make(map[string]store.Job)
	}
	if job, ok := s.pending[target]; ok {
		return &job, nil
	}
	job := store.Job{Kind: kind, Target: target, Status: store.StatusPending, Force: force}
	s.pending[target] = job
	s.created++
	return &job, nil
}

func TestEnqueueExtractionBackfillIndexedLiveReposOnlyAndDedupes(t *testing.T) {
	st := &extractionBackfillStore{repos: []store.Repo{
		{Name: "example.com/live/one", IndexedCommitHash: "a"},
		{Name: "example.com/unindexed"},
		{Name: "example.com/deleting", IndexedCommitHash: "b", Deleting: true},
		{Name: "example.com/live/two", IndexedCommitHash: "c"},
	}}
	if err := enqueueExtractionBackfill(t.Context(), st); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if err := enqueueExtractionBackfill(t.Context(), st); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if st.created != 2 || len(st.pending) != 2 {
		t.Fatalf("created=%d pending=%v, want one job for each of two live indexed repos", st.created, st.pending)
	}
	for _, target := range []string{"example.com/live/one", "example.com/live/two"} {
		if _, ok := st.pending[target]; !ok {
			t.Errorf("missing backfill job for %s", target)
		}
	}
}

func TestEnqueueExtractionBackfillPropagatesListError(t *testing.T) {
	want := errors.New("list failed")
	err := enqueueExtractionBackfill(t.Context(), &extractionBackfillStore{listErr: want})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}

func TestEnqueueExtractionBackfillPropagatesEnqueueError(t *testing.T) {
	want := errors.New("enqueue failed")
	st := &extractionBackfillStore{
		repos:      []store.Repo{{Name: "example.com/live", IndexedCommitHash: "a"}},
		enqueueErr: want,
	}
	err := enqueueExtractionBackfill(t.Context(), st)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}

func TestEnqueueExtractionAfterIndexClassifiesEnqueueError(t *testing.T) {
	want := errors.New("enqueue failed")
	err := enqueueExtractionAfterIndex(t.Context(), &extractionBackfillStore{enqueueErr: want},
		"example.com/live", "abc123")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
	if class := store.Classify(err); class != store.ClassExtract {
		t.Fatalf("class = %q, want %q", class, store.ClassExtract)
	}
}

type evidenceMaintenanceStore struct {
	store.EvidenceStore
	calls       chan time.Duration
	results     []evidenceSweepResult
	resultIndex int
	count       int
	err         error
}

type evidenceSweepResult struct {
	count int
	err   error
}

type proofBundleMaintenanceStore struct {
	cutoffs     chan time.Time
	results     []evidenceSweepResult
	resultIndex int
}

func (s *proofBundleMaintenanceStore) SweepProofBundles(_ context.Context, activeAfter time.Time) (int, error) {
	s.cutoffs <- activeAfter
	if s.resultIndex >= len(s.results) {
		return 0, nil
	}
	result := s.results[s.resultIndex]
	s.resultIndex++
	return result.count, result.err
}

func (s *evidenceMaintenanceStore) SweepEvidence(
	_ context.Context, _ time.Time, staleStagedAfter time.Duration,
) (int, error) {
	s.calls <- staleStagedAfter
	if s.resultIndex < len(s.results) {
		result := s.results[s.resultIndex]
		s.resultIndex++
		return result.count, result.err
	}
	return s.count, s.err
}

func TestEvidenceSweepPassStopsWhenDrained(t *testing.T) {
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, 3),
		results: []evidenceSweepResult{
			{count: 1}, {count: 1}, {count: 0},
		},
	}
	deleted, backlog, err := runEvidenceSweepPass(t.Context(), st, 24*time.Hour)
	if err != nil || deleted != 2 || backlog {
		t.Fatalf("runEvidenceSweepPass = (%d, %v, %v), want (2, false, nil)", deleted, backlog, err)
	}
	if len(st.calls) != 3 {
		t.Fatalf("sweep calls = %d, want 3", len(st.calls))
	}
}

func TestProofBundleSweepPassUsesConfiguredLifetimeAndStopsWhenDrained(t *testing.T) {
	lifetime := 7 * 24 * time.Hour
	st := &proofBundleMaintenanceStore{
		cutoffs: make(chan time.Time, 3),
		results: []evidenceSweepResult{{count: 1}, {count: 1}, {count: 0}},
	}
	before := time.Now().UTC().Add(-lifetime)
	deleted, backlog, err := runProofBundleSweepPass(t.Context(), st, lifetime)
	after := time.Now().UTC().Add(-lifetime)
	if err != nil || deleted != 2 || backlog {
		t.Fatalf("runProofBundleSweepPass = (%d, %v, %v), want (2, false, nil)", deleted, backlog, err)
	}
	if len(st.cutoffs) != 3 {
		t.Fatalf("proof bundle sweep calls = %d, want 3", len(st.cutoffs))
	}
	for cutoff := range 3 {
		got := <-st.cutoffs
		if got.Before(before) || got.After(after) {
			t.Fatalf("cutoff %d = %s, want within [%s, %s]", cutoff, got, before, after)
		}
	}
}

func TestProofBundleMaintenanceCanceledBeforeBootDoesNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	st := &proofBundleMaintenanceStore{cutoffs: make(chan time.Time, 1)}
	runProofBundleMaintenance(ctx, st, 24*time.Hour, time.Hour, time.Second)
	if len(st.cutoffs) != 0 {
		t.Fatalf("canceled proof maintenance made %d sweep call(s)", len(st.cutoffs))
	}
}

func TestEvidenceSweepPassCapsLikelyBacklog(t *testing.T) {
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, evidenceSweepMaxRunsPerPass), count: 1,
	}
	deleted, backlog, err := runEvidenceSweepPass(t.Context(), st, 24*time.Hour)
	if err != nil || deleted != evidenceSweepMaxRunsPerPass || !backlog {
		t.Fatalf("runEvidenceSweepPass = (%d, %v, %v), want (%d, true, nil)",
			deleted, backlog, err, evidenceSweepMaxRunsPerPass)
	}
	if len(st.calls) != evidenceSweepMaxRunsPerPass {
		t.Fatalf("sweep calls = %d, want %d", len(st.calls), evidenceSweepMaxRunsPerPass)
	}
}

func TestEvidenceSweepPassStopsOnError(t *testing.T) {
	want := errors.New("sweep failed")
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, 2),
		results: []evidenceSweepResult{
			{count: 1}, {err: want},
		},
	}
	deleted, backlog, err := runEvidenceSweepPass(t.Context(), st, 24*time.Hour)
	if deleted != 1 || backlog || !errors.Is(err, want) {
		t.Fatalf("runEvidenceSweepPass = (%d, %v, %v), want (1, false, %v)", deleted, backlog, err, want)
	}
	if len(st.calls) != 2 {
		t.Fatalf("sweep calls = %d, want 2", len(st.calls))
	}
}

func TestEvidenceMaintenanceUsesBacklogDelayThenReturnsIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, evidenceSweepMaxRunsPerPass+2),
		results: append(
			make([]evidenceSweepResult, evidenceSweepMaxRunsPerPass),
			evidenceSweepResult{count: 0},
		),
	}
	for i := range evidenceSweepMaxRunsPerPass {
		st.results[i].count = 1
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runEvidenceMaintenance(ctx, st, time.Hour, 10*time.Millisecond, 24*time.Hour)
	}()

	for i := 0; i < evidenceSweepMaxRunsPerPass+1; i++ {
		select {
		case staleAge := <-st.calls:
			if staleAge != 24*time.Hour {
				t.Fatalf("stale staged age = %s, want 24h", staleAge)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d maintenance sweep call(s)", i)
		}
	}
	select {
	case <-st.calls:
		t.Fatal("drained maintenance pass did not return to the idle interval")
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("evidence maintenance did not stop after cancellation")
	}
}

func TestEvidenceMaintenanceIdleBootWaitsForIdleInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st := &evidenceMaintenanceStore{calls: make(chan time.Duration, 2)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runEvidenceMaintenance(ctx, st, time.Hour, 10*time.Millisecond, 24*time.Hour)
	}()
	select {
	case staleAge := <-st.calls:
		if staleAge != 24*time.Hour {
			t.Fatalf("stale staged age = %s, want 24h", staleAge)
		}
	case <-time.After(time.Second):
		t.Fatal("boot evidence sweep did not run")
	}
	select {
	case <-st.calls:
		t.Fatal("idle maintenance retried on the backlog delay")
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("evidence maintenance did not stop after cancellation")
	}
}

func TestEvidenceMaintenanceCanceledBeforeBootDoesNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	st := &evidenceMaintenanceStore{calls: make(chan time.Duration, 1)}
	runEvidenceMaintenance(ctx, st, time.Hour, time.Second, 24*time.Hour)
	if len(st.calls) != 0 {
		t.Fatalf("canceled maintenance made %d sweep call(s)", len(st.calls))
	}
}

func TestEvidenceExtractorsRemainValidationGated(t *testing.T) {
	if got := evidenceExtractors(false, false); len(got) != 0 {
		t.Fatalf("default extractor registry = %d entries, want disabled", len(got))
	}
	got := evidenceExtractors(true, false)
	// T17.2: the registry pins the shape-aware protodecl v3 alongside the
	// T13.1/T13.2 readers behind the proto experimental flag; default runtime
	// activation remains disabled.
	if len(got) != 3 || got[0].Domain() != "proto-contract" || got[1].Domain() != "grpc-consumer" ||
		got[2].Domain() != "scip-proto-field" || got[0].Version() != "3.0.0" {
		t.Fatalf("proto-only extractor registry = %#v", got)
	}
	// T19.2: the Thrift declaration pack rides its own dark flag and composes
	// with the proto pack without reordering it.
	thriftOnly := evidenceExtractors(false, true)
	if len(thriftOnly) != 1 || thriftOnly[0].Domain() != "thrift-contract" ||
		thriftOnly[0].Version() != "1.0.0" {
		t.Fatalf("thrift-only extractor registry = %#v", thriftOnly)
	}
	both := evidenceExtractors(true, true)
	if len(both) != 4 || both[0].Domain() != "proto-contract" ||
		both[3].Domain() != "thrift-contract" {
		t.Fatalf("combined extractor registry = %#v", both)
	}
}
