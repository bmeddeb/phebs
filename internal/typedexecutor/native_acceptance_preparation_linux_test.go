//go:build linux

package typedexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

// These bounded private test observations are not formatter Wait, descendant
// custody or native completion evidence. Keep those unavailable facts explicit.
type acceptanceHostPreparation struct {
	EnteredUTC    time.Time `json:"entered_utc"`
	ElapsedNanos  int64     `json:"elapsed_nanos"`
	DeadlineUTC   time.Time `json:"deadline_utc"`
	ContextBefore string    `json:"context_before"`
	ContextAfter  string    `json:"context_after"`
	ReturnedError bool      `json:"returned_error"`
	Refused       bool      `json:"refused"`
	Custody       bool      `json:"custody"`
	Canceled      bool      `json:"canceled"`
	Deadline      bool      `json:"deadline"`
	Errno         uint64    `json:"errno"`
}

func acceptanceContextState(ctx context.Context) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "deadline"
	case errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	case ctx.Err() == nil:
		return "live"
	default:
		return "other"
	}
}

func acceptanceObserveHostPreparation(c *Controller, result *acceptanceChildResult) {
	begin, prepare := c.native.begin, c.native.prepare
	c.native.begin = func(ctx context.Context, planning, attempt string) (typedsandbox.Allowance, error) {
		allowance, err := begin(ctx, planning, attempt)
		if err == nil && result.HostAllowance == nil {
			copy := allowance
			result.HostAllowance = &copy
		}
		return allowance, err
	}
	c.native.prepare = func(ctx context.Context, options typedsandbox.HostScratchOptions, gate *lifecycle.Gate) (typedsandbox.HostScratchReceipt, error) {
		started := time.Now()
		row := acceptanceHostPreparation{EnteredUTC: started.UTC(), ContextBefore: acceptanceContextState(ctx)}
		if deadline, ok := ctx.Deadline(); ok {
			row.DeadlineUTC = deadline.UTC()
		}
		receipt, err := prepare(ctx, options, gate)
		row.ElapsedNanos, row.ContextAfter = time.Since(started).Nanoseconds(), acceptanceContextState(ctx)
		row.ReturnedError = err != nil
		row.Refused, row.Custody = errors.Is(err, typedsandbox.ErrRefused), errors.Is(err, typedsandbox.ErrCustody)
		row.Canceled, row.Deadline = errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded)
		var errno syscall.Errno
		if errors.As(err, &errno) {
			row.Errno = uint64(errno)
		}
		if len(result.HostPreparation) < 2 {
			result.HostPreparation = append(result.HostPreparation, row)
		} else {
			result.HostPreparationOverflow = true
		}
		return receipt, err
	}
}

func TestNativeAcceptanceHostPreparationObservation(t *testing.T) {
	private := errors.New("private /secret/fixture must not enter a diagnostic")
	for _, tc := range []struct {
		name    string
		err     error
		cancel  bool
		refused bool
		custody bool
		errno   uint64
	}{
		{name: "success"},
		{name: "unknown", err: private},
		{name: "refused-errno", err: errors.Join(typedsandbox.ErrRefused, fmt.Errorf("private wrapper: %w", syscall.EPERM)), refused: true, errno: uint64(syscall.EPERM)},
		{name: "custody", err: typedsandbox.ErrCustody, custody: true},
		{name: "caller-canceled", err: context.Canceled, cancel: true},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Minute))
			defer cancel()
			deadline, _ := ctx.Deadline()
			options := typedsandbox.HostScratchOptions{RequestDigest: "planning", AttemptDigest: "attempt", Socket: "/private/socket"}
			gate := lifecycle.NewGate("/private/gate")
			want := typedsandbox.HostScratchReceipt{Schema: "unchanged", Options: options}
			allowance := typedsandbox.Allowance{Start: 7, Deadline: 11}
			calls, begins := 0, 0
			controller := &Controller{native: nativeOperations{
				begin: func(got context.Context, planning, attempt string) (typedsandbox.Allowance, error) {
					begins++
					if got != ctx || planning != "planning" || attempt != "attempt" {
						t.Fatal("begin inputs changed")
					}
					return allowance, tc.err
				},
				prepare: func(got context.Context, o typedsandbox.HostScratchOptions, g *lifecycle.Gate) (typedsandbox.HostScratchReceipt, error) {
					calls++
					d, _ := got.Deadline()
					if got != ctx || d != deadline || o != options || g != gate {
						t.Fatal("preparation context, deadline, options or gate changed")
					}
					if tc.cancel {
						cancel()
					}
					return want, tc.err
				},
			}}
			var result acceptanceChildResult
			acceptanceObserveHostPreparation(controller, &result)
			if a, err := controller.native.begin(ctx, "planning", "attempt"); a != allowance || err != tc.err || begins != 1 {
				t.Fatal("begin result changed")
			}
			if (result.HostAllowance != nil) != (tc.err == nil) || result.HostAllowance != nil && *result.HostAllowance != allowance {
				t.Fatal("allowance observation changed")
			}
			if got, err := controller.native.prepare(ctx, options, gate); got != want || err != tc.err || calls != 1 {
				t.Fatal("original preparation result changed")
			}
			row := result.HostPreparation[0]
			if row.ElapsedNanos < 0 || row.EnteredUTC.IsZero() || !row.DeadlineUTC.Equal(deadline) || row.ContextBefore != "live" || row.Refused != tc.refused || row.Custody != tc.custody || row.Errno != tc.errno || row.ReturnedError != (tc.err != nil) || row.Canceled != errors.Is(tc.err, context.Canceled) || row.Deadline != errors.Is(tc.err, context.DeadlineExceeded) {
				t.Fatal("original error/deadline observation lost", row)
			}
			if (row.ContextAfter == "canceled") != tc.cancel {
				t.Fatal("context state changed", row)
			}
			for range 2 {
				_, _ = controller.native.prepare(ctx, options, gate)
			}
			if calls != 3 || len(result.HostPreparation) != 2 || !result.HostPreparationOverflow {
				t.Fatal("diagnostic bound changed original calls")
			}
			raw, err := json.Marshal(result)
			if err != nil || len(raw) > 8192 || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "secret") {
				t.Fatal("private error/path leaked or observation overflowed")
			}
		})
	}
}
