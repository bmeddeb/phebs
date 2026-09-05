package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

var ErrExecutionAuthorCustody = errors.New("execution author custody unavailable or changed")

// ExecutionAuthorRequest borrows genuine protected inputs. Plan must contain
// the fixed non-executable name "plan"; Author must have been independently
// reference-built by Builds, with the same protected Git. No supplied path,
// command, prior response, revision choice or verified bit issues authority.
type ExecutionAuthorRequest struct {
	Git    *ExecutionGitCustody
	Builds *ExecutionGoBuildCustody
	Author *ExecutionToolCustody
	Plan   *ExecutionInputCustody
}

// ExecutionAuthorCustody owns one fresh source and its exclusive native lease,
// plus private home/tmp roots. Borrowed protected inputs must outlive every
// joined author. Close releases descriptors only, never source or input bytes.
// This is a serial author vertical, not full host/profile or executor admission.
type ExecutionAuthorCustody struct {
	mu          sync.Mutex
	parent      string
	request     ExecutionAuthorRequest
	roots       []productionRoot // parent, source, home, temporary
	lease       *os.File
	leaseInfo   os.FileInfo
	identity    ExecutionCorpusSourceIdentity
	planPath    string
	planSHA256  string
	authorPath  string
	gitPath     string
	environment []string
	tools       []dispatchadmission.ProductionToolBinding
	expected    [3]AuthoredExecutionRevision
	deadlines   [3]time.Duration
	phases      [3]uint32
	previous    *ExecutionCorpusAuthorResponse
	results     []ExecutionAuthorResult
	next        int
	active      bool
	closed      bool
	err         error
}

