package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/store"
)

type userResponse struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	IsAdmin     bool   `json:"is_admin"`
}

type statusResponse struct {
	Authenticated   bool          `json:"authenticated"`
	AuthRequired    bool          `json:"auth_required"`
	SetupRequired   bool          `json:"setup_required"`
	OIDCEnabled     bool          `json:"oidc_enabled"`
	PasswordEnabled bool          `json:"password_enabled"`
	User            *userResponse `json:"user,omitempty"`
	CSRFToken       string        `json:"csrf_token,omitempty"`
}

type keyResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// Handler exposes the complete Epic 9 auth route set. It must be mounted
// beneath LoadAndSave so SCS has loaded request context before a route runs.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/status", s.handleStatus)
	mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.Handle("POST /api/auth/logout", s.Require(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /api/auth/keys", s.Require(http.HandlerFunc(s.handleListKeys)))
	mux.Handle("POST /api/auth/keys", s.Require(http.HandlerFunc(s.handleCreateKey)))
	mux.Handle("DELETE /api/auth/keys/{id}", s.Require(http.HandlerFunc(s.handleDeleteKey)))
	mux.HandleFunc("GET /api/auth/oidc/start", s.handleOIDCStart)
	mux.HandleFunc("GET /api/auth/oidc/callback", s.handleOIDCCallback)
	return mux
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	principal, err := s.Authenticate(r)
	if err != nil && !errors.Is(err, ErrUnauthenticated) {
		writeError(w, http.StatusInternalServerError, "authentication unavailable")
		return
	}
	if errors.Is(err, ErrUnauthenticated) && r.Header.Get("Authorization") != "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := s.writeStatus(w, r, principal, err == nil); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication unavailable")
	}
}

func (s *Service) writeStatus(w http.ResponseWriter, r *http.Request, principal Principal, authenticated bool) error {
	stats, err := s.store.AuthStats(r.Context())
	if err != nil {
		return err
	}
	response := statusResponse{
		Authenticated: authenticated, AuthRequired: true,
		SetupRequired: stats.Users == 0 && !stats.SetupComplete && s.provider == nil,
		OIDCEnabled:   s.provider != nil, PasswordEnabled: stats.PasswordUsers > 0,
	}
	if principal.User != nil {
		response.User = publicUser(principal.User)
	}
	if authenticated && principal.AuthMethod == "session" {
		response.CSRFToken = s.sessions.GetString(r.Context(), sessionCSRF)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
	return nil
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	key := s.clientIPs.clientKey(r)
	reservation, ok := s.loginLimits.reserve(key, s.now())
	if !ok {
		w.Header().Set("Retry-After", "300")
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	defer reservation.cancel()
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	email, emailErr := normalizeEmail(input.Email)
	user, findErr := s.store.GetUserByEmail(r.Context(), email)
	hash := s.dummyHash
	if emailErr == nil && findErr == nil && user != nil && user.PasswordHash != "" {
		hash = user.PasswordHash
	}
	if !s.acquireArgon() {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "password verification is busy")
		return
	}
	defer s.releaseArgon()
	valid := verifyPassword(hash, input.Password)
	if emailErr != nil || findErr != nil || user == nil || user.Disabled || user.PasswordHash == "" || !valid {
		if findErr != nil && !errors.Is(findErr, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "authentication unavailable")
			return
		}
		reservation.fail(s.now())
		s.audit(r, nil, "auth.login", email, http.StatusUnauthorized)
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err := s.store.MarkUserLogin(r.Context(), user.ID, s.now()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record login")
		return
	}
	if err := s.startSession(r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}
	reservation.success()
	s.audit(r, user, "auth.login", "", http.StatusOK)
	if err := s.writeStatus(w, r, Principal{User: user, AuthMethod: "session", IsAdmin: user.IsAdmin}, true); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication unavailable")
	}
}

