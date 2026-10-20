//go:build linux

package dispatchadmission

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxProductionWorkspaceNativeBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	binding, err := DescribeProductionWorkspace(file, path)
	if err != nil || binding.Path != path || binding.Inode == 0 || binding.FSID == ([2]int32{}) {
		t.Fatal("native binding", err)
	}
	var stat unix.Stat_t
	if unix.Fstat(int(file.Fd()), &stat) != nil || binding.Device != stat.Dev || binding.Inode != stat.Ino {
		t.Fatal("native widths or identity changed")
	}
	// Adoption restores the inherited exec boundary's intentionally cleared bit.
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := adoptProductionWorkspace(file, binding); err != nil {
		t.Fatal("inherited adoption", err)
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("adoption did not protect inheritance")
	}
	for _, mode := range []os.FileMode{0o770, 0o755, os.ModeSticky | 0o700} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := DescribeProductionWorkspace(file, path); err == nil {
			t.Fatal("wrong mode accepted", mode)
		}
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	moved := path + "-moved"
	if os.Rename(path, moved) != nil || os.Mkdir(path, 0o700) != nil {
		t.Fatal("replacement fixture")
	}
	if _, err := DescribeProductionWorkspace(file, path); err == nil {
		t.Fatal("replaced root accepted")
	}
	if _, err := adoptProductionWorkspace(file, binding); err == nil {
		t.Fatal("replaced root adopted")
	}
	if _, err := DescribeProductionWorkspace(nil, path); err == nil {
		t.Fatal("nil FD accepted")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := DescribeProductionWorkspace(file, moved); err == nil {
		t.Fatal("closed FD accepted")
	}
}

func TestLinuxProductionWorkspaceInheritedHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-3] != "t422-linux-workspace" {
		return
	}
	path, want := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	captured := captureInheritedProductionWorkspace()
	if want == "refuse" {
		if captured != nil {
			os.Exit(23)
		}
		os.Exit(0)
	}
	if captured == nil {
		os.Exit(24)
	}
	captured.Path = path
	file := os.NewFile(6, "proven-inherited-workspace")
	adopted, err := adoptProductionWorkspace(file, *captured)
	if err != nil || adopted.binding != *captured {
		os.Exit(25)
	}
	if file.Close() != nil {
		os.Exit(26)
	}
	os.Exit(0)
}
func TestLinuxProductionWorkspaceActualInheritedSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace")
	if os.Mkdir(path, 0o700) != nil {
		t.Fatal("private workspace")
	}
	directory, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = directory.Close() }()
	regular, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = regular.Close() }()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	for _, test := range []struct {
		name string
		file *os.File
		want string
	}{{"directory", directory, "accept"}, {"regular", regular, "refuse"}, {"absent", nil, "refuse"}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxProductionWorkspaceInheritedHelper$", "--", "t422-linux-workspace", path, test.want)
			command.Env = []string{"GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
			if test.file != nil {
				command.ExtraFiles = []*os.File{null, null, null, test.file}
			}
			if output, err := command.CombinedOutput(); err != nil || ctx.Err() != nil {
				t.Fatalf("fixed inherited FD6 helper: %v %s", err, output)
			}
		})
	}
}
