//go:build linux

package t421

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// These own-image children reproduce the observation boundary only. Their real
// DA handles do not issue author, Git, input, or operational resource authority.
type linuxFiniteExitFixture struct {
	command *exec.Cmd
	handle  dispatchadmission.Handle
	parent  *dispatchadmission.LocalProducer
	input   *os.File
	output  *os.File
	reader  *bufio.Reader
	pidfd   int
	joined  bool
}

func linuxFiniteExitStart(t *testing.T, mode string) *linuxFiniteExitFixture {
	t.Helper()
	settlement, cancelSettlement := context.WithCancel(context.Background())
	t.Cleanup(cancelSettlement)
	controller, err := dispatchadmission.New(settlement, dispatchadmission.Config{
		Limits:    dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1, Attempts: 1, WireBytes: 16 << 10, AckTimeout: 2 * time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{{ID: 1, Role: 1}}}},
		Phases:    []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{{Role: 1, Attempts: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(settlement, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := parent.Close(ctx); err != nil {
			t.Error("finite fixture local producer close", err)
		}
	})
	childInput, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output, childOutput, err := os.Pipe()
	if err != nil {
		_ = childInput.Close()
		_ = input.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = childInput.Close()
		_ = childOutput.Close()
		_ = input.Close()
		_ = output.Close()
	})
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxEpochFiniteExitFixedChild$", "--", "t422-finite-"+mode)
	command.Env = []string{"GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = []*os.File{childInput, childOutput}
	handle, err := parent.StartInPhase(settlement, 1, dispatchadmission.Site{ID: 1, Role: 1}, command)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &linuxFiniteExitFixture{command: command, handle: handle, parent: parent, input: input, output: output, reader: bufio.NewReaderSize(output, 128), pidfd: -1}
	fixture.pidfd, err = pinExecutionRootDescriptor(command)
	// A child receives EOF before cleanup waits. A nested root then actually
	// joins its leaf and local DA transport before it exits itself.
	watchLimit := 10 * time.Second
	if mode == "root" {
		watchLimit = 30 * time.Second // Its nested leaf must finish cleanup first.
	}
	watch, cancelWatch := context.WithTimeout(context.Background(), watchLimit)
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(watch, func() {
		defer close(callbackDone)
		if fixture.pidfd >= 0 {
			_ = unix.PidfdSendSignal(fixture.pidfd, unix.SIGKILL, nil, 0)
		}
	})
	t.Cleanup(func() {
		_ = fixture.input.Close()
		if !fixture.joined {
			if err := fixture.wait(); err != nil {
				t.Error("finite fixture actual Wait", err)
			}
		}
		if !stop() {
			<-callbackDone
		}
		cancelWatch()
		if fixture.pidfd >= 0 && unix.Close(fixture.pidfd) != nil {
			t.Error("finite fixture pidfd close")
		}
	})
	if err != nil {
		t.Fatal("finite fixture pin before sole Wait", err)
	}
	if childInput.Close() != nil || childOutput.Close() != nil {
		t.Fatal("finite fixture child endpoint release")
	}
	return fixture
}

func (fixture *linuxFiniteExitFixture) wait() error {
	err := fixture.handle.Wait()
	fixture.joined = true
	return err
}

func (fixture *linuxFiniteExitFixture) send(t *testing.T, value string) {
	t.Helper()
	if fixture.input.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
		t.Fatal("finite fixture write deadline")
	}
	if _, err := fmt.Fprintln(fixture.input, value); err != nil {
		t.Fatal("finite fixture command", err)
	}
}

func (fixture *linuxFiniteExitFixture) receive(t *testing.T) string {
	t.Helper()
	if fixture.output.SetReadDeadline(time.Now().Add(5*time.Second)) != nil {
		t.Fatal("finite fixture read deadline")
	}
	line, err := fixture.reader.ReadString('\n')
	if err != nil || len(line) > 128 {
		t.Fatal("finite fixture response", err)
	}
	return strings.TrimSuffix(line, "\n")
}

func TestLinuxEpochFiniteExitFixedChild(t *testing.T) {
	if len(os.Args) == 0 {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode != "t422-finite-leaf" && mode != "t422-finite-root" {
		return
	}
	unix.CloseOnExec(3)
	unix.CloseOnExec(4)
	if unix.SetNonblock(3, true) != nil || unix.SetNonblock(4, true) != nil {
		t.Fatal("fixed child pollable endpoints")
	}
	input, output := os.NewFile(3, "finite-command"), os.NewFile(4, "finite-report")
	defer func() { _ = input.Close(); _ = output.Close() }()
	if input.SetReadDeadline(time.Now().Add(10*time.Second)) != nil {
		t.Fatal("fixed child read deadline")
	}
	reader := bufio.NewReaderSize(input, 128)
	if mode == "t422-finite-leaf" {
		if _, err := fmt.Fprintln(output, "ready"); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" {
			return // EOF also releases the fixed child on parent failure.
		}
		if err != nil || line != "exit\n" {
			t.Fatal("fixed leaf command", err)
		}
		return // Natural exit; no signal or fake Wait result.
	}
	leaf := linuxFiniteExitStart(t, "leaf")
	if leaf.receive(t) != "ready" {
		t.Fatal("fixed grandchild readiness")
	}
	if _, err := fmt.Fprintln(output, "ready", leaf.command.Process.Pid); err != nil {
		t.Fatal(err)
	}
	for {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" {
			return
		}
		if err != nil || len(line) > 128 {
			t.Fatal("fixed root command", err)
		}
		switch line {
		case "exit-leaf\n":
			leaf.send(t, "exit")
			if err := rootWaitObserveUnreaped(leaf.command); err != nil {
				t.Fatal(err)
			}
			_, err = fmt.Fprintln(output, "zombie")
		case "reap-leaf\n":
			if err := leaf.wait(); err != nil {
				t.Fatal("genuine grandchild Wait", err)
			}
			count, countErr := leaf.parent.Count()
			if countErr != nil || count.Ordinal != 1 || count.Active != 0 {
				t.Fatal("genuine grandchild DA settlement", count, countErr)
			}
			_, err = fmt.Fprintln(output, "reaped")
		case "exit-root\n":
			return
		default:
			t.Fatal("unknown fixed root command")
		}
		if err != nil {
			t.Fatal("fixed root report", err)
		}
	}
}

