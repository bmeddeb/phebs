package config

import (
	"strings"
	"testing"
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
		{"unknown field", "server:\n  adress: \":8080\"\n", "line 2: field adress not found"},
		{"bad type value", "server:\n  addr: [1, 2]\n", "line 2"},
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
			"git with github fields",
			"connections:\n  - {name: g, type: git, url: u, orgs: [x]}\n",
			"only valid for code-host types",
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

func TestDefaults(t *testing.T) {
	cfg, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != ":3070" {
		t.Errorf("Addr = %q, want :3070", cfg.Server.Addr)
	}
	if !strings.HasSuffix(cfg.Server.DataDir, ".phebs") || strings.HasPrefix(cfg.Server.DataDir, "~") {
		t.Errorf("DataDir = %q, want expanded ~/.phebs", cfg.Server.DataDir)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("PHEBS_TEST_KEY", "s3cret")
	t.Setenv("PHEBS_TEST_TOK", "ghp_xyz")
	cfg, err := Parse([]byte("auth:\n  api_key: \"${PHEBS_TEST_KEY}\"\nconnections:\n  - {name: gh, type: github, users: [u], token: \"${PHEBS_TEST_TOK}\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.APIKey != "s3cret" {
		t.Errorf("api_key = %q, want expanded s3cret (MANUAL claims ${ENV} expansion)", cfg.Auth.APIKey)
	}
	if cfg.Connections[0].Token != "ghp_xyz" {
		t.Errorf("token = %q, want expanded", cfg.Connections[0].Token)
	}
}

// A non-empty api_key referencing an unset env var must fail closed, not
// expand to "" and silently disable API auth.
func TestAPIKeyUnsetEnvFailsClosed(t *testing.T) {
	_, err := Parse([]byte("auth:\n  api_key: \"${PHEBS_DEFINITELY_UNSET_KEY}\"\n"))
	if err == nil || !strings.Contains(err.Error(), "expands to empty") {
		t.Fatalf("Parse err = %v, want fail-closed on unset api_key env var", err)
	}
	// an intentionally-empty api_key (open API by design) still parses.
	if _, err := Parse([]byte("auth:\n  api_key: \"\"\n")); err != nil {
		t.Errorf("empty api_key should stay valid (open API), got %v", err)
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
