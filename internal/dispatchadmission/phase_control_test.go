//go:build darwin || linux

package dispatchadmission

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

func phaseTestConfig() PhaseControlConfig {
	return PhaseControlConfig{
		Phases: []uint32{1, 2}, InitialPhase: 1, MaximumPhases: 2,
		MaximumWireBytes: 64 << 10, Timeout: time.Second,
	}
}

func phaseTestPair(t *testing.T, client *Client) (*PhaseControl, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	parent, child, err := NewPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	control, err := NewPhaseControl(ctx, parent, client.binding, phaseTestConfig())
	if err != nil {
		cancel()
		_ = child.Close()
		t.Fatal(err)
	}
	done, err := StartPhaseControl(ctx, child, client, phaseTestConfig())
	if err != nil {
		cancel()
		_ = control.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = control.Close()
		_ = phaseTestResult(t, done)
	})
	return control, done
}

func phaseTestResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("phase-control operation did not terminate")
		return nil
	}
}

func TestPhaseControlPauseAllBeforeAdvance(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	config := testConfig()
	config.Limits.Producers, config.Limits.Sites = 2, 4
	config.Producers = append(config.Producers, Producer{
		ID: 2, Binding: [32]byte{2}, Sites: []Site{{ID: 1, Role: 1}, {ID: 2, Role: 2, Persistent: true}},
	})
	controller, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	var clients []*Client
	var controls []*PhaseControl
	var servers []chan error
	for _, producer := range config.Producers {
		parent, child, err := NewPipe()
		if err != nil {
			t.Fatal(err)
		}
		client, err := NewClient(ctx, child, producer, 1, config.Limits)
		if err != nil {
			_ = parent.Close()
			t.Fatal(err)
		}
		clients = append(clients, client)
		server := make(chan error, 1)
		servers = append(servers, server)
		go func() {
			server <- controller.Serve(ctx, producer.ID, os.Getpid(), parent)
			close(server)
		}()
		t.Cleanup(func() {
			cancel()
			_ = client.conn.Close()
			_ = phaseTestResult(t, server)
		})
		control, _ := phaseTestPair(t, client)
		controls = append(controls, control)
		if err := client.Run(ctx, 1, exec.Command("/usr/bin/true")); err != nil {
			t.Fatal(err)
		}
	}
	for _, control := range controls {
		if err := control.Pause(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := controls[0].Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); !errors.Is(err, ErrBusy) {
		t.Fatalf("advanced before every producer checkpoint: %v", err)
	}
	if err := controls[1].Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	for index, control := range controls {
		if err := control.Resume(ctx); err != nil {
			t.Fatal(err)
		}
		if err := clients[index].Run(ctx, 1, exec.Command("/usr/bin/true")); err != nil {
			t.Fatal(err)
		}
		if err := control.Pause(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	for index, client := range clients {
		if err := client.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := phaseTestResult(t, servers[index]); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Attempts != 4 ||
		snapshot.Phases[0].Attempts != 2 || snapshot.Phases[1].Attempts != 2 {
		t.Fatalf("multi-producer handoff: %+v, %v", snapshot, err)
	}
}

func TestPhaseControlRejectsMalformedFrames(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*[FrameBytes]byte)
	}{
		{"magic", func(raw *[FrameBytes]byte) { raw[0] = 'D' }},
		{"operation", func(raw *[FrameBytes]byte) { raw[4] = 255 }},
		{"reserved_5", func(raw *[FrameBytes]byte) { raw[5] = 1 }},
		{"reserved_12", func(raw *[FrameBytes]byte) { raw[12] = 1 }},
		{"reserved_24", func(raw *[FrameBytes]byte) { raw[24] = 1 }},
		{"wrong_phase", func(raw *[FrameBytes]byte) { raw[11] = 2 }},
		{"ordinal_gap", func(raw *[FrameBytes]byte) { raw[23] = 2 }},
		{"wrong_lifetime", func(raw *[FrameBytes]byte) { raw[32] = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller, client, server := paired(t, testConfig())
			if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
				t.Fatal(err)
			}
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			peer, err := adopt(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = peer.Close() }()
			done, err := StartPhaseControl(t.Context(), child, client, phaseTestConfig())
			if err != nil {
				t.Fatal(err)
			}
			raw := phaseControlFrame{op: phasePause, phase: 1, sequence: 1, binding: client.binding}.encode()
			test.change(&raw)
			if _, err := peer.Write(raw[:]); err != nil {
				t.Fatal(err)
			}
			if err := phaseTestResult(t, done); !errors.Is(err, ErrProtocol) {
				t.Fatalf("malformed frame: %v", err)
			}
			_ = phaseTestResult(t, server)
			snapshot, err := controller.Snapshot()
			if err == nil || snapshot.Complete || snapshot.Attempts != 1 {
				t.Fatalf("malformed control changed prior prefix: %+v, %v", snapshot, err)
			}
		})
	}
}

func TestPhaseControlCloseDoesNotCloseProducer(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	control, done := phaseTestPair(t, client)
	if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, done); err == nil {
		t.Fatal("control-only close completed the producer")
	}
	_ = phaseTestResult(t, server)
	snapshot, err := controller.Snapshot()
	if err == nil || snapshot.Complete || snapshot.Attempts != 1 || snapshot.Producers[0].Closed {
		t.Fatalf("control close fabricated terminal evidence: %+v, %v", snapshot, err)
	}
}

