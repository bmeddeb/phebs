//go:build linux

package t451b

import (
	"context"
	"os"

	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

// StartManagedObservations uses only the already-frozen managed task ceiling.
// Sampling, worker-presence, lifetime, descriptor and sticky-refusal semantics
// are the unchanged T45.1b observer. No caller can choose a different limit.
func StartManagedObservations(ctx context.Context) (func() (Observations, error), error) {
	return startObservationsBounded(ctx, os.DirFS("/proc"), uint32(os.Getpid()), uint32(os.Geteuid()), typedsandbox.TaskLimit)
}

// ObserveManagedPrivateCache requires all cache writers to be quiescent. It
// selects only the frozen managed scratch byte ceiling; roots, inode ceiling,
// no-follow accounting and completeness predicates remain unchanged.
func ObserveManagedPrivateCache() (PrivateCacheObservation, error) {
	return observePrivateCacheBounded("/scratch", typedsandbox.ScratchBytes)
}