func (s *Service) handleSetup(w http.ResponseWriter, r *http.Request) {
	key := s.clientIPs.clientKey(r)
	reservation, ok := s.loginLimits.reserve(key, s.now())
	if !ok {
		w.Header().Set("Retry-After", "300")
		writeError(w, http.StatusTooManyRequests, "too many setup attempts")
		return
	}
	defer reservation.cancel()
	var input struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		SetupToken  string `json:"setup_token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.consumeSetupToken(input.SetupToken) {
		reservation.fail(s.now())
		writeError(w, http.StatusUnauthorized, "invalid setup token")
		return
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		reservation.fail(s.now())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.acquireArgon() {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "password hashing is busy")
		return
	}
	hash, err := hashPassword(input.Password)
	s.releaseArgon()
	if err != nil {
		reservation.fail(s.now())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := randomToken(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}
	user, err := s.store.CreateFirstUser(r.Context(), store.User{
		ID: id, Email: strings.TrimSpace(input.Email), NormalizedEmail: email,
		DisplayName: cleanDisplayName(input.DisplayName), PasswordHash: hash,
		IsAdmin: true, CreatedAt: s.now(),
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.finishSetup()
			writeError(w, http.StatusConflict, "setup has already completed")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}
	s.finishSetup()
	reservation.success()
	if err := s.startSession(r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}
	s.audit(r, user, "auth.setup", "", http.StatusOK)
	if err := s.writeStatus(w, r, Principal{User: user, AuthMethod: "session", IsAdmin: true}, true); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication unavailable")
	}
}

func (s *Service) startSession(r *http.Request, user *store.User) error {
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		return err
	}
	if err := s.sessions.Clear(r.Context()); err != nil {
		return err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return err
	}
	s.sessions.Put(r.Context(), sessionUserID, user.ID)
	s.sessions.Put(r.Context(), sessionCSRF, csrf)
	return nil
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Destroy(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not end session")
		return
	}
	principal, _ := PrincipalFromContext(r.Context())
	s.audit(r, principal.User, "auth.logout", "", http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleListKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := requestUser(w, r)
	if !ok {
		return
	}
	keys, err := s.store.ListAPIKeys(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list API keys")
		return
	}
	response := make([]keyResponse, 0, len(keys))
	for i := range keys {
		response = append(response, publicKey(&keys[i]))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"keys": response})
}

func (s *Service) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	user, ok := requestUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		writeError(w, http.StatusBadRequest, "name must be 1 to 80 characters without control characters")
		return
	}
	existing, err := s.store.ListAPIKeys(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not inspect API keys")
		return
	}
	if len(existing) >= 100 {
		writeError(w, http.StatusConflict, "API key limit reached")
		return
	}
	for range 3 {
		id, idErr := randomToken(12)
		secret, secretErr := randomToken(32)
		if idErr != nil || secretErr != nil {
			writeError(w, http.StatusInternalServerError, "could not create API key")
			return
		}
		token := "phebs_" + id + "." + secret
		key, createErr := s.store.CreateAPIKey(r.Context(), store.APIKey{
			ID: id, UserID: user.ID, Name: name, Prefix: "phebs_" + id[:8],
			Hash: bearerHash(token), CreatedAt: s.now(),
		})
		if createErr == nil {
			s.audit(r, user, "auth.key.create", key.ID, http.StatusCreated)
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusCreated, map[string]any{"key": publicKey(key), "token": token})
			return
		}
		if !errors.Is(createErr, store.ErrConflict) {
			writeError(w, http.StatusInternalServerError, "could not create API key")
			return
		}
	}
	writeError(w, http.StatusInternalServerError, "could not allocate API key")
}

func (s *Service) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	user, ok := requestUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" || len(id) > 64 {
		writeError(w, http.StatusNotFound, "API key not found")
		return
	}
	if err := s.store.RevokeAPIKey(r.Context(), id, user.ID, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "API key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not revoke API key")
		return
	}
	s.audit(r, user, "auth.key.revoke", id, http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}

func requestUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.User == nil || principal.AuthMethod != "session" {
		writeError(w, http.StatusForbidden, "API key management requires a browser session")
		return nil, false
	}
	return principal.User, true
}

func publicUser(user *store.User) *userResponse {
	return &userResponse{ID: user.ID, Email: user.Email, DisplayName: user.DisplayName, IsAdmin: user.IsAdmin}
}

func publicKey(key *store.APIKey) keyResponse {
	return keyResponse{ID: key.ID, Name: key.Name, Prefix: key.Prefix, CreatedAt: key.CreatedAt,
		LastUsedAt: key.LastUsedAt, ExpiresAt: key.ExpiresAt}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid JSON body: expected one object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
