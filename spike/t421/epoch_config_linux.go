//go:build linux

package t421

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// LinuxExecutionEpochConfigCustody owns eight read-only inputs, four writable
// native roots and five ordered listener reservations. Private descriptor routes
// remain valid only while this owner stays open. It borrows Author and its lease;
// it issues neither server execution nor reference-tool authority. Never copy it.
type LinuxExecutionEpochConfigCustody struct {
	mu                        sync.Mutex
	author                    *LinuxExecutionAuthorCustody
	authorInputs              linuxEpochAuthorInputs
	parent, stage             linuxAuthorRoot
	roots                     []linuxAuthorRoot
	catalogs, configs         *LinuxExecutionInputTree
	catalogStage, configStage *linuxEpochInputStage
	constructors              linuxAuthorRoot
	epochs                    [5]ExecutionEpochConfig
	listeners                 [5]net.Listener
	parsedConfigs             [5]*config.Config
	queryCatalog              *servicecatalog.Catalog
	logicalChanges            *epochPreparedLogicalChanges
	released                  uint64
	borrowedBy                *LinuxExecutionEpochFlow
	active                    bool // Only a joined native owner may clear an actual use.
	closing, cleaning, closed bool
	preparationStep           string
	err                       error
}

// PrepareLinuxExecutionEpochConfigs borrows the genuine idle author and rereads
// its already-admitted sealed plan. Logical generation is bounded; this path
// never calls DecodePlan or regenerates the physical corpus. Two fixed H2k
// constructors are the only children. Failed preparation returns retained owners.
func PrepareLinuxExecutionEpochConfigs(ctx context.Context, parent string, author *LinuxExecutionAuthorCustody, helper *LinuxInputTreeHelper) (*LinuxExecutionEpochConfigCustody, error) {
	if ctx == nil || ctx.Err() != nil || author == nil || helper == nil || helper.tool == nil || !linuxGitPrivateDirectory(parent) {
		return nil, ErrExecutionEpochConfigs
	}
	author.mu.Lock()
	defer author.mu.Unlock()
	if author.borrowedBy != nil || author.closing || author.released || author.err != nil || author.active != nil || author.request.Builds == nil || author.request.Author == nil || author.request.Plan == nil || author.checkRoots(ctx) != nil || author.checkSource(ctx, author.previous) != nil {
		return nil, ErrExecutionEpochConfigs
	}
	var authorInputs linuxEpochAuthorInputs
	var identity ExecutionToolIdentity
	err := author.request.Author.WithImage(ctx, "t422-author", func(value ExecutionToolIdentity, _ *os.File) error {
		identity = value
		authorInputs.image = author.request.Author.input.inputs["t422-author"]
		return nil
	})
	if err != nil || identity != author.authorIdentity {
		return nil, ErrExecutionEpochConfigs
	}
	author.request.Builds.mu.Lock()
	bound := linuxAuthorBuildBinding(author.request, identity, author.request.Builds.request.PlanSourceCommit)
	author.request.Builds.mu.Unlock()
	if !bound {
		return nil, ErrExecutionEpochConfigs
	}
	var raw []byte
	err = author.request.Plan.WithInput(ctx, "plan", func(file *os.File) error {
		authorInputs.plan = author.request.Plan.inputs["plan"]
		info, e := file.Stat()
		if e != nil || info.Size() <= 0 || info.Size() > MaxPlanBytes || info.Mode().Perm() != 0o400 {
			return ErrExecutionEpochConfigs
		}
		raw, e = io.ReadAll(executionInputReader{ctx, io.LimitReader(file, MaxPlanBytes+1)})
		return e
	})
	var plan Plan
	if err != nil || len(raw) > MaxPlanBytes || SHA256(raw) != author.planSHA256 || json.Unmarshal(raw, &plan) != nil || !processAccountingPlanSemantics(plan.Schema) || validatePlan(plan, &plan.Revisions) != nil || plan.SourceCommit != author.request.Builds.request.PlanSourceCommit || ctx.Err() != nil {
		return nil, ErrExecutionEpochConfigs
	}
	return prepareLinuxEpochConfigs(ctx, parent, author, plan, helper, authorInputs)
}

