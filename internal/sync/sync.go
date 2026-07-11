// Package sync mirrors code-host repos into local bare repos (Epic 2).
//
// A connection sync does two things: resolve the connection to repo rows in
// the store, and mirror each repo to disk at $DATA/repos/<host>/<path>.git.
package sync

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// RepoName derives the canonical repo name (host/path, no .git) from a clone
// URL. file:// URLs and plain absolute paths land under "local/".
func RepoName(cloneURL string) (string, error) {
	s := strings.TrimSuffix(cloneURL, ".git")
	if strings.HasPrefix(s, "/") {
		p := strings.Trim(filepath.Clean(s), "/")
		if p == "" {
			return "", fmt.Errorf("clone path %q has no repo path", cloneURL)
		}
		return safeName("local/"+p, cloneURL)
	}
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
		return safeName(host+"/"+p, cloneURL)
	}
	// scp-like syntax: [user@]host:path
	if h, p, ok := strings.Cut(s, ":"); ok && !strings.Contains(h, "/") && p != "" {
		if _, host, ok := strings.Cut(h, "@"); ok {
			h = host
		}
		return safeName(h+"/"+strings.Trim(p, "/"), cloneURL)
	}
	return "", fmt.Errorf("unrecognized clone url %q", cloneURL)
}

// safeName rejects names containing a ".." path segment. Without this the
// URL/scp branches (which don't filepath.Clean) could yield a name whose
// RepoDir escapes $DATA — and CleanupOrphans would then RemoveAll outside it.
func safeName(name, cloneURL string) (string, error) {
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", fmt.Errorf("clone url %q yields unsafe repo name %q", cloneURL, name)
		}
	}
	return name, nil
}

// RepoDir is the deterministic on-disk location of a repo's bare mirror.
func RepoDir(dataDir, name string) string {
	return filepath.Join(dataDir, "repos", name+".git")
}

// hostPrefix derives the repo-name prefix from a code-host base URL,
// keeping any subpath (an instance at example.com/gitea names repos
// "example.com/gitea/owner/name") so names align with what
// RepoName(clone_url) derives — webhook fetches match on that (T7.4).
func hostPrefix(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("base url %q has no host", base)
	}
	return strings.Trim(u.Host+u.Path, "/"), nil
}

// cloneOrigin returns the scheme and host a connection's clone URLs must
// live on; empty host means unconstrained (operator-configured git URLs).
func cloneOrigin(conn config.Connection) (scheme, host string) {
	switch conn.Type {
	case "github":
		return "https", "github.com"
	case "gitlab", "gitea":
		base := conn.URL
		if base == "" {
			base = "https://gitlab.com"
		}
		if u, err := url.Parse(base); err == nil {
			return u.Scheme, u.Host
		}
	}
	return "", ""
}

// checkCloneURL rejects a server-supplied clone URL pointing away from the
// connection's own origin: git sends the connection's credentials
// (http.extraheader) to whatever host it contacts, so a hostile or MITM'd
// code host must not be able to steer that traffic elsewhere (SSRF,
// Epic 7 review).
// testAllowOffOriginClones disables checkCloneURL in tests only — fixtures
// clone from file:// origins a real code host would never hand out.
var testAllowOffOriginClones = false

func checkCloneURL(conn config.Connection, cloneURL string) error {
	if testAllowOffOriginClones {
		return nil
	}
	scheme, host := cloneOrigin(conn)
	if host == "" {
		return nil
	}
	u, err := url.Parse(cloneURL)
	if err != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, host) {
		return fmt.Errorf("clone url %q is off-origin (connection %s expects %s://%s); refusing to send credentials",
			cloneURL, conn.Name, scheme, host)
	}
	return nil
}

