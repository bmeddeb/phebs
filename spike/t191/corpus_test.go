package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyCorpusCheckoutBindsCommitAndTrackedBytes(t *testing.T) {
	newRepo := func(t *testing.T) (string, CorpusEntry) {
		t.Helper()
		dir := t.TempDir()
		if err := runGit(dir, "init", "--quiet"); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{
			"user.name": "phebs-test", "user.email": "phebs-test@example.invalid",
		} {
			if err := runGit(dir, "config", key, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "tracked.thrift"), []byte("service S {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runGit(dir, "add", "tracked.thrift"); err != nil {
			t.Fatal(err)
		}
		if err := runGit(dir, "commit", "--quiet", "-m", "fixture"); err != nil {
			t.Fatal(err)
		}
		commit, err := gitOutput(dir, "rev-parse", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return dir, CorpusEntry{Name: "fixture", Commit: commit}
	}

	t.Run("locked clean checkout permits untracked module fence", func(t *testing.T) {
		dir, entry := newRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module corpus.invalid/fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := verifyCorpusCheckout(dir, entry); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("tracked modification refuses", func(t *testing.T) {
		dir, entry := newRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "tracked.thrift"), []byte("service Changed {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := verifyCorpusCheckout(dir, entry)
		if err == nil || !strings.Contains(err.Error(), "tracked changes") {
			t.Fatalf("verify dirty checkout = %v", err)
		}
	})

	t.Run("wrong commit refuses", func(t *testing.T) {
		dir, entry := newRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "second.thrift"), []byte("service Other {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runGit(dir, "add", "second.thrift"); err != nil {
			t.Fatal(err)
		}
		if err := runGit(dir, "commit", "--quiet", "-m", "second"); err != nil {
			t.Fatal(err)
		}
		err := verifyCorpusCheckout(dir, entry)
		if err == nil || !strings.Contains(err.Error(), "does not match locked commit") {
			t.Fatalf("verify wrong commit = %v", err)
		}
	})
}
