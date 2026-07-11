package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
)

// ReconcileReport summarizes one startup/runtime artifact audit.
type ReconcileReport struct {
	OrphanRepos      int
	UntrackedShards  int
	UntrackedMirrors int
	CredentialsFixed int
	InvalidRepos     int
	RevisionRepairs  int
	Deleted          int
}

// ErrCredentialAudit marks a startup invariant failure that may leave a
// credential-bearing legacy URL persisted in the database or mirror config.
var ErrCredentialAudit = errors.New("credential artifact audit failed")

// ReconcileArtifacts audits repository rows, mirrors, and zoekt shards. It
// always scrubs persisted URL userinfo; destructive cleanup is gated by
// cleanupEnabled. Confirmed orphan rows are marked deleting before disk work,
// which removes them from the production search RepoSet immediately.
func ReconcileArtifacts(ctx context.Context, st store.Store, dataDir string, cleanupEnabled bool) (ReconcileReport, error) {
	var report ReconcileReport
	var errs []error

	repos, err := st.ListRepos(ctx)
	if err != nil {
		return report, err
	}
	invalidNames := legacyLayoutCollisions(repos)
	for _, repo := range repos {
		nameErr := ValidateRepoName(repo.Name)
		if nameErr == nil {
			_, nameErr = SafeRepoDir(dataDir, repo.Name)
		}
		if nameErr != nil || invalidNames[repo.Name] {
			invalidNames[repo.Name] = true
			report.InvalidRepos++
			if err := st.SetRepoDeleting(ctx, repo.Name, true); err != nil {
				errs = append(errs, fmt.Errorf("quarantine invalid repo %q: %w", repo.Name, err))
			}
		}
		changed, err := scrubRepoCredentials(ctx, st, repo)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: repo %q: %v", ErrCredentialAudit, repo.Name, err))
		} else if changed {
			report.CredentialsFixed++
		}
	}
	if err := scrubMirrorCredentials(ctx, dataDir, repos, &report); err != nil {
		errs = append(errs, fmt.Errorf("%w: %v", ErrCredentialAudit, err))
	}
	if len(errs) > 0 {
		return report, errors.Join(errs...)
	}

	statuses, err := st.RepoStatuses(ctx)
	if err != nil {
		return report, errors.Join(append(errs, err)...)
	}
	for _, status := range statuses {
		if !status.Orphaned {
			continue
		}
		report.OrphanRepos++
		if !cleanupEnabled {
			continue
		}
		if invalidNames[status.Name] {
			continue // quarantined legacy collisions are never touched automatically
		}
		deleted, err := deleteRepoArtifacts(ctx, st, dataDir, status.Name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if deleted {
			report.Deleted++
		}
	}

	// Reload after row cleanup. Shards and mirrors without a row may have been
	// stranded by a crash or by older filename-based cleanup.
	repos, err = st.ListRepos(ctx)
	if err != nil {
		return report, errors.Join(append(errs, err)...)
	}
	live := make(map[string]bool, len(repos))
	liveMirrors := make(map[string]bool, len(repos))
	for _, repo := range repos {
		if !repo.Deleting || invalidNames[repo.Name] {
			live[repo.Name] = true
			if key, ok := legacyArtifactKey(repo.Name); ok {
				liveMirrors[key] = true
			}
		}
	}
	if err := reconcileUntrackedShards(dataDir, live, cleanupEnabled, &report); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileUntrackedMirrors(dataDir, liveMirrors, cleanupEnabled, &report); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileIndexedRevisions(ctx, st, dataDir, repos, &report); err != nil {
		errs = append(errs, err)
	}
	return report, errors.Join(errs...)
}

