//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// LinuxExecutionAuthorRequest borrows genuine Linux owners. Only the protected
// reference issuer can bind Author to Builds; selected paths grant no authority.
type LinuxExecutionAuthorRequest struct {
	Git    *LinuxExecutionGitCustody
	Builds *LinuxExecutionGoBuildCustody
	Author *LinuxExecutionToolCustody
	Plan   *LinuxExecutionInputCustody
}

type linuxAuthorRoot struct {
	path     string
	file     *os.File
	info     os.FileInfo
	identity LinuxExecutionCorpusSourceIdentity
}
type linuxAuthorAttempt struct {
	done   chan struct{}
	cancel context.CancelFunc
	result ExecutionAuthorResult
	err    error
	pid    int
	stderr []byte
	step   string
}

// LinuxExecutionAuthorCustody owns one serial A/B/A-return source and native
// mutation lease. It does not compose a live server or shared controller. Close
// joins tracked use before releasing descriptors; source bytes remain retained.
type LinuxExecutionAuthorCustody struct {
	mu                sync.Mutex
	request           LinuxExecutionAuthorRequest
	roots             []linuxAuthorRoot // parent, source, home, tmp
	lease             *os.File
	leaseInfo         os.FileInfo
	projection        authorPlanProjection
	planSHA256        string
	authorIdentity    ExecutionToolIdentity
	previous          *ExecutionCorpusAuthorResponse
	results           []ExecutionAuthorResult
	next              int
	active            *linuxAuthorAttempt
	last              *linuxAuthorAttempt
	closing, released bool
	err               error
}

const linuxAuthorLeaseName = ".t422-linux-source.lock"

func openLinuxAuthorOwnedRoot(path string) (linuxAuthorRoot, error) {
	root := linuxAuthorRoot{path: path}
	if !linuxGitPrivateDirectory(path) {
		return root, ErrExecutionAuthorCustody
	}
	var err error
	root.file, err = os.Open(path)
	if err == nil {
		root.info, err = root.file.Stat()
	}
	if err == nil {
		root.identity, err = observeLinuxAuthorRoot(root.file)
	}
	if err != nil {
		if root.file != nil {
			_ = root.file.Close()
			root.file = nil
		}
		return root, ErrExecutionAuthorCustody
	}
	return root, nil
}
func (root linuxAuthorRoot) check() error {
	if root.file == nil || root.info == nil || !linuxGitPrivateDirectory(root.path) {
		return ErrExecutionAuthorCustody
	}
	id, err := observeLinuxAuthorRoot(root.file)
	current, pathErr := os.Lstat(root.path)
	held, heldErr := root.file.Stat()
	if err != nil || pathErr != nil || heldErr != nil || id != root.identity || !os.SameFile(root.info, current) || !os.SameFile(held, current) {
		return ErrExecutionAuthorCustody
	}
	return nil
}

