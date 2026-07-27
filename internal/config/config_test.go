package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string // substring of the error; empty = must succeed
	}{
		{"empty doc", "", "empty config"},
		{"minimal defaults", "{}", ""},
		{
			"full valid",
			`server:
  addr: ":8080"
  data_dir: /tmp/phebs
auth:
  api_key: secret
connections:
  - name: gh-me
    type: github
    users: [bmeddeb]
  - name: local-mirror
    type: git
    url: https://example.com/repo.git
`,
			"",
		},
		{
			"revision allowlist valid",
			"revisions:\n  github.com/acme/api:\n    release-1: refs/heads/release/1\n    v1.4.0: refs/tags/v1.4.0\n",
			"",
		},
		{
			"revision cap",
			"revisions:\n  github.com/acme/api: {a: refs/heads/a, b: refs/heads/b, c: refs/heads/c, d: refs/heads/d, e: refs/heads/e, f: refs/heads/f, g: refs/heads/g, h: refs/heads/h}\n",
			"at most 7 are allowed (8 including HEAD)",
		},
		{
			"revision selector HEAD reserved",
			"revisions:\n  github.com/acme/api: {HEAD: refs/heads/main}\n",
			"HEAD is reserved",
		},
		{
			"revision ref must be full",
			"revisions:\n  github.com/acme/api: {release: release}\n",
			"must start with refs/heads/ or refs/tags/",
		},
		{
			"revision duplicate ref",
			"revisions:\n  github.com/acme/api: {one: refs/heads/release, two: refs/heads/release}\n",
			"name the same ref",
		},
		{
			"revision unsafe repo",
			"revisions:\n  ../outside: {release: refs/heads/release}\n",
			"unsafe repository name",
		},
		{
			"auth bootstrap valid",
			"auth:\n  cookie_secure: false\n  session_lifetime: 8h\n  trusted_proxies: ['127.0.0.1/32', 'fd00::/8']\n  bootstrap_user: {email: admin@example.com, password: 'long-enough-password'}\n",
			"",
		},
		{
			"auth invalid trusted proxy",
			"auth:\n  trusted_proxies: ['127.0.0.1']\n",
			"auth.trusted_proxies entry \"127.0.0.1\" must be a valid CIDR",
		},
		{
			"auth bootstrap partial",
			"auth:\n  bootstrap_user: {email: admin@example.com}\n",
			"auth.bootstrap_user requires email and password",
		},
		{
			"auth session too short",
			"auth:\n  session_lifetime: 5m\n",
			"auth.session_lifetime",
		},
		{
			"OIDC valid loopback",
			"auth:\n  oidc: {issuer_url: 'http://127.0.0.1:9090/realms/test', client_id: phebs, client_secret: secret, redirect_url: 'http://localhost:3070/api/auth/oidc/callback'}\n",
			"",
		},
		{
			"OIDC partial",
			"auth:\n  oidc: {issuer_url: 'https://idp.example', client_id: phebs}\n",
			"auth.oidc requires issuer_url, client_id, client_secret, and redirect_url",
		},
		{
			"OIDC external HTTP rejected",
			"auth:\n  oidc: {issuer_url: 'http://idp.example', client_id: phebs, client_secret: secret, redirect_url: 'https://phebs.example/api/auth/oidc/callback'}\n",
			"issuer_url must use HTTPS",
		},
		{
			"OIDC credential URL rejected",
			"auth:\n  oidc: {issuer_url: 'https://user:secret@idp.example', client_id: phebs, client_secret: secret, redirect_url: 'https://phebs.example/api/auth/oidc/callback'}\n",
			"without credentials, query, or fragment",
		},
		{
			"OIDC bad scope",
			"auth:\n  oidc: {issuer_url: 'https://idp.example', client_id: phebs, client_secret: secret, redirect_url: 'https://phebs.example/api/auth/oidc/callback', scopes: ['bad scope']}\n",
			"must be one non-empty token",
		},
		{"unknown field", "server:\n  adress: \":8080\"\n", "line 2: field adress not found"},
		{"bad type value", "server:\n  addr: [1, 2]\n", "line 2"},
		{"glob data dir", "server:\n  data_dir: '/tmp/phebs[*]'\n", "data_dir must not contain glob metacharacters"},
		{
			"duplicate names",
			"connections:\n  - {name: a, type: git, url: u}\n  - {name: a, type: git, url: u}\n",
			"line 3: connections[1]: duplicate name \"a\"",
		},
		{
			"github without selectors",
			"connections:\n  - name: gh\n    type: github\n",
			"line 2: connections[0]: github connection needs at least one",
		},
		{
			"git without url",
			"connections:\n  - name: g\n    type: git\n",
			"line 2: connections[0]: git connection requires url",
		},
		{
			"home-relative local git with watch",
			"connections:\n  - {name: g, type: git, url: '~/src/project', watch: true}\n",
			"",
		},
		{
			"bare home is not a repository",
			"connections:\n  - {name: g, type: git, url: '~'}\n",
			"home-relative path must start with ~/",
		},
		{
			"named user home is not expanded",
			"connections:\n  - {name: g, type: git, url: '~other/src/project'}\n",
			"home-relative path must start with ~/",
		},
		{
			"home-relative traversal rejected",
			"connections:\n  - {name: g, type: git, url: '~/../outside'}\n",
			"must not contain empty, '.', or '..' components",
		},
		{
			"home-relative glob rejected",
			"connections:\n  - {name: g, type: git, url: '~/src/*/project'}\n",
			"must not contain glob metacharacters",
		},
		{
			"file url does not expand home",
			"connections:\n  - {name: g, type: git, url: 'file://~/src/project'}\n",
			"file:// does not expand '~'",
		},
		{
			"git with github fields",
			"connections:\n  - {name: g, type: git, url: u, orgs: [x]}\n",
			"only valid for code-host types",
		},
		{
			"git with http auth",
			"connections:\n  - {name: g, type: git, url: 'https://git.example.com/team/repo.git', http_auth: {username: bot, password: secret}}\n",
			"",
		},
		{
			"git http auth requires http",
			"connections:\n  - {name: g, type: git, url: 'ssh://git@git.example.com/team/repo.git', http_auth: {username: bot, password: secret}}\n",
			"http_auth requires an HTTP(S) git url",
		},
		{
			"git http auth requires both fields",
			"connections:\n  - {name: g, type: git, url: 'https://git.example.com/team/repo.git', http_auth: {username: bot}}\n",
			"http_auth requires both username and password",
		},
		{
			"http auth only on generic git",
			"connections:\n  - {name: gh, type: github, users: [u], http_auth: {username: bot, password: secret}}\n",
			"http_auth is only valid for type git",
		},
		{
			"git url credentials rejected",
			"connections:\n  - {name: g, type: git, url: 'https://user:secret@git.example.com/team/repo.git'}\n",
			"move the username and password to http_auth",
		},
		{
			"malformed git http url rejected",
			"connections:\n  - {name: g, type: git, url: 'https://user:secret@%zz/repo.git'}\n",
			"git HTTP(S) url is invalid",
		},
		{
			"opaque http credentials rejected",
			"connections:\n  - {name: g, type: git, url: 'https:user:secret@git.example.com/team/repo.git'}\n",
			"move the username and password to http_auth",
		},
		{
			"single slash http credentials rejected",
			"connections:\n  - {name: g, type: git, url: 'https:/user:secret@git.example.com/team/repo.git'}\n",
			"move the username and password to http_auth",
		},
		{
			"opaque http url rejected",
			"connections:\n  - {name: g, type: git, url: 'https:git.example.com/team/repo.git'}\n",
			"git HTTP(S) url is invalid",
		},
		{
			"http url without host rejected",
			"connections:\n  - {name: g, type: git, url: 'https:///team/repo.git'}\n",
			"git HTTP(S) url is invalid",
		},
		{
			"http query rejected",
			"connections:\n  - {name: g, type: git, url: 'https://git.example.com/team/repo.git?token=secret'}\n",
			"must not contain a query or fragment",
		},
		{
			"http fragment rejected",
			"connections:\n  - {name: g, type: git, url: 'https://git.example.com/team/repo.git#credential'}\n",
			"must not contain a query or fragment",
		},
		{
			"ssh password rejected",
			"connections:\n  - {name: g, type: git, url: 'ssh://git:secret@git.example.com/team/repo.git'}\n",
			"move the username and password to http_auth",
		},
		{
			"other scheme userinfo rejected",
			"connections:\n  - {name: g, type: git, url: 'ftp://user:secret@git.example.com/team/repo.git'}\n",
			"move the username and password to http_auth",
		},
		{
			"scheme relative userinfo rejected",
			"connections:\n  - {name: g, type: git, url: '//user:secret@git.example.com/team/repo.git'}\n",
			"move the username and password to http_auth",
		},
		{
			"ssh url username remains valid",
			"connections:\n  - {name: g, type: git, url: 'ssh://git@git.example.com/team/repo.git'}\n",
			"",
		},
		{
			"git plus ssh username remains valid",
			"connections:\n  - {name: g, type: git, url: 'git+ssh://git@git.example.com/team/repo.git'}\n",
			"",
		},
		{
			"scp path containing at remains valid",
			"connections:\n  - {name: g, type: git, url: 'git.example.com:team/repo@release'}\n",
			"",
		},
		{
			"github with url",
			"connections:\n  - {name: gh, type: github, users: [u], url: x}\n",
			"url is not valid for type github",
		},
		{
			"gitea without url",
			"connections:\n  - {name: gt, type: gitea, orgs: [o]}\n",
			"gitea connection requires an http(s) base url",
		},
		{
			"gitea with groups",
			"connections:\n  - {name: gt, type: gitea, orgs: [o], url: 'https://g.example.com', groups: [g]}\n",
			"groups is only valid for type gitlab",
		},
		{
			"gitea valid",
			"connections:\n  - {name: gt, type: gitea, orgs: [o], url: 'https://g.example.com'}\n",
			"",
		},
		{
			"gitea query rejected",
			"connections:\n  - {name: gt, type: gitea, orgs: [o], url: 'https://g.example.com?token=secret'}\n",
			"gitea url must not contain a query or fragment",
		},
		{
			"app with token",
			"connections:\n  - {name: gh, type: github, token: t, app: {id: 1, installation_id: 2, private_key_path: /k.pem}}\n",
			"app and token are mutually exclusive",
		},
		{
			"app missing ids",
			"connections:\n  - {name: gh, type: github, app: {private_key_path: /k.pem}}\n",
			"app requires id and installation_id",
		},
		{
			"app both key forms",
			"connections:\n  - {name: gh, type: github, app: {id: 1, installation_id: 2, private_key_path: /k.pem, private_key: x}}\n",
			"exactly one of private_key_path, private_key",
		},
		{
			"app no key",
			"connections:\n  - {name: gh, type: github, app: {id: 1, installation_id: 2}}\n",
			"exactly one of private_key_path, private_key",
		},
		{
			"app on gitlab",
			"connections:\n  - {name: gl, type: gitlab, groups: [g], app: {id: 1, installation_id: 2, private_key_path: /k.pem}}\n",
			"app is only valid for type github",
		},
		{
			"app without selectors is valid",
			"connections:\n  - {name: gh, type: github, app: {id: 1, installation_id: 2, private_key_path: /k.pem}}\n",
			"",
		},
		{
			"github with groups",
			"connections:\n  - {name: gh, type: github, users: [u], groups: [g]}\n",
			"groups is only valid for type gitlab",
		},
		{
			"gitlab without selectors",
			"connections:\n  - {name: gl, type: gitlab}\n",
			"gitlab connection needs at least one of groups, users, repos",
		},
		{
			"gitlab with orgs",
			"connections:\n  - {name: gl, type: gitlab, orgs: [x]}\n",
			"orgs is only valid for type github",
		},
		{
			"gitlab bad base url",
			"connections:\n  - {name: gl, type: gitlab, groups: [g], url: git@example.com}\n",
			"must be an http(s) base URL",
		},
		{
			"gitlab valid",
			"connections:\n  - {name: gl, type: gitlab, groups: [team/platform], url: 'https://git.example.com'}\n",
			"",
		},
		{
			"gitlab fragment rejected",
			"connections:\n  - {name: gl, type: gitlab, groups: [team/platform], url: 'https://git.example.com#secret'}\n",
			"gitlab url must not contain a query or fragment",
		},
		{
			"bad resync_interval",
			"sync:\n  resync_interval: soonish\n",
			"sync.resync_interval \"soonish\"",
		},
		{
			"resync_interval disabled",
			"sync:\n  resync_interval: \"0\"\n",
			"",
		},
		{
			"proof bundle retention valid",
			"proof_bundles:\n  retention: 168h\n",
			"",
		},
		{
			"proof bundle retention disabled",
			"proof_bundles:\n  retention: \"0\"\n",
			"",
		},
		{
			"proof bundle retention invalid",
			"proof_bundles:\n  retention: someday\n",
			"proof_bundles.retention \"someday\"",
		},
		{
			"context bad name",
			"contexts:\n  Bad_Name: [\"h/*\"]\n",
			"contexts: name \"Bad_Name\" must match",
		},
		{
			"context empty patterns",
			"contexts:\n  backend: []\n",
			"contexts: \"backend\" has no repo patterns",
		},
		{
			"context bad glob",
			"contexts:\n  backend: [\"a[\"]\n",
			"bad pattern",
		},
		{
			"context valid",
			"contexts:\n  backend: [\"github.com/acme/api-*\"]\n",
			"",
		},
		{"missing type", "connections:\n  - name: a\n", "type is required"},
		{"unknown type", "connections:\n  - {name: a, type: svn}\n", "unknown type \"svn\""},
		{"bad name charset", "connections:\n  - {name: Bad_Name, type: git, url: u}\n", "must match"},
		{"missing name", "connections:\n  - {type: git, url: u}\n", "name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.in))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse() = %+v, want error containing %q", cfg, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse() error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolveHomeRelativePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	absolute, relative, err := ResolveHomeRelativePath("~/Uber/go-code")
	if err != nil {
		t.Fatal(err)
	}
	if absolute != filepath.Join(home, "Uber", "go-code") {
		t.Errorf("absolute = %q, want path beneath %q", absolute, home)
	}
	if relative != "Uber/go-code" {
		t.Errorf("relative = %q, want stable slash-relative identity", relative)
	}

	cfg, err := Parse([]byte("connections:\n  - {name: local, type: git, url: '~/Uber/go-code', watch: true}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connections[0].URL != "~/Uber/go-code" {
		t.Fatalf("configured url = %q, want portable spelling retained", cfg.Connections[0].URL)
	}

	for _, raw := range []string{
		"~",
		"~other/repo",
		"~/",
		"~/../repo",
		"~/a//repo",
		"~/a/./repo",
		"~/a/*/repo",
		"~/a/repo\\other",
		"~/a/\x7frepo",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, _, err := ResolveHomeRelativePath(raw); err == nil {
				t.Fatalf("ResolveHomeRelativePath(%q) succeeded", raw)
			}
		})
	}
}

func TestIsLocalURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"/Users/ben/src/project", true},
		{"~/src/project", true},
		{"file:///tmp/project", true},
		{"https://github.com/acme/project.git", false},
		{"git@github.com:acme/project.git", false},
		{"relative/project", false},
	}
	for _, tt := range tests {
		if got := IsLocalURL(tt.raw); got != tt.want {
			t.Errorf("IsLocalURL(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestProvisionalProtoExtractionIsExplicitOptIn(t *testing.T) {
	cfg, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Experimental.ProvisionalProtoExtraction {
		t.Fatal("provisional protobuf extraction enabled by default")
	}

	cfg, err = Parse([]byte("experimental:\n  provisional_proto_extraction: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Experimental.ProvisionalProtoExtraction {
		t.Fatal("explicit provisional protobuf extraction opt-in was ignored")
	}
}

func TestProvisionalThriftFieldExtractionIsExplicitOptIn(t *testing.T) {
	cfg, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Experimental.ProvisionalThriftFieldExtraction {
		t.Fatal("provisional Thrift field extraction enabled by default")
	}

	cfg, err = Parse([]byte("experimental:\n  provisional_thrift_field_extraction: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Experimental.ProvisionalThriftFieldExtraction {
		t.Fatal("explicit provisional Thrift field extraction opt-in was ignored")
	}
}

func TestURLCredentialErrorsDoNotLeak(t *testing.T) {
	tests := []struct {
		url, secret string
	}{
		{"https:bot:opaque-secret@git.example/repo.git", "opaque-secret"},
		{"ssh://bot:ssh-secret@git.example/repo.git", "ssh-secret"},
		{"ftp://bot:ftp-secret@git.example/repo.git", "ftp-secret"},
		{"https://git.example/repo.git?token=query-secret", "query-secret"},
	}
	for _, tt := range tests {
		_, err := Parse([]byte("connections:\n  - {name: g, type: git, url: '" + tt.url + "'}\n"))
		if err == nil {
			t.Fatalf("Parse accepted credential-bearing URL %q", tt.url)
		}
		if strings.Contains(err.Error(), tt.secret) {
			t.Errorf("Parse error leaked %q: %v", tt.secret, err)
		}
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != "127.0.0.1:3070" {
		t.Errorf("Addr = %q, want 127.0.0.1:3070", cfg.Server.Addr)
	}
	if !strings.HasSuffix(cfg.Server.DataDir, ".phebs") || strings.HasPrefix(cfg.Server.DataDir, "~") {
		t.Errorf("DataDir = %q, want expanded ~/.phebs", cfg.Server.DataDir)
	}
	if !cfg.Auth.SecureCookies() || cfg.Auth.SessionDuration() != 12*time.Hour {
		t.Errorf("auth defaults: secure=%v lifetime=%v", cfg.Auth.SecureCookies(), cfg.Auth.SessionDuration())
	}
	if got := cfg.ProofBundles.RetentionFor(); got != 0 {
		t.Errorf("proof bundle retention = %v, want disabled", got)
	}
	configured, err := Parse([]byte("proof_bundles:\n  retention: 168h\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := configured.ProofBundles.RetentionFor(); got != 7*24*time.Hour {
		t.Errorf("configured proof bundle retention = %v, want 168h", got)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("PHEBS_TEST_KEY", "s3cret")
	t.Setenv("PHEBS_TEST_TOK", "ghp_xyz")
	t.Setenv("PHEBS_TEST_USER", "git-bot")
	t.Setenv("PHEBS_TEST_PASS", "git-password")
	t.Setenv("PHEBS_TEST_BOOTSTRAP", "bootstrap-password")
	t.Setenv("PHEBS_TEST_OIDC", "oidc-secret")
	cfg, err := Parse([]byte("auth:\n  api_key: \"${PHEBS_TEST_KEY}\"\n  bootstrap_user: {email: admin@example.com, password: '${PHEBS_TEST_BOOTSTRAP}'}\n  oidc: {issuer_url: 'https://idp.example', client_id: phebs, client_secret: '${PHEBS_TEST_OIDC}', redirect_url: 'https://phebs.example/api/auth/oidc/callback'}\nconnections:\n  - {name: gh, type: github, users: [u], token: \"${PHEBS_TEST_TOK}\"}\n  - {name: git, type: git, url: 'https://git.example/repo.git', http_auth: {username: '${PHEBS_TEST_USER}', password: '${PHEBS_TEST_PASS}'}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.APIKey != "s3cret" {
		t.Errorf("api_key = %q, want expanded s3cret (MANUAL claims ${ENV} expansion)", cfg.Auth.APIKey)
	}
	if cfg.Connections[0].Token != "ghp_xyz" {
		t.Errorf("token = %q, want expanded", cfg.Connections[0].Token)
	}
	if cfg.Connections[1].HTTPAuth != (HTTPAuth{Username: "git-bot", Password: "git-password"}) {
		t.Errorf("http_auth = %+v, want expanded credentials", cfg.Connections[1].HTTPAuth)
	}
	if cfg.Auth.BootstrapUser.Password != "bootstrap-password" || cfg.Auth.OIDC.ClientSecret != "oidc-secret" {
		t.Errorf("auth secrets not expanded: bootstrap=%q oidc=%q", cfg.Auth.BootstrapUser.Password, cfg.Auth.OIDC.ClientSecret)
	}
}

// A non-empty api_key referencing an unset env var must fail closed, not
// expand to "" and silently disable API auth.
func TestAPIKeyUnsetEnvFailsClosed(t *testing.T) {
	_, err := Parse([]byte("auth:\n  api_key: \"${PHEBS_DEFINITELY_UNSET_KEY}\"\n"))
	if err == nil || !strings.Contains(err.Error(), "unset or empty environment variable") {
		t.Fatalf("Parse err = %v, want fail-closed on unset api_key env var", err)
	}
	// an intentionally-empty api_key (open API by design) still parses.
	if _, err := Parse([]byte("auth:\n  api_key: \"\"\n")); err != nil {
		t.Errorf("empty api_key should stay valid (open API), got %v", err)
	}
}

func TestSecretEnvExpansionFailsClosed(t *testing.T) {
	t.Setenv("PHEBS_TEST_EMPTY_SECRET", "")
	tests := []struct {
		name, in, field string
	}{
		{"partial api key", "auth:\n  api_key: 'prefix-${PHEBS_TEST_EMPTY_SECRET}'\n", "auth.api_key"},
		{"bootstrap password", "auth:\n  bootstrap_user: {email: admin@example.com, password: '${PHEBS_TEST_EMPTY_SECRET}'}\n", "auth.bootstrap_user.password"},
		{"OIDC client secret", "auth:\n  oidc: {issuer_url: 'https://idp.example', client_id: phebs, client_secret: '${PHEBS_TEST_EMPTY_SECRET}', redirect_url: 'https://phebs.example/api/auth/oidc/callback'}\n", "auth.oidc.client_secret"},
		{"webhook", "webhook:\n  secret: '${PHEBS_TEST_EMPTY_SECRET}'\n", "webhook.secret"},
		{"pat", "connections:\n  - {name: gh, type: github, users: [u], token: '${PHEBS_TEST_EMPTY_SECRET}'}\n", "connections[0].token"},
		{"app key", "connections:\n  - {name: gh, type: github, app: {id: 1, installation_id: 2, private_key: '${PHEBS_TEST_EMPTY_SECRET}'}}\n", "connections[0].app.private_key"},
		{"http username", "connections:\n  - {name: git, type: git, url: 'https://git.example/repo.git', http_auth: {username: '${PHEBS_TEST_EMPTY_SECRET}', password: pass}}\n", "connections[0].http_auth.username"},
		{"http password", "connections:\n  - {name: git, type: git, url: 'https://git.example/repo.git', http_auth: {username: bot, password: '${PHEBS_TEST_EMPTY_SECRET}'}}\n", "connections[0].http_auth.password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.field) || !strings.Contains(err.Error(), "unset or empty") {
				t.Fatalf("Parse() err = %v, want strict %s expansion error", err, tt.field)
			}
		})
	}
}

func TestValidateReportsAllErrors(t *testing.T) {
	in := "connections:\n  - {name: a, type: git}\n  - {name: a, type: github}\n"
	_, err := Parse([]byte(in))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"requires url", "duplicate name", "needs at least one"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResyncEvery(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", time.Hour},
		{"0", 0},
		{"30m", 30 * time.Minute},
	}
	for _, tt := range cases {
		if got := (Sync{ResyncInterval: tt.in}).ResyncEvery(); got != tt.want {
			t.Errorf("ResyncEvery(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// Like api_key and webhook.secret, an app private key referencing an unset
// env var must fail at boot, not as a cryptic per-sync PEM error.
func TestAppKeyUnsetEnvFailsClosed(t *testing.T) {
	in := "connections:\n  - {name: gh, type: github, app: {id: 1, installation_id: 2, private_key: '${PHEBS_TEST_UNSET_KEY}'}}\n"
	_, err := Parse([]byte(in))
	if err == nil || !strings.Contains(err.Error(), "app.private_key") {
		t.Fatalf("Parse() err = %v, want app.private_key fail-closed error", err)
	}
}

func TestLoadForRecoveryValidatesWithoutExpandingSecrets(t *testing.T) {
	t.Setenv("PHEBS_RECOVERY_MISSING_SECRET", "")
	raw := []byte("server:\n  data_dir: ./restore-data\nwebhook:\n  secret: '${PHEBS_RECOVERY_MISSING_SECRET}'\n")
	path := filepath.Join(t.TempDir(), "phebs.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "unset or empty") {
		t.Fatalf("normal Parse error = %v, want missing-secret refusal", err)
	}
	cfg, gotRaw, err := LoadForRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRaw) != string(raw) {
		t.Fatalf("recovery config bytes changed: %q", gotRaw)
	}
	if cfg.Webhook.Secret != "${PHEBS_RECOVERY_MISSING_SECRET}" {
		t.Fatalf("recovery secret = %q, want unexpanded reference", cfg.Webhook.Secret)
	}
	if !filepath.IsAbs(cfg.Server.DataDir) {
		t.Fatalf("recovery data dir = %q, want normalized absolute path", cfg.Server.DataDir)
	}
}
