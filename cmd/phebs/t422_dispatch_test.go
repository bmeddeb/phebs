package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const t422BoundaryHelper = "PHEBS_T422_COMMAND_BOUNDARY_HELPER"

func TestT422CommandBoundary(t *testing.T) {
	for _, test := range []struct {
		name     string
		selector string
		code     int
		failed   bool
	}{
		{name: "usage", code: 2},
		{name: "unknown", code: 2},
		{name: "version"},
		{name: "version-arguments", code: 1, failed: true},
		{name: "invalid-selector", selector: "not-a-contract", code: 1, failed: true},
		{name: "missing-endpoints", selector: dispatchadmission.ProductionSelector, code: 1, failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422CommandBoundaryHelper$")
			command.Dir = t.TempDir()
			command.WaitDelay = time.Second
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, t422BoundaryHelper+"=") &&
					!strings.HasPrefix(value, dispatchadmission.ProductionEnvironment+"=") {
					command.Env = append(command.Env, value)
				}
			}
			command.Env = append(command.Env, t422BoundaryHelper+"="+test.name)
			if test.selector != "" {
				command.Env = append(command.Env, dispatchadmission.ProductionEnvironment+"="+test.selector)
			}
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				var exited *exec.ExitError
				if !errors.As(err, &exited) {
					t.Fatalf("command boundary did not join: %v", err)
				}
				code = exited.ExitCode()
			}
			if ctx.Err() != nil || code != test.code || strings.Contains(string(output), "terminal_error") != test.failed {
				t.Fatalf("boundary code=%d output=%q error=%v deadline=%v", code, output, err, ctx.Err())
			}
		})
	}
}

// A missing inherited descriptor must be tested in its own process: an
// arbitrary descriptor number in the test runner could belong to its poller.
func TestT422CommandBoundaryHelper(t *testing.T) {
	name := os.Getenv(t422BoundaryHelper)
	if name == "" {
		return
	}
	var args []string
	switch name {
	case "usage":
	case "unknown":
		args = []string{"unknown"}
	case "version", "invalid-selector", "missing-endpoints":
		args = []string{"version"}
	case "version-arguments":
		args = []string{"version", "unexpected"}
	default:
		t.Fatal("unknown command boundary fixture")
	}
	code, err := runPhebs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "terminal_error")
	}
	os.Exit(code)
}
