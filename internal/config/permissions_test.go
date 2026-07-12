package config

import (
	"strings"
	"testing"
)

// T10.3 permissions block semantics.
func TestPermissionsConfig(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string // substring; empty = must succeed
		enabled bool   // expected cfg.Permissions != nil when no error
	}{
		{"absent block disables", "{}", "", false},
		{"empty block enables", "permissions: {}", "", true},
		// a bare key is YAML null — presence must still enable (docs promise it)
		{"bare key enables", "permissions:\n", "", true},
		{"valid users and globs",
			"permissions:\n  users:\n    ben@example.com: ['github.com:bmeddeb']\n  always_visible: ['local/*']\n",
			"", true},
		// path-hosted GitLab/Gitea instances put a subpath in the repo-name
		// prefix; the identity host must be allowed to carry it
		{"path-hosted identity host",
			"permissions:\n  users:\n    ben@example.com: ['git.example.com/gitlab:ben']\n",
			"", true},
		{"login with slash rejected",
			"permissions:\n  users:\n    ben@example.com: ['github.com:group/ben']\n",
			`identity "github.com:group/ben"`, false},
		{"identity missing colon",
			"permissions:\n  users:\n    ben@example.com: ['bmeddeb']\n",
			`identity "bmeddeb"`, false},
		{"non-email key",
			"permissions:\n  users:\n    bmeddeb: ['github.com:bmeddeb']\n",
			"must be an email address", false},
		{"bad always_visible glob",
			"permissions:\n  always_visible: ['[']\n",
			"bad pattern", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := cfg.Permissions != nil; got != tt.enabled {
				t.Fatalf("Permissions enabled = %v, want %v", got, tt.enabled)
			}
		})
	}
}
