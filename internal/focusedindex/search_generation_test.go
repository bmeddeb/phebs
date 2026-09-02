package focusedindex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestSearchGenerationPublicationRollbackRecoveryAndAccounting(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	const repository = "example.com/acme/search-lifecycle"
	indexDir := t.TempDir()

	publish := func(content string, finish bool) ([]store.IndexedRevision, SearchGenerationRoot) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repositoryDir, "main.go"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, repositoryDir, "add", "main.go")
		git(t, repositoryDir, "commit", "-m", content)
		revisions := []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD",
			Commit: git(t, repositoryDir, "rev-parse", "HEAD"),
		}}
		wholeStage := buildWholeStageFixture(t, repository, revisions, 2)
		sourceStage := filepath.Join(t.TempDir(), "source")
		source, err := repositoryindex.BuildSourceGeneration(
			t.Context(), repositoryDir, sourceStage, repository, revisions,
		)
		if err != nil {
			t.Fatal(err)
		}
		reservation, err := SearchGenerationReservation(source)
		if err != nil || reservation != source.RegularDeclaredBytes*3+source.EncodedMemberBytes {
			t.Fatalf("reservation = %d, %v", reservation, err)
		}
		if err := PublishWholeGeneration(
			t.Context(), indexDir, wholeStage, sourceStage, repository, revisions, source,
		); err != nil {
			t.Fatal(err)
		}
		if finish {
			if err := FinishPublication(indexDir, repository); err != nil {
				t.Fatal(err)
			}
		}
		root, err := ReadSearchGenerationRoot(indexDir, repository)
		if err != nil {
			t.Fatal(err)
		}
		return revisions, root
	}

	revisionsA, rootA := publish("package main\nconst GenerationA = true\n", true)
	if rootA.Prior != nil || !sameSearchRevisions(rootA.Current.Revisions, revisionsA) {
		t.Fatalf("first root = %+v", rootA)
	}
	directoryA, err := searchGenerationDirectory(
		indexDir, repository, rootA.Current.GenerationDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptA, err := readSearchGenerationReceipt(directoryA, repository)
	if err != nil {
		t.Fatal(err)
	}
	if receiptA.BlobReaderMode != SearchBlobReaderGoGit || receiptA.BatchReadCount != 0 ||
		receiptA.FallbackReadCount != receiptA.FilesOffered || receiptA.FilesOffered != 1 ||
		receiptA.LogicalBytes <= 0 || receiptA.ShardCount != 2 {
		t.Fatalf("first receipt = %+v", receiptA)
	}
	if receiptA.AllocatedState == "exact" {
		drifted := receiptA
		drifted.AllocatedBytes += 4096
		if err := replaceSearchControlFile(
			filepath.Join(directoryA, searchGenerationReceiptName), drifted,
		); err != nil {
			t.Fatal(err)
		}
		measured, err := validateImmutableSearchGeneration(
			t.Context(), indexDir, repository, rootA.Current.GenerationDigest,
		)
		if err != nil || measured.AllocatedBytes != receiptA.AllocatedBytes {
			t.Fatalf(
				"allocation-drift validation = %d, %v; want current %d",
				measured.AllocatedBytes, err, receiptA.AllocatedBytes,
			)
		}
	}

	revisionsB, rootB := publish("package main\nconst GenerationB = true\n", true)
	if rootB.Prior == nil || rootB.Prior.GenerationDigest != rootA.Current.GenerationDigest ||
		!sameSearchRevisions(rootB.Current.Revisions, revisionsB) {
		t.Fatalf("replacement root = %+v", rootB)
	}
	if _, err := ValidateRepositorySearchGeneration(
		t.Context(), indexDir, repository, revisionsB,
	); err != nil {
		t.Fatalf("validate replacement: %v", err)
	}

	_, pendingC := publish("package main\nconst GenerationC = true\n", false)
	if pendingC.Current.GenerationDigest == rootB.Current.GenerationDigest ||
		!IsPublishing(indexDir, repository) {
		t.Fatalf("pending root/marker = %+v/%t", pendingC, IsPublishing(indexDir, repository))
	}
	if err := RollbackSearchPublication(t.Context(), indexDir, repository); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := ReadSearchGenerationRoot(indexDir, repository)
	if err != nil || rolledBack.Current.GenerationDigest != rootB.Current.GenerationDigest ||
		IsPublishing(indexDir, repository) {
		t.Fatalf("rolled back root/marker = %+v/%t, %v", rolledBack, IsPublishing(indexDir, repository), err)
	}
	if _, err := ValidateRepositorySearchGeneration(
		t.Context(), indexDir, repository, revisionsB,
	); err != nil {
		t.Fatalf("validate rollback: %v", err)
	}

	revisionsD, pendingD := publish("package main\nconst GenerationD = true\n", false)
	recovered, err := RecoverSearchPublication(
		t.Context(), indexDir, repository, revisionsD,
	)
	if err != nil || !recovered {
		t.Fatalf("recover candidate = %t, %v", recovered, err)
	}
	recoveryRoot, err := ReadSearchGenerationRoot(indexDir, repository)
	if err != nil || recoveryRoot.Current.GenerationDigest != pendingD.Current.GenerationDigest ||
		recoveryRoot.Prior == nil || recoveryRoot.Prior.GenerationDigest != rootB.Current.GenerationDigest {
		t.Fatalf("recovery root = %+v, %v", recoveryRoot, err)
	}
	reactivated, err := ReactivatePriorSearchGeneration(
		t.Context(), indexDir, repository, revisionsB,
	)
	if err != nil || !reactivated || !IsPublishing(indexDir, repository) {
		t.Fatalf("reactivate retained B = %t/%t, %v", reactivated, IsPublishing(indexDir, repository), err)
	}
	reactivatedRoot, err := ReadSearchGenerationRoot(indexDir, repository)
	if err != nil || reactivatedRoot.Current.GenerationDigest != rootB.Current.GenerationDigest ||
		reactivatedRoot.Prior == nil ||
		reactivatedRoot.Prior.GenerationDigest != pendingD.Current.GenerationDigest {
		t.Fatalf("reactivated root = %+v, %v", reactivatedRoot, err)
	}
	if err := FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRepositorySearchGeneration(
		t.Context(), indexDir, repository, revisionsB,
	); err != nil {
		t.Fatalf("validate reactivated B: %v", err)
	}
}

