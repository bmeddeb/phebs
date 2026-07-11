package sync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/store"
)

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		werr := fmt.Errorf("git %s: %w\n%s", redactArgs(args), err, out.String())
		if isAuthFailure(out.String()) {
			return "", store.WithClass(store.ClassAuth, werr)
		}
		return "", werr
	}
	return strings.TrimSpace(out.String()), nil
}

// redactArgs joins argv for error messages with the credential-bearing
// `-c http.extraheader=Authorization: …` value scrubbed — errors are
// persisted to the job row and logged, so the PAT must never appear there.
func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.Contains(a, "http.extraheader") || strings.Contains(a, "Authorization") {
			out[i] = "http.extraheader=<redacted>"
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}

// isAuthFailure sniffs git's credential complaints (T3.3 clone-auth class).
func isAuthFailure(output string) bool {
	for _, marker := range []string{
		"Authentication failed",
		"could not read Username",
		"could not read Password",
		"Permission denied",
		"Invalid username or token",
		"HTTP Basic: Access denied",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// Mirror clones cloneURL as a bare mirror into dir, or incrementally fetches
// if the mirror already exists. gitCfg holds per-invocation `-c` flags (e.g.
// auth headers) that must never persist into the mirror's config.
func Mirror(ctx context.Context, cloneURL, dir string, gitCfg ...string) error {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		_, err := runGit(ctx, dir, append(gitCfg, "fetch", "--prune", "origin")...)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}
	_, err := runGit(ctx, "", append(gitCfg, "clone", "--mirror", cloneURL, dir)...)
	return err
}

// GitConfig sets a config key in a repo (used for zoekt.name).
func GitConfig(ctx context.Context, dir, key, value string) (string, error) {
	return runGit(ctx, dir, "config", key, value)
}

// DefaultBranch reads the symref target of HEAD in a bare repo.
func DefaultBranch(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "symbolic-ref", "--short", "HEAD")
}

// Head returns the commit hash HEAD points at in a bare repo.
func Head(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "HEAD")
}
