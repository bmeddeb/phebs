//go:build !linux

package typedsandbox

import "context"

func PrepareHostScratch(context.Context, HostScratchOptions) (HostScratchReceipt, error) {
	return HostScratchReceipt{}, ErrRefused
}
func VerifyHostScratch(context.Context, HostScratchOptions) (HostScratchReceipt, error) {
	return HostScratchReceipt{}, ErrRefused
}
func CleanupHostScratch(context.Context, HostScratchOptions) error { return ErrRefused }
