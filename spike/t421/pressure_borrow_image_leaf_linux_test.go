//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Same-UID kernel byte-copy control only, not a root privilege/provenance test.
func TestLinuxPressureBorrowImageSeparateSealedObject(t *testing.T) {
	source := linuxExecutorCandidateFixture(t, true, true, true, 120)
	if _, err := source.Seek(17, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	before, err := source.Stat()
	if err != nil {
		t.Fatal(err)
	}
	observation := &executionPreworkObservation{}
	image, err := copyLinuxPressureBorrowImage(t.Context(), source, uint32(os.Getuid()), uint32(os.Getgid()), observation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if image.Close() != nil {
			t.Error("image close")
		}
	})
	after, err := source.Stat()
	current, currentErr := image.file.Stat()
	offset, seekErr := source.Seek(0, io.SeekCurrent)
	if err != nil || currentErr != nil || !os.SameFile(before, after) || os.SameFile(after, current) || offset != 17 || seekErr != nil || image.check(t.Context()) != nil || observation.nativeCloseFailed.Load() {
		t.Fatal("source modified, object not independent, or target protection absent")
	}
	left, right := make([]byte, 120), make([]byte, 120)
	if _, err := source.ReadAt(left, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := image.file.ReadAt(right, 0); err != nil || !bytes.Equal(left, right) {
		t.Fatal("target bytes differ", err)
	}
	if image.digest != fmt.Sprintf("sha256:%x", sha256.Sum256(left)) {
		t.Fatal("target digest does not describe the actual borrowed source bytes")
	}
	firstClose, secondClose := image.Close(), image.Close()
	if firstClose != nil || secondClose != nil || image.check(t.Context()) == nil {
		t.Fatal("closed image reused or repeated close failed")
	}
	if _, err := source.Stat(); err != nil {
		t.Fatal("borrowed source closed", err)
	}
}

func TestLinuxPressureBorrowImageRefusals(t *testing.T) {
	for _, test := range []struct {
		name                  string
		sealed, readonly, elf bool
		size                  int64
	}{
		{"unsealed", false, true, true, 120}, {"writable", true, false, true, 120}, {"nonelf", true, true, false, 120}, {"oversize", true, true, true, maxInputCustodyFileBytes + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := linuxExecutorCandidateFixture(t, test.sealed, test.readonly, test.elf, test.size)
			if owner, err := copyLinuxPressureBorrowImage(t.Context(), source, uint32(os.Getuid()), uint32(os.Getgid()), &executionPreworkObservation{}); err == nil || owner != nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
}

func TestLinuxPressureBorrowLeafSingleFaultRolesAndOrigin(t *testing.T) {
	base := t.TempDir()
	if os.Chmod(base, 0o700) != nil {
		t.Fatal("mode")
	}
	workspace, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	leaf, err := createLinuxPressureBorrowLeaf(t.Context(), workspace, uint32(os.Getuid()), uint32(os.Getgid()), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = leaf.Close() })
	if leaf.checkLink(t.Context(), workspace) != nil {
		t.Fatal("real origin positive failed")
	}
	for _, mode := range []string{"device", "inode", "mount", "fsid", "uid", "gid", "closed", "wrong-parent", "opath", "writable"} {
		t.Run(mode, func(t *testing.T) {
			copy := *leaf
			switch mode {
			case "device":
				copy.stat.Dev++
			case "inode":
				copy.stat.Ino++
			case "mount":
				copy.mount++
			case "fsid":
				copy.fsid[0]++
			case "uid":
				copy.uid++
			case "gid":
				copy.gid++
			case "closed":
				copy.closed = true
			case "wrong-parent":
				other, err := os.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = other.Close() }()
				if copy.checkLink(t.Context(), other) == nil {
					t.Fatal("foreign origin accepted")
				}
				return
			case "opath":
				fd, err := unix.Openat(int(workspace.Fd()), linuxPressureBorrowLeafName, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err != nil {
					t.Fatal(err)
				}
				copy.file = os.NewFile(uintptr(fd), "opath-leaf")
				defer func() { _ = copy.file.Close() }()
			case "writable":
				// A writable regular descriptor cannot be a directory loan.
				file, err := os.CreateTemp(base, "wrong-role-")
				if err != nil {
					t.Fatal(err)
				}
				copy.file = file
				defer func() { _ = file.Close() }()
			}
			if copy.check(t.Context()) == nil {
				t.Fatal("single fault accepted")
			}
		})
	}
	if os.Rename(filepath.Join(base, linuxPressureBorrowLeafName), filepath.Join(base, "renamed")) != nil {
		t.Fatal("rename own test leaf")
	}
	if leaf.checkLink(t.Context(), workspace) == nil {
		t.Fatal("unlinked name accepted")
	}
}

// Actual FD/cwd mechanism under an inaccessible ancestor, in a dedicated
// same-UID test child. No protected executor/root/ext4 authority is claimed.
func TestLinuxPressureBorrowLeafDedicatedChildFDWork(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unprivileged ancestor-permission control")
	}
	base := t.TempDir()
	if os.Chmod(base, 0o700) != nil {
		t.Fatal("mode")
	}
	workspace, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	leaf, err := createLinuxPressureBorrowLeaf(t.Context(), workspace, uint32(os.Getuid()), uint32(os.Getgid()), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = leaf.Close() })
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if os.Chmod(base, 0) != nil {
		t.Fatal("hide own ancestor")
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestLinuxPressureBorrowLeafKernelHelper$")
	command.ExtraFiles = []*os.File{leaf.file}
	command.Env = []string{"PHEBS_PRESSURE_LEAF_HELPER=" + filepath.Join(base, linuxPressureBorrowLeafName), fmt.Sprintf("PHEBS_PRESSURE_LEAF_ID=%d:%d", leaf.stat.Dev, leaf.stat.Ino), "GORACE=atexit_sleep_ms=0"}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if command.Start() != nil {
		t.Fatal("start own finite child")
	}
	waitErr := command.Wait()
	if waitErr != nil || command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatalf("mechanical child Wait %v: %s", waitErr, output.String())
	}
	if os.Chmod(base, 0o700) != nil {
		t.Fatal("restore own ancestor")
	}
	after, err := os.Getwd()
	if err != nil || after != cwd || leaf.checkLink(t.Context(), workspace) != nil {
		t.Fatal("parent cwd changed or leaf origin lost")
	}
	entries, err := os.ReadDir(filepath.Join(base, linuxPressureBorrowLeafName))
	if err != nil || len(entries) != 0 {
		t.Fatal("bounded file not removed")
	}
}

