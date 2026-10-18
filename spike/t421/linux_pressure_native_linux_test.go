//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Explicit Linux host admission only. This test is AUTHORED, NOT EXECUTED by
// the Mac preparation lane. It creates a tiny 64-MiB mechanism image but keeps
// the 120-GiB backing floor, root/capability, fixed prospective formatter and
// actual ext4/loop/teardown checks. It is not a full pressure/restore phase.
// No testing.TempDir/RemoveAll may recurse into a possibly retained mount.
func TestLinuxPressureVolumeOptionalNative(t *testing.T) {
	mode := os.Getenv("PHEBS_T42_H2M_NATIVE")
	if mode != "1" {
		t.Fatal("unknown native selection")
	}
	request := LinuxPressureRequest{Parent: os.Getenv("PHEBS_T42_H2M_PARENT"), ImageBytes: linuxPressureMinimumImage,
		FormatterSHA256: os.Getenv("PHEBS_T42_H2M_FORMATTER_SHA256")}
	if !validLinuxPressureRequest(request) {
		t.Fatal("native selection needs existing root-private parent and prospective sha256: formatter binding")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	owner, err := PrepareLinuxPressureVolume(ctx, request)
	if owner != nil {
		t.Cleanup(func() {
			if closeErr := owner.Close(); closeErr != nil {
				t.Error("safe descriptor close retained unresolved formatter custody", closeErr)
			}
		})
	}
	if err != nil {
		token, phase, _ := owner.RetainedOwner()
		t.Fatalf("native preparation stopped; retain t42h2m-%s under selected parent at phase %s: %v", token, phase, err)
	}
	before, err := owner.Geometry(ctx)
	if err != nil || !validLinuxPressureGeometry(before) {
		t.Fatal("missing actual ext4 geometry", err)
	}
	if err = owner.WithRoot(ctx, func(root *os.File) (retErr error) {
		fd, openErr := unix.Openat(int(root.Fd()), "tiny-ballast", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if openErr != nil {
			return openErr
		}
		file := os.NewFile(uintptr(fd), "tiny-pressure-mechanism-ballast")
		defer func() { retErr = errors.Join(retErr, file.Close()) }()
		if resizeErr := resizeExecutionPressureBallast(ctx, file, 0, 8192); resizeErr != nil {
			return resizeErr
		}
		return resizeExecutionPressureBallast(ctx, file, 8192, 0)
	}); err != nil {
		t.Fatal("tiny serialized allocation loan stopped; retain owned prefix", err)
	}
	after, err := owner.Geometry(ctx)
	if err != nil || !sameLinuxPressureGeometry(before, after) {
		t.Fatal("actual filesystem identity changed across tiny allocation", err)
	}
	if err = owner.Remove(ctx); err != nil {
		t.Fatal("nonforced exact teardown stopped; retain owned prefix", err)
	}
	_, phase, removed := owner.RetainedOwner()
	if !removed || phase != "image-removed" || owner.Remove(ctx) != nil {
		t.Fatal("missing exact completed removal state", phase, removed)
	}
	t.Logf("tiny owned ext4 mechanism only: nominal=%d usable=%d block=%d; nonforced umount/exact loop detach and underlay passed", before.ImageBytes, before.Blocks*before.BlockBytes, before.BlockBytes)
}
