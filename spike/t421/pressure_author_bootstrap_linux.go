//go:build linux

package t421

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

const (
	linuxPressureAuthorRootHello byte = iota + 1
	linuxPressureAuthorRootProceed
	linuxPressureAuthorViewReady
	linuxPressureAuthorExecProceed
	linuxPressureAuthorTargetHello
	linuxPressureAuthorTargetProceed
	linuxPressureAuthorSuccess
	linuxPressureAuthorFailure
)

// Only the dedicated NEWNS child changes credentials or cwd. Setres* use the
// pinned Go all-thread implementations; the explicit all-thread syscall rejects
// cgo before the first mutation. Exec then eliminates all former runtime threads.
func dropLinuxPressureAuthorCredentials(policy linuxPressureBorrowPolicy) error {
	if runtime.GOARCH != "amd64" || !policy.valid() || os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 || !linuxPressureBorrowBounding() {
		return ErrLinuxPressure
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ErrLinuxPressure
	}
	cgoOff := false
	for _, setting := range info.Settings {
		if setting.Key == "CGO_ENABLED" && setting.Value == "0" {
			cgoOff = true
		}
	}
	if !cgoOff {
		return ErrLinuxPressure
	}
	// Read-only all-thread operation also refuses an unsupported runtime.
	if _, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0, 0); errno != 0 {
		return ErrLinuxPressure
	}
	if _, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0, 0); errno != 0 {
		return ErrLinuxPressure
	}
	if syscall.Setgroups([]int{}) != nil || syscall.Setresgid(int(policy.gid), int(policy.gid), int(policy.gid)) != nil || syscall.Setresuid(int(policy.uid), int(policy.uid), int(policy.uid)) != nil {
		return ErrLinuxPressure
	}
	if policy.constrainChild() != nil {
		return ErrLinuxPressure
	}
	return checkLinuxPressureAuthorThreads(policy)
}

func checkLinuxPressureAuthorThreads(policy linuxPressureBorrowPolicy) error {
	directory, err := os.Open("/proc/self/task")
	if err != nil {
		return ErrLinuxPressure
	}
	entries, err := directory.ReadDir(65)
	closeErr := directory.Close()
	if err != nil && err != io.EOF || closeErr != nil || len(entries) == 0 || len(entries) > 64 || policy.checkChild() != nil {
		return ErrLinuxPressure
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return ErrLinuxPressure
		}
		file, openErr := os.Open("/proc/self/task/" + entry.Name() + "/status")
		if openErr != nil {
			return ErrLinuxPressure
		}
		raw, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || len(raw) > 16<<10 || !linuxPressureAuthorThreadStatus(raw, policy) {
			return ErrLinuxPressure
		}
	}
	return nil
}

func linuxPressureAuthorThreadStatus(raw []byte, policy linuxPressureBorrowPolicy) bool {
	if len(raw) == 0 || len(raw) > 16<<10 {
		return false
	}
	fields := linuxPressureAuthorStatusFields(raw)
	uid, gid := strconv.FormatUint(uint64(policy.uid), 10), strconv.FormatUint(uint64(policy.gid), 10)
	return fields["Uid"] == uid+" "+uid+" "+uid+" "+uid && fields["Gid"] == gid+" "+gid+" "+gid+" "+gid &&
		fields["Groups"] == "" && fields["CapInh"] == "0000000000000000" && fields["CapPrm"] == "0000000000000000" && fields["CapEff"] == "0000000000000000" &&
		fields["CapAmb"] == "0000000000000000" && fields["CapBnd"] == "000001ffffffffff" && fields["NoNewPrivs"] == "1"
}

