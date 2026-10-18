//go:build linux && phebs_t42_native

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This required native test is deliberately not a skip: the October Linux
// acceptance command must provide an exact independently rebuildable helper
// request and a kernel/LSM policy admitting this precise user/mount recipe.
func requireLinuxInputTreeHelper(t *testing.T) *LinuxInputTreeHelper {
	t.Helper()
	path := os.Getenv("PHEBS_T42_INPUT_TREE_REQUEST")
	if !executionGitAbsolutePath(path) {
		t.Fatal("native prerequisite missing: PHEBS_T42_INPUT_TREE_REQUEST must name the exact clean-source helper reference request; no skip/pass")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("native prerequisite: helper request unreadable")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 16<<10))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) == 16<<10 {
		t.Fatal("native prerequisite: invalid helper request")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request ReferenceToolRequest
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		t.Fatal("native prerequisite: malformed helper request")
	}
	helper, err := ProtectLinuxInputTreeHelper(t.Context(), request)
	if err != nil {
		if helper != nil {
			_ = helper.Close()
		}
		t.Fatal("native prerequisite: exact-source sealed helper refused", err)
	}
	t.Cleanup(func() {
		if helper.Close() != nil {
			t.Error("helper cleanup refused")
		}
	})
	return helper
}

func newNativeLinuxInputTree(t *testing.T, helper *LinuxInputTreeHelper) (*LinuxExecutionInputTree, string) {
	t.Helper()
	base, err := os.MkdirTemp("", "phebs-input-tree-native-")
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "parent")
	source := filepath.Join(base, "source")
	for _, name := range []string{parent, source} {
		if os.Mkdir(name, 0o700) != nil {
			t.Fatal("mkdir")
		}
	}
	if os.WriteFile(filepath.Join(source, "data"), []byte("frozen neutral\n"), 0o600) != nil {
		t.Fatal("fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tree, err := ProtectLinuxExecutionInputTree(ctx, parent, []LinuxInputTreeSource{{Name: "source", Path: source}}, helper)
	if tree != nil {
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = tree.Close(cleanup)
			if !tree.closed {
				t.Errorf("uncertain native custody retained; do not remove %s", base)
				return
			}
			if os.RemoveAll(base) != nil {
				t.Error("native fixture cleanup refused")
			}
		})
	} else if os.RemoveAll(base) != nil {
		t.Fatal("unused native fixture cleanup refused")
	}
	if err != nil {
		t.Fatal("required native user/mount/tmpfs/read-only/capability/actual-join prerequisite refused; no skip", err)
	}
	if !tree.joined || !tree.empty {
		t.Fatal("tree published before actual helper/session join")
	}
	return tree, source
}

func TestLinuxInputTreeRequiredNativeReadonlyPersistence(t *testing.T) {
	helper := requireLinuxInputTreeHelper(t)
	tree, source := newNativeLinuxInputTree(t, helper)
	before, err := tree.Check(t.Context())
	if err != nil || before.Entries != 2 || before.Files != 1 || before.Bytes != 15 {
		t.Fatal("identity", before, err)
	}
	if os.WriteFile(filepath.Join(source, "data"), []byte("source changes\n"), 0o600) != nil || os.RemoveAll(source) != nil {
		t.Fatal("source mutation")
	}
	if err := tree.WithRoot(t.Context(), func(root *os.File) error {
		fd, err := unix.Openat(int(root.Fd()), "source/data", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		file := os.NewFile(uintptr(fd), "neutral")
		raw, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || string(raw) != "frozen neutral\n" {
			t.Fatal("protected bytes drift")
		}
		for _, operation := range []func() error{
			func() error {
				fd, err := unix.Openat(int(root.Fd()), "source/data", unix.O_WRONLY|unix.O_TRUNC|unix.O_CLOEXEC, 0)
				if err == nil {
					_ = unix.Close(fd)
				}
				return err
			},
			func() error { return unix.Mkdirat(int(root.Fd()), "new", 0o700) },
			func() error { return unix.Unlinkat(int(root.Fd()), "source/data", 0) },
			func() error { return unix.Renameat(int(root.Fd()), "source/data", int(root.Fd()), "source/moved") },
			func() error { return unix.Symlinkat("source/data", int(root.Fd()), "alias") },
			func() error { return unix.Fchmodat(int(root.Fd()), "source/data", 0o600, 0) },
		} {
			if err := operation(); !errors.Is(err, unix.EROFS) {
				t.Fatal("read-only mount mutation was not refused", err)
			}
		}
		if err := unix.MountSetattr(int(root.Fd()), "", unix.AT_EMPTY_PATH, &unix.MountAttr{Attr_clr: unix.MOUNT_ATTR_RDONLY}); !errors.Is(err, unix.EPERM) {
			t.Fatal("outside owner regained remount authority", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := tree.Check(t.Context())
	if err != nil || after != before {
		t.Fatal("post-loan identity", err)
	}
	if err := tree.Close(t.Context()); err != nil {
		t.Fatal("joined cleanup", err)
	}
	if _, err := os.Lstat(tree.stagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private placeholder retained after successful cleanup", err)
	}
}

func TestLinuxInputTreeRequiredNativeCheckedLoanAndStickyRefusal(t *testing.T) {
	helper := requireLinuxInputTreeHelper(t)
	tree, _ := newNativeLinuxInputTree(t, helper)
	callbackErr := errors.New("neutral callback error")
	if err := tree.WithRoot(t.Context(), func(*os.File) error { return callbackErr }); !errors.Is(err, callbackErr) {
		t.Fatal("callback result", err)
	}
	if _, err := tree.Check(t.Context()); err != nil {
		t.Fatal("callback error poisoned protection")
	}
	if err := tree.WithRoot(t.Context(), func(root *os.File) error { return root.Close() }); !errors.Is(err, ErrLinuxInputTree) {
		t.Fatal("closed loan was accepted", err)
	}
	if _, err := tree.Check(t.Context()); !errors.Is(err, ErrLinuxInputTree) {
		t.Fatal("refusal was not sticky")
	}
	if err := tree.Close(t.Context()); !errors.Is(err, ErrLinuxInputTree) {
		t.Fatal("sticky failure lost during cleanup", err)
	}
	if _, err := os.Lstat(tree.stagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("joined failed loan cleanup incomplete", err)
	}
	// A cancelled trusted loan also poisons that exact owner, while allowing
	// deterministic cleanup after the already joined constructor helper.
	cancelledTree, _ := newNativeLinuxInputTree(t, helper)
	ctx, cancel := context.WithCancel(t.Context())
	if err := cancelledTree.WithRoot(ctx, func(*os.File) error { cancel(); return nil }); !errors.Is(err, ErrLinuxInputTree) {
		t.Fatal("cancelled loan accepted", err)
	}
	if _, err := cancelledTree.Check(t.Context()); !errors.Is(err, ErrLinuxInputTree) {
		t.Fatal("cancelled loan refusal was not sticky")
	}
	if err := cancelledTree.Close(t.Context()); !errors.Is(err, ErrLinuxInputTree) || !cancelledTree.closed {
		t.Fatal("cancelled joined loan cleanup incomplete", err)
	}
}
