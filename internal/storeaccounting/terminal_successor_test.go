//go:build darwin || linux

package storeaccounting

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// Socket/reducer evidence only; these peers prove no process kill or SDK health.
func terminalSuccessorFixture(t *testing.T) (*Transport, context.Context) {
	t.Helper()
	transport, ctx := transportFixture(t, []uint32{4, 5}, []Phase{{ID: 8, Transactions: 2, Rows: 512}}, time.Second)
	first := transportClient(t, transport, ctx, 4)
	submitSettle(t, ctx, first, ImplicitWrite, 0, 512)
	if transport.Fence() != nil || first.Checkpoint(ctx) != nil || transport.ArmTerminalEOF(4, 8) != nil {
		t.Fatal("terminal checkpoint refused")
	}
	if first.conn.CloseWrite() != nil || transport.Wait(ctx, 4) != nil {
		t.Fatal("actual terminal EOF did not join")
	}
	return transport, ctx
}

func TestTerminalSuccessorPreservesSamePhasePrefix(t *testing.T) {
	transport, ctx := terminalSuccessorFixture(t)
	before, err := transport.Snapshot()
	if err != nil || before.Store.Phase != 8 || before.PrefixesClosed {
		t.Fatal(before, err)
	}
	if err := transport.ReopenAfterTerminalEOF(4, 5, 8); err != nil {
		t.Fatal(err)
	}
	cutover, err := transport.Snapshot()
	if err != nil || !reflect.DeepEqual(before, cutover) {
		t.Fatal("cutover reset accepted prefix", before, cutover, err)
	}
	second := transportClient(t, transport, ctx, 5)
	if second.config.Phase != 8 {
		t.Fatal("successor moved phase")
	}
	submitSettle(t, ctx, second, ImplicitWrite, 0, 0)
	if second.Close(ctx) != nil || transport.Wait(ctx, 5) != nil || transport.Fence() != nil {
		t.Fatal("same-phase successor did not close")
	}
	after, err := transport.Snapshot()
	if err != nil || after.Complete || !after.PrefixesClosed || after.Store.Phase != 8 ||
		after.Store.Transactions != 2 || after.Store.Rows != 512 || after.Store.MaximumRows != 512 ||
		after.Store.Producers[0] != before.Store.Producers[0] || after.Store.Producers[1].Ordinal != 2 ||
		after.ReservedBytes != 10*pairBytes {
		t.Fatal("successor lost terminal accounting", after, err)
	}
}

func TestTerminalSuccessorRefusalsRetainPrefix(t *testing.T) {
	for _, mode := range []string{"wrong_predecessor", "same", "unknown", "phase", "opened", "repeat", "failed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			transport, _ := terminalSuccessorFixture(t)
			before, _ := transport.Snapshot()
			prior, next, phase := uint32(4), uint32(5), uint32(8)
			switch mode {
			case "wrong_predecessor":
				prior, next = 5, 4
			case "same":
				next = 4
			case "unknown":
				next = 6
			case "phase":
				phase = 9
			case "opened":
				file, _, err := transport.Open(5)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = file.Close() })
			case "repeat":
				if transport.ReopenAfterTerminalEOF(4, 5, 8) != nil || transport.Fence() != nil {
					t.Fatal("first cutover/fence failed")
				}
			case "failed":
				_ = transport.controller.Fail(ErrDescriptor)
			case "canceled":
				transport.cancel()
			}
			if err := transport.ReopenAfterTerminalEOF(prior, next, phase); err == nil {
				t.Fatal("invalid successor reopened")
			}
			after, err := transport.Snapshot()
			if err == nil || after.PrefixesClosed || after.Store.Transactions != before.Store.Transactions ||
				after.Store.Rows != before.Store.Rows || !reflect.DeepEqual(after.Store.Producers, before.Store.Producers) {
				t.Fatal("refusal erased accepted prefix", after, err)
			}
		})
	}
}
