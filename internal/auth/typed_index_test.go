package auth

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestTypedIndexBrowserCSRFBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st := newMemoryAuthStore()
	if _, err := st.CreateFirstUser(ctx, store.User{ID: "admin", Email: "admin@example.invalid"}); err != nil {
		t.Fatal(err)
	}
	service, err := New(ctx, Options{Store: st, Config: config.Auth{CookieSecure: insecureCookieConfig()}})
	if err != nil {
		t.Fatal(err)
	}
	handler := api.New(api.Options{IsAdmin: func(ctx context.Context) bool { p, ok := PrincipalFromContext(ctx); return ok && p.IsAdmin }})
	server := httptest.NewServer(service.LoadAndSave(service.Require(handler)))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := establishSession(t, service, jar, server.URL, "admin")
	for _, path := range []string{"/plan", "/enqueue"} {
		body := `{"repository":"example.test/repo","expected_revision":"one","provider":"one","profile":"one","purpose":"publish"}`
		if path == "/enqueue" {
			body = body[:len(body)-1] + `,"request_digest":"one","idempotency_key":"one"}`
		}
		for _, token := range []string{"", "wrong", csrf} {
			response := request(t, client, http.MethodPost, server.URL+api.TypedIndexPath+path, body, token, "")
			want := 403
			if token == csrf {
				want = 404
			}
			if response.StatusCode != want {
				t.Fatal(path, token == csrf, response.StatusCode)
			}
			_ = response.Body.Close()
		}
	}
}
