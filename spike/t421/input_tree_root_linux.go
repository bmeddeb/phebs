//go:build linux

package t421

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func inputTreeMountID(fd int) (uint64, error) {
	var stat unix.Statx_t
	if unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat) != nil ||
		stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, ErrLinuxInputTree
	}
	return stat.Mnt_id, nil
}

func observeInputTreeRoot(file *os.File) (inputTreeKernelIdentity, error) {
	var identity inputTreeKernelIdentity
	var filesystem unix.Statfs_t
	if file == nil || unix.Fstat(int(file.Fd()), &identity.stat) != nil ||
		identity.stat.Mode != unix.S_IFDIR|0o700 || identity.stat.Uid != uint32(os.Geteuid()) || identity.stat.Nlink == 0 ||
		unix.Fstatfs(int(file.Fd()), &filesystem) != nil || filesystem.Type != unix.TMPFS_MAGIC ||
		filesystem.Flags&(unix.ST_RDONLY|unix.ST_NOSUID|unix.ST_NODEV) != unix.ST_RDONLY|unix.ST_NOSUID|unix.ST_NODEV {
		return inputTreeKernelIdentity{}, ErrLinuxInputTree
	}
	identity.fsid = filesystem.Fsid.Val
	var err error
	identity.mount, err = inputTreeMountID(int(file.Fd()))
	flags, flagErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flagErr != nil || fdErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || fdFlags&unix.FD_CLOEXEC == 0 {
		return inputTreeKernelIdentity{}, ErrLinuxInputTree
	}
	return identity, nil
}

func (tree *LinuxExecutionInputTree) checkRoot() error {
	identity, err := observeInputTreeRoot(tree.root)
	if err != nil || !sameLinuxInputStat(tree.kernel.stat, identity.stat) || identity.fsid != tree.kernel.fsid || identity.mount != tree.kernel.mount {
		return ErrLinuxInputTree
	}
	return nil
}

func (tree *LinuxExecutionInputTree) checkStage() error {
	if tree.stage == nil || tree.stageInfo == nil {
		return ErrLinuxInputTree
	}
	held, err := tree.stage.Stat()
	current, pathErr := os.Lstat(tree.stagePath)
	canonical, canonicalErr := filepath.EvalSymlinks(tree.stagePath)
	if err != nil || pathErr != nil || canonicalErr != nil || canonical != tree.stagePath ||
		!inputCustodySame(tree.stageInfo, held) || !inputCustodySame(held, current) ||
		!current.IsDir() || current.Mode().Perm() != 0o700 || !inputCustodyCopyOwned(current) {
		return ErrLinuxInputTree
	}
	return nil
}

// Kernel namespace identities are observed while the actual measured helper is
// awaiting ACK. No namespace identifier or capability boolean comes from JSON.
type inputTreeNamespaces struct {
	userDevice, userInode, mountDevice, mountInode uint64
}

func observeInputTreeNamespaces(pid int) (inputTreeNamespaces, error) {
	var identity inputTreeNamespaces
	if pid <= 0 {
		return identity, ErrLinuxInputTree
	}
	for _, name := range []string{"user", "mnt"} {
		var self, child unix.Stat_t
		if unix.Stat("/proc/self/ns/"+name, &self) != nil || unix.Stat("/proc/"+strconv.Itoa(pid)+"/ns/"+name, &child) != nil ||
			self.Dev == child.Dev && self.Ino == child.Ino {
			return inputTreeNamespaces{}, ErrLinuxInputTree
		}
		if name == "user" {
			identity.userDevice, identity.userInode = uint64(child.Dev), child.Ino
		} else {
			identity.mountDevice, identity.mountInode = uint64(child.Dev), child.Ino
		}
	}
	return identity, nil
}

func observeInputTreeDroppedPrivileges(pid int) error {
	path := "/proc/" + strconv.Itoa(pid) + "/task"
	directory, err := os.Open(path)
	if err != nil {
		return ErrLinuxInputTree
	}
	tasks, readErr := directory.ReadDir(65)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(tasks) == 0 || len(tasks) > 64 {
		return ErrLinuxInputTree
	}
	for _, task := range tasks {
		id, parseErr := strconv.ParseUint(task.Name(), 10, 32)
		if parseErr != nil || id == 0 || !task.IsDir() {
			return ErrLinuxInputTree
		}
		file, err := os.Open(path + "/" + task.Name() + "/status")
		if err != nil {
			return ErrLinuxInputTree
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 16<<10))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !inputTreeDroppedStatus(raw) {
			return ErrLinuxInputTree
		}
	}
	return nil
}

func inputTreeDroppedStatus(raw []byte) bool {
	if len(raw) == 0 || len(raw) >= 16<<10 {
		return false
	}
	want := map[string]bool{"CapEff": false, "CapPrm": false, "CapInh": false, "CapAmb": false, "CapBnd": false, "NoNewPrivs": false}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, _ := strings.Cut(line, ":")
		if seen, required := want[key]; required {
			if seen {
				return false
			}
			value = strings.TrimSpace(value)
			if key == "NoNewPrivs" {
				if value != "1" {
					return false
				}
			} else if number, err := strconv.ParseUint(value, 16, 64); err != nil || number != 0 {
				return false
			}
			want[key] = true
		}
	}
	for _, seen := range want {
		if !seen {
			return false
		}
	}
	return true
}

func observeInputTreeHelperMount(pid int, stage string, mount uint64) error {
	file, err := os.Open("/proc/" + strconv.Itoa(pid) + "/mountinfo")
	if err != nil {
		return ErrLinuxInputTree
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 512<<10))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) == 512<<10 || !inputTreeHelperMountMatches(raw, stage, mount) {
		return ErrLinuxInputTree
	}
	return nil
}

