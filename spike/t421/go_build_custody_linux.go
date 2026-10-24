//go:build linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// LinuxExecutionGoBuildRequest selects actual offline input bytes. Git and the
// constructor helper remain borrowed resources with their own joined owners.
type LinuxExecutionGoBuildRequest struct {
	Git                  *LinuxExecutionGitCustody
	RepositoryRoot       string
	PlanSourceCommit     string
	IntegratedMainCommit string
	SourceCommit         string
	GoRoot               string
	ModuleCache          string
}

// LinuxExecutionGoBuildCustody owns one H2k read-only input tree, distinct from
// the historical Darwin path owner. It exposes no SDK path or arbitrary runner.
// Input custody alone grants no reference identity or aggregate admission.
type LinuxExecutionGoBuildCustody struct {
	mu              sync.Mutex
	directory       string
	stage           *os.Root
	stageInfo       os.FileInfo
	children        map[string]os.FileInfo
	staging         *linuxGoBuildStaging
	transport       *linuxCheckoutGit
	tree            *LinuxExecutionInputTree
	reference       executionReferenceSource
	commits         ExecutionCommits
	request         LinuxExecutionGoBuildRequest
	inventory       ExecutionGoBuildInventory
	active          *linuxGoReferenceAttempt
	goIdentity      ExecutionToolIdentity
	last            linuxGoReferenceCompletion
	preparationStep string
	closing         bool
	released        bool
	err             error
	closeErr        error // Propagated native close failure before child owner release.
	observation     *executionPreworkObservation
}

// ProtectLinuxExecutionGoBuildInputs reconstructs exact clean source with H2p
// Git, selects checksum-authorized module bytes, copies the fixed SDK selections
// and seven verified Git aliases, then constructs a single bounded H2k tree.
// Mutable preparation and protected copies coexist and are charged separately.
// Originals are observed, never renamed or changed on the host.
//
// Construction is serial with a cooperative thirty-minute deadline. After stage
// creation every failure returns retained custody. Close must join preparation
// and the H2k constructor before callers may dispose any retained private prefix.
func ProtectLinuxExecutionGoBuildInputs(ctx context.Context, parent string, request LinuxExecutionGoBuildRequest, helper *LinuxInputTreeHelper) (*LinuxExecutionGoBuildCustody, error) {
	return protectLinuxExecutionGoBuildInputsObserved(ctx, parent, request, helper, nil)
}

