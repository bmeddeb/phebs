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

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
)

type reconcileStore struct {
	store.Store
	repo          store.Repo
	orphan        bool
	enqueued      int
	enqueuedForce bool
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
	liveWholeArtifacts := []string{
		focusedindex.WholeManifestName(live),
		focusedindex.WholeShardName(live, 16, 0),
	}
	orphanArtifacts := []string{
		focusedindex.ArtifactBase(orphan) + ".manifest.json",
		focusedindex.ArtifactBase(orphan) + ".publishing",
		focusedindex.ArtifactBase(orphan) + "-old.zoekt" + focusedindex.MemberSuffix,
		focusedindex.WholeManifestName(orphan),
		focusedindex.WholeShardName(orphan, 16, 0),
	}
	names := append([]string{liveArtifact}, liveWholeArtifacts...)
	names = append(names, orphanArtifacts...)
	for _, name := range names {
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
	for _, name := range liveWholeArtifacts {
		if _, err := os.Stat(filepath.Join(indexDir, name)); err != nil {
			t.Fatalf("live whole artifact %q removed: %v", name, err)
		}
	}
	for _, name := range orphanArtifacts {
		if _, err := os.Stat(filepath.Join(indexDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan focused artifact %q survived: %v", name, err)
		}
	}
}

func TestReconcileManagedShardUsesNameNotDecodedMetadata(t *testing.T) {
	dataDir := t.TempDir()
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const orphanMetadata = "example.com/orphan-metadata"
	const liveNamespace = "example.com/live-namespace"
	managedPath := writeManagedShardWithMetadata(
		t,
		indexDir,
		focusedindex.WholeShardName(liveNamespace, 16, 0),
		orphanMetadata,
	)
	report := ReconcileReport{}
	live := map[string]bool{liveNamespace: true}
	if err := reconcileUntrackedShards(
		t.Context(), dataDir, live, true, &report,
	); err != nil {
		t.Fatal(err)
	}
	if err := reconcileFocusedArtifacts(
		t.Context(), dataDir, live, true, &report,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(managedPath); err != nil {
		t.Fatalf(
			"live B namespace was deleted from orphan A metadata: %v",
			err,
		)
	}
	if report.UntrackedShards != 0 || report.Deleted != 0 {
		t.Fatalf("managed-name reconciliation report = %+v", report)
	}
}

func TestReconcileCandidateArtifactsOnlyRemovesConfiguredOrphans(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "candidates")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	live := "example.com/live"
	liveArtifact := candidate.ManifestName(live)
	orphanArtifact := candidate.ManifestName("example.com/orphan")
	unrelated := "operator-note.txt"
	for _, name := range []string{liveArtifact, orphanArtifact, unrelated} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report := ReconcileReport{}
	if err := reconcileCandidateArtifacts(
		t.Context(), dataDir, map[string]bool{live: true}, false, &report,
	); err != nil {
		t.Fatal(err)
	}
	if report.UntrackedCandidates != 1 || report.Deleted != 0 {
		t.Fatalf("candidate audit report = %+v, want orphan retained", report)
	}
	for _, name := range []string{liveArtifact, orphanArtifact, unrelated} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("audit-only pass removed %q: %v", name, err)
		}
	}

	report = ReconcileReport{}
	if err := reconcileCandidateArtifacts(
		t.Context(), dataDir, map[string]bool{live: true}, true, &report,
	); err != nil {
		t.Fatal(err)
	}
	if report.UntrackedCandidates != 1 || report.Deleted != 1 {
		t.Fatalf("candidate cleanup report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, orphanArtifact)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan candidate artifact survived: %v", err)
	}
	for _, name := range []string{liveArtifact, unrelated} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("candidate cleanup removed %q: %v", name, err)
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
	s.enqueuedForce = force
	return &store.Job{Kind: kind, Target: target, Force: force, Status: store.StatusPending}, nil
}

