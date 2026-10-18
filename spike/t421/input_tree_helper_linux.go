//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// LinuxInputTreeHelperMain is only the fixed sealed construction endpoint. It
// accepts no paths/commands/flags and starts no child. Its caller immediately
// exits with this status; mount/namespace failures reveal no native diagnostic.
func LinuxInputTreeHelperMain() int {
	if runtime.GOARCH != "amd64" || len(os.Args) != 2 || os.Args[1] != linuxInputTreeRecipe ||
		os.Geteuid() != 0 || os.Getegid() != 0 || os.Getuid() != 0 || os.Getgid() != 0 {
		return 125
	}
	for _, fd := range []int{3, 4, 5} {
		unix.CloseOnExec(fd)
	}
	kind, err := unix.GetsockoptInt(4, unix.SOL_SOCKET, unix.SO_TYPE)
	peer, peerErr := unix.GetsockoptUcred(4, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil || kind != unix.SOCK_SEQPACKET || peerErr != nil || peer.Pid != int32(os.Getppid()) || peer.Uid != 0 || peer.Gid != 0 {
		return 125
	}
	wire := &inputTreeSocket{fd: 4}
	startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, _, err := wire.receive(startup, treeWireInit, false)
	cancel()
	if err != nil {
		return 125
	}
	initial := bytes.Clone(raw)
	ctx, cancel, err := inputTreeDeadline(initial)
	if err != nil {
		return 125
	}
	defer cancel()
	if !inputTreeHelperMappings(initial) || setupInputTreeMount() != nil {
		return 126
	}
	if receiveInputTree(ctx, wire, initial[16:]) != nil {
		return 126
	}
	return 0
}

func inputTreeHelperMappings(initial []byte) bool {
	for _, mapping := range []struct {
		path string
		id   uint32
	}{{"/proc/self/uid_map", binary.BigEndian.Uint32(initial[8:])}, {"/proc/self/gid_map", binary.BigEndian.Uint32(initial[12:])}} {
		file, err := os.Open(mapping.path)
		if err != nil {
			return false
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 128))
		closeErr := file.Close()
		values := strings.Fields(string(raw))
		if readErr != nil || closeErr != nil || len(raw) >= 128 || len(values) != 3 ||
			values[0] != "0" || values[1] != strconv.FormatUint(uint64(mapping.id), 10) || values[2] != "1" {
			return false
		}
	}
	return true
}

func setupInputTreeMount() (retErr error) {
	var mountpoint, linked unix.Stat_t
	priorMount, err := inputTreeMountID(5)
	name, nameErr := os.Readlink("/proc/self/fd/5")
	if err != nil || nameErr != nil || strings.HasSuffix(name, " (deleted)") || !executionGitAbsolutePath(name) ||
		unix.Fstat(5, &mountpoint) != nil || mountpoint.Mode != unix.S_IFDIR|0o700 || mountpoint.Uid != 0 || mountpoint.Nlink == 0 ||
		unix.Fstatat(unix.AT_FDCWD, name, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameLinuxInputStat(mountpoint, linked) ||
		unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "") != nil ||
		unix.Mount("tmpfs", name, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0700,size=2147483648,nr_inodes=100002") != nil {
		return ErrLinuxInputTree
	}
	// The original descriptor still names the covered host directory. Enter only
	// an actual new empty tmpfs mount, never a replacement host directory or the
	// old /proc/self/fd/5 view. The parent independently fences its placeholder.
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrLinuxInputTree
	}
	view := os.NewFile(uintptr(fd), "fresh-input-tmpfs")
	defer func() {
		if view.Close() != nil {
			retErr = ErrLinuxInputTree
		}
	}()
	var actual unix.Stat_t
	var filesystem unix.Statfs_t
	actualMount, err := inputTreeMountID(fd)
	entries, readErr := view.ReadDir(1)
	if err != nil || actualMount == priorMount || unix.Fstat(fd, &actual) != nil || actual.Dev == mountpoint.Dev ||
		actual.Mode != unix.S_IFDIR|0o700 || actual.Uid != 0 || unix.Fstatfs(fd, &filesystem) != nil || filesystem.Type != unix.TMPFS_MAGIC ||
		len(entries) != 0 || readErr != io.EOF || unix.Fchdir(fd) != nil || unix.Close(5) != nil {
		return ErrLinuxInputTree
	}
	return nil
}

