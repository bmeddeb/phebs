//go:build linux

package t421

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

type linuxEpochTreeCharge struct {
	Role, Manifest, Helper  string
	Device, Inode, Mount    uint64
	FSID                    [2]int32
	BlockSize, Blocks, Free uint64
	Files, FreeFiles        uint64
	LogicalBytes, Entries   uint64
	AllocatedBytes          uint64
}

type linuxEpochImageCharge struct {
	Role, SHA256               string
	Device, Inode              uint64
	Bytes, Blocks, ChargeBytes uint64
}

type linuxEpochResourceFootprint struct {
	Trees        [4]linuxEpochTreeCharge
	Images       [5]linuxEpochImageCharge
	PayloadBytes uint64
}

func linuxEpochChargeAdd(total, value uint64) (uint64, error) {
	if value > math.MaxInt64 || total > math.MaxInt64-value {
		return 0, errLinuxEpochResources
	}
	return total + value, nil
}

func linuxEpochImageBytes(size, blocks, page uint64) (uint64, error) {
	if size == 0 || page == 0 || page&(page-1) != 0 || size > math.MaxInt64 || blocks > math.MaxInt64/512 || size > math.MaxInt64-(page-1) {
		return 0, errLinuxEpochResources
	}
	return max((size+page-1)/page*page, blocks*512), nil
}

func linuxEpochAllocatedTreeBytes(blocks, free, blockSize uint64) (uint64, error) {
	if blockSize == 0 || free > blocks || blocks-free > math.MaxInt64/blockSize {
		return 0, errLinuxEpochResources
	}
	return (blocks - free) * blockSize, nil
}

