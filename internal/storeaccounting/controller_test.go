package storeaccounting

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		Producers: []Producer{{ID: 2, Calls: 40, Transactions: 2}, {ID: 10, Calls: 1, Transactions: 1}},
		Phases:    []Phase{{ID: 2, Transactions: 170, Rows: 170 * 512}, {ID: 12, Transactions: 170, Rows: 170 * 512}},
	}
}

func controllerForTest(t *testing.T, config Config) *Controller {
	t.Helper()
	c, err := New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Attach(config.Producers[0].ID, config.Phases[0].ID); err != nil {
		t.Fatal(err)
	}
	return c
}

func submitForTest(t *testing.T, c *Controller, kind Kind, tx, rows uint64) Submission {
	t.Helper()
	submission, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: kind, Transaction: tx, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

func settleForTest(t *testing.T, c *Controller, submission Submission) {
	t.Helper()
	if err := c.Settle(submission); err != nil {
		t.Fatal(err)
	}
}

func snapshotForTest(t *testing.T, c *Controller) Snapshot {
	t.Helper()
	snapshot, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestLogicalTransactionRowsAndNoCarry(t *testing.T) {
	c := controllerForTest(t, testConfig())
	begin := submitForTest(t, c, Begin, 0, 0)
	if got := snapshotForTest(t, c); got.Transactions != 1 || got.Rows != 0 || got.Producers[0].Calls != 1 || got.Producers[0].Transactions != 1 {
		t.Fatalf("accepted Begin prefix = %+v", got)
	}
	settleForTest(t, c, begin)
	for _, rows := range []uint64{1, 510, 1} {
		settleForTest(t, c, submitForTest(t, c, Append, begin.Transaction, rows))
	}
	got := snapshotForTest(t, c)
	if got.Transactions != 1 || got.Rows != 512 || got.MaximumRows != 512 || got.Phases[0].MaximumRows != 512 {
		t.Fatalf("split row accounting = %+v", got)
	}
	if err := c.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := c.Checkpoint(2, 2); !errors.Is(err, ErrBusy) {
		t.Fatalf("open UUID crossed checkpoint: %v", err)
	}
	if err := c.Close(2); !errors.Is(err, ErrBusy) {
		t.Fatalf("open UUID closed: %v", err)
	}
	// A terminal operation remains admissible while globally fenced. Its ACK
	// alone cannot release the UUID or authorize a phase handoff.
	terminal := submitForTest(t, c, Cancel, begin.Transaction, 0)
	if err := c.Checkpoint(2, 2); !errors.Is(err, ErrBusy) {
		t.Fatalf("pending Cancel crossed checkpoint: %v", err)
	}
	settleForTest(t, c, terminal)
	if err := c.Checkpoint(2, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := c.Attach(10, 12); err != nil {
		t.Fatal(err)
	}
	for _, producer := range []uint32{2, 10} {
		if err := c.Close(producer); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Fence(); err != nil {
		t.Fatal(err)
	}
	got = snapshotForTest(t, c)
	if !got.Complete || got.Transactions != 1 || got.Rows != 512 || got.Phases[1].Transactions != 0 {
		t.Fatalf("final prefix = %+v", got)
	}
}

func TestEmptyWriteAndReadOnlyCanceledBeginCount(t *testing.T) {
	c := controllerForTest(t, testConfig())
	settleForTest(t, c, submitForTest(t, c, ImplicitWrite, 0, 0))
	for _, terminal := range []Kind{Commit, Cancel} {
		begin := submitForTest(t, c, Begin, 0, 0)
		settleForTest(t, c, begin)
		// A real adapter tracks any zero-row SELECT locally; it adds no new
		// logical transaction and cannot hide the already accepted Begin.
		settleForTest(t, c, submitForTest(t, c, terminal, begin.Transaction, 0))
	}
	if got := snapshotForTest(t, c); got.Transactions != 3 || got.Rows != 0 || got.MaximumRows != 0 || got.Producers[0].Transactions != 0 {
		t.Fatalf("empty transaction prefix = %+v", got)
	}
}

func TestRefusalRetainsExactPrefix(t *testing.T) {
	for _, test := range []struct {
		name   string
		config func(*Config)
		setup  func(*testing.T, *Controller) uint64
		kind   Kind
		rows   uint64
		want   error
	}{
		{name: "at transaction cap", config: func(c *Config) { c.Phases[0].Transactions = 1 }, setup: func(t *testing.T, c *Controller) uint64 {
			settleForTest(t, c, submitForTest(t, c, ImplicitWrite, 0, 7))
			return 0
		}, kind: ImplicitWrite, want: ErrLimit},
		{name: "zero transaction cap", config: func(c *Config) { c.Phases[0].Transactions = 0 }, kind: Begin, want: ErrLimit},
		{name: "phase row cap", config: func(c *Config) { c.Phases[0].Rows = 2 }, kind: ImplicitWrite, rows: 3, want: ErrLimit},
		{name: "single transaction row cap", kind: ImplicitWrite, rows: 513, want: ErrLimit},
		{name: "append row cap", setup: func(t *testing.T, c *Controller) uint64 {
			b := submitForTest(t, c, Begin, 0, 0)
			settleForTest(t, c, b)
			settleForTest(t, c, submitForTest(t, c, Append, b.Transaction, 512))
			return b.Transaction
		}, kind: Append, rows: 1, want: ErrLimit},
		{name: "overflow rows", kind: ImplicitWrite, rows: math.MaxUint64, want: ErrLimit},
		{name: "empty append descriptor", kind: Append, want: ErrDescriptor},
		{name: "Begin payload descriptor", kind: Begin, rows: 1, want: ErrDescriptor},
		{name: "unknown kind", kind: Kind(255), want: ErrDescriptor},
		{name: "fenced write", setup: func(t *testing.T, c *Controller) uint64 {
			if err := c.Fence(); err != nil {
				t.Fatal(err)
			}
			return 0
		}, kind: ImplicitWrite, want: ErrFenced},
		{name: "pending Begin terminal", setup: func(t *testing.T, c *Controller) uint64 {
			return submitForTest(t, c, Begin, 0, 0).Transaction
		}, kind: Commit, want: ErrProtocol},
		{name: "pending append terminal", setup: func(t *testing.T, c *Controller) uint64 {
			b := submitForTest(t, c, Begin, 0, 0)
			settleForTest(t, c, b)
			submitForTest(t, c, Append, b.Transaction, 3)
			return b.Transaction
		}, kind: Cancel, want: ErrProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig()
			if test.config != nil {
				test.config(&config)
			}
			c := controllerForTest(t, config)
			var tx uint64
			if test.setup != nil {
				tx = test.setup(t, c)
			}
			before := snapshotForTest(t, c)
			if _, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: test.kind, Transaction: tx, Rows: test.rows}); !errors.Is(err, test.want) {
				t.Fatalf("refusal = %v, want %v", err, test.want)
			}
			after, err := c.Snapshot()
			if !errors.Is(err, test.want) || !reflect.DeepEqual(before, after) || after.Complete {
				t.Fatalf("refusal changed accepted prefix: before=%+v after=%+v error=%v", before, after, err)
			}
			if err := c.Fail(ErrTransport); !errors.Is(err, test.want) {
				t.Fatalf("first failure replaced: %v", err)
			}
		})
	}
}

func TestUnknownTerminalRetainsReservations(t *testing.T) {
	for _, kind := range []Kind{Begin, Append, Commit, Cancel, ImplicitWrite} {
		t.Run(string(rune('0'+kind)), func(t *testing.T) {
			c := controllerForTest(t, testConfig())
			var tx, rows uint64
			if kind >= Append {
				begin := submitForTest(t, c, Begin, 0, 0)
				settleForTest(t, c, begin)
				tx = begin.Transaction
			}
			if kind == Append || kind == ImplicitWrite {
				rows = 3
			}
			submission := submitForTest(t, c, kind, tx, rows)
			before := snapshotForTest(t, c)
			if err := c.Fail(ErrTransport); !errors.Is(err, ErrTransport) {
				t.Fatal(err)
			}
			if err := c.Settle(submission); !errors.Is(err, ErrTransport) {
				t.Fatal(err)
			}
			if err := c.Close(2); !errors.Is(err, ErrTransport) {
				t.Fatal(err)
			}
			after, err := c.Snapshot()
			if !errors.Is(err, ErrTransport) || !reflect.DeepEqual(before, after) || after.Producers[0].Calls != 1 || after.Transactions != 1 {
				t.Fatalf("lost ACK/death erased prefix: %+v %v", after, err)
			}
		})
	}
}

func TestCapacityAndConcurrency(t *testing.T) {
	t.Run("forty live source calls", func(t *testing.T) {
		c := controllerForTest(t, testConfig())
		var group sync.WaitGroup
		for range 40 {
			group.Go(func() {
				if _, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: ImplicitWrite, Rows: 1}); err != nil {
					t.Error(err)
				}
			})
		}
		group.Wait()
		before := snapshotForTest(t, c)
		if before.Transactions != 40 || before.Rows != 40 || before.Producers[0].Calls != 40 {
			t.Fatalf("bounded calls = %+v", before)
		}
		if _, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: ImplicitWrite}); !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
		after, _ := c.Snapshot()
		if !reflect.DeepEqual(before, after) {
			t.Fatal("capacity refusal counted")
		}
	})
	t.Run("two all-UUID reservations", func(t *testing.T) {
		c := controllerForTest(t, testConfig())
		for range 2 {
			settleForTest(t, c, submitForTest(t, c, Begin, 0, 0))
		}
		if _, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: Begin}); !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
		snapshot, _ := c.Snapshot()
		if snapshot.Transactions != 2 || snapshot.Producers[0].Transactions != 2 || snapshot.Producers[0].Calls != 0 {
			t.Fatalf("UUID reservation = %+v", snapshot)
		}
	})
	t.Run("CLI one call one UUID", func(t *testing.T) {
		config := testConfig()
		config.Producers = []Producer{{ID: 2, Calls: 1, Transactions: 1}}
		c := controllerForTest(t, config)
		begin := submitForTest(t, c, Begin, 0, 0)
		settleForTest(t, c, begin)
		if _, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: Begin}); !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
	})
}

