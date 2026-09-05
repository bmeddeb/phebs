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
	"strconv"
	"testing"
	"time"
)

func TestDispatchChildHelper(t *testing.T) {
	if os.Getenv("DISPATCH_ADMISSION_TEST_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	config := testConfig()
	client, err := NewClient(t.Context(), os.NewFile(3, "test-inherited-dispatch"), config.Producers[0], 1, config.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "pending" {
		if _, err := client.admit(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
			t.Fatal(err)
		}
	} else {
		var descriptor uintptr
		raw, err := client.conn.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		if err := raw.Control(func(fd uintptr) { descriptor = fd }); err != nil {
			t.Fatal(err)
		}
		// The actual adopted socket must not survive an unregistered native
		// exec. This child runs through the same Start/Wait admission boundary.
		command := exec.Command("/bin/sh", "-c", "test ! -e /dev/fd/\"$1\"", "dispatch-fd-check", strconv.FormatUint(uint64(descriptor), 10))
		if err := client.Run(t.Context(), 1, command); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionInheritedSocketAndHardDeath(t *testing.T) {
	for _, mode := range []string{"normal", "pending"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			config := testConfig()
			controller, err := New(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDispatchChildHelper$", "--", mode)
			command.Env = []string{"DISPATCH_ADMISSION_TEST_HELPER=1", "GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles = []*os.File{child}
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
				_ = parent.Close()
				_ = child.Close()
				t.Fatal(err)
			}
			_ = child.Close()
			defer func() {
				_ = input.Close()
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			server := make(chan error, 1)
			go func() { server <- controller.Serve(ctx, 1, command.Process.Pid, parent) }()
			ready, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || ready != "ready\n" {
				t.Fatalf("helper readiness: %q, %v", ready, err)
			}
			if err := controller.Fence(); err != nil {
				t.Fatal(err)
			}
			if mode == "pending" {
				if err := controller.ExpectHardDeath(1); err != nil {
					t.Fatal(err)
				}
				if err := controller.Advance(); !errors.Is(err, ErrBusy) {
					t.Fatalf("advanced before hard-death Wait: %v", err)
				}
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				if err := command.Wait(); err == nil {
					t.Fatal("hard-death command succeeded")
				}
				if err := <-server; err != nil {
					t.Fatal(err)
				}
				if err := controller.CloseHardDeath(1, command.ProcessState); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := input.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
				if err := command.Wait(); err != nil {
					t.Fatal(err)
				}
				if err := <-server; err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := controller.Snapshot()
			if err != nil || !snapshot.Complete || snapshot.Attempts != 1 || snapshot.Producers[0].Active != 0 {
				t.Fatalf("native closure: %+v, %v", snapshot, err)
			}
		})
	}
}

func TestControllerLostAdmissionACKRetainsAcceptedPrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	config := testConfig()
	controller, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := adopt(child)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	server := make(chan error, 1)
	go func() { server <- controller.Serve(ctx, 1, os.Getpid(), parent) }()
	request := frame{op: opAdmit, phase: 1, site: 1, ordinal: 1, sequence: 1, binding: config.Producers[0].Binding}.encode()
	if _, err := peer.Write(request[:]); err != nil {
		t.Fatal(err)
	}
	// Never read the ACK and never start a process. Wait for the commit, then
	// lose the endpoint: permission stays counted, completeness must refuse.
	deadline := time.Now().Add(time.Second)
	for {
		snapshot, err := controller.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Attempts == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("controller did not commit")
		}
		time.Sleep(time.Millisecond)
	}
	_ = peer.Close()
	if err := <-server; err == nil {
		t.Fatal("lost terminal record accepted")
	}
	snapshot, err := controller.Snapshot()
	if err == nil || snapshot.Attempts != 1 || snapshot.Complete || snapshot.Producers[0].Active != 1 {
		t.Fatalf("prefix lost: %+v, %v", snapshot, err)
	}
}

func TestClientLostSettlementACKStillJoinsCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	config := testConfig()
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := adopt(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	client, err := NewClient(ctx, child, config.Producers[0], 1, config.Limits)
	if err != nil {
		t.Fatal(err)
	}
	server := make(chan error, 1)
	go func() {
		var raw [FrameBytes]byte
		if _, err := io.ReadFull(peer, raw[:]); err != nil {
			server <- err
			return
		}
		if _, err := peer.Write(raw[:]); err != nil {
			server <- err
			return
		}
		if _, err := io.ReadFull(peer, raw[:]); err != nil {
			server <- err
			return
		}
		request, err := decode(raw)
		if err == nil && request.op != opSettle {
			err = ErrProtocol
		}
		_ = peer.Close() // Commit could have happened; no settlement ACK survives.
		server <- err
	}()
	command := exec.Command("/usr/bin/true")
	handle, err := client.Start(ctx, 1, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Wait(); !errors.Is(err, ErrTransport) {
		t.Fatalf("lost ACK: %v", err)
	}
	if command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatal("command was not actually joined before reporting transport failure")
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionTransportBoundsAndCancellation(t *testing.T) {
	for _, mode := range []string{"ack timeout", "partial frame", "malformed", "wire limit", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			config := testConfig()
			config.Limits.AckTimeout = 30 * time.Millisecond
			if mode == "wire limit" {
				config.Limits.WireBytes = 2 * FrameBytes
			}
			controller, err := New(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ack timeout" {
				defer func() { _ = parent.Close() }() // Deliberately never serve/read/ACK.
				client, err := NewClient(ctx, child, config.Producers[0], 1, config.Limits)
				if err != nil {
					t.Fatal(err)
				}
				command := exec.Command("/usr/bin/true")
				if _, err := client.Start(ctx, 1, command); err == nil {
					t.Fatal("missing ACK admitted a dispatch")
				}
				if command.Process != nil {
					t.Fatal("command started without ACK")
				}
				return
			}
			peer, err := adopt(child)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = peer.Close() }()
			server := make(chan error, 1)
			go func() { server <- controller.Serve(ctx, 1, os.Getpid(), parent) }()
			request := frame{op: opAdmit, phase: 1, site: 1, ordinal: 1, sequence: 1, binding: config.Producers[0].Binding}.encode()
			switch mode {
			case "partial frame":
				_, err = peer.Write(request[:1])
			case "malformed":
				request[7] = 1
				_, err = peer.Write(request[:])
			case "wire limit":
				_, err = peer.Write(request[:])
			case "canceled":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-server:
				if err == nil {
					t.Fatal("invalid transport accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("transport did not respect its bound")
			}
			snapshot, err := controller.Snapshot()
			if err == nil || snapshot.Complete || snapshot.ReservedWireBytes > config.Limits.WireBytes {
				t.Fatalf("bound: %+v, %v", snapshot, err)
			}
		})
	}
}

type panicDoneContext struct{ context.Context }

func (panicDoneContext) Done() <-chan struct{} { panic("private panic text must not escape") }

func TestAdmissionServePanicIsSourceFreeAndLatched(t *testing.T) {
	controller, err := New(t.Context(), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Close() }()
	if err := controller.Serve(panicDoneContext{t.Context()}, 1, os.Getpid(), parent); !errors.Is(err, ErrPanic) {
		t.Fatalf("panic escaped: %v", err)
	}
	snapshot, err := controller.Snapshot()
	if !errors.Is(err, ErrPanic) || snapshot.Complete || snapshot.Attempts != 0 {
		t.Fatalf("panic lost: %+v, %v", snapshot, err)
	}
}
