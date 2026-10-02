//go:build linux

package typedsandbox

import (
	"context"
	"errors"
	"flag"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"golang.org/x/sys/unix"
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

func hostTestBase(t *testing.T, base string) HostBaseIdentity {
	t.Helper()
	f, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(int(f.Fd()), &st) != nil || unix.Fstatfs(int(f.Fd()), &fs) != nil {
		t.Fatal("stat fixture")
	}
	return HostBaseIdentity{Device: uint64(st.Dev), Inode: st.Ino, BlockSize: uint64(fs.Bsize)}
}

func TestHostBoundBaseAndSharedPressure(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	expected := hostTestBase(t, base)
	open := func(identity HostBaseIdentity) (*os.File, HostCapacity, error) {
		return hostBoundBase(t.Context(), base, identity, uint32(os.Geteuid()), uint32(os.Getegid()))
	}
	f, observed, err := open(expected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, change := range []func(*HostBaseIdentity){func(i *HostBaseIdentity) { i.Inode++ }, func(i *HostBaseIdentity) { i.Device++ }, func(i *HostBaseIdentity) { i.BlockSize *= 2 }} {
		bad := expected
		change(&bad)
		if file, _, e := open(bad); e == nil {
			_ = file.Close()
			t.Fatal("different allocation root admitted")
		}
	}
	if err = os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = hostRecheckBase(t.Context(), f, base, expected, uint32(os.Geteuid()), uint32(os.Getegid())); !errors.Is(err, ErrCustody) {
		t.Fatal("public base retained authority", err)
	}
	if err = os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(base, base+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(base); _ = os.Rename(base+"-old", base) })
	if err = os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = hostRecheckBase(t.Context(), f, base, expected, uint32(os.Geteuid()), uint32(os.Getegid())); !errors.Is(err, ErrCustody) {
		t.Fatal("replaced base admitted", err)
	}
	if entries, e := os.ReadDir(base); e != nil || len(entries) != 0 {
		t.Fatal("identity refusal grew files", entries, e)
	}
	gate := lifecycle.NewGate(base)
	// Both allocation lanes reuse this exact gate. A host refusal must preserve
	// the latch observed by the other lane, including a completely full filesystem.
	observed.TotalBytes = 1 << 40
	observed.TotalInodes = 10000
	observed.FreeInodes = 9000
	for _, used := range []int64{100, 85, 73, 95, 85, 73} {
		observed.AvailableBytes = observed.TotalBytes * uint64(100-used) / 100
		err = hostCapacity(t.Context(), observed, gate)
		if (err == nil) != (used == 73) {
			t.Fatalf("used=%d err=%v", used, err)
		}
	}
	if err = hostCapacity(t.Context(), observed, nil); err == nil {
		t.Fatal("nil pressure authority accepted")
	}
}

// Opt-in neutral host proof: no loop, mount, daemon, store or worker mutation.
// It exercises the actual fixed geometry and formatter on the backing filesystem.
var hostInitializationProof = flag.Bool("typed-host-initialization-proof", false, "run fixed native backing-image initialization proof")

func TestHostInitializedBackingImage(t *testing.T) {
	if !*hostInitializationProof {
		t.Skip("native opt-in")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required")
	}
	base := hostTestBase(t, HostScratchBase)
	o := hostFixture()
	o.Base = base
	o.MkfsDigest = "sha256:84419f91298e173cf1569b3a9c957c9f3e26a28852a6025800ed9e0cd9227ea2"
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	lock, err := hostLock(ctx, o)
	if err != nil {
		t.Fatal("lock refused")
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error("lock close", err)
		}
	}()
	entries, err := os.ReadDir(HostScratchBase)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".lock" {
		t.Fatal("custody not empty")
	}
	bound, capacity, err := hostBoundBase(ctx, HostScratchBase, base, 0, 0)
	if err != nil {
		t.Fatal("base refused")
	}
	err = hostCapacity(ctx, capacity, lifecycle.NewGate(HostScratchBase))
	err = errors.Join(err, bound.Close())
	if err != nil {
		t.Fatal("capacity refused")
	}
	path := HostScratchBase + "/.initialization-proof"
	image, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := image.Close(); err != nil {
			t.Error("close", err)
		}
		if err := os.Remove(path); err != nil {
			t.Error("remove", err)
		}
		if err := syncParent(path); err != nil {
			t.Error("sync retirement", err)
		}
	}()
	var st unix.Stat_t
	if unix.Fstat(int(image.Fd()), &st) != nil || !hostPrivateFileMetadata(st.Mode, st.Uid, st.Gid, uint64(st.Nlink)) {
		t.Fatal("private image")
	}
	j := hostOwner{Options: o, ImageDevice: uint64(st.Dev), ImageInode: st.Ino}
	observe := func(stage string) {
		if unix.Fstat(int(image.Fd()), &st) != nil || !hostImageMatches(image, j, true) {
			t.Fatal(stage, "allocation refused", st.Size, st.Blocks)
		}
		t.Logf("INITIALIZED_IMAGE stage=%s logical=%d allocated=%d", stage, st.Size, st.Blocks*512)
	}
	if unix.Fallocate(int(image.Fd()), 0, 0, hostImageBytes) != nil {
		t.Fatal("fallocate")
	}
	if err = initializeHostImage(ctx, image, hostImageBytes); err != nil {
		t.Fatal("initialize", err)
	}
	if err = image.Sync(); err != nil {
		t.Fatal("sync", err)
	}
	observe("initialized")
	if err = runHostMkfs(ctx, o, image); err != nil {
		t.Fatal("formatter", err)
	}
	if err = image.Sync(); err != nil {
		t.Fatal("sync", err)
	}
	observe("formatted")
	block := make([]byte, 4096)
	for i := range block {
		block[i] = byte(i)
	}
	for i := int64(0); i < 65536; i++ {
		if err = ctx.Err(); err != nil {
			t.Fatal(err)
		}
		offset := (i * 3001 % (hostImageBytes / 4096)) * 4096
		if n, e := image.WriteAt(block, offset); e != nil || n != len(block) {
			t.Fatal("scattered write", e)
		}
	}
	if err = image.Sync(); err != nil {
		t.Fatal("sync", err)
	}
	observe("scattered")
}

func TestHostImageIdentity(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned file metadata")
	}
	path := filepath.Join(t.TempDir(), "image")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error("file close", err)
		}
	}()
	if _, err = f.Write([]byte("image")); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil {
		t.Fatal("stat")
	}
	j := hostOwner{ImageDevice: uint64(st.Dev), ImageInode: st.Ino}
	if _, ok := hostImageIdentity(f, j); !ok {
		t.Fatal("positive identity")
	}
	for _, change := range []func(*hostOwner){func(j *hostOwner) { j.ImageDevice++ }, func(j *hostOwner) { j.ImageInode++ }} {
		bad := j
		change(&bad)
		if _, ok := hostImageIdentity(f, bad); ok {
			t.Fatal("foreign identity")
		}
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := hostImageIdentity(f, j); ok {
		t.Fatal("public file")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	alias := path + "-alias"
	if err = os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, ok := hostImageIdentity(f, j); ok {
		t.Fatal("aliased image")
	}
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(hostImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := hostImageIdentity(f, j); ok {
		t.Fatal("wrong geometry")
	}
}
