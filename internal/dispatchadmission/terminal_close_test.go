//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestProducerLocalCloseKeepsOtherProducerRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	config := testConfig()
	config.Limits.Producers, config.Limits.Sites = 3, 6
	for id := uint32(2); id <= 3; id++ {
		config.Producers = append(config.Producers, Producer{ID: id, Binding: [32]byte{byte(id)}, Sites: config.Producers[0].Sites})
	}
	controller, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	var clients []*Client
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
		server := make(chan error, 1)
		go func() {
			server <- controller.Serve(ctx, producer.ID, os.Getpid(), parent)
			close(server)
		}()
		t.Cleanup(func() {
			cancel()
			_ = client.conn.Close()
			_ = phaseTestResult(t, server)
		})
		clients, servers = append(clients, client), append(servers, server)
	}
	command := exec.Command("/bin/cat")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	handle, err := clients[0].Start(ctx, 2, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = handle.Wait()
		}
	})
	// Two distinct one-shot lifetimes close serially in one phase while the
	// first producer still owns a genuinely live persistent child.
	for index := 1; index < len(clients); index++ {
		if err := clients[index].Run(ctx, 1, exec.Command("/usr/bin/true")); err != nil {
			t.Fatal(err)
		}
		if err := clients[index].Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := phaseTestResult(t, servers[index]); err != nil {
			t.Fatal(err)
		}
		snapshot, err := controller.Snapshot()
		if err != nil || snapshot.Complete || snapshot.Producers[0].Active != 1 || !snapshot.Producers[index].Closed ||
			snapshot.Attempts != uint64(index+1) || controller.fenced {
			t.Fatalf("producer closure changed global work: %+v, %v", snapshot, err)
		}
	}
	if err := clients[0].Run(ctx, 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := clients[0].Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, servers[0]); err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Complete || snapshot.Attempts != 4 || snapshot.Phases[0].Attempts != 4 || snapshot.Phases[1].Attempts != 0 {
		t.Fatalf("local closes claimed global completion: %+v, %v", snapshot, err)
	}
	// Four admit/settle pairs and three terminal pairs, with no checkpoint,
	// carry, new operation or per-close idle reservation on this successful path.
	if snapshot.ReservedWireBytes != (2*4+3)*2*FrameBytes {
		t.Fatalf("unexpected terminal wire cost: %d", snapshot.ReservedWireBytes)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := controller.Snapshot(); err != nil || !snapshot.Complete {
		t.Fatalf("final fence did not complete closed producers: %+v, %v", snapshot, err)
	}
}

func TestProducerLocalCloseRejectsInvalidTerminalAuthority(t *testing.T) {
	for _, mode := range []string{
		"unattached", "binding", "phase", "sequence", "ordinal", "site", "one-shot", "persistent", "carried",
		"repeat", "later admission", "later attachment", "checkpoint unfenced", "advance unfenced", "old phase after advance",
	} {
		t.Run(mode, func(t *testing.T) {
			config := testConfig()
			controller, err := New(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			if err := controller.attach(1, os.Getpid()); err != nil {
				t.Fatal(err)
			}
			request := frame{op: opAdmit, phase: 1, site: 1, ordinal: 1, sequence: 1, binding: config.Producers[0].Binding}
			if mode == "persistent" || mode == "carried" {
				request.site = 2
			}
			if err := controller.accept(1, request); err != nil {
				t.Fatal(err)
			}
			request.site, request.sequence, request.op = 0, 2, opSettle
			active := mode == "one-shot" || mode == "persistent" || mode == "carried"
			if mode == "carried" {
				request.op = opCarry
			}
			if !active || mode == "carried" {
				if err := controller.accept(1, request); err != nil {
					t.Fatal(err)
				}
				request.sequence++
			}
			request.op = opClose
			want := ErrProtocol
			switch mode {
			case "unattached":
				controller.producers[1].attached = false
			case "binding":
				request.binding[1] = 1
			case "phase":
				request.phase = 2
			case "sequence":
				request.sequence++
			case "ordinal":
				request.ordinal++
			case "site":
				request.site = 1
			case "one-shot", "persistent", "carried":
				want = ErrBusy
			case "checkpoint unfenced":
				request.op = opCheckpoint
			case "old phase after advance":
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
				request.op = opCheckpoint
				if err := controller.accept(1, request); err != nil {
					t.Fatal(err)
				}
				if err := controller.Advance(); err != nil {
					t.Fatal(err)
				}
				request.sequence++
				request.op = opClose
			case "repeat", "later admission", "later attachment", "advance unfenced":
				if err := controller.accept(1, request); err != nil {
					t.Fatal(err)
				}
				request.sequence++
				if mode == "later admission" {
					request.op, request.site, request.ordinal = opAdmit, 1, 2
				}
			}
			switch mode {
			case "later attachment":
				err = controller.attach(1, os.Getpid())
			case "advance unfenced":
				err = controller.Advance()
			default:
				err = controller.accept(1, request)
			}
			if !errors.Is(err, want) {
				t.Fatalf("invalid terminal authority accepted: %v, want %v", err, want)
			}
			snapshot, err := controller.Snapshot()
			if !errors.Is(err, want) || snapshot.Complete || snapshot.Attempts != 1 || (snapshot.Producers[0].Active != 0) != active {
				t.Fatalf("terminal refusal lost its positive prefix: %+v, %v", snapshot, err)
			}
		})
	}
}

func TestProducerLocalClosePendingStartCannotDisappear(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	command := exec.Command("/usr/bin/true")
	if _, err := client.admit(t.Context(), 1, command); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.Close(ctx); !errors.Is(err, ErrCanceled) {
		t.Fatalf("pending Start closed without settlement: %v", err)
	}
	if err := phaseTestResult(t, server); err == nil {
		t.Fatal("lost pending permission accepted as terminal closure")
	}
	snapshot, err := controller.Snapshot()
	if err == nil || snapshot.Complete || snapshot.Attempts != 1 || snapshot.Producers[0].Active != 1 || snapshot.Producers[0].Closed || command.Process != nil {
		t.Fatalf("pending permission was lost or started: %+v, %v", snapshot, err)
	}
}

func TestProducerLocalCloseLostACKDoesNotSucceed(t *testing.T) {
	config := testConfig()
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := adopt(parent)
	if err != nil {
		_ = child.Close()
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	client, err := NewClient(t.Context(), child, config.Producers[0], 1, config.Limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.conn.Close() })
	controller, err := New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.attach(1, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	server := make(chan error, 1)
	go func() {
		var raw [FrameBytes]byte
		_, err := io.ReadFull(peer, raw[:])
		if err == nil {
			var request frame
			request, err = decode(raw)
			if err == nil && request.op != opClose {
				err = ErrProtocol
			}
			if err == nil {
				err = controller.accept(1, request)
			}
		}
		_ = peer.Close() // Terminal state committed; its ACK is deliberately lost.
		server <- err
	}()
	if err := client.Close(t.Context()); !errors.Is(err, ErrTransport) {
		t.Fatalf("lost terminal ACK succeeded: %v", err)
	}
	if err := phaseTestResult(t, server); err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Complete || !snapshot.Producers[0].Closed || snapshot.Attempts != 0 || client.closed {
		t.Fatalf("terminal prefix or ACK truth changed: %+v, %v", snapshot, err)
	}
	if err := client.Close(t.Context()); !errors.Is(err, ErrTransport) {
		t.Fatalf("lost terminal ACK was retried or forgotten: %v", err)
	}
}
