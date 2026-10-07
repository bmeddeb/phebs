//go:build linux

// Command t42h2b-seal-probe verifies the kernel memfd sealing contract that
// spike/t421/input_custody_linux.go depends on, without exercising any phebs
// code. It exists so a refusal inside ProtectLinuxExecutionInputs can be
// attributed to the host kernel rather than to the custody implementation, and
// so the contract recorded in spike/t42h2b/review-reproduction-1.json can be
// re-derived from the repository instead of trusted from a measurement alone.
//
// Every reported invariant must hold on a host that is to protect direct
// inputs; the command exits non-zero if any fails. It creates no files, leaves
// no disk copy, spawns at most one short-lived joined child, and holds at most
// three descriptors at a time.
//
// Run it on the target host:
//
//	GOOS=linux GOARCH=amd64 go run ./spike/t42h2b/cmd/t42h2b-seal-probe
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// allSeals mirrors linuxInputSeals in spike/t421/input_custody_linux.go. Any
// divergence between the two would make this probe worthless, so the set is
// restated deliberately rather than imported from a package that is not a
// library.
const allSeals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_EXEC | unix.F_SEAL_SEAL

const payload = "neutral sealed input\n"

var (
	passed int
	failed int
)

func report(name string, pass bool, format string, args ...any) {
	status := "PASS"
	if pass {
		passed++
	} else {
		failed++
		status = "FAIL"
	}
	fmt.Printf("%-4s %-46s %s\n", status, name, fmt.Sprintf(format, args...))
}

func main() {
	executable := flag.String("executable", "", "ELF image to seal, copy and join; defaults to the first of /usr/bin/true or /bin/true that exists")
	flag.Parse()

	image := *executable
	if image == "" {
		for _, candidate := range []string{"/usr/bin/true", "/bin/true"} {
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				image = candidate
				break
			}
		}
	}
	var release unix.Utsname
	if err := unix.Uname(&release); err != nil {
		fmt.Fprintf(os.Stderr, "uname failed: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("kernel=%s goarch=%s executable=%s\n", chars(release.Release[:]), runtime.GOARCH, image)
	if image == "" {
		fmt.Fprintln(os.Stderr, "no executable image found; pass -executable")
		os.Exit(2)
	}

	probeCreationFlags()
	probeSealSet()
	probeMutationRefusals()
	probeReaderIdentity()
	probeTimestampDrift()
	probeJoinedExecution(image)

	fmt.Printf("\nSUMMARY pass=%d fail=%d\n", passed, failed)
	if failed > 0 {
		fmt.Println("RESULT host does not satisfy the T42.H2b sealing contract")
		os.Exit(1)
	}
	fmt.Println("RESULT host satisfies the T42.H2b sealing contract")
}

// probeCreationFlags pins the two creation modes the custody constructor
// selects between: MFD_NOEXEC_SEAL must arrive already execute-sealed, and
// MFD_EXEC must arrive unsealed so the explicit seal set can be applied.
func probeCreationFlags() {
	noexec, err := create(unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING | unix.MFD_NOEXEC_SEAL)
	if err != nil {
		report("mfd_noexec_seal_creates", false, "%v", err)
		return
	}
	defer closeFile(noexec)
	seals, sealErr := unix.FcntlInt(noexec.Fd(), unix.F_GET_SEALS, 0)
	report("mfd_noexec_seal_applies_f_seal_exec", sealErr == nil && seals&unix.F_SEAL_EXEC == unix.F_SEAL_EXEC,
		"seals=%s err=%v", sealNames(seals), sealErr)

	execFile, err := create(unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING | unix.MFD_EXEC)
	if err != nil {
		report("mfd_exec_creates", false, "%v", err)
		return
	}
	defer closeFile(execFile)
	seals, sealErr = unix.FcntlInt(execFile.Fd(), unix.F_GET_SEALS, 0)
	report("mfd_exec_starts_unsealed", sealErr == nil && seals == 0, "seals=%s err=%v", sealNames(seals), sealErr)
}

// probeSealSet answers the load-bearing question for executable inputs: an
// MFD_EXEC image must accept F_SEAL_EXEC through the explicit seal set,
// otherwise no executable direct input could ever be protected and the
// constructor would fail closed on every tool image.
func probeSealSet() {
	for _, tc := range []struct {
		name       string
		executable bool
		mode       uint32
	}{
		{"noexec", false, 0o400},
		{"exec", true, 0o500},
	} {
		file, stat, err := sealedBytes([]byte(payload), tc.executable)
		if err != nil {
			report(tc.name+"_accepts_full_seal_set", false, "%v", err)
			continue
		}
		report(tc.name+"_accepts_full_seal_set", stat.Mode&0o7777 == tc.mode,
			"mode=0%o want=0%o size=%d", stat.Mode&0o7777, tc.mode, stat.Size)
		closeFile(file)
	}
}

// probeMutationRefusals reproduces the six operations the custody suite asserts
// and, critically, asserts them with the mode left owner-writable and an O_RDWR
// descriptor open. That is what makes the point that file mode is not the
// control: the seals are.
func probeMutationRefusals() {
	file, stat, err := sealedBytes([]byte(payload), false)
	if err != nil {
		report("mutation_refusals_setup", false, "%v", err)
		return
	}
	defer closeFile(file)

	// Non-execute permission bits stay mutable after F_SEAL_EXEC, so an owner
	// can make the inode owner-writable. Detecting that drift is custody's job;
	// preventing writes is the kernel's, and must not depend on it.
	chmodErr := unix.Fchmod(int(file.Fd()), 0o600)
	report("non_execute_chmod_allowed_after_exec_seal", chmodErr == nil, "err=%v", chmodErr)

	writable, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), unix.O_RDWR|unix.O_CLOEXEC, 0)
	report("rdwr_reopen_succeeds_on_write_sealed", err == nil, "err=%v", err)
	if err != nil {
		return
	}
	defer func() { _ = unix.Close(writable) }()

	operations := []struct {
		name string
		run  func() error
	}{
		{"write", func() error { _, err := unix.Pwrite(writable, []byte("x"), 0); return err }},
		{"shrink", func() error { return unix.Ftruncate(writable, 0) }},
		{"grow", func() error { return unix.Ftruncate(writable, stat.Size+1) }},
		{"execute_mode", func() error { return unix.Fchmod(writable, stat.Mode^0o100) }},
		{"seal_change", func() error {
			_, err := unix.FcntlInt(uintptr(writable), unix.F_ADD_SEALS, unix.F_SEAL_FUTURE_WRITE)
			return err
		}},
		{"shared_write_map", func() error {
			mapping, err := unix.Mmap(writable, 0, 1, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
			if err == nil {
				_ = unix.Munmap(mapping)
			}
			return err
		}},
	}
	for _, operation := range operations {
		err := operation.run()
		report("eperm_"+operation.name, errors.Is(err, unix.EPERM), "err=%v", err)
	}
}

