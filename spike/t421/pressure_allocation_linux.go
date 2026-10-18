//go:build linux

package t421

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

// A cooperative native mutation of an already-owned, joined-use descriptor.
// Growth allocates only the delta, then every mutation truncates and syncs once.
// Failure retains the actual prefix; there is no retry or rollback. This seam
// does not admit a pressure volume or replace its capacity/settlement oracle.
func resizeExecutionPressureBallast(ctx context.Context, file *os.File, before, after uint64) error {
	if !validExecutionPressureAllocation(ctx, file, before, after) {
		return errPressureVolume
	}
	var prior unix.Stat_t
	if unix.Fstat(int(file.Fd()), &prior) != nil || prior.Mode&unix.S_IFMT != unix.S_IFREG || prior.Size != int64(before) || ctx.Err() != nil {
		return errPressureVolume
	}
	if after > before {
		if unix.Fallocate(int(file.Fd()), 0, int64(before), int64(after-before)) != nil {
			return errPressureVolume
		}
	}
	if ctx.Err() != nil || file.Truncate(int64(after)) != nil || file.Sync() != nil || ctx.Err() != nil {
		return errPressureVolume
	}
	// This aggregate block floor is not per-range hole proof. The caller must
	// retain zero-created allocation lineage and its capacity/settlement oracle.
	var current unix.Stat_t
	if unix.Fstat(int(file.Fd()), &current) != nil || current.Dev != prior.Dev || current.Ino != prior.Ino ||
		current.Mode != prior.Mode || current.Nlink != prior.Nlink || current.Uid != prior.Uid || current.Gid != prior.Gid ||
		current.Size != int64(after) || current.Blocks < 0 || uint64(current.Blocks) < after/512 || ctx.Err() != nil {
		return errPressureVolume
	}
	return nil
}