// All owners below are already held by the single worker. Do not reacquire
// their public Check/With/Close methods from inside this complete loan stack.
func collectLinuxEpochResourceFootprint(ctx context.Context, held *linuxEpochHeldFlow) (linuxEpochResourceFootprint, error) {
	var result linuxEpochResourceFootprint
	if ctx == nil || ctx.Err() != nil || held == nil || held.author == nil || held.epochs == nil || held.builds == nil || held.git == nil || held.epochs.author != held.author ||
		held.author.request.Builds != held.builds || held.author.request.Git != held.git || held.builds.request.Git != held.git || held.builds.closing || held.builds.released || held.builds.err != nil || held.builds.active != nil || held.builds.staging != nil ||
		!held.builds.last.joined || !held.builds.last.empty || held.git.closing || held.git.released || held.git.err != nil || held.git.sealed != nil || held.git.probe != nil || len(held.git.aliases) != 0 {
		return result, errLinuxEpochResources
	}
	held.author.mu.Lock()
	held.epochs.mu.Lock()
	valid := held.flow != nil && held.author.borrowedBy == held.flow && held.epochs.active && !held.author.closing && !held.author.released && held.author.err == nil &&
		!held.epochs.closed && !held.epochs.closing && held.epochs.err == nil && held.author.checkRoots(ctx) == nil && held.checkWorkspace() == nil
	held.epochs.mu.Unlock()
	held.author.mu.Unlock()
	if !valid {
		return result, errLinuxEpochResources
	}
	if _, exists := held.builds.children["payload"]; exists || len(held.builds.children) != 3 {
		return result, errLinuxEpochResources
	}
	for _, name := range []string{"home", "tmp", "scratch"} {
		if held.builds.children[name] == nil {
			return result, errLinuxEpochResources
		}
	}
	trees := [4]*LinuxExecutionInputTree{held.builds.tree, held.git.tree, held.epochs.catalogs, held.epochs.configs}
	roots := [4]*os.File{held.buildRoot, held.gitRoot, held.catalogRoot, held.configRoot}
	roles := [4]string{"build-inputs", "git", "catalogs", "configs"}
	seenTrees := make(map[*LinuxExecutionInputTree]bool, 4)
	seenMounts := make(map[uint64]bool, 4)
	for index, tree := range trees {
		if tree == nil || roots[index] == nil || tree.root == nil || tree.closed || tree.err != nil || !tree.joined || !tree.empty || seenTrees[tree] {
			return result, errLinuxEpochResources
		}
		select {
		case <-tree.done:
		default:
			return result, errLinuxEpochResources
		}
		seenTrees[tree] = true
		identity, err := observeInputTreeRoot(roots[index])
		if err != nil || tree.checkRoot() != nil || !sameLinuxInputStat(tree.kernel.stat, identity.stat) || identity.mount != tree.kernel.mount || identity.fsid != tree.kernel.fsid || seenMounts[identity.mount] {
			return result, errLinuxEpochResources
		}
		seenMounts[identity.mount] = true
		var fs unix.Statfs_t
		if unix.Fstatfs(int(roots[index].Fd()), &fs) != nil || fs.Bsize <= 0 || !validExecutionSHA256(tree.identity.ManifestSHA256) || !validExecutionSHA256(tree.identity.HelperSHA256) {
			return result, errLinuxEpochResources
		}
		allocated, err := linuxEpochAllocatedTreeBytes(fs.Blocks, fs.Bfree, uint64(fs.Bsize))
		if err != nil || allocated == 0 {
			return result, errLinuxEpochResources
		}
		result.Trees[index] = linuxEpochTreeCharge{Role: roles[index], Manifest: tree.identity.ManifestSHA256, Helper: tree.identity.HelperSHA256,
			Device: identity.stat.Dev, Inode: identity.stat.Ino, Mount: identity.mount, FSID: identity.fsid,
			BlockSize: uint64(fs.Bsize), Blocks: fs.Blocks, Free: fs.Bfree, Files: fs.Files, FreeFiles: fs.Ffree,
			LogicalBytes: tree.identity.Bytes, Entries: tree.identity.Entries, AllocatedBytes: allocated}
		result.PayloadBytes, err = linuxEpochChargeAdd(result.PayloadBytes, allocated)
		if err != nil {
			return linuxEpochResourceFootprint{}, err
		}
	}
	if trees[0].identity.Bytes != uint64(held.builds.inventory.Bytes) || trees[0].identity.Files != uint64(held.builds.inventory.Files) || trees[0].identity.Entries != uint64(held.builds.inventory.Entries) ||
		trees[1].identity.Files != 7 || trees[1].identity.Entries != 8 || trees[2].identity.Files != 3 || trees[2].identity.Entries != 4 || trees[3].identity.Files != 5 || trees[3].identity.Entries != 6 {
		return linuxEpochResourceFootprint{}, errLinuxEpochResources
	}
	seenOwners := make(map[*LinuxExecutionInputCustody]bool, 5)
	seenImages := make(map[[2]uint64]bool, 5)
	for index, role := range [5]string{"t422-author", "phebs", "zoekt-git-index", "surreal", "plan"} {
		var owner *LinuxExecutionInputCustody
		var file *os.File
		var entry linuxExecutionInput
		if index < 4 {
			tool := held.tools[index]
			if tool == nil || tool.input == nil || tool.identity != held.imageIdentities[index] || tool.identity.Role != role || index < 3 && tool.referenceInputs != held.builds {
				return linuxEpochResourceFootprint{}, errLinuxEpochResources
			}
			tool.mu.Lock()
			probeAbsent := tool.probe == nil
			tool.mu.Unlock()
			if !probeAbsent {
				return linuxEpochResourceFootprint{}, errLinuxEpochResources
			}
			owner, file, entry = tool.input, held.images[index], held.imageEntries[index]
			if entry.identity.SHA256 != tool.identity.SHA256 || !entry.identity.Executable {
				return linuxEpochResourceFootprint{}, errLinuxEpochResources
			}
		} else {
			owner, file, entry = held.author.request.Plan, held.plan, held.planEntry
			if entry.identity.Executable || entry.identity.Bytes > MaxPlanBytes || entry.identity.SHA256 != held.author.planSHA256 {
				return linuxEpochResourceFootprint{}, errLinuxEpochResources
			}
		}
		if owner == nil || owner.closed || owner.err != nil || len(owner.inputs) != 1 || seenOwners[owner] || file == nil || entry.file == nil || entry.identity.Name != role || entry.identity.Bytes <= 0 ||
			!linuxInputProtected(file, entry) || !linuxInputProtected(entry.file, entry) || owner.inputs[role] != entry {
			return linuxEpochResourceFootprint{}, errLinuxEpochResources
		}
		seenOwners[owner] = true
		var stat unix.Stat_t
		if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Blocks < 0 || stat.Size <= 0 {
			return linuxEpochResourceFootprint{}, errLinuxEpochResources
		}
		key := [2]uint64{stat.Dev, stat.Ino}
		if seenImages[key] {
			return linuxEpochResourceFootprint{}, errLinuxEpochResources
		}
		seenImages[key] = true
		charge, err := linuxEpochImageBytes(uint64(stat.Size), uint64(stat.Blocks), uint64(os.Getpagesize()))
		if err != nil {
			return linuxEpochResourceFootprint{}, err
		}
		result.Images[index] = linuxEpochImageCharge{Role: role, SHA256: entry.identity.SHA256, Device: stat.Dev, Inode: stat.Ino, Bytes: uint64(stat.Size), Blocks: uint64(stat.Blocks), ChargeBytes: charge}
		result.PayloadBytes, err = linuxEpochChargeAdd(result.PayloadBytes, charge)
		if err != nil {
			return linuxEpochResourceFootprint{}, err
		}
	}
	if held.tools[0] != held.author.request.Author || !linuxAuthorBuildBinding(held.author.request, held.imageIdentities[0], held.builds.request.PlanSourceCommit) || ctx.Err() != nil {
		return linuxEpochResourceFootprint{}, errLinuxEpochResources
	}
	return result, nil
}

