package typedsandbox

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestSupervisorReportRefusalNamesPredicate proves the instrumented refusal reproduces
// the historical eight-predicate OR exactly: each predicate is individually named, a
// fully-valid report yields no refusal (nothing is weakened), the first failing
// predicate wins in the historical short-circuit order, the retained hex prefix is
// bounded, and every named reason still satisfies errors.Is(err, ErrExecution) through
// errors.Join and the StageError wrapper.
func TestSupervisorReportRefusalNamesPredicate(t *testing.T) {
	baseOpts := Options{
		Control:   ControlIdentity{SealDigest: "seal-digest", Phase: ControlPlan, RequestDigest: "req-digest"},
		Allowance: Allowance{Schema: allowanceSchema, WorkerBytesUsed: 0},
	}
	good := func() supervisorReport {
		return supervisorReport{
			Schema: reportSchema, Allowance: baseOpts.Allowance,
			SealDigest: "seal-digest", Phase: ControlPlan, RequestDigest: "req-digest",
			Stdout: []byte("worker ok"),
		}
	}
	mutate := func(f func(*supervisorReport)) supervisorReport {
		r := good()
		f(&r)
		return r
	}
	// boundOpts shrinks the output bound to 2 bytes so a 3-byte worker output trips it
	// without allocating a real OutputBytes-sized buffer.
	boundOpts := baseOpts
	boundOpts.Allowance.WorkerBytesUsed = OutputBytes - 2

	for _, tc := range []struct {
		name      string
		decodeErr error
		wire      wireResult
		report    supervisorReport
		opts      Options
		want      string // "" asserts a valid report produces no refusal
		contains  []string
	}{
		{name: "all_pass", wire: wireResult{stdout: []byte("ignored")}, report: good(), opts: baseOpts, want: ""},
		{
			name: "report_decode", decodeErr: errors.New("unexpected end of JSON input"),
			wire:   wireResult{stdout: []byte(`{"schema":`), stderr: []byte("panic: boom")},
			report: supervisorReport{}, opts: baseOpts, want: "predicate=report_decode",
			contains: []string{
				`decode_error="unexpected end of JSON input"`, "stdout_len=10",
				"stdout_prefix_hex=" + hex.EncodeToString([]byte(`{"schema":`)),
				"stderr_len=11", "stderr_prefix_hex=" + hex.EncodeToString([]byte("panic: boom")),
			},
		},
		{
			name: "nonempty_stderr", wire: wireResult{stdout: []byte("x"), stderr: []byte("boom")},
			report: good(), opts: baseOpts, want: "predicate=nonempty_stderr",
			contains: []string{"stderr_len=4", "stderr_prefix_hex=" + hex.EncodeToString([]byte("boom"))},
		},
		{name: "schema_mismatch", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.Schema = "unknown-profile" }), opts: baseOpts, want: "predicate=schema_mismatch"},
		{name: "allowance_mismatch", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.Allowance.Deadline++ }), opts: baseOpts, want: "predicate=allowance_mismatch"},
		{name: "seal_digest_mismatch", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.SealDigest = "other" }), opts: baseOpts, want: "predicate=seal_digest_mismatch"},
		{name: "phase_mismatch", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.Phase = ControlExecute }), opts: baseOpts, want: "predicate=phase_mismatch"},
		{name: "request_digest_mismatch", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.RequestDigest = "other" }), opts: baseOpts, want: "predicate=request_digest_mismatch"},
		{
			name: "output_bound_exceeded", wire: wireResult{stdout: []byte("x")},
			report: mutate(func(r *supervisorReport) { r.Allowance = boundOpts.Allowance; r.Stdout = []byte("abc") }),
			opts:   boundOpts, want: "predicate=output_bound_exceeded",
			contains: []string{"worker_output_len=3", "output_bound=2"},
		},
		// Short-circuit order locks: the first failing predicate in historical OR order
		// wins. Every adjacent pair is locked, so reordering any two neighbouring
		// predicates fails exactly one case.
		{name: "decode_before_stderr", decodeErr: errors.New("bad"), wire: wireResult{stdout: []byte("x"), stderr: []byte("y")}, report: supervisorReport{}, opts: baseOpts, want: "predicate=report_decode"},
		{name: "stderr_before_schema", wire: wireResult{stdout: []byte("x"), stderr: []byte("y")}, report: mutate(func(r *supervisorReport) { r.Schema = "bad" }), opts: baseOpts, want: "predicate=nonempty_stderr"},
		{name: "schema_before_allowance", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.Schema = "bad"; r.Allowance.Deadline++ }), opts: baseOpts, want: "predicate=schema_mismatch"},
		{name: "allowance_before_seal", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.Allowance.Deadline++; r.SealDigest = "bad" }), opts: baseOpts, want: "predicate=allowance_mismatch"},
		{name: "seal_before_phase", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.SealDigest = "bad"; r.Phase = ControlExecute }), opts: baseOpts, want: "predicate=seal_digest_mismatch"},
		{name: "phase_before_request", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.Phase = ControlExecute; r.RequestDigest = "other" }), opts: baseOpts, want: "predicate=phase_mismatch"},
		{
			name: "request_before_output_bound", wire: wireResult{stdout: []byte("x")},
			report: mutate(func(r *supervisorReport) {
				r.Allowance = boundOpts.Allowance
				r.RequestDigest = "other"
				r.Stdout = []byte("abc")
			}),
			opts: boundOpts, want: "predicate=request_digest_mismatch",
		},
		{name: "schema_before_seal", wire: wireResult{stdout: []byte("x")}, report: mutate(func(r *supervisorReport) { r.Schema = "bad"; r.SealDigest = "bad" }), opts: baseOpts, want: "predicate=schema_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := supervisorReportRefusal(tc.decodeErr, tc.wire, tc.report, tc.opts)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("valid report produced a refusal: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("reason %q missing %q", got, tc.want)
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("reason %q missing %q", got, want)
				}
			}
			joined := errors.Join(ErrExecution, errors.New(got))
			if !errors.Is(joined, ErrExecution) {
				t.Fatal("errors.Join lost ErrExecution")
			}
			staged := &StageError{Stage: "supervisor_report", Cause: joined}
			if !errors.Is(staged, ErrExecution) {
				t.Fatal("StageError lost ErrExecution")
			}
			if !strings.Contains(staged.Error(), "typed-index supervisor_report:") {
				t.Fatalf("stage prefix missing: %q", staged.Error())
			}
		})
	}

	t.Run("prefix_bounded", func(t *testing.T) {
		// maxReasonBytes is a hard ceiling over the analytic worst case: two full
		// reportWirePrefixBytes hex prefixes plus the fixed keys and a bounded
		// encoding/json message. It fails if the diagnostic ever grows unbounded.
		const maxReasonBytes = 4096
		big := bytes.Repeat([]byte("A"), reportWirePrefixBytes+100)
		got := supervisorReportRefusal(errors.New("truncated"), wireResult{stdout: big, stderr: big}, supervisorReport{}, baseOpts)
		if want := "stdout_prefix_hex=" + strings.Repeat("41", reportWirePrefixBytes) + " "; !strings.Contains(got, want) {
			t.Fatal("prefix not truncated to exactly reportWirePrefixBytes")
		}
		if strings.Contains(got, strings.Repeat("41", reportWirePrefixBytes+1)) {
			t.Fatal("prefix exceeded reportWirePrefixBytes")
		}
		if len(got) > maxReasonBytes {
			t.Fatalf("reason length %d exceeded %d", len(got), maxReasonBytes)
		}
	})

	t.Run("bounded_wire_hex", func(t *testing.T) {
		if got := boundedWireHex([]byte("boom")); got != hex.EncodeToString([]byte("boom")) {
			t.Fatalf("boundedWireHex=%q", got)
		}
		if got := boundedWireHex(bytes.Repeat([]byte("A"), reportWirePrefixBytes+10)); len(got) != 2*reportWirePrefixBytes {
			t.Fatalf("boundedWireHex len=%d want=%d", len(got), 2*reportWirePrefixBytes)
		}
	})
}

