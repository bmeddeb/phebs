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
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repopath"
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

	maxCorpusPathBytes = repopath.MaxBytes
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
	// Candidate symlinks are resolved only inside the immutable Git tree. A
	// short chain bound prevents a malicious alias graph from turning one
	// candidate into unbounded object lookups.
	maxCorpusSymlinkDepth = 16
	// Exact root probes are used only to validate bounded attribution metadata.
	// They stream and stop at the first safe regular descendant, but a
	// repository can contain an arbitrarily large run of symlinks/gitlinks
	// before that proof. Refuse instead of scanning without a work bound.
	maxCorpusLookupRecords   = 4096
	maxCorpusLookupPathBytes = 1 << 20
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

// inventoryCorpus is the trusted production-only inventory capability. The
// extractor predicate is evaluated by the walker for both regular entries and
// symlinks, but only regular entries are exposed to the extractor. The bool
// passed to visit is the already-evaluated predicate result, which keeps one
// predicate call per tree entry and lets symlink admission remain
// extractor-specific without expanding the public SDK corpus.
type inventoryCorpus interface {
	WalkInventory(
		context.Context,
		func(string) (bool, error),
		func(string, bool) error,
	) error
}

// exactTreeCorpus is a trusted harness capability, deliberately absent from
// the extractor SDK. Candidate manifests replace complete-tree retention, so
// attribution validation uses exact immutable-tree probes for paths and roots
// that are not part of the bounded domain plan.
type exactTreeCorpus interface {
	lookupRegular(context.Context, string) (treeRecord, bool, error)
	anyRegularUnder(context.Context, string) (bool, error)
	readManifestBlob(context.Context, treeRecord, int64) (sdk.Blob, error)
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

// WalkFiles streams regular files from the exact tree. Generic SDK callers
// have no extractor predicate, so symlinks are skipped except for fixed-root
// product inputs, which remain fail-closed. Production extraction uses
// WalkInventory below to apply the current extractor's symlink policy.
func (g *gitCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	if visit == nil {
		return errors.New("walk corpus: nil visitor")
	}
	return g.walkTree(ctx, nil, func(filePath string, _ bool) error {
		return visit(filePath)
	})
}

// WalkInventory gates symlink handling with one extractor's Candidate
// predicate. A candidate symlink is validated as an alias to a same-commit
// final regular path that is also a candidate. The alias itself is never
// enumerated or made readable, preserving source citations at the final
// regular Git path.
func (g *gitCorpus) WalkInventory(
	ctx context.Context,
	candidate func(string) (bool, error),
	visit func(string, bool) error,
) error {
	if candidate == nil {
		return errors.New("walk corpus inventory: nil candidate predicate")
	}
	if visit == nil {
		return errors.New("walk corpus inventory: nil visitor")
	}
	return g.walkTree(ctx, candidate, visit)
}

type candidateSymlink struct {
	path string
	oid  string
}

func (g *gitCorpus) walkTree(
	ctx context.Context,
	candidate func(string) (bool, error),
	visit func(string, bool) error,
) error {
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
	var pipes dispatchadmission.CommandPipes
	stdout, err := pipes.StdoutPipe(cmd)
	if err != nil {
		return fmt.Errorf("walk corpus: stdout: %w", err)
	}
	// StdoutPipe leaves pipe ownership with the caller, so CommandContext
	// cannot close it when cancellation races a child blocked in write(2).
	// Close the read side before killing the process: SIGPIPE plus the explicit
	// kill make both deadline cancellation and local inventory refusals hard
	// stops instead of allowing cmd.Wait to strand the extraction lease.
	cmd.Cancel = func() error {
		_ = stdout.Close()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = abortTimeout
	var stderr gitobj.StderrBuffer
	cmd.Stderr = &stderr
	handle, err := dispatchadmission.StartPipedProduction(cmdCtx, dispatchadmission.SiteExtractTree, cmd, &pipes)
	if err != nil {
		return fmt.Errorf("walk corpus: start git: %w", err)
	}

	reader := bufio.NewReaderSize(stdout, maxTreeRecordBytes+1)
	var walkErr error
	previous := ""
	fileCount := 0
	oids := make(map[string]string)
	candidateRegulars := make(map[string]struct{})
	candidateSymlinks := make([]candidateSymlink, 0)
	candidateSymlinkPathBytes := 0
	boundaries := gitlinkInventory{}
	boundaryHash := sha256.New()
	_, _ = boundaryHash.Write([]byte(gitlinkDigestDomain))
	boundarySampleBytes := 0
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			walkErr = ctxErr
			break
		}
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
					walkErr = measuredOperationLimitError(
						fmt.Sprintf(
							"walk corpus: more than %d gitlink boundaries",
							maxCorpusFiles,
						),
						pipelinerefusal.DimensionInventoryFiles,
						int64(boundaries.count), int64(maxCorpusFiles),
					)
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
				// Fixed-root product inputs have separate provenance contracts
				// and remain fail-closed regardless of the extractor.
				if fixedErr := fixedRootSymlinkError(entry.path); fixedErr != nil {
					walkErr = fixedErr
					break
				}
				// Generic SDK walks have no extractor policy and simply skip
				// non-regular entries.
				if candidate == nil {
					break
				}
				isCandidate, candErr := candidate(entry.path)
				if candErr != nil {
					walkErr = candErr
					break
				}
				if !isCandidate {
					break
				}
				if pathErr := checkCorpusPath(entry.path); pathErr != nil {
					walkErr = fmt.Errorf("candidate symlink path is not readable: %w", pathErr)
					break
				}
				if len(candidateSymlinks) >= maxCorpusFiles {
					walkErr = measuredOperationLimitError(
						fmt.Sprintf(
							"corpus inventory exceeds %d candidate-symlink limit",
							maxCorpusFiles,
						),
						pipelinerefusal.DimensionInventoryFiles,
						int64(len(candidateSymlinks)+1), int64(maxCorpusFiles),
					)
					break
				}
				if len(entry.path) > maxCorpusInventoryPathBytes-candidateSymlinkPathBytes {
					walkErr = measuredOperationLimitError(
						fmt.Sprintf(
							"corpus inventory exceeds %d-byte candidate-symlink path limit",
							maxCorpusInventoryPathBytes,
						),
						pipelinerefusal.DimensionInventoryPathBytes,
						int64(candidateSymlinkPathBytes+len(entry.path)),
						int64(maxCorpusInventoryPathBytes),
					)
					break
				}
				candidateSymlinkPathBytes += len(entry.path)
				candidateSymlinks = append(candidateSymlinks, candidateSymlink{
					path: entry.path,
					oid:  entry.oid,
				})
			case entry.mode != "100644" && entry.mode != "100755":
				walkErr = fmt.Errorf("walk corpus: unsupported mode %s for %q", entry.mode, entry.path)
			default:
				fileCount++
				if fileCount > maxCorpusFiles {
					walkErr = measuredOperationLimitError(
						fmt.Sprintf(
							"walk corpus: more than %d regular files", maxCorpusFiles,
						),
						pipelinerefusal.DimensionInventoryFiles,
						int64(fileCount), int64(maxCorpusFiles),
					)
					break
				}
				// Entries with unrepresentable names are still visited so the
				// harness includes them in the published corpus file count; they
				// are never recorded as readable. The harness fails closed when
				// such an entry is an extraction candidate.
				isCandidate := false
				if candidate != nil {
					isCandidate, walkErr = candidate(entry.path)
					if walkErr != nil {
						break
					}
				}
				if checkCorpusPath(entry.path) == nil {
					oids[entry.path] = entry.oid
					if isCandidate {
						candidateRegulars[entry.path] = struct{}{}
					}
				}
				walkErr = visit(entry.path, isCandidate)
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
		stopTreeCommand(cmd, stdout, &handle)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("walk corpus: %w", ctxErr)
		}
		return walkErr
	}
	if err := handle.Wait(); err != nil {
		return gitobj.WrapError(ctx, args, err, stderr.String())
	}
	if len(candidateSymlinks) > 0 {
		entryCache := make(map[string]treeRecord, len(candidateSymlinks))
		for _, alias := range candidateSymlinks {
			entryCache[alias.path] = treeRecord{
				mode: "120000", objectType: "blob", oid: alias.oid, path: alias.path,
			}
		}
		targetCache := make(map[string]string)
		for _, alias := range candidateSymlinks {
			if err := resolveCandidateSymlink(
				ctx,
				dir,
				g.commit,
				entryCache[alias.path],
				oids,
				candidateRegulars,
				entryCache,
				targetCache,
			); err != nil {
				return err
			}
		}
	}
	boundaries.digest = "sha256:" + hex.EncodeToString(boundaryHash.Sum(nil))
	g.mu.Lock()
	g.oids = oids
	g.boundaries = boundaries
	g.mu.Unlock()
	return nil
}

