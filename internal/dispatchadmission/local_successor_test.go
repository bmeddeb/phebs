//go:build darwin || linux

package dispatchadmission

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestTerminalSuccessorChildHelper(t *testing.T) {
	if os.Getenv("DISPATCH_TERMINAL_SUCCESSOR_HELPER") != "1" {
		return
	}
	config := testConfig()
	client, err := NewClient(t.Context(), os.NewFile(3, "terminal-successor-test"), config.Producers[0], 1, config.Limits)
	if err != nil || client.Pause(t.Context()) != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	var next [1]byte
	if _, err := io.ReadFull(os.Stdin, next[:]); err != nil || client.Checkpoint(t.Context()) != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "checkpoint"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadFull(os.Stdin, next[:]) // Parent owns the deliberate kill.
}

// Real inherited DA socket, owned Start/kill/Wait and reducer retirement, not
// a Phebs/Surreal session, terminal SDK checkpoint or SA composition proof.
func localSuccessorFixture(t *testing.T) (*Controller, *LocalProducer, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	config := testConfig()
	config.Limits.Producers, config.Limits.Sites = 3, 6
	for _, id := range []uint32{2, 3} {
		config.Producers = append(config.Producers, Producer{ID: id, Binding: [32]byte{byte(id)}, Sites: config.Producers[0].Sites})
	}
	controller, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	local, err := controller.NewLocalProducer(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTerminalSuccessorChildHelper$")
	command.Env = []string{"DISPATCH_TERMINAL_SUCCESSOR_HELPER=1", "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = []*os.File{child}
	command.Stderr, command.WaitDelay = os.Stderr, time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := local.Start(ctx, 1, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = handle.Wait()
		}
	})
	_ = child.Close()
	done := make(chan error, 1)
	go func() { done <- controller.Serve(ctx, 1, command.Process.Pid, parent) }()
	reader := bufio.NewReader(output)
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal("native helper not ready", line, err)
	}
	if local.Pause(ctx) != nil || controller.Fence() != nil {
		t.Fatal("parent fence failed")
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "checkpoint\n" {
		t.Fatal("native checkpoint not joined", line, err)
	}
	if controller.ExpectHardDeath(1) != nil || command.Process.Kill() != nil || handle.Wait() == nil {
		t.Fatal("owned hard death did not occur")
	}
	if err := <-done; err != nil || controller.CloseHardDeath(1, command.ProcessState) != nil || local.Checkpoint(ctx) != nil {
		t.Fatal("native Wait/EOF retirement failed", err)
	}
	return controller, local, ctx
}

func TestLocalSuccessorSamePhasePreservesAccounting(t *testing.T) {
	controller, local, ctx := localSuccessorFixture(t)
	before, _ := controller.Snapshot()
	if err := local.ReopenAfterHardDeath(ctx, 1, 2, 1); err != nil {
		t.Fatal(err)
	}
	after, err := controller.Snapshot()
	if err != nil || before.Attempts != after.Attempts || before.Digest != after.Digest ||
		before.ReservedWireBytes != after.ReservedWireBytes || !reflect.DeepEqual(before.Phases, after.Phases) ||
		before.Producers[0] != after.Producers[0] || after.Producers[2].Checkpoint != 0 ||
		after.Producers[2].Ordinal != before.Producers[2].Ordinal {
		t.Fatal("same-phase cutover changed accepted accounting", before, after, err)
	}
	if launch, err := controller.ProducerLaunch(2); err != nil || launch.Phase != 1 {
		t.Fatal("successor did not retain phase", launch, err)
	}
	handle, err := local.StartInPhase(ctx, 1, Site{ID: 1, Role: 1}, exec.Command("/usr/bin/true"))
	if err != nil || handle.Wait() != nil {
		t.Fatal("actual successor launch admission failed", err)
	}
	after, err = controller.Snapshot()
	if err != nil || after.Attempts != before.Attempts+1 || after.Producers[2].Ordinal != before.Producers[2].Ordinal+1 || after.Phases[0].Roles[0].Attempts != 2 {
		t.Fatal("same phase did not accumulate native attempts", after, err)
	}
}

func TestLocalSuccessorRefusesInvalidOrRepeatedCutover(t *testing.T) {
	for _, mode := range []string{"phase", "identity", "unknown", "replay", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			controller, local, ctx := localSuccessorFixture(t)
			prior, next, phase := uint32(1), uint32(2), uint32(1)
			switch mode {
			case "phase":
				phase = 2
			case "identity":
				prior = 3
			case "unknown":
				next = 9
			case "replay":
				if local.ReopenAfterHardDeath(ctx, 1, 2, 1) != nil || local.Pause(ctx) != nil || controller.Fence() != nil || local.Checkpoint(ctx) != nil {
					t.Fatal("first cutover or re-fence failed")
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, _ := controller.Snapshot()
			if err := local.ReopenAfterHardDeath(ctx, prior, next, phase); err == nil {
				t.Fatal("invalid cutover reopened")
			}
			after, err := controller.Snapshot()
			if err == nil || after.Attempts != before.Attempts || after.Digest != before.Digest || !reflect.DeepEqual(after.Producers, before.Producers) {
				t.Fatal("refusal lost accepted prefix", after, err)
			}
		})
	}
}
