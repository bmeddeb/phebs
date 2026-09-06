package t421

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestExecutionStoreFlowExactBudgetsAndTopology(t *testing.T) {
	plan := accountingTestPlan(t)
	bindings := testExecutionDispatchBindings()
	config, wire, err := executionStoreConfig(plan, bindings)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := [...]uint32{2, 3, 4, 5, 6, 10, 11}
	wantMasks := [...]uint16{0x000e, 0x0010, 0x00e0, 0x0780, 0x3800, 0x0800, 0x0800}
	if len(config.Producers) != len(wantIDs) || len(wire.Producers) != len(wantIDs) ||
		len(config.Phases) != 15 || wire.AckTimeout != 5*time.Second {
		t.Fatalf("unexpected topology: %+v / %+v", config, wire)
	}
	for index, id := range wantIDs {
		calls, transactions := 40, 2
		if id >= 10 {
			calls, transactions = 1, 1
		}
		if config.Producers[index] != (storeaccounting.Producer{ID: id, Calls: calls, Transactions: transactions}) ||
			wire.Producers[index] != (storeaccounting.WireProducer{ID: id, Binding: bindings[id-1], Phases: wantMasks[index]}) {
			t.Fatalf("producer %d differs from actual lifetime", id)
		}
	}
	var totalTransactions, totalRows uint64
	for index, phase := range config.Phases {
		bounds := plan.WorkEnvelope.Phases[index]
		if phase != (storeaccounting.Phase{ID: uint32(index + 1), Transactions: bounds.StoreTransactions.Maximum, Rows: bounds.StoreRows.Maximum}) {
			t.Fatalf("phase %d changed a frozen ceiling", index+1)
		}
		totalTransactions += phase.Transactions
		totalRows += phase.Rows
	}
	controller, err := storeaccounting.New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(t.Context(), controller, wire)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); !errors.Is(err, storeaccounting.ErrIncomplete) {
			t.Errorf("never-opened lifetimes closed as complete: %v", err)
		}
	})
	snapshot, err := transport.Snapshot()
	if err != nil || snapshot.Complete || snapshot.Store.Complete || snapshot.Opened != 0 ||
		snapshot.ReservedBytes != 0 || snapshot.MaximumBytes == 0 || snapshot.Store.Transactions != 0 || snapshot.Store.Rows != 0 {
		t.Fatalf("construction invented work: %+v / %v", snapshot, err)
	}
	if totalTransactions != 507170 || totalRows != 259671040 || snapshot.MaximumBytes != 66735462912 {
		t.Fatalf("frozen store/wire ceilings changed: %d/%d/%d", totalTransactions, totalRows, snapshot.MaximumBytes)
	}
	t.Logf("store ceilings: transactions=%d rows=%d SA01 bytes=%d", totalTransactions, totalRows, snapshot.MaximumBytes)
	if err := transport.Close(); !errors.Is(err, storeaccounting.ErrIncomplete) {
		t.Fatalf("never-opened lifetimes closed as complete: %v", err)
	}

	// Returned slices belong to the caller, not the plan or a later constructor.
	wantConfig, wantWire, err := executionStoreConfig(plan, bindings)
	if err != nil {
		t.Fatal(err)
	}
	config.Producers[0].Calls = 1
	config.Phases[0].Transactions++
	wire.Producers[0].Phases = 1
	freshConfig, freshWire, err := executionStoreConfig(plan, bindings)
	if err != nil || !reflect.DeepEqual(wantConfig, freshConfig) || !reflect.DeepEqual(wantWire, freshWire) {
		t.Fatal("caller mutation changed a later store configuration")
	}
}

func TestExecutionStoreFlowRefusesUnboundOrChangedPlans(t *testing.T) {
	for _, mode := range []string{"zero-server-binding", "zero-root-binding", "duplicate-binding", "v2", "missing-accounting", "missing-phase", "phase-order", "transactions", "rows", "row-cap", "physical-history"} {
		t.Run(mode, func(t *testing.T) {
			plan := accountingTestPlan(t)
			bindings := testExecutionDispatchBindings()
			switch mode {
			case "zero-server-binding":
				bindings[1] = [32]byte{}
			case "zero-root-binding":
				bindings[0] = [32]byte{}
			case "duplicate-binding":
				bindings[10] = bindings[1]
			case "v2":
				plan.Schema = PlanV2Schema
			case "missing-accounting":
				plan.ProcessAccounting = nil
			case "missing-phase":
				plan.WorkEnvelope.Phases = plan.WorkEnvelope.Phases[:14]
			case "phase-order":
				plan.PhaseOrder[0] = "invented"
			case "transactions":
				plan.WorkEnvelope.Phases[4].StoreTransactions.Maximum++
			case "rows":
				plan.WorkEnvelope.Phases[4].StoreRows.Maximum++
			case "row-cap":
				plan.WorkEnvelope.MaximumStoreRowsPerTransaction++
			case "physical-history":
				plan.Revisions.Physical = nil
			}
			config, wire, err := executionStoreConfig(plan, bindings)
			if err == nil || !reflect.DeepEqual(config, storeaccounting.Config{}) || !reflect.DeepEqual(wire, storeaccounting.WireConfig{}) {
				t.Fatal("unbound or changed flow admitted")
			}
		})
	}
}
