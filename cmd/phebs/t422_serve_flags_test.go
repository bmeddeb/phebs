//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// This record authenticates the real inherited protocol only. No tool starts,
// config loads, private-custody issuer or execution-freeze claim is involved.
func t422ServeFlagsRecord() dispatchadmission.ProductionBootstrap {
	environment := []string{"HOME=/tmp", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "PATH=/tmp", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	gitEnvironment := append(slices.Clone(environment), "GIT_EXEC_PATH=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR=/dev/null", "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_VALUE_2=/dev/null")
	return dispatchadmission.ProductionBootstrap{
		Program:  dispatchadmission.ProgramPhebs,
		Producer: dispatchadmission.Producer{ID: 1, Binding: [32]byte{11}, Sites: dispatchadmission.ProductionSites()}, Phase: 1,
		Limits: dispatchadmission.Limits{Producers: 1, Sites: 16, Roles: 4, Phases: 1, ActivePerProducer: 1,
			Attempts: 1, WireBytes: 4096, AckTimeout: 2 * time.Second},
		Control: dispatchadmission.PhaseControlConfig{Phases: []uint32{1}, InitialPhase: 1, MaximumPhases: 1, MaximumWireBytes: 4096, Timeout: 2 * time.Second},
		Tools: []dispatchadmission.ProductionToolBinding{
			{Role: "git", Path: "/usr/bin/true", Environment: slices.Clone(gitEnvironment)},
			{Role: "surreal", Path: "/usr/bin/true", Environment: slices.Clone(environment)},
			{Role: "zoekt-git-index", Path: "/usr/bin/true", Environment: slices.Clone(gitEnvironment)},
		},
	}
}

func TestT422ServeFlagsReturnThroughAdmittedCleanup(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       string
		code       int
		unfenced   bool
		badBinding bool
	}{
		{name: "serve-help", mode: "serve-help"},
		{name: "serve-invalid", mode: "serve-invalid", code: 2},
		{name: "unfenced-help", mode: "serve-help", unfenced: true},
		{name: "cleanup-error-wins", mode: "serve-help", code: 1, unfenced: true, badBinding: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			record := t422ServeFlagsRecord()
			producer := record.Producer
			if test.badBinding {
				// Authenticate the bootstrap, then deliberately bind its DA01
				// endpoint to a different lifetime. This is a real terminal
				// protocol failure, unlike valid unfenced producer-local Close.
				producer.Binding[1] = 1
			}
			controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{
				Limits: record.Limits, Producers: []dispatchadmission.Producer{producer},
				Phases: []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{
					{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal},
					{Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility},
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			// Early parser return has no owner work. Both a globally fenced
			// completion and an unfenced producer-local terminal Close are valid.
			if !test.unfenced {
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
			}
			parent, child, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
			controlParent, controlChild, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = controlParent.Close(); _ = controlChild.Close() })
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422CommandBoundaryHelper$")
			command.Dir = t.TempDir()
			command.Env = []string{t422BoundaryHelper + "=" + test.mode,
				dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector,
				"GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles = []*os.File{child, controlChild}
			command.WaitDelay = time.Second
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
			_ = child.Close()
			_ = controlChild.Close()
			if err := dispatchadmission.SendProductionBootstrap(ctx, parent, controlParent, record); err != nil {
				t.Fatal(err)
			}
			served := make(chan error, 1)
			go func() { served <- controller.Serve(ctx, 1, command.Process.Pid, parent) }()
			err = command.Wait()
			code := 0
			if err != nil {
				var exited *exec.ExitError
				if !errors.As(err, &exited) {
					t.Fatalf("serve parser did not join: %v", err)
				}
				code = exited.ExitCode()
			}
			if code != test.code || !strings.Contains(output.String(), "owned_cleanup_returned") ||
				strings.Contains(output.String(), "terminal_error") != test.badBinding {
				t.Fatalf("serve parser code=%d, output=%q, error=%v", code, output.String(), err)
			}
			select {
			case err := <-served:
				if (err != nil) != test.badBinding {
					t.Fatalf("parser closure result=%v, want refusal=%v", err, test.badBinding)
				}
			case <-ctx.Done():
				t.Fatal("admission receiver did not join the parser return")
			}
			snapshot, err := controller.Snapshot()
			if (err != nil) != test.badBinding || snapshot.Complete != (!test.unfenced && !test.badBinding) ||
				snapshot.Attempts != 0 || snapshot.Producers[0].Closed == test.badBinding {
				t.Fatalf("parser did not close its exact empty prefix: %+v, %v", snapshot, err)
			}
		})
	}
}
