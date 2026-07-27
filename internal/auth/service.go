// Package auth implements Epic 9 browser sessions, local login, hashed API
// keys, and an OIDC bridge over the DB-backed user model.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/text/unicode/norm"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	sessionUserID = "user_id"
	sessionCSRF   = "csrf"
	legacyKeyID   = "legacy-config"
)

// Options contains the dependencies required by the cohesive auth HTTP
// surface. HTTPClient is optional and primarily supports private CAs/test IdPs.
type Options struct {
	Config     config.Auth
	Store      store.AuthStore
	HTTPClient *http.Client
	Now        func() time.Time
	// ArgonConcurrency bounds memory-hard password work. Zero selects the
	// production default of four concurrent hashes.
	ArgonConcurrency int
	// Audit records authentication and key-lifecycle events (T10.1). Nil
	// disables recording; failures inside the callback must not fail requests.
	Audit func(ctx context.Context, event store.AuditEvent)
}

// Service is safe for concurrent HTTP use.
type Service struct {
	store    store.AuthStore
	cfg      config.Auth
	sessions *scs.SessionManager
	now      func() time.Time

	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauth       *oauth2.Config
	httpClient  *http.Client
	dummyHash   string
	loginLimits *loginLimiter
	oidcLimits  *loginLimiter
	clientIPs   clientIPResolver
	argonSlots  chan struct{}
	auditFn     func(ctx context.Context, event store.AuditEvent)

	setupMu    sync.RWMutex
	setupToken string
}

// Principal is attached to authenticated request contexts. User is nil only
// for the migration-only auth.api_key principal.
type Principal struct {
	User               *store.User
	APIKeyID           string
	APIKeyCapabilities []store.APIKeyCapability
	AuthMethod         string
	IsAdmin            bool
}

type principalContextKey struct{}

// HasAPIKeyCapability reports authority carried by the authenticated named
// bearer key. Browser sessions and the migration-only legacy key never emulate
// a bearer capability.
func (principal Principal) HasAPIKeyCapability(capability store.APIKeyCapability) bool {
	if principal.AuthMethod != "api_key" || principal.APIKeyID == "" ||
		principal.APIKeyID == legacyKeyID {
		return false
	}
	for _, current := range principal.APIKeyCapabilities {
		if current == capability {
			return true
		}
	}
	return false
}

// PrincipalFromContext returns the identity established by Require.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func New(ctx context.Context, options Options) (*Service, error) {
	if options.Store == nil {
		return nil, errors.New("auth: store is required")
	}
	argonConcurrency := options.ArgonConcurrency
	if argonConcurrency == 0 {
		argonConcurrency = 4
	}
	if argonConcurrency < 1 || argonConcurrency > 16 {
		return nil, errors.New("auth: ArgonConcurrency must be from 1 through 16")
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	dummyHash, err := hashPassword("phebs-dummy-password-never-valid")
	if err != nil {
		return nil, fmt.Errorf("auth: prepare password verifier: %w", err)
	}
	clientIPs, err := newClientIPResolver(options.Config.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("auth: trusted proxies: %w", err)
	}
	s := &Service{
		store: options.Store, cfg: options.Config, now: now,
		httpClient: options.HTTPClient, dummyHash: dummyHash,
		loginLimits: newLoginLimiter(8, 5*time.Minute, 4096),
		oidcLimits:  newLoginLimiter(12, 5*time.Minute, 4096),
		clientIPs:   clientIPs,
		argonSlots:  make(chan struct{}, argonConcurrency),
		auditFn:     options.Audit,
	}

	s.sessions = scs.New()
	s.sessions.Store = sessionStore{store: options.Store}
	s.sessions.HashTokenInStore = true
	s.sessions.Lifetime = options.Config.SessionDuration()
	s.sessions.IdleTimeout = 30 * time.Minute
	s.sessions.Cookie.Name = "phebs_session"
	s.sessions.Cookie.HttpOnly = true
	s.sessions.Cookie.Secure = options.Config.SecureCookies()
	s.sessions.Cookie.SameSite = http.SameSiteLaxMode
	s.sessions.Cookie.Path = "/"
	s.sessions.Cookie.Persist = true

	legacyHash := ""
	if options.Config.APIKey != "" {
		legacyHash = bearerHash(options.Config.APIKey)
	}
	if err := options.Store.SetLegacyAPIKey(ctx, legacyHash, now()); err != nil {
		return nil, fmt.Errorf("auth: import legacy API key: %w", err)
	}
	if err := s.bootstrapLocalUser(ctx); err != nil {
		return nil, err
	}
	if err := s.configureOIDC(ctx); err != nil {
		return nil, err
	}
	stats, err := options.Store.AuthStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: inspect users: %w", err)
	}
	if stats.Users == 0 && !stats.SetupComplete && s.provider == nil {
		s.setupToken, err = randomToken(32)
		if err != nil {
			return nil, fmt.Errorf("auth: generate setup token: %w", err)
		}
	}
	if _, err := options.Store.DeleteExpiredAuthSessions(ctx, now()); err != nil {
		return nil, fmt.Errorf("auth: clean expired sessions: %w", err)
	}
	go s.cleanExpiredSessions(ctx)
	return s, nil
}

