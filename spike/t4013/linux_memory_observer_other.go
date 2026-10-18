//go:build !linux

package t4013

import "context"

// ObserveLinuxMemory has no non-Linux native implementation.
func ObserveLinuxMemory(context.Context) (LinuxMemoryObservation, error) {
	return LinuxMemoryObservation{}, ErrLinuxMemoryObservation
}
