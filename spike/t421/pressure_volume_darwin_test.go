//go:build darwin

package t421

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

const pressureAttachFixture = `<?xml version="1.0"?>
<plist version="1.0"><dict><key>system-entities</key><array>
<dict><key>dev-entry</key><string>/dev/disk4</string><key>potentially-mountable</key><false/></dict>
<dict><key>content-hint</key><string>41504653-0000-11AA-AA11-00306543ECAC</string><key>dev-entry</key><string>/dev/disk5s1</string><key>mount-point</key><string>/private/fixture/mount</string><key>potentially-mountable</key><true/><key>unmapped-content-hint</key><string>41504653-0000-11AA-AA11-00306543ECAC</string><key>volume-kind</key><string>apfs</string></dict>
<dict><key>content-hint</key><string>EF57347C-0000-11AA-AA11-00306543ECAC</string><key>dev-entry</key><string>/dev/disk5</string><key>potentially-mountable</key><false/><key>unmapped-content-hint</key><string>EF57347C-0000-11AA-AA11-00306543ECAC</string></dict>
</array></dict></plist>`

func TestExecutionPressureAttachment(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"native_shape", pressureAttachFixture, true},
		{"trailing_space", pressureAttachFixture + "\n\t", true},
		{"empty", "", false},
		{"oversized", strings.Repeat(" ", 64<<10) + pressureAttachFixture, false},
		{"truncated", pressureAttachFixture[:len(pressureAttachFixture)-1], false},
		{"trailing_element", pressureAttachFixture + "\n<plist/>", false},
		{"wrong_mount", strings.ReplaceAll(pressureAttachFixture, "/private/fixture/mount", "/private/elsewhere"), false},
		{"wrong_filesystem", strings.ReplaceAll(pressureAttachFixture, "<string>apfs</string>", "<string>hfs</string>"), false},
		{"wrong_partition", strings.ReplaceAll(pressureAttachFixture, "disk5s1", "disk5s2"), false},
		{"device_path", strings.ReplaceAll(pressureAttachFixture, "disk4", "disk4/../disk0"), false},
		{"missing_device_number", strings.ReplaceAll(pressureAttachFixture, "disk4", "disk"), false},
		{"duplicate_device", strings.ReplaceAll(pressureAttachFixture, "disk4", "disk5"), false},
		{"duplicate_key", strings.Replace(pressureAttachFixture, "<key>dev-entry</key>", "<key>dev-entry</key><string></string><key>dev-entry</key>", 1), false},
		{"unknown_key", strings.Replace(pressureAttachFixture, "<key>dev-entry</key>", "<key>unknown</key>", 1), false},
		{"unexpected_element", strings.Replace(pressureAttachFixture, "<array>", "<string>unexpected</string><array>", 1), false},
		{"nested_value", strings.Replace(pressureAttachFixture, "/dev/disk4</string>", "/dev/disk4<unexpected/></string>", 1), false},
		{"string_boolean", strings.Replace(pressureAttachFixture, "<true/>", "<string>true</string>", 1), false},
		{"invalid_container", strings.ReplaceAll(pressureAttachFixture, "disk5", "diskss"), false},
		{"leading_zero", strings.ReplaceAll(pressureAttachFixture, "disk5", "disk05"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			device, mounted, err := pressureAttachment([]byte(test.raw), "/private/fixture/mount")
			if (err == nil) != test.ok || test.ok && (device != "/dev/disk4" || mounted != "/dev/disk5s1") {
				t.Fatalf("attachment = %q/%q/%v", device, mounted, err)
			}
		})
	}
}

func TestExecutionPressureEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"workspace", ".fseventsd", ".Trashes", ".Spotlight-V100"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !pressureMountEmpty(root) || !pressureDirectoryEmpty(filepath.Join(root, "workspace")) {
		t.Fatal("bounded partial directory read falsely refused empty custody")
	}
	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pressureMountEmpty(root) || pressureDirectoryEmpty(root) {
		t.Fatal("unexpected custody accepted")
	}
}