// PrepareExecutionAuthor performs one complete existing Builds.Check, reads
// and hashes the bounded protected V3 plan once, and creates only empty private
// mutable roots. DecodePlan regenerates frozen identities synchronously; its
// existing contextless work is checked before/after, not made interruptible by
// the five-minute cooperative constructor context. It starts no author/Git.
func PrepareExecutionAuthor(ctx context.Context, parent string, request ExecutionAuthorRequest) (_ *ExecutionAuthorCustody, retErr error) {
	if ctx == nil || ctx.Err() != nil || !executionGitPrivateDirectory(parent) || request.Git == nil ||
		request.Builds == nil || request.Author == nil || request.Plan == nil ||
		request.Author.referenceInputs != request.Builds || request.Builds.git != request.Git {
		return nil, ErrExecutionAuthorCustody
	}
	for _, directory := range []string{request.Git.Directory(), request.Builds.Directory(), request.Author.Directory(), request.Plan.Directory()} {
		if filepath.Dir(directory) != parent {
			return nil, ErrExecutionAuthorCustody
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if request.Builds.Check(ctx) != nil {
		return nil, ErrExecutionAuthorCustody
	}
	path, raw, err := readAuthorCustodyPlan(ctx, request.Plan)
	if err != nil {
		return nil, ErrExecutionAuthorCustody
	}
	plan, err := DecodePlan(raw)
	if err != nil || plan.Schema != PlanV3Schema || !authorCustodyBuildBinding(request.Builds, plan.SourceCommit) || ctx.Err() != nil {
		return nil, ErrExecutionAuthorCustody
	}
	identity, authorPath, err := request.Author.Check(ctx, "t422-author")
	if err != nil || identity.BuildVCSRevision != request.Builds.commits.T422SourceCommit {
		return nil, ErrExecutionAuthorCustody
	}
	_, gitPath, err := request.Git.Check(ctx)
	if err != nil {
		return nil, ErrExecutionAuthorCustody
	}
	custody := &ExecutionAuthorCustody{parent: parent, request: request, planPath: path,
		planSHA256: SHA256(raw), authorPath: authorPath, gitPath: gitPath}
	defer func() {
		if retErr != nil {
			custody.err = ErrExecutionAuthorCustody
			_ = custody.Close()
			retErr = ErrExecutionAuthorCustody
		}
	}()
	if custody.bindPlan(plan) != nil || custody.createRoots(ctx) != nil {
		return custody, ErrExecutionAuthorCustody
	}
	environment, err := request.Git.Environment(ctx, custody.roots[2].path, custody.roots[3].path)
	if err != nil {
		return custody, ErrExecutionAuthorCustody
	}
	custody.tools = []dispatchadmission.ProductionToolBinding{{Role: "git", Path: gitPath, Environment: environment}}
	custody.environment = append(append([]string(nil), environment...), dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionSelector)
	if custody.check(ctx) != nil {
		return custody, ErrExecutionAuthorCustody
	}
	return custody, nil
}

func authorCustodyBuildBinding(builds *ExecutionGoBuildCustody, planSource string) bool {
	if builds == nil {
		return false
	}
	builds.mu.Lock()
	defer builds.mu.Unlock()
	return !builds.closed && builds.err == nil && validCommit(planSource) && builds.planSource == planSource &&
		validCommit(builds.commits.IntegratedMainCommit) && validCommit(builds.commits.T422SourceCommit) &&
		builds.reference.source == builds.commits.T422SourceCommit && builds.commits.CleanTree &&
		builds.commits.IntegratedMainDescendsFromPlanSource && builds.commits.SourceDescendsFromIntegratedMain
}

func readAuthorCustodyPlan(ctx context.Context, inputs *ExecutionInputCustody) (_ string, _ []byte, retErr error) {
	path, err := inputs.Check(ctx, "plan")
	if err != nil {
		return "", nil, ErrExecutionAuthorCustody
	}
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return "", nil, ErrExecutionAuthorCustody
	}
	defer func() {
		if file.Close() != nil {
			retErr = ErrExecutionAuthorCustody
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !inputCustodyProtected(info) || info.Mode().Perm()&0o111 != 0 || info.Size() <= 0 || info.Size() > MaxPlanBytes {
		return "", nil, ErrExecutionAuthorCustody
	}
	raw, err := io.ReadAll(executionInputReader{ctx, io.LimitReader(file, MaxPlanBytes+1)})
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	checkedPath, custodyErr := inputs.Check(ctx, "plan")
	if err != nil || statErr != nil || pathErr != nil || custodyErr != nil || checkedPath != path ||
		len(raw) > MaxPlanBytes || !inputCustodySame(info, after) || !inputCustodySame(after, current) || ctx.Err() != nil {
		return "", nil, ErrExecutionAuthorCustody
	}
	return path, raw, nil
}

func (custody *ExecutionAuthorCustody) bindPlan(plan Plan) error {
	if len(plan.Revisions.Physical) != 3 {
		return ErrExecutionAuthorCustody
	}
	// Only the manifest projection is reused; the parent does not regenerate
	// 31,602 author descriptors or repeat the child's full streamed census.
	source := corpusAuthorSource{recipe: plan.Revisions.SourceRecipe, profile: plan.Profile}
	parent := ""
	for index, name := range []string{"a", "b", "a-return"} {
		physical := plan.Revisions.Physical[index]
		if physical.Name != name {
			return ErrExecutionAuthorCustody
		}
		raw, err := canonicalGitCommitBytesFor(physical.ExpectedTree, parent, physical.CommitMessage, source.recipe)
		if err != nil || gitSHA1ObjectID("commit", raw) != physical.ExpectedCommit {
			return ErrExecutionAuthorCustody
		}
		custody.expected[index] = AuthoredExecutionRevision{Name: name, Commit: physical.ExpectedCommit,
			Tree: physical.ExpectedTree, ParentCommit: parent, Manifest: source.manifest(physical, physical.ExpectedTreeInventory, SHA256(raw))}
		phase := []string{"cold", "physical_delta_b", "return_a"}[index]
		for phaseIndex, candidate := range plan.PhaseOrder {
			if candidate == phase {
				custody.phases[index] = uint32(phaseIndex + 1)
			}
		}
		for _, deadline := range plan.PhaseDeadlines {
			if deadline.Phase == phase && deadline.DeadlineMS > 0 && deadline.DeadlineMS <= uint64(math.MaxInt64/int64(time.Millisecond)) {
				custody.deadlines[index] = time.Duration(deadline.DeadlineMS) * time.Millisecond
			}
		}
		if custody.deadlines[index] == 0 || custody.phases[index] == 0 {
			return ErrExecutionAuthorCustody
		}
		parent = physical.ExpectedCommit
	}
	return nil
}

func (custody *ExecutionAuthorCustody) createRoots(ctx context.Context) error {
	parent, err := openProductionRoot(custody.parent)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	custody.roots = append(custody.roots, parent)
	custody.lease, err = acquireProductionSourceLease(custody.parent)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	custody.leaseInfo, err = custody.lease.Stat()
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	for _, prefix := range []string{"t422-source-", "t422-author-home-", "t422-author-tmp-"} {
		if ctx.Err() != nil {
			return ErrExecutionAuthorCustody
		}
		path, err := os.MkdirTemp(custody.parent, prefix)
		if err != nil {
			return ErrExecutionAuthorCustody
		}
		// Retain the exact created path even if opening its native descriptor fails.
		custody.roots = append(custody.roots, productionRoot{path: path})
		root, err := openProductionRoot(path)
		if err != nil {
			return ErrExecutionAuthorCustody
		}
		custody.roots[len(custody.roots)-1] = root
		if root.volume != parent.volume {
			return ErrExecutionAuthorCustody
		}
	}
	custody.identity, err = corpusAuthorNativeIdentity(custody.roots[1].info, custody.roots[1].volume)
	return err
}

func (custody *ExecutionAuthorCustody) checkRoots(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || custody.closed || custody.err != nil || len(custody.roots) != 4 || custody.lease == nil {
		return ErrExecutionAuthorCustody
	}
	for _, root := range custody.roots {
		if root.file == nil {
			return ErrExecutionAuthorCustody
		}
		held, err := root.file.Stat()
		current, pathErr := os.Lstat(root.path)
		volume, volumeErr := inputCustodyVolume(root.file)
		canonical, canonicalErr := filepath.EvalSymlinks(root.path)
		if err != nil || pathErr != nil || volumeErr != nil || canonicalErr != nil || canonical != root.path ||
			!os.SameFile(root.info, held) || !os.SameFile(held, current) || !inputCustodyOwned(current) ||
			!current.IsDir() || current.Mode().Perm() != 0o700 || volume != root.volume {
			return ErrExecutionAuthorCustody
		}
	}
	held, err := custody.lease.Stat()
	current, pathErr := os.Lstat(filepath.Join(custody.parent, productionSourceLeaseName))
	if err != nil || pathErr != nil || !inputCustodySame(custody.leaseInfo, held) || !inputCustodySame(held, current) {
		return ErrExecutionAuthorCustody
	}
	return nil
}

func (custody *ExecutionAuthorCustody) check(ctx context.Context) error {
	if custody.checkRoots(ctx) != nil {
		return ErrExecutionAuthorCustody
	}
	if path, err := custody.request.Plan.Check(ctx, "plan"); err != nil || path != custody.planPath {
		return ErrExecutionAuthorCustody
	}
	if _, path, err := custody.request.Author.Check(ctx, "t422-author"); err != nil || path != custody.authorPath {
		return ErrExecutionAuthorCustody
	}
	if _, path, err := custody.request.Git.Check(ctx); err != nil || path != custody.gitPath {
		return ErrExecutionAuthorCustody
	}
	// A concurrent reference build may hold this mutex for its complete build.
	// Do not turn a constant-cost dispatch check into an unbounded lock wait.
	if !custody.request.Builds.mu.TryLock() {
		return ErrExecutionAuthorCustody
	}
	defer custody.request.Builds.mu.Unlock()
	if custody.request.Builds.closed || custody.request.Builds.err != nil || ctx.Err() != nil {
		return ErrExecutionAuthorCustody
	}
	return nil
}

func (custody *ExecutionAuthorCustody) checkSource(ctx context.Context, response *ExecutionCorpusAuthorResponse) error {
	root := custody.roots[1]
	author := ExecutionCorpusAuthor{directory: root.path, root: root.file, rootInfo: root.info, volume: root.volume}
	if response != nil {
		author.config = response.ConfigSHA256
		return author.checkContinuity(ctx, response.Result.Commit)
	}
	file, err := t4013.OpenHostImage(root.path)
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

// Directory returns private source custody, not public evidence. The other
// two retained directories are available only through PrivateDirectories.
func (custody *ExecutionAuthorCustody) Directory() string {
	if custody == nil || len(custody.roots) < 2 {
		return ""
	}
	return custody.roots[1].path
}

func (custody *ExecutionAuthorCustody) PrivateDirectories() []string {
	if custody == nil || len(custody.roots) < 2 {
		return nil
	}
	result := make([]string, 0, len(custody.roots)-1)
	for _, root := range custody.roots[1:] {
		result = append(result, root.path)
	}
	return result
}

func (custody *ExecutionAuthorCustody) Close() error {
	if custody == nil {
		return nil
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if custody.active {
		return ErrExecutionAuthorCustody
	}
	if !custody.closed {
		custody.closed = true
		for _, root := range custody.roots {
			if root.file != nil && root.file.Close() != nil {
				custody.err = ErrExecutionAuthorCustody
			}
		}
		if custody.lease != nil && custody.lease.Close() != nil {
			custody.err = ErrExecutionAuthorCustody
		}
	}
	return custody.err
}

func authorCustodyCanonicalResponse(raw []byte, expected AuthoredExecutionRevision) (ExecutionCorpusAuthorResponse, error) {
	var response ExecutionCorpusAuthorResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > MaxExecutionCorpusAuthorResponseBytes || decoder.Decode(&response) != nil || !corpusAuthorJSONEOF(decoder) ||
		response.Result != expected || !validExecutionSHA256(response.ConfigSHA256) {
		return response, ErrExecutionAuthorCustody
	}
	canonical, err := corpusAuthorCanonical(response, MaxExecutionCorpusAuthorResponseBytes)
	if err != nil || !bytes.Equal(raw, canonical) {
		return response, ErrExecutionAuthorCustody
	}
	return response, nil
}
