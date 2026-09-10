//go:build darwin

package custodybytes

import (
	"context"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func custodyByteFixture(t *testing.T) (borrowedRoot, context.Context) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "custody")
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	volume, err := custodyByteVolume(file)
	owner := borrowedRoot{file: file, path: path, info: info, volume: volume}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.file.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	return owner, ctx
}

func actualCustodyByteFixtureTotal(t *testing.T, path string) Sample {
	t.Helper()
	var want Sample
	err := filepath.WalkDir(path, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			want.LogicalBytes += uint64(info.Size())
		}
		want.AllocatedBytes += uint64(info.Sys().(*syscall.Stat_t).Blocks) * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return want
}

func TestCustodyByteActualLinkedPaths(t *testing.T) {
	owner, ctx := custodyByteFixture(t)
	dir := filepath.Join(owner.path, "nested")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("actual native bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(regular, filepath.Join(owner.path, "hardlink")); err != nil {
		t.Fatal(err)
	}
	sparse, err := os.OpenFile(filepath.Join(owner.path, "sparse"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = sparse.Truncate(16 << 20); err != nil {
		_ = sparse.Close()
		t.Fatal(err)
	}
	if err = sparse.Close(); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(owner.path), "outside")
	if err = os.WriteFile(outside, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(dir, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("missing", filepath.Join(owner.path, "dangling")); err != nil {
		t.Fatal(err)
	}
	want := actualCustodyByteFixtureTotal(t, owner.path)
	g := newObservation(owner)
	got, err := g.Sample(ctx, 1)
	if err != nil || got != want {
		t.Fatalf("actual=%+v metadata=%+v err=%v", got, want, err)
	}
	regularInfo, err := os.Stat(regular)
	if err != nil {
		t.Fatal(err)
	}
	if got.LogicalBytes != 16<<20+2*uint64(regularInfo.Size()) || got.AllocatedBytes >= got.LogicalBytes {
		t.Fatalf("sparse/hardlink/link units: %+v", got)
	}
	if g.Snapshot().Unavailable || !g.Snapshot().Phases[0].Completed || g.Snapshot().Phases[0].Maximum != got {
		t.Fatal("completed sample not retained")
	}
	// A smaller actual sample cannot replace the prior completed maximum.
	if err = os.Truncate(filepath.Join(owner.path, "sparse"), 0); err != nil {
		t.Fatal(err)
	}
	smaller, err := g.Sample(ctx, 1)
	if err != nil || smaller.LogicalBytes >= got.LogicalBytes || g.Snapshot().Phases[0].Maximum.LogicalBytes != got.LogicalBytes {
		t.Fatal("maximum lost", err)
	}
	phaseTwo, err := g.Sample(ctx, 2)
	if err != nil || phaseTwo.LogicalBytes != smaller.LogicalBytes || g.Snapshot().Phases[1].Maximum != phaseTwo {
		t.Fatal("actual phase attribution", err)
	}
}

func TestCustodyByteFailureRetainsCompletedMaximum(t *testing.T) {
	for _, mode := range []string{"canceled", "nil_context", "unbounded_context", "phase_backwards", "phase_zero", "phase_overflow", "root_replaced", "root_symlink", "root_mode", "closed_owner", "volume_drift"} {
		t.Run(mode, func(t *testing.T) {
			owner, ctx := custodyByteFixture(t)
			if err := os.WriteFile(filepath.Join(owner.path, "positive"), []byte("retained completed prefix"), 0o600); err != nil {
				t.Fatal(err)
			}
			g := newObservation(owner)
			prior, err := g.Sample(ctx, 2)
			if err != nil || prior.LogicalBytes == 0 {
				t.Fatal("prior", err)
			}
			phase := uint32(2)
			switch mode {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_context":
				ctx = nil
			case "unbounded_context":
				ctx = context.Background()
			case "phase_backwards":
				phase = 1
			case "phase_zero":
				phase = 0
			case "phase_overflow":
				phase = 16
			case "root_replaced", "root_symlink":
				if err = os.Rename(owner.path, owner.path+"-prior"); err != nil {
					t.Fatal(err)
				}
				if mode == "root_replaced" {
					err = os.Mkdir(owner.path, 0o700)
				} else {
					err = os.Symlink(owner.path+"-prior", owner.path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "root_mode":
				if err = os.Chmod(owner.path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "closed_owner":
				if err = owner.file.Close(); err != nil {
					t.Fatal(err)
				}
			case "volume_drift":
				g.owner.volume[0]++
			}
			if partial, err := g.Sample(ctx, phase); err == nil || partial != (Sample{}) {
				t.Fatal("failed traversal exposed partial", partial, err)
			}
			got := g.Snapshot()
			if !got.Unavailable || !got.Phases[1].Completed || got.Phases[1].Maximum != prior {
				t.Fatal("completed maximum lost", got)
			}
			fresh, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			if _, err = g.Sample(fresh, 3); err == nil || g.Snapshot() != got {
				t.Fatal("sticky refusal reset")
			}
		})
	}
}

func TestCustodyByteObservedMetadataMutation(t *testing.T) {
	owner, _ := custodyByteFixture(t)
	path := filepath.Join(owner.path, "file")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	var before, after unix.Stat_t
	if err := unix.Lstat(path, &before); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after actual mutation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lstat(path, &after); err != nil {
		t.Fatal(err)
	}
	if sameCustodyByteStat(before, after) {
		t.Fatal("observed native mutation hidden")
	}
}

// Cancel the actual caller context during the real walk, without substituting
// any filesystem observation or expected total for the native sampler.
type custodyByteCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (ctx *custodyByteCancelContext) Err() error {
	ctx.remaining--
	if ctx.remaining == 0 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestCustodyByteInterruptedWalkKeepsOnlyCompletedMaximum(t *testing.T) {
	owner, ctx := custodyByteFixture(t)
	if err := os.WriteFile(filepath.Join(owner.path, "prior"), []byte("previous completed sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newObservation(owner)
	prior, err := g.Sample(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	// More real linked entries ensure cancellation occurs after traversal
	// entry rather than merely in its initial argument checks.
	for n := 0; n < 32; n++ {
		name := filepath.Join(owner.path, string(rune('a'+n)))
		if err = os.WriteFile(name, []byte("later native bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	parent, cancel := context.WithCancel(ctx)
	defer cancel()
	interrupted := &custodyByteCancelContext{Context: parent, cancel: cancel, remaining: 12}
	if partial, err := g.Sample(interrupted, 1); err == nil || partial != (Sample{}) || parent.Err() != context.Canceled {
		t.Fatal("interrupted traversal admitted", partial, err)
	}
	if snapshot := g.Snapshot(); !snapshot.Unavailable || snapshot.Phases[0].Maximum != prior {
		t.Fatal("partial replaced prior completed maximum", snapshot)
	}
}

// Synthetic arithmetic boundary cases test the private checked accumulator,
// not a substituted native observation or a ceremony measurement.
func TestCustodyByteCheckedAccumulator(t *testing.T) {
	for _, mode := range []string{"negative_size", "negative_blocks", "device", "multiply_overflow", "logical_overflow", "allocated_overflow", "regular", "directory", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			stat := unix.Stat_t{Dev: 1, Mode: unix.S_IFREG, Size: 7, Blocks: 1}
			before := Sample{LogicalBytes: 10, AllocatedBytes: 20}
			valid := false
			switch mode {
			case "negative_size":
				stat.Size = -1
			case "negative_blocks":
				stat.Blocks = -1
			case "device":
				stat.Dev = 2
			case "multiply_overflow":
				stat.Blocks = math.MaxInt64
			case "logical_overflow":
				before.LogicalBytes = math.MaxUint64
			case "allocated_overflow":
				before.AllocatedBytes = math.MaxUint64
			case "regular":
				valid = true
			case "directory":
				valid = true
				stat.Mode = unix.S_IFDIR
			case "symlink":
				valid = true
				stat.Mode = unix.S_IFLNK
			}
			value := before
			err := addCustodyByteStat(&value, stat, 1)
			if (err == nil) != valid {
				t.Fatal("closed arithmetic", err)
			}
			if !valid {
				if value != before {
					t.Fatal("refusal changed accumulator")
				}
				return
			}
			logical := before.LogicalBytes
			if mode == "regular" {
				logical += 7
			}
			if value.LogicalBytes != logical || value.AllocatedBytes != before.AllocatedBytes+512 {
				t.Fatal("linked-entry unit", value)
			}
		})
	}
}

func TestCustodyByteUnionFilesystemRefusal(t *testing.T) {
	owner, _ := custodyByteFixture(t)
	var actual unix.Statfs_t
	if err := unix.Fstatfs(int(owner.file.Fd()), &actual); err != nil {
		t.Fatal(err)
	}
	if id, err := custodyByteFilesystem(actual); err != nil || id != owner.volume {
		t.Fatal("actual private fixture filesystem", err)
	}
	// This exercises the guard, not a claim that a union volume was mounted.
	actual.Flags |= unix.MNT_UNION
	if _, err := custodyByteFilesystem(actual); err == nil {
		t.Fatal("union directory buffering accepted")
	}
}

func TestCustodyBytePostWalkPhaseFenceRetainsPrior(t *testing.T) {
	for _, mode := range []string{"phase_changed", "caller_canceled"} {
		t.Run(mode, func(t *testing.T) {
			owner, ctx := custodyByteFixture(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			path := filepath.Join(owner.path, "actual")
			if err := os.WriteFile(path, []byte("prior"), 0o600); err != nil {
				t.Fatal(err)
			}
			g := newObservation(owner)
			prior, err := g.Sample(ctx, 2)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, []byte("larger actual completed walk in a changed phase"), 0o600); err != nil {
				t.Fatal(err)
			}
			confirmed := false
			value, err := g.SampleConfirmed(ctx, 2, func() bool {
				confirmed = true
				if mode == "caller_canceled" {
					cancel()
					return true
				}
				return false
			})
			if err == nil || !confirmed || value != (Sample{}) || g.Snapshot().Phases[1].Maximum != prior || !g.Snapshot().Unavailable {
				t.Fatal("cross-phase walk published", value, err)
			}
		})
	}
}