func legacyLayoutCollisions(repos []store.Repo) map[string]bool {
	invalid := map[string]bool{}
	byArtifact := map[string][]string{}
	artifactByName := map[string]string{}
	for _, repo := range repos {
		key, ok := legacyArtifactKey(repo.Name)
		if !ok {
			continue
		}
		artifactByName[repo.Name] = key
		byArtifact[key] = append(byArtifact[key], repo.Name)
	}
	for _, names := range byArtifact {
		if len(names) > 1 {
			for _, name := range names {
				invalid[name] = true
			}
		}
	}
	for name, key := range artifactByName {
		parts := strings.Split(key, "/")
		for i := 0; i < len(parts)-1; i++ {
			if !strings.HasSuffix(parts[i], ".git") {
				continue
			}
			parents := byArtifact[strings.Join(parts[:i+1], "/")]
			if len(parents) == 0 {
				continue
			}
			invalid[name] = true
			for _, parent := range parents {
				invalid[parent] = true
			}
		}
	}
	return invalid
}

func legacyArtifactKey(name string) (string, bool) {
	normalized := strings.ReplaceAll(name, `\`, "/")
	clean := pathpkg.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", false
	}
	return filepath.ToSlash(repowork.CanonicalKey(clean + ".git")), true
}

func deleteRepoArtifacts(ctx context.Context, st store.Store, dataDir, name string) (bool, error) {
	dir, err := SafeRepoDir(dataDir, name)
	if err != nil {
		return false, fmt.Errorf("refuse cleanup of %q: %w", name, err)
	}
	if err := st.SetRepoDeleting(ctx, name, true); err != nil {
		return false, fmt.Errorf("mark %s deleting: %w", name, err)
	}
	for _, kind := range []store.JobKind{store.JobFetch, store.JobIndex} {
		if _, err := st.CancelPendingJobs(ctx, kind, name); err != nil {
			return false, fmt.Errorf("cancel %s jobs for %s: %w", kind, name, err)
		}
	}

	unlock := repowork.Lock(dir)
	defer unlock()
	// Close the enqueue-before-lock window.
	for _, kind := range []store.JobKind{store.JobFetch, store.JobIndex} {
		if _, err := st.CancelPendingJobs(ctx, kind, name); err != nil {
			return false, fmt.Errorf("cancel late %s jobs for %s: %w", kind, name, err)
		}
	}
	current, err := st.GetRepo(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("recheck cleanup identity for %s: %w", name, err)
	}
	if !current.Deleting {
		return false, nil // a concurrent sync reactivated this repository
	}
	if err := removeRepoShardsByMetadata(dataDir, name); err != nil {
		return false, fmt.Errorf("cleanup %s shards: %w", name, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Errorf("cleanup %s mirror: %w", name, err)
	}
	if err := st.DeleteRepo(ctx, name); err != nil {
		return false, fmt.Errorf("cleanup %s row: %w", name, err)
	}
	return true, nil
}

func removeRepoShardsByMetadata(dataDir, name string) error {
	paths, err := shardPaths(dataDir)
	if err != nil {
		return err
	}
	for _, shard := range paths {
		repos, _, err := index.ReadMetadataPath(shard)
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(shard), err)
		}
		contains := false
		allTarget := len(repos) > 0
		for _, repo := range repos {
			contains = contains || repo.Name == name
			allTarget = allTarget && repo.Name == name
		}
		if contains && !allTarget {
			return fmt.Errorf("shard %s mixes %q with live repositories", filepath.Base(shard), name)
		}
		if allTarget {
			if err := os.Remove(shard); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func reconcileUntrackedShards(dataDir string, live map[string]bool, remove bool, report *ReconcileReport) error {
	paths, err := shardPaths(dataDir)
	if err != nil {
		return err
	}
	var errs []error
	for _, shard := range paths {
		repos, _, err := index.ReadMetadataPath(shard)
		if err != nil {
			errs = append(errs, fmt.Errorf("audit shard %s: %w", filepath.Base(shard), err))
			continue
		}
		orphan := len(repos) > 0
		for _, repo := range repos {
			if live[repo.Name] {
				orphan = false
				break
			}
		}
		if !orphan {
			continue
		}
		report.UntrackedShards++
		if remove {
			if err := os.Remove(shard); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			} else {
				report.Deleted++
			}
		}
	}
	return errors.Join(errs...)
}

func reconcileUntrackedMirrors(dataDir string, liveArtifacts map[string]bool, remove bool, report *ReconcileReport) error {
	root := filepath.Join(dataDir, "repos")
	return walkMirrorDirs(root, func(path string) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(strings.TrimSuffix(rel, ".git"))
		artifact, validArtifact := legacyArtifactKey(name)
		if !validArtifact || !liveArtifacts[artifact] {
			report.UntrackedMirrors++
			if remove {
				unlock := repowork.Lock(path)
				err = os.RemoveAll(path)
				unlock()
				if err != nil {
					return err
				}
				report.Deleted++
			}
		}
		return nil
	})
}

func reconcileIndexedRevisions(ctx context.Context, st store.Store, dataDir string, repos []store.Repo, report *ReconcileReport) error {
	paths, err := shardPaths(dataDir)
	if err != nil {
		return err
	}
	versions := map[string]map[string]bool{}
	var errs []error
	for _, shard := range paths {
		metadata, _, readErr := index.ReadMetadataPath(shard)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("audit revision %s: %w", filepath.Base(shard), readErr))
			continue
		}
		for _, indexedRepo := range metadata {
			set := versions[indexedRepo.Name]
			if set == nil {
				set = map[string]bool{}
				versions[indexedRepo.Name] = set
			}
			for _, branch := range indexedRepo.Branches {
				set[branch.Version] = true
			}
		}
	}

	for _, repo := range repos {
		if repo.Deleting || ValidateRepoName(repo.Name) != nil {
			continue
		}
		indexedVersions, hasShard := versions[repo.Name]
		mismatch := repo.IndexedCommitHash != "" && (!hasShard || len(indexedVersions) != 1 || !indexedVersions[repo.IndexedCommitHash])
		needsIndex := repo.IndexedCommitHash == "" || mismatch
		if !needsIndex {
			continue
		}
		if mismatch {
			if err := st.ClearRepoIndexState(ctx, repo.Name); err != nil {
				errs = append(errs, fmt.Errorf("clear mismatched index state for %s: %w", repo.Name, err))
				continue
			}
			report.RevisionRepairs++
		}
		dir, dirErr := SafeRepoDir(dataDir, repo.Name)
		if dirErr != nil {
			errs = append(errs, dirErr)
			continue
		}
		if _, statErr := os.Stat(filepath.Join(dir, "HEAD")); statErr != nil {
			if !os.IsNotExist(statErr) {
				errs = append(errs, statErr)
			}
			continue
		}
		if _, err := st.EnqueuePending(ctx, store.JobIndex, repo.Name, mismatch || hasShard); err != nil {
			errs = append(errs, fmt.Errorf("enqueue revision repair for %s: %w", repo.Name, err))
		}
	}
	return errors.Join(errs...)
}

func scrubRepoCredentials(ctx context.Context, st store.Store, repo store.Repo) (bool, error) {
	cloneURL, err := SanitizeURL(repo.CloneURL)
	if err != nil {
		cloneURL = "" // fail closed for malformed legacy values
	}
	webURL, err := SanitizeHTTPURL(repo.WebURL)
	if err != nil {
		webURL = ""
	}
	externalHostURL, err := SanitizeHTTPURL(repo.ExternalHostURL)
	if err != nil {
		externalHostURL = ""
	}
	if cloneURL == repo.CloneURL && webURL == repo.WebURL && externalHostURL == repo.ExternalHostURL {
		return false, nil
	}
	repo.CloneURL = cloneURL
	repo.WebURL = webURL
	repo.ExternalHostURL = externalHostURL
	if err := st.UpsertRepo(ctx, repo); err != nil {
		return false, err
	}
	return true, nil
}

func scrubMirrorCredentials(ctx context.Context, dataDir string, repos []store.Repo, report *ReconcileReport) error {
	root := filepath.Join(dataDir, "repos")
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed repos root is a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	seen := map[string]bool{}
	for _, repo := range repos {
		path, pathErr := legacyMirrorDir(dataDir, repo.Name)
		if pathErr != nil {
			if errors.Is(pathErr, ErrBadInput) {
				continue
			}
			return pathErr
		}
		if !isBareMirrorDir(path) {
			continue
		}
		changed, scrubErr := scrubMirrorAt(ctx, path)
		if scrubErr != nil {
			return scrubErr
		}
		seen[repowork.CanonicalKey(path)] = true
		if changed {
			report.CredentialsFixed++
		}
	}

	return walkMirrorDirs(root, func(path string) error {
		if !seen[repowork.CanonicalKey(path)] {
			changed, err := scrubMirrorAt(ctx, path)
			if err != nil {
				return err
			}
			if changed {
				report.CredentialsFixed++
			}
		}
		return nil
	})
}

func scrubMirrorAt(ctx context.Context, path string) (bool, error) {
	unlock := repowork.Lock(path)
	defer unlock()
	return scrubRemoteURLs(ctx, path, "origin")
}

func legacyMirrorDir(dataDir, name string) (string, error) {
	root, err := filepath.Abs(filepath.Join(dataDir, "repos"))
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, name+".git")
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("legacy mirror path escapes repository root: %w", ErrBadInput)
	}
	current := root
	if info, statErr := os.Lstat(current); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed repos root is a symlink")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			return dir, nil
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("legacy mirror path contains a symlink")
		}
	}
	return dir, nil
}

func isBareMirrorDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, "HEAD"))
	return err == nil && !info.IsDir()
}

var bareRepoInternalDirs = map[string]bool{
	"branches":  true,
	"hooks":     true,
	"info":      true,
	"lfs":       true,
	"logs":      true,
	"objects":   true,
	"refs":      true,
	"rr-cache":  true,
	"svn":       true,
	"worktrees": true,
}

// walkMirrorDirs follows managed namespace directories and visits every bare
// mirror, including legacy mirrors nested beneath an outer *.git directory.
// Once inside a bare repo it prunes Git's own storage trees, which may contain
// millions of loose objects, but continues through non-Git namespace paths.
func walkMirrorDirs(root string, visit func(string) error) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed repos root is a symlink")
	}
	if !info.IsDir() {
		return errors.New("managed repos path is not a directory")
	}
	var walk func(string, bool) error
	walk = func(dir string, atBareRoot bool) error {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("managed repos tree contains symlink %q", filepath.Join(dir, entry.Name()))
			}
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			isBare := strings.HasSuffix(entry.Name(), ".git") && isBareMirrorDir(path)
			if isBare {
				if err := visit(path); err != nil {
					return err
				}
				if err := walk(path, true); err != nil {
					return err
				}
				continue
			}
			if atBareRoot && bareRepoInternalDirs[entry.Name()] {
				continue
			}
			if err := walk(path, false); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root, false)
}

func scrubRemoteURLs(ctx context.Context, dir, remote string) (bool, error) {
	changed := false
	for _, key := range []string{"remote." + remote + ".url", "remote." + remote + ".pushurl"} {
		values, err := gitConfigValues(ctx, dir, key)
		if err != nil {
			return changed, err
		}
		clean := make([]string, 0, len(values))
		for _, value := range values {
			safe, sanitizeErr := SanitizeURL(value)
			if sanitizeErr != nil {
				changed = true
				continue
			}
			clean = append(clean, safe)
			changed = changed || safe != value
		}
		if equalStrings(values, clean) {
			continue
		}
		if len(values) > 0 {
			if _, err := runGit(ctx, dir, "config", "--unset-all", key); err != nil {
				return changed, err
			}
		}
		for _, value := range clean {
			if _, err := runGit(ctx, dir, "config", "--add", key, value); err != nil {
				return changed, err
			}
		}
	}
	return changed, nil
}

func gitConfigValues(ctx context.Context, dir, key string) ([]string, error) {
	value, err := runGit(ctx, dir, "config", "--get-all", key)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	if value == "" {
		return []string{""}, nil
	}
	return strings.Split(value, "\n"), nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
