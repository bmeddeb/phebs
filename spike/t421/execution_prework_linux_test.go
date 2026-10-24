//go:build linux

package t421

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func TestLinuxExecutorPreworkWireAndOrdinaryEntry(t *testing.T) {
	value := linuxExecutorPreworkSelection{Schema: executionLinuxPreworkSchema}
	encoded, err := encodeLinuxExecutorPrework(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLinuxExecutorPrework(encoded)
	if err != nil || decoded != value {
		t.Fatal("canonical wire", err)
	}
	raw, _ := json.Marshal(value)
	for _, input := range []string{"", encoded + "=", base64.RawURLEncoding.EncodeToString(append([]byte(" "), raw...)),
		base64.RawURLEncoding.EncodeToString(append(raw[:len(raw)-1], []byte(",\"ready\":true}")...)),
		base64.RawURLEncoding.EncodeToString(append(raw, raw...)), strings.Repeat("A", maxExecutionSelectionCharacters+1)} {
		if _, err := decodeLinuxExecutorPrework(input); err == nil {
			t.Fatal("noncanonical/unbounded/unknown wire accepted")
		}
	}
	for _, mode := range []string{executionOuterMode, executionInnerMode, executionLinuxPreworkOuterMode, executionLinuxPreworkInnerMode} {
		if err := RunExecutionCommand(t.Context(), []string{"executor", mode, "--selection-base64url", encoded}, nil); !errors.Is(err, ErrExecutionLauncher) {
			t.Fatal("ordinary formal or unbound prework entry opened", mode, err)
		}
	}
}

func TestLinuxExecutorPreworkRetirementReceiptRefusals(t *testing.T) {
	attempt := strings.Repeat("a", 32)
	for _, failed := range []bool{false, true} {
		if !linuxExecutorPreworkRetired(linuxExecutorPreworkReceipt(attempt, failed), attempt) {
			t.Fatal("fixed terminal receipt refused")
		}
	}
	success := linuxExecutorPreworkReceipt(attempt, false)
	for _, receipt := range []string{"", executionLinuxPreworkComplete, success[:len(success)-1], success + success,
		linuxExecutorPreworkReceipt(strings.Repeat("b", 32), false), " " + success, success + linuxExecutorPreworkReceipt(attempt, true)} {
		if linuxExecutorPreworkRetired(receipt, attempt) {
			t.Fatal("missing/partial/stale/duplicate/out-of-order receipt accepted")
		}
	}
}

func TestLinuxExecutorPreworkAttemptFailuresStaySpent(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled, t.Context()} {
		owner := newLinuxExecutorPrework(nil, nil, linuxExecutorPreworkSelection{})
		if owner.Run(ctx) == nil || owner.Run(t.Context()) == nil || owner.Close(t.Context()) == nil || !owner.released {
			t.Fatal("missing/canceled source lineage was reusable")
		}
		inner := &linuxExecutorInnerPrework{}
		if inner.prepare(ctx, linuxExecutorPreworkSelection{}, nil, nil) == nil ||
			inner.prepare(t.Context(), linuxExecutorPreworkSelection{}, nil, nil) == nil || !inner.attempted.Load() {
			t.Fatal("inner attempt reset after failure")
		}
		if inner.consume(ctx, linuxExecutorPreworkSelection{}, nil, nil) == nil || inner.consume(t.Context(), linuxExecutorPreworkSelection{}, nil, nil) == nil || !inner.consumed.Load() {
			t.Fatal("failed private binding consumption reset")
		}
		if inner.Close(t.Context()) != nil || !inner.retired() {
			t.Fatal("empty failed owner did not retire")
		}
	}
	// These explicitly modeled claims reach the source-owner refusal branches;
	// they do not fabricate successful protected provenance.
	for _, builds := range []*LinuxExecutionGoBuildCustody{nil, {released: true}, {closing: true}, {}} {
		tool := &LinuxExecutionToolCustody{}
		tool.executorClaim = &linuxExecutorPreworkClaim{tool: tool, builds: builds}
		owner := newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
		if owner.Run(t.Context()) == nil || owner.Close(t.Context()) == nil {
			t.Fatal("foreign/closed/missing source owner accepted")
		}
	}
}