func TestSearchGenerationPinsExcludeRetirement(t *testing.T) {
	pins := &SearchGenerationPins{}
	repository := "example.com/acme/runtime"
	generation := "sha256:" + strings.Repeat("a", 64)
	lease, err := pins.Acquire(repository, generation)
	if err != nil {
		t.Fatal(err)
	}
	if release, admitted := pins.BeginRetire(repository, generation); admitted {
		release()
		t.Fatal("retirement admitted while generation was pinned")
	}
	lease.Release()
	release, admitted := pins.BeginRetire(repository, generation)
	if !admitted {
		t.Fatal("retirement refused after lease release")
	}
	if lease, err := pins.Acquire(repository, generation); err == nil {
		lease.Release()
		t.Fatal("lease admitted during retirement")
	}
	release()
	if lease, err := pins.Acquire(repository, generation); err != nil {
		t.Fatalf("lease after retirement = %v", err)
	} else {
		lease.Release()
	}
}

func TestSearchGenerationRecoveryRestoresLifecycleRootFromFlatPublication(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	if err := os.WriteFile(
		filepath.Join(repositoryDir, "main.go"),
		[]byte("package main\nconst Restored = true\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	git(t, repositoryDir, "add", "main.go")
	git(t, repositoryDir, "commit", "-m", "restore fixture")
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: git(t, repositoryDir, "rev-parse", "HEAD"),
	}}
	const repository = "example.com/acme/restored-search"
	indexDir := t.TempDir()
	wholeStage := buildWholeStageFixture(t, repository, revisions, 2)
	sourceStage := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDir, sourceStage, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishWholeGeneration(
		t.Context(), indexDir, wholeStage, sourceStage, repository, revisions, source,
	); err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	before, err := ReadSearchGenerationRoot(indexDir, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(indexDir, SearchGenerationRootName(repository))); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(SearchGenerationRootDirectory(indexDir)); err != nil {
		t.Fatal(err)
	}
	search, err := ValidateRepositorySearchGeneration(
		t.Context(), indexDir, repository, revisions,
	)
	if err != nil || search.Digest != before.Current.GenerationDigest {
		t.Fatal(err)
	}
	if _, err := ReadSearchGenerationRoot(indexDir, repository); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-recovery lifecycle root error = %v, want not-exist", err)
	}

	recovered, err := RecoverSearchPublication(
		t.Context(), indexDir, repository, revisions,
	)
	if err != nil || !recovered {
		t.Fatalf("recover flat publication = %t, %v", recovered, err)
	}
	root, err := ReadSearchGenerationRoot(indexDir, repository)
	if err != nil || root.Current.GenerationDigest != search.Digest || root.Prior != nil ||
		!sameSearchRevisions(root.Current.Revisions, revisions) {
		t.Fatalf("restored lifecycle root = %+v, %v", root, err)
	}
	recovered, err = RecoverSearchPublication(
		t.Context(), indexDir, repository, revisions,
	)
	if err != nil || recovered {
		t.Fatalf("repeat lifecycle recovery = %t, %v", recovered, err)
	}
}

