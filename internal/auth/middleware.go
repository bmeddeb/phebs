package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrInvalidCSRF     = errors.New("invalid CSRF token")
)

// LoadAndSave must wrap the top-level mux so sessions established on the auth
// routes are available to protected API and MCP handlers too.
func (s *Service) LoadAndSave(next http.Handler) http.Handler {
	return s.sessions.LoadAndSave(next)
}

// Authenticate accepts either one Bearer API key or the SCS browser session.
// An explicitly malformed/invalid Authorization header never falls back to a
// cookie, preventing ambiguous credential selection.
func (s *Service) Authenticate(r *http.Request) (Principal, error) {
	values := r.Header.Values("Authorization")
	if len(values) > 0 {
		if len(values) != 1 {
			return Principal{}, ErrUnauthenticated
		}
		fields := strings.Fields(values[0])
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 512 {
			return Principal{}, ErrUnauthenticated
		}
		return s.authenticateBearer(r.Context(), fields[1])
	}

	userID := s.sessions.GetString(r.Context(), sessionUserID)
	if userID == "" {
		return Principal{}, ErrUnauthenticated
	}
	user, err := s.store.GetUserByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, fmt.Errorf("load session user: %w", err)
	}
	if user.Disabled {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{User: user, AuthMethod: "session", IsAdmin: user.IsAdmin}, nil
}

func (s *Service) authenticateBearer(ctx context.Context, token string) (Principal, error) {
	id := legacyKeyID
	if strings.HasPrefix(token, "phebs_") {
		rest := strings.TrimPrefix(token, "phebs_")
		var ok bool
		id, _, ok = strings.Cut(rest, ".")
		if !ok || id == "" || len(id) > 64 {
			return Principal{}, ErrUnauthenticated
		}
	}
	now := s.now()
	key, err := s.store.GetAPIKey(ctx, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Principal{}, fmt.Errorf("load API key: %w", err)
	}
	valid := err == nil && key.RevokedAt == nil &&
		(key.ExpiresAt == nil || key.ExpiresAt.After(now)) && secureEqual(key.Hash, bearerHash(token))
	// A migration key may itself use the new-looking phebs_<id>.<secret>
	// shape. Fall back to its reserved row only when the parsed key did not
	// authenticate; an invalid explicit credential still never falls back to a
	// browser session.
	if !valid && id != legacyKeyID {
		key, err = s.store.GetAPIKey(ctx, legacyKeyID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return Principal{}, fmt.Errorf("load legacy API key: %w", err)
		}
		valid = err == nil && key.RevokedAt == nil &&
			(key.ExpiresAt == nil || key.ExpiresAt.After(now)) && secureEqual(key.Hash, bearerHash(token))
	}
	if !valid {
		return Principal{}, ErrUnauthenticated
	}
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= 5*time.Minute {
		if err := s.store.TouchAPIKey(ctx, key.ID, now); err != nil {
			return Principal{}, fmt.Errorf("touch API key: %w", err)
		}
	}
	if key.UserID == "" && key.ID == legacyKeyID {
		return Principal{APIKeyID: key.ID, AuthMethod: "api_key", IsAdmin: true}, nil
	}
	user, err := s.store.GetUserByID(ctx, key.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, fmt.Errorf("load API key user: %w", err)
	}
	if user.Disabled {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{User: user, APIKeyID: key.ID, AuthMethod: "api_key", IsAdmin: user.IsAdmin}, nil
}

// Require establishes a Principal and enforces CSRF on unsafe browser-session
// requests. Bearer clients are not vulnerable to ambient-cookie CSRF.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.Authenticate(r)
		if err != nil {
			status := http.StatusInternalServerError
			message := "authentication unavailable"
			if errors.Is(err, ErrUnauthenticated) {
				status = http.StatusUnauthorized
				message = "authentication required"
			}
			writeError(w, status, message)
			return
		}
		if principal.AuthMethod == "session" && isUnsafe(r.Method) && !s.validCSRF(r) {
			writeError(w, http.StatusForbidden, ErrInvalidCSRF.Error())
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Identify attaches a principal when the request authenticates successfully,
// but preserves public access when it does not. It is intentionally narrower
// than Require: public handlers must still decide which response fields are
// safe without a principal, and invalid credentials never reveal protected
// state through a distinguishable response.
func (s *Service) Identify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.Authenticate(r)
		if err == nil {
			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) validCSRF(r *http.Request) bool {
	want := s.sessions.GetString(r.Context(), sessionCSRF)
	got := r.Header.Get("X-CSRF-Token")
	return want != "" && len(got) <= 256 && secureEqual(want, got)
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}
