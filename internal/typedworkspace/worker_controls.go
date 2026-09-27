package typedworkspace

import (
	"context"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

// WorkerControls contains authenticated transferred controller snapshots, not
// freshly checked store authority. The host must still fence every transition.
// Inventory retains its authenticated original bytes; it is never inferred from
// a directory walk.
type WorkerControls struct {
	Parent    typedindex.Admission
	Execution typedindex.Admission
	Profile   typedindex.Profile
	Inventory typedindex.Inventory
	Plan      typedindex.PackagePlan
	Allowance typedsandbox.Allowance
	Phase     typedindex.Action
}

// LoadWorkerControls accepts only the opaque executable-entry token and reads
// the fixed read-only /controls mount. No public path/scalar decoder can create
// these admissions from an untrusted request's own proposed authority.
func LoadWorkerControls(ctx context.Context, invocation typedsandbox.WorkerInvocation) (WorkerControls, error) {
	a, phase, request, seal, err := invocation.Binding(ctx)
	if err != nil {
		return WorkerControls{}, err
	}
	return loadWorkerControls(ctx, "/controls", a, phase, request, seal, workerReadOnly)
}
