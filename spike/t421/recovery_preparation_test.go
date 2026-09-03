package t421

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

// These small control-projection counterexamples do not model a completed
// corpus. The separate ordinary-pass test supplies native full-corpus IDs.
func TestRecoveryPreparationRejectsMissingWorkAndDestructiveRecipes(t *testing.T) {
	plan := Plan{Schema: PlanV2Schema, Correction: &ContractCorrection{RecoveryPreparations: correctedRecoveryPreparations()}}
	for _, row := range plan.Correction.RecoveryPreparations {
		t.Run(row.Phase, func(t *testing.T) {
			target, prior := SHA256([]byte("test-target")), SHA256([]byte("test-prior"))
			generation := SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + target + "\x00" + prior))
			schedule, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
				Repository: t401.RepositoryName, Stage: extractionpublication.ScheduleStage, Generation: generation,
				ResourceClass: store.GenerationResourceExtraction, TotalItems: int64(row.Chunks), ChunkItems: extractionpublication.ScheduleChunkItems,
				MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
			})
			if err != nil {
				t.Fatal(err)
			}
			authority := AuthorityPhaseResult{AuthorityState: AuthorityState{ExtractionRootsSHA256: SHA256([]byte("test-result-set"))}}
			digest := SHA256([]byte("test-product-authority"))
			p := RecoveryPreparationResult{
				Schema: "t422-native-recovery-preparation-v1", Phase: row.Phase, PrepareEventOrdinal: 2,
				AuthoritySHA256: digest, PreservedRootsSHA256: authority.ExtractionRootsSHA256,
				TargetGenerationSHA256: target, PriorScheduleSHA256: prior, RecoveryGenerationSHA256: generation, RecoveryScheduleSHA256: schedule,
				ScheduleWrites: row.ScheduleWrites, Chunks: row.Chunks, Starts: row.Starts, Successes: row.Successes, Requeues: row.Requeues,
				PreparationCompletionWrites: row.PreparationCompletionWrites, PreparationDeletes: row.PreparationDeletes,
				RecoveryCompletionWrites: row.RecoveryCompletionWrites, RecoveryRootInstalls: row.RecoveryRootInstalls,
				PublicationCalls: row.PublicationCalls.Minimum, StoreAuthorityUnchanged: true, PreparedBeforeArm: true, DirectoriesSynced: true, LocksReleasedBeforeWait: true,
			}
			value := InjectionTransition{Target: InjectionTargetProjection{Phase: row.Phase, GenerationSHA256: target, ScheduleSHA256: schedule},
				ArmEventOrdinal: 3, AuthorityBeforeSHA256: digest, AuthorityAfterSHA256: digest, Preparation: &p}
			metrics := ReceiptMetrics{PublicationWrites: CountMetric(p.PublicationCalls), JobAttempts: CountMetric(p.Starts), StoreTransactions: CountMetric(p.ScheduleWrites)}
			if err := validateRecoveryPreparation(value, authority, metrics, 1, plan); err != nil {
				t.Fatal(err)
			}
			for name, mutate := range map[string]func(*RecoveryPreparationResult){
				"no_schedule_write":         func(p *RecoveryPreparationResult) { p.ScheduleWrites = 0 },
				"missing_preparation_event": func(p *RecoveryPreparationResult) { p.PrepareEventOrdinal = 0 },
				"late_preparation":          func(p *RecoveryPreparationResult) { p.PrepareEventOrdinal = value.ArmEventOrdinal },
				"missing_chunks":            func(p *RecoveryPreparationResult) { p.Chunks-- },
				"missing_recovery_start":    func(p *RecoveryPreparationResult) { p.Starts-- },
				"job_reset":                 func(p *RecoveryPreparationResult) { p.OldJobsReset = 1 },
				"result_removal":            func(p *RecoveryPreparationResult) { p.ResultFilesRemoved = 1 },
				"sealed_evidence_removal":   func(p *RecoveryPreparationResult) { p.EvidenceRowsRemoved = 1 },
				"extra_control_deletion":    func(p *RecoveryPreparationResult) { p.PreparationDeletes++ },
				"changed_product_authority": func(p *RecoveryPreparationResult) { p.StoreAuthorityUnchanged = false },
				"locks_held_while_waiting":  func(p *RecoveryPreparationResult) { p.LocksReleasedBeforeWait = false },
				"invented_schedule":         func(p *RecoveryPreparationResult) { p.RecoveryScheduleSHA256 = prior },
			} {
				t.Run(name, func(t *testing.T) {
					changed := p
					mutate(&changed)
					invalid := value
					invalid.Preparation = &changed
					if validateRecoveryPreparation(invalid, authority, metrics, 1, plan) == nil {
						t.Fatal("invalid preparation accepted")
					}
				})
			}
			missing := value
			missing.Preparation = nil
			if validateRecoveryPreparation(missing, authority, metrics, 1, plan) == nil {
				t.Fatal("missing preparation accepted")
			}
			if validateRecoveryPreparation(value, authority, ReceiptMetrics{}, 1, plan) == nil {
				t.Fatal("unmetered preparation accepted")
			}
			if validateRecoveryPreparation(value, authority, metrics, 1, Plan{Schema: PlanSchema}) == nil {
				t.Fatal("v1 accepted prospective preparation")
			}
		})
	}
}

func TestRecoveryPreparationLineageAndReportBinding(t *testing.T) {
	plan := Plan{Schema: PlanV2Schema, Correction: &ContractCorrection{RecoveryPreparations: correctedRecoveryPreparations()}}
	target := SHA256([]byte("test-target"))
	prior, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: t401.RepositoryName, Stage: extractionpublication.ScheduleStage, Generation: target,
		ResourceClass: store.GenerationResourceExtraction, TotalItems: int64(plan.Correction.RecoveryPreparations[0].Chunks),
		ChunkItems: extractionpublication.ScheduleChunkItems, MaxAttempts: extractionpublication.ScheduleMaxAttempts,
		RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	var values []TransitionResult
	for _, row := range plan.Correction.RecoveryPreparations {
		p := &RecoveryPreparationResult{Phase: row.Phase, TargetGenerationSHA256: target,
			PriorScheduleSHA256: prior, RecoveryScheduleSHA256: SHA256([]byte(row.Phase))}
		values = append(values, TransitionResult{Phase: row.Phase, Outcome: "passed",
			Injections: []InjectionTransition{{Preparation: p}}})
		prior = p.RecoveryScheduleSHA256
	}
	if err := validateRecoveryPreparationLineage(values, plan); err != nil {
		t.Fatal(err)
	}
	values[1].Injections[0].Preparation.PriorScheduleSHA256 = values[0].Injections[0].Preparation.PriorScheduleSHA256
	if validateRecoveryPreparationLineage(values, plan) == nil {
		t.Fatal("second preparation reused the pre-stale schedule")
	}
	value := values[0].Injections[0]
	hit, err := injectionHitReportSHA256(value, FailurePoint{})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := injectionRecoveryProjectionSHA256(value, FailurePoint{})
	if err != nil {
		t.Fatal(err)
	}
	value.Preparation.PublicationCalls++
	laterHit, _ := injectionHitReportSHA256(value, FailurePoint{})
	laterRecovery, _ := injectionRecoveryProjectionSHA256(value, FailurePoint{})
	if laterHit != hit || laterRecovery == recovery {
		t.Fatal("hit report included future recovery work or recovery omitted it")
	}
	value.Preparation.PreparationDeletes++
	changedHit, _ := injectionHitReportSHA256(value, FailurePoint{})
	if changedHit == hit {
		t.Fatal("hit report did not bind preparation mutations")
	}
}
