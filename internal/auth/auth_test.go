package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

type memorySession struct {
	data   []byte
	expiry time.Time
}

type memoryAuthStore struct {
	mu            sync.Mutex
	users         map[string]store.User
	keys          map[string]store.APIKey
	sessions      map[string]memorySession
	setupComplete bool
}

func newMemoryAuthStore() *memoryAuthStore {
	return &memoryAuthStore{users: map[string]store.User{}, keys: map[string]store.APIKey{}, sessions: map[string]memorySession{}}
}

func (m *memoryAuthStore) AuthStats(context.Context) (store.AuthStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := store.AuthStats{Users: len(m.users)}
	stats.SetupComplete = m.setupComplete
	for _, user := range m.users {
		if user.PasswordHash != "" {
			stats.PasswordUsers++
		}
	}
	return stats, nil
}

func (m *memoryAuthStore) CreateFirstUser(_ context.Context, user store.User) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) != 0 {
		return nil, store.ErrConflict
	}
	user.IsAdmin = true
	user.UpdatedAt = user.CreatedAt
	m.users[user.ID] = user
	m.setupComplete = true
	copy := user
	return &copy, nil
}

func (m *memoryAuthStore) CreateUser(_ context.Context, user store.User) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if existing.NormalizedEmail == user.NormalizedEmail {
			return nil, store.ErrConflict
		}
	}
	user.UpdatedAt = user.CreatedAt
	m.users[user.ID] = user
	copy := user
	return &copy, nil
}

func (m *memoryAuthStore) GetUserByID(_ context.Context, id string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &user, nil
}

