//go:build darwin || linux

package storeaccounting

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/ownedpipe"
)

func TestRestoreReplayWrite(t *testing.T) {
	for _, mode := range []string{"success", "zero", "overflow", "nil callback", "canceled", "fenced", "HTTP failure", "panic", "late cancellation", "concurrent SDK"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 1, 1)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			rows := uint64(512)
			called := false
			submit := func(ctx context.Context) error {
				called = true
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != 1 || prefix.Rows != 512 || prefix.Producers[0].Calls != 1 {
					t.Fatalf("native submission preceded ACK: %+v %v", prefix, err)
				}
				switch mode {
				case "HTTP failure":
					return errors.New("native reply unavailable")
				case "panic":
					panic("native panic")
				case "late cancellation":
					cancel()
				case "concurrent SDK":
					if _, err := owner.acquire(ctx, 0, nil); !errors.Is(err, ErrLimit) {
						t.Fatalf("SDK call escaped shared slot: %v", err)
					}
				}
				return nil
			}
			switch mode {
			case "zero":
				rows = 0
			case "overflow":
				rows = 513
			case "nil callback":
				submit = nil
			case "canceled":
				cancel()
			case "fenced":
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
			}
			err := owner.RestoreReplayWrite(ctx, rows, submit)
			if (err == nil) != (mode == "success") {
				t.Fatalf("result %v", err)
			}
			prefix, _ := controller.Snapshot()
			if called {
				if prefix.Transactions != 1 || prefix.Rows != 512 || prefix.MaximumRows != 512 {
					t.Fatalf("accepted prefix lost: %+v", prefix)
				}
			} else if prefix.Transactions != 0 || prefix.Rows != 0 {
				t.Fatalf("preparation invented an attempt: %+v", prefix)
			}
			if mode == "success" {
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
				if prefix.Producers[0].Calls != 0 || owner.Checkpoint(ctx) != nil {
					t.Fatalf("completed native call did not drain: %+v", prefix)
				}
			} else {
				if err := owner.RestoreReplayWrite(context.Background(), 1, func(context.Context) error {
					t.Fatal("failed owner forwarded subsequent request")
					return nil
				}); err == nil {
					t.Fatal("failure was not sticky")
				}
				if owner.Checkpoint(context.Background()) == nil || owner.Close(context.Background()) == nil {
					t.Fatal("failed owner reported clean closure")
				}
			}
		})
	}
}

func TestRestoreReplayWriteLostACK(t *testing.T) {
	for _, lost := range []byte{opSubmit, opSettle} {
		t.Run(string(rune('0'+lost)), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			controller, err := New(ctx, Config{Producers: []Producer{{ID: 11, Calls: 1, Transactions: 1}},
				Phases: []Phase{{ID: 12, Transactions: 2, Rows: 512}}})
			if err != nil {
				t.Fatal(err)
			}
			parentFile, child, err := ownedpipe.New()
			if err != nil {
				t.Fatal(err)
			}
			parent, err := adopt(parentFile)
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				defer func() { _ = parent.Close() }()
				if err := parent.SetDeadline(time.Now().Add(time.Second)); err != nil {
					done <- err
					return
				}
				var submission Submission
				for {
					var raw [FrameBytes]byte
					if _, err := io.ReadFull(parent, raw[:]); err != nil {
						done <- err
						return
					}
					frame, err := decodeFrame(raw, false)
					if err == nil {
						switch frame.op {
						case opAttach:
							err = controller.Attach(11, 12)
						case opSubmit:
							submission, err = controller.Submit(Request{Producer: 11, Phase: 12, Kind: ImplicitWrite, Rows: uint64(frame.rows)})
							frame.token = submission.Ordinal
						case opSettle:
							err = controller.Settle(submission)
						default:
							err = ErrProtocol
						}
					}
					if err != nil || frame.op == lost {
						done <- err
						return
					}
					frame.op |= replyBit
					ack := frame.encode()
					if _, err := parent.Write(ack[:]); err != nil {
						done <- err
						return
					}
				}
			}()
			t.Cleanup(func() {
				_ = parent.Close()
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			client, err := NewClient(ctx, child, ClientConfig{Producer: 11, Binding: [32]byte{1}, Phase: 12, Phases: 2048,
				Calls: 1, Transactions: 1, WireBytes: 4096, AckTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.closeOwned)
			owner, err := NewSDKOwner(client)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = owner.RestoreReplayWrite(ctx, 512, func(context.Context) error { called = true; return nil })
			prefix, _ := controller.Snapshot()
			if err == nil || called != (lost == opSettle) || prefix.Transactions != 1 || prefix.Rows != 512 || prefix.Complete {
				t.Fatalf("lost ACK escaped: called=%t prefix=%+v error=%v", called, prefix, err)
			}
			if owner.Close(ctx) == nil {
				t.Fatal("lost ACK permitted successful owner close")
			}
		})
	}
}
