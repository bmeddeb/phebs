package main

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestDocumentationAuthenticationAndPublicAssetIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	sessions := &failingHTTPSessionStore{AuthStore: state}
	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: sessions, Config: config.Auth{
		APIKey: "documentation-test-key", CookieSecure: &insecure,
		BootstrapUser: config.BootstrapUser{Email: "docs@example.com", Password: "documentation-password"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	docs := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		_, _ = w.Write([]byte("documentation"))
	})
	assets := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("public asset")) })
	notFound := http.NotFoundHandler()
	handler := newHTTPHandler(authService, notFound, notFound, notFound, documentationAndUI(authService, docs, assets), config.Server{})
	server := httptest.NewServer(handler)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	for _, name := range []string{"docs/README.md", "docs/phebs-architecture.svg", "docs/config.example.yaml", "spike/retained.json"} {
		assertStatus(t, client, http.MethodGet, server.URL+"/documentation/"+name, "", nil, http.StatusUnauthorized, "authentication required")
	}
	if reads.Load() != 0 {
		t.Fatal("anonymous request reached documentation")
	}
	assertStatus(t, client, http.MethodGet, server.URL+"/documentation/docs/README.md", "", http.Header{"Authorization": {"Bearer documentation-test-key"}}, http.StatusOK, "documentation")
	assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login", `{"email":"docs@example.com","password":"documentation-password"}`,
		http.Header{"Content-Type": {"application/json"}}, http.StatusOK, `"authenticated":true`)
	assertStatus(t, client, http.MethodGet, server.URL+"/documentation/docs/README.md", "", nil, http.StatusOK, "documentation")
	assertStatus(t, client, http.MethodGet, server.URL+"/documentation/docs/README.md", "", http.Header{"Authorization": {"Bearer wrong"}}, http.StatusUnauthorized, "authentication required")
	assertStatus(t, client, http.MethodPost, server.URL+"/documentation/docs/README.md", "", nil, http.StatusForbidden, "CSRF")
	before := sessions.calls.Load()
	sessions.fail.Store(true)
	for _, name := range []string{"/", "/assets/app.js", "/settings"} {
		assertStatus(t, client, http.MethodGet, server.URL+name, "", nil, http.StatusOK, "public asset")
	}
	if got := sessions.calls.Load(); got != before {
		t.Fatalf("public asset touched sessions: %d -> %d", before, got)
	}
	assertStatus(t, client, http.MethodGet, server.URL+"/documentation/docs/README.md", "", nil, http.StatusInternalServerError, "Internal Server Error")
	if got := reads.Load(); got != 2 {
		t.Fatalf("protected content reads = %d", got)
	}
}
