package t421

import (
	"math"
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
	authority := make(map[string]AuthorityPhaseResult, 3)
	var target, prior string
	for index, phase := range []string{"cold", "physical_delta_b", "return_a"} {
		target = SHA256([]byte("test-target-" + phase))
		// Deliberately different small partition inventories catch deriving all
		// ordinary schedules from the final recovery phase's chunk count.
		partitions := uint64(index + 2)
		if phase == "return_a" {
			partitions = plan.Correction.RecoveryPreparations[0].Chunks
		}
		authority[phase] = AuthorityPhaseResult{Phase: phase, Outcome: "passed", ExtractionRoots: []ExtractionRootResult{
			{GenerationSHA256: target, ApplicablePartitions: 1},
			{GenerationSHA256: target, ApplicablePartitions: partitions - 1},
		}}
		generation := target
		if prior != "" {
			generation = SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + target + "\x00" + prior))
		}
		prior = testExtractionScheduleDigest(t, generation, partitions)
	}
	var values []TransitionResult
	for _, row := range plan.Correction.RecoveryPreparations {
		p := &RecoveryPreparationResult{Phase: row.Phase, TargetGenerationSHA256: target,
			PriorScheduleSHA256: prior, RecoveryScheduleSHA256: SHA256([]byte(row.Phase)), Chunks: row.Chunks}
		values = append(values, TransitionResult{Phase: row.Phase, Outcome: "passed",
			Injections: []InjectionTransition{{Preparation: p}}})
		prior = p.RecoveryScheduleSHA256
	}
	if err := validateRecoveryPreparationLineage(values, authority, plan); err != nil {
		t.Fatal(err)
	}
	t.Run("reject_target_only_return_a_schedule", func(t *testing.T) {
		invalid := append([]TransitionResult(nil), values...)
		changed := *values[0].Injections[0].Preparation
		changed.PriorScheduleSHA256 = testExtractionScheduleDigest(t, target, changed.Chunks)
		invalid[0].Injections = []InjectionTransition{{Preparation: &changed}}
		if validateRecoveryPreparationLineage(invalid, authority, plan) == nil {
			t.Fatal("preparation reset return-A to a fresh schedule instead of following cold and physical B")
		}
	})
	for name, mutate := range map[string]func(map[string]AuthorityPhaseResult){
		"missing_cold": func(values map[string]AuthorityPhaseResult) { delete(values, "cold") },
		"incomplete_physical_b": func(values map[string]AuthorityPhaseResult) {
			value := values["physical_delta_b"]
			value.Outcome = "stopped"
			values["physical_delta_b"] = value
		},
		"empty_return_a": func(values map[string]AuthorityPhaseResult) {
			value := values["return_a"]
			value.ExtractionRoots = nil
			values["return_a"] = value
		},
		"mixed_generation": func(values map[string]AuthorityPhaseResult) {
			values["return_a"].ExtractionRoots[1].GenerationSHA256 = SHA256([]byte("other-generation"))
		},
		"wrong_cold_partitions": func(values map[string]AuthorityPhaseResult) {
			values["cold"].ExtractionRoots[0].ApplicablePartitions++
		},
		"overflow_partitions": func(values map[string]AuthorityPhaseResult) {
			values["return_a"].ExtractionRoots[1].ApplicablePartitions = math.MaxUint64
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := make(map[string]AuthorityPhaseResult, len(authority))
			for phase, value := range authority {
				value.ExtractionRoots = append([]ExtractionRootResult(nil), value.ExtractionRoots...)
				changed[phase] = value
			}
			mutate(changed)
			if validateRecoveryPreparationLineage(values, changed, plan) == nil {
				t.Fatal("invalid ordinary schedule lineage accepted")
			}
		})
	}
	if err := validateRecoveryPreparationLineage(nil, nil, plan); err != nil {
		t.Fatalf("unreached preparation required future ordinary authority: %v", err)
	}
	values[1].Injections[0].Preparation.PriorScheduleSHA256 = values[0].Injections[0].Preparation.PriorScheduleSHA256
	if validateRecoveryPreparationLineage(values, authority, plan) == nil {
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

func testExtractionScheduleDigest(t *testing.T, generation string, partitions uint64) string {
	t.Helper()
	value, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: t401.RepositoryName, Stage: extractionpublication.ScheduleStage, Generation: generation,
		ResourceClass: store.GenerationResourceExtraction, TotalItems: int64(partitions),
		ChunkItems: extractionpublication.ScheduleChunkItems, MaxAttempts: extractionpublication.ScheduleMaxAttempts,
		RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
