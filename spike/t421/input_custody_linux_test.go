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
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func linuxInputFixture(t *testing.T) ExecutionInputCopy {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("neutral sealed input\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return linuxInputSelection(t, path, "neutral", false)
}

func linuxInputSelection(t *testing.T, path, name string, executable bool) ExecutionInputCopy {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	return ExecutionInputCopy{Name: name, Path: canonical, SHA256: "sha256:" + hex.EncodeToString(digest[:]), Executable: executable}
}

func requireLinuxInputCustody(t *testing.T, inputs ...ExecutionInputCopy) *LinuxExecutionInputCustody {
	t.Helper()
	custody, err := ProtectLinuxExecutionInputs(context.Background(), inputs)
	if err != nil {
		t.Fatalf("native kernel custody must pass, without skipping: %v", err)
	}
	t.Cleanup(func() { _ = custody.Close() })
	return custody
}

func TestLinuxInputCustodyProtectsBytesAndReleasesFDs(t *testing.T) {
	input := linuxInputFixture(t)
	before := linuxInputFDCount(t)
	custody := requireLinuxInputCustody(t, input)
	if linuxInputFDCount(t) != before+1 {
		t.Fatal("custody must retain exactly one read descriptor")
	}
	identity, err := custody.Check(context.Background(), input.Name)
	if err != nil || identity.Name != input.Name || identity.SHA256 != input.SHA256 || identity.Bytes != 21 || identity.Executable {
		t.Fatalf("identity: %+v, %v", identity, err)
	}
	// Original files and their namespace remain caller-owned and mutable.
	if err := os.WriteFile(input.Path, []byte("source changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(input.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input.Path, []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := custody.WithInput(context.Background(), input.Name, func(file *os.File) error {
		bytes, err := io.ReadAll(file)
		if err != nil || string(bytes) != "neutral sealed input\n" || linuxInputFDCount(t) != before+2 {
			t.Fatalf("sealed bytes or scoped FD count: %q, %v", bytes, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := custody.Close(); err != nil || linuxInputFDCount(t) != before {
		t.Fatalf("close must release owned anonymous custody: %v", err)
	}
	if err := custody.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := custody.Check(context.Background(), input.Name); !errors.Is(err, ErrExecutionInputCustody) {
		t.Fatal("closed custody accepted")
	}
}

func TestLinuxInputCustodyKernelMutationRefusals(t *testing.T) {
	for _, executable := range []bool{false, true} {
		t.Run(fmt.Sprint(executable), func(t *testing.T) {
			input := linuxInputFixture(t)
			if executable {
				input = linuxInputSelection(t, "/usr/bin/true", "executable", true)
			}
			custody := requireLinuxInputCustody(t, input)
			entry := custody.inputs[input.Name]
			// A separately reopened writable descriptor must still be powerless;
			// file mode alone cannot establish this protection for the owner.
			// Non-execute permission changes are allowed by the seal API, but
			// custody separately detects that metadata drift even when restored.
			if err := entry.file.Chmod(entry.fileModeForTest() | 0o200); err != nil {
				t.Fatal(err)
			}
			fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", entry.file.Fd()), unix.O_RDWR|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(fd) }()
			if err := entry.file.Chmod(entry.fileModeForTest()); err != nil {
				t.Fatal(err)
			}
			operations := []struct {
				name string
				run  func() error
			}{
				{"write", func() error { _, err := unix.Pwrite(fd, []byte("x"), 0); return err }},
				{"shrink", func() error { return unix.Ftruncate(fd, 0) }},
				{"grow", func() error { return unix.Ftruncate(fd, entry.stat.Size+1) }},
				{"execute-mode", func() error { return unix.Fchmod(fd, entry.stat.Mode^0o100) }},
				{"seal-change", func() error {
					_, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_FUTURE_WRITE)
					return err
				}},
				{"shared-write-map", func() error {
					mapping, err := unix.Mmap(fd, 0, 1, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
					if err == nil {
						_ = unix.Munmap(mapping)
					}
					return err
				}},
			}
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					if err := operation.run(); !errors.Is(err, unix.EPERM) {
						t.Fatalf("kernel mutation must return EPERM: %v", err)
					}
				})
			}
			if _, err := custody.Check(context.Background(), input.Name); !errors.Is(err, ErrExecutionInputCustody) {
				t.Fatal("allowed non-execute metadata drift must still invalidate custody")
			}
		})
	}
}

func (entry linuxExecutionInput) fileModeForTest() os.FileMode {
	if entry.identity.Executable {
		return 0o500
	}
	return 0o400
}

func TestLinuxInputCustodyJoinedExecutableAndNoexecData(t *testing.T) {
	input := linuxInputSelection(t, "/usr/bin/true", "executable", true)
	custody := requireLinuxInputCustody(t, input)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := custody.WithInput(ctx, input.Name, func(file *os.File) error {
		cmd := exec.CommandContext(ctx, "/proc/self/fd/3")
		cmd.ExtraFiles = []*os.File{file}
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
		return cmd.Run() // Joined before loan release; loader closure is not admitted.
	}); err != nil {
		t.Fatal(err)
	}
	data := linuxInputFixture(t)
	dataCustody := requireLinuxInputCustody(t, data)
	if err := dataCustody.WithInput(ctx, data.Name, func(file *os.File) error {
		cmd := exec.CommandContext(ctx, "/proc/self/fd/3")
		cmd.ExtraFiles = []*os.File{file}
		if err := cmd.Run(); !errors.Is(err, unix.EACCES) {
			t.Fatalf("data executable launch must be denied: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxInputCustodyRefusesMalformedSelectionsAndCleansPartial(t *testing.T) {
	input := linuxInputFixture(t)
	symlink := filepath.Join(filepath.Dir(input.Path), "link")
	if err := os.Symlink(input.Path, symlink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(filepath.Dir(input.Path), "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	oversize := filepath.Join(filepath.Dir(input.Path), "oversize")
	file, err := os.Create(oversize)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxInputCustodyFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*ExecutionInputCopy)
	}{
		{"relative", func(input *ExecutionInputCopy) { input.Path = "input" }},
		{"unclean", func(input *ExecutionInputCopy) { input.Path += "/../input" }},
		{"missing", func(input *ExecutionInputCopy) { input.Path += "-missing" }},
		{"symlink", func(input *ExecutionInputCopy) { input.Path = symlink }},
		{"fifo", func(input *ExecutionInputCopy) { input.Path = fifo }},
		{"oversize", func(input *ExecutionInputCopy) { input.Path = oversize }},
		{"directory", func(input *ExecutionInputCopy) { input.Path = filepath.Dir(input.Path) }},
		{"path-bound", func(input *ExecutionInputCopy) { input.Path = "/" + strings.Repeat("a", maxInputCustodyPathBytes) }},
		{"name", func(input *ExecutionInputCopy) { input.Name = "../escape" }},
		{"empty-name", func(input *ExecutionInputCopy) { input.Name = "" }},
		{"name-bound", func(input *ExecutionInputCopy) { input.Name = strings.Repeat("a", 65) }},
		{"digest-format", func(input *ExecutionInputCopy) { input.SHA256 = "invalid" }},
		{"digest-mismatch", func(input *ExecutionInputCopy) { input.SHA256 = "sha256:" + strings.Repeat("0", 64) }},
		{"execute-mismatch", func(input *ExecutionInputCopy) { input.Executable = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := input
			changed.Name = "second"
			test.change(&changed)
			before := linuxInputFDCount(t)
			custody, err := ProtectLinuxExecutionInputs(context.Background(), []ExecutionInputCopy{input, changed})
			if !errors.Is(err, ErrExecutionInputCustody) || custody != nil || linuxInputFDCount(t) != before {
				t.Fatalf("refusal/partial cleanup: %v, %v", custody, err)
			}
		})
	}
	for _, inputs := range [][]ExecutionInputCopy{nil, {input, input}, slices.Repeat([]ExecutionInputCopy{input}, 65)} {
		if custody, err := ProtectLinuxExecutionInputs(context.Background(), inputs); err == nil || custody != nil {
			t.Fatal("selection bounds accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if custody, err := ProtectLinuxExecutionInputs(ctx, []ExecutionInputCopy{input}); err == nil || custody != nil {
			t.Fatal("invalid context accepted")
		}
	}
	if _, err := copyLinuxExecutionInput(context.Background(), input, 20, make([]byte, 32<<10)); err == nil {
		t.Fatal("aggregate remaining-byte guard accepted an oversized next input")
	}
}

func TestLinuxInputCustodyFileCeilingAndUnsealedRefusal(t *testing.T) {
	input := linuxInputFixture(t)
	inputs := make([]ExecutionInputCopy, maxInputCustodyFiles)
	for i := range inputs {
		inputs[i] = input
		inputs[i].Name = fmt.Sprintf("input%d", i)
	}
	before := linuxInputFDCount(t)
	custody := requireLinuxInputCustody(t, inputs...)
	if linuxInputFDCount(t) != before+maxInputCustodyFiles {
		t.Fatal("file ceiling FD accounting")
	}
	if err := custody.Close(); err != nil || linuxInputFDCount(t) != before {
		t.Fatal("file ceiling cleanup")
	}

	fd, err := unix.MemfdCreate("phebs-sealed-input", unix.MFD_ALLOW_SEALING|unix.MFD_CLOEXEC|unix.MFD_NOEXEC_SEAL)
	if err != nil {
		t.Fatal(err)
	}
	writer := os.NewFile(uintptr(fd), "unsealed")
	defer func() { _ = writer.Close() }()
	if _, err := writer.Write([]byte("unsealed")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Chmod(0o400); err != nil {
		t.Fatal(err)
	}
	reader, err := openLinuxInputReadOnly(writer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	entry := linuxExecutionInput{file: reader, identity: LinuxExecutionInputIdentity{Bytes: 8}}
	if err := unix.Fstat(int(reader.Fd()), &entry.stat); err != nil {
		t.Fatal(err)
	}
	if linuxInputProtected(reader, entry) {
		t.Fatal("nonexec mode/execute seal alone accepted without byte/size/seal-set protection")
	}
	if _, err := unix.FcntlInt(writer.Fd(), unix.F_ADD_SEALS, linuxInputSeals); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(int(reader.Fd()), &entry.stat); err != nil {
		t.Fatal(err)
	}
	if !linuxInputProtected(reader, entry) {
		t.Fatal("complete seals refused")
	}
}

func TestLinuxInputCustodyRejectsSetIDAndScripts(t *testing.T) {
	for _, mode := range []os.FileMode{0o600 | os.ModeSetuid, 0o600 | os.ModeSetgid, 0o700} {
		t.Run(mode.String(), func(t *testing.T) {
			input := linuxInputFixture(t)
			if err := os.Chmod(input.Path, mode); err != nil {
				t.Fatal(err)
			}
			input.Executable = mode.Perm()&0o111 != 0
			info, err := os.Stat(input.Path)
			if err != nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != mode&(os.ModeSetuid|os.ModeSetgid) {
				t.Fatal("set-id fixture not applied", err)
			}
			if custody, err := ProtectLinuxExecutionInputs(context.Background(), []ExecutionInputCopy{input}); custody != nil || err == nil {
				t.Fatal("set-id/script accepted")
			}
		})
	}
}

func TestLinuxInputCustodyRefusesInvalidLoanAndCancellation(t *testing.T) {
	for _, fault := range []string{"nil", "close", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			input := linuxInputFixture(t)
			custody := requireLinuxInputCustody(t, input)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var callback func(*os.File) error
			if fault != "nil" {
				callback = func(file *os.File) error {
					if fault == "close" {
						return file.Close()
					}
					cancel()
					return nil
				}
			}
			before := linuxInputFDCount(t)
			if err := custody.WithInput(ctx, input.Name, callback); !errors.Is(err, ErrExecutionInputCustody) {
				t.Fatal("invalid loan accepted", err)
			}
			if linuxInputFDCount(t) != before {
				t.Fatal("invalid loan leaked FD")
			}
			if _, err := custody.Check(context.Background(), input.Name); err == nil {
				t.Fatal("loan failure did not latch")
			}
		})
	}
}

func TestLinuxInputCustodyStickyChecksAndScopedClosure(t *testing.T) {
	for _, fault := range []string{"unknown", "canceled", "closed-keeper", "fd-flags", "mode"} {
		t.Run(fault, func(t *testing.T) {
			input := linuxInputFixture(t)
			custody := requireLinuxInputCustody(t, input)
			ctx := context.Background()
			name := input.Name
			entry := custody.inputs[name]
			switch fault {
			case "unknown":
				name = "unknown"
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "closed-keeper":
				_ = entry.file.Close()
			case "fd-flags":
				_, _ = unix.FcntlInt(entry.file.Fd(), unix.F_SETFD, 0)
			case "mode":
				if err := entry.file.Chmod(0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := custody.Check(ctx, name); !errors.Is(err, ErrExecutionInputCustody) {
				t.Fatal("fault accepted")
			}
			if _, err := custody.Check(context.Background(), input.Name); !errors.Is(err, ErrExecutionInputCustody) {
				t.Fatal("fault did not latch")
			}
		})
	}
	input := linuxInputFixture(t)
	custody := requireLinuxInputCustody(t, input)
	callbackErr := errors.New("caller error")
	if err := custody.WithInput(context.Background(), input.Name, func(*os.File) error { return callbackErr }); err != callbackErr {
		t.Fatal(err)
	}
	if _, err := custody.Check(context.Background(), input.Name); err != nil {
		t.Fatal(err)
	}
	before := linuxInputFDCount(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("callback panic missing")
			}
		}()
		_ = custody.WithInput(context.Background(), input.Name, func(*os.File) error { panic("caller panic") })
	}()
	if linuxInputFDCount(t) != before {
		t.Fatal("panic leaked loan")
	}
	if _, err := custody.Check(context.Background(), input.Name); err == nil {
		t.Fatal("panic did not invalidate custody")
	}
	if err := custody.Close(); !errors.Is(err, ErrExecutionInputCustody) {
		t.Fatal(err)
	}
	custody = requireLinuxInputCustody(t, input)
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		_ = custody.WithInput(context.Background(), input.Name, func(*os.File) error { close(entered); <-release; return nil })
	}()
	<-entered
	go func() { _ = custody.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("close overtook scoped use")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not finish after scoped use")
	}
}

func TestLinuxInputCustodyELFScreenBounds(t *testing.T) {
	valid := make([]byte, 120)
	copy(valid, "\x7fELF\x02\x01\x01")
	binary.LittleEndian.PutUint16(valid[16:18], 2)
	binary.LittleEndian.PutUint16(valid[18:20], 62)
	binary.LittleEndian.PutUint32(valid[20:24], 1)
	binary.LittleEndian.PutUint64(valid[32:40], 64)
	binary.LittleEndian.PutUint16(valid[52:54], 64)
	binary.LittleEndian.PutUint16(valid[54:56], 56)
	binary.LittleEndian.PutUint16(valid[56:58], 1)
	for _, offset := range []int{-1, 0, 4, 5, 6, 16, 18, 20, 32, 52, 54, 56} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			bytes := slices.Clone(valid)
			if offset >= 0 {
				bytes[offset] ^= 0xff
			}
			path := filepath.Join(t.TempDir(), "elf")
			if err := os.WriteFile(path, bytes, 0o700); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			if linuxInputELF(file, int64(len(bytes))) != (offset == -1) {
				t.Fatal("ELF screen result")
			}
			if linuxInputELF(file, 63) {
				t.Fatal("short ELF accepted")
			}
		})
	}
}

func linuxInputFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && target == "/memfd:phebs-sealed-input (deleted)" {
			count++
		}
	}
	return count
}
