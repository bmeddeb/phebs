// Package main is the T19.1 Thrift protocol-pack validation spike. It
// prototypes the extraction rules for the future thriftdecl and thriftgo
// extractors against pinned real corpora and records the rule decisions the
// production tickets (T19.2/T19.3) must implement. Spike code is throwaway by
// charter: nothing here is imported by production packages.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CorpusEntry pins one repository to an exact commit. corpus.lock.json is the
// committed source of truth; clones land in spike/t191/corpus/ (gitignored).
type CorpusEntry struct {
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
	Commit string `json:"commit"`
	Note   string `json:"note,omitempty"`
}

func loadCorpusLock(lockPath string) ([]CorpusEntry, error) {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read corpus lock: %w", err)
	}
	var entries []CorpusEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("decode corpus lock: %w", err)
	}
	return entries, nil
}

// ensureCorpus clones the entry (if absent) and pins the working tree to the
// locked commit. GitHub serves reachable commits by SHA, so a plain fetch of
// the pinned object keeps the checkout reproducible even after upstream HEAD
// moves.
func ensureCorpus(root string, entry CorpusEntry) (string, error) {
	dir := filepath.Join(root, entry.Name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := runGit("", "clone", "--quiet", entry.GitURL, dir); err != nil {
			return "", err
		}
	}
	head, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if head != entry.Commit {
		if err := runGit(dir, "fetch", "--quiet", "origin", entry.Commit); err != nil {
			return "", err
		}
		if err := runGit(dir, "checkout", "--quiet", "--detach", entry.Commit); err != nil {
			return "", err
		}
	}
	// A corpus checkout without its own go.mod would otherwise join the
	// parent phebs module and pollute `go mod tidy` (jaeger-client-go
	// predates Go modules). A stub module fences it off.
	stub := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(stub); err != nil {
		content := "module corpus.invalid/" + entry.Name + "\n\ngo 1.21\n"
		if err := os.WriteFile(stub, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("fence corpus module: %w", err)
		}
	}
	return dir, nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// walkSuffix returns repo-relative paths under dir whose name ends in suffix,
// sorted by fs.WalkDir's lexical order. The .git directory is skipped.
func walkSuffix(dir, suffix string) ([]string, error) {
	var paths []string
	err := fs.WalkDir(os.DirFS(dir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, suffix) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}
	return paths, nil
}