func receiveInputTree(ctx context.Context, wire *inputTreeSocket, nonce []byte) (retErr error) {
	root, err := os.OpenRoot(".")
	if err != nil {
		return ErrLinuxInputTree
	}
	defer func() {
		if root.Close() != nil {
			retErr = ErrLinuxInputTree
		}
	}()
	var census inputTreeCensus
	var expected []byte
	for {
		// Peek only the fixed header to choose the next closed message kind.
		var header [treeWireHeader]byte
		if wire.wait(ctx, unix.POLLIN) != nil {
			return ErrLinuxInputTree
		}
		count, _, _, _, err := unix.Recvmsg(wire.fd, header[:], nil, unix.MSG_PEEK|unix.MSG_DONTWAIT)
		if err != nil || count < treeWireHeader {
			return ErrLinuxInputTree
		}
		kind := header[4]
		if kind != treeWireDirectory && kind != treeWireFile && kind != treeWireEnd {
			return ErrLinuxInputTree
		}
		raw, _, err := wire.receive(ctx, kind, false)
		if err != nil {
			return err
		}
		if kind == treeWireEnd {
			if len(census.rows) == 0 || len(raw) != 64 {
				return ErrLinuxInputTree
			}
			expected = bytes.Clone(raw)
			break
		}
		entry, err := decodeInputTreeEntry(raw)
		if err != nil || (kind == treeWireDirectory) != (entry.mode == 0o700) || census.add(entry) != nil {
			return ErrLinuxInputTree
		}
		if kind == treeWireDirectory {
			if root.Mkdir(entry.name, 0o700) != nil {
				return ErrLinuxInputTree
			}
			continue
		}
		file, err := root.OpenFile(entry.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return ErrLinuxInputTree
		}
		err = receiveInputTreeFile(ctx, wire, file, &entry)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return ErrLinuxInputTree
		}
		census.rows[len(census.rows)-1] = entry
	}
	if !bytes.Equal(inputTreeManifest(census), expected) {
		return ErrLinuxInputTree
	}
	// No writing descriptor survives this transition. The mount is new and has
	// never been bound anywhere else; neither source nor host alias is adopted.
	view, err := os.Open(".")
	if err != nil {
		return ErrLinuxInputTree
	}
	defer func() {
		if view.Close() != nil {
			retErr = ErrLinuxInputTree
		}
	}()
	if view.Sync() != nil || unix.MountSetattr(int(view.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_RECURSIVE,
		&unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY | unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NODEV}) != nil ||
		dropInputTreeRemountAuthority() != nil || verifyProtectedInputTree(ctx, root, census) != nil {
		return ErrLinuxInputTree
	}
	ready := append(bytes.Clone(nonce), expected...)
	if wire.send(ctx, treeWireReady, ready, view) != nil {
		return ErrLinuxInputTree
	}
	ack, _, err := wire.receive(ctx, treeWireAck, false)
	if err != nil || !bytes.Equal(ack, nonce) {
		return ErrLinuxInputTree
	}
	return nil
}

func receiveInputTreeFile(ctx context.Context, wire *inputTreeSocket, file *os.File, entry *inputTreeEntry) error {
	remaining := entry.size
	for remaining != 0 {
		raw, _, err := wire.receive(ctx, treeWireData, false)
		if err != nil || len(raw) == 0 || len(raw) > treeWireChunk || uint64(len(raw)) > remaining {
			return ErrLinuxInputTree
		}
		written, err := file.Write(raw)
		if err != nil || written != len(raw) {
			return ErrLinuxInputTree
		}
		remaining -= uint64(written)
	}
	raw, _, err := wire.receive(ctx, treeWireFileEnd, false)
	if err != nil || len(raw) != sha256.Size || file.Chmod(os.FileMode(entry.mode)) != nil || file.Sync() != nil || ctx.Err() != nil {
		return ErrLinuxInputTree
	}
	copy(entry.digest[:], raw)
	return nil
}

