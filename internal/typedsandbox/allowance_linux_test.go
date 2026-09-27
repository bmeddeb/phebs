//go:build linux

package typedsandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestAllowanceLinuxClockAndContext(t *testing.T) {
	c := testControlIdentity()
	a, err := BeginAllowance(t.Context(), c.PlanningDigest, c.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	if a.CheckLive(t.Context()) != nil {
		t.Fatal("own clock refused")
	}
	for _, change := range []func(*Allowance){func(v *Allowance) { v.BootID = "ffffffff-ffff-ffff-ffff-ffffffffffff" }, func(v *Allowance) { v.TimeInode++ }, func(v *Allowance) { v.TimeDevice++ }, func(v *Allowance) { v.Start += int64(time.Hour); v.Deadline += int64(time.Hour) }} {
		bad := a
		change(&bad)
		if bad.CheckLive(t.Context()) == nil {
			t.Fatal("changed clock accepted")
		}
	}
	now, err := bootNow()
	if err != nil {
		t.Fatal(err)
	}
	a.Start = now - int64(WallLimit) + int64(100*time.Millisecond)
	a.Deadline = a.Start + int64(WallLimit)
	ctx, stop, err := AllowanceContext(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("absolute context did not expire")
	}
	if a.CheckLive(t.Context()) == nil {
		t.Fatal("expired allowance admitted")
	}
}

type allowanceProbe struct {
	PID, Parent int
	Now         int64
	Event       string
}

func TestAllowanceProcessHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_ALLOWANCE_TEST")
	if mode == "" {
		return
	}
	if mode == "owner" {
		child := exec.Command(os.Args[0], "-test.run=^TestAllowanceProcessHelper$")
		child.Env = append(os.Environ(), "PHEBS_ALLOWANCE_TEST=orphan")
		child.ExtraFiles = []*os.File{os.NewFile(3, "probe")}
		if child.Start() != nil {
			os.Exit(90)
		}
		_ = child.Wait()
		os.Exit(91)
	}
	if mode != "orphan" {
		os.Exit(92)
	}
	raw := os.Getenv("PHEBS_ALLOWANCE_RAW")
	a, err := DecodeAllowance([]byte(raw))
	if err != nil {
		os.Exit(93)
	}
	probe := os.NewFile(3, "probe")
	args := supervisorArgs(Options{Allowance: a, Control: ControlIdentity{Phase: ControlExecute, RequestDigest: testImage, SealDigest: testImage}})
	_, _, _, stop, err := bootstrapSupervisor(args, func(timerErr error) {
		if timerErr != nil {
			os.Exit(96)
		}
		now, _ := bootNow()
		_ = json.NewEncoder(probe).Encode(allowanceProbe{os.Getpid(), os.Getppid(), now, "expired"})
		os.Exit(124)
	})
	if err != nil {
		os.Exit(94)
	}
	defer stop()
	now, _ := bootNow()
	_ = json.NewEncoder(probe).Encode(allowanceProbe{os.Getpid(), os.Getppid(), now, "armed"})
	// A real blocking filesystem validation after the production bootstrap. No
	// writer ever opens this FIFO; only the absolute watchdog can end this process.
	_, _ = unix.Open(os.Getenv("PHEBS_ALLOWANCE_FIFO"), unix.O_RDONLY, 0)
	os.Exit(95)
}

func TestAllowanceOrphanedBlockedSecondSupervisor(t *testing.T) {
	c := testControlIdentity()
	a, err := BeginAllowance(t.Context(), "sha256:"+fmt.Sprintf("%064x", 3), c.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	now, err := bootNow()
	if err != nil {
		t.Fatal(err)
	}
	a.Start = now - int64(WallLimit) + int64(time.Second)
	a.Deadline = a.Start + int64(WallLimit)
	a.WorkerBytesUsed = 123
	a.WireBytesUsed = 456
	raw, err := EncodeAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(t.TempDir(), "validation")
	if err = unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	// Interphase work consumes part of the already fixed deadline.
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	owner := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAllowanceProcessHelper$")
	owner.Env = append(os.Environ(), "PHEBS_ALLOWANCE_TEST=owner", "PHEBS_ALLOWANCE_RAW="+string(raw), "PHEBS_ALLOWANCE_FIFO="+fifo)
	owner.ExtraFiles = []*os.File{writer}
	if err = owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Process.Kill(); _ = owner.Wait() }()
	_ = writer.Close()
	events := make(chan allowanceProbe, 2)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scan := bufio.NewScanner(reader)
		for scan.Scan() {
			var p allowanceProbe
			if json.Unmarshal(scan.Bytes(), &p) == nil {
				events <- p
			}
		}
	}()
	defer func() { _ = reader.Close(); <-readDone }()
	var armed allowanceProbe
	select {
	case armed = <-events:
	case <-ctx.Done():
		t.Fatal("supervisor did not arm")
	}
	if armed.Event != "armed" || armed.Parent != owner.Process.Pid {
		t.Fatal(armed)
	}
	orphan, err := os.FindProcess(armed.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = orphan.Kill(); _ = orphan.Release() }()
	if err = owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	select {
	case expired := <-events:
		if expired.Event != "expired" || expired.PID != armed.PID || expired.Parent == armed.Parent || expired.Now < a.Deadline || expired.Now > a.Deadline+int64(time.Second) {
			t.Fatal("deadline refreshed or owner survived", armed, expired, a.Deadline)
		}
	case <-ctx.Done():
		t.Fatal("orphan gained another interval")
	}
	// If this test is namespace PID1, reap its newly adopted exact test child.
	var status unix.WaitStatus
	_, _ = unix.Wait4(armed.PID, &status, 0, nil)
}

func TestAllowanceWorkerAggregateBoundaries(t *testing.T) {
	for _, used := range []int64{0, 3, OutputBytes - 3, OutputBytes} {
		for _, delta := range []int64{-1, 0, 1} {
			n := int64(OutputBytes) - used + delta
			if n < 0 {
				continue
			}
			t.Run(strconv.FormatInt(used, 10)+"/"+strconv.FormatInt(delta, 10), func(t *testing.T) {
				canceled := false
				o := &childOutput{limit: OutputBytes - used, cancel: func() { canceled = true }}
				out, stderr := childStream{o, false}, childStream{o, true}
				first := min(n, 2)
				if _, err := out.Write(bytes.Repeat([]byte("o"), int(first))); err != nil && delta <= 0 {
					t.Fatal(err)
				}
				_, err := stderr.Write(bytes.Repeat([]byte("e"), int(n-first)))
				if delta <= 0 {
					if err != nil || canceled || int64(o.stdout.Len()+o.stderr.Len()) != n {
						t.Fatal(err, canceled)
					}
				} else if !canceled || !o.overflow {
					t.Fatal("aggregate overflow accepted")
				}
			})
		}
	}
}