func linuxEpochResourceDirectoryNames(root *os.File, maximum int) (_ []string, retErr error) {
	if root == nil {
		return nil, errLinuxEpochResources
	}
	fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errLinuxEpochResources
	}
	file := os.NewFile(uintptr(fd), "epoch-resource-directory")
	defer func() {
		if file.Close() != nil {
			retErr = errLinuxEpochResources
		}
	}()
	names, err := file.Readdirnames(maximum + 1)
	if err != nil && err != io.EOF || len(names) > maximum {
		return nil, errLinuxEpochResources
	}
	return names, nil
}

func checkLinuxEpochPreparedDirectories(held *linuxEpochHeldFlow) error {
	for _, stage := range []*linuxEpochInputStage{held.epochs.catalogStage, held.epochs.configStage} {
		if stage == nil || len(stage.files) != 0 || stage.root.check() != nil {
			return errLinuxEpochResources
		}
		names, err := linuxEpochResourceDirectoryNames(stage.root.file, 0)
		if err != nil || len(names) != 0 {
			return errLinuxEpochResources
		}
	}
	if held.epochs.constructors.check() != nil {
		return errLinuxEpochResources
	}
	names, err := linuxEpochResourceDirectoryNames(held.epochs.constructors.file, 2)
	if err != nil || len(names) != 2 {
		return errLinuxEpochResources
	}
	expected := map[string]*LinuxExecutionInputTree{filepath.Base(held.epochs.catalogs.stagePath): held.epochs.catalogs, filepath.Base(held.epochs.configs.stagePath): held.epochs.configs}
	if len(expected) != 2 {
		return errLinuxEpochResources
	}
	for _, name := range names {
		tree := expected[name]
		if tree == nil || tree.checkStage() != nil {
			return errLinuxEpochResources
		}
	}
	return nil
}

