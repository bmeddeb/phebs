package sync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

// ReconcileReport summarizes one startup/runtime artifact audit.
type ReconcileReport struct {
	OrphanRepos         int
	UntrackedShards     int
	UntrackedMirrors    int
	UntrackedCandidates int
	CredentialsFixed    int
	InvalidRepos        int
	RevisionRepairs     int
	LifecycleArtifacts  int
	Deleted             int
}

// ErrCredentialAudit marks a startup invariant failure that may leave a
// credential-bearing legacy URL persisted in the database or mirror config.
var ErrCredentialAudit = errors.New("credential artifact audit failed")

// ReconcileArtifacts audits repository rows, mirrors, and zoekt shards. It
// always scrubs persisted URL userinfo and reclaims prior-process private
// staging. Destructive orphan cleanup is gated by cleanupEnabled. Confirmed
// orphan rows are marked deleting before disk work, which removes them from
// the production search RepoSet immediately.
func ReconcileArtifacts(ctx context.Context, st store.Store, dataDir string, cleanupEnabled bool) (ReconcileReport, error) {
	var report ReconcileReport
	var errs []error
	if err := ctx.Err(); err != nil {
		return report, err
	}
	releaseMutation, err := focusedindex.AcquireMutationLock(
		ctx, filepath.Join(dataDir, "index"),
	)
	if err != nil {
		return report, fmt.Errorf("acquire index reconciliation lock: %w", err)
	}
	defer releaseMutation()

	lifecycle, err := focusedindex.CleanupAbandonedLifecycle(
		filepath.Join(dataDir, "index"),
	)
	if err != nil {
		return report, fmt.Errorf("reclaim abandoned index staging: %w", err)
	}
	report.LifecycleArtifacts += lifecycle.Workspaces + lifecycle.TemporaryMarkers
	report.Deleted += lifecycle.Workspaces + lifecycle.TemporaryMarkers

	repos, err := st.ListRepos(ctx)
	if err != nil {
		return report, err
	}
	invalidNames := legacyLayoutCollisions(repos)
	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(errs, err)...)
		}
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
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(errs, err)...)
		}
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
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(errs, err)...)
		}
		if !repo.Deleting || invalidNames[repo.Name] {
			live[repo.Name] = true
			if key, ok := legacyArtifactKey(repo.Name); ok {
				liveMirrors[key] = true
			}
		}
	}
	if err := reclaimCommittedPublicationMarkers(
		ctx, dataDir, repos, &report,
	); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileUntrackedShards(ctx, dataDir, live, cleanupEnabled, &report); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileFocusedArtifacts(ctx, dataDir, live, cleanupEnabled, &report); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileCandidateArtifacts(
		ctx, dataDir, live, cleanupEnabled, &report,
	); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileUntrackedMirrors(ctx, dataDir, liveMirrors, cleanupEnabled, &report); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileIndexedRevisions(ctx, st, dataDir, repos, &report); err != nil {
		errs = append(errs, err)
	}
	return report, errors.Join(errs...)
}

func reconcileCandidateArtifacts(
	ctx context.Context,
	dataDir string,
	live map[string]bool,
	remove bool,
	report *ReconcileReport,
) error {
	root := filepath.Join(dataDir, "candidates")
	names, err := candidate.ManagedArtifactNames(root)
	if err != nil {
		return fmt.Errorf("audit candidate artifacts: %w", err)
	}
	liveBases := make([]string, 0, len(live))
	for repository := range live {
		liveBases = append(liveBases, candidate.ArtifactBase(repository))
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		owned := false
		for _, base := range liveBases {
			if name == base+".manifest.json" ||
				name == base+".publishing" ||
				strings.HasPrefix(name, base+"-") {
				owned = true
				break
			}
		}
		if owned {
			continue
		}
		report.UntrackedCandidates++
		if !remove {
			continue
		}
		// ManagedArtifactNames is a non-recursive basename audit. Removing the
		// exact child with os.Remove cannot follow a symlink or recursively
		// traverse an attacker-controlled directory.
		if err := os.Remove(filepath.Join(root, name)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove orphan candidate artifact %q: %w", name, err)
		}
		report.Deleted++
	}
	return nil
}

