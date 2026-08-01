package callerleaf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

func testIdentity(t *testing.T) (GenerationIdentity, PairIdentity) {
	t.Helper()
	digestA := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	generation, err := NewGenerationIdentity(GenerationIdentity{
		Repository:               "github.com/acme/service",
		HeadCommit:               "0123456789012345678901234567890123456789",
		DeclarationSetDigest:     digestA,
		CandidateManifestDigest:  digestB,
		CandidatePolicyDigest:    digestA,
		ResolverGenerationDigest: digestB,
		ResolverManifestDigest:   digestA,
		Extractors: []ExtractorIdentity{{
			Domain: "grpc-caller", Version: "1.5.0",
			LeafAdapterVersion: LeafAdapterV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := NewPairIdentity(generation, "grpc-caller", "1.5.0", LeafDescriptor{
		Name: "candidate-caller-000000.ndjson", Ordinal: 0,
		Prefix: "00", PrefixBits: 2, RecordCount: 3,
		DeclaredBytes: 128, ContentBytes: 100, ContentDigest: digestB,
	})
	if err != nil {
		t.Fatal(err)
	}
	return generation, pair
}

func testResultRecord() Record {
	digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	return Record{
		Schema: RecordSchema, Kind: RecordResult,
		Path:       "service/client.go",
		ObjectID:   "0123456789012345678901234567890123456789",
		SourceLane: candidate.SourceLaneBase,
		Fact: &sdk.Fact{
			Path: "service/client.go", StartLine: 1, EndLine: 1,
			Atom: sdk.AtomInput{
				SchemaVersion: "caller-v1", BlobDigest: digest,
				StartByte: 1, EndByte: 4, RuleID: "direct-v1",
				AdapterConfigDigest: digest, FactFingerprint: "f",
			},
			Assertion: sdk.AssertionInput{
				Predicate: "CALLS_OPERATION", Subject: "service/client.go:1-4",
				Object: "/demo.Service/Get", Lineage: "lineage", Tier: "derived",
			},
		},
	}
}

func TestArtifactLifecycleAndWarmFingerprint(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Discard() })
	if err := stage.Add(testResultRecord()); err != nil {
		t.Fatal(err)
	}
	if err := stage.Add(Record{
		Schema: RecordSchema, Kind: RecordAbstention,
		Path:       "service/empty.go",
		ObjectID:   "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
	}); err != nil {
		t.Fatal(err)
	}
	stage.ObserveExcludedGoTest()
	if err := stage.ObserveSourceBlob(42); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !publication.Current() || publication.Receipt().RecordCount != 2 ||
		publication.Receipt().ResultCount != 1 ||
		publication.Receipt().AbstentionCount != 1 ||
		publication.Receipt().ExcludedGoTestRecords != 1 {
		t.Fatalf("publication = current:%v receipt:%+v", publication.Current(), publication.Receipt())
	}
	seen := 0
	if _, err := Open(t.Context(), root, generation, pair, publication.Receipt(), func(Record) error {
		seen++
		return nil
	}); err != nil || seen != 2 {
		t.Fatalf("Open seen=%d err=%v", seen, err)
	}
	raw, err := os.ReadFile(publication.path)
	if err != nil {
		t.Fatal(err)
	}
	seen = 0
	if err := VerifyReader(
		t.Context(), bytes.NewReader(raw), generation, pair, publication.Receipt(),
		func(Record) error {
			seen++
			return nil
		},
	); err != nil || seen != 2 {
		t.Fatalf("VerifyReader seen=%d err=%v", seen, err)
	}
	if err := VerifyReader(
		t.Context(), bytes.NewReader(append(slices.Clone(raw), 'x')),
		generation, pair, publication.Receipt(), nil,
	); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("VerifyReader appended content = %v, want ErrInvalidArtifact", err)
	}
}

func TestPublicationScansAndRereadsExactRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Discard() })
	records := []Record{
		testResultRecord(),
		{
			Schema: RecordSchema, Kind: RecordAbstention,
			Path:       "service/empty.go",
			ObjectID:   "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
			SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
		},
	}
	for _, record := range records {
		if err := stage.Add(record); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var references []RecordReference
	if err := publication.ScanRecords(
		t.Context(), generation, pair,
		func(reference RecordReference, record Record) error {
			if !reflect.DeepEqual(record, records[len(references)]) {
				t.Fatalf("scanned record %d = %+v", len(references), record)
			}
			references = append(references, reference)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(references) != len(records) || references[0].Offset != 0 ||
		references[1].Offset != int64(references[0].Length) {
		t.Fatalf("record references = %+v", references)
	}
	for index, reference := range references {
		record, err := publication.ReadRecord(t.Context(), reference)
		if err != nil || !reflect.DeepEqual(record, records[index]) {
			t.Fatalf("ReadRecord(%d) = %+v, %v", index, record, err)
		}
	}
	forged := references[0]
	forged.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := publication.ReadRecord(t.Context(), forged); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("ReadRecord(forged) = %v, want ErrInvalidArtifact", err)
	}
	raw, err := os.ReadFile(publication.path)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte("service/client.go")
	position := bytes.Index(raw, marker)
	if position < 0 {
		t.Fatal("test record path is absent from the artifact")
	}
	raw[position] = 'S'
	if err := os.WriteFile(publication.path, raw, publication.info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(
		publication.path, publication.info.ModTime(), publication.info.ModTime(),
	); err != nil {
		t.Fatal(err)
	}
	if !publication.Current() {
		t.Fatal("same-identity mutation unexpectedly changed the warm fingerprint")
	}
	if _, err := publication.ReadRecord(
		t.Context(), references[0],
	); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("ReadRecord(same-identity mutation) = %v, want ErrInvalidArtifact", err)
	}
}

func TestPublicationRecordReadsClassifyRemovedLeafAsTransition(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Add(testResultRecord()); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var reference RecordReference
	if err := publication.ScanRecords(
		t.Context(), generation, pair,
		func(current RecordReference, _ Record) error {
			reference = current
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(publication.path); err != nil {
		t.Fatal(err)
	}
	if err := publication.ScanRecords(
		t.Context(), generation, pair,
		func(RecordReference, Record) error { return nil },
	); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("scan removed leaf = %v, want ErrInvalidArtifact", err)
	}
	if _, err := publication.ReadRecord(
		t.Context(), reference,
	); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("read removed leaf = %v, want ErrInvalidArtifact", err)
	}
}

func TestPublicationRecordReadsClassifyReplacedRepositoryAsTransition(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Add(testResultRecord()); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var reference RecordReference
	if err := publication.ScanRecords(
		t.Context(), generation, pair,
		func(current RecordReference, _ Record) error {
			reference = current
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(publication.root, publication.root+".retired"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publication.root, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publication.ScanRecords(
		t.Context(), generation, pair,
		func(RecordReference, Record) error { return nil },
	); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("scan replaced repository = %v, want ErrInvalidArtifact", err)
	}
	if _, err := publication.ReadRecord(
		t.Context(), reference,
	); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("read replaced repository = %v, want ErrInvalidArtifact", err)
	}
}

func TestArtifactOpenClassifiesSameSizeRecordFramingCorruption(t *testing.T) {
	t.Run("partial trailing record", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		generation, pair := testIdentity(t)
		stage, err := NewStage(root, generation, pair)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.Add(testResultRecord()); err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal()
		if err != nil {
			t.Fatal(err)
		}
		publication, err := prepared.Install(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		path, err := ArtifactPath(root, generation.Repository, publication.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil || len(content) == 0 || content[len(content)-1] != '\n' {
			t.Fatalf("canonical artifact = %d bytes, %v", len(content), err)
		}
		content[len(content)-1] = ' '
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(
			t.Context(), root, generation, pair, publication.Receipt(), nil,
		); !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("partial trailing record = %v, want ErrInvalidArtifact", err)
		}
	})

	t.Run("record exceeds byte limit", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		generation, pair := testIdentity(t)
		stage, err := NewStage(root, generation, pair)
		if err != nil {
			t.Fatal(err)
		}
		for index := range 300 {
			if err := stage.Add(Record{
				Schema: RecordSchema, Kind: RecordAbstention,
				Path: fmt.Sprintf(
					"bulk/%03d-%s.go", index, strings.Repeat("a", 3900),
				),
				ObjectID:   "1111111111111111111111111111111111111111",
				SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
			}); err != nil {
				t.Fatal(err)
			}
		}
		prepared, err := stage.Seal()
		if err != nil {
			t.Fatal(err)
		}
		publication, err := prepared.Install(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		path, err := ArtifactPath(root, generation.Repository, publication.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil || bytes.Count(content, []byte{'\n'}) < 2 {
			t.Fatalf("canonical multiline artifact = %d bytes, %v", len(content), err)
		}
		for index := 0; index < len(content)-1; index++ {
			if content[index] == '\n' {
				content[index] = ' '
			}
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(
			t.Context(), root, generation, pair, publication.Receipt(), nil,
		); !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("overlong record = %v, want ErrInvalidArtifact", err)
		}
	})
}

func TestExplicitEmptyArtifactAndDivergentRetryRefusal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if publication.Receipt().RecordCount != 0 || publication.Receipt().ContentBytes != 0 {
		t.Fatalf("empty receipt = %+v", publication.Receipt())
	}

	// The same immutable pair cannot select different canonical bytes after a
	// crash-before-state leaves the first artifact behind.
	stage, err = NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Add(testResultRecord()); err != nil {
		t.Fatal(err)
	}
	prepared, err = stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepared.Install(t.Context()); !errors.Is(err, ErrNondeterministic) {
		t.Fatalf("divergent retry = %v, want ErrNondeterministic", err)
	}
	_ = prepared.Discard()
}

func TestArtifactInventoryRefusesEntryCapBeforeInstall(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	inventory, err := OpenArtifactInventory(t.Context(), root, generation.Repository)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inventory.Close() })
	inventory.entryCount = MaxRepositoryDirectoryEntries
	if inventory.HasCapacity() {
		t.Fatal("full artifact inventory reported install capacity")
	}
	if _, err := prepared.InstallWithInventory(t.Context(), inventory); !errors.Is(err, ErrCapacity) {
		t.Fatalf("full inventory install = %v, want ErrCapacity", err)
	}
	if _, err := os.Lstat(prepared.final); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("full inventory installed bytes: %v", err)
	}
}

