package extract

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	// MaxBlobBytes bounds every ordinary source blob and its parser input.
	// Extractors stream facts, so memory does not grow with repository size or
	// total fact count. Committed SCIP input has the separate fixed-root cap.
	MaxBlobBytes = int64(10 << 20)
	// MaxSCIPIndexBytes preserves the code-navigation reader's independent
	// committed-index ceiling without raising the source/extraction blob cap.
	MaxSCIPIndexBytes = int64(64 << 20)
	scipIndexPath     = "index.scip"

	maxCorpusPathBytes = 4096
	maxTreeRecordBytes = maxCorpusPathBytes + 128
	maxCorpusFiles     = 200_000
	// Inventory keeps path identities so the extractor can replay the trusted
	// tree and the worker can prove that every declared candidate was read.
	// Bound the aggregate path payload independently from the file-count cap:
	// 200,000 individually valid 4 KiB paths would otherwise retain hundreds
	// of MiB before any source blob was parsed.
	maxCorpusInventoryPathBytes = 16 << 20
	// Gitlink boundaries render at most this bounded, display-safe sample;
	// the domain-separated digest binds every boundary regardless (T19.8).
	maxGitlinkSamplePaths = 64
	maxGitlinkSampleBytes = 4 << 10
)

// CorpusFactory constructs immutable corpora and fences them with the same
// per-mirror lock used by fetch, reconciliation, deletion, and indexing.
type CorpusFactory interface {
	Lock(ctx context.Context, repoName string) (unlock func(), err error)
	New(repoName, commit string) sdk.Corpus
}

// GitCorpus returns the production corpus factory over bare mirrors below
// dataDir. Each read is pinned to an exact commit object, disables replacement
// objects, and disables lazy promisor fetches so extraction can never reach the
// network or observe a mutable ref.
func GitCorpus(dataDir string) CorpusFactory { return gitCorpusFactory{dataDir: dataDir} }

type gitCorpusFactory struct{ dataDir string }

func (f gitCorpusFactory) Lock(ctx context.Context, repoName string) (func(), error) {
	dir, err := phebssync.SafeRepoDir(f.dataDir, repoName)
	if err != nil {
		return nil, err
	}
	return repowork.LockContext(ctx, dir)
}

func (f gitCorpusFactory) New(repoName, commit string) sdk.Corpus {
	return &gitCorpus{dataDir: f.dataDir, repo: repoName, commit: commit}
}

type gitCorpus struct {
	dataDir string
	repo    string
	commit  string

	mu sync.Mutex
	// oids maps each walked regular-file path to its blob object id, so Read
	// is a single immutable cat-file instead of re-resolving commit, tree, and
	// size per call. Populated by WalkFiles; reads require a completed walk.
	oids map[string]string
	// boundaries is the gitlink inventory of the last completed walk (T19.8):
	// submodule pointers are repository boundaries — named and bound into
	// coverage, never traversed and never silently dropped.
	boundaries gitlinkInventory
}

// gitlinkInventory is the trusted walker's record of submodule boundaries.
// The digest is authoritative and covers every gitlink, including those whose
// path bytes are unsafe to render; the sample is a bounded, display-safe
// subset. Recalculation authority stays with the walker: the store validates
// shape and consistency but cannot recompute the Git tree.
type gitlinkInventory struct {
	count           int
	digest          string
	samplePaths     []string
	sampleTruncated bool
}

// gitlinkDigestDomain separates the boundary digest from every other sha256
// use in the evidence plane.
const gitlinkDigestDomain = "phebs-corpus-gitlink-v1\x00"

// emptyGitlinkInventory is the canonical zero-boundary inventory, identical
// to what a completed walk over a gitlink-free tree produces.
func emptyGitlinkInventory() gitlinkInventory {
	hash := sha256.New()
	_, _ = hash.Write([]byte(gitlinkDigestDomain))
	return gitlinkInventory{digest: "sha256:" + hex.EncodeToString(hash.Sum(nil))}
}

// boundaryCorpus is the optional trusted-side capability a corpus offers when
// it can enumerate gitlink boundaries. sdk extractors never see it.
type boundaryCorpus interface {
	gitlinkBoundaries() gitlinkInventory
}

func (g *gitCorpus) gitlinkBoundaries() gitlinkInventory {
	g.mu.Lock()
	defer g.mu.Unlock()
	boundaries := g.boundaries
	boundaries.samplePaths = append([]string(nil), g.boundaries.samplePaths...)
	return boundaries
}

func (g *gitCorpus) RepoName() string { return g.repo }
func (g *gitCorpus) Commit() string   { return g.commit }

