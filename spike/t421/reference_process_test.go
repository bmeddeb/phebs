//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestReferencePreparationSession(t *testing.T) {
	for _, mode := range []string{"clean-root", "exit-root", "held-root", "failed-start", "canceled-before-start"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
			command.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"}
			command.WaitDelay = time.Second
			command.Stdin = bytes.NewReader([]byte{1}) // Authorizes only the fixture root's exit.
			var output referencePreparationOutput
			command.Stdout = &output
			if mode == "held-root" {
				output.cancel = cancel // Cancel only after the real descendant reports ready.
			}
			if mode == "failed-start" {
				command.Path = filepath.Join(t.TempDir(), "missing-executable")
			}
			if mode == "canceled-before-start" {
				cancel()
			}
			err := runReferenceCommand(ctx, command)
			if mode == "held-root" && ctx.Err() != context.Canceled {
				t.Fatal("fixture readiness did not drive cancellation")
			}
			if (err == nil) != (mode == "clean-root") {
				t.Fatalf("honest session result: %v", err)
			}
			if command.Process == nil {
				if mode != "failed-start" && mode != "canceled-before-start" {
					t.Fatal("fixture did not start")
				}
				return
			}
			if command.ProcessState == nil {
				t.Fatal("preparation root was not joined")
			}
			if members, err := t4013.PrivateProcessSessionMembers(command.Process.Pid); err != nil || members != 0 {
				_ = t4013.KillPrivateProcessSession(command.Process.Pid)
				t.Fatalf("preparation session survived: %d/%v", members, err)
			}
			if mode == "exit-root" || mode == "held-root" {
				if output.buffer.Len() < 8 || binary.BigEndian.Uint64(output.buffer.Bytes()[:8]) == 0 {
					t.Fatal("test did not exercise a surviving different-group descendant")
				}
			}
		})
	}
}

// Used only by the executable fixture; native Wait joins this writer before
// the test reads it. Readiness-driven cancellation avoids timing assumptions.
type referencePreparationOutput struct {
	buffer bytes.Buffer
	cancel context.CancelFunc
}

func (output *referencePreparationOutput) Write(raw []byte) (int, error) {
	n, err := output.buffer.Write(raw)
	if output.cancel != nil && output.buffer.Len() >= 8 {
		output.cancel()
	}
	return n, err
}

func TestExternalProbePreparationParentRetainsScratch(t *testing.T) {
	requireExternalToolFrozenHost(t)
	outside := t.TempDir()
	t.Setenv("TMPDIR", outside)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	ctx := withExecutionPreparationParent(t.Context(), parent)
	// The wrong-role attempt refuses after creating its probe directory, which
	// must be retained just like a successful observation's scratch.
	for _, role := range []string{"go", "git"} {
		_, err := ObserveExecutionExternalTool(ctx, role, goBinary)
		if (err == nil) != (role == "go") {
			t.Fatalf("role %s result: %v", role, err)
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 2 {
		t.Fatalf("marked preparation scratch not retained: %d/%v", len(entries), err)
	}
	assertExternalProbeParentEmpty(t, outside)
}