func TestPhaseControlRejectsSecondReceiver(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	_, firstDone := phaseTestPair(t, client)
	if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	done, err := StartPhaseControl(t.Context(), child, client, phaseTestConfig())
	if !errors.Is(err, ErrConfig) || done != nil {
		t.Fatalf("duplicate receiver created: %v", err)
	}
	if _, err := child.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("duplicate endpoint retained: %v", err)
	}
	if err := phaseTestResult(t, firstDone); err == nil {
		t.Fatal("duplicate attachment did not fail the existing lifetime")
	}
	_ = phaseTestResult(t, server)
	snapshot, err := controller.Snapshot()
	if err == nil || snapshot.Complete || snapshot.Attempts != 1 {
		t.Fatalf("duplicate attachment changed accepted prefix: %+v, %v", snapshot, err)
	}
}

func TestPhaseControlInvalidConfigurationClosesDescriptor(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*PhaseControlConfig)
	}{
		{"empty", func(config *PhaseControlConfig) { config.Phases = nil }},
		{"duplicate", func(config *PhaseControlConfig) { config.Phases = []uint32{1, 1} }},
		{"zero_phase", func(config *PhaseControlConfig) { config.Phases = []uint32{0, 1} }},
		{"absent_initial", func(config *PhaseControlConfig) { config.InitialPhase = 3 }},
		{"phase_cap", func(config *PhaseControlConfig) { config.MaximumPhases = 1 }},
		{"wire_cap", func(config *PhaseControlConfig) { config.MaximumWireBytes = 2*FrameBytes - 1 }},
		{"timeout", func(config *PhaseControlConfig) { config.Timeout = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Close() }()
			config := phaseTestConfig()
			test.change(&config)
			if _, err := NewPhaseControl(t.Context(), parent, [32]byte{1}, config); !errors.Is(err, ErrConfig) {
				t.Fatalf("invalid configuration: %v", err)
			}
			if _, err := parent.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("rejected descriptor retained: %v", err)
			}
		})
	}
}

func TestPhaseControlRuntimeWireExhaustion(t *testing.T) {
	for _, limitedSide := range []string{"parent", "receiver_idle_reservation"} {
		t.Run(limitedSide, func(t *testing.T) {
			controller, client, server := paired(t, testConfig())
			if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
				t.Fatal(err)
			}
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			parentConfig, childConfig := phaseTestConfig(), phaseTestConfig()
			if limitedSide == "parent" {
				parentConfig.MaximumWireBytes = 2 * FrameBytes
			} else {
				childConfig.MaximumWireBytes = 2 * FrameBytes
			}
			control, err := NewPhaseControl(t.Context(), parent, client.binding, parentConfig)
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			defer func() { _ = control.Close() }()
			done, err := StartPhaseControl(t.Context(), child, client, childConfig)
			if err != nil {
				t.Fatal(err)
			}
			if err := control.Pause(t.Context()); err != nil {
				t.Fatal(err)
			}
			if limitedSide == "parent" {
				if err := control.Checkpoint(t.Context()); !errors.Is(err, ErrLimit) {
					t.Fatalf("second pair exceeded parent budget: %v", err)
				}
				if err := phaseTestResult(t, done); err == nil {
					t.Fatal("parent budget refusal did not fail producer")
				}
			} else if err := phaseTestResult(t, done); !errors.Is(err, ErrLimit) {
				// Pause consumed the only permitted pair. The receiver must
				// reserve another pair before its idle read, not overrun its
				// budget or wait indefinitely with unaccounted wire capacity.
				t.Fatalf("receiver idle reservation: %v", err)
			}
			if got := control.ReservedWireBytes(); got != 2*FrameBytes {
				t.Fatalf("wire refusal invented an extra pair: %d", got)
			}
			_ = phaseTestResult(t, server)
			snapshot, err := controller.Snapshot()
			if err == nil || snapshot.Complete || snapshot.Attempts != 1 {
				t.Fatalf("wire refusal lost prior prefix: %+v, %v", snapshot, err)
			}
		})
	}
}

