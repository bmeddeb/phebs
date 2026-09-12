package custodybytes

import (
	"context"
	"errors"
	"testing"
)

func TestCheckpointContext(t *testing.T) {
	base := t.Context()
	if WithCheckpoint(base, nil) != base || Checkpoint(base) != nil {
		t.Fatal("absent observer changed ordinary context")
	}
	if CheckpointSelected(base) || WithCheckpoint(nil, func(context.Context) error { return nil }) != nil { //nolint:staticcheck // Exercise explicit nil-context refusal.
		t.Fatal("absent context selected measurement")
	}
	want := errors.New("measurement refused")
	calls := 0
	bound := WithCheckpoint(base, func(ctx context.Context) error {
		calls++
		if ctx.Value(checkpointKey{}) == nil {
			t.Fatal("checkpoint lost owning context")
		}
		return want
	})
	if err := Checkpoint(bound); !errors.Is(err, want) || calls != 1 {
		t.Fatal("measurement error lost", calls, err)
	}
	canceled, cancel := context.WithCancel(bound)
	cancel()
	if err := Checkpoint(canceled); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("canceled checkpoint invoked measurement", calls, err)
	}
	if !errors.Is(Checkpoint(nil), ErrUnavailable) { //nolint:staticcheck // Exercise explicit nil-context refusal.
		t.Fatal("nil context admitted")
	}
	guardCalls := 0
	guard := func(ctx context.Context, measure func(context.Context) error) error {
		guardCalls++
		return measure(ctx)
	}
	if WithCheckpointGuard(base, guard) != base || CheckpointGuard(base) != nil {
		t.Fatal("ordinary context acquired guard")
	}
	guarded := WithCheckpointGuard(bound, guard)
	if !CheckpointSelected(guarded) || CheckpointGuard(bound) != nil || CheckpointGuard(guarded) == nil {
		t.Fatal("guard escaped its sub-operation")
	}
	if err := CheckpointGuard(guarded)(guarded, func(context.Context) error { return want }); !errors.Is(err, want) || guardCalls != 1 {
		t.Fatal("owned guard error lost", guardCalls, err)
	}
}
