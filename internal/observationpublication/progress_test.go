package observationpublication

import (
	"context"
	"encoding/json"
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

func TestProgressReportsZeroUnsupportedReceipt(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, source := progressFixtureWithFiles(t, map[string][]byte{
		"main.go": []byte("package main\nfunc main() {}\n"),
	})
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
	manifest := publication.Manifest()
	if manifest.OperationReceipt == nil || manifest.OperationReceipt.UnsupportedBlobs != 0 ||
		manifest.OperationReceipt.UnsupportedReasons == nil || len(manifest.OperationReceipt.UnsupportedReasons) != 0 {
		t.Fatalf("zero-unsupported manifest receipt = %+v", manifest.OperationReceipt)
	}
	progress, err := (&ProgressReader{
		DataDir: dataDirectory, Store: &scheduleCapture{}, Cache: &Cache{},
	}).Read(t.Context(), repository)
	if err != nil || progress.State != "current" || progress.Publication == nil ||
		progress.Publication.Receipt == nil || progress.Publication.Receipt.UnsupportedBlobs != 0 ||
		progress.Publication.Receipt.UnsupportedReasons == nil ||
		len(progress.Publication.Receipt.UnsupportedReasons) != 0 {
		t.Fatalf("zero-unsupported progress = %+v, %v", progress, err)
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"unsupported_reasons":[]`) {
		t.Fatalf("zero-unsupported progress JSON = %s", encoded)
	}
}

func TestProgressOmitsArchivedSettledPlanningSidecar(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, source := progressFixture(t)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: func(context.Context) (func(), error) {
			return func() {}, nil
		},
	}
	if disposition, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		disposition != PlanningEnqueued {
		t.Fatalf("planning enqueue = %q, %v", disposition, err)
	}

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
	if _, err := Publish(
		t.Context(), filepath.Join(dataDirectory, "observations"), repositoryDirectory, plan, nil,
	); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	planning := state.current[PlanningScheduleStage]
	planning.NextOffset = planning.TotalItems
	planning.Materialized = planning.TotalChunks
	planning.Succeeded = planning.TotalChunks
	planning.Status = store.GenerationScheduleSettled
	planning.UpdatedAt = planning.UpdatedAt.Add(time.Nanosecond)
	state.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(dataDirectory, "observation-plans")); err != nil {
		t.Fatal(err)
	}

	progress, err := (&ProgressReader{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
	}).Read(t.Context(), repository)
	if err != nil || progress.State != "current" || progress.Publication == nil ||
		progress.Planning != nil {
		t.Fatalf("restored progress = %+v, %v", progress, err)
	}
}

func TestProgressRetainsSettledRecoveryScheduleAfterMarkerRemoval(t *testing.T) {
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
	if _, present, err := readMarker(filepath.Join(dataDirectory, "observations"), repository); err != nil || present {
		t.Fatalf("marker after publication: present=%t err=%v", present, err)
	}

	priorSchedule := "sha256:" + strings.Repeat("d", 64)
	target := publication.manifest.GenerationDigest
	scheduleGeneration := recoveryScheduleGeneration(target, priorSchedule)
	state := &recoveryScheduleCapture{}
	schedule, err := state.EnqueueGenerationSchedule(t.Context(), store.GenerationScheduleSpec{
		Repository: repository, Stage: ScheduleStage, Generation: scheduleGeneration,
		ResourceClass: store.GenerationResourceCPU, TotalItems: int64(len(partition.Members)),
		ChunkItems: ScheduleChunkItems, MaxAttempts: ScheduleMaxAttempts,
		RepositoryTokens: ScheduleRepositoryTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	state.current.NextOffset = state.current.TotalItems
	state.current.Materialized = state.current.TotalChunks
	state.current.Succeeded = state.current.TotalChunks
	state.current.Status = store.GenerationScheduleSettled
	state.current.UpdatedAt = state.current.UpdatedAt.Add(time.Second)
	runtime := &Runtime{DataDir: dataDirectory, Store: state}
	if err := runtime.writeScheduleBinding(scheduleBinding{
		Schema: BindingSchema, Repository: repository,
		ScheduleGeneration: scheduleGeneration, PublicationGeneration: target,
		PriorScheduleDigest: priorSchedule, SourceGenerationDigest: source.Digest,
		PartitionManifestDigest: partition.Digest,
	}); err != nil {
		t.Fatal(err)
	}
	runtime.cleanupSettledSchedule(t.Context(), repository, true)
	if _, err := runtime.readScheduleBinding(repository, schedule.Generation); err != nil {
		t.Fatalf("current settled binding was removed: %v", err)
	}

	progress, err := (&ProgressReader{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
	}).Read(t.Context(), repository)
	if err != nil || progress.State != "current" || progress.Publication == nil ||
		progress.Schedule == nil || progress.Schedule.State != "settled" ||
		progress.Schedule.ScheduleGeneration != scheduleGeneration ||
		progress.Schedule.PublicationGeneration != target ||
		progress.Schedule.Succeeded != progress.Schedule.TotalPartitions {
		t.Fatalf("settled current progress = %+v, %v", progress, err)
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

func TestProgressDistinguishesPlanningOwnershipAndFailure(t *testing.T) {
	dataDirectory, _, repository, _ := progressFixture(t)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: func(context.Context) (func(), error) {
			return func() {}, nil
		},
	}
	if disposition, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		disposition != PlanningEnqueued {
		t.Fatalf("planning enqueue = %q, %v", disposition, err)
	}
	reader := &ProgressReader{DataDir: dataDirectory, Store: state, Cache: &Cache{}}
	building, err := reader.Read(t.Context(), repository)
	if err != nil || building.State != "building" || building.Planning == nil ||
		building.Planning.State != "active" || building.Schedule != nil ||
		building.Planning.SourceGenerationDigest != building.SourceGenerationDigest {
		t.Fatalf("planning progress = %+v, %v", building, err)
	}
	state.settlePlanningFailure(t)
	failed, err := reader.Read(t.Context(), repository)
	if err != nil || failed.State != "failed" || failed.Planning == nil ||
		failed.Planning.State != "failed" || failed.Planning.Failed != 1 {
		t.Fatalf("failed planning progress = %+v, %v", failed, err)
	}
}

func TestProgressTreatsMismatchedPlanningAsStaleAuthority(t *testing.T) {
	repository := "github.com/example/observation-progress"
	currentSource := "sha256:" + strings.Repeat("a", 64)
	planningSource := "sha256:" + strings.Repeat("b", 64)
	planningGeneration := "sha256:" + strings.Repeat("c", 64)
	targetGeneration, _, err := planningTarget(repository, planningSource)
	if err != nil {
		t.Fatal(err)
	}
	projected := projectProgress(
		repository, currentSource, nil,
		&store.GenerationSchedule{
			Generation: planningGeneration, Status: store.GenerationScheduleActive,
		},
		planningBinding{
			TargetGeneration:       targetGeneration,
			SourceGenerationDigest: planningSource,
		},
		nil, "", "",
	)
	if projected.State != "stale" || projected.Planning == nil ||
		projected.Planning.SourceGenerationDigest != planningSource {
		t.Fatalf("mismatched planning projection = %+v", projected)
	}
	if err := ValidateProgress(projected); err != nil {
		t.Fatalf("stale planning projection is invalid: %v", err)
	}

	for _, state := range []string{"building", "failed"} {
		t.Run(state, func(t *testing.T) {
			crossed := projected
			crossed.State = state
			planningState := "active"
			if state == "failed" {
				planningState = "failed"
			}
			crossed.Planning = &PlanningProgress{
				State: planningState, ScheduleGeneration: planningGeneration,
				TargetGeneration:       targetGeneration,
				SourceGenerationDigest: planningSource,
			}
			if state == "failed" {
				crossed.Planning.Failed = 1
			}
			if err := ValidateProgress(crossed); err == nil {
				t.Fatal("crossed planning authority justified current-source progress")
			}
		})
	}
}

func TestProgressAbsentPlanningPreservesLegacyJSONBytes(t *testing.T) {
	progress := Progress{
		SchemaVersion:          ProgressSchema,
		Repository:             "github.com/example/observation-progress",
		State:                  "building",
		SourceGenerationDigest: "sha256:" + strings.Repeat("a", 64),
		Schedule: &ScheduleProgress{
			State:                 "active",
			ScheduleGeneration:    "sha256:" + strings.Repeat("b", 64),
			PublicationGeneration: "sha256:" + strings.Repeat("c", 64),
			TotalPartitions:       2, Materialized: 2, Pending: 1, Running: 1,
		},
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"phebs-observation-progress-v1","repository":"github.com/example/observation-progress","state":"building","source_generation_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","schedule":{"state":"active","schedule_generation":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","publication_generation":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","total_partitions":2,"materialized":2,"pending":1,"running":1,"succeeded":0,"failed":0}}`
	if string(encoded) != want {
		t.Fatalf("legacy progress JSON = %s\nwant %s", encoded, want)
	}
}

func progressFixture(t *testing.T) (string, string, string, repositoryindex.SourceManifest) {
	t.Helper()
	return progressFixtureWithFiles(t, map[string][]byte{
		"main.go": []byte("package main\nfunc main() {}\n"),
		"bad.go":  []byte("not valid Go"),
	})
}

func progressFixtureWithFiles(
	t *testing.T,
	files map[string][]byte,
) (string, string, string, repositoryindex.SourceManifest) {
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