func TestExecutionPressureFilesystem(t *testing.T) {
	stat := unix.Statfs_t{Bsize: 4096, Blocks: (96 << 30) / 4096, Fsid: unix.Fsid{Val: [2]int32{1, 2}}}
	copy(stat.Fstypename[:], "apfs")
	copy(stat.Mntfromname[:], "/dev/disk5s1")
	copy(stat.Mntonname[:], "/private/fixture/mount")
	for _, test := range []struct {
		name string
		edit func(*unix.Statfs_t)
		ok   bool
	}{
		{"exact", func(*unix.Statfs_t) {}, true},
		{"nominal_GPT_image", func(s *unix.Statfs_t) { s.Blocks = 102869458944 / 4096 }, false},
		{"host_filesystem", func(s *unix.Statfs_t) { s.Fsid.Val = [2]int32{9, 9} }, false},
		{"owners_ignored", func(s *unix.Statfs_t) { s.Flags |= unix.MNT_IGNORE_OWNERSHIP }, false},
		{"readonly", func(s *unix.Statfs_t) { s.Flags |= unix.MNT_RDONLY }, false},
		{"wrong_device", func(s *unix.Statfs_t) { copy(s.Mntfromname[:], "/dev/disk6s1") }, false},
		{"wrong_mount", func(s *unix.Statfs_t) { s.Mntonname[0] = 'x' }, false},
		{"wrong_block", func(s *unix.Statfs_t) { s.Bsize = 8192 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := stat
			test.edit(&value)
			if got := pressureFilesystem(value, [2]int32{9, 9}, "/dev/disk5s1", "/private/fixture/mount"); got != test.ok {
				t.Fatalf("filesystem accepted = %v", got)
			}
		})
	}
}

func TestExecutionPressureVolumeRefusesBeforeCreation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		ctx  context.Context
		path string
	}{{nil, "/"}, {ctx, "/"}, {t.Context(), "/"}, {t.Context(), "relative"}} {
		v, err := prepareExecutionPressureVolume(test.ctx, test.path)
		if v != nil || !errors.Is(err, errPressureVolume) {
			t.Fatalf("invalid input acquired custody: %v/%v", v, err)
		}
	}
}

func TestExecutionPressureBackingCapacity(t *testing.T) {
	minimum := frozenSafetyEnvelope().MinimumAvailableDiskBytes
	if minimum != 120<<30 {
		t.Fatal("frozen backing floor changed", minimum)
	}
	volume := [2]int32{1, 2}
	base := unix.Statfs_t{Bsize: 4096, Blocks: (512 << 30) / 4096, Bavail: minimum / 4096, Fsid: unix.Fsid{Val: volume}}
	for _, test := range []struct {
		name string
		edit func(*unix.Statfs_t)
		ok   bool
	}{
		{"equality", func(*unix.Statfs_t) {}, true},
		{"above", func(s *unix.Statfs_t) { s.Bavail++ }, true},
		{"below", func(s *unix.Statfs_t) { s.Bavail-- }, false},
		{"zero_available", func(s *unix.Statfs_t) { s.Bavail = 0 }, false},
		{"zero_block", func(s *unix.Statfs_t) { s.Bsize = 0 }, false},
		{"zero_total", func(s *unix.Statfs_t) { s.Blocks = 0 }, false},
		{"available_above_total", func(s *unix.Statfs_t) { s.Bavail = s.Blocks + 1 }, false},
		{"total_overflow", func(s *unix.Statfs_t) { s.Blocks = math.MaxUint64 }, false},
		{"available_overflow", func(s *unix.Statfs_t) { s.Blocks, s.Bavail = math.MaxUint64, math.MaxUint64 }, false},
		{"wrong_volume", func(s *unix.Statfs_t) { s.Fsid.Val = [2]int32{3, 4} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			stat := base
			test.edit(&stat)
			if got := pressureBackingCapacity(stat, volume); got != test.ok {
				t.Fatal("backing capacity admitted", got)
			}
		})
	}
	if pressureBackingCapacity(unix.Statfs_t{}, [2]int32{}) {
		t.Fatal("unbound backing admitted")
	}
}