func linuxFiniteExitGauge(t *testing.T) *ProcessObservationGauge {
	t.Helper()
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = image.Close() })
	path, err := filepath.EvalSymlinks("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	shell, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	held := &linuxEpochHeldFlow{controllerPID: os.Getpid(), controllerImage: image, shellImage: shell}
	for i := range held.images {
		held.images[i] = shell
	}
	for i := range held.gitImages {
		held.gitImages[i] = shell
	}
	gauge, _, err := held.gauge(t.Context(), os.Getpid(), "")
	if err != nil {
		t.Fatal("mechanical live-image gauge", err)
	}
	return gauge
}

func linuxFiniteExitLiveRows(t *testing.T, pid int) []t4013.NativeProcessRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		rows, err := t4013.ObserveProcessTreeRecords(ctx, os.Getpid())
		if err != nil {
			t.Fatal("finite fixture native census refused", err)
		}
		for _, row := range rows {
			if row.PID == pid && row.RSSBytes > 0 {
				return rows
			}
		}
		if ctx.Err() != nil {
			t.Fatal("finite fixture missing positive live row")
		}
		time.Sleep(time.Millisecond) // Only wait for readiness; never retry refusal.
	}
}

func linuxFiniteExitRequireRefusal(t *testing.T, rows []t4013.NativeProcessRecord) {
	t.Helper()
	gauge := linuxFiniteExitGauge(t)
	observation, err := gauge.acceptRowsContext(t.Context(), rows)
	if err == nil || observation.Available || observation.CompletedCensuses != 0 || observation.FailureClass != "unknown_classification" {
		t.Fatal("live-only classifier accepted an exited/reaped finite child", observation, err)
	}
}

func TestLinuxEpochFiniteExitDirectNaturalBoundary(t *testing.T) {
	fixture := linuxFiniteExitStart(t, "leaf")
	if fixture.receive(t) != "ready" {
		t.Fatal("fixed direct child readiness")
	}
	rows := linuxFiniteExitLiveRows(t, fixture.command.Process.Pid)
	fixture.send(t, "exit")
	if err := rootWaitObserveUnreaped(fixture.command); err != nil || fixture.command.ProcessState != nil {
		t.Fatal("natural direct exit lost WNOWAIT barrier", err)
	}
	linuxFiniteExitRequireRefusal(t, rows)
	if err := fixture.wait(); err != nil {
		t.Fatal("genuine direct Handle.Wait", err)
	}
	count, err := fixture.parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 || fixture.command.ProcessState == nil {
		t.Fatal("direct native/DA settlement missing", count, err)
	}
	linuxFiniteExitRequireRefusal(t, rows)
}

func TestLinuxEpochFiniteExitNestedNaturalBoundary(t *testing.T) {
	fixture := linuxFiniteExitStart(t, "root")
	fields := strings.Fields(fixture.receive(t))
	if len(fields) != 2 || fields[0] != "ready" {
		t.Fatal("fixed nested readiness")
	}
	leaf, err := strconv.Atoi(fields[1])
	if err != nil || leaf <= 0 {
		t.Fatal("fixed nested PID", err)
	}
	rows := linuxFiniteExitLiveRows(t, leaf)
	for _, row := range rows {
		if row.PID == leaf && row.ParentPID != fixture.command.Process.Pid {
			t.Fatal("nested native row lost its actual parent")
		}
	}
	fixture.send(t, "exit-leaf")
	if fixture.receive(t) != "zombie" {
		t.Fatal("nested actual WNOWAIT missing")
	}
	linuxFiniteExitRequireRefusal(t, rows)
	fixture.send(t, "reap-leaf")
	if fixture.receive(t) != "reaped" {
		t.Fatal("nested genuine Wait/DA settlement missing")
	}
	linuxFiniteExitRequireRefusal(t, rows)
	fixture.send(t, "exit-root")
	if err := fixture.wait(); err != nil {
		t.Fatal("genuine outer Handle.Wait", err)
	}
	count, err := fixture.parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 {
		t.Fatal("outer native/DA settlement missing", count, err)
	}
}