// WalkFiles streams the exact tree without buffering its path list. Special
// entries are rejected instead of silently shrinking coverage: symlinks and
// gitlinks are not immutable regular-file content available to the parser.
func (g *gitCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	if visit == nil {
		return errors.New("walk corpus: nil visitor")
	}
	dir, err := g.repoDir()
	if err != nil {
		return err
	}
	if err := checkCommit(g.commit); err != nil {
		return err
	}
	if err := ensureCommit(ctx, dir, g.commit); err != nil {
		return err
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"ls-tree", "-r", "-z", "--full-tree", g.commit}
	cmd := gitobj.Command(cmdCtx, dir, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("walk corpus: stdout: %w", err)
	}
	var stderr gitobj.StderrBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("walk corpus: start git: %w", err)
	}

	reader := bufio.NewReaderSize(stdout, maxTreeRecordBytes+1)
	var walkErr error
	previous := ""
	fileCount := 0
	oids := make(map[string]string)
	boundaries := gitlinkInventory{}
	boundaryHash := sha256.New()
	_, _ = boundaryHash.Write([]byte(gitlinkDigestDomain))
	boundarySampleBytes := 0
	for {
		record, readErr := readNULRecord(reader, maxTreeRecordBytes)
		if len(record) > 0 {
			entry, parseErr := parseTreeRecord(record)
			if parseErr != nil {
				walkErr = parseErr
				break
			}
			if entry.path <= previous {
				walkErr = fmt.Errorf("walk corpus: non-unique or unsorted path %q", entry.path)
				break
			}
			previous = entry.path
			switch {
			case entry.objectType == "commit" || entry.mode == "160000":
				// A gitlink is a pointer to a different repository, not a blob
				// of this one. It is recorded as an explicit coverage boundary
				// (count, domain-separated digest over sorted path/oid records,
				// bounded display-safe sample) and never traversed (T19.8).
				boundaries.count++
				if boundaries.count > maxCorpusFiles {
					walkErr = fmt.Errorf("walk corpus: more than %d gitlink boundaries", maxCorpusFiles)
					break
				}
				_, _ = boundaryHash.Write([]byte(entry.path))
				_, _ = boundaryHash.Write([]byte{0})
				_, _ = boundaryHash.Write([]byte(entry.oid))
				_, _ = boundaryHash.Write([]byte{0})
				// Unsafe path bytes still bind count and digest; only the
				// rendered sample omits them.
				if checkCorpusPath(entry.path) == nil {
					if len(boundaries.samplePaths) < maxGitlinkSamplePaths &&
						boundarySampleBytes+len(entry.path) <= maxGitlinkSampleBytes {
						boundaries.samplePaths = append(boundaries.samplePaths, entry.path)
						boundarySampleBytes += len(entry.path)
					} else {
						boundaries.sampleTruncated = true
					}
				}
			case entry.objectType != "blob":
				walkErr = fmt.Errorf("walk corpus: unsupported %s entry %q", entry.objectType, entry.path)
			case entry.mode == "120000":
				// Symlinks are not regular corpus content and are never visited.
				// A candidate .proto or .thrift symlink is a declared-plane
				// coverage gap and therefore fails closed. The root SCIP index
				// is also a selected corpus input (T20.6), so a symlink there
				// must not degrade into the indistinguishable "index absent"
				// result. Unrelated repository symlinks are harmless.
				if entry.path == scipIndexPath {
					walkErr = fmt.Errorf("walk corpus: unsupported SCIP index symlink %q", entry.path)
				}
				if strings.HasSuffix(entry.path, ".proto") {
					walkErr = fmt.Errorf("walk corpus: unsupported proto symlink %q", entry.path)
				}
				if strings.HasSuffix(entry.path, ".thrift") {
					walkErr = fmt.Errorf("walk corpus: unsupported thrift symlink %q", entry.path)
				}
			case entry.mode != "100644" && entry.mode != "100755":
				walkErr = fmt.Errorf("walk corpus: unsupported mode %s for %q", entry.mode, entry.path)
			default:
				fileCount++
				if fileCount > maxCorpusFiles {
					walkErr = fmt.Errorf("walk corpus: more than %d regular files", maxCorpusFiles)
					break
				}
				// Entries with unrepresentable names are still visited so the
				// harness includes them in the published corpus file count; they
				// are never recorded as readable. The harness fails closed when
				// such an entry is an extraction candidate.
				if checkCorpusPath(entry.path) == nil {
					oids[entry.path] = entry.oid
				}
				walkErr = visit(entry.path)
			}
			if walkErr != nil {
				break
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				walkErr = fmt.Errorf("walk corpus: read tree: %w", readErr)
			}
			break
		}
	}
	if walkErr != nil {
		cancel()
		_ = cmd.Wait()
		return walkErr
	}
	if err := cmd.Wait(); err != nil {
		return gitobj.WrapError(ctx, args, err, stderr.String())
	}
	boundaries.digest = "sha256:" + hex.EncodeToString(boundaryHash.Sum(nil))
	g.mu.Lock()
	g.oids = oids
	g.boundaries = boundaries
	g.mu.Unlock()
	return nil
}

