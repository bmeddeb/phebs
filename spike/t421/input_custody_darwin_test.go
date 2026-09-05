//go:build darwin

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func TestExecutionInputCustodyCopiesRealImageAndFixedBytes(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	image := inputCustodyTestSpec(t, "true", "/usr/bin/true", true)
	originalInput := inputCustodyTestStat(t, input.Path)
	originalImage := inputCustodyTestStat(t, image.Path)
	writer, err := os.OpenFile(input.Path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input, image})
	if err != nil {
		t.Fatal(err)
	}
	if !inputCustodySame(originalInput, inputCustodyTestStat(t, input.Path)) ||
		!inputCustodySame(originalImage, inputCustodyTestStat(t, image.Path)) {
		t.Fatal("construction changed original input metadata")
	}
	for _, selected := range []ExecutionInputCopy{input, image} {
		path, err := custody.Check(t.Context(), selected.Name)
		if err != nil || path != filepath.Join(custody.Directory(), selected.Name) {
			t.Fatalf("check: %q, %v", path, err)
		}
		copied := inputCustodyTestStat(t, path)
		mode := os.FileMode(0o400)
		if selected.Executable {
			mode = 0o500
		}
		if copied.Mode().Perm() != mode || !inputCustodyProtected(copied) ||
			os.SameFile(copied, inputCustodyTestStat(t, selected.Path)) {
			t.Fatalf("copy is not a distinct protected %o object", mode)
		}
		if got := inputCustodyTestSpec(t, selected.Name, path, selected.Executable).SHA256; got != selected.SHA256 {
			t.Fatalf("copied digest: %s, want %s", got, selected.SHA256)
		}
	}
	inputCustodyTestReadOnlyDescriptors(t, custody)
	path, err := custody.Check(t.Context(), image.Name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, path).CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatalf("real copied native image: %v, output %q", err, output)
	}
	if _, err := writer.WriteAt([]byte("changed original"), 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Truncate(16); err != nil {
		t.Fatal(err)
	}
	path, err = custody.Check(t.Context(), input.Name)
	if err != nil || inputCustodyTestSpec(t, input.Name, path, false).SHA256 != input.SHA256 {
		t.Fatalf("original writer changed the fresh protected copy: %v", err)
	}
}

func TestExecutionInputCustodyBlocksNativeMutations(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(custody.Directory(), input.Name)
	for _, test := range []struct {
		name   string
		mutate func() error
	}{
		{"new writer", func() error {
			file, err := os.OpenFile(path, os.O_WRONLY, 0)
			if file != nil {
				_ = file.Close()
			}
			return err
		}},
		{"truncate", func() error { return os.Truncate(path, 0) }},
		{"unlink", func() error { return os.Remove(path) }},
		{"file rename", func() error { return os.Rename(path, filepath.Join(parent, "moved-file")) }},
		{"hardlink outside root", func() error { return os.Link(path, filepath.Join(parent, "linked-file")) }},
		{"file mode", func() error { return custody.inputs[input.Name].file.Chmod(0o600) }},
		{"file times", func() error { return os.Chtimes(path, time.Unix(1, 0), time.Unix(1, 0)) }},
		{"root addition", func() error { return os.WriteFile(filepath.Join(custody.Directory(), "extra"), []byte("x"), 0o600) }},
		{"root child directory", func() error { return os.Mkdir(filepath.Join(custody.Directory(), "extra-directory"), 0o700) }},
		{"root child symlink", func() error { return os.Symlink(input.Path, filepath.Join(custody.Directory(), "extra-link")) }},
		{"root rename", func() error { return os.Rename(custody.Directory(), filepath.Join(parent, "moved-root")) }},
		{"root mode", func() error { return custody.root.Chmod(0o750) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.mutate(); !errors.Is(err, syscall.EPERM) {
				t.Fatalf("mutation error = %v, want EPERM", err)
			}
			if _, err := custody.Check(t.Context(), input.Name); err != nil {
				t.Fatalf("denied mutation changed protected custody: %v", err)
			}
		})
	}
}

