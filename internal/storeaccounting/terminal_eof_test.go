//go:build darwin || linux

package storeaccounting

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

// Actual socket/reducer tests only: raw peers do not establish a genuine SDK
// checkpoint, outer PC consumption, owned process kill, Wait or session census.
func terminalEOFFixture(t *testing.T) (*Transport, context.Context, *net.UnixConn, wireFrame) {
	t.Helper()
	transport, ctx := transportFixture(t, []uint32{4}, []Phase{{ID: 8, Transactions: 2, Rows: 512}}, time.Second)
	conn, frame := rawTransportClient(t, transport, 4)
	frame.op, frame.sequence, frame.kind, frame.rows = opSubmit, 2, byte(ImplicitWrite), 512
	reply := rawExchange(t, conn, frame)
	frame.op, frame.sequence, frame.kind, frame.rows, frame.token = opSettle, 3, 0, 0, reply.token
	rawExchange(t, conn, frame)
	if err := transport.Fence(); err != nil {
		t.Fatal(err)
	}
	frame.op, frame.sequence, frame.token = opCheckpoint, 4, 0
	rawExchange(t, conn, frame)
	return transport, ctx, conn, frame
}

func TestTransportTerminalEOFOnlyExplicitRemoteBoundary(t *testing.T) {
	transport, ctx, conn, _ := terminalEOFFixture(t)
	if err := transport.ArmTerminalEOF(4, 8); err != nil {
		t.Fatal(err)
	}
	before, err := transport.Snapshot()
	if err != nil || before.Complete || before.PrefixesClosed || before.TerminalEOF != 0 || before.Store.Producers[0].TerminalPhase != 8 || before.Store.Producers[0].TerminalFencedEOF {
		t.Fatal("arm fabricated closure", before, err)
	}
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 4); err != nil {
		t.Fatal(err)
	}
	after, err := transport.Snapshot()
	row := after.Store.Producers[0]
	if err != nil || after.Complete || after.Store.Complete || !after.PrefixesClosed || !after.Store.PrefixesClosed ||
		!row.TerminalFencedEOF || row.TerminalPhase != 8 || row.Closed || row.Calls != 0 || row.Transactions != 0 ||
		after.Store.Transactions != 1 || after.Store.Rows != 512 || after.Store.MaximumRows != 512 || row.Ordinal != 1 ||
		after.TerminalEOF != 1 || after.ReservedBytes != 5*pairBytes {
		t.Fatal("terminal prefix differs", after, err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTransportTerminalEOFFailuresRemainIncomplete(t *testing.T) {
	for _, mode := range []string{"unarmed", "partial", "opFail", "lateClose", "submit", "binding", "localClose", "cancel", "transportClose"} {
		t.Run(mode, func(t *testing.T) {
			transport, ctx, conn, frame := terminalEOFFixture(t)
			if mode != "unarmed" {
				if err := transport.ArmTerminalEOF(4, 8); err != nil {
					t.Fatal(err)
				}
			}
			frame.sequence++
			switch mode {
			case "partial":
				if _, err := conn.Write([]byte("SA")); err != nil {
					t.Fatal(err)
				}
			case "opFail", "lateClose", "submit", "binding":
				switch mode {
				case "opFail":
					frame.op, frame.kind = opFail, byte(failureKind(ErrDescriptor))
				case "lateClose":
					frame.op = opClose
				case "submit":
					frame.op, frame.kind, frame.rows = opSubmit, byte(ImplicitWrite), 0
				case "binding":
					frame.binding[0]++
				}
				raw := frame.encode()
				if _, err := conn.Write(raw[:]); err != nil {
					t.Fatal(err)
				}
			case "localClose":
				if err := transport.peers[0].conn.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				transport.cancel()
			case "transportClose":
				if err := transport.Close(); err == nil {
					t.Fatal("local close fabricated remote EOF")
				}
			}
			_ = conn.CloseWrite()
			if err := transport.Wait(ctx, 4); err == nil {
				t.Fatal("bad terminal stream completed")
			}
			result, err := transport.Snapshot()
			row := result.Store.Producers[0]
			if err == nil || result.Complete || result.PrefixesClosed || result.Store.PrefixesClosed || result.TerminalEOF != 0 || row.TerminalFencedEOF || row.Closed ||
				result.Store.Transactions != 1 || result.Store.Rows != 512 || result.Store.MaximumRows != 512 || row.Ordinal != 1 {
				t.Fatal("failed prefix lost or relabeled", result, err)
			}
		})
	}
}

func TestTransportTerminalEOFArmRefusesUnprovenOrBusyState(t *testing.T) {
	for _, mode := range []string{"unattached", "wrongProducer", "wrongPhase", "unfenced", "noCheckpoint", "call", "uuid", "notFinal", "repeat", "failed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			phases := []Phase{{ID: 8, Transactions: 2, Rows: 512}}
			if mode == "notFinal" {
				phases = append(phases, Phase{ID: 9})
			}
			transport, ctx := transportFixture(t, []uint32{4}, phases, time.Second)
			if mode == "unattached" {
				if err := transport.ArmTerminalEOF(4, 8); err == nil {
					t.Fatal("unattached producer armed")
				}
				return
			}
			client := transportClient(t, transport, ctx, 4)
			var transactions uint64
			switch mode {
			case "call":
				if _, err := client.Submit(ctx, ImplicitWrite, 0, 512); err != nil {
					t.Fatal(err)
				}
				transactions = 1
			case "uuid":
				submitSettle(t, ctx, client, Begin, 0, 0)
				transactions = 1
			}
			if mode != "unfenced" {
				if err := transport.Fence(); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "unfenced" && mode != "noCheckpoint" && mode != "call" && mode != "uuid" {
				if err := client.Checkpoint(ctx); err != nil {
					t.Fatal(err)
				}
			}
			producer, phase := uint32(4), uint32(8)
			switch mode {
			case "wrongProducer":
				producer = 5
			case "wrongPhase":
				phase = 9
			case "repeat":
				if err := transport.ArmTerminalEOF(4, 8); err != nil {
					t.Fatal(err)
				}
			case "failed":
				_ = transport.controller.Fail(ErrDescriptor)
			case "canceled":
				transport.cancel()
			}
			if err := transport.ArmTerminalEOF(producer, phase); err == nil {
				t.Fatal("unproven terminal arm accepted")
			}
			_ = transport.Wait(ctx, 4)
			result, err := transport.Snapshot()
			row := result.Store.Producers[0]
			if err == nil || result.PrefixesClosed || row.TerminalFencedEOF || row.Closed || result.Store.Transactions != transactions ||
				mode == "call" && (row.Calls != 1 || result.Store.Rows != 512) || mode == "uuid" && row.Transactions != 1 {
				t.Fatal("arm refusal lost live prefix", result, err)
			}
		})
	}
}

func TestTransportTerminalEOFRetirementAllowsOtherProducerAdvance(t *testing.T) {
	for _, mode := range []string{"healthy", "lateSubmit", "lateCheckpoint"} {
		t.Run(mode, func(t *testing.T) {
			transport := terminalEOFMixedFixture(t)
			var err error
			switch mode {
			case "healthy":
				return
			case "lateSubmit":
				_, err = transport.controller.Submit(Request{Producer: 4, Phase: 9, Kind: ImplicitWrite})
			case "lateCheckpoint":
				err = transport.controller.Checkpoint(4, 9)
			}
			if !errors.Is(err, ErrProtocol) {
				t.Fatal("retired producer reentered later phase", err)
			}
			result, err := transport.Snapshot()
			if err == nil || result.PrefixesClosed || result.Store.Transactions != 3 || result.Store.Rows != 6 || !result.Store.Producers[0].TerminalFencedEOF {
				t.Fatal("late failure lost prefix or stayed complete", result, err)
			}
		})
	}
}

func terminalEOFMixedFixture(t *testing.T) *Transport {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	controller, err := New(ctx, Config{Producers: []Producer{{ID: 4, Calls: 1, Transactions: 1}, {ID: 5, Calls: 1, Transactions: 1}},
		Phases: []Phase{{ID: 8, Transactions: 2, Rows: 4}, {ID: 9, Transactions: 1, Rows: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewTransport(ctx, controller, WireConfig{AckTimeout: time.Second, Producers: []WireProducer{
		{ID: 4, Binding: [32]byte{4}, Phases: 1 << 7}, {ID: 5, Binding: [32]byte{5}, Phases: 1<<7 | 1<<8}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	first, second := transportClient(t, transport, ctx, 4), transportClient(t, transport, ctx, 5)
	submitSettle(t, ctx, first, ImplicitWrite, 0, 3)
	submitSettle(t, ctx, second, ImplicitWrite, 0, 1)
	if transport.Fence() != nil || first.Checkpoint(ctx) != nil || transport.ArmTerminalEOF(4, 8) != nil {
		t.Fatal("first checkpoint/arm refused")
	}
	if err := first.conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if err := transport.Advance(); !errors.Is(err, ErrBusy) {
		t.Fatal("uncheckpointed second producer advanced", err)
	}
	if second.Checkpoint(ctx) != nil || transport.Advance() != nil || second.Resume(9) != nil {
		t.Fatal("remaining producer could not advance")
	}
	submitSettle(t, ctx, second, ImplicitWrite, 0, 2)
	if second.Close(ctx) != nil || transport.Wait(ctx, 5) != nil || transport.Fence() != nil {
		t.Fatal("ordinary producer close failed")
	}
	result, err := transport.Snapshot()
	if err != nil || result.Complete || !result.PrefixesClosed || !result.Store.PrefixesClosed || result.TerminalEOF != 2 ||
		result.Store.Transactions != 3 || result.Store.Rows != 6 || result.Store.MaximumRows != 3 || result.ReservedBytes != 13*pairBytes ||
		!result.Store.Producers[0].TerminalFencedEOF || result.Store.Producers[0].Closed || !result.Store.Producers[1].Closed || result.Store.Producers[1].TerminalFencedEOF {
		t.Fatal("mixed retirement lost attribution", result, err)
	}
	return transport
}

func TestTransportTerminalEOFArmSerializesFailureAndSubmit(t *testing.T) {
	for _, mode := range []string{"submit", "failure"} {
		t.Run(mode, func(t *testing.T) {
			transport, _, _, _ := terminalEOFFixture(t)
			start, results := make(chan struct{}), make(chan error, 2)
			go func() {
				<-start
				results <- transport.ArmTerminalEOF(4, 8)
			}()
			go func() {
				<-start
				var err error
				if mode == "submit" {
					_, err = transport.controller.Submit(Request{Producer: 4, Phase: 8, Kind: ImplicitWrite})
				} else {
					err = transport.controller.Fail(ErrTransport)
				}
				results <- err
			}()
			close(start)
			first, second := <-results, <-results
			result, err := transport.Snapshot()
			if first == nil && second == nil || err == nil || result.PrefixesClosed || result.Store.Transactions != 1 || result.Store.Rows != 512 || result.Store.Producers[0].Calls != 0 {
				t.Fatal("concurrent refusal changed accepted prefix", first, second, result, err)
			}
		})
	}
}

func TestTransportTerminalEOFRefusesOtherProducerSlots(t *testing.T) {
	for _, kind := range []Kind{ImplicitWrite, Begin} {
		t.Run(map[Kind]string{ImplicitWrite: "call", Begin: "uuid"}[kind], func(t *testing.T) {
			transport, ctx := transportFixture(t, []uint32{4, 5}, []Phase{{ID: 8, Transactions: 1, Rows: 3}}, time.Second)
			first, second := transportClient(t, transport, ctx, 4), transportClient(t, transport, ctx, 5)
			rows := uint64(0)
			if kind == ImplicitWrite {
				rows = 3
			}
			submission, err := second.Submit(ctx, kind, 0, rows)
			if err != nil {
				t.Fatal(err)
			}
			if kind == Begin {
				if err := second.Settle(ctx, submission); err != nil {
					t.Fatal(err)
				}
			}
			if transport.Fence() != nil || first.Checkpoint(ctx) != nil {
				t.Fatal("empty target checkpoint failed")
			}
			if err := transport.ArmTerminalEOF(4, 8); !errors.Is(err, ErrProtocol) {
				t.Fatal("other producer's live reservation ignored", err)
			}
			result, err := transport.Snapshot()
			other := result.Store.Producers[1]
			if err == nil || result.PrefixesClosed || result.Store.Transactions != 1 || result.Store.Rows != rows ||
				kind == ImplicitWrite && other.Calls != 1 || kind == Begin && other.Transactions != 1 {
				t.Fatal("other producer prefix lost", result, err)
			}
		})
	}
}

func TestTransportTerminalEOFACKWriteFailureAfterArm(t *testing.T) {
	transport, ctx := transportFixture(t, []uint32{4}, []Phase{{ID: 8}}, time.Second)
	conn, frame := rawTransportClient(t, transport, 4)
	parent := transport.peers[0].conn
	if err := parent.SetWriteBuffer(1024); err != nil {
		t.Fatal(err)
	}
	rawConn, err := parent.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	// Fill only this private test socket's outbound buffer. The peer does not
	// read it: the receiver's next checkpoint ACK must block, not race the arm.
	blocked := false
	var fillErr error
	if err := rawConn.Control(func(fd uintptr) {
		var bytes [4096]byte
		for total := 0; total < 1<<20; {
			n, err := syscall.SendmsgN(int(fd), bytes[:], nil, nil, syscall.MSG_DONTWAIT)
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				blocked = true
				break
			}
			if err != nil || n == 0 {
				fillErr = err
				break
			}
			total += n
		}
	}); err != nil || fillErr != nil || !blocked {
		t.Fatal("could not bound private ACK backpressure", err, fillErr)
	}
	if err := transport.Fence(); err != nil {
		t.Fatal(err)
	}
	frame.op, frame.sequence = opCheckpoint, 2
	raw := frame.encode()
	if _, err := conn.Write(raw[:]); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := transport.controller.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Producers[0].Checkpoint == 8 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("checkpoint never committed")
		}
	}
	if err := transport.ArmTerminalEOF(4, 8); err != nil {
		t.Fatal("post-commit arm raced ACK publication", err)
	}
	// Closing the remote read half breaks that blocked ACK; closing its write
	// half cannot turn the earlier ACK failure into admitted terminal EOF.
	if err := conn.CloseRead(); err != nil {
		t.Fatal(err)
	}
	_ = conn.CloseWrite()
	if err := transport.Wait(ctx, 4); !errors.Is(err, ErrTransport) {
		t.Fatal("ACK-write failure was hidden by arm", err)
	}
	snapshot, err := transport.Snapshot()
	if err == nil || snapshot.PrefixesClosed || snapshot.TerminalEOF != 0 || snapshot.Store.Producers[0].TerminalFencedEOF {
		t.Fatal("failed ACK fabricated EOF", snapshot, err)
	}
}
