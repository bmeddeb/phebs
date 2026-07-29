package t301

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/gitobj"
)

type childInput struct {
	Scope  Scope  `json:"scope"`
	Corpus Corpus `json:"corpus"`
}

func defaultScope() Scope {
	return Scope{
		Repository: neutralRepository,
		Name:       "payments",
		Primary:    []string{"services/payments/src"},
		Supporting: []string{
			"contracts/payment.proto",
			"services/payments/generated",
			"services/payments/go.mod",
		},
	}
}

func TestT301FocusedIndexChild(t *testing.T) {
	if os.Getenv("T301_CHILD") != "1" {
		return
	}
	inputPath := os.Getenv("T301_CHILD_INPUT")
	outputPath := os.Getenv("T301_CHILD_OUTPUT")
	repoDir := os.Getenv("T301_CHILD_REPO")
	indexDir := os.Getenv("T301_CHILD_INDEX")
	for name, value := range map[string]string{
		"T301_CHILD_INPUT": inputPath, "T301_CHILD_OUTPUT": outputPath,
		"T301_CHILD_REPO": repoDir, "T301_CHILD_INDEX": indexDir,
	} {
		if value == "" {
			t.Fatalf("%s is required", name)
		}
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var input childInput
	if err := decodeJSONStrict(raw, &input); err != nil {
		t.Fatal(err)
	}
	result, err := BuildFocusedIndex(
		t.Context(), repoDir, indexDir, input.Scope, input.Corpus,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestT301FocusedIndexSpike(t *testing.T) {
	if testing.Short() {
		t.Skip("generates the over-limit neutral Git tree and physical shards")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	irrelevantFiles, err := parseIntEnv(
		"T301_IRRELEVANT_FILES", DefaultIrrelevantFiles,
	)
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(t.TempDir(), "neutral.git")
	generationStarted := time.Now()
	corpus, err := GenerateRepository(ctx, repoDir, irrelevantFiles)
	if err != nil {
		t.Fatalf("generate corpus: %v", err)
	}
	generationWall := time.Since(generationStarted)
	retained := loadCommittedResults(t)
	if corpus.Digest != retained.Corpus.Digest ||
		corpus.Head != retained.Corpus.Head ||
		corpus.Branch != retained.Corpus.Branch ||
		corpus.Tag != retained.Corpus.Tag ||
		corpus.MissingScopeCommit != retained.Corpus.MissingScopeCommit ||
		corpus.SymlinkCommit != retained.Corpus.SymlinkCommit ||
		corpus.GitlinkCommit != retained.Corpus.GitlinkCommit {
		t.Fatalf(
			"generated corpus drifted from retained identity:\ngot=%+v\nwant=%+v",
			corpus, retained.Corpus,
		)
	}
	scope := defaultScope()
	inputPath := filepath.Join(t.TempDir(), "input.json")
	inputBytes, err := json.Marshal(childInput{Scope: scope, Corpus: corpus})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, inputBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	run1Dir := filepath.Join(t.TempDir(), "run1")
	run1, peak1 := runBuildChild(t, ctx, inputPath, repoDir, run1Dir, false)
	run2Dir := filepath.Join(t.TempDir(), "run2")
	run2, peak2 := runBuildChild(t, ctx, inputPath, repoDir, run2Dir, false)

	if run1.CorpusDigest != run2.CorpusDigest ||
		run1.UnitDigest != run2.UnitDigest ||
		run1.GenerationDigest != run2.GenerationDigest {
		t.Fatalf("stable identities differ:\nrun1=%+v\nrun2=%+v", run1, run2)
	}
	for _, run := range []BuildResult{run1, run2} {
		if run.ShardCount < 2 {
			t.Fatalf("physical shard split not demonstrated: %+v", run)
		}
		for _, opened := range run.OpenedPaths {
			if strings.HasPrefix(opened, "bulk/") ||
				strings.HasPrefix(opened, "services/shipping/") {
				t.Fatalf("out-of-unit blob was opened: %q", opened)
			}
		}
	}

	searchStarted := time.Now()
	hits1, err := SearchVisible(ctx, run1Dir, "T301_FOCUSED_NEEDLE")
	if err != nil {
		t.Fatal(err)
	}
	searchWall := time.Since(searchStarted)
	hits2, err := SearchVisible(ctx, run2Dir, "T301_FOCUSED_NEEDLE")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits1) == 0 || !slices.Equal(hits1, hits2) {
		t.Fatalf("search results differ: run1=%v run2=%v", hits1, hits2)
	}
	outside, err := SearchVisible(ctx, run1Dir, "repository-overlay caller")
	if err != nil {
		t.Fatal(err)
	}
	if len(outside) != 0 {
		t.Fatalf("out-of-scope content entered physical shards: %v", outside)
	}

	missingMemberRefused := testMissingMember(t, run2Dir)
	extraMemberRefused := testExtraMember(t, run1Dir)
	mixedMemberRefused := testMixedMember(t, run1Dir)
	staleMemberRefused := testStaleMember(t, run1Dir)
	trailingJSONRefused := testTrailingJSON(t, run1Dir)
	interruptedRefused := testInterruptedChild(t, ctx, inputPath, repoDir)
	missingRevisionRefused, symlinkRefused, gitlinkRefused :=
		testRevisionMatrixRefusals(t, ctx, repoDir, scope, corpus)
	replacementIgnored, lazyFetchRefused :=
		testReplacementAndLazyFetchFences(t, ctx, repoDir, corpus)

	if generationWall > 30*time.Second ||
		time.Duration(max(run1.BuildWallNanos, run2.BuildWallNanos)) > 5*time.Second ||
		searchWall > 250*time.Millisecond ||
		max(peak1, peak2) > 512<<20 {
		t.Fatalf(
			"frozen local gates failed: generation=%s build=%s search=%s peak=%d",
			generationWall,
			time.Duration(max(run1.BuildWallNanos, run2.BuildWallNanos)),
			searchWall, max(peak1, peak2),
		)
	}

	results := Results{
		Schema: SchemaVersion, Decision: "GO",
		Reason:    "The pinned builder accepted exact Git blobs without projected commits; focused search, original path/revision provenance, physical splitting, and manifest refusal gates passed.",
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		ZoektModule: ZoektModuleVersion, Corpus: corpus, Scope: scope,
		Run1: run1, Run2: run2, EquivalentSearch: true,
		MissingMemberRefused:   missingMemberRefused,
		ExtraMemberRefused:     extraMemberRefused,
		MixedMemberRefused:     mixedMemberRefused,
		StaleMemberRefused:     staleMemberRefused,
		TrailingJSONRefused:    trailingJSONRefused,
		InterruptedRefused:     interruptedRefused,
		MissingRevisionRefused: missingRevisionRefused,
		SymlinkRefused:         symlinkRefused,
		GitlinkRefused:         gitlinkRefused,
		ReplacementIgnored:     replacementIgnored,
		LazyFetchRefused:       lazyFetchRefused,
		OutOfUnitBlobReads:     0,
		GenerationWallNanos:    generationWall.Nanoseconds(),
		PeakRSSBytes:           max(peak1, peak2),
		SearchWallNanos:        searchWall.Nanoseconds(),
	}
	if resultPath := os.Getenv("T301_RESULTS_PATH"); resultPath != "" {
		cleaned, err := cleanResultPath(resultPath)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cleaned, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnalysisUnitIdentityContract(t *testing.T) {
	scope := defaultScope()
	digest1, err := scope.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reordered := scope
	reordered.Supporting = []string{
		"services/payments/go.mod",
		"contracts/payment.proto",
		"services/payments/generated",
	}
	digest2, err := reordered.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest1 != digest2 {
		t.Fatalf("scope digest depends on input order: %s != %s", digest1, digest2)
	}
	changed := scope
	changed.Supporting = append(
		append([]string(nil), changed.Supporting...),
		"services/payments/README.md",
	)
	digest3, err := changed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest3 == digest1 {
		t.Fatal("scope change preserved unit digest")
	}
	overlap := scope
	overlap.Supporting = append(overlap.Supporting, "services/payments/src/main.go")
	if _, err := overlap.Digest(); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("overlap error = %v, want ErrInvalidScope", err)
	}
	interposedOverlap := Scope{
		Repository: scope.Repository,
		Name:       scope.Name,
		Primary:    []string{"a", "a-b", "a/c"},
	}
	if _, err := interposedOverlap.Digest(); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("interposed overlap error = %v, want ErrInvalidScope", err)
	}
	withoutSupporting := scope
	withoutSupporting.Supporting = nil
	if _, err := withoutSupporting.Digest(); err != nil {
		t.Fatalf("empty supporting scope should be valid: %v", err)
	}

	revisions := []Revision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: strings.Repeat("a", 40),
	}}
	generation1, err := GenerationDigest(scope, revisions)
	if err != nil {
		t.Fatal(err)
	}
	revisions[0].Commit = strings.Repeat("b", 40)
	generation2, err := GenerationDigest(scope, revisions)
	if err != nil {
		t.Fatal(err)
	}
	if generation1 == generation2 {
		t.Fatal("revision change preserved index-generation digest")
	}
	duplicate := append(revisions, revisions[0])
	if _, err := GenerationDigest(scope, duplicate); err == nil {
		t.Fatal("duplicate revision selector was accepted")
	}
	invalidName := scope
	invalidName.Name = string([]byte{0xff})
	if _, err := invalidName.Digest(); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("invalid unit name error = %v, want ErrInvalidScope", err)
	}
	output, err := exec.Command(
		"go", "list", "-m", "-f", "{{.Version}}",
		"github.com/sourcegraph/zoekt",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("read pinned zoekt module: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != ZoektModuleVersion {
		t.Fatalf("zoekt module = %q, want %q", got, ZoektModuleVersion)
	}
}

func TestCommittedResults(t *testing.T) {
	results := loadCommittedResults(t)
	if results.Schema != SchemaVersion || results.Decision != "GO" ||
		results.ZoektModule != ZoektModuleVersion ||
		results.Corpus.RegularFiles <= CurrentCorpusFileLimit ||
		results.Corpus.InventoryPathBytes <= CurrentInventoryByteCap ||
		!results.EquivalentSearch ||
		!results.MissingMemberRefused ||
		!results.ExtraMemberRefused ||
		!results.MixedMemberRefused ||
		!results.StaleMemberRefused ||
		!results.TrailingJSONRefused ||
		!results.InterruptedRefused ||
		!results.MissingRevisionRefused ||
		!results.SymlinkRefused ||
		!results.GitlinkRefused ||
		!results.ReplacementIgnored ||
		!results.LazyFetchRefused ||
		results.OutOfUnitBlobReads != 0 ||
		results.Run1.ShardCount < 2 ||
		results.Run2.ShardCount < 2 ||
		results.Run1.CorpusDigest != results.Corpus.Digest ||
		results.Run2.CorpusDigest != results.Corpus.Digest ||
		results.Run1.UnitDigest != results.Run2.UnitDigest ||
		results.Run1.GenerationDigest != results.Run2.GenerationDigest {
		t.Fatalf("invalid retained T30.1 result: %+v", results)
	}
	corpusDigest, err := CorpusDigest(results.Corpus)
	if err != nil || corpusDigest != results.Corpus.Digest {
		t.Fatalf("retained corpus digest mismatch: got=%q err=%v", corpusDigest, err)
	}
	unitDigest, err := results.Scope.Digest()
	if err != nil || unitDigest != results.Run1.UnitDigest {
		t.Fatalf("retained unit digest mismatch: got=%q err=%v", unitDigest, err)
	}
	if time.Duration(results.GenerationWallNanos) > 30*time.Second ||
		time.Duration(max(
			results.Run1.BuildWallNanos,
			results.Run2.BuildWallNanos,
		)) > 5*time.Second ||
		time.Duration(results.SearchWallNanos) > 250*time.Millisecond ||
		results.PeakRSSBytes > 512<<20 {
		t.Fatalf("retained T30.1 result exceeds frozen gate: %+v", results)
	}
}

func loadCommittedResults(t *testing.T) Results {
	t.Helper()
	raw, err := os.ReadFile("results.json")
	if err != nil {
		t.Fatal(err)
	}
	var results Results
	if err := decodeJSONStrict(raw, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

func runBuildChild(
	t *testing.T,
	ctx context.Context,
	inputPath,
	repoDir,
	indexDir string,
	injectFailure bool,
) (BuildResult, int64) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "child-result.json")
	cmd := exec.CommandContext(
		ctx, executable, "-test.run=^TestT301FocusedIndexChild$",
	)
	cmd.Env = append(os.Environ(),
		"T301_CHILD=1",
		"T301_CHILD_INPUT="+inputPath,
		"T301_CHILD_OUTPUT="+outputPath,
		"T301_CHILD_REPO="+repoDir,
		"T301_CHILD_INDEX="+indexDir,
	)
	if injectFailure {
		cmd.Env = append(cmd.Env, "T301_INJECT_CHILD_FAILURE=1")
	}
	output, runErr := cmd.CombinedOutput()
	if injectFailure {
		if runErr == nil {
			t.Fatal("injected child failure succeeded")
		}
		if !strings.Contains(string(output), "injected T30.1 child failure") {
			t.Fatalf("child failure output = %s", output)
		}
		return BuildResult{}, peakRSS(cmd.ProcessState)
	}
	if runErr != nil {
		t.Fatalf("focused index child: %v\n%s", runErr, output)
	}
	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var result BuildResult
	if err := decodeJSONStrict(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result, peakRSS(cmd.ProcessState)
}

func testMissingMember(t *testing.T, indexDir string) bool {
	t.Helper()
	manifest, err := ValidateShardSet(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(indexDir, manifest.Members[0].Name)); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateShardSet(indexDir)
	if !errors.Is(err, ErrShardSet) {
		t.Fatalf("missing-member error = %v, want ErrShardSet", err)
	}
	return true
}

func testExtraMember(t *testing.T, sourceDir string) bool {
	t.Helper()
	indexDir := copyIndexDir(t, sourceDir)
	manifest, err := ValidateShardSet(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(indexDir, manifest.Members[0].Name)
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "unexpected.zoekt"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateShardSet(indexDir)
	if !errors.Is(err, ErrShardSet) {
		t.Fatalf("extra-member error = %v, want ErrShardSet", err)
	}
	return true
}

func testMixedMember(t *testing.T, sourceDir string) bool {
	t.Helper()
	indexDir := copyIndexDir(t, sourceDir)
	manifest, err := ValidateShardSet(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	member := manifest.Members[0]
	sidecarPath := filepath.Join(indexDir, member.Name+memberSuffix)
	member.GenerationDigest = "sha256:" + strings.Repeat("0", 64)
	raw, err := json.Marshal(member)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateShardSet(indexDir)
	if !errors.Is(err, ErrShardSet) {
		t.Fatalf("mixed-member error = %v, want ErrShardSet", err)
	}
	return true
}

func testStaleMember(t *testing.T, sourceDir string) bool {
	t.Helper()
	indexDir := copyIndexDir(t, sourceDir)
	raw, err := os.ReadFile(filepath.Join(indexDir, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ShardManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	staleGeneration := "sha256:" + strings.Repeat("1", 64)
	manifest.GenerationDigest = staleGeneration
	for index := range manifest.Members {
		manifest.Members[index].GenerationDigest = staleGeneration
		sidecar, err := json.Marshal(manifest.Members[index])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(
				indexDir,
				manifest.Members[index].Name+memberSuffix,
			),
			sidecar,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	manifest.Digest, err = ManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(indexDir, manifestFileName), updated, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateShardSet(indexDir)
	if !errors.Is(err, ErrShardSet) {
		t.Fatalf("stale-member error = %v, want ErrShardSet", err)
	}
	return true
}

func testTrailingJSON(t *testing.T, sourceDir string) bool {
	t.Helper()
	indexDir := copyIndexDir(t, sourceDir)
	manifestPath := filepath.Join(indexDir, manifestFileName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(raw, []byte("{}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateShardSet(indexDir)
	if !errors.Is(err, ErrShardSet) {
		t.Fatalf("trailing-JSON error = %v, want ErrShardSet", err)
	}
	return true
}

func testInterruptedChild(
	t *testing.T,
	ctx context.Context,
	inputPath,
	repoDir string,
) bool {
	t.Helper()
	indexDir := filepath.Join(t.TempDir(), "interrupted")
	runBuildChild(t, ctx, inputPath, repoDir, indexDir, true)
	if _, err := os.Stat(filepath.Join(indexDir, manifestFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed child published visibility manifest: %v", err)
	}
	if _, err := ValidateShardSet(indexDir); !errors.Is(err, ErrShardSet) {
		t.Fatalf("interrupted set error = %v, want ErrShardSet", err)
	}
	return true
}

func testRevisionMatrixRefusals(
	t *testing.T,
	ctx context.Context,
	repoDir string,
	scope Scope,
	corpus Corpus,
) (bool, bool, bool) {
	t.Helper()
	missing := append([]Revision(nil), corpus.Revisions...)
	missing = append(missing, Revision{
		Selector: "missing", Branch: "refs/heads/missing",
		Commit: corpus.MissingScopeCommit,
	})
	if _, err := resolveScopeRecords(ctx, repoDir, scope, missing); !errors.Is(err, ErrMissingScope) {
		t.Fatalf("missing revision scope error = %v, want ErrMissingScope", err)
	}
	refused := map[string]bool{}
	for name, commit := range map[string]string{
		"symlink": corpus.SymlinkCommit,
		"gitlink": corpus.GitlinkCommit,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveScopeRecords(ctx, repoDir, scope, []Revision{{
				Selector: "HEAD", Branch: "HEAD", Commit: commit,
			}})
			if !errors.Is(err, ErrSpecialEntry) {
				t.Fatalf("special-entry error = %v, want ErrSpecialEntry", err)
			}
			refused[name] = true
		})
	}
	return true, refused["symlink"], refused["gitlink"]
}

func testReplacementAndLazyFetchFences(
	t *testing.T,
	ctx context.Context,
	repoDir string,
	corpus Corpus,
) (bool, bool) {
	t.Helper()
	originalOID, err := runGit(
		ctx, repoDir, nil, "rev-parse",
		corpus.Head+":services/payments/src/main.go",
	)
	if err != nil {
		t.Fatal(err)
	}
	replacementOID, err := writeBlob(
		ctx, repoDir, []byte("package payments\nconst REPLACED = true\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repoDir, nil, "replace", originalOID, replacementOID); err != nil {
		t.Fatal(err)
	}
	content, err := gitobj.ReadBlob(ctx, repoDir, originalOID, maxFocusedBlob)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "T301_FOCUSED_NEEDLE_HEAD") ||
		strings.Contains(string(content), "REPLACED") {
		t.Fatalf("replacement object redirected immutable read: %s", content)
	}

	missingRepo := filepath.Join(t.TempDir(), "missing.git")
	copyTree(t, repoDir, missingRepo)
	objectPath := filepath.Join(
		missingRepo, "objects", originalOID[:2], originalOID[2:],
	)
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(
		ctx, missingRepo, nil, "config", "remote.origin.promisor", "true",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(
		ctx, missingRepo, nil, "config", "extensions.partialClone", "origin",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := gitobj.ReadBlob(
		ctx, missingRepo, originalOID, maxFocusedBlob,
	); err == nil {
		t.Fatal("missing promisor object was fetched or accepted")
	}
	return true, true
}

func copyIndexDir(t *testing.T, source string) string {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "index")
	copyTree(t, source, destination)
	return destination
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filePath)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to copy symlink %q", filePath)
		}
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
}

func peakRSS(state *os.ProcessState) int64 {
	if state == nil {
		return 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	value := int64(usage.Maxrss)
	if runtime.GOOS != "darwin" {
		value *= 1024
	}
	return value
}
