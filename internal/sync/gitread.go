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
	if len(s) > 1024 || !refRE.MatchString(s) || strings.Contains(s, "..") {
		return fmt.Errorf("ref %q: %w", s, ErrBadInput)
	}
	return nil
}

func checkPath(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > 4096 || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.Contains(s, "..") {
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
		return nil, gitCommandError(ctx, err, args, stderr.String())
	}
	return stdout.Bytes(), nil
}

func gitCommandError(ctx context.Context, runErr error, args []string, stderr string) error {
	// exec.CommandContext commonly reports "signal: killed" rather than
	// wrapping the context error. Preserve cancellation/deadline semantics
	// for callers and HTTP request teardown.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	werr := fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), runErr, stderr)
	for _, marker := range []string{
		"does not exist", "invalid object name", "Not a valid object name",
		"not a valid object", "bad revision", "bad object", "not a tree object",
		"bad file", "ambiguous argument", "unknown revision", "Needed a single revision",
		"no such path", "no such file",
	} {
		if strings.Contains(stderr, marker) {
			return fmt.Errorf("%w: %w", store.ErrNotFound, werr)
		}
	}
	return werr
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
	dir, dirErr := SafeRepoDir(dataDir, repoName)
	if err := errors.Join(dirErr, checkRef(ref), checkPath(path)); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("empty path: %w", ErrBadInput)
	}
	// Resolve the mutable tree-ish once. Sizing and reading the immutable blob
	// OID prevents a concurrent fetch from moving HEAD between the two calls.
	oidOut, err := runGitRaw(ctx, dir, "rev-parse", "--verify", spec(ref, path))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrNotFound, err)
	}
	oid := strings.TrimSpace(string(oidOut))
	typeOut, err := runGitRaw(ctx, dir, "cat-file", "-t", oid)
	if err != nil || strings.TrimSpace(string(typeOut)) != "blob" {
		return nil, fmt.Errorf("%s is not a blob: %w", path, store.ErrNotFound)
	}
	sizeOut, err := runGitRaw(ctx, dir, "cat-file", "-s", oid)
	if err != nil {
		return nil, err
	}
	if n, perr := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64); perr == nil && n > MaxBlobBytes {
		return nil, fmt.Errorf("%s is %d bytes (limit %d): %w", path, n, MaxBlobBytes, ErrTooLarge)
	}
	return runGitRaw(ctx, dir, "cat-file", "blob", oid)
}

type TreeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // file | dir | symlink | submodule
	Size int64  `json:"size,omitempty"`
}

// FolderContents lists one directory level at ref.
func FolderContents(ctx context.Context, dataDir, repoName, ref, path string) ([]TreeEntry, error) {
	dir, dirErr := SafeRepoDir(dataDir, repoName)
	if err := errors.Join(dirErr, checkRef(ref), checkPath(path)); err != nil {
		return nil, err
	}
	out, err := runGitRaw(ctx, dir, "ls-tree", "-l", spec(ref, path))
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
	dir, dirErr := SafeRepoDir(dataDir, repoName)
	if err := errors.Join(dirErr, checkRef(ref)); err != nil {
		return nil, err
	}
	out, err := runGitRaw(ctx, dir, "ls-tree", "-r", "--name-only", spec(ref, ""))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return []string{}, nil
	}
	return strings.Split(trimmed, "\n"), nil
}