func reconcileFocusedArtifacts(
	ctx context.Context,
	dataDir string,
	live map[string]bool,
	remove bool,
	report *ReconcileReport,
) error {
	indexDir := filepath.Join(dataDir, "index")
	entries, err := os.ReadDir(indexDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	liveBases := make([]string, 0, len(live))
	liveWholeManifests := make(map[string]bool, len(live))
	liveWholePrefixes := make([]string, 0, len(live))
	for repository := range live {
		liveBases = append(liveBases, focusedindex.ArtifactBase(repository))
		liveWholeManifests[focusedindex.WholeManifestName(repository)] = true
		liveWholePrefixes = append(
			liveWholePrefixes,
			focusedindex.WholeShardPrefix(repository)+"_v",
		)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if focusedindex.IsManagedShardName(name) {
			owned := false
			if strings.HasPrefix(name, "phebs-whole-") {
				for _, prefix := range liveWholePrefixes {
					if strings.HasPrefix(name, prefix) {
						owned = true
						break
					}
				}
			} else {
				for _, base := range liveBases {
					if strings.HasPrefix(name, base+"-") {
						owned = true
						break
					}
				}
			}
			if owned {
				continue
			}
			report.UntrackedShards++
			if remove {
				if err := os.Remove(filepath.Join(indexDir, name)); err != nil &&
					!errors.Is(err, os.ErrNotExist) {
					return err
				}
				report.Deleted++
			}
			continue
		}
		if strings.HasPrefix(name, "phebs-whole-") &&
			strings.HasSuffix(name, ".manifest.json") {
			if liveWholeManifests[name] {
				continue
			}
			report.UntrackedShards++
			if remove {
				if err := os.Remove(filepath.Join(indexDir, name)); err != nil &&
					!errors.Is(err, os.ErrNotExist) {
					return err
				}
				report.Deleted++
			}
			continue
		}
		if !strings.HasPrefix(name, "phebs-focus-") {
			continue
		}
		if strings.HasSuffix(name, ".zoekt") {
			if _, _, readErr := index.ReadMetadataPath(filepath.Join(indexDir, name)); readErr == nil {
				continue // reconcileUntrackedShards owns readable shards
			}
		}
		owned := false
		for _, base := range liveBases {
			if strings.HasPrefix(name, base) {
				owned = true
				break
			}
		}
		if owned {
			continue
		}
		report.UntrackedShards++
		if remove {
			if err := os.Remove(filepath.Join(indexDir, name)); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return err
			}
			report.Deleted++
		}
	}
	return nil
}

