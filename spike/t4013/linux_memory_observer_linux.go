//go:build linux

package t4013

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ObserveLinuxMemory reads only fixed kernel filesystem roots. It establishes
// the reachable real hierarchy root and brackets this thread's namespace,
// membership, mount, process lifetime, ancestors and limits. Current use is
// sampled sequentially: two-pass maxima/minima are not an atomic snapshot,
// continuous bound, kernel reservation, host admission or launch permission.
func ObserveLinuxMemory(ctx context.Context) (LinuxMemoryObservation, error) {
	if ctx == nil || ctx.Err() != nil {
		return LinuxMemoryObservation{}, errors.Join(ErrLinuxMemoryObservation, linuxMemoryContextError(ctx))
	}
	// Mount namespaces are thread-specific. Use this locked thread's task
	// records rather than assuming /proc/self describes the executing thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	source, err := newLinuxMemoryNativeSource()
	if err != nil {
		return LinuxMemoryObservation{}, ErrLinuxMemoryObservation
	}
	return observeLinuxMemorySource(ctx, source)
}

func linuxMemoryContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

type linuxMemoryNativeSource struct {
	proc, process, task *os.File
	pid, tid            int
	procObject          linuxMemoryObject
	namespaces          []*os.File
}

func newLinuxMemoryNativeSource() (_ *linuxMemoryNativeSource, retErr error) {
	source := &linuxMemoryNativeSource{pid: os.Getpid(), tid: unix.Gettid()}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, source.close())
		}
	}()
	var err error
	source.proc, err = openNoFollowDirectory("/proc")
	if err != nil {
		return nil, err
	}
	source.procObject, err = linuxMemoryNativeObject(source.proc, linuxMemoryProcMagic)
	if err != nil {
		return nil, err
	}
	if _, _, err := source.currentAnchors(); err != nil {
		return nil, err
	}
	source.process, err = linuxMemoryOpenAt(source.proc, strconv.Itoa(source.pid), true, source.procObject)
	if err != nil {
		return nil, err
	}
	source.task, err = linuxMemoryOpenAt(source.process, "task/"+strconv.Itoa(source.tid), true, source.procObject)
	if err != nil {
		return nil, err
	}
	return source, nil
}

func (source *linuxMemoryNativeSource) snapshot(ctx context.Context) (_ linuxMemorySnapshot, retErr error) {
	if ctx.Err() != nil || source.pid != os.Getpid() || source.tid != unix.Gettid() {
		return linuxMemorySnapshot{}, ErrLinuxMemoryObservation
	}
	currentPID, currentTID, err := source.currentAnchors()
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	processRaw, err := linuxMemoryReadAt(ctx, source.process, "stat", maxLinuxProcessFileBytes, source.procObject)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	process, err := parseLinuxProcessStat(source.pid, processRaw)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	taskRaw, err := linuxMemoryReadAt(ctx, source.task, "stat", maxLinuxProcessFileBytes, source.procObject)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	task, err := parseLinuxProcessStat(source.tid, taskRaw)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	identity := fmt.Sprintf("linux-memory-process-v1:%d:%d:%s:%s", source.pid, source.tid,
		process.snapshot.identityToken, task.snapshot.identityToken)
	first := len(source.namespaces) == 0
	for _, namespace := range []struct {
		name string
		kind int
	}{{"cgroup", unix.CLONE_NEWCGROUP}, {"mnt", unix.CLONE_NEWNS}, {"pid", unix.CLONE_NEWPID}} {
		file, binding, err := source.namespace(ctx, namespace.name, namespace.kind)
		if err != nil {
			return linuxMemorySnapshot{}, err
		}
		identity += ":" + binding
		if first {
			source.namespaces = append(source.namespaces, file)
		} else if err := file.Close(); err != nil {
			return linuxMemorySnapshot{}, err
		}
	}
	membership, err := linuxMemoryReadAt(ctx, source.task, "cgroup", linuxMemoryMembershipBytes, source.procObject)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	mountinfo, err := linuxMemoryReadAt(ctx, source.task, "mountinfo", linuxMemoryMountBytes, source.procObject)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	return linuxMemorySnapshot{identity: digest([]byte(identity)), membership: membership, mountinfo: mountinfo,
		pid: source.pid, tid: source.tid, currentPID: currentPID, currentTID: currentTID}, nil
}

