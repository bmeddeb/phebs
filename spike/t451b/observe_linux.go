//go:build linux

package t451b

import (
	"context"
	"os"
)

// StartObservations starts the target-only same-UID /proc observer. Its stop
// function is safe to call repeatedly or concurrently and waits for closure.
func StartObservations(ctx context.Context) (func() (Observations, error), error) {
	return startObservations(ctx, os.DirFS("/proc"), uint32(os.Getpid()), uint32(os.Geteuid()))
}