func TestLegacyCommittedPublicationSurvivesUnmatchedSearchTransition(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	const repository = "example.com/acme/legacy-search-recovery"
	indexDir := t.TempDir()
	commit := func(content string) []store.IndexedRevision {
		t.Helper()
		if err := os.WriteFile(
			filepath.Join(repositoryDir, "main.go"), []byte(content), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		git(t, repositoryDir, "add", "main.go")
		git(t, repositoryDir, "commit", "-m", content)
		return []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD",
			Commit: git(t, repositoryDir, "rev-parse", "HEAD"),
		}}
	}

	revisionsA := commit("package main\nconst LegacyA = true\n")
	legacyStage := buildWholeStageFixture(t, repository, revisionsA, 2)
	if err := PublishWhole(
		t.Context(), indexDir, legacyStage, repository, revisionsA,
	); err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}

	revisionsB := commit("package main\nconst CandidateB = true\n")
	wholeStage := buildWholeStageFixture(t, repository, revisionsB, 2)
	whole, err := createWholeStageManifest(
		t.Context(), wholeStage, repository, revisionsB,
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceStage := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDir, sourceStage, repository, revisionsB,
	)
	if err != nil {
		t.Fatal(err)
	}
	search, err := repositoryindex.WriteSearchManifest(
		sourceStage, repository, revisionsB, source, wholePhysicalRoot(whole),
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := createImmutableSearchGeneration(
		t.Context(), indexDir, wholeStage, sourceStage, repository,
		revisionsB, source, whole, search, SearchBlobReaderGoGit,
	)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := prepareSearchGenerationTransition(
		t.Context(), indexDir, repository, candidate,
	)
	if err != nil || marker.Previous != nil {
		t.Fatalf("legacy transition marker = %+v, %v", marker, err)
	}
	if err := startPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}

	recovered, err := RecoverSearchPublication(
		t.Context(), indexDir, repository, revisionsA,
	)
	if recovered || !errors.Is(err, ErrSearchPublicationRevisionMismatch) {
		t.Fatalf("unmatched legacy recovery = %t, %v", recovered, err)
	}
	if _, err := ValidateCommittedWholePublication(
		t.Context(), indexDir, repository, revisionsA,
	); err != nil {
		t.Fatalf("legacy committed publication was not recoverable: %v", err)
	}
	if err := FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	if IsPublishing(indexDir, repository) {
		t.Fatal("legacy committed recovery left publication marker")
	}
}