func TestLinuxExecutorPreworkSelectionRefusals(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0o700) != nil {
		t.Fatal("private selection root")
	}
	value := linuxExecutorPreworkSelection{Schema: executionLinuxPreworkSchema, PreparationRoot: root, RepositoryRoot: t.TempDir(), GoRoot: t.TempDir(),
		ModuleCache: t.TempDir(), GitBinary: root, InputHelperBinary: root, ReferenceHelperBinary: root, PlanSourceCommit: strings.Repeat("a", 40),
		IntegratedMainCommit: strings.Repeat("b", 40), SourceCommit: strings.Repeat("c", 40), ParentPID: os.Getpid(), ParentStart: "1", ReadInode: 1,
		DeadlineUnixNano: time.Now().Add(time.Minute).UnixNano(), Attempt: strings.Repeat("a", 32),
		ExpectedInput:    linuxExecutorPreworkInput{ManifestSHA256: "sha256:" + strings.Repeat("a", 64), HelperSHA256: "sha256:" + strings.Repeat("b", 64), Entries: 1, Files: 1, Bytes: 1},
		ExpectedGoSHA256: "sha256:" + strings.Repeat("c", 64)}
	if !validLinuxExecutorPrework(value, time.Now()) {
		info, err := os.Lstat(root)
		t.Fatal("selection shape control", linuxGitPrivateDirectory(root), info.Mode(), err)
	}
	for name, mutate := range map[string]func(*linuxExecutorPreworkSelection){
		"old-schema": func(v *linuxExecutorPreworkSelection) { v.Schema = executionSelectionSchema },
		"expired":    func(v *linuxExecutorPreworkSelection) { v.DeadlineUnixNano = time.Now().Add(-time.Second).UnixNano() },
		"extended": func(v *linuxExecutorPreworkSelection) {
			v.DeadlineUnixNano = time.Now().Add(executionLinuxPreworkMaximum + time.Minute).UnixNano()
		},
		"missing-birth":        func(v *linuxExecutorPreworkSelection) { v.ParentStart = "" },
		"wrong-birth-shape":    func(v *linuxExecutorPreworkSelection) { v.ParentStart = "01" },
		"missing-pipe":         func(v *linuxExecutorPreworkSelection) { v.ReadInode = 0 },
		"missing-attempt":      func(v *linuxExecutorPreworkSelection) { v.Attempt = "" },
		"relative-root":        func(v *linuxExecutorPreworkSelection) { v.PreparationRoot = "." },
		"repository-overlap":   func(v *linuxExecutorPreworkSelection) { v.PreparationRoot = v.RepositoryRoot },
		"sdk-overlap":          func(v *linuxExecutorPreworkSelection) { v.PreparationRoot = v.GoRoot },
		"modules-overlap":      func(v *linuxExecutorPreworkSelection) { v.PreparationRoot = v.ModuleCache },
		"preparation-ancestor": func(v *linuxExecutorPreworkSelection) { v.RepositoryRoot = filepath.Dir(v.PreparationRoot) },
		"missing-helper":       func(v *linuxExecutorPreworkSelection) { v.InputHelperBinary = filepath.Join(root, "absent") },
		"invalid-source":       func(v *linuxExecutorPreworkSelection) { v.SourceCommit = "main" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := value
			mutate(&changed)
			if validLinuxExecutorPrework(changed, time.Now()) {
				t.Fatal("invalid selection accepted")
			}
		})
	}
}

func TestLinuxExecutorPreworkDeadlineClipping(t *testing.T) {
	for _, duration := range []time.Duration{time.Minute, 2 * executionLinuxPreworkMaximum} {
		t.Run(duration.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), duration)
			defer cancel()
			builds, tool := &LinuxExecutionGoBuildCustody{}, &LinuxExecutionToolCustody{}
			tool.executorClaim = &linuxExecutorPreworkClaim{tool: tool, builds: builds} // Modeled claim; no protected issuance.
			owner := newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
			before := time.Now()
			if owner.Run(ctx) == nil {
				t.Fatal("missing actual custody accepted")
			}
			after := time.Now()
			selected := time.Unix(0, owner.selection.DeadlineUnixNano)
			bound, _ := ctx.Deadline()
			if selected.After(bound) || selected.After(after.Add(executionLinuxPreworkMaximum)) || selected.Before(before) ||
				duration < executionLinuxPreworkMaximum && !selected.Equal(bound) ||
				duration > executionLinuxPreworkMaximum && selected.Before(before.Add(executionLinuxPreworkMaximum)) {
				t.Fatal("source work deadline was extended or not clipped")
			}
			if owner.Close(t.Context()) == nil {
				t.Fatal("shape-only deadline check issued success")
			}
		})
	}
}