// Initial open-FD census, not object or mmap history. Completeness comes from
// the genuine closed owner recipe, which creates no new operational memfds or
// mounts and closes every preparation helper without retaining mapped copies.
func checkLinuxEpochInitialAnonymousObjects(footprint linuxEpochResourceFootprint) (retErr error) {
	directory, err := os.Open("/proc/thread-self/fd")
	if err != nil {
		return errLinuxEpochResources
	}
	defer func() {
		if directory.Close() != nil {
			retErr = errLinuxEpochResources
		}
	}()
	names, err := directory.Readdirnames(8193)
	if err != nil || len(names) > 8192 {
		return errLinuxEpochResources
	}
	for _, name := range names {
		n, err := strconv.ParseUint(name, 10, 31)
		if err != nil {
			return errLinuxEpochResources
		}
		fd, err := unix.Open("/proc/thread-self/fd/"+strconv.FormatUint(n, 10), unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return errLinuxEpochResources
		}
		var stat unix.Stat_t
		var fs unix.Statfs_t
		inspectErr := unix.Fstat(fd, &stat)
		if inspectErr == nil {
			inspectErr = unix.Fstatfs(fd, &fs)
		}
		known := fs.Type != unix.TMPFS_MAGIC || stat.Mode&unix.S_IFMT != unix.S_IFREG && stat.Mode&unix.S_IFMT != unix.S_IFDIR
		if inspectErr == nil && !known {
			for _, image := range footprint.Images {
				known = known || image.Device == stat.Dev && image.Inode == stat.Ino
			}
			if !known {
				mount, mountErr := inputTreeMountID(fd)
				if mountErr != nil {
					inspectErr = mountErr
				}
				for _, tree := range footprint.Trees {
					known = known || tree.Mount == mount
				}
			}
		}
		closeErr := unix.Close(fd)
		if inspectErr != nil || closeErr != nil || !known {
			return errLinuxEpochResources
		}
	}
	return nil
}

type linuxEpochResourceNamespace struct {
	file          *os.File
	device, inode uint64
}

type linuxEpochResourceNamespaces [4]linuxEpochResourceNamespace

var linuxEpochResourceNamespaceKinds = [4]struct {
	name string
	kind int
}{{"mnt", unix.CLONE_NEWNS}, {"user", unix.CLONE_NEWUSER}, {"pid", unix.CLONE_NEWPID}, {"cgroup", unix.CLONE_NEWCGROUP}}

func linuxEpochNamespaceObject(file *os.File, kind int) (uint64, uint64, error) {
	if file == nil {
		return 0, 0, errLinuxEpochResources
	}
	fd := int(file.Fd())
	var stat unix.Stat_t
	var fs unix.Statfs_t
	actualKind, kindErr := unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
	if kindErr != nil || actualKind != kind || unix.Fstat(fd, &stat) != nil || unix.Fstatfs(fd, &fs) != nil || fs.Type != unix.NSFS_MAGIC || stat.Ino == 0 {
		return 0, 0, errLinuxEpochResources
	}
	return stat.Dev, stat.Ino, nil
}

func captureLinuxEpochResourceNamespaces() (linuxEpochResourceNamespaces, error) {
	var pins linuxEpochResourceNamespaces
	for index, selection := range linuxEpochResourceNamespaceKinds {
		file, err := os.Open("/proc/thread-self/ns/" + selection.name)
		if err != nil {
			return pins, errLinuxEpochResources
		}
		pins[index].file = file
		pins[index].device, pins[index].inode, err = linuxEpochNamespaceObject(file, selection.kind)
		if err != nil {
			return pins, err
		}
	}
	return pins, nil
}

func (pins linuxEpochResourceNamespaces) check() error {
	for index, selection := range linuxEpochResourceNamespaceKinds {
		device, inode, err := linuxEpochNamespaceObject(pins[index].file, selection.kind)
		if err != nil || device != pins[index].device || inode != pins[index].inode {
			return errLinuxEpochResources
		}
		current, err := os.Open("/proc/thread-self/ns/" + selection.name)
		if err != nil {
			return errLinuxEpochResources
		}
		device, inode, inspectErr := linuxEpochNamespaceObject(current, selection.kind)
		closeErr := current.Close()
		if inspectErr != nil || closeErr != nil || device != pins[index].device || inode != pins[index].inode {
			return errLinuxEpochResources
		}
	}
	return nil
}

func (pins *linuxEpochResourceNamespaces) close() error {
	var err error
	for index := range pins {
		if pins[index].file != nil {
			if pins[index].file.Close() != nil {
				err = errLinuxEpochResources
			}
			pins[index].file = nil
		}
	}
	return err
}
