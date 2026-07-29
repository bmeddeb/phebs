package extract

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

type corpusGitFixture struct {
	t        *testing.T
	source   string
	dataDir  string
	repoName string
}

func newCorpusGitFixture(t *testing.T) *corpusGitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &corpusGitFixture{
		t: t, source: t.TempDir(), dataDir: t.TempDir(), repoName: "example.com/corpus/repo",
	}
	f.git(f.source, "init", "-q")
	return f
}

func (f *corpusGitFixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *corpusGitFixture) gitInput(dir, input string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *corpusGitFixture) addSymlink(name, target string) {
	f.t.Helper()
	fullPath := filepath.Join(f.source, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, fullPath); err != nil {
		f.t.Skipf("symlinks unsupported: %v", err)
	}
	f.git(f.source, "add", "--", name)
}

func (f *corpusGitFixture) stageBlob(mode, name, content string) {
	f.t.Helper()
	oid := f.gitInput(f.source, content, "hash-object", "-w", "--stdin")
	f.git(f.source, "update-index", "--add", "--cacheinfo", mode+","+oid+","+name)
}

func (f *corpusGitFixture) commitFile(name, content, message string) string {
	f.t.Helper()
	fullPath := filepath.Join(f.source, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(f.source, "add", "--", name)
	f.git(f.source, "commit", "-q", "-m", message)
	return f.git(f.source, "rev-parse", "HEAD")
}

func (f *corpusGitFixture) cloneMirror() string {
	f.t.Helper()
	mirror := filepath.Join(f.dataDir, "repos", filepath.FromSlash(f.repoName)+".git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.git(".", "clone", "-q", "--mirror", f.source, mirror)
	return mirror
}

func TestCorpusRejectsInvalidPathsAndMutableRefs(t *testing.T) {
	for _, filePath := range []string{"", "/absolute", "../escape", "a/../b", `a\b.proto`, string([]byte{'a', 0xff})} {
		if err := checkCorpusPath(filePath); err == nil {
			t.Errorf("checkCorpusPath(%q) succeeded", filePath)
		}
	}
	for _, commit := range []string{"HEAD", strings.Repeat("A", 40), strings.Repeat("a", 39), strings.Repeat("a", 41)} {
		if err := checkCommit(commit); err == nil {
			t.Errorf("checkCommit(%q) succeeded", commit)
		}
	}
}

// walkThenRead mirrors the production order: reads are only served for
// entries recorded by the pinned-commit walk.
func walkThenRead(t *testing.T, corpus sdk.Corpus, filePath string) (sdk.Blob, error) {
	t.Helper()
	if err := corpus.WalkFiles(context.Background(), func(string) error { return nil }); err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	return corpus.Read(context.Background(), filePath)
}

func TestGitCorpusIgnoresMutableReplaceRefs(t *testing.T) {
	f := newCorpusGitFixture(t)
	oldCommit := f.commitFile("api.proto", "old immutable content", "old")
	newCommit := f.commitFile("api.proto", "replacement content", "new")
	mirror := f.cloneMirror()
	f.git(mirror, "replace", oldCommit, newCommit)

	corpus := GitCorpus(f.dataDir).New(f.repoName, oldCommit)
	blob, err := walkThenRead(t, corpus, "api.proto")
	if err != nil {
		t.Fatal(err)
	}
	if blob.Content != "old immutable content" {
		t.Fatalf("replacement ref changed pinned corpus: %q", blob.Content)
	}
}

func TestGitCorpusReadsPathspecMagicLiterally(t *testing.T) {
	f := newCorpusGitFixture(t)
	filePath := "literal[1].proto"
	if err := os.WriteFile(filepath.Join(f.source, filePath), []byte("literal content"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(f.source, "add", "-A")
	f.git(f.source, "commit", "-q", "-m", "literal pathspec")
	head := f.git(f.source, "rev-parse", "HEAD")
	f.cloneMirror()
	blob, err := walkThenRead(t, GitCorpus(f.dataDir).New(f.repoName, head), filePath)
	if err != nil {
		t.Fatal(err)
	}
	if blob.Content != "literal content" {
		t.Fatalf("blob content = %q", blob.Content)
	}
}

func TestGitCorpusReadAbsentWalkedPathReturnsNotFound(t *testing.T) {
	f := newCorpusGitFixture(t)
	head := f.commitFile("api.proto", "content", "content")
	f.cloneMirror()

	_, err := walkThenRead(t, GitCorpus(f.dataDir).New(f.repoName, head), "missing.proto")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Read missing path error = %v, want ErrNotFound", err)
	}
}

func TestGitCorpusExactLookupAndRootProbeAreCommitBound(t *testing.T) {
	f := newCorpusGitFixture(t)
	oldCommit := f.commitFile("svc/[literal]/api.go", "package old\n", "old")
	f.commitFile("svc/[literal]/api.go", "package current\n", "current")
	f.commitFile("outside.go", "package outside\n", "outside")
	f.addSymlink("links/api.go", "../outside.go")
	f.git(f.source, "commit", "-q", "-m", "symlink")
	f.cloneMirror()

	corpus := GitCorpus(f.dataDir).New(f.repoName, oldCommit).(*gitCorpus)
	entry, present, err := corpus.lookupRegular(
		context.Background(), "svc/[literal]/api.go")
	if err != nil {
		t.Fatal(err)
	}
	if !present || entry.path != "svc/[literal]/api.go" ||
		entry.mode != "100644" || entry.objectType != "blob" ||
		entry.size != int64(len("package old\n")) {
		t.Fatalf("exact entry = %+v, present=%v", entry, present)
	}
	blob, err := corpus.readManifestBlob(context.Background(), entry, MaxBlobBytes)
	if err != nil {
		t.Fatal(err)
	}
	if blob.Content != "package old\n" {
		t.Fatalf("exact blob observed mutable ref content %q", blob.Content)
	}

	for _, testCase := range []struct {
		path string
		want bool
	}{
		{"missing.go", false},
		{"svc", false},
	} {
		_, got, err := corpus.lookupRegular(context.Background(), testCase.path)
		if err != nil || got != testCase.want {
			t.Fatalf("lookupRegular(%q) = %v, %v", testCase.path, got, err)
		}
	}
	if got, err := corpus.anyRegularUnder(context.Background(), "svc"); err != nil || !got {
		t.Fatalf("anyRegularUnder(svc) = %v, %v", got, err)
	}
	// This commit predates both the symlink and its target. The probe is pinned
	// to the exact tree rather than the mirror's mutable default branch.
	if got, err := corpus.anyRegularUnder(context.Background(), "links"); err != nil || got {
		t.Fatalf("anyRegularUnder(links) = %v, %v", got, err)
	}
}

func TestGitCorpusManifestBlobChecksDeclaredSize(t *testing.T) {
	f := newCorpusGitFixture(t)
	head := f.commitFile("api.proto", "content", "content")
	f.cloneMirror()
	corpus := GitCorpus(f.dataDir).New(f.repoName, head).(*gitCorpus)
	entry, present, err := corpus.lookupRegular(context.Background(), "api.proto")
	if err != nil || !present {
		t.Fatalf("lookupRegular = %+v, %v, %v", entry, present, err)
	}
	entry.size++
	if _, err := corpus.readManifestBlob(
		context.Background(), entry, MaxBlobBytes); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("declared-size mismatch error = %v", err)
	}
}

func TestReadNULRecordRejectsTruncatedRecord(t *testing.T) {
	_, err := readNULRecord(bufio.NewReader(strings.NewReader("unterminated")), 64)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readNULRecord error = %v", err)
	}
}

func TestStopTreeCommandKillsBlockedWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("process cleanup regression")
	}
	cmd := exec.Command("sh", "-c", `while :; do printf 'tree-output-tree-output-tree-output\n'; done`)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Prove the child reached its writer before abandoning the pipe.
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(stdout, buffer); err != nil {
		t.Fatalf("read child output: %v", err)
	}

	done := make(chan struct{})
	go func() {
		stopTreeCommand(cmd, stdout)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("blocked tree writer was not killed and reaped")
	}
	if cmd.ProcessState == nil {
		t.Fatal("tree writer was not reaped")
	}
}