// probeReaderIdentity pins every field custody compares when it reopens a
// keeper read-only through /proc/self/fd.
func probeReaderIdentity() {
	writer, snapshot, err := writerSealed([]byte(payload), false)
	if err != nil {
		report("reader_identity_setup", false, "%v", err)
		return
	}
	reader, err := reopenReadOnly(writer)
	closeFile(writer)
	if err != nil {
		report("reader_identity_setup", false, "reopen: %v", err)
		return
	}
	defer closeFile(reader)

	var readerStat unix.Stat_t
	statErr := unix.Fstat(int(reader.Fd()), &readerStat)
	seals, sealErr := unix.FcntlInt(reader.Fd(), unix.F_GET_SEALS, 0)
	flags, flagErr := unix.FcntlInt(reader.Fd(), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(reader.Fd(), unix.F_GETFD, 0)
	report("reader_same_inode_and_snapshot", statErr == nil &&
		readerStat.Dev == snapshot.Dev && readerStat.Ino == snapshot.Ino && readerStat.Mode == snapshot.Mode &&
		readerStat.Size == snapshot.Size && readerStat.Mtim == snapshot.Mtim && readerStat.Ctim == snapshot.Ctim,
		"stat=%v", statErr)
	report("reader_nlink_zero", statErr == nil && readerStat.Nlink == 0, "nlink=%d", readerStat.Nlink)
	report("reader_mode_preserved", statErr == nil && readerStat.Mode&0o7777 == 0o400, "mode=0%o", readerStat.Mode&0o7777)
	report("reader_seals_visible", sealErr == nil && seals&allSeals == allSeals, "seals=%s err=%v", sealNames(seals), sealErr)
	report("reader_access_mode_rdonly", flagErr == nil && flags&unix.O_ACCMODE == unix.O_RDONLY, "err=%v", flagErr)
	report("reader_cloexec", fdErr == nil && fdFlags&unix.FD_CLOEXEC != 0, "err=%v", fdErr)
}

// probeTimestampDrift establishes that explicit timestamp drift stays possible
// on a fully sealed memfd, which is what makes it a deterministic stand-in for
// the unsound restored-chmod oracle the custody suite originally used.
func probeTimestampDrift() {
	file, before, err := sealedBytes([]byte(payload), false)
	if err != nil {
		report("timestamp_drift_setup", false, "%v", err)
		return
	}
	defer closeFile(file)

	drifted := unix.NsecToTimespec(int64(before.Mtim.Sec)*int64(time.Second) + int64(before.Mtim.Nsec) - int64(72*time.Hour))
	proc := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
	utimesErr := unix.UtimesNanoAt(unix.AT_FDCWD, proc, []unix.Timespec{drifted, drifted}, 0)
	var after unix.Stat_t
	statErr := unix.Fstat(int(file.Fd()), &after)
	seals, sealErr := unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
	report("utimensat_succeeds_on_sealed_memfd", utimesErr == nil, "err=%v", utimesErr)
	report("utimensat_moves_mtim", statErr == nil && after.Mtim != before.Mtim,
		"stat=%v moved=%v", statErr, after.Mtim != before.Mtim)
	report("utimensat_leaves_seal_set_intact", sealErr == nil && seals&allSeals == allSeals,
		"seals=%s err=%v", sealNames(seals), sealErr)

	// Byte immutability must survive the metadata change. The mode is made
	// owner-writable first so the reopen can actually succeed; only then does a
	// refused write prove the seal rather than the mode is doing the work.
	if chmodErr := unix.Fchmod(int(file.Fd()), 0o600); chmodErr != nil {
		report("write_still_eperm_after_utimensat", false, "chmod: %v", chmodErr)
		return
	}
	writable, openErr := unix.Open(proc, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if openErr != nil {
		report("write_still_eperm_after_utimensat", false, "rdwr reopen: %v", openErr)
		return
	}
	defer func() { _ = unix.Close(writable) }()
	_, writeErr := unix.Pwrite(writable, []byte("x"), 0)
	report("write_still_eperm_after_utimensat", errors.Is(writeErr, unix.EPERM), "err=%v", writeErr)
}

// probeJoinedExecution reproduces both halves of the joined-use contract: a
// sealed executable image runs from an inherited descriptor, and a sealed
// nonexecutable image is refused rather than silently degrading.
func probeJoinedExecution(image string) {
	source, err := os.ReadFile(image)
	if err != nil {
		report("joined_execution_setup", false, "read %s: %v", image, err)
		return
	}
	executable, _, err := sealedBytes(source, true)
	if err != nil {
		report("joined_execution_setup", false, "seal executable: %v", err)
		return
	}
	defer closeFile(executable)
	data, _, err := sealedBytes([]byte(payload), false)
	if err != nil {
		report("joined_execution_setup", false, "seal data: %v", err)
		return
	}
	defer closeFile(data)

	runErr := join(executable)
	report("sealed_exec_image_joins_via_proc_self_fd", runErr == nil, "err=%v", runErr)
	dataErr := join(data)
	report("sealed_noexec_image_refused", dataErr != nil, "err=%v", dataErr)
}

// join launches the given descriptor as the child's fd 3 and waits for it, so
// no loan is outstanding after the child exits.
func join(file *os.File) error {
	command := exec.Command("/proc/self/fd/3")
	command.ExtraFiles = []*os.File{file}
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	return command.Run()
}

func create(flags int) (*os.File, error) {
	fd, err := unix.MemfdCreate("phebs-seal-probe", flags)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "probe-writer"), nil
}

func reopenReadOnly(file *os.File) (*os.File, error) {
	fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "probe-reader"), nil
}

