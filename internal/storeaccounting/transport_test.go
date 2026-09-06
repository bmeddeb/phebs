//go:build darwin || linux

package storeaccounting

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"testing"
	"time"
)

func transportFixture(t *testing.T, ids []uint32, phases []Phase, timeout time.Duration) (*Transport, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	var producers []Producer
	var wires []WireProducer
	var mask uint16
	for _, phase := range phases {
		mask |= 1 << (phase.ID - 1)
	}
	for _, id := range ids {
		producers = append(producers, Producer{ID: id, Calls: MaximumCalls, Transactions: MaximumTransactions})
		wires = append(wires, WireProducer{ID: id, Binding: [32]byte{byte(id)}, Phases: mask})
	}
	c, err := New(ctx, Config{Producers: producers, Phases: phases})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewTransport(ctx, c, WireConfig{Producers: wires, AckTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport, ctx
}

func transportClient(t *testing.T, transport *Transport, ctx context.Context, id uint32) *Client {
	t.Helper()
	file, config, err := transport.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ctx, file, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Stat(); err == nil {
		t.Fatal("adoption retained original child FD")
	}
	t.Cleanup(client.closeOwned)
	return client
}

func submitSettle(t *testing.T, ctx context.Context, client *Client, kind Kind, transaction, rows uint64) Submission {
	t.Helper()
	submission, err := client.Submit(ctx, kind, transaction, rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Settle(ctx, submission); err != nil {
		t.Fatal(err)
	}
	return submission
}

func TestTransportActualPipeTransactionsAndClosure(t *testing.T) {
	transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1, Transactions: 2, Rows: 1024}}, time.Second)
	client := transportClient(t, transport, ctx, 2)
	submitSettle(t, ctx, client, ImplicitWrite, 0, 512)
	begin := submitSettle(t, ctx, client, Begin, 0, 0)
	if err := client.Close(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("live UUID Close: %v", err)
	}
	if err := client.Checkpoint(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("live UUID Checkpoint: %v", err)
	}
	submitSettle(t, ctx, client, Append, begin.Transaction, 500)
	submitSettle(t, ctx, client, Append, begin.Transaction, 12)
	submitSettle(t, ctx, client, Commit, begin.Transaction, 0)
	if err := transport.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Submit(ctx, ImplicitWrite, 0, 0); !errors.Is(err, ErrFenced) {
		t.Fatalf("post checkpoint: %v", err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 2); err != nil {
		t.Fatal(err)
	}
	snapshot, err := transport.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Store.Transactions != 2 || snapshot.Store.Rows != 1024 || snapshot.Store.MaximumRows != 512 || snapshot.TerminalEOF != 1 {
		t.Fatalf("snapshot %+v: %v", snapshot, err)
	}
	// Attach + five Submit/Settle pairs + checkpoint + Close + reserved EOF.
	if snapshot.ReservedBytes != 14*pairBytes {
		t.Fatalf("reserved wire %d", snapshot.ReservedBytes)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTransportPhaseHandoffAndIndependentProducerClose(t *testing.T) {
	transport, ctx := transportFixture(t, []uint32{2, 3}, []Phase{{ID: 1, Transactions: 3, Rows: 6}, {ID: 2, Transactions: 1, Rows: 1}}, time.Second)
	first := transportClient(t, transport, ctx, 2)
	second := transportClient(t, transport, ctx, 3)
	one := submitSettle(t, ctx, first, ImplicitWrite, 0, 2)
	two, err := second.Submit(ctx, ImplicitWrite, 0, 2)
	if err != nil || two.Ordinal <= one.Ordinal {
		t.Fatalf("global tokens: %+v %+v %v", one, two, err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := second.Settle(ctx, two); err != nil {
		t.Fatal(err)
	}
	if err := transport.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := second.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := second.Resume(2); err != nil {
		t.Fatal(err)
	}
	submitSettle(t, ctx, second, ImplicitWrite, 0, 1)
	if err := second.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := transport.Fence(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := transport.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Store.Rows != 5 {
		t.Fatalf("snapshot %+v: %v", snapshot, err)
	}
}

func rawTransportClient(t *testing.T, transport *Transport, id uint32) (*net.UnixConn, wireFrame) {
	t.Helper()
	file, config, err := transport.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adopt(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	f := wireFrame{op: opAttach, phase: config.Phase, sequence: 1, binding: config.Binding}
	rawExchange(t, conn, f)
	return conn, f
}

func rawExchange(t *testing.T, conn *net.UnixConn, frame wireFrame) wireFrame {
	t.Helper()
	raw := frame.encode()
	if n, err := conn.Write(raw[:]); err != nil || n != len(raw) {
		t.Fatalf("write %d: %v", n, err)
	}
	var ack [FrameBytes]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		t.Fatal(err)
	}
	reply, err := decodeFrame(ack, true)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestTransportRejectsMalformedAndPrematureEOF(t *testing.T) {
	for _, name := range []string{"binding", "sequence", "phase", "reserved", "kind", "rows", "partial", "eof", "fail"} {
		t.Run(name, func(t *testing.T) {
			transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1, Transactions: 1, Rows: 512}}, 100*time.Millisecond)
			conn, frame := rawTransportClient(t, transport, 2)
			frame.op, frame.kind, frame.sequence, frame.rows = opSubmit, byte(ImplicitWrite), 2, 1
			switch name {
			case "binding":
				frame.binding[31]++
			case "sequence":
				frame.sequence++
			case "phase":
				frame.phase++
			case "kind":
				frame.kind = 9
			case "rows":
				frame.rows = 513
			case "fail":
				frame.op, frame.kind, frame.rows = opFail, failureKind(ErrDescriptor), 0
			}
			raw := frame.encode()
			if name == "reserved" {
				raw[12] = 1
			}
			if name == "eof" {
				_ = conn.CloseWrite()
			} else {
				data := raw[:]
				if name == "partial" {
					data = raw[:1]
				}
				if _, err := conn.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := transport.Wait(ctx, 2); err == nil {
				t.Fatal("invalid request accepted")
			}
			snapshot, err := transport.Snapshot()
			if err == nil || snapshot.Complete || snapshot.Store.Rows != 0 || snapshot.Store.Transactions != 0 {
				t.Fatalf("snapshot %+v: %v", snapshot, err)
			}
		})
	}
}

func TestTransportCloseRequiresTimedTerminalEOF(t *testing.T) {
	for _, name := range []string{"half-close", "extra-byte", "timeout"} {
		t.Run(name, func(t *testing.T) {
			transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1}}, 100*time.Millisecond)
			conn, frame := rawTransportClient(t, transport, 2)
			frame.op, frame.sequence = opClose, 2
			rawExchange(t, conn, frame)
			switch name {
			case "half-close":
				_ = conn.CloseWrite()
			case "extra-byte":
				_, _ = conn.Write([]byte{1})
			}
			err := transport.Wait(ctx, 2)
			if (err == nil) != (name == "half-close") {
				t.Fatalf("terminal %s: %v", name, err)
			}
			snapshot, _ := transport.Snapshot()
			if !snapshot.Store.Producers[0].Closed || (snapshot.TerminalEOF == 1) != (name == "half-close") {
				t.Fatalf("closure %+v", snapshot)
			}
		})
	}
}

func TestTransportLostSubmitACKRetainsParentPrefix(t *testing.T) {
	transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1, Transactions: 1, Rows: 512}}, time.Second)
	conn, frame := rawTransportClient(t, transport, 2)
	if err := conn.CloseRead(); err != nil {
		t.Fatal(err)
	}
	frame.op, frame.kind, frame.sequence, frame.rows = opSubmit, byte(ImplicitWrite), 2, 512
	raw := frame.encode()
	if _, err := conn.Write(raw[:]); err != nil {
		t.Fatal(err)
	}
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 2); err == nil {
		t.Fatal("lost ACK passed")
	}
	snapshot, err := transport.Snapshot()
	if err == nil || snapshot.Store.Rows != 512 || snapshot.Store.Transactions != 1 || snapshot.Store.Producers[0].Calls != 1 || snapshot.Complete {
		t.Fatalf("lost prefix %+v: %v", snapshot, err)
	}
	if _, _, err := transport.Open(2); err == nil {
		t.Fatal("failed lifetime reopened")
	}
}

