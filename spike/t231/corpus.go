// Package t231 is the T23.1 Kafka topic-evidence validation spike. It
// prototypes the recognition, literal-or-abstain, and identity rules for the
// future kafkago extractor (Epic 23) against pinned real corpora and freezes
// the rule decisions the production tickets (T23.2/T23.3/T23.4) must
// implement. Spike code is throwaway by charter: nothing here is imported by
// production packages.
package t231

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CorpusEntry pins one repository to an exact commit. corpus.lock.json is the
// committed source of truth; clones land in spike/t231/corpus/ (gitignored)
// unless T231_CORPUS_ROOT points elsewhere.
type CorpusEntry struct {
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
	Commit string `json:"commit"`
	Note   string `json:"note,omitempty"`
}

// LoadCorpusLock reads the committed pin list.
func LoadCorpusLock(lockPath string) ([]CorpusEntry, error) {
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

// CorpusRoot returns the directory corpora live in. T231_CORPUS_ROOT overrides
// the default spike-local directory so an operator can reuse existing pinned
// clones instead of re-fetching.
func CorpusRoot(spikeDir string) string {
	if root := os.Getenv("T231_CORPUS_ROOT"); root != "" {
		return root
	}
	return filepath.Join(spikeDir, "corpus")
}

// EnsureCorpus clones the entry (if absent) and pins the working tree to the
// locked commit. Cadence is large, so clones are blob-filtered; the pinned
// checkout materializes every blob the gates read. GitHub serves reachable
// commits by SHA, so fetching the pinned object keeps the checkout
// reproducible after upstream HEAD moves.
func EnsureCorpus(root string, entry CorpusEntry) (string, error) {
	dir := filepath.Join(root, entry.Name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := runGit("", "clone", "--quiet", "--filter=blob:none", entry.GitURL, dir); err != nil {
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
	// predates Go modules). The exact stub is the only admitted untracked
	// corpus byte; VerifyCorpusCheckout rejects every other untracked or
	// ignored file because gates walk the filesystem.
	trackedModule, err := gitPathTracked(dir, "go.mod")
	if err != nil {
		return "", err
	}
	if !trackedModule {
		stub := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(stub); os.IsNotExist(err) {
			if err := os.WriteFile(stub, []byte(corpusFenceContent(entry)), 0o644); err != nil {
				return "", fmt.Errorf("fence corpus module: %w", err)
			}
		} else if err != nil {
			return "", fmt.Errorf("fence corpus module: %w", err)
		}
	}
	if err := VerifyCorpusCheckout(dir, entry); err != nil {
		return "", err
	}
	return dir, nil
}

// VerifyCorpusCheckout binds every executable gate to both the locked commit
// and the exact filesystem population. The sole exception is EnsureCorpus's
// content-checked go.mod fence for a commit that has no tracked go.mod.
func VerifyCorpusCheckout(dir string, entry CorpusEntry) error {
	head, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != entry.Commit {
		return fmt.Errorf("corpus %s HEAD %s does not match locked commit %s", entry.Name, head, entry.Commit)
	}
	dirty, err := gitOutput(dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	if dirty != "" {
		return fmt.Errorf("corpus %s has tracked changes; restore the locked checkout before running gates", entry.Name)
	}
	trackedModule, err := gitPathTracked(dir, "go.mod")
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	if !trackedModule {
		raw, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		switch {
		case readErr == nil && string(raw) != corpusFenceContent(entry):
			return fmt.Errorf("corpus %s has a modified go.mod fence", entry.Name)
		case readErr == nil:
			allowed["go.mod"] = true
		case !os.IsNotExist(readErr):
			return fmt.Errorf("corpus %s cannot read its go.mod fence: %w", entry.Name, readErr)
		}
	}
	others, err := corpusOtherPaths(dir)
	if err != nil {
		return err
	}
	for _, path := range others {
		if !allowed[path] {
			return fmt.Errorf("corpus %s has untracked or ignored path %q; remove it before running gates", entry.Name, path)
		}
		delete(allowed, path)
	}
	for path := range allowed {
		return fmt.Errorf("corpus %s expected untracked fence %q was not reported by git", entry.Name, path)
	}
	return nil
}

func corpusFenceContent(entry CorpusEntry) string {
	return "module corpus.invalid/" + entry.Name + "\n\ngo 1.21\n"
}

func gitPathTracked(dir, path string) (bool, error) {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", "--", path)
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		return true, nil
	} else if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	} else {
		return false, fmt.Errorf("git ls-files %s: %w", path, err)
	}
}

func corpusOtherPaths(dir string) ([]string, error) {
	seen := make(map[string]bool)
	for _, args := range [][]string{
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "-z"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		raw, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		for _, value := range strings.Split(string(raw), "\x00") {
			if value != "" {
				seen[filepath.ToSlash(value)] = true
			}
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
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
