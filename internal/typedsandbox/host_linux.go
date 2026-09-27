//go:build linux

package typedsandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"golang.org/x/sys/unix"
)

// PrepareHostScratch is prospective root-only host staging. It never provisions
// privileges, tools or a VM. A ready exact journal may resume only while empty;
// interrupted setup must first pass CleanupHostScratch, or retain ambiguous custody.
func PrepareHostScratch(ctx context.Context, o HostScratchOptions) (HostScratchReceipt, error) {
	lock, err := hostLock(ctx, o)
	if err != nil {
		return HostScratchReceipt{}, err
	}
	defer func() { _ = lock.Close() }()
	root := o.root()
	if _, err = os.Lstat(root); err == nil {
		j, e := loadHostOwner(root, o)
		if e != nil || j.Phase != "ready" {
			return HostScratchReceipt{}, ErrCustody
		}
		receipt, e := verifyHost(ctx, j)
		if e != nil || !scratchEmpty(j.Authority.Source) {
			return HostScratchReceipt{}, ErrCustody
		}
		return receipt, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return HostScratchReceipt{}, ErrCustody
	}
	if err = hostCapacity(ctx); err != nil {
		return HostScratchReceipt{}, err
	}
	if err = os.Mkdir(root, 0700); err != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	if err = syncParent(root); err != nil {
		return HostScratchReceipt{}, err
	}
	j := hostOwner{Schema: hostOwnerSchema, Options: o, Phase: "new", Loop: -1}
	if err = writeHostOwner(root, j, true); err != nil {
		return HostScratchReceipt{}, err
	}
	image, err := os.OpenFile(root+"/image.ext4", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	defer func() { _ = image.Close() }()
	var st unix.Stat_t
	if unix.Fstat(int(image.Fd()), &st) != nil || image.Sync() != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	j.ImageDevice, j.ImageInode = uint64(st.Dev), st.Ino
	j.Phase = "image"
	if err = writeHostOwner(root, j, false); err != nil {
		return HostScratchReceipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return HostScratchReceipt{}, err
	}
	if unix.Fallocate(int(image.Fd()), 0, 0, hostImageBytes) != nil || image.Sync() != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	j.Phase = "allocated"
	if !hostImageMatches(image, j, true) {
		return HostScratchReceipt{}, ErrCustody
	}
	if err = writeHostOwner(root, j, false); err != nil {
		return HostScratchReceipt{}, err
	}
	// A lost formatter owner is not automatically reclaimable: a direct-child
	// parent-death signal cannot prove the lifetime of escaped descendants.
	j.Phase = "formatting"
	if err = writeHostOwner(root, j, false); err != nil {
		return HostScratchReceipt{}, err
	}
	if err = runHostMkfs(ctx, o, image); err != nil {
		return HostScratchReceipt{}, err
	}
	if image.Sync() != nil || !hostImageMatches(image, j, true) {
		return HostScratchReceipt{}, ErrCustody
	}
	j.Phase = "formatted"
	if err = writeHostOwner(root, j, false); err != nil {
		return HostScratchReceipt{}, err
	}
	control, err := unix.Open("/dev/loop-control", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return HostScratchReceipt{}, ErrRefused
	}
	number, err := unix.IoctlRetInt(control, unix.LOOP_CTL_GET_FREE)
	_ = unix.Close(control)
	if err != nil || number < 0 || number > 1048575 {
		return HostScratchReceipt{}, ErrRefused
	}
	j.Loop = number
	j.Phase = "loop-selected"
	// The chosen device is durable BEFORE either kernel attachment or mount.
	if err = writeHostOwner(root, j, false); err != nil {
		return HostScratchReceipt{}, err
	}
	loop, err := openHostLoop(number)
	if err != nil {
		return HostScratchReceipt{}, err
	}
	defer func() { _ = loop.Close() }()
	if _, err = unix.IoctlLoopGetStatus64(int(loop.Fd())); !errors.Is(err, unix.ENXIO) {
		return HostScratchReceipt{}, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return HostScratchReceipt{}, err
	}
	config := unix.LoopConfig{Fd: uint32(image.Fd()), Info: unix.LoopInfo64{Flags: unix.LO_FLAGS_DIRECT_IO}}
	if unix.IoctlLoopConfigure(int(loop.Fd()), &config) != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	if err = verifyHostLoop(loop, j); err != nil {
		return HostScratchReceipt{}, err
	}
	mount := root + "/scratch"
	if os.Mkdir(mount, 0700) != nil || syncParent(mount) != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return HostScratchReceipt{}, err
	}
	if unix.Mount(loop.Name(), mount, "ext4", unix.MS_NOSUID|unix.MS_NODEV, "") != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	j.Authority, err = observeHostScratch(j)
	if err != nil {
		return HostScratchReceipt{}, err
	}
	// mkfs creates only this empty directory. Never recursively remove its contents.
	lost := mount + "/lost+found"
	if st, e := os.Lstat(lost); e == nil {
		if !st.IsDir() || !scratchEmpty(lost) || os.Remove(lost) != nil {
			return HostScratchReceipt{}, ErrCustody
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return HostScratchReceipt{}, ErrCustody
	}
	if !scratchEmpty(mount) {
		return HostScratchReceipt{}, ErrCustody
	}
	if unix.Chmod(mount, 0777) != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	dir, e := os.Open(mount)
	if e != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	e = errors.Join(dir.Sync(), dir.Close())
	if e != nil {
		return HostScratchReceipt{}, ErrCustody
	}
	j.Phase = "ready"
	if err = writeHostOwner(root, j, false); err != nil {
		return HostScratchReceipt{}, err
	}
	return verifyHost(ctx, j)
}

func VerifyHostScratch(ctx context.Context, o HostScratchOptions) (HostScratchReceipt, error) {
	lock, err := hostLock(ctx, o)
	if err != nil {
		return HostScratchReceipt{}, err
	}
	defer func() { _ = lock.Close() }()
	j, err := loadHostOwner(o.root(), o)
	if err != nil || j.Phase != "ready" {
		return HostScratchReceipt{}, ErrCustody
	}
	return verifyHost(ctx, j)
}

// CleanupHostScratch never forces/lazily unmounts or deletes a container. It
// retains the journal on ambiguity. A detached/absent owned device is resumable.
// The operator must dedicate the selected daemon to this owner and serialize all
// container mutations through its lifecycle. Any existing container,
// including stopped or unrelated containers, refuses cleanup; none is deleted.
// This availability tradeoff makes no shared-daemon coexistence claim.
func CleanupHostScratch(ctx context.Context, o HostScratchOptions) error {
	lock, err := hostLock(ctx, o)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	root := o.root()
	j, err := loadHostOwner(root, o)
	if err != nil {
		return err
	}
	if !hostCleanupPhase(j.Phase) {
		return ErrCustody
	}
	if err = closedHostEntries(root); err != nil {
		return err
	}
	if err = hostNoContainers(ctx, j); err != nil {
		return err
	}
	imagePath := root + "/image.ext4"
	var image *os.File
	if j.ImageInode != 0 {
		if _, e := os.Lstat(imagePath); errors.Is(e, os.ErrNotExist) && j.Phase == "retiring" {
			image = nil
		} else {
			image, err = openHostImage(j)
			if err != nil {
				return err
			}
			defer func() {
				if image != nil {
					_ = image.Close()
				}
			}()
			if !hostImageMatches(image, j, hostFullImageRequired(j.Phase)) {
				return ErrCustody
			}
		}
	} else if _, err = os.Lstat(imagePath); !errors.Is(err, os.ErrNotExist) {
		return ErrCustody
	}
	mounted, err := hostMountPresent(root + "/scratch")
	if err != nil {
		return err
	}
	if mounted {
		if j.Loop < 0 {
			return ErrCustody
		}
		loop, e := openHostLoop(j.Loop)
		if e != nil {
			return e
		}
		e = verifyHostLoop(loop, j)
		_ = loop.Close()
		if e != nil {
			return e
		}
		actual, e := observeHostScratch(j)
		if e != nil || j.Phase == "ready" && actual != j.Authority {
			return ErrCustody
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if unix.Unmount(root+"/scratch", 0) != nil {
			return ErrCustody
		}
	}
	if j.Loop >= 0 {
		loop, e := openHostLoop(j.Loop)
		if e != nil {
			return e
		}
		defer func() { _ = loop.Close() }()
		_, e = unix.IoctlLoopGetStatus64(int(loop.Fd()))
		if !errors.Is(e, unix.ENXIO) {
			if e != nil || verifyHostLoop(loop, j) != nil {
				return ErrCustody
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if unix.IoctlSetInt(int(loop.Fd()), unix.LOOP_CLR_FD, 0) != nil {
				return ErrCustody
			}
			if _, e = unix.IoctlLoopGetStatus64(int(loop.Fd())); !errors.Is(e, unix.ENXIO) {
				return ErrCustody
			}
		}
	}
	if mounted, err = hostMountPresent(root + "/scratch"); err != nil || mounted {
		return ErrCustody
	}
	// Keep the image identity journal until the image unlink is durable. If death
	// occurs after unlink, the next cleanup requires the separately journaled phase.
	if j.Phase != "new" {
		j.Phase = "retiring"
		j.Loop = -1
		j.Authority = ScratchAuthority{}
		if err = writeHostOwner(root, j, false); err != nil {
			return err
		}
	}
	if image != nil {
		if err = image.Close(); err != nil {
			return ErrCustody
		}
		image = nil
	}
	for _, name := range []string{"scratch", "image.ext4"} {
		if err = os.Remove(root + "/" + name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrCustody
		}
	}
	if err = syncParent(root + "/owner.json"); err != nil {
		return err
	}
	for _, name := range []string{"owner.next", "owner.json"} {
		if err = os.Remove(root + "/" + name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrCustody
		}
	}
	if err = syncParent(root + "/owner.json"); err != nil {
		return err
	}
	if os.Remove(root) != nil {
		return ErrCustody
	}
	return syncParent(root)
}

func hostLock(ctx context.Context, o HostScratchOptions) (*os.File, error) {
	if err := hostAllowed(ctx, o); err != nil {
		return nil, err
	}
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return nil, ErrRefused
	}
	for _, ancestor := range []string{"/", "/var", "/var/lib"} {
		var st unix.Stat_t
		if unix.Lstat(ancestor, &st) != nil || !hostAncestorMetadata(st.Mode, st.Uid, st.Gid) {
			return nil, ErrCustody
		}
	}
	if err := hostDirectory(HostScratchBase); err != nil {
		return nil, err
	}
	fd, err := unix.Open(HostScratchBase+"/.lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrCustody
	}
	f := os.NewFile(uintptr(fd), "scratch-owner-lock")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || !hostPrivateFileMetadata(st.Mode, st.Uid, st.Gid, uint64(st.Nlink)) {
		_ = f.Close()
		return nil, ErrCustody
	}
	return hostAcquireLock(ctx, f)
}

// hostAcquireLock owns f on entry and closes it on every refusal.
func hostAcquireLock(ctx context.Context, f *os.File) (*os.File, error) {
	var err error
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return f, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, ErrCustody
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-timeout.C:
			_ = f.Close()
			return nil, ErrCustody
		case <-tick.C:
		}
	}
}
func hostDirectory(path string) error {
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || actual != path {
		return ErrCustody
	}
	var st unix.Stat_t
	if unix.Lstat(path, &st) != nil || st.Mode&0177777 != unix.S_IFDIR|0700 || st.Uid != 0 || st.Gid != 0 {
		return ErrCustody
	}
	return nil
}

