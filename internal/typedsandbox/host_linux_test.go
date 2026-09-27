//go:build linux

package typedsandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Safe subprocess regressions: no mount, device, root directory or daemon use.
func TestHostFormatterProcessHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_HOST_FORMATTER_TEST")
	if mode == "" {
		return
	}
	ready := os.Getenv("PHEBS_HOST_FORMATTER_READY")
	if mode == "leaf" {
		if os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
			os.Exit(41)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestHostFormatterProcessHelper$")
	child.Env = append(os.Environ(), "PHEBS_HOST_FORMATTER_TEST=leaf")
	if mode == "owner" {
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
		if runHostFormatter(child) != nil {
			os.Exit(42)
		}
		os.Exit(0)
	}
	if child.Start() != nil {
		os.Exit(43)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			os.Exit(23)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	os.Exit(44)
}

func TestHostFormatterResidualGroupRefuses(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostFormatterProcessHelper$")
	cmd.Env = append(os.Environ(), "PHEBS_HOST_FORMATTER_TEST=residual", "PHEBS_HOST_FORMATTER_READY="+ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := runHostFormatter(cmd); !errors.Is(err, ErrCustody) {
		t.Fatal("residual formatter group not retained", err)
	}
	pid := hostTestPID(t, ready)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	hostTestExited(t, pid)
}

func TestHostFormatterOwnerDeath(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	owner := exec.Command(os.Args[0], "-test.run=^TestHostFormatterProcessHelper$")
	owner.Env = append(os.Environ(), "PHEBS_HOST_FORMATTER_TEST=owner", "PHEBS_HOST_FORMATTER_READY="+ready)
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Process.Kill(); _ = owner.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	pid := hostTestPID(t, ready)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	hostTestExited(t, pid)
	// Direct-child death is only mitigation. The durable formatting phase still
	// refuses cleanup because a descendant could have escaped this process group.
	if hostCleanupPhase("formatting") {
		t.Fatal("hard-death custody reclaimed")
	}
}

func hostTestPID(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil || pid <= 1 {
		t.Fatal("invalid child pid", err)
	}
	return pid
}
func hostTestExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err == nil {
			end := strings.LastIndexByte(string(raw), ')')
			if end >= 0 && len(raw) > end+2 && (raw[end+2] == 'Z' || raw[end+2] == 'X') {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("formatter process remained live", pid)
}
