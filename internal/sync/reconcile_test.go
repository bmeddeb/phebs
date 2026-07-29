package sync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
)

type reconcileStore struct {
	store.Store
	repo          store.Repo
	orphan        bool
	enqueued      int
	cleared       int
	deleted       bool
	canceledKinds []store.JobKind
}

func TestReconcileFocusedArtifactsRemovesOnlyOrphanOwnership(t *testing.T) {
	dataDir := t.TempDir()
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := "example.com/live"
	orphan := "example.com/orphan"
	liveArtifact := focusedindex.ArtifactBase(live) + ".manifest.json"
	orphanArtifacts := []string{
		focusedindex.ArtifactBase(orphan) + ".manifest.json",
		focusedindex.ArtifactBase(orphan) + ".publishing",
		focusedindex.ArtifactBase(orphan) + "-old.zoekt" + focusedindex.MemberSuffix,
	}
	for _, name := range append([]string{liveArtifact}, orphanArtifacts...) {
		if err := os.WriteFile(filepath.Join(indexDir, name), []byte("artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report := ReconcileReport{}
	if err := reconcileFocusedArtifacts(
		t.Context(), dataDir, map[string]bool{live: true}, true, &report,
	); err != nil {
		t.Fatal(err)
	}
	if report.UntrackedShards != len(orphanArtifacts) ||
		report.Deleted != len(orphanArtifacts) {
		t.Fatalf("focused cleanup report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(indexDir, liveArtifact)); err != nil {
		t.Fatalf("live focused artifact removed: %v", err)
	}
	for _, name := range orphanArtifacts {
		if _, err := os.Stat(filepath.Join(indexDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan focused artifact %q survived: %v", name, err)
		}
	}
}

func (s *reconcileStore) ListRepos(context.Context) ([]store.Repo, error) {
	if s.deleted {
		return nil, nil
	}
	return []store.Repo{s.repo}, nil
}

func (s *reconcileStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	if s.deleted || name != s.repo.Name {
		return nil, store.ErrNotFound
	}
	repo := s.repo
	return &repo, nil
}

func (s *reconcileStore) UpsertRepo(_ context.Context, repo store.Repo) error {
	repo.Deleting = s.repo.Deleting
	s.repo = repo
	return nil
}

func (s *reconcileStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	if s.deleted {
		return nil, nil
	}
	connections := []string{"c"}
	if s.orphan {
		connections = nil
	}
	return []store.RepoStatus{{Repo: s.repo, Connections: connections, Orphaned: s.orphan}}, nil
}

func (s *reconcileStore) SetRepoDeleting(_ context.Context, _ string, deleting bool) error {
	if s.deleted {
		return store.ErrNotFound
	}
	s.repo.Deleting = deleting
	return nil
}

func (s *reconcileStore) DeleteRepo(context.Context, string) error {
	s.deleted = true
	return nil
}

func (s *reconcileStore) CancelPendingJobs(_ context.Context, kind store.JobKind, _ string) (int, error) {
	s.canceledKinds = append(s.canceledKinds, kind)
	return 0, nil
}

func (s *reconcileStore) EnqueuePending(_ context.Context, kind store.JobKind, target string, force bool) (*store.Job, error) {
	s.enqueued++
	return &store.Job{Kind: kind, Target: target, Force: force, Status: store.StatusPending}, nil
}

func (s *reconcileStore) ClearRepoIndexState(context.Context, string) error {
	s.cleared++
	s.repo.IndexedCommitHash = ""
	s.repo.IndexedRevisions = nil
	s.repo.IndexedAt = nil
	return nil
}

func TestReconcileScrubsPersistedCredentials(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{
		Name:            "example.com/team/repo",
		CloneURL:        "https://user:top-secret@example.com/team/repo.git",
		WebURL:          "https://user:web-secret@example.com/team/repo?token=web#fragment",
		ExternalHostURL: "https://user:host-secret@example.com?token=host",
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", repo.CloneURL).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	st := &reconcileStore{repo: repo}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.CredentialsFixed < 1 {
		t.Fatalf("credentials fixed = %d, want at least one", report.CredentialsFixed)
	}
	if strings.Contains(st.repo.CloneURL, "top-secret") || strings.Contains(st.repo.CloneURL, "user:") {
		t.Errorf("stored repo URL still contains credentials: %q", st.repo.CloneURL)
	}
	for field, value := range map[string]string{"web_url": st.repo.WebURL, "external_host_url": st.repo.ExternalHostURL} {
		if strings.Contains(value, "secret") || strings.Contains(value, "user:") || strings.ContainsAny(value, "?#") {
			t.Errorf("stored %s still contains sensitive URL data: %q", field, value)
		}
	}
	out, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); strings.Contains(got, "top-secret") || strings.Contains(got, "user:") {
		t.Errorf("mirror origin still contains credentials: %q", got)
	}
}

func TestScrubMirrorCredentialsAuditsKnownNestedLegacyMirror(t *testing.T) {
	dataDir := t.TempDir()
	outerName := "example.com/team"
	outer := RepoDir(dataDir, outerName)
	nestedName := "example.com/team.git/repo"
	nested := RepoDir(dataDir, nestedName)
	for _, dir := range []string{outer, nested} {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init %s: %v\n%s", dir, err, out)
		}
	}
	secretURL := "https://user:nested-secret@example.com/repo.git"
	if out, err := exec.Command("git", "-C", nested, "remote", "add", "origin", secretURL).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	report := ReconcileReport{}
	if err := scrubMirrorCredentials(t.Context(), dataDir, []store.Repo{{Name: outerName}, {Name: nestedName}}, &report); err != nil {
		t.Fatal(err)
	}
	configBytes, err := os.ReadFile(filepath.Join(nested, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configBytes), "nested-secret") || strings.Contains(string(configBytes), "user:") {
		t.Fatalf("nested mirror retained credentials:\n%s", configBytes)
	}
}

func TestReconcileFindsUntrackedNestedLegacyMirror(t *testing.T) {
	dataDir := t.TempDir()
	outerName := "example.com/team"
	outer := RepoDir(dataDir, outerName)
	nested := filepath.Join(outer, "repo.git")
	for _, dir := range []string{outer, nested} {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init %s: %v\n%s", dir, err, out)
		}
	}
	secretURL := "https://user:untracked-secret@example.com/repo.git"
	if out, err := exec.Command("git", "-C", nested, "remote", "add", "origin", secretURL).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	st := &reconcileStore{repo: store.Repo{Name: outerName, CloneURL: "https://example.com/team.git"}}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.UntrackedMirrors != 1 {
		t.Fatalf("untracked mirrors = %d, want 1", report.UntrackedMirrors)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatalf("untracked nested mirror was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outer, "HEAD")); err != nil {
		t.Fatalf("live outer mirror was removed: %v", err)
	}
}

func TestReconcilePreservesCanonicallyEquivalentMirrorSpelling(t *testing.T) {
	dataDir := t.TempDir()
	repoName := "Host/Caf\u00e9"
	diskName := "host/cafe\u0301"
	dir := RepoDir(dataDir, diskName)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	st := &reconcileStore{repo: store.Repo{Name: repoName, CloneURL: "https://example.com/repo.git"}}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.UntrackedMirrors != 0 {
		t.Fatalf("canonically equivalent mirror reported untracked: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		t.Fatalf("canonically equivalent live mirror was removed: %v", err)
	}
}

func TestReconcilePreservesSSHUsername(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{
		Name:     "example.com/team/repo",
		CloneURL: "ssh://git@example.com/team/repo.git",
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", repo.CloneURL).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	st := &reconcileStore{repo: repo}
	if _, err := ReconcileArtifacts(t.Context(), st, dataDir, false); err != nil {
		t.Fatal(err)
	}
	if st.repo.CloneURL != repo.CloneURL {
		t.Fatalf("stored SSH URL = %q, want %q", st.repo.CloneURL, repo.CloneURL)
	}
	origin, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(origin)); got != repo.CloneURL {
		t.Fatalf("mirror SSH URL = %q, want %q", got, repo.CloneURL)
	}
}

func TestReconcileScrubsEveryOriginAndPushURL(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{Name: "example.com/team/repo", CloneURL: "https://example.com/team/repo.git"}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	commands := [][]string{
		{"config", "--add", "remote.origin.url", "https://one:secret-one@example.com/one.git"},
		{"config", "--add", "remote.origin.url", "https://example.com/two.git"},
		{"config", "--add", "remote.origin.pushurl", "https://two:secret-two@example.com/push.git"},
	}
	for _, args := range commands {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	st := &reconcileStore{repo: repo}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.CredentialsFixed == 0 {
		t.Fatal("mirror credentials were not reported as scrubbed")
	}
	configBytes, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-one", "secret-two", "one:", "two:"} {
		if strings.Contains(string(configBytes), secret) {
			t.Fatalf("mirror config retained %q:\n%s", secret, configBytes)
		}
	}
}

func TestReconcileClearsCommittedRevisionWithoutShard(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{
		Name:              "example.com/team/repo",
		CloneURL:          "https://example.com/team/repo.git",
		IndexedCommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	st := &reconcileStore{repo: repo}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.RevisionRepairs != 1 || st.cleared != 1 || st.enqueued != 1 {
		t.Fatalf("report=%+v cleared=%d enqueued=%d, want one revision repair", report, st.cleared, st.enqueued)
	}
	if st.repo.IndexedCommitHash != "" {
		t.Fatalf("indexed hash = %q, want fail-closed empty state", st.repo.IndexedCommitHash)
	}
}

func TestIndexStateMismatchMultipleRevisions(t *testing.T) {
	repo := store.Repo{
		IndexedCommitHash: "head-commit",
		IndexedRevisions: []store.IndexedRevision{
			{Selector: "HEAD", Branch: "HEAD", Commit: "head-commit"},
			{Selector: "release", Branch: "refs/heads/release", Commit: "release-commit"},
		},
	}
	tests := []struct {
		name     string
		versions map[string]string
		hasShard bool
		want     bool
	}{
		{"exact", map[string]string{"HEAD": "head-commit", "refs/heads/release": "release-commit"}, true, false},
		{"missing shard", nil, false, true},
		{"missing branch", map[string]string{"HEAD": "head-commit"}, true, true},
		{"extra branch", map[string]string{"HEAD": "head-commit", "refs/heads/release": "release-commit", "refs/tags/v1": "tag"}, true, true},
		{"moved branch", map[string]string{"HEAD": "head-commit", "refs/heads/release": "new"}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := indexStateMismatch(repo, tt.versions, tt.hasShard); got != tt.want {
				t.Errorf("indexStateMismatch = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconcileRevisionRepairWaitsForRepositoryLock(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{
		Name:              "example.com/team/repo",
		CloneURL:          "https://example.com/team/repo.git",
		IndexedCommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &reconcileStore{repo: repo}
	unlock := repowork.Lock(dir)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	report := ReconcileReport{}
	err := reconcileIndexedRevisions(ctx, st, dataDir, []store.Repo{repo}, &report)
	unlock()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reconcileIndexedRevisions error = %v, want context deadline", err)
	}
	if st.cleared != 0 || report.RevisionRepairs != 0 {
		t.Fatalf("state cleared without repository lock: report=%+v cleared=%d", report, st.cleared)
	}
}

func TestReconcilePreservesUnreadableShardAndCommittedState(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{
		Name:              "example.com/team/repo",
		CloneURL:          "https://example.com/team/repo.git",
		IndexedCommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shard := filepath.Join(indexDir, "half-written.zoekt")
	if err := os.WriteFile(shard, []byte("incomplete"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &reconcileStore{repo: repo}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, true)
	if err != nil {
		t.Fatalf("ReconcileArtifacts failed on unreadable shard: %v", err)
	}
	if st.cleared != 0 || report.RevisionRepairs != 0 {
		t.Fatalf("incomplete audit cleared committed state: report=%+v cleared=%d", report, st.cleared)
	}
	if _, err := os.Stat(shard); err != nil {
		t.Fatalf("unreadable shard was removed: %v", err)
	}
}

func TestDeleteRepoArtifactsReactivatesRowAfterDiskFailure(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{Name: "example.com/team/repo"}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "unreadable.zoekt"), []byte("incomplete"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &reconcileStore{repo: repo, orphan: true}
	deleted, err := deleteRepoArtifacts(t.Context(), st, dataDir, repo.Name)
	if err == nil || deleted {
		t.Fatalf("deleteRepoArtifacts = %v, %v; want disk audit failure", deleted, err)
	}
	if st.repo.Deleting {
		t.Fatal("failed cleanup left repository marked deleting")
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		t.Fatalf("failed cleanup removed mirror: %v", err)
	}
}

func TestDeleteRepoArtifactsCancelsExtractionJobs(t *testing.T) {
	st := &reconcileStore{repo: store.Repo{Name: "example.com/team/repo"}, orphan: true}
	deleted, err := deleteRepoArtifacts(t.Context(), st, t.TempDir(), st.repo.Name)
	if err != nil || !deleted {
		t.Fatalf("deleteRepoArtifacts = %v, %v; want successful deletion", deleted, err)
	}
	var extractCancellations int
	for _, kind := range st.canceledKinds {
		if kind == store.JobExtract {
			extractCancellations++
		}
	}
	if extractCancellations != 2 {
		t.Fatalf("extraction job cancellations = %d, want 2 (before and after repo lock)", extractCancellations)
	}
}

func TestReconcileHonorsCanceledContextBeforeFilesystemAudit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	visited := false
	err := walkMirrorDirs(ctx, t.TempDir(), func(string) error {
		visited = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("walkMirrorDirs error = %v, want context.Canceled", err)
	}
	if visited {
		t.Fatal("walkMirrorDirs visited filesystem entries after cancellation")
	}
}

func TestReconcileQuarantinesDotGitNamespace(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{
		Name:     "example.com/team.git/repo",
		CloneURL: "ssh://git@example.com/team.git/repo.git",
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	st := &reconcileStore{repo: repo}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.InvalidRepos != 1 || !st.repo.Deleting {
		t.Fatalf("report=%+v repo=%+v, want quarantined invalid row", report, st.repo)
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		t.Fatalf("quarantined nested mirror was removed: %v", err)
	}
}

func TestReconcileRefusesTraversalDeletion(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	victim := filepath.Join(base, "victim.git")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(victim, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &reconcileStore{
		repo:   store.Repo{Name: "../../victim", CloneURL: "https://example.com/victim.git"},
		orphan: true,
	}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, true)
	if err != nil || report.InvalidRepos != 1 {
		t.Fatalf("ReconcileArtifacts report=%+v err=%v, want quarantined invalid row", report, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup touched path outside data_dir: %v", err)
	}
}

func TestReconcileNeverFollowsSymlinkedReposRootForCleanup(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	outside := filepath.Join(base, "outside")
	victim := filepath.Join(outside, "example.com", "victim.git")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", victim).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dataDir, "repos")); err != nil {
		t.Fatal(err)
	}
	st := &reconcileStore{
		repo:   store.Repo{Name: "example.com/victim", CloneURL: "https://example.com/victim.git"},
		orphan: true,
	}
	if _, err := ReconcileArtifacts(t.Context(), st, dataDir, true); err == nil {
		t.Fatal("ReconcileArtifacts accepted a symlinked repos root")
	}
	if _, err := os.Stat(filepath.Join(victim, "HEAD")); err != nil {
		t.Fatalf("cleanup followed the repos-root symlink: %v", err)
	}
}

func TestLegacyLayoutCollisionsQuarantineEveryAlias(t *testing.T) {
	repos := []store.Repo{
		{Name: "host/team"},
		{Name: "host/team.git/repo"},
		{Name: "host/a/b"},
		{Name: "host/a/./b"},
		{Name: "host/Case/repo"},
		{Name: "host/case/repo"},
	}
	invalid := legacyLayoutCollisions(repos)
	for _, repo := range repos {
		if !invalid[repo.Name] {
			t.Errorf("%q was not quarantined from a colliding layout", repo.Name)
		}
	}
}

func TestEnsureRepoArtifactAvailableRejectsRuntimeCaseAlias(t *testing.T) {
	dataDir := t.TempDir()
	st := &reconcileStore{repo: store.Repo{Name: "host/Team/repo"}}
	dir, err := SafeRepoDir(dataDir, "host/team/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRepoArtifactAvailable(t.Context(), st, dataDir, "host/team/repo", dir); !errors.Is(err, ErrBadInput) {
		t.Fatalf("runtime alias error = %v, want ErrBadInput", err)
	}
}

func TestEnsureRepoArtifactAvailableRejectsUnicodeNormalizationAlias(t *testing.T) {
	dataDir := t.TempDir()
	st := &reconcileStore{repo: store.Repo{Name: "host/caf\u00e9/repo"}}
	name := "host/cafe\u0301/repo"
	dir, err := SafeRepoDir(dataDir, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRepoArtifactAvailable(t.Context(), st, dataDir, name, dir); !errors.Is(err, ErrBadInput) {
		t.Fatalf("Unicode normalization alias error = %v, want ErrBadInput", err)
	}
}

func TestEnsureRepoArtifactAvailableRejectsDiskCaseAlias(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "repos", "Host"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := &reconcileStore{repo: store.Repo{Name: "host/team/repo"}}
	dir, err := SafeRepoDir(dataDir, "host/team/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRepoArtifactAvailable(t.Context(), st, dataDir, "host/team/repo", dir); !errors.Is(err, ErrBadInput) {
		t.Fatalf("disk alias error = %v, want ErrBadInput", err)
	}
}
