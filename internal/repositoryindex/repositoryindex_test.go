package repositoryindex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestSourceGenerationOwnsRevisionPathsExactlyOnce(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	write(t, repositoryDir, "a.go", "package fixture\nconst Shared = 1\n", 0o644)
	write(t, repositoryDir, "b.go", "package fixture\nconst Changed = 1\n", 0o644)
	if err := os.Symlink("a.go", filepath.Join(repositoryDir, "a-link")); err != nil {
		t.Fatal(err)
	}
	git(t, repositoryDir, "add", ".")
	git(t, repositoryDir, "commit", "-m", "first")
	first := git(t, repositoryDir, "rev-parse", "HEAD")

	write(t, repositoryDir, "b.go", "package fixture\nconst Changed = 2\n", 0o644)
	write(t, repositoryDir, "tool.sh", "#!/bin/sh\nexit 0\n", 0o755)
	git(t, repositoryDir, "add", ".")
	git(t, repositoryDir, "commit", "-m", "second")
	second := git(t, repositoryDir, "rev-parse", "HEAD")
	revisions := []store.IndexedRevision{
		{Selector: "HEAD", Branch: "HEAD", Commit: second},
		{Selector: "before", Branch: "before", Commit: first},
	}
	stage := filepath.Join(t.TempDir(), "source")
	manifest, err := BuildSourceGeneration(
		t.Context(), repositoryDir, stage,
		"example.com/acme/source-generation", revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.OwnerCount != 5 || manifest.PlacementCount != 7 ||
		manifest.RegularOwnerCount != 4 || manifest.SymlinkOwnerCount != 1 ||
		manifest.GitlinkOwnerCount != 0 {
		t.Fatalf("unexpected census: %+v", manifest)
	}
	if len(manifest.RevisionMembers) != 2 ||
		manifest.RevisionMembers[0].PlacementCount != 4 ||
		manifest.RevisionMembers[1].PlacementCount != 3 {
		t.Fatalf("unexpected revision census: %+v", manifest.RevisionMembers)
	}
	var records []SourceRecord
	opened, err := WalkPublishedSource(
		t.Context(), stage, manifest.Repository,
		func(record SourceRecord) error {
			records = append(records, record)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Digest != manifest.Digest || len(records) != 5 {
		t.Fatalf("opened source = %+v records=%d", opened, len(records))
	}
	shared := 0
	for _, record := range records {
		if record.Path == "a.go" && slices.Equal(record.Revisions, []int{0, 1}) {
			shared++
		}
	}
	if shared != 1 {
		t.Fatalf("shared physical owner count = %d, records=%+v", shared, records)
	}

	rootPath := filepath.Join(stage, SourceManifestName(manifest.Repository))
	root, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, bytes.TrimSpace(root), "", "  "); err != nil {
		t.Fatal(err)
	}
	indented.WriteByte('\n')
	if err := os.WriteFile(rootPath, indented.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSourceManifest(stage, manifest.Repository); err == nil {
		t.Fatal("noncanonical source root was accepted")
	}
	if err := os.WriteFile(rootPath, root, 0o600); err != nil {
		t.Fatal(err)
	}

	memberPath := filepath.Join(stage, manifest.Members[0].Name)
	content, err := os.ReadFile(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 1
	if err := os.WriteFile(memberPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WalkPublishedSource(
		context.Background(), stage, manifest.Repository, nil,
	); err == nil {
		t.Fatal("tampered source member was accepted")
	}
}

func TestEmptyCommitHasCanonicalZeroOwnerGeneration(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	git(t, repositoryDir, "commit", "--allow-empty", "-m", "empty")
	commit := git(t, repositoryDir, "rev-parse", "HEAD")
	stage := filepath.Join(t.TempDir(), "source")
	manifest, err := BuildSourceGeneration(
		t.Context(), repositoryDir, stage, "example.com/acme/empty",
		[]store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: commit,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.OwnerCount != 0 || manifest.PlacementCount != 0 ||
		len(manifest.Members) != 0 || manifest.EncodedMemberBytes != 0 {
		t.Fatalf("empty source generation = %+v", manifest)
	}
	if _, err := WalkPublishedSource(
		t.Context(), stage, manifest.Repository, nil,
	); err != nil {
		t.Fatal(err)
	}
}

func TestSourceWriterAcceptsEscapedCanonicalPath(t *testing.T) {
	components := make([]string, 16)
	for index := range components {
		components[index] = strings.Repeat("&", 250)
	}
	path := strings.Join(components, "/")
	if len(path) > 4_096 {
		t.Fatalf("fixture path bytes = %d", len(path))
	}
	directory := t.TempDir()
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: strings.Repeat("a", 40),
	}}
	writer := sourceWriter{
		directory: directory, repository: "example.com/acme/escaped-path",
		revisions: revisions,
	}
	record := SourceRecord{
		Schema: SourceMemberSchema, Path: path, Mode: "100644",
		Kind: "regular", ObjectID: strings.Repeat("b", 40),
		DeclaredBytes: 1, Revisions: []int{0},
	}
	if err := validateRecord(record, 1); err != nil {
		t.Fatal(err)
	}
	if err := writer.add(record); err != nil {
		t.Fatal(err)
	}
	members, total, err := writer.finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || total <= int64(len(path)) {
		t.Fatalf("escaped member = %+v total=%d", members, total)
	}
}

func TestParseTreeRecordKinds(t *testing.T) {
	tests := []struct {
		name, raw, kind string
		wantBytes       int64
		wantError       bool
	}{
		{"regular", "100644 blob " + strings.Repeat("a", 40) + " 12\tfile.go", "regular", 12, false},
		{"executable", "100755 blob " + strings.Repeat("b", 40) + " 4\ttool", "regular", 4, false},
		{"symlink", "120000 blob " + strings.Repeat("c", 40) + " 7\tlink", "symlink", 7, false},
		{"gitlink", "160000 commit " + strings.Repeat("d", 40) + " -\tmodule", "gitlink", 0, false},
		{"unsupported", "100664 blob " + strings.Repeat("e", 40) + " 1\tbad", "", 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record, err := parseTreeRecord([]byte(test.raw))
			if (err != nil) != test.wantError {
				t.Fatalf("parseTreeRecord() error = %v", err)
			}
			if err == nil && (record.kind != test.kind ||
				record.declaredBytes != test.wantBytes) {
				t.Fatalf("parseTreeRecord() = %+v", record)
			}
		})
	}
}

func TestSearchGenerationBindsSourceAndPhysicalRoots(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	write(t, repositoryDir, "main.go", "package main\n", 0o644)
	git(t, repositoryDir, "add", ".")
	git(t, repositoryDir, "commit", "-m", "one")
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: git(t, repositoryDir, "rev-parse", "HEAD"),
	}}
	stage := filepath.Join(t.TempDir(), "source")
	const repository = "example.com/acme/search-generation"
	source, err := BuildSourceGeneration(
		t.Context(), repositoryDir, stage, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	physical := PhysicalRoot{
		Schema: "test-whole-v1", ManifestName: "whole.json",
		ManifestDigest: "sha256:" + strings.Repeat("a", 64),
		Members: []PhysicalMember{{
			Ordinal: 0, Count: 1, Name: "whole.00000.zoekt",
			ContentDigest:  "sha256:" + strings.Repeat("b", 64),
			ContentBytes:   123,
			MetadataDigest: "sha256:" + strings.Repeat("c", 64),
		}},
	}
	written, err := WriteSearchManifest(
		stage, repository, revisions, source, physical,
	)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := ValidatePublished(
		t.Context(), stage, repository, revisions, physical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Digest != written.Digest ||
		opened.SourceGenerationDigest != source.Digest {
		t.Fatalf("opened search generation = %+v", opened)
	}
	changed := physical
	changed.ManifestDigest = "sha256:" + strings.Repeat("d", 64)
	if _, err := ValidatePublished(
		t.Context(), stage, repository, revisions, changed,
	); err == nil {
		t.Fatal("search generation accepted a different physical root")
	}
}

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := []string{
		"-c", "user.name=T34.1", "-c", "user.email=t341@example.invalid",
		"-C", directory,
	}
	commandArgs = append(commandArgs, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func write(
	t *testing.T,
	directory, name, content string,
	mode os.FileMode,
) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
