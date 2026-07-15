package gitobj

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func fixtureRepo(t *testing.T) (dir, blobOID, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello object"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-m", "one")
	commit = git("rev-parse", "HEAD")
	blobOID = git("rev-parse", "HEAD:a.txt")
	return dir, blobOID, commit
}

func TestCommandScrubsInheritedGitEnvironment(t *testing.T) {
	for key, value := range map[string]string{
		"GIT_DIR": "/attacker", "GIT_WORK_TREE": "/attacker/work",
		"GIT_OBJECT_DIRECTORY": "/attacker/objects", "GIT_ALTERNATE_OBJECT_DIRECTORIES": "/attacker/alt",
		"GIT_NO_LAZY_FETCH": "0", "GIT_TERMINAL_PROMPT": "1", "GIT_OPTIONAL_LOCKS": "1",
	} {
		t.Setenv(key, value)
	}
	cmd := Command(context.Background(), "/trusted/repo.git", "version")
	values := map[string][]string{}
	for _, entry := range cmd.Env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") {
			values[key] = append(values[key], value)
		}
	}
	want := map[string]string{
		"GIT_NO_LAZY_FETCH": "1", "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0",
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_ATTR_NOSYSTEM": "1",
	}
	if len(values) != len(want) {
		t.Fatalf("Git environment keys = %v, want only %v", values, want)
	}
	for key, expected := range want {
		if got := values[key]; len(got) != 1 || got[0] != expected {
			t.Fatalf("%s = %v, want [%q]", key, got, expected)
		}
	}
	if !slices.Contains(cmd.Args, "--no-replace-objects") {
		t.Fatalf("git args omit --no-replace-objects: %v", cmd.Args)
	}
}

func TestIsObjectID(t *testing.T) {
	for value, want := range map[string]bool{
		strings.Repeat("a", 40): true,
		strings.Repeat("0", 64): true,
		strings.Repeat("A", 40): false,
		strings.Repeat("a", 39): false,
		strings.Repeat("g", 40): false,
		"":                      false,
	} {
		if got := IsObjectID(value); got != want {
			t.Errorf("IsObjectID(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestWrapErrorClassification(t *testing.T) {
	base := errors.New("exit status 128")
	for stderr, wantNotFound := range map[string]bool{
		"fatal: path 'x' does not exist in 'HEAD'":      true,
		"fatal: x exists on disk, but not in 'HEAD'":    true,
		"fatal: Not a valid object name deadbeef":       true,
		"fatal: bad object deadbeef":                    true,
		"fatal: ambiguous argument 'x'":                 true,
		"fatal: Needed a single revision":               true,
		"fatal: unable to read tree; disk exploded":     false,
		"error: object file is empty (corrupt objects)": false,
	} {
		err := WrapError(context.Background(), []string{"cat-file"}, base, stderr)
		if got := errors.Is(err, store.ErrNotFound); got != wantNotFound {
			t.Errorf("WrapError(%q) NotFound = %v, want %v", stderr, got, wantNotFound)
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WrapError(canceled, []string{"x"}, base, "bad object"); !errors.Is(err, context.Canceled) {
		t.Fatalf("context preference lost: %v", err)
	}
}

func TestResolveAndReadBlob(t *testing.T) {
	dir, blobOID, commit := fixtureRepo(t)
	ctx := context.Background()

	oid, size, err := ResolveBlob(ctx, dir, commit+":a.txt")
	if err != nil || oid != blobOID || size != int64(len("hello object")) {
		t.Fatalf("ResolveBlob = %q, %d, %v", oid, size, err)
	}
	if _, _, err := ResolveBlob(ctx, dir, commit+":missing.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing path error = %v, want ErrNotFound", err)
	}
	// A tree object is not a blob and must classify as not found.
	if _, _, err := ResolveBlob(ctx, dir, commit+"^{tree}"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tree object error = %v, want ErrNotFound", err)
	}

	data, err := ReadBlob(ctx, dir, blobOID, 1<<20)
	if err != nil || string(data) != "hello object" {
		t.Fatalf("ReadBlob = %q, %v", data, err)
	}
	if _, err := ReadBlob(ctx, dir, blobOID, 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bounded read error = %v, want ErrTooLarge", err)
	}
	if _, err := ReadBlob(ctx, dir, "not-an-oid", 4); err == nil {
		t.Fatal("invalid oid accepted")
	}
}
