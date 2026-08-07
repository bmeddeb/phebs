package focusedindex

import (
	"errors"
	"os"
	"path/filepath"
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
