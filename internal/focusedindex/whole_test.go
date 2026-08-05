package focusedindex

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestRepositorySearchGenerationArchiveIsExactAndFailClosed(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	if err := os.WriteFile(
		filepath.Join(repositoryDir, "main.go"),
		[]byte("package main\nconst ArchiveNeedle = true\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	git(t, repositoryDir, "add", "main.go")
	git(t, repositoryDir, "commit", "-m", "fixture")
	const repository = "example.com/acme/archive-v2"
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: git(t, repositoryDir, "rev-parse", "HEAD"),
	}}
	indexDir := t.TempDir()
	wholeStage := buildWholeStageFixture(t, repository, revisions, 1)
	sourceStage := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDir, sourceStage, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishWholeGeneration(
		t.Context(), indexDir, wholeStage, sourceStage,
		repository, revisions, source,
	); err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	search, err := ValidateRepositorySearchGeneration(
		t.Context(), indexDir, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, names, err := validatedPublications(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "search-index.tar")
	report, err := CreateArchiveWithReport(indexDir, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if report != (ArchiveReport{Publications: 1}) {
		t.Fatalf("repository-search archive report = %+v", report)
	}
	restored := filepath.Join(t.TempDir(), "index")
	if err := RestoreArchive(archivePath, restored); err != nil {
		t.Fatal(err)
	}
	restoredSearch, err := ValidateRepositorySearchGeneration(
		t.Context(), restored, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restoredSearch.Digest != search.Digest {
		t.Fatalf("restored search digest = %q, want %q", restoredSearch.Digest, search.Digest)
	}
	for _, name := range names {
		before, err := os.ReadFile(filepath.Join(indexDir, name))
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(restored, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("restored repository-search artifact %q is not byte-exact", name)
		}
	}

	member := source.Members[0].Name
	if err := os.WriteFile(filepath.Join(indexDir, member), []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	omittedPath := filepath.Join(t.TempDir(), "omitted.tar")
	omitted, err := CreateArchiveWithReport(indexDir, omittedPath)
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Publications != 0 || omitted.OmittedPublications != 1 ||
		omitted.OmittedArtifacts == 0 {
		t.Fatalf("corrupt repository-search archive report = %+v", omitted)
	}
}

func TestWholePublicationCanonicalMultiShardAndCleanupIsolation(
	t *testing.T,
) {
	ctx := t.Context()
	indexDir := t.TempDir()
	const commit = "1111111111111111111111111111111111111111"
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	repositoryA := "example.com/acme/a"
	repositoryB := "example.com/acme/b"
	manifestA := publishWholeFixture(
		t, ctx, indexDir, repositoryA, revisions, 4,
	)
	manifestB := publishWholeFixture(
		t, ctx, indexDir, repositoryB, revisions, 2,
	)
	if len(manifestA.Members) < 2 {
		t.Fatalf(
			"fixture produced %d whole members, want multiple",
			len(manifestA.Members),
		)
	}
	for ordinal, member := range manifestA.Members {
		if !validWholeShardName(repositoryA, member.Name, ordinal) {
			t.Fatalf(
				"member %d name = %q, want canonical repository namespace",
				ordinal, member.Name,
			)
		}
	}

	// Even a self-digested control file cannot grant A authority over B's
	// cryptographic namespace.
	forged := manifestA
	forged.Members = append([]WholeShardMember(nil), manifestA.Members...)
	forged.Members[0].Name = manifestB.Members[0].Name
	forged.Digest = ""
	var err error
	forged.Digest, err = WholeManifestDigest(forged)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(
		indexDir, WholeManifestName(repositoryA),
	)
	forgedPath := manifestPath + ".forged"
	if err := WriteControlFile(forgedPath, forged); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(forgedPath, manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWholeManifestSelfContained(
		indexDir, repositoryA,
	); err == nil {
		t.Fatal("cross-repository whole member was accepted")
	}

	// An unreadable A member is still safely owned by A's hash namespace and
	// must not wedge deletion. B remains byte-valid.
	if err := os.WriteFile(
		filepath.Join(indexDir, manifestA.Members[1].Name),
		[]byte("unreadable"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRepository(ctx, indexDir, repositoryA); err != nil {
		t.Fatal(err)
	}
	for _, member := range manifestA.Members {
		if _, err := os.Lstat(
			filepath.Join(indexDir, member.Name),
		); !os.IsNotExist(err) {
			t.Fatalf("A member %q survived cleanup: %v", member.Name, err)
		}
	}
	if _, err := ValidateWholePublishedContext(
		ctx, indexDir, repositoryB, revisions,
	); err != nil {
		t.Fatalf("A cleanup damaged B publication: %v", err)
	}
}

func TestWholeArtifactNamesBoundLongRepositories(t *testing.T) {
	repository := "example.com/" + strings.Repeat("a", 900)
	for _, name := range []string{
		WholeManifestName(repository),
		WholeShardName(repository, 16, 123),
		PublishingName(repository),
	} {
		if len(name) > 255 {
			t.Fatalf("artifact name length = %d, want <= 255", len(name))
		}
		if filepath.Base(name) != name {
			t.Fatalf("artifact name escaped basename: %q", name)
		}
	}
	if !validWholeShardName(
		repository, WholeShardName(repository, 16, 123), 123,
	) {
		t.Fatal("long-repository canonical shard name was rejected")
	}
}

func TestManagedShardNameRequiresCryptographicNamespace(t *testing.T) {
	repository := "example.com/acme/managed-name"
	focused := ShardPrefix(
		repository, "sha256:"+strings.Repeat("1", 64),
	) + "_v16.00000.zoekt"
	tests := []struct {
		name string
		want bool
	}{
		{WholeShardName(repository, 16, 0), true},
		{focused, true},
		{"phebs-whole-not-a-hash_v16.00000.zoekt", false},
		{"phebs-focus-" + strings.Repeat("1", 63) + "-x.zoekt", false},
		{WholeManifestName(repository), false},
		{"nested/" + WholeShardName(repository, 16, 0), false},
	}
	for _, test := range tests {
		if got := IsManagedShardName(test.name); got != test.want {
			t.Errorf(
				"IsManagedShardName(%q) = %v, want %v",
				test.name, got, test.want,
			)
		}
	}
}

func TestRemoveRepositoryUsesCanonicalNameOwnership(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	const repositoryA = "example.com/acme/name-owner-a"
	const repositoryB = "example.com/acme/name-owner-b"

	t.Run("other namespace ignores target metadata", func(t *testing.T) {
		indexDir := t.TempDir()
		manifestA := publishWholeFixture(
			t, t.Context(), indexDir, repositoryA, revisions, 1,
		)
		manifestB := publishWholeFixture(
			t, t.Context(), indexDir, repositoryB, revisions, 1,
		)
		pathA := filepath.Join(indexDir, manifestA.Members[0].Name)
		pathB := filepath.Join(indexDir, manifestB.Members[0].Name)
		bytesA, err := os.ReadFile(pathA)
		if err != nil {
			t.Fatal(err)
		}
		bytesB, err := os.ReadFile(pathB)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathB, bytesA, 0o600); err != nil {
			t.Fatal(err)
		}

		if err := RemoveRepository(
			t.Context(), indexDir, repositoryA,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(pathB); err != nil {
			t.Fatalf("B-namespaced shard was metadata-deleted with A: %v", err)
		}
		if err := os.WriteFile(pathB, bytesB, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateWholePublishedContext(
			t.Context(), indexDir, repositoryB, revisions,
		); err != nil {
			t.Fatalf("restored B publication is invalid: %v", err)
		}
	})

	t.Run("target namespace ignores mixed metadata", func(t *testing.T) {
		indexDir := t.TempDir()
		manifestA := publishWholeFixture(
			t, t.Context(), indexDir, repositoryA, revisions, 1,
		)
		manifestB := publishWholeFixture(
			t, t.Context(), indexDir, repositoryB, revisions, 1,
		)
		pathA := filepath.Join(indexDir, manifestA.Members[0].Name)
		pathB := filepath.Join(indexDir, manifestB.Members[0].Name)
		replaceWithMergedWholeShard(t, pathA, pathA, pathB)

		if err := RemoveRepository(
			t.Context(), indexDir, repositoryA,
		); err != nil {
			t.Fatalf("mixed metadata wedged canonical A cleanup: %v", err)
		}
		if _, err := os.Lstat(pathA); !os.IsNotExist(err) {
			t.Fatalf("A-namespaced mixed shard survived cleanup: %v", err)
		}
		if _, err := ValidateWholePublishedContext(
			t.Context(), indexDir, repositoryB, revisions,
		); err != nil {
			t.Fatalf("A cleanup damaged B publication: %v", err)
		}
	})
}

func TestWholeReceiptRejectsForeignDecodedMetadata(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	const repositoryA = "example.com/acme/receipt-a"
	const repositoryB = "example.com/acme/receipt-b"
	indexDir := t.TempDir()
	manifestA := publishWholeFixture(
		t, t.Context(), indexDir, repositoryA, revisions, 1,
	)
	manifestB := publishWholeFixture(
		t, t.Context(), indexDir, repositoryB, revisions, 1,
	)
	pathA := filepath.Join(indexDir, manifestA.Members[0].Name)
	pathB := filepath.Join(indexDir, manifestB.Members[0].Name)
	bytesB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathA, bytesB, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestA.Members[0].ContentBytes = manifestB.Members[0].ContentBytes
	manifestA.Members[0].ContentDigest = manifestB.Members[0].ContentDigest
	manifestA.Members[0].MetadataDigest = manifestB.Members[0].MetadataDigest
	manifestA.Digest = ""
	manifestA.Digest, err = WholeManifestDigest(manifestA)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(
		indexDir, WholeManifestName(repositoryA),
	)
	replacement := manifestPath + ".replacement"
	if err := WriteControlFile(replacement, manifestA); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWholeManifest(
		indexDir, repositoryA, revisions,
	); err != nil {
		t.Fatalf("forged control identity should be self-consistent: %v", err)
	}
	if _, err := ValidateWholeReceipt(
		indexDir, repositoryA, revisions,
	); err == nil {
		t.Fatal("foreign decoded metadata passed local whole receipt")
	}
}

func TestWholeValidationRejectsMixedMultiShardGeneration(t *testing.T) {
	ctx := t.Context()
	indexDir := t.TempDir()
	repository := "example.com/acme/mixed-generation"
	oldRevisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: "1111111111111111111111111111111111111111",
	}}
	oldManifest := publishWholeFixture(
		t, ctx, indexDir, repository, oldRevisions, 4,
	)
	if len(oldManifest.Members) < 3 {
		t.Fatalf(
			"old fixture members = %d, want at least 3",
			len(oldManifest.Members),
		)
	}
	newRevisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: "2222222222222222222222222222222222222222",
	}}
	newStage := buildWholeStageFixture(
		t, repository, newRevisions, 4,
	)
	newManifest, err := createWholeStageManifest(
		ctx, newStage, repository, newRevisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(newManifest.Members) != len(oldManifest.Members) {
		t.Fatalf(
			"new/old member counts = %d/%d",
			len(newManifest.Members), len(oldManifest.Members),
		)
	}
	// Deterministically model a watcher-visible mix: the new receipt and first
	// shard have arrived while every remaining path still contains old bytes.
	replaceWholeFixtureFile(
		t,
		filepath.Join(
			newStage, WholeManifestName(repository),
		),
		filepath.Join(
			indexDir, WholeManifestName(repository),
		),
	)
	replaceWholeFixtureFile(
		t,
		filepath.Join(newStage, newManifest.Members[0].Name),
		filepath.Join(indexDir, newManifest.Members[0].Name),
	)
	if _, err := ValidateWholePublishedContext(
		ctx, indexDir, repository, newRevisions,
	); err == nil {
		t.Fatal("one-new-plus-old-members generation was accepted")
	}
}

func publishWholeFixture(
	t *testing.T,
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
	documents int,
) WholeManifest {
	t.Helper()
	stageDir := buildWholeStageFixture(
		t, repository, revisions, documents,
	)
	if err := PublishWhole(
		ctx, indexDir, stageDir, repository, revisions,
	); err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	manifest, err := ValidateWholePublishedContext(
		ctx, indexDir, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func buildWholeStageFixture(
	t *testing.T,
	repository string,
	revisions []store.IndexedRevision,
	documents int,
) string {
	t.Helper()
	stageDir := t.TempDir()
	description := zoekt.Repository{
		Name: repository,
		Branches: []zoekt.RepositoryBranch{{
			Name: revisions[0].Branch, Version: revisions[0].Commit,
		}},
	}
	builder, err := index.NewBuilder(index.Options{
		IndexDir:              stageDir,
		ShardPrefixOverride:   "builder",
		ShardMax:              1,
		Parallelism:           1,
		DisableCTags:          true,
		RepositoryDescription: description,
	})
	if err != nil {
		t.Fatal(err)
	}
	for document := range documents {
		if err := builder.Add(index.Document{
			Name: "file-" + strings.Repeat("x", document+1) + ".go",
			Content: []byte(
				"package fixture\nconst WholeNeedle" +
					strings.Repeat("X", document+1) + " = true\n",
			),
			Branches: []string{revisions[0].Branch},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	return stageDir
}

func replaceWholeFixtureFile(t *testing.T, source, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	temp := destination + ".replacement"
	if err := os.WriteFile(temp, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, destination); err != nil {
		t.Fatal(err)
	}
}

func replaceWithMergedWholeShard(
	t *testing.T,
	destination string,
	sources ...string,
) {
	t.Helper()
	files := make([]index.IndexFile, 0, len(sources))
	for _, source := range sources {
		file, err := os.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		indexFile, err := index.NewIndexFile(file)
		if err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		files = append(files, indexFile)
	}
	defer func() {
		for _, file := range files {
			file.Close()
		}
	}()
	temporary, _, err := index.Merge(t.TempDir(), files...)
	if err != nil {
		t.Fatal(err)
	}
	replaceWholeFixtureFile(t, temporary, destination)
	repositories, _, err := index.ReadMetadataPath(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != len(sources) {
		t.Fatalf(
			"merged fixture repositories = %d, want %d",
			len(repositories), len(sources),
		)
	}
}