func TestClientRefusalRetainsPriorPrefixAndNeverReusesSlots(t *testing.T) {
	for _, name := range []string{"row-overflow", "token-alias", "duplicate-settle", "call-cap", "cancel", "native-uncertain"} {
		t.Run(name, func(t *testing.T) {
			transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1, Transactions: 100, Rows: 1000}}, time.Second)
			client := transportClient(t, transport, ctx, 2)
			begin := submitSettle(t, ctx, client, Begin, 0, 0)
			submitSettle(t, ctx, client, Append, begin.Transaction, 512)
			var err error
			switch name {
			case "row-overflow":
				_, err = client.Submit(ctx, Append, begin.Transaction, 1)
			case "token-alias":
				_, err = client.Submit(ctx, Append, begin.Transaction+1, 1)
			case "duplicate-settle":
				err = client.Settle(ctx, begin)
			case "call-cap":
				for i := 0; i < MaximumCalls; i++ {
					if _, err = client.Submit(ctx, ImplicitWrite, 0, 0); err != nil {
						t.Fatal(err)
					}
				}
				_, err = client.Submit(ctx, ImplicitWrite, 0, 0)
			case "cancel":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				_, err = client.Submit(canceled, Append, begin.Transaction, 1)
			case "native-uncertain":
				err = client.Fail(ctx, ErrIncomplete)
			}
			if err == nil {
				t.Fatal("refusal missing")
			}
			if _, retry := client.Submit(ctx, ImplicitWrite, 0, 0); retry == nil {
				t.Fatal("failure retried")
			}
			if wait := transport.Wait(ctx, 2); wait == nil {
				t.Fatal("uncertain receiver completed")
			}
			snapshot, _ := transport.Snapshot()
			if snapshot.Store.Rows != 512 || snapshot.Store.Producers[0].Transactions != 1 || snapshot.Complete {
				t.Fatalf("prefix %+v", snapshot)
			}
		})
	}
}

