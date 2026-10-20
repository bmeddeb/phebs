//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestLinuxGitCustodyRetainsUndrainedProbe(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "owned")
	if os.Mkdir(directory, 0o700) != nil {
		t.Fatal("fixture root")
	}
	stage, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	info, err := stage.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	// Existing neutral helper joins its root but leaves a real private-session
	// descendant. Cleanup must retain even an empty Git stage until that drains.
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
	command.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=exit-root", "GORACE=atexit_sleep_ms=0"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Stdin = bytes.NewReader([]byte{1})
	var output bytes.Buffer
	command.Stdout, command.Stderr, command.WaitDelay = &output, os.Stderr, time.Second
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = t4013.KillPrivateProcessSession(command.Process.Pid)
		_ = t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(6*time.Second))
	})
	if output.Len() != 8 || command.ProcessState == nil {
		t.Fatal("missing joined-root/retained-descendant fixture")
	}
	input := linuxInputFixture(t)
	sealed := requireLinuxInputCustody(t, input)
	keeper := sealed.inputs[input.Name].file
	git := &LinuxExecutionGitCustody{sealed: sealed, directory: directory, stage: stage, stageInfo: info,
		probe: &linuxExecutionToolProbe{pid: command.Process.Pid, joined: true}}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if git.Close(ctx) != ErrExecutionGitCustody || git.released {
		t.Fatal("undrained probe released scratch")
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal("undrained scratch removed", err)
	}
	if _, err := sealed.Check(t.Context(), input.Name); err != nil {
		t.Fatal("undrained source probe lost sealed custody", err)
	}
	if t4013.KillPrivateProcessSession(command.Process.Pid) != nil || t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(6*time.Second)) != nil {
		t.Fatal("owned neutral session teardown")
	}
	if git.Close(t.Context()) != ErrExecutionGitCustody || !git.released {
		t.Fatal("sticky refusal concealed completed drain/cleanup")
	}
	if git.sealed != nil || !sealed.closed {
		t.Fatal("drained probe retained sealed owner")
	}
	if _, err := keeper.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("drained probe retained keeper descriptor", err)
	}
}

