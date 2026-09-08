package t421

import (
	"errors"
	"slices"

	"github.com/bmeddeb/phebs/internal/store"
)

// SelectorHandoffCleanupSchema opts a newly authored V3 plan into the bounded
// preimage-only handoff operation. Omission preserves the prior V3 contract.
const SelectorHandoffCleanupSchema = "t422-selector-handoff-cleanup-v1"

// SelectorHandoffCleanupContract is prospective admission, not evidence that
// any cleanup ran. The exact native reports and independent store-accounting
// stream must establish the work actually attempted and committed.
type SelectorHandoffCleanupContract struct {
	Schema                string                        `json:"schema"`
	PlacementPolicy       string                        `json:"placement_policy"`
	OwnershipPolicy       string                        `json:"ownership_policy"`
	AccountingPolicy      string                        `json:"accounting_policy"`
	MaximumDeletesPerTurn uint64                        `json:"maximum_deletes_per_turn"`
	Phases                []SelectorHandoffCleanupPhase `json:"phases"`
}

// SelectorHandoffCleanupPhase separates the fixed-corpus obsolete-inventory
// bound from operation attempts. A native no-op still owns one transaction and
// turn; neither a deletion count nor a successful response proves SDK attempts.
type SelectorHandoffCleanupPhase struct {
	Phase                   string       `json:"phase"`
	ServerEpoch             uint64       `json:"server_epoch"`
	Calls                   CounterBound `json:"calls"`
	PreimageRowsMaximum     uint64       `json:"preimage_rows_maximum"`
	SummaryPreimagesMaximum uint64       `json:"summary_preimages_maximum"`
	OwnerTurns              CounterBound `json:"owner_turns"`
	StoreTransactions       CounterBound `json:"store_transactions"`
	StoreRows               CounterBound `json:"store_rows"`
	LifecycleDeleted        CounterBound `json:"lifecycle_deleted"`
	ControlFileReads        CounterBound `json:"control_file_reads"`
	StoreReadAttempts       CounterBound `json:"store_read_attempts"`
	StoreWriteAttempts      CounterBound `json:"store_write_attempts"`
	MemberVisits            CounterBound `json:"member_visits"`
}