func TestTransportConcurrentConstructorHasOneOwner(t *testing.T) {
	controller, err := New(t.Context(), Config{Producers: []Producer{{ID: 2, Calls: 1, Transactions: 1}}, Phases: []Phase{{ID: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	config := WireConfig{Producers: []WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 1}}, AckTimeout: time.Second}
	bad := config
	bad.AckTimeout = 0
	if _, err := NewTransport(t.Context(), controller, bad); err == nil {
		t.Fatal("invalid constructor")
	}
	var results [2]*Transport
	var failures [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i], failures[i] = NewTransport(t.Context(), controller, config) })
	}
	wg.Wait()
	winners := 0
	for i, result := range results {
		if result != nil {
			winners++
			_ = result.Close()
		} else if !errors.Is(failures[i], ErrConfig) {
			t.Fatal(failures[i])
		}
	}
	if winners != 1 {
		t.Fatalf("owners %d", winners)
	}
}

func TestTransportReservedBudgetAndOverflow(t *testing.T) {
	got, err := wireBudget(507170, 259671040, 16, 7)
	if err != nil || got != 66735462912 {
		t.Fatalf("full conservative reserve %d: %v", got, err)
	}
	for _, input := range [][4]uint64{{math.MaxUint64, 0, 0, 1}, {0, 0, math.MaxUint64, 1}, {1, 1, 1, math.MaxUint64}, {math.MaxUint64 / 512, math.MaxUint64, 1, 1}} {
		if _, err := wireBudget(input[0], input[1], input[2], input[3]); err == nil {
			t.Fatalf("overflow accepted %+v", input)
		}
	}
}