func TestLinuxGitCustodyRefusesBeforeScratch(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	// A non-nil placeholder exercises only guards/failed observations. It can
	// never construct a tree or confer helper provenance.
	placeholder := &LinuxInputTreeHelper{tool: &LinuxExecutionToolCustody{}}
	for _, test := range []struct {
		name, parent, binary string
		ctx                  context.Context
		helper               *LinuxInputTreeHelper
	}{
		{"nil-context", parent, "/usr/bin/git", nil, placeholder},
		{"canceled", parent, "/usr/bin/git", canceled, placeholder},
		{"nil-helper", parent, "/usr/bin/git", t.Context(), nil},
		{"empty-helper", parent, "/usr/bin/git", t.Context(), &LinuxInputTreeHelper{}},
		{"relative-parent", "parent", "/usr/bin/git", t.Context(), placeholder},
		{"unclean-parent", parent + "/.", "/usr/bin/git", t.Context(), placeholder},
		{"relative-image", parent, "git", t.Context(), placeholder},
		{"missing-image", parent, parent + "/missing", t.Context(), placeholder},
	} {
		t.Run(test.name, func(t *testing.T) {
			git, err := ProtectLinuxExecutionGit(test.ctx, test.parent, test.binary, test.helper)
			if git != nil || err != ErrExecutionGitCustody {
				t.Fatal("invalid selection published custody", git, err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("early refusal left scratch", err)
			}
		})
	}
	git, err := ProtectLinuxExecutionGit(t.Context(), parent, "/usr/bin/true", placeholder)
	if err != ErrExecutionGitCustody || git == nil || git.identity != (ExecutionToolIdentity{}) {
		t.Fatal("wrong sealed image published identity or lost cleanup custody", err)
	}
	if git.Close(t.Context()) != ErrExecutionGitCustody || !git.released {
		t.Fatal("wrong image retained after joined rejection cleanup")
	}
	for _, git := range []*LinuxExecutionGitCustody{nil, {}} {
		if identity, err := git.Check(t.Context()); identity != (ExecutionToolIdentity{}) || err != ErrExecutionGitCustody {
			t.Fatal("absent custody published identity")
		}
		if git.WithGit(t.Context(), parent, parent, func(ExecutionToolIdentity, *os.File, []string) error { t.Fatal("absent loan"); return nil }) != ErrExecutionGitCustody {
			t.Fatal("absent custody lent root")
		}
	}
}

func TestLinuxGitCustodyAliasSourceProof(t *testing.T) {
	binary := requireLinuxExternalTool(t, "git")
	digest, err := t4013.DigestHostExecutable(t.Context(), binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"matching", "missing", "directory", "different", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range executionGitImageNames() {
				if err := os.Symlink(binary, filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(root, "git-pack-objects")
			ctx := t.Context()
			if fault == "missing" || fault == "directory" || fault == "different" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if fault == "directory" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if fault == "different" {
					if err := os.Symlink("/usr/bin/true", path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if fault == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			selections, err := executionGitSelections(ctx, root, digest)
			if fault != "matching" {
				if selections != nil || err != ErrExecutionGitCustody {
					t.Fatal("invalid source alias set admitted", err)
				}
			} else if err != nil || len(selections) != 7 {
				t.Fatal("matching alias set refused", err)
			}
		})
	}
}

func TestLinuxGitCustodyCleanupRefusesForeignEntries(t *testing.T) {
	for _, fault := range []string{"clean", "replaced-file", "extra-file", "replaced-core-symlink", "renamed-directory", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			base := t.TempDir()
			directory := filepath.Join(base, "owned")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			stage, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			info, err := stage.Stat(".")
			if err != nil {
				t.Fatal(err)
			}
			git := &LinuxExecutionGitCustody{directory: directory, stage: stage, stageInfo: info,
				aliases: make(map[string]os.FileInfo), children: make(map[string]os.FileInfo)}
			if err := stage.Mkdir("core", 0o700); err != nil {
				t.Fatal(err)
			}
			git.children["core"], err = stage.Stat("core")
			if err != nil {
				t.Fatal(err)
			}
			input := linuxInputFixture(t)
			sealed := requireLinuxInputCustody(t, input)
			if err := sealed.WithInput(t.Context(), input.Name, func(file *os.File) error { return git.copyAlias(t.Context(), "git", file) }); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			foreign := filepath.Join(directory, "core", "git")
			switch fault {
			case "replaced-file":
				if os.Rename(foreign, foreign+".old") != nil || os.WriteFile(foreign, []byte("foreign"), 0o600) != nil {
					t.Fatal("replace fixture")
				}
			case "extra-file":
				foreign = filepath.Join(directory, "core", "foreign")
				if os.WriteFile(foreign, []byte("foreign"), 0o600) != nil {
					t.Fatal("extra fixture")
				}
			case "replaced-core-symlink":
				// Preserve a matching recorded file inode under a replacement
				// directory. A per-file identity check alone must not authorize
				// traversal/unlink through a substituted intermediate symlink.
				if os.Rename(filepath.Join(directory, "core"), filepath.Join(directory, "foreign")) != nil ||
					os.Symlink("foreign", filepath.Join(directory, "core")) != nil {
					t.Fatal("replace intermediate directory")
				}
				foreign = filepath.Join(directory, "foreign", "git")
			case "renamed-directory":
				if os.Rename(directory, directory+".retained") != nil || os.Mkdir(directory, 0o700) != nil {
					t.Fatal("rename fixture")
				}
				foreign = filepath.Join(directory, "foreign")
				if os.WriteFile(foreign, []byte("foreign"), 0o600) != nil {
					t.Fatal("foreign replacement")
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err = git.Close(ctx)
			if fault == "clean" {
				if err != nil || !git.released {
					t.Fatal("exact cleanup failed", err)
				}
				if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("empty owned root survived", err)
				}
				return
			}
			if err != ErrExecutionGitCustody || git.released {
				t.Fatal("uncertain cleanup claimed release", err)
			}
			if _, err := os.Lstat(foreign); err != nil {
				t.Fatal("cleanup deleted foreign or retained entry", err)
			}
		})
	}
}
