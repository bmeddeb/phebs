package codenav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

var (
	errBlobNotFound = errors.New("committed blob not found")
	revisionRE      = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

func validRevision(revision string) bool {
	return revisionRE.MatchString(revision)
}

func validateRepoPath(value string) error {
	if value == "" || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return fmt.Errorf("path %q is not repository-relative: %w", value, ErrInvalidInput)
	}
	if path.Clean(value) != value {
		return fmt.Errorf("path %q is not canonical: %w", value, ErrInvalidInput)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("path %q is not canonical: %w", value, ErrInvalidInput)
		}
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("path contains control bytes: %w", ErrInvalidInput)
		}
	}
	return nil
}

func (s *Service) safeRepoDir(repo string) (string, error) {
	if s.dataDir == "" {
		return "", errors.New("empty data directory")
	}
	return phebssync.SafeRepoDir(s.dataDir, repo)
}

func (s *Service) readBlob(ctx context.Context, repo, revision, filePath string, limit int64, tooLarge error) ([]byte, error) {
	dir, err := s.safeRepoDir(repo)
	if err != nil {
		return nil, err
	}
	commit, err := gitOutput(ctx, dir, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		if isMissingGitRevision(err) {
			return nil, fmt.Errorf("revision %s: %w", revision, ErrRevisionNotFound)
		}
		return nil, fmt.Errorf("resolve indexed revision: %w", err)
	}
	if strings.TrimSpace(string(commit)) != revision {
		return nil, fmt.Errorf("resolved revision differs from requested object ID: %w", ErrInvalidInput)
	}
	oid, err := gitOutput(ctx, dir, "rev-parse", "--verify", revision+":"+filePath)
	if err != nil {
		if isMissingGitPath(err) {
			return nil, errBlobNotFound
		}
		return nil, err
	}
	oidString := strings.TrimSpace(string(oid))
	typ, err := gitOutput(ctx, dir, "cat-file", "-t", oidString)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(typ)) != "blob" {
		return nil, errBlobNotFound
	}
	sizeBytes, err := gitOutput(ctx, dir, "cat-file", "-s", oidString)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeBytes)), 10, 64)
	if err != nil || size < 0 {
		return nil, fmt.Errorf("invalid git blob size %q", strings.TrimSpace(string(sizeBytes)))
	}
	if size > limit {
		return nil, fmt.Errorf("%s is %d bytes (limit %d): %w", filePath, size, limit, tooLarge)
	}
	data, err := gitOutput(ctx, dir, "cat-file", "blob", oidString)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size {
		return nil, fmt.Errorf("git blob size changed while reading %s", filePath)
	}
	return data, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &gitCommandError{operation: args[0], cause: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}

type gitCommandError struct {
	operation string
	cause     error
	stderr    string
}

func (e *gitCommandError) Error() string {
	if e.stderr == "" {
		return fmt.Sprintf("git %s: %v", e.operation, e.cause)
	}
	return fmt.Sprintf("git %s: %v: %s", e.operation, e.cause, e.stderr)
}

func (e *gitCommandError) Unwrap() error { return e.cause }

func isMissingGitPath(err error) bool {
	var commandError *gitCommandError
	if !errors.As(err, &commandError) {
		return false
	}
	return strings.Contains(commandError.stderr, "does not exist in") ||
		strings.Contains(commandError.stderr, "exists on disk, but not in") ||
		strings.Contains(commandError.stderr, "Needed a single revision")
}

func isMissingGitRevision(err error) bool {
	var commandError *gitCommandError
	if !errors.As(err, &commandError) {
		return false
	}
	for _, marker := range []string{
		"Needed a single revision",
		"bad object",
		"not a valid object name",
		"unknown revision",
	} {
		if strings.Contains(commandError.stderr, marker) {
			return true
		}
	}
	return false
}
