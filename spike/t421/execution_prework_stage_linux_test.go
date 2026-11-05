//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLinuxExecutorPreworkStageKeepsRefusalIdentity(t *testing.T) {
	staged := preworkStage("reference-build")
	if !errors.Is(staged, ErrExecutionLauncher) || staged.Error() != ErrExecutionLauncher.Error() {
		t.Fatal("staged refusal changed the launcher refusal")
	}
	joined := errors.Join(ErrExecutionLauncher, preworkStage("go-build-inputs"), preworkStage("inner-close"))
	if refusal, ok := firstPreworkRefusal(joined); !ok || refusal.stage != "go-build-inputs" {
		t.Fatal("first stage in join order not retained")
	}
	if _, ok := firstPreworkRefusal(ErrExecutionLauncher); ok {
		t.Fatal("unstaged refusal reported a stage")
	}
}

func TestLinuxExecutorPreworkRefusalLine(t *testing.T) {
	for _, test := range []struct {
		mode string
		err  error
		want string
	}{
		{executionLinuxPreworkInnerMode, preworkStage("reference-build"), "t422-prework-refusal: mode=inner stage=reference-build\n"},
		{executionLinuxPreworkOuterMode, &preworkRefusal{stage: "outer-inner-refused", inner: "measured-input-tree", exit: 1},
			"t422-prework-refusal: mode=outer stage=outer-inner-refused inner=measured-input-tree inner-exit=1\n"},
		{executionLinuxPreworkOuterMode, ErrExecutionLauncher, "t422-prework-refusal: mode=outer stage=unrecorded\n"},
		{"--forged mode", preworkStage("Bad Stage/../x"), "t422-prework-refusal: mode=unknown stage=unrecorded\n"},
		{executionLinuxPreworkOuterMode, &preworkRefusal{stage: "outer-start", inner: "spaced token", exit: 300}, "t422-prework-refusal: mode=outer stage=outer-start\n"},
	} {
		if got := preworkRefusalLine(test.mode, test.err); got != test.want || len(got) > maxPreworkRefusalLine {
			t.Fatalf("line %q, want %q", got, test.want)
		}
	}
	var output bytes.Buffer
	writePreworkRefusal(&output, executionLinuxPreworkInnerMode, nil)
	if output.Len() != 0 {
		t.Fatal("success wrote a refusal line")
	}
	longest := &preworkRefusal{stage: strings.Repeat("a", maxPreworkRefusalStage), inner: strings.Repeat("b", maxPreworkRefusalStage), exit: 255}
	writePreworkRefusal(&output, executionLinuxPreworkOuterMode, longest)
	if output.Len() == 0 || output.Len() > maxPreworkRefusalLine {
		t.Fatal("longest valid line not written within its bound", output.Len())
	}
}

func TestLinuxExecutorPreworkRelayedStage(t *testing.T) {
	for _, test := range []struct{ stderr, want string }{
		{"t422-prework-refusal: mode=inner stage=reference-build\nt422-execute: authenticated execution operation unavailable\n", "reference-build"},
		{"t422-execute: authenticated execution operation unavailable\n", ""},
		{"t422-prework-refusal: mode=outer stage=outer-start\n", ""},
		{"t422-prework-refusal: mode=inner stage=Bad/Token\n", ""},
		{"t422-prework-refusal: mode=inner stage=" + strings.Repeat("x", 200) + "\n", ""},
		{"", ""},
	} {
		if got := relayedPreworkStage(test.stderr); got != test.want {
			t.Fatalf("relayed %q from %q", got, test.stderr)
		}
	}
}