// sealedBytes mirrors the production construction sequence and returns the
// read-only keeper: create with an explicit exec/noexec mode, stream the bytes
// once, set the mode, apply the full seal set, snapshot, reopen read-only, then
// close the writer before the descriptor is handed back.
func sealedBytes(content []byte, executable bool) (*os.File, unix.Stat_t, error) {
	writer, snapshot, err := writerSealed(content, executable)
	if err != nil {
		return nil, snapshot, err
	}
	reader, err := reopenReadOnly(writer)
	closeErr := writer.Close()
	if err != nil || closeErr != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, snapshot, errors.Join(err, closeErr)
	}
	return reader, snapshot, nil
}

// writerSealed returns the still-writable sealed descriptor plus its snapshot,
// for the one check that must compare the writer's identity against the
// reopened reader's.
func writerSealed(content []byte, executable bool) (*os.File, unix.Stat_t, error) {
	var snapshot unix.Stat_t
	flags := unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING | unix.MFD_NOEXEC_SEAL
	mode := uint32(0o400)
	if executable {
		flags = unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING | unix.MFD_EXEC
		mode = 0o500
	}
	writer, err := create(flags)
	if err != nil {
		return nil, snapshot, err
	}
	written, copyErr := io.Copy(writer, bytes.NewReader(content))
	chmodErr := unix.Fchmod(int(writer.Fd()), mode)
	_, sealErr := unix.FcntlInt(writer.Fd(), unix.F_ADD_SEALS, allSeals)
	statErr := unix.Fstat(int(writer.Fd()), &snapshot)
	if copyErr != nil || written != int64(len(content)) || chmodErr != nil || sealErr != nil || statErr != nil {
		_ = writer.Close()
		return nil, snapshot, errors.Join(copyErr, chmodErr, sealErr, statErr)
	}
	return writer, snapshot, nil
}

func closeFile(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}

func sealNames(seals int) string {
	if seals < 0 {
		return "unavailable"
	}
	names := ""
	for _, entry := range []struct {
		bit  int
		name string
	}{
		{unix.F_SEAL_SEAL, "SEAL"},
		{unix.F_SEAL_SHRINK, "SHRINK"},
		{unix.F_SEAL_GROW, "GROW"},
		{unix.F_SEAL_WRITE, "WRITE"},
		{unix.F_SEAL_EXEC, "EXEC"},
	} {
		if seals&entry.bit != 0 {
			names += entry.name + "|"
		}
	}
	if names == "" {
		return "none"
	}
	return names[:len(names)-1]
}

// chars renders a fixed-size utsname field, whose element type differs between
// linux architectures.
func chars[T int8 | uint8](values []T) string {
	out := make([]byte, 0, len(values))
	for _, value := range values {
		if value == 0 {
			break
		}
		out = append(out, byte(value))
	}
	return string(out)
}
