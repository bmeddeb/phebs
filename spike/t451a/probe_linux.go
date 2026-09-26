//go:build linux

package t451a

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
	"golang.org/x/sys/unix"
)

// Probe runs only a finite, compiled-in neutral boundary test. Resource probes
// intentionally hit kernel ceilings; their nonzero terminal status is evidence,
// never a successful plan. No argument supplies an executable or shell text.
func Probe(name string) error {
	switch name {
	case "access":
		for _, name := range []string{"/inputs/unexpected-write", "/unexpected-write", "/proc/1/mem", "/proc/1/fd/1"} {
			file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE, 0600)
			if err == nil {
				_ = file.Close()
				return errors.New("forbidden write or watchdog access succeeded")
			}
		}
		if conn, err := net.DialTimeout("tcp", "192.0.2.1:80", time.Second); err == nil {
			_ = conn.Close()
			return errors.New("external network succeeded")
		}
		if fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0); err == nil {
			_ = unix.Close(fd)
			return errors.New("vsock creation succeeded")
		}
		if _, err := os.Stat("/var/run/docker.sock"); !errors.Is(err, os.ErrNotExist) {
			return errors.New("unexpected Docker socket")
		}
	case "watchdog":
		for _, signal := range []syscall.Signal{syscall.SIGSTOP, syscall.SIGKILL, syscall.SIGTERM} {
			if err := syscall.Kill(1, signal); !errors.Is(err, syscall.EPERM) {
				return errors.New("watchdog signal was not denied")
			}
		}
		if err := unix.PtraceAttach(1); !errors.Is(err, syscall.EPERM) {
			return errors.New("watchdog ptrace was not denied")
		}
	case "output":
		block := bytes.Repeat([]byte("x"), 64<<10)
		for count := 0; count <= sandbox.OutputBytes; count += len(block) {
			if _, err := os.Stdout.Write(block); err != nil {
				return err
			}
		}
		return errors.New("output ceiling not enforced")
	case "memory":
		var retained [][]byte
		for count := 0; count < sandbox.MemoryBytes+(64<<20); count += 16 << 20 {
			block := make([]byte, 16<<20)
			for offset := 0; offset < len(block); offset += 4096 {
				block[offset] = 1
			}
			retained = append(retained, block)
		}
		return fmt.Errorf("memory ceiling not enforced: %d", len(retained))
	case "descriptors":
		var files []*os.File
		defer func() {
			for _, file := range files {
				_ = file.Close()
			}
		}()
		for range sandbox.DescriptorLimit + 1 {
			file, err := os.Open("/dev/null")
			if errors.Is(err, syscall.EMFILE) {
				return nil
			}
			if err != nil {
				return err
			}
			files = append(files, file)
		}
		return errors.New("descriptor ceiling not enforced")
	case "tasks", "detached":
		var children []*exec.Cmd
		defer func() {
			for _, child := range children {
				_ = child.Process.Kill()
			}
			for _, child := range children {
				_ = child.Wait()
			}
		}()
		for range sandbox.TaskLimit + 1 {
			child := exec.Command("/inputs/t451a", "__probe_child")
			child.Env = os.Environ()
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := child.Start(); err != nil {
				if name == "tasks" && errors.Is(err, syscall.EAGAIN) {
					return nil
				}
				return err
			}
			children = append(children, child)
			if name == "detached" {
				// The controller-death test kills the host runner here. Both this
				// worker and its session-detached child must die with namespace PID1.
				fmt.Println("detached-child-running")
				time.Sleep(2 * sandbox.WallLimit)
				return errors.New("watchdog deadline not enforced")
			}
		}
		return errors.New("task ceiling not enforced")
	case "scratch-bytes":
		file, err := os.Create("/scratch/byte-probe")
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		block := bytes.Repeat([]byte("x"), 64<<10)
		for count := 0; count <= sandbox.ScratchBytes; count += len(block) {
			_, err := file.Write(block)
			if errors.Is(err, syscall.ENOSPC) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		return errors.New("scratch byte ceiling not enforced")
	case "scratch-inodes":
		if err := os.Mkdir("/scratch/inodes", 0700); err != nil {
			return err
		}
		for index := range sandbox.ScratchInodes + 1 {
			file, err := os.Create("/scratch/inodes/" + strconv.Itoa(index))
			if errors.Is(err, syscall.ENOSPC) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
		return errors.New("scratch inode ceiling not enforced")
	default:
		return errors.New("unknown probe")
	}
	_, err := io.WriteString(os.Stdout, "probe-observed\n")
	return err
}
