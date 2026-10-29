//go:build linux

package typedsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func TestHostPreparationTraceFailure(t *testing.T) {
	formatter := &hostFormatterFailure{class: ErrRefused, observation: HostFormatterFailure{Stage: "wait", ExitCode: -1, Signal: int(syscall.SIGKILL), FormatterContext: "deadline"}}
	live := t.Context()
	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelExpired()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		err     error
		stages  []string
		stage   string
		context string
		is      []error
	}{
		{name: "unentered", ctx: live, err: ErrRefused, stage: "unentered", context: "live", is: []error{ErrRefused}},
		{name: "custody", ctx: live, err: ErrCustody, stages: []string{"admission_lock", "capacity", "journal"}, stage: "journal", context: "live", is: []error{ErrCustody}},
		{name: "joined-custody", ctx: live, err: errors.Join(ErrCustody, fmt.Errorf("private /secret: %w", syscall.EIO)), stages: []string{"admission_lock", "mount", "scratch_finalize"}, stage: "scratch_finalize", context: "live", is: []error{ErrCustody, syscall.EIO}},
		// A live caller with an expired formatter context is the formatter's own timeout.
		{name: "formatter-timeout", ctx: live, err: formatter, stages: []string{"admission_lock", "initialize", "formatting", "mkfs"}, stage: "mkfs", context: "live", is: []error{ErrRefused}},
		{name: "allowance-deadline", ctx: expired, err: context.DeadlineExceeded, stages: []string{"admission_lock", "fallocate", "initialize"}, stage: "initialize", context: "deadline", is: []error{context.DeadlineExceeded}},
		{name: "caller-canceled", ctx: canceled, err: context.Canceled, stages: []string{"admission_lock"}, stage: "admission_lock", context: "canceled", is: []error{context.Canceled}},
		{name: "nil-context", ctx: nil, err: ErrRefused, stages: []string{"admission_lock"}, stage: "admission_lock", context: "live", is: []error{ErrRefused}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := hostPreparationTrace{started: time.Now()}
			for _, stage := range tc.stages {
				trace.enter(stage)
			}
			err := trace.failure(tc.ctx, tc.err)
			if err.Error() != tc.err.Error() {
				t.Fatal("public error text changed", err)
			}
			for _, target := range tc.is {
				if !errors.Is(err, target) {
					t.Fatal("classification lost", target)
				}
			}
			if f, ok := HostFormatterFailureOf(err); ok != (tc.err == formatter) || ok && f.FormatterContext != "deadline" {
				t.Fatal("formatter observation not forwarded", f)
			}
			d, ok := HostPreparationFailureOf(err)
			if !ok || d.Stage != tc.stage || d.Context != tc.context || d.Overflow || len(d.Stages) != len(tc.stages) {
				t.Fatal("stage record", d)
			}
			last := int64(0)
			for i, s := range d.Stages {
				if s.Name != tc.stages[i] || s.OffsetNanos < last || s.OffsetNanos > d.ElapsedNanos {
					t.Fatal("stage order or offset", d.Stages)
				}
				last = s.OffsetNanos
			}
			raw, encodeErr := json.Marshal(d)
			if encodeErr != nil || len(raw) > 2048 || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "secret") {
				t.Fatal("diagnostic leaked error text or exceeded its bound")
			}
			if len(d.Stages) > 0 {
				d.Stages[0].Name = "changed copy"
				if again, _ := HostPreparationFailureOf(err); again.Stages[0].Name == "changed copy" {
					t.Fatal("diagnostic accessor exposed shared mutable state")
				}
			}
		})
	}
	for _, err := range []error{nil, ErrRefused, formatter, errors.New("private error")} {
		if _, ok := HostPreparationFailureOf(err); ok {
			t.Fatal("unobserved stage record invented")
		}
	}
}

func TestHostPreparationTraceBound(t *testing.T) {
	trace := hostPreparationTrace{started: time.Now()}
	for i := 0; i <= hostPreparationStages; i++ {
		trace.enter(fmt.Sprintf("stage-%d", i))
	}
	d, _ := HostPreparationFailureOf(trace.failure(t.Context(), ErrCustody))
	if len(d.Stages) != hostPreparationStages || !d.Overflow || d.Stage != fmt.Sprintf("stage-%d", hostPreparationStages-1) {
		t.Fatal("trace exceeded its fixed bound", d)
	}
}

// Real unprivileged refusals carry the stage that returned them.
func TestPrepareHostScratchRefusalStage(t *testing.T) {
	o := hostFixture()
	_, err := PrepareHostScratch(t.Context(), o, nil)
	if d, ok := HostPreparationFailureOf(err); !errors.Is(err, ErrRefused) || !ok || d.Stage != "unentered" || len(d.Stages) != 0 || d.Context != "live" {
		t.Fatal("nil gate", err, d)
	}
	t.Setenv(dispatchadmission.ProductionEnvironment, "")
	_, err = PrepareHostScratch(t.Context(), o, lifecycle.NewGate(HostScratchBase))
	if d, ok := HostPreparationFailureOf(err); err == nil || !ok || d.Stage != "admission_lock" || len(d.Stages) != 1 {
		t.Fatal("selected prepare", err, d)
	}
}
