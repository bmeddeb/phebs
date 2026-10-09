//go:build linux

package typedworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestObserveCapacityUsesActualPrivateDescriptorWithoutGrowth(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	var before, after unix.Stat_t
	if err := unix.Stat(base, &before); err != nil {
		t.Fatal(err)
	}
	observed, err := ObserveCapacity(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	var actual unix.Statfs_t
	if err = unix.Statfs(base, &actual); err != nil {
		t.Fatal(err)
	}
	if err = unix.Stat(base, &after); err != nil {
		t.Fatal(err)
	}
	if observed.Device != uint64(before.Dev) || observed.Inode != before.Ino || observed.BlockSize != uint64(actual.Bsize) || observed.TotalBytes != actual.Blocks*uint64(actual.Bsize) || observed.FreeBytes != actual.Bfree*uint64(actual.Bsize) || observed.AvailableBytes != actual.Bavail*uint64(actual.Bsize) || observed.TotalInodes != actual.Files || observed.FreeInodes != actual.Ffree {
		t.Fatalf("observation differs from actual kernel state: %+v %+v", observed, actual)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || before.Ino != after.Ino || before.Dev != after.Dev || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		t.Fatal("read-only observation changed directory custody")
	}
}
func TestObserveCapacityRefusesChangedOrUntrustedRoot(t *testing.T) {
	for _, kind := range []string{"replacement", "permissions", "cancel", "invalid geometry"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(parent, "base")
			if err := os.Mkdir(base, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			got, err := observeCapacity(ctx, base, func(f *os.File) (space, error) {
				s, e := capacity(f)
				if e != nil {
					return s, e
				}
				switch kind {
				case "replacement":
					if e = os.Rename(base, base+"-old"); e != nil {
						return s, e
					}
					e = os.Mkdir(base, 0700)
				case "permissions":
					e = os.Chmod(base, 0755)
				case "cancel":
					cancel()
				case "invalid geometry":
					s.free = s.total + 1
				}
				return s, e
			})
			if err == nil || got != (CapacityObservation{}) {
				t.Fatalf("%s admitted: %+v %v", kind, got, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	parent := t.TempDir()
	target := filepath.Join(parent, "private")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveCapacity(t.Context(), link); err == nil {
		t.Fatal("symlink root observed")
	}
	//nolint:staticcheck // Deliberate malformed-context refusal regression.
	if _, err := ObserveCapacity(nil, target); err == nil {
		t.Fatal("nil context observed")
	}
}
