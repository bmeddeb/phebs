//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Limits:    Limits{Producers: 1, Sites: 2, Roles: 2, Phases: 2, ActivePerProducer: 4, Attempts: 100, WireBytes: 64 << 10, AckTimeout: time.Second},
		Producers: []Producer{{ID: 1, Binding: [32]byte{1}, Sites: []Site{{ID: 1, Role: 1}, {ID: 2, Role: 2, Persistent: true}}}},
		Phases:    []Phase{{ID: 1, Roles: []RoleBudget{{Role: 1, Attempts: 50}, {Role: 2, Attempts: 5}}}, {ID: 2, Roles: []RoleBudget{{Role: 1, Attempts: 50}, {Role: 2, Attempts: 5}}}},
	}
}

func paired(t *testing.T, config Config) (*Controller, *Client, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	controller, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ctx, child, config.Producers[0], config.Phases[0].ID, config.Limits)
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	server := make(chan error, 1)
	go func() { server <- controller.Serve(ctx, config.Producers[0].ID, os.Getpid(), parent) }()
	t.Cleanup(func() { cancel(); _ = client.conn.Close() })
	return controller, client, server
}

func finishPair(t *testing.T, controller *Controller, client *Client, server <-chan error) Snapshot {
	t.Helper()
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-server:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not close")
	}
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete {
		t.Fatalf("incomplete: %+v, %v", snapshot, err)
	}
	return snapshot
}

func TestAdmissionCountsFailedStartAndRetry(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	missing := exec.Command("/dispatch-admission-no-such-executable")
	if _, err := client.Start(t.Context(), 1, missing); err == nil {
		t.Fatal("missing executable started")
	}
	if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Attempts != 2 || snapshot.Phases[0].Roles[0].Attempts != 2 || snapshot.Producers[0].Ordinal != 2 || snapshot.Producers[0].Active != 0 || snapshot.Digest == ([32]byte{}) {
		t.Fatalf("bad prefix: %+v", snapshot)
	}
	// Two one-shots have two pairs apiece; terminal close has one pair.
	if snapshot.ReservedWireBytes != 10*FrameBytes {
		t.Fatalf("wire reservation: %d", snapshot.ReservedWireBytes)
	}
}