func TestExecutionInputCustodyRefusalsAreSticky(t *testing.T) {
	for _, name := range []string{"unknown", "cancel", "nil context", "file flag cleared", "root flag cleared", "file clear and reseal", "root clear and reseal", "parent mode"} {
		t.Run(name, func(t *testing.T) {
			parent, input := inputCustodyTestFixture(t)
			custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			selectedName := input.Name
			switch name {
			case "unknown":
				selectedName = "not-selected"
			case "cancel":
				cancel()
			case "nil context":
				ctx = nil
			case "parent mode":
				if err := os.Chmod(parent, 0o750); err != nil {
					t.Fatal(err)
				}
			default:
				file := custody.inputs[input.Name].file
				if strings.HasPrefix(name, "root") {
					file = custody.root
				}
				before := inputCustodyTestFileStat(t, file)
				if err := inputCustodyFlag(file, false); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(name, "reseal") {
					if err := inputCustodyFlag(file, true); err != nil {
						t.Fatal(err)
					}
					after := inputCustodyTestFileStat(t, file)
					if before.Sys().(*syscall.Stat_t).Ctimespec == after.Sys().(*syscall.Stat_t).Ctimespec {
						t.Fatal("native clear/reseal did not advance change time")
					}
				}
			}
			path, err := custody.Check(ctx, selectedName)
			inputCustodyTestRefusal(t, path, err)
			if err := os.Chmod(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := inputCustodyFlag(custody.root, true); err != nil {
				t.Fatal(err)
			}
			if err := inputCustodyFlag(custody.inputs[input.Name].file, true); err != nil {
				t.Fatal(err)
			}
			path, err = custody.Check(t.Context(), input.Name)
			inputCustodyTestRefusal(t, path, err)
		})
	}
}

func TestExecutionInputCustodyRefusesReplacedParent(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input})
	if err != nil {
		t.Fatal(err)
	}
	moved := parent + "-moved"
	if err := os.Rename(parent, moved); err != nil {
		t.Fatal(err)
	}
	restore := true
	t.Cleanup(func() {
		if restore {
			if err := os.Remove(parent); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Error(err)
				return
			}
			if err := os.Rename(moved, parent); err != nil {
				t.Error(err)
			}
		}
	})
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := custody.Check(t.Context(), input.Name)
	inputCustodyTestRefusal(t, path, err)
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, parent); err != nil {
		t.Fatal(err)
	}
	restore = false
	path, err = custody.Check(t.Context(), input.Name)
	inputCustodyTestRefusal(t, path, err)
}

func TestExecutionInputCustodyCloseRetainsProtectionAndClosesDescriptors(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input})
	if err != nil {
		t.Fatal(err)
	}
	if err := custody.Close(); err != nil {
		t.Fatal(err)
	}
	if err := custody.Close(); err != nil {
		t.Fatal(err)
	}
	inputCustodyTestClosed(t, custody)
	for _, path := range []string{custody.Directory(), filepath.Join(custody.Directory(), input.Name)} {
		if !inputCustodyProtected(inputCustodyTestStat(t, path)) {
			t.Fatal("Close thawed retained custody")
		}
	}
	path, err := custody.Check(t.Context(), input.Name)
	inputCustodyTestRefusal(t, path, err)
}

