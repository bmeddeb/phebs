//go:build linux

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

// Fixed subprocess mechanics only. Neither this own test image nor the host
// Git invocation issues executable, resource, or operational admission.
const linuxEpochTraceFixtureEnvironment = "PHEBS_PRIVATE_EPOCH_TRACE_FIXTURE"
const linuxEpochTraceFixtureSelector = "^TestLinuxEpochTraceFixtureChild$"
const linuxEpochTraceFixturePreACK = "TRACE_FIXTURE_PRE_ACK "
const linuxEpochTraceFixtureFinal = "TRACE_FIXTURE "

type linuxEpochTraceFixtureReport struct {
	Mode              string
	PID               int
	ChildPID          int
	WaitCode          int
	Ordinal           uint64
	Active            uint32
	Tasks             int
	AckConsumed       bool
	MaintenancePacked bool
}

func TestLinuxEpochTraceFixtureChild(t *testing.T) {
	mode := os.Getenv(linuxEpochTraceFixtureEnvironment)
	if mode == "" {
		return
	}
	code, err := runLinuxEpochTraceFixture(mode)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fixed trace fixture refused:", err)
		os.Exit(125)
	}
	os.Exit(code)
}

func runLinuxEpochTraceFixture(mode string) (int, error) {
	if mode != "go-parent" && mode != "go-leaf" && mode != "git-parent" && mode != "git-maintenance-parent" && mode != "capture-live" && mode != "engine-parent" && mode != "engine-exit-parent" && mode != "engine-burst-parent" && mode != "engine-leaf" {
		return 125, errors.New("unknown fixed mode")
	}
	engineParent := mode == "engine-parent" || mode == "engine-exit-parent" || mode == "engine-burst-parent"
	tasks, release, err := linuxEpochTraceFixtureTasks()
	if err != nil {
		return 125, err
	}
	defer release()
	if mode == "go-leaf" {
		return 17, nil
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, unix.SIGTRAP, unix.SIGUSR1, unix.SIGUSR2)
	defer signal.Stop(signals)
	if mode == "engine-leaf" {
		var continued chan os.Signal
		if os.Getenv("PHEBS_PRIVATE_EPOCH_ENGINE_BURST") == "1" {
			continued = make(chan os.Signal, 1)
			signal.Notify(continued, unix.SIGCONT)
			defer signal.Stop(continued)
		}
		if err := linuxEpochTraceFixtureWrite("TRACE_ENGINE_CHILD_READY ", linuxEpochTraceFixtureReport{Mode: mode, PID: os.Getpid(), Tasks: tasks}); err != nil {
			return 125, err
		}
		if continued != nil {
			if err := linuxEpochTraceFixtureSignal(continued, unix.SIGCONT); err != nil {
				return 125, err
			}
			ready, release := make(chan struct{}, 8), make(chan struct{})
			var joined sync.WaitGroup
			for range 8 {
				joined.Add(1)
				go func() {
					defer joined.Done()
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					ready <- struct{}{}
					<-release
				}()
			}
			defer func() { close(release); joined.Wait() }()
			for range 8 {
				<-ready
			}
			if err := linuxEpochTraceFixtureWrite("TRACE_ENGINE_BURST_READY ", linuxEpochTraceFixtureReport{Mode: mode, PID: os.Getpid(), Tasks: 8}); err != nil {
				return 125, err
			}
		}
		if err := linuxEpochTraceFixtureSignal(signals, unix.SIGUSR2); err != nil {
			return 125, err
		}
		return 17, nil
	}
	for _, expected := range []unix.Signal{unix.SIGTRAP, unix.SIGUSR1} {
		if err := unix.Kill(os.Getpid(), expected); err != nil {
			return 125, errors.New("self signal failed")
		}
		if err := linuxEpochTraceFixtureSignal(signals, expected); err != nil {
			return 125, err
		}
	}
	if mode == "capture-live" {
		report := linuxEpochTraceFixtureReport{Mode: mode, PID: os.Getpid(), Tasks: tasks}
		return 0, linuxEpochTraceFixtureACK(signals, report)
	}
	// Settlement remains alive across the real native Wait and local receiver
	// close. The controller's SIGUSR2 acknowledges original-parent reap later.
	settlement, cancel := context.WithCancel(context.Background())
	defer cancel()
	controller, err := dispatchadmission.New(settlement, dispatchadmission.Config{
		Limits:    dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1, Attempts: 1, WireBytes: 16 << 10, AckTimeout: 2 * time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{{ID: 1, Role: 1}}}},
		Phases:    []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{{Role: 1, Attempts: 1}}}},
	})
	if err != nil {
		return 125, errors.New("local controller unavailable")
	}
	parent, err := controller.NewLocalProducer(settlement, 1)
	if err != nil {
		return 125, errors.New("local producer unavailable")
	}
	closed := false
	defer func() {
		if !closed {
			closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelClose()
			_ = parent.Close(closeCtx)
		}
	}()
	command := exec.Command(os.Args[0], "-test.run="+linuxEpochTraceFixtureSelector, "-test.timeout=15s")
	command.Env = []string{linuxEpochTraceFixtureEnvironment + "=go-leaf", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	if engineParent {
		command = exec.Command(os.Getenv("PHEBS_PRIVATE_EPOCH_ENGINE_IMAGE"), "-test.run="+linuxEpochTraceFixtureSelector, "-test.timeout=15s")
		command.Env = []string{linuxEpochTraceFixtureEnvironment + "=engine-leaf", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
		if mode == "engine-burst-parent" {
			command.Env = append(command.Env, "PHEBS_PRIVATE_EPOCH_ENGINE_BURST=1")
		}
	}
	wantCode := 17
	if mode == "engine-exit-parent" {
		wantCode = -1
	}
	if mode == "git-parent" {
		command = exec.Command("/usr/bin/git", "--version")
		command.Env = []string{"LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		wantCode = 0
	}
	if mode == "git-maintenance-parent" {
		repository := os.Getenv("PHEBS_PRIVATE_EPOCH_TRACE_GIT_REPO")
		if !executionGitAbsolutePath(repository) {
			return 125, errors.New("fixed maintenance repository unavailable")
		}
		command = exec.Command("/usr/lib/git-core/git", "-C", repository, "-c", "maintenance.loose-objects.enabled=true", "-c", "maintenance.loose-objects.auto=1", "fetch", "--prune", "origin")
		command.Env = linuxEpochForegroundGitEnvironment("/usr/lib/git-core", repository, repository)
		command.Env = append(command.Env, "GIT_TRACE2_EVENT="+filepath.Join(repository, "maintenance-trace.jsonl"))
		wantCode = 0
	}
	command.Dir = "/"
	command.Stdout, command.Stderr = io.Discard, os.Stderr
	if engineParent {
		command.Stdout = os.Stdout
	}
	handle, err := parent.StartInPhase(settlement, 1, dispatchadmission.Site{ID: 1, Role: 1}, command)
	if err != nil {
		return 125, errors.New("fixed child start refused")
	}
	if engineParent {
		raw, encodeErr := json.Marshal(linuxEpochTraceFixtureReport{Mode: mode, PID: os.Getpid(), ChildPID: command.Process.Pid, Tasks: tasks})
		if encodeErr == nil {
			_, _ = fmt.Fprintln(os.Stdout, "TRACE_ENGINE_READY "+string(raw))
		}
	}
	// Nothing fallible intervenes after successful Start: this actual parent
	// always invokes its one genuine Handle.Wait, including nonzero exit17.
	waitErr := handle.Wait()
	if command.ProcessState == nil || command.ProcessState.ExitCode() != wantCode {
		return 125, errors.New("fixed child native exit mismatch")
	}
	var exit *exec.ExitError
	if wantCode == 0 && waitErr != nil || wantCode == 17 && (!errors.As(waitErr, &exit) || exit.ExitCode() != 17) {
		return 125, errors.New("fixed child genuine wait mismatch")
	}
	if wantCode == -1 {
		status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
		var killed *exec.ExitError
		if !errors.As(waitErr, &killed) || !ok || !status.Signaled() || status.Signal() != unix.SIGKILL {
			return 125, errors.New("fixed child genuine killed Wait mismatch")
		}
	}
	count, err := parent.Count()
	if err != nil || count.Ordinal != 1 || count.Active != 0 {
		return 125, errors.New("fixed child DA settlement mismatch")
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	err = parent.Close(closeCtx)
	cancelClose()
	if err != nil {
		return 125, errors.New("local producer close refused")
	}
	closed = true
	report := linuxEpochTraceFixtureReport{Mode: mode, PID: os.Getpid(), ChildPID: command.Process.Pid, WaitCode: wantCode, Ordinal: count.Ordinal, Active: uint32(count.Active), Tasks: tasks}
	if mode == "git-maintenance-parent" {
		packs, err := filepath.Glob(filepath.Join(os.Getenv("PHEBS_PRIVATE_EPOCH_TRACE_GIT_REPO"), "objects", "pack", "loose-*.pack"))
		if err != nil || len(packs) != 1 {
			return 125, errors.New("automatic maintenance produced no exact loose-object pack")
		}
		report.MaintenancePacked = true
		raw, err := linuxEpochTraceRead(filepath.Join(os.Getenv("PHEBS_PRIVATE_EPOCH_TRACE_GIT_REPO"), "maintenance-trace.jsonl"), 64<<10)
		if err != nil {
			return 125, errors.New("bounded automatic maintenance trace unavailable")
		}
		foreground := false
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var event struct {
				Event string
				Argv  []string
			}
			if json.Unmarshal([]byte(line), &event) != nil {
				return 125, errors.New("malformed fixed Git trace")
			}
			if event.Event == "child_start" && slices.Equal(event.Argv, []string{"git", "maintenance", "run", "--auto", "--no-quiet", "--no-detach"}) {
				foreground = true
			}
		}
		if !foreground {
			return 125, errors.New("fetch did not select foreground automatic maintenance")
		}
	}
	return 0, linuxEpochTraceFixtureACK(signals, report)
}

func linuxEpochTraceFixtureACK(signals <-chan os.Signal, report linuxEpochTraceFixtureReport) error {
	if err := linuxEpochTraceFixtureWrite(linuxEpochTraceFixturePreACK, report); err != nil {
		return err
	}
	if err := linuxEpochTraceFixtureSignal(signals, unix.SIGUSR2); err != nil {
		return err
	}
	report.AckConsumed = true
	return linuxEpochTraceFixtureWrite(linuxEpochTraceFixtureFinal, report)
}

func linuxEpochTraceFixtureSignal(signals <-chan os.Signal, expected unix.Signal) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case observed := <-signals:
		if observed != expected {
			return errors.New("unexpected delivered signal")
		}
		return nil
	case <-timer.C:
		return errors.New("fixed signal deadline expired")
	}
}

func linuxEpochTraceFixtureWrite(prefix string, report linuxEpochTraceFixtureReport) error {
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > 512 {
		return errors.New("fixed report encoding refused")
	}
	raw = append(append([]byte(prefix), raw...), '\n')
	n, err := os.Stdout.Write(raw)
	if err != nil || n != len(raw) {
		return errors.New("fixed report write refused")
	}
	return nil
}

func linuxEpochTraceFixtureTasks() (int, func(), error) {
	ready, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(joined)
		close(ready)
		<-release
	}()
	<-ready
	var once sync.Once
	cleanup := func() { once.Do(func() { close(release); <-joined }) }
	directory, err := os.Open("/proc/self/task")
	if err != nil {
		cleanup()
		return 0, nil, errors.New("fixed task directory unavailable")
	}
	names, readErr := directory.Readdirnames(65)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(names) < 2 || len(names) > 64 {
		cleanup()
		return 0, nil, errors.New("fixed task census refused")
	}
	return len(names), cleanup, nil
}
