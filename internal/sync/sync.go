// Package sync mirrors code-host repos into local bare repos (Epic 2).
//
// A connection sync does two things: resolve the connection to repo rows in
// the store, and mirror each repo to disk at $DATA/repos/<host>/<path>.git.
package sync

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
)

// RepoName derives the canonical repo name (host/path, no .git) from a clone
// URL. file:// URLs and plain absolute paths land under "local/". A portable
// ~/ path deliberately omits the machine-specific home prefix.
func RepoName(cloneURL string) (string, error) {
	if strings.HasPrefix(cloneURL, "~/") {
		_, relative, err := config.ResolveHomeRelativePath(cloneURL)
		if err != nil {
			return "", fmt.Errorf("resolve clone path %q: %w", cloneURL, err)
		}
		relative = strings.TrimSuffix(relative, ".git")
		if relative == "" {
			return "", fmt.Errorf("clone path %q has no repo path", cloneURL)
		}
		return safeName("local/"+relative, cloneURL)
	}
	safeURL, err := SanitizeURL(cloneURL)
	if err != nil {
		return "", errors.New("invalid clone URL")
	}
	cloneURL = safeURL
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

// safeName applies the canonical repository-name invariant to names derived
// from operator URLs and untrusted code-host API responses.
func safeName(name, cloneURL string) (string, error) {
	if err := ValidateRepoName(name); err != nil {
		return "", fmt.Errorf("clone url %q yields unsafe repo name: %w", cloneURL, err)
	}
	return name, nil
}

// ValidateRepoName rejects names that can escape, alias, or nest another
// mirror in the legacy host/path.git disk layout.
func ValidateRepoName(name string) error {
	if err := reponame.Validate(name); err != nil {
		return fmt.Errorf("unsafe repository name: %w", ErrBadInput)
	}
	return nil
}

// RepoDir is the deterministic on-disk location of a validated repo name.
func RepoDir(dataDir, name string) string {
	return filepath.Join(dataDir, "repos", name+".git")
}

// SafeRepoDir validates an untrusted/persisted repo name and refuses symlinked
// path components beneath the managed repos root before returning its path.
func SafeRepoDir(dataDir, name string) (string, error) {
	if err := ValidateRepoName(name); err != nil {
		return "", err
	}
	root, err := filepath.Abs(filepath.Join(dataDir, "repos"))
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, filepath.FromSlash(name)+".git")
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repository path escapes data directory: %w", ErrBadInput)
	}
	current := root
	components := strings.Split(filepath.Dir(rel), string(filepath.Separator))
	for _, component := range components {
		if component == "." || component == "" {
			continue
		}
		if info, statErr := os.Lstat(current); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("repository path contains symlink: %w", ErrBadInput)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return "", statErr
		}
		current = filepath.Join(current, component)
	}
	if info, statErr := os.Lstat(current); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("repository path contains symlink: %w", ErrBadInput)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	if info, statErr := os.Lstat(dir); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("repository mirror is a symlink: %w", ErrBadInput)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	return dir, nil
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
// A non-nil acl additionally mirrors private repos' host ACLs into
// permission edges (T10.3); type git has no ACL source and ignores it.
func SyncConnection(ctx context.Context, st store.Store, dataDir string, conn config.Connection, acl store.PermissionStore) ([]string, error) {
	switch conn.Type {
	case "git":
		return syncGeneric(ctx, st, dataDir, conn)
	case "github":
		return syncGitHub(ctx, st, dataDir, conn, acl)
	case "gitlab":
		return syncGitLab(ctx, st, dataDir, conn, acl)
	case "gitea":
		return syncGitea(ctx, st, dataDir, conn, acl)
	default:
		return nil, fmt.Errorf("connection %s: unsupported type %q", conn.Name, conn.Type)
	}
}

