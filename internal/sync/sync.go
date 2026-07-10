// Package sync mirrors code-host repos into local bare repos (Epic 2).
//
// A connection sync does two things: resolve the connection to repo rows in
// the store, and mirror each repo to disk at $DATA/repos/<host>/<path>.git.
package sync

import (
	"context"
	"fmt"
	"net/url"
	"os"
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

// SyncConnection resolves conn to repo rows, mirrors them to disk, and
// returns the names it synced (the connection's current membership set).
func SyncConnection(ctx context.Context, st store.Store, dataDir string, conn config.Connection) ([]string, error) {
	switch conn.Type {
	case "git":
		return syncGeneric(ctx, st, dataDir, conn)
	case "github":
		return syncGitHub(ctx, st, dataDir, conn)
	default:
		return nil, fmt.Errorf("connection %s: unsupported type %q", conn.Name, conn.Type)
	}
}

// syncGeneric mirrors the single repo a generic-git connection points at.
// Mirror first, upsert after: a failed clone must not leave a phantom row.
func syncGeneric(ctx context.Context, st store.Store, dataDir string, conn config.Connection) ([]string, error) {
	name, err := RepoName(conn.URL)
	if err != nil {
		return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
	}
	dir := RepoDir(dataDir, name)
	if err := Mirror(ctx, conn.URL, dir); err != nil {
		return nil, fmt.Errorf("connection %s: mirror %s: %w", conn.Name, name, err)
	}
	branch, err := DefaultBranch(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("connection %s: default branch of %s: %w", conn.Name, name, err)
	}
	repo := store.Repo{
		Name:             name,
		CloneURL:         conn.URL,
		DefaultBranch:    branch,
		ExternalHostType: "git",
	}
	if err := st.UpsertRepo(ctx, repo); err != nil {
		return nil, fmt.Errorf("connection %s: upsert %s: %w", conn.Name, name, err)
	}
	return []string{name}, nil
}

// Handler adapts connection syncing to the store.Runner: the job target is
// the connection name; membership is recorded after a successful sync, and
// orphan cleanup runs when the config enables it.
func Handler(cfg *config.Config, st store.Store) func(context.Context, store.Job) error {
	return func(ctx context.Context, job store.Job) error {
		var conn *config.Connection
		for i := range cfg.Connections {
			if cfg.Connections[i].Name == job.Target {
				conn = &cfg.Connections[i]
				break
			}
		}
		if conn == nil {
			return fmt.Errorf("connection %q no longer in config", job.Target)
		}
		names, err := SyncConnection(ctx, st, cfg.Server.DataDir, *conn)
		if err != nil {
			return err
		}
		if err := st.SetRepoConnections(ctx, conn.Name, names); err != nil {
			return err
		}
		if cfg.Sync.CleanupOrphans {
			return CleanupOrphans(ctx, st, cfg.Server.DataDir)
		}
		return nil
	}
}

// EnqueueMissing creates a sync job per configured connection unless one is
// already queued or in flight. ponytail: boot-time enqueue only; periodic
// re-sync cadence comes with the webhook/freshness work (P2 github-app).
func EnqueueMissing(ctx context.Context, st store.Store, cfg *config.Config) error {
	inFlight := map[string]bool{}
	for _, status := range []store.JobStatus{store.StatusPending, store.StatusClaimed, store.StatusRunning} {
		jobs, err := st.ListJobs(ctx, store.JobSync, status)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			inFlight[j.Target] = true
		}
	}
	for _, conn := range cfg.Connections {
		if inFlight[conn.Name] {
			continue
		}
		if _, err := st.CreateJob(ctx, store.JobSync, conn.Name); err != nil {
			return err
		}
	}
	return nil
}

// CleanupOrphans deletes repo rows and mirrors that no connection claims.
func CleanupOrphans(ctx context.Context, st store.Store, dataDir string) error {
	statuses, err := st.RepoStatuses(ctx)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		if !s.Orphaned {
			continue
		}
		if err := st.DeleteRepo(ctx, s.Name); err != nil {
			return fmt.Errorf("cleanup %s: %w", s.Name, err)
		}
		if err := os.RemoveAll(RepoDir(dataDir, s.Name)); err != nil {
			return fmt.Errorf("cleanup %s mirror: %w", s.Name, err)
		}
	}
	return nil
}
