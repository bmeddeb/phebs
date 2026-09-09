package t421

import "context"

type executionPreparationParentKey struct{}

// Only the native volume owner installs this private routing marker after its
// workspace identity check. It neither issues a budget nor admits a profile.
func withExecutionPreparationParent(ctx context.Context, parent string) context.Context {
	return context.WithValue(ctx, executionPreparationParentKey{}, parent)
}

func executionPreparationParent(ctx context.Context) string {
	parent, _ := ctx.Value(executionPreparationParentKey{}).(string)
	return parent
}