// PrepareLinuxExecutionAuthor validates actual reference lineage and the bounded
// sealed plan once, then creates empty mutable roots. It launches no author/Git.
// Partial construction returns retained custody. DecodePlan remains synchronous
// bounded work, with cancellation checked before and after.
func PrepareLinuxExecutionAuthor(ctx context.Context, parent string, request LinuxExecutionAuthorRequest) (*LinuxExecutionAuthorCustody, error) {
	if ctx == nil || ctx.Err() != nil || !linuxGitPrivateDirectory(parent) || request.Git == nil || request.Builds == nil || request.Author == nil || request.Plan == nil || request.Author.input == nil || request.Author.input == request.Plan || request.Author.referenceInputs != request.Builds || request.Builds.request.Git != request.Git {
		return nil, ErrExecutionAuthorCustody
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := request.Builds.CheckGo(ctx); err != nil {
		return nil, ErrExecutionAuthorCustody
	}
	identity, err := request.Author.Check(ctx, "t422-author")
	if err != nil {
		return nil, ErrExecutionAuthorCustody
	}
	var raw []byte
	err = request.Plan.WithInput(ctx, "plan", func(file *os.File) error {
		info, e := file.Stat()
		if e != nil || info.Size() <= 0 || info.Size() > MaxPlanBytes || info.Mode().Perm()&0o111 != 0 {
			return ErrExecutionAuthorCustody
		}
		raw, e = io.ReadAll(executionInputReader{ctx, io.LimitReader(file, MaxPlanBytes+1)})
		return e
	})
	if err != nil || len(raw) > MaxPlanBytes {
		return nil, ErrExecutionAuthorCustody
	}
	plan, err := DecodePlan(raw)
	if err != nil || !processAccountingPlanSemantics(plan.Schema) || ctx.Err() != nil {
		return nil, ErrExecutionAuthorCustody
	}
	request.Builds.mu.Lock()
	bound := linuxAuthorBuildBinding(request, identity, plan.SourceCommit)
	request.Builds.mu.Unlock()
	if !bound {
		return nil, ErrExecutionAuthorCustody
	}
	owner := &LinuxExecutionAuthorCustody{request: request, planSHA256: SHA256(raw), authorIdentity: identity}
	fail := func() (*LinuxExecutionAuthorCustody, error) {
		owner.err = ErrExecutionAuthorCustody
		return owner, owner.err
	}
	owner.projection, err = projectAuthorPlan(plan)
	if err != nil {
		return fail()
	}
	if owner.createRoots(ctx, parent) != nil {
		return fail()
	}
	if owner.checkRoots(ctx) != nil {
		return fail()
	}
	return owner, nil
}

func (owner *LinuxExecutionAuthorCustody) createRoots(ctx context.Context, parent string) error {
	root, err := openLinuxAuthorOwnedRoot(parent)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	owner.roots = append(owner.roots, root)
	leasePath := filepath.Join(parent, linuxAuthorLeaseName)
	owner.lease, err = os.OpenFile(leasePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	owner.leaseInfo, err = owner.lease.Stat()
	if err != nil || unix.Flock(int(owner.lease.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrExecutionAuthorCustody
	}
	for _, prefix := range []string{"t422-linux-source-", "t422-linux-home-", "t422-linux-tmp-"} {
		if ctx.Err() != nil {
			return ErrExecutionAuthorCustody
		}
		path, e := os.MkdirTemp(parent, prefix)
		if e != nil {
			return ErrExecutionAuthorCustody
		}
		owner.roots = append(owner.roots, linuxAuthorRoot{path: path})
		root, e := openLinuxAuthorOwnedRoot(path)
		if e != nil {
			return ErrExecutionAuthorCustody
		}
		owner.roots[len(owner.roots)-1] = root
		if root.identity.Device != owner.roots[0].identity.Device || root.identity.Mount != owner.roots[0].identity.Mount {
			return ErrExecutionAuthorCustody
		}
	}
	return nil
}

// Caller holds Builds.mu; this checks private issuer lineage, never supplied claims.
func linuxAuthorBuildBinding(request LinuxExecutionAuthorRequest, identity ExecutionToolIdentity, source string) bool {
	builds := request.Builds
	return builds != nil && !builds.closing && !builds.released && builds.err == nil && builds.active == nil && builds.tree != nil && builds.goIdentity.Role == "go" && request.Author.referenceInputs == builds && builds.request.Git == request.Git &&
		validCommit(source) && builds.request.PlanSourceCommit == source && builds.commits.CleanTree && builds.commits.IntegratedMainDescendsFromPlanSource && builds.commits.SourceDescendsFromIntegratedMain &&
		builds.reference.source == builds.commits.T422SourceCommit && identity.Role == "t422-author" && identity.BuildVCSRevision == builds.commits.T422SourceCommit
}
func (owner *LinuxExecutionAuthorCustody) checkRoots(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || len(owner.roots) != 4 || owner.lease == nil || owner.leaseInfo == nil {
		return ErrExecutionAuthorCustody
	}
	for _, root := range owner.roots {
		if root.check() != nil {
			return ErrExecutionAuthorCustody
		}
	}
	held, err := owner.lease.Stat()
	current, pathErr := os.Lstat(filepath.Join(owner.roots[0].path, linuxAuthorLeaseName))
	var stat unix.Stat_t
	if err != nil || pathErr != nil || !inputCustodySame(owner.leaseInfo, held) || !inputCustodySame(held, current) || unix.Fstat(int(owner.lease.Fd()), &stat) != nil || stat.Mode != unix.S_IFREG|0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Size != 0 {
		return ErrExecutionAuthorCustody
	}
	return nil
}
func (owner *LinuxExecutionAuthorCustody) checkSource(ctx context.Context, response *ExecutionCorpusAuthorResponse) error {
	root := owner.roots[1]
	author := &ExecutionCorpusAuthor{directory: root.path, root: root.file, rootInfo: root.info, platform: &linuxCorpusAuthorState{identity: root.identity}}
	if response != nil {
		author.config = response.ConfigSHA256
		return author.checkContinuity(ctx, response.Result.Commit)
	}
	// Open a fresh description; ReadDir must not mutate the keeper's offset.
	file, err := os.Open(root.path)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	rows, readErr := file.ReadDir(1)
	closeErr := file.Close()
	if len(rows) != 0 || !errors.Is(readErr, io.EOF) || closeErr != nil || author.checkRoot(ctx) != nil {
		return ErrExecutionAuthorCustody
	}
	return nil
}
func (owner *LinuxExecutionAuthorCustody) Directory() string {
	if owner == nil || len(owner.roots) < 2 {
		return ""
	}
	return owner.roots[1].path
}
func (owner *LinuxExecutionAuthorCustody) PrivateDirectories() []string {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.roots) < 2 {
		return nil
	}
	var paths []string
	for _, root := range owner.roots[1:] {
		paths = append(paths, root.path)
	}
	return paths
}
func (owner *LinuxExecutionAuthorCustody) Results() []ExecutionAuthorResult {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	results := make([]ExecutionAuthorResult, len(owner.results))
	for i, result := range owner.results {
		results[i] = cloneAuthorCustodyResult(result)
	}
	return results
}

// Close cancels the one worker and waits without holding the hook's mutex. A
// deadline retains all exact roots and input loans; it cannot certify joining.
func (owner *LinuxExecutionAuthorCustody) Close(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	if ctx == nil {
		return ErrExecutionAuthorCustody
	}
	owner.mu.Lock()
	owner.closing = true
	attempt := owner.active
	if attempt != nil {
		attempt.cancel()
	}
	owner.mu.Unlock()
	if attempt != nil {
		select {
		case <-attempt.done:
		case <-ctx.Done():
			return ErrExecutionAuthorCustody
		}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if !owner.released {
		for _, root := range owner.roots {
			if root.file != nil && root.file.Close() != nil {
				owner.err = ErrExecutionAuthorCustody
			}
		}
		if owner.lease != nil && owner.lease.Close() != nil {
			owner.err = ErrExecutionAuthorCustody
		}
		owner.released = true
	}
	return owner.err
}
