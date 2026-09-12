package custodybytes

import "context"

type checkpointKey struct{}
type checkpointGuardKey struct{}

// WithCheckpoint binds an operation-owned measurement, not byte totals or
// custody authority. Only the selected caller installs it; ordinary operations
// keep their original context and perform no measurement.
func WithCheckpoint(ctx context.Context, checkpoint func(context.Context) error) context.Context {
	if ctx == nil || checkpoint == nil {
		return ctx
	}
	return context.WithValue(ctx, checkpointKey{}, checkpoint)
}

func CheckpointSelected(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	checkpoint, _ := ctx.Value(checkpointKey{}).(func(context.Context) error)
	return checkpoint != nil
}

// WithCheckpointGuard binds the actual engine owner only for the sub-operation
// during which it is alive. It is not a signal-by-PID or supplied-byte inlet.
// The ordinary path keeps its context and allocates no guard state.
func WithCheckpointGuard(ctx context.Context, guard func(context.Context, func(context.Context) error) error) context.Context {
	if !CheckpointSelected(ctx) {
		return ctx
	}
	return context.WithValue(ctx, checkpointGuardKey{}, guard)
}

func CheckpointGuard(ctx context.Context) func(context.Context, func(context.Context) error) error {
	if ctx == nil {
		return nil
	}
	guard, _ := ctx.Value(checkpointGuardKey{}).(func(context.Context, func(context.Context) error) error)
	return guard
}

// Checkpoint must run while the operation has stopped its custody mutations
// and before any transient population is removed. The owner supplies engine
// quiescence and the original deadline; this hook neither acquires nor renews
// either. Errors must propagate through the owning operation's cleanup.
func Checkpoint(ctx context.Context) error {
	if ctx == nil {
		return ErrUnavailable
	}
	checkpoint, _ := ctx.Value(checkpointKey{}).(func(context.Context) error)
	if checkpoint == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return checkpoint(ctx)
}