func TestConfigurationCopiesAndPhaseClosure(t *testing.T) {
	config := testConfig()
	c := controllerForTest(t, config)
	config.Phases[0].Transactions, config.Producers[0].Calls = 0, 0
	settleForTest(t, c, submitForTest(t, c, ImplicitWrite, 0, 1))
	snapshot := snapshotForTest(t, c)
	snapshot.Phases[0].Rows, snapshot.Producers[0].Ordinal = 100, 100
	if got := snapshotForTest(t, c); got.Phases[0].Rows != 1 || got.Producers[0].Ordinal != 1 {
		t.Fatal("snapshot aliases reducer")
	}
	if err := c.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if err := c.Checkpoint(2, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(2); err != nil {
		t.Fatal(err)
	}
	if err := c.Fence(); err != nil {
		t.Fatal(err)
	}
	if snapshotForTest(t, c).Complete {
		t.Fatal("never-attached required lifetime counted complete")
	}
}

func TestConfigurationRefusal(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Phases = nil }, func(c *Config) { c.Producers = nil },
		func(c *Config) { c.Phases[0].ID = 0 }, func(c *Config) { c.Phases[1].ID = 16 },
		func(c *Config) { c.Phases[1].ID = 2 }, func(c *Config) { c.Phases[1].ID = 1 },
		func(c *Config) { c.Phases[0].Transactions = math.MaxUint64 },
		func(c *Config) { c.Phases[0].Rows = math.MaxUint64 },
		func(c *Config) { c.Producers[1].ID = 2 }, func(c *Config) { c.Producers[0].ID = 0 },
		func(c *Config) { c.Producers[0].Calls = 0 }, func(c *Config) { c.Producers[0].Calls = 41 },
		func(c *Config) { c.Producers[0].Transactions = 0 }, func(c *Config) { c.Producers[0].Transactions = 3 },
		func(c *Config) { c.Producers = make([]Producer, 8) }, func(c *Config) { c.Phases = make([]Phase, 16) },
	} {
		config := testConfig()
		mutate(&config)
		if _, err := New(t.Context(), config); !errors.Is(err, ErrConfig) {
			t.Fatalf("invalid config admitted: %+v %v", config, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := New(ctx, testConfig()); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	var absent *Controller
	if _, err := absent.Submit(Request{}); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	if _, err := (&Controller{}).Snapshot(); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
}

func TestInvalidIdentitySettlementAndCancellation(t *testing.T) {
	for _, mutate := range []func(*Submission){
		func(s *Submission) { s.Ordinal++ }, func(s *Submission) { s.Transaction++ }, func(s *Submission) { s.Producer = 11 },
	} {
		c := controllerForTest(t, testConfig())
		submission := submitForTest(t, c, Begin, 0, 0)
		before := snapshotForTest(t, c)
		mutate(&submission)
		if err := c.Settle(submission); !errors.Is(err, ErrProtocol) {
			t.Fatal(err)
		}
		after, _ := c.Snapshot()
		if !reflect.DeepEqual(before, after) {
			t.Fatal("bad settlement erased prefix")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	c, err := New(ctx, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Attach(2, 2); err != nil {
		t.Fatal(err)
	}
	submitForTest(t, c, ImplicitWrite, 0, 512)
	cancel()
	after, err := c.Snapshot()
	if !errors.Is(err, ErrCanceled) || after.Transactions != 1 || after.Rows != 512 || after.Producers[0].Calls != 1 {
		t.Fatalf("canceled prefix = %+v %v", after, err)
	}
}

func TestProtocolRefusalAndOrdinalOverflow(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Controller) error
		want error
	}{
		{"duplicate attachment", func(c *Controller) error { return c.Attach(2, 2) }, ErrProtocol},
		{"unattached close", func(c *Controller) error { return c.Close(10) }, ErrProtocol},
		{"unfenced checkpoint", func(c *Controller) error { return c.Checkpoint(2, 2) }, ErrProtocol},
		{"unfenced advance", func(c *Controller) error { return c.Advance() }, ErrProtocol},
		{"wrong phase", func(c *Controller) error {
			_, err := c.Submit(Request{Producer: 2, Phase: 12, Kind: ImplicitWrite})
			return err
		}, ErrProtocol},
		{"unattached source", func(c *Controller) error {
			_, err := c.Submit(Request{Producer: 10, Phase: 2, Kind: ImplicitWrite})
			return err
		}, ErrProtocol},
		{"unknown UUID", func(c *Controller) error {
			_, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: Append, Transaction: 42, Rows: 1})
			return err
		}, ErrProtocol},
		{"unknown failure sanitized", func(c *Controller) error { return c.Fail(errors.New("private source text")) }, ErrProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := controllerForTest(t, testConfig())
			before := snapshotForTest(t, c)
			if err := test.run(c); !errors.Is(err, test.want) {
				t.Fatalf("protocol refusal = %v", err)
			}
			after, err := c.Snapshot()
			if !errors.Is(err, test.want) || !reflect.DeepEqual(before, after) {
				t.Fatalf("protocol refusal changed prefix: %+v %v", after, err)
			}
		})
	}
	c := controllerForTest(t, testConfig())
	// Exercise the scalar overflow boundary without billions of submissions.
	c.ordinal = math.MaxUint64
	before := snapshotForTest(t, c)
	if _, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: ImplicitWrite}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	after, _ := c.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("ordinal overflow changed prefix")
	}
}

