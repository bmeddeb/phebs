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
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	// MaxBlobBytes bounds both the source blob and the parser working set's
	// input component. Extractors stream facts, so memory does not grow with
	// repository size or total fact count.
	MaxBlobBytes = int64(10 << 20)

	maxCorpusPathBytes = 4096
	maxTreeRecordBytes = maxCorpusPathBytes + 128
	maxGitErrorBytes   = 64 << 10
	maxCorpusFiles     = 200_000
	// Inventory keeps path identities so the extractor can replay the trusted
	// tree and the worker can prove that every declared candidate was read.
	// Bound the aggregate path payload independently from the file-count cap:
	// 200,000 individually valid 4 KiB paths would otherwise retain hundreds
	// of MiB before any source blob was parsed.
	maxCorpusInventoryPathBytes = 16 << 20
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
	cmd := gitCommand(cmdCtx, dir, "ls-tree", "-r", "-z", "--full-tree", g.commit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("walk corpus: stdout: %w", err)
	}
	var stderr cappedBuffer
	stderr.max = maxGitErrorBytes
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("walk corpus: start git: %w", err)
	}

	reader := bufio.NewReaderSize(stdout, maxTreeRecordBytes+1)
	var walkErr error
	previous := ""
	fileCount := 0
	oids := make(map[string]string)
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
				walkErr = fmt.Errorf("walk corpus: unsupported gitlink %q", entry.path)
			case entry.objectType != "blob":
				walkErr = fmt.Errorf("walk corpus: unsupported %s entry %q", entry.objectType, entry.path)
			case entry.mode == "120000":
				// Symlinks are not regular corpus content and are never visited.
				// A candidate .proto symlink is a declared-plane coverage gap and
				// therefore fails closed; unrelated repository symlinks are harmless.
				if strings.HasSuffix(entry.path, ".proto") {
					walkErr = fmt.Errorf("walk corpus: unsupported proto symlink %q", entry.path)
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
				// harness can keep them inside the coverage certificate; they are
				// never recorded as readable. The harness fails closed when such
				// an entry is an extraction candidate.
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
		return gitRunError(ctx, "ls-tree", err, stderr.String())
	}
	g.mu.Lock()
	g.oids = oids
	g.mu.Unlock()
	return nil
}

// Read serves blob content by the object id recorded during WalkFiles: one
// immutable cat-file per read instead of re-verifying the commit and
// re-resolving path, type, and size through four child processes. Object ids
// never change, so a walked entry cannot be swapped underneath the run, and a
// path outside the walked tree is simply not found.
func (g *gitCorpus) Read(ctx context.Context, filePath string) (sdk.Blob, error) {
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
	// runGitBounded caps memory at the limit regardless of blob size, and
	// errors past it, so no separate cat-file -s pre-check is needed.
	content, err := runGitBounded(ctx, dir, MaxBlobBytes, "cat-file", "blob", oid)
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
	if len(commit) != 40 && len(commit) != 64 {
		return fmt.Errorf("corpus commit must be a full object id")
	}
	for _, r := range commit {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return fmt.Errorf("corpus commit must be lowercase hexadecimal")
		}
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
	out, err := runGitBounded(ctx, dir, 32, "cat-file", "-t", commit)
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
// or not, so unreadable names stay inside the coverage certificate.
func parseTreeRecord(record []byte) (treeRecord, error) {
	meta, name, ok := bytes.Cut(record, []byte{'\t'})
	fields := strings.Fields(string(meta))
	if !ok || len(fields) != 3 || len(name) == 0 {
		return treeRecord{}, fmt.Errorf("walk corpus: malformed ls-tree record")
	}
	if err := checkObjectID(fields[2]); err != nil {
		return treeRecord{}, fmt.Errorf("walk corpus: %w", err)
	}
	return treeRecord{mode: fields[0], objectType: fields[1], oid: fields[2], path: string(name)}, nil
}

func checkObjectID(oid string) error {
	if len(oid) != 40 && len(oid) != 64 {
		return errors.New("invalid Git object id")
	}
	for _, r := range oid {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return errors.New("invalid Git object id")
		}
	}
	return nil
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

func gitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := append([]string{"--no-replace-objects", "-c", "core.quotePath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	// Never inherit GIT_DIR, GIT_WORK_TREE, alternate object directories,
	// config injection, or weaker duplicate values for the safe settings.
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

func runGitBounded(ctx context.Context, dir string, maxOutput int64, args ...string) ([]byte, error) {
	cmd := gitCommand(ctx, dir, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr cappedBuffer
	stderr.max = maxGitErrorBytes
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxOutput+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, readErr
	}
	if int64(len(out)) > maxOutput {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("git output exceeds %d-byte limit", maxOutput)
	}
	if err := cmd.Wait(); err != nil {
		return nil, gitRunError(ctx, args[0], err, stderr.String())
	}
	return out, nil
}

func gitRunError(ctx context.Context, operation string, runErr error, stderr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("git %s: %w: %s", operation, runErr, strings.TrimSpace(stderr))
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	return n, nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
