package sync

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestDeleteRepoArtifactsRemovesUnreadableOwnedFocusedShardFirst(t *testing.T) {
	dataDir := t.TempDir()
	repo := store.Repo{Name: "example.com/team/focused-orphan"}
	mirror := RepoDir(dataDir, repo.Name)
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(mirror, "HEAD"),
		[]byte("ref: refs/heads/main\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(dataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shard := filepath.Join(
		indexDir,
		focusedindex.RepositoryPrefix(repo.Name)+"corrupt.zoekt",
	)
	if err := os.WriteFile(shard, []byte("not a zoekt shard"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := &reconcileStore{repo: repo, orphan: true}
	deleted, err := deleteRepoArtifacts(t.Context(), st, dataDir, repo.Name)
	if err != nil || !deleted {
		t.Fatalf("deleteRepoArtifacts = %v, %v; want successful deletion", deleted, err)
	}
	if _, err := os.Lstat(shard); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreadable owned focused shard survived deletion: %v", err)
	}
	if !st.deleted {
		t.Fatal("repository row survived focused orphan deletion")
	}
}

func TestReconcileRecoversOnlyCommittedStaleFocusedMarker(t *testing.T) {
	tests := []struct {
		name   string
		tamper bool
	}{
		{"committed publication", false},
		{"invalid publication", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir, repo, indexDir, shard := staleFocusedPublication(t)
			if test.tamper {
				file, err := os.OpenFile(shard, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("tamper"); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			st := &reconcileStore{repo: repo}
			report, err := ReconcileArtifacts(t.Context(), st, dataDir, false)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(indexDir, focusedindex.PublishingName(repo.Name))
			if !test.tamper {
				if report.LifecycleArtifacts != 1 || report.RevisionRepairs != 0 ||
					st.cleared != 0 || st.enqueued != 0 {
					t.Fatalf(
						"committed marker report=%+v cleared=%d enqueued=%d",
						report, st.cleared, st.enqueued,
					)
				}
				if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("committed stale marker survived: %v", err)
				}
				if _, err := focusedindex.ValidatePublished(
					indexDir, repo.Name,
					st.repo.IndexedAnalysisUnit, st.repo.IndexedRevisions,
				); err != nil {
					t.Fatalf("reclaimed publication is not visible: %v", err)
				}
				return
			}
			if report.LifecycleArtifacts != 0 || report.RevisionRepairs != 1 ||
				st.cleared != 1 || st.enqueued != 1 || !st.enqueuedForce {
				t.Fatalf(
					"invalid marker report=%+v cleared=%d enqueued=%d force=%v",
					report, st.cleared, st.enqueued, st.enqueuedForce,
				)
			}
			if _, err := os.Lstat(marker); err != nil {
				t.Fatalf("invalid publication marker was prematurely removed: %v", err)
			}
			if st.repo.IndexedCommitHash != "" ||
				st.repo.IndexedAnalysisUnit != nil {
				t.Fatalf("invalid committed claim survived: %+v", st.repo)
			}
		})
	}
}

func staleFocusedPublication(
	t *testing.T,
) (string, store.Repo, string, string) {
	t.Helper()
	source := t.TempDir()
	runFocusedLifecycleGit(t, source, "init", "-b", "main")
	runFocusedLifecycleGit(t, source, "config", "user.name", "Test")
	runFocusedLifecycleGit(t, source, "config", "user.email", "test@example.com")
	if err := os.Mkdir(filepath.Join(source, "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "service", "main.go"),
		[]byte("package service\nconst MarkerRecoveryNeedle = true\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	runFocusedLifecycleGit(t, source, "add", ".")
	runFocusedLifecycleGit(t, source, "commit", "-m", "fixture")
	head := runFocusedLifecycleGit(t, source, "rev-parse", "HEAD")

	repository := "example.com/team/focused-marker"
	scope := analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
	}
	state, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD",
		Branch:   "HEAD",
		Commit:   head,
	}}
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := focusedindex.Build(t.Context(), focusedindex.Request{
		Schema:    focusedindex.RequestSchema,
		RepoDir:   source,
		OutputDir: stage,
		Scope:     scope,
		Revisions: revisions,
	}, focusedindex.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	manifest, err := focusedindex.ValidateStage(
		stage, repository, state, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	indexDir := filepath.Join(dataDir, "index")
	if err := os.Mkdir(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.PublishFocused(
		t.Context(), indexDir, stage, repository, state, revisions,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(indexDir, focusedindex.PublishingName(repository)),
		[]byte(repository+"\nprior-process-token\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	mirror := RepoDir(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "--bare", mirror).CombinedOutput(); err != nil {
		t.Fatalf("git init bare: %v\n%s", err, output)
	}
	return dataDir, store.Repo{
		Name:                repository,
		CloneURL:            "https://example.com/team/focused-marker.git",
		IndexedCommitHash:   head,
		IndexedRevisions:    revisions,
		IndexedAnalysisUnit: state,
	}, indexDir, filepath.Join(indexDir, manifest.Members[0].Name)
}

func runFocusedLifecycleGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
