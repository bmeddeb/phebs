package sync_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

type historyFixture struct {
	dataDir string
	repo    string
	root    string
	edit    string
	rename  string
	binary  string
	head    string
}

func newHistoryFixture(t *testing.T) historyFixture {
	t.Helper()
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")

	writeFixtureFile(t, origin, "old/name.txt", []byte("alpha\ncommon\nomega\n"))
	writeFixtureFile(t, origin, "asset.bin", []byte{0x00, 0x01, 0x02})
	writeFixtureFile(t, origin, "latin1.txt", []byte{'c', 'a', 'f', 0xe9, '\n'})
	writeFixtureFile(t, origin, "empty.txt", nil)
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "initial files")
	root := gitc(t, origin, "rev-parse", "HEAD")

	writeFixtureFile(t, origin, "old/name.txt", []byte("alpha\nchanged\nomega\n"))
	gitc(t, origin, "add", "old/name.txt")
	gitc(t, origin, "commit", "-m", "change middle line", "-m", "Detailed body.")
	edit := gitc(t, origin, "rev-parse", "HEAD")

	if err := os.MkdirAll(filepath.Join(origin, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "mv", "old/name.txt", "src/new name.txt")
	writeFixtureFile(t, origin, "src/new name.txt", []byte("alpha\nchanged\nomega\nadded\n"))
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "rename source file")
	rename := gitc(t, origin, "rev-parse", "HEAD")

	writeFixtureFile(t, origin, "asset.bin", []byte{0x00, 0xff, 0x10, 0x80, 0x01})
	gitc(t, origin, "add", "asset.bin")
	gitc(t, origin, "commit", "-m", "update binary asset")
	binary := gitc(t, origin, "rev-parse", "HEAD")

	dataDir := t.TempDir()
	const repo = "example.com/history-fixture"
	if err := sync.Mirror(context.Background(), "file://"+origin, sync.RepoDir(dataDir, repo)); err != nil {
		t.Fatal(err)
	}
	return historyFixture{
		dataDir: dataDir,
		repo:    repo,
		root:    root,
		edit:    edit,
		rename:  rename,
		binary:  binary,
		head:    binary,
	}
}

