//go:build linux

package t421

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// LinuxPressureVolume owns its newly created backing image, loop association,
// private ext4 mount and native descriptors. It is a root-only trusted-host
// staging primitive, not an unprivileged pressure executor or ceremony entry.
// Initial-user-namespace privilege must be admitted externally. Neither EUID0
// nor a capability in the current user namespace establishes that prerequisite.
// This package never installs a helper, changes sudo policy or grants a cap.
// Its private backing-mount context may be supplied by a future admitted
// privileged launcher; no permanent host-mount provisioning is implied.
type LinuxPressureVolume struct {
	mu                                          sync.Mutex
	request                                     LinuxPressureRequest
	parent, root, underlay, mount, workspace    linuxPressureDirectory
	lock, image, loop                           *os.File
	lockNode                                    linuxPressureNode
	loopNode                                    linuxPressureNode
	backingGate                                 *lifecycle.Gate
	formatter                                   *linuxPressureFormatter
	record                                      linuxPressureRecord
	loopNumber                                  int
	token                                       string
	durablePhase                                string
	rootCreated                                 bool
	ready, failed, closed, removalUsed, removed bool
	formatterSession                            int
	formatterJoined, formatterEmpty             bool
}

// PrepareLinuxPressureVolume creates no supplied image or mount. Obvious
// missing privilege/tool/base/capacity refusals precede the large reservation.
// Effective CAP_SYS_ADMIN is a necessary early check, not proof of initial
// user-namespace privilege; the kernel mount still enforces that boundary.
// A nonnil owner on error retains its actual prefix. Close only releases safe
// descriptors; failed construction never retries or deletes partial custody.
func PrepareLinuxPressureVolume(ctx context.Context, request LinuxPressureRequest) (_ *LinuxPressureVolume, retErr error) {
	if linuxPressureContext(ctx) != nil || !validLinuxPressureRequest(request) || runtime.GOARCH != "amd64" ||
		os.Geteuid() != 0 || os.Getegid() != 0 || !linuxPressureEffectiveMountCapability() {
		return nil, ErrLinuxPressure
	}
	owner := &LinuxPressureVolume{request: request, loopNumber: -1}
	defer func() {
		if retErr != nil {
			owner.failed = true
			if !owner.rootCreated {
				_ = owner.Close()
			}
		}
	}()
	var err error
	owner.parent, err = openLinuxPressureDirectory(ctx, request.Parent, false)
	if err != nil {
		return owner, ErrLinuxPressure
	}
	owner.formatter, err = holdLinuxPressureFormatter(ctx, request.FormatterSHA256)
	if err != nil {
		return owner, err
	}
	if owner.acquireLock(ctx) != nil || owner.checkBacking(ctx, true) != nil || owner.selectLoop(ctx) != nil {
		return owner, ErrLinuxPressure
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil || ctx.Err() != nil {
		return owner, ErrLinuxPressure
	}
	name := "t42h2m-" + hex.EncodeToString(token[:])
	if err = unix.Mkdirat(int(owner.parent.file.Fd()), name, 0o700); err != nil {
		return owner, ErrLinuxPressure
	}
	owner.rootCreated = true
	owner.token = hex.EncodeToString(token[:])
	owner.root.path = filepath.Join(request.Parent, name)
	owner.root, err = openLinuxPressureDirectory(ctx, owner.root.path, false)
	if err != nil || owner.root.node.Device != owner.parent.node.Device || owner.parent.file.Sync() != nil {
		return owner, ErrLinuxPressure
	}
	owner.record = linuxPressureRecord{Schema: "phebs-linux-pressure-owner-v1", Token: hex.EncodeToString(token[:]), Phase: "created",
		Parent: owner.parent.node, Root: owner.root.node, ImageBytes: request.ImageBytes, FormatterSHA256: request.FormatterSHA256, FormatterRole: linuxPressureRole, Loop: -1}
	if owner.writeRecord(ctx, owner.record, true) != nil || unix.Mkdirat(int(owner.root.file.Fd()), "mount", 0o700) != nil {
		return owner, ErrLinuxPressure
	}
	owner.underlay, err = openLinuxPressureDirectory(ctx, filepath.Join(owner.root.path, "mount"), false)
	if err != nil || owner.underlay.node.Device != owner.parent.node.Device || owner.root.file.Sync() != nil {
		return owner, ErrLinuxPressure
	}
	fd, err := unix.Openat(int(owner.root.file.Fd()), "image.ext4", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return owner, ErrLinuxPressure
	}
	owner.image = os.NewFile(uintptr(fd), "linux-pressure-backing-image")
	var image unix.Stat_t
	if unix.Fstat(fd, &image) != nil || !linuxPressurePrivateFile(image) || image.Size != 0 || uint64(image.Dev) != owner.parent.node.Device {
		return owner, ErrLinuxPressure
	}
	next := owner.record
	next.Image, next.Phase = linuxPressureNode{Device: uint64(image.Dev), Inode: image.Ino}, "image"
	if owner.image.Sync() != nil || owner.writeRecord(ctx, next, false) != nil || owner.checkBacking(ctx, true) != nil {
		return owner, ErrLinuxPressure
	}
	// One complete new-file allocation lineage, never a sparse image or an
	// aggregate st_blocks count presented as proof of arbitrary extent coverage.
	if ctx.Err() != nil || unix.Fallocate(fd, 0, 0, int64(request.ImageBytes)) != nil || ctx.Err() != nil ||
		owner.image.Truncate(int64(request.ImageBytes)) != nil || owner.image.Sync() != nil || owner.checkImage(ctx) != nil ||
		owner.checkBacking(ctx, false) != nil || owner.advance(ctx, "allocated") != nil || owner.advance(ctx, "formatting") != nil {
		return owner, ErrLinuxPressure
	}
	if owner.format(ctx) != nil || owner.advance(ctx, "formatted") != nil || owner.checkBacking(ctx, false) != nil {
		return owner, ErrLinuxPressure
	}
	next = owner.record
	next.Loop, next.Phase = owner.loopNumber, "loop-selected"
	if owner.writeRecord(ctx, next, false) != nil || ctx.Err() != nil {
		return owner, ErrLinuxPressure
	}
	if _, err = unix.IoctlLoopGetStatus64(int(owner.loop.Fd())); !errors.Is(err, unix.ENXIO) {
		return owner, ErrLinuxPressure
	}
	configuration := unix.LoopConfig{Fd: uint32(owner.image.Fd()), Size: 4096,
		Info: unix.LoopInfo64{Flags: unix.LO_FLAGS_DIRECT_IO, Sizelimit: request.ImageBytes}}
	if unix.IoctlLoopConfigure(int(owner.loop.Fd()), &configuration) != nil || owner.checkLoop(ctx) != nil || owner.advance(ctx, "attached") != nil {
		return owner, ErrLinuxPressure
	}
	if owner.checkBacking(ctx, false) != nil || ctx.Err() != nil || unix.Mount(owner.loop.Name(), owner.underlay.path, "ext4", unix.MS_NOSUID|unix.MS_NODEV, "nodiscard") != nil ||
		unix.Mount("", owner.underlay.path, "", unix.MS_PRIVATE|unix.MS_REC, "") != nil || owner.advance(ctx, "mounted") != nil {
		return owner, ErrLinuxPressure
	}
	owner.mount, err = openLinuxPressureDirectory(ctx, owner.underlay.path, true)
	if err != nil {
		return owner, ErrLinuxPressure
	}
	if owner.removeFreshLostFound(ctx) != nil || unix.Mkdirat(int(owner.mount.file.Fd()), "workspace", 0o700) != nil || owner.mount.file.Sync() != nil {
		return owner, ErrLinuxPressure
	}
	owner.workspace, err = openLinuxPressureDirectory(ctx, filepath.Join(owner.mount.path, "workspace"), false)
	if err != nil {
		return owner, ErrLinuxPressure
	}
	geometry, err := owner.observeGeometry(ctx)
	if err != nil || owner.checkImage(ctx) != nil || owner.checkBacking(ctx, false) != nil {
		return owner, ErrLinuxPressure
	}
	next = owner.record
	next.Geometry, next.Phase = geometry, "ready"
	if owner.writeRecord(ctx, next, false) != nil {
		return owner, ErrLinuxPressure
	}
	owner.ready = true
	if _, err = owner.check(ctx); err != nil {
		return owner, ErrLinuxPressure
	}
	return owner, nil
}

// Geometry performs bounded native identity/capacity observations only. It
// does not supply a pressure target, process-drain result or admission token.
func (owner *LinuxPressureVolume) Geometry(ctx context.Context) (LinuxPressureGeometry, error) {
	if owner == nil {
		return LinuxPressureGeometry{}, ErrLinuxPressure
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	geometry, err := owner.check(ctx)
	if err != nil {
		owner.failed = true
	}
	return geometry, err
}

// RetainedOwner reports only the randomized owning prefix token and last
// durable phase, never a mount pathname or loan capability. The operator can
// locate t42h2m-<token> under its already supplied Parent after a refusal. A
// missing phase means construction stopped before the first durable journal.
func (owner *LinuxPressureVolume) RetainedOwner() (token, phase string, removed bool) {
	if owner == nil {
		return "", "", false
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.token, owner.durablePhase, owner.removed
}

// WithRoot lends one temporary directory descriptor, never a pathname or a
// mount-admission claim. It serializes Close/removal/other loans for the whole
// callback. Trusted callers must join ALL uses before returning and must not
// retain aliases/mappings, replace this FD or recursively invoke owner methods.
// A returned callback never certifies joined descendants. Native nonforced
// unmount/exact detach remain mandatory; the future phase binder must retain
// its actual executor/session ownership independently.
func (owner *LinuxPressureVolume) WithRoot(ctx context.Context, callback func(*os.File) error) (retErr error) {
	if owner == nil || callback == nil || linuxPressureContext(ctx) != nil {
		return ErrLinuxPressure
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if _, err := owner.check(ctx); err != nil {
		owner.failed = true
		return err
	}
	fd, err := unix.Openat(int(owner.workspace.file.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrLinuxPressure
	}
	loan := os.NewFile(uintptr(fd), "linux-pressure-workspace-loan")
	completed := false
	defer func() {
		retErr = errors.Join(retErr, loan.Close())
		if !completed || retErr != nil {
			owner.failed = true
		}
	}()
	info, err := loan.Stat()
	if err != nil || !os.SameFile(owner.workspace.info, info) {
		return ErrLinuxPressure
	}
	if err = callback(loan); err != nil {
		return errors.Join(ErrLinuxPressure, err)
	}
	info, err = loan.Stat()
	if err != nil || !os.SameFile(owner.workspace.info, info) {
		return ErrLinuxPressure
	}
	if _, err = owner.check(ctx); err != nil {
		return err
	}
	completed = true
	return nil
}

// Remove consumes one cleanup attempt. No force/lazy unmount, mounted-tree
// deletion or callback-issued drain assertion is accepted. Any uncertainty
// retains the actual prefix and cannot be silently retried by this owner.
func (owner *LinuxPressureVolume) Remove(ctx context.Context) (retErr error) {
	if owner == nil || linuxPressureContext(ctx) != nil {
		return ErrLinuxPressure
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	defer func() {
		if retErr != nil {
			owner.failed = true
		}
	}()
	if owner.removed {
		return nil
	}
	if owner.removalUsed || owner.failed || owner.closed {
		return ErrLinuxPressure
	}
	if _, err := owner.check(ctx); err != nil || owner.closedRootEntries(ctx) != nil {
		return ErrLinuxPressure
	}
	owner.removalUsed = true
	if owner.advance(ctx, "retiring") != nil || owner.formatterSession <= 0 || !owner.formatterJoined || !owner.formatterEmpty {
		return ErrLinuxPressure
	}
	deadline, _ := ctx.Deadline()
	if t4013.WaitPrivateProcessSession(owner.formatterSession, deadline) != nil {
		return ErrLinuxPressure
	}
	owner.ready = false
	if errors.Join(owner.workspace.file.Close(), owner.mount.file.Close()) != nil {
		return ErrLinuxPressure
	}
	owner.workspace.file, owner.mount.file = nil, nil
	if ctx.Err() != nil || unix.Unmount(owner.underlay.path, unix.UMOUNT_NOFOLLOW) != nil || owner.checkUnderlay(ctx) != nil || owner.advance(ctx, "unmounted") != nil {
		return ErrLinuxPressure
	}
	if owner.checkLoop(ctx) != nil || ctx.Err() != nil || unix.IoctlSetInt(int(owner.loop.Fd()), unix.LOOP_CLR_FD, 0) != nil {
		return ErrLinuxPressure
	}
	// LOOP_CLR_FD can defer a busy device's destruction. Success alone never
	// authorizes deleting backing storage; require actual detached status.
	if _, err := unix.IoctlLoopGetStatus64(int(owner.loop.Fd())); !errors.Is(err, unix.ENXIO) || owner.checkUnderlay(ctx) != nil || owner.advance(ctx, "detached") != nil {
		return ErrLinuxPressure
	}
	if t4013.WaitPrivateProcessSession(owner.formatterSession, deadline) != nil || owner.checkImage(ctx) != nil || owner.checkRoots(ctx) != nil {
		return ErrLinuxPressure
	}
	if owner.image.Close() != nil {
		return ErrLinuxPressure
	}
	owner.image = nil
	if ctx.Err() != nil || unix.Unlinkat(int(owner.root.file.Fd()), "image.ext4", 0) != nil || owner.root.file.Sync() != nil || owner.advance(ctx, "image-removed") != nil {
		return ErrLinuxPressure
	}
	if owner.checkUnderlay(ctx) != nil || unix.Unlinkat(int(owner.root.file.Fd()), "mount", unix.AT_REMOVEDIR) != nil ||
		owner.checkRecord(ctx) != nil || unix.Unlinkat(int(owner.root.file.Fd()), "owner.json", 0) != nil || owner.root.file.Sync() != nil ||
		owner.checkRoots(ctx) != nil || unix.Unlinkat(int(owner.parent.file.Fd()), filepath.Base(owner.root.path), unix.AT_REMOVEDIR) != nil || owner.parent.file.Sync() != nil {
		return ErrLinuxPressure
	}
	if _, err := os.Lstat(owner.root.path); !errors.Is(err, os.ErrNotExist) || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	owner.removed = true
	return nil
}

// Close joins no callback or unknown process and deletes nothing. A failed
// formatter join retains descriptors as well as disk custody. An owned mount
// or loop is not detached merely because its safe descriptors are closed.
func (owner *LinuxPressureVolume) Close() error {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return nil
	}
	if owner.formatterSession > 0 && (!owner.formatterJoined || !owner.formatterEmpty) {
		return ErrLinuxPressure
	}
	owner.closed = true
	var err error
	files := []*os.File{owner.workspace.file, owner.mount.file, owner.underlay.file, owner.image, owner.loop, owner.root.file, owner.lock, owner.parent.file}
	if owner.formatter != nil {
		files = append(files, owner.formatter.file)
	}
	for _, file := range files {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
	}
	return err
}

func linuxPressureContext(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || !time.Now().Before(deadline) {
		return ErrLinuxPressure
	}
	return nil
}

func linuxPressureEffectiveMountCapability() bool {
	var data [2]unix.CapUserData
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	return unix.Capget(&header, &data[0]) == nil && data[0].Effective&(1<<unix.CAP_SYS_ADMIN) != 0
}
