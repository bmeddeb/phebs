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
	jobs []string // "kind:target"
}

func (f *jobStore) CreateJob(_ context.Context, kind store.JobKind, target string) (*store.Job, error) {
	f.jobs = append(f.jobs, string(kind)+":"+target)
	return &store.Job{Target: target}, nil
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

func TestWebhookDisabledWithoutSecret(t *testing.T) {
	h := api.New(api.Options{Version: "t", Store: &jobStore{}})
	req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unconfigured webhook = %d, want 404", rec.Code)
	}
}
