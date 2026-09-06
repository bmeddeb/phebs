//go:build darwin || linux

package storeaccounting

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestClientStrictACKAndCloseEOF(t *testing.T) {
	for _, name := range []string{"op", "binding", "phase", "sequence", "kind", "rows", "token-zero", "lost-submit", "close-extra", "close-timeout", "close-eof", "cancel-in-ack"} {
		t.Run(name, func(t *testing.T) {
			parentFile, child, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			parent, err := adopt(parentFile)
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			requestCtx, cancelRequest := context.WithCancel(ctx)
			defer cancelRequest()
			keepOpen := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				defer func() { _ = parent.Close() }()
				if err := parent.SetDeadline(time.Now().Add(time.Second)); err != nil {
					done <- err
					return
				}
				for turn := 0; turn < 2; turn++ {
					var raw [FrameBytes]byte
					if _, err := io.ReadFull(parent, raw[:]); err != nil {
						done <- err
						return
					}
					frame, err := decodeFrame(raw, false)
					if err != nil {
						done <- err
						return
					}
					frame.op |= replyBit
					if turn == 1 {
						if name == "lost-submit" {
							done <- nil
							return
						}
						if name == "cancel-in-ack" {
							cancelRequest()
							done <- nil
							return
						}
						if frame.op == opSubmit|replyBit {
							frame.token = 1
						}
						switch name {
						case "op":
							frame.op = opSettle | replyBit
						case "binding":
							frame.binding[31]++
						case "phase":
							frame.phase++
						case "sequence":
							frame.sequence++
						case "kind":
							frame.kind = byte(Begin)
						case "rows":
							frame.rows++
						case "token-zero":
							frame.token = 0
						}
					}
					ack := frame.encode()
					if _, err := parent.Write(ack[:]); err != nil {
						done <- err
						return
					}
				}
				if name == "close-extra" {
					_, err := parent.Write([]byte{1})
					done <- err
					return
				}
				if name == "close-eof" || name == "close-timeout" {
					var extra [1]byte
					if n, err := parent.Read(extra[:]); n != 0 || err != io.EOF {
						done <- ErrProtocol
						return
					}
				}
				if name == "close-timeout" {
					<-keepOpen
				}
				done <- nil
			}()
			t.Cleanup(func() {
				close(keepOpen)
				_ = parent.Close()
				if err := <-done; err != nil {
					t.Errorf("controlled peer: %v", err)
				}
			})
			client, err := NewClient(ctx, child, ClientConfig{Producer: 2, Binding: [32]byte{1}, Phase: 1, Phases: 1,
				Calls: 2, Transactions: 1, WireBytes: 4096, AckTimeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.closeOwned)
			if name == "close-extra" || name == "close-timeout" || name == "close-eof" {
				err = client.Close(requestCtx)
				if (err == nil) != (name == "close-eof") {
					t.Fatalf("Close %s: %v", name, err)
				}
			} else {
				_, err = client.Submit(requestCtx, ImplicitWrite, 0, 1)
				if err == nil {
					t.Fatalf("accepted %s ACK", name)
				}
				client.mu.Lock()
				retained := client.calls[0].used
				wire := client.bytes
				client.mu.Unlock()
				if !retained || wire != 2*pairBytes {
					t.Fatalf("lost pending slot=%v bytes=%d", retained, wire)
				}
				if _, retry := client.Submit(ctx, ImplicitWrite, 0, 1); retry == nil {
					t.Fatal("lost ACK retried")
				}
			}
			if _, err := child.Stat(); err == nil {
				t.Fatal("original descriptor retained")
			}
		})
	}
}

func TestClientConstructorConsumesWrongFileAndInvalidConfig(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "regular-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(t.Context(), regular, ClientConfig{}); err == nil {
		t.Fatal("regular file accepted")
	}
	if _, err := regular.Stat(); err == nil {
		t.Fatal("wrong file was not closed")
	}
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	if _, err := NewClient(t.Context(), child, ClientConfig{}); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	if _, err := child.Stat(); err == nil {
		t.Fatal("invalid constructor retained child")
	}
}