func TestAdmissionPreSetupAndNilPassThrough(t *testing.T) {
	var ordinary *Client
	if err := ordinary.Run(t.Context(), 0, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	handle, err := ordinary.Start(t.Context(), 0, exec.Command("/usr/bin/true"))
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Wait(); err != nil {
		t.Fatal(err)
	}
	controller, client, server := paired(t, testConfig())
	command := exec.Command("/usr/bin/true")
	command.Stdout = os.Stdout
	if _, err := command.StdoutPipe(); err == nil {
		t.Fatal("pipe preflight should fail")
	}
	if _, err := client.Start(t.Context(), 1, nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("nil command: %v", err)
	}
	if got := finishPair(t, controller, client, server).Attempts; got != 0 {
		t.Fatalf("pre-admission work counted: %d", got)
	}
}

func TestAdmissionACKBeforeStartCheckpointBarrier(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	command := exec.Command("/usr/bin/true")
	// This is the exact private method used by Start, paused after consuming
	// the real socket ACK and before invoking exec.Cmd.Start.
	ordinal, err := client.admit(t.Context(), 1, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	checkpoint := make(chan error, 1)
	go func() { checkpoint <- client.Checkpoint(t.Context()) }()
	select {
	case err := <-checkpoint:
		t.Fatalf("checkpoint crossed pending Start: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := controller.Advance(); !errors.Is(err, ErrBusy) {
		t.Fatalf("advance: %v", err)
	}
	// Irrevocable pre-Start cancellation settles permission without a process.
	if err := client.settle(ordinal); err != nil {
		t.Fatal(err)
	}
	if err := <-checkpoint; err != nil {
		t.Fatal(err)
	}
	if command.Process != nil {
		t.Fatal("paused command unexpectedly started")
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := client.Resume(2); err != nil {
		t.Fatal(err)
	}
	if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Phases[0].Attempts != 1 || snapshot.Phases[1].Attempts != 1 {
		t.Fatalf("phase attribution changed: %+v", snapshot.Phases)
	}
}

func TestAdmissionPersistentHandleAndOwnedWait(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	command := exec.Command("/bin/cat")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	handle, err := client.Start(t.Context(), 2, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := client.Resume(2); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	if err := handle.Wait(); err != nil {
		t.Fatal(err)
	}
	snapshot := finishPair(t, controller, client, server)
	// Admit + one carry + two checkpoints + settle + close, each a pair.
	if snapshot.Attempts != 1 || snapshot.ReservedWireBytes != 12*FrameBytes {
		t.Fatalf("persistent accounting: %+v", snapshot)
	}
}

func TestAdmissionConcurrentOneShots(t *testing.T) {
	config := testConfig()
	config.Limits.ActivePerProducer = 24
	controller, client, server := paired(t, config)
	var workers sync.WaitGroup
	errors := make(chan error, 20)
	for range 20 {
		workers.Go(func() { errors <- client.Run(t.Context(), 1, exec.Command("/usr/bin/true")) })
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Attempts != 20 || snapshot.Producers[0].Active != 0 {
		t.Fatalf("concurrency: %+v", snapshot)
	}
}

func TestAdmissionPersistentSettlementBetweenAdvanceAndResume(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	handle, err := client.Start(t.Context(), 2, exec.Command("/usr/bin/true"))
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	// A real persistent command can finish while its producer still knows
	// only the previous phase. Settlement must not falsify a new admission.
	if err := handle.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := client.Resume(2); err != nil {
		t.Fatal(err)
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Phases[0].Attempts != 1 || snapshot.Phases[1].Attempts != 0 {
		t.Fatalf("settlement counted as admission: %+v", snapshot.Phases)
	}
}

func TestAdmissionOverflowAndForgedHardDeathRefuse(t *testing.T) {
	for _, mode := range []string{"sequence overflow", "wire overflow", "forged Wait"} {
		t.Run(mode, func(t *testing.T) {
			config := testConfig()
			controller, err := New(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			if err := controller.attach(1, os.Getpid()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "sequence overflow":
				controller.producers[1].sequence = math.MaxUint64
				err = controller.accept(1, frame{op: opAdmit, phase: 1, site: 1, ordinal: 1, sequence: 0, binding: config.Producers[0].Binding})
			case "wire overflow":
				controller.limits.WireBytes = math.MaxUint64
				controller.wireBytes = math.MaxUint64 - 1
				err = controller.reservePair()
			case "forged Wait":
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
				if err := controller.ExpectHardDeath(1); err != nil {
					t.Fatal(err)
				}
				if err := controller.endStream(1); err != nil {
					t.Fatal(err)
				}
				err = controller.CloseHardDeath(1, &os.ProcessState{})
			}
			if err == nil {
				t.Fatal("invalid state accepted")
			}
			snapshot, snapshotErr := controller.Snapshot()
			if snapshotErr == nil || snapshot.Complete || snapshot.Attempts != 0 {
				t.Fatalf("failure hidden: %+v, %v", snapshot, snapshotErr)
			}
		})
	}
}

func TestControllerRejectsClosedProtocol(t *testing.T) {
	tests := []struct {
		name   string
		prefix bool
		edit   func(*Config, *frame)
		want   error
	}{
		{"unknown site", false, func(_ *Config, f *frame) { f.site = 99 }, ErrProtocol},
		{"wrong binding", false, func(_ *Config, f *frame) { f.binding[1] = 1 }, ErrProtocol},
		{"ordinal gap", false, func(_ *Config, f *frame) { f.ordinal = 2 }, ErrProtocol},
		{"sequence gap", false, func(_ *Config, f *frame) { f.sequence = 2 }, ErrProtocol},
		{"wrong phase", false, func(_ *Config, f *frame) { f.phase = 2 }, ErrProtocol},
		{"zero role", false, func(c *Config, _ *frame) { c.Phases[0].Roles[0].Attempts = 0 }, ErrLimit},
		{"duplicate sequence", true, func(_ *Config, f *frame) { f.ordinal = 2 }, ErrProtocol},
		{"active limit", true, func(c *Config, f *frame) { c.Limits.ActivePerProducer = 1; f.ordinal = 2; f.sequence = 2 }, ErrLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig()
			request := frame{op: opAdmit, phase: 1, site: 1, ordinal: 1, sequence: 1, binding: config.Producers[0].Binding}
			test.edit(&config, &request)
			controller, err := New(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			if err := controller.attach(1, os.Getpid()); err != nil {
				t.Fatal(err)
			}
			if test.prefix {
				if err := controller.accept(1, frame{op: opAdmit, phase: 1, site: 1, ordinal: 1, sequence: 1, binding: config.Producers[0].Binding}); err != nil {
					t.Fatal(err)
				}
			}
			if err := controller.accept(1, request); !errors.Is(err, test.want) {
				t.Fatalf("got %v", err)
			}
			if err := controller.Fence(); !errors.Is(err, test.want) {
				t.Fatalf("failure not sticky: %v", err)
			}
			snapshot, err := controller.Snapshot()
			if !errors.Is(err, test.want) || snapshot.Complete {
				t.Fatalf("failed snapshot: %+v / %v", snapshot, err)
			}
			want := uint64(0)
			if test.prefix {
				want = 1
			}
			if snapshot.Attempts != want {
				t.Fatalf("refused dispatch counted: %d", snapshot.Attempts)
			}
		})
	}
}

func TestAdmissionConstructorCopiesAndRefusesBadBounds(t *testing.T) {
	for _, edit := range []func(*Config){
		func(c *Config) { c.Limits.ActivePerProducer = 0 },
		func(c *Config) { c.Limits.AckTimeout = 0 },
		func(c *Config) { c.Limits.WireBytes = 1 },
		func(c *Config) { c.Limits.Sites = 1 },
		func(c *Config) { c.Producers[0].Sites[1].ID = 1 },
		func(c *Config) { c.Phases[0].Roles[1].Role = 1 },
		func(c *Config) { c.Phases[1].ID = 1 },
		func(c *Config) { c.Producers[0].Binding = [32]byte{} },
		func(c *Config) {
			c.Limits.Producers = 2
			c.Limits.Sites = 4
			duplicate := c.Producers[0]
			duplicate.ID = 2
			c.Producers = append(c.Producers, duplicate)
		},
	} {
		config := testConfig()
		edit(&config)
		if _, err := New(t.Context(), config); !errors.Is(err, ErrConfig) {
			t.Fatalf("invalid config: %v", err)
		}
	}
	config := testConfig()
	controller, err := New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	config.Phases[0].Roles[0].Attempts = 0
	config.Producers[0].Sites[0].Role = 99
	if controller.phases[0].Roles[0].Attempts != 50 || controller.producers[1].sites[1].Role != 1 {
		t.Fatal("configuration retained mutable slices")
	}
}

func TestAdmissionCancelUnusedClosesEarlyStopPrefix(t *testing.T) {
	config := testConfig()
	config.Limits.Producers = 2
	config.Limits.Sites = 3
	config.Producers = append(config.Producers, Producer{ID: 2, Binding: [32]byte{2}, Sites: []Site{{ID: 1, Role: 1}}})
	controller, client, server := paired(t, config)
	if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := controller.CancelUnused(2); err != nil {
		t.Fatal(err)
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Attempts != 1 || !snapshot.Producers[0].Attached || snapshot.Producers[1].Attached || !snapshot.Producers[1].Closed || snapshot.Producers[1].Ordinal != 0 {
		t.Fatalf("unused prefix mislabeled: %+v", snapshot)
	}
}

func TestAdmissionCancelUnusedIsFencedAndIrreversible(t *testing.T) {
	for _, mode := range []string{"unfenced", "unknown", "attached", "repeat", "later attach", "later admission"} {
		t.Run(mode, func(t *testing.T) {
			config := testConfig()
			controller, err := New(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "unfenced" {
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "attached" {
				if err := controller.attach(1, os.Getpid()); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "repeat" || mode == "later attach" || mode == "later admission" {
				if err := controller.CancelUnused(1); err != nil {
					t.Fatal(err)
				}
				snapshot, err := controller.Snapshot()
				if err != nil || !snapshot.Complete || snapshot.Producers[0].Attached {
					t.Fatalf("unused cancellation: %+v, %v", snapshot, err)
				}
			}
			switch mode {
			case "unknown":
				err = controller.CancelUnused(99)
			case "later attach":
				err = controller.attach(1, os.Getpid())
			case "later admission":
				err = controller.accept(1, frame{op: opAdmit, phase: 1, site: 1, ordinal: 1, sequence: 1, binding: config.Producers[0].Binding})
			default:
				err = controller.CancelUnused(1)
			}
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("unsafe unused closure accepted: %v", err)
			}
			snapshot, err := controller.Snapshot()
			if !errors.Is(err, ErrProtocol) || snapshot.Complete || snapshot.Attempts != 0 {
				t.Fatalf("failure hidden: %+v, %v", snapshot, err)
			}
		})
	}
}