func TestSearchGenerationLifecycleProtectsCurrentPriorAndReaderLease(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	const repository = "example.com/acme/search-collection"
	indexDir := t.TempDir()
	publish := func(label string) SearchGenerationRef {
		t.Helper()
		if err := os.WriteFile(
			filepath.Join(repositoryDir, "main.go"),
			[]byte("package main\nconst "+label+" = true\n"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		git(t, repositoryDir, "add", "main.go")
		git(t, repositoryDir, "commit", "-m", label)
		revisions := []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD",
			Commit: git(t, repositoryDir, "rev-parse", "HEAD"),
		}}
		wholeStage := buildWholeStageFixture(t, repository, revisions, 2)
		sourceStage := filepath.Join(t.TempDir(), "source")
		source, err := repositoryindex.BuildSourceGeneration(
			t.Context(), repositoryDir, sourceStage, repository, revisions,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := PublishWholeGeneration(
			t.Context(), indexDir, wholeStage, sourceStage, repository, revisions, source,
		); err != nil {
			t.Fatal(err)
		}
		if err := FinishPublication(indexDir, repository); err != nil {
			t.Fatal(err)
		}
		root, err := ReadSearchGenerationRoot(indexDir, repository)
		if err != nil {
			t.Fatal(err)
		}
		return root.Current
	}

	generationA := publish("GenerationA")
	generationB := publish("GenerationB")
	// Return to A's exact file bytes after B. Revision identity still mints a
	// distinct generation, and the one-over collector must not strand old A.
	generationC := publish("GenerationA")
	pins := &SearchGenerationPins{}
	lease, err := pins.Acquire(repository, generationA.GenerationDigest)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SweepSearchGenerationLifecycle(
		t.Context(), indexDir, time.Now().Add(30*24*time.Hour), "", pins, 2,
	)
	if err != nil || result.Scanned != 1 || result.Deleted != 0 ||
		result.LogicalBytes <= 0 || result.AllocatedState != "exact" {
		t.Fatalf("pinned sweep = %+v, %v", result, err)
	}
	directoryA, _ := searchGenerationDirectory(indexDir, repository, generationA.GenerationDigest)
	if _, err := os.Lstat(directoryA); err != nil {
		t.Fatalf("pinned generation removed: %v", err)
	}
	lease.Release()
	for turn := 0; turn < MaxSearchGenerationFiles+4; turn++ {
		result, err = SweepSearchGenerationLifecycle(
			t.Context(), indexDir, time.Now().Add(30*24*time.Hour), result.Cursor, pins, 2,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(directoryA); errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	if _, err := os.Lstat(directoryA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale generation remains: %v", err)
	}
	for _, protected := range []SearchGenerationRef{generationB, generationC} {
		directory, _ := searchGenerationDirectory(indexDir, repository, protected.GenerationDigest)
		if _, err := os.Lstat(directory); err != nil {
			t.Fatalf("protected generation %s removed: %v", protected.GenerationDigest, err)
		}
	}
}

func TestSearchGenerationLifecycleRefusesSymlinkEntry(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, searchGenerationReceiptName), []byte("receipt\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "phebs-whole-bad_v1.00000.zoekt")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := deleteSearchGenerationStep(directory, 16); err == nil {
		t.Fatal("search lifecycle accepted a symlinked generation member")
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "keep\n" {
		t.Fatalf("symlink target changed: %q, %v", raw, err)
	}
}

func TestSearchGenerationLifecycleRemovesKnownHostMetadata(t *testing.T) {
	indexDir := t.TempDir()
	base := SearchGenerationRootDirectory(indexDir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(base, ".DS_Store")
	if err := os.WriteFile(metadata, []byte("finder metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SweepSearchGenerationLifecycle(
		t.Context(), indexDir, time.Now(), "", &SearchGenerationPins{}, 16,
	)
	if err != nil || result.Scanned != 1 || result.Deleted != 1 {
		t.Fatalf("metadata sweep = %+v, %v", result, err)
	}
	if _, err := os.Lstat(metadata); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("known host metadata survived: %v", err)
	}
}

func TestSearchGenerationLifecycleDrainsOverBudgetCrashStages(t *testing.T) {
	indexDir := t.TempDir()
	repositoryDirectory := filepath.Join(
		SearchGenerationRootDirectory(indexDir), strings.Repeat("a", 64),
	)
	if err := os.MkdirAll(repositoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxSearchRepositoryGenerations+9; index++ {
		if err := os.Mkdir(
			filepath.Join(repositoryDirectory, fmt.Sprintf(".stage-prior-owner-%03d", index)),
			0o700,
		); err != nil {
			t.Fatal(err)
		}
	}
	result, err := SweepSearchGenerationLifecycle(
		t.Context(), indexDir, time.Now(), "", &SearchGenerationPins{}, 16,
	)
	if err != nil || result.Scanned != 1 || result.Deleted != 1 || !result.More {
		t.Fatalf("overflow-stage sweep = %+v, %v", result, err)
	}
	entries, err := os.ReadDir(repositoryDirectory)
	if err != nil || len(entries) != MaxSearchRepositoryGenerations+8 {
		t.Fatalf("remaining overflow stages = %d, %v", len(entries), err)
	}
}

func TestSearchGenerationAdmissionReservesCrashStageHeadroom(t *testing.T) {
	repositoryDirectory := t.TempDir()
	for index := 0; index < 8; index++ {
		if err := os.Mkdir(
			filepath.Join(repositoryDirectory, fmt.Sprintf(".stage-prior-owner-%03d", index)),
			0o700,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := admitSearchGenerationGrowth(repositoryDirectory); err == nil {
		t.Fatal("search generation admission accepted an exhausted stage budget")
	}
}
