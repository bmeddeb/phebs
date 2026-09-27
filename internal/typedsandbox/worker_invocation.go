package typedsandbox

import (
	"context"
	"os"
)

// WorkerInvocation is a capability local to the fixed trusted helper process.
// Only ReadWorkerInvocation mints it after authenticating the actual executable
// dispatch boundary. Request/control files cannot mint it. This is transferred
// snapshot authority, never proof of current database admission.
type WorkerInvocation struct {
	allowance            Allowance
	phase, request, seal string
	pid                  int
}

// Binding exposes the immutable supervisor transfer only for an invocation
// minted in this process. A caller cannot construct one from these scalars.
func (i WorkerInvocation) Binding(ctx context.Context) (Allowance, string, string, string, error) {
	if ctx == nil || i.pid == 0 || i.pid != os.Getpid() || !i.allowance.invocation(i.phase, i.request) || !hostDigest(i.seal) {
		return Allowance{}, "", "", "", ErrRefused
	}
	if err := i.allowance.CheckLive(ctx); err != nil {
		return Allowance{}, "", "", "", err
	}
	return i.allowance, i.phase, i.request, i.seal, nil
}
func workerArgs(args []string) []string {
	if _, _, _, err := parseSupervisorArgs(args); err != nil {
		return nil
	}
	out := append([]string(nil), args...)
	out[0] = WorkerCommand
	return out
}
func parseWorkerArgs(args []string) (Allowance, string, string, string, error) {
	if len(args) != 5 || args[0] != WorkerCommand {
		return Allowance{}, "", "", "", ErrRefused
	}
	supervisor := append([]string(nil), args...)
	supervisor[0] = SupervisorCommand
	a, phase, request, err := parseSupervisorArgs(supervisor)
	if err != nil {
		return Allowance{}, "", "", "", err
	}
	return a, phase, request, args[4], nil
}
