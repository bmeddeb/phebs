package sync

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name, raw, want, wantErr string
	}{
		{"https strips unsafe components", "https://bot:secret@git.example.com/team/repo.git?token=query-secret#fragment-secret", "https://git.example.com/team/repo.git", ""},
		{"http username", "http://bot@git.example.com/repo.git", "http://git.example.com/repo.git", ""},
		{"http query", "https://git.example.com/repo.git?download=1", "https://git.example.com/repo.git", ""},
		{"ssh username", "ssh://git@git.example.com/team/repo.git", "ssh://git@git.example.com/team/repo.git", ""},
		{"git plus ssh username", "git+ssh://git@git.example.com/team/repo.git", "git+ssh://git@git.example.com/team/repo.git", ""},
		{"ssh strips suffix", "ssh://git@git.example.com/team/repo.git?token=query-secret#fragment-secret", "ssh://git@git.example.com/team/repo.git", ""},
		{"opaque ssh username", "ssh:git@git.example.com/team/repo.git", "ssh:git@git.example.com/team/repo.git", ""},
		{"scp syntax", "git@git.example.com:team/repo.git", "git@git.example.com:team/repo.git", ""},
		{"scp path with at", "git.example.com:team/repo@release", "git.example.com:team/repo@release", ""},
		{"local path", "/tmp/repo", "/tmp/repo", ""},
		{"git scheme", "git://git.example.com/team/repo.git?token=query-secret#fragment-secret", "git://git.example.com/team/repo.git", ""},
		{"opaque http", "https:git.example.com/repo.git", "", "hierarchical HTTP(S)"},
		{"single slash http", "https:/git.example.com/repo.git", "", "hierarchical HTTP(S)"},
		{"http missing host", "https:///repo.git", "", "hierarchical HTTP(S)"},
		{"ssh password", "ssh://git:ssh-private@git.example.com/repo.git", "", "SSH password"},
		{"opaque ssh password", "ssh:git:ssh-private@git.example.com/repo.git", "", "SSH password"},
		{"other scheme userinfo", "ftp://bot:ftp-private@git.example.com/repo.git", "", "userinfo"},
		{"scheme relative userinfo", "//bot:relative-private@git.example.com/repo.git", "", "userinfo"},
		{"malformed credential URL", "https://user:malformed-private@%zz/repo.git", "", "invalid URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeURL(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("SanitizeURL(%q) error = %v, want %q", tt.raw, err, tt.wantErr)
				}
				for _, secret := range []string{"ssh-private", "ftp-private", "relative-private", "malformed-private"} {
					if strings.Contains(err.Error(), secret) {
						t.Errorf("SanitizeURL error leaked %q: %v", secret, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("SanitizeURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestHTTPBasicAuthConfig(t *testing.T) {
	args := HTTPBasicAuthConfig("git-bot", "secret:with-colon")
	if len(args) != 2 || args[0] != "-c" {
		t.Fatalf("HTTPBasicAuthConfig = %q", args)
	}
	encoded := strings.TrimPrefix(args[1], "http.extraheader=Authorization: Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(decoded), "git-bot:secret:with-colon"; got != want {
		t.Errorf("decoded auth = %q, want %q", got, want)
	}
	if got := HTTPBasicAuthConfig("", ""); got != nil {
		t.Errorf("empty auth config = %q, want nil", got)
	}
}

func TestRedactGitOutput(t *testing.T) {
	args := HTTPBasicAuthConfig("private-user", "private-password")
	encoded := strings.TrimPrefix(args[1], "http.extraheader=Authorization: Basic ")
	raw := "remote reflected private-user private-password " + encoded +
		" https://legacy:legacy-password@git.example/repo.git" +
		" https:opaque-user:opaque-password@opaque.example/repo.git"
	got := redactGitOutput(raw, args)
	for _, secret := range []string{"private-user", "private-password", encoded, "legacy-password", "opaque-user", "opaque-password"} {
		if strings.Contains(got, secret) {
			t.Errorf("redactGitOutput leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "git.example/repo.git") {
		t.Errorf("redactGitOutput removed non-secret context: %q", got)
	}
	if !strings.Contains(got, "opaque.example/repo.git") {
		t.Errorf("redactGitOutput removed opaque URL context: %q", got)
	}
}

func TestMirrorRefreshesSanitizedOrigin(t *testing.T) {
	ctx := context.Background()
	origin1 := filepath.Join(t.TempDir(), "one.git")
	origin2 := filepath.Join(t.TempDir(), "two.git")
	for _, origin := range []string{origin1, origin2} {
		if out, err := exec.Command("git", "init", "--bare", origin).CombinedOutput(); err != nil {
			t.Fatalf("git init --bare: %v\n%s", err, out)
		}
	}

	mirror := filepath.Join(t.TempDir(), "mirror.git")
	if err := Mirror(ctx, "file://"+origin1, mirror, HTTPBasicAuthConfig("git-bot", "transient-secret")...); err != nil {
		t.Fatalf("initial mirror: %v", err)
	}
	configBytes, err := os.ReadFile(filepath.Join(mirror, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configBytes), "transient-secret") || strings.Contains(string(configBytes), "extraheader") || strings.Contains(string(configBytes), "Authorization") {
		t.Fatalf("transient auth persisted in mirror config:\n%s", configBytes)
	}
	if _, err := runGit(ctx, mirror, "remote", "set-url", "origin", "https://legacy:secret@example.invalid/repo.git"); err != nil {
		t.Fatal(err)
	}
	if err := Mirror(ctx, "file://"+origin2, mirror); err != nil {
		t.Fatalf("refresh mirror: %v", err)
	}
	origin, err := runGit(ctx, mirror, "config", "--get", "remote.origin.url")
	if err != nil {
		t.Fatal(err)
	}
	if want := "file://" + origin2; origin != want {
		t.Errorf("remote.origin.url = %q, want %q", origin, want)
	}
	if strings.Contains(origin, "legacy") || strings.Contains(origin, "secret") {
		t.Errorf("remote origin retained credentials: %q", origin)
	}
}