func dropInputTreeRemountAuthority() error {
	// Linux UAPI securebits.h: NOROOT/LOCKED and NO_SETUID_FIXUP/LOCKED.
	// x/sys exposes prctl but not these four securebits constants.
	const secure = 1<<0 | 1<<1 | 1<<2 | 1<<3
	// The reference recipe requires CGO_ENABLED=0. Apply per-thread changes to
	// every Go runtime thread; plain prctl/capset would leave sibling threads
	// privileged and could report a different state through the process leader.
	prctl := func(option, value, argument uintptr) error {
		_, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, option, value, argument, 0, 0, 0)
		if errno != 0 {
			return errno
		}
		return nil
	}
	if prctl(unix.PR_SET_SECUREBITS, secure, 0) != nil || prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0) != nil ||
		prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0) != nil {
		return ErrLinuxInputTree
	}
	for capability := uintptr(0); capability < 64; capability++ {
		err := prctl(unix.PR_CAPBSET_DROP, capability, 0)
		if err != nil && err != unix.EINVAL {
			return ErrLinuxInputTree
		}
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	_, _, errno := syscall.AllThreadsSyscall(unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
	runtime.KeepAlive(&header)
	runtime.KeepAlive(&data)
	if errno != 0 || unix.Capget(&header, &data[0]) != nil || data != [2]unix.CapUserData{} {
		return ErrLinuxInputTree
	}
	bits, err := unix.PrctlRetInt(unix.PR_GET_SECUREBITS, 0, 0, 0, 0)
	privs, privErr := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || privErr != nil || bits != secure || privs != 1 {
		return ErrLinuxInputTree
	}
	return nil
}

func verifyProtectedInputTree(ctx context.Context, root *os.Root, census inputTreeCensus) error {
	rows := make(map[string]inputTreeEntry, len(census.rows))
	var tops []string
	for _, entry := range census.rows {
		rows[entry.name] = entry
		if !strings.Contains(entry.name, "/") {
			if entry.mode != 0o700 || len(tops) == 3 {
				return ErrLinuxInputTree
			}
			tops = append(tops, entry.name)
		}
	}
	// Inventory every mount-root name with one overflow sentinel. Walk each
	// admitted source root separately so the extra private tmpfs root does not
	// consume one of the shared walker's 100,000 selected-entry allowance.
	directory, err := root.Open(".")
	if err != nil {
		return ErrLinuxInputTree
	}
	children, readErr := directory.ReadDir(4)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(tops) == 0 || len(children) != len(tops) {
		return ErrLinuxInputTree
	}
	for _, child := range children {
		if entry, ok := rows[child.Name()]; !ok || entry.mode != 0o700 || !child.IsDir() {
			return ErrLinuxInputTree
		}
	}
	var count int
	buffer := make([]byte, treeWireChunk)
	visit := func(name string, info os.FileInfo) (retErr error) {
		entry, ok := rows[name]
		if !ok || info.Mode().Perm() != os.FileMode(entry.mode) || info.IsDir() != (entry.mode == 0o700) {
			return ErrLinuxInputTree
		}
		count++
		if info.IsDir() {
			return nil
		}
		file, err := root.Open(name)
		if err != nil {
			return ErrLinuxInputTree
		}
		defer func() {
			if file.Close() != nil {
				retErr = ErrLinuxInputTree
			}
		}()
		digest := sha256.New()
		size, err := io.CopyBuffer(digest, executionInputReader{ctx, io.LimitReader(file, int64(entry.size)+1)}, buffer)
		if err != nil || uint64(size) != entry.size || !bytes.Equal(digest.Sum(nil), entry.digest[:]) {
			return ErrLinuxInputTree
		}
		return nil
	}
	for _, name := range tops {
		if walkGoBuildTree(ctx, root, name, visit) != nil {
			return ErrLinuxInputTree
		}
	}
	if count != len(census.rows) {
		return ErrLinuxInputTree
	}
	return nil
}
