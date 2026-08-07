package sourcepartition

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestBuildIsDeterministicAndBatchReadsEachUniqueBlobOnce(t *testing.T) {
	ctx := t.Context()
	repository, commit := sourceFixture(t, map[string][]byte{
		"a.go":       []byte("package demo\n\nconst A = 1\n"),
		"copy/a.go":  []byte("package demo\n\nconst A = 1\n"),
		"b.go":       []byte("package demo\n\nconst B = 2\n"),
		"README.txt": []byte("not selected\n"),
	})
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		ctx, repository, sourceDirectory, "example/repository",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{
		Schema: PolicySchema, Name: "go-source", Version: "1.0.0",
		IncludeSuffixes: []string{".go"},
	}
	firstDirectory := t.TempDir()
	secondDirectory := t.TempDir()
	var metrics BuildMetrics
	first, err := Build(ctx, BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: firstDirectory,
		Repository: source.Repository, Source: source, Policy: policy, Metrics: &metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(ctx, BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: secondDirectory,
		Repository: source.Repository, Source: source, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.BlobCount != 2 ||
		first.PlacementCount != 3 || metrics.SelectedPlacements != 3 ||
		metrics.UniqueBlobs != 2 || metrics.PeakSpoolBytes <= 0 {
		t.Fatalf("first plan = %+v metrics=%+v second=%+v", first, metrics, second)
	}
	assertDirectoriesEqual(t, firstDirectory, secondDirectory)
	plan, err := Open(ctx, firstDirectory, first)
	if err != nil {
		t.Fatal(err)
	}

	contents := map[string]int{}
	total := ReadStats{}
	for ordinal := range first.Members {
		stats, err := plan.ReadPartition(
			ctx, repository, ordinal,
			func(record BlobRecord, content []byte) error {
				contents[string(content)]++
				if record.ObjectID == "" || len(record.Placements) == 0 {
					t.Fatal("reader yielded an empty blob identity")
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Descriptors != MaxBatchDescriptors || stats.OutputBytes > MaxBatchOutputBytes {
			t.Fatalf("batch stats = %+v", stats)
		}
		total.Blobs += stats.Blobs
		total.Placements += stats.Placements
		total.BlobBytes += stats.BlobBytes
	}
	if total.Blobs != 2 || total.Placements != 3 ||
		contents["package demo\n\nconst A = 1\n"] != 1 ||
		contents["package demo\n\nconst B = 2\n"] != 1 {
		t.Fatalf("batch result stats=%+v contents=%v", total, contents)
	}
	privateFailure := errors.New("private/path/parser detail")
	_, err = plan.ReadPartition(ctx, repository, 0,
		func(BlobRecord, []byte) error { return privateFailure })
	if !errors.Is(err, privateFailure) || strings.Contains(err.Error(), "private") {
		t.Fatalf("visitor classification leaked detail: %v", err)
	}
}

func TestBatchReaderIgnoresReplacementRefs(t *testing.T) {
	ctx := t.Context()
	original := []byte("package demo\nconst Value = \"original\"\n")
	repository, commit := sourceFixture(t, map[string][]byte{"main.go": original})
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		ctx, repository, sourceDirectory, "example/replacement",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	planDirectory := t.TempDir()
	manifest, err := Build(ctx, BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: planDirectory,
		Repository: source.Repository, Source: source,
		Policy: Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Open(ctx, planDirectory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	records, err := readMember(
		ctx, planDirectory, len(manifest.Members), manifest.RevisionCount,
		manifest.Members[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	replacement := runGitInput(t, repository, []byte("replacement bytes\n"), "hash-object", "-w", "--stdin")
	runGit(t, repository, "replace", records[0].ObjectID, strings.TrimSpace(replacement))
	var got []byte
	_, err = plan.ReadPartition(ctx, repository, 0,
		func(_ BlobRecord, content []byte) error {
			got = slices.Clone(content)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("replacement ref redirected immutable read: %q", got)
	}
}

func TestBatchReaderClassifiesMissingWithoutIdentityLeak(t *testing.T) {
	ctx := t.Context()
	repository, commit := sourceFixture(t, map[string][]byte{
		"private/secret.go": []byte("package secret\n"),
	})
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		ctx, repository, sourceDirectory, "example/missing",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	planDirectory := t.TempDir()
	manifest, err := Build(ctx, BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: planDirectory,
		Repository: source.Repository, Source: source,
		Policy: Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Open(ctx, planDirectory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	records, err := readMember(
		ctx, planDirectory, len(manifest.Members), manifest.RevisionCount,
		manifest.Members[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	oid := records[0].ObjectID
	objectPath := filepath.Join(repository, ".git", "objects", oid[:2], oid[2:])
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	_, err = plan.ReadPartition(ctx, repository, 0,
		func(BlobRecord, []byte) error { return nil })
	if !errors.Is(err, store.ErrNotFound) || !errors.Is(err, ErrMissingObject) {
		t.Fatalf("missing error = %v", err)
	}
	if strings.Contains(err.Error(), oid) || strings.Contains(err.Error(), "secret") ||
		strings.Contains(err.Error(), repository) {
		t.Fatalf("missing error leaked source identity: %v", err)
	}
}

func TestBatchReaderCancellationAndExternalAlternatesFailClosed(t *testing.T) {
	repository, commit := sourceFixture(t, map[string][]byte{
		"main.go": []byte("package main\n"),
	})
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repository, sourceDirectory, "example/fences",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	planDirectory := t.TempDir()
	manifest, err := Build(t.Context(), BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: planDirectory,
		Repository: source.Repository, Source: source,
		Policy: Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Open(t.Context(), planDirectory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	_, err = plan.ReadPartition(canceled, repository, 0,
		func(BlobRecord, []byte) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled batch = calls %d, error %v", calls, err)
	}
	alternates := filepath.Join(repository, ".git", "objects", "info", "alternates")
	if err := os.MkdirAll(filepath.Dir(alternates), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alternates, []byte("/outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = plan.ReadPartition(t.Context(), repository, 0,
		func(BlobRecord, []byte) error { calls++; return nil })
	if !errors.Is(err, gitobj.ErrExternalAlternate) || calls != 0 {
		t.Fatalf("alternate batch = calls %d, error %v", calls, err)
	}
}

func TestExactABAPlanReusesSettledContentIdentity(t *testing.T) {
	repository, commitA := sourceFixture(t, map[string][]byte{
		"main.go": []byte("package main\nconst Version = \"a\"\n"),
	})
	sourceRoot := t.TempDir()
	sourceA, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repository, filepath.Join(sourceRoot, "a"), "example/aba",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commitA}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repository, "main.go"),
		[]byte("package main\nconst Version = \"b\"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "main.go")
	runGit(t, repository, "commit", "-m", "generation b")
	commitB := strings.TrimSpace(runGit(t, repository, "rev-parse", "HEAD"))
	sourceB, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repository, filepath.Join(sourceRoot, "b"), "example/aba",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commitB}},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}}
	planA1, planB, planA2 := t.TempDir(), t.TempDir(), t.TempDir()
	first, err := Build(t.Context(), BuildRequest{
		SourceDirectory: filepath.Join(sourceRoot, "a"), OutputDirectory: planA1,
		Repository: sourceA.Repository, Source: sourceA, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	middle, err := Build(t.Context(), BuildRequest{
		SourceDirectory: filepath.Join(sourceRoot, "b"), OutputDirectory: planB,
		Repository: sourceB.Repository, Source: sourceB, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	last, err := Build(t.Context(), BuildRequest{
		SourceDirectory: filepath.Join(sourceRoot, "a"), OutputDirectory: planA2,
		Repository: sourceA.Repository, Source: sourceA, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationDigest != last.GenerationDigest || first.Digest != last.Digest ||
		first.GenerationDigest == middle.GenerationDigest {
		t.Fatalf("A/B/A identities = %s / %s / %s", first.GenerationDigest, middle.GenerationDigest, last.GenerationDigest)
	}
	assertDirectoriesEqual(t, planA1, planA2)
}

func TestPlannerRefusesOversizedSelectedBlobBeforeGitRead(t *testing.T) {
	repository, commit := sourceFixture(t, map[string][]byte{
		"large.go": bytes.Repeat([]byte{'x'}, int(MaxBlobBytes)+1),
	})
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repository, sourceDirectory, "example/large",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(t.Context(), BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: t.TempDir(),
		Repository: source.Repository, Source: source,
		Policy: Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
	})
	if !errors.Is(err, ErrTooLarge) || strings.Contains(err.Error(), "large.go") {
		t.Fatalf("oversized error = %v", err)
	}
	var measurement *pipelinerefusal.Measurement
	if !errors.As(err, &measurement) || measurement == nil ||
		measurement.Dimension != pipelinerefusal.DimensionBlobBytes ||
		measurement.Observed != MaxBlobBytes+1 || measurement.Limit != MaxBlobBytes {
		t.Fatalf("oversized measurement = %+v", measurement)
	}
}

func TestPlannerBlobPlacementBoundAtOwningSplit(t *testing.T) {
	content := []byte("package shared\n")
	for _, testCase := range []struct {
		name  string
		files int
		want  bool
	}{
		{name: "exact", files: MaxPlacementsPerPartition},
		{name: "one over", files: MaxPlacementsPerPartition + 1, want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			files := make(map[string][]byte, testCase.files)
			for index := range testCase.files {
				files[fmt.Sprintf("file-%05d.go", index)] = content
			}
			repository, commit := sourceFixture(t, files)
			sourceDirectory := filepath.Join(t.TempDir(), "source")
			source, err := repositoryindex.BuildSourceGeneration(
				t.Context(), repository, sourceDirectory, "example/placements",
				[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Build(t.Context(), BuildRequest{
				SourceDirectory: sourceDirectory, OutputDirectory: t.TempDir(),
				Repository: source.Repository, Source: source,
				Policy: Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
			})
			if !testCase.want {
				if err != nil {
					t.Fatalf("exact bound: %v", err)
				}
				return
			}
			var measurement *pipelinerefusal.Measurement
			if !errors.Is(err, ErrTooLarge) || !errors.As(err, &measurement) ||
				measurement == nil ||
				measurement.Dimension != pipelinerefusal.DimensionBlobPlacements ||
				measurement.Observed != MaxPlacementsPerPartition+1 ||
				measurement.Limit != MaxPlacementsPerPartition {
				t.Fatalf("one-over refusal = %v, measurement=%+v", err, measurement)
			}
		})
	}
}

func TestClassifiedPlanLimitsAcceptExactAndRefuseOneOver(t *testing.T) {
	tests := []struct {
		name      string
		dimension pipelinerefusal.Dimension
		limit     int64
	}{
		{name: "blob bytes", dimension: pipelinerefusal.DimensionBlobBytes, limit: MaxBlobBytes},
		{name: "blob placements", dimension: pipelinerefusal.DimensionBlobPlacements, limit: MaxPlacementsPerPartition},
		{name: "partition count", dimension: pipelinerefusal.DimensionPartitionCount, limit: MaxPartitions},
		{name: "generation blob bytes", dimension: pipelinerefusal.DimensionGenerationBlobBytes, limit: MaxGenerationBlobBytes},
		{name: "generation encoded bytes", dimension: pipelinerefusal.DimensionGenerationEncodedBytes, limit: MaxGenerationContentBytes},
		{name: "generation control bytes", dimension: pipelinerefusal.DimensionGenerationControlBytes, limit: MaxManifestBytes},
		{name: "planning spool bytes", dimension: pipelinerefusal.DimensionPlanningSpoolBytes, limit: MaxSpoolBytes},
		{name: "batch header bytes", dimension: pipelinerefusal.DimensionBatchHeaderBytes, limit: 256},
		{name: "batch output bytes", dimension: pipelinerefusal.DimensionBatchOutputBytes, limit: MaxBatchOutputBytes},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			for _, observed := range []int64{testCase.limit - 1, testCase.limit} {
				if err := checkPlanLimit(testCase.dimension, observed, testCase.limit, "classified bound"); err != nil {
					t.Fatalf("observed %d at limit %d: %v", observed, testCase.limit, err)
				}
			}
			err := checkPlanLimit(testCase.dimension, testCase.limit+1, testCase.limit, "classified bound")
			var measurement *pipelinerefusal.Measurement
			if !errors.Is(err, ErrTooLarge) || !errors.As(err, &measurement) ||
				measurement == nil || measurement.Dimension != testCase.dimension ||
				measurement.Observed != testCase.limit+1 || measurement.Limit != testCase.limit {
				t.Fatalf("one-over refusal = %v, measurement=%+v", err, measurement)
			}
		})
	}
}

func TestPolicyAndStageValidationAreClosed(t *testing.T) {
	valid := Policy{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}}
	tests := []Policy{
		{Schema: "other", Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
		{Schema: PolicySchema, Name: "Go", Version: "1.0.0", IncludeSuffixes: []string{".go"}},
		{Schema: PolicySchema, Name: "go-source", Version: "v1", IncludeSuffixes: []string{".go"}},
		{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{"go"}},
		{Schema: PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".proto", ".go"}},
	}
	if err := ValidatePolicy(valid); err != nil {
		t.Fatal(err)
	}
	digest, err := PolicyDigest(valid)
	if err != nil || !validDigest(digest) {
		t.Fatalf("policy digest = %q, %v", digest, err)
	}
	for index, policy := range tests {
		if err := ValidatePolicy(policy); !errors.Is(err, ErrInvalid) {
			t.Errorf("policy %d error = %v", index, err)
		}
	}

	repository, commit := sourceFixture(t, map[string][]byte{"main.go": []byte("package main\n")})
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repository, sourceDirectory, "example/closed",
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	planDirectory := t.TempDir()
	manifest, err := Build(t.Context(), BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: planDirectory,
		Repository: source.Repository, Source: source, Policy: valid,
	})
	if err != nil {
		t.Fatal(err)
	}
	tooLarge := manifest
	tooLarge.DeclaredBytes = MaxGenerationBlobBytes + 1
	if err := ValidateManifest(tooLarge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-bound manifest error = %v", err)
	}
	if manifest.PartitionPolicy.MaxBatchDescriptors != MaxBatchDescriptors ||
		manifest.PartitionPolicy.MaxBatchDuration != MaxBatchDuration ||
		manifest.PartitionPolicy.MaxBatchOutputBytes != MaxBatchOutputBytes ||
		manifest.PartitionPolicy.MaxBlobBytes != MaxBlobBytes ||
		manifest.PartitionPolicy.MaxPartitionBlobBytes != MaxPartitionBlobBytes ||
		manifest.PartitionPolicy.MaxGenerationContentBytes != MaxGenerationContentBytes {
		t.Fatalf("frozen reader bounds = %+v", manifest.PartitionPolicy)
	}
	if err := os.WriteFile(filepath.Join(planDirectory, "extra"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStage(t.Context(), planDirectory, manifest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("extra stage error = %v", err)
	}
}

func sourceFixture(t *testing.T, files map[string][]byte) (string, string) {
	t.Helper()
	directory := t.TempDir()
	runGit(t, directory, "init")
	runGit(t, directory, "config", "user.email", "fixture@example.invalid")
	runGit(t, directory, "config", "user.name", "Fixture")
	for name, content := range files {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, directory, "add", ".")
	runGit(t, directory, "commit", "-m", "fixture")
	return directory, strings.TrimSpace(runGit(t, directory, "rev-parse", "HEAD"))
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	return runGitInput(t, directory, nil, arguments...)
}

func runGitInput(t *testing.T, directory string, input []byte, arguments ...string) string {
	t.Helper()
	command := execCommand(t.Context(), "git", append([]string{"-C", directory}, arguments...)...)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func assertDirectoriesEqual(t *testing.T, left, right string) {
	t.Helper()
	leftEntries, err := os.ReadDir(left)
	if err != nil {
		t.Fatal(err)
	}
	rightEntries, err := os.ReadDir(right)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftEntries) != len(rightEntries) {
		t.Fatalf("directory entry counts differ: %d != %d", len(leftEntries), len(rightEntries))
	}
	for index, entry := range leftEntries {
		if entry.Name() != rightEntries[index].Name() {
			t.Fatalf("directory entries differ: %s != %s", entry.Name(), rightEntries[index].Name())
		}
		leftBytes, err := os.ReadFile(filepath.Join(left, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		rightBytes, err := os.ReadFile(filepath.Join(right, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(leftBytes, rightBytes) {
			t.Fatalf("artifact %s differs", entry.Name())
		}
	}
}

// execCommand is a test seam kept small so production code never accepts a
// caller-selected executable.
var execCommand = func(ctx context.Context, name string, arguments ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, arguments...)
}