func reclaimCommittedPublicationMarkers(
	ctx context.Context,
	dataDir string,
	repos []store.Repo,
	report *ReconcileReport,
) error {
	indexDir := filepath.Join(dataDir, "index")
	var errs []error
	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if repo.Deleting || !focusedindex.IsPublishing(indexDir, repo.Name) ||
			focusedindex.PublicationMarkerOwnedByCurrentProcess(indexDir, repo.Name) {
			continue
		}
		committed := false
		if repo.IndexedAnalysisUnit != nil &&
			repo.IndexedAnalysisUnit.SearchIndexPosture == analysisunit.SearchIndexFocused {
			_, validateErr := focusedindex.ValidateCommittedPublication(
				indexDir, repo.Name,
				repo.IndexedAnalysisUnit, repo.IndexedRevisions,
			)
			committed = validateErr == nil
		} else {
			_, validateErr := focusedindex.ValidateCommittedWholePublication(
				ctx, indexDir, repo.Name, wholeRevisions(repo),
			)
			committed = validateErr == nil
		}
		if !committed {
			continue
		}
		if err := focusedindex.FinishPublication(indexDir, repo.Name); err != nil {
			errs = append(errs, fmt.Errorf(
				"reclaim committed publication marker for %s: %w",
				repo.Name, err,
			))
			continue
		}
		report.LifecycleArtifacts++
		report.Deleted++
	}
	return errors.Join(errs...)
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
	rollback := func(cause error) (bool, error) {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := st.SetRepoDeleting(rollbackCtx, name, false); err != nil && !errors.Is(err, store.ErrNotFound) {
			cause = errors.Join(cause, fmt.Errorf("reactivate %s after failed cleanup: %w", name, err))
		}
		return false, cause
	}
	for _, kind := range []store.JobKind{
		store.JobFetch, store.JobIndex, store.JobCandidate, store.JobExtract,
		store.JobResolverCatalog, store.JobCallerLeaf,
	} {
		if _, err := st.CancelPendingJobs(ctx, kind, name); err != nil {
			return rollback(fmt.Errorf("cancel %s jobs for %s: %w", kind, name, err))
		}
	}

	unlock, err := repowork.LockContext(ctx, dir)
	if err != nil {
		return rollback(err)
	}
	defer unlock()
	// Close the enqueue-before-lock window.
	for _, kind := range []store.JobKind{
		store.JobFetch, store.JobIndex, store.JobCandidate, store.JobExtract,
		store.JobResolverCatalog, store.JobCallerLeaf,
	} {
		if _, err := st.CancelPendingJobs(ctx, kind, name); err != nil {
			return rollback(fmt.Errorf("cancel late %s jobs for %s: %w", kind, name, err))
		}
	}
	current, err := st.GetRepo(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return rollback(fmt.Errorf("recheck cleanup identity for %s: %w", name, err))
	}
	if !current.Deleting {
		return false, nil // a concurrent sync reactivated this repository
	}
	legacyShards, err := planRepoShardsByMetadata(
		ctx, dataDir, name,
	)
	if err != nil {
		return rollback(fmt.Errorf(
			"preflight %s legacy shards: %w", name, err,
		))
	}
	// From this point onward, a failure leaves the row marked deleting. Disk
	// mutation may already have removed the authoritative receipt/member set,
	// so reactivation would expose a searchable row without its bytes.
	destructiveFailure := func(cause error) (bool, error) {
		return false, cause
	}
	if err := focusedindex.RemoveRepository(ctx, filepath.Join(dataDir, "index"), name); err != nil {
		return destructiveFailure(fmt.Errorf("cleanup %s shards: %w", name, err))
	}
	if err := removeRepoShardPaths(legacyShards); err != nil {
		return destructiveFailure(fmt.Errorf("cleanup %s legacy shards: %w", name, err))
	}
	if err := candidate.Remove(
		ctx, filepath.Join(dataDir, "candidates"), name,
	); err != nil {
		return destructiveFailure(fmt.Errorf("cleanup %s candidate artifacts: %w", name, err))
	}
	if err := resolvercatalog.RemoveRepository(
		ctx, filepath.Join(dataDir, "resolver-catalogs"), name,
	); err != nil {
		return destructiveFailure(fmt.Errorf("cleanup %s resolver catalog: %w", name, err))
	}
	// This literal is the same package-owned root returned by
	// callerexecute.Root. Importing callerexecute here would create a cycle
	// because caller execution consumes SafeRepoDir from this package.
	if err := callerleaf.RemoveRepository(
		ctx, filepath.Join(dataDir, "caller-leaves"), name,
	); err != nil {
		return destructiveFailure(fmt.Errorf("cleanup %s caller leaves: %w", name, err))
	}
	if err := ctx.Err(); err != nil {
		return destructiveFailure(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return destructiveFailure(fmt.Errorf("cleanup %s mirror: %w", name, err))
	}
	if err := st.DeleteRepo(ctx, name); err != nil {
		return destructiveFailure(fmt.Errorf("cleanup %s row: %w", name, err))
	}
	// T10.3: drop grants so a future repo reusing this name starts with none.
	// Best-effort — the row is gone, so stale edges grant nothing today.
	if ps, ok := st.(store.PermissionStore); ok {
		if err := ps.DeleteRepoPermissions(ctx, name); err != nil {
			log.Printf("prune permissions for %s: %v", name, err)
		}
	}
	return true, nil
}

func planRepoShardsByMetadata(
	ctx context.Context,
	dataDir, name string,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths, err := shardPaths(dataDir)
	if err != nil {
		return nil, err
	}
	var removals []string
	for _, shard := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if focusedindex.IsManagedShardName(filepath.Base(shard)) {
			// Cryptographic managed basenames are the sole ownership
			// authority. RemoveRepository handles the target namespace; never
			// let decoded metadata select another repository's managed path.
			continue
		}
		repos, _, err := index.ReadMetadataPath(shard)
		if err != nil {
			// Ownership of an unreadable legacy name is unknowable. Current
			// publications use repository-keyed canonical names and are removed
			// separately; preserve this legacy residue for reconciliation
			// rather than coupling another repository's deletion to it.
			continue
		}
		contains := false
		allTarget := len(repos) > 0
		for _, repo := range repos {
			contains = contains || repo.Name == name
			allTarget = allTarget && repo.Name == name
		}
		if contains && !allTarget {
			return nil, fmt.Errorf(
				"shard %s mixes %q with live repositories",
				filepath.Base(shard), name,
			)
		}
		if allTarget {
			removals = append(removals, shard)
		}
	}
	return removals, nil
}