var hostCapacityGate = lifecycle.NewGate(HostScratchBase)

func hostCapacity(ctx context.Context) error {
	fd, err := unix.Open(HostScratchBase, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrRefused
	}
	defer func() { _ = unix.Close(fd) }()
	var s unix.Statfs_t
	if unix.Fstatfs(fd, &s) != nil || s.Bsize <= 0 || s.Blocks > uint64(math.MaxInt64)/uint64(s.Bsize) || s.Bavail > s.Blocks {
		return ErrRefused
	}
	total, available := int64(s.Blocks)*int64(s.Bsize), int64(s.Bavail)*int64(s.Bsize)
	capacity := lifecycle.Capacity{TotalBytes: total, AvailableBytes: available, UsedBytes: total - available}
	observed, err := hostCapacityGate.CheckObserved(ctx, capacity, hostImageBytes+65536)
	if err != nil || observed.Pressure != lifecycle.PressureNormal || !hostInodesFree(s.Files, s.Ffree) {
		return ErrRefused
	}
	return nil
}

func writeHostOwner(root string, j hostOwner, initial bool) error {
	raw, err := json.Marshal(j)
	if err != nil || len(raw) > 8192 {
		return ErrCustody
	}
	name := root + "/owner.json"
	if !initial {
		name = root + "/owner.next"
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrCustody
	}
	_, err = f.Write(raw)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return ErrCustody
	}
	if !initial && os.Rename(name, root+"/owner.json") != nil {
		return ErrCustody
	}
	return syncParent(name)
}
func loadHostOwner(root string, o HostScratchOptions) (hostOwner, error) {
	if err := hostDirectory(root); err != nil {
		return hostOwner{}, err
	}
	for _, name := range []string{"owner.json", "owner.next"} {
		var st unix.Stat_t
		err := unix.Lstat(root+"/"+name, &st)
		if name == "owner.next" && errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil || !hostPrivateFileMetadata(st.Mode, st.Uid, st.Gid, uint64(st.Nlink)) {
			return hostOwner{}, ErrCustody
		}
	}
	j, err := readHostOwner(root, o)
	if err != nil {
		return j, err
	}
	if _, err = os.Lstat(root + "/owner.next"); err == nil {
		next, e := readHostOwnerFile(root+"/owner.next", o)
		if e != nil || !hostOwnerAdvance(j, next) {
			return j, ErrCustody
		}
		// Complete atomic replacement is durable before any next-stage mutation.
		if os.Rename(root+"/owner.next", root+"/owner.json") != nil || syncParent(root+"/owner.json") != nil {
			return j, ErrCustody
		}
		return next, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return j, ErrCustody
	}
	return j, nil
}
func openHostImage(j hostOwner) (*os.File, error) {
	fd, err := unix.Open(j.Options.root()+"/image.ext4", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrCustody
	}
	f := os.NewFile(uintptr(fd), "scratch-image")
	if !hostImageMatches(f, j, false) {
		_ = f.Close()
		return nil, ErrCustody
	}
	return f, nil
}
func hostImageMatches(f *os.File, j hostOwner, full bool) bool {
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil || !hostPrivateFileMetadata(st.Mode, st.Uid, st.Gid, uint64(st.Nlink)) || uint64(st.Dev) != j.ImageDevice || st.Ino != j.ImageInode || st.Size < 0 || st.Size > hostImageBytes {
		return false
	}
	return hostImageAllocation(st.Size, st.Blocks, full)
}
func openHostLoop(number int) (*os.File, error) {
	name := "/dev/loop" + strconv.Itoa(number)
	fd, err := unix.Open(name, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrCustody
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFBLK || unix.Major(uint64(st.Rdev)) != 7 || unix.Minor(uint64(st.Rdev)) != uint32(number) {
		_ = f.Close()
		return nil, ErrCustody
	}
	return f, nil
}
func verifyHostLoop(f *os.File, j hostOwner) error {
	st, err := unix.IoctlLoopGetStatus64(int(f.Fd()))
	if err != nil {
		return ErrCustody
	}
	if !hostLoopMatches(j, hostLoopObservation{st.Number, st.Device, st.Inode, st.Offset, st.Sizelimit, st.Encrypt_type, st.Encrypt_key_size, st.Flags}) {
		return ErrCustody
	}
	return nil
}
func observeHostScratch(j hostOwner) (ScratchAuthority, error) {
	path := j.Options.root() + "/scratch"
	var fs unix.Statfs_t
	var st unix.Stat_t
	if unix.Statfs(path, &fs) != nil || unix.Stat(path, &st) != nil || fs.Type != unix.EXT4_SUPER_MAGIC || fs.Bsize != 4096 || fs.Files != ScratchInodes || fs.Bfree > fs.Blocks || fs.Bavail > fs.Bfree || fs.Ffree > fs.Files || unix.Major(uint64(st.Dev)) != 7 || unix.Minor(uint64(st.Dev)) != uint32(j.Loop) {
		return ScratchAuthority{}, ErrCustody
	}
	a := ScratchAuthority{Source: path, DeviceMajor: 7, DeviceMinor: uint32(j.Loop), BlockSize: 4096, Blocks: fs.Blocks, Inodes: fs.Files, ImageBytes: hostImageBytes}
	raw, err := readSmall("/proc/self/mountinfo", 1<<20)
	if err != nil || a.Validate() != nil || verifyScratchMountpoint(string(raw), a, path) != nil {
		return ScratchAuthority{}, ErrCustody
	}
	return a, nil
}
func hostMountPresent(path string) (bool, error) {
	raw, err := readSmall("/proc/self/mountinfo", 1<<20)
	if err != nil {
		return false, ErrCustody
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return false, ErrCustody
		}
		if strings.HasPrefix(fields[4], path+"/") {
			return false, ErrCustody
		}
		if fields[4] == path {
			if found {
				return false, ErrCustody
			}
			found = true
		}
	}
	return found, nil
}
func verifyHost(ctx context.Context, j hostOwner) (HostScratchReceipt, error) {
	if err := ctx.Err(); err != nil {
		return HostScratchReceipt{}, err
	}
	image, err := openHostImage(j)
	if err != nil {
		return HostScratchReceipt{}, err
	}
	defer func() { _ = image.Close() }()
	if !hostImageMatches(image, j, true) {
		return HostScratchReceipt{}, ErrCustody
	}
	loop, err := openHostLoop(j.Loop)
	if err != nil {
		return HostScratchReceipt{}, err
	}
	defer func() { _ = loop.Close() }()
	if err = verifyHostLoop(loop, j); err != nil {
		return HostScratchReceipt{}, err
	}
	a, err := observeHostScratch(j)
	if err != nil || a != j.Authority {
		return HostScratchReceipt{}, ErrCustody
	}
	return HostScratchReceipt{Schema: hostOwnerSchema, Options: j.Options, Authority: a, ObservedDirectIO: true}, nil
}
func hostNoContainers(ctx context.Context, j hostOwner) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := hostContainers(ctx, j.Options.Socket)
	if err != nil {
		return err
	}
	if hostContainerCustody(list) {
		return ErrCustody
	}
	return nil
}
func closedHostEntries(root string) error {
	f, err := os.Open(root)
	if err != nil {
		return ErrCustody
	}
	entries, err := f.ReadDir(5)
	closeErr := f.Close()
	if err != nil && err != io.EOF || closeErr != nil || len(entries) > 4 {
		return ErrCustody
	}
	for _, e := range entries {
		if e.Name() != "owner.json" && e.Name() != "owner.next" && e.Name() != "image.ext4" && e.Name() != "scratch" {
			return ErrCustody
		}
		if e.Type()&os.ModeSymlink != 0 {
			return ErrCustody
		}
	}
	return nil
}

