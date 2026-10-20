//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

type goBuildImportObservation struct {
	Name       string
	MountErrno int
	Readonly   bool
	Source     goBuildImportObject
}

type goBuildImportObject struct {
	Device, Inode, Mount uint64
	Filesystem, Flags    int64
	Sentinel             string
}

type goBuildImportResult struct {
	UserNamespace, MountNamespace string
	EffectiveCapabilities         uint64
	LocalControl                  bool
	Rows                          []goBuildImportObservation
}

func observeGoBuildImportObject(fd int, sentinel string) (goBuildImportObject, error) {
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	mount, err := inputTreeMountID(fd)
	if err != nil || unix.Fstat(fd, &stat) != nil || unix.Fstatfs(fd, &filesystem) != nil {
		return goBuildImportObject{}, ErrLinuxInputTree
	}
	file, err := os.Open(fmt.Sprintf("/proc/self/fd/%d/%s", fd, sentinel))
	if err != nil {
		return goBuildImportObject{}, err
	}
	var prefix [4]byte
	_, err = io.ReadFull(file, prefix[:])
	closeErr := file.Close()
	return goBuildImportObject{Device: stat.Dev, Inode: stat.Ino, Mount: mount, Filesystem: filesystem.Type, Flags: filesystem.Flags, Sentinel: fmt.Sprintf("%x", prefix)}, errors.Join(err, closeErr)
}

func goBuildImportHostRoot() (string, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 1<<20))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) == 1<<20 {
		return "", ErrLinuxInputTree
	}
	for _, row := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(row)
		if len(fields) >= 10 && fields[4] == "/" {
			return row, nil
		}
	}
	return "", ErrLinuxInputTree
}

// This diagnostic child performs three fixed bind attempts and starts no child.
// It is a test image, not an admitted build helper or an arbitrary mount API.
func TestLinuxGoBuildNamespaceImportProbeHelper(t *testing.T) {
	if os.Getenv("PHEBS_T42_IMPORT_PROBE") != "1" {
		return
	}
	var result goBuildImportResult
	stageName := "credentials"
	fail := func(details ...error) {
		_ = json.NewEncoder(os.Stderr).Encode(struct {
			Stage        string
			Error        string
			Observations goBuildImportResult
		}{stageName, fmt.Sprint(details), result})
		os.Exit(125)
	}
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		fail()
	}
	runtime.LockOSThread() // Observe the capabilities on the mount-calling thread.
	for _, fd := range []int{3, 4, 5} {
		unix.CloseOnExec(fd)
	}
	stageName = "stage-descriptor-and-cwd"
	var stage, current unix.Stat_t
	if unix.Fstat(5, &stage) != nil || unix.Stat(".", &current) != nil || !sameLinuxInputStat(stage, current) ||
		stage.Mode != unix.S_IFDIR|0o700 || stage.Uid != 0 {
		fail()
	}
	stageName = "private-propagation"
	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		fail(err)
	}
	stageName = "calling-thread-capabilities"
	var capabilities [2]unix.CapUserData
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	if unix.Capget(&header, &capabilities[0]) != nil {
		fail()
	}
	result.EffectiveCapabilities = uint64(capabilities[0].Effective) | uint64(capabilities[1].Effective)<<32
	var err error
	stageName = "self-user-namespace"
	result.UserNamespace, err = os.Readlink("/proc/self/ns/user")
	if err != nil {
		fail()
	}
	stageName = "self-mount-namespace"
	result.MountNamespace, err = os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		fail()
	}
	// A fresh local tmpfs bind is a positive control for this exact namespace.
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"local-directory", func() error { return os.Mkdir("local", 0o700) }},
		{"local-copy-directory", func() error { return os.Mkdir("local-copy", 0o700) }},
		{"local-tmpfs-mount", func() error {
			return unix.Mount("tmpfs", "local", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "size=32768,nr_inodes=8,mode=0700")
		}},
		{"local-bind", func() error { return unix.Mount("local", "local-copy", "", unix.MS_BIND, "") }},
		{"local-write", func() error { return os.WriteFile("local-copy/control", []byte("local"), 0o600) }},
		{"local-remove", func() error { return os.Remove("local/control") }},
		{"local-copy-unmount", func() error { return unix.Unmount("local-copy", 0) }},
		{"local-unmount", func() error { return unix.Unmount("local", 0) }},
	} {
		stageName = step.name
		if err := step.run(); err != nil {
			fail(err)
		}
	}
	result.LocalControl = true
	sentinels := []string{"core/git", "source/data", "sentinel"}
	for i, name := range []string{"git", "inputs", "work"} {
		stageName = "target-directory-" + name
		if os.Mkdir(name, 0o700) != nil {
			fail()
		}
		stageName = "source-descriptor-" + name
		source, err := observeGoBuildImportObject(i+3, sentinels[i])
		if err != nil {
			fail(err)
		}
		stageName = "bind-" + name
		err = unix.Mount(fmt.Sprintf("/proc/self/fd/%d", i+3), name, "", unix.MS_BIND, "")
		row := goBuildImportObservation{Name: name, Source: source}
		result.Rows = append(result.Rows, row)
		if err != nil {
			var errno syscall.Errno
			if !errors.As(err, &errno) {
				fail()
			}
			row.MountErrno = int(errno)
		} else {
			stageName = "validate-import-" + name
			var flags unix.Statfs_t
			if unix.Statfs(name, &flags) != nil {
				fail()
			}
			row.Readonly = flags.Flags&unix.ST_RDONLY != 0
			view, err := os.Open(name)
			if err != nil {
				fail()
			}
			target, err := observeGoBuildImportObject(int(view.Fd()), sentinels[i])
			closeErr := view.Close()
			if err != nil || closeErr != nil || target.Device != source.Device || target.Inode != source.Inode ||
				target.Filesystem != source.Filesystem || target.Flags != source.Flags || target.Sentinel != source.Sentinel {
				fail()
			}
			if name == "work" {
				if os.WriteFile(filepath.Join(name, "control"), []byte("reachable work\n"), 0o600) != nil ||
					os.Remove(filepath.Join(name, "control")) != nil {
					fail()
				}
			} else if !row.Readonly || !errors.Is(os.Mkdir(filepath.Join(name, "mutation-must-refuse"), 0o700), unix.EROFS) {
				fail()
			}
			stageName = "unmount-" + name
			if unix.Unmount(name, 0) != nil { // Never force or lazily detach.
				fail()
			}
		}
		result.Rows[len(result.Rows)-1] = row
	}
	stageName = "result-output"
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		fail()
	}
	os.Exit(0)
}