func removeRepoShardPaths(paths []string) error {
	for _, shard := range paths {
		if err := os.Remove(shard); err != nil &&
			!os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func reconcileUntrackedShards(ctx context.Context, dataDir string, live map[string]bool, remove bool, report *ReconcileReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := shardPaths(dataDir)
	if err != nil {
		return err
	}
	var errs []error
	for _, shard := range paths {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if focusedindex.IsManagedShardName(filepath.Base(shard)) {
			// reconcileFocusedArtifacts classifies managed paths solely by
			// their cryptographic repository namespace.
			continue
		}
		repos, _, err := index.ReadMetadataPath(shard)
		if err != nil {
			// A builder can briefly expose an unreadable final path while swapping
			// shards. Never classify or remove a shard whose ownership is unknown.
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
			if err := ctx.Err(); err != nil {
				return errors.Join(append(errs, err)...)
			}
			if err := os.Remove(shard); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			} else {
				report.Deleted++
			}
		}
	}
	return errors.Join(errs...)
}

func reconcileUntrackedMirrors(ctx context.Context, dataDir string, liveArtifacts map[string]bool, remove bool, report *ReconcileReport) error {
	root := filepath.Join(dataDir, "repos")
	return walkMirrorDirs(ctx, root, func(path string) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(strings.TrimSuffix(rel, ".git"))
		artifact, validArtifact := legacyArtifactKey(name)
		if !validArtifact || !liveArtifacts[artifact] {
			report.UntrackedMirrors++
			if remove {
				if err := ctx.Err(); err != nil {
					return err
				}
				unlock, lockErr := repowork.LockContext(ctx, path)
				if lockErr != nil {
					return lockErr
				}
				if err := ctx.Err(); err != nil {
					unlock()
					return err
				}
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
	versions, complete, err := indexedVersions(ctx, dataDir)
	if err != nil {
		return err
	}
	var errs []error

	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if repo.Deleting || ValidateRepoName(repo.Name) != nil {
			continue
		}
		repoVersions, hasShard := versions[repo.Name]
		mismatch := repo.IndexedCommitHash != "" &&
			committedIndexMismatch(dataDir, repo, repoVersions, hasShard, complete)
		needsIndex := repo.IndexedCommitHash == "" || mismatch
		if !needsIndex {
			continue
		}
		dir, dirErr := SafeRepoDir(dataDir, repo.Name)
		if dirErr != nil {
			errs = append(errs, dirErr)
			continue
		}

		// The indexer owns this same lock from before the child starts through
		// shard publication and SetRepoIndexed. Re-read both sides while holding
		// it so a mid-swap snapshot can never clear a newly committed revision.
		unlock, lockErr := repowork.LockContext(ctx, dir)
		if lockErr != nil {
			errs = append(errs, lockErr)
			continue
		}
		fresh, freshErr := st.GetRepo(ctx, repo.Name)
		if errors.Is(freshErr, store.ErrNotFound) {
			unlock()
			continue
		}
		if freshErr != nil {
			unlock()
			errs = append(errs, fmt.Errorf("reload revision state for %s: %w", repo.Name, freshErr))
			continue
		}
		if fresh.Deleting {
			unlock()
			continue
		}

		force := hasShard || focusedindex.IsPublishing(filepath.Join(dataDir, "index"), repo.Name)
		if fresh.IndexedCommitHash != "" {
			freshVersions, freshComplete, auditErr := indexedVersions(ctx, dataDir)
			if auditErr != nil {
				unlock()
				errs = append(errs, auditErr)
				continue
			}
			freshSet, freshHasShard := freshVersions[repo.Name]
			if !committedIndexMismatch(
				dataDir, *fresh, freshSet, freshHasShard, freshComplete,
			) {
				unlock()
				continue
			}
			if err := st.ClearRepoIndexState(ctx, repo.Name); err != nil {
				unlock()
				errs = append(errs, fmt.Errorf("clear mismatched index state for %s: %w", repo.Name, err))
				continue
			}
			report.RevisionRepairs++
			force = true
		}
		if err := ctx.Err(); err != nil {
			unlock()
			errs = append(errs, err)
			continue
		}
		if _, statErr := os.Stat(filepath.Join(dir, "HEAD")); statErr != nil {
			unlock()
			if !os.IsNotExist(statErr) {
				errs = append(errs, statErr)
			}
			continue
		}
		if _, err := st.EnqueuePending(ctx, store.JobIndex, repo.Name, force); err != nil {
			errs = append(errs, fmt.Errorf("enqueue revision repair for %s: %w", repo.Name, err))
		}
		unlock()
	}
	return errors.Join(errs...)
}

func committedIndexMismatch(
	dataDir string,
	repo store.Repo,
	_ map[string]string,
	_, _ bool,
) bool {
	if repo.IndexedAnalysisUnit != nil &&
		repo.IndexedAnalysisUnit.SearchIndexPosture == analysisunit.SearchIndexFocused {
		_, err := focusedindex.ValidatePublished(
			filepath.Join(dataDir, "index"), repo.Name,
			repo.IndexedAnalysisUnit, repo.IndexedRevisions,
		)
		return err != nil
	}
	if _, err := focusedindex.ValidateWholeReceipt(
		filepath.Join(dataDir, "index"), repo.Name,
		wholeRevisions(repo),
	); err != nil {
		return true
	}
	// The canonical receipt validates this repository's exact member set and
	// decoded metadata. Global shard metadata is only a legacy inventory and
	// must not let another repository perturb this committed claim.
	return false
}

func wholeRevisions(repo store.Repo) []store.IndexedRevision {
	if len(repo.IndexedRevisions) == 0 && repo.IndexedCommitHash != "" {
		return []store.IndexedRevision{{
			Selector: "HEAD",
			Branch:   "HEAD",
			Commit:   repo.IndexedCommitHash,
		}}
	}
	return append([]store.IndexedRevision(nil), repo.IndexedRevisions...)
}

func indexedVersions(ctx context.Context, dataDir string) (map[string]map[string]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	paths, err := shardPaths(dataDir)
	if err != nil {
		return nil, false, err
	}
	versions := map[string]map[string]string{}
	complete := true
	for _, shard := range paths {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		metadata, _, readErr := index.ReadMetadataPath(shard)
		if readErr != nil {
			// An unreadable shard may be in the middle of an atomic builder swap.
			// Treat the snapshot as incomplete and decline destructive repairs.
			complete = false
			continue
		}
		for _, indexedRepo := range metadata {
			set := versions[indexedRepo.Name]
			if set == nil {
				set = map[string]string{}
				versions[indexedRepo.Name] = set
			}
			for _, branch := range indexedRepo.Branches {
				if prior, exists := set[branch.Name]; exists && prior != branch.Version {
					set[branch.Name] = "" // conflicting duplicate shards fail closed
				} else {
					set[branch.Name] = branch.Version
				}
			}
		}
	}
	return versions, complete, nil
}

func indexStateMismatch(repo store.Repo, versions map[string]string, hasShard bool) bool {
	if !hasShard {
		return true
	}
	expected := map[string]string{"HEAD": repo.IndexedCommitHash}
	if len(repo.IndexedRevisions) > 0 {
		expected = make(map[string]string, len(repo.IndexedRevisions))
		selectors := make(map[string]bool, len(repo.IndexedRevisions))
		validDefault := false
		for _, revision := range repo.IndexedRevisions {
			if revision.Selector == "" || revision.Branch == "" || revision.Commit == "" ||
				selectors[revision.Selector] || expected[revision.Branch] != "" {
				return true
			}
			selectors[revision.Selector] = true
			expected[revision.Branch] = revision.Commit
			if revision.Selector == "HEAD" {
				validDefault = revision.Branch == "HEAD" && revision.Commit == repo.IndexedCommitHash
			}
		}
		if !validDefault {
			return true
		}
	}
	if len(expected) != len(versions) {
		return true
	}
	for branch, commit := range expected {
		if versions[branch] != commit {
			return true
		}
	}
	return false
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
	if err := ctx.Err(); err != nil {
		return err
	}
	root := filepath.Join(dataDir, "repos")
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed repos root is a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	seen := map[string]bool{}
	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return err
		}
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

	return walkMirrorDirs(ctx, root, func(path string) error {
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
	unlock, err := repowork.LockContext(ctx, path)
	if err != nil {
		return false, err
	}
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
func walkMirrorDirs(ctx context.Context, root string, visit func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
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
