// Package config defines and loads the phebs YAML config schema (T1.1).
//
// The schema is phebs' own (PLAN §1): upstream's JSON schemas are never
// copied. See docs/config.example.yaml for the annotated reference.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      Server       `yaml:"server"`
	Auth        Auth         `yaml:"auth"`
	Sync        Sync         `yaml:"sync"`
	Webhook     Webhook      `yaml:"webhook"`
	Connections []Connection `yaml:"connections"`
	// Contexts are named repo sets for `context:<name>` search filters
	// (T8.1): name → glob patterns matched against full repo names
	// ("github.com/acme/api-*"; `*` does not cross `/`).
	Contexts map[string][]string `yaml:"contexts"`
}

type Sync struct {
	// CleanupOrphans deletes repo rows and mirrors no connection claims
	// (mirrors upstream's isAutoCleanupDisabled semantics, default off).
	CleanupOrphans bool `yaml:"cleanup_orphans"`
	// PollInterval is the job-runner poll cadence (Go duration, default
	// "15s"). Lower it when watch mode should feel instant.
	PollInterval string `yaml:"poll_interval"`
	// ResyncInterval re-syncs remote connections on a cadence (T7.5).
	// Go duration, default "1h"; "0" disables.
	ResyncInterval string `yaml:"resync_interval"`
}

// Webhook configures POST /api/webhook (T7.4).
type Webhook struct {
	// Secret verifies X-Hub-Signature-256 payload signatures; empty leaves
	// the endpoint disabled. ${ENV} references are expanded.
	Secret string `yaml:"secret"`
}

// Interval returns the parsed poll cadence; validation guarantees the
// string parses, so the default only covers the empty case.
func (s Sync) Interval() time.Duration {
	if d, err := time.ParseDuration(s.PollInterval); err == nil && d > 0 {
		return d
	}
	return 15 * time.Second
}

// ResyncEvery returns the parsed re-sync cadence; 0 means disabled.
func (s Sync) ResyncEvery() time.Duration {
	if s.ResyncInterval == "" {
		return time.Hour
	}
	if s.ResyncInterval == "0" {
		return 0
	}
	d, err := time.ParseDuration(s.ResyncInterval)
	if err != nil || d < 0 { // validation guarantees this branch is dead
		return time.Hour
	}
	return d
}

type Server struct {
	// Addr is the listen address. Default "127.0.0.1:3070"; deployments that
	// intentionally expose phebs should configure a trusted TLS proxy.
	Addr string `yaml:"addr"`
	// DataDir holds all state: shards at <DataDir>/index, bare repos at
	// <DataDir>/repos/<host>/<path>.git, the embedded DB at <DataDir>/db.
	// Default "~/.phebs".
	DataDir string `yaml:"data_dir"`
}

type Auth struct {
	// APIKey is the legacy bearer token. Epic 9 imports only its hash into the
	// api_key table; new clients should create individually revocable keys.
	APIKey string `yaml:"api_key"`
	// CookieSecure controls the Secure attribute on browser session cookies.
	// It defaults to true. Plain-HTTP local development must explicitly opt
	// out; the default listener may be exposed beyond loopback.
	CookieSecure *bool `yaml:"cookie_secure"`
	// SessionLifetime is an absolute SCS session lifetime (default 12h).
	SessionLifetime string `yaml:"session_lifetime"`
	// TrustedProxies contains CIDRs for reverse-proxy hops. An
	// X-Forwarded-For chain is considered only when the direct peer is in one
	// of these networks; forwarded headers from every other peer are ignored.
	TrustedProxies []string      `yaml:"trusted_proxies"`
	BootstrapUser  BootstrapUser `yaml:"bootstrap_user"`
	OIDC           OIDC          `yaml:"oidc"`
}

// BootstrapUser creates the first local administrator, once. The password is
// environment-expandable and is hashed before the user is persisted.
type BootstrapUser struct {
	Email       string `yaml:"email"`
	DisplayName string `yaml:"display_name"`
	Password    string `yaml:"password"`
}

// IsZero reports whether no bootstrap user was configured.
func (u BootstrapUser) IsZero() bool { return u == BootstrapUser{} }

