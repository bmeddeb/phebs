package sync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	stdsync "sync"

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

// urlCredRE matches userinfo in a URL (scheme://user:pass@host) so a clone
// URL with embedded credentials — the only auth path for a private type:git
// repo, since token is github-only — can be scrubbed from error strings.
var urlCredRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s]+@`)

// redactArgs joins argv for error messages with credentials scrubbed — errors
// are persisted to the job row and logged, so neither the GitHub PAT (injected
// via `-c http.extraheader=Authorization: …`) nor userinfo embedded in a clone
// URL may appear there.
func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.Contains(a, "http.extraheader") || strings.Contains(a, "Authorization") {
			out[i] = "http.extraheader=<redacted>"
			continue
		}
		out[i] = urlCredRE.ReplaceAllString(a, "$1<redacted>@")
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

// mirrorLocks serializes git operations per mirror dir: a webhook fetch and
// a connection sync run on independent job runners and would otherwise race
// git's ref locks on the same bare repo (Epic 7 review). In-process is
// enough — one phebs owns $DATA.
var mirrorLocks stdsync.Map

// Mirror clones cloneURL as a bare mirror into dir, or incrementally fetches
// if the mirror already exists. gitCfg holds per-invocation `-c` flags (e.g.
// auth headers) that must never persist into the mirror's config.
func Mirror(ctx context.Context, cloneURL, dir string, gitCfg ...string) error {
	mu, _ := mirrorLocks.LoadOrStore(dir, &stdsync.Mutex{})
	mu.(*stdsync.Mutex).Lock()
	defer mu.(*stdsync.Mutex).Unlock()

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
