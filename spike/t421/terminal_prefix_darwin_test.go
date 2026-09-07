//go:build darwin

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

type terminalPrefixTestWriter struct {
	output *checkoutCommandOutput
	wrote  chan error
}

func (writer *terminalPrefixTestWriter) Write(raw []byte) (int, error) {
	n, err := writer.output.Write(raw)
	writer.wrote <- err
	return n, err
}

// Real inherited stderr, native Wait and owned SIGKILL prove the pump-error
// counterfactual. The test binary is not an admitted Phebs image and supplies
// no PC/SDK/DA/SA or complete terminal-phase authority.
func TestExecutionTerminalPrefixNativePump(t *testing.T) {
	if os.Getenv("PHEBS_TERMINAL_PREFIX_HELPER") == "1" {
		prefix, footer := terminalPrefixTestBytes()
		if _, err := io.WriteString(os.Stderr, prefix+footer); err != nil {
			os.Exit(2)
		}
		var next [1]byte
		if _, err := io.ReadFull(os.Stdin, next[:]); err != nil {
			os.Exit(2)
		}
		_, _ = io.WriteString(os.Stderr, "lost complete diagnostic\n")
		_, _ = io.ReadFull(os.Stdin, next[:])
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	prefix, footer := terminalPrefixTestBytes()
	output := &checkoutCommandOutput{remaining: int64(len(prefix) + len(footer)), cancel: func() {}}
	writer := &terminalPrefixTestWriter{output: output, wrote: make(chan error, 2)}
	command := exec.Command(os.Args[0], "-test.run=^TestExecutionTerminalPrefixNativePump$")
	command.Env = []string{"PHEBS_TERMINAL_PREFIX_HELPER=1", "GORACE=atexit_sleep_ms=0"}
	command.Stdout, command.Stderr = writer, writer
	prepareProductionSession(command)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	joined := false
	defer func() {
		if !joined {
			_ = command.Process.Kill()
			<-waited
		}
	}()
	select {
	case err := <-writer.wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writer.wrote:
		if err == nil {
			t.Fatal("native pump failed to reach output refusal")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	death, err := killExecutionProcessSession(ctx, command, waited)
	joined = death.RootJoined
	if err != nil || !death.RootJoined || !death.SessionEmpty || output.err == nil || errors.Is(death.WaitErr, output.err) {
		t.Fatal("native SIGKILL did not hide joined copy error as expected", death, err)
	}
	if seen, err := executionTerminalFooter(output.buffer.Bytes(), [32]byte{1}); !seen || err != nil {
		t.Fatal("retained bytes alone should contain a valid footer", seen, err)
	}
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: accountingTestPlan(t)}, output: output, attemptInput: [32]byte{1},
		epoch: ExecutionEpochConfig{Epoch: 3}, command: command, terminalEntered: true, terminalRequested: true, checkpointAllowed: true}
	result := ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true}
	if err := run.finishAttemptObservation(ctx, &result, death, nil); err == nil || result.Attempts.Complete || result.IndexOffers.Complete ||
		result.Attempts.Phases[5].JobAttempts != 1 || result.IndexOffers.Phases[5].Offers != 1 {
		t.Fatal("native footer/kill hid missing controller proof or writer refusal", result.Attempts, result.IndexOffers)
	}
}