// TestRunNamesSupervisorReportPredicate drives the real runChecked path through the fake
// daemon with the existing report-field faults and proves the returned error carries the
// supervisor_report stage, names the matching predicate, and still satisfies
// errors.Is(err, ErrExecution). These are the same faults TestAllowanceJoinedToken uses,
// so they are known to reach the refusal site.
func TestRunNamesSupervisorReportPredicate(t *testing.T) {
	for _, tc := range []struct{ fault, predicate string }{
		{"wrong allowance report", "allowance_mismatch"},
		{"wrong seal report", "seal_digest_mismatch"},
		{"wrong phase report", "phase_mismatch"},
		{"crossed supervisor report", "schema_mismatch"},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			_, o := fakeDaemon(t, tc.fault)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := runFake(ctx, o)
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !errors.Is(err, ErrExecution) {
				t.Fatalf("errors.Is lost ErrExecution: %v", err)
			}
			if !strings.Contains(err.Error(), "typed-index supervisor_report:") {
				t.Fatalf("wrong stage: %v", err)
			}
			if !strings.Contains(err.Error(), "supervisor_report_refusal predicate="+tc.predicate) {
				t.Fatalf("predicate not named: %v", err)
			}
		})
	}
}

func TestRunNamesInspectStoppedRefusal(t *testing.T) {
	for _, tc := range []struct {
		fault, want string
	}{
		{"reported kernel limits", "predicate=supervisor_exit stop_reason=kernel_limits"},
		{"reported resource observation", "predicate=supervisor_exit stop_reason=resource_observation sampling_stage=process_stat_read"},
		{"reported hostile sampling", "predicate=supervisor_exit stop_reason=resource_observation sampling_stage=unknown"},
		{"reported hostile stop", "predicate=supervisor_exit stop_reason=unknown"},
		{"incomplete report", "predicate=incomplete"},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			_, options := fakeDaemon(t, tc.fault)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := runFake(ctx, options)
			if !errors.Is(err, ErrExecution) || !result.Removed ||
				!strings.Contains(err.Error(), "typed-index inspect_stopped:") ||
				!strings.Contains(err.Error(), "inspect_stopped_refusal "+tc.want) ||
				strings.Contains(err.Error(), "private/path") {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}
