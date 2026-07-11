package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// T10.1: the auth surface bypasses the huma audit middleware, so its handlers
// record events through Options.Audit directly. This drives the full flow and
// asserts the recorded sequence.
func TestAuditEventsOnAuthSurface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var events []store.AuditEvent
	st := newMemoryAuthStore()
	service, err := New(ctx, Options{
		Store: st, Config: config.Auth{CookieSecure: insecureCookieConfig()},
		ArgonConcurrency: 1,
		Audit: func(_ context.Context, e store.AuditEvent) {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.LoadAndSave(service.Handler()))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	setupBody := fmt.Sprintf(`{"email":"admin@example.com","display_name":"Admin","password":"correct horse battery staple","setup_token":%q}`, service.SetupToken())
	response := request(t, client, http.MethodPost, server.URL+"/api/auth/setup", setupBody, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d: %s", response.StatusCode, readBody(response))
	}
	var loggedIn statusResponse
	decodeResponse(t, response, &loggedIn)

	response = request(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"wrong password entirely"}`, "", "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", response.StatusCode)
	}
	_ = response.Body.Close()

	response = request(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"correct horse battery staple"}`, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", response.StatusCode)
	}
	decodeResponse(t, response, &loggedIn)

	response = request(t, client, http.MethodPost, server.URL+"/api/auth/keys", `{"name":"CI"}`, loggedIn.CSRFToken, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d: %s", response.StatusCode, readBody(response))
	}
	var created struct {
		Key keyResponse `json:"key"`
	}
	decodeResponse(t, response, &created)

	response = request(t, client, http.MethodDelete, server.URL+"/api/auth/keys/"+created.Key.ID, "", loggedIn.CSRFToken, "")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke key = %d", response.StatusCode)
	}
	_ = response.Body.Close()

	response = request(t, client, http.MethodPost, server.URL+"/api/auth/logout", "", loggedIn.CSRFToken, "")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d", response.StatusCode)
	}
	_ = response.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	want := []struct {
		action string
		status int
		actor  bool // expect actor fields populated
	}{
		{"auth.setup", http.StatusOK, true},
		{"auth.login", http.StatusUnauthorized, false},
		{"auth.login", http.StatusOK, true},
		{"auth.key.create", http.StatusCreated, true},
		{"auth.key.revoke", http.StatusNoContent, true},
		{"auth.logout", http.StatusNoContent, true},
	}
	if len(events) != len(want) {
		t.Fatalf("recorded %d events %v, want %d", len(events), events, len(want))
	}
	for i, w := range want {
		e := events[i]
		if e.Action != w.action || e.Status != w.status {
			t.Errorf("event[%d] = %s/%d, want %s/%d", i, e.Action, e.Status, w.action, w.status)
		}
		if w.actor && (e.ActorID == "" || e.ActorEmail != "admin@example.com" || e.AuthMethod != "session") {
			t.Errorf("event[%d] actor = %+v, want admin@example.com session actor", i, e)
		}
		if !w.actor && e.ActorID != "" {
			t.Errorf("event[%d] unauthenticated action carries an actor: %+v", i, e)
		}
		if e.SourceIP == "" {
			t.Errorf("event[%d] has no source IP", i)
		}
	}
	if events[1].Target != "admin@example.com" {
		t.Errorf("failed login target = %q, want attempted email", events[1].Target)
	}
	if events[3].Target != created.Key.ID || events[4].Target != created.Key.ID {
		t.Errorf("key lifecycle targets = %q/%q, want %q", events[3].Target, events[4].Target, created.Key.ID)
	}
}
