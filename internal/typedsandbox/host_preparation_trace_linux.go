//go:build linux

package typedsandbox

import (
	"context"
	"errors"
	"time"
)

// hostPreparationStages bounds the trace to the fixed PrepareHostScratch order.
const hostPreparationStages = 16

// HostPreparationStage is one entered PrepareHostScratch stage. OffsetNanos is
// monotonic time since the call began; it carries no path, digest or output.
type HostPreparationStage struct {
	Name        string `json:"name"`
	OffsetNanos int64  `json:"offset_nanos"`
}

// HostPreparationFailure describes where an existing preparation refusal
// occurred and how long each entered stage took. Context is the caller's
// context at return, so a live caller context with an expired formatter
// context identifies the formatter's own timeout rather than the allowance.
// It grants no custody, cleanup or retry authority.
type HostPreparationFailure struct {
	Stage        string                 `json:"stage"`
	Stages       []HostPreparationStage `json:"stages"`
	ElapsedNanos int64                  `json:"elapsed_nanos"`
	Context      string                 `json:"context"`
	Overflow     bool                   `json:"overflow,omitempty"`
}

type hostPreparationTrace struct {
	started  time.Time
	stages   [hostPreparationStages]HostPreparationStage
	n        int
	overflow bool
}

func (t *hostPreparationTrace) enter(name string) {
	if t.n == len(t.stages) {
		t.overflow = true
		return
	}
	t.stages[t.n] = HostPreparationStage{Name: name, OffsetNanos: time.Since(t.started).Nanoseconds()}
	t.n++
}

type hostPreparationFailure struct {
	err         error
	observation HostPreparationFailure
}

func (e *hostPreparationFailure) Error() string { return e.err.Error() }
func (e *hostPreparationFailure) Unwrap() error { return e.err }

// failure wraps err without changing its classification: ErrRefused,
// ErrCustody, context errors and any formatter observation still unwrap.
func (t *hostPreparationTrace) failure(ctx context.Context, err error) error {
	d := HostPreparationFailure{Stage: "unentered", Stages: append([]HostPreparationStage(nil), t.stages[:t.n]...), ElapsedNanos: time.Since(t.started).Nanoseconds(), Overflow: t.overflow}
	if t.n > 0 {
		d.Stage = t.stages[t.n-1].Name
	}
	var cause error
	if ctx != nil {
		cause = ctx.Err()
	}
	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		d.Context = "deadline"
	case errors.Is(cause, context.Canceled):
		d.Context = "canceled"
	default:
		d.Context = "live"
	}
	return &hostPreparationFailure{err: err, observation: d}
}

// HostPreparationFailureOf returns a copy of the bounded stage record.
func HostPreparationFailureOf(err error) (HostPreparationFailure, bool) {
	var failure *hostPreparationFailure
	if errors.As(err, &failure) {
		return failure.observation, true
	}
	return HostPreparationFailure{}, false
}