// Source-order regression: the below-floor branch must return before any
// lock file, tool session or image/mount creation. No native mount is needed
// to test refusal placement, including on a host with plenty of free space.
func TestExecutionPressureBackingPrecedesMutation(t *testing.T) {
	raw, err := os.ReadFile("pressure_volume_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, "func prepareExecutionPressureVolume(")
	if start < 0 {
		t.Fatal("preparation function unavailable")
	}
	end := strings.Index(text[start:], "func pressureBackingCapacity(")
	if end < 0 {
		t.Fatal("preparation function unavailable")
	}
	text = text[start : start+end]
	previous := -1
	for _, needle := range []string{"v.parent, err = openProductionRoot(parent)", "unix.Fstatfs(int(v.parent.file.Fd()), &backing)",
		"!pressureBackingCapacity(backing, v.parent.volume)", "return v, errPressureVolume", "v.lock, err = t4013.LockRunRoot(parent)",
		"v.tool, err = HoldExecutionSystemTool", "directory, err := os.MkdirTemp", "v.command(ctx, \"create\"", "v.command(ctx, \"attach\""} {
		index := strings.Index(text[previous+1:], needle)
		if index < 0 {
			t.Fatal("backing refusal must precede mutation", needle)
		}
		previous += index + 1
	}
}

func TestExecutionPressureCloseRetainsUnsettledLock(t *testing.T) {
	session, err := unix.Getsid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		pid  int
	}{{"unavailable", 0}, {"live", session}} {
		t.Run(test.name, func(t *testing.T) {
			parent, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			lock, err := t4013.LockRunRoot(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Close() }() // Only this fixture's supplied lock.
			v := &executionPressureVolume{lock: lock, sessions: []int{test.pid}}
			if v.Close() == nil || v.closed || !v.unsettled {
				t.Fatal("unsettled session released custody")
			}
			if contender, err := t4013.LockRunRoot(parent); err == nil {
				_ = contender.Close()
				t.Fatal("unsettled close released mutation lock")
			}
		})
	}
}

// Explicitly selected empty-volume gate only. A failed run retains its root
// outside testing.TempDir: testing cleanup must never recurse into a mount.
func TestExecutionPressureVolumeOptionalNative(t *testing.T) {
	if os.Getenv("PHEBS_T422_EMPTY_PRESSURE_VOLUME") != "1" {
		t.Skip("requires explicit empty native APFS custody gate; no ballast or corpus")
	}
	requireExternalToolFrozenHost(t)
	parent, err := os.MkdirTemp("", "t422-empty-pressure-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	v, err := prepareExecutionPressureVolume(ctx, parent)
	if v != nil {
		defer func() { _ = v.Close() }()
	}
	if err != nil {
		t.Fatalf("retained empty-volume preparation %s: %v", parent, err)
	}
	t.Logf("owned APFS workspace %s; exact bytes=%d; owners enabled; recorded sessions=%v", v.workspace.path, uint64(96<<30), v.sessions)
	// Model the sticky post-Close refusal on otherwise valid native custody.
	// Clearing this supplied fixture bit is not an operator recovery API.
	v.unsettled = true
	if v.removeEmpty(ctx) == nil || !v.ready || len(v.sessions) != 2 {
		t.Fatal("sticky unsettled state reached detach")
	}
	v.unsettled = false
	if contender, err := t4013.LockRunRoot(parent); err == nil {
		_ = contender.Close()
		t.Fatal("volume did not hold shared mutation lock")
	}
	probe := filepath.Join(v.workspace.path, "probe")
	if err := os.WriteFile(probe, []byte("owned volume\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(probe); err != nil || string(raw) != "owned volume\n" {
		t.Fatalf("probe read: %q/%v", raw, err)
	}
	if v.removeEmpty(ctx) == nil || !v.ready || len(v.sessions) != 2 {
		t.Fatal("populated workspace reached detach")
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	if err := v.removeEmpty(ctx); err != nil {
		t.Fatalf("retained empty-volume teardown %s: %v", parent, err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if len(v.sessions) != 3 || !v.removed {
		t.Fatal("missing finite native command joins/removal")
	}
	if _, err := os.Lstat(v.root.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("removed root remains", err)
	}
	if err := os.Remove(filepath.Join(parent, ".t4013-operation.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	t.Log("non-forced detach, recorded-session zero and exact image/root absence passed")
}