func TestArtifactInventoryReservesRenameCrashSlot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	inventory, err := OpenArtifactInventory(t.Context(), root, generation.Repository)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inventory.Close() })
	inventory.entryCount = MaxRepositoryDirectoryEntries - 1
	if inventory.HasCapacity() {
		t.Fatal("near-cap inventory exposed no reserved rename crash slot")
	}
	inventory.byPair[pair.Digest] = []string{prepared.Receipt().Name}
	if !inventory.CanPrepare(pair.Digest) {
		t.Fatal("near-cap inventory cannot examine an exact orphan")
	}
	if _, err := prepared.InstallWithInventory(t.Context(), inventory); !errors.Is(err, ErrCapacity) {
		t.Fatalf("near-cap new artifact = %v, want reserved-slot ErrCapacity", err)
	}
}

func TestSourceBlobBudgetIsIndependentAndPreReadBounded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Discard() })
	if err := stage.ObserveSourceBlob(MaxSourceBlobBytesPerPair); err != nil {
		t.Fatalf("exact source-byte cap: %v", err)
	}
	if err := stage.ObserveSourceBlob(1); !errors.Is(err, ErrLimit) {
		t.Fatalf("source-byte cap+1 = %v, want ErrLimit", err)
	}
}

func TestArtifactOpenRejectsDescriptorSwap(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Add(testResultRecord()); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Replace the artifact path from the synchronous visitor while Open still
	// owns the original descriptor.
	path, err := ArtifactPath(root, generation.Repository, publication.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	replaced := false
	_, err = Open(t.Context(), root, generation, pair, publication.Receipt(), func(Record) error {
		if replaced {
			return nil
		}
		replaced = true
		backup := path + ".old"
		if err := os.Rename(path, backup); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("{}\n"), 0o600)
	})
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("descriptor swap Open = %v, want ErrInvalidArtifact", err)
	}
}

func TestInstallRejectsRepositoryPathReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })

	repositoryDirectory := filepath.Dir(stage.directory)
	movedRepository := repositoryDirectory + ".moved"
	if err := os.Rename(repositoryDirectory, movedRepository); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	sentinel := filepath.Join(foreign, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, repositoryDirectory); err != nil {
		t.Fatal(err)
	}

	if _, err := prepared.Install(t.Context()); err == nil {
		t.Fatal("Install accepted a replaced repository directory")
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatalf("foreign sentinel changed: %q, %v", raw, err)
	}
	if _, err := os.Lstat(filepath.Join(foreign, prepared.receipt.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Install mutated the replacement directory: %v", err)
	}
}

func TestDiscardRejectsStagePathReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	movedStage := stage.directory + ".moved"
	if err := os.Rename(stage.directory, movedStage); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	sentinel := filepath.Join(foreign, stageArtifactName)
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, stage.directory); err != nil {
		t.Fatal(err)
	}

	if err := stage.Discard(); err == nil {
		t.Fatal("Discard accepted a replaced stage directory")
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatalf("foreign stage sentinel changed: %q, %v", raw, err)
	}
}

func TestPublicationCurrentRejectsRepositoryIdentityReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Add(testResultRecord()); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	repositoryDirectory := filepath.Dir(publication.path)
	movedRepository := repositoryDirectory + ".moved"
	if err := os.Rename(repositoryDirectory, movedRepository); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repositoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(movedRepository, publication.receipt.Name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, publication.receipt.Name), raw, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if publication.Current() {
		t.Fatal("publication remained current across repository identity replacement")
	}
}