// Read serves blob content by the object id recorded during WalkFiles: one
// immutable cat-file per read instead of re-verifying the commit and
// re-resolving path, type, and size through four child processes. Object ids
// never change, so a walked entry cannot be swapped underneath the run, and a
// path outside the walked tree is simply not found.
func (g *gitCorpus) Read(ctx context.Context, filePath string) (sdk.Blob, error) {
	return g.read(ctx, filePath, MaxBlobBytes)
}

// ReadSCIPIndex exposes only the repository-root committed index through the
// larger SCIP-specific limit. It cannot be used to read arbitrary blobs.
func (g *gitCorpus) ReadSCIPIndex(ctx context.Context) (sdk.Blob, error) {
	return g.read(ctx, scipIndexPath, MaxSCIPIndexBytes)
}

func (g *gitCorpus) read(ctx context.Context, filePath string, maxBytes int64) (sdk.Blob, error) {
	dir, err := g.repoDir()
	if err != nil {
		return sdk.Blob{}, err
	}
	if err := checkCorpusPath(filePath); err != nil {
		return sdk.Blob{}, err
	}
	g.mu.Lock()
	oids := g.oids
	g.mu.Unlock()
	if oids == nil {
		return sdk.Blob{}, fmt.Errorf("read corpus %q: read before corpus walk", filePath)
	}
	oid, ok := oids[filePath]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("read corpus %q: %w", filePath, store.ErrNotFound)
	}
	content, err := gitobj.ReadBlob(ctx, dir, oid, maxBytes)
	if err != nil {
		return sdk.Blob{}, fmt.Errorf("read corpus %q: content: %w", filePath, err)
	}
	digest := sha256.Sum256(content)
	return sdk.Blob{Content: string(content), Digest: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func (g *gitCorpus) repoDir() (string, error) {
	dir, err := phebssync.SafeRepoDir(g.dataDir, g.repo)
	if err != nil {
		return "", fmt.Errorf("corpus repository: %w", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "objects", "info", "alternates")); err == nil {
		return "", errors.New("corpus repository uses an external object alternate")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect corpus object alternates: %w", err)
	}
	return dir, nil
}

func checkCommit(commit string) error {
	if !gitobj.IsObjectID(commit) {
		return fmt.Errorf("corpus commit must be a full lowercase hexadecimal object id")
	}
	return nil
}

func checkCorpusPath(filePath string) error {
	if filePath == "" || len(filePath) > maxCorpusPathBytes ||
		strings.HasPrefix(filePath, "/") || strings.HasPrefix(filePath, "-") ||
		strings.Contains(filePath, `\`) || path.Clean(filePath) != filePath ||
		!utf8.ValidString(filePath) {
		return fmt.Errorf("invalid corpus path %q", filePath)
	}
	for _, part := range strings.Split(filePath, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid corpus path %q", filePath)
		}
	}
	for _, r := range filePath {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid corpus path %q", filePath)
		}
	}
	return nil
}

func ensureCommit(ctx context.Context, dir, commit string) error {
	out, err := gitobj.Output(ctx, dir, 32, "cat-file", "-t", commit)
	if err != nil {
		return fmt.Errorf("corpus commit %s: %w", commit, err)
	}
	if strings.TrimSpace(string(out)) != "commit" {
		return fmt.Errorf("corpus object %s is not a commit", commit)
	}
	return nil
}

type treeRecord struct {
	mode       string
	objectType string
	oid        string
	path       string
}

// parseTreeRecord validates record structure and object id only. Path rules
// are the harness's policy: the walk surfaces every regular entry, valid name
// or not, so unreadable names contribute to the published corpus file count.
func parseTreeRecord(record []byte) (treeRecord, error) {
	meta, name, ok := bytes.Cut(record, []byte{'\t'})
	fields := strings.Fields(string(meta))
	if !ok || len(fields) != 3 || len(name) == 0 {
		return treeRecord{}, fmt.Errorf("walk corpus: malformed ls-tree record")
	}
	if !gitobj.IsObjectID(fields[2]) {
		return treeRecord{}, errors.New("walk corpus: invalid Git object id")
	}
	return treeRecord{mode: fields[0], objectType: fields[1], oid: fields[2], path: string(name)}, nil
}

func readNULRecord(r *bufio.Reader, max int) ([]byte, error) {
	record, err := r.ReadSlice(0)
	if errors.Is(err, bufio.ErrBufferFull) || len(record) > max+1 {
		return nil, fmt.Errorf("tree record exceeds %d-byte limit", max)
	}
	if len(record) > 0 && record[len(record)-1] != 0 && errors.Is(err, io.EOF) {
		return nil, io.ErrUnexpectedEOF
	}
	if len(record) > 0 && record[len(record)-1] == 0 {
		record = record[:len(record)-1]
	}
	return record, err
}
