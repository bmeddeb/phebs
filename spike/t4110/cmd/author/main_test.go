package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	privateSessionSignalHelperEnvironment = "PHEBS_T4110_SIGNAL_HELPER"
	privateSessionSignalFileEnvironment   = "PHEBS_T4110_SIGNAL_FILE"
)

func TestClearAmbientGitEnvironment(t *testing.T) {
	t.Setenv("GIT_DIR", "/attacker/repository")
	t.Setenv("GIT_TRACE", "/attacker/trace")
	if err := clearAmbientGitEnvironment(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_") {
			t.Fatalf("ambient Git variable remained: %q", entry)
		}
	}
}

func TestPrivateSessionSignalHelper(t *testing.T) {
	if os.Getenv(privateSessionSignalHelperEnvironment) != "1" {
		return
	}
	sessionFile := os.Getenv(privateSessionSignalFileEnvironment)
	if sessionFile == "" {
		t.Fatal("signal helper session file is absent")
	}
	script := `printf '%s\n' "$$" > "$PHEBS_T4110_SIGNAL_FILE"; sleep 300 & wait`
	if err := runPrivateSessionCommand(
		"/bin/sh", []string{"-c", script}, os.Environ(),
	); err == nil {
		t.Fatal("interrupted private session command returned success")
	}
}

func TestRunPrivateSessionCommandCleansSessionOnCatchableSignals(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(signal.String(), func(t *testing.T) {
			sessionFile := filepath.Join(t.TempDir(), "session")
			command := exec.Command(os.Args[0], "-test.run=^TestPrivateSessionSignalHelper$")
			command.Env = append(os.Environ(),
				privateSessionSignalHelperEnvironment+"=1",
				privateSessionSignalFileEnvironment+"="+sessionFile,
			)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill() })

			var sessionID int
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(sessionFile)
				if err == nil {
					sessionID, err = strconv.Atoi(strings.TrimSpace(string(data)))
					if err != nil || sessionID <= 0 {
						t.Fatalf("session identity %q: %v", data, err)
					}
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if sessionID == 0 {
				t.Fatalf("private session did not start: %s", output.String())
			}
			t.Cleanup(func() { _ = syscall.Kill(-sessionID, syscall.SIGKILL) })
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("interrupted wrapper did not exit: %s", output.String())
			}
			deadline = time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				members, err := t4013.PrivateProcessSessionMembers(sessionID)
				if err != nil {
					t.Fatal(err)
				}
				if members == 0 {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			members, err := t4013.PrivateProcessSessionMembers(sessionID)
			t.Fatalf("private session retained %d member(s): %v; %s", members, err, output.String())
		})
	}
}