// The private fixed constructor is also exercised with a mechanical native
// fixture; that fixture does not acquire the public issuer's author authority.
func prepareLinuxEpochConfigs(ctx context.Context, parent string, author *LinuxExecutionAuthorCustody, plan Plan, helper *LinuxInputTreeHelper, authorInputs linuxEpochAuthorInputs) (_ *LinuxExecutionEpochConfigCustody, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	owner := &LinuxExecutionEpochConfigCustody{author: author, authorInputs: authorInputs, preparationStep: "parent"}
	fail := func() (*LinuxExecutionEpochConfigCustody, error) {
		owner.err = ErrExecutionEpochConfigs
		owner.parsedConfigs = [5]*config.Config{}
		owner.queryCatalog = nil
		owner.logicalChanges = nil
		return owner, owner.err
	}
	var err error
	owner.parent, err = openLinuxAuthorOwnedRoot(parent)
	if err != nil {
		return fail()
	}
	if authorInputs.check() != nil || authorInputs.plan.identity.SHA256 != author.planSHA256 || author.checkRoots(ctx) != nil || inputTreePathsOverlap(parent, author.roots[0].path) || !linuxEpochSameVolume(owner.parent.identity, author.roots[0].identity) {
		return fail()
	}
	directory, err := os.MkdirTemp(parent, "t422-linux-epochs-")
	if err != nil {
		return fail()
	}
	owner.stage = linuxAuthorRoot{path: directory}
	owner.stage, err = openLinuxAuthorOwnedRoot(directory)
	if err != nil {
		return fail()
	}
	owner.preparationStep = "staging"
	for _, name := range []string{"catalogs", "configs", "constructors", "data", "home", "tmp", "backup"} {
		if ctx.Err() != nil || owner.checkStage() != nil || os.Mkdir(filepath.Join(directory, name), 0o700) != nil {
			return fail()
		}
		root, e := openLinuxAuthorOwnedRoot(filepath.Join(directory, name))
		if e != nil {
			return fail()
		}
		switch name {
		case "catalogs":
			owner.catalogStage = &linuxEpochInputStage{root: root, files: map[string]os.FileInfo{}}
		case "configs":
			owner.configStage = &linuxEpochInputStage{root: root, files: map[string]os.FileInfo{}}
		case "constructors":
			owner.constructors = root
		default:
			owner.roots = append(owner.roots, root)
		}
	}
	owner.preparationStep = "catalogs"
	catalogs, changes, err := epochCatalogInputsObserved(ctx, plan)
	if err != nil || len(catalogs) != 3 {
		return fail()
	}
	queryCatalog := new(servicecatalog.Catalog)
	if catalogs[2].name != "catalog-a-return" || json.Unmarshal(catalogs[2].raw, queryCatalog) != nil {
		return fail()
	}
	if owner.catalogStage.write(ctx, catalogs, maxEpochCatalogBytes) != nil {
		return fail()
	}
	owner.preparationStep = "catalog-tree"
	owner.catalogs, err = ProtectLinuxExecutionInputTree(ctx, owner.constructors.path, []LinuxInputTreeSource{{Name: "catalogs", Path: owner.catalogStage.root.path}}, helper)
	if err != nil || owner.verifyInputs(ctx, owner.catalogs, "catalogs", catalogs, maxEpochCatalogBytes) != nil {
		return fail()
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil || key == ([32]byte{}) {
		return fail()
	}
	source := productionSourceURL(author.roots[1].path)
	repository, err := phebssync.RepoName(source)
	if err != nil {
		return fail()
	}
	owner.preparationStep = "configs"
	inputs := make([]epochGeneratedInput, 0, 5)
	var parsedConfigs [5]*config.Config
	for index, logical := range []string{"a", "b", "a-return", "a-return", "a-return"} {
		listener, e := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
		if e != nil {
			return fail()
		}
		owner.listeners[index] = listener
		epoch := ExecutionEpochConfig{Epoch: uint64(index + 1), LogicalRevision: logical, CatalogPath: linuxEpochInputRoute(owner.catalogs, "catalogs", "catalog-"+logical), CatalogSHA256: plan.Revisions.Logical[min(index, 2)].CatalogSource.SHA256, Repository: repository, Listen: listener.Addr().String(), APIKey: hex.EncodeToString(key[:]), DataRoot: owner.roots[0].path, Home: owner.roots[1].path, Temporary: owner.roots[2].path, BackupRoot: owner.roots[3].path}
		raw, parsed, e := epochConfigBytesParsed(plan, epoch, source)
		if e != nil {
			return fail()
		}
		epoch.ConfigSHA256 = SHA256(raw)
		owner.epochs[index] = epoch
		parsedConfigs[index] = parsed
		inputs = append(inputs, epochGeneratedInput{name: "config-" + strconv.Itoa(index+1), raw: raw})
	}
	if owner.configStage.write(ctx, inputs, maxEpochConfigBytes) != nil {
		return fail()
	}
	owner.preparationStep = "config-tree"
	owner.configs, err = ProtectLinuxExecutionInputTree(ctx, owner.constructors.path, []LinuxInputTreeSource{{Name: "configs", Path: owner.configStage.root.path}}, helper)
	if err != nil || owner.verifyInputs(ctx, owner.configs, "configs", inputs, maxEpochConfigBytes) != nil {
		return fail()
	}
	for index := range owner.epochs {
		owner.epochs[index].ConfigPath = linuxEpochInputRoute(owner.configs, "configs", "config-"+strconv.Itoa(index+1))
	}
	owner.preparationStep = "staging-retirement"
	if owner.checkStage() != nil || owner.catalogStage.remove(ctx) != nil || owner.configStage.remove(ctx) != nil {
		return fail()
	}
	owner.parsedConfigs, owner.queryCatalog, owner.logicalChanges = parsedConfigs, queryCatalog, &changes
	owner.preparationStep = "ready"
	if owner.checkLocked(ctx, 1) != nil {
		return fail()
	}
	return owner, nil
}

func linuxEpochInputRoute(tree *LinuxExecutionInputTree, selection, name string) string {
	return "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strconv.Itoa(int(tree.root.Fd())) + "/" + selection + "/" + name
}
func (owner *LinuxExecutionEpochConfigCustody) checkStage() error {
	if owner.parent.check() != nil || owner.stage.check() != nil || owner.constructors.file != nil && owner.constructors.check() != nil {
		return ErrExecutionEpochConfigs
	}
	return nil
}
func linuxEpochSameVolume(a, b LinuxExecutionCorpusSourceIdentity) bool {
	return a.Device == b.Device && a.Mount == b.Mount && a.Filesystem == b.Filesystem && a.FSID == b.FSID && a.NamespaceDevice == b.NamespaceDevice && a.NamespaceInode == b.NamespaceInode
}

// Borrowed immutable keeper entries captured while their genuine input-owner
// callbacks hold the mutex. These are not escaped loan descriptors. Closing a
// prerequisite invalidates its os.File and refuses the next metadata check.
type linuxEpochAuthorInputs struct{ plan, image linuxExecutionInput }

func (inputs linuxEpochAuthorInputs) check() error {
	if inputs.plan.file == nil || inputs.image.file == nil || inputs.plan.identity.Executable || !inputs.image.identity.Executable || !linuxInputProtected(inputs.plan.file, inputs.plan) || !linuxInputProtected(inputs.image.file, inputs.image) {
		return ErrExecutionEpochConfigs
	}
	return nil
}
func (owner *LinuxExecutionEpochConfigCustody) checkNativeRoots(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || owner.author == nil || owner.author.closing || owner.author.released || owner.author.err != nil || owner.checkStage() != nil || owner.author.checkRoots(ctx) != nil || len(owner.roots) != 4 {
		return ErrExecutionEpochConfigs
	}
	for _, root := range owner.roots {
		if root.check() != nil || !linuxEpochSameVolume(root.identity, owner.parent.identity) {
			return ErrExecutionEpochConfigs
		}
	}
	return nil
}
func linuxEpochTreeReady(tree *LinuxExecutionInputTree) bool {
	if tree == nil || tree.done == nil {
		return false
	}
	select {
	case <-tree.done:
	default:
		return false
	}
	return !tree.closed && tree.err == nil && tree.joined && tree.empty && tree.checkRoot() == nil
}

// Caller serializes this owner; its trees never escape and their keepers remain
// owned through all uses. A future held flow must use this check, not tree.Check.
func (owner *LinuxExecutionEpochConfigCustody) checkLocked(ctx context.Context, epoch uint64) error {
	if ctx == nil || ctx.Err() != nil || owner.closing || owner.closed || owner.err != nil || epoch < 1 || epoch > 5 || owner.checkNativeRoots(ctx) != nil || owner.authorInputs.check() != nil || !linuxEpochTreeReady(owner.catalogs) || !linuxEpochTreeReady(owner.configs) || owner.logicalChanges == nil {
		return ErrExecutionEpochConfigs
	}
	selected := owner.epochs[epoch-1]
	if selected.ConfigPath != linuxEpochInputRoute(owner.configs, "configs", "config-"+strconv.FormatUint(epoch, 10)) || selected.CatalogPath != linuxEpochInputRoute(owner.catalogs, "catalogs", "catalog-"+selected.LogicalRevision) || owner.parsedConfigs[epoch-1] == nil || epoch > owner.released && (owner.listeners[epoch-1] == nil || owner.listeners[epoch-1].Addr().String() != selected.Listen) {
		return ErrExecutionEpochConfigs
	}
	return nil
}
func (owner *LinuxExecutionEpochConfigCustody) Check(ctx context.Context, epoch uint64) (ExecutionEpochConfig, error) {
	if owner == nil || owner.author == nil {
		return ExecutionEpochConfig{}, ErrExecutionEpochConfigs
	}
	owner.author.mu.Lock()
	defer owner.author.mu.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.checkLocked(ctx, epoch) != nil {
		owner.err = ErrExecutionEpochConfigs
		return ExecutionEpochConfig{}, owner.err
	}
	return owner.epochs[epoch-1], nil
}

// ReleaseListener is ordered and one-shot; native Start still owns the race
// between closing this reservation and actually binding the selected address.
func (owner *LinuxExecutionEpochConfigCustody) ReleaseListener(ctx context.Context, epoch uint64) error {
	if owner == nil || owner.author == nil {
		return ErrExecutionEpochConfigs
	}
	owner.author.mu.Lock()
	defer owner.author.mu.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.borrowedBy != nil || epoch < 1 || epoch > uint64(len(owner.listeners)) || epoch != owner.released+1 {
		return ErrExecutionEpochConfigs
	}
	if owner.checkLocked(ctx, epoch) != nil {
		owner.err = ErrExecutionEpochConfigs
		return owner.err
	}
	if owner.listeners[epoch-1].Close() != nil {
		owner.err = ErrExecutionEpochConfigs
		return owner.err
	}
	owner.listeners[epoch-1] = nil
	owner.released++
	return nil
}

// RetainedPaths is private cleanup information, never recursive deletion authority.
func (owner *LinuxExecutionEpochConfigCustody) RetainedPaths() []string {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.stage.path == "" {
		return nil
	}
	return []string{owner.stage.path}
}

// Close refuses active use; future flow code must establish actual join before
// clearing it. Constructor joins and cleanup run outside the hook's epoch mutex.
// Writable roots/source bytes are retained; only known preparation files retire.
func (owner *LinuxExecutionEpochConfigCustody) Close(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	if ctx == nil {
		return ErrExecutionEpochConfigs
	}
	owner.mu.Lock()
	if owner.active || owner.cleaning {
		owner.mu.Unlock()
		return ErrExecutionEpochConfigs
	}
	if owner.closed {
		err := owner.err
		owner.mu.Unlock()
		return err
	}
	owner.closing, owner.cleaning = true, true
	owner.mu.Unlock()
	defer func() { owner.mu.Lock(); owner.cleaning = false; owner.mu.Unlock() }()
	fail := func() error {
		owner.mu.Lock()
		owner.err = ErrExecutionEpochConfigs
		owner.mu.Unlock()
		return ErrExecutionEpochConfigs
	}
	if owner.stage.file != nil && owner.checkStage() != nil {
		return fail()
	}
	for _, tree := range []*LinuxExecutionInputTree{owner.catalogs, owner.configs} {
		if tree != nil {
			if tree.Close(ctx) != nil {
				_ = fail()
			}
			if !tree.closed {
				return fail()
			}
		}
	}
	for _, stage := range []*linuxEpochInputStage{owner.catalogStage, owner.configStage} {
		if stage != nil && stage.remove(ctx) != nil {
			return fail()
		}
	}
	for index, listener := range owner.listeners {
		if listener != nil {
			if listener.Close() != nil {
				return fail()
			}
			owner.listeners[index] = nil
		}
	}
	for _, stage := range []*linuxEpochInputStage{owner.catalogStage, owner.configStage} {
		if stage != nil && stage.root.file != nil && stage.root.file.Close() != nil {
			return fail()
		}
	}
	for _, root := range append(append([]linuxAuthorRoot{}, owner.roots...), owner.constructors, owner.stage, owner.parent) {
		if root.file != nil && root.file.Close() != nil {
			return fail()
		}
	}
	owner.mu.Lock()
	owner.closed = true
	err := owner.err
	owner.mu.Unlock()
	return err
}
