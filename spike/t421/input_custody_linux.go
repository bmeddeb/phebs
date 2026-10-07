//go:build linux

package t421

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

const linuxInputSeals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_EXEC | unix.F_SEAL_SEAL

// LinuxExecutionInputIdentity describes selected bytes, never tool provenance,
// command permission, a private pathname or completed ceremony admission.
type LinuxExecutionInputIdentity struct {
	Name       string
	SHA256     string
	Bytes      int64
	Executable bool
}

type linuxExecutionInput struct {
	file     *os.File
	stat     unix.Stat_t
	identity LinuxExecutionInputIdentity
}

// LinuxExecutionInputCustody holds anonymous, kernel-sealed direct copies.
// It is deliberately separate from Darwin's path/directory custody contract.
// Neither source files nor namespace entries are made immutable. The trusted
// single-owner caller must exclude descriptor replacement and code injection.
// Anonymous payload memory must be reserved separately from process RSS; it
// can reach 2 GiB and receives no swap credit in future resource admission.
//
// The custody lock is held for a whole loan, so at most one loan is
// outstanding and one child can be handed exactly one lent input. Nested
// WithInput calls deadlock rather than composing; dispatch that needs a tool
// image and a fixed input in the same child requires a separate atomic
// multi-loan API, which this slice deliberately does not provide. Close and
// Check wait behind an in-progress loan and its joined child.
type LinuxExecutionInputCustody struct {
	mu     sync.Mutex
	inputs map[string]linuxExecutionInput
	closed bool
	err    error
}

// ProtectLinuxExecutionInputs owns at most 64 files, each <=256 MiB and together
// <=2 GiB. The ceilings are local, not frozen resource admission. Construction
// copies each source once, seals writes/size/execute bits/the seal set, reopens
// read-only, closes the writer, then hashes the sealed bytes once. Unsupported
// or denied kernel protection refuses without a chmod-only fallback.
//
// This symbol is compiled for every linux GOARCH and refuses at runtime on
// anything but amd64, because the ELF screen is fixed to ELF64 little-endian
// EM_X86_64; it does not exist on other GOOS. Callers therefore still need
// build tags. Keeping one linux symbol with a runtime arch gate holds the amd64
// requirement in one place instead of splitting this file per architecture, and
// makes a non-amd64 linux misuse a sticky refusal rather than a link error.
//
// Executable copies require the bounded ELF64 amd64 header screen documented on
// linuxInputELF. That screen is not a provenance, loader/library/helper closure
// or executable-dispatch verifier. Existing deleted-path process observations
// do not bind anonymous images. Failure closes all owned descriptors and
// returns nil; no disk copies remain.
func ProtectLinuxExecutionInputs(ctx context.Context, copies []ExecutionInputCopy) (_ *LinuxExecutionInputCustody, retErr error) {
	if runtime.GOARCH != "amd64" || ctx == nil || ctx.Err() != nil || len(copies) == 0 || len(copies) > maxInputCustodyFiles {
		return nil, ErrExecutionInputCustody
	}
	copies = slices.Clone(copies)
	names := make(map[string]bool, len(copies))
	for _, input := range copies {
		if len(input.Name) > 64 || !validSanitizedToken(strings.ReplaceAll(input.Name, "-", ""), 64) || names[input.Name] ||
			!validExecutionSHA256(input.SHA256) || len(input.Path) > maxInputCustodyPathBytes || !filepath.IsAbs(input.Path) || filepath.Clean(input.Path) != input.Path {
			return nil, ErrExecutionInputCustody
		}
		names[input.Name] = true
	}
	custody := &LinuxExecutionInputCustody{inputs: make(map[string]linuxExecutionInput, len(copies))}
	defer func() {
		if retErr != nil {
			_ = custody.Close()
			retErr = ErrExecutionInputCustody
		}
	}()
	var total int64
	buffer := make([]byte, 32<<10)
	for _, input := range copies {
		entry, err := copyLinuxExecutionInput(ctx, input, maxInputCustodyBytes-total, buffer)
		if err != nil {
			return nil, ErrExecutionInputCustody
		}
		custody.inputs[input.Name] = entry
		total += entry.identity.Bytes
	}
	if ctx.Err() != nil {
		return nil, ErrExecutionInputCustody
	}
	return custody, nil
}

