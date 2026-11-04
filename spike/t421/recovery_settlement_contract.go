package t421

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/store"
)

// V7 is a prospective contract correction for the epoch-four selected
// recovery (MED-496 V23). V1-V6 recipes, inventories and retained evidence are
// unchanged. Recovered proves only the selected chunk; whole-schedule
// settlement is a separate later barrier with its own accounted call class Z.
const recoverySettlementPolicy = "recovery-settlement-v7:epoch-four-process_restart-only;" +
	"R-recovered=selected-chunk-done-unleased-stale-priority-reaper-provenance-lease-changed-result-and-authority-fences;" +
	"R-schedule=valid-active-or-settled;R-domain=complete-current-prior-root-or-exactly-unpublished;" +
	"Z=after-R-before-X;exact-recovery-schedule-digest;settled-pending0-running0-failed0-succeeded-total;" +
	"failure-replacement-supersession-or-phase-deadline-refuses;progress-and-readiness-never-substitute;" +
	"F-still-proves-every-root;historical-V1-V6-exact"

const recoverySettlementReadAccounting = ";Z=[1,1+floor(process_restart-deadline/5s)]xC0S[1,64]M0W0;phase-K=prep+X+T+F+L+R+Z+Q"

// recoverySettlementPath is deliberately outside /api/t422/checkpoint/ so the
// exact two-call R subtotal (p2C7S4) is unchanged.
const recoverySettlementPath = "/api/t422/recovery-schedule/settled"

// epochFourInspectionOrdinalMaximum is the retained global per-server report
// ordinal ceiling; V7 epoch-four accounted requests must stay within it.
const epochFourInspectionOrdinalMaximum = 11531

func recoverySettlementPlanSemantics(schema string) bool {
	return schema == PlanV7Schema
}

// BuildPlanV7 constructs an unsealed plan; construction grants no execution.
func BuildPlanV7(sourceCommit string) (Plan, error) {
	plan, err := BuildPlanV6(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	if err = applyRecoverySettlementCorrection(&plan); err != nil {
		return Plan{}, err
	}
	if err = validatePlan(plan, &plan.Revisions); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func applyRecoverySettlementCorrection(plan *Plan) error {
	if plan == nil || plan.Schema != PlanV6Schema || plan.Correction == nil ||
		plan.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV6Schema || plan.ReceiptContract.Schema != ReceiptV6Schema {
		return errors.New("V7 recovery settlement requires complete V6")
	}
	if err := validatePlanExecutionContract(*plan); err != nil {
		return fmt.Errorf("validate V6 recovery-settlement preimage: %w", err)
	}
	correction := *plan.Correction
	const oldPhaseK = ";phase-K=prep+X+T+F+L+R+Q"
	if strings.Count(correction.ReadAccountingPolicy, oldPhaseK+";") != 1 || strings.Count(correction.ReadAccountingPolicy, oldPhaseK) != 1 {
		return errors.New("V7 phase read-accounting preimage changed")
	}
	correction.ReadAccountingPolicy = strings.Replace(correction.ReadAccountingPolicy, oldPhaseK, recoverySettlementReadAccounting, 1)
	correction.RequiredReadiness = append(slices.Clone(correction.RequiredReadiness), recoverySettlementPolicy)
	plan.Schema = PlanV7Schema
	plan.ToolPolicy.ExecutionFreezeSchema = ExecutionFreezeV7Schema
	plan.ReceiptContract.Schema = ReceiptV7Schema
	digest, err := inspectionInventorySHA256(*plan, planTailReadinessTransitions(plan.Schema))
	if err != nil {
		return err
	}
	correction.InspectionInventorySHA256 = digest
	plan.Correction = &correction
	plan.WorkEnvelope.Phases = slices.Clone(plan.WorkEnvelope.Phases)
	return applyCorrectedPhaseReadMaximums(&plan.WorkEnvelope, *plan)
}

// applyRecoverySettlementInventory adds the V7 Z class to process_restart and
// its epoch. Each call is one exact current-schedule read whose store attempts
// include the store's bounded retries.
func applyRecoverySettlementInventory(rows []phaseInspectionInventory, epochs []epochInspectionInventory) error {
	deadlines := frozenPhaseDeadlines()
	for index := range rows {
		row := &rows[index]
		if row.Phase != "process_restart" {
			continue
		}
		if index >= len(deadlines) || deadlines[index].Phase != row.Phase || row.ServerEpoch == 0 {
			return errors.New("V7 recovery settlement phase is invalid")
		}
		calls, err := maximumInspectionAttempts(deadlines[index].DeadlineMS, correctedInspectionPollMS)
		if err != nil {
			return err
		}
		perCall, err := checkedMultiply(store.GenerationScheduleSettlementStoreReadAttempts, store.MaxGenerationScheduleReadAttempts)
		if err != nil {
			return err
		}
		reads, err := checkedMultiply(calls, perCall)
		if err != nil {
			return err
		}
		row.ScheduleSettlementCalls = CounterBound{Minimum: 1, Maximum: calls}
		row.ScheduleSettlementStoreReadAttempts = CounterBound{Minimum: store.GenerationScheduleSettlementStoreReadAttempts, Maximum: reads}
		for epochIndex := range epochs {
			epoch := &epochs[epochIndex]
			if epoch.ServerEpoch != row.ServerEpoch {
				continue
			}
			epoch.ScheduleSettlementCallsMaximum = calls
			epoch.ScheduleSettlementStoreReadAttemptsMaximum = reads
			if epoch.AccountedServerRequestsMaximum, err = checkedInspectionReadSum(epoch.AccountedServerRequestsMaximum, calls); err != nil {
				return err
			}
			return nil
		}
		return errors.New("V7 recovery settlement epoch is absent")
	}
	return errors.New("V7 recovery settlement phase is absent")
}
