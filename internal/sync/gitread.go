package sync

// Read-side git plumbing (T4.4): file and tree serving straight from bare
// mirrors — cat-file/ls-tree resolve within the object store, never the
// filesystem (PLAN §1: no zoekt content tricks).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/gitobj"
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

// runGitRaw runs a read-only git command through the shared hardened builder
// (gitobj.Command: scrubbed env, no replace refs, verbatim UTF-8 paths) with
// unbounded stdout — used for listings and history streams only; blob reads
// go through the bounded gitobj primitives.
func runGitRaw(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := gitobj.Command(ctx, dir, args...)
	var stdout bytes.Buffer
	var stderr gitobj.StderrBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := dispatchadmission.RunProduction(ctx, dispatchadmission.SiteSyncGitRead, cmd); err != nil {
		return nil, gitobj.WrapError(ctx, args, err, stderr.String())
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
// MaxBlobBytes. The mutable tree-ish is resolved once to an immutable blob
// OID (gitobj.ResolveBlob), so a concurrent fetch cannot move HEAD between
// the size check and the bounded content read.
func CatFile(ctx context.Context, dataDir, repoName, ref, path string) ([]byte, error) {
	dir, dirErr := SafeRepoDir(dataDir, repoName)
	if err := errors.Join(dirErr, checkRef(ref), checkPath(path)); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("empty path: %w", ErrBadInput)
	}
	oid, size, err := gitobj.ResolveBlob(ctx, dir, spec(ref, path))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", store.ErrNotFound, err)
	}
	if size > MaxBlobBytes {
		return nil, fmt.Errorf("%s is %d bytes (limit %d): %w", path, size, MaxBlobBytes, ErrTooLarge)
	}
	return gitobj.ReadBlob(ctx, dir, oid, MaxBlobBytes)
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