// Root bootstrap is entered only by the source-issued broker. The root-phase
// socket authenticates the actual creator before privileged setup; target
// credentials have a separate post-exec phase on the same original PID/birth.
func runLinuxPressureAuthorBootstrap(ctx context.Context, value linuxPressureAuthorSelection, observation *executionPreworkObservation) (retErr error) {
	image := os.NewFile(3, "pressure-root-bootstrap-image")
	original := os.NewFile(5, "pressure-author-original-leaf")
	target := os.NewFile(6, "pressure-author-target-image")
	namespace := os.NewFile(8, "pressure-author-original-namespace")
	liveImage := image
	imageOpen, namespaceOpen, targetOpen := true, true, true
	defer func() {
		if imageOpen {
			retErr = errors.Join(retErr, observation.close(liveImage.Close()))
		}
		retErr = errors.Join(retErr, observation.close(original.Close()))
		if targetOpen {
			retErr = errors.Join(retErr, observation.close(target.Close()))
		}
		if namespaceOpen {
			retErr = errors.Join(retErr, observation.close(namespace.Close()))
		}
	}()
	if os.Getuid() != 0 || os.Geteuid() != 0 || !linuxPressureBorrowBounding() || !linuxPressureAuthorOriginalPaths(value.Borrow.Target) {
		return ErrLinuxPressure
	}
	for fd := 3; fd <= 8; fd++ {
		unix.CloseOnExec(fd)
	}
	if unix.SetNonblock(4, true) != nil {
		return ErrLinuxPressure
	}
	// The watch duplicate retires before exec; only original read FD4 survives.
	watchFD, err := unix.FcntlInt(4, unix.F_DUPFD_CLOEXEC, 9)
	if err != nil {
		return ErrLinuxPressure
	}
	watch := os.NewFile(uintptr(watchFD), "pressure-root-bootstrap-watch")
	parent, err := adoptLinuxExecutorParentObserved(ctx, value.Borrow.Target, image, watch, false, observation)
	if err != nil {
		_ = observation.close(watch.Close())
		return ErrLinuxPressure
	}
	defer func() { retErr = errors.Join(retErr, parent.Close()) }()
	if t4013.MatchLinuxProcessExecutableImage(ctx, os.Getppid(), image) != nil {
		return ErrLinuxPressure
	}
	var before, old, parentNamespace unix.Stat_t
	if !linuxPressureAuthorNamespaceFD(8) || unix.Fstat(8, &old) != nil || uint64(old.Dev) != value.Borrow.NamespaceDev || old.Ino != value.Borrow.NamespaceIno || unix.Stat("/proc/"+strconv.Itoa(os.Getppid())+"/ns/mnt", &parentNamespace) != nil || old.Dev != parentNamespace.Dev || old.Ino != parentNamespace.Ino {
		return ErrLinuxPressure
	}
	if unix.Stat("/proc/self/ns/mnt", &before) != nil || uint64(before.Dev) == value.Borrow.NamespaceDev && before.Ino == value.Borrow.NamespaceIno {
		return ErrLinuxPressure
	}
	birth, err := strconv.ParseUint(parent.self.StartIdentity, 10, 64)
	attempt, decodeErr := hex.DecodeString(value.Borrow.Target.Attempt)
	wire := linuxPressureBorrowSocket{fd: 7, author: true, peer: unix.Ucred{Pid: int32(os.Getppid()), Uid: 0, Gid: 0}, birth: birth, observation: observation}
	copy(wire.attempt[:], attempt)
	if err != nil || decodeErr != nil || len(attempt) != 16 || birth == 0 || wire.send(parent.alive, linuxPressureAuthorRootHello) != nil || wire.receive(parent.alive, linuxPressureAuthorRootProceed) != nil {
		return ErrLinuxPressure
	}
	var leafStat unix.Stat_t
	leafMount, leafErr := inputTreeMountID(5)
	var leafFS unix.Statfs_t
	if unix.Fstat(5, &leafStat) != nil || uint64(leafStat.Dev) != value.Borrow.LeafDevice || leafStat.Ino != value.Borrow.LeafInode || leafErr != nil || leafMount != value.Borrow.LeafMount || unix.Fstatfs(5, &leafFS) != nil || leafFS.Fsid.Val != value.Borrow.LeafFSID || linuxPressureAuthorTargetImage(target, value.Borrow.UID, value.Borrow.GID) != nil {
		return ErrLinuxPressure
	}
	unix.Umask(0o077)
	view, err := createLinuxPressureAuthorView(parent.alive, original, namespace, value.Borrow.UID, value.Borrow.GID, observation)
	if view != nil {
		defer func() { retErr = errors.Join(retErr, view.Close()) }()
	}
	if err != nil {
		return err
	}
	value.ViewNamespaceDevice, value.ViewNamespaceInode, value.ViewMount = view.namespaceDevice, view.namespaceInode, view.mount
	if wire.send(parent.alive, linuxPressureAuthorViewReady) != nil || wire.receive(parent.alive, linuxPressureAuthorExecProceed) != nil {
		return ErrLinuxPressure
	}
	// Close all root-phase aliases before the UID transition, not on a future
	// process-exit assumption. Original leaf/control/read and target image remain.
	if parent.check(ctx) != nil || view.check(ctx) != nil {
		return ErrLinuxPressure
	}
	namespaceErr := observation.close(namespace.Close())
	namespaceOpen = false
	if parent.Close() != nil || view.Close() != nil || namespaceErr != nil {
		return ErrLinuxPressure
	}
	imageErr := observation.close(image.Close())
	imageOpen = false
	if imageErr != nil {
		return ErrLinuxPressure
	}
	if unix.Dup3(6, 3, unix.O_CLOEXEC) != nil {
		return ErrLinuxPressure
	}
	liveImage = os.NewFile(3, "pressure-target-exec-image")
	imageOpen = true
	targetErr := observation.close(target.Close())
	targetOpen = false
	if targetErr != nil {
		return ErrLinuxPressure
	}
	unix.Umask(0o077)
	if dropLinuxPressureAuthorCredentials(linuxPressureBorrowPolicy{value.Borrow.UID, value.Borrow.GID}) != nil {
		return ErrLinuxPressure
	}
	encoded, err := encodeLinuxPressureAuthor(value)
	if err != nil || ctx.Err() != nil || !time.Now().Before(time.Unix(0, value.Borrow.Target.DeadlineUnixNano)) {
		return ErrLinuxPressure
	}
	for _, fd := range []int{3, 4, 5, 7} {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
			return ErrLinuxPressure
		}
	}
	// Failed exec returns failure with original owners still held; no retry.
	return syscall.Exec("/proc/self/fd/3", []string{"t422-execute", executionLinuxPressureAuthorTargetMode, "--selection-base64url", encoded}, []string{"LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2"})
}