func fixedRootSymlinkError(filePath string) error {
	if filePath == scipIndexPath {
		return fmt.Errorf("walk corpus: unsupported SCIP index symlink %q", filePath)
	}
	for _, snapshotPath := range attributionSnapshotPaths {
		if filePath == snapshotPath {
			return fmt.Errorf(
				"walk corpus: unsupported attribution snapshot symlink %q", filePath)
		}
	}
	return nil
}

func resolveCandidateSymlink(
	ctx context.Context,
	dir string,
	commit string,
	alias treeRecord,
	regularOIDs map[string]string,
	regularCandidates map[string]struct{},
	entryCache map[string]treeRecord,
	targetCache map[string]string,
) error {
	current := alias
	visited := make(map[string]struct{}, maxCorpusSymlinkDepth)
	for depth := 0; depth < maxCorpusSymlinkDepth; depth++ {
		if _, duplicate := visited[current.path]; duplicate {
			return fmt.Errorf("walk corpus: candidate symlink %q has a cycle at %q", alias.path, current.path)
		}
		visited[current.path] = struct{}{}

		target, ok := targetCache[current.oid]
		if !ok {
			content, err := gitobj.ReadBlob(ctx, dir, current.oid, maxCorpusPathBytes)
			if err != nil {
				if errors.Is(err, gitobj.ErrTooLarge) {
					return measuredOperationLimitCause(
						fmt.Sprintf(
							"walk corpus: symlink %q target exceeds %d-byte limit",
							current.path, maxCorpusPathBytes,
						),
						err, pipelinerefusal.DimensionSourceBlobBytes,
						maxCorpusPathBytes+1, maxCorpusPathBytes,
					)
				}
				return fmt.Errorf("walk corpus: read symlink %q target: %w", current.path, err)
			}
			target = string(content)
			targetCache[current.oid] = target
		}
		resolved, err := resolveCorpusSymlinkTarget(current.path, target)
		if err != nil {
			return fmt.Errorf("walk corpus: candidate symlink %q: %w", alias.path, err)
		}
		if _, regular := regularOIDs[resolved]; regular {
			if _, admitted := regularCandidates[resolved]; !admitted {
				return fmt.Errorf(
					"walk corpus: candidate symlink %q final target %q is not an extractor candidate",
					alias.path, resolved)
			}
			return nil
		}

		next, ok := entryCache[resolved]
		if !ok {
			next, err = lookupTreeEntry(ctx, dir, commit, resolved)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf(
						"walk corpus: candidate symlink %q target %q is missing: %w",
						alias.path, resolved, err)
				}
				return fmt.Errorf(
					"walk corpus: candidate symlink %q inspect target %q: %w",
					alias.path, resolved, err)
			}
			entryCache[resolved] = next
		}
		switch {
		case next.objectType == "commit" || next.mode == "160000":
			return fmt.Errorf(
				"walk corpus: candidate symlink %q targets gitlink %q", alias.path, resolved)
		case next.objectType == "tree":
			return fmt.Errorf(
				"walk corpus: candidate symlink %q targets directory %q", alias.path, resolved)
		case next.objectType != "blob":
			return fmt.Errorf(
				"walk corpus: candidate symlink %q targets unsupported %s entry %q",
				alias.path, next.objectType, resolved)
		case next.mode == "120000":
			if depth+1 == maxCorpusSymlinkDepth {
				return measuredOperationLimitError(
					fmt.Sprintf(
						"walk corpus: candidate symlink %q exceeds %d-link depth limit",
						alias.path, maxCorpusSymlinkDepth,
					),
					pipelinerefusal.DimensionSymlinkDepth,
					int64(depth+2), int64(maxCorpusSymlinkDepth),
				)
			}
			current = next
		case next.mode == "100644" || next.mode == "100755":
			// Every safe regular entry from the completed tree is in
			// regularOIDs. Reaching one only through a second lookup means the
			// streamed inventory and exact lookup disagree.
			return fmt.Errorf(
				"walk corpus: candidate symlink %q target %q was not in regular inventory",
				alias.path, resolved)
		default:
			return fmt.Errorf(
				"walk corpus: candidate symlink %q targets unsupported mode %s at %q",
				alias.path, next.mode, resolved)
		}
	}
	return measuredOperationLimitError(
		fmt.Sprintf(
			"walk corpus: candidate symlink %q exceeds %d-link depth limit",
			alias.path, maxCorpusSymlinkDepth,
		),
		pipelinerefusal.DimensionSymlinkDepth,
		int64(maxCorpusSymlinkDepth+1), int64(maxCorpusSymlinkDepth),
	)
}

