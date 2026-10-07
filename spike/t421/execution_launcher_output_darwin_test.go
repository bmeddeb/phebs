//go:build darwin

package t421

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// These fixtures own the nonblocking pipe before launch. NewFile preserves its
// already-nonblocking status when exec obtains the child stdout descriptor.
func runExecutionLauncherWithOutput(t *testing.T, command *exec.Cmd) ([]byte, error) {
	t.Helper()
	reader, writer := executionLauncherOutputPipe(t, true)
	command.Stdout = writer
	type capture struct {
		raw []byte
		err error
	}
	done := make(chan capture, 1)
	go func() {
		defer func() { _ = reader.Close() }()
		limit := int64(maxExecutionAuthorizationHandoffFrameBytes+len(executionReturnedFrameMagic)+4) + int64(frozenSealPolicy().MaximumPackageBytes)
		raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
		done <- capture{raw: raw, err: err}
	}()
	runErr := command.Run()
	if err := writer.Close(); err != nil {
		t.Error(err)
	}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result.raw, runErr
}

func TestExecutionOuterRefusesUnsupportedOutputBeforeInnerStart(t *testing.T) {
	selection, _ := testExecutionSelection(t)
	selection.CeremonyID = "t422-no-handoff-test"
	executable := protectedExecutionTestImage(t)
	for _, kind := range []string{"blocking_pipe", "regular_file"} {
		t.Run(kind, func(t *testing.T) {
			var output *os.File
			if kind == "blocking_pipe" {
				_, output = executionLauncherOutputPipe(t, false)
			} else {
				var err error
				output, err = os.CreateTemp(t.TempDir(), "output")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = output.Close() })
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
			command.Stdout = output
			command.Env = []string{"AMBIENT_IGNORED=1"}
			// This TestMain selection returns 45 if the inner ever starts and
			// supplies its native exit 43; refusal before Start returns 46.
			if code := exitCode(t, command.Run()); code != 46 || ctx.Err() != nil {
				t.Fatalf("unsupported output reached inner or exceeded deadline: %d, %v", code, ctx.Err())
			}
		})
	}
}
