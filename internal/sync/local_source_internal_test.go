package sync

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveGenericGitSourceHomeRelative(t *testing.T) {
	homes := []string{t.TempDir(), t.TempDir()}
	for _, home := range homes {
		t.Setenv("HOME", home)
		source, err := resolveGenericGitSource("~/Uber/go-code.git")
		if err != nil {
			t.Fatal(err)
		}
		wantPath := filepath.Join(home, "Uber", "go-code.git")
		if source.cloneURL != wantPath || source.localPath != wantPath {
			t.Fatalf("resolved source = %+v, want operational path %q", source, wantPath)
		}
		if source.repoName != "local/Uber/go-code" {
			t.Fatalf("repository name = %q, want stable home-relative identity", source.repoName)
		}
	}
}

func TestResolveGenericGitSourceCompatibility(t *testing.T) {
	tests := []struct {
		name, raw, cloneURL, repoName, localPath, wantErr string
	}{
		{
			name: "absolute local",
			raw:  "/tmp/acme/repo.git", cloneURL: "/tmp/acme/repo.git",
			repoName: "local/tmp/acme/repo", localPath: "/tmp/acme/repo.git",
		},
		{
			name: "file local",
			raw:  "file:///tmp/acme/repo.git", cloneURL: "file:///tmp/acme/repo.git",
			repoName: "local/tmp/acme/repo", localPath: "/tmp/acme/repo.git",
		},
		{
			name: "remote",
			raw:  "https://github.com/acme/repo.git", cloneURL: "https://github.com/acme/repo.git",
			repoName: "github.com/acme/repo",
		},
		{
			name: "misleading file home",
			raw:  "file://~/acme/repo.git", wantErr: "file:// does not expand",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := resolveGenericGitSource(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolve error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if source.cloneURL != tt.cloneURL || source.repoName != tt.repoName ||
				source.localPath != tt.localPath {
				t.Fatalf("resolved source = %+v, want clone=%q repo=%q local=%q",
					source, tt.cloneURL, tt.repoName, tt.localPath)
			}
		})
	}
}
