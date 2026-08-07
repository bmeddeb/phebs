package observationpublication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestPublicationReusesOnlyExactContentAndReactivatesABA(t *testing.T) {
	repository, commitA := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
		"b.go": []byte("package demo\nconst B = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	planA := buildObservationPlan(t, repository, commitA, "example/observations", "a")
	var metricsA Metrics
	publicationA, err := Publish(t.Context(), root, repository, planA, &metricsA)
	if err != nil {
		t.Fatal(err)
	}
	if metricsA.ParsedBlobs != 2 || metricsA.ReusedObservations != 0 || publicationA.manifest.ObservedCount != 2 {
		t.Fatalf("A metrics/publication = %+v %+v", metricsA, publicationA.manifest)
	}
	if receipt := publicationA.manifest.OperationReceipt; receipt == nil ||
		receipt.InputBlobs != 2 || receipt.SourceBlobReads != 2 ||
		receipt.ParsedObservations != 2 || receipt.ReusedObservations != 0 {
		t.Fatalf("A operation receipt = %+v", receipt)
	}
	recordsA := publicationRecords(t, publicationA)
	walked := 0
	if err := publicationA.WalkObserved(
		t.Context(),
		func(record Record, observation sourceobservation.Observation) error {
			walked++
			if observation.ContentDigest != record.ContentDigest || len(record.Placements) == 0 {
				t.Fatalf("walked observation mismatch: %+v %+v", record, observation)
			}
			// The visitor receives a clone; mutation cannot alter publication authority.
			record.Placements[0].Revisions[0] = 99
			return nil
		},
	); err != nil || walked != publicationA.manifest.ObservedCount {
		t.Fatalf("walk observed = %d, %v", walked, err)
	}

	if err := os.WriteFile(filepath.Join(repository, "b.go"), []byte("package demo\nconst B = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repository, "add", "b.go")
	runObservationGit(t, repository, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repository, "rev-parse", "HEAD"))
	planB := buildObservationPlan(t, repository, commitB, "example/observations", "b")
	var metricsB Metrics
	publicationB, err := Publish(t.Context(), root, repository, planB, &metricsB)
	if err != nil {
		t.Fatal(err)
	}
	if metricsB.ParsedBlobs != 1 || metricsB.ReusedObservations != 1 || publicationB.manifest.GenerationDigest == publicationA.manifest.GenerationDigest {
		t.Fatalf("B metrics/publication = %+v %+v", metricsB, publicationB.manifest)
	}
	if receipt := publicationB.manifest.OperationReceipt; receipt == nil ||
		receipt.InputBlobs != 2 || receipt.SourceBlobReads != 2 ||
		receipt.ParsedObservations != 1 || receipt.ReusedObservations != 1 {
		t.Fatalf("B operation receipt = %+v", receipt)
	}
	recordsB := publicationRecords(t, publicationB)
	aA, aB := recordForPath(t, recordsA, "a.go"), recordForPath(t, recordsB, "a.go")
	bA, bB := recordForPath(t, recordsA, "b.go"), recordForPath(t, recordsB, "b.go")
	infoAA, err := os.Stat(filepath.Join(generationDirectory(root, publicationA.manifest.Repository, publicationA.manifest.GenerationDigest), aA.ObservationName))
	if err != nil {
		t.Fatal(err)
	}
	infoAB, err := os.Stat(filepath.Join(generationDirectory(root, publicationB.manifest.Repository, publicationB.manifest.GenerationDigest), aB.ObservationName))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(infoAA, infoAB) {
		t.Fatal("unchanged observation was not physically reused")
	}
	if bA.ContentDigest == bB.ContentDigest || bA.ObservationName == bB.ObservationName {
		t.Fatal("changed blob retained its prior observation identity")
	}

	var aba Metrics
	reactivated, err := Publish(t.Context(), root, repository, planA, &aba)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.manifest.Digest != publicationA.manifest.Digest || aba != (Metrics{}) {
		t.Fatalf("A reactivation = %+v metrics=%+v", reactivated.manifest, aba)
	}
	current, err := CurrentGeneration(root, "example/observations")
	if err != nil || current != publicationA.manifest.GenerationDigest {
		t.Fatalf("current A = %q, %v", current, err)
	}
}

func TestMarkerRecoveryPreservesPriorAndCacheDelaysRetirement(t *testing.T) {
	repository, commitA := observationFixture(t, map[string][]byte{"a.go": []byte("package demo\nconst A = 1\n")})
	root := filepath.Join(t.TempDir(), "observations")
	planA := buildObservationPlan(t, repository, commitA, "example/recovery", "a")
	publicationA, err := Publish(t.Context(), root, repository, planA, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := &Cache{}
	leaseA, err := cache.Acquire(t.Context(), root, "example/recovery")
	if err != nil {
		t.Fatal(err)
	}
	if !cache.Pinned("example/recovery", publicationA.manifest.GenerationDigest) {
		t.Fatal("active A lease did not pin its generation")
	}

	if err := os.WriteFile(filepath.Join(repository, "a.go"), []byte("package demo\nconst A = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repository, "add", "a.go")
	runObservationGit(t, repository, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repository, "rev-parse", "HEAD"))
	planB := buildObservationPlan(t, repository, commitB, "example/recovery", "b")
	publicationB, err := Publish(t.Context(), root, repository, planB, nil)
	if err != nil {
		t.Fatal(err)
	}
	leaseB, err := cache.Acquire(t.Context(), root, "example/recovery")
	if err != nil {
		t.Fatal(err)
	}
	if !cache.Pinned("example/recovery", publicationA.manifest.GenerationDigest) ||
		!cache.Pinned("example/recovery", publicationB.manifest.GenerationDigest) {
		t.Fatal("generation replacement lost an active lease pin")
	}
	leaseA.Release()
	if cache.Pinned("example/recovery", publicationA.manifest.GenerationDigest) {
		t.Fatal("released retired A lease remained pinned")
	}
	leaseB.Release()

	// An interrupted C stage cannot move the B pointer.
	if err := os.WriteFile(filepath.Join(repository, "a.go"), []byte("package demo\nconst A = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repository, "add", "a.go")
	runObservationGit(t, repository, "commit", "-m", "C")
	commitC := strings.TrimSpace(runObservationGit(t, repository, "rev-parse", "HEAD"))
	planC := buildObservationPlan(t, repository, commitC, "example/recovery", "c")
	stageC, err := Begin(root, planC.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	if len(planC.Manifest().Members) > 0 {
		if err := stageC.BuildPartition(t.Context(), planC, repository, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := Recover(t.Context(), root, "example/recovery")
	if err != nil || recovered {
		t.Fatalf("incomplete recovery = %t, %v", recovered, err)
	}
	current, err := CurrentGeneration(root, "example/recovery")
	if err != nil || current != publicationB.manifest.GenerationDigest {
		t.Fatalf("prior B pointer changed = %q, %v", current, err)
	}
	publicationC, err := Publish(t.Context(), root, repository, planC, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := activate(root, publicationB.manifest); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveCanonical(markerPath(root, "example/recovery"), Marker{
		Schema: MarkerSchema, Repository: "example/recovery",
		GenerationDigest: publicationC.manifest.GenerationDigest,
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err = Recover(t.Context(), root, "example/recovery")
	if err != nil || !recovered {
		t.Fatalf("complete recovery = %t, %v", recovered, err)
	}
	current, err = CurrentGeneration(root, "example/recovery")
	if err != nil || current != publicationC.manifest.GenerationDigest {
		t.Fatalf("complete C recovery pointer = %q, %v", current, err)
	}
}

func TestWarmCacheDoesNotRereadMemberAndCorruptColdOpenRefuses(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{"a.go": []byte("package demo\n")})
	root := filepath.Join(t.TempDir(), "observations")
	plan := buildObservationPlan(t, repository, commit, "example/cache", "a")
	publication, err := Publish(t.Context(), root, repository, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := &Cache{}
	first, err := cache.Acquire(t.Context(), root, "example/cache")
	if err != nil {
		t.Fatal(err)
	}
	memberPath := filepath.Join(generationDirectory(root, "example/cache", publication.manifest.GenerationDigest), publication.manifest.Members[0].Name)
	if err := os.WriteFile(memberPath, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	warm, err := cache.Acquire(t.Context(), root, "example/cache")
	if err != nil {
		t.Fatalf("warm cache reread corrupt member: %v", err)
	}
	warm.Release()
	first.Release()
	if _, err := Open(t.Context(), root, "example/cache"); err == nil {
		t.Fatal("cold open accepted corrupt member")
	}
	pointer, err := readPointer(root, "example/cache")
	if err != nil {
		t.Fatal(err)
	}
	pointer.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
	if err := writeAtomicCanonical(pointerPath(root, "example/cache"), pointer); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Acquire(t.Context(), root, "example/cache"); err == nil {
		t.Fatal("warm cache accepted a changed manifest digest")
	}
}

func TestArchiveRoundTripPreservesExactCurrentPublication(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go":   []byte("package demo\nconst A = 1\n"),
		"bad.go": []byte("not valid Go"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	plan := buildObservationPlan(t, repository, commit, "example/archive", "a")
	publication, err := Publish(t.Context(), root, repository, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if publication.manifest.UnsupportedCount != 1 {
		t.Fatalf("unsupported count = %d", publication.manifest.UnsupportedCount)
	}
	if receipt := publication.manifest.OperationReceipt; receipt == nil ||
		receipt.UnsupportedBlobs != 1 || len(receipt.UnsupportedReasons) != 1 ||
		receipt.UnsupportedReasons[0] != (ReasonCount{Reason: "parse_error", Count: 1}) {
		t.Fatalf("unsupported receipt = %+v", receipt)
	}
	archive := filepath.Join(t.TempDir(), "observations.tar")
	report, err := CreateArchive(t.Context(), root, archive)
	if err != nil {
		t.Fatal(err)
	}
	if report.Publications != 1 || report.Files < 3 || report.Bytes == 0 {
		t.Fatalf("archive report = %+v", report)
	}
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	if err := RestoreArchive(t.Context(), archive, restoredRoot); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(t.Context(), restoredRoot, "example/archive")
	if err != nil {
		t.Fatal(err)
	}
	if restored.manifest.Digest != publication.manifest.Digest || restored.manifest.GenerationDigest != publication.manifest.GenerationDigest {
		t.Fatalf("restored publication = %+v, want %+v", restored.manifest, publication.manifest)
	}
}

type testPins map[string]bool

func (pins testPins) Pinned(repository, generation string) bool {
	return pins[repository+"\x00"+generation]
}

func TestLifecyclePreservesCurrentRollbackFloorAndLease(t *testing.T) {
	repository, commitA := observationFixture(t, map[string][]byte{"a.go": []byte("package demo\nconst A = 1\n")})
	root := filepath.Join(t.TempDir(), "observations")
	planA := buildObservationPlan(t, repository, commitA, "example/lifecycle", "a")
	publicationA, err := Publish(t.Context(), root, repository, planA, nil)
	if err != nil {
		t.Fatal(err)
	}
	var publications []*Publication
	publications = append(publications, publicationA)
	for index := 2; index <= 3; index++ {
		content := []byte("package demo\nconst A = " + string(rune('0'+index)) + "\n")
		if err := os.WriteFile(filepath.Join(repository, "a.go"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		runObservationGit(t, repository, "add", "a.go")
		runObservationGit(t, repository, "commit", "-m", string(rune('A'+index)))
		commit := strings.TrimSpace(runObservationGit(t, repository, "rev-parse", "HEAD"))
		plan := buildObservationPlan(t, repository, commit, "example/lifecycle", string(rune('a'+index)))
		publication, err := Publish(t.Context(), root, repository, plan, nil)
		if err != nil {
			t.Fatal(err)
		}
		publications = append(publications, publication)
	}
	now := time.Now()
	for index, publication := range publications {
		stamp := now.Add(time.Duration(index-3) * time.Hour)
		if err := os.Chtimes(generationDirectory(root, "example/lifecycle", publication.manifest.GenerationDigest), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	pins := testPins{"example/lifecycle\x00" + publicationA.manifest.GenerationDigest: true}
	result, err := SweepLifecycle(t.Context(), root, time.Now().Add(30*24*time.Hour), "", pins, 1)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("pinned lifecycle result = %+v, %v", result, err)
	}
	delete(pins, "example/lifecycle\x00"+publicationA.manifest.GenerationDigest)
	collectingPath := filepath.Join(repositoryDirectory(root, "example/lifecycle"), "collecting-"+strings.TrimPrefix(publicationA.manifest.GenerationDigest, "sha256:"))
	for turn := 0; turn < 16; turn++ {
		result, err = SweepLifecycle(t.Context(), root, time.Now().Add(30*24*time.Hour), "", pins, 1)
		if err != nil || result.Deleted > 1 {
			t.Fatalf("released lifecycle result = %+v, %v", result, err)
		}
		_, generationErr := os.Lstat(generationDirectory(root, "example/lifecycle", publicationA.manifest.GenerationDigest))
		_, collectingErr := os.Lstat(collectingPath)
		if os.IsNotExist(generationErr) && os.IsNotExist(collectingErr) {
			break
		}
	}
	if _, err := os.Lstat(generationDirectory(root, "example/lifecycle", publicationA.manifest.GenerationDigest)); !os.IsNotExist(err) {
		t.Fatalf("released oldest generation still exists: %v", err)
	}
	if _, err := os.Lstat(collectingPath); !os.IsNotExist(err) {
		t.Fatalf("released collection stage still exists: %v", err)
	}
	current, err := Open(t.Context(), root, "example/lifecycle")
	if err != nil || current.manifest.GenerationDigest != publications[2].manifest.GenerationDigest {
		t.Fatalf("current publication after lifecycle = %+v, %v", current, err)
	}
}

func TestLifecycleRepairsCombinedGenerationLimitByDrainingCollectingFirst(t *testing.T) {
	const repository = "example/lifecycle-limit-repair"
	root := filepath.Join(t.TempDir(), "observations")
	repositoryRoot := repositoryDirectory(root, repository)
	if err := os.MkdirAll(repositoryRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	markerGeneration := "sha256:" + strings.Repeat("f", 64)
	if err := writeExclusiveCanonical(markerPath(root, repository), Marker{
		Schema: MarkerSchema, Repository: repository, GenerationDigest: markerGeneration,
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxRepositoryGenerations; index++ {
		if err := os.Mkdir(filepath.Join(repositoryRoot, fmt.Sprintf("%064x", index)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	collectingName := collectingStageName("sha256:"+strings.Repeat("e", 64), -1)
	if err := os.Mkdir(filepath.Join(repositoryRoot, collectingName), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := SweepLifecycle(t.Context(), root, time.Now().UTC(), "", testPins{}, 1)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("over-limit collecting repair = %+v, %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(repositoryRoot, collectingName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("collecting generation after repair = %v", err)
	}

	ordinaryOnlyRoot := filepath.Join(t.TempDir(), "observations")
	ordinaryOnlyRepositoryRoot := repositoryDirectory(ordinaryOnlyRoot, repository)
	if err := os.MkdirAll(ordinaryOnlyRepositoryRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveCanonical(markerPath(ordinaryOnlyRoot, repository), Marker{
		Schema: MarkerSchema, Repository: repository, GenerationDigest: markerGeneration,
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= MaxRepositoryGenerations; index++ {
		if err := os.Mkdir(filepath.Join(ordinaryOnlyRepositoryRoot, fmt.Sprintf("%064x", index)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := SweepLifecycle(
		t.Context(), ordinaryOnlyRoot, time.Now().UTC(), "", testPins{}, 1,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("ordinary-only over-limit lifecycle = %v", err)
	}
}

type scheduleCapture struct {
	store.GenerationSchedulerStore
	specs []store.GenerationScheduleSpec
}

type recoveryScheduleCapture struct {
	store.GenerationSchedulerStore
	current *store.GenerationSchedule
	specs   []store.GenerationScheduleSpec
}

func (capture *recoveryScheduleCapture) EnqueueGenerationSchedule(
	_ context.Context, spec store.GenerationScheduleSpec,
) (*store.GenerationSchedule, error) {
	capture.specs = append(capture.specs, spec)
	scheduleDigest, err := store.GenerationScheduleDigest(spec)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	capture.current = &store.GenerationSchedule{
		Schema:     store.GenerationScheduleSchema,
		Digest:     scheduleDigest,
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ResourceClass: spec.ResourceClass, TotalItems: spec.TotalItems,
		ChunkItems:  spec.ChunkItems,
		TotalChunks: int((spec.TotalItems + int64(spec.ChunkItems) - 1) / int64(spec.ChunkItems)),
		MaxAttempts: spec.MaxAttempts, RepositoryTokens: spec.RepositoryTokens,
		Status: store.GenerationScheduleActive, CreatedAt: now, UpdatedAt: now,
	}
	value := *capture.current
	return &value, nil
}

func (capture *recoveryScheduleCapture) GetGenerationSchedule(
	_ context.Context, _, stage string,
) (*store.GenerationSchedule, error) {
	if capture.current == nil || capture.current.Stage != stage {
		return nil, store.ErrNotFound
	}
	value := *capture.current
	return &value, nil
}

func (capture *scheduleCapture) EnqueueGenerationSchedule(
	_ context.Context, spec store.GenerationScheduleSpec,
) (*store.GenerationSchedule, error) {
	capture.specs = append(capture.specs, spec)
	return &store.GenerationSchedule{
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		Status: store.GenerationScheduleActive,
	}, nil
}

func (capture *scheduleCapture) GetGenerationSchedule(
	_ context.Context, repository, stage string,
) (*store.GenerationSchedule, error) {
	if len(capture.specs) == 0 {
		return nil, store.ErrNotFound
	}
	spec := capture.specs[len(capture.specs)-1]
	if spec.Stage != stage {
		return nil, store.ErrNotFound
	}
	return &store.GenerationSchedule{
		Repository: repository, Stage: stage, Generation: spec.Generation,
		Status: store.GenerationScheduleActive,
	}, nil
}

func TestRuntimeSchedulesPublishesAndRestartNoOps(t *testing.T) {
	dataDirectory := t.TempDir()
	repositoryName := "github.com/example/observation-runtime"
	repositoryDirectory := filepath.Join(dataDirectory, "repos", filepath.FromSlash(repositoryName)+".git")
	if err := os.MkdirAll(repositoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "init")
	runObservationGit(t, repositoryDirectory, "config", "user.email", "test@example.com")
	runObservationGit(t, repositoryDirectory, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repositoryDirectory, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "fixture")
	commit := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	indexDirectory := filepath.Join(dataDirectory, "index")
	if _, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, indexDirectory, repositoryName,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	); err != nil {
		t.Fatal(err)
	}
	capture := &scheduleCapture{}
	runtime := &Runtime{DataDir: dataDirectory, Store: capture}
	if err := runtime.Reconcile(t.Context(), repositoryName); err != nil {
		t.Fatal(err)
	}
	if len(capture.specs) != 1 || capture.specs[0].Stage != ScheduleStage || capture.specs[0].TotalItems < 1 {
		t.Fatalf("scheduled specs = %+v", capture.specs)
	}
	spec := capture.specs[0]
	chunk := store.GenerationChunk{
		Repository: repositoryName, Stage: spec.Stage, Generation: spec.Generation,
		Offset: 0, Length: int(spec.TotalItems),
	}
	capture.specs = append(capture.specs, store.GenerationScheduleSpec{
		Repository: repositoryName, Stage: spec.Stage,
		Generation: "sha256:" + strings.Repeat("0", 64),
	})
	if err := runtime.Handle(t.Context(), chunk); err == nil ||
		!errors.Is(err, ErrWorkUnavailable) ||
		!errors.Is(err, store.ErrGenerationStale) {
		t.Fatalf("stale runtime error = %v", err)
	} else if receipt, ok := pipelinerefusal.From(err); !ok ||
		receipt.Stage != pipelinerefusal.StageObservationPublication ||
		receipt.GenerationKind != pipelinerefusal.GenerationObservation ||
		receipt.Classification != pipelinerefusal.ClassificationUnknown {
		t.Fatalf("stale runtime refusal = %+v, present=%t", receipt, ok)
	}
	capture.specs = capture.specs[:1]
	if err := runtime.Handle(t.Context(), chunk); err != nil {
		t.Fatal(err)
	}
	publication, err := Open(t.Context(), filepath.Join(dataDirectory, "observations"), repositoryName)
	if err != nil || publication.manifest.GenerationDigest != spec.Generation {
		t.Fatalf("runtime publication = %+v, %v", publication, err)
	}
	restarted := &Runtime{DataDir: dataDirectory, Store: capture}
	if err := restarted.Reconcile(t.Context(), repositoryName); err != nil {
		t.Fatal(err)
	}
	if len(capture.specs) != 1 {
		t.Fatalf("restart enqueued another schedule: %+v", capture.specs)
	}
	if err := os.WriteFile(filepath.Join(repositoryDirectory, "main.go"), []byte("package main\nfunc main() { println(1) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "fixture-b")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	if err := os.RemoveAll(indexDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, indexDirectory, repositoryName,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commitB}},
	); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(t.Context(), repositoryName); err != nil {
		t.Fatal(err)
	}
	if len(capture.specs) != 2 {
		t.Fatalf("B schedule specs = %+v", capture.specs)
	}
	specB := capture.specs[1]
	if err := restarted.Handle(t.Context(), store.GenerationChunk{
		Repository: repositoryName, Stage: specB.Stage, Generation: specB.Generation,
		Offset: 0, Length: int(specB.TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(indexDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, indexDirectory, repositoryName,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(t.Context(), repositoryName); err != nil {
		t.Fatal(err)
	}
	if len(capture.specs) != 2 {
		t.Fatalf("A reactivation resurrected its stale schedule: %+v", capture.specs)
	}
	current, err := CurrentGeneration(filepath.Join(dataDirectory, "observations"), repositoryName)
	if err != nil || current != spec.Generation {
		t.Fatalf("A reactivation current = %q, %v", current, err)
	}
}

func TestRuntimeMintsDistinctRecoveryScheduleAfterExhaustion(t *testing.T) {
	dataDirectory := t.TempDir()
	repositoryName := "github.com/example/observation-recovery-schedule"
	repositoryDirectory := filepath.Join(dataDirectory, "repos", filepath.FromSlash(repositoryName)+".git")
	if err := os.MkdirAll(repositoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "init")
	runObservationGit(t, repositoryDirectory, "config", "user.email", "test@example.com")
	runObservationGit(t, repositoryDirectory, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repositoryDirectory, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "fixture")
	commit := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	if _, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, filepath.Join(dataDirectory, "index"), repositoryName,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	); err != nil {
		t.Fatal(err)
	}
	capture := &recoveryScheduleCapture{}
	runtime := &Runtime{DataDir: dataDirectory, Store: capture}
	if err := runtime.Reconcile(t.Context(), repositoryName); err != nil {
		t.Fatal(err)
	}
	if len(capture.specs) != 1 {
		t.Fatalf("initial schedules = %+v", capture.specs)
	}
	target := capture.specs[0].Generation
	capture.current.Status = store.GenerationScheduleSettled
	capture.current.Failed = 1
	capture.current.Materialized = 1
	capture.current.TotalChunks = 1
	if err := runtime.Reconcile(t.Context(), repositoryName); err != nil {
		t.Fatal(err)
	}
	initialScheduleDigest, err := store.GenerationScheduleDigest(capture.specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.specs) != 2 || capture.specs[1].Generation == target ||
		capture.specs[1].Generation != recoveryScheduleGeneration(target, initialScheduleDigest) {
		t.Fatalf("recovery schedules = %+v", capture.specs)
	}
	recovery := capture.specs[1]
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repositoryName, Stage: recovery.Stage, Generation: recovery.Generation,
		Offset: 0, Length: int(recovery.TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	publication, err := Open(t.Context(), filepath.Join(dataDirectory, "observations"), repositoryName)
	if err != nil || publication.manifest.GenerationDigest != target || publication.manifest.OperationReceipt == nil {
		t.Fatalf("recovered publication = %+v, %v", publication, err)
	}
	if _, err := os.Lstat(runtime.scheduleBindingPath(repositoryName, recovery.Generation)); err != nil {
		t.Fatalf("active recovery binding was removed before schedule settlement: %v", err)
	}
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repositoryName, Stage: recovery.Stage, Generation: recovery.Generation,
		Offset: 0, Length: int(recovery.TotalItems),
	}); err != nil {
		t.Fatalf("late sibling did not converge on complete recovery: %v", err)
	}
	capture.current.Status = store.GenerationScheduleSettled
	capture.current.NextOffset = capture.current.TotalItems
	capture.current.Materialized = capture.current.TotalChunks
	capture.current.Succeeded = capture.current.TotalChunks
	capture.current.UpdatedAt = capture.current.UpdatedAt.Add(time.Second)
	if err := runtime.Reconcile(t.Context(), repositoryName); err != nil {
		t.Fatal(err)
	}
	if len(capture.specs) != 2 {
		t.Fatalf("current publication rescheduled: %+v", capture.specs)
	}
	if _, err := os.Lstat(runtime.scheduleBindingPath(repositoryName, recovery.Generation)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settled recovery binding remains: %v", err)
	}
}

func buildObservationPlan(t *testing.T, repository, commit, name, suffix string) *sourcepartition.Plan {
	t.Helper()
	sourceDirectory := filepath.Join(t.TempDir(), "source-"+suffix)
	source, err := repositoryindex.BuildSourceGeneration(t.Context(), repository, sourceDirectory, name,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}})
	if err != nil {
		t.Fatal(err)
	}
	planDirectory := filepath.Join(t.TempDir(), "plan-"+suffix)
	if err := os.Mkdir(planDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := sourcepartition.Build(t.Context(), sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: planDirectory,
		Repository: name, Source: source,
		Policy: sourcepartition.Policy{Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourcepartition.Open(t.Context(), planDirectory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func publicationRecords(t *testing.T, publication *Publication) []Record {
	t.Helper()
	var records []Record
	directory := generationDirectory(publication.root, publication.manifest.Repository, publication.manifest.GenerationDigest)
	for ordinal := range publication.manifest.Members {
		_, memberRecords, err := readMember(t.Context(), directory, ordinal, len(publication.manifest.Members))
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, memberRecords...)
	}
	return records
}

func recordForPath(t *testing.T, records []Record, path string) Record {
	t.Helper()
	for _, record := range records {
		for _, placement := range record.Placements {
			if placement.Path == path {
				return record
			}
		}
	}
	t.Fatalf("record for %s not found", path)
	return Record{}
}

func observationFixture(t *testing.T, files map[string][]byte) (string, string) {
	t.Helper()
	directory := t.TempDir()
	runObservationGit(t, directory, "init")
	runObservationGit(t, directory, "config", "user.email", "fixture@example.invalid")
	runObservationGit(t, directory, "config", "user.name", "Fixture")
	for name, content := range files {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runObservationGit(t, directory, "add", ".")
	runObservationGit(t, directory, "commit", "-m", "fixture")
	return directory, strings.TrimSpace(runObservationGit(t, directory, "rev-parse", "HEAD"))
}

func runObservationGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{"-C", directory}, arguments...)...)
	command.Stdin = bytes.NewReader(nil)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}