func copyLinuxExecutionInput(ctx context.Context, input ExecutionInputCopy, remaining int64, buffer []byte) (_ linuxExecutionInput, retErr error) {
	var entry linuxExecutionInput
	canonical, err := filepath.EvalSymlinks(input.Path)
	if err != nil || canonical != input.Path || ctx.Err() != nil {
		return entry, ErrExecutionInputCustody
	}
	source, err := t4013.OpenHostImage(input.Path)
	if err != nil {
		return entry, ErrExecutionInputCustody
	}
	defer func() {
		if source.Close() != nil {
			retErr = ErrExecutionInputCustody
		}
		if retErr != nil && entry.file != nil {
			_ = entry.file.Close()
		}
	}()
	var before, after, pathStat unix.Stat_t
	if unix.Fstat(int(source.Fd()), &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&(unix.S_ISUID|unix.S_ISGID) != 0 ||
		before.Size < 0 || before.Size > maxInputCustodyFileBytes || before.Size > remaining ||
		(before.Mode&0o111 != 0) != input.Executable || input.Executable && !linuxInputELF(source, before.Size) {
		return entry, ErrExecutionInputCustody
	}
	flags := unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING | unix.MFD_NOEXEC_SEAL
	mode := uint32(0o400)
	if input.Executable {
		flags = unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING | unix.MFD_EXEC
		mode = 0o500
	}
	fd, err := unix.MemfdCreate("phebs-sealed-input", flags)
	if err != nil {
		return entry, ErrExecutionInputCustody
	}
	writer := os.NewFile(uintptr(fd), "sealed-input-writer")
	defer func() { _ = writer.Close() }()
	written, copyErr := io.CopyBuffer(struct{ io.Writer }{writer}, executionInputReader{ctx, io.LimitReader(source, before.Size)}, buffer)
	var sentinel [1]byte
	_, tailErr := io.ReadFull(executionInputReader{ctx, source}, sentinel[:])
	if copyErr != nil || written != before.Size || !errors.Is(tailErr, io.EOF) || ctx.Err() != nil ||
		unix.Fstat(int(source.Fd()), &after) != nil || unix.Lstat(input.Path, &pathStat) != nil ||
		!sameLinuxInputStat(before, after) || !sameLinuxInputStat(after, pathStat) || unix.Fchmod(fd, mode) != nil {
		return entry, ErrExecutionInputCustody
	}
	if _, err := unix.FcntlInt(writer.Fd(), unix.F_ADD_SEALS, linuxInputSeals); err != nil {
		return entry, ErrExecutionInputCustody
	}
	if unix.Fstat(fd, &entry.stat) != nil {
		return entry, ErrExecutionInputCustody
	}
	entry.file, err = openLinuxInputReadOnly(writer)
	if err != nil || writer.Close() != nil {
		return entry, ErrExecutionInputCustody
	}
	entry.identity = LinuxExecutionInputIdentity{input.Name, input.SHA256, written, input.Executable}
	if !linuxInputProtected(entry.file, entry) {
		return entry, ErrExecutionInputCustody
	}
	hash := sha256.New()
	if size, err := io.CopyBuffer(hash, executionInputReader{ctx, io.NewSectionReader(entry.file, 0, written)}, buffer); err != nil || size != written ||
		"sha256:"+hex.EncodeToString(hash.Sum(nil)) != input.SHA256 || !linuxInputProtected(entry.file, entry) || ctx.Err() != nil {
		return entry, ErrExecutionInputCustody
	}
	return entry, nil
}

// linuxInputELF reads only the 64-byte ELF header; no parser allocates from
// untrusted counts. Admitted bounds: size >= 64, ELFCLASS64, ELFDATA2LSB,
// EI_VERSION 1, e_type ET_EXEC or ET_DYN, e_machine EM_X86_64, e_version 1,
// e_ehsize 64, e_phentsize 56, e_phnum in 1..128, and e_phoff in 64..size with
// the whole program-header table inside the admitted size.
//
// e_phnum <= 128 and e_phoff >= 64 are conservative screen bounds, not ELF
// limits (e_phnum may reach 65535, or 0xffff with PN_XNUM extension). Measured
// linux/amd64 Go binaries, including a full phebs build, use e_phnum 6 and
// e_phoff 64, so the bound is generous for every intended direct image. A
// legitimate header outside it fails closed with no weaker fallback.
func linuxInputELF(file *os.File, size int64) bool {
	var header [64]byte
	if size < int64(len(header)) {
		return false
	}
	if n, err := file.ReadAt(header[:], 0); err != nil || n != len(header) || string(header[:4]) != "\x7fELF" ||
		header[4] != 2 || header[5] != 1 || header[6] != 1 {
		return false
	}
	typeID := binary.LittleEndian.Uint16(header[16:18])
	count := binary.LittleEndian.Uint16(header[56:58])
	offset := binary.LittleEndian.Uint64(header[32:40])
	return (typeID == 2 || typeID == 3) && binary.LittleEndian.Uint16(header[18:20]) == 62 &&
		binary.LittleEndian.Uint32(header[20:24]) == 1 && binary.LittleEndian.Uint16(header[52:54]) == 64 &&
		binary.LittleEndian.Uint16(header[54:56]) == 56 && count > 0 && count <= 128 &&
		offset >= 64 && offset <= uint64(size) && uint64(count)*56 <= uint64(size)-offset
}

func sameLinuxInputStat(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode && left.Nlink == right.Nlink &&
		left.Uid == right.Uid && left.Gid == right.Gid && left.Size == right.Size && left.Mtim == right.Mtim && left.Ctim == right.Ctim
}

func openLinuxInputReadOnly(file *os.File) (*os.File, error) {
	fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrExecutionInputCustody
	}
	return os.NewFile(uintptr(fd), "sealed-input-reader"), nil
}

