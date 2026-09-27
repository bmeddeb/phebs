//go:build !linux

package typedsandbox

import (
	"context"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func PrepareHostScratch(context.Context, HostScratchOptions, *lifecycle.Gate) (HostScratchReceipt, error) {
	return HostScratchReceipt{}, ErrRefused
}
func VerifyHostScratch(context.Context, HostScratchOptions) (HostScratchReceipt, error) {
	return HostScratchReceipt{}, ErrRefused
}
func CleanupHostScratch(context.Context, HostScratchOptions) error { return ErrRefused }
