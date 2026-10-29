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

// A distinct root-admitted diagnostic cannot launch a container or satisfy the
// parent's unchanged two-phase acceptance oracle. Its outer Wait stays nonzero.
func acceptancePreparationOnly(c nativeAcceptanceConfig, controller *Controller, result *acceptanceChildResult) {
	if c.Schema != acceptanceHostPreparationSchema {
		return
	}
	controller.native.run = func(context.Context, typedsandbox.Options, typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		result.NativeLaunchBlocked = true
		return typedsandbox.Result{ExitCode: -1}, typedsandbox.ErrRefused
	}
}

// These bounded private observations may include the direct formatter's wait
// result. They supply neither descendant custody nor native completion authority.
type acceptanceHostPreparation struct {
	EnteredUTC    time.Time                          `json:"entered_utc"`
	ElapsedNanos  int64                              `json:"elapsed_nanos"`
	DeadlineUTC   time.Time                          `json:"deadline_utc"`
	ContextBefore string                             `json:"context_before"`
	ContextAfter  string                             `json:"context_after"`
	ReturnedError bool                               `json:"returned_error"`
	Refused       bool                               `json:"refused"`
	Custody       bool                               `json:"custody"`
	Canceled      bool                               `json:"canceled"`
	Deadline      bool                               `json:"deadline"`
	Errno         uint64                             `json:"errno"`
	Formatter     *typedsandbox.HostFormatterFailure `json:"formatter,omitempty"`
	// Preparation names the stage that refused and each entered stage's offset.
	Preparation *typedsandbox.HostPreparationFailure `json:"preparation,omitempty"`
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
		if formatter, ok := typedsandbox.HostFormatterFailureOf(err); ok {
			row.Formatter = &formatter
		}
		if preparation, ok := typedsandbox.HostPreparationFailureOf(err); ok {
			row.Preparation = &preparation
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
	// The real nil-gate refusal carries an unentered stage record.
	_, staged := typedsandbox.PrepareHostScratch(t.Context(), typedsandbox.HostScratchOptions{}, nil)
	for _, tc := range []struct {
		name    string
		err     error
		cancel  bool
		expired bool
		refused bool
		custody bool
		errno   uint64
		stage   bool
	}{
		{name: "success"},
		{name: "unknown", err: private},
		{name: "refused-errno", err: errors.Join(typedsandbox.ErrRefused, fmt.Errorf("private wrapper: %w", syscall.EPERM)), refused: true, errno: uint64(syscall.EPERM)},
		{name: "custody", err: typedsandbox.ErrCustody, custody: true},
		{name: "caller-canceled", err: context.Canceled, cancel: true},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "expired-context", err: context.DeadlineExceeded, expired: true},
		{name: "stage-trace", err: staged, refused: true, stage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			until := time.Now().Add(time.Minute)
			if tc.expired {
				until = time.Now().Add(-time.Second)
			}
			ctx, cancel := context.WithDeadline(t.Context(), until)
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
			before := "live"
			if tc.expired {
				before = "deadline"
			}
			if row.ElapsedNanos < 0 || row.EnteredUTC.IsZero() || !row.DeadlineUTC.Equal(deadline) || row.ContextBefore != before || row.Refused != tc.refused || row.Custody != tc.custody || row.Errno != tc.errno || row.ReturnedError != (tc.err != nil) || row.Canceled != errors.Is(tc.err, context.Canceled) || row.Deadline != errors.Is(tc.err, context.DeadlineExceeded) {
				t.Fatal("original error/deadline observation lost", row)
			}
			if (row.Preparation != nil) != tc.stage || tc.stage && row.Preparation.Stage != "unentered" {
				t.Fatal("stage record not forwarded", row.Preparation)
			}
			if (row.ContextAfter == "canceled") != tc.cancel {
				t.Fatal("context state changed", row)
			}
			if (row.ContextAfter == "deadline") != tc.expired {
				t.Fatal("expired context state changed", row)
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

func TestNativeAcceptanceFirstHostAllowanceObservation(t *testing.T) {
	ctx := t.Context()
	wantErr := errors.New("unchanged begin error")
	calls := 0
	controller := &Controller{native: nativeOperations{begin: func(got context.Context, planning, attempt string) (typedsandbox.Allowance, error) {
		calls++
		if got != ctx || planning != "planning" || attempt != "attempt" {
			t.Fatal("begin inputs changed")
		}
		a := typedsandbox.Allowance{Start: int64(calls), Deadline: 300}
		if calls == 1 {
			return a, wantErr
		}
		return a, nil
	}}}
	var result acceptanceChildResult
	acceptanceObserveHostPreparation(controller, &result)
	for n := 1; n <= 3; n++ {
		a, err := controller.native.begin(ctx, "planning", "attempt")
		if a.Start != int64(n) || a.Deadline != 300 || (n == 1 && err != wantErr) || (n > 1 && err != nil) {
			t.Fatal("original begin result changed", a, err)
		}
		if n == 1 && result.HostAllowance != nil || n > 1 && (result.HostAllowance == nil || result.HostAllowance.Start != 2) {
			t.Fatal("first successful allowance not retained", result.HostAllowance)
		}
	}
}

func TestNativeAcceptancePreparationOnly(t *testing.T) {
	for _, schema := range []string{acceptanceSchema, acceptanceCorpusSchema, acceptanceWorkspaceFaultSchema, acceptanceHostPreparationSchema} {
		t.Run(schema, func(t *testing.T) {
			called := false
			originalErr := errors.New("original runner")
			controller := &Controller{native: nativeOperations{run: func(context.Context, typedsandbox.Options, typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
				called = true
				return typedsandbox.Result{ExitCode: 17}, originalErr
			}}}
			var result acceptanceChildResult
			acceptancePreparationOnly(nativeAcceptanceConfig{Schema: schema}, controller, &result)
			out, err := controller.native.run(t.Context(), typedsandbox.Options{}, typedsandbox.ScratchAuthority{})
			if schema == acceptanceHostPreparationSchema {
				if called || !result.NativeLaunchBlocked || out.ExitCode != -1 || err != typedsandbox.ErrRefused || out.Removed {
					t.Fatal("diagnostic launched or manufactured native completion")
				}
			} else if !called || result.NativeLaunchBlocked || out.ExitCode != 17 || err != originalErr {
				t.Fatal("ordinary acceptance runner changed")
			}
		})
	}
}
