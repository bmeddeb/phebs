package sync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

type reconcileStore struct {
	store.Store
	repo     store.Repo
	orphan   bool
	enqueued int
	cleared  int
}

func (s *reconcileStore) ListRepos(context.Context) ([]store.Repo, error) {
	return []store.Repo{s.repo}, nil
}

func (s *reconcileStore) UpsertRepo(_ context.Context, repo store.Repo) error {
	repo.Deleting = s.repo.Deleting
	s.repo = repo
	return nil
}

func (s *reconcileStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	connections := []string{"c"}
	if s.orphan {
		connections = nil
	}
	return []store.RepoStatus{{Repo: s.repo, Connections: connections, Orphaned: s.orphan}}, nil
}

func (s *reconcileStore) SetRepoDeleting(_ context.Context, _ string, deleting bool) error {
	s.repo.Deleting = deleting
	return nil
}

func (s *reconcileStore) EnqueuePending(_ context.Context, kind store.JobKind, target string, force bool) (*store.Job, error) {
	s.enqueued++
	return &store.Job{Kind: kind, Target: target, Force: force, Status: store.StatusPending}, nil
}

func (s *reconcileStore) ClearRepoIndexState(context.Context, string) error {
	s.cleared++
	s.repo.IndexedCommitHash = ""
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
