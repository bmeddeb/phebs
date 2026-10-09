//go:build linux

package typedsandbox

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// TestMain supplies a neutral executable dispatch so the regression uses actual
// worker argv, including after its launching parent exits. It never supplies
// privileged sandbox setup and must therefore never mint a worker token.
func TestMain(m *testing.M) {
	mode := os.Getenv("PHEBS_HANDOFF_TEST")
	if mode == "parent" {
		child := exec.Command(os.Args[0], os.Args[1:]...)
		child.Env = append(os.Environ(), "PHEBS_HANDOFF_TEST=orphan", "PHEBS_HANDOFF_PARENT="+strconv.Itoa(os.Getpid()))
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(90)
		}
		os.Exit(0)
	}
	if mode == "orphan" {
		launchingParent, parseErr := strconv.Atoi(os.Getenv("PHEBS_HANDOFF_PARENT"))
		if parseErr != nil || launchingParent <= 1 {
			os.Exit(90)
		}
		deadline := time.Now().Add(2 * time.Second)
		// Linux may adopt this child into a subreaper rather than PID 1. Prove
		// the actual launching parent was lost while retaining the argv refusal.
		for os.Getppid() == launchingParent && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		_, err := ReadWorkerInvocation(context.Background())
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Parent          int
			LaunchingParent int
			Refused         bool
		}{os.Getppid(), launchingParent, err != nil})
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestWorkerInvocationOrphanArgvRefuses(t *testing.T) {
	c := testControlIdentity()
	allowance, err := BeginAllowance(t.Context(), c.PlanningDigest, c.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	args := workerArgs(supervisorArgs(Options{Allowance: allowance, Control: c}))
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], args...)
	command.Env = append(os.Environ(), "PHEBS_HANDOFF_TEST=parent")
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(raw))
	}
	var got struct {
		Parent          int
		LaunchingParent int
		Refused         bool
	}
	if json.Unmarshal(raw, &got) != nil || got.Parent < 1 || got.LaunchingParent <= 1 || got.Parent == got.LaunchingParent || !got.Refused {
		t.Fatal("orphan argv minted authority", string(raw))
	}
}

func TestWorkerInvocationSocketOneShot(t *testing.T) {
	for _, fault := range []string{"", "empty", "wrong peer", "wrong digest", "truncated", "stream"} {
		t.Run(fault, func(t *testing.T) {
			kind := unix.SOCK_DGRAM
			if fault == "stream" {
				kind = unix.SOCK_STREAM
			}
			pair, err := unix.Socketpair(unix.AF_UNIX, kind, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(pair[0]) }()
			defer func() { _ = unix.Close(pair[1]) }()
			expected := controlDigest([]byte("argv"))
			raw := []byte(expected)
			if fault == "wrong digest" {
				raw = []byte(controlDigest([]byte("other")))
			}
			if fault == "truncated" {
				raw = append(raw, 'x')
			}
			if fault != "empty" {
				if _, err = unix.SendmsgN(pair[0], raw, nil, nil, unix.MSG_DONTWAIT); err != nil {
					t.Fatal(err)
				}
			}
			want := unix.Ucred{Pid: int32(os.Getpid()), Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}
			if fault == "wrong peer" {
				want.Pid++
			}
			err = authenticateWorkerSocket(pair[1], expected, want)
			if (err == nil) != (fault == "") {
				t.Fatal("authentication", err)
			}
			flags, e := unix.FcntlInt(uintptr(pair[1]), unix.F_GETFD, 0)
			if e != nil || flags&unix.FD_CLOEXEC == 0 {
				t.Fatal("inherited socket", flags, e)
			}
			if fault == "" && authenticateWorkerSocket(pair[1], expected, want) == nil {
				t.Fatal("consumed authorization replayed")
			}
		})
	}
}