func writeFixtureFile(t *testing.T, root, path string, content []byte) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryBlameMapsLinesAcrossRename(t *testing.T) {
	fx := newHistoryFixture(t)
	svc := sync.NewHistoryService(fx.dataDir)
	got, err := svc.Blame(context.Background(), sync.BlameRequest{
		Repo: fx.repo, Ref: fx.head, Path: "src/new name.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != fx.head || got.Path != "src/new name.txt" || got.Truncated {
		t.Fatalf("unexpected blame envelope: %+v", got)
	}
	if len(got.Lines) != 4 {
		t.Fatalf("blame lines = %d, want 4", len(got.Lines))
	}
	want := []struct {
		content, commit, path string
		line, original        int
	}{
		{"alpha", fx.root, "old/name.txt", 1, 1},
		{"changed", fx.edit, "old/name.txt", 2, 2},
		{"omega", fx.root, "old/name.txt", 3, 3},
		{"added", fx.rename, "src/new name.txt", 4, 4},
	}
	for i, expected := range want {
		line := got.Lines[i]
		if line.Content != expected.content || line.CommitID != expected.commit ||
			line.OriginalPath != expected.path || line.Line != expected.line ||
			line.OriginalLine != expected.original {
			t.Errorf("line %d = %+v, want %+v", i, line, expected)
		}
	}
	commitIDs := map[string]bool{}
	for _, commit := range got.Commits {
		commitIDs[commit.ID] = true
	}
	for _, id := range []string{fx.root, fx.edit, fx.rename} {
		if !commitIDs[id] {
			t.Errorf("blame commit metadata missing %s", id)
		}
	}
}

func TestHistoryBlameBoundsEmptyAndBinary(t *testing.T) {
	fx := newHistoryFixture(t)
	svc := sync.NewHistoryService(fx.dataDir)
	svc.MaxBlameLines = 2
	got, err := svc.Blame(context.Background(), sync.BlameRequest{
		Repo: fx.repo, Ref: fx.head, Path: "src/new name.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 2 || !got.Truncated {
		t.Fatalf("bounded blame = %d lines, truncated=%v", len(got.Lines), got.Truncated)
	}

	empty, err := svc.Blame(context.Background(), sync.BlameRequest{
		Repo: fx.repo, Ref: fx.head, Path: "empty.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Lines) != 0 || len(empty.Commits) != 0 {
		t.Fatalf("empty blame = %+v", empty)
	}

	if _, err := svc.Blame(context.Background(), sync.BlameRequest{
		Repo: fx.repo, Ref: fx.head, Path: "asset.bin",
	}); !errors.Is(err, sync.ErrBinaryFile) {
		t.Fatalf("binary blame err = %v, want ErrBinaryFile", err)
	}

	latin1, err := svc.Blame(context.Background(), sync.BlameRequest{
		Repo: fx.repo, Ref: fx.head, Path: "latin1.txt",
	})
	if err != nil {
		t.Fatalf("non-UTF-8 text blame: %v", err)
	}
	if len(latin1.Lines) != 1 || latin1.Lines[0].Content != "caf?" {
		t.Fatalf("non-UTF-8 text blame = %+v", latin1.Lines)
	}

	workBounded := sync.NewHistoryService(fx.dataDir)
	workBounded.MaxBlameBlobBytes = 8
	if _, err := workBounded.Blame(context.Background(), sync.BlameRequest{
		Repo: fx.repo, Ref: fx.head, Path: "src/new name.txt",
	}); !errors.Is(err, sync.ErrTooLarge) {
		t.Fatalf("oversized blame blob err = %v, want ErrTooLarge", err)
	}
}

func TestHistoryCommitsFollowRenameAndPaginate(t *testing.T) {
	fx := newHistoryFixture(t)
	svc := sync.NewHistoryService(fx.dataDir)
	page, err := svc.Commits(context.Background(), sync.CommitListRequest{
		Repo: fx.repo, Ref: fx.head, Path: "src/new name.txt", Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Revision != fx.head || len(page.Commits) != 2 || !page.HasMore {
		t.Fatalf("first page = %+v", page)
	}
	if page.Commits[0].ID != fx.rename || page.Commits[1].ID != fx.edit {
		t.Errorf("first page ids = %s, %s", page.Commits[0].ID, page.Commits[1].ID)
	}
	if page.Commits[1].Message != "change middle line\n\nDetailed body." {
		t.Errorf("commit message = %q", page.Commits[1].Message)
	}

	next, err := svc.Commits(context.Background(), sync.CommitListRequest{
		Repo: fx.repo, Ref: fx.head, Path: "src/new name.txt", Limit: 2, Offset: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Commits) != 1 || next.Commits[0].ID != fx.root || next.HasMore || next.Offset != 2 {
		t.Fatalf("second page = %+v", next)
	}
}

func TestHistoryCommitIncludesParentsAndRename(t *testing.T) {
	fx := newHistoryFixture(t)
	svc := sync.NewHistoryService(fx.dataDir)
	got, err := svc.Commit(context.Background(), sync.CommitRequest{Repo: fx.repo, Ref: fx.rename})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != fx.rename || got.Commit.ID != fx.rename || got.Commit.Subject != "rename source file" {
		t.Fatalf("commit envelope = %+v", got)
	}
	if len(got.Commit.ParentIDs) != 1 || got.Commit.ParentIDs[0] != fx.edit {
		t.Fatalf("parents = %v, want [%s]", got.Commit.ParentIDs, fx.edit)
	}
	if len(got.Changes) != 1 {
		t.Fatalf("changes = %+v", got.Changes)
	}
	change := got.Changes[0]
	if change.Status != "renamed" || change.OldPath != "old/name.txt" || change.Path != "src/new name.txt" {
		t.Fatalf("rename change = %+v", change)
	}
	if change.Additions != 1 || change.Deletions != 0 || change.Binary {
		t.Errorf("rename stats = %+v", change)
	}

	root, err := svc.Commit(context.Background(), sync.CommitRequest{Repo: fx.repo, Ref: fx.root})
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Commit.ParentIDs) != 0 {
		t.Errorf("root parents = %v", root.Commit.ParentIDs)
	}
	if len(root.Changes) != 4 {
		t.Errorf("root changes = %+v, want four added files", root.Changes)
	}
}

func TestHistoryDiffTextBinaryPathAndTruncation(t *testing.T) {
	fx := newHistoryFixture(t)
	svc := sync.NewHistoryService(fx.dataDir)
	textDiff, err := svc.Diff(context.Background(), sync.DiffRequest{
		Repo: fx.repo, Head: fx.rename,
	})
	if err != nil {
		t.Fatal(err)
	}
	if textDiff.Base != fx.edit || textDiff.Head != fx.rename || textDiff.Truncated {
		t.Fatalf("diff envelope = %+v", textDiff)
	}
	if !strings.Contains(textDiff.Patch, "rename from old/name.txt") ||
		!strings.Contains(textDiff.Patch, "+added") {
		t.Errorf("rename patch missing expected content:\n%s", textDiff.Patch)
	}
	if len(textDiff.Files) != 1 || textDiff.Files[0].Status != "renamed" {
		t.Errorf("rename files = %+v", textDiff.Files)
	}

	binaryDiff, err := svc.Diff(context.Background(), sync.DiffRequest{
		Repo: fx.repo, Head: fx.binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(binaryDiff.Files) != 1 || !binaryDiff.Files[0].Binary {
		t.Fatalf("binary files = %+v", binaryDiff.Files)
	}
	if strings.IndexByte(binaryDiff.Patch, 0) >= 0 || !strings.Contains(binaryDiff.Patch, "Binary files") {
		t.Errorf("binary patch was not safely summarized: %q", binaryDiff.Patch)
	}

	filtered, err := svc.Diff(context.Background(), sync.DiffRequest{
		Repo: fx.repo, Base: fx.root, Head: fx.edit, Path: "old/name.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Files) != 1 || filtered.Files[0].Path != "old/name.txt" ||
		!strings.Contains(filtered.Patch, "+changed") {
		t.Errorf("filtered diff = %+v\n%s", filtered.Files, filtered.Patch)
	}

	zeroContext, err := svc.Diff(context.Background(), sync.DiffRequest{
		Repo: fx.repo, Base: fx.root, Head: fx.edit, Path: "old/name.txt",
		ContextLines: 0, ContextLinesSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(zeroContext.Patch, "-common\n+changed") ||
		strings.Contains(zeroContext.Patch, "\n alpha\n") || strings.Contains(zeroContext.Patch, "\n omega\n") {
		t.Errorf("zero-context diff contains surrounding lines:\n%s", zeroContext.Patch)
	}

	bounded := sync.NewHistoryService(fx.dataDir)
	bounded.MaxDiffBytes = 80
	truncated, err := bounded.Diff(context.Background(), sync.DiffRequest{
		Repo: fx.repo, Head: fx.edit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated.Truncated || len(truncated.Patch) > 80 {
		t.Errorf("bounded diff len=%d truncated=%v", len(truncated.Patch), truncated.Truncated)
	}
}

func TestHistoryRejectsUnsafeAndMissingInputs(t *testing.T) {
	fx := newHistoryFixture(t)
	svc := sync.NewHistoryService(fx.dataDir)
	ctx := context.Background()

	if _, err := svc.Blame(ctx, sync.BlameRequest{Repo: fx.repo, Path: "../../etc/passwd"}); !errors.Is(err, sync.ErrBadInput) {
		t.Errorf("traversal blame err = %v", err)
	}
	if _, err := svc.Commits(ctx, sync.CommitListRequest{Repo: fx.repo, Path: "--all"}); !errors.Is(err, sync.ErrBadInput) {
		t.Errorf("flag path commits err = %v", err)
	}
	if _, err := svc.Commit(ctx, sync.CommitRequest{Repo: fx.repo, Ref: "--all"}); !errors.Is(err, sync.ErrBadInput) {
		t.Errorf("flag ref commit err = %v", err)
	}
	if _, err := svc.Diff(ctx, sync.DiffRequest{Repo: fx.repo, Head: fx.head, Path: "/etc/passwd"}); !errors.Is(err, sync.ErrBadInput) {
		t.Errorf("absolute diff path err = %v", err)
	}
	if _, err := svc.Commit(ctx, sync.CommitRequest{Repo: "../outside"}); !errors.Is(err, sync.ErrBadInput) {
		t.Errorf("repository traversal err = %v", err)
	}
	if _, err := svc.Commit(ctx, sync.CommitRequest{Repo: fx.repo, Ref: "deadbeef"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing commit err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Blame(ctx, sync.BlameRequest{Repo: fx.repo, Ref: fx.head, Path: "missing.txt"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing blame path err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Commits(ctx, sync.CommitListRequest{Repo: fx.repo, Limit: -1}); !errors.Is(err, sync.ErrBadInput) {
		t.Errorf("negative limit err = %v", err)
	}
	if _, err := svc.Diff(ctx, sync.DiffRequest{Repo: fx.repo, ContextLines: 100}); !errors.Is(err, sync.ErrBadInput) {
		t.Errorf("context bound err = %v", err)
	}

	// Git pathspec magic and globs are treated as literal repository paths,
	// never as a way to widen a supposedly path-scoped history request.
	magic, err := svc.Commits(ctx, sync.CommitListRequest{Repo: fx.repo, Path: ":(top)**"})
	if err != nil {
		t.Fatal(err)
	}
	if len(magic.Commits) != 0 {
		t.Errorf("pathspec magic widened commit history: %+v", magic.Commits)
	}
	glob, err := svc.Diff(ctx, sync.DiffRequest{Repo: fx.repo, Head: fx.edit, Path: "*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(glob.Files) != 0 || glob.Patch != "" {
		t.Errorf("path glob widened diff: files=%+v patch=%q", glob.Files, glob.Patch)
	}
}

func TestHistoryPreservesCancellationAndMetadataBound(t *testing.T) {
	fx := newHistoryFixture(t)
	svc := sync.NewHistoryService(fx.dataDir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	checks := []struct {
		name string
		run  func() error
	}{
		{"blame", func() error {
			_, err := svc.Blame(ctx, sync.BlameRequest{Repo: fx.repo, Path: "src/new name.txt"})
			return err
		}},
		{"commits", func() error {
			_, err := svc.Commits(ctx, sync.CommitListRequest{Repo: fx.repo})
			return err
		}},
		{"commit", func() error {
			_, err := svc.Commit(ctx, sync.CommitRequest{Repo: fx.repo})
			return err
		}},
		{"diff", func() error {
			_, err := svc.Diff(ctx, sync.DiffRequest{Repo: fx.repo})
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}

	bounded := sync.NewHistoryService(fx.dataDir)
	bounded.MaxHistoryOutputBytes = 32
	if _, err := bounded.Commits(context.Background(), sync.CommitListRequest{Repo: fx.repo}); !errors.Is(err, sync.ErrTooLarge) {
		t.Fatalf("metadata bound err = %v, want ErrTooLarge", err)
	}
}
