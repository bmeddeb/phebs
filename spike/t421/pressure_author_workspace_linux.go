//go:build linux

package t421

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"golang.org/x/sys/unix"
)

// This enclosing graph outlives AuthorNextOn. Only its own explicit Close joins
// every future borrower before a one-shot healthy retirement can be issued.
type linuxPressureAuthorWorkspace struct {
	mu              sync.Mutex
	self            *linuxPressureAuthorWorkspace
	view            *linuxPressureAuthorView
	root            *os.File
	stat            unix.Stat_t
	path            string
	inner           *linuxExecutorInnerPrework
	authorTool      *LinuxExecutionToolCustody
	planInput       *LinuxExecutionInputCustody
	author          *LinuxExecutionAuthorCustody
	controller      *dispatchadmission.Controller
	parent          *dispatchadmission.LocalProducer
	store           *storeaccounting.Controller
	cancel          context.CancelFunc
	result          ExecutionAuthorResult
	retirement      *linuxPressureAuthorRetirement
	healthy, closed bool
	rootClosed      bool
	retiring        bool
	err             error
}

// No request field, path or success bit can construct this capability. The
// enclosing actual graph must issue it after all of its original owners retire.
type linuxPressureAuthorRetirement struct {
	owner       *linuxPressureAuthorWorkspace
	used        atomic.Bool
	root        *os.File
	uid, gid    uint32
	device      uint64
	fsid        [2]int32
	mount       uint64
	imageBytes  uint64
	observation *executionPreworkObservation
	journal     *linuxPressureAuthorRetirementJournal
}