func TestLinuxExecutorPreworkCloseAfterDeadlineRemainsFailed(t *testing.T) {
	root := exec.CommandContext(t.Context(), "/usr/bin/true")
	root.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if root.Start() != nil || root.Wait() != nil || root.ProcessState == nil {
		t.Fatal("real root Wait fixture")
	}
	caller, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	deadline, _ := caller.Deadline()
	owner := newLinuxExecutorPrework(nil, nil, linuxExecutorPreworkSelection{DeadlineUnixNano: deadline.UnixNano()})
	owner.used.Store(true)
	owner.command, owner.retired, owner.caller = root, true, caller
	close(owner.result)
	barrier := make(chan struct{})
	go func() { <-owner.release; <-barrier; close(owner.joined) }()
	settlement, settleCancel := context.WithTimeout(t.Context(), time.Second)
	defer settleCancel()
	result := make(chan error, 1)
	go func() { result <- owner.Close(settlement) }()
	<-owner.release
	select {
	case <-result:
		close(barrier)
		t.Fatal("modeled loan join was not awaited")
	default:
	}
	<-caller.Done()
	close(barrier)
	if err := <-result; err == nil || !owner.released {
		t.Fatal("late joined cleanup renewed the original work deadline")
	}
}

func TestLinuxExecutorPreworkFailureNeedsAffirmativeRetirement(t *testing.T) {
	// Real child lifetimes, with a modeled result/loan barrier only. This does
	// not manufacture protected executor provenance or an inner cleanup receipt.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	read, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = writer.Close() }()
	helper := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxExecutorPreworkKernelHelper$")
	helper.Env = []string{"PHEBS_EXECUTOR_KERNEL_HELPER=retained-leaf", "GORACE=atexit_sleep_ms=0"}
	helper.Stdin = read
	helper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if helper.Start() != nil {
		t.Fatal("separate-session helper Start")
	}
	helperJoined := false
	defer func() {
		if !helperJoined {
			_, _ = writer.Write([]byte{1})
			_ = helper.Wait()
		}
	}()
	root := exec.CommandContext(ctx, "/usr/bin/false")
	root.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if root.Start() != nil {
		t.Fatal("ordinary failed root Start")
	}
	if err := root.Wait(); err == nil || root.ProcessState == nil {
		t.Fatal("ordinary failed root actual Wait")
	}
	members, err := t4013.PrivateProcessSessionMembership(root.Process.Pid)
	helperMembers, helperErr := t4013.PrivateProcessSessionMembership(helper.Process.Pid)
	if err != nil || len(members) != 0 || helperErr != nil || len(helperMembers) != 1 {
		t.Fatal("empty root SID/live separate helper discriminator")
	}
	owner := newLinuxExecutorPrework(nil, nil, linuxExecutorPreworkSelection{})
	owner.used.Store(true)
	owner.command, owner.resultErr = root, ErrExecutionLauncher
	close(owner.result)
	close(owner.joined) // Modeled loan completion; no actual image/source loan.
	if owner.Close(ctx) == nil || owner.released {
		t.Fatal("root Wait/empty SID released unknown separate-session custody")
	}
	select {
	case <-owner.release:
		t.Fatal("unknown retirement released the source loan")
	default:
	}
	if _, err := writer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if helper.Wait() != nil || helper.ProcessState == nil || !helper.ProcessState.Success() {
		t.Fatal("same original helper actual Wait")
	}
	helperJoined = true
	if owner.Close(ctx) == nil || owner.released {
		t.Fatal("later empty sessions invented missing affirmative source receipt")
	}
	// Separately model the receipt predicate after real joined failure. Cleanup
	// can release known custody; the sticky failure cannot become success.
	known := newLinuxExecutorPrework(nil, nil, linuxExecutorPreworkSelection{})
	known.used.Store(true)
	known.command, known.resultErr, known.retired = root, ErrExecutionLauncher, true
	close(known.result)
	close(known.joined)
	var group sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() { defer group.Done(); results <- known.Close(ctx) }()
	}
	group.Wait()
	close(results)
	for result := range results {
		if result == nil {
			t.Fatal("joined failure was promoted to success")
		}
	}
	if !known.released {
		t.Fatal("known retirement did not release modeled loan")
	}
}