// A proc mount can belong to an ancestor PID namespace. Its kernel current-task
// links must translate back to our local IDs before numeric directories are used.
func (source *linuxMemoryNativeSource) currentAnchors() (int, int, error) {
	for _, anchor := range []struct{ name, target string }{
		{"self", strconv.Itoa(source.pid)},
		{"thread-self", strconv.Itoa(source.pid) + "/task/" + strconv.Itoa(source.tid)},
	} {
		if err := linuxMemoryCurrentLink(source.proc, source.procObject, anchor.name, anchor.target); err != nil {
			return 0, 0, err
		}
	}
	return source.pid, source.tid, nil
}

func linuxMemoryCurrentLink(parent *os.File, expected linuxMemoryObject, name, target string) (retErr error) {
	before, object, err := linuxMemoryOpenLink(parent, name, expected)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, before.Close()) }()
	var raw [linuxMemoryValueBytes + 1]byte
	size, err := unix.Readlinkat(int(parent.Fd()), name, raw[:])
	if err != nil || size > linuxMemoryValueBytes || string(raw[:size]) != target {
		return ErrLinuxMemoryObservation
	}
	after, confirmed, err := linuxMemoryOpenLink(parent, name, expected)
	if err != nil {
		return err
	}
	closeErr := after.Close()
	if object != confirmed {
		return errors.Join(ErrLinuxMemoryObservation, closeErr)
	}
	return closeErr
}

func linuxMemoryOpenLink(parent *os.File, name string, expected linuxMemoryObject) (_ *os.File, object linuxMemoryObject, retErr error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, object, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, file.Close())
		}
	}()
	object, err = linuxMemoryNativeObject(file, linuxMemoryProcMagic)
	var stat unix.Stat_t
	if err != nil || !linuxMemorySameMount(object, expected) || unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFLNK {
		return nil, object, ErrLinuxMemoryObservation
	}
	return file, object, nil
}

func (source *linuxMemoryNativeSource) namespace(ctx context.Context, name string, kind int) (result *os.File, binding string, retErr error) {
	var output *os.File
	// Registered first, so this runs after both temporary descriptors close.
	// A late close error revokes publication and closes the namespace descriptor.
	defer func() {
		if retErr != nil && output != nil {
			retErr = errors.Join(retErr, output.Close())
			result, binding = nil, ""
		}
	}()
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	directory, err := linuxMemoryOpenAt(source.task, "ns", true, source.procObject)
	if err != nil {
		return nil, "", err
	}
	defer func() { retErr = errors.Join(retErr, directory.Close()) }()
	// Verify the link itself is on the held proc mount. An overlaid namespace
	// descriptor must not masquerade as the kernel's current-thread magic link.
	link, before, err := linuxMemoryOpenLink(directory, name, source.procObject)
	if err != nil {
		return nil, "", err
	}
	defer func() { retErr = errors.Join(retErr, link.Close()) }()
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	file := os.NewFile(uintptr(fd), name)
	output = file
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	actualKind, kindErr := unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
	confirmed, after, afterErr := linuxMemoryOpenLink(directory, name, source.procObject)
	if afterErr == nil {
		afterErr = confirmed.Close()
	}
	if unix.Fstat(fd, &stat) != nil || unix.Fstatfs(fd, &filesystem) != nil || filesystem.Type != unix.NSFS_MAGIC ||
		kindErr != nil || actualKind != kind || afterErr != nil || before != after {
		return nil, "", ErrLinuxMemoryObservation
	}
	return file, fmt.Sprintf("%d:%d:%d", stat.Dev, stat.Ino, actualKind), nil
}

func (source *linuxMemoryNativeSource) meminfo(ctx context.Context) ([]byte, error) {
	return linuxMemoryReadAt(ctx, source.proc, "meminfo", linuxMemoryInfoBytes, source.procObject)
}

