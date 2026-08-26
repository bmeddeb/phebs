package observationpublication

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

func buildInventoryPublicationTransitionV2(
	t *testing.T, transition InventoryPublicationTransitionV2,
	repositoryDirectory, repository, commit string,
) InventoryRootV2 {
	t.Helper()
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, sourceDirectory, repository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := sourcepartition.BuildSuperRoot(t.Context(), sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: transition.SourceDirectory,
		Repository: repository, Source: source,
		Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourcepartition.OpenSuperRoot(t.Context(), transition.SourceDirectory, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := BuildInventoryStageV2(t.Context(), InventoryBuildRequestV2{
		OutputDirectory:     transition.InventoryDirectory,
		RepositoryDirectory: repositoryDirectory, Plan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	return inventory
}

func TestInventoryV2PublicationRecoveryKeepsPriorAndFencesStaleWorker(t *testing.T) {
	repositoryDirectory, commitA := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/inventory-publication-v2"
	transitionA, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	inventoryA := buildInventoryPublicationTransitionV2(
		t, transitionA, repositoryDirectory, repository, commitA,
	)
	rootA, err := CompleteInventoryPublicationV2(
		t.Context(), root, repository, transitionA.TransitionID, nil,
	)
	if err != nil || rootA.Current.InventoryDigest != inventoryA.Digest || rootA.Prior != nil {
		t.Fatalf("A publication = %+v, %v", rootA, err)
	}

	if err := os.WriteFile(filepath.Join(repositoryDirectory, "b.go"), []byte("package demo\nconst B = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "b.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	transitionB, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, recoverErr := RecoverInventoryPublicationV2(t.Context(), root, repository); recoverErr == nil || recovered {
		t.Fatalf("incomplete recovery = %t, %v", recovered, recoverErr)
	}
	unchanged, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil || unchanged.Current != rootA.Current {
		t.Fatalf("prior changed after incomplete recovery = %+v, %v", unchanged, err)
	}
	inventoryB := buildInventoryPublicationTransitionV2(
		t, transitionB, repositoryDirectory, repository, commitB,
	)
	if recovered, recoverErr := RecoverInventoryPublicationV2(t.Context(), root, repository); recoverErr != nil || !recovered {
		t.Fatalf("complete recovery = %t, %v", recovered, recoverErr)
	}
	rootB, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil || rootB.Current.InventoryDigest != inventoryB.Digest || rootB.Prior == nil ||
		rootB.Prior.GenerationDigest != rootA.Current.GenerationDigest {
		t.Fatalf("B recovery root = %+v, %v", rootB, err)
	}

	if err := os.WriteFile(filepath.Join(repositoryDirectory, "c.go"), []byte("package demo\nconst C = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "c.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "C")
	commitC := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	transitionC, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	buildInventoryPublicationTransitionV2(t, transitionC, repositoryDirectory, repository, commitC)
	_, err = CompleteInventoryPublicationV2(
		t.Context(), root, repository, transitionC.TransitionID,
		func(context.Context) error { return store.ErrGenerationStale },
	)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("stale completion = %v", err)
	}
	staleMarker, present, markerErr := readInventoryPublicationMarkerV2(root, repository)
	if markerErr != nil || !present || staleMarker.Candidate != nil {
		t.Fatalf("stale worker sealed candidate = %+v, %t, %v", staleMarker, present, markerErr)
	}
	stillB, readErr := ReadInventoryPublicationRootV2(root, repository)
	if readErr != nil || stillB.Current != rootB.Current {
		t.Fatalf("stale worker changed root = %+v, %v", stillB, readErr)
	}
}

func TestInventoryV2RecoveryCompletesCrashAfterCanonicalRename(t *testing.T) {
	repositoryDirectory, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/inventory-rename-recovery-v2"
	transition, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	buildInventoryPublicationTransitionV2(t, transition, repositoryDirectory, repository, commit)
	ref, err := validateInventoryPublicationStageV2(t.Context(), transition)
	if err != nil {
		t.Fatal(err)
	}
	marker, present, err := readInventoryPublicationMarkerV2(root, repository)
	if err != nil || !present {
		t.Fatalf("marker before crash = %+v, %t, %v", marker, present, err)
	}
	marker.Candidate = &ref
	if err := writeAtomicCanonical(inventoryPublicationMarkerPathV2(root, repository), marker); err != nil {
		t.Fatal(err)
	}
	canonical := inventoryGenerationDirectoryV2(root, repository, ref.GenerationDigest)
	if err := os.Rename(transition.Directory, canonical); err != nil {
		t.Fatal(err)
	}
	if recovered, err := RecoverInventoryPublicationV2(t.Context(), root, repository); err != nil || !recovered {
		t.Fatalf("canonical recovery = %t, %v", recovered, err)
	}
	publicationRoot, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil || publicationRoot.Current != ref {
		t.Fatalf("recovered root = %+v, %v", publicationRoot, err)
	}
}

func TestInventoryV2SameGenerationRetryPreservesRollbackFloor(t *testing.T) {
	repositoryDirectory, commitA := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/inventory-same-generation-retry-v2"
	transitionA, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	buildInventoryPublicationTransitionV2(t, transitionA, repositoryDirectory, repository, commitA)
	publicationA, err := CompleteInventoryPublicationV2(
		t.Context(), root, repository, transitionA.TransitionID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repositoryDirectory, "b.go"), []byte("package demo\nconst B = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "b.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	transitionB, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	buildInventoryPublicationTransitionV2(t, transitionB, repositoryDirectory, repository, commitB)
	publicationB, err := CompleteInventoryPublicationV2(
		t.Context(), root, repository, transitionB.TransitionID, nil,
	)
	if err != nil || publicationB.Prior == nil || *publicationB.Prior != publicationA.Current {
		t.Fatalf("B publication = %+v, %v", publicationB, err)
	}

	retry, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	buildInventoryPublicationTransitionV2(t, retry, repositoryDirectory, repository, commitB)
	retried, err := CompleteInventoryPublicationV2(
		t.Context(), root, repository, retry.TransitionID, nil,
	)
	if err != nil || retried.Schema != publicationB.Schema || retried.Repository != publicationB.Repository ||
		retried.Current != publicationB.Current || retried.Prior == nil || *retried.Prior != publicationA.Current {
		t.Fatalf("same-generation retry = %+v, want %+v, %v", retried, publicationB, err)
	}
	if _, err := os.Lstat(retry.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("same-generation retry retained live stage: %v", err)
	}
	retired := filepath.Join(
		filepath.Dir(retry.Directory), "collecting-stage-"+retry.TransitionID,
	)
	if info, err := os.Lstat(retired); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("same-generation retry retirement = %v, %v", info, err)
	}
}

func TestInventoryV2BeginReservesNamespaceAndCountsRetiredStages(t *testing.T) {
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/inventory-publication-admission-v2"
	directory := inventoryPublicationDirectoryV2(root, repository)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxInventoryPublicationEntriesV2-1; index++ {
		name := "entry-" + strings.Repeat("0", 60) + string(rune('A'+index%26)) +
			string(rune('A'+index/26))
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginInventoryPublicationV2(root, repository); !errors.Is(err, ErrLimit) {
		t.Fatalf("namespace reservation = %v", err)
	}
	after, err := os.ReadDir(directory)
	if err != nil || len(after) != len(before) {
		t.Fatalf("reservation changed full namespace: before=%d after=%d err=%v", len(before), len(after), err)
	}

	root = filepath.Join(t.TempDir(), "observations")
	directory = inventoryPublicationDirectoryV2(root, repository)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxInventoryPublicationStagesV2; index++ {
		name := "collecting-stage-" + strings.Repeat(string(rune('a'+index)), 32)
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := BeginInventoryPublicationV2(root, repository); !errors.Is(err, ErrLimit) {
		t.Fatalf("retired stage admission = %v", err)
	}
	if _, err := os.Lstat(inventoryPublicationMarkerPathV2(root, repository)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired stage admission created marker: %v", err)
	}
}

func TestInventoryV2IncompleteStageRestartsByRetirement(t *testing.T) {
	repositoryDirectory, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/inventory-restart-v2"
	first, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(first.SourceDirectory, ".source-partition-spool-crash")
	if err := os.Mkdir(spool, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spool, "root-00000000.jsonl"), []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := RestartInventoryPublicationV2(
		t.Context(), root, repository, first.TransitionID, nil,
	)
	if err != nil || second.TransitionID == first.TransitionID {
		t.Fatalf("restart transition = %+v, %v", second, err)
	}
	retired := filepath.Join(
		inventoryPublicationDirectoryV2(root, repository),
		"collecting-stage-"+first.TransitionID,
	)
	if _, err := os.Lstat(retired); err != nil {
		t.Fatalf("retired incomplete stage = %v", err)
	}
	buildInventoryPublicationTransitionV2(t, second, repositoryDirectory, repository, commit)
	if _, err := CompleteInventoryPublicationV2(t.Context(), root, repository, second.TransitionID, nil); err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 20; turn++ {
		if _, err := os.Lstat(retired); errors.Is(err, os.ErrNotExist) {
			break
		}
		if _, err := SweepInventoryLifecycleV2(
			t.Context(), root, time.Now(), "", InventoryPinsV2{}, 64, 1,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(retired); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired stage did not drain: %v", err)
	}
}

type inventoryLifecyclePinsV2 struct {
	proof         map[string]bool
	investigation map[string]bool
}

func (pins inventoryLifecyclePinsV2) PinnedInventoryProofV2(
	_ context.Context, repository, generation string,
) (bool, error) {
	return pins.proof[repository+"\x00"+generation], nil
}

func (pins inventoryLifecyclePinsV2) PinnedInventoryInvestigationV2(
	_ context.Context, repository, generation string,
) (bool, error) {
	return pins.investigation[repository+"\x00"+generation], nil
}

func TestInventoryV2LifecyclePreservesAllRootsAndResumesDrain(t *testing.T) {
	repositoryDirectory, commitA := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/inventory-lifecycle-v2"
	published := make([]InventoryPublicationRootV2, 0, 3)
	commits := []string{commitA}
	for index, name := range []string{"b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(repositoryDirectory, name), []byte("package demo\nconst X = 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runObservationGit(t, repositoryDirectory, "add", name)
		runObservationGit(t, repositoryDirectory, "commit", "-m", name)
		commits = append(commits, strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD")))
		_ = index
	}
	for _, commit := range commits {
		transition, err := BeginInventoryPublicationV2(root, repository)
		if err != nil {
			t.Fatal(err)
		}
		buildInventoryPublicationTransitionV2(t, transition, repositoryDirectory, repository, commit)
		value, err := CompleteInventoryPublicationV2(t.Context(), root, repository, transition.TransitionID, nil)
		if err != nil {
			t.Fatal(err)
		}
		published = append(published, value)
	}
	old := published[0].Current
	oldDirectory := inventoryGenerationDirectoryV2(root, repository, old.GenerationDigest)
	oldRoot, err := ReadInventoryRootV2(filepath.Join(oldDirectory, InventoryPublicationInventoryNameV2), repository)
	if err != nil {
		t.Fatal(err)
	}
	cache := &InventoryCacheV2{}
	lease, err := cache.Acquire(t.Context(), filepath.Join(oldDirectory, InventoryPublicationInventoryNameV2), oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	durablePins := inventoryLifecyclePinsV2{
		proof: map[string]bool{}, investigation: map[string]bool{},
	}
	pins := InventoryPinsV2{Cache: cache, Durable: durablePins}
	result, err := SweepInventoryLifecycleV2(
		t.Context(), root, time.Now().Add(30*24*time.Hour), "", pins, 64, 1,
	)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("leased sweep = %+v, %v", result, err)
	}
	lease.Release()
	durablePins.proof[repository+"\x00"+old.GenerationDigest] = true
	result, err = SweepInventoryLifecycleV2(
		t.Context(), root, time.Now().Add(30*24*time.Hour), "", pins, 64, 1,
	)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("durably pinned sweep = %+v, %v", result, err)
	}
	delete(durablePins.proof, repository+"\x00"+old.GenerationDigest)
	durablePins.investigation[repository+"\x00"+old.GenerationDigest] = true
	result, err = SweepInventoryLifecycleV2(
		t.Context(), root, time.Now().Add(30*24*time.Hour), "", pins, 64, 1,
	)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("investigation-pinned sweep = %+v, %v", result, err)
	}
	delete(durablePins.investigation, repository+"\x00"+old.GenerationDigest)
	for turn := 0; turn < 100; turn++ {
		result, err = SweepInventoryLifecycleV2(
			t.Context(), root, time.Now().Add(30*24*time.Hour), "", pins, 64, 1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, statErr := os.Lstat(oldDirectory); errors.Is(statErr, os.ErrNotExist) {
			break
		}
	}
	if _, err := os.Lstat(oldDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old generation remains after bounded drain: %v", err)
	}
	current, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil || current.Current != published[2].Current || current.Prior == nil ||
		current.Prior.GenerationDigest != published[1].Current.GenerationDigest {
		t.Fatalf("lifecycle changed current/rollback floor = %+v, %v", current, err)
	}
}

func TestObservationArchiveV2RoundTripsV1V2AndResumesRestore(t *testing.T) {
	repositoryDirectory, commitA := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/archive-v2"
	v1Plan := buildObservationPlan(t, repositoryDirectory, commitA, repository, "archive-v1")
	v1, err := Publish(t.Context(), root, repositoryDirectory, v1Plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	transitionA, err := BeginInventoryPublicationV2(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	buildInventoryPublicationTransitionV2(t, transitionA, repositoryDirectory, repository, commitA)
	v2, err := CompleteInventoryPublicationV2(t.Context(), root, repository, transitionA.TransitionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// An incomplete next transition is visible omission, not an archive pin or
	// a reason to discard the complete v1/v2 roots.
	if _, err := BeginInventoryPublicationV2(root, repository); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "observations-v2.tar")
	report, err := CreateArchive(t.Context(), root, archive)
	if err != nil {
		t.Fatal(err)
	}
	if report.V1Publications != 1 || report.V2Publications != 1 ||
		report.Publications != 2 || report.StaleMarkers != 1 || report.OmittedArtifacts < 2 {
		t.Fatalf("v2 archive report = %+v", report)
	}

	// Seed one exact completed entry in the deterministic restore stage, as a
	// process crash would leave it. Restore compares and reuses that byte-exact
	// prefix, then continues with the remaining archive entries.
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	restoreStage := filepath.Join(t.TempDir(), "external-observation-restore-stage")
	if err := os.Mkdir(restoreStage, 0o700); err != nil {
		t.Fatal(err)
	}
	archiveFile, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(archiveFile)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(restoreStage, filepath.FromSlash(header.Name))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(output, reader, header.Size); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreArchiveWithStage(t.Context(), archive, restoredRoot, restoreStage); err != nil {
		t.Fatal(err)
	}
	restoredV1, err := Open(t.Context(), restoredRoot, repository)
	if err != nil || restoredV1.manifest.Digest != v1.manifest.Digest {
		t.Fatalf("restored v1 = %+v, %v", restoredV1, err)
	}
	restoredV2, err := ReadInventoryPublicationRootV2(restoredRoot, repository)
	if err != nil || restoredV2.Current != v2.Current {
		t.Fatalf("restored v2 = %+v, %v", restoredV2, err)
	}
}

func TestObservationArchiveRestoreRefusesDuplicatePathsAndStageSymlinks(t *testing.T) {
	duplicateArchive := filepath.Join(t.TempDir(), "duplicate.tar")
	file, err := os.OpenFile(duplicateArchive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	for range 2 {
		if err := writer.WriteHeader(&tar.Header{
			Name: "repository/current.json", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreArchive(t.Context(), duplicateArchive, filepath.Join(t.TempDir(), "duplicate-root")); err == nil ||
		!strings.Contains(err.Error(), "duplicate observation archive path") {
		t.Fatalf("duplicate restore error = %v", err)
	}

	repositoryDirectory, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"),
	})
	root := filepath.Join(t.TempDir(), "observations")
	repository := "example/archive-stage-symlink"
	plan := buildObservationPlan(t, repositoryDirectory, commit, repository, "archive-symlink")
	if _, err := Publish(t.Context(), root, repositoryDirectory, plan, nil); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "observations.tar")
	if _, err := CreateArchive(t.Context(), root, archive); err != nil {
		t.Fatal(err)
	}
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	restoreStage := restoredRoot + ".restore"
	if err := os.Mkdir(restoreStage, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(restoreStage, repositoryHash(repository))); err != nil {
		t.Fatal(err)
	}
	if err := RestoreArchive(t.Context(), archive, restoredRoot); err == nil ||
		!strings.Contains(err.Error(), "restore parent is special") {
		t.Fatalf("symlinked stage restore error = %v", err)
	}
	outsideEntries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(outsideEntries) != 0 {
		t.Fatalf("restore wrote through stage symlink: %v", outsideEntries)
	}

	priorRoot := filepath.Join(t.TempDir(), "prior-restored")
	if err := RestoreArchive(t.Context(), archive, priorRoot); err != nil {
		t.Fatal(err)
	}
	otherRepository := "example/archive-stage-other"
	otherSource, otherCommit := observationFixture(t, map[string][]byte{
		"other.go": []byte("package other\nconst Other = 1\n"),
	})
	otherPublicationRoot := filepath.Join(t.TempDir(), "other-observations")
	otherPlan := buildObservationPlan(t, otherSource, otherCommit, otherRepository, "archive-other")
	if _, err := Publish(t.Context(), otherPublicationRoot, otherSource, otherPlan, nil); err != nil {
		t.Fatal(err)
	}
	otherArchive := filepath.Join(t.TempDir(), "other-observations.tar")
	if _, err := CreateArchive(t.Context(), otherPublicationRoot, otherArchive); err != nil {
		t.Fatal(err)
	}
	otherRestoredRoot := filepath.Join(t.TempDir(), "other-restored")
	otherStage := otherRestoredRoot + ".restore"
	if err := os.Rename(priorRoot, otherStage); err != nil {
		t.Fatal(err)
	}
	if err := RestoreArchive(t.Context(), otherArchive, otherRestoredRoot); err == nil ||
		!strings.Contains(err.Error(), "archive-absent directory") {
		t.Fatalf("different-archive resumed stage error = %v", err)
	}
	if _, err := os.Lstat(otherRestoredRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("different archive installed mixed restore: %v", err)
	}
}