func TestLiveUUIDRejectsOverlappingAndOtherProducerCalls(t *testing.T) {
	for _, test := range []struct {
		name string
		kind Kind
		rows uint64
	}{
		{"append", Append, 1}, {"commit", Commit, 0}, {"cancel", Cancel, 0},
	} {
		t.Run("pending append rejects "+test.name, func(t *testing.T) {
			c := controllerForTest(t, testConfig())
			begin := submitForTest(t, c, Begin, 0, 0)
			settleForTest(t, c, begin)
			submitForTest(t, c, Append, begin.Transaction, 1)
			before := snapshotForTest(t, c)
			if _, err := c.Submit(Request{Producer: 2, Phase: 2, Kind: test.kind, Rows: test.rows, Transaction: begin.Transaction}); !errors.Is(err, ErrProtocol) {
				t.Fatal(err)
			}
			after, _ := c.Snapshot()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("overlapping UUID call changed prefix")
			}
		})
		t.Run("other producer rejects "+test.name, func(t *testing.T) {
			c := controllerForTest(t, testConfig())
			if err := c.Attach(10, 2); err != nil {
				t.Fatal(err)
			}
			first := submitForTest(t, c, Begin, 0, 0)
			settleForTest(t, c, first)
			second, err := c.Submit(Request{Producer: 10, Phase: 2, Kind: Begin})
			if err != nil {
				t.Fatal(err)
			}
			settleForTest(t, c, second)
			if first.Transaction == second.Transaction {
				t.Fatal("cross-producer transaction token alias")
			}
			before := snapshotForTest(t, c)
			if _, err := c.Submit(Request{Producer: 10, Phase: 2, Kind: test.kind, Rows: test.rows, Transaction: first.Transaction}); !errors.Is(err, ErrProtocol) {
				t.Fatal(err)
			}
			after, _ := c.Snapshot()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("cross-producer UUID call changed prefix")
			}
		})
	}
}
