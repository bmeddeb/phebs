package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	maxGoBuildEntries   = 100_000
	maxGoBuildBytes     = 2 << 30
	maxGoBuildPathBytes = 64 << 20
)

// ErrExecutionGoBuildCustody never includes an input path or native diagnostic.
var ErrExecutionGoBuildCustody = errors.New("execution Go build inputs unavailable or changed")

// ExecutionGoBuildRequest selects actual inputs, not caller-authored identities.
// Git must be the real protected seven-image resource custody. Source must be a
// clean checkout; ModuleCache must already contain the offline dependency set.
type ExecutionGoBuildRequest struct {
	Git                  *ExecutionGitCustody
	RepositoryRoot       string
	PlanSourceCommit     string
	IntegratedMainCommit string
	SourceCommit         string
	GoRoot               string
	ModuleCache          string
}

type goBuildEntry struct {
	path   string
	info   os.FileInfo
	digest string
}

// ExecutionGoBuildInventory reports copied logical regular-file bytes on
// success and the conservative reserved upper bound after construction failure,
// not allocated filesystem bytes or a frozen ceremony resource allowance.
type ExecutionGoBuildInventory struct {
	Entries int
	Files   int
	Bytes   int64
}

// ExecutionGoBuildCustody owns exactly sdk, modules and source below one fresh
// private directory. All files and directories are immutable before the first
// Go command. It retains three root descriptors, not one descriptor per file.
// This is trusted-host input custody, not SDK vendor attestation, a sandbox,
// platform-library closure, command admission, or an execution-profile binding.
type ExecutionGoBuildCustody struct {
	mu         sync.Mutex
	directory  string
	root       *os.File
	tree       *os.Root
	parent     *os.File
	parentInfo os.FileInfo
	volume     [2]int32
	entries    []goBuildEntry
	names      map[string]bool
	pathBytes  int
	inventory  ExecutionGoBuildInventory
	git        *ExecutionGitCustody
	reference  executionReferenceSource
	commits    ExecutionCommits
	planSource string
	closed     bool
	err        error
}