func (permit *linuxPressureAuthorRetirement) begin(ctx context.Context) error {
	if permit == nil || !permit.used.CompareAndSwap(false, true) || ctx == nil || ctx.Err() != nil || permit.owner == nil {
		return ErrLinuxPressure
	}
	owner := permit.owner
	if owner.self != owner || owner.retirement != permit || !owner.healthy || !owner.closed || !owner.retiring || owner.err != nil || owner.root != permit.root || owner.inner == nil || !owner.inner.retired() || owner.author != nil || owner.authorTool != nil || owner.planInput != nil || permit.observation == nil || !permit.observation.settle(ctx) || owner.view.check(ctx) != nil {
		return ErrLinuxPressure
	}
	var current unix.Stat_t
	var fs unix.Statfs_t
	mount, err := inputTreeMountID(int(owner.root.Fd()))
	if err != nil || mount != permit.mount || unix.Fstat(int(owner.root.Fd()), &current) != nil || current.Dev != owner.stat.Dev || current.Ino != owner.stat.Ino || current.Mode != unix.S_IFDIR|0o700 || current.Uid != permit.uid || current.Gid != permit.gid || unix.Fstatfs(int(owner.root.Fd()), &fs) != nil || fs.Fsid.Val != permit.fsid {
		return ErrLinuxPressure
	}
	return nil
}
func newLinuxPressureAuthorWorkspace(ctx context.Context, value linuxPressureAuthorSelection, view *linuxPressureAuthorView, image *os.File, observation *executionPreworkObservation) (*linuxPressureAuthorWorkspace, error) {
	if ctx == nil || ctx.Err() != nil || view.check(ctx) != nil || value.ImageBytes != value.Borrow.Pressure.ImageBytes {
		return nil, ErrLinuxPressure
	}
	owner := &linuxPressureAuthorWorkspace{view: view, inner: &linuxExecutorInnerPrework{observation: observation}, path: filepath.Join(linuxPressureAuthorWork, "derived")}
	owner.self = owner
	fail := func() (*linuxPressureAuthorWorkspace, error) { owner.err = ErrLinuxPressure; return owner, owner.err }
	if unix.Mkdirat(int(view.file.Fd()), "derived", 0o700) != nil {
		return fail()
	}
	// Consume the new subtree only through the authenticated view descriptor.
	// The held FD, linked name, mount and filesystem identities are bound before
	// any writer runs; a path alias supplies no ownership evidence.
	var err error
	derivedFD, err := unix.Openat(int(view.file.Fd()), "derived", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	owner.root = os.NewFile(uintptr(derivedFD), owner.path)
	if owner.root == nil {
		_ = unix.Close(derivedFD)
		return fail()
	}
	var linked unix.Stat_t
	var derivedFS unix.Statfs_t
	rootMount, mountErr := inputTreeMountID(derivedFD)
	if mountErr != nil || !linuxPressureAuthorDirectoryFD(derivedFD) || unix.Fstat(derivedFD, &owner.stat) != nil || unix.Fstatat(int(view.file.Fd()), "derived", &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstatfs(derivedFD, &derivedFS) != nil ||
		owner.stat.Dev != view.stat.Dev || owner.stat.Ino != linked.Ino || linked.Dev != owner.stat.Dev || owner.stat.Uid != value.Borrow.UID || owner.stat.Gid != value.Borrow.GID || owner.stat.Mode != unix.S_IFDIR|0o700 ||
		rootMount != view.mount || derivedFS.Fsid.Val != view.fsid {
		return fail()
	}
	// Classify only this attempt-exclusive subtree as healthy ephemeral before
	// writers and issue its sole private retirement owner. No later stage can
	// mint or replace this capability; construction failures retain the graph.
	owner.retirement = &linuxPressureAuthorRetirement{owner: owner, root: owner.root, uid: value.Borrow.UID, gid: value.Borrow.GID, device: uint64(owner.stat.Dev), fsid: view.fsid, mount: view.mount, imageBytes: value.ImageBytes, observation: observation}
	for _, name := range []string{"preparation", "author"} {
		if unix.Mkdirat(int(owner.root.Fd()), name, 0o700) != nil {
			return fail()
		}
	}
	target := value.Borrow.Target
	target.PreparationRoot = filepath.Join(owner.path, "preparation")
	if !owner.inner.attempted.CompareAndSwap(false, true) || owner.inner.prepareReferenceLayout(ctx, target, image, view) != nil || !owner.inner.tool.executorClaim.bindAuthorInner(owner.inner) || !owner.inner.tool.executorClaim.consumeAuthorInner(owner.inner) || owner.inner.measuredInputs(ctx, target, owner.inner.tool.identity) != nil {
		return fail()
	}
	owner.authorTool, err = owner.inner.builds.ProtectReferenceToolV3(ctx, target.PreparationRoot, "t422-author", value.AuthorBinary, owner.inner.referenceHelper)
	if err != nil {
		return fail()
	}
	plan, err := BuildPlanV3(target.PlanSourceCommit)
	if err != nil || ctx.Err() != nil || plan.Profile.Physical.CombinedRegularFiles != 2_031_604 {
		return fail()
	}
	raw, err := MarshalCanonical(plan)
	if err != nil || ctx.Err() != nil || len(raw) > MaxPlanBytes {
		return fail()
	}
	planPath := filepath.Join(owner.path, "author-plan.json")
	planFD, err := unix.Openat(int(owner.root.Fd()), "author-plan.json", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o400)
	if err != nil {
		return fail()
	}
	file := os.NewFile(uintptr(planFD), planPath)
	if file == nil {
		_ = unix.Close(planFD)
		return fail()
	}
	count, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := observation.close(file.Close())
	if count != len(raw) || writeErr != nil || syncErr != nil || closeErr != nil {
		return fail()
	}
	owner.planInput, err = protectLinuxExecutionInputsObserved(ctx, []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}}, observation)
	if err != nil {
		return fail()
	}
	owner.author, err = prepareLinuxExecutionAuthorObserved(ctx, filepath.Join(owner.path, "author"), LinuxExecutionAuthorRequest{Git: owner.inner.git, Builds: owner.inner.builds, Author: owner.authorTool, Plan: owner.planInput}, observation)
	if err != nil {
		return fail()
	}
	lifetime, cancel := context.WithCancel(ctx)
	owner.cancel = cancel
	var bindings [executionProducerCount][32]byte
	for i := range bindings {
		if _, err := rand.Read(bindings[i][:]); err != nil {
			return fail()
		}
	}
	da, err := executionDispatchConfig(plan, bindings)
	if err != nil {
		return fail()
	}
	owner.controller, err = dispatchadmission.New(lifetime, da)
	if err != nil {
		return fail()
	}
	owner.parent, err = owner.controller.NewLocalProducer(lifetime, executionRootProducer)
	if err != nil {
		return fail()
	}
	sa, _, err := executionStoreConfig(plan, bindings)
	if err != nil {
		return fail()
	}
	owner.store, err = storeaccounting.New(lifetime, sa)
	if err != nil {
		return fail()
	}
	if owner.parent.Pause(ctx) != nil || owner.controller.Fence() != nil || owner.parent.Checkpoint(ctx) != nil || owner.store.Fence() != nil || owner.store.Advance() != nil || owner.controller.Advance() != nil || owner.parent.Resume(2) != nil {
		return fail()
	}
	return owner, nil
}
func (owner *linuxPressureAuthorWorkspace) AuthorA(ctx context.Context) (ExecutionAuthorResult, error) {
	if owner == nil {
		return ExecutionAuthorResult{}, ErrLinuxPressure
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	author := owner.author
	if owner.self != owner || owner.closed || owner.err != nil || owner.healthy || author == nil || owner.view.check(ctx) != nil {
		return ExecutionAuthorResult{}, ErrLinuxPressure
	}
	result, err := author.AuthorNextOn(ctx, owner.controller, owner.parent, 7)
	owner.result = result
	if err != nil || !result.Completed || result.ProducerID != 7 || result.Response == nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || ctx.Err() != nil {
		owner.err = ErrLinuxPressure
		return result, owner.err
	}
	// Still live here: this operation did not release the graph/root producer.
	if !owner.parent.OnController(owner.controller) || owner.controller.Context().Err() != nil || owner.view.check(ctx) != nil {
		owner.err = ErrLinuxPressure
		return result, owner.err
	}
	owner.healthy = true
	return result, nil
}
func (owner *linuxPressureAuthorWorkspace) Close(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	if ctx == nil || owner.self != owner {
		return ErrLinuxPressure
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return owner.err
	}
	observation := owner.inner.observation
	if author := owner.author; author != nil {
		err := author.Close(ctx)
		author.mu.Lock()
		retired := author.released && author.active == nil
		author.mu.Unlock()
		if !retired {
			return ErrLinuxPressure
		}
		owner.author = nil
		if err != nil && owner.err == nil {
			owner.err = err
		}
	}
	if owner.parent != nil {
		if err := owner.parent.Close(ctx); err != nil && owner.err == nil {
			owner.err = err
		}
		owner.parent = nil
	}
	// This slice never opens an SA remote endpoint. Keep the genuine reducer
	// through Author A; cancel its unused future lifetime without fabricating EOF.
	if owner.controller != nil {
		fenceErr := owner.controller.Fence()
		if fenceErr == nil {
			snapshot, snapshotErr := owner.controller.Snapshot()
			if snapshotErr != nil && owner.err == nil {
				owner.err = snapshotErr
			}
			for _, producer := range snapshot.Producers {
				if !producer.Attached && !producer.Closed {
					if err := owner.controller.CancelUnused(producer.Producer); err != nil && owner.err == nil {
						owner.err = err
					}
				}
			}
		} else if owner.err == nil {
			owner.err = fenceErr
		}
	}
	owner.store = nil
	if owner.cancel != nil {
		owner.cancel()
		owner.cancel = nil
	}
	if owner.authorTool != nil {
		err := owner.authorTool.Close()
		if !linuxExecutorToolReleased(owner.authorTool) {
			return ErrLinuxPressure
		}
		owner.authorTool = nil
		if err != nil && owner.err == nil {
			owner.err = err
		}
	}
	if owner.planInput != nil {
		err := owner.planInput.Close()
		owner.planInput.mu.Lock()
		released := owner.planInput.closed && owner.planInput.closeErr == nil
		owner.planInput.mu.Unlock()
		if !released {
			return ErrLinuxPressure
		}
		owner.planInput = nil
		if err != nil && owner.err == nil {
			owner.err = err
		}
	}
	err := owner.inner.Close(ctx)
	if !owner.inner.retired() || !observation.settle(ctx) {
		return ErrLinuxPressure
	}
	if err != nil && owner.err == nil {
		owner.err = err
	}
	owner.closed = true
	return owner.err
}
func (owner *linuxPressureAuthorWorkspace) retire(ctx context.Context, imageBytes uint64) error {
	if owner == nil {
		return ErrLinuxPressure
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.retiring = true
	defer func() { owner.retiring = false }()
	// Consume the single pre-writer owner; a zero or replaced capability refuses
	// rather than fabricating retirement authority after the writers finished.
	permit := owner.retirement
	if permit == nil || permit.owner != owner || permit.root != owner.root || permit.used.Load() || imageBytes != permit.imageBytes || owner.err != nil || !owner.closed || !owner.healthy || owner.root == nil || owner.rootClosed {
		return ErrLinuxPressure
	}
	if err := retireLinuxPressureAuthorWorkspace(ctx, permit); err != nil {
		return err
	}
	owner.rootClosed = true
	if err := permit.observation.close(owner.root.Close()); err != nil {
		return err
	}
	if unix.Unlinkat(int(owner.view.file.Fd()), "derived", unix.AT_REMOVEDIR) != nil || owner.view.file.Sync() != nil {
		return ErrLinuxPressure
	}
	return nil
}

// Called only while the original workspace.mu is held across the engine call.
// charge accounts syscalls; it cannot issue an owner or supply readiness.
func (permit *linuxPressureAuthorRetirement) checkRoot(ctx context.Context, charge func(context.Context) error) error {
	if permit == nil || permit.owner == nil || charge == nil || ctx == nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	owner := permit.owner
	if owner.self != owner || owner.retirement != permit || !owner.retiring || !owner.closed || !owner.healthy || owner.err != nil || owner.rootClosed || owner.root != permit.root || owner.view == nil || owner.view.self != owner.view || owner.view.closed {
		return ErrLinuxPressure
	}
	var held, linked, view, namespace unix.Stat_t
	var actual, named, parent unix.Statx_t
	if charge(ctx) != nil || unix.Fstat(int(owner.root.Fd()), &held) != nil {
		return ErrLinuxPressure
	}
	if charge(ctx) != nil || unix.Fstatat(int(owner.view.file.Fd()), "derived", &linked, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return ErrLinuxPressure
	}
	if charge(ctx) != nil || unix.Statx(int(owner.root.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &actual) != nil {
		return ErrLinuxPressure
	}
	if charge(ctx) != nil || unix.Statx(int(owner.view.file.Fd()), "derived", unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &named) != nil {
		return ErrLinuxPressure
	}
	if charge(ctx) != nil || unix.Fstat(int(owner.view.file.Fd()), &view) != nil {
		return ErrLinuxPressure
	}
	if charge(ctx) != nil || unix.Statx(int(owner.view.file.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &parent) != nil {
		return ErrLinuxPressure
	}
	if charge(ctx) != nil || unix.Stat("/proc/self/ns/mnt", &namespace) != nil {
		return ErrLinuxPressure
	}
	if held.Dev != owner.stat.Dev || held.Ino != owner.stat.Ino || linked.Dev != held.Dev || linked.Ino != held.Ino || held.Mode != unix.S_IFDIR|0o700 || held.Uid != permit.uid || held.Gid != permit.gid ||
		actual.Mask&unix.STATX_MNT_ID == 0 || named.Mask&unix.STATX_MNT_ID == 0 || parent.Mask&unix.STATX_MNT_ID == 0 || actual.Mnt_id != permit.mount || named.Mnt_id != permit.mount || parent.Mnt_id != owner.view.mount ||
		view.Dev != owner.view.stat.Dev || view.Ino != owner.view.stat.Ino || uint64(namespace.Dev) != owner.view.namespaceDevice || namespace.Ino != owner.view.namespaceInode {
		return ErrLinuxPressure
	}
	return nil
}
