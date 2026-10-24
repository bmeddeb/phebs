//go:build linux

package t421

import (
	"context"
	"debug/buildinfo"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"time"
)

// LinuxGoReferenceHelper is independently rebuilt and sealed before borrowing.
// Close it only after each borrowing Go owner has completed Close.
type LinuxGoReferenceHelper struct {
	mu     sync.Mutex
	tool   *LinuxExecutionToolCustody
	closed bool
	layout *linuxPressureAuthorView
}

func ProtectLinuxGoReferenceHelper(ctx context.Context, request ReferenceToolRequest) (*LinuxGoReferenceHelper, error) {
	return protectLinuxGoReferenceHelperObserved(ctx, request, nil)
}

func protectLinuxGoReferenceHelperObserved(ctx context.Context, request ReferenceToolRequest, observation *executionPreworkObservation) (*LinuxGoReferenceHelper, error) {
	request.Role, request.PlanSchema = linuxGoReferenceRole, linuxGoReferenceRecipe
	tool, err := protectLinuxExecutionReferenceToolObserved(ctx, request, observation)
	if tool == nil {
		return nil, ErrExecutionGoBuildCustody
	}
	helper := &LinuxGoReferenceHelper{tool: tool}
	if err != nil {
		return helper, ErrExecutionGoBuildCustody
	}
	return helper, nil
}

