package sync

// Read-side git plumbing (T4.4): file and tree serving straight from bare
// mirrors — cat-file/ls-tree resolve within the object store, never the
// filesystem (PLAN §1: no zoekt content tricks).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/store"
)

var ErrBadInput = errors.New("invalid ref or path")

// checkRef/checkPath guard against flag injection (leading "-"), traversal
// ("..", leading "/"), and control bytes. cat-file/ls-tree read the object
// database, so ".." cannot escape the repo anyway — this is defense in depth
// plus clean 400s.

var refRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`) // no spaces: never valid in a ref

func checkRef(s string) error {
	if s == "" {
		return nil
	}
	if !refRE.MatchString(s) || strings.Contains(s, "..") {
		return fmt.Errorf("ref %q: %w", s, ErrBadInput)
	}
	return nil
}

func checkPath(s string) error {
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.Contains(s, "..") {
		return fmt.Errorf("path %q: %w", s, ErrBadInput)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("path %q: %w", s, ErrBadInput)
		}
	}
	return nil
}

func runGitRaw(ctx context.Context, dir string, args ...string) ([]byte, error) {
	// core.quotePath=false: emit UTF-8 paths verbatim instead of C-quoting
	// (octal-escaping, double-wrapping) non-ASCII names, which would make
	// ls-tree output unparseable and the resulting names unopenable.
	full := append([]string{"-c", "core.quotePath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		werr := fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, msg)
		for _, marker := range []string{"does not exist", "invalid object name", "Not a valid object name", "not a valid object", "bad revision", "not a tree object", "bad file"} {
			if strings.Contains(msg, marker) {
				return nil, fmt.Errorf("%w: %w", store.ErrNotFound, werr)
			}
		}
		return nil, werr
	}
	return stdout.Bytes(), nil
}

// spec builds the tree-ish for ref (default HEAD) and an optional path.
func spec(ref, path string) string {
	if ref == "" {
		ref = "HEAD"
	}
	if path == "" {
		return ref
	}
	return ref + ":" + path
}

// MaxBlobBytes caps a single /api/source read. cat-file buffers the whole
// blob, then the API copies it to a string (or a 4/3 base64 string) and JSON-
// encodes it — several × the blob in transient memory, in the process that
// also serves search. A multi-GB blob would OOM the binary. Var so tests can
// lower it without materializing a huge fixture.
var MaxBlobBytes int64 = 10 << 20 // 10 MiB

// ErrTooLarge is returned when a blob exceeds MaxBlobBytes.
var ErrTooLarge = errors.New("file too large")

// CatFile returns the exact blob bytes of path at ref, refusing blobs over
// MaxBlobBytes (checked cheaply via cat-file -s before reading the content).
func CatFile(ctx context.Context, dataDir, repoName, ref, path string) ([]byte, error) {
	if err := errors.Join(checkRef(ref), checkPath(path)); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("empty path: %w", ErrBadInput)
	}
	dir := RepoDir(dataDir, repoName)
	sizeOut, err := runGitRaw(ctx, dir, "cat-file", "-s", spec(ref, path))
	if err != nil {
		return nil, err
	}
	if n, perr := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64); perr == nil && n > MaxBlobBytes {
		return nil, fmt.Errorf("%s is %d bytes (limit %d): %w", path, n, MaxBlobBytes, ErrTooLarge)
	}
	return runGitRaw(ctx, dir, "cat-file", "blob", spec(ref, path))
}

type TreeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // file | dir | symlink | submodule
	Size int64  `json:"size,omitempty"`
}

// FolderContents lists one directory level at ref.
func FolderContents(ctx context.Context, dataDir, repoName, ref, path string) ([]TreeEntry, error) {
	if err := errors.Join(checkRef(ref), checkPath(path)); err != nil {
		return nil, err
	}
	out, err := runGitRaw(ctx, RepoDir(dataDir, repoName), "ls-tree", "-l", spec(ref, path))
	if err != nil {
		return nil, err
	}
	entries := []TreeEntry{}
	for line := range strings.Lines(strings.TrimSpace(string(out))) {
		line = strings.TrimRight(line, "\n")
		if line == "" {
			continue
		}
		meta, name, ok := strings.Cut(line, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			return nil, fmt.Errorf("unexpected ls-tree line %q", line)
		}
		e := TreeEntry{Name: name}
		switch {
		case f[1] == "tree":
			e.Type = "dir"
		case f[1] == "commit":
			e.Type = "submodule"
		case f[0] == "120000":
			e.Type = "symlink"
		default:
			e.Type = "file"
		}
		if f[3] != "-" {
			e.Size, _ = strconv.ParseInt(f[3], 10, 64)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// TreePaths returns every file path at ref, recursively — the file-finder
// feed. ponytail: uncapped; revisit when a monorepo makes this a problem.
func TreePaths(ctx context.Context, dataDir, repoName, ref string) ([]string, error) {
	if err := checkRef(ref); err != nil {
		return nil, err
	}
	out, err := runGitRaw(ctx, RepoDir(dataDir, repoName), "ls-tree", "-r", "--name-only", spec(ref, ""))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return []string{}, nil
	}
	return strings.Split(trimmed, "\n"), nil
}
