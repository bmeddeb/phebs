// Package gitobj is the shared bounded reader over bare-mirror Git object
// stores (TD.4): one hardened command builder, one bounded runner, one
// missing-object classifier, and one immutable-OID blob primitive serve the
// source, history, SCIP, and extraction read paths. Byte limits are always
// the caller's; this package never sets a default cap and never weakens one.
package gitobj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

// ErrTooLarge is returned when a read exceeds the caller's byte limit.
var ErrTooLarge = errors.New("git object exceeds byte limit")

// ErrExternalAlternate marks an object store whose reads could escape the
// managed mirror into another filesystem object database.
var ErrExternalAlternate = errors.New("git object store uses an external object alternate")

// maxStderrBytes caps retained diagnostics so a noisy failing child cannot
// allocate unbounded memory.
const maxStderrBytes = 64 << 10

// Command builds a read-only git invocation pinned to the repository's own
// object store: replace refs are ignored, inherited GIT_* environment is
// scrubbed, lazy promisor fetches and system/global config are disabled, and
// paths are emitted verbatim (core.quotePath=false). Not for clone/fetch:
// network writers keep their own runners.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := append([]string{"--no-replace-objects", "-c", "core.quotePath=false"}, args...)
	cmd := exec.CommandContext(ctx, dispatchadmission.ProductionTool("git"), full...)
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+6)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			env = append(env, value)
		}
	}
	cmd.Env = append(env,
		"GIT_NO_LAZY_FETCH=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
	)
	return cmd
}

// StderrBuffer retains a bounded prefix of child stderr for diagnostics.
type StderrBuffer struct{ buf bytes.Buffer }

func (b *StderrBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxStderrBytes - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	return n, nil
}

func (b *StderrBuffer) String() string { return b.buf.String() }

// WrapError converts a git child failure into a classified error: caller
// context cancellation wins, and stderr that names a missing object,
// revision, or path maps to store.ErrNotFound. This is the one classifier
// for every read path (TD.4).
func WrapError(ctx context.Context, args []string, runErr error, stderr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	werr := fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), runErr, strings.TrimSpace(stderr))
	for _, marker := range []string{
		"does not exist", "exists on disk, but not in", "invalid object name",
		"Not a valid object name", "not a valid object", "bad revision",
		"bad object", "not a tree object", "bad file", "ambiguous argument",
		"unknown revision", "Needed a single revision", "no such path",
		"no such file",
	} {
		if strings.Contains(stderr, marker) {
			return fmt.Errorf("%w: %w", store.ErrNotFound, werr)
		}
	}
	return werr
}

// Output runs a read-only git command with stdout capped at maxOutput bytes.
// Exceeding the cap kills the child and returns an error wrapping
// ErrTooLarge: object and metadata reads must never silently truncate.
func Output(ctx context.Context, dir string, maxOutput int64, args ...string) ([]byte, error) {
	cmd := Command(ctx, dir, args...)
	var pipes dispatchadmission.CommandPipes
	stdout, err := pipes.StdoutPipe(cmd)
	if err != nil {
		return nil, err
	}
	var stderr StderrBuffer
	cmd.Stderr = &stderr
	handle, err := dispatchadmission.StartPipedProduction(ctx, dispatchadmission.SiteGitOutput, cmd, &pipes)
	if err != nil {
		return nil, err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxOutput+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = handle.Wait()
		return nil, readErr
	}
	if int64(len(out)) > maxOutput {
		_ = cmd.Process.Kill()
		_ = handle.Wait()
		return nil, fmt.Errorf("git output exceeds %d-byte limit: %w", maxOutput, ErrTooLarge)
	}
	if err := handle.Wait(); err != nil {
		return nil, WrapError(ctx, args, err, stderr.String())
	}
	return out, nil
}

// IsObjectID reports whether value is a full lowercase hexadecimal Git
// object id (SHA-1 or SHA-256 length).
func IsObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// RejectAlternates refuses both bare-repository and ordinary-worktree
// alternates files. Lstat treats a symlink at the control path as present.
func RejectAlternates(dir string) error {
	for _, candidate := range []string{
		filepath.Join(dir, "objects", "info", "alternates"),
		filepath.Join(dir, ".git", "objects", "info", "alternates"),
	} {
		if _, err := os.Lstat(candidate); err == nil {
			return ErrExternalAlternate
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect git object alternates: %w", err)
		}
	}
	return nil
}

// EnsureCommit proves oid is an actual commit in the managed object store.
func EnsureCommit(ctx context.Context, dir, oid string) error {
	if !IsObjectID(oid) {
		return errors.New("commit must be a full lowercase object id")
	}
	out, err := Output(ctx, dir, 32, "cat-file", "-t", oid)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "commit" {
		return fmt.Errorf("git object %s is not a commit", oid)
	}
	return nil
}

// ResolveBlob resolves spec (a "rev" or "rev:path" expression) to an
// immutable blob object id and its size. Missing specs and non-blob objects
// classify as store.ErrNotFound.
func ResolveBlob(ctx context.Context, dir, spec string) (string, int64, error) {
	out, err := Output(ctx, dir, 128, "rev-parse", "--verify", spec)
	if err != nil {
		return "", 0, err
	}
	oid := strings.TrimSpace(string(out))
	if !IsObjectID(oid) {
		return "", 0, fmt.Errorf("git returned invalid object id %q", oid)
	}
	typeOut, err := Output(ctx, dir, 32, "cat-file", "-t", oid)
	if err != nil {
		return "", 0, err
	}
	if strings.TrimSpace(string(typeOut)) != "blob" {
		return "", 0, fmt.Errorf("%s is not a blob: %w", spec, store.ErrNotFound)
	}
	sizeOut, err := Output(ctx, dir, 128, "cat-file", "-s", oid)
	if err != nil {
		return "", 0, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64)
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("invalid git blob size %q", strings.TrimSpace(string(sizeOut)))
	}
	return oid, size, nil
}

// ReadBlob returns the exact bytes of the immutable blob oid in one child
// process, bounded by limit. Object ids never change, so no size pre-check
// or re-verification is needed for memory safety: the bounded reader stops
// at the cap and errors with ErrTooLarge.
func ReadBlob(ctx context.Context, dir, oid string, limit int64) ([]byte, error) {
	if !IsObjectID(oid) {
		return nil, fmt.Errorf("read blob: invalid object id %q", oid)
	}
	return Output(ctx, dir, limit, "cat-file", "blob", oid)
}