func linuxInputProtected(file *os.File, entry linuxExecutionInput) bool {
	mode := uint32(0o400)
	if entry.identity.Executable {
		mode = 0o500
	}
	var stat unix.Stat_t
	if unix.Fstat(int(file.Fd()), &stat) != nil || !sameLinuxInputStat(entry.stat, stat) || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Nlink != 0 || stat.Uid != uint32(os.Getuid()) || stat.Size != entry.identity.Bytes ||
		stat.Mode&0o7777 != mode {
		return false
	}
	seals, sealErr := unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
	flags, flagsErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	return sealErr == nil && seals&linuxInputSeals == linuxInputSeals && flagsErr == nil && flags&unix.O_ACCMODE == unix.O_RDONLY &&
		fdErr == nil && fdFlags&unix.FD_CLOEXEC != 0
}

func (custody *LinuxExecutionInputCustody) checkLocked(ctx context.Context, name string) (linuxExecutionInput, error) {
	if ctx == nil || ctx.Err() != nil || len(name) > 64 || custody.closed || custody.err != nil {
		custody.err = ErrExecutionInputCustody
		return linuxExecutionInput{}, custody.err
	}
	entry, exists := custody.inputs[name]
	if !exists || !linuxInputProtected(entry.file, entry) || ctx.Err() != nil {
		custody.err = ErrExecutionInputCustody
		return linuxExecutionInput{}, custody.err
	}
	return entry, nil
}

// Check checks one held inode, immutable seals and read-only/CLOEXEC flags under
// the custody lock. It never rereads or hashes payloads. Any refusal is sticky.
func (custody *LinuxExecutionInputCustody) Check(ctx context.Context, name string) (LinuxExecutionInputIdentity, error) {
	custody.mu.Lock()
	defer custody.mu.Unlock()
	entry, err := custody.checkLocked(ctx, name)
	return entry.identity, err
}

// WithInput lends one fresh read-only CLOEXEC descriptor while holding custody's
// lock. The trusted callback must not retain/duplicate it, call custody methods,
// or return before any child using it has joined. Because the lock is held for
// the whole callback, exactly one loan is outstanding at a time and one child
// can receive exactly one input; nested WithInput calls deadlock, so multi-input
// dispatch needs a separate atomic multi-loan API rather than composition here.
// This method cannot prove that caller contract, session/orphan teardown,
// argv/environment safety or dispatch authority. Callback errors are returned
// unchanged and do not latch; protection/close failures and cancellation latch
// custody failure. Close and Check wait behind the callback.
func (custody *LinuxExecutionInputCustody) WithInput(ctx context.Context, name string, use func(*os.File) error) error {
	custody.mu.Lock()
	defer custody.mu.Unlock()
	entry, err := custody.checkLocked(ctx, name)
	if err != nil || use == nil {
		custody.err = ErrExecutionInputCustody
		return custody.err
	}
	file, err := openLinuxInputReadOnly(entry.file)
	if err != nil || !linuxInputProtected(file, entry) {
		if file != nil {
			_ = file.Close()
		}
		custody.err = ErrExecutionInputCustody
		return custody.err
	}
	// The deferred close serves only the panic/Goexit path. The normal path
	// closes exactly once below, where closeErr is inspected instead of
	// discarded, and released stops the defer from closing a second time.
	released := false
	defer func() {
		if !released {
			_ = file.Close()
			// A panic/Goexit cannot leave a usable custody certificate.
			custody.err = ErrExecutionInputCustody
		}
	}()
	useErr := use(file)
	protected := linuxInputProtected(file, entry)
	closeErr := file.Close()
	released = true
	if _, err := custody.checkLocked(ctx, name); err != nil || !protected || closeErr != nil {
		custody.err = ErrExecutionInputCustody
		return custody.err
	}
	return useErr
}

// Close releases all owned FDs and drops their stat snapshots; anonymous bytes
// disappear after the last holder releases them. It does not certify that a
// caller joined children or released escaped mappings/duplicates. No source
// deletion, thaw or disk cleanup occurs.
func (custody *LinuxExecutionInputCustody) Close() error {
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if !custody.closed {
		custody.closed = true
		for _, entry := range custody.inputs {
			if entry.file.Close() != nil {
				custody.err = ErrExecutionInputCustody
			}
		}
		// Retention hygiene only: checkLocked refuses on closed before it
		// consults the map, so clearing it changes no observable result and
		// keeps closed descriptors and their snapshots from being retained.
		clear(custody.inputs)
	}
	return custody.err
}