// SyncConnection resolves conn to repo rows, mirrors them to disk, and
// returns the names it synced (the connection's current membership set).
func SyncConnection(ctx context.Context, st store.Store, dataDir string, conn config.Connection) ([]string, error) {
	switch conn.Type {
	case "git":
		return syncGeneric(ctx, st, dataDir, conn)
	case "github":
		return syncGitHub(ctx, st, dataDir, conn)
	case "gitlab":
		return syncGitLab(ctx, st, dataDir, conn)
	case "gitea":
		return syncGitea(ctx, st, dataDir, conn)
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
	if config.IsLocalURL(conn.URL) {
		followSourceHead(ctx, LocalPath(conn.URL), dir)
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

// LocalPath strips the file:// scheme from a local clone URL.
func LocalPath(url string) string { return strings.TrimPrefix(url, "file://") }

// followSourceHead points the mirror's HEAD at the branch the source repo
// has checked out — a watched working repo should be searched on the branch
// its owner is on (2026-07-09 ADR). Detached HEAD (mid-rebase, bisect)
// keeps the mirror's previous HEAD: the commit may not be on any fetched
// ref yet, and the next sync catches up.
func followSourceHead(ctx context.Context, sourcePath, mirrorDir string) {
	branchRef, err := runGit(ctx, sourcePath, "symbolic-ref", "HEAD")
	if err != nil {
		return
	}
	_, _ = runGit(ctx, mirrorDir, "symbolic-ref", "HEAD", branchRef)
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
		if err := enqueueIndexJobs(ctx, st, names); err != nil {
			return err
		}
		if cfg.Sync.CleanupOrphans {
			return CleanupOrphans(ctx, st, cfg.Server.DataDir)
		}
		return nil
	}
}

// enqueueIndexJobs chains indexing after a sync: one job per synced repo
// unless one is already queued or in flight. The indexer's short-circuit
// makes redundant jobs cheap.
func enqueueIndexJobs(ctx context.Context, st store.Store, names []string) error {
	for _, name := range names {
		if err := store.EnqueueUnlessInFlight(ctx, st, store.JobIndex, name); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueMissing creates a sync job per configured connection unless one is
// already queued or in flight.
func EnqueueMissing(ctx context.Context, st store.Store, cfg *config.Config) error {
	for _, conn := range cfg.Connections {
		if err := store.EnqueueUnlessInFlight(ctx, st, store.JobSync, conn.Name); err != nil {
			return err
		}
	}
	return nil
}

// IsRemote reports whether a connection's freshness depends on a remote
// host — local repos are covered by boot-time sync and watch mode.
func IsRemote(conn config.Connection) bool {
	return conn.Type != "git" || !config.IsLocalURL(conn.URL)
}

// Resync enqueues re-sync jobs for remote connections on a fixed cadence
// (T7.5) — freshness without webhooks. EnqueueUnlessInFlight is the
// debounce: a connection still syncing (or already queued) is skipped, so
// slow syncs never pile up.
func Resync(ctx context.Context, st store.Store, cfg *config.Config, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, conn := range cfg.Connections {
				if !IsRemote(conn) {
					continue
				}
				if err := store.EnqueueUnlessInFlight(ctx, st, store.JobSync, conn.Name); err != nil {
					log.Printf("resync: enqueue %s: %v", conn.Name, err)
				}
			}
		}
	}
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
		if err := RemoveShards(dataDir, s.Name); err != nil {
			return fmt.Errorf("cleanup %s shards: %w", s.Name, err)
		}
	}
	return nil
}

// RemoveShards deletes a repo's zoekt shards from $DATA/index. Without this an
// orphaned repo's content stays searchable (the searcher keeps every on-disk
// shard mounted). Shards are named url.QueryEscape(name)_v<ver>.<n>.zoekt; the
// _v separator anchors the glob so a name that prefixes another can't match
// its shards.
// ponytail: misses repos whose escaped name exceeds 200 chars (zoekt then
// truncates+hashes the prefix) — vanishingly rare; those shards leak as they
// did for every repo before this existed.
func RemoveShards(dataDir, name string) error {
	glob := filepath.Join(dataDir, "index", url.QueryEscape(name)+"_v*.zoekt")
	shards, err := filepath.Glob(glob)
	if err != nil {
		return err
	}
	for _, s := range shards {
		if err := os.Remove(s); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
