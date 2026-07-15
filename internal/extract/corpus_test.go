package extract

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

func TestReadNULRecordRejectsTruncatedRecord(t *testing.T) {
	_, err := readNULRecord(bufio.NewReader(strings.NewReader("unterminated")), 64)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readNULRecord error = %v", err)
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

	t.Run("proto symlink rejected", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		f.commitFile("target", "not parsed", "target")
		if err := os.Symlink("target", filepath.Join(f.source, "linked.proto")); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		f.git(f.source, "add", "linked.proto")
		f.git(f.source, "commit", "-q", "-m", "proto symlink")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		err := GitCorpus(f.dataDir).New(f.repoName, head).WalkFiles(context.Background(), func(string) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "proto symlink") {
			t.Fatalf("proto symlink error = %v", err)
		}
	})

	t.Run("gitlink rejected", func(t *testing.T) {
		f := newCorpusGitFixture(t)
		parent := f.commitFile("README", "root", "root")
		f.git(f.source, "update-index", "--add", "--cacheinfo", "160000,"+parent+",third_party")
		f.git(f.source, "commit", "-q", "-m", "gitlink")
		head := f.git(f.source, "rev-parse", "HEAD")
		f.cloneMirror()
		err := GitCorpus(f.dataDir).New(f.repoName, head).WalkFiles(context.Background(), func(string) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "gitlink") {
			t.Fatalf("gitlink error = %v", err)
		}
	})
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