func TestLinuxGoBuildRequiredNativeNamespaceImportDiagnostic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("unprivileged namespace import diagnostic required; no skip")
	}
	helper := requireLinuxInputTreeHelper(t)
	hostRoot, err := goBuildImportHostRoot()
	userNamespace, userErr := os.Readlink("/proc/self/ns/user")
	mountNamespace, mountErr := os.Readlink("/proc/self/ns/mnt")
	if err != nil || userErr != nil || mountErr != nil {
		t.Fatal("parent native observation", err, userErr, mountErr)
	}
	t.Cleanup(func() {
		after, err := goBuildImportHostRoot()
		if err != nil || after != hostRoot {
			t.Error("host root mount/propagation changed", hostRoot, after, err)
		}
	})
	base, err := os.MkdirTemp("", "phebs-linux-go-import-")
	if err != nil {
		t.Fatal(err)
	}
	parent, source, stage := filepath.Join(base, "parent"), filepath.Join(base, "source"), filepath.Join(base, "stage")
	for _, path := range []string{parent, source, stage} {
		if os.Mkdir(path, 0o700) != nil {
			t.Fatal("private fixture directory; retain", base)
		}
	}
	if os.WriteFile(filepath.Join(source, "data"), []byte("neutral import\n"), 0o600) != nil {
		t.Fatal("private fixture data; retain", base)
	}
	if os.WriteFile(filepath.Join(stage, "sentinel"), []byte("work import\n"), 0o600) != nil {
		t.Fatal("work sentinel; retain", base)
	}
	var tree *LinuxExecutionInputTree
	var git *LinuxExecutionGitCustody
	joined := true
	t.Cleanup(func() {
		if !joined {
			t.Errorf("uncertain import session; retain owners and %s", base)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if tree != nil {
			_ = tree.Close(ctx)
			if !tree.closed {
				t.Errorf("uncertain input construction; retain %s", base)
				return
			}
		}
		if git != nil {
			_ = git.Close(ctx)
			if !git.released {
				t.Errorf("uncertain Git construction; retain %s", base)
				return
			}
		}
		if os.RemoveAll(base) != nil {
			t.Error("joined import fixture cleanup failed")
		}
	})
	git, err = ProtectLinuxExecutionGit(t.Context(), parent, requireLinuxExternalTool(t, "git"), helper)
	if err != nil {
		t.Fatal("protected Git prerequisite", err)
	}
	tree, err = ProtectLinuxExecutionInputTree(t.Context(), parent, []LinuxInputTreeSource{{Name: "source", Path: source}}, helper)
	if err != nil {
		t.Fatal("protected input prerequisite", err)
	}
	work, err := os.Open(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := work.Close(); err != nil {
			t.Error("work descriptor close", err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var output string
	var expected []goBuildImportObject
	err = git.WithGit(ctx, stage, stage, func(_ ExecutionToolIdentity, gitRoot *os.File, _ []string) error {
		return tree.WithRoot(ctx, func(inputRoot *os.File) error {
			for i, root := range []*os.File{gitRoot, inputRoot, work} {
				object, err := observeGoBuildImportObject(int(root.Fd()), []string{"core/git", "source/data", "sentinel"}[i])
				if err != nil {
					return err
				}
				expected = append(expected, object)
			}
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxGoBuildNamespaceImportProbeHelper$")
			command.Dir, command.Env = stage, []string{"PHEBS_T42_IMPORT_PROBE=1", "GOMAXPROCS=2"}
			command.ExtraFiles = []*os.File{gitRoot, inputRoot, work}
			if err := prepareReferenceCommand(command); err != nil {
				return err
			}
			command.SysProcAttr = &syscall.SysProcAttr{
				Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS, Setsid: true, Pdeathsig: syscall.SIGKILL,
				UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}},
				GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}}, GidMappingsEnableSetgroups: false,
			}
			stdout, stderr := checkoutCommandOutput{remaining: 16 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 16 << 10, cancel: cancel}
			command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
			runtime.LockOSThread() // Keep Pdeathsig's launching thread alive through Wait.
			defer runtime.UnlockOSThread()
			if err := command.Start(); err != nil {
				return err
			}
			joined = false
			waitErr := command.Wait()
			members, observeErr := t4013.PrivateProcessSessionMembers(command.Process.Pid)
			joined = command.ProcessState != nil && observeErr == nil && members == 0
			t.Logf("import pid=%d root_joined=%t session_members=%d observation_error=%v", command.Process.Pid, command.ProcessState != nil, members, observeErr)
			output = stdout.buffer.String()
			if waitErr != nil || !joined || stdout.err != nil || stderr.err != nil || ctx.Err() != nil {
				return fmt.Errorf("import probe refused: %w; stderr=%q", errors.Join(waitErr, observeErr, stdout.err, stderr.err, ctx.Err()), stderr.buffer.String())
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var result goBuildImportResult
	if json.Unmarshal([]byte(output), &result) != nil || len(result.Rows) != 3 || len(expected) != 3 ||
		!result.LocalControl || result.EffectiveCapabilities&(1<<unix.CAP_SYS_ADMIN) == 0 ||
		result.UserNamespace == "" || result.UserNamespace == userNamespace || result.MountNamespace == "" || result.MountNamespace == mountNamespace {
		t.Fatal("native namespace/control observations unavailable", output)
	}
	rows := result.Rows
	if rows[0] != (goBuildImportObservation{Name: "git", MountErrno: int(unix.EINVAL), Source: expected[0]}) ||
		rows[1] != (goBuildImportObservation{Name: "inputs", MountErrno: int(unix.EINVAL), Source: expected[1]}) ||
		rows[2].Name != "work" || rows[2].Readonly || rows[2].Source != expected[2] ||
		(rows[2].MountErrno != 0 && rows[2].MountErrno != int(unix.EINVAL)) {
		t.Fatal("namespace import prediction changed; no recipe admitted", output)
	}
	t.Logf("actual bind observations: %s; protected imports refused; local control passed; inherited work FD outcome recorded; no Go build admission", output)
}