func TestGitCorpusWalkPreservesContextCancellation(t *testing.T) {
	f := newCorpusGitFixture(t)
	head := f.commitFile("api.proto", "content", "content")
	f.cloneMirror()

	ctx, cancel := context.WithCancel(context.Background())
	err := GitCorpus(f.dataDir).New(f.repoName, head).WalkFiles(
		ctx,
		func(string) error {
			cancel()
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WalkFiles error = %v, want context.Canceled", err)
	}
}

func TestGitCorpusSymlinkAndGitlinkPolicy(t *testing.T) {
	t.Run("unrelated symlink skipped", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		f.commitFile("api.proto", "regular", "regular")
		if err := os.Symlink("api.proto", filepath.Join(f.source, "docs-link")); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		f.git(f.source, "add", "docs-link")
		f.git(f.source, "commit", "-q", "-m", "unrelated symlink")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		var paths []string
		err := GitCorpus(f.dataDir).New(f.repoName, head).WalkFiles(context.Background(), func(filePath string) error {
			paths = append(paths, filePath)
			return nil
		})
		if err != nil || !slices.Equal(paths, []string{"api.proto"}) {
			t.Fatalf("WalkFiles = %v, %v", paths, err)
		}
	})

	t.Run("candidate-shaped symlink skipped by generic walk", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		f.commitFile("target.proto", "regular", "target")
		f.addSymlink("linked.proto", "target.proto")
		f.git(f.source, "commit", "-q", "-m", "candidate-shaped symlink")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		var paths []string
		err := GitCorpus(f.dataDir).New(f.repoName, head).WalkFiles(
			context.Background(), func(filePath string) error {
				paths = append(paths, filePath)
				return nil
			})
		if err != nil || !slices.Equal(paths, []string{"target.proto"}) {
			t.Fatalf("WalkFiles = %v, %v", paths, err)
		}
	})

	t.Run("root SCIP index symlink rejected", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		f.commitFile("target", "not parsed", "target")
		if err := os.Symlink("target", filepath.Join(f.source, scipIndexPath)); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		f.git(f.source, "add", scipIndexPath)
		f.git(f.source, "commit", "-q", "-m", "SCIP index symlink")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		err := GitCorpus(f.dataDir).New(f.repoName, head).WalkFiles(
			context.Background(), func(string) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "SCIP index symlink") {
			t.Fatalf("SCIP index symlink error = %v", err)
		}
	})

	for _, snapshotPath := range attributionSnapshotPaths {
		t.Run("attribution snapshot symlink "+snapshotPath, func(t *testing.T) {
			f := newCorpusGitFixture(t)
			f.commitFile("target", "not parsed", "target")
			if err := os.Symlink("target", filepath.Join(f.source, snapshotPath)); err != nil {
				t.Skipf("symlinks unsupported: %v", err)
			}
			f.git(f.source, "add", snapshotPath)
			f.git(f.source, "commit", "-q", "-m", "attribution snapshot symlink")
			head := f.git(f.source, "rev-parse", "HEAD")
			f.cloneMirror()
			err := GitCorpus(f.dataDir).New(f.repoName, head).WalkFiles(
				context.Background(), func(string) error { return nil })
			if err == nil || !strings.Contains(err.Error(), "attribution snapshot symlink") {
				t.Fatalf("attribution snapshot symlink error = %v", err)
			}
		})
	}

	t.Run("gitlink boundaries recorded not traversed", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		parent := f.commitFile("README", "root", "root")
		f.git(f.source, "update-index", "--add", "--cacheinfo", "160000,"+parent+",third_party")
		f.git(f.source, "update-index", "--add", "--cacheinfo", "160000,"+parent+",vendor_src/idl")
		f.git(f.source, "commit", "-q", "-m", "gitlinks")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		corpus := GitCorpus(f.dataDir).New(f.repoName, head)
		var paths []string
		if err := corpus.WalkFiles(context.Background(), func(filePath string) error {
			paths = append(paths, filePath)
			return nil
		}); err != nil {
			t.Fatalf("WalkFiles = %v", err)
		}
		// Gitlink descendants are never enumerated or attributed to the
		// parent repository; only the regular blob is visited.
		if !slices.Equal(paths, []string{"README"}) {
			t.Fatalf("visited paths = %v", paths)
		}
		boundaryAware, ok := corpus.(boundaryCorpus)
		if !ok {
			t.Fatal("git corpus does not expose gitlink boundaries")
		}
		boundaries := boundaryAware.gitlinkBoundaries()
		hash := sha256.New()
		hash.Write([]byte(gitlinkDigestDomain))
		for _, record := range []string{"third_party", parent, "vendor_src/idl", parent} {
			hash.Write([]byte(record))
			hash.Write([]byte{0})
		}
		wantDigest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
		if boundaries.count != 2 || boundaries.digest != wantDigest ||
			!slices.Equal(boundaries.samplePaths, []string{"third_party", "vendor_src/idl"}) ||
			boundaries.sampleTruncated {
			t.Fatalf("boundaries = %+v, want digest %s", boundaries, wantDigest)
		}
	})

	t.Run("gitlink sample bounds and unsafe paths", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		parent := f.commitFile("README", "root", "root")
		// One unsafe path (backslash) plus enough safe gitlinks to overflow
		// the sample entry bound: count and digest bind everything, the
		// rendered sample stays bounded, and truncation is explicit.
		f.git(f.source, "update-index", "--add", "--cacheinfo", "160000,"+parent+`,a\b`)
		for i := 0; i < maxGitlinkSamplePaths+1; i++ {
			f.git(f.source, "update-index", "--add", "--cacheinfo",
				fmt.Sprintf("160000,%s,links/mod%03d", parent, i))
		}
		f.git(f.source, "commit", "-q", "-m", "many gitlinks")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		corpus := GitCorpus(f.dataDir).New(f.repoName, head)
		if err := corpus.WalkFiles(context.Background(), func(string) error { return nil }); err != nil {
			t.Fatalf("WalkFiles = %v", err)
		}
		boundaries := corpus.(boundaryCorpus).gitlinkBoundaries()
		if boundaries.count != maxGitlinkSamplePaths+2 ||
			len(boundaries.samplePaths) != maxGitlinkSamplePaths ||
			!boundaries.sampleTruncated {
			t.Fatalf("boundaries = count %d, sample %d, truncated %v",
				boundaries.count, len(boundaries.samplePaths), boundaries.sampleTruncated)
		}
		for _, samplePath := range boundaries.samplePaths {
			if strings.Contains(samplePath, `\`) {
				t.Fatalf("unsafe path leaked into sample: %q", samplePath)
			}
		}
	})
}

func TestGitCorpusCandidateSymlinkPolicy(t *testing.T) {
	t.Run("safe relative chain extracts final path once", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		f.commitFile("shared/real.thrift", "service Real {}", "regular target")
		f.commitFile("svc/main.go", "package svc", "unrelated Go source")
		f.addSymlink("shared/intermediate", "real.thrift")
		f.addSymlink("api/service.thrift", "../shared/intermediate")
		f.git(f.source, "commit", "-q", "-m", "safe alias chain")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()

		verified := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, head))
		if err := verified.Inventory(
			context.Background(),
			func(filePath string) bool { return strings.HasSuffix(filePath, ".thrift") },
		); err != nil {
			t.Fatalf("Inventory: %v", err)
		}
		var paths []string
		if err := verified.WalkFiles(context.Background(), func(filePath string) error {
			paths = append(paths, filePath)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(paths, []string{"shared/real.thrift", "svc/main.go"}) {
			t.Fatalf("enumerated paths = %v", paths)
		}
		blob, err := verified.Read(context.Background(), "shared/real.thrift")
		if err != nil || blob.Content != "service Real {}" {
			t.Fatalf("final target read = %q, %v", blob.Content, err)
		}
		if _, err := verified.Read(context.Background(), "api/service.thrift"); err == nil {
			t.Fatal("logical symlink alias became readable")
		}
		stats, err := verified.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if stats.corpusFileCount != 2 || stats.candidateFileCount != 1 ||
			stats.readFileCount != 1 {
			t.Fatalf("stats = %+v", stats)
		}

		goCorpus := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, head))
		if err := goCorpus.Inventory(
			context.Background(),
			func(filePath string) bool { return strings.HasSuffix(filePath, ".go") },
		); err != nil {
			t.Fatalf("Go inventory was aborted by safe Thrift alias: %v", err)
		}
		if len(goCorpus.candidates) != 1 {
			t.Fatalf("Go candidates = %v", goCorpus.candidates)
		}
	})

	t.Run("noncandidate unsafe symlink does not abort unrelated domain", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		f.commitFile("svc/main.go", "package svc", "go source")
		f.addSymlink("idl/service.thrift", "/outside/service.thrift")
		f.git(f.source, "commit", "-q", "-m", "absolute thrift alias")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()

		goCorpus := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, head))
		if err := goCorpus.Inventory(
			context.Background(),
			func(filePath string) bool { return strings.HasSuffix(filePath, ".go") },
		); err != nil {
			t.Fatalf("Go inventory was aborted by unrelated Thrift symlink: %v", err)
		}
		if _, ok := goCorpus.candidates["svc/main.go"]; !ok || len(goCorpus.candidates) != 1 {
			t.Fatalf("Go candidates = %v", goCorpus.candidates)
		}

		thriftCorpus := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, head))
		err := thriftCorpus.Inventory(
			context.Background(),
			func(filePath string) bool { return strings.HasSuffix(filePath, ".thrift") },
		)
		if err == nil || !strings.Contains(err.Error(), "absolute target") {
			t.Fatalf("Thrift inventory error = %v", err)
		}
	})

	tests := []struct {
		name    string
		want    string
		prepare func(*corpusGitFixture)
	}{
		{
			name: "repository escape",
			want: "escapes repository root",
			prepare: func(f *corpusGitFixture) {
				f.addSymlink("idl/service.thrift", "../../outside.thrift")
			},
		},
		{
			name: "cycle",
			want: "cycle",
			prepare: func(f *corpusGitFixture) {
				f.addSymlink("service.thrift", "next")
				f.addSymlink("next", "service.thrift")
			},
		},
		{
			name: "missing target",
			want: "is missing",
			prepare: func(f *corpusGitFixture) {
				f.addSymlink("service.thrift", "missing.thrift")
			},
		},
		{
			name: "directory target",
			want: "targets directory",
			prepare: func(f *corpusGitFixture) {
				f.commitFile("defs/real.thrift", "service Real {}", "directory content")
				f.addSymlink("service.thrift", "defs")
			},
		},
		{
			name: "gitlink target",
			want: "targets gitlink",
			prepare: func(f *corpusGitFixture) {
				parent := f.commitFile("README", "root", "root")
				f.git(f.source, "update-index", "--add", "--cacheinfo", "160000,"+parent+",third_party")
				f.addSymlink("service.thrift", "third_party")
			},
		},
		{
			name: "final noncandidate target",
			want: "is not an extractor candidate",
			prepare: func(f *corpusGitFixture) {
				f.commitFile("target.txt", "not thrift", "regular noncandidate")
				f.addSymlink("service.thrift", "target.txt")
			},
		},
		{
			name: "excessive depth",
			want: "depth limit",
			prepare: func(f *corpusGitFixture) {
				f.commitFile("final.thrift", "service Final {}", "final")
				f.addSymlink("service.thrift", "chain01")
				for i := 1; i <= maxCorpusSymlinkDepth; i++ {
					target := "final.thrift"
					if i < maxCorpusSymlinkDepth {
						target = fmt.Sprintf("chain%02d", i+1)
					}
					f.addSymlink(fmt.Sprintf("chain%02d", i), target)
				}
			},
		},
		{
			name: "oversized target",
			want: "target exceeds",
			prepare: func(f *corpusGitFixture) {
				f.stageBlob("120000", "service.thrift", strings.Repeat("x", maxCorpusPathBytes+1))
			},
		},
		{
			name: "unsafe target bytes",
			want: "unsafe target bytes",
			prepare: func(f *corpusGitFixture) {
				f.stageBlob("120000", "service.thrift", `defs\real.thrift`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newCorpusGitFixture(t)
			test.prepare(f)
			f.git(f.source, "commit", "-q", "-m", test.name)
			head := f.git(f.source, "rev-parse", "HEAD")
			f.cloneMirror()
			verified := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, head))
			err := verified.Inventory(
				context.Background(),
				func(filePath string) bool { return strings.HasSuffix(filePath, ".thrift") },
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Inventory error = %v, want %q", err, test.want)
			}
		})
	}

	t.Run("candidate panic on symlink is contained", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		f.commitFile("target.thrift", "service Target {}", "target")
		f.addSymlink("service.thrift", "target.thrift")
		f.git(f.source, "commit", "-q", "-m", "panic alias")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		verified := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, head))
		err := verified.Inventory(context.Background(), func(filePath string) bool {
			if filePath == "service.thrift" {
				panic("boom")
			}
			return strings.HasSuffix(filePath, ".thrift")
		})
		if err == nil || !strings.Contains(err.Error(), "candidate predicate panic") {
			t.Fatalf("Inventory panic error = %v", err)
		}
	})
}

func TestT206SCIPRootCapabilityIsCommitAndPathBound(t *testing.T) {
	f := newCorpusGitFixture(t)
	f.commitFile(scipIndexPath, "old root index", "old root")
	oldCommit := f.commitFile("nested/index.scip", "nested index", "nested")
	newCommit := f.commitFile(scipIndexPath, "new root index", "new root")
	mirror := f.cloneMirror()
	// Mutable replacement refs and a newer repository commit must not redirect
	// the selected capability away from the exact walked root blob.
	f.git(mirror, "replace", oldCommit, newCommit)

	corpus := GitCorpus(f.dataDir).New(f.repoName, oldCommit)
	if err := corpus.WalkFiles(context.Background(), func(string) error { return nil }); err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	indexCorpus, ok := corpus.(sdk.SCIPCorpus)
	if !ok {
		t.Fatal("production corpus does not expose the bounded SCIP capability")
	}
	blob, err := indexCorpus.ReadSCIPIndex(context.Background())
	if err != nil {
		t.Fatalf("ReadSCIPIndex: %v", err)
	}
	sum := sha256.Sum256([]byte("old root index"))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if blob.Content != "old root index" || blob.Digest != wantDigest {
		t.Fatalf("root SCIP blob = content %q digest %q, want old root / %s",
			blob.Content, blob.Digest, wantDigest)
	}
}

func TestGitCorpusRejectsExternalAlternates(t *testing.T) {
	f := newCorpusGitFixture(t)
	head := f.commitFile("api.proto", "content", "content")
	mirror := f.cloneMirror()
	alternates := filepath.Join(mirror, "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte("/external/objects\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := GitCorpus(f.dataDir).New(f.repoName, head).Read(context.Background(), "api.proto")
	if err == nil || !strings.Contains(err.Error(), "external object alternate") {
		t.Fatalf("alternate error = %v", err)
	}
}

func TestGitCorpusRejectsOversizedBlob(t *testing.T) {
	if testing.Short() {
		t.Skip("large boundedness fixture")
	}
	f := newCorpusGitFixture(t)
	head := f.commitFile("large.proto", strings.Repeat("x", int(MaxBlobBytes)+1), "large")
	f.cloneMirror()
	// The bounded runner stops reading at the cap, so memory stays bounded
	// even though there is no separate size pre-check anymore.
	_, err := walkThenRead(t, GitCorpus(f.dataDir).New(f.repoName, head), "large.proto")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized blob error = %v", err)
	}
}

func TestGitCorpusSCIPIndexUsesIndependentRootOnlyLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("large boundedness fixture")
	}
	f := newCorpusGitFixture(t)
	content := strings.Repeat("i", int(MaxBlobBytes)+1)
	head := f.commitFile("index.scip", content, "large committed SCIP index")
	f.cloneMirror()
	corpus := GitCorpus(f.dataDir).New(f.repoName, head)
	if err := corpus.WalkFiles(context.Background(), func(string) error { return nil }); err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	if _, err := corpus.Read(context.Background(), "index.scip"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ordinary source read error = %v, want 10 MiB refusal", err)
	}
	indexCorpus, ok := corpus.(sdk.SCIPCorpus)
	if !ok {
		t.Fatal("production corpus does not expose the bounded SCIP capability")
	}
	blob, err := indexCorpus.ReadSCIPIndex(context.Background())
	if err != nil {
		t.Fatalf("ReadSCIPIndex: %v", err)
	}
	if blob.Content != content {
		t.Fatalf("SCIP blob length = %d, want %d", len(blob.Content), len(content))
	}
}

func TestUnreadableNamesCountedInCoverageButNotEnumerated(t *testing.T) {
	f := newCorpusGitFixture(t)
	f.commitFile("api.proto", `syntax = "proto3";`, "proto")
	oddHead := f.commitFile(`fixtures\case.txt`, "windows-origin fixture", "odd name")
	f.cloneMirror()
	isProto := func(p string) bool { return strings.HasSuffix(p, ".proto") }

	verified := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, oddHead))
	if err := verified.Inventory(context.Background(), isProto); err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if verified.corpusFileCount != 2 {
		t.Fatalf("corpusFileCount = %d, want 2", verified.corpusFileCount)
	}
	if _, err := verified.Read(context.Background(), `fixtures\case.txt`); err == nil {
		t.Fatal("unreadable name was readable")
	}
	if _, err := verified.Read(context.Background(), "api.proto"); err != nil {
		t.Fatalf("candidate read: %v", err)
	}
}

func TestUnreadableCandidateNameFailsClosed(t *testing.T) {
	f := newCorpusGitFixture(t)
	head := f.commitFile(`bad\name.proto`, `syntax = "proto3";`, "bad candidate")
	f.cloneMirror()
	verified := newVerifiedCorpus(GitCorpus(f.dataDir).New(f.repoName, head))
	err := verified.Inventory(context.Background(),
		func(p string) bool { return strings.HasSuffix(p, ".proto") })
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("Inventory error = %v", err)
	}
}

func TestVerifiedCorpusRejectsChangingOrIncorrectDigest(t *testing.T) {
	bad := &changingCorpus{repo: "r", commit: unitCommit}
	verified := newVerifiedCorpus(bad)
	if err := verified.Inventory(context.Background(), func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := verified.Read(context.Background(), "a.proto"); err == nil ||
		!strings.Contains(err.Error(), "invalid trusted digest") {
		t.Fatalf("incorrect digest error = %v", err)
	}
}

func TestT206VerifiedSCIPCapabilityFailsClosed(t *testing.T) {
	t.Run("unselected root is a supported absence", func(t *testing.T) {
		inner := &scipCapabilityCorpus{
			paths: []string{"nested/index.scip"},
			blob:  trustedCorpusBlob("hidden"),
		}
		verified := newVerifiedCorpus(inner)
		if err := verified.Inventory(context.Background(), func(string) bool { return false }); err != nil {
			t.Fatal(err)
		}
		input, err := verified.ReadSCIPIndex(context.Background())
		if err != nil || input.Present || input.Path != scipIndexPath {
			t.Fatalf("unselected typed input = %+v, %v", input, err)
		}
		if inner.scipReads != 0 {
			t.Fatalf("unadmitted SCIP capability performed %d inner reads", inner.scipReads)
		}
	})

	t.Run("trusted digest mismatch", func(t *testing.T) {
		inner := &scipCapabilityCorpus{
			paths: []string{scipIndexPath},
			blob: sdk.Blob{
				Content: "index",
				Digest:  "sha256:" + strings.Repeat("0", 64),
			},
		}
		verified := newVerifiedCorpus(inner)
		if err := verified.Inventory(context.Background(), func(path string) bool {
			return path == scipIndexPath
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := verified.ReadSCIPIndex(context.Background()); err == nil ||
			!strings.Contains(err.Error(), "invalid trusted digest") {
			t.Fatalf("digest mismatch error = %v", err)
		}
	})

	t.Run("dedicated byte ceiling", func(t *testing.T) {
		if testing.Short() {
			t.Skip("large boundedness fixture")
		}
		inner := &scipCapabilityCorpus{
			paths: []string{scipIndexPath},
			blob:  sdk.Blob{Content: strings.Repeat("x", int(MaxSCIPIndexBytes)+1)},
		}
		verified := newVerifiedCorpus(inner)
		if err := verified.Inventory(context.Background(), func(path string) bool {
			return path == scipIndexPath
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := verified.ReadSCIPIndex(context.Background()); err == nil ||
			!strings.Contains(err.Error(), "exceeds byte limit") {
			t.Fatalf("oversized SCIP error = %v", err)
		}
	})
}

func TestCandidateManifestTypedInputIsSeparateAndScoped(t *testing.T) {
	const typedPath = "service/typed/custom.scip"
	blob := trustedCorpusBlob("typed bytes")
	manifest := validMemoryCandidateManifest()
	manifest.records[0].Required = false
	manifest.unitDigest = "sha256:" + strings.Repeat("1", 64)
	manifest.typed = CandidateManifestTypedInput{
		Kind: "scip", Path: typedPath, ObjectID: strings.Repeat("f", 40),
		DeclaredBytes: int64(len(blob.Content)), Present: true,
	}
	manifest.inScope = func(filePath string) bool {
		return filePath == "read.proto" ||
			filePath == "same.proto" ||
			filePath == typedPath
	}
	inner := &scipCapabilityCorpus{
		paths: []string{"read.proto", "same.proto"},
		path:  typedPath,
		blob:  blob,
	}
	verified := newVerifiedCorpus(inner)
	if err := verified.InventoryCandidateManifest(
		context.Background(), manifest, "proto-contract", "1",
		func(string) bool { return false },
	); err != nil {
		t.Fatal(err)
	}
	var walked []string
	if err := verified.WalkFiles(
		context.Background(), func(filePath string) error {
			walked = append(walked, filePath)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(walked, typedPath) {
		t.Fatalf("typed input leaked into ordinary replay: %v", walked)
	}
	input, err := verified.ReadSCIPIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !input.Present || input.Path != typedPath || input.Content != blob.Content {
		t.Fatalf("typed input = %+v", input)
	}
	if !verified.SCIPDocumentInScope("read.proto") ||
		verified.SCIPDocumentInScope("outside/secret.go") {
		t.Fatal("SCIP document scope did not follow the committed unit")
	}
	stats, err := verified.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.scopePosture != "focused-local" ||
		stats.manifestDigest != manifest.identity ||
		stats.typedInputPath != typedPath ||
		stats.typedInputObjectID != strings.Repeat("f", 40) ||
		stats.typedInputDigest != blob.Digest ||
		!stats.typedInputPresent {
		t.Fatalf("typed scope stats = %+v", stats)
	}
}

func TestCoverageManifestPreservesApplicableAbsentTypedInput(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	coverage, err := coverageManifest(
		sdk.Coverage{},
		0,
		0,
		0,
		corpusStats{
			sourceScopeDigest: digest,
			scopePosture:      "focused-local",
			manifestDigest:    digest,
			unitDigest:        digest,
			plane:             candidate.PlaneLocal,
			scopeCorpusDigest: digest,
			plannedDigest:     digest,
			typedInputKind:    analysisunit.TypedIndexKindSCIP,
		},
		emptyGitlinkInventory(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.TypedInputKind != analysisunit.TypedIndexKindSCIP ||
		coverage.TypedInputPath != "" ||
		coverage.TypedInputPresent ||
		coverage.TypedInputObjectID != "" ||
		coverage.TypedInputDeclaredBytes != 0 ||
		coverage.TypedInputDigest != "" {
		t.Fatalf(
			"applicable absent typed-input coverage = %+v",
			coverage,
		)
	}
}

func TestScopedCandidateManifestBlocksOutOfUnitExactProbes(t *testing.T) {
	manifest := validMemoryCandidateManifest()
	manifest.records[0].Required = false
	manifest.unitDigest = "sha256:" + strings.Repeat("2", 64)
	manifest.inScope = func(filePath string) bool {
		return strings.HasPrefix(filePath, "service/")
	}
	inner := &countingExactCorpus{
		scipCapabilityCorpus: &scipCapabilityCorpus{},
	}
	verified := newVerifiedCorpus(inner)
	if err := verified.InventoryCandidateManifest(
		context.Background(), manifest, "proto-contract", "1",
		func(string) bool { return false },
	); err != nil {
		t.Fatal(err)
	}
	present, err := verified.containsRegular(
		context.Background(), "outside/secret.go",
	)
	if err != nil || present {
		t.Fatalf("out-of-unit exact lookup = %t, %v", present, err)
	}
	under, err := verified.anyRegularUnder(
		context.Background(), "outside",
	)
	if err != nil || under {
		t.Fatalf("out-of-unit root lookup = %t, %v", under, err)
	}
	if inner.lookups != 0 || inner.rootLookups != 0 {
		t.Fatalf(
			"out-of-unit probes reached exact corpus: %d/%d",
			inner.lookups, inner.rootLookups,
		)
	}
	present, err = verified.containsRegular(
		context.Background(), "service/generated.go",
	)
	if err != nil || !present || inner.lookups != 1 {
		t.Fatalf("in-unit exact lookup = %t, %v, calls=%d", present, err, inner.lookups)
	}
}

func TestVerifiedCorpusBoundsAggregateInventoryPathBytes(t *testing.T) {
	verified := newVerifiedCorpus(pathFloodCorpus{})
	err := verified.Inventory(context.Background(), func(string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "aggregate path limit") {
		t.Fatalf("Inventory error = %v", err)
	}
}

func TestVerifiedCorpusBoundsUnreadableAggregateInventoryPathBytes(t *testing.T) {
	verified := newVerifiedCorpus(unreadablePathFloodCorpus{})
	err := verified.Inventory(context.Background(), func(string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "aggregate path limit") {
		t.Fatalf("Inventory error = %v", err)
	}
}

func TestSparseLineIndexMatchesFullSourceCount(t *testing.T) {
	content := strings.Repeat("abc\n", 3_000) + "tail"
	index := buildLineIndex(content)
	for _, offset := range []int{0, 1, lineIndexBlock - 1, lineIndexBlock, lineIndexBlock + 1, len(content)} {
		want := 1 + strings.Count(content[:offset], "\n")
		if got := sourceLine(content, index, offset); got != want {
			t.Fatalf("sourceLine(offset=%d) = %d, want %d", offset, got, want)
		}
	}
}

type changingCorpus struct{ repo, commit string }

func (c *changingCorpus) RepoName() string { return c.repo }
func (c *changingCorpus) Commit() string   { return c.commit }
func (*changingCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	return visit("a.proto")
}
func (*changingCorpus) Read(context.Context, string) (sdk.Blob, error) {
	return sdk.Blob{Content: "content", Digest: "sha256:" + strings.Repeat("0", 64)}, nil
}

type scipCapabilityCorpus struct {
	paths     []string
	blob      sdk.Blob
	path      string
	scipReads int
}

type countingExactCorpus struct {
	*scipCapabilityCorpus
	lookups     int
	rootLookups int
}

func (c *countingExactCorpus) lookupRegular(
	context.Context,
	string,
) (treeRecord, bool, error) {
	c.lookups++
	return treeRecord{
		mode: "100644", objectType: "blob", oid: strings.Repeat("a", 40),
		size: 1, path: "service/generated.go",
	}, true, nil
}

func (c *countingExactCorpus) anyRegularUnder(
	context.Context,
	string,
) (bool, error) {
	c.rootLookups++
	return true, nil
}

func (c *countingExactCorpus) readManifestBlob(
	context.Context,
	treeRecord,
	int64,
) (sdk.Blob, error) {
	return sdk.Blob{}, errors.New("unexpected manifest blob read")
}

func (*scipCapabilityCorpus) RepoName() string { return "synthetic.invalid/t206" }
func (*scipCapabilityCorpus) Commit() string   { return unitCommit }
func (c *scipCapabilityCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	for _, filePath := range c.paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(filePath); err != nil {
			return err
		}
	}
	return nil
}
func (*scipCapabilityCorpus) Read(context.Context, string) (sdk.Blob, error) {
	return sdk.Blob{}, store.ErrNotFound
}
func (c *scipCapabilityCorpus) ReadSCIPIndex(context.Context) (sdk.SCIPInput, error) {
	c.scipReads++
	typedPath := c.path
	if typedPath == "" {
		typedPath = scipIndexPath
	}
	return sdk.SCIPInput{
		Path: typedPath, Blob: c.blob, Present: true,
	}, nil
}

func trustedCorpusBlob(content string) sdk.Blob {
	sum := sha256.Sum256([]byte(content))
	return sdk.Blob{
		Content: content,
		Digest:  "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type pathFloodCorpus struct{}

func (pathFloodCorpus) RepoName() string { return "r" }
func (pathFloodCorpus) Commit() string   { return unitCommit }
func (pathFloodCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	prefix := strings.Repeat("p", maxCorpusPathBytes-8)
	for i := 0; ; i++ {
		if err := visit(prefix + fmt.Sprintf("%08d", i)); err != nil {
			return err
		}
	}
}
func (pathFloodCorpus) Read(context.Context, string) (sdk.Blob, error) {
	return sdk.Blob{}, errors.New("unexpected read")
}

type unreadablePathFloodCorpus struct{}

func (unreadablePathFloodCorpus) RepoName() string { return "r" }
func (unreadablePathFloodCorpus) Commit() string   { return unitCommit }
func (unreadablePathFloodCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	// Each path is individually within the Git corpus record limit but contains
	// a forbidden backslash. Stop just beyond the aggregate threshold so a
	// missing unreadable-path check fails quickly instead of reaching the much
	// larger file-count limit.
	prefix := `\` + strings.Repeat("p", maxCorpusPathBytes-9)
	for i := 0; i < maxCorpusInventoryPathBytes/maxCorpusPathBytes+2; i++ {
		if err := visit(prefix + fmt.Sprintf("%08d", i)); err != nil {
			return err
		}
	}
	return errors.New("unreadable path flood escaped aggregate limit")
}
func (unreadablePathFloodCorpus) Read(context.Context, string) (sdk.Blob, error) {
	return sdk.Blob{}, errors.New("unexpected read")
}