func protectLinuxExecutionGoBuildInputsObserved(ctx context.Context, parent string, request LinuxExecutionGoBuildRequest, helper *LinuxInputTreeHelper, observation *executionPreworkObservation) (*LinuxExecutionGoBuildCustody, error) {
	if runtime.GOARCH != "amd64" || ctx == nil || ctx.Err() != nil || !linuxGitPrivateDirectory(parent) || request.Git == nil || helper == nil || helper.tool == nil {
		return nil, ErrExecutionGoBuildCustody
	}
	for _, path := range []string{request.RepositoryRoot, request.GoRoot, request.ModuleCache} {
		canonical, err := filepath.EvalSymlinks(path)
		if !executionGitAbsolutePath(path) || err != nil || canonical != path || inputTreePathsOverlap(parent, path) {
			return nil, ErrExecutionGoBuildCustody
		}
	}
	for _, commit := range []string{request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit} {
		if !validCommit(commit) {
			return nil, ErrExecutionGoBuildCustody
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	directory, err := os.MkdirTemp(parent, "t42-linux-go-")
	if err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	custody := &LinuxExecutionGoBuildCustody{directory: directory, children: make(map[string]os.FileInfo), request: request, preparationStep: "stage", observation: observation}
	fail := func() (*LinuxExecutionGoBuildCustody, error) {
		if custody.staging != nil {
			custody.inventory = custody.staging.inventory
		}
		custody.err = ErrExecutionGoBuildCustody
		return custody, custody.err
	}
	custody.stage, err = os.OpenRoot(directory)
	if err == nil {
		custody.stageInfo, err = custody.stage.Stat(".")
	}
	if err != nil {
		return fail()
	}
	for _, name := range []string{"payload", "home", "tmp", "scratch"} {
		if custody.stage.Mkdir(name, 0o700) != nil {
			return fail()
		}
		custody.children[name], err = custody.stage.Stat(name)
		if err != nil {
			return fail()
		}
	}
	custody.preparationStep = "mutable-inputs"
	custody.staging, err = newLinuxGoBuildStagingObserved(filepath.Join(directory, "payload"), observation)
	if err != nil {
		return fail()
	}
	custody.preparationStep = "git-transport"
	custody.transport, err = newLinuxCheckoutGit(ctx, request.Git, filepath.Join(directory, "home"), filepath.Join(directory, "tmp"))
	if err != nil {
		return fail()
	}
	origin := executionCheckoutInspector{root: request.RepositoryRoot, digest: custody.transport.digest, transport: custody.transport}
	custody.preparationStep = "checkout"
	custody.commits, err = inspectExecutionCheckout(ctx, origin, request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit)
	if err != nil {
		return fail()
	}
	custody.preparationStep = "source"
	custody.reference, err = custody.staging.prepareSource(ctx, origin, request.SourceCommit, filepath.Join(directory, "payload", "source"))
	if err != nil {
		return fail()
	}
	for _, selection := range []string{"bin/go", "src", "pkg", "lib", "go.env", "VERSION"} {
		custody.preparationStep = "sdk-" + selection
		if custody.staging.copyTree(ctx, request.GoRoot, selection, filepath.Join("sdk", selection)) != nil {
			return fail()
		}
	}
	custody.preparationStep = "modules"
	if custody.staging.prepareModules(ctx, request.ModuleCache) != nil {
		return fail()
	}
	custody.preparationStep = "git"
	if custody.staging.prepareGit(ctx, request.Git, filepath.Join(directory, "home"), filepath.Join(directory, "tmp")) != nil {
		return fail()
	}
	custody.preparationStep = "source-recheck"
	if custody.reference.verify(ctx) != nil {
		return fail()
	}
	custody.preparationStep = "checkout-recheck"
	after, err := inspectExecutionCheckout(ctx, origin, request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit)
	if err != nil || after != custody.commits {
		return fail()
	}
	// No preparation command may outlive the source/workspace it borrowed.
	if custody.transport.close(ctx) != nil || custody.checkStage() != nil {
		return fail()
	}
	custody.preparationStep = "protected-tree"
	custody.tree, err = protectLinuxExecutionInputTreeObserved(ctx, filepath.Join(directory, "scratch"),
		[]LinuxInputTreeSource{{Name: "inputs", Path: filepath.Join(directory, "payload")}}, helper, observation)
	if err != nil {
		return fail()
	}
	identity, err := custody.tree.Check(ctx)
	custody.preparationStep = "inventory-retirement"
	custody.inventory = custody.staging.inventory
	if err != nil || identity.Entries != uint64(custody.inventory.Entries) || identity.Files != uint64(custody.inventory.Files) ||
		identity.Bytes != uint64(custody.inventory.Bytes) || identity.PathBytes != uint64(custody.staging.pathBytes) ||
		custody.checkStage() != nil || custody.staging.removeContents(ctx) != nil || observation.close(custody.staging.tree.Close()) != nil {
		return fail()
	}
	custody.staging = nil
	if custody.removeChild("payload") != nil || ctx.Err() != nil {
		return fail()
	}
	custody.preparationStep = "ready"
	return custody, nil
}

// PrivateDirectory identifies only exact cleanup custody, never an SDK path.
func (custody *LinuxExecutionGoBuildCustody) PrivateDirectory() string {
	if custody == nil {
		return ""
	}
	return custody.directory
}

func (custody *LinuxExecutionGoBuildCustody) Inventory() ExecutionGoBuildInventory {
	if custody == nil {
		return ExecutionGoBuildInventory{}
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	return custody.inventory
}

// Check inspects the kernel-held tree object, not original paths or payloads.
func (custody *LinuxExecutionGoBuildCustody) Check(ctx context.Context) error {
	if custody == nil {
		return ErrExecutionGoBuildCustody
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || custody.closing || custody.err != nil || custody.tree == nil {
		custody.err = ErrExecutionGoBuildCustody
		return custody.err
	}
	if custody.active != nil {
		return ErrExecutionGoBuildCustody
	}
	if _, err := custody.tree.Check(ctx); err != nil {
		custody.err = ErrExecutionGoBuildCustody
	}
	return custody.err
}

func (custody *LinuxExecutionGoBuildCustody) removeChild(name string) error {
	before, exists := custody.children[name]
	if !exists || before == nil || custody.checkStage() != nil {
		return ErrExecutionGoBuildCustody
	}
	current, childErr := custody.stage.Lstat(name)
	if childErr != nil || !current.IsDir() || !os.SameFile(before, current) || current.Mode().Perm() != 0o700 || !inputCustodyCopyOwned(current) || custody.stage.Remove(name) != nil {
		return ErrExecutionGoBuildCustody
	}
	delete(custody.children, name)
	return nil
}

func (custody *LinuxExecutionGoBuildCustody) checkStage() error {
	if custody.stage == nil || custody.stageInfo == nil || !linuxGitPrivateDirectory(custody.directory) {
		return ErrExecutionGoBuildCustody
	}
	held, err := custody.stage.Stat(".")
	linked, pathErr := os.Lstat(custody.directory)
	if err != nil || pathErr != nil || !os.SameFile(custody.stageInfo, held) || !os.SameFile(held, linked) {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

// Close joins all retained constructors before nonrecursive exact cleanup. A
// timeout, unknown entry or incomplete source reservation retains the prefix.
// Borrowed Git/helper owners are never closed here.
func (custody *LinuxExecutionGoBuildCustody) Close(ctx context.Context) error {
	if custody == nil || ctx == nil {
		return ErrExecutionGoBuildCustody
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	custody.closing = true
	if custody.released {
		return custody.err
	}
	if custody.active != nil {
		custody.active.cancel()
		if custody.closeGoReferenceAttempt(ctx, custody.active, false) != nil {
			custody.err = ErrExecutionGoBuildCustody
			return custody.err
		}
		custody.active = nil
	}
	if custody.transport.close(ctx) != nil {
		custody.err = ErrExecutionGoBuildCustody
		return custody.err
	}
	if custody.checkStage() != nil {
		custody.err = ErrExecutionGoBuildCustody
		return custody.err
	}
	if custody.tree != nil {
		if custody.tree.Close(ctx) != nil {
			custody.err = ErrExecutionGoBuildCustody
		}
		custody.tree.mu.Lock()
		if custody.tree.closeErr != nil {
			custody.closeErr = ErrExecutionGoBuildCustody
		}
		custody.tree.mu.Unlock()
		if !custody.tree.closed {
			return ErrExecutionGoBuildCustody
		}
	}
	if custody.staging != nil {
		if custody.checkStage() != nil || custody.staging.removeContents(ctx) != nil || custody.observation.close(custody.staging.tree.Close()) != nil {
			custody.err = ErrExecutionGoBuildCustody
			return custody.err
		}
		custody.staging = nil
	}
	for _, name := range []string{"payload", "home", "tmp", "scratch"} {
		if _, exists := custody.children[name]; exists && (ctx.Err() != nil || custody.removeChild(name) != nil) {
			custody.err = ErrExecutionGoBuildCustody
			return custody.err
		}
	}
	if ctx.Err() != nil || custody.checkStage() != nil || os.Remove(custody.directory) != nil || custody.observation.close(custody.stage.Close()) != nil {
		custody.err = ErrExecutionGoBuildCustody
		return custody.err
	}
	custody.released = true
	return custody.err
}