// syncGeneric mirrors the single repo a generic-git connection points at.
// Mirror first, upsert after: a failed clone must not leave a phantom row.
func syncGeneric(ctx context.Context, st store.Store, dataDir string, conn config.Connection) ([]string, error) {
	source, err := resolveGenericGitSource(conn.URL)
	if err != nil {
		return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
	}
	cloneURL, name := source.cloneURL, source.repoName
	dir, err := SafeRepoDir(dataDir, name)
	if err != nil {
		return nil, fmt.Errorf("connection %s: mirror path: %w", conn.Name, err)
	}
	unlock, err := repowork.LockContext(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("connection %s: lock mirror: %w", conn.Name, err)
	}
	defer unlock()
	if err := ensureRepoArtifactAvailable(ctx, st, dataDir, name, dir); err != nil {
		return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
	}
	gitCfg := HTTPBasicAuthConfig(conn.HTTPAuth.Username, conn.HTTPAuth.Password)
	if err := mirrorLocked(ctx, cloneURL, dir, gitCfg...); err != nil {
		return nil, fmt.Errorf("connection %s: mirror %s: %w", conn.Name, name, err)
	}
	if source.localPath != "" {
		followSourceHead(ctx, source.localPath, dir)
	}
	branch, err := DefaultBranch(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("connection %s: default branch of %s: %w", conn.Name, name, err)
	}
	repo := store.Repo{
		Name:             name,
		CloneURL:         cloneURL,
		DefaultBranch:    branch,
		ExternalHostType: "git",
	}
	if err := st.UpsertRepo(ctx, repo); err != nil {
		return nil, fmt.Errorf("connection %s: upsert %s: %w", conn.Name, name, err)
	}
	if err := st.SetRepoDeleting(ctx, name, false); err != nil {
		return nil, fmt.Errorf("connection %s: reactivate %s: %w", conn.Name, name, err)
	}
	return []string{name}, nil
}

type genericGitSource struct {
	cloneURL  string
	repoName  string
	localPath string
}

// resolveGenericGitSource keeps the configured ~/ spelling as the logical
// identity while returning an absolute operational path to Git and the
// watcher. No shell or filepath glob expansion occurs.
func resolveGenericGitSource(raw string) (genericGitSource, error) {
	cloneInput := raw
	localPath := ""
	homeRelative := strings.HasPrefix(raw, "~/")
	if homeRelative {
		absolute, _, err := config.ResolveHomeRelativePath(raw)
		if err != nil {
			return genericGitSource{}, err
		}
		cloneInput = absolute
		localPath = absolute
	} else if strings.HasPrefix(strings.ToLower(raw), "file://~") {
		return genericGitSource{}, errors.New("file:// does not expand '~'; use a quoted ~/ path instead")
	}

	cloneURL, err := SanitizeURL(cloneInput)
	if err != nil {
		return genericGitSource{}, err
	}
	nameInput := cloneURL
	if homeRelative {
		nameInput = raw
	}
	name, err := RepoName(nameInput)
	if err != nil {
		return genericGitSource{}, err
	}
	if localPath == "" && config.IsLocalURL(raw) {
		localPath = LocalPath(cloneURL)
	}
	return genericGitSource{
		cloneURL:  cloneURL,
		repoName:  name,
		localPath: localPath,
	}, nil
}

// mirrorAndUpsert serializes the runtime identity check, mirror mutation, and
// row publication. The lower-cased repowork key also fences case aliases on
// case-insensitive filesystems before either URL can reset the other's origin.
func mirrorAndUpsert(ctx context.Context, st store.Store, dataDir string, repo store.Repo, gitCfg ...string) error {
	dir, err := SafeRepoDir(dataDir, repo.Name)
	if err != nil {
		return fmt.Errorf("mirror path for %s: %w", repo.Name, err)
	}
	unlock, err := repowork.LockContext(ctx, dir)
	if err != nil {
		return fmt.Errorf("lock mirror for %s: %w", repo.Name, err)
	}
	defer unlock()
	if err := ensureRepoArtifactAvailable(ctx, st, dataDir, repo.Name, dir); err != nil {
		return err
	}
	if err := mirrorLocked(ctx, repo.CloneURL, dir, gitCfg...); err != nil {
		return fmt.Errorf("mirror %s: %w", repo.Name, err)
	}
	repo.WebURL, _ = SanitizeHTTPURL(repo.WebURL)
	repo.ExternalHostURL, _ = SanitizeHTTPURL(repo.ExternalHostURL)
	if err := st.UpsertRepo(ctx, repo); err != nil {
		return fmt.Errorf("upsert %s: %w", repo.Name, err)
	}
	if err := st.SetRepoDeleting(ctx, repo.Name, false); err != nil {
		return fmt.Errorf("reactivate %s: %w", repo.Name, err)
	}
	return nil
}

func ensureRepoArtifactAvailable(ctx context.Context, st store.Store, dataDir, name, dir string) error {
	candidate, ok := legacyArtifactKey(name)
	if !ok {
		return fmt.Errorf("repository %q has no safe artifact identity: %w", name, ErrBadInput)
	}
	repos, err := st.ListRepos(ctx)
	if err != nil {
		return fmt.Errorf("check repository artifact identity: %w", err)
	}
	for _, existing := range repos {
		if existing.Name == name {
			continue
		}
		artifact, artifactOK := legacyArtifactKey(existing.Name)
		if artifactOK && artifactsCollide(candidate, artifact) {
			return fmt.Errorf("repository %q conflicts with persisted repository %q: %w", name, existing.Name, ErrBadInput)
		}
	}
	root := filepath.Join(dataDir, "repos")
	if err := rejectCaseFoldDiskAlias(root, dir); err != nil {
		return fmt.Errorf("repository %q: %w", name, err)
	}
	return nil
}