func (*linuxMemoryNativeSource) openRoot(ctx context.Context) (linuxMemoryDirectory, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	file, err := openNoFollowDirectory(linuxMemoryMountPoint)
	if err != nil {
		return nil, err
	}
	object, err := linuxMemoryNativeObject(file, linuxMemoryCgroupMagic)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return &linuxMemoryNativeDirectory{file, object}, nil
}

func (source *linuxMemoryNativeSource) close() error {
	var result error
	for _, file := range append(source.namespaces, source.task, source.process, source.proc) {
		if file != nil {
			result = errors.Join(result, file.Close())
		}
	}
	return result
}

type linuxMemoryNativeDirectory struct {
	file     *os.File
	expected linuxMemoryObject
}

func (directory *linuxMemoryNativeDirectory) object() (linuxMemoryObject, error) {
	return linuxMemoryNativeObject(directory.file, linuxMemoryCgroupMagic)
}

func (directory *linuxMemoryNativeDirectory) openChild(ctx context.Context, name string) (linuxMemoryDirectory, error) {
	if ctx.Err() != nil || name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return nil, ErrLinuxMemoryObservation
	}
	file, err := linuxMemoryOpenAt(directory.file, name, true, directory.expected)
	if err != nil {
		return nil, err
	}
	object, err := linuxMemoryNativeObject(file, linuxMemoryCgroupMagic)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return &linuxMemoryNativeDirectory{file, object}, nil
}

func (directory *linuxMemoryNativeDirectory) read(ctx context.Context, name string, maximum int) ([]byte, error) {
	return linuxMemoryReadAt(ctx, directory.file, name, maximum, directory.expected)
}

func (directory *linuxMemoryNativeDirectory) exists(ctx context.Context, name string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	file, err := linuxMemoryOpenAt(directory.file, name, false, directory.expected)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, file.Close()
}

func (directory *linuxMemoryNativeDirectory) close() error { return directory.file.Close() }

func linuxMemoryNativeObject(file *os.File, filesystemType int64) (linuxMemoryObject, error) {
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	var extended unix.Statx_t
	if unix.Fstat(int(file.Fd()), &stat) != nil || unix.Fstatfs(int(file.Fd()), &filesystem) != nil || filesystem.Type != filesystemType ||
		unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &extended) != nil ||
		extended.Mask&unix.STATX_MNT_ID == 0 || extended.Mnt_id == 0 || stat.Ino == 0 {
		return linuxMemoryObject{}, ErrLinuxMemoryObservation
	}
	return linuxMemoryObject{filesystemType, unix.Major(stat.Dev), unix.Minor(stat.Dev), stat.Ino, extended.Mnt_id}, nil
}

func linuxMemoryOpenAt(parent *os.File, name string, directory bool, expected linuxMemoryObject) (_ *os.File, retErr error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{Flags: uint64(flags),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return nil, err // No fallback can silently discard the resolution policy.
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, file.Close())
		}
	}()
	object, err := linuxMemoryNativeObject(file, expected.Filesystem)
	var stat unix.Stat_t
	if err != nil || !linuxMemorySameMount(object, expected) || unix.Fstat(fd, &stat) != nil ||
		directory && stat.Mode&unix.S_IFMT != unix.S_IFDIR || !directory && stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, ErrLinuxMemoryObservation
	}
	return file, nil
}

func linuxMemoryReadAt(ctx context.Context, parent *os.File, name string, maximum int, expected linuxMemoryObject) (_ []byte, retErr error) {
	if ctx.Err() != nil || maximum <= 0 {
		return nil, errors.Join(ErrLinuxMemoryObservation, ctx.Err())
	}
	file, err := linuxMemoryOpenAt(parent, name, false, expected)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	before, err := linuxMemoryNativeObject(file, expected.Filesystem)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	after, afterErr := linuxMemoryNativeObject(file, expected.Filesystem)
	if err != nil || afterErr != nil || before != after || len(raw) > maximum || ctx.Err() != nil {
		return nil, errors.Join(ErrLinuxMemoryObservation, ctx.Err())
	}
	return raw, nil
}