type hostOutput struct {
	mu     sync.Mutex
	n      int
	cancel context.CancelFunc
}

func (b *hostOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > 65536-b.n {
		b.cancel()
		return 0, ErrRefused
	}
	b.n += len(p)
	return len(p), nil
}
func runHostMkfs(ctx context.Context, o HostScratchOptions, image *os.File) error {
	if hostSelected() {
		return ErrRefused
	}
	f, err := os.Open(HostMkfsPath)
	if err != nil {
		return ErrRefused
	}
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Gid != 0 || st.Mode&0022 != 0 || st.Mode&0111 == 0 || st.Size <= 0 || st.Size > 32<<20 {
		return ErrRefused
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, 32<<20+1))
	if err != nil || n != st.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != o.MkfsDigest {
		return ErrRefused
	}
	if _, err = f.Seek(0, 0); err != nil {
		return ErrRefused
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", mkfsArgs("/proc/self/fd/4")...)
	// mke2fs selects the filesystem type from argv[0], while Path pins the open executable.
	cmd.Args[0] = HostMkfsPath
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "HOME=/nonexistent", "MKE2FS_CONFIG=/dev/null"}
	cmd.Dir = HostScratchBase
	cmd.ExtraFiles = []*os.File{f, image}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	output := &hostOutput{cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = output
	return runHostFormatter(cmd)
}

func runHostFormatter(cmd *exec.Cmd) error {
	// Linux Pdeathsig follows the creating thread. Keep that thread alive until
	// the direct child has been joined; this does not establish descendant custody.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := cmd.Start(); err != nil {
		return ErrRefused
	}
	runErr := cmd.Wait()
	group := -cmd.Process.Pid
	if err := syscall.Kill(group, 0); !errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(group, syscall.SIGKILL)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if err = syscall.Kill(group, 0); errors.Is(err, syscall.ESRCH) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		// Even if killed group members disappear, retain formatting custody: an
		// unexpected descendant means the reviewed formatter recipe did not hold.
		return ErrCustody
	}
	if runErr != nil {
		return ErrRefused
	}
	return nil
}