func TestPhaseControlPartialFirstByteTimesOut(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := adopt(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	config := phaseTestConfig()
	config.Timeout = 30 * time.Millisecond
	done, err := StartPhaseControl(t.Context(), child, client, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write([]byte{'P'}); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, done); !errors.Is(err, ErrTransport) {
		t.Fatalf("partial frame did not reach the bounded read deadline: %v", err)
	}
	_ = phaseTestResult(t, server)
	snapshot, err := controller.Snapshot()
	if err == nil || snapshot.Complete || snapshot.Attempts != 1 || client.Context().Err() == nil {
		t.Fatalf("partial frame lost prior prefix or failed open: %+v, %v", snapshot, err)
	}
}

func TestPhaseControlHandoffPersistentAndPausedStart(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	control, controlDone := phaseTestPair(t, client)
	command := exec.Command("/bin/cat")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := client.Start(t.Context(), 2, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	if err := control.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	marker := t.TempDir() + "/born"
	blocked := exec.Command("/bin/sh", "-c", `: > "$1"`, "phase-test", marker)
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		finished <- client.Run(t.Context(), 1, blocked)
		close(finished)
	}()
	<-started
	select {
	case err := <-finished:
		t.Fatalf("paused Start returned: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("paused command executed: %v", err)
	}
	if snapshot, err := controller.Snapshot(); err != nil || snapshot.Attempts != 1 {
		t.Fatalf("paused caller consumed admission: %+v, %v", snapshot, err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := control.Checkpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := control.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, finished); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	if err := handle.Wait(); err != nil {
		t.Fatal(err)
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Phases[0].Attempts != 1 || snapshot.Phases[1].Attempts != 1 {
		t.Fatalf("wrong admission phase: %+v", snapshot)
	}
	if got := control.ReservedWireBytes(); got != 6*FrameBytes {
		t.Fatalf("control wire bytes = %d, want %d", got, 6*FrameBytes)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, controlDone); err != nil {
		t.Fatalf("closed producer control: %v", err)
	}
}

func TestPhaseControlCheckpointDoesNotQuiesceOwnerWork(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	control, _ := phaseTestPair(t, client)
	joined := make(chan struct{})
	continueOwner := make(chan struct{})
	done := make(chan error, 1)
	var ownerPublished atomic.Bool
	go func() {
		err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true"))
		close(joined)
		if err == nil {
			select {
			case <-continueOwner:
				// Models post-Wait Go/store work, not a new dispatch or an
				// actual database test. The owner must fence this separately.
				ownerPublished.Store(true)
			case <-client.Context().Done():
				err = context.Cause(client.Context())
			}
		}
		done <- err
		close(done)
	}()
	<-joined
	if err := control.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := control.Checkpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ownerPublished.Load() {
		t.Fatal("fixture owner unexpectedly finished")
	}
	close(continueOwner)
	if err := phaseTestResult(t, done); err != nil || !ownerPublished.Load() {
		t.Fatalf("post-checkpoint owner work: %v", err)
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Attempts != 1 || snapshot.Producers[0].Active != 0 {
		t.Fatalf("non-dispatch work altered the prefix: %+v", snapshot)
	}
}

func TestPhaseControlCancellationRetainsJoinedPrefix(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	control, controlDone := phaseTestPair(t, client)
	command := exec.Command("/bin/cat")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := client.Start(t.Context(), 1, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	if err := control.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := control.Checkpoint(ctx); err == nil {
		t.Fatal("checkpoint crossed an active one-shot")
	}
	_ = input.Close()
	_ = handle.Wait() // The join remains mandatory even if settlement fails.
	if command.ProcessState == nil || !command.ProcessState.Exited() {
		t.Fatal("canceled control lost the owned child join")
	}
	if err := phaseTestResult(t, controlDone); err == nil {
		t.Fatal("uncertain control was accepted")
	}
	_ = phaseTestResult(t, server)
	snapshot, err := controller.Snapshot()
	if err == nil || snapshot.Complete || snapshot.Attempts != 1 {
		t.Fatalf("cancellation lost accepted prefix: %+v, %v", snapshot, err)
	}
	if err := control.Resume(t.Context()); err == nil {
		t.Fatal("canceled control resumed")
	}
}

func TestPhaseControlLostCheckpointAndResumeACK(t *testing.T) {
	for _, drop := range []int{2, 3} {
		t.Run(fmt.Sprintf("reply_%d", drop), func(t *testing.T) {
			controller, client, server := paired(t, testConfig())
			parent, relayParent, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			relayChild, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			left, err := adopt(relayParent)
			if err != nil {
				t.Fatal(err)
			}
			right, err := adopt(relayChild)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
			control, err := NewPhaseControl(t.Context(), parent, client.binding, phaseTestConfig())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = control.Close() })
			controlDone, err := StartPhaseControl(t.Context(), child, client, phaseTestConfig())
			if err != nil {
				t.Fatal(err)
			}
			relayDone := make(chan error, 1)
			go func() {
				defer func() { _ = left.Close(); _ = right.Close() }()
				for ordinal := 1; ordinal <= drop; ordinal++ {
					var request, ack [FrameBytes]byte
					if _, err := io.ReadFull(left, request[:]); err != nil {
						relayDone <- err
						return
					}
					if _, err := right.Write(request[:]); err != nil {
						relayDone <- err
						return
					}
					if _, err := io.ReadFull(right, ack[:]); err != nil {
						relayDone <- err
						return
					}
					if request != ack {
						relayDone <- ErrProtocol
						return
					}
					if ordinal != drop {
						if _, err := left.Write(ack[:]); err != nil {
							relayDone <- err
							return
						}
					}
				}
				relayDone <- nil
			}()
			if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
				t.Fatal(err)
			}
			if err := control.Pause(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := controller.Fence(); err != nil {
				t.Fatal(err)
			}
			checkpointErr := control.Checkpoint(t.Context())
			if drop == 2 {
				if checkpointErr == nil {
					t.Fatal("lost checkpoint ACK succeeded")
				}
			} else {
				if checkpointErr != nil {
					t.Fatal(checkpointErr)
				}
				if err := controller.Advance(); err != nil {
					t.Fatal(err)
				}
				if err := control.Resume(t.Context()); err == nil {
					t.Fatal("lost resume ACK succeeded")
				}
			}
			if err := phaseTestResult(t, relayDone); err != nil {
				t.Fatal(err)
			}
			if err := phaseTestResult(t, controlDone); err == nil {
				t.Fatal("lost control endpoint did not fail producer")
			}
			_ = phaseTestResult(t, server)
			snapshot, err := controller.Snapshot()
			if err == nil || snapshot.Complete || snapshot.Attempts != 1 || snapshot.Phases[0].Attempts != 1 {
				t.Fatalf("lost ACK changed accepted prefix: %+v, %v", snapshot, err)
			}
			if err := control.Resume(t.Context()); err == nil {
				t.Fatal("uncertain control retried")
			}
		})
	}
}

func TestPhaseControlInheritedChildHelper(t *testing.T) {
	if os.Getenv("DISPATCH_PHASE_CONTROL_TEST_HELPER") != "1" {
		return
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	config := testConfig()
	client, err := NewClient(ctx, os.NewFile(3, "inherited-admission"), config.Producers[0], 1, config.Limits)
	if err != nil {
		t.Fatal(err)
	}
	controlFile := os.NewFile(4, "inherited-phase-control")
	done, err := StartPhaseControl(ctx, controlFile, client, phaseTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	// The inherited descriptor must be adopted synchronously, before this
	// producer's first native Start. No receiver scheduling, RPC or sleep
	// may be needed to protect it from inheritance by an unregistered child.
	if _, err := controlFile.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("control descriptor was not synchronously adopted: %v", err)
	}
	if err := client.Run(ctx, 1, exec.Command("/bin/sh", "-c", "test ! -e /dev/fd/4")); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		t.Fatal(err)
	}
	if err := client.Run(ctx, 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "resumed"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestPhaseControlInheritedChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	config := testConfig()
	controller, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	admissionParent, admissionChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	controlParent, controlChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewPhaseControl(ctx, controlParent, config.Producers[0].Binding, phaseTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Close() }()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPhaseControlInheritedChildHelper$")
	command.Env = []string{"DISPATCH_PHASE_CONTROL_TEST_HELPER=1", "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = []*os.File{admissionChild, controlChild}
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	_ = admissionChild.Close()
	_ = controlChild.Close()
	defer func() {
		_ = input.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	server := make(chan error, 1)
	go func() { server <- controller.Serve(ctx, 1, command.Process.Pid, admissionParent) }()
	reader := bufio.NewReader(output)
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("inherited readiness: %q, %v", line, err)
	}
	if err := control.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := control.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := control.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "resumed\n" {
		t.Fatalf("inherited resume: %q, %v", line, err)
	}
	if err := control.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, server); err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Phases[0].Attempts != 1 || snapshot.Phases[1].Attempts != 1 {
		t.Fatalf("inherited phase prefix: %+v, %v", snapshot, err)
	}
}
