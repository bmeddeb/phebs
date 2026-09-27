//go:build !linux

package typedsandbox

import "context"

func ReadWorkerInvocation(context.Context) (WorkerInvocation, error) {
	return WorkerInvocation{}, ErrRefused
}
