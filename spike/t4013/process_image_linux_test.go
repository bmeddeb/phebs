//go:build linux

package t4013

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxProcessImageMatchesHeldCurrentObject(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()
	if err := MatchLinuxProcessExecutableImage(t.Context(), os.Getpid(), image); err != nil {
		t.Fatal(err)
	}
	wrong, err := os.Open("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wrong.Close() }()
	if err := MatchLinuxProcessExecutableImage(t.Context(), os.Getpid(), wrong); !errors.Is(err, ErrLinuxProcessImageMatch) {
		t.Fatal("wrong inode accepted", err)
	}
}

func TestLinuxProcessImageMatchesAnonymousAndDeletedObjects(t *testing.T) {
	for _, anonymous := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted", true: "anonymous"}[anonymous], func(t *testing.T) {
			raw, err := os.ReadFile("/usr/bin/sleep")
			if err != nil {
				t.Fatal(err)
			}
			var image *os.File
			var path string
			if anonymous {
				fd, err := unix.MemfdCreate("phebs-image-match", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
				if err != nil {
					t.Fatal(err)
				}
				image = os.NewFile(uintptr(fd), "anonymous-image")
				if _, err := image.Write(raw); err != nil {
					t.Fatal(err)
				}
				if err := image.Chmod(0o500); err != nil {
					t.Fatal(err)
				}
				if _, err := unix.FcntlInt(image.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_EXEC|unix.F_SEAL_SEAL); err != nil {
					t.Fatal(err)
				}
				reader, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", image.Fd()))
				if err != nil {
					_ = image.Close()
					t.Fatal(err)
				}
				if err := image.Close(); err != nil {
					_ = reader.Close()
					t.Fatal(err)
				}
				image = reader
				path = "/proc/self/fd/3"
			} else {
				path = filepath.Join(t.TempDir(), "sleep")
				if err := os.WriteFile(path, raw, 0o700); err != nil {
					t.Fatal(err)
				}
				image, err = os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			defer func() { _ = image.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, path, "30")
			if anonymous {
				child.ExtraFiles = []*os.File{image}
			}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			defer func() {
				if !joined {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			if !anonymous {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := MatchLinuxProcessExecutableImage(ctx, child.Process.Pid, image); err != nil {
				t.Fatal(err)
			}
			if _, err := ObserveProcessExecutablePath(ctx, child.Process.Pid); err == nil {
				t.Fatal("deleted pathname observer weakened")
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			joined = true
			if err := MatchLinuxProcessExecutableImage(ctx, child.Process.Pid, image); err == nil {
				t.Fatal("exited task accepted")
			}
		})
	}
}

func TestLinuxProcessImageRefusalInputsAndExitFence(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		ctx   context.Context
		pid   int
		image *os.File
	}{
		{nil, os.Getpid(), image}, {ctx, os.Getpid(), image}, {t.Context(), 0, image}, {t.Context(), -1, image},
		{t.Context(), 1 << 32, image}, {t.Context(), 1 << 30, image}, {t.Context(), os.Getpid(), nil},
	} {
		if err := MatchLinuxProcessExecutableImage(test.ctx, test.pid, test.image); !errors.Is(err, ErrLinuxProcessImageMatch) {
			t.Fatal("invalid input accepted", err)
		}
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !linuxImageTaskLive(fd) {
		t.Fatal("live pidfd refused")
	}
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
	if linuxImageTaskLive(fd) || linuxImageTaskLive(-1) {
		t.Fatal("invalid pidfd accepted")
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
	if err := MatchLinuxProcessExecutableImage(t.Context(), os.Getpid(), image); err == nil {
		t.Fatal("closed image accepted")
	}
}