// This runs a sealed test image, not t422-execute or a protected reference
// build. It qualifies actual same-image parent/child/pipe mechanics only.
func TestLinuxExecutorPreworkRealKernelChain(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	imageOwner, err := protectLinuxExecutionTool(t.Context(), "t422-execute", executable)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = imageOwner.Close() })
	for _, mode := range []string{"close", "eof", "byte", "deadline", "cancel", "wrong-parent", "wrong-pipe", "wrong-image", "writable-pipe"} {
		t.Run(mode, func(t *testing.T) {
			err := imageOwner.input.WithInput(t.Context(), "t422-execute", func(image *os.File) error {
				read, writer, err := os.Pipe()
				if err != nil {
					return err
				}
				defer func() { _ = read.Close(); _ = writer.Close() }()
				selection := linuxExecutorPreworkSelection{Schema: executionLinuxPreworkSchema, DeadlineUnixNano: time.Now().Add(8 * time.Second).UnixNano()}
				selection, err = bindLinuxExecutorPrework(t.Context(), selection, read)
				if err != nil {
					return err
				}
				encoded, err := encodeLinuxExecutorPrework(selection)
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, "/proc/self/fd/3", "-test.run=^TestLinuxExecutorPreworkKernelHelper$")
				command.ExtraFiles = []*os.File{image, read}
				command.Env = []string{"PHEBS_EXECUTOR_KERNEL_HELPER=outer", "PHEBS_EXECUTOR_KERNEL_MODE=" + mode, "PHEBS_EXECUTOR_KERNEL_WIRE=" + encoded, "GORACE=atexit_sleep_ms=0"}
				command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				if command.Start() != nil {
					return ErrExecutionLauncher
				}
				_ = read.Close()
				waitErr := command.Wait() // Sole actual Wait on every successful Start.
				members, membershipErr := t4013.PrivateProcessSessionMembership(command.Process.Pid)
				if waitErr != nil || command.ProcessState == nil || !command.ProcessState.Success() || membershipErr != nil || len(members) != 0 ||
					stdout.String() != "kernel-chain-joined\nPASS\n" || stderr.Len() != 0 {
					t.Errorf("mechanical chain failed: Wait=%v stdout=%q stderr=%q members=%d", waitErr, stdout.String(), stderr.String(), len(members))
					return ErrExecutionLauncher
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLinuxExecutorPreworkKernelHelper(t *testing.T) {
	kind := os.Getenv("PHEBS_EXECUTOR_KERNEL_HELPER")
	if kind == "" {
		return
	}
	if kind == "retained-leaf" {
		var one [1]byte
		if _, err := io.ReadFull(os.Stdin, one[:]); err != nil || one[0] != 1 {
			t.Fatal("owned natural-exit barrier", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	value, err := decodeLinuxExecutorPrework(os.Getenv("PHEBS_EXECUTOR_KERNEL_WIRE"))
	if err != nil {
		t.Fatal(err)
	}
	unix.CloseOnExec(3)
	unix.CloseOnExec(4)
	if unix.SetNonblock(4, true) != nil {
		t.Fatal("inherited pipe nonblocking")
	}
	image, read := os.NewFile(3, "kernel-image"), os.NewFile(4, "kernel-parent")
	defer func() { _ = image.Close(); _ = read.Close() }()
	parent, err := adoptLinuxExecutorParent(ctx, value, image, read, kind == "inner")
	mode := os.Getenv("PHEBS_EXECUTOR_KERNEL_MODE")
	if kind == "inner" && strings.HasPrefix(mode, "wrong-") || kind == "inner" && mode == "writable-pipe" {
		if err == nil {
			_ = parent.Close()
			t.Fatal("altered native boundary accepted")
		}
		_, _ = os.Stdout.WriteString("refused\n")
		return
	}
	if err != nil {
		t.Fatal("actual parent adoption", err)
	}
	defer func() {
		if err := parent.Close(); err != nil && kind != "inner" {
			t.Error(err)
		}
	}()
	if kind == "outer" {
		runLinuxExecutorKernelInner(t, ctx, mode, value, image)
		_, _ = os.Stdout.WriteString("kernel-chain-joined\n")
		return
	}
	_, _ = os.Stdout.WriteString("alive\n")
	if mode == "cancel" {
		cancel()
		if parent.alive.Err() == nil {
			t.Fatal("caller cancellation did not cancel derived liveness")
		}
	} else if mode != "close" {
		select {
		case <-parent.alive.Done():
		case <-ctx.Done():
			t.Fatal("liveness endpoint did not settle")
		}
	}
	if mode != "close" {
		if parent.check(ctx) == nil {
			t.Fatal("lost liveness remained valid")
		}
	}
	var group sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() { defer group.Done(); results <- parent.Close() }()
	}
	group.Wait()
	close(results)
	for result := range results {
		if mode == "close" && result != nil || mode != "close" && mode != "cancel" && result == nil {
			t.Fatal("concurrent/repeated watcher close lost result", result)
		}
	}
	_, _ = os.Stdout.WriteString("joined\n")
}

func runLinuxExecutorKernelInner(t *testing.T, ctx context.Context, mode string, value linuxExecutorPreworkSelection, image *os.File) {
	t.Helper()
	read, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = writer.Close() }()
	value, err = bindLinuxExecutorPrework(ctx, value, read)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "wrong-parent" {
		value.ParentStart = "1"
	}
	if mode == "wrong-pipe" {
		value.ReadInode++
	}
	if mode == "deadline" {
		value.DeadlineUnixNano = time.Now().Add(time.Second).UnixNano()
	}
	encoded, err := encodeLinuxExecutorPrework(value)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", "-test.run=^TestLinuxExecutorPreworkKernelHelper$")
	command.ExtraFiles = []*os.File{image, read}
	var wrongImage *os.File
	if mode == "wrong-image" {
		wrongImage, err = os.Open("/usr/bin/true")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = wrongImage.Close() }()
		command.ExtraFiles[0] = wrongImage
		command.ExtraFiles = append(command.ExtraFiles, image)
		command.Path = "/proc/self/fd/5"
	}
	if mode == "writable-pipe" {
		command.ExtraFiles[1] = writer
	}
	command.Env = []string{"PHEBS_EXECUTOR_KERNEL_HELPER=inner", "PHEBS_EXECUTOR_KERNEL_MODE=" + mode, "PHEBS_EXECUTOR_KERNEL_WIRE=" + encoded, "GORACE=atexit_sleep_ms=0"}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if command.Start() != nil {
		_ = stdout.Close()
		t.Fatal("mechanical inner Start")
	}
	_ = read.Close()
	reader := bufio.NewReader(io.LimitReader(stdout, 128))
	first, firstErr := reader.ReadString('\n')
	switch mode {
	case "eof":
		_ = writer.Close()
	case "byte":
		_, _ = writer.Write([]byte{1})
	}
	tail, tailErr := io.ReadAll(reader)
	waitErr := command.Wait() // No Fatal path between successful Start and Wait.
	if firstErr != nil || tailErr != nil || waitErr != nil || command.ProcessState == nil || !command.ProcessState.Success() || stderr.Len() != 0 {
		t.Fatalf("mechanical inner join: first=%q tail=%q read=%v/%v wait=%v stderr=%q", first, tail, firstErr, tailErr, waitErr, stderr.String())
	}
	expected := "alive\njoined\nPASS\n"
	if strings.HasPrefix(mode, "wrong-") || mode == "writable-pipe" {
		expected = "refused\nPASS\n"
	}
	if first+string(tail) != expected {
		t.Fatalf("unexpected mechanical control: %q", first+string(tail))
	}
}