func (s *reconcileStore) ClearRepoIndexState(context.Context, string) error {
	s.cleared++
	s.repo.IndexedCommitHash = ""
	s.repo.IndexedRevisions = nil
	s.repo.IndexedAnalysisUnit = nil
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

func TestReconcileClearsInvalidFocusedClaimAndForcesReplacement(t *testing.T) {
	dataDir := t.TempDir()
	repository := "example.com/team/focused"
	commit := strings.Repeat("a", 40)
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	repo := store.Repo{
		Name: repository, CloneURL: "https://example.com/team/focused.git",
		IndexedCommitHash: commit,
		IndexedRevisions: []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: commit,
		}},
		IndexedAnalysisUnit: unit,
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(indexDir, focusedindex.ManifestName(repository)),
		[]byte("{}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	st := &reconcileStore{repo: repo}
	report, err := ReconcileArtifacts(t.Context(), st, dataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.RevisionRepairs != 1 || st.cleared != 1 ||
		st.enqueued != 1 || !st.enqueuedForce {
		t.Fatalf(
			"report=%+v cleared=%d enqueued=%d force=%v, want one forced focused repair",
			report, st.cleared, st.enqueued, st.enqueuedForce,
		)
	}
	if st.repo.IndexedCommitHash != "" ||
		st.repo.IndexedAnalysisUnit != nil {
		t.Fatalf("invalid focused claim survived: %+v", st.repo)
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

func TestReconcilePreReceiptPublicationClearsAndQueuesReplacement(t *testing.T) {
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
	if st.cleared != 1 || report.RevisionRepairs != 1 ||
		st.enqueued != 1 || !st.enqueuedForce {
		t.Fatalf(
			"pre-receipt repair report=%+v cleared=%d enqueued=%d force=%v",
			report, st.cleared, st.enqueued, st.enqueuedForce,
		)
	}
	if _, err := os.Stat(shard); err != nil {
		t.Fatalf("unowned unreadable shard was removed: %v", err)
	}
}

func TestReconcileLocallyRepairsReceiptBoundUnreadableShard(
	t *testing.T,
) {
	dataDir := t.TempDir()
	commit := strings.Repeat("a", 40)
	repository := "example.com/team/receipt-bound"
	repo := store.Repo{
		Name:              repository,
		CloneURL:          "https://example.com/team/receipt-bound.git",
		IndexedCommitHash: commit,
	}
	dir := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(
		"git", "init", "--bare", dir,
	).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memberName := focusedindex.WholeShardName(repository, 16, 0)
	memberBytes := []byte("unreadable zoekt member")
	if err := os.WriteFile(
		filepath.Join(indexDir, memberName), memberBytes, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	manifest := focusedindex.WholeManifest{
		Schema:     focusedindex.WholeManifestSchema,
		Repository: repository,
		Revisions:  revisions,
		Members: []focusedindex.WholeShardMember{{
			Ordinal: 0, Count: 1, Name: memberName,
			ContentDigest:  "sha256:" + strings.Repeat("1", 64),
			ContentBytes:   int64(len(memberBytes)),
			MetadataDigest: "sha256:" + strings.Repeat("2", 64),
		}},
	}
	var err error
	manifest.Digest, err = focusedindex.WholeManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.WriteControlFile(
		filepath.Join(
			indexDir, focusedindex.WholeManifestName(repository),
		),
		manifest,
	); err != nil {
		t.Fatal(err)
	}
	st := &reconcileStore{repo: repo}
	report, err := ReconcileArtifacts(
		t.Context(), st, dataDir, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.cleared != 1 || report.RevisionRepairs != 1 ||
		st.enqueued != 1 || !st.enqueuedForce {
		t.Fatalf(
			"receipt-local repair: report=%+v cleared=%d enqueue=%d force=%v",
			report, st.cleared, st.enqueued, st.enqueuedForce,
		)
	}
	if _, err := os.Lstat(
		filepath.Join(indexDir, memberName),
	); err != nil {
		t.Fatalf("derived member was removed before replacement: %v", err)
	}
}

func TestWholeCommittedMismatchIgnoresForeignManagedMetadata(
	t *testing.T,
) {
	dataDir := t.TempDir()
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const repository = "example.com/team/local-receipt"
	const commit = "1111111111111111111111111111111111111111"
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	stageDir := buildWholeMetadataStage(
		t, repository, commit,
	)
	if err := focusedindex.PublishWhole(
		t.Context(), indexDir, stageDir, repository, revisions,
	); err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.FinishPublication(
		indexDir, repository,
	); err != nil {
		t.Fatal(err)
	}

	foreignStage := buildWholeMetadataStage(
		t, repository, strings.Repeat("2", 40),
	)
	foreignShards, err := filepath.Glob(
		filepath.Join(foreignStage, "*.zoekt"),
	)
	if err != nil || len(foreignShards) != 1 {
		t.Fatalf("foreign fixture shards = %v, %v", foreignShards, err)
	}
	if err := os.Rename(
		foreignShards[0],
		filepath.Join(
			indexDir,
			focusedindex.WholeShardName(
				"example.com/team/foreign-namespace", 16, 0,
			),
		),
	); err != nil {
		t.Fatal(err)
	}
	versions, complete, err := indexedVersions(t.Context(), dataDir)
	if err != nil || !complete {
		t.Fatalf("global metadata inventory = %v, complete=%v", err, complete)
	}
	if versions[repository]["HEAD"] != "" {
		t.Fatalf(
			"fixture did not create global conflict: %+v",
			versions[repository],
		)
	}
	repo := store.Repo{
		Name: repository, IndexedCommitHash: commit,
		IndexedRevisions: revisions,
	}
	if committedIndexMismatch(
		dataDir, repo, versions[repository], true, complete,
	) {
		t.Fatal("foreign managed metadata invalidated local whole receipt")
	}
}

func TestDeleteRepoArtifactsIgnoresUnrelatedUnreadableShard(t *testing.T) {
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
	manifestPath := filepath.Join(
		indexDir, focusedindex.WholeManifestName(repo.Name),
	)
	memberPath := filepath.Join(
		indexDir, focusedindex.WholeShardName(repo.Name, 16, 0),
	)
	for _, path := range []string{manifestPath, memberPath} {
		if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	foreignManagedPath := writeManagedShardWithMetadata(
		t,
		indexDir,
		focusedindex.WholeShardName(
			"example.com/team/foreign-owner", 16, 0,
		),
		repo.Name,
	)

	st := &reconcileStore{repo: repo, orphan: true}
	deleted, err := deleteRepoArtifacts(t.Context(), st, dataDir, repo.Name)
	if err != nil || !deleted {
		t.Fatalf(
			"deleteRepoArtifacts = %v, %v; want unrelated residue ignored",
			deleted, err,
		)
	}
	if !st.deleted {
		t.Fatal("repository row survived successful cleanup")
	}
	for _, path := range []string{manifestPath, memberPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned canonical artifact survived: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(
		filepath.Join(indexDir, "unreadable.zoekt"),
	); err != nil {
		t.Fatalf("unrelated unreadable residue was removed: %v", err)
	}
	if _, err := os.Stat(foreignManagedPath); err != nil {
		t.Fatalf(
			"foreign managed namespace was metadata-deleted with target: %v",
			err,
		)
	}
}

func TestDeleteRepoArtifactsCancelsCandidateAndExtractionJobs(t *testing.T) {
	dataDir := t.TempDir()
	st := &reconcileStore{repo: store.Repo{Name: "example.com/team/repo"}, orphan: true}
	candidateRoot := filepath.Join(dataDir, "candidates")
	if err := os.MkdirAll(candidateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(candidateRoot, candidate.ManifestName(st.repo.Name))
	if err := os.WriteFile(candidatePath, []byte("derived"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted, err := deleteRepoArtifacts(t.Context(), st, dataDir, st.repo.Name)
	if err != nil || !deleted {
		t.Fatalf("deleteRepoArtifacts = %v, %v; want successful deletion", deleted, err)
	}
	if _, err := os.Stat(candidatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository candidate artifact survived deletion: %v", err)
	}
	cancellations := map[store.JobKind]int{}
	for _, kind := range st.canceledKinds {
		cancellations[kind]++
	}
	for _, kind := range []store.JobKind{store.JobCandidate, store.JobExtract} {
		if cancellations[kind] != 2 {
			t.Fatalf(
				"%s cancellations = %d, want 2 (before and after repo lock)",
				kind, cancellations[kind],
			)
		}
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

func writeManagedShardWithMetadata(
	t *testing.T,
	indexDir, managedName, metadataRepository string,
) string {
	t.Helper()
	stageDir := buildWholeMetadataStage(
		t, metadataRepository, strings.Repeat("1", 40),
	)
	shards, err := filepath.Glob(filepath.Join(stageDir, "*.zoekt"))
	if err != nil || len(shards) != 1 {
		t.Fatalf("fixture shards = %v, %v", shards, err)
	}
	path := filepath.Join(indexDir, managedName)
	if err := os.Rename(shards[0], path); err != nil {
		t.Fatal(err)
	}
	return path
}

func buildWholeMetadataStage(
	t *testing.T,
	metadataRepository, commit string,
) string {
	t.Helper()
	stageDir := t.TempDir()
	builder, err := index.NewBuilder(index.Options{
		IndexDir:            stageDir,
		ShardPrefixOverride: "metadata-fixture",
		Parallelism:         1,
		DisableCTags:        true,
		RepositoryDescription: zoekt.Repository{
			Name: metadataRepository,
			Branches: []zoekt.RepositoryBranch{{
				Name: "HEAD", Version: commit,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(index.Document{
		Name:     "fixture.go",
		Content:  []byte("package fixture\nconst Needle = true\n"),
		Branches: []string{"HEAD"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	return stageDir
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
