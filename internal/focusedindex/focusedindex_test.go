package focusedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	zoektsearch "github.com/sourcegraph/zoekt/search"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/store"
)

type focusedFixture struct {
	repo      string
	scope     analysisunit.Scope
	revisions []store.IndexedRevision
	head      string
}

func TestBackupLockExcludesPublicationMutations(t *testing.T) {
	indexDir := t.TempDir()
	releaseMutation, err := AcquireMutationLock(t.Context(), indexDir)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := AcquireBackupLock(blocked, indexDir); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("backup lock while mutation held = %v, want deadline", err)
	}
	releaseMutation()

	releaseBackup, err := AcquireBackupLock(t.Context(), indexDir)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := AcquireMutationLock(blocked, indexDir); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mutation lock while backup held = %v, want deadline", err)
	}
	releaseBackup()
}

func TestFocusedBuildIntegrityAndExactRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fixture := newFocusedFixture(t)
	stage := newOutputDir(t)
	request := Request{
		Schema:    RequestSchema,
		RepoDir:   fixture.repo,
		OutputDir: stage,
		Scope:     fixture.scope,
		Revisions: fixture.revisions,
	}
	result, err := Build(ctx, request, BuildOptions{ShardMax: 128})
	if err != nil {
		t.Fatal(err)
	}
	if result.OpenedBlobCount < 8 || result.OpenedBlobBytes < 1 ||
		result.OutOfUnitBlobReads != 0 || result.ShardCount < 2 {
		t.Fatalf("focused counters/result = %+v", result)
	}
	state, err := fixture.scope.State()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ValidateStage(stage, fixture.scope.Repository, state, fixture.revisions)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Digest != result.ManifestDigest || len(manifest.Members) != result.ShardCount {
		t.Fatalf("manifest/result mismatch: %+v %+v", manifest, result)
	}
	unrelated := filepath.Join(stage, "unrelated-transient.zoekt")
	if err := os.WriteFile(unrelated, []byte("not a readable zoekt shard"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStage(
		stage, fixture.scope.Repository, state, fixture.revisions,
	); err != nil {
		t.Fatalf("unrelated unreadable shard invalidated focused publication: %v", err)
	}
	if err := os.Remove(unrelated); err != nil {
		t.Fatal(err)
	}
	hits := searchIndex(t, ctx, stage, "ADMITTED_HEAD_NEEDLE branch:HEAD")
	if len(hits) != 1 || hits[0].FileName != "services/payments/src/main.go" ||
		hits[0].Version != fixture.head {
		t.Fatalf("admitted hits = %+v", hits)
	}
	if hits := searchIndex(t, ctx, stage, "OUT_OF_SCOPE_PHYSICAL_NEEDLE"); len(hits) != 0 {
		t.Fatalf("out-of-scope shard hits = %+v", hits)
	}
	for _, member := range manifest.Members {
		raw, err := os.ReadFile(filepath.Join(stage, member.Name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("OUT_OF_SCOPE_PHYSICAL_NEEDLE")) {
			t.Fatalf("out-of-scope needle present in physical member %s", member.Name)
		}
	}

	reader := &countingBlobReader{repoDir: fixture.repo, allowed: map[blobKey]bool{}}
	if _, err := reader.Read(ctx, "other/secret.go", strings.Repeat("a", 40)); err == nil ||
		reader.outOfUnitRead != 1 || reader.openedCount != 0 || reader.openedBytes != 0 {
		t.Fatalf("trusted reader refusal counters = %+v, err=%v", reader, err)
	}

	testManifestRefusals(t, stage, fixture, state, manifest)

	live := t.TempDir()
	if err := PublishFocused(
		ctx, live, stage, fixture.scope.Repository, state, fixture.revisions,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePublished(
		live, fixture.scope.Repository, state, fixture.revisions,
	); !errors.Is(err, ErrShardSet) {
		t.Fatalf("publication marker did not fail closed: %v", err)
	}
	if err := FinishPublication(live, fixture.scope.Repository); err != nil {
		t.Fatal(err)
	}
	published, err := ValidatePublished(
		live, fixture.scope.Repository, state, fixture.revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	orphanDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(orphanDir, "phebs-focus-"+strings.Repeat("0", 64)+"-orphan.zoekt"),
		[]byte("orphan"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	orphanArchive := filepath.Join(t.TempDir(), "focused-index.tar")
	orphanReport, err := CreateArchiveWithReport(orphanDir, orphanArchive)
	if err != nil {
		t.Fatal(err)
	}
	if orphanReport.OmittedArtifacts != 1 ||
		orphanReport.Publications != 0 {
		t.Fatalf("orphan archive report = %+v", orphanReport)
	}
	if err := VerifyArchive(orphanArchive); err != nil {
		t.Fatalf("verify orphan-omitting archive: %v", err)
	}

	archivePath := filepath.Join(t.TempDir(), "focused-index.tar")
	if err := startPublication(live, fixture.scope.Repository); err != nil {
		t.Fatal(err)
	}
	archiveReport, err := CreateArchiveWithReport(live, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if archiveReport.Publications != 1 ||
		archiveReport.StaleMarkers != 1 ||
		archiveReport.OmittedPublications != 0 ||
		archiveReport.OmittedArtifacts != 0 {
		t.Fatalf("stale-marker archive report = %+v", archiveReport)
	}
	if err := FinishPublication(live, fixture.scope.Repository); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "index")
	if err := RestoreArchive(archivePath, restored); err != nil {
		t.Fatal(err)
	}
	restoredManifest, err := ValidateSelfContained(restored, fixture.scope.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if restoredManifest.Digest != published.Digest {
		t.Fatalf("restored digest = %q, want %q", restoredManifest.Digest, published.Digest)
	}
	_, names, err := validatedPublications(live)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		before, err := os.ReadFile(filepath.Join(live, name))
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(restored, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("restored focused artifact %q is not byte-exact", name)
		}
	}

	rebuiltDir := newOutputDir(t)
	rebuiltRequest := request
	rebuiltRequest.OutputDir = rebuiltDir
	rebuilt, err := Build(ctx, rebuiltRequest, BuildOptions{ShardMax: 128})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.UnitDigest != result.UnitDigest ||
		rebuilt.GenerationDigest != result.GenerationDigest ||
		!sameHits(
			searchIndex(t, ctx, live, "ADMITTED_HEAD_NEEDLE"),
			searchIndex(t, ctx, rebuiltDir, "ADMITTED_HEAD_NEEDLE"),
		) {
		t.Fatal("independent rebuild changed semantic unit, generation, or search results")
	}
	// Manifest digests are deliberately not compared: zoekt embeds build
	// identity/time, so a fresh publication is semantic equality, not restore.
}

func TestFocusedBuildIndexesBlobAboveZoektDefaultSize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	content := []byte("package large\n")
	for len(content) < 3<<20 {
		content = append(content, "// ordinary focused filler line\n"...)
	}
	content = append(content, "const FocusedLargeBlobNeedle = \"FOCUSED_LARGE_BLOB_NEEDLE\"\n"...)
	path := filepath.Join(repo, "src", "large.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "large focused source")
	head := git(t, repo, "rev-parse", "HEAD")
	scope := analysisunit.Scope{
		Repository: "example.com/neutral/large",
		Name:       "large",
		Primary:    []string{"src"},
	}
	stage := newOutputDir(t)
	result, err := Build(ctx, Request{
		Schema:    RequestSchema,
		RepoDir:   repo,
		OutputDir: stage,
		Scope:     scope,
		Revisions: []store.IndexedRevision{{
			Selector: "HEAD",
			Branch:   "HEAD",
			Commit:   head,
		}},
	}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.OpenedBlobBytes != int64(len(content)) ||
		result.AdmittedSourceBytes != int64(len(content)) {
		t.Fatalf("large focused counters = %+v, want bytes=%d", result, len(content))
	}
	hits := searchIndex(t, ctx, stage, "FOCUSED_LARGE_BLOB_NEEDLE")
	if len(hits) != 1 || hits[0].FileName != "src/large.go" {
		t.Fatalf("large focused blob hits = %+v", hits)
	}
}

func TestFocusedBuildRefusesZoektContentTombstones(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{
			name:    "too small",
			content: []byte("x"),
			want:    "contains too few trigrams",
		},
		{
			name:    "binary",
			content: []byte("ordinary text before a NUL\x00after"),
			want:    "contains binary content",
		},
		{
			name:    "too many trigrams",
			content: highTrigramFocusedContent(),
			want:    "contains too many distinct trigrams",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			git(t, repo, "init", "-b", "main")
			path := filepath.Join(repo, "src", "input.txt")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, test.content, 0o644); err != nil {
				t.Fatal(err)
			}
			git(t, repo, "add", ".")
			git(t, repo, "commit", "-m", "content policy fixture")
			head := git(t, repo, "rev-parse", "HEAD")
			repository := "example.com/neutral/content-policy-" +
				strings.ReplaceAll(test.name, " ", "-")
			stage := newOutputDir(t)
			_, err := Build(t.Context(), Request{
				Schema:    RequestSchema,
				RepoDir:   repo,
				OutputDir: stage,
				Scope: analysisunit.Scope{
					Repository: repository,
					Name:       "source",
					Primary:    []string{"src"},
				},
				Revisions: []store.IndexedRevision{{
					Selector: "HEAD",
					Branch:   "HEAD",
					Commit:   head,
				}},
			}, BuildOptions{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build error = %v, want %q refusal", err, test.want)
			}
			if _, err := os.Lstat(
				filepath.Join(stage, ManifestName(repository)),
			); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused content published a manifest: %v", err)
			}
		})
	}
}

func highTrigramFocusedContent() []byte {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var content strings.Builder
	content.WriteString("high-trigram text fixture\n")
	for value := 0; value <= maxFocusedTrigrams; value++ {
		first := value / (len(alphabet) * len(alphabet))
		second := value / len(alphabet) % len(alphabet)
		third := value % len(alphabet)
		fmt.Fprintf(
			&content, "token %c%c%c\n",
			alphabet[first], alphabet[second], alphabet[third],
		)
	}
	return []byte(content.String())
}

func TestControlFileWriteBoundAndResultCounters(t *testing.T) {
	oversized := filepath.Join(t.TempDir(), "oversized.json")
	if err := WriteControlFile(oversized, strings.Repeat("x", maxControlBytes)); err == nil {
		t.Fatal("oversized control file write succeeded")
	}
	if _, err := os.Lstat(oversized); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized control file exists: %v", err)
	}

	digest := "sha256:" + strings.Repeat("a", 64)
	valid := Result{
		Schema:              ResultSchema,
		Repository:          "example.com/neutral/repo",
		UnitDigest:          digest,
		GenerationDigest:    digest,
		ManifestDigest:      digest,
		OpenedBlobCount:     2,
		OpenedBlobBytes:     20,
		AdmittedDocuments:   2,
		AdmittedSourceBytes: 20,
		ShardCount:          1,
		ShardBytes:          100,
		BuildWallNanos:      1,
	}
	tests := []struct {
		name   string
		mutate func(*Result)
	}{
		{"opened-count", func(result *Result) { result.OpenedBlobCount++ }},
		{"opened-bytes", func(result *Result) { result.OpenedBlobBytes++ }},
		{"out-of-unit", func(result *Result) { result.OutOfUnitBlobReads = 1 }},
		{"shard-count", func(result *Result) { result.ShardCount = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid
			test.mutate(&result)
			path := filepath.Join(t.TempDir(), "result.json")
			writeJSON(t, path, result)
			if _, err := ReadResult(path); err == nil {
				t.Fatal("invalid focused child result was accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "result.json")
	writeJSON(t, path, valid)
	if got, err := ReadResult(path); err != nil || got != valid {
		t.Fatalf("valid focused child result = %+v, %v", got, err)
	}
}

func TestControlFileIdentityRejectsSameShapeReplacement(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.json")
	rightPath := filepath.Join(dir, "right.json")
	for _, path := range []string{leftPath, rightPath} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stamp := time.Unix(1_700_000_000, 0)
	for _, path := range []string{leftPath, rightPath} {
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	left, err := os.Lstat(leftPath)
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.Lstat(rightPath)
	if err != nil {
		t.Fatal(err)
	}
	if sameControlFileIdentity(left, right) {
		t.Fatal("same-shape replacement passed the control-file identity gate")
	}
}

func TestDigestRegularFileContextHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shard.zoekt")
	if err := os.WriteFile(path, []byte("ordinary shard bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := DigestRegularFileContext(
		ctx, path,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("DigestRegularFileContext error = %v, want context.Canceled", err)
	}
}

func TestScopeOnlyReplacementAndRevisionRefusals(t *testing.T) {
	ctx := context.Background()
	fixture := newFocusedFixture(t)
	stage := newOutputDir(t)
	request := Request{
		Schema: RequestSchema, RepoDir: fixture.repo, OutputDir: stage,
		Scope: fixture.scope, Revisions: fixture.revisions,
	}
	if _, err := Build(ctx, request, BuildOptions{ShardMax: 128}); err != nil {
		t.Fatal(err)
	}
	oldState, _ := fixture.scope.State()
	live := t.TempDir()
	if err := PublishFocused(
		ctx, live, stage, fixture.scope.Repository, oldState, fixture.revisions,
	); err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(live, fixture.scope.Repository); err != nil {
		t.Fatal(err)
	}
	oldManifest, err := ValidatePublished(
		live, fixture.scope.Repository, oldState, fixture.revisions,
	)
	if err != nil {
		t.Fatal(err)
	}

	replacementScope := fixture.scope
	replacementScope.Supporting = nil
	replacementState, err := replacementScope.State()
	if err != nil {
		t.Fatal(err)
	}
	replacementDir := newOutputDir(t)
	replacementRequest := request
	replacementRequest.Scope = replacementScope
	replacementRequest.OutputDir = replacementDir
	if _, err := Build(ctx, replacementRequest, BuildOptions{ShardMax: 128}); err != nil {
		t.Fatal(err)
	}
	if err := PublishFocused(
		ctx, live, replacementDir, fixture.scope.Repository,
		replacementState, fixture.revisions,
	); err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(live, fixture.scope.Repository); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePublished(
		live, fixture.scope.Repository, replacementState, fixture.revisions,
	); err != nil {
		t.Fatal(err)
	}
	for _, old := range oldManifest.Members {
		if _, err := os.Lstat(filepath.Join(live, old.Name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old focused member %q survived scope replacement", old.Name)
		}
	}
	if hits := searchIndex(t, ctx, live, "SUPPORTING_CONTRACT_NEEDLE"); len(hits) != 0 {
		t.Fatalf("removed supporting path still searches: %+v", hits)
	}

	git(t, fixture.repo, "rm", "-r", "services/payments/src")
	git(t, fixture.repo, "commit", "-m", "remove selected scope")
	missingCommit := git(t, fixture.repo, "rev-parse", "HEAD")
	missing := append(slices.Clone(fixture.revisions), store.IndexedRevision{
		Selector: "missing", Branch: "refs/heads/missing", Commit: missingCommit,
	})
	failedDir := newOutputDir(t)
	failed := request
	failed.OutputDir = failedDir
	failed.Revisions = missing
	if _, err := Build(ctx, failed, BuildOptions{}); !errors.Is(err, ErrMissingScope) {
		t.Fatalf("missing revision error = %v, want ErrMissingScope", err)
	}
	if entries, err := os.ReadDir(failedDir); err != nil || len(entries) != 0 {
		t.Fatalf("failed revision published artifacts: entries=%v err=%v", entries, err)
	}

	git(t, fixture.repo, "reset", "--hard", fixture.head)
	contractPath := filepath.Join(fixture.repo, "contracts", "payment.proto")
	if err := os.Remove(contractPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other/secret.go", contractPath); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", "contracts/payment.proto")
	git(t, fixture.repo, "commit", "-m", "selected symlink")
	symlinkCommit := git(t, fixture.repo, "rev-parse", "HEAD")
	symlinkRequest := request
	symlinkRequest.OutputDir = newOutputDir(t)
	symlinkRequest.Revisions = []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: symlinkCommit,
	}}
	if _, err := Build(
		ctx, symlinkRequest, BuildOptions{},
	); !errors.Is(err, ErrSpecialEntry) {
		t.Fatalf("selected symlink error = %v, want ErrSpecialEntry", err)
	}
}

func testManifestRefusals(
	t *testing.T,
	source string,
	fixture focusedFixture,
	state *analysisunit.State,
	manifest Manifest,
) {
	t.Helper()
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"missing", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, manifest.Members[0].Name)); err != nil {
				t.Fatal(err)
			}
		}},
		{"extra", func(t *testing.T, dir string) {
			sourcePath := filepath.Join(dir, manifest.Members[0].Name)
			raw, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(dir, ShardPrefix(
					fixture.scope.Repository, manifest.GenerationDigest,
				)+"-extra.zoekt"),
				raw, 0o600,
			); err != nil {
				t.Fatal(err)
			}
		}},
		{"extra-control", func(t *testing.T, dir string) {
			if err := os.WriteFile(
				filepath.Join(
					dir, ArtifactBase(fixture.scope.Repository)+".unexpected",
				),
				[]byte("{}\n"), 0o600,
			); err != nil {
				t.Fatal(err)
			}
		}},
		{"sidecar", func(t *testing.T, dir string) {
			path := filepath.Join(dir, manifest.Members[0].Name+MemberSuffix)
			if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"stale", func(t *testing.T, dir string) {
			changed := manifest
			changed.GenerationDigest = "sha256:" + strings.Repeat("0", 64)
			changed.Digest, _ = ManifestDigest(changed)
			writeJSON(t, filepath.Join(dir, ManifestName(fixture.scope.Repository)), changed)
		}},
		{"trailing", func(t *testing.T, dir string) {
			path := filepath.Join(dir, ManifestName(fixture.scope.Repository))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(raw, []byte("{}\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run("refuse-"+test.name, func(t *testing.T) {
			dir := copyDirectory(t, source)
			test.mutate(t, dir)
			if _, err := ValidateStage(
				dir, fixture.scope.Repository, state, fixture.revisions,
			); !errors.Is(err, ErrShardSet) {
				t.Fatalf("error = %v, want ErrShardSet", err)
			}
		})
	}
}

func newFocusedFixture(t *testing.T) focusedFixture {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("services/payments/src/main.go", "package payments\nconst Needle = \"ADMITTED_V1_NEEDLE\"\n")
	for index := range 8 {
		write(
			fmt.Sprintf("services/payments/src/file%d.go", index),
			fmt.Sprintf("package payments\nconst Value%d = %q\n", index, strings.Repeat("x", 80+index)),
		)
	}
	write("contracts/payment.proto", "message Payment {} // SUPPORTING_CONTRACT_NEEDLE\n")
	write("other/secret.go", "package other\nconst Secret = \"OUT_OF_SCOPE_PHYSICAL_NEEDLE\"\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "v1")
	v1 := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "tag", "v1")

	write("services/payments/src/main.go", "package payments\nconst Needle = \"ADMITTED_RELEASE_NEEDLE\"\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "release")
	release := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "branch", "release")

	write("services/payments/src/main.go", "package payments\nconst Needle = \"ADMITTED_HEAD_NEEDLE\"\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "head")
	head := git(t, repo, "rev-parse", "HEAD")
	return focusedFixture{
		repo: repo,
		scope: analysisunit.Scope{
			Repository: "example.com/neutral/monorepo",
			Name:       "payments",
			Primary:    []string{"services/payments/src"},
			Supporting: []string{"contracts/payment.proto"},
		},
		revisions: []store.IndexedRevision{
			{Selector: "HEAD", Branch: "HEAD", Commit: head},
			{Selector: "release", Branch: "refs/heads/release", Commit: release},
			{Selector: "v1", Branch: "refs/tags/v1^{commit}", Commit: v1},
		},
		head: head,
	}
}

func newOutputDir(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shards")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=phebs", "-c", "user.email=phebs@example.test", "-C", dir,
	}, args...)
	output, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func searchIndex(
	t *testing.T,
	ctx context.Context,
	indexDir, expression string,
) []zoekt.FileMatch {
	t.Helper()
	searcher, err := zoektsearch.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()
	parsed, err := query.Parse(expression)
	if err != nil {
		t.Fatal(err)
	}
	result, err := searcher.Search(ctx, parsed, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result.Files
}

func sameHits(left, right []zoekt.FileMatch) bool {
	project := func(input []zoekt.FileMatch) []string {
		result := make([]string, 0, len(input))
		for _, hit := range input {
			result = append(result, hit.Repository+":"+hit.FileName+":"+hit.Version)
		}
		slices.Sort(result)
		return result
	}
	return slices.Equal(project(left), project(right))
}

func copyDirectory(t *testing.T, source string) string {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "copy")
	if err := filepath.WalkDir(source, func(
		path string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("refuse test symlink")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	return destination
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
