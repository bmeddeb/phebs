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
			"only valid for type github",
		},
		{
			"github with url",
			"connections:\n  - {name: gh, type: github, users: [u], url: x}\n",
			"url is only valid for type git",
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