func TestRepositoryCreationFenceHonorsInstallationLimit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	const limit = 2
	for _, repository := range []string{
		"github.com/acme/cap-a", "github.com/acme/cap-b",
	} {
		if err := ensureRepositoryWithinLimit(root, repository, limit); err != nil {
			t.Fatalf("ensure %q: %v", repository, err)
		}
	}
	if err := ensureRepositoryWithinLimit(
		root, "github.com/acme/cap-a", limit,
	); err != nil {
		t.Fatalf("existing repository at cap: %v", err)
	}
	if err := ensureRepositoryWithinLimit(
		root, "github.com/acme/cap-c", limit,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("repository beyond cap = %v, want ErrCapacity", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != limit {
		t.Fatalf("repository entries at cap = %d, %v", len(entries), err)
	}
}

func TestRepositoryCreationFenceSerializesConcurrentAdmission(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, repository := range []string{
		"github.com/acme/race-a", "github.com/acme/race-b",
	} {
		go func() {
			<-start
			results <- ensureRepositoryWithinLimit(root, repository, 1)
		}()
	}
	close(start)
	succeeded := 0
	refused := 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrCapacity):
			refused++
		default:
			t.Fatalf("concurrent repository admission: %v", err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || succeeded != 1 || refused != 1 {
		t.Fatalf(
			"concurrent fence = entries:%d success:%d refused:%d err:%v",
			len(entries), succeeded, refused, err,
		)
	}
}

func TestCleanupStagesRejectsUnexpectedStageShape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.file.Close(); err != nil {
		t.Fatal(err)
	}
	stage.file = nil
	if err := os.Mkdir(filepath.Join(stage.directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupStages(context.Background(), root); err == nil {
		t.Fatal("CleanupStages accepted an unexpected nested stage")
	}
}

func TestDeletionRejectsNoncanonicalCallerNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, _ := testIdentity(t)
	directory, err := ensureRepositoryRoot(root, generation.Repository)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(directory, "leaf-v1-not-owned.ndjson")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveArtifact(root, generation.Repository, Receipt{Name: filepath.Base(foreign)}); err == nil {
		t.Fatal("RemoveArtifact accepted a noncanonical package name")
	}
	if err := RemoveRepository(t.Context(), root, generation.Repository); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("RemoveRepository noncanonical name = %v", err)
	}
	if _, err := os.Lstat(foreign); err != nil {
		t.Fatalf("foreign caller-shaped entry was removed: %v", err)
	}
}

func TestRemovalPreflightsAllRepositoryEntries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	repositoryDirectory := filepath.Dir(publication.path)
	foreign := filepath.Join(repositoryDirectory, "zz-foreign")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveRepository(t.Context(), root, generation.Repository); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("RemoveRepository foreign entry = %v", err)
	}
	if _, err := os.Lstat(publication.path); err != nil {
		t.Fatalf("owned artifact was removed before repository preflight: %v", err)
	}
	if _, err := os.Lstat(foreign); err != nil {
		t.Fatalf("foreign entry was removed: %v", err)
	}
}

func TestForeignStagePrefixIsNotCleanupAuthority(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, _ := testIdentity(t)
	directory, err := ensureRepositoryRoot(root, generation.Repository)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(directory, callerleafid.StagePrefix+"foreign")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ArtifactNames(root, generation.Repository); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("ArtifactNames foreign stage prefix = %v", err)
	}
	removed, err := CleanupStages(t.Context(), root)
	if err != nil || removed != 0 {
		t.Fatalf("CleanupStages foreign stage prefix = (%d, %v)", removed, err)
	}
	if _, err := os.Lstat(foreign); err != nil {
		t.Fatalf("foreign stage-prefix directory was removed: %v", err)
	}
}