func resolveCorpusSymlinkTarget(linkPath, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("symlink %q has an empty target", linkPath)
	}
	if path.IsAbs(target) {
		return "", fmt.Errorf("symlink %q has absolute target %q", linkPath, target)
	}
	if strings.Contains(target, `\`) || !utf8.ValidString(target) {
		return "", fmt.Errorf("symlink %q has unsafe target bytes", linkPath)
	}
	for _, r := range target {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("symlink %q has unsafe target bytes", linkPath)
		}
	}
	resolved := path.Clean(path.Join(path.Dir(linkPath), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("symlink %q target %q escapes repository root", linkPath, target)
	}
	if err := checkCorpusPath(resolved); err != nil {
		return "", fmt.Errorf("symlink %q target %q is unsafe: %w", linkPath, target, err)
	}
	return resolved, nil
}

func lookupTreeEntry(ctx context.Context, dir, commit, filePath string) (treeRecord, error) {
	const outputLimit = int64(maxTreeRecordBytes + 2)
	out, err := gitobj.Output(
		ctx,
		dir,
		outputLimit,
		"ls-tree",
		"-z",
		"--full-tree",
		commit,
		"--",
		":(literal)"+filePath,
	)
	if err != nil {
		if errors.Is(err, gitobj.ErrTooLarge) {
			return treeRecord{}, pipelinerefusal.Measure(
				err, pipelinerefusal.DimensionTreeRecordBytes,
				outputLimit+1, outputLimit,
			)
		}
		return treeRecord{}, err
	}
	if len(out) == 0 {
		return treeRecord{}, store.ErrNotFound
	}
	if out[len(out)-1] != 0 {
		return treeRecord{}, errors.New("walk corpus: exact tree lookup returned a truncated record")
	}
	records := bytes.Split(out[:len(out)-1], []byte{0})
	if len(records) != 1 {
		return treeRecord{}, fmt.Errorf(
			"walk corpus: exact tree lookup for %q returned %d records", filePath, len(records))
	}
	entry, err := parseTreeRecord(records[0])
	if err != nil {
		return treeRecord{}, err
	}
	if entry.path != filePath {
		return treeRecord{}, fmt.Errorf(
			"walk corpus: exact tree lookup for %q returned %q", filePath, entry.path)
	}
	return entry, nil
}

func (g *gitCorpus) lookupRegular(
	ctx context.Context,
	filePath string,
) (treeRecord, bool, error) {
	if err := checkCorpusPath(filePath); err != nil {
		return treeRecord{}, false, err
	}
	dir, err := g.repoDir()
	if err != nil {
		return treeRecord{}, false, err
	}
	if err := checkCommit(g.commit); err != nil {
		return treeRecord{}, false, err
	}
	if err := ensureCommit(ctx, dir, g.commit); err != nil {
		return treeRecord{}, false, err
	}
	entry, err := lookupSizedTreeEntry(ctx, dir, g.commit, filePath)
	if errors.Is(err, store.ErrNotFound) {
		return treeRecord{}, false, nil
	}
	if err != nil {
		return treeRecord{}, false, err
	}
	switch {
	case entry.objectType != "blob":
		return treeRecord{}, false, nil
	case entry.mode == "100644" || entry.mode == "100755":
		if entry.size < 0 {
			return treeRecord{}, false, fmt.Errorf(
				"lookup corpus %q: regular blob has no declared size", filePath)
		}
		return entry, true, nil
	case entry.mode == "120000":
		return treeRecord{}, false, nil
	default:
		return treeRecord{}, false, fmt.Errorf(
			"lookup corpus %q: unsupported blob mode %s", filePath, entry.mode)
	}
}

func lookupSizedTreeEntry(
	ctx context.Context,
	dir, commit, filePath string,
) (treeRecord, error) {
	const outputLimit = int64(maxTreeRecordBytes + 32)
	out, err := gitobj.Output(
		ctx,
		dir,
		outputLimit,
		"ls-tree",
		"-l",
		"-z",
		"--full-tree",
		commit,
		"--",
		":(literal)"+filePath,
	)
	if err != nil {
		if errors.Is(err, gitobj.ErrTooLarge) {
			return treeRecord{}, pipelinerefusal.Measure(
				err, pipelinerefusal.DimensionTreeRecordBytes,
				outputLimit+1, outputLimit,
			)
		}
		return treeRecord{}, err
	}
	if len(out) == 0 {
		return treeRecord{}, store.ErrNotFound
	}
	if out[len(out)-1] != 0 {
		return treeRecord{}, errors.New("lookup corpus: exact tree lookup returned a truncated record")
	}
	records := bytes.Split(out[:len(out)-1], []byte{0})
	if len(records) != 1 {
		return treeRecord{}, fmt.Errorf(
			"lookup corpus: exact tree lookup for %q returned %d records",
			filePath, len(records))
	}
	entry, err := parseSizedTreeRecord(records[0])
	if err != nil {
		return treeRecord{}, err
	}
	if entry.path != filePath {
		return treeRecord{}, fmt.Errorf(
			"lookup corpus: exact tree lookup for %q returned %q",
			filePath, entry.path)
	}
	return entry, nil
}

func (g *gitCorpus) anyRegularUnder(ctx context.Context, root string) (bool, error) {
	if err := checkCorpusPath(root); err != nil {
		return false, err
	}
	dir, err := g.repoDir()
	if err != nil {
		return false, err
	}
	if err := checkCommit(g.commit); err != nil {
		return false, err
	}
	if err := ensureCommit(ctx, dir, g.commit); err != nil {
		return false, err
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{
		"ls-tree", "-r", "-l", "-z", "--full-tree", g.commit,
		"--", ":(literal)" + root,
	}
	cmd := gitobj.Command(cmdCtx, dir, args...)
	var pipes dispatchadmission.CommandPipes
	stdout, err := pipes.StdoutPipe(cmd)
	if err != nil {
		return false, fmt.Errorf("lookup corpus root: stdout: %w", err)
	}
	cmd.Cancel = func() error {
		_ = stdout.Close()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = abortTimeout
	var stderr gitobj.StderrBuffer
	cmd.Stderr = &stderr
	handle, err := dispatchadmission.StartPipedProduction(cmdCtx, dispatchadmission.SiteExtractSubtree, cmd, &pipes)
	if err != nil {
		return false, fmt.Errorf("lookup corpus root: start git: %w", err)
	}

	reader := bufio.NewReaderSize(stdout, maxTreeRecordBytes+32)
	records := 0
	pathBytes := 0
	for {
		if err := ctx.Err(); err != nil {
			stopTreeCommand(cmd, stdout, &handle)
			return false, fmt.Errorf("lookup corpus root: %w", err)
		}
		record, readErr := readNULRecord(reader, maxTreeRecordBytes+31)
		if len(record) > 0 {
			records++
			if records > maxCorpusLookupRecords {
				stopTreeCommand(cmd, stdout, &handle)
				return false, measuredOperationLimitError(
					fmt.Sprintf(
						"lookup corpus root %q exceeds %d-record probe limit",
						root, maxCorpusLookupRecords,
					),
					pipelinerefusal.DimensionLookupRecords,
					int64(records), int64(maxCorpusLookupRecords),
				)
			}
			entry, parseErr := parseSizedTreeRecord(record)
			if parseErr != nil {
				stopTreeCommand(cmd, stdout, &handle)
				return false, parseErr
			}
			pathBytes += len(entry.path)
			if pathBytes > maxCorpusLookupPathBytes {
				stopTreeCommand(cmd, stdout, &handle)
				return false, measuredOperationLimitError(
					fmt.Sprintf(
						"lookup corpus root %q exceeds %d-byte path probe limit",
						root, maxCorpusLookupPathBytes,
					),
					pipelinerefusal.DimensionLookupPathBytes,
					int64(pathBytes), int64(maxCorpusLookupPathBytes),
				)
			}
			if entry.path != root &&
				strings.HasPrefix(entry.path, root+"/") &&
				entry.objectType == "blob" &&
				(entry.mode == "100644" || entry.mode == "100755") &&
				entry.size >= 0 &&
				checkCorpusPath(entry.path) == nil {
				stopTreeCommand(cmd, stdout, &handle)
				return true, nil
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				stopTreeCommand(cmd, stdout, &handle)
				return false, fmt.Errorf("lookup corpus root: read tree: %w", readErr)
			}
			break
		}
	}
	if err := handle.Wait(); err != nil {
		return false, gitobj.WrapError(ctx, args, err, stderr.String())
	}
	return false, nil
}

func (g *gitCorpus) readManifestBlob(
	ctx context.Context,
	entry treeRecord,
	maxBytes int64,
) (sdk.Blob, error) {
	if err := checkCorpusPath(entry.path); err != nil {
		return sdk.Blob{}, err
	}
	if entry.objectType != "blob" ||
		(entry.mode != "" && entry.mode != "100644" && entry.mode != "100755") ||
		!gitobj.IsObjectID(entry.oid) ||
		entry.size < 0 {
		return sdk.Blob{}, fmt.Errorf(
			"read corpus %q: invalid candidate-manifest entry", entry.path)
	}
	actual, present, err := g.lookupRegular(ctx, entry.path)
	if err != nil {
		return sdk.Blob{}, fmt.Errorf(
			"read corpus %q: verify candidate-manifest entry: %w", entry.path, err)
	}
	if !present || actual.oid != entry.oid || actual.size != entry.size ||
		entry.mode != "" && actual.mode != entry.mode {
		return sdk.Blob{}, fmt.Errorf(
			"read corpus %q: candidate-manifest entry does not match commit tree",
			entry.path)
	}
	if entry.size > maxBytes {
		return sdk.Blob{}, measuredOperationLimitError(
			fmt.Sprintf("corpus blob %q exceeds byte limit", entry.path),
			pipelinerefusal.DimensionSourceBlobBytes,
			entry.size, maxBytes,
		)
	}
	dir, err := g.repoDir()
	if err != nil {
		return sdk.Blob{}, err
	}
	content, err := gitobj.ReadBlob(ctx, dir, entry.oid, maxBytes)
	if err != nil {
		return sdk.Blob{}, fmt.Errorf("read corpus %q: content: %w", entry.path, err)
	}
	if int64(len(content)) != entry.size {
		return sdk.Blob{}, fmt.Errorf(
			"read corpus %q: manifest size %d does not match blob size %d",
			entry.path, entry.size, len(content))
	}
	digest := sha256.Sum256(content)
	return sdk.Blob{
		Content: string(content),
		Digest:  "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

// Read serves blob content by the object id recorded during WalkFiles: one
// immutable cat-file per read instead of re-verifying the commit and
// re-resolving path, type, and size through four child processes. Object ids
// never change, so a walked entry cannot be swapped underneath the run, and a
// path outside the walked tree is simply not found.
func (g *gitCorpus) Read(ctx context.Context, filePath string) (sdk.Blob, error) {
	return g.read(ctx, filePath, MaxBlobBytes)
}

// ReadSCIPIndex exposes only the legacy repository-root committed index
// through the larger SCIP-specific limit. Candidate-backed extraction reads
// the configured exact path through readManifestBlob instead.
func (g *gitCorpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	g.mu.Lock()
	_, present := g.oids[scipIndexPath]
	g.mu.Unlock()
	if !present {
		return sdk.SCIPInput{Path: scipIndexPath}, nil
	}
	blob, err := g.read(ctx, scipIndexPath, MaxSCIPIndexBytes)
	if err != nil {
		return sdk.SCIPInput{}, err
	}
	return sdk.SCIPInput{
		Path: scipIndexPath, Blob: blob, Present: true,
	}, nil
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
		if errors.Is(err, gitobj.ErrTooLarge) {
			return sdk.Blob{}, measuredOperationLimitCause(
				fmt.Sprintf("read corpus %q: content exceeds byte limit", filePath),
				err, pipelinerefusal.DimensionSourceBlobBytes,
				maxBytes+1, maxBytes,
			)
		}
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
	if err := gitobj.RejectAlternates(dir); err != nil {
		return "", fmt.Errorf("corpus repository: %w", err)
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
	if len(filePath) > maxCorpusPathBytes ||
		repopath.Validate(filePath) != nil {
		return fmt.Errorf("invalid corpus path %q", filePath)
	}
	return nil
}

func ensureCommit(ctx context.Context, dir, commit string) error {
	if err := gitobj.EnsureCommit(ctx, dir, commit); err != nil {
		return fmt.Errorf("corpus commit %s: %w", commit, err)
	}
	return nil
}

type treeRecord struct {
	mode       string
	objectType string
	oid        string
	path       string
	size       int64
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

func parseSizedTreeRecord(record []byte) (treeRecord, error) {
	meta, name, ok := bytes.Cut(record, []byte{'\t'})
	fields := strings.Fields(string(meta))
	if !ok || len(fields) != 4 || len(name) == 0 {
		return treeRecord{}, errors.New("lookup corpus: malformed sized ls-tree record")
	}
	if !gitobj.IsObjectID(fields[2]) {
		return treeRecord{}, errors.New("lookup corpus: invalid Git object id")
	}
	size := int64(-1)
	if fields[3] != "-" {
		var err error
		size, err = strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 {
			return treeRecord{}, errors.New("lookup corpus: invalid Git object size")
		}
	}
	return treeRecord{
		mode: fields[0], objectType: fields[1], oid: fields[2],
		path: string(name), size: size,
	}, nil
}

func readNULRecord(r *bufio.Reader, max int) ([]byte, error) {
	record, err := r.ReadSlice(0)
	if errors.Is(err, bufio.ErrBufferFull) || len(record) > max+1 {
		observed := len(record)
		if observed <= max {
			observed = max + 1
		}
		return nil, measuredOperationLimitError(
			fmt.Sprintf("tree record exceeds %d-byte limit", max),
			pipelinerefusal.DimensionTreeRecordBytes,
			int64(observed), int64(max),
		)
	}
	if len(record) > 0 && record[len(record)-1] != 0 && errors.Is(err, io.EOF) {
		return nil, io.ErrUnexpectedEOF
	}
	if len(record) > 0 && record[len(record)-1] == 0 {
		record = record[:len(record)-1]
	}
	return record, err
}

// stopTreeCommand synchronously closes, kills, and reaps a tree walk whose
// output is no longer being consumed. Closing first prevents a writer from
// remaining blocked on a full pipe while Process.Kill and Wait race.
func stopTreeCommand(cmd *exec.Cmd, stdout io.Closer, handle *dispatchadmission.PipedHandle) {
	_ = stdout.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = handle.Wait()
}
