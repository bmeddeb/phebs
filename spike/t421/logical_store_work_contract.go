package t421

import (
	"errors"
	"slices"

	"github.com/bmeddeb/phebs/internal/store"
)

// LogicalStoreWorkSchema selects a prospective whole-phase refusal allowance.
// It is not a guarantee that every otherwise valid retry schedule can finish.
const LogicalStoreWorkSchema = "t422-logical-store-work-v1"

type LogicalStoreWorkContract struct {
	Schema               string `json:"schema"`
	Phase                string `json:"phase"`
	ReferencePhase       string `json:"reference_phase"`
	ClaimAttemptsMaximum uint64 `json:"claim_attempts_maximum"`
	BaseTransactions     uint64 `json:"base_transactions"`
	BaseRows             uint64 `json:"base_rows"`
	AccountingPolicy     string `json:"accounting_policy"`
}

// BuildPlanV3WithLogicalStoreWork keeps both prior V3 constructors exact.
// The base logical allowance is replaced before adding selector cleanup.
func BuildPlanV3WithLogicalStoreWork(sourceCommit string) (Plan, error) {
	plan, err := BuildPlanV3(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	if err := applyLogicalStoreWorkCorrection(&plan); err != nil {
		return Plan{}, err
	}
	if err := applySelectorHandoffCleanupCorrection(&plan); err != nil {
		return Plan{}, err
	}
	if err := validatePlan(plan, &plan.Revisions); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func applyLogicalStoreWorkCorrection(plan *Plan) error {
	if plan == nil || plan.Schema != PlanV3Schema || plan.ProcessAccounting == nil ||
		plan.LogicalStoreWork != nil || plan.SelectorHandoffCleanup != nil || store.MaxBoundedJobClaimAttempts != 64 {
		return errors.New("logical store work requires an unmodified V3 contract")
	}
	index := func(phase string) int {
		return slices.IndexFunc(plan.WorkEnvelope.Phases, func(row PhaseWorkBounds) bool { return row.Phase == phase })
	}
	logical, reference := index("logical_delta_b"), index("cold")
	if logical < 0 || reference < 0 {
		return errors.New("logical store work phase is absent")
	}
	base := plan.WorkEnvelope.Phases[reference]
	if base.StoreTransactions.Maximum != 100_000 || base.StoreRows.Maximum != 100_000*512 {
		return errors.New("logical store work reference allowance differs")
	}
	// Reuse the established full-process allowance as a policy choice, not a
	// claim that logical work is dominated by cold work in every interleaving.
	plan.WorkEnvelope.Phases[logical].StoreTransactions.Maximum = base.StoreTransactions.Maximum
	plan.WorkEnvelope.Phases[logical].StoreRows.Maximum = base.StoreRows.Maximum
	plan.LogicalStoreWork = &LogicalStoreWorkContract{
		Schema: LogicalStoreWorkSchema, Phase: "logical_delta_b", ReferencePhase: "cold",
		ClaimAttemptsMaximum: store.MaxBoundedJobClaimAttempts,
		BaseTransactions:     base.StoreTransactions.Maximum, BaseRows: base.StoreRows.Maximum,
		AccountingPolicy: "chosen-existing-full-process-aggregate-refusal-allowance;not-a-successful-work-upper-bound;all-native-attempts-remain-charged:startup,backfill,validation,enqueue,claim,start,settle,handler,generation,heartbeat,retry,selector,retirement;bounded-ordinary-claim-selection=64-per-invocation;exhaustion-returns-to-existing-poll;selector-cleanup-added-separately;512-rows-per-transaction-unchanged",
	}
	return nil
}