func TestTransportWireConfigurationRefusals(t *testing.T) {
	for _, name := range []string{"missing", "unknown-id", "zero-binding", "duplicate-binding", "duplicate-id", "empty-mask", "extra-mask", "overflow"} {
		t.Run(name, func(t *testing.T) {
			phases := []Phase{{ID: 1, Transactions: 10, Rows: 5120}}
			if name == "overflow" {
				phases[0].Transactions = math.MaxUint64
			}
			controller, err := New(t.Context(), Config{Producers: []Producer{{ID: 2, Calls: 1, Transactions: 1}, {ID: 3, Calls: 1, Transactions: 1}}, Phases: phases})
			if err != nil {
				t.Fatal(err)
			}
			config := WireConfig{AckTimeout: time.Second, Producers: []WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 1}, {ID: 3, Binding: [32]byte{2}, Phases: 1}}}
			switch name {
			case "missing":
				config.Producers = config.Producers[:1]
			case "unknown-id":
				config.Producers[1].ID = 1
			case "zero-binding":
				config.Producers[1].Binding = [32]byte{}
			case "duplicate-binding":
				config.Producers[1].Binding = config.Producers[0].Binding
			case "duplicate-id":
				config.Producers[1].ID = 2
			case "empty-mask":
				config.Producers[1].Phases = 0
			case "extra-mask":
				config.Producers[1].Phases = 3
			}
			if _, err := NewTransport(t.Context(), controller, config); !errors.Is(err, ErrConfig) {
				t.Fatalf("configuration %s: %v", name, err)
			}
			if controller.wireOwned {
				t.Fatal("invalid constructor claimed controller")
			}
		})
	}
}

func TestTransportConcurrentSubmitSettleAndOwnerCancellation(t *testing.T) {
	transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1, Transactions: 100, Rows: 100}}, time.Second)
	client := transportClient(t, transport, ctx, 2)
	var workers sync.WaitGroup
	var outcomes [20]error
	for i := range outcomes {
		workers.Go(func() {
			for turn := 0; turn < 5; turn++ {
				submission, err := client.Submit(ctx, ImplicitWrite, 0, 1)
				if err == nil {
					err = client.Settle(ctx, submission)
				}
				if err != nil {
					outcomes[i] = err
					return
				}
			}
		})
	}
	workers.Wait()
	for _, err := range outcomes {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := transport.Snapshot()
	if err != nil || snapshot.Store.Transactions != 100 || snapshot.Store.Rows != 100 || snapshot.Store.Producers[0].Calls != 0 {
		t.Fatalf("concurrent prefix %+v: %v", snapshot, err)
	}
	transport.cancel()
	if err := transport.Wait(ctx, 2); err == nil {
		t.Fatal("canceled live stream completed")
	}
	if err := client.Close(ctx); err == nil {
		t.Fatal("canceled parent supplied closure")
	}
}

func TestTransportRetainedObjectsCloseFDsWithoutGC(t *testing.T) {
	old := debug.SetGCPercent(-1)
	t.Cleanup(func() { debug.SetGCPercent(old) })
	// Warm netpoll before measuring, retaining every subsequently closed object.
	measure := func() int {
		directory, err := os.Open("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		entries, err := directory.Readdirnames(-1)
		closeErr := directory.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("FD names: %v, close: %v", err, closeErr)
		}
		return len(entries)
	}
	var retained [33]*Client
	before := 0
	for i := range retained {
		transport, ctx := transportFixture(t, []uint32{2}, []Phase{{ID: 1}}, time.Second)
		retained[i] = transportClient(t, transport, ctx, 2)
		if err := retained[i].Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := transport.Wait(ctx, 2); err != nil {
			t.Fatal(err)
		}
		if err := transport.Close(); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			before = measure()
		}
	}
	if after := measure(); after != before {
		t.Fatalf("retained closed clients: FDs %d -> %d", before, after)
	}
}
