package t421

import (
	"context"
	"errors"
	"os"
)

var errPressureVolume = errors.New("execution pressure volume unavailable or retained")

// These are the existing local mutation bounds, not a Linux pressure geometry
// or permission to allocate. The caller must own and fence the actual inode,
// admit backing capacity and retain it through any partially completed failure.
func validExecutionPressureAllocation(ctx context.Context, file *os.File, before, after uint64) bool {
	return ctx != nil && ctx.Err() == nil && file != nil && before <= 80<<30 && after <= 80<<30 &&
		before%4096 == 0 && after%4096 == 0
}
