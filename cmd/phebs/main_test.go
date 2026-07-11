package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

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
