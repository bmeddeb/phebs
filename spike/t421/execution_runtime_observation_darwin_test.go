//go:build darwin

package t421

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// Real tiny native sessions with supplied stdout, not protected Phebs facts,
// selected admission, any database/worker or a complete profile.
func TestExecutionRuntimeProbeNativeSession(t *testing.T) {
	raw := string(runtimeFactsTestJSON(t, modeledExecutionRuntimeFacts()))
	for _, mode := range []string{"success", "stderr", "exit", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second) // Original test-only deadline.
			defer cancel()
			deadline, _ := ctx.Deadline()
			script := "printf '%s' '" + raw + "'"
			switch mode {
			case "stderr":
				script += "; printf refused >&2"
			case "exit":
				script += "; exit 7"
			case "overflow":
				script += "; printf '%05000d' 0; while :; do :; done"
			}
			command := exec.Command("/bin/sh", "-c", script)
			directory, makeErr := os.MkdirTemp("", "t422-runtime-probe-model-")
			if makeErr != nil {
				t.Fatal(makeErr)
			}
			command.Dir = directory
			command.Env = externalToolEnvironment(command.Dir)
			var observed executionRuntimeObservation
			defer func() {
				if !observed.releasable() {
					t.Log("retained tiny process cwd beside unjoined native prefix:", directory)
					return
				}
				if err := os.Remove(directory); err != nil {
					t.Error(err)
				}
			}()
			err := observed.run(ctx, command)
			if (err == nil) != (mode == "success") || observed.Deadline != deadline ||
				!observed.RootStarted || !observed.RootJoined || !observed.SessionEmpty || observed.PID <= 0 {
				t.Fatalf("native prefix pid=%d started=%t joined=%t empty=%t deadline=%s: %v",
					observed.PID, observed.RootStarted, observed.RootJoined, observed.SessionEmpty, observed.Deadline, err)
			}
			members, memberErr := t4013.PrivateProcessSessionMembers(observed.PID)
			if memberErr != nil || members != 0 {
				t.Fatal("native test session not empty", members, memberErr)
			}
			if observed.stdout.buffer.Len() > 4<<10 || observed.stderr.buffer.Len() > 4<<10 {
				t.Fatal("existing output bounds exceeded")
			}
			if mode == "success" {
				facts, decodeErr := decodeExecutionRuntimeFacts(observed.stdout.buffer.Bytes())
				if decodeErr != nil || facts != modeledExecutionRuntimeFacts() {
					t.Fatal("actual pipe did not retain supplied model", facts, decodeErr)
				}
			}
			if observed.Complete || observed.Observed {
				t.Fatal("generic process runner issued protected runtime observations")
			}
		})
	}
}

func TestExecutionRuntimeProbeNativePrestartRefusal(t *testing.T) {
	past, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, ctx := range []context.Context{nil, context.Background(), past} {
		var observed executionRuntimeObservation
		command := exec.Command("/bin/sh", "-c", "exit 0")
		if err := observed.run(ctx, command); err == nil || observed.RootStarted || command.Process != nil {
			t.Fatal("unbounded or canceled prework started a process", err)
		}
	}
}

func TestExecutionRuntimeProbeClosedEnvironment(t *testing.T) {
	environment := externalToolEnvironment(t.TempDir())
	for _, value := range environment {
		if strings.HasPrefix(value, "PHEBS_") {
			t.Fatal("operational selector entered no-work environment", value)
		}
	}
}