// BuildPlanV3WithSelectorCleanup is a separate prospective constructor. The
// prior BuildPlanV3 and all omitted-policy plans retain their exact semantics.
// Construction is neither a seal nor authority to execute the new operation.
func BuildPlanV3WithSelectorCleanup(sourceCommit string) (Plan, error) {
	plan, err := BuildPlanV3(sourceCommit)
	if err != nil {
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

func applySelectorHandoffCleanupCorrection(plan *Plan) error {
	if plan == nil || plan.Schema != PlanV3Schema || plan.ProcessAccounting == nil ||
		plan.SelectorHandoffCleanup != nil {
		return errors.New("selector handoff cleanup requires an unmodified V3 contract")
	}
	policy, err := selectorHandoffCleanupContract(plan.Profile)
	if err != nil {
		return err
	}
	for _, cleanup := range policy.Phases {
		index := slices.IndexFunc(plan.WorkEnvelope.Phases, func(row PhaseWorkBounds) bool {
			return row.Phase == cleanup.Phase
		})
		if index < 0 {
			return errors.New("selector handoff cleanup phase is absent")
		}
		row := &plan.WorkEnvelope.Phases[index]
		for destination, addition := range map[*CounterBound]CounterBound{
			&row.StoreTransactions:   cleanup.StoreTransactions,
			&row.StoreRows:           cleanup.StoreRows,
			&row.LifecycleOwnerTurns: cleanup.OwnerTurns,
			&row.LifecycleDeleted:    cleanup.LifecycleDeleted,
		} {
			minimum, minErr := checkedInspectionReadSum(destination.Minimum, addition.Minimum)
			maximum, maxErr := checkedInspectionReadSum(destination.Maximum, addition.Maximum)
			if minErr != nil || maxErr != nil {
				return errors.New("selector handoff cleanup work maximum overflows")
			}
			*destination = CounterBound{Minimum: minimum, Maximum: maximum}
		}
	}
	plan.SelectorHandoffCleanup = &policy
	return applyCorrectedPhaseReadMaximums(&plan.WorkEnvelope, *plan)
}

func selectorHandoffCleanupContract(profile CombinedProfile) (SelectorHandoffCleanupContract, error) {
	// These identities are fixed by the combined-corpus contract: no handoff
	// adds/removes a service key, and only logical B changes one display name.
	if profile.Logical.TotalServiceRecords != 10_000 || profile.Logical.AcceptedServices != 10_000 ||
		store.ServiceStateV3PreimageHandoffDeleteLimit != 16 ||
		store.ServiceStateV3PreimageHandoffStoreReadMaximum != 9 ||
		store.ServiceStateV3PreimageHandoffStoreWriteMaximum != 2 {
		return SelectorHandoffCleanupContract{}, errors.New("selector handoff cleanup native derivation differs")
	}
	policy := SelectorHandoffCleanupContract{
		Schema:                SelectorHandoffCleanupSchema,
		PlacementPolicy:       "once-after-accepted-F-and-owner-reader-join-before-request-fence-or-next-selector-mutation;physical-B-after-search-retention-report;phases=cold,physical_delta_b,logical_delta_b,return_a;existing-phase-deadline;no-retry",
		OwnershipPolicy:       "repository-local-summary-snapshot-owner;caller-holds-existing-exclusive-mutation-lock-per-turn-through-transaction-close;full-selected-selector-transaction-read-compare;selected-snapshot-refuses-without-deletion;delete-only-obsolete-state-preimage-rows-and-summary;Done-proves-empty-inventory-in-same-transaction;no-catalog-root-or-member-or-current-state-deletion;no-native-optimistic-write-fence-claim",
		AccountingPolicy:      "one-native-Begin-and-Commit-per-successful-turn-including-empty;actual-read-ledger-and-turn-reports-plus-independent-SA01;submitted-rows=actual-delete-attempt-rows;R=obsolete-rows,q=floor(R/16);R+1-deletes,q+1-turns;full-prefix=7-reads+1-write;final=9-reads+1-write-if-R%16=0-else-2;cold-empty=3-reads+0-writes;zero-member-visits;ceilings-not-measurements",
		MaximumDeletesPerTurn: uint64(store.ServiceStateV3PreimageHandoffDeleteLimit),
	}
	for _, phase := range []struct {
		name                   string
		epoch, rows, summaries uint64
	}{
		{"cold", 1, 0, 0},
		{"physical_delta_b", 1, profile.Logical.TotalServiceRecords, 1},
		{"logical_delta_b", 2, 1, 1},
		{"return_a", 3, profile.Logical.TotalServiceRecords, 1},
	} {
		row, err := selectorHandoffCleanupPhase(phase.name, phase.epoch, phase.rows, phase.summaries)
		if err != nil {
			return SelectorHandoffCleanupContract{}, err
		}
		policy.Phases = append(policy.Phases, row)
	}
	return policy, nil
}

func selectorHandoffCleanupPhase(phase string, epoch, rows, summaries uint64) (SelectorHandoffCleanupPhase, error) {
	if summaries > 1 || summaries == 0 && rows != 0 {
		return SelectorHandoffCleanupPhase{}, errors.New("selector handoff cleanup inventory is invalid")
	}
	limit := uint64(store.ServiceStateV3PreimageHandoffDeleteLimit)
	turns, reads, writes := uint64(1), uint64(3), uint64(0)
	if summaries != 0 {
		full := rows / limit
		var err error
		turns, err = checkedInspectionReadSum(full, 1)
		if err != nil {
			return SelectorHandoffCleanupPhase{}, err
		}
		reads, err = checkedMultiply(full, 7)
		if err != nil {
			return SelectorHandoffCleanupPhase{}, err
		}
		reads, err = checkedInspectionReadSum(reads, 9)
		if err != nil {
			return SelectorHandoffCleanupPhase{}, err
		}
		writes = full
		finalWrites := uint64(1)
		if rows%limit != 0 {
			finalWrites++
		}
		writes, err = checkedInspectionReadSum(writes, finalWrites)
		if err != nil {
			return SelectorHandoffCleanupPhase{}, err
		}
	}
	deleted, err := checkedInspectionReadSum(rows, summaries)
	if err != nil {
		return SelectorHandoffCleanupPhase{}, err
	}
	return SelectorHandoffCleanupPhase{
		Phase: phase, ServerEpoch: epoch, Calls: exactInspectionCalls(1),
		PreimageRowsMaximum: rows, SummaryPreimagesMaximum: summaries,
		OwnerTurns:         CounterBound{Minimum: 1, Maximum: turns},
		StoreTransactions:  CounterBound{Minimum: 1, Maximum: turns},
		StoreRows:          CounterBound{Maximum: deleted},
		LifecycleDeleted:   CounterBound{Maximum: deleted},
		StoreReadAttempts:  CounterBound{Minimum: 3, Maximum: reads},
		StoreWriteAttempts: CounterBound{Maximum: writes},
	}, nil
}

// SelectorHandoffCleanupForPhase looks up admission in an already validated
// plan. It performs no I/O or plan regeneration; callers still validate the
// returned native report and may not treat this bound as measured work.
func SelectorHandoffCleanupForPhase(plan Plan, phase string) (SelectorHandoffCleanupPhase, bool) {
	policy := plan.SelectorHandoffCleanup
	if plan.Schema != PlanV3Schema || policy == nil || policy.Schema != SelectorHandoffCleanupSchema {
		return SelectorHandoffCleanupPhase{}, false
	}
	for _, row := range policy.Phases {
		if row.Phase == phase {
			return row, true
		}
	}
	return SelectorHandoffCleanupPhase{}, false
}
