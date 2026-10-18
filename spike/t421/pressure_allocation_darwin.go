//go:build darwin

package t421

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

// Native syscalls remain cooperative. Each fixed mutation has one allocation
// (growth only), one truncate and one sync; no sparse-hole success is accepted.
func resizeExecutionPressureBallast(ctx context.Context, file *os.File, before, after uint64) error {
	if !validExecutionPressureAllocation(ctx, file, before, after) {
		return errPressureVolume
	}
	if after > before {
		allocation := &unix.Fstore_t{Flags: unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: int64(after - before)}
		if unix.FcntlFstore(file.Fd(), unix.F_PREALLOCATE, allocation) != nil || allocation.Bytesalloc != int64(after-before) {
			return errPressureVolume
		}
	}
	if ctx.Err() != nil || file.Truncate(int64(after)) != nil || file.Sync() != nil || ctx.Err() != nil {
		return errPressureVolume
	}
	return nil
}
