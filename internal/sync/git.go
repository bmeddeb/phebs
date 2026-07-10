package sync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// Mirror clones cloneURL as a bare mirror into dir, or incrementally fetches
// if the mirror already exists.
func Mirror(ctx context.Context, cloneURL, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		_, err := runGit(ctx, dir, "fetch", "--prune", "origin")
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}
	_, err := runGit(ctx, "", "clone", "--mirror", cloneURL, dir)
	return err
}

// DefaultBranch reads the symref target of HEAD in a bare repo.
func DefaultBranch(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "symbolic-ref", "--short", "HEAD")
}

// Head returns the commit hash HEAD points at in a bare repo.
func Head(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "HEAD")
}
