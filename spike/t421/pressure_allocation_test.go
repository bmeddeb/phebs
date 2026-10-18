//go:build darwin || linux

package t421

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestExecutionPressureAllocationBounds(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	// The placeholder proves bounds only. It never reaches a syscall: every
	// invocation of the mutation below is an input refusal, before file use.
	file := &os.File{}
	for _, test := range []struct {
		name          string
		ctx           context.Context
		file          *os.File
		before, after uint64
		valid         bool
	}{
		{name: "grow", ctx: t.Context(), file: file, after: 4096, valid: true},
		{name: "shrink", ctx: t.Context(), file: file, before: 4096, valid: true},
		{name: "same", ctx: t.Context(), file: file, before: 4096, after: 4096, valid: true},
		{name: "ceiling", ctx: t.Context(), file: file, before: 80 << 30, after: 80 << 30, valid: true},
		{name: "nil_context", file: file, after: 4096},
		{name: "canceled", ctx: canceled, file: file, after: 4096},
		{name: "nil_file", ctx: t.Context(), after: 4096},
		{name: "before_alignment", ctx: t.Context(), file: file, before: 1, after: 4096},
		{name: "after_alignment", ctx: t.Context(), file: file, after: 4095},
		{name: "before_ceiling", ctx: t.Context(), file: file, before: 80<<30 + 4096},
		{name: "after_ceiling", ctx: t.Context(), file: file, after: 80<<30 + 4096},
		{name: "overflow", ctx: t.Context(), file: file, before: ^uint64(0), after: ^uint64(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validExecutionPressureAllocation(test.ctx, test.file, test.before, test.after); got != test.valid {
				t.Fatalf("bounds=%t, want %t", got, test.valid)
			}
			if !test.valid && !errors.Is(resizeExecutionPressureBallast(test.ctx, test.file, test.before, test.after), errPressureVolume) {
				t.Fatal("invalid mutation reached file use")
			}
		})
	}
}