// Bind the received foreign mount descriptor to the actual live helper's
// private mount table, rather than accepting a self-reported mount identifier.
func inputTreeHelperMountMatches(raw []byte, stage string, mount uint64) bool {
	if len(raw) == 0 || len(raw) >= 512<<10 || mount == 0 || !executionGitAbsolutePath(stage) || raw[len(raw)-1] != '\n' {
		return false
	}
	found, lines := false, strings.Split(string(raw[:len(raw)-1]), "\n")
	if len(lines) > 8192 {
		return false
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return false
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || id == 0 {
			return false
		}
		if id != mount {
			continue
		}
		if found || len(fields) != 10 || fields[3] != "/" || fields[4] != stage || fields[6] != "-" || fields[7] != "tmpfs" || fields[8] != "tmpfs" {
			return false
		}
		options := "," + fields[5] + ","
		if !strings.Contains(options, ",ro,") || !strings.Contains(options, ",nosuid,") || !strings.Contains(options, ",nodev,") || strings.Contains(options, ",rw,") {
			return false
		}
		found = true
	}
	return found
}

// Check performs only constant kernel-object/mount/descriptor observations.
// A copied tree is not an assertion about its original source's provenance.
func (tree *LinuxExecutionInputTree) Check(ctx context.Context) (LinuxInputTreeIdentity, error) {
	if tree == nil || ctx == nil {
		return LinuxInputTreeIdentity{}, ErrLinuxInputTree
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	select {
	case <-tree.done:
	default:
		return LinuxInputTreeIdentity{}, ErrLinuxInputTree
	}
	if ctx.Err() != nil || tree.closed || tree.err != nil || !tree.joined || !tree.empty || tree.checkRoot() != nil {
		tree.err = ErrLinuxInputTree
		return LinuxInputTreeIdentity{}, ErrLinuxInputTree
	}
	return tree.identity, nil
}

// WithRoot lends a fresh read-only CLOEXEC descriptor for the same private RO
// mount, holding the custody mutex through use. The trusted callback must not
// retain/duplicate the descriptor, reenter custody, or return before every
// consumer it starts has joined. As in H2b, that is a caller obligation, not an
// inferred drain proof. This is not a pressure-executor/join authority. No path
// is returned and no payload is rehashed. Callback errors alone do not poison
// protection; cancellation, panic/Goexit, descriptor or mount drift do.
func (tree *LinuxExecutionInputTree) WithRoot(ctx context.Context, use func(*os.File) error) error {
	if tree == nil || ctx == nil || use == nil {
		return ErrLinuxInputTree
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	select {
	case <-tree.done:
	default:
		return ErrLinuxInputTree
	}
	if ctx.Err() != nil || tree.closed || tree.err != nil || !tree.joined || !tree.empty || tree.checkRoot() != nil {
		tree.err = ErrLinuxInputTree
		return ErrLinuxInputTree
	}
	fd, err := unix.Openat(int(tree.root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		tree.err = ErrLinuxInputTree
		return tree.err
	}
	loan := os.NewFile(uintptr(fd), "readonly-input-tree-loan")
	released := false
	defer func() {
		if !released {
			_ = loan.Close()
			tree.err = ErrLinuxInputTree
		}
	}()
	before, err := observeInputTreeRoot(loan)
	if err != nil || !sameLinuxInputStat(before.stat, tree.kernel.stat) || before.mount != tree.kernel.mount || before.fsid != tree.kernel.fsid {
		_ = loan.Close()
		released, tree.err = true, ErrLinuxInputTree
		return tree.err
	}
	useErr := use(loan)
	after, observeErr := observeInputTreeRoot(loan)
	closeErr := loan.Close()
	released = true
	if ctx.Err() != nil || observeErr != nil || closeErr != nil || !sameLinuxInputStat(before.stat, after.stat) ||
		before.mount != after.mount || before.fsid != after.fsid || tree.checkRoot() != nil {
		tree.err = ErrLinuxInputTree
		return tree.err
	}
	return useErr
}

// Close cancels preparation and joins its tracked owner before releasing any
// transferred mount or private placeholder. An unjoined/unknown session retains
// custody; repeated Close may recheck that same recorded session. It neither
// thaws a mount nor removes a caller source, nor certifies callback consumers.
func (tree *LinuxExecutionInputTree) Close(ctx context.Context) error {
	if tree == nil || ctx == nil || tree.cancel == nil || tree.done == nil {
		return ErrLinuxInputTree
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.cancel()
	select {
	case <-tree.done:
	case <-ctx.Done():
		return ErrLinuxInputTree
	}
	if tree.closed {
		return tree.err
	}
	if tree.pid != 0 && (!tree.joined || !tree.empty) {
		deadline := time.Now().Add(5 * time.Second)
		if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
			deadline = bound
		}
		if !tree.joined || ctx.Err() != nil || t4013.WaitPrivateProcessSession(tree.pid, deadline) != nil {
			return ErrLinuxInputTree
		}
		tree.empty = true
	}
	if tree.checkStage() != nil {
		return ErrLinuxInputTree
	}
	rows, readErr := tree.stage.ReadDir(1)
	if len(rows) != 0 || readErr != io.EOF || tree.checkStage() != nil || os.Remove(tree.stagePath) != nil {
		return ErrLinuxInputTree
	}
	if tree.root != nil && tree.root.Close() != nil {
		tree.err = ErrLinuxInputTree
	}
	if tree.stage.Close() != nil {
		tree.err = ErrLinuxInputTree
	}
	tree.closed = true
	return tree.err
}
