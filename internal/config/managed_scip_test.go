package config

import (
	"strings"
	"testing"
)

func TestManagedSCIPRequiresExplicitInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"absent", "server: {}\n", true},
		{"explicit", "managed_scip: {manifest: /var/lib/phebs-scip/installation.json, sha256: 'sha256:" + strings.Repeat("a", 64) + "'}\n", true},
		{"empty", "managed_scip: {}\n", false},
		{"relative", "managed_scip: {manifest: install.json, sha256: 'sha256:" + strings.Repeat("a", 64) + "'}\n", false},
		{"missing-digest", "managed_scip: {manifest: /var/lib/phebs-scip/installation.json}\n", false},
		{"command", "managed_scip: {manifest: /var/lib/phebs-scip/installation.json, command: scip-go}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.body)); (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
