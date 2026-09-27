//go:build linux

package typedsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func hostObservationFixture(t *testing.T) (string, func(string) (HostObservation, error)) {
	t.Helper()
	base := t.TempDir()
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return base, func(name string) (HostObservation, error) {
		return observeHostDirectory(t.Context(), base, name, uint32(os.Geteuid()), uint32(os.Getegid()))
	}
}
func hostObservationOwner(t *testing.T, base string) (string, hostOwner) {
	t.Helper()
	o := hostFixture()
	name := filepath.Base(o.root())
	if err := os.Mkdir(filepath.Join(base, name), 0700); err != nil {
		t.Fatal(err)
	}
	j := hostOwner{Schema: hostOwnerSchema, Options: o, Phase: "new", Loop: -1}
	hostObservationWrite(t, base, name, "owner.json", j)
	return name, j
}
func hostObservationWrite(t *testing.T, base, name, control string, j hostOwner) []byte {
	t.Helper()
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, name, control), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHostObservationEmptyCapacityAndNoCreation(t *testing.T) {
	base, observe := hostObservationFixture(t)
	out, err := observe("")
	if err != nil || out.Held || out.Overflow || len(out.Names) != 0 || out.Selected != nil {
		t.Fatal(out, err)
	}
	f, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(int(f.Fd()), &st) != nil || unix.Fstatfs(int(f.Fd()), &fs) != nil {
		t.Fatal("stat")
	}
	want, err := hostObservationCapacity(st, fs)
	if err != nil || want != out.Capacity {
		t.Fatal(want, out.Capacity, err)
	}
	if err = os.Remove(filepath.Join(base, ".lock")); err != nil {
		t.Fatal(err)
	}
	if got, err := observe(""); err == nil || !got.Held {
		t.Fatal("missing lock accepted", got, err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatal("readonly observation created entry", entries, err)
	}
}
func TestHostObservationSelectedAndPendingStayUnchanged(t *testing.T) {
	base, observe := hostObservationFixture(t)
	name, j := hostObservationOwner(t, base)
	out, err := observe(name)
	if err != nil || !out.Held || out.Selected == nil || out.Selected.Options != j.Options || out.Selected.Phase != "new" {
		t.Fatal(out, err)
	}
	next := j
	next.Phase = "image"
	next.ImageInode = 1
	raw := hostObservationWrite(t, base, name, "owner.next", next)
	out, err = observe(name)
	if err != nil || !out.Held || out.Selected == nil || out.Selected.Phase != "new" {
		t.Fatal(out, err)
	}
	got, err := os.ReadFile(filepath.Join(base, name, "owner.next"))
	if err != nil || string(got) != string(raw) {
		t.Fatal("pending changed", err)
	}
	main, err := readHostOwner(filepath.Join(base, name), j.Options)
	if err != nil || main.Phase != "new" {
		t.Fatal("pending promoted", err)
	}
	// The mounted scratch root may belong to a worker and is not traversed.
	if err = os.Mkdir(filepath.Join(base, name, "scratch"), 0000); err != nil {
		t.Fatal(err)
	}
	if _, err = observe(name); err != nil {
		t.Fatal("scratch content/permissions treated as host directory", err)
	}
}
func TestHostObservationBoundsAndUnknown(t *testing.T) {
	for _, count := range []int{1, 2, 6} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			base, observe := hostObservationFixture(t)
			for i := 0; i < count; i++ {
				if err := os.WriteFile(filepath.Join(base, strings.Repeat(string(rune('a'+i)), 64)), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := observe("")
			if err != nil || !out.Held || len(out.Names) != min(count, 2) || out.Overflow != (count > 1) {
				t.Fatal(out, err)
			}
		})
	}
	base, observe := hostObservationFixture(t)
	name, _ := hostObservationOwner(t, base)
	for _, n := range []string{"x", "y", "z", "q", "w"} {
		if err := os.WriteFile(filepath.Join(base, name, n), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := observe(name); err == nil || !out.Held {
		t.Fatal("selected overflow accepted", out, err)
	}
}

func TestHostObservationReadyIsOnlyJournalData(t *testing.T) {
	base, observe := hostObservationFixture(t)
	name, j := hostObservationOwner(t, base)
	j.Phase, j.Loop, j.ImageDevice, j.ImageInode = "ready", 3, 9, 10
	j.Authority = ScratchAuthority{Source: j.Options.root() + "/scratch", DeviceMajor: 7, DeviceMinor: 3, BlockSize: 4096, Blocks: 100, Inodes: ScratchInodes, ImageBytes: hostImageBytes}
	hostObservationWrite(t, base, name, "owner.json", j)
	// Missing ready names remain held, even though the journal itself decodes.
	out, err := observe(name)
	if err != nil || !out.Held || out.Selected == nil {
		t.Fatal(out, err)
	}
	imagePath := filepath.Join(base, name, "image.ext4")
	if err = os.WriteFile(imagePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(base, name, "scratch"), 0700); err != nil {
		t.Fatal(err)
	}
	var image unix.Stat_t
	if unix.Lstat(imagePath, &image) != nil {
		t.Fatal("image stat")
	}
	j.ImageDevice, j.ImageInode = uint64(image.Dev), image.Ino
	hostObservationWrite(t, base, name, "owner.json", j)
	// These are ordinary empty fixtures: no allocation/device/mount proof exists.
	out, err = observe(name)
	if err != nil || out.Held || out.Selected == nil || out.Selected.Phase != "ready" || out.Selected.ImageInode != image.Ino {
		t.Fatal(out, err)
	}
	j.ImageInode++
	hostObservationWrite(t, base, name, "owner.json", j)
	if out, err = observe(name); err == nil || !out.Held {
		t.Fatal("wrong image identity accepted", out, err)
	}
	root, err := os.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	opened, err := hostObserveOpen(root, "proc", true)
	if opened != nil {
		_ = opened.Close()
	}
	if !errors.Is(err, unix.EXDEV) {
		t.Fatal("mount crossing did not specifically refuse", err)
	}
}
func TestHostObservationStrictIdentityAndUnsafeCustody(t *testing.T) {
	for _, kind := range []string{"legacy", "numeric", "unknown-field", "wrong-root", "missing", "partial", "oversized", "pending-mismatch", "symlink", "hardlink", "fifo", "mode", "lock-symlink", "lock-data", "unknown-child"} {
		t.Run(kind, func(t *testing.T) {
			base, observe := hostObservationFixture(t)
			name, j := hostObservationOwner(t, base)
			file := filepath.Join(base, name, "owner.json")
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "legacy":
				raw = []byte(strings.Replace(string(raw), hostOwnerSchema, "phebs-typed-host-scratch-v1", 1))
			case "numeric":
				raw = []byte(strings.Replace(string(raw), `"attempt_digest":"`+j.Options.AttemptDigest+`"`, `"attempt":1`, 1))
			case "unknown-field":
				raw = append(raw[:len(raw)-1], []byte(`,"extra":1}`)...)
			case "wrong-root":
				j.Options.AttemptDigest = "sha256:" + strings.Repeat("e", 64)
				raw, _ = json.Marshal(j)
			case "missing":
				err = os.Remove(file)
			case "partial":
				raw = []byte("{")
			case "oversized":
				raw = make([]byte, 8193)
			case "pending-mismatch":
				j.Options.AttemptDigest = "sha256:" + strings.Repeat("e", 64)
				j.Phase = "image"
				j.ImageInode = 1
				hostObservationWrite(t, base, name, "owner.next", j)
			case "symlink":
				err = os.Remove(file)
				if err == nil {
					err = os.Symlink(filepath.Join(base, ".lock"), file)
				}
			case "hardlink":
				err = os.Link(file, filepath.Join(t.TempDir(), "alias"))
			case "fifo":
				err = os.Remove(file)
				if err == nil {
					err = unix.Mkfifo(file, 0600)
				}
			case "mode":
				err = os.Chmod(file, 0644)
			case "lock-symlink":
				err = os.Remove(filepath.Join(base, ".lock"))
				if err == nil {
					err = os.Symlink(file, filepath.Join(base, ".lock"))
				}
			case "lock-data":
				err = os.WriteFile(filepath.Join(base, ".lock"), []byte("x"), 0600)
			case "unknown-child":
				err = os.WriteFile(filepath.Join(base, name, "alien"), nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains([]string{"legacy", "numeric", "unknown-field", "wrong-root", "partial", "oversized"}, kind) {
				if err = os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := observe(name)
			if err == nil || !out.Held {
				t.Fatal("unsafe custody accepted", out, err)
			}
		})
	}
}

func TestHostObservationCancellationLockAndMetadata(t *testing.T) {
	base, _ := hostObservationFixture(t)
	f, err := os.Open(filepath.Join(base, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	if unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		t.Fatal("lock")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	out, err := observeHostDirectory(ctx, base, "", uint32(os.Geteuid()), uint32(os.Getegid()))
	if !errors.Is(err, context.DeadlineExceeded) || !out.Held {
		t.Fatal(out, err)
	}
	_ = f.Close()
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, err = observeHostDirectory(ctx, base, "", uint32(os.Geteuid()), uint32(os.Getegid())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f, err = os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var original unix.Stat_t
	if unix.Fstatat(int(f.Fd()), ".lock", &original, unix.AT_SYMLINK_NOFOLLOW) != nil {
		t.Fatal("stat")
	}
	if err = os.Rename(filepath.Join(base, ".lock"), filepath.Join(base, "old")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, ".lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if hostObserveNamed(f, ".lock", original) {
		t.Fatal("inode substitution accepted")
	}
	fs := unix.Statfs_t{Bsize: 4096, Blocks: 100, Bfree: 50, Bavail: 40, Files: 100, Ffree: 50}
	for _, edit := range []func(*unix.Statfs_t){func(s *unix.Statfs_t) { s.Bsize = 0 }, func(s *unix.Statfs_t) { s.Blocks = math.MaxUint64 }, func(s *unix.Statfs_t) { s.Bavail = s.Bfree + 1 }, func(s *unix.Statfs_t) { s.Ffree = s.Files + 1 }} {
		bad := fs
		edit(&bad)
		if got, err := hostObservationCapacity(original, bad); err == nil || !reflect.DeepEqual(got, HostCapacity{}) {
			t.Fatal("bad capacity", got, err)
		}
	}
}

// Cancel at a journal boundary without a timing-dependent filesystem race.
type hostJournalCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *hostJournalCancelContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestHostObservationJournalCancellation(t *testing.T) {
	for _, at := range []int{1, 3} {
		t.Run(map[int]string{1: "main", 3: "pending"}[at], func(t *testing.T) {
			base, _ := hostObservationFixture(t)
			name, j := hostObservationOwner(t, base)
			next := j
			next.Phase, next.ImageInode = "image", 1
			hostObservationWrite(t, base, name, "owner.next", next)
			dir, err := os.Open(base)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = dir.Close() }()
			var st unix.Stat_t
			if err = unix.Fstat(int(dir.Fd()), &st); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			out, held, err := hostObserveSelected(&hostJournalCancelContext{ctx, cancel, at}, dir, name, uint32(os.Geteuid()), uint32(os.Getegid()), uint64(st.Dev))
			if !errors.Is(err, context.Canceled) || !held || out != nil {
				t.Fatal(out, held, err)
			}
		})
	}
}