func artifactsCollide(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func rejectCaseFoldDiskAlias(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact path escapes repository root: %w", ErrBadInput)
	}
	current := root
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		entries, readErr := os.ReadDir(current)
		if os.IsNotExist(readErr) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		candidateInfo, candidateErr := os.Lstat(filepath.Join(current, component))
		if candidateErr != nil && !os.IsNotExist(candidateErr) {
			return candidateErr
		}
		exact := false
		for _, entry := range entries {
			if entry.Name() == component {
				exact = true
				break
			}
			if candidateErr == nil {
				entryInfo, infoErr := entry.Info()
				if infoErr != nil {
					return infoErr
				}
				if os.SameFile(candidateInfo, entryInfo) {
					return fmt.Errorf("artifact component %q resolves to existing %q: %w", component, entry.Name(), ErrBadInput)
				}
			}
			if repowork.CanonicalKey(entry.Name()) == repowork.CanonicalKey(component) {
				return fmt.Errorf("artifact component %q aliases existing %q by filesystem identity: %w", component, entry.Name(), ErrBadInput)
			}
		}
		if !exact {
			return nil
		}
		current = filepath.Join(current, component)
	}
	return nil
}

// LocalPath strips the file:// scheme from a local clone URL.
func LocalPath(url string) string {
	if len(url) >= len("file://") && strings.EqualFold(url[:len("file://")], "file://") {
		return url[len("file://"):]
	}
	return url
}

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
		names, syncErr := SyncConnection(ctx, st, cfg.Server.DataDir, *conn, ACLStore(cfg, st))
		// Preserve partial progress: one persistently broken repository must
		// not prevent repositories already fetched during this run from being
		// indexed. Membership is still replaced only on total success.
		if err := enqueueIndexJobs(ctx, st, names); err != nil {
			return err
		}
		if syncErr != nil {
			return syncErr
		}
		if err := st.SetRepoConnections(ctx, conn.Name, names); err != nil {
			return err
		}
		if cfg.Sync.CleanupOrphans {
			_, err := ReconcileArtifacts(ctx, st, cfg.Server.DataDir, true)
			return err
		}
		return nil
	}
}

// enqueueIndexJobs chains indexing after a sync: one job per synced repo
// through one pending slot. Work already running gets one successor so a
// freshness event is not lost.
func enqueueIndexJobs(ctx context.Context, st store.Store, names []string) error {
	for _, name := range names {
		if err := store.EnqueueUnlessInFlight(ctx, st, store.JobIndex, name); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueMissing ensures one pending sync slot per configured connection.
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
// debounce: repeated ticks collapse into one pending successor, so slow syncs
// never pile up and still run once more after an in-flight snapshot.
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
	_, err := ReconcileArtifacts(ctx, st, dataDir, true)
	return err
}

// RemoveShards deletes a repo's zoekt shards from $DATA/index. Without this an
// orphaned repo's content stays searchable (the searcher keeps every on-disk
// shard mounted). Shards are named url.QueryEscape(name)_v<ver>.<n>.zoekt; the
// last _v separator identifies the exact escaped repository prefix; using a
// simple prefix test would make h/foo delete h/foo_victim. Zoekt's long-name
// truncation/hash scheme is mirrored below.
func RemoveShards(dataDir, name string) error {
	prefix := url.QueryEscape(name)
	if len(prefix) > 200 {
		hash := fmt.Sprintf("%x", sha1.Sum([]byte(prefix)))
		prefix = prefix[:200] + hash[:8]
	}
	shards, err := shardPaths(dataDir)
	if err != nil {
		return err
	}
	for _, s := range shards {
		base := strings.TrimSuffix(filepath.Base(s), ".zoekt")
		delimiter := strings.LastIndex(base, "_v")
		if delimiter < 0 || base[:delimiter] != prefix {
			continue
		}
		if err := os.Remove(s); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return focusedindex.RemoveRepository(
		context.Background(), filepath.Join(dataDir, "index"), name,
	)
}

func shardPaths(dataDir string) ([]string, error) {
	dir := filepath.Join(dataDir, "index")
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("managed index directory is a symlink")
	}
	if !info.IsDir() {
		return nil, errors.New("managed index path is not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("managed index contains symlink %q", entry.Name())
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zoekt") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths, nil
}
