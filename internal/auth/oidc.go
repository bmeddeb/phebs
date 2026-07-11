package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	oidcStateKey    = "oidc_state"
	oidcNonceKey    = "oidc_nonce"
	oidcVerifierKey = "oidc_verifier"
	oidcStartedKey  = "oidc_started"
	oidcFlowTTL     = 10 * time.Minute
)

func (s *Service) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		writeError(w, http.StatusNotFound, "OIDC login is not configured")
		return
	}
	if !s.oidcLimits.consume(s.clientIPs.clientKey(r), s.now()) {
		w.Header().Set("Retry-After", "300")
		writeError(w, http.StatusTooManyRequests, "too many OIDC login attempts")
		return
	}
	state, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start OIDC login")
		return
	}
	nonce, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start OIDC login")
		return
	}
	verifier := oauth2.GenerateVerifier()
	if s.sessions.GetString(r.Context(), sessionUserID) == "" {
		if err := s.sessions.RenewToken(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "could not start OIDC login")
			return
		}
		if err := s.sessions.Clear(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "could not start OIDC login")
			return
		}
		s.sessions.SetDeadline(r.Context(), s.now().Add(oidcFlowTTL))
	}
	s.sessions.Put(r.Context(), oidcStateKey, state)
	s.sessions.Put(r.Context(), oidcNonceKey, nonce)
	s.sessions.Put(r.Context(), oidcVerifierKey, verifier)
	s.sessions.Put(r.Context(), oidcStartedKey, s.now().Unix())
	location := s.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Service) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		writeError(w, http.StatusNotFound, "OIDC login is not configured")
		return
	}
	state := s.sessions.PopString(r.Context(), oidcStateKey)
	nonce := s.sessions.PopString(r.Context(), oidcNonceKey)
	verifier := s.sessions.PopString(r.Context(), oidcVerifierKey)
	started, _ := s.sessions.Pop(r.Context(), oidcStartedKey).(int64)
	providedState := r.URL.Query().Get("state")
	if state == "" || nonce == "" || verifier == "" || len(providedState) > 256 || !secureEqual(state, providedState) ||
		started == 0 || s.now().Sub(time.Unix(started, 0)) < 0 || s.now().Sub(time.Unix(started, 0)) > oidcFlowTTL {
		writeError(w, http.StatusBadRequest, "invalid or expired OIDC state")
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		writeError(w, http.StatusUnauthorized, "OIDC provider rejected login")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" || len(code) > 2048 {
		writeError(w, http.StatusBadRequest, "missing OIDC authorization code")
		return
	}
	ctx := r.Context()
	if s.httpClient != nil {
		ctx = oidc.ClientContext(ctx, s.httpClient)
	}
	token, err := s.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "OIDC token exchange failed")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		writeError(w, http.StatusUnauthorized, "OIDC response did not include an ID token")
		return
	}
	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil || idToken.Subject == "" || !secureEqual(idToken.Nonce, nonce) {
		writeError(w, http.StatusUnauthorized, "OIDC ID token verification failed")
		return
	}
	if idToken.AccessTokenHash != "" && idToken.VerifyAccessToken(token.AccessToken) != nil {
		writeError(w, http.StatusUnauthorized, "OIDC access token verification failed")
		return
	}
	var claims struct {
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil || !claims.EmailVerified {
		writeError(w, http.StatusUnauthorized, "OIDC requires a verified email claim")
		return
	}
	normalizedEmail, err := normalizeEmail(claims.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "OIDC returned an invalid email claim")
		return
	}
	displayName := claims.Name
	if strings.TrimSpace(displayName) == "" {
		displayName = claims.PreferredUsername
	}
	user, err := s.store.UpsertOIDCUser(r.Context(), s.cfg.OIDC.IssuerURL, idToken.Subject,
		strings.TrimSpace(claims.Email), normalizedEmail, cleanDisplayName(displayName), claims.EmailVerified)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "OIDC identity conflicts with an existing account")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not persist OIDC identity")
		return
	}
	if user.Disabled {
		writeError(w, http.StatusForbidden, "user is disabled")
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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