func TestExecutionInputCustodyRejectsMalformedSelectionsBeforeMutation(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	for _, name := range []string{"", "-", ".", "..", "../escape", "a/b", "A", "a_b", "a\x00b", "é", strings.Repeat("a", 65)} {
		t.Run("name "+strconv.Quote(name), func(t *testing.T) {
			selected := input
			selected.Name = name
			custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{selected})
			if custody != nil || err != ErrExecutionInputCustody {
				t.Fatalf("preflight = %v, %v", custody, err)
			}
		})
	}
	for _, test := range []struct{ name, field, value string }{
		{"short digest", "digest", "sha256:00"},
		{"upper digest", "digest", "sha256:" + strings.Repeat("A", 64)},
		{"relative source", "path", "source"},
		{"unclean source", "path", filepath.Dir(input.Path) + "/./source"},
		{"long source", "path", "/" + strings.Repeat("a", maxInputCustodyPathBytes)},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := input
			if test.field == "path" {
				selected.Path = test.value
			} else {
				selected.SHA256 = test.value
			}
			custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{selected})
			if custody != nil || err != ErrExecutionInputCustody {
				t.Fatalf("preflight = %v, %v", custody, err)
			}
		})
	}
	for _, selected := range [][]ExecutionInputCopy{nil, {input, input}, make([]ExecutionInputCopy, maxInputCustodyFiles+1)} {
		custody, err := inputCustodyTestProtect(t, t.Context(), parent, selected)
		if custody != nil || err != ErrExecutionInputCustody {
			t.Fatalf("inventory preflight = %v, %v", custody, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, candidate := range []context.Context{nil, ctx} {
		custody, err := inputCustodyTestProtect(t, candidate, parent, []ExecutionInputCopy{input})
		if custody != nil || err != ErrExecutionInputCustody {
			t.Fatalf("context preflight = %v, %v", custody, err)
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preflight mutated parent: %d, %v", len(entries), err)
	}
}

func TestExecutionInputCustodyRejectsUnadmittedParent(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	link := parent + "-symlink"
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative", "/", parent + "/.", link, "/" + strings.Repeat("a", maxInputCustodyPathBytes)} {
		custody, err := inputCustodyTestProtect(t, t.Context(), path, []ExecutionInputCopy{input})
		if custody != nil || err != ErrExecutionInputCustody {
			t.Fatalf("parent preflight = %v, %v", custody, err)
		}
	}
	if err := os.Chmod(parent, 0o750); err != nil {
		t.Fatal(err)
	}
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input})
	if custody != nil || err != ErrExecutionInputCustody {
		t.Fatalf("parent mode = %v, %v", custody, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("bad parent selection mutated parent: %d, %v", len(entries), err)
	}
}

func TestExecutionInputCustodyRejectsSourceTypeModeAndSize(t *testing.T) {
	for _, name := range []string{"symlink", "directory", "fifo", "missing", "exec as data", "data as exec", "empty executable", "setuid", "setgid", "file byte limit"} {
		t.Run(name, func(t *testing.T) {
			parent, input := inputCustodyTestFixture(t)
			source := input.Path
			switch name {
			case "symlink":
				input.Path += "-link"
				if err := os.Symlink(source, input.Path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				input.Path = filepath.Dir(source)
			case "fifo":
				input.Path += "-fifo"
				if err := unix.Mkfifo(input.Path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				input.Path += "-missing"
			case "exec as data":
				if err := os.Chmod(source, 0o700); err != nil {
					t.Fatal(err)
				}
			case "data as exec":
				input.Executable = true
			case "empty executable":
				if err := os.Truncate(source, 0); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(source, 0o700); err != nil {
					t.Fatal(err)
				}
				input.Executable = true
			case "setuid", "setgid":
				mode := os.FileMode(0o600) | os.ModeSetuid
				if name == "setgid" {
					mode = 0o600 | os.ModeSetgid
				}
				if err := os.Chmod(source, mode); err != nil {
					t.Fatal(err)
				}
			case "file byte limit":
				if err := os.Truncate(source, maxInputCustodyFileBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input})
			if custody == nil || err != ErrExecutionInputCustody {
				t.Fatalf("source refusal = %v, %v", custody, err)
			}
			inputCustodyTestClosed(t, custody)
			entries, err := os.ReadDir(custody.Directory())
			if err != nil || len(entries) != 0 {
				t.Fatalf("bad source produced a copy: %d, %v", len(entries), err)
			}
		})
	}
}

func TestExecutionInputCustodyByteAndInventoryCeilings(t *testing.T) {
	if maxInputCustodyFiles != 64 || maxInputCustodyFileBytes != 256<<20 || maxInputCustodyBytes != 2<<30 || maxInputCustodyPathBytes != 4096 {
		t.Fatal("local input-custody limits changed")
	}
	parent, input := inputCustodyTestFixture(t)
	root, err := os.OpenRoot(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	// Exercise the actual aggregate remaining-byte predicate without writing a
	// two-GiB fixture. The constructor passes its monotonically reduced balance.
	file, size, err := copyExecutionInput(t.Context(), root, input, 1)
	if file != nil || size != 0 || err != ErrExecutionInputCustody {
		t.Fatalf("remaining-byte guard = %v, %d, %v", file, size, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("remaining-byte refusal wrote a file: %d, %v", len(entries), err)
	}
	selected := make([]ExecutionInputCopy, maxInputCustodyFiles)
	for i := range selected {
		selected[i] = input
		selected[i].Name = fmt.Sprintf("input-%d", i)
	}
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, selected)
	if err != nil || len(custody.inputs) != maxInputCustodyFiles {
		t.Fatalf("exact file limit: %v", err)
	}
	for _, spec := range selected {
		if _, err := custody.Check(t.Context(), spec.Name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecutionInputCustodyDigestFailureRetainsClosedCopies(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	second := input
	second.Name = "second"
	second.SHA256 = "sha256:" + strings.Repeat("0", 64)
	custody, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{input, second})
	if custody == nil || err != ErrExecutionInputCustody {
		t.Fatalf("digest refusal = %v, %v", custody, err)
	}
	inputCustodyTestClosed(t, custody)
	for _, name := range []string{input.Name, second.Name} {
		if !inputCustodyProtected(inputCustodyTestStat(t, filepath.Join(custody.Directory(), name))) {
			t.Fatal("digest failure removed or thawed copy")
		}
	}
	if !inputCustodyProtected(inputCustodyTestStat(t, custody.Directory())) {
		t.Fatal("digest failure thawed root")
	}
	path, err := custody.Check(t.Context(), input.Name)
	inputCustodyTestRefusal(t, path, err)
}

func TestExecutionInputCustodyPartialCopyFailureRetainsClosedCustody(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	data := bytes.Repeat([]byte("x"), 128<<10)
	if err := os.WriteFile(input.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	input = inputCustodyTestSpec(t, input.Name, input.Path, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := &inputCustodyCancelAfterWrite{Context: ctx, cancel: cancel, parent: parent, name: input.Name}
	custody, err := inputCustodyTestProtect(t, observed, parent, []ExecutionInputCopy{input})
	if custody == nil || err != ErrExecutionInputCustody {
		t.Fatalf("partial copy refusal = %v, %v", custody, err)
	}
	inputCustodyTestClosed(t, custody)
	partial := inputCustodyTestStat(t, filepath.Join(custody.Directory(), input.Name))
	if partial.Size() <= 0 || partial.Size() >= int64(len(data)) {
		t.Fatalf("not a genuinely partial retained copy: %d", partial.Size())
	}
	if len(custody.inputs) != 0 {
		t.Fatal("partial copy became a published input")
	}
	path, err := custody.Check(t.Context(), input.Name)
	inputCustodyTestRefusal(t, path, err)
}

func TestExecutionInputCustodyNativeFlagDoesNotRevokeExistingWriter(t *testing.T) {
	parent, input := inputCustodyTestFixture(t)
	// This separate native fixture is never published as protected custody.
	// The result is why the constructor closes its writer before protection.
	writer, err := os.OpenFile(input.Path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	original := inputCustodyTestFileStat(t, writer)
	t.Cleanup(func() {
		inputCustodyTestClearOwnedFlag(t, writer, original)
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
		current, err := os.Lstat(input.Path)
		if err != nil || !os.SameFile(original, current) {
			t.Error("native fixture path changed; retaining it")
			return
		}
		if err := os.Remove(input.Path); err != nil {
			t.Error(err)
		}
	})
	if err := inputCustodyFlag(writer, true); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt([]byte("native writer remains live"), 0); err != nil {
		t.Fatalf("existing writable FD: %v", err)
	}
	if err := writer.Truncate(4); err != nil {
		t.Fatalf("existing writable FD truncate: %v", err)
	}
	fresh, err := os.OpenFile(input.Path, os.O_WRONLY, 0)
	if fresh != nil {
		_ = fresh.Close()
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("fresh writable open: %v", err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatal("native fixture unexpectedly constructed custody")
	}
}

type inputCustodyCancelAfterWrite struct {
	context.Context
	cancel context.CancelFunc
	parent string
	name   string
}

func (ctx *inputCustodyCancelAfterWrite) Err() error {
	if err := ctx.Context.Err(); err != nil {
		return err
	}
	// One constructor-owned directory and one fixed expected entry. Cancellation
	// is latched after the first real copy write, not by timing a goroutine.
	entries, err := os.ReadDir(ctx.parent)
	if err == nil && len(entries) == 1 {
		info, err := os.Lstat(filepath.Join(ctx.parent, entries[0].Name(), ctx.name))
		if err == nil && info.Size() > 0 {
			ctx.cancel()
		}
	}
	return ctx.Context.Err()
}

func inputCustodyTestFixture(t *testing.T) (string, ExecutionInputCopy) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "source")
	if err := os.WriteFile(source, []byte("fixed neutral configuration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return parent, inputCustodyTestSpec(t, "config", source, false)
}

func inputCustodyTestSpec(t *testing.T, name, path string, executable bool) ExecutionInputCopy {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return ExecutionInputCopy{Name: name, Path: path, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(data)), Executable: executable}
}

func inputCustodyTestStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func inputCustodyTestFileStat(t *testing.T, file *os.File) os.FileInfo {
	t.Helper()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func inputCustodyTestRefusal(t *testing.T, path string, err error) {
	t.Helper()
	if path != "" || err != ErrExecutionInputCustody {
		t.Fatalf("refusal leaked a path or nonclosed error: %q, %v", path, err)
	}
}

func inputCustodyTestClosed(t *testing.T, custody *ExecutionInputCustody) {
	t.Helper()
	if !custody.closed {
		t.Fatal("custody was not closed")
	}
	for _, file := range []*os.File{custody.root, custody.parent} {
		if file != nil {
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("retained descriptor remains open: %v", err)
			}
		}
	}
	for _, input := range custody.inputs {
		if _, err := input.file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("retained copy descriptor remains open: %v", err)
		}
	}
	if custody.tree != nil {
		if _, err := custody.tree.Stat("."); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("retained root descriptor remains open: %v", err)
		}
	}
}

func inputCustodyTestReadOnlyDescriptors(t *testing.T, custody *ExecutionInputCustody) {
	t.Helper()
	identities := []os.FileInfo{custody.rootInfo, custody.parentInfo}
	for _, input := range custody.inputs {
		identities = append(identities, input.info)
	}
	directory, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry)
		if err != nil {
			t.Fatal(err)
		}
		var actual unix.Stat_t
		if unix.Fstat(fd, &actual) != nil {
			continue
		} // The enumeration FD has closed.
		for _, info := range identities {
			want := info.Sys().(*syscall.Stat_t)
			if actual.Dev != want.Dev || actual.Ino != want.Ino {
				continue
			}
			matched++
			flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
			if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
				t.Fatalf("owned FD %d is not read-only: %d, %v", fd, flags, err)
			}
			flags, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			if err != nil || flags&unix.FD_CLOEXEC == 0 {
				t.Fatalf("owned FD %d lacks CLOEXEC: %d, %v", fd, flags, err)
			}
			break
		}
	}
	// Includes os.Root's otherwise private FD as well as the explicit copies,
	// root and parent descriptors. Independent cleanup FDs are read-only too.
	if matched < len(custody.inputs)+3 {
		t.Fatalf("incomplete owned-FD inspection: %d", matched)
	}
}

type inputCustodyCleanupFile struct {
	name string
	file *os.File
	info os.FileInfo
}

func inputCustodyTestProtect(t *testing.T, ctx context.Context, parent string, inputs []ExecutionInputCopy) (*ExecutionInputCustody, error) {
	t.Helper()
	custody, resultErr := ProtectExecutionInputs(ctx, parent, inputs)
	if custody == nil {
		return nil, resultErr
	}
	var root *os.File
	var rootInfo os.FileInfo
	var files []inputCustodyCleanupFile
	// Register first: even a later assertion must leave exact-owned cleanup live.
	t.Cleanup(func() {
		if err := custody.Close(); err != nil && err != ErrExecutionInputCustody {
			t.Error(err)
		}
		if root == nil {
			t.Error("cannot safely clean custody without its held root")
			return
		}
		defer func() { _ = root.Close() }()
		for _, item := range files {
			inputCustodyTestClearOwnedFlag(t, item.file, item.info)
			if err := item.file.Close(); err != nil {
				t.Error(err)
			}
		}
		inputCustodyTestClearOwnedFlag(t, root, rootInfo)
		for _, item := range files {
			var current unix.Stat_t
			if err := unix.Fstatat(int(root.Fd()), item.name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				t.Error(err)
				continue
			}
			known := item.info.Sys().(*syscall.Stat_t)
			if current.Dev != known.Dev || current.Ino != known.Ino || current.Mode&unix.S_IFMT != unix.S_IFREG {
				t.Error("cleanup entry identity changed; retaining it")
				continue
			}
			if err := unix.Unlinkat(int(root.Fd()), item.name, 0); err != nil {
				t.Error(err)
			}
		}
		current, err := os.Lstat(custody.Directory())
		if err != nil || !os.SameFile(rootInfo, current) {
			t.Error("cleanup root path changed; retaining it")
			return
		}
		if err := os.Remove(custody.Directory()); err != nil {
			t.Error(err)
		}
		if _, err := os.Lstat(custody.Directory()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("custody residue remains: %v", err)
		}
	})
	var err error
	root, err = t4013.OpenHostImage(custody.Directory())
	if err != nil {
		t.Fatal(err)
	}
	rootInfo = inputCustodyTestFileStat(t, root)
	if !inputCustodyOwned(rootInfo) || !rootInfo.IsDir() {
		t.Fatal("test cleanup root is not an owned directory")
	}
	seen := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		// A malformed-name regression must never make fixture cleanup escape the
		// exact root or operate on dot/dot-dot as an entry.
		if input.Name == "" || input.Name == "." || input.Name == ".." ||
			filepath.Base(input.Name) != input.Name || strings.ContainsRune(input.Name, 0) {
			continue
		}
		if seen[input.Name] {
			continue
		}
		seen[input.Name] = true
		fd, err := unix.Openat(int(root.Fd()), input.Name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if errors.Is(err, syscall.ENOENT) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		file := os.NewFile(uintptr(fd), input.Name)
		info := inputCustodyTestFileStat(t, file)
		files = append(files, inputCustodyCleanupFile{input.Name, file, info})
		if !inputCustodyOwned(info) || !info.Mode().IsRegular() {
			t.Fatal("test cleanup entry is not an owned regular copy")
		}
	}
	return custody, resultErr
}

func inputCustodyTestClearOwnedFlag(t *testing.T, file *os.File, known os.FileInfo) {
	t.Helper()
	current, err := file.Stat()
	if err != nil || known == nil || !os.SameFile(known, current) || !inputCustodyOwned(current) {
		t.Error("refusing to clear flags on an unverified fixture descriptor")
		return
	}
	// Only the fixture's owner immutable bit; never recursive/path-based chflags
	// or replacement of unrelated flag bits. The exact FD owns this cleanup.
	flags := current.Sys().(*syscall.Stat_t).Flags
	if flags&unix.UF_IMMUTABLE != 0 {
		if err := unix.Fchflags(int(file.Fd()), int(flags&^unix.UF_IMMUTABLE)); err != nil {
			t.Error(err)
		}
	}
}
