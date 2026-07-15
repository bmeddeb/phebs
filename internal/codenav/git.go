package codenav

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/store"
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

// readBlob resolves the pinned revision, then reads the immutable blob
// through the shared bounded reader (TD.4). The revision-vs-path not-found
// distinction is preserved by call structure: a failed commit resolve maps
// to ErrRevisionNotFound, a failed path resolve to errBlobNotFound.
func (s *Service) readBlob(ctx context.Context, repo, revision, filePath string, limit int64, tooLarge error) ([]byte, error) {
	dir, err := s.safeRepoDir(repo)
	if err != nil {
		return nil, err
	}
	commit, err := gitobj.Output(ctx, dir, 256, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("revision %s: %w", revision, ErrRevisionNotFound)
		}
		return nil, fmt.Errorf("resolve indexed revision: %w", err)
	}
	if strings.TrimSpace(string(commit)) != revision {
		return nil, fmt.Errorf("resolved revision differs from requested object ID: %w", ErrInvalidInput)
	}
	oid, size, err := gitobj.ResolveBlob(ctx, dir, revision+":"+filePath)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errBlobNotFound
		}
		return nil, err
	}
	if size > limit {
		return nil, fmt.Errorf("%s is %d bytes (limit %d): %w", filePath, size, limit, tooLarge)
	}
	data, err := gitobj.ReadBlob(ctx, dir, oid, limit)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size {
		return nil, fmt.Errorf("git blob size changed while reading %s", filePath)
	}
	return data, nil
}