func (s *Service) acquireArgon() bool {
	select {
	case s.argonSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) releaseArgon() {
	<-s.argonSlots
}

func (s *Service) configureOIDC(ctx context.Context) error {
	if s.cfg.OIDC.IsZero() {
		return nil
	}
	discoveryCtx := ctx
	if s.httpClient != nil {
		discoveryCtx = oidc.ClientContext(discoveryCtx, s.httpClient)
	}
	provider, err := oidc.NewProvider(discoveryCtx, s.cfg.OIDC.IssuerURL)
	if err != nil {
		return fmt.Errorf("auth: discover OIDC provider: %w", err)
	}
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	seen := map[string]bool{"openid": true, "profile": true, "email": true}
	for _, scope := range s.cfg.OIDC.Scopes {
		if !seen[scope] {
			scopes = append(scopes, scope)
			seen[scope] = true
		}
	}
	s.provider = provider
	s.verifier = provider.Verifier(&oidc.Config{ClientID: s.cfg.OIDC.ClientID})
	s.oauth = &oauth2.Config{
		ClientID: s.cfg.OIDC.ClientID, ClientSecret: s.cfg.OIDC.ClientSecret,
		RedirectURL: s.cfg.OIDC.RedirectURL, Endpoint: provider.Endpoint(), Scopes: scopes,
	}
	return nil
}

func (s *Service) bootstrapLocalUser(ctx context.Context) error {
	bootstrap := s.cfg.BootstrapUser
	if bootstrap.IsZero() {
		return nil
	}
	email, err := normalizeEmail(bootstrap.Email)
	if err != nil {
		return fmt.Errorf("auth: bootstrap email: %w", err)
	}
	hash, err := hashPassword(bootstrap.Password)
	if err != nil {
		return fmt.Errorf("auth: bootstrap password: %w", err)
	}
	stats, err := s.store.AuthStats(ctx)
	if err != nil {
		return fmt.Errorf("auth: inspect bootstrap user: %w", err)
	}
	if stats.Users > 0 {
		if _, err := s.store.GetUserByEmail(ctx, email); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return errors.New("auth: bootstrap_user is only valid for the first user; configured email is absent")
			}
			return fmt.Errorf("auth: inspect bootstrap user: %w", err)
		}
		return nil
	}
	id, err := randomToken(16)
	if err != nil {
		return fmt.Errorf("auth: generate bootstrap user id: %w", err)
	}
	_, err = s.store.CreateFirstUser(ctx, store.User{
		ID: id, Email: strings.TrimSpace(bootstrap.Email), NormalizedEmail: email,
		DisplayName: cleanDisplayName(bootstrap.DisplayName), PasswordHash: hash,
		IsAdmin: true, CreatedAt: s.now(),
	})
	if err != nil {
		return fmt.Errorf("auth: create bootstrap user: %w", err)
	}
	return nil
}

// SetupToken returns the ephemeral token required by POST /api/auth/setup.
// The caller should log it to the local operator once; it is cleared after the
// first user is created and is never persisted.
func (s *Service) SetupToken() string {
	s.setupMu.RLock()
	defer s.setupMu.RUnlock()
	return s.setupToken
}

func (s *Service) consumeSetupToken(candidate string) bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupToken != "" && secureEqual(s.setupToken, candidate)
}

func (s *Service) finishSetup() {
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
}

func (s *Service) cleanExpiredSessions(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.store.DeleteExpiredAuthSessions(ctx, s.now())
		}
	}
}

// audit records one auth-surface event (T10.1). The source IP uses the same
// trusted-proxy resolution as the login limiter buckets. user is nil for
// unauthenticated outcomes such as failed logins.
func (s *Service) audit(r *http.Request, user *store.User, action, target string, status int) {
	if s.auditFn == nil {
		return
	}
	event := store.AuditEvent{Action: action, Target: target, Status: status,
		SourceIP: s.clientIPs.clientKey(r)}
	if user != nil {
		event.ActorID, event.ActorEmail, event.AuthMethod = user.ID, user.Email, "session"
	}
	s.auditFn(r.Context(), event)
}

func normalizeEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 254 || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("invalid email address")
	}
	address, err := mail.ParseAddress(raw)
	if err != nil || address.Address != raw {
		return "", errors.New("invalid email address")
	}
	return strings.ToLower(norm.NFC.String(address.Address)), nil
}

func cleanDisplayName(raw string) string {
	raw = strings.TrimSpace(norm.NFC.String(raw))
	runes := []rune(raw)
	if len(runes) > 100 {
		return string(runes[:100])
	}
	return raw
}
