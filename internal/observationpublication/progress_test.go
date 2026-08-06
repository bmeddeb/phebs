package observationpublication

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestProgressReportsCurrentReceiptAndWarmReadsNoMembers(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, source := progressFixture(t)
	planDirectory := filepath.Join(t.TempDir(), "plan")
	if err := os.Mkdir(planDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	partition, err := sourcepartition.Build(t.Context(), sourcepartition.BuildRequest{
		SourceDirectory: filepath.Join(dataDirectory, "index"), OutputDirectory: planDirectory,
		Repository: repository, Source: source,
		Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourcepartition.Open(t.Context(), planDirectory, partition)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := Publish(
		t.Context(), filepath.Join(dataDirectory, "observations"), repositoryDirectory, plan, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	reader := &ProgressReader{
		DataDir: dataDirectory, Store: &scheduleCapture{}, Cache: &Cache{},
	}
	progress, err := reader.Read(t.Context(), repository)
	if err != nil || progress.State != "current" || progress.Publication == nil ||
		progress.Publication.ReceiptState != "complete" || progress.Publication.Receipt == nil ||
		progress.Publication.Receipt.InputBlobs != 2 || progress.Publication.Receipt.UnsupportedBlobs != 1 {
		t.Fatalf("current progress = %+v, %v", progress, err)
	}
	member := filepath.Join(
		generationDirectory(filepath.Join(dataDirectory, "observations"), repository, publication.manifest.GenerationDigest),
		memberName(0),
	)
	if err := os.WriteFile(member, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	warm, err := reader.Read(t.Context(), repository)
	if err != nil || warm.Publication == nil || warm.Publication.Receipt == nil {
		t.Fatalf("warm progress reread members: %+v, %v", warm, err)
	}
	cold := &ProgressReader{DataDir: dataDirectory, Store: &scheduleCapture{}, Cache: &Cache{}}
	if _, err := cold.Read(t.Context(), repository); err == nil {
		t.Fatal("cold progress accepted corrupt publication member")
	}
}

func TestProgressShowsFailedScheduleAndDistinctRecovery(t *testing.T) {
	dataDirectory, _, repository, _ := progressFixture(t)
	capture := &recoveryScheduleCapture{}
	runtime := &Runtime{DataDir: dataDirectory, Store: capture}
	if err := runtime.Reconcile(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
	reader := &ProgressReader{DataDir: dataDirectory, Store: capture, Cache: &Cache{}}
	building, err := reader.Read(t.Context(), repository)
	if err != nil || building.State != "building" || building.Schedule == nil ||
		building.Schedule.State != "active" ||
		building.Schedule.ScheduleGeneration != building.Schedule.PublicationGeneration {
		t.Fatalf("building progress = %+v, %v", building, err)
	}
	target := building.Schedule.PublicationGeneration
	capture.current.Status = store.GenerationScheduleSettled
	capture.current.NextOffset = capture.current.TotalItems
	capture.current.Materialized = capture.current.TotalChunks
	capture.current.Failed = capture.current.TotalChunks
	capture.current.UpdatedAt = capture.current.UpdatedAt.Add(time.Second)
	failed, err := reader.Read(t.Context(), repository)
	if err != nil || failed.State != "failed" || failed.Schedule == nil ||
		failed.Schedule.Failed != failed.Schedule.TotalPartitions {
		t.Fatalf("failed progress = %+v, %v", failed, err)
	}
	if err := runtime.Reconcile(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
	recovery, err := reader.Read(t.Context(), repository)
	if err != nil || recovery.State != "building" || recovery.Schedule == nil ||
		recovery.Schedule.PublicationGeneration != target ||
		recovery.Schedule.ScheduleGeneration == target {
		t.Fatalf("recovery progress = %+v, %v", recovery, err)
	}
}

func progressFixture(t *testing.T) (string, string, string, repositoryindex.SourceManifest) {
	t.Helper()
	dataDirectory := t.TempDir()
	repository := "github.com/example/observation-progress"
	repositoryDirectory := filepath.Join(dataDirectory, "repos", filepath.FromSlash(repository)+".git")
	if err := os.MkdirAll(repositoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "init")
	runObservationGit(t, repositoryDirectory, "config", "user.email", "test@example.com")
	runObservationGit(t, repositoryDirectory, "config", "user.name", "Test")
	files := map[string][]byte{
		"main.go": []byte("package main\nfunc main() {}\n"),
		"bad.go":  []byte("not valid Go"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(repositoryDirectory, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runObservationGit(t, repositoryDirectory, "add", ".")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "fixture")
	commit := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, filepath.Join(dataDirectory, "index"), repository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return dataDirectory, repositoryDirectory, repository, source
}