func TestLinuxPressureBorrowLeafKernelHelper(t *testing.T) {
	path := os.Getenv("PHEBS_PRESSURE_LEAF_HELPER")
	if path == "" {
		return
	}
	if _, err := os.Stat(path); !os.IsPermission(err) {
		t.Fatal("absolute inaccessible ancestor was traversed", err)
	}
	unix.CloseOnExec(3)
	file := os.NewFile(3, "inherited-own-leaf")
	leaf := &linuxPressureBorrowLeaf{file: file, uid: uint32(os.Getuid()), gid: uint32(os.Getgid())}
	if leaf.capture(t.Context()) != nil {
		t.Fatal("inherited leaf protection")
	}
	if fmt.Sprintf("%d:%d", leaf.stat.Dev, leaf.stat.Ino) != os.Getenv("PHEBS_PRESSURE_LEAF_ID") {
		t.Fatal("inherited object differs")
	}
	if leaf.work(t.Context()) != nil || leaf.Close() != nil {
		t.Fatal("descriptor rooted work/close")
	}
}

func TestLinuxPressureBorrowSharedCrossModeClaim(t *testing.T) {
	// Explicit modeled shape, not protected publication/provenance. No other
	// canceled/missing guard is used to mask the actual canonical predicate.
	for _, firstPressure := range []bool{false, true} {
		tool := &LinuxExecutionToolCustody{}
		builds := &LinuxExecutionGoBuildCustody{}
		tool.executorClaim = &linuxExecutorPreworkClaim{tool: tool, builds: builds}
		var first, second *linuxExecutorPrework
		if firstPressure {
			first = newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{})
			second = newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
		} else {
			first = newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
			second = newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{})
		}
		if first.claim != tool.executorClaim || second.claim != tool.executorClaim || !first.claim.consumeOuter(first) || second.claim.consumeOuter(second) {
			t.Fatal("cross-mode shared spend missing")
		}
	}
	value := linuxPressureBorrowSelection{Schema: executionLinuxPressureSchema}
	encoded, err := encodeLinuxPressureBorrow(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLinuxPressureBorrow(encoded)
	if err != nil || decoded != value {
		t.Fatal("canonical request")
	}
	for _, raw := range []string{"", encoded + "=", strings.Repeat("A", maxExecutionSelectionCharacters+1)} {
		if _, err := decodeLinuxPressureBorrow(raw); err == nil {
			t.Fatal("unbounded/noncanonical request")
		}
	}
}

func TestLinuxPressureBorrowClaimFirstInvalidPresentationSpendsAcrossModes(t *testing.T) {
	// Direct predicate shapes only. No context, missing native owner or later
	// preparation error is used to mask a canonical/purpose identity fault.
	for _, pressure := range []bool{false, true} {
		for _, fault := range []string{"opposite-wrapper", "purpose-drift", "clone"} {
			tool, builds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
			claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
			tool.executorClaim = claim
			var original *linuxExecutorPrework
			if pressure {
				original = newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{})
			} else {
				original = newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
			}
			presented := original
			switch fault {
			case "opposite-wrapper":
				if pressure {
					presented = newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
				} else {
					presented = newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{})
				}
			case "purpose-drift":
				if pressure {
					original.pressure = nil
				} else {
					original.pressure = &linuxPressureExecutorPrework{}
				}
			case "clone":
				presented = &linuxExecutorPrework{self: original.self, claim: original.claim, builds: original.builds, tool: original.tool, pressure: original.pressure}
			}
			if original.claim != claim || presented.claim != claim || tool.executorClaim != claim || presented.claim.consumeOuter(presented) || !claim.used.Load() || original.claim.consumeOuter(original) {
				t.Fatal("first invalid mode/canonical presentation did not spend original shared claim", pressure, fault)
			}
		}
		tool, builds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
		claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
		tool.executorClaim = claim
		inner := &linuxExecutorInnerPrework{tool: tool, builds: builds}
		if pressure {
			if !claim.bindPressureInner(inner) || claim.consumeInner(inner) || !claim.used.Load() || claim.consumePressureInner(inner) {
				t.Fatal("pressure inner gained ordinary-mode reset")
			}
		} else if !claim.bindInner(inner) || claim.consumePressureInner(inner) || !claim.used.Load() || claim.consumeInner(inner) {
			t.Fatal("ordinary inner gained pressure-mode reset")
		}
	}
	tool, builds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
	claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
	inner := &linuxExecutorInnerPrework{tool: tool, builds: builds}
	if !claim.bindPressureInner(inner) || !claim.consumePressureInner(inner) {
		t.Fatal("fresh canonical pressure inner positive refused")
	}
}
