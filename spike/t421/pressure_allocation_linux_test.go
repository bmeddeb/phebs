//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A tiny actual allocation, not an isolated pressure/geometry/restore receipt.
// A Linux filesystem that cannot provide the primitive fails rather than skips.
func TestExecutionPressureAllocationLinuxNative(t *testing.T) {
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "ballast"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var before uint64
	for _, after := range []uint64{4096, 8192, 4096, 0} {
		if err := resizeExecutionPressureBallast(ctx, file, before, after); err != nil {
			t.Fatalf("resize %d to %d: %v", before, after, err)
		}
		var stat unix.Stat_t
		if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Size != int64(after) || stat.Blocks < 0 || uint64(stat.Blocks) < after/512 {
			t.Fatal("wrong native size or aggregate allocated-block floor", stat.Size, stat.Blocks)
		}
		before = after
	}
	if err := resizeExecutionPressureBallast(ctx, file, 4096, 8192); !errors.Is(err, errPressureVolume) {
		t.Fatal("wrong initial size admitted", err)
	}
	info, err := file.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("initial-size refusal changed the file", err)
	}
}