// ProtectExecutionGoBuildInputs creates fresh copies without modifying the
// selected SDK, cache or checkout. The local aggregate ceiling is 100,000 entries,
// 2 GiB logical bytes, 256 MiB per file and 64 MiB of relative names; each path is
// at most 4096 bytes. These are refusal ceilings, not frozen flow limits.
//
// Construction is serial and cooperatively bounded to twenty minutes. A failed
// constructor after Mkdir returns closed, retained custody. Close never thaws or
// removes it; the owner must bound retries and dispose each exact retained root.
// Writable build/cache outputs are separately owned and are never placed here.
func ProtectExecutionGoBuildInputs(ctx context.Context, parent string, request ExecutionGoBuildRequest) (_ *ExecutionGoBuildCustody, retErr error) {
	if ctx == nil || ctx.Err() != nil || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" ||
		!executionGitPrivateDirectory(parent) || request.Git == nil {
		return nil, ErrExecutionGoBuildCustody
	}
	for _, path := range []string{request.RepositoryRoot, request.GoRoot, request.ModuleCache} {
		if !executionGitAbsolutePath(path) {
			return nil, ErrExecutionGoBuildCustody
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical != path {
			return nil, ErrExecutionGoBuildCustody
		}
	}
	for _, commit := range []string{request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit} {
		if !validCommit(commit) {
			return nil, ErrExecutionGoBuildCustody
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	identity, git, err := request.Git.Check(ctx)
	if err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	origin := executionCheckoutInspector{root: request.RepositoryRoot, git: git, digest: identity.SHA256, custody: request.Git}
	commits, err := inspectExecutionCheckout(ctx, origin, request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit)
	if err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	parentFile, err := t4013.OpenHostImage(parent)
	if err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	parentInfo, err := parentFile.Stat()
	if err != nil || !inputCustodyOwned(parentInfo) || parentInfo.Mode().Perm() != 0o700 || !parentInfo.IsDir() {
		_ = parentFile.Close()
		return nil, ErrExecutionGoBuildCustody
	}
	directory, err := os.MkdirTemp(parent, "t422-go-inputs-")
	if err != nil {
		_ = parentFile.Close()
		return nil, ErrExecutionGoBuildCustody
	}
	custody := &ExecutionGoBuildCustody{directory: directory, parent: parentFile, parentInfo: parentInfo,
		git: request.Git, commits: commits, names: make(map[string]bool)}
	defer func() {
		if retErr != nil {
			custody.err = ErrExecutionGoBuildCustody
			_ = custody.Close()
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	custody.root, err = t4013.OpenHostImage(directory)
	if err != nil {
		return custody, ErrExecutionGoBuildCustody
	}
	custody.tree, err = os.OpenRoot(directory)
	if err != nil {
		return custody, ErrExecutionGoBuildCustody
	}
	rootInfo, statErr := custody.root.Stat()
	treeInfo, treeErr := custody.tree.Stat(".")
	if statErr != nil || treeErr != nil || !inputCustodyOwned(rootInfo) || !os.SameFile(rootInfo, treeInfo) {
		return custody, ErrExecutionGoBuildCustody
	}
	custody.volume, err = inputCustodyVolume(custody.root)
	if err != nil || custody.addName(".") != nil {
		return custody, ErrExecutionGoBuildCustody
	}
	// Source Git metadata is generated only by the protected Git. Reserve its
	// conservative construction envelope before allowing any object-store write.
	if err := custody.prepareSource(ctx, origin, request.SourceCommit); err != nil {
		return custody, ErrExecutionGoBuildCustody
	}
	if err := custody.makeDirectory("sdk"); err != nil {
		return custody, err
	}
	for _, selection := range []string{"bin/go", "src", "pkg", "lib", "go.env", "VERSION"} {
		if err := custody.copyTree(ctx, request.GoRoot, selection, filepath.Join("sdk", selection)); err != nil {
			return custody, err
		}
	}
	if err := custody.prepareModules(ctx, request.ModuleCache); err != nil {
		return custody, err
	}
	if err := custody.sealDirectories(ctx); err != nil {
		return custody, err
	}
	if err := custody.Check(ctx); err != nil || custody.reference.verify(ctx) != nil {
		return custody, ErrExecutionGoBuildCustody
	}
	after, err := inspectExecutionCheckout(ctx, origin, request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit)
	if err != nil || after != commits {
		return custody, ErrExecutionGoBuildCustody
	}
	custody.planSource = request.PlanSourceCommit
	return custody, nil
}

// Directory is private cleanup location information, never returned evidence.
func (custody *ExecutionGoBuildCustody) Directory() string {
	if custody == nil {
		return ""
	}
	return custody.directory
}

// Inventory returns bounded aggregate measurements, not file names or authority.
func (custody *ExecutionGoBuildCustody) Inventory() ExecutionGoBuildInventory {
	if custody == nil {
		return ExecutionGoBuildInventory{}
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	return custody.inventory
}

// Check scans the entire bounded immutable inventory, without rehashing bytes.
// It is used only at fixed admission-stage Go command boundaries, never per
// compiler child, request, sync tick or publication. Any refusal is sticky.
func (custody *ExecutionGoBuildCustody) Check(ctx context.Context) error {
	if custody == nil {
		return ErrExecutionGoBuildCustody
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	return custody.check(ctx)
}

func (custody *ExecutionGoBuildCustody) check(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || custody.closed || custody.err != nil || custody.tree == nil || len(custody.entries) == 0 {
		custody.err = ErrExecutionGoBuildCustody
		return custody.err
	}
	canonical, canonicalErr := filepath.EvalSymlinks(custody.directory)
	parent, parentErr := os.Lstat(filepath.Dir(custody.directory))
	root, rootErr := os.Lstat(custody.directory)
	held, heldErr := custody.root.Stat()
	volume, volumeErr := inputCustodyVolume(custody.root)
	if canonicalErr != nil || canonical != custody.directory || parentErr != nil ||
		!os.SameFile(parent, custody.parentInfo) || !inputCustodyOwned(parent) || parent.Mode().Perm() != 0o700 ||
		rootErr != nil || heldErr != nil || !inputCustodySame(root, held) || volumeErr != nil || volume != custody.volume {
		custody.err = ErrExecutionGoBuildCustody
		return custody.err
	}
	for _, entry := range custody.entries {
		info, err := custody.tree.Lstat(entry.path)
		if ctx.Err() != nil || err != nil || !inputCustodySame(entry.info, info) || !inputCustodyProtected(info) {
			custody.err = ErrExecutionGoBuildCustody
			return custody.err
		}
	}
	if _, _, err := custody.git.Check(ctx); err != nil {
		custody.err = ErrExecutionGoBuildCustody
	}
	return custody.err
}

// Close only closes the three retained read-only root descriptors. Protected
// copies remain, including failures. It neither joins a child nor closes Git.
func (custody *ExecutionGoBuildCustody) Close() error {
	if custody == nil {
		return nil
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if !custody.closed {
		custody.closed = true
		if custody.tree != nil && custody.tree.Close() != nil {
			custody.err = ErrExecutionGoBuildCustody
		}
		for _, file := range []*os.File{custody.root, custody.parent} {
			if file != nil && file.Close() != nil {
				custody.err = ErrExecutionGoBuildCustody
			}
		}
	}
	return custody.err
}