// OIDC configures one OpenID Connect provider. Provider discovery, ID-token
// verification, nonce checking, and PKCE are handled by coreos/go-oidc.
type OIDC struct {
	IssuerURL    string   `yaml:"issuer_url"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
}

// IsZero reports whether OIDC login is disabled.
func (o OIDC) IsZero() bool {
	return o.IssuerURL == "" && o.ClientID == "" && o.ClientSecret == "" &&
		o.RedirectURL == "" && len(o.Scopes) == 0
}

// SessionDuration returns the validated absolute session lifetime.
func (a Auth) SessionDuration() time.Duration {
	if a.SessionLifetime == "" {
		return 12 * time.Hour
	}
	d, err := time.ParseDuration(a.SessionLifetime)
	if err != nil {
		return 12 * time.Hour
	}
	return d
}

// SecureCookies reports the session cookie transport policy. Secure is the
// fail-closed default; local HTTP development explicitly opts out.
func (a Auth) SecureCookies() bool {
	return a.CookieSecure == nil || *a.CookieSecure
}

// Connection declares one source of repos to sync (Epic 2 consumes these).
type Connection struct {
	Name string `yaml:"name"` // required, unique, [a-z0-9-]+
	Type string `yaml:"type"` // "github", "gitlab", "gitea", or "git"

	// code-host types. Token is a PAT; ${ENV} references are expanded so
	// secrets stay out of the file.
	Token   string   `yaml:"token"`
	Orgs    []string `yaml:"orgs"`   // github/gitea: all repos of each org
	Groups  []string `yaml:"groups"` // gitlab: all projects of each group (subgroups included)
	Users   []string `yaml:"users"`
	Repos   []string `yaml:"repos"` // explicit "owner/name" (gitlab: full project path)
	Exclude Exclude  `yaml:"exclude"`

	// github only: authenticate as a GitHub App installation instead of a
	// PAT (higher rate limits, per-install scoping).
	App GitHubApp `yaml:"app"`

	// git: clone URL of a single repository (plain absolute paths and
	// file:// URLs point at local repos). gitlab: optional http(s) base URL
	// of a self-hosted instance (default https://gitlab.com). gitea:
	// required http(s) base URL.
	URL string `yaml:"url"`
	// HTTPAuth supplies Basic authentication for an HTTP(S) generic-git
	// connection. Credentials are injected into each git invocation and are
	// never embedded in URL or persisted in the mirror config.
	HTTPAuth HTTPAuth `yaml:"http_auth"`
	// Watch (local git only): poll the repo's HEAD and re-sync/re-index
	// when it moves — live search over a working repo.
	Watch bool `yaml:"watch"`
}

// HTTPAuth is the username/password pair used by a generic HTTP(S) Git
// remote. Both fields support environment expansion.
type HTTPAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// IsZero reports whether no HTTP authentication was configured.
func (a HTTPAuth) IsZero() bool { return a == HTTPAuth{} }

// IsLocalURL reports whether a git connection URL points at a repo on this
// machine (plain absolute path or file://).
func IsLocalURL(url string) bool {
	return strings.HasPrefix(url, "/") || strings.HasPrefix(url, "file://")
}

// GitHubApp authenticates a github connection as an App installation.
// Exactly one of PrivateKeyPath / PrivateKey (inline PEM, ${ENV} expanded)
// is required.
type GitHubApp struct {
	ID             int64  `yaml:"id"`
	InstallationID int64  `yaml:"installation_id"`
	PrivateKeyPath string `yaml:"private_key_path"`
	PrivateKey     string `yaml:"private_key"`
}

// IsZero reports whether no app block was configured.
func (a GitHubApp) IsZero() bool { return a == GitHubApp{} }

// Exclude filters repos out of a github connection's listing.
type Exclude struct {
	Archived bool     `yaml:"archived"`
	Forks    bool     `yaml:"forks"`
	Repos    []string `yaml:"repos"` // glob patterns on "owner/name"
}

func (e Exclude) isZero() bool {
	return !e.Archived && !e.Forks && len(e.Repos) == 0
}

var nameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes and validates config bytes. Errors carry YAML line numbers:
// syntax/type/unknown-field errors from the strict decoder, semantic errors
// from the node tree.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty config")
		}
		return nil, err
	}
	var doc yaml.Node
	// cannot fail: the strict decode above already parsed the same bytes
	_ = yaml.Unmarshal(data, &doc)
	if err := cfg.validate(connectionLines(&doc)); err != nil {
		return nil, err
	}
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// connectionLines returns the YAML line of each entry under "connections".
func connectionLines(doc *yaml.Node) []int {
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "connections" {
			seq := root.Content[i+1]
			lines := make([]int, len(seq.Content))
			for j, item := range seq.Content {
				lines[j] = item.Line
			}
			return lines
		}
	}
	return nil
}

func (c *Config) validate(lines []int) error {
	var errs []error
	fail := func(i int, format string, args ...any) {
		line := 0
		if i < len(lines) {
			line = lines[i]
		}
		errs = append(errs, fmt.Errorf("line %d: connections[%d]: %s", line, i, fmt.Sprintf(format, args...)))
	}

	if c.Sync.PollInterval != "" {
		if d, err := time.ParseDuration(c.Sync.PollInterval); err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("sync.poll_interval %q: not a positive Go duration", c.Sync.PollInterval))
		}
	}
	if ri := c.Sync.ResyncInterval; ri != "" && ri != "0" {
		if d, err := time.ParseDuration(ri); err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("sync.resync_interval %q: not a positive Go duration (or \"0\" to disable)", ri))
		}
	}
	if raw := c.Auth.SessionLifetime; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 15*time.Minute || d > 30*24*time.Hour {
			errs = append(errs, fmt.Errorf("auth.session_lifetime %q: must be a Go duration from 15m through 720h", raw))
		}
	}
	for _, cidr := range c.Auth.TrustedProxies {
		if strings.TrimSpace(cidr) != cidr || cidr == "" {
			errs = append(errs, fmt.Errorf("auth.trusted_proxies entry %q must be a CIDR without surrounding whitespace", cidr))
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			errs = append(errs, fmt.Errorf("auth.trusted_proxies entry %q must be a valid CIDR", cidr))
		}
	}
	if bootstrap := c.Auth.BootstrapUser; !bootstrap.IsZero() {
		if strings.TrimSpace(bootstrap.Email) == "" || bootstrap.Password == "" {
			errs = append(errs, errors.New("auth.bootstrap_user requires email and password"))
		}
	}
	if oidc := c.Auth.OIDC; !oidc.IsZero() {
		if oidc.IssuerURL == "" || oidc.ClientID == "" || oidc.ClientSecret == "" || oidc.RedirectURL == "" {
			errs = append(errs, errors.New("auth.oidc requires issuer_url, client_id, client_secret, and redirect_url"))
		} else {
			if err := validateAuthURL("issuer_url", oidc.IssuerURL); err != nil {
				errs = append(errs, err)
			}
			if err := validateAuthURL("redirect_url", oidc.RedirectURL); err != nil {
				errs = append(errs, err)
			}
		}
		for _, scope := range oidc.Scopes {
			if strings.TrimSpace(scope) == "" || strings.ContainsAny(scope, " \t\r\n") {
				errs = append(errs, fmt.Errorf("auth.oidc scope %q must be one non-empty token", scope))
			}
		}
	}

	for name, patterns := range c.Contexts {
		if !nameRE.MatchString(name) {
			errs = append(errs, fmt.Errorf("contexts: name %q must match %s", name, nameRE))
		}
		if len(patterns) == 0 {
			errs = append(errs, fmt.Errorf("contexts: %q has no repo patterns", name))
		}
		for _, pat := range patterns {
			if _, err := path.Match(pat, "x/y"); err != nil {
				errs = append(errs, fmt.Errorf("contexts: %q: bad pattern %q: %v", name, pat, err))
			}
		}
	}

	seen := map[string]bool{}
	for i, conn := range c.Connections {
		switch {
		case conn.Name == "":
			fail(i, "name is required")
		case !nameRE.MatchString(conn.Name):
			fail(i, "name %q must match %s", conn.Name, nameRE)
		case seen[conn.Name]:
			fail(i, "duplicate name %q", conn.Name)
		}
		seen[conn.Name] = true

		if hasDisallowedURLCredentials(conn.URL) {
			if conn.Type == "git" {
				fail(i, "url must not contain credentials; move the username and password to http_auth")
			} else {
				fail(i, "url must not contain credentials")
			}
		}
		if conn.Type != "git" && !conn.HTTPAuth.IsZero() {
			fail(i, "http_auth is only valid for type git")
		}

		switch conn.Type {
		case "github":
			// app-authed connections may omit selectors: they default to
			// everything the installation was granted
			if len(conn.Orgs)+len(conn.Users)+len(conn.Repos) == 0 && conn.App.IsZero() {
				fail(i, "github connection needs at least one of orgs, users, repos (or an app block)")
			}
			if !conn.App.IsZero() {
				if conn.Token != "" {
					fail(i, "app and token are mutually exclusive")
				}
				if conn.App.ID <= 0 || conn.App.InstallationID <= 0 {
					fail(i, "app requires id and installation_id")
				}
				if (conn.App.PrivateKeyPath == "") == (conn.App.PrivateKey == "") {
					fail(i, "app requires exactly one of private_key_path, private_key")
				}
			}
			if len(conn.Groups) > 0 {
				fail(i, "groups is only valid for type gitlab (github uses orgs)")
			}
			if conn.URL != "" {
				fail(i, "url is not valid for type github")
			}
			if conn.Watch {
				fail(i, "watch is only valid for local git connections")
			}
			validateExcludes(fail, i, conn.Exclude)
		case "gitlab":
			if len(conn.Groups)+len(conn.Users)+len(conn.Repos) == 0 {
				fail(i, "gitlab connection needs at least one of groups, users, repos")
			}
			if !conn.App.IsZero() {
				fail(i, "app is only valid for type github")
			}
			if len(conn.Orgs) > 0 {
				fail(i, "orgs is only valid for type github/gitea (gitlab uses groups)")
			}
			if conn.URL != "" && !isHTTPBase(conn.URL) {
				fail(i, "gitlab url must be an http(s) base URL")
			}
			if httpURLHasQueryOrFragment(conn.URL) {
				fail(i, "gitlab url must not contain a query or fragment")
			}
			if conn.Watch {
				fail(i, "watch is only valid for local git connections")
			}
			validateExcludes(fail, i, conn.Exclude)
		case "gitea":
			if len(conn.Orgs)+len(conn.Users)+len(conn.Repos) == 0 {
				fail(i, "gitea connection needs at least one of orgs, users, repos")
			}
			if !conn.App.IsZero() {
				fail(i, "app is only valid for type github")
			}
			if len(conn.Groups) > 0 {
				fail(i, "groups is only valid for type gitlab (gitea uses orgs)")
			}
			if !isHTTPBase(conn.URL) {
				fail(i, "gitea connection requires an http(s) base url")
			}
			if httpURLHasQueryOrFragment(conn.URL) {
				fail(i, "gitea url must not contain a query or fragment")
			}
			if conn.Watch {
				fail(i, "watch is only valid for local git connections")
			}
			validateExcludes(fail, i, conn.Exclude)
		case "git":
			if conn.URL == "" {
				fail(i, "git connection requires url")
			}
			if looksHTTPURL(conn.URL) && !isHTTPURL(conn.URL) {
				fail(i, "git HTTP(S) url is invalid")
			}
			if isHTTPURL(conn.URL) && httpURLHasQueryOrFragment(conn.URL) {
				fail(i, "git HTTP(S) url must not contain a query or fragment; move credentials to http_auth")
			}
			if !conn.HTTPAuth.IsZero() {
				if !isHTTPURL(conn.URL) {
					fail(i, "http_auth requires an HTTP(S) git url")
				}
				if conn.HTTPAuth.Username == "" || conn.HTTPAuth.Password == "" {
					fail(i, "http_auth requires both username and password")
				}
			}
			if len(conn.Orgs)+len(conn.Groups)+len(conn.Users)+len(conn.Repos) > 0 || conn.Token != "" || !conn.Exclude.isZero() || !conn.App.IsZero() {
				fail(i, "orgs/groups/users/repos/token/exclude/app are only valid for code-host types")
			}
			if conn.Watch && !IsLocalURL(conn.URL) {
				fail(i, "watch requires a local url (absolute path or file://)")
			}
		case "":
			fail(i, "type is required (github, gitlab, gitea, or git)")
		default:
			fail(i, "unknown type %q (want github, gitlab, gitea, or git)", conn.Type)
		}
	}
	return errors.Join(errs...)
}

// isHTTPBase reports whether s can serve as a code-host API base URL.
func isHTTPBase(s string) bool {
	return isHTTPURL(s)
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Opaque == "" && (strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) && u.Hostname() != ""
}

func looksHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:")
}

var apparentHTTPCredRE = regexp.MustCompile(`(?i)^https?:/*[^/?#@\s]*@`)

func hasDisallowedURLCredentials(s string) bool {
	if isSCPLikeURL(s) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return apparentHTTPCredRE.MatchString(s)
	}
	if u.User != nil {
		if isSSHScheme(u.Scheme) {
			_, hasPassword := u.User.Password()
			return hasPassword
		}
		return true
	}
	authority, _, _ := strings.Cut(u.Opaque, "/")
	userinfo, _, hasUserinfo := strings.Cut(authority, "@")
	if hasUserinfo {
		return !isSSHScheme(u.Scheme) || strings.Contains(userinfo, ":")
	}
	return apparentHTTPCredRE.MatchString(s)
}

func isSSHScheme(s string) bool {
	switch strings.ToLower(s) {
	case "ssh", "git+ssh", "ssh+git", "sftp":
		return true
	default:
		return false
	}
}

func isSCPLikeURL(s string) bool {
	host, repoPath, ok := strings.Cut(s, ":")
	if !ok || repoPath == "" || strings.Contains(host, "/") || strings.HasPrefix(repoPath, "//") {
		return false
	}
	switch strings.ToLower(host) {
	case "http", "https", "ssh", "git", "ftp", "file", "sftp", "rsync", "git+ssh", "ssh+git":
		return false
	default:
		return true
	}
}

func httpURLHasQueryOrFragment(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.RawQuery != "" || u.ForceQuery || u.Fragment != "")
}

func validateExcludes(fail func(int, string, ...any), i int, ex Exclude) {
	for _, pat := range ex.Repos {
		if _, err := path.Match(pat, "x/y"); err != nil {
			fail(i, "bad exclude pattern %q: %v", pat, err)
		}
	}
}

func validateAuthURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("auth.oidc.%s must be an absolute URL without credentials, query, or fragment", field)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := strings.Trim(u.Hostname(), "[]")
	if u.Scheme == "http" && (host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return fmt.Errorf("auth.oidc.%s must use HTTPS (HTTP is allowed only for loopback testing)", field)
}

func (c *Config) applyDefaults() error {
	// Secret expansion is strict: a referenced variable that is absent or
	// empty is a configuration error, even when other literal text would keep
	// the final value non-empty. This prevents a typo from silently weakening
	// authentication or changing which private repositories are visible.
	var err error
	if c.Auth.APIKey, err = expandSecret("auth.api_key", c.Auth.APIKey); err != nil {
		return err
	}
	if c.Auth.BootstrapUser.Password, err = expandSecret("auth.bootstrap_user.password", c.Auth.BootstrapUser.Password); err != nil {
		return err
	}
	if c.Auth.OIDC.ClientSecret, err = expandSecret("auth.oidc.client_secret", c.Auth.OIDC.ClientSecret); err != nil {
		return err
	}
	if c.Webhook.Secret, err = expandSecret("webhook.secret", c.Webhook.Secret); err != nil {
		return err
	}
	for i := range c.Connections {
		conn := &c.Connections[i]
		if conn.Token, err = expandSecret(fmt.Sprintf("connections[%d].token", i), conn.Token); err != nil {
			return err
		}
		if conn.App.PrivateKey, err = expandSecret(fmt.Sprintf("connections[%d].app.private_key", i), conn.App.PrivateKey); err != nil {
			return err
		}
		if conn.HTTPAuth.Username, err = expandSecret(fmt.Sprintf("connections[%d].http_auth.username", i), conn.HTTPAuth.Username); err != nil {
			return err
		}
		if conn.HTTPAuth.Password, err = expandSecret(fmt.Sprintf("connections[%d].http_auth.password", i), conn.HTTPAuth.Password); err != nil {
			return err
		}
	}
	if c.Server.Addr == "" {
		c.Server.Addr = "127.0.0.1:3070"
	}
	if c.Server.DataDir == "" {
		c.Server.DataDir = "~/.phebs"
	}
	if c.Server.DataDir == "~" || len(c.Server.DataDir) > 1 && c.Server.DataDir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve data_dir: %w", err)
		}
		c.Server.DataDir = filepath.Join(home, c.Server.DataDir[1:])
	}
	abs, err := filepath.Abs(c.Server.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data_dir: %w", err)
	}
	if strings.ContainsAny(abs, `*?[\`) {
		return errors.New("server.data_dir must not contain glob metacharacters (*, ?, [, or \\)")
	}
	c.Server.DataDir = abs
	return nil
}

func expandSecret(field, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	var unavailable []string
	seen := map[string]bool{}
	expanded := os.Expand(raw, func(name string) string {
		value, ok := os.LookupEnv(name)
		if (!ok || value == "") && !seen[name] {
			seen[name] = true
			unavailable = append(unavailable, name)
		}
		return value
	})
	if len(unavailable) > 0 {
		return "", fmt.Errorf("%s references unset or empty environment variable %q", field, strings.Join(unavailable, ", "))
	}
	if expanded == "" {
		return "", fmt.Errorf("%s expands to empty; refusing to start", field)
	}
	return expanded, nil
}
