package t421

import (
	"errors"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

var errExecutionStoreFlow = errors.New("T42.2 fixed operational store flow is invalid")

// executionStoreConfig projects the frozen V3 ceilings onto the actual five
// server and two archive-command lifetimes. It creates no controller, socket,
// child or admission binding; the SA01 transport derives its own wire ceiling.
func executionStoreConfig(plan Plan, bindings [executionProducerCount][32]byte) (storeaccounting.Config, storeaccounting.WireConfig, error) {
	if plan.Schema != PlanV3Schema || len(plan.PhaseOrder) != executionPhaseCount ||
		len(plan.WorkEnvelope.Phases) != executionPhaseCount || validatePlan(plan, &plan.Revisions) != nil {
		return storeaccounting.Config{}, storeaccounting.WireConfig{}, errExecutionStoreFlow
	}
	for index, binding := range bindings {
		if binding == ([32]byte{}) || slices.Contains(bindings[:index], binding) {
			return storeaccounting.Config{}, storeaccounting.WireConfig{}, errExecutionStoreFlow
		}
	}
	config := storeaccounting.Config{}
	wire := storeaccounting.WireConfig{AckTimeout: 5 * time.Second}
	for _, id := range [...]uint32{2, 3, 4, 5, 6, 10, 11} {
		producer := storeaccounting.Producer{ID: id, Calls: 40, Transactions: 2}
		if id >= 10 {
			producer.Calls, producer.Transactions = 1, 1
		}
		config.Producers = append(config.Producers, producer)
		var phases uint16
		for _, phase := range executionProducerPhases(id) {
			phases |= 1 << (phase - 1)
		}
		wire.Producers = append(wire.Producers, storeaccounting.WireProducer{ID: id, Binding: bindings[id-1], Phases: phases})
	}
	for index, bounds := range plan.WorkEnvelope.Phases {
		config.Phases = append(config.Phases, storeaccounting.Phase{
			ID: uint32(index + 1), Transactions: bounds.StoreTransactions.Maximum, Rows: bounds.StoreRows.Maximum,
		})
	}
	return config, wire, nil
}
