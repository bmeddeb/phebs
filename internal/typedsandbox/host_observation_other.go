//go:build !linux

package typedsandbox

import "context"

func observeHostNamespace(context.Context, string) (HostObservation, error) {
	return HostObservation{Held: true}, ErrRefused
}
