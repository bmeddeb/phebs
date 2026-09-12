package t421

import "context"

// executionProfileSystemImage is a detached prework observation, not command
// admission, a complete profile, a signing result, or an issued event ordinal.
type executionProfileSystemImage struct {
	Identity ExecutionToolIdentity
	Path     string
}

// prepareProfileSystemTools borrows an outer owner's fixed-host handles. They
// deliberately do not enter profileTools: mounted input release may precede
// future signing, which must join before that outer owner closes the signer.
// No image hash or child is added here; Check revalidates actual held metadata.
func (flow *ExecutionEpochOne) prepareProfileSystemTools(ctx context.Context, shell, signer *ExecutionSystemToolCustody) error {
	if flow == nil || ctx == nil || ctx.Err() != nil || flow.epochs == nil || flow.epochs.author == nil || shell == nil || signer == nil {
		return ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if flow.plan.Schema != PlanV3Schema || flow.closed || flow.used || flow.authored || !flow.authorStarted.IsZero() ||
		flow.profileSystemUsed || flow.workspace != nil || author.closed || author.err != nil || author.active || author.borrowedBy != nil ||
		author.next != 0 || epochs.closed || epochs.err != nil || epochs.active || epochs.released != 0 {
		return ErrExecutionEpochOne
	}
	flow.profileSystemUsed = true
	selected := [2]*ExecutionSystemToolCustody{shell, signer}
	var observed [2]executionProfileSystemImage
	for i, role := range []string{"sh", "ssh-keygen"} {
		identity, path, err := selected[i].Check(ctx, role)
		if err != nil {
			return ErrExecutionEpochOne
		}
		observed[i] = executionProfileSystemImage{Identity: identity, Path: path}
	}
	if ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	flow.profileSystemTools, flow.profileSystemImages = selected, observed
	return nil
}