func linuxPressureAuthorStatusFields(raw []byte) map[string]string {
	result := make(map[string]string, 9)
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "Uid", "Gid", "Groups", "CapInh", "CapPrm", "CapEff", "CapAmb", "CapBnd", "NoNewPrivs":
			result[key] = strings.Join(strings.Fields(value), " ")
		}
	}
	return result
}
func linuxPressureAuthorTargetImage(file *os.File, uid, gid uint32) error {
	if file == nil {
		return ErrLinuxPressure
	}
	var stat unix.Stat_t
	if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Uid != uid || stat.Gid != gid || stat.Mode != unix.S_IFREG|0o500 || stat.Size <= 0 || stat.Size > maxInputCustodyFileBytes || stat.Nlink != 0 {
		return ErrLinuxPressure
	}
	entry := linuxExecutionInput{file: file, stat: stat, identity: LinuxExecutionInputIdentity{Bytes: stat.Size, Executable: true}}
	if !linuxPressureAuthorSealed(file, entry) || !linuxInputELF(file, stat.Size) {
		return ErrLinuxPressure
	}
	return nil
}

func linuxPressureAuthorSealed(file *os.File, entry linuxExecutionInput) bool {
	fd := int(file.Fd())
	status, e := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	flags, f := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	seals, s := unix.FcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
	return e == nil && f == nil && s == nil && status&unix.O_ACCMODE == unix.O_RDONLY && flags&unix.FD_CLOEXEC != 0 && seals&linuxInputSeals == linuxInputSeals && entry.stat.Mode == unix.S_IFREG|0o500
}
