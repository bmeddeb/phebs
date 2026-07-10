// Package sync mirrors code-host repos into local bare repos (Epic 2).
//
// A connection sync does two things: resolve the connection to repo rows in
// the store, and mirror each repo to disk at $DATA/repos/<host>/<path>.git.
package sync

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// RepoName derives the canonical repo name (host/path, no .git) from a clone
// URL. file:// and other host-less URLs land under "local/".
func RepoName(cloneURL string) (string, error) {
	s := strings.TrimSuffix(cloneURL, ".git")
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("parse clone url %q: %w", cloneURL, err)
		}
		host := u.Host
		if host == "" {
			host = "local"
		}
		p := strings.Trim(u.Path, "/")
		if p == "" {
			return "", fmt.Errorf("clone url %q has no repo path", cloneURL)
		}
		return host + "/" + p, nil
	}
	// scp-like syntax: [user@]host:path
	if h, p, ok := strings.Cut(s, ":"); ok && !strings.Contains(h, "/") && p != "" {
		if _, host, ok := strings.Cut(h, "@"); ok {
			h = host
		}
		return h + "/" + strings.Trim(p, "/"), nil
	}
	return "", fmt.Errorf("unrecognized clone url %q", cloneURL)
}

// RepoDir is the deterministic on-disk location of a repo's bare mirror.
func RepoDir(dataDir, name string) string {
	return filepath.Join(dataDir, "repos", name+".git")
}

// SyncConnection resolves conn to repo rows and mirrors them to disk.
func SyncConnection(ctx context.Context, st store.Store, dataDir string, conn config.Connection) error {
	switch conn.Type {
	case "git":
		return syncGeneric(ctx, st, dataDir, conn)
	default:
		return fmt.Errorf("connection %s: unsupported type %q", conn.Name, conn.Type)
	}
}

// syncGeneric mirrors the single repo a generic-git connection points at.
// Mirror first, upsert after: a failed clone must not leave a phantom row.
func syncGeneric(ctx context.Context, st store.Store, dataDir string, conn config.Connection) error {
	name, err := RepoName(conn.URL)
	if err != nil {
		return fmt.Errorf("connection %s: %w", conn.Name, err)
	}
	dir := RepoDir(dataDir, name)
	if err := Mirror(ctx, conn.URL, dir); err != nil {
		return fmt.Errorf("connection %s: mirror %s: %w", conn.Name, name, err)
	}
	branch, err := DefaultBranch(ctx, dir)
	if err != nil {
		return fmt.Errorf("connection %s: default branch of %s: %w", conn.Name, name, err)
	}
	repo := store.Repo{
		Name:             name,
		CloneURL:         conn.URL,
		DefaultBranch:    branch,
		ExternalHostType: "git",
	}
	if err := st.UpsertRepo(ctx, repo); err != nil {
		return fmt.Errorf("connection %s: upsert %s: %w", conn.Name, name, err)
	}
	return nil
}