func (helper *LinuxGoReferenceHelper) Close() error {
	if helper == nil || helper.tool == nil {
		return ErrExecutionGoBuildCustody
	}
	helper.mu.Lock()
	defer helper.mu.Unlock()
	helper.closed = true
	if helper.tool.Close() != nil {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

type linuxGoReferenceAttempt struct {
	done          chan struct{}
	cancel        context.CancelFunc
	directory     string
	stage         *os.Root
	stageInfo     os.FileInfo
	workInfo      os.FileInfo
	outputs       *linuxGoBuildStaging
	tree          *LinuxExecutionInputTree // Cleanup only; no H2k publication.
	tool          *LinuxExecutionToolCustody
	plan          linuxGoReferencePlan
	input         LinuxInputTreeIdentity
	goDigest      string
	digest        string
	step          string
	stderr        []byte
	commandStderr []byte
	waitError     string
	err           error
	released      bool
	observation   *executionPreworkObservation
}

// Retain one bounded completed join record after its private workspace is gone.
// These observations are preparation evidence, never executor admission.
type linuxGoReferenceCompletion struct {
	pid           int
	joined, empty bool
	input         LinuxInputTreeIdentity
	goDigest      string
	toolDigest    string
}

// ProtectReferenceTool verifies a sealed candidate with the fixed namespace
// recipe. Neither this method nor its helper ever executes the candidate. A
// failed/uncertain attempt stays in this owner; Close must finish before disposal.
func (custody *LinuxExecutionGoBuildCustody) ProtectReferenceTool(ctx context.Context, parent, role, binary string, helper *LinuxGoReferenceHelper) (*LinuxExecutionToolCustody, error) {
	return custody.protectReferenceTool(ctx, parent, role, binary, "", helper)
}

func (custody *LinuxExecutionGoBuildCustody) ProtectReferenceToolV3(ctx context.Context, parent, role, binary string, helper *LinuxGoReferenceHelper) (*LinuxExecutionToolCustody, error) {
	return custody.protectReferenceTool(ctx, parent, role, binary, PlanV3Schema, helper)
}

func (custody *LinuxExecutionGoBuildCustody) protectReferenceTool(ctx context.Context, parent, role, binary, schema string, helper *LinuxGoReferenceHelper) (*LinuxExecutionToolCustody, error) {
	return custody.protectReferenceCandidate(ctx, parent, role, schema, helper, func() (*LinuxExecutionToolCustody, error) {
		return protectLinuxExecutionToolObserved(ctx, role, binary, custody.observation)
	})
}

// Only the finite executor prework consumer admits an inherited candidate. The
// public path constructors keep their canonical-path contract unchanged.
func (custody *LinuxExecutionGoBuildCustody) protectExecutorReferenceFD(ctx context.Context, parent string, image *os.File, helper *LinuxGoReferenceHelper) (*LinuxExecutionToolCustody, error) {
	return custody.protectReferenceCandidate(ctx, parent, "t422-execute", "", helper, func() (*LinuxExecutionToolCustody, error) {
		return protectLinuxExecutorCandidateFDObserved(ctx, image, custody.observation)
	})
}

func (custody *LinuxExecutionGoBuildCustody) protectReferenceCandidate(ctx context.Context, parent, role, schema string, helper *LinuxGoReferenceHelper, candidate func() (*LinuxExecutionToolCustody, error)) (*LinuxExecutionToolCustody, error) {
	if custody == nil || ctx == nil || ctx.Err() != nil || helper == nil || helper.tool == nil || parent != filepath.Dir(custody.directory) {
		return nil, ErrExecutionGoBuildCustody
	}
	if _, _, _, _, _, err := referenceToolRoleForSchema(role, schema, custody.reference.source); err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	custody.mu.Lock()
	if custody.closing || custody.err != nil || custody.active != nil || custody.tree == nil || custody.checkStage() != nil {
		custody.mu.Unlock()
		return nil, ErrExecutionGoBuildCustody
	}
	input, err := custody.tree.Check(ctx)
	if err != nil || !helper.mu.TryLock() {
		custody.mu.Unlock()
		return nil, ErrExecutionGoBuildCustody
	}
	if helper.closed {
		helper.mu.Unlock()
		custody.mu.Unlock()
		return nil, ErrExecutionGoBuildCustody
	}
	tool, err := candidate()
	if err != nil {
		helper.mu.Unlock()
		custody.mu.Unlock()
		return nil, ErrExecutionGoBuildCustody
	}
	buildCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	attempt := &linuxGoReferenceAttempt{done: make(chan struct{}), cancel: cancel, tool: tool, input: input, observation: custody.observation,
		plan: linuxGoReferencePlan{Role: role, Schema: schema, Source: custody.reference.source, Tree: custody.reference.tree,
			ConfigSHA256: custody.reference.configSHA256, OriginalSDK: custody.request.GoRoot,
			OriginalModules: custody.request.ModuleCache, OriginalSource: custody.request.RepositoryRoot}}
	custody.active = attempt
	prepared := custody.prepareGoReferenceAttempt(attempt) == nil
	if !prepared {
		attempt.err = ErrExecutionGoBuildCustody
		helper.mu.Unlock()
		completeInputTreeConstruction(attempt.done, attempt.cancel)
	}
	custody.mu.Unlock()
	if prepared {
		go custody.runGoReferenceAttempt(buildCtx, attempt, helper)
	}
	select {
	case <-attempt.done:
	case <-buildCtx.Done():
		select {
		case <-attempt.done:
		default:
			return nil, ErrExecutionGoBuildCustody
		}
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if custody.closing || custody.err != nil || attempt.err != nil || ctx.Err() != nil || custody.checkStage() != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	identity, goIdentity, err := custody.compareGoReference(ctx, attempt)
	if err != nil {
		attempt.err, attempt.step = ErrExecutionGoBuildCustody, "parent-candidate-compare"
		return nil, ErrExecutionGoBuildCustody
	}
	if custody.closeGoReferenceAttempt(ctx, attempt, true) != nil || custody.err != nil {
		attempt.err, attempt.step = ErrExecutionGoBuildCustody, "parent-cleanup"
		return nil, ErrExecutionGoBuildCustody
	}
	tool = attempt.tool
	tool.identity = identity
	tool.referenceInputs = custody
	if role == "t422-execute" {
		tool.executorClaim = &linuxExecutorPreworkClaim{tool: tool, builds: custody}
	}
	custody.goIdentity = goIdentity
	custody.last = linuxGoReferenceCompletion{pid: attempt.tree.pid, joined: attempt.tree.joined, empty: attempt.tree.empty,
		input: attempt.input, goDigest: attempt.goDigest, toolDigest: attempt.digest}
	attempt.tool = nil // Transfer only after every borrowed consumer and cleanup.
	custody.active = nil
	return tool, nil
}

func (custody *LinuxExecutionGoBuildCustody) compareGoReference(ctx context.Context, attempt *linuxGoReferenceAttempt) (ExecutionToolIdentity, ExecutionToolIdentity, error) {
	var identity, goIdentity ExecutionToolIdentity
	packagePath, modulePath, version, sum, recipe, err := referenceToolRoleForSchema(attempt.plan.Role, attempt.plan.Schema, attempt.plan.Source)
	if err != nil || attempt.tree == nil || !attempt.tree.joined || !attempt.tree.empty || !validExecutionSHA256(attempt.goDigest) || !validExecutionSHA256(attempt.digest) {
		return identity, goIdentity, ErrExecutionGoBuildCustody
	}
	err = attempt.tool.input.WithInput(ctx, attempt.plan.Role, func(candidate *os.File) (retErr error) {
		output, err := attempt.stage.Open("work/reference")
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		defer func() {
			if custody.observation.close(output.Close()) != nil {
				retErr = ErrExecutionGoBuildCustody
			}
		}()
		supplied, suppliedErr := buildinfo.Read(candidate)
		actual, actualErr := buildinfo.Read(output)
		if suppliedErr != nil || actualErr != nil || !reflect.DeepEqual(supplied, actual) ||
			validateReferenceBuildInfoForSchema(actual, attempt.plan.Role, attempt.plan.Schema, packagePath, attempt.plan.Source, modulePath, version, sum, nil) != nil {
			return ErrExecutionGoBuildCustody
		}
		candidateDigest, candidateErr := digestExecutionReferenceFD(ctx, candidate)
		outputDigest, outputErr := compareGoReferenceOutput(ctx, candidate, output)
		if candidateErr != nil || outputErr != nil || candidateDigest != attempt.digest || outputDigest != candidateDigest {
			return ErrExecutionGoBuildCustody
		}
		return nil
	})
	if err != nil || ctx.Err() != nil {
		return identity, goIdentity, ErrExecutionGoBuildCustody
	}
	identity = ExecutionToolIdentity{Role: attempt.plan.Role, FileType: regularFileType, SHA256: attempt.digest,
		Version: "clean commit " + attempt.plan.Source, Provenance: "go-build-info-vcs-v1", BuildVCSRevision: attempt.plan.Source}
	if modulePath != "" {
		identity.Version, identity.Provenance, identity.BuildVCSRevision = version, "go-module-build-v1", ""
		identity.ModulePath, identity.ModuleVersion, identity.ModuleSum, identity.BuildRecipeSHA256 = modulePath, version, sum, recipe
		if attempt.plan.Role == "zoekt-git-index" && processAccountingPlanSemantics(attempt.plan.Schema) {
			identity.Provenance = zoektOfferProvenance
		}
	}
	goIdentity = ExecutionToolIdentity{Role: "go", FileType: regularFileType, SHA256: attempt.goDigest,
		Version: "go version " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH, Provenance: "external-executed-file-v1"}
	return identity, goIdentity, nil
}

func goReferenceCensusDriver(census inputTreeCensus) string {
	for _, entry := range census.rows {
		if entry.name == "inputs/sdk/bin/go" && entry.mode == 0o500 {
			return "sha256:" + hex.EncodeToString(entry.digest[:])
		}
	}
	return ""
}

// Called under the owner mutex before a worker can borrow any prefix.
func (custody *LinuxExecutionGoBuildCustody) prepareGoReferenceAttempt(attempt *linuxGoReferenceAttempt) error {
	var err error
	attempt.directory, err = os.MkdirTemp(custody.directory, "build-")
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	attempt.stage, err = os.OpenRoot(attempt.directory)
	if err == nil {
		attempt.stageInfo, err = attempt.stage.Stat(".")
	}
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	custody.children[filepath.Base(attempt.directory)] = attempt.stageInfo
	for _, name := range []string{"mount", "work", "work/home", "work/tmp", "work/cache"} {
		if attempt.stage.Mkdir(name, 0o700) != nil {
			return ErrExecutionGoBuildCustody
		}
	}
	attempt.workInfo, err = attempt.stage.Stat("work")
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	tree := &LinuxExecutionInputTree{stagePath: filepath.Join(attempt.directory, "mount"), done: attempt.done, cancel: attempt.cancel, observation: custody.observation}
	attempt.tree = tree
	tree.stage, err = os.Open(tree.stagePath)
	if err == nil {
		tree.stageInfo, err = tree.stage.Stat()
	}
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

// CheckGo publishes only the actual joined reference-build driver's identity.
func (custody *LinuxExecutionGoBuildCustody) CheckGo(ctx context.Context) (ExecutionToolIdentity, error) {
	if custody == nil {
		return ExecutionToolIdentity{}, ErrExecutionGoBuildCustody
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || custody.closing || custody.err != nil || custody.active != nil || custody.tree == nil || custody.goIdentity.Role != "go" {
		return ExecutionToolIdentity{}, ErrExecutionGoBuildCustody
	}
	if _, err := custody.tree.Check(ctx); err != nil {
		return ExecutionToolIdentity{}, ErrExecutionGoBuildCustody
	}
	return custody.goIdentity, nil
}

func (attempt *linuxGoReferenceAttempt) checkStage(custody *LinuxExecutionGoBuildCustody) error {
	if custody.checkStage() != nil || attempt.stage == nil || attempt.stageInfo == nil || !linuxGitPrivateDirectory(attempt.directory) || filepath.Dir(attempt.directory) != custody.directory {
		return ErrExecutionGoBuildCustody
	}
	held, err := attempt.stage.Stat(".")
	linked, pathErr := custody.stage.Lstat(filepath.Base(attempt.directory))
	if err != nil || pathErr != nil || !os.SameFile(attempt.stageInfo, held) || !os.SameFile(held, linked) {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

func (custody *LinuxExecutionGoBuildCustody) closeGoReferenceAttempt(ctx context.Context, attempt *linuxGoReferenceAttempt, keepCandidate bool) error {
	select {
	case <-attempt.done:
	case <-ctx.Done():
		return ErrExecutionGoBuildCustody
	}
	if attempt.tree != nil && attempt.tree.pid != 0 && (!attempt.tree.joined || !attempt.tree.empty) {
		return ErrExecutionGoBuildCustody
	}
	if !keepCandidate && attempt.tool != nil {
		if attempt.tool.Close() != nil {
			custody.err = ErrExecutionGoBuildCustody
		}
		if attempt.tool.input != nil {
			attempt.tool.input.mu.Lock()
			if attempt.tool.input.closeErr != nil {
				custody.closeErr = ErrExecutionGoBuildCustody
			}
			attempt.tool.input.mu.Unlock()
		}
		if attempt.tool.input == nil || !attempt.tool.input.closed {
			return ErrExecutionGoBuildCustody
		}
		attempt.tool = nil
	}
	if attempt.released {
		return nil
	}
	if attempt.checkStage(custody) != nil {
		return ErrExecutionGoBuildCustody
	}
	if attempt.tree != nil {
		if attempt.tree.Close(ctx) != nil {
			custody.err = ErrExecutionGoBuildCustody
		}
		attempt.tree.mu.Lock()
		if attempt.tree.closeErr != nil {
			custody.closeErr = ErrExecutionGoBuildCustody
		}
		attempt.tree.mu.Unlock()
		if !attempt.tree.closed {
			return ErrExecutionGoBuildCustody
		}
	}
	if attempt.outputs == nil {
		var err error
		attempt.outputs, err = observeGoReferenceOutputsObserved(ctx, filepath.Join(attempt.directory, "work"), attempt.workInfo, custody.observation)
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
	}
	if attempt.checkStage(custody) != nil || attempt.outputs.removeContents(ctx) != nil || custody.observation.close(attempt.outputs.tree.Close()) != nil {
		return ErrExecutionGoBuildCustody
	}
	attempt.outputs = nil
	work, err := attempt.stage.Lstat("work")
	if err != nil || !os.SameFile(attempt.workInfo, work) || work.Mode().Perm() != 0o700 || attempt.stage.Remove("work") != nil ||
		attempt.checkStage(custody) != nil || custody.removeChild(filepath.Base(attempt.directory)) != nil || custody.observation.close(attempt.stage.Close()) != nil {
		return ErrExecutionGoBuildCustody
	}
	attempt.released = true
	if keepCandidate && custody.err != nil {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

// Output accounting is observed only after actual helper/descendant joins. This
// bounded cleanup census is not a quota or prospective execution allowance.
func observeGoReferenceOutputs(ctx context.Context, directory string, expected os.FileInfo) (*linuxGoBuildStaging, error) {
	return observeGoReferenceOutputsObserved(ctx, directory, expected, nil)
}

func observeGoReferenceOutputsObserved(ctx context.Context, directory string, expected os.FileInfo, observation *executionPreworkObservation) (*linuxGoBuildStaging, error) {
	if ctx == nil || ctx.Err() != nil || expected == nil || !linuxGitPrivateDirectory(directory) {
		return nil, ErrExecutionGoBuildCustody
	}
	tree, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	info, err := tree.Stat(".")
	if err != nil || !os.SameFile(expected, info) {
		_ = observation.close(tree.Close())
		return nil, ErrExecutionGoBuildCustody
	}
	staging := &linuxGoBuildStaging{directory: directory, tree: tree, names: map[string]os.FileInfo{".": info},
		pathBytes: len("inputs"), inventory: ExecutionGoBuildInventory{Entries: 1}, observation: observation}
	err = walkGoBuildTree(ctx, tree, ".", func(path string, current os.FileInfo) error {
		if path == "." {
			return nil
		}
		if staging.addName(path) != nil || !current.IsDir() && staging.reserveFile(current.Size()) != nil {
			return staging.refuse()
		}
		staging.names[path] = current
		return nil
	})
	if err != nil {
		staging.err = ErrExecutionGoBuildCustody
		return staging, staging.err
	}
	return staging, nil
}