func (m *memoryAuthStore) GetUserByEmail(_ context.Context, email string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, user := range m.users {
		if user.NormalizedEmail == email {
			copy := user
			return &copy, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memoryAuthStore) UpsertOIDCUser(_ context.Context, issuer, subject, email, normalizedEmail, displayName string, verified bool) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !verified {
		return nil, fmt.Errorf("unverified")
	}
	m.setupComplete = true
	for _, user := range m.users {
		if user.OIDCIssuer == issuer && user.OIDCSubject == subject {
			return &user, nil
		}
		if user.NormalizedEmail == normalizedEmail {
			return nil, store.ErrConflict
		}
	}
	id := fmt.Sprintf("oidc-%d", len(m.users)+1)
	user := store.User{ID: id, Email: email, NormalizedEmail: normalizedEmail,
		DisplayName: displayName, OIDCIssuer: issuer, OIDCSubject: subject,
		IsAdmin: len(m.users) == 0, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	m.users[id] = user
	return &user, nil
}

func TestMalformedLoginDoesNotConsumeAttemptQuota(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := newMemoryAuthStore()
	service, err := New(ctx, Options{Store: st, Config: config.Auth{
		CookieSecure: insecureCookieConfig(),
		BootstrapUser: config.BootstrapUser{
			Email: "admin@example.com", Password: "correct horse battery staple",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := service.LoadAndSave(service.Handler())
	login := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	for range 8 {
		response := login(`{`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed login = %d, want 400", response.Code)
		}
	}
	response := login(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("valid login after malformed bodies = %d: %s", response.Code, response.Body.String())
	}
}

func (m *memoryAuthStore) MarkUserLogin(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[id]
	if !ok {
		return store.ErrNotFound
	}
	user.LastLoginAt = &at
	m.users[id] = user
	return nil
}

func (m *memoryAuthStore) CreateAPIKey(_ context.Context, key store.APIKey) (*store.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.keys[key.ID]; exists {
		return nil, store.ErrConflict
	}
	m.keys[key.ID] = key
	copy := key
	return &copy, nil
}

func (m *memoryAuthStore) GetAPIKey(_ context.Context, id string) (*store.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.keys[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &key, nil
}

func (m *memoryAuthStore) ListAPIKeys(_ context.Context, userID string) ([]store.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []store.APIKey
	for _, key := range m.keys {
		if key.UserID == userID && key.RevokedAt == nil {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (m *memoryAuthStore) RevokeAPIKey(_ context.Context, id, userID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.keys[id]
	if !ok || key.UserID != userID || key.RevokedAt != nil {
		return store.ErrNotFound
	}
	key.RevokedAt = &at
	m.keys[id] = key
	return nil
}

func (m *memoryAuthStore) TouchAPIKey(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.keys[id]
	if !ok {
		return store.ErrNotFound
	}
	key.LastUsedAt = &at
	m.keys[id] = key
	return nil
}

func (m *memoryAuthStore) SetLegacyAPIKey(_ context.Context, hash string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hash == "" {
		delete(m.keys, legacyKeyID)
		return nil
	}
	m.keys[legacyKeyID] = store.APIKey{ID: legacyKeyID, Name: "Legacy config key", Prefix: "legacy", Hash: hash, CreatedAt: at}
	return nil
}

func (m *memoryAuthStore) CommitAuthSession(_ context.Context, token string, data []byte, expiry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[token] = memorySession{data: append([]byte(nil), data...), expiry: expiry}
	return nil
}

func (m *memoryAuthStore) FindAuthSession(_ context.Context, token string, now time.Time) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[token]
	if !ok || !session.expiry.After(now) {
		return nil, false, nil
	}
	return append([]byte(nil), session.data...), true, nil
}

func (m *memoryAuthStore) DeleteAuthSession(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

func (m *memoryAuthStore) DeleteExpiredAuthSessions(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for token, session := range m.sessions {
		if !session.expiry.After(now) {
			delete(m.sessions, token)
			n++
		}
	}
	return n, nil
}

func insecureCookieConfig() *bool {
	value := false
	return &value
}

func TestSetupLoginSessionAPIKeyAndCSRF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := newMemoryAuthStore()
	service, err := New(ctx, Options{Store: st, Config: config.Auth{CookieSecure: insecureCookieConfig()}, ArgonConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if service.SetupToken() == "" {
		t.Fatal("fresh install has no setup token")
	}
	server := httptest.NewServer(service.LoadAndSave(service.Handler()))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	status := getStatus(t, client, server.URL)
	if !status.SetupRequired || !status.AuthRequired || status.Authenticated {
		t.Fatalf("fresh status = %+v", status)
	}
	plain, err := client.Post(server.URL+"/api/auth/setup", "text/plain",
		strings.NewReader(`{"email":"admin@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if plain.StatusCode != http.StatusBadRequest {
		t.Fatalf("simple cross-site content type = %d, want 400", plain.StatusCode)
	}
	_ = plain.Body.Close()
	setupBody := fmt.Sprintf(`{"email":"admin@example.com","display_name":"Admin","password":"correct horse battery staple","setup_token":%q}`, service.SetupToken())
	response := request(t, client, http.MethodPost, server.URL+"/api/auth/setup", setupBody, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("setup status = %d: %s", response.StatusCode, readBody(response))
	}
	var loggedIn statusResponse
	decodeResponse(t, response, &loggedIn)
	if !loggedIn.Authenticated || loggedIn.SetupRequired || loggedIn.CSRFToken == "" || loggedIn.User == nil || !loggedIn.User.IsAdmin {
		t.Fatalf("setup response = %+v", loggedIn)
	}
	st.mu.Lock()
	stored := st.users[loggedIn.User.ID]
	st.mu.Unlock()
	if stored.PasswordHash == "correct horse battery staple" || !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("password persisted unsafely: %q", stored.PasswordHash)
	}

	response = request(t, client, http.MethodPost, server.URL+"/api/auth/keys", `{"name":"CI"}`, "", "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("create key without CSRF = %d, want 403", response.StatusCode)
	}
	response = request(t, client, http.MethodPost, server.URL+"/api/auth/keys", `{"name":"CI"}`, loggedIn.CSRFToken, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d: %s", response.StatusCode, readBody(response))
	}
	var created struct {
		Key   keyResponse `json:"key"`
		Token string      `json:"token"`
	}
	decodeResponse(t, response, &created)
	if !strings.HasPrefix(created.Token, "phebs_") || created.Key.ID == "" {
		t.Fatalf("created key response = %+v", created)
	}
	st.mu.Lock()
	storedKey := st.keys[created.Key.ID]
	st.mu.Unlock()
	if storedKey.Hash == created.Token || storedKey.Hash != bearerHash(created.Token) {
		t.Fatalf("API key was not hashed: %+v", storedKey)
	}

	for _, attempt := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/auth/keys", ""},
		{http.MethodPost, "/api/auth/keys", `{"name":"replacement"}`},
		{http.MethodDelete, "/api/auth/keys/" + created.Key.ID, ""},
	} {
		response = request(t, client, attempt.method, server.URL+attempt.path, attempt.body, "", created.Token)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("bearer %s key management = %d, want 403", attempt.method, response.StatusCode)
		}
		_ = response.Body.Close()
	}
	response = request(t, client, http.MethodGet, server.URL+"/api/auth/status", "", "", created.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bearer status auth = %d: %s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	response = request(t, client, http.MethodDelete, server.URL+"/api/auth/keys/"+created.Key.ID, "", loggedIn.CSRFToken, "")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke key = %d: %s", response.StatusCode, readBody(response))
	}
	response = request(t, client, http.MethodGet, server.URL+"/api/auth/keys", "", "", created.Token)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked bearer = %d, want 401", response.StatusCode)
	}
	response = request(t, client, http.MethodGet, server.URL+"/api/auth/status", "", "", "invalid-explicit-bearer")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid bearer fell back to session: status=%d", response.StatusCode)
	}
	response = request(t, client, http.MethodPost, server.URL+"/api/auth/logout", "", "", "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF = %d, want 403", response.StatusCode)
	}
	response = request(t, client, http.MethodPost, server.URL+"/api/auth/logout", "", loggedIn.CSRFToken, "")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d: %s", response.StatusCode, readBody(response))
	}
	if status := getStatus(t, client, server.URL); status.Authenticated {
		t.Fatalf("status after logout = %+v", status)
	}
	service.argonSlots <- struct{}{}
	response = request(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"correct horse battery staple"}`, "", "")
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("login under Argon2 saturation = %d, want 429", response.StatusCode)
	}
	_ = response.Body.Close()
	<-service.argonSlots
	response = request(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"wrong password"}`, "", "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password login = %d, want 401", response.StatusCode)
	}
	response = request(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"correct horse battery staple"}`, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("password login = %d: %s", response.StatusCode, readBody(response))
	}
	var relogged statusResponse
	decodeResponse(t, response, &relogged)
	if !relogged.Authenticated || relogged.CSRFToken == "" || relogged.CSRFToken == loggedIn.CSRFToken {
		t.Fatalf("password login response = %+v", relogged)
	}

	// A second service instance over the same session table can load the SCS
	// cookie, proving sessions bridge process restarts rather than memory state.
	second, err := New(ctx, Options{Store: st, Config: config.Auth{CookieSecure: insecureCookieConfig()}})
	if err != nil {
		t.Fatal(err)
	}
	secondServer := httptest.NewServer(second.LoadAndSave(second.Handler()))
	defer secondServer.Close()
	for _, cookie := range jar.Cookies(mustURL(t, server.URL)) {
		jar.SetCookies(mustURL(t, secondServer.URL), []*http.Cookie{cookie})
	}
	status = getStatus(t, client, secondServer.URL)
	if !status.Authenticated || status.User == nil || status.User.Email != "admin@example.com" {
		t.Fatalf("restarted session status = %+v", status)
	}
	st.mu.Lock()
	for tokenHash := range st.sessions {
		if strings.Contains(tokenHash, "phebs_session") || len(tokenHash) != 43 {
			t.Fatalf("session key %q is not a SHA-256 base64url digest", tokenHash)
		}
	}
	st.mu.Unlock()
}

func TestLegacyBearerImportedAsHash(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := newMemoryAuthStore()
	const legacyToken = "phebs_legacy-shaped.token"
	service, err := New(ctx, Options{Store: st, Config: config.Auth{APIKey: legacyToken, CookieSecure: insecureCookieConfig()}})
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	legacy := st.keys[legacyKeyID]
	st.mu.Unlock()
	if legacy.Hash == legacyToken || legacy.Hash != bearerHash(legacyToken) {
		t.Fatalf("legacy key persisted incorrectly: %+v", legacy)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+legacyToken)
	principal, err := service.Authenticate(serviceRequestContext(t, service, req))
	if err != nil || !principal.IsAdmin || principal.User != nil {
		t.Fatalf("legacy principal = %+v, %v", principal, err)
	}
}

func TestOIDCLoginBridgesToSession(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "phebs-test-client"
	var issuer string
	var challenge string
	var nonce string
	testIDP := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{
		PublicKey: privateKey.Public(), KeyID: "test-key", Algorithm: oidc.RS256,
	}}}
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			challenge = r.URL.Query().Get("code_challenge")
			nonce = r.URL.Query().Get("nonce")
			http.Error(w, "browser interaction not used in test", http.StatusTeapot)
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if challenge == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				http.Error(w, "bad PKCE verifier", http.StatusUnauthorized)
				return
			}
			claims := fmt.Sprintf(`{"iss":%q,"aud":%q,"sub":"subject-123","exp":%d,"iat":%d,"nonce":%q,"email":"oidc@example.com","email_verified":true,"name":"OIDC User"}`,
				issuer, clientID, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), nonce)
			raw := oidctest.SignIDToken(privateKey, "test-key", oidc.RS256, claims)
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": raw})
		default:
			testIDP.ServeHTTP(w, r)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	testIDP.SetIssuer(issuer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := newMemoryAuthStore()
	service, err := New(ctx, Options{Store: st, Config: config.Auth{
		CookieSecure: insecureCookieConfig(), OIDC: config.OIDC{
			IssuerURL: issuer, ClientID: clientID, ClientSecret: "client-secret",
			RedirectURL: "http://127.0.0.1/callback",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.LoadAndSave(service.Handler()))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	start, err := client.Get(server.URL + "/api/auth/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	if start.StatusCode != http.StatusFound {
		t.Fatalf("OIDC start = %d: %s", start.StatusCode, readBody(start))
	}
	location, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	challenge = location.Query().Get("code_challenge")
	nonce = location.Query().Get("nonce")
	if location.Query().Get("code_challenge_method") != "S256" || challenge == "" || nonce == "" {
		t.Fatalf("OIDC authorization URL missing nonce/PKCE: %s", location)
	}
	st.mu.Lock()
	for _, session := range st.sessions {
		remaining := time.Until(session.expiry)
		if remaining < 9*time.Minute || remaining > 11*time.Minute {
			st.mu.Unlock()
			t.Fatalf("anonymous OIDC flow lifetime = %v, want about %v", remaining, oidcFlowTTL)
		}
	}
	st.mu.Unlock()
	callbackURL := server.URL + "/api/auth/oidc/callback?code=valid-code&state=" + url.QueryEscape(location.Query().Get("state"))
	callback, err := client.Get(callbackURL)
	if err != nil {
		t.Fatal(err)
	}
	if callback.StatusCode != http.StatusSeeOther || callback.Header.Get("Location") != "/" {
		t.Fatalf("OIDC callback = %d location=%q body=%s", callback.StatusCode, callback.Header.Get("Location"), readBody(callback))
	}
	status := getStatus(t, client, server.URL)
	if !status.Authenticated || status.SetupRequired || status.User == nil ||
		status.User.Email != "oidc@example.com" || !status.User.IsAdmin || status.CSRFToken == "" {
		t.Fatalf("OIDC session status = %+v", status)
	}
	preservedCSRF := status.CSRFToken
	preserved, err := client.Get(server.URL + "/api/auth/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	if preserved.StatusCode != http.StatusFound {
		t.Fatalf("authenticated OIDC start = %d", preserved.StatusCode)
	}
	_ = preserved.Body.Close()
	status = getStatus(t, client, server.URL)
	if !status.Authenticated || status.CSRFToken != preservedCSRF {
		t.Fatalf("OIDC start destroyed authenticated session: %+v", status)
	}
	replay, err := client.Get(callbackURL)
	if err != nil {
		t.Fatal(err)
	}
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatalf("OIDC callback replay = %d, want 400", replay.StatusCode)
	}
	_ = replay.Body.Close()
	rateLimited := false
	for range 20 {
		response, err := client.Get(server.URL + "/api/auth/oidc/start")
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests {
			rateLimited = true
			break
		}
	}
	if !rateLimited {
		t.Fatal("OIDC starts were not rate limited")
	}
}

func TestArgonSemaphoreBoundsConcurrentWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := New(ctx, Options{
		Store: newMemoryAuthStore(), Config: config.Auth{CookieSecure: insecureCookieConfig()},
		ArgonConcurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	const callers = 32
	start := make(chan struct{})
	release := make(chan struct{})
	var attempted sync.WaitGroup
	var finished sync.WaitGroup
	attempted.Add(callers)
	finished.Add(callers)
	acquired := make(chan struct{}, callers)
	for range callers {
		go func() {
			defer finished.Done()
			<-start
			if !service.acquireArgon() {
				attempted.Done()
				return
			}
			acquired <- struct{}{}
			attempted.Done()
			<-release
			service.releaseArgon()
		}()
	}
	close(start)
	attempted.Wait()
	if got := len(acquired); got != 2 {
		t.Fatalf("concurrent Argon work = %d, want hard cap 2", got)
	}
	close(release)
	finished.Wait()
}

func serviceRequestContext(t *testing.T, service *Service, request *http.Request) *http.Request {
	t.Helper()
	var captured *http.Request
	service.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r })).ServeHTTP(httptest.NewRecorder(), request)
	if captured == nil {
		t.Fatal("session middleware did not invoke handler")
	}
	return captured
}

func getStatus(t *testing.T, client *http.Client, base string) statusResponse {
	t.Helper()
	response, err := client.Get(base + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint = %d: %s", response.StatusCode, readBody(response))
	}
	var status statusResponse
	decodeResponse(t, response, &status)
	return status
}

func request(t *testing.T, client *http.Client, method, target, body, csrf, bearer string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(response *http.Response) string {
	defer func() {
		_ = response.Body.Close()
	}()
	var value any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return err.Error()
	}
	return fmt.Sprint(value)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	value, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
