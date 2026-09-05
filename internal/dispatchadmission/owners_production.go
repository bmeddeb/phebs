package dispatchadmission

import "context"

// NewProductionOwners creates optional local work coordination only for an
// already installed Phebs producer. It installs no control route and issues no
// tool/input or semantic-readiness authority. Main owns and wires this instance.
func NewProductionOwners(ctx context.Context, limits OwnerLimits) (*Owners, error) {
	runtime := productionRuntime.Load()
	if runtime == nil {
		return nil, nil
	}
	runtime.client.mu.Lock()
	required := runtime.client.ownersRequired
	valid := runtime.program == ProgramPhebs && !runtime.client.closed && runtime.client.err == nil &&
		runtime.client.Context().Err() == nil
	runtime.client.mu.Unlock()
	if !valid {
		return nil, ErrProductionBootstrap
	}
	if !required {
		return nil, nil
	}
	return NewOwners(ctx, limits)
}
