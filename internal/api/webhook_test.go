package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

// jobStore extends fakeStore to record job kinds, which webhook routing
// dispatches on (fetch vs sync).
type jobStore struct {
	fakeStore
	jobs         []string // "kind:target"
	inFlight     []store.Job
	repoErr      error // non-nil: GetRepo fails with this for every repo
	enqueueErr   error
	repoDeleting bool
}

func (f *jobStore) EnqueuePending(_ context.Context, kind store.JobKind, target string, force bool) (*store.Job, error) {
	if f.enqueueErr != nil {
		return nil, f.enqueueErr
	}
	for i := range f.inFlight {
		if f.inFlight[i].Kind == kind && f.inFlight[i].Target == target && f.inFlight[i].Status == store.StatusPending {
			f.inFlight[i].Force = f.inFlight[i].Force || force
			return &f.inFlight[i], nil
		}
	}
	f.jobs = append(f.jobs, string(kind)+":"+target)
	return &store.Job{Kind: kind, Target: target, Status: store.StatusPending, Force: force}, nil
}

func (f *jobStore) CreateJob(_ context.Context, kind store.JobKind, target string) (*store.Job, error) {
	f.jobs = append(f.jobs, string(kind)+":"+target)
	return &store.Job{Target: target}, nil
}

func (f *jobStore) ListJobs(_ context.Context, kind store.JobKind, status store.JobStatus) ([]store.Job, error) {
	var out []store.Job
	for _, j := range f.inFlight {
		if j.Kind == kind && j.Status == status {
			out = append(out, j)
		}
	}
	return out, nil
}

func (f *jobStore) GetRepo(ctx context.Context, name string) (*store.Repo, error) {
	if f.repoErr != nil {
		return nil, f.repoErr
	}
	repo, err := f.fakeStore.GetRepo(ctx, name)
	if repo != nil {
		repo.Deleting = f.repoDeleting
	}
	return repo, err
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhook(t *testing.T) {
	const secret = "hush"
	pushKnown := `{"repository":{"clone_url":"https://github.com/foo/bar.git"}}`
	pushUnknown := `{"repository":{"clone_url":"https://github.com/no/such.git"}}`

	tests := []struct {
		name     string
		event    string
		body     string
		sig      string
		wantCode int
		wantJobs []string
	}{
		{"ping", "ping", `{}`, sign(secret, `{}`), 200, nil},
		{"bad signature", "push", pushKnown, sign("wrong", pushKnown), 401, nil},
		{"missing signature", "push", pushKnown, "", 401, nil},
		{"tampered body", "push", pushKnown + " ", sign(secret, pushKnown), 401, nil},
		{"push known repo", "push", pushKnown, sign(secret, pushKnown), 202,
			[]string{"repo_fetch_job:github.com/foo/bar"}},
		{"push unknown repo ignored", "push", pushUnknown, sign(secret, pushUnknown), 202, nil},
		{"push without clone_url", "push", `{}`, sign(secret, `{}`), 400, nil},
		{"repository event re-syncs connections", "repository", `{}`, sign(secret, `{}`), 202,
			[]string{"connection_sync_job:gh", "connection_sync_job:gl"}},
		{"installation_repositories event re-syncs connections", "installation_repositories", `{}`, sign(secret, `{}`), 202,
			[]string{"connection_sync_job:gh", "connection_sync_job:gl"}},
		{"unhandled event ignored", "star", `{}`, sign(secret, `{}`), 202, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &jobStore{}
			h := api.New(api.Options{Version: "t", APIKey: "apikey", Store: fs,
				WebhookSecret: secret, ResyncConnections: []string{"gh", "gl"}})
			req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(tt.body))
			req.Header.Set("X-GitHub-Event", tt.event)
			if tt.sig != "" {
				req.Header.Set("X-Hub-Signature-256", tt.sig)
			}
			// note: no Authorization bearer — HMAC is the auth here
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body)
			}
			if got := strings.Join(fs.jobs, ","); got != strings.Join(tt.wantJobs, ",") {
				t.Errorf("jobs = %v, want %v", fs.jobs, tt.wantJobs)
			}
		})
	}
}

// Epic 7 review regressions: truncation-as-401, store-error-as-202, and the
// running-fetch lost-wakeup.
func TestWebhookEdgeCases(t *testing.T) {
	const secret = "hush"
	pushKnown := `{"repository":{"clone_url":"https://github.com/foo/bar.git"}}`
	newReq := func(event, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(body))
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-Hub-Signature-256", sign(secret, body))
		return req
	}
	newAPI := func(fs *jobStore) http.Handler {
		return api.New(api.Options{Version: "t", Store: fs, WebhookSecret: secret,
			ResyncConnections: []string{"gh"}})
	}

	t.Run("oversize body is 413, not bad-signature", func(t *testing.T) {
		big := `{"pad":"` + strings.Repeat("x", 25<<20) + `"}` // > 25MB
		rec := httptest.NewRecorder()
		newAPI(&jobStore{}).ServeHTTP(rec, newReq("push", big))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
	})

	t.Run("store failure is 500, not unknown-repo 202", func(t *testing.T) {
		fs := &jobStore{repoErr: context.DeadlineExceeded}
		rec := httptest.NewRecorder()
		newAPI(fs).ServeHTTP(rec, newReq("push", pushKnown))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (sender must retry)", rec.Code)
		}
		if len(fs.jobs) != 0 {
			t.Errorf("jobs = %v, want none", fs.jobs)
		}
	})

	t.Run("push during running fetch still enqueues", func(t *testing.T) {
		fs := &jobStore{inFlight: []store.Job{
			{Kind: store.JobFetch, Target: "github.com/foo/bar", Status: store.StatusRunning},
		}}
		rec := httptest.NewRecorder()
		newAPI(fs).ServeHTTP(rec, newReq("push", pushKnown))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", rec.Code)
		}
		if len(fs.jobs) != 1 || fs.jobs[0] != "repo_fetch_job:github.com/foo/bar" {
			t.Fatalf("jobs = %v, want one fetch despite the running job (lost-wakeup)", fs.jobs)
		}
	})

	t.Run("pending fetch still dedupes", func(t *testing.T) {
		fs := &jobStore{inFlight: []store.Job{
			{Kind: store.JobFetch, Target: "github.com/foo/bar", Status: store.StatusPending},
		}}
		rec := httptest.NewRecorder()
		newAPI(fs).ServeHTTP(rec, newReq("push", pushKnown))
		if rec.Code != http.StatusAccepted || len(fs.jobs) != 0 {
			t.Fatalf("code=%d jobs=%v, want 202 and no duplicate of the pending job", rec.Code, fs.jobs)
		}
	})

	t.Run("deleting repo ignores push", func(t *testing.T) {
		fs := &jobStore{repoDeleting: true}
		rec := httptest.NewRecorder()
		newAPI(fs).ServeHTTP(rec, newReq("push", pushKnown))
		if rec.Code != http.StatusAccepted || len(fs.jobs) != 0 {
			t.Fatalf("code=%d jobs=%v, want deleting repo ignored", rec.Code, fs.jobs)
		}
	})

	t.Run("membership enqueue failure is retriable", func(t *testing.T) {
		fs := &jobStore{enqueueErr: context.DeadlineExceeded}
		rec := httptest.NewRecorder()
		newAPI(fs).ServeHTTP(rec, newReq("repository", `{}`))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 so sender retries", rec.Code)
		}
	})
}

func TestWebhookDisabledWithoutSecret(t *testing.T) {
	h := api.New(api.Options{Version: "t", Store: &jobStore{}})
	req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unconfigured webhook = %d, want 404", rec.Code)
	}
}