func TestClientImmutableCapacities(t *testing.T) {
	var absent *Client
	if calls, transactions := absent.Capacities(); calls != 0 || transactions != 0 {
		t.Fatalf("nil capacities %d/%d", calls, transactions)
	}
	for _, capacity := range [][2]int{{1, 1}, {MaximumCalls, MaximumTransactions}} {
		controller, err := New(t.Context(), Config{Producers: []Producer{{ID: 2, Calls: capacity[0], Transactions: capacity[1]}}, Phases: []Phase{{ID: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		transport, err := NewTransport(t.Context(), controller, WireConfig{Producers: []WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 1}}, AckTimeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = transport.Close() })
		client := transportClient(t, transport, t.Context(), 2)
		if calls, transactions := client.Capacities(); calls != capacity[0] || transactions != capacity[1] {
			t.Fatalf("capacities %d/%d; want %v", calls, transactions, capacity)
		}
		if err := client.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := transport.Wait(t.Context(), 2); err != nil {
			t.Fatal(err)
		}
		if calls, transactions := client.Capacities(); calls != capacity[0] || transactions != capacity[1] {
			t.Fatal("closed lifetime changed immutable capacities")
		}
	}
}

func TestClientSingleSDKOwner(t *testing.T) {
	for _, client := range []*Client{nil, {}} {
		if err := client.ClaimSDKOwner(); !errors.Is(err, ErrConfig) {
			t.Fatalf("invalid client claim: %v", err)
		}
	}
	transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1}}, time.Second)
	client := transportClient(t, transport, ctx, 2)
	before := client.bytes
	claims := make(chan error, 2)
	for range 2 {
		go func() { claims <- client.ClaimSDKOwner() }()
	}
	winners := 0
	for range 2 {
		if err := <-claims; err == nil {
			winners++
		} else if !errors.Is(err, ErrConfig) {
			t.Fatalf("duplicate claim: %v", err)
		}
	}
	if winners != 1 || client.bytes != before || !client.sdkOwned {
		t.Fatalf("claims=%d bytes=%d/%d owned=%v", winners, client.bytes, before, client.sdkOwned)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := client.ClaimSDKOwner(); !errors.Is(err, ErrCanceled) || !client.sdkOwned {
		t.Fatalf("closed claim=%v owned=%v", err, client.sdkOwned)
	}
}

func TestClientSDKOwnerRequiresLiveClient(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "failed"}[failed], func(t *testing.T) {
			transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1}}, time.Second)
			client := transportClient(t, transport, ctx, 2)
			want := ErrCanceled
			if failed {
				want = ErrProtocol
				_ = client.Fail(ctx, want)
			} else {
				client.cancel()
			}
			if err := client.ClaimSDKOwner(); !errors.Is(err, want) || client.sdkOwned {
				t.Fatalf("inactive claim=%v owned=%v", err, client.sdkOwned)
			}
		})
	}
}

func TestClientBoundedEntrantsAndConcurrentNativeSlots(t *testing.T) {
	transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1, Transactions: MaximumCalls, Rows: MaximumCalls}}, time.Second)
	client := transportClient(t, transport, ctx, 2)
	// A successful Submit releases the wire gate without settling its native
	// lifetime. All forty concrete slots can remain live concurrently.
	var submissions [MaximumCalls]Submission
	for i := range submissions {
		var err error
		submissions[i], err = client.Submit(ctx, ImplicitWrite, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, submission := range submissions {
		if err := client.Settle(ctx, submission); err != nil {
			t.Fatal(err)
		}
	}
	// Deterministically hold the wire gate, then fill the finite entrance count.
	// No worker goroutines or an unbounded waiting-caller queue are needed.
	client.mu.Lock()
	client.entrants = client.config.Calls + 1
	before := client.bytes
	client.mu.Unlock()
	if _, err := client.acquire(ctx); !errors.Is(err, ErrLimit) {
		t.Fatalf("entrance cap: %v", err)
	}
	client.mu.Lock()
	after := client.bytes
	client.entrants = 0
	client.mu.Unlock()
	if before != after {
		t.Fatal("argument/cap refusal charged another wire pair")
	}
	if err := transport.Wait(ctx, 2); err == nil {
		t.Fatal("failed client completed")
	}
}
