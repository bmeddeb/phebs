//go:build darwin || linux

package t421

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// Protocol fixture only: the root owns a different-process-group descendant in
// its isolated session. No Phebs, engine, admitted image or source is involved.
func TestExecutionProcessSessionHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_EXECUTION_SESSION_HELPER")
	if mode == "" || mode == "clean-root" {
		return
	}
	signal.Ignore(syscall.SIGTERM, os.Interrupt)
	if mode == "child" {
		if _, err := os.Stdout.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
	child.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=child", "GORACE=atexit_sleep_ms=0"}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Stderr = os.Stderr
	ready, err := child.StdoutPipe()
	if err != nil || child.Start() != nil {
		t.Fatal("start isolated-session descendant fixture", err)
	}
	var marker [1]byte
	if _, err := io.ReadFull(ready, marker[:]); err != nil || marker[0] != 1 {
		t.Fatal("descendant fixture readiness", err)
	}
	var pid [8]byte
	binary.BigEndian.PutUint64(pid[:], uint64(child.Process.Pid))
	if _, err := os.Stdout.Write(pid[:]); err != nil {
		t.Fatal(err)
	}
	if mode == "exit-root" {
		if _, err := io.ReadFull(os.Stdin, marker[:]); err != nil || marker[0] != 1 {
			t.Fatal("fixture root exit signal", err)
		}
		os.Exit(0) // Deliberately orphan only this fixture's recorded descendant.
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestExecutionProcessSessionCleanup(t *testing.T) {
	for _, mode := range []string{"exit-root", "held-root"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
			command.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"}
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			command.Stderr = os.Stderr
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close() }()
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = output.Close() }()
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			var waited chan error
			defer func() {
				_ = t4013.KillPrivateProcessSession(command.Process.Pid)
				if !joined {
					if waited == nil {
						_ = command.Wait()
					} else {
						<-waited
					}
				}
				if err := t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(6*time.Second)); err != nil {
					t.Errorf("fixture session cleanup: %v", err)
				}
			}()
			var raw [8]byte
			if _, err := io.ReadFull(output, raw[:]); err != nil {
				t.Fatal(err)
			}
			childPID := int(binary.BigEndian.Uint64(raw[:]))
			session, sessionErr := unix.Getsid(childPID)
			group, groupErr := syscall.Getpgid(childPID)
			if sessionErr != nil || groupErr != nil || session != command.Process.Pid || group == command.Process.Pid {
				t.Fatalf("fixture descendant is not a separate group in the owned session: %d/%d %v/%v", session, group, sessionErr, groupErr)
			}
			waited = make(chan error, 1)
			go func() { waited <- command.Wait() }()
			var waitErr error
			if mode == "exit-root" {
				if _, err := input.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
				waitErr = <-waited
				joined = true
				if waitErr != nil {
					t.Fatal(waitErr)
				}
			}
			if members, err := t4013.PrivateProcessSessionMembers(command.Process.Pid); err != nil || members == 0 {
				t.Fatalf("expected live fixture survivor: %d/%v", members, err)
			}
			if !joined {
				if err := command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			rootJoined, empty, err := finishExecutionProcessSession(command.Process.Pid, waited, joined, waitErr, time.Now().Add(100*time.Millisecond))
			joined = rootJoined
			if !rootJoined || !empty || !errors.Is(err, ErrExecutionProductionCustody) {
				t.Fatalf("forced cleanup must join and empty while remaining failed: %v/%v/%v", rootJoined, empty, err)
			}
			if members, err := t4013.PrivateProcessSessionMembers(command.Process.Pid); err != nil || members != 0 {
				t.Fatalf("session-wide cleanup left a live descendant: %d/%v", members, err)
			}
		})
	}
}

func TestExecutionProcessSessionCleanRoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
	command.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=clean-root", "GORACE=atexit_sleep_ms=0"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	joined, empty, err := finishExecutionProcessSession(command.Process.Pid, waited, false, nil, time.Now().Add(5*time.Second))
	if !joined {
		_ = t4013.KillPrivateProcessSession(command.Process.Pid)
		<-waited
	}
	if !joined || !empty || err != nil {
		t.Fatalf("natural joined empty session refused: %v/%v/%v", joined, empty, err)
	}
}

func TestExecutionProcessSessionRejectsInvalidOwner(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if joined, empty, err := finishExecutionProcessSession(pid, nil, true, nil, time.Now()); !joined || empty || err == nil {
			t.Fatal("invalid session acquired clean closure")
		}
	}
}