func TestLinuxExecutorPreworkActualRefusalsCarryStages(t *testing.T) {
	refusal, ok := firstPreworkRefusal(runLinuxExecutionPreworkStaged(t.Context(), time.Now(), []string{"t422-execute", executionLinuxPreworkOuterMode}, nil))
	if !ok || refusal.stage != "entry-args" {
		t.Fatal("entry refusal lost its stage")
	}
	args := []string{"t422-execute", executionLinuxPreworkInnerMode, "--selection-base64url", "AA"}
	refusal, ok = firstPreworkRefusal(runLinuxExecutionPreworkStaged(t.Context(), time.Now(), args, []string{executionLivenessEnvironment + "=1"}))
	if !ok || refusal.stage != "entry-environment" {
		t.Fatal("environment refusal lost its stage")
	}
	refusal, ok = firstPreworkRefusal(runLinuxExecutionPreworkStaged(t.Context(), time.Now(), args, nil))
	if !ok || refusal.stage != "entry-selection" {
		t.Fatal("selection refusal lost its stage")
	}
	inner := &linuxExecutorInnerPrework{observation: &executionPreworkObservation{}}
	if refusal, ok := firstPreworkRefusal(inner.prepare(nil, linuxExecutorPreworkSelection{}, nil, nil)); !ok || refusal.stage != "inner-entry" {
		t.Fatal("inner entry refusal lost its stage")
	}
	if refusal, ok := firstPreworkRefusal(inner.prepare(t.Context(), linuxExecutorPreworkSelection{}, nil, nil)); !ok || refusal.stage != "inner-reentry" {
		t.Fatal("inner reentry refusal lost its stage")
	}
	consumer := &linuxExecutorInnerPrework{observation: &executionPreworkObservation{}}
	if refusal, ok := firstPreworkRefusal(consumer.consume(t.Context(), linuxExecutorPreworkSelection{}, nil, nil)); !ok || refusal.stage != "consume-claim" {
		t.Fatal("consume refusal lost its stage")
	}
}

func TestLinuxExecutorPreworkOuterRelaysActualInnerChild(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", "printf 't422-prework-refusal: mode=inner stage=measured-input-tree\\nt422-execute: authenticated execution operation unavailable\\n' >&2; exit 1")
	stderr := checkoutCommandOutput{remaining: 1024, cancel: cancel}
	command.Stderr = &stderr
	if command.Start() != nil {
		t.Fatal("start inner stand-in")
	}
	_ = command.Wait()
	refusal, ok := firstPreworkRefusal(outerInnerRefusal("outer-inner-refused", command, &stderr))
	if !ok || refusal.stage != "outer-inner-refused" || refusal.inner != "measured-input-tree" || refusal.exit != 1 {
		t.Fatalf("relay lost actual inner facts: %#v", refusal)
	}
	if got := preworkRefusalLine(executionLinuxPreworkOuterMode, refusal); got != "t422-prework-refusal: mode=outer stage=outer-inner-refused inner=measured-input-tree inner-exit=1\n" {
		t.Fatalf("outer line %q", got)
	}
	unstarted, _ := firstPreworkRefusal(outerInnerRefusal("outer-inner-unretired", exec.Command("/bin/true"), &checkoutCommandOutput{}))
	if unstarted.exit != -1 || unstarted.inner != "" {
		t.Fatal("unstarted inner reported facts")
	}
}

// Not parallel: the wrapper writes to the process stderr, redirected here.
func TestLinuxExecutorPreworkCommandWritesOneLineOnlyOnRefusal(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal("pipe")
	}
	original := os.Stderr
	os.Stderr = write
	refused := runLinuxExecutionPreworkCommand(t.Context(), time.Now(), []string{"t422-execute", executionLinuxPreworkOuterMode}, nil)
	os.Stderr = original
	if write.Close() != nil {
		t.Fatal("close pipe writer")
	}
	output, err := io.ReadAll(read)
	if err != nil || read.Close() != nil {
		t.Fatal("read redirected stderr")
	}
	if !errors.Is(refused, ErrExecutionLauncher) || string(output) != "t422-prework-refusal: mode=outer stage=entry-args\n" {
		t.Fatalf("wrapper refusal %v wrote %q", refused, output)
	}
}
